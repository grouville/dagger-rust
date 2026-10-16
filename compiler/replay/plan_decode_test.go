package replay

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/grouville/dagger-rust/compiler/model"
)

func compilerPlanDecodeCases() []string {
	return []string{
		`null`, `{}`, `{"version":1,"actions":null}`, `{"version":1,"actions":[]}`,
		`{"version":1,"actions":[{"id":"a","args":["x","y"],"env":{"A":"1","B":"2"}}]}`,
		`{"version":1,"actions":[{"id":"a","env":{"A":"1","A":"2"}}]}`,
		`{"version":1,"actions":[{"id":"a","args":["\ud800","\udc00","\ud83d\ude00"]}]}`,
		"{\"version\":1,\"actions\":[{\"id\":\"bad\xffutf8\"}]}",
		`{"version":1,"VERSION":2,"actions":[{"id":"a","compiler_seconds":0.10000000000000002}]}`,
		`{"version":1,"actions":[{"id":"a","id":null,"env":{"A":"1"},"env":null}]}`,
		`{"version":1,"actions":[{"id":"a"}],"unknown":{"deep":[{"nested":true}]}}`,
		`{"version":1} trailing`, `{"version":1}{}`, `{"version":"bad"}`, `{"version":1.5}`,
		`{"version":1,"actions":[{"args":[1]}]}`, `{"version":1,"actions":[{"compiler_seconds":1e999}]}`,
		`{"version":1,"actions":[{"id":"bad\x"}]}`, `{"version":1,}`, `{"version":1`,
	}
}

// A decoder difference could change compiler inputs or accept an invalid plan.
func checkCompilerPlanDecode(t *testing.T, contents []byte) {
	t.Helper()
	var expected, actual model.Plan
	referenceErr := json.Unmarshal(contents, &expected)
	actualErr := UnmarshalPlan(contents, &actual)
	if (referenceErr == nil) != (actualErr == nil) {
		t.Fatalf("different error behavior for %q: reference=%v actual=%v", contents, referenceErr, actualErr)
	}
	if referenceErr == nil && !reflect.DeepEqual(expected, actual) {
		t.Fatalf("decoded compiler inputs differ for %q: expected=%#v actual=%#v", contents, expected, actual)
	}
}

func TestCompilerPlanDecoderCompatibility(t *testing.T) {
	for _, input := range compilerPlanDecodeCases() {
		t.Run(input, func(t *testing.T) { checkCompilerPlanDecode(t, []byte(input)) })
	}
}

func FuzzCompilerPlanDecoderCompatibility(f *testing.F) {
	for _, input := range compilerPlanDecodeCases() {
		f.Add([]byte(input))
	}
	f.Fuzz(func(t *testing.T, contents []byte) { checkCompilerPlanDecode(t, contents) })
}
