---
module: github.com/ramgml/orenda
go_version: 1.22+
license: MIT
status: pre-alpha
---

# Orenda — Product Requirements Document

## 1. Vision

**Orenda** — это локально запускаемый «второй мозг» для всей жизни человека: работа, личные дела, проекты, обучение, привычки, идеи. Главная особенность — **AI-агенты являются полноправными участниками workflow**: они создают задачи по просьбе пользователя, выполняют их, оставляют комментарии, получают обратную связь и продолжают работу. Человек — владелец, ревьюер и инициатор.

Имя **Orenda** (ирокез.) — внутренняя сила, пронизывающая всё сущее.

## 2. Цели

| ID | Цель | Критерий успеха |
|----|------|-----------------|
| G1 | Запускается локально одной командой | `./orenda serve` → UI на http://127.0.0.1:2137 |
| G2 | AI-агенты работают через REST API | Регистрация → claim → submit → review |
| G3 | Человек и агент сотрудничают в одной задаче | Общие комментарии, файлы, упоминания, история |
| G4 | База знаний хранит контекст и опыт | Wiki-страницы, wiki-links, FTS-поиск |
| G5 | Календарь покрывает задачи и события | day/week/month, drag-and-drop, recurrence |
| G6 | Бэкапы не теряют данные даже при падении диска | git remote + sqlite snapshot + WAL archive |
| G7 | Pluggable уведомления | VK, Telegram, Email, Webhook, Console |
| G8 | PWA offline-first | UI работает без сети, синхронизируется при онлайне |

## 3. Не-цели (Non-goals)

- Многопользовательская SaaS-модель (single-user).
- Real-time collaboration > 1 пользователь онлайн одновременно.
- Мобильные нативные приложения (только PWA).
- Интеграция с календарём Google/Apple (отложено).
- Платная подписка / монетизация.
- Multi-tenant.

## 4. Пользователи

### Primary user — Владелец (человек)
- Создаёт проекты, задачи, страницы wiki.
- Инициирует общение с AI-агентом: «сделай X», «проверь Y».
- Проверяет результаты агентов, оставляет фидбек.
- Настраивает бэкапы, уведомления, агентов.

### Secondary user — AI-агент (программный клиент)
- Получает API-токен с ограниченными scope'ами.
- Создаёт/берёт/обновляет задачи через REST API.
- Оставляет комментарии и заметки для контекста.
- Получает события через long-poll или WebSocket.
- Использует `@owner` для упоминания владельца.

## 5. Ключевые сценарии (User Stories)

### S1. Пользователь просит агента создать задачу
> «Сделай мне лендинг для продукта X»

1. Пользователь отправляет запрос агенту (вне Orenda — в чате с агентом).
2. Агент вызывает `POST /api/v1/tasks` с описанием.
3. Задача появляется в UI владельца.
4. Владелец уточняет комментарием + прикрепляет файл с референсом.
5. Агент видит событие, читает контекст, продолжает.

### S2. Агент берёт задачу в работу
1. Агент `GET /api/v1/tasks?status=todo&assignee=free`.
2. Выбирает подходящую, делает `POST /tasks/:id/claim`.
3. Статус: `in_progress`, `assignee: agent/qwen`.
4. Владельцу уходит уведомление (in-app + бот).

### S3. Агент просит уточнить
1. Агент пишет `POST /tasks/:id/comments {body: "Нужны логотип и цвета"}`.
2. Владелец видит в UI, отвечает + прикрепляет файл.
3. Уведомление агенту через long-poll.
4. Агент продолжает: `PATCH /tasks/:id {progress: 60}`.

### S4. Агент завершает, владелец проверяет
1. Агент: `POST /tasks/:id/submit {status: review}`.
2. Задача в колонке «На проверке».
3. Владелец: `POST /tasks/:id/review {decision: approve}`.
4. Задача в `done`.

### S5. Владелец возвращает задачу агенту
1. Владелец: `POST /tasks/:id/review {decision: reject, comment: "Не та цветовая гамма"}`.
2. Задача возвращается в `in_progress` с `awaiting: agent`.

### S6. Календарь событий
1. Владелец создаёт событие «Встреча с клиентом» 15.08 14:00.
2. Видит в day/week/month.
3. Агент имеет read-only доступ к календарю.

### S7. База знаний
1. Владелец создаёт страницу «Архитектура Orenda».
2. Вставляет `[[Taskloom]]`, `[[Бэкапы]]`.
3. Автолинковка. Backlinks видны.

