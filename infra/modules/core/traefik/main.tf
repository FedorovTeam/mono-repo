# modules/traefik/main.tf
# OpenTofu/Terraform модуль для установки Traefik через Helm

variable "kubeconfig" {
  type = string
}

variable "values_file" {
  type = string
}

terraform {
  required_providers {
    helm = {
      source  = "registry.terraform.io/hashicorp/helm"
      version = ">= 2.10.1"
    }
  }
}

provider "helm" {
  kubernetes = {
    config_path = var.kubeconfig
  }
}

resource "helm_release" "traefik" {
  name             = "traefik"
  repository       = "https://traefik.github.io/charts"
  chart            = "traefik"
  namespace        = "traefik"
  create_namespace = true
  values           = [file(var.values_file)]  # Используем список файлов для значений
}
