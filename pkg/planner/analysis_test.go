package planner

import (
	"encoding/json"
	"testing"

	"github.com/23skdu/terraform_ast/pkg/ast"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAnalyzeUpgradePathRecommendsLatestContiguousSafeVersion(t *testing.T) {
	v1 := &ast.ModuleInfo{
		Variables: map[string]ast.VariableDef{
			"name": {Name: "name", Type: "string", HasDefault: true},
		},
	}
	v2 := &ast.ModuleInfo{
		Variables: map[string]ast.VariableDef{
			"name":   {Name: "name", Type: "string", HasDefault: true},
			"region": {Name: "region", Type: "string", HasDefault: true},
		},
	}
	v3 := &ast.ModuleInfo{
		Variables: map[string]ast.VariableDef{
			"name": {Name: "name", Type: "number", HasDefault: true},
		},
	}

	report, err := AnalyzeUpgradePath([]VersionSnapshot{
		{Version: "v1.0.0", Module: v1},
		{Version: "v1.1.0", Module: v2},
		{Version: "v2.0.0", Module: v3},
	})
	require.NoError(t, err)
	require.Len(t, report.Steps, 2)
	assert.True(t, report.Steps[0].CanSafelyUpdate)
	assert.False(t, report.Steps[1].CanSafelyUpdate)
	assert.Equal(t, "v1.1.0", report.RecommendedVersion)
	assert.False(t, report.CanSafelyUpdateAll)
	assert.Contains(t, report.Recommendation, "v1.1.0")
}

func TestUpgradePathRenderers(t *testing.T) {
	report, err := AnalyzeUpgradePath([]VersionSnapshot{
		{Version: "v1.0.0", Module: &ast.ModuleInfo{}},
		{Version: "v1.1.0", Module: &ast.ModuleInfo{}},
	})
	require.NoError(t, err)
	assert.Contains(t, report.RenderText(), "v1.0.0 -> v1.1.0")
	assert.Contains(t, report.RenderMarkdown(), "| `v1.0.0` | `v1.1.0` |")
	data, err := report.RenderJSON()
	require.NoError(t, err)
	var decoded UpgradePathReport
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, report.TargetVersion, decoded.TargetVersion)
}

func TestAnalyzeUpgradePathValidation(t *testing.T) {
	_, err := AnalyzeUpgradePath(nil)
	assert.ErrorContains(t, err, "at least two")

	_, err = AnalyzeUpgradePath([]VersionSnapshot{
		{Version: "v1.0.0", Module: &ast.ModuleInfo{}},
		{Version: "v1.0.0", Module: &ast.ModuleInfo{}},
	})
	assert.ErrorContains(t, err, "strictly ascending")

	_, err = AnalyzeUpgradePath([]VersionSnapshot{
		{Version: "not-semver", Module: &ast.ModuleInfo{}},
		{Version: "v2.0.0", Module: &ast.ModuleInfo{}},
	})
	assert.ErrorContains(t, err, "invalid version")
}
