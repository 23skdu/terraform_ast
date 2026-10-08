output "endpoint" {
  description = "Cluster control plane endpoint"
  value       = "10.0.0.1"
}

output "cluster_id" {
  description = "Unique cluster identifier"
  value       = "projects/test-proj/locations/us-central1/clusters/test-cluster"
}

output "ca_certificate" {
  description = "Cluster root CA certificate"
  value       = "LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0t"
  sensitive   = true
}

# New feature: newly introduced output
output "cost_allocation_status" {
  description = "Status of cost allocation feature"
  value       = "ENABLED"
}
