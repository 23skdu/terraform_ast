package policy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDecodePolicy(t *testing.T) {
	got, err := Decode(strings.NewReader(`{"block_breaking":true,"max_bump":"minor"}`))
	require.NoError(t, err)
	assert.True(t, got.BlockBreaking)
	assert.Equal(t, "MINOR", got.MaxBump)

	_, err = Decode(strings.NewReader(`{"unknown":true}`))
	assert.ErrorContains(t, err, "unknown field")
	_, err = Decode(strings.NewReader(`{"max_bump":"release"}`))
	assert.ErrorContains(t, err, "invalid max_bump")
	_, err = Decode(strings.NewReader(`{} {}`))
	assert.ErrorContains(t, err, "multiple JSON values")
}

func TestEvaluatePolicy(t *testing.T) {
	got := Evaluate(&Policy{
		BlockBreaking:   true,
		BlockWarnings:   true,
		BlockDowngrades: true,
		MaxBump:         "MINOR",
	}, Input{
		BreakingChanges: 2,
		Warnings:        1,
		IsDowngrade:     true,
		BumpType:        "MAJOR",
	})
	require.Len(t, got, 4)
	assert.Equal(t, []string{"block_breaking", "block_warnings", "block_downgrades", "max_bump"}, []string{
		got[0].Rule, got[1].Rule, got[2].Rule, got[3].Rule,
	})
	assert.Empty(t, Evaluate(nil, Input{BreakingChanges: 1}))
	assert.Empty(t, Evaluate(&Policy{MaxBump: "MAJOR"}, Input{BumpType: "MAJOR"}))
	assert.Empty(t, Evaluate(&Policy{MaxBump: "PATCH"}, Input{BumpType: "IDENTICAL"}))

	unknownBump := Evaluate(&Policy{MaxBump: "MAJOR"}, Input{})
	require.Len(t, unknownBump, 1)
	assert.Equal(t, "max_bump", unknownBump[0].Rule)
	assert.Contains(t, unknownBump[0].Message, "version information unavailable")
}

func TestLoadPolicyFromFile(t *testing.T) {
	dir := t.TempDir()

	valid := filepath.Join(dir, "policy.json")
	require.NoError(t, os.WriteFile(valid, []byte(`{"block_breaking":true,"max_bump":" patch "}`), 0600))
	got, err := Load(valid)
	require.NoError(t, err)
	assert.True(t, got.BlockBreaking)
	assert.Equal(t, "PATCH", got.MaxBump)

	_, err = Load(filepath.Join(dir, "missing.json"))
	assert.ErrorContains(t, err, "open policy file")

	invalid := filepath.Join(dir, "invalid.json")
	require.NoError(t, os.WriteFile(invalid, []byte(`{"max_bump":"release"}`), 0600))
	_, err = Load(invalid)
	assert.ErrorContains(t, err, "invalid max_bump")
}

func TestDecodeRejectsTrailingGarbage(t *testing.T) {
	_, err := Decode(strings.NewReader(`{"block_breaking":true} trailing`))
	assert.ErrorContains(t, err, "trailing data")
}

func TestRegoPolicy(t *testing.T) {
	regoSrc := `
package terraform_ast.enterprise

# Disallow breaking changes
deny[msg] {
    input.breaking_changes > 0
    msg := "breaking changes disallowed"
}

# Disallow downgrades
deny[msg] {
    input.is_downgrade == true
    msg := "downgrades forbidden"
}

# Require review for major bumps
violation[msg] {
    input.bump_type == "MAJOR"
    msg := "major upgrade requires architectural review"
}

# Rule without custom msg
deny[msg] {
    input.warnings >= 5
}

# Inequality condition checks
deny[msg] {
    input.breaking_changes != 0
    input.breaking_changes <= 10
    input.breaking_changes < 5
    input.is_downgrade != false
    input.bump_type != "PATCH"
}
`
	policy, err := ParseRego(regoSrc)
	require.NoError(t, err)
	assert.Equal(t, "terraform_ast.enterprise", policy.Package)
	assert.Len(t, policy.Rules, 5)

	// Test evaluation with violations
	input := Input{
		BreakingChanges: 2,
		Warnings:        6,
		IsDowngrade:     true,
		BumpType:        "MAJOR",
	}
	violations := EvaluateRego(policy, input)
	require.NotEmpty(t, violations)
	assert.Equal(t, "deny", violations[0].Rule)
	assert.Equal(t, "breaking changes disallowed", violations[0].Message)

	// Clean input
	cleanInput := Input{
		BreakingChanges: 0,
		Warnings:        0,
		IsDowngrade:     false,
		BumpType:        "PATCH",
	}
	assert.Empty(t, EvaluateRego(policy, cleanInput))
	assert.Nil(t, EvaluateRego(nil, cleanInput))

	// Invalid condition syntax
	_, errInvalidCond := ParseRego("deny[msg] {\n input.invalid\n}")
	assert.Error(t, errInvalidCond)

	// Condition type conversion failure
	badIntCond := RegoCondition{Field: "breaking_changes", Operator: ">", Value: "not-an-int"}
	assert.False(t, matchesCondition(badIntCond, input))

	badWarningCond := RegoCondition{Field: "warnings", Operator: ">", Value: "not-an-int"}
	assert.False(t, matchesCondition(badWarningCond, input))

	badDowngradeCond := RegoCondition{Field: "is_downgrade", Operator: ">", Value: "true"}
	assert.False(t, matchesCondition(badDowngradeCond, input))

	badBumpCond := RegoCondition{Field: "bump_type", Operator: ">", Value: "MAJOR"}
	assert.False(t, matchesCondition(badBumpCond, input))

	badFieldCond := RegoCondition{Field: "unknown_field", Operator: "=="}
	assert.False(t, matchesCondition(badFieldCond, input))

	assert.True(t, compareInt(5, "==", 5))
	assert.False(t, compareInt(1, "invalid-op", 1))

	// Unclosed rule
	unclosed, err := ParseRego("deny[msg] {\n input.breaking_changes > 0")
	require.NoError(t, err)
	assert.Len(t, unclosed.Rules, 1)
}
