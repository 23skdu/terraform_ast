package policy

import (
	"bytes"
	"context"
	"testing"
)

func FuzzPolicyDecode(f *testing.F) {
	f.Add([]byte("block_breaking: true\nmax_bump: MAJOR\n"))
	f.Add([]byte("block_warnings: true\nblock_downgrades: true\nmax_bump: MINOR\n"))
	f.Add([]byte("invalid: [yaml: broken\n"))
	f.Add([]byte(""))
	f.Add([]byte("max_bump: INVALID_BUMP\n"))

	f.Fuzz(func(t *testing.T, data []byte) {
		p, err := Decode(bytes.NewReader(data))
		if err == nil && p != nil {
			input := Input{
				BreakingChanges: 1,
				Warnings:        1,
				IsDowngrade:     false,
				BumpType:        "MAJOR",
			}
			_ = Evaluate(p, input)
		}
	})
}

func FuzzParseRego(f *testing.F) {
	f.Add(`
package terraform.upgrade

deny[msg] {
    input.breaking_changes > 0
    msg := "Breaking changes prohibited"
}
`)
	f.Add(`
package terraform.upgrade
deny[msg] {
    input.bump_type == "MAJOR"
    msg := "Major upgrade disallowed"
}
`)
	f.Add("invalid syntax %$$#@!")
	f.Add("")

	f.Fuzz(func(t *testing.T, regoText string) {
		parsed, err := ParseRego(regoText)
		if err == nil && parsed != nil {
			input := Input{
				BreakingChanges: 2,
				Warnings:        1,
				IsDowngrade:     false,
				BumpType:        "MAJOR",
			}
			_ = EvaluateRego(parsed, input)

			arbitrary := map[string]any{
				"breaking_changes": 2,
				"bump_type":        "MAJOR",
			}
			_, _ = EvaluateRegoArbitrary(context.Background(), parsed, arbitrary)
		}
	})
}
