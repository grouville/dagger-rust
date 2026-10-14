package replay

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/grouville/dagger-rust/compiler/model"
)

func testPlan() (*model.Plan, Source) {
	source := Source{"Cargo.toml": {Contents: "[workspace]", Mode: 0644}, "a/Cargo.toml": {Contents: "[package]", Mode: 0644}, "a/src/lib.rs": {Contents: "pub fn a() {}", Mode: 0644}}
	plan := &model.Plan{Version: model.Version, Image: DefaultImage, ConfigDigest: source.ConfigDigest(), Packages: []model.Package{{Root: "/src/a"}, {Root: "/src/b"}}}
	plan.Actions = []model.Action{
		{ID: "b", PackageRoot: "/src/b", Crate: "b", Compiler: "/bin/rustc", Cwd: "/src", Args: []string{"--crate-type", "lib", "--extern", "a=/target/liba.rlib"}, Inputs: []string{"/src/b/src/lib.rs"}, Outputs: []string{"/target/libb.rlib"}},
		{ID: "a", PackageRoot: "/src/a", Crate: "a", Compiler: "/bin/rustc", Cwd: "/src", Args: []string{"--crate-type=lib"}, Inputs: []string{"/src/a/src/lib.rs"}, Outputs: []string{"/target/liba.rlib"}},
	}
	return plan, source
}

func TestPlanDerivesDependenciesAndRejectsCycles(t *testing.T) {
	plan, source := testPlan()
	if err := Validate(plan, source); err != nil {
		t.Fatal(err)
	}
	ordered, err := ordered(plan.Actions)
	if err != nil {
		t.Fatal(err)
	}
	if ordered[0].ID != "a" || ordered[1].ID != "b" {
		t.Fatalf("order: %v", ordered)
	}
	plan.Actions[1].Args = append(plan.Actions[1].Args, "--extern=b=/target/libb.rlib")
	if err := Validate(plan, source); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("expected cycle error, got %v", err)
	}
}

func TestConfigurationAndSourceEdits(t *testing.T) {
	plan, source := testPlan()
	source["a/src/lib.rs"] = SourceFile{Contents: "pub fn a() { println!(\"edit\"); }", Mode: 0644}
	if err := Validate(plan, source); err != nil {
		t.Fatal("source edit should reuse plan:", err)
	}
	source["a/Cargo.toml"] = SourceFile{Contents: "changed manifest", Mode: 0644}
	if err := Validate(plan, source); err == nil || !strings.Contains(err.Error(), "recapture") {
		t.Fatalf("expected recapture, got %v", err)
	}
}

func TestUnsupportedInputs(t *testing.T) {
	for name, change := range map[string]func(*model.Plan){
		"outside source":        func(p *model.Plan) { p.Actions[0].Inputs = []string{"/src/a/data.txt"} },
		"unrecorded dependency": func(p *model.Plan) { p.Actions[0].Args = append(p.Actions[0].Args, "--extern=c=/target/libc.rlib") },
		"proc macro":            func(p *model.Plan) { p.Actions[0].Args = []string{"--crate-type=proc-macro"} },
		"incremental":           func(p *model.Plan) { p.Actions[0].Args = append(p.Actions[0].Args, "-Cincremental=/target/inc") },
		"custom target":         func(p *model.Plan) { p.Actions[0].Args = append(p.Actions[0].Args, "--target=/src/target.json") },
	} {
		t.Run(name, func(t *testing.T) {
			plan, source := testPlan()
			change(plan)
			if err := Validate(plan, source); err == nil {
				t.Fatal("accepted unsupported plan")
			}
		})
	}
}

func TestUnsupportedPackages(t *testing.T) {
	for _, contents := range []string{
		`{"packages":[{"name":"external","source":"registry+url","manifest_path":"/src/external/Cargo.toml"}]}`,
		`{"packages":[{"name":"generated","manifest_path":"/src/generated/Cargo.toml","targets":[{"kind":["custom-build"]}]}]}`,
		`{"packages":[{"name":"macro","manifest_path":"/src/macro/Cargo.toml","targets":[{"kind":["proc-macro"]}]}]}`,
	} {
		if _, err := parseMetadata(contents); err == nil {
			t.Fatal("accepted unsupported metadata:", contents)
		}
	}
}

func TestReadSourceIgnoresBuildStateAndRejectsSymlinks(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Cargo.toml"), []byte("[workspace]"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "target"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "target", "binary"), []byte{0xff}, 0644); err != nil {
		t.Fatal(err)
	}
	source, err := ReadSource(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(source) != 1 {
		t.Fatalf("unexpected build inputs: %v", source)
	}
	if err := os.Symlink("Cargo.toml", filepath.Join(dir, "alias")); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadSource(dir); err == nil {
		t.Fatal("accepted a symlink")
	}
}

func TestPlanRoundTrip(t *testing.T) {
	plan, source := testPlan()
	data, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	var restored model.Plan
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatal(err)
	}
	if err := Validate(&restored, source); err != nil {
		t.Fatal(err)
	}
}
