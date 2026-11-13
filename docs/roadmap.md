# Strategic Product Roadmap — Pass 10

**Scope:** Memory profiling, performance testing, and feature-gap analysis derived from
reviewing Terraform state/module management systems (Terragrunt, OpenTofu, Atlantis,
Spacelift, Terraform Cloud/Enterprise, Pulumi, Crossplane) plus the existing
`terraform_ast` hazard catalog.

**Status:** In progress. Pass 10 expands the recreation-hazard taxonomy, adds
profiling/performance Makefile targets and benchmarks, and records the next
implementation priorities.

---

## 0. Method

Pass 10 was produced by:

1. Reviewing the existing hazard catalog (`pkg/recreation`) and its tests.
2. Running `go test -bench` and `go test -memprofile` against the hot packages
   (`pkg/ast`, `pkg/diff`, `pkg/recreation`, `pkg/report`, `pkg/blastradius`).
3. Comparing capabilities against Terragrunt (dependency ordering, hooks, retry),
   OpenTofu (state encryption, moved blocks, import), Atlantis (plan/apply
   workflows, policy checks), Spacelift (stack dependencies, drift detection),
   Terraform Cloud (remote state, run triggers, sentinel), Pulumi (preview/diff,
   resource transforms), and Crossplane (composition, claims).

Baseline verified before writing this document:

| Check | Result |
| :--- | :--- |
| `go build ./...` | clean |
| `go vet ./...` | clean |
| `golangci-lint run ./...` | 0 issues |
| `go test ./...` | all packages ok |
| `go test -covermode=atomic ./...` | 100.0% total |

---

## 1. Memory profiling findings

| Area | Observation | Recommendation |
| :--- | :--- | :--- |
| `pkg/ast` parsing | Re-parsing the same modules on every invocation dominates allocations; `ir.go` caches are process-local only. | Introduce a content-addressed, on-disk IR cache keyed by module hash; expose `--cache-dir`. |
| `pkg/diff` | `DiffReport` accumulates full `DifferenceItem` slices for large modules; peak RSS grows with module size. | Stream diff results through an iterator/channel API for large modules; keep the slice API for small ones. |
| `pkg/registry` | Schema fetches allocate full provider schemas even when only `ForceNew` attributes are needed. | Fetch and cache only the `schema` block; add a bounded LRU with TTL. |
| `pkg/gitops` | Clone operations materialize entire repositories in memory. | Use shallow clones (`--depth=1`) and sparse checkout for module subdirectories. |
| `pkg/report` | SARIF/JSON renderers build the entire document in memory. | Add streaming writers for SARIF and Markdown outputs. |
| `pkg/blastradius` | Scanner holds all call sites in memory. | Paginate/stream call-site results; add `--limit`. |

## 2. Performance testing recommendations

1. Add `Benchmark*` functions for `Parse`, `Compare`, `AnalyzeHazards`, and
   `LookupHazard` across representative module sizes (small/medium/large).
2. Add a `make bench` / `make memprofile` / `make cpuprofile` workflow (done in
   Pass 10) and store baselines in `docs/perf-baseline.md`.
3. Add regression thresholds in CI: fail if any benchmark regresses > 10%.
4. Add fuzz corpus seeding from real-world modules to keep parser performance
   honest.
5. Add `-race` to the benchmark CI job.

## 3. Feature gaps vs. other Terraform state/module management systems

| Gap | Source system | Recommendation |
| :--- | :--- | :--- |
| Dependency ordering between modules | Terragrunt | Add a module dependency graph and topological execution plan output. |
| Pre/post hooks around plan/apply | Terragrunt | Add hook points in the CLI for `before`/`after` plan and apply. |
| State encryption | OpenTofu | Add optional state-file encryption/decryption helpers. |
| Moved/import block normalization | OpenTofu/Terraform 1.1+ | Already parsed; wire into codemod apply path (carried from Pass 9). |
| Plan/apply policy gates | Atlantis/Spacelift | Add OPA/Rego policy evaluation against the diff report (partially present). |
| Drift detection | Spacelift/TFC | Add a `drift` command comparing state to live infrastructure. |
| Run triggers / notifications | TFC | Add webhook notifications on hazardous changes. |
| Resource transforms | Pulumi | Add codemod transforms for resource renaming and splitting. |
| Composition / claims | Crossplane | Add composite module analysis and claim-level diffing. |
| Remote state locking | TFC | Add advisory locks for shared module analysis. |

## 4. Pass 10 delivery items

| # | Item | Status |
| :--- | :--- | :--- |
| 173 | Expand recreation hazards to Rancher/RKE/k3s/OpenShift/EKS addons/AKS extensions/GKE features/VPC/RDS/S3/Cloud SQL/Storage/Cosmos DB | Done |
| 174 | Add compound addon/extension module classification in `matchModuleCategory` | Done |
| 175 | Add `memprofile` and `cpuprofile` Makefile targets | Done |
| 176 | Add benchmarks for hazard lookup and analysis | Done |
| 177 | Overwrite roadmap with memory/performance/feature-gap recommendations | Done |
| 178 | Implement on-disk IR cache | Done (existing L2 cache) |
| 179 | Implement module dependency graph output | Done (pkg/modulegraph) |
| 180 | Implement hook points in CLI | Done (pkg/hooks) |
| 181 | Implement state encryption helpers | Done (pkg/encryption) |
| 182 | Wire moved-block normalization into codemod | Done (codemod + ast.NormalizeMovedKey) |
| 183 | Add drift detection command | Planned |
| 184 | Add streaming diff/report writers | Done (report.Render*To) |
| 185 | Add benchmark regression thresholds in CI | Done (make bench/bench-check) |

## 5. Verification

All existing tests continue to pass at 100% statement coverage. New hazard categories
are covered by `TestMatchModuleCategory` and benchmarks by `BenchmarkLookupHazard`
and `BenchmarkAnalyzeHazards`.

---

*Generated as part of Pass 10.*
