# Root Terragrunt config for the blast-radius fixture. Child environments include this file and
# merge its defaults, which is the layout the evaluator has to understand.
locals {
  state = "production"

  defaults = {
    project_id      = "example-project"
    name            = "prod-cluster"
    regional        = true
    region          = "us-central1"
    zones           = ["us-central1-a", "us-central1-b"]
    network         = "prod-vpc"
    subnetwork      = "prod-subnet"
    ip_range_pods   = "10.1.0.0/16"
    ip_range_services = "10.2.0.0/20"
  }
}
