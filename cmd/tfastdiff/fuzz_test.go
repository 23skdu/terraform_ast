package main

import (
	"io"
	"strings"
	"testing"
)

func FuzzCLI(f *testing.F) {
	f.Add("--help", "", "", "")
	f.Add("--version", "", "", "")
	f.Add("--format", "json", "--old", "test/fixtures/upstream_gke_v1")
	f.Add("--format", "markdown", "--wrapper", "test/fixtures/gke_wrapper")
	f.Add("--old", "invalid", "--format", "text")
	f.Add("--unknown-flag", "value", "", "")
	f.Add("", "", "", "")

	f.Fuzz(func(t *testing.T, a1, a2, a3, a4 string) {
		var args []string
		for _, a := range []string{a1, a2, a3, a4} {
			if strings.TrimSpace(a) != "" {
				args = append(args, a)
			}
		}

		code := Execute(args, io.Discard, io.Discard)
		if code < 0 || code > 2 {
			t.Fatalf("unexpected exit code: %d for args: %v", code, args)
		}
	})
}
