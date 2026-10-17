package goldentest

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// CloudProvider represents a major cloud provider supported in the golden test matrix.
type CloudProvider string

const (
	ProviderGCP   CloudProvider = "GCP"
	ProviderAWS   CloudProvider = "AWS"
	ProviderAzure CloudProvider = "Azure"
)

// ModuleSpec describes a cloud module benchmark in the golden matrix.
type ModuleSpec struct {
	ID                 string        `json:"id"`
	Provider           CloudProvider `json:"provider"`
	Name               string        `json:"name"`
	Source             string        `json:"source"`
	BaselineVersion    string        `json:"baseline_version"`
	TargetVersion      string        `json:"target_version"`
	GoldenBaselinePath string        `json:"golden_baseline_path"`
}

// MultiCloudCatalog returns the canonical golden module test matrix.
func MultiCloudCatalog() []ModuleSpec {
	return []ModuleSpec{
		// GCP Modules
		{
			ID:                 "gcp-gke",
			Provider:           ProviderGCP,
			Name:               "Kubernetes Engine (GKE)",
			Source:             "terraform-google-modules/kubernetes-engine/google",
			BaselineVersion:    "v29.0.0",
			TargetVersion:      "v30.0.0",
			GoldenBaselinePath: "test/golden/gke_upstream_diff.golden.json",
		},
		{
			ID:                 "gcp-fabric",
			Provider:           ProviderGCP,
			Name:               "Cloud Foundation Fabric",
			Source:             "terraform-google-modules/cloud-foundation-fabric",
			BaselineVersion:    "v30.0.0",
			TargetVersion:      "v31.0.0",
			GoldenBaselinePath: "test/golden/fabric_upstream_diff.golden.json",
		},
		{
			ID:                 "gcp-vpc",
			Provider:           ProviderGCP,
			Name:               "Network (VPC)",
			Source:             "terraform-google-modules/network/google",
			BaselineVersion:    "v9.0.0",
			TargetVersion:      "v10.0.0",
			GoldenBaselinePath: "test/golden/gcp_vpc_upstream_diff.golden.json",
		},
		{
			ID:                 "gcp-sql",
			Provider:           ProviderGCP,
			Name:               "Cloud SQL",
			Source:             "terraform-google-modules/sql-db/google",
			BaselineVersion:    "v20.0.0",
			TargetVersion:      "v21.0.0",
			GoldenBaselinePath: "test/golden/gcp_sql_upstream_diff.golden.json",
		},
		// AWS Modules
		{
			ID:                 "aws-eks",
			Provider:           ProviderAWS,
			Name:               "Elastic Kubernetes Service (EKS)",
			Source:             "terraform-aws-modules/eks/aws",
			BaselineVersion:    "v19.0.0",
			TargetVersion:      "v20.0.0",
			GoldenBaselinePath: "test/golden/eks_upstream_diff.golden.json",
		},
		{
			ID:                 "aws-vpc",
			Provider:           ProviderAWS,
			Name:               "VPC",
			Source:             "terraform-aws-modules/vpc/aws",
			BaselineVersion:    "v5.0.0",
			TargetVersion:      "v5.1.0",
			GoldenBaselinePath: "test/golden/aws_vpc_upstream_diff.golden.json",
		},
		{
			ID:                 "aws-rds",
			Provider:           ProviderAWS,
			Name:               "RDS",
			Source:             "terraform-aws-modules/rds/aws",
			BaselineVersion:    "v6.0.0",
			TargetVersion:      "v6.1.0",
			GoldenBaselinePath: "test/golden/aws_rds_upstream_diff.golden.json",
		},
		{
			ID:                 "aws-iam",
			Provider:           ProviderAWS,
			Name:               "IAM",
			Source:             "terraform-aws-modules/iam/aws",
			BaselineVersion:    "v5.30.0",
			TargetVersion:      "v5.31.0",
			GoldenBaselinePath: "test/golden/aws_iam_upstream_diff.golden.json",
		},
		// Azure Modules
		{
			ID:                 "azure-aks",
			Provider:           ProviderAzure,
			Name:               "Azure Kubernetes Service (AKS)",
			Source:             "Azure/aks/azurerm",
			BaselineVersion:    "v7.0.0",
			TargetVersion:      "v8.0.0",
			GoldenBaselinePath: "test/golden/aks_upstream_diff.golden.json",
		},
		{
			ID:                 "azure-vnet",
			Provider:           ProviderAzure,
			Name:               "Virtual Network (VNet)",
			Source:             "Azure/vnet/azurerm",
			BaselineVersion:    "v4.0.0",
			TargetVersion:      "v4.1.0",
			GoldenBaselinePath: "test/golden/azure_vnet_upstream_diff.golden.json",
		},
		{
			ID:                 "azure-postgresql",
			Provider:           ProviderAzure,
			Name:               "PostgreSQL Flexible Server",
			Source:             "Azure/postgresql/azurerm",
			BaselineVersion:    "v3.0.0",
			TargetVersion:      "v3.1.0",
			GoldenBaselinePath: "test/golden/azure_postgresql_upstream_diff.golden.json",
		},
	}
}

// BreakingChangeSummary captures the name and description of a breaking change from diff JSON.
type BreakingChangeSummary struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description"`
}

