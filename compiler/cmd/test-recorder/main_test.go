package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/grouville/dagger-rust/compiler/model"
)

func TestRecorderKeepsCommandsAndDropsCaptureTransport(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("RCE_CAPTURE_DIR", directory)
	t.Setenv("RCE_RUSTDOC", "/toolchain/bin/rustdoc")
	t.Setenv("DAGGER_SESSION_TOKEN", "must-not-be-recorded")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "must-not-be-recorded")
	t.Setenv("MAKEFLAGS", "must-not-be-recorded")
	for _, name := range []string{"TRACEPARENT", "TRACESTATE", "BAGGAGE"} {
		t.Setenv(name, "must-not-be-recorded")
	}
	t.Setenv("RUST_TEST_THREADS", "2")
	before := os.Args
	t.Cleanup(func() { os.Args = before })
	for _, args := range [][]string{
		{"/opt/test-recorder", "/target/debug/deps/unit-test", "--ignored"},
		{"/opt/rustdoc-recorder", "--test", "base/src/lib.rs"},
	} {
		os.Args = args
		if err := run(); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(filepath.Join(directory, "tests.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d commands", len(lines))
	}
	for i, line := range lines {
		var command model.TestCommand
		if err := json.Unmarshal([]byte(line), &command); err != nil {
			t.Fatal(err)
		}
		if command.Cwd == "" || command.Env["RUST_TEST_THREADS"] != "2" {
			t.Fatalf("lost runtime context: %#v", command)
		}
		for _, name := range []string{"RCE_CAPTURE_DIR", "RCE_RUSTDOC", "DAGGER_SESSION_TOKEN", "OTEL_EXPORTER_OTLP_ENDPOINT", "MAKEFLAGS", "TRACEPARENT", "TRACESTATE", "BAGGAGE"} {
			if _, present := command.Env[name]; present {
				t.Errorf("retained transport %s", name)
			}
		}
		if i == 0 && (command.Executable != "/target/debug/deps/unit-test" || len(command.Args) != 1 || command.Args[0] != "--ignored") {
			t.Fatal("changed harness command")
		}
		if i == 1 && command.Executable != "rustdoc" {
			t.Fatal("did not capture documentation command")
		}
	}
}

func TestResolveRustdoc(t *testing.T) {
	for _, mode := range []string{"direct", "custom", "symlink", "hardlink"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("PATH", dir)
			t.Setenv("RUSTDOC", "")
			t.Setenv("CARGO_BUILD_RUSTDOC", "")
			t.Setenv("RUSTUP_TOOLCHAIN", "selected")
			rustdoc := filepath.Join(dir, "rustdoc")
			rustup := filepath.Join(dir, "rustup")
			selected := filepath.Join(dir, "toolchain", "bin", "rustdoc")
			script := "#!/bin/sh\n[ \"$1\" = which ] && [ \"$2\" = rustdoc ] && [ \"$RUSTUP_TOOLCHAIN\" = selected ] || exit 12\nprintf '%s\\n' '" + selected + "'\n"
			if err := os.WriteFile(rustup, []byte(script), 0755); err != nil {
				t.Fatal(err)
			}
			var err error
			want := rustdoc
			switch mode {
			case "symlink":
				err = os.Symlink(rustup, rustdoc)
				want = selected
			case "hardlink":
				err = os.Link(rustup, rustdoc)
				want = selected
			case "custom":
				rustdoc = filepath.Join(dir, "custom-rustdoc")
				t.Setenv("RUSTDOC", rustdoc)
				want = rustdoc
				fallthrough
			default:
				err = os.WriteFile(rustdoc, []byte("#!/bin/sh\nexit 13\n"), 0755)
			}
			if err != nil {
				t.Fatal(err)
			}
			got, err := resolveRustdoc()
			if err != nil || got != want {
				t.Fatalf("got %q, %v; want %q", got, err, want)
			}
		})
	}
}
