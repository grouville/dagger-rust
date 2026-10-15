package worker

import (
	"fmt"
	"testing"

	"github.com/grouville/dagger-rust/compiler/model"
)

// Measure dependency selection separately from filesystem work and rustc.
// Each independent crate depends on one already available shared crate.
func BenchmarkBatchDependencyPreparation(b *testing.B) {
	for _, size := range []int{128, 1024} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			actions := make([]model.Action, size)
			for i := range actions {
				actions[i] = model.Action{ID: fmt.Sprint(i)}
				if i > 0 {
					actions[i].Dependencies = []string{"0"}
				}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				dependencies := make(map[string]model.Action, len(actions))
				for _, action := range actions {
					dependencies[action.ID] = action
				}
				for _, action := range actions {
					if err := prepareDependencies("", action, dependencies); err != nil {
						b.Fatal(err)
					}
				}
			}
		})
	}
}
