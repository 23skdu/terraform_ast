# CLI Usage & Integration Guide

`tfastdiff` is the command-line utility for `terraform_ast`. It supports auditing wrappers against upstream modules and comparing standalone module versions.

## Building the Binary

Compile `tfastdiff` with standard Go tooling:

```bash
go build -o bin/tfastdiff cmd/tfastdiff/main.go
```

---

## Command Modes

### 1. Wrapper Audit & Pinning Mode (`--wrapper`)

Audits a local wrapper module (such as a Google GKE or AWS EKS wrapper) against an upstream module target version:

```bash
# Audit wrapper against a local upstream directory
./bin/tfastdiff \
  --wrapper ./my-gke-wrapper \
  --upstream ./upstream-gke-v30 \
  --new-version v30.0.0 \
  --format markdown \
  --exit-code
```

```bash
# Audit wrapper directly against a remote upstream Git repository
./bin/tfastdiff \
  --wrapper ./my-eks-wrapper \
  --upstream https://github.com/terraform-aws-modules/terraform-aws-eks.git \
  --new-version v20.0.0 \
  --old-version v19.21.0 \
  --format text \
  --exit-code
```

#### Flags:
- `--wrapper <dir>`: Path to local wrapper module root.
- `--upstream <dir|git-url>`: Upstream module path or Git URL (inferred from `module` block `source` if omitted).
- `--new-version <tag>`: Target upstream Git version or tag to pin.
- `--old-version <tag>`: Currently pinned upstream Git version (enables side-by-side upstream diff).
- `--format <text|markdown|json>`: Output format (default: `text`).
- `--output <file>`: Write report to destination file.
- `--exit-code`: Returns non-zero exit code (1) if breaking changes are detected.

---

### 2. Upstream Module Version Comparison Mode (`--old` & `--new`)

Compares two versions of an upstream module directly:

```bash
# Compare two local module directories
./bin/tfastdiff \
  --old ./modules/gke-v29 \
  --new ./modules/gke-v30 \
  --old-version v29.0.0 \
  --new-version v30.0.0 \
  --format text
```

```bash
# Compare two tags from a remote Git repository
./bin/tfastdiff \
  --old https://github.com/terraform-google-modules/terraform-google-kubernetes-engine.git \
  --new https://github.com/terraform-google-modules/terraform-google-kubernetes-engine.git \
  --old-version v29.0.0 \
  --new-version v30.0.0 \
  --format markdown \
  --output diff_report.md
```

#### Flags:
- `--old <dir|git-url>`: Old/current upstream module source.
- `--new <dir|git-url>`: New/target upstream module source.
- `--old-version <tag>`: Old Git tag or reference.
- `--new-version <tag>`: New Git tag or reference.
- `--format <text|markdown|json>`: Output format (`text`, `markdown`, `json`).
- `--output <file>`: Path to file destination.
- `--exit-code`: Exits with code 1 if breaking changes exist.

---

## Exit Codes

| Exit Code | Meaning |
| :--- | :--- |
| `0` | Success: Module is safe to update, or only non-breaking changes/warnings were detected. |
| `1` | Failure: Breaking changes detected (when `--exit-code` is supplied), or syntax/operational error. |
| `2` | CLI usage error: Invalid or missing required command-line arguments. |

---

## CI/CD Pipeline Automation

### GitHub Actions Pull Request Check

```yaml
name: Terraform Wrapper Compatibility Check

on:
  pull_request:
    paths:
      - 'modules/gke-wrapper/**'

jobs:
  check-wrapper:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - name: Set up Go
        uses: actions/setup-go@v5
        with:
          go-version: '1.27'

      - name: Build tfastdiff
        run: go build -o /tmp/tfastdiff ./cmd/tfastdiff/main.go

      - name: Run Wrapper Compatibility Audit
        id: audit
        run: |
          /tmp/tfastdiff \
            --wrapper ./modules/gke-wrapper \
            --upstream https://github.com/terraform-google-modules/terraform-google-kubernetes-engine.git \
            --new-version v30.0.0 \
            --format markdown \
            --output report.md \
            --exit-code

      - name: Post PR Comment
        if: always()
        uses: actions/github-script@v7
        with:
          script: |
            const fs = require('fs');
            if (fs.existsSync('report.md')) {
              const body = fs.readFileSync('report.md', 'utf8');
              github.rest.issues.createComment({
                issue_number: context.issue.number,
                owner: context.repo.owner,
                repo: context.repo.repo,
                body: body
              });
            }
```
