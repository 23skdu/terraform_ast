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
| **R1** | P3 | Add Crossplane composition and claim analysis | No composite-module analysis or claim-level diffing exists. Done when compositions and claims can be inspected and compared. |
| **R2** | P3 | Add resource transforms for renaming and splitting | Codemod supports input removal and moved-block synthesis only. Done when resource rename/split transforms are previewable and applicable. |
| **R3** | P3 | Add webhook notifications for hazardous changes | There is no notification transport. Done when hazardous diffs can trigger configurable webhooks. |
| **R4** | P3 | Add advisory locks for shared analysis runs | No locking exists for shared or remote analysis. Done when concurrent runs can coordinate through advisory locks. |

## 2. Summary of remaining work

Completed and removed from this roadmap: the CI benchmark regression gate and committed
baseline, the streaming diff iterator/channel API, blast-radius `--limit` pagination, the
streaming SARIF writers, recorded state-vs-live drift detection, topological module
execution plans, hook phase support, import-block codemod synthesis, sparse checkout for
module subdirectories, bounded TTL LRU schema caching, and benchmark/fuzz matrices.

The remaining effort falls into one group:

1. **Strategic gaps (P3):** Crossplane (R1), resource transforms (R2), webhooks (R3),
   and advisory locking (R4).

**Recommended next pass:** R1 as the highest-impact remaining product work. Defer R2–R4
until it lands.


*Revised after removing completed, fully wired Pass 10 items and re-prioritizing what
remains.*
