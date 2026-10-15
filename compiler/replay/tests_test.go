package replay

import (
	"reflect"
	"testing"

	"github.com/grouville/dagger-rust/compiler/model"
)

func TestTestCommandsUseCapturedRustdocWithoutChangingThePlan(t *testing.T) {
	commands := []model.TestCommand{
		{Executable: "/target/debug/deps/unit", Cwd: "/src/app", Args: []string{"--ignored"}},
		{Executable: "rustdoc", Cwd: "/src", Args: []string{"--test", "app/src/lib.rs"}, Env: map[string]string{"RUSTUP_TOOLCHAIN": "chosen"}},
	}
	plan := &model.Plan{Rustdoc: "/toolchain/bin/rustdoc", Tests: commands}
	selected, err := testCommands(plan)
	if err != nil {
		t.Fatal(err)
	}
	if selected[1].Executable != plan.Rustdoc || commands[1].Executable != "rustdoc" {
		t.Fatal("selected rustdoc was lost or the original plan was changed")
	}
	selected[1].Executable = "rustdoc"
	if !reflect.DeepEqual(commands, selected) {
		t.Fatal("changed test arguments, environment, cwd or ordering")
	}
	plan.Rustdoc = ""
	legacy, err := testCommands(plan)
	if err != nil || !reflect.DeepEqual(commands, legacy) {
		t.Fatal("legacy explicit plans must remain usable", err)
	}
	for _, path := range []string{"relative/rustdoc", "/src/rustdoc", "/target/rustdoc", "/toolchain/../rustdoc"} {
		plan.Rustdoc = path
		if _, err := testCommands(plan); err == nil {
			t.Fatalf("accepted a rustdoc outside the pinned toolchain: %s", path)
		}
	}
}
