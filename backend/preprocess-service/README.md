# preprocess-service

Сервис принимает файлы в исходных форматах (doc/docx/pdf), конвертирует их в docx с помощью установленного LibreOffice и складывает результат в MinIO. Предназначен для внутреннего использования в цепочке DOCX → Markdown → GitHub.

## Структура каталога
- `cmd/preprocess-service/main.go` — точка входа, загрузка конфигурации и запуск HTTP/gRPC серверов.
- `internal/config` — загрузка `config.yaml` с параметрами PostgreSQL, MinIO, путями LibreOffice и воркерами.
- `internal/app` — инициализация логирования, телеметрии (метрики/трейсы), запуск http/gRPC серверов и воркера.
- `internal/domain` — доменные сущности и валидация задания на предобработку.
- `internal/transport` — HTTP‑слой с health/readiness/metrics и gRPC‑хендлер с middleware логов/метрик/trace.
- `internal/worker` — загрузка исходника, конвертация LibreOffice, загрузка артефакта, обратные вызовы.
- `config/config.yaml` — пример конфигурации для запуска.
- `Makefile`, `Dockerfile` — сборка и упаковка сервиса.

## Локальный запуск
1. Скопируйте или настройте `config/config.yaml` под свою среду (PostgreSQL, MinIO, пути LibreOffice, степень параллельности воркеров). Для стандартной кластерной схемы имена сервисов задаются в формате `<context>.backend.svc.cluster.local`.
2. Примените миграции, чтобы таблица `preprocess_queue` из `migrations/001_create_preprocess_queue.sql` появилась до старта воркера:
   ```bash
   POSTGRES_DSN="postgres://preprocess:preprocess_password@localhost:5432/preprocess?sslmode=disable" make migrate
   ```
3. Запустите сервис (миграции повторно применятся при старте и пропустят уже выполненные шаги):
   ```bash
   make run
   ```
4. Проверьте наблюдаемость и готовность:
   ```bash
   curl -i http://localhost:8082/health
   curl -i http://localhost:8082/ready
   curl -i http://localhost:8082/metrics
   ```
5. Отправьте gRPC‑запрос `SubmitPreprocess` на порт 9082 и проверьте статус через `GetPreprocessStatus` или дождитесь `callback_url`.

## Конфигурация (`config/config.yaml`)
Все параметры вынесены в YAML и читаются через `CONFIG_PATH` (по умолчанию `config/config.yaml`):
- `service.name`, `service.port`, `service.grpc_port`, `service.region` — сетевые параметры и регион сервиса.
- `postgres.dsn` — DSN для очереди/метаданных. Пинги БД используются в `readiness`.
- `minio.endpoint`, `minio.access_key`, `minio.secret_key`, `minio.region`, `minio.ssl` — доступ к бакетам источника и результата.
- `preprocess.libreoffice_path` — путь до бинаря LibreOffice, `working_dir` — временные файлы и загрузка исходников, `output_dir` — итоговые DOCX (воркер создаёт подкаталог с `job_id` и кладёт результат туда перед загрузкой в MinIO), `worker_concurrency` — число воркеров, `convert_timeout`/`backoff`/`poll_interval` — тайминги обработки и повтора, `target_media_type`/`target_bucket`/`source_bucket` — ключевые бакеты и медиатип, `target_key_prefix` — префикс для дефолтного `target_key`.
- `logging.level` — уровень логов (`debug|info|warn|error`).

Если клиент не передаёт `target_bucket`/`target_key` в запросе gRPC, сервис подставит значения из конфигурации: bucket возьмётся из `preprocess.target_bucket`, а ключ сформируется как `<target_key_prefix>/<document_id>.docx` (или просто `<document_id>.docx`, если префикс пустой).

В контейнерном образе пример `config/config.yaml` уже скопирован в `/app/config/config.yaml`, рабочие каталоги `/tmp/preprocess-service/work` и `/tmp/preprocess-service/output` созданы и доступны для записи. Итоговый файл конвертации складывается в `output_dir` (подкаталог с `job_id`), откуда загружается в MinIO. Значения MinIO и PostgreSQL можно переопределить через переменные окружения `MINIO_ENDPOINT`, `MINIO_ACCESS_KEY`, `MINIO_SECRET_KEY`, `MINIO_REGION`, `MINIO_SSL` и `POSTGRES_DSN`, либо подменить весь конфиг файлом, смонтированным по `CONFIG_PATH`.

## Миграции базы данных
- Цель `make migrate` использует `goose` для применения SQL из каталога `migrations`. Переменная `POSTGRES_DSN` обязательна: она должна указывать на схему, где хранится очередь `preprocess_queue`.
- При старте сервиса миграции запускаются автоматически (hook в `internal/app`), чтобы воркер не поднимался без таблиц. Повторное применение пропускает уже выполненные версии.
- В Kubernetes миграции можно выполнять отдельной Helm Job/Argo Workflows step перед деплоем, используя тот же `make migrate` или вызов `goose` внутри init-контейнера.

## Запуск в Docker
1. Соберите образ:
   ```bash
   docker build -t preprocess-service:local .
   ```
