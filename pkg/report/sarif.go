package report

import (
	"fmt"

	"github.com/23skdu/terraform_ast/pkg/blastradius"
	"github.com/23skdu/terraform_ast/pkg/diff"
	"github.com/23skdu/terraform_ast/pkg/policy"
	"github.com/23skdu/terraform_ast/pkg/recreation"
	"github.com/23skdu/terraform_ast/pkg/wrapper"
	"github.com/hashicorp/terraform-config-inspect/tfconfig"
)

// SARIFLog represents the top-level SARIF v2.1.0 log document.
type SARIFLog struct {
	Schema  string     `json:"$schema"`
	Version string     `json:"version"`
	Runs    []SARIFRun `json:"runs"`
}

// SARIFRun defines a single analysis run within a SARIF log.
type SARIFRun struct {
	Tool    SARIFTool     `json:"tool"`
	Results []SARIFResult `json:"results"`
}

// SARIFTool describes the analyzer tool.
type SARIFTool struct {
	Driver SARIFDriver `json:"driver"`
}

// SARIFDriver describes the analyzer tool driver metadata and rules.
type SARIFDriver struct {
	Name           string      `json:"name"`
	Version        string      `json:"version"`
	InformationURI string      `json:"informationUri"`
	Rules          []SARIFRule `json:"rules"`
}

// SARIFRule specifies rule metadata.
type SARIFRule struct {
	ID               string           `json:"id"`
	ShortDescription SARIFMessageText `json:"shortDescription"`
	FullDescription  SARIFMessageText `json:"fullDescription"`
	DefaultConfig    SARIFRuleConfig  `json:"defaultConfiguration"`
}

// SARIFRuleConfig defines the default severity level.
type SARIFRuleConfig struct {
	Level string `json:"level"`
}

// SARIFResult represents an individual finding reported by the analyzer.
type SARIFResult struct {
	RuleID    string           `json:"ruleId"`
	Level     string           `json:"level"`
	Message   SARIFMessageText `json:"message"`
	Locations []SARIFLocation  `json:"locations,omitempty"`
}

// SARIFLocation describes the file and line where a finding applies.
type SARIFLocation struct {
	PhysicalLocation SARIFPhysicalLocation `json:"physicalLocation"`
}

// SARIFPhysicalLocation describes physical file coordinates.
type SARIFPhysicalLocation struct {
	ArtifactLocation SARIFArtifactLocation `json:"artifactLocation"`
	Region           SARIFRegion           `json:"region"`
}

// SARIFArtifactLocation holds the file URI.
type SARIFArtifactLocation struct {
	URI string `json:"uri"`
}

// SARIFRegion points to the line number.
type SARIFRegion struct {
	StartLine int `json:"startLine"`
}

// SARIFMessageText wraps a text message.
type SARIFMessageText struct {
	Text string `json:"text"`
}

const (
	sarifSchema = "https://raw.githubusercontent.com/oasis-tcs/sarif-spec/master/Schemata/sarif-schema-2.1.0.json"
	sarifVer    = "2.1.0"
	toolName    = "terraform-ast-diff"
	toolVer     = "0.9.0"
	toolURI     = "https://github.com/23skdu/terraform_ast"

	ruleBreaking             = "TF-DIFF-BREAKING-CHANGE"
	ruleWarning              = "TF-DIFF-WARNING"
	ruleNewFeature           = "TF-DIFF-NEW-FEATURE"
	rulePolicy               = "TF-POLICY-VIOLATION"
	ruleRecreationHazard     = "TF-RECREATION-HAZARD"
	ruleBlastRadius          = "TF-BLAST-RADIUS-CONSUMER"
	rulePlanRecreationHazard = "TF-GOLDEN-PLAN-RECREATION-HAZARD"
	ruleUpstreamBreaking     = "TF-UPSTREAM-BREAKING-CHANGE"
	ruleAddressShift         = "TF-ADDRESS-SHIFT"
	ruleTaintReachable       = "TF-TAINT-REACHABLE"
)

