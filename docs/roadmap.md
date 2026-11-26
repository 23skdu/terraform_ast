# Strategic Product Roadmap — Pass 11

**Scope:** Remaining memory/performance hardening and feature-gap closure for
`terraform_ast` (`tfastdiff`).

**Status:** Active. A gap analysis found that the earlier "all items implemented" claim was
inaccurate. Bounded fixes and both large Pass 11 gaps (R1 incremental streaming and R6 remote
schema retrieval) are now implemented and verified. The remaining items are the CI wiring and
proof-surface extensions for the formal Lean model, tracked in sections 1 and 3.

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
| **L3** | P2 | Run the Lean gate in CI | `make lean-verify` type-checks the proof locally and the toolchain is pinned in `lean/lean-toolchain`, but CI does not yet install Lean. Done when the CI workflow runs `make lean-verify` as a required check. |
| **L4** | P3 | Extend the formal proof surface | The decision procedure is proved; pin fallback for empty version strings, address-shift suppression, and wrapper-mode count equivalence are still covered only by Go tests. Done when those invariants have Lean theorems and `docs/formalproof.md` records them. |

The two large Pass 11 gaps (R1 and R6) are now closed; their implementation notes are
in section 2. The public registry still does not publish provider schemas, so R6 is
closed against an operator-supplied endpoint rather than a registry API that does not
exist.

---

## 2. Work completed in the bounded pass

The following gaps were closed and verified with 100% statement coverage:

- **R1:** `diff.CompareStreamContext` now emits each finding as it is produced through a shared
  `diffSink`; no item slices are accumulated. The terminal `Done` event carries only counters,
  verdict, safe pin, and address shifts, and cancellation stops the next send without leaking a
  producer goroutine. The streaming iterator is benchmarked.
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
- **R6:** `registry.Client.FetchProviderSchema` retrieves a `terraform providers schema -json`
  document from an operator-supplied HTTP(S) endpoint through the existing SSRF guard, retry, and
  cancellation logic, bounded to 32 MiB. `recreation.LoadRemoteProviderSchema` rejects documents
  without a `provider_schemas` block and stores them in the bounded, expiring on-disk schema cache.
  The CLI exposes this via `--provider-schema-url`; `--provider-schema` with a Terraform-generated
  file remains authoritative.

---

## 3. Formal verification steps (Lean)

The safety verdict is now also covered by a machine-checked model. The steps below are
tracked here so the proof cannot silently drift from the Go implementation.

1. **L1 — Model the decision procedure (done).** All Lean files live under `lean/`.
   `lean/DiffVerdict.lean` mirrors `computeVerdict`, `CanSafelyUpdate`, and the pin source
   in `recommendSafePin`; the toolchain is pinned in `lean/lean-toolchain` (Lean 4.36.0).
2. **L2 — Prove the invariants (done).** Six theorems are checked with core Lean only:
   the exact safety condition, the four-way verdict partition, agreement between
   verdict and exit-code safety, downgrade pin selection, and monotonicity of breaking
   findings. See `docs/formalproof.md` for statements and scope.
3. **L3 — Verify (done locally, CI open).** `make lean-verify` type-checks the proof and
   fails on any unsolved goal. It is intentionally opt-in so `make verify` stays usable
   without Lean; the remaining work is to install Lean in CI and require the target.
4. **L4 — Extend the surface (open).** Add theorems for empty-version pin fallbacks,
   address-shift suppression, and wrapper-mode count equivalence, then update
   `docs/formalproof.md` in the same commit.

The authoritative flag reference is `docs/cli_usage.md`; stale feature claims in `README.md` were
corrected to match the implemented CLI.
