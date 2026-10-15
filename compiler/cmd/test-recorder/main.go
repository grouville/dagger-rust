// Capture test commands without running them; replay executes them separately.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/grouville/dagger-rust/compiler/model"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) == 2 && os.Args[1] == "--resolve-rustdoc" {
		program, err := resolveRustdoc()
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(program)
	}
	command := model.TestCommand{Env: map[string]string{}}
	if filepath.Base(os.Args[0]) == "rustdoc-recorder" {
		command.Executable = "rustdoc"
		command.Args = os.Args[1:]
		if !slices.Contains(command.Args, "--test") {
			program := os.Getenv("RCE_RUSTDOC")
			if program == "" {
				program = command.Executable
			}
			probe := exec.Command(program, command.Args...)
			probe.Stdin, probe.Stdout, probe.Stderr = os.Stdin, os.Stdout, os.Stderr
			return probe.Run()
		}
	} else {
		if len(os.Args) < 2 {
			return fmt.Errorf("missing test executable")
		}
		command.Executable, command.Args = os.Args[1], os.Args[2:]
	}
	var err error
	command.Cwd, err = os.Getwd()
	if err != nil {
		return err
	}
	for _, entry := range os.Environ() {
		name, value, _ := strings.Cut(entry, "=")
		if model.TestTransportEnvironment(name) {
			continue
		}
		command.Env[name] = value
	}
	data, err := json.Marshal(command)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(filepath.Join(os.Getenv("RCE_CAPTURE_DIR"), "tests.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.Write(append(data, '\n'))
	return err
}

// Preserve custom rustdoc programs. For a rustup proxy, ask rustup for its
// selected binary once instead of running the proxy for every documentation test.
func resolveRustdoc() (string, error) {
	program := os.Getenv("RUSTDOC")
	if program == "" {
		program = os.Getenv("CARGO_BUILD_RUSTDOC")
	}
	if program == "" {
		program = "rustdoc"
	}
	program, err := exec.LookPath(program)
	if err != nil {
		return "", err
	}
	if filepath.Base(program) == "rustdoc" {
		if rustup, err := exec.LookPath("rustup"); err == nil {
			tool, toolErr := os.Stat(program)
			manager, managerErr := os.Stat(rustup)
			if toolErr == nil && managerErr == nil && os.SameFile(tool, manager) {
				selected, err := exec.Command(rustup, "which", "rustdoc").Output()
				if err != nil {
					return "", fmt.Errorf("resolve selected rustdoc: %w", err)
				}
				program = strings.TrimSpace(string(selected))
				if !filepath.IsAbs(program) || filepath.Clean(program) != program {
					return "", fmt.Errorf("rustup returned an invalid rustdoc path %q", program)
				}
			}
		}
	}
	return filepath.Abs(program)
}
