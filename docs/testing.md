# Тесты и CI

Документ описывает, какие проверки считаются обязательными для проекта, что именно гоняется в GitHub Actions и как это запускать локально.

## Что проверяется сейчас

### Backend smoke и security tests

Файл:
- [backend/main_test.go](/Users/grigorevmp/Downloads/app/backend/main_test.go)

Покрытые сценарии:

- успешный вход преподавателя и администратора;
- прозрачная миграция legacy `SHA-256` пароля в `Argon2id`;
- вход студента с role-aware выдачей данных;
- отсутствие `passwordHash`, `accessCode`, `expectedQuery`, `starterSql` в student catalog по умолчанию;
- `bootstrap` по JWT;
- отказ по `Invalid token`;
- блокировка запроса с чужого `Origin`;
- запрет студенту открывать закрытый семинар;
- запрет студенту выполнять teacher-only action;
- раскрытие эталона преподавателем и корректная выдача студенту после этого;
- раздача [api-docs.html](/Users/grigorevmp/Downloads/app/docs/api-docs.html) и [openapi.yaml](/Users/grigorevmp/Downloads/app/docs/openapi.yaml).

### Frontend checks

Проверки:

- `eslint`
- production `vite build`

Они ловят:

- синтаксические ошибки;
- типовые проблемы React/TS кода;
- поломки импортов;
- ошибки сборки после изменений в API или структуре компонентов.

## Что запускает GitHub Actions

Workflow:
- [.github/workflows/ci.yml](/Users/grigorevmp/Downloads/app/.github/workflows/ci.yml)

Jobs:

- `Backend tests`
  - `cd backend && go test .`
- `Frontend lint and build`
  - `npm ci`
  - `npm run lint`
  - `npm run build`

## Почему именно эти тесты

Это не набор “на всякий случай”. В CI оставлены именно те проверки, которые страхуют наиболее болезненные регрессии:

- поломка входа преподавателя прямо перед занятием;
- утечка скрытых решений студенту;
- потеря role-aware логики каталога;
- сломанный `bootstrap` после перезагрузки страницы;
- отказ документации API;
- сломанная production-сборка фронтенда.

## Локальный запуск

Быстрый прогон всего CI-контура:

```bash
npm run test:ci
```

Только backend:

```bash
cd backend && go test .
```

Только frontend checks:

```bash
npm run lint
npm run build
```

## Что имеет смысл добавить следующим этапом

Следующие действительно полезные тесты, если расширять покрытие дальше:

- интеграционный тест `submit-seminar-query` с реальной автопроверкой по нескольким датасетам;
- тест `run-playground-query` и `validate-playground-challenge`;
- тесты на CRUD групп, студентов и преподавателей через action endpoint;
- тесты на reset стенда и сохранение state-file;
- E2E через Playwright для критического пути `teacher opens seminar -> student submits -> teacher sees result`.
