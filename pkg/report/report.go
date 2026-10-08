package report

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/23skdu/terraform_ast/pkg/diff"
	"github.com/23skdu/terraform_ast/pkg/wrapper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// RenderText formats a DiffReport into human-readable text.
func RenderText(r *diff.DiffReport) string {
	if r == nil {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("======================================================================\n")
	sb.WriteString(" TERRAFORM AST COMPATIBILITY DIFF REPORT\n")
	sb.WriteString("======================================================================\n")
	sb.WriteString(fmt.Sprintf("Module:      %s\n", r.ModuleName))
	sb.WriteString(fmt.Sprintf("Transition:  %s -> %s (%s)\n", r.OldVersion, r.NewVersion, r.BumpType))
	sb.WriteString(fmt.Sprintf("Verdict:     %s\n", r.Verdict))
	sb.WriteString(fmt.Sprintf("Summary:     %s\n", r.Summary))
	sb.WriteString("----------------------------------------------------------------------\n")

	if len(r.BreakingChanges) > 0 {
		sb.WriteString(fmt.Sprintf("🚨 BREAKING CHANGES (%d):\n", len(r.BreakingChanges)))
		for _, item := range r.BreakingChanges {
			sb.WriteString(fmt.Sprintf("  • [%s] %s: %s\n", item.Category, item.Name, item.Description))
			if item.Remediation != "" {
				sb.WriteString(fmt.Sprintf("    Remediation: %s\n", item.Remediation))
			}
		}
		sb.WriteString("\n")
	}

	if len(r.NewFeatures) > 0 {
		sb.WriteString(fmt.Sprintf("✨ NEW FEATURES (%d):\n", len(r.NewFeatures)))
		for _, item := range r.NewFeatures {
			sb.WriteString(fmt.Sprintf("  • [%s] %s: %s\n", item.Category, item.Name, item.Description))
		}
		sb.WriteString("\n")
	}

	if len(r.Warnings) > 0 {
		sb.WriteString(fmt.Sprintf("⚠️  WARNINGS & NON-BREAKING CHANGES (%d):\n", len(r.Warnings)))
		for _, item := range r.Warnings {
			sb.WriteString(fmt.Sprintf("  • [%s] %s: %s\n", item.Category, item.Name, item.Description))
			if item.Remediation != "" {
				sb.WriteString(fmt.Sprintf("    Remediation: %s\n", item.Remediation))
			}
		}
		sb.WriteString("\n")
	}

	sb.WriteString("======================================================================\n")
	return sb.String()
}

// RenderMarkdown formats a DiffReport into GitHub-flavored Markdown.
func RenderMarkdown(r *diff.DiffReport) string {
	if r == nil {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("# Terraform AST Compatibility Diff Report\n\n")
	sb.WriteString(fmt.Sprintf("- **Module**: `%s`\n", r.ModuleName))
	sb.WriteString(fmt.Sprintf("- **Version Transition**: `%s` -> `%s` (`%s`)\n", r.OldVersion, r.NewVersion, r.BumpType))
	sb.WriteString(fmt.Sprintf("- **Verdict**: `%s`\n\n", r.Verdict))

	if r.IsDowngrade || len(r.BreakingChanges) > 0 {
		sb.WriteString(fmt.Sprintf("> [!CAUTION]\n> **%s**\n\n", r.Summary))
	} else if len(r.Warnings) > 0 {
		sb.WriteString(fmt.Sprintf("> [!WARNING]\n> **%s**\n\n", r.Summary))
	} else {
		sb.WriteString(fmt.Sprintf("> [!NOTE]\n> **%s**\n\n", r.Summary))
	}

	if len(r.BreakingChanges) > 0 {
		sb.WriteString(fmt.Sprintf("### 🚨 Breaking Changes (%d)\n\n", len(r.BreakingChanges)))
		sb.WriteString("| Category | Name | Description | Remediation |\n")
		sb.WriteString("| :--- | :--- | :--- | :--- |\n")
		for _, item := range r.BreakingChanges {
			sb.WriteString(fmt.Sprintf("| `%s` | `%s` | %s | %s |\n", item.Category, item.Name, item.Description, item.Remediation))
		}
		sb.WriteString("\n")
	}

	if len(r.NewFeatures) > 0 {
		sb.WriteString(fmt.Sprintf("### ✨ New Features (%d)\n\n", len(r.NewFeatures)))
		sb.WriteString("| Category | Name | Description |\n")
		sb.WriteString("| :--- | :--- | :--- |\n")
		for _, item := range r.NewFeatures {
			sb.WriteString(fmt.Sprintf("| `%s` | `%s` | %s |\n", item.Category, item.Name, item.Description))
		}
		sb.WriteString("\n")
	}

	if len(r.Warnings) > 0 {
		sb.WriteString(fmt.Sprintf("### ⚠️ Warnings (%d)\n\n", len(r.Warnings)))
		sb.WriteString("| Category | Name | Description | Remediation |\n")
		sb.WriteString("| :--- | :--- | :--- | :--- |\n")
		for _, item := range r.Warnings {
			sb.WriteString(fmt.Sprintf("| `%s` | `%s` | %s | %s |\n", item.Category, item.Name, item.Description, item.Remediation))
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

// RenderJSON serializes a DiffReport into JSON bytes.
func RenderJSON(r *diff.DiffReport) ([]byte, error) {
	return json.MarshalIndent(r, "", "  ")
}

// RenderWrapperText formats a WrapperCompatibilityReport into text.
func RenderWrapperText(w *wrapper.WrapperCompatibilityReport) string {
	if w == nil {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("======================================================================\n")
	sb.WriteString(" TERRAFORM WRAPPER COMPATIBILITY & PIN AUDIT\n")
	sb.WriteString("======================================================================\n")
	sb.WriteString(fmt.Sprintf("Wrapper Path:     %s\n", w.WrapperPath))
	sb.WriteString(fmt.Sprintf("Target Module:    %s\n", w.TargetModuleName))
	sb.WriteString(fmt.Sprintf("Target Version:   %s\n", w.TargetVersion))
	sb.WriteString(fmt.Sprintf("Recommended Pin:  %s\n", w.RecommendedPin))
	sb.WriteString(fmt.Sprintf("Verdict:          %s\n", w.Verdict))
	sb.WriteString(fmt.Sprintf("Summary:          %s\n", w.Summary))
	sb.WriteString("----------------------------------------------------------------------\n")

	if len(w.Issues) > 0 {
		sb.WriteString(fmt.Sprintf("🚨 WRAPPER ISSUES (%d):\n", len(w.Issues)))
		for _, iss := range w.Issues {
			sb.WriteString(fmt.Sprintf("  • [%s] %s: %s\n", iss.Category, iss.Target, iss.Description))
			if iss.Remediation != "" {
				sb.WriteString(fmt.Sprintf("    Remediation: %s\n", iss.Remediation))
			}
		}
		sb.WriteString("\n")
	}

	if len(w.NewFeatureAdoptions) > 0 {
		sb.WriteString(fmt.Sprintf("💡 UNUSED UPSTREAM FEATURES AVAILABLE (%d):\n", len(w.NewFeatureAdoptions)))
		for _, feat := range w.NewFeatureAdoptions {
			sb.WriteString(fmt.Sprintf("  • [%s] %s: %s\n", feat.Category, feat.Target, feat.Description))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("======================================================================\n")
	return sb.String()
}

// RenderWrapperMarkdown formats a WrapperCompatibilityReport into Markdown.
func RenderWrapperMarkdown(w *wrapper.WrapperCompatibilityReport) string {
	if w == nil {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("# Terraform Wrapper Compatibility Audit\n\n")
	sb.WriteString(fmt.Sprintf("- **Wrapper Path**: `%s`\n", w.WrapperPath))
	sb.WriteString(fmt.Sprintf("- **Target Module**: `%s`\n", w.TargetModuleName))
	sb.WriteString(fmt.Sprintf("- **Target Version**: `%s`\n", w.TargetVersion))
	sb.WriteString(fmt.Sprintf("- **Recommended Pin**: `%s`\n", w.RecommendedPin))
	sb.WriteString(fmt.Sprintf("- **Verdict**: `%s`\n\n", w.Verdict))

	if w.TotalBreaking > 0 {
		sb.WriteString(fmt.Sprintf("> [!CAUTION]\n> **%s**\n\n", w.Summary))
	} else if w.TotalWarnings > 0 {
		sb.WriteString(fmt.Sprintf("> [!WARNING]\n> **%s**\n\n", w.Summary))
	} else {
		sb.WriteString(fmt.Sprintf("> [!NOTE]\n> **%s**\n\n", w.Summary))
	}

	if len(w.Issues) > 0 {
		sb.WriteString(fmt.Sprintf("### 🚨 Compatibility Issues (%d)\n\n", len(w.Issues)))
		sb.WriteString("| Category | Target | Description | Remediation |\n")
		sb.WriteString("| :--- | :--- | :--- | :--- |\n")
		for _, iss := range w.Issues {
			sb.WriteString(fmt.Sprintf("| `%s` | `%s` | %s | %s |\n", iss.Category, iss.Target, iss.Description, iss.Remediation))
		}
		sb.WriteString("\n")
	}

	if len(w.NewFeatureAdoptions) > 0 {
		sb.WriteString(fmt.Sprintf("### 💡 Available Upstream Features (%d)\n\n", len(w.NewFeatureAdoptions)))
		sb.WriteString("| Category | Target | Description |\n")
		sb.WriteString("| :--- | :--- | :--- |\n")
		for _, feat := range w.NewFeatureAdoptions {
			sb.WriteString(fmt.Sprintf("| `%s` | `%s` | %s |\n", feat.Category, feat.Target, feat.Description))
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

// RenderWrapperJSON serializes a WrapperCompatibilityReport into JSON bytes.
func RenderWrapperJSON(w *wrapper.WrapperCompatibilityReport) ([]byte, error) {
	return json.MarshalIndent(w, "", "  ")
}

// AssertGoldenMatch verifies that actual output matches the canonical golden file at goldenPath.
// If updateGolden is true or UPDATE_GOLDEN environment variable is set to true, it writes/updates the golden file.
func AssertGoldenMatch(t *testing.T, goldenPath string, actual []byte, updateGolden bool) {
	t.Helper()
	if os.Getenv("UPDATE_GOLDEN") == "true" {
		updateGolden = true
	}

	if updateGolden {
		err := os.MkdirAll(filepath.Dir(goldenPath), 0750)
		require.NoError(t, err)
		err = os.WriteFile(filepath.Clean(goldenPath), actual, 0600)
		require.NoError(t, err)
		return
	}

	// #nosec G304 -- test golden verification path
	expected, err := os.ReadFile(filepath.Clean(goldenPath))
	require.NoError(t, err, "golden file %s missing. Run with UPDATE_GOLDEN=true to generate", goldenPath)
	assert.Equal(t, string(expected), string(actual), "golden file mismatch for %s", goldenPath)
}
