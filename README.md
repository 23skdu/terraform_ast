# terraform_ast (tfastdiff)

[![Go Coverage](https://img.shields.io/badge/coverage-100%25-brightgreen.svg)](#testing--quality-assurance)
[![Terratest](https://img.shields.io/badge/terratest-validated-blue.svg)](test/terratest/)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

`terraform_ast` (`tfastdiff`) is a Go CLI and library for comparing the Abstract Syntax Tree (AST) and top-level configuration metadata of Terraform modules. It specifically targets enterprise wrappers (such as **Google GKE** and **AWS EKS** terraform wrappers) to determine whether a wrapper can be safely updated and pinned to a new upstream version without breaking downstream consumers.

---

## Key Features

- **AST Inspection & Parsing**: Uses [`github.com/hashicorp/terraform-config-inspect`](https://github.com/hashicorp/terraform-config-inspect) and HCL parsing to extract variables, outputs, module calls, provider requirements, and type constraints.
- **Terraform Registry Protocol**: Directly downloads and inspects modules from the Terraform Registry (e.g. `terraform-google-modules/kubernetes-engine/google`, `terraform-aws-modules/eks/aws`).
- **Deep Structural Object Schema Diffing**: Analyzes nested `object({ ... })` type constraints attribute-by-attribute (attribute additions, removals, optional-to-required transitions, and type shifts).
- **Blast-Radius & Consumer Analysis**: Recursively scans downstream root modules and environment directories to evaluate consumer-level impact (`--blast-radius ./environments`).
- **State Destruction & Recreation Heuristics**: Flags `ForceNew` destruction hazards on critical immutable cloud resources (GKE cluster name, project ID, location, secondary subnet CIDRs, EKS cluster name, VPC ID).
- **OASIS SARIF v2.1.0 Export**: Generates industry-standard SARIF reports (`--format sarif`) for direct integration with GitHub Code Scanning.
- **Git Optimization & In-Memory Caching**: Implements shallow cloning (`--depth 1`) and thread-safe repository caching to maximize analysis speed.
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
- **Multi-Format Output**: Terminal text with visual indicators, GitHub-flavored Markdown for PR comments, machine-readable JSON for CI/CD gates, and SARIF for code scanning.
- **Enterprise Test Coverage**: Validated via [Terratest](test/terratest/) on realistic GKE/EKS fixtures and [Golden Image / Golden File](test/golden/) methodology.

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

---

## Example Output (Markdown)

```markdown
# Terraform Wrapper Compatibility Audit

- **Wrapper Path**: `test/fixtures/gke_wrapper`
- **Target Module**: `gke`
- **Target Version**: `v30.0.0`
- **Recommended Pin**: `version = "30.0.0"`
- **Verdict**: `BREAKING_CHANGES_DETECTED`

> [!CAUTION]
> **CANNOT SAFELY PIN: Wrapper has 5 breaking issue(s) with upstream v30.0.0. Resolve remediation steps before pinning.**

### 🚨 Compatibility Issues (6)

| Category | Target | Description | Remediation |
| :--- | :--- | :--- | :--- |
| `BrokenOutputDelegation` | `legacy_network_tag` | Wrapper output 'legacy_network_tag' references module.gke.legacy_network_tag, but output was removed | Update or remove output "legacy_network_tag" in wrapper outputs.tf |
| `MissingRequiredInput` | `cluster_autoscaling_profile` | Target upstream v30.0.0 requires input 'cluster_autoscaling_profile' (no default), but wrapper call 'gke' does not provide it | Add argument 'cluster_autoscaling_profile = ...' to module "gke" block in wrapper |
| `MissingRequiredInput` | `grant_registry_access` | Target upstream v30.0.0 requires input 'grant_registry_access' (no default), but wrapper call 'gke' does not provide it | Add argument 'grant_registry_access = ...' to module "gke" block in wrapper |
| `PassedRemovedInput` | `ip_range_services` | Wrapper passes argument 'ip_range_services' to child module 'gke', but 'ip_range_services' is not an accepted input | Remove argument 'ip_range_services' from module "gke" block in wrapper |
| `TypeMismatch` | `ip_range_pods` | Wrapper variable 'ip_range_pods' type 'string' is incompatible with upstream expected type 'list(string)' | Update variable "ip_range_pods" in wrapper to type = list(string) |
| `TypeMismatch` | `zones` | Wrapper variable 'zones' type 'list(string)' differs from upstream 'set(string)': Type converted from list to set | Align variable "zones" with upstream type |

### 💡 Available Upstream Features (1)

| Category | Target | Description |
| :--- | :--- | :--- |
| `OptionalVariableAvailable` | `enable_cost_allocation` | New optional upstream parameter 'enable_cost_allocation' (type bool) can be exposed in wrapper |
```

---

## Documentation

Full architectural and technical specifications are available under [`docs/`](docs/):

- [Architecture & Design](docs/ARCHITECTURE.md): Component breakdown, pipelines, and class diagrams.
- [CLI Reference & CI/CD Guide](docs/CLI_USAGE.md): Complete flag reference, exit codes, and GitHub Actions integration.
- [Compatibility Rules](docs/COMPATIBILITY_RULES.md): Detailed classification taxonomy for breaking changes, downgrades, and new features.
- [Testing Methodology](docs/TESTING_METHODOLOGY.md): Terratest validation, golden file verification, and 100% coverage guidelines.
- [Strategic Roadmap](docs/ROADMAP.md): Evolution plan covering pre-commit hooks, codemods, and multi-version planning.

---

## Testing & Quality Assurance

This repository enforces **100% statement coverage** across all packages.

Run all unit, integration, and terratest suites:

```bash
go test -v -cover ./...
```

Run Terratest validation against local Terraform engine:

```bash
go test -v ./test/terratest
```

Run Docker DevSecOps static analysis and container security tests:

```bash
go test -v ./test/docker
```

Regenerate canonical golden test artifacts:

```bash
UPDATE_GOLDEN=true go test -v ./test/terratest
```

---

## License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.
