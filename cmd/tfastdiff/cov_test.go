package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestCoverage_SVG(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir1, "main.tf"), []byte(`variable "a" { type = string }`), 0644)
	_ = os.WriteFile(filepath.Join(dir2, "main.tf"), []byte(`variable "a" { type = string }`), 0644)
	svgBefore := filepath.Join(t.TempDir(), "before.svg")
	svgAfter := filepath.Join(t.TempDir(), "after.svg")
	svgTrans := filepath.Join(t.TempDir(), "trans.svg")
	var stdout, stderr bytes.Buffer
	_ = stdout
	_ = stderr
	code := Execute([]string{"--old", dir1, "--new", dir2, "--svg-before", svgBefore, "--svg-after", svgAfter, "--svg-transition", svgTrans}, &stdout, &stderr)
	if code != 0 {
		t.Fatal(stderr.String())
	}
	// error branches: invalid path
	bad := filepath.Join(t.TempDir(), "no", "dir", "x.svg")
	code = Execute([]string{"--old", dir1, "--new", dir2, "--svg-before", bad}, &stdout, &stderr)
	if code == 0 {
		t.Fatal("expected error")
	}
	code = Execute([]string{"--old", dir1, "--new", dir2, "--svg-after", bad}, &stdout, &stderr)
	if code == 0 {
		t.Fatal("expected error")
	}
	code = Execute([]string{"--old", dir1, "--new", dir2, "--svg-transition", bad}, &stdout, &stderr)
	if code == 0 {
		t.Fatal("expected error")
	}
}
