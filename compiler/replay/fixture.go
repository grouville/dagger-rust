package replay

import (
	"embed"
	"io/fs"
	"strings"
)

//go:embed testdata/workspace
var fixtureFS embed.FS

// Fixture supplies the small, path-only workspace used to measure selective reuse.
func Fixture() (Source, error) {
	source := Source{}
	err := fs.WalkDir(fixtureFS, "testdata/workspace", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		contents, err := fixtureFS.ReadFile(name)
		if err != nil {
			return err
		}
		source[strings.TrimPrefix(name, "testdata/workspace/")] = SourceFile{Contents: string(contents), Mode: 0644}
		return nil
	})
	return source, err
}
