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

output "aws_auth_configmap_yaml" {
  description = "Formatted yaml output for aws-auth configmap"
  value       = "apiVersion: v1\nkind: ConfigMap"
}
