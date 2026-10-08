variable "project_id" {
  type        = string
  description = "GCP Project ID"
}

variable "name" {
  type        = string
  description = "GKE cluster name"
  default     = "wrapper-cluster"
}

variable "regional" {
  type        = bool
  description = "Regional cluster boolean"
  default     = true
}

variable "region" {
  type        = string
  description = "GCP Region"
  default     = "us-central1"
}

variable "zones" {
  type        = list(string)
  description = "Cluster zones"
  default     = ["us-central1-a", "us-central1-b"]
}

variable "network" {
  type        = string
  description = "VPC network name"
}

variable "subnetwork" {
  type        = string
  description = "Subnetwork name"
}

variable "ip_range_pods" {
  type        = string
  description = "Secondary range for pods"
}

variable "ip_range_services" {
  type        = string
  description = "Secondary range for services"
}
