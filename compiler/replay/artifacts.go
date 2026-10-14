package replay

import dagger "dagger.io/dagger/core"

// Merge compiler snapshots without projecting and copying every artifact file.
// A compiler's output mount already contains its transitive dependency artifacts.
// Balanced joins allow independent dependency branches to materialize together.
func mergeArtifactDirectories(directories []*dagger.Directory) *dagger.Directory {
	if len(directories) == 1 {
		return directories[0]
	}
	middle := len(directories) / 2
	return mergeArtifactDirectories(directories[:middle]).
		WithDirectory(".", mergeArtifactDirectories(directories[middle:]))
}
