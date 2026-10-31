package visualize

import (
	"encoding/xml"
	"strings"
	"testing"

	"github.com/23skdu/terraform_ast/pkg/ast"
)

func TestRenderBeforeEmpty(t *testing.T) {
	data, err := RenderBefore(nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(data), "<svg") {
		t.Fatal("expected svg root")
	}
	var v struct {
		XMLName xml.Name
	}
	if err := xml.Unmarshal(data, &v); err != nil {
		t.Fatalf("invalid xml: %v", err)
	}
}

func TestRenderAfterEmpty(t *testing.T) {
	data, err := RenderAfter(nil, Options{Theme: "dark", Width: 100, Height: 100})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "</svg>") {
		t.Fatal("missing close")
	}
}

func TestRenderTransitionEmpty(t *testing.T) {
	data, err := RenderTransition(nil, nil, nil, Options{Title: "T"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "Before") {
		t.Fatal("missing before label")
	}
}

func TestRenderTransitionWithModule(t *testing.T) {
	mod := &ast.ModuleInfo{
		ManagedResources: map[string]ast.ResourceDef{
			"aws_s3_bucket.logs": {Type: "aws_s3_bucket", Name: "logs"},
		},
	}
	old := mod
	newMod := &ast.ModuleInfo{
		ManagedResources: map[string]ast.ResourceDef{
			"aws_s3_bucket.logs2": {Type: "aws_s3_bucket", Name: "logs2"},
		},
	}
	shifts := ast.DetectAddressShifts(old, newMod)
	data, err := RenderTransition(old, newMod, shifts, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := xml.Unmarshal(data, &struct{ XMLName xml.Name }{}); err != nil {
		t.Fatalf("invalid xml: %v", err)
	}
}
