#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

shopt -s nullglob
GOOGLE_PROTO_ROOTS=( "$(go env GOMODCACHE)"/github.com/gogo/protobuf@*/protobuf )
shopt -u nullglob

INCLUDE_OPTS=()
for root in "${GOOGLE_PROTO_ROOTS[@]:-}"; do
  INCLUDE_OPTS+=("-I" "$root")
done

protoc -I. "${INCLUDE_OPTS[@]}" \
  --go_out=. --go_opt=paths=source_relative \
  --go-grpc_out=. --go-grpc_opt=paths=source_relative \
  backend/tableextractor/v1/tableextractor.proto
