variable "cluster_name" {
  type        = string
  description = "Name of the EKS cluster"
}

variable "cluster_version" {
  type        = string
  description = "Kubernetes <major>.<minor> version to use for the EKS cluster"
  default     = "1.28"
}

variable "vpc_id" {
  type        = string
  description = "ID of the VPC where the cluster security group will be provisioned"
}

variable "subnet_ids" {
  type        = list(string)
  description = "A list of subnet IDs where the EKS cluster and nodes will be hosted"
}

variable "cluster_endpoint_public_access" {
  type        = bool
  description = "Indicates whether or not the Amazon EKS public API server endpoint is enabled"
  default     = true
}

variable "manage_aws_auth_configmap" {
  type        = bool
  description = "Determines whether to manage the aws-auth configmap"
  default     = false
}

variable "legacy_security_group_id" {
  type        = string
  description = "Legacy security group ID"
  default     = ""
}
