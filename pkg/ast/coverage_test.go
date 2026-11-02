package ast

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/stretchr/testify/require"
)

func TestCoverage_InspectPrePostImportProvider(t *testing.T) {
	dir := t.TempDir()
	src := `resource "aws_vpc" "main" {
  cidr_block = "10.0.0.0/16"
  precondition {
    condition = true
  }
  lifecycle {
    precondition {
      condition = true
    }
  }
}

resource "aws_vpc" "other" {
  cidr_block = "10.0.0.0/16"
  postcondition {
    condition = true
  }
  lifecycle {
    postcondition {
      condition = true
    }
  }
}

resource "aws_vpc" "plain" {
  cidr_block = "10.0.0.0/16"
  lifecycle {
  }
}

output "o" {
  value = "x"
  precondition {
    condition = true
  }
}

output "p" {
  value = "y"
  postcondition {
    condition = true
  }
}

output "q" {
  value = "z"
}

import {
  to       = aws_s3_bucket.b
  id       = "id"
  provider = aws.west
}
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o644))
	info, err := Inspect(dir)
	require.NoError(t, err)
	r := info.ManagedResources["aws_vpc.main"]
	require.NotEmpty(t, r.Preconditions)
	r2 := info.ManagedResources["aws_vpc.other"]
	require.NotEmpty(t, r2.Postconditions)
	o := info.Outputs["o"]
	require.NotEmpty(t, o.Preconditions)
	o2 := info.Outputs["p"]
	require.NotEmpty(t, o2.Postconditions)
	require.Len(t, info.ImportBlocks, 1)
	require.Equal(t, "aws.west", info.ImportBlocks[0].Provider)
}

func TestCoverage_DetectAddressShiftsIndexExpr(t *testing.T) {
	oldMod := &ModuleInfo{ManagedResources: map[string]ResourceDef{"r": {Type: "t", Name: "r", Count: "2"}}}
	newMod := &ModuleInfo{ManagedResources: map[string]ResourceDef{"r2": {Type: "t", Name: "r2", Count: "2"}}}
	shifts := DetectAddressShifts(oldMod, newMod)
	require.NotEmpty(t, shifts)
}

func TestCoverage_CloneModuleInfoDeep(t *testing.T) {
	src := &ModuleInfo{
		ProviderConfigs:         map[string]map[string]string{"p": {"a": "b"}},
		WrapperOutputReferences: map[string][]OutputReference{"o": {{OutputName: "o", TargetModule: "m", TargetOutput: "out"}}},
		ParsedBodies:            map[string]*hclsyntax.Body{},
	}
	clone := cloneModuleInfo(src)
	require.NotNil(t, clone)
	src.ProviderConfigs["p"]["a"] = "changed"
	require.Equal(t, "b", clone.ProviderConfigs["p"]["a"])
	require.Nil(t, cloneModuleInfo(nil))
}
