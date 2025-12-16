# preprocess-service

Сервис отвечает за предварительную обработку документов перед дальнейшей конвертацией и публикацией. Он управляет запуском LibreOffice, хранением временных файлов и обменом артефактами с MinIO.

## Структура каталога
- `cmd/preprocess-service/main.go` — точка входа, загрузка конфигурации и запуск HTTP‑обработчика здоровья.
- `internal/config` — загрузка `config.yaml` с настройками сервиса и секцией `preprocess`.
- `internal/app` — инициализация базовых зависимостей и управление жизненным циклом приложения.
- `internal/domain` — доменные сущности и простая валидация задания на предобработку.
- `internal/transport` — HTTP‑слой с health‑endpoint для наблюдаемости.
- `config/config.yaml` — пример конфигурации с параметрами LibreOffice и MinIO.
- `Makefile`, `Dockerfile` — сборка и упаковка сервиса.

## Локальный запуск
1. Скопируйте или настройте `config/config.yaml` под свою среду (пути LibreOffice, MinIO, временные каталоги).
2. Выполните сборку и запуск:
   ```bash
   make run
   ```
3. Проверьте доступность health‑проверки:
   ```bash
   curl http://localhost:8082/health
   ```

## Конфигурация
Ключевые параметры лежат в `config/config.yaml`:
- `preprocess.libreoffice_path` — путь к бинарю LibreOffice (например, `/usr/bin/libreoffice`).
- `preprocess.working_dir` и `preprocess.output_dir` — каталоги для временных файлов и результатов.
- `preprocess.minio` — параметры подключения к бакету артефактов.
- `logging.level` — уровень логирования (info/debug).

## Требования
- Go 1.23+
- LibreOffice установлен в системе исполнения сервиса.
- Доступ к MinIO или совместимому S3‑хранилищу.
