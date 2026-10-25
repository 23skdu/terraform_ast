package ast

import (
	"os"
	"testing"

	"github.com/hashicorp/terraform-config-inspect/tfconfig"
)

func TestPass5_RebaseMovedBlocks(t *testing.T) {
	info := &ModuleInfo{
		MovedBlocks: []MovedBlockDef{{From: "a", To: "b", Pos: tfconfig.SourcePos{Filename: "/from/x.tf"}}},
	}
	rebaseModulePaths(info, "/from", "/to")
	if info.MovedBlocks[0].Pos.Filename != "/to/x.tf" {
		t.Fatalf("expected /to/x.tf, got %s", info.MovedBlocks[0].Pos.Filename)
	}
}

func TestPass5_DetectAddressShiftsMoved(t *testing.T) {
	oldMod := &ModuleInfo{
		ManagedResources: map[string]ResourceDef{
			"aws_s3_bucket.old": {Mode: "managed", Type: "aws_s3_bucket", Name: "old"},
		},
	}
	newMod := &ModuleInfo{
		ManagedResources: map[string]ResourceDef{
			"aws_s3_bucket.new": {Mode: "managed", Type: "aws_s3_bucket", Name: "new"},
		},
		MovedBlocks: []MovedBlockDef{{From: "aws_s3_bucket.old", To: "aws_s3_bucket.new"}},
	}
	shifts := DetectAddressShifts(oldMod, newMod)
	if len(shifts) != 1 {
		t.Fatalf("expected 1 shift, got %d", len(shifts))
	}
	if shifts[0].IsRecreation {
		t.Fatal("expected IsRecreation false due to moved block")
	}
	// oldMod moved blocks path
	oldMod2 := &ModuleInfo{
		ManagedResources: map[string]ResourceDef{
			"aws_s3_bucket.old": {Mode: "managed", Type: "aws_s3_bucket", Name: "old"},
		},
		MovedBlocks: []MovedBlockDef{{From: "aws_s3_bucket.old", To: "aws_s3_bucket.new"}},
	}
	newMod2 := &ModuleInfo{
		ManagedResources: map[string]ResourceDef{
			"aws_s3_bucket.new": {Mode: "managed", Type: "aws_s3_bucket", Name: "new"},
		},
	}
	shifts2 := DetectAddressShifts(oldMod2, newMod2)
	if len(shifts2) != 1 || shifts2[0].IsRecreation {
		t.Fatal("expected suppression via oldMod moved blocks")
	}
}

func TestPass5_EnrichMovedBlocks(t *testing.T) {
	dir := t.TempDir()
	src := `moved {
  from = aws_s3_bucket.old
  to   = aws_s3_bucket.new
}
`
	if err := os.WriteFile(dir+"/main.tf", []byte(src), 0644); err != nil {
		t.Fatal(err)
	}
	info := &ModuleInfo{
		Variables:               make(map[string]VariableDef),
		Outputs:                 make(map[string]OutputDef),
		ModuleCalls:             make(map[string]ModuleCallDef),
		WrapperOutputReferences: make(map[string][]OutputReference),
		ManagedResources:        make(map[string]ResourceDef),
		DataResources:           make(map[string]ResourceDef),
		CheckBlocks:             make(map[string]CheckBlockDef),
		Functions:               make(map[string]FunctionDef),
	}
	enrichFromHCLFiles(dir, info)
	if len(info.MovedBlocks) != 1 {
		t.Fatalf("expected 1 moved block, got %d", len(info.MovedBlocks))
	}
	if info.MovedBlocks[0].From != "aws_s3_bucket.old" {
		t.Fatalf("unexpected from: %s", info.MovedBlocks[0].From)
	}
}
