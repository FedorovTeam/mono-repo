# infra/modules/harbor/variables.tf

variable "kubeconfig" { type = string }

variable "namespace" {
  type    = string
  default = "harbor"
}

variable "domain" { type = string }

variable "admin_password" { type = string }

variable "storage_size" {
  type    = string
  default = "10Gi"
}

variable "storage_class" {
  type    = string
  default = "local-path"
}

variable "db_password" {
  type = string
}

variable "s3_access_key" {
  type = string
}

variable "s3_secret_key" {
  type = string
}


variable "internal_secret" {
  type = string
}
