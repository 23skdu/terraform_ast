package planner

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/23skdu/terraform_ast/pkg/ast"
	"github.com/23skdu/terraform_ast/pkg/diff"
	"github.com/23skdu/terraform_ast/pkg/policy"
	"github.com/23skdu/terraform_ast/pkg/semverutil"
)

// VersionSnapshot is an inspected module at a semantic version.
type VersionSnapshot struct {
	Version string
	Module  *ast.ModuleInfo
}

// UpgradeStep describes the compatibility of one adjacent upgrade.
type UpgradeStep struct {
	FromVersion     string           `json:"from_version"`
	ToVersion       string           `json:"to_version"`
	CanSafelyUpdate bool             `json:"can_safely_update"`
	Diff            *diff.DiffReport `json:"diff"`
}

// UpgradePathReport aggregates adjacent comparisons and a conservative safe pin.
type UpgradePathReport struct {
	FromVersion        string             `json:"from_version"`
	TargetVersion      string             `json:"target_version"`
	RecommendedVersion string             `json:"recommended_version,omitempty"`
	Recommendation     string             `json:"recommendation"`
	CanSafelyUpdateAll bool               `json:"can_safely_update_all"`
	Steps              []UpgradeStep      `json:"steps"`
	PolicyViolations   []policy.Violation `json:"policy_violations,omitempty"`
}

// AnalyzeUpgradePath compares each adjacent version and recommends the last
// contiguous version reachable without a breaking transition.
func AnalyzeUpgradePath(snapshots []VersionSnapshot) (*UpgradePathReport, error) {
	if len(snapshots) < 2 {
		return nil, fmt.Errorf("at least two version snapshots are required")
	}
	var previous *VersionSnapshot
	for i := range snapshots {
		snapshot := &snapshots[i]
		if snapshot.Module == nil {
			return nil, fmt.Errorf("module snapshot for version %s is nil", snapshot.Version)
		}
		if !semverutil.IsValid(snapshot.Version) {
			return nil, fmt.Errorf("invalid version %q in upgrade path", snapshot.Version)
		}
		if previous != nil && semverutil.Compare(previous.Version, snapshot.Version) >= 0 {
			return nil, fmt.Errorf("upgrade path versions must be strictly ascending: %s then %s", previous.Version, snapshot.Version)
		}
		previous = snapshot
	}

	report := &UpgradePathReport{
		FromVersion:        snapshots[0].Version,
		TargetVersion:      snapshots[len(snapshots)-1].Version,
		CanSafelyUpdateAll: true,
		Steps:              make([]UpgradeStep, 0, len(snapshots)-1),
		RecommendedVersion: snapshots[0].Version,
	}
	for i := 0; i < len(snapshots)-1; i++ {
		from, to := snapshots[i], snapshots[i+1]
		comparison := diff.Compare(from.Module, to.Module, from.Version, to.Version)
		report.Steps = append(report.Steps, UpgradeStep{
			FromVersion:     from.Version,
			ToVersion:       to.Version,
			CanSafelyUpdate: comparison.CanSafelyUpdate,
			Diff:            comparison,
		})
		if comparison.CanSafelyUpdate && report.CanSafelyUpdateAll {
			report.RecommendedVersion = to.Version
		} else {
			report.CanSafelyUpdateAll = false
		}
	}
	if report.CanSafelyUpdateAll {
		report.Recommendation = fmt.Sprintf("All transitions are safe; pin %s.", report.TargetVersion)
	} else {
		report.Recommendation = fmt.Sprintf("The latest continuously safe version is %s; resolve the first breaking transition before proceeding.", report.RecommendedVersion)
	}
	return report, nil
}

// RenderJSON serializes an upgrade path report. Every field of the report graph is a JSON-safe
// primitive, slice or struct of those, so encoding cannot fail and no error is returned.
func (r *UpgradePathReport) RenderJSON() []byte {
	encoded, _ := json.MarshalIndent(r, "", "  ")
	return encoded
}

// RenderMarkdown formats an upgrade path and adjacent compatibility verdicts.
func (r *UpgradePathReport) RenderMarkdown() string {
	if r == nil {
		return ""
	}
	var out strings.Builder
	fmt.Fprintf(&out, "# Terraform Upgrade Path: %s -> %s\n\n", r.FromVersion, r.TargetVersion)
	fmt.Fprintf(&out, "- **Safe through**: `%s`\n", r.RecommendedVersion)
	fmt.Fprintf(&out, "- **All steps safe**: `%t`\n", r.CanSafelyUpdateAll)
	fmt.Fprintf(&out, "- **Recommendation**: %s\n\n", r.Recommendation)
	out.WriteString("| From | To | Verdict | Breaking | Warnings |\n")
	out.WriteString("| :--- | :--- | :--- | ---: | ---: |\n")
	for _, step := range r.Steps {
		fmt.Fprintf(&out, "| `%s` | `%s` | `%s` | %d | %d |\n",
			step.FromVersion, step.ToVersion, step.Diff.Verdict,
			step.Diff.TotalBreaking, step.Diff.TotalWarnings)
	}
	if len(r.PolicyViolations) > 0 {
		out.WriteString("\n## Policy Violations\n\n")
		for _, violation := range r.PolicyViolations {
			fmt.Fprintf(&out, "- **%s**: %s\n", violation.Rule, violation.Message)
		}
	}
	return out.String()
}

// RenderText formats the per-step verdicts for terminal output.
func (r *UpgradePathReport) RenderText() string {
	if r == nil {
		return ""
	}
	var out strings.Builder
	fmt.Fprintf(&out, "Terraform Upgrade Path: %s -> %s\n", r.FromVersion, r.TargetVersion)
	fmt.Fprintf(&out, "Safe through: %s\n", r.RecommendedVersion)
	fmt.Fprintf(&out, "All steps safe: %t\n", r.CanSafelyUpdateAll)
	fmt.Fprintf(&out, "Recommendation: %s\n\n", r.Recommendation)
	for _, step := range r.Steps {
		fmt.Fprintf(&out, "%s -> %s: %s (%d breaking, %d warnings)\n",
			step.FromVersion, step.ToVersion, step.Diff.Verdict,
			step.Diff.TotalBreaking, step.Diff.TotalWarnings)
	}
	for _, violation := range r.PolicyViolations {
		fmt.Fprintf(&out, "Policy violation (%s): %s\n", violation.Rule, violation.Message)
	}
	return out.String()
}
