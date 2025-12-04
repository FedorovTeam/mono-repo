Развертывание Typebot управляется через ArgoCD.

Создай приложение в ArgoCD:

```bash
kubectl apply -n argocd -f application.yaml
```

ArgoCD синхронизирует манифесты из каталога `manifests/typebot`.
