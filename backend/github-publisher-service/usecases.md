# Use cases

## Публикация Markdown из запроса
- **Актор:** workflow после конвертации или сервис, формирующий готовый Markdown.
- **Шаги:**
  1. Передать `path` и `commit_message` в `PublishMarkdown` (оба поля обязательны).
  2. При необходимости указать `owner`/`repository` и `branch`; если не заданы, используются значения из конфигурации (`github.owner`, `github.repository`, `github.default_branch`).
  3. Отправить тело `content` с Markdown или перейти к сценарию загрузки из хранилища.
  4. По желанию передать `author_name`/`author_email` для переопределения автора коммита (по умолчанию берутся из конфигурации).
  5. Прикрепить `correlation_id` в поле запроса или заголовке gRPC метаданных (`X-Correlation-ID`).
  6. Получить SHA успешного коммита в ответе.

## Публикация Markdown из MinIO
- **Актор:** workflow, который уже сохранил Markdown в хранилище.
- **Шаги:**
  1. Не заполнять `content` и передать `bucket` и `object_key` в `PublishMarkdown`.
  2. Сервис загрузит Markdown через сконфигурированный клиент MinIO; при отсутствии клиента или параметров вернёт ошибку `FailedPrecondition/InvalidArgument`.
  3. Далее выполняются шаги коммита как в сценарии выше (ветка, автор, correlation-id, получение SHA).

## Создание Pull Request после коммита
- **Актор:** workflow публикации или оператор поддержки.
- **Шаги:**
  1. В запросе `PublishMarkdown` указать `create_pr=true` и ветку `branch` с изменениями.
  2. При необходимости задать `base_branch`; если не указана, используется `github.default_branch` из конфигурации.
  3. Передать `pr_title` и `pr_body` для описания изменений.
  4. Получить номер и URL созданного Pull Request из ответа.

## Обновление без PR
- **Актор:** фоновые задачи синхронизации.
- **Шаги:**
  1. Отправить `PublishMarkdown` без `create_pr` для прямого коммита в `branch` (по умолчанию `github.default_branch`).
  2. Использовать `author_name`/`author_email` для маркировки техпроцессов.
