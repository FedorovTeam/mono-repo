#!/bin/bash
set -e

echo "=== Argo Workflows Diagnostics ==="
echo "Date: $(date)"
echo

echo "=== Namespaces ==="
kubectl get namespaces | grep -E "(argo|NAME)"
echo

echo "=== Pods in argo namespace ==="
kubectl get pods -n argo -o wide
echo

echo "=== Services in argo namespace ==="
kubectl get svc -n argo
echo

echo "=== Ingress in argo namespace ==="
kubectl get ingress -n argo
echo

echo "=== ServiceAccounts in argo namespace ==="
kubectl get sa -n argo
echo

echo "=== Secrets in argo namespace ==="
kubectl get secrets -n argo
echo

echo "=== CRDs related to Argo ==="
kubectl get crd | grep argoproj
echo

echo "=== ConfigMaps in argo namespace ==="
kubectl get cm -n argo
echo

echo "=== ClusterRoles related to workflows ==="
kubectl get clusterroles | grep -i workflow
echo

echo "=== ClusterRoleBindings related to workflows ==="
kubectl get clusterrolebindings | grep -i workflow
echo

echo "=== Helm releases ==="
helm list -n argo
echo

echo "=== WorkflowTemplates ==="
kubectl get workflowtemplate -n argo 2>/dev/null || echo "No WorkflowTemplates found"
echo

echo "=== Workflows ==="
kubectl get workflow -n argo 2>/dev/null || echo "No Workflows found"
echo

echo "=== Recent events in argo namespace ==="
kubectl get events -n argo --sort-by='.lastTimestamp' | tail -20
echo

echo "=== Argo server logs (last 50 lines) ==="
kubectl logs -n argo deployment/argo-workflows-server --tail=50 2>/dev/null || echo "Could not get server logs"
echo

echo "=== Argo controller logs (last 50 lines) ==="
kubectl logs -n argo deployment/argo-workflows-workflow-controller --tail=50 2>/dev/null || echo "Could not get controller logs"
echo

echo "=== Terraform state resources ==="
echo "Current Terraform resources:"
cd ./infrastructure/live/prod/ci/argo-workflows
terragrunt show 2>/dev/null | grep -E "^resource|^data" || echo "Could not get terraform state"

echo
echo "=== End of diagnostics ==="
