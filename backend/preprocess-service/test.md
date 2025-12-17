# Ручные проверки

## Быстрый smoke
1. Сервис запущен локально (`make run`), HTTP порт 8082, gRPC порт 9082.
2. Проверить наблюдаемость:
   ```bash
   curl -f http://localhost:8082/health
   curl -f http://localhost:8082/ready
   curl -f http://localhost:8082/metrics | head -n 5
   ```
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
5. Ожидаемый финал: `status=PREPROCESS_STATUS_COMPLETED`, заполнены `target_bucket`/`target_key`, ошибок нет.

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
4. Ожидаемый финал: `status` = `PREPROCESS_STATUS_COMPLETED`, поля `target_bucket`/`target_key` заполнены, `error_code` и `error_details` пустые.

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
4. Для имитации ошибки можно временно вернуть 500 от вебхука — воркер сделает до трёх попыток, после чего статус станет `FAILED` и в payload появится блок `error`.
