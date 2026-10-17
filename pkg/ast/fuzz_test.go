package ast

import (
	"os"
	"path/filepath"
	"testing"
)

// FuzzInspect feeds arbitrary Terraform source into the full module inspection pipeline.
//
// Inspect must never panic: the CLI parses untrusted third-party module sources, so a crash on
// malformed input would be a denial-of-service bug. Any panic found here fails the fuzz target.
func FuzzInspect(f *testing.F) {
	seeds := []string{
		`variable "name" { type = string }`,
		`variable "obj" { type = object({ a = string, b = optional(number) }) }`,
		"variable \"nullable\" {\n  type     = string\n  nullable = false\n}\n",
		`module "child" { source = "./child" }`,
		`module "child" { source = "./child" }
output "id" { value = module.child.id }`,
		`output "indexed" { value = module.gke[0].outputs }`,
		`variable "a" { type = any default = null }`,
		`variable "b" { type = list(set(map(string))) }`,
		`variable "c" { nullable = local.enable }`,
		`variable "d" { validation { condition = length(var.d) > 0 } }`,
		`{"variable": {"json": {"type": "string"}}}`,
		`{"module": {"m": {"source": "./x"}}}`,
		`not valid { terraform`,
		``,
		"\x00\x01\x02",
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, source string) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(source), 0o600); err != nil {
			t.Skip("unable to materialise the fuzz input")
		}

		// A corrupt inspection cache must never break the analysis, and repeated inspections must
		// stay stable regardless of what the parser produced.
		first, err := Inspect(dir)
		if err != nil {
			return
		}
		if first == nil {
			t.Fatalf("Inspect returned a nil module without an error")
		}
		second, err := Inspect(dir)
		if err != nil {
			t.Fatalf("cached re-inspection failed: %v", err)
		}
		if len(first.Variables) != len(second.Variables) || len(first.Outputs) != len(second.Outputs) {
			t.Fatalf("re-inspection produced a different module shape")
		}
	})
}

// FuzzCheckTypeCompatibility verifies the type comparison never panics and stays self-consistent
// for arbitrary constraint strings.
func FuzzCheckTypeCompatibility(f *testing.F) {
	seeds := []struct{ old, new string }{
		{"string", "number"},
		{"any", "string"},
		{"list(string)", "set(string)"},
		{"set(string)", "list(string)"},
		{"object({ a = string })", "object({ b = number })"},
		{"list(object({ a = optional(string) }))", "map(object({ a = string }))"},
		{"", "any"},
		{"map(any)", "object({})"},
		{"object({", "})"},
		{"((((", "))))"},
	}
	for _, seed := range seeds {
		f.Add(seed.old, seed.new)
	}

	f.Fuzz(func(t *testing.T, oldType, newType string) {
		result := CheckTypeCompatibility(oldType, newType)
		if result.Compatible && result.IsBreaking {
			t.Fatalf("a compatible result must not be breaking: %+v", result)
		}
		if NormalizeType(oldType) == NormalizeType(newType) && result.IsBreaking {
			t.Fatalf("identical types must never be breaking: %q", oldType)
		}
		// Widening to `any` is always safe; narrowing from `any` is always breaking.
		if NormalizeType(newType) == "any" && NormalizeType(oldType) != "any" && result.IsBreaking {
			t.Fatalf("widening to any must not be breaking: %q -> %q", oldType, newType)
		}
		if NormalizeType(oldType) == "any" && NormalizeType(newType) != "any" && !result.IsBreaking {
			t.Fatalf("narrowing from any must be breaking: %q -> %q", oldType, newType)
		}
	})
}

// FuzzParseObjectSchema ensures object type constraints never panic and that the parsed schema is
// always derived from the original expression.
func FuzzParseObjectSchema(f *testing.F) {
	for _, seed := range []string{
		"object({ a = string })",
		"object({ a = optional(string), b = optional(number, 3) })",
		"list(object({ a = map(string) }))",
		"object({})",
		"object({",
		"object({ a = })",
		"string",
		"",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, expression string) {
		attrs, ok := ParseObjectSchema(expression)
		if !ok {
			return
		}
		for name, attr := range attrs {
			if attr.Name != name {
				t.Fatalf("attribute key %q does not match its recorded name %q", name, attr.Name)
			}
		}
	})
}

// FuzzParseModernHCLBlocks exercises the parser against modern HCL / OpenTofu blocks
// (removed, check, import, function, encryption).
func FuzzParseModernHCLBlocks(f *testing.F) {
	seeds := []string{
		`removed {
  from = aws_instance.example
  lifecycle {
    destroy = false
  }
}`,
		`removed {
  from = google_container_cluster.primary
  lifecycle {
    destroy = true
  }
}`,
		`check "health" {
  data "http" "endpoint" {
    url = "https://example.com"
  }
  assert {
    condition     = data.http.endpoint.status_code == 200
    error_message = "Status code failed"
  }
}`,
		`import {
  to = aws_s3_bucket.bucket
  id = "my-bucket-name"
}`,
		`function "slugify" {
  params = [string]
  result = string
}`,
		`encryption "aes_gcm" "key" {
  key_provider = "gcp_kms"
}`,
		"removed { from = }",
		"check { assert { } }",
		"import { }",
	}

	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, input string) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "modern.tf"), []byte(input), 0o600); err != nil {
			return
		}

		info, err := Inspect(dir)
		if err != nil {
			return
		}
		if info == nil {
			t.Fatalf("Inspect returned nil module without error for input: %s", input)
		}
	})
}
