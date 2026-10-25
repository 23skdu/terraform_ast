package report

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/23skdu/terraform_ast/pkg/ast"
	"github.com/23skdu/terraform_ast/pkg/blastradius"
	"github.com/23skdu/terraform_ast/pkg/diff"
	"github.com/23skdu/terraform_ast/pkg/eval"
	"github.com/23skdu/terraform_ast/pkg/policy"
	"github.com/23skdu/terraform_ast/pkg/recreation"
	"github.com/23skdu/terraform_ast/pkg/wrapper"
)

// Findings carries the optional analyses that augment a report. They are embedded into the
// machine-readable documents so that text, Markdown, JSON and SARIF output all describe the same
// set of findings instead of silently dropping the ones a given format cannot express inline.
type Findings struct {
	RecreationHazards []recreation.RecreationHazard      `json:"recreation_hazards,omitempty"`
	BlastRadius       *blastradius.BlastRadiusReport     `json:"blast_radius,omitempty"`
	Taint             *eval.TaintReport                  `json:"taint,omitempty"`
	AddressShifts     []ast.ResourceAddressShift         `json:"address_shifts,omitempty"`
	GoldenPlan        *recreation.GoldenPlanVerification `json:"golden_plan,omitempty"`
}

// RenderText formats a DiffReport into human-readable text.
func RenderText(r *diff.DiffReport, findings Findings) string {
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
	if r.SafePinVersion != "" {
		sb.WriteString(fmt.Sprintf("Safest pin:   %s (%s)\n", r.SafePinVersion, r.SafePinRationale))
	}
	sb.WriteString("----------------------------------------------------------------------\n")

	if len(r.BreakingChanges) > 0 {
		sb.WriteString(fmt.Sprintf("BREAKING CHANGES (%d):\n", len(r.BreakingChanges)))
		for _, item := range r.BreakingChanges {
			sb.WriteString(fmt.Sprintf("  • [%s] %s: %s%s\n", item.Category, item.Name, item.Description, confidenceSuffix(item.ConfidenceScore)))
			if item.Remediation != "" {
				sb.WriteString(fmt.Sprintf("    Remediation: %s\n", item.Remediation))
			}
		}
		sb.WriteString("\n")
	}

	if len(r.NewFeatures) > 0 {
		sb.WriteString(fmt.Sprintf("NEW FEATURES (%d):\n", len(r.NewFeatures)))
		for _, item := range r.NewFeatures {
			sb.WriteString(fmt.Sprintf("  • [%s] %s: %s%s\n", item.Category, item.Name, item.Description, confidenceSuffix(item.ConfidenceScore)))
		}
		sb.WriteString("\n")
	}

	if len(r.Warnings) > 0 {
		sb.WriteString(fmt.Sprintf("WARNINGS & NON-BREAKING CHANGES (%d):\n", len(r.Warnings)))
		for _, item := range r.Warnings {
			sb.WriteString(fmt.Sprintf("  • [%s] %s: %s%s\n", item.Category, item.Name, item.Description, confidenceSuffix(item.ConfidenceScore)))
			if item.Remediation != "" {
				sb.WriteString(fmt.Sprintf("    Remediation: %s\n", item.Remediation))
			}
		}
		sb.WriteString("\n")
	}
	if len(findings.RecreationHazards) > 0 {
		sb.WriteString(recreation.FormatHazardsText(findings.RecreationHazards))
	}
	if len(findings.AddressShifts) > 0 {
		sb.WriteString(fmt.Sprintf("RESOURCE ADDRESS SHIFTS (%d):\n", len(findings.AddressShifts)))
		for _, s := range findings.AddressShifts {
			sb.WriteString(fmt.Sprintf("  • %s -> %s (%s)\n", s.OldAddress.FullAddress(), s.NewAddress.FullAddress(), s.Reason))
		}
		sb.WriteString("\n")
	}
	writePolicyViolations(&sb, r.PolicyViolations)

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
	if r.SafePinRationale != "" {
		sb.WriteString(fmt.Sprintf("- **Safest Known Pin**: `%s` — %s\n\n", r.SafePinVersion, r.SafePinRationale))
	}

	if r.IsDowngrade || len(r.BreakingChanges) > 0 {
		sb.WriteString(fmt.Sprintf("> [!CAUTION]\n> **%s**\n\n", r.Summary))
	} else if len(r.Warnings) > 0 {
		sb.WriteString(fmt.Sprintf("> [!WARNING]\n> **%s**\n\n", r.Summary))
	} else {
		sb.WriteString(fmt.Sprintf("> [!NOTE]\n> **%s**\n\n", r.Summary))
	}

	if len(r.BreakingChanges) > 0 {
		sb.WriteString(fmt.Sprintf("### Breaking Changes (%d)\n\n", len(r.BreakingChanges)))
		sb.WriteString("| Category | Name | Description | Confidence | Remediation |\n")
		sb.WriteString("| :--- | :--- | :--- | :--- | :--- |\n")
		for _, item := range r.BreakingChanges {
			sb.WriteString(fmt.Sprintf("| `%s` | `%s` | %s | %s | %s |\n", item.Category, item.Name, item.Description, confidenceText(item.ConfidenceScore), item.Remediation))
		}
		sb.WriteString("\n")
	}

	if len(r.NewFeatures) > 0 {
		sb.WriteString(fmt.Sprintf("### New Features (%d)\n\n", len(r.NewFeatures)))
		sb.WriteString("| Category | Name | Description | Confidence |\n")
		sb.WriteString("| :--- | :--- | :--- | :--- |\n")
		for _, item := range r.NewFeatures {
			sb.WriteString(fmt.Sprintf("| `%s` | `%s` | %s | %s |\n", item.Category, item.Name, item.Description, confidenceText(item.ConfidenceScore)))
		}
		sb.WriteString("\n")
	}

	if len(r.Warnings) > 0 {
		sb.WriteString(fmt.Sprintf("### Warnings (%d)\n\n", len(r.Warnings)))
		sb.WriteString("| Category | Name | Description | Confidence | Remediation |\n")
		sb.WriteString("| :--- | :--- | :--- | :--- | :--- |\n")
		for _, item := range r.Warnings {
			sb.WriteString(fmt.Sprintf("| `%s` | `%s` | %s | %s | %s |\n", item.Category, item.Name, item.Description, confidenceText(item.ConfidenceScore), item.Remediation))
		}
		sb.WriteString("\n")
	}
	writePolicyViolations(&sb, r.PolicyViolations)

	return sb.String()
}

