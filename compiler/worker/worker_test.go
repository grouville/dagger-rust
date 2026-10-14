package worker

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/grouville/dagger-rust/compiler/model"
)

func TestCleanupPrivateTargetsPreservesPublishedArtifacts(t *testing.T) {
	target := t.TempDir()
	private := filepath.Join(target, ".rcexp-private", "crate", "debug", "deps")
	if err := os.MkdirAll(private, 0755); err != nil {
		t.Fatal(err)
	}
	produced := filepath.Join(private, "artifact")
	if err := os.WriteFile(produced, []byte("compiled output"), 0751); err != nil {
		t.Fatal(err)
	}
	published := filepath.Join(target, "artifact")
	if err := publish(produced, published); err != nil {
		t.Fatal(err)
	}
	if err := cleanupPrivateTargets(target); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(target, ".rcexp-private")); !os.IsNotExist(err) {
		t.Fatalf("private targets remain: %v", err)
	}
	contents, err := os.ReadFile(published)
	if err != nil || string(contents) != "compiled output" {
		t.Fatalf("published output lost: %q, %v", contents, err)
	}
	info, err := os.Stat(published)
	if err != nil || info.Mode().Perm() != 0751 {
		t.Fatalf("published output mode changed: %v, %v", info, err)
	}
}

func TestMetadataAllowsLibraryButBinaryWaitsForTransitiveCodegen(t *testing.T) {
	a := model.Action{ID: "a", Outputs: []string{"a.rmeta"}}
	b := model.Action{ID: "b", Outputs: []string{"b.rmeta"}, Dependencies: []string{"a"}, Args: []string{"--extern", "a=a.rmeta"}}
	app := model.Action{ID: "app", Outputs: []string{"app"}, Dependencies: []string{"b"}}
	all := map[string]model.Action{"a": a, "b": b, "app": app}
	metadata := map[string]bool{"a": true, "b": true}
	complete := map[string]bool{"b": true}
	if !ready(b, all, complete, metadata, true) {
		t.Fatal("library could not start with metadata")
	}
	if ready(b, all, complete, metadata, false) {
		t.Fatal("disabled pipelining started before complete dependency")
	}
	if ready(app, all, complete, metadata, true) {
		t.Fatal("binary started before transitive machine code was ready")
	}
	complete["a"] = true
	if !ready(app, all, complete, metadata, true) {
		t.Fatal("binary could not start with complete dependencies")
	}
	delete(all, "a") // An exact result has already been mounted by the caller.
	delete(complete, "a")
	if !ready(b, all, complete, metadata, false) {
		t.Fatal("cached dependency unnecessarily delayed compiler")
	}
}

func TestExplicitRlibDoesNotStartWithOnlyMetadata(t *testing.T) {
	a := model.Action{ID: "a", Outputs: []string{"a.rmeta"}}
	b := model.Action{ID: "b", Outputs: []string{"b.rmeta"}, Dependencies: []string{"a"}, Args: []string{"--extern", "a=a.rlib"}}
	if ready(b, map[string]model.Action{"a": a}, nil, map[string]bool{"a": true}, true) {
		t.Fatal("an explicit rlib request started before machine code was ready")
	}
}

func TestNewInputsMustStayWithinPackageOwnership(t *testing.T) {
	action := model.Action{PackageRoot: "/src/app"}
	packages := []model.Package{{Root: "/src/app"}, {Root: "/src/app/nested"}, {Root: "/src/other"}}
	if err := validateInputs(action, packages, []string{"/src/app/src/main.rs", "/src/app/resource.txt"}); err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{"/src/other/src/lib.rs", "/src/app2/resource.txt", "/src/app/nested/src/lib.rs", "/target/debug/deps/generated.rs"} {
		if err := validateInputs(action, packages, []string{input}); err == nil {
			t.Errorf("accepted source outside package ownership: %s", input)
		}
	}
}

func TestSymlinkCannotReadAnotherPackagesSources(t *testing.T) {
	root := t.TempDir()
	for _, pkg := range []string{"app", "other"} {
		if err := os.Mkdir(filepath.Join(root, pkg), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, pkg, "value.rs"), []byte("pub const VALUE: u64 = 1;"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	for name, target := range map[string]string{"own.rs": "value.rs", "foreign.rs": "../other/value.rs"} {
		if err := os.Symlink(target, filepath.Join(root, "app", name)); err != nil {
			t.Fatal(err)
		}
	}
	resolve := func(input string) (string, error) {
		resolved, err := filepath.EvalSymlinks(filepath.Join(root, strings.TrimPrefix(input, "/src/")))
		return "/src/" + strings.TrimPrefix(resolved, root+"/"), err
	}
	action := model.Action{PackageRoot: "/src/app"}
	packages := []model.Package{{Root: "/src/app"}, {Root: "/src/other"}}
	if err := validateSourceInputs(action, packages, []string{"/src/app/own.rs"}, resolve); err != nil {
		t.Fatal(err)
	}
	if err := validateSourceInputs(action, packages, []string{"/src/app/foreign.rs"}, resolve); err == nil {
		t.Fatal("a symlink let an unchanged crate read an independently cached package")
	}
}

func TestCompilerEnvironmentUsesAssignmentsAndRemovals(t *testing.T) {
	action := model.Action{Env: map[string]string{"NEW": "value with\nspaces", "REMOVE": "assigned"}, UnsetEnv: []string{"REMOVE"}}
	got := environment(action, []string{"PATH=/bin", "REMOVE=inherited", "NEW=previous"})
	want := []string{"NEW=value with\nspaces", "PATH=/bin"}
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("compiler environment: %q", got)
	}
}
