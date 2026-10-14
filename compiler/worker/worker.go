// Package worker schedules compiler misses inside a single isolated execution.
// Dagger retains inputs and per-action outputs as ordinary native snapshots.
package worker

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/grouville/dagger-rust/compiler/model"
)

const Root = "/rcexp-batch"
const OutputRoot = "/rcexp-results"
const TargetRoot = OutputRoot + "/target"
const IncrementalRoot = OutputRoot + "/incremental"
const ActionsRoot = OutputRoot + "/actions"

// ArtifactPath locates a captured /target output in the retained native tree.
func ArtifactPath(filename string) string {
	return privatePath(TargetRoot, filename)
}

type Request struct {
	Actions       []model.Action  `json:"actions"`
	Dependencies  []model.Action  `json:"dependencies"`
	Packages      []model.Package `json:"packages"`
	Concurrency   int             `json:"concurrency"`
	Pipelining    bool            `json:"pipelining"`
	NoIncremental bool            `json:"no_incremental,omitempty"`
}

type Timing struct {
	ID           string  `json:"id"`
	Crate        string  `json:"crate"`
	StartedNanos int64   `json:"started_unix_nanos"`
	Seconds      float64 `json:"seconds"`
	Metadata     float64 `json:"metadata_seconds,omitempty"`
}

func Token(id string) string {
	digest := sha256.Sum256([]byte(id))
	return hex.EncodeToString(digest[:])
}

type event struct {
	id       string
	metadata bool
	timing   Timing
	err      error
}

