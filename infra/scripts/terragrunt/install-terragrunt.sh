#!/bin/bash

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

# Получение последней версии Terragrunt
VERSION=$(curl -s https://api.github.com/repos/gruntwork-io/terragrunt/releases/latest | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')

if [ -z "$VERSION" ]; then
    echo "Не удалось получить последнюю версию Terragrunt"
    exit 1
fi

echo "🔧 Устанавливается Terragrunt версии $VERSION для архитектуры $ARCH"

# Скачивание и установка
cd /tmp
curl -LO "https://github.com/gruntwork-io/terragrunt/releases/download/${VERSION}/terragrunt_linux_${ARCH}"
sudo mv terragrunt_linux_${ARCH} /usr/local/bin/terragrunt
sudo chmod +x /usr/local/bin/terragrunt

# Проверка установки
echo "✅ Terragrunt установлен:"
terragrunt -v

# Проверка, установлен ли tofu
if ! command -v tofu &> /dev/null; then
    echo "⚠️ OpenTofu (tofu) не установлен. Установи tofu перед использованием Terragrunt с OpenTofu."
    exit 0
fi

# Настройка Terragrunt на использование tofu вместо terraform
echo "🔧 Настройка Terragrunt на использование tofu"
mkdir -p ~/.terragrunt
echo 'terraform_binary = "tofu"' > ~/.terragrunt/config.hcl

echo "✅ Готово. Terragrunt теперь использует tofu вместо terraform."
