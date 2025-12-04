# modules/databases/redis/main.tf

variable "kubeconfig" {
  type = string
}

variable "namespace" {
  type    = string
  default = "redis"
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

resource "helm_release" "redis" {
  name             = "redis"
  namespace        = var.namespace
  repository       = "https://charts.bitnami.com/bitnami"
  chart            = "redis"
  version          = "18.5.0"
  create_namespace = true

  set {
    name  = "architecture"
    value = "standalone"
  }

  set {
    name  = "auth.enabled"
    value = "false"
  }

  set {
    name  = "master.persistence.enabled"
    value = "true"
  }

  set {
    name  = "master.persistence.size"
    value = "1Gi"
  }

  set {
    name  = "master.resources.requests.memory"
    value = "256Mi"
  }

  set {
    name  = "master.resources.requests.cpu"
    value = "100m"
  }

  # Подключаем configMap из values.yaml в этом модуле
  values = [file("${path.module}/values.yaml")]
}
