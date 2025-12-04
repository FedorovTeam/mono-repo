# infrastructure/live/prod/core/argocd/terragrunt.hcl
terraform {
  source = "../../../../../modules/core/argocd"
}

include {
  path = find_in_parent_folders()
}

inputs = {
  kubeconfig = get_env("KUBECONFIG", "~/.kube/config-k3s")
  domain     = "argocd.tech.fedorov-team.tech"
  server_ingress_service_port = "http"

  authentik_issuer_url     = "https://auth.tech.fedorov-team.tech/application/o/argocd/"
  authentik_client_id      = "grPnDsHeesNLlyOrAnEITQvqhDPx316411dDAyjW"
  authentik_client_secret  = "p4wGBOsnYcnutl72DVlybtftvUXUBLLlEgSNYujGG5LTYr3C1ukqwNeniNCMXTICeEkPAXCijFT3Vi2V7tOLH6FDBiSQIFk3NDzcPeDNkxfxUUrbTOf49fHUbnBfpHE5"
}
