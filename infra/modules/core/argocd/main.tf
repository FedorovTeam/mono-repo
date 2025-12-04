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
          servicePort = "http" # игнорируется
        }
        config = {
          url = "https://${var.domain}"
        }
      }
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
