package eval

import (
	"testing"

	"github.com/zclconf/go-cty/cty"
)

func FuzzEvaluateExpression(f *testing.F) {
	seeds := []string{
		`concat(["a", "b"], ["c"])`,
		`merge({ a = "1" }, { b = "2" })`,
		`lookup({ k = "v" }, "k", "d")`,
		`coalesce("", null, "val")`,
		`lower("UPPER")`,
		`upper("lower")`,
		`trimspace("  spaced  ")`,
		`jsondecode("{\"key\":\"value\"}")`,
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	ctx := BuildEvalContext(
		map[string]cty.Value{"env": cty.StringVal("prod")},
		map[string]cty.Value{"cluster": cty.StringVal("primary")},
	)

	f.Fuzz(func(t *testing.T, exprStr string) {
		// Invariant: EvaluateExpression must never panic regardless of arbitrary input string
		_, _ = EvaluateExpression(exprStr, ctx)
	})
}
