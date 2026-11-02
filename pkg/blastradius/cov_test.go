package blastradius

import (
	"testing"

	"github.com/23skdu/terraform_ast/pkg/eval"
)

func TestCoverage_FilterTaintedRenamed(t *testing.T) {
	tr := &eval.TaintReport{TargetVariable: "foo", AffectedResourceAttributes: []string{}}
	cs := []CallSite{{PassedInputs: map[string]string{"bar": "var.foo"}}}
	out := FilterTaintedCallSites(cs, tr)
	if len(out) != 1 {
		t.Fatalf("len=%d", len(out))
	}
}
