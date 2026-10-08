package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func createTestTFRepo(t *testing.T, tag string, tfContent string) string {
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	require.NoError(t, err)

	w, err := repo.Worktree()
	require.NoError(t, err)

	mainTF := filepath.Join(dir, "main.tf")
	require.NoError(t, os.WriteFile(mainTF, []byte(tfContent), 0644))

	_, err = w.Add("main.tf")
	require.NoError(t, err)

	sig := &object.Signature{
		Name:  "Test",
		Email: "test@example.com",
		When:  time.Now(),
	}

	commit, err := w.Commit("Initial commit", &git.CommitOptions{
		Author:    sig,
		Committer: sig,
	})
	require.NoError(t, err)

	if tag != "" {
		_, err = repo.CreateTag(tag, commit, nil)
		require.NoError(t, err)
	}

	return dir
}

func TestExecute_FlagErrors(t *testing.T) {
	var stdout, stderr bytes.Buffer

	// Invalid flag
	code := Execute([]string{"--unrecognized"}, &stdout, &stderr)
	assert.Equal(t, 2, code)

	// Missing flags
	stdout.Reset()
	stderr.Reset()
	code = Execute([]string{}, &stdout, &stderr)
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr.String(), "Must specify either --wrapper")

	// Missing --new flag
	stdout.Reset()
	stderr.Reset()
	code = Execute([]string{"--old", "some_dir"}, &stdout, &stderr)
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr.String(), "Both --old and --new must be specified")
}

func TestExecute_CompareMode(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()

	tf1 := `
variable "cluster_name" {
  type = string
}
`
	tf2 := `
variable "cluster_name" {
  type = string
}
variable "region" {
  type    = string
  default = "us-central1"
}
`
	require.NoError(t, os.WriteFile(filepath.Join(dir1, "main.tf"), []byte(tf1), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir2, "main.tf"), []byte(tf2), 0644))

	var stdout, stderr bytes.Buffer

	// 1. Text format to stdout
	code := Execute([]string{"--old", dir1, "--new", dir2}, &stdout, &stderr)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout.String(), "TERRAFORM AST COMPATIBILITY DIFF REPORT")

	// 2. Markdown format to stdout
	stdout.Reset()
	code = Execute([]string{"--old", dir1, "--new", dir2, "--format", "markdown"}, &stdout, &stderr)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout.String(), "# Terraform AST Compatibility Diff Report")

	// 3. JSON format to file with exit-code flag
	outFile := filepath.Join(t.TempDir(), "report.json")
	code = Execute([]string{"--old", dir1, "--new", dir2, "--format", "json", "--output", outFile, "--exit-code"}, &stdout, &stderr)
	assert.Equal(t, 0, code)
	content, err := os.ReadFile(outFile)
	require.NoError(t, err)
	assert.Contains(t, string(content), `"region"`)

	// 4. Error on non-existent old module
	stderr.Reset()
	code = Execute([]string{"--old", "/non/existent/dir", "--new", dir2}, &stdout, &stderr)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr.String(), "Error inspecting old module")

	// 5. Error on non-existent new module
	stderr.Reset()
	code = Execute([]string{"--old", dir1, "--new", "/non/existent/dir"}, &stdout, &stderr)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr.String(), "Error inspecting new module")

	// 6. Breaking change with exit-code returns 1
	dirBreaking := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dirBreaking, "main.tf"), []byte(`variable "empty" {}`), 0644))
	code = Execute([]string{"--old", dir1, "--new", dirBreaking, "--exit-code"}, &stdout, &stderr)
	assert.Equal(t, 1, code)
}

func TestExecute_WrapperMode(t *testing.T) {
	wrapperDir := t.TempDir()
	upstreamDir := t.TempDir()

	upstreamTF := `
variable "project_id" {
  type = string
}
output "endpoint" {
  value = "https://cluster"
}
`
	wrapperTF := `
module "gke" {
  source     = "` + upstreamDir + `"
  project_id = "my-proj"
}

output "cluster_endpoint" {
  value = module.gke.endpoint
}
`
	require.NoError(t, os.WriteFile(filepath.Join(upstreamDir, "main.tf"), []byte(upstreamTF), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(wrapperDir, "main.tf"), []byte(wrapperTF), 0644))

	var stdout, stderr bytes.Buffer

	// 1. Deduce upstream from wrapper module call
	code := Execute([]string{"--wrapper", wrapperDir}, &stdout, &stderr)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout.String(), "TERRAFORM WRAPPER COMPATIBILITY & PIN AUDIT")

	// 2. Markdown format
	stdout.Reset()
	code = Execute([]string{"--wrapper", wrapperDir, "--upstream", upstreamDir, "--format", "markdown"}, &stdout, &stderr)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout.String(), "# Terraform Wrapper Compatibility Audit")

	// 3. JSON format
	stdout.Reset()
	code = Execute([]string{"--wrapper", wrapperDir, "--upstream", upstreamDir, "--format", "json"}, &stdout, &stderr)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout.String(), `"verdict"`)

	// 4. Wrapper error on non-existent dir
	stderr.Reset()
	code = Execute([]string{"--wrapper", "/non/existent/dir"}, &stdout, &stderr)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr.String(), "Error inspecting wrapper module")

	// 5. Wrapper without module calls and no upstream flag
	emptyWrapper := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(emptyWrapper, "main.tf"), []byte(`variable "foo" {}`), 0644))
	stderr.Reset()
	code = Execute([]string{"--wrapper", emptyWrapper}, &stdout, &stderr)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr.String(), "upstream module path not specified")

	// 6. Upstream inspecting error
	stderr.Reset()
	code = Execute([]string{"--wrapper", emptyWrapper, "--upstream", "/non/existent/upstream"}, &stdout, &stderr)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr.String(), "Error inspecting target upstream module")

	// 7. Breaking wrapper with exit-code
	badUpstream := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(badUpstream, "main.tf"), []byte(`variable "other" { type = string }`), 0644))
	code = Execute([]string{"--wrapper", wrapperDir, "--upstream", badUpstream, "--exit-code"}, &stdout, &stderr)
	assert.Equal(t, 1, code)
}

