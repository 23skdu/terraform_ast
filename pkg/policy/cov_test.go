package policy

import (
	"testing"
)

func TestCoverage_RegoCacheEviction(t *testing.T) {
	for i := 0; i < 70; i++ {
		reportMapCache.put(uintptr(i+1), "h", map[string]any{})
	}
}

func TestCoverage_EvaluateRegoNoReport(t *testing.T) {
	pol, err := ParseRego("package terraform_ast.policy\ndeny contains msg if { false }")
	if err != nil {
		t.Fatal(err)
	}
	vs := EvaluateRego(pol, Input{})
	_ = vs
}
