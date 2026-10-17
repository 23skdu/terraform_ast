output "aks_id" {
  value       = "id"
  description = "The ID of the Managed Kubernetes Cluster"
}

# Removed output: client_key

# New feature output:
output "oidc_issuer_url" {
  value       = "https://oidc.azure.com"
  description = "The OIDC issuer URL"
}
