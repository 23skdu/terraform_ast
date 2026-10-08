output "cluster_id" {
  description = "The ID of the EKS cluster"
  value       = "test-eks-cluster"
}

output "cluster_endpoint" {
  description = "Endpoint for your Kubernetes API server"
  value       = "https://test-eks.us-east-1.eks.amazonaws.com"
}

output "cluster_arn" {
  description = "The Amazon Resource Name (ARN) of the cluster"
  value       = "arn:aws:eks:us-east-1:123456789012:cluster/test-eks-cluster"
}

# New feature: newly introduced output
output "access_entries" {
  description = "Map of access entries created and their attributes"
  value       = "enabled"
}
