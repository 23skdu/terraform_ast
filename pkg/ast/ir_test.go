package ast

import (
	"testing"
)

func TestResourceAddress_FullAddressAndParse(t *testing.T) {
	cases := []struct {
		addr     string
		expected ResourceAddress
	}{
		{
			addr: "aws_subnet.this",
			expected: ResourceAddress{
				Mode: "managed",
				Type: "aws_subnet",
				Name: "this",
			},
		},
		{
			addr: "data.aws_ami.ubuntu",
			expected: ResourceAddress{
				Mode: "data",
				Type: "aws_ami",
				Name: "ubuntu",
			},
		},
		{
			addr: "module.vpc.aws_subnet.this",
			expected: ResourceAddress{
				ModulePath: "module.vpc",
				Mode:       "managed",
				Type:       "aws_subnet",
				Name:       "this",
			},
		},
		{
			addr: "module.parent.module.child.data.aws_vpc.main",
			expected: ResourceAddress{
				ModulePath: "module.parent.module.child",
				Mode:       "data",
				Type:       "aws_vpc",
				Name:       "main",
			},
		},
		{
			addr: `module.vpc.aws_subnet.this["a"]`,
			expected: ResourceAddress{
				ModulePath: "module.vpc",
				Mode:       "managed",
				Type:       "aws_subnet",
				Name:       "this",
				IndexKey:   `"a"`,
			},
		},
		{
			addr: "aws_instance.server[0]",
			expected: ResourceAddress{
				Mode:     "managed",
				Type:     "aws_instance",
				Name:     "server",
				IndexKey: "0",
			},
		},
		{
			addr: "lone_identifier",
			expected: ResourceAddress{
				Mode: "managed",
				Name: "lone_identifier",
			},
		},
		{
			addr: "module.vpc",
			expected: ResourceAddress{
				Mode: "module",
				Type: "module",
				Name: "vpc",
			},
		},
		{
			addr: "module.parent.module.child",
			expected: ResourceAddress{
				ModulePath: "module.parent",
				Mode:       "module",
				Type:       "module",
				Name:       "child",
			},
		},
		{
			addr: "module.vpc[0]",
			expected: ResourceAddress{
				Mode:     "module",
				Type:     "module",
				Name:     "vpc",
				IndexKey: "0",
			},
		},
	}

	for _, tc := range cases {
		parsed := ParseResourceAddress(tc.addr)
		if parsed != tc.expected {
			t.Errorf("ParseResourceAddress(%q) = %+v, want %+v", tc.addr, parsed, tc.expected)
		}
		if tc.addr != "lone_identifier" && parsed.FullAddress() != tc.addr {
			t.Errorf("FullAddress() = %q, want %q", parsed.FullAddress(), tc.addr)
		}
		if parsed.String() != parsed.FullAddress() {
			t.Errorf("String() = %q, want %q", parsed.String(), parsed.FullAddress())
		}
	}
}

func TestExtractResourceAddressesAndGraph(t *testing.T) {
	if addrs := ExtractResourceAddresses(nil); addrs != nil {
		t.Errorf("expected nil for nil module, got %v", addrs)
	}

	gNil := BuildEvaluationGraph(nil)
	if gNil == nil || len(gNil.Nodes) != 0 {
		t.Errorf("expected empty graph for nil module")
	}

	mod := &ModuleInfo{
		ModuleCalls: map[string]ModuleCallDef{
			"vpc": {Name: "vpc", Count: "2"},
			"db":  {Name: "db", ForEach: `toset(["a"])`},
		},
		ManagedResources: map[string]ResourceDef{
			"aws_vpc.main": {
				Mode: "managed",
				Type: "aws_vpc",
				Name: "main",
			},
			"aws_subnet.public": {
				Mode: "managed",
				Type: "aws_subnet",
				Name: "public",
			},
		},
		DataResources: map[string]ResourceDef{
			"aws_ami.ubuntu": {
				Mode: "data",
				Type: "aws_ami",
				Name: "ubuntu",
			},
		},
	}

	addrs := ExtractResourceAddresses(mod)
	if len(addrs) != 5 {
		t.Fatalf("expected 5 addresses, got %d", len(addrs))
	}

	graph := BuildEvaluationGraph(mod)
	if len(graph.Nodes) != 5 {
		t.Fatalf("expected 5 graph nodes, got %d", len(graph.Nodes))
	}
	if node, ok := graph.Nodes["aws_vpc.main"]; !ok || node.Resource.Name != "main" {
		t.Errorf("expected node aws_vpc.main in graph")
	}
	if node, ok := graph.Nodes["data.aws_ami.ubuntu"]; !ok || node.Resource.Name != "ubuntu" {
		t.Errorf("expected node data.aws_ami.ubuntu in graph")
	}
	if node, ok := graph.Nodes["module.vpc[count]"]; !ok || node.Resource.Name != "vpc" {
		t.Errorf("expected node module.vpc in graph")
	}
}

func TestDetectAddressShifts(t *testing.T) {
	if shifts := DetectAddressShifts(nil, nil); shifts != nil {
		t.Errorf("expected nil shifts for nil modules")
	}
	if shifts := DetectAddressShifts(&ModuleInfo{}, nil); shifts != nil {
		t.Errorf("expected nil shifts for nil newMod")
	}

	oldMod := &ModuleInfo{
		ManagedResources: map[string]ResourceDef{
			"aws_subnet.this": {
				Mode: "managed",
				Type: "aws_subnet",
				Name: "this",
			},
			"aws_vpc.old_vpc": {
				Mode: "managed",
				Type: "aws_vpc",
				Name: "old_vpc",
			},
		},
	}

	newMod := &ModuleInfo{
		ManagedResources: map[string]ResourceDef{
			"aws_subnet.renamed": {
				Mode: "managed",
				Type: "aws_subnet",
				Name: "renamed",
			},
			// aws_vpc.old_vpc is removed with no replacement
		},
	}

	shifts := DetectAddressShifts(oldMod, newMod)
	if len(shifts) != 2 {
		t.Fatalf("expected 2 address shifts, got %d: %+v", len(shifts), shifts)
	}

	// First shift: aws_subnet.this -> aws_subnet.renamed (candidate matched)
	s1 := shifts[0]
	if s1.OldAddress.FullAddress() != "aws_subnet.this" || s1.NewAddress.FullAddress() != "aws_subnet.renamed" {
		t.Errorf("unexpected shift 1: %+v", s1)
	}
	if !s1.IsRecreation {
		t.Errorf("expected IsRecreation=true")
	}

	// Second shift: aws_vpc.old_vpc removed
	s2 := shifts[1]
	if s2.OldAddress.FullAddress() != "aws_vpc.old_vpc" || s2.NewAddress.FullAddress() != "" {
		t.Errorf("unexpected shift 2: %+v", s2)
	}
	if !s2.IsRecreation {
		t.Errorf("expected IsRecreation=true")
	}

	// Test identical modules (no shifts)
	noShifts := DetectAddressShifts(oldMod, oldMod)
	if len(noShifts) != 0 {
		t.Errorf("expected 0 shifts for identical modules, got %d", len(noShifts))
	}
}
