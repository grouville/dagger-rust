package main

import (
	"reflect"
	"testing"
	"time"
)

func TestArtifactTimingHandlesFragmentedMetadataMessages(t *testing.T) {
	observer := artifactTiming{started: time.Now().Add(-time.Second)}
	for _, chunk := range []string{"diagnostic\n{\"$message_type\":\"arti", "fact\",\"emit\":\"metadata\"}"} {
		if _, err := observer.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	if observer.metadataSeconds != 0 {
		t.Fatal("incomplete message announced metadata")
	}
	if _, err := observer.Write([]byte("\n")); err != nil {
		t.Fatal(err)
	}
	if observer.metadataSeconds < 1 {
		t.Fatal("metadata readiness was not recorded")
	}
	first := observer.metadataSeconds
	_, err := observer.Write([]byte("{\"$message_type\":\"artifact\",\"emit\":\"metadata\"}\n"))
	if err != nil || observer.metadataSeconds != first {
		t.Fatal("a later message replaced initial readiness")
	}
}

func TestEnvironmentInputs(t *testing.T) {
	label := "value"
	values, unset, err := environment([]string{"CARGO_PKG_NAME=pkg", "PATH=/bin", "CARGO_MAKEFLAGS=--jobserver-auth=3,4", "DAGGER_SESSION_TOKEN=transport", "IGNORED=unused", "LABEL=value"}, map[string]*string{"LABEL": &label, "ABSENT": nil})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"CARGO_PKG_NAME": "pkg", "PATH": "/bin", "LABEL": "value"}
	if !reflect.DeepEqual(values, want) || !reflect.DeepEqual(unset, []string{"ABSENT"}) {
		t.Fatalf("environment: %v, unset: %v", values, unset)
	}
	if _, _, err := environment(nil, map[string]*string{"DAGGER_SESSION_TOKEN": &label}); err == nil {
		t.Fatal("accepted a session-dependent compilation")
	}
}

func TestEnvironmentPreservesEscapedValues(t *testing.T) {
	escaped := `first\nsecond\\third`
	actual := "first\nsecond\\third"
	values, _, err := environment([]string{"LABEL=" + actual}, map[string]*string{"LABEL": &escaped})
	if err != nil {
		t.Fatal(err)
	}
	if values["LABEL"] != actual {
		t.Fatalf("environment value changed: %q", values["LABEL"])
	}
}

func TestArtifactsIgnoreDiagnostics(t *testing.T) {
	files, err := artifacts([]byte("warning\n{\"$message_type\":\"diagnostic\",\"artifact\":\"wrong\"}\n{\"$message_type\":\"artifact\",\"artifact\":\"/target/libpkg.rlib\"}\n"), "/src")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(files, []string{"/target/libpkg.rlib"}) {
		t.Fatalf("outputs: %v", files)
	}
}
