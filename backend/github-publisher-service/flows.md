# Flows

## Публикация Markdown
1. Gateway/worker вызывает gRPC `PublishMarkdown` с путём файла, сообщением коммита, `idempotency_key`, параметрами веток (`branch`/`base_branch`) и политикой бэкоффа (`retry_policy`).
2. Сервис выбирает `correlation-id`: в приоритете поле запроса `correlation_id`, иначе берётся значение из входящих метаданных по заголовку из конфигурации (`X-Correlation-ID` по умолчанию).
3. Контент:
   - Если `content` задан, используется он.
   - Если `content` пустой, обязателен конфигурированный загрузчик и параметры `bucket` + `object_key`; Markdown скачивается из MinIO через `LoadMarkdown`.
4. Перед публикацией проверяется кэш идемпотентности: повторный успешный запрос с тем же `idempotency_key` вернёт закэшированный `commit_sha`/`pull_request_*` и проставит `idempotency_hit=true` без походов в GitHub.
5. GitHub клиент делает GET `/repos/{owner}/{repo}/contents/{path}?ref={branch}`:
   - если файла нет, переходит к созданию;
   - если файл существует и его контент совпадает, публикация пропускается, возвращается актуальный SHA;
   - если файл существует и отличается, используется его SHA (и заголовок `If-Match`) для безопасного обновления.
   После этого коммит выполняется PUT `/repos/{owner}/{repo}/contents/{path}` с Base64-контентом и автором коммита (значения берутся из запроса или из конфигурации). Ветки `branch`/`base_branch`, как и owner/repository, по умолчанию берутся из конфигурации.
6. При `create_pr=true` создаётся Pull Request POST `/repos/{owner}/{repo}/pulls` из ветки `branch` в `base_branch` (по умолчанию `default_branch`). Ошибки PR возвращаются вместе с `commit_sha`, чтобы потребитель мог зафиксировать факт коммита отдельно от PR.
7. В ответе возвращаются `commit_sha`, `pull_request_number/url` (если PR создан), `retry_after` (при временных ошибках) и флаг `idempotency_hit`. В логах каждого шага фигурирует `correlation-id`.

## Ошибки и валидация
- `INVALID_ARGUMENT`: пустые `path` или `commit_message`; пустые `bucket`/`object_key`, когда `content` не передан.
- `FAILED_PRECONDITION`: не сконфигурирован storage при отсутствии `content`; ошибки загрузки из MinIO; неожиданные статусы GitHub API при коммите или создании PR.
- При всех ошибках в логи добавляется `correlation-id`, указанный в запросе или метаданных.
