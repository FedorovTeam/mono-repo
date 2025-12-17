#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PROTO_DIR="${ROOT_DIR}/proto"
TESTDATA_DIR="${ROOT_DIR}/testdata"
SAMPLE_FILE="${TESTDATA_DIR}/sample.docx"
MINIO_MC_IMAGE="${MINIO_MC_IMAGE:-minio/mc:RELEASE.2024-06-11T03-13-30Z}"
GRPCURL_IMAGE="${GRPCURL_IMAGE:-fullstorydev/grpcurl:v1.9.1}"

if [ ! -f "${SAMPLE_FILE}" ]; then
  echo "Не найден файл с тестовым образцом: ${SAMPLE_FILE}" >&2
  exit 1
fi

echo "Загружаем образец в MinIO исходный бакет..."
docker run --rm --network host \
  -v "${TESTDATA_DIR}:/data" \
  "${MINIO_MC_IMAGE}" \
  /bin/sh -c "\
  mc alias set local http://localhost:9000 PREPROCESS_MINIO_KEY PREPROCESS_MINIO_SECRET && \
  mc mb -p local/preprocess-source || true && \
  mc mb -p local/preprocess-artifacts || true && \
  mc cp /data/sample.docx local/preprocess-source/incoming/demo.docx"

echo "Отправляем SubmitPreprocess через grpcurl..."
docker run --rm --network host \
  -v "${PROTO_DIR}:/proto" \
  "${GRPCURL_IMAGE}" \
  -plaintext \
  -import-path /proto \
  -proto /proto/preprocess.proto \
  -d '{
    "source_bucket": "preprocess-source",
    "source_key": "incoming/demo.docx",
    "source_etag": "etag-demo-1",
    "target_bucket": "preprocess-artifacts",
    "target_key": "converted/demo.docx",
    "document_id": "doc-demo-1",
    "correlation_id": "corr-demo-1"
  }' \
  localhost:9082 backend.preprocess.v1.PreprocessService/SubmitPreprocess

echo "Готово. Используйте job_id из ответа выше, чтобы опросить статус задачи."
