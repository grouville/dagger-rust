package replay

import (
	"context"
	"fmt"
	"os"
	"slices"

	dagger "dagger.io/dagger/core"
	"github.com/grouville/dagger-rust/compiler/model"
)

// CaptureNative records Cargo's compiler graph from the module's actual source.
// Source files stay in the engine; only configuration is exported for validation.
func CaptureNative(ctx context.Context, client *dagger.Query, source *dagger.Directory, opts CaptureOptions) (*model.Plan, error) {
	if source == nil || opts.SourceDirectory != nil {
		return nil, fmt.Errorf("supply one native source directory")
	}
	configuration, err := nativeConfiguration(ctx, source)
	if err != nil {
		return nil, err
	}
	opts.SourceDirectory = source
	plan, err := Capture(ctx, client, configuration, opts)
	if err != nil {
		return nil, err
	}
	plan.NativeConfigDigest, err = nativeConfigurationDirectory(source).Digest(ctx)
	if err != nil {
		return nil, fmt.Errorf("hash native Cargo configuration: %w", err)
	}
	plan.NativeSourcePaths, err = source.Glob(ctx, "**/*")
	if err != nil {
		return nil, fmt.Errorf("read native source paths: %w", err)
	}
	slices.Sort(plan.NativeSourcePaths)
	return plan, nil
}

// NativePlanMatches checks Cargo's inputs to graph discovery. Contents of
// ordinary source files are compilation inputs, not a reason to recapture.
// Conservatively recapture on any path addition/removal, including new targets
// or build scripts that Cargo would otherwise discover outside the old graph.
func NativePlanMatches(ctx context.Context, source *dagger.Directory, plan *model.Plan, opts CaptureOptions) (bool, error) {
	if plan == nil || plan.NativeConfigDigest == "" || plan.NativeSourcePaths == nil || !planOptionsMatch(plan, opts) {
		return false, nil
	}
	digest, err := nativeConfigurationDirectory(source).Digest(ctx)
	if err != nil {
		return false, fmt.Errorf("hash native Cargo configuration: %w", err)
	}
	if digest != plan.NativeConfigDigest {
		return false, nil
	}
	paths, err := source.Glob(ctx, "**/*")
	if err != nil {
		return false, fmt.Errorf("read native source paths: %w", err)
	}
	slices.Sort(paths)
	return slices.Equal(paths, plan.NativeSourcePaths), nil
}

func planOptionsMatch(plan *model.Plan, opts CaptureOptions) bool {
	command := plan.CargoCommand
	if command == "" {
		command = "build"
	}
	return plan.Version == model.Version && plan.Image == opts.Image && command == opts.Command &&
		slices.Equal(plan.CargoArgs, opts.CargoArgs) && len(plan.Environment) == 0
}

// BuildNativeAutomatic accepts a module's immutable source and cache directly.
// Cargo configuration is validated in the engine for plans captured natively;
// legacy plans still export configuration. Compiler inputs and package hashes
// come from source itself. The caller excludes .git and target from source and
// retains Graph.History as a Directory.
func BuildNativeAutomatic(ctx context.Context, client *dagger.Query, plan *model.Plan, source *dagger.Directory, opts BuildOptions, previous *dagger.Directory) (*Graph, error) {
	if source == nil {
		return nil, fmt.Errorf("native source directory is required")
	}
	if opts.SourceDirectory != nil {
		return nil, fmt.Errorf("native source directory must be supplied through source")
	}
	var configuration Source
	var err error
	if plan.NativeConfigDigest == "" {
		configuration, err = nativeConfiguration(ctx, source)
	} else {
		opts.nativeConfigDigest, err = nativeConfigurationDirectory(source).Digest(ctx)
		if err != nil {
			return nil, fmt.Errorf("hash native Cargo configuration: %w", err)
		}
	}
	if err != nil {
		return nil, err
	}
	opts.SourceDirectory = source
	return BuildAutomatic(ctx, client, plan, configuration, opts, previous)
}

func nativeConfiguration(ctx context.Context, source *dagger.Directory) (Source, error) {
	local, err := os.MkdirTemp("", "rcexp-configuration-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(local)
	configuration := nativeConfigurationDirectory(source)
	if _, err := configuration.Export(ctx, local); err != nil {
		return nil, fmt.Errorf("read native Cargo configuration: %w", err)
	}
	return ReadSource(local)
}

func nativeConfigurationDirectory(source *dagger.Directory) *dagger.Directory {
	return source.Filter(dagger.DirectoryFilterOpts{Include: []string{
		"**/Cargo.toml", "**/Cargo.lock", "**/rust-toolchain", "**/rust-toolchain.toml", "**/.cargo/**",
	}})
}
