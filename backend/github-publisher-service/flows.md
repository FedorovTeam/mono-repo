# Flows

## Публикация Markdown
1. Gateway/worker вызывает gRPC `PublishMarkdown` с путём файла, сообщением коммита, опциональными owner/repository/author и параметрами веток (`branch`/`base_branch`).
2. Сервис выбирает `correlation-id`: в приоритете поле запроса `correlation_id`, иначе берётся значение из входящих метаданных по заголовку из конфигурации (`X-Correlation-ID` по умолчанию).
3. Контент:
   - Если `content` задан, используется он.
   - Если `content` пустой, обязателен конфигурированный загрузчик и параметры `bucket` + `object_key`; Markdown скачивается из MinIO через `LoadMarkdown`.
4. GitHub клиент коммитит файл PUT `/repos/{owner}/{repo}/contents/{path}` с Base64-контентом и автором коммита (значения берутся из запроса или из конфигурации). Ветки `branch`/`base_branch`, как и owner/repository, по умолчанию берутся из конфигурации.
5. При `create_pr=true` создаётся Pull Request POST `/repos/{owner}/{repo}/pulls` из ветки `branch` в `base_branch` (по умолчанию `default_branch`).
6. В ответе возвращается SHA коммита; номер и URL PR заполняются только при успешном создании PR. В логах каждого шага фигурирует `correlation-id`.

## Ошибки и валидация
- `INVALID_ARGUMENT`: пустые `path` или `commit_message`; пустые `bucket`/`object_key`, когда `content` не передан.
- `FAILED_PRECONDITION`: не сконфигурирован storage при отсутствии `content`; ошибки загрузки из MinIO; неожиданные статусы GitHub API при коммите или создании PR.
- При всех ошибках в логи добавляется `correlation-id`, указанный в запросе или метаданных.
