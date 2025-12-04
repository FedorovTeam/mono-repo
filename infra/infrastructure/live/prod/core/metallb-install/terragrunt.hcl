# infrastructure/live/prod/core/metallb-install/terragrunt.hcl

terraform {
  source = "../../../../../modules/core/metallb-install"
}

inputs = {
  kubeconfig = "${get_env("KUBECONFIG", "~/.kube/config-k3s")}"
}
