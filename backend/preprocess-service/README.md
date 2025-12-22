# preprocess-service

Сервис конвертирует входные файлы (`doc`/`docx`/`pdf`) в `docx` через установленный LibreOffice и сохраняет результат в MinIO. Очередь задач и состояния хранятся в PostgreSQL; взаимодействие с сервисом идёт через gRPC.

## Структура каталога
- `cmd/main.go` — точка входа, чтение конфига, явный запуск миграций и сборка приложения.
- `cmd/bootstrap` — создание внешних адаптеров (MinIO, LibreOffice, callback, workspace), подключение телеметрии и DI для `App`.
- `internal/config` — разбор `config.yaml`, поддержка переменных окружения для PostgreSQL и MinIO.
- `internal/app` — сборка HTTP/gRPC серверов и воркера на основе переданных интерфейсов, без побочных эффектов подключения адаптеров.
- `internal/domain` — сущность задания и валидация входных параметров.
- `internal/transport` — HTTP‑ручки (`/health`, `/ready`, `/metrics`) и gRPC сервер с метриками и correlation-id middleware.
- `internal/storage/postgres` — репозиторий очереди на базе таблицы `preprocess_queue`.
- `internal/worker` — цикл выборки задач, загрузка исходника из MinIO, запуск LibreOffice, выгрузка результата и callback.
- `proto/preprocess.proto` — контракт gRPC (`SubmitPreprocess`, `GetPreprocessStatus`).

## Порядок инициализации и DI
1. `cmd/main.go` загружает конфигурацию и создаёт zap-логгер (`*zap.Logger` и `*zap.SugaredLogger`) через `app.NewLogger`.
2. Перед поднятием сервисов вызывается `bootstrap.RunMigrations`, чтобы прогнать встроенные миграции PostgreSQL явным шагом.
3. `bootstrap.BuildDependencies` собирает инфраструктуру: подключает телеметрию, пул PostgreSQL и репозиторий, storage‑адаптер (MinIO по умолчанию или файловый при `storage_mode=filesystem`), LibreOffice‑конвертер, файловый workspace и callback‑клиент.
4. `app.New` принимает `config.Config` и `app.Dependencies`, состоящие из интерфейсов портов (`PreprocessJobRepository`, `StorageDownloader/StorageUploader`, `DocumentConverter`, `WorkspaceManager`, `CallbackSender`, `Telemetry`). Это позволяет подменять реализации для тестов или локального раннера без запуска миграций и без создания внешних клиентов внутри `App`.
5. Запуск (`App.Run`) стартует HTTP/gRPC серверы и воркер, используя переданные зависимости; завершение (`App.Close`) закрывает только пул БД и телеметрию, оставляя управление адаптерами на уровне DI.

## gRPC API
- `SubmitPreprocess` принимает `source_bucket`, `source_key`, `source_etag`, `document_id`, опциональные `target_bucket`, `target_key`, `callback_url`, `correlation_id`. При пустых `target_bucket`/`target_key` подставляются значения из конфига: bucket берётся из `preprocess.target_bucket`, ключ строится как `<target_key_prefix>/<document_id>.docx` (или `<document_id>.docx`). `document_id`, `source_bucket`, `source_key`, `source_etag` обязательны; иначе вернётся `INVALID_ARGUMENT`. Идемпотентность обеспечивается уникальным ключом `(source_bucket, source_key, source_etag)`: при повторе вернётся существующий `job_id` и текущий статус. Поле `correlation_id` в ответе берётся из запроса.
- `GetPreprocessStatus` требует `job_id`; при отсутствии задачи возвращает `NOT_FOUND`. В ответе присутствуют координаты источника/результата, `status`, `error_code`, `error_message`, `error_details`, `updated_at` и `correlation_id` (из запроса или сохранённый вместе с задачей).
- Статусы gRPC: `PREPROCESS_STATUS_PENDING`, `PROCESSING`, `COMPLETED`, `FAILED`. Внутри БД им сопоставлены `pending|processing|done|failed|stopped`.

