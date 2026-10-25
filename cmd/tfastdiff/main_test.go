package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/23skdu/terraform_ast/pkg/ast"
	"github.com/23skdu/terraform_ast/pkg/eval"
	"github.com/23skdu/terraform_ast/pkg/gitops"
	"github.com/23skdu/terraform_ast/pkg/registry"
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

// createTestTFRepoWithTags builds one repository carrying several tagged releases, which is the
// realistic shape of an --old-version/--new-version pair. Asking a repository for a tag it does
// not have used to be swallowed by the CLI, so a vacuous pinned diff passed as a real one.
func createTestTFRepoWithTags(t *testing.T, releases []struct{ tag, content string }) string {
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	require.NoError(t, err)
	w, err := repo.Worktree()
	require.NoError(t, err)

	mainTF := filepath.Join(dir, "main.tf")
	sig := &object.Signature{Name: "Test", Email: "test@example.com", When: time.Now()}

	for _, release := range releases {
		require.NoError(t, os.WriteFile(mainTF, []byte(release.content), 0644))
		_, err = w.Add("main.tf")
		require.NoError(t, err)
		commit, err := w.Commit("release "+release.tag, &git.CommitOptions{Author: sig, Committer: sig})
		require.NoError(t, err)
		_, err = repo.CreateTag(release.tag, commit, nil)
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
	assert.Contains(t, stderr.String(), "inspecting old module")

	// 5. Error on non-existent new module
	stderr.Reset()
	code = Execute([]string{"--old", dir1, "--new", "/non/existent/dir"}, &stdout, &stderr)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr.String(), "inspecting new module")

	// 6. Breaking change with exit-code returns 1
	dirBreaking := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dirBreaking, "main.tf"), []byte(`variable "empty" {}`), 0644))
	code = Execute([]string{"--old", dir1, "--new", dirBreaking, "--exit-code"}, &stdout, &stderr)
	assert.Equal(t, 1, code)
}

func TestExecute_PolicyGate(t *testing.T) {
	oldDir := t.TempDir()
	newDir := t.TempDir()
	policyFile := filepath.Join(t.TempDir(), "policy.json")
	require.NoError(t, os.WriteFile(filepath.Join(oldDir, "main.tf"), []byte(`variable "name" { type = string }`), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(newDir, "main.tf"), []byte(`variable "name" { type = string }
variable "region" {
  type    = string
  default = "west"
}`), 0644))
	require.NoError(t, os.WriteFile(policyFile, []byte(`{"max_bump":"PATCH"}`), 0600))

	var stdout, stderr bytes.Buffer
	code := Execute([]string{
		"--old", oldDir, "--new", newDir,
		"--old-version", "1.0.0", "--new-version", "1.1.0",
		"--policy", policyFile, "--format", "json",
	}, &stdout, &stderr)
	assert.Equal(t, 1, code)
	assert.Contains(t, stdout.String(), `"rule": "max_bump"`)
	assert.Contains(t, stdout.String(), "allows at most a PATCH")

	require.NoError(t, os.WriteFile(policyFile, []byte(`{"not_a_rule":true}`), 0600))
	stderr.Reset()
	code = Execute([]string{"--old", oldDir, "--new", newDir, "--policy", policyFile}, &stdout, &stderr)
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr.String(), "Error loading policy")
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

func TestExecute_FixPreviewDoesNotModifyWrapper(t *testing.T) {
	wrapperDir := t.TempDir()
	upstreamDir := t.TempDir()
	wrapperFile := filepath.Join(wrapperDir, "main.tf")
	wrapperSource := `module "upstream" {
  source = "` + upstreamDir + `"
  removed = "value"
  retained = "value"
}
`
	require.NoError(t, os.WriteFile(wrapperFile, []byte(wrapperSource), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(upstreamDir, "main.tf"), []byte(`variable "retained" { type = string }`), 0600))

	var stdout, stderr bytes.Buffer
	code := Execute([]string{"--wrapper", wrapperDir, "--upstream", upstreamDir, "--fix-preview"}, &stdout, &stderr)
	require.Equal(t, 0, code, stderr.String())
	assert.Contains(t, stdout.String(), "--- a/main.tf")
	assert.Contains(t, stdout.String(), "-  removed = \"value\"")
	assert.NotContains(t, stdout.String(), "+  removed = \"value\"")

	after, err := os.ReadFile(wrapperFile)
	require.NoError(t, err)
	assert.Equal(t, wrapperSource, string(after))
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

	// 6. Invalid oldVersion in wrapper mode now fails loudly: silently continuing would drop
	// the upstream-to-upstream diff and therefore downgrade detection and the safe-pin
	// recommendation, while still reporting the target as safe to pin.
	stderr.Reset()
	stdout.Reset()
	code = Execute([]string{
		"--wrapper", wrapperDir,
		"--upstream", repo,
		"--old-version", "invalid-old-ref",
	}, &stdout, &stderr)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr.String(), "Error checking out pinned upstream version invalid-old-ref")
	assert.Empty(t, stdout.String())

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
	assert.Contains(t, stderr.String(), "Error checking out target upstream version")

	stderr.Reset()
	code = Execute([]string{
		"--old", repo,
		"--new", repo,
		"--old-version", "v1.0.0",
	}, &stdout, &stderr)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr.String(), "Error checking out old version")

	stderr.Reset()
	code = Execute([]string{
		"--old", repo,
		"--new", repo,
		"--new-version", "v1.0.0",
	}, &stdout, &stderr)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr.String(), "Error checking out new version")
}

func TestHelpers(t *testing.T) {
	assert.True(t, isGitURL("https://github.com/org/repo.git"))
	assert.True(t, isGitURL("http://github.com/org/repo.git"))
	assert.True(t, isGitURL("git@github.com:org/repo.git"))
	assert.True(t, isGitURL("git::https://github.com/org/repo.git"))
	assert.True(t, isGitURL("file:///srv/modules/repo"))
	assert.True(t, isGitURL("github.com/org/repo"))
	assert.True(t, isGitURL("bitbucket.org/org/repo"))
	assert.True(t, isGitURL("git://github.com/org/repo.git"))
	assert.True(t, isGitURL("ssh://git@github.com/org/repo"))
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
variable "ip_range_services" {
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

func setupMockRegistryServer(t *testing.T) (*httptest.Server, func()) {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	content := []byte(`variable "cluster_name" { type = string }`)
	_ = tw.WriteHeader(&tar.Header{
		Name:     "main.tf",
		Mode:     0600,
		Size:     int64(len(content)),
		Typeflag: tar.TypeReg,
	})
	_, _ = tw.Write(content)

	contentSub := []byte(`variable "sub_var" { type = string }`)
	_ = tw.WriteHeader(&tar.Header{
		Name:     "modules/sub/main.tf",
		Mode:     0600,
		Size:     int64(len(contentSub)),
		Typeflag: tar.TypeReg,
	})
	_, _ = tw.Write(contentSub)
	_ = tw.Close()
	_ = gw.Close()
	tarData := buf.Bytes()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/versions") {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
				"modules": [{"versions": [{"version": "1.0.0"}, {"version": "0.9.0"}]}]
			}`))
			return
		}
		if strings.HasSuffix(r.URL.Path, "/download") {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(tarData)
			return
		}
		http.NotFound(w, r)
	}))

	origClient := newRegistryClient
	newRegistryClient = func() *registry.Client {
		c := registry.NewClient(server.Client())
		c.BaseURL = server.URL
		// These servers answer 500, which is retryable. Without an explicit budget the client
		// retries against the production 2 minute default, which made these tests take 303s and
		// 92s respectively and pushed the package close to Go's 600s timeout in CI.
		c.RetryMaxElapsedTime = 20 * time.Millisecond
		return c
	}

	cleanup := func() {
		server.Close()
		newRegistryClient = origClient
	}
	return server, cleanup
}

func TestExecute_RegistrySources_WrapperMode(t *testing.T) {
	_, cleanup := setupMockRegistryServer(t)
	defer cleanup()

	wrapperDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(wrapperDir, "main.tf"), []byte(`
module "upstream" {
  source = "hashicorp/consul/aws"
  cluster_name = "test"
}
`), 0600))

	var stdout, stderr bytes.Buffer

	// 1. With explicit new and old version
	code := Execute([]string{
		"--wrapper", wrapperDir,
		"--upstream", "hashicorp/consul/aws",
		"--old-version", "0.9.0",
		"--new-version", "1.0.0",
	}, &stdout, &stderr)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout.String(), "TERRAFORM WRAPPER COMPATIBILITY & PIN AUDIT")

	// 2. With omitted version (latest version deduced) and deduced upstream source
	stdout.Reset()
	stderr.Reset()
	code = Execute([]string{
		"--wrapper", wrapperDir,
	}, &stdout, &stderr)
	assert.Equal(t, 0, code)
}

func TestExecute_RegistrySources_CompareMode(t *testing.T) {
	_, cleanup := setupMockRegistryServer(t)
	defer cleanup()

	var stdout, stderr bytes.Buffer

	// 1. Explicit versions
	code := Execute([]string{
		"--old", "hashicorp/consul/aws",
		"--new", "hashicorp/consul/aws",
		"--old-version", "0.9.0",
		"--new-version", "1.0.0",
	}, &stdout, &stderr)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout.String(), "TERRAFORM AST COMPATIBILITY DIFF REPORT")

	// 2. Omitted versions
	stdout.Reset()
	stderr.Reset()
	code = Execute([]string{
		"--old", "hashicorp/consul/aws",
		"--new", "hashicorp/consul/aws",
	}, &stdout, &stderr)
	assert.Equal(t, 0, code)
}

func TestExecute_RegistryDownloadErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "server error", http.StatusInternalServerError)
	}))
	defer server.Close()

	origClient := newRegistryClient
	newRegistryClient = func() *registry.Client {
		c := registry.NewClient(server.Client())
		c.BaseURL = server.URL
		// These servers answer 500, which is retryable. Without an explicit budget the client
		// retries against the production 2 minute default, which made these tests take 303s and
		// 92s respectively and pushed the package close to Go's 600s timeout in CI.
		c.RetryMaxElapsedTime = 20 * time.Millisecond
		return c
	}
	defer func() { newRegistryClient = origClient }()

	wrapperDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(wrapperDir, "main.tf"), []byte(`
module "m" { source = "hashicorp/consul/aws" }
`), 0600))

	var stdout, stderr bytes.Buffer

	// Get latest version fails in wrapper mode. The lookup error is now reported in its own
	// right: it used to be discarded, so the CLI went on to download an unspecified version.
	code := Execute([]string{
		"--wrapper", wrapperDir,
		"--upstream", "hashicorp/consul/aws",
	}, &stdout, &stderr)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr.String(), "Error resolving the latest version of hashicorp/consul/aws")

	// Download fails with explicit version in wrapper mode
	stdout.Reset()
	stderr.Reset()
	code = Execute([]string{
		"--wrapper", wrapperDir,
		"--upstream", "hashicorp/consul/aws",
		"--new-version", "1.0.0",
	}, &stdout, &stderr)
	assert.Equal(t, 1, code)

	// Compare mode old download fails
	stdout.Reset()
	stderr.Reset()
	code = Execute([]string{
		"--old", "hashicorp/consul/aws",
		"--new", "hashicorp/consul/aws",
		"--old-version", "1.0.0",
	}, &stdout, &stderr)
	assert.Equal(t, 1, code)
}

func TestExecute_BlastRadius_Exit1(t *testing.T) {
	upstreamDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(upstreamDir, "main.tf"), []byte(`
variable "new_required" {
  type = string
}
`), 0600))

	wrapperDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(wrapperDir, "main.tf"), []byte(`
variable "ip_range_services" {
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
		"--exit-code",
	}, &stdout, &stderr)
	assert.Equal(t, 1, code)
}

