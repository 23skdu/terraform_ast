# A realistic Terragrunt consumer: the dynamic inputs expression below is the canonical idiom,
# and it can only be resolved by evaluating this file's include chain and locals.
include {
  path = find_in_parent_folders("root.hcl")
}

terraform {
  source = "../../gke_wrapper"
}

inputs = merge(local.defaults, {
  cluster_autoscaling_profile = {
    name                 = "prod"
    enable_cpu_autoscaling = true
    initial_node_count   = 1
  }
})
