// Package replay turns recorded compiler invocations into native Dagger operations.
package replay

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"slices"
	"strings"
	"sync"
	"time"

	dagger "dagger.io/dagger/core"
	"github.com/grouville/dagger-rust/compiler/model"
	"github.com/grouville/dagger-rust/compiler/worker"
	"golang.org/x/sync/errgroup"
)

// Keep diagnostics in a small directory. Engines without the contenthash file
// fast path walk a file's parent; a marker at / scans the entire toolchain image.
const stampPath = "/rcexp-meta/execution"

type Operation struct {
	Action    model.Action
	Container *dagger.Container
	Files     map[string]*dagger.File
	Stamp     *dagger.File
}

type Graph struct {
	Operations    []Operation
	Artifacts     *dagger.Directory
	Incremental   *dagger.Directory
	History       *dagger.Directory
	Cache         *NativeCache
	HistoryHits   int
	BatchTrace    *dagger.File
	batchSnapshot *dagger.Directory
	results       map[string]*dagger.Directory
}

// BuildOptions exposes incremental-state and artifact-layout experiments. The
// seed remains an ordinary cache-key input; this does not implement cache hints.
type BuildOptions struct {
	IncrementalCrate    string
	IncrementalSeed     *dagger.Directory
	ArtifactLeafFiles   int
	ArtifactDirectories bool
	// SourceDirectory supplies an already imported workspace snapshot. Source
	// still validates the captured configuration and supported project boundary.
	SourceDirectory *dagger.Directory
	// SourceDigestPaths requires the experimental Directory.digest(path:) API.
	SourceDigestPaths bool
	// BatchCompiler is the rcexp executable, used to schedule cache misses in
	// one container. Automatic history still retains results separately.
	BatchCompiler     *dagger.File
	BatchWorkers      int
	BatchNoPipelining bool
	automatic         *automaticInputs
	nativeCache       *NativeCache
}

type ActionReport struct {
	ID            string            `json:"id"`
	Crate         string            `json:"crate"`
	Execution     string            `json:"execution"`
	Digests       map[string]string `json:"digests"`
	DemandSeconds float64           `json:"demand_seconds"`
	DigestSeconds float64           `json:"digest_seconds"`
}

type Report struct {
	ReplaySeconds float64         `json:"replay_seconds"`
	Actions       []ActionReport  `json:"actions"`
	Driver        *DriverTimings  `json:"driver,omitempty"`
	Batch         []worker.Timing `json:"batch,omitempty"`
}

// DriverTimings separates CLI setup and output transfer from graph evaluation.
// ReadySeconds ends when artifacts and retained incremental state are ready;
// process shutdown is measured externally with the full user command.
// ExportSeconds and HistorySeconds are concurrent demands, not additive phases.
type DriverTimings struct {
	SourceSeconds      float64 `json:"source_seconds"`
	ConnectSeconds     float64 `json:"connect_seconds"`
	GraphSeconds       float64 `json:"graph_seconds"`
	DemandSeconds      float64 `json:"demand_seconds,omitempty"`
	ExportSeconds      float64 `json:"export_seconds"`
	IncrementalSeconds float64 `json:"incremental_seconds,omitempty"`
	HistorySeconds     float64 `json:"history_seconds,omitempty"`
	ReadySeconds       float64 `json:"ready_seconds"`
}

// Build creates only recipes. The caller chooses when and on which engine to demand them.
func Build(client *dagger.Query, plan *model.Plan, source Source) (*Graph, error) {
	return BuildWithOptions(client, plan, source, BuildOptions{})
}

