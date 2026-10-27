package docker

import (
	"fmt"
	"strings"
)

type containerConfig struct {
	User       string            `json:"User"`
	WorkingDir string            `json:"WorkingDir"`
	Entrypoint []string          `json:"Entrypoint"`
	Labels     map[string]string `json:"Labels"`
}

var requiredDockerfileTokens = []struct {
	token   string
	message string
}{
	{"AS builder", "Dockerfile must use multi-stage builds"},
	{"COPY go.mod go.sum ./", "Dockerfile must copy go.mod and go.sum before downloading modules"},
	{"go mod verify", "Dependencies must be verified against checksums"},
	{"CGO_ENABLED=0", "Must disable CGO for a statically-linked binary"},
	{"-trimpath", "Must use -trimpath to prevent host path disclosure"},
	{"-ldflags=\"-s -w", "Must strip symbol table and debugging info"},
	{"gcr.io/distroless/static-debian12:nonroot", "Must use minimal distroless base image"},
	{"USER 65532:65532", "Must explicitly enforce non-root user execution"},
	{`ENTRYPOINT ["/usr/local/bin/tfastdiff"]`, "Must define a secure direct entrypoint"},
	{"TARGETARCH", "Must support multi-arch target builds"},
	{"org.opencontainers.image.title", "Must include OCI title label"},
	{"org.opencontainers.image.source", "Must include OCI source label"},
	{"org.opencontainers.image.licenses", "Must include OCI licenses label"},
	{`org.opencontainers.image.version="0.9.0"`, "Must include current OCI version label"},
}

var requiredDockerignoreExclusions = []string{
	".git",
	"test/",
	"*.out",
	"coverage.*",
	"bin/",
}

func validateDockerfilePolicy(content string) error {
	for _, rule := range requiredDockerfileTokens {
		if !strings.Contains(content, rule.token) {
			return fmt.Errorf("%s (missing %s)", rule.message, rule.token)
		}
	}
	return nil
}

func validateDockerignorePolicy(content string) error {
	for _, excl := range requiredDockerignoreExclusions {
		if !strings.Contains(content, excl) {
			return fmt.Errorf(".dockerignore must exclude %s", excl)
		}
	}
	return nil
}

func validateContainerMetadata(cfg containerConfig) error {
	if cfg.User != "65532:65532" {
		return fmt.Errorf("container must run as non-root user 65532:65532, got %q", cfg.User)
	}
	if cfg.WorkingDir != "/workspace" {
		return fmt.Errorf("container must have working directory /workspace, got %q", cfg.WorkingDir)
	}
	if len(cfg.Entrypoint) != 1 || cfg.Entrypoint[0] != "/usr/local/bin/tfastdiff" {
		return fmt.Errorf("unexpected container entrypoint: %v", cfg.Entrypoint)
	}
	if cfg.Labels["org.opencontainers.image.title"] != "terraform-ast-diff" {
		return fmt.Errorf("missing or invalid OCI title label: %q", cfg.Labels["org.opencontainers.image.title"])
	}
	return nil
}
