# Strategic Product Roadmap — Pass 11

**Scope:** Remaining memory/performance hardening and feature-gap closure for
`terraform_ast` (`tfastdiff`).

**Status:** Active. All Pass 10 items that are implemented and wired end-to-end have been
removed from this document. Only outstanding work is tracked below and re-prioritized.

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

## 1. Remaining work (re-prioritized)

| ID | Pri | Remaining item | Why it remains / acceptance criteria |
| :--- | :---: | :--- | :--- |
| **R1** | P1 | Add a streaming/iterator diff API for large modules | `diff.Compare` still materializes a full in-memory `DiffReport`; report writers stream, but diff accumulation still scales with module size. Keep the slice API for small modules and add an iterator/channel path for large ones. |
| **R2** | P1 | Detect drift between Terraform state and live infrastructure | The `drift` command currently compares diff output against golden baselines only; it does not compare state to live infrastructure. Done when the command can evaluate state against live/provider data (or a recorded equivalent) and report resource drift. |
| **R3** | P2 | Emit a topological module execution plan | `--module-graph` renders Graphviz DOT from `pkg/modulegraph`, but there is no dependency-ordered execution plan. Done when cycle detection and a deterministic topological order are emitted. |
| **R4** | P2 | Support hook phases and post-analysis hooks | `--hook` runs commands only before analysis via `pkg/hooks`. Done when hooks can run before/after named phases rather than only as pre-analysis commands. |
| **R5** | P2 | Wire import-block normalization into the codemod path | `import {}` blocks are parsed and diffed, but codemod synthesis covers only `moved {}` blocks. Done when import blocks can be normalized/emitted consistently with moved blocks. |
| **R6** | P2 | Fetch and cache only provider schema data with a bounded TTL LRU | The registry client does not fetch provider schemas, and the on-disk schema cache has no size bound or TTL. Done when schema retrieval is scoped to the `schema` block and cached by a bounded, expiring LRU. |
| **R7** | P2 | Use sparse checkout for module subdirectories | Shallow clones are implemented and wired through the repository cache; sparse checkout is not. Done when remote modules that address a `//subdir` avoid materializing unrelated repository paths. |
| **R8** | P3 | Add small/medium/large benchmark matrices and a real-world fuzz corpus | Benchmarks use a single small fixture each, and only the LSP fuzz target has a seed corpus. Done when hot paths are benchmarked across representative sizes and fuzz targets are seeded from real modules. |
| **R9** | P3 | Add Crossplane composition and claim analysis | No composite-module analysis or claim-level diffing exists. Done when compositions and claims can be inspected and compared. |
| **R10** | P3 | Add resource transforms for renaming and splitting | Codemod supports input removal and moved-block synthesis only. Done when resource rename/split transforms are previewable and applicable. |
| **R11** | P3 | Add webhook notifications for hazardous changes | There is no notification transport. Done when hazardous diffs can trigger configurable webhooks. |
| **R12** | P3 | Add advisory locks for shared analysis runs | No locking exists for shared or remote analysis. Done when concurrent runs can coordinate through advisory locks. |

## 2. Summary of remaining work

Completed and removed from this roadmap: the CI benchmark regression gate and committed
baseline (previous R1/R2), blast-radius `--limit` pagination (previous R4), and the
streaming SARIF writers (previous R5).

The remaining effort falls into three groups:

1. **Large-repository memory scalability (P1):** streaming/paginating diff results (R1)
   and true state-to-live drift detection (R2).
2. **Feature parity and ergonomics (P2):** topological execution plans (R3), richer
   hook phases (R4), import-block codemods (R5), schema cache bounds (R6), and sparse
   checkout (R7).
3. **Coverage and strategic gaps (P3):** benchmark/fuzz matrices (R8), Crossplane
   (R9), resource transforms (R10), webhooks (R11), and advisory locking (R12).

**Recommended next pass:** R1 + R2 as the highest-impact product work. Defer R3–R12
until the P1 items land.


*Revised after removing completed, fully wired Pass 10 items and re-prioritizing what
remains.*