func BuildWithOptions(client *dagger.Query, plan *model.Plan, source Source, opts BuildOptions) (*Graph, error) {
	if err := Validate(plan, source); err != nil {
		return nil, err
	}
	if opts.BatchCompiler != nil {
		if opts.automatic == nil {
			return nil, fmt.Errorf("batch compilation requires automatic native history")
		}
		return buildBatch(client, plan, source, opts)
	}
	if opts.IncrementalSeed != nil && opts.IncrementalCrate == "" {
		return nil, fmt.Errorf("incremental seed requires a crate")
	}
	if opts.ArtifactLeafFiles < 0 {
		return nil, fmt.Errorf("artifact leaf size must be nonnegative")
	}
	if opts.ArtifactDirectories && opts.ArtifactLeafFiles != 0 {
		return nil, fmt.Errorf("artifact directories cannot be combined with file leaves")
	}
	if opts.IncrementalCrate != "" {
		if _, err := SeedCompatibility(plan, opts.IncrementalCrate); err != nil {
			return nil, err
		}
	}
	actions, err := ordered(plan.Actions)
	if err != nil {
		return nil, err
	}
	base := client.Container(dagger.ContainerOpts{Platform: "linux/amd64"}).From(plan.Image)
	graph := &Graph{Artifacts: client.Directory(), results: map[string]*dagger.Directory{}}
	byID := map[string]Operation{}
	artifacts := map[string][]*dagger.File{}
	nativeDirs := map[string]map[string]*dagger.Directory{}
	nativeOutputs := map[string]map[string]bool{}
	usedAsDependency := map[string]bool{}
	packageSources := source.packageSources(plan.Packages)
	packageDirs := map[string]*dagger.Directory{}
	if opts.SourceDirectory != nil {
		packageDirs = PackageDirectories(opts.SourceDirectory, plan.Packages)
	}
	for _, a := range actions {
		if packageDirs[a.PackageRoot] == nil {
			packageDirs[a.PackageRoot] = packageSources[a.PackageRoot].Directory(client)
		}
		ctr := base.WithMountedDirectory(a.PackageRoot, packageDirs[a.PackageRoot]).WithWorkdir(a.Cwd)
		dependencyDirs := map[string][]*dagger.Directory{}
		for _, dep := range a.Dependencies {
			usedAsDependency[dep] = true
			if opts.ArtifactDirectories {
				for dir, directory := range nativeDirs[dep] {
					dependencyDirs[dir] = append(dependencyDirs[dir], directory)
				}
			}
		}
		if opts.ArtifactDirectories {
			for _, dir := range sortedKeys(dependencyDirs) {
				ctr = ctr.WithMountedDirectory(dir, mergeArtifactDirectories(dependencyDirs[dir]))
			}
		} else {
			// rustc discovers indirect dependencies through -L as well as --extern.
			closure := map[string]bool{}
			var collect func(string)
			collect = func(id string) {
				if closure[id] {
					return
				}
				closure[id] = true
				for _, dep := range byID[id].Action.Dependencies {
					collect(dep)
				}
			}
			for _, dep := range a.Dependencies {
				collect(dep)
			}
			bundles := map[string][]*dagger.File{}
			for _, id := range sortedKeys(closure) {
				op := byID[id]
				for _, filename := range sortedKeys(op.Files) {
					// dep-info is bookkeeping, not a compiler dependency.
					if path.Ext(filename) == ".d" {
						continue
					}
					bundles[path.Dir(filename)] = append(bundles[path.Dir(filename)], op.Files[filename])
				}
			}
			for _, dir := range sortedKeys(bundles) {
				ctr = ctr.WithMountedDirectory(dir, filesDirectory(client, bundles[dir], opts.ArtifactLeafFiles))
			}
		}
		// The nonce is an output, never an argument or a dependency input.
		cmd := []string{"sh", "-ec", "mkdir -p \"$1\" " + path.Dir(stampPath) + "; shift; cat /proc/sys/kernel/random/uuid > " + stampPath + "; exec \"$@\"", "rcexp", model.Option(a.Args, "--out-dir"), a.Compiler}
		cmd = append(cmd, a.Args...)
		var retained *retainedResult
		if opts.automatic != nil {
			retained = opts.automatic.hits[a.ID]
		}
		if retained == nil && (a.Crate == opts.IncrementalCrate || opts.automatic != nil) {
			seed := opts.IncrementalSeed
			if opts.automatic != nil {
				seed = opts.automatic.seeds[a.ID]
			}
			if seed == nil {
				seed = client.Directory()
			}
			ctr = ctr.WithMountedDirectory(incrementalPath, seed)
			cmd = append(cmd, "-C", "incremental="+incrementalPath)
		}
		if retained == nil {
			ctr = ctr.WithExec(compilerEnvironment(a, cmd), noNesting())
		}
		if a.Crate == opts.IncrementalCrate {
			// Retain a standalone native Directory result. Container.directory
			// is a transient projection whose runtime handle can disappear when
			// the session closes; withDirectory is a persistable operation.
			graph.Incremental = client.Directory().WithDirectory(".", ctr.Directory(incrementalPath))
		}
		op := Operation{Action: a, Container: ctr, Files: map[string]*dagger.File{}, Stamp: ctr.File(stampPath)}
		outputDirectory := ctr.Directory
		outputFile := ctr.File
		if retained != nil {
			graph.HistoryHits++
			op.Container = nil
			op.Stamp = retained.stamp
			outputDirectory = func(dir string, _ ...dagger.ContainerDirectoryOpts) *dagger.Directory {
				return retained.target.Directory(targetRelative(dir))
			}
			outputFile = func(filename string, _ ...dagger.ContainerFileOpts) *dagger.File {
				return retained.target.File(targetRelative(filename))
			}
		}
		outputDirs := map[string]bool{}
		for dir := range dependencyDirs {
			outputDirs[dir] = true
		}
		for _, filename := range a.Outputs {
			file := outputFile(filename)
			op.Files[filename] = file
			dir := strings.TrimPrefix(path.Dir(filename), model.TargetRoot+"/")
			if dir == model.TargetRoot {
				dir = "."
			}
			if opts.ArtifactDirectories {
				absoluteDir := path.Dir(filename)
				outputDirs[absoluteDir] = true
				if nativeOutputs[absoluteDir] == nil {
					nativeOutputs[absoluteDir] = map[string]bool{}
				}
				nativeOutputs[absoluteDir][path.Base(filename)] = true
			} else {
				artifacts[dir] = append(artifacts[dir], file)
			}
		}
		if opts.ArtifactDirectories {
			nativeDirs[a.ID] = map[string]*dagger.Directory{}
			for _, dir := range sortedKeys(outputDirs) {
				nativeDirs[a.ID][dir] = outputDirectory(dir)
			}
		}
		if opts.automatic != nil {
			if retained == nil {
				bundle := client.Directory().WithFile("execution", op.Stamp).
					WithDirectory("incremental", ctr.Directory(incrementalPath))
				for _, dir := range sortedKeys(nativeDirs[a.ID]) {
					relative := strings.TrimPrefix(dir, model.TargetRoot+"/")
					if dir == model.TargetRoot {
						relative = "."
					}
					bundle = bundle.WithDirectory(path.Join("target", relative), nativeDirs[a.ID][dir])
				}
				graph.results[a.ID] = bundle
			}
		}
		byID[a.ID] = op
		graph.Operations = append(graph.Operations, op)
	}
	if opts.ArtifactDirectories {
		sinks := map[string][]*dagger.Directory{}
		for _, action := range actions {
			if usedAsDependency[action.ID] {
				continue
			}
			for dir, directory := range nativeDirs[action.ID] {
				sinks[dir] = append(sinks[dir], directory)
			}
		}
		for _, dir := range sortedKeys(sinks) {
			relative := strings.TrimPrefix(dir, model.TargetRoot+"/")
			if relative == model.TargetRoot {
				relative = "."
			}
			// Export exactly the captured artifact set, including dep-info.
			// Whole dependency mounts also carry dep-info between compilers.
			graph.Artifacts = graph.Artifacts.WithDirectory(relative, mergeArtifactDirectories(sinks[dir]),
				dagger.DirectoryWithDirectoryOpts{Include: sortedKeys(nativeOutputs[dir])})
		}
		return graph, nil
	}
	for _, dir := range sortedKeys(artifacts) {
		if opts.ArtifactLeafFiles == 0 || len(artifacts[dir]) <= opts.ArtifactLeafFiles {
			graph.Artifacts = graph.Artifacts.WithFiles(dir, artifacts[dir])
		} else {
			graph.Artifacts = graph.Artifacts.WithDirectory(dir, filesDirectory(client, artifacts[dir], opts.ArtifactLeafFiles))
		}
	}
	return graph, nil
}

