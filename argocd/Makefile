.PHONY: argocd-secret
argocd-secret:
	./scripts/core/argocd/create-argo-git-secret.sh

# ---
# Kubernetes/ArgoCD clean-up targets (kubectl, не Terragrunt)
.PHONY: argocd-apps-destroy argocd-ns-destroy argocd-clean

# Удалить все приложения (app-of-apps и дочерние)
argocd-apps-destroy:
	kubectl apply -f ./argocd/argocd/app-of-apps.yaml -n argocd || true

# Удалить namespace argocd полностью (все ресурсы ArgoCD)
argocd-ns-destroy:
	kubectl delete namespace argocd || true

# Полная очистка всего, что связано с ArgoCD (секреты, приложения, namespace)
argocd-clean: argocd-apps-destroy argocd-ns-destroy

# ---
# Terragrunt destroy (инфраструктура, НЕ приложения)
.PHONY: destroy k3s-destroy traefik-destroy cert-manager-destroy vault-destroy argocd-destroy
