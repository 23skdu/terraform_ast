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
  type        = list(string)
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

variable "ip_range_pods" {
  type        = string
  description = "The secondary subnet ip range to use for pods"
}

variable "ip_range_services" {
  type        = string
  description = "The secondary subnet ip range to use for services"
}

variable "grant_registry_access" {
  type        = bool
  description = "Grants artifact registry access"
  default     = true
}
