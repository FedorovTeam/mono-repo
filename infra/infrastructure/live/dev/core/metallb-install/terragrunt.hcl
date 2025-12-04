# infrastructure/live/dev/core/metallb-install/terragrunt.hcl

terraform {
  source = "../../../../../modules/core/metallb-install"
}

inputs = {
  kubeconfig = "${get_env("KUBECONFIG", "~/.kube/config")}"
}
