#!/bin/bash

sudo apt install unzip

set -e

# Определение архитектуры
ARCH=$(uname -m)
if [ "$ARCH" = "x86_64" ]; then
    ARCH="amd64"
elif [ "$ARCH" = "aarch64" ] || [ "$ARCH" = "arm64" ]; then
    ARCH="arm64"
else
    echo "Неизвестная архитектура: $ARCH"
    exit 1
fi

# Получение последней версии OpenTofu
VERSION=$(curl -s https://api.github.com/repos/opentofu/opentofu/releases/latest | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')

# Проверка, скачиваем ли мы релиз
if [ -z "$VERSION" ]; then
    echo "Не удалось получить последнюю версию OpenTofu"
    exit 1
fi

echo "🔧 Устанавливается OpenTofu версии $VERSION для архитектуры $ARCH"

# Скачивание и установка
cd /tmp
curl -LO "https://github.com/opentofu/opentofu/releases/download/${VERSION}/tofu_${VERSION#v}_linux_${ARCH}.zip"

unzip "tofu_${VERSION#v}_linux_${ARCH}.zip"
sudo mv tofu /usr/local/bin/
rm "tofu_${VERSION#v}_linux_${ARCH}.zip"

# Проверка
echo "✅ OpenTofu установлен:"
tofu version
