# Benchmark Baseline

Committed reference for the `make bench-gate` CI check. Regenerate with
`make bench-baseline`; do not hand-edit the values.

- **Generated**: 2026-11-17T10:10:51Z
- **Threshold**: 10.0%

| Benchmark | Median ns/op |
| :--- | ---: |
| `BenchmarkAnalyzeHazards` | 498 |
| `BenchmarkCompare` | 1818 |
| `BenchmarkCompareLarge` | 103473 |
| `BenchmarkInspectModule` | 46041 |
| `BenchmarkInspectModuleLarge` | 305004 |
| `BenchmarkInspectModuleMedium` | 77222 |
| `BenchmarkInspectModuleSmall` | 10145 |
| `BenchmarkInspectSource` | 459999 |
| `BenchmarkLookupHazard` | 535 |
| `BenchmarkRenderJSON` | 4224 |
| `BenchmarkRenderText` | 1762 |
| `BenchmarkScanDirectory` | 711758 |
