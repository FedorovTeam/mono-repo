# 🧱 Fedorov-Team Core Infrastructure Setup

> Это руководство поможет установить и проверить базовые компоненты инфраструктуры: cert-manager, Traefik, MetalLB и тестовый Ingress.
>

---

## 📦 Компоненты ядра

- **cert-manager**: автоматическая выдача TLS-сертификатов (Let's Encrypt)
- **traefik**: Ingress-контроллер с поддержкой HTTPS и Middleware
- **metallb**: Балансировщик L2 (для bare metal / VDS)
- **whoami**: тестовое приложение для проверки HTTPS-доступа

---

## 🚀 Установка шаг за шагом

### 1. Установка cert-manager

```bash
cd infrastructure/live/prod/core/cert-manager
terragrunt apply
```

### ✅ Проверка:

```bash
kubectl get crd | grep clusterissuers
```

---

### 2. Настройка ClusterIssuer (Let's Encrypt)

```bash
cd ../cert-manager-bootstrap
terragrunt apply
```

---

### 3. Установка Traefik

```bash
cd ../traefik
kubectl delete ingressclass traefik  # Предварительно удалить, если уже существует
terragrunt apply
```

### ✅ Проверка:

```bash
kubectl get svc -n traefik
kubectl get ingressclass
```

---

### 4. Установка MetalLB

### 4.1 Установить Helm-чарт + CRD

```bash
cd ../../metallb-install
terragrunt apply
```

### ✅ Проверка CRD:

```bash
kubectl get crd | grep metallb
```

### 4.2 Настройка IPAddressPool и L2Advertisement

```bash
cd ../metallb
terragrunt apply
```

### ✅ Проверка:

```bash
kubectl get ipaddresspools.metallb.io -n metallb-system
kubectl get l2advertisements.metallb.io -n metallb-system
```

---

## 🧪 Тестирование HTTPS‑Ingress

```bash
kubectl apply -f manifests/test/whoami-ingress.yaml
```

### ✅ Проверка:

```bash
kubectl get ingress
kubectl describe ingress whoami
curl -vk https://whoami.fedorov-team.tech
```

---

## 📂 Структура проекта (фрагмент)

```
infrastructure/
  └── live/
      └── prod/
          └── core/
              ├── cert-manager/
              ├── cert-manager-bootstrap/
              ├── traefik/
              ├── metallb-install/
              ├── metallb/
              └── vault-csi-provider/
```

---

## 📘 Примечания

- `cert-manager` использует ClusterIssuer с Let's Encrypt.
- `traefik` использует `websecure` как entrypoint с автоматической TLS-конфигурацией.
- `metallb` назначает внешний IP из диапазона, заданного в `IPAddressPool`.