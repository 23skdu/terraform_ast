package recreation

import (
	"fmt"
	"strings"

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
}

func matchModuleCategory(moduleName string) string {
	lower := strings.ToLower(moduleName)
	if strings.Contains(lower, "gke") || strings.Contains(lower, "kubernetes-engine") {
		return "gke"
	}
	if strings.Contains(lower, "eks") {
		return "eks"
	}
	// Default check across both if unclassified
	return "generic"
}

// LookupHazard checks if a given variable name represents a known recreation hazard for the module.
func LookupHazard(moduleName, varName string) (HazardDescriptor, bool) {
	cat := matchModuleCategory(moduleName)
	if cat != "generic" {
		if desc, ok := knownModuleHazards[cat][varName]; ok {
			return desc, true
		}
	}
	// Check all known rules if not found
	for _, mapping := range knownModuleHazards {
		if desc, ok := mapping[varName]; ok {
			return desc, true
		}
	}
	return HazardDescriptor{}, false
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

// FormatHazardsText converts hazards into a terminal-friendly alert block.
func FormatHazardsText(hazards []RecreationHazard) string {
	if len(hazards) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("⚠️  STATE DESTRUCTION & RECREATION HAZARDS (ForceNew):\n")
	sb.WriteString("  Upgrading with the changes below may cause Terraform to destroy and re-create core cloud resources in state!\n")
	for _, h := range hazards {
		fmt.Fprintf(&sb, "  • [%s] %s (%s): %s\n", h.Severity, h.Variable, h.Resource, h.Description)
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
	sb.WriteString("\n### ⚠️ State Destruction & Recreation Hazards (ForceNew)\n\n")
	sb.WriteString("> **CAUTION**: Upgrading with the changes below may cause Terraform to destroy and re-create core cloud resources in state!\n\n")
	sb.WriteString("| Variable | Target Resource | Severity | Destruction Hazard | Remediation |\n")
	sb.WriteString("| :--- | :--- | :--- | :--- | :--- |\n")
	for _, h := range hazards {
		fmt.Fprintf(&sb, "| `%s` | `%s` | `%s` | %s | %s |\n",
			h.Variable, h.Resource, h.Severity, h.Description, h.Remediation)
	}
	sb.WriteString("\n")
	return sb.String()
}
