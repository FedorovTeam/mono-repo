# modules/databases/postgres/main.tf

terraform {
  backend "local" {}
}

variable "kubeconfig" {
  type = string
}

variable "namespace" {
  type    = string
  default = "postgres"
}

terraform {
  required_providers {
    helm = {
      source  = "hashicorp/helm"
      version = ">= 2.10.1"
    }
  }
}

provider "helm" {
  kubernetes = {
    config_path = var.kubeconfig
  }
}

resource "helm_release" "postgres" {
  name             = "postgres"
  namespace        = var.namespace
  chart            = "postgresql"
  repository       = "https://charts.bitnami.com/bitnami"
  version          = "12.1.5"
  create_namespace = true

  # Используем заранее созданный Kubernetes Secret
  set {
    name  = "auth.existingSecret"
    value = "postgres-secret"
  }

  set {
    name  = "auth.username"
    value = "postgres"
  }

  set {
    name  = "auth.database"
    value = "app"
  }

  # Переопределим имя ресурсов чарта
  set {
    name  = "nameOverride"
    value = "postgresql"
  }

  set {
    name  = "primary.persistence.enabled"
    value = "true"
  }

  set {
    name  = "primary.persistence.size"
    value = "2Gi"
  }

  set {
    name  = "primary.resources.requests.memory"
    value = "256Mi"
  }

  set {
    name  = "primary.resources.requests.cpu"
    value = "100m"
  }

  # Параметры PostgreSQL (зависят от версии чарта, но оставим как есть)
  set {
    name  = "postgresql.parameters[0].name"
    value = "max_connections"
  }

  set {
    name  = "postgresql.parameters[0].value"
    value = "500"
  }

  set {
    name  = "postgresql.parameters[1].name"
    value = "superuser_reserved_connections"
  }

  set {
    name  = "postgresql.parameters[1].value"
    value = "5"
  }
}
