package report

import (
	"testing"

	"github.com/23skdu/terraform_ast/pkg/diff"
	"github.com/23skdu/terraform_ast/pkg/policy"
	"github.com/23skdu/terraform_ast/pkg/wrapper"
)

func FuzzRenderReports(f *testing.F) {
	f.Add("mod/test", "v1.0.0", "v2.0.0", "BREAKING_CHANGES", "cluster_name", "removed required variable", "remedy here")
	f.Add("", "", "", "SAFE", "", "", "")
	f.Add("long/path/to/module/deep", "v1.2.3", "v1.2.4", "WARNINGS_ONLY", "desc", "warn detail", "")

	f.Fuzz(func(t *testing.T, mod, vOld, vNew, verdict, name, desc, rem string) {
		rep := &diff.DiffReport{
			ModuleName:       mod,
			OldVersion:       vOld,
			NewVersion:       vNew,
			Verdict:          verdict,
			CanSafelyUpdate:  verdict == "SAFE" || verdict == "WARNINGS_ONLY",
			TotalBreaking:    1,
			TotalWarnings:    1,
			TotalNewFeatures: 1,
			BreakingChanges: []diff.DifferenceItem{
				{
					Category:        "Variable",
					Name:            name,
					Description:     desc,
					Remediation:     rem,
					ConfidenceScore: 9,
				},
			},
			Warnings: []diff.DifferenceItem{
				{
					Category:        "TerraformCore",
					Name:            "required_version",
					Description:     desc,
					Remediation:     rem,
					ConfidenceScore: 7,
				},
			},
			NewFeatures: []diff.DifferenceItem{
				{
					Category:        "Variable",
					Name:            name,
					Description:     desc,
					ConfidenceScore: 9,
				},
			},
			PolicyViolations: []policy.Violation{
				{
					Rule:    "no_breaking",
					Message: desc,
				},
			},
		}

		findings := Findings{}

		_ = RenderText(rep, findings)
		_ = RenderMarkdown(rep)
		_ = RenderJSON(rep, findings)
		_ = RenderSARIF(rep, findings)

		wrapRep := &wrapper.WrapperCompatibilityReport{
			WrapperPath:      mod,
			TargetModuleName: name,
			TargetVersion:    vNew,
			CanSafelyUpdate:  true,
			Verdict:          "SAFE_TO_PIN",
			Issues: []wrapper.WrapperIssue{
				{
					Category:        "VariableRemoved",
					Target:          name,
					Description:     desc,
					Remediation:     rem,
					ConfidenceScore: 9,
				},
			},
			NewFeatureAdoptions: []wrapper.WrapperIssue{
				{
					Category:        "OptionalVariableAvailable",
					Target:          name,
					Description:     desc,
					Remediation:     rem,
					ConfidenceScore: 9,
				},
			},
		}

		_ = RenderWrapperText(wrapRep, findings)
		_ = RenderWrapperMarkdown(wrapRep)
		_ = RenderWrapperJSON(wrapRep, findings)
		_ = RenderWrapperSARIF(wrapRep, findings)
	})
}
