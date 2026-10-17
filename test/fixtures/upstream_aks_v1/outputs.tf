output "aks_id" {
  value       = "id"
  description = "The ID of the Managed Kubernetes Cluster"
}

output "client_key" {
  value       = "client_key"
  description = "Client certificate key"
  sensitive   = true
}
