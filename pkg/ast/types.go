package ast

import (
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-config-inspect/tfconfig"
)

// VariableDef represents an input variable in a Terraform module.
type VariableDef struct {
	Name        string              `json:"name"`
	Type        string              `json:"type"`
	Description string              `json:"description"`
	Required    bool                `json:"required"`
	Default     interface{}         `json:"default,omitempty"`
	HasDefault  bool                `json:"has_default"`
	Sensitive   bool                `json:"sensitive"`
	Deprecated  string              `json:"deprecated,omitempty"`
	Pos         tfconfig.SourcePos  `json:"pos"`
}

// OutputDef represents an output exposed by a Terraform module.
type OutputDef struct {
	Name        string              `json:"name"`
	Description string              `json:"description"`
	Sensitive   bool                `json:"sensitive"`
	Type        string              `json:"type,omitempty"`
	Deprecated  string              `json:"deprecated,omitempty"`
	Pos         tfconfig.SourcePos  `json:"pos"`
}

// ModuleCallDef represents a child module instantiation (e.g. `module "gke"` or `module "eks"`).
type ModuleCallDef struct {
	Name             string             `json:"name"`
	Source           string             `json:"source"`
	Version          string             `json:"version,omitempty"`
	PassedInputs     []string           `json:"passed_inputs"`
	InputExpressions map[string]string  `json:"input_expressions,omitempty"`
	Pos              tfconfig.SourcePos `json:"pos"`
}

// OutputReference tracks if a wrapper output delegates directly to a child module output.
type OutputReference struct {
	OutputName   string `json:"output_name"`
	TargetModule string `json:"target_module"`
	TargetOutput string `json:"target_output"`
}

// ProviderRequirement captures provider name and constraint.
type ProviderRequirement struct {
	Name               string   `json:"name"`
	Source             string   `json:"source,omitempty"`
	VersionConstraints []string `json:"version_constraints,omitempty"`
}

// ModuleInfo is the normalized AST representation of a Terraform module.
type ModuleInfo struct {
	Path                     string                         `json:"path"`
	Variables                map[string]VariableDef         `json:"variables"`
	Outputs                  map[string]OutputDef           `json:"outputs"`
	ModuleCalls              map[string]ModuleCallDef       `json:"module_calls"`
	RequiredCore             []string                       `json:"required_core,omitempty"`
	RequiredProviders        map[string]ProviderRequirement `json:"required_providers,omitempty"`
	WrapperOutputReferences  map[string]OutputReference     `json:"wrapper_output_references,omitempty"`
}

// TypeCompatibilityResult describes the compatibility between two type constraints.
type TypeCompatibilityResult struct {
	Compatible bool   `json:"compatible"`
	IsBreaking bool   `json:"is_breaking"`
	Reason     string `json:"reason"`
}

// NormalizeType simplifies type constraint strings for reliable comparison.
func NormalizeType(t string) string {
	cleaned := strings.TrimSpace(t)
	if cleaned == "" {
		return "any"
	}
	// Remove outer whitespace and canonicalize common aliases
	cleaned = strings.ToLower(cleaned)
	cleaned = strings.ReplaceAll(cleaned, " ", "")
	return cleaned
}

// CheckTypeCompatibility compares an old type constraint with a new type constraint.
func CheckTypeCompatibility(oldType, newType string) TypeCompatibilityResult {
	normOld := NormalizeType(oldType)
	normNew := NormalizeType(newType)

	if normOld == normNew {
		return TypeCompatibilityResult{
			Compatible: true,
			IsBreaking: false,
			Reason:     "Types are identical",
		}
	}

	// If old was any, restricting to a concrete type in new is a breaking change
	if normOld == "any" && normNew != "any" {
		return TypeCompatibilityResult{
			Compatible: false,
			IsBreaking: true,
			Reason:     fmt.Sprintf("Type restricted from 'any' to '%s'", newType),
		}
	}

	// If old was concrete and new is any, this is widening (compatible)
	if normOld != "any" && normNew == "any" {
		return TypeCompatibilityResult{
			Compatible: true,
			IsBreaking: false,
			Reason:     fmt.Sprintf("Type widened from '%s' to 'any'", oldType),
		}
	}

	// List to Set conversion: In Terraform, list is automatically converted to set
	if strings.HasPrefix(normOld, "list(") && strings.HasPrefix(normNew, "set(") {
		innerOld := strings.TrimSuffix(strings.TrimPrefix(normOld, "list("), ")")
		innerNew := strings.TrimSuffix(strings.TrimPrefix(normNew, "set("), ")")
		if innerOld == innerNew {
			return TypeCompatibilityResult{
				Compatible: true,
				IsBreaking: false,
				Reason:     "Type converted from list to set (Terraform automatically coerces list to set, but ordering is not preserved)",
			}
		}
	}

	// Set to List conversion: Requires explicit list or ordering
	if strings.HasPrefix(normOld, "set(") && strings.HasPrefix(normNew, "list(") {
		return TypeCompatibilityResult{
			Compatible: false,
			IsBreaking: true,
			Reason:     "Type converted from set to list; sets cannot automatically preserve list order",
		}
	}

	// Incompatible primitives or mismatched complex structures
	return TypeCompatibilityResult{
		Compatible: false,
		IsBreaking: true,
		Reason:     fmt.Sprintf("Type constraint changed incompatibly from '%s' to '%s'", oldType, newType),
	}
}
