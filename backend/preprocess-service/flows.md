# Основные потоки preprocess-service

## Постановка задачи через `SubmitPreprocess`
1. Клиент загружает исходный документ (например, `.doc`/`.pdf`/`.docx`) в исходный бакет `preprocess.source_bucket` (по умолчанию `preprocess-source`).
2. По gRPC вызывается `SubmitPreprocess` с обязательными `document_id`, `source_bucket`, `source_key`, `source_etag` и опциональными `target_bucket`, `target_key`, `callback_url`, `correlation_id`.
3. Если в БД (`preprocess_queue`) уже есть запись с теми же `source_bucket`/`source_key`/`source_etag`, сервис возвращает существующий `job_id` и актуальное состояние задачи (идемпотентность по исходнику).
4. Если задачи ещё нет, сервис создаёт новую запись со статусом `pending`, `max_attempts=5` и ближайшим запуском `next_run_at=now()`:
   - `target_bucket` и `target_key` берутся из запроса; при их отсутствии используются `preprocess.target_bucket` и `preprocess.target_key_prefix` (`<prefix>/<document_id>.docx`).
   - В ответе клиент получает `job_id`, `status` и `correlation_id`.

## Обработка задач воркером
1. Воркер каждые `preprocess.poll_interval` (по умолчанию 2s) переводит «зависшие» задачи из статуса `processing` в `failed`, если они длятся дольше `convert_timeout + backoff`, чтобы они снова попали в очередь.
2. Для статусов `pending`/`failed` с `next_run_at <= now()` задача помечается как `processing`, счётчик `attempts` увеличивается.
3. Шаги обработки:
   - Подготовка рабочего каталога (`preprocess.working_dir`/`output_dir`).
   - Скачивание исходника из MinIO или локального стораджа (выбор через `preprocess.storage_mode`), с проверкой `source_etag`. Ошибки «нет файла» считаются постоянными (`source_missing`).
   - Конвертация LibreOffice (`preprocess.libreoffice_path`) в DOCX с таймаутом `convert_timeout`.
   - Сохранение результата в `preprocess.target_bucket` (или явный `target_bucket`) под `target_key`, загрузка с контент-тайпом `preprocess.target_media_type`.
4. Результаты и ошибки:
   - При успехе задача получает статус `done`, проставляются `target_bucket`, `target_key`, `target_media_type`, запускается callback (если указан).
   - При ошибке задача получает статус `failed` и `next_run_at = now() + backoff * attempts`; при превышении `max_attempts` или при постоянной ошибке — статус `stopped` и callback с деталями ошибки.

## Polling через `GetPreprocessStatus`
1. Вызывайте `GetPreprocessStatus(job_id, correlation_id?)` по gRPC-порту сервиса (`service.grpc_port`, по умолчанию 9090).
2. Возможные статусы: `pending`, `processing`, `done`, `failed` (ожидает повторной попытки), `stopped` (окончательная ошибка).
3. Ответ содержит координаты исходника и результата, `error_code`/`error_message`/`error_details`, `updated_at` и `correlation_id`.

## Webhook c `callback_url`
1. Поднимите HTTP endpoint с приёмом POST JSON.
2. Передайте `callback_url` в `SubmitPreprocess`. Callback отправляется при успешном завершении (`status=done`) и при окончательной остановке (`status=stopped`). Повторы — до 3 попыток с паузой `preprocess.backoff`, временные ошибки (5xx/сетевые) приводят к повтору.
3. Пример полезной нагрузки:
   ```json
   {
     "job_id": "...",
     "status": "stopped",
     "target_bucket": "preprocess-artifacts",
     "target_key": "converted/sample.docx",
     "error": {
       "code": "convert_failed",
       "message": "libreoffice error: ..."
     }
   }
   ```
