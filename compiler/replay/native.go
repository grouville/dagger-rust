package replay

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"

	dagger "dagger.io/dagger/core"
	"github.com/grouville/dagger-rust/compiler/model"
)

// ConfigurationGuard accelerates validation of a canonical configuration digest.
// It is cache metadata, never part of the compiler recipe or the worker binary.
type ConfigurationGuard struct {
	Version   int    `json:"version"`
	Canonical string `json:"canonical"`
	Entries   string `json:"entries"`
}

func (g *ConfigurationGuard) matches(canonical, entries string) bool {
	return g != nil && g.Version == 1 && g.Canonical == canonical && g.Entries == entries
}

func ConfigurationGuardFromManifest(manifest string) (*ConfigurationGuard, error) {
	if manifest == "" {
		return nil, nil
	}
	var metadata struct {
		ConfigGuard *ConfigurationGuard `json:"native_configuration,omitempty"`
	}
	if err := json.Unmarshal([]byte(manifest), &metadata); err != nil {
		return nil, fmt.Errorf("read native configuration guard: %w", err)
	}
	return metadata.ConfigGuard, nil
}

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
// A match returns a refreshed cache guard; the plan itself is unchanged.
func NativePlanMatches(ctx context.Context, client *dagger.Query, source *dagger.Directory, plan *model.Plan, opts CaptureOptions, guard *ConfigurationGuard) (bool, *ConfigurationGuard, error) {
	if plan == nil || plan.NativeConfigDigest == "" || plan.NativeSourcePaths == nil || !planOptionsMatch(plan, opts) {
		return false, nil, nil
	}
	if opts.Command == "build" && len(plan.Publications) == 0 {
		return false, nil, nil
	}
	digest, err := nativeConfigurationDigest(ctx, client, source)
	if err != nil {
		return false, nil, fmt.Errorf("hash native Cargo configuration: %w", err)
	}
	if !guard.matches(plan.NativeConfigDigest, digest) {
		// A derived snapshot can rescan unchanged imported records in another
		// hash format. Verify the canonical projection before recapturing Cargo.
		canonical, err := nativeConfigurationDirectory(source).Digest(ctx)
		if err != nil {
			return false, nil, fmt.Errorf("verify native Cargo configuration: %w", err)
		}
		if canonical != plan.NativeConfigDigest {
			return false, nil, nil
		}
	}
	paths, err := source.Glob(ctx, "**/*")
	if err != nil {
		return false, nil, fmt.Errorf("read native source paths: %w", err)
	}
	slices.Sort(paths)
	if !slices.Equal(paths, plan.NativeSourcePaths) {
		return false, nil, nil
	}
	return true, &ConfigurationGuard{Version: 1, Canonical: plan.NativeConfigDigest, Entries: digest}, nil
}

func planOptionsMatch(plan *model.Plan, opts CaptureOptions) bool {
	command := plan.CargoCommand
	if command == "" {
		command = "build"
	}
	if command == "test" && plan.Rustdoc == "" {
		return false // Older captures did not retain the selected rustdoc program.
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
		entries, entryErr := nativeConfigurationDigest(ctx, client, source)
		if entryErr != nil {
			return nil, fmt.Errorf("hash native Cargo entries: %w", entryErr)
		}
		if opts.ConfigGuard.matches(plan.NativeConfigDigest, entries) {
			opts.nativeConfigDigest = plan.NativeConfigDigest
		} else {
			opts.nativeConfigDigest, err = nativeConfigurationDirectory(source).Digest(ctx)
		}
		if err != nil {
			return nil, fmt.Errorf("hash native Cargo configuration: %w", err)
		}
		opts.ConfigGuard = &ConfigurationGuard{Version: 1, Canonical: opts.nativeConfigDigest, Entries: entries}
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

var nativeConfigurationPatterns = []string{
	"**/Cargo.toml", "**/Cargo.lock", "**/rust-toolchain", "**/rust-toolchain.toml", "**/.cargo/**",
}

func nativeConfigurationDigest(ctx context.Context, client *dagger.Query, source *dagger.Directory) (string, error) {
	id, err := source.ID(ctx)
	if err != nil {
		return "", err
	}
	var checksum string
	err = client.QueryBuilder().Select("node").Arg("id", id).InlineFragment("Directory").
		Select("digestEntries").Arg("include", nativeConfigurationPatterns).Bind(&checksum).Execute(ctx)
	if err != nil {
		return "", err
	}
	if checksum == "" {
		return "", fmt.Errorf("missing native Cargo configuration checksum")
	}
	return checksum, nil
}

func nativeConfigurationDirectory(source *dagger.Directory) *dagger.Directory {
	return source.Filter(dagger.DirectoryFilterOpts{Include: nativeConfigurationPatterns})
}
