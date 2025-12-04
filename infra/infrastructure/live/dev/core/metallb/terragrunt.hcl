#infrastructure/live/dev/core/metallb/terragrunt.hcl

terraform {
  source = "../../../../../modules/core/metallb"
}

inputs = {
  kubeconfig   = "${get_env("KUBECONFIG", "~/.kube/config")}"
  address_pool = ["192.168.1.240-192.168.1.250"]  # замените на ваш пул IP
}

dependencies {
  paths = ["../metallb-install"]
}
