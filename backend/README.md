## **База знаний через GitHub / DOCX → Markdown → GitHub**

**Версия:** 2.1
**Инфраструктура:** Docker/Kubernetes (опционально)

---

# 1. 📌 Цель проекта

Создать минимально работоспособную систему (MVP), которая:

1. Принимает от пользователя файлы **DOC/DOCX/PDF**;
2. Сохраняет оригинал файлов в **MinIO**;
3. Конвертирует документ в Markdown с помощью **docling** (как отдельного сервиса);
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