func TestExecute_CompareMode_HazardsMarkdown(t *testing.T) {
	oldDir := t.TempDir()
	newDir := t.TempDir()

	// A provider-agnostic attribute, because comparing two local directories gives the analyzer no
	// way to tell which cloud the module manages. `ip_range_services` is a GKE-only secondary
	// CIDR attribute and is deliberately not asserted without a provider signal; supply
	// --provider-schema when that attribution is needed.
	require.NoError(t, os.WriteFile(filepath.Join(oldDir, "main.tf"), []byte(`
variable "location" {
  type    = string
  default = "us-central1"
}
`), 0600))

	require.NoError(t, os.WriteFile(filepath.Join(newDir, "main.tf"), []byte(`
variable "location" {
  type = list(string)
}
`), 0600))

	var stdout, stderr bytes.Buffer
	code := Execute([]string{
		"--old", oldDir,
		"--new", newDir,
		"--format", "markdown",
		"--check-recreation=true",
	}, &stdout, &stderr)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout.String(), "State Destruction & Recreation Hazards")
	assert.Contains(t, stdout.String(), "provider-agnostic resource",
		"an unattributed hazard must say so rather than name an invented resource type")
}

func TestExecute_UpgradePathAndPolicy(t *testing.T) {
	oldDir := t.TempDir()
	newDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(oldDir, "main.tf"), []byte(`
variable "name" {
  type = string
}
`), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(newDir, "main.tf"), []byte(`
variable "name" {
  type = number
}
`), 0600))

	policyFile := filepath.Join(t.TempDir(), "policy.json")
	require.NoError(t, os.WriteFile(policyFile, []byte(`{"max_bump":"MINOR"}`), 0600))

	var stdout, stderr bytes.Buffer
	code := Execute([]string{
		"--old", oldDir, "--new", newDir,
		"--old-version", "1.0.0", "--new-version", "2.0.0",
		"--upgrade-path", "--policy", policyFile, "--format", "json",
	}, &stdout, &stderr)
	assert.Equal(t, 1, code)
	assert.Contains(t, stdout.String(), `"target_version": "2.0.0"`)
	assert.Contains(t, stdout.String(), `"rule": "max_bump"`)
	assert.Empty(t, stderr.String())

	stdout.Reset()
	stderr.Reset()
	code = Execute([]string{"--old", oldDir, "--new", newDir, "--upgrade-path"}, &stdout, &stderr)
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr.String(), "requires --old-version and --new-version")
}

func TestExecute_RegistryMkdirTempFailures(t *testing.T) {
	origMkdir := mkdirTemp
	defer func() { mkdirTemp = origMkdir }()

	mkdirTemp = func(dir, pattern string) (string, error) {
		return "", errors.New("mkdir failure")
	}

	var stdout, stderr bytes.Buffer
	// Wrapper mode
	code := Execute([]string{
		"--wrapper", t.TempDir(),
		"--upstream", "hashicorp/consul/aws",
	}, &stdout, &stderr)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr.String(), "Error creating temp dir")

	// Compare mode old
	stdout.Reset()
	stderr.Reset()
	code = Execute([]string{
		"--old", "hashicorp/consul/aws",
		"--new", t.TempDir(),
	}, &stdout, &stderr)
	assert.Equal(t, 1, code)

	// Compare mode new
	stdout.Reset()
	stderr.Reset()
	code = Execute([]string{
		"--old", t.TempDir(),
		"--new", "hashicorp/consul/aws",
	}, &stdout, &stderr)
	assert.Equal(t, 1, code)
}

func TestExecute_CompareMode_NewDownloadFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "versions") {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"modules": [{"versions": [{"version": "1.0.0"}]}]}`))
			return
		}
		http.Error(w, "download failed", http.StatusInternalServerError)
	}))
	defer server.Close()

	origClient := newRegistryClient
	newRegistryClient = func() *registry.Client {
		c := registry.NewClient(server.Client())
		c.BaseURL = server.URL
		// These servers answer 500, which is retryable. Without an explicit budget the client
		// retries against the production 2 minute default, which made these tests take 303s and
		// 92s respectively and pushed the package close to Go's 600s timeout in CI.
		c.RetryMaxElapsedTime = 20 * time.Millisecond
		return c
	}
	defer func() { newRegistryClient = origClient }()

	var stdout, stderr bytes.Buffer
	code := Execute([]string{
		"--old", t.TempDir(),
		"--new", "hashicorp/consul/aws",
		"--new-version", "1.0.0",
	}, &stdout, &stderr)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr.String(), "Error checking out new version")
}

func TestDefaultRegistryClient(t *testing.T) {
	c := newRegistryClient()
	assert.NotNil(t, c)
}

func TestExecute_BlastRadiusMachineFormatsAreSingleDocuments(t *testing.T) {
	upstreamDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(upstreamDir, "main.tf"), []byte(`
variable "ip_range_services" {
  type = string
}
`), 0600))

	wrapperDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(wrapperDir, "main.tf"), []byte(`
variable "ip_range_services" {
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

	// JSON must stay a single parseable document that carries the blast radius inline.
	code := Execute([]string{
		"--wrapper", wrapperDir,
		"--upstream", upstreamDir,
		"--blast-radius", environmentsDir,
		"--format", "json",
	}, &stdout, &stderr)
	require.Equal(t, 0, code, stderr.String())

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &decoded), "output must be valid JSON: %s", stdout.String())
	assert.Contains(t, decoded, "verdict")
	blast, ok := decoded["blast_radius"].(map[string]any)
	require.True(t, ok, "blast radius must be embedded in the same document")
	assert.Equal(t, float64(1), blast["total_call_sites_scanned"])

	// SARIF must stay a single valid SARIF document including the consumer findings.
	stdout.Reset()
	stderr.Reset()
	code = Execute([]string{
		"--wrapper", wrapperDir,
		"--upstream", upstreamDir,
		"--blast-radius", environmentsDir,
		"--format", "sarif",
	}, &stdout, &stderr)
	require.Equal(t, 0, code, stderr.String())

	var log map[string]any
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &log), "output must be valid SARIF: %s", stdout.String())
	assert.Equal(t, "2.1.0", log["version"])

	runs := log["runs"].([]any)
	require.Len(t, runs, 1)
	run := runs[0].(map[string]any)
	ruleIDs := map[string]bool{}
	for _, rule := range run["tool"].(map[string]any)["driver"].(map[string]any)["rules"].([]any) {
		ruleIDs[rule.(map[string]any)["id"].(string)] = true
	}
	assert.True(t, ruleIDs["TF-BLAST-RADIUS-CONSUMER"], "consumer rule must be advertised: %v", ruleIDs)
	for _, result := range run["results"].([]any) {
		assert.True(t, ruleIDs[result.(map[string]any)["ruleId"].(string)], "every result needs a documented rule")
	}
}

func TestExecute_RecreationHazardsAppearInEveryFormat(t *testing.T) {
	oldDir := t.TempDir()
	newDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(oldDir, "main.tf"), []byte(`
variable "location" {
  type    = string
  default = "us-central1"
}
`), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(newDir, "main.tf"), []byte(`
variable "location" {
  type = list(string)
}
`), 0600))

	for _, format := range []string{"text", "markdown", "json", "sarif"} {
		t.Run(format, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := Execute([]string{
				"--old", oldDir,
				"--new", newDir,
				"--format", format,
				"--check-recreation=true",
			}, &stdout, &stderr)
			require.Equal(t, 0, code, stderr.String())
			assert.Contains(t, stdout.String(), "location")
			assert.NotContains(t, stdout.String(), "Recreation Hazards\n  \n")

			if format == "text" {
				assert.Contains(t, stdout.String(), "STATE DESTRUCTION & RECREATION HAZARDS")
			}
			if format == "json" {
				var decoded map[string]any
				require.NoError(t, json.Unmarshal(stdout.Bytes(), &decoded))
				assert.Contains(t, decoded, "recreation_hazards")
			}
			if format == "sarif" {
				assert.Contains(t, stdout.String(), "TF-RECREATION-HAZARD")
			}
		})
	}

	// Disabling the flag must remove the hazard findings entirely.
	var stdout, stderr bytes.Buffer
	code := Execute([]string{"--old", oldDir, "--new", newDir, "--format", "json", "--check-recreation=false"}, &stdout, &stderr)
	require.Equal(t, 0, code, stderr.String())
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &decoded))
	assert.NotContains(t, decoded, "recreation_hazards")
}

func TestExecute_ReportsModuleSource(t *testing.T) {
	oldDir := t.TempDir()
	newDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(oldDir, "main.tf"), []byte(`variable "name" { type = string }`), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(newDir, "main.tf"), []byte(`variable "name" { type = number }`), 0600))

	var stdout, stderr bytes.Buffer
	code := Execute([]string{"--old", oldDir, "--new", newDir, "--format", "json"}, &stdout, &stderr)
	require.Equal(t, 0, code, stderr.String())

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &decoded))
	assert.Equal(t, newDir, decoded["module_name"], "the compared module source must be reported")

	stdout.Reset()
	stderr.Reset()
	code = Execute([]string{"--old", oldDir, "--new", newDir, "--format", "text"}, &stdout, &stderr)
	require.Equal(t, 0, code, stderr.String())
	assert.Contains(t, stdout.String(), "Module:      "+newDir)
}

