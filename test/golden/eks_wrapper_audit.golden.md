# Terraform Wrapper Compatibility Audit

- **Wrapper Path**: `test/fixtures/eks_wrapper`
- **Target Module**: `eks`
- **Target Version**: `v20.0.0`
- **Recommended Pin**: `version = "20.0.0"`
- **Safest Known Pin**: `v19.0.0` — Keep the currently pinned upstream version until the wrapper compatibility issues are resolved.
- **Verdict**: `BREAKING_CHANGES_DETECTED`

> [!CAUTION]
> **CANNOT SAFELY PIN: Wrapper has 4 breaking issue(s) with upstream v20.0.0. Resolve remediation steps before pinning.**

### Compatibility Issues (5)

| Category | Target | Description | Confidence | Remediation |
| :--- | :--- | :--- | :--- | :--- |
| `BrokenOutputDelegation` | `aws_auth` | Wrapper output 'aws_auth' references module.eks.aws_auth_configmap_yaml, but output 'aws_auth_configmap_yaml' was removed in target upstream v20.0.0 | 9/10 | Update or remove output "aws_auth" in wrapper outputs.tf |
| `MissingRequiredInput` | `authentication_mode` | Target upstream v20.0.0 requires input 'authentication_mode' (no default), but wrapper call 'eks' does not provide it | 9/10 | Add argument 'authentication_mode = ...' to module "eks" block in wrapper |
| `PassedRemovedInput` | `legacy_security_group_id` | Wrapper passes argument 'legacy_security_group_id' to child module 'eks', but 'legacy_security_group_id' is not an accepted input in target upstream v20.0.0 | 9/10 | Remove argument 'legacy_security_group_id' from module "eks" block in wrapper |
| `PassedRemovedInput` | `manage_aws_auth_configmap` | Wrapper passes argument 'manage_aws_auth_configmap' to child module 'eks', but 'manage_aws_auth_configmap' is not an accepted input in target upstream v20.0.0 | 9/10 | Remove argument 'manage_aws_auth_configmap' from module "eks" block in wrapper |
| `TypeMismatch` | `subnet_ids` | Wrapper variable 'subnet_ids' type 'list(string)' differs from upstream 'set(string)': Type converted from list to set (Terraform automatically coerces list to set, but ordering is not preserved) | 8/10 | Align variable "subnet_ids" with upstream type |

### Available Upstream Features (1)

| Category | Target | Description | Confidence |
| :--- | :--- | :--- | :--- |
| `OptionalVariableAvailable` | `enable_cluster_creator_admin_permissions` | New optional upstream parameter 'enable_cluster_creator_admin_permissions' (type bool) can be exposed in wrapper | 9/10 |

### Upstream Breaking Changes (5)

| Category | Name | Description | Confidence | Remediation |
| :--- | :--- | :--- | :--- | :--- |
| `Output` | `aws_auth_configmap_yaml` | Output 'aws_auth_configmap_yaml' was removed in upstream version | 9/10 | Remove or replace references to output 'aws_auth_configmap_yaml' in wrapper or downstream modules |
| `Variable` | `authentication_mode` | Newly introduced variable 'authentication_mode' is required and has no default value | 9/10 | Provide a required value for 'authentication_mode' in wrapper module call |
| `Variable` | `legacy_security_group_id` | Input variable 'legacy_security_group_id' was removed in upstream version | 9/10 | Remove variable 'legacy_security_group_id' from wrapper or module callers before upgrading |
| `Variable` | `manage_aws_auth_configmap` | Input variable 'manage_aws_auth_configmap' was removed in upstream version | 9/10 | Remove variable 'manage_aws_auth_configmap' from wrapper or module callers before upgrading |
| `VariableDefault` | `cluster_version` | Default value for variable 'cluster_version' changed | 9/10 | Review callers that omit 'cluster_version'; the default no longer matches |

