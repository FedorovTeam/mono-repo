#!/bin/bash

set -e

echo "🧼 Starting Kubernetes node cleanup..."

# 1. Очистка завершённых и эвакуированных подов
echo "🔹 Deleting completed and evicted pods..."
kubectl get pods --all-namespaces --field-selector=status.phase=Succeeded -o jsonpath='{range .items[*]}{.metadata.namespace}{" "}{.metadata.name}{"\n"}{end}' \
  | while read namespace pod; do
      kubectl delete pod "$pod" -n "$namespace" --ignore-not-found
    done

kubectl get pods --all-namespaces --field-selector=status.phase=Failed -o json \
  | jq -r '.items[] | select(.status.reason=="Evicted") | [.metadata.namespace, .metadata.name] | @tsv' \
  | while IFS=$'\t' read -r namespace pod; do
      kubectl delete pod "$pod" -n "$namespace" --ignore-not-found
    done

# 2. Очистка завершённых jobs
echo "🔹 Deleting completed jobs..."
kubectl get jobs --all-namespaces -o json \
  | jq -r '.items[] | select(.status.succeeded==1 or .status.failed==1) | [.metadata.namespace, .metadata.name] | @tsv' \
  | while IFS=$'\t' read -r namespace job; do
      kubectl delete job "$job" -n "$namespace" --ignore-not-found
    done

# 3. Очистка образов containerd
echo "🔹 Removing unused container images..."
crictl rmi --prune

# 4. Очистка логов и временных данных
echo "🔹 Cleaning old logs..."
sudo journalctl --vacuum-time=1d
sudo rm -rf /var/log/pods/*
sudo rm -rf /var/log/containers/*

# 5. Проверка занимаемого пространства
echo "📦 Disk usage after cleanup:"
df -h | grep -E 'Filesystem|/var|/'

echo "✅ Cleanup complete!"
