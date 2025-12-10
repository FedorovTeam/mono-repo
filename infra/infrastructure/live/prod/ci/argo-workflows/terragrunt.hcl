# infrastructure/live/prod/ci/argo-workflows/terragrunt.hcl
terraform {
  source = "../../../../../modules/ci/argo-workflows"
}

include {
  path = find_in_parent_folders()
}

inputs = {
  kubeconfig              = get_env("KUBECONFIG", "~/.kube/config-k3s")
  namespace               = "argo"
  github_token            = "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"
  harbor_dockerconfigjson = "cm9ib3QkYXJnb193b3JrZmxvd3M6TGN2NU9yVmVZODJkamVlT0JNNjlENVdCYVpoR1BMQUQ="
  harbor_username         = "robot$argo_workflows"
  harbor_password         = "eya7lpnckty8zQQP0jYZ0X9Wq8sWCj0j"
  domain                  = "workflows.tech.fedorov-team.tech"
  oidc_client_id          = "xCiHcnaoQUI4jrAaIbScaDwdbmPaQW7P4LFZwTWd"
  oidc_client_secret      = "smjumacsAumCISXIx9OvEHoAkdSAMX2W0M87adVGzJyalFYTuykC1YaUXmtlX2hUZxUZtvXcUmEqz1H5ri4YMdNRZVDy3rcKXBcQHTpkB70XeQuZgGuzDT55ijrJ9a1j"
  oidc_issuer_url         = "https://auth.tech.fedorov-team.tech/application/o/argo-workflows/"
  argocd_token            = "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJpc3MiOiJhcmdvY2QiLCJzdWIiOiJjaTphcGlLZXkiLCJuYmYiOjE3NjUzNjQ5MjMsImlhdCI6MTc2NTM2NDkyMywianRpIjoiYmViYjNkMmItN2FiMi00ODVjLTk4MjUtNjU4ZGNhMzU0OGVhIn0.a7yDIWv47FNSN50_RiGqT4mtrNuu6jVxmx4AXl_CFjE"
  minio_access_key        = get_env("MINIO_ROOT_USER", "IiAXxuXEwLbbNGMysepU")
  minio_secret_key        = get_env("MINIO_ROOT_PASSWORD", "Fd5ffnfGojFzVMCEhcOOWwN4fORYt9dOQk8lNg2G")
  minio_bucket            = "argo-artifacts"
  minio_endpoint          = "minio-tenant-prod-hl.minio.svc.cluster.local:9000"
  minio_insecure          = true  # false для HTTPS endpoint
  create_rbac             = true
  controller_parallelism  = 2
}
