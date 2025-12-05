# Flows

## Публикация Markdown
1. Gateway/worker вызывает gRPC `PublishMarkdown` с путём файла, веткой и ссылкой на Markdown в MinIO (`bucket` + `object_key`).
2. Сервис извлекает `correlation-id` из метаданных запроса либо из поля запроса.
3. Markdown скачивается из MinIO, при необходимости используется явный `content` из запроса.
4. GitHub клиент выполняет PUT `/repos/{owner}/{repo}/contents/{path}` с Base64-контентом и автором коммита.
5. При флаге `create_pr=true` открывается Pull Request `POST /repos/{owner}/{repo}/pulls` в базовую ветку.
6. Ответ возвращает SHA коммита и (опционально) номер/URL PR.

## Ошибки и валидация
- Пустые `path` или `commit_message`, а также отсутствие `content` вместе с `bucket/object_key` приводят к `INVALID_ARGUMENT`.
- Ошибки скачивания из MinIO и ответы GitHub API маппятся в `FAILED_PRECONDITION`, при этом в логах всегда присутствует `correlation-id`.
