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
  --upstream https://github.com/terraform-aws-modules/terraform-eks.git \
  --new-version v20.0.0 \
  --old-version v19.21.0 \
  --format text \
  --exit-code
```

#### Flags:
- `--wrapper <dir>`: Path to local wrapper module root.
- `--upstream <dir|git-url|registry>`: Upstream module path, Git URL, or Terraform Registry identifier (e.g. `terraform-google-modules/kubernetes-engine/google`). Inferred from `module` block `source` if omitted. A source that resolves to a directory on disk is always treated as a local module, even when it has three or four path segments.
- `--new-version <tag>`: Target upstream Git version or Registry version to pin (e.g. `v30.0.0` or `30.0.0`).
- `--old-version <tag>`: Currently pinned upstream Git version (enables side-by-side upstream diff).
- `--policy <file>`: Optional path to a JSON policy file to enforce breaking-change, warning, downgrade, and max-bump constraints.
- `--rego-policy <file>`: Optional path to an Open Policy Agent / Rego policy document (`.rego`) evaluated against the audit result.
- `--taint <var>`: Trace input variable taint reachability from wrapper declarations through locals to downstream module inputs and outputs.
- `--blast-radius <dir>`: Optional root directory (e.g. `./environments`) to recursively scan for downstream consumer root modules affected by breaking changes.
- `--check-recreation`: Enables or disables state destruction and `ForceNew` hazard detection heuristics (default: `true`). Hazards are reported in every output format: an alert block in `text`/`markdown`, a `recreation_hazards` array in `json`, and `TF-RECREATION-HAZARD` SARIF results for code scanning.
- `--format <text|markdown|json|sarif>`: Output format (default: `text`). Use `sarif` for GitHub Code Scanning.
- `--output <file>`: Write report to destination file (written with permissions `0644`).
- `--svg-before <path>`: Write an SVG rendering of the before state address graph to this path (written with permissions `0600`).
- `--svg-after <path>`: Write an SVG rendering of the after state address graph to this path (written with permissions `0600`).
- `--svg-transition <path>`: Write an SVG rendering of the before/after state address transition graph to this path (written with permissions `0600`).
- `--exit-code`: Returns non-zero exit code (1) if breaking changes or policy violations are detected.
- `--fix-preview`: Emit a preview-only unified patch for removed arguments, note missing required inputs that `--fix` would inject, and preview synthesized `moved.tf` blocks without modifying files on disk.
- `--fix`: Apply fixes in-place to remove obsolete wrapper arguments, inject missing required upstream inputs with safe primitive placeholders (`string` `""`, `bool` `false`, `number` `0`; exits with code 2 if a complex required input has no safe placeholder), and synthesize `moved.tf` for unmigrated address shifts. Mutually exclusive with `--fix-preview`.
- `--golden-plan <file>`: Optional path to a Terraform plan JSON file (`terraform show -json <planfile>`). Correlates planned resource changes against detected `ForceNew` recreation hazards, address shifts, and replacement actions (`["delete", "create"]`). If omitted, automatically discovers candidate plans (`golden-plan.json`, `golden_plan.json`, `tfplan.json`, `plan.json`, `.terraform/tfplan.json`) in the wrapper directory.
- `--provider-schema <file>`: Optional path to a provider schema JSON file (`terraform providers schema -json`). Dynamically extracts `requires_replace` metadata across resources and nested blocks to detect ForceNew recreation hazards across any cloud provider.
- `--provider-schema-url <url>`: Optional absolute HTTP(S) URL to a document published in the same `terraform providers schema -json` format. Fetched through the registry client's SSRF guard, retry logic, and `--registry-timeout`, bounded to 32 MiB; only the `provider_schemas` block is used. Fresh responses are cached in `--cache-dir` with a 30-minute TTL. Mutually exclusive with `--provider-schema`. The public Terraform Registry does not publish schema JSON, so the URL must point at an endpoint or artifact store that does; a Terraform-generated file remains authoritative.
- `--cache-dir <dir>`: Optional path to an on-disk directory for persistent L2 AST caching, accelerating monorepo traversals and pre-commit checks.
- `--clear-cache`: Clears only cache files matching `<sha256>.json` in the `--cache-dir`.
- `--registry-timeout <duration>`: Timeout for Terraform Registry operations (default: `2m`).
- `--verbose`, `-v`: Enable verbose progress and diagnostic logging to stderr.
- `--version`: Print CLI version (`0.9.0`) and exit.

Example policy:

```json
{
  "block_breaking": true,
  "block_warnings": false,
  "block_downgrades": true,
  "max_bump": "MINOR"
}
```

`max_bump` accepts `PATCH`, `MINOR`, or `MAJOR`. Unspecified rules are disabled. A policy violation enforces a failing exit code even when `--exit-code` is not supplied.

#### Machine-Readable Output

`--format json` and `--format sarif` always emit a **single, valid document**. When `--blast-radius`
is used, the downstream consumer scan is embedded in the same document rather than concatenated onto
it:

- `json`: the wrapper audit fields stay at the top level and a `blast_radius` object is added.
- `sarif`: consumer modules that cannot be safely updated are added as `TF-BLAST-RADIUS-CONSUMER`
  results pointing at the consuming directory. Only rules that back an emitted result are advertised.

---

### Golden Plan State Recreation Verification (`--golden-plan`)

When upgrading module version pins in enterprise wrappers, subtle input changes may trigger unexpected recreation of existing state resources (e.g. databases, VPCs, Kubernetes clusters). `tfastdiff` bridges static AST analysis with Terraform execution plans by inspecting a golden plan or image to verify that upgrading module version pins does not introduce breaking recreation hazards:

```bash
# Verify wrapper upgrade against an explicit golden plan JSON
./bin/tfastdiff \
  --wrapper ./my-gke-wrapper \
  --upstream terraform-google-modules/kubernetes-engine/google \
  --new-version 30.0.0 \
  --golden-plan ./golden-plan.json \
  --exit-code
