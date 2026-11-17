// Command benchgate compares Go benchmark output against a committed baseline and fails when a
// benchmark regresses beyond the configured threshold.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/23skdu/terraform_ast/internal/benchgate"
)

var osExit = os.Exit

// createMarkdown is a package variable so the write-error path can be exercised without relying
// on filesystem quirks.
var createMarkdown = func(path string) (*os.File, error) { return os.Create(path) } // #nosec G304 -- markdown path is operator-supplied

func main() {
	osExit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("benchgate", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var (
		currentPath   string
		baselinePath  string
		writeBaseline string
		writeMarkdown string
		threshold     float64
	)
	fs.StringVar(&currentPath, "current", "", "Path to go test benchmark output, or - for stdin")
	fs.StringVar(&baselinePath, "baseline", "", "Path to committed baseline JSON")
	fs.StringVar(&writeBaseline, "write-baseline", "", "Write a new baseline JSON to this path")
	fs.StringVar(&writeMarkdown, "write-markdown", "", "Write a human-readable baseline to this path")
	fs.Float64Var(&threshold, "threshold", benchgate.DefaultThresholdPct, "Maximum allowed regression percentage")

	if err := fs.Parse(args); err != nil {
		return 2
	}
	if currentPath == "" {
		_, _ = fmt.Fprintln(stderr, "Error: --current is required")
		return 2
	}
	if threshold < 0 {
		_, _ = fmt.Fprintf(stderr, "Error: --threshold must be non-negative, got %g\n", threshold)
		return 2
	}

	input, closeInput, err := openInput(currentPath, stdin)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "Error: %v\n", err)
		return 2
	}
	defer closeInput()

	benchmarks, err := benchgate.Parse(input)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "Error parsing benchmark output: %v\n", err)
		return 2
	}

	if writeBaseline != "" || writeMarkdown != "" {
		return record(benchmarks, threshold, writeBaseline, writeMarkdown, stdout, stderr)
	}
	if baselinePath == "" {
		_, _ = fmt.Fprintln(stderr, "Error: --baseline is required unless --write-baseline or --write-markdown is set")
		return 2
	}

	baseline, err := benchgate.Load(baselinePath)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "Error: %v\n", err)
		return 2
	}
	result, err := benchgate.Compare(baseline, benchmarks, threshold)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "Error: %v\n", err)
		return 2
	}
	if err := benchgate.WriteReport(stdout, result, threshold); err != nil {
		_, _ = fmt.Fprintf(stderr, "Error writing report: %v\n", err)
		return 1
	}
	if !result.OK() {
		return 1
	}
	return 0
}

func openInput(path string, stdin io.Reader) (io.Reader, func(), error) {
	if path == "-" {
		return stdin, func() {}, nil
	}
	// #nosec G304 -- benchmark output path is supplied by the operator.
	file, err := os.Open(path)
	if err != nil {
		return nil, func() {}, fmt.Errorf("open benchmark output %s: %w", path, err)
	}
	return file, func() { _ = file.Close() }, nil
}

func record(benchmarks []benchgate.Benchmark, threshold float64, writeBaseline, writeMarkdown string, stdout, stderr io.Writer) int {
	baseline := benchgate.NewBaseline(benchmarks, threshold, time.Now().UTC().Format(time.RFC3339))
	if writeBaseline != "" {
		if err := benchgate.Save(writeBaseline, baseline); err != nil {
			_, _ = fmt.Fprintf(stderr, "Error: %v\n", err)
			return 1
		}
		_, _ = fmt.Fprintf(stdout, "Wrote baseline with %d benchmarks to %s\n", len(baseline.Benchmarks), writeBaseline)
	}
	if writeMarkdown != "" {
		// #nosec G304 -- markdown path is supplied by the operator.
		file, err := createMarkdown(writeMarkdown)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "Error open markdown %s: %v\n", writeMarkdown, err)
			return 1
		}
		if err := benchgate.WriteMarkdown(file, baseline); err != nil {
			_ = file.Close()
			_, _ = fmt.Fprintf(stderr, "Error writing markdown: %v\n", err)
			return 1
		}
		_ = file.Close()
		_, _ = fmt.Fprintf(stdout, "Wrote markdown baseline to %s\n", writeMarkdown)
	}
	return 0
}
