package diff

import (
	"github.com/23skdu/terraform_ast/pkg/policy"
	"github.com/23skdu/terraform_ast/pkg/semverutil"
)

// ChangeSeverity represents the impact of a detected change.
type ChangeSeverity string

const (
	SeverityBreaking   ChangeSeverity = "BREAKING"
	SeverityWarning    ChangeSeverity = "WARNING"
	SeverityNewFeature ChangeSeverity = "NEW_FEATURE"
)

// DifferenceItem represents a single identified difference between module versions.
type DifferenceItem struct {
	Category        string         `json:"category"`
	Name            string         `json:"name"`
	Severity        ChangeSeverity `json:"severity"`
	ConfidenceScore int            `json:"confidence_score"`
	Description     string         `json:"description"`
	OldValue        string         `json:"old_value,omitempty"`
	NewValue        string         `json:"new_value,omitempty"`
	Remediation     string         `json:"remediation,omitempty"`
}

// DiffReport contains the full comparison between two Terraform module versions.
type DiffReport struct {
	ModuleName       string              `json:"module_name"`
	OldVersion       string              `json:"old_version"`
	NewVersion       string              `json:"new_version"`
	BumpType         semverutil.BumpType `json:"bump_type"`
	IsDowngrade      bool                `json:"is_downgrade"`
	CanSafelyUpdate  bool                `json:"can_safely_update"`
	Verdict          string              `json:"verdict"`
	SafePinVersion   string              `json:"safe_pin_version,omitempty"`
	SafePinRationale string              `json:"safe_pin_rationale,omitempty"`
	BreakingChanges  []DifferenceItem    `json:"breaking_changes"`
	NewFeatures      []DifferenceItem    `json:"new_features"`
	Warnings         []DifferenceItem    `json:"warnings"`
	PolicyViolations []policy.Violation  `json:"policy_violations,omitempty"`
	TotalBreaking    int                 `json:"total_breaking"`
	TotalNewFeatures int                 `json:"total_new_features"`
	TotalWarnings    int                 `json:"total_warnings"`
	Summary          string              `json:"summary"`
}
