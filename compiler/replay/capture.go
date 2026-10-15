package replay

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	dagger "dagger.io/dagger/core"
	"github.com/grouville/dagger-rust/compiler/model"
)

// Same pinned toolchain as .dagger/modules/rust-client-dev.
const DefaultImage = "rust:1.77-bookworm@sha256:83101f6985c93e1e6501b3375de188ee3d2cbb89968bcc91611591f9f447bd42"

type CaptureOptions struct {
	Image           string
	Wrapper         *dagger.File
	TestRecorder    *dagger.File
	CargoArgs       []string
	Command         string
	Environment     map[string]string
	SourceDirectory *dagger.Directory
}

func Capture(ctx context.Context, client *dagger.Query, source Source, opts CaptureOptions) (*model.Plan, error) {
	started := time.Now()
	if opts.Image == "" {
		opts.Image = DefaultImage
	}
	if opts.Wrapper == nil {
		return nil, fmt.Errorf("compiler wrapper is required")
	}
	if opts.Command == "" {
		opts.Command = "build"
	}
	if opts.Command != "build" && opts.Command != "check" && opts.Command != "test" {
		return nil, fmt.Errorf("unsupported Cargo command %s", opts.Command)
	}
	if opts.Command == "test" {
		if err := validateTestConfiguration(source); err != nil {
			return nil, err
		}
	}
	base := client.Container(dagger.ContainerOpts{Platform: "linux/amd64"}).From(opts.Image)
	image, err := base.ImageRef(ctx)
	if err != nil {
		return nil, err
	}
	rustcVersion, err := base.WithExec([]string{"rustc", "-Vv"}, noNesting()).Stdout(ctx)
	if err != nil {
		return nil, err
	}
	workspace := opts.SourceDirectory
	if workspace == nil {
		workspace = source.Directory(client)
	}
	ctr := base.WithMountedDirectory(model.SourceRoot, workspace).WithWorkdir(model.SourceRoot)
	keys := sortedKeys(opts.Environment)
	for _, key := range keys {
		if key == "RUSTC" || key == "RUSTC_WRAPPER" || key == "RUSTC_WORKSPACE_WRAPPER" || strings.HasPrefix(key, "CARGO_TARGET_") || key == "CARGO_TARGET_DIR" || key == "CARGO_BUILD_BUILD_DIR" || key == "RCE_CAPTURE_DIR" {
			return nil, fmt.Errorf("unsupported capture environment override %s", key)
		}
		ctr = ctr.WithEnvVariable(key, opts.Environment[key])
	}
	ctr = ctr.WithEnvVariable("CARGO_INCREMENTAL", "0").WithEnvVariable("CARGO_TARGET_DIR", model.TargetRoot).WithEnvVariable("CARGO_BUILD_BUILD_DIR", model.TargetRoot)
	version, err := ctr.WithExec([]string{"rustc", "-Vv"}, noNesting()).Stdout(ctx)
	if err != nil {
		return nil, err
	}
	if version != rustcVersion {
		return nil, fmt.Errorf("project selects a different Rust toolchain; supply a matching --image")
	}
	metadataJSON, err := ctr.WithExec([]string{"cargo", "metadata", "--offline", "--locked", "--format-version=1"}, noNesting()).Stdout(ctx)
	if err != nil {
		return nil, fmt.Errorf("capture supports path-only workspaces; cargo metadata: %w", err)
	}
	packages, err := parseMetadata(metadataJSON)
	if err != nil {
		return nil, err
	}
	// Output location and target configuration belong to the recorded experiment.
	for _, arg := range opts.CargoArgs {
		if strings.HasPrefix(arg, "--target") || strings.HasPrefix(arg, "--manifest-path") || strings.HasPrefix(arg, "--message-format") || strings.HasPrefix(arg, "--config") {
			return nil, fmt.Errorf("unsupported Cargo argument %s", arg)
		}
	}
	args := append([]string{"cargo", opts.Command}, CargoFlags(opts.CargoArgs)...)
	args = append(args, "--offline", "--message-format=json-render-diagnostics")
	if opts.Command == "test" {
		if opts.TestRecorder == nil {
			return nil, fmt.Errorf("test command recorder is required")
		}
		runner, err := ctr.EnvVariable(ctx, "CARGO_TARGET_X86_64_UNKNOWN_LINUX_GNU_RUNNER")
		if err != nil {
			return nil, err
		}
		if runner != "" {
			return nil, fmt.Errorf("native test capture does not yet support Cargo target runners")
		}
		if err := validateImageTestConfiguration(ctx, ctr); err != nil {
			return nil, err
		}
		ctr = ctr.WithMountedFile("/opt/test-recorder", opts.TestRecorder).
			WithMountedFile("/opt/rustdoc-recorder", opts.TestRecorder).
			WithEnvVariable("RUSTDOC", "/opt/rustdoc-recorder")
		args = append(args, "--config", `target.x86_64-unknown-linux-gnu.runner="/opt/test-recorder"`)
	}
	compiled := ctr.WithMountedFile("/opt/rustc-wrapper", opts.Wrapper).
		WithEnvVariable("RUSTC_WRAPPER", "/opt/rustc-wrapper").
		WithEnvVariable("RCE_CAPTURE_DIR", model.CaptureRoot).
		WithExec(args, noNesting())
	records := compiled.Directory(model.CaptureRoot)
	names, err := records.Entries(ctx)
	if err != nil {
		return nil, fmt.Errorf("Cargo capture failed: %w", err)
	}
	plan := &model.Plan{Version: model.Version, Image: image, RustcVersion: rustcVersion, ConfigDigest: source.ConfigDigest(), CargoArgs: opts.CargoArgs, CargoCommand: opts.Command, Environment: opts.Environment, Packages: packages, BaselineDigests: map[string]string{}}
	for _, name := range names {
		if !strings.HasSuffix(name, ".json") {
			continue
		}
		data, err := records.File(name).Contents(ctx)
		if err != nil {
			return nil, err
		}
		var a model.Action
		if err := json.Unmarshal([]byte(data), &a); err != nil {
			return nil, err
		}
		plan.Actions = append(plan.Actions, a)
	}
	sort.Slice(plan.Actions, func(i, j int) bool { return plan.Actions[i].ID < plan.Actions[j].ID })
	if err := Validate(plan, source); err != nil {
		return nil, err
	}
	if opts.Command == "build" || opts.Command == "test" {
		messages, err := compiled.Stdout(ctx)
		if err != nil {
			return nil, err
		}
		plan.Publications, err = buildPublications(metadataJSON, messages, plan.Actions, opts.Command == "test")
		if err != nil {
			return nil, err
		}
	}
	if opts.Command == "test" {
		commands, err := records.File("tests.jsonl").Contents(ctx)
		if err != nil {
			return nil, fmt.Errorf("read captured tests: %w", err)
		}
		for _, line := range strings.Split(strings.TrimSpace(commands), "\n") {
			var command model.TestCommand
			if err := json.Unmarshal([]byte(line), &command); err != nil {
				return nil, err
			}
			plan.Tests = append(plan.Tests, command)
		}
	}
	if err := Validate(plan, source); err != nil {
		return nil, err
	}
	compilers := map[string]bool{}
	for _, a := range plan.Actions {
		if compilers[a.Compiler] {
			continue
		}
		if _, err := base.File(a.Compiler).Digest(ctx); err != nil {
			return nil, fmt.Errorf("compiler %s is absent from the pinned image; supply an image containing the selected toolchain: %w", a.Compiler, err)
		}
		compilers[a.Compiler] = true
	}
	for _, a := range plan.Actions {
		for _, output := range a.Outputs {
			digest, err := compiled.File(output).Digest(ctx)
			if err != nil {
				return nil, err
			}
			plan.BaselineDigests[output] = digest
		}
	}
	plan.CaptureSeconds = time.Since(started).Seconds()
	return plan, nil
}

func noNesting() dagger.ContainerWithExecOpts {
	return dagger.ContainerWithExecOpts{DisableDaggerInDagger: true}
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
