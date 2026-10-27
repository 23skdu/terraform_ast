# Testing Methodology & Quality Assurance

This repository enforces **100% statement coverage in every package** through a multi-tiered
testing strategy combining:
1. **Exhaustive Unit & Branch Coverage** — every package sits at 100%, gated in CI.
2. **Native Go Fuzz Targets** — one seeded target per untrusted-input parser, asserting real invariants.
3. **Terratest Integration Validation** — real `terraform init`/`validate`/`plan` runs on fixtures.
4. **Golden File / Golden Image Testing Methodology** — byte-exact regression artefacts.

A single command runs the whole gate:

```bash
./scripts/verify.sh        # gofmt, vet, unit tests, 100% coverage gate, fuzz smoke run
```

---

## 1. Multi-Tiered Testing Strategy

```
+-------------------------------------------------------------+
|        Fuzz Targets: Untrusted Parser Invariants            |
|  (ast, blastradius, codemod, registry, semverutil)          |
+-------------------------------------------------------------+
                               |
+-------------------------------------------------------------+
|        Unit Tests & Error Branch Coverage (100%)           |
|   (Every package, every success and failure branch)          |
+-------------------------------------------------------------+
                               |
+-------------------------------------------------------------+
|             AST Extraction & Compatibility Tier             |
|       (Inspects ASTs, analyzes wrappers & computes diffs)    |
+-------------------------------------------------------------+
                               |
+-------------------------------------------------------------+
|             Golden Image / Golden File Verification         |
|   (Compares outputs against canonical golden JSON & Markdown)|
+-------------------------------------------------------------+
                               |
+-------------------------------------------------------------+
|          DevSecOps & Container Security Validation          |
|    (Static linting, distroless non-root, read-only runtime) |
+-------------------------------------------------------------+
                               |
+-------------------------------------------------------------+
|               Terratest HCL Validation Tier                 |
|  (Initializes, validates and plans real Terraform modules)  |
+-------------------------------------------------------------+
```

---

## 2. Terratest Methodology

The terratest suite resides in `test/terratest/` and executes using `github.com/gruntwork-io/terratest/modules/terraform`.

### Realistic Fixtures (`test/fixtures/`)
- `upstream_gke_v1`: Baseline Google GKE upstream module (v29.0.0).
- `upstream_gke_v2`: Upstream Google GKE module (v30.0.0) containing removed inputs, newly required variables, type modifications, and new features.
- `gke_wrapper`: Realistic enterprise wrapper module wrapping GKE v1.
- `upstream_eks_v19`: Baseline AWS EKS upstream module (v19.0.0).
- `upstream_eks_v20`: Upstream AWS EKS module (v20.0.0) simulating the v19 to v20 major migration (access entries, removed auth configmaps).
- `eks_wrapper`: Realistic enterprise wrapper module wrapping EKS v19.
- `modern_constructs`: Terraform 1.7+ / OpenTofu fixture exercising `moved {}`, `removed {}`, `check {}`, `count`, `for_each`, `lifecycle` flags, and `backend` configuration.

---

## 2.5 Evidence-Based Confidence Scoring (`#100`)

Every reported `DifferenceItem` and `WrapperIssue` carries an evidence-calibrated `confidence_score` clamped to `[1, 10]`:

| Evidence Signal | Base / Delta | Resulting Score |
| :--- | :--- | :--- |
| Breaking structural symbol removal or incompatible type/requirement change | Base `8` (`Evidence = 1`) | `8/10` |
| Breaking parsed-literal comparison (`VariableDefault`, `ProviderSource`, `ProviderMajorUpgrade`, `StateBackendChange`, `Lifecycle`, `RemovedDestroy`) | Base `8` + literal/schema evidence (`Evidence = 2`) | `9/10` |
| Provider-schema-confirmed `requires_replace` + structural + literal evidence | Base `8` + `Evidence >= 3` | `10/10` |
| Warning (`VariableValidation`) | Base `6` + `Evidence` | `6/10` – `8/10` |
| Warning (`TerraformCore`, `ProviderRequirement`) | Base `7` + `Evidence` | `7/10` – `9/10` |
| Warning (`VariableType`, `OutputType`, `OutputValue`) | Base `8` + `Evidence` | `8/10` – `9/10` |

### Execution
Terratest invokes the native Terraform CLI engine (`terraform init` and `terraform validate`) directly against all 6 module fixtures:
```go
opts := &terraform.Options{
    TerraformDir: dir,
    NoColor:      true,
}
terraform.InitAndValidate(t, opts)
```
This guarantees that all test cases operate on real, syntactically valid Terraform configurations.

---

## 3. Golden Image / Golden File Methodology

Golden testing preserves canonical expected results in version-controlled artifact files (`test/golden/`).

### Canonical Artifacts
| Golden File | Target Verified |
| :--- | :--- |
| `gke_upstream_diff.golden.json` | GKE v29 to v30 upstream AST diff (JSON format) |
| `gke_upstream_diff.golden.md` | GKE v29 to v30 upstream AST diff (Markdown format) |
| `gke_wrapper_audit.golden.json` | GKE wrapper compatibility report (JSON format) |
| `gke_wrapper_audit.golden.md` | GKE wrapper compatibility report (Markdown format) |
| `eks_upstream_diff.golden.json` | EKS v19 to v20 upstream AST diff (JSON format) |
| `eks_upstream_diff.golden.md` | EKS v19 to v20 upstream AST diff (Markdown format) |
| `eks_wrapper_audit.golden.json` | EKS wrapper compatibility report (JSON format) |
| `eks_wrapper_audit.golden.md` | EKS wrapper compatibility report (Markdown format) |

