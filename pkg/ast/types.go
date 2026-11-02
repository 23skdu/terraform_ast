package ast

import (
	"fmt"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/terraform-config-inspect/tfconfig"
)

// VariableDef represents an input variable in a Terraform module.
type VariableDef struct {
	Name            string             `json:"name"`
	Type            string             `json:"type"`
	Description     string             `json:"description"`
	Required        bool               `json:"required"`
	Default         interface{}        `json:"default,omitempty"`
	HasDefault      bool               `json:"has_default"`
	Sensitive       bool               `json:"sensitive"`
	Nullable        *bool              `json:"nullable,omitempty"`
	ValidationRules []string           `json:"validation_rules,omitempty"`
	Deprecated      string             `json:"deprecated,omitempty"`
	Pos             tfconfig.SourcePos `json:"pos"`
}

// OutputDef represents an output exposed by a Terraform module.
type OutputDef struct {
	Name           string             `json:"name"`
	Description    string             `json:"description"`
	Sensitive      bool               `json:"sensitive"`
	Type           string             `json:"type,omitempty"`
	Deprecated     string             `json:"deprecated,omitempty"`
	Value          string             `json:"value,omitempty"`
	Preconditions  []string           `json:"preconditions,omitempty"`
	Postconditions []string           `json:"postconditions,omitempty"`
	Pos            tfconfig.SourcePos `json:"pos"`
}

// ModuleCallDef represents a child module instantiation (e.g. `module "gke"` or `module "eks"`).
type ModuleCallDef struct {
	Name             string             `json:"name"`
	Source           string             `json:"source"`
	Version          string             `json:"version,omitempty"`
	Count            string             `json:"count,omitempty"`
	ForEach          string             `json:"for_each,omitempty"`
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
	Name                 string   `json:"name"`
	Source               string   `json:"source,omitempty"`
	VersionConstraints   []string `json:"version_constraints,omitempty"`
	ConfigurationAliases []string `json:"configuration_aliases,omitempty"`
}

// RemovedBlockDef captures Terraform 1.7+ / OpenTofu 1.6+ removed {} blocks.
type RemovedBlockDef struct {
	From    string             `json:"from"`
	Destroy bool               `json:"destroy"`
	Pos     tfconfig.SourcePos `json:"pos"`
}

// MovedBlockDef captures Terraform 1.1+ moved {} blocks shipped by upstream.
type MovedBlockDef struct {
	From string             `json:"from"`
	To   string             `json:"to"`
	Pos  tfconfig.SourcePos `json:"pos"`
}

// CheckBlockDef captures Terraform 1.5+ check {} blocks and assertions.
type CheckBlockDef struct {
	Name        string             `json:"name"`
	AssertRules []string           `json:"assert_rules,omitempty"`
	Pos         tfconfig.SourcePos `json:"pos"`
}

// ImportBlockDef captures Terraform 1.5+ programmatic import {} blocks.
type ImportBlockDef struct {
	To       string             `json:"to"`
	ID       string             `json:"id"`
	Provider string             `json:"provider,omitempty"`
	Pos      tfconfig.SourcePos `json:"pos"`
}

// FunctionDef captures Terraform 1.8+ custom function blocks.
type FunctionDef struct {
	Name       string             `json:"name"`
	Summary    string             `json:"summary,omitempty"`
	ReturnType string             `json:"return_type,omitempty"`
	Pos        tfconfig.SourcePos `json:"pos"`
}

// EncryptionBlockDef captures OpenTofu 1.7+ state encryption blocks.
type EncryptionBlockDef struct {
	KeyProvider string             `json:"key_provider"`
	Pos         tfconfig.SourcePos `json:"pos"`
}

// ResourceDef captures managed and data resource definitions in a Terraform module.
type ResourceDef struct {
	Mode         string             `json:"mode"` // "managed" or "data"
	Type         string             `json:"type"`
	Name         string             `json:"name"`
	Provider     string             `json:"provider,omitempty"`
	Count        string             `json:"count,omitempty"`
	ForEach      string             `json:"for_each,omitempty"`
	Lifecycle    LifecycleFlags     `json:"lifecycle,omitempty"`
	Provisioners []string           `json:"provisioners,omitempty"`
	Triggers       map[string]string  `json:"triggers,omitempty"`
	Attributes     map[string]string  `json:"attributes,omitempty"`
	Preconditions  []string           `json:"preconditions,omitempty"`
	Postconditions []string           `json:"postconditions,omitempty"`
	Pos            tfconfig.SourcePos `json:"pos"`
}

