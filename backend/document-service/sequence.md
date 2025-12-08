# Последовательность CreateDocument

```mermaid
sequenceDiagram
    participant C as Клиент
    participant G as gRPC сервер DocumentService
    participant S as Бизнес-логика DocumentService
    participant K as KeyManager (MinIO)
    participant R as Репозиторий (PostgreSQL)
    participant T as Temporal Client

    C->>G: CreateDocument(id?, fileName, repository, branch, path,
    uploadBucket?, uploadObjectKey?, markdownBucket?, markdownObjectKey?, contentLength)
    G->>G: Логирование correlation-id
    G->>S: Вызов CreateDocument с извлечённым расширением файла
    S-->>S: Проверка имени и id (UUID)
    S-->>S: Определение bucket'ов (заданые или значения по умолчанию)
    S->>K: GenerateKeys()
    K-->>S: originalKey, markdownKey (с валидацией существования bucket'ов)
    S-->>S: Выбор итоговых object key (переданные или сгенерированные)
    S->>R: Транзакция: вставка document со статусом Uploaded,
    версия v1, запись в document_status_logs
    R-->>S: Сохранённый документ
    alt Temporal подключен
        S->>T: Запуск DocumentProcessingWorkflow(taskQueue, input{bucket'ы, object key, ext, publishPath, originalContent, version})
        T-->>S: Workflow запущен
    end
    S-->>C: Document{status=Uploaded, object key, bucket'ы}
```