```

#### Autodiscovery:
If `--golden-plan` is omitted in wrapper mode, `tfastdiff` automatically searches the wrapper directory for:
1. `golden-plan.json`
2. `golden_plan.json`
3. `tfplan.json`
4. `plan.json`
5. `.terraform/golden-plan.json`
6. `.terraform/tfplan.json`

If a valid Terraform plan JSON is discovered, verification runs automatically. If any planned resource matches a `ForceNew` immutable attribute, an unmigrated address shift, or an explicit replacement action (`["delete", "create"]`), the safety verdict is revoked and the command exits with code 1 when `--exit-code` is supplied.

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
  --format sarif \
  --output results.sarif
```

```bash
# Compare versions directly from the Terraform Registry
./bin/tfastdiff \
  --old terraform-aws-modules/eks/aws \
  --new terraform-aws-modules/eks/aws \
  --old-version 19.21.0 \
  --new-version 20.0.0 \
  --format markdown
```

```bash
# Review each tagged release before moving across a major-version jump
./bin/tfastdiff \
  --old https://github.com/terraform-google-modules/terraform-google-kubernetes-engine.git \
  --new https://github.com/terraform-google-modules/terraform-google-kubernetes-engine.git \
  --old-version v29.0.0 \
  --new-version v31.0.0 \
  --upgrade-path \
  --format markdown \
  --exit-code
```

#### Flags:
- `--old <dir|git-url|registry>`: Old/current upstream module source (resolved and downloaded concurrently with `--new` when fetching from Git/Registry, reusing `--cache-dir` snapshots).
- `--new <dir|git-url|registry>`: New/target upstream module source. Reports, JSON output and SARIF artifact locations are labelled with this source.
- `--old-version <tag>`: Old Git tag or Registry release version.
- `--new-version <tag>`: New Git tag or Registry release version.
- `--check-recreation`: Evaluates immutable resource destruction hazards (default: `true`) and emits them in every output format.
- `--format <text|markdown|json|sarif>`: Output format (`text`, `markdown`, `json`, `sarif`). Markdown format renders complete GitHub PR tables including `### Resource Address Shifts`.
- `--output <file>`: Path to file destination.
- `--exit-code`: Exits with code 1 if breaking changes exist.
- `--policy <file>`: Applies the same JSON policy gates as wrapper audit mode.
- `--rego-policy <file>`: Optional path to an Open Policy Agent / Rego policy document (`.rego`) evaluated against comparison findings.
- `--provider-schema <file>`: Supply a provider schema JSON file (`terraform providers schema -json`) for dynamic ForceNew recreation detection across all providers.
- `--provider-schema-url <url>`: Fetch the same schema document from an HTTP(S) endpoint with the credential-free registry request path, retry budget, and bounded cache. Mutually exclusive with `--provider-schema`.
- `--cache-dir <dir>`: Optional path to an on-disk directory for persistent L2 AST caching.
- `--clear-cache`: Clears only cache files matching `<sha256>.json` in the `--cache-dir`.
- `--registry-timeout <duration>`: Timeout for Terraform Registry operations (default: `2m`).
- `--verbose`, `-v`: Enable verbose progress and diagnostic logging to stderr.
- `--version`: Print CLI version (`0.9.0`) and exit.
- `--upgrade-path`: Executes the multi-version upgrade plan executor (`pkg/planner`) across intermediate semantic-version Git tags (including monorepo `//subpath`-scoped tags) or Terraform Registry releases between `--old-version` and `--new-version`:
  - Prunes candidate versions to major `.0.0` boundaries and the latest patch of each `MAJOR.MINOR` series (`pruneToSafeHarbor`).
  - Prefetches intermediate versions concurrently using a bounded worker pool (`min(8, GOMAXPROCS)`) under the shared `--registry-timeout` budget.
  - Caches inspected version snapshots in `--cache-dir` keyed by `source@version` (`<sha256>.json`), so repeated CI runs execute with zero network fetches.
  - Inspects intermediate modules in parallel (`InspectModulesParallel`), evaluates prepared OPA/JSON policies across every step, and computes `recommended_version` (the highest contiguous safe-harbor version pin reachable before the first breaking step).

