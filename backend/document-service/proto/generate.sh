#!/usr/bin/env bash
set -euo pipefail
SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
cd "$SCRIPT_DIR"
MOD_CACHE=$(go env GOMODCACHE)
protoc -I. -I"$MOD_CACHE" -I/usr/include \
  --go_out=. --go_opt=paths=source_relative \
  --go-grpc_out=. --go-grpc_opt=paths=source_relative \
  backend/document/v1/document.proto \
  backend/storage/v1/storage.proto \
  backend/transformer/v1/transformer.proto \
  backend/githubpublisher/v1/githubpublisher.proto
