package replay

import (
	"testing"

	"github.com/grouville/dagger-rust/compiler/model"
)

func TestIncrementalSeedCompatibility(t *testing.T) {
	plan, source := testPlan()
	if err := Validate(plan, source); err != nil {
		t.Fatal(err)
	}
	key, err := SeedCompatibility(plan, "a")
	if err != nil {
		t.Fatal(err)
	}
	seed := IncrementalState{Version: 1, Crate: "a", Compatibility: key, DirectoryID: "native-directory"}
	// Diagnostics and recorded dep-info paths do not describe compatibility.
	plan.CaptureSeconds = 5
	plan.Actions[0].CompilerSeconds = 8
	plan.Actions[0].CompilerStartedUnixNanos = 123
	plan.Actions[0].MetadataSeconds = 3
	plan.Actions[0].Inputs = []string{"/src/b/new-module.rs"}
	plan.BaselineDigests = map[string]string{"/target/liba.rlib": "changed"}
	plan.NativeConfigDigest = "native configuration validation metadata"
	if err := seed.Validate(plan, "a"); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*model.Plan){
		"toolchain":     func(p *model.Plan) { p.RustcVersion = "different" },
		"configuration": func(p *model.Plan) { p.ConfigDigest = "different" },
		"flags":         func(p *model.Plan) { p.Actions[0].Args = append(p.Actions[0].Args, "-Copt-level=1") },
		"environment":   func(p *model.Plan) { p.Environment = map[string]string{"RUSTFLAGS": "different"} },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := *plan
			candidate.Actions = append([]model.Action(nil), plan.Actions...)
			change(&candidate)
			if err := seed.Validate(&candidate, "a"); err == nil {
				t.Fatal("accepted incompatible seed")
			}
		})
	}
	if err := seed.Validate(plan, "b"); err == nil {
		t.Fatal("accepted another crate's seed")
	}
	if _, err := SeedCompatibility(plan, "missing"); err == nil {
		t.Fatal("accepted missing crate")
	}
	plan.Actions = append(plan.Actions, plan.Actions[1])
	if _, err := SeedCompatibility(plan, "a"); err == nil {
		t.Fatal("accepted ambiguous crate")
	}
}
