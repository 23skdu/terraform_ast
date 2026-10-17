package diff

import (
	"fmt"
	"path/filepath"
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
		report.BreakingChanges[i].ConfidenceScore = 9
	}
	for i := range report.NewFeatures {
		report.NewFeatures[i].ConfidenceScore = 9
	}
	for i := range report.Warnings {
		switch report.Warnings[i].Category {
		case "TerraformCore", "ProviderRequirement":
			report.Warnings[i].ConfidenceScore = 7
		case "VariableValidation":
			report.Warnings[i].ConfidenceScore = 6
		case "VariableType", "OutputType":
			report.Warnings[i].ConfidenceScore = 8
		default:
			report.Warnings[i].ConfidenceScore = 9
		}
	}
}

func makeStringSet(values []string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	return set
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	leftSet := makeStringSet(left)
	rightSet := makeStringSet(right)
	if len(leftSet) != len(rightSet) {
		return false
	}
	for value := range leftSet {
		if _, exists := rightSet[value]; !exists {
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

func compareModernBlocks(oldMod, newMod *ast.ModuleInfo, report *DiffReport) {
	// 1. Removed blocks (Terraform 1.7+ / OpenTofu 1.6+)
	for _, rb := range newMod.RemovedBlocks {
		if !rb.Destroy {
			report.NewFeatures = append(report.NewFeatures, DifferenceItem{
				Category:        "SoftRemovedBlock",
				Name:            rb.From,
				Severity:        SeverityNewFeature,
				ConfidenceScore: 9,
				Description:     fmt.Sprintf("Resource or block '%s' declared in removed block with lifecycle.destroy = false (soft migration)", rb.From),
			})
		}
	}

	// 2. Check blocks (Terraform 1.5+)
	for name, cb := range newMod.CheckBlocks {
		if _, existed := oldMod.CheckBlocks[name]; !existed {
			report.NewFeatures = append(report.NewFeatures, DifferenceItem{
				Category:        "CheckBlockAdded",
				Name:            name,
				Severity:        SeverityNewFeature,
				ConfidenceScore: 9,
				Description:     fmt.Sprintf("Check assertion block '%s' declared with %d rules", name, len(cb.AssertRules)),
			})
		}
	}

	// 3. Import blocks (Terraform 1.5+). The resource address is what makes an import new; a
	// changed id on an address that was already imported is not a new capability.
	oldImports := makeStringSet(importAddresses(oldMod.ImportBlocks))
	for _, ib := range newMod.ImportBlocks {
		if _, existed := oldImports[ib.To]; existed {
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

	// 4. Custom Functions (Terraform 1.8+)
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

	// 5. Encryption blocks (OpenTofu 1.7+). Keying on the key provider also surfaces a swap of
	// the encryption backend, which is a configuration change the operator must act on.
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
}

// importAddresses returns the set of resource addresses that are programmatically imported.
func importAddresses(blocks []ast.ImportBlockDef) []string {
	addresses := make([]string, 0, len(blocks))
	for _, block := range blocks {
		addresses = append(addresses, block.To)
	}
	return addresses
}

// encryptionProviders returns the set of declared state-encryption key providers.
func encryptionProviders(blocks []ast.EncryptionBlockDef) []string {
	providers := make([]string, 0, len(blocks))
	for _, block := range blocks {
		providers = append(providers, block.KeyProvider)
	}
	return providers
}
