# Установка Argo Workflows

Модуль `ci/argo-workflows` разворачивает Argo Workflows и все ресурсы
необходимые для CI `backend-mono-repo`.
Модуль также настраивает хранение артефактов в MinIO и публикует образы в Harbor автоматически.

## Быстрый старт

```bash
cd infrastructure/live/prod/ci/argo-workflows
terragrunt apply
```

После применения будет создан CronJob `github-poller`, который проверяет
репозиторий и при появлении нового коммита собирает образ с Kaniko и
публикует его в Harbor. Консоль Argo Workflows будет доступна по адресу,
указанному в переменной `domain` (например `workflows.tech.fedorov-team.tech`).

По умолчанию Argo Workflows настроен на авторизацию через Authentik (OIDC).
Для работы SSO необходимо указать параметры `oidc_client_id`,
`oidc_client_secret` и `oidc_issuer_url` в `terragrunt.hcl`.

## Проверка доступа к Workflows

При использовании команды `argocd workflows` может появляться ошибка:

```
Failed to load version/info Error: {"code":7,"message":"not allowed"}
```

Для работы с Argo Workflows следует применять CLI `argo`. Получить токен
и выполнить команду можно так:

```bash
kubectl -n argo port-forward svc/argo-workflows-server 2746:2746 &
ARGO_TOKEN=$(kubectl -n argo create token argo-workflows-server)
argo --server localhost:2746 --auth-token "$ARGO_TOKEN" list
```

## Настройка Authentik

1. Создайте в Authentik OAuth2-провайдера и приложение для Argo Workflows.
   - Redirect URI: `https://<DOMAIN>/oauth2/callback`
   - Scopes: `openid profile email groups`
2. Скопируйте `client_id` и `client_secret` в `terragrunt.hcl`.
3. В параметре `oidc_issuer_url` укажите URL провайдера (например `https://auth.tech.fedorov-team.tech/application/o/argo/`).

## Настройка RBAC

```bash
kubectl apply -f ~/IaC/modules/ci/argo-workflows/rbac/
kubectl patch serviceaccount workflows-readonly -n argo -p '{"metadata":{"annotations":{"workflows.argoproj.io/rbac-rule":"false"}}}'
```

Argo Workflows использует группы из токена Authentik. Роли и сервисные аккаунты создаются автоматически модулем `ci/argo-workflows` во время `terragrunt apply`. RBAC-манифесты находятся в каталоге `modules/ci/argo-workflows/rbac`, применять их вручную не требуется.

После входа через Authentik можно получить токен и работать с CLI так:

```bash
ARGO_TOKEN=$(kubectl -n argo create token workflows-admin)
argo --server https://workflows.tech.fedorov-team.tech --auth-token "$ARGO_TOKEN" list
```

## CI Workflow для backend

Модуль создаёт CronJob `github-poller`, который отслеживает коммиты в
`backend-mono-repo` и запускает Workflow `backend-build`. Пайплайн собирает
образ через Kaniko, публикует его в Harbor и затем выполняет деплой приложения
`backend` с помощью ArgoCD.

Для работы шага деплоя требуется токен пользователя ArgoCD. Передайте его через
переменную `argocd_token` в `terragrunt.hcl`:

```hcl
inputs = {
  argocd_token = "<ARGOCD_TOKEN>"
}
```

Токен можно сгенерировать командой:

```bash
argocd account generate-token --account ci --expiry 720h
```

Аккаунт `ci` описан в файле `argocd/argocd-repos-configmap.yaml` (ключ
`accounts.ci`). Если он ещё не создан, примените манифест:

```bash
kubectl apply -f argocd/argocd-repos-configmap.yaml
```

После этого команда выше вернёт токен, который можно использовать в
`terragrunt.hcl` в переменной `argocd_token`.
