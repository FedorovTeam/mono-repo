# infrastructure/live/prod/core/cert-manager-bootstrap/terragrunt.hcl

terraform {
  source = "../../../../../modules/core/cert-manager-bootstrap"
}

inputs = {
  kubeconfig = "${get_env("KUBECONFIG", "~/.kube/config-k3s")}"
}

dependencies {
  paths = ["../cert-manager"]
}
