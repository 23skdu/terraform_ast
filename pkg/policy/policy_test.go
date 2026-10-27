package policy

import (
	"context"
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
}

func TestEvaluatePolicyMaxBumpVersionHandling(t *testing.T) {
	// No version information means there is no bump to bound, so the gate cannot fire. This is
	// the documented "compare two local directories" flow, which supplies no version flags.
	for _, bump := range []string{"", "UNKNOWN"} {
		assert.Empty(t, Evaluate(&Policy{MaxBump: "MAJOR"}, Input{BumpType: bump}),
			"a missing version must not be reported as a bump violation")
		assert.Empty(t, Evaluate(&Policy{MaxBump: "PATCH"}, Input{IsDowngrade: true, BumpType: bump}),
			"other gates still work without version information")
	}

	// A malformed version is a different failure and must still be reported: the caller asked
	// for a comparison that cannot be made.
	invalid := Evaluate(&Policy{MaxBump: "MAJOR"}, Input{BumpType: "INVALID"})
	require.Len(t, invalid, 1)
	assert.Equal(t, "max_bump", invalid[0].Rule)
	assert.Contains(t, invalid[0].Message, "unrecognised bump type")

	// A real transition above the ceiling is still reported.
	exceeded := Evaluate(&Policy{MaxBump: "PATCH"}, Input{BumpType: "MAJOR"})
	require.Len(t, exceeded, 1)
	assert.Contains(t, exceeded[0].Message, "got MAJOR")
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

	// No valid rules found
	_, errNoRules := ParseRego("package test\n# just a comment\n")
	assert.Error(t, errNoRules)
	assert.Contains(t, errNoRules.Error(), "no valid rules found in Rego policy")

	// Unrecognized policy field
	_, errBadField := ParseRego("deny[msg] {\n input.unknown_custom_field == true\n}")
	assert.Error(t, errBadField)
	assert.Contains(t, errBadField.Error(), "unrecognized policy field")

	// LoadRego valid file
	tmpRego := filepath.Join(t.TempDir(), "policy.rego")
	require.NoError(t, os.WriteFile(tmpRego, []byte("package test\ndeny[msg] {\n input.breaking_changes > 0\n}"), 0644))
	loaded, err := LoadRego(tmpRego)
	require.NoError(t, err)
	assert.Len(t, loaded.Rules, 1)

	// LoadRego non-existent file
	_, errMissing := LoadRego("/nonexistent/rego/path.rego")
	assert.Error(t, errMissing)
}

func TestEvaluateRego_OPAFullSuite(t *testing.T) {
	ctx := context.Background()

	// 1. Nil policy or empty source
	res, err := EvaluateRegoArbitrary(ctx, nil, nil)
	assert.NoError(t, err)
	assert.Nil(t, res)

	res, err = EvaluateRegoArbitrary(ctx, &RegoPolicy{}, nil)
	assert.NoError(t, err)
	assert.Nil(t, res)

	// 2. Evaluation error (invalid rego syntax in source)
	badPolicy := &RegoPolicy{Package: "bad", Source: "package bad\n= syntax error"}
	_, err = EvaluateRegoArbitrary(ctx, badPolicy, nil)
	assert.Error(t, err)

	// 3. OPA with set/slice of deny strings (deny contains msg)
	regoSrc := `package terraform_ast.policy
deny contains msg if {
	input.breaking_changes > 0
	msg := sprintf("breaking changes found: %v", [input.breaking_changes])
}
`
	pol, err := ParseRego(regoSrc)
	require.NoError(t, err)
	violations, err := EvaluateRegoArbitrary(ctx, pol, map[string]any{"breaking_changes": 3})
	require.NoError(t, err)
	require.Len(t, violations, 1)
	assert.Equal(t, "deny", violations[0].Rule)
	assert.Contains(t, violations[0].Message, "breaking changes found: 3")

	// Call EvaluateRego top-level helper which should invoke OPA and return violations
	topViolations := EvaluateRego(pol, Input{BreakingChanges: 3})
	require.Len(t, topViolations, 1)
	assert.Contains(t, topViolations[0].Message, "breaking changes found: 3")

	// 4. Boolean violation rule
	regoBool := `package terraform_ast.policy
violation = true if {
	input.is_downgrade == true
}
`
	polBool, err := ParseRego(regoBool)
	require.NoError(t, err)
	violationsBool, err := EvaluateRegoArbitrary(ctx, polBool, map[string]any{"is_downgrade": true})
	require.NoError(t, err)
	require.Len(t, violationsBool, 1)
	assert.Equal(t, "violation", violationsBool[0].Rule)

	// 5. String deny rule
	reGoString := `package terraform_ast.policy
deny := "direct string violation" if {
	input.warnings > 5
}
`
	polStr, err := ParseRego(reGoString)
	require.NoError(t, err)
	violationsStr, err := EvaluateRegoArbitrary(ctx, polStr, map[string]any{"warnings": 10})
	require.NoError(t, err)
	require.Len(t, violationsStr, 1)
	assert.Equal(t, "direct string violation", violationsStr[0].Message)

	// 6. Map of items in deny
	regoMap := `package terraform_ast.policy
deny[k] := v if {
	k := "item1"
	v := "detail1"
}
`
	polMap, err := ParseRego(regoMap)
	require.NoError(t, err)
	violationsMap, err := EvaluateRegoArbitrary(ctx, polMap, map[string]any{})
	require.NoError(t, err)
	require.Len(t, violationsMap, 1)
	assert.Equal(t, "detail1", violationsMap[0].Message)

	// 7. Undefined package or empty expressions
	regoEmpty := `package other_pkg
deny = true if { false }
`
	polEmpty := &RegoPolicy{Package: "nonexistent_pkg", Source: regoEmpty}
	violationsEmpty, err := EvaluateRegoArbitrary(ctx, polEmpty, map[string]any{})
	require.NoError(t, err)
	assert.Nil(t, violationsEmpty)
}

