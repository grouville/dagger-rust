package replay

import (
	"testing"

	"github.com/grouville/dagger-rust/compiler/model"
)

func TestNativePlanOptions(t *testing.T) {
	plan, _ := testPlan()
	plan.CargoCommand = "check"
	plan.CargoArgs = []string{"--features", "extra"}
	opts := CaptureOptions{Image: plan.Image, Command: "check", CargoArgs: plan.CargoArgs}
	if !planOptionsMatch(plan, opts) {
		t.Fatal("matching compilation options should reuse the plan")
	}
	for name, change := range map[string]func(*model.Plan){
		"version":     func(p *model.Plan) { p.Version++ },
		"image":       func(p *model.Plan) { p.Image = "another image" },
		"command":     func(p *model.Plan) { p.CargoCommand = "build" },
		"flags":       func(p *model.Plan) { p.CargoArgs = []string{"--release"} },
		"environment": func(p *model.Plan) { p.Environment = map[string]string{"RUSTFLAGS": "-C opt-level=1"} },
	} {
		t.Run(name, func(t *testing.T) {
			changed := *plan
			change(&changed)
			if planOptionsMatch(&changed, opts) {
				t.Fatal("reused a plan with different compilation options")
			}
		})
	}
}

func TestNativeTestPlanNeedsSelectedRustdoc(t *testing.T) {
	plan, _ := testPlan()
	plan.CargoCommand = "test"
	opts := CaptureOptions{Image: plan.Image, Command: "test", CargoArgs: plan.CargoArgs}
	if planOptionsMatch(plan, opts) {
		t.Fatal("an old test plan must be refreshed to record its rustdoc program")
	}
	plan.Rustdoc = "/toolchain/bin/rustdoc"
	if !planOptionsMatch(plan, opts) {
		t.Fatal("a matching test plan should be reused")
	}
}
