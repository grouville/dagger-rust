package replay

import (
	"reflect"
	"testing"

	"github.com/grouville/dagger-rust/compiler/model"
)

func TestBuildPublicationsUseCargoNamesAndWorkspaceMembership(t *testing.T) {
	metadata := `{"workspace_members":["app"]}`
	actions := []model.Action{
		{ID: "binary", Crate: "my_app", PackageRoot: "/src/app", Inputs: []string{"/src/app/src/main.rs"}, Outputs: []string{"/target/debug/deps/my_app-123.d", "/target/debug/deps/my_app-123"}},
		{ID: "library", Crate: "app", PackageRoot: "/src/app", Inputs: []string{"/src/app/src/lib.rs"}, Outputs: []string{"/target/debug/deps/libapp-456.rlib", "/target/debug/deps/libapp-456.rmeta"}},
	}
	messages := `unrelated output
{"reason":"compiler-artifact","package_id":"app","manifest_path":"/src/app/Cargo.toml","target":{"src_path":"/src/app/src/main.rs","name":"my-app","kind":["bin"]},"filenames":["/target/debug/my-app"],"fresh":true}
{"reason":"compiler-artifact","package_id":"app","manifest_path":"/src/app/Cargo.toml","target":{"src_path":"/src/app/src/lib.rs","name":"app","kind":["lib"]},"filenames":["/target/debug/libapp.rlib","/target/debug/deps/libapp-456.rmeta"]}
{"reason":"compiler-artifact","package_id":"dependency","filenames":["/target/debug/libdependency.rlib"]}
{"reason":"build-finished","success":true}`
	got, err := buildPublications(metadata, messages, actions, false)
	if err != nil {
		t.Fatal(err)
	}
	want := []model.Publication{{Output: "/target/debug/deps/libapp-456.rlib", Path: "debug/libapp.rlib"}, {Output: "/target/debug/deps/my_app-123", Path: "debug/my-app"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestBuildPublicationRejectsUncapturedAndUnsafeOutputs(t *testing.T) {
	plan, source := testPlan()
	for _, publication := range []model.Publication{
		{Output: "/target/liba.rlib", Path: "../escape"},
		{Output: "/target/liba.rlib", Path: "/absolute"},
		{Output: "/target/missing", Path: "debug/result"},
	} {
		plan.Publications = []model.Publication{publication}
		if err := Validate(plan, source); err == nil {
			t.Errorf("accepted %#v", publication)
		}
	}
	plan.Publications = []model.Publication{{Output: "/target/liba.rlib", Path: "debug/liba.rlib"}, {Output: "/target/libb.rlib", Path: "debug/liba.rlib"}}
	if err := Validate(plan, source); err == nil {
		t.Fatal("accepted duplicate final paths")
	}
}
