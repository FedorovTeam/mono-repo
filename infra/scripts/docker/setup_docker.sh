#!/bin/bash

# Обновление системы
sudo apt update
sudo apt upgrade -y

# Установка необходимых пакетов
sudo apt install apt-transport-https ca-certificates curl software-properties-common -y

# Добавление официального GPG ключа Docker
curl -fsSL https://download.docker.com/linux/ubuntu/gpg | sudo gpg --dearmor -o /usr/share/keyrings/docker-archive-keyring.gpg

# Добавление репозитория Docker
echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/docker-archive-keyring.gpg] https://download.docker.com/linux/ubuntu $(lsb_release -cs) stable" | sudo tee /etc/apt/sources.list.d/docker.list > /dev/null

# Обновление информации о пакетах
sudo apt update

# Убедитесь, что Docker будет установлен из официального репозитория Docker, а не из репозитория Ubuntu
sudo apt-cache policy docker-ce

# Установка Docker
sudo apt install docker-ce docker-ce-cli containerd.io -y

# Проверка состояния Docker
sudo systemctl status docker

# Добавление текущего пользователя в группу docker (чтобы использовать Docker без sudo)
echo "Добавление пользователя $USER в группу docker"
sudo usermod -aG docker ${USER}

# Активирование изменений (перезагрузка сессии)
echo "Для применения изменений, пожалуйста, выйдите из системы и войдите снова, или выполните команду: newgrp docker"

# Проверка установки Docker версии
docker --version

# Установка Docker Compose (последней версии)
DOCKER_COMPOSE_VERSION=$(curl -s https://api.github.com/repos/docker/compose/releases/latest | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')
sudo curl -L "https://github.com/docker/compose/releases/download/$DOCKER_COMPOSE_VERSION/docker-compose-$(uname -s)-$(uname -m)" -o /usr/local/bin/docker-compose
sudo chmod +x /usr/local/bin/docker-compose

# Проверка версии Docker Compose
docker-compose --version

echo "Установка и настройка Docker завершена!"
