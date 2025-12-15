# Тестирование

## Автотесты
- `go test ./...` — unit тесты конвертера, воркера и gRPC-обвязки, e2e против заглушки Docling (bufconn + httptest).

## SLA и таймауты
- `service.processing_timeout` — верхняя граница на нахождение задачи в статусе `processing`; по превышению задача возвращается в `pending`, `docling_job_id`/`docling_poll_count` сбрасываются и воркер начинает конвертацию с нуля.
- `service.io_timeout` — ограничение на короткие операции (MinIO/submit Docling), не распространяется на ожидание Docling.
- Отправка в Docling: до 5 быстрых ретраев с экспоненциальной задержкой (0.5s → 5s) для временных 5xx/timeout; если попытки исчерпаны, задача переводится в `waiting_docling` с новым `next_poll_at`, чтобы воркер повторил submit вместо финального `failed`.
- `service.cleanup_interval` — частота фоновой проверки зависших задач и очистки завершённых записей.
- `docling.status_initial_interval` / `docling.status_backoff_factor` / `docling.status_max_interval` — экспоненциальный backoff между запросами статуса Docling (до 30–60 минут при длительном ожидании).
- `docling.status_max_attempts` — лимит количества запросов статуса (0 — без ограничения).
- `docling.max_job_lifetime` — предел жизни задачи Docling; после его превышения задача завершается с ошибкой.

## Ручные проверки
### gRPC через grpcurl
```bash
grpcurl -plaintext -d '{"documentId":"doc-1","originalBucket":"orig","originalObjectKey":"file.docx","markdownBucket":"md","filenameExtension":"docx"}' localhost:9092 backend.transformer.v1.TransformerService/ConvertDocumentToMarkdown
```
Ожидаемый ответ — JSON c полем `markdown`, где лежит строка из Docling, а в логах сервера присутствует переданный/сгенерированный `correlation_id`.

### Проверка логов
1. Запустить сервер с `CONFIG_PATH`.
2. Выполнить gRPC запрос с заголовком `x-correlation-id: manual-check-1`.
3. Убедиться, что в логах есть строчки `incoming gRPC request` и `ConvertDocumentToMarkdown completed` с тем же идентификатором и что прокинуты заголовки до Docling.

### Переполнение очереди и метрики
1. В `config.yaml` установить, например, `service.queue_limit: 1` (оставив `service.max_workers: 1` для последовательной обработки).
2. Отправить две gRPC-заявки подряд.
3. Вторая заявка должна завершиться `ResourceExhausted`, а метрика `docling_transformer_queue_pending` в OTEL покажет рост ожидания. При `service.queue_limit: 0` обе заявки принимаются и получают `queue_id` со статусом `pending`, ожидая воркер.

### Восстановление зависших задач
1. В БД вставить запись в `transformations_queue` со статусом `processing`, `started_at` в прошлом (более `service.processing_timeout`) и валидным `correlation_id`.
2. Запустить сервис с включённым воркером и дождаться тика `service.cleanup_interval`.
3. Задача должна перейти в `pending` (если не превысила `max_processing_retries`) или `failed` с ошибкой `processing timeout exceeded`; при восстановлении `started_at` станет `NULL`, а поля Docling очистятся. В логах отобразится предупреждение `stuck tasks processed` с соответствующими счётчиками.

### Повторный запуск после рестарта
1. Создать задачу, дождаться перевода в `processing`, затем принудительно остановить сервис (например, `docker stop`).
2. Перезапустить сервис с теми же параметрами.
3. Убедиться, что фоновая проверка вернула задачу в `pending`/`failed`, обнулила `docling_job_id`/`docling_poll_count` и что новый воркер забрал её в обработку либо пометил отказом; метрика `docling_transformer_tasks_restored` должна увеличиться.

### Очистка завершённых задач
1. В таблице `transformations_queue` создать записи со статусом `done` и `failed` со старыми `finished_at`.
2. Установить `service.cleanup_interval` и `service.retention_period` на небольшие значения.
3. После пары тиков в логах появится сообщение `cleanup finished tasks completed`, а записи исчезнут из таблицы.

### Интеграция с Docling-заглушкой
1. Поднять локальный HTTP-сервис, отвечающий на POST `/v1/convert/file`, принимающий multipart поле `files` и возвращающий JSON `{"document":{"md_content":"..."},"status":"success"}`.
2. Указать его адрес в `docling.endpoint` и при необходимости `docling.api_key`.
3. Вызвать gRPC метод — тело исходного документа должно дойти до заглушки внутри multipart, а в ответе gRPC вернётся Markdown из неё.
