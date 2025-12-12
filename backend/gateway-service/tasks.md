# tasks

- Вынести список разрешённых CORS origin из кода в конфигурацию.
- Добавить сквозные тесты цепочки upload → webhook → ConfirmUpload (mock storage/document-service).
- Реализовать rate limiting/quotas по `X-API-Key` для GraphQL ручек.
- Добавить интеграцию с GitHub Webhook для уведомлений о публикации.

