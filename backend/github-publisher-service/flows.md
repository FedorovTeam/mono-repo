# Flows

## Публикация Markdown
1. Gateway/worker вызывает gRPC `PublishMarkdown` с путём файла, веткой и текстом Markdown.
2. Сервис извлекает `correlation-id` из метаданных запроса либо из поля запроса.
3. GitHub клиент выполняет PUT `/repos/{owner}/{repo}/contents/{path}` с Base64-контентом и автором коммита.
4. При флаге `create_pr=true` открывается Pull Request `POST /repos/{owner}/{repo}/pulls` в базовую ветку.
5. Ответ возвращает SHA коммита и (опционально) номер/URL PR.

## Ошибки и валидация
- Пустые `path`, `content` или `commit_message` приводят к `INVALID_ARGUMENT`.
- Любые ошибки GitHub API маппятся в `INTERNAL` с сообщением сервиса.
