# Архитектурные принципы и гайдлайны

для проекта «База знаний через GitHub / DOCX → Markdown → GitHub»

**Версия:** 1.0

**Дата:** 22 апреля 2025

**БД:** PostgreSQL

**Файлы:** MinIO (S3-совместимое хранилище)

**Оркестрация долгих процессов:** Temporal

**Внешний API:** GraphQL (`github.com/99designs/gqlgen`, схема — `graph/schema.graphqls`)

**Внутренние коммуникации:** gRPC

---

## 1. Цели и охват

Построить минимально жизнеспособную, но расширяемую платформу, которая:

1. Принимает от пользователя файлы **DOC/DOCX** через публичный API.
2. Сохраняет оригинал в **MinIO**.
3. Конвертирует документ в **Markdown** с помощью **Pandoc-сервиса** (HTTP/gRPC).
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
| 1 | **Domain-Driven Design** | Core-домен: управление *Knowledge Document* (знаниевая статья). Границы Bounded Context = границы микросервисов. |
| 2 | **Workflow-Oriented Orchestration** | Долгие процессы (upload→convert→publish) моделируются как **Temporal Workflow** с шагами-активити. Без отдельной шины событий (NATS не используется). |
| 3 | **CQRS по необходимости** | Базовая модель — обычный CRUD. При росте нагрузки допускается выделение read-модели для «списка документов» с оптимизированными индексами. |
| 4 | **Self-Contained Service** | Каждый сервис владеет своей схемой PostgreSQL и своим набором bucket’ов в MinIO. Код других сервисов не импортируется. |
| 5 | **Consumer Owns Integration** | Адаптеры чужих API (GitHub, Pandoc) живут в соответствующих сервисах и не протекают в core-домен. |
| 6 | **Security by Default** | Все публичные GraphQL-мутации требуют аутентификации. GitHub токены и MinIO креды хранятся в Vault/секретах Kubernetes. |
| 7 | **Observability First** | Все сервисы поддерживают логирование, метрики и tracing (OpenTelemetry). Везде прокидывается `Correlation-ID` / Workflow-ID. |

---

## 3. Доменная модель и сервисы

### 3.1 Core-домен: Knowledge Document

**Knowledge Document / Article** — основная доменная сущность.

Жизненный цикл (статусы):

1. `Uploaded` — оригинальный DOCX получен и сохранён в MinIO.
2. `StoredOriginal` — метаданные записаны в PostgreSQL.
3. `ConvertedToMarkdown` — DOCX → Markdown в MinIO.
4. `PublishedToGitHub` — Markdown закоммичен/запушен в указанный репозиторий.
5. `Failed` — любой шаг завершился ошибкой (с деталями).

Все статусы фиксируются в Postgres (таблица `documents` + таблица `document_events` при необходимости).

### 3.2 Bounded Context’ы и микросервисы

**Рекомендуемый минимум:**

1. **`gateway-service`**
    - Внешний GraphQL API.
    - Принимает файл/метаданные (организация, репозиторий, ветка, путь).
    - Вызывает gRPC-методы доменного сервиса и/или запускает Workflow через специальный gRPC/HTTP API Temporal-адаптера.
2. **`document-service`** (core-домен)
    - Владеет таблицами `documents`, `document_versions`, `document_status_logs`.
    - Сохраняет/чтёт метаданные документов.
    - Знает про ключи объектов в MinIO (оригинал и Markdown).
    - Выступает источником правды по статусам.
3. **`storage-service`** (MinIO-адаптер, можно встроить внутрь `document-service`, но логически это отдельный контекст)
    - Отвечает за загрузку/выдачу файлов из MinIO.
    - Складывает оригинал и конвертированный Markdown в разные bucket’ы.
4. **`pandoc-converter-service`**
    - HTTP/gRPC-обёртка над Pandoc.
    - Принимает путь/байты DOCX, возвращает Markdown.
    - Stateless, легко масштабируется.
