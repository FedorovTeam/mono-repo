# Основные потоки preprocess-service

## Polling через `GetPreprocessStatus`
1. Клиент создаёт задание gRPC вызовом `SubmitPreprocess`, передавая источник в MinIO и координаты, куда сложить результат.
   ```bash
   grpcurl -plaintext -d '{
     "source_bucket": "preprocess-source",
     "source_key": "incoming/sample.pdf",
     "source_etag": "etag-123",
     "target_bucket": "preprocess-artifacts",
     "target_key": "converted/sample.docx",
     "document_id": "doc-123",
     "correlation_id": "corr-123"
   }' \
     localhost:9082 backend.preprocess.v1.PreprocessService/SubmitPreprocess
   ```
2. Полученный `job_id` используется для опроса статуса. Пока воркер не закончит работу, статус остаётся `PREPROCESS_STATUS_PENDING` или `PREPROCESS_STATUS_PROCESSING`.
   ```bash
   grpcurl -plaintext -d '{"job_id": "<JOB_ID_FROM_STEP_1>"}' \
     localhost:9082 backend.preprocess.v1.PreprocessService/GetPreprocessStatus
   ```
3. Финальный ответ содержит таргет-координаты и ошибку (если есть):
   ```json
   {
     "job_id": "...",
     "target_bucket": "preprocess-artifacts",
     "target_key": "converted/sample.docx",
     "status": "PREPROCESS_STATUS_COMPLETED",
     "error_code": "",
     "error_message": "",
     "error_details": {}
   }
   ```

## Webhook c `callback_url`
1. Поднимите тестовый приёмник, например echo-сервер:
   ```bash
   docker run --rm -p 8090:80 mendhak/http-https-echo:30
   ```
2. Передайте `callback_url` в `SubmitPreprocess` — воркер отправит POST после завершения (успех или остановка по ошибке):
   ```bash
   grpcurl -plaintext -d '{
     "source_bucket": "preprocess-source",
     "source_key": "incoming/sample.pdf",
     "source_etag": "etag-456",
     "target_bucket": "preprocess-artifacts",
     "target_key": "converted/sample.docx",
     "document_id": "doc-456",
     "correlation_id": "corr-456",
     "callback_url": "http://localhost:8090/hooks/preprocess"
   }' \
     localhost:9082 backend.preprocess.v1.PreprocessService/SubmitPreprocess
   ```
3. Пример payload’а вебхука при успехе:
   ```json
   {
     "job_id": "...",
     "status": "done",
     "target_bucket": "preprocess-artifacts",
     "target_key": "converted/sample.docx"
   }
   ```
4. Пример при ошибке (статус `stopped`):
   ```json
   {
     "job_id": "...",
     "status": "stopped",
     "error": {
       "code": "convert_failed",
       "message": "libreoffice error: ..."
     }
   }
   ```
5. Если вебхук вернул 5xx или произошла сетевая ошибка, воркер повторит доставку до 3 раз с паузой `preprocess.backoff` между попытками.
