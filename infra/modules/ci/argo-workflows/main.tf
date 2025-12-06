# modules/ci/argo-workflows/main.tf
terraform {
  required_providers {
    helm = {
      source  = "hashicorp/helm"
      version = ">= 2.10.1"
    }
    kubernetes = {
      source  = "hashicorp/kubernetes"
      version = ">= 2.24.0"
    }
  }
  backend "local" {}
}

provider "helm" {
  kubernetes {
    config_path = var.kubeconfig
  }
}

provider "kubernetes" {
  config_path = var.kubeconfig
}

# Credentials for MinIO artifact storage
resource "kubernetes_secret" "minio_creds" {
  metadata {
    name      = "minio-creds"
    namespace = var.namespace
  }
  data = {
    accesskey = var.minio_access_key
    secretkey = var.minio_secret_key
  }
}

resource "kubernetes_secret" "sso" {
  metadata {
    name      = "argo-server-sso"
    namespace = var.namespace
  }
  data = {
    client-id     = var.oidc_client_id
    client-secret = var.oidc_client_secret
  }
}

resource "kubernetes_secret" "harbor_docker_config" {
  metadata {
    name      = "harbor-docker-config"
    namespace = var.namespace
  }

  data = {
    ".dockerconfigjson" = local.dockerconfigjson_raw
  }

  type = "kubernetes.io/dockerconfigjson"
}

resource "helm_release" "argo_workflows" {
  name             = "argo-workflows"
  repository       = "https://argoproj.github.io/argo-helm"
  chart            = "argo-workflows"
  namespace        = var.namespace
  create_namespace = true
  version          = var.chart_version

  depends_on = [
    kubernetes_secret.sso,
    kubernetes_secret.minio_creds,
    kubernetes_secret.harbor_docker_config,
    helm_release.argo_workflows_crds
  ]

  values = [
    yamlencode({
      crds = {
        install = false # CRDs are now handled by argo_workflows_crds release
      }
      # Конфигурация хранилища артефактов (MinIO) - должна быть на верхнем уровне
      artifactRepository = {
        archiveLogs = true
        s3 = {
          bucket            = var.minio_bucket
          endpoint          = var.minio_endpoint
          insecure          = var.minio_insecure
          accessKeySecret = {
            name = kubernetes_secret.minio_creds.metadata[0].name
            key  = "accesskey"
          }
          secretKeySecret = {
            name = kubernetes_secret.minio_creds.metadata[0].name
            key  = "secretkey"
          }
        }
      }
      controller = {
        parallelism = var.controller_parallelism
        configMap = {
          name = "argo-workflows-workflow-controller-configmap"
        }
        workflowNamespaces = [var.namespace]
      }
      server = {
        authModes = ["client", "sso"]
        extraArgs = [
          "--basehref=/",
          "--secure=false"
        ]
        sso = {
          enabled       = true
          issuer        = var.oidc_issuer_url
          sessionExpiry = "240h"
          clientId = {
            name = kubernetes_secret.sso.metadata[0].name
            key  = "client-id"
          }
          clientSecret = {
            name = kubernetes_secret.sso.metadata[0].name
            key  = "client-secret"
          }
          redirectUrl = "https://${var.domain}/oauth2/callback"
          scopes      = ["openid", "profile", "email", "groups"]
          rbac = { 
            enabled = true 
          }
        }
        ingress = {
          enabled          = false
          ingressClassName = "traefik"
          hosts            = [var.domain]
          annotations = {
            "cert-manager.io/cluster-issuer"                   = "letsencrypt"
            "traefik.ingress.kubernetes.io/router.entrypoints" = "websecure"
          }
        }
      }
    })
  ]
}

locals {
  backend_ci_template = templatefile("${path.module}/manifests/backend-ci.yaml.tpl", {
    namespace               = var.namespace
    github_username         = var.github_username
    github_token            = var.github_token
    harbor_dockerconfigjson = var.harbor_dockerconfigjson
    github_repo             = var.github_repo
    github_branch           = var.github_branch
    poller_schedule         = var.poller_schedule
    harbor_image            = var.harbor_image
    argocd_server           = var.argocd_server
    argocd_app              = var.argocd_app
    argocd_token            = var.argocd_token
  })

  backend_ci_docs = [for doc in split("---", local.backend_ci_template) : trimspace(doc) if trimspace(doc) != ""]

  auth_base64 = base64encode("${var.harbor_username}:${var.harbor_password}")

  dockerconfigjson_raw = jsonencode({
    auths = {
      "${var.harbor_registry}" = {
        auth = local.auth_base64
      }
    }
  })

  dockerconfigjson_base64 = base64encode(local.dockerconfigjson_raw)
}

# Wait for CRDs to be available in Kubernetes API
resource "null_resource" "wait_for_crds_api" {
  depends_on = [helm_release.argo_workflows_crds]
  
  provisioner "local-exec" {
    command = <<-EOF
      timeout=300
      while [ $timeout -gt 0 ]; do
        if kubectl --kubeconfig="${var.kubeconfig}" get crd workflows.argoproj.io workflowtemplates.argoproj.io cronworkflows.argoproj.io 2>/dev/null; then
          echo "CRDs are available"
          exit 0
        fi
        echo "Waiting for CRDs to become available..."
        sleep 5
        timeout=$((timeout-5))
      done
      echo "Timeout waiting for CRDs"
      exit 1
    EOF
  }
}

resource "kubernetes_manifest" "backend_ci" {
  for_each   = { for idx, doc in local.backend_ci_docs : idx => yamldecode(doc) }
  manifest   = each.value
  
  depends_on = [
    null_resource.wait_for_crds_api,
    helm_release.argo_workflows
  ]
  
  field_manager {
    name           = "terraform"
    force_conflicts = true
  }
}

resource "kubernetes_manifest" "server_ingress" {
  manifest = {
    apiVersion = "networking.k8s.io/v1"
    kind       = "Ingress"
    metadata = {
      name      = "argo-workflows-server"
      namespace = var.namespace
      annotations = {
        "cert-manager.io/cluster-issuer"                   = "letsencrypt"
        "traefik.ingress.kubernetes.io/router.entrypoints" = "websecure"
      }
    }
    spec = {
      ingressClassName = "traefik"
      tls = [{
        hosts      = [var.domain]
        secretName = "argo-workflows-server-tls"
      }]
      rules = [{
        host = var.domain
        http = {
          paths = [{
            path     = "/"
            pathType = "Prefix"
            backend = {
              service = {
                name = "argo-workflows-server"
                port = { number = 2746 }
              }
            }
          }]
        }
      }]
    }
  }

  field_manager {
    name           = "terraform"
    force_conflicts = true
  }
  depends_on = [helm_release.argo_workflows_crds]
}

# Apply RBAC manifests for Argo Workflows
locals {
  rbac_files = var.create_rbac ? fileset("${path.module}/rbac", "*.yaml") : []
  rbac_docs  = flatten([
    for f in local.rbac_files : [
      for d in split("---", file("${path.module}/rbac/${f}")) : trimspace(d) if trimspace(d) != ""
    ]
  ])
}

resource "kubernetes_manifest" "rbac" {
  for_each = { for idx, doc in local.rbac_docs : idx => yamldecode(doc) }
  manifest = each.value
  field_manager {
    name           = "terraform"
    force_conflicts = true
  }
  depends_on = [helm_release.argo_workflows_crds]
}
