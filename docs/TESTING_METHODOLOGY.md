# Testing Methodology & Quality Assurance

This repository maintains **100% test statement coverage** across all packages through a multi-tiered testing strategy combining:
1. **Terratest Integration Validation**
2. **Golden File / Golden Image Testing Methodology**
3. **Exhaustive Unit & Branch Coverage**

---

## 1. Multi-Tiered Testing Strategy

```
+-------------------------------------------------------------+
|               Terratest HCL Validation Tier                 |
|  (Initializes & validates real Terraform modules with HCL)   |
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
|               Unit Tests & Error Branch Coverage            |
|       (Guarantees 100% branch and statement coverage)       |
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

Run all test suites and verify coverage:

```bash
go test -cover ./...
```

To generate a full HTML report:

```bash
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out -o coverage.html
```
