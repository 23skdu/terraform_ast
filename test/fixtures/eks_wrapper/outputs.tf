output "cluster_id" {
  description = "Wrapper EKS Cluster ID"
  value       = module.eks.cluster_id
}

output "cluster_endpoint" {
  description = "Wrapper EKS Cluster Endpoint"
  value       = module.eks.cluster_endpoint
}

output "aws_auth" {
  description = "Wrapper AWS Auth ConfigMap"
  value       = module.eks.aws_auth_configmap_yaml
}