5. **`github-publisher-service`**
    - Знает только про GitHub API (репы, ветки, коммиты, PR).
    - Принимает «готовый Markdown + путь + метаданные коммита», создаёт коммит/PR.
    - Не хранит доменную модель документа — только technical интеграцию.
6. **`workflow-service`** (Temporal worker)
    - Реализует Temporal Workflow:
        - шаг сохранения оригинала;
        - шаг конвертации;
        - шаг сохранения Markdown;
        - шаг публикации в GitHub.
    - Все шаги — Temporal Activities, вызывающие gRPC этих сервисов.

---

## 4. Нейминг и структура репозитория

### 4.1 Микросервисы

Формат названия сервиса:

```
<domain>-<context>-service

```

Для этого проекта:

- `backend-gateway-service`
- `backend-document-service`
- `backend-storage-service`
- `backend-pandoc-converter-service`
- `backend-github-publisher-service`
- `backend-workflow-service`

### 4.2 DNS / Helm-release (если Kubernetes)

```
<context>.backend.svc.cluster.local   # внутри кластера
<context>.backend.mydomain.ru         # внешний ingress (если нужен)

```

Примеры:

- `gateway.backend.svc.cluster.local`
- `document.backend.svc.cluster.local`

### 4.3 Директория сервиса

Внутри mono-репозитория (рекомендуется):

```
backend/
  gateway/
  document/
  storage/
  pandoc-converter/
  github-publisher/
  workflow/

```

Внутри каталога сервиса — стандартизированный набор Markdown-файлов (см. секцию 7).

---

## 5. Работа с MinIO (S3) в рамках проекта

### 5.1 Buckets

Минимальный набор bucket’ов:

| Назначение | Bucket | Права | Пример ключа |
| --- | --- | --- | --- |
| Оригиналы DOC/DOCX | `backend-docx` | private | `org/<org_slug>/project/<project_slug>/2025/04/22/<doc_id>.docx` |
| Конвертированные Markdown | `backend-md` | private | `org/<org_slug>/project/<project_slug>/2025/04/22/<doc_id>.md` |

**Правила:**

- Только **private** access, никакого public-read.
- Доступ к MinIO — по access key/secret из Vault/секретов.

### 5.2 Именование ключей

Формат:

```
org/<org_slug>/project/<project_slug>/year=YYYY/month=MM/day=DD/<document_id>.<ext>

```

Пример:

```
org/acme/project/payments/year=2025/month=04/day=22/6f8c...e42.docx
org/acme/project/payments/year=2025/month=04/day=22/6f8c...e42.md

```

### 5.3 Метаданные в PostgreSQL

Таблица `documents` (упрощённо):

| Колонка | Тип | Описание |
| --- | --- | --- |
| `id` | UUID PK | Идентификатор документа |
| `org_slug` | TEXT | Организация |
| `project_slug` | TEXT | Проект/репозиторий в домене клиента |
| `status` | TEXT | См. статусы выше |
| `source_bucket` | TEXT | Обычно `backend-docx` |
| `source_key` | TEXT | Ключ DOCX в MinIO |
| `md_bucket` | TEXT | Обычно `backend-md` |
| `md_key` | TEXT | Ключ MD в MinIO |
| `github_repo` | TEXT | `org/repo` |
| `github_ref` | TEXT | Ветка/refs |
| `github_path` | TEXT | Путь в репозитории |
| `created_at` | TIMESTAMPTZ |  |
| `updated_at` | TIMESTAMPTZ |  |

---

## 6. API и интеграция

### 6.1 Внешний API — GraphQL (gateway-service)

**Файл схемы:** `graph/schema.graphqls`.

Обязательный минимум:

- **Mutations:**
    - `uploadKnowledgeDocument(input: UploadKnowledgeDocumentInput!): KnowledgeDocument!`
    - `retryProcessing(documentId: ID!): KnowledgeDocument!`
