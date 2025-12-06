# infra/infrastructure/live/prod/registry/harbor/terragrunt.hcl
terraform {
  source = "../../../../../modules/harbor"
}

include {
  path = find_in_parent_folders()
}

inputs = {
  kubeconfig     = get_env("KUBECONFIG", "~/.kube/config-k3s")
  namespace      = "harbor"
  domain         = "harbor.tech.fedorov-team.tech"
  admin_password = get_env("HARBOR_ADMIN_PASSWORD", "c)N8Ay2?TQzQTMt6")
  storage_size   = "10Gi"
  storage_class  = "local-path"
  db_password    = get_env("POSTGRES_PASSWORD", "3&!tAL5bf38yfx")
  s3_access_key  = get_env("MINIO_ROOT_USER", "3zDCij2aIXjChCuCxDuL")
  s3_secret_key  = get_env("MINIO_ROOT_PASSWORD", "uDFCTlClJWXlY4rB6f8RXDLpJwD2utDCxwBbO2HC")
  internal_secret = "3de70a3f9abf0e064df141e0a5f17680"
}
