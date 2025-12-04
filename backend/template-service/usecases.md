# Use Cases

## Создать DemoEntity (HTTP/gRPC)
1. Клиент вызывает gRPC/HTTP endpoint CreateDemoEntity
2. Сервис валидирует и сохраняет данные в YugabyteDB (id генерируется базой)
3. Загружает файл в MinIO
4. Публикует событие в NATS (JetStream)

## Получить DemoEntity
1. Клиент вызывает gRPC/HTTP endpoint GetDemoEntity
2. Сервис читает из YugabyteDB
3. Возвращает результат

## Event-driven обработка события
1. NATS JetStream доставляет событие подписчику
2. Подписчик (gRPC handler) получает DemoEntity
3. Генерирует presigned URL для файла в MinIO
4. Кэширует URL в Redis (если доступен)
5. Делает GET /demo/:id для проверки

## Получить метрики
1. Клиент вызывает /metrics
2. Получает Prometheus-метрики
