package recreation

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/23skdu/terraform_ast/pkg/diff"
	"github.com/23skdu/terraform_ast/pkg/wrapper"
)

// RecreationHazard describes a change that triggers Terraform resource destruction/re-creation (ForceNew).
type RecreationHazard struct {
	Variable    string `json:"variable"`
	Resource    string `json:"resource"`
	HazardType  string `json:"hazard_type"`
	Severity    string `json:"severity"`
	Description string `json:"description"`
	Impact      string `json:"impact"`
	Remediation string `json:"remediation"`
}

// HazardDescriptor holds metadata about immutable arguments for a provider resource.
type HazardDescriptor struct {
	Resource    string
	Impact      string
	Remediation string
}

var knownModuleHazards = map[string]map[string]HazardDescriptor{
	// Google GKE module hazards
	"gke": {
		"name": {
			Resource:    "google_container_cluster",
			Impact:      "Cluster name is immutable; changing or removing it triggers complete cluster destruction and re-creation.",
			Remediation: "Ensure cluster name matches existing state exactly or plan for full cluster rebuild and workload migration.",
		},
		"project_id": {
			Resource:    "google_container_cluster",
			Impact:      "GCP Project ID cannot be changed in-place; triggers ForceNew cluster destruction.",
			Remediation: "Keep project_id pinned to the existing project.",
		},
		"location": {
			Resource:    "google_container_cluster",
			Impact:      "Cluster location/region is immutable; forces cluster re-creation.",
			Remediation: "Do not change location between regional and zonal or across regions.",
		},
		"region": {
			Resource:    "google_container_cluster",
			Impact:      "Cluster region cannot be migrated in-place; triggers cluster re-creation.",
			Remediation: "Preserve regional setting across upgrades.",
		},
		"network": {
			Resource:    "google_container_cluster",
			Impact:      "VPC network attachment cannot be changed in-place; triggers ForceNew cluster destruction.",
			Remediation: "Maintain network binding or provision a separate parallel cluster.",
		},
		"subnetwork": {
			Resource:    "google_container_cluster",
			Impact:      "Subnetwork attachment is immutable; altering it destroys and recreates the cluster.",
			Remediation: "Keep subnetwork configuration unchanged.",
		},
		"ip_range_pods": {
			Resource:    "google_container_cluster / node_pools",
			Impact:      "Secondary pod CIDR range cannot be altered after cluster creation; triggers ForceNew recreation.",
			Remediation: "Do not alter or re-type ip_range_pods on an active production cluster.",
		},
		"ip_range_services": {
			Resource:    "google_container_cluster",
			Impact:      "Services secondary CIDR range is immutable; removing or modifying it triggers cluster destruction.",
			Remediation: "Retain services CIDR configuration.",
		},
	},
	// AWS EKS module hazards
	"eks": {
		"name": {
			Resource:    "aws_eks_cluster",
			Impact:      "EKS cluster name is immutable; modifying it triggers complete cluster destruction.",
			Remediation: "Keep cluster name unchanged to prevent control plane recreation.",
		},
		"cluster_name": {
			Resource:    "aws_eks_cluster",
			Impact:      "EKS cluster name is immutable; modifying it triggers complete cluster destruction.",
			Remediation: "Ensure cluster_name remains constant.",
		},
		"vpc_id": {
			Resource:    "aws_eks_cluster",
			Impact:      "VPC ID attachment cannot be modified in-place; triggers ForceNew cluster destruction.",
			Remediation: "Do not change the underlying VPC attachment for an active EKS cluster.",
		},
		"cluster_ip_family": {
			Resource:    "aws_eks_cluster",
			Impact:      "IP family (ipv4/ipv6) is immutable on AWS EKS; triggers ForceNew cluster destruction.",
			Remediation: "Do not alter cluster_ip_family on an existing cluster.",
		},
	},
	// Azure AKS module hazards
	"aks": {
		"cluster_name": {
			Resource:    "azurerm_kubernetes_cluster",
			Impact:      "AKS cluster name is immutable; modifying it triggers complete cluster destruction.",
			Remediation: "Ensure cluster_name remains constant.",
		},
		"resource_group_name": {
			Resource:    "azurerm_kubernetes_cluster",
			Impact:      "Resource group cannot be migrated in-place; triggers ForceNew cluster destruction.",
			Remediation: "Keep resource_group_name unchanged.",
		},
		"location": {
			Resource:    "azurerm_kubernetes_cluster",
			Impact:      "Azure region location is immutable; triggers ForceNew cluster recreation.",
			Remediation: "Do not alter location for an active cluster.",
		},
		"dns_prefix": {
			Resource:    "azurerm_kubernetes_cluster",
			Impact:      "DNS prefix is immutable; altering it triggers AKS cluster re-creation.",
			Remediation: "Maintain constant dns_prefix across upgrades.",
		},
	},
}

