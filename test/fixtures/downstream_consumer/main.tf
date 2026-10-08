terraform {
  required_version = ">= 1.3.0"
}

module "primary_cluster" {
  source = "../gke_wrapper"

  project_id        = "my-project-123"
  name              = "prod-us-central1"
  regional          = true
  region            = "us-central1"
  zones             = ["us-central1-a", "us-central1-b"]
  network           = "vpc-prod"
  subnetwork        = "subnet-k8s"
  ip_range_pods     = "10.1.0.0/16"
  ip_range_services = "10.2.0.0/20"
}

output "cluster_tag" {
  value = module.primary_cluster.legacy_network_tag
}
