# terraform_ast (tfastdiff)

[![Go Coverage](https://img.shields.io/badge/coverage-100%25-brightgreen.svg)](#testing--quality-assurance)
[![Terratest](https://img.shields.io/badge/terratest-validated-blue.svg)](test/terratest/)

`terraform_ast` (`tfastdiff`) is a Go CLI and library for comparing the Abstract Syntax Tree (AST) and top-level configuration metadata of Terraform modules. It specifically targets enterprise wrappers (such as **Google GKE** and **AWS EKS** terraform wrappers) to determine whether a wrapper can be safely updated and pinned to a new upstream version without breaking downstream consumers.

---

## Key Features

- **AST Inspection & Parsing**: Uses [`github.com/hashicorp/terraform-config-inspect`](https://github.com/hashicorp/terraform-config-inspect) and HCL parsing to extract variables, outputs, module calls, provider requirements and aliases, validation rules, and type constraints from both `.tf` and `.tf.json` files. Module meta-arguments are excluded from forwarded input analysis, and statically declared variable nullability is tracked.
- **Terraform Registry Protocol**: Directly downloads and inspects modules from the Terraform Registry (e.g. `terraform-google-modules/kubernetes-engine/google`, `terraform-aws-modules/eks/aws`).
- **Deep Structural Object Schema Diffing**: Analyzes nested `object({ ... })` type constraints attribute-by-attribute (attribute additions, removals, optional-to-required transitions, and type shifts).
- **Blast-Radius & Consumer Analysis**: Recursively scans Terraform root modules and Terragrunt `terragrunt.hcl` configurations to evaluate consumer-level impact (`--blast-radius ./environments`). Findings are embedded in the same document for every output format, so JSON and SARIF stay machine-parseable.
- **Confidence-Scored Findings & Incremental Inspection**: Adds per-finding confidence scores to reports and reuses module inspections while source files remain unchanged; edits invalidate cached results.
- **State Destruction & Recreation Heuristics**: Flags `ForceNew` destruction hazards on critical immutable cloud resources (GKE cluster name, project ID, location, secondary subnet CIDRs, EKS cluster name, VPC ID). `--check-recreation` applies to every output format, including `TF-RECREATION-HAZARD` SARIF rules for GitHub Code Scanning.
- **OASIS SARIF v2.1.0 Export**: Generates industry-standard SARIF reports (`--format sarif`) for direct integration with GitHub Code Scanning.
- **Git Optimization & In-Memory Caching**: Implements shallow cloning (`--depth 1`) and a thread-safe repository cache that clones each repository once per run and reuses the local copy for every tag evaluation, so `--upgrade-path` across a major version no longer re-downloads the repository for each intermediate release.
- **Semantic Versioning & Pinning**: Uses [`golang.org/x/mod/semver`](https://pkg.go.dev/golang.org/x/mod/semver) to detect major breaking bumps, minor additions, patch updates, and downgrade hazards.
- **Breaking Change Detection**:
  - **Removed inputs**: Arguments passed to upstream that are no longer supported.
  - **Newly required inputs without defaults**: Variables introduced upstream that require values.
  - **Type constraint shifts**: Incompatible type changes (e.g., `string` $\rightarrow$ `number` or `list(string)`).
  - **Removed outputs**: Outputs that downstream root modules or the wrapper rely on.
- **Wrapper Compatibility Audit**:
  - Validates passed arguments in child `module` blocks.
  - Verifies wrapper variable types match upstream types.
  - Detects broken output delegations (`module.<name>.<output>`).
  - Generates recommended pinning constraints (e.g. `version = "30.0.0"`).
  - Outlines exact remediation steps for engineers.
- **State Graph & Intermediate Representation (IR)**: Tracks canonical hierarchical Terraform resource instance addresses (`module.vpc.aws_subnet.this["a"]`) and detects state destruction hazards when topology or naming shifts between module versions.
- **Automated `moved {}` Block Synthesis**: Translates detected resource address shifts directly into Terraform 1.1+ `moved {}` blocks (`moved { from = ... to = ... }`), eliminating manual remediation.
- **Dynamic Provider Schema Auto-Discovery**: Automatically inspects `required_providers`, discovers local schemas or queries built-in ForceNew attribute tables for standard cloud providers (AWS, GCP, AzureRM, Kubernetes, Cloudflare), and caches them in the persistent L2 disk cache.
- **Multi-Format Output**: Terminal text with visual indicators, GitHub-flavored Markdown for PR comments, machine-readable JSON for CI/CD gates, and SARIF for code scanning.
- **Enterprise Policy Governance (OPA Rego v1)**: Enforce JSON or official OPA SDK-powered Rego policies (`--policy`, `--rego-policy`) evaluating arbitrary findings; trace input variable taint dependency graphs across module boundaries (`--taint`); preview unified patches for removed wrapper arguments.
- **Upgrade-Path Analysis**: Compare adjacent semantic-version Git tags or Registry releases across a major-version jump, identify the last continuously safe version, and emit text, Markdown, or JSON reports.
- **Language Server Protocol (LSP)**: Built-in JSON-RPC 2.0 language server (`tfastdiff daemon` / `--lsp`) with buffer-aware diagnostics and automated quick fixes.
- **Enterprise Test Coverage**: **100% statement coverage** across every package, enforced in CI. Validated via [Terratest](test/terratest/) on realistic GKE/EKS fixtures, [Golden Image / Golden File](test/golden/) regression testing, and 30 native Go fuzz targets over every parser.

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
- [CLI Reference & CI/CD Guide](docs/cli_usage.md): Complete flag reference, exit codes, and GitHub Actions integration.
- [Compatibility Rules](docs/compatibility_rules.md): Detailed classification taxonomy for breaking changes, downgrades, and new features.
- [Testing Methodology](docs/testing_methodology.md): Unit, fuzz and Terratest validation, golden file verification, and the 100% coverage gate.
- [Quality & Accuracy Gates](docs/gates.md): Upgrade path accuracy validation, invariant assertions, and the pre-commit quality gate.
- [Strategic Roadmap](docs/roadmap.md): Evolution plan covering pre-commit hooks, codemods, and multi-version planning.

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
| `FuzzInspect` | `pkg/ast` | Inspect never panics and re-inspection is stable |
| `FuzzCheckTypeCompatibility` | `pkg/ast` | Compatible is never breaking; `any` widening/narrowing rules hold |
| `FuzzParseObjectSchema` | `pkg/ast` | Parsed attribute keys always match their recorded names |
| `FuzzParseModernHCLBlocks` | `pkg/ast` | Modern HCL blocks parse stably or fail gracefully |
| `FuzzParseMovedLifecycleBackend` | `pkg/ast` | `moved`, `lifecycle`, `backend`, and `count`/`for_each` enrich cleanly |
| `FuzzDifferentialValidation` | `pkg/ast` | AST parser matches compiler validation invariants |
| `FuzzDiscoverCallSites` | `pkg/blastradius` | Consumer scanning never panics and always yields a complete call site |
| `FuzzDiscoverJSONCallSites` | `pkg/blastradius` | Terraform JSON scanning never panics |
| `FuzzTerragruntInputKey` | `pkg/blastradius` | Terragrunt keys resolve to a name or cleanly fail |
| `FuzzPreviewRemovedInputs` | `pkg/codemod` | Patch generation never modifies the wrapper it inspects |
| `FuzzUnifiedReplacement` | `pkg/codemod` | Generated patches always carry a valid unified diff header |
| `FuzzFormatMovedBlocks` | `pkg/codemod` | Generated `moved {}` blocks are syntactically valid |
| `FuzzEvaluateExpression` | `pkg/eval` | HCL expression evaluation never panics |
| `FuzzLSPServe` | `pkg/lsp` | LSP JSON-RPC framing handles arbitrary payloads safely |
| `FuzzParseProviderSchema` | `pkg/recreation` | Dynamic provider schema parsing never panics |
| `FuzzIsRegistrySource` | `pkg/registry` | Local paths and VCS addresses are never treated as Registry sources |
| `FuzzParseRegistrySource` | `pkg/registry` | Registry addresses parse completely or are rejected |
| `FuzzNormalizeAndIsValid` | `pkg/semverutil` | Normalisation is idempotent and agrees with the semver library |
| `FuzzDetermineBumpType` | `pkg/semverutil` | Bump classification always matches semver ordering |
| `FuzzSort` | `pkg/semverutil` | Sorting preserves every element and enforces strict weak ordering |

Run a single target:

```bash
go test ./pkg/ast -run '^FuzzInspect$' -fuzz '^FuzzInspect$' -fuzztime 60s
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
