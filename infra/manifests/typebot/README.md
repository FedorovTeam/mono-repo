Развертывание Typebot управляется через ArgoCD.

Создай приложение в ArgoCD:

```bash
kubectl apply -n argocd -f application.yaml
```

ArgoCD синхронизирует манифесты из каталога `manifests/typebot`.


OIDC:
CallbackURL:
```shell
https://builder.typebot.fedorov-team.tech/api/auth/callback/custom-oauth
```