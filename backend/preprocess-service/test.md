# Ручные проверки

## Быстрый smoke
1. Сервис запущен локально (`make run`), HTTP порт 8082, gRPC порт 9082.
2. Проверить наблюдаемость:
   ```bash
   curl -f http://localhost:8082/health
   curl -f http://localhost:8082/ready
   curl -f http://localhost:8082/metrics | head -n 5
   ```

## Конфигурация для тестов
1. Подготовьте тестовую копию `config/config.yaml` в `config/testdata/config.yaml` и пропишите в ней актуальные пути для `preprocess.libreoffice_path`, `preprocess.working_dir` и `preprocess.output_dir`.
2. При запуске тестов укажите путь к файлу через `CONFIG_PATH`, чувствительные параметры (`POSTGRES_DSN`, `MINIO_*`) можно перекрыть переменными окружения:
   ```bash
   CONFIG_PATH=./config/testdata/config.yaml go test ./...
   ```

## Интеграционный тест LibreOffice (локальный режим)
1. Убедитесь, что LibreOffice установлен локально и исполняемый файл доступен по пути из `preprocess.libreoffice_path` (например, `/usr/bin/libreoffice`).
2. В `config/testdata/config.yaml` должен быть включён `storage_mode: filesystem` и актуальные пути для `local_source_dir`, `local_target_dir`, `working_dir` и `output_dir`.
3. Запустите интеграционный тест, который использует реальный LibreOffice и локальные адаптеры файловой системы:
   ```bash
   CONFIG_PATH=./config/testdata/config.yaml go test -tags integration ./internal/worker -run TestLibreOfficeLocalConversion -count=1
   ```
4. После успешного прогона ожидайте конвертированный файл в `<preprocess.output_dir>/job-libreoffice-local/sample.docx` и копию результата в `<preprocess.output_dir>/<target_bucket>/job-libreoffice-local/converted.docx` для ручной проверки.

## Демонстрация через docker-compose
1. Запустить стенд: `docker compose up -d` (PostgreSQL, MinIO и preprocess-service).
2. Проверить готовность:
   ```bash
   docker compose ps
   curl -f http://localhost:8082/health
   curl -f http://localhost:8082/ready
   ```
3. Сгенерировать тестовый объект и отправить задачу на предобработку:
   ```bash
   make demo
   ```
   В MinIO появится `preprocess-source/incoming/demo.docx`, а в выводе команды — `job_id`.
4. Опросить статус задачи, подставив полученный идентификатор:
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
5. Ожидаемый финал: `status=PREPROCESS_STATUS_COMPLETED`, заполнены `target_bucket`/`target_key`, `target_media_type=application/vnd.openxmlformats-officedocument.wordprocessingml.document`, `attempts > 0`, `started_at` и `finished_at` не пустые, `next_run_at` соответствует времени последней операции, ошибок нет.

## Polling сценарий (.doc → .docx)
1. Подготовить исходник в MinIO: `preprocess-source/incoming/manual.doc`.
2. Создать задание без `callback_url`:
   ```bash
   grpcurl -plaintext -d '{
     "source_bucket": "preprocess-source",
     "source_key": "incoming/manual.doc",
     "source_etag": "etag-test-1",
     "target_bucket": "preprocess-artifacts",
     "target_key": "converted/manual.docx",
     "document_id": "doc-test-1",
     "correlation_id": "corr-test-1"
   }' localhost:9082 backend.preprocess.v1.PreprocessService/SubmitPreprocess
   ```
3. Из ответа взять `job_id` и опрашивать статус:
   ```bash
   grpcurl -plaintext -d '{"job_id": "<JOB_ID>"}' localhost:9082 backend.preprocess.v1.PreprocessService/GetPreprocessStatus
   ```
4. Ожидаемый финал: `status` = `PREPROCESS_STATUS_COMPLETED`, поля `target_bucket`/`target_key` заполнены, `target_media_type` соответствует DOCX, `attempts` и `max_attempts` отражают счётчики ретраев, `started_at`/`finished_at` проставлены, `error_code` и `error_details` пустые. При невосстановимой ошибке ожидайте `PREPROCESS_STATUS_STOPPED` вместе с заполненными `error_*` и последним `next_run_at`.

## Webhook сценарий
1. Поднять echo-приёмник обратных вызовов:
   ```bash
   docker run --rm -p 8090:80 mendhak/http-https-echo:30
   ```
2. Создать задание с `callback_url`:
   ```bash
   grpcurl -plaintext -d '{
     "source_bucket": "preprocess-source",
     "source_key": "incoming/manual2.pdf",
     "source_etag": "etag-test-2",
     "target_bucket": "preprocess-artifacts",
     "target_key": "converted/manual2.docx",
     "document_id": "doc-test-2",
     "correlation_id": "corr-test-2",
     "callback_url": "http://localhost:8090/hooks/preprocess"
   }' localhost:9082 backend.preprocess.v1.PreprocessService/SubmitPreprocess
   ```
3. Проверить лог контейнера echo — там появится POST с телом наподобие:
   ```json
   {
     "job_id": "...",
     "status": "done",
     "target_bucket": "preprocess-artifacts",
     "target_key": "converted/manual2.docx"
   }
   ```
4. Для имитации ошибки можно временно вернуть 500 от вебхука — воркер сделает до трёх попыток, после чего статус станет `STOPPED`, в payload появится блок `error`, а в `GetPreprocessStatus` будут заполнены `error_code/error_message/error_details`, `finished_at` и итоговый `attempts`.

## Локальный режим без MinIO (LibreOffice only)
1. В `config/config.yaml` выставить:
   ```yaml
   preprocess:
     storage_mode: filesystem
     local_source_dir: ./tmp/source
     local_target_dir: ./tmp/target
   ```
2. Скопировать входной файл в `local_source_dir/<source_bucket>/<source_key>` с тем же ключом, который будет в запросе (`source_bucket=local-bucket`, `source_key=incoming/manual.docx` → `./tmp/source/local-bucket/incoming/manual.docx`).
3. Запустить сервис и отправить задачу:
   ```bash
   grpcurl -plaintext -d '{
     "source_bucket": "local-bucket",
     "source_key": "incoming/manual.docx",
     "source_etag": "etag-local-1",
     "target_bucket": "local-target",
     "target_key": "converted/manual.docx",
     "document_id": "doc-local-1"
   }' localhost:9082 backend.preprocess.v1.PreprocessService/SubmitPreprocess
   ```
4. После завершения конвертации файл появится в `local_target_dir/local-target/converted/manual.docx`, а статусы можно проверить через `GetPreprocessStatus`. MinIO при этом не используется.
