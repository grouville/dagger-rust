package replay

import (
	"fmt"
	"path"
	"reflect"
	"strings"
	"testing"

	"github.com/grouville/dagger-rust/compiler/model"
)

func TestPackageSourcesPreservesOwnership(t *testing.T) {
	packages := []model.Package{{Root: "/src"}, {Root: "/src/app"}, {Root: "/src/app/nested"}, {Root: "/src/app2"}}
	source := Source{
		"Cargo.toml":            {Contents: "workspace", Mode: 0644},
		"app/Cargo.toml":        {Contents: "app", Mode: 0644},
		"app/src/main.rs":       {Contents: "fn main() {}", Mode: 0664},
		"app/nested/src/lib.rs": {Contents: "nested", Mode: 0600},
		"app2/src/lib.rs":       {Contents: "sibling", Mode: 0644},
	}
	if got, want := source.packageSources(packages), scanPackageSources(source, packages); !reflect.DeepEqual(got, want) {
		t.Fatalf("package ownership changed:\ngot %#v\nwant %#v", got, want)
	}
	withoutWorkspace := source.packageSources(packages[1:])
	if len(withoutWorkspace) != 3 || len(withoutWorkspace["/src/app"]) != 2 {
		t.Fatalf("workspace files or nested sources leaked into a package: %#v", withoutWorkspace)
	}
	roots := newPackageRoots(packages[1:])
	for filename, want := range map[string]string{
		"/src/app/src/main.rs":       "/src/app",
		"/src/app/nested/src/lib.rs": "/src/app/nested",
		"/src/app2/src/lib.rs":       "/src/app2",
		"/src/application/file.rs":   "",
		"/outside/src/app/file.rs":   "",
	} {
		if got := roots.owner(filename); got != want {
			t.Errorf("owner(%q) = %q, want %q", filename, got, want)
		}
	}
}

// Keep the previous per-crate scan here to compare scaling, not just the cost
// of indexing one tiny fixture. It also supplies the independent ownership
// reference used above.
func scanPackageSources(source Source, packages []model.Package) map[string]Source {
	result := map[string]Source{}
	for _, pkg := range packages {
		for _, name := range source.names() {
			absolute := path.Join(model.SourceRoot, name)
			var root string
			for _, candidate := range packages {
				if model.Within(candidate.Root, absolute) && len(candidate.Root) > len(root) {
					root = candidate.Root
				}
			}
			if root != pkg.Root {
				continue
			}
			if result[root] == nil {
				result[root] = Source{}
			}
			result[root][strings.TrimPrefix(absolute, root+"/")] = source[name]
		}
	}
	return result
}

func BenchmarkPackageSources(b *testing.B) {
	for _, count := range []int{10, 100, 300} {
		source := Source{}
		packages := make([]model.Package, count)
		for i := range packages {
			name := fmt.Sprintf("crate%03d", i)
			packages[i] = model.Package{Root: "/src/" + name}
			for j := range 16 {
				source[fmt.Sprintf("%s/src/file%02d.rs", name, j)] = SourceFile{Contents: "pub fn example() {}", Mode: 0644}
			}
		}
		for name, collect := range map[string]func(Source, []model.Package) map[string]Source{
			"previous-scan": scanPackageSources,
			"index":         Source.packageSources,
		} {
			b.Run(fmt.Sprintf("%d/%s", count, name), func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if got := collect(source, packages); len(got) != count {
						b.Fatal("lost package sources")
					}
				}
			})
		}
	}
}
