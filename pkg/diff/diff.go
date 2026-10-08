package diff

import (
	"fmt"
	"sort"
	"strings"

	"github.com/23skdu/terraform_ast/pkg/ast"
	"github.com/23skdu/terraform_ast/pkg/semverutil"
)

// Compare analyzes the syntactic and semantic differences between oldMod and newMod.
func Compare(oldMod, newMod *ast.ModuleInfo, oldVer, newVer string) *DiffReport {
	if oldMod == nil {
		oldMod = &ast.ModuleInfo{
			Variables:         make(map[string]ast.VariableDef),
			Outputs:           make(map[string]ast.OutputDef),
			RequiredProviders: make(map[string]ast.ProviderRequirement),
		}
	}
	if newMod == nil {
		newMod = &ast.ModuleInfo{
			Variables:         make(map[string]ast.VariableDef),
			Outputs:           make(map[string]ast.OutputDef),
			RequiredProviders: make(map[string]ast.ProviderRequirement),
		}
	}

	bump := semverutil.DetermineBumpType(oldVer, newVer)
	isDowngrade := (bump == semverutil.BumpDowngrade)

	report := &DiffReport{
		OldVersion:  oldVer,
		NewVersion:  newVer,
		BumpType:    bump,
		IsDowngrade: isDowngrade,
	}

	// 1. Compare Input Variables
	compareVariables(oldMod, newMod, report)

	// 2. Compare Outputs
	compareOutputs(oldMod, newMod, report)

	// 3. Compare Providers and Core Requirements
	compareRequirements(oldMod, newMod, report)

	// Sort slices deterministically for stable reporting and golden testing
	sortDifferences(report.BreakingChanges)
	sortDifferences(report.NewFeatures)
	sortDifferences(report.Warnings)

	report.TotalBreaking = len(report.BreakingChanges)
	report.TotalNewFeatures = len(report.NewFeatures)
	report.TotalWarnings = len(report.Warnings)

	// Compute safety verdict
	computeVerdict(report)

	return report
}

func compareVariables(oldMod, newMod *ast.ModuleInfo, report *DiffReport) {
	// Check removed variables & modified variables
	for name, oldVar := range oldMod.Variables {
		newVar, exists := newMod.Variables[name]
		if !exists {
			report.BreakingChanges = append(report.BreakingChanges, DifferenceItem{
				Category:    "Variable",
				Name:        name,
				Severity:    SeverityBreaking,
				Description: fmt.Sprintf("Input variable '%s' was removed in upstream version", name),
				OldValue:    fmt.Sprintf("type=%s, required=%t", oldVar.Type, oldVar.Required),
				Remediation: fmt.Sprintf("Remove variable '%s' from wrapper or module callers before upgrading", name),
			})
			continue
		}

		// Type constraint comparison
		typeCompat := ast.CheckTypeCompatibility(oldVar.Type, newVar.Type)
		if typeCompat.IsBreaking {
			report.BreakingChanges = append(report.BreakingChanges, DifferenceItem{
				Category:    "VariableType",
				Name:        name,
				Severity:    SeverityBreaking,
				Description: fmt.Sprintf("Type constraint for variable '%s' changed: %s", name, typeCompat.Reason),
				OldValue:    oldVar.Type,
				NewValue:    newVar.Type,
				Remediation: fmt.Sprintf("Update variable type definition in wrapper to '%s'", newVar.Type),
			})
		} else if oldVar.Type != newVar.Type {
			report.Warnings = append(report.Warnings, DifferenceItem{
				Category:    "VariableType",
				Name:        name,
				Severity:    SeverityWarning,
				Description: fmt.Sprintf("Type constraint for variable '%s' widened: %s", name, typeCompat.Reason),
				OldValue:    oldVar.Type,
				NewValue:    newVar.Type,
				Remediation: "Verify variable values remain valid under widened type",
			})
		}

		// Check if previously optional input became required
		if oldVar.HasDefault && (!newVar.HasDefault && newVar.Required) {
			report.BreakingChanges = append(report.BreakingChanges, DifferenceItem{
				Category:    "VariableRequirement",
				Name:        name,
				Severity:    SeverityBreaking,
				Description: fmt.Sprintf("Variable '%s' was optional (had default) but is now required with no default", name),
				OldValue:    "optional with default",
				NewValue:    "required without default",
				Remediation: fmt.Sprintf("Explicitly supply a value for '%s' in the wrapper module call", name),
			})
		}

		// Check sensitivity transition
		if !oldVar.Sensitive && newVar.Sensitive {
			report.Warnings = append(report.Warnings, DifferenceItem{
				Category:    "VariableSensitivity",
				Name:        name,
				Severity:    SeverityWarning,
				Description: fmt.Sprintf("Variable '%s' is now marked sensitive", name),
				OldValue:    "sensitive=false",
				NewValue:    "sensitive=true",
				Remediation: "Ensure sensitive variable is not exposed in plaintext logs or outputs",
			})
		}

		// Check deprecation
		if oldVar.Deprecated == "" && newVar.Deprecated != "" {
			report.Warnings = append(report.Warnings, DifferenceItem{
				Category:    "VariableDeprecation",
				Name:        name,
				Severity:    SeverityWarning,
				Description: fmt.Sprintf("Variable '%s' is deprecated: %s", name, newVar.Deprecated),
				NewValue:    newVar.Deprecated,
				Remediation: "Migrate away from deprecated variable",
			})
		}
	}

	// Check newly introduced variables
	for name, newVar := range newMod.Variables {
		if _, exists := oldMod.Variables[name]; !exists {
			if newVar.Required && !newVar.HasDefault {
				// Newly required variable without default is BREAKING
				report.BreakingChanges = append(report.BreakingChanges, DifferenceItem{
					Category:    "Variable",
					Name:        name,
					Severity:    SeverityBreaking,
					Description: fmt.Sprintf("Newly introduced variable '%s' is required and has no default value", name),
					NewValue:    fmt.Sprintf("type=%s (required)", newVar.Type),
					Remediation: fmt.Sprintf("Provide a required value for '%s' in wrapper module call", name),
				})
			} else {
				// New optional variable is a NEW FEATURE
				report.NewFeatures = append(report.NewFeatures, DifferenceItem{
					Category:    "Variable",
					Name:        name,
					Severity:    SeverityNewFeature,
					Description: fmt.Sprintf("New optional variable '%s' available in upstream", name),
					NewValue:    fmt.Sprintf("type=%s", newVar.Type),
					Remediation: "Consider exposing this new parameter in wrapper if needed",
				})
			}
		}
	}
}

