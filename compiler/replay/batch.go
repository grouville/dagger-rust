package replay

import (
	"encoding/json"
	"fmt"
	"path"

	dagger "dagger.io/dagger/core"
	"github.com/grouville/dagger-rust/compiler/model"
	"github.com/grouville/dagger-rust/compiler/worker"
)

func buildBatch(client *dagger.Query, plan *model.Plan, source Source, opts BuildOptions) (*Graph, error) {
	if opts.BatchWorkers < 1 {
		return nil, fmt.Errorf("batch compiler concurrency must be positive")
	}
	actions, err := ordered(plan.Actions)
	if err != nil {
		return nil, err
	}
	var missing []model.Action
	var targets []*dagger.Directory
	var seeds []*dagger.Directory
	for _, action := range actions {
		if retained := opts.automatic.hits[action.ID]; retained != nil {
			if opts.automatic.artifacts == nil {
				targets = append(targets, retained.target)
			}
		} else {
			missing = append(missing, action)
			if seed := opts.automatic.seeds[action.ID]; seed != nil && !opts.automatic.batchSeeded[action.ID] {
				seeds = append(seeds, client.Directory().WithDirectory(worker.Token(action.ID), seed))
			}
		}
	}
	if len(missing) == 0 {
		opts.BatchCompiler = nil
		return BuildWithOptions(client, plan, source, opts)
	}
	// Dependency views only need graph edges and output paths. Keep compiler
	// arguments and environment on the actions that will actually execute.
	dependencies := make([]model.Action, len(plan.Actions))
	for i, action := range plan.Actions {
		dependencies[i] = model.Action{ID: action.ID, Outputs: action.Outputs, Dependencies: action.Dependencies}
	}
	request := worker.Request{Actions: missing, Dependencies: dependencies, Packages: plan.Packages,
		Concurrency: opts.BatchWorkers, Pipelining: !opts.BatchNoPipelining, NoIncremental: opts.BatchNoIncremental}
	contents, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	workspace := opts.SourceDirectory
	if workspace == nil {
		workspace = source.Directory(client)
	}
	target, incremental := client.Directory(), client.Directory()
	if opts.automatic.artifacts != nil {
		target = opts.automatic.artifacts
	} else if len(targets) > 0 {
		target = mergeArtifactDirectories(targets)
	}
	if len(seeds) > 0 {
		incremental = mergeArtifactDirectories(seeds)
	}
	state := client.Directory().WithDirectory("target", target).WithDirectory("incremental", incremental)
	for _, seed := range opts.automatic.batchSeeds {
		// Copy only the selected crates into their final input path. An
		// intermediate filtered directory would copy the same state twice.
		state = state.WithDirectory("incremental", seed.directory, dagger.DirectoryWithDirectoryOpts{Include: seed.include})
	}
	container := client.Container(dagger.ContainerOpts{Platform: "linux/amd64"}).From(plan.Image).
		WithMountedDirectory(model.SourceRoot, workspace).
		WithMountedDirectory(worker.OutputRoot, state).
		WithMountedFile(worker.Root+"/runner", opts.BatchCompiler).
		WithNewFile(worker.Root+"/request.json", string(contents), dagger.ContainerWithNewFileOpts{Permissions: 0600}).
		WithExec([]string{worker.Root + "/runner", "worker", worker.Root + "/request.json"}, noNesting())
	graph := &Graph{results: map[string]*dagger.Directory{}, BatchTrace: container.File(worker.Root + "/timings.json")}
	for _, action := range actions {
		retained := opts.automatic.hits[action.ID]
		operation := Operation{Action: action, Files: map[string]*dagger.File{}}
		if retained != nil {
			graph.HistoryHits++
			operation.Stamp = retained.stamp
		} else {
			operation.Container = container
			operation.Stamp = container.File(path.Join(worker.ActionsRoot, worker.Token(action.ID), "execution"))
		}
		for _, output := range action.Outputs {
			if operation.Container == nil {
				operation.Files[output] = retained.target.File(targetRelative(output))
			} else {
				operation.Files[output] = container.File(worker.ArtifactPath(output))
			}
		}
		graph.Operations = append(graph.Operations, operation)
	}
	// The worker publishes only captured outputs and removes its private
	// compiler trees after success. Reuse that snapshot rather than copying
	// every artifact into a newly filtered directory.
	graph.Artifacts = container.Directory(worker.TargetRoot)
	// Outputs, incremental state and markers share one mounted snapshot.
	// Manifest entries still select each crate's results independently.
	graph.batchSnapshot = container.Directory(worker.OutputRoot)
	return graph, nil
}
