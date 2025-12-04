# modules/minio/operator/main.tf

terraform {
  required_providers {
    helm = {
      source  = "hashicorp/helm"
      version = ">= 2.7.1"
    }
  }
  backend "local" {}
}

provider "helm" {
  kubernetes = {
    config_path = var.kubeconfig
  }
}


resource "helm_release" "operator" {
  name       = "minio-operator"
  repository = "https://operator.min.io/"
  chart      = "operator"
  namespace  = var.namespace
  version    = var.chart_version

  create_namespace = true

  values = [
    yamlencode({
      operator = {
        env = [
          {
            name  = "OPERATOR_STS_ENABLED"
            value = "on"
          },
          {
            name  = "MINIO_CONSOLE_TLS_ENABLE"
            value = var.console_tls_enable ? "on" : "off"
          },
          {
            name  = "WATCH_NAMESPACE"
            value = ""
          }
        ]
      }
      console = {
        ingress = {
          enabled  = var.ingress_enabled
          host     = var.ingress_host
          number   = 9090
          annotations = {
            "traefik.ingress.kubernetes.io/service.serversscheme" = "http"
            "cert-manager.io/cluster-issuer" = "letsencrypt"
          }
          tls = [{
              hosts      = [var.ingress_host]
              secretName = "console-tls"
            }]

        }
      }
    })
  ]
}
