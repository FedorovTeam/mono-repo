terraform {
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
  name       = "redis"
  namespace  = var.namespace
  repository = "https://charts.bitnami.com/bitnami"
  chart      = "redis"
  version    = "18.5.0" # проверено, поддерживает конфиг и memory policy

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

  # Подключим наш конфиг через configMap
  values = [file("${path.module}/values.yaml")]
}
