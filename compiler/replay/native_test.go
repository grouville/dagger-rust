package replay

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	dagger "dagger.io/dagger/core"
	"github.com/Khan/genqlient/graphql"
	"github.com/dagger/querybuilder"
	"github.com/grouville/dagger-rust/compiler/model"
)

func TestNativePlanOptions(t *testing.T) {
	plan, _ := testPlan()
	plan.CargoCommand = "check"
	plan.CargoArgs = []string{"--features", "extra"}
	opts := CaptureOptions{Image: plan.Image, Command: "check", CargoArgs: plan.CargoArgs}
	if !planOptionsMatch(plan, opts) {
		t.Fatal("matching compilation options should reuse the plan")
	}
	for name, change := range map[string]func(*model.Plan){
		"version":     func(p *model.Plan) { p.Version++ },
		"image":       func(p *model.Plan) { p.Image = "another image" },
		"command":     func(p *model.Plan) { p.CargoCommand = "build" },
		"flags":       func(p *model.Plan) { p.CargoArgs = []string{"--release"} },
		"environment": func(p *model.Plan) { p.Environment = map[string]string{"RUSTFLAGS": "-C opt-level=1"} },
	} {
		t.Run(name, func(t *testing.T) {
			changed := *plan
			change(&changed)
			if planOptionsMatch(&changed, opts) {
				t.Fatal("reused a plan with different compilation options")
			}
		})
	}
}

type nativeInputClient struct {
	requests int
	err      error
	query    string
	response string
}

func (c *nativeInputClient) MakeRequest(_ context.Context, req *graphql.Request, resp *graphql.Response) error {
	c.requests++
	c.query = req.Query
	if c.err != nil {
		return c.err
	}
	response := c.response
	if response == "" {
		response = `{"node":{"id":"source","configuration":{"digest":"config"},"paths":["b/src/lib.rs","a/src/lib.rs"],"p0":{"digest":"a"},"p1":{"digest":"b"}}}`
	}
	return json.Unmarshal([]byte(response), resp.Data)
}

func TestNativePlanInputs(t *testing.T) {
	plan, _ := testPlan()
	plan.CargoCommand = "check"
	plan.NativeConfigDigest = "config"
	plan.NativeSourcePaths = []string{"a/src/lib.rs", "b/src/lib.rs"}
	c := &nativeInputClient{}
	query := new(dagger.Query).WithGraphQLQuery(querybuilder.Query().Client(c))
	source := dagger.Ref[*dagger.Directory](query, "source")
	opts := CaptureOptions{Image: plan.Image, Command: "check"}
	matches, inputs, err := NativePlanMatches(context.Background(), query, source, plan, opts)
	if err != nil || !matches || inputs == nil || !inputs.matches(source, plan) {
		t.Fatalf("matching inputs: %v %v %v", matches, inputs, err)
	}
	if inputs.packages["/src/a"] != "a" || inputs.packages["/src/b"] != "b" ||
		!strings.Contains(c.query, "configuration:") || !strings.Contains(c.query, "paths:") {
		t.Fatalf("missing batched inputs: %s", c.query)
	}
	if inputs.matches(&dagger.Directory{}, plan) {
		t.Fatal("inputs reused for another source")
	}
	changed := *plan
	changed.NativeConfigDigest = "changed"
	if inputs.matches(source, &changed) {
		t.Fatal("inputs reused after plan changed")
	}
	if _, err := BuildAutomatic(context.Background(), query, plan, nil, BuildOptions{SourceDirectory: &dagger.Directory{}, NativeInputs: inputs}, nil); err == nil {
		t.Fatal("build accepted inputs from another source")
	}
	for name, change := range map[string]func(*model.Plan){
		"configuration": func(p *model.Plan) { p.NativeConfigDigest = "changed" },
		"paths":         func(p *model.Plan) { p.NativeSourcePaths = []string{"new/src/lib.rs"} },
	} {
		t.Run(name, func(t *testing.T) {
			changed := *plan
			change(&changed)
			matches, inputs, err := NativePlanMatches(context.Background(), query, source, &changed, opts)
			if err != nil || matches || inputs != nil {
				t.Fatalf("stale plan accepted: %v %v %v", matches, inputs, err)
			}
		})
	}
	c.response = `{"node":{"id":"source","digest":"changed","configuration":{"digest":"changed"},"paths":["a/src/lib.rs"],"p0":{"digest":"a"}}}`
	if matches, inputs, err := NativePlanMatches(context.Background(), query, source, plan, opts); err != nil || matches || inputs != nil {
		t.Fatalf("removed package did not request recapture: %v %v %v", matches, inputs, err)
	}
	c.err = errors.New("request failed")
	if matches, inputs, err := NativePlanMatches(context.Background(), query, source, plan, opts); err == nil || matches || inputs != nil {
		t.Fatalf("query failure accepted: %v %v %v", matches, inputs, err)
	}
}
