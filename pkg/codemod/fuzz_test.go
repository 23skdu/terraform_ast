package codemod

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// FuzzPreviewRemovedInputs guarantees the patch generator never mutates the wrapper it inspects and
// never panics, whatever Terraform source and input names it is handed.
func FuzzPreviewRemovedInputs(f *testing.F) {
	for _, seed := range []string{
		`module "gke" { source = "./x" }`,
		"module \"gke\" {\n  source = \"./x\"\n  obsolete = \"v\"\n  kept = \"v\"\n}\n",
		"module \"other\" { obsolete = \"v\" }",
		"module {\n",
		`{"module": {"gke": {"obsolete": "v"}}}`,
		"not valid {",
		"",
	} {
		f.Add(seed, "obsolete")
		f.Add(seed, "kept")
		f.Add(seed, "")
	}

	f.Fuzz(func(t *testing.T, source, inputName string) {
		dir := t.TempDir()
		path := filepath.Join(dir, "main.tf")
		if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
			t.Skip("unable to materialise the fuzz input")
		}
		before, err := os.ReadFile(path)
		if err != nil {
			t.Skipf("unable to read back the fuzz input: %v", err)
		}

		preview, err := PreviewRemovedInputs(dir, "gke", []string{inputName})
		if err != nil {
			return
		}
		if strings.Contains(preview, "+\n") && !strings.Contains(preview, "--- a/main.tf") {
			t.Fatalf("a patch was produced without a file header")
		}

		after, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("preview mode changed the file set: %v", err)
		}
		if string(before) != string(after) {
			t.Fatalf("preview mode modified the Terraform file")
		}
	})
}

// FuzzUnifiedReplacement checks the unified diff helper for arbitrary file bodies.
func FuzzUnifiedReplacement(f *testing.F) {
	for _, seed := range []string{"", "\n", "a\nb\n", "resource \"null_resource\" \"a\" {}", "a\r\nb\r\n"} {
		f.Add(seed, seed+" // edited")
	}

	f.Fuzz(func(t *testing.T, oldContent, newContent string) {
		patch := unifiedReplacement("main.tf", oldContent, newContent)
		if !strings.HasPrefix(patch, "--- a/main.tf\n+++ b/main.tf\n@@") {
			t.Fatalf("patch is missing a unified diff header: %q", patch)
		}
		if !strings.Contains(patch, "@@ -1,") {
			t.Fatalf("patch is missing its hunk header: %q", patch)
		}
	})
}

// FuzzFormatMovedBlocks checks moved block synthesis for arbitrary input addresses.
func FuzzFormatMovedBlocks(f *testing.F) {
	for _, seed := range []string{"module.a", "module.b", "aws_instance.web", ""} {
		f.Add(seed, seed+"_new")
	}

	f.Fuzz(func(t *testing.T, from, to string) {
		res := FormatMovedBlocks([]MovedBlock{{From: from, To: to}})
		if strings.TrimSpace(from) != "" && strings.TrimSpace(to) != "" {
			if !strings.Contains(res, "moved {") {
				t.Fatalf("expected moved block for valid addresses: %q", res)
			}
		}
	})
}
