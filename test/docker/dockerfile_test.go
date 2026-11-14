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

	// 2. Validate Dockerfile and .dockerignore against DevSecOps policy rules
	require.NoError(t, validateDockerfilePolicy(dockerfileContent))

	dockerignoreBytes, err := os.ReadFile(dockerignorePath)
	require.NoError(t, err)
	dockerignoreContent := string(dockerignoreBytes)
	require.NoError(t, validateDockerignorePolicy(dockerignoreContent))
}

func TestValidateDockerfileAndContainerPolicies_Unit(t *testing.T) {
	rootPath, err := filepath.Abs("../../")
	require.NoError(t, err)

	dockerfileBytes, err := os.ReadFile(filepath.Join(rootPath, "Dockerfile"))
	require.NoError(t, err)
	validDockerfile := string(dockerfileBytes)

	// Every required token removal must be caught by validateDockerfilePolicy.
	for _, rule := range requiredDockerfileTokens {
		broken := strings.ReplaceAll(validDockerfile, rule.token, "")
		err := validateDockerfilePolicy(broken)
		require.Error(t, err)
		assert.Contains(t, err.Error(), rule.token)
	}

	dockerignoreBytes, err := os.ReadFile(filepath.Join(rootPath, ".dockerignore"))
	require.NoError(t, err)
	validDockerignore := string(dockerignoreBytes)

	for _, excl := range requiredDockerignoreExclusions {
		broken := strings.ReplaceAll(validDockerignore, excl, "")
		err := validateDockerignorePolicy(broken)
		require.Error(t, err)
		assert.Contains(t, err.Error(), excl)
	}

	validCfg := containerConfig{
		User:       "65532:65532",
		WorkingDir: "/workspace",
		Entrypoint: []string{"/usr/local/bin/tfastdiff"},
		Labels:     map[string]string{"org.opencontainers.image.title": "terraform-ast-diff"},
	}
	require.NoError(t, validateContainerMetadata(validCfg))

	badUser := validCfg
	badUser.User = "0:0"
	assert.ErrorContains(t, validateContainerMetadata(badUser), "non-root user")

	badWorkdir := validCfg
	badWorkdir.WorkingDir = "/root"
	assert.ErrorContains(t, validateContainerMetadata(badWorkdir), "working directory")

	badEntry := validCfg
	badEntry.Entrypoint = []string{"/bin/sh"}
	assert.ErrorContains(t, validateContainerMetadata(badEntry), "entrypoint")

	badLabel := validCfg
	badLabel.Labels = map[string]string{}
	assert.ErrorContains(t, validateContainerMetadata(badLabel), "OCI title label")
}

func TestDeveloperWorkflowManifests(t *testing.T) {
	hooks, err := os.ReadFile("../../.pre-commit-hooks.yaml")
	require.NoError(t, err)
	assert.Contains(t, string(hooks), "id: tfastdiff")
	assert.Contains(t, string(hooks), "entry: tfastdiff")
	assert.Contains(t, string(hooks), "pass_filenames: false")

	ciWorkflow, err := os.ReadFile("../../.github/workflows/ci.yml")
	require.NoError(t, err)
	ciContent := string(ciWorkflow)
	assert.Contains(t, ciContent, "golangci-lint-action")
	assert.Contains(t, ciContent, "scripts/verify.sh")
	assert.Contains(t, ciContent, "build-push-action")
	assert.Contains(t, ciContent, "ghcr.io")
	assert.Contains(t, ciContent, "docker/setup-buildx-action")

	// Assert the actions are referenced without pinning a major version, which Dependabot bumps
	// and which would otherwise fail this test on every dependency update.
	assert.Regexp(t, `actions/checkout@v[0-9]+`, ciContent)
	assert.Regexp(t, `actions/setup-go@v[0-9]+`, ciContent)

	dependabot, err := os.ReadFile("../../.github/dependabot.yml")
	require.NoError(t, err)
	dependabotContent := string(dependabot)
	assert.Contains(t, dependabotContent, `package-ecosystem: "gomod"`)
	assert.Contains(t, dependabotContent, `package-ecosystem: "pip"`)
	assert.Contains(t, dependabotContent, `package-ecosystem: "github-actions"`)
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
		Config containerConfig `json:"Config"`
	}
	require.NoError(t, json.Unmarshal(inspectOut, &inspectData))
	require.NotEmpty(t, inspectData)
	require.NoError(t, validateContainerMetadata(inspectData[0].Config))

	// 2b. Test Container Execution: --version flag returns 0.9.0 and exit code 0
	cmdVersion := exec.Command("docker", "run", "--rm", "--read-only", imageTag, "--version")
	versionOut, err := cmdVersion.CombinedOutput()
	require.NoError(t, err)
	assert.Contains(t, string(versionOut), "tfastdiff 0.9.0")

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

	// 6. Test Container Execution: Identity diff on modern_constructs fixture inside read-only container
	cmdModernIdentity := exec.Command(
		"docker", "run", "--rm",
		"--read-only",
		"--security-opt", "no-new-privileges:true",
		"-v", fixturesPath+":/workspace/fixtures:ro",
		imageTag,
		"--old", "/workspace/fixtures/modern_constructs",
		"--new", "/workspace/fixtures/modern_constructs",
		"--format", "json",
		"--exit-code",
	)
	modernOut, err := cmdModernIdentity.CombinedOutput()
	require.NoError(t, err, "Container modern_constructs identity failed: %s", string(modernOut))
	assert.Contains(t, string(modernOut), `"verdict": "SAFE"`)

	// 7. Security Assurance: Verify distroless container has NO shell
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