---

## Additional Analysis Controls

### State Drift (`drift`)

`drift` compares a recorded state snapshot against a live/provider snapshot. Both documents must
contain a `resources` array; blank or duplicate addresses are rejected so a malformed file cannot
be reported as `IN SYNC`.

```bash
tfastdiff drift \
  --state ./state-snapshot.json \
  --live ./live-snapshot.json
```

Snapshots use the shape written by `drift.SaveSnapshot`:

```json
{
  "resources": [
    {"address": "aws_s3_bucket.example", "attributes": {"acl": "private"}}
  ]
}
```

Without `--state`/`--live`, the command runs a legacy health check that compares each stored
golden baseline with itself; the CLI prints a note to stderr to make that limitation explicit.

### Crossplane Compositions (`crossplane`)

```bash
tfastdiff crossplane ./old-composition.yaml ./new-composition.yaml
```

The command reports added, removed, and changed composed resources and exits `1` when a change is
found.

### Crossplane Claims (`claim`)

```bash
tfastdiff claim ./old-claim.yaml ./new-claim.yaml
```

The command emits a JSON `ClaimDiff` covering composite type changes, composition
selection/secret wiring changes, and added, removed, or changed `spec.parameters` keys. It exits
`1` when drift is detected. Both `crossplane` and `claim` accept the shared `--hook`,
`--post-hook`, `--lock-file`, and `--lock-timeout` controls described below.

### Hooks, Webhooks, and Run Coordination

- `--hook <shell>` (repeatable): Runs a `pre-analysis` shell command via `sh -c` before analysis.
  Works on audit/compare modes and on the `drift`, `crossplane`, `encrypt`, `decrypt`, and
  `list-versions` subcommands.
- `--post-hook <shell>` (repeatable): Runs a `post-analysis` shell command after analysis. A failing
  post-hook prints an error but never overwrites a non-zero analysis verdict.
- `--webhook <url>` (repeatable): POSTs a JSON payload (`module_name`, `total_breaking`, `summary`)
  when an audit, fix, fix-preview, or upgrade-path run detects hazardous changes. Safe runs send
  nothing.
- `--webhook-timeout <duration>`: HTTP timeout per notification (default `10s`).
- `--lock-file <path>`: Acquires an advisory OS file lock (`flock`) for the duration of a run so
  concurrent CI jobs serialize. The kernel releases the lock if the process crashes, so stale
  locks cannot block later runs; the file itself is intentionally kept after release so contenders
  always lock the same inode. Owner metadata (PID, host, acquisition time) is written to the file
  for diagnostics and can be read with `lock.ReadInfo`. The lock is also applied to the
  `drift`, `crossplane`, `encrypt`, `decrypt`, and `list-versions` subcommands.
- `--lock-timeout <duration>`: How long to retry a contended `--lock-file` before failing
  (default `0` = fail immediately).

### Codemods, Graphs, and Streaming

- `--transform <file>`: Applies a JSON resource-rename specification during `--fix` or
  `--fix-preview`. It is rejected in audit-only modes rather than silently ignored:

  ```json
  {"transforms": [{"from": "aws_instance.old", "to": ["aws_instance.new"]}]}
  ```

  Single-target transforms produce `moved.tf`. A one-to-many split requires one concrete import
  ID per target; it produces `split.tf` containing a `removed` block (`destroy = false`) for the
  source plus an `import` block per target:

  ```json
  {"transforms": [{
    "from": "aws_instance.mono",
    "to": ["aws_instance.a", "aws_instance.b"],
    "ids": ["i-0aaa", "i-0bbb"]
  }]}
  ```

  Empty specs, missing addresses, and split specs without a matching `ids` list are rejected.
  IDs must identify the real cloud resources; they cannot be inferred from Terraform source.