### S8. Бэкап
1. Владелец настраивает git remote в Settings → Backups.
2. Каждые 5 минут git commit + push.
3. Каждый день 03:00 sqlite snapshot.

### S9. Уведомление в VK
1. Владелец подключает VK Community Bot в Settings.
2. Подписывается на событие `task.review_needed`.
3. Получает сообщение с кнопками «Принять / Вернуть».

### S10. Offline
1. Владелец в метро без сети.
2. UI открывается (service worker).
3. Создаёт задачу — попадает в IndexedDB outbox.
4. При онлайне — flush через `POST /sync`.

## 6. Функциональные требования

### 6.1. Управление задачами

| ID | Требование | Приоритет |
|----|------------|-----------|
| F-T-1 | CRUD задач с полями: title, description, status, priority, assignee, due_at, time_estimate, time_spent, parent_task | P0 |
| F-T-2 | Подзадачи (subtasks) и чек-листы | P0 |
| F-T-3 | Статусы: backlog → todo → in_progress → review → done, rejected → todo | P0 |
| F-T-4 | Атомарный claim задачи агентом (защита от двойного claim) | P0 |
| F-T-5 | Heartbeat агента, статус online/offline (TTL 2 мин) | P0 |
| F-T-6 | Контекст задачи: context_md, agent_notes, awaiting | P0 |
| F-T-7 | Комментарии (markdown) с упоминаниями `@user` и `@agent` | P0 |
| F-T-8 | Вложения (файлы) | P0 |
| F-T-9 | Audit log всех действий (task_activity) | P0 |
| F-T-10 | Перемещение по колонкам канбана (drag-and-drop) | P0 |
| F-T-11 | WIP-лимиты на колонки | P1 |
| F-T-12 | Таймер + ручной ввод time_entries | P0 |
| F-T-13 | Теги и фильтры | P1 |
| F-T-14 | Recurrence для повторяющихся задач | P2 |

### 6.2. Проекты и канбан

| ID | Требование | Приоритет |
|----|------------|-----------|
| F-P-1 | CRUD проектов с цветом и описанием | P0 |
| F-P-2 | Архивирование проектов | P1 |
| F-P-3 | Boards и columns (multiple boards per project) | P0 |
| F-P-4 | Drag-and-drop задач между колонками | P0 |
| F-P-5 | Реалтайм-синхронизация через WebSocket | P0 |
| F-P-6 | WIP-лимиты | P1 |

### 6.3. Календарь

| ID | Требование | Приоритет |
|----|------------|-----------|
| F-C-1 | CRUD событий: title, start_at, end_at, all_day, color, recurrence | P0 |
| F-C-2 | Виды: day / week / month / agenda | P0 |
| F-C-3 | Привязка задач к датам (drag из канбана) | P1 |
| F-C-4 | Уведомление за N минут до события | P2 |

### 6.4. База знаний (wiki)

| ID | Требование | Приоритет |
|----|------------|-----------|
| F-W-1 | Иерархия страниц (parent_id) | P0 |
| F-W-2 | Markdown-редактор (Tiptap) | P0 |
| F-W-3 | Wiki-links `[[slug]]` с авто-созданием связей | P0 |
| F-W-4 | Backlinks | P1 |
| F-W-5 | FTS5 полнотекстовый поиск | P0 |
| F-W-6 | Граф связей | P2 |

### 6.5. Агенты

| ID | Требование | Приоритет |
|----|------------|-----------|
| F-A-1 | CRUD агентов (имя, тип, описание) | P0 |
| F-A-2 | Выдача API-токенов с scopes | P0 |
| F-A-3 | Heartbeat (TTL 2 мин) | P0 |
| F-A-4 | Claim с lock на задачу | P0 |
| F-A-5 | Авто-assignment (опционально): агенты берут задачи из inbox | P1 |
| F-A-6 | Дашборд активных агентов | P0 |
| F-A-7 | История активности агента | P1 |

### 6.6. Уведомления

| ID | Требование | Приоритет |
|----|------------|-----------|
| F-N-1 | In-app через WebSocket | P0 |
| F-N-2 | Long-poll fallback для агентов | P0 |
| F-N-3 | Pluggable bot-интерфейс | P0 |
| F-N-4 | Боты: VK, Telegram, Email, Webhook, Console | P1 (в Фазе 10) |
| F-N-5 | Подписки: пользователь выбирает каналы и события | P0 |
| F-N-6 | Интерактивные кнопки (accept/reject) в VK/TG | P2 |

