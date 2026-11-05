# terraform_ast (tfastdiff)

[![Go Coverage](https://img.shields.io/badge/coverage-100%25-brightgreen.svg)](#testing--quality-assurance)
[![Terratest](https://img.shields.io/badge/terratest-validated-blue.svg)](test/terratest/)

`terraform_ast` (`tfastdiff`) is a Go CLI and library for comparing the Abstract Syntax Tree (AST) and top-level configuration metadata of Terraform modules. It specifically targets enterprise wrappers (such as **Google GKE** and **AWS EKS** terraform wrappers) to determine whether a wrapper can be safely updated and pinned to a new upstream version without breaking downstream consumers.

---

## Key Features

- **Single-Pass AST Inspection & Multi-Tier Caching (`pkg/ast`)**: Uses [`github.com/hashicorp/terraform-config-inspect`](https://github.com/hashicorp/terraform-config-inspect) and HCL/JSON parsing (`parseAndEnrichModule`) to extract variables, outputs, module calls, resources, data sources, provider requirements/aliases/configs, `lifecycle` preconditions/postconditions, independent `backend {}` and `cloud {}` configurations, and modern Terraform 1.7+ / OpenTofu blocks (`moved`, `removed`, `check`, `import`, `function`, `encryption`) from `.tf` and `.tf.json` files in a single pass. Includes in-memory overlay inspection (`InspectSource`) and L1 in-memory / L2 persistent disk caching (`--cache-dir`).
- **Terraform Registry Protocol (`pkg/registry`)**: Directly downloads and inspects modules from the Terraform Registry (e.g. `terraform-google-modules/kubernetes-engine/google`, `terraform-aws-modules/eks/aws`) with SSRF protection and path-traversal guards.
- **Deep Structural Object Schema Diffing**: Analyzes nested `object({ ... })` type constraints attribute-by-attribute (attribute additions, removals, `optional()` default transitions, and type shifts).
- **Parallel Blast-Radius & Terragrunt Consumer Analysis (`pkg/blastradius`)**: Uses a bounded worker pool to scan Terraform root modules (`.tf`, `.tf.json`) and Terragrunt `terragrunt.hcl` configurations (`--blast-radius ./environments`), evaluating `merge(...)`, `find_in_parent_folders()`, and `read_terragrunt_config()` chains with evidence-gated risk attribution (`CRITICAL`, `HIGH`, `MEDIUM`, `LOW`).
- **Evidence-Calibrated Confidence & Safety Scoring**: Assigns evidence-backed confidence scores (`[1, 10]`) to every finding, computes safety verdicts (`SAFE`, `WARNINGS_ONLY`, `BREAKING_CHANGES`, `DOWNGRADE`), and recommends the highest contiguous safe-harbor version (`SafePinVersion`).
- **ForceNew Destruction Heuristics & Golden Plan Verification (`pkg/recreation`)**: Flags `ForceNew` (`requires_replace`) hazards using built-in cloud catalogs (AWS, GCP, AzureRM, Kubernetes, Cloudflare), auto-discovered `terraform providers schema -json` dumps (`DynamicHazardRegistry`), and Terraform execution plan JSON (`--golden-plan` or automatic `tfplan.json`/`golden-plan.json` discovery).
- **OASIS SARIF v2.1.0 Export (`pkg/report`)**: Generates line-exact SARIF v2.1.0 reports (`--format sarif`), including `TF-RECREATION-HAZARD` and `TF-POLICY-VIOLATION` rules for GitHub Code Scanning.
- **Git Optimization & Shared Repository Cache (`pkg/gitops`)**: Implements shallow cloning (`--depth 1`), Terraform VCS source normalization (`git::`, `github.com/`, `//subdir?ref=`), and a thread-safe `RepositoryCache` that clones each repository once per run.
- **Semantic Versioning & Constraint Range Analysis (`pkg/semverutil`)**: Uses [`golang.org/x/mod/semver`](https://pkg.go.dev/golang.org/x/mod/semver) and interval constraint parsing (`ParseConstraintRange`) to detect major breaking bumps, `0.x` minor breaking bumps, upper-bound major narrowings, and downgrade hazards.
- **Comprehensive Breaking Change Detection (`pkg/diff`)**:
  - **Interface & Type Contracts**: Removed inputs/outputs, newly required inputs without defaults, changed defaults, disallowed `null`, incompatible type shifts, and output expression changes paired with type/sensitivity shifts.
  - **Resource & Module Cardinality**: Unmigrated resource/module removals or address shifts, `count` reductions, static `for_each` key-set shrinkage (while key-set preservation/widening emits `WARNING`), and indexing mode transitions.
  - **Infrastructure & Lifecycle Hazards**: Resource-body `ForceNew` attribute changes (`ResourceForceNew`), `triggers`/`keepers` drift, disabled `deletion_protection` or enabled `force_destroy`, `local.*` and `data.*` selector drift feeding `ForceNew` attributes, `precondition`/`postcondition` modifications on `ForceNew` resources, `provider {}` block additions/modifications (`ProviderConfigRemoved` emitted as `WARNING`), and `backend {}` / `cloud {}` state migrations (`StateBackendChange`).
- **Wrapper Compatibility Audit (`pkg/wrapper`)**:
  - Validates passed arguments and required inputs across every child `module` call in the wrapper.
  - Traces renamed variables and transitive `local.*` expressions across input forwarding and output delegations (`module.<name>.<output>`).
  - Generates recommended pinning constraints, safe-harbor fallback pins, and actionable remediation steps.
- **State Graph, Dependency Visualization & Automated `moved {}` Synthesis (`pkg/ast`, `pkg/visualize`, `pkg/codemod`)**: Tracks canonical Terraform resource instance addresses, exports DOT/Mermaid dependency graphs (`--graph-format dot|mermaid`), generates unified diffs removing dead wrapper inputs (`--fix-preview`), and synthesizes Terraform 1.1+ `moved {}` blocks (`--generate-moved`).
- **Enterprise Policy Governance & Variable Taint Analysis (`pkg/policy`, `pkg/eval`)**: Enforces JSON or official OPA SDK Rego v1 policies (`--policy`, `--rego-policy`) and traces input variable taint paths across `.tf` and `.tf.json` `locals`, resources, data sources, modules, and outputs (`--taint`).
- **Multi-Step Upgrade-Path Planner (`pkg/planner`)**: Evaluates adjacent Git tags or Registry releases across a major-version jump (`--upgrade-path`) with schema-aware `ForceNew` detection to identify the furthest contiguous safe version.
- **Language Server Protocol (`pkg/lsp`)**: Built-in JSON-RPC 2.0 language server (`tfastdiff daemon` / `--lsp`) with in-memory buffer overlay diagnostics, hover inspection, and code-action quick fixes.
- **Enterprise Verification & Test Coverage**: **100.0% statement coverage** across every package, enforced in CI, backed by [Terratest](test/terratest/), [Golden File](test/golden/) regression suites, **31 native Go fuzz targets**, and an [Independent Python Verification Oracle](scripts/oracle_verify.py).

---

## Quickstart

### 1. Installation

```bash
git clone git@github.com:23skdu/terraform_ast.git
cd terraform_ast
go build -o bin/tfastdiff cmd/tfastdiff/main.go
```

### 2. Running via Docker (DevSecOps Hardened)

A hardened, minimal distroless image (~5.7MB, non-root, read-only rootfs compatible) is provided:

```bash
# Build the minimal container
docker build -t tfastdiff:latest .

# Run module analysis securely
docker run --rm \
  --read-only \
  --security-opt no-new-privileges:true \
  -v $(pwd):/workspace:ro \
  tfastdiff:latest \
  --old ./modules/upstream-v19 \
  --new ./modules/upstream-v20 \
  --format markdown
```

### 3. Auditing a Wrapper Module

Audit a wrapper module against a target upstream release:

```bash
./bin/tfastdiff \
  --wrapper ./my-gke-wrapper \
  --upstream https://github.com/terraform-google-modules/terraform-google-kubernetes-engine.git \
  --new-version v30.0.0 \
  --format markdown \
  --exit-code
```

### 4. Comparing Two Upstream Module Versions

Directly compare two versions of an upstream module:

```bash
./bin/tfastdiff \
  --old ./modules/upstream-v19 \
  --new ./modules/upstream-v20 \
  --old-version v19.0.0 \
  --new-version v20.0.0 \
  --format text
```

### 5. GitHub Action and Pre-Commit

Use the Docker-based GitHub Action with explicit module inputs:

```yaml
- uses: 23skdu/terraform_ast@main
  with:
    wrapper_path: ./modules/gke-wrapper
    upstream: terraform-google-modules/kubernetes-engine/google
    new_version: v30.0.0
    format: sarif
    exit_code: true
```

For a reusable pull-request report workflow, call `.github/workflows/tfastdiff-audit.yml` from your repository workflow:

```yaml
jobs:
  terraform-compatibility:
    uses: 23skdu/terraform_ast/.github/workflows/tfastdiff-audit.yml@main
    with:
      wrapper_path: ./modules/gke-wrapper
      upstream: terraform-google-modules/kubernetes-engine/google
      new_version: v30.0.0
      blast_radius: ./environments
```

The reusable workflow uploads SARIF and upserts one summary comment on pull requests. Its caller must allow `pull-requests: write` and `security-events: write`.

For local checks, add the repository hook to `.pre-commit-config.yaml` after installing `tfastdiff`:

```yaml
repos:
  - repo: https://github.com/23skdu/terraform_ast
    rev: v0.1.0
    hooks:
      - id: tfastdiff
        args: [--wrapper, ./modules/gke-wrapper, --exit-code]
```

---

## Example Output (Markdown)

```markdown
# Terraform Wrapper Compatibility Audit

- **Wrapper Path**: `test/fixtures/gke_wrapper`
- **Target Module**: `gke`
- **Target Version**: `v30.0.0`
- **Recommended Pin**: `version = "30.0.0"`
- **Safest Known Pin**: `v29.0.0` — Keep the currently pinned upstream version until the wrapper compatibility issues are resolved.
- **Verdict**: `BREAKING_CHANGES_DETECTED`

> [!CAUTION]
> **CANNOT SAFELY PIN: Wrapper has 5 breaking issue(s) with upstream v30.0.0. Resolve remediation steps before pinning.**

### Compatibility Issues (6)

| Category | Target | Description | Confidence | Remediation |
| :--- | :--- | :--- | :--- | :--- |
| `BrokenOutputDelegation` | `legacy_network_tag` | Wrapper output 'legacy_network_tag' references module.gke.legacy_network_tag, but output 'legacy_network_tag' was removed in target upstream v30.0.0 | 9/10 | Update or remove output "legacy_network_tag" in wrapper outputs.tf |
| `MissingRequiredInput` | `cluster_autoscaling_profile` | Target upstream v30.0.0 requires input 'cluster_autoscaling_profile' (no default), but wrapper call 'gke' does not provide it | 9/10 | Add argument 'cluster_autoscaling_profile = ...' to module "gke" block in wrapper |
| `MissingRequiredInput` | `grant_registry_access` | Target upstream v30.0.0 requires input 'grant_registry_access' (no default), but wrapper call 'gke' does not provide it | 9/10 | Add argument 'grant_registry_access = ...' to module "gke" block in wrapper |
| `PassedRemovedInput` | `ip_range_services` | Wrapper passes argument 'ip_range_services' to child module 'gke', but 'ip_range_services' is not an accepted input in target upstream v30.0.0 | 9/10 | Remove argument 'ip_range_services' from module "gke" block in wrapper |
| `TypeMismatch` | `ip_range_pods` | Wrapper variable 'ip_range_pods' type 'string' is incompatible with upstream expected type 'list(string)' | 8/10 | Update variable "ip_range_pods" in wrapper to type = list(string) |
| `TypeMismatch` | `zones` | Wrapper variable 'zones' type 'list(string)' differs from upstream 'set(string)': Type converted from list to set (Terraform automatically coerces list to set, but ordering is not preserved) | 8/10 | Align variable "zones" with upstream type |

### Available Upstream Features (1)

| Category | Target | Description | Confidence |
| :--- | :--- | :--- | :--- |
| `OptionalVariableAvailable` | `enable_cost_allocation` | New optional upstream parameter 'enable_cost_allocation' (type bool) can be exposed in wrapper | 9/10 |
```

---

## Documentation

Full architectural and technical specifications are available under [`docs/`](docs/):

- [Architecture & Design](docs/architecture.md): Component breakdown, pipelines, and class diagrams.
- [Features & Capabilities](docs/features.md): Detailed breakdown of core capabilities, enterprise extensions, and outputs.
- [CLI Reference & CI/CD Guide](docs/cli_usage.md): Complete flag reference, exit codes, and GitHub Actions integration.
- [Compatibility Rules](docs/compatibility_rules.md): Detailed classification taxonomy for breaking changes, warnings, downgrades, and new features.
- [Testing Methodology](docs/testing_methodology.md): Unit, fuzz, and Terratest validation, golden file verification, and the 100% coverage gate.
- [Quality & Accuracy Gates](docs/gates.md): Upgrade path accuracy validation, invariant assertions, independent oracle verification, and the pre-commit quality gate.
- [Strategic Roadmap](docs/roadmap.md): Completed engineering milestones across Passes 1–9.

---

## Testing & Quality Assurance

Every package is held at **100% statement coverage**, and the gate is enforced in CI so it cannot
silently regress.

Run everything (gofmt, vet, unit tests, coverage gate, fuzz smoke run):

```bash
./scripts/verify.sh          # default: 10s per fuzz target
./scripts/verify.sh 60       # longer fuzz campaign
```

### Unit tests

Run the unit suite and inspect per-package coverage:

```bash
go test -cover ./...
go test -coverprofile=coverage.out ./...
go tool cover -func=coverage.out | grep -v '100.0%'   # must print nothing
```

### Fuzz tests

Native Go fuzz targets cover every untrusted-input parser. Each one is seeded, runs as part of
`go test`, and asserts a real invariant rather than "does not crash":

| Target | Package | Invariant |
| :--- | :--- | :--- |
| `FuzzCLI` | `cmd/tfastdiff` | CLI entry point handles arbitrary argument sets safely without unhandled panic |
| `FuzzInspect` | `pkg/ast` | Inspect never panics and re-inspection is stable |
| `FuzzCheckTypeCompatibility` | `pkg/ast` | Compatible is never breaking; `any` widening/narrowing rules hold |
| `FuzzParseObjectSchema` | `pkg/ast` | Parsed attribute keys always match their recorded names |
| `FuzzParseModernHCLBlocks` | `pkg/ast` | Modern HCL/OpenTofu blocks parse stably or fail gracefully |
| `FuzzParseMovedLifecycleBackend` | `pkg/ast` | `moved`, `lifecycle`, `backend`, and `count`/`for_each` enrich cleanly |
| `FuzzDifferentialValidation` | `pkg/ast` | In-memory overlay inspection produces identical ASTs to disk inspection |
| `FuzzDiscoverCallSites` | `pkg/blastradius` | Consumer scanning never panics and always yields a complete call site |
| `FuzzDiscoverJSONCallSites` | `pkg/blastradius` | Terraform JSON scanning never panics |
| `FuzzTerragruntInputKey` | `pkg/blastradius` | Terragrunt keys resolve to a name or cleanly fail |
| `FuzzPreviewRemovedInputs` | `pkg/codemod` | Patch generation never modifies the wrapper it inspects |
| `FuzzUnifiedReplacement` | `pkg/codemod` | Generated patches always carry a valid unified diff header |
| `FuzzFormatMovedBlocks` | `pkg/codemod` | Generated `moved {}` blocks are syntactically valid |
| `FuzzCompare` | `pkg/diff` | AST module comparison preserves verdict soundness and confidence bounds |
| `FuzzEvaluateExpression` | `pkg/eval` | HCL expression evaluation never panics |
| `FuzzParseGitSource` | `pkg/gitops` | Git module source normalizer never panics on malformed URLs or refs |
| `FuzzResolveSubDir` | `pkg/gitops` | Subdirectory resolution never escapes repository boundaries |
| `FuzzLSPServe` | `pkg/lsp` | LSP JSON-RPC framing handles arbitrary payloads safely |
| `FuzzAnalyzeUpgradePath` | `pkg/planner` | Upgrade path planning preserves contiguous safe-harbor version bounds |
| `FuzzPolicyDecode` | `pkg/policy` | JSON policy unmarshaling never crashes on malformed policy schemas |
| `FuzzParseRego` | `pkg/policy` | Rego parser handles arbitrary rules with fail-closed security |
| `FuzzParseGoldenPlan` | `pkg/recreation` | Plan JSON ingestion normalizes actions and resource changes safely |
| `FuzzParseProviderSchema` | `pkg/recreation` | Dynamic provider schema parsing never panics |
| `FuzzIsRegistrySource` | `pkg/registry` | Local paths and VCS addresses are never treated as Registry sources |
| `FuzzParseRegistrySource` | `pkg/registry` | Registry addresses parse completely or are rejected |
| `FuzzRenderReports` | `pkg/report` | Multi-format report renderer produces valid text, Markdown, JSON, and SARIF |
| `FuzzNormalizeAndIsValid` | `pkg/semverutil` | Normalisation is idempotent and agrees with the semver library |
| `FuzzDetermineBumpType` | `pkg/semverutil` | Bump classification always matches semver ordering |
| `FuzzSort` | `pkg/semverutil` | Sorting preserves every element and enforces strict weak ordering |
| `FuzzParseConstraintRange` | `pkg/semverutil` | Constraint range parser handles arbitrary version constraint strings without panic |
| `FuzzAnalyzeWrapper` | `pkg/wrapper` | Wrapper analysis preserves safety equivalence (`TotalBreaking == 0 <=> Safe`) |

Run a single target:

```bash
go test ./pkg/ast -run '^FuzzInspect$' -fuzz '^FuzzInspect$' -fuzztime 60s
```

### Independent Verification Oracle & Performance Benchmarks

Run the decoupled Python verification oracle against golden fixtures and extended compatibility rules:

```bash
python3 scripts/oracle_verify.py --skip-upstream -v
```

Run the benchmark suite across `pkg/ast`, `pkg/diff`, `pkg/blastradius`, and `pkg/report`:

```bash
make perf
```

### Terratest

Runs `terraform init`/`validate`/`plan` against realistic GKE, EKS and object-schema fixtures,
plus a golden-file suite and end-to-end CLI runs against the compiled binary:

```bash
go test -v ./test/terratest
```

Regenerate the canonical golden artifacts:

```bash
UPDATE_GOLDEN=true go test -v ./test/terratest
```

Run Docker DevSecOps static analysis and container security tests:

```bash
go test -v ./test/docker
```

---

## License

This project is released into the public domain under the Unlicense - see the [LICENSE](LICENSE) file for details.
