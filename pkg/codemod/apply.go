package codemod

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/23skdu/terraform_ast/pkg/ast"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
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

	var pendingWrites = make(map[string][]byte)
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
			pendingWrites[path] = file.Bytes()
			modified = append(modified, path)
		}
	}

	for path, bytes := range pendingWrites {
		// #nosec G703 -- path within inspected wrapper directory
		if err := os.WriteFile(path, bytes, 0o600); err != nil {
			return nil, fmt.Errorf("write updated Terraform file %s: %w", path, err)
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

	var pendingWrites = make(map[string][]byte)
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
					var ctyVal cty.Value
					if val == "true" {
						ctyVal = cty.BoolVal(true)
					} else if val == "false" {
						ctyVal = cty.BoolVal(false)
					} else if num, err := strconv.ParseInt(val, 10, 64); err == nil {
						ctyVal = cty.NumberIntVal(num)
					} else {
						ctyVal = cty.StringVal(val)
					}
					block.Body().SetAttributeValue(key, ctyVal)
					changed = true
				}
			}
		}
		if changed {
			pendingWrites[path] = file.Bytes()
			modified = append(modified, path)
		}
	}

	for path, bytes := range pendingWrites {
		// #nosec G703 -- path within inspected directory
		if err := os.WriteFile(path, bytes, 0o600); err != nil {
			return nil, fmt.Errorf("write updated Terraform file %s: %w", path, err)
		}
	}
	return modified, nil
}

func cleanAddressForMoved(addr string) string {
	res := strings.TrimSpace(addr)
	res = strings.ReplaceAll(res, "[count]", "")
	res = strings.ReplaceAll(res, "[for_each]", "")
	return res
}

func parseAddressTraversal(addr string) hcl.Traversal {
	cleaned := cleanAddressForMoved(addr)
	traversal, diags := hclsyntax.ParseTraversalAbs([]byte(cleaned), "", hcl.Pos{})
	if diags.HasErrors() || len(traversal) == 0 {
		return hcl.Traversal{hcl.TraverseRoot{Name: cleaned}}
	}
	return traversal
}

// FormatMovedBlocks synthesizes Terraform 1.1+ moved blocks.
func FormatMovedBlocks(blocks []MovedBlock) string {
	if len(blocks) == 0 {
		return ""
	}
	file := hclwrite.NewEmptyFile()
	body := file.Body()

	for _, mb := range blocks {
		cleanFrom := cleanAddressForMoved(mb.From)
		cleanTo := cleanAddressForMoved(mb.To)
		if cleanFrom == "" || cleanTo == "" {
			continue
		}
		block := body.AppendNewBlock("moved", nil)
		blockBody := block.Body()
		blockBody.SetAttributeTraversal("from", parseAddressTraversal(cleanFrom))
		blockBody.SetAttributeTraversal("to", parseAddressTraversal(cleanTo))
		body.AppendNewline()
	}
	return string(file.Bytes())
}

