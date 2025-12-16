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
2. Запустите сервис:
   ```bash
   make run
   ```
3. Проверьте наблюдаемость и готовность:
   ```bash
   curl -i http://localhost:8082/health
   curl -i http://localhost:8082/ready
   curl -i http://localhost:8082/metrics
   ```
4. Отправьте gRPC‑запрос `SubmitPreprocess` на порт 9082 и проверьте статус через `GetPreprocessStatus` или дождитесь `callback_url`.

## Конфигурация (`config/config.yaml`)
Все параметры вынесены в YAML и читаются через `CONFIG_PATH` (по умолчанию `config/config.yaml`):
- `service.name`, `service.port`, `service.grpc_port`, `service.region` — сетевые параметры и регион сервиса.
- `postgres.dsn` — DSN для очереди/метаданных. Пинги БД используются в `readiness`.
- `minio.endpoint`, `minio.access_key`, `minio.secret_key`, `minio.region`, `minio.ssl` — доступ к бакетам источника и результата.
- `preprocess.libreoffice_path` — путь до бинаря LibreOffice, `working_dir` — временные файлы и загрузка исходников, `output_dir` — итоговые DOCX (воркер создаёт подкаталог с `job_id` и кладёт результат туда перед загрузкой в MinIO), `worker_concurrency` — число воркеров, `convert_timeout`/`backoff`/`poll_interval` — тайминги обработки и повтора, `target_media_type`/`target_bucket`/`source_bucket` — ключевые бакеты и медиатип.
- `logging.level` — уровень логов (`debug|info|warn|error`).

В контейнерном образе пример `config/config.yaml` уже скопирован в `/app/config/config.yaml`, рабочие каталоги `/tmp/preprocess-service/work` и `/tmp/preprocess-service/output` созданы и доступны для записи. Итоговый файл конвертации складывается в `output_dir` (подкаталог с `job_id`), откуда загружается в MinIO. Значения MinIO и PostgreSQL можно переопределить через переменные окружения `MINIO_ENDPOINT`, `MINIO_ACCESS_KEY`, `MINIO_SECRET_KEY`, `MINIO_REGION`, `MINIO_SSL` и `POSTGRES_DSN`, либо подменить весь конфиг файлом, смонтированным по `CONFIG_PATH`.

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

## Поток Submit → очередь → воркер → MinIO → callback
1. Клиент вызывает `SubmitPreprocess` (gRPC), передавая координаты исходного объекта в MinIO, `document_id`, `correlation_id` и при необходимости `callback_url`.
2. Хендлер проверяет дубликаты по `source_bucket`/`source_key`/`source_etag`, возвращает существующий `job_id` или ставит новое задание в очередь.
3. Воркеры по таймеру выбирают задание, скачивают исходный файл из MinIO и запускают LibreOffice для конвертации в docx.
4. Результат кладётся в `preprocess.target_bucket`, статус фиксируется в PostgreSQL, а `GetPreprocessStatus` сразу отражает итог.
5. Если указан `callback_url`, воркер отправит POST с итоговым статусом (успех/ошибка) и координатами выгруженного файла.

## Требования
- Go 1.23+
- LibreOffice установлен в системе исполнения сервиса.
- Доступ к MinIO или совместимому S3‑хранилищу и PostgreSQL.