func TestExecute_UpgradePathReportsModuleSource(t *testing.T) {
	oldDir := t.TempDir()
	newDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(oldDir, "main.tf"), []byte(`variable "name" { type = string }`), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(newDir, "main.tf"), []byte(`variable "name" { type = number }`), 0600))

	var stdout, stderr bytes.Buffer
	code := Execute([]string{
		"--old", oldDir, "--new", newDir,
		"--old-version", "1.0.0", "--new-version", "2.0.0",
		"--upgrade-path", "--format", "json",
	}, &stdout, &stderr)
	require.Equal(t, 0, code, stderr.String())

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &decoded))
	steps := decoded["steps"].([]any)
	require.Len(t, steps, 1)
	stepDiff := steps[0].(map[string]any)["diff"].(map[string]any)
	assert.Equal(t, newDir, stepDiff["module_name"])
}

func TestExecute_GitFileURLSources(t *testing.T) {
	repoDir := createTestTFRepo(t, "v1.0.0", `variable "cluster_name" { type = string }`)
	remote := "file://" + repoDir

	var stdout, stderr bytes.Buffer

	// A file:// URL is a Git URL and must be cloned rather than treated as a registry address.
	code := Execute([]string{"--old", remote, "--new", remote, "--old-version", "v1.0.0"}, &stdout, &stderr)
	assert.Equal(t, 0, code, stderr.String())
	assert.Contains(t, stdout.String(), "TERRAFORM AST COMPATIBILITY DIFF REPORT")

	// Wrapper mode against the same remote.
	wrapperDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(wrapperDir, "main.tf"), []byte(`module "gke" {
  cluster_name = "test"
}`), 0600))
	stdout.Reset()
	stderr.Reset()
	code = Execute([]string{"--wrapper", wrapperDir, "--upstream", remote, "--new-version", "v1.0.0"}, &stdout, &stderr)
	assert.Equal(t, 0, code, stderr.String())
	assert.Contains(t, stdout.String(), "SAFE_TO_PIN")

	// The cached clones must not outlive the run.
	assert.Empty(t, gitops.CachedRepositoryDirs(), "cached repositories must be released when Execute returns")
}

// TestExecute_GitSubdirAndPinnedRef runs the documented Terraform shape end to end: a module in a
// repository subdirectory, pinned by the source itself, analysed with no version flags at all.
// Inspecting the repository root instead of the subdirectory, or the head instead of the pin,
// would both change the verdict, so both are asserted against.
func TestExecute_GitSubdirAndPinnedRef(t *testing.T) {
	repoDir := t.TempDir()
	repo, err := git.PlainInit(repoDir, false)
	require.NoError(t, err)
	w, err := repo.Worktree()
	require.NoError(t, err)
	sig := &object.Signature{Name: "Test", Email: "test@example.com", When: time.Now()}
	gkeDir := filepath.Join(repoDir, "modules", "gke")
	require.NoError(t, os.MkdirAll(gkeDir, 0750))

	commitRelease := func(tag, gkeContent string) {
		require.NoError(t, os.WriteFile(filepath.Join(repoDir, "main.tf"), []byte(`variable "from_root" {}`), 0644))
		require.NoError(t, os.WriteFile(filepath.Join(gkeDir, "main.tf"), []byte(gkeContent), 0644))
		_, err := w.Add("main.tf")
		require.NoError(t, err)
		_, err = w.Add("modules/gke/main.tf")
		require.NoError(t, err)
		commit, err := w.Commit("release "+tag, &git.CommitOptions{Author: sig, Committer: sig})
		require.NoError(t, err)
		_, err = repo.CreateTag(tag, commit, nil)
		require.NoError(t, err)
	}
	// The wrapper only passes cluster_name, so the verdict differs between v1.0.0 and v2.0.0.
	commitRelease("v1.0.0", `variable "cluster_name" { type = string }`)
	commitRelease("v2.0.0", `variable "other_name" { type = string }`)

	wrapperDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(wrapperDir, "main.tf"), []byte(`module "gke" {
  cluster_name = "test"
}`), 0600))

	source := "git::file://" + repoDir + "//modules/gke?ref=v1.0.0"
	var stdout, stderr bytes.Buffer

	// No version flags: the source's ?ref= pin selects v1.0.0 and //modules/gke the module.
	code := Execute([]string{"--wrapper", wrapperDir, "--upstream", source}, &stdout, &stderr)
	assert.Equal(t, 0, code, stderr.String())
	assert.Contains(t, stdout.String(), "SAFE_TO_PIN")
	assert.Empty(t, gitops.CachedRepositoryDirs(), "cached repositories must be released when Execute returns")

	// Control: pinning the incompatible head explicitly must not yield the same verdict, which is
	// what makes the assertion above discriminate between the pinned revision and the head.
	stdout.Reset()
	stderr.Reset()
	code = Execute([]string{
		"--wrapper", wrapperDir, "--upstream", source, "--new-version", "v2.0.0",
	}, &stdout, &stderr)
	assert.Equal(t, 0, code, stderr.String())
	assert.NotContains(t, stdout.String(), "SAFE_TO_PIN")

	// The same source in direct comparison mode, still with the revision pinned by the source.
	stdout.Reset()
	stderr.Reset()
	code = Execute([]string{"--old", source, "--new", source}, &stdout, &stderr)
	assert.Equal(t, 0, code, stderr.String())
	assert.Contains(t, stdout.String(), "TERRAFORM AST COMPATIBILITY DIFF REPORT")

	// A subdirectory that is not in the repository is an error, never a silent root inspection.
	stdout.Reset()
	stderr.Reset()
	code = Execute([]string{"--wrapper", wrapperDir, "--upstream", "git::file://" + repoDir + "//missing"}, &stdout, &stderr)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr.String(), "subdirectory")
	assert.Empty(t, gitops.CachedRepositoryDirs(), "cached repositories must be released when Execute returns")
}

func TestExecute_ModeValidationErrors(t *testing.T) {
	var stdout, stderr bytes.Buffer

	// --upgrade-path is a direct-comparison feature.
	code := Execute([]string{"--wrapper", t.TempDir(), "--upstream", t.TempDir(), "--upgrade-path"}, &stdout, &stderr)
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr.String(), "--upgrade-path is only available in direct comparison mode")

	// --fix-preview is a wrapper-audit feature.
	stdout.Reset()
	stderr.Reset()
	code = Execute([]string{"--old", t.TempDir(), "--new", t.TempDir(), "--fix-preview"}, &stdout, &stderr)
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr.String(), "--fix-preview is only available in wrapper audit mode")

	// --upgrade-path needs a strictly ascending version window.
	stdout.Reset()
	stderr.Reset()
	code = Execute([]string{
		"--old", t.TempDir(), "--new", t.TempDir(),
		"--old-version", "2.0.0", "--new-version", "1.0.0",
		"--upgrade-path",
	}, &stdout, &stderr)
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr.String(), "requires --new-version to be greater than --old-version")

	// --upgrade-path has no SARIF renderer.
	stdout.Reset()
	stderr.Reset()
	code = Execute([]string{
		"--old", t.TempDir(), "--new", t.TempDir(),
		"--old-version", "1.0.0", "--new-version", "2.0.0",
		"--upgrade-path", "--format", "sarif",
	}, &stdout, &stderr)
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr.String(), "--upgrade-path supports text, markdown, and json formats")
}

func TestExecute_WrapperPolicyCombinesUpstreamDiff(t *testing.T) {
	// One repository carrying both releases, so --old-version resolves to a real module.
	// Previously this built two repositories and asked the v2.0.0 repo for a v1.0.0 tag; the
	// resulting "reference not found" was swallowed, the pinned diff was vacuously identical to
	// the target, and the assertion below passed without ever exercising a real transition.
	target := createTestTFRepoWithTags(t, []struct{ tag, content string }{
		{"v1.0.0", `
variable "cluster_name" { type = string }
`},
		{"v2.0.0", `
variable "cluster_name" { type = list(string) }
variable "region"         { type = string }
`},
	})

	wrapperDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(wrapperDir, "main.tf"), []byte(`
module "gke" {
  source = "`+target+`"
}
`), 0600))

	policyFile := filepath.Join(t.TempDir(), "policy.json")
	require.NoError(t, os.WriteFile(policyFile, []byte(`{"block_breaking":true}`), 0600))

	var stdout, stderr bytes.Buffer
	code := Execute([]string{
		"--wrapper", wrapperDir,
		"--upstream", target,
		"--old-version", "v1.0.0",
		"--new-version", "v2.0.0",
		"--policy", policyFile,
		"--format", "json",
	}, &stdout, &stderr)
	assert.Equal(t, 1, code, stderr.String())

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &decoded))
	_, ok := decoded["upstream_diff"].(map[string]any)
	require.True(t, ok, "the pinned upstream version must produce an upstream diff")
	require.Contains(t, decoded, "policy_violations")
	assert.NotEmpty(t, decoded["policy_violations"])

	// Without a pinned version there is no upstream diff to fold into the policy input.
	require.NoError(t, os.WriteFile(policyFile, []byte(`{"block_warnings":true}`), 0600))
	stdout.Reset()
	stderr.Reset()
	code = Execute([]string{
		"--wrapper", wrapperDir,
		"--upstream", target,
		"--policy", policyFile,
		"--format", "json",
	}, &stdout, &stderr)
	assert.Equal(t, 0, code, stderr.String())
	var noDiff map[string]any
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &noDiff))
	assert.NotContains(t, noDiff, "upstream_diff")
	assert.NotContains(t, noDiff, "policy_violations")
}

