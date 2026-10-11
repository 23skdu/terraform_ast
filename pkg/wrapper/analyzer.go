package wrapper

import (
	"fmt"
	"sort"
	"strings"

	"github.com/23skdu/terraform_ast/pkg/ast"
	"github.com/23skdu/terraform_ast/pkg/diff"
	"github.com/23skdu/terraform_ast/pkg/semverutil"
)

// WrapperIssue represents a specific compatibility issue found in the wrapper module.
type WrapperIssue struct {
	Severity        diff.ChangeSeverity `json:"severity"`
	ConfidenceScore int                 `json:"confidence_score"`
	Category        string              `json:"category"`
	Target          string              `json:"target"`
	Description     string              `json:"description"`
	Remediation     string              `json:"remediation"`
}

// WrapperCompatibilityReport contains the verdict and findings for updating a wrapper module.
type WrapperCompatibilityReport struct {
	WrapperPath         string           `json:"wrapper_path"`
	TargetModuleName    string           `json:"target_module_name"`
	TargetVersion       string           `json:"target_version"`
	PinnedVersion       string           `json:"pinned_version,omitempty"`
	CanSafelyUpdate     bool             `json:"can_safely_update"`
	Verdict             string           `json:"verdict"`
	Issues              []WrapperIssue   `json:"issues"`
	NewFeatureAdoptions []WrapperIssue   `json:"new_feature_adoptions"`
	UpstreamDiff        *diff.DiffReport `json:"upstream_diff,omitempty"`
	RecommendedPin      string           `json:"recommended_pin"`
	RemediationSteps    []string         `json:"remediation_steps"`
	TotalBreaking       int              `json:"total_breaking"`
	TotalWarnings       int              `json:"total_warnings"`
	Summary             string           `json:"summary"`
}

