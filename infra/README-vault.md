# Установка HashiCorp Vault

Модули `core/vault` и `core/vault-csi-provider` разворачивают сервер Vault и CSI
провайдер для монтирования секретов в Pods.

## Быстрый старт

```bash
# Установить Vault
cd infrastructure/live/prod/core/vault
terragrunt apply

# Установить CSI Driver и Vault CSI Provider
cd ../vault-csi-provider
terragrunt apply
```

Панель Vault будет доступна по адресу `https://vault.tech.fedorov-team.tech` после
успешного деплоя.

## Настройка секретов

```bash
export VAULT_ADDR=http://vault.tech.fedorov-team.tech:8200
export VAULT_TOKEN=<ваш root token>

vault kv put kv/flipt FLIPT_DB_URL="postgres://user:pass@db:5432/flipt"

vault policy write flipt-policy - <<'POLICY'
path "kv/data/flipt" {
  capabilities = ["read"]
}
POLICY

vault write auth/kubernetes/role/flipt-role \
    bound_service_account_names=vault-csi \
    bound_service_account_namespaces=analytics \
    policies=flipt-policy \
    ttl=24h
```

## Пример использования

```bash
kubectl apply -f modules/core/vault-csi-provider/secretproviderclass.yaml
kubectl apply -f modules/core/vault-csi-provider/deployment.yaml
```