// TestExecute_PinnedVersionMustResolve covers the pinned lookup failing loudly. It previously
// degraded silently, which left the upstream-to-upstream diff unset and so disabled downgrade
// detection and the safe-pin recommendation while still reporting the target as safe to pin.
func TestExecute_PinnedVersionMustResolve(t *testing.T) {
	target := createTestTFRepo(t, "v2.0.0", `variable "name" { type = string }`)

	wrapperDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(wrapperDir, "main.tf"),
		[]byte(`module "gke" { source = "`+target+`" }`), 0600))

	var stdout, stderr bytes.Buffer
	code := Execute([]string{
		"--wrapper", wrapperDir,
		"--upstream", target,
		"--old-version", "v9.9.9-does-not-exist",
		"--new-version", "v2.0.0",
	}, &stdout, &stderr)

	assert.Equal(t, 1, code)
	assert.Contains(t, stderr.String(), "Error checking out pinned upstream version v9.9.9-does-not-exist")
	assert.Empty(t, stdout.String(), "no report may be emitted when an analysis was skipped")
}

func TestExecute_FixPreviewOutcomes(t *testing.T) {
	upstreamDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(upstreamDir, "main.tf"), []byte(`variable "kept" { type = string }`), 0600))

	wrapperDir := t.TempDir()
	wrapperFile := filepath.Join(wrapperDir, "main.tf")
	require.NoError(t, os.WriteFile(wrapperFile, []byte(`module "upstream" {
  source = "`+upstreamDir+`"
  obsolete = "x"
}
`), 0600))

	var stdout, stderr bytes.Buffer

	// Patch generation failure.
	code := Execute([]string{
		"--wrapper", wrapperDir, "--upstream", upstreamDir, "--fix-preview",
		"--output", "/invalid/directory/that/does/not/exist/out.diff",
	}, &stdout, &stderr)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr.String(), "Error writing fix preview")

	// A wrapper with a module call but nothing patchable yields an explanatory placeholder.
	noRemoved := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(noRemoved, "main.tf"), []byte(`module "upstream" {
  source = "`+upstreamDir+`"
}
`), 0600))
	stdout.Reset()
	stderr.Reset()
	code = Execute([]string{
		"--wrapper", noRemoved, "--upstream", upstreamDir, "--fix-preview",
	}, &stdout, &stderr)
	assert.Equal(t, 0, code, stderr.String())
	assert.Contains(t, stdout.String(), "No automatic patch can be generated")

	// The exit-code gate still applies to a preview.
	stdout.Reset()
	stderr.Reset()
	code = Execute([]string{
		"--wrapper", wrapperDir, "--upstream", upstreamDir, "--fix-preview", "--exit-code",
	}, &stdout, &stderr)
	assert.Equal(t, 1, code)
	assert.Contains(t, stdout.String(), "--- a/main.tf")

	// Policy violations fail a preview run even without --exit-code.
	policyFile := filepath.Join(t.TempDir(), "policy.json")
	require.NoError(t, os.WriteFile(policyFile, []byte(`{"block_breaking":true}`), 0600))
	stdout.Reset()
	stderr.Reset()
	code = Execute([]string{
		"--wrapper", wrapperDir, "--upstream", upstreamDir, "--fix-preview", "--policy", policyFile,
	}, &stdout, &stderr)
	assert.Equal(t, 1, code)
	assert.Contains(t, stdout.String(), "--- a/main.tf")
}

func TestExecute_UpgradePathFormatsAndFailures(t *testing.T) {
	oldDir := t.TempDir()
	newDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(oldDir, "main.tf"), []byte(`variable "name" { type = string }`), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(newDir, "main.tf"), []byte(`variable "name" { type = number }`), 0600))

	var stdout, stderr bytes.Buffer

	for _, format := range []string{"markdown", "text"} {
		stdout.Reset()
		stderr.Reset()
		code := Execute([]string{
			"--old", oldDir, "--new", newDir,
			"--old-version", "v1.0.0", "--new-version", "v2.0.0",
			"--upgrade-path", "--format", format,
		}, &stdout, &stderr)
		assert.Equal(t, 0, code, stderr.String())
		assert.Contains(t, stdout.String(), "v1.0.0")
	}

	// Writing the report to an unwritable path fails.
	stdout.Reset()
	stderr.Reset()
	code := Execute([]string{
		"--old", oldDir, "--new", newDir,
		"--old-version", "v1.0.0", "--new-version", "v2.0.0",
		"--upgrade-path",
		"--output", "/invalid/directory/that/does/not/exist/out.md",
	}, &stdout, &stderr)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr.String(), "Error writing output")

	// Different sources for --old and --new cannot be resolved into a single upgrade path.
	first := createTaggedTFRepo(t, map[string]string{"v1.0.0": `variable "name" { type = string }`})
	second := createTaggedTFRepo(t, map[string]string{"v1.0.0": `variable "name" { type = string }`, "v2.0.0": `variable "name" { type = number }`})
	stdout.Reset()
	stderr.Reset()
	code = Execute([]string{
		"--old", first, "--new", second,
		"--old-version", "v1.0.0", "--new-version", "v2.0.0",
		"--upgrade-path",
	}, &stdout, &stderr)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr.String(), "Error analyzing upgrade path")
	assert.Contains(t, stderr.String(), "same source")

	// A registry that cannot list versions fails while planning the upgrade path.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/versions"):
			http.Error(w, "no versions", http.StatusInternalServerError)
		case strings.HasSuffix(r.URL.Path, "/download"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(createTestModuleArchive(t))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	origClient := newRegistryClient
	newRegistryClient = func() *registry.Client {
		client := registry.NewClient(server.Client())
		client.BaseURL = server.URL
		client.RetryMaxElapsedTime = 20 * time.Millisecond
		return client
	}
	defer func() { newRegistryClient = origClient }()

	stdout.Reset()
	stderr.Reset()
	code = Execute([]string{
		"--old", "hashicorp/consul/aws", "--new", "hashicorp/consul/aws",
		"--old-version", "1.0.0", "--new-version", "2.0.0",
		"--upgrade-path",
	}, &stdout, &stderr)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr.String(), "Error analyzing upgrade path")
}

func TestExecute_UpgradePathInspectsIntermediateVersions(t *testing.T) {
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	require.NoError(t, err)
	worktree, err := repo.Worktree()
	require.NoError(t, err)
	signature := &object.Signature{Name: "Test", Email: "test@example.com", When: time.Now()}

	for _, version := range []string{"v1.0.0", "v1.1.0", "v2.0.0"} {
		content := `variable "name" { type = string }`
		if version == "v2.0.0" {
			content = `variable "name" { type = number }`
		} else {
			content += "\n# " + version
		}
		require.NoError(t, os.WriteFile(filepath.Join(dir, "main.tf"), []byte(content), 0600))
		_, err = worktree.Add("main.tf")
		require.NoError(t, err)
		commit, err := worktree.Commit(version, &git.CommitOptions{Author: signature, Committer: signature})
		require.NoError(t, err)
		_, err = repo.CreateTag(version, commit, nil)
		require.NoError(t, err)
	}

	var stdout, stderr bytes.Buffer
	code := Execute([]string{
		"--old", dir, "--new", dir,
		"--old-version", "v1.0.0", "--new-version", "v2.0.0",
		"--upgrade-path", "--format", "json",
	}, &stdout, &stderr)
	require.Equal(t, 0, code, stderr.String())

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &decoded))
	steps := decoded["steps"].([]any)
	assert.Len(t, steps, 2, "the intermediate v1.1.0 release must be inspected and diffed")
}

func TestExecute_UpgradePathFailsOnUnreadableIntermediateVersion(t *testing.T) {
	// The intermediate release carries an unparsable Terraform file, so inspecting it fails.
	dir := createTaggedTFRepo(t, map[string]string{
		"v1.0.0": `variable "name" { type = string }`,
		"v1.1.0": "this is not { valid hcl",
		"v2.0.0": `variable "name" { type = number }`,
	})

	var stdout, stderr bytes.Buffer
	code := Execute([]string{
		"--old", dir, "--new", dir,
		"--old-version", "v1.0.0", "--new-version", "v2.0.0",
		"--upgrade-path",
	}, &stdout, &stderr)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr.String(), "Error analyzing upgrade path")
	assert.Contains(t, stderr.String(), "inspect intermediate version v1.1.0")
}

