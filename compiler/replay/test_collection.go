package replay

import (
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/grouville/dagger-rust/compiler/model"
)

// CollectionTarget keeps Cargo's actual command and its discovered libtest keys.
// Rustdoc remains one selectable entry per target; libtest exposes each test.
type CollectionTarget struct {
	Package string            `json:"package"`
	Name    string            `json:"name"`
	Command model.TestCommand `json:"command"`
	Names   []string          `json:"names"`
	Doc     bool              `json:"doc,omitempty"`
}

func CollectionTargets(plan *model.Plan) ([]CollectionTarget, error) {
	commands, err := testCommands(plan)
	if err != nil {
		return nil, err
	}
	outputs := map[string]*model.Action{}
	for i := range plan.Actions {
		for _, output := range plan.Actions[i].Outputs {
			outputs[output] = &plan.Actions[i]
		}
	}
	packages := map[string]string{}
	roots := newPackageRoots(plan.Packages)
	for _, pkg := range plan.Packages {
		packages[pkg.Root] = pkg.Name
	}
	var targets []CollectionTarget
	seen := map[string]bool{}
	for i, command := range commands {
		target := CollectionTarget{Command: command, Doc: plan.Tests[i].Executable == "rustdoc"}
		var root, input string
		if target.Doc {
			for _, arg := range command.Args {
				if strings.HasSuffix(arg, ".rs") {
					input = sourcePath(command.Cwd, arg)
					break
				}
			}
			root = roots.owner(input)
		} else {
			action := outputs[command.Executable]
			if action == nil || !slices.Contains(action.Args, "--test") {
				return nil, fmt.Errorf("test collection needs a standard libtest harness: %s", command.Executable)
			}
			root = action.PackageRoot
			for _, arg := range action.Args {
				if strings.HasSuffix(arg, ".rs") {
					input = sourcePath(action.Cwd, arg)
					break
				}
			}
		}
		target.Package = packages[root]
		if target.Package == "" || input == "" || !model.Within(root, input) {
			return nil, fmt.Errorf("cannot identify the Cargo test target %s", command.Executable)
		}
		target.Name = strings.TrimPrefix(input, root+"/")
		if target.Doc {
			target.Name = "doctests::" + target.Name
		}
		key := target.Package + "/" + target.Name
		if seen[key] {
			return nil, fmt.Errorf("ambiguous Cargo test target %s", key)
		}
		seen[key] = true
		targets = append(targets, target)
	}
	return targets, nil
}

func sourcePath(cwd, input string) string {
	if path.IsAbs(input) {
		return path.Clean(input)
	}
	return path.Join(cwd, input)
}

func CollectionListing(targets []CollectionTarget) []model.TestCommand {
	var commands []model.TestCommand
	for _, target := range targets {
		if target.Doc {
			continue
		}
		command := target.Command
		command.Args = append(slices.Clone(command.Args), "--list", "--format=terse")
		commands = append(commands, command)
	}
	return commands
}

func ReadCollectionListing(targets []CollectionTarget, output string) error {
	byExecutable := map[string]int{}
	for i, target := range targets {
		if !target.Doc {
			byExecutable[target.Command.Executable] = i
		}
	}
	current := -1
	listed := map[int]bool{}
	names := map[string]bool{}
	for _, line := range strings.Split(output, "\n") {
		if executable, ok := strings.CutPrefix(line, "==> "); ok {
			i, found := byExecutable[executable]
			if !found || listed[i] {
				return fmt.Errorf("unexpected test-listing executable %q", executable)
			}
			current = i
			listed[i] = true
			names = map[string]bool{}
			continue
		}
		if name, ok := strings.CutSuffix(line, ": test"); ok {
			if current < 0 || name == "" || names[name] {
				return fmt.Errorf("invalid test-listing entry %q", line)
			}
			targets[current].Names = append(targets[current].Names, name)
			names[name] = true
		}
	}
	if len(listed) != len(byExecutable) {
		return fmt.Errorf("incomplete test listing: %d/%d harnesses", len(listed), len(byExecutable))
	}
	return nil
}

func CollectionKeys(target CollectionTarget) []string {
	if target.Doc {
		return []string{target.Name}
	}
	keys := make([]string, len(target.Names))
	for i, name := range target.Names {
		keys[i] = target.Name + "::" + name
	}
	return keys
}

// CollectionSelection starts at most one process for each selected harness.
// libtest has one positive filter; exact skip filters express an arbitrary subset.
func CollectionSelection(targets []CollectionTarget, pkg string, keys []string) ([]model.TestCommand, error) {
	selected := map[string]bool{}
	for _, key := range keys {
		selected[key] = true
	}
	known := map[string]bool{}
	var commands []model.TestCommand
	for _, target := range targets {
		if target.Package != pkg {
			continue
		}
		all := CollectionKeys(target)
		count := 0
		for _, key := range all {
			known[key] = true
			if selected[key] {
				count++
			}
		}
		if count == 0 {
			continue
		}
		command := target.Command
		if count != len(all) {
			command.Args = append(slices.Clone(command.Args), "--exact")
			for i, key := range all {
				if !selected[key] {
					command.Args = append(command.Args, "--skip", target.Names[i])
				}
			}
		}
		commands = append(commands, command)
	}
	for key := range selected {
		if !known[key] {
			return nil, fmt.Errorf("unknown test %q in package %q", key, pkg)
		}
	}
	return commands, nil
}