func Run(ctx context.Context, request Request) ([]Timing, error) {
	if request.Concurrency < 1 {
		return nil, fmt.Errorf("batch compiler concurrency must be positive")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	pending := map[string]model.Action{}
	all := map[string]model.Action{}
	for _, action := range request.Actions {
		if action.ID == "" || all[action.ID].ID != "" {
			return nil, fmt.Errorf("duplicate or empty compiler action")
		}
		pending[action.ID], all[action.ID] = action, action
	}
	complete, metadata := map[string]bool{}, map[string]bool{}
	events := make(chan event, 2*len(all))
	var timings []Timing
	active := 0
	var failure error
	for len(pending) > 0 || active > 0 {
		if failure == nil {
			for _, id := range sortedIDs(pending) {
				action := pending[id]
				if active == request.Concurrency || !ready(action, all, complete, metadata, request.Pipelining) {
					continue
				}
				delete(pending, id)
				active++
				go compile(ctx, action, request.Dependencies, request.Packages, !request.NoIncremental, events)
			}
		}
		if active == 0 {
			if failure != nil {
				return nil, failure
			}
			return nil, fmt.Errorf("batch compiler dependency cycle")
		}
		update := <-events
		if update.metadata {
			metadata[update.id] = true
			continue
		}
		active--
		if update.err != nil && failure == nil {
			failure = fmt.Errorf("compile %s: %w", all[update.id].Crate, update.err)
			cancel()
		}
		complete[update.id] = true
		metadata[update.id] = true
		timings = append(timings, update.timing)
		if failure != nil && active == 0 {
			return nil, failure
		}
	}
	// Published outputs are hard links. All compilers have finished, so their
	// private dependency views can be removed without deleting those outputs.
	if err := cleanupPrivateTargets(TargetRoot); err != nil {
		return nil, fmt.Errorf("clean private compiler targets: %w", err)
	}
	return timings, nil
}

func cleanupPrivateTargets(target string) error {
	return os.RemoveAll(path.Join(target, ".rcexp-private"))
}

func ready(action model.Action, all map[string]model.Action, complete, metadata map[string]bool, pipelining bool) bool {
	useMetadata := pipelining && slices.ContainsFunc(action.Outputs, func(output string) bool { return path.Ext(output) == ".rmeta" })
	// A captured command explicitly requesting an rlib cannot consume metadata.
	for _, arg := range action.Args {
		if strings.HasSuffix(arg, ".rlib") {
			useMetadata = false
		}
	}
	visited := map[string]bool{}
	var available func(string) bool
	available = func(id string) bool {
		if _, missed := all[id]; !missed || visited[id] {
			return true // Already compiled inputs, supplied by native history.
		}
		visited[id] = true
		if useMetadata {
			return metadata[id]
		}
		if !complete[id] {
			return false
		}
		for _, parent := range all[id].Dependencies {
			if !available(parent) {
				return false
			}
		}
		return true
	}
	for _, dep := range action.Dependencies {
		if !available(dep) {
			return false
		}
	}
	return true
}

func sortedIDs(actions map[string]model.Action) []string {
	ids := make([]string, 0, len(actions))
	for id := range actions {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

func compile(ctx context.Context, action model.Action, dependencies []model.Action, packages []model.Package, incremental bool, events chan<- event) {
	started := time.Now()
	private := path.Join(TargetRoot, ".rcexp-private", Token(action.ID))
	observer := metadataObserver{started: started, notify: func() error {
		for _, output := range action.Outputs {
			if path.Ext(output) == ".rmeta" {
				if err := publish(privatePath(private, output), ArtifactPath(output)); err != nil {
					return err
				}
			}
		}
		events <- event{id: action.ID, metadata: true}
		return nil
	}}
	err := func() error {
		state := path.Join(IncrementalRoot, Token(action.ID))
		if err := os.MkdirAll(state, 0755); err != nil {
			return err
		}
		if err := os.MkdirAll(privatePath(private, model.Option(action.Args, "--out-dir")), 0755); err != nil {
			return err
		}
		if err := prepareDependencies(private, action, dependencies); err != nil {
			return err
		}
		args := slices.Clone(action.Args)
		for i, arg := range args {
			if model.Within(model.TargetRoot, arg) {
				args[i] = privatePath(private, arg)
			} else {
				args[i] = strings.ReplaceAll(arg, "="+model.TargetRoot+"/", "="+private+"/")
			}
		}
		if incremental {
			args = append(args, "-C", "incremental="+state)
		}
		command := exec.CommandContext(ctx, action.Compiler, args...)
		command.Dir = action.Cwd
		command.Env = environment(action, os.Environ())
		command.Stdout = os.Stdout
		command.Stderr = io.MultiWriter(os.Stderr, &observer)
		if err := command.Run(); err != nil {
			return err
		}
		// All sources are visible to the batch. Reject newly introduced reads
		// outside package ownership before publishing any cache results.
		for _, output := range action.Outputs {
			if path.Ext(output) != ".d" {
				continue
			}
			filename := privatePath(private, output)
			contents, err := os.ReadFile(filename)
			if err != nil {
				return err
			}
			normalized := strings.ReplaceAll(string(contents), private+"/", model.TargetRoot+"/")
			inputs, _, err := model.DepInfo(normalized, action.Cwd)
			if err != nil {
				return err
			}
			if err := validateSourceInputs(action, packages, inputs, filepath.EvalSymlinks); err != nil {
				return err
			}
			if err := os.WriteFile(filename, []byte(normalized), 0644); err != nil {
				return err
			}
		}
		for _, output := range action.Outputs {
			if err := publish(privatePath(private, output), ArtifactPath(output)); err != nil {
				return err
			}
		}
		marker := path.Join(ActionsRoot, Token(action.ID), "execution")
		if err := os.MkdirAll(path.Dir(marker), 0755); err != nil {
			return err
		}
		return os.WriteFile(marker, []byte(rand.Text()+"\n"), 0600)
	}()
	events <- event{id: action.ID, err: err, timing: Timing{ID: action.ID, Crate: action.Crate,
		StartedNanos: started.UnixNano(), Seconds: time.Since(started).Seconds(), Metadata: observer.seconds}}
}

func privatePath(private, filename string) string {
	if filename == model.TargetRoot {
		return private
	}
	return path.Join(private, strings.TrimPrefix(filename, model.TargetRoot+"/"))
}

// Each compiler searches only its captured dependency closure. A shared -L
// directory would let a new `extern crate` discover an undeclared library and
// make the per-crate cache key incomplete. Hard links keep this view cheap;
// the private tree and published files live on the same mounted filesystem.
func prepareDependencies(private string, action model.Action, actions []model.Action) error {
	all := map[string]model.Action{}
	for _, candidate := range actions {
		all[candidate.ID] = candidate
	}
	visited := map[string]bool{}
	var collect func(string) error
	collect = func(id string) error {
		if visited[id] {
			return nil
		}
		visited[id] = true
		dependency, ok := all[id]
		if !ok {
			return fmt.Errorf("missing dependency configuration %s", id)
		}
		for _, output := range dependency.Outputs {
			if path.Ext(output) == ".d" {
				continue
			}
			destination := privatePath(private, output)
			if err := os.MkdirAll(path.Dir(destination), 0755); err != nil {
				return err
			}
			if err := os.Link(ArtifactPath(output), destination); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
		for _, parent := range dependency.Dependencies {
			if err := collect(parent); err != nil {
				return err
			}
		}
		return nil
	}
	for _, id := range action.Dependencies {
		if err := collect(id); err != nil {
			return err
		}
	}
	return nil
}

func publish(source, destination string) error {
	if err := os.MkdirAll(path.Dir(destination), 0755); err != nil {
		return err
	}
	if original, err := os.Stat(destination); err == nil {
		produced, err := os.Stat(source)
		if err != nil {
			return err
		}
		if os.SameFile(original, produced) {
			return nil
		}
		if err := os.Remove(destination); err != nil {
			return err
		}
	}
	return os.Link(source, destination)
}

func validateInputs(action model.Action, packages []model.Package, inputs []string) error {
	return validateSourceInputs(action, packages, inputs, nil)
}

func validateSourceInputs(action model.Action, packages []model.Package, inputs []string, resolve func(string) (string, error)) error {
	roots := make(map[string]bool, len(packages))
	for _, pkg := range packages {
		roots[pkg.Root] = true
	}
	owned := func(input string) bool {
		for at := path.Clean(input); model.Within(model.SourceRoot, at); at = path.Dir(at) {
			if roots[at] {
				return at == action.PackageRoot
			}
		}
		return false
	}
	for _, input := range inputs {
		if !owned(input) {
			return fmt.Errorf("new source input %s is outside package %s; recapture or use Cargo", input, action.PackageRoot)
		}
		if resolve != nil {
			resolved, err := resolve(input)
			if err != nil {
				return fmt.Errorf("resolve source input %s: %w", input, err)
			}
			if !owned(resolved) {
				return fmt.Errorf("source input %s resolves outside package %s", input, action.PackageRoot)
			}
		}
	}
	return nil
}

func environment(action model.Action, inherited []string) []string {
	values := map[string]string{}
	for _, item := range inherited {
		key, value, _ := strings.Cut(item, "=")
		values[key] = value
	}
	for key, value := range action.Env {
		values[key] = value
	}
	for _, key := range action.UnsetEnv {
		delete(values, key)
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+values[key])
	}
	return result
}

type metadataObserver struct {
	started time.Time
	pending []byte
	seconds float64
	notify  func() error
}

func (o *metadataObserver) Write(data []byte) (int, error) {
	if o.seconds != 0 {
		return len(data), nil
	}
	o.pending = append(o.pending, data...)
	for {
		end := bytes.IndexByte(o.pending, '\n')
		if end < 0 {
			break
		}
		var message struct {
			Type string `json:"$message_type"`
			Emit string `json:"emit"`
		}
		if json.Unmarshal(o.pending[:end], &message) == nil && message.Type == "artifact" && message.Emit == "metadata" {
			o.seconds = time.Since(o.started).Seconds()
			o.pending = nil
			if err := o.notify(); err != nil {
				return len(data), err
			}
			break
		}
		o.pending = o.pending[end+1:]
	}
	return len(data), nil
}
