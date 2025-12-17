#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROTO_DIR="${ROOT_DIR}/proto"
TESTDATA_DIR="${ROOT_DIR}/testdata"
SAMPLE_FILE="${TESTDATA_DIR}/sample.doc"
SAMPLE_FILE_NAME="$(basename "${SAMPLE_FILE}")"

# MinIO: на macOS контейнеры ходят на хост через host.docker.internal
MINIO_ENDPOINT="${MINIO_ENDPOINT:-http://host.docker.internal:9000}"

# gRPC: будем ходить по docker-сети на сервис preprocess-service:9082
COMPOSE_PROJECT="${COMPOSE_PROJECT:-preprocess-service}"
DOCKER_NET="${DOCKER_NET:-${COMPOSE_PROJECT}_default}"
GRPC_TARGET="${GRPC_TARGET:-preprocess-service:9090}"

# Образы
MINIO_MC_IMAGE="${MINIO_MC_IMAGE:-minio/mc:RELEASE.2025-08-13T08-35-41Z-cpuv1}"
GRPCURL_IMAGE="${GRPCURL_IMAGE:-fullstorydev/grpcurl:v1.9.1}"

# Для M1 часто нужно amd64 (если у образов нет arm64)
DOCKER_PLATFORM="${DOCKER_PLATFORM:-linux/amd64}"

if [ ! -f "${SAMPLE_FILE}" ]; then
  echo "Не найден файл с тестовым образцом: ${SAMPLE_FILE}" >&2
  exit 1
fi

echo "Проверяем, что docker-сеть существует: ${DOCKER_NET}"
if ! docker network inspect "${DOCKER_NET}" >/dev/null 2>&1; then
  echo "Сеть ${DOCKER_NET} не найдена. Проверь COMPOSE_PROJECT/DOCKER_NET." >&2
  echo "Подсказка: docker network ls | grep ${COMPOSE_PROJECT}" >&2
  exit 1
fi

echo "Загружаем образец в MinIO (source bucket)..."
docker run --rm \
  --platform "${DOCKER_PLATFORM}" \
  -v "${TESTDATA_DIR}:/data" \
  --entrypoint /bin/sh \
  "${MINIO_MC_IMAGE}" \
  -c "\
    set -e; \
    mc alias set local '${MINIO_ENDPOINT}' PREPROCESS_MINIO_KEY PREPROCESS_MINIO_SECRET; \
    mc mb -p local/preprocess-source >/dev/null 2>&1 || true; \
    mc mb -p local/preprocess-artifacts >/dev/null 2>&1 || true; \
    mc cp '/data/${SAMPLE_FILE_NAME}' local/preprocess-source/incoming/demo.doc \
  "

echo "Отправляем SubmitPreprocess через grpcurl (по docker-сети: ${DOCKER_NET}, target: ${GRPC_TARGET})..."
docker run --rm \
  --platform "${DOCKER_PLATFORM}" \
  --network "${DOCKER_NET}" \
  -v "${PROTO_DIR}:/proto" \
  "${GRPCURL_IMAGE}" \
  -plaintext \
  -import-path /proto \
  -proto /proto/preprocess.proto \
  -d '{
    "source_bucket": "preprocess-source",
    "source_key": "incoming/demo.doc",
    "source_etag": "etag-demo-1",
    "target_bucket": "preprocess-artifacts",
    "target_key": "converted/demo.docx",
    "document_id": "doc-demo-1",
    "correlation_id": "corr-demo-1"
  }' \
  "${GRPC_TARGET}" backend.preprocess.v1.PreprocessService/SubmitPreprocess

echo "Готово. Используйте job_id из ответа выше, чтобы опросить статус задачи."
