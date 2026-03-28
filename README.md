# Игровая площадка ИВТ

Многопользовательская платформа для обучения SQL с семинарным режимом, серверной автопроверкой, live-мониторингом преподавателя и отдельным playground.

Текущая версия работает как связка:

- `React + Vite` frontend
- `Go` backend
- `WebSocket` для live-синхронизации
- серверное хранение состояния в `server-data/state.json`
- серверный каталог пользователей, групп, семинаров, шаблонов и задач

## Что реализовано по ТЗ

### Реализовано

- Авторизация по логину и паролю с ролями `student`, `teacher`, `admin`
- Общий серверный runtime для группы
- Live-обновления преподавательского интерфейса через WebSocket
- Семинарный режим:
  - студент видит только открытые семинары, выбранное задание, SQL-редактор и таблицу результата
  - преподаватель видит сводку по семинару, кто что сдавал, список задач и полную схему/диаграмму
  - преподаватель может открыть эталонное решение студентам отдельным переключателем
- Playground:
  - выбор шаблона
  - фильтры по сложности, теме и конструкции
  - запуск запросов
  - challenge-валидация
- Автопроверка по нескольким датасетам
- Проверка `SELECT`-решений по результату
- Проверка `VIEW`, `TRIGGER`, `INDEX`, `EXPLAIN QUERY PLAN`
- Изоляция выполнения: каждый запуск идёт в отдельной in-memory SQLite БД на backend
- frontend больше не получает исходный `seed.ts`; каталог теперь приходит только с backend API по роли пользователя
- скрытые решения и стартовый SQL не отправляются студенту, пока преподаватель явно не откроет доступ
- password hashing переведён на `Argon2id`, legacy `SHA-256` принимается только для прозрачной миграции при входе
- origin allowlist, CSP/security headers, rate limiting для логина/API/WebSocket, строгая валидация JSON и полей
- Панель преподавателя:
  - открыть/закрыть семинар
  - заморозить отправки
  - включать и выключать рейтинг, уведомления, диагностику и автопроверку
  - видеть матрицу прогресса
  - смотреть тексты запросов
  - экспортировать `CSV` и `XLSX`
  - создавать задачи
  - добавлять и удалять задачи из текущего семинара
- Панель администратора:
  - группы и студенты
  - преподаватели
  - библиотека шаблонов БД
  - импорт SQL-схем и датасетов
  - телеметрия
- Аудит событий и серверная история действий

### Реализовано частично

- Несколько семинаров:
  - каталог семинаров есть
  - основной live-процесс работает вокруг выбранного активного семинара
- Persistence:
  - данные уже серверные
  - пока это JSON state-file, а не PostgreSQL

### Пока не реализовано

- PostgreSQL sandbox на пользователя
- `PROCEDURE / FUNCTION` в стиле PostgreSQL
- `EXPLAIN ANALYZE` и реальная performance-проверка PostgreSQL
- LMS/SSO интеграции
- production БД платформы для долговременного хранения
- полноценный внешний WAF: в репозитории есть reverse proxy и fail2ban-конфиги, но сам WAF остаётся инфраструктурной задачей

## Архитектура

### Frontend

- [App.tsx](/Users/grigorevmp/Downloads/app/src/App.tsx)
  основной shell, student/teacher/admin/playground интерфейсы
- [api.ts](/Users/grigorevmp/Downloads/app/src/lib/api.ts)
  REST API клиент, JWT storage, WebSocket подключение

### Go backend

- [main.go](/Users/grigorevmp/Downloads/app/backend/main.go)
  HTTP API, JWT, actions, state management, WebSocket, static serving
- [sql.go](/Users/grigorevmp/Downloads/app/backend/sql.go)
  изолированное выполнение SQL и автопроверка
- [security.go](/Users/grigorevmp/Downloads/app/backend/security.go)
  rate limits, валидация входа и полей, security headers, Argon2id и проверка legacy-хэшей
- [types.go](/Users/grigorevmp/Downloads/app/backend/types.go)
  серверные модели данных
