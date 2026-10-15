package replay

import (
	"fmt"
	"slices"
	"strings"
)

// Package selections must omit --workspace: Cargo otherwise builds the union.
func CargoFlags(args []string) []string {
	flags := []string{"--workspace", "--locked"}
	for _, arg := range args {
		if arg == "--package" || strings.HasPrefix(arg, "--package=") || arg == "-p" || strings.HasPrefix(arg, "-p") {
			flags = []string{"--locked"}
			break
		}
	}
	return append(flags, args...)
}

func CaptureArgs(flags []string) ([]string, error) {
	args := flags
	if len(args) > 0 && args[0] == "--workspace" {
		args = args[1:]
	}
	if len(args) == 0 || args[0] != "--locked" {
		return nil, fmt.Errorf("native Cargo commands require --locked")
	}
	args = args[1:]
	if slices.Contains(args, "--workspace") || !slices.Equal(flags, CargoFlags(args)) {
		return nil, fmt.Errorf("select either the workspace or named packages")
	}
	return args, nil
}
