# modules/minio/tenant/main.tf
terraform {
  backend "local" {}
  required_providers {
    kubernetes = {
      source = "hashicorp/kubernetes",
      version = ">=2.37.0"
    }
    helm       = {
      source = "hashicorp/helm",
      version = ">=2.17.0"
    }
  }
}

################ PROVIDERS ################
provider "kubernetes" {
  config_path = var.kubeconfig
}
provider "helm" {
  kubernetes = {
    config_path = var.kubeconfig
  }
}


################ ROOT-SECRET ##############
resource "kubernetes_secret" "creds" {
  metadata {
    name      = "${var.tenant_name}-creds"
    namespace = var.namespace
  }

  data = {
    "config.env" = <<EOF
export MINIO_ROOT_USER=${var.minio_root_user}
export MINIO_ROOT_PASSWORD=${var.minio_root_pass}
EOF
  }

  type = "Opaque"
}

################ ENV-CONFIG SECRET (for default chart values) ##############
resource "kubernetes_secret" "env_configuration" {
  metadata {
    name      = "myminio-env-configuration"
    namespace = var.namespace
  }

  data = {
    "config.env" = <<EOF
export MINIO_ROOT_USER=${var.minio_root_user}
export MINIO_ROOT_PASSWORD=${var.minio_root_pass}
EOF
  }

  type = "Opaque"
}

################ HELM RELEASE #############
resource "helm_release" "tenant" {
  name       = var.tenant_name
  namespace  = var.namespace
  repository = "https://operator.min.io/"
  chart      = "tenant"
  version    = "6.0.2"

  values = [
    yamlencode({
      secrets = null
      tenant = {
        name  = var.tenant_name
        image = {
          repository = "quay.io/minio/minio"
          tag        = "RELEASE.2024-12-13T22-19-12Z"
        }
        configSecret = {
          name           = kubernetes_secret.creds.metadata[0].name
          existingSecret = true
        }

        certificate = {
          requestAutoCert = false
        }
        exposeServices = { console = true, minio = true }

        pools = [{
          name               = "pool-0"
          servers            = var.replicas
          volumesPerServer   = 1
          volumeClaimTemplate = {
            metadata = { name = "data" }
            spec     = {
              accessModes = ["ReadWriteOnce"]
              resources   = { requests = { storage = var.storage_size } }
            }
          }
        }]

        mountPath = "/export"

        buckets = [
          for b in var.bucket_list : {
            name       = b
            policy     = "none"
            purge      = false
            versioning = false
          }
        ]
      }
    }),
    var.extra_values_yaml      # <- пусто, если overlay не нужен
  ]

  depends_on = [kubernetes_secret.creds, kubernetes_secret.env_configuration]
}

################ INGRESS: API #############
resource "kubernetes_ingress_v1" "api" {
  metadata {
    name        = "${var.tenant_name}-api"
    namespace   = var.namespace
    annotations = {
      "cert-manager.io/cluster-issuer"                   = "letsencrypt"
      "traefik.ingress.kubernetes.io/router.entrypoints" = "websecure"
    }
  }
  spec {
    tls {
      secret_name = "minio-s3-tls"
      hosts = [var.domain_api]
    }
    rule {
      host = var.domain_api
      http {
        path {
          path      = "/"
          path_type = "Prefix"
          backend {
            service {
              name = "${var.tenant_name}-hl"
              port { number = 9000 }
            }
          }
        }
      }
    }
  }
  depends_on = [helm_release.tenant]
}

############### INGRESS: CONSOLE ##########
#  service внутри Tenant-чарта остаётся HTTPS:9443,
#  поэтому говорим Traefik’у ходить к сервису по HTTPS
resource "kubernetes_manifest" "transport" {
  manifest = {
    apiVersion = "traefik.io/v1alpha1"
    kind       = "ServersTransport"
    metadata   = { name = "minio-console-transport", namespace = var.namespace }
    spec       = { insecureSkipVerify = true }
  }
}

resource "kubernetes_ingress_v1" "console" {
  metadata {
    name      = "${var.tenant_name}-console"
    namespace = var.namespace
    annotations = {
      "cert-manager.io/cluster-issuer"                    = "letsencrypt"
      "traefik.ingress.kubernetes.io/router.entrypoints"  = "websecure"
      # Console работает по http (9090), поэтому указываем http
      "traefik.ingress.kubernetes.io/service.serversscheme"   = "http"
      "traefik.ingress.kubernetes.io/service.serverstransport" = "minio-console-transport"
    }
  }
  spec {
    tls {
      secret_name = "minio-console-tls"
      hosts = [var.domain_console]
    }
    rule {
      host = var.domain_console
      http {
        path {
          path      = "/"
          path_type = "Prefix"
          backend {
            service {
              name = "${var.tenant_name}-console"
              port { number = 9090 }
            }
          }
        }
      }
    }
  }
  depends_on = [helm_release.tenant, kubernetes_manifest.transport]
}

############### CERT-MANAGER CERTIFICATE ###################
resource "kubernetes_manifest" "minio_console_certificate" {
  manifest = {
    apiVersion = "cert-manager.io/v1"
    kind       = "Certificate"
    metadata = {
      name      = "minio-tenant-console-cert"
      namespace = var.namespace
    }
    spec = {
      secretName = "minio-console-tls"
      issuerRef = {
        name = "letsencrypt"
        kind = "ClusterIssuer"
      }
      commonName = var.domain_console
      dnsNames   = [var.domain_console]
    }
  }
  depends_on = [helm_release.tenant]
}

############### OUTPUTS ###################
output "console_url" { value = "https://${var.domain_console}" }
output "s3_endpoint" { value = "https://${var.domain_api}"     }