// ReportSummary matches the minimal subset of diff/report JSON required for drift comparison.
type ReportSummary struct {
	ModuleName      string                  `json:"module_name"`
	Verdict         string                  `json:"verdict"`
	TotalBreaking   int                     `json:"total_breaking"`
	BreakingChanges []BreakingChangeSummary `json:"breaking_changes"`
}

// DriftReport holds comparison results between live upstream diff results and canonical golden baselines.
type DriftReport struct {
	Spec                  ModuleSpec `json:"spec"`
	HasDrift              bool       `json:"has_drift"`
	BaselineBreakingCount int        `json:"baseline_breaking_count"`
	ActualBreakingCount   int        `json:"actual_breaking_count"`
	UndocumentedChanges   []string   `json:"undocumented_changes,omitempty"`
	Summary               string     `json:"summary"`
}

// DetectDrift compares actual diff output JSON against the golden baseline.
func DetectDrift(spec ModuleSpec, actualJSON []byte, goldenPath string) (*DriftReport, error) {
	// Parse actual report
	var actual ReportSummary
	if err := json.Unmarshal(actualJSON, &actual); err != nil {
		return nil, fmt.Errorf("failed to parse actual diff json: %w", err)
	}

	// Read and parse golden baseline
	cleanPath := filepath.Clean(goldenPath)
	// #nosec G304 -- golden baseline path lookup in tests
	baselineBytes, err := os.ReadFile(cleanPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read golden baseline file %s: %w", goldenPath, err)
	}

	var baseline ReportSummary
	if err := json.Unmarshal(baselineBytes, &baseline); err != nil {
		return nil, fmt.Errorf("failed to parse golden baseline json: %w", err)
	}

	baselineSet := make(map[string]bool)
	for _, b := range baseline.BreakingChanges {
		baselineSet[b.Name+":"+b.Description] = true
	}

	var undocumented []string
	for _, a := range actual.BreakingChanges {
		key := a.Name + ":" + a.Description
		if !baselineSet[key] {
			undocumented = append(undocumented, fmt.Sprintf("%s (%s): %s", a.Name, a.Type, a.Description))
		}
	}

	hasDrift := len(undocumented) > 0 || actual.TotalBreaking != baseline.TotalBreaking
	summary := fmt.Sprintf("[%s] %s: baseline breaking=%d, actual breaking=%d",
		spec.Provider, spec.Name, baseline.TotalBreaking, actual.TotalBreaking)
	if hasDrift {
		summary += " (DRIFT DETECTED)"
	} else {
		summary += " (MATCHES BASELINE)"
	}

	return &DriftReport{
		Spec:                  spec,
		HasDrift:              hasDrift,
		BaselineBreakingCount: baseline.TotalBreaking,
		ActualBreakingCount:   actual.TotalBreaking,
		UndocumentedChanges:   undocumented,
		Summary:               summary,
	}, nil
}

// GenerateDriftIssuePayload formats a GitHub Issue Markdown body when upstream drift is detected.
func GenerateDriftIssuePayload(reports []*DriftReport) (string, error) {
	var drifted []*DriftReport
	for _, r := range reports {
		if r.HasDrift {
			drifted = append(drifted, r)
		}
	}

	if len(drifted) == 0 {
		return "## Upstream Multi-Cloud Drift Matrix: All Baselines Intact\n\nNo unexpected drift or undocumented breaking changes detected.", nil
	}

	var sb strings.Builder
	sb.WriteString("# Upstream Multi-Cloud Module Drift Detected\n\n")
	sb.WriteString("The automated daily upstream scan detected discrepancies between canonical golden baselines and current registry outputs.\n\n")
	sb.WriteString("| Cloud Provider | Module | Upstream Source | Baseline Breaking | Actual Breaking | Status |\n")
	sb.WriteString("| :--- | :--- | :--- | :--- | :--- | :--- |\n")

	for _, d := range drifted {
		sb.WriteString(fmt.Sprintf("| **%s** | %s | `%s` | %d | %d | DRIFT |\n",
			d.Spec.Provider, d.Spec.Name, d.Spec.Source, d.BaselineBreakingCount, d.ActualBreakingCount))
	}

	sb.WriteString("\n### Discrepancy Details\n\n")
	for _, d := range drifted {
		sb.WriteString(fmt.Sprintf("#### %s (%s)\n", d.Spec.Name, d.Spec.Source))
		sb.WriteString(fmt.Sprintf("- **Baseline**: `%s` -> `%s`\n", d.Spec.BaselineVersion, d.Spec.TargetVersion))
		if len(d.UndocumentedChanges) > 0 {
			sb.WriteString("- **Undocumented Breaking Changes**:\n")
			for _, ch := range d.UndocumentedChanges {
				sb.WriteString(fmt.Sprintf("  - %s\n", ch))
			}
		} else {
			sb.WriteString(fmt.Sprintf("- Breaking count mismatch: baseline had %d, actual has %d.\n", d.BaselineBreakingCount, d.ActualBreakingCount))
		}
		sb.WriteString("\n")
	}

	sb.WriteString("### Recommended Remediation\n")
	sb.WriteString("1. Inspect upstream release notes to determine if changes are intentional or upstream regressions.\n")
	sb.WriteString("2. If intentional, run `UPDATE_GOLDEN=true go test ./test/terratest/...` to update canonical golden baselines.\n")

	return sb.String(), nil
}
