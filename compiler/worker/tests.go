package worker

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"slices"
	"strings"

	"github.com/grouville/dagger-rust/compiler/model"
)

// RunTests preserves Cargo's recorded command order and per-command environment.
// A failing command prevents all later commands from running.
func RunTests(ctx context.Context, commands []model.TestCommand, stdout, stderr io.Writer) error {
	for _, command := range commands {
		args := []string{"-i"}
		keys := make([]string, 0, len(command.Env))
		for key := range command.Env {
			if key == "TRACEPARENT" || key == "TRACESTATE" || key == "BAGGAGE" {
				continue
			}
			if key == "" || strings.ContainsAny(key, "=\x00") {
				return fmt.Errorf("invalid test environment name")
			}
			keys = append(keys, key)
		}
		slices.Sort(keys)
		for _, key := range keys {
			args = append(args, key+"="+command.Env[key])
		}
		args = append(args, command.Executable)
		args = append(args, command.Args...)
		run := exec.CommandContext(ctx, "env", args...)
		run.Dir = command.Cwd
		run.Stdout, run.Stderr = stdout, stderr
		fmt.Fprintf(stdout, "\n==> %s\n", command.Executable)
		if err := run.Run(); err != nil {
			return fmt.Errorf("test command %s: %w", command.Executable, err)
		}
	}
	return nil
}
