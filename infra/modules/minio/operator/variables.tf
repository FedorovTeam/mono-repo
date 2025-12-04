# modules/minio/operator/variables.tf

variable "kubeconfig" {
  type = string
}

variable "namespace" {
  type    = string
  default = "minio-operator"
}

variable "chart_version" {
  type    = string
  default = "5.0.18"
}

variable "ingress_enabled" {
  type    = bool
  default = true
}

variable "ingress_host" {
  type    = string
  default = "operator.minio.local"
}

variable "ingress_number" {
  type    = number
  default = 9443
}

variable "tls_secret_name" {
  type    = string
  default = ""
}

variable "console_tls_enable" {
  type    = bool
  default = true
}