### Verification Protocol
Tests serialize actual comparison results and assert byte-for-byte equality against the golden baseline using `report.AssertGoldenMatch`:
```go
report.AssertGoldenMatch(t, goldenPath, actualBytes, false)
```

### Updating Golden Files
When updating baseline behavior or schemas, regenerate golden files by running:
```bash
UPDATE_GOLDEN=true go test -v ./test/terratest
```

---

## 4. DevSecOps & Container Security Validation

The container test suite in `test/docker/` enforces compliance with cloud security baselines and container hardening best practices:

### Static DevSecOps Rules (`TestDockerfile_DevSecOpsStaticAnalysis`)
- **Multi-Stage Build**: Asserts separation of compile environment (`builder`) and runtime image.
- **Supply Chain Integrity**: Validates checksum verification (`go mod verify`).
- **Binary Hardening**: Asserts static CGO disabling (`CGO_ENABLED=0`), path disclosure prevention (`-trimpath`), and symbol/debug stripping (`-ldflags="-s -w"`).
- **Distroless Base**: Enforces `gcr.io/distroless/static-debian12:nonroot`.
- **Non-Root Execution**: Enforces `USER 65532:65532`.
- **OCI Metadata Labels**: Verifies `org.opencontainers.image.*` specifications.
- **Context Hygiene**: Validates `.dockerignore` exclusions (`.git`, `test/`, credentials, caches).

### Dynamic Runtime Security (`TestDockerfile_BuildAndRuntimeSecurity`)
- **Metadata Inspection**: Dynamically inspects built image configuration via `docker inspect` to verify UID/GID, working directory, and entrypoint.
- **Read-Only Root Filesystem Execution**: Runs analysis with `--read-only` rootfs and `--security-opt no-new-privileges:true`.
- **Exit Code Enforcement**: Verifies that breaking changes propagate as exit code 1 inside the container runtime.
- **Attack Surface Neutralization**: Directly confirms that no shell (`/bin/sh` or `/bin/bash`) exists within the container.

Run the container test suite:
```bash
go test -v ./test/docker
```

---

## 5. 100% Statement Coverage Verification

Every package is required to reach 100% statement coverage. CI fails the build if any package, or
the module as a whole, drops below it.

```bash
go test -covermode=atomic -coverprofile=coverage.out ./...
go tool cover -func=coverage.out            # total must read 100.0%
go tool cover -func=coverage.out | grep -v '100.0%'   # must print nothing
```

To generate a full HTML report:

```bash
go tool cover -html=coverage.out -o coverage.html
```

Defensive branches that cannot be reached from any input are removed during review rather than
excluded from the profile, so the number reflects real behaviour instead of measurement exclusions.

---

## 6. Fuzz Testing

Every parser that consumes untrusted third-party Terraform source has a native Go fuzz target.
Targets are seeded, so they execute during a plain `go test` run and count toward coverage; the
same targets can then be run as an open-ended campaign.

| Target | Package | Invariant asserted |
| :--- | :--- | :--- |
| `FuzzInspect` | `pkg/ast` | `Inspect` never panics; re-inspection is stable |
| `FuzzCheckTypeCompatibility` | `pkg/ast` | Compatible is never breaking; `any` widening/narrowing rules hold |
| `FuzzParseObjectSchema` | `pkg/ast` | Parsed attribute keys match their recorded names |
| `FuzzParseModernHCLBlocks` | `pkg/ast` | Modern HCL/OpenTofu blocks parse without panicking |
| `FuzzParseMovedLifecycleBackend` | `pkg/ast` | `moved`, `lifecycle`, `backend`, and `count`/`for_each` enrich cleanly |
| `FuzzDiscoverCallSites` | `pkg/blastradius` | Scanning never panics and always yields a complete call site |
| `FuzzTerragruntInputKey` | `pkg/blastradius` | Terragrunt keys resolve or cleanly fail |
| `FuzzDiscoverJSONCallSites` | `pkg/blastradius` | Terraform JSON scanning never panics |
| `FuzzPreviewRemovedInputs` | `pkg/codemod` | Preview mode never mutates the wrapper |
| `FuzzUnifiedReplacement` | `pkg/codemod` | Generated patches carry a valid unified diff header |
| `FuzzIsRegistrySource` | `pkg/registry` | Local paths and VCS addresses are never Registry sources |
| `FuzzParseRegistrySource` | `pkg/registry` | Registry addresses parse completely or are rejected |
| `FuzzNormalizeAndIsValid` | `pkg/semverutil` | Normalisation is idempotent and agrees with the semver library |
| `FuzzDetermineBumpType` | `pkg/semverutil` | Bump classification matches semver ordering |
| `FuzzSort` | `pkg/semverutil` | Sorting preserves every element |

Run one target as a campaign:

```bash
go test ./pkg/ast -run '^FuzzInspect$' -fuzz '^FuzzInspect$' -fuzztime 60s
```

Run every target for a short smoke window (this is what `scripts/verify.sh` and CI do):

```bash
./scripts/verify.sh 30
```

Any input discovered by a campaign is written to `testdata/fuzz/<Target>/` and replayed
automatically by `go test`, so regressions are captured as permanent regression seeds.
