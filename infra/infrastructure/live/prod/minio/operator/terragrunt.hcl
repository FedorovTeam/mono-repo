# infrastructure/live/prod/minio/operator/terragrunt.hcl

terraform {
  source = "../../../../../modules/minio/operator"
}

include {
  path = find_in_parent_folders()
}

inputs = {
  kubeconfig    = get_env("KUBECONFIG", "~/.kube/config-k3s")
  namespace     = "minio-operator"
  chart_version = "6.0.2"

  ingress_enabled    = true
  ingress_host       = "operator.minio.tech.fedorov-team.tech"
  ingress_number     = 9090
  # tls_secret_name    = "minio-console-tls"
  console_tls_enable = true
}
