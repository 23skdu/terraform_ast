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

### Incompatible Output Types
- **Condition**: An output's type signature changes incompatibly (e.g. from `string` to `list(string)`).
- **Impact**: Downstream modules expecting a scalar receive a collection, failing interpolations.

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
- **Condition**: Upstream updates minimum Terraform core version (e.g. `>= 1.3.0` to `>= 1.5.0`) or updates provider constraint boundaries.
- A newly declared `configuration_aliases` entry or provider source change is treated as breaking because callers may need different provider mappings.

### Confidence Scores
- Every diff and wrapper finding includes a `confidence_score` from 1 to 10 in machine-readable reports and human-readable output.
- Scores estimate certainty that the analyzer's extracted declaration change is real, not the operational impact of the finding.
- Exact input/output API changes are scored 9/10; type compatibility judgments are 8/10; provider and Terraform Core constraint shifts are 7/10 because the effective result depends on the consumer's selected versions; arbitrary validation-rule changes are scored 6/10 because static analysis cannot determine their full acceptance set.

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
