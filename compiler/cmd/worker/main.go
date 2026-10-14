package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/grouville/dagger-rust/compiler/model"
	"github.com/grouville/dagger-rust/compiler/worker"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) != 3 || (os.Args[1] != "worker" && os.Args[1] != "tests") {
		return fmt.Errorf("usage: worker worker|tests REQUEST")
	}
	contents, err := os.ReadFile(os.Args[2])
	if err != nil {
		return err
	}
	if os.Args[1] == "tests" {
		var commands []model.TestCommand
		if err := json.Unmarshal(contents, &commands); err != nil {
			return err
		}
		return worker.RunTests(context.Background(), commands, os.Stdout, os.Stderr)
	}
	var request worker.Request
	if err := json.Unmarshal(contents, &request); err != nil {
		return err
	}
	timings, err := worker.Run(context.Background(), request)
	if err != nil {
		return err
	}
	contents, err = json.Marshal(timings)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(worker.Root, "timings.json"), contents, 0600)
}
