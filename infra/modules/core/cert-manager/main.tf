# modules/cert-manager/main.tf
# OpenTofu/Terraform модуль для установки cert-manager через Helm

variable "kubeconfig" {
  type = string
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

resource "helm_release" "cert_manager" {
  name             = "cert-manager"
  repository       = "https://charts.jetstack.io"
  chart            = "cert-manager"
  namespace        = "cert-manager"
  create_namespace = true
  version          = "v1.14.5"

  set = [
    {
      name  = "installCRDs"
      value = "true"
    }
  ]
}

