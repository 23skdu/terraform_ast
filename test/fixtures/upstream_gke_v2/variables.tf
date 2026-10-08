variable "project_id" {
  type        = string
  description = "The project ID to host the cluster in"
}

variable "name" {
  type        = string
  description = "The name of the cluster"
}

variable "regional" {
  type        = bool
  description = "Whether is a regional cluster"
  default     = true
}

variable "region" {
  type        = string
  description = "The region to host the cluster in"
  default     = "us-central1"
}

variable "zones" {
  type        = set(string)
  description = "The zones to host the cluster in"
  default     = []
}

variable "network" {
  type        = string
  description = "The VPC network to host the cluster in"
}

variable "subnetwork" {
  type        = string
  description = "The subnetwork to host the cluster in"
}

# Type constraint changed: string -> list(string)
variable "ip_range_pods" {
  type        = list(string)
  description = "The secondary subnet ip range(s) to use for pods"
}

# Was optional (default true), now required without default
variable "grant_registry_access" {
  type        = bool
  description = "Grants artifact registry access"
}

# Newly introduced required variable without default
variable "cluster_autoscaling_profile" {
  type        = string
  description = "Autoscaling profile for cluster"
}

# New feature: optional variable with default
variable "enable_cost_allocation" {
  type        = bool
  description = "Enables GKE cost allocation feature"
  default     = false
}