HTTP сервер доступен на `service.port` (по умолчанию `8082`), gRPC — на `service.grpc_port` (по умолчанию `9090`).

## Очередь в PostgreSQL
- Таблица `preprocess_queue` содержит: `job_id` (UUID PK), `document_id`, `source_bucket/source_key/source_etag`, `target_bucket/target_key`, `target_media_type`, `status`, `attempts`, `max_attempts` (по умолчанию 5), `next_run_at`, `started_at`, `finished_at`, `error_code`, `error_message`, `error_details` (JSONB), `callback_url`, `correlation_id`, `created_at/updated_at`. Индексы: `(status, next_run_at)`, `(source_bucket, source_key)`, уникальный `(source_bucket, source_key, source_etag)`.
- Миграции встроены в бинарник и выполняются при старте через `cmd/bootstrap.RunMigrations`. При необходимости можно отдельно прогнать `go run github.com/pressly/goose/v3/cmd/goose@v3.22.3 -dir db/migrations postgres "$POSTGRES_DSN" up`.

## Воркеры и ретраи
- Воркер выбирает задания со статусами `pending` или `failed`, у которых `next_run_at <= now()`, блокирует строку через `FOR UPDATE SKIP LOCKED`, увеличивает `attempts` и переводит статус в `processing`.
- Перед выборкой воркер реанимирует застрявшие задачи: если статус `processing`, а `started_at` старше `convert_timeout + backoff`, запись переводится в `failed`, `attempts` увеличивается и назначается новое `next_run_at = now() + backoff`.
- Настройки по умолчанию: параллелизм 1 поток (`preprocess.worker_concurrency`), таймаут конвертации 2 минуты (`convert_timeout`), задержка между опросами очереди 2 секунды (`poll_interval`), backoff 30 секунд (`backoff`), целевой медиатип результата `application/vnd.openxmlformats-officedocument.wordprocessingml.document`.
- Скачивание исходника использует `source_etag` как `If-Match`; 404/412 считаются постоянной ошибкой `source_missing`. Конвертация выполняется LibreOffice (`--convert-to docx`) в подкаталог `output_dir/<job_id>`. Загрузка результата в MinIO происходит в переданный или дефолтный бакет/ключ с указанным `target_media_type`.
- Ошибки маркируются через `error_code/error_message/error_details`; при временной ошибке задача получает статус `failed` и `next_run_at = now() + backoff*attempt`. При постоянной ошибке или превышении `max_attempts` статус становится `stopped`, заполняется `finished_at`.
- Callback (`POST` с полями `job_id`, `status`, `target_bucket`, `target_key`, `error{code,message}`) отправляется на `callback_url` только после финального успеха (`done`) или необратимой остановки (`stopped`). Повторов не более 3, между попытками — `backoff` (или 1s, если он не задан).

## Конфигурация (`config/config.yaml`)
- `service`: `name`, `port` (HTTP), `grpc_port`, `region`.
- `postgres`: `dsn` (используется и для readiness).
- `minio`: `endpoint`, `access_key`, `secret_key`, `ssl`, `region`.
- `preprocess`: `libreoffice_path` (обязателен), `working_dir`, `output_dir`, `storage_mode` (`minio`|`filesystem`), `local_source_dir`/`local_target_dir` (используются при `storage_mode=filesystem`), `target_bucket`, `target_key_prefix`, `target_media_type`, `worker_concurrency`, `convert_timeout`, `backoff`, `poll_interval`.
- `logging`: `level` (`trace|debug|info|warn|error|dpanic|panic|fatal`), `format` (`console|json`), при отсутствии значений используется `info` + `console`.

Переменные окружения перекрывают только `CONFIG_PATH`, `POSTGRES_DSN`, `MINIO_ENDPOINT`, `MINIO_ACCESS_KEY`, `MINIO_SECRET_KEY`, `MINIO_REGION`, `MINIO_SSL`. Рабочие директории создаются на старте; если `target_bucket` не передан клиентом, используется значение из конфига. `target_key` обязателен: либо приходит в запросе, либо формируется по `target_key_prefix` и `document_id`.

