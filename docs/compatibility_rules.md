# Compatibility & Semantic Rules

This document details the classification taxonomy applied by `terraform_ast` when comparing Terraform modules.

---

## 1. Breaking Changes (`BREAKING`)

A breaking change is any modification that will prevent existing wrapper code or root modules from successfully running `terraform plan` or `terraform apply`, or that alters the type contract in a non-backwards-compatible manner.

### Removed Inputs
- **Condition**: An input variable defined in version $N$ no longer exists in version $N+1$.
- **Impact**: Terraform rejects unknown arguments passed to a child module:
  ```
  Error: Unsupported argument: An argument named "..." is not expected here.
  ```
- **Remediation**: The wrapper or calling module must remove the attribute from the `module` invocation block.

### Newly Required Inputs Without Defaults
- **Condition**: A variable is added in version $N+1$ with `required = true` and no `default`, OR an existing variable in version $N$ had a `default` value that was removed in version $N+1$.
- **Impact**: Terraform aborts execution:
  ```
  Error: Missing required argument: The argument "..." is required, but no definition was found.
  ```
- **Remediation**: The wrapper module must explicitly define and pass this argument to the child module.

### Incompatible Type Constraints
- **Condition**: The type constraint on an input variable changes to a type that cannot be coerced:
  - Primitive changes: `string` $\rightarrow$ `number` or `bool`.
  - Structure changes: `string` $\rightarrow$ `list(...)` or `object(...)`.
  - Narrowing: `any` $\rightarrow$ `string`.
  - Set to list: `set(string)` $\rightarrow$ `list(string)` (sets do not guarantee list index ordering).
- **Impact**: Terraform evaluation error:
  ```
  Error: Invalid value for variable: Incorrect attribute value type.
  ```
- **Remediation**: Align wrapper variable types and values with upstream expectations.

