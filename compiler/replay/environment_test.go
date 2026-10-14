package replay

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/grouville/dagger-rust/compiler/model"
)

func TestCompilerEnvironment(t *testing.T) {
	type observed struct {
		Values map[string]string
		Unset  []string
		Args   []string
	}
	keys := []string{"RCE_ENV_LITERAL", "RCE_ENV_EMPTY", "RCE_ENV_UNSET", "RCE_ENV_OVERLAP"}
	if os.Getenv("RCE_ENV_PROBE") == "1" {
		result := observed{Values: map[string]string{}}
		for _, key := range keys {
			if value, ok := os.LookupEnv(key); ok {
				result.Values[key] = value
			} else {
				result.Unset = append(result.Unset, key)
			}
		}
		for i, arg := range os.Args {
			if arg == "--" {
				result.Args = os.Args[i+1:]
				break
			}
		}
		if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
			t.Fatal(err)
		}
		return
	}

	literal := " spaces 'quotes' \"double\" $PATH $(exit 1) `exit 1` \\ backslash\nnew line ☃"
	a := model.Action{
		Env:      map[string]string{"RCE_ENV_PROBE": "1", "RCE_ENV_LITERAL": literal, "RCE_ENV_EMPTY": "", "RCE_ENV_OVERLAP": "set"},
		UnsetEnv: []string{"RCE_ENV_UNSET", "RCE_ENV_OVERLAP"},
	}
	compilerArgs := []string{"--extern=a=/target/path with spaces.rlib", literal}
	command := append([]string{os.Args[0], "-test.run=^TestCompilerEnvironment$", "--"}, compilerArgs...)
	args := compilerEnvironment(a, command)
	cmd := exec.Command(args[0], args[1:]...)
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "RCE_ENV_") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "RCE_ENV_UNSET=inherited", "RCE_ENV_OVERLAP=inherited")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("environment probe: %v\n%s", err, output)
	}
	var result observed
	if err := json.NewDecoder(bytes.NewReader(output)).Decode(&result); err != nil {
		t.Fatal(err)
	}
	want := observed{
		Values: map[string]string{"RCE_ENV_LITERAL": literal, "RCE_ENV_EMPTY": ""},
		Unset:  []string{"RCE_ENV_UNSET", "RCE_ENV_OVERLAP"}, Args: compilerArgs,
	}
	if !reflect.DeepEqual(result, want) {
		t.Fatalf("environment or compiler arguments changed: got %#v, want %#v", result, want)
	}
}
