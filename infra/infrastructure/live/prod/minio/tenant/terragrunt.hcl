# infrastructure/live/prod/minio/tenant/terragrunt.hcl
terraform {
  source = "../../../../../modules/minio/tenant"
}

locals {
  overlay_file_path = "${get_terragrunt_dir()}/overlay-values.yaml"
  overlay_yaml      = fileexists(local.overlay_file_path) ? file(local.overlay_file_path) : ""
}

include {
  path = find_in_parent_folders()
}

inputs = {
  kubeconfig        = get_env("KUBECONFIG", "~/.kube/config-k3s")
  namespace         = "minio"

  tenant_name       = "minio-tenant-prod"
  minio_root_user   = get_env("MINIO_ROOT_USER", "Burdensome3253")
  minio_root_pass   = get_env("MINIO_ROOT_PASSWORD", "1545Upz_9jwI58__")

  replicas          = 1
  storage_size      = "20Gi"
  bucket_list       = ["events", "logs", "archive"]

  domain_api        = "api.minio.tech.fedorov-team.tech"
  domain_console    = "console.minio.tech.fedorov-team.tech"

  extra_values_yaml = local.overlay_yaml
}
