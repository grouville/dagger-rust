package model

import (
	"reflect"
	"testing"
)

func TestDepInfo(t *testing.T) {
	files, env, err := DepInfo("/target/a.d: src/lib.rs src/a\\ b.rs \\\n data.txt\n\n# env-dep:LABEL=hello=world\n# env-dep:ABSENT\n", "/src/pkg")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"/src/pkg/data.txt", "/src/pkg/src/a b.rs", "/src/pkg/src/lib.rs"}; !reflect.DeepEqual(want, files) {
		t.Fatalf("files: %v", files)
	}
	if env["LABEL"] == nil || *env["LABEL"] != "hello=world" {
		t.Fatalf("env: %v", env)
	}
	if value, ok := env["ABSENT"]; !ok || value != nil {
		t.Fatal("unset environment dependency lost")
	}
}

func TestDepInfoRejectsMissingDependencies(t *testing.T) {
	if _, _, err := DepInfo("not a depfile", "/src"); err == nil {
		t.Fatal("accepted missing dependency list")
	}
}
