package replay

import (
	"encoding/json"
	"fmt"
	"path"
	"slices"
	"sort"
	"strings"

	"github.com/grouville/dagger-rust/compiler/model"
)

type metadata struct {
	Packages []struct {
		ID           string  `json:"id"`
		Name         string  `json:"name"`
		Source       *string `json:"source"`
		ManifestPath string  `json:"manifest_path"`
		Targets      []struct {
			Kind []string `json:"kind"`
		} `json:"targets"`
	} `json:"packages"`
}

func parseMetadata(contents string) ([]model.Package, error) {
	var meta metadata
	if err := json.Unmarshal([]byte(contents), &meta); err != nil {
		return nil, err
	}
	var packages []model.Package
	for _, pkg := range meta.Packages {
		root := path.Dir(pkg.ManifestPath)
		if pkg.Source != nil || !model.Within(model.SourceRoot, root) {
			return nil, fmt.Errorf("unsupported dependency %s: only path packages inside /src are supported", pkg.Name)
		}
		for _, target := range pkg.Targets {
			if slices.Contains(target.Kind, "custom-build") || slices.Contains(target.Kind, "proc-macro") {
				return nil, fmt.Errorf("unsupported package %s: build scripts and procedural macros are not yet supported", pkg.Name)
			}
		}
		packages = append(packages, model.Package{ID: pkg.ID, Name: pkg.Name, Root: root})
	}
	if len(packages) == 0 {
		return nil, fmt.Errorf("metadata contains no packages")
	}
	sort.Slice(packages, func(i, j int) bool { return packages[i].Root < packages[j].Root })
	return packages, nil
}

// Validate checks both the supported boundary and the graph before any replay.
func Validate(plan *model.Plan, source Source) error {
	if plan.ConfigDigest != source.ConfigDigest() {
		return fmt.Errorf("build configuration changed; recapture after manifest, lockfile or toolchain configuration changes")
	}
	return validateGraph(plan)
}

func validateBuild(plan *model.Plan, source Source, opts BuildOptions) error {
	if opts.nativeConfigDigest == "" {
		return Validate(plan, source)
	}
	if opts.SourceDirectory == nil || plan.NativeConfigDigest != opts.nativeConfigDigest {
		return fmt.Errorf("build configuration changed; recapture after manifest, lockfile or toolchain configuration changes")
	}
	return validateGraph(plan)
}

func validateGraph(plan *model.Plan) error {
	if plan.Version != model.Version {
		return fmt.Errorf("unsupported plan version %d", plan.Version)
	}
	if plan.CargoCommand != "" && plan.CargoCommand != "build" && plan.CargoCommand != "check" {
		return fmt.Errorf("unsupported captured Cargo command %s", plan.CargoCommand)
	}
	if !strings.Contains(plan.Image, "@sha256:") {
		return fmt.Errorf("plan toolchain image must be pinned by digest")
	}
	if len(plan.Actions) == 0 {
		return fmt.Errorf("plan contains no compiler actions")
	}
	producers := map[string]string{}
	ids := map[string]bool{}
	roots := newPackageRoots(plan.Packages)
	for _, a := range plan.Actions {
		if a.ID == "" || ids[a.ID] {
			return fmt.Errorf("duplicate or empty action ID %q", a.ID)
		}
		ids[a.ID] = true
		if !model.Within(model.SourceRoot, a.Cwd) || roots.owner(a.PackageRoot) != a.PackageRoot {
			return fmt.Errorf("action %s has an unknown package or working directory", a.ID)
		}
		if !path.IsAbs(a.Compiler) || model.Within(model.SourceRoot, a.Compiler) || model.Within(model.TargetRoot, a.Compiler) {
			return fmt.Errorf("action %s requires a compiler from the toolchain image", a.ID)
		}
		if slices.Contains(a.Args, "--test") || strings.Contains(strings.Join(a.Args, " "), "incremental=") {
			return fmt.Errorf("action %s uses unsupported test or incremental compilation", a.ID)
		}
		if model.Option(a.Args, "--target") != "" {
			return fmt.Errorf("action %s selects an unsupported custom or cross-compilation target", a.ID)
		}
		for _, kind := range strings.Split(model.Option(a.Args, "--crate-type"), ",") {
			if kind != "lib" && kind != "rlib" && kind != "bin" {
				return fmt.Errorf("unsupported crate type %q in %s", kind, a.ID)
			}
		}
		if len(a.Inputs) == 0 || len(a.Outputs) == 0 {
			return fmt.Errorf("action %s has no source inputs or outputs", a.ID)
		}
		for _, filename := range a.Inputs {
			if roots.owner(filename) != a.PackageRoot {
				return fmt.Errorf("unsupported source input %s outside package %s", filename, a.PackageRoot)
			}
		}
		for _, filename := range a.Outputs {
			if !model.Within(model.TargetRoot, filename) || path.Clean(filename) != filename {
				return fmt.Errorf("unsupported output path %s", filename)
			}
			if previous := producers[filename]; previous != "" {
				return fmt.Errorf("output %s has multiple producers", filename)
			}
			producers[filename] = a.ID
		}
	}
	for i := range plan.Actions {
		a := &plan.Actions[i]
		var dependencies []string
		for _, filename := range externFiles(a.Args) {
			producer := producers[filename]
			if producer == "" {
				return fmt.Errorf("dependency artifact %s has no captured producer", filename)
			}
			dependencies = append(dependencies, producer)
		}
		sort.Strings(dependencies)
		dependencies = slices.Compact(dependencies)
		// Dependency edges are derived rather than trusted from the plan file.
		a.Dependencies = dependencies
	}
	_, err := ordered(plan.Actions)
	return err
}

func externFiles(args []string) []string {
	var files []string
	for i, arg := range args {
		value := ""
		if arg == "--extern" && i+1 < len(args) {
			value = args[i+1]
		}
		if v, ok := strings.CutPrefix(arg, "--extern="); ok {
			value = v
		}
		if _, filename, ok := strings.Cut(value, "="); ok {
			files = append(files, filename)
		}
	}
	return files
}

func ordered(actions []model.Action) ([]model.Action, error) {
	byID := map[string]model.Action{}
	for _, a := range actions {
		byID[a.ID] = a
	}
	state := map[string]int{}
	var result []model.Action
	var visit func(string) error
	visit = func(id string) error {
		if state[id] == 2 {
			return nil
		}
		if state[id] == 1 {
			return fmt.Errorf("compiler dependency cycle at %s", id)
		}
		a, ok := byID[id]
		if !ok {
			return fmt.Errorf("unknown dependency %s", id)
		}
		state[id] = 1
		for _, dep := range a.Dependencies {
			if err := visit(dep); err != nil {
				return err
			}
		}
		state[id] = 2
		result = append(result, a)
		return nil
	}
	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if err := visit(id); err != nil {
			return nil, err
		}
	}
	return result, nil
}
