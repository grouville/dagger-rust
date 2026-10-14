package replay

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	dagger "dagger.io/dagger/core"
	"github.com/grouville/dagger-rust/compiler/model"
	"github.com/grouville/dagger-rust/compiler/worker"
)

const historyVersion = 1
const historyEntriesPerAction = 4

// HistoryReference is only a local pointer. The referenced Directory contains
// the complete, portable history: metadata, compiler outputs and rustc state.
type HistoryReference struct {
	Version        int    `json:"version"`
	DirectoryID    string `json:"directory_id"`
	ManifestDigest string `json:"manifest_digest"`
}

var ErrHistoryUnavailable = errors.New("native build history is unavailable")

// Load validates the local handle before using it. Result IDs are engine-local:
// a different engine can reuse the same numeric ID for an unrelated directory.
// Missing/evicted history is an acceleration miss, not a reason to fail a build.
func (ref HistoryReference) Load(ctx context.Context, client *dagger.Query) (*dagger.Directory, error) {
	if ref.Version != historyVersion || ref.DirectoryID == "" {
		return nil, fmt.Errorf("invalid native history reference")
	}
	directory := dagger.Ref[*dagger.Directory](client, dagger.ID(ref.DirectoryID))
	contents, err := directory.File("manifest.json").Contents(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrHistoryUnavailable, err)
	}
	digest := sha256.Sum256([]byte(contents))
	if ref.ManifestDigest == "" || ref.ManifestDigest != hex.EncodeToString(digest[:]) {
		return nil, fmt.Errorf("%w: local handle no longer matches its history manifest", ErrHistoryUnavailable)
	}
	return directory, nil
}

func (g *Graph) HistoryReference(ctx context.Context) (HistoryReference, error) {
	if g.History == nil {
		return HistoryReference{}, fmt.Errorf("graph has no automatic history")
	}
	contents, err := g.History.File("manifest.json").Contents(ctx)
	if err != nil {
		return HistoryReference{}, err
	}
	id, err := g.History.ID(ctx)
	if err != nil {
		return HistoryReference{}, err
	}
	digest := sha256.Sum256([]byte(contents))
	return HistoryReference{Version: historyVersion, DirectoryID: string(id), ManifestDigest: hex.EncodeToString(digest[:])}, nil
}

type historyEntry struct {
	Key           string `json:"key"`
	Compatibility string `json:"compatibility"`
	Batch         string `json:"batch,omitempty"`
}

type historyManifest struct {
	Version      int                       `json:"version"`
	Actions      map[string][]historyEntry `json:"actions"`
	CurrentBatch string                    `json:"current_batch,omitempty"`
}

type retainedResult struct {
	target *dagger.Directory
	state  *dagger.Directory
	stamp  *dagger.File
}

type automaticInputs struct {
	hits        map[string]*retainedResult
	seeds       map[string]*dagger.Directory
	artifacts   *dagger.Directory
	batchSeeds  []batchSeed
	batchSeeded map[string]bool
}

type batchSeed struct {
	directory *dagger.Directory
	include   []string
}

func (entry historyEntry) result(directory func(string) *dagger.Directory, action model.Action, outputs []string, client *dagger.Query) *retainedResult {
	if entry.Batch == "" {
		bundle := directory("results/" + entry.Key)
		return &retainedResult{target: bundle.Directory("target"), state: bundle.Directory("incremental"), stamp: bundle.File("execution")}
	}
	bundle := directory("batches/" + entry.Batch)
	return &retainedResult{
		target: client.Directory().WithDirectory(".", bundle.Directory("target"), dagger.DirectoryWithDirectoryOpts{Include: outputs}),
		state:  bundle.Directory("incremental/" + worker.Token(action.ID)),
		stamp:  bundle.File("actions/" + worker.Token(action.ID) + "/execution"),
	}
}

