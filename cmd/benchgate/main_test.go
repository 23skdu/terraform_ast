package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const passingBenchOutput = "BenchmarkAlpha-8  10  100.0 ns/op\nBenchmarkBeta-8  10  50.0 ns/op\n"

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, os.ErrClosed }

func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRunArgumentErrors(t *testing.T) {
	current := writeFile(t, "bench.txt", passingBenchOutput)

	cases := []struct {
		name string
		args []string
	}{
		{"bad flag", []string{"--bogus"}},
		{"missing current", nil},
		{"negative threshold", []string{"--current", current, "--threshold", "-1"}},
		{"missing current file", []string{"--current", filepath.Join(t.TempDir(), "nope.txt")}},
		{"no benchmark results", []string{"--current", writeFile(t, "empty.txt", "goos: linux\n")}},
		{"missing baseline", []string{"--current", current}},
		{"bad baseline", []string{"--current", current, "--baseline", writeFile(t, "bad.json", "{")}},
		{"zero baseline value", []string{"--current", current, "--baseline", writeFile(t, "zero.json", `{"benchmarks_ns_per_op":{"BenchmarkAlpha":0}}`)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run(tc.args, strings.NewReader(""), &stdout, &stderr); code != 2 {
				t.Fatalf("code = %d, want 2; stderr=%s", code, stderr.String())
			}
			if stderr.Len() == 0 {
				t.Fatal("expected stderr message")
			}
		})
	}
}

func TestRunCheckPass(t *testing.T) {
	current := writeFile(t, "bench.txt", passingBenchOutput)
	baseline := writeFile(t, "baseline.json", `{"benchmarks_ns_per_op":{"BenchmarkAlpha":100,"BenchmarkBeta":50}}`)
	var stdout, stderr bytes.Buffer
	code := run([]string{"--current", current, "--baseline", baseline}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d; stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "No benchmark regressions") {
		t.Fatalf("stdout = %s", stdout.String())
	}
}

func TestRunCheckFromStdin(t *testing.T) {
	baseline := writeFile(t, "baseline.json", `{"benchmarks_ns_per_op":{"BenchmarkAlpha":100,"BenchmarkBeta":50}}`)
	var stdout, stderr bytes.Buffer
	code := run([]string{"--current", "-", "--baseline", baseline}, strings.NewReader(passingBenchOutput), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d; stderr=%s", code, stderr.String())
	}
}

func TestRunCheckFails(t *testing.T) {
	baseline := writeFile(t, "baseline.json", `{"benchmarks_ns_per_op":{"BenchmarkAlpha":100,"BenchmarkBeta":50}}`)

	regression := writeFile(t, "regression.txt", "BenchmarkAlpha-8  10  150.0 ns/op\nBenchmarkBeta-8  10  50.0 ns/op\n")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--current", regression, "--baseline", baseline}, strings.NewReader(""), &stdout, &stderr); code != 1 {
		t.Fatalf("regression code = %d; stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "BenchmarkAlpha") {
		t.Fatalf("stdout = %s", stdout.String())
	}

	missing := writeFile(t, "missing.txt", "BenchmarkAlpha-8  10  100.0 ns/op\n")
	stdout.Reset()
	if code := run([]string{"--current", missing, "--baseline", baseline}, strings.NewReader(""), &stdout, &stderr); code != 1 {
		t.Fatalf("missing code = %d; stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "BenchmarkBeta") {
		t.Fatalf("stdout = %s", stdout.String())
	}
}

func TestRunReportWriteError(t *testing.T) {
	current := writeFile(t, "bench.txt", passingBenchOutput)
	baseline := writeFile(t, "baseline.json", `{"benchmarks_ns_per_op":{"BenchmarkAlpha":100,"BenchmarkBeta":50}}`)
	var stderr bytes.Buffer
	if code := run([]string{"--current", current, "--baseline", baseline}, strings.NewReader(""), failingWriter{}, &stderr); code != 1 {
		t.Fatalf("code = %d; stderr=%s", code, stderr.String())
	}
}

func TestRunRecord(t *testing.T) {
	current := writeFile(t, "bench.txt", passingBenchOutput)
	dir := t.TempDir()
	baselinePath := filepath.Join(dir, "baseline.json")
	markdownPath := filepath.Join(dir, "baseline.md")
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"--current", current,
		"--write-baseline", baselinePath,
		"--write-markdown", markdownPath,
	}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code = %d; stderr=%s", code, stderr.String())
	}
	if _, err := os.Stat(baselinePath); err != nil {
		t.Fatalf("baseline not written: %v", err)
	}
	markdown, err := os.ReadFile(markdownPath)
	if err != nil {
		t.Fatalf("markdown not written: %v", err)
	}
	if !strings.Contains(string(markdown), "BenchmarkAlpha") {
		t.Fatalf("markdown = %s", markdown)
	}
	if !strings.Contains(stdout.String(), "Wrote baseline") || !strings.Contains(stdout.String(), "Wrote markdown") {
		t.Fatalf("stdout = %s", stdout.String())
	}
}

func TestRunRecordBaselineError(t *testing.T) {
	current := writeFile(t, "bench.txt", passingBenchOutput)
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"--current", current,
		"--write-baseline", filepath.Join(blocker, "baseline.json"),
	}, strings.NewReader(""), &stdout, &stderr)
	if code != 1 {
		t.Fatalf("code = %d; stderr=%s", code, stderr.String())
	}
}

func TestRunRecordMarkdownOpenError(t *testing.T) {
	current := writeFile(t, "bench.txt", passingBenchOutput)
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"--current", current,
		"--write-markdown", filepath.Join(t.TempDir(), "missing", "baseline.md"),
	}, strings.NewReader(""), &stdout, &stderr)
	if code != 1 {
		t.Fatalf("code = %d; stderr=%s", code, stderr.String())
	}
}

func TestRunRecordMarkdownWriteError(t *testing.T) {
	readPipe, writePipe, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := readPipe.Close(); err != nil {
		t.Fatal(err)
	}
	original := createMarkdown
	createMarkdown = func(string) (*os.File, error) { return writePipe, nil }
	t.Cleanup(func() {
		createMarkdown = original
		_ = writePipe.Close()
	})

	current := writeFile(t, "bench.txt", passingBenchOutput)
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"--current", current,
		"--write-markdown", filepath.Join(t.TempDir(), "baseline.md"),
	}, strings.NewReader(""), &stdout, &stderr)
	if code != 1 {
		t.Fatalf("code = %d; stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "writing markdown") {
		t.Fatalf("stderr = %s", stderr.String())
	}
}

func TestMainFunc(t *testing.T) {
	originalExit := osExit
	originalArgs := os.Args
	defer func() {
		osExit = originalExit
		os.Args = originalArgs
	}()
	var exitCode int
	osExit = func(code int) { exitCode = code }
	os.Args = []string{"benchgate", "--bogus"}
	main()
	if exitCode != 2 {
		t.Fatalf("exit code = %d, want 2", exitCode)
	}
}
