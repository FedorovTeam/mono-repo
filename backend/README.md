## **База знаний через GitHub / DOCX → Markdown → GitHub**

**Версия:** 2.1

**Дата:** 2025-12-04

**Исполнитель:** 1 разработчик

**Инфраструктура:** Docker/Kubernetes (опционально)

---

# 1. 📌 Цель проекта

Создать минимально работоспособную систему (MVP), которая:

1. Принимает от пользователя файлы **DOC/DOCX**;
2. Сохраняет оригинал файлов в **MinIO**;
3. Конвертирует документ в Markdown с помощью **Pandoc** (как отдельного сервиса);
4. Сохраняет Markdown в MinIO;
5. Публикует Markdown-файл в **GitHub Repository**;
6. Показывает клиенту **статусы** всех этапов процесса.

Система устойчива к сбоям за счёт **Temporal Workflow** + записи жизненного цикла в PostgreSQL.

---

# 2. 📌 Базовые принципы

### 2.1. Сетевые взаимодействия

- Внешний API: **GraphQL** через `99designs/gqlgen`
- Внутренние коммуникации: **gRPC**
- Межсервисные операции — **асинхронные**, через Temporal Activities

### 2.2. Авторизация

- На этапе MVP: **единый секретный пароль** в `gateway-service`
- Без OAuth2/OIDC

### 2.3. Observability

- Лишь **логирование** (`zap`)
- Без rate limiting
- Без трейсинга
- Без метрик (опционально можно добавить Prometheus в будущем)

### 2.4. Устойчивость

- Temporal гарантирует выполнение workflow
- Каждое состояние документа фиксируется в **PostgreSQL**
- Каждый сервис должен быть безопасно повторяемым (idempotent)

---

# 3. ⚙ Core-домен: Knowledge / Document Lifecycle

Это главный bounded context всей системы.

### 3.1. Доменная сущность: **Knowledge Document / Knowledge Article**

| Поле | Описание |
| --- | --- |
| `id` | UUID |
| `status` | `Uploaded`, `Stored`, `Converted`, `Published`, `Failed` |
| `originalFileLocation` | путь в MinIO |
| `markdownFileLocation` | путь в MinIO |
| `githubPath` | путь в GitHub |
| `createdAt`, `updatedAt` | таймстемпы |
| `metadata` | формат, размер, пользователь и т.п. |

---

# 4. 🧠 Bounded Contexts / Микросервисы

## 4.1. `gateway-service` (GraphQL API Gateway)

**Тип:** Edge-service / BFF (без доменной логики)

### Функции:

- Принимает запросы клиента через GraphQL
- Проверяет секретный пароль
- Проксирует запросы в `knowledge-service` через gRPC
- Отображает статус документа

### Основные GraphQL операции:

- `uploadDocument(file: Upload!): Document!`
- `document(id: ID!): Document`
- `documents(): [Document!]!`

---

## 4.2. `knowledge-service` (Core Domain + Temporal Worker)

**Главный доменный сервис**, содержащий:

- агрегат `Document`,
- бизнес-инварианты,
- репозитории PostgreSQL,
- Temporal workflow + activities.

### Основные доменные команды:

- `UploadDocument`
- `MarkOriginalStored`
- `MarkConvertedToMarkdown`
- `MarkPublished`
- `FailProcessing`

### Основные доменные события:

- `DocumentUploaded`
- `DocumentStorageRequested`
- `DocumentConversionRequested`
- `DocumentPublishRequested`
- `DocumentConverted`
- `DocumentPublished`

### Взаимодействия:

- Инициирует Temporal workflow
- Обновляет статусы в PostgreSQL
- Через Activities вызывает:
    - `storage-service`
    - `transformation-service`
    - `publishing-service`

---

## 4.3. `storage-service`

**Поддомен хранения файлов / S3 abstraction**

### Возможности:

- `StoreOriginal(fileBytes) → originalFileLocation`
- `StoreMarkdown(markdownContent) → markdownFileLocation`
- `GetFile(location) → bytes`
- опционально публичные ссылки (не обязательно)

Не знает ничего о GitHub, Pandoc или статусах.

---

## 4.4. `transformation-service` (Pandoc Service)

**Преобразование DOCX → Markdown**

### Возможности:

- `ConvertToMarkdown(fileLocation) → markdownContent`

Внутри:

- скачивает файл из MinIO,
- вызывает Pandoc (как бинарь),
- отдаёт строку Markdown.

Абсолютно технический сервис.

---

## 4.5. `publishing-service` (GitHub Integration)

**Публикация Markdown в GitHub**

### Возможности:

- `PublishMarkdown(repo, path, content) → githubPath`

Только отвечает за GitHub API:

- авторизация токеном,
- commit/PR/branch (опционально),
- возврат пути/URL.

---

# 5. 🎛 Оркестрация через Temporal

### Выделенный сервис:

- Temporal Server (History + Matching + Frontend)
- Temporal Web UI (debug)

### Worker:

- жил в `knowledge-service`

---

# 6. 🧵 Workflow обработки документа

## `UploadDocumentWorkflow(documentId)`

### 1. **StoreOriginalInMinioActivity**

- вызывает `storage-service.StoreOriginal`
- фиксирует статус: `Stored`

### 2. **ConvertToMarkdownActivity**

- вызывает `transformation-service.ConvertToMarkdown`
- кладёт Markdown в MinIO через `storage-service.StoreMarkdown`
- фиксирует статус: `Converted`

### 3. **PublishToGitHubActivity**

- вызывает `publishing-service.PublishMarkdown`
- фиксирует статус: `Published`

### 4. Завершение

- возвращает итоговые данные
- любой сбой → `FailProcessing` → статус `Failed`

---

# 7. 🗄 PostgreSQL (модель данных)

```
documents (
  id uuid primary key,
  status text not null,
  original_file_location text,
  markdown_file_location text,
  github_path text,
  created_at timestamp,
  updated_at timestamp,
  metadata jsonb
)
```

---

# 8. 🔐 Конфигурация безопасности (MVP)

- единый секретный пароль `X-API-Key` в `gateway-service` 8 символов
- gRPC трафик внутризонный (Kubernetes / Internal network)
- Нет ACL между сервисами на MVP
- GitHub токен хранится в config.yaml

---

# 9. 📦 Краткая схема микросервисов

```
Client → gateway-service → knowledge-service → Temporal Workflow
                         ↘ gRPC calls →
      storage-service | transformation-service | publishing-service
```

MinIO, PostgreSQL, Temporal — отдельные сервисы.