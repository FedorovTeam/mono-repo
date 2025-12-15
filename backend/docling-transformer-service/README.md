# Docling Transformer Service

Docling Transformer Service — gRPC-обёртка над Docling для конвертации DOC/DOCX/PDF в Markdown с обязательным прокидыванием `x-correlation-id`. Запросы на конвертацию ставятся в очередь в PostgreSQL и обрабатываются воркером, чтобы сохранять предсказуемую нагрузку на Docling и MinIO. Сервис повторяет структуру `template-service` и конфигурируется через один `config.yaml`.

## Возможности
- gRPC метод `ConvertDocumentToMarkdown` по контракту `proto/backend/transformer/v1/transformer.proto` принимает данные об исходном объекте и ставит задачу в очередь `transformations_queue`, возвращая `queue_id`, `status=pending`, итоговый путь для Markdown и `correlation_id`. Повторный вызов с тем же `document_id` возвращает статус и параметры существующей задачи без создания дублей и повторяет исходный correlation-id.
- gRPC метод `ConvertDocxToMarkdown` принимает байты или ссылку на DOCX/DOC/PDF, отправляет задачу напрямую в Docling и возвращает Markdown/charset вместе с `correlation_id` без постановки в очередь.
- Проверка на переполнение очереди (`service.queue_limit`): gRPC код `ResourceExhausted` возвращается только если суммарное число задач в статусах `pending`/`processing`/`waiting_docling` достигло установленного лимита; `service.max_workers` ограничивает только параллелизм фоновых воркеров.
- HTTP-запрос к Docling-конвертеру выполняется воркером в фоне через `POST /v1/convert/file/async`, затем по `docling_job_id` он поллит `GET /v1/status/poll/{job_id}` и забирает результат `GET /v1/result/{job_id}`; итог сохраняется в MinIO в указанный `markdown_bucket/object_key` и помечается в очереди как `done/failed`.
- При старте воркеров запускается сервисный цикл обслуживания очереди: он активирует задачи в `waiting_docling` с наступившим `next_poll_at` и помечает просроченные по `docling.max_job_lifetime` как `failed`; зависшие `processing` восстанавливаются или завершаются только при превышении `service.processing_timeout` с учётом лимитов `scheduler.max_processing_retries` и `docling.status_max_attempts`; `failed` задачи старше `scheduler.failed_retry_delay` отправляются в повторную попытку, пока `retry_count < scheduler.max_failed_retries`, иначе остаются `failed` с причиной исчерпания попыток.
- Проксирование correlation-id во все внешние вызовы (Docling, MinIO) из воркера.
- Очередь хранится в PostgreSQL, схема создаётся через миграции `db/migrations/ и применяется автоматически при старте сервиса.
- Настройки Docling, MinIO, PostgreSQL и наблюдаемости в одном конфигурационном файле.
- Unit-тесты покрывают постановку задач в очередь и работу воркера.

### Политика retry/timeout конвертации
- gRPC-слой не дублирует задачи: если в очереди уже есть запись для `document_id`, возвращается её `queue_id` и текущий статус (`pending`/`processing`/`waiting_docling`/`done`), что позволяет внешнему воркеру опрашивать готовность без раздувания очереди.
- Лимит активных задач контролируется `service.queue_limit` (сумма `pending`/`processing`/`waiting_docling`); при превышении возвращается `ResourceExhausted`.
- Фоновый воркер использует `scheduler.retry_backoff`, `scheduler.max_processing_retries` и `docling.status_*` для экспоненциального опроса Docling и восстановления зависших задач; `service.processing_timeout` и `docling.status_max_attempts` ограничивают длительность/число попыток обработки, `scheduler.cleanup_retention` удаляет завершённые задачи, а `scheduler.failed_retry_delay`/`scheduler.max_failed_retries` управляют повторными перезапусками задач после `failed`.

## Конфигурация
Все параметры описаны в `config/config.yaml` и загружаются через переменную окружения `CONFIG_PATH` (по умолчанию этот файл). Обязательные поля и их рекомендованные значения:
- `service.name` и `service.grpc_port` — идентификатор сервиса и порт gRPC (9090).
- `service.max_workers` — количество одновременных воркеров очереди (по умолчанию 1; не влияет на приём входящих заявок).
- `service.queue_limit` — максимальное количество задач в очереди (учитываются статусы `pending`/`processing`/`waiting_docling`); при значении `0` лимит выключен.
- `postgres.dsn` и `postgres.max_connections` — строка подключения и лимит коннектов пула для очереди `transformations_queue`.
- `docling.endpoint` — HTTPS endpoint Docling: `https://docling.tech.fedorov-team.tech`.
- `docling.status_initial_interval` / `docling.status_backoff_factor` / `docling.status_max_interval` / `docling.status_max_attempts` — шаг, экспоненциальный backoff и лимит запросов статуса Docling (для ожидания длительных задач без блокировки воркеров).
- `minio.endpoint` — `api.minio.tech.fedorov-team.tech`, `minio.access_key` / `minio.secret_key` — учётные данные, `minio.bucket` — обязательный бакет для результата, `minio.region` и `minio.use_ssl` — параметры подключения.
- `observability.tracingEnabled` / `observability.metricsEnabled` / `observability.loggingEnabled` — флаги включения трассировки, метрик и логов (при `false` провайдеры и middleware не поднимаются).
- `scheduler.poll_interval` / `scheduler.maintenance_interval` — шаг опроса очереди и периодичность технического цикла восстановления/очистки; `scheduler.batch_size` ограничивает размер выборки `SELECT ... FOR UPDATE SKIP LOCKED`, `scheduler.retry_backoff`/`scheduler.max_processing_retries` задают параметры повторов при ошибках обработки, а `scheduler.cleanup_retention` определяет TTL для завершённых задач.
- `scheduler.failed_retry_delay` / `scheduler.max_failed_retries` — период выжидания перед повтором `failed` задач и максимальное число запусков (при превышении запись остаётся в статусе `failed` с пометкой об исчерпании ретраев).