// defaultRules is the SARIF rule catalogue. Each document only advertises the subset of rules for
// which it actually emits results, which SARIF consumers use to decide which findings are actionable.
var defaultRules = []SARIFRule{
	{
		ID:               ruleBreaking,
		ShortDescription: SARIFMessageText{Text: "Breaking API change in Terraform module"},
		FullDescription:  SARIFMessageText{Text: "A breaking API change was detected between module versions (e.g. removed input, newly required input, or incompatible type change)."},
		DefaultConfig:    SARIFRuleConfig{Level: "error"},
	},
	{
		ID:               ruleWarning,
		ShortDescription: SARIFMessageText{Text: "Warning or widening change in Terraform module"},
		FullDescription:  SARIFMessageText{Text: "A non-breaking change or type widening that may require verification."},
		DefaultConfig:    SARIFRuleConfig{Level: "warning"},
	},
	{
		ID:               ruleNewFeature,
		ShortDescription: SARIFMessageText{Text: "New feature or optional attribute added"},
		FullDescription:  SARIFMessageText{Text: "A newly introduced optional variable or output is available."},
		DefaultConfig:    SARIFRuleConfig{Level: "note"},
	},
	{
		ID:               rulePolicy,
		ShortDescription: SARIFMessageText{Text: "Configured Terraform upgrade policy was violated"},
		FullDescription:  SARIFMessageText{Text: "The analysis result violates a configured policy gate."},
		DefaultConfig:    SARIFRuleConfig{Level: "error"},
	},
	{
		ID:               ruleRecreationHazard,
		ShortDescription: SARIFMessageText{Text: "State destruction and recreation hazard (ForceNew)"},
		FullDescription:  SARIFMessageText{Text: "A modification touches an immutable cloud resource attribute that forces Terraform to destroy and re-create the resource."},
		DefaultConfig:    SARIFRuleConfig{Level: "error"},
	},
	{
		ID:               ruleBlastRadius,
		ShortDescription: SARIFMessageText{Text: "Downstream consumer module cannot be safely updated"},
		FullDescription:  SARIFMessageText{Text: "A root module calling the audited wrapper passes removed inputs, misses newly required inputs, or consumes removed outputs."},
		DefaultConfig:    SARIFRuleConfig{Level: "error"},
	},
	{
		ID:               rulePlanRecreationHazard,
		ShortDescription: SARIFMessageText{Text: "Terraform plan recreation hazard detected"},
		FullDescription:  SARIFMessageText{Text: "The execution plan contains resources subject to ForceNew destruction/recreation or unmigrated address shifts upon version upgrade."},
		DefaultConfig:    SARIFRuleConfig{Level: "error"},
	},
	{
		ID:               ruleUpstreamBreaking,
		ShortDescription: SARIFMessageText{Text: "Upstream breaking change detected"},
		FullDescription:  SARIFMessageText{Text: "The upstream module upgrade introduces a breaking change that the wrapper must accommodate."},
		DefaultConfig:    SARIFRuleConfig{Level: "error"},
	},
	{
		ID:               ruleAddressShift,
		ShortDescription: SARIFMessageText{Text: "Resource address shift detected"},
		FullDescription:  SARIFMessageText{Text: "A resource address shifted between module versions without a state migration, triggering recreation."},
		DefaultConfig:    SARIFRuleConfig{Level: "error"},
	},
	{
		ID:               ruleTaintReachable,
		ShortDescription: SARIFMessageText{Text: "Tainted variable reaches a resource attribute"},
		FullDescription:  SARIFMessageText{Text: "A changed variable propagates to a resource attribute via the taint analysis."},
		DefaultConfig:    SARIFRuleConfig{Level: "warning"},
	},
}

func sarifLocationWithLine(artifact string, line int) []SARIFLocation {
	if line <= 0 {
		line = 1
	}
	return []SARIFLocation{{
		PhysicalLocation: SARIFPhysicalLocation{
			ArtifactLocation: SARIFArtifactLocation{URI: artifactOrDefault(artifact)},
			Region:           SARIFRegion{StartLine: line},
		},
	}}
}

func sarifLocation(artifact string) []SARIFLocation {
	return sarifLocationWithLine(artifact, 1)
}

func artifactOrDefault(artifact string) string {
	if artifact == "" {
		return "main.tf"
	}
	return artifact
}

