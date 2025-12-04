# modules/cert-manager-bootstrap/main.tf
variable "kubeconfig" { type = string }

terraform {
  required_providers {
    kubernetes = {
      source  = "hashicorp/kubernetes"
      version = ">= 2.0.0"
    }
  }
}

provider "kubernetes" {
  config_path = var.kubeconfig
}

resource "kubernetes_manifest" "cluster_issuer" {
  manifest = yamldecode(file("${path.module}/cluster-issuer.yaml"))
}