// AnalyzeWrapper verifies whether a wrapper module can safely update and pin to targetUpstream.
func AnalyzeWrapper(
	wrapperMod *ast.ModuleInfo,
	targetUpstream *ast.ModuleInfo,
	pinnedUpstream *ast.ModuleInfo,
	targetVersion string,
	pinnedVersion string,
) *WrapperCompatibilityReport {
	if wrapperMod == nil {
		wrapperMod = &ast.ModuleInfo{
			Variables:               make(map[string]ast.VariableDef),
			Outputs:                 make(map[string]ast.OutputDef),
			ModuleCalls:             make(map[string]ast.ModuleCallDef),
			WrapperOutputReferences: make(map[string]ast.OutputReference),
		}
	}
	if targetUpstream == nil {
		targetUpstream = &ast.ModuleInfo{
			Variables: make(map[string]ast.VariableDef),
			Outputs:   make(map[string]ast.OutputDef),
		}
	}

	report := &WrapperCompatibilityReport{
		WrapperPath:      wrapperMod.Path,
		TargetVersion:    targetVersion,
		PinnedVersion:    pinnedVersion,
		Issues:           []WrapperIssue{},
		RemediationSteps: []string{},
	}

	// 1. Identify child module call in wrapper (e.g. "gke" or "eks")
	var targetCall *ast.ModuleCallDef
	for _, mc := range wrapperMod.ModuleCalls {
		m := mc
		targetCall = &m
		report.TargetModuleName = mc.Name
		break
	}

	passedInputSet := make(map[string]bool)
	if targetCall != nil {
		for _, inp := range targetCall.PassedInputs {
			passedInputSet[inp] = true
		}
	}

	// 2. Check if wrapper passes inputs removed or missing in target upstream
	for inp := range passedInputSet {
		if _, exists := targetUpstream.Variables[inp]; !exists {
			callName := "upstream"
			if targetCall != nil {
				callName = targetCall.Name
			}
			report.Issues = append(report.Issues, WrapperIssue{
				Severity:    diff.SeverityBreaking,
				Category:    "PassedRemovedInput",
				Target:      inp,
				Description: fmt.Sprintf("Wrapper passes argument '%s' to child module '%s', but '%s' is not an accepted input in target upstream %s", inp, callName, inp, targetVersion),
				Remediation: fmt.Sprintf("Remove argument '%s' from module \"%s\" block in wrapper", inp, callName),
			})
		}
	}

	// 3. Check if target upstream requires inputs that wrapper does NOT supply
	for varName, upVar := range targetUpstream.Variables {
		if upVar.Required && !upVar.HasDefault {
			if !passedInputSet[varName] {
				callName := "upstream"
				if targetCall != nil {
					callName = targetCall.Name
				}
				report.Issues = append(report.Issues, WrapperIssue{
					Severity:    diff.SeverityBreaking,
					Category:    "MissingRequiredInput",
					Target:      varName,
					Description: fmt.Sprintf("Target upstream %s requires input '%s' (no default), but wrapper call '%s' does not provide it", targetVersion, varName, callName),
					Remediation: fmt.Sprintf("Add argument '%s = ...' to module \"%s\" block in wrapper", varName, callName),
				})
			}
		}
	}

	// 4. Check wrapper variable type consistency against target upstream
	for varName, wVar := range wrapperMod.Variables {
		if upVar, exists := targetUpstream.Variables[varName]; exists {
			wrapperNullable := wVar.Nullable == nil || *wVar.Nullable
			upstreamNullable := upVar.Nullable == nil || *upVar.Nullable
			if wrapperNullable && !upstreamNullable {
				report.Issues = append(report.Issues, WrapperIssue{
					Severity:    diff.SeverityBreaking,
					Category:    "VariableNullability",
					Target:      varName,
					Description: fmt.Sprintf("Wrapper variable '%s' accepts null, but upstream no longer does", varName),
					Remediation: fmt.Sprintf("Reject null values for variable \"%s\" in the wrapper", varName),
				})
			}

			typeCompat := ast.CheckTypeCompatibility(wVar.Type, upVar.Type)
			if typeCompat.IsBreaking {
				report.Issues = append(report.Issues, WrapperIssue{
					Severity:    diff.SeverityBreaking,
					Category:    "TypeMismatch",
					Target:      varName,
					Description: fmt.Sprintf("Wrapper variable '%s' type '%s' is incompatible with upstream expected type '%s'", varName, wVar.Type, upVar.Type),
					Remediation: fmt.Sprintf("Update variable \"%s\" in wrapper to type = %s", varName, upVar.Type),
				})
			} else if wVar.Type != upVar.Type {
				report.Issues = append(report.Issues, WrapperIssue{
					Severity:    diff.SeverityWarning,
					Category:    "TypeMismatch",
					Target:      varName,
					Description: fmt.Sprintf("Wrapper variable '%s' type '%s' differs from upstream '%s': %s", varName, wVar.Type, upVar.Type, typeCompat.Reason),
					Remediation: fmt.Sprintf("Align variable \"%s\" with upstream type", varName),
				})
			}
		}
	}

	// 5. Check broken wrapper output delegations
	for outName, ref := range wrapperMod.WrapperOutputReferences {
		if _, exists := targetUpstream.Outputs[ref.TargetOutput]; !exists {
			report.Issues = append(report.Issues, WrapperIssue{
				Severity:    diff.SeverityBreaking,
				Category:    "BrokenOutputDelegation",
				Target:      outName,
				Description: fmt.Sprintf("Wrapper output '%s' references module.%s.%s, but output '%s' was removed in target upstream %s", outName, ref.TargetModule, ref.TargetOutput, ref.TargetOutput, targetVersion),
				Remediation: fmt.Sprintf("Update or remove output \"%s\" in wrapper outputs.tf", outName),
			})
		}
	}

	// 6. Upstream-to-Upstream AST comparison (if pinnedUpstream provided)
	if pinnedUpstream != nil {
		report.UpstreamDiff = diff.Compare(pinnedUpstream, targetUpstream, pinnedVersion, targetVersion)
	}

	// 7. Check new optional features available for adoption
	for varName, upVar := range targetUpstream.Variables {
		if !upVar.Required && upVar.HasDefault && !passedInputSet[varName] {
			if _, inWrapper := wrapperMod.Variables[varName]; !inWrapper {
				report.NewFeatureAdoptions = append(report.NewFeatureAdoptions, WrapperIssue{
					Severity:    diff.SeverityNewFeature,
					Category:    "OptionalVariableAvailable",
					Target:      varName,
					Description: fmt.Sprintf("New optional upstream parameter '%s' (type %s) can be exposed in wrapper", varName, upVar.Type),
					Remediation: fmt.Sprintf("Consider declaring variable \"%s\" in wrapper and forwarding to upstream", varName),
				})
			}
		}
	}

	scoreIssueConfidence(report.Issues)
	scoreIssueConfidence(report.NewFeatureAdoptions)

	// Sort issues deterministically
	sortIssues(report.Issues)
	sortIssues(report.NewFeatureAdoptions)

	// Count breakages
	for _, iss := range report.Issues {
		if iss.Severity == diff.SeverityBreaking {
			report.TotalBreaking++
			report.RemediationSteps = append(report.RemediationSteps, iss.Remediation)
		} else if iss.Severity == diff.SeverityWarning {
			report.TotalWarnings++
		}
	}

	// Generate safe pin recommendation & verdict
	normTarget := semverutil.Normalize(targetVersion)
	if normTarget != "" {
		report.RecommendedPin = fmt.Sprintf("version = \"%s\"", strings.TrimPrefix(normTarget, "v"))
	} else {
		report.RecommendedPin = fmt.Sprintf("version = \"%s\"", targetVersion)
	}

	if report.TotalBreaking > 0 {
		report.CanSafelyUpdate = false
		report.Verdict = "BREAKING_CHANGES_DETECTED"
		report.Summary = fmt.Sprintf("CANNOT SAFELY PIN: Wrapper has %d breaking issue(s) with upstream %s. Resolve remediation steps before pinning.", report.TotalBreaking, targetVersion)
	} else if report.TotalWarnings > 0 {
		report.CanSafelyUpdate = true
		report.Verdict = "SAFE_WITH_WARNINGS"
		report.Summary = fmt.Sprintf("CAN SAFELY PIN WITH WARNINGS: No syntax or type errors preventing upgrade to %s, but %d warning(s) detected.", targetVersion, report.TotalWarnings)
	} else {
		report.CanSafelyUpdate = true
		report.Verdict = "SAFE_TO_PIN"
		report.Summary = fmt.Sprintf("SAFE TO PIN: Wrapper is fully compatible with upstream version %s. Pin to %s.", targetVersion, report.RecommendedPin)
	}

	return report
}

func scoreIssueConfidence(issues []WrapperIssue) {
	for i := range issues {
		switch issues[i].Category {
		case "TypeMismatch":
			issues[i].ConfidenceScore = 8
		default:
			issues[i].ConfidenceScore = 9
		}
	}
}

func sortIssues(issues []WrapperIssue) {
	sort.Slice(issues, func(i, j int) bool {
		if issues[i].Category != issues[j].Category {
			return issues[i].Category < issues[j].Category
		}
		return issues[i].Target < issues[j].Target
	})
}
