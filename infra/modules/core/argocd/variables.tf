# modules/core/argocd/variables.tf
variable "kubeconfig" { type = string }

variable "domain" { type = string }

variable "namespace" {
  type    = string
  default = "argocd"
}

variable "chart_version" {
  type    = string
  default = "8.0.14"
}

variable "server_ingress_service_port" {
  description = "Service port name for ArgoCD ingress"
  type        = string
  default     = "http"
}

