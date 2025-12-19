# pdf2md

Конвертер PDF протоколов голосования (Ил Тумэн) -> Markdown.

Особенность: вытаскивает **галочки** из таблицы (Присутствует / За / Против / Воздержался / Не голосовал) и ставит их в правильные ячейки.

## Требования

Нужны внешние утилиты Poppler:

- `pdfinfo`
- `pdftotext`
- `pdftoppm`

На Ubuntu/Debian:

```bash
sudo apt-get update
sudo apt-get install -y poppler-utils
