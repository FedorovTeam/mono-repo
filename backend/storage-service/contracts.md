# Контракты

Публичный gRPC-контракт: `proto/backend/storage/v1/storage.proto`.

Методы:
- `UploadOriginal` — принимает org_slug, project_slug, document_id, filename_extension, bytes контента. Возвращает bucket/object_key.
- `UploadMarkdown` — принимает org_slug, project_slug, document_id, bytes контента. Возвращает bucket/object_key.
- `GetObject` — принимает bucket и object_key, возвращает содержимое файла.

Ключ формируется по шаблону `org/<org_slug>/project/<project_slug>/year=YYYY/month=MM/day=DD/<document_id>.<ext>`.
