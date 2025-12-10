#!/usr/bin/env bash
set -euo pipefail

DOMAINS=("$@")

divider() {
  echo
  echo "================================================================="
  echo "== $1"
  echo "================================================================="
}

header() {
  echo
  echo "---- $1 ----"
}

divider "1. Общая информация о хосте"

header "1.1. Hostname и время"
hostnamectl || hostname
date

header "1.2. IP-адреса интерфейсов"
ip addr show

header "1.3. Маршруты"
ip route show

header "1.4. Таблица ARP"
ip neigh show || arp -an || true

divider "2. Сокеты и firewall"

header "2.1. Слушающие сокеты (TCP 80/443)"
ss -tulnp | egrep ':80 |:443 ' || echo "Ничего не слушает на 80/443"

header "2.2. UFW"
if command -v ufw >/dev/null 2>&1; then
  ufw status verbose || true
else
  echo "ufw не установлен"
fi

header "2.3. iptables (filter)"
iptables -L -n -v || echo "iptables недоступен"

header "2.4. iptables (nat)"
iptables -t nat -L -n -v || echo "iptables nat недоступен"

divider "3. Проверка DNS и HTTP(S) для доменов"

if [ "${#DOMAINS[@]}" -eq 0 ]; then
  echo "Доменов не передано. Можно вызвать так: $0 argocd.example.com console.minio.example.com"
else
  for d in "${DOMAINS[@]}"; do
    header "3.x. Домен: $d"
    if command -v dig >/dev/null 2>&1; then
      echo "dig +short $d:"
      dig +short "$d" || true
    else
      echo "dig не установлен, использую getent hosts:"
      getent hosts "$d" || true
    fi

    echo
    echo "curl -vk http://$d/ (5s timeout):"
    curl -vk "http://$d/" --connect-timeout 5 || echo "curl http://$d завершился с ошибкой"

    echo
    echo "curl -vk https://$d/ (5s timeout, insecure):"
    curl -vk "https://$d/" --connect-timeout 5 --insecure || echo "curl https://$d завершился с ошибкой"
  done
fi

divider "4. Kubernetes: ноды, сервисы, поды"

if ! command -v kubectl >/dev/null 2>&1; then
  echo "kubectl не найден, пропускаю k8s-диагностику"
  exit 0
fi

header "4.1. Ноды (с IP и ролями)"
kubectl get nodes -o wide

header "4.2. Все svc с типом LoadBalancer"
kubectl get svc -A | grep LoadBalancer || echo "LoadBalancer сервисов не найдено"

header "4.3. Traefik / Ingress-контроллеры"
echo "Сервисы с именем traefik:"
kubectl get svc -A | grep traefik || echo "svc traefik не найдены"

echo
echo "Поды с именем traefik:"
kubectl get pods -A -o wide | grep traefik || echo "pods traefik не найдены"

header "4.4. MetalLB (если есть)"
if kubectl get ns metallb-system >/dev/null 2>&1; then
  echo "IPAddressPools:"
  kubectl get ipaddresspool -n metallb-system || true
  echo
  echo "L2Advertisements:"
  kubectl get l2advertisement -n metallb-system || true
else
  echo "Namespace metallb-system отсутствует"
fi

header "4.5. kube-system pods"
kubectl get pods -n kube-system -o wide

divider "5. Traefik и ingress-сервисы"

# kube-system/traefik
if kubectl get svc -n kube-system traefik >/dev/null 2>&1; then
  header "5.1. kube-system/traefik svc"
  kubectl get svc -n kube-system traefik -o wide
  echo
  kubectl describe svc -n kube-system traefik | sed -n '1,120p'
else
  echo "kube-system/traefik svc отсутствует"
fi

# traefik/traefik
if kubectl get ns traefik >/dev/null 2>&1 && kubectl get svc -n traefik traefik >/dev/null 2>&1; then
  header "5.2. traefik/traefik svc"
  kubectl get svc -n traefik traefik -o wide
  echo
  kubectl describe svc -n traefik traefik | sed -n '1,120p'
else
  echo "traefik/traefik svc отсутствует или ns traefik не существует"
fi

divider "6. Проверка доступа до Traefik по IP (ClusterIP / EXTERNAL-IP)"

TRA_SERVICES=$(kubectl get svc -A | grep traefik || true)
if [ -n "$TRA_SERVICES" ]; then
  echo "$TRA_SERVICES" | while read -r ns name type cluster_ip ext_ip ports rest; do
    echo
    header "6.x. Service $ns/$name"
    echo "CLUSTER-IP: $cluster_ip"
    echo "EXTERNAL-IP: $ext_ip"
    echo "PORTS: $ports"

    if [ "$cluster_ip" != "<none>" ] && [ "$cluster_ip" != "" ]; then
      echo
      echo "curl -vk http://$cluster_ip/ (3s timeout)"
      curl -vk "http://$cluster_ip/" --connect-timeout 3 || echo "curl до CLUSTER-IP завершился с ошибкой"
    fi

    if [ "$ext_ip" != "<none>" ] && [ "$ext_ip" != "" ]; then
      echo
      echo "curl -vk http://$ext_ip/ (3s timeout)"
      curl -vk "http://$ext_ip/" --connect-timeout 3 || echo "curl до EXTERNAL-IP завершился с ошибкой"
    fi
  done
else
  echo "Traefik-сервисов не найдено"
fi

divider "7. Готово"

echo "Диагностика завершена."
echo "Запускали как: $0 ${DOMAINS[*]:-<no domains>}"