### 6.7. Бэкапы

| ID | Требование | Приоритет |
|----|------------|-----------|
| F-B-1 | Markdown-зеркало сущностей в `data/mirror/` (git-репо) | P0 |
| F-B-2 | Авто-commit + push каждые 5 минут | P0 |
| F-B-3 | Настройка git remote в UI (GitHub/Bitbucket/SourceCraft/custom) | P0 |
| F-B-4 | SQLite .backup ежедневно + ротация (30 дн + 12 мес) | P0 |
| F-B-5 | WAL-archive каждые 15 мин | P1 |
| F-B-6 | UI: статус, список снапшотов, restore | P0 |
| F-B-7 | CLI: `orenda backup push`, `orenda backup snapshot`, `orenda backup status` | P0 |

### 6.8. PWA

| ID | Требование | Приоритет |
|----|------------|-----------|
| F-PWA-1 | vite-plugin-pwa, manifest.json | P1 |
| F-PWA-2 | Service worker кеширует shell | P1 |
| F-PWA-3 | IndexedDB-кэш последних ответов | P1 |
| F-PWA-4 | Outbox для оффлайн-записи + flush на `/sync` | P1 |

### 6.9. Аутентификация

| ID | Требование | Приоритет |
|----|------------|-----------|
| F-AU-1 | Один владелец (email + bcrypt password) | P0 |
| F-AU-2 | JWT cookie сессии для UI | P0 |
| F-AU-3 | Opaque API-токены (bcrypt-хеши) для агентов | P0 |
| F-AU-4 | Scopes API-токенов | P0 |
| F-AU-5 | Rate limiting (token bucket per token) | P1 |

## 7. Нефункциональные требования

| ID | Требование | Целевое значение |
|----|------------|------------------|
| NFR-1 | Холодный старт сервера | < 500 мс |
| NFR-2 | Latency REST API (p95) | < 50 мс |
| NFR-3 | Latency WS push | < 100 мс |
| NFR-4 | Размер бинаря (без web) | < 30 МБ |
| NFR-5 | Размер бинаря (с web) | < 60 МБ |
| NFR-6 | Размер БД при 10k задач | < 100 МБ |
| NFR-7 | FTS-поиск (p95) | < 200 мс |
| NFR-8 | Concurrent WS клиентов | 50 |
| NFR-9 | Concurrent агентов | 20 |
| NFR-10 | Время восстановления из бэкапа | < 5 мин |
| NFR-11 | Zero-knowledge: данные не покидают машину | да |

## 8. Технический стек

### Backend (Go 1.22+)
- HTTP: `net/http` + `github.com/go-chi/chi/v5`
- БД: `modernc.org/sqlite` (pure Go, без CGO)
- Миграции: `github.com/golang-migrate/migrate/v4`
- ORM: `database/sql` + `github.com/jmoiron/sqlx`
- WebSocket: `github.com/gorilla/websocket`
- JWT: `github.com/golang-jwt/jwt/v5`
- UUID: `github.com/google/uuid`
- CLI: `github.com/spf13/cobra`
- Логи: `go.uber.org/zap`
- Конфиг: `github.com/spf13/viper` или `gopkg.in/yaml.v3`
- Валидация: `github.com/go-playground/validator/v10`
- Telegram: `github.com/go-telegram-bot-api/telegram-bot-api/v5` (в Фазе 10)
- VK: `github.com/SevereCloud/vksdk/v3` (в Фазе 10)

### Frontend (React 18 + TS)
- Билд: Vite
- UI: Tailwind CSS + shadcn/ui
- State: `@tanstack/react-query` + `zustand`
- Роутинг: `react-router-dom`
- Канбан: `@dnd-kit/core`
- Календарь: `react-big-calendar`
- Markdown: `@tiptap/react` + lowlight
- PWA: `vite-plugin-pwa` + Workbox
- HTTP: `axios`
- WebSocket: нативный + reconnect-обёртка
- Линт: ESLint + Prettier
- Тесты: Vitest + Playwright

### Инфраструктура
- Dev: Go air + Vite dev-server с proxy
- Билд: единый бинарь с `embed.FS`
- CI: golangci-lint, eslint, тесты, билд (опционально)
- Деплой: `systemd --user` unit или просто бинарь
- Логи: структурные в `data/logs/`

## 9. Архитектура

### Слои

