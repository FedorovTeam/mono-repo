# Ручное тестирование table-extractor-service

## Запуск MinIO локально
1. Поднимите MinIO в Docker (порт API 9000, консоль 9001):
   ```bash
   docker run --rm -it \
     -p 9000:9000 -p 9001:9001 \
     -e MINIO_ROOT_USER=minioadmin \
     -e MINIO_ROOT_PASSWORD=minioadmin \
     minio/minio:RELEASE.2024-11-07T23-46-52Z server /data --console-address ":9001"
   ```
2. Создайте бакеты для оригиналов и артефактов:
   ```bash
   mc alias set local http://localhost:9000 minioadmin minioadmin --api S3v4
   mc mb local/table-extractor-originals
   mc mb local/table-extractor-artifacts
   ```

## Применение миграций и запуск сервера
1. Скопируйте пример конфига и обновите DSN/доступы к MinIO:
   ```bash
   cp config/config.yaml.example config/config.yaml
   ```
2. Запустите сервис (миграции применяются автоматически в `app.Build` перед стартом gRPC и воркера):
   ```bash
   CONFIG_PATH=config/config.yaml go run ./cmd/main.go
   ```
   Для SQLite достаточно значений по умолчанию; для PostgreSQL задайте `database.driver=postgres` и DSN.

## gRPC вызовы через grpcurl
1. Подготовьте PDF из base64 (пример фикстуры):
   ```bash
   base64 -d fixtures/regular/document.base64 > /tmp/document.pdf
   ```
2. Отправьте задачу на извлечение таблиц из PDF, лежащего в MinIO:
   ```bash
   grpcurl -plaintext -d '{
     "documentId": "doc-manual",
     "originalBucket": "table-extractor-originals",
     "originalObjectKey": "input/document.pdf",
     "artifactsBucket": "table-extractor-artifacts",
     "artifactsPrefix": "tables/doc-manual"
   }' localhost:9090 backend.tableextractor.v1.TableExtractorService/ExtractTablesFromPdf
   ```
   В ответе запомните `job.queueId` и `job.artifacts`.
3. Проверяйте статус задачи:
   ```bash
   grpcurl -plaintext -d '{"queueId": "<queueId из предыдущего ответа>"}' localhost:9090 backend.tableextractor.v1.TableExtractorService/GetTableExtractionJob
   ```

## Проверка артефактов в MinIO
1. После статуса `done` убедитесь, что в бакете артефактов лежат файлы:
   - `tables.raw.json`
   - `tables.normalized.json`
   - `tables.md`
   - `manifest.json`
   - `debug/…`
2. Быстрые проверки через `mc`:
   ```bash
   mc ls local/table-extractor-artifacts/tables/doc-manual/
   mc cat local/table-extractor-artifacts/tables/doc-manual/tables.md | head -n 20
   mc cat local/table-extractor-artifacts/tables/doc-manual/manifest.json | jq '.table_count'
   ```
3. Если используете фикстуру (`fixtures/regular/document.base64`), ожидаемые артефакты для сравнения лежат рядом (`tables.*` и `manifest.json`).
