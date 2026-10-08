terraform {
  required_version = ">= 1.3.0"
}

module "gke" {
  source = "../upstream_gke_v1"

  project_id         = var.project_id
  name               = var.name
  regional           = var.regional
  region             = var.region
  zones              = var.zones
  network            = var.network
  subnetwork         = var.subnetwork
  ip_range_pods      = var.ip_range_pods
  ip_range_services  = var.ip_range_services
}
