cat > outline-manager-config.sh <<'BASH'
#!/usr/bin/env bash
set -euo pipefail

NS="${NS:-outline-vpn}"
DEPLOY="${DEPLOY:-shadowbox}"
TLS_SECRET="${TLS_SECRET:-shadowbox-tls}"
DOMAIN="${DOMAIN:-vpn.tech.fedorov-team.tech}"

need() { command -v "$1" >/dev/null 2>&1 || { echo "Missing required command: $1" >&2; exit 1; }; }
need kubectl
need openssl
need base64
need sed

# 1) Read SB_API_PREFIX from Deployment env
PREFIX="$(kubectl -n "$NS" get deploy "$DEPLOY" -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="SB_API_PREFIX")].value}')"
if [[ -z "${PREFIX}" ]]; then
  echo "ERROR: SB_API_PREFIX not found in deployment/$DEPLOY env (namespace=$NS)" >&2
  exit 1
fi

API_URL="https://${DOMAIN}/${PREFIX}"

# 2) Extract TLS cert from Secret and compute SHA256 fingerprint (no colons)
TMP_CRT="$(mktemp)"
trap 'rm -f "$TMP_CRT"' EXIT

kubectl -n "$NS" get secret "$TLS_SECRET" -o jsonpath='{.data.tls\.crt}' \
  | base64 -d > "$TMP_CRT"

CERT_SHA256="$(openssl x509 -in "$TMP_CRT" -noout -fingerprint -sha256 | sed 's/^.*=//; s/://g')"
if [[ -z "${CERT_SHA256}" ]]; then
  echo "ERROR: could not compute certSha256 from secret/$TLS_SECRET" >&2
  exit 1
fi

# 3) Optional health check
echo "Checking API: ${API_URL}/access-keys"
if curl -sk --max-time 5 "${API_URL}/access-keys" >/dev/null 2>&1; then
  echo "OK: API reachable"
else
  echo "WARN: API not reachable from this host. JSON still generated." >&2
fi

# 4) Print JSON for Outline Manager
cat <<JSON
{
  "apiUrl": "${API_URL}",
  "certSha256": "${CERT_SHA256}"
}
JSON
BASH

chmod +x outline-manager-config.sh
