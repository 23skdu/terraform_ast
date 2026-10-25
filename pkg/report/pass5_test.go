package report

import (
	"testing"

	"github.com/23skdu/terraform_ast/pkg/ast"
	"github.com/23skdu/terraform_ast/pkg/diff"
	"github.com/23skdu/terraform_ast/pkg/eval"
	"github.com/hashicorp/terraform-config-inspect/tfconfig"
)

func TestPass5_RenderWrapperUpstreamBreaking(t *testing.T) {
	w := sampleWrapperReport()
	w.UpstreamDiff = &diff.DiffReport{
		BreakingChanges: []diff.DifferenceItem{
			{Category: "Variable", Name: "x", Severity: diff.SeverityBreaking, Description: "removed", Remediation: "fix", Pos: tfconfig.SourcePos{Filename: "a.tf", Line: 5}},
		},
	}
	_ = RenderWrapperText(w, Findings{})
	_ = RenderWrapperMarkdown(w, Findings{})
	_ = RenderWrapperSARIF(w, Findings{})
}

func TestPass5_RenderSARIFPosAndShifts(t *testing.T) {
	r := sampleDiffReport()
	r.BreakingChanges[0].Pos = tfconfig.SourcePos{Filename: "a.tf", Line: 7}
	findings := Findings{
		AddressShifts: []ast.ResourceAddressShift{{OldAddress: ast.ResourceAddress{Type: "a", Name: "b"}, NewAddress: ast.ResourceAddress{Type: "a", Name: "c"}, Reason: "r"}},
		Taint:         &eval.TaintReport{TargetVariable: "v", IsDeadCode: false, AffectedResourceAttributes: []string{"attr"}},
	}
	_ = RenderSARIF(r, findings)
	_ = RenderWrapperSARIF(sampleWrapperReport(), findings)
}

func TestPass5_RenderWrapperSARIFPos(t *testing.T) {
	w := sampleWrapperReport()
	w.Issues[0].Pos = tfconfig.SourcePos{Filename: "a.tf", Line: 3}
	_ = RenderWrapperSARIF(w, Findings{})
}
