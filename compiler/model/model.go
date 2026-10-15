// Package model describes a captured Cargo build without retaining its build cache.
package model

import (
	"bufio"
	"fmt"
	"path"
	"sort"
	"strings"
)

const (
	Version     = 1
	SourceRoot  = "/src"
	TargetRoot  = "/target"
	CaptureRoot = "/capture"
)

type Plan struct {
	Version            int               `json:"version"`
	Image              string            `json:"image"`
	RustcVersion       string            `json:"rustc_version"`
	Rustdoc            string            `json:"rustdoc,omitempty"`
	ConfigDigest       string            `json:"config_digest"`
	NativeConfigDigest string            `json:"native_config_digest,omitempty"`
	NativeSourcePaths  []string          `json:"native_source_paths,omitempty"`
	CargoArgs          []string          `json:"cargo_args"`
	CargoCommand       string            `json:"cargo_command,omitempty"`
	Environment        map[string]string `json:"environment"`
	Packages           []Package         `json:"packages"`
	Actions            []Action          `json:"actions"`
	BaselineDigests    map[string]string `json:"baseline_digests"`
	CaptureSeconds     float64           `json:"capture_seconds"`
	Publications       []Publication     `json:"publications,omitempty"`
	Tests              []TestCommand     `json:"tests,omitempty"`
}

// TestCommand records Cargo's actual unit, integration or rustdoc invocation.
type TestCommand struct {
	Executable string            `json:"executable"`
	Args       []string          `json:"args"`
	Cwd        string            `json:"cwd"`
	Env        map[string]string `json:"env"`
}

// TestTransportEnvironment identifies capture and tracing state belonging to
// the recording session, rather than the test invocation being replayed.
func TestTransportEnvironment(name string) bool {
	return strings.HasPrefix(name, "DAGGER_") || strings.HasPrefix(name, "_DAGGER_") ||
		strings.HasPrefix(name, "OTEL_") || strings.HasPrefix(name, "RCE_") ||
		name == "TRACEPARENT" || name == "TRACESTATE" || name == "BAGGAGE" ||
		name == "RUSTC_WRAPPER" || name == "RUSTDOC" || name == "CARGO_MAKEFLAGS" || name == "MAKEFLAGS"
}

// Publication maps a compiler output to Cargo's final workspace artifact path.
type Publication struct {
	Output string `json:"output"`
	Path   string `json:"path"`
}

type Package struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Root string `json:"root"`
}

type Action struct {
	ID           string            `json:"id"`
	PackageRoot  string            `json:"package_root"`
	Crate        string            `json:"crate"`
	Compiler     string            `json:"compiler"`
	Args         []string          `json:"args"`
	Cwd          string            `json:"cwd"`
	Env          map[string]string `json:"env"`
	UnsetEnv     []string          `json:"unset_env,omitempty"`
	Inputs       []string          `json:"inputs"`
	Outputs      []string          `json:"outputs"`
	Dependencies []string          `json:"dependencies"`
	// Diagnostic only; replay identities use compiler arguments and inputs.
	CompilerSeconds float64 `json:"compiler_seconds,omitempty"`
	// Capture diagnostics for Cargo's metadata/code-generation overlap.
	CompilerStartedUnixNanos int64   `json:"compiler_started_unix_nanos,omitempty"`
	MetadataSeconds          float64 `json:"metadata_seconds,omitempty"`
}

// Option accepts both --name value and --name=value, as emitted by Cargo.
func Option(args []string, name string) string {
	for i, arg := range args {
		if arg == name && i+1 < len(args) {
			return args[i+1]
		}
		if value, ok := strings.CutPrefix(arg, name+"="); ok {
			return value
		}
	}
	return ""
}

func Absolute(cwd, filename string) string {
	if path.IsAbs(filename) {
		return path.Clean(filename)
	}
	return path.Join(cwd, filename)
}

func Within(root, filename string) bool {
	return filename == root || strings.HasPrefix(filename, root+"/")
}

// DepInfo parses rustc's Makefile dependencies and its env-dep extension.
// An absent value represents option_env! reading an unset variable.
func DepInfo(contents, cwd string) ([]string, map[string]*string, error) {
	env := map[string]*string{}
	var files []string
	scanner := bufio.NewScanner(strings.NewReader(strings.ReplaceAll(contents, "\\\n", "")))
	scanner.Buffer(make([]byte, 4096), 16*1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if value, ok := strings.CutPrefix(line, "# env-dep:"); ok {
			name, val, present := strings.Cut(value, "=")
			if !present {
				env[name] = nil
			} else {
				env[name] = &val
			}
			continue
		}
		if files != nil || strings.HasPrefix(line, "#") {
			continue
		}
		_, deps, ok := strings.Cut(line, ": ")
		if !ok {
			continue
		}
		var token strings.Builder
		escaped := false
		flush := func() {
			if token.Len() > 0 {
				files = append(files, Absolute(cwd, token.String()))
				token.Reset()
			}
		}
		for _, c := range deps {
			if escaped {
				token.WriteRune(c)
				escaped = false
				continue
			}
			switch c {
			case '\\':
				escaped = true
			case ' ', '\t':
				flush()
			default:
				token.WriteRune(c)
			}
		}
		if escaped {
			return nil, nil, fmt.Errorf("unterminated escape in dep-info")
		}
		flush()
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, err
	}
	if len(files) == 0 {
		return nil, nil, fmt.Errorf("dep-info has no source dependencies")
	}
	sort.Strings(files)
	return files, env, nil
}
