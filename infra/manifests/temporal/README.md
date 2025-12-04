# Temporal

Развёртывание Temporal управляется через ArgoCD.

## 1. Приложение в ArgoCD

Либо используй app-of-apps, который подхватит `infra/argocd/temporal-app.yaml`,
либо создавай приложение вручную:

```bash
kubectl apply -n argocd -f infra/manifests/temporal/application.yaml
