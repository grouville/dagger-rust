package replay

import (
	"context"
	"fmt"
	"path"

	dagger "dagger.io/dagger/core"
	"github.com/pelletier/go-toml"
)

func validateTestConfiguration(source Source) error {
	for _, name := range []string{".cargo/config", ".cargo/config.toml"} {
		file, present := source[name]
		if !present {
			continue
		}
		return validateTestConfig(file.Contents)
	}
	return nil
}

func validateTestConfig(contents string) error {
	configuration, err := toml.Load(contents)
	if err != nil {
		return fmt.Errorf("read Cargo test configuration: %w", err)
	}
	targets, ok := configuration.Get("target").(*toml.Tree)
	if !ok {
		return nil
	}
	for _, key := range targets.Keys() {
		if target, ok := targets.Get(key).(*toml.Tree); ok && target.Has("runner") {
			return fmt.Errorf("native test capture does not yet support Cargo target runners")
		}
	}
	return nil
}

// Cargo also reads image configuration from / and CARGO_HOME. Check those
// before replacing its target runner, so a custom runner is never bypassed.
func validateImageTestConfiguration(ctx context.Context, container *dagger.Container) error {
	home, err := container.EnvVariable(ctx, "CARGO_HOME")
	if err != nil {
		return err
	}
	if home == "" {
		userHome, err := container.EnvVariable(ctx, "HOME")
		if err != nil {
			return err
		}
		if !path.IsAbs(userHome) {
			return fmt.Errorf("native test capture requires an absolute HOME or CARGO_HOME")
		}
		home = path.Join(userHome, ".cargo")
	} else if !path.IsAbs(home) {
		home = path.Join("/src", home)
	}
	root := container.Directory("/")
	for _, directory := range []string{"/.cargo", home} {
		for _, name := range []string{"config", "config.toml"} {
			filename := path.Join(directory, name)
			present, err := root.Exists(ctx, filename)
			if err != nil {
				return err
			}
			if !present {
				continue
			}
			contents, err := root.File(filename).Contents(ctx)
			if err != nil {
				return err
			}
			if err := validateTestConfig(contents); err != nil {
				return err
			}
			break // Cargo prefers config when both file names exist.
		}
	}
	return nil
}
