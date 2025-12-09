## **База знаний через GitHub / DOCX → Markdown → GitHub**

**БД:** PostgreSQL

**Файлы:** MinIO (S3-совместимое хранилище)

**Оркестрация долгих процессов:** Temporal

**Внешний API:** GraphQL (`github.com/99designs/gqlgen`, схема — `graph/schema.graphqls`)

**Внутренние коммуникации:** gRPC

**Конфигурация приложения:** `config.yaml`

**Структура сервисов:** максимально приближена к `template-service` (из другого проекта) по структуре каталогов, документации и подходам к конфигурированию.

---

## 1. Цели и охват

Построить минимально жизнеспособную, но расширяемую платформу, которая:

1. Принимает от пользователя файлы **DOC/DOCX/PDF** через публичный API.
2. Сохраняет оригинал в **MinIO**.
3. Конвертирует документ в **Markdown** с помощью **Docling-конвертера** (HTTP/gRPC).
4. Сохраняет результат в MinIO.
5. Публикует Markdown в **GitHub-репозиторий**.
6. Отдаёт пользователю **статусы** всех этапов (upload → convert → publish).

Цель гайдлайнов — стандартизировать:

- архитектурные решения;
- границы микросервисов;
- контрактную документацию;
- работу с MinIO и PostgreSQL.

---

## 2. Ключевые архитектурные принципы

| № | Принцип | Как применяется в проекте |
| --- | --- | --- |
| 1 | **Domain-Driven Design** | Core-домен: управление *Knowledge Document* (знаниевая статья). Граница Bounded Context = граница микросервиса `document-service`. |
| 2 | **Workflow-Oriented Orchestration** | Долгие процессы (upload → convert → publish) моделируются как **Temporal Workflow** с шагами-активити. Отдельная шина событий (NATS и т.п.) не используется. |
| 3 | **CQRS по необходимости** | Базовая модель — обычный CRUD поверх PostgreSQL. При росте нагрузки допускается выделение read-модели для списков документов. |
| 4 | **Self-Contained Service** | Каждый сервис владеет своей схемой PostgreSQL (если БД ему нужна) и своим набором bucket’ов в MinIO. Код других сервисов не импортируется. |
| 5 | **Consumer Owns Integration** | Адаптеры внешних API (GitHub, Docling) живут в соответствующих сервисах (`github-publisher-service`, `docling-transformer-service`) и не протекают в core-домен. |
| 6 | **Security by Default** | Все публичные GraphQL-мутации требуют аутентификации. |
| 7 | **Observability First** | Все сервисы поддерживают логирование, метрики и tracing (OpenTelemetry). Везде прокидывается `Correlation-ID` / Workflow-ID. |

### 2.1. Конфигурация

Все переменные, ключи, токены, URL сервисов, параметры БД, MinIO и GitHub — **в одном `config.yaml`**:

```yaml
postgres:
  dsn: postgres://...

minio:
  endpoint: ...
  access_key: ...
  secret_key: ...

github:
  token: ...
  repo: ...

temporal:
  address: temporal.example.com:7233
  namespace: knowledge-base

docling:
  endpoint: http://docling-service:8080

```

> ❗ И никаких .env или отдельного vault’а — всё централизовано в config.yaml.

---

## 3. Доменная модель и сервисы

### 3.1. Core-домен: Knowledge Document

**Knowledge Document / Article** — основная доменная сущность.

Жизненный цикл (статусы — примерный набор):

1. `UPLOADED` — оригинальный DOCX принят от клиента.
2. `STORED_ORIGINAL` — оригинал сохранён в MinIO, метаданные записаны в PostgreSQL.
3. `CONVERTED_TO_MARKDOWN` — DOCX сконвертирован в Markdown и сохранён в MinIO.
4. `PUBLISHED_TO_GITHUB` — Markdown закоммичен/запушен в указанный GitHub-репозиторий.
5. `FAILED` — любой шаг завершился ошибкой (причина фиксируется отдельно).

Статусы и основные данные хранятся в таблице `documents`. Дополнительно можно использовать таблицу `document_events` для аудита.

### 3.2. Bounded Context’ы и микросервисы

Рекомендуемый минимальный набор сервисов:

1. **`gateway-service`**
   - Внешний GraphQL API.
   - Принимает файл/метаданные (организация, репозиторий, ветка, путь).
   - Вызывает gRPC-методы `document-service` и/или запускает Workflow через адаптер Temporal.
2. **`document-service`** (core-домен)
   - Владеет таблицами `documents`, `document_versions`, `document_status_logs`.
   - Знает про ключи объектов в MinIO (оригинал и Markdown).
   - Является источником правды по статусам и метаданным документов.
