package codemod

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

// MovedBlock describes a state address migration block in Terraform 1.1+.
type MovedBlock struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// ApplyRemovedInputs removes the specified inputs in-place from moduleName in dir.
// It returns the list of file paths that were modified.
func ApplyRemovedInputs(dir, moduleName string, inputNames []string) ([]string, error) {
	if moduleName == "" {
		return nil, fmt.Errorf("module name is required for fix")
	}
	remove := make(map[string]struct{}, len(inputNames))
	for _, name := range inputNames {
		remove[name] = struct{}{}
	}
	if len(remove) == 0 {
		return nil, nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read wrapper directory %s: %w", dir, err)
	}

	var paths []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".tf") {
			paths = append(paths, filepath.Join(dir, entry.Name()))
		}
	}
	sort.Strings(paths)

	var modified []string
	for _, path := range paths {
		// #nosec G304 -- path within inspected wrapper directory
		original, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read Terraform file %s: %w", path, err)
		}
		file, diags := hclwrite.ParseConfig(original, path, hcl.Pos{Line: 1, Column: 1})
		if diags.HasErrors() || file == nil {
			return nil, fmt.Errorf("parse Terraform file %s: %s", path, diags.Error())
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
			// #nosec G703 -- path within inspected wrapper directory
			if err := os.WriteFile(path, file.Bytes(), 0o600); err != nil {
				return nil, fmt.Errorf("write updated Terraform file %s: %w", path, err)
			}
			modified = append(modified, path)
		}
	}
	return modified, nil
}

// AddMissingInputs inserts missing required inputs with placeholder or default values into moduleName.
// It modifies the files in-place and returns the list of modified paths.
func AddMissingInputs(dir, moduleName string, missingInputs map[string]string) ([]string, error) {
	if moduleName == "" {
		return nil, fmt.Errorf("module name is required for adding missing inputs")
	}
	if len(missingInputs) == 0 {
		return nil, nil
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read wrapper directory %s: %w", dir, err)
	}

	var paths []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".tf") {
			paths = append(paths, filepath.Join(dir, entry.Name()))
		}
	}
	sort.Strings(paths)

	// Sort input keys for deterministic formatting
	keys := make([]string, 0, len(missingInputs))
	for k := range missingInputs {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var modified []string
	for _, path := range paths {
		// #nosec G304 -- path within inspected directory
		original, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read Terraform file %s: %w", path, err)
		}
		file, diags := hclwrite.ParseConfig(original, path, hcl.Pos{Line: 1, Column: 1})
		if diags.HasErrors() || file == nil {
			return nil, fmt.Errorf("parse Terraform file %s: %s", path, diags.Error())
		}
		changed := false
		for _, block := range file.Body().Blocks() {
			if block.Type() != "module" || len(block.Labels()) != 1 || block.Labels()[0] != moduleName {
				continue
			}
			for _, key := range keys {
				val := missingInputs[key]
				if block.Body().GetAttribute(key) == nil {
					block.Body().SetAttributeValue(key, cty.StringVal(val))
					changed = true
				}
			}
		}
		if changed {
			// #nosec G703 -- path within inspected directory
			if err := os.WriteFile(path, file.Bytes(), 0o600); err != nil {
				return nil, fmt.Errorf("write updated Terraform file %s: %w", path, err)
			}
			modified = append(modified, path)
		}
	}
	return modified, nil
}

// FormatMovedBlocks synthesizes Terraform 1.1+ moved blocks.
func FormatMovedBlocks(blocks []MovedBlock) string {
	if len(blocks) == 0 {
		return ""
	}
	file := hclwrite.NewEmptyFile()
	body := file.Body()

	for _, mb := range blocks {
		if strings.TrimSpace(mb.From) == "" || strings.TrimSpace(mb.To) == "" {
			continue
		}
		block := body.AppendNewBlock("moved", nil)
		blockBody := block.Body()
		blockBody.SetAttributeTraversal("from", hcl.Traversal{
			hcl.TraverseRoot{Name: mb.From},
		})
		blockBody.SetAttributeTraversal("to", hcl.Traversal{
			hcl.TraverseRoot{Name: mb.To},
		})
		body.AppendNewline()
	}
	return string(file.Bytes())
}

// WriteMovedBlocks writes or appends formatted moved blocks to targetPath.
func WriteMovedBlocks(targetPath string, blocks []MovedBlock) error {
	formatted := FormatMovedBlocks(blocks)
	if strings.TrimSpace(formatted) == "" {
		return nil
	}

	cleanPath := filepath.Clean(targetPath)
	dir := filepath.Dir(cleanPath)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("create directory for moved blocks %s: %w", dir, err)
	}

	// #nosec G304 -- operator specified target file
	existing, err := os.ReadFile(cleanPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read existing moved blocks file %s: %w", cleanPath, err)
	}

	var content []byte
	if len(existing) > 0 {
		content = append(existing, []byte("\n"+formatted)...)
	} else {
		content = []byte(formatted)
	}

	// #nosec G703 -- operator specified target file
	if err := os.WriteFile(cleanPath, content, 0o600); err != nil {
		return fmt.Errorf("write moved blocks file %s: %w", cleanPath, err)
	}
	return nil
}
