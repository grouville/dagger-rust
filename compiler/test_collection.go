package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	core "dagger.io/dagger/core"
	"github.com/grouville/dagger-rust/compiler/internal/dagger"
	"github.com/grouville/dagger-rust/compiler/model"
	"github.com/grouville/dagger-rust/compiler/replay"
	"github.com/grouville/dagger-rust/compiler/worker"
)

type TestSuite struct {
	Metadata    string
	Environment *dagger.Container
	Executions  []*dagger.Container
}

type testPackage struct {
	Name           string   `json:"name"`
	Keys           []string `json:"keys"`
	Commands       string   `json:"commands"`
	ExecutionIndex int      `json:"executionIndex"`
}

// DiscoverTests asks compiled libtest harnesses for their actual test names.
// Discovery runs once for the workspace; package batches share its result.
func (m *RustCompiler) DiscoverTests(ctx context.Context, source *dagger.Directory, plan *dagger.File, artifacts *dagger.Directory) (*TestSuite, error) {
	contents, err := readCompilerPlan(ctx, plan)
	if err != nil {
		return nil, err
	}
	var recipe model.Plan
	if err := json.Unmarshal(contents, &recipe); err != nil {
		return nil, err
	}
	targets, err := replay.CollectionTargets(&recipe)
	if err != nil {
		return nil, err
	}
	query := nativeQuery()
	native, err := nativeDirectory(ctx, query, source)
	if err != nil {
		return nil, err
	}
	target, err := nativeDirectory(ctx, query, artifacts)
	if err != nil {
		return nil, err
	}
	for _, publication := range recipe.Publications {
		target = target.WithFile(publication.Path, target.File(strings.TrimPrefix(publication.Output, model.TargetRoot+"/")))
	}
	runner, err := m.tools().File("/out/worker").ID(ctx)
	if err != nil {
		return nil, err
	}
	environment := replay.TestEnvironment(query, &recipe, native, target, core.Ref[*core.File](query, core.ID(runner)))
	listing, err := json.Marshal(replay.CollectionListing(targets))
	if err != nil {
		return nil, err
	}
	output, err := environment.WithNewFile(worker.Root+"/test-listing.json", string(listing), core.ContainerWithNewFileOpts{Permissions: 0600}).
		WithExec([]string{worker.Root + "/runner", "tests", worker.Root + "/test-listing.json"}, core.ContainerWithExecOpts{DisableDaggerInDagger: true}).Stdout(ctx)
	if err != nil {
		return nil, fmt.Errorf("list Rust tests: %w", err)
	}
	if err := replay.ReadCollectionListing(targets, output); err != nil {
		return nil, err
	}
	suite := &TestSuite{}
	var packages []testPackage
	byPackage := map[string][]replay.CollectionTarget{}
	for _, target := range targets {
		if _, found := byPackage[target.Package]; !found {
			packages = append(packages, testPackage{Name: target.Package})
		}
		byPackage[target.Package] = append(byPackage[target.Package], target)
	}
	for i := range packages {
		packages[i].ExecutionIndex = i
		for _, target := range byPackage[packages[i].Name] {
			packages[i].Keys = append(packages[i].Keys, replay.CollectionKeys(target)...)
		}
		commands, err := json.Marshal(byPackage[packages[i].Name])
		if err != nil {
			return nil, err
		}
		packages[i].Commands = string(commands)
	}
	serialized, err := json.Marshal(packages)
	if err != nil {
		return nil, err
	}
	suite.Metadata = string(serialized)
	id, err := environment.ID(ctx)
	if err != nil {
		return nil, err
	}
	suite.Environment = dagger.Ref[*dagger.Container](dag, dagger.ID(id))
	for _, pkg := range packages {
		execution, err := m.RunTestSelection(ctx, suite.Environment, pkg.Commands, pkg.Name, pkg.Keys)
		if err != nil {
			return nil, err
		}
		suite.Executions = append(suite.Executions, execution)
	}
	return suite, nil
}

// RunTestSelection executes one package's selected tests, retaining Cargo's
// captured command order, environment and working directories.
func (m *RustCompiler) RunTestSelection(ctx context.Context, environment *dagger.Container, commands string, pkg string, keys []string) (*dagger.Container, error) {
	var targets []replay.CollectionTarget
	if err := json.Unmarshal([]byte(commands), &targets); err != nil {
		return nil, err
	}
	selected, err := replay.CollectionSelection(targets, pkg, keys)
	if err != nil {
		return nil, err
	}
	serialized, err := json.Marshal(selected)
	if err != nil {
		return nil, err
	}
	return environment.WithNewFile(worker.Root+"/selected-tests.json", string(serialized), dagger.ContainerWithNewFileOpts{Permissions: 0600}).
		WithExec([]string{worker.Root + "/runner", "tests", worker.Root + "/selected-tests.json"}, dagger.ContainerWithExecOpts{DisableDaggerInDagger: true}), nil
}
