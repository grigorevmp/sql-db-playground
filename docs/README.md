# Документация

Вся проектная документация собрана в этой папке.

## Что здесь есть

- [frontend-client.md](/Users/grigorevmp/Downloads/app/docs/frontend-client.md)
  схема клиентской части, навигационный граф и описание основных экранов
- [openapi.yaml](/Users/grigorevmp/Downloads/app/docs/openapi.yaml)
  OpenAPI-спецификация backend API
- [api-docs.html](/Users/grigorevmp/Downloads/app/docs/api-docs.html)
  HTML-обзор backend API
- [threat-model.md](/Users/grigorevmp/Downloads/app/docs/threat-model.md)
  модель угроз, техники атак, меры защиты и остаточные риски
- [testing.md](/Users/grigorevmp/Downloads/app/docs/testing.md)
  обязательные тесты, покрытие CI и локальный запуск проверок

## Как смотреть

- из репозитория:
  - откройте файлы напрямую из `docs/`
- на поднятом backend:
  - [http://localhost:3001/api/docs](http://localhost:3001/api/docs)
  - [http://localhost:3001/api/openapi.yaml](http://localhost:3001/api/openapi.yaml)

## Состав документации

- Клиентская часть:
  - роли и маршруты
  - граф навигации
  - структура экранов
  - поток данных `catalog` / `runtime`
- Серверная часть:
  - REST и WebSocket API
  - авторизация и bootstrap
  - action endpoint
- Безопасность:
  - модель угроз
  - техники злоумышленника
  - текущие меры защиты
  - остаточные риски
- Тестирование:
  - backend smoke и security tests
  - frontend lint/build checks
  - GitHub Actions workflow