// ModuleInfo is the normalized AST representation of a Terraform module.
type ModuleInfo struct {
	Path                    string                         `json:"path"`
	Variables               map[string]VariableDef         `json:"variables"`
	Outputs                 map[string]OutputDef           `json:"outputs"`
	ModuleCalls             map[string]ModuleCallDef       `json:"module_calls"`
	RequiredCore            []string                       `json:"required_core,omitempty"`
	RequiredProviders       map[string]ProviderRequirement `json:"required_providers,omitempty"`
	WrapperOutputReferences map[string][]OutputReference   `json:"wrapper_output_references,omitempty"`
	RemovedBlocks           []RemovedBlockDef              `json:"removed_blocks,omitempty"`
	MovedBlocks             []MovedBlockDef                `json:"moved_blocks,omitempty"`
	CheckBlocks             map[string]CheckBlockDef       `json:"check_blocks,omitempty"`
	ImportBlocks            []ImportBlockDef               `json:"import_blocks,omitempty"`
	Functions               map[string]FunctionDef         `json:"functions,omitempty"`
	EncryptionBlocks        []EncryptionBlockDef           `json:"encryption_blocks,omitempty"`
	ManagedResources        map[string]ResourceDef         `json:"managed_resources,omitempty"`
	DataResources           map[string]ResourceDef         `json:"data_resources,omitempty"`
	Backend                 *BackendConfig                 `json:"backend,omitempty"`
	Cloud                   *BackendConfig                 `json:"cloud,omitempty"`
	ProviderConfigs         map[string]map[string]string   `json:"provider_configs,omitempty"`
	Locals                  map[string]string              `json:"locals,omitempty"`
	ParsedBodies            map[string]*hclsyntax.Body     `json:"-"`
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

	// Deep Object Schema Comparison
	if isObjectOrCollectionOfObject(normOld) && isObjectOrCollectionOfObject(normNew) {
		containerOld, rawObjOld := splitObjectContainer(normOld)
		containerNew, rawObjNew := splitObjectContainer(normNew)

		if containerOld != containerNew {
			return TypeCompatibilityResult{
				Compatible: false,
				IsBreaking: true,
				Reason:     fmt.Sprintf("Container type changed incompatibly from '%s' to '%s'", containerOld, containerNew),
			}
		}

		oldAttrs, okOld := ParseObjectSchema(rawObjOld)
		newAttrs, okNew := ParseObjectSchema(rawObjNew)
		if okOld && okNew {
			return CheckObjectCompatibility(oldAttrs, newAttrs)
		}
	}

	// Incompatible primitives or mismatched complex structures
	return TypeCompatibilityResult{
		Compatible: false,
		IsBreaking: true,
		Reason:     fmt.Sprintf("Type constraint changed incompatibly from '%s' to '%s'", oldType, newType),
	}
}

// ObjectAttribute represents an attribute defined within an object schema.
type ObjectAttribute struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	IsOptional bool   `json:"is_optional"`
}

func isObjectOrCollectionOfObject(t string) bool {
	return strings.Contains(t, "object({")
}

func splitObjectContainer(t string) (container string, rawObj string) {
	if strings.HasPrefix(t, "object({") {
		return "object", t
	}
	for _, prefix := range []string{"list(", "set(", "map("} {
		if strings.HasPrefix(t, prefix) && strings.HasSuffix(t, ")") {
			inner := strings.TrimSuffix(strings.TrimPrefix(t, prefix), ")")
			if strings.HasPrefix(inner, "object({") {
				return strings.TrimSuffix(prefix, "("), inner
			}
		}
	}
	return "unknown", t
}

// ParseObjectSchema parses an HCL object type expression like object({ foo = string, bar = optional(number) })
func ParseObjectSchema(t string) (map[string]ObjectAttribute, bool) {
	start := strings.Index(t, "object({")
	if start == -1 {
		return nil, false
	}
	end := strings.LastIndex(t, "})")
	if end == -1 || end <= start+8 {
		return nil, false
	}
	inner := t[start+8 : end]
	attrs := make(map[string]ObjectAttribute)

	parts := splitObjectAttributes(inner)
	for _, part := range parts {
		eqIdx := strings.Index(part, "=")
		if eqIdx == -1 {
			continue
		}
		name := strings.TrimSpace(part[:eqIdx])
		typeExpr := strings.TrimSpace(part[eqIdx+1:])

		isOptional := false
		if strings.HasPrefix(typeExpr, "optional(") && strings.HasSuffix(typeExpr, ")") {
			isOptional = true
			typeExpr = strings.TrimSuffix(strings.TrimPrefix(typeExpr, "optional("), ")")
			// Handle optional(type, default)
			if commaIdx := strings.Index(typeExpr, ","); commaIdx != -1 {
				typeExpr = strings.TrimSpace(typeExpr[:commaIdx])
			}
		}
		attrs[name] = ObjectAttribute{
			Name:       name,
			Type:       typeExpr,
			IsOptional: isOptional,
		}
	}
	return attrs, true
}

func splitObjectAttributes(inner string) []string {
	var parts []string
	var current strings.Builder
	depth := 0

	for _, ch := range inner {
		switch ch {
		case '(', '{', '[':
			depth++
			current.WriteRune(ch)
		case ')', '}', ']':
			depth--
			current.WriteRune(ch)
		case ',', '\n':
			if depth == 0 {
				part := strings.TrimSpace(current.String())
				if part != "" {
					parts = append(parts, part)
				}
				current.Reset()
			} else {
				current.WriteRune(ch)
			}
		default:
			current.WriteRune(ch)
		}
	}
	if current.Len() > 0 {
		part := strings.TrimSpace(current.String())
		if part != "" {
			parts = append(parts, part)
		}
	}
	return parts
}

// CheckObjectCompatibility compares attributes between two object schemas.
func CheckObjectCompatibility(oldAttrs, newAttrs map[string]ObjectAttribute) TypeCompatibilityResult {
	var reasons []string

	// 1. Check for removed attributes from object schema in sorted order
	var oldNames []string
	for name := range oldAttrs {
		oldNames = append(oldNames, name)
	}
	sort.Strings(oldNames)

	for _, name := range oldNames {
		if _, exists := newAttrs[name]; !exists {
			reasons = append(reasons, fmt.Sprintf("Attribute '%s' was removed from object schema", name))
		}
	}

	// 2. Check for newly required attributes added to object schema in sorted order
	var newNames []string
	for name := range newAttrs {
		newNames = append(newNames, name)
	}
	sort.Strings(newNames)

	for _, name := range newNames {
		newAttr := newAttrs[name]
		oldAttr, exists := oldAttrs[name]
		if !exists {
			if !newAttr.IsOptional {
				reasons = append(reasons, fmt.Sprintf("New required attribute '%s' added to object schema", name))
			}
			continue
		}

		// Was optional, now required
		if oldAttr.IsOptional && !newAttr.IsOptional {
			reasons = append(reasons, fmt.Sprintf("Object attribute '%s' changed from optional to required", name))
		}

		// Compare inner attribute types
		subCompat := CheckTypeCompatibility(oldAttr.Type, newAttr.Type)
		if subCompat.IsBreaking {
			reasons = append(reasons, fmt.Sprintf("Object attribute '%s' type changed incompatibly: %s", name, subCompat.Reason))
		}
	}

	if len(reasons) > 0 {
		return TypeCompatibilityResult{
			Compatible: false,
			IsBreaking: true,
			Reason:     strings.Join(reasons, "; "),
		}
	}

	return TypeCompatibilityResult{
		Compatible: true,
		IsBreaking: false,
		Reason:     "Object schemas are compatible",
	}
}

// LifecycleFlags captures the lifecycle toggles that control destruction behaviour.
type LifecycleFlags struct {
	PreventDestroy      bool     `json:"prevent_destroy,omitempty"`
	CreateBeforeDestroy bool     `json:"create_before_destroy,omitempty"`
	ReplaceTriggeredBy  []string `json:"replace_triggered_by,omitempty"`
	IgnoreChanges       []string `json:"ignore_changes,omitempty"`
}

// BackendConfig captures the state backend type and its literal attributes.
type BackendConfig struct {
	Type       string            `json:"type,omitempty"`
	Attributes map[string]string `json:"attributes,omitempty"`
}

// ModuleInfo lifecycle/backend/provisioner fields are stored below via enrichment.

// HazardLookup is an optional interface for schema-driven ForceNew attribute detection.
// Implementations (e.g. recreation.DynamicHazardRegistry) report whether an attribute is
// immutable (requires replacement) for a given resource attribute key.
type HazardLookup interface {
	LookupHazard(key string) (bool, string, string)
}
