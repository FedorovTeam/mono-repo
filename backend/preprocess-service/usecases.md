# Сценарии использования

## Конвертация (.doc/.docx/.pdf → .docx) с опросом статуса
1. Загрузить исходный файл в бакет, указанный в конфиге `preprocess.source_bucket` (по умолчанию `preprocess-source`), и сохранить его ETag — он обязателен для `SubmitPreprocess`.
2. Вызвать `SubmitPreprocess`, передав `document_id`, `source_bucket/source_key/source_etag` и при необходимости `correlation_id`. Если не задавать `target_bucket` и `target_key`, сервис заполнит их из конфига (`preprocess.target_bucket` и `<target_key_prefix>/<document_id>.docx`).
3. В ответе придёт `job_id` и текущий статус (для уже существующего источника — возвращается сохранённая задача без создания новой).
4. Опросить `GetPreprocessStatus` до перехода в `PREPROCESS_STATUS_COMPLETED` или `PREPROCESS_STATUS_FAILED`. Статус `FAILED` может означать как временный сбой с последующим ретраем, так и финальную остановку после превышения лимита попыток или необратимой ошибки — смотрите `error_code`, `error_message`, `error_details`.
5. После `COMPLETED` забирать готовый DOCX из координат `target_bucket/target_key` из ответа и передавать в docling/дальнейший пайплайн.

## Конвертация с webhook для финального статуса
1. Развернуть HTTP endpoint для приёма POST с JSON.
2. Вызвать `SubmitPreprocess`, передав `callback_url` и координаты источника (`source_bucket/source_key/source_etag`), а также нужные `target_bucket/target_key` (или оставить дефолтные из конфига). `document_id` и ETag обязательны.
3. Сервис вернёт `job_id`; воркер скачает файл, сконвертирует его через LibreOffice и загрузит результат. Callback отправляется только после финального состояния: `status=done` при успешной конвертации или `status=stopped` при необратимой ошибке/превышении попыток. В payload всегда есть `job_id`, `target_bucket`, `target_key`, а при остановке — блок `error { code, message }`.
4. При `status=done` можно сразу читать результат в целевом бакете; при `status=stopped` обрабатывать ошибку и решать, нужно ли перезапустить задачу с исправленными данными.

## Повторная отправка того же файла
1. Если `SubmitPreprocess` вызывается повторно с теми же `source_bucket/source_key/source_etag`, сервис найдёт существующее задание и вернёт его без изменения статуса или целевых координат.
2. Этот сценарий подходит для безопасного повтора после сетевых сбоёв клиента: `job_id`, `status`, `target_bucket` и `target_key` будут идентичны исходным значениям.
