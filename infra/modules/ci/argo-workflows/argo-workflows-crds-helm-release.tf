# modules/ci/argo-workflows/argo-workflows-crds-helm-release.tf
# Управление CRD Argo Workflows через отдельный helm_release (best practice)
resource "helm_release" "argo_workflows_crds" {
  name       = "argo-workflows-crds"
  repository = "https://argoproj.github.io/argo-helm"
  chart      = "argo-workflows"
  namespace  = var.namespace
  create_namespace = true
  version    = var.chart_version
  
  # Wait for CRDs to be fully installed
  wait          = true
  timeout       = 600
  atomic        = true
  
  values = [
    yamlencode({
      # Only install CRDs, disable all components
      crds = {
        install = true
        keep    = true
      }
      controller = {
        enabled = false
      }
      server = {
        enabled = false
      }
      executor = {
        enabled = false
      }
    })
  ]
  
  # Use latest chart version available for CRDs to avoid mismatched versions
  dependency_update = true
}

# В README.md модуля обязательно добавить инструкцию: любые изменения только через terraform/terragrunt apply
