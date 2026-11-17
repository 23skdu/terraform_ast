# Benchmark Baseline

Committed reference for the `make bench-gate` CI check. Regenerate with
`make bench-baseline`; do not hand-edit the values.

- **Generated**: 2026-11-17T03:58:11Z
- **Threshold**: 10.0%

| Benchmark | Median ns/op |
| :--- | ---: |
| `BenchmarkAnalyzeHazards` | 500 |
| `BenchmarkCompare` | 1833 |
| `BenchmarkInspectModule` | 45982 |
| `BenchmarkInspectSource` | 359352 |
| `BenchmarkLookupHazard` | 532 |
| `BenchmarkRenderJSON` | 4259 |
| `BenchmarkRenderText` | 1790 |
| `BenchmarkScanDirectory` | 719155 |
