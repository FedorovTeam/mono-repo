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
  name             = "postgres-v2"
  namespace        = var.namespace
  chart            = "postgresql"
  repository       = "https://charts.bitnami.com/bitnami"
  version          = "18.1.13"
  create_namespace = true

  set = [
    # Используем заранее созданный Kubernetes Secret
    {
      name  = "auth.existingSecret"
      value = "postgres-secret"
    },
    {
      name  = "auth.username"
      value = "postgres"
    },
    {
      name  = "auth.database"
      value = "app"
    },
    {
      name  = "nameOverride"
      value = "postgresql"
    },
    {
      name  = "primary.persistence.enabled"
      value = "true"
    },
    {
      name  = "primary.persistence.size"
      value = "2Gi"
    },
    {
      name  = "primary.resources.requests.memory"
      value = "512Mi"
    },
    {
      name  = "primary.resources.requests.cpu"
      value = "200m"
    },
    {
      name  = "postgresql.parameters[0].name"
      value = "max_connections"
    },
    {
      name  = "postgresql.parameters[0].value"
      value = "1000"
    },
    {
      name  = "postgresql.parameters[1].name"
      value = "superuser_reserved_connections"
    },
    {
      name  = "postgresql.parameters[1].value"
      value = "10"
    },
  ]
}