- `--module-graph <path>`: Writes a Graphviz DOT dependency graph.
- `--topological-order <path>`: Writes one module address per line in dependency order
  (dependencies first). The order is deterministic and cycle detection is reported as an error.

For library callers, `diff.CompareStream` exposes difference items on a channel and
`diff.CompareStreamContext` adds cancellation. Findings are emitted through a shared sink as soon
as their comparison phase produces them, so no item slices are accumulated: memory stays bounded
regardless of the number of differences. The terminal `Done` event carries a summary report
(totals, verdict, safe pin, and address shifts) with empty item slices; callers that need the
complete `DiffReport` should use `diff.Compare`.

---

## Containerized Execution (Docker & DevSecOps)

A minimal distroless image (~5.7MB) is available for secure containerized environments. It enforces:
- **Non-root execution** as UID/GID `65532:65532`.
- **Zero OS shell access** (no `/bin/sh` or `/bin/bash` in runtime image).
- **Read-only root filesystem compatibility** (`--read-only`).
- **No privilege escalation** (`--security-opt no-new-privileges:true`).

### Building the Image

```bash
docker build -t tfastdiff:latest .
```

### Running Secure Scans

Mount module directories as read-only volumes (`:ro`) into the default `/workspace` directory:

```bash
# Audit wrapper module inside a hardened container
docker run --rm \
  --read-only \
  --security-opt no-new-privileges:true \
  -v $(pwd):/workspace:ro \
  tfastdiff:latest \
  --wrapper /workspace/my-gke-wrapper \
  --upstream https://github.com/terraform-google-modules/terraform-google-kubernetes-engine.git \
  --new-version v30.0.0 \
  --format markdown \
  --exit-code
```

```bash
# Generate SARIF report for GitHub Code Scanning
docker run --rm \
  --read-only \
  --security-opt no-new-privileges:true \
  -v $(pwd)/modules:/workspace/modules:ro \
  -v $(pwd)/reports:/reports \
  tfastdiff:latest \
  --old /workspace/modules/upstream-v1 \
  --new /workspace/modules/upstream-v2 \
  --format sarif \
  --output /reports/tfastdiff.sarif
```

---

## Exit Codes

| Exit Code | Meaning |
| :--- | :--- |
| `0` | Success: Module is safe to update, or only non-breaking changes/warnings were detected. |
| `1` | Failure: Breaking changes detected (when `--exit-code` is supplied), or syntax/operational error. |
| `2` | CLI usage error: Invalid or missing required command-line arguments. |

---

## CI/CD Pipeline Automation

### GitHub Actions Pull Request & Code Scanning Check

This workflow runs `tfastdiff` via the hardened Docker container, generates both a PR Markdown comment and a SARIF report, and automatically uploads findings to **GitHub Code Scanning**:

```yaml
name: Terraform Wrapper Compatibility & Security Audit

on:
  pull_request:
    paths:
      - 'modules/**'

jobs:
  audit:
    runs-on: ubuntu-latest
    permissions:
      contents: read
      pull-requests: write
      security-events: write

    steps:
      - name: Checkout Code
        uses: actions/checkout@v4

      - name: Build DevSecOps Container
        run: docker build -t tfastdiff:latest .

      - name: Run Wrapper Audit (Markdown & Exit Code Gate)
        run: |
          docker run --rm \
            --read-only \
            --security-opt no-new-privileges:true \
            -v ${{ github.workspace }}:/workspace:ro \
            -v ${{ runner.temp }}:/out \
            tfastdiff:latest \
            --wrapper /workspace/modules/gke-wrapper \
            --upstream https://github.com/terraform-google-modules/terraform-google-kubernetes-engine.git \
            --new-version v30.0.0 \
            --blast-radius /workspace/environments \
            --format markdown \
            --output /out/report.md \
            --exit-code

      - name: Generate SARIF Security Report
        if: always()
        run: |
          docker run --rm \
            --read-only \
            --security-opt no-new-privileges:true \
            -v ${{ github.workspace }}:/workspace:ro \
            -v ${{ runner.temp }}:/out \
            tfastdiff:latest \
            --wrapper /workspace/modules/gke-wrapper \
            --upstream https://github.com/terraform-google-modules/terraform-google-kubernetes-engine.git \
            --new-version v30.0.0 \
            --format sarif \
            --output /out/results.sarif

      - name: Upload SARIF to GitHub Code Scanning
        if: always()
        uses: github/codeql-action/upload-sarif@v3
        with:
          sarif_file: ${{ runner.temp }}/results.sarif

      - name: Post PR Summary Comment
        if: always()
        uses: actions/github-script@v7
        with:
          script: |
            const fs = require('fs');
            const reportPath = '${{ runner.temp }}/report.md';
            if (fs.existsSync(reportPath)) {
              const body = fs.readFileSync(reportPath, 'utf8');
              github.rest.issues.createComment({
                issue_number: context.issue.number,
                owner: context.repo.owner,
                repo: context.repo.repo,
                body: body
              });
            }
```

