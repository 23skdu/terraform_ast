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
| **R6** | P2 | Registry provider-schema retrieval | The in-memory and on-disk caches are now bounded by TTL/count, but the registry client still does not fetch `schema` blocks. Done when provider schemas are fetched from the registry, scoped to the `schema` block, and served by the bounded expiring cache. |
| **R7** | P2 | Sparse checkout for `//subdir` modules | `gitops` exposes sparse-path configuration, but remote subdirectories are still extracted from a full shallow clone. Done when a remote module addressing `//subdir` avoids materializing unrelated repository paths. |
| **R9** | P3 | Crossplane claim-level analysis | Compositions can be inspected and compared, but there is no `Claim` type, loader, diff, or CLI path. Done when compositions **and claims** can be inspected and compared, including claim fields and deterministic drift output. |
| **R10** | P3 | Split resource transforms | Rename transforms are previewable/applicable. One-to-many `moved{}` specs are rejected because a single `from` cannot legally split state. Done when split transforms emit valid, applicable Terraform (for example derived `import{}`/`removed{}` synthesis) and are validated end-to-end. |
| **R12** | P3 | Real advisory OS locks with stale recovery | `--lock-file` now creates an exclusive lockfile, but it is not an `flock`/`fcntl` advisory lock and a crash leaves a lock that blocks all later runs. Done when concurrent runs coordinate via an OS advisory lock with owner metadata, stale-lock detection, and subcommand support. |
| **R4** | P3 | Hooks for subcommands | Main audit/compare paths now run explicit `pre-analysis`/`post-analysis` phases and preserve the analysis verdict, but `drift`, `crossplane`, and encryption subcommands dispatch before hook flags are parsed. Done when all analysis subcommands accept and run both hook phases. |
| **R8** | P3 | Performance gate and broader fuzz seeding | Small/medium/large benchmarks and real-fixture fuzz seeds now exist, but the CI benchmark comparison is advisory (`continue-on-error: true`). Done when the baseline gate blocks regressions and every untrusted parser is seeded from real modules. |

---

## 2. Work completed in the bounded pass

The following gaps were closed and verified with 100% statement coverage:

- **R1:** `diff.CompareStreamContext` adds cancellation and deterministic early stop; the streaming
  iterator is benchmarked.
- **R2:** `drift --state --live` rejects empty/null/duplicate-address snapshots, error messages use
  real newlines, and the legacy golden mode announces that it self-compares.
- **R3:** topological order is documented and covered for cycles, duplicate edges, unknown endpoints,
  and empty graphs.
- **R4:** pre- and post-analysis hooks use explicit phases on the audit/compare paths; a failing
  post-hook no longer masks a non-zero analysis verdict. Subcommand coverage remains open.
- **R5:** import blocks are normalized, de-duplicated, and only reported as changed when the file
  actually changes.
- **R8:** size-matrix benchmarks for inspect/compare/report and fuzz seeds from real fixtures were
  added; stale benchmark names in the docs were corrected.
- **R10:** transform specs are validated; unsupported split specs and ignored `--transform` usage
  now fail fast with actionable errors.
- **R11:** webhook delivery is centralized and wired into audit, fix, fix-preview, and upgrade-path
  runs, with payload assertions and a configurable `--webhook-timeout`.

The authoritative flag reference is `docs/cli_usage.md`; stale feature claims in `README.md` were
corrected to match the implemented CLI.
