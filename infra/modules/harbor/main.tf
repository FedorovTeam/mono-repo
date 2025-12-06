# infra/modules/harbor/main.tf

terraform {
  required_providers {
    kubernetes = {
      source  = "hashicorp/kubernetes"
      version = ">= 2.22.0"
    }
  }
  backend "local" {}
}

provider "kubernetes" {
  config_path = var.kubeconfig
}

resource "kubernetes_namespace" "harbor" {
  metadata {
    name = var.namespace
  }
}

resource "kubernetes_secret" "admin" {
  metadata {
    name      = "harbor-admin"
    namespace = var.namespace
  }
  data = {
    HARBOR_ADMIN_PASSWORD = var.admin_password
  }
  type = "Opaque"
}

resource "kubernetes_secret" "database" {
  metadata {
    name      = "harbor-db"
    namespace = var.namespace
  }
  data = {
    password = var.db_password
  }
  type = "Opaque"
}

resource "kubernetes_secret" "s3" {
  metadata {
    name      = "harbor-s3"
    namespace = var.namespace
  }
  data = {
    REGISTRY_STORAGE_S3_ACCESSKEY = var.s3_access_key
    REGISTRY_STORAGE_S3_SECRETKEY = var.s3_secret_key
  }
  type = "Opaque"
}

resource "kubernetes_secret" "internal" {
  metadata {
    name      = "harbor-internal-secret"
    namespace = var.namespace
  }

  data = {
    secret = var.internal_secret
  }

  type = "Opaque"
}


resource "kubernetes_persistent_volume_claim" "registry" {
  metadata {
    name      = "harbor-registry"
    namespace = var.namespace
  }
  spec {
    access_modes      = ["ReadWriteOnce"]
    storage_class_name = var.storage_class
    resources {
      requests = {
        storage = var.storage_size
      }
    }
  }
}

resource "kubernetes_ingress_v1" "harbor" {
  metadata {
    name      = "harbor"
    namespace = var.namespace
    annotations = {
      "cert-manager.io/cluster-issuer"                   = "letsencrypt"
      "traefik.ingress.kubernetes.io/router.entrypoints" = "websecure"
      "acme.cert-manager.io/http01-ingress-class"         = "traefik"
    }
  }

  spec {
    ingress_class_name = "traefik"

    tls {
      secret_name = "harbor-tls"
      hosts       = [var.domain]
    }

    rule {
      host = var.domain

      http {
        path {
          path      = "/api/"
          path_type = "Prefix"
          backend {
            service {
              name = "harbor-core"
              port {
                number = 80
              }
            }
          }
        }

        path {
          path      = "/service/"
          path_type = "Prefix"
          backend {
            service {
              name = "harbor-core"
              port {
                number = 80
              }
            }
          }
        }

        path {
          path      = "/v2/"
          path_type = "Prefix"
          backend {
            service {
              name = "harbor-core"
              port {
                number = 80
              }
            }
          }
        }

        path {
          path      = "/c/"
          path_type = "Prefix"
          backend {
            service {
              name = "harbor-core"
              port {
                number = 80
              }
            }
          }
        }

        path {
          path      = "/"
          path_type = "Prefix"
          backend {
            service {
              name = "harbor-portal"
              port {
                number = 80
              }
            }
          }
        }
      }
    }
  }
}

output "harbor_url" {
  value = "https://${var.domain}"
}