### Nullability Changes
- **Condition**: An existing input changes from `nullable = true` (Terraform's default) to `nullable = false`.
- **Impact**: Existing callers that provide null may fail validation or module evaluation.
- **Verdict**: Breaking when null is disallowed; changing from non-nullable to nullable is reported as a warning because module behavior may change.

### Variable Validation Rule Changes
- **Condition**: A variable's `validation` condition or error message changes.
- **Impact**: The module may begin rejecting values that previously worked, or stop rejecting invalid values.
- **Verdict**: `WARNING`, because static comparison cannot reliably determine whether an arbitrary expression became stricter or more permissive.

### Removed Outputs
- **Condition**: An output present in version $N$ is omitted in version $N+1$.
- **Impact**: Any wrapper module or downstream root module referencing `module.<name>.<output>` fails to evaluate:
  ```
  Error: Unsupported attribute: This object does not have an attribute named "...".
  ```
- **Remediation**: Update downstream references to use successor attributes or remove delegations.

### Incompatible Output Types & Value Expressions
- **Condition**: An output's type signature changes incompatibly (e.g. from `string` to `list(string)`), or its `value` expression changes alongside a type or `sensitive` transition (`OutputValue`).
- **Impact**: Downstream modules expecting a scalar receive a collection, or observe altered semantics alongside a contract shift.
- **Note**: When only the output `value` expression changes while its declared `type` and `sensitive` flags remain identical, it is reported as a `WARNING` (`OutputValue`).

### Changed Variable Defaults (`VariableDefault`)
- **Condition**: An optional input variable's `default` value changes between version $N$ and version $N+1$.
- **Impact**: Callers that omit the input silently receive a different value (such as a different region, disk size, or Kubernetes version).
- **Verdict**: `BREAKING` (`ConfidenceScore = 9/10`).

### Managed & Data Resource Removals and Provider Reassignments (`Resource`, `ResourceProvider`)
- **Condition**: A `resource` or `data` block is removed, changes resource type, or is reassigned to a different `provider` alias.
- **Impact**: Existing state instances are destroyed or fail provider resolution unless migrated.
- **Verdict**: `BREAKING`.

### Unmigrated Resource Address Shifts (`AddressShift`)
- **Condition**: An internal resource or module address moves (renamed, indexed, or relocated) without an upstream `moved {}` declaration covering the transition.
- **Impact**: Terraform treats the old address as destroyed and the new address as newly created, triggering unmigrated state destruction.
- **Verdict**: `BREAKING` (`Category: "AddressShift"`, `ConfidenceScore = 9/10`).

### Nested Child Module Call Transitions (`ModuleCall`, `ModuleCallSource`, `ModuleCallIndex`)
- **Condition**:
  - A child `module "..." {}` call is removed from an upstream module without a covering `moved {}` or soft `removed {}` block (`ModuleCall` → `BREAKING`).
  - An existing child `module` call changes its `source` or bumps its major `version` constraint (`ModuleCallSource` → `BREAKING`).
  - An existing child `module` call transitions between singleton, `count`, and `for_each` indexing modes (`ModuleCallIndex` → `BREAKING`).
- **Impact**: Removing or relocating a child module call destroys its entire state subtree during `terraform apply`.

### Instance Cardinality & Count / ForEach Destruction (`ResourceCount`, `ResourceForEach`)
- **Condition**:
  - Literal integer `count` reduction ($n_{\text{new}} < n_{\text{old}}$) or explicit `count = 0` (`ResourceCount` → `BREAKING`).
  - Static `for_each` key-set shrinkage (e.g. `toset(["a", "b"])` → `toset(["a"])`) (`ResourceForEach` → `BREAKING`), while key-set preservation or widening (e.g. `toset(["a", "b"])` → `toset(["b", "a", "c"])`) is reported as `WARNING`.
  - Dynamic `count` or `for_each` expression drift (`WARNING`, promoted to `BREAKING` if evaluated to empty or 0).
- **Impact**: Higher-indexed instances ($[n_{\text{new}} \dots n_{\text{old}}-1]$) or omitted keys are immediately destroyed in cloud state.

### Resource-Body ForceNew Attribute Modifications (`ResourceForceNew`)
- **Condition**: An attribute defined directly on a managed `resource` block changes value between versions, and the attribute is confirmed as `ForceNew` (`requires_replace`) by built-in cloud heuristics or dynamic provider schemas.
- **Impact**: In-place updates are rejected by the cloud provider, forcing resource destruction and recreation.
- **Verdict**: `BREAKING` (`Category: "ResourceForceNew"`, `ConfidenceScore = 10/10` with schema evidence).

### Destructive Protection Toggles & Triggers (`ResourceTriggers`, `DestructiveAttribute`)
- **Condition**:
  - `triggers`, `triggers_replace` (`terraform_data`), or `keepers` (`random_*`) attribute expressions change (`ResourceTriggers` → `BREAKING`).
  - Destruction safeguards are disabled (`deletion_protection = false` or `purge_protection_enabled = false`) (`DestructiveAttribute` → `BREAKING`).
  - Destruction accelerators are enabled (`force_destroy = true` or `skip_final_snapshot = true`) (`DestructiveAttribute` → `BREAKING`).
- **Verdict**: `BREAKING` (`ConfidenceScore = 9/10`).

### Lifecycle List Mutations & Checks (`Lifecycle`, `CheckBlockModified`, `ImportBlockModified`)
- **Condition**:
  - `lifecycle { prevent_destroy = true }` is removed (`Lifecycle` → `BREAKING`).
  - `lifecycle { replace_triggered_by = [...] }` is added or has targets expanded (`Lifecycle` → `BREAKING`).
  - `lifecycle { ignore_changes = [...] }` is narrowed, has elements removed, or transitions from `all` to a subset (`WARNING`, promoted to `BREAKING` if target attribute is `ForceNew`).
  - A destroy-time `provisioner` (`when = destroy`) is added (`Provisioner` → `BREAKING`).
  - A `check {}` assertion condition is altered or weakened (`CheckBlockModified` → `WARNING`).
  - An `import {}` block changes its target `id` for the same resource address (`ImportBlockModified` → `BREAKING`).
  - An `import {}` block changes its `provider` argument (`ImportBlockProvider` → `BREAKING`).
  - A `precondition {}` or `postcondition {}` block is modified or removed (`PreconditionModified`/`PostconditionModified` → `WARNING`, promoted to `BREAKING` when the resource has or references a `ForceNew` attribute).

### Module Call Version & Cardinality (`ModuleCallVersion`, `ModuleCallCount`, `ModuleCallForEach`)
- **Condition**:
  - A nested `module {}` call changes its `version` constraint in a major or `0.x`-minor breaking way (`ModuleCallVersion` → `BREAKING`).
  - A nested `module {}` call reduces `count` or shrinks static `for_each` keys (`ModuleCallCount`/`ModuleCallForEach` → `BREAKING`), while non-shrinking count/key-set updates emit `WARNING`.
- **Verdict**: `BREAKING` on shrinkage/major bump; `WARNING` on non-shrinking updates.

### Provider Configuration & State Backend Changes (`ProviderConfig`, `ProviderConfigAdded`, `ProviderConfigRemoved`, `StateBackendChange`)
- **Condition**:
  - `terraform { backend "..." { ... } }` or `terraform { cloud { ... } }` changes organization, workspace, backend type, or key attributes (`StateBackendChange` → `BREAKING`).
  - A `provider "..." {}` block is newly added (`ProviderConfigAdded` → `BREAKING`) or changes its `region`, `project`, `subscription_id`, or `assume_role` configuration (`ProviderConfig` → `BREAKING`), while removing an upstream `provider {}` block (aligning with Terraform best practices) emits `ProviderConfigRemoved` (`WARNING`).
- **Impact**: Modifies target deployment cloud tenant or state storage destination, risking cross-account state or resource corruption.

### Local Value & Data Selector Drift (`LocalValue`, `DataSelector`)
- **Condition**: A `locals {}` expression or `data` source selector (`filter`, `most_recent`, `owners`) changes value and is referenced by an attribute of a managed resource.
- **Verdict**: `WARNING`, promoted to `BREAKING` if the downstream resource attribute is `ForceNew`.

### Modern HCL & OpenTofu Block Transitions (`RemovedDestroy`, `StateMigrationRemoved`, `Function`, `Encryption`)
- **Condition**:
  - A new `removed {}` block is introduced with `destroy = true` (Terraform 1.7+ default) (`RemovedDestroy` → `BREAKING`).
  - A soft `removed { lifecycle { destroy = false } }` block is newly added (`SoftRemovedBlock` → `NEW_FEATURE`).
  - An upstream `moved {}` block is removed (`StateMigrationRemoved` → `BREAKING`), or newly added (`MovedBlock` → `NEW_FEATURE`).
  - A custom `function {}` or OpenTofu `encryption {}` block is removed (`BREAKING`).
  - A `check {}`, `import {}`, or `removed {}` block is removed (`WARNING`).

### Multi-Step Upgrade Path & Safe-Harbor Pin Selection (`pkg/planner`)
- **Condition**: When evaluating a multi-version upgrade path $v_0 \to v_1 \to \dots \to v_k$ via `--upgrade-path`, each adjacent step $(v_i, v_{i+1})$ is evaluated independently by `diff.Compare`.
- **Safe-Harbor Pin Rule**: The plan executor advances `RecommendedVersion` through every contiguous step with `CanSafelyUpdate == true` (`TotalBreaking == 0`) and stops at $v_i$ of the first step $(v_i \to v_{i+1})$ where `TotalBreaking > 0`. If all steps are safe, `RecommendedVersion == TargetVersion` and `CanSafelyUpdateAll == true`.

---

## 2. Downgrade Hazards (`DOWNGRADE`)

- **Condition**: The target version is lower than the currently pinned version according to semver (`semver.Compare(to, from) < 0`).
- **Impact**: Downgrading infrastructure modules often causes Terraform state mismatches, forced resource recreation, or unsupported provider configurations.
- **Verdict**: Flagged with `DOWNGRADE` verdict and `CanSafelyUpdate = false`.

---

## 3. New Features (`NEW_FEATURE`)

Additions that expand capabilities without breaking existing configurations:

### New Optional Variables
- **Condition**: A variable is introduced in version $N+1$ with a default value (`has_default = true`).
- **Opportunity**: Callers can choose to adopt the parameter if needed, but existing invocations remain valid.

### New Outputs
- **Condition**: An output is added in version $N+1$.
- **Opportunity**: Wrappers can expose this new output to downstream modules.

---

## 4. Warnings & Non-Breaking Modifications (`WARNING`)

Subtle behavioral shifts that do not break the HCL parser but may require operational awareness:

### Widened Type Constraints
- **Example**: `list(string)` $\rightarrow$ `set(string)`.
- **Note**: Terraform automatically coerces lists to sets when passed as inputs; however, element ordering is no longer preserved.

### Sensitive Transitions
- **Condition**: A variable or output has `sensitive = true` added.
- **Note**: Values will be masked in CLI output and may require `nonsensitive()` functions if used in non-sensitive downstream contexts.

### Deprecation Notices
- **Condition**: A variable or output has a `deprecated = "..."` attribute populated.
- **Note**: Terraform emits deprecation warnings; planned migration to recommended alternatives is advised before future major releases.

### Provider & Core Requirement Shifts
- **Condition**: Upstream updates or removes minimum Terraform core version (`required_version`, category `TerraformCore`) or updates provider constraint boundaries.
- **Provider Major Upgrade (`ProviderMajorUpgrade`)**: A major version jump in a required provider constraint (e.g. `~> 4.0` → `~> 6.0`) is classified as `BREAKING` (`ConfidenceScore = 9/10`) because provider major upgrades routinely require state and schema migrations.
- **Aliases & Sources**: A newly declared `configuration_aliases` entry (`ProviderConfigurationAlias`) or provider `source` change (`ProviderSource`) is treated as `BREAKING`.

### Evidence-Based Confidence Scores
- Every diff and wrapper finding includes a `confidence_score` clamped to `[1, 10]` in machine-readable reports and human-readable output.
- Scores are computed from concrete AST and schema evidence rather than fixed category constants:
  - `10/10`: Provider-schema-confirmed `requires_replace` combined with structural and literal evidence (`Evidence >= 3`).
  - `9/10`: Parsed-literal value comparisons (`VariableDefault`, `ProviderMajorUpgrade`, `StateBackendChange`, `Lifecycle`, `RemovedDestroy`, `StateMigrationRemoved`) and wrapper symbol mismatches.
  - `8/10`: Structural declaration removals/additions and type compatibility judgments (`Variable`, `Output`, `VariableType`, `VariableRequirement`, `ProviderSource`).
  - `7/10`: Non-major `ProviderRequirement` and `TerraformCore` constraint updates.
  - `6/10` – `8/10`: `VariableValidation` expression changes, scaled by available evidence.

---

## 5. State Destruction & ForceNew Hazards (`HAZARD`)

Modifications that evaluate cleanly in static HCL but trigger cloud infrastructure destruction and recreation cycles during `terraform apply`:

### Immutable Cluster & VPC Identifiers
- **Condition**: Modifications affecting immutable resource attributes:
  - **Google GKE**: `name`, `project_id`, `location`, `regional`, `ip_range_pods`, `ip_range_services`.
  - **AWS EKS**: `cluster_name`, `vpc_id`, `subnets`.
- **Impact**: Terraform cloud providers mark the resource as `(forces new resource)`, triggering teardown of production clusters and control planes.
- **Remediation**: Use Terraform `moved {}` blocks, state manipulation scripts (`terraform state mv`), or blue/green cluster provisioning rather than in-place module version bumps.

---

## 6. Deep Structural Object Schema Rules (`OBJECT_SCHEMA`)

When comparing input variables or outputs typed as complex structural objects (`object({ ... })`):

### Removed Object Attributes
- **Condition**: An object attribute required by downstream configurations is removed from the upstream object definition.
- **Verdict**: `BREAKING`. Callers passing the removed attribute or accessing it from outputs fail validation.

### Newly Required Object Attributes
- **Condition**: An attribute within `object({ ... })` is introduced without an optional marker (`optional(...)`).
- **Verdict**: `BREAKING`. Existing callers passing object literals without this attribute encounter immediate schema errors.

### Optional Attribute Introduction
- **Condition**: An attribute is added with `optional(type, default)`.
- **Verdict**: `NEW_FEATURE` (backwards-compatible expansion).