3. **`storage-service`** 
   - Отвечает за загрузку/выдачу файлов из MinIO.
   - Складывает оригинал и конвертированный Markdown в разные bucket’ы.
   - Инкапсулирует работу с S3-совместимым API.
4. **`docling-transformer-service`**
   - HTTP/gRPC-обёртка над Docling.
   - Принимает путь/байты DOCX, возвращает Markdown.
   - Stateless, легко масштабируется и перезапускается.
5. **`github-publisher-service`**
   - Инкапсулирует работу с GitHub API (репозитории, ветки, коммиты, PR).
   - Принимает «готовый Markdown + путь + метаданные коммита», создаёт коммит/PR.
   - Не хранит доменную модель документа — только техническую интеграцию.
6. **`workflow-service`** (Temporal worker)
   - Реализует Temporal Workflow:
      - шаг сохранения оригинала;
      - шаг конвертации;
      - шаг сохранения Markdown;
      - шаг публикации в GitHub.
   - Все шаги — Temporal Activities, вызывающие gRPC этих сервисов.
   - Сам workflow-драйвер может жить либо как отдельный сервис, либо внутри `document-service`, но логически описывается тут.

---

## 4. Нейминг и структура репозитория

### 4.1. Нейминг микросервисов

Формат названия сервиса:

```
<context>-service

```

Для этого проекта:

- `gateway-service`
- `document-service`
- `storage-service`
- `docling-transformer-service`
- `github-publisher-service`
- `workflow-service`

Названия директорий, helm-релизов и gRPC-пакетов должны быть согласованы между собой (см. ниже).

### 4.2. DNS / Helm-release (Kubernetes)

Внутренний DNS (Service):

```
<context>.backend.svc.cluster.local

```

Внешний ingress (если нужен):

```
<context>.backend.fedorov-team.tech

```

Примеры:

- `gateway.backend.svc.cluster.local`
- `document.backend.svc.cluster.local`
- `storage.backend.svc.cluster.local`

Helm Release обычно совпадает с `<context>-service` (например, `gateway-service`).

### 4.3. Структура репозитория

Рекомендуется mono-репозиторий:

```
backend/
  gateway-service/
  document-service/
  storage-service/
  docling-transformer-service/
  github-publisher-service/
  workflow-service/

```

Каждый сервис по структуре должен быть максимально близок к `template-service`:

- единый подход к `cmd/`, `internal/`, `pkg/`;
- единый способ конфигурации (`config.yaml` + env);
- единый подход к логированию и observability.

---

## 5. Работа с MinIO (S3) в рамках проекта

### 5.1. Buckets

Минимальный набор bucket’ов:

| Назначение                | Bucket | Права | Пример ключа |
|---------------------------| --- | --- | --- |
| Оригиналы DOC/DOCX/PDF    | `knowledge-original` | private | `org/<org_slug>/project/<project_slug>/year=YYYY/month=MM/day=DD/<doc_id>.docx` |
| Конвертированный Markdown | `knowledge-md` | private | `org/<org_slug>/project/<project_slug>/year=YYYY/month=MM/day=DD/<doc_id>.md` |

**Правила:**

- Только **private** access, без public-read.
- Доступ к MinIO — по access key/secret из Kubernetes Secrets / Vault.
- Никаких “случайных” новых bucket’ов без документирования.

### 5.2. Именование ключей

Формат:

```
org/<org_slug>/project/<project_slug>/year=YYYY/month=MM/day=DD/<document_id>.<ext>

```

Пример:

```
org/acme/project/payments/year=2025/month=04/day=22/6f8c...e42.docx
org/acme/project/payments/year=2025/month=04/day=22/6f8c...e42.md

```

### 5.3. Метаданные в PostgreSQL

Таблица `documents` (упрощённо):

| Колонка | Тип | Описание |
| --- | --- | --- |
| `id` | UUID PK | Идентификатор документа |
| `org_slug` | TEXT | Организация |
| `project_slug` | TEXT | Проект/репозиторий в домене клиента |
| `status` | TEXT | Текущий статус (см. жизненный цикл) |
| `source_bucket` | TEXT | Обычно `knowledge-original` |
| `source_key` | TEXT | Ключ DOCX в MinIO |
| `md_bucket` | TEXT | Обычно `knowledge-md` |
| `md_key` | TEXT | Ключ Markdown в MinIO |
| `github_repo` | TEXT | `org/repo` |
| `github_ref` | TEXT | Ветка / ref |
| `github_path` | TEXT | Путь в репозитории |
| `created_at` | TIMESTAMPTZ | Время создания записи |
| `updated_at` | TIMESTAMPTZ | Время последнего обновления |

### 5.4. Temporal: как используется

