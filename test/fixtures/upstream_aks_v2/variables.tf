variable "cluster_name" {
  type        = string
  description = "The name of the Managed Kubernetes Cluster"
}

variable "resource_group_name" {
  type        = string
  description = "The name of the resource group"
}

variable "location" {
  type        = string
  description = "The Azure region"
  default     = "eastus"
}

# Type constraint changed: string -> list(string)
variable "dns_prefix" {
  type        = list(string)
  description = "DNS prefixes specified when creating the managed cluster"
}

# agents_type removed (removed input)

variable "os_disk_size_gb" {
  type        = number
  description = "Disk size of nodes in GB"
  default     = 100
}

# Newly introduced required variable without default
variable "workload_identity_enabled" {
  type        = bool
  description = "Whether to enable Azure AD Workload Identity"
}

# New feature: optional variable with default
variable "oidc_issuer_enabled" {
  type        = bool
  description = "Enable or Disable the OIDC issuer URL"
  default     = true
}
