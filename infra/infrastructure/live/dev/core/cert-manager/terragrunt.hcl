# infrastructure/live/dev/core/cert-manager/terragrunt.hcl
# Terragrunt config для cert-manager (dev)
terraform {
  source = "../../../../../modules/core/cert-manager"
}

inputs = {
  kubeconfig = "${get_env("KUBECONFIG", "~/.kube/config")}"
}