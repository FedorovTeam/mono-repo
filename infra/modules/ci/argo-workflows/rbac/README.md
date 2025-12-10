# RBAC for Argo Workflows

Эти манифесты настраивают доступ по группам из Authentik:

- `workflows-admin` — полные права для пользователей из группы `argo-workflows-admin`.
- `argo-workflows-admin` — полные права для пользователей из группы `argo-workflows-admin`.
- `workflows-readonly` — минимальный доступ по умолчанию для всех остальных.

Манифесты применяются автоматически модулем `ci/argo-workflows` во время `terragrunt apply`.
Токены сервисных аккаунтов добавлены прямо в файлы `admin.yaml` и `readonly.yaml`.
