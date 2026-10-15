package registry

import (
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

	localPrefixes := []string{".", "/", "~", "git::", "git@", "http://", "https://"}
	f.Fuzz(func(t *testing.T, source string) {
		got := IsRegistrySource(source)

		for _, prefix := range localPrefixes {
			if len(source) >= len(prefix) && source[:len(prefix)] == prefix {
				if got {
					t.Fatalf("%q must never be treated as a registry source", source)
				}
				return
			}
		}
		if len(source) >= 4 && source[len(source)-4:] == ".git" && got {
			t.Fatalf("%q must never be treated as a registry source", source)
		}
		if source == "" && got {
			t.Fatal("an empty source is never a registry source")
		}
	})
}

// FuzzParseRegistrySource ensures the registry address parser only accepts well formed sources and
// never panics on arbitrary input.
func FuzzParseRegistrySource(f *testing.F) {
	for _, seed := range []string{
		"terraform-google-modules/kubernetes-engine/google",
		"app.terraform.io/my-org/eks/aws",
		"invalid",
		"a/b",
		"a/b/c/d/e",
		"",
		"/////",
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
	})
}
