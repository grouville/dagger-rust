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
	command := model.TestCommand{Env: map[string]string{}}
	if filepath.Base(os.Args[0]) == "rustdoc-recorder" {
		command.Executable = "rustdoc"
		command.Args = os.Args[1:]
		if !slices.Contains(command.Args, "--test") {
			probe := exec.Command(command.Executable, command.Args...)
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
