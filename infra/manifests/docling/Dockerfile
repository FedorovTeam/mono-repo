FROM quay.io/docling-project/docling-serve-cpu:latest

# В этом образе tesseract читает данные отсюда:
ENV TESSDATA_PREFIX=/usr/share/tesseract/tessdata/

# Под root добавляем traineddata
USER 0

# Скачаем rus.traineddata прямо из официального репо tessdata_best
# (можно заменить на tessdata_fast если важнее скорость)
ADD https://github.com/tesseract-ocr/tessdata_best/raw/main/rus.traineddata /usr/share/tesseract/tessdata/rus.traineddata

# Права чтобы и non-root мог читать
RUN chmod 0644 /usr/share/tesseract/tessdata/rus.traineddata && \
    ls -la /usr/share/tesseract/tessdata/

# Вернёмся на дефолтного пользователя образа
USER 1001
