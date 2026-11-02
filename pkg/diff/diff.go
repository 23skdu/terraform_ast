package diff

import (
	"fmt"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/23skdu/terraform_ast/pkg/ast"
	"github.com/23skdu/terraform_ast/pkg/semverutil"
)

// Compare analyzes the syntactic and semantic differences between oldMod and newMod.
// An optional ast.HazardLookup enables schema-driven ForceNew attribute detection (#133).
func Compare(oldMod, newMod *ast.ModuleInfo, oldVer, newVer string, hazardLookups ...ast.HazardLookup) *DiffReport {
	if oldMod == nil {
		oldMod = &ast.ModuleInfo{
			Variables:         make(map[string]ast.VariableDef),
			Outputs:           make(map[string]ast.OutputDef),
			RequiredProviders: make(map[string]ast.ProviderRequirement),
			ManagedResources:  make(map[string]ast.ResourceDef),
			DataResources:     make(map[string]ast.ResourceDef),
		}
	}
	if newMod == nil {
		newMod = &ast.ModuleInfo{
			Variables:         make(map[string]ast.VariableDef),
			Outputs:           make(map[string]ast.OutputDef),
			RequiredProviders: make(map[string]ast.ProviderRequirement),
			ManagedResources:  make(map[string]ast.ResourceDef),
			DataResources:     make(map[string]ast.ResourceDef),
		}
	}
	if oldMod.ManagedResources == nil {
		oldMod.ManagedResources = make(map[string]ast.ResourceDef)
	}
	if oldMod.DataResources == nil {
		oldMod.DataResources = make(map[string]ast.ResourceDef)
	}
	if newMod.ManagedResources == nil {
		newMod.ManagedResources = make(map[string]ast.ResourceDef)
	}
	if newMod.DataResources == nil {
		newMod.DataResources = make(map[string]ast.ResourceDef)
	}

	bump := semverutil.DetermineBumpType(oldVer, newVer)
	isDowngrade := (bump == semverutil.BumpDowngrade)

	var hazardLookup ast.HazardLookup
	if len(hazardLookups) > 0 {
		hazardLookup = hazardLookups[0]
	}

	report := &DiffReport{
		ModuleName:  moduleDisplayName(newMod, oldMod),
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

	// 4. Compare Modern HCL & OpenTofu Blocks
	compareModernBlocks(oldMod, newMod, report)

	// 5. Compare Managed and Data Resources
	compareResources(oldMod, newMod, report)

	// 6. Compare Nested Module Calls
	compareModuleCalls(oldMod, newMod, report)

	// 7. Compare destructive toggles (lifecycle, backend, cloud, provider, provisioners, triggers, count/for_each, locals)
	compareDestructiveToggles(oldMod, newMod, report, hazardLookup)
	scoreConfidence(report)

	// Sort slices deterministically for stable reporting and golden testing
	sortDifferences(report.BreakingChanges)
	sortDifferences(report.NewFeatures)
	sortDifferences(report.Warnings)

	report.TotalBreaking = len(report.BreakingChanges)
	report.TotalNewFeatures = len(report.NewFeatures)
	report.TotalWarnings = len(report.Warnings)

	// Compute safety verdict
	computeVerdict(report)
	recommendSafePin(report)

	return report
}

// moduleDisplayName derives a human readable identifier for the compared module so that reports
// and SARIF artifact locations are never blank when callers do not supply an explicit source.
func moduleDisplayName(mods ...*ast.ModuleInfo) string {
	for _, mod := range mods {
		if trimmed := strings.TrimSpace(mod.Path); trimmed != "" {
			return filepath.Base(filepath.Clean(trimmed))
		}
	}
	return ""
}

func recommendSafePin(report *DiffReport) {
	switch {
	case report.IsDowngrade:
		// IsDowngrade is only set when both versions parse as valid semver, so the currently
		// analyzed version is always known here.
		report.SafePinVersion = report.OldVersion
		report.SafePinRationale = "The analyzed target is a downgrade; keep the currently analyzed version pinned."
	case report.CanSafelyUpdate && report.NewVersion != "":
		report.SafePinVersion = report.NewVersion
		report.SafePinRationale = "The analyzed target version has no detected breaking changes."
	case report.TotalBreaking > 0 && report.OldVersion != "":
		report.SafePinVersion = report.OldVersion
		report.SafePinRationale = "Keep the currently analyzed version pinned until the breaking changes are resolved."
	default:
		report.SafePinRationale = "No safe pin can be recommended from the supplied version information."
	}
}

func scoreConfidence(report *DiffReport) {
	for i := range report.BreakingChanges {
		ev := report.BreakingChanges[i].Evidence
		if ev < 1 {
			ev = 1
		}
		score := 8 + (ev - 1)
		report.BreakingChanges[i].ConfidenceScore = score
	}
	for i := range report.NewFeatures {
		ev := report.NewFeatures[i].Evidence
		if ev < 1 {
			ev = 1
		}
		score := 8 + (ev - 1)
		report.NewFeatures[i].ConfidenceScore = score
	}
	for i := range report.Warnings {
		ev := report.Warnings[i].Evidence
		var base int
		switch report.Warnings[i].Category {
		case "TerraformCore", "ProviderRequirement":
			base = 7
		case "VariableValidation":
			base = 6
		case "VariableType", "OutputType":
			base = 8
		default:
			base = 9
		}
		score := base + ev
		report.Warnings[i].ConfidenceScore = score
	}
}

func makeStringSet(values []string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	return set
}

func normalizeCondition(s string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	leftCounts := make(map[string]int, len(left))
	for _, s := range left {
		leftCounts[normalizeCondition(s)]++
	}
	rightCounts := make(map[string]int, len(right))
	for _, s := range right {
		rightCounts[normalizeCondition(s)]++
	}
	if len(leftCounts) != len(rightCounts) {
		return false
	}
	for k, v := range leftCounts {
		if rightCounts[k] != v {
			return false
		}
	}
	return true
}

func compareVariables(oldMod, newMod *ast.ModuleInfo, report *DiffReport) {
	// Check removed variables & modified variables
	for name, oldVar := range oldMod.Variables {
		newVar, exists := newMod.Variables[name]
		if !exists {
			isSoftRemoved := false
			for _, rb := range newMod.RemovedBlocks {
				if (rb.From == name || rb.From == "var."+name) && !rb.Destroy {
					isSoftRemoved = true
					break
				}
			}
			if isSoftRemoved {
				report.Warnings = append(report.Warnings, DifferenceItem{
					Category:    "Variable",
					Name:        name,
					Severity:    SeverityWarning,
					Description: fmt.Sprintf("Input variable '%s' was soft-removed via removed block (lifecycle.destroy = false)", name),
					OldValue:    fmt.Sprintf("type=%s, required=%t", oldVar.Type, oldVar.Required),
					Remediation: fmt.Sprintf("Variable '%s' is gracefully retired without destroying infrastructure", name),
				})
				continue
			}

			report.BreakingChanges = append(report.BreakingChanges, DifferenceItem{
				Category:    "Variable",
				Name:        name,
				Severity:    SeverityBreaking,
				Description: fmt.Sprintf("Input variable '%s' was removed in upstream version", name),
				OldValue:    fmt.Sprintf("type=%s, required=%t", oldVar.Type, oldVar.Required),
				Remediation: fmt.Sprintf("Remove variable '%s' from wrapper or module callers before upgrading", name),
				Pos:         oldVar.Pos,
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
				Pos:         oldVar.Pos,
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

		if !sameStrings(oldVar.ValidationRules, newVar.ValidationRules) {
			report.Warnings = append(report.Warnings, DifferenceItem{
				Category:    "VariableValidation",
				Name:        name,
				Severity:    SeverityWarning,
				Description: fmt.Sprintf("Validation rules for variable '%s' changed", name),
				OldValue:    strings.Join(oldVar.ValidationRules, "; "),
				NewValue:    strings.Join(newVar.ValidationRules, "; "),
				Remediation: "Review accepted values and validation behavior before upgrading",
			})
		}

		// Default value change (silent behaviour change for callers that omit the input).
		if oldVar.HasDefault && newVar.HasDefault && !reflect.DeepEqual(oldVar.Default, newVar.Default) {
			report.BreakingChanges = append(report.BreakingChanges, DifferenceItem{
				Category:    "VariableDefault",
				Name:        name,
				Severity:    SeverityBreaking,
				Description: fmt.Sprintf("Default value for variable '%s' changed", name),
				OldValue:    fmt.Sprintf("%v", oldVar.Default),
				NewValue:    fmt.Sprintf("%v", newVar.Default),
				Remediation: fmt.Sprintf("Review callers that omit '%s'; the default no longer matches", name),
				Pos:         oldVar.Pos,
				Evidence:    2,
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

		oldNullable := oldVar.Nullable == nil || *oldVar.Nullable
		newNullable := newVar.Nullable == nil || *newVar.Nullable
		if oldNullable && !newNullable {
			report.BreakingChanges = append(report.BreakingChanges, DifferenceItem{
				Category:    "VariableNullability",
				Name:        name,
				Severity:    SeverityBreaking,
				Description: fmt.Sprintf("Variable '%s' no longer accepts null values", name),
				OldValue:    "nullable=true",
				NewValue:    "nullable=false",
				Remediation: fmt.Sprintf("Ensure callers provide a non-null value for '%s'", name),
			})
		} else if !oldNullable && newNullable {
			report.Warnings = append(report.Warnings, DifferenceItem{
				Category:    "VariableNullability",
				Name:        name,
				Severity:    SeverityWarning,
				Description: fmt.Sprintf("Variable '%s' now accepts null values", name),
				OldValue:    "nullable=false",
				NewValue:    "nullable=true",
				Remediation: "Verify null values are handled safely by the module",
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
					Pos:         newVar.Pos,
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
				Pos:         oldOut.Pos,
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
					Pos:         oldOut.Pos,
				})
			} else {
				report.Warnings = append(report.Warnings, DifferenceItem{
					Category:    "OutputType",
					Name:        name,
					Severity:    SeverityWarning,
					Description: fmt.Sprintf("Output '%s' type constraint changed: %s", name, typeCompat.Reason),
					OldValue:    oldOut.Type,
					NewValue:    newOut.Type,
					Remediation: "Verify downstream consumers remain compatible with the changed output type",
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

		// Precondition / postcondition changes (#140)
		if !sameStrings(oldOut.Preconditions, newOut.Preconditions) {
			report.Warnings = append(report.Warnings, DifferenceItem{
				Category:    "PreconditionModified",
				Name:        name,
				Severity:    SeverityWarning,
				Description: fmt.Sprintf("precondition blocks changed for output '%s'", name),
				OldValue:    strings.Join(oldOut.Preconditions, "; "),
				NewValue:    strings.Join(newOut.Preconditions, "; "),
				Remediation: "Verify output preconditions remain valid",
			})
		}
		if !sameStrings(oldOut.Postconditions, newOut.Postconditions) {
			report.Warnings = append(report.Warnings, DifferenceItem{
				Category:    "PostconditionModified",
				Name:        name,
				Severity:    SeverityWarning,
				Description: fmt.Sprintf("postcondition blocks changed for output '%s'", name),
				OldValue:    strings.Join(oldOut.Postconditions, "; "),
				NewValue:    strings.Join(newOut.Postconditions, "; "),
				Remediation: "Verify output postconditions remain valid",
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

		// Output value expression change (silent behaviour change).
		oldVal := normalizeCondition(oldOut.Value)
		newVal := normalizeCondition(newOut.Value)
		if oldVal != "" && newVal != "" && oldVal != newVal {
			if oldOut.Type != newOut.Type || oldOut.Sensitive != newOut.Sensitive {
				report.BreakingChanges = append(report.BreakingChanges, DifferenceItem{
					Category:    "OutputValue",
					Name:        name,
					Severity:    SeverityBreaking,
					Description: fmt.Sprintf("Output '%s' value expression changed (type/sensitivity also changed)", name),
					OldValue:    oldOut.Value,
					NewValue:    newOut.Value,
					Remediation: "Update downstream consumers of this output",
					Pos:         oldOut.Pos,
				})
			} else {
				report.Warnings = append(report.Warnings, DifferenceItem{
					Category:    "OutputValue",
					Name:        name,
					Severity:    SeverityWarning,
					Description: fmt.Sprintf("Output '%s' value expression changed", name),
					OldValue:    oldOut.Value,
					NewValue:    newOut.Value,
					Remediation: "Verify downstream consumers remain compatible",
				})
			}
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
				Pos:         newOut.Pos,
			})
		}
	}
}

func compareRequirements(oldMod, newMod *ast.ModuleInfo, report *DiffReport) {
	// Core version constraints. Normalise whitespace and ordering so a reordering of a
	// multi-constraint list does not surface as a change; a removal is still reported.
	normalizeCore := func(values []string) string {
		trimmed := make([]string, 0, len(values))
		for _, v := range values {
			v = strings.TrimSpace(v)
			if v != "" {
				trimmed = append(trimmed, v)
			}
		}
		sort.Strings(trimmed)
		return strings.Join(trimmed, ", ")
	}
	oldCore := normalizeCore(oldMod.RequiredCore)
	newCore := normalizeCore(newMod.RequiredCore)
	if oldCore != newCore {
		desc := fmt.Sprintf("Terraform Core version constraint updated from '%s' to '%s'", oldCore, newCore)
		if newCore == "" {
			desc = fmt.Sprintf("Terraform Core version constraint removed (was '%s')", oldCore)
		}
		report.Warnings = append(report.Warnings, DifferenceItem{
			Category:    "TerraformCore",
			Name:        "required_version",
			Severity:    SeverityWarning,
			Description: desc,
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
			oldAliases := makeStringSet(oldReq.ConfigurationAliases)
			newAliases := makeStringSet(newReq.ConfigurationAliases)
			for alias := range newAliases {
				if _, exists := oldAliases[alias]; !exists {
					report.BreakingChanges = append(report.BreakingChanges, DifferenceItem{
						Category:    "ProviderConfigurationAlias",
						Name:        alias,
						Severity:    SeverityBreaking,
						Description: fmt.Sprintf("Provider configuration alias '%s' is newly required by upstream", alias),
						Remediation: fmt.Sprintf("Pass a provider configuration for alias '%s' to the module call", alias),
					})
				}
			}
			for alias := range oldAliases {
				if _, exists := newAliases[alias]; !exists {
					report.Warnings = append(report.Warnings, DifferenceItem{
						Category:    "ProviderConfigurationAlias",
						Name:        alias,
						Severity:    SeverityWarning,
						Description: fmt.Sprintf("Provider configuration alias '%s' is no longer declared by upstream", alias),
						Remediation: "Verify provider mappings in parent modules remain valid",
					})
				}
			}

			if oldReq.Source != "" && newReq.Source != "" && oldReq.Source != newReq.Source {
				report.BreakingChanges = append(report.BreakingChanges, DifferenceItem{
					Category:    "ProviderSource",
					Name:        name,
					Severity:    SeverityBreaking,
					Description: fmt.Sprintf("Provider '%s' source changed from '%s' to '%s'", name, oldReq.Source, newReq.Source),
					OldValue:    oldReq.Source,
					NewValue:    newReq.Source,
					Remediation: "Verify provider configuration and state compatibility for the new provider source",
				})
			}

			normalizeConstraints := func(values []string) string {
				trimmed := make([]string, 0, len(values))
				for _, v := range values {
					v = strings.TrimSpace(v)
					if v != "" {
						trimmed = append(trimmed, v)
					}
				}
				sort.Strings(trimmed)
				return strings.Join(trimmed, ", ")
			}
			oldC := normalizeConstraints(oldReq.VersionConstraints)
			newC := normalizeConstraints(newReq.VersionConstraints)
			if oldC == newC {
				// constraint-only noise (whitespace/ordering) produces nothing
			} else if isMajorProviderBump(oldReq.VersionConstraints, newReq.VersionConstraints) {
				report.BreakingChanges = append(report.BreakingChanges, DifferenceItem{
					Category:    "ProviderMajorUpgrade",
					Name:        name,
					Severity:    SeverityBreaking,
					Description: fmt.Sprintf("Provider '%s' constraint major upgrade from '%s' to '%s' (schema/state migration likely required)", name, oldC, newC),
					OldValue:    oldC,
					NewValue:    newC,
					Remediation: "Review provider migration guide and update resource schemas before upgrading",
					Evidence:    2,
				})
			} else {
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

	// Newly added required providers
	var newProviderNames []string
	for name := range newMod.RequiredProviders {
		if _, exists := oldMod.RequiredProviders[name]; !exists {
			newProviderNames = append(newProviderNames, name)
		}
	}
	sort.Strings(newProviderNames)
	for _, name := range newProviderNames {
		newReq := newMod.RequiredProviders[name]
		report.Warnings = append(report.Warnings, DifferenceItem{
			Category:    "ProviderRequirement",
			Name:        name,
			Severity:    SeverityWarning,
			Description: fmt.Sprintf("Provider requirement '%s' was added in upstream", name),
			NewValue:    strings.Join(newReq.VersionConstraints, ", "),
			Remediation: fmt.Sprintf("Ensure root module supplies provider '%s' matching constraints '%s'", name, strings.Join(newReq.VersionConstraints, ", ")),
		})
		for _, alias := range newReq.ConfigurationAliases {
			report.BreakingChanges = append(report.BreakingChanges, DifferenceItem{
				Category:    "ProviderConfigurationAlias",
				Name:        alias,
				Severity:    SeverityBreaking,
				Description: fmt.Sprintf("Provider configuration alias '%s' is newly required by upstream", alias),
				Remediation: fmt.Sprintf("Pass a provider configuration for alias '%s' to the module call", alias),
			})
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

func compareModernBlocks(oldMod, newMod *ast.ModuleInfo, report *DiffReport) {
	if oldMod == nil {
		oldMod = &ast.ModuleInfo{}
	}
	if newMod == nil {
		newMod = &ast.ModuleInfo{}
	}

	// 1. Removed blocks (Terraform 1.7+ / OpenTofu 1.6+). Keyed on both directions.
	oldRemoved := make(map[string]ast.RemovedBlockDef)
	for _, rb := range oldMod.RemovedBlocks {
		oldRemoved[rb.From] = rb
	}
	for _, rb := range newMod.RemovedBlocks {
		if !rb.Destroy {
			if _, existed := oldRemoved[rb.From]; existed {
				continue
			}
			report.NewFeatures = append(report.NewFeatures, DifferenceItem{
				Category:        "SoftRemovedBlock",
				Name:            rb.From,
				Severity:        SeverityNewFeature,
				ConfidenceScore: 9,
				Description:     fmt.Sprintf("Resource or block '%s' declared in removed block with lifecycle.destroy = false (soft migration)", rb.From),
				Pos:             rb.Pos,
			})
		} else {
			if _, existed := oldRemoved[rb.From]; existed {
				continue
			}
			report.BreakingChanges = append(report.BreakingChanges, DifferenceItem{
				Category:        "RemovedDestroy",
				Name:            rb.From,
				Severity:        SeverityBreaking,
				ConfidenceScore: 9,
				Description:     fmt.Sprintf("Resource or block '%s' declared in removed block with lifecycle.destroy = true (upstream declares this address will be destroyed)", rb.From),
				Pos:             rb.Pos,
			})
		}
	}
	// A removed block that disappears -> destruction path no longer declared.
	for _, rb := range oldMod.RemovedBlocks {
		found := false
		for _, nrb := range newMod.RemovedBlocks {
			if nrb.From == rb.From {
				found = true
				break
			}
		}
		if !found {
			report.Warnings = append(report.Warnings, DifferenceItem{
				Category:        "RemovedBlockRemoved",
				Name:            rb.From,
				Severity:        SeverityWarning,
				ConfidenceScore: 9,
				Description:     fmt.Sprintf("removed block for '%s' no longer declared (destruction path undeclared again)", rb.From),
			})
		}
	}

	// 2. Check blocks (Terraform 1.5+).
	for name, cb := range newMod.CheckBlocks {
		oldCB, existed := oldMod.CheckBlocks[name]
		if !existed {
			report.NewFeatures = append(report.NewFeatures, DifferenceItem{
				Category:        "CheckBlockAdded",
				Name:            name,
				Severity:        SeverityNewFeature,
				ConfidenceScore: 9,
				Description:     fmt.Sprintf("Check assertion block '%s' declared with %d rules", name, len(cb.AssertRules)),
			})
		} else if !sameStrings(oldCB.AssertRules, cb.AssertRules) {
			report.Warnings = append(report.Warnings, DifferenceItem{
				Category:        "CheckBlockModified",
				Name:            name,
				Severity:        SeverityWarning,
				ConfidenceScore: 9,
				Description:     fmt.Sprintf("Check assertion block '%s' conditions changed", name),
				OldValue:        strings.Join(oldCB.AssertRules, "; "),
				NewValue:        strings.Join(cb.AssertRules, "; "),
			})
		}
	}
	for name := range oldMod.CheckBlocks {
		if _, exists := newMod.CheckBlocks[name]; !exists {
			report.Warnings = append(report.Warnings, DifferenceItem{
				Category:        "CheckBlockRemoved",
				Name:            name,
				Severity:        SeverityWarning,
				ConfidenceScore: 9,
				Description:     fmt.Sprintf("Check assertion block '%s' removed (validation regression)", name),
			})
		}
	}

	// 3. Import blocks (Terraform 1.5+).
	oldImportMap := make(map[string]ast.ImportBlockDef, len(oldMod.ImportBlocks))
	for _, ib := range oldMod.ImportBlocks {
		oldImportMap[ib.To] = ib
	}
	for _, ib := range newMod.ImportBlocks {
		if oldIB, existed := oldImportMap[ib.To]; existed {
			if oldIB.ID != ib.ID {
				report.BreakingChanges = append(report.BreakingChanges, DifferenceItem{
					Category:        "ImportBlockModified",
					Name:            ib.To,
					Severity:        SeverityBreaking,
					ConfidenceScore: 9,
					Description:     fmt.Sprintf("Programmatic import ID for '%s' changed from '%s' to '%s'", ib.To, oldIB.ID, ib.ID),
					OldValue:        oldIB.ID,
					NewValue:        ib.ID,
					Evidence:        2,
				})
			}
			if oldIB.Provider != "" && ib.Provider != "" && oldIB.Provider != ib.Provider {
				report.BreakingChanges = append(report.BreakingChanges, DifferenceItem{
					Category:        "ImportBlockProvider",
					Name:            ib.To,
					Severity:        SeverityBreaking,
					ConfidenceScore: 9,
					Description:     fmt.Sprintf("Programmatic import provider for '%s' changed from '%s' to '%s'", ib.To, oldIB.Provider, ib.Provider),
					OldValue:        oldIB.Provider,
					NewValue:        ib.Provider,
					Evidence:        2,
				})
			}
			continue
		}
		report.NewFeatures = append(report.NewFeatures, DifferenceItem{
			Category:        "ImportBlockAdded",
			Name:            ib.To,
			Severity:        SeverityNewFeature,
			ConfidenceScore: 9,
			Description:     fmt.Sprintf("Programmatic import declared for '%s' (id=%s)", ib.To, ib.ID),
		})
	}
	newImports := makeStringSet(importAddresses(newMod.ImportBlocks))
	for _, ib := range oldMod.ImportBlocks {
		if _, existed := newImports[ib.To]; !existed {
			report.Warnings = append(report.Warnings, DifferenceItem{
				Category:        "ImportBlockRemoved",
				Name:            ib.To,
				Severity:        SeverityWarning,
				ConfidenceScore: 9,
				Description:     fmt.Sprintf("Import block for '%s' removed (re-import required)", ib.To),
			})
		}
	}

	// 4. Custom Functions (Terraform 1.8+).
	for name, fn := range newMod.Functions {
		if _, existed := oldMod.Functions[name]; !existed {
			report.NewFeatures = append(report.NewFeatures, DifferenceItem{
				Category:        "FunctionAdded",
				Name:            name,
				Severity:        SeverityNewFeature,
				ConfidenceScore: 9,
				Description:     fmt.Sprintf("Custom function '%s' declared (returns %s)", name, fn.ReturnType),
			})
		}
	}
	for name := range oldMod.Functions {
		if _, exists := newMod.Functions[name]; !exists {
			report.BreakingChanges = append(report.BreakingChanges, DifferenceItem{
				Category:        "FunctionRemoved",
				Name:            name,
				Severity:        SeverityBreaking,
				ConfidenceScore: 9,
				Description:     fmt.Sprintf("Custom function '%s' removed (callers fail at plan)", name),
			})
		}
	}

	// 5. Encryption blocks (OpenTofu 1.7+).
	oldEncryption := makeStringSet(encryptionProviders(oldMod.EncryptionBlocks))
	for _, eb := range newMod.EncryptionBlocks {
		if _, existed := oldEncryption[eb.KeyProvider]; existed {
			continue
		}
		report.NewFeatures = append(report.NewFeatures, DifferenceItem{
			Category:        "EncryptionConfigured",
			Name:            eb.KeyProvider,
			Severity:        SeverityNewFeature,
			ConfidenceScore: 9,
			Description:     fmt.Sprintf("State encryption configured with key provider '%s'", eb.KeyProvider),
		})
	}
	newEncryption := makeStringSet(encryptionProviders(newMod.EncryptionBlocks))
	for _, eb := range oldMod.EncryptionBlocks {
		if _, existed := newEncryption[eb.KeyProvider]; !existed {
			report.BreakingChanges = append(report.BreakingChanges, DifferenceItem{
				Category:        "EncryptionRemoved",
				Name:            eb.KeyProvider,
				Severity:        SeverityBreaking,
				ConfidenceScore: 9,
				Description:     fmt.Sprintf("State encryption with key provider '%s' removed (encryption stops applying)", eb.KeyProvider),
			})
		}
	}

	// Moved blocks (Terraform 1.1+). New upstream moved block -> new feature (state migration shipped).
	// Removed upstream moved block -> breaking (state migration no longer declared).
	oldMoved := make(map[string]ast.MovedBlockDef)
	for _, mb := range oldMod.MovedBlocks {
		oldMoved[mb.From+"->"+mb.To] = mb
	}
	for _, mb := range newMod.MovedBlocks {
		key := mb.From + "->" + mb.To
		if _, existed := oldMoved[key]; existed {
			continue
		}
		report.NewFeatures = append(report.NewFeatures, DifferenceItem{
			Category:        "MovedBlockAdded",
			Name:            mb.To,
			Severity:        SeverityNewFeature,
			ConfidenceScore: 9,
			Description:     fmt.Sprintf("State migration declared: moved %s to %s", mb.From, mb.To),
		})
	}
	newMoved := make(map[string]ast.MovedBlockDef)
	for _, mb := range newMod.MovedBlocks {
		newMoved[mb.From+"->"+mb.To] = mb
	}
	for _, mb := range oldMod.MovedBlocks {
		key := mb.From + "->" + mb.To
		if _, existed := newMoved[key]; !existed {
			report.BreakingChanges = append(report.BreakingChanges, DifferenceItem{
				Category:        "StateMigrationRemoved",
				Name:            mb.From,
				Severity:        SeverityBreaking,
				ConfidenceScore: 9,
				Description:     fmt.Sprintf("State migration removed: %s -> %s no longer declared", mb.From, mb.To),
			})
		}
	}
}

// importAddresses returns the set of resource addresses that are programmatically imported.
func importAddresses(blocks []ast.ImportBlockDef) []string {
	addresses := make([]string, 0, len(blocks))
	for _, block := range blocks {
		addresses = append(addresses, block.To)
	}
	return addresses
}

// majorVersionRe extracts the numeric major and optional minor version component from a constraint
// string such as "~> 4.0", ">= 0.12.0", or "< 5.0".
var majorVersionRe = regexp.MustCompile(`(\d+)(?:\.(\d+))?`)

func majorFromConstraint(c string) (int, bool) {
	m := majorVersionRe.FindStringSubmatch(strings.TrimSpace(c))
	if m == nil {
		return 0, false
	}
	n, _ := strconv.Atoi(m[1])
	return n, true
}

func isZeroMajorBreakingBump(oldC, newC string) bool {
	mOld := majorVersionRe.FindStringSubmatch(strings.TrimSpace(oldC))
	mNew := majorVersionRe.FindStringSubmatch(strings.TrimSpace(newC))
	if len(mOld) >= 3 && len(mNew) >= 3 && mOld[1] == "0" && mNew[1] == "0" && mOld[2] != "" && mNew[2] != "" {
		minOld, _ := strconv.Atoi(mOld[2])
		minNew, _ := strconv.Atoi(mNew[2])
		return minNew > minOld
	}
	return false
}

func upperBoundMajor(c string) (int, bool) {
	for _, part := range strings.Split(c, ",") {
		trimmed := strings.TrimSpace(part)
		if strings.HasPrefix(trimmed, "<") && !strings.HasPrefix(trimmed, "<=") {
			return majorFromConstraint(trimmed)
		}
	}
	return 0, false
}

// isMajorProviderBump reports whether the provider version constraints move to a different
// major version, drop an upper-bound major version, or bump minor in 0.x.
func isMajorProviderBump(oldConstraints, newConstraints []string) bool {
	if len(oldConstraints) == 0 || len(newConstraints) == 0 {
		return false
	}
	oldJoined := strings.Join(oldConstraints, ", ")
	newJoined := strings.Join(newConstraints, ", ")
	if oldUpper, okO := upperBoundMajor(oldJoined); okO {
		if newUpper, okN := upperBoundMajor(newJoined); okN && newUpper < oldUpper {
			return true
		}
	}
	oldMajor, okOld := majorFromConstraint(oldConstraints[0])
	newMajor, okNew := majorFromConstraint(newConstraints[0])
	if !okOld || !okNew {
		return false
	}
	if isZeroMajorBreakingBump(oldConstraints[0], newConstraints[0]) {
		return true
	}
	for _, oc := range oldConstraints {
		om, ok := majorFromConstraint(oc)
		if !ok {
			continue
		}
		for _, nc := range newConstraints {
			nm, ok2 := majorFromConstraint(nc)
			if !ok2 {
				continue
			}
			if om != nm && om != 0 && nm > om {
				return true
			}
		}
	}
	return oldMajor != newMajor && newMajor > oldMajor
}

func compareDestructiveToggles(oldMod, newMod *ast.ModuleInfo, report *DiffReport, hazardLookup ast.HazardLookup) {
	if oldMod == nil {
		oldMod = &ast.ModuleInfo{}
	}
	if newMod == nil {
		newMod = &ast.ModuleInfo{}
	}

	// Backend / Cloud change (#136): treat backend {} and cloud {} as distinct surfaces.
	// backend -> cloud (or vice versa) is a breaking transition.
	oldBackendType := ""
	newBackendType := ""
	if oldMod.Backend != nil {
		oldBackendType = oldMod.Backend.Type
	}
	if newMod.Backend != nil {
		newBackendType = newMod.Backend.Type
	}


	// backend <-> cloud transition
	if (oldMod.Backend != nil && newMod.Cloud != nil) || (oldMod.Cloud != nil && newMod.Backend != nil) {
		report.BreakingChanges = append(report.BreakingChanges, DifferenceItem{
			Category:    "StateBackendChange",
			Name:        "backend",
			Severity:    SeverityBreaking,
			Description: "State backend configuration transitioned between backend {} and cloud {}",
			OldValue:    func() string {
				if oldMod.Backend != nil {
					return oldMod.Backend.Type
				}
				return "cloud"
			}(),
			NewValue: func() string {
				if newMod.Cloud != nil {
					return "cloud"
				}
				return newMod.Backend.Type
			}(),
			Remediation: "Migrate state to the new backend before upgrading",
		})
	}

	// backend type change
	if oldBackendType != newBackendType && (oldBackendType != "" || newBackendType != "") && !(oldMod.Backend == nil && newMod.Backend == nil) {
		// only report when both sides use backend (not a backend<->cloud transition already reported)
		if !(oldMod.Backend != nil && newMod.Cloud != nil) && !(oldMod.Cloud != nil && newMod.Backend != nil) {
			report.BreakingChanges = append(report.BreakingChanges, DifferenceItem{
				Category:    "StateBackendChange",
				Name:        "backend",
				Severity:    SeverityBreaking,
				Description: fmt.Sprintf("State backend type changed from '%s' to '%s'", oldBackendType, newBackendType),
				OldValue:    oldBackendType,
				NewValue:    newBackendType,
				Remediation: "Migrate state to the new backend before upgrading",
			})
		}
	}
	// cloud organization/workspace/attributes change
	compareBackendAttrs := func(label string, oldB, newB *ast.BackendConfig) {
		if oldB == nil || newB == nil {
			return
		}
		oldAttrs := oldB.Attributes
		newAttrs := newB.Attributes
		if oldAttrs == nil {
			oldAttrs = map[string]string{}
		}
		if newAttrs == nil {
			newAttrs = map[string]string{}
		}
		for k, v := range oldAttrs {
			if nv, ok := newAttrs[k]; ok {
				if v != nv {
					report.BreakingChanges = append(report.BreakingChanges, DifferenceItem{
						Category:    "StateBackendChange",
						Name:        label + "." + k,
						Severity:    SeverityBreaking,
						Description: fmt.Sprintf("Backend attribute '%s' changed from '%s' to '%s'", k, v, nv),
						OldValue:    v,
						NewValue:    nv,
						Remediation: "Migrate state to the new backend before upgrading",
					})
				}
			} else {
				report.BreakingChanges = append(report.BreakingChanges, DifferenceItem{
					Category:    "StateBackendChange",
					Name:        label + "." + k,
					Severity:    SeverityBreaking,
					Description: fmt.Sprintf("Backend attribute '%s' removed", k),
					OldValue:    v,
					Remediation: "Migrate state to the new backend before upgrading",
				})
			}
		}
		for k, v := range newAttrs {
			if _, ok := oldAttrs[k]; !ok {
				report.BreakingChanges = append(report.BreakingChanges, DifferenceItem{
					Category:    "StateBackendChange",
					Name:        label + "." + k,
					Severity:    SeverityBreaking,
					Description: fmt.Sprintf("Backend attribute '%s' added", k),
					NewValue:    v,
					Remediation: "Migrate state to the new backend before upgrading",
				})
			}
		}
	}
	compareBackendAttrs("backend", oldMod.Backend, newMod.Backend)
	compareBackendAttrs("cloud", oldMod.Cloud, newMod.Cloud)

	// cloud added/removed
	if (oldMod.Cloud == nil) != (newMod.Cloud == nil) && !(oldMod.Cloud == nil && newMod.Cloud == nil) {
		report.BreakingChanges = append(report.BreakingChanges, DifferenceItem{
			Category:    "StateBackendChange",
			Name:        "cloud",
			Severity:    SeverityBreaking,
			Description: "terraform { cloud {} } configuration added or removed",
			Remediation: "Migrate state to the new backend before upgrading",
		})
	}

	// Resource-level lifecycle/provisioner comparison
	sets := []struct {
		old    map[string]ast.ResourceDef
		new    map[string]ast.ResourceDef
		isData bool
	}{
		{oldMod.ManagedResources, newMod.ManagedResources, false},
		{oldMod.DataResources, newMod.DataResources, true},
	}
	for _, s := range sets {
		for key, oldRes := range s.old {
			newRes, ok := s.new[key]
			if !ok {
				continue
			}
			// prevent_destroy removed
			if oldRes.Lifecycle.PreventDestroy && !newRes.Lifecycle.PreventDestroy {
				report.BreakingChanges = append(report.BreakingChanges, DifferenceItem{
					Category:    "LifecycleDestroy",
					Name:        key,
					Severity:    SeverityBreaking,
					Description: fmt.Sprintf("lifecycle.prevent_destroy removed from resource '%s'", key),
					OldValue:    "true",
					NewValue:    "false",
					Remediation: "Re-enable prevent_destroy or accept destruction risk",
				})
			}
			// create_before_destroy removed
			if oldRes.Lifecycle.CreateBeforeDestroy && !newRes.Lifecycle.CreateBeforeDestroy {
				report.Warnings = append(report.Warnings, DifferenceItem{
					Category:    "LifecycleCreateBeforeDestroy",
					Name:        key,
					Severity:    SeverityWarning,
					Description: fmt.Sprintf("lifecycle.create_before_destroy removed from resource '%s'", key),
					OldValue:    "true",
					NewValue:    "false",
					Remediation: "Verify downtime tolerance for in-place replacements",
				})
			}
			// replace_triggered_by added or modified
			if len(newRes.Lifecycle.ReplaceTriggeredBy) > 0 && !sameStrings(oldRes.Lifecycle.ReplaceTriggeredBy, newRes.Lifecycle.ReplaceTriggeredBy) {
				report.BreakingChanges = append(report.BreakingChanges, DifferenceItem{
					Category:    "LifecycleReplaceTriggered",
					Name:        key,
					Severity:    SeverityBreaking,
					Description: fmt.Sprintf("lifecycle.replace_triggered_by changed for resource '%s'", key),
					OldValue:    strings.Join(oldRes.Lifecycle.ReplaceTriggeredBy, ", "),
					NewValue:    strings.Join(newRes.Lifecycle.ReplaceTriggeredBy, ", "),
					Remediation: "Review replacement triggers before upgrading",
					Evidence:    2,
				})
			}
			// ignore_changes: narrowing (old has more than new) -> warning
			if len(oldRes.Lifecycle.IgnoreChanges) > len(newRes.Lifecycle.IgnoreChanges) {
				report.Warnings = append(report.Warnings, DifferenceItem{
					Category:    "LifecycleIgnoreChanges",
					Name:        key,
					Severity:    SeverityWarning,
					Description: fmt.Sprintf("lifecycle.ignore_changes narrowed for resource '%s'", key),
					OldValue:    strings.Join(oldRes.Lifecycle.IgnoreChanges, ", "),
					NewValue:    strings.Join(newRes.Lifecycle.IgnoreChanges, ", "),
					Remediation: "Verify previously ignored attributes are now applied",
				})
			}
			// count / for_each cardinality changes (#113)
			if oldRes.Count != "" && newRes.Count != "" && oldRes.Count != newRes.Count {
				oldN, errOld := strconv.Atoi(oldRes.Count)
				newN, errNew := strconv.Atoi(newRes.Count)
				if (errOld == nil && errNew == nil && newN < oldN) || newRes.Count == "0" {
					report.BreakingChanges = append(report.BreakingChanges, DifferenceItem{
						Category:    "ResourceCount",
						Name:        key,
						Severity:    SeverityBreaking,
						Description: fmt.Sprintf("Resource '%s' count reduced from %s to %s (destroys state instances)", key, oldRes.Count, newRes.Count),
						OldValue:    oldRes.Count,
						NewValue:    newRes.Count,
						Remediation: "Verify state instance removal before reducing count",
						Evidence:    2,
					})
				} else {
					report.Warnings = append(report.Warnings, DifferenceItem{
						Category:    "ResourceCount",
						Name:        key,
						Severity:    SeverityWarning,
						Description: fmt.Sprintf("Resource '%s' count expression changed from '%s' to '%s'", key, oldRes.Count, newRes.Count),
						OldValue:    oldRes.Count,
						NewValue:    newRes.Count,
						Remediation: "Verify instance count change does not drop active state instances",
					})
				}
			}
			if oldRes.ForEach != "" && newRes.ForEach != "" && oldRes.ForEach != newRes.ForEach {
				report.BreakingChanges = append(report.BreakingChanges, DifferenceItem{
					Category:    "ResourceForEach",
					Name:        key,
					Severity:    SeverityBreaking,
					Description: fmt.Sprintf("Resource '%s' for_each expression changed from '%s' to '%s' (may destroy or recreate instances)", key, oldRes.ForEach, newRes.ForEach),
					OldValue:    oldRes.ForEach,
					NewValue:    newRes.ForEach,
					Remediation: "Add moved {} blocks or verify for_each key set preservation",
					Evidence:    2,
				})
			}
			// triggers / triggers_replace / keepers (#114)
			if (len(oldRes.Triggers) > 0 || len(newRes.Triggers) > 0) && !reflect.DeepEqual(oldRes.Triggers, newRes.Triggers) {
				report.BreakingChanges = append(report.BreakingChanges, DifferenceItem{
					Category:    "ResourceTriggers",
					Name:        key,
					Severity:    SeverityBreaking,
					Description: fmt.Sprintf("Replacement triggers changed for resource '%s'", key),
					Remediation: "Review trigger changes that force resource replacement",
					Evidence:    2,
				})
			}
			// destructive protection attributes (#114) and ForceNew resource attributes (#115)
			for _, protAttr := range []string{"deletion_protection", "purge_protection_enabled"} {
				if oldRes.Attributes[protAttr] == "true" && newRes.Attributes[protAttr] != "true" {
					report.BreakingChanges = append(report.BreakingChanges, DifferenceItem{
						Category:    "DestructiveAttribute",
						Name:        key + "." + protAttr,
						Severity:    SeverityBreaking,
						Description: fmt.Sprintf("Destruction guard '%s' disabled or removed on resource '%s'", protAttr, key),
						OldValue:    "true",
						NewValue:    newRes.Attributes[protAttr],
						Remediation: fmt.Sprintf("Keep %s = true to prevent accidental resource deletion", protAttr),
						Evidence:    2,
					})
				}
			}
			for _, forceAttr := range []string{"force_destroy", "skip_final_snapshot"} {
				if oldRes.Attributes[forceAttr] != "true" && newRes.Attributes[forceAttr] == "true" {
					report.BreakingChanges = append(report.BreakingChanges, DifferenceItem{
						Category:    "DestructiveAttribute",
						Name:        key + "." + forceAttr,
						Severity:    SeverityBreaking,
						Description: fmt.Sprintf("Destructive flag '%s = true' enabled on resource '%s'", forceAttr, key),
						OldValue:    oldRes.Attributes[forceAttr],
						NewValue:    "true",
						Remediation: fmt.Sprintf("Disable %s in production to prevent unrecoverable data loss", forceAttr),
						Evidence:    2,
					})
				}
			}
			hardcodedForceAttrs := map[string]bool{"cidr_block": true, "vpc_id": true, "availability_zone": true, "subnet_id": true, "ami": true, "engine": true, "location": true, "region": true, "project": true, "resource_group_name": true}
			for _, fnAttr := range []string{"cidr_block", "vpc_id", "availability_zone", "subnet_id", "ami", "engine", "location", "region", "project", "resource_group_name"} {
				oldV, okO := oldRes.Attributes[fnAttr]
				newV, okN := newRes.Attributes[fnAttr]
				if okO && okN && oldV != newV {
					report.BreakingChanges = append(report.BreakingChanges, DifferenceItem{
						Category:    "ResourceForceNew",
						Name:        key + "." + fnAttr,
						Severity:    SeverityBreaking,
						Description: fmt.Sprintf("Immutable ForceNew attribute '%s' on resource '%s' changed from '%s' to '%s'", fnAttr, key, oldV, newV),
						OldValue:    oldV,
						NewValue:    newV,
						Remediation: "Avoid changing immutable resource attributes in-place",
						Evidence:    2,
					})
				}
			}
			// schema-driven ForceNew detection (#133)
			if hazardLookup != nil {
				for attrName, oldV := range oldRes.Attributes {
					if hardcodedForceAttrs[attrName] {
						continue
					}
					newV, okN := newRes.Attributes[attrName]
					if !okN || oldV == newV {
						continue
					}
					isForce, impact, remediation := hazardLookup.LookupHazard(key + "." + attrName)
					if !isForce {
						isForce, impact, remediation = hazardLookup.LookupHazard(attrName)
					}
					if isForce {
						if remediation == "" {
							remediation = "Avoid changing immutable resource attributes in-place"
						}
						if impact == "" {
							impact = fmt.Sprintf("Immutable ForceNew attribute '%s' on resource '%s' changed from '%s' to '%s'", attrName, key, oldV, newV)
						}
						report.BreakingChanges = append(report.BreakingChanges, DifferenceItem{
							Category:    "ResourceForceNew",
							Name:        key + "." + attrName,
							Severity:    SeverityBreaking,
							Description: impact,
							OldValue:    oldV,
							NewValue:    newV,
							Remediation: remediation,
							Evidence:    2,
						})
					}
				}
			}
			// provisioner destroy-time added/removed
			oldDestroyProv := 0
			newDestroyProv := 0
			for _, p := range oldRes.Provisioners {
				if p == "destroy" {
					oldDestroyProv++
				}
			}
			for _, p := range newRes.Provisioners {
				if p == "destroy" {
					newDestroyProv++
				}
			}
			if oldDestroyProv != newDestroyProv {
				report.BreakingChanges = append(report.BreakingChanges, DifferenceItem{
					Category:    "LifecycleProvisioner",
					Name:        key,
					Severity:    SeverityBreaking,
					Description: fmt.Sprintf("Destroy-time provisioner changed for resource '%s'", key),
					OldValue:    fmt.Sprintf("%d", oldDestroyProv),
					NewValue:    fmt.Sprintf("%d", newDestroyProv),
					Remediation: "Review provisioner side effects before upgrading",
				})
			}
			// precondition / postcondition changes (#140)
			if !sameStrings(oldRes.Preconditions, newRes.Preconditions) {
				report.Warnings = append(report.Warnings, DifferenceItem{
					Category:    "PreconditionModified",
					Name:        key,
					Severity:    SeverityWarning,
					Description: fmt.Sprintf("precondition blocks changed for resource '%s'", key),
					OldValue:    strings.Join(oldRes.Preconditions, "; "),
					NewValue:    strings.Join(newRes.Preconditions, "; "),
					Remediation: "Verify resource preconditions remain valid",
				})
			}
			if !sameStrings(oldRes.Postconditions, newRes.Postconditions) {
				report.Warnings = append(report.Warnings, DifferenceItem{
					Category:    "PostconditionModified",
					Name:        key,
					Severity:    SeverityWarning,
					Description: fmt.Sprintf("postcondition blocks changed for resource '%s'", key),
					OldValue:    strings.Join(oldRes.Postconditions, "; "),
					NewValue:    strings.Join(newRes.Postconditions, "; "),
					Remediation: "Verify resource postconditions remain valid",
				})
			}
			// data-source selector/filter drift (#138)
			if s.isData {
				forceAttrs := map[string]bool{"cidr_block": true, "vpc_id": true, "availability_zone": true, "subnet_id": true, "ami": true, "engine": true, "location": true, "region": true, "project": true, "resource_group_name": true}
				for _, selAttr := range []string{"most_recent", "filter", "owners", "query", "sort"} {
					oldV, okO := oldRes.Attributes[selAttr]
					newV, okN := newRes.Attributes[selAttr]
					if !(okO && ((okN && oldV != newV) || !okN)) {
						continue
					}
					promote := false
					for _, res := range oldMod.ManagedResources {
						for attrName, attrVal := range res.Attributes {
							if strings.Contains(attrVal, key) && forceAttrs[attrName] {
								promote = true
							}
						}
					}
					sev := SeverityWarning
					if promote {
						sev = SeverityBreaking
					}
					di := DifferenceItem{
						Category:    "DataSelector",
						Name:        key + "." + selAttr,
						Severity:    sev,
						Description: fmt.Sprintf("Data source '%s' selector '%s' changed", key, selAttr),
						Remediation: "Verify resources referencing this data source remain compatible",
					}
					if okN {
						di.Description = fmt.Sprintf("Data source '%s' selector '%s' changed from '%s' to '%s'", key, selAttr, oldV, newV)
						di.OldValue = oldV
						di.NewValue = newV
					} else {
						di.Description = fmt.Sprintf("Data source '%s' selector '%s' removed", key, selAttr)
						di.OldValue = oldV
					}
					if promote {
						report.BreakingChanges = append(report.BreakingChanges, di)
					} else {
						report.Warnings = append(report.Warnings, di)
					}
				}
			}
		}
	}

	// Provider block configuration comparison (#117, #135): iterate union of keys.
	for pName, oldP := range oldMod.ProviderConfigs {
		newP, exists := newMod.ProviderConfigs[pName]
		if exists {
			for k, oldV := range oldP {
				if newV, ok := newP[k]; ok && oldV != newV {
					report.BreakingChanges = append(report.BreakingChanges, DifferenceItem{
						Category:    "ProviderConfig",
						Name:        pName + "." + k,
						Severity:    SeverityBreaking,
						Description: fmt.Sprintf("Provider '%s' configuration attribute '%s' changed from '%s' to '%s'", pName, k, oldV, newV),
						OldValue:    oldV,
						NewValue:    newV,
						Remediation: "Verify provider target region/account before applying",
						Evidence:    2,
					})
				} else if !ok {
					report.BreakingChanges = append(report.BreakingChanges, DifferenceItem{
						Category:    "ProviderConfig",
						Name:        pName + "." + k,
						Severity:    SeverityBreaking,
						Description: fmt.Sprintf("Provider '%s' configuration attribute '%s' removed", pName, k),
						OldValue:    oldV,
						Remediation: "Verify provider target region/account before applying",
						Evidence:    2,
					})
				}
			}
			for k, newV := range newP {
				if _, ok := oldP[k]; !ok {
					report.BreakingChanges = append(report.BreakingChanges, DifferenceItem{
						Category:    "ProviderConfig",
						Name:        pName + "." + k,
						Severity:    SeverityBreaking,
						Description: fmt.Sprintf("Provider '%s' configuration attribute '%s' added", pName, k),
						NewValue:    newV,
						Remediation: "Verify provider target region/account before applying",
						Evidence:    2,
					})
				}
			}
		} else {
			report.BreakingChanges = append(report.BreakingChanges, DifferenceItem{
				Category:    "ProviderConfigRemoved",
				Name:        pName,
				Severity:    SeverityBreaking,
				Description: fmt.Sprintf("Provider configuration block '%s' removed in upstream", pName),
				Remediation: "Verify provider configuration before applying",
				Evidence:    2,
			})
		}
	}
	for pName, newP := range newMod.ProviderConfigs {
		if _, exists := oldMod.ProviderConfigs[pName]; !exists {
			report.BreakingChanges = append(report.BreakingChanges, DifferenceItem{
				Category:    "ProviderConfigAdded",
				Name:        pName,
				Severity:    SeverityBreaking,
				Description: fmt.Sprintf("Provider configuration block '%s' added in upstream", pName),
				NewValue:    fmt.Sprintf("%v", newP),
				Remediation: "Verify provider configuration before applying",
				Evidence:    2,
			})
		}
	}

	// Locals comparison (#119, #139)
	forceAttrs := map[string]bool{"cidr_block": true, "vpc_id": true, "availability_zone": true, "subnet_id": true, "ami": true, "engine": true, "location": true, "region": true, "project": true, "resource_group_name": true}
	for lName, oldVal := range oldMod.Locals {
		if newVal, exists := newMod.Locals[lName]; exists && normalizeCondition(oldVal) != normalizeCondition(newVal) {
			promote := false
			for _, res := range oldMod.ManagedResources {
				for attrName, attrVal := range res.Attributes {
					if strings.Contains(attrVal, "local."+lName) && forceAttrs[attrName] {
						promote = true
					}
				}
			}
			if promote {
				report.BreakingChanges = append(report.BreakingChanges, DifferenceItem{
					Category:    "LocalValue",
					Name:        "local." + lName,
					Severity:    SeverityBreaking,
					Description: fmt.Sprintf("Local value 'local.%s' expression changed from '%s' to '%s' (feeds a ForceNew attribute)", lName, oldVal, newVal),
					OldValue:    oldVal,
					NewValue:    newVal,
					Remediation: "Verify resources and outputs referencing local." + lName,
				})
			} else {
				report.Warnings = append(report.Warnings, DifferenceItem{
					Category:    "LocalValue",
					Name:        "local." + lName,
					Severity:    SeverityWarning,
					Description: fmt.Sprintf("Local value 'local.%s' expression changed from '%s' to '%s'", lName, oldVal, newVal),
					OldValue:    oldVal,
					NewValue:    newVal,
					Remediation: "Verify resources and outputs referencing local." + lName,
				})
			}
		}
	}
}

// encryptionProviders returns the set of declared state-encryption key providers.
func encryptionProviders(blocks []ast.EncryptionBlockDef) []string {
	providers := make([]string, 0, len(blocks))
	for _, block := range blocks {
		providers = append(providers, block.KeyProvider)
	}
	return providers
}

// resourceFullAddress renders the canonical address string for a ResourceDef so it can be
// matched against ast.DetectAddressShifts output.
func resourceFullAddress(mode string, res ast.ResourceDef) string {
	if mode == "data" {
		return "data." + res.Type + "." + res.Name
	}
	return res.Type + "." + res.Name
}

func modeLabel(mode string) string {
	if mode == "data" {
		return "data"
	}
	return "managed"
}

// compareResources diffs the managed and data resources declared by oldMod and newMod.
// Removed resources become breaking changes, added resources become new features, and
// type/provider reassignment becomes a breaking change. Resources that are already
// reported as address shifts by ast.DetectAddressShifts are suppressed so a renamed
// resource is reported once.
func compareResources(oldMod, newMod *ast.ModuleInfo, report *DiffReport) {
	if oldMod == nil {
		oldMod = &ast.ModuleInfo{}
	}
	if newMod == nil {
		newMod = &ast.ModuleInfo{}
	}
	if oldMod.ManagedResources == nil {
		oldMod.ManagedResources = make(map[string]ast.ResourceDef)
	}
	if oldMod.DataResources == nil {
		oldMod.DataResources = make(map[string]ast.ResourceDef)
	}
	if newMod.ManagedResources == nil {
		newMod.ManagedResources = make(map[string]ast.ResourceDef)
	}
	if newMod.DataResources == nil {
		newMod.DataResources = make(map[string]ast.ResourceDef)
	}

	// Address shifts are computed once (#125) and unmigrated recreations fold into BreakingChanges (#111).
	shiftedOld := make(map[string]struct{})
	shiftedNew := make(map[string]struct{})
	shifts := ast.DetectAddressShifts(oldMod, newMod)
	report.AddressShifts = shifts
	for _, sh := range shifts {
		// Only suppress when the resource is renamed/recreated at a new address;
		// a true removal (empty new address) is still reported as a removed resource.
		if sh.NewAddress.FullAddress() != "" {
			shiftedOld[sh.OldAddress.FullAddress()] = struct{}{}
			shiftedNew[sh.NewAddress.FullAddress()] = struct{}{}
			if sh.IsRecreation {
				report.BreakingChanges = append(report.BreakingChanges, DifferenceItem{
					Category:    "AddressShift",
					Name:        sh.OldAddress.FullAddress(),
					Severity:    SeverityBreaking,
					Description: sh.Reason,
					OldValue:    sh.OldAddress.FullAddress(),
					NewValue:    sh.NewAddress.FullAddress(),
					Remediation: fmt.Sprintf("Add a moved { from = %s to = %s } block to migrate state without recreation", sh.OldAddress.FullAddress(), sh.NewAddress.FullAddress()),
					Evidence:    2,
				})
			}
		}
	}

	type resourceSet struct {
		mode string
		old  map[string]ast.ResourceDef
		new  map[string]ast.ResourceDef
	}
	sets := []resourceSet{
		{"managed", oldMod.ManagedResources, newMod.ManagedResources},
		{"data", oldMod.DataResources, newMod.DataResources},
	}

	for _, rs := range sets {
		consumedNew := make(map[string]bool)

		// Removed / changed resources
		for key, oldRes := range rs.old {
			fullOld := resourceFullAddress(rs.mode, oldRes)
			if _, shifted := shiftedOld[fullOld]; shifted {
				continue
			}

			// Exact key match
			if newRes, ok := rs.new[key]; ok {
				if oldRes.Provider != "" && newRes.Provider != "" && oldRes.Provider != newRes.Provider {
					report.BreakingChanges = append(report.BreakingChanges, DifferenceItem{
						Category:    "ResourceProvider",
						Name:        key,
						Severity:    SeverityBreaking,
						Description: fmt.Sprintf("Resource '%s' provider changed from '%s' to '%s'", key, oldRes.Provider, newRes.Provider),
						OldValue:    oldRes.Provider,
						NewValue:    newRes.Provider,
						Remediation: "Update provider configuration for this resource",
						Pos:         oldRes.Pos,
					})
				}
				consumedNew[key] = true
				continue
			}

			// Same label, different type -> type reassignment
			reassigned := false
			for nKey, newRes := range rs.new {
				if consumedNew[nKey] {
					continue
				}
				if newRes.Name != "" && newRes.Name == oldRes.Name && newRes.Type != oldRes.Type {
					report.BreakingChanges = append(report.BreakingChanges, DifferenceItem{
						Category:    "ResourceProvider",
						Name:        key,
						Severity:    SeverityBreaking,
						Description: fmt.Sprintf("Resource '%s' type changed from '%s' to '%s'", oldRes.Name, oldRes.Type, newRes.Type),
						OldValue:    oldRes.Type,
						NewValue:    newRes.Type,
						Remediation: "Update resource type and migrate state before upgrading",
						Pos:         oldRes.Pos,
					})
					consumedNew[nKey] = true
					reassigned = true
					break
				}
			}
			if reassigned {
				continue
			}

			// Removed
			report.BreakingChanges = append(report.BreakingChanges, DifferenceItem{
				Category:    "Resource",
				Name:        key,
				Severity:    SeverityBreaking,
				Description: fmt.Sprintf("%s resource '%s' was removed in upstream version", modeLabel(rs.mode), key),
				OldValue:    fmt.Sprintf("type=%s, provider=%s", oldRes.Type, oldRes.Provider),
				Remediation: "Remove resource or migrate state before upgrading",
				Pos:         oldRes.Pos,
			})
		}

		// Added resources
		for key, newRes := range rs.new {
			if consumedNew[key] {
				continue
			}
			fullNew := resourceFullAddress(rs.mode, newRes)
			if _, shifted := shiftedNew[fullNew]; shifted {
				continue
			}
			// skip if it was a type-reassignment target (consumed already handled above via consumedNew)
			report.NewFeatures = append(report.NewFeatures, DifferenceItem{
				Category:    "Resource",
				Name:        key,
				Severity:    SeverityNewFeature,
				Description: fmt.Sprintf("New %s resource '%s' available in upstream", modeLabel(rs.mode), key),
				NewValue:    fmt.Sprintf("type=%s, provider=%s", newRes.Type, newRes.Provider),
				Remediation: "Consider exposing this resource in wrapper if needed",
				Pos:         newRes.Pos,
			})
		}
	}
}

// compareModuleCalls diffs nested module {} blocks declared inside oldMod and newMod (#112).
func compareModuleCalls(oldMod, newMod *ast.ModuleInfo, report *DiffReport) {
	for name, oldCall := range oldMod.ModuleCalls {
		newCall, exists := newMod.ModuleCalls[name]
		if !exists {
			report.BreakingChanges = append(report.BreakingChanges, DifferenceItem{
				Category:    "ModuleCall",
				Name:        "module." + name,
				Severity:    SeverityBreaking,
				Description: fmt.Sprintf("Nested module call 'module.%s' was removed in upstream version (destroys child module state)", name),
				OldValue:    oldCall.Source,
				Remediation: fmt.Sprintf("Add a moved {} or removed {} block for 'module.%s' before upgrading", name),
				Pos:         oldCall.Pos,
				Evidence:    2,
			})
			continue
		}
		if oldCall.Source != "" && newCall.Source != "" && oldCall.Source != newCall.Source {
			report.BreakingChanges = append(report.BreakingChanges, DifferenceItem{
				Category:    "ModuleCallSource",
				Name:        "module." + name,
				Severity:    SeverityBreaking,
				Description: fmt.Sprintf("Nested module call 'module.%s' source changed from '%s' to '%s'", name, oldCall.Source, newCall.Source),
				OldValue:    oldCall.Source,
				NewValue:    newCall.Source,
				Remediation: fmt.Sprintf("Verify child module state compatibility for 'module.%s'", name),
				Pos:         newCall.Pos,
				Evidence:    2,
			})
		}
		// version constraint bump (#134)
		if oldCall.Version != "" && newCall.Version != "" && oldCall.Version != newCall.Version {
			oldMaj, _ := majorFromConstraint(oldCall.Version)
			newMaj, _ := majorFromConstraint(newCall.Version)
			breaking := false
			if oldMaj != 0 && newMaj > oldMaj {
				breaking = true
			}
			if isZeroMajorBreakingBump(oldCall.Version, newCall.Version) {
				breaking = true
			}
			if oldUpper, okO := upperBoundMajor(oldCall.Version); okO {
				if newUpper, okN := upperBoundMajor(newCall.Version); okN && newUpper < oldUpper {
					breaking = true
				}
			}
			if breaking {
				report.BreakingChanges = append(report.BreakingChanges, DifferenceItem{
					Category:    "ModuleCallVersion",
					Name:        "module." + name,
					Severity:    SeverityBreaking,
					Description: fmt.Sprintf("Nested module call 'module.%s' version constraint changed from '%s' to '%s'", name, oldCall.Version, newCall.Version),
					OldValue:    oldCall.Version,
					NewValue:    newCall.Version,
					Remediation: fmt.Sprintf("Review child module migration guide for 'module.%s'", name),
					Pos:         newCall.Pos,
					Evidence:    2,
				})
			} else {
				report.Warnings = append(report.Warnings, DifferenceItem{
					Category:    "ModuleCallVersion",
					Name:        "module." + name,
					Severity:    SeverityWarning,
					Description: fmt.Sprintf("Nested module call 'module.%s' version constraint updated from '%s' to '%s'", name, oldCall.Version, newCall.Version),
					OldValue:    oldCall.Version,
					NewValue:    newCall.Version,
					Remediation: fmt.Sprintf("Verify child module compatibility for 'module.%s'", name),
					Pos:         newCall.Pos,
				})
			}
		}
		if (oldCall.Count != "") != (newCall.Count != "") || (oldCall.ForEach != "") != (newCall.ForEach != "") {
			report.BreakingChanges = append(report.BreakingChanges, DifferenceItem{
				Category:    "ModuleCallIndex",
				Name:        "module." + name,
				Severity:    SeverityBreaking,
				Description: fmt.Sprintf("Nested module call 'module.%s' instance indexing mode changed (count/for_each transition)", name),
				Remediation: fmt.Sprintf("Add moved {} blocks for indexed instances of 'module.%s'", name),
				Pos:         newCall.Pos,
				Evidence:    2,
			})
		}
		// cardinality expression changes (#137)
		if oldCall.Count != "" && newCall.Count != "" && oldCall.Count != newCall.Count {
			oldN, errOld := strconv.Atoi(oldCall.Count)
			newN, errNew := strconv.Atoi(newCall.Count)
			if (errOld == nil && errNew == nil && newN < oldN) || newCall.Count == "0" {
				report.BreakingChanges = append(report.BreakingChanges, DifferenceItem{
					Category:    "ModuleCallCount",
					Name:        "module." + name,
					Severity:    SeverityBreaking,
					Description: fmt.Sprintf("Nested module call 'module.%s' count reduced from %s to %s", name, oldCall.Count, newCall.Count),
					OldValue:    oldCall.Count,
					NewValue:    newCall.Count,
					Remediation: fmt.Sprintf("Verify child module instance removal for 'module.%s'", name),
					Evidence:    2,
				})
			} else {
				report.Warnings = append(report.Warnings, DifferenceItem{
					Category:    "ModuleCallCount",
					Name:        "module." + name,
					Severity:    SeverityWarning,
					Description: fmt.Sprintf("Nested module call 'module.%s' count expression changed from '%s' to '%s'", name, oldCall.Count, newCall.Count),
					OldValue:    oldCall.Count,
					NewValue:    newCall.Count,
					Remediation: fmt.Sprintf("Verify child module instance count for 'module.%s'", name),
				})
			}
		}
		if oldCall.ForEach != "" && newCall.ForEach != "" && oldCall.ForEach != newCall.ForEach {
			report.BreakingChanges = append(report.BreakingChanges, DifferenceItem{
				Category:    "ModuleCallForEach",
				Name:        "module." + name,
				Severity:    SeverityBreaking,
				Description: fmt.Sprintf("Nested module call 'module.%s' for_each expression changed from '%s' to '%s'", name, oldCall.ForEach, newCall.ForEach),
				OldValue:    oldCall.ForEach,
				NewValue:    newCall.ForEach,
				Remediation: fmt.Sprintf("Verify child module instance keys for 'module.%s'", name),
				Evidence:    2,
			})
		}
	}
	for name, newCall := range newMod.ModuleCalls {
		if _, exists := oldMod.ModuleCalls[name]; !exists {
			report.NewFeatures = append(report.NewFeatures, DifferenceItem{
				Category:    "ModuleCall",
				Name:        "module." + name,
				Severity:    SeverityNewFeature,
				Description: fmt.Sprintf("New nested module call 'module.%s' added in upstream", name),
				NewValue:    newCall.Source,
				Pos:         newCall.Pos,
			})
		}
	}
}
