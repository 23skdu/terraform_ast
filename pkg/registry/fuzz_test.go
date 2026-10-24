package registry

import (
	"os"
	"strings"
	"testing"
)

// FuzzIsRegistrySource verifies the source classifier never panics and never labels a source that
// is already known to be a local path, git URL or VCS address as a Registry identifier.
func FuzzIsRegistrySource(f *testing.F) {
	for _, seed := range []string{
		"terraform-google-modules/kubernetes-engine/google",
		"app.terraform.io/my-org/eks/aws",
		"./modules/eks",
		"../modules/eks",
		"/absolute/path",
		"git::https://github.com/org/repo.git",
		"git@github.com:org/repo.git",
		"https://github.com/org/repo.git",
		"git://github.com/org/repo.git",
		"ssh://git@github.com/org/repo",
		"github.com/org/repo",
		"bitbucket.org/org/repo",
		"~/modules/gke/v30",
		"a//b",
		"a/b/",
		"",
		"   ",
		"one/two",
		"one/two/three/four/five",
	} {
		f.Add(seed)
	}

	nonRegistryPrefixes := []string{".", "/", "~", "git@", "github.com/", "bitbucket.org/"}
	f.Fuzz(func(t *testing.T, source string) {
		got := IsRegistrySource(source)

		// Any URL scheme -- http, https, git, ssh, file, or a "git::"-prefixed one -- is a VCS or
		// generic remote source, never a registry address.
		if got && strings.Contains(source, "://") {
			t.Fatalf("%q must never be treated as a registry source", source)
		}
		for _, prefix := range nonRegistryPrefixes {
			if strings.HasPrefix(source, prefix) && got {
				t.Fatalf("%q must never be treated as a registry source", source)
			}
		}
		if strings.HasSuffix(source, ".git") && got {
			t.Fatalf("%q must never be treated as a registry source", source)
		}
		if strings.TrimSpace(source) == "" && got {
			t.Fatal("an empty source is never a registry source")
		}
	})
}

// FuzzParseRegistrySource ensures the registry address parser only accepts well formed sources and
// never panics on arbitrary input, and that it stays in agreement with the classifier: a source the
// parser accepts must be classified as a registry source unless it names an existing directory,
// the one probe the pure parser does not perform.
func FuzzParseRegistrySource(f *testing.F) {
	for _, seed := range []string{
		"terraform-google-modules/kubernetes-engine/google",
		"app.terraform.io/my-org/eks/aws",
		"invalid",
		"a/b",
		"a/b/c/d/e",
		"",
		"/////",
		"git::a/b/c",
		"git@github.com/org/repo",
		"github.com/org/repo",
		"bitbucket.org/org/repo",
		"a/b/c.git",
		"./modules/eks",
		"hashicorp/consul/aws?ref=1.0.0",
		"a/../b",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, source string) {
		host, namespace, name, provider, err := ParseRegistrySource(source)
		if err != nil {
			if host != "" || namespace != "" || name != "" || provider != "" {
				t.Fatalf("a failed parse must not return partial results for %q", source)
			}
			return
		}
		if host == "" || namespace == "" || name == "" || provider == "" {
			t.Fatalf("a successful parse of %q returned an empty component", source)
		}
		if IsRegistrySource(source) {
			return
		}
		// The classifier may only be stricter than the parser for sources that resolve to an
		// existing directory, the filesystem probe ParseRegistrySource deliberately skips.
		trimmed := strings.TrimSpace(source)
		if fi, statErr := os.Stat(trimmed); statErr != nil || !fi.IsDir() {
			t.Fatalf("ParseRegistrySource accepted %q but IsRegistrySource rejected it", source)
		}
	})
}
