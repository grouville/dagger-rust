package replay

import (
	"strings"
	"testing"
)

func TestHistoryRejectsUnsafeOrAmbiguousEntries(t *testing.T) {
	key := strings.Repeat("a", 64)
	valid := historyEntry{Key: key, Compatibility: strings.Repeat("b", 64)}
	for _, candidate := range []historyManifest{
		{Version: 2, Actions: map[string][]historyEntry{"a": {valid}}},
		{Version: 1},
		{Version: 1, Actions: map[string][]historyEntry{"": {valid}}},
		{Version: 1, Actions: map[string][]historyEntry{"a": {}}},
		{Version: 1, Actions: map[string][]historyEntry{"a": {{Key: "../outside", Compatibility: valid.Compatibility}}}},
		{Version: 1, Actions: map[string][]historyEntry{"a": {{Key: strings.ToUpper(key), Compatibility: valid.Compatibility}}}},
		{Version: 1, Actions: map[string][]historyEntry{"a": {valid, valid}}},
		{Version: 1, Actions: map[string][]historyEntry{"a": {valid}, "b": {valid}}},
	} {
		if err := candidate.validate(); err == nil {
			t.Errorf("accepted invalid history: %#v", candidate)
		}
	}
	if err := (historyManifest{Version: 1, Actions: map[string][]historyEntry{"a": {valid}}}).validate(); err != nil {
		t.Fatal(err)
	}
}

func TestSourceContentDigestTracksAllPackageInputs(t *testing.T) {
	original := Source{"src/main.rs": {Contents: "fn main() {}", Mode: 0644}, "resource.txt": {Contents: "data", Mode: 0644}}
	key := original.ContentDigest()
	for name, change := range map[string]func(Source){
		"contents": func(s Source) { s["resource.txt"] = SourceFile{Contents: "changed", Mode: 0644} },
		"mode":     func(s Source) { s["resource.txt"] = SourceFile{Contents: "data", Mode: 0600} },
		"added":    func(s Source) { s["extra.txt"] = SourceFile{Contents: "data", Mode: 0644} },
		"deleted":  func(s Source) { delete(s, "resource.txt") },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := Source{}
			for path, file := range original {
				candidate[path] = file
			}
			change(candidate)
			if candidate.ContentDigest() == key {
				t.Fatal("package edit preserved its history key")
			}
		})
	}
}

func TestHistoryBoundsPhysicalSnapshotAncestry(t *testing.T) {
	batch := strings.Repeat("c", 64)
	entry := historyEntry{Key: strings.Repeat("a", 64), Compatibility: strings.Repeat("b", 64), Batch: batch}
	for _, depth := range []int{0, 1, maxBatchCopyOnWriteDepth, maxBatchCopyOnWriteDepth + 1} {
		manifest := historyManifest{Version: historyVersion, Actions: map[string][]historyEntry{"a": {entry}},
			CurrentBatch: batch, BatchDepths: map[string]int{batch: depth}}
		err := manifest.validate()
		if (err == nil) != (depth > 0 && depth <= maxBatchCopyOnWriteDepth) {
			t.Errorf("depth %d: %v", depth, err)
		}
	}
	manifest := historyManifest{Version: historyVersion, Actions: map[string][]historyEntry{"a": {entry}},
		BatchDepths: map[string]int{strings.Repeat("d", 64): 1}}
	if err := manifest.validate(); err == nil {
		t.Fatal("accepted depth for an unretained batch")
	}
}
