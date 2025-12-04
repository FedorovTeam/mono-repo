# MoiQR Template Service

Шаблон микросервиса MoiQR для быстрого старта новых сервисов.

Сначала сервис должен сам себе отправить post запрос на создание демо entity
После получения запроса сервис должен создать файл и загрузить этот файл в MinIO
Создать запись с загруженным файлов в БД
Отправить nats событие о том что файл загружен и сохранен
Сервис должен подписываться на этот топик и при получении события отправлять get запрос на получение только что созданной записи с ссылкой на MinIO

## Возможности
- gRPC и HTTP endpoint (создание и получение DemoEntity)
- Интеграция с YugabyteDB, MinIO, Redis, NATS (JetStream), ClickHouse
- Event-driven архитектура: HTTP → DB/MinIO → NATS → gRPC → Redis → HTTP
- Prometheus, Tracing (OpenTelemetry/Tempo), Logging (zap)
- Docker, Helm, Makefile, CI/CD, мониторинг, алерты
- Сервис работает даже если Redis недоступен (ошибки логируются, цепочка не прерывается)

## Быстрый старт
1. Склонируйте репозиторий и переименуйте директорию.
2. Проверьте и настройте `config/config.yaml`, переменные окружения (`.env.example`).
3. Сгенерируйте gRPC-код: `make proto`
4. Соберите сервис: `make build`
5. Запустите сервис (локально или через Docker): `make run` или `docker-compose up`
6. Проверьте endpoints:
   - REST: `GET /healthz`, `POST /demo`, `GET /demo/:id`
   - gRPC: см. proto/demo_entity.proto
   - Метрики: `GET /metrics`

## Основные Flows
- POST /demo: создаёт DemoEntity, файл в MinIO, публикует событие в NATS
- NATS subscriber: по событию получает entity через gRPC, генерирует presigned URL (MinIO), кэширует в Redis, делает GET /demo/:id

## Структура
- cmd/ — точка входа
- internal/ — бизнес-логика, интеграции
- pkg/ — общие библиотеки
- config/ — настройки, примеры конфигов
- charts/ — Helm chart
- proto/ — gRPC контракты
- db/migrations_ysql/ — миграции YB
- monitoring/ — алерты Prometheus, Grafana dashboard

## Kubernetes

Сервис предоставляет эндпоинт `/healthz` на порту `8080` и корректно завершает работу по SIGTERM.

## CI/CD
- Пример workflow: .github/workflows/ci.yml
- Makefile: build, run, test, lint, migrate, proto

## Документация
- [Контракты](./contracts.md)
- [sequence.md](sequence.md)
- [Use Cases](./usecases.md)
- [Flows](./flows.md)

## Подробнее — см. tasks.md
