# infrastructure/live/prod/core/traefik/terragrunt.hcl
# Terragrunt config для Traefik
terraform {
  source = "../../../../../modules/core/traefik"
}

inputs = {
  kubeconfig   = "${get_env("KUBECONFIG", "~/.kube/config-k3s")}"
  values_file  = "./values.yaml"
}
