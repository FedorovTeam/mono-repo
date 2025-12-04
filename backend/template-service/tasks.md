# Tasks: Создание микросервисного шаблона MoiQR

## Этап 1. Инициализация структуры
- [x] Создать директорию `template-service/`
- [x] Создать базовые подпапки: `cmd/`, `internal/`, `pkg/`, `config/`, `charts/`, `proto/`, `db/migrations_ysql/`
- [x] Добавить файлы: `Dockerfile`, `Makefile`, `README.md`, `config.yaml`, `.env.example`, `.gitignore`, `contracts.md`, `diagrams.md`, `usecases.md`, `flows.md`

## Этап 2. Интеграция с базовыми компонентами
- [x] YugabyteDB (YSQL):
  - [x] Пример миграции и подключения через `pgx v5`
  - [x] Стандартные поля `bucket_id`, `region_id` в таблицах
- [x] MinIO:
  - [x] Пример клиента и загрузки/скачивания файлов
  - [x] Presigned URL + кэш через Redis
- [x] Redis:
  - [x] Пример подключения, кэширования, pub/sub
- [x] NATS JetStream:
  - [x] Пример публикации/подписки событий, idempotency
- [x] ClickHouse (аналитика):
  - [x] Пример записи событий через insert
  - [x] DDL для аналитической таблицы
- [x] gRPC:
  - [x] Базовый proto-файл, генерация клиента/сервера
  - [x] Пример gRPC endpoint
- [x] HTTP:
  - [x] REST endpoint через Gin или аналог
- [x] Prometheus:
  - [x] Экспорт метрик `/metrics`
- [x] Tracing:
  - [x] Интеграция с OpenTelemetry (Tempo)
- [x] Logging:
  - [x] Структурное логирование (zap), DI logger
- [x] Config:
  - [x] Пример config.yaml + env override (cleanenv)

## Этап 3. DevOps и инфраструктура
- [x] Dockerfile для сборки сервиса
- [x] docker-compose.yaml для локального запуска без зависимостей
- [x] Helm chart (charts/template-service/) с примерами values.yaml
- [x] Makefile с командами: build, run, test, lint, migrate, proto

## Этап 4. Observability и CI/CD
- [x] Healthcheck endpoint `/healthz`
- [x] Prometheus-метрики `/metrics`
- [ ] Пример алертов и dashboard для Grafana
- [ ] Пример конфигов для логирования, trace, metrics
- [ ] Пример GitHub Actions workflow или другого CI pipeline

## Этап 5. Документация и стандарты
- [ ] README.md с описанием шаблона и quickstart
- [ ] contracts.md с примерами gRPC и NATS контрактов
- [ ] diagrams.md (Mermaid)
- [ ] usecases.md (основные сценарии)
- [ ] flows.md (последовательности событий)
- [ ] Чек-лист по архитектурным стандартам (bucket_id, region_id, logging, observability, security)

## Этап 6. Пример бизнес-логики
- [ ] Простой CRUD-ресурс (например, DemoEntity)
- [ ] gRPC и REST endpoint для DemoEntity
- [ ] Пример публикации/обработки события через NATS
- [ ] Пример работы с MinIO и Redis

---

> После выполнения этих этапов шаблон можно копировать для создания новых сервисов MoiQR с полной поддержкой всех стандартов и интеграций.
