# Terraform AST Compatibility Diff Report

- **Module**: `terraform-google-modules/kubernetes-engine/google`
- **Version Transition**: `v29.0.0` -> `v30.0.0` (`MAJOR`)
- **Verdict**: `BREAKING_CHANGES`

- **Safest Known Pin**: `v29.0.0` — Keep the currently analyzed version pinned until the breaking changes are resolved.

> [!CAUTION]
> **CANNOT SAFELY UPDATE: Detected 5 breaking change(s) between v29.0.0 and v30.0.0. Wrapper must be updated before pinning.**

### 🚨 Breaking Changes (5)

| Category | Name | Description | Confidence | Remediation |
| :--- | :--- | :--- | :--- | :--- |
| `Output` | `legacy_network_tag` | Output 'legacy_network_tag' was removed in upstream version | 9/10 | Remove or replace references to output 'legacy_network_tag' in wrapper or downstream modules |
| `Variable` | `cluster_autoscaling_profile` | Newly introduced variable 'cluster_autoscaling_profile' is required and has no default value | 9/10 | Provide a required value for 'cluster_autoscaling_profile' in wrapper module call |
| `Variable` | `ip_range_services` | Input variable 'ip_range_services' was removed in upstream version | 9/10 | Remove variable 'ip_range_services' from wrapper or module callers before upgrading |
| `VariableRequirement` | `grant_registry_access` | Variable 'grant_registry_access' was optional (had default) but is now required with no default | 9/10 | Explicitly supply a value for 'grant_registry_access' in the wrapper module call |
| `VariableType` | `ip_range_pods` | Type constraint for variable 'ip_range_pods' changed: Type constraint changed incompatibly from 'string' to 'list(string)' | 9/10 | Update variable type definition in wrapper to 'list(string)' |

### ✨ New Features (2)

| Category | Name | Description | Confidence |
| :--- | :--- | :--- | :--- |
| `Output` | `cost_allocation_status` | New output 'cost_allocation_status' available in upstream | 9/10 |
| `Variable` | `enable_cost_allocation` | New optional variable 'enable_cost_allocation' available in upstream | 9/10 |

### ⚠️ Warnings (2)

| Category | Name | Description | Confidence | Remediation |
| :--- | :--- | :--- | :--- | :--- |
| `TerraformCore` | `required_version` | Terraform Core version constraint updated from '>= 1.3.0' to '>= 1.5.0' | 7/10 | Ensure execution environment meets new Terraform Core requirement |
| `VariableType` | `zones` | Type constraint for variable 'zones' widened: Type converted from list to set (Terraform automatically coerces list to set, but ordering is not preserved) | 8/10 | Verify variable values remain valid under widened type |