func historyDigest(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func targetRelative(filename string) string {
	if filename == model.TargetRoot {
		return "."
	}
	return strings.TrimPrefix(filename, model.TargetRoot+"/")
}

// BuildAutomatic selects a finished result before choosing an incremental seed.
// Seeds stay ordinary exec inputs. Exact reuse comes from a separate native
// history index keyed by compiler configuration, sources and dependency keys;
// no exec input is removed from Dagger's cache identity.
//
// Previous must be history produced by this builder, not an arbitrary directory
// claiming to contain compiler results. Callers retain/transfer History as a
// native Directory. A local ID file alone is neither portable nor a remote index.
func BuildAutomatic(ctx context.Context, client *dagger.Query, plan *model.Plan, source Source, opts BuildOptions, previous *dagger.Directory) (*Graph, error) {
	if err := validateBuild(plan, source, opts); err != nil {
		return nil, err
	}
	if opts.IncrementalCrate != "" || opts.IncrementalSeed != nil || opts.ArtifactLeafFiles != 0 {
		return nil, fmt.Errorf("automatic history cannot be combined with an explicit seed or file bundles")
	}
	opts.ArtifactDirectories = true
	old := historyManifest{Version: historyVersion, Actions: map[string][]historyEntry{}}
	var lookup func(string) *dagger.Directory
	if previous != nil {
		lookup = previous.Directory
	}
	var previousContents string
	if opts.nativeCache != nil {
		if previous != nil {
			return nil, fmt.Errorf("cannot combine directory history and object history")
		}
		lookup = opts.nativeCache.directory
		previousContents = opts.nativeCache.Manifest
	} else if previous != nil {
		var err error
		previousContents, err = previous.File("manifest.json").Contents(ctx)
		if err != nil {
			return nil, fmt.Errorf("load native build history: %w", err)
		}
	}
	if previous != nil || previousContents != "" {
		contents := previousContents
		if err := json.Unmarshal([]byte(contents), &old); err != nil {
			return nil, fmt.Errorf("decode native build history: %w", err)
		}
		if err := old.validate(); err != nil {
			return nil, err
		}
	}
	if opts.nativeCache != nil {
		if err := opts.nativeCache.validate(old); err != nil {
			return nil, err
		}
	}
	var workerDigest string
	if opts.BatchCompiler != nil {
		var err error
		workerDigest, err = opts.BatchCompiler.Digest(ctx)
		if err != nil {
			return nil, err
		}
	}
	configuration, err := historyDigest([]any{recipeConfiguration(plan), workerDigest, opts.BatchWorkers, opts.BatchNoPipelining})
	if err != nil {
		return nil, err
	}
	actions, err := ordered(plan.Actions)
	if err != nil {
		return nil, err
	}
	sources := source.packageSources(plan.Packages)
	sourceKeys := map[string]string{}
	if opts.SourceDirectory != nil {
		id, err := opts.SourceDirectory.ID(ctx)
		if err != nil {
			return nil, err
		}
		opts.SourceDirectory = dagger.Ref[*dagger.Directory](client, dagger.ID(id))
		// Hash the actual immutable inputs, rather than trusting a caller's
		// separate Source map to describe the imported snapshot.
		sourceKeys, err = packageDigests(ctx, client, id, plan.Packages, opts.SourceDigestPaths)
		if err != nil {
			return nil, err
		}
	} else {
		for root, files := range sources {
			sourceKeys[root] = files.ContentDigest()
		}
	}
	inputs := &automaticInputs{hits: map[string]*retainedResult{}, seeds: map[string]*dagger.Directory{}, batchSeeded: map[string]bool{}}
	seedGroups := map[string][]string{}
	if old.CurrentBatch != "" {
		inputs.artifacts = lookup("batches/" + old.CurrentBatch + "/target")
	}
	var overlays []*dagger.Directory
	var missingOutputs []string
	closures := map[string][]string{}
	keys := map[string]string{}
	next := historyManifest{Version: historyVersion, Actions: map[string][]historyEntry{}}
	for _, action := range actions {
		outputSet := map[string]bool{}
		for _, output := range action.Outputs {
			outputSet[targetRelative(output)] = true
		}
		for _, dep := range action.Dependencies {
			for _, output := range closures[dep] {
				outputSet[output] = true
			}
		}
		closures[action.ID] = sortedKeys(outputSet)
		compatibility, err := historyDigest([]any{historyVersion, configuration, action.ID, opts.SourceDirectory != nil})
		if err != nil {
			return nil, err
		}
		dependencies := map[string]string{}
		for _, id := range action.Dependencies {
			dependencies[id] = keys[id]
		}
		key, err := historyDigest([]any{compatibility, sourceKeys[action.PackageRoot], dependencies})
		if err != nil {
			return nil, err
		}
		keys[action.ID] = key
		entries := old.Actions[action.ID]
		selectedEntry := historyEntry{Key: key, Compatibility: compatibility}
		for _, entry := range entries {
			if entry.Key == key && entry.Compatibility == compatibility {
				inputs.hits[action.ID] = entry.result(lookup, action, closures[action.ID], client)
				selectedEntry = entry
				if inputs.artifacts != nil && entries[0].Key != key {
					overlays = append(overlays, inputs.hits[action.ID].target)
				}
				break
			}
		}
		if inputs.hits[action.ID] == nil {
			for _, entry := range entries {
				if entry.Compatibility == compatibility {
					inputs.seeds[action.ID] = entry.result(lookup, action, closures[action.ID], client).state
					if entry.Batch != "" && opts.BatchCompiler != nil {
						seedGroups[entry.Batch] = append(seedGroups[entry.Batch], worker.Token(action.ID)+"/**")
						inputs.batchSeeded[action.ID] = true
					}
					break
				}
			}
			for _, output := range action.Outputs {
				missingOutputs = append(missingOutputs, targetRelative(output))
			}
		}
		selected := []historyEntry{selectedEntry}
		for _, entry := range entries {
			if entry.Key != key && len(selected) < historyEntriesPerAction {
				selected = append(selected, entry)
			}
		}
		next.Actions[action.ID] = selected
	}
	for _, group := range sortedKeys(seedGroups) {
		inputs.batchSeeds = append(inputs.batchSeeds, batchSeed{
			directory: lookup("batches/" + group + "/incremental"),
			include:   seedGroups[group],
		})
	}
	if inputs.artifacts != nil && len(missingOutputs) > 0 {
		for _, overlay := range overlays {
			inputs.artifacts = inputs.artifacts.WithDirectory(".", overlay)
		}
		inputs.artifacts = client.Directory().WithDirectory(".", inputs.artifacts,
			dagger.DirectoryWithDirectoryOpts{Exclude: missingOutputs})
		if len(inputs.hits) == 0 {
			inputs.artifacts = client.Directory()
		}
	}
	opts.automatic = inputs
	graph, err := BuildWithOptions(client, plan, source, opts)
	if err != nil {
		return nil, err
	}
	var batchID string
	if graph.batchSnapshot != nil {
		batchID, err = historyDigest(rand.Text())
		if err != nil {
			return nil, err
		}
		next.CurrentBatch = batchID
		for _, action := range actions {
			if inputs.hits[action.ID] == nil {
				next.Actions[action.ID][0].Batch = batchID
			}
		}
	} else if old.CurrentBatch != "" && len(missingOutputs) == 0 && len(overlays) == 0 {
		next.CurrentBatch = old.CurrentBatch
	}
	keep := map[string]bool{}
	keepBatches := map[string]bool{}
	for _, entries := range next.Actions {
		for _, entry := range entries {
			keep[entry.Key] = true
			if entry.Batch != "" {
				keepBatches[entry.Batch] = true
			}
		}
	}
	if opts.nativeCache != nil {
		contents, err := json.Marshal(next)
		if err != nil {
			return nil, err
		}
		cache := &NativeCache{Manifest: string(contents)}
		for _, id := range sortedKeys(keepBatches) {
			var snapshot *dagger.Directory
			if id == batchID {
				snapshot = graph.batchSnapshot
			} else {
				snapshot = opts.nativeCache.snapshots[id]
			}
			cache.Batches = append(cache.Batches, NativeCacheBatch{ID: id, Snapshot: snapshot})
		}
		graph.Cache = cache
		return graph, nil
	}
	var exclude []string
	for _, entries := range old.Actions {
		for _, entry := range entries {
			if entry.Batch != "" && !keepBatches[entry.Batch] {
				exclude = append(exclude, "batches/"+entry.Batch)
			} else if entry.Batch == "" && !keep[entry.Key] {
				exclude = append(exclude, "results/"+entry.Key)
			}
		}
	}
	slices.Sort(exclude)
	exclude = slices.Compact(exclude)
	history := client.Directory()
	if previous != nil {
		if len(exclude) == 0 {
			history = previous
		} else if len(exclude) == 1 {
			// Steady edits usually expire one version/batch. Remove it from
			// a snapshot of the previous history instead of copying all the
			// remaining compiler states into a new filtered directory.
			history = previous.WithoutDirectory(exclude[0])
		} else {
			history = history.WithDirectory(".", previous, dagger.DirectoryWithDirectoryOpts{Exclude: exclude})
		}
	}
	var updates []*dagger.Directory
	if graph.batchSnapshot != nil {
		history = history.WithDirectory("batches/"+batchID, graph.batchSnapshot)
	}
	for _, action := range actions {
		if inputs.hits[action.ID] == nil && graph.batchSnapshot == nil {
			updates = append(updates, client.Directory().WithDirectory("results/"+keys[action.ID], graph.results[action.ID]))
		}
	}
	if len(updates) > 0 {
		history = history.WithDirectory(".", mergeArtifactDirectories(updates))
	}
	contents, err := json.Marshal(next)
	if err != nil {
		return nil, err
	}
	if previous != nil && graph.batchSnapshot == nil && len(updates) == 0 && len(exclude) == 0 {
		oldContents, err := json.Marshal(old)
		if err != nil {
			return nil, err
		}
		if string(oldContents) == string(contents) {
			graph.History = previous
			return graph, nil
		}
	}
	graph.History = history.WithNewFile("manifest.json", string(contents), dagger.DirectoryWithNewFileOpts{Permissions: 0600})
	return graph, nil
}

func (h historyManifest) validate() error {
	if h.Version != historyVersion || h.Actions == nil {
		return fmt.Errorf("unsupported native history format")
	}
	seen := map[string]bool{}
	if h.CurrentBatch != "" {
		decoded, err := hex.DecodeString(h.CurrentBatch)
		if err != nil || len(decoded) != sha256.Size || hex.EncodeToString(decoded) != h.CurrentBatch {
			return fmt.Errorf("invalid current native history batch")
		}
	}
	for id, entries := range h.Actions {
		if id == "" || len(entries) == 0 || len(entries) > historyEntriesPerAction {
			return fmt.Errorf("invalid native history action %q", id)
		}
		for _, entry := range entries {
			values := []string{entry.Key, entry.Compatibility}
			if entry.Batch != "" {
				values = append(values, entry.Batch)
			}
			for _, value := range values {
				decoded, err := hex.DecodeString(value)
				if err != nil || len(decoded) != sha256.Size || hex.EncodeToString(decoded) != value {
					return fmt.Errorf("invalid native history digest for %q", id)
				}
			}
			if seen[entry.Key] {
				return fmt.Errorf("duplicate native history result for %q", id)
			}
			seen[entry.Key] = true
		}
	}
	return nil
}
