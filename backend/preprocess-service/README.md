# preprocess-service

Сервис отвечает за предварительную обработку документов перед дальнейшей конвертацией и публикацией. Он управляет запуском LibreOffice, хранением временных файлов и обменом артефактами с MinIO.

## Структура каталога
- `cmd/preprocess-service/main.go` — точка входа, загрузка конфигурации и запуск HTTP‑обработчика здоровья.
- `internal/config` — загрузка `config.yaml` с настройками сервиса и секцией `preprocess`.
- `internal/app` — инициализация базовых зависимостей и управление жизненным циклом приложения.
- `internal/domain` — доменные сущности и простая валидация задания на предобработку.
- `internal/transport` — HTTP‑слой с health‑endpoint для наблюдаемости.
- `config/config.yaml` — пример конфигурации с параметрами LibreOffice и MinIO.
- `Makefile`, `Dockerfile` — сборка и упаковка сервиса.

## Локальный запуск
1. Скопируйте или настройте `config/config.yaml` под свою среду (пути LibreOffice, MinIO, временные каталоги).
2. Выполните сборку и запуск:
   ```bash
   make run
   ```
3. Проверьте доступность health‑проверки:
   ```bash
   curl http://localhost:8082/health
   ```

## Конфигурация
Ключевые параметры лежат в `config/config.yaml`:
- `service.port` и `service.grpc_port` — HTTP и gRPC порты сервиса.
- `postgres.dsn` — строка подключения для хранения метаданных задач предобработки.
- `minio.endpoint`/`access_key`/`secret_key` — параметры подключения к MinIO для чтения исходных файлов и загрузки артефактов.
- `preprocess.libreoffice_path` — путь к бинарю LibreOffice (например, `/usr/bin/libreoffice`).
- `preprocess.working_dir` и `preprocess.output_dir` — каталоги для временных файлов и результатов.
- `preprocess.source_bucket` и `preprocess.target_bucket` — бакеты для чтения исходных документов и сохранения результатов.
- `preprocess.target_media_type` — медиатип результирующего файла (по умолчанию docx).
- `preprocess.worker_concurrency` — максимальное количество параллельных заданий воркера.
- `preprocess.convert_timeout` — таймаут конвертации LibreOffice для одного задания.
- `preprocess.backoff` — базовый backoff между повторными попытками после временной ошибки.
- `preprocess.poll_interval` — пауза между циклами выборки задач из очереди.
- `logging.level` — уровень логирования (info/debug).

## Поток Submit → очередь → воркер → MinIO → статус
1. Клиент вызывает `SubmitPreprocess` (gRPC), передавая координаты исходного объекта в MinIO, `document_id` и `correlation_id`.
2. Хендлер проверяет наличие задачи с теми же `source_bucket`/`source_key`/`source_etag` и при совпадении возвращает существующие `job_id` и статус без создания нового задания.
3. Новая задача помещается в очередь (в дальнейшем — через PostgreSQL) и отдаёт `job_id` со статусом `PENDING`.
4. Воркеры извлекают задания из очереди, загружают исходный файл из MinIO, прогоняют LibreOffice и сохраняют результат в `preprocess.target_bucket`.
5. После успешной записи в MinIO или возникновения ошибки статус обновляется, и `GetPreprocessStatus` возвращает клиенту итоговое состояние.

## Требования
- Go 1.23+
- LibreOffice установлен в системе исполнения сервиса.
- Доступ к MinIO или совместимому S3‑хранилищу.