## Локальный запуск и тесты
1. Настройте `config/config.yaml` для своей БД/MinIO и пути LibreOffice. Пример лежит в репозитории и копируется в образ `/app/config/config.yaml`.
2. Примените миграции (опционально, они запустятся и при старте приложения через `bootstrap.RunMigrations`):
   ```bash
   POSTGRES_DSN="postgres://preprocess:preprocess_password@localhost:5432/preprocess?sslmode=disable" \
     go run github.com/pressly/goose/v3/cmd/goose@v3.22.3 -dir db/migrations postgres "$POSTGRES_DSN" up
   ```
3. Запустите сервис:
   ```bash
   CONFIG_PATH=./config/config.yaml go run ./cmd/main.go
   ```
4. Проверки готовности и метрик:
   ```bash
   curl -i http://localhost:8082/health
   curl -i http://localhost:8082/ready
   curl -i http://localhost:8082/metrics
   ```
5. gRPC: отправьте `SubmitPreprocess` на `localhost:9090`, затем `GetPreprocessStatus` по полученному `job_id`.
6. Тесты:
   ```bash
   go test ./...
   ```

### Локальная интеграция LibreOffice без MinIO
Для отладки конвертации можно обойтись без MinIO, используя файловый адаптер:
1. В `config/config.yaml` укажите:
   ```yaml
   preprocess:
     storage_mode: filesystem
     local_source_dir: ./tmp/source
     local_target_dir: ./tmp/target
   ```
   Каталоги будут созданы автоматически.
2. Положите исходный файл в `local_source_dir/<source_bucket>/<source_key>`, соблюдая структуру `key`, которую передадите в gRPC (например, `source_bucket=local-bucket`, `source_key=incoming/manual.docx` → `./tmp/source/local-bucket/incoming/manual.docx`).
3. Запустите сервис и вызовите `SubmitPreprocess`, передав любые значения бакетов и `source_key` из шага 2. Файл скопируется в рабочее пространство, конвертация пройдет через локальный LibreOffice, а результат запишется в `local_target_dir/<target_bucket>/<target_key>`.
4. Заберите готовый файл из `local_target_dir` или запросите статус через `GetPreprocessStatus` — координаты будут ссылаться на локальные пути с префиксом бакета, MinIO не понадобится.

## Быстрый стенд через docker-compose
1. Поднимите сервис и зависимости:
   ```bash
   docker compose up -d
   ```
   В составе: PostgreSQL (`5432`), MinIO (`9000/9001`), `preprocess-service` (`8082` HTTP, `9090` gRPC).
2. Проверьте здоровье:
   ```bash
   docker compose ps
   curl -f http://localhost:8082/health
   curl -f http://localhost:8082/ready
   ```
3. Демонстрация через `make demo`: загрузит `testdata/sample.doc` в `preprocess-source`, вызовет gRPC на `preprocess-service:9090` в docker-сети и вернёт `job_id`. Для своего файла задайте `DEMO_SOURCE_URL`/`DEMO_SOURCE_KEY`/`DEMO_SOURCE_FILE_NAME`.

## Наблюдаемость
- HTTP эндпоинты: `/health` (OK), `/ready` (пингует PostgreSQL), `/metrics` (Prometheus).
- Метрики: `http_requests_total`, `http_request_duration_ms`, `grpc_requests_total`, `grpc_request_duration_ms` с атрибутами метода/статуса.
- Корреляция: HTTP хедер `X-Correlation-ID` прокидывается и логируется; в gRPC добавляется/переиспользуется метаданные `x-correlation-id` и подтягивается в логах/метриках.

## Требования
- Go 1.24+
- LibreOffice в среде исполнения.
- Доступ к PostgreSQL и MinIO (или совместимому S3).
