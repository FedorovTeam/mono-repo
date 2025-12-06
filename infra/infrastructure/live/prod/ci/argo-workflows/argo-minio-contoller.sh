#!/usr/bin/env bash
# ------------------------------------------------------------------
# Argo × MinIO диагностика artifactRepository  (v2.1, 2025-06-14)
# ------------------------------------------------------------------
set -euo pipefail

NS="${1:-argo}"            # namespace, по умолчанию argo
CM_NAME="${ARGO_CM:-}"     # можно задать вручную ARGO_CM=...
cyan(){ printf '\e[36m%s\e[0m\n' "$*"; }
red(){  printf '\e[31m%s\e[0m\n' "$*"; exit 1; }

echo "🔍  Namespace: $NS"

# 1) Найти ConfigMap вашего workflow-controller
if [[ -n "$CM_NAME" ]]; then
  CM="configmap/${CM_NAME}"
else
  CM=$(kubectl -n "$NS" get cm -o name \
        | grep -E '^configmap/argo-workflows(-[a-z0-9]+)?-workflow-controller-configmap$' \
        | grep -v '\-crds\-' \
        | head -1)
fi
[[ -n "$CM" ]] || red "❌  Не найден ConfigMap workflow-controller в ns/$NS"
cyan "✅  ConfigMap: $CM"

# 2) Забрать data.config
CFG=$(kubectl -n "$NS" get "$CM" -o jsonpath='{.data.config}' 2>/dev/null) \
    || red "❌  В $CM нет ключа data.config"
[[ -n "$CFG" ]] || red "❌  data.config пустой"
cyan "✅  Получили data.config (${#CFG} байт)"

# 3) Распарсить S3-настройки
bucket=$(printf '%s' "$CFG" | yq e '.artifactRepository.s3.bucket // ""' -)
endpoint=$(printf '%s' "$CFG" | yq e '.artifactRepository.s3.endpoint // ""' -)
insecure=$(printf '%s' "$CFG" | yq e '.artifactRepository.s3.insecure // false' -)
akName=$(printf '%s' "$CFG" | yq e '.artifactRepository.s3.accessKeySecret.name // ""' -)
skName=$(printf '%s' "$CFG" | yq e '.artifactRepository.s3.secretKeySecret.name // ""' -)
[[ -n "$bucket" && -n "$endpoint" ]] || red "❌  Нет блока artifactRepository.s3 в data.config"
cyan "✅  S3 → bucket=$bucket  endpoint=$endpoint  insecure=$insecure"

# 4) Проверить, что существуют secret’ы с ключами
for sec in "$akName" "$skName"; do
  kubectl -n "$NS" get secret "$sec" &>/dev/null \
    || red "❌  Secret \"$sec\" не найден в ns/$NS"
done
cyan "✅  Secret’ы $akName / $skName на месте"

AK=$(kubectl -n "$NS" get secret "$akName" -o=jsonpath='{.data.accesskey}' | base64 -d)
SK=$(kubectl -n "$NS" get secret "$skName" -o=jsonpath='{.data.secretkey}' | base64 -d)
echo "🔑  AccessKey: ${AK:0:4}… (len=${#AK})"

# 5) Найти правильный deployment workflow-controller из вашего релиза
controller=$(kubectl -n "$NS" get deploy \
  -l app.kubernetes.io/component=workflow-controller,app.kubernetes.io/instance=argo-workflows \
  -o jsonpath='{.items[0].metadata.name}' 2>/dev/null)
[[ -n "$controller" ]] || red "❌  Deployment argo-workflows-workflow-controller не найден"
cyan "✅  Нашли Deployment: $controller"

args=$(kubectl -n "$NS" get deploy "$controller" -o json \
       | jq -r '.spec.template.spec.containers[0].args[]' | xargs)
echo "🔗  Args контроллера: $args"
echo "$args" | grep -q -- "${CM#configmap/}" \
  || red "❌  Controller не ссылается на $CM\; перезапустите:\n    kubectl -n $NS rollout restart deploy/$controller"
cyan "✅  Controller ссылается на нужный ConfigMap"

# 6) (опционально) Проверить доступ через mc, если установлен
if command -v mc &>/dev/null; then
  mc alias rm argo-minio >/dev/null 2>&1 || true
  mc alias set argo-minio "$endpoint" "$AK" "$SK" --api S3v4 \
     $( [[ "$insecure" == "true" ]] && echo "--insecure" )
  mc ls "argo-minio/$bucket" >/dev/null 2>&1 \
    && cyan "✅  mc видит бакет \"$bucket\"" \
    || red "❌  mc не видит бакет \"$bucket\" — проверьте endpoint/TLS/креды"

  echo "📥  Тест upload…"
  echo "argo-diagnose" | mc pipe "argo-minio/$bucket/argo-diag.txt"
  mc rm "argo-minio/$bucket/argo-diag.txt" >/dev/null 2>&1
  cyan "🎉  Тестовый файл успешно записался и удалился"
else
  red "⚠️  mc (MinIO Client) не установлен — пропускаем проверку соединения"
fi

echo "🏁 Все OK! Argo Workflows сможет писать артефакты в MinIO."
