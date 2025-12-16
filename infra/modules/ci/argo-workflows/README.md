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
- Нужно удалить CRD:
```shell
# Остановить лишний контроллер:
kubectl -n argo scale deploy/argo-workflows-crds-workflow-controller --replicas=0

# Убедиться, что остался один
kubectl -n argo get pod | grep -i workflow-controller

# Перезапустить нормальный контроллер (чтобы он точно подхватил config)
kubectl -n argo rollout restart deploy/argo-workflows-workflow-controller
```

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
