package docker

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDockerfile_DevSecOpsStaticAnalysis validates that Dockerfile and .dockerignore
// strictly adhere to DevSecOps standards (multi-stage, non-root, static binaries, minimal attack surface).
func TestDockerfile_DevSecOpsStaticAnalysis(t *testing.T) {
	rootPath, err := filepath.Abs("../../")
	require.NoError(t, err)

	dockerfilePath := filepath.Join(rootPath, "Dockerfile")
	dockerignorePath := filepath.Join(rootPath, ".dockerignore")

	// 1. Verify existence
	require.FileExists(t, dockerfilePath, "Dockerfile must exist at repository root")
	require.FileExists(t, dockerignorePath, ".dockerignore must exist at repository root")

	dockerfileBytes, err := os.ReadFile(dockerfilePath)
	require.NoError(t, err)
	dockerfileContent := string(dockerfileBytes)

	// 2. Multi-stage build check
	assert.Contains(t, dockerfileContent, "AS builder", "Dockerfile must use multi-stage builds")

	// 3. Dependency caching & verification
	assert.Contains(t, dockerfileContent, "COPY go.mod go.sum ./")
	assert.Contains(t, dockerfileContent, "go mod verify", "Dependencies must be verified against checksums")

	// 4. Hardened binary compilation checks
	assert.Contains(t, dockerfileContent, "CGO_ENABLED=0", "Must disable CGO for a statically-linked binary")
	assert.Contains(t, dockerfileContent, "-trimpath", "Must use -trimpath to prevent host path disclosure")
	assert.Contains(t, dockerfileContent, "-ldflags=\"-s -w", "Must strip symbol table and debugging info")

	// 5. Final stage security
	assert.Contains(t, dockerfileContent, "gcr.io/distroless/static-debian12:nonroot", "Must use minimal distroless base image")
	assert.Contains(t, dockerfileContent, "USER 65532:65532", "Must explicitly enforce non-root user execution")
	assert.Contains(t, dockerfileContent, `ENTRYPOINT ["/usr/local/bin/tfastdiff"]`, "Must define a secure direct entrypoint")

	// 6. OCI metadata annotations
	assert.Contains(t, dockerfileContent, "org.opencontainers.image.title")
	assert.Contains(t, dockerfileContent, "org.opencontainers.image.source")
	assert.Contains(t, dockerfileContent, "org.opencontainers.image.licenses")

	// 7. .dockerignore validation
	dockerignoreBytes, err := os.ReadFile(dockerignorePath)
	require.NoError(t, err)
	dockerignoreContent := string(dockerignoreBytes)

	requiredExclusions := []string{".git", "test/", "*.out", "coverage.*", "bin/"}
	for _, excl := range requiredExclusions {
		assert.Contains(t, dockerignoreContent, excl, ".dockerignore must exclude "+excl)
	}
}

// TestDockerfile_BuildAndRuntimeSecurity builds the container and validates runtime security constraints.
func TestDockerfile_BuildAndRuntimeSecurity(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("Docker executable not found; skipping dynamic container tests")
	}

	rootPath, err := filepath.Abs("../../")
	require.NoError(t, err)

	imageTag := "tfastdiff:test"

	// 1. Build image
	cmdBuild := exec.Command("docker", "build", "-t", imageTag, rootPath)
	buildOutput, err := cmdBuild.CombinedOutput()
	require.NoError(t, err, "Docker build failed: %s", string(buildOutput))

	// 2. Validate Image Metadata via docker inspect
	cmdInspect := exec.Command("docker", "inspect", imageTag)
	inspectOut, err := cmdInspect.CombinedOutput()
	require.NoError(t, err)

	var inspectData []struct {
		Config struct {
			User       string            `json:"User"`
			WorkingDir string            `json:"WorkingDir"`
			Entrypoint []string          `json:"Entrypoint"`
			Labels     map[string]string `json:"Labels"`
		} `json:"Config"`
	}
	require.NoError(t, json.Unmarshal(inspectOut, &inspectData))
	require.NotEmpty(t, inspectData)

	cfg := inspectData[0].Config
	assert.Equal(t, "65532:65532", cfg.User, "Container must run as non-root user 65532")
	assert.Equal(t, "/workspace", cfg.WorkingDir, "Container must have working directory set to /workspace")
	assert.Equal(t, []string{"/usr/local/bin/tfastdiff"}, cfg.Entrypoint)
	assert.Equal(t, "terraform-ast-diff", cfg.Labels["org.opencontainers.image.title"])

	// 3. Test Container Execution: --help flag returns usage and expected exit code 2
	cmdRunHelp := exec.Command("docker", "run", "--rm", imageTag, "--help")
	helpOut, err := cmdRunHelp.CombinedOutput()
	var exitErr *exec.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, 2, exitErr.ExitCode(), "Flag parsing on --help must return code 2")
	assert.Contains(t, string(helpOut), "Usage of tfastdiff:")

	// 4. Test Container Execution: Analysis on mounted fixtures
	fixturesPath := filepath.Join(rootPath, "test", "fixtures")
	cmdRunAnalysis := exec.Command(
		"docker", "run", "--rm",
		"--read-only",
		"--security-opt", "no-new-privileges:true",
		"-v", fixturesPath+":/workspace/fixtures:ro",
		imageTag,
		"--old", "/workspace/fixtures/upstream_gke_v1",
		"--new", "/workspace/fixtures/upstream_gke_v2",
		"--format", "markdown",
	)
	analysisOut, err := cmdRunAnalysis.CombinedOutput()
	require.NoError(t, err, "Container analysis failed: %s", string(analysisOut))
	assert.Contains(t, string(analysisOut), "Terraform AST Compatibility Diff Report")
	assert.Contains(t, string(analysisOut), "State Destruction & Recreation Hazards")

	// 5. Test Container Execution: Enforce exit code 1 on breaking changes
	cmdRunExitCode := exec.Command(
		"docker", "run", "--rm",
		"-v", fixturesPath+":/workspace/fixtures:ro",
		imageTag,
		"--old", "/workspace/fixtures/upstream_gke_v1",
		"--new", "/workspace/fixtures/upstream_gke_v2",
		"--exit-code",
	)
	err = cmdRunExitCode.Run()
	var breakExitErr *exec.ExitError
	require.ErrorAs(t, err, &breakExitErr)
	assert.Equal(t, 1, breakExitErr.ExitCode(), "CLI must return exit code 1 on breaking changes inside container")

	// 6. Security Assurance: Verify distroless container has NO shell
	cmdNoShell := exec.Command("docker", "run", "--rm", "--entrypoint", "/bin/sh", imageTag)
	noShellOut, err := cmdNoShell.CombinedOutput()
	assert.Error(t, err, "Distroless container must not contain /bin/sh")
	assert.True(t,
		strings.Contains(string(noShellOut), "no such file or directory") ||
			strings.Contains(string(noShellOut), "not found") ||
			strings.Contains(string(noShellOut), "executable file not found"),
		"Expected shell execution to fail because image is distroless, got: %s", string(noShellOut),
	)
}
