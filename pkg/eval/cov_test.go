package eval

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

func TestCoverage_TaintJSON(t *testing.T) {
	dir := t.TempDir()
	mainTF := `variable "x" { type = string }`
	_ = os.WriteFile(filepath.Join(dir, "main.tf"), []byte(mainTF), 0o644)
	jsonSrc := `{"locals":{"l":"${var.x}"},"resource":{"aws_s3_bucket":{"b":{"bucket":"${var.x}","tags":["${var.x}"]}}}}`
	_ = os.WriteFile(filepath.Join(dir, "extra.tf.json"), []byte(jsonSrc), 0o644)
	rep, err := AnalyzeVariableTaint(dir, "x")
	if err != nil {
		t.Fatal(err)
	}
	if rep.IsDeadCode {
		t.Fatal("dead code")
	}
	// preParsed with json key triggers skip branch
	parsed, diags := hclsyntax.ParseConfig([]byte(mainTF), "main.tf", hcl.Pos{Line: 1, Column: 1})
	if diags.HasErrors() {
		t.Fatal(diags)
	}
	_, _ = AnalyzeVariableTaint(dir, "x", map[string]*hclsyntax.Body{"extra.tf.json": parsed.Body.(*hclsyntax.Body)})
	// unreadable file branch: create unreadable file
	bad1 := filepath.Join(dir, "bad1.tf.json")
	_ = os.WriteFile(bad1, []byte("{bad"), 0o644)
	bad2 := filepath.Join(dir, "bad2.tf.json")
	_ = os.WriteFile(bad2, []byte("{}"), 0o644)
	_ = os.Chmod(bad2, 0o000)
	_, _ = AnalyzeVariableTaint(dir, "x")
	_ = os.Chmod(bad2, 0o644)
	// json parse error branch already covered by bad file above? AnalyzeVariableTaint skips bad json
}
