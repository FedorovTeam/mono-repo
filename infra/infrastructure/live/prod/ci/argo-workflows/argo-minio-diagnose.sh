#!/usr/bin/env bash
# ------------------------------------------------------------------
# Argo × MinIO диагностика artifactRepository  (v2.1, 2025-06-14)
# ------------------------------------------------------------------
# Проверяет:
#   1) ConfigMap с artifactRepository.s3
#   2) Secret с accesskey / secretkey
#   3) Связку deployment workflow-controller ↔ ConfigMap
#   4) Подключение к MinIO через mc (если установлен)
#
#   Usage: ./argo-minio-diagnose.sh [NAMESPACE]
#          ARGO_CM=<cm_name> ./argo-minio-diagnose.sh   # указать CM вручную
# ------------------------------------------------------------------
set -euo pipefail

NS="${1:-argo}"                                   # namespace по умолчанию argo
# Если задан ARGO_CM — это именно имя CM (без префикса)
CM_NAME="${ARGO_CM:-}"

cyan(){ printf '\e[36m%s\e[0m\n' "$*"; }
red(){  printf '\e[31m%s\e[0m\n' "$*"; exit 1; }

echo "🔍  Namespace: $NS"

# 1) Найти ConfigMap
if [[ -n "$CM_NAME" ]]; then
  NAME="$CM_NAME"
else
  # получаем строку "configmap/имя..."
  CM_FULL=$(kubectl -n "$NS" get cm -o name \
      | grep -E '^configmap/argo-workflows(-[a-z0-9]+)?-workflow-controller-configmap$' \
      | grep -v -- '-crds-' \
      | head -n1)
  [[ -n "$CM_FULL" ]] || red "❌  ConfigMap workflow-controller не найден в ns/$NS"
  NAME="${CM_FULL#configmap/}"
fi
cyan "✅  ConfigMap: $NAME"

# 2) Вытащить data.config
CFG=$(kubectl -n "$NS" get cm "$NAME" -o jsonpath='{.data.config}' 2>/dev/null) \
    || red "❌  В CM $NAME нет ключа .data.config"
[[ -n "$CFG" ]] || red "❌  data.config пустой в CM $NAME"
cyan "✅  Извлечён data.config (${#CFG} байт)"

# 3) Распарсить S3-настройки
bucket=$(printf '%s' "$CFG" | yq eval '.artifactRepository.s3.bucket // ""' -)
endpoint=$(printf '%s' "$CFG" | yq eval '.artifactRepository.s3.endpoint // ""' -)
insecure=$(printf '%s' "$CFG" | yq eval '.artifactRepository.s3.insecure // false' -)
akName=$(printf '%s' "$CFG" | yq eval '.artifactRepository.s3.accessKeySecret.name // ""' -)
skName=$(printf '%s' "$CFG" | yq eval '.artifactRepository.s3.secretKeySecret.name // ""' -)

[[ -n "$bucket" && -n "$endpoint" ]] \
  || red "❌  Нет блока artifactRepository.s3 в data.config"
cyan "✅  S3 → bucket=$bucket  endpoint=$endpoint  insecure=$insecure"

# 4) Проверить Secret-ы
for sec in "$akName" "$skName"; do
  kubectl -n "$NS" get secret "$sec" &>/dev/null \
    || red "❌  Secret \"$sec\" не найден в ns/$NS"
done
cyan "✅  Secret-ы $akName / $skName есть"
AK=$(kubectl -n "$NS" get secret "$akName" -o jsonpath='{.data.accesskey}' | base64 -d)
SK=$(kubectl -n "$NS" get secret "$skName" -o jsonpath='{.data.secretkey}' | base64 -d)
echo "🔑  AccessKey: ${AK:0:4}… (len=${#AK})"

# 5) Проверить, что контроллер использует именно этот CM
controller=$(kubectl -n "$NS" get deploy -l app.kubernetes.io/component=workflow-controller \
               -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)
[[ -n "$controller" ]] || red "❌  Deployment workflow-controller не найден"
args=$(kubectl -n "$NS" get deploy "$controller" -o json \
       | jq -r '.spec.template.spec.containers[0].args[]' | xargs)
echo "🔗  Args контроллера: $args"
echo "$args" | grep -qw -- "$NAME" \
  || red "❌  Контроллер не использует CM \"$NAME\" — перезапустите:\n    kubectl -n $NS rollout restart deploy/$controller"
cyan "✅  Контроллер ссылается на CM \"$NAME\""

# 6) Проверка доступа к MinIO через mc (если установлен)
if ! command -v mc &>/dev/null; then
  red "⚠️  mc не установлен — проверка подключения к MinIO пропущена"
fi

mc alias rm argo-minio &>/dev/null || true
mc alias set argo-minio "$endpoint" "$AK" "$SK" --api S3v4 \
   $( [[ "$insecure" == "true" ]] && echo "--insecure" )

mc ls "argo-minio/$bucket" &>/dev/null \
  || red "❌  mc не видит бакет \"$bucket\" — проверьте endpoint/TLS/креды"
cyan "✅  Доступ к бакету \"$bucket\" подтверждён"

echo "📥  Загружаю тестовый файл…"
echo "argo-diagnose $(date)" | mc pipe argo-minio/"$bucket"/argo-diag-test.txt
mc rm argo-minio/"$bucket"/argo-diag-test.txt &>/dev/null

cyan "🎉  Диагностика завершена успешно — Argo Workflows сможет писать артефакты в MinIO."