- [seed.json](/Users/grigorevmp/Downloads/app/backend/seed.json)
  серверный seed для пользователей, задач, шаблонов и runtime; в браузер напрямую не отгружается
- [go.mod](/Users/grigorevmp/Downloads/app/backend/go.mod)
  Go module backend-сервиса
- [docs/README.md](/Users/grigorevmp/Downloads/app/docs/README.md)
  единая точка входа в документацию проекта
- [frontend-client.md](/Users/grigorevmp/Downloads/app/docs/frontend-client.md)
  схема клиентской части, граф навигации и описание экранов
- [openapi.yaml](/Users/grigorevmp/Downloads/app/docs/openapi.yaml)
  OpenAPI-спецификация backend API
- [api-docs.html](/Users/grigorevmp/Downloads/app/docs/api-docs.html)
  HTML-обзор backend API
- [threat-model.md](/Users/grigorevmp/Downloads/app/docs/threat-model.md)
  модель угроз, техники атак и остаточные риски
- [testing.md](/Users/grigorevmp/Downloads/app/docs/testing.md)
  обязательные тесты, покрытие CI и локальный запуск проверок
- [docker-compose.prod.yml](/Users/grigorevmp/Downloads/app/docker-compose.prod.yml)
  production-compose со связкой `app + caddy`
- [Caddyfile](/Users/grigorevmp/Downloads/app/deploy/Caddyfile)
  reverse proxy и TLS-терминация
- [jail.local](/Users/grigorevmp/Downloads/app/deploy/fail2ban/jail.local)
  пример fail2ban-конфигурации для блокировки по `401/403/429`

## Демо-вход

- Преподаватель: `admin` / `adminmephi`
- Студент: вход по фамилии
- фамилии и группы администратор может менять в интерфейсе
- После входа студенту показываются только открытые семинары
- Эталонный запрос виден студенту только если преподаватель включил `Показывать эталон студентам`

## Переменные окружения

- `PORT`
  порт Go backend, по умолчанию `3001`
- `JWT_SECRET`
  секрет подписи токенов
- `ALLOWED_ORIGINS`
  список разрешённых origin через запятую; для production должен совпадать с внешним HTTPS-доменом
- `VITE_API_BASE_URL`
  опционально; нужен, если frontend должен обращаться к другому origin
- `DOMAIN`
  домен для production reverse proxy через `docker-compose.prod.yml`

## Локальная разработка

Требования:

- Node.js `20+`
- npm `10+`
- Go `1.23+`

Установка frontend:

```bash
npm install
```

Установка backend-зависимостей:

```bash
cd backend
go mod tidy
cd ..
```

Запуск в двух терминалах:

```bash
npm run dev:server
```

```bash
npm run dev:client
```

По умолчанию:

- frontend: `http://localhost:5173`
- Go backend: `http://localhost:3001`
- API docs: `http://localhost:3001/api/docs`
- raw OpenAPI: `http://localhost:3001/api/openapi.yaml`
- индекс документации: [docs/README.md](/Users/grigorevmp/Downloads/app/docs/README.md)

Проверка:

```bash
npm run lint
npm run build
cd backend && go build .
```

Быстрый прогон CI-чеков локально:

```bash
npm run test:ci
```

## GitHub CI

Добавлен workflow:

- [.github/workflows/ci.yml](/Users/grigorevmp/Downloads/app/.github/workflows/ci.yml)

Он запускает два обязательных контура:

- `Backend tests`
  - `go test .`
  - покрывает вход преподавателя, student-sanitization каталога, bootstrap по JWT, invalid token, CORS, role restrictions и раздачу API docs
- `Frontend lint and build`
  - `npm run lint`
  - `npm run build`

Серверные тесты находятся в:

- [backend/main_test.go](/Users/grigorevmp/Downloads/app/backend/main_test.go)
- подробное описание тестового контура: [docs/testing.md](/Users/grigorevmp/Downloads/app/docs/testing.md)

## Docker Compose

Самый простой способ поднять стенд:

```bash
docker compose up --build -d
```

После старта:

- web UI и API: `http://localhost:3001`

Остановить:

```bash
docker compose down
```

