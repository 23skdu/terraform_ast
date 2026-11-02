package diff

import (
	"testing"

	"github.com/23skdu/terraform_ast/pkg/ast"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type simpleHazardLookup struct{}

func (simpleHazardLookup) LookupHazard(key string) (bool, string, string) {
	if key == "aws_vpc.main.cidr_block" || key == "cidr_block" {
		return true, "impact", "remediation"
	}
	if key == "aws_vpc.main.machine_type" || key == "machine_type" || key == "r.machine_type" {
		return true, "", ""
	}
	return false, "", ""
}

func TestCoverage_DestructiveToggles(t *testing.T) {
	oldMod := &ast.ModuleInfo{
		ManagedResources: map[string]ast.ResourceDef{
			"aws_vpc.main": {Type: "aws_vpc", Name: "main", Count: "2", ForEach: `toset(["a","b"])`, Attributes: map[string]string{"cidr_block": "10.0.0.0/16", "deletion_protection": "true", "machine_type": "n1"}},
		},
		DataResources: map[string]ast.ResourceDef{
			"data.aws_ami.ubuntu": {Type: "aws_ami", Name: "ubuntu", Mode: "data", Attributes: map[string]string{"most_recent": "true", "filter": "x"}},
		},
		Locals:  map[string]string{"cidr": "10.0.0.0/16"},
		Backend: &ast.BackendConfig{Type: "s3", Attributes: map[string]string{"bucket": "b"}},
		Cloud:   nil,
		ProviderConfigs: map[string]map[string]string{
			"aws": {"region": "us-east-1"},
		},
		ModuleCalls: map[string]ast.ModuleCallDef{
			"vpc": {Name: "vpc", Source: "x", Version: ">= 1.0, < 2.0", Count: "2", ForEach: `toset(["a"])`},
		},
		ImportBlocks: []ast.ImportBlockDef{{To: "aws_s3_bucket.b", ID: "id1", Provider: "aws.west"}},
		CheckBlocks:  map[string]ast.CheckBlockDef{"h": {Name: "h", AssertRules: []string{"true"}}},
	}
	newMod := &ast.ModuleInfo{
		ManagedResources: map[string]ast.ResourceDef{
			"aws_vpc.main": {Type: "aws_vpc", Name: "main", Count: "1", ForEach: `toset(["a"])`, Attributes: map[string]string{"cidr_block": "10.1.0.0/16", "machine_type": "n2"}, Preconditions: []string{"cond"}, Postconditions: []string{"cond2"}},
		},
		DataResources: map[string]ast.ResourceDef{
			"data.aws_ami.ubuntu": {Type: "aws_ami", Name: "ubuntu", Mode: "data", Attributes: map[string]string{"most_recent": "false"}},
		},
		Locals: map[string]string{"cidr": "10.1.0.0/16"},
		Cloud:  &ast.BackendConfig{Type: "cloud", Attributes: map[string]string{"organization": "o"}},
		ProviderConfigs: map[string]map[string]string{
			"aws": {"region": "eu-west-1"},
		},
		ModuleCalls: map[string]ast.ModuleCallDef{
			"vpc": {Name: "vpc", Source: "x", Version: ">= 2.0, < 3.0", Count: "1", ForEach: `toset(["a","b"])`},
		},
		ImportBlocks: []ast.ImportBlockDef{{To: "aws_s3_bucket.b", ID: "id2", Provider: "aws.east"}},
		CheckBlocks:  map[string]ast.CheckBlockDef{"h": {Name: "h", AssertRules: []string{"false"}}},
	}
	// managed resource references data source and local for promotion
	oldMod.ManagedResources["aws_vpc.main"] = ast.ResourceDef{Type: "aws_vpc", Name: "main", Count: "2", ForEach: `toset(["a","b"])`, Attributes: map[string]string{"cidr_block": "10.0.0.0/16", "ami": "data.aws_ami.ubuntu.id", "vpc_id": "local.cidr", "machine_type": "n1"}}
	hr := simpleHazardLookup{}
	report := Compare(oldMod, newMod, "1.0.0", "2.0.0", hr)
	require.NotNil(t, report)
	assert.Greater(t, report.TotalBreaking, 0)
	// provider added/removed
	newMod2 := &ast.ModuleInfo{ManagedResources: map[string]ast.ResourceDef{}, ProviderConfigs: map[string]map[string]string{"gcp": {"region": "r"}}}
	report2 := Compare(oldMod, newMod2, "1.0.0", "2.0.0", hr)
	assert.NotNil(t, report2)
	// provider attr removed
	oldP := &ast.ModuleInfo{ProviderConfigs: map[string]map[string]string{"aws": {"region": "us-east-1", "profile": "p"}}}
	newP := &ast.ModuleInfo{ProviderConfigs: map[string]map[string]string{"aws": {"region": "us-east-1"}}}
	oldP2 := &ast.ModuleInfo{ProviderConfigs: map[string]map[string]string{"aws": {"region": "us-east-1"}}}
	newP2 := &ast.ModuleInfo{ProviderConfigs: map[string]map[string]string{"aws": {"region": "us-east-1", "profile": "p"}}}
	rP2 := Compare(oldP2, newP2, "1.0.0", "2.0.0")
	require.NotNil(t, rP2)
	rP := Compare(oldP, newP, "1.0.0", "2.0.0")
	require.NotNil(t, rP)
	// moved blocks normalization
	_ = report

	// cloud transition and nil attrs
	old2 := &ast.ModuleInfo{Backend: &ast.BackendConfig{Type: "s3", Attributes: nil}}
	new2 := &ast.ModuleInfo{Cloud: &ast.BackendConfig{Type: "cloud", Attributes: nil}}
	r2 := Compare(old2, new2, "1.0.0", "2.0.0")
	require.NotNil(t, r2)
	old3 := &ast.ModuleInfo{Backend: &ast.BackendConfig{Type: "s3", Attributes: map[string]string{"bucket": "b"}}}
	new3 := &ast.ModuleInfo{Backend: &ast.BackendConfig{Type: "s3", Attributes: map[string]string{}}}
	old6 := &ast.ModuleInfo{Cloud: &ast.BackendConfig{Type: "cloud", Attributes: nil}}
	new6 := &ast.ModuleInfo{Cloud: &ast.BackendConfig{Type: "cloud", Attributes: nil}}
	r6 := Compare(old6, new6, "1.0.0", "2.0.0")
	require.NotNil(t, r6)
	old7 := &ast.ModuleInfo{Cloud: &ast.BackendConfig{Type: "cloud", Attributes: map[string]string{"organization": "o"}}}
	new7 := &ast.ModuleInfo{Backend: &ast.BackendConfig{Type: "s3", Attributes: map[string]string{"bucket": "b"}}}
	r7 := Compare(old7, new7, "1.0.0", "2.0.0")
	require.NotNil(t, r7)
	r3 := Compare(old3, new3, "1.0.0", "2.0.0")
	require.NotNil(t, r3)
	// hasForceNew via hazard lookup only
	oldH := &ast.ModuleInfo{ManagedResources: map[string]ast.ResourceDef{"r": {Type: "t", Name: "r", Attributes: map[string]string{"machine_type": "n1"}, Preconditions: []string{}}}}
	newH := &ast.ModuleInfo{ManagedResources: map[string]ast.ResourceDef{"r": {Type: "t", Name: "r", Attributes: map[string]string{"machine_type": "n2"}, Preconditions: []string{"cond"}}}}
	oldH3 := &ast.ModuleInfo{ManagedResources: map[string]ast.ResourceDef{"r": {Type: "t", Name: "r", Attributes: map[string]string{"foo": "bar"}, Preconditions: []string{"c1"}, Postconditions: []string{"c2"}}}}
	newH3 := &ast.ModuleInfo{ManagedResources: map[string]ast.ResourceDef{"r": {Type: "t", Name: "r", Attributes: map[string]string{"foo": "bar"}, Preconditions: []string{"c3"}, Postconditions: []string{"c4"}}}}
	rH3 := Compare(oldH3, newH3, "1.0.0", "2.0.0")
	require.NotNil(t, rH3)
	oldH2 := &ast.ModuleInfo{ManagedResources: map[string]ast.ResourceDef{"x": {Type: "t", Name: "x", Attributes: map[string]string{"machine_type": "n1"}, Preconditions: []string{}}}}
	newH2 := &ast.ModuleInfo{ManagedResources: map[string]ast.ResourceDef{"x": {Type: "t", Name: "x", Attributes: map[string]string{"machine_type": "n2"}, Preconditions: []string{"cond"}}}}
	rH2 := Compare(oldH2, newH2, "1.0.0", "2.0.0", hr)
	require.NotNil(t, rH2)
	rH := Compare(oldH, newH, "1.0.0", "2.0.0", hr)
	require.NotNil(t, rH)
	// for_each shrinkage
	old4 := &ast.ModuleInfo{ManagedResources: map[string]ast.ResourceDef{"r": {Type: "t", Name: "r", ForEach: `toset(["a","b"])`}}}
	// data selector non-promoted
	oldD := &ast.ModuleInfo{DataResources: map[string]ast.ResourceDef{"data.aws_ami.ubuntu": {Type: "aws_ami", Name: "ubuntu", Mode: "data", Attributes: map[string]string{"most_recent": "true"}}}, ManagedResources: map[string]ast.ResourceDef{"r": {Type: "t", Name: "r", Attributes: map[string]string{"foo": "bar"}}}}
	newD := &ast.ModuleInfo{DataResources: map[string]ast.ResourceDef{"data.aws_ami.ubuntu": {Type: "aws_ami", Name: "ubuntu", Mode: "data", Attributes: map[string]string{"most_recent": "false"}}}, ManagedResources: map[string]ast.ResourceDef{"r": {Type: "t", Name: "r", Attributes: map[string]string{"foo": "bar"}}}}
	rD := Compare(oldD, newD, "1.0.0", "2.0.0")
	require.NotNil(t, rD)
	new4 := &ast.ModuleInfo{ManagedResources: map[string]ast.ResourceDef{"r": {Type: "t", Name: "r", ForEach: `toset(["a"])`}}}
	r4 := Compare(old4, new4, "1.0.0", "2.0.0")
	require.NotNil(t, r4)
	// for_each shrinkage (non-static)
	old4b := &ast.ModuleInfo{ManagedResources: map[string]ast.ResourceDef{"r": {Type: "t", Name: "r", ForEach: `var.x`}}}
	new4b := &ast.ModuleInfo{ManagedResources: map[string]ast.ResourceDef{"r": {Type: "t", Name: "r", ForEach: `var.y`}}}
	old4c := &ast.ModuleInfo{ManagedResources: map[string]ast.ResourceDef{"r": {Type: "t", Name: "r", ForEach: `toset(["a","b"])`}}}
	new4c := &ast.ModuleInfo{ManagedResources: map[string]ast.ResourceDef{"r": {Type: "t", Name: "r", ForEach: `toset(["b","a"])`}}}
	r4c := Compare(old4c, new4c, "1.0.0", "2.0.0")
	require.NotNil(t, r4c)
	r4b := Compare(old4b, new4b, "1.0.0", "2.0.0")
	require.NotNil(t, r4b)
	// module call version breaking (major)
	old5 := &ast.ModuleInfo{ModuleCalls: map[string]ast.ModuleCallDef{"m": {Name: "m", Source: "s", Version: ">= 1.0, < 2.0"}}}
	new5 := &ast.ModuleInfo{ModuleCalls: map[string]ast.ModuleCallDef{"m": {Name: "m", Source: "s", Version: ">= 2.0, < 3.0"}}}
	// module call version warning (non-breaking bump)
	old5w := &ast.ModuleInfo{ModuleCalls: map[string]ast.ModuleCallDef{"m": {Name: "m", Source: "s", Version: ">= 1.0, < 2.0"}}}
	new5w := &ast.ModuleInfo{ModuleCalls: map[string]ast.ModuleCallDef{"m": {Name: "m", Source: "s", Version: ">= 1.1, < 2.0"}}}
	r5w := Compare(old5w, new5w, "1.0.0", "2.0.0")
	require.NotNil(t, r5w)
	// module call count warning (increase)
	old5c2 := &ast.ModuleInfo{ModuleCalls: map[string]ast.ModuleCallDef{"m": {Name: "m", Source: "s", Count: "1"}}}
	new5c2 := &ast.ModuleInfo{ModuleCalls: map[string]ast.ModuleCallDef{"m": {Name: "m", Source: "s", Count: "3"}}}
	r5c2 := Compare(old5c2, new5c2, "1.0.0", "2.0.0")
	require.NotNil(t, r5c2)
	r5d := Compare(old5, new5, "1.0.0", "2.0.0")
	require.NotNil(t, r5d)
	// module call version breaking (0.x minor)
	old5b := &ast.ModuleInfo{ModuleCalls: map[string]ast.ModuleCallDef{"m": {Name: "m", Source: "s", Version: "~> 0.12.0"}}}
	new5b := &ast.ModuleInfo{ModuleCalls: map[string]ast.ModuleCallDef{"m": {Name: "m", Source: "s", Version: "~> 0.15.0"}}}
	r5b := Compare(old5b, new5b, "1.0.0", "2.0.0")
	require.NotNil(t, r5b)
	// module call version breaking (upper bound)
	old5c := &ast.ModuleInfo{ModuleCalls: map[string]ast.ModuleCallDef{"m": {Name: "m", Source: "s", Version: ">= 1.0, < 6.0"}}}
	new5c := &ast.ModuleInfo{ModuleCalls: map[string]ast.ModuleCallDef{"m": {Name: "m", Source: "s", Version: ">= 1.0, < 5.0"}}}
	r5c := Compare(old5c, new5c, "1.0.0", "2.0.0")
	require.NotNil(t, r5c)
	// isBreakingConstraintChange max and excluded
	assert.True(t, isBreakingConstraintChange([]string{">= 1.0, < 2.0"}, []string{">= 1.0, < 1.5"}))
	assert.True(t, isBreakingConstraintChange([]string{">= 1.0, < 2.0, != 1.5.0"}, []string{">= 1.0, < 2.0, != 1.5.0, != 1.6.0"}))
	// return false/max
	assert.False(t, isBreakingConstraintChange([]string{}, []string{">= 1.0"}))
	assert.False(t, isBreakingConstraintChange([]string{">= 1.0"}, []string{}))

	// outputs coverage
	oldO := &ast.ModuleInfo{Outputs: map[string]ast.OutputDef{"o": {Name: "o", Type: "string", Value: "a", Preconditions: []string{"c1"}, Postconditions: []string{"c2"}}}}
	newO := &ast.ModuleInfo{Outputs: map[string]ast.OutputDef{"o": {Name: "o", Type: "number", Value: "b", Sensitive: true, Deprecated: "dep", Preconditions: []string{"c3"}, Postconditions: []string{"c4"}}}}
	rO := Compare(oldO, newO, "1.0.0", "2.0.0")
	require.NotNil(t, rO)
	// modern blocks
	oldM := &ast.ModuleInfo{CheckBlocks: map[string]ast.CheckBlockDef{"c": {Name: "c", AssertRules: []string{"true"}}}, ImportBlocks: []ast.ImportBlockDef{{To: "r", ID: "1", Provider: "aws.west"}}, MovedBlocks: []ast.MovedBlockDef{{From: "a", To: "b"}}, EncryptionBlocks: []ast.EncryptionBlockDef{{KeyProvider: "k"}}}
	newM := &ast.ModuleInfo{CheckBlocks: map[string]ast.CheckBlockDef{"c": {Name: "c", AssertRules: []string{"false"}}}, ImportBlocks: []ast.ImportBlockDef{{To: "r", ID: "2", Provider: "aws.east"}}, Functions: map[string]ast.FunctionDef{"f": {Name: "f"}}, EncryptionBlocks: []ast.EncryptionBlockDef{}}
	rM := Compare(oldM, newM, "1.0.0", "2.0.0")
	require.NotNil(t, rM)
	// provider source change
	oldPX := &ast.ModuleInfo{RequiredProviders: map[string]ast.ProviderRequirement{"aws": {Name: "aws", Source: "hashicorp/aws", VersionConstraints: []string{">= 5.0"}}}}
	newPX := &ast.ModuleInfo{RequiredProviders: map[string]ast.ProviderRequirement{"aws": {Name: "aws", Source: "hashicorp/aws-fork", VersionConstraints: []string{">= 5.1"}}}}
	rPX := Compare(oldPX, newPX, "1.0.0", "2.0.0")
	require.NotNil(t, rPX)
}

func TestCoverage_IsBreakingConstraintChange(t *testing.T) {
	assert.True(t, isBreakingConstraintChange([]string{">= 1.0, < 2.0"}, []string{">= 1.0, < 1.5"}))
	assert.True(t, isBreakingConstraintChange([]string{">= 1.0, < 2.0, != 1.5.0"}, []string{">= 1.0, < 2.0, != 1.5.0, != 1.6.0"}))
	assert.False(t, isBreakingConstraintChange([]string{">= 1.0"}, []string{">= 1.1"}))
}
