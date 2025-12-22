# pdf2md

Конвертер PDF протоколов голосования (Ил Тумэн) -> Markdown.

Особенность: вытаскивает **галочки** из таблицы (Присутствует / За / Против / Воздержался / Не голосовал) и ставит их в правильные ячейки.

## Требования

Нужны внешние утилиты Poppler:

- `pdfinfo`
- `pdftotext`
- `pdftoppm`

Рекомендованный DPI рендера для устойчивого поиска галочек — 200 (`pdftoppm -png -r 200 ...`), нужен Poppler с поддержкой `pdftotext -bbox`.

На Ubuntu/Debian:

```bash
sudo apt-get update
sudo apt-get install -y poppler-utils
