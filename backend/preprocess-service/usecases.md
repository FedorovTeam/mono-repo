# Сценарии использования

## Конвертация .doc → .docx с polling
1. Подготовить исходный файл `incoming/report.doc` в бакете `preprocess-source`.
2. Вызвать `SubmitPreprocess` с `target_key: converted/report.docx`, `document_id` и `correlation_id`.
3. Каждые несколько секунд опрашивать `GetPreprocessStatus` до получения `PREPROCESS_STATUS_COMPLETED`.
4. Забрать сконвертированный docx из `preprocess-artifacts/converted/report.docx` и передать дальше в docling.

## Конвертация .pdf → .docx с callback
1. Задеплоить callback endpoint, принимающий POST с JSON (например, `https://example.tld/hooks/preprocess`).
2. Вызвать `SubmitPreprocess` c `callback_url` и параметрами источника (bucket/key/etag).
3. Получить `job_id` и дождаться уведомления о статусе. В payload будут путь результата и возможное описание ошибки.
4. По уведомлению инициировать следующую стадию пайплайна (например, публикацию в GitHub).
