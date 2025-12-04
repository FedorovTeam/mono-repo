#infrastructure/live/prod/core/metallb/terragrunt.hcl

terraform {
  source = "../../../../../modules/core/metallb"
}

inputs = {
  kubeconfig   = "${get_env("KUBECONFIG", "~/.kube/config-k3s")}"
  address_pool = ["192.168.1.240-192.168.1.250"]  # замените на ваш пул IP
}

dependencies {
  paths = ["../metallb-install"]
}
