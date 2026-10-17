# Executive Summary: Features & Enterprise Use Cases

`terraform_ast` (`tfastdiff`) is an enterprise-grade Abstract Syntax Tree (AST) diff, semantic compatibility analysis, and automated migration engine for HashiCorp Terraform and OpenTofu modules.

---

## 🎯 Executive Overview

Modern cloud platforms rely on shared Terraform and OpenTofu modules to provision critical infrastructure across Google Cloud (GCP), Amazon Web Services (AWS), and Microsoft Azure. However, upgrading upstream modules often introduces subtle breaking changes—such as removed variables, tightened type constraints, altered outputs, or attribute modifications that trigger catastrophic resource recreation (`ForceNew`).

`tfastdiff` eliminates upgrade uncertainty by parsing Terraform configurations directly into an AST, evaluating expressions and variable taint dependencies, computing semantic compatibility diffs, predicting blast radius across monorepos, and generating automated migration codemods.

```mermaid
flowchart LR
    A[Upstream Module\nv1.0.0 → v2.0.0] --> B[tfastdiff Engine\nAST Parser & EvalContext]
    C[Local Wrappers &\nTerragrunt Inputs] --> B
    D[Provider Schema JSON\nrequires_replace] --> B
    B --> E[Semantic Diff & Verdict\nSAFE / BREAKING]
    B --> F[Recreation Hazards\nForceNew Alerts]
    B --> G[Automated Codemods\n--fix / moved {} Blocks]
    B --> H[IDE LSP & CI Gates\nSARIF / OPA Rego Policies]
```

---

## 🚀 Key Platform Features

### 1. Semantic AST Inspection & Deep Diff Engine (`pkg/ast`, `pkg/diff`)
- **Direct AST Parsing**: Parses `.tf` and `.tf.json` files without requiring an initialized `.terraform` directory or live cloud credentials.
- **Modern HCL & OpenTofu Support**: Full first-class support for `removed {}` blocks (with soft destruction lifecycle `destroy = false`), `check {}` assertions, programmatic `import {}`, custom provider functions (`function "..." {}`), and client-side state encryption blocks (`encryption {}`).
- **Comprehensive Change Categorization**: Accurately classifies changes into **Breaking Changes** (removed inputs, newly required inputs without defaults, narrowed type constraints, removed outputs), **New Features** (optional inputs, new outputs), and **Deprecation Warnings**.
- **Object Schema Inspection**: Traverses deeply nested `object(...)` type constraints, detecting added required fields or incompatible attribute definitions.

### 2. Expression Evaluation Context & Taint Reachability (`pkg/eval`)
- **Embedded `hcl.EvalContext`**: Pure, hermetic implementations of Terraform standard functions (`concat`, `merge`, `lookup`, `coalesce`, `element`, `lower`, `upper`, `trimspace`, `jsondecode`).
- **Terragrunt Dynamic Inputs**: Evaluates Terragrunt `inputs = merge(...)` blocks and wrapper variable delegations without failing with incomplete analysis.
- **Dependency Taint Analysis**: Traces input variables through intermediate `locals` and child module calls to verify whether an upstream breaking change reaches an active managed resource or remains dead code.

### 3. State Recreation & `ForceNew` Hazard Detection (`pkg/recreation`)
- **Dynamic Provider Schema Ingestion**: Ingests schema dumps (`terraform providers schema -json`) to extract `requires_replace` metadata across all cloud resource attributes.
- **Built-in Cloud Heuristics**: Pre-configured immutability detection for flagship multi-cloud modules:
  - **GCP GKE**: Cluster name, region, network, subnetwork, and secondary pod CIDR blocks.
  - **AWS EKS**: Cluster name, VPC attachment, and IP family.
  - **Azure AKS**: Cluster name, resource group, location, and DNS prefix.
- **Disaster Prevention**: Alerts teams before applying plans that would trigger complete database rebuilds or Kubernetes control plane destructions.

### 4. Monorepo Blast Radius & Terragrunt Scanner (`pkg/blastradius`)
- **Recursive Monorepo Traversal**: Discovers all downstream root modules and Terragrunt environments consuming an upstream module.
- **Consumer Impact Assessment**: Pinpoints exactly which call sites pass deprecated arguments or reference removed outputs.
- **Unified SARIF & JSON Reporting**: Emits structured diagnostics (`TF-BLAST-RADIUS-CONSUMER`) for GitHub Code Scanning and enterprise compliance dashboards.

