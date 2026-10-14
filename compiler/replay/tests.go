package replay

import (
	"fmt"
	"path"
	"slices"

	dagger "dagger.io/dagger/core"
	"github.com/grouville/dagger-rust/compiler/model"
)

// Tests see the whole workspace, including runtime files outside crate roots.
// Chain the captured commands so Cargo's execution order and filesystem effects
// are preserved. Compilation caches remain separate from these execution keys.
func TestExecution(client *dagger.Query, plan *model.Plan, source, target *dagger.Directory) (*dagger.Container, error) {
	if plan.CargoCommand != "test" || len(plan.Tests) == 0 {
		return nil, fmt.Errorf("plan contains no captured Cargo test commands")
	}
	container := client.Container(dagger.ContainerOpts{Platform: "linux/amd64"}).From(plan.Image).
		WithMountedDirectory(model.SourceRoot, source).WithMountedDirectory(model.TargetRoot, target)
	for _, command := range plan.Tests {
		if !model.Within(model.SourceRoot, command.Cwd) || path.Clean(command.Cwd) != command.Cwd ||
			(command.Executable != "rustdoc" && (!model.Within(model.TargetRoot, command.Executable) || path.Clean(command.Executable) != command.Executable)) {
			return nil, fmt.Errorf("invalid captured test command")
		}
		// Preserve the complete captured environment in one action input,
		// rather than building one Dagger container operation per variable.
		args := []string{"env", "-i"}
		keys := make([]string, 0, len(command.Env))
		for key := range command.Env {
			// Older captures include their recording session's trace context.
			if key == "TRACEPARENT" || key == "TRACESTATE" || key == "BAGGAGE" {
				continue
			}
			keys = append(keys, key)
		}
		slices.Sort(keys)
		for _, key := range keys {
			args = append(args, key+"="+command.Env[key])
		}
		args = append(args, command.Executable)
		args = append(args, command.Args...)
		container = container.WithWorkdir(command.Cwd).
			WithExec(args, noNesting())
	}
	return container, nil
}
