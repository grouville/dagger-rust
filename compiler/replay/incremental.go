package replay

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/grouville/dagger-rust/compiler/model"
)

const incrementalPath = "/rcexp-incremental"

// IncrementalState references native filesystem state, rather than copying it
// through the host or retaining it in a CacheVolume. It is engine-local until
// the referenced native cache results are transferred to another engine.
type IncrementalState struct {
	Version       int    `json:"version"`
	Crate         string `json:"crate"`
	Compatibility string `json:"compatibility"`
	DirectoryID   string `json:"directory_id"`
}

// SeedCompatibility deliberately excludes source contents and compiled output
// digests: those may change between builds. Keep captured compiler configuration
// and dependency graph configuration fixed for this first experiment.
func SeedCompatibility(plan *model.Plan, crate string) (string, error) {
	matches := 0
	for _, action := range plan.Actions {
		if action.Crate == crate {
			matches++
		}
	}
	if matches != 1 {
		return "", fmt.Errorf("incremental crate %q must select exactly one compiler action, got %d", crate, matches)
	}
	configuration := recipeConfiguration(plan)
	data, err := json.Marshal(struct {
		Crate string
		Plan  model.Plan
	}{crate, configuration})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), nil
}

func recipeConfiguration(plan *model.Plan) model.Plan {
	configuration := *plan
	// This validates the native source without changing compiler identities.
	configuration.NativeConfigDigest = ""
	configuration.NativeSourcePaths = nil
	// The documentation-test launcher does not affect compiler results or seeds.
	configuration.Rustdoc = ""
	configuration.Actions = slices.Clone(plan.Actions)
	configuration.BaselineDigests = nil
	configuration.CaptureSeconds = 0
	for i := range configuration.Actions {
		configuration.Actions[i].CompilerSeconds = 0
		configuration.Actions[i].CompilerStartedUnixNanos = 0
		configuration.Actions[i].MetadataSeconds = 0
		configuration.Actions[i].Inputs = nil
	}
	slices.SortFunc(configuration.Actions, func(a, b model.Action) int {
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	return configuration
}

func (s IncrementalState) Validate(plan *model.Plan, crate string) error {
	compatibility, err := SeedCompatibility(plan, crate)
	if err != nil {
		return err
	}
	if s.Version != 1 || s.Crate != crate || s.Compatibility != compatibility || s.DirectoryID == "" {
		return fmt.Errorf("incremental seed is incompatible with the selected crate or compiler configuration")
	}
	return nil
}