func TestExecute_UpgradePathRegistryIntermediateVersions(t *testing.T) {
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	content := []byte(`variable "cluster_name" { type = string }`)
	_ = tw.WriteHeader(&tar.Header{
		Name:     "main.tf",
		Mode:     0600,
		Size:     int64(len(content)),
		Typeflag: tar.TypeReg,
	})
	_, _ = tw.Write(content)
	_ = tw.Close()
	_ = gw.Close()
	tarData := buf.Bytes()

	var failDownload bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/versions") {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{
				"modules": [{"versions": [{"version": "1.0.0"}, {"version": "1.1.0"}, {"version": "2.0.0"}]}]
			}`))
			return
		}
		if strings.HasSuffix(r.URL.Path, "/download") {
			if failDownload && strings.Contains(r.URL.Path, "1.1.0") {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(tarData)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	origClient := newRegistryClient
	newRegistryClient = func() *registry.Client {
		c := registry.NewClient(server.Client())
		c.BaseURL = server.URL
		c.RetryMaxElapsedTime = 20 * time.Millisecond
		return c
	}
	defer func() { newRegistryClient = origClient }()

	// 1. Success case: intermediate version 1.1.0 is downloaded and cleaned up.
	var stdout, stderr bytes.Buffer
	code := Execute([]string{
		"--old", "hashicorp/consul/aws", "--new", "hashicorp/consul/aws",
		"--old-version", "1.0.0", "--new-version", "2.0.0",
		"--upgrade-path", "--format", "json",
	}, &stdout, &stderr)
	assert.Equal(t, 0, code, stderr.String())

	// 2. Download failure on intermediate version.
	failDownload = true
	stdout.Reset()
	stderr.Reset()
	code = Execute([]string{
		"--old", "hashicorp/consul/aws", "--new", "hashicorp/consul/aws",
		"--old-version", "1.0.0", "--new-version", "2.0.0",
		"--upgrade-path",
	}, &stdout, &stderr)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr.String(), "retrieve intermediate version 1.1.0")
	failDownload = false

	// 3. Fallthrough error in inspectModulesParallel (error does not contain dir).
	origInspect := inspectModulesParallel
	inspectModulesParallel = func(ctx context.Context, dirs []string, maxConcurrent int) ([]*ast.ModuleInfo, error) {
		return nil, errors.New("simulated unmapped parallel error")
	}
	defer func() { inspectModulesParallel = origInspect }()

	stdout.Reset()
	stderr.Reset()
	code = Execute([]string{
		"--old", "hashicorp/consul/aws", "--new", "hashicorp/consul/aws",
		"--old-version", "1.0.0", "--new-version", "2.0.0",
		"--upgrade-path",
	}, &stdout, &stderr)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr.String(), "inspect intermediate versions: simulated unmapped parallel error")
}

func TestInspectModuleVersion(t *testing.T) {
	repoDir := createTestTFRepo(t, "v1.0.0", `variable "name" { type = string }`)

	t.Cleanup(gitops.GlobalCache.Clear)

	// Registry sources download into a temporary directory.
	_, cleanup := setupMockRegistryServer(t)
	defer cleanup()
	info, err := inspectModuleVersion(context.Background(), 2*time.Minute, "hashicorp/consul/aws", "1.0.0")
	require.NoError(t, err)
	require.Contains(t, info.Variables, "cluster_name")

	// A failing temporary directory surfaces as an error.
	origMkdir := mkdirTemp
	defer func() { mkdirTemp = origMkdir }()
	mkdirTemp = func(dir, pattern string) (string, error) {
		return "", assert.AnError
	}
	_, err = inspectModuleVersion(context.Background(), 2*time.Minute, "hashicorp/consul/aws", "1.0.0")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "create temporary module directory")
	mkdirTemp = origMkdir

	// Git URLs and repositories are extracted through the shared cache.
	info, err = inspectModuleVersion(context.Background(), 2*time.Minute, repoDir, "v1.0.0")
	require.NoError(t, err)
	require.Contains(t, info.Variables, "name")

	info, err = inspectModuleVersion(context.Background(), 2*time.Minute, "file://"+repoDir, "v1.0.0")
	require.NoError(t, err)
	require.Contains(t, info.Variables, "name")

	// An unknown ref fails.
	_, err = inspectModuleVersion(context.Background(), 2*time.Minute, repoDir, "v9.9.9")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "checkout repository version")

	// Non-versioned local directories cannot provide intermediate releases.
	_, err = inspectModuleVersion(context.Background(), 2*time.Minute, t.TempDir(), "v1.0.0")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "non-versioned local directory")
}

func TestComparisonSource(t *testing.T) {
	assert.Equal(t, "registry/mod/aws", comparisonSource("registry/mod/aws", "old/mod/aws"))
	assert.Equal(t, "old/mod/aws", comparisonSource("   ", "  old/mod/aws  "))
	assert.Equal(t, "", comparisonSource("", ""))
}

// createTaggedTFRepo builds a git repository with one tagged commit per supplied version.
func createTaggedTFRepo(t *testing.T, versions map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	require.NoError(t, err)
	worktree, err := repo.Worktree()
	require.NoError(t, err)
	signature := &object.Signature{Name: "Test", Email: "test@example.com", When: time.Now()}

	for _, version := range sortedKeys(versions) {
		require.NoError(t, os.WriteFile(filepath.Join(dir, "main.tf"), []byte(versions[version]), 0600))
		_, err = worktree.Add("main.tf")
		require.NoError(t, err)
		commit, err := worktree.Commit(version, &git.CommitOptions{Author: signature, Committer: signature})
		require.NoError(t, err)
		_, err = repo.CreateTag(version, commit, nil)
		require.NoError(t, err)
	}
	return dir
}

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// createTestModuleArchive returns a tar.gz archive containing a minimal Terraform module.
func createTestModuleArchive(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	content := []byte(`variable "cluster_name" { type = string }`)
	require.NoError(t, tw.WriteHeader(&tar.Header{
		Name:     "main.tf",
		Mode:     0600,
		Size:     int64(len(content)),
		Typeflag: tar.TypeReg,
	}))
	_, err := tw.Write(content)
	require.NoError(t, err)
	require.NoError(t, tw.Close())
	require.NoError(t, gw.Close())
	return buf.Bytes()
}

func TestExecute_FixPreviewWithoutModuleCallFails(t *testing.T) {
	// A wrapper with no module call has no target module name, so no patch can be generated.
	upstreamDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(upstreamDir, "main.tf"), []byte(`variable "kept" { type = string }`), 0600))

	wrapperDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(wrapperDir, "main.tf"), []byte(`variable "orphan" { type = string }`), 0600))

	var stdout, stderr bytes.Buffer
	code := Execute([]string{"--wrapper", wrapperDir, "--upstream", upstreamDir, "--fix-preview"}, &stdout, &stderr)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr.String(), "Error generating fix preview")
	assert.Contains(t, stderr.String(), "module name is required")
}

func TestInspectModuleVersion_RegistryDownloadFailure(t *testing.T) {
	t.Cleanup(gitops.GlobalCache.Clear)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "download failed", http.StatusInternalServerError)
	}))
	defer server.Close()

	origClient := newRegistryClient
	newRegistryClient = func() *registry.Client {
		client := registry.NewClient(server.Client())
		client.BaseURL = server.URL
		client.RetryMaxElapsedTime = 20 * time.Millisecond
		return client
	}
	defer func() { newRegistryClient = origClient }()

	_, err := inspectModuleVersion(context.Background(), 2*time.Minute, "hashicorp/consul/aws", "1.0.0")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "download registry module hashicorp/consul/aws@1.0.0")
}

func TestExecute_FixMode(t *testing.T) {
	var stdout, stderr bytes.Buffer

	// Both --fix and --fix-preview
	code := Execute([]string{"--wrapper", t.TempDir(), "--fix", "--fix-preview"}, &stdout, &stderr)
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr.String(), "Cannot specify both --fix and --fix-preview")

	// --fix in compare mode
	stdout.Reset()
	stderr.Reset()
	code = Execute([]string{"--old", t.TempDir(), "--new", t.TempDir(), "--fix"}, &stdout, &stderr)
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr.String(), "--fix is only available in wrapper audit mode")

	// Upstream module with removed argument
	upstreamDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(upstreamDir, "main.tf"), []byte(`variable "kept" { type = string }`), 0600))

	// Wrapper without module call fails
	noCallWrapper := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(noCallWrapper, "main.tf"), []byte(`variable "v" { type = string }`), 0600))
	stdout.Reset()
	stderr.Reset()
	code = Execute([]string{"--wrapper", noCallWrapper, "--upstream", upstreamDir, "--fix"}, &stdout, &stderr)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr.String(), "Error applying fixes")

	// Wrapper with module call and removable argument
	wrapperDir := t.TempDir()
	wrapperFile := filepath.Join(wrapperDir, "main.tf")
	require.NoError(t, os.WriteFile(wrapperFile, []byte(`module "child" {
  source = "`+upstreamDir+`"
  kept   = "val"
  old    = "val"
}
`), 0600))

	// Write output error
	stdout.Reset()
	stderr.Reset()
	code = Execute([]string{
		"--wrapper", wrapperDir, "--upstream", upstreamDir, "--fix",
		"--output", "/invalid/directory/not/exists/out.txt",
	}, &stdout, &stderr)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr.String(), "Error writing fix output")

	// Reset wrapper file content with removable argument
	require.NoError(t, os.WriteFile(wrapperFile, []byte(`module "child" {
  source = "`+upstreamDir+`"
  kept   = "val"
  old    = "val"
}
`), 0600))

	// Apply fix successfully
	stdout.Reset()
	stderr.Reset()
	code = Execute([]string{"--wrapper", wrapperDir, "--upstream", upstreamDir, "--fix"}, &stdout, &stderr)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout.String(), "Applied fixes to 1 file(s):")
	afterContent, err := os.ReadFile(wrapperFile)
	require.NoError(t, err)
	assert.NotContains(t, string(afterContent), `old    = "val"`)
	assert.Contains(t, string(afterContent), `kept`)

	// Re-running when nothing left to fix yields explanatory message
	stdout.Reset()
	stderr.Reset()
	code = Execute([]string{"--wrapper", wrapperDir, "--upstream", upstreamDir, "--fix"}, &stdout, &stderr)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout.String(), "No fixes were applied; no patchable arguments found.")

	// Exit code enforcement with --fix
	brokenUpstream := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(brokenUpstream, "main.tf"), []byte(`variable "new_required" { type = string }`), 0600))
	stdout.Reset()
	stderr.Reset()
	code = Execute([]string{"--wrapper", wrapperDir, "--upstream", brokenUpstream, "--fix", "--exit-code"}, &stdout, &stderr)
	assert.Equal(t, 1, code)

	// Policy enforcement with --fix
	policyFile := filepath.Join(t.TempDir(), "policy.json")
	require.NoError(t, os.WriteFile(policyFile, []byte(`{"block_breaking":true}`), 0600))
	stdout.Reset()
	stderr.Reset()
	code = Execute([]string{"--wrapper", wrapperDir, "--upstream", brokenUpstream, "--fix", "--policy", policyFile}, &stdout, &stderr)
	assert.Equal(t, 1, code)
}

func TestExecute_ProviderSchema(t *testing.T) {
	var stdout, stderr bytes.Buffer

	// Invalid schema path
	code := Execute([]string{"--wrapper", t.TempDir(), "--provider-schema", "/non/existent/schema.json"}, &stdout, &stderr)
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr.String(), "Error loading provider schema")

	// Valid schema path in compare mode
	schemaJSON := `{
  "format_version": "1.0",
  "provider_schemas": {
    "google": {
      "resource_schemas": {
        "google_compute_instance": {
          "block": {
            "attributes": {
              "zone": {
                "type": "string",
                "requires_replace": true
              }
            }
          }
        }
      }
    }
  }
}`
	schemaFile := filepath.Join(t.TempDir(), "schema.json")
	require.NoError(t, os.WriteFile(schemaFile, []byte(schemaJSON), 0600))

	oldDir := t.TempDir()
	newDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(oldDir, "main.tf"), []byte(`variable "zone" { type = string }`), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(newDir, "main.tf"), []byte(`variable "zone" { type = number }`), 0600))

	stdout.Reset()
	stderr.Reset()
	code = Execute([]string{
		"--old", oldDir, "--new", newDir,
		"--provider-schema", schemaFile,
	}, &stdout, &stderr)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout.String(), "STATE DESTRUCTION & RECREATION HAZARDS")
	assert.Contains(t, stdout.String(), "google_compute_instance")
}

func TestExecute_CacheDir(t *testing.T) {
	cacheDir := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := Execute([]string{
		"--old", t.TempDir(), "--new", t.TempDir(),
		"--cache-dir", cacheDir,
	}, &stdout, &stderr)
	assert.Equal(t, 0, code)
	assert.Equal(t, cacheDir, ast.GetPersistentCacheDir())
	ast.SetPersistentCacheDir("")
}

func TestExecute_LSPDaemon(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Execute([]string{"lsp"}, &stdout, &stderr)
	assert.Equal(t, 0, code)

	stdout.Reset()
	stderr.Reset()
	codeFlag := Execute([]string{"--lsp"}, &stdout, &stderr)
	assert.Equal(t, 0, codeFlag)
}

// TestExecute_InvalidFormatIsRejected covers roadmap #42. An unrecognised --format previously
// fell through the renderer's default branch and silently produced text, so a caller asking for
// a machine-readable document received prose.
func TestExecute_InvalidFormatIsRejected(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.tf"), []byte(`variable "a" { type = string }`), 0600))

	for _, format := range []string{"yaml", "json5", "TEXT5", "sarif2"} {
		var stdout, stderr bytes.Buffer
		code := Execute([]string{"--old", dir, "--new", dir, "--format", format}, &stdout, &stderr)
		assert.Equal(t, 2, code, "--format %s must be rejected", format)
		assert.Contains(t, stderr.String(), "invalid --format")
		assert.Contains(t, stderr.String(), "text, markdown, json, sarif")
		assert.Empty(t, stdout.String())
	}

	// Case and surrounding whitespace are still accepted, as before.
	var stdout, stderr bytes.Buffer
	code := Execute([]string{"--old", dir, "--new", dir, "--format", " JSON "}, &stdout, &stderr)
	assert.Equal(t, 0, code, stderr.String())
	assert.True(t, strings.HasPrefix(strings.TrimSpace(stdout.String()), "{"), "expected a JSON document")
}

// TestExecute_BlastRadiusFlagGuards covers roadmap #2 and #1. --blast-radius used to be
// accepted and silently discarded in comparison mode, and an empty or flag-shaped value was
// treated as a directory path.
func TestExecute_BlastRadiusFlagGuards(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.tf"), []byte(`variable "a" { type = string }`), 0600))

	// Rejected in comparison mode rather than silently ignored.
	var stdout, stderr bytes.Buffer
	code := Execute([]string{"--old", dir, "--new", dir, "--blast-radius", dir}, &stdout, &stderr)
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr.String(), "--blast-radius is only available in wrapper audit mode")

	// An empty value, as action.yml passes when the input is unset, means "not requested".
	stdout.Reset()
	stderr.Reset()
	code = Execute([]string{"--old", dir, "--new", dir, "--blast-radius", ""}, &stdout, &stderr)
	assert.Equal(t, 0, code, stderr.String())
	assert.Contains(t, stdout.String(), "TERRAFORM AST COMPATIBILITY DIFF REPORT")

	// A flag-shaped value is a caller error, not a directory named "--exit-code=true".
	stdout.Reset()
	stderr.Reset()
	code = Execute([]string{"--old", dir, "--new", dir, "--blast-radius", "--exit-code=true"}, &stdout, &stderr)
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr.String(), "--blast-radius requires a directory path")

	// --golden-plan is also rejected in comparison mode.
	stdout.Reset()
	stderr.Reset()
	code = Execute([]string{"--old", dir, "--new", dir, "--golden-plan", "plan.json"}, &stdout, &stderr)
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr.String(), "--golden-plan is only available in wrapper audit mode")
}

// TestExecute_FixModeRejectsDroppedAnalysisFlags covers roadmap #56. The codemod paths return
// before the findings block, so every analysis flag paired with --fix was silently discarded.
func TestExecute_FixModeRejectsDroppedAnalysisFlags(t *testing.T) {
	upstreamDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(upstreamDir, "main.tf"), []byte(`variable "kept" { type = string }`), 0600))
	wrapperDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(wrapperDir, "main.tf"), []byte(
		"module \"m\" {\n  source = \""+upstreamDir+"\"\n  obsolete = \"x\"\n}\n"), 0600))

	schemaFile := filepath.Join(t.TempDir(), "schema.json")
	require.NoError(t, os.WriteFile(schemaFile, []byte(`{"format_version":"1.0"}`), 0600))

	for _, flag := range []string{"--fix", "--fix-preview"} {
		for _, tc := range []struct {
			name string
			args []string
			want string
		}{
			{"blast-radius", []string{"--blast-radius", wrapperDir}, "--blast-radius is not supported"},
			{"golden-plan", []string{"--golden-plan", "plan.json"}, "--golden-plan is not supported"},
			{"provider-schema", []string{"--provider-schema", schemaFile}, "--provider-schema is not supported"},
			{"check-recreation", []string{"--check-recreation=false"}, "--check-recreation is not supported"},
			{"format", []string{"--format", "json"}, "--format json is not supported"},
		} {
			var stdout, stderr bytes.Buffer
			args := append([]string{"--wrapper", wrapperDir, "--upstream", upstreamDir, flag}, tc.args...)
			code := Execute(args, &stdout, &stderr)
			assert.Equal(t, 2, code, "%s with %s must be rejected", flag, tc.name)
			assert.Contains(t, stderr.String(), tc.want)
			assert.Empty(t, stdout.String())
		}
	}

	// The default configuration is untouched: no analysis flag, text format.
	var stdout, stderr bytes.Buffer
	code := Execute([]string{"--wrapper", wrapperDir, "--upstream", upstreamDir, "--fix-preview"}, &stdout, &stderr)
	assert.Equal(t, 0, code, stderr.String())
	assert.Contains(t, stdout.String(), "obsolete")
}

// TestExecute_RegistryTimeoutValidation covers the roadmap #63 flag guard.
func TestExecute_RegistryTimeoutValidation(t *testing.T) {
	for _, value := range []string{"0", "-1s", "0s"} {
		var stdout, stderr bytes.Buffer
		code := Execute([]string{"--old", t.TempDir(), "--new", t.TempDir(), "--registry-timeout", value}, &stdout, &stderr)
		assert.Equal(t, 2, code, "--registry-timeout %s must be rejected", value)
		assert.Contains(t, stderr.String(), "--registry-timeout must be positive")
	}

	var stdout, stderr bytes.Buffer
	code := Execute([]string{
		"--old", t.TempDir(), "--new", t.TempDir(), "--registry-timeout", "5s",
	}, &stdout, &stderr)
	assert.Equal(t, 0, code, stderr.String())
}

// TestExecute_RegistryLatestVersionFailureIsReported covers roadmap #41 in comparison mode. The
// GetLatestVersion error used to be discarded, so the run continued with an empty version string.
func TestExecute_RegistryLatestVersionFailureIsReported(t *testing.T) {
	versionsFails := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/download") {
			http.Error(w, "server error", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer versionsFails.Close()

	origClient := newRegistryClient
	newRegistryClient = func() *registry.Client {
		c := registry.NewClient(versionsFails.Client())
		c.BaseURL = versionsFails.URL
		c.RetryMaxElapsedTime = 20 * time.Millisecond
		return c
	}
	defer func() { newRegistryClient = origClient }()

	// --old with no version triggers a latest-version lookup that now fails loudly.
	var stdout, stderr bytes.Buffer
	code := Execute([]string{"--old", "hashicorp/consul/aws", "--new", t.TempDir()}, &stdout, &stderr)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr.String(), "Error resolving the latest version of hashicorp/consul/aws")

	// --new with no version triggers the same lookup on the new side.
	stdout.Reset()
	stderr.Reset()
	code = Execute([]string{"--old", t.TempDir(), "--new", "hashicorp/consul/aws"}, &stdout, &stderr)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr.String(), "Error resolving the latest version of hashicorp/consul/aws")
	assert.Empty(t, stdout.String())
}

// TestExecute_PinnedResolutionFailureIsReported covers roadmap #41 for the pinned lookup. The
// git path used to discard the error, leaving a nil pinned report and silently disabling
// downgrade detection and the safe-pin recommendation.
func TestExecute_PinnedResolutionFailureIsReported(t *testing.T) {
	repo := createTestTFRepo(t, "v1.0.0", `variable "a" { type = string }`)

	wrapperDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(wrapperDir, "main.tf"), []byte(
		"module \"m\" { source = \""+repo+"\" }"), 0600))

	origMkdir := mkdirTemp
	mkdirTemp = func(string, string) (string, error) { return "", assert.AnError }
	defer func() { mkdirTemp = origMkdir }()

	// A git source resolves the pinned ref through the shared cache, whose temp directory
	// factory is mkdirTemp.
	var stdout, stderr bytes.Buffer
	code := Execute([]string{
		"--wrapper", wrapperDir,
		"--upstream", repo,
		"--old-version", "v1.0.0",
	}, &stdout, &stderr)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr.String(), "Error checking out pinned upstream version v1.0.0")
	assert.Empty(t, stdout.String())

	// A registry source allocates the pinned extraction directory first, so the same failure is
	// reported as a temp directory error.
	stdout.Reset()
	stderr.Reset()
	code = Execute([]string{
		"--wrapper", wrapperDir,
		"--upstream", "hashicorp/consul/aws",
		"--old-version", "1.0.0",
	}, &stdout, &stderr)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr.String(), "Error creating temp dir")
	assert.Empty(t, stdout.String())
}

// TestExecute_RegistryPinnedResolutionFailures covers the registry-backed pinned lookup in
// wrapper mode. Every one of these previously continued silently: mkdirTemp failures vanished
// into `if err == nil`, a failed pinned download left pinnedInfo nil, and a failed inspection of
// the extracted module was discarded. All three disabled downgrade detection and the safe-pin
// recommendation while the report still claimed the target was safe to pin.
func TestExecute_RegistryPinnedResolutionFailures(t *testing.T) {
	wrapperDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(wrapperDir, "main.tf"),
		[]byte(`module "m" { source = "hashicorp/consul/aws" }`), 0600))

	archive := createTestModuleArchive(t)

	// 1. The target extraction directory cannot be created.
	origMkdir := mkdirTemp
	mkdirTemp = func(string, string) (string, error) { return "", assert.AnError }
	defer func() { mkdirTemp = origMkdir }()

	var stdout, stderr bytes.Buffer
	code := Execute([]string{"--wrapper", wrapperDir, "--upstream", "hashicorp/consul/aws", "--new-version", "1.0.0"}, &stdout, &stderr)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr.String(), "Error creating temp dir")

	// 2. The target resolves but the pinned extraction directory cannot be created.
	calls := 0
	mkdirTemp = func(pattern, prefix string) (string, error) {
		calls++
		if calls == 2 {
			return "", assert.AnError
		}
		return origMkdir(pattern, prefix)
	}

	registryOK := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/download") {
			w.Header().Set("Content-Type", "application/octet-stream")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(archive)
		}
	}))
	defer registryOK.Close()

	origClient := newRegistryClient
	newRegistryClient = func() *registry.Client {
		c := registry.NewClient(registryOK.Client())
		c.BaseURL = registryOK.URL
		c.RetryMaxElapsedTime = 20 * time.Millisecond
		return c
	}
	defer func() { newRegistryClient = origClient }()

	stdout.Reset()
	stderr.Reset()
	code = Execute([]string{
		"--wrapper", wrapperDir, "--upstream", "hashicorp/consul/aws",
		"--new-version", "1.0.0", "--old-version", "0.9.0",
	}, &stdout, &stderr)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr.String(), "Error creating temp dir")

	// 3. The pinned download itself fails.
	mkdirTemp = origMkdir
	registryPinnedFails := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The target version downloads successfully; the pinned one does not.
		if strings.HasSuffix(r.URL.Path, "/0.9.0/download") {
			http.Error(w, "gone", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(archive)
	}))
	defer registryPinnedFails.Close()

	newRegistryClient = func() *registry.Client {
		c := registry.NewClient(registryPinnedFails.Client())
		c.BaseURL = registryPinnedFails.URL
		c.RetryMaxElapsedTime = 20 * time.Millisecond
		return c
	}

	stdout.Reset()
	stderr.Reset()
	code = Execute([]string{
		"--wrapper", wrapperDir, "--upstream", "hashicorp/consul/aws",
		"--new-version", "1.0.0", "--old-version", "0.9.0",
	}, &stdout, &stderr)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr.String(), "Error checking out pinned upstream version 0.9.0")
	assert.Empty(t, stdout.String())
}

// TestExecute_PinnedInspectionFailureIsReported covers the discarded ast.Inspect error on the
// pinned module. A git upstream pinned at a ref whose content cannot be parsed must fail loudly
// rather than silently producing a nil pinned report.
func TestExecute_PinnedInspectionFailureIsReported(t *testing.T) {
	repo := createTestTFRepoWithTags(t, []struct{ tag, content string }{
		{"v1.0.0", "this is not valid terraform {{{"},
		{"v2.0.0", `variable "a" { type = string }`},
	})

	wrapperDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(wrapperDir, "main.tf"), []byte(
		"module \"m\" { source = \""+repo+"\" }"), 0600))

	var stdout, stderr bytes.Buffer
	code := Execute([]string{
		"--wrapper", wrapperDir,
		"--upstream", repo,
		"--new-version", "v2.0.0",
		"--old-version", "v1.0.0",
	}, &stdout, &stderr)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr.String(), "Error inspecting pinned upstream module")
	assert.Empty(t, stdout.String())
}

// TestExecute_ClearCache covers roadmap #5. ast.ClearPersistentCache used to call os.RemoveAll on
// whatever --cache-dir named, so `--cache-dir .` destroyed the repository. It now deletes only
// the cache entries and refuses a directory holding anything else.
func TestExecute_ClearCache(t *testing.T) {
	dir := t.TempDir()
	moduleDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(moduleDir, "main.tf"), []byte(`variable "a" { type = string }`), 0600))

	// Populate the persistent cache.
	var stdout, stderr bytes.Buffer
	code := Execute([]string{
		"--old", moduleDir, "--new", moduleDir,
		"--cache-dir", dir, "--registry-timeout", "5s",
	}, &stdout, &stderr)
	require.Equal(t, 0, code, stderr.String())

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.NotEmpty(t, entries, "precondition: the inspection populated the cache")

	// Clearing removes the entries and keeps the directory.
	stdout.Reset()
	stderr.Reset()
	code = Execute([]string{"--cache-dir", dir, "--clear-cache"}, &stdout, &stderr)
	assert.Equal(t, 0, code, stderr.String())
	assert.Contains(t, stdout.String(), "Cleared cached module inspections")
	assert.DirExists(t, dir, "the cache directory must survive a clear")

	entries, err = os.ReadDir(dir)
	require.NoError(t, err)
	assert.Empty(t, entries)

	// The module it analysed is untouched: clearing a cache is not allowed to delete anything else.
	_, err = os.Stat(filepath.Join(moduleDir, "main.tf"))
	assert.NoError(t, err)
}

// TestExecute_ClearCacheRefusesForeignDirectory is the destructive-case regression: pointing the
// cache at a directory of real files must refuse rather than delete them.
func TestExecute_ClearCacheRefusesForeignDirectory(t *testing.T) {
	dir := t.TempDir()
	keep := filepath.Join(dir, "main.tf")
	require.NoError(t, os.WriteFile(keep, []byte(`variable "p" { type = string }`), 0600))

	var stdout, stderr bytes.Buffer
	code := Execute([]string{"--cache-dir", dir, "--clear-cache"}, &stdout, &stderr)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr.String(), "Error clearing cache")
	assert.FileExists(t, keep, "a file this cache never wrote must not be deleted")
}

// TestExecute_ClearCacheRequiresCacheDir confirms the flag cannot be used without a target, since
// there would be no way to know what to clear.
func TestExecute_ClearCacheRequiresCacheDir(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Execute([]string{"--clear-cache"}, &stdout, &stderr)
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr.String(), "--clear-cache requires --cache-dir")
	assert.Empty(t, stdout.String())
}

func TestExecute_VersionFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Execute([]string{"--version"}, &stdout, &stderr)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout.String(), "tfastdiff 0.9.0")
	assert.Empty(t, stderr.String())

	stdout.Reset()
	stderr.Reset()
	code = Execute([]string{"-version"}, &stdout, &stderr)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout.String(), "tfastdiff 0.9.0")
}

func TestExecute_VerboseLogging(t *testing.T) {
	wrapperDir := t.TempDir()
	upstreamDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(upstreamDir, "main.tf"), []byte(`variable "cluster" { type = string }`), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(wrapperDir, "main.tf"), fmt.Appendf(nil, `
