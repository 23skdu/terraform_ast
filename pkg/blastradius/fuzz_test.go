package blastradius

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

// FuzzDiscoverCallSites feeds arbitrary Terraform and Terragrunt files through the downstream
// consumer scanner. Discovery walks untrusted configuration trees, so it must never panic and must
// always return a consistent set of call sites.
func FuzzDiscoverCallSites(f *testing.F) {
	for _, seed := range []string{
		`module "wrapper" { source = "../wrapper" }`,
		`module "wrapper" { source = "../wrapper"
  region = "us"
}
output "endpoint" { value = module.wrapper.endpoint }`,
		`module "other" { source = "./elsewhere" }`,
		"module {}\noutput {}\n",
		`locals { x = module.wrapper.out }`,
		"not valid { hcl",
		"",
		"\x00",
	} {
		f.Add(seed, false)
		f.Add(seed, true)
	}

	f.Fuzz(func(t *testing.T, source string, asTerragrunt bool) {
		dir := t.TempDir()
		name := "main.tf"
		if asTerragrunt {
			name = "terragrunt.hcl"
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(source), 0o600); err != nil {
			t.Skip("unable to materialise the fuzz input")
		}

		sites, err := discoverCallSites(dir, "wrapper")
		if err != nil {
			return
		}
		for _, site := range sites {
			// An empty module label is legal HCL, so only the structural invariants are asserted.
			if site.File == "" {
				t.Fatalf("discovered a call site without a file path")
			}
			if site.PassedInputs == nil {
				t.Fatalf("discovered a call site without the inputs map")
			}
		}
	})
}

// FuzzTerragruntInputKey exercises Terragrunt inputs key resolution, which previously carried an
// unreachable branch. Every key must resolve to either a static name or a clean "unresolvable".
func FuzzTerragruntInputKey(f *testing.F) {
	for _, seed := range []string{
		`inputs = { key = "value" }`,
		`inputs = { "key" = "value" }`,
		`inputs = { (var.key) = "value" }`,
		`inputs = { 1 = "value" }`,
		`inputs = { true = "value" }`,
		`inputs = { a.b = "value" }`,
		"inputs = local.inputs",
		"inputs = {",
		"",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, src string) {
		file, diags := hclsyntax.ParseConfig([]byte(src), "terragrunt.hcl", hcl.Pos{Line: 1, Column: 1})
		if diags.HasErrors() {
			return
		}
		attr, exists := file.Body.(*hclsyntax.Body).Attributes["inputs"]
		if !exists {
			return
		}
		cons, ok := attr.Expr.(*hclsyntax.ObjectConsExpr)
		if !ok {
			// Non-literal inputs are handled by the caller as incomplete analysis.
			return
		}
		for _, item := range cons.Items {
			key, resolved := terragruntInputKey(item.KeyExpr)
			if resolved && key == "" {
				t.Fatalf("a resolved key must not be empty")
			}
			if !resolved && key != "" {
				t.Fatalf("an unresolved key must be empty, got %q", key)
			}
		}
	})
}

// FuzzDiscoverJSONCallSites exercises the Terraform JSON consumer scanner against arbitrary JSON.
func FuzzDiscoverJSONCallSites(f *testing.F) {
	for _, seed := range []string{
		`{"module": {"m": {"source": "../wrapper"}}}`,
		`{"module": {"m": {"source": "../wrapper", "region": "us"}}}`,
		`{"module": {"m": "not-an-object"}}`,
		`{"output": {"o": {"value": {"${module.m.out}": null}}}}`,
		`{"provider": {"aws": {}}}`,
		`{`,
		`[]`,
		`null`,
		"",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, src string) {
		sites := discoverJSONCallSites([]byte(src), "main.tf.json", "wrapper")
		for _, site := range sites {
			if site.PassedInputs == nil {
				t.Fatalf("discovered a JSON call site without the inputs map")
			}
		}
	})
}
