package replay

import (
	"strings"
	"testing"

	dagger "dagger.io/dagger/core"
)

func TestNativeIncrementalSnapshotReferences(t *testing.T) {
	batch, state := strings.Repeat("a", 64), strings.Repeat("b", 64)
	entry := historyEntry{Key: strings.Repeat("c", 64), Compatibility: strings.Repeat("d", 64), Batch: batch}
	manifest := historyManifest{Version: incrementalMountHistoryVersion,
		Actions: map[string][]historyEntry{"crate": {entry}}, CurrentBatch: batch,
		IncrementalBatches: map[string]string{batch: state}}
	if err := manifest.validate(); err != nil {
		t.Fatal(err)
	}
	artifacts, incremental := new(dagger.Directory), new(dagger.Directory)
	cache := NativeCache{Manifest: "metadata", Batches: []NativeCacheBatch{
		{ID: batch, Snapshot: artifacts}, {ID: state, Snapshot: incremental},
	}}
	if err := cache.validate(manifest); err != nil {
		t.Fatal(err)
	}
	if cache.directory("batches/"+batch) != artifacts ||
		cache.directory("batches/"+batch+"/incremental") != incremental {
		t.Fatal("native paths resolve to the wrong snapshot")
	}
	cache.Batches = cache.Batches[:1]
	if err := cache.validate(manifest); err == nil {
		t.Fatal("accepted a cache without its incremental snapshot")
	}
	manifest.Version = historyVersion
	if err := manifest.validate(); err == nil {
		t.Fatal("accepted split snapshots as legacy history")
	}
	manifest.Version = incrementalMountHistoryVersion
	manifest.IncrementalBatches[strings.Repeat("e", 64)] = state
	if err := manifest.validate(); err == nil {
		t.Fatal("accepted incremental state for an unreferenced batch")
	}
	delete(manifest.IncrementalBatches, strings.Repeat("e", 64))
	manifest.IncrementalBatches[batch] = batch
	if err := manifest.validate(); err == nil {
		t.Fatal("accepted aliasing the artifact snapshot as incremental state")
	}
}
