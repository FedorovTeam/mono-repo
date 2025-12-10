#!/usr/bin/env bash
set -euo pipefail

# === НАСТРОЙКИ ===
PUBLIC_IP="80.74.29.116"

# NodePort'ы Traefik из описания сервиса traefik/traefik
TRAEFIK_NODEPORT_HTTP=32610     # web
TRAEFIK_NODEPORT_HTTPS=32015    # websecure

echo "== 1. Показываем текущие слушающие 80/443 на хосте =="
ss -tulnp | egrep ':80 |:443 ' || echo "Никто не слушает 80/443 (это нормально перед настройкой)"

echo
echo "== 2. Добавляем DNAT c 80/443 на NodePort Traefik =="

# Внешние запросы: dst=80.74.29.116:80 => 80.74.29.116:32610
RULE1="-A PREROUTING -p tcp -d ${PUBLIC_IP} --dport 80  -j DNAT --to-destination ${PUBLIC_IP}:${TRAEFIK_NODEPORT_HTTP}"
RULE2="-A PREROUTING -p tcp -d ${PUBLIC_IP} --dport 443 -j DNAT --to-destination ${PUBLIC_IP}:${TRAEFIK_NODEPORT_HTTPS}"

# Локальные curl на сам PUBLIC_IP тоже гоняем в NodePort (OUTPUT)
RULE3="-A OUTPUT -p tcp -d ${PUBLIC_IP} --dport 80  -j DNAT --to-destination ${PUBLIC_IP}:${TRAEFIK_NODEPORT_HTTP}"
RULE4="-A OUTPUT -p tcp -d ${PUBLIC_IP} --dport 443 -j DNAT --to-destination ${PUBLIC_IP}:${TRAEFIK_NODEPORT_HTTPS}"

add_rule_if_missing() {
  local table="$1"
  local rule="$2"

  if iptables-save -t "${table}" | grep -F -- "$rule" >/dev/null 2>&1; then
    echo "Правило уже есть в таблице ${table}: $rule"
  else
    echo "Добавляю в ${table}: $rule"
    # shellcheck disable=SC2086
    iptables -t "${table}" $rule
  fi
}

add_rule_if_missing nat "$RULE1"
add_rule_if_missing nat "$RULE2"
add_rule_if_missing nat "$RULE3"
add_rule_if_missing nat "$RULE4"

echo
echo "== 3. Делаем правила постоянными (iptables-persistent) =="

if ! command -v netfilter-persistent >/dev/null 2>&1; then
  export DEBIAN_FRONTEND=noninteractive
  apt-get update -y
  apt-get install -y iptables-persistent
fi

netfilter-persistent save

echo
echo "== 4. Быстрая проверка изнутри сервера =="

echo "Пробуем HTTP через NodePort:"
curl -vk "http://127.0.0.1:${TRAEFIK_NODEPORT_HTTP}/" --connect-timeout 5 || echo "curl к NodePort HTTP не удался"

echo
echo "Пробуем напрямую на PUBLIC_IP:80 (должно редиректнуть на https://):"
curl -vk "http://${PUBLIC_IP}/" --connect-timeout 5 || echo "curl к PUBLIC_IP:80 не удался"

echo
echo "Теперь с любой машины снаружи можно пробовать:"
echo "  curl -vk http://argocd.tech.fedorov-team.tech/"
echo "  curl -vk https://argocd.tech.fedorov-team.tech/"

echo
echo "ВАЖНО: предполагается, что Ingress для ArgoCD уже настроен в Traefik."
echo "Если его нет — могу дать пример манифеста для argocd.tech.fedorov-team.tech."