---

## Production Real-World Use Cases

### Use Case 1: Pre-Upgrade Breaking Change Audit for Enterprise Wrappers
Platform engineering teams maintain standardized Terraform wrapper modules (such as an internal GKE or EKS wrapper). Before bumping the upstream version pin, run `tfastdiff` to verify compatibility:

```bash
# Audit wrapper module against target Google GKE upstream release
tfastdiff \
  --wrapper ./modules/gke-wrapper \
  --upstream terraform-google-modules/kubernetes-engine/google \
  --old-version 29.0.0 \
  --new-version 30.0.0 \
  --format markdown \
  --exit-code
```
**Outcome**:
- Identifies removed variables passed by the wrapper.
- Highlights newly required upstream variables missing in the wrapper call.
- Flags output delegations referencing dropped upstream outputs.
- Recommends the exact version pinning constraint.

---

### Use Case 2: Monorepo Downstream Blast-Radius & Terragrunt Impact Analysis
When modifying or upgrading a shared module in an infrastructure monorepo, determine which environment stacks (`dev`, `staging`, `prod`) will break:

```bash
# Scan all environments and Terragrunt configurations invoking the wrapper
tfastdiff \
  --wrapper ./modules/eks-wrapper \
  --upstream terraform-aws-modules/eks/aws \
  --new-version 20.0.0 \
  --blast-radius ./environments \
  --format text
```
**Outcome**:
- Traverses all subdirectories in `./environments` for `.tf` and `terragrunt.hcl` files.
- Maps breaking changes to affected root modules.
- Assigns per-consumer risk classifications (`CRITICAL`, `WARNING`, `SAFE`).
- Emits incomplete expression warnings if dynamic Terragrunt expressions cannot be resolved statically.

---

### Use Case 3: Automated In-Place Codemod Refactoring (`--fix`)
When upstream modules drop deprecated arguments, manually updating wrapper configurations across multiple files is error-prone. Use `--fix` to automatically remove obsolete arguments in-place:

```bash
# First preview the changes safely without mutating files
tfastdiff \
  --wrapper ./modules/gke-wrapper \
  --upstream terraform-google-modules/kubernetes-engine/google \
  --new-version 30.0.0 \
  --fix-preview

# Apply the fixes directly in-place
tfastdiff \
  --wrapper ./modules/gke-wrapper \
  --upstream terraform-google-modules/kubernetes-engine/google \
  --new-version 30.0.0 \
  --fix
```
**Outcome**:
- Parses files with `hclwrite`, removing obsolete arguments from `module "..."` blocks while preserving comments and indentation.
- Injects newly required primitive inputs (`string`, `bool`, `number`) with safe placeholders, or exits with code 2 when a complex required input requires manual intervention.
- Synthesizes `moved.tf` migration blocks for unmigrated resource address shifts.

---

### Use Case 4: Dynamic Provider Schema Immutability & ForceNew Hazard Detection
Detect infrastructure recreation hazards where an input variable change triggers provider-level resource destruction:

```bash
# 1. Export provider schema JSON from initialized Terraform project
terraform providers schema -json > provider-schema.json

# 2. Run tfastdiff with dynamic schema inspection
tfastdiff \
  --wrapper ./modules/cloud-sql-wrapper \
  --upstream GoogleCloudPlatform/sql-db/google \
  --new-version 18.0.0 \
  --provider-schema provider-schema.json \
  --format markdown
```

If your platform publishes the exported document over HTTP(S), replace
`--provider-schema provider-schema.json` with
`--provider-schema-url https://artifacts.example.com/schemas/google-18.0.0.json`.