// renderSARIF builds the SARIF document, advertising exactly the rules that back an emitted
// result so that code-scanning consumers only see actionable rule metadata.
func renderSARIF(results []SARIFResult) []byte {
	emitted := make(map[string]struct{}, len(results))
	for _, result := range results {
		emitted[result.RuleID] = struct{}{}
	}
	rules := make([]SARIFRule, 0, len(emitted))
	for _, rule := range defaultRules {
		if _, ok := emitted[rule.ID]; ok {
			rules = append(rules, rule)
		}
	}

	return marshalJSON(SARIFLog{
		Schema:  sarifSchema,
		Version: sarifVer,
		Runs: []SARIFRun{
			{
				Tool: SARIFTool{
					Driver: SARIFDriver{
						Name:           toolName,
						Version:        toolVer,
						InformationURI: toolURI,
						Rules:          rules,
					},
				},
				Results: results,
			},
		},
	})
}

// emptySARIF renders a well-formed document with no runs, used when there is nothing to report.
func emptySARIF() []byte {
	return marshalJSON(SARIFLog{
		Schema:  sarifSchema,
		Version: sarifVer,
		Runs:    []SARIFRun{},
	})
}

// RenderSARIF formats a DiffReport into standard OASIS SARIF v2.1.0 JSON.
func RenderSARIF(r *diff.DiffReport, findings Findings) []byte {
	if r == nil {
		return emptySARIF()
	}

	artifact := artifactOrDefault(r.ModuleName)

	results := make([]SARIFResult, 0, len(r.BreakingChanges)+len(r.Warnings)+len(r.NewFeatures)+len(findings.RecreationHazards)+len(findings.AddressShifts))
	lineOf := func(p tfconfig.SourcePos) int {
		if p.Line > 0 {
			return p.Line
		}
		return 1
	}
	for _, item := range r.BreakingChanges {
		results = append(results, sarifResultWithLine(ruleBreaking, "error", artifact, lineOf(item.Pos),
			fmt.Sprintf("[%s] %s: %s (Confidence: %s; Remediation: %s)", item.Category, item.Name, item.Description, confidenceText(item.ConfidenceScore), item.Remediation)))
	}
	for _, item := range r.Warnings {
		results = append(results, sarifResultWithLine(ruleWarning, "warning", artifact, lineOf(item.Pos),
			fmt.Sprintf("[%s] %s: %s (Confidence: %s; Remediation: %s)", item.Category, item.Name, item.Description, confidenceText(item.ConfidenceScore), item.Remediation)))
	}
	for _, item := range r.NewFeatures {
		results = append(results, sarifResultWithLine(ruleNewFeature, "note", artifact, lineOf(item.Pos),
			fmt.Sprintf("[%s] %s: %s (Confidence: %s)", item.Category, item.Name, item.Description, confidenceText(item.ConfidenceScore))))
	}
	for _, s := range findings.AddressShifts {
		results = append(results, sarifResult(ruleAddressShift, "error", artifact,
			fmt.Sprintf("%s -> %s (%s)", s.OldAddress.FullAddress(), s.NewAddress.FullAddress(), s.Reason)))
	}
	if findings.Taint != nil && !findings.Taint.IsDeadCode {
		for _, attr := range findings.Taint.AffectedResourceAttributes {
			results = append(results, sarifResult(ruleTaintReachable, "warning", artifact,
				fmt.Sprintf("var.%s reaches %s", findings.Taint.TargetVariable, attr)))
		}
	}
	results = appendHazardResults(results, artifact, findings.RecreationHazards)
	results = appendBlastResults(results, findings.BlastRadius)
	results = appendPolicyResults(results, r.PolicyViolations, artifact)
	results = appendGoldenPlanResults(results, findings.GoldenPlan)

	return renderSARIF(results)
}

