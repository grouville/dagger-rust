//go:build integration

package replay

import (
	"encoding/json"
	"path"
	"path/filepath"
	"strings"
	"testing"

	daggerclient "dagger.io/dagger"
	dagger "dagger.io/dagger/core"
	"github.com/grouville/dagger-rust/compiler/model"
)

func TestBatchHistoryRestoresRevertedArtifacts(t *testing.T) {
	ctx := t.Context()
	client, err := daggerclient.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	query := dagger.NewQuery(client)
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	tools := query.Container().From("golang:1.26.6-alpine").
		WithMountedDirectory("/src", query.Host().Directory(root).Filter(dagger.DirectoryFilterOpts{Include: []string{
			"go.mod", "go.sum", "model/**", "worker/**", "cmd/**",
		}})).WithWorkdir("/src").WithEnvVariable("CGO_ENABLED", "0").
		WithExec([]string{"go", "build", "-buildvcs=false", "-trimpath", "-ldflags=-s -w", "-o", "/out/", "./cmd/worker", "./cmd/wrapper"}, noNesting())
	files := Source{
		"Cargo.toml":        {Contents: "[workspace]\nresolver = \"2\"\nmembers = [\"base\", \"middle\", \"app\"]\n", Mode: 0644},
		"base/Cargo.toml":   {Contents: "[package]\nname = \"base\"\nversion = \"0.1.0\"\nedition = \"2021\"\n", Mode: 0644},
		"middle/Cargo.toml": {Contents: "[package]\nname = \"middle\"\nversion = \"0.1.0\"\nedition = \"2021\"\n[dependencies]\nbase = { path = \"../base\" }\n", Mode: 0644},
		"app/Cargo.toml":    {Contents: "[package]\nname = \"app\"\nversion = \"0.1.0\"\nedition = \"2021\"\n[dependencies]\nmiddle = { path = \"../middle\" }\n", Mode: 0644},
		"Cargo.lock": {Contents: `version = 3
[[package]]
name = "app"
version = "0.1.0"
dependencies = ["middle"]
[[package]]
name = "base"
version = "0.1.0"
[[package]]
name = "middle"
version = "0.1.0"
dependencies = ["base"]
`, Mode: 0644},
		"base/src/lib.rs":   {Contents: "pub fn value() -> u32 { 1 }\n", Mode: 0644},
		"middle/src/lib.rs": {Contents: "pub fn value() -> u32 { base::value() + 1 }\n", Mode: 0644},
		"app/src/main.rs":   {Contents: "fn main() { println!(\"{}\", middle::value()); }\n", Mode: 0644},
	}
	source := files.Directory(query)
	plan, err := CaptureNative(ctx, query, source, CaptureOptions{Image: DefaultImage, Command: "build", Wrapper: tools.File("/out/wrapper")})
	if err != nil {
		t.Fatal(err)
	}
	executable := ""
	for _, publication := range plan.Publications {
		if path.Base(publication.Path) == "app" {
			executable = strings.TrimPrefix(publication.Output, model.TargetRoot+"/")
		}
	}
	if executable == "" || len(plan.Actions) != 3 {
		t.Fatalf("expected three actions and an app executable, got %d actions and %q", len(plan.Actions), executable)
	}
	changedMiddle := source.WithNewFile("middle/src/lib.rs", "pub fn value() -> u32 { base::value() + 11 }\n")
	changedApp := source.WithNewFile("app/src/main.rs", "fn main() { println!(\"{}\", middle::value() + 100); }\n")
	mixed := changedApp.WithNewFile("middle/src/lib.rs", "pub fn value() -> u32 { base::value() + 11 }\n")
	var previous *NativeCache
	for _, step := range []struct {
		name         string
		source       *dagger.Directory
		hits         int
		output       string
		detachedRoot bool
	}{
		{"initial", source, 0, "2", false},
		{"edit middle", changedMiddle, 1, "12", false},
		{"revert entirely from cache", source, 3, "2", true},
		{"edit with retained transitive inputs", changedApp, 2, "102", false},
		{"mix an older dependency with a new compile", mixed, 2, "112", false},
		{"revert mixed result entirely from cache", changedApp, 3, "102", true},
	} {
		t.Run(step.name, func(t *testing.T) {
			graph, err := BuildNativeCached(ctx, query, plan, step.source, BuildOptions{
				BatchCompiler: tools.File("/out/worker"), BatchWorkers: 2, SourceDigestPaths: true,
			}, previous)
			if err != nil {
				t.Fatal(err)
			}
			if graph.HistoryHits != step.hits {
				t.Fatalf("hits = %d, want %d", graph.HistoryHits, step.hits)
			}
			output, err := query.Container().From(DefaultImage).WithMountedDirectory("/out", graph.Artifacts).
				WithExec([]string{"/out/" + executable}, noNesting()).Stdout(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if strings.TrimSpace(output) != step.output {
				t.Fatalf("binary output = %q, want %q", output, step.output)
			}
			if step.detachedRoot {
				var manifest historyManifest
				if err := json.Unmarshal([]byte(graph.Cache.Manifest), &manifest); err != nil {
					t.Fatal(err)
				}
				if manifest.CurrentBatch != "" {
					t.Fatal("reversion should require assembling retained artifacts rather than reusing one batch root")
				}
			}
			previous = graph.Cache
			t.Logf("reused %d/3 actions; executed binary prints %s", step.hits, step.output)
		})
		if t.Failed() {
			break
		}
	}
}
