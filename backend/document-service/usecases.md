# Сценарии использования

1. **Регистрация документа и сохранение ключей**
   - Клиент вызывает `CreateDocument`, передавая имя файла и (опционально) идентификатор, владельца, репозиторий, ветки, путь публикации и флаг `create_pr`.
   - Сервис валидирует имя и UUID, разворачивает `repository` вида `owner/repo`, подставляет значения по умолчанию для владельца, репозитория, веток, `base_branch` (если база не задана, используется целевая ветка) и пути (если `path` пустой, используется имя файла). Для бакетов применяются дефолты конфигурации, если поля пустые, но ключи `upload_object_key`/`markdown_object_key` остаются обязательными — сервис их не генерирует. `workflow_id` фиксируется равным `document_id`, стартовый статус — `Uploaded`, создаётся версия №1 с переданными ключами.

2. **Загрузка файла и запуск автоматической обработки**
   - Клиент загружает оригинал через gRPC `storage-service.UploadOriginal` или presigned URL (`upload_bucket`/`upload_object_key`).
   - После успешной загрузки вызывается `ConfirmUpload`. Сервис проверяет, что бакет и ключ совпадают с ожидаемыми, валидирует существование объекта через `storage-service.GetObject` и ставит запись в `processing_jobs` со стадией `store_original` в статусе `pending`.

3. **Автоматическая обработка cron‑воркеров**
   - Stage workers читают `processing_jobs` со статусом `pending` и актуальным `next_run_at`, переводят задачу в `completed|failed` и, в случае `store_original`/`convert_to_markdown`/`publish_to_github`, обновляют статус документа. JobFollower периодически забирает завершённые/упавшие задачи (после задержки `postpone`), ставит новые стадии или помечает документ `Failed` по `payload.last_error`.
   - Конвертация выполняется через gRPC `TransformerService/ConvertDocumentToMarkdown` (`docling-transformer-service`) без ожидания результата. В ответе фиксируются `queue_id` и markdown-ключ; если сервис сообщает `pending|processing|waiting_docling`, задача возвращается в `pending` с обновлённым `next_run_at`. При `status=done` воркер сохраняет `markdown_queue_id`/ключ и выставляет `ConvertedToMarkdown`. Ошибки `ResourceExhausted` откладывают задачу с увеличенным счётчиком `attempts`.
   - Публикация в GitHub вызывается через `github-publisher-service` с `idempotency_key`, параметрами репозитория/веток/пути и обязательными `commit_message`, `author_name`, `author_email` (JobFollower подставляет дефолты). При `retry_after` или backoff задача переносится, при успехе документ получает `PublishedToGitHub`, а событие фиксирует `commit_sha`/PR/`idempotency_hit`.

4. **Ручное обновление статуса и резюме после сбоя**
   - `UpdateDocumentStatus` принимает желаемый статус, причину, версии и новые ключи. Допустимы переходы `Uploaded → StoredOriginal → ConvertedToMarkdown → PublishedToGitHub`, перевод в `Failed` из любого состояния, а также восстановление после ошибки в `StoredOriginal`/`ConvertedToMarkdown`/`PublishedToGitHub`. Если версия уже существует, новые ключи должны совпадать.
   - При установке `Failed` поле `error_reason` заполняется переданным текстом. Переданные ключи и версия обновляют `document_versions` и, при необходимости, сам документ; после переходов в `StoredOriginal` или `ConvertedToMarkdown` сервис автоматически ставит следующую задачу, а при ошибке постановки помечает документ `Failed`.

5. **Получение состояния**
   - `GetDocument` возвращает актуальный документ вместе с версиями.
   - `ListDocuments` отдаёт список с пагинацией и фильтром по статусу.

6. **Фоновые помощники**
   - `UploadReconciler` периодически ищет документы в статусе `Uploaded`, проверяет минимальный возраст записи и автоматически вызывает `ConfirmUpload` с сохранёнными бакетом и ключом (проверка объекта выполняется через `storage-service.GetObject`).
   - `ProcessingReconciler` возвращает зависшие задачи из статуса `in_progress` обратно в `pending`, если они превысили допустимое время обработки (`stage_interval + grace_period`), с повторными попытками и экспоненциальным backoff.
   - Cron-воркеры опираются на `processing_jobs` и повторяют попытки согласно счётчикам `attempts` и расписанию `next_run_at`, записывая детали в `job_events`.

7. **Обработка ошибок в воркфлоу**
   - Если `ConfirmUpload`, конвертация или публикация завершаются ошибкой, stage worker или JobFollower помечают документ `Failed` с текстом причины; дальнейшие шаги останавливаются, пока статус не скорректируют вручную.
