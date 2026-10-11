package codemod

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclwrite"
)

// PreviewRemovedInputs returns a unified patch that removes only the named
// arguments from a wrapper's module call. It never modifies files.
func PreviewRemovedInputs(dir, moduleName string, inputNames []string) (string, error) {
	if moduleName == "" {
		return "", fmt.Errorf("module name is required for fix preview")
	}
	remove := make(map[string]struct{}, len(inputNames))
	for _, name := range inputNames {
		remove[name] = struct{}{}
	}
	if len(remove) == 0 {
		return "", nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("read wrapper directory %s: %w", dir, err)
	}
	var paths []string
	for _, entry := range entries {
		if !entry.IsDir() && (strings.HasSuffix(entry.Name(), ".tf") || strings.HasSuffix(entry.Name(), ".tf.json")) {
			paths = append(paths, filepath.Join(dir, entry.Name()))
		}
	}
	sort.Strings(paths)

	var patches []string
	for _, path := range paths {
		original, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("read Terraform file %s: %w", path, err)
		}
		if strings.HasSuffix(path, ".tf.json") {
			continue
		}
		file, diags := hclwrite.ParseConfig(original, path, hcl.Pos{Line: 1, Column: 1})
		if diags.HasErrors() || file == nil {
			return "", fmt.Errorf("parse Terraform file %s: %s", path, diags.Error())
		}
		changed := false
		for _, block := range file.Body().Blocks() {
			if block.Type() != "module" || len(block.Labels()) != 1 || block.Labels()[0] != moduleName {
				continue
			}
			for name := range remove {
				if block.Body().GetAttribute(name) != nil {
					block.Body().RemoveAttribute(name)
					changed = true
				}
			}
		}
		if changed {
			patches = append(patches, unifiedReplacement(filepath.Base(path), string(original), string(file.Bytes())))
		}
	}
	if len(patches) == 0 {
		return "", nil
	}
	return strings.Join(patches, "\n"), nil
}

func unifiedReplacement(name, oldContent, newContent string) string {
	oldLines := strings.Split(strings.TrimSuffix(oldContent, "\n"), "\n")
	newLines := strings.Split(strings.TrimSuffix(newContent, "\n"), "\n")
	if len(oldLines) == 1 && oldLines[0] == "" {
		oldLines = nil
	}
	if len(newLines) == 1 && newLines[0] == "" {
		newLines = nil
	}
	var patch strings.Builder
	fmt.Fprintf(&patch, "--- a/%s\n+++ b/%s\n@@ -1,%d +1,%d @@\n", name, name, len(oldLines), len(newLines))
	for _, line := range oldLines {
		fmt.Fprintf(&patch, "-%s\n", line)
	}
	for _, line := range newLines {
		fmt.Fprintf(&patch, "+%s\n", line)
	}
	return patch.String()
}
