output "cluster_id" {
  description = "Wrapper cluster ID"
  value       = module.gke.cluster_id
}

output "endpoint" {
  description = "Wrapper endpoint"
  value       = module.gke.endpoint
}

output "legacy_network_tag" {
  description = "Wrapper legacy network tag"
  value       = module.gke.legacy_network_tag
}