func compareOutputs(oldMod, newMod *ast.ModuleInfo, report *DiffReport) {
	// Check removed outputs & modified outputs
	for name, oldOut := range oldMod.Outputs {
		newOut, exists := newMod.Outputs[name]
		if !exists {
			report.BreakingChanges = append(report.BreakingChanges, DifferenceItem{
				Category:    "Output",
				Name:        name,
				Severity:    SeverityBreaking,
				Description: fmt.Sprintf("Output '%s' was removed in upstream version", name),
				Remediation: fmt.Sprintf("Remove or replace references to output '%s' in wrapper or downstream modules", name),
			})
			continue
		}

		// Type changes for outputs
		if oldOut.Type != "" && newOut.Type != "" && oldOut.Type != newOut.Type {
			typeCompat := ast.CheckTypeCompatibility(oldOut.Type, newOut.Type)
			if typeCompat.IsBreaking {
				report.BreakingChanges = append(report.BreakingChanges, DifferenceItem{
					Category:    "OutputType",
					Name:        name,
					Severity:    SeverityBreaking,
					Description: fmt.Sprintf("Output '%s' type constraint changed: %s", name, typeCompat.Reason),
					OldValue:    oldOut.Type,
					NewValue:    newOut.Type,
					Remediation: fmt.Sprintf("Update downstream references to output '%s' to expect '%s'", name, newOut.Type),
				})
			}
		}

		// Sensitivity changes
		if !oldOut.Sensitive && newOut.Sensitive {
			report.Warnings = append(report.Warnings, DifferenceItem{
				Category:    "OutputSensitivity",
				Name:        name,
				Severity:    SeverityWarning,
				Description: fmt.Sprintf("Output '%s' is now marked sensitive", name),
				OldValue:    "sensitive=false",
				NewValue:    "sensitive=true",
				Remediation: "Downstream modules reading this output may require nonsensitive() or will be masked in CLI",
			})
		}

		// Deprecation
		if oldOut.Deprecated == "" && newOut.Deprecated != "" {
			report.Warnings = append(report.Warnings, DifferenceItem{
				Category:    "OutputDeprecation",
				Name:        name,
				Severity:    SeverityWarning,
				Description: fmt.Sprintf("Output '%s' is deprecated: %s", name, newOut.Deprecated),
				NewValue:    newOut.Deprecated,
				Remediation: "Migrate downstream usage to recommended successor output",
			})
		}
	}

	// Check new outputs
	for name, newOut := range newMod.Outputs {
		if _, exists := oldMod.Outputs[name]; !exists {
			report.NewFeatures = append(report.NewFeatures, DifferenceItem{
				Category:    "Output",
				Name:        name,
				Severity:    SeverityNewFeature,
				Description: fmt.Sprintf("New output '%s' available in upstream", name),
				NewValue:    fmt.Sprintf("type=%s", newOut.Type),
				Remediation: "Consider re-exporting this output in wrapper if useful for downstream consumers",
			})
		}
	}
}