### Параметры воркера
Основной бинарник `cmd/main.go` поднимает gRPC-сервер и фоновые воркеры очереди одновременно. Управление воркерами происходит через единый `config.yaml`:
- `service.max_workers` — число параллельных обработчиков очереди;
- `service.processing_timeout` и `service.io_timeout` — ограничение длительности долгих и быстрых операций (загрузка из MinIO, отправка в Docling);
- `scheduler.*` — интервалы опроса очереди, повторов при ошибках, удаления завершённых записей и рестарта `failed` задач;
- `docling.status_*` и `docling.max_job_lifetime` — стратегия backoff/лимитов опроса Docling;
- `minio.bucket` — обязательный бакет для сохранения результата, который воркер подставляет, если заявка пришла без собственного бакета.

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
- **Docling**: асинхронная отправка файла через `POST /v1/convert/file/async` (`multipart/form-data` c полем `files[]`, заголовками `X-Correlation-Id`, `X-Docling-Source-Filename`, `X-Docling-Endpoint`, опционально `X-Api-Key`), затем опрос `GET /v1/status/poll/{job_id}` и получение результата `GET /v1/result/{job_id}`.
- **MinIO**: хранение исходников и Markdown результата (bucket задаётся в конфиге).

## Структура проекта
- `cmd/main.go` — точка входа, загрузка конфига, инициализация MinIO/PostgreSQL, запуск gRPC и воркера очереди.
- `internal/config` — чтение `config.yaml`.
- `internal/infrastructure/converter` — HTTP-клиент Docling с прокидыванием correlation-id.
- `internal/server` — gRPC сервер и интерсептор логирования; постановка задач в очередь и лимитирование слотов.
- `internal/repository` — DAO для таблицы `transformations_queue` (enqueue, `SELECT ... FOR UPDATE SKIP LOCKED`, обновления статусов).
- `internal/worker` — фоновые воркеры, которые забирают pending-задачи, выполняют Docling-конвертацию и отмечают результат.
- `internal/transformations` — утилиты по формированию ключей и нормализации расширений.
- `internal/infrastructure/telemetry` — утилиты correlation-id.
- `proto/backend/transformer/v1` — контракт и сгенерированные gRPC артефакты.

## Логи и наблюдаемость
Каждый запрос логируется с `correlation_id` и входными метаданными, если разрешён `observability.loggingEnabled`. Заголовок `x-correlation-id` генерируется, если не пришёл снаружи. Трассировка и метрики отправляются через OTEL только при активных флагах `observability.tracingEnabled` и `observability.metricsEnabled`.

Фоновый воркер отдаёт метрики загрузки очереди: gauge `docling_transformer_queue_pending`/`docling_transformer_queue_processing`/`docling_transformer_queue_docling_waiting`, гистограмму размеров выборки `docling_transformer_queue_batch_size`, счётчики успешных/ошибочных задач (`docling_transformer_tasks_acquired`/`docling_transformer_tasks_completed`/`docling_transformer_tasks_failed`) и длительности `docling_transformer_processing_duration_ms` вместе с ожиданием Docling (`docling_transformer_docling_wait_duration_ms`, `docling_transformer_docling_poll_attempts`). В логах фиксируются выборки пачек и ошибки Docling, чтобы отслеживать частоту сбоев.