**Outcome**:
- Ingests `requires_replace` metadata across resource attributes.
- Alerts if attributes like database instance name or storage type trigger complete resource rebuilds and data destruction.

---

### Use Case 5: Multi-Version Upgrade Path & Safe-Harbor Pin Update Plan Execution
When planning a pin update across multiple major or minor versions (e.g. GKE `v25.0.0` to `v31.0.0`), run the upgrade-path plan executor with `--cache-dir` to prefetch intermediate releases concurrently, evaluate adjacent transitions, and determine the safest version pin:

```bash
# Step 1: Compute the multi-version upgrade path and safe-harbor pin recommendation
tfastdiff \
  --old https://github.com/terraform-google-modules/terraform-google-kubernetes-engine.git \
  --new https://github.com/terraform-google-modules/terraform-google-kubernetes-engine.git \
  --old-version v25.0.0 \
  --new-version v31.0.0 \
  --upgrade-path \
  --cache-dir ~/.cache/tfastdiff \
  --format markdown

# Step 2: Audit the wrapper against the recommended pin and verify against a golden plan
tfastdiff \
  --wrapper ./modules/gke-wrapper \
  --upstream https://github.com/terraform-google-modules/terraform-google-kubernetes-engine.git \
  --old-version v25.0.0 \
  --new-version v30.0.0 \
  --golden-plan ./golden-plan.json \
  --cache-dir ~/.cache/tfastdiff \
  --format markdown \
  --exit-code
```
**Outcome**:
- Discovers and prunes intermediate semantic versions (including monorepo `//subpath` tags), prefetching uncached releases in parallel across a bounded worker pool (`min(8, GOMAXPROCS)`).
- Persists `source@version` AST snapshots in `--cache-dir` so subsequent runs require zero network fetches.
- Identifies the highest contiguous "safe harbor" version pin (`recommended_version`) reachable without breaking changes.
- Verifies the wrapper pin update against both the upstream AST diff (`UpstreamDiff`) and the Terraform execution plan (`--golden-plan`) to confirm zero `ForceNew` resource replacements or unmigrated address shifts.

---

### Use Case 6: Accelerated Monorepo Scanning with Persistent L2 AST Caching
In large monorepos with hundreds of modules, accelerate repeated runs and pre-commit checks:

```bash
tfastdiff \
  --wrapper ./modules/vpc-wrapper \
  --upstream terraform-aws-modules/vpc/aws \
  --new-version 5.0.0 \
  --blast-radius ./live \
  --cache-dir ~/.cache/tfastdiff
```
**Outcome**:
- Reuses pre-parsed AST binary definitions for unchanged files keyed by SHA-256 fingerprint.
- Reduces full-repository scans from tens of seconds to sub-second runtimes.

---

### Use Case 7: IDE Language Server Protocol (LSP) Feedback
Launch the Language Server Protocol (LSP) daemon to get inline diagnostics and runnable quick-fix code actions directly inside VS Code, Neovim, or JetBrains IDEs:

```bash
# Launch LSP daemon over standard I/O (stdio)
tfastdiff daemon

# Alternatively, invoke via flag
tfastdiff --lsp
```
**Outcome**:
- Speaks JSON-RPC 2.0 over stdio, honouring `initialize`, `shutdown`, and `exit`.
- Publishes diagnostics on `textDocument/didOpen`, `didChange`, and `didSave`. Under full document
  sync the analysis uses the in-editor buffer, so a finding reflects what you are looking at rather
  than the last saved file.
- Offers quick fixes through `textDocument/codeAction` and `workspace/executeCommand`: write
  `moved {}` blocks, and inject missing required module inputs. Both take a single JSON object
  argument and mutate the workspace only when explicitly invoked.
- Reports diagnostics for deprecated module variables. Breaking-change and recreation analysis in
  the editor is roadmap item #43, not yet implemented.

---

### Use Case 8: Enterprise Policy Governance with Open Policy Agent (OPA / Rego)
Enforce fine-grained compliance and organizational upgrade policies:

```bash
tfastdiff \
  --wrapper ./modules/gke-wrapper \
  --upstream terraform-google-modules/kubernetes-engine/google \
  --new-version 30.0.0 \
  --rego-policy ./policies/upgrade_governance.rego \
  --format markdown
```
**Outcome**:
- Ingests structured diff and blast radius reports into OPA evaluation engine.
- Enforces user-defined `deny[msg]` and `violation[msg]` rules for multi-cloud platform governance.

