# infrastructure/live/dev/traefik/core/terragrunt.hcl
# Terragrunt config для Traefik (dev)
terraform {
  source = "../../../../../modules/core/traefik"
}

inputs = {
  kubeconfig   = "${get_env("KUBECONFIG", "~/.kube/config")}"
  values_file  = "${get_parent_terragrunt_dir()}/../../../../charts/traefik/values.yaml"
}
