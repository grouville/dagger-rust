package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/grouville/dagger-rust/compiler/worker"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) != 3 || os.Args[1] != "worker" {
		return fmt.Errorf("usage: worker worker REQUEST")
	}
	contents, err := os.ReadFile(os.Args[2])
	if err != nil {
		return err
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
