// Package benchgate parses Go benchmark output, stores committed performance baselines,
// and detects regressions beyond a configured threshold.
package benchgate

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// DefaultThresholdPct is the maximum allowed slowdown before a benchmark is considered a
// regression.
const DefaultThresholdPct = 10.0

// CalibrationBenchmark is a deterministic CPU loop included in every benchmark run. Dividing
// current timings by the calibration ratio makes the committed baseline portable across CI
// runners whose CPU is faster or slower than the machine that recorded it.
const CalibrationBenchmark = "BenchmarkGateCalibration"

// Baseline is the committed performance reference for a set of benchmarks.
type Baseline struct {
	GeneratedAt  string             `json:"generated_at"`
	ThresholdPct float64            `json:"threshold_pct"`
	Benchmarks   map[string]float64 `json:"benchmarks_ns_per_op"`
}

// Benchmark is one parsed benchmark result, reported as a median across runs.
type Benchmark struct {
	Name    string
	NSPerOp float64
}

// Regression describes a benchmark that slowed down beyond the threshold.
type Regression struct {
	Name      string
	Baseline  float64
	Current   float64
	ChangePct float64
}

// Result is the outcome of comparing current benchmarks with a baseline.
type Result struct {
	Regressions []Regression
	Missing     []string
	Added       []string
	Scale       float64 // current runner speed relative to the baseline runner (1 = identical)
}

// OK reports whether the comparison passed: no regressions and no baseline benchmarks vanished.
func (r Result) OK() bool {
	return len(r.Regressions) == 0 && len(r.Missing) == 0
}

// benchmarkLine matches a `go test -bench` result line. The optional `-N` suffix is the
// GOMAXPROCS suffix Go appends, which differs between machines and must not be part of the key.
var benchmarkLine = regexp.MustCompile(`^(Benchmark\S+?)(?:-\d+)?\s+\d+\s+([0-9]+(?:\.[0-9]+)?)\s+ns/op`)

// Parse reads benchmark output and returns the median ns/op for each benchmark name.
func Parse(r io.Reader) ([]Benchmark, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	values := make(map[string][]float64)
	for scanner.Scan() {
		match := benchmarkLine.FindStringSubmatch(strings.TrimSpace(scanner.Text()))
		if match == nil {
			continue
		}
		value, err := strconv.ParseFloat(match[2], 64)
		if err != nil {
			return nil, fmt.Errorf("parse benchmark %s: %w", match[1], err)
		}
		values[match[1]] = append(values[match[1]], value)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read benchmark output: %w", err)
	}
	if len(values) == 0 {
		return nil, fmt.Errorf("no benchmark results found in input")
	}

	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)

	benchmarks := make([]Benchmark, 0, len(names))
	for _, name := range names {
		benchmarks = append(benchmarks, Benchmark{Name: name, NSPerOp: median(values[name])})
	}
	return benchmarks, nil
}

func median(values []float64) float64 {
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	n := len(sorted)
	if n%2 == 1 {
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2
}

// NewBaseline builds a baseline from parsed benchmarks. The caller supplies the timestamp so
// that recording is deterministic and testable.
func NewBaseline(benchmarks []Benchmark, thresholdPct float64, generatedAt string) *Baseline {
	entries := make(map[string]float64, len(benchmarks))
	for _, benchmark := range benchmarks {
		entries[benchmark.Name] = benchmark.NSPerOp
	}
	return &Baseline{
		GeneratedAt:  generatedAt,
		ThresholdPct: thresholdPct,
		Benchmarks:   entries,
	}
}

// Compare checks current benchmarks against the baseline. It returns an error for an invalid
// baseline or threshold so a gate cannot silently pass on malformed data.
func Compare(baseline *Baseline, current []Benchmark, thresholdPct float64) (Result, error) {
	if baseline == nil {
		return Result{}, fmt.Errorf("baseline is required")
	}
	if thresholdPct < 0 {
		return Result{}, fmt.Errorf("threshold must be non-negative, got %g", thresholdPct)
	}
	if len(baseline.Benchmarks) == 0 {
		return Result{}, fmt.Errorf("baseline contains no benchmarks")
	}

	scale, err := calibrationScale(baseline, current)
	if err != nil {
		return Result{}, err
	}
	result := Result{Scale: scale}
	seen := make(map[string]struct{}, len(current))
	for _, benchmark := range current {
		seen[benchmark.Name] = struct{}{}
		base, ok := baseline.Benchmarks[benchmark.Name]
		if !ok {
			result.Added = append(result.Added, benchmark.Name)
			continue
		}
		if base <= 0 {
			return Result{}, fmt.Errorf("baseline for %s must be positive, got %g", benchmark.Name, base)
		}
		normalized := benchmark.NSPerOp / scale
		change := (normalized - base) / base * 100
		if change > thresholdPct {
			result.Regressions = append(result.Regressions, Regression{
				Name:      benchmark.Name,
				Baseline:  base,
				Current:   normalized,
				ChangePct: change,
			})
		}
	}
	for name := range baseline.Benchmarks {
		if _, ok := seen[name]; !ok {
			result.Missing = append(result.Missing, name)
		}
	}
	sort.Slice(result.Regressions, func(i, j int) bool {
		return result.Regressions[i].Name < result.Regressions[j].Name
	})
	sort.Strings(result.Missing)
	sort.Strings(result.Added)
	return result, nil
}

// calibrationScale returns how many times slower the current runner is than the baseline runner.
// A missing calibration benchmark disables normalization for legacy baselines.
func calibrationScale(baseline *Baseline, current []Benchmark) (float64, error) {
	baseCal, baseOK := baseline.Benchmarks[CalibrationBenchmark]
	if !baseOK {
		return 1, nil
	}
	var currentCal float64
	currentOK := false
	for _, benchmark := range current {
		if benchmark.Name == CalibrationBenchmark {
			currentCal = benchmark.NSPerOp
			currentOK = true
			break
		}
	}
	if !currentOK {
		return 1, nil
	}
	if baseCal <= 0 || currentCal <= 0 {
		return 0, fmt.Errorf("%s must be positive (baseline %g, current %g)", CalibrationBenchmark, baseCal, currentCal)
	}
	return currentCal / baseCal, nil
}

// Load reads a committed baseline from disk.
func Load(path string) (*Baseline, error) {
	// #nosec G304 -- baseline path is operator-supplied by design.
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read baseline %s: %w", path, err)
	}
	var baseline Baseline
	if err := json.Unmarshal(data, &baseline); err != nil {
		return nil, fmt.Errorf("decode baseline %s: %w", path, err)
	}
	if len(baseline.Benchmarks) == 0 {
		return nil, fmt.Errorf("baseline %s contains no benchmarks", path)
	}
	return &baseline, nil
}

// Save writes a baseline atomically enough for a developer workflow: the parent directory must
// already exist, and a partial write cannot be mistaken for a valid baseline.
func Save(path string, baseline *Baseline) error {
	if baseline == nil {
		return fmt.Errorf("baseline is required")
	}
	data, err := json.MarshalIndent(baseline, "", "  ")
	if err != nil {
		return fmt.Errorf("encode baseline: %w", err)
	}
	data = append(data, '\n')
	dir := filepath.Dir(path)
	if dir != "." {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return fmt.Errorf("create baseline directory %s: %w", dir, err)
		}
	}
	// #nosec G306 -- baseline is checked into the repository and is intentionally readable.
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write baseline %s: %w", path, err)
	}
	return nil
}

// WriteMarkdown writes a human-readable baseline document.
func WriteMarkdown(w io.Writer, baseline *Baseline) error {
	if baseline == nil {
		return fmt.Errorf("baseline is required")
	}
	names := make([]string, 0, len(baseline.Benchmarks))
	for name := range baseline.Benchmarks {
		names = append(names, name)
	}
	sort.Strings(names)

	var b strings.Builder
	b.WriteString("# Benchmark Baseline\n\n")
	b.WriteString("Committed reference for the `make bench-gate` CI check. Regenerate with\n")
	b.WriteString("`make bench-baseline`; do not hand-edit the values.\n\n")
	if baseline.GeneratedAt != "" {
		fmt.Fprintf(&b, "- **Generated**: %s\n", baseline.GeneratedAt)
	}
	fmt.Fprintf(&b, "- **Threshold**: %.1f%%\n\n", baseline.ThresholdPct)
	b.WriteString("| Benchmark | Median ns/op |\n")
	b.WriteString("| :--- | ---: |\n")
	for _, name := range names {
		fmt.Fprintf(&b, "| `%s` | %.0f |\n", name, baseline.Benchmarks[name])
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// WriteReport prints the comparison outcome in a CI-friendly form.
func WriteReport(w io.Writer, result Result, thresholdPct float64) error {
	var b strings.Builder
	if result.Scale > 0 {
		fmt.Fprintf(&b, "Runner scale vs baseline: %.3fx (timings normalized)\n", result.Scale)
	}
	if len(result.Regressions) == 0 {
		fmt.Fprintf(&b, "No benchmark regressions beyond %.1f%%.\n", thresholdPct)
	} else {
		fmt.Fprintf(&b, "Benchmark regressions beyond %.1f%%:\n", thresholdPct)
		for _, regression := range result.Regressions {
			fmt.Fprintf(&b, "  %-40s %10.0f -> %10.0f ns/op (%+.1f%%)\n",
				regression.Name, regression.Baseline, regression.Current, regression.ChangePct)
		}
	}
	if len(result.Missing) > 0 {
		fmt.Fprintf(&b, "Baseline benchmarks missing from current run:\n")
		for _, name := range result.Missing {
			fmt.Fprintf(&b, "  %s\n", name)
		}
	}
	if len(result.Added) > 0 {
		fmt.Fprintf(&b, "New benchmarks (not yet in baseline):\n")
		for _, name := range result.Added {
			fmt.Fprintf(&b, "  %s\n", name)
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}
