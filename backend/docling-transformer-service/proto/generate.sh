#!/usr/bin/env bash
set -euo pipefail
SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
PROJECT_ROOT=$(cd "$SCRIPT_DIR/.." && pwd)
SOURCE_PROTO="$PROJECT_ROOT/../document-service/proto/backend/transformer/v1/transformer.proto"
TARGET_PROTO="$SCRIPT_DIR/backend/transformer/v1/transformer.proto"

if [[ ! -f "$SOURCE_PROTO" ]]; then
  echo "Source proto not found: $SOURCE_PROTO" >&2
  exit 1
fi

temp_proto=$(mktemp)
cp "$SOURCE_PROTO" "$temp_proto"
# Align go_package with the current module while keeping the canonical schema from document-service.
sed -i 's#backend-document-service/proto/backend/transformer/v1;transformerv1#github.com/mono-repo/docling-transformer-service/proto/backend/transformer/v1;transformerv1#' "$temp_proto"

mkdir -p "$(dirname "$TARGET_PROTO")"
mv "$temp_proto" "$TARGET_PROTO"

cd "$SCRIPT_DIR"
protoc -I. \
  --go_out=. --go_opt=paths=source_relative \
  --go-grpc_out=. --go-grpc_opt=paths=source_relative \
  backend/transformer/v1/transformer.proto