```
┌─────────────────────────────────────────────────┐
│  React SPA (web/dist)                           │
│  Features: kanban | calendar | wiki | agents    │
│  Shared: api, ws, ui, hooks                     │
└──────────────────┬──────────────────────────────┘
                   │ REST + WS
┌──────────────────▼──────────────────────────────┐
│  HTTP API (chi router)                          │
│  - handlers (REST)                              │
│  - ws hub                                       │
│  - middleware: auth, ratelimit, logging, CORS   │
└──────────────────┬──────────────────────────────┘
                   │
┌──────────────────▼──────────────────────────────┐
│  Services (бизнес-логика)                       │
│  - task service                                 │
│  - agent service                                │
│  - notification service (notifier → bots)       │
│  - search service (FTS5)                        │
│  - mention service                              │
│  - mirror service (markdown write)              │
└──────────────────┬──────────────────────────────┘
                   │
┌──────────────────▼──────────────────────────────┐
│  Storage (SQLite + FTS5)                        │
│  - Repositories                                 │
│  - Migrations                                    │
│  - Triggers (FTS sync)                          │
└──────────────────┬──────────────────────────────┘
                   │
                   ▼
            data/orenda.db
```

### Бот-абстракция

```go
type Bot interface {
    Name() string
    Start(ctx) error
    Stop(ctx) error
    Send(ctx, target, msg) error
    OnEvent(ctx, event) error
}

type Notifier interface {  // фасад
    Notify(ctx, event) error  // раскидывает по подпискам и ботам
}
```

Боты реализуют один интерфейс; notifier-фасад читает `bot_subscriptions` и шлёт в нужные каналы с шаблонами.

## 10. Структура каталогов

```
orenda/
├── cmd/orenda/main.go
├── internal/
│   ├── api/                    # HTTP и WS
│   ├── domain/                 # интерфейсы и DTO
│   ├── storage/sqlite/         # репозитории + миграции
│   ├── service/                # бизнес-логика
│   ├── bot/                    # pluggable bots
│   ├── mirror/                 # markdown-зеркало
│   ├── backup/                 # git push + sqlite snapshot
│   ├── auth/                   # JWT + opaque tokens
│   ├── embed/web/              # embed.FS статика
│   └── config/
├── web/                        # Vite + React
├── data/                       # runtime (gitignored)
├── scripts/
├── docs/
│   ├── PRD.md                  # этот файл
│   ├── PLAN.md                 # фазы и задачи
│   ├── ARCHITECTURE.md         # детальная архитектура
│   ├── API.md                  # REST API reference
│   └── DB.md                   # схема БД
├── configs/                    # примеры конфигов
├── Makefile
├── go.mod
└── README.md
```

## 11. Безопасность

- Все эндпоинты (кроме `/healthz`, `/api/v1/auth/login`, `/api/v1/webhooks/*`) требуют auth.
- Пароли — bcrypt cost 12.
- API-токены для агентов хранятся как bcrypt-хеш, открытый токен показывается только при создании.
- Все мутации идут через prepared statements (нет SQL-инъекций).
- Markdown рендерится в UI через санитайзер (DOMPurify).
- Загруженные файлы имеют whitelisted mime + расширение.
- CORS настроен только для loopback.
- Rate limit: 100 req/s на токен, 1000 req/s на IP.

## 12. Глоссарий

| Термин | Значение |
|--------|----------|
| **Orenda** | Имя продукта (ирокез. «внутренняя сила») |
| **Агент** | Программный клиент (AI), работает через API |
| **Claim** | Атомарный переход задачи в `in_progress` агентом |
| **Heartbeat** | Периодический сигнал «я жив» от агента |
| **Awaiting** | enum: `none | human | agent` — кто должен действовать |
| **Mirror** | Markdown-копия сущностей для git-истории |
| **Snapshot** | SQLite .backup на момент времени |
| **Scope** | Разрешение API-токена (`tasks:read` и т.п.) |
| **Long-poll** | Endpoint `/events/await` для агентов без WS |
| **Outbox** | Очередь записей в IndexedDB для оффлайна |

## 13. Открытые вопросы (для будущих фаз)

- Multi-device sync (Phase 11+).
- Mobile native (Phase 12+).
- E2E шифрование (Phase 13+).
- Плагины (Phase 14+).

## 14. История изменений

| Дата | Версия | Изменения |
|------|--------|-----------|
| 2026-08-08 | 0.1.0 | Начальная версия PRD |