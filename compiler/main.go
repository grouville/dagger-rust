// Native compiler results and incremental state for a Cargo workspace.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	core "dagger.io/dagger/core"
	"github.com/dagger/querybuilder"
	"github.com/grouville/dagger-rust/compiler/internal/dagger"
	"github.com/grouville/dagger-rust/compiler/model"
	"github.com/grouville/dagger-rust/compiler/replay"
)

type RustCompiler struct{}

// Cache retains compiler results and incremental state as native Dagger fields.
type Cache struct {
	Plan      *dagger.File
	Manifest  string
	Tokens    []string
	Snapshots []*dagger.Directory
}

type Compilation struct {
	Artifacts *dagger.Directory
	Cache     *Cache
	Hits      int
	Actions   int
	Published *dagger.Directory
	Tests     *dagger.Container
}

// Capture records the compiler graph for a supported, locked Cargo workspace.
func (m *RustCompiler) Capture(ctx context.Context,
	source *dagger.Directory,
	// +default="rust:1.77-bookworm@sha256:83101f6985c93e1e6501b3375de188ee3d2cbb89968bcc91611591f9f447bd42"
	image string,
	// +default="build"
	command string,
	// +optional
	cargoArgs []string,
) (*dagger.File, error) {
	query := nativeQuery()
	native, err := nativeDirectory(ctx, query, source)
	if err != nil {
		return nil, err
	}
	wrapper, err := m.tools().File("/out/wrapper").ID(ctx)
	if err != nil {
		return nil, err
	}
	opts := replay.CaptureOptions{
		Image: image, Command: command, CargoArgs: cargoArgs,
		Wrapper: core.Ref[*core.File](query, core.ID(wrapper)),
	}
	if command == "test" {
		recorder, err := m.tools().File("/out/test-recorder").ID(ctx)
		if err != nil {
			return nil, err
		}
		opts.TestRecorder = core.Ref[*core.File](query, core.ID(recorder))
	}
	plan, err := replay.CaptureNative(ctx, query, native, opts)
	if err != nil {
		return nil, err
	}
	contents, err := json.Marshal(plan)
	if err != nil {
		return nil, err
	}
	return dag.Directory().WithNewFile("plan.json", string(contents)).File("plan.json"), nil
}

// Compile keeps exact results and compatible incremental state separately.
// Previous cache fields must come from this builder or their native transfer.
func (m *RustCompiler) Compile(ctx context.Context,
	source *dagger.Directory,
	// +optional
	plan *dagger.File,
	// Capture or refresh the plan from the current Cargo inputs.
	// +optional
	automaticPlan bool,
	// +optional
	previousManifest string,
	// +optional
	previousTokens []string,
	// +optional
	previousSnapshots []*dagger.Directory,
	// +default=8
	workers int,
	// Expected Cargo command, when called by the Rust module's normal action.
	// +optional
	command string,
	// Expected Cargo flags, when called by the Rust module's normal action.
	// +optional
	cargoFlags []string,
	// Expected toolchain image, when called by the Rust module's normal action.
	// +optional
	image string,
	// Disable rustc incremental state for reproducible generated artifacts.
	// +optional
	noIncremental bool,
) (*Compilation, error) {
	if plan == nil && !automaticPlan {
		return nil, fmt.Errorf("compiler plan is required")
	}
	query := nativeQuery()
	native, err := nativeDirectory(ctx, query, source)
	if err != nil {
		return nil, err
	}
	var recipe model.Plan
	if plan != nil {
		contents, readErr := plan.Contents(ctx)
		if readErr == nil {
			readErr = json.Unmarshal([]byte(contents), &recipe)
		}
		if readErr != nil && !automaticPlan {
			return nil, fmt.Errorf("read compiler plan: %w", readErr)
		}
		if readErr != nil {
			recipe = model.Plan{}
		}
	}
	resolvedImage := ""
	if image != "" {
		resolvedImage, err = dag.Container().From(image).ImageRef(ctx)
		if err != nil {
			return nil, err
		}
	}
	if automaticPlan {
		if resolvedImage == "" || (command != "check" && command != "build" && command != "test") {
			return nil, fmt.Errorf("automatic plans require an image and a Cargo build, check or test command")
		}
		args, err := replay.CaptureArgs(cargoFlags)
		if err != nil {
			return nil, err
		}
		opts := replay.CaptureOptions{Image: resolvedImage, Command: command, CargoArgs: args}
		matches, err := replay.NativePlanMatches(ctx, native, &recipe, opts)
		if err != nil {
			return nil, err
		}
		if !matches {
			wrapper, err := m.tools().File("/out/wrapper").ID(ctx)
			if err != nil {
				return nil, err
			}
			opts.Wrapper = core.Ref[*core.File](query, core.ID(wrapper))
			if command == "test" {
				recorder, err := m.tools().File("/out/test-recorder").ID(ctx)
				if err != nil {
					return nil, err
				}
				opts.TestRecorder = core.Ref[*core.File](query, core.ID(recorder))
			}
			captured, err := replay.CaptureNative(ctx, query, native, opts)
			if err != nil {
				return nil, err
			}
			recipe = *captured
			contents, err := json.Marshal(captured)
			if err != nil {
				return nil, err
			}
			plan = dag.File("plan.json", string(contents))
		}
	}
	capturedCommand := recipe.CargoCommand
	if capturedCommand == "" {
		capturedCommand = "build"
	}
	if command != "" && command != capturedCommand {
		return nil, fmt.Errorf("compiler plan is for cargo %s, requested cargo %s", capturedCommand, command)
	}
	if cargoFlags != nil && !slices.Equal(cargoFlags, replay.CargoFlags(recipe.CargoArgs)) {
		return nil, fmt.Errorf("Cargo flags differ from the compiler plan")
	}
	if resolvedImage != "" && resolvedImage != recipe.Image {
		return nil, fmt.Errorf("toolchain image differs from the compiler plan")
	}
	var retained *replay.NativeCache
	if previousManifest != "" {
		if len(previousTokens) != len(previousSnapshots) {
			return nil, fmt.Errorf("compiler cache tokens and snapshots must match")
		}
		retained = &replay.NativeCache{Manifest: previousManifest}
		for i, token := range previousTokens {
			snapshot, err := nativeDirectory(ctx, query, previousSnapshots[i])
			if err != nil {
				return nil, err
			}
			retained.Batches = append(retained.Batches, replay.NativeCacheBatch{ID: token, Snapshot: snapshot})
		}
	} else if len(previousTokens) != 0 || len(previousSnapshots) != 0 {
		return nil, fmt.Errorf("compiler cache directories require a manifest")
	}
	runner, err := m.tools().File("/out/worker").ID(ctx)
	if err != nil {
		return nil, err
	}
	graph, err := replay.BuildNativeCached(ctx, query, &recipe, native, replay.BuildOptions{
		BatchCompiler: core.Ref[*core.File](query, core.ID(runner)), BatchWorkers: workers, BatchNoIncremental: noIncremental,
	}, retained)
	if err != nil {
		return nil, err
	}
	ready, err := graph.SyncArtifacts(ctx)
	if err != nil {
		return nil, err
	}
	artifacts, err := ready.ID(ctx)
	if err != nil {
		return nil, err
	}
	cache := &Cache{Plan: plan, Manifest: graph.Cache.Manifest}
	for _, batch := range graph.Cache.Batches {
		id, err := batch.Snapshot.ID(ctx)
		if err != nil {
			return nil, err
		}
		// Keep a closed native snapshot value, rather than retaining every
		// previous seeded compilation through its producing recipe.
		var saved struct {
			Checkpoint struct {
				ID core.ID `json:"id"`
			} `json:"checkpoint"`
		}
		err = querybuilder.Query().Client(dag.GraphQLClient()).Select("node").Arg("id", id).
			SelectMultiple("... on Directory { checkpoint(snapshotIdentity: true) { id } }").Bind(&saved).Execute(ctx)
		if err != nil {
			return nil, fmt.Errorf("checkpoint native compiler state: %w", err)
		}
		if saved.Checkpoint.ID == "" {
			return nil, fmt.Errorf("checkpoint native compiler state returned no directory")
		}
		cache.Tokens = append(cache.Tokens, batch.ID)
		cache.Snapshots = append(cache.Snapshots, dagger.Ref[*dagger.Directory](dag, dagger.ID(saved.Checkpoint.ID)))
	}
	published := dag.Directory()
	for _, publication := range recipe.Publications {
		published = published.WithFile(publication.Path,
			dagger.Ref[*dagger.Directory](dag, dagger.ID(artifacts)).File(strings.TrimPrefix(publication.Output, model.TargetRoot+"/")))
	}
	var tests *dagger.Container
	if command == "test" {
		target := core.Ref[*core.Directory](query, artifacts)
		for _, publication := range recipe.Publications {
			target = target.WithFile(publication.Path, graph.Artifacts.File(strings.TrimPrefix(publication.Output, model.TargetRoot+"/")))
		}
		run, err := replay.TestExecution(query, &recipe, native, target, core.Ref[*core.File](query, core.ID(runner)))
		if err != nil {
			return nil, err
		}
		id, err := run.ID(ctx)
		if err != nil {
			return nil, err
		}
		tests = dagger.Ref[*dagger.Container](dag, dagger.ID(id))
	}
	return &Compilation{Artifacts: dagger.Ref[*dagger.Directory](dag, dagger.ID(artifacts)), Published: published, Tests: tests, Cache: cache,
		Hits: graph.HistoryHits, Actions: len(recipe.Actions)}, nil
}

func nativeQuery() *core.Query {
	return new(core.Query).WithGraphQLQuery(querybuilder.Query().Client(dag.GraphQLClient()))
}

func nativeDirectory(ctx context.Context, query *core.Query, directory *dagger.Directory) (*core.Directory, error) {
	if directory == nil {
		return nil, fmt.Errorf("native directory is required")
	}
	id, err := directory.ID(ctx)
	if err != nil {
		return nil, err
	}
	return core.Ref[*core.Directory](query, core.ID(id)), nil
}

func (m *RustCompiler) tools() *dagger.Container {
	// Compiler helpers have only standard-library imports. Their build is a
	// normal immutable Dagger execution, independent of the project's sources.
	source := dag.CurrentModule().Source().Filter(dagger.DirectoryFilterOpts{Include: []string{
		"go.mod", "go.sum", "model/**", "worker/**", "cmd/**",
	}})
	return dag.Container().From("golang:1.26.6-alpine").
		WithMountedDirectory("/src", source).WithWorkdir("/src").
		WithEnvVariable("CGO_ENABLED", "0").
		WithExec([]string{"go", "build", "-buildvcs=false", "-trimpath", "-ldflags=-s -w", "-o", "/out/", "./cmd/worker", "./cmd/wrapper", "./cmd/test-recorder"}, dagger.ContainerWithExecOpts{DisableDaggerInDagger: true})
}
