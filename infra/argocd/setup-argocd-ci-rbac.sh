#!/bin/bash

set -euo pipefail

NAMESPACE="argocd"

echo "📌 Применение RBAC политики..."
kubectl apply -n "$NAMESPACE" -f - <<EOF
apiVersion: v1
kind: ConfigMap
metadata:
  name: argocd-rbac-cm
  namespace: ${NAMESPACE}
  labels:
    app.kubernetes.io/name: argocd-rbac-cm
    app.kubernetes.io/part-of: argocd
data:
  policy.default: role:readonly
  policy.csv: |
    p, role:ci, applications, get, *, allow
    p, role:ci, applications, update, *, allow
    p, role:ci, applications, sync, *, allow
    p, role:ci, applications, create, *, allow
    p, role:ci, applications, delete, *, allow
    g, ci, role:ci
  scopes: '[groups]'
EOF

echo "✅ Обновлён ConfigMap argocd-rbac-cm"

echo "📌 Установка accounts.ci: apiKey, login..."
kubectl patch configmap argocd-cm -n "$NAMESPACE" --type merge -p '{
  "data": {
    "accounts.ci": "apiKey, login"
  }
}'

echo "🔄 Перезапуск argocd-server..."
kubectl rollout restart deployment argocd-server -n "$NAMESPACE"

echo "✅ Готово!"
