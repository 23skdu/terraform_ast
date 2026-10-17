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

variable "dns_prefix" {
  type        = string
  description = "DNS prefix specified when creating the managed cluster"
}

variable "agents_type" {
  type        = string
  description = "The type of Agent Pool"
  default     = "VirtualMachineScaleSets"
}

variable "os_disk_size_gb" {
  type        = number
  description = "Disk size of nodes in GB"
  default     = 50
}
