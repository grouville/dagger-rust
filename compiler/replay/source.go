package replay

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	dagger "dagger.io/dagger/core"
	"github.com/grouville/dagger-rust/compiler/model"
	"github.com/dagger/querybuilder"
)

type SourceFile struct {
	Contents string
	Mode     int
}

// Source is a content snapshot, independent of host/session directory handles.
type Source map[string]SourceFile

const sourceLeafFiles = 64

func ReadSource(root string) (Source, error) {
	files := Source{}
	err := filepath.WalkDir(root, func(filename string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, filename)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if rel != "." && (entry.Name() == "target" || entry.Name() == ".git") {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported source file %s: only regular files are supported", rel)
		}
		contents, err := os.ReadFile(filename)
		if err != nil {
			return err
		}
		if !utf8.Valid(contents) {
			return fmt.Errorf("unsupported binary source resource %s: this experiment currently accepts UTF-8 files", rel)
		}
		files[filepath.ToSlash(rel)] = SourceFile{Contents: string(contents), Mode: int(info.Mode().Perm())}
		return nil
	})
	return files, err
}

func (s Source) ConfigDigest() string {
	hash := sha256.New()
	for _, name := range s.names() {
		base := path.Base(name)
		if base != "Cargo.toml" && base != "Cargo.lock" && base != "rust-toolchain" && base != "rust-toolchain.toml" && !strings.Contains("/"+name, "/.cargo/") {
			continue
		}
		file := s[name]
		fmt.Fprintf(hash, "%s\x00%d\x00%d\x00", name, file.Mode, len(file.Contents))
		hash.Write([]byte(file.Contents))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func (s Source) ContentDigest() string {
	hash := sha256.New()
	for _, name := range s.names() {
		file := s[name]
		fmt.Fprintf(hash, "%s\x00%d\x00%d\x00", name, file.Mode, len(file.Contents))
		hash.Write([]byte(file.Contents))
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func (s Source) names() []string {
	names := make([]string, 0, len(s))
	for name := range s {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (s Source) Directory(client *dagger.Query) *dagger.Directory {
	var build func([]string) *dagger.Directory
	build = func(names []string) *dagger.Directory {
		dir := client.Directory()
		// A linear recipe invalidates every later snapshot when an early
		// file's content changes. Bound that chain and merge independent
		// subtrees. Small sources retain their single-ID query path.
		if len(names) > sourceLeafFiles {
			middle := len(names) / 2
			return dir.WithDirectory(".", build(names[:middle])).WithDirectory(".", build(names[middle:]))
		}
		for _, name := range names {
			file := s[name]
			dir = dir.WithNewFile(name, file.Contents, dagger.DirectoryWithNewFileOpts{Permissions: file.Mode})
		}
		return dir
	}
	return build(s.names())
}

// packageSources assigns each file once, rather than scanning the whole workspace
// and every package root again for each compiler action. Nested packages own
// their files; a parent package must not depend on a nested package's source.
func (s Source) packageSources(packages []model.Package) map[string]Source {
	roots := newPackageRoots(packages)
	result := make(map[string]Source, len(packages))
	for name, file := range s {
		absolute := path.Join(model.SourceRoot, name)
		root := roots.owner(absolute)
		if root == "" {
			continue
		}
		if result[root] == nil {
			result[root] = Source{}
		}
		result[root][strings.TrimPrefix(absolute, root+"/")] = file
	}
	return result
}

// PackageDirectories projects a native workspace snapshot with the same file
// ownership as packageSources. Remove only immediate nested packages: each
// removal already excludes its descendants, avoiding a scan per source file.
// The caller must exclude target and .git directories from the input snapshot.
func PackageDirectories(source *dagger.Directory, packages []model.Package) map[string]*dagger.Directory {
	projections := packageProjections(packages)
	directories := make(map[string]*dagger.Directory, len(projections))
	for root, projection := range projections {
		directory := source.Directory(projection.path)
		for _, child := range projection.children {
			directory = directory.WithoutDirectory(child)
		}
		directories[root] = directory
	}
	return directories
}

type packageProjection struct {
	path     string
	children []string
}

func packageProjections(packages []model.Package) map[string]packageProjection {
	roots := newPackageRoots(packages)
	children := map[string][]string{}
	for root := range roots {
		if parent := roots.owner(path.Dir(root)); parent != "" {
			children[parent] = append(children[parent], strings.TrimPrefix(root, parent+"/"))
		}
	}
	projections := make(map[string]packageProjection, len(roots))
	for root := range roots {
		relative := strings.TrimPrefix(root, model.SourceRoot+"/")
		if root == model.SourceRoot {
			relative = "."
		}
		sort.Strings(children[root])
		projections[root] = packageProjection{path: relative, children: children[root]}
	}
	return projections
}

// PackageDigests hashes the same native projections as PackageDirectories in
// one request. Aliases change the response shape, not the directory cache keys.
func PackageDigests(ctx context.Context, client *dagger.Query, sourceID dagger.ID, packages []model.Package) (map[string]string, error) {
	return packageDigests(ctx, client, sourceID, packages, false)
}

func packageDigests(ctx context.Context, client *dagger.Query, sourceID dagger.ID, packages []model.Package, directPaths bool) (map[string]string, error) {
	projections := packageProjections(packages)
	roots := sortedKeys(projections)
	keys := make(map[string]string, len(roots))
	if len(roots) == 0 {
		return keys, nil
	}
	fields := make([]string, len(roots))
	for i, root := range roots {
		projection := projections[root]
		query := querybuilder.Query().SelectWithAlias(fmt.Sprintf("p%d", i), "directory").Arg("path", projection.path)
		if directPaths && len(projection.children) == 0 {
			query = querybuilder.Query().SelectWithAlias(fmt.Sprintf("p%d", i), "digest").Arg("path", projection.path)
		} else {
			for _, child := range projection.children {
				query = query.Select("withoutDirectory").Arg("path", child)
			}
			query = query.Select("digest")
		}
		field, err := query.Build(ctx)
		if err != nil {
			return nil, err
		}
		fields[i] = strings.TrimSuffix(strings.TrimPrefix(field, "{"), "}")
	}
	type digestResult struct {
		Digest           string
		WithoutDirectory *digestResult
	}
	var response map[string]json.RawMessage
	err := client.QueryBuilder().Select("node").Arg("id", sourceID).InlineFragment("Directory").
		SelectMultiple(fields...).Bind(&response).Execute(ctx)
	if err != nil {
		return nil, err
	}
	for i, root := range roots {
		value := response[fmt.Sprintf("p%d", i)]
		if directPaths && len(projections[root].children) == 0 {
			var digest string
			if err := json.Unmarshal(value, &digest); err != nil {
				return nil, err
			}
			if digest == "" {
				return nil, fmt.Errorf("missing native source digest for %s", root)
			}
			keys[root] = digest
			continue
		}
		var result *digestResult
		if err := json.Unmarshal(value, &result); err != nil {
			return nil, err
		}
		for range projections[root].children {
			if result == nil {
				break
			}
			result = result.WithoutDirectory
		}
		if result == nil || result.Digest == "" {
			return nil, fmt.Errorf("missing native source digest for %s", root)
		}
		keys[root] = result.Digest
	}
	return keys, nil
}

type packageRoots map[string]struct{}

func newPackageRoots(packages []model.Package) packageRoots {
	roots := make(packageRoots, len(packages))
	for _, pkg := range packages {
		roots[pkg.Root] = struct{}{}
	}
	return roots
}

func (roots packageRoots) owner(filename string) string {
	// Walk toward /src so the most deeply nested package wins. This costs
	// directory depth, independently of the number of packages in the workspace.
	for candidate := path.Clean(filename); model.Within(model.SourceRoot, candidate); candidate = path.Dir(candidate) {
		if _, ok := roots[candidate]; ok {
			return candidate
		}
	}
	return ""
}
