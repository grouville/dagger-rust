package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	daggerclient "dagger.io/dagger"
	core "dagger.io/dagger/core"
	"github.com/dagger/querybuilder"
)

const image = "rust:1.77-bookworm@sha256:83101f6985c93e1e6501b3375de188ee3d2cbb89968bcc91611591f9f447bd42"

type compilation struct {
	ID        core.ID
	Hits      int
	Actions   int
	Artifacts struct{ ID core.ID }
	Cache     struct {
		ID        core.ID
		Manifest  string
		Tokens    []string
		Snapshots []struct{ ID core.ID }
	}
}

type transfer struct {
	Plan        string
	Manifest    string
	Tokens      []string
	Edit        string
	SnapshotIDs []core.ID `json:",omitempty"`
	CacheID     core.ID   `json:",omitempty"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	sourcePath := flag.String("source", "", "Cargo workspace used by this integration fixture")
	cacheInConstructor := flag.Bool("cache-in-constructor", false, "use the earlier compilerCache constructor input for comparison")
	restoreCacheFields := flag.Bool("restore-cache-fields", false, "reconstruct the cache fields on each command for comparison")
	expectedActions := flag.Int("expected-actions", -1, "expected compiler action count for the check stage; negative disables the assertion")
	expectedHits := flag.Int("expected-hits", -1, "expected reused compiler actions for the check stage; negative disables the assertion")
	flag.Parse()
	if flag.NArg() != 2 || *sourcePath == "" {
		return fmt.Errorf("usage: integration -source WORKSPACE writer|reader|check|digests STATE_DIRECTORY")
	}
	stage, dir := flag.Arg(0), flag.Arg(1)
	ctx := context.Background()
	client, err := daggerclient.Connect(ctx)
	if err != nil {
		return err
	}
	defer client.Close()
	query := core.NewQuery(client)
	base := querybuilder.Query().Client(client.GraphQLClient())
	var served bool
	if err := base.Select("serveModule").Arg("address", ".").Bind(&served).Execute(ctx); err != nil {
		return err
	}
	source := query.Host().Directory(*sourcePath, core.HostDirectoryOpts{Exclude: []string{".git", "target", "dagger.toml", "dagger.lock"}})
	sourceID, err := source.ID(ctx)
	if err != nil {
		return err
	}
	if stage == "digests" {
		text, err := source.File("app/src/part_0000.rs").Contents(ctx)
		if err != nil {
			return err
		}
		edit := source.WithNewFile("app/src/part_0000.rs", text+"\n// digest diagnostic\n")
		for _, pkg := range []string{"base", "left", "right", "app"} {
			before, err := source.Directory(pkg).Digest(ctx)
			if err != nil {
				return err
			}
			after, err := edit.Directory(pkg).Digest(ctx)
			if err != nil {
				return err
			}
			fmt.Printf("%s before=%s after=%s unchanged=%t\n", pkg, before, after, before == after)
		}
		return nil
	}
	rust := base.Select("rust").Arg("image", image).Arg("locked", true)
	build := func(source *core.Directory, plan, previous core.ID) (*compilation, error) {
		id, err := source.ID(ctx)
		if err != nil {
			return nil, err
		}
		root := rust.Arg("compilerPlan", plan)
		if previous != "" && *cacheInConstructor {
			root = root.Arg("compilerCache", previous)
		}
		check := root.Select("nativeCheck").Arg("source", id)
		if previous != "" && !*cacheInConstructor {
			check = check.Arg("previous", previous)
		}
		var result compilation
		err = check.
			SelectMultiple("id", "hits", "actions", "artifacts { id }", "cache { id manifest tokens snapshots { id } }").Bind(&result).Execute(ctx)
		return &result, err
	}
	if stage == "check" {
		// Each invocation imports a fresh host snapshot. Retain only metadata
		// and native Directory IDs on this engine; never export compiler state
		// through the host while measuring edited-command latency.
		stored := transfer{}
		data, err := os.ReadFile(filepath.Join(dir, "metadata.json"))
		if err == nil {
			if err := json.Unmarshal(data, &stored); err != nil {
				return err
			}
		} else if !os.IsNotExist(err) {
			return err
		}
		var planID, previous core.ID
		if stored.Plan == "" {
			if err := rust.Select("captureCheckPlan").Arg("source", sourceID).Select("id").Bind(&planID).Execute(ctx); err != nil {
				return err
			}
			stored.Plan, err = core.Ref[*core.File](query, planID).Contents(ctx)
			if err != nil {
				return err
			}
		} else {
			planID, err = query.Directory().WithNewFile("plan.json", stored.Plan).File("plan.json").ID(ctx)
			if err != nil {
				return err
			}
			if stored.CacheID != "" && !*restoreCacheFields {
				previous = stored.CacheID
			} else {
				if len(stored.Tokens) != len(stored.SnapshotIDs) {
					return fmt.Errorf("same-engine cache tokens and directory IDs must match")
				}
				var snapshots []*core.Directory
				for _, id := range stored.SnapshotIDs {
					snapshots = append(snapshots, core.Ref[*core.Directory](query, id))
				}
				if err := rust.Select("restoreCompilerCache").Arg("manifest", stored.Manifest).Arg("tokens", stored.Tokens).Arg("snapshots", snapshots).Select("id").Bind(&previous).Execute(ctx); err != nil {
					return err
				}
			}
		}
		result, err := build(source, planID, previous)
		if err != nil {
			return err
		}
		if *expectedActions >= 0 && result.Actions != *expectedActions {
			return fmt.Errorf("expected %d compiler actions, got %d", *expectedActions, result.Actions)
		}
		if *expectedHits >= 0 && result.Hits != *expectedHits {
			return fmt.Errorf("expected %d reused compiler actions, got %d", *expectedHits, result.Hits)
		}
		stored.Manifest, stored.Tokens, stored.SnapshotIDs = result.Cache.Manifest, result.Cache.Tokens, nil
		stored.CacheID = result.Cache.ID
		for _, snapshot := range result.Cache.Snapshots {
			stored.SnapshotIDs = append(stored.SnapshotIDs, snapshot.ID)
		}
		data, err = json.Marshal(stored)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "metadata.json"), data, 0644); err != nil {
			return err
		}
		fmt.Printf("PASS: fresh-command native check reused %d/%d actions\n", result.Hits, result.Actions)
		return nil
	}
	if stage == "writer" {
		var planID core.ID
		if err := rust.Select("captureCheckPlan").Arg("source", sourceID).Select("id").Bind(&planID).Execute(ctx); err != nil {
			return err
		}
		cold, err := build(source, planID, "")
		if err != nil {
			return err
		}
		if cold.Actions != 4 || cold.Hits != 0 {
			return fmt.Errorf("cold result: %+v", cold)
		}
		var checked bool
		if err := rust.Arg("compilerPlan", planID).Arg("compilerCache", cold.Cache.ID).
			Select("check").Arg("source", sourceID).Select("pass").Bind(&checked).Execute(ctx); err != nil {
			return fmt.Errorf("normal check entrypoint: %w", err)
		}
		if !checked {
			return fmt.Errorf("normal check entrypoint failed")
		}
		original, err := source.File("app/src/part_0000.rs").Contents(ctx)
		if err != nil {
			return err
		}
		edited := regexp.MustCompile(`pub const EDIT: u64 = \d+;`).ReplaceAllString(original, "pub const EDIT: u64 = 987654321;")
		if edited == original {
			return fmt.Errorf("fixture edit did not change")
		}
		edit := source.WithNewFile("app/src/part_0000.rs", edited)
		changed, err := build(edit, planID, cold.Cache.ID)
		if err != nil {
			return err
		}
		if changed.Hits != 3 {
			return fmt.Errorf("edited result reused %d/4", changed.Hits)
		}
		reverted, err := build(source, planID, changed.Cache.ID)
		if err != nil {
			return err
		}
		if reverted.Hits != 4 {
			return fmt.Errorf("reversion reused %d/4", reverted.Hits)
		}
		for _, filename := range []string{"Cargo.toml", "Cargo.lock", ".cargo/config.toml", "rust-toolchain.toml"} {
			contents := "# added configuration\n"
			if filename == "Cargo.toml" || filename == "Cargo.lock" {
				original, err := source.File(filename).Contents(ctx)
				if err != nil {
					return err
				}
				contents = original + "\n# changed configuration\n"
			}
			_, err := build(source.WithNewFile(filename, contents), planID, reverted.Cache.ID)
			if err == nil || !strings.Contains(err.Error(), "build configuration changed; recapture") {
				return fmt.Errorf("configuration change %s was not rejected: %v", filename, err)
			}
		}
		modeChange := source.WithFile("Cargo.toml", source.File("Cargo.toml"), core.DirectoryWithFileOpts{Permissions: 0600})
		stat, err := modeChange.Stat(ctx, "Cargo.toml")
		if err != nil {
			return err
		}
		permissions, err := stat.Permissions(ctx)
		if err != nil || permissions != 0600 {
			return fmt.Errorf("configuration mode fixture has permissions %o: %v", permissions, err)
		}
		_, err = build(modeChange, planID, reverted.Cache.ID)
		if err == nil || !strings.Contains(err.Error(), "build configuration changed; recapture") {
			return fmt.Errorf("configuration permission change was not rejected: %v", err)
		}
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
		plan, err := core.Ref[*core.File](query, planID).Contents(ctx)
		if err != nil {
			return err
		}
		data, err := json.Marshal(transfer{Plan: plan, Manifest: changed.Cache.Manifest, Tokens: changed.Cache.Tokens, Edit: edited})
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, "metadata.json"), data, 0644); err != nil {
			return err
		}
		for i, snapshot := range changed.Cache.Snapshots {
			if _, err := core.Ref[*core.Directory](query, snapshot.ID).Export(ctx, filepath.Join(dir, fmt.Sprint(i))); err != nil {
				return err
			}
		}
		fmt.Println("PASS: actual Rust module cold check, edited 3/4 hits, reverted 4/4 hits; native cache fields exported")
		return nil
	}
	if stage != "reader" {
		return fmt.Errorf("unknown stage")
	}
	data, err := os.ReadFile(filepath.Join(dir, "metadata.json"))
	if err != nil {
		return err
	}
	var stored transfer
	if err := json.Unmarshal(data, &stored); err != nil {
		return err
	}
	var snapshots []*core.Directory
	for i := range stored.Tokens {
		snapshots = append(snapshots, query.Host().Directory(filepath.Join(dir, fmt.Sprint(i))))
	}
	var previous core.ID
	if err := rust.Select("restoreCompilerCache").Arg("manifest", stored.Manifest).Arg("tokens", stored.Tokens).Arg("snapshots", snapshots).Select("id").Bind(&previous).Execute(ctx); err != nil {
		return err
	}
	planID, err := query.Directory().WithNewFile("plan.json", stored.Plan).File("plan.json").ID(ctx)
	if err != nil {
		return err
	}
	edit := source.WithNewFile("app/src/part_0000.rs", stored.Edit)
	reused, err := build(edit, planID, previous)
	if err != nil {
		return err
	}
	if reused.Hits != 4 {
		return fmt.Errorf("fresh engine reused %d/4", reused.Hits)
	}
	next := regexp.MustCompile(`pub const EDIT: u64 = \d+;`).ReplaceAllString(stored.Edit, "pub const EDIT: u64 = 987654322;")
	changed, err := build(source.WithNewFile("app/src/part_0000.rs", next), planID, reused.Cache.ID)
	if err != nil {
		return err
	}
	if changed.Hits != 3 {
		return fmt.Errorf("fresh-engine edit reused %d/4", changed.Hits)
	}
	fmt.Println("PASS: actual Rust module transferred 4/4 hits and new edit with 3/4 hits")
	return nil
}