// genericHazards are immutable on essentially every cloud resource, so they apply whatever the
// module manages. They deliberately carry no Resource: naming one would misattribute the hazard,
// which is exactly what the previous cross-provider fallback did. This is what keeps
// `--old <dir> --new <dir>` useful, where the module identifier is a filesystem path and no
// provider-specific classification is possible.
var genericHazards = map[string]HazardDescriptor{
	"name": {
		Impact:      "Resource names are immutable on most cloud resources; changing or replacing one triggers ForceNew destruction.",
		Remediation: "Keep the resource name unchanged, or plan for a replacement.",
	},
	"location": {
		Impact:      "Location or region is immutable on most cloud resources; changing it triggers ForceNew destruction.",
		Remediation: "Keep the location unchanged, or migrate with a replacement plan.",
	},
	"region": {
		Impact:      "Region is immutable on most cloud resources; changing it triggers ForceNew destruction.",
		Remediation: "Keep the region unchanged, or migrate with a replacement plan.",
	},
}

// matchModuleCategory classifies a module by the cloud it manages, or returns "" when the name
// carries no usable signal. Matching is token-aware rather than a raw substring test: "checks"
// and "weekends" both contain "eks", and classifying those as AWS EKS silently applied the EKS
// hazard table to unrelated modules.
func matchModuleCategory(moduleName string) string {
	trimmed := strings.TrimSpace(moduleName)
	if strings.Contains(strings.ToLower(trimmed), "kubernetes-engine") {
		return "gke"
	}
	tokens := moduleTokens(trimmed)
	// Ordered so the more specific Azure signals win over the generic "eks" token.
	for _, candidate := range []struct{ token, category string }{
		{"azurerm", "aks"},
		{"aks", "aks"},
		{"gke", "gke"},
		{"eks", "eks"},
	} {
		if _, ok := tokens[candidate.token]; ok {
			return candidate.category
		}
	}
	return ""
}

// moduleTokens splits a module identifier into lower-cased tokens on delimiters and camelCase
// boundaries, so both "my-eks-wrapper" and "myEksWrapper" yield the token "eks". It expects the
// original casing, because the camelCase boundaries are only visible before lower-casing.
func moduleTokens(name string) map[string]struct{} {
	tokens := make(map[string]struct{})
	var current strings.Builder
	flush := func() {
		if current.Len() > 0 {
			tokens[strings.ToLower(current.String())] = struct{}{}
			current.Reset()
		}
	}
	runes := []rune(name)
	for i, r := range runes {
		switch {
		case r == '-' || r == '_' || r == '.' || r == '/' || r == ' ':
			flush()
		case i > 0 && unicode.IsUpper(r):
			flush()
			current.WriteRune(r)
		default:
			current.WriteRune(r)
		}
	}
	flush()
	return tokens
}

// LookupHazard checks if a given variable name represents a known recreation hazard for the module.
//
// A module whose name identifies its cloud resolves against that provider's table alone, so a
// GKE module is never told its `vpc_id` is an EKS cluster hazard. An unclassified module (a bare
// directory comparison, say) falls back to the provider-agnostic table, which reports the hazard
// without inventing a resource type. The previous behaviour searched every provider's table for
// any unclassified module and reported whichever matched last.
func LookupHazard(moduleName, varName string) (HazardDescriptor, bool) {
	if category := matchModuleCategory(moduleName); category != "" {
		if desc, ok := knownModuleHazards[category][varName]; ok {
			return desc, true
		}
	}
	desc, ok := genericHazards[varName]
	return desc, ok
}

// AnalyzeHazards inspects a DiffReport and identifies any breaking changes or warnings that present recreation hazards.
func AnalyzeHazards(diffReport *diff.DiffReport) []RecreationHazard {
	return AnalyzeHazardsWithRegistry(diffReport, nil)
}

