package replay

import (
	"bufio"
	"encoding/json"
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/grouville/dagger-rust/compiler/model"
)

func buildPublications(metadataJSON, messages string, actions []model.Action, allowEmpty bool) ([]model.Publication, error) {
	var workspace struct {
		Members []string `json:"workspace_members"`
	}
	if err := json.Unmarshal([]byte(metadataJSON), &workspace); err != nil {
		return nil, err
	}
	var publications []model.Publication
	seen := map[string]bool{}
	finished := false
	scanner := bufio.NewScanner(strings.NewReader(messages))
	scanner.Buffer(make([]byte, 4096), 16*1024*1024)
	for scanner.Scan() {
		var header struct {
			Reason string `json:"reason"`
		}
		if json.Unmarshal(scanner.Bytes(), &header) != nil {
			continue
		}
		switch header.Reason {
		case "build-finished":
			var status struct {
				Success bool `json:"success"`
			}
			if err := json.Unmarshal(scanner.Bytes(), &status); err != nil || !status.Success {
				return nil, fmt.Errorf("Cargo did not report a successful build")
			}
			finished = true
		case "compiler-artifact":
			var artifact struct {
				Package  string `json:"package_id"`
				Manifest string `json:"manifest_path"`
				Target   struct {
					Source string   `json:"src_path"`
					Kind   []string `json:"kind"`
					Name   string   `json:"name"`
				} `json:"target"`
				Filenames []string `json:"filenames"`
			}
			if err := json.Unmarshal(scanner.Bytes(), &artifact); err != nil {
				return nil, fmt.Errorf("read Cargo artifact: %w", err)
			}
			if !slices.Contains(workspace.Members, artifact.Package) || slices.Contains(artifact.Target.Kind, "custom-build") {
				continue
			}
			for _, filename := range artifact.Filenames {
				if !model.Within(model.TargetRoot, filename) || path.Clean(filename) != filename || filename == model.TargetRoot {
					return nil, fmt.Errorf("invalid Cargo artifact path %q", filename)
				}
				relative := targetRelative(filename)
				if strings.Contains("/"+relative, "/deps/") || strings.Contains("/"+relative, "/build/") || strings.Contains("/"+relative, "/incremental/") || strings.Contains("/"+relative, "/.fingerprint/") {
					continue
				}
				output := ""
				for _, action := range actions {
					if action.PackageRoot != path.Dir(artifact.Manifest) || action.Crate != strings.ReplaceAll(artifact.Target.Name, "-", "_") || !slices.Contains(action.Inputs, artifact.Target.Source) {
						continue
					}
					for _, candidate := range action.Outputs {
						if path.Ext(candidate) == ".d" || path.Ext(candidate) == ".rmeta" || path.Ext(candidate) != path.Ext(filename) {
							continue
						}
						if output != "" && output != candidate {
							return nil, fmt.Errorf("ambiguous compiler output for %q", filename)
						}
						output = candidate
					}
				}
				if output == "" || seen[relative] {
					return nil, fmt.Errorf("missing or duplicate compiler output for %q", filename)
				}
				seen[relative] = true
				publications = append(publications, model.Publication{Output: output, Path: relative})
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if !finished || (len(publications) == 0 && !allowEmpty) {
		return nil, fmt.Errorf("Cargo reported no successful workspace final artifacts")
	}
	slices.SortFunc(publications, func(a, b model.Publication) int { return strings.Compare(a.Path, b.Path) })
	return publications, nil
}
