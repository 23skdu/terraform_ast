package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExecute_Verbose(t *testing.T) {
	upstreamDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(upstreamDir, "main.tf"), []byte(`variable "x" { type = string }`), 0600))
	wrapperDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(wrapperDir, "main.tf"), []byte(`module "gke" { source = "`+upstreamDir+`" }`), 0600))
	var stdout, stderr bytes.Buffer
	code := Execute([]string{"--wrapper", wrapperDir, "--upstream", upstreamDir, "--verbose"}, &stdout, &stderr)
	assert.Equal(t, 0, code)
	t.Logf("wrapper stderr: %s", stderr.String())

	oldDir := t.TempDir()
	newDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(oldDir, "main.tf"), []byte(`variable "x" { type = string }`), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(newDir, "main.tf"), []byte(`variable "x" { type = string }`), 0600))
	stdout.Reset()
	stderr.Reset()
	code = Execute([]string{"--old", oldDir, "--new", newDir, "--verbose"}, &stdout, &stderr)
	assert.Equal(t, 0, code)
	t.Logf("compare stderr: %s", stderr.String())
}

func TestExecute_FixMissingRequiredInput_Default(t *testing.T) {
	upstreamDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(upstreamDir, "main.tf"), []byte(`variable "new_required" { type = string }`), 0600))

	wrapperDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(wrapperDir, "main.tf"), []byte(`module "gke" { source = "`+upstreamDir+`" }`), 0600))

	var stdout, stderr bytes.Buffer
	code := Execute([]string{"--wrapper", wrapperDir, "--upstream", upstreamDir, "--fix"}, &stdout, &stderr)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout.String(), "Applied fixes")
}

func TestExecute_FixMissingRequiredInput_BoolNumber(t *testing.T) {
	upstreamDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(upstreamDir, "main.tf"), []byte(`variable "b" { type = bool }
variable "n" { type = number }`), 0600))

	wrapperDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(wrapperDir, "main.tf"), []byte(`module "gke" { source = "`+upstreamDir+`" }`), 0600))

	var stdout, stderr bytes.Buffer
	code := Execute([]string{"--wrapper", wrapperDir, "--upstream", upstreamDir, "--fix"}, &stdout, &stderr)
	assert.Equal(t, 0, code)
}

func TestExecute_FixMissingRequiredInput_Refuse(t *testing.T) {
	upstreamDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(upstreamDir, "main.tf"), []byte(`variable "c" { type = list(string) }`), 0600))

	wrapperDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(wrapperDir, "main.tf"), []byte(`module "gke" { source = "`+upstreamDir+`" }`), 0600))

	var stdout, stderr bytes.Buffer
	code := Execute([]string{"--wrapper", wrapperDir, "--upstream", upstreamDir, "--fix"}, &stdout, &stderr)
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr.String(), "Cannot derive a safe placeholder")
}

func TestExecute_FixMissingRequiredInput_ApplyError(t *testing.T) {
	upstreamDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(upstreamDir, "main.tf"), []byte(`variable "new_required" { type = string }`), 0600))

	wrapperDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(wrapperDir, "main.tf"), []byte(`module "gke" { source = "`+upstreamDir+`" }`), 0600))
	require.NoError(t, os.Chmod(filepath.Join(wrapperDir, "main.tf"), 0444))
	defer func() { _ = os.Chmod(filepath.Join(wrapperDir, "main.tf"), 0644) }()

	var stdout, stderr bytes.Buffer
	code := Execute([]string{"--wrapper", wrapperDir, "--upstream", upstreamDir, "--fix"}, &stdout, &stderr)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr.String(), "Error applying missing-input fixes")
}

func TestExecute_FixPolicyViolation(t *testing.T) {
	upstreamDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(upstreamDir, "main.tf"), []byte(`variable "kept" { type = string }`), 0600))

	wrapperDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(wrapperDir, "main.tf"), []byte(`module "gke" {
  source = "`+upstreamDir+`"
  old    = "val"
}`), 0600))

	policyFile := t.TempDir() + "/policy.json"
	require.NoError(t, os.WriteFile(policyFile, []byte(`{"block_breaking":true}`), 0600))

	var stdout, stderr bytes.Buffer
	code := Execute([]string{"--wrapper", wrapperDir, "--upstream", upstreamDir, "--fix", "--policy", policyFile}, &stdout, &stderr)
	assert.Equal(t, 1, code)
}

func TestExecute_FixPreviewEmpty(t *testing.T) {
	upstreamDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(upstreamDir, "main.tf"), []byte(`# no vars`), 0600))

	wrapperDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(wrapperDir, "main.tf"), []byte(`module "gke" { source = "`+upstreamDir+`" }`), 0600))

	var stdout, stderr bytes.Buffer
	code := Execute([]string{"--wrapper", wrapperDir, "--upstream", upstreamDir, "--fix-preview"}, &stdout, &stderr)
	assert.Equal(t, 0, code)
}

func TestExecute_BlastRadiusCanSafelyUpdateFalse(t *testing.T) {
	upstreamDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(upstreamDir, "main.tf"), []byte(`# no removed_input`), 0600))

	wrapperDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(wrapperDir, "main.tf"), []byte(`
variable "removed_input" {
  type = string
}

module "gke" {
  source = "`+upstreamDir+`"
}
`), 0600))

	environmentsDir := t.TempDir()
	envDev := filepath.Join(environmentsDir, "dev")
	require.NoError(t, os.MkdirAll(envDev, 0750))
	require.NoError(t, os.WriteFile(filepath.Join(envDev, "main.tf"), []byte(`
module "my_wrapper" {
  source = "`+wrapperDir+`"
}
`), 0600))

	var stdout, stderr bytes.Buffer
	code := Execute([]string{
		"--wrapper", wrapperDir,
		"--upstream", upstreamDir,
		"--blast-radius", environmentsDir,
		"--format", "json",
	}, &stdout, &stderr)
	assert.Contains(t, stdout.String(), "total_call_sites_scanned")
	_ = code
}
