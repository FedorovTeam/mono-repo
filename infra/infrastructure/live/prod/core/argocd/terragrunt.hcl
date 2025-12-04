# infrastructure/live/prod/core/argocd/terragrunt.hcl
terraform {
  source = "../../../../../modules/core/argocd"
}

include {
  path = find_in_parent_folders()
}

inputs = {
  kubeconfig = get_env("KUBECONFIG", "~/.kube/config-k3s")
  domain     = "argocd.tech.fedorov-team.tech"
  server_ingress_service_port = "http"
}