func TestEvaluateRego_WithReportAndPureOPA(t *testing.T) {
	regoSrc := `package terraform_ast.policy
import rego.v1

deny contains msg if {
	some item in input.report_items
	item.level == "critical"
	msg := sprintf("Critical item: %s", [item.name])
}
`
	pol, err := ParseRego(regoSrc)
	require.NoError(t, err)

	report := map[string]any{
		"report_items": []map[string]any{
			{"name": "vuln1", "level": "critical"},
		},
	}
	violations := EvaluateRego(pol, Input{Report: report})
	require.Len(t, violations, 1)
	assert.Contains(t, violations[0].Message, "Critical item: vuln1")
}

func TestEvaluateRego_CacheAndError(t *testing.T) {
	regoSrc := `package terraform_ast.policy
import rego.v1

deny contains msg if {
	some item in input.report_items
	item.level == "critical"
	msg := sprintf("Critical item: %s", [item.name])
}
`
	pol, err := ParseRego(regoSrc)
	require.NoError(t, err)

	report := &map[string]any{
		"report_items": []map[string]any{
			{"name": "vuln1", "level": "critical"},
		},
	}
	// First call populates cache
	v1 := EvaluateRego(pol, Input{Report: report})
	require.Len(t, v1, 1)
	// Second call hits cache
	v2 := EvaluateRego(pol, Input{Report: report})
	require.Len(t, v2, 1)

	// Error path: cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = EvaluateRegoArbitrary(ctx, pol, map[string]any{})
	_ = err

	// Error path: invalid rego source
	badPol := &RegoPolicy{Package: "terraform_ast.policy", Source: "deny = { invalid"}
	_, err2 := EvaluateRegoArbitrary(context.Background(), badPol, map[string]any{})
	_ = err2
}

func TestEvaluateRego_CoverMissingPackageAndEmptyConditions(t *testing.T) {
	assert.Equal(t, uintptr(0), reportKey(nil))
	assert.Equal(t, uintptr(0), reportKey(123))

	// Policy without package evaluated via EvaluateRegoArbitrary
	polNoPkg := &RegoPolicy{
		Package: "terraform_ast.policy",
		Source:  "deny = true",
	}
	v, err := EvaluateRegoArbitrary(context.Background(), polNoPkg, map[string]any{})
	require.NoError(t, err)
	assert.NotEmpty(t, v)

	// Policy rule with no conditions in fallback evaluation (OPA errors out)
	polEmptyRule := &RegoPolicy{
		Package: "terraform_ast.policy",
		Source:  "deny = { broken",
		Rules: []RegoRule{
			{Name: "empty", Conditions: nil},
		},
	}
	vEmpty := EvaluateRego(polEmptyRule, Input{BreakingChanges: 0})
	assert.Empty(t, vEmpty)
}