// WriteMovedBlocks writes or appends formatted moved blocks to targetPath idempotently.
func WriteMovedBlocks(targetPath string, blocks []MovedBlock) error {
	if len(blocks) == 0 {
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

	existingStr := string(existing)
	var newBlocks []MovedBlock
	for _, mb := range blocks {
		if strings.TrimSpace(mb.From) == "" || strings.TrimSpace(mb.To) == "" {
			continue
		}
		fromPattern := "from = " + mb.From
		toPattern1 := "to   = " + mb.To
		toPattern2 := "to = " + mb.To
		if strings.Contains(existingStr, fromPattern) && (strings.Contains(existingStr, toPattern1) || strings.Contains(existingStr, toPattern2)) {
			continue
		}
		newBlocks = append(newBlocks, mb)
	}
	if len(newBlocks) == 0 {
		return nil
	}

	formatted := FormatMovedBlocks(newBlocks)

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

// ResourceTransform describes a resource rename or split operation.
type ResourceTransform struct {
	From string   `json:"from"`
	To   []string `json:"to"`
}

// MovedBlocks expands a transform into one moved block per target address.
func (t ResourceTransform) MovedBlocks() []MovedBlock {
	blocks := make([]MovedBlock, 0, len(t.To))
	for _, to := range t.To {
		if strings.TrimSpace(t.From) == "" || strings.TrimSpace(to) == "" {
			continue
		}
		blocks = append(blocks, MovedBlock{From: t.From, To: to})
	}
	return blocks
}

// SynthesizeTransformMovedBlocks converts resource transforms into moved blocks.
func SynthesizeTransformMovedBlocks(transforms []ResourceTransform) []MovedBlock {
	var blocks []MovedBlock
	for _, t := range transforms {
		blocks = append(blocks, t.MovedBlocks()...)
	}
	sort.Slice(blocks, func(i, j int) bool {
		if blocks[i].From != blocks[j].From {
			return blocks[i].From < blocks[j].From
		}
		return blocks[i].To < blocks[j].To
	})
	return blocks
}

// LoadResourceTransforms reads a JSON transform specification from disk.
func LoadResourceTransforms(path string) ([]ResourceTransform, error) {
	// #nosec G304 -- operator specified transform file
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("read transform file %s: %w", path, err)
	}
	var spec struct {
		Transforms []ResourceTransform `json:"transforms"`
	}
	if err := json.Unmarshal(data, &spec); err != nil {
		return nil, fmt.Errorf("decode transform file %s: %w", path, err)
	}
	if len(spec.Transforms) == 0 {
		return nil, fmt.Errorf("transform file %s contains no transforms", path)
	}
	for i, transform := range spec.Transforms {
		if strings.TrimSpace(transform.From) == "" {
			return nil, fmt.Errorf("transform file %s entry %d has an empty from address", path, i)
		}
		if len(transform.To) == 0 {
			return nil, fmt.Errorf("transform file %s entry %d has no to addresses", path, i)
		}
		for _, to := range transform.To {
			if strings.TrimSpace(to) == "" {
				return nil, fmt.Errorf("transform file %s entry %d has an empty to address", path, i)
			}
		}
		if len(transform.To) > 1 {
			// A one-to-many moved{} block is not valid Terraform. Reject the spec instead of
			// emitting a patch that can never apply; see the roadmap for split support.
			return nil, fmt.Errorf("transform file %s entry %d is a split transform, which is not supported", path, i)
		}
	}
	return spec.Transforms, nil
}
func CoveredMovedAddresses(blocks []ast.MovedBlockDef) map[string]struct{} {
	covered := make(map[string]struct{})
	for _, mb := range blocks {
		covered[ast.NormalizeMovedKey(mb.From, mb.To)] = struct{}{}
	}
	return covered
}

// SynthesizeMovedBlocksForShifts translates detected AST resource address shifts into Terraform 1.1+ moved blocks.
// Addresses already covered by an upstream moved {} block (passed via covered) are skipped so --fix
// does not write a duplicate.
func SynthesizeMovedBlocksForShifts(shifts []ast.ResourceAddressShift, covered ...map[string]struct{}) []MovedBlock {
	var blocks []MovedBlock
	seen := make(map[string]struct{})
	var coveredSet map[string]struct{}
	if len(covered) > 0 {
		coveredSet = covered[0]
	}

	for _, s := range shifts {
		from := cleanAddressForMoved(s.OldAddress.FullAddress())
		to := cleanAddressForMoved(s.NewAddress.FullAddress())
		if from == "" || to == "" || from == to {
			continue
		}
		key := ast.NormalizeMovedKey(from, to)
		if coveredSet != nil {
			if _, ok := coveredSet[key]; ok {
				continue
			}
		}
		if _, exists := seen[key]; !exists {
			seen[key] = struct{}{}
			blocks = append(blocks, MovedBlock{
				From: from,
				To:   to,
			})
		}
	}

	sort.Slice(blocks, func(i, j int) bool {
		return blocks[i].From < blocks[j].From
	})

	return blocks
}

// SynthesizeAndWriteMovedBlocks converts address shifts into moved blocks and writes them idempotently to targetPath.
func SynthesizeAndWriteMovedBlocks(targetPath string, shifts []ast.ResourceAddressShift, covered ...map[string]struct{}) error {
	blocks := SynthesizeMovedBlocksForShifts(shifts, covered...)
	return WriteMovedBlocks(targetPath, blocks)
}

// ImportBlock describes a Terraform 1.5+ import {} block for codemod output.
type ImportBlock struct {
	To       string `json:"to"`
	ID       string `json:"id"`
	Provider string `json:"provider,omitempty"`
}

// NormalizeImportBlocks trims, de-duplicates, and deterministically orders import
// blocks. It is shared by preview and apply so the two paths cannot disagree.
func NormalizeImportBlocks(blocks []ImportBlock) []ImportBlock {
	seen := make(map[string]struct{}, len(blocks))
	normalized := make([]ImportBlock, 0, len(blocks))
	for _, ib := range blocks {
		ib.To = strings.TrimSpace(ib.To)
		ib.ID = strings.TrimSpace(ib.ID)
		ib.Provider = strings.TrimSpace(ib.Provider)
		if ib.To == "" || ib.ID == "" {
			continue
		}
		key := ib.To + "\x00" + ib.ID + "\x00" + ib.Provider
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		normalized = append(normalized, ib)
	}
	sort.Slice(normalized, func(i, j int) bool {
		if normalized[i].To != normalized[j].To {
			return normalized[i].To < normalized[j].To
		}
		if normalized[i].ID != normalized[j].ID {
			return normalized[i].ID < normalized[j].ID
		}
		return normalized[i].Provider < normalized[j].Provider
	})
	return normalized
}

// FormatImportBlocks synthesizes Terraform 1.5+ import blocks.
func FormatImportBlocks(blocks []ImportBlock) string {
	blocks = NormalizeImportBlocks(blocks)
	if len(blocks) == 0 {
		return ""
	}
	file := hclwrite.NewEmptyFile()
	body := file.Body()

	for _, ib := range blocks {
		// NormalizeImportBlocks has already dropped incomplete entries.
		block := body.AppendNewBlock("import", nil)
		blockBody := block.Body()
		blockBody.SetAttributeTraversal("to", parseAddressTraversal(ib.To))
		blockBody.SetAttributeValue("id", cty.StringVal(ib.ID))
		if strings.TrimSpace(ib.Provider) != "" {
			blockBody.SetAttributeTraversal("provider", parseAddressTraversal(ib.Provider))
		}
		body.AppendNewline()
	}
	return string(file.Bytes())
}

// WriteImportBlocks writes or appends formatted import blocks to targetPath idempotently.
func WriteImportBlocks(targetPath string, blocks []ImportBlock) error {
	_, err := WriteImportBlocksIfChanged(targetPath, blocks)
	return err
}

// WriteImportBlocksIfChanged behaves like WriteImportBlocks and additionally reports
// whether the file was actually modified, so callers do not list an untouched file as fixed.
func WriteImportBlocksIfChanged(targetPath string, blocks []ImportBlock) (bool, error) {
	blocks = NormalizeImportBlocks(blocks)
	if len(blocks) == 0 {
		return false, nil
	}
	cleanPath := filepath.Clean(targetPath)
	dir := filepath.Dir(cleanPath)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return false, fmt.Errorf("create directory for import blocks %s: %w", dir, err)
	}

	// #nosec G304 -- operator specified target file
	existing, err := os.ReadFile(cleanPath)
	if err != nil && !os.IsNotExist(err) {
		return false, fmt.Errorf("read existing import blocks file %s: %w", cleanPath, err)
	}

	existingStr := string(existing)
	var newBlocks []ImportBlock
	for _, ib := range blocks {
		if strings.Contains(existingStr, ib.To) && strings.Contains(existingStr, ib.ID) {
			continue
		}
		newBlocks = append(newBlocks, ib)
	}
	if len(newBlocks) == 0 {
		return false, nil
	}

	formatted := FormatImportBlocks(newBlocks)

	var content []byte
	if len(existing) > 0 {
		content = append(existing, []byte("\n"+formatted)...)
	} else {
		content = []byte(formatted)
	}

	// #nosec G703 -- operator specified target file
	if err := os.WriteFile(cleanPath, content, 0o600); err != nil {
		return false, fmt.Errorf("write import blocks file %s: %w", cleanPath, err)
	}
	return true, nil
}