func confidenceText(score int) string {
	if score <= 0 {
		return "Not scored"
	}
	return fmt.Sprintf("%d/10", score)
}

func confidenceSuffix(score int) string {
	if score <= 0 {
		return ""
	}
	return fmt.Sprintf(" (confidence: %d/10)", score)
}

func writePolicyViolations(sb *strings.Builder, violations []policy.Violation) {
	if len(violations) == 0 {
		return
	}
	sb.WriteString(fmt.Sprintf("POLICY VIOLATIONS (%d):\n", len(violations)))
	for _, violation := range violations {
		sb.WriteString(fmt.Sprintf("  • [%s] %s\n", violation.Rule, violation.Message))
	}
	sb.WriteString("\n")
}

// RenderJSON serializes a DiffReport into JSON bytes. The optional findings are embedded at the
// top level so a report that carries extra analyses stays a single valid JSON document.
//
// Every field of the report graph is a string, number, boolean or slice of those, so encoding
// cannot fail and no error is returned.
func RenderJSON(r *diff.DiffReport, findings Findings) []byte {
	return marshalJSON(struct {
		*diff.DiffReport
		Findings
	}{DiffReport: r, Findings: findings})
}

// RenderWrapperText formats a WrapperCompatibilityReport into text.
func RenderWrapperText(w *wrapper.WrapperCompatibilityReport, findings Findings) string {
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
	sb.WriteString(fmt.Sprintf("Safest Known Pin: %s\n", w.SafePinVersion))
	sb.WriteString(fmt.Sprintf("Pin Rationale:    %s\n", w.SafePinRationale))
	sb.WriteString("----------------------------------------------------------------------\n")

	if len(w.Issues) > 0 {
		sb.WriteString(fmt.Sprintf("WRAPPER ISSUES (%d):\n", len(w.Issues)))
		for _, iss := range w.Issues {
			sb.WriteString(fmt.Sprintf("  • [%s] %s: %s%s\n", iss.Category, iss.Target, iss.Description, confidenceSuffix(iss.ConfidenceScore)))
			if iss.Remediation != "" {
				sb.WriteString(fmt.Sprintf("    Remediation: %s\n", iss.Remediation))
			}
		}
		sb.WriteString("\n")
	}

	if len(w.NewFeatureAdoptions) > 0 {
		sb.WriteString(fmt.Sprintf("UNUSED UPSTREAM FEATURES AVAILABLE (%d):\n", len(w.NewFeatureAdoptions)))
		for _, feat := range w.NewFeatureAdoptions {
			sb.WriteString(fmt.Sprintf("  • [%s] %s: %s\n", feat.Category, feat.Target, feat.Description))
		}
		sb.WriteString("\n")
	}
	if len(findings.RecreationHazards) > 0 {
		sb.WriteString(recreation.FormatHazardsText(findings.RecreationHazards))
	}
	if len(findings.AddressShifts) > 0 {
		sb.WriteString(fmt.Sprintf("RESOURCE ADDRESS SHIFTS (%d):\n", len(findings.AddressShifts)))
		for _, s := range findings.AddressShifts {
			sb.WriteString(fmt.Sprintf("  • %s -> %s (%s)\n", s.OldAddress.FullAddress(), s.NewAddress.FullAddress(), s.Reason))
		}
		sb.WriteString("\n")
	}
	if w.UpstreamDiff != nil && len(w.UpstreamDiff.BreakingChanges) > 0 {
		sb.WriteString(fmt.Sprintf("UPSTREAM BREAKING CHANGES (%d):\n", len(w.UpstreamDiff.BreakingChanges)))
		for _, item := range w.UpstreamDiff.BreakingChanges {
			sb.WriteString(fmt.Sprintf("  • [%s] %s: %s\n", item.Category, item.Name, item.Description))
			if item.Remediation != "" {
				sb.WriteString(fmt.Sprintf("    Remediation: %s\n", item.Remediation))
			}
		}
		sb.WriteString("\n")
	}
	if findings.BlastRadius != nil {
		sb.WriteString("\n")
		sb.WriteString(findings.BlastRadius.RenderText())
	}
	if findings.Taint != nil {
		sb.WriteString(fmt.Sprintf("\nVARIABLE TAINT ANALYSIS (var.%s):\n", findings.Taint.TargetVariable))
		if findings.Taint.IsDeadCode {
			sb.WriteString(fmt.Sprintf("  • Status: DEAD CODE (no downstream references in %s)\n", w.WrapperPath))
		} else {
			sb.WriteString("  • Status: REACHABLE\n")
			sb.WriteString(fmt.Sprintf("  • Trace path: %s\n", strings.Join(findings.Taint.TracePath, " -> ")))
			if len(findings.Taint.AffectedResourceAttributes) > 0 {
				sb.WriteString(fmt.Sprintf("  • Affected attributes (%d):\n", len(findings.Taint.AffectedResourceAttributes)))
				for _, attr := range findings.Taint.AffectedResourceAttributes {
					sb.WriteString(fmt.Sprintf("    - %s\n", attr))
				}
			}
		}
	}
	if findings.GoldenPlan != nil {
		sb.WriteString(recreation.FormatGoldenPlanText(findings.GoldenPlan))
	}
	writePolicyViolations(&sb, w.PolicyViolations)

	sb.WriteString("======================================================================\n")
	return sb.String()
}

