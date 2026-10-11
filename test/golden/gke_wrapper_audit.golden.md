# Terraform Wrapper Compatibility Audit

- **Wrapper Path**: `test/fixtures/gke_wrapper`
- **Target Module**: `gke`
- **Target Version**: `v30.0.0`
- **Recommended Pin**: `version = "30.0.0"`
- **Verdict**: `BREAKING_CHANGES_DETECTED`

> [!CAUTION]
> **CANNOT SAFELY PIN: Wrapper has 5 breaking issue(s) with upstream v30.0.0. Resolve remediation steps before pinning.**

### 🚨 Compatibility Issues (6)

| Category | Target | Description | Confidence | Remediation |
| :--- | :--- | :--- | :--- | :--- |
| `BrokenOutputDelegation` | `legacy_network_tag` | Wrapper output 'legacy_network_tag' references module.gke.legacy_network_tag, but output 'legacy_network_tag' was removed in target upstream v30.0.0 | 9/10 | Update or remove output "legacy_network_tag" in wrapper outputs.tf |
| `MissingRequiredInput` | `cluster_autoscaling_profile` | Target upstream v30.0.0 requires input 'cluster_autoscaling_profile' (no default), but wrapper call 'gke' does not provide it | 9/10 | Add argument 'cluster_autoscaling_profile = ...' to module "gke" block in wrapper |
| `MissingRequiredInput` | `grant_registry_access` | Target upstream v30.0.0 requires input 'grant_registry_access' (no default), but wrapper call 'gke' does not provide it | 9/10 | Add argument 'grant_registry_access = ...' to module "gke" block in wrapper |
| `PassedRemovedInput` | `ip_range_services` | Wrapper passes argument 'ip_range_services' to child module 'gke', but 'ip_range_services' is not an accepted input in target upstream v30.0.0 | 9/10 | Remove argument 'ip_range_services' from module "gke" block in wrapper |
| `TypeMismatch` | `ip_range_pods` | Wrapper variable 'ip_range_pods' type 'string' is incompatible with upstream expected type 'list(string)' | 8/10 | Update variable "ip_range_pods" in wrapper to type = list(string) |
| `TypeMismatch` | `zones` | Wrapper variable 'zones' type 'list(string)' differs from upstream 'set(string)': Type converted from list to set (Terraform automatically coerces list to set, but ordering is not preserved) | 8/10 | Align variable "zones" with upstream type |

### 💡 Available Upstream Features (1)

| Category | Target | Description | Confidence |
| :--- | :--- | :--- | :--- |
| `OptionalVariableAvailable` | `enable_cost_allocation` | New optional upstream parameter 'enable_cost_allocation' (type bool) can be exposed in wrapper | 9/10 |

