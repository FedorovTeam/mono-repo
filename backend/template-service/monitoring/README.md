# Monitoring & Observability for MoiQR Template Service

## Prometheus Alerts
- Файл `alerts.yaml` содержит пример алертов:
  - Сервис недоступен (`up == 0`)
  - Высокий процент ошибок 5xx

## Grafana Dashboard
- Файл `grafana_dashboard.json` — базовый дашборд:
  - График ошибок 5xx
  - График RPS
  - Статус up/down

## Использование
1. Импортируйте `grafana_dashboard.json` в Grafana.
2. Добавьте правила из `alerts.yaml` в конфиг Prometheus.
3. Настройте Prometheus scrape для `template-service` (`/metrics`).

> Для расширения мониторинга добавьте кастомные метрики и дополнительные панели в Grafana.