// RenderWrapperMarkdown formats a WrapperCompatibilityReport into Markdown.
func RenderWrapperMarkdown(w *wrapper.WrapperCompatibilityReport, findings ...Findings) string {
	if w == nil {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("# Terraform Wrapper Compatibility Audit\n\n")
	sb.WriteString(fmt.Sprintf("- **Wrapper Path**: `%s`\n", w.WrapperPath))
	sb.WriteString(fmt.Sprintf("- **Target Module**: `%s`\n", w.TargetModuleName))
	sb.WriteString(fmt.Sprintf("- **Target Version**: `%s`\n", w.TargetVersion))
	sb.WriteString(fmt.Sprintf("- **Recommended Pin**: `%s`\n", w.RecommendedPin))
	sb.WriteString(fmt.Sprintf("- **Safest Known Pin**: `%s` — %s\n", w.SafePinVersion, w.SafePinRationale))
	sb.WriteString(fmt.Sprintf("- **Verdict**: `%s`\n\n", w.Verdict))

	if w.TotalBreaking > 0 {
		sb.WriteString(fmt.Sprintf("> [!CAUTION]\n> **%s**\n\n", w.Summary))
	} else if w.TotalWarnings > 0 {
		sb.WriteString(fmt.Sprintf("> [!WARNING]\n> **%s**\n\n", w.Summary))
	} else {
		sb.WriteString(fmt.Sprintf("> [!NOTE]\n> **%s**\n\n", w.Summary))
	}

	if len(w.Issues) > 0 {
		sb.WriteString(fmt.Sprintf("### Compatibility Issues (%d)\n\n", len(w.Issues)))
		sb.WriteString("| Category | Target | Description | Confidence | Remediation |\n")
		sb.WriteString("| :--- | :--- | :--- | :--- | :--- |\n")
		for _, iss := range w.Issues {
			sb.WriteString(fmt.Sprintf("| `%s` | `%s` | %s | %s | %s |\n", iss.Category, iss.Target, iss.Description, confidenceText(iss.ConfidenceScore), iss.Remediation))
		}
		sb.WriteString("\n")
	}

	if len(w.NewFeatureAdoptions) > 0 {
		sb.WriteString(fmt.Sprintf("### Available Upstream Features (%d)\n\n", len(w.NewFeatureAdoptions)))
		sb.WriteString("| Category | Target | Description | Confidence |\n")
		sb.WriteString("| :--- | :--- | :--- | :--- |\n")
		for _, feat := range w.NewFeatureAdoptions {
			sb.WriteString(fmt.Sprintf("| `%s` | `%s` | %s | %s |\n", feat.Category, feat.Target, feat.Description, confidenceText(feat.ConfidenceScore)))
		}
		sb.WriteString("\n")
	}
	if w.UpstreamDiff != nil && len(w.UpstreamDiff.BreakingChanges) > 0 {
		sb.WriteString(fmt.Sprintf("### Upstream Breaking Changes (%d)\n\n", len(w.UpstreamDiff.BreakingChanges)))
		sb.WriteString("| Category | Name | Description | Confidence | Remediation |\n")
		sb.WriteString("| :--- | :--- | :--- | :--- | :--- |\n")
		for _, item := range w.UpstreamDiff.BreakingChanges {
			sb.WriteString(fmt.Sprintf("| `%s` | `%s` | %s | %s | %s |\n", item.Category, item.Name, item.Description, confidenceText(item.ConfidenceScore), item.Remediation))
		}
		sb.WriteString("\n")
	}
	writePolicyViolations(&sb, w.PolicyViolations)

	if len(findings) > 0 {
		f := findings[0]
		if len(f.RecreationHazards) > 0 {
			sb.WriteString(recreation.FormatHazardsMarkdown(f.RecreationHazards))
		}
		if f.GoldenPlan != nil {
			sb.WriteString(recreation.FormatGoldenPlanMarkdown(f.GoldenPlan))
		}
	}

	return sb.String()
}

// RenderWrapperJSON serializes a WrapperCompatibilityReport into JSON bytes.
func RenderWrapperJSON(w *wrapper.WrapperCompatibilityReport, findings Findings) []byte {
	return marshalJSON(struct {
		*wrapper.WrapperCompatibilityReport
		Findings
	}{WrapperCompatibilityReport: w, Findings: findings})
}

// marshalJSON encodes v as indented JSON. The report graph is composed exclusively of JSON-safe
// types, so encoding cannot fail and no error is returned.
func marshalJSON(v any) []byte {
	encoded, _ := json.MarshalIndent(v, "", "  ")
	return encoded
}

// FormatTaintMarkdown formats a TaintReport into Markdown.
func FormatTaintMarkdown(t *eval.TaintReport) string {
	if t == nil {
		return ""
	}
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("### Variable Taint Analysis (`var.%s`)\n\n", t.TargetVariable))
	if t.IsDeadCode {
		sb.WriteString("- **Status**: Unused / dead code\n\n")
	} else {
		sb.WriteString(fmt.Sprintf("- **Status**: Reachable\n- **Trace path**: `%s`\n\n", strings.Join(t.TracePath, " -> ")))
		if len(t.AffectedResourceAttributes) > 0 {
			sb.WriteString(fmt.Sprintf("#### Affected Resource Attributes (%d)\n\n", len(t.AffectedResourceAttributes)))
			for _, attr := range t.AffectedResourceAttributes {
				sb.WriteString(fmt.Sprintf("- `%s`\n", attr))
			}
			sb.WriteString("\n")
		}
	}
	return sb.String()
}
