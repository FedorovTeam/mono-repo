# Use-cases

## 1. Загрузка оригинала от gateway-service
- gateway-service принимает DOCX.
- Вызывает `UploadOriginal` с org/project/document_id.
- Получает bucket/key и сохраняет в DocumentService.

## 2. Сохранение Markdown после конвертации
- docling-transformer-service возвращает Markdown.
- document-service отправляет `UploadMarkdown` для того же document_id.
- Полученный ключ связывается с записью документа и передается в GitHub publisher.

## 3. Повторная выдача файла для отладки
- Администратор запрашивает `GetObject` с bucket/key.
- Получает байты исходника или Markdown для проверки публикации.
