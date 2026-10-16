package replay

import (
	json "github.com/goccy/go-json"
	"github.com/grouville/dagger-rust/compiler/model"
)

// UnmarshalPlan decodes captured compiler inputs. Canonical plan and result-key
// encoding continue to use encoding/json, preserving existing cache identities.
func UnmarshalPlan(contents []byte, plan *model.Plan) error {
	return json.Unmarshal(contents, plan)
}