module "child" {
  source  = %q
  cluster = "prod"
}
`, upstreamDir), 0644))

	var stdout, stderr bytes.Buffer
	code := Execute([]string{"--wrapper", wrapperDir, "--verbose"}, &stdout, &stderr)
	assert.Equal(t, 0, code)
	assert.Contains(t, stderr.String(), "[verbose]")

	// Compare mode verbose
	stdout.Reset()
	stderr.Reset()
	code = Execute([]string{"--old", upstreamDir, "--new", upstreamDir, "-v"}, &stdout, &stderr)
	assert.Equal(t, 0, code)
	assert.Contains(t, stderr.String(), "[verbose]")
}

func TestExecute_TaintAnalysis(t *testing.T) {
	// Rejection in compare mode
	var stdout, stderr bytes.Buffer
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.tf"), []byte(`variable "x" { type = string }`), 0644))
	code := Execute([]string{"--old", dir, "--new", dir, "--taint", "x"}, &stdout, &stderr)
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr.String(), "--taint is only available in wrapper audit mode")

	// Rejection in fix mode
	stdout.Reset()
	stderr.Reset()
	code = Execute([]string{"--wrapper", dir, "--fix", "--taint", "x"}, &stdout, &stderr)
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr.String(), "--taint is not supported with --fix or --fix-preview")

	// Rejection in fix-preview mode
	stdout.Reset()
	stderr.Reset()
	code = Execute([]string{"--wrapper", dir, "--fix-preview", "--taint", "x"}, &stdout, &stderr)
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr.String(), "--taint is not supported with --fix or --fix-preview")

	// Valid taint analysis in wrapper mode: text and markdown format
	wrapperDir := t.TempDir()
	upstreamDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(upstreamDir, "main.tf"), []byte(`variable "region" { type = string }`), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(wrapperDir, "main.tf"), fmt.Appendf(nil, `
