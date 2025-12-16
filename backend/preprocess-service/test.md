# Ручные проверки

## Polling сценарий
1. Убедитесь, что сервис запущен локально (gRPC порт 9082) и MinIO/LibreOffice доступны.
2. Создайте задание без `callback_url`:
   ```bash
   grpcurl -plaintext -d '{
     "source_bucket": "preprocess-source",
     "source_key": "manual/sample.pdf",
     "source_etag": "etag-test-1",
     "target_bucket": "preprocess-artifacts",
     "target_key": "converted/sample.docx",
     "document_id": "doc-test-1",
     "correlation_id": "corr-test-1"
   }' localhost:9082 backend.preprocess.v1.PreprocessService/SubmitPreprocess
   ```
3. Из ответа возьмите `job_id` и опрашивайте статус:
   ```bash
   grpcurl -plaintext -d '{"job_id": "<JOB_ID>"}' localhost:9082 backend.preprocess.v1.PreprocessService/GetPreprocessStatus
   ```
4. Ожидаемый финал: `status` = `PREPROCESS_STATUS_COMPLETED`, поля `target_bucket`/`target_key` заполнены, `error_code` и `error_details` пустые.

## Webhook сценарий
1. Поднимите простой echo-эндпоинт для приёма обратных вызовов:
   ```bash
   docker run --rm -p 8090:80 mendhak/http-https-echo:30
   ```
2. Создайте задание с `callback_url` на запущенный эндпоинт:
   ```bash
   grpcurl -plaintext -d '{
     "source_bucket": "preprocess-source",
     "source_key": "manual/sample2.pdf",
     "source_etag": "etag-test-2",
     "target_bucket": "preprocess-artifacts",
     "target_key": "converted/sample2.docx",
     "document_id": "doc-test-2",
     "correlation_id": "corr-test-2",
     "callback_url": "http://localhost:8090/hooks/preprocess"
   }' localhost:9082 backend.preprocess.v1.PreprocessService/SubmitPreprocess
   ```
3. После завершения воркера в логах контейнера появится POST с телом формата:
   ```json
   {
     "job_id": "...",
     "status": "done",
     "target_bucket": "preprocess-artifacts",
     "target_key": "converted/sample2.docx"
   }
   ```
4. Для имитации временной ошибки верните 500 от вебхука (например, через `iptables`/stub). Воркера повторит до 3 попыток; в последнем запросе при фейле будет блок `error` с `code` и `message`.