Остановить и полностью сбросить состояние семинаров:

```bash
docker compose down -v
```

Compose поднимает один контейнер:

- frontend собирается внутри Docker
- Go backend собирается внутри Docker
- backend раздаёт и API, и готовый `dist`
- серверное состояние хранится в volume `sql-seminar-data`

Перед показом наружу поменяйте `JWT_SECRET` в [docker-compose.yml](/Users/grigorevmp/Downloads/app/docker-compose.yml).

## Production-стенд с TLS

Для показа на семинарах с внешним доменом используйте production-compose:

1. Создайте `.env` рядом с `docker-compose.prod.yml`:

```bash
DOMAIN=seminar.example.com
JWT_SECRET=замените-на-длинный-случайный-секрет
```

2. Поднимите сервис:

```bash
docker compose -f docker-compose.prod.yml up --build -d
```

3. Проверьте:

```bash
docker compose -f docker-compose.prod.yml ps
curl https://seminar.example.com/api/health
```

Что делает production-стек:

- backend не публикуется наружу напрямую
- `Caddy` принимает `80/443`, поднимает TLS и проксирует в приложение
- backend принимает запросы только от разрешённых origin
- access log `Caddy` можно подключить к fail2ban

### Fail2ban

В репозитории есть шаблоны:

- [jail.local](/Users/grigorevmp/Downloads/app/deploy/fail2ban/jail.local)
- [caddy-auth.conf](/Users/grigorevmp/Downloads/app/deploy/fail2ban/filter.d/caddy-auth.conf)

Подключение на хосте:

```bash
sudo cp deploy/fail2ban/jail.local /etc/fail2ban/jail.d/ivt-playground.local
sudo cp deploy/fail2ban/filter.d/caddy-auth.conf /etc/fail2ban/filter.d/caddy-auth.conf
sudo systemctl restart fail2ban
sudo fail2ban-client status caddy-auth
```

## Как поднять на сервере для семинаров

Рекомендуемый вариант:

- сервер с Docker Engine и Docker Compose Plugin
- приложение запускается через `docker compose -f docker-compose.prod.yml`
- TLS завершает `Caddy`
- fail2ban анализирует `Caddy` access log

### 1. Подготовить сервер

Нужно установить:

- Docker Engine
- Docker Compose Plugin
- fail2ban

### 2. Развернуть проект

```bash
git clone <repo-url> /opt/ivt-playground
cd /opt/ivt-playground
```

### 3. Настроить `.env`

```bash
cat > .env <<'EOF'
DOMAIN=seminar.example.com
JWT_SECRET=замените-на-длинный-случайный-секрет
EOF
```

### 4. Запустить контейнер

```bash
docker compose -f docker-compose.prod.yml up --build -d
```

### 5. Проверить

```bash
docker compose -f docker-compose.prod.yml ps
curl https://seminar.example.com/api/health
```

## Сценарий запуска перед парой

1. `git pull`
2. При изменении кода: `docker compose -f docker-compose.prod.yml up --build -d`
3. Если код не менялся: `docker compose -f docker-compose.prod.yml up -d`
4. Открыть платформу под `admin / adminmephi`
5. При необходимости очистить стенд: `docker compose -f docker-compose.prod.yml down -v && docker compose -f docker-compose.prod.yml up --build -d`
6. Проверить, что семинар открыт и задачи выставлены
7. Раздать ссылку студентам

## Где лежат данные

- backend module: [backend](/Users/grigorevmp/Downloads/app/backend)
- server state inside compose volume: `sql-seminar-data`
- server state without Docker: `server-data/state.json`
- frontend build: `dist/`
- access log reverse proxy: volume `caddy-logs`

## Текущие ограничения

- Это учебная семинарная платформа, а не полнофункциональная LMS
- Хранилище состояния файловое
- SQL engine основан на SQLite, а не на PostgreSQL
- Изоляция серверная, но не контейнерная
- `backend/seed.json` является серверным источником стартовых данных, но больше не попадает в клиентский bundle
- fail2ban и внешний WAF требуют настройки на хосте или на внешнем edge-слое
