# Fedorov-Team Kubernetes Infrastructure

Инфраструктура FedorovTeam с поддержкой Kubernetes, OpenTofu, Terragrunt, Traefik, cert-manager. Подход основан на этапной установке с возможностью добавления новых модулей.

## Структура

* **charts/** — Helm values для сервисов (Traefik и пр.).
* **manifests/** — Kubernetes YAML манифесты для демо-сервисов и тестов.
* **infrastructure/live/dev|prod/** — Terragrunt конфигурация по окружениям.
* **modules/** — OpenTofu модули (Helm chart, CRD, manifest).
* **scripts/** — Bash-скрипты для установки Docker, k3s, OpenTofu, Terragrunt.

---

## Основной гайд по установке

### 0. Подготовка среды

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

### 1. Сеть и внешние IP

#### 1.1. MetalLB Operator / Controller

```bash
cd infrastructure/live/prod/core/metallb-install
terragrunt apply
```

Проверка:

```bash
kubectl get pods -n metallb-system
```

#### 1.2. IPAddressPool + L2Advertisement

```bash
cd ../metallb
terragrunt apply
```

Проверка:

```bash
kubectl get ipaddresspools.metallb.io -A
kubectl get l2advertisements.metallb.io -A
```

---

### 2. cert-manager + ClusterIssuer

#### 2.1. cert-manager (Helm через Terragrunt)

```bash
cd ../cert-manager
terragrunt apply
```

Проверка:

```bash
kubectl get pods -n cert-manager
```

#### 2.2. ClusterIssuer Let’s Encrypt

```bash
cd ../cert-manager-bootstrap
terragrunt apply
```

Проверка:

```bash
kubectl get clusterissuer
kubectl describe clusterissuer letsencrypt
```

---

### 3. Traefik Ingress Controller

```bash
cd ../traefik
kubectl get ingressclass -A # если уже имеется IngressClass — нужно удалить
kubectl delete ingressclass traefik -n kube-system
terragrunt apply
```

Проверка:

```bash
kubectl get pods -n traefik
kubectl get svc -n traefik
```

Убедиться, что:

- есть `LoadBalancer` service;
- он получил IP из пула MetalLB;
- http → https редирект работает (это проверяется на `whoami`).

---

### 4. Тестовый Ingress

```bash
kubectl apply -f manifests/whoami/whoami-ingress.yaml
```

* Сервис whoami (HTTP)
* Ingress на `https://whoami.fedorov-team.tech`
* cert-manager выдает сертификат

---

### 5. Базы: Redis и PostgreSQL

#### 5.1. Redis

1. Если старый релиз живёт от старого Helm-чарта — удалить:

    ```bash
    kubectl delete ns redis --ignore-not-found=true
    ```

2. Применить Terragrunt:

    ```bash
    cd infrastructure/live/prod/databases/redis
    terragrunt apply
    ```

Проверка:

```bash
kubectl get pods -n redis
kubectl get pvc -n redis
```

#### 5.2. PostgreSQL

1. Создать секрет с паролем **до** деплоя (чтобы контейнер не падал):

    ```bash
    kubectl create ns postgres --dry-run=client -o yaml | kubectl apply -f -
    kubectl create secret generic postgres-secret \
      --from-literal=postgres-password='3&!tAL5bf38yfx' \
      -n postgres
    ```

2. Применить модуль:

    ```bash
    cd ../postgres
    terragrunt apply
    ```

Проверка:

```bash
kubectl get pods -n postgres
kubectl logs -n postgres postgres-postgresql-0
```

Проверка подключения:

```bash
kubectl run psql-client --rm -it --restart=Never \
  --namespace postgres \
  --image=bitnami/postgresql:15 \
  --env="PGPASSWORD=3&!tAL5bf38yfx" \
  -- psql -h postgresql -U postgres -d app
```

(пароль в кавычки из-за `!` в bash, если вводишь в CLI).

---

### 6. MinIO

#### 6.1. MinIO Operator

```bash
cd ../../minio/operator
terragrunt apply
```

Перед этим — твоя аннотация CRD, если она ещё не сделана:

```bash
kubectl annotate crd policybindings.sts.min.io \
  meta.helm.sh/release-name=minio-operator \
  meta.helm.sh/release-namespace=minio-operator \
  --overwrite
```

Потом — сертификат для operator console:

```bash
kubectl apply -f operator-console-certificate.yaml
```

Проверка:

```bash
kubectl get pods -n minio-operator
kubectl get certificate -n minio-operator
```

#### 6.2. MinIO Tenant

1. Экспортируем креды:

    ```bash
    export MINIO_ROOT_USER="Burdensome3253"
    export MINIO_ROOT_PASSWORD="1545Upz_9jwI58__"
    ```

2. Применяем tenant:

    ```bash
    cd ../tenant
    terragrunt apply
    ```

Проверка:

```bash
kubectl get pods -n minio
kubectl get tenant -n minio
kubectl get certificate -n minio
```

Потом проверяем доступ:

- `https://api.minio.tech.fedorov-team.tech`
- `https://console.minio.tech.fedorov-team.tech`

---

### 7. ArgoCD

Когда:

- Traefik + cert-manager уже работают,
- ClusterIssuer `letsencrypt` есть.

Тогда:

```bash
cd ../../core/argocd
terragrunt apply
```

Проверка:

```bash
kubectl get pods -n argocd
kubectl get ingress -n argocd
kubectl get certificate -n argocd
```

Зайти в UI:

- `https://argocd.tech.fedorov-team.tech`

Потом:

- создать `Application` (App-of-Apps),
- проверить, что ArgoCD видит `git@github.com:FedorovTeam/mono-repo.git`,
- синкануть.

---

### 8. Проверка end-to-end

1. `whoami` через Traefik по https — уже ОК.
2. ArgoCD открывается по https и даёт логин.
3. MinIO console открывается, можно залогиниться root-кредами.
4. Redis и PostgreSQL — Running, коннектятся из k8s.

---

## Требования
* Kubernetes 1.23+
* Helm 3+
* OpenTofu >= 1.6
* Terragrunt >= 0.48
* DNS: доступные A/сNAME записи на IP нод кластера