func compareRequirements(oldMod, newMod *ast.ModuleInfo, report *DiffReport) {
	// Core version constraints
	oldCore := strings.Join(oldMod.RequiredCore, ", ")
	newCore := strings.Join(newMod.RequiredCore, ", ")
	if oldCore != newCore && newCore != "" {
		report.Warnings = append(report.Warnings, DifferenceItem{
			Category:    "TerraformCore",
			Name:        "required_version",
			Severity:    SeverityWarning,
			Description: fmt.Sprintf("Terraform Core version constraint updated from '%s' to '%s'", oldCore, newCore),
			OldValue:    oldCore,
			NewValue:    newCore,
			Remediation: "Ensure execution environment meets new Terraform Core requirement",
		})
	}

	// Required providers
	for name, oldReq := range oldMod.RequiredProviders {
		newReq, exists := newMod.RequiredProviders[name]
		if !exists {
			report.Warnings = append(report.Warnings, DifferenceItem{
				Category:    "ProviderRequirement",
				Name:        name,
				Severity:    SeverityWarning,
				Description: fmt.Sprintf("Provider requirement '%s' was removed in upstream", name),
				OldValue:    strings.Join(oldReq.VersionConstraints, ", "),
				Remediation: "Verify wrapper still satisfies provider dependencies",
			})
		} else {
			oldC := strings.Join(oldReq.VersionConstraints, ", ")
			newC := strings.Join(newReq.VersionConstraints, ", ")
			if oldC != newC {
				report.Warnings = append(report.Warnings, DifferenceItem{
					Category:    "ProviderRequirement",
					Name:        name,
					Severity:    SeverityWarning,
					Description: fmt.Sprintf("Provider '%s' constraint updated from '%s' to '%s'", name, oldC, newC),
					OldValue:    oldC,
					NewValue:    newC,
					Remediation: "Ensure provider versions in root modules satisfy updated constraint",
				})
			}
		}
	}
}

func computeVerdict(report *DiffReport) {
	if report.IsDowngrade {
		report.CanSafelyUpdate = false
		report.Verdict = "DOWNGRADE"
		report.Summary = fmt.Sprintf("DOWNGRADE HAZARD: Target version %s is lower than current version %s. Downgrades may cause state corruption or resource recreation.", report.NewVersion, report.OldVersion)
		return
	}

	if report.TotalBreaking > 0 {
		report.CanSafelyUpdate = false
		report.Verdict = "BREAKING_CHANGES"
		report.Summary = fmt.Sprintf("CANNOT SAFELY UPDATE: Detected %d breaking change(s) between %s and %s. Wrapper must be updated before pinning.", report.TotalBreaking, report.OldVersion, report.NewVersion)
		return
	}

	if report.TotalWarnings > 0 {
		report.CanSafelyUpdate = true
		report.Verdict = "WARNINGS_ONLY"
		report.Summary = fmt.Sprintf("UPDATE SAFE WITH WARNINGS: No breaking API changes detected between %s and %s. Detected %d warning(s) and %d new feature(s).", report.OldVersion, report.NewVersion, report.TotalWarnings, report.TotalNewFeatures)
		return
	}

	report.CanSafelyUpdate = true
	report.Verdict = "SAFE"
	report.Summary = fmt.Sprintf("SAFE TO UPDATE: 100%% backward-compatible transition from %s to %s. Detected %d new feature(s).", report.OldVersion, report.NewVersion, report.TotalNewFeatures)
}

func sortDifferences(items []DifferenceItem) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].Category != items[j].Category {
			return items[i].Category < items[j].Category
		}
		return items[i].Name < items[j].Name
	})
}
