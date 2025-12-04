# Fedorov-Team Kubernetes Infrastructure

Инфраструктура FedorovTeam с поддержкой Kubernetes, OpenTofu, Terragrunt, Traefik, cert-manager. Подход основан на этапной установке с возможностью добавления новых модулей.

## Структура

* **charts/** — Helm values для сервисов (Traefik и пр.).
* **manifests/** — Kubernetes YAML манифесты для демо-сервисов и тестов.
* **infrastructure/live/dev|prod/** — Terragrunt конфигурация по окружениям.
* **modules/** — OpenTofu модули (Helm chart, CRD, manifest).
* **scripts/** — Bash-скрипты для установки Docker, k3s, OpenTofu, Terragrunt.

## Этапы установки

### Этап 0 — Подготовка среды

```bash
./scripts/docker/setup_docker.sh
./scripts/k3s/setup_k3s.sh
./scripts/opentofu/install-opentofu.sh
./scripts/terragrunt/install-terragrunt.sh
```

Убедись, что кластер работает:

```bash
kubectl get nodes
```

---

### Этап 1 — Установка cert-manager

```bash
cd infrastructure/live/prod/core/cert-manager
terragrunt apply
```

* Установка cert-manager с CRD
* Создан ClusterIssuer Let's Encrypt

---

### Этап 2 — Установка Traefik

```bash
cd ../traefik
kubectl get ingressclass -A # если уже имеется Ингрес - нужно удалить
kubectl delete ingressclass traefik -n kube-system
# потом применить
terragrunt apply
```

* Traefik устанавливается с Helm
* IngressClass, dashboard, HTTPS
* Traefik использует TLS-секреты, созданные cert-manager

---

### Этап 3 — Тестовое Ingress-приложение

```bash
kubectl apply -f manifests/whoami/whoami-ingress.yaml
```

* Сервис whoami (HTTP)
* Ingress на `https://whoami.fedorov-team.tech`
* cert-manager выдает сертификат

---

### Этап 4 — Установка ArgoCD

```bash
cd ../argocd
terragrunt apply
```

* ArgoCD устанавливается через Helm
* Доступ по `https://argocd.tech.fedorov-team.tech`

---

Требования
* Kubernetes 1.23+
* Helm 3+
* OpenTofu >= 1.6
* Terragrunt >= 0.48
* DNS: доступные A/сNAME записи на IP нод кластера
