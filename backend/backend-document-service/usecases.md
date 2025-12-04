# Сценарии использования

1. **Администратор загружает новый документ**
   - Отправляет имя и целевые бакеты в `CreateDocument`.
   - Получает UUID и ключи для загрузки файлов.

2. **Сервис хранилища подтверждает загрузку оригинала**
   - После успешной записи в MinIO вызывает `UpdateDocumentStatus` со статусом `StoredOriginal`.

3. **Pandoc-конвертер завершил обработку**
   - Передаёт ключ Markdown и вызывает `UpdateDocumentStatus` → `ConvertedToMarkdown`.

4. **GitHub публикует статью**
   - По завершении пуша вызывает `UpdateDocumentStatus` → `PublishedToGitHub`.

5. **Ошибка на любом этапе**
   - Любой шаг может вызвать `UpdateDocumentStatus` со статусом `Failed` и комментарием.
