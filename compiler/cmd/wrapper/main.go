// rustc-wrapper forwards Cargo's compiler commands and records successful actions.
// It deliberately has no Dagger dependency: recording and replay are separate.
package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/grouville/dagger-rust/compiler/model"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "rustc-wrapper: missing compiler")
		os.Exit(2)
	}
	if err := run(os.Args[1], os.Args[2:]); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			os.Exit(exit.ExitCode())
		}
		fmt.Fprintln(os.Stderr, "rustc-wrapper:", err)
		os.Exit(1)
	}
}

func run(compiler string, args []string) error {
	crate := model.Option(args, "--crate-name")
	// Cargo also invokes rustc for version, target and sysroot probes.
	record := crate != "" && model.Option(args, "--out-dir") != ""
	if record {
		args = artifactMessages(args)
	}
	cmd := exec.Command(compiler, args...)
	cmd.Stdin, cmd.Stdout = os.Stdin, os.Stdout
	var stderr bytes.Buffer
	started := time.Now()
	timing := artifactTiming{started: started}
	cmd.Stderr = io.MultiWriter(os.Stderr, &stderr, &timing)
	if err := cmd.Run(); err != nil {
		return err
	}
	compilerSeconds := time.Since(started).Seconds()
	if !record {
		return nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	a := model.Action{Crate: crate, Compiler: compiler, Args: args, Cwd: cwd, PackageRoot: os.Getenv("CARGO_MANIFEST_DIR"),
		CompilerSeconds: compilerSeconds, CompilerStartedUnixNanos: started.UnixNano(), MetadataSeconds: timing.metadataSeconds}
	a.Outputs, err = artifacts(stderr.Bytes(), cwd)
	if err != nil {
		return err
	}
	// rustc does not report dep-info as an artifact on every supported release.
	depfile := path.Join(model.Option(args, "--out-dir"), crate+codegenOption(args, "extra-filename")+".d")
	depfile = model.Absolute(cwd, depfile)
	contents, err := os.ReadFile(depfile)
	if err != nil {
		return fmt.Errorf("read dep-info %s: %w", depfile, err)
	}
	if !contains(a.Outputs, depfile) {
		a.Outputs = append(a.Outputs, depfile)
	}
	var envDeps map[string]*string
	a.Inputs, envDeps, err = model.DepInfo(string(contents), cwd)
	if err != nil {
		return err
	}
	a.Env, a.UnsetEnv, err = environment(os.Environ(), envDeps)
	if err != nil {
		return err
	}
	sort.Strings(a.Outputs)
	id := sha256.Sum256([]byte(strings.Join(a.Outputs, "\x00")))
	a.ID = crate + "-" + hex.EncodeToString(id[:8])
	data, err := json.Marshal(a)
	if err != nil {
		return err
	}
	dir := os.Getenv("RCE_CAPTURE_DIR")
	if dir == "" {
		return errors.New("RCE_CAPTURE_DIR is unset")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	// Each invocation writes its own record; no ordering or shared append lock.
	f, err := os.CreateTemp(dir, ".action-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path.Join(dir, a.ID+".json"))
}

// rustc announces metadata on stderr before finishing code generation. Preserve
// the original stream and handle messages split across writes without rescanning
// the complete diagnostic buffer. Timing remains capture-only information.
type artifactTiming struct {
	started         time.Time
	pending         []byte
	metadataSeconds float64
}

func (t *artifactTiming) Write(data []byte) (int, error) {
	if t.metadataSeconds != 0 {
		return len(data), nil
	}
	t.pending = append(t.pending, data...)
	for {
		end := bytes.IndexByte(t.pending, '\n')
		if end < 0 {
			break
		}
		var message struct {
			Type string `json:"$message_type"`
			Emit string `json:"emit"`
		}
		if json.Unmarshal(t.pending[:end], &message) == nil && message.Type == "artifact" && message.Emit == "metadata" {
			t.metadataSeconds = time.Since(t.started).Seconds()
			t.pending = nil
			break
		}
		t.pending = t.pending[end+1:]
	}
	return len(data), nil
}

func artifactMessages(args []string) []string {
	args = append([]string(nil), args...)
	for i, arg := range args {
		if arg == "--json" && i+1 < len(args) {
			if !contains(strings.Split(args[i+1], ","), "artifacts") {
				args[i+1] += ",artifacts"
			}
			return args
		}
		if value, ok := strings.CutPrefix(arg, "--json="); ok {
			if !contains(strings.Split(value, ","), "artifacts") {
				args[i] += ",artifacts"
			}
			return args
		}
	}
	return append(args, "--error-format=json", "--json=artifacts")
}

func artifacts(stderr []byte, cwd string) ([]string, error) {
	var outputs []string
	scanner := bufio.NewScanner(bytes.NewReader(stderr))
	scanner.Buffer(make([]byte, 4096), 16*1024*1024)
	for scanner.Scan() {
		var message struct {
			Type     string `json:"$message_type"`
			Artifact string `json:"artifact"`
		}
		if json.Unmarshal(scanner.Bytes(), &message) == nil && message.Type == "artifact" {
			filename := model.Absolute(cwd, message.Artifact)
			if !contains(outputs, filename) {
				outputs = append(outputs, filename)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(outputs) == 0 {
		return nil, errors.New("compiler emitted no artifact messages")
	}
	return outputs, nil
}

func codegenOption(args []string, name string) string {
	for i, arg := range args {
		value := ""
		if arg == "-C" && i+1 < len(args) {
			value = args[i+1]
		}
		if strings.HasPrefix(arg, "-C") && len(arg) > 2 {
			value = arg[2:]
		}
		if result, ok := strings.CutPrefix(value, name+"="); ok {
			return result
		}
	}
	return ""
}

func environment(environ []string, deps map[string]*string) (map[string]string, []string, error) {
	values := map[string]string{}
	all := map[string]string{}
	for _, entry := range environ {
		key, value, _ := strings.Cut(entry, "=")
		all[key] = value
		keep := strings.HasPrefix(key, "CARGO_PKG_") || strings.HasPrefix(key, "CARGO_MANIFEST_")
		switch key {
		case "PATH", "HOME", "USER", "LOGNAME", "LANG", "LC_ALL", "LD_LIBRARY_PATH", "RUSTUP_HOME", "RUSTUP_TOOLCHAIN", "CARGO_HOME", "CARGO", "CARGO_CRATE_NAME", "CARGO_BIN_NAME", "CARGO_PRIMARY_PACKAGE":
			keep = true
		}
		if keep {
			values[key] = value
		}
	}
	var unset []string
	for key, value := range deps {
		if strings.HasPrefix(key, "DAGGER_") || strings.HasPrefix(key, "OTEL_") || key == "CARGO_MAKEFLAGS" || key == "MAKEFLAGS" || key == "RCE_CAPTURE_DIR" || key == "RUSTC_WRAPPER" {
			return nil, nil, fmt.Errorf("unsupported transport environment input %s", key)
		}
		if value == nil {
			unset = append(unset, key)
		} else {
			// dep-info escapes newlines and backslashes in values. The wrapper
			// inherits the compiler's actual environment, so preserve it directly.
			actual, ok := all[key]
			if !ok {
				return nil, nil, fmt.Errorf("environment dependency %s was not inherited by rustc", key)
			}
			values[key] = actual
		}
	}
	sort.Strings(unset)
	return values, unset, nil
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