// Bound the chains expanded by Directory.withFiles. Merging left before right
// preserves the original later-file-wins behavior for duplicate basenames.
func filesDirectory(client *dagger.Query, files []*dagger.File, leafSize int) *dagger.Directory {
	if leafSize == 0 || len(files) <= leafSize {
		return client.Directory().WithFiles(".", files)
	}
	middle := len(files) / 2
	return client.Directory().
		WithDirectory(".", filesDirectory(client, files[:middle], leafSize)).
		WithDirectory(".", filesDirectory(client, files[middle:], leafSize))
}

// DemandFilesystem bounds actual compiler demand without reading exec metadata
// or hashing artifacts. Each action completes before its dependents start.
func (g *Graph) DemandFilesystem(ctx context.Context, concurrency int) error {
	if concurrency < 1 {
		return fmt.Errorf("compiler concurrency must be positive")
	}
	group, ctx := errgroup.WithContext(ctx)
	semaphore := make(chan struct{}, concurrency)
	done := map[string]chan struct{}{}
	for _, op := range g.Operations {
		done[op.Action.ID] = make(chan struct{})
	}
	for _, op := range g.Operations {
		group.Go(func() error {
			for _, dep := range op.Action.Dependencies {
				select {
				case <-done[dep]:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			select {
			case semaphore <- struct{}{}:
			case <-ctx.Done():
				return ctx.Err()
			}
			defer func() { <-semaphore }()
			// Demand the root-filesystem marker first, as Evaluate does. Starting
			// with an artifact mount's Size caused an app to reexecute after a
			// filesystem-only transfer in the experimental balanced layout.
			if _, err := op.Stamp.Contents(ctx); err != nil {
				return err
			}
			close(done[op.Action.ID])
			return nil
		})
	}
	return group.Wait()
}

// Keep environment values in the exec's cache identity without adding a
// container recipe step for every variable. Values are argv, never shell source.
// The pinned toolchain supplies GNU env, including its -u option.
func compilerEnvironment(a model.Action, command []string) []string {
	args := []string{"/usr/bin/env"}
	unset := map[string]bool{}
	for _, key := range a.UnsetEnv {
		args = append(args, "-u", key)
		unset[key] = true
	}
	args = append(args, "--")
	for _, key := range sortedKeys(a.Env) {
		if !unset[key] {
			args = append(args, key+"="+a.Env[key])
		}
	}
	return append(args, command...)
}

// Evaluate reads filesystem outputs only. Reading exec metadata (Sync/Stdout)
// could run a compiler again when only its filesystem was remotely transferred.
func (g *Graph) Evaluate(ctx context.Context, concurrency int) (*Report, error) {
	if concurrency < 1 {
		return nil, fmt.Errorf("concurrency must be positive")
	}
	started := time.Now()
	report := &Report{Actions: make([]ActionReport, len(g.Operations))}
	group, actionCtx := errgroup.WithContext(ctx)
	semaphore := make(chan struct{}, concurrency)
	done := map[string]chan struct{}{}
	for _, op := range g.Operations {
		done[op.Action.ID] = make(chan struct{})
	}
	var mutex sync.Mutex
	for i, op := range g.Operations {
		group.Go(func() error {
			for _, dep := range op.Action.Dependencies {
				select {
				case <-done[dep]:
				case <-actionCtx.Done():
					return actionCtx.Err()
				}
			}
			select {
			case semaphore <- struct{}{}:
			case <-actionCtx.Done():
				return actionCtx.Err()
			}
			defer func() { <-semaphore }()
			started := time.Now()
			marker, err := op.Stamp.Contents(actionCtx)
			if err != nil {
				return fmt.Errorf("compile %s: %w", op.Action.ID, err)
			}
			entry := ActionReport{ID: op.Action.ID, Crate: op.Action.Crate, Execution: strings.TrimSpace(marker), Digests: map[string]string{}}
			entry.DemandSeconds = time.Since(started).Seconds()
			started = time.Now()
			for _, filename := range sortedKeys(op.Files) {
				digest, err := op.Files[filename].Digest(actionCtx)
				if err != nil {
					return err
				}
				entry.Digests[filename] = digest
			}
			entry.DigestSeconds = time.Since(started).Seconds()
			mutex.Lock()
			report.Actions[i] = entry
			mutex.Unlock()
			close(done[op.Action.ID])
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return nil, err
	}
	report.ReplaySeconds = time.Since(started).Seconds()
	if g.BatchTrace != nil {
		contents, err := g.BatchTrace.Contents(ctx)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(contents), &report.Batch); err != nil {
			return nil, err
		}
	}
	slices.SortFunc(report.Actions, func(a, b ActionReport) int { return strings.Compare(a.ID, b.ID) })
	return report, nil
}

func (r *Report) Reused(previous *Report) int {
	markers := map[string]string{}
	for _, action := range previous.Actions {
		markers[action.ID] = action.Execution
	}
	n := 0
	for _, action := range r.Actions {
		if action.Execution != "" && markers[action.ID] == action.Execution {
			n++
		}
	}
	return n
}
