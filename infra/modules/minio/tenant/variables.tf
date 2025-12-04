# modules/minio/tenant/variables.tf
variable "kubeconfig"      {
  type = string
}

variable "namespace"       {
  type = string
  default = "minio"
}

variable "tenant_name"     {
  type = string
}

variable "minio_root_user" {
  type = string
}

variable "minio_root_pass" {
  type = string
}

variable "replicas"        {
  type = number
  default = 4
}

variable "storage_size"    {
  type = string
  default = "20Gi"
}

variable "bucket_list"     {
  type = list(string)
  default = []
}

variable "domain_api"      {
  type = string
}

variable "domain_console"  {
  type = string
}

# YAML-overlay (может быть "")
variable "extra_values_yaml" {
  type    = string
  default = ""
}
