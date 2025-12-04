# 1. Установить CSI Driver и Vault CSI Provider
cd infrastructure/live/prod/core/vault-csi-provider
terragrunt apply

# 2. Настроить Vault: добавить секрет и политику (один раз)
export VAULT_ADDR=http://vault.tech.fedorov-team.tech:8200
export VAULT_TOKEN=<ваш root token>

vault kv put kv/flipt FLIPT_DB_URL="postgres://user:pass@db:5432/flipt"

vault policy write flipt-policy - <<EOF
path "kv/data/flipt" {
  capabilities = ["read"]
}
EOF

vault write auth/kubernetes/role/flipt-role \
    bound_service_account_names=vault-csi \
    bound_service_account_namespaces=analytics \
    policies=flipt-policy \
    ttl=24h

# 3. Применить SecretProviderClass и Deployment
kubectl apply -f modules/core/vault-csi-provider/secretproviderclass.yaml
kubectl apply -f modules/core/vault-csi-provider/deployment.yaml