2. Запустите контейнер, передав параметры подключения к PostgreSQL и MinIO через переменные окружения или собственный конфиг:
   ```bash
   docker run --rm \
     -p 8082:8082 -p 9082:9082 \
     -e CONFIG_PATH=/app/config/config.yaml \
     -e POSTGRES_DSN="postgres://preprocess:preprocess_password@postgres:5432/preprocess?sslmode=disable" \
     -e MINIO_ENDPOINT="minio.moiqr.localhost:80" \
     -e MINIO_ACCESS_KEY="PREPROCESS_MINIO_KEY" \
     -e MINIO_SECRET_KEY="PREPROCESS_MINIO_SECRET" \
     -e MINIO_REGION="us-east-1" \
     -e MINIO_SSL="false" \
     -v $(pwd)/config/config.yaml:/app/config/config.yaml:ro \
     preprocess-service:local
   ```
   При желании можно заменить файл конфигурации на другой путь, передав `-e CONFIG_PATH=/app/config/custom.yaml` и примонтировав соответствующий volume.

## Быстрый стенд через docker-compose
1. Поднимите инфраструктуру и сервис из корня каталога:
   ```bash
   docker compose up -d
   ```
   В состав стенда входят PostgreSQL (порт 5432), MinIO (9000/9001) и `preprocess-service` (8082/9082). Консоль MinIO доступна на `http://localhost:9001` с учетными данными `PREPROCESS_MINIO_KEY/PREPROCESS_MINIO_SECRET`.
2. Убедитесь, что контейнеры здоровы, а сервис перешел в состояние `healthy`:
   ```bash
   docker compose ps
   curl -f http://localhost:8082/health
   curl -f http://localhost:8082/ready
   ```
3. Сформируйте тестовый объект в `preprocess-source` и отправьте `SubmitPreprocess` с готовыми параметрами:
   ```bash
   make demo
   ```
   
   Скрипт дождётся готовности MinIO и сервиса, загрузит пример `testdata/sample.doc` в `preprocess-source/incoming/demo.doc` и выполнит gRPC‑вызов на `localhost:9082` через `grpcurl`. Чтобы прогнать конвертацию на стороннем файле (например, `*.doc` из интернета), передайте `DEMO_SOURCE_URL` и при необходимости `DEMO_SOURCE_FILE_NAME`/`DEMO_SOURCE_KEY`:
   ```bash
   DEMO_SOURCE_URL="https://file-examples.com/storage/fe4fbfdb5f6e1f040b04f3c/file-sample_100kB.doc" make demo
   ```

4. Возьмите `job_id` из ответа и проверьте статус выполнения, например:
   ```bash
   docker run --rm --network host \
     -v $(pwd)/proto:/proto \
     fullstorydev/grpcurl:v1.9.1 \
     -plaintext \
     -import-path /proto \
     -proto /proto/preprocess.proto \
     -d '{"job_id": "<JOB_ID>"}' \
     localhost:9082 backend.preprocess.v1.PreprocessService/GetPreprocessStatus
   ```

## Поток Submit → очередь → воркер → MinIO → callback
1. Клиент вызывает `SubmitPreprocess` (gRPC), передавая координаты исходного объекта в MinIO, `document_id`, `correlation_id` и при необходимости `callback_url`.
2. Хендлер проверяет дубликаты по `source_bucket`/`source_key`/`source_etag`, возвращает существующий `job_id` или ставит новое задание в очередь.
3. Воркеры по таймеру выбирают задание, скачивают исходный файл из MinIO и запускают LibreOffice для конвертации в docx.
4. Результат кладётся в `preprocess.target_bucket`, статус фиксируется в PostgreSQL, а `GetPreprocessStatus` сразу отражает итог.
5. Если указан `callback_url`, воркер отправит POST с итоговым статусом (успех/ошибка) и координатами выгруженного файла.

## Требования
- Go 1.24+
- LibreOffice установлен в системе исполнения сервиса.
- Доступ к MinIO или совместимому S3‑хранилищу и PostgreSQL.

## CI/CD через Argo Workflows
Для сборки и выката образа используется WorkflowTemplate `preprocess-service` в `cicd/argo-workflows/workflow.yaml`:

1. Запустить пайплайн, передав SHA коммита:
   ```bash
   argo submit --from workflowtemplate/preprocess-service -p git_sha=$(git rev-parse HEAD)
   ```
2. Шаги пайплайна:
   - `clone` — sparse-checkout только каталога `backend/preprocess-service` из репозитория `mono-repo`.
   - `test` — запуск `go test ./...` для сервиса с кешем `/go` (без `-tags integration`).
   - `build-image` — сборка образа `harbor.tech.fedorov-team.tech/backend/preprocess-service:<git_sha>` (и `:latest`) через Kaniko.
   - `deploy` — обновление Argo CD приложения `app-preprocess-service` с новым тегом Helm-параметра `image.tag` и ожидание синхронизации/здоровья.
3. Секреты, ожидаемые воркфлоу: `github-secret` (доступ к репозиторию), `harbor-docker-config` (pull/push в Harbor), `argocd-token` (токен для Argo CD).
