package replay

import (
	"context"
	"fmt"
	"os"

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
	return plan, nil
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
