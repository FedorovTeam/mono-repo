# infrastructure/live/prod/terragrunt.hcl

remote_state {
  backend = "local"

  config = {
    path = "terraform.tfstate"
  }
}
