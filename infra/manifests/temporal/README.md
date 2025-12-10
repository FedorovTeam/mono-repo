# Temporal

Развёртывание Temporal управляется через ArgoCD.

## 1. Приложение в ArgoCD

Либо используй app-of-apps, который подхватит `infra/argocd/temporal-app.yaml`,
либо создавай приложение вручную:

```bash
kubectl apply -n argocd -f infra/manifests/temporal/application.yaml
```

SSO:
Callback:
```shell
https://temporal.tech.fedorov-team.tech/auth/sso/callback
```

Установка namespace:
```shell
cd /tmp

# Скачиваем последний релиз CLI для Linux amd64
curl -sSfL "https://temporal.download/cli/archive/latest?platform=linux&arch=amd64" -o temporal.tar.gz

# Распаковываем бинарник
tar -xzf temporal.tar.gz temporal

# Кладём в PATH
sudo install temporal /usr/local/bin/temporal

# Проверяем
temporal version
```

Проброс порта Temporal из кластера на localhost
```shell
kubectl port-forward -n temporal svc/temporal-frontend 7233:7233
```

Создаём namespace knowledge-base
```shell
temporal operator namespace create \
  --address localhost:7233 \
  --namespace knowledge-base \
  --retention 7d \
  --description "Knowledge base namespace"

```