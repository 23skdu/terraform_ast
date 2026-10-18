include {
  path = find_in_parent_folders("root.hcl")
}

terraform {
  source = "../../gke_wrapper"
}

# A child whose own locals must take precedence over the inherited ones.
locals {
  name = "dev-cluster"
}

inputs = merge(local.defaults, {
  name                 = local.name
  cluster_autoscaling_profile = null
  grant_registry_access = false
})
