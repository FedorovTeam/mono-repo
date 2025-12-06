# Argo Workflows Infrastructure as Code

## Важно: Только через Terraform/Terragrunt

- **Все ресурсы Argo Workflows (CRD, helm-чарты, RBAC, ingress, secrets, configmap, workflow templates и т.д.) создаются и удаляются только через Terraform/Terragrunt.**
- **Ручное применение через kubectl запрещено.**
- Любые изменения — только через `terraform apply` или `terragrunt apply`.
- CRD управляются отдельным helm_release (`argo-workflows-crds`).
- Основной релиз Argo Workflows устанавливается с параметром `crds.install = false`.
- Все шаблоны, роли, биндинги, сервисные аккаунты, ingress, secrets, configmap — только через Terraform.

## Порядок деплоя/удаления

1. `terraform/terragrunt apply` — создаёт CRD, helm-релизы, все необходимые ресурсы.
2. `terraform/terragrunt destroy` — удаляет всё, кроме CRD (если не требуется удалять CRD, helm_release можно удалить отдельно).

## Troubleshooting
- Если после удаления helm_release CRD остались — удалите их вручную только если миграция невозможна.
- Все логи и диагностика — через стандартные средства Kubernetes и Argo Workflows.

## Документация
- [Argo Workflows Helm Chart](https://github.com/argoproj/argo-helm/tree/main/charts/argo-workflows)
- [Argo Workflows RBAC](https://github.com/argoproj/argo-workflows/tree/master/manifests/base/rbac)
- [Официальная документация](https://argo-workflows.readthedocs.io/en/stable/)

## Переменные модуля

- `controller_parallelism` — максимальное количество одновременно выполняемых workflows.
  Используется для настройки блока `controller.parallelism` в Helm-чарте.

### Пример использования

```hcl
module "argo_workflows" {
  source = "../../../../../modules/ci/argo-workflows"
  # ... другие параметры ...
  controller_parallelism = 10
}
```
