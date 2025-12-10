Развертывание Outline Wiki управляется через ArgoCD.

Создай приложение в ArgoCD:

```bash
kubectl apply -n argocd -f application.yaml
```

ArgoCD синхронизирует манифесты из каталога `manifests/outline`.

Настройка OIDC
callback URL:
```shell
https://docs.tech.fedorov-team.tech/auth/oidc.callback
```