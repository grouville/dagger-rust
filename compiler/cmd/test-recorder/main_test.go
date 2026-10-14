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
		for _, name := range []string{"RCE_CAPTURE_DIR", "DAGGER_SESSION_TOKEN", "OTEL_EXPORTER_OTLP_ENDPOINT", "MAKEFLAGS", "TRACEPARENT", "TRACESTATE", "BAGGAGE"} {
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
