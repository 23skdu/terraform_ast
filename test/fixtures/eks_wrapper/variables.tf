variable "cluster_name" {
  type        = string
  description = "EKS Cluster Name"
  default     = "corporate-eks-cluster"
}

variable "vpc_id" {
  type        = string
  description = "VPC ID"
}

variable "subnet_ids" {
  type        = list(string)
  description = "Subnet IDs"
}

variable "manage_aws_auth_configmap" {
  type        = bool
  description = "Manage aws auth"
  default     = false
}

variable "legacy_security_group_id" {
  type        = string
  description = "Legacy security group"
  default     = ""
}