variable "input_region" {
  type = string
}

locals {
  eff_region = var.input_region
}

module "test" {
  source = %q
  region = local.eff_region
}
`, upstreamDir), 0644))

	stdout.Reset()
	stderr.Reset()
	code = Execute([]string{"--wrapper", wrapperDir, "--taint", "input_region", "--format", "text"}, &stdout, &stderr)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout.String(), "VARIABLE TAINT ANALYSIS (var.input_region)")

	stdout.Reset()
	stderr.Reset()
	code = Execute([]string{"--wrapper", wrapperDir, "--taint", "input_region", "--format", "markdown"}, &stdout, &stderr)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout.String(), "Variable Taint Analysis (`var.input_region`)")

	// Error when taint analysis fails
	origTaint := analyzeVariableTaint
	analyzeVariableTaint = func(dir, target string) (*eval.TaintReport, error) {
		return nil, errors.New("simulated taint error")
	}
	defer func() { analyzeVariableTaint = origTaint }()

	stdout.Reset()
	stderr.Reset()
	code = Execute([]string{"--wrapper", wrapperDir, "--taint", "x"}, &stdout, &stderr)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr.String(), "Error analyzing variable taint: simulated taint error")
}

func TestExecute_RegoPolicy(t *testing.T) {
	// 1. Invalid rego policy file path
	var stdout, stderr bytes.Buffer
	code := Execute([]string{"--wrapper", t.TempDir(), "--rego-policy", "/non/existent/policy.rego"}, &stdout, &stderr)
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr.String(), "Error loading Rego policy")

	// Create a valid rego policy that denies when breaking_changes > 0 or bump_type is MAJOR
	regoFile := filepath.Join(t.TempDir(), "rule.rego")
	require.NoError(t, os.WriteFile(regoFile, []byte(`
package terraform.analysis

deny[msg] {
    input.breaking_changes > 0
    msg := "Breaking changes are disallowed by company policy"
}

deny[msg] {
    input.bump_type == "MAJOR"
    msg := "Major version bumps are disallowed by company policy"
}
`), 0644))

	// 2. Wrapper mode with breaking changes triggering rego violation
	wrapperDir := t.TempDir()
	upstreamDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(upstreamDir, "main.tf"), []byte(`variable "cluster" { type = string }`), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(wrapperDir, "main.tf"), fmt.Appendf(nil, `
