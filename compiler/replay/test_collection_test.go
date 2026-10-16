package replay

import (
	"reflect"
	"testing"

	"github.com/grouville/dagger-rust/compiler/model"
)

func TestCollectionListingAndSelection(t *testing.T) {
	plan := &model.Plan{
		Rustdoc:  "/toolchain/rustdoc",
		Packages: []model.Package{{Name: "app", Root: "/src/app"}},
		Actions:  []model.Action{{PackageRoot: "/src/app", Cwd: "/src", Args: []string{"app/src/lib.rs", "--test"}, Outputs: []string{"/target/debug/deps/app"}}},
		Tests: []model.TestCommand{
			{Executable: "/target/debug/deps/app", Cwd: "/src/app", Env: map[string]string{"KEEP": "yes"}},
			{Executable: "rustdoc", Cwd: "/src", Args: []string{"--test", "app/src/lib.rs"}},
		},
	}
	targets, err := CollectionTargets(plan)
	if err != nil {
		t.Fatal(err)
	}
	listing := CollectionListing(targets)
	if len(listing) != 1 || !reflect.DeepEqual(listing[0].Args, []string{"--list", "--format=terse"}) {
		t.Fatalf("listing: %#v", listing)
	}
	if err := ReadCollectionListing(targets, "\n==> /target/debug/deps/app\nleft::same: test\nright::same: test\n2 tests, 0 benchmarks\n"); err != nil {
		t.Fatal(err)
	}
	keys := CollectionKeys(targets[0])
	selected, err := CollectionSelection(targets, "app", keys)
	if err != nil || len(selected) != 1 || !reflect.DeepEqual(selected[0], plan.Tests[0]) {
		t.Fatalf("whole harness changed: %#v, %v", selected, err)
	}
	selected, err = CollectionSelection(targets, "app", keys[:1])
	if err != nil || len(selected) != 1 || !reflect.DeepEqual(selected[0].Args, []string{"--exact", "--skip", "right::same"}) || selected[0].Env["KEEP"] != "yes" {
		t.Fatalf("subset: %#v, %v", selected, err)
	}
	if len(plan.Tests[0].Args) != 0 || len(targets[0].Command.Args) != 0 {
		t.Fatal("selection mutated captured command")
	}
	docs, err := CollectionSelection(targets, "app", CollectionKeys(targets[1]))
	if err != nil || len(docs) != 1 || docs[0].Executable != plan.Rustdoc {
		t.Fatalf("rustdoc: %#v, %v", docs, err)
	}
	for _, pkg := range []string{"app", "other"} {
		if _, err := CollectionSelection(targets, pkg, []string{"missing"}); err == nil {
			t.Fatal("accepted an unknown selection")
		}
	}
	if empty, err := CollectionSelection(targets, "app", nil); err != nil || len(empty) != 0 {
		t.Fatal("empty selection starts tests", err)
	}
}

func TestCollectionListingRejectsMissingDuplicateAndCustomHarnesses(t *testing.T) {
	for _, output := range []string{"", "==> /unexpected\n", "==> /target/unit\na: test\na: test\n", "==> /target/unit\n==> /target/unit\n"} {
		targets := []CollectionTarget{{Command: model.TestCommand{Executable: "/target/unit"}}}
		if err := ReadCollectionListing(targets, output); err == nil {
			t.Fatalf("accepted %q", output)
		}
	}
	plan := &model.Plan{
		Packages: []model.Package{{Name: "app", Root: "/src/app"}},
		Actions:  []model.Action{{PackageRoot: "/src/app", Cwd: "/src", Args: []string{"app/src/lib.rs"}, Outputs: []string{"/target/unit"}}},
		Tests:    []model.TestCommand{{Executable: "/target/unit", Cwd: "/src/app"}},
	}
	if _, err := CollectionTargets(plan); err == nil {
		t.Fatal("custom harness has no libtest listing contract")
	}
}
