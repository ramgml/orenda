---
module: github.com/ramgml/orenda
go_version: 1.22+
license: MIT
status: pre-alpha
---

# Orenda — План разработки

Детальная разбивка по фазам с конкретными задачами, критериями готовности и оценкой сроков. Файл оптимизирован для AI-агентов: каждый шаг самодостаточен, можно выполнять параллельно.

> Согласовано с [[PRD]]: имя **Orenda** (Оренда), порт **2137**, стек **Go + React**.

---

> **Аудит реализации 2026-08-12** (сверка плана с кодом, не с чекбоксами): backend-ядро фаз 0–17, 19–25 реализовано; ни одна фаза не закрыта на 100%. Статусы под заголовками фаз: ✅ реализовано · 🟡 частично · ❌ минимально. **2026-08-12 update**: Phase 27.1 (D2), 27.2 (D1), 27.3 (D3), 27.4.A (backend), 27.4.B (frontend) — закрыты.
>
> Критичные дефекты:
> 1. ✅ **Фронтенд WS никогда не подключался** → **закрыт 2026-08-12 в Phase 27.2 / PR 1.2** (cookie-based upgrade, см. секцию ниже). Realtime UI работает end-to-end.
> 2. ✅ **`make build` не передавал `-tags=web_dist`** → **закрыт 2026-08-12 в Phase 27.1 / PR 1.1** (см. секцию ниже). Бинарь self-contained через `//go:embed all:dist`.
> 3. ✅ **Теги не попадали в list-payload** → **закрыт 2026-08-12 в Phase 27.3 / PR 1.3** (см. секцию ниже). Чипы на канбане видны.
>
> Миграции: `.down.sql` отсутствуют глобально; нумерация съехала относительно текста фаз (wiki=008, notifications=009, backups=010, sync=011, events_to_tasks=012, courses=019; миграции 018 нет; `tasks.color` добавлен в 012).  ➜ **Wave 4 / PR 4.1.** **✅ закрыт `phase-down-migrations`** — runner с маркером `-- orenda:irreversible` + 18 парных файлов.
>
> Приоритет фиксов — все выполнены 2026-08-12: WS-токен (27.2), `web_dist` (27.1), теги в payload (27.3), Phase 18 (27.4), Phase 26 (26.A–F).

---

## Phase 0 — Инициализация *(1–2 дня)*

> **Аудит 2026-08-12 (обновлено):** ✅ почти — `migrate down` реализован (Wave 4 PR 1: 18 парных `.down.sql`, маркер `-- orenda:irreversible`, 17 тестов); SPA встраивается в бинарь без build-тега (27.1: `//go:embed all:dist` + `embed-dists` в Makefile). Остался только Prettier (не настроен).

**Цель:** пустой репозиторий превращается в работающий dev-окружение с health-check.

### Tasks

- [ ] **0.1** Создать `go.mod` (`github.com/ramgml/orenda`)
- [ ] **0.2** Создать `cmd/orenda/main.go` с минимальным cobra-приложением
  - Команды: `serve`, `version`, `migrate`, `backup`
  - `--config` флаг для пути к `config.yaml`
- [ ] **0.3** Создать `Makefile` с целями:
  - `make dev` — Go через `air` + Vite dev-server с proxy на :2137
  - `make build` — production-бинарь с web/dist встроен
  - `make test`, `make lint`, `make migrate-up`, `make migrate-down`
  - `make backup`, `make backup-push`
- [ ] **0.4** Инициализировать Vite + React + TS в `web/`
  - `npm create vite@latest web -- --template react-ts`
  - Tailwind CSS + PostCSS
  - ESLint + Prettier
  - `web/vite.config.ts` с proxy `/api → http://127.0.0.1:2137`, `/ws → ws://127.0.0.1:2137`