module "child" {
  source      = %q
  cluster     = "prod"
  removed_arg = "foo"
}
`, upstreamDir), 0644))

	stdout.Reset()
	stderr.Reset()
	code = Execute([]string{"--wrapper", wrapperDir, "--rego-policy", regoFile, "--exit-code"}, &stdout, &stderr)
	assert.Equal(t, 1, code)
	assert.Contains(t, stdout.String(), "Breaking changes are disallowed by company policy")

	// 3. Compare mode triggering rego violation
	oldDir := t.TempDir()
	newDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(oldDir, "main.tf"), []byte(`variable "foo" { type = string }`), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(newDir, "main.tf"), []byte(``), 0644))

	stdout.Reset()
	stderr.Reset()
	code = Execute([]string{"--old", oldDir, "--new", newDir, "--rego-policy", regoFile, "--exit-code"}, &stdout, &stderr)
	assert.Equal(t, 1, code)
	assert.Contains(t, stdout.String(), "Breaking changes are disallowed by company policy")

	// 4. Upgrade-path mode triggering rego violation
	_, cleanup := setupMockRegistryServer(t)
	defer cleanup()
	stdout.Reset()
	stderr.Reset()
	code = Execute([]string{
		"--old", "hashicorp/consul/aws",
		"--new", "hashicorp/consul/aws",
		"--old-version", "0.9.0",
		"--new-version", "1.0.0",
		"--upgrade-path",
		"--rego-policy", regoFile,
		"--exit-code",
	}, &stdout, &stderr)
	assert.Equal(t, 1, code)
	assert.Contains(t, stdout.String(), "Major version bumps are disallowed by company policy")
}

func TestExecute_RegistrySubmodules_WrapperAndCompare(t *testing.T) {
	_, cleanup := setupMockRegistryServer(t)
	defer cleanup()

	// 1. Direct comparison with submodule
	var stdout, stderr bytes.Buffer
	code := Execute([]string{
		"--old", "hashicorp/consul/aws//modules/sub",
		"--new", "hashicorp/consul/aws//modules/sub",
		"--old-version", "0.9.0",
		"--new-version", "1.0.0",
	}, &stdout, &stderr)
	assert.Equal(t, 0, code, stderr.String())

	// 2. Wrapper mode with submodule
	wrapperDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(wrapperDir, "main.tf"), []byte(`
module "submod" {
  source  = "hashicorp/consul/aws//modules/sub"
  version = "0.9.0"
  sub_var = "foo"
}
`), 0600))

	stdout.Reset()
	stderr.Reset()
	code = Execute([]string{
		"--wrapper", wrapperDir,
		"--upstream", "hashicorp/consul/aws//modules/sub",
		"--old-version", "0.9.0",
		"--new-version", "1.0.0",
	}, &stdout, &stderr)
	assert.Equal(t, 0, code, stderr.String())
}

func TestExecute_FixAndFixPreview_AddressShifts(t *testing.T) {
	repoDir := createTestTFRepoWithTags(t, []struct{ tag, content string }{
		{tag: "v1.0.0", content: "resource \"aws_s3_bucket\" \"b\" {\n  bucket = \"my-bucket\"\n}\nvariable \"removed_input\" { type = string }\n"},
		{tag: "v2.0.0", content: "resource \"aws_s3_bucket\" \"buckets\" {\n  for_each = toset([\"my-bucket\"])\n  bucket = each.key\n}\n"},
	})

	wrapperDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(wrapperDir, "main.tf"), []byte(`
module "upstream" {
  source        = "`+repoDir+`"
  removed_input = "bar"
}
`), 0600))

	var stdout, stderr bytes.Buffer

	// 1. --fix-preview with both removed input patch AND moved blocks preview
	code := Execute([]string{
		"--wrapper", wrapperDir,
		"--upstream", repoDir,
		"--old-version", "v1.0.0",
		"--new-version", "v2.0.0",
		"--fix-preview",
	}, &stdout, &stderr)
	assert.Equal(t, 0, code, stderr.String())
	assert.Contains(t, stdout.String(), "moved.tf")
	assert.Contains(t, stdout.String(), "aws_s3_bucket.b")

	// 2. --fix-preview with only moved blocks (no removed inputs)
	wrapperNoRemoved := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(wrapperNoRemoved, "main.tf"), []byte(`
module "upstream" {
  source = "`+repoDir+`"
}
`), 0600))
	stdout.Reset()
	stderr.Reset()
	code = Execute([]string{
		"--wrapper", wrapperNoRemoved,
		"--upstream", repoDir,
		"--old-version", "v1.0.0",
		"--new-version", "v2.0.0",
		"--fix-preview",
	}, &stdout, &stderr)
	assert.Equal(t, 0, code, stderr.String())
	assert.Contains(t, stdout.String(), "moved.tf")

	// 3. --fix-preview with policy violation
	policyFile := filepath.Join(t.TempDir(), "policy.json")
	require.NoError(t, os.WriteFile(policyFile, []byte(`{"block_breaking":true}`), 0600))
	stdout.Reset()
	stderr.Reset()
	code = Execute([]string{
		"--wrapper", wrapperDir,
		"--upstream", repoDir,
		"--old-version", "v1.0.0",
		"--new-version", "v2.0.0",
		"--fix-preview",
		"--policy", policyFile,
	}, &stdout, &stderr)
	assert.Equal(t, 1, code)

	// 4. --fix applies removed inputs and creates moved.tf
	stdout.Reset()
	stderr.Reset()
	code = Execute([]string{
		"--wrapper", wrapperDir,
		"--upstream", repoDir,
		"--old-version", "v1.0.0",
		"--new-version", "v2.0.0",
		"--fix",
	}, &stdout, &stderr)
	assert.Equal(t, 0, code, stderr.String())
	assert.Contains(t, stdout.String(), "Applied fixes to 2 file(s)")
	movedContent, err := os.ReadFile(filepath.Join(wrapperDir, "moved.tf"))
	require.NoError(t, err)
	assert.Contains(t, string(movedContent), "from = aws_s3_bucket.b")

	// 5. --fix error writing moved.tf (directory collision)
	errWrapper := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(errWrapper, "main.tf"), []byte(`
module "upstream" {
  source = "`+repoDir+`"
}
`), 0600))
	require.NoError(t, os.Mkdir(filepath.Join(errWrapper, "moved.tf"), 0755))
	stdout.Reset()
	stderr.Reset()
	code = Execute([]string{
		"--wrapper", errWrapper,
		"--upstream", repoDir,
		"--old-version", "v1.0.0",
		"--new-version", "v2.0.0",
		"--fix",
	}, &stdout, &stderr)
	assert.Equal(t, 1, code)
	assert.Contains(t, stderr.String(), "Error writing moved blocks")
}

func TestExecute_GoldenPlanVerification(t *testing.T) {
	upstreamDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(upstreamDir, "main.tf"), []byte(`
variable "cluster_name" {
  type    = string
  default = "my-cluster"
}
resource "google_container_cluster" "primary" {
  name     = var.cluster_name
  location = "us-central1"
}
`), 0600))

	wrapperDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(wrapperDir, "main.tf"), []byte(`
module "gke" {
  source       = "`+upstreamDir+`"
  cluster_name = "prod-cluster"
}
`), 0600))

	var stdout, stderr bytes.Buffer

	// 1. Invalid / missing golden plan path -> returns 2 with error
	code := Execute([]string{
		"--wrapper", wrapperDir,
		"--upstream", upstreamDir,
		"--golden-plan", filepath.Join(t.TempDir(), "nonexistent.json"),
	}, &stdout, &stderr)
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr.String(), "Error verifying golden plan")

	// 2. Explicit golden plan with replacement action and recreation hazard -> exit code 1 with --exit-code
	planWithHazard := filepath.Join(t.TempDir(), "hazard-plan.json")
	require.NoError(t, os.WriteFile(planWithHazard, []byte(`{
		"format_version": "1.0",
		"resource_changes": [
			{
				"address": "module.gke.google_container_cluster.primary",
				"type": "google_container_cluster",
				"name": "primary",
				"change": {
					"actions": ["delete", "create"]
				}
			}
		]
	}`), 0600))

	stdout.Reset()
	stderr.Reset()
	code = Execute([]string{
		"--wrapper", wrapperDir,
		"--upstream", upstreamDir,
		"--golden-plan", planWithHazard,
		"--exit-code",
		"-v",
	}, &stdout, &stderr)
	assert.Equal(t, 1, code)
	assert.Contains(t, stdout.String(), "GOLDEN PLAN STATE RECREATION VERIFICATION")
	assert.Contains(t, stdout.String(), "module.gke.google_container_cluster.primary")

	// 3. Clean golden plan in markdown format -> exit code 0
	cleanPlan := filepath.Join(t.TempDir(), "clean-plan.json")
	require.NoError(t, os.WriteFile(cleanPlan, []byte(`{
		"format_version": "1.0",
		"resource_changes": [
			{
				"address": "aws_s3_bucket.b",
				"type": "aws_s3_bucket",
				"name": "b",
				"change": {
					"actions": ["no-op"]
				}
			}
		]
	}`), 0600))

	stdout.Reset()
	stderr.Reset()
	code = Execute([]string{
		"--wrapper", wrapperDir,
		"--upstream", upstreamDir,
		"--golden-plan", cleanPlan,
		"--format", "markdown",
	}, &stdout, &stderr)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout.String(), "### Golden Plan State Recreation Verification")
	assert.Contains(t, stdout.String(), "Plan Verification Passed")

	// 4. Autodiscovery of golden-plan.json in wrapper directory
	autoWrapperDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(autoWrapperDir, "main.tf"), []byte(`
module "gke" {
  source = "`+upstreamDir+`"
}
`), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(autoWrapperDir, "golden-plan.json"), []byte(`{
		"format_version": "1.0",
		"resource_changes": [
			{
				"address": "aws_s3_bucket.data",
				"type": "aws_s3_bucket",
				"name": "data",
				"change": {
					"actions": ["update"]
				}
			}
		]
	}`), 0600))

	stdout.Reset()
	stderr.Reset()
	code = Execute([]string{
		"--wrapper", autoWrapperDir,
		"--upstream", upstreamDir,
		"-v",
	}, &stdout, &stderr)
	assert.Equal(t, 0, code)
	assert.Contains(t, stderr.String(), "Autodiscovered golden plan")
	assert.Contains(t, stdout.String(), "(autodiscovered)")
}
