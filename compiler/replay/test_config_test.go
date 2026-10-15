package replay

import (
	"testing"

	"github.com/grouville/dagger-rust/compiler/model"
)

func TestTestCaptureDoesNotReplaceConfiguredCommands(t *testing.T) {
	for _, config := range []string{
		`[target.x86_64-unknown-linux-gnu]
runner = "custom-runner"`,
		`[target.'cfg(unix)']
runner = ["custom-runner", "--flag"]`,
		"[build]\nrustdoc = 'custom-rustdoc'",
		"[env]\nRUSTDOC = 'custom-rustdoc'",
		"[env]\nCARGO_BUILD_RUSTDOC = { value = 'custom-rustdoc', force = true }",
	} {
		if err := validateTestConfiguration(Source{".cargo/config.toml": {Contents: config}}); err == nil {
			t.Fatal("silently replaced a configured Cargo command")
		}
	}
	if err := validateTestConfiguration(Source{".cargo/config.toml": {Contents: "# runner is documented here\n[build]\njobs = 2"}}); err != nil {
		t.Fatal(err)
	}
}

func TestTestCommandsMustUseCapturedOutputsAndCleanWorkingDirectories(t *testing.T) {
	plan, source := testPlan()
	plan.CargoCommand = "test"
	for _, command := range []struct{ executable, cwd string }{
		{"/target/unknown", "/src/a"},
		{"/target/liba.rlib", "/src/../../outside"},
	} {
		plan.Tests = []model.TestCommand{{Executable: command.executable, Cwd: command.cwd}}
		if err := Validate(plan, source); err == nil {
			t.Fatalf("accepted %+v", command)
		}
	}
}
