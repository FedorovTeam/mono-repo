#!/usr/bin/env bash
# ------------------------------------------------------------
# Argo Workflows + GitHub PAT диагностика   (v0.2)
# ------------------------------------------------------------
# Проверяет:
#  1) наличие секрета github-secret и ключей username/password
#  2) корректность формата PAT (начинается с ghp_ или github_pat_)
#  3) наличие переменных GIT_USERNAME / GIT_TOKEN в активном git-clone pod
#  4) совпадение значений PAT в секрете и в pod'е
#
# Usage:  ./argo-github-diagnose.sh [NAMESPACE] [WORKFLOW_NAME]
#   NAMESPACE      – namespace Argo (default: argo)
#   WORKFLOW_NAME  – если указан, ищет pod внутри конкретного Workflow
#                    иначе берёт самый свежий pod c названием "*git-clone*"
# ------------------------------------------------------------
set -euo pipefail
NS="${1:-argo}"
WF="${2:-}"

cyan() { printf '\e[36m%s\e[0m\n' "$*"; }
red()  { printf '\e[31m%s\e[0m\n' "$*"; exit 1; }

echo "🔍  Namespace: $NS"

# ------------------------------------------------------------------
# 1. Проверяем секрет github-secret
# ------------------------------------------------------------------
if ! kubectl -n "$NS" get secret github-secret &>/dev/null; then
  red "❌ секрет github-secret не найден в namespace $NS"
fi
cyan "✅ secret/github-secret найден"

SEC=$(kubectl -n "$NS" get secret github-secret -o json)

userKey=$(echo "$SEC" | jq -r '.data.username?')
passKey=$(echo "$SEC" | jq -r '.data.password?')

[[ "$userKey" == "null" ]] && red "❌ в секрете нет ключа username"
[[ "$passKey" == "null" ]] && red "❌ в секрете нет ключа password"

USERNAME=$(echo "$userKey" | base64 -d)
PAT=$(echo "$passKey" | base64 -d)

cyan "   • username: $USERNAME"
[[ $PAT == ghp_* || $PAT == github_pat_* ]] \
  && cyan "   • PAT формат OK (${PAT:0:10}…)" \
  || red  "❌ PAT не похож на GitHub Personal Access Token"

# ------------------------------------------------------------------
# 2. Находим pod git-clone
# ------------------------------------------------------------------
if [[ -z $WF ]]; then
  POD=$(kubectl -n "$NS" get pod -o json \
        | jq -r '.items[] | select(.metadata.name|test("git-clone")) | .metadata.name' \
        | sort | tail -n1)
else
  POD=$(kubectl -n "$NS" get pod -l "workflows.argoproj.io/workflow=$WF" -o json \
        | jq -r '.items[] | select(.metadata.name|test("git-clone")) | .metadata.name' \
        | sort | tail -n1)
fi

[[ -z $POD ]] && red "❌ git-clone pod не найден (Workflow ещё не стартовал?)"
cyan "✅ найден pod: $POD"

# ------------------------------------------------------------------
# 3. Считываем переменные в контейнере main
# ------------------------------------------------------------------
ENV=$(kubectl -n "$NS" exec "$POD" -c main -- printenv 2>/dev/null | grep '^GIT_' || true)

echo "🔗 env в pod:"
echo "$ENV" | sed 's/^/   /'

podUser=$(echo "$ENV" | grep ^GIT_USERNAME= | cut -d= -f2- || true)
podPat=$(echo "$ENV" | grep ^GIT_TOKEN=    | cut -d= -f2- || true)

[[ -z $podUser ]] && red "❌ В pod нет переменной GIT_USERNAME"
[[ -z $podPat  ]] && red "❌ В pod нет переменной GIT_TOKEN"

[[ "$podPat" == ghp_* || "$podPat" == github_pat_* ]] \
  && cyan "   • PAT в pod выглядит корректно (${podPat:0:10}…)" \
  || red  "❌ PAT в pod не похож на GitHub токен"

# ------------------------------------------------------------------
# 4. Сравниваем значения секрета и pod'а
# ------------------------------------------------------------------
[[ "$USERNAME" == "$podUser" ]] || red "❌ username в pod отличается от секрета"
[[ "$PAT"      == "$podPat"  ]] || red "❌ PAT в pod отличается от секрета"

cyan "🎉  Диагностика успешно завершена — секрет и pod настроены правильно."
