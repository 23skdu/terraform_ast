package gitops

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func FuzzParseGitSource(f *testing.F) {
	f.Add("git::https://github.com/terraform-aws-modules/terraform-aws-eks.git//modules/karpenter?ref=v20.0.0")
	f.Add("git@github.com:terraform-google-modules/terraform-google-kubernetes-engine.git//modules/beta-private-cluster?ref=v30.0.0")
	f.Add("github.com/org/repo//sub/path")
	f.Add("https://example.com/repo.git?ref=main")
	f.Add("file:///var/tmp/repo.git")
	f.Add("git://git.example.com/project.git")
	f.Add("ssh://git@git.example.com:22/project.git//sub?ref=v1.0.0")
	f.Add("invalid:url:pattern")
	f.Add("")

	f.Fuzz(func(t *testing.T, source string) {
		_ = IsGitURL(source)

		parsed := ParseGitSource(source)
		if IsGitURL(source) && strings.Contains(source, "?ref=") && !strings.Contains(source, "?ref=&") {
			_ = parsed.Ref
		}
	})
}

func FuzzResolveSubDir(f *testing.F) {
	f.Add("modules/vpc")
	f.Add("../escaped")
	f.Add("/absolute/path")
	f.Add(".")
	f.Add("")
	f.Add("a/b/c/../../d")

	f.Fuzz(func(t *testing.T, subDir string) {
		tempDir, err := os.MkdirTemp("", "fuzz-sub-*")
		if err != nil {
			return
		}
		defer func() {
			_ = os.RemoveAll(tempDir)
		}()

		res, err := resolveSubDir(tempDir, subDir)
		if err == nil {
			rel, relErr := filepath.Rel(tempDir, res)
			if relErr != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
				t.Fatalf("path traversal escape detected: root=%q, res=%q", tempDir, res)
			}
		}
	})
}
