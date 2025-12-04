# infrastructure/live/dev/core/cert-manager-bootstrap/terragrunt.hcl
terraform {
  source = "../../../../../modules/core/cert-manager-bootstrap"
}

inputs = {
  kubeconfig = "${get_env("KUBECONFIG", "~/.kube/config")}"
}

dependencies {
  paths = ["../cert-manager"]
}
