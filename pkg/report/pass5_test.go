package report

import (
	"testing"

	"github.com/23skdu/terraform_ast/pkg/diff"
)

func TestPass5_RenderWrapperUpstreamBreaking(t *testing.T) {
	w := sampleWrapperReport()
	w.UpstreamDiff = &diff.DiffReport{
		BreakingChanges: []diff.DifferenceItem{
			{Category: "Variable", Name: "x", Severity: diff.SeverityBreaking, Description: "removed", Remediation: "fix"},
		},
	}
	_ = RenderWrapperText(w, Findings{})
	_ = RenderWrapperMarkdown(w, Findings{})
	_ = RenderWrapperSARIF(w, Findings{})
}
