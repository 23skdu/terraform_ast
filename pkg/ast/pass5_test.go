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

func TestPass5_EnrichLifecycleBackend(t *testing.T) {
	dir := t.TempDir()
	src := `resource "null_resource" "x" {
  triggers = {
    a = "b"
  }
  lifecycle {
    prevent_destroy      = true
    create_before_destroy = true
    replace_triggered_by = [null_resource.y]
    ignore_changes      = [triggers]
  }
  provisioner "local-exec" {
    when    = destroy
    command = "echo hi"
  }
}

terraform {
  backend "s3" {
    bucket = "my-bucket"
    key    = "path/to/state"
  }
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
	if len(info.ManagedResources) != 1 {
		t.Fatalf("expected 1 managed resource, got %d", len(info.ManagedResources))
	}
	r := info.ManagedResources["null_resource.x"]
	if !r.Lifecycle.PreventDestroy || !r.Lifecycle.CreateBeforeDestroy {
		t.Fatal("lifecycle flags not captured")
	}
	if len(r.Provisioners) != 1 || r.Provisioners[0] != "destroy" {
		t.Fatalf("provisioner not captured: %v", r.Provisioners)
	}
	if info.Backend == nil || info.Backend.Type != "s3" {
		t.Fatalf("backend not captured: %v", info.Backend)
	}
	_ = info.Backend.Attributes["bucket"]
}

func TestPass5_EnrichNilMaps(t *testing.T) {
	dir := t.TempDir()
	src := `resource "null_resource" "x" {
  triggers = {
    a = "b"
  }
}

data "aws_ami" "ubuntu" {
  most_recent = true
  lifecycle {
    prevent_destroy = true
  }
}

terraform {
  backend "s3" {
    bucket = "b"
  }
}
`
	if err := os.WriteFile(dir+"/main.tf", []byte(src), 0644); err != nil {
		t.Fatal(err)
	}
	info := &ModuleInfo{}
	enrichFromHCLFiles(dir, info)
	if info.ManagedResources == nil {
		t.Fatal("expected managed resources initialized")
	}
}

func TestPass5_EnrichCountForEach(t *testing.T) {
	dir := t.TempDir()
	src := `resource "aws_instance" "web" {
  count = 2
}

resource "aws_instance" "app" {
  for_each = toset(["a", "b"])
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
	if info.ManagedResources["aws_instance.web"].Count == "" {
		t.Fatal("count not captured")
	}
	if info.ManagedResources["aws_instance.app"].ForEach == "" {
		t.Fatal("for_each not captured")
	}
}

func TestPass5_ExtractAddressesCountForEachData(t *testing.T) {
	mod := &ModuleInfo{
		DataResources: map[string]ResourceDef{
			"data.aws_ami.ubuntu": {Mode: "data", Type: "aws_ami", Name: "ubuntu", Count: "2"},
		},
	}
	addrs := ExtractResourceAddresses(mod)
	if len(addrs) != 1 || addrs[0].IndexKey != "count" {
		t.Fatalf("expected count index key for data, got %v", addrs)
	}
	mod2 := &ModuleInfo{
		DataResources: map[string]ResourceDef{
			"data.aws_region.current": {Mode: "data", Type: "aws_region", Name: "current", ForEach: "toset([])"},
		},
	}
	addrs2 := ExtractResourceAddresses(mod2)
	if len(addrs2) != 1 || addrs2[0].IndexKey != "for_each" {
		t.Fatalf("expected for_each index key for data, got %v", addrs2)
	}
}

func TestPass5_ExtractAddressesCountForEach(t *testing.T) {
	mod := &ModuleInfo{
		ManagedResources: map[string]ResourceDef{
			"aws_instance.web": {Mode: "managed", Type: "aws_instance", Name: "web", Count: "2"},
		},
	}
	addrs := ExtractResourceAddresses(mod)
	if len(addrs) != 1 || addrs[0].IndexKey != "count" {
		t.Fatalf("expected count index key, got %v", addrs)
	}
	mod2 := &ModuleInfo{
		ManagedResources: map[string]ResourceDef{
			"aws_instance.web": {Mode: "managed", Type: "aws_instance", Name: "web", ForEach: "toset([])"},
		},
	}
	addrs2 := ExtractResourceAddresses(mod2)
	if len(addrs2) != 1 || addrs2[0].IndexKey != "for_each" {
		t.Fatalf("expected for_each index key, got %v", addrs2)
	}
}

func TestPass5_DetectAddressShiftsSameIndexKey(t *testing.T) {
	oldMod := &ModuleInfo{
		ManagedResources: map[string]ResourceDef{
			"aws_instance.a": {Mode: "managed", Type: "aws_instance", Name: "a", Count: "2"},
		},
	}
	newMod := &ModuleInfo{
		ManagedResources: map[string]ResourceDef{
			"aws_instance.b": {Mode: "managed", Type: "aws_instance", Name: "b", Count: "3"},
		},
	}
	shifts := DetectAddressShifts(oldMod, newMod)
	if len(shifts) != 1 {
		t.Fatalf("expected 1 shift, got %d", len(shifts))
	}
}

func TestPass5_DetectAddressShiftsModulePath(t *testing.T) {
	oldMod2 := &ModuleInfo{
		ManagedResources: map[string]ResourceDef{
			"aws_vpc.main": {Mode: "managed", Type: "aws_vpc", Name: "main"},
		},
	}
	newMod2 := &ModuleInfo{
		ManagedResources: map[string]ResourceDef{
			"aws_vpc.moved": {Mode: "managed", Type: "aws_vpc", Name: "moved"},
		},
	}
	shifts := DetectAddressShifts(oldMod2, newMod2)
	if len(shifts) != 1 {
		t.Fatalf("expected 1 shift, got %d", len(shifts))
	}
}

func TestPass5_ModuleProvidersDependsOn(t *testing.T) {
	dir := t.TempDir()
	src := `module "child" {
  source       = "./child"
  count        = 2
  for_each     = toset(["a"])
  providers    = { aws = aws.west }
  depends_on   = [null_resource.x]
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
	mc := info.ModuleCalls["child"]
	if mc.Count == "" || mc.ForEach == "" {
		t.Fatalf("count/for_each not captured: %+v", mc)
	}
}

func TestPass5_DataResourceCountForEach(t *testing.T) {
	dir := t.TempDir()
	src := `data "aws_ami" "ubuntu" {
  count = 2
}
data "aws_region" "current" {
  for_each = toset(["a"])
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
	if info.DataResources["data.aws_ami.ubuntu"].Count == "" {
		t.Fatal("data count not captured")
	}
	if info.DataResources["data.aws_region.current"].ForEach == "" {
		t.Fatal("data for_each not captured")
	}
}

func TestPass5_DetectAddressShiftsMultiAndCountForEach(t *testing.T) {
	// multi-resource: one unchanged, one renamed -> covers continue (newAddr in oldMap)
	oldMod := &ModuleInfo{
		ManagedResources: map[string]ResourceDef{
			"aws_vpc.stable": {Mode: "managed", Type: "aws_vpc", Name: "stable"},
			"aws_vpc.old":    {Mode: "managed", Type: "aws_vpc", Name: "old"},
		},
	}
	newMod := &ModuleInfo{
		ManagedResources: map[string]ResourceDef{
			"aws_vpc.stable": {Mode: "managed", Type: "aws_vpc", Name: "stable"},
			"aws_vpc.new":    {Mode: "managed", Type: "aws_vpc", Name: "new"},
		},
	}
	shifts := DetectAddressShifts(oldMod, newMod)
	if len(shifts) != 1 {
		t.Fatalf("expected 1 shift, got %d", len(shifts))
	}

	// count -> for_each: same name, different index key -> sc += 2 and sc += 1
	oldMod2 := &ModuleInfo{
		ManagedResources: map[string]ResourceDef{
			"aws_instance.web": {Mode: "managed", Type: "aws_instance", Name: "web", Count: "2"},
		},
	}
	newMod2 := &ModuleInfo{
		ManagedResources: map[string]ResourceDef{
			"aws_instance.web": {Mode: "managed", Type: "aws_instance", Name: "web", ForEach: "toset([])"},
		},
	}
	shifts2 := DetectAddressShifts(oldMod2, newMod2)
	if len(shifts2) != 1 {
		t.Fatalf("expected 1 shift for count->for_each, got %d", len(shifts2))
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
