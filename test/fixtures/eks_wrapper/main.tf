terraform {
  required_version = ">= 1.0.0"
}

module "eks" {
  source = "../upstream_eks_v19"

  cluster_name               = var.cluster_name
  vpc_id                     = var.vpc_id
  subnet_ids                 = var.subnet_ids
  manage_aws_auth_configmap  = var.manage_aws_auth_configmap
  legacy_security_group_id   = var.legacy_security_group_id
}
