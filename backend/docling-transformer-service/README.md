# Docling Transformer Service

Docling Transformer Service — gRPC-обёртка над Docling для конвертации DOC/DOCX/PDF в Markdown с обязательным прокидыванием `x-correlation-id`. Сервис повторяет структуру `template-service` и конфигурируется через один `config.yaml`.

## Возможности
- gRPC метод `ConvertDocumentToMarkdown` по контракту `proto/backend/transformer/v1/transformer.proto` (принимает `document_id`, `original_bucket`/`original_object_key`, опционально `markdown_bucket`/`markdown_object_key` и `filename_extension` для валидации формата).
- HTTP-запрос к Docling-конвертеру для получения Markdown.
- Проксирование correlation-id во все внешние вызовы.
- Загрузка исходника из MinIO, сохранение результата в бакет Markdown.
- Настройки Docling, MinIO и Temporal в одном конфигурационном файле.
- Минимальные unit- и e2e-тесты против заглушки Docling.

## Конфигурация
Все параметры описаны в `config/config.yaml` и загружаются через переменную окружения `CONFIG_PATH` (по умолчанию этот файл). Обязательные поля и их рекомендованные значения:
- `service.name` и `service.grpc_port` — идентификатор сервиса и порт gRPC (9092).
- `docling.endpoint` — HTTPS endpoint Docling: `https://docling.tech.fedorov-team.tech`.
- `minio.endpoint` — `api.minio.tech.fedorov-team.tech`, `minio.access_key` / `minio.secret_key` — учётные данные, `minio.bucket` — обязательный бакет для результата, `minio.region` и `minio.use_ssl` — параметры подключения.
- `temporal.address` — `temporal-frontend.temporal.svc.cluster.local:7233`, `temporal.namespace` — `knowledge-base`.
- `observability.tracingEnabled` / `observability.metricsEnabled` / `observability.loggingEnabled` — флаги включения трассировки, метрик и логов (при `false` провайдеры и middleware не поднимаются).

## Локальный запуск
```bash
CONFIG_PATH=config/config.yaml go run ./cmd/server
```
Логи выводятся в STDOUT с correlation-id и метаданными gRPC вызова.

## Тестирование
```bash
go test ./...
```

## Интеграции и зависимости
- **Docling**: HTTP POST `/v1/convert/file` на `docling.endpoint`, тело — `multipart/form-data` с полем `files[]`, заголовки `X-Correlation-Id` и опционально `X-Api-Key`; в ответе JSON c `document.md_content`.
- **MinIO**: планируется для хранения исходников и результата (bucket `docling-transformations`).
- **Temporal**: подключение как клиент к namespace `knowledge-base`.

## Структура проекта
- `cmd/server` — точка входа, загрузка конфига и запуск gRPC.
- `internal/config` — чтение `config.yaml`.
- `internal/converter` — HTTP-клиент Docling с прокидыванием correlation-id.
- `internal/server` — gRPC сервер и интерсептор логирования.
- `internal/telemetry` — утилиты correlation-id.
- `proto/backend/transformer/v1` — контракт и сгенерированные gRPC артефакты.

## Логи и наблюдаемость
Каждый запрос логируется с `correlation_id` и входными метаданными, если разрешён `observability.loggingEnabled`. Заголовок `x-correlation-id` генерируется, если не пришёл снаружи. Трассировка и метрики отправляются через OTEL только при активных флагах `observability.tracingEnabled` и `observability.metricsEnabled`.