// RenderWrapperSARIF formats a WrapperCompatibilityReport into standard SARIF v2.1.0 JSON.
func RenderWrapperSARIF(res *wrapper.WrapperCompatibilityReport, findings Findings) []byte {
	if res == nil {
		return emptySARIF()
	}

	artifact := artifactOrDefault(res.WrapperPath)

	results := make([]SARIFResult, 0, len(res.Issues)+len(findings.RecreationHazards))
	for _, item := range res.Issues {
		level := "error"
		ruleID := ruleBreaking
		if item.Severity == diff.SeverityWarning {
			level = "warning"
			ruleID = ruleWarning
		}
		line := 1
		if item.Pos.Line > 0 {
			line = item.Pos.Line
		}
		results = append(results, sarifResultWithLine(ruleID, level, artifact, line,
			fmt.Sprintf("[%s] %s: %s (Confidence: %s). %s", item.Category, item.Target, item.Description, confidenceText(item.ConfidenceScore), item.Remediation)))
	}
	results = appendHazardResults(results, artifact, findings.RecreationHazards)
	results = appendBlastResults(results, findings.BlastRadius)
	results = appendPolicyResults(results, res.PolicyViolations, artifact)
	results = appendGoldenPlanResults(results, findings.GoldenPlan)
	if res.UpstreamDiff != nil {
		for _, item := range res.UpstreamDiff.BreakingChanges {
			results = append(results, sarifResult(ruleUpstreamBreaking, "error", artifact,
				fmt.Sprintf("[UPSTREAM] [%s] %s: %s (Confidence: %s). %s", item.Category, item.Name, item.Description, confidenceText(item.ConfidenceScore), item.Remediation)))
		}
	}

	return renderSARIF(results)
}

func sarifResult(ruleID, level, artifact, message string) SARIFResult {
	return sarifResultWithLine(ruleID, level, artifact, 1, message)
}

func sarifResultWithLine(ruleID, level, artifact string, line int, message string) SARIFResult {
	return SARIFResult{
		RuleID:    ruleID,
		Level:     level,
		Message:   SARIFMessageText{Text: message},
		Locations: sarifLocationWithLine(artifact, line),
	}
}

func appendHazardResults(results []SARIFResult, artifact string, hazards []recreation.RecreationHazard) []SARIFResult {
	for _, hazard := range hazards {
		results = append(results, sarifResult(ruleRecreationHazard, "error", artifact,
			fmt.Sprintf("[%s] %s: %s", hazard.HazardType, hazard.Variable, hazard.Description)))
	}
	return results
}

func appendBlastResults(results []SARIFResult, blast *blastradius.BlastRadiusReport) []SARIFResult {
	if blast == nil {
		return results
	}
	for _, impact := range blast.ImpactedRootModules {
		if impact.CanSafelyUpdate && len(impact.RemovedInputsPassed) == 0 &&
			len(impact.MissingRequiredInputs) == 0 && len(impact.RemovedOutputsReferenced) == 0 &&
			len(impact.RecreationHazards) == 0 && !impact.AnalysisIncomplete {
			continue
		}
		level := "error"
		if impact.AnalysisIncomplete && impact.CanSafelyUpdate {
			level = "warning"
		}
		msg := fmt.Sprintf("[%s] downstream consumer %s is impacted (risk %s): removed inputs %v, missing required inputs %v, removed outputs %v, recreation hazards %v",
			impact.ModuleName, impact.Dir, impact.RiskScore,
			impact.RemovedInputsPassed, impact.MissingRequiredInputs,
			impact.RemovedOutputsReferenced, impact.RecreationHazards)
		if impact.AnalysisIncomplete {
			msg += " (analysis incomplete: some dynamic inputs could not be statically evaluated)"
		}
		results = append(results, sarifResult(ruleBlastRadius, level, artifactOrDefault(impact.Dir), msg))
	}
	return results
}

func appendPolicyResults(results []SARIFResult, violations []policy.Violation, artifact string) []SARIFResult {
	artifact = artifactOrDefault(artifact)
	for _, violation := range violations {
		results = append(results, sarifResult(rulePolicy, "error", artifact,
			fmt.Sprintf("[%s] %s", violation.Rule, violation.Message)))
	}
	return results
}

func appendGoldenPlanResults(results []SARIFResult, v *recreation.GoldenPlanVerification) []SARIFResult {
	if v == nil {
		return results
	}
	artifact := artifactOrDefault(v.PlanPath)
	for _, h := range v.ConfirmedHazards {
		results = append(results, sarifResult(rulePlanRecreationHazard, "error", artifact,
			fmt.Sprintf("[%s] %s (%s): %s", h.Severity, h.ResourceAddress, h.HazardType, h.Detail)))
	}
	return results
}
