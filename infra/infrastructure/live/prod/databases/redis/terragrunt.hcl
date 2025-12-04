terraform {
  source = "../../../../../modules/databases/redis"
}

include {
  path = find_in_parent_folders()
}

inputs = {
  kubeconfig = get_env("KUBECONFIG", "~/.kube/config-k3s")
  namespace  = "redis"
}
