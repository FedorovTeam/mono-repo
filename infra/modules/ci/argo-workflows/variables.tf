# modules/ci/argo-workflows/variables.tf
variable "kubeconfig" { type = string }

variable "namespace" {
  type    = string
  default = "argo"
}

variable "chart_version" {
  type    = string
  default = "0.41.1"
}

variable "github_username" {
  type    = string
  default = "git"
}

variable "github_token" { type = string }

variable "github_repo" {
  type    = string
  default = "FedorovTeam/mono-repo"
}

variable "github_branch" {
  type    = string
  default = "main"
}

variable "harbor_image" {
  type    = string
  default = "harbor.tech.fedorov-team.tech/backend/docling-transformer-service"
}

variable "harbor_dockerconfigjson" { type = string }

variable "poller_schedule" {
  type    = string
  default = "*/5 * * * *"
}

variable "domain" {
  description = "FQDN where Argo Workflows UI will be exposed"
  type        = string
}

variable "oidc_client_id" {
  description = "OIDC client ID for Authentik"
  type        = string
}

variable "oidc_client_secret" {
  description = "OIDC client secret for Authentik"
  type        = string
}

variable "oidc_issuer_url" {
  description = "OIDC issuer URL"
  type        = string
}

variable "argocd_server" {
  type    = string
  default = "argocd-server.argocd.svc.cluster.local:443"
}

variable "argocd_app" {
  type    = string
  default = "backend"
}

variable "argocd_token" { type = string }

variable "minio_endpoint" {
  type    = string
  default = "minio.argo.svc.cluster.local:9000"
}

variable "minio_bucket" {
  type    = string
  default = "argo-artifacts"
}

variable "minio_insecure" {
  type    = bool
  default = true  # true для HTTP-подключения к MinIO в кластере
}

variable "minio_access_key" { type = string }
variable "minio_secret_key" { type = string }

variable "create_rbac" {
  type    = bool
  default = true
}

variable "harbor_registry" {
  type        = string
  description = "Harbor registry hostname"
  default     = "harbor.tech.fedorov-team.tech"
}

variable "harbor_username" {
  type        = string
  description = "Robot username"
}

variable "harbor_password" {
  type        = string
  description = "Robot password"
  sensitive   = true
}

variable "controller_parallelism" {
  type        = number
  description = "Maximum number of workflows executed in parallel"
}
