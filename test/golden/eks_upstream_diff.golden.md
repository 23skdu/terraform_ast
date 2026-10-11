# Terraform AST Compatibility Diff Report

- **Module**: `terraform-aws-modules/eks/aws`
- **Version Transition**: `v19.0.0` -> `v20.0.0` (`MAJOR`)
- **Verdict**: `BREAKING_CHANGES`

> [!CAUTION]
> **CANNOT SAFELY UPDATE: Detected 4 breaking change(s) between v19.0.0 and v20.0.0. Wrapper must be updated before pinning.**

### 🚨 Breaking Changes (4)

| Category | Name | Description | Confidence | Remediation |
| :--- | :--- | :--- | :--- | :--- |
| `Output` | `aws_auth_configmap_yaml` | Output 'aws_auth_configmap_yaml' was removed in upstream version | 9/10 | Remove or replace references to output 'aws_auth_configmap_yaml' in wrapper or downstream modules |
| `Variable` | `authentication_mode` | Newly introduced variable 'authentication_mode' is required and has no default value | 9/10 | Provide a required value for 'authentication_mode' in wrapper module call |
| `Variable` | `legacy_security_group_id` | Input variable 'legacy_security_group_id' was removed in upstream version | 9/10 | Remove variable 'legacy_security_group_id' from wrapper or module callers before upgrading |
| `Variable` | `manage_aws_auth_configmap` | Input variable 'manage_aws_auth_configmap' was removed in upstream version | 9/10 | Remove variable 'manage_aws_auth_configmap' from wrapper or module callers before upgrading |

### ✨ New Features (2)

| Category | Name | Description | Confidence |
| :--- | :--- | :--- | :--- |
| `Output` | `access_entries` | New output 'access_entries' available in upstream | 9/10 |
| `Variable` | `enable_cluster_creator_admin_permissions` | New optional variable 'enable_cluster_creator_admin_permissions' available in upstream | 9/10 |

### ⚠️ Warnings (2)

| Category | Name | Description | Confidence | Remediation |
| :--- | :--- | :--- | :--- | :--- |
| `TerraformCore` | `required_version` | Terraform Core version constraint updated from '>= 1.0.0' to '>= 1.3.0' | 7/10 | Ensure execution environment meets new Terraform Core requirement |
| `VariableType` | `subnet_ids` | Type constraint for variable 'subnet_ids' widened: Type converted from list to set (Terraform automatically coerces list to set, but ordering is not preserved) | 8/10 | Verify variable values remain valid under widened type |