func TestExecute_GitReposAndVersions(t *testing.T) {
	tfV1 := `variable "cluster_name" { type = string }`
	tfV2 := `variable "cluster_name" { type = string }`

	repoV1 := createTestTFRepo(t, "v1.0.0", tfV1)
	repoV2 := createTestTFRepo(t, "v2.0.0", tfV2)

	// Add v1.0.0 tag to repoV2 as well so oldVersion checkout succeeds
	r2, err := git.PlainOpen(repoV2)
	require.NoError(t, err)
	head, err := r2.Head()
	require.NoError(t, err)
	_, err = r2.CreateTag("v1.0.0", head.Hash(), nil)
	require.NoError(t, err)

	var stdout, stderr bytes.Buffer
	code := Execute([]string{
		"--old", repoV1,
		"--new", repoV2,
		"--old-version", "v1.0.0",
		"--new-version", "v2.0.0",
	}, &stdout, &stderr)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout.String(), "v1.0.0 -> v2.0.0")

	// Wrapper mode with old and new versions on git repo
	wrapperDir := t.TempDir()
	wrapperTF := `
module "gke" {
  cluster_name = "test"
}
`
	require.NoError(t, os.WriteFile(filepath.Join(wrapperDir, "main.tf"), []byte(wrapperTF), 0644))

	stdout.Reset()
	code = Execute([]string{
		"--wrapper", wrapperDir,
		"--upstream", repoV2,
		"--new-version", "v2.0.0",
		"--old-version", "v1.0.0",
	}, &stdout, &stderr)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout.String(), "SAFE_TO_PIN")
}

func TestMainFunc(t *testing.T) {
	origArgs := os.Args
	origExit := osExit
	defer func() {
		os.Args = origArgs
		osExit = origExit
	}()

	var capturedExit int
	osExit = func(code int) {
		capturedExit = code
	}

	os.Args = []string{"tfastdiff", "--invalid"}
	main()
	assert.Equal(t, 2, capturedExit)
}

func TestExecute_GitErrorBranches(t *testing.T) {
	repo := createTestTFRepo(t, "v1.0.0", "variable \"x\" {}")
	var stdout, stderr bytes.Buffer

	// 1. Invalid old version in compare mode
	code := Execute([]string{
		"--old", repo,
		"--new", repo,
		"--old-version", "invalid-old-ref",
		"--new-version", "v1.0.0",
	}, &stdout, &stderr)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr.String(), "Error checking out old version")

	// 2. Invalid new version in compare mode
	stderr.Reset()
	code = Execute([]string{
		"--old", repo,
		"--new", repo,
		"--old-version", "v1.0.0",
		"--new-version", "invalid-new-ref",
	}, &stdout, &stderr)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr.String(), "Error checking out new version")

	// 3. Invalid new version in wrapper mode
	wrapperDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(wrapperDir, "main.tf"), []byte(`module "gke" { source = "`+repo+`" }`), 0644))
	stderr.Reset()
	code = Execute([]string{
		"--wrapper", wrapperDir,
		"--upstream", repo,
		"--new-version", "invalid-upstream-ref",
	}, &stdout, &stderr)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr.String(), "Error checking out target upstream version")

	// 4. Invalid output file in compare mode
	stderr.Reset()
	code = Execute([]string{
		"--old", repo,
		"--new", repo,
		"--output", "/invalid/nonexistent/path/out.txt",
	}, &stdout, &stderr)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr.String(), "Error writing output")

	// 5. Invalid output file in wrapper mode
	stderr.Reset()
	code = Execute([]string{
		"--wrapper", wrapperDir,
		"--upstream", repo,
		"--output", "/invalid/nonexistent/path/out.txt",
	}, &stdout, &stderr)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr.String(), "Error writing output")

	// 6. Invalid oldVersion in wrapper mode (failed ExtractVersionToDir)
	stderr.Reset()
	code = Execute([]string{
		"--wrapper", wrapperDir,
		"--upstream", repo,
		"--old-version", "invalid-old-ref",
	}, &stdout, &stderr)
	assert.Equal(t, 0, code)

	// 7. Test mkdirTemp failure in wrapper and compare modes
	origMkdir := mkdirTemp
	defer func() { mkdirTemp = origMkdir }()
	mkdirTemp = func(dir, pattern string) (string, error) {
		return "", assert.AnError
	}

	stderr.Reset()
	code = Execute([]string{
		"--wrapper", wrapperDir,
		"--upstream", repo,
		"--new-version", "v1.0.0",
	}, &stdout, &stderr)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr.String(), "Error creating temp dir")

	stderr.Reset()
	code = Execute([]string{
		"--wrapper", wrapperDir,
		"--upstream", repo,
		"--old-version", "v1.0.0",
	}, &stdout, &stderr)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr.String(), "Error creating temp dir")

	stderr.Reset()
	code = Execute([]string{
		"--old", repo,
		"--new", repo,
		"--old-version", "v1.0.0",
	}, &stdout, &stderr)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr.String(), "Error creating temp dir")

	stderr.Reset()
	code = Execute([]string{
		"--old", repo,
		"--new", repo,
		"--new-version", "v1.0.0",
	}, &stdout, &stderr)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr.String(), "Error creating temp dir")
}