### 5. Automated Migration Codemods & Planner (`pkg/codemod`, `pkg/planner`)
- **In-Place Refactoring (`--fix`)**: Modifies `.tf` files using `hclwrite` to cleanly strip obsolete arguments while preserving indentation and comments.
- **Safe `moved {}` Block Synthesis**: Automatically outputs refactoring declarations (`from = ...` to `to = ...`) to prevent resource re-creation when upstream refactors internal module addresses.
- **Transitive Multi-Version Planner**: Analyzes intermediate Git releases (e.g. `v25.0.0` to `v31.0.0`) to find the highest contiguous "safe harbor" version.

### 6. Enterprise Policy Governance (OPA / Rego & JSON Gates) (`pkg/policy`)
- **Open Policy Agent (OPA) Integration**: Evaluates customizable Rego policies (`deny[msg]`, `violation[msg]`) against diff and blast radius reports.
- **Architectural Rules**: Enforce policies such as blocking major version jumps in production environments, requiring review if blast radius exceeds 5 root modules, or disallowing breaking changes in PCI-DSS compliant repositories.

### 7. Real-Time IDE Language Server Protocol Daemon (`pkg/lsp`)
- **Sub-50ms Inline Diagnostics**: Runs in the background (`tfastdiff daemon` / `--lsp`) implementing the JSON-RPC 2.0 LSP protocol for VS Code, Neovim, and JetBrains.
- **Instant Developer Feedback**: Triggers on `textDocument/didSave` to display inline squiggles for breaking changes.
- **Quick-Fix Code Actions**: Allows developers to apply codemod fixes directly from their editor.

### 8. Enterprise Supply Chain Security & SLSA Level 3 (`.github/workflows`)
- **Hermetic SLSA Level 3 Provenance**: Automated cryptographic build attestations via GitHub Actions builders.
- **Sigstore Cosign Keyless Signing**: Cryptographically signs release binaries and container images.
- **Syft SBOM**: Generates both CycloneDX and SPDX Software Bill of Materials at build time.
- **Hardened Distroless Containers**: Strict non-root UID `65532:65532`, read-only rootfs (`--read-only`), and zero critical/high CVEs.

---

## 💼 Enterprise Use Cases

| # | Use Case | Target Audience | Problem Solved | Primary Value |
| :-: | :--- | :--- | :--- | :--- |
| **1** | **Upstream Module Upgrades** | Platform & DevOps Engineers | Upgrading public or internal modules (GKE, EKS, AKS) without breaking wrapper abstractions. | Zero-downtime upgrades, guaranteed semantic compatibility. |
| **2** | **Monorepo CI/CD Pull Request Gates** | SRE & Cloud Architects | Module maintainers inadvertently merging breaking changes that affect downstream teams. | Automated PR blocking (`--exit-code`), SARIF code scanning annotations. |
| **3** | **Automated Refactoring & Migration PRs** | Infrastructure Engineers | Manually updating hundreds of module call sites across repositories. | In-place automated fix (`--fix`) and `moved {}` block generation. |
| **4** | **Infrastructure Recreation Prevention** | Cloud Security & Operations | Accidental cluster or database drops caused by modifying immutable attributes. | Early detection of `ForceNew` hazards before running `terraform apply`. |
| **5** | **Multi-Version Strategic Upgrade Planning** | Engineering Leadership | Migrating long-unmaintained infrastructure forward across multiple major versions. | Identifies optimal safe intermediate versions and upgrade paths. |
| **6** | **Real-Time IDE Feedback Loop** | Terraform Developers | Discovering module compatibility errors late in the CI cycle. | Sub-50ms inline diagnostics and quick-fix actions inside the editor. |
| **7** | **Multi-Cloud Upstream Drift Monitoring** | Enterprise Architecture | Public registry modules silently releasing breaking changes or deprecations. | Daily automated matrix scans across GCP, AWS, and Azure. |

---

## 📊 Technical Specifications & Verification Guarantees

- **Language & Runtime**: Pure Go (statically linked, zero external CGO dependencies).
- **Test Coverage**: **100.0% statement coverage** across all packages enforced in CI.
- **Verification Gates**:
  - `gofmt` & `go vet`: Clean.
  - `golangci-lint`: 0 issues (govet, staticcheck, unused, gosec).
  - Concurrency Safety: 0 data races under `go test -race`.
  - Differential Fuzzing: Fuzz-tested against official HashiCorp Terraform and OpenTofu compilers.
  - Algebraic Invariants: Formally verified Identity ($\Delta(A \to A) = \emptyset$) and Triangle Inequality ($\Delta(A \to B) \cup \Delta(B \to C) \supseteq \Delta(A \to C)$).
  - Terratest Plan State Validation: Offline validation confirming `Plan: 0 to destroy` on safe updates and `(forces replacement)` on ForceNew hazards.
