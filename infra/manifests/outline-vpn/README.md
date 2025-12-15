# Outline VPN Server (shadowbox) via ArgoCD

## 1) Настрой домен
В `configmap-shadowbox.yaml` и `ingress-api.yaml` замени `vpn.tech.fedorov-team.tech` на свой домен.

## 2) TLS
Заполни `secret-shadowbox-tls.yaml` (tls.crt/tls.key).
Можно сделать self-signed:
openssl req -x509 -newkey rsa:2048 -nodes -keyout tls.key -out tls.crt -days 365 -subj "/CN=vpn.tech.fedorov-team.tech"

и вставить содержимое в secret.

## 3) Применение
ArgoCD синкнет сам. Либо:
kubectl apply -k k8s/outline-vpn

## 4) Проверка
Проверить, что запущен outline-ss-server:
kubectl -n outline-vpn exec -it deploy/shadowbox -- sh -lc 'ps auxww | egrep "outline-ss-server|main.js"'

Проверить API внутри кластера:
kubectl -n outline-vpn run -it --rm curl --image=curlimages/curl -- \
curl -vk https://shadowbox-api:12000/cjaorBbzegUKLvKxJhqzug/access-keys

Проверить внешний keys-port:
kubectl -n outline-vpn get svc shadowbox-keys -o wide

Важно: клиентам нужен именно порт 12001 TCP/UDP на внешнем IP сервиса shadowbox-keys.
