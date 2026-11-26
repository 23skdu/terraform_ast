package benchgate

import (
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }

func TestParse(t *testing.T) {
	input := strings.Join([]string{
		"goos: linux",
		"BenchmarkAlpha-8  10  100.0 ns/op  1 B/op  1 allocs/op",
		"BenchmarkAlpha-8  10  300.0 ns/op  1 B/op  1 allocs/op",
		"BenchmarkAlpha-8  10  200.0 ns/op  1 B/op  1 allocs/op",
		"BenchmarkBeta/even-8  10  50 ns/op",
		"BenchmarkBeta/even-8  10  150 ns/op",
		"BenchmarkGamma  10  75 ns/op",
		"not a benchmark",
	}, "\n")
	benchmarks, err := Parse(strings.NewReader(input))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(benchmarks) != 3 {
		t.Fatalf("got %d benchmarks, want 3: %+v", len(benchmarks), benchmarks)
	}
	if benchmarks[0].Name != "BenchmarkAlpha" || benchmarks[0].NSPerOp != 200 {
		t.Fatalf("alpha = %+v", benchmarks[0])
	}
	if benchmarks[1].Name != "BenchmarkBeta/even" || benchmarks[1].NSPerOp != 100 {
		t.Fatalf("beta = %+v", benchmarks[1])
	}
	if benchmarks[2].NSPerOp != 75 {
		t.Fatalf("gamma = %+v", benchmarks[2])
	}
}

func TestParseErrors(t *testing.T) {
	if _, err := Parse(strings.NewReader("goos: linux\n")); err == nil {
		t.Fatal("expected no-results error")
	}
	if _, err := Parse(errorReader{}); err == nil || !strings.Contains(err.Error(), "read failed") {
		t.Fatalf("expected read error, got %v", err)
	}
	huge := "BenchmarkHuge 1 " + strings.Repeat("9", 400) + " ns/op"
	if _, err := Parse(strings.NewReader(huge)); err == nil {
		t.Fatal("expected numeric overflow error")
	}
}

func TestNewBaselineAndCompare(t *testing.T) {
	baseline := NewBaseline([]Benchmark{
		{Name: "BenchmarkSlow", NSPerOp: 100},
		{Name: "BenchmarkSlower", NSPerOp: 100},
		{Name: "BenchmarkFast", NSPerOp: 10},
	}, 10, "2026-01-01T00:00:00Z")
	if baseline.GeneratedAt != "2026-01-01T00:00:00Z" || baseline.ThresholdPct != 10 {
		t.Fatalf("baseline metadata = %+v", baseline)
	}

	result, err := Compare(baseline, []Benchmark{
		{Name: "BenchmarkFast", NSPerOp: 10},
		{Name: "BenchmarkSlow", NSPerOp: 120},
		{Name: "BenchmarkSlower", NSPerOp: 200},
		{Name: "BenchmarkNew", NSPerOp: 1},
	}, 10)
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	if result.OK() {
		t.Fatal("expected regression and missing benchmark")
	}
	if len(result.Regressions) != 2 || result.Regressions[0].Name != "BenchmarkSlow" || result.Regressions[1].Name != "BenchmarkSlower" {
		t.Fatalf("regressions = %+v", result.Regressions)
	}
	if result.Regressions[0].ChangePct != 20 {
		t.Fatalf("change = %g", result.Regressions[0].ChangePct)
	}
	if len(result.Missing) != 0 {
		t.Fatalf("missing = %+v", result.Missing)
	}
	if len(result.Added) != 1 || result.Added[0] != "BenchmarkNew" {
		t.Fatalf("added = %+v", result.Added)
	}

	// A slowdown exactly at the threshold is allowed.
	result, err = Compare(baseline, []Benchmark{
		{Name: "BenchmarkSlow", NSPerOp: 110},
		{Name: "BenchmarkSlower", NSPerOp: 110},
		{Name: "BenchmarkFast", NSPerOp: 10},
	}, 10)
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	if !result.OK() {
		t.Fatalf("threshold boundary should pass: %+v", result)
	}
}

func TestCompareCalibrationNormalizesRunnerSpeed(t *testing.T) {
	baseline := NewBaseline([]Benchmark{
		{Name: CalibrationBenchmark, NSPerOp: 100},
		{Name: "BenchmarkWork", NSPerOp: 100},
	}, 10, "now")

	// A runner twice as slow reports 220 ns/op for a 100 ns/op baseline workload.
	result, err := Compare(baseline, []Benchmark{
		{Name: CalibrationBenchmark, NSPerOp: 200},
		{Name: "BenchmarkWork", NSPerOp: 220},
	}, 10)
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	if !result.OK() || result.Scale != 2 {
		t.Fatalf("normalised result = %+v", result)
	}

	result, err = Compare(baseline, []Benchmark{
		{Name: CalibrationBenchmark, NSPerOp: 200},
		{Name: "BenchmarkWork", NSPerOp: 250},
	}, 10)
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	if len(result.Regressions) != 1 || result.Regressions[0].ChangePct != 25 {
		t.Fatalf("regressions = %+v", result.Regressions)
	}

	// An old baseline without calibration keeps absolute comparison.
	legacy := NewBaseline([]Benchmark{{Name: "BenchmarkWork", NSPerOp: 100}}, 10, "then")
	result, err = Compare(legacy, []Benchmark{{Name: "BenchmarkWork", NSPerOp: 100}}, 10)
	if err != nil || result.Scale != 1 || !result.OK() {
		t.Fatalf("legacy result = %+v, err = %v", result, err)
	}
	// A run missing the calibration benchmark likewise falls back.
	result, err = Compare(baseline, []Benchmark{{Name: "BenchmarkWork", NSPerOp: 100}}, 10)
	if err != nil || result.Scale != 1 {
		t.Fatalf("missing calibration result = %+v, err = %v", result, err)
	}
}

