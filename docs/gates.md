# Quality & Accuracy Gates Specification

This document details the quality assurance, verification, and accuracy governance architecture of `terraform_ast` (`tfastdiff`).

The system employs two foundational verification gates:
1. **The Pre-Commit & Verification Quality Gate**: An invariant pipeline enforcing code formatting, static security scanning, 100.0% statement coverage, and continuous differential fuzzing before merge.
2. **The Upgrade Path Accuracy Gate Engine**: An end-to-end empirical verification engine evaluating real-world AWS EKS and Google GKE upstream module releases across random and structured version paths to validate semantic correctness, safe version boundaries, exit codes, and confidence scoring.

---

## 1. Upgrade Path Accuracy Gate Engine

The Upgrade Path Accuracy Gate ([`scripts/validate_upgrade_accuracy.sh`](file:///home/rsd/REPOS/terraform_ast/scripts/validate_upgrade_accuracy.sh)) validates `tfastdiff` against live upstream releases of enterprise Terraform modules.

It exercises multi-step version paths across **AWS EKS** (`terraform-aws-modules/terraform-aws-eks`) and **Google GKE** (`terraform-google-modules/terraform-google-kubernetes-engine`), asserting mathematical and semantic invariants over the generated Abstract Syntax Tree (AST) comparison graph.

```mermaid
flowchart TD
    subgraph INGRESS["1. Module Ingress & Tag Discovery"]
        EKS["AWS EKS Upstream Repo"] --> DISCOVER["Remote Tag Discovery (git ls-remote)"]
        GKE["Google GKE Upstream Repo"] --> DISCOVER
        DISCOVER --> POOL["Semver Candidate Pool (vX.Y.Z)"]
    end

    subgraph SAMPLING["2. Upgrade Path Sampling"]
        POOL --> S_PATCH["Patch Series (e.g. v19.0.0 -> v19.0.4)"]
        POOL --> S_MINOR["Minor Bumps (e.g. v19.0.0 -> v19.2.0)"]
        POOL --> S_MAJOR["Major Jumps (e.g. v18.31.2 -> v19.0.0)"]
    end

    subgraph ENGINE["3. tfastdiff Execution"]
        S_PATCH --> EXEC["tfastdiff --upgrade-path --format json"]
        S_MINOR --> EXEC
        S_MAJOR --> EXEC
        EXEC --> JSON_OUT["JSON UpgradePathReport"]
        EXEC --> EXIT_CHECK["tfastdiff --exit-code Parity Check"]
    end

    subgraph INVARIANTS["4. Semantic Invariant Verification Engine"]
        JSON_OUT --> INV1["INV-1: Valid JSON & Schema Integrity"]
        JSON_OUT --> INV2["INV-2: Monotonic Ascending Version Progression"]
        JSON_OUT --> INV3["INV-3: Safety Parity (total_breaking == 0 <=> Safe)"]
        JSON_OUT --> INV4["INV-4: Verdict Soundness (SAFE / WARNINGS / BREAKING)"]
        JSON_OUT --> INV5["INV-5: Contiguous Safe Version Recommendation Bounds"]
        JSON_OUT --> INV6["INV-6: Confidence Score Bounds (1 <= score <= 10)"]
        EXIT_CHECK --> INV7["INV-7: Exit-Code Parity (Code 0 for safe, 1 for break)"]
    end

    subgraph SCORING["5. Gate Decision & Confidence Scoring"]
        INV1 --> METRICS["Accuracy Rate Calculation"]
        INV2 --> METRICS
        INV3 --> METRICS
        INV4 --> METRICS
        INV5 --> METRICS
        INV6 --> METRICS
        INV7 --> METRICS
        METRICS --> EVAL{"Accuracy Rate >= Threshold (100%)?"}
        EVAL -->|Yes| PASS["GATE PASS: Exit 0 (Export JSON Artifact)"]
        EVAL -->|No| FAIL["GATE FAIL: Exit 1 (Violations Reported)"]
    end
```

### Invariant Rules Enforced

| Rule ID | Name | Semantic Definition |
| :--- | :--- | :--- |
| **INV-1** | Schema Integrity | Output parses as valid JSON adhering to `UpgradePathReport` with non-empty `steps`. |
| **INV-2** | Monotonic Ordering | For all steps $i$, $v_{from, i} < v_{to, i}$ in strict semver ascending order. |
| **INV-3** | Safety Equivalence | $\text{total\_breaking} = 0 \iff \text{can\_safely\_update} = \text{true}$. |
| **INV-4** | Verdict Soundness | Verdict $\in \{\text{"SAFE"}, \text{"WARNINGS\_ONLY"}, \text{"BREAKING\_CHANGES"}, \text{"DOWNGRADE"}\}$. |
| **INV-5** | Recommendation Bounds | If $\text{can\_safely\_update\_all} = \text{true}$, $\text{recommended} = \text{target}$. If false, $\text{recommended} = v_{from}$ of the first breaking step. |
| **INV-6** | Confidence Score Bounds | For all findings, $1 \le \text{confidence\_score} \le 10$. Breaking findings must satisfy $\text{confidence\_score} \ge 7$. |
| **INV-7** | Exit-Code Parity | `--exit-code` produces exit code `0` if all transitions are safe, and code `1` if breaking changes exist. |

### Execution

```bash
# Run validation with default settings (3 paths per module, 100.0% threshold)
make accuracy-gate

# Run directly with customized sampling and artifact export
./scripts/validate_upgrade_accuracy.sh \
  --samples 5 \
  --min-accuracy 100.0 \
  --seed 1337 \
  --output /tmp/accuracy_report.json
```

---

## 2. Pre-Commit & Verification Quality Gate

The quality gate ensures that every commit, pull request, and release artifact maintains clean formatting, strict static analysis, complete statement coverage, and fuzz resilience.

The gate is implemented through [`scripts/verify.sh`](file:///home/rsd/REPOS/terraform_ast/scripts/verify.sh) and integrated directly into git pre-commit hooks ([`.pre-commit-hooks.yaml`](file:///home/rsd/REPOS/terraform_ast/.pre-commit-hooks.yaml)) and GitHub Actions CI ([`.github/workflows/ci.yml`](file:///home/rsd/REPOS/terraform_ast/.github/workflows/ci.yml)).

```mermaid
flowchart TD
    subgraph HOOK["1. Developer Entrypoint"]
        GIT_COMMIT["git commit / pre-commit run"] --> HOOK_DEF[".pre-commit-hooks.yaml"]
        HOOK_DEF --> WRAPPER_CHECK["tfastdiff --wrapper . --exit-code"]
        CLI_MAKE["make verify"] --> VERIFY_SCRIPT["scripts/verify.sh"]
    end

    subgraph STATIC["2. Static Hygiene & Linting"]
        VERIFY_SCRIPT --> FMT["gofmt -l . (Zero Unformatted Files)"]
        FMT --> VET["go vet ./..."]
        VET --> LINT["golangci-lint run (govet, errcheck, staticcheck, unused, gosec)"]
    end

    subgraph COVERAGE["3. Statement Coverage Gate"]
        LINT --> TEST["go test -covermode=atomic -coverprofile=coverage.out ./..."]
        TEST --> COV_EVAL["go tool cover -func=coverage.out"]
        COV_EVAL --> COV_GATE{"Coverage == 100.0% Across All Packages?"}
        COV_GATE -->|No| COV_FAIL["REJECT: Coverage Under 100.0%"]
    end

    subgraph FUZZ["4. Differential & Property Fuzzing"]
        COV_GATE -->|Yes| SMOKE["Fuzz Smoke Run (10s per target)"]
        SMOKE --> F_CMD["cmd/tfastdiff (FuzzCLI)"]
        SMOKE --> F_AST["pkg/ast (FuzzInspect, FuzzCheckTypeCompatibility, FuzzParseModernHCLBlocks, FuzzParseMovedLifecycleBackend...)"]
        SMOKE --> F_DIFF["pkg/diff (FuzzCompare)"]
        SMOKE --> F_GITOPS["pkg/gitops (FuzzParseGitSource, FuzzResolveSubDir)"]
        SMOKE --> F_PLAN["pkg/planner (FuzzAnalyzeUpgradePath)"]
        SMOKE --> F_POL["pkg/policy (FuzzPolicyDecode, FuzzParseRego)"]
        SMOKE --> F_REP["pkg/report (FuzzRenderReports)"]
        SMOKE --> F_WRAP["pkg/wrapper (FuzzAnalyzeWrapper)"]
        SMOKE --> F_REG["pkg/registry (FuzzIsRegistrySource, FuzzParseRegistrySource)"]
        SMOKE --> F_LSP["pkg/lsp (FuzzLSPServe)"]
        SMOKE --> F_BLAST["pkg/blastradius (FuzzDiscoverCallSites, FuzzTerragruntInputKey...)"]
        SMOKE --> F_CODE["pkg/codemod (FuzzFormatMovedBlocks, FuzzUnifiedReplacement...)"]
        SMOKE --> F_SEM["pkg/semverutil (FuzzDetermineBumpType, FuzzSort, FuzzNormalize, FuzzParseConstraintRange)"]
        SMOKE --> F_REC["pkg/recreation (FuzzParseProviderSchema, FuzzParseGoldenPlan)"]
        SMOKE --> F_EVAL["pkg/eval (FuzzEvaluateExpression)"]
    end

    subgraph MERGE["5. Gate Verdict & CI Release"]
        F_CMD --> PASS_ALL["All 31 Fuzz Targets Clean"]
        F_AST --> PASS_ALL
        F_DIFF --> PASS_ALL
        F_GITOPS --> PASS_ALL
        F_PLAN --> PASS_ALL
        F_POL --> PASS_ALL
        F_REP --> PASS_ALL
        F_WRAP --> PASS_ALL
        F_REG --> PASS_ALL
        F_LSP --> PASS_ALL
        F_BLAST --> PASS_ALL
        F_CODE --> PASS_ALL
        F_SEM --> PASS_ALL
        F_REC --> PASS_ALL
        F_EVAL --> PASS_ALL
        PASS_ALL --> GREEN["PASSED: Safe to Merge / Release"]
    end
```

### Quality Gate Stages

1. **Source Formatting (`gofmt`)**: Rejects any commit with unformatted Go files.
2. **Static Analysis (`go vet`)**: Validates Go idioms, type alignments, and struct tags.
3. **Multi-Linter Security Gate (`golangci-lint`)**:
   - `govet`: Compiler correctness and shadow variable detection.
   - `errcheck`: Ensures zero unhandled error returns.
   - `staticcheck`: Static code analysis and deprecation warnings.
   - `unused`: Rejects dead code and unreferenced package symbols.
   - `gosec`: AST-level security vulnerability scanning (CWE-compliant).
4. **100.0% Statement Coverage Gate**:
   - Evaluated atomically via `go test -covermode=atomic -coverprofile=... ./...`.
   - Rejects the build if a single statement across all packages remains uncovered.
5. **Universal Fuzz Testing Across All Packages**:
   - Executes fuzz testing across all 31 native Go fuzz targets spanning every package in the repository (`cmd/tfastdiff`, `pkg/ast`, `pkg/blastradius`, `pkg/codemod`, `pkg/diff`, `pkg/eval`, `pkg/gitops`, `pkg/lsp`, `pkg/planner`, `pkg/policy`, `pkg/recreation`, `pkg/registry`, `pkg/report`, `pkg/semverutil`, `pkg/wrapper`).

6. **Pre-Commit Hook (`.pre-commit-hooks.yaml`)**:
   - Executes `tfastdiff --wrapper . --exit-code` on wrapper directories to catch backward-incompatible changes before code is staged or pushed.

---

## 3. Independent Python Verification Oracle

The Independent Python Verification Oracle ([`scripts/oracle_verify.py`](file:///home/rsd/REPOS/terraform_ast/scripts/oracle_verify.py)) executes within an isolated, reproducible virtual environment (`.venv`) to perform decoupled verification of detection accuracy, confidence score distributions, and safety score calibration theorems.

### Architecture & Calibration Flow

```mermaid
flowchart TD
    subgraph ORACLE_ENV["1. Environment & Dependencies"]
        VENV[".venv Virtual Environment (make venv)"]
        REQ["requirements.txt (Standard Library Base)"]
        VENV --- REQ
    end

    subgraph SUITES["2. Dual-Track Oracle Verification Suites"]
        VENV --> FIXTURES["Suite 1: Local Golden Fixtures Oracle"]
        VENV --> UPSTREAM["Suite 2: Upstream Module Upgrade Oracle"]
        FIXTURES --> GKE_F["GKE v1 vs v2 (5 breaking, 2 warn, 2 feats, 2 hazards)"]
        FIXTURES --> EKS_F["EKS v19 vs v20 (5 breaking, 2 warn, 2 feats)"]
        FIXTURES --> AKS_F["AKS v1 vs v2 (5 breaking, 1 warn, 2 feats, 1 hazard)"]
        FIXTURES --> ID_F["Identity Zero-Diff (0 breaking, 0 warn => 100% Safety)"]
        FIXTURES --> WRAP_F["GKE Wrapper Analysis (0 issues => SAFE_TO_PIN)"]
        UPSTREAM --> EKS_LIVE["AWS EKS Upstream Paths (Patch, Minor, Major)"]
        UPSTREAM --> GKE_LIVE["Google GKE Upstream Paths (Patch, Minor, Major)"]
    end

    subgraph THEOREMS["3. Independent Scoring & Calibration Theorems"]
        GKE_F --> EVAL_S["Independent Safety Score Calculation: S"]
        EKS_F --> EVAL_S
        AKS_F --> EVAL_S
        ID_F --> EVAL_S
        WRAP_F --> EVAL_S
        EKS_LIVE --> EVAL_S
        GKE_LIVE --> EVAL_S
        EVAL_S --> TH1["Theorem 1 (Soundness): N_breaking == 0 <=> can_safely_update <=> S >= 70%"]
        EVAL_S --> TH2["Theorem 2 (Safety Bounds): Breaking >= 1 => S <= 65% < 70%"]
        EVAL_S --> TH3["Theorem 3 (Verdict Agreement): S = 100% <=> SAFE; S in [75%, 95%] <=> WARNINGS_ONLY"]
        EVAL_S --> TH4["Theorem 4 (Path Composition): S_path = min(S_i); can_safely_all <=> S_path >= 70%"]
        EVAL_S --> CONF_AUDIT["Confidence Calibration: 1 <= c_i <= 10; Breaking c_i >= 7; Mean >= 8.0"]
    end

    subgraph DECISION["4. Oracle Gate Verdict & Reporting"]
        TH1 --> REPORT["Comprehensive Evaluation Report"]
        TH2 --> REPORT
        TH3 --> REPORT
        TH4 --> REPORT
        CONF_AUDIT --> REPORT
        REPORT --> DECIDE{"All Invariants & Theorems Hold?"}
        DECIDE -->|Yes| PASS_ORACLE["GATE PASS: Exit 0 (Export oracle_report.json)"]
        DECIDE -->|No| FAIL_ORACLE["GATE FAIL: Exit 1 (Violations Logged)"]
    end
```

### Safety Score Formulation & Calibration Rules

The oracle computes an independent safety score $S \in [0.0, 100.0\%]$ for each transition:

$$S = \max\left(0.0, 100.0 - 35.0 \cdot N_{\text{breaking}} - \min(25.0, 5.0 \cdot N_{\text{warnings}}) - 15.0 \cdot N_{\text{hazards}}\right)$$

Where the scoring guarantees the following calibration theorems:
1. **Safety Soundness**: $N_{\text{breaking}} = 0 \iff \text{can\_safely\_update} = \text{true} \iff S \ge 70.0\%$.
2. **Definitive Breaking Penalty**: Any transition with $N_{\text{breaking}} \ge 1$ satisfies $S \le 65.0\% < 70.0\%$.
3. **Pure Backward Compatibility**: $N_{\text{breaking}} = 0 \land N_{\text{warnings}} = 0 \iff S = 100.0\% \iff \text{verdict} = \text{"SAFE"}$.
4. **Non-Breaking Warnings**: $N_{\text{breaking}} = 0 \land N_{\text{warnings}} > 0 \iff S \in [75.0\%, 95.0\%] \iff \text{verdict} = \text{"WARNINGS\_ONLY"}$.
5. **Multi-Step Upgrade Path Composition**: For an upgrade path composed of steps $(s_1, s_2, \dots, s_k)$, the composite path safety score is $S_{\text{path}} = \min_{1 \le i \le k} S(s_i)$, and $\text{can\_safely\_update\_all} = \text{true} \iff S_{\text{path}} \ge 70.0\%$.

### Execution Targets

```bash
# Set up shared Python virtual environment (.venv)
make venv

# Execute full independent oracle suite (builds binary, invokes oracle, exports JSON report)
make oracle

# Run oracle directly with custom parameters
.venv/bin/python3 scripts/oracle_verify.py \
  --samples 3 \
  --min-accuracy 100.0 \
  --min-confidence 8.0 \
  --min-safety 70.0 \
  --output oracle_report.json
```

