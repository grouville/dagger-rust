package replay

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	dagger "dagger.io/dagger/core"
	"github.com/grouville/dagger-rust/compiler/model"
)

// NativeCache keeps metadata and immutable compiler snapshots as separate
// objects. A module can retain these Directory fields without copying their
// files into a history archive. Only caches produced by this builder are valid
// inputs; a serialized list of engine-local IDs is not a portable cache.
type NativeCache struct {
	Manifest     string
	Batches      []NativeCacheBatch
	snapshots    map[string]*dagger.Directory
	incrementals map[string]*dagger.Directory
}

type NativeCacheBatch struct {
	ID       string
	Snapshot *dagger.Directory
}

// BuildNativeCached builds a module's source with a cache of native objects.
// The returned Cache retains both exact results and compatible incremental
// states. Compilation seeds remain ordinary inputs to the execution cache key.
func BuildNativeCached(ctx context.Context, client *dagger.Query, plan *model.Plan, source *dagger.Directory, opts BuildOptions, previous *NativeCache) (*Graph, error) {
	if opts.BatchCompiler == nil {
		return nil, fmt.Errorf("native object cache requires the batch compiler")
	}
	input := NativeCache{}
	if previous != nil {
		input = *previous
	}
	opts.nativeCache = &input
	return BuildNativeAutomatic(ctx, client, plan, source, opts, nil)
}

func (cache *NativeCache) validate(manifest historyManifest) error {
	if cache.Manifest == "" && len(cache.Batches) != 0 {
		return fmt.Errorf("native cache batches require a manifest")
	}
	cache.snapshots = make(map[string]*dagger.Directory, len(cache.Batches))
	for _, batch := range cache.Batches {
		decoded, err := hex.DecodeString(batch.ID)
		if err != nil || len(decoded) != sha256.Size || hex.EncodeToString(decoded) != batch.ID || batch.Snapshot == nil || cache.snapshots[batch.ID] != nil {
			return fmt.Errorf("invalid or duplicate native cache batch %q", batch.ID)
		}
		cache.snapshots[batch.ID] = batch.Snapshot
	}
	if manifest.CurrentBatch != "" && cache.snapshots[manifest.CurrentBatch] == nil {
		return fmt.Errorf("missing current native cache batch")
	}
	cache.incrementals = make(map[string]*dagger.Directory, len(manifest.IncrementalBatches))
	for batch, state := range manifest.IncrementalBatches {
		if cache.snapshots[batch] == nil || cache.snapshots[state] == nil {
			return fmt.Errorf("missing native incremental cache snapshot")
		}
		cache.incrementals[batch] = cache.snapshots[state]
	}
	for _, entries := range manifest.Actions {
		for _, entry := range entries {
			if entry.Batch == "" || cache.snapshots[entry.Batch] == nil {
				return fmt.Errorf("missing native cache batch for result %s", entry.Key)
			}
		}
	}
	return nil
}

func (cache *NativeCache) directory(name string) *dagger.Directory {
	name = strings.TrimPrefix(name, "batches/")
	id, subdir, _ := strings.Cut(name, "/")
	if state := cache.incrementals[id]; state != nil {
		if subdir == "incremental" {
			return state
		}
		if path, ok := strings.CutPrefix(subdir, "incremental/"); ok {
			return state.Directory(path)
		}
	}
	if subdir == "" {
		return cache.snapshots[id]
	}
	return cache.snapshots[id].Directory(subdir)
}
