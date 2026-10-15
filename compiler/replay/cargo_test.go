package replay

import (
	"slices"
	"testing"
)

func TestCargoPackageSelectionOmitsWorkspace(t *testing.T) {
	for _, args := range [][]string{
		{"--package", "app"}, {"-p", "app"}, {"--package=app"}, {"-papp"},
		{"--package", "app", "--package", "base", "--features", "extra"},
	} {
		flags := CargoFlags(args)
		if slices.Contains(flags, "--workspace") || !slices.Contains(flags, "--locked") {
			t.Fatalf("wrong scope: %v", flags)
		}
		got, err := CaptureArgs(flags)
		if err != nil || !slices.Equal(got, args) {
			t.Fatalf("selection changed: %v, %v", got, err)
		}
	}
	if got := CargoFlags([]string{"--release"}); !slices.Equal(got, []string{"--workspace", "--locked", "--release"}) {
		t.Fatalf("workspace scope changed: %v", got)
	}
}

func TestCargoPackageSelectionRejectsWorkspaceUnion(t *testing.T) {
	for _, flags := range [][]string{
		{"--workspace", "--locked", "--package", "app"},
		{"--locked", "--package", "app", "--workspace"},
		{"--package", "app"}, {"--locked"},
	} {
		if _, err := CaptureArgs(flags); err == nil {
			t.Fatalf("accepted ambiguous or unlocked scope: %v", flags)
		}
	}
}
