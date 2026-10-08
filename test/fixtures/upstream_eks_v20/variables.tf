variable "cluster_name" {
  type        = string
  description = "Name of the EKS cluster"
}

variable "cluster_version" {
  type        = string
  description = "Kubernetes <major>.<minor> version to use for the EKS cluster"
  default     = "1.30"
}

variable "vpc_id" {
  type        = string
  description = "ID of the VPC where the cluster security group will be provisioned"
}

# Type constraint changed: list(string) -> set(string)
variable "subnet_ids" {
  type        = set(string)
  description = "A set of subnet IDs where the EKS cluster and nodes will be hosted"
}

variable "cluster_endpoint_public_access" {
  type        = bool
  description = "Indicates whether or not the Amazon EKS public API server endpoint is enabled"
  default     = true
}

# Newly required input without default (access entries migration in v20)
variable "authentication_mode" {
  type        = string
  description = "The authentication mode for the cluster (API, API_AND_CONFIG_MAP, or CONFIG_MAP)"
}

# New feature: optional input with default
variable "enable_cluster_creator_admin_permissions" {
  type        = bool
  description = "Indicates whether to add the cluster creator as an administrator via access entry"
  default     = true
}
