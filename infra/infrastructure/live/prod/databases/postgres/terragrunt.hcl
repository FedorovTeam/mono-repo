# infrastructure/live/prod/databases/postgres/terragrunt.hcl

terraform {
  source = "../../../../../modules/databases/postgres"
}

include {
  path = find_in_parent_folders()
}

inputs = {
  kubeconfig = get_env("KUBECONFIG", "~/.kube/config-k3s")
  namespace  = "postgres"
}