func TestCompareCalibrationErrors(t *testing.T) {
	baseline := &Baseline{Benchmarks: map[string]float64{CalibrationBenchmark: 0, "BenchmarkWork": 100}}
	_, err := Compare(baseline, []Benchmark{{Name: CalibrationBenchmark, NSPerOp: 100}}, 10)
	if err == nil || !strings.Contains(err.Error(), "must be positive") {
		t.Fatalf("expected calibration error, got %v", err)
	}
}

func TestCompareErrors(t *testing.T) {
	valid := &Baseline{Benchmarks: map[string]float64{"BenchmarkA": 1}}
	if _, err := Compare(nil, nil, 10); err == nil {
		t.Fatal("expected nil baseline error")
	}
	if _, err := Compare(valid, nil, -1); err == nil {
		t.Fatal("expected negative threshold error")
	}
	if _, err := Compare(&Baseline{}, nil, 10); err == nil {
		t.Fatal("expected empty baseline error")
	}
	if _, err := Compare(&Baseline{Benchmarks: map[string]float64{"BenchmarkA": 0}}, []Benchmark{{Name: "BenchmarkA", NSPerOp: 1}}, 10); err == nil {
		t.Fatal("expected zero baseline error")
	}
	if _, err := Compare(&Baseline{Benchmarks: map[string]float64{"BenchmarkA": -1}}, []Benchmark{{Name: "BenchmarkA", NSPerOp: 1}}, 10); err == nil {
		t.Fatal("expected negative baseline error")
	}

	result, err := Compare(valid, []Benchmark{{Name: "BenchmarkB", NSPerOp: 1}}, 10)
	if err != nil {
		t.Fatalf("Compare: %v", err)
	}
	if len(result.Missing) != 1 || result.Missing[0] != "BenchmarkA" {
		t.Fatalf("missing = %+v", result.Missing)
	}
}

func TestLoadSave(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "baseline.json")
	baseline := NewBaseline([]Benchmark{{Name: "BenchmarkA", NSPerOp: 12.5}}, 10, "now")
	if err := Save(path, baseline); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Benchmarks["BenchmarkA"] != 12.5 || loaded.GeneratedAt != "now" {
		t.Fatalf("loaded = %+v", loaded)
	}

	if err := Save("", nil); err == nil {
		t.Fatal("expected nil baseline error")
	}
	nested := filepath.Join(dir, "nested", "baseline.json")
	if err := Save(nested, baseline); err != nil {
		t.Fatalf("Save nested: %v", err)
	}
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Save(filepath.Join(blocker, "baseline.json"), baseline); err == nil {
		t.Fatal("expected directory creation error")
	}
	if err := Save(dir, baseline); err == nil {
		t.Fatal("expected write error for directory target")
	}
	invalid := &Baseline{Benchmarks: map[string]float64{"BenchmarkNaN": math.Inf(1)}}
	if err := Save(filepath.Join(dir, "invalid.json"), invalid); err == nil {
		t.Fatal("expected encode error")
	}
	if _, err := Load(filepath.Join(dir, "missing.json")); err == nil {
		t.Fatal("expected missing file error")
	}
	badPath := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(badPath, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(badPath); err == nil {
		t.Fatal("expected decode error")
	}
	emptyPath := filepath.Join(dir, "empty.json")
	if err := os.WriteFile(emptyPath, []byte(`{"benchmarks_ns_per_op":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(emptyPath); err == nil {
		t.Fatal("expected empty baseline error")
	}
}

func TestWriteMarkdown(t *testing.T) {
	baseline := NewBaseline([]Benchmark{{Name: "BenchmarkA", NSPerOp: 12.5}}, 10, "2026-01-01T00:00:00Z")
	var b strings.Builder
	if err := WriteMarkdown(&b, baseline); err != nil {
		t.Fatalf("WriteMarkdown: %v", err)
	}
	out := b.String()
	if !strings.Contains(out, "# Benchmark Baseline") || !strings.Contains(out, "`BenchmarkA`") || !strings.Contains(out, "10.0%") {
		t.Fatalf("markdown = %s", out)
	}
	if err := WriteMarkdown(io.Discard, NewBaseline(nil, 0, "")); err != nil {
		t.Fatalf("empty markdown: %v", err)
	}
	if err := WriteMarkdown(io.Discard, nil); err == nil {
		t.Fatal("expected nil baseline error")
	}
	if err := WriteMarkdown(failingWriter{}, baseline); err == nil {
		t.Fatal("expected write error")
	}
}

func TestWriteReport(t *testing.T) {
	var b strings.Builder
	if err := WriteReport(&b, Result{Added: []string{"BenchmarkNew"}}, 10); err != nil {
		t.Fatalf("WriteReport: %v", err)
	}
	if !strings.Contains(b.String(), "No benchmark regressions") || !strings.Contains(b.String(), "BenchmarkNew") {
		t.Fatalf("report = %s", b.String())
	}

	b.Reset()
	result := Result{
		Regressions: []Regression{{Name: "BenchmarkSlow", Baseline: 100, Current: 130, ChangePct: 30}},
		Missing:     []string{"BenchmarkGone"},
		Scale:       1.25,
	}
	if err := WriteReport(&b, result, 10); err != nil {
		t.Fatalf("WriteReport: %v", err)
	}
	if !strings.Contains(b.String(), "BenchmarkSlow") || !strings.Contains(b.String(), "BenchmarkGone") || !strings.Contains(b.String(), "1.250x") {
		t.Fatalf("report = %s", b.String())
	}
	if err := WriteReport(failingWriter{}, result, 10); err == nil {
		t.Fatal("expected write error")
	}
}
