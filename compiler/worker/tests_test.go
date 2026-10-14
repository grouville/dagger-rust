package worker

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/grouville/dagger-rust/compiler/model"
)

func TestRunTestsPreservesEnvironmentArgumentsAndFilesystem(t *testing.T) {
	t.Setenv("UNRECORDED", "must not reach tests")
	root := t.TempDir()
	value := "quote ' and newline\n$literal"
	commands := []model.TestCommand{
		{Executable: "/bin/sh", Cwd: root, Env: map[string]string{"VALUE": value, "TRACEPARENT": "old-session"},
			Args: []string{"-c", `test -z "$UNRECORDED" && test -z "$TRACEPARENT" && test "$VALUE" = "$1" && printf '%s' "$1" > state`, "test", value}},
		{Executable: "/bin/sh", Cwd: root, Env: map[string]string{"VALUE": "next"},
			Args: []string{"-c", `test "$VALUE" = next && /bin/cat state`}},
	}
	var stdout, stderr bytes.Buffer
	if err := RunTests(context.Background(), commands, &stdout, &stderr); err != nil {
		t.Fatalf("commands failed: %v: %s", err, stderr.String())
	}
	contents, err := os.ReadFile(filepath.Join(root, "state"))
	if err != nil || string(contents) != value || !bytes.HasSuffix(stdout.Bytes(), []byte(value)) {
		t.Fatalf("state or output changed: %q, %v, %q", contents, err, stdout.String())
	}
}

func TestRunTestsStopsOnFailure(t *testing.T) {
	root := t.TempDir()
	commands := []model.TestCommand{
		{Executable: "/bin/sh", Cwd: root, Args: []string{"-c", "exit 17"}},
		{Executable: "/bin/sh", Cwd: root, Args: []string{"-c", "touch later"}},
	}
	var stdout, stderr bytes.Buffer
	if err := RunTests(context.Background(), commands, &stdout, &stderr); err == nil {
		t.Fatal("test failure was swallowed")
	}
	if _, err := os.Stat(filepath.Join(root, "later")); !os.IsNotExist(err) {
		t.Fatal("a command ran after failure")
	}
}

func TestRunTestsCancellationStopsTheProcess(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	var stdout, stderr bytes.Buffer
	commands := []model.TestCommand{{Executable: "/bin/sleep", Cwd: t.TempDir(), Args: []string{"10"}}}
	if err := RunTests(ctx, commands, &stdout, &stderr); err == nil {
		t.Fatal("canceled test returned success")
	}
	if ctx.Err() == nil {
		t.Fatal("test failed before cancellation")
	}
}
