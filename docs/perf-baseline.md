# Benchmark Baseline

Committed reference for the `make bench-gate` CI check. Regenerate with
`make bench-baseline`; do not hand-edit the values.

- **Generated**: 2026-11-17T18:02:28Z
- **Threshold**: 10.0%

| Benchmark | Median ns/op |
| :--- | ---: |
| `BenchmarkAnalyzeHazards` | 498 |
| `BenchmarkCompare` | 1829 |
| `BenchmarkCompareLarge` | 105327 |
| `BenchmarkInspectModule` | 46540 |
| `BenchmarkInspectModuleLarge` | 305201 |
| `BenchmarkInspectModuleMedium` | 77802 |
| `BenchmarkInspectModuleSmall` | 10339 |
| `BenchmarkInspectSource` | 471838 |
| `BenchmarkLookupHazard` | 546 |
| `BenchmarkRenderJSON` | 4350 |
| `BenchmarkRenderText` | 1777 |
| `BenchmarkScanDirectory` | 713370 |