- **Queries:**
    - `knowledgeDocument(id: ID!): KnowledgeDocument`
    - `knowledgeDocuments(filter: KnowledgeDocumentFilter, pagination: Pagination): KnowledgeDocumentConnection!`

GraphQL только проксирует/композирует:

- gRPC-вызовы `document-service`;
- запуск/проверку Temporal Workflow.

### 6.2 Внутренний API — gRPC

Общий паттерн для каждого сервиса — один публичный gRPC-файл:

```
proto/backend/<service>/<service>.proto

```

**Пакет:**

```protobuf
package backend.<service>.v1;
// пример: backend.document.v1

```

**Имя сервиса:**

```protobuf
service DocumentService { ... }
service StorageService  { ... }
service ConverterService { ... }
service GithubPublisherService { ... }

```

**Минимальная матрица методов для `DocumentService`:**

```protobuf
rpc CreateDocument(CreateDocumentRequest) returns (Document);
rpc GetDocument(GetDocumentRequest) returns (Document);
rpc ListDocuments(ListDocumentsRequest) returns (ListDocumentsResponse);
rpc UpdateDocumentStatus(UpdateDocumentStatusRequest) returns (Document);

```

Для `ConverterService`:

```protobuf
rpc ConvertDocxToMarkdown(ConvertDocxToMarkdownRequest) returns (ConvertDocxToMarkdownResponse);

```

Для `GithubPublisherService`:

```protobuf
rpc PublishMarkdown(PublishMarkdownRequest) returns (PublishMarkdownResponse);

```

**Требования:**

- Везде использовать `google.protobuf.Timestamp` для дат.
- Идентификаторы — `string`/`uuid` (строка), никаких инкрементных `int`.
- gRPC-коды ошибок: `INVALID_ARGUMENT`, `NOT_FOUND`, `FAILED_PRECONDITION`, `INTERNAL` и т. д.
- Метаданные: в каждом запросе обрабатываются `x-correlation-id` и, при необходимости, `x-user-id`.

---

## 7. Требования к документации микросервисов

В каталоге КАЖДОГО сервиса должны быть:

- `README.md` — назначение, БД, MinIO bucket’ы, внешние зависимости; локальный запуск.
- `diagrams.md` — ключевые схемы компонентов и последовательности (Mermaid).
- `flows.md` — текстовое описание бизнес-процессов (например: Upload → Convert → Publish).
- `contracts.md` — ссылки на gRPC proto, пример GraphQL-запросов/ответов (для gateway).
- `usecases.md` — сценарии: «админ загружает документ», «дорабатываем документ и перепубликуем» и т. п.
- `tasks.md` — крупные этапы развития сервиса.
- `test.md` — сценарии ручного тестирования (curl/gRPCurl/GraphQL примеры).
- `code.md` — Mermaid-диаграмма Code / Module View конкретного сервиса.
- `infrastructure.md` — Mermaid-диаграмма инфраструктурного уровня (подключения к БД, MinIO, Temporal).
- `sequence.md` — Mermaid sequenceDiagram по основному поведению сервиса.

После прочтения этих файлов разработчик должен понимать:

1. Какую задачу решает сервис в рамках цепочки DOCX → MD → GitHub.
2. Какие у него gRPC/HTTP/GraphQL-контракты.
3. Как выглядит жизненный цикл документа и где он в нём участвует.
4. Какие таблицы и bucket’ы он использует.
5. Как поднять его локально и прогнать happy-path.

---

## 8. Стандарты качества и проверки

Перед мержем изменений в любой сервис:

- [ ]  gRPC proto-контракты обновлены и сгенерированы (go/ts и пр.).
- [ ]  Если меняется шаг Workflow — обновлена диаграмма в `sequence.md` и описание в `flows.md`.
- [ ]  В MinIO не появляются новые bucket’ы без описания в документации.
- [ ]  Все публичные методы логируют `correlation-id` и статус операции.
- [ ]  Локальный happy-path «загрузить DOCX → получить опубликованный Markdown в GitHub» задокументирован и проверен.