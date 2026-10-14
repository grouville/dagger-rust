package replay

import (
	"encoding/json"
	"fmt"
	"path"

	dagger "dagger.io/dagger/core"
	"github.com/grouville/dagger-rust/compiler/model"
	"github.com/grouville/dagger-rust/compiler/worker"
)

// Tests see the whole workspace, including runtime files outside crate roots.
// Run the captured commands in order within one fresh execution. Each command
// keeps its environment and cwd; filesystem effects pass to the next command.
// Compilation caches remain separate from these execution keys.
func TestExecution(client *dagger.Query, plan *model.Plan, source, target *dagger.Directory, runner *dagger.File) (*dagger.Container, error) {
	if plan.CargoCommand != "test" || len(plan.Tests) == 0 {
		return nil, fmt.Errorf("plan contains no captured Cargo test commands")
	}
	if runner == nil {
		return nil, fmt.Errorf("native test execution requires its runner")
	}
	for _, command := range plan.Tests {
		if !model.Within(model.SourceRoot, command.Cwd) || path.Clean(command.Cwd) != command.Cwd ||
			(command.Executable != "rustdoc" && (!model.Within(model.TargetRoot, command.Executable) || path.Clean(command.Executable) != command.Executable)) {
			return nil, fmt.Errorf("invalid captured test command")
		}
	}
	contents, err := json.Marshal(plan.Tests)
	if err != nil {
		return nil, err
	}
	return client.Container(dagger.ContainerOpts{Platform: "linux/amd64"}).From(plan.Image).
		WithMountedDirectory(model.SourceRoot, source).WithMountedDirectory(model.TargetRoot, target).
		WithMountedFile(worker.Root+"/runner", runner).
		WithNewFile(worker.Root+"/tests.json", string(contents), dagger.ContainerWithNewFileOpts{Permissions: 0600}).
		WithExec([]string{worker.Root + "/runner", "tests", worker.Root + "/tests.json"}, noNesting()), nil
}
