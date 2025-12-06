# syntax=docker.io/docker/dockerfile:1

######################################
# Базовый образ Node
######################################
FROM node:23-alpine AS base

# ------------------------------------
# Зависимости (deps)
# ------------------------------------
FROM base AS deps
# При необходимости установить дополнительные пакеты:
RUN apk add --no-cache libc6-compat
WORKDIR /app

# Копируем файлы для установки зависимостей
COPY package.json yarn.lock* package-lock.json* pnpm-lock.yaml* .npmrc* ./

# Устанавливаем зависимости в зависимости от менеджера пакетов
RUN \
  if [ -f yarn.lock ]; then \
    yarn --frozen-lockfile; \
  elif [ -f package-lock.json ]; then \
    npm ci; \
  elif [ -f pnpm-lock.yaml ]; then \
    corepack enable pnpm && pnpm i --frozen-lockfile; \
  else \
    echo "Lockfile not found." && exit 1; \
  fi

######################################
# Сборка (builder)
######################################
FROM base AS builder
WORKDIR /app

# Переносим установленные node_modules
COPY --from=deps /app/node_modules ./node_modules

# Копируем остальной код
COPY . .

# Можно отключить телеметрию Next.js на этапе сборки
# ENV NEXT_TELEMETRY_DISABLED=1

# Сборка проекта (standalone-режим указывается в next.config или auto-вывод)
RUN \
  if [ -f yarn.lock ]; then \
    yarn run build; \
  elif [ -f package-lock.json ]; then \
    npm run build; \
  elif [ -f pnpm-lock.yaml ]; then \
    corepack enable pnpm && pnpm run build; \
  else \
    echo "Lockfile not found." && exit 1; \
  fi

######################################
# Финальный образ (runner)
######################################
FROM base AS runner
WORKDIR /app

ENV NODE_ENV=production

# Можно отключить телеметрию Next.js во время рантайма:
# ENV NEXT_TELEMETRY_DISABLED=1

# Создаём системного пользователя для безопасности
RUN addgroup --system --gid 1001 nodejs
RUN adduser --system --uid 1001 nextjs

# Копируем public, если нужно (из builder)
COPY --from=builder --chown=nextjs:nodejs /app/public ./public

# Переносим сборку из .next/standalone + .next/static
COPY --from=builder --chown=nextjs:nodejs /app/.next/standalone ./
COPY --from=builder --chown=nextjs:nodejs /app/.next/static ./.next/static

# Запуск от пользователя nextjs
USER nextjs

# Next.js будет слушать 3000
EXPOSE 3000
ENV PORT=3000

# Нередко необходимо слушать 0.0.0.0
ENV HOSTNAME="0.0.0.0"

# Запускаем standalone сервер (server.js генерируется Next.js)
CMD ["node", "server.js"]