Мы **не деплоим Temporal-кластер**, используем **готовый внешний инстанс**:
см: ```infra/argocd/temporal-app.yaml```
```
temporal:
  address: temporal.example.com:7233
  namespace: knowledge-base
```

Наши задачи:

- подключиться к **Temporal Worker**
- выполнить **Workflow** + **Activity**
- зарегистрировать воркер под `taskQueue: document-processing`

Никакого NATS, никакого self-host Temporal — только подключение **как клиент**.
---

## 6. API и интеграция

### 6.1. Внешний API — GraphQL (`gateway-service`)

**Файл схемы:** `graph/schema.graphqls`.

Базовый минимум:

- **Mutations:**
   - `uploadKnowledgeDocument(input: UploadKnowledgeDocumentInput!): KnowledgeDocument!`
   - `retryProcessing(documentId: ID!): KnowledgeDocument!`
- **Queries:**
   - `knowledgeDocument(id: ID!): KnowledgeDocument`
   - `knowledgeDocuments(filter: KnowledgeDocumentFilter, pagination: Pagination): KnowledgeDocumentConnection!`

GraphQL-слой:

- не содержит бизнес-логики;
- проксирует и композирует:
   - gRPC-вызовы `document-service`;
   - операции запуска/чтения Temporal Workflow.

### 6.2. Внутренний API — gRPC

Для каждого сервиса — один публичный gRPC-файл:

```
proto/backend/<context>/v1/<context>.proto

```

Примеры:

```protobuf
package backend.document.v1;
service DocumentService { ... }

package backend.storage.v1;
service StorageService { ... }

package backend.transformer.v1;
service TransformerService { ... }

package backend.githubpublisher.v1;
service GithubPublisherService { ... }

```

**Минимальный набор методов для `DocumentService`:**

```protobuf
rpc CreateDocument(CreateDocumentRequest) returns (Document);
rpc GetDocument(GetDocumentRequest) returns (Document);
rpc ListDocuments(ListDocumentsRequest) returns (ListDocumentsResponse);
rpc UpdateDocumentStatus(UpdateDocumentStatusRequest) returns (Document);

```

Для `TransformerService`:

```protobuf
rpc ConvertDocxToMarkdown(ConvertDocxToMarkdownRequest)
    returns (ConvertDocxToMarkdownResponse);

```

Для `GithubPublisherService`:

```protobuf
rpc PublishMarkdown(PublishMarkdownRequest)
    returns (PublishMarkdownResponse);

```

**Общие требования:**

- Даты/время — `google.protobuf.Timestamp`.
- Идентификаторы — строковые (`string`), как правило UUID.
- gRPC-коды ошибок: `INVALID_ARGUMENT`, `NOT_FOUND`, `FAILED_PRECONDITION`, `INTERNAL`, и т.п.
- Метаданные запросов: поддержка `x-correlation-id`, при необходимости `x-user-id`.

---

## 7. Требования к документации микросервисов

В каталоге **каждого** сервиса должны быть:

- `README.md` — назначение, БД, MinIO bucket’ы, внешние зависимости; сценарий локального запуска.
- `diagrams.md` — ключевые схемы компонентов и последовательностей (Mermaid).
- `flows.md` — текстовое описание бизнес-процессов (например: Upload → Convert → Publish).
- `usecases.md` — сценарии: «админ загружает документ», «перепубликация документа» и т.п.
- `test.md` — сценарии ручного тестирования (curl / grpcurl / GraphQL-примеры).
- `code.md` — Mermaid-диаграмма Code / Module View конкретного сервиса.
- `infrastructure.md` — Mermaid-диаграмма инфраструктурного уровня (БД, MinIO, Temporal).
- `sequence.md` — Mermaid `sequenceDiagram` по основному поведению сервиса.

После прочтения этих файлов разработчик должен понимать:

1. Какую задачу решает сервис в цепочке DOCX → MD → GitHub.
2. Какие у него gRPC / HTTP / GraphQL-контракты.
3. Как выглядит жизненный цикл документа и где сервис в нём участвует.
4. Какие таблицы и bucket’ы он использует.
5. Как поднять сервис локально и прогнать happy-path.

---

## 8. Стандарты качества и проверки

Перед мержем изменений в любой сервис:

- [ ]  gRPC proto-контракты обновлены и сгенерированы (go/ts и др. языки, если используются).
- [ ]  Если меняется шаг Workflow — обновлена диаграмма в `sequence.md` и описание в `flows.md`.
- [ ]  В MinIO не появляются новые bucket’ы без описания в документации.
- [ ]  Все публичные методы логируют `correlation-id` и статус операции.
- [ ]  `config.yaml` содержит секреты; все секреты.
- [ ]  Локальный happy-path «загрузить DOCX → получить опубликованный Markdown в GitHub» задокументирован и проверен.

---