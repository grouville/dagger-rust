//go:build integration

package replay

import (
	"slices"
	"testing"

	daggerclient "dagger.io/dagger"
	dagger "dagger.io/dagger/core"
	"github.com/grouville/dagger-rust/compiler/model"
)

func TestNativeConfigurationPlanMatches(t *testing.T) {
	ctx := t.Context()
	client, err := daggerclient.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	query := dagger.NewQuery(client)
	constructed := query.Directory().WithNewFile("Cargo.toml", "root").
		WithNewFile("Cargo.lock", "lock").WithNewFile("rust-toolchain.toml", "toolchain").
		WithNewFile(".cargo/config.toml", "configuration").WithNewFile("src/lib.rs", "source").
		WithNewFile("member/Cargo.toml", "member").WithNewFile("member/src/lib.rs", "member source").
		WithNewFile("odd [name]/Cargo.toml", "brackets").WithNewFile("!bang/Cargo.toml", "bang")
	root := t.TempDir()
	if _, err := constructed.Export(ctx, root); err != nil {
		t.Fatal(err)
	}
	for origin, source := range map[string]*dagger.Directory{"constructed": constructed, "imported": query.Host().Directory(root)} {
		t.Run(origin, func(t *testing.T) {
			opts := CaptureOptions{Image: "rust@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Command: "check"}
			plan := &model.Plan{Version: model.Version, Image: opts.Image, CargoCommand: opts.Command}
			entries, err := nativeConfigurationDigest(ctx, query, source)
			if err != nil {
				t.Fatal(err)
			}
			plan.NativeSourcePaths, err = source.Glob(ctx, "**/*")
			if err != nil {
				t.Fatal(err)
			}
			slices.Sort(plan.NativeSourcePaths)
			legacy, err := nativeConfigurationDirectory(source).Digest(ctx)
			if err != nil {
				t.Fatal(err)
			}
			plan.NativeConfigDigest = legacy
			guard := &ConfigurationGuard{Version: 1, Canonical: legacy, Entries: entries}
			for _, tc := range []struct {
				name   string
				source *dagger.Directory
				want   bool
			}{
				{"same", source, true},
				{"source contents", source.WithNewFile("src/lib.rs", "edited"), true},
				{"source mode", source.WithFile("src/lib.rs", source.File("src/lib.rs"), dagger.DirectoryWithFileOpts{Permissions: 0640}), true},
				{"source directory mode", source.WithDirectory("src", source.Directory("src"), dagger.DirectoryWithDirectoryOpts{Permissions: 0750}), true},
				{"manifest contents", source.WithNewFile("Cargo.toml", "changed"), false},
				{"manifest mode", source.WithFile("member/Cargo.toml", source.File("member/Cargo.toml"), dagger.DirectoryWithFileOpts{Permissions: 0640}), false},
				{"manifest directory mode", source.WithDirectory("member", source.Directory("member"), dagger.DirectoryWithDirectoryOpts{Permissions: 0750}), false},
				{"lockfile", source.WithNewFile("Cargo.lock", "changed"), false},
				{"toolchain", source.WithNewFile("rust-toolchain.toml", "changed"), false},
				{"cargo config", source.WithNewFile(".cargo/config.toml", "changed"), false},
				{"new config", source.WithNewFile(".cargo/credentials.toml", "new"), false},
				{"new source path", source.WithNewFile("src/new.rs", "new"), false},
				{"removed manifest", source.WithoutFile("member/Cargo.toml"), false},
			} {
				t.Run(tc.name, func(t *testing.T) {
					var modePath string
					var mode int
					switch tc.name {
					case "source mode":
						modePath, mode = "src/lib.rs", 0640
					case "source directory mode":
						modePath, mode = "src", 0750
					case "manifest mode":
						modePath, mode = "member/Cargo.toml", 0640
					case "manifest directory mode":
						modePath, mode = "member", 0750
					}
					if modePath != "" {
						stat, err := tc.source.Stat(ctx, modePath)
						if err != nil {
							t.Fatal(err)
						}
						got, err := stat.Permissions(ctx)
						if err != nil || got != mode {
							t.Fatalf("mode of %s = %#o, error %v; want %#o", modePath, got, err, mode)
						}
					}
					paths, err := tc.source.Glob(ctx, "**/*")
					if err != nil {
						t.Fatal(err)
					}
					slices.Sort(paths)
					oldDigest, err := nativeConfigurationDirectory(tc.source).Digest(ctx)
					if err != nil {
						t.Fatal(err)
					}
					want := oldDigest == legacy && slices.Equal(paths, plan.NativeSourcePaths)
					if want != tc.want {
						t.Fatalf("legacy reference = %t, want %t", want, tc.want)
					}
					candidate := *plan
					got, refreshed, err := NativePlanMatches(ctx, query, tc.source, &candidate, opts, guard)
					if err != nil || got != want || (got && (refreshed == nil || refreshed.Entries == "" || refreshed.Canonical != legacy)) {
						t.Fatalf("native plan matches = %t, error %v; want %t", got, err, want)
					}
				})
			}
			missing, err := ConfigurationGuardFromManifest(`{"version":1,"actions":{}}`)
			if err != nil || missing != nil {
				t.Fatalf("legacy manifest: guard %v, error %v", missing, err)
			}
			matches, refreshed, err := NativePlanMatches(ctx, query, source, plan, opts, missing)
			if err != nil || !matches || refreshed == nil || refreshed.Entries != entries || plan.NativeConfigDigest != legacy {
				t.Fatalf("legacy guard migration = %t, guard %v, error %v", matches, refreshed, err)
			}

		})
	}
}
