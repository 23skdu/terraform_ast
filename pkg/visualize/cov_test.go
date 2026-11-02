package visualize

import (
	"testing"

	"github.com/23skdu/terraform_ast/pkg/ast"
)

func TestCoverage_Visualize(t *testing.T) {
	old := &ast.ModuleInfo{ManagedResources: map[string]ast.ResourceDef{"a": {Type: "t", Name: "a"}, "b": {Type: "t", Name: "b"}, "verylongresourcenameverylongresourcenameverylongresourcenameverylongresourcename": {Type: "t", Name: "verylongresourcenameverylongresourcenameverylongresourcenameverylongresourcename"}}}
	nw := &ast.ModuleInfo{ManagedResources: map[string]ast.ResourceDef{"a": {Type: "t", Name: "a"}, "c": {Type: "t", Name: "c"}, "d": {Type: "t", Name: "d"}, "e": {Type: "t", Name: "e"}}}
	opts := Options{Title: "t", Width: 100, Height: 50, Theme: "dark"}
	b, err := RenderBefore(old, opts)
	if err != nil || len(b) == 0 {
		t.Fatal("before")
	}
	a, err := RenderAfter(nw, opts)
	if err != nil || len(a) == 0 {
		t.Fatal("after")
	}
	shifts := ast.DetectAddressShifts(old, nw)
	tr, err := RenderTransition(old, nw, shifts, opts)
	if err != nil || len(tr) == 0 {
		t.Fatal("transition")
	}
	// migrated shift
	old2 := &ast.ModuleInfo{ManagedResources: map[string]ast.ResourceDef{"old": {Type: "t", Name: "old"}}}
	new2 := &ast.ModuleInfo{ManagedResources: map[string]ast.ResourceDef{"new": {Type: "t", Name: "new"}}}
	// removed resource shift (empty new address)
	old3 := &ast.ModuleInfo{ManagedResources: map[string]ast.ResourceDef{"gone": {Type: "u", Name: "gone"}}}
	new3 := &ast.ModuleInfo{ManagedResources: map[string]ast.ResourceDef{"a": {Type: "t", Name: "a"}}}
	shifts3 := ast.DetectAddressShifts(old3, new3)
	tr3, err := RenderTransition(old3, new3, shifts3, opts)
	if err != nil || len(tr3) == 0 {
		t.Fatal("transition3")
	}
	new2.MovedBlocks = []ast.MovedBlockDef{{From: "t.old", To: "t.new"}}
	shifts2 := ast.DetectAddressShifts(old2, new2)
	tr2, err := RenderTransition(old2, new2, shifts2, opts)
	if err != nil || len(tr2) == 0 {
		t.Fatal("transition2")
	}
	// truncate long address
	long := &ast.ModuleInfo{ManagedResources: map[string]ast.ResourceDef{"verylongresourcename": {Type: "t", Name: "verylongresourcename"}}}
	_, _ = RenderBefore(long, Options{Width: 10, Height: 10})
}
