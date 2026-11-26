# Strategic Product Roadmap — Pass 11

**Scope:** Remaining memory/performance hardening and feature-gap closure for
`terraform_ast` (`tfastdiff`).

**Status:** Active. A gap analysis against the original Pass 11 acceptance criteria found that the
previous "all items implemented" claim was inaccurate. Bounded fixes (validation, wiring, tests,
and documentation) have landed for every item; the large, genuinely unimplemented features are
re-opened below with their acceptance criteria.

---

## 0. Prioritization model

| Priority | Meaning |
| :--- | :--- |
| **P0** | Blocks a quality claim or CI gate; do first. |
| **P1** | High-impact memory or correctness gap on large repositories. |
| **P2** | Material feature gap with a clear implementation path. |
| **P3** | Strategic or lower-urgency work; revisit after P0–P2. |

The existing quality baseline (formatting, `go vet`, `golangci-lint`, 100% statement
coverage, race detection, and fuzz smoke) is enforced by `scripts/verify.sh` and CI.
This roadmap does not re-track it.

---

## 1. Remaining work (re-opened after gap analysis)

| ID | Pri | Remaining item | Why it remains / acceptance criteria |
| :--- | :---: | :--- | :--- |
| **R1** | P1 | Genuinely incremental diff accumulation | `diff.CompareStreamContext` now streams items with cancellation, but the full `DiffReport` is still built before emission. Done when large modules are diffed incrementally with bounded memory and no whole-report accumulation. |
| **R6** | P2 | Registry provider-schema retrieval | The in-memory and on-disk caches are now bounded by TTL/count, but the registry client still does not fetch provider schemas. The public registry publishes provider versions and binaries, not `terraform providers schema -json` output (`/v1/providers/.../schema` returns 404), so the original fetch endpoint does not exist. Done when schemas are obtained from an endpoint or plugin handshake that actually publishes them, scoped to the `provider_schemas` block, and served by the bounded expiring cache. Until then, `--provider-schema` with a Terraform-generated JSON file remains authoritative. |

---

## 2. Work completed in the bounded pass

The following gaps were closed and verified with 100% statement coverage:

- **R1:** `diff.CompareStreamContext` adds cancellation and deterministic early stop; the streaming
  iterator is benchmarked.
- **R2:** `drift --state --live` rejects empty/null/duplicate-address snapshots, error messages use
  real newlines, and the legacy golden mode announces that it self-compares.
- **R3:** topological order is documented and covered for cycles, duplicate edges, unknown endpoints,
  and empty graphs.
- **R4:** pre- and post-analysis hooks use explicit phases; a failing post-hook no longer masks a
  non-zero analysis verdict. Hooks were subsequently extended to the `drift`, `crossplane`,
  `encrypt`, `decrypt`, and `list-versions` subcommands.
- **R5:** import blocks are normalized, de-duplicated, and only reported as changed when the file
  actually changes.
- **R8:** size-matrix benchmarks for inspect/compare/report and fuzz seeds from real fixtures were
  added; stale benchmark names in the docs were corrected.
- **R10:** transform specs are validated; unsupported split specs and ignored `--transform` usage
  now fail fast with actionable errors.
- **R11:** webhook delivery is centralized and wired into audit, fix, fix-preview, and upgrade-path
  runs, with payload assertions and a configurable `--webhook-timeout`.

- **R9:** Crossplane claims can be loaded and compared (`tfastdiff claim`), including composite
  type, composition selection, resource refs, connection secret wiring, and parameter drift with
  deterministic JSON output.
- **R10:** split transforms are now applicable: a spec with `from`, multiple `to` addresses, and a
  matching `ids` list emits `split.tf` with a non-destroying `removed` block and one `import` block
  per target. Specs without IDs are rejected with an actionable error.
- **R12:** `pkg/lock` now uses a kernel advisory `flock` (auto-released on process exit, so no stale
  locks), writes owner metadata readable with `lock.ReadInfo`, and supports a retry timeout via
  `--lock-timeout`. Locking is applied to the analysis subcommands as well.
- **R7:** a remote module source addressing `//subdir` now gets a sparse shallow clone, so unrelated
  repository directories are never materialised; registry archives are likewise filtered to the
  requested submodule before extraction. `--upgrade-path` intermediate registry versions honour
  `//subdir` too. Local directories keep their `//` paths untouched.
- **R8:** the benchmark gate is now blocking in CI and normalises each run against
  `BenchmarkGateCalibration`, so a faster or slower runner does not create a false regression. The
  committed baseline was regenerated with the calibration benchmark included.

The authoritative flag reference is `docs/cli_usage.md`; stale feature claims in `README.md` were
corrected to match the implemented CLI.
