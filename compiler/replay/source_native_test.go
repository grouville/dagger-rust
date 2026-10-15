//go:build integration

package replay

import (
	"context"
	"testing"

	daggerclient "dagger.io/dagger"
	dagger "dagger.io/dagger/core"
	"github.com/grouville/dagger-rust/compiler/model"
)

func TestNativePackageProjectionOwnership(t *testing.T) {
	ctx := t.Context()
	client, err := daggerclient.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Error(err)
		}
	})
	query := dagger.NewQuery(client)
	source := query.Directory().
		WithNewFile("Cargo.toml", "root", dagger.DirectoryWithNewFileOpts{Permissions: 0640}).
		WithNewFile("src/lib.rs", "root source").
		WithNewFile("app/src/lib.rs", "app source").
		WithNewFile("app/nested/src/lib.rs", "nested source").
		WithNewDirectory("group", dagger.DirectoryWithNewDirectoryOpts{Permissions: 0750}).
		WithNewFile("group/left/src/lib.rs", "left", dagger.DirectoryWithNewFileOpts{Permissions: 0600}).
		WithNewFile("group/right/src/lib.rs", "right").
		WithNewFile("odd [name]/src/lib.rs", "brackets").
		WithNewFile("!bang/src/lib.rs", "exclamation").
		WithNewFile("app-shadow/keep.rs", "owned by root").
		WithSymlink("group/left", "shortcut")
	roots := map[string][]string{
		"/src":             {"app", "group/left", "group/right", "odd [name]", "!bang"},
		"/src/app":         {"nested"},
		"/src/app/nested":  nil,
		"/src/group/left":  nil,
		"/src/group/right": nil,
		"/src/odd [name]":  nil,
		"/src/!bang":       nil,
	}
	var packages []model.Package
	for root := range roots {
		packages = append(packages, model.Package{Root: root})
	}

	verify := func(t *testing.T, source *dagger.Directory) map[string]string {
		t.Helper()
		id, err := source.ID(ctx)
		if err != nil {
			t.Fatal(err)
		}
		digests, err := PackageDigests(ctx, query, id, packages)
		if err != nil {
			t.Fatal(err)
		}
		direct, err := packageDigests(ctx, query, id, packages, true)
		if err != nil {
			t.Fatal(err)
		}
		directories := PackageDirectories(source, packages)
		for root, children := range roots {
			relative := root[len("/src"):]
			if relative == "" {
				relative = "."
			}
			// Independent reference: the previous sequential path removals.
			expected := source.Directory(relative)
			for _, child := range children {
				expected = expected.WithoutDirectory(child)
			}
			want := nativeProjectionDigest(t, ctx, expected)
			if got := nativeProjectionDigest(t, ctx, directories[root]); got != want {
				t.Errorf("%s directory digest = %s, want %s", root, got, want)
			}
			if got := digests[root]; got != want {
				t.Errorf("%s package digest = %s, want %s", root, got, want)
			}
			if got := direct[root]; got != want {
				t.Errorf("%s direct digest = %s, want %s", root, got, want)
			}
		}
		return digests
	}

	baseline := verify(t, source)
	t.Run("child edit preserves its ancestors", func(t *testing.T) {
		got := verify(t, source.WithNewFile("app/nested/src/lib.rs", "edited nested source"))
		for root, before := range baseline {
			changed := got[root] != before
			if want := root == "/src/app/nested"; changed != want {
				t.Errorf("%s changed=%t, want %t", root, changed, want)
			}
		}
	})
	t.Run("owned file mode affects only its package", func(t *testing.T) {
		// WithNewFile preserves the mode of an existing file. Replace it to
		// exercise a real mode change rather than rewriting the same inode.
		edited := source.WithoutFile("Cargo.toml").
			WithNewFile("Cargo.toml", "root", dagger.DirectoryWithNewFileOpts{Permissions: 0600})
		stat, err := edited.File("Cargo.toml").Stat(ctx)
		if err != nil {
			t.Fatal(err)
		}
		mode, err := stat.Permissions(ctx)
		if err != nil || mode != 0600 {
			t.Fatalf("edited mode = %#o, error = %v", mode, err)
		}
		got := verify(t, edited)
		for root, before := range baseline {
			changed := got[root] != before
			if want := root == "/src"; changed != want {
				t.Errorf("%s changed=%t, want %t", root, changed, want)
			}
		}
	})
}

func nativeProjectionDigest(t *testing.T, ctx context.Context, directory *dagger.Directory) string {
	t.Helper()
	digest, err := directory.Digest(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}
