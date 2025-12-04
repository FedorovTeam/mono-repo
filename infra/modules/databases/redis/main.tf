# modules/databases/redis/main.tf

terraform {
  required_providers {
    helm = {
      source  = "hashicorp/helm"
      version = ">= 3.0.0"
    }
  }

  backend "local" {}
}

variable "kubeconfig" {
  type = string
}

variable "namespace" {
  type    = string
  default = "redis"
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
  version          = "24.0.0"
  create_namespace = true

  # Все бывшие set {} собраны в один список
  set = [
    {
      name  = "architecture"
      value = "standalone"
    },
    {
      name  = "auth.enabled"
      value = "false"
    },
    {
      name  = "master.persistence.enabled"
      value = "true"
    },
    {
      name  = "master.persistence.size"
      value = "1Gi"
    },
    {
      name  = "master.resources.requests.memory"
      value = "256Mi"
    },
    {
      name  = "master.resources.requests.cpu"
      value = "100m"
    },
  ]

  # Подключаем configMap / дополнительные настройки
  values = [file("${path.module}/values.yaml")]
}
