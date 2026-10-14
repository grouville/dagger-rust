package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

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
	Plan     string
	Manifest string
	Tokens   []string
	Edit     string
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) != 3 {
		return fmt.Errorf("usage: integration writer|reader TRANSFER_DIRECTORY")
	}
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
	source := query.Host().Directory("/tmp/rust-cache-sipsma-review/module-baseline/source", core.HostDirectoryOpts{Exclude: []string{".git", "target", "dagger.toml", "dagger.lock"}})
	sourceID, err := source.ID(ctx)
	if err != nil {
		return err
	}
	if os.Args[1] == "digests" {
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
		if previous != "" {
			root = root.Arg("compilerCache", previous)
		}
		var result compilation
		err = root.Select("nativeCheck").Arg("source", id).
			SelectMultiple("id", "hits", "actions", "artifacts { id }", "cache { id manifest tokens snapshots { id } }").Bind(&result).Execute(ctx)
		return &result, err
	}
	dir := os.Args[2]
	if os.Args[1] == "writer" {
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
		original, err := source.File("app/src/part_0000.rs").Contents(ctx)
		if err != nil {
			return err
		}
		edited := regexp.MustCompile(`pub const EDIT: u64 = \d+;`).ReplaceAllString(original, "pub const EDIT: u64 = 987654321;")
		if edited == original {
			return fmt.Errorf("fixture edit did not change")
		}
		filename := "/tmp/rust-cache-sipsma-review/module-baseline/source/app/src/part_0000.rs"
		defer os.WriteFile(filename, []byte(original), 0644)
		if err := os.WriteFile(filename, []byte(edited), 0644); err != nil {
			return err
		}
		edit := query.Host().Directory("/tmp/rust-cache-sipsma-review/module-baseline/source", core.HostDirectoryOpts{Exclude: []string{".git", "target", "dagger.toml", "dagger.lock"}})
		changed, err := build(edit, planID, cold.Cache.ID)
		if err != nil {
			return err
		}
		if changed.Hits != 3 {
			_ = os.WriteFile("/tmp/rust-cache-sipsma-review/module-cold-manifest.json", []byte(cold.Cache.Manifest), 0644)
			_ = os.WriteFile("/tmp/rust-cache-sipsma-review/module-edited-manifest.json", []byte(changed.Cache.Manifest), 0644)
			return fmt.Errorf("edited result reused %d/4", changed.Hits)
		}
		if err := os.WriteFile(filename, []byte(original), 0644); err != nil {
			return err
		}
		revertedSource := query.Host().Directory("/tmp/rust-cache-sipsma-review/module-baseline/source", core.HostDirectoryOpts{Exclude: []string{".git", "target", "dagger.toml", "dagger.lock"}})
		reverted, err := build(revertedSource, planID, changed.Cache.ID)
		if err != nil {
			return err
		}
		if reverted.Hits != 4 {
			return fmt.Errorf("reversion reused %d/4", reverted.Hits)
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
	if os.Args[1] != "reader" {
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
	filename := "/tmp/rust-cache-sipsma-review/module-baseline/source/app/src/part_0000.rs"
	original, err := os.ReadFile(filename)
	if err != nil {
		return err
	}
	defer os.WriteFile(filename, original, 0644)
	if err := os.WriteFile(filename, []byte(stored.Edit), 0644); err != nil {
		return err
	}
	edit := query.Host().Directory("/tmp/rust-cache-sipsma-review/module-baseline/source", core.HostDirectoryOpts{Exclude: []string{".git", "target", "dagger.toml", "dagger.lock"}})
	reused, err := build(edit, planID, previous)
	if err != nil {
		return err
	}
	if reused.Hits != 4 {
		return fmt.Errorf("fresh engine reused %d/4", reused.Hits)
	}
	next := regexp.MustCompile(`pub const EDIT: u64 = \d+;`).ReplaceAllString(stored.Edit, "pub const EDIT: u64 = 987654322;")
	if err := os.WriteFile(filename, []byte(next), 0644); err != nil {
		return err
	}
	nextSource := query.Host().Directory("/tmp/rust-cache-sipsma-review/module-baseline/source", core.HostDirectoryOpts{Exclude: []string{".git", "target", "dagger.toml", "dagger.lock"}})
	changed, err := build(nextSource, planID, reused.Cache.ID)
	if err != nil {
		return err
	}
	if changed.Hits != 3 {
		return fmt.Errorf("fresh-engine edit reused %d/4", changed.Hits)
	}
	fmt.Println("PASS: actual Rust module transferred 4/4 hits and new edit with 3/4 hits")
	return nil
}
