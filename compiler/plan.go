package main

import (
	"context"
	"os"
	"path/filepath"

	"github.com/grouville/dagger-rust/compiler/internal/dagger"
)

func readCompilerPlan(ctx context.Context, plan *dagger.File) ([]byte, error) {
	dir, err := os.MkdirTemp("", "rust-compiler-plan-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	filename := filepath.Join(dir, "plan.json")
	if _, err := plan.Export(ctx, filename); err != nil {
		return nil, err
	}
	// Plan permissions do not determine whether its JSON can be decoded.
	if err := os.Chmod(filename, 0600); err != nil {
		return nil, err
	}
	return os.ReadFile(filename)
}
