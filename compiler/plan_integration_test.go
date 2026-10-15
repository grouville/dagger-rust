//go:build integration

package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/grouville/dagger-rust/compiler/internal/dagger"
)

func TestReadCompilerPlan(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	for _, size := range []int{0, 128 * 1024} {
		contents, err := json.Marshal(map[string]string{
			"message": "héllo \"quoted\" \\path\n",
			"padding": strings.Repeat("rust-plan-", size),
		})
		if err != nil {
			t.Fatal(err)
		}
		file := dag.Directory().WithNewFile("plan.json", string(contents), dagger.DirectoryWithNewFileOpts{Permissions: 0400}).File("plan.json")
		got, err := readCompilerPlan(t.Context(), file)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(contents) {
			t.Fatal("plan transfer changed the contents")
		}
	}
	if _, err := readCompilerPlan(t.Context(), dag.Directory().File("missing.json")); err == nil {
		t.Fatal("reading a missing plan succeeded")
	}
	files, err := os.ReadDir(os.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Fatalf("temporary plan files remain: %v", files)
	}
}
