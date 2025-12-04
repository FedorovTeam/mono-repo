#!/bin/bash

# Обновление системы
sudo apt update

# Установка Git
sudo apt install git -y

# Настройка имени пользователя и электронной почты
echo "Введите ваше имя для Git:"
read username
git config --global user.name "$username"

echo "Введите ваш email для Git:"
read email
git config --global user.email "$email"

# Создание SSH-ключа
ssh-keygen -t rsa -b 4096 -C "$email"

# Запуск SSH-агента
eval "$(ssh-agent -s)"
ssh-add ~/.ssh/id_rsa

# Вывод публичного ключа для добавления в Git-сервис
echo "Скопируйте следующий SSH-ключ и добавьте его в настройки вашего Git-сервиса:"
cat ~/.ssh/id_rsa.pub

# Вывод глобальных конфигураций Git
git config --list

echo "Git успешно настроен!"
