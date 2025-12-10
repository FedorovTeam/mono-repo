## Пререквизиты

1. Клонировать репозиторий и перейти в каталог проекта.
2. Если установка идёт в среде, где требуется Docker, выполнить `scripts/docker/setup_docker.sh`.
3. Мастер-нод K3s устанавливается с флагом `--disable traefik`; версия K3s берётся из `scripts/k3s/setup_k3s.sh` и должна быть `v1.29.3+k3s1`.

## Как получить `K3S_URL` и `K3S_TOKEN` на мастер-ноде

1. Токен: `sudo cat /var/lib/rancher/k3s/server/node-token`.
2. Адрес API (`K3S_URL`): внешний IP или hostname мастер-ноды можно посмотреть через `kubectl get nodes -o wide` и использовать вместе с портом `6443`, например `https://<external-ip>:6443`.

## Установка воркера

Вариант 1 — через curl с подстановкой переменных:

```shell
curl -sfL https://get.k3s.io |
  K3S_URL=https://<external-ip-or-hostname>:6443 \
  K3S_TOKEN="<node-token-from-master>" \
  INSTALL_K3S_VERSION="v1.29.3+k3s1" \
  sh -
```

Вариант 2 — через готовый скрипт: `infra/scripts/k3s/setup_worker_node.sh`, передав значения `K3S_URL`, `K3S_TOKEN` и `INSTALL_K3S_VERSION` (`v1.29.3+k3s1`).

## Проверка

1. На мастер-ноде выполнить `kubectl get nodes` и убедиться, что новая нода находится в статусе `Ready`.
2. После успешного присоединения удалить из окружения чувствительные переменные (`unset K3S_URL K3S_TOKEN INSTALL_K3S_VERSION`).