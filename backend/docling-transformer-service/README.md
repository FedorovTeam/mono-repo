# Docling Transformer Service

Docling Transformer Service — gRPC-обёртка над Docling для конвертации DOC/DOCX/PDF в Markdown с обязательным прокидыванием `x-correlation-id`. Запросы на конвертацию ставятся в очередь в PostgreSQL и обрабатываются воркером, чтобы сохранять предсказуемую нагрузку на Docling и MinIO. Сервис повторяет структуру `template-service` и конфигурируется через один `config.yaml`.

## Возможности
- gRPC метод `ConvertDocumentToMarkdown` по контракту `proto/backend/transformer/v1/transformer.proto` принимает данные об исходном объекте и ставит задачу в очередь `transformations_queue`, возвращая `queue_id`, `status=pending` и итоговый путь для Markdown.
- Проверка на переполнение слотов (`service.max_workers`): при превышении лимита задач в статусах `pending`/`processing` возвращается gRPC код `ResourceExhausted`.
- HTTP-запрос к Docling-конвертеру выполняется воркером в фоне; результат сохраняется в MinIO в указанный `markdown_bucket/object_key` и помечается в очереди как `done/failed`.
- При старте воркеров сервис восстанавливает незавершённые задачи из статуса `processing` в `pending` (или продолжает опрос Docling по сохранённому `docling_job_id`), чтобы после рестартов очередь автоматически возобновлялась без ручных действий.
- Проксирование correlation-id во все внешние вызовы (Docling, MinIO) из воркера.
- Очередь хранится в PostgreSQL, схема создаётся через миграции `db/migrations/ и применяется автоматически при старте сервиса.
- Настройки Docling, MinIO, PostgreSQL и наблюдаемости в одном конфигурационном файле.
- Unit-тесты покрывают постановку задач в очередь и работу воркера.

## Конфигурация
Все параметры описаны в `config/config.yaml` и загружаются через переменную окружения `CONFIG_PATH` (по умолчанию этот файл). Обязательные поля и их рекомендованные значения:
- `service.name` и `service.grpc_port` — идентификатор сервиса и порт gRPC (9090).
- `service.max_workers` — количество одновременных воркеров очереди (по умолчанию 1; используется также как лимит слотов для входящих gRPC запросов).
- `postgres.dsn` и `postgres.max_connections` — строка подключения и лимит коннектов пула для очереди `transformations_queue`.
- `docling.endpoint` — HTTPS endpoint Docling: `https://docling.tech.fedorov-team.tech`.
- `docling.status_initial_interval` / `docling.status_backoff_factor` / `docling.status_max_interval` / `docling.status_max_attempts` — шаг, экспоненциальный backoff и лимит запросов статуса Docling (для ожидания длительных задач без блокировки воркеров).
- `minio.endpoint` — `api.minio.tech.fedorov-team.tech`, `minio.access_key` / `minio.secret_key` — учётные данные, `minio.bucket` — обязательный бакет для результата, `minio.region` и `minio.use_ssl` — параметры подключения.
- `observability.tracingEnabled` / `observability.metricsEnabled` / `observability.loggingEnabled` — флаги включения трассировки, метрик и логов (при `false` провайдеры и middleware не поднимаются).
- `temporal` блок оставлен для совместимости, но в текущей версии не используется.

## Локальный запуск
```bash
# применить миграции без запуска сервиса
go run ./cmd/main.go -config config/config.yaml -migrate-only

# запустить сервис с автоматическим прогоном миграций
go run ./cmd/main.go -config config/config.yaml
```
Логи выводятся в STDOUT с correlation-id и метаданными gRPC вызова. Флаг `-config` переопределяет путь до `config.yaml`, по умолчанию используется `CONFIG_PATH` или `config/config.yaml`.

## Тестирование
```bash
go test ./...
```

## Интеграции и зависимости
- **PostgreSQL**: таблица `transformations_queue` для хранения статусов заявок на конвертацию.
- **Docling**: HTTP POST `/v1/convert/file` на `docling.endpoint`, тело — `multipart/form-data` с полем `files[]`, заголовки `X-Correlation-Id` и опционально `X-Api-Key`; в ответе JSON c `document.md_content`.
- **MinIO**: хранение исходников и Markdown результата (bucket задаётся в конфиге).

## Структура проекта
- `cmd/main.go` — точка входа, загрузка конфига, инициализация MinIO/PostgreSQL, запуск gRPC и воркера очереди.
- `internal/config` — чтение `config.yaml`.
- `internal/converter` — HTTP-клиент Docling с прокидыванием correlation-id.
- `internal/server` — gRPC сервер и интерсептор логирования; постановка задач в очередь и лимитирование слотов.
- `internal/repository` — DAO для таблицы `transformations_queue` (enqueue, `SELECT ... FOR UPDATE SKIP LOCKED`, обновления статусов).
- `internal/worker` — фоновые воркеры, которые забирают pending-задачи, выполняют Docling-конвертацию и отмечают результат.
- `internal/transformations` — утилиты по формированию ключей и нормализации расширений.
- `internal/telemetry` — утилиты correlation-id.
- `proto/backend/transformer/v1` — контракт и сгенерированные gRPC артефакты.

## Логи и наблюдаемость
Каждый запрос логируется с `correlation_id` и входными метаданными, если разрешён `observability.loggingEnabled`. Заголовок `x-correlation-id` генерируется, если не пришёл снаружи. Трассировка и метрики отправляются через OTEL только при активных флагах `observability.tracingEnabled` и `observability.metricsEnabled`.
