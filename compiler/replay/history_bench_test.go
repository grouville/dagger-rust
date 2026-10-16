package replay

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	dagger "dagger.io/dagger/core"
	"github.com/Khan/genqlient/graphql"
	"github.com/dagger/querybuilder"
	"github.com/grouville/dagger-rust/compiler/model"
)

// Fixed input responses isolate local graph construction from engine I/O.
type historyBenchmarkClient struct{ response []byte }

func (c historyBenchmarkClient) MakeRequest(_ context.Context, _ *graphql.Request, response *graphql.Response) error {
	return json.Unmarshal(c.response, response.Data)
}

func historyBenchmarkFixture(size int, chain bool) (*model.Plan, *dagger.Query, *dagger.Directory, BuildOptions, *historyBenchmarkClient) {
	digest := "sha256:" + strings.Repeat("a", 64)
	plan := &model.Plan{Version: model.Version, Image: DefaultImage, NativeConfigDigest: digest}
	for i := range size {
		name := fmt.Sprintf("pkg_%04d", i)
		root := "/src/" + name
		action := model.Action{ID: name, PackageRoot: root, Crate: name, Compiler: "/bin/rustc", Cwd: root,
			Args:   []string{"--crate-type=lib", "--emit=metadata", "--out-dir=/target/deps", root + "/src/lib.rs"},
			Inputs: []string{root + "/src/lib.rs"}, Outputs: []string{"/target/deps/lib" + name + ".rmeta"}}
		if i > 0 {
			parent := 0
			if chain {
				parent = i - 1
			}
			action.Args = append(action.Args, "--extern", fmt.Sprintf("pkg_%04d=/target/deps/libpkg_%04d.rmeta", parent, parent))
		}
		plan.Packages = append(plan.Packages, model.Package{Root: root})
		plan.Actions = append(plan.Actions, action)
	}
	client := &historyBenchmarkClient{}
	query := new(dagger.Query).WithGraphQLQuery(querybuilder.Query().Client(client))
	source := dagger.Ref[*dagger.Directory](query, dagger.ID("source"))
	opts := BuildOptions{SourceDigestPaths: true, BatchCompiler: dagger.Ref[*dagger.File](query, dagger.ID("worker")), BatchWorkers: 8,
		ConfigGuard: &ConfigurationGuard{Version: 1, Canonical: digest, Entries: digest}}
	return plan, query, source, opts, client
}

func (c *historyBenchmarkClient) setSource(size int, edited bool) {
	digest := "sha256:" + strings.Repeat("a", 64)
	keys := make([]string, size)
	for i := range keys {
		keys[i] = digest
	}
	if edited {
		keys[size-1] = "sha256:" + strings.Repeat("b", 64)
	}
	response := map[string]any{"node": map[string]any{"digest": digest, "digestEntries": digest, "digests": keys}}
	var err error
	c.response, err = json.Marshal(response)
	if err != nil {
		panic(err)
	}
}

func BenchmarkNativeHistoryGraph(b *testing.B) {
	for _, shape := range []string{"wide", "chain"} {
		for _, size := range []int{1, 10, 100, 1000} {
			for _, phase := range []string{"empty-history", "edited-tail"} {
				b.Run(fmt.Sprintf("%s/%d/%s", shape, size, phase), func(b *testing.B) {
					plan, query, source, opts, client := historyBenchmarkFixture(size, shape == "chain")
					client.setSource(size, false)
					var previous *NativeCache
					if phase == "edited-tail" {
						graph, err := BuildNativeCached(context.Background(), query, plan, source, opts, nil)
						if err != nil {
							b.Fatal(err)
						}
						previous = graph.Cache
						client.setSource(size, true)
					}
					b.ReportAllocs()
					b.ResetTimer()
					for b.Loop() {
						graph, err := BuildNativeCached(context.Background(), query, plan, source, opts, previous)
						if err != nil {
							b.Fatal(err)
						}
						if graph.Cache == nil || len(graph.Operations) != size {
							b.Fatal("incomplete graph")
						}
						if phase == "edited-tail" && graph.HistoryHits != size-1 {
							b.Fatalf("got %d hits, want %d", graph.HistoryHits, size-1)
						}
					}
				})
			}
		}
	}
}