func TestHelpers(t *testing.T) {
	assert.True(t, isGitURL("https://github.com/org/repo.git"))
	assert.True(t, isGitURL("http://github.com/org/repo.git"))
	assert.True(t, isGitURL("git@github.com:org/repo.git"))
	assert.True(t, isGitURL("git::https://github.com/org/repo.git"))
	assert.False(t, isGitURL("/local/path"))

	tmpDir := t.TempDir()
	assert.False(t, isGitRepo("/non/existent/path"))
	assert.False(t, isGitRepo(tmpDir))

	repoDir := createTestTFRepo(t, "v1.0.0", "variable \"x\" {}")
	assert.True(t, isGitRepo(repoDir))

	// writeOutput error
	var buf bytes.Buffer
	err := writeOutput("/invalid/directory/that/does/not/exist/out.txt", "hello", &buf)
	assert.Error(t, err)
}

func TestExecute_SARIFFormat(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(dir1, "main.tf"), []byte(`variable "x" { type = string }`), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(dir2, "main.tf"), []byte(`variable "x" { type = list(string) }`), 0600))

	var stdout, stderr bytes.Buffer

	// Compare mode with SARIF
	code := Execute([]string{"--old", dir1, "--new", dir2, "--format", "sarif"}, &stdout, &stderr)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout.String(), "sarif-schema-2.1.0.json")
	assert.Contains(t, stdout.String(), "TF-DIFF-BREAKING-CHANGE")

	// Wrapper mode with SARIF
	wrapperDir := t.TempDir()
	wrapperTF := `
module "mod" {
  source = "` + dir1 + `"
  x      = "val"
}
`
	require.NoError(t, os.WriteFile(filepath.Join(wrapperDir, "main.tf"), []byte(wrapperTF), 0600))

	stdout.Reset()
	stderr.Reset()
	code = Execute([]string{
		"--wrapper", wrapperDir,
		"--upstream", dir2,
		"--format", "sarif",
	}, &stdout, &stderr)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout.String(), "sarif-schema-2.1.0.json")
}

func TestExecute_BlastRadiusAndRecreation(t *testing.T) {
	upstreamDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(upstreamDir, "main.tf"), []byte(`
variable "ip_range_services" {
  type = string
}
`), 0600))

	wrapperDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(wrapperDir, "main.tf"), []byte(`
module "gke" {
  source = "` + upstreamDir + `"
}
`), 0600))

	environmentsDir := t.TempDir()
	envDev := filepath.Join(environmentsDir, "dev")
	require.NoError(t, os.MkdirAll(envDev, 0750))
	require.NoError(t, os.WriteFile(filepath.Join(envDev, "main.tf"), []byte(`
module "my_wrapper" {
  source = "` + wrapperDir + `"
}
`), 0600))

	var stdout, stderr bytes.Buffer

	// Test with blast-radius and markdown recreation
	code := Execute([]string{
		"--wrapper", wrapperDir,
		"--upstream", upstreamDir,
		"--blast-radius", environmentsDir,
		"--format", "markdown",
		"--check-recreation=true",
	}, &stdout, &stderr)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout.String(), "Blast-Radius Downstream Consumer Analysis")

	// Test with blast-radius and JSON format
	stdout.Reset()
	stderr.Reset()
	code = Execute([]string{
		"--wrapper", wrapperDir,
		"--upstream", upstreamDir,
		"--blast-radius", environmentsDir,
		"--format", "json",
	}, &stdout, &stderr)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout.String(), "total_call_sites_scanned")

	// Error scanning blast-radius
	stdout.Reset()
	stderr.Reset()
	code = Execute([]string{
		"--wrapper", wrapperDir,
		"--upstream", upstreamDir,
		"--blast-radius", "/invalid/nonexistent/dir",
	}, &stdout, &stderr)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr.String(), "Error scanning blast radius")
}

