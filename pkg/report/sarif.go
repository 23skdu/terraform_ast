package report

import (
	"encoding/json"
	"fmt"

	"github.com/23skdu/terraform_ast/pkg/diff"
	"github.com/23skdu/terraform_ast/pkg/policy"
	"github.com/23skdu/terraform_ast/pkg/wrapper"
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
	toolVer     = "0.1.0"
	toolURI     = "https://github.com/23skdu/terraform_ast"
)

var defaultRules = []SARIFRule{
	{
		ID:               "TF-DIFF-BREAKING-CHANGE",
		ShortDescription: SARIFMessageText{Text: "Breaking API change in Terraform module"},
		FullDescription:  SARIFMessageText{Text: "A breaking API change was detected between module versions (e.g. removed input, newly required input, or incompatible type change)."},
		DefaultConfig:    SARIFRuleConfig{Level: "error"},
	},
	{
		ID:               "TF-DIFF-WARNING",
		ShortDescription: SARIFMessageText{Text: "Warning or widening change in Terraform module"},
		FullDescription:  SARIFMessageText{Text: "A non-breaking change or type widening that may require verification."},
		DefaultConfig:    SARIFRuleConfig{Level: "warning"},
	},
	{
		ID:               "TF-DIFF-NEW-FEATURE",
		ShortDescription: SARIFMessageText{Text: "New feature or optional attribute added"},
		FullDescription:  SARIFMessageText{Text: "A newly introduced optional variable or output is available."},
		DefaultConfig:    SARIFRuleConfig{Level: "note"},
	},
	{
		ID:               "TF-POLICY-VIOLATION",
		ShortDescription: SARIFMessageText{Text: "Configured Terraform upgrade policy was violated"},
		FullDescription:  SARIFMessageText{Text: "The analysis result violates a configured policy gate."},
		DefaultConfig:    SARIFRuleConfig{Level: "error"},
	},
}

// RenderSARIF formats a DiffReport into standard OASIS SARIF v2.1.0 JSON.
func RenderSARIF(r *diff.DiffReport) ([]byte, error) {
	if r == nil {
		return json.MarshalIndent(SARIFLog{
			Schema:  sarifSchema,
			Version: sarifVer,
			Runs:    []SARIFRun{},
		}, "", "  ")
	}

	var results []SARIFResult

	for _, item := range r.BreakingChanges {
		results = append(results, SARIFResult{
			RuleID: "TF-DIFF-BREAKING-CHANGE",
			Level:  "error",
			Message: SARIFMessageText{
				Text: fmt.Sprintf("[%s] %s: %s (Confidence: %s; Remediation: %s)", item.Category, item.Name, item.Description, confidenceText(item.ConfidenceScore), item.Remediation),
			},
			Locations: []SARIFLocation{
				{
					PhysicalLocation: SARIFPhysicalLocation{
						ArtifactLocation: SARIFArtifactLocation{URI: r.ModuleName},
						Region:           SARIFRegion{StartLine: 1},
					},
				},
			},
		})
	}

	for _, item := range r.Warnings {
		results = append(results, SARIFResult{
			RuleID: "TF-DIFF-WARNING",
			Level:  "warning",
			Message: SARIFMessageText{
				Text: fmt.Sprintf("[%s] %s: %s (Confidence: %s; Remediation: %s)", item.Category, item.Name, item.Description, confidenceText(item.ConfidenceScore), item.Remediation),
			},
			Locations: []SARIFLocation{
				{
					PhysicalLocation: SARIFPhysicalLocation{
						ArtifactLocation: SARIFArtifactLocation{URI: r.ModuleName},
						Region:           SARIFRegion{StartLine: 1},
					},
				},
			},
		})
	}

	for _, item := range r.NewFeatures {
		results = append(results, SARIFResult{
			RuleID: "TF-DIFF-NEW-FEATURE",
			Level:  "note",
			Message: SARIFMessageText{
				Text: fmt.Sprintf("[%s] %s: %s (Confidence: %s)", item.Category, item.Name, item.Description, confidenceText(item.ConfidenceScore)),
			},
			Locations: []SARIFLocation{
				{
					PhysicalLocation: SARIFPhysicalLocation{
						ArtifactLocation: SARIFArtifactLocation{URI: r.ModuleName},
						Region:           SARIFRegion{StartLine: 1},
					},
				},
			},
		})
	}
	results = appendPolicyResults(results, r.PolicyViolations, r.ModuleName)

	log := SARIFLog{
		Schema:  sarifSchema,
		Version: sarifVer,
		Runs: []SARIFRun{
			{
				Tool: SARIFTool{
					Driver: SARIFDriver{
						Name:           toolName,
						Version:        toolVer,
						InformationURI: toolURI,
						Rules:          defaultRules,
					},
				},
				Results: results,
			},
		},
	}

	return json.MarshalIndent(log, "", "  ")
}

// RenderWrapperSARIF formats a WrapperCompatibilityReport into standard SARIF v2.1.0 JSON.
func RenderWrapperSARIF(res *wrapper.WrapperCompatibilityReport) ([]byte, error) {
	if res == nil {
		return json.MarshalIndent(SARIFLog{
			Schema:  sarifSchema,
			Version: sarifVer,
			Runs:    []SARIFRun{},
		}, "", "  ")
	}

	var results []SARIFResult

	for _, item := range res.Issues {
		level := "error"
		ruleID := "TF-DIFF-BREAKING-CHANGE"
		if item.Severity == diff.SeverityWarning {
			level = "warning"
			ruleID = "TF-DIFF-WARNING"
		}
		targetFile := res.WrapperPath
		if targetFile == "" {
			targetFile = "main.tf"
		}

		results = append(results, SARIFResult{
			RuleID: ruleID,
			Level:  level,
			Message: SARIFMessageText{
				Text: fmt.Sprintf("[%s] %s: %s (Confidence: %s). %s", item.Category, item.Target, item.Description, confidenceText(item.ConfidenceScore), item.Remediation),
			},
			Locations: []SARIFLocation{
				{
					PhysicalLocation: SARIFPhysicalLocation{
						ArtifactLocation: SARIFArtifactLocation{URI: targetFile},
						Region:           SARIFRegion{StartLine: 1},
					},
				},
			},
		})
	}
	results = appendPolicyResults(results, res.PolicyViolations, res.WrapperPath)

	log := SARIFLog{
		Schema:  sarifSchema,
		Version: sarifVer,
		Runs: []SARIFRun{
			{
				Tool: SARIFTool{
					Driver: SARIFDriver{
						Name:           toolName,
						Version:        toolVer,
						InformationURI: toolURI,
						Rules:          defaultRules,
					},
				},
				Results: results,
			},
		},
	}

	return json.MarshalIndent(log, "", "  ")
}

func appendPolicyResults(results []SARIFResult, violations []policy.Violation, artifact string) []SARIFResult {
	if artifact == "" {
		artifact = "main.tf"
	}
	for _, violation := range violations {
		results = append(results, SARIFResult{
			RuleID: "TF-POLICY-VIOLATION",
			Level:  "error",
			Message: SARIFMessageText{
				Text: fmt.Sprintf("[%s] %s", violation.Rule, violation.Message),
			},
			Locations: []SARIFLocation{{
				PhysicalLocation: SARIFPhysicalLocation{
					ArtifactLocation: SARIFArtifactLocation{URI: artifact},
					Region:           SARIFRegion{StartLine: 1},
				},
			}},
		})
	}
	return results
}
