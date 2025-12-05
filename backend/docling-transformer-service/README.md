# Docling Transformer Service

Docling Transformer Service — gRPC-обёртка над Pandoc/Docling для конвертации DOCX в Markdown с обязательным прокидыванием `x-correlation-id`. Сервис повторяет структуру `template-service` и конфигурируется через один `config.yaml`.

## Возможности
- gRPC метод `ConvertDocxToMarkdown` по контракту `proto/backend/transformer/v1/transformer.proto`.
- Проксирование correlation-id во все внешние вызовы.
- Настройки Pandoc/Docling, MinIO и Temporal в одном конфигурационном файле.
- Минимальные unit- и e2e-тесты против заглушки Pandoc.

## Конфигурация
Все параметры описаны в `config/config.yaml` и загружаются через переменную окружения `CONFIG_PATH` (по умолчанию этот файл).

Основные блоки:
- `service.grpc_port` — порт gRPC сервера.
- `pandoc.endpoint` — URL HTTP-обёртки Pandoc.
- `docling.endpoint` — URL Docling (прокидывается в заголовках как источник).
- `minio.*` — доступы и bucket для результатов конвертации.
- `temporal.*` — namespace и адрес для будущих воркеров/клиентов Temporal.

## Локальный запуск
```bash
CONFIG_PATH=config/config.yaml go run ./cmd/server
```
Логи выводятся в STDOUT с correlation-id и метаданными gRPC вызова.

## Тестирование
```bash
go test ./...
```

## Интеграции и зависимости
- **Pandoc**: HTTP POST `/convert`, на вход сырые байты DOCX, на выход — Markdown.
- **Docling**: endpoint прокидывается для трассировки/валидации.
- **MinIO**: планируется для хранения исходников и результата (bucket `docling-transformations`).
- **Temporal**: подключение как клиент к namespace `knowledge-base`.

## Структура проекта
- `cmd/server` — точка входа, загрузка конфига и запуск gRPC.
- `internal/config` — чтение `config.yaml`.
- `internal/converter` — HTTP-клиент Pandoc с correlation-id.
- `internal/server` — gRPC сервер и интерсептор логирования.
- `internal/telemetry` — утилиты correlation-id.
- `proto/backend/transformer/v1` — контракт и сгенерированные gRPC артефакты.

## Логи и наблюдаемость
Каждый запрос логируется с `correlation_id` и входными метаданными. Заголовок `x-correlation-id` генерируется, если не пришёл снаружи.
