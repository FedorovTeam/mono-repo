# Основные потоки preprocess-service

## Бизнес-поток: .doc → .docx
1. Клиент загружает исходный `.doc`/`.pdf`/`.docx` в MinIO (bucket `preprocess-source`).
2. Через gRPC метод `SubmitPreprocess` передаются координаты файла, `document_id`, `correlation_id` и желаемый `target_key`.
3. Сервис кладёт задание в очередь (PostgreSQL) со статусом `PREPROCESS_STATUS_PENDING` и возвращает `job_id`.
4. Воркер скачивает исходный файл, запускает LibreOffice (`preprocess.libreoffice_path`) и конвертирует в docx в `working_dir`.
5. Готовый docx сохраняется в `preprocess.target_bucket` под переданным `target_key`, статус обновляется на `PREPROCESS_STATUS_COMPLETED`.
6. Клиент либо получает финальный ответ через `GetPreprocessStatus`, либо дожидается POST на `callback_url`.

## Polling через `GetPreprocessStatus`
1. Создать задание `SubmitPreprocess` без `callback_url`.
2. Опросить статус по `job_id` на gRPC порту 9082 (интервалы можно синхронизировать с `preprocess.poll_interval`).
3. В ответе появятся координаты результирующего docx и возможные коды ошибок (`error_code`, `error_message`).

## Webhook c `callback_url`
1. Поднять HTTP endpoint, принимающий POST с JSON.
2. Передать `callback_url` в `SubmitPreprocess` — воркер отправит уведомление при успехе или ошибке. Повторы ограничены тремя попытками с задержкой `preprocess.backoff`.
3. Пример полезной нагрузки:
   ```json
   {
     "job_id": "...",
     "status": "done",
     "target_bucket": "preprocess-artifacts",
     "target_key": "converted/sample.docx"
   }
   ```
