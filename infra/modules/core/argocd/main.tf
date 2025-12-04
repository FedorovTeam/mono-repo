# modules/core/argocd/main.tf
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
  kubernetes = {
    config_path = var.kubeconfig
  }
}

provider "kubernetes" {
  config_path = var.kubeconfig
}

resource "helm_release" "argocd" {
  name             = "argocd"
  repository       = "https://argoproj.github.io/argo-helm"
  chart            = "argo-cd"
  namespace        = var.namespace
  create_namespace = true
  version          = var.chart_version

  values = [
    yamlencode({
      global = {
        domain = var.domain
      }

      server = {
        extraArgs = ["--insecure"]

        service = {
          ports = [
            {
              name       = "http"
              port       = 80
              targetPort = 8080
            },
            {
              name       = "https"
              port       = 443
              targetPort = 8080
            }
          ]
        }

        ingress = {
          enabled          = false
          ingressClassName = "traefik"
          hosts            = [var.domain]
          annotations = {
            "cert-manager.io/cluster-issuer"                   = "letsencrypt"
            "traefik.ingress.kubernetes.io/router.entrypoints" = "websecure"
          }
          servicePort = var.server_ingress_service_port
        }

        # argocd-cm (ConfigMap) — сюда уйдёт url и dex.config
        config = {
          url = "https://${var.domain}"

          # Конфиг Dex с Authentik как OIDC-провайдером
          # по гайду authentik: https://integrations.goauthentik.io/infrastructure/argocd/
          "dex.config" = <<-EOT
            connectors:
              - type: oidc
                id: authentik
                name: Authentik
                config:
                  issuer: ${var.authentik_issuer_url}
                  clientID: ${var.authentik_client_id}
                  clientSecret: $dex.authentik.clientSecret
                  insecureEnableGroups: true
                  scopes:
                    - openid
                    - profile
                    - email
                    - groups
          EOT
        }
      }

      # Repo-server env как у тебя было
      repoServer = {
        env = [
          {
            name  = "SSH_AUTH_SOCK"
            value = ""
          },
          {
            name  = "ARGOCD_GIT_SSH_COMMAND"
            value = "ssh -i /ssh/id_rsa -o StrictHostKeyChecking=yes"
          }
        ]
      }

      # --- configs.* идут в argocd-* ConfigMaps/Secrets ---
      configs = {
        # argocd-secret: добавляем clientSecret для Dex/Authenitk
        secret = {
          extra = {
            # будет доступно как $dex.authentik.clientSecret
            "dex.authentik.clientSecret" = var.authentik_client_secret
          }
        }

        # argocd-rbac-cm: простейший RBAC по группам
        rbac = {
          "policy.csv" = <<-EOT
            g, ArgoCD Admins, role:admin
            g, ArgoCD Viewers, role:readonly
          EOT
        }
      }
    })
  ]
}


resource "kubernetes_manifest" "argocd_ingress_override" {
  manifest = {
    apiVersion = "networking.k8s.io/v1"
    kind       = "Ingress"
    metadata = {
      name      = "argocd-server"
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
        secretName = "argocd-server-tls"
      }]
      rules = [{
        host = var.domain
        http = {
          paths = [{
            path     = "/"
            pathType = "Prefix"
            backend = {
              service = {
                name = "argocd-server"
                port = {
                  name = "http"
                }
              }
            }
          }]
        }
      }]
    }
  }

  depends_on = [helm_release.argocd]
}