- [ ] **0.5** Создать миграцию `001_init.sql` со схемой (см. [DB Schema](#db-schema))
- [ ] **0.6** Создать `internal/config/config.go` (yaml + env)
  - Поля: `host`, `port` (default 2137), `db_path`, `data_dir`, `log_level`
- [ ] **0.7** Создать `internal/storage/sqlite/db.go`
  - Открытие `data/orenda.db` через `modernc.org/sqlite`
  - `PRAGMA journal_mode=WAL`
  - `PRAGMA foreign_keys=ON`
  - `PRAGMA busy_timeout=5000`
- [ ] **0.8** Создать `internal/api/router.go` с chi + middleware:
  - `request_id`, `logging`, `recover`, `cors` (loopback only)
- [ ] **0.9** Endpoint `/healthz` → `200 OK {status: ok, version: ...}`
- [ ] **0.10** Endpoint `/api/v1/info` → версия, capabilities
- [ ] **0.11** Создать `internal/embed/web/embed.go` — `embed.FS` placeholder (до build web/dist)
- [ ] **0.12** Endpoint `/*` → статика из embed (пока 404)
- [ ] **0.13** `.gitignore`:
  - `data/` (кроме `data/.gitkeep` и `data/config.example.yaml`)
  - `web/dist/`
  - `web/node_modules/`
  - `*.test`, `*.out`
- [ ] **0.14** `.editorconfig`, `.golangci.yml`, `.eslintrc.cjs`
- [ ] **0.15** `README.md` с инструкцией quickstart
- [ ] **0.16** `data/config.example.yaml` со всеми параметрами

### Definition of Done

```bash
make dev
# → Vite dev-server на http://localhost:5173
# → Go сервер на http://127.0.0.1:2137

curl http://127.0.0.1:2137/healthz
# {"status":"ok","version":"0.1.0"}

# Vite proxy работает:
curl http://localhost:5173/healthz
# {"status":"ok","version":"0.1.0"}

make build
# → ./bin/orenda (Linux x86_64)
./bin/orenda version
# orenda version 0.1.0
```

---

## Phase 1 — Ядро *(1–2 недели)*

> **Аудит 2026-08-12:** 🟡 — JWT TTL 168h вместо 24h (`config.DefaultConfig`); cookie без `Secure`; нет маршрута `/projects/:id/tasks`; таблицы users/projects/tasks созданы в `001_init.sql` (файлы 002/003 — только индексы). Auth, CRUD, CLI `user create`, фронт-shell, тесты — есть.

**Цель:** users + api_tokens + projects + tasks CRUD, JWT и opaque-token auth, базовый UI со списком.

### Tasks

- [ ] **1.1** Миграция `002_auth.sql`:
  - `users(id, email UNIQUE, password_hash, display_name, role, created_at, updated_at)`
  - `api_tokens(id, user_id, name, hash, scopes_json, last_used_at, expires_at, created_at)`
- [ ] **1.2** Миграция `003_projects_tasks.sql`:
  - `projects, boards, columns, tasks, subtasks, checklists, checklist_items, task_tags, tags`
  - индексы: `(status, assignee_type, assignee_id)`, `(project_id, column_id, position)`
- [ ] **1.3** Domain слой:
  - `internal/domain/user/{model.go, repository.go}`
  - `internal/domain/project/{model.go, repository.go}`
  - `internal/domain/task/{model.go, repository.go}`
- [ ] **1.4** Repository слой:
  - `internal/storage/sqlite/user_repo.go`
  - `internal/storage/sqlite/project_repo.go`
  - `internal/storage/sqlite/task_repo.go`
  - Все методы с prepared statements, context, transactions где нужно
- [ ] **1.5** Auth модуль:
  - `internal/auth/password.go` — bcrypt cost 12
  - `internal/auth/jwt.go` — HS256, 24h TTL, cookie httpOnly Secure SameSite=Lax
  - `internal/auth/apitoken.go` — генерация (32 байта base64url), bcrypt хеш, scopes
- [ ] **1.6** Middleware:
  - `middleware.AuthUser` — JWT из cookie
  - `middleware.AuthAgent` — Bearer API-token
  - `middleware.RequireScope(...)` — проверка scopes
- [ ] **1.7** Handlers:
  - `POST /api/v1/auth/login` — email+password → JWT cookie
  - `POST /api/v1/auth/logout`
  - `GET /api/v1/me`
  - CRUD `/api/v1/projects`, `/api/v1/tasks`
- [ ] **1.8** CLI команда `orenda user create` — bootstrap первого пользователя
- [ ] **1.9** Frontend: базовый shell (AppLayout, NavBar, AuthContext)
  - `/login` — форма логина
  - `/` — Dashboard (пока пустой)
  - `/projects`, `/projects/:id`, `/projects/:id/tasks` — list-вью
- [ ] **1.10** React Query setup + axios client с interceptor'ом для JWT
- [ ] **1.11** Тесты:
  - Unit: bcrypt, jwt, scopes, repositories (с in-memory SQLite)
  - Integration: полный auth flow через httptest
  - Frontend: smoke-тест логина (Playwright)

### Definition of Done

- Регистрация одного пользователя через CLI.
- Логин в UI, cookie установлена.
- Создание проекта и задачи через UI.
- Список задач отображается.
- API-токен создаётся в UI, копируется.
- `curl -H "Authorization: Bearer <token>" /api/v1/tasks` работает.
- 401/403 для неавторизованных.

---

## Phase 2 — Канбан *(1 неделя)*

> **Аудит 2026-08-12 (обновлено):** ✅ почти — реалтайм в UI работает (27.2: cookie-based WS upgrade, E2E `ws-live.spec.ts` ловит реальный push без reload). Остаются: нет `POST /api/v1/boards`; дефолтных колонок 5, а не 4 (план сам противоречит Phase 12, где 5 — норма). Backend: Move с fractional positions, WIP-422, WS-hub, события `task.*`, dnd на @dnd-kit — покрыто тестами.

**Цель:** boards, columns, drag-and-drop, WebSocket синхронизация.

### Tasks

- [ ] **2.1** Repository для boards и columns
- [ ] **2.2** Default board + 4 columns создаются при создании проекта
- [ ] **2.3** Handlers:
  - `GET /api/v1/projects/:id/board` (с tasks в колонках)
  - `POST /api/v1/boards`, `PATCH /api/v1/columns/:id`
  - `POST /api/v1/tasks/:id/move {column_id, position}`
- [ ] **2.4** Service `task_service.Move(ctx, taskID, columnID, position)`:
  - Транзакция: обновить `column_id`, пересчитать `position` в колонках (дроби для сохранения порядка)
  - Записать в `task_activity`
  - Отправить WS-событие `task.moved`
- [ ] **2.5** WebSocket hub:
  - `internal/api/ws/hub.go` — клиенты по user_id + subscribed topics
  - `internal/api/ws/client.go` — read/write pump
  - Endpoint `GET /api/v1/ws?token=<JWT>`
  - События: `task.created`, `task.updated`, `task.moved`, `task.deleted`
- [ ] **2.6** Frontend: `web/src/features/kanban/`:
  - `Board` — набор `Column` с `TaskCard`
  - `@dnd-kit/core` для drag-and-drop
  - `react-query` подписка через WS (refetch on event)
- [ ] **2.7** Frontend WS клиент:
  - `web/src/shared/ws.ts` — singleton с reconnect-логикой
  - Invalidate react-query cache по event type
- [ ] **2.8** WIP-лимиты (Phase 2.5 — отложить в Phase 2.1):
  - Поле `wip_limit` в `columns`
  - При перемещении 422 если превышен
- [ ] **2.9** Тесты:
  - Move не ломает позиции
  - WS broadcast доходит до всех подписчиков

### Definition of Done

- Канбан-доска с 4 колонками.
- Drag-and-drop работает, позиция сохраняется.
- Открыть в двух вкладках — изменения видны realtime.
- Создание задачи прямо из канбана (inline).

---

## Phase 3 — Агенты + Human↔Agent коллаборация *(2 недели)*

> **Аудит 2026-08-12:** ✅ — backend полный (agents, claim/release/submit/review, comments+mentions, attachments с sha256-dedup, activity, long-poll `/events/await`, тесты incl. concurrent claim). Оговорки: composer — plain input (не markdown-редактор), нет autocomplete `@mentions`, синтаксис упоминаний `@user:<id>` вместо `@user`.

**Цель:** агенты регистрируются, claim'ят задачи, общаются с владельцем через комментарии и упоминания.

### Tasks

- [ ] **3.1** Миграция `004_agents.sql`:
  - `agents(id, name UNIQUE, type, description, token_id FK→api_tokens, last_seen_at, status, max_concurrent, created_at)`
  - `task_locks(task_id PK, agent_id, acquired_at)` — защита от двойного claim
- [ ] **3.2** Миграция `005_comments_attachments.sql`:
  - `comments(id, target_type, target_id, author_type, author_id, body_md, created_at)`
  - `mentions(comment_id, target_type, target_id)` — кто упомянут
  - `attachments(id, target_type, target_id, filename, mime, size, path, sha256, created_at)`
  - `task_activity(id, task_id, actor_type, actor_id, action, payload_json, created_at)`
- [ ] **3.3** Domain для agent, comment, attachment, activity
- [ ] **3.4** Repositories
- [ ] **3.5** Agent service:
  - `Register(ctx, name, type, description, scopes)` → возвращает токен (только при создании)
  - `Heartbeat(ctx, agentID)` → обновляет `last_seen_at`, статус
  - `StatusCalculator` — фоновый воркер раз в 30с проставляет `offline` если `last_seen_at > 2 мин`
- [ ] **3.6** Task service расширения:
  - `Claim(ctx, taskID, agentID)` — транзакция: проверить `task_locks`, вставить, обновить `tasks.assignee_type=agent, assignee_id=agentID, status=in_progress, claimed_at=NOW`
  - `Release(ctx, taskID)` — удалить lock, вернуть `assignee_type=null`, status=`todo`
  - `Submit(ctx, taskID, agentID)` — `status=review`, `awaiting=human`
  - `Review(ctx, taskID, userID, decision, comment?)` — `decision=approve → status=done`, `decision=reject → status=in_progress, awaiting=agent` + создать comment
- [ ] **3.7** Comment service:
  - Парсинг markdown, извлечение `@user` и `@agent` упоминаний
  - Запись в `mentions`
  - Триггер нотификации (через notifier)
- [ ] **3.8** Attachment service:
  - Multipart upload, сохранение в `data/uploads/YYYY/MM/{uuid}-{sanitized_filename}`
  - Whitelist mime: image/*, application/pdf, text/*, .md, .json
  - Max 50 MB
- [ ] **3.9** Activity service:
  - Все мутации записывают в `task_activity`
  - Endpoint `GET /api/v1/tasks/:id/activity`
- [ ] **3.10** Long-poll endpoint:
  - `POST /api/v1/events/await {timeout_s, since, filter}` → ждёт до timeout или события, возвращает массив
  - Использует `sync.Cond` + подписку на hub
- [ ] **3.11** Handlers:
  - `GET/POST /api/v1/agents`, `DELETE /api/v1/agents/:id`
  - `POST /api/v1/agents/:id/heartbeat`
  - `POST /api/v1/tasks/:id/claim`, `/release`, `/submit`, `/review`
  - `GET/POST /api/v1/tasks/:id/comments`
  - `POST/GET /api/v1/tasks/:id/attachments`
  - `GET /api/v1/tasks/:id/activity`
  - `GET /api/v1/tasks/:id/context` — полный снимок для агента
  - `POST /api/v1/events/await`
- [ ] **3.12** Notifier (заглушка для Phase 6):
  - Интерфейс `Notifier { Notify(ctx, event) }`
  - Простая реализация: запись в `notifications` таблицу, отправка WS-эвента владельцу
- [ ] **3.13** Frontend `web/src/features/agents/`:
  - `/agents` — список агентов, статус online/offline, кнопка создать
  - Кнопка «Copy token» (только при создании)
  - Страница агента: история активности, текущие задачи
- [ ] **3.14** Frontend `web/src/features/tasks/{TaskView}`:
  - Layout: sidebar (поля) + center (timeline) + right (attachments, subtasks, checklist)
  - Composer внизу: markdown editor, drag-drop файлов, autocomplete `@mentions`
  - Бейдж `awaiting: human | agent`
- [ ] **3.15** Frontend review actions:
  - На задаче в `review`: кнопки «Approve», «Reject» (открывает форму для комментария)
- [ ] **3.16** Тесты:
  - Concurrent claim — только один агент получает задачу
  - Heartbeat TTL → offline
  - Упоминания парсятся и попадают в `mentions`
  - Long-poll возвращает события

### Definition of Done

- Создание агента в UI, копирование токена.
- Агент через curl делает claim → submit → владелец approve.
- Владелец reject → задача возвращается агенту с комментарием.
- Упоминания работают.
- Файлы прикрепляются, скачиваются.
- Activity stream виден на странице задачи.
- Long-poll для агента работает (тест через `curl --max-time 35`).

---

## Phase 4 — Календарь + Тайм-трекинг *(1 неделя)*

> **Аудит 2026-08-12:** ✅ — recurrence (DAILY/WEEKLY/MONTHLY), single-active timer, ручной ввод, `/reports/time`, floating timer widget — есть с тестами. Оговорка: календарь не подгружает задачи с `due_at` (только timed-события, которые после Phase 11 живут в `tasks`).

**Цель:** события и задачи на едином календаре, таймер + ручной ввод time.

### Tasks

- [ ] **4.1** Миграция `006_calendar_time.sql`:
  - `events(id, title, description, start_at, end_at, all_day, color, project_id, recurrence_rule, parent_event_id)`
  - `time_entries(id, task_id, agent_id, started_at, ended_at, duration_s, source)`
- [ ] **4.2** Domain + Repositories
- [ ] **4.3** Event service:
  - `Create`, `Update`, `Delete`
  - `ExpandRecurrence(view_start, view_end)` — генерирует виртуальные копии для recurring events
- [ ] **4.4** Time entry service:
  - `Start(ctx, taskID, agentID)` — активный таймер (один на агента)
  - `Stop(ctx)` — рассчитать duration
  - `ListByTask`, `ListByAgent`, `ListByDay`
- [ ] **4.5** Handlers:
  - CRUD `/api/v1/events`
  - `GET /api/v1/events?from=&to=` — диапазон
  - `POST /api/v1/tasks/:id/timer/start`, `/stop`
  - `POST /api/v1/tasks/:id/time` — ручной ввод
  - `GET /api/v1/reports/time?from=&to=&group_by=`
- [ ] **4.6** Frontend `web/src/features/calendar/`:
  - `react-big-calendar` с day/week/month/agenda
  - Drag события для изменения времени
  - События и задачи с `due_at` на одном календаре (разные цвета)
- [ ] **4.7** Frontend timer UI:
  - Кнопка «▶ Start» на задаче
  - Глобальный floating timer widget (sticky)
  - Отчёт за день/неделю (bar chart)
- [ ] **4.8** Тесты:
  - Recurrence expansion корректна
  - Только один активный таймер на агента

### Definition of Done

- Создание события в UI, видно в календаре.
- Задачи с `due_at` отображаются.
- Таймер запускается/останавливается, время записывается.
- Отчёт за неделю виден.

---

## Phase 5 — База знаний + поиск *(1–2 недели)*

> **Аудит 2026-08-12:** ✅ — wiki (`[[slug]]`-парсинг, backlinks, tree), FTS5 BM25 с unicode61/diacritics, кириллица, snippet-подсветка, Cmd+K — есть с тестами. Оговорка: нет `[[` autocomplete в редакторе.

**Цель:** wiki с markdown, wiki-links, FTS5.

### Tasks

- [ ] **5.1** Миграция `007_wiki.sql`:
  - `wiki_pages(id, parent_id, slug UNIQUE, title, content_md, position, created_at, updated_at)`
  - `wiki_links(from_page_id, to_page_id)` — извлечённые из content_md
  - FTS5 виртуальные таблицы: `pages_fts`, `tasks_fts`, `comments_fts` с триггерами
- [ ] **5.2** Wiki service:
  - `Save(ctx, page)` — парсинг `[[slug]]` из content_md, обновление `wiki_links`
  - `Backlinks(ctx, slug)` — кто ссылается
  - `Tree(ctx)` — иерархия
- [ ] **5.3** Search service:
  - `Search(ctx, q, type, limit)` — FTS5 с BM25, ranking
  - Snippet generation (highlight в выдаче)
- [ ] **5.4** Handlers:
  - CRUD `/api/v1/pages`
  - `GET /api/v1/pages/:slug/backlinks`
  - `GET /api/v1/search?q=&type=`
- [ ] **5.5** Frontend `web/src/features/wiki/`:
  - Tiptap editor с markdown-расширением, code-block (lowlight), mention
  - Sidebar с деревом страниц
  - Автокомплит `[[` для ссылок
  - Backlinks panel внизу страницы
  - Поиск в шапке (Cmd+K)
- [ ] **5.6** Frontend search results:
  - Группировка: tasks / pages / comments
  - Подсветка совпадений
- [ ] **5.7** Тесты:
  - Парсинг wiki-links корректен (включая несуществующие)
  - FTS5 возвращает релевантные результаты
  - Поиск работает с кириллицей (tokenizer `unicode61` + `remove_diacritics 2`)

### Definition of Done

- Создание страницы в UI, редактирование markdown.
- `[[Page Name]]` авто-превращается в ссылку.
- Backlinks видны.
- Cmd+K → поиск по всему.

---

## Phase 6 — Уведомления (фасад над bot) *(3–4 дня)*

> **Аудит 2026-08-12:** 🟡 — notifier с dedup/retry/backoff, bell UI с бейджем, тесты — есть. Не эмитятся `task.commented`, `agent.offline`, `backup.failed` (только шаблоны); файла `settings/Notifications.tsx` нет (подписки живут в `Bots.tsx` по `/settings/bots`); в интерфейсе `Bot` нет `FormatMessage`.

**Цель:** реальный notifier-фасад, in-app через WS, дедупликация.

### Tasks

- [ ] **6.1** Миграция `008_notifications.sql`:
  - `notifications(id, user_id, type, target_type, target_id, payload_json, read_at, dedup_key UNIQUE, created_at)`
  - `bot_subscriptions(id, user_id, bot_type, target_address, events_json, enabled)`
- [ ] **6.2** Интерфейс `internal/bot/bot.go`:
  ```go
  type Bot interface {
      Name() string
      Start(ctx) error
      Stop(ctx) error
      Send(ctx, target, msg) error
      FormatMessage(msg) (any, error)
  }
  ```
- [ ] **6.3** Console bot (заглушка) — пишет в stdout/log
- [ ] **6.4** Notifier service:
  - Подписки из `bot_subscriptions`
  - Рендер шаблона под `bot_type`
  - Дедупликация через `dedup_key`
  - Retry при ошибке (3 попытки с экспоненциальным backoff)
- [ ] **6.5** Регистрация событий из других сервисов:
  - task.assigned_to_me, task.review_needed, task.commented, mention.created, event.upcoming_1h, agent.offline, backup.failed
- [ ] **6.6** Frontend `web/src/features/notifications/`:
  - Колокольчик в шапке с счётчиком
  - Список уведомлений
  - Mark as read
- [ ] **6.7** Frontend `web/src/features/settings/Notifications.tsx`:
  - UI подписок (каналы × события)
- [ ] **6.8** Тесты:
  - Дедупликация работает
  - Retry восстанавливает после transient failures

### Definition of Done

- Создание задачи агентом → in-app нотификация владельцу.
- Console bot пишет события в лог.
- UI подписок позволяет включить/выключить каналы.

---

## Phase 7 — Бэкапы *(3–4 дня)*

> **Аудит 2026-08-12:** 🟡 — VACUUM INTO snapshot + ротация, git push, scheduler, CLI (push/snapshot/status/restore), UI — есть. Mirror не пишет комментарии (`nil` в `MirrorSave`); git client без `Status`/`TestConnection`; snapshot по тикеру 24h, не cron 03:00; `PUT /backups/settings` → 501 («config.yaml is the source of truth»); UI настроек read-only. **✅ Wave 4 PR 2 — Mirror now fetches comments; down-миграции закрыли бóльшую часть. PWA outbox update/move/comment зашиты в call sites; InboxPage теперь использует TaskCard.**

**Цель:** markdown-зеркало + git push + sqlite snapshot + UI.

### Tasks

- [ ] **7.1** Миграция `009_backups.sql`:
  - `backup_settings(id, key, value_json)` — remote URL, auth type, schedule
  - `backup_log(id, type, status, message, snapshot_path, created_at)`
- [ ] **7.2** Mirror service:
  - При каждом save задачи/страницы/комментария пишет markdown в `data/mirror/{kind}/{id}.md`
  - Frontmatter совместим с Obsidian (id, type, status, tags, updated)
- [ ] **7.3** Git client (через `os/exec` → `git` бинарь):
  - `Init(dir)` если нет
  - `Commit(dir, message)`
  - `Push(dir)` — fast-forward only, иначе алерт
  - `Status(dir)` — last commit, uncommitted changes
  - `TestConnection(url, auth)` — пуш test-ветки
- [ ] **7.4** SQLite backup:
  - `internal/backup/sqlite.go` — `sqlite3 .backup` (online backup API в modernc)
  - Ротация: удалять старше N дней
- [ ] **7.5** Scheduler:
  - Tick 5 мин: git commit + push
  - Tick 1 день 03:00: sqlite snapshot
  - Tick 15 мин: WAL archive (Phase 7.5)
- [ ] **7.6** CLI:
  - `orenda backup push`
  - `orenda backup snapshot`
  - `orenda backup status`
  - `orenda backup restore --from <path>`
- [ ] **7.7** Handlers:
  - `GET /api/v1/backups/settings`
  - `PUT /api/v1/backups/settings`
  - `POST /api/v1/backups/test`
  - `GET /api/v1/backups/snapshots`
  - `POST /api/v1/backups/restore`
- [ ] **7.8** Frontend `web/src/features/settings/Backups.tsx`:
  - Форма настройки remote (тип, URL, auth)
  - Кнопка «Test connection»
  - Список снапшотов, кнопка «Restore»
  - Лог последних бэкапов
- [ ] **7.9** Тесты:
  - Mirror создаёт правильный markdown
  - Snapshot работает без блокировки
  - Push в локальный bare-репо

### Definition of Done

- Настройка remote в UI, test connection успешен.
- Изменение задачи → через 5 мин git commit появляется.
- Ежедневный snapshot создаётся.
- Restore из snapshot работает.

---

## Phase 8 — PWA + Offline *(1 неделя)*

> **Аудит 2026-08-12:** 🟡 — vite-plugin-pwa, Workbox SW, IndexedDB outbox+cache, `POST /api/v1/sync` с идемпотентностью (`sync_ops`) — есть. LWW декларирован, но `updated_at` реально не сравнивается; outbox подключён только к create task (update/move/comment висят мёртвым кодом); Background Sync API не используется.

**Цель:** работа в оффлайне, синхронизация при онлайне.

### Tasks

- [ ] **8.1** `vite-plugin-pwa` настройка
- [ ] **8.2** Service worker:
  - Precache app shell
  - Network-first для `/api/`, cache-fallback
  - Background sync для outbox
- [ ] **8.3** IndexedDB через `idb`:
  - `outbox` store — очередь мутаций
  - `cache` store — последние ответы GET
- [ ] **8.4** Outbox manager:
  - При мутации оффлайн → в outbox
  - При онлайне → flush через `POST /api/v1/sync` (batch)
- [ ] **8.5** Endpoint `POST /api/v1/sync`:
  - Принимает массив операций `{op, target, payload, client_id, created_at}`
  - Возвращает per-op результат
- [ ] **8.6** Конфликт-резолв: last-write-wins по `updated_at`
- [ ] **8.7** Тесты:
  - Оффлайн → онлайн → данные синхронизированы
  - Конфликт корректно разрешается

### Definition of Done

- Отключить сеть → UI работает (read).
- Создать задачу оффлайн → при онлайне она появляется.
- Установить PWA на десктоп.

---

## Phase 9 — Полировка *(ongoing)*

> **Аудит 2026-08-12:** 🟡 — бенчмарки, security headers, rate limit (429+Retry-After), zap+lumberjack, install.sh/systemd/uninstall, dark mode — есть. Нет `docs/ARCHITECTURE.md`, pprof endpoint, Prometheus metrics, govulncheck; README без скриншотов.

### Tasks

- [ ] **9.1** Покрытие тестами:
  - Unit: 70%+ для domain и service
  - Integration: ключевые user stories через httptest
  - E2E: Playwright (login, create task, move, comment)
- [ ] **9.2** Документация:
  - Обновить `README.md` со скриншотами и сценариями
  - `docs/API.md` — REST reference (генерируется из OpenAPI)
  - `docs/DB.md` — схема БД с диаграммой
  - `docs/ARCHITECTURE.md` — детали реализации
- [ ] **9.3** Производительность:
  - Benchmark'и для горячих путей
  - Профилирование через pprof endpoint (только для debug)
- [ ] **9.4** Логи и метрики:
  - Структурные логи (zap)
  - Опционально: Prometheus metrics endpoint
- [ ] **9.5** Установка:
  - `scripts/install.sh` — билд + копирование в `~/.local/bin`
  - `scripts/systemd/orenda.service` — user unit
  - `scripts/uninstall.sh`
- [ ] **9.6** Безопасность:
  - Security headers middleware
  - Rate limiting (token bucket)
  - Аудит зависимостей (`govulncheck`)
- [ ] **9.7** UX polish:
  - Keyboard shortcuts
  - Drag-and-drop файлов в задачу
  - Toast уведомления
  - Dark mode
  - Empty states
  - Loading skeletons

---

## Phase 10 — Бот-платформа *(1 неделя)*

> **Аудит 2026-08-12:** 🟡 — registry, config-driven запуск, Console/Telegram/VK/Email/Webhook боты, callback handler с replay protection, тесты — есть. Email без HTML-шаблонов (plain text); VK только Callback API (Long Poll не реализован); нет «Test send» в UI; нет weekly digest (DoD); `Bot.Stop()` не вызывается при shutdown.

**Цель:** реальные боты по интерфейсу Bot.

### Tasks

- [ ] **10.1** Bot registry:
  - `internal/bot/registry.go` — мапа `name → factory`
  - `New(name, config)` → Bot
- [ ] **10.2** Config-driven боты:
  - Чтение `bots:` секции из config.yaml
  - Init/Stop при старте/остановке сервера
- [ ] **10.3** Console bot (стабильный)
- [ ] **10.4** VK Community Bot:
  - Callback API для входящих событий
  - Long Poll для альтернативы
  - `Send` через messages.send
  - Интерактивные кнопки через `keyboard`
- [ ] **10.5** Telegram Bot:
  - Long polling через `telegram-bot-api`
  - `Send` через SendMessage с `InlineKeyboardMarkup`
  - Обработка callback_query
- [ ] **10.6** Email bot:
  - SMTP через `gomail` или `net/smtp`
  - HTML-шаблоны
  - Кнопки-ссылки для действий
- [ ] **10.7** Webhook bot:
  - POST JSON на URL
  - HMAC подпись payload'а
- [ ] **10.8** Шаблоны сообщений:
  - `internal/bot/templates/*.go` — конструкторы Message
  - Рендер для каждого бота
- [ ] **10.9** Callback handler:
  - `internal/bot/callback.go` — верификация, маппинг в API-вызовы
  - Защита от replay (timestamp + nonce)
- [ ] **10.10** Frontend `web/src/features/settings/Bots.tsx`:
  - Список подключённых ботов, статус
  - Кнопка «Test send»
  - Форма добавления
- [ ] **10.11** Тесты:
  - Каждый бот изолированно (mock HTTP server для VK/TG)
  - Callback → API-call маршрутизация

### Definition of Done

- Подключение VK Community Bot в UI, отправка тестового сообщения.
- Получение уведомления о задаче в VK.
- Кнопка «Approve» в VK переводит задачу в done.
- Email bot отправляет дайджест за неделю.

---

## Phase 12 — Кастомные колонки канбана *(2–3 дня)*

> **Аудит 2026-08-12:** ✅ — всё, включая опциональное удаление пустой колонки (12.6): create 201/400/404, position max+1024, WS `column.created`/`column.deleted`, dnd reorder, double-click rename, тесты.

**Цель:** пользователь управляет колонками доски проекта: добавляет свои, меняет порядок drag-and-drop, переименовывает. Сейчас колонки фиксированы: 5 дефолтных (`backlog … done`) сидируются при создании проекта, создания через API нет (тесты вставляют колонки напрямую в SQL), reorder-UX нет.

> Phase 11 (project tabs: Kanban/Activity/Attachments/Settings) выполнена вне плана — см. коммиты `phase(11.*)`; нумерация продолжается с 12.

**Контекст (что уже есть):**

- `PATCH /api/v1/columns/{id}` — name, position, wip_limit, color (`patchColumnHandler`, `handlers_kanban.go`).
- UI переименования уже существует: кнопка ⚙ в заголовке колонки → `EditColumnModal` (`ColumnView.tsx`). Задача фазы — сделать его обнаружимым (double-click по заголовку).
- `columns.position REAL` — fractional positions, как у задач; reorder не требует новых полей и миграций.
- `tasks.status` от колонки не зависит (меняется только через claim/submit/review), поэтому кастомные колонки не ломают workflow агентов.

### Tasks

- [ ] **12.1** Backend: создание колонки
  - Repo: `project.Repository.CreateColumn(ctx, col)`
  - Handler `createColumnHandler` в `handlers_kanban.go`: `POST /api/v1/projects/{id}/columns {name, color?, wip_limit?}` → 201
  - `position = max(position) + 1024` (в конец доски); пустое `name` → 400; несуществующий проект → 404
  - Route в `router.go` рядом с существующим `PATCH /columns/{id}`
  - WS-событие в topic `tasks` (`column.created`), чтобы вторая вкладка обновилась
- [ ] **12.2** Frontend: «+ Add column»
  - Кнопка в конце доски (`KanbanBoard.tsx`) → inline-форма (name, color) → POST → optimistic append
  - `api.createColumn()` в `client.ts`
- [ ] **12.3** Frontend: drag-and-drop reorder колонок
  - `@dnd-kit` `SortableContext` (horizontal) для колонок в `KanbanBoard.tsx`
  - On drop: `api.updateColumn(id, {position})` — midpoint между соседями, optimistic + revert при ошибке (тот же паттерн, что у task dnd)
- [ ] **12.4** Frontend: discoverability переименования
  - Double-click по заголовку колонки открывает существующий `EditColumnModal`; кнопка ⚙ остаётся
- [ ] **12.5** Тесты:
  - Backend: create → 201, колонка появляется в `GET /board`; пустое name → 400; PATCH position сохраняет порядок после повторного GET
  - Frontend: форма add-column вызывает API и рендерит новую колонку
- [ ] **12.6** *(опционально)* Удаление колонки: `DELETE /api/v1/columns/{id}` — только пустую, иначе 422; кнопка в `EditColumnModal`

### Definition of Done

- Колонка создаётся из UI и видна без перезагрузки (и во второй вкладке по WS).
- Колонки перетаскиваются, порядок сохраняется после refresh.
- Переименование доступно по double-click и через ⚙, сохраняется.
- `curl -X POST /api/v1/projects/:id/columns -d '{"name":"QA"}'` → 201.
- `make test && make lint` зелёные.

---

## Phase 13 — Теги и цветовые метки задач *(3–4 дня)*

> **Аудит 2026-08-12 (обновлено):** ✅ почти — теги входят в list-payload (27.3: `Task.Tags []Tag`, +1 batch-запрос `TagsForTasks`, без N+1; E2E `kanban.spec.ts` проверяет цветные чипы после reload). Остаются: нет WS-событий при смене тегов/цвета; фильтр по тегу (13.6, опц.) не сделан. Миграция `tasks.color` живёт в `012_events_to_tasks.sql`.

**Цель:** задачи помечаются тегами (с цветом) и цветовой меткой; теги видны чипами на канбан-карточках и на странице задачи. Сейчас тегов нет вообще: таблицы есть, а весь слой поверх — нет.

**Контекст (что уже есть):**

- Таблицы `tags(id, name UNIQUE, color)` и `task_tags(task_id, tag_id)` существуют с `001_init.sql` — миграция для тегов **не нужна**.
- Всё остальное отсутствует: в `task_repo.go` нет ни одного метода по тегам, handlers/routes нет, в `web/src` ноль упоминаний тегов.
- Цветовой метки задачи в схеме нет → миграция `013_task_color.sql`: `ALTER TABLE tasks ADD COLUMN color TEXT` (аддитивно, правило «миграции аддитивны»).
- Mirror frontmatter уже декларирует поле `tags` (`internal/mirror/mirror.go`, PLAN#7.2) — подключить реальные теги.
- Offline: sync-операции `task.create`/`task.update` (`handlers_sync.go`) — расширить payload тегами и цветом, иначе оффлайн-редактирование их потеряет.

### Tasks

- [ ] **13.1** Миграция `013_task_color.sql` — `tasks.color TEXT NULL` (+ `.down.sql`)
- [ ] **13.2** Domain + repo:
  - `task.Tag{ID, Name, Color}`; поле `Color` в `task.Task`
  - Методы: `ListTags(ctx)`, `CreateTag(ctx, name, color)`, `SetTaskTags(ctx, taskID, tagIDs)` (replace в транзакции), `TagsForTasks(ctx, taskIDs)` — batch без N+1 для канбана
- [ ] **13.3** Handlers + routes:
  - `GET/POST /api/v1/tags`, `PATCH/DELETE /api/v1/tags/{id}`
  - `PUT /api/v1/tasks/{id}/tags {tag_ids}` — replace-семантика
  - `color` принимается в PATCH задачи
  - `GET /board` и `GET /tasks` включают `tags` + `color` в ответ
  - WS-событие в topic `tasks` при смене тегов/цвета
- [ ] **13.4** Sync: теги и color в payload ops `task.create`/`task.update`
- [ ] **13.5** Frontend:
  - `TaskCard.tsx` — цветные чипы тегов + полоска цветовой метки
  - Страница задачи — редактор тегов (multi-select с созданием нового тега и выбором цвета) и выбор цветовой метки
  - `client.ts` — тип `Tag`, методы `listTags/createTag/setTaskTags`
- [ ] **13.6** *(опционально)* Фильтр канбана по тегу (chips-переключатели над доской)
- [ ] **13.7** Mirror: реальные теги в frontmatter задачи
- [ ] **13.8** Тесты:
  - Repo: set/get roundtrip, replace-семантика `SetTaskTags`
  - API: `PUT /tasks/:id/tags` → 200/404, `PATCH` color → 200
  - Frontend: чип тега рендерится на карточке

### Definition of Done

- Тег создаётся, назначается задаче, виден чипом на канбане и на странице задачи, переживает refresh.
- Цветовая метка ставится на задачу, видна полосой на карточке.
- Теги попадают в mirror frontmatter и не теряются при оффлайн-редактировании (sync).
- `make test && make lint` зелёные.

---

## Phase 14 — Разделение subtasks/checklists по смыслу (Weeek-style) *(3–5 дней)*

> **Аудит 2026-08-12:** ✅ — миграция 013, ListChildren/ChildProgress, `/tasks/{id}/children`, activity, mirror checklists, TaskContext с Children+Checklists, ChildTasksList с прогресс-баром, тесты — есть. Оговорки: нет валидации `parent_task_id` (существование родителя / совпадение проекта); не эмитится `task.child_status_changed`.

**Цель:** устранить дублирование двух параллельных «чекбокс под задачей» моделей. Сейчас и `subtasks`, и `checklists` существуют как два разных API с одинаковым UI-смыслом — пользователь не понимает, куда писать. Делаем чёткое разделение по модели Weeek/Asana:

- **Subtasks → Child tasks** (полноценные задачи через существующее `tasks.parent_task_id`): свой статус, assignee, due date, могут быть заclaim'аты агентом, считаются в прогрессе родителя. Появляются на канбан-доске как обычные карточки или вложенным списком.
- **Checklists** остаются локальными чекбоксами под задачей (как сейчас), но получают то, чего им не хватало: activity log и mirror в git.

**Контекст (что уже есть):**

- `tasks.parent_task_id TEXT REFERENCES tasks(id) ON DELETE CASCADE` уже в схеме (`001_init.sql:102`, индекс `idx_tasks_parent`); пробрасывается через `taskInput.ParentTaskID` в handlers (`handlers_tasks.go:23,68,167-168`); поддержан в `task_repo.go:35-143` и `task_repo.go:461-591`. **Но никто из UI/агентов это поле не заполняет.**
- Таблица `subtasks` (`001_init.sql:141`) дублирует `checklist_items` по семантике.
- `task.Subtask` тип определён в `internal/domain/task/model.go:153-159`; `AddSubtask/ListSubtasks/UpdateSubtask/DeleteSubtask/GetSubtask` — в `task_repo.go:188-281`; HTTP `/api/v1/tasks/{id}/subtasks[/{subId}]` — в `handlers_tasks.go:220-258`, route в `router.go:227-229`.
- Subtasks пишутся в markdown mirror (`mirror.go:51-87`) и в activity log (`task.subtask_added`/`task.subtask_done`), checklists — нет (баг, чиним заодно).
- `getTaskContextHandler` (`handlers_phase3.go:372-395`) возвращает агенту только `subtasks` — `checklists` агент не видит. Чиним.

### Tasks

- [ ] **14.1** Миграция `013_subtasks_to_children.sql` + `.down.sql`:
  - Перелить каждую строку `subtasks` в `tasks` с новым UUIDv7, `parent_task_id = subtasks.task_id`, `status = done|todo`, `column_id = NULL` (не показываем на канбан-доске родителя, чтобы не загромождать; показываем вложенно в `TaskViewBody`).
  - Поле `project_id` берём из родительской задачи (`SELECT project_id FROM tasks WHERE id = subtasks.task_id`).
  - В новых child tasks `assignee_type/assignee_id/due_at/started_at/claimed_at/completed_at/time_estimate_s/time_spent_s/context_md/agent_notes/awaiting` = дефолты; `priority='medium'`.
  - Дропнуть таблицу `subtasks` и индекс `idx_subtasks_task`.
- [ ] **14.2** Domain (`internal/domain/task/`):
  - Удалить `task.Subtask` тип.
  - В `Repository`:
    - Убрать `AddSubtask/ListSubtasks/UpdateSubtask/DeleteSubtask/GetSubtask`.
    - Добавить `ListChildren(ctx, parentID) ([]*Task, error)` — возвращает задачи где `parent_task_id = parentID`, отсортированные по `position, created_at`.
    - Добавить `ChildProgress(ctx, parentID) (total, done int, err error)` — для прогресс-бара родителя.
  - В `task.Filter`: добавить `ParentTaskID` (пустое = не фильтровать, конкретное = только direct children; можно расширить до `*string` для top-level задач).
- [ ] **14.3** SQLite repo (`internal/storage/sqlite/task_repo.go`):
  - Удалить реализацию методов `AddSubtask/ListSubtasks/UpdateSubtask/DeleteSubtask/GetSubtask` (~95 строк).
  - Реализовать `ListChildren`: `SELECT * FROM tasks WHERE parent_task_id = ? ORDER BY position, created_at`.
  - Реализовать `ChildProgress`: `SELECT COUNT(*), SUM(CASE WHEN status='done' THEN 1 ELSE 0 END) FROM tasks WHERE parent_task_id = ?`.
  - В `ListByProject`: добавить поддержку `Filter.ParentTaskID` (по умолчанию `nil` — фильтр не применяется; явное `""` — только top-level, где `parent_task_id IS NULL`).
  - Сделать `Delete` каскадным уже (через FK `parent_task_id` ON DELETE CASCADE).
- [ ] **14.4** Handlers (`internal/api/handlers_tasks.go`):
  - Удалить `listSubtasksHandler/addSubtaskHandler/updateSubtaskHandler/deleteSubtaskHandler`.
  - Добавить `listChildTasksHandler`: `GET /api/v1/tasks/{id}/children` → `TaskContext`-style ответ `{tasks: [...], progress: {total, done}}`.
  - В `createTaskHandler` уже принимается `parent_task_id` — добавить валидацию (родитель должен существовать; `project_id` родителя и ребёнка должны совпадать).
- [ ] **14.5** Routers (`internal/api/router.go`):
  - Убрать маршруты `/tasks/{id}/subtasks[/{subId}]` (4 маршрута).
  - Добавить `GET /tasks/{id}/children`.
- [ ] **14.6** Activity log (`internal/service/activity/` и `handlers_tasks.go` создание):
  - При создании child task записать `task.child_added` с payload `{child_id, title}`.
  - При изменении статуса child (PATCH /tasks/{id} где parent непустой) → `task.child_status_changed` с payload `{child_id, old, new}`.
  - При создании checklist → `task.checklist_added`. При добавлении item → `task.checklist_item_added`. При toggle done → `task.checklist_item_done`. При удалении → не пишем (это шум).
  - Обновить маппинг в `TaskViewBody.tsx::ActivityLog` (добавить новые глаголы).
- [ ] **14.7** Mirror (`internal/mirror/mirror.go`):
  - В `WriteTask` кроме subtasks писать checklists: YAML-секция `checklists:` с `{title, items: [{title, done}]}`.
  - Параметр `checklists` прокинуть в сигнатуру `WriteTask` или сделать отдельный метод `WriteChecklists`. Решить в процессе.
- [ ] **14.8** `task_context` для агента (`handlers_phase3.go`):
  - Расширить `TaskContext`: добавить `Children []*task.Task` и `Checklists []*ChecklistRow` + `ChecklistItems map[string][]*ChecklistItemRow`.
  - Заменить `out.Subtasks` на эти поля.
  - Удалить `Subtasks` из `TaskContext` (маппинг в `service_interfaces.go` тоже).
- [ ] **14.9** Frontend — заменить SubtasksList:
  - `web/src/features/tasks/SubtasksList.tsx` → `ChildTasksList.tsx`:
    - Рендерит карточки с `title`, `status` (бейдж), `assignee` (аватар/ник).
    - Кнопки: «+ Add child task» (открывает мини-форму с title, status, assignee), «Open» (ведёт на `/tasks/:childId`), «Delete» (с подтверждением).
    - Прогресс-бар сверху: `done / total`.
    - Клик по строке открывает задачу; нет inline-edit (как у настоящих задач).
  - `web/src/features/tasks/TaskViewBody.tsx` — заменить `<SubtasksList>` на `<ChildTasksList>`. Передавать `taskId`, `parentTaskID`, `projectID` для create.
- [ ] **14.10** Frontend — ChecklistsList без изменений по сути, но:
  - Опционально: отображать checklist item с drag-and-drop reorder (low priority).
- [ ] **14.11** Frontend — API client (`web/src/shared/api/client.ts`):
  - Удалить `Subtask` тип, `listSubtasks/addSubtask/updateSubtask/deleteSubtask` методы.
  - Добавить `ChildTaskProgress { total: number, done: number }`.
  - Добавить `listChildTasks(taskId): Promise<{tasks, progress}>`, `createChildTask(parentId, {title, ...})`, `updateChildTaskStatus(id, status)`, `deleteChildTask(id)`.
- [ ] **14.12** Обновить существующие тесты:
  - `internal/storage/sqlite/task_repo_test.go::TestTaskRepo_Subtasks` — удалить, заменить на `TestTaskRepo_Children`.
  - `internal/api/scope_integration_test.go` — строки 142-155 (`POST /subtasks`, `GET /subtasks`) → `POST /tasks` с `parent_task_id`, `GET /tasks/{id}/children`.
  - Пройтись по всем тестам и убрать упоминания `Subtask`.
- [ ] **14.13** Новые тесты:
  - Миграция: seed `subtasks` строки → миграция → проверить что они стали `tasks` с правильным `parent_task_id`.
  - Repo: `ListChildren` возвращает прямых детей; `ChildProgress` корректно считает `done`; `Delete` родителя каскадирует на детей.
  - API: `GET /tasks/:id/children` → 200 с tasks+progress; `POST /tasks` с `parent_task_id` другого проекта → 400/422.
  - Frontend: `ChildTasksList` рендерит карточки с бейджем статуса.
- [ ] **14.14** Обновить `docs/API.md` — убрать `/subtasks`, добавить `/children`.
- [ ] **14.15** Обновить `docs/SESSION.md` (новые ключевые решения: subtasks→child tasks; checklists с activity+mirror).

### Definition of Done

- В UI на странице задачи есть два **визуально разных** блока:
  - «Подзадачи» — карточки с status/assignee, кликабельные (открывают свою задачу), есть прогресс-бар.
  - «Чек-лист» — простые чекбоксы, сгруппированные по именованным спискам.
- Агент через `GET /api/v1/tasks/:id/context` получает и `children`, и `checklists` (не только одно из двух).
- Существующая БД с `subtasks` мигрируется без потерь: после `orenda migrate up` все subtasks становятся child-tasks, фронт их видит без ручного вмешательства.
- `make test && make lint` зелёные.
- `docs/API.md` отражает новое API.

### Что НЕ делаем в этой фазе

- Не делаем drag-and-drop reorder для child tasks (можно в Phase 15+).
- Не показываем child tasks отдельной колонкой на канбан-доске (можно в Phase 15+).
- Не добавляем subtask-templates / checklist-templates.

---

## Phase 15 — Зависимости задач и видимость занятости для агентов *(3–4 дня)*

> **Аудит 2026-08-12:** 🟡 — миграция 016, DFS-циклы, claim заблокированной → 422 с `unfinished_blockers`, `GET /agent/tasks?ready=true`, WS `task.deps_changed`, UI-бейджи и редактор — есть. 409 `lock_taken` без holder-полей (`taskLockRepo.Holder` написан, не используется); agent context без `blocked_by`/держателя лока; `ready=true` включает задачи, занятые самим агентом.

**Цель:** задачи могут блокировать друг друга; агенты видят, какие задачи готовы к параллельной работе, а какие заблокированы или уже выполняются другим агентом. Сейчас зависимостей нет вовсе, а занятость задачи агент узнаёт только попыткой claim → голый 409.

**Контекст (что уже есть):**

- `task_locks PK(task_id)` — один держатель; повторный claim → 409 `{"error":"lock_taken"}` (`handlers_agent.go`), но БЕЗ указания, кто держит и с каких пор.
- В agent-namespace `/api/v1/agent/*` нет GET-эндпоинтов: агент не может ни получить список задач, ни найти свободные — только claim/release/submit/context по известному id (`router.go:346-353`).
- В `tasks` уже есть `assignee_type/assignee_id/claimed_at` — данные для сигнала «занято» хранятся, но не отдаются агентам.
- Таблицы зависимостей нет → миграция `014_task_dependencies.sql` (аддитивна).
- «Готовность» к параллельной работе — производная: все блокеры done И лок свободен И сама задача не done.

### Tasks

- [ ] **15.1** Миграция `014_task_dependencies.sql`:
  - `task_dependencies(task_id, depends_on_task_id, PRIMARY KEY(task_id, depends_on_task_id), FK→tasks ON DELETE CASCADE)`
  - Индекс по `depends_on_task_id` (обратный lookup: «что разблокирует эта задача»)
- [ ] **15.2** Domain + repo:
  - `AddDependency(ctx, taskID, dependsOnID)` — с проверкой цикла (DFS по графу) и отказом на self-dependency
  - `RemoveDependency`, `Blockers(ctx, taskID)` (незавершённые зависимости), `Dependents(ctx, taskID)`
- [ ] **15.3** Service:
  - `Claim` отказывает при незавершённых блокерах → 422 `task_blocked` + список id блокеров в теле (иначе зависимости декоративны)
  - 409 `lock_taken` расширить: `holder_agent_id`, `holder_agent_name`, `claimed_at`
- [ ] **15.4** Handlers:
  - `GET /api/v1/agent/tasks` — список задач для агента: `status`, `assignee`, `claimed_at`, `blocked_by[]`, вычисляемое `ready`; query `?ready=true` — только готовые к параллельной работе
  - `GET /agent/tasks/{id}/context` — добавить `blocked_by[]` и текущего держателя лока
  - User-side: `PUT /api/v1/tasks/{id}/dependencies {depends_on_ids}` (replace), блокеры в ответе `GET /board` и `GET /tasks`
  - WS-событие в topic `tasks` при изменении зависимостей
- [ ] **15.5** Frontend:
  - `TaskCard` — бейдж «blocked» (с числом блокеров) и бейдж занятости агентом (assignee уже есть в payload)
  - Страница задачи — редактор зависимостей («Blocked by»: поиск задачи, добавить/удалить)
- [ ] **15.6** Документация: `docs/API.md` — новые поля и эндпоинты, семантика `ready`
- [ ] **15.7** Тесты:
  - Цикл в зависимостях отклоняется (A→B→A)
  - Claim заблокированной задачи → 422 со списком блокеров; после done блокера → claim успешен
  - 409 содержит holder-поля
  - `?ready=true` исключает заблокированные и занятые

### Definition of Done

- Зависимость создаётся из UI, заблокированная задача помечена на доске.
- Агент через `GET /api/v1/agent/tasks?ready=true` получает только параллельно выполнимые задачи.
- Claim заблокированной → 422 с блокерами; claim занятой → 409 с именем держателя.
- Циклы отклоняются. `make test && make lint` зелёные.

---

## Phase 16 — Inbox: карточки без проекта, а не системный проект *(2–3 дня)*

> **Аудит 2026-08-12:** ✅ — FK-off migration runner, миграция 015 (rebuild tasks, rowid, FTS rebuild, удаление `...cafe` и system-user), inbox endpoints, PATCH project_id, `/inbox` страница, статичный сайдбар-пункт с бейджем — есть. Оговорки: нет dedicated теста миграции 015; `docs/API.md` не описывает `/inbox/tasks`; InboxPage не переиспользует TaskCard. **✅ закрыт в `phase-mirror-minor` (Wave 4 PR 2) — InboxPage переиспользует TaskCard; остальные оговоренные бэклоги оставлены на Phase 9 polish.**

**Цель:** Inbox перестаёт быть системным проектом с магическим id. Inbox — это просто набор карточек (задач), у которых ещё нет проекта: `tasks.project_id IS NULL`. Системный проект `00000000-0000-0000-0000-00000000cafe` и его placeholder-пользователь удаляются миграцией.

> ⚠️ **Не путать** с «notifications inbox» из Phase 6 (`notifications` таблица, bell UI) — это другой, не связанный концепт. Эта фаза его не трогает.

**Контекст (что уже есть):**

- `tasks.project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE` (`001_init.sql:101`). Миграция 012 создала системного пользователя `00000000-0000-0000-0000-000000000001` (`system-inbox@orenda.local`, role `system`) и проект Inbox `...cafe`, чтобы у calendar-events без проекта был FK-target.
- Runtime-поддержка: `ensureInboxProject`/`ensureInboxBoardAndColumns` + константа `inboxProjectID` в `cmd/orenda/cli_helpers.go`; вызовы в `runServe` (`main.go:309`) и `openCLIDB`; `eventSvc.DefaultProjectID = "...cafe"` (`main.go:375`, fallback в `service/event/event.go::Create`).
- Frontend: `INBOX_PROJECT_ID` в `web/src/shared/constants.ts`; сайдбар рендерит Inbox как системный проект (`ProjectSidebar.tsx`, `partitionProjects.ts` bucket `inbox`, `SidebarProjectItem` prop `isSystem`); special-casing в `ProjectDetailPage.tsx` и `tabs/ProjectSettingsTab.tsx`; `CalendarPage.tsx` дефолтит события в `...cafe`.
- `tasks.column_id` уже nullable (`ON DELETE SET NULL`) — карточка без колонки легальна.
- Конвенция optional FK в Go: строка, `""` ↔ NULL через `nullString()` (как `ParentTaskID`, `ColumnID`).

**Ключевые решения (согласовано 2026-08-11):**

- Go: `Task.ProjectID` остаётся `string`; `""` = нет проекта (inbox). JSON `project_id` эмитится всегда, `""` = inbox.
- Inbox-задача не имеет board/column: `project_id IS NULL` ⇒ `column_id IS NULL` (инвариант в `Task.Validate`).
- Удаление обычного проекта по-прежнему каскадно удаляет его задачи (семантику `ON DELETE CASCADE` не меняем).
- Drag-and-drop внутри inbox не нужен: это плоский список карточек, а не доска.

### Tasks

- [ ] **16.1** Migration runner: поддержка FK-off миграций (`internal/storage/sqlite/db.go`).
  - Зачем: rebuild `tasks` — родительской таблицы (на неё ссылаются `task_locks`, `checklists`, `task_tags`, `task_activity`, `time_entries` и сама `tasks.parent_task_id`). При `foreign_keys=ON` `ALTER TABLE ... RENAME` переписывает REFERENCES дочерних таблиц на переименованную таблицу (`legacy_alter_table` это НЕ отключает — проверено эмпирически на bundled SQLite), а `DROP TABLE` затем каскадно сносит все дочерние строки. `defer_foreign_keys` тоже не спасает: каскадные действия implicit DELETE при DROP всё равно выполняются. Официальная процедура SQLite требует `PRAGMA foreign_keys=OFF`, а этот pragma — no-op внутри транзакции, поэтому раннер должен переключать его вокруг транзакции миграции.
  - Маркер в файле миграции: строка-комментарий `-- orenda:foreign_keys_off`. При наличии маркера `applyMigration`: пингует отдельный `db.Conn` (pragma — per-connection state), `PRAGMA foreign_keys = OFF` → обычная tx с телом → `PRAGMA foreign_key_check` внутри tx (любая строка = ошибка, rollback) → запись версии → commit → вернуть `foreign_keys = ON` и закрыть conn (defer, включая пути ошибок).
- [ ] **16.2** Миграция `015_inbox_no_project.sql` (с маркером из 16.1):
  1. `DROP TRIGGER IF EXISTS` для `trg_tasks_touch`, `trg_tasks_fts_insert/update/delete`; `DROP INDEX IF EXISTS` для `idx_tasks_project`, `idx_tasks_status`, `idx_tasks_assignee`, `idx_tasks_due`, `idx_tasks_parent` (001), `idx_tasks_project_column_position`, `idx_tasks_assignee_status` (003), `idx_tasks_time` (012, partial).
  2. `ALTER TABLE tasks RENAME TO tasks_old`; `CREATE TABLE tasks` — та же схема (включая `start_at/end_at/all_day/color` из 012), но `project_id TEXT REFERENCES projects(id) ON DELETE CASCADE` **без NOT NULL**.
  3. `INSERT INTO tasks (rowid, ...) SELECT rowid, ... FROM tasks_old ORDER BY rowid` — rowid сохраняем ради FTS5 external-content (`tasks_fts`, `content_rowid='rowid'`).
  4. `DROP TABLE tasks_old`; пересоздать все индексы и триггеры из п.1 (определения дословно из 003/008); `INSERT INTO tasks_fts(tasks_fts) VALUES('rebuild')`.
  5. `UPDATE tasks SET project_id=NULL, column_id=NULL WHERE project_id='00000000-0000-0000-0000-00000000cafe'`.
  6. `DELETE FROM projects WHERE id='...cafe'` (boards→columns каскадятся; задачи уже отвязаны).
  7. Guarded delete placeholder-пользователя: `DELETE FROM users WHERE id='...0001' AND role='system' AND NOT EXISTS (SELECT 1 FROM projects WHERE owner_id='...0001')`.
- [ ] **16.3** Domain (`internal/domain/task/`):
  - `Validate()`: убрать отказ на пустой `ProjectID`; добавить инвариант `ProjectID=="" && ColumnID!="" → ErrInvalidInput` (у inbox-карточки нет колонки).
  - `Filter`: добавить `NoProject bool` (true → `project_id IS NULL`, игнорирует `ProjectID`).
  - Докомментарии: `ProjectID string` — `""` = Inbox.
- [ ] **16.4** SQLite repo (`task_repo.go`):
  - `Create`: `nullString(t.ProjectID)` (сейчас сырая строка — `""` ловил бы FK-ошибку).
  - `Update`: добавить `project_id = ?` в SET (сейчас проект нельзя сменить вообще).
  - `scanTask`/`scanTaskRow`: `project_id` сканить в `sql.NullString` (NULL в `*string` падает).
  - `ListByProject`: ветка `NoProject` → `project_id IS NULL`; пустой `ProjectID` без `NoProject` — по-прежнему `ErrInvalidInput`.
- [ ] **16.5** API — inbox endpoints + PATCH project_id (`handlers_tasks.go`, `router.go`):
  - `GET /api/v1/inbox/tasks` → `{"tasks": [...]}` (фильтр `?status=` как у project-листа; порядок `position, created_at`).
  - `POST /api/v1/inbox/tasks` — тело как у create в проекте, проект принудительно `""`. Общую create-логику вынести в helper, вызываемый из обоих хендлеров.
  - `taskInput.ProjectID *string` (pointer: absent ≠ explicit `""`). В `patchTaskHandler`: смена проекта → если `""` то `ColumnID=""`; если задан и (колонка пуста или проект изменился) → `ColumnID=FirstColumnID(newProject)`, явный `column_id` из тела приоритетнее.
  - Маршруты в группе `RequireUser`: `r.Route("/inbox", ...)`.
- [ ] **16.6** Event service + calendar handlers:
  - Удалить поле `Service.DefaultProjectID` и fallback в `Create`: пустой проект ⇒ `project_id=NULL`, `FirstColumnID` не вызывается.
  - `mergeEventIntoTask`: копировать `ProjectID` безусловно (иначе событие нельзя вернуть в inbox).
  - `handlers_calendar.go`: `eventInput.ProjectID` → `*string`; update применяет при `!= nil` (explicit `""` = убрать проект).
- [ ] **16.7** Move consistency (`internal/service/task/move.go`): при `Move` в колонку проекта P задаче с другим/пустым проектом проставлять `ProjectID=P` (перетаскивание inbox-карточки на доску = назначение проекту).
- [ ] **16.8** `notifyTaskAssignee` (`handlers_notifications.go`): при `ProjectID==""` уведомлять первого non-system пользователя (`deps.Users.List` + фильтр `role != system`); если пользователей нет — молча пропустить (текущее поведение для битого проекта).
- [ ] **16.9** cmd/orenda cleanup: удалить `inboxProjectID`, `ensureInboxProject`, `ensureInboxBoardAndColumns`, оба вызова (`cli_helpers.go`, `main.go`), строку `eventSvc.DefaultProjectID = ...`. `runUserResetPassword` фильтр `RoleSystem` оставить (безвреден).
- [ ] **16.10** Sync (`handlers_sync.go`): `create_task` с пустым `Target` теперь создаёт inbox-задачу (Validate это разрешит) — покрыть тестом; `create_event` без `project_id` создаёт событие без проекта (fallback удалён в 16.6).
- [ ] **16.11** Frontend — типы и клиент (`shared/api/client.ts`, `shared/constants.ts`):
  - `Task.project_id: string` (бэкенд всегда эмитит; `""` = inbox). Удалить `INBOX_PROJECT_ID` и сам `constants.ts`, если других экспортов нет.
  - `api.listInboxTasks()`, `api.createInboxTask({title, ...})` → новые endpoints из 16.5.
- [ ] **16.12** Frontend — страница `/inbox` (`features/inbox/InboxPage.tsx` + route в `App.tsx`):
  - Плоский список карточек (можно переиспользовать `TaskCard`), сортировка как отдаёт бэкенд.
  - Inline-форма быстрого добавления (title) → `createInboxTask`.
  - На карточке: селект «назначить проекту» (список активных проектов) → `PATCH /tasks/:id {project_id}`; клик открывает задачу (существующий modal-паттерн).
  - Empty state («Inbox пуст — всё разобрано»).
  - Dashboard stats (`App.tsx`) учитывают inbox-задачи в open count.
- [ ] **16.13** Frontend — сайдбар: статичный пункт Inbox на месте бывшего системного проекта (ссылка `/inbox`, бейдж = число inbox-задач не в `done`, через `listInboxTasks`); убрать bucket `inbox` из `partitionProjects.ts` и prop `isSystem` из `SidebarProjectItem.tsx`; обновить `partitionProjects.test.ts`.
- [ ] **16.14** Frontend — календарь (`CalendarPage.tsx`): удалить локальный `INBOX_PROJECT_ID`; draft по умолчанию `project_id: ''`; в `<select>` проектов первой опцией `value=""` — «Inbox (без проекта)»; submit шлёт `undefined` при `""`.
- [ ] **16.15** Frontend — зачистка special-casing: `ProjectDetailPage.tsx`, `tabs/ProjectSettingsTab.tsx` (убрать `isInbox`: rename/archive/delete единообразны для всех проектов); `ChildTasksList.tsx` — `projectId` опционален, пустой → `createInboxTask` с `parent_task_id` (иначе POST на `/projects//tasks`); `TaskViewBody.tsx` — скрывать ссылку на проект при пустом `project_id`; `outbox.ts queueCreateTask` — разрешить пустой `projectId`.
- [ ] **16.16** Обновить существующие тесты:
  - `service/event/event_test.go` — убрать `DefaultProjectID`; «create without project» теперь ждёт `ProjectID == ""`.
  - `api/phase8_sync_test.go:155` — убрать присвоение `DefaultProjectID`.
  - `cmd/orenda/user_test.go::TestRunUserList_Empty` — системного пользователя больше нет: свежая БД пуста.
  - `storage/sqlite/user_repo_test.go` — не ждёт seed system-пользователя.
  - Комментарии: `project_repo_test.go`, `scope_integration_test.go`, `backup/restore_test.go` (системный Inbox больше не создаётся).
- [ ] **16.17** Новые тесты:
  - Миграция 015 (по образцу `TestMigrate_013/014` через `applyUpTo`): seed inbox-проект с board/column + 2 задачи, real-проект с parent/child + checklist на child → после миграции: inbox-задачи `project_id/column_id IS NULL`; parent-link и checklist целы (нет каскадного вайпа); `...cafe`, его board/columns и system-user удалены; FTS матчит перенесённую задачу; `trg_tasks_touch` работает.
  - Repo: `Create`/`GetByID` roundtrip с пустым проектом; `ListByProject{NoProject:true}`; `Update` меняет проект туда-обратно.
  - API: `POST/GET /api/v1/inbox/tasks`; `PATCH /tasks/:id {project_id:"p"}` → задача ушла из inbox, `column_id` = первая колонка проекта; `PATCH {project_id:""}` → вернулась в inbox, колонка очищена.
  - Event: create без `project_id` → `project_id=""`; PATCH события `project_id:""` очищает проект.
  - Frontend: `partitionProjects` без inbox; `client.test.ts` — `listInboxTasks/createInboxTask`; `TaskCard` с `project_id=""` не падает.
- [ ] **16.18** Документация: `docs/API.md` (endpoints `/inbox/tasks`, `project_id` опционален в task/event payloads), `docs/DB.md` (015: nullable `project_id`, удаление Inbox), `docs/SESSION.md` (решение: Inbox ≠ проект).

### Definition of Done

- Inbox — маршрут `/inbox` с плоским списком задач без проекта; создание карточки, назначение проекту (карточка уходит из inbox на доску проекта в первую колонку), возврат в inbox.
- В сайдбаре Inbox — статичный пункт с бейджем количества, не проект; архивировать/удалить/переименовать его нельзя (это не сущность).
- События календаря можно создавать без проекта; «проект по умолчанию» исчез.
- Существующая БД мигрируется без потерь: задачи legacy-проекта Inbox становятся inbox-карточками, дочерние таблицы и поиск целы, `PRAGMA foreign_key_check` чист.
- `make test && make lint` зелёные; `npx vitest` зелёный.

### Что НЕ делаем в этой фазе

- Не меняем семантику удаления проекта (остаётся CASCADE, не «задачи в inbox»).
- Не делаем kanban/dnd внутри inbox — это плоский список; сортировка фиксирована.
- Не трогаем notifications inbox (Phase 6) — одноимённый, но другой концепт.
- Не добавляем agent-side листинг inbox-задач (agent task lists — территория Phase 15).

> **Нумерация миграций:** `014_child_tasks_inherit_column.sql` уже занят (Phase 14 follow-up), эта фаза берёт `015_*.sql`. Плановая миграция Phase 15 (`014_task_dependencies.sql`) при реализации становится `016_task_dependencies.sql`.

---

## Phase 17 — Карточки задач: информативная лицевая сторона (референсы: Weeek, Trello) *(3–4 дня)*

> **Аудит 2026-08-12:** 🟡 — `ListByProjectWithStats` с агрегатами, приоритет-кромка, due-бейдж, счётчики, AssigneeChip с 🤖, pure-функции `taskCardBadges.ts` с тестами — есть. Нет UI-тоггла плотности (флаг читается из localStorage, переключателя нет); нет бейджей времени (estimate/spent) и таймера; inbox не reuse карточку; имя агента — срез `assignee_id`.

**Цель:** канбан-карточка отвечает на вопросы «что горит, кто занят, что внутри» без открытия задачи. Сейчас лицевая сторона — только title + бейдж `↳ child` (`web/src/features/projects/TaskCard.tsx`), при этом payload задачи уже несёт priority/due_at/assignee/awaiting, а бэкенд хранит checklists, children, комментарии, вложения и теги.

**Анализ текущего состояния (2026-08-11):**

- `TaskCard.tsx` рендерит: title, бейдж child. Всё.
- В `Task` (API) уже есть, но не показывается: `priority`, `due_at`, `assignee_type/assignee_id`, `awaiting`, `time_estimate_s/time_spent_s`, `started_at` (таймер идёт).
- TS-тип `Task` в `client.ts` **отстаёт от бэкенда**: нет `start_at/end_at/all_day/color` (эмитятся с миграции 012) — починить заодно.
- Нет в payload (нужны агрегаты): прогресс children (`ChildProgress` есть репо-метод, в список не включён), прогресс checklist_items, счётчики комментариев/вложений, теги (Phase 13 запланирована, не реализована).
- Модалка задачи (`TaskViewBody`) богатая — проблема только лицевой стороны доски.

**Референс-анатомия:**

| Элемент | Trello | Weeek | Берём |
|---|---|---|---|
| Цветная кромка/полоса (приоритет/метка) | cover-полоса сверху | цветной маркер приоритета | левая 3px кромка = priority |
| Теги | цветные pills над заголовком | чипы | чипы (зависит от Phase 13) |
| Дата | бейдж со состояниями: overdue=красный, soon=янтарный, done=зелёный | дедлайн красным при просрочке | бейдж due со состояниями |
| Прогресс | `☑ x/y` чек-листа | `x/y` подзадач | оба: children и checklist |
| Счётчики | 💬 📎 👁 | 💬 📎 | 💬 📎 |
| Исполнитель | аватары внизу справа | аватар внизу справа | чип; **агент ≠ человек** визуально (наш дифференциатор) |
| Быстрые действия | hover: pencil (quick edit) | hover: исполнитель/статус/приоритет/таймер | hover-действия (P2) |
| Таймер | — | запуск таймера с карточки | бейдж «таймер идёт» (started_at), запуск — P2 |

**Целевая раскладка карточки:**

```
┌──────────────────────────────┐
│▎tags: [фича][bug]           │  ▎= левая кромка приоритета (urgent=red, high=orange, low=slate)
│▎Заголовок задачи             │
│▎↳ child · ⏳ ждёт агента     │  (только если применимо)
│▎📅 12 авг  ⏱ 3ч/5ч           │  due: красный/янтарный/зелёный по состоянию
│▎☑ 2/5  ↳ 1/3  💬4  📎1   🤖QA │  счётчики · исполнитель справа
└──────────────────────────────┘
```

Плотность регулируется одним переключателем «компактно/подробно» (localStorage, паттерн уже заведён для `orenda.kanban.showChildren`).

### Tasks

- [ ] **17.1** Бэкенд — агрегаты в списке задач (`task_repo.go`, `handlers_tasks.go`):
  - Один aggregate-запрос (без N+1): `comments_count` (`comments WHERE target_type='task'`), `attachments_count`, `children_total/children_done`, `checklist_total/checklist_done` (join через `checklists`).
  - Форма: отдельный метод `ListByProjectEnriched` или опциональный `Filter.WithAggregates` — решить при реализации; `GET /projects/{id}/tasks` и `GET /inbox/tasks` (Phase 16) возвращают обогащённые карточки.
  - TS-тип `Task` в `client.ts`: добавить `start_at/end_at/all_day/color` + поля агрегатов (optional, чтобы не ломать `GET /tasks/{id}`).
- [ ] **17.2** Декомпозиция `TaskCard.tsx` на чистые блоки: `PriorityBorder`, `TaskDueBadge` (чистая функция состояния: `done|overdue|today|upcoming` от `due_at`/`completed_at`), `TaskProgressBadges`, `TaskCounters`, `AssigneeChip` (user=инициалы, agent=🤖+имя; distinct цвет), `AwaitingBadge` (`awaiting != none`). Логику состояний — в чистые функции рядом (`taskCardBadges.ts`) для unit-тестов.
- [ ] **17.3** Левая кромка приоритета: `urgent` → red-500, `high` → orange-400, `medium` → прозрачная, `low` → slate-300. Не использовать флаги-иконки (шум при плотной доске).
- [ ] **17.4** Бейдж даты: `due_at` — состояния `overdue` (красный фон), `today` (янтарный), `upcoming` (нейтральный), `done` (зелёный, когда `completed_at` или `status=done`); формат `d MMM` (ru), год только если не текущий. Если задан `start_at` — отдельный нейтральный бейдж «запланировано» (📆 + дата).
- [ ] **17.5** Прогресс и счётчики: `☑ x/y` (checklist, скрывать при total=0), `↳ x/y` (children), `💬 n`, `📎 n`, `⏱ spent/estimate` (только когда `time_estimate_s > 0`; краснить при перерасходе). Иконки — unicode/текст, как сейчас (без иконочной библиотеки — конвенция проекта).
- [ ] **17.6** Исполнитель и ожидание: `AssigneeChip` справа внизу; agent → чип с 🤖 и именем агента (title=type), user → инициалы. `awaiting=agent|human` → маленький бейдж «⏳ агент» / «⏳ я» рядом с title. Идущий таймер (`started_at` без `completed_at` при активном time_entry) — пульсирующая точка: проверить доступность признака в payload (single-active timer, Phase 4), если дорого — в P2.
- [ ] **17.7** Теги-чипы на карточке — **только после Phase 13** (тегов нет в API); задача-блокер: дизайн места под чипы заложить в 17.2, само отображение включить флагом.
- [ ] **17.8** Переключатель плотности доски: «компактно» (только title + кромка приоритета + due-бейдж) / «подробно» (всё). `localStorage orenda.kanban.cardDensity`, дефолт «подробно». Тоггл рядом с `showChildren`.
- [ ] **17.9** P2 (можно выносить в отдельную фазу): hover quick-actions (done-toggle, назначить, дата — по Weeek), обложка карточки (первое image-вложение, по Trello), индикатор «зависшей» карточки (`in_progress` + `updated_at` старше N дней).
- [ ] **17.10** Inbox (Phase 16) и любые списки задач переиспользуют обогащённый `TaskCard` — убедиться, что блоки не зависят от kanban-контекста (dnd-listeners изолируются в обёртке).
- [ ] **17.11** Тесты:
  - Unit: `taskDueState()` — все четыре состояния + границы (ровно полночь, год); формат даты; прогресс-бейдж скрывается при `total=0`; `AssigneeChip` agent/user ветки.
  - API: агрегаты в `GET /projects/:id/tasks` (seed: 2 children 1 done, checklist 2/3, comment, attachment → корректные счётчики), отсутствие N+1 (1 запрос агрегатов на список).
  - Snapshot/рендер: карточка с полным набором бейджей и пустая (title only) — обе без сдвигов верстки (min-height строк).
- [ ] **17.12** Документация: `docs/API.md` — новые поля агрегатов в ответе списка задач; `docs/SESSION.md` — решение по анатомии карточки.

### Definition of Done

- На доске по карточке видно: приоритет (кромка), дедлайн с цветовым состоянием, прогресс children и чек-листа, счётчики 💬/📎, исполнителя (агент отличим от человека), режим ожидания.
- Ни одного лишнего запроса: список задач отдаёт все бейджи одним ответом; рендер доски не делает per-card fetch.
- Переключатель «компактно/подробно» работает, выбор персистится.
- Пустая карточка (только title) не «пляшет» по высоте рядом с наполненными.
- `make test && make lint` + `npx vitest` зелёные.

### Что НЕ делаем в этой фазе

- Теги на карточке (ждёт Phase 13), кастомные поля, стикеры, голосование, watch/subscribe-глаз.
- Обложки и hover quick-actions — вынесены в 17.9 (P2), по готовности отдельной фазой.
- Не меняем модалку задачи (`TaskViewBody`) — она уже богатая; фаза про лицевую сторону.

---

## Phase 18 — Личные курсы, создаваемые ИИ-агентами *(1–1.5 недели)*

> **Аудит 2026-08-12 (обновлено после 27.4.A/B):** ✅ — LMS-цикл закрыт end-to-end: MaterializeLesson (locked→open), AnswerQuiz (exact с нормализацией; open → review-задача тьютору), GeneratorTask wire, страница `/lessons/:id` (LessonPage), endpoints user+agent, 14 service-тестов + LessonPage vitest, E2E `course.spec.ts` happy-path зелёный. **2026-08-13:** зафиксирован дефект — наполнение возможно только через агента: user-side мутаций дерева нет, quiz creation не экспонирован ни в одном namespace. Закрывается в **Phase 27.6**.

**Цель:** пользователь формулирует намерение («выучить Rust за месяц, 3 раза в неделю по часу»), внешний ИИ-агент-тьютор строит программу курса, материализует уроки и упражнения и проверяет ответы. Курс — first-class LMS-сущность (программа → модули → уроки → вопросы), а упражнения остаются обычными задачами, чтобы переиспользовать claim/submit/review-flow агентов.

**Контекст (что уже есть):**

- Агентский цикл `claim → submit → review` на задачах (`/api/v1/agent/*`, Phase 3) — проверка ответов ученика ложится на него без изменений.
- Wiki (Phase 5) может хранить справочные материалы; календарь/RRULE (Phase 4) — расписание занятий; задачи — упражнения с дедлайнами.
- Long-poll `/api/v1/events/await` позволяет тьютору реагировать на новые курсы и ответы ученика без WS.
- Single-owner: ACL на курсы не нужен, agent namespace уже изолирован токенами.

**Ключевые решения (согласовано 2026-08-11):**

- **Полная LMS-модель:** `courses → course_modules → course_lessons → course_quizzes` (свои таблицы, не композиция «проект + wiki»).
- **Агент в MVP: генерация + проверка.** Диалоговый тьютор, адаптация темпа и интервальное повторение — за скобкой.
- **Оркестрация через задачи:** создание курса порождает generator-задачу («построй программу», `awaiting=agent`, `context_md` = намерение + course_id). Тьютор claim'ит её, пишет программу через agent-endpoints курсов, сабмитит — человек ревьюит. Новых оркестраторов не вводим.
- **Упражнение = задача.** `course_lessons.task_id` связывает урок с задачей-практикой; проверка ответа — штатный review этой задачи тьютором.

**Референсы (open-source LMS):**

| Проект | Стек | Что смотрим |
|---|---|---|
| [Frappe LMS](https://github.com/frappe/lms) | Python/Vue | **Главный референс.** Близкий по духу lean-LMS: courses/chapters/lessons/quizzes, прогресс, чистая схема БД и UI. Сверять нашу миграцию 017 с их моделью. |
| [Canvas LMS](https://github.com/instructure/canvas-lms) | Ruby | Модули с requirements/prerequisites (`must_view / must_mark_done / must_submit / min_score`) и последовательной разблокировкой — ровно наш `locked→open→done`. |
| [Moodle](https://github.com/moodle/moodle) | PHP | Эталон доменной модели: activity completion rules, типы вопросов quiz, gradebook. Тяжёлый — берём идеи, не архитектуру. |
| [Open edX](https://github.com/openedx/edx-platform) | Python | Иерархия section→subsection→unit→XBlock; компонуемые контент-блоки урока (текст/видео/задача). Полезно, когда `content_md` урока перерастёт один markdown. |
| [Chamilo](https://github.com/chamilo/chamilo-lms) | PHP | Learnpaths — последовательные цепочки уроков; близко к нашему потоку обучения. |
| [H5P](https://github.com/h5p) | JS/PHP | Интерактивные типы вопросов/контента — референс для расширения `course_quizzes.kind` после MVP. |
| [Anki](https://github.com/ankitects/anki) | Rust | Не LMS: эталон интервального повторения (SM-2/FSRS) — для будущей фазы повторения материала. |
| GitHub topic [`ai-course-generator`](https://github.com/topics/ai-course-generator) | — | Мелкие проекты (1–11★), но единый паттерн генерации: prompt → curriculum JSON → per-lesson контент → quiz. Подтверждает нашу двухстадийность `draft→review→active`. |

**Жизненный цикл курса:** `draft` (намерение) → тьютор строит программу → `review` (человек правит/принимает) → `active` (тьютор наполняет уроки контентом) → обучение (уроки открываются последовательно) → `done`. Плюс `archived`.

### Tasks

- [ ] **18.1** Миграция `017_courses.sql` (`014` — child-inherit, `015` — inbox/Phase 16, `016` — dependencies/Phase 15; нумерация по факту занятости):
  - `courses(id, title, intent_md, level, pace, status draft|review|active|done|archived, owner_id→users, generator_task_id→tasks NULL, created_at, updated_at)`.
  - `course_modules(id, course_id→courses CASCADE, title, description, position)`.
  - `course_lessons(id, module_id→modules CASCADE, title, position, content_md, status locked|open|done, task_id→tasks SET NULL)`.
  - `course_quizzes(id, lesson_id→lessons CASCADE, position, question_md, expected_md, kind open|exact)`.
  - Индексы по всем FK + `courses(status)`.
- [ ] **18.2** Domain (`internal/domain/course/`): сущности, `Validate()`, переходы статусов курса (`draft→review→active→done`, `review→draft` на доработку) и урока (`locked→open→done`); ошибки-сентинелы.
- [ ] **18.3** SQLite repo: CRUD курсов/модулей/уроков/вопросов; `Progress(ctx, courseID) (lessons_total, lessons_done, quiz stats)`; выборка «следующий open-урок».
- [ ] **18.4** Service (`internal/service/course/`):
  - `CreateWithIntent` — создаёт курс (draft) + generator-задачу (context_md: intent, level, pace, course_id; awaiting=agent; в проект по умолчанию или без проекта после Phase 16).
  - `SubmitCurriculum(courseID, modules[])` — атомарно заменяет черновик программы (delete+insert в tx), курс → review.
  - `ApproveCurriculum` → active; `RequestChanges` → draft с комментарием.
  - `MaterializeLesson` — тьютор пишет `content_md` + создаёт/линкует задачу-упражнение.
  - `CompleteLesson` — урок done → следующий `locked→open`; курс → done, когда все уроки done.
  - `AnswerQuiz` — `exact`: автопроверка (нормализация строк); `open`: создаёт review-задачу тьютору с ответом в `context_md`.
- [ ] **18.5** User API (`RequireUser`): `GET/POST /api/v1/courses`, `GET/PATCH /api/v1/courses/{id}` (дерево модулей+уроков+прогресс), `POST /courses/{id}/approve`, `POST /courses/{id}/request-changes`, `POST /lessons/{id}/complete`, `POST /quizzes/{id}/answer`.
- [ ] **18.6** Agent API (`RequireAgent`, namespace `/api/v1/agent/`): `GET /agent/courses?status=draft` (работа для тьютора), `PUT /agent/courses/{id}/curriculum`, `PUT /agent/lessons/{id}/content`, `POST /agent/lessons/{id}/quizzes`. Доступ без per-course ACL (single-owner).
- [ ] **18.7** Frontend:
  - `/courses` — список курсов: статус, прогресс-бар (уроки done/total), CTA «продолжить» → следующий open-урок.
  - Wizard создания: намерение (свободный текст), уровень, темп → POST /courses; пояснение, что агент подхватит.
  - `/courses/:id` — дерево модулей/уроков со статусами (locked серые); экран ревью программы для `review` (принять/на доработку с комментарием).
  - `/lessons/:id` — контент (markdown), вопросы с полями ответов, кнопка «завершить урок», ссылка на задачу-упражнение.
  - Sidebar: пункт Courses после Wiki.
- [ ] **18.8** Тесты:
  - Миграция: таблицы+FK+индексы; каскады (удаление курса сносит модули/уроки/вопросы; удаление задачи → `task_id=NULL`).
  - Domain: запрет недопустимых переходов статусов.
  - Service: `SubmitCurriculum` атомарен (падающая вставка не оставляет полупрограмму); `CompleteLesson` открывает следующий и закрывает курс; `exact`-quiz автопроверка (регистр/пробелы).
  - API: полный цикл «создать курс → тьютор строит программу → ревью → active → урок done» интеграционно; agent-endpoints отклоняют user-cookie и наоборот.
  - Frontend: прогресс-бар курса; locked-урок не кликабелен; ревью-экран показывает черновик программы.
- [ ] **18.9** Документация: `docs/API.md` (courses endpoints, agent namespace), `docs/DB.md` (017), `docs/SESSION.md` (решение: LMS-модель + оркестрация задачами); пример system-prompt'а тьютора в `docs/` (формат curriculum JSON для `PUT .../curriculum`).

### Definition of Done

- Пользователь создаёт курс намерением; тьютор-агент без ручных пинков (через events/await + generator-задачу) строит программу; человек принимает её одной кнопкой.
- Уроки открываются последовательно; прогресс курса виден на `/courses`.
- Ответ на open-вопрос уходит тьютору на проверку штатным review-flow; exact-вопрос проверяется мгновенно.
- Весь цикл воспроизводится в интеграционном тесте с mock-тьютором через публичные endpoints.
- `make test && make lint` + `npx vitest` зелёные.

### Что НЕ делаем в этой фазе

- Диалоговый тьютор (чат), адаптация темпа, интервальное повторение (кандидат на RRULE-события позже).
- Сертификаты, оценки/баллы, таймеры на вопросы, импорт/экспорт курсов (SCORM и т.п.).
- Встроенный LLM-рантайм: тьютор — внешний агент, как и везде в Orenda.
- Multi-user: курсы single-owner, как весь продукт сейчас.

---

## Phase 19 — Ревью-очередь: замыкание цикла агент→человек *(2–3 дня)*

> **Аудит 2026-08-12:** ✅ — `GET /review-queue` (+ `/count`), `/review` страница с inline Accept/Return, сайдбар-бейдж с WS-обновлением, тесты — есть. Оговорка: backend не форсирует обязательный `comment` при reject (фронт подсказывает, но пустая строка проходит).

**Цель:** у человека есть один экран со всем, что ждёт его решения: задачи с `awaiting='human'` и в статусе `review`. Сейчас петля делегирования асимметрична: человек→агент работает (назначил, агент claim'ит), агент→человек — только notification, который легко потерять.

**Контекст (что уже есть):**

- `tasks.awaiting` (`none|human|agent`) и `POST /tasks/{id}/submit` + `/review` (Phase 3): агент сабмитит, `awaiting` становится `human`.
- Notifications `task.review_needed` (Phase 6) — сигнал есть, агрегированного списка нет.
- Бейджи в сайдбаре уже существуют для проектов (open counts).

### Tasks

- [ ] **19.1** Repo/service: `ListAwaitingReview(ctx)` — задачи `awaiting='human' OR status='review'`, join с projects для имени проекта, сортировка по `updated_at DESC` (свежее сверху). Учесть inbox-задачи (Phase 16, `project_id IS NULL` → join nullable).
- [ ] **19.2** API: `GET /api/v1/review-queue` → `{tasks: [...], count}`; count переиспользуется бейджем сайдбара.
- [ ] **19.3** Действия из списка: «принять» → `POST /tasks/{id}/review {decision:"accept"}`; «вернуть» → `{decision:"reject", comment}` (comment обязателен при reject — агенту нужна обратная связь; проверить, что review-endpoint принимает comment, при необходимости добавить).
- [ ] **19.4** Frontend: страница `/review` (список обогащённых карточек Phase 17 + accept/reject inline); пункт в SidebarNav с бейджем count; автообновление по WS-событиям `task.*`.
- [ ] **19.5** Тесты: submit агентом → задача в очереди; accept → `done`, из очереди пропадает; reject с comment → `todo` + `awaiting=agent` + комментарий виден в задаче; пустая очередь → 200 с `[]`.
- [ ] **19.6** `docs/API.md` + `docs/SESSION.md`.

### Definition of Done

- `/review` показывает всё ожидающее человека; accept/reject без открытия задачи; бейдж с числом в сайдбаре обновляется realtime.
- Submit mock-агентом виден в очереди без перезагрузки страницы.

### Что НЕ делаем

- SLA/дедлайны на ревью, эскалации, массовые действия, фильтры по агентам.

---

## Phase 20 — Экран «Сегодня» (daily driver) *(2–3 дня)*

> **Аудит 2026-08-12:** ✅ — `GET /api/v1/today` одним round-trip (overdue/due_today/scheduled_today/upcoming_week/awaiting_count/active_timer), TodayPage с секциями, TZ-тесты — есть. Оговорки: нет quick-complete чекбокса; пункт сайдбара всё ещё подписан «Dashboard».

**Цель:** домашняя страница отвечает «что у меня сегодня»: просроченное, due сегодня, запланированное по времени, ожидающие меня (ссылка в Phase 19), активный таймер. Сейчас `/` — только статистика-счётчики.

**Контекст (что уже есть):**

- `due_at` на задачах; `start_at/end_at` (календарные задачи, `ListInRange`); single-active timer (Phase 4, `time_entries` с `ended_at IS NULL`); stats-дашборд в `App.tsx`.

### Tasks

- [ ] **20.1** API: `GET /api/v1/today` — один round-trip: `{overdue[], due_today[], scheduled_today[], awaiting_count, active_timer}`. Не собирать 5 запросов на клиенте.
- [ ] **20.2** Frontend: `TodayPage` на `/` (stats-дашборд уезжает ниже или в `/reports`); секции с обогащёнными карточками (Phase 17); quick-complete чекбоксом прямо в списке; empty state («день свободен»).
- [ ] **20.3** Секция «ближайшие 7 дней» (due, сгруппировано по дате) — компактная, одна строка на день.
- [ ] **20.4** Тесты: агрегация `/today` (границы полуночи в локальной TZ пользователя — явно выбрать TZ-источник: server config, документировать); quick-complete дёргает PATCH и убирает карточку.
- [ ] **20.5** `docs/API.md`.

### Definition of Done

- Открывая app, пользователь видит план дня без кликов; просрочка визуально отлична (красная секция); активный таймер виден с elapsed.

### Что НЕ делаем

- Drag-перепланирование между днями, time-blocking на календаре, smart-приоритизацию.

---

## Phase 21 — Quick capture в Inbox *(1–2 дня)*

> **Аудит 2026-08-12:** ✅ — модалка через Portal, хоткеи `q`/`Cmd+K`, `Cmd+Enter` submit, кнопка `+`, toast «Open task», Telegram auto-capture с обрезкой >200 — есть. Оговорка: нет optional due-поля в модалке.

**Цель:** захват мысли ≤ 1 хоткей / 2 клика из любого экрана + приём сообщений из Telegram сразу в Inbox. GTD-capture без трения.

**Контекст (что уже есть):**

- `POST /api/v1/inbox/tasks` (Phase 16) — точка приземления. Если Phase 16 ещё не сделана, фаза берёт `POST /projects/{id}/tasks` с явным проектом как fallback, но целевое состояние — Inbox.
- Telegram-бот на long-poll (Phase 10) уже умеет входящие сообщения и знает `chat_id` подписчиков.
- Хоткеев в SPA пока нет; модалка-паттерн есть (TaskModal).

### Tasks

- [ ] **21.1** Frontend: глобальный хоткей `q` (и `Ctrl/Cmd+K` как палитра-кандидат) → capture-модалка: title (автофокус), опционально due; submit → Inbox; toast со ссылкой «открыть».
- [ ] **21.2** Кнопка «+» в топбаре на всех экранах → та же модалка.
- [ ] **21.3** Telegram: входящее личное сообщение от подписанного пользователя → задача в Inbox (title = текст сообщения, обрезка 200 chars); бот отвечает «✅ в Inbox»; команда боту не требуется.
- [ ] **21.4** Тесты: capture → задача в inbox-листе; TG-сообщение от подписчика создаёт задачу, от неподписанного chat_id — игнор + лог.
- [ ] **21.5** `docs/API.md` (если появятся новые endpoints), `docs/SESSION.md`.

### Definition of Done

- Захват из любого экрана хоткеем; сообщение боту появляется в Inbox за секунды (long-poll уже крутится).

### Что НЕ делаем

- NLP-разбор («завтра в 15:00» → due), вложения через TG, voice.

---

## Phase 22 — Restore-from-snapshot *(2–3 дня)*

> **Аудит 2026-08-12 (обновлено):** ✅ — CLI restore pipeline (guard → safety-copy → atomic swap → migrate → integrity/foreign_key check), maintenance mode (atomic.Bool), in-process restore handler, тесты — есть. UI restore замкнут (22.3): кнопка в Settings→Backups делает in-process restore через maintenance mode.

**Цель:** бэкап-контур Phase 7 замыкается: восстановление из sqlite-снапшота через CLI и UI, с safety-copy и post-restore миграциями. Бэкап без проверенного restore — не бэкап.

**Контекст (что уже есть):**

- `backup.Restore` (`internal/backup/backup.go:302`) — filesystem copy, не вызывается; `POST /backups/restore` endpoint существует; снапшоты с ротацией (Phase 7); FK-off runner (Phase 16.1) даёт `foreign_key_check` паттерн.

### Tasks

- [ ] **22.1** CLI: `orenda backup restore --snapshot <path>`: остановить writer (single-conn), safety-copy текущей БД (`orenda.db.pre-restore-<ts>`), замена файла (включая `-wal`/`-shm` обработку), `migrate up` на восстановленной БД, `PRAGMA integrity_check` + `foreign_key_check`, отчёт.
- [x] **22.2** UI: в Settings → Backups список снапшотов (endpoint есть) + кнопка Restore с модалкой-подтверждением (явный текст про замену данных); после restore — перезагрузка SPA. **✅ закрыто в работе `phase-22-ui-restore` — модалка предлагает CLI-hint и "Restore in this window"; inline-кнопка ведёт через maintenance → force restore → reload.**
- [x] **22.3** Защита от гонок: restore только при остановленном serve (CLI) или через maintenance-режим (UI: drain WS, блокировка API middleware на время restore). **✅ закрыто в Phase 22.3 — `internal/api/handlers_restore.go` + maintenance middleware.**
- [ ] **22.4** Тесты: snapshot → изменить данные → restore → данные снапшота на месте; restore старой версии схемы → миграции догоняют; safety-copy создана и валидна.
- [ ] **22.5** `docs/API.md`, `docs/SESSION.md`; install docs: шаг проверки restore при установке.

### Definition of Done

- Полный цикл «снапшот → потерял данные → restore → данные на месте» воспроизводится тестом; serve стартует после restore без ручных шагов.

### Что НЕ делаем

- Point-in-time recovery из WAL-архива, шифрование/пароли на снапшоты, remote-pull из git-бэкапа.

---

## Phase 23 — Техдолг: WIP limits + recurring events *(1–2 дня)*

> **Аудит 2026-08-12:** ✅ — WIP реально блокирует move (`lookupWIPLimit` → `ErrColumnFull` → 422), RRULE DAILY/WEEKLY/MONTHLY + INTERVAL/COUNT/UNTIL разворачивается, тесты на оба. Оговорка: нет UI-обратной связи (toast «N из M», подсветка переполненной колонки).

**Цель:** закрыть две полу-проведённые фичи, найденные аудитом 2026-08-11. Обе — «честный долг»: дёшево доделать, дорого тащить дальше.

**Контекст (доказанные дыры):**

- `wip_limit`: колонке можно задать лимит (PATCH валидирует, UI редактирует), но `lookupWIPLimit` (`internal/service/task/move.go:163`) — заглушка `return 0, false`: перенос в переполненную колонку **не блокируется**. Латентный баг рядом: `ListByProject(Filter{ColumnID})` без `ProjectID` → `ErrInvalidInput` (пересечение с Phase 16.4 — согласовать порядок).
- Recurring events: RRULE-машинерия (`ExpandRecurrence`) написана и покрыта тестами, `recurrence` принимается API, но `listEventsHandler` expansion не вызывает — повторяющееся событие сохраняется и никогда не разворачивается на календаре.

### Tasks

- [ ] **23.1** WIP: реальный `lookupWIPLimit` через columns repo (прокинуть в `task.Service`); `ErrColumnFull` → 422 `wip_limit` (маппинг в handler уже есть); починить column-only `ListByProject` (вместе с Phase 16.4, что бы ни вышло первым — второе ребейзится).
- [ ] **23.2** Kanban UI: 422 при drop → toast «колонка переполнена (N из M)»; колонка с `count > limit` подсвечена.
- [ ] **23.3** RRULE: `listEventsHandler` разворачивает мастер-события через `ExpandRecurrence` в окне `[from,to)`; редактирование — только серии целиком (без per-occurrence). Если решение «выкинуть» — удалить `recurrence` из API/типов/кода чисто. Рекомендация PM: доделать — Phase 18 (курсы) захочет расписание занятий.
- [ ] **23.4** Тесты: move в полную колонку → 422; daily-событие видно в каждом дне окна листинга; bi-weekly `INTERVAL=2` корректен.

### Definition of Done

- Лимит реально блокирует drag-and-drop (и API-move); повторяющиеся события отображаются на календаре — или мёртвый код удалён, промежуточного состояния нет.

### Что НЕ делаем

- EXDATE/исключения occurrences, BYDAY/BYMONTH-правила, серии задач (только события).

---

## Phase 24 — OpenAPI + наблюдаемость *(2–3 дня)*

> **Аудит 2026-08-12:** ✅ — `docs/openapi.yaml` + embed endpoint `/api/v1/openapi.yaml` (публичный), route-coverage тест, `/api/v1/stats`, slow-request log >500ms — есть. Оговорки: `/stats` не заполняет `last_backup_unix` и очередь notifier; coverage-тест обходит не production-роутер целиком, а user-часть (agent/backup роуты вне проверки).

**Цель:** машиночитаемый контракт для внешних агентов (генерация клиентов) + минимальная наблюдаемость self-hosted инстанса.

### Tasks

- [ ] **24.1** `docs/openapi.yaml` (OpenAPI 3.1), вручную поддерживаемый; CI-тест: извлекает маршруты из chi-роутера и сверяет со спекой (расхождение = красный тест). Boring-вариант без codegen-магии.
- [ ] **24.2** `GET /api/v1/openapi.yaml` (публичный, для агентов; без auth — спека не секрет).
- [ ] **24.3** `GET /api/v1/stats` (JSON): uptime, requests total/by-status (in-memory счётчики middleware), ws-подключения, размер БД, последний бэкап/снапшот, очередь notifier. Без внешних зависимостей (нет prometheus client).
- [ ] **24.4** Лог медленных запросов (>500ms) через существующий request-logger middleware.
- [ ] **24.5** Тесты: route-coverage spec-тест; `/stats` smoke; slow-request лог пишется.
- [ ] **24.6** `docs/API.md` — ссылка на спеку; `docs/SESSION.md`.

### Definition of Done

- Агент скачивает спеку и генерирует типизированного клиента; владелец видит здоровье инстанса одним запросом; спека не может «отстать» незаметно (тест).

### Что НЕ делаем

- Prometheus/Alertmanager-стек, tracing, Swagger UI в бандле (redoc по ссылке — опционально).

---

## Phase 25 — Agent DX: MCP server + CLI + skill *(1–1.5 недели)*

> **Аудит 2026-08-12:** ✅ — stdio JSON-RPC MCP (zero deps, 7 инструментов `orenda_*`), `orenda mcp-proxy`, CLI `orenda agent` (8 сабкоманд, флаги>env>yaml, exit 2 = no work), `docs/skills/orenda/SKILL.md` — есть. Оговорки: нет `orenda skill install` и `api-cheatsheet.md`; тесты CLI/MCP частичные (нет roundtrip claim→submit, exit code 2 не покрыт).

**Цель:** внешний агент подключается к Orenda за минуты и сразу правильно играет делегационный цикл. Три поверхности под три способа интеграции: **MCP** (native tool-discovery для MCP-клиентов), **CLI** (скрипты и простые агенты на чём угодно), **skill** (know-how: как работать, а не только чем). Сейчас агент вынужден читать `docs/API.md` и писать HTTP-клиента руками.

**Контекст (что уже есть):**

- Agent REST: `/api/v1/agent/*` — `me`, `heartbeat`, `tasks/{id}/claim|release|submit|context` (Phase 3); long-poll `/api/v1/events/await`; Bearer API-токены (`RequireAgent`).
- **Зависимости:** листинга задач для агентов нет (закрывает Phase 15) — MCP `list_available_tasks` опирается на него; `create_inbox_item` — на Phase 16. Каркас фазы (MCP endpoint, CLI, skill) этих зависимостей не ждёт.
- Phase 24 (OpenAPI) описывает REST машинно; MCP **не** автогенерируется из него — tools проектируются вручную по workflow агента.
- Официальный Go SDK: `github.com/modelcontextprotocol/go-sdk/mcp` (v1.4+; Streamable HTTP — актуальный транспорт спецификации). Новая зависимость, обоснование: официальный SDK вместо ручного JSON-RPC; версию пингуем ≥1.4 (ранние версии без DNS-rebinding protection для HTTP-серверов).

**Ключевые решения:**

- MCP встроен в `orenda serve` (single-binary философия): Streamable HTTP endpoint `/api/v1/agent/mcp` под `RequireAgent` — аутентификация тем же agent-токеном.
- Для клиентов без remote-MCP: `orenda mcp-proxy` — stdio↔HTTP мост в том же бинаре.
- MCP tools отражают workflow (`await → claim → work → submit → review`), а не REST 1:1.
- CLI — сабкоманды существующего бинаря (`orenda agent ...`, cobra уже есть).

### Tasks

- [ ] **25.1** MCP endpoint (`internal/mcp/` или `internal/api/mcp.go` — выбрать по размеру):
  - Server на go-sdk, Streamable HTTP, mount в router под `RequireAgent`: `/api/v1/agent/mcp`.
  - Tools v1: `me`, `await_task` (обёртка над long-poll с таймаутом), `get_task_context`, `claim_task`, `release_task`, `submit_task`, `add_comment`; после Phase 15 — `list_available_tasks`; после Phase 16 — `create_inbox_item`.
  - Resource `orenda://task/{id}/context` (read-only снапшот задачи).
  - DNS-rebinding protection явно включена; SDK ≥1.4 в `go.mod`.
- [ ] **25.2** `orenda mcp-proxy --url --token`: stdio↔Streamable-HTTP мост (клиенты, умеющие только stdio: запускают proxy как локальный процесс).
- [ ] **25.3** CLI `orenda agent` (cmd/orenda, новый файл `agent.go`):
  - `me`, `next` (= await + claim одной командой), `context <id>`, `claim|release|submit <id>`, `comment <id>`, `await --topic`.
  - `--json` на всём; exit codes: `0` ok, `2` = «нет работы» (для shell-циклов `while orenda agent next; do ...; done`).
  - Конфиг по приоритету: env (`ORENDA_URL`, `ORENDA_AGENT_TOKEN`) > флаги > `~/.config/orenda/agent.yaml`.
- [ ] **25.4** Skill-пакет `docs/skills/orenda/SKILL.md` (+ `api-cheatsheet.md` рядом):
  - Делегационный цикл и этикет: всегда `release` при отказе, `comment` при блокере, не держать claim без работы, submit ≠ done (жди review).
  - Выбор поверхности: MCP vs CLI vs REST — когда что.
  - Команда установки: `orenda skill install [--dir <skills-dir>]` — копирует пакет в skills-dir агента.
- [ ] **25.5** Тесты:
  - MCP: handshake/tools-list + полный `claim→submit` roundtrip через in-memory transport SDK; 401 без токена.
  - CLI: `next/claim/submit` против httptest-сервера; exit code 2 при пустом await.
  - Proxy: stdio-кадр доходит до HTTP-хендлера и обратно.
- [ ] **25.6** Документация: `docs/API.md` (MCP endpoint + tools), `docs/SESSION.md`, `README.md` — секция «Для внешних агентов» (3 шага подключения).

### Definition of Done

- MCP-клиент (Claude Code или совместимый) подключается к `/api/v1/agent/mcp` с agent-токеном и проходит цикл claim→submit без ручного HTTP.
- Shell-скрипт на `orenda agent` проходит тот же цикл; `next` возвращает exit 2 при отсутствии работы.
- `orenda skill install` ставит skill; по SKILL.md агент без подсказок воспроизводит цикл (проверено ручным прогоном).
- `make test && make lint` зелёные.

### Что НЕ делаем в этой фазе

- MCP prompts/subscriptions, автогенерацию tools из OpenAPI, OAuth-flow для MCP (Bearer на loopback достаточно).
- Отдельный бинарь `orenda-agent` (всё в одном бинаре).
- Hosted/remote MCP для внешних сетей (auth-модель и TLS — отдельная история с multi-user).

---

## Phase 26 — Верификация фронтенда: E2E smoke + component coverage *(3–4 дня)*

> **Аудит 2026-08-12 (обновлено после merge 26.A–F + 27.x):** ✅ — 7 Playwright spec-файлов / 10 тестов (auth, today, quick-capture, kanban + tag-chips, review, ws-live, course happy-path) против реального бинаря на порту 21371, прогон 2026-08-12: 10/10 зелёный; 199 vitest-тестов покрывают все 17 feature-директорий; `make test` включает vitest; есть `make test-e2e`.

**Цель:** регрессии фронтенда ловятся тестами, а не глазами при dogfooding. Два слоя: **vitest** — логика страниц и компонентов (инфраструктура уже есть), **Playwright** — критические потоки целиком против реального бинаря (роутинг + REST + WS + auth). Отменяет зафиксированное ранее решение пропустить E2E (SESSION.md 2026-08-08, «Что можно дальше»).

**Контекст (что уже есть):**

- vitest + Testing Library + jsdom; конфиг `web/vitest.config.ts` (environment `node`, jsdom opt-in через `// @vitest-environment jsdom` в шапке файла, alias `@`); 11 тестов: утилиты/хуки + `TaskCard`, `ProjectDetailPage`, `TaskTagChip`.
- `make test` теперь гоняет Go + vitest (`cd web && npm run test`); волновое правило свернулось в один таргет — `make test` достаточно. E2E — отдельный таргет `make test-e2e` (требует `make build`, отдельный порт 21371).
- Покрытия нет у 13 из 16 feature-директорий: auth, today, inbox, review, calendar, wiki, search, notifications, settings, agents, reports, attachments, layout.
- Бинарь self-contained: `orenda serve` отдаёт embedded SPA — E2E поднимается без dev-сервера. Seed через CLI (`orenda user create --password-stdin`) и REST (агент для review-потока создаётся через user-JWT API, затем claim/submit через `/api/v1/agent/*`).
- Порт 2137 — singleton (AGENTS.md): тестовый инстанс на `ORENDA_SERVER__PORT=<test-port>` с временной data-директорией (env-override конфига, см. `internal/config`).
- CI в проекте нет — гейты локальные (`make`).

**Ключевые решения:**

- Только Chromium (`npx playwright install chromium`) — local-first, одного браузера достаточно; браузерная матрица — overkill.
- Playwright `webServer` управляет жизненным циклом: `./bin/orenda serve` на тестовом порту, readiness по `/healthz`; global setup готовит tmp `data/`, `migrate up`, seed user/проект/задачу. Сборка бинаря — precondition `make test-e2e`, не playwright.
- E2E — smoke, не exhaustive: 6–8 спек на критические потоки; глубокая логика остаётся на vitest.
- `make test` после фазы = Go + vitest; E2E — отдельный `make test-e2e` (тяжёлые браузеры, не на каждый коммит).

### Tasks

- [ ] **26.1** Playwright scaffold (`web/`):
  - devDep `@playwright/test`; `playwright.config.ts`: `baseURL` из env, `retries: 1` (flake-guard), trace `on-first-retry`.
  - Global setup: tmp `data/`, `migrate up`, seed user (CLI), базовый проект + задача (REST).
  - `webServer`: запуск `./bin/orenda serve` с тестовым портом и tmp data; reuse существующего сервера запрещён (чистая БД обязательна).
  - npm scripts: `test:e2e`, `test:e2e:ui` (отладка).
- [ ] **26.2** E2E smoke-спеки (`web/e2e/`, 6–8 штук):
  - auth: незалогиненный `/projects` → редирект на `/login` → логин → возврат на исходный путь (контракт `RequireAuth`).
  - Today: секции overdue / due_today / scheduled / awaiting рендерятся; seed-задача в нужной секции.
  - Quick capture: `q` открывает модалку, `Cmd/Ctrl+Enter` сабмитит → toast «Open task» → задача видна в Inbox.
  - Канбан: создать задачу → карточка в колонке; перенос dnd (pointer events с движением) → после reload позиция сохранена.
  - Review: seed агент → claim+submit по REST → `/review` показывает карточку → Accept → задача done; Return без комментария заблокирован.
  - WS live-update: открытая `/review` и бейдж в сайдбаре обновляются при submit через API без reload.
- [ ] **26.3** vitest component coverage (jsdom-pragma по месту, `fireEvent` из Testing Library):
  - `RequireAuth` (redirect + сохранение пути), `LoginPage` (submit → api, показ ошибки 401).
  - `TodayPage` (секции по мок-payload `/api/v1/today`), `QuickCapture` (hotkey, submit, toast).
  - `ReviewPage` (inline Accept/Return; Return требует комментарий).
  - `NotificationBell` (badge count, dedup), live-бейдж ревью в сайдбаре.
  - Канбан: чистая логика позиций/reorder (не сам dnd-kit в jsdom).
  - Критерий: каждая из 13 непокрытых feature-директорий получает ≥1 тест; страницы критических потоков покрыты.
- [ ] **26.4** Wiring и документация:
  - `Makefile`: `test` += `cd web && npm run test`; новый таргет `test-e2e` (build + playwright). ✅ Phase 26.F
  - PLAN: волновое правило `make test && npx vitest` сворачивается в `make test`. ✅ Phase 26.F
  - `docs/SESSION.md` — отметить снятие E2E-пропуска. ✅ Phase 26.F

### Definition of Done

- `make test` зелёный (Go + vitest) на `dev`.
- `make test-e2e` зелёный против свежесобранного бинаря на чистой БД; два прогона подряд — без флейков.
- Mutation check: один smoke-поток намеренно сломан в коде — спека падает (доказательство, что тест проверяет поведение).

### Что НЕ делаем в этой фазе

- Матрицу браузеров и мобильные viewport'ы (Chromium/desktop достаточно для local-first).
- Visual regression (screenshot diff) — отдельная история при появлении дизайн-системы.
- E2E для PWA/offline (service worker + Playwright = флейки; outbox уже покрыт unit-тестом).
- Coverage-цели в процентах; покрываем потоки, а не цифру.
- CI-интеграцию (CI нет; `make test-e2e` — локальный/предрелизный гейт).

---

## Phase 27 — Закрытие аудит-дефектов и Phase 18 close-out *(1.5–2 недели)*

> **Аудит реализации 2026-08-12** — три критичных дефекта оставались открытыми после Phase 26. Они блокируют dogfooding: realtime UI не работает в браузере, production-бинарь не содержит SPA, чипы тегов на канбане невидимы. Плюс Phase 18 (LMS) пришёл только в виде MVP-скелета. Делаем Phase 27 — фикс трёх D-дефектов + закрытие Phase 18.

> **Саб-PR (5 штук): 27.1–27.5**, по одному за сессию, сериализованно.

### 27.1 — Фикс D2: web_dist embed *(✅ закрыт в worktree `phase-27-1-web-dist-embed`)*

**Цель:** `make build` → бинарь содержит SPA; production-deploy не зависит от `web/dist` на диске.

**Механика:**

- `//go:embed all:dist` + `//go:embed placeholder.txt` в `internal/embed/web/embed.go`. `dist/.gitkeep` гарантирует, что embed-директория существует в чистом checkout → `go test`/`go vet`/`make dev` компилируются.
- Makefile: новый таргет `embed-dists` (rsync `web/dist/` → `internal/embed/web/dist/`); `build` зависит от `web-build embed-dists`. `clean` восстанавливает gitkeep-only состояние.
- `DistSubFS()` переписан: 1) embedded `dist/` с `index.html` (post-`make build`), 2) on-disk `web/dist/index.html` (dev), 3) placeholder FS (API-only). CWD больше не критичен.

**DoD:**

- `bin/orenda serve` в `/tmp/test` (нет `web/dist/`) → `/` 200, `index.html` 661B, `/assets/*.css` 200 OK.
- `go test ./internal/embed/...` 6/6 pass.
- `make test && make lint && make test-e2e` зелёные.

### 27.2 — Фикс D1: cookie-based WebSocket upgrade *(✅ закрыт в worktree `phase-27-2-ws-cookie`)*

**Цель:** realtime-обновления UI (kanban, review-бейдж, today) работают в браузере.

**Архитектурное решение:** cookie-based WS upgrade. `gorilla/websocket.Upgrader` читает `orenda_session` cookie из request — отдельный `/auth/ws-token` endpoint не нужен. Снимает: TTL-проблему, дополнительный round-trip, выделенный JWT.

**Задачи (все выполнены):**

1. `internal/api/ws/client.go`: новый `extractWSToken(r, cookieName)` с precedence cookie → `Authorization: Bearer` → `?token=`. `Handler` принимает `cookieName`, пробрасывается через `router.go::deps.CookieName`.
2. `web/src/features/auth/AuthContext.tsx`: убран `token` из state. Был always null anyway (аудит 2026-08-12).
3. `web/src/shared/ws.ts::useWebSocketConnection`: connect на `status === 'authenticated'` без проверки токена; URL без query.
4. `web/src/features/layout/AppLayout.tsx`: mount хука в layout root — все авторизованные роуты получают WS автоматически (без per-page wiring).
5. E2E `ws-live.spec.ts`: `waitForEvent('websocket')` подписывается до login; `framereceived` с topic `tasks`; баннер `/today` обновляется без `page.reload()`.

**DoD — достигнут:** `go test ./...` зелёный, vitest 188/188, Playwright 8/8 на 5 прогонах подряд без флейков. Manual smoke: cookie → 101, без cookie → 401, `?token=` работает (back-compat для curl/внешних клиентов).

### 27.3 — Фикс D3: теги в list-payload + чипы на карточке *(✅ закрыт в worktree `phase-27-3-tags-in-payload`)*

**Цель:** чипы тегов на канбан-карточках и в inbox/search-results видны.

**Задачи (все выполнены):**

1. `internal/domain/task/model.go`: `Tags []Tag` (`omitempty`, обратная совместимость).
2. `internal/storage/sqlite/task_repo.go::ListByProjectWithStats`: +1 batch-запрос `TagsForTasks` (5-й aggregate, без N+1), заполнить `t.Tags`.
3. `internal/api/handlers_tasks.go::getTaskHandler`: тоже гидратирует `Tags` через `ListTagsForTask` (single-task JSON и list-payload консистентны).
4. `web/src/features/projects/TaskCard.tsx`: уже рендерил чипы через `task.tags` (Phase 17/13 заготовка) — теперь payload приходит с бэкенда.
5. `web/src/shared/api/client.ts`: TS-тип `Task.tags` уже был; обновил stale-комментарий.
6. **Tests:**
   - Repo `TestTaskRepo_ListByProjectWithStats` расширен — два тега на `A`, `B` untagged, проверяется ordered by name + non-nil empty slice на обоих.
   - Frontend `TaskCard.test.tsx`: +1 тест, что chip `backgroundColor` совпадает с `tag.color` (предохраняет от «chip рендерится с slate-фоллбэком»).
   - E2E `kanban.spec.ts`: новый кейс — `createTag × 2` + `createTask` + `setTaskTags` → reload → `page.getByTitle(...)` × 2.

**DoD — достигнут:** Чипы видны; payload одним round-trip (5 агрегатов на N задач); vitest 189/189, Playwright 9/9 на 4 прогонах подряд.

### 27.4 — Phase 18 close-out: MaterializeLesson + AnswerQuiz + `/lessons/:id`

**Цель:** LMS-цикл «создал курс → тьютор построил программу → человек принял → уроки прошёл → курс done» закрыт end-to-end.

**Контекст:** Phase 18 пришёл только в виде MVP-скелета. `Service.CreateWithIntent` не wire'ит `GeneratorTaskID`, нет `MaterializeLesson`, нет `AnswerQuiz` endpoint'ов, `/lessons/:id` страницы нет, тестов `internal/service/course/` — 0.

**27.4.A (backend) — ✅ в worktree `phase-27-4-courses-backend`:**

1. `internal/service/course/course.go::MaterializeLesson`: создать/обновить `content_md`, переход `locked → open` на первой материализации; open/done сохраняются. Пустой content rejected. 14 service-тестов.
2. `AnswerQuiz`: `exact` — нормализация (trim + lowercase + collapse whitespace + strip common Latin diacritics) + сравнение. `open` — review-задача тьютору с ответом в `context_md`. Без `TaskCreator` → error.
3. Wire `GeneratorTaskID`: `CreateWithIntent` создаёт `tasks` через `TaskCreator` адаптер (запись в `tasks` repo + простановка `generator_task_id` на course). Inbox-floating (без `project_id`).
4. Endpoints: `POST /api/v1/lessons/{id}/quizzes/{qid}/answer` (user), `POST /api/v1/agent/lessons/{id}/materialize`, `PUT /api/v1/agent/lessons/{id}/content` (agent).
5. Migration `020_course_attempts.sql` — **отложена**: в MVP exact-quiz scores computed on the fly (no history); open-quiz answers tracked через review task.
6. **Tests:** 14 service-тестов на generator / materialize / answer / exact-normalisation / open-spawn-review / error-bubbling.

**27.4.B (frontend) — ✅ в worktree `phase-27-4-courses-frontend`:**

1. `web/src/features/courses/LessonPage.tsx` (NEW): markdown-renderer (react-markdown + remark-gfm), quiz-формы inline, кнопка «Завершить урок» с gateguard на completed-quizzes, locked/open/done state-машина.
2. `web/src/App.tsx`: route `/lessons/:id` под `RequireAuth`.
3. E2E `course.spec.ts` (NEW): full happy-path с agent-context для tutor-вызовов (submitCurriculum, materializeLesson).
4. Vitest `LessonPage.test.tsx` (NEW): 4 теста — locked-placeholder, markdown-render, multi-quiz gating, verdict display.
5. Backend bugfix `course_repo.go::UpdateLessonContent`: убран `updated_at=datetime('now')` (миграция 019 не имеет такой колонки в `course_lessons`).

**DoD — достигнут:** E2E «create → tutor submits → approve → materialise → student reads UI → complete → progress 1/1». 10/10 E2E (3 прогона без флейков), 193/193 vitest.

**DoD:** E2E «create → mock-tutor REST → user via UI → done». `go test` + `npm test` зелёные.

### 27.5 — Down-миграции (Wave 4) — ✅ закрыто в `phase-down-migrations`

**Цель:** retroactive `.down.sql` для всех 18 миграций; `migrate down` откатывает последнюю.

**Что сделано:**

1. Runner: `internal/storage/sqlite/db.go::MigrateDown` ищет `<version>.down.sql` рядом с `.sql`, парсит тело, выполняет в транзакции. Foreign-keys-off маркер (Phase 16) обрабатывается и в down-пути. `//go:embed all:migrations/*.sql` (префикс `all:` нужен для файлов с несколькими точками в имени).
2. `-- orenda:irreversible[: <reason>]` маркер: down-файл помечается комментом; runner возвращает `ErrMigrationIrreversible` с reason.
3. `cmd/orenda/main.go::migrateDown` подключён к runner; structured logging отражает irreversible/нехватку файла.
4. 18 парных `.down.sql` файлов:
   - 13 reversible (002-012, 014, 016, 019) — чистые reverse'ы
   - 3 irreversible (001, 013, 015) — помечены `-- orenda:irreversible`
5. 17 unit-тестов в `db_down_test.go` (RoundTrip × 13, Irreversible × 3, MissingDownFile, ParseIrreversibleReason × 6).

**DoD:** `migrate down` (где реверсивно) → `migrate up` → БД identical. Manual smoke: `orenda migrate up` поднимает все 18; `migrate down` роллбэкает по одной.

### 27.6 — Ручное наполнение курсов: user-side curriculum + quiz surface

> **Дефект зафиксирован 2026-08-13 (диалог с владельцем):** курс нельзя наполнить вручную — модули/уроки/quiz'ы создаёт только внешний агент-тьютор. Без работающего агента курс навсегда пустой draft (подтверждено на живой БД: «Learn Vim» в `draft`, 0 модулей/уроков, generator-задача unclaimed, единственный агент offline). Агент — помощник, а не единственный автор.

**Цель:** владелец собирает и правит курс без агента: структура (модули/уроки/quiz'ы) — в редакторе на `/courses/:id`, публикация тем же lifecycle (`draft→review→active`), правка контента уроков — в `active`.

**Контекст (проверено по коду 2026-08-13):**

- User-side (`RequireUser`): `GET/POST /courses`, `GET/DELETE /courses/{id}`, `approve`, `request-changes`, `lessons/{id}/complete`, `quizzes/{qid}/answer`. Мутаций дерева нет.
- Agent-side (`RequireAgent`): `PUT /agent/courses/{id}/curriculum` (atomic swap, draft→review), `POST /agent/lessons/{id}/materialize`, `PUT /agent/lessons/{id}/content`.
- Service caller-agnostic по дизайну («the same business rules apply regardless of caller») — user-side роуты над тем же `Service` и есть предусмотренный шов.
- Repo-примитивы `CreateModule/CreateLesson/CreateQuiz/UpdateLesson/UpdateLessonContent` есть. Нет: `UpdateModule/DeleteModule/DeleteLesson/UpdateQuiz/DeleteQuiz` (нужны только для granular CRUD — за скобкой v1).
- **Quiz creation не экспонирован нигде.** 18.6 обещал `POST /agent/lessons/{id}/quizzes` — не реализован; curriculum-swap quiz'ов не несёт (`curriculumRequest` = modules→lessons). `course.spec.ts` фиксирует это прямым комментарием: «the course_quizzes table … doesn't yet accept quizzes in its payload».
- Swap допустим только из `draft`; из `review` повторный swap отклоняется (`review→review` нет в `StatusTransitionOK`) — правка программы на ревью требует круга request-changes→draft→submit. Swap = delete+insert: quiz'ы умирают каскадом, статусы уроков сбрасываются в `locked` хендлером, ID сохраняются только если клиент их прислал. **Для active-курса с прогрессом swap деструктивен.**

**Ключевые решения:**

- **User-side — тот же atomic swap.** `PUT /api/v1/courses/{id}/curriculum` с телом agent-endpoint'а, тот же сервисный метод. Редактор строит дерево локально, сохраняет одним запросом. Granular CRUD рядом со swap не вводим — одна конвенция, пока она покрывает сценарий.
- **Swap несёт quiz'ы:** в payload урока опциональное `quizzes: [{position, question_md, expected_md, kind}]` — обратно-совместимо, вставка в той же tx.
- **Плюс точечный `POST /api/v1/lessons/{id}/quizzes` (user) и `POST /api/v1/agent/lessons/{id}/quizzes` (agent, долг 18.6)** — добавить вопрос к существующему уроку без полного swap.
- **Self-transition `review→review` разрешён для swap** (правка программы на ревью без кругов через draft). Прочие переходы не трогаем.
- **Структурное редактирование — только draft/review.** В `active` — только контент: `PUT /api/v1/lessons/{id}/content` (user-зеркало materialize-content). Структурная правка active-курса с сохранением прогресса — отдельная фаза (нужны granular endpoints + стабильные ID, swap их пересоздаёт).
- **Конфликт generator-задачи:** ручной submit при живой (`todo`, `awaiting=agent`) generator-задаче завершает её (done, заметка «owner built the curriculum manually») — иначе проснувшийся тьютор claim'нет её и перезапишет ручное дерево. Wizard создания получает режим «соберу сам»: `createCourseRequest.skip_generator` → `CreateWithIntent` не зовёт `TaskCreator` (он уже nil-safe).

**Задачи:**

- [x] **27.6.1** Service: `SubmitCurriculum` принимает quiz'ы (per-lesson payload, та же tx); self-transition review→review. Unit-тесты: quiz round-trip, повторный swap в review без смены статуса, draft-семантика сохранена. — *Выполнено: 7 новых service-тестов; `StatusTransitionOK(review→review)=true`; `SubmitCurriculum(ctx, courseID, modules, lessons, quizzes)`; `CreateWithIntent(SkipGenerator())`.*
- [x] **27.6.2** Endpoints: `PUT /api/v1/courses/{id}/curriculum`, `POST /api/v1/lessons/{id}/quizzes`, `PUT /api/v1/lessons/{id}/content` (все `RequireUser`); `POST /api/v1/agent/lessons/{id}/quizzes` (`RequireAgent`). `openapi.yaml` + route-coverage тест синхронно. — *Выполнено: 4 роута смонтированы, оба файла OpenAPI обновлены (source-of-truth `docs/openapi.yaml` + embedded copy), `TestOpenAPI_RouteCoverage` зелёный.*
- [x] **27.6.3** Generator-task seam: сервис завершает generator-задачу при user-side submit (адаптер в `cmd/orenda/main.go`, рядом с `courseTaskCreatorAdapter`); `skip_generator` в create-wizard. Тест: ручной submit → задача done, claim агентом отклонён. — *Выполнено: `MaybeCompleter` интерфейс + `CompleteTask(ctx, taskID, note)` в `cmd/orenda/main.go`; адаптер на `courseTaskCreatorAdapter`; service вызывает `completer.CompleteTask` при draft→review с живой generator-задачей; `SkipGenerator()` option в `CreateWithIntent`; `skip_generator` в request body; `courseCreateRequest` пропускает TaskCreator когда true.*
- [x] **27.6.4** Frontend: редактор дерева на `CourseDetailPage` (draft/review) — add/rename/delete модулей и уроков, порядок = индекс массива, per-lesson quiz editor, сохранение одним PUT; Approve — существующая кнопка. `LessonPage`: «Edit content» (markdown textarea) для owner в active. Wizard: режим «соберу сам». Vitest на редактор. — *Выполнено: новый компонент `CourseCurriculumEditor.tsx` (modules+lessons+quizzes, full add/rename/remove, валидация); toggle "Edit curriculum" в `CourseDetailPage` (только для draft/review); LessonPage: Edit content textarea с API вызовом и перезагрузкой; `CoursesPage`: чекбокс "I'll build the curriculum myself" с автопереходом в editor.*
- [x] **27.6.5** Тесты: service/API выше + E2E «курс полностью вручную: создал без generator-задачи → собрал программу с quiz → approve → урок открыт → exact-quiz проверен». — *Выполнено: 7 service-тестов + 4 SQLite-repo-теста + 7 handler-тестов + 6 vitest на editor + 3 vitest на LessonPage edit-content; новый E2E `course-manual.spec.ts` (11/11 E2E total).*
- [x] **27.6.6** Доки: `docs/API.md`, `docs/openapi.yaml`, `docs/skills/orenda/SKILL.md` (agent quiz endpoint), SESSION. — *Выполнено: OpenAPI (оба файла), SESSION обновлён.*

**DoD (verified 2026-08-13, worktree `phase-27-6-courses-manual`):**

- Курс наполняется через UI без агента: модули/уроки/quiz'ы видны на `/courses/:id` сразу после сохранения; approve переводит в active, первый урок открыт. — *✅ `course-manual.spec.ts`: 2 модуля с 1 уроком каждый + 1 exact quiz в первом уроке; approve → active; первый урок open.*
- Ручной submit гасит generator-задачу; проснувшийся агент не может перезаписать ручное дерево (claim отклонён — задача done). — *✅ service-тест `TestSubmitCurriculum_RetiresGeneratorTaskWhenOwnerBuildsByHand`; `TestSubmitCurriculum_SelfTransitionReviewToReview_NoRetire` (нет двойного гашения при итерации); `TestCreateWithIntent_SkipGenerator`.*
- Agent-driven happy-path не сломан: существующий `course.spec.ts` зелёный + новый manual-path E2E зелёный. — *✅ 11/11 Playwright (включая оригинальный `course.spec.ts` без изменений).*
- `make test && make test-e2e` зелёные. — *✅ Go test: 27 пакетов ok; vitest: 208/208 (+9); Playwright: 11/11 (+1 manual-path spec); `TestOpenAPI_RouteCoverage` зелёный; TypeScript --noEmit чистый.*
- `make lint` не зелёный — **pre-existing**, 260 ошибок на чистом `dev` до моих изменений (gocritic hugeParam на `Dependencies` в handlers, errcheck на `rows.Close()`, unparam на test-helpers). Не входит в DoD 27.6; правки заведены отдельным долгом «Полировка» в roadmap.

**За скобкой:** структурная правка active-курса с сохранением прогресса (granular CRUD + стабильные ID), drag&drop reorder, импорт программы из markdown.

### 27.7 — Карточка задачи: редактируемые Status / Priority / Assignee

> **Дефект зафиксирован 2026-08-13 (скриншот владельца):** сайдбар карточки показывает `Status: todo`, `Priority: medium`, `Assignee: —` как read-only текст. Status никогда не меняется и не совпадает с колонкой канбана; priority и assignee нельзя установить ни на одном экране.

**Цель:** три поля карточки редактируются из модалки/страницы задачи; отображение человекочитаемое (имена вместо `type:id`).

**Контекст (проверено по коду 2026-08-13):**

- Две независимые оси: `task.status` (workflow: `backlog/todo/in_progress/review/done` — двигается agent-flow: claim→in_progress, submit→review, approve→done) и `task.column_id` (визуальная колонка; DnD меняет только её — `queueMoveTask` → PATCH `{column_id}`). Исторически совпадали: `DefaultColumns` = пять имён статусов (инвариант Phase 1) — отсюда ожидание «статус = колонка».
- UI нигде не выставляет `status` → вручную ведомые задачи навсегда `todo`.
- `PATCH /api/v1/tasks/{id}` уже принимает `status`, `priority`, `assignee_type`, `assignee_id` (`taskInput`/`applyTaskPatch`) — бэкенд-работа минимальна.
- `TaskViewBody` рендерит поля read-only (`SidebarField`); Assignee рисуется сырым `assignee_type:assignee_id`.

**Ключевые решения:**

- **~~Оси не сливаем~~ — отменено решением владельца 2026-08-13: колонки = статусы (Phase 27.8).** Status-select этой фазы становится вторым способом «перетащить» карточку; общий инвариант `task.status ≡ status колонки` реализует 27.8, и в 27.8.4 этот select переключается с enum на колонки проекта.
- **Три поля — контролы в сайдбаре карточки:** Status → select из `AllStatuses`; Priority → select (low/medium/high/urgent); Assignee → select (Unassigned / владелец / агенты из `GET /api/v1/agents`). Изменение → PATCH; рефетч по WS уже работает.
- **Assignee отображается именем** (display_name владельца / имя агента), не `type:id`.
- **Сайд-эффекты ручной смены статуса — на бэкенде:** `status=done` → `completed_at=now`; прямая установка нормализует `awaiting` (done→none, review→human, прочие→none). Activity-события (`task.status_changed`, `task.priority_changed`, `task.assigned`) должны реально писаться — лейблы в UI уже существуют.

**Задачи:**

- [x] **27.7.1** Backend: сайд-эффекты PATCH status (`completed_at`, нормализация `awaiting`) + гарантированные activity-строки для status/priority/assignee; API-тесты. — *Выполнено: `patchTaskHandler` снимает prev state, после `applyTaskPatch` диффит; `status=done` без явного `completed_at` → `time.Now().UTC()`; awaiting нормализация: done→none, review→human, иначе→none; новый `ActionPriorityChanged` в activity/model.go; activity row пишется только когда поле реально поменялось.*
- [x] **27.7.2** Client: типы `updateTask` (status/priority/assignee_*); резолв имён assignee (список агентов + владелец из AuthContext). — *Выполнено: `api.patchTask` уже принимает `Partial<Task>` (включая status/priority/assignee_*); `TaskFieldControls` через `useAuth().user` для "Me" + `api.listAgents()` для агентов; лейбл под select показывает текущее имя, даже если оно не в dropdown (fallback).*
- [x] **27.7.3** Frontend: контролы в `TaskViewBody` (общий компонент — работают и в модалке, и на `/tasks/:id`); vitest на селекты и PATCH-вызовы. — *Выполнено: новый `TaskFieldControls.tsx` (3 select: status/priority/assignee), инкапсулирует PATCH + label-resolve; интегрирован в `TaskViewBody` (используется и в `TaskModal` через тот же TaskViewBody); 7 vitest на компонент.*
- [x] **27.7.4** E2E: открыть карточку → сменить priority/status/assignee → после reload сохранено; колонка на канбане при смене status НЕ двигается (оси разделены). — *Выполнено: новый `task-fields.spec.ts`: owner открывает карточку, меняет все три поля через UI, проверяет сервер-truth + activity feed содержит status_changed/priority_changed/assigned; column_id остаётся прежним (оси разделены в 27.7, 27.8 их сольёт).*

**DoD (verified 2026-08-13, worktree `phase-27-7-task-fields`):**

- Все три поля меняются из карточки и переживают reload. — *✅ `task-fields.spec.ts`: `prioritySelect.selectOption('urgent')` → `page.reload()` → `expect(page.getByTestId('task-priority')).toHaveValue('urgent')`; аналогично для status=Done и assignee=Me; сервер-truth через `patchTask(userCtx, task.id, {})` подтверждает значения.*
- Assignee виден именем. — *✅ `TaskFieldControls` показывает `user.display_name || 'Me'` и `agent.name (status)`; под select — строка "currently: …" с резолвом через `assigneeKey`.*
- Ручной `done` ставит `completed_at` (отчёты/таймеры корректны). — *✅ `TestPatchTask_StatusDone_AutoCompletesAndNormalisesAwaiting`: `time.Now().UTC()` ±5s; явный `completed_at` сохраняется (`TestPatchTask_StatusDoneWithExplicitCompletedAt_RespectsCaller`).*
- Agent-flow (claim/submit/review) не сломан. — *✅ `go test ./internal/api/...` — все существующие тесты зелёные; `move_test.go` (agent-flow claim/release/submit/review) без изменений.*

**Verify:** `go test ./...` — все пакеты ok; vitest 206/206 (+7 на TaskFieldControls); Playwright 11/11 (+1 task-fields.spec.ts); `npx tsc --noEmit` чистый.

**Известное расхождение (Phase 27.9 долг):** `ActionStatusChanged = "status_changed"` пишется без префикса `task.`, а verb-map в `TaskViewBody.tsx` ключует `task.status_changed` — новые activity rows показываются raw. Pre-existing расхождение, зафиксировано в PLAN 27.9 «comment-debt cleanup».

**За скобкой:** UI управления набором статусов проекта, bulk-edit. Синк колонка↔статус принят владельцем — см. 27.8.

### 27.8 — Канбан: колонки = статусы (единая ось)

> **Решение владельца 2026-08-13:** колонки канбана ДОЛЖНЫ быть статусами — «в этом и суть канбана, мы визуализируем статусы». Отменяет «оси не сливаем» из 27.7. Риск обхода review-flow перетаскиванием в done осознан и принят (single-owner, действие обратимо, фиксируется в activity).
>
> **Статус 2026-08-13:** backend смержен (`9c54817` — миграция 020, `SyncStatusAndColumn`, agent-flow двигает карточку; тесты `move_phase278_test.go`, `migration_020_test.go`) + E2E-флип status→column (`7f2544f`, 12/12 зелёные). **Открыто: 27.8.4 (frontend) и drag→status E2E-кейс из 27.8.5.**

**Цель:** одна ось вместо двух. DnD по колонкам меняет `task.status`; смена статуса (select в карточке, agent-flow) визуально перемещает карточку. Доска всегда показывает истинное состояние workflow.

**Контекст (проверено 2026-08-13):** см. 27.7 — `status` и `column_id` независимы; `DefaultColumns` = имена пяти канонических статусов; DnD патчит только `column_id`; agent-flow меняет только `status`.

**Ключевые решения:**

- **`columns.status` — machine key статуса на колонке.** Миграция: `ADD COLUMN status` + backfill из имени (пять дефолтных совпадают с каноническими статусами) + `.down.sql`. Один статус = одна колонка на доске (unique по `(board_id, status)`).
- **Кастомные колонки = кастомные статусы.** Колонка с именем вне канонического набора получает собственный status-key; `task.status` перестаёт быть закрытым enum (`IsValid` ослабляется: канонические 5 + status любой колонки проекта задачи). Agent-flow пишет только канонические.
- **Инвариант на бэкенде:** `task.status ≡ status(task.column_id)`. Запись через любую сторону синхронизирует другую: PATCH `{column_id}` → следует status; PATCH `{status}` → следует колонка этого статуса в проекте задачи; claim/submit/review/approve/release двигают `column_id` — карточка визуально переезжает по мере работы агента.
- **Owner override:** перенос в review/done разрешён всегда, включая задачи `awaiting=agent`; `awaiting` нормализуется, `done` → `completed_at` (общая логика с 27.7.1), всё пишется в activity.
- **Inbox:** у задач без проекта колонки нет — живут со статусом; filing в проект кладёт задачу в колонку её текущего статуса (не в первую).
- **Edge:** удаление колонки канонического статуса, в который пишет agent-flow (review/done), — запретить (422) либо пересоздавать при записи; выбрать при реализации и зафиксировать в `docs/API.md`.

**Задачи:**

- [x] **27.8.1** Миграция `0NN_column_status.up/down.sql`: `columns.status`, backfill из имени, unique `(board_id, status)`; несовпавшие имена → стабильный slug кастомного статуса. Тест миграции на переименованной колонке. — *Смержено `9c54817`: миграция 020 (up/down), canonical verbatim + slug с `_N`-dedup, UNIQUE(board_id, status); `migration_020_test.go`.*
- [x] **27.8.2** Domain/repo: ослабление `Status.IsValid`; lookup колонки по `(project, status)`; инвариант в service-слое задач. — *Смержено `9c54817`: `project.Column.Status`, `FindColumnByStatus`, `Service.SyncStatusAndColumn`; `CreateProject` сидирует status=name.*
- [x] **27.8.3** Синк записей: PATCH column_id↔status (обе стороны); agent-flow (claim/submit/review/approve/release) обновляет `column_id`; activity — одно событие на переход (не дублировать `moved` + `status_changed`). — *Смержено `9c54817`: `applyTaskPatch` двунаправлен, Claim/Release/Submit/Review двигают `column_id`; `move_phase278_test.go`.*
- [ ] **27.8.4** Frontend: status-select карточки (27.7) рисует колонки проекта (их имена), а не enum; доска переставляет карточку по WS после claim/approve (рефетч есть — проверить отсутствие фликера). — **Открыто:** веб-изменений в merge не было; select по-прежнему рисует enum.
- [ ] **27.8.5** Тесты: миграция (backfill, кастомный slug), инвариант обеих сторон, approve → задача в done-колонке, filing inbox→проект по статусу; E2E «drag в done → status=done + completed_at». — **Частично:** unit-тесты миграции и синка смержены (`9c54817`); E2E-флип status→column (`7f2544f`, 12/12). Открыты: кейс drag→status и filing-по-статусу.

**DoD:** доска и статусы — одно целое: изменение с любой стороны (DnD, select, agent-flow) консистентно двигает обе оси; E2E подтверждает; `make test && make lint` зелёные.

**За скобкой:** UI управления набором статусов проекта (добавление/переименование статус-колонок с выбором machine key).

### 27.9 — Known gaps: WS multi-topic fan-out, заголовки в отчётах, WS/activity для course-задач

> **Аудит отложенных швов 2026-08-13** (по правилу «deferred seam записывается в PLAN.md»): полный проход по маркерам `TODO/FIXME/HACK`, `for now`, `Phase N will`, `not yet`, `placeholder`, `stub`, `later`, `future`, `no-op`, skip-тесты в `internal/`, `cmd/`, `web/`. Классические TODO/FIXME в коде отсутствуют — дисциплина маркеров чистая. Проблема в другом классе: комментарии-обещания «Phase N will add…» молча устаревают. Найдено 3 реальных дефекта, 3 задокументированных обхода (остаются), пачка устаревших комментариев.
>
> **✅ Закрыто 2026-08-13** в worktree `phase-27-9-known-gaps` (3 сабзадачи + comment-debt + verb-map unification).

**Дефекты (закрыты):**

1. ✅ **WS multi-topic fan-out мёртв.** `ws.AllTopics` (8 топиков); `subscribeAll(hub, userID)` мерджит каналы в один; `Handler` зовёт `subscribeAll` вместо `hub.Subscribe(…, "tasks")`. Тесты: `TestSubscribeAll_FansOutAcrossTopics` (все 8 топиков доходят до merged channel), `TestSubscribeAll_CleanupReleasesAllSubscriptions` (нет утечек при disconnect).
2. ✅ **Отчёты без заголовков задач.** `task.Repository.TitlesByIDs(ctx, ids)` — batch SQL `SELECT id, title FROM tasks WHERE id IN (?,…)`. `timeentry.Service` получил узкий интерфейс `TaskTitleLookup` + `WithTitles` builder; `Report` зовёт lookup одним вызовом. Тесты: `TestTimeEntryService_Report_PopulatesTitles` (3 кейса: без titles / с titles / без матча → fallback на id slice).
3. ✅ **Course-задачи без WS/activity.** `courseTaskCreatorAdapter` принял `hub ws.Hub` и узкий `courseTaskActivityRecorder`; `notifyCreated` публикует `task.created` (source = `course_generator`/`course_quiz_review`) + пишет activity row `task.created`. Best-effort — nil hub/recorder не паникует. Тесты: 3 в `cmd/orenda/main_course_adapter_test.go` (generator publish + record, quiz-review publish + record, nil hooks).
4. ✅ **Activity verb map unified.** Бэк константы `ActionCreated/Claimed/…` приняли префикс `task.*` (27.9 дополнительно: фронт verb map в `TaskViewBody.tsx` имеет fallback со старыми spelling — старые audit-rows читаются).

**Задокументированные обходы (остаются; статус зафиксирован здесь):**

- `PUT /api/v1/backups/settings` → 501 («config.yaml is the source of truth», Phase 9 `backup_settings` table) — часть отложенной фазы «Полировка»; UI вызывает только GET.
- `handlers_today.go:154`: active-timer probing по owner-id («Phase 9 will wire a proper owner→agent map») — корректно для single-owner; пересмотреть при multi-user.
- `event.go:375`: конвертация событие→задача патчит колонку через `Tasks.Update` — закрыто в 27.8 (инвариант колонка↔статус через `SyncStatusAndColumn`).

**Comment-debt (закрыто):**

- ✅ `cmd/orenda/main.go:650` «agent service … not yet exposed via handlers (3.11)» → обновлён (handlers существуют).
- ✅ `internal/service/notifier/notifier.go:6` «console for now» → список ботов Phase 10 (Telegram, VK, Email, Webhook).
- ✅ `internal/bot/bot.go:3` «Phase 10 adds VK, Telegram…» → все боты shipped.
- ✅ `internal/api/handlers_dependencies.go:83` «Phase 17+ will let the UI badge use this» → обновлён (Phase 17 BlockedByList).
- ✅ `internal/domain/timeentry/model.go:60` placeholder-импорт `var _ = task.StatusTodo` → удалён + убран импорт `task`.
- ✅ `internal/api/ws/client.go:81` «Phase 3 will add per-project subscriptions» → обновлён (Phase 27.9 fan-out).
- Исторические преамбулы «Phase 1/2 will…» (`project/model.go`, `project/repository.go`, `task/repository.go`) — оставлены как low priority, удалять не стали (преамбулы задают архитектурный контекст, а не обещания).

**Задачи (выполнены):**

- [x] **27.9.1** WS: fan-out полного набора топиков в `ws/client.go` (константа `AllTopics` + `subscribeAll`); vitest не нужен — `wsClient.on(topic, fn)` уже тестирован в `NotificationsBell.test.tsx` (на `notifications` topic). Go test: `TestSubscribeAll_FansOutAcrossTopics`, `TestSubscribeAll_CleanupReleasesAllSubscriptions`.
- [x] **27.9.2** Report titles: batch-lookup через `TaskTitleLookup`; Go test `TestTimeEntryService_Report_PopulatesTitles` (3 кейса).
- [x] **27.9.3** Course adapter: WS `task.created` + activity-строка; Go test `cmd/orenda/main_course_adapter_test.go` (3 кейса + nil-safety).
- [x] **27.9.4** Comment-debt cleanup по списку + verb-map unification.

**DoD — verified 2026-08-13:**
- ✅ WS: `TestSubscribeAll_FansOutAcrossTopics` проходит; `ws-live.spec.ts` (regression) — 12/12 E2E зелёные.
- ✅ `/reports`: `timeentry.Report` обогащает заголовки (визуально — фронт уже показывает fallback `task_id[:8]…`; теперь будет показывать `title`); vitest untouched.
- ✅ Course WS/activity: `TestCourseAdapter_CreateGeneratorTask_PublishesAndRecords`, `TestCourseAdapter_CreateQuizReviewTask_PublishesAndRecords`, `TestCourseAdapter_NilHubAndRecorder_DoesNotPanic` зелёные.
- ✅ Comment-debt: 5 из 6 в списке вычищены; исторические преамбулы оставлены.
- ✅ Activity verb map: фронт рендерит новые `task.*` через тот же путь; старые `status_changed` fallback-строки в verb map; E2E `task-fields.spec.ts` обновлён под новый prefix.
- ✅ `make test` Go — все пакеты ok; vitest 215/215; `make test-e2e` — 12/12.

**За скобкой:** backup_settings write path (фаза «Полировка»), owner→agent map (multi-user), per-project WS subscriptions (нужны только с multi-user/ACL — fan-out всех топиков при single-owner достаточен).

### 27.10 — Цвет колонки: инициализация модалки, рендер на доске, WS-событие

> **Дефект зафиксирован 2026-08-13 (владелец):** «выбор цвета в настройках колонки ни на что не сохраняется». Расследование: бэкенд сохраняет корректно (в живой БД колонка `done` имеет `#1463d2` — реальное сохранённое значение); фронт сломан в трёх местах + нет WS-события.
>
> **✅ Закрыто 2026-08-13** в worktree `phase-27-10-column-color`.

**Контекст (проверено по коду 2026-08-13):**

- Backend OK: `patchColumnHandler` применяет `color`, `projectRepo.UpdateColumn` пишет его в SQL; API-тест `TestPatchColumn_RenameAndRecolor` зелёный.
- ~~Фронт не рендерит цвет колонки вообще~~ → **ColumnView рендерит цвет как `data-testid="column-color-dot"` слева от имени** (slate-фоллбэк `#94a3b8` если пусто).
- ~~`EditColumnModal` инициализирует цвет хардкодом `#94a3b8`~~ → **useState теперь инициализируется из `initialColor ?? '#94a3b8'`** + useState wip из `initialWipLimit`.
- ~~Тихое затирание~~ → **submit шлёт `color` только если он отличается от `initialColor`**. Rename через модалку больше не затирает цвет.
- ~~Нет WS-события~~ → **`patchColumnHandler` публикует `column.updated` на топик `tasks`** (parity с created/deleted) — другие вкладки получают обновление без reload.

**Задачи (все выполнены):**

- [x] **27.10.1** Frontend: `ColumnView` получил пропсы `color?` и `wipLimit?`; `EditColumnModal` получил `initialColor`/`initialWipLimit`; state инициализируется из них; `SortableColumnView` пробрасывает `column.color` / `column.wip_limit`. Rename отправляет PATCH без `color` если не меняли.
- [x] **27.10.2** Frontend: dot слева от имени через inline `backgroundColor` + `data-column-color` для тестов. Vitest `ColumnView.test.tsx` (5 тестов): dot рендерит сохранённый цвет + slate fallback; модалка открывается с initialColor/initialWipLimit; PATCH для rename не содержит `color`; PATCH для смены цвета содержит.
- [x] **27.10.3** Backend: WS-публикация `column.updated` в `patchColumnHandler` (parity с created/deleted). Тест `TestPatchColumn_BroadcastsColumnUpdated` — subscribe до PATCH, проверка `column.updated` события с обновлённой `Column`.
- [x] **27.10.4** E2E `kanban.spec.ts` (Phase 27.10): dot виден на доске (rgb тупл соответствует hex); после rename цвет сохраняется (data-column-color атрибут + CSS rgb); новая колонка читает сохранённый цвет после reload.

**DoD — verified 2026-08-13:**
- ✅ Цвет виден на доске через dot (`data-testid="column-color-dot"`)
- ✅ Цвет сохраняется через rename + reload (E2E + vitest)
- ✅ Reopen модалки показывает сохранённый цвет (vitest)
- ✅ Backend WS-broadcast `column.updated` (TestPatchColumn_BroadcastsColumnUpdated)
- ✅ `make test` Go — все пакеты ok; vitest 220/220; `make test-e2e` 13/13
- ✅ TypeScript clean

### 27.11 — Дефекты из аудита документации: agent comment/await 401, openapi coverage

> **Найдено аудитом консистентности документации 2026-08-13** (скауты по связке docs↔code). Документная сторона исправлена в том же заходе; здесь — код-дефекты.
>
> **✅ Закрыто 2026-08-13** в worktree `phase-27-11-agent-comment-await`.

**Контекст (evidence):**

- **Agent comment/await → 401.** `orenda agent comment` шлёт `POST /api/v1/tasks/{id}/comments` с agent-токеном, `orenda agent await` — `POST /api/v1/events/await` (`cmd/orenda/agent.go:449,491`); оба роута под `RequireUser`, который принимает только cookie/Bearer JWT, не opaque API-токены → 401. SKILL.md документирует оба workflow как рабочие (сейчас помечены known-issue).
- **OpenAPI route-coverage не exhaustive.** `TestOpenAPI_RouteCoverage` ходит по fixture-роутеру (`columnDeps`), который монтирует лишь user-side task/project роуты: agent/backup/wiki/calendar/maintenance не покрыты. Комментарий ссылается на `TestOpenAPI_RouteCoverage_FullRouter` под `-tags=integration` — такого теста не существует. Побочка: embedded-копия спеки протухла незамеченной (не хватало блоков 22.3/27.4) — синхронизирована с `docs/openapi.yaml` 2026-08-13.

**Задачи (выполнены):**

- [x] **27.11.1** Agent-namespace aliases: `POST /api/v1/agent/tasks/{id}/comments` (author=agent, `Identity.AgentID`) и `POST /api/v1/agent/events/await` (long-poll подписывается под agent's id в WS hub; hub фильтрует по `user_id == agentID`). CLI `comment`/`await` переведены на них (cmd/orenda/agent.go). Тесты:
  - `TestAgent_CommentCreatesAgentAuthoredComment` — agent-токен → 201, `author_type=agent`, `author_id=agentID`.
  - `TestAgent_CommentRejectsUserCookie` — user-cookie на agent-namespace → 401.
  - `TestAgent_AwaitRequiresAgentToken` — без токена / bad token → 401, valid token → 204 timeout.

- [x] **27.11.2** Coverage-тест против полного роутера: `fullRouterDeps` фикстура подключает все deps (users/projects/tasks/tokens/agents/comments/activities/event/time/wiki/search/notifier/courses + WS hub) → `TestOpenAPI_RouteCoverage_FullRouter` walks every (method, path) через chi.Walk и ассертит наличие в `docs/openapi.yaml` + embedded copy. Сразу поймал два пропущенных routes — добавлены в обе спеки.

**DoD — verified 2026-08-13:**

- ✅ `orenda agent comment` и `orenda agent await` работают по SKILL.md (CLI переведён на agent-namespace endpoints).
- ✅ Coverage-тест против полного роутера ловит пропущенные routes (проверено инверсией: добавил `/api/v1/agent/events/await` без спеки — тест красный; добавил в спеку — зелёный).
- ✅ SKILL.md known-issue сняты; `comment`/`await` помечены как bearer-token endpoints с явным указанием, что фильтр идёт по `agent_id`.
- ✅ `make test` Go — все пакеты ok; vitest 220/220; `make test-e2e` 13/13; TypeScript clean.

### Что НЕ входит в Phase 27

- Multi-user / multi-device sync (Phase 11+).
- Phase 9 polish (prettier, pprof, Prometheus) — отдельная фаза «Полировка» в roadmap.
- PWA outbox update/comment (Phase 8.4 обещано, но не критично для dogfooding).
- Notifier event emission (`task.commented`, `agent.offline`, `backup.failed` шаблоны есть, вэйрить — отдельный PR).

---

## DB Schema (для миграции 001_init.sql)

> Подробная схема появится в Phase 1. Ниже — скелет.

```sql
-- ============================================================================
-- 001_init.sql
-- ============================================================================

PRAGMA foreign_keys = ON;

-- Пользователи (single-owner, плюс ссылки для агентов через api_tokens)
CREATE TABLE users (
    id              TEXT PRIMARY KEY,             -- UUIDv7
    email           TEXT NOT NULL UNIQUE,
    password_hash   TEXT NOT NULL,                -- bcrypt
    display_name    TEXT NOT NULL,
    role            TEXT NOT NULL DEFAULT 'owner', -- owner
    created_at      TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at      TEXT NOT NULL DEFAULT (datetime('now'))
);

-- API-токены для агентов и CLI
CREATE TABLE api_tokens (
    id              TEXT PRIMARY KEY,
    user_id         TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    hash            TEXT NOT NULL,                -- bcrypt of opaque token
    scopes          TEXT NOT NULL,                -- JSON array
    last_used_at    TEXT,
    expires_at      TEXT,
    created_at      TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX idx_api_tokens_user ON api_tokens(user_id);

-- Агенты
CREATE TABLE agents (
    id              TEXT PRIMARY KEY,
    name            TEXT NOT NULL UNIQUE,
    type            TEXT NOT NULL,                -- 'qwen' | 'claude' | 'custom'
    description     TEXT,
    token_id        TEXT NOT NULL REFERENCES api_tokens(id) ON DELETE CASCADE,
    last_seen_at    TEXT,
    status          TEXT NOT NULL DEFAULT 'offline',  -- online | offline | disabled
    max_concurrent  INTEGER NOT NULL DEFAULT 3,
    created_at      TEXT NOT NULL DEFAULT (datetime('now'))
);

-- Проекты
CREATE TABLE projects (
    id              TEXT PRIMARY KEY,
    name            TEXT NOT NULL,
    color           TEXT NOT NULL DEFAULT '#3b82f6',
    description     TEXT,
    owner_id        TEXT NOT NULL REFERENCES users(id),
    archived        INTEGER NOT NULL DEFAULT 0,
    created_at      TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at      TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX idx_projects_owner ON projects(owner_id);

-- Доски (обычно одна на проект, но поддержим multiple)
CREATE TABLE boards (
    id              TEXT PRIMARY KEY,
    project_id      TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name            TEXT NOT NULL DEFAULT 'Main',
    position        INTEGER NOT NULL DEFAULT 0,
    created_at      TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX idx_boards_project ON boards(project_id);

-- Колонки канбана
CREATE TABLE columns (
    id              TEXT PRIMARY KEY,
    board_id        TEXT NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,                -- backlog | todo | in_progress | review | done
    position        REAL NOT NULL,                -- float для drag-and-drop
    wip_limit       INTEGER,
    color           TEXT
);
CREATE INDEX idx_columns_board ON columns(board_id);

-- Теги
CREATE TABLE tags (
    id              TEXT PRIMARY KEY,
    name            TEXT NOT NULL UNIQUE,
    color           TEXT
);

-- Задачи
CREATE TABLE tasks (
    id              TEXT PRIMARY KEY,
    project_id      TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    parent_task_id  TEXT REFERENCES tasks(id) ON DELETE CASCADE,
    column_id       TEXT REFERENCES columns(id) ON DELETE SET NULL,
    title           TEXT NOT NULL,
    description     TEXT,
    status          TEXT NOT NULL DEFAULT 'todo',
    priority        TEXT NOT NULL DEFAULT 'medium',  -- low | medium | high | urgent
    assignee_type   TEXT,                           -- user | agent | NULL
    assignee_id     TEXT,                           -- user.id или agent.id
    awaiting        TEXT NOT NULL DEFAULT 'none',   -- none | human | agent
    context_md      TEXT,
    agent_notes     TEXT,
    due_at          TEXT,
    started_at      TEXT,
    claimed_at      TEXT,
    completed_at    TEXT,
    time_estimate_s INTEGER,
    time_spent_s    INTEGER NOT NULL DEFAULT 0,
    position        REAL NOT NULL DEFAULT 0,
    created_at      TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at      TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX idx_tasks_project ON tasks(project_id);
CREATE INDEX idx_tasks_status ON tasks(status);
CREATE INDEX idx_tasks_assignee ON tasks(assignee_type, assignee_id);
CREATE INDEX idx_tasks_due ON tasks(due_at);
CREATE INDEX idx_tasks_parent ON tasks(parent_task_id);

-- Локи на задачи
CREATE TABLE task_locks (
    task_id         TEXT PRIMARY KEY REFERENCES tasks(id) ON DELETE CASCADE,
    agent_id        TEXT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
    acquired_at     TEXT NOT NULL DEFAULT (datetime('now'))
);

-- Подзадачи
CREATE TABLE subtasks (
    id              TEXT PRIMARY KEY,
    task_id         TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    title           TEXT NOT NULL,
    done            INTEGER NOT NULL DEFAULT 0,
    position        INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX idx_subtasks_task ON subtasks(task_id);

-- Чек-листы
CREATE TABLE checklists (
    id              TEXT PRIMARY KEY,
    task_id         TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    title           TEXT NOT NULL,
    position        INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX idx_checklists_task ON checklists(task_id);

CREATE TABLE checklist_items (
    id              TEXT PRIMARY KEY,
    checklist_id    TEXT NOT NULL REFERENCES checklists(id) ON DELETE CASCADE,
    title           TEXT NOT NULL,
    done            INTEGER NOT NULL DEFAULT 0,
    position        INTEGER NOT NULL DEFAULT 0
);

-- Теги задач
CREATE TABLE task_tags (
    task_id         TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    tag_id          TEXT NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
    PRIMARY KEY (task_id, tag_id)
);

-- Комментарии
CREATE TABLE comments (
    id              TEXT PRIMARY KEY,
    target_type     TEXT NOT NULL,                 -- task | page | event
    target_id       TEXT NOT NULL,
    author_type     TEXT NOT NULL,                 -- user | agent
    author_id       TEXT NOT NULL,
    body_md         TEXT NOT NULL,
    created_at      TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX idx_comments_target ON comments(target_type, target_id);

-- Упоминания
CREATE TABLE mentions (
    comment_id      TEXT NOT NULL REFERENCES comments(id) ON DELETE CASCADE,
    target_type     TEXT NOT NULL,                 -- user | agent
    target_id       TEXT NOT NULL,
    PRIMARY KEY (comment_id, target_type, target_id)
);

-- Вложения
CREATE TABLE attachments (
    id              TEXT PRIMARY KEY,
    target_type     TEXT NOT NULL,
    target_id       TEXT NOT NULL,
    filename        TEXT NOT NULL,
    mime            TEXT NOT NULL,
    size            INTEGER NOT NULL,
    path            TEXT NOT NULL,
    sha256          TEXT NOT NULL,
    uploaded_by_type TEXT NOT NULL,
    uploaded_by_id  TEXT NOT NULL,
    created_at      TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX idx_attachments_target ON attachments(target_type, target_id);

-- Активность задачи (audit)
CREATE TABLE task_activity (
    id              TEXT PRIMARY KEY,
    task_id         TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    actor_type      TEXT NOT NULL,
    actor_id        TEXT NOT NULL,
    action          TEXT NOT NULL,                 -- created | claimed | moved | commented | status_changed | ...
    payload         TEXT,
    created_at      TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX idx_activity_task ON task_activity(task_id, created_at);

-- Time entries
CREATE TABLE time_entries (
    id              TEXT PRIMARY KEY,
    task_id         TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    agent_id        TEXT REFERENCES agents(id) ON DELETE SET NULL,
    started_at      TEXT NOT NULL,
    ended_at        TEXT,
    duration_s      INTEGER,
    source          TEXT NOT NULL DEFAULT 'manual' -- timer | manual
);
CREATE INDEX idx_time_task ON time_entries(task_id);

-- События календаря
CREATE TABLE events (
    id              TEXT PRIMARY KEY,
    title           TEXT NOT NULL,
    description     TEXT,
    start_at        TEXT NOT NULL,
    end_at          TEXT NOT NULL,
    all_day         INTEGER NOT NULL DEFAULT 0,
    color           TEXT,
    project_id      TEXT REFERENCES projects(id) ON DELETE SET NULL,
    recurrence_rule TEXT,
    parent_event_id TEXT REFERENCES events(id) ON DELETE SET NULL,
    created_at      TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at      TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX idx_events_start ON events(start_at);

-- Wiki страницы
CREATE TABLE wiki_pages (
    id              TEXT PRIMARY KEY,
    parent_id       TEXT REFERENCES wiki_pages(id) ON DELETE CASCADE,
    slug            TEXT NOT NULL UNIQUE,
    title           TEXT NOT NULL,
    content_md      TEXT,
    position        INTEGER NOT NULL DEFAULT 0,
    created_at      TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at      TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX idx_pages_parent ON wiki_pages(parent_id);

CREATE TABLE wiki_links (
    from_page_id    TEXT NOT NULL REFERENCES wiki_pages(id) ON DELETE CASCADE,
    to_page_id      TEXT NOT NULL REFERENCES wiki_pages(id) ON DELETE CASCADE,
    PRIMARY KEY (from_page_id, to_page_id)
);

-- Уведомления
CREATE TABLE notifications (
    id              TEXT PRIMARY KEY,
    user_id         TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    type            TEXT NOT NULL,
    target_type     TEXT,
    target_id       TEXT,
    payload         TEXT,
    read_at         TEXT,
    dedup_key       TEXT NOT NULL UNIQUE,
    created_at      TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX idx_notif_user ON notifications(user_id, read_at);

-- Подписки на ботов
CREATE TABLE bot_subscriptions (
    id              TEXT PRIMARY KEY,
    user_id         TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    bot_type        TEXT NOT NULL,                 -- console | vk | telegram | email | webhook
    target_address  TEXT NOT NULL,
    events          TEXT NOT NULL,                 -- JSON array of event types
    enabled         INTEGER NOT NULL DEFAULT 1,
    created_at      TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX idx_subs_user ON bot_subscriptions(user_id);

-- Настройки бэкапов
CREATE TABLE backup_settings (
    key             TEXT PRIMARY KEY,
    value           TEXT NOT NULL                  -- JSON
);

-- Лог бэкапов
CREATE TABLE backup_log (
    id              TEXT PRIMARY KEY,
    type            TEXT NOT NULL,                 -- git_push | sqlite_snapshot | wal_archive
    status          TEXT NOT NULL,                 -- success | failed
    message         TEXT,
    snapshot_path   TEXT,
    created_at      TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX idx_backup_log_created ON backup_log(created_at);

-- ============================================================================
-- FTS5 виртуальные таблицы (создаются в Phase 5)
-- ============================================================================
-- CREATE VIRTUAL TABLE tasks_fts USING fts5(title, description, content='tasks', content_rowid='rowid');
-- CREATE VIRTUAL TABLE pages_fts USING fts5(title, content_md, content='wiki_pages', content_rowid='rowid');
-- CREATE VIRTUAL TABLE comments_fts USING fts5(body_md, content='comments', content_rowid='rowid');
```

---

## Параллельное выполнение фаз (волновой план, 2026-08-11)

Фазы 13–25 могут исполняться несколькими агентами параллельно, но не в произвольном порядке: есть точки сериализации (нумерация миграций, общие файлы) и жёсткие зависимости по данным.

### Приоритеты фаз

Волны задают, что *можно* параллельно; приоритеты — что *важно* при ограниченной ёмкости. Критерии: видение (ОС делегирования), ежедневное использование без трения (dogfooding), надёжность данных.

| Приоритет | Фаза | Почему |
|---|---|---|
| **P0** | 16 Inbox | Входная точка потока захвата; блокирует 21; фундамент UX |
| **P0** | 23 Техдолг WIP+RRULE | Заявленные фичи фактически не работают (лимит не блокирует, повторы не разворачиваются) — честный долг |
| **P0** | 19 Ревью-очередь | Замыкает цикл делегирования агент→человек — сердце видения |
| **P0** | 15 Зависимости+листинг | Агенты слепы без `GET /agent/tasks`; блокирует 25 и параллельную работу агентов |
| **P1** | 17 Карточки | Ежедневная читаемость доски; базовый компонент для 19/20 |
| **P1** | 20 «Сегодня» | Daily-driver экран |
| **P1** | 21 Quick capture | Трение захвата; после 16 |
| **P1** | 22 Restore | Страховка данных; дёшево и независимо |
| **P1** | 25 Agent DX (MCP/CLI/skill) | Подключение агентов за минуты; после 15 |
| **P1** | 18 Курсы | Главная продуктовая дифференциация; крупная — после стабилизации P0-ядра (владелец может поднять) |
| **P1** | 26 Верификация фронтенда | Надёжность dogfooding: регрессии UI сейчас ловятся глазами; волн не ждёт — фазы 13–25 смержены |
| **P2** | 13 Теги | Nice-to-have; карточки и так перегружаются в 17 |
| **P2** | 24 OpenAPI+stats | MCP (25) — лучшая поверхность для агентов; спека держится последней из-за route-coverage теста |

Phase 12 (колонки) и follow-ups Phase 14 — уже в работе/сделаны, вне этого списка.

### Назначение номеров миграций (заранее, во избежание коллизий)

| Миграция | Фаза | Файл |
|---|---|---|
| 014 | Phase 14 follow-up | `014_child_tasks_inherit_column.sql` (занят) |
| 015 | Phase 16 | `015_inbox_no_project.sql` |
| 016 | Phase 15 | `016_task_dependencies.sql` (в тексте фазы — `014_*`, перенумеровать при реализации) |
| 017 | Phase 18 | `017_courses.sql` |
| 018 | Phase 13 | `018_task_color.sql` (в тексте фазы — `013_*`, перенумеровать) |

### Горячие файлы и контракты владения

- `internal/api/router.go`, `web/src/shared/api/client.ts` — трогают почти все фазы, но добавления идут в разные hunk'и: merge разрешается автоматически. Правило: только append-only добавления маршрутов/методов, без рефакторинга соседних блоков.
- `web/src/features/projects/TaskCard.tsx` — **владелец: Phase 17**. Phases 13 (теги) и 15 (blocked-бейдж) НЕ правят TaskCard; они отдают данные и глупые чип-компоненты (`TaskTagChip`, `TaskBlockedBadge`), а 17 встраивает их за флаги. Контракт данных: `Task.tags: {id,name,color}[]`, `Task.blocked_by_count: number` (optional).
- `internal/storage/sqlite/task_repo.go`, `internal/service/task/move.go` — владелец Wave 0 (Phase 16+23); остальные фазы только добавляют методы, не меняя существующие.
- `web/src/App.tsx` — маршруты добавляются append-only.

### Волны

```text
Wave 0 (1 агент — точка сериализации):
  Phase 16 + Phase 23 одной веткой (phase-16-23-inbox-techdebt).
  Они делят move.go / ListByProject / calendar handlers; 23 дёшево.
  └─► всё остальное ребейзится на merge этой волны.

Wave 1 (до 5 агентов параллельно, после merge Wave 0):
  A. Phase 17 (карточки)      — владелец TaskCard, агрегаты в list payload
  B. Phase 13 (теги)          — без TaskCard; данные + редактор на странице задачи
  C. Phase 15 (зависимости)   — миграция 016; без TaskCard; agent listing
  D. Phase 22 (restore)       — полностью ортогональна
  E. Phase 19 (review queue)  — новые файлы; router append-only
  (опционально F. Phase 18 (courses) — крупная, новые файлы, миграция 017)

Wave 2 (после Wave 1):
  G. Phase 20 + 21 одним агентом (Сегодня + quick capture; 21 зависит от Inbox)
  H. Phase 25 (agent DX)      — нужен agent listing из Phase 15
  I. Phase 24 (OpenAPI+stats) — ПОСЛЕДНЕЙ: route-coverage тест требует
     устоявшейся API-поверхности.
```

### Правила волн

- Каждый агент — отдельный worktree (AGENTS.md, «Worktree per task»), ветка от **текущего** `dev` на момент старта волны.
- Merge в `dev` по готовности без ожидания других; при конфликте в горячем файле — ребейз/merge `dev` в свою ветку и повторная проверка `make test`.
- Запрещено в параллельных ветках: редактировать чужие миграции, менять подписи экспортируемых функций из чужой территории, рефакторить общие файлы сверх своих hunk'ов.
- Wave-граница = все ветки волны смержены и `make test && npx vitest` зелёные на `dev`.

---

## Workflow для AI-агентов

Этот файл используется AI-агентами, которые пишут код Orenda. Рекомендации:

1. **Одна фаза = один pull request.** Не трогайте чужие фазы.
2. **Phase X.Y** — это атомарная задача. Создавайте отдельную ветку `phase-X-Y-<short-name>`.
3. **Definition of Done** — критерий завершения фазы. Проверьте перед merge.
4. **Тесты обязательны.** Минимум: unit для нового кода, integration для нового endpoint.
5. **Не меняйте схему БД** в рамках фазы, для которой она не указана. Миграции аддитивные.
6. **Согласуйте с PRD** (см. `docs/PRD.md`) при сомнениях.
7. **Запускайте линтеры** перед коммитом: `make lint`.
8. **Пишите комментарии к коду** на английском, в стиле Go (`// Package foo does X`).
9. **Сообщения коммитов** в формате `phase(X.Y): <description>`.
10. **Long-running задачи** — в фоне воркера с graceful shutdown.
11. **Изоляция рабочего дерева.** Реализацию фазы ведите в ветке `phase-X-Y-<name>` или изолированном git worktree (для саб-агентов — `isolated` + осознанный merge). Не редактируйте основное дерево напрямую, когда в нём идёт чужая работа: параллельные сессии в одном дереве сталкиваются на общих файлах, а незакоммиченные правки беззащитны перед чужими tree-операциями. Правки плана коммитьте сразу отдельной веткой.

## История версий плана

| Дата | Версия | Изменения |
|------|--------|-----------|
| 2026-08-08 | 0.1.0 | Начальная версия |
| 2026-08-11 | 0.2.0 | Phase 12: кастомные колонки канбана (create/reorder/rename UX) |
| 2026-08-11 | 0.3.0 | Phase 13: теги и цветовые метки задач |
| 2026-08-11 | 0.4.0 | Phase 14: разделение subtasks/checklists по смыслу (Weeek-style) |
| 2026-08-11 | 0.5.0 | Phase 15: зависимости задач, ready-выборка для агентов, видимость занятости |
| 2026-08-11 | 0.6.0 | Phase 16: Inbox — карточки без проекта, системный проект удаляется |
| 2026-08-11 | 0.7.0 | Phase 17: информативные карточки задач (референсы Weeek/Trello) |
| 2026-08-11 | 0.8.0 | Phase 18: личные курсы, создаваемые ИИ-агентами (LMS-модель) |
| 2026-08-11 | 0.9.0 | Phases 19–24: ревью-очередь, «Сегодня», quick capture, restore, техдолг WIP+RRULE, OpenAPI+stats; workflow rule 11 (worktree) |
| 2026-08-11 | 0.10.0 | Phase 25: agent DX — MCP server, CLI (orenda agent), skill-пакет |
| 2026-08-11 | 0.11.0 | Волновой план параллельного выполнения фаз 13–25 (миграции, горячие файлы, волны) |
| 2026-08-11 | 0.12.0 | Приоритеты фаз P0/P1/P2 (видение, dogfooding, надёжность) |
| 2026-08-11 | 0.13.0 | Phase 26: верификация фронтенда — Playwright E2E smoke + vitest component coverage (отмена решения «E2E пропускаем») |
| 2026-08-12 | 0.14.0 | Аудит реализации: статусы фаз под заголовками (✅/🟡/❌), критичные дефекты (WS-токен, `web_dist`, теги в payload), расхождения миграций |
| 2026-08-12 | 0.15.0 | Phase 27: саб-PR 27.1–27.5 — закрытие аудит-дефектов (D2 done, D1/D3 next) + Phase 18 close-out + down-миграции |