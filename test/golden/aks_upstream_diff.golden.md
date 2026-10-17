# Terraform AST Compatibility Diff Report

- **Module**: `Azure/aks/azurerm`
- **Version Transition**: `v7.0.0` -> `v8.0.0` (`MAJOR`)
- **Verdict**: `BREAKING_CHANGES`

- **Safest Known Pin**: `v7.0.0` — Keep the currently analyzed version pinned until the breaking changes are resolved.

> [!CAUTION]
> **CANNOT SAFELY UPDATE: Detected 4 breaking change(s) between v7.0.0 and v8.0.0. Wrapper must be updated before pinning.**

### 🚨 Breaking Changes (4)

| Category | Name | Description | Confidence | Remediation |
| :--- | :--- | :--- | :--- | :--- |
| `Output` | `client_key` | Output 'client_key' was removed in upstream version | 9/10 | Remove or replace references to output 'client_key' in wrapper or downstream modules |
| `Variable` | `agents_type` | Input variable 'agents_type' was removed in upstream version | 9/10 | Remove variable 'agents_type' from wrapper or module callers before upgrading |
| `Variable` | `workload_identity_enabled` | Newly introduced variable 'workload_identity_enabled' is required and has no default value | 9/10 | Provide a required value for 'workload_identity_enabled' in wrapper module call |
| `VariableType` | `dns_prefix` | Type constraint for variable 'dns_prefix' changed: Type constraint changed incompatibly from 'string' to 'list(string)' | 9/10 | Update variable type definition in wrapper to 'list(string)' |

### ✨ New Features (2)

| Category | Name | Description | Confidence |
| :--- | :--- | :--- | :--- |
| `Output` | `oidc_issuer_url` | New output 'oidc_issuer_url' available in upstream | 9/10 |
| `Variable` | `oidc_issuer_enabled` | New optional variable 'oidc_issuer_enabled' available in upstream | 9/10 |

### ⚠️ Warnings (1)

| Category | Name | Description | Confidence | Remediation |
| :--- | :--- | :--- | :--- | :--- |
| `TerraformCore` | `required_version` | Terraform Core version constraint updated from '>= 1.3.0' to '>= 1.5.0' | 7/10 | Ensure execution environment meets new Terraform Core requirement |

