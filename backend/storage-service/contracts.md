# Контракты

Публичный gRPC-контракт: `proto/backend/storage/v1/storage.proto`.

Методы:
- `UploadOriginal` — принимает org_slug, project_slug, document_id, filename_extension, bytes контента. Возвращает bucket/object_key.
- `UploadMarkdown` — принимает org_slug, project_slug, document_id, bytes контента. Возвращает bucket/object_key.
- `GetObject` — принимает bucket и object_key, возвращает содержимое файла.
- `GenerateUploadURL` — принимает document_id, file_name, content_length, expires_in_seconds и опциональный correlation_id, возвращает подписанную ссылку и ключи для загрузки оригинала и будущей Markdown-версии.

Ключ формируется по шаблону `org/<org_slug>/project/<project_slug>/year=YYYY/month=MM/day=DD/<uuid>/<document_id>.<ext>`; в ответах presigned-запросов дополнительно возвращаются `markdown_bucket` и `markdown_object_key` для будущего конвертированного файла.