// AnalyzeHazardsWithRegistry inspects a DiffReport using both dynamic schema registry and static heuristics.
func AnalyzeHazardsWithRegistry(diffReport *diff.DiffReport, registry *DynamicHazardRegistry) []RecreationHazard {
	if diffReport == nil {
		return nil
	}

	lookup := func(name string) (HazardDescriptor, bool) {
		if registry != nil {
			if desc, ok := registry.Lookup(name); ok {
				return desc, true
			}
		}
		return LookupHazard(diffReport.ModuleName, name)
	}

	var hazards []RecreationHazard

	// Check breaking changes
	for _, item := range diffReport.BreakingChanges {
		if desc, ok := lookup(item.Name); ok {
			hazards = append(hazards, RecreationHazard{
				Variable:    item.Name,
				Resource:    desc.Resource,
				HazardType:  item.Category,
				Severity:    "CRITICAL_HAZARD",
				Description: fmt.Sprintf("%s (%s)", item.Description, desc.Impact),
				Impact:      desc.Impact,
				Remediation: desc.Remediation,
			})
		}
	}

	// Check warnings (e.g. type widenings on hazardous attributes)
	for _, item := range diffReport.Warnings {
		if desc, ok := lookup(item.Name); ok {
			hazards = append(hazards, RecreationHazard{
				Variable:    item.Name,
				Resource:    desc.Resource,
				HazardType:  item.Category,
				Severity:    "WARNING_HAZARD",
				Description: fmt.Sprintf("Modification to immutable attribute '%s': %s", item.Name, desc.Impact),
				Impact:      desc.Impact,
				Remediation: desc.Remediation,
			})
		}
	}

	return hazards
}

// AnalyzeWrapperHazards inspects a WrapperCompatibilityReport for recreation hazards.
func AnalyzeWrapperHazards(res *wrapper.WrapperCompatibilityReport) []RecreationHazard {
	return AnalyzeWrapperHazardsWithRegistry(res, nil)
}

// AnalyzeWrapperHazardsWithRegistry inspects a WrapperCompatibilityReport with dynamic schema support.
func AnalyzeWrapperHazardsWithRegistry(res *wrapper.WrapperCompatibilityReport, registry *DynamicHazardRegistry) []RecreationHazard {
	if res == nil {
		return nil
	}

	lookup := func(name string) (HazardDescriptor, bool) {
		if registry != nil {
			if desc, ok := registry.Lookup(name); ok {
				return desc, true
			}
		}
		return LookupHazard(res.TargetModuleName, name)
	}

	var hazards []RecreationHazard
	for _, item := range res.Issues {
		if desc, ok := lookup(item.Target); ok {
			hazards = append(hazards, RecreationHazard{
				Variable:    item.Target,
				Resource:    desc.Resource,
				HazardType:  item.Category,
				Severity:    "CRITICAL_HAZARD",
				Description: fmt.Sprintf("Wrapper issue on immutable variable '%s': %s (%s)", item.Target, item.Description, desc.Impact),
				Impact:      desc.Impact,
				Remediation: desc.Remediation,
			})
		}
	}
	return hazards
}

// displayResource names the resource a hazard applies to, or says so plainly when the hazard
// came from the provider-agnostic table and no resource type can honestly be named.
func displayResource(desc HazardDescriptor) string {
	if desc.Resource == "" {
		return "provider-agnostic resource"
	}
	return desc.Resource
}

// hazardDescriptorFor reconstructs the descriptor behind a reported hazard so the formatters can
// tell a named resource from a provider-agnostic one.
func hazardDescriptorFor(h RecreationHazard) HazardDescriptor {
	return HazardDescriptor{Resource: h.Resource}
}

// FormatHazardsText converts hazards into a terminal-friendly alert block.
func FormatHazardsText(hazards []RecreationHazard) string {
	if len(hazards) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("STATE DESTRUCTION & RECREATION HAZARDS (ForceNew):\n")
	sb.WriteString("  Upgrading with the changes below may cause Terraform to destroy and re-create core cloud resources in state!\n")
	for _, h := range hazards {
		fmt.Fprintf(&sb, "  • [%s] %s (%s): %s\n", h.Severity, h.Variable, displayResource(hazardDescriptorFor(h)), h.Description)
		if h.Remediation != "" {
			fmt.Fprintf(&sb, "    Remediation: %s\n", h.Remediation)
		}
	}
	sb.WriteString("\n")
	return sb.String()
}

// FormatHazardsMarkdown converts hazards into a formatted Markdown alert block.
func FormatHazardsMarkdown(hazards []RecreationHazard) string {
	if len(hazards) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("\n### State Destruction & Recreation Hazards (ForceNew)\n\n")
	sb.WriteString("> **CAUTION**: Upgrading with the changes below may cause Terraform to destroy and re-create core cloud resources in state!\n\n")
	sb.WriteString("| Variable | Target Resource | Severity | Destruction Hazard | Remediation |\n")
	sb.WriteString("| :--- | :--- | :--- | :--- | :--- |\n")
	for _, h := range hazards {
		fmt.Fprintf(&sb, "| `%s` | `%s` | `%s` | %s | %s |\n",
			h.Variable, displayResource(hazardDescriptorFor(h)), h.Severity, h.Description, h.Remediation)
	}
	sb.WriteString("\n")
	return sb.String()
}
