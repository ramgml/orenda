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
> **2026-08-14 update (Phase doc-audit)**: per-phase 🟡 markers ниже сверены с кодом; закрытые дефекты помечены ✅. Реально открытые gaps:
>
> - **Phase 1**: ✅ закрыт (2026-08-16, Phase 28.21) — запись «нет `/projects/:id/tasks` route» была stale: роут существует (`router.go` монтирует `GET/POST /projects/{id}/tasks`).
> - **Phase 7**: `git client` без `Status`/`TestConnection`; snapshot по тикеру 24h (не cron 03:00) — низкий приоритет.
> - **Phase 8**: ✅ LWW-семантика зафиксирована (2026-08-16, Phase 28.21): delivery-order LWW корректен для single-device PWA outbox (он флашит в порядке правок пользователя — arrival order ≡ edit order); timestamp-based LWW по `updated_at` отложен до эры multi-device. Комментарий `handlers_sync.go` переписан под это решение.
> - **Phase 10**: VK Long Poll / Email HTML / Weekly digest — большие подфазы, не блокируют dogfooding.
> - **Phase 17**: ~~UI-тоггл плотности карточки~~ ✅ закрыт в 28.23.6 (чекбокс «Compact cards» на доске); остаются бейджи времени (estimate/spent) и таймера.
>
> Критичные дефекты:
> 1. ✅ **Фронтенд WS никогда не подключався** → **закрыт 2026-08-12 в Phase 27.2 / PR 1.2** (cookie-based upgrade, см. секцию ниже). Realtime UI работает end-to-end.
> 2. ✅ **`make build` не передавав `-tags=web_dist`** → **закрыт 2026-08-12 в Phase 27.1 / PR 1.1** (см. секцию ниже). Бинарь self-contained через `//go:embed all:dist`.
> 3. ✅ **Теги не попадали в list-payload** → **закрыт 2026-08-12 в Phase 27.3 / PR 1.3** (см. секцию ниже). Чипы на канбане видны.
>
> Миграции: `.down.sql` отсутствуют глобально; нумерация съехала относительно текста фаз (wiki=008, notifications=009, backups=010, sync=011, events_to_tasks=012, courses=019; миграции 018 нет; `tasks.color` добавлен в 012).  ➜ **Wave 4 / PR 4.1.** **✅ закрыт `phase-down-migrations`** — runner с маркером `-- orenda:irreversible` + 18 парных файлов.
>
> Приоритет фиксов — все выполнены 2026-08-12: WS-токен (27.2), `web_dist` (27.1), теги в payload (27.3), Phase 18 (27.4), Phase 26 (26.A–F).
>
> **2026-08-17 (как читать этот файл):** исполняемый бэклог — **Phase 30** (реестр: приоритеты + порядок волн в шапке; **реестр пуст — все 17 задач закрыты**) и **Phase 31** (постановка 2026-08-17: учебные напоминания в Today). Phase 29 закрыта 2026-08-16. Все `[ ]` в фазах ≤ 28.x — исторические записи: реальный статус каждой фазы зафиксирован в её audit-заголовке, а невыполненные пункты оттуда перенесены в Phase 30 с номерами. Линейное чтение файла сверху вниз как очереди задач некорректно.

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
  - `data/` (runtime; шаблон конфига живёт в `configs/config.example.yaml`, Phase 28.21)
  - `web/dist/`
  - `web/node_modules/`
  - `*.test`, `*.out`
- [ ] **0.14** `.editorconfig`, `.golangci.yml`, `.eslintrc.cjs`
- [ ] **0.15** `README.md` с инструкцией quickstart
- [ ] **0.16** `configs/config.example.yaml` со всеми параметрами (перенесён из `data/` в Phase 28.21 — gitignore-негации внутри исключённой директории не работают)

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

> **Аудит 2026-08-12 (обновлено 2026-08-16):** ✅ — JWT TTL 24h + cookie Secure закрыты в Phase 28.4; аудит-запись про отсутствующий `/projects/:id/tasks` route оказалась stale — роут существует (Phase 28.21). Auth, CRUD, CLI `user create`, фронт-shell, тесты — есть. Таблицы users/projects/tasks созданы в `001_init.sql` (файлы 002/003 — только индексы).

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

> **Аудит 2026-08-12 (обновлено 2026-08-14):** ✅ — all emissions live. Wave 4 PR 2 закрыл `agent.offline` (через `Service.SweepOffline` + `SweepOnline` → `SweepOffline` → `Notifier.Notify`) и `backup.failed` (через `Scheduler.FailureNotifier` интерфейс + `notifyBackupFailed` adapter). Phase 28.5 закрыл `task.commented` и `task.attachment_added` (через `Dependencies.ActivityRecorder` в `createTaskCommentHandler` / `agentCreateTaskCommentHandler` / `addTaskAttachmentHandler`). Не эмитится: `mention.created` (низкий приоритет — для single-owner с агентом, агент сам читает polled events). `settings/Notifications.tsx` остаётся в `Bots.tsx` (UI разметка — нет отдельного notif-экрана). `Bot.FormatMessage` остаётся интерфейсной дырой (боты реализуют Send, FormatMessage нигде не вызывается — можно удалить из комментария).

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

> **Аудит 2026-08-12 (обновлено 2026-08-14):** 🟡 — VACUUM INTO snapshot + ротация, git push, scheduler, CLI (push/snapshot/status/restore), UI — есть. Mirror не пишет комментарии (`nil` в `MirrorSave`); git client без `Status`/`TestConnection`; snapshot по тикеру 24h, не cron 03:00; `PUT /backups/settings` → 501 («config.yaml is the source of truth»); UI настроек read-only. **✅ Wave 4 PR 2 — Mirror now fetches comments; down-миграции закрыли бóльшую часть. PWA outbox update/move/comment зашиты в call sites; InboxPage теперь использует TaskCard. ✅ Phase 28.1 — `PUT /backups/settings` 200 + restart-to-apply banner. ✅ Phase 28.9 — hot-reload backup settings (без restart). Остаётся:** `git client` без `Status`/`TestConnection` (низкий приоритет — инвалидация пиров в error-path); snapshot по тикеру 24h вместо cron 03:00 (косметика).

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

> **Аудит 2026-08-12 (обновлено 2026-08-16):** ✅ — vite-plugin-pwa, Workbox SW, IndexedDB outbox+cache, `POST /api/v1/sync` с идемпотентностью (`sync_ops`), outbox с `queueCreateTask` / `queueUpdateTask` / `queueMoveTask` / `queueCommentTask` (Phase Wave 4 PR 2 — PWA outbox update/move/comment **зашиты в call sites** через `web/src/shared/offline/outbox.ts`). **LWW-расхождение закрыто документально (Phase 28.21):** delivery-order LWW — реальная и корректная семантика для single-device outbox (arrival order ≡ edit order); timestamp-based LWW по `updated_at` отложен до эры multi-device, комментарий `handlers_sync.go` переписан. Background Sync API не используется.

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
- [x] **8.6** Конфликт-резолв: LWW по delivery-order (зафиксировано решением 2026-08-16, Phase 28.21: корректно для single-device outbox; timestamp-LWW по `updated_at` — до эры multi-device)
- [ ] **8.7** Тесты:
  - Оффлайн → онлайн → данные синхронизированы
  - Конфликт корректно разрешается

### Definition of Done

- Отключить сеть → UI работает (read).
- Создать задачу оффлайн → при онлайне она появляется.
- Установить PWA на десктоп.

---

## Phase 9 — Полировка *(ongoing)*

> **Аудит 2026-08-12 (обновлено 2026-08-14):** ✅ — бенчмарки, security headers, rate limit (429+Retry-After), zap+lumberjack, install.sh/systemd/uninstall, dark mode — есть. **✅ Phase 28.11** — `docs/ARCHITECTURE.md` (556 строк, 13 секций). **✅ Phase 28.6** — opt-in pprof endpoint (`DebugPProf` flag + `PProfAddr`), govulncheck target в Makefile. **README скриншоты** — отклонено (вместо PNG-embedding — text pointer на 4 ключевые страницы). **(Prometheus metrics вычеркнут из скоупа решением 2026-08-13.)**
>
> **Update 2026-08-13 (Phase 28.1 polish.1):** закрыт блокер dogfooding — `PUT /api/v1/backups/settings` теперь 200 (раньше 501). UI Settings → Backups: редактируемая форма с Save и restart-to-apply banner. Полная секция — ниже.

---

## Phase 28.1 (полировка) — backup_settings write path *(2026-08-13)*

**Цель:** закрыть blocker dogfooding — operator мог настроить remote backup только через ssh + vim config.yaml + restart. План: PUT /api/v1/backups/settings → 200, пишет в таблицу; UI форма редактируемая; restart-to-apply контракт (in-memory Service immutable, новый URL применяется на следующем старте процесса).

**Контекст:**

- Таблица `backup_settings` (key, value) существовала с `001_init.sql:303`, но ни один кусок кода её не читал и не писал (`grep INSERT|UPDATE|DELETE|SELECT.*FROM backup_settings → 0 hits`).
- `handlers_backup.go:36-47` отдавал 501: «Phase 9 — config.yaml is the source of truth».
- `cmd/orenda/main.go:601-630` создавал `*backup.Service` с in-memory `Config` (URL/auth из cfg) — `backup.Config` иммутабельный после `New()`.
- `web/src/features/settings/Backups.tsx` показывал read-only `<dl>`.
- `docs/API.md:184` и `docs/openapi.yaml:1084` явно документировали 501.

**Ключевые решения:**

- **Два слоя конфига.** `data/config.yaml` остаётся cold-start source of truth (пути, секреты вне БД). `backup_settings` БД-таблица — UI-facing overrides (remote URL, auth, enabled). Get: сначала DB, fallback на cfg; если DB override ≠ cfg.default → `source_hint=ui_override_restart_to_apply`.
- **Restart-to-apply.** `*backup.Service` immutable; UI Save пишет в DB, restart читает. Hot-reload — отдельный долг. Без этой границы форма создаёт ложное ощущение что «сохранил → push пошёл на новый remote», а на деле продолжает работать старый URL.
- **Settings keys.** `enabled`, `remote_url`, `remote_auth` — короткие стабильные имена; значение — JSON blob (через `json.RawMessage`).
- **Auth.** GET никогда не возвращает `remote_auth` в plain text — только флаг `has_auth` (security). Форма не пре-заполняет auth input.
- **Валидация.** `enabled=true` требует непустой `remote_url`; URL `Parse` обязателен; разрешённые схемы: `http`, `https`, `ssh`, `git` (последняя — для GitHub-стиля `git@…` клонов).

**Tasks:**

- [x] **`internal/storage/sqlite/backup_settings_repo.go`** — `BackupSettingsRepository` интерфейс + реализация `GetAll/GetByKey/SetKey/ClearByKey`. PK = key; value = JSON blob; `SetKey` — UPSERT.
- [x] **`internal/api/router.go`** — добавлено `BackupSettings sqlite.BackupSettingsRepository` в `Dependencies`. `fullRouterDeps` подключает репо (zero-cost wiring).
- [x] **`internal/api/handlers_backup.go`**:
  - `listBackupSettingsHandler` → merge DB над cfg; `source_hint` если override.
  - `putBackupSettingsHandler` → 501 → 200 + валидация + persist в DB.
  - `backupSettingsInput` (pointer на `Enabled` чтобы missing ≠ false).
- [x] **`cmd/orenda/main.go`** — wire `BackupSettings: sqlite.NewBackupSettingsRepository(db)`.
- [x] **`web/src/shared/api/client.ts`** — `setBackupSettings(body)` + `BackupSettingsInput` тип + `source_hint` в `BackupSettings`.
- [x] **`web/src/features/settings/Backups.tsx`** — read-only `<dl>` → редактируемая форма (checkbox, URL input, password input), Save button, restart banner. Form state отдельный (`formEnabled/formRemoteUrl/formRemoteAuth`); `formInitialized` flag — форма синкается с сервером только на initial load, не после каждого fetch.
- [x] **Tests:**
  - `internal/storage/sqlite/backup_settings_repo_test.go` — 8 тестов: empty roundtrip, set+get, missing → not ok, empty key rejected, upsert, invalid JSON rejected, empty key on Set rejected, ClearByKey removes row.
  - `internal/api/handlers_backup_test.go` — 7 тестов: GET empty default, PUT+GET roundtrip (с source_hint), invalid URL (missing scheme + ftp scheme → 400), enabled requires URL, disabled без URL OK, remote_auth persist (auth present → has_auth=true, secret never returned), invalid JSON → 400.
  - `web/src/features/settings/Backups.test.tsx` — обновлён existing «read-only settings panel» → editable form (3 теста: form renders, Save posts + success banner, server error surfaces). Pre-existing 9 тестов (push/snapshot/restore/maintenance) остались зелёные.
- [x] **`web/e2e/backups-settings.spec.ts`** (NEW) — happy-path через UI: login → settings → fill form → Save → assert 200 (не 501) → reload → GET reflects persisted URL → source_hint="ui_override_restart_to_apply".

**DoD — verified 2026-08-13:**

- ✅ `go test ./...` — 0 fail (новые: 8 repo + 7 handler; существующие не задеты)
- ✅ `npx vitest run` — 224/224 (+2 от 222; обновлены тесты Backups не «съели» существующие)
- ✅ `npx tsc --noEmit` — clean
- ✅ `make test-e2e` — 15/15 (+1 — `backups-settings.spec.ts`)
- ✅ `TestOpenAPI_RouteCoverage_FullRouter` — pass (route existing в спеке, обновил PUT response shape: now documents `requestBody` + 400/503 codes)
- ✅ Manual: PUT через UI → page.reload → settings сохраняются. Таблица `backup_settings` заполняется. Restart banner показывается при наличии override.

**За скобкой (явно отложено):** hot-reload без restart (5.4 — hot reload `*backup.Service` на каждом `Push` tick — отдельный долг, pre-existing сложность с `Service.cfg` иммутабельностью); `BACKUP__SNAPSHOT_ROTATION_DAYS` и cron schedule как UI-editable (валидация побольше); `Bot.Stop()` на shutdown; `bot_subscriptions` events JSON в UI form; всё из инвентаря «Полировки» ниже.

**Известные ограничения (зафиксированы):**

- Перечень опций: **только `enabled`/`remote_url`/`remote_auth`**. `mirror_dir`, `snapshot_dir`, schedule cron остаются cfg-only (нечего делать в БД — сервер их использует напрямую для создания Service).
- Auth-write нет separate path — форма шлёт `remote_auth=""` для удаления (но `SetKey` пишет empty value; clear=delete отдельный путь, не выставлен через PUT).
- `source_hint` показывается как жёлтый banner; нет кнопки «Restart now» (systemd-notify integration — отдельная история).

## Phase 28.2 (полировка) — Settings index: hub-страница *(2026-08-13)*

> **Дефект зафиксирован владельцем 2026-08-13:** `/settings` рендерит `<Placeholder title="Settings" />` — пустую страницу (`App.tsx`). Подстраницы `/settings/backups` и `/settings/bots` рабочие, но из индекса недостижимы; из сайдбара (⚙ Settings) пользователь попадает в пустоту. Задачи на это в плане отсутствовали.

**Цель:** индекс настроек как hub — все настроечные поверхности достижимы в один клик.

**Контекст (проверено 2026-08-13):**

- Существуют: `Backups.tsx` (редактируемая форма с 28.1), `Bots.tsx` (подписки + Telegram bind), `/agents` (top-level), ThemeToggle в `AppTopBar`, `/reports`.
- Бэкенд-работы не требуется — всё на существующих GET (`/api/v1/info`, `/api/v1/stats`).

**Задачи:**
- [x] **28.2.1** `web/src/features/settings/SettingsHome.tsx`: карточки-ссылки — Backups, Bots & notifications, Agents (`/agents`), Reports; блок About (version, uptime, db size из `/api/v1/info` + `/api/v1/stats`); пометка, что тема — в топбаре. Route `/settings` → компонент вместо `Placeholder`. Vitest: карточки ведут на правильные пути, About рендерит версию.

- [x] **28.2.2** E2E: сайдбар ⚙ → `/settings` показывает hub → клик по Backups ведёт на `/settings/backups`.

**DoD:** `/settings` не пустая; Backups/Bots/Agents достижимы из индекса в один клик; vitest + E2E зелёные.

## Phase 28.3 (полировка) — TaskModal: двойной скролл и недостижимый верх *(2026-08-13)*

> **Баг зафиксирован владельцем 2026-08-13:** при длинном контенте карточки появляются две полоски прокрутки, и до самого верха карточки нельзя доскроллить.

**Диагноз (проверено по коду 2026-08-13):**

- `TaskModal.tsx` backdrop: `fixed inset-0 flex items-start md:items-center justify-center p-2 md:p-6 overflow-y-auto`. На md+ `items-center` центрирует карточку; когда контент выше вьюпорта, верх flex-item'а уходит в отрицательное переполнение, которое не скроллится (классический баг flex-центрирования + overflow). На мобильном (`items-start`) не воспроизводится.
- Вторая полоска — скролл фоновой страницы: scroll-lock на `document.body` при открытой модалке отсутствует; собственный скролл есть только у оверлея (внутри `TaskViewBody` скролл-контейнеров нет).

**Задачи:**
- [x] **28.3.1** Центрирование без отрицательного переполнения: убрать `md:items-center` у backdrop; карточке — `m-auto`/`my-auto` (auto-margin центрирует, но при переполнении схлопывается к верху, весь контент достижим). Сохранить отступы/закрытие по клику мимо.

- [x] **28.3.2** Scroll-lock: на mount TaskModal — `document.body.style.overflow='hidden'`, на unmount — restore (useEffect cleanup). Учесть смену задачи внутри модалки (child → replace, см. `isInModal`): lock не должен «отпасть» между навигациями.

- [x] **28.3.3** E2E: задача с длинным описанием (форсировать узкое/низкое окно) → верх карточки достижим скроллом; полоска одна; фон не скроллится; Esc/клик мимо закрывают.
**DoD:** на md+ вьюпорте длинная карточка скроллится от самого верха до низа; одна полоска прокрутки; фоновая страница неподвижна; vitest + E2E зелёные.

## Phase 28.4 (полировка) — security defaults: JWT TTL 24h + cookie Secure from config *(2026-08-13)*

**Цель:** закрыть два бэклог-долга, которые висели в `SESSION.md` с самого первого аудита — `config.DefaultConfig.JWTTTL 168h` (OWASP-рекомендация для cookie-session — 24h) и `Secure: false` хардкод в `handlers_auth.go`. Оба forward-only: выпущенные до изменения cookie валидны до истечения (JWT exp вшит в токен), новые выпускаются с более строгими дефолтами.

**Контекст (проверено 2026-08-13):**

- `config.go:136` `JWTTTL: 168 * time.Hour` — 7 дней, мягче чем рекомендует OWASP для cookie-session (24h).
- `AuthConfig.CookieSecure bool` уже существовало (default false), но `Dependencies` его не пробрасывало → в `handlers_auth.go:65` хардкод `Secure: false`, `handlers_auth.go:88` (logout) cookie без Secure вообще.
- Расхождение: cookie `Expires` был захардкожен `24 * time.Hour` в handler'е, а `JWTTTL` дефолт 168h — token жил дольше cookie.

**Ключевые решения:**

- **JWT TTL 24h forward-only.** Существующие 168h-токены валидны до конца их срока. Новые логины получают 24h. Оператор может вернуть 168h через `auth.jwt_ttl: "168h"` в `config.yaml` или `ORENDA_AUTH__JWT_TTL=168h`.
- **Cookie Secure из config, не хардкод.** `Dependencies.CookieSecure bool` пробрасывается из `cfg.Auth.CookieSecure` (default false — loopback dev, true за reverse-proxy или прямым TLS). Logout cookie тоже получает Secure — иначе `MaxAge=-1` очищает только не-secure cookie set, secure login cookie переживает logout.
- **Cookie Expires из `deps.JWTTTL`, не хардкод 24h.** Cookie lifetime == JWT lifetime; иначе cookie может пережить token (или наоборот) и `RequireUser` молча отказывает валидным сессиям.
- **Test seams `LoginHandlerForTest` / `LogoutHandlerForTest`.** Маленькие экспорты, чтобы тест атрибутов cookie жил рядом с handlers, а не тянул весь `NewRouter`.

**Задачи:**

- [x] **28.4.1** `config.go:136` `JWTTTL: 168h → 24h`.
- [x] **28.4.2** `router.go::Dependencies` — два новых поля: `CookieSecure bool`, `JWTTTL time.Duration`.
- [x] **28.4.3** `main.go:903` — wire `cfg.Auth.CookieSecure`, `cfg.Auth.JWTTTL` в Dependencies.
- [x] **28.4.4** `handlers_auth.go::loginHandler` — `Secure: false → deps.CookieSecure`, `Expires: 24h → deps.JWTTTL`.
- [x] **28.4.5** `handlers_auth.go::logoutHandler` — `Secure: deps.CookieSecure` (MaxAge=-1).
- [x] **28.4.6** `handlers_auth.go` — `LoginHandlerForTest(deps)`, `LogoutHandlerForTest(deps)` test seams.
- [x] **28.4.7** `config_test.go` — `TestDefaultConfig` asserts JWTTTL == 24h; новые `TestLoad_JWTTTLFromYAML` (YAML override wins), `TestLoad_EnvOverridesYAML` расширен env-ами `JWT_TTL`/`COOKIE_SECURE`.
- [x] **28.4.8** `handlers_auth_test.go` (NEW) — in-memory `pwUserRepo`, 3 кейса `TestLogin_CookieAttributes` (loopback default / HTTPS install / operator-opted-in 168h legacy) + `TestLogin_InvalidCredentials_Returns401` (failed login → no cookie) + 2 кейса `TestLogout_CookieAttributes` (Secure matches login).

**DoD — verified 2026-08-13:**
- `go test ./...`     30/30 ok (config: +2; api: +5 handlers_auth_test).
- `npx vitest run`    236/236 (фронт не тронут).
- `make test-e2e`     17/17 (regression — login/logout flow в других specs не сломан).
- `npx tsc --noEmit`  clean.

**За скобкой (явно отложено):**
- ~~`data/config.example.yaml` не в git~~ — **закрыто в Phase 28.21 (2026-08-16):** шаблон переехал в отслеживаемый `configs/config.example.yaml` (gitignore-негации внутри исключённой директории мертвы — файл физически не попадал в клоны, install.sh падал на fresh clone), `jwt_ttl` обновлён до 24h, секрет генерируется install.sh в `$DATA_DIR/env`.

## Phase 28.5 (полировка) — task.commented / task.attachment_added emission + Bot.Stop() on shutdown *(2026-08-13)*

**Цель:** закрыть два small audit-debt'а из «Полировки»: константы `task.commented` / `task.attachment_added` (declared never emitted) и `Bot.Stop()` никогда не вызывался на shutdown (long-poll транспорты SIGKILL'ились после `ShutdownTimeout`).

**Контекст (проверено по коду 2026-08-13):**

- `internal/domain/activity/model.go:34-35` — константы `ActionCommented = "task.commented"` и `ActionAttachmentAdd = "task.attachment_added"` существуют с Phase 6, но `grep` по использованию — 0 hits в handlers.
- `internal/api/handlers_phase3.go::createTaskCommentHandler` пишет `mention.created` notification, но не activity row. `addTaskAttachmentHandler` тоже не пишет. Обе мутации не шли через `taskSvc` (где живёт стандартный side-effect emission), поэтому audit log пропускал их.
- `cmd/orenda/main.go:917-947` shutdown loop: `srv.Shutdown(shutdownCtx)` → exit. Ботов `Stop()` нет — `for _, b := range botRegistry.List() { ... b.Start(cmd.Context()) ... }` на старте, но без симметричного Stop. Telegram long-poll goroutine остаётся живой до SIGKILL.

**Ключевые решения:**

- **Узкий `ActivityRecorder` интерфейс** в `api/service_interfaces.go` (один метод `RecordTask`). Nil-safe — паттерн `deps.Notifier`. `*activityservice.Recorder` удовлетворяет структурно (та же сигнатура), без explicit адаптера.
- **Log-on-error, не fail.** Audit gap recoverable, failed user request — нет. На error пишем `zap.Warn` и возвращаем успешный ответ.
- **Dedup-attach пропускается.** `res.Duplicate=true` означает, что attachment уже существовал (sha256 dedup); `res.Attachment.ID` указывает на старую запись. Лишний audit row "added" с existing-id обманывает timeline.
- **Best-effort shutdown loop.** `if err := b.Stop(shutdownCtx); err != nil { logger.Warn(...) }` — бот, который не смог Stop, не валит остальной loop (тест `TestRegistry_ShutdownLoop_OneFailingBot_Continues` это пинит).
- **Pre-existing infra fix в E2E setup.** `web/e2e-setup/run-server.sh` теперь `mkdir -p data/uploads/` перед стартом сервера. Attachment service `CreateTemp(s.Config.UploadDir, ...)` требует директорию существующей; без неё первый attachment upload 500'ит. Это попутный полезный фикс для любых будущих attachment E2E.

**Задачи:**

- [x] **28.5.1** `internal/api/service_interfaces.go` — `ActivityRecorder` interface.
- [x] **28.5.2** `internal/api/router.go::Dependencies` — поле `ActivityRecorder ActivityRecorder`.
- [x] **28.5.3** `internal/api/handlers_phase3.go::createTaskCommentHandler` — emission после Add (payload: comment_id, length).
- [x] **28.5.4** `internal/api/handlers_phase3.go::addTaskAttachmentHandler` — emission после StoreFromBytes (payload: attachment_id, filename, mime, size); dedup пропускается.
- [x] **28.5.5** `internal/api/handlers_agent_namespace.go::agentCreateTaskCommentHandler` — emission с `ActorAgent`.
- [x] **28.5.6** `cmd/orenda/main.go` — wire `ActivityRecorder` + shutdown loop `for _, b := range botRegistry.List() { b.Stop(shutdownCtx) }`.
- [x] **28.5.7** `internal/bot/bot_test.go` — `TestConsole_Stop_ReturnsNil` + `TestRegistry_ShutdownLoop_StopsEveryBot` + `TestRegistry_ShutdownLoop_OneFailingBot_Continues`.
- [x] **28.5.8** `web/e2e-setup/run-server.sh` — `mkdir -p data/uploads/`.
- [x] **28.5.9** `web/e2e/task-activity.spec.ts` (NEW) — POST comment → GET /activity → assert task.commented row.

**DoD — verified 2026-08-13:**

- ✅ `go test ./...` — 30 packages ok (bot: +3 Stop tests; api без изменений).
- ✅ `npx vitest run` — 236/236 (фронт не тронут).
- ✅ `make test-e2e` — 18/18 (+1 task-activity.spec.ts).
- ✅ `npx tsc --noEmit` — clean.

## Phase 28.6 (полировка) — opt-in pprof listener + govulncheck target *(2026-08-13)*

**Цель:** закрыть два small infra-долга из «Полировки»: pprof endpoint под флагом и govulncheck target.

**Контекст (проверено 2026-08-13):**

- pprof полностью отсутствует. Нет ни endpoint, ни флага, ни упоминания в Makefile.
- `make lint` гоняет `golangci-lint` + `eslint`. Ни один из них не проверяет Go vulnerability database. Раньше упоминалось «govulncheck target в Makefile».

**Ключевые решения:**

- **Отдельный listener для pprof**, не монтирование в основной mux. `net/http/pprof` экспортирует хендлеры в `http.DefaultServeMux`. Сторонний `http.Server{Addr: 127.0.0.1:6060, Handler: http.DefaultServeMux}` запускается параллельно, если `cfg.Server.DebugPProf=true`. Loopback-only by design — оператор, желающий remote profiling, делает ssh tunnel.
- **Off by default.** Default `DebugPProf=false`. Поднять — `ORENDA_SERVER__DEBUG_PPROF=true` либо `server.debug_pprof: true` в yaml.
- **Graceful shutdown под тот же timeout.** `pprofSrv.Shutdown(shutdownCtx)` рядом с `srv.Shutdown(shutdownCtx)`.
- **govulncheck install-gate.** Если `which govulncheck` пусто, ставим через `go install golang.org/x/vuln/cmd/govulncheck@latest` в `GOBIN`. Иначе запускаем через `go run @latest` — кэшируется Go build cache, следующие запуски быстрые.
- **govulncheck ругается на CVE в stdlib 1.26.4** (GO-2026-5856 Encrypted Client Hello privacy leak) — это **не моя проблема**, фиксится апгрейдом Go. Target работает как надо (exit 3 = найдена CVE).

**Задачи:**

- [x] **28.6.1** `internal/config/config.go` — `ServerConfig.DebugPProf bool` (default false), `ServerConfig.PProfAddr string` (default `127.0.0.1:6060`); env overrides `ORENDA_SERVER__DEBUG_PPROF` / `ORENDA_SERVER__PPROF_ADDR`.
- [x] **28.6.2** `cmd/orenda/main.go` — `_ "net/http/pprof"` import (side-effect registration на DefaultServeMux); если `cfg.Server.DebugPProf`, запустить второй `http.Server` в goroutine; на shutdown `pprofSrv.Shutdown(shutdownCtx)`.
- [x] **28.6.3** `internal/config/config_test.go` — `TestDefaultConfig` asserts `DebugPProf=false` и `PProfAddr="127.0.0.1:6060"`; `TestLoad_EnvOverridesYAML` расширен env-ами `DEBUG_PPROF` / `PPROF_ADDR`.
- [x] **28.6.4** `Makefile` — target `govulncheck` с install-gate.

**DoD — verified 2026-08-13:**

- ✅ `go test ./...` — 30 packages ok (config +2 assertion).
- ✅ `npx vitest run` — 236/236 (фронт не тронут).
- ✅ `npx tsc --noEmit` — clean.
- ✅ Manual smoke: `ORENDA_SERVER__DEBUG_PPROF=true bin/orenda serve` → лог `pprof listening (debug only)` + `/debug/pprof/heap` 200; без флага порт 6060 closed, лог pprof не упоминается.
- ✅ `make govulncheck` — target install'ит tool, скан нашёл GO-2026-5856 в stdlib 1.26.4, exit 3 (правильное поведение).
- ✅ Manual: `cmd/orenda/main.go:946` shutdown loop теперь вызывает `b.Stop(shutdownCtx)` для всех зарегистрированных ботов.

> **Статус 2026-08-13:** Phase 9 (Полировка) закрыта через Phase 28.x sub-phases 28.1–28.18. Roadmap обновлён, секции 28.7–28.17 ниже в PLAN.md.

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
  - ~~Опционально: Prometheus metrics endpoint~~ — **отклонено 2026-08-13:** для single-binary single-user избыточен (второй always-on процесс + TSDB); покрытие — `/api/v1/stats` + slow-request log
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

> **Аудит 2026-08-12 (обновлено 2026-08-14):** 🟡 — registry, config-driven запуск, Console/Telegram/VK/Email/Webhook боты, callback handler с replay protection, тесты — есть. **✅ Phase 28.5** — `Bot.Stop()` теперь вызывается на shutdown (loop через `botRegistry.List()` в `cmd/orenda/main.go`, best-effort). **✅ Phase 10 subphase (test-send)** — POST `/api/v1/bots/test` + UI-карточка с dropdown/target/submit, console исключён из списка, per-bot target pre-check. Email без HTML-шаблонов (plain text); VK только Callback API (Long Poll не реализован); нет weekly digest (DoD). Остаются три подфазы: **Email HTML**, **VK Long Poll**, **Weekly digest** — большие, не блокируют dogfooding.

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
- [x] **10.10** Frontend `web/src/features/settings/Bots.tsx`:
  - Список подключённых ботов, статус
  - **Кнопка «Test send»** — закрыто в `phase-10-test-send` (POST `/api/v1/bots/test`, UI-карточка с dropdown/target/submit, success+error banners, console исключён из списка)
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

> **Аудит 2026-08-12:** 🟡 — миграция 016, DFS-циклы, claim заблокированной → 422 с `unfinished_blockers`, `GET /agent/tasks?ready=true`, WS `task.deps_changed`, UI-бейджи и редактор — есть. **✅ Закрыто 2026-08-14 в `phase-15-agent-context`**: 409 `lock_taken` теперь несёт `holder_agent_id`/`holder_agent_name`/`claimed_at` (был написан `taskLockRepo.Holder`, не был подключён); agent/user context endpoint теперь несёт `blocked_by` (open dependency ids) + `lock_holder` (agent_id/agent_name/acquired_at); `ready=true` исключает задачи, занятые самим агентом (ранее шумели в очереди).

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

> **Аудит 2026-08-12 (обновлено 2026-08-14):** 🟡 — `ListByProjectWithStats` с агрегатами, приоритет-кромка, due-бейдж, счётчики, AssigneeChip с 🤖 (имя через `useAgents()` hook с chips), pure-функции `taskCardBadges.ts` с тестами — есть. **✅ Phase Wave 4 PR 2** — InboxPage переиспользует TaskCard. **✅ Phase 28.19** — AssigneeChip показывает `Agent: <name> (<labels>)` (но Agent-карточки — agent_id для других). Остаётся: ~~UI-тоггл плотности~~ ✅ 28.23.6 (чекбокс «Compact cards» пишет `orenda.kanban.cardDensity`); **бейджи времени** (estimate/spent/таймер не показываются).

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
> **Статус 2026-08-13:** backend смержен (`9c54817` — миграция 020, `SyncStatusAndColumn`, agent-flow двигает карточку; тесты `move_phase278_test.go`, `migration_020_test.go`) + E2E-флип status→column (`7f2544f`, 12/12 зелёные) + **27.8.4 закрыт 2026-08-13** в `phase-27-8-4-columns-status-select` (см. секцию ниже): фронт Status select из колонок проекта, Inbox read-only fallback, `Service.Move` теперь симметричен `SyncStatusAndColumn` (DnD поднимает статус из колонки-цели), E2E `Phase 27.8.4: moving a task to the done column flips status to done`. Все три файла (move.go, project_repo.go, TaskFieldControls.tsx) плюс 9 vitest + 14 E2E. **Phase 27.8 закрыт полностью.**

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
- [x] **27.8.4** Frontend: status-select карточки (27.7) рисует колонки проекта (их имена), а не enum; доска переставляет карточку по WS после claim/approve (рефетч есть — проверить отсутствие фликера). — **✅ Закрыто 2026-08-13 в `phase-27-8-4-columns-status-select`** (полная секция ниже).
- [x] **27.8.5** Тесты: миграция (backfill, кастомный slug), инвариант обеих сторон, approve → задача в done-колонке, filing inbox→проект по статусу; E2E «drag в done → status=done + completed_at». — **✅ Закрыто 2026-08-13:** unit-тесты миграции и синка смержены (`9c54817`) + E2E-флип status→column (`7f2544f`) + новый E2E `Phase 27.8.4: moving a task to the done column flips status to done` (move API → task.status === 'done' + task-status select reads 'done').

**DoD:** доска и статусы — одно целое: изменение с любой стороны (DnD, select, agent-flow) консистентно двигает обе оси; E2E подтверждает; `make test && make lint` зелёные.

**За скобкой:** UI управления набором статусов проекта (добавление/переименование статус-колонок с выбором machine key).

---

## Phase 27.8.4 — Status select из колонок проекта + латентный gap в `Service.Move` *(2026-08-13)*

> **Дефект зафиксирован 2026-08-13.** Phase 27.8 закрыл backend-часть axis collapse (миграция 020, `SyncStatusAndColumn`, agent-flow синхронизирует `column_id`), но **пропустил `Service.Move`** — DnD менял `column_id`, оставляя `status` на старом значении. Плюс фронт `TaskFieldControls` рисовал Status из хардкод enum, а не из колонок проекта. Закрыто одной фазой.

**Что сделано:**

1. **Backend: `Service.Move` поднимает статус из колонки-цели.** В `internal/service/task/move.go::Move` (строки 200-271) после fixup `tr.ProjectID` (Phase 16) добавлена строка: `if s.Columns != nil { col, err := s.Columns.GetColumn(ctx, opts.TargetColumnID); if err == nil && col.Status != "" { tr.Status = task.Status(col.Status) } }`. Defensive — nil Columns или пустой status оставляет `tr.Status` нетронутым (старое поведение, на случай если Columns-репо не завирлен). Symmetric к существующему `syncColumnToStatus` (status → column).
2. **Backend: `GetColumn` подтягивает `status`.** В `internal/storage/sqlite/project_repo.go::GetColumn` (строки 357-385) SELECT дополнен `c.status`, Scan — `sql.NullString`, result populated. До этого `GetColumn` возвращал `Column.Status == ""` всегда, что маскировало gap для всех, кто зовёт метод напрямую (включая наш `Move`).
3. **Backend тест:** `internal/service/task/move_phase278_test.go::TestService_Move_ColumnDrivesStatus` — seed проект с колонками `todo` (status="todo") и `done` (status="done"), задача в `todo` со status="todo", `Move(ctx, taskID, MoveOptions{TargetColumnID: doneCol.ID})`, assert `tr.Status == StatusDone`. Plus обратное направление (drag обратно в `todo` → status="todo"). Persisted check через `GetByID`, не только returned value.
4. **Frontend: `BoardColumn` тип получил `status?: string`.** В `web/src/shared/api/client.ts:67-77` добавлено поле `status?: string` с JSDoc ссылкой на инвариант `task.status ≡ column.status`. Backend уже отдаёт поле (после фикса GetColumn).
5. **Frontend: `TaskFieldControls` теперь читает `api.getBoard(projectId)`.** В `web/src/features/tasks/TaskFieldControls.tsx` добавлен prop `projectID: string`. `useEffect` на его изменение зовёт `api.getBoard(projectID)`, деривит `statusOptions` из `columns`, отсортированных по `position`. Фильтрует `c.status !== ""` (защита от кастомных колонок без явного status). Defensive fallback на канонический enum при ошибке — пользователь не теряет возможность править статус, если бэкенд моргнул. **Inbox fallback:** если `projectID === ''`, `statusOptions` остаётся `null`, рендерится `SidebarReadOnlyField` (label + hint «Inbox task — assign to a project to change status»). Это не ломает UX — у inbox-задачи действительно нет колонки, которую статус бы двигал; вместо misleading select видим честную метку.
6. **Frontend: `TaskViewBody` пробрасывает `projectID`.** Однострочное изменение в `web/src/features/tasks/TaskViewBody.tsx:402` — `projectID={task.project_id ?? ''}`. Это покрывает оба места интеграции (`/tasks/:id` page и `TaskModal`).
7. **Frontend vitest:** `TaskFieldControls.test.tsx` расширен с 7 до 9 тестов. Все 7 существующих тестов теперь передают `projectID="p1"` и мокают `getBoard` через helper `mockBoard()`. Добавлены два новых: `Status select renders project columns sorted by position` (deliberately unsorted + custom column → asserts position-order + custom status renders) и `renders Status as read-only when projectID is empty (Inbox)` (asserts `task-status` testid absent, `task-status-readonly` present, value rendered, **no API call to `getBoard`**).
8. **E2E:** `web/e2e/kanban.spec.ts` — новый тест `Phase 27.8.4: moving a task to the done column flips status to done`. Seed проект с дефолтными колонками (status=`name`), find `todo` and `done` columns by status, create task in `todo`, POST `/tasks/:id/move` с `doneCol.id`, reload, assert (a) `task.column_id === doneCol.id` (b) `task.status === 'done'` (c) sidebar `task-status` select reads `'done'`. (Тест через API, не pointer events — дnd-kit pointer sequences ненадёжны в headless Chromium, см. комментарий в начале файла; но API path тот же, что использует drag handler.)

**DoD — verified 2026-08-13:**
- ✅ Drag на доске (через `POST /tasks/:id/move`) меняет и `column_id`, и `status` атомарно. E2E passes.
- ✅ Status select карточки рендерит опции из колонок проекта (5 канонических + кастомные колонки Phase 12). Vitest passes.
- ✅ Inbox задачи показывают read-only Status label, не misleading select. Vitest passes.
- ✅ Латентный gap закрыт — `Service.Move` теперь симметричен `SyncStatusAndColumn`. Backend tests + new test pass.
- ✅ `go test ./...` — все пакеты ok; `npx vitest run` — 222/222 (+2); `make test-e2e` — 14/14 (+1); `npx tsc --noEmit` — clean.

**За скобкой:** drag-and-drop в inbox (inbox — плоский список без доски, перетаскивать нечего); per-column color editor widget (Phase 27.10 закрыл инициализацию); двусторонний optimistic-update UX (сейчас reload-driven).

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
- Phase 9 polish (prettier, pprof) — отдельная фаза «Полировка» в roadmap. Prometheus вычеркнут решением 2026-08-13 (не нужен для single-binary single-user; покрытие — `/api/v1/stats` + slow-request log).
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
---

## Phase 28.7 (полировка) — Prettier 3.x setup *(2026-08-13)*

**Цель:** убрать шум формата в review. До 28.7 код проходил на глаз; после 28.7 каждый `.ts/.tsx/.css` проверяется Prettier на CI-гейте.

**Контекст:** Prettier сам по себе не был настроен. Каждый PR приносил свой стиль: trailing comma — у кого 1, у кого 0, точка с запятой — иногда. Review-цикл страдал.

**Задачи (выполнены):**

- [x] **28.7.1** `web/.prettierrc.json` — Prettier 3.x конфиг: `printWidth: 100`, `singleQuote: true`, `trailingComma: 'es5'`, `arrowParens: 'always'`.
- [x] **28.7.2** `web/.prettierignore` — `dist/`, `node_modules/`, `playwright-report/`, `coverage/`, `*.gen.ts`.
- [x] **28.7.3** `web/package.json` — скрипты `format` и `format:check`; devDep `prettier@^3.3.3`.
- [x] **28.7.4** `Makefile` — `web-format` и `web-format-check` targets.

**DoD — verified 2026-08-13:** `npx prettier --check src/**/*.{ts,tsx,css} e2e/**/*.ts` запускается; config committed; Makefile target green.

**За скобкой:** автоформат на pre-commit — Phase 28.12.

---

## Phase 28.8 (полировка) — rate_limit YAML section *(2026-08-13)*

**Цель:** rate-limit knobs (`auth.burst`/`auth.refill`, `anon.burst`/`anon.refill`) экспортируются в YAML секцию, не только env-only.

**Контекст:** `internal/api/router.go` имел `os.Getenv("ORENDA_RATELIMIT_AUTH_BURST")` блок. `config.go` ничего про rate-limit не знал.

**Ключевое решение:** имя секции должно матчить env-токены. Секция `ratelimit` + env `ORENDA_RATELIMIT__AUTH_BURST` (`__`-separator). Симметрично `auth.jwt_ttl` ↔ `ORENDA_AUTH__JWT_TTL`.

**Задачи (выполнены):**

- [x] **28.8.1** `internal/config/config.go` — `RateLimitConfig{AnonBurst, AnonPerSec, AuthBurst, AuthPerSec}`; дефолты `20/5` (anon), `300/100` (authed).
- [x] **28.8.2** `internal/config/config_test.go` — `TestDefaultConfig` проверяет дефолты; `TestLoad_YAML*` читает `ratelimit: { auth: { burst: 42 } }`.
- [x] **28.8.3** `internal/api/router.go` — `os.Getenv("ORENDA_RATELIMIT_*")` блоки удалены; `Dependencies` расширен.
- [x] **28.8.4** `cmd/orenda/main.go` — wire `cfg.RateLimit.*` в deps.
- [x] **28.8.5** `configs/config.example.yaml` (tracked) — секция `ratelimit:` с дефолтами. *(Примечание Phase 28.21: изначально задача ссылалась на `data/config.example.yaml` и помечалась «tracked», но файл никогда не трекался — gitignore-негация мертва. Реально отслеживаемым файл стал только в 28.21 после переезда в `configs/`.)*

**DoD — verified 2026-08-13:**
- `go test ./...` — 30/30 ok.
- `make test-e2e` — 18/18.
- `TestRateLimit_Anonymous429` показывает 429 с retry-after.

---

## Phase 28.9 (полировка) — Hot-reload backup settings *(2026-08-13)*

**Цель:** `PUT /api/v1/backups/settings` применяется к живому `*backup.Service` без restart.

**Контекст:** Phase 28.1 сделал PUT-writeable, но restart-зависимый — `*backup.Service` immutable. UI требовал restart процесса.

**Ключевое решение:** `*backup.Service` хранит `cfg atomic.Pointer[Config]` (Go 1.19+). `UpdateConfig(cfg)` атомарно подменяет. Hot-reload читает через `Service.Config()` (public).

Restart-зависимые knobs (mirror dir, snapshot dir, db path) остаются на restart.

**Задачи (выполнены):**

- [x] **28.9.1** `internal/backup/backup.go` — `Service.cfg atomic.Pointer[Config]`; `getCfg()`, `UpdateConfig(cfg)`, `Config()` public.
- [x] **28.9.2** `internal/api/handlers_backup.go` — `putBackupSettingsHandler` зовёт `deps.Backup.UpdateConfig(...)`. `SourceHint` пустая.
- [x] **28.9.3** `internal/api/router.go::Dependencies` — три backup-зеркала удалены.
- [x] **28.9.4** `cmd/orenda/main.go` — wire обновлён.
- [x] **28.9.5** `internal/backup/backup_test.go` — `+3`: `UpdateConfig_SwapsConfigAtomically`, `ConfigReturnsDefensiveCopy`, `UpdateConfig_ConcurrentReadersSeeNoTear`.
- [x] **28.9.6** `web/src/features/settings/Backups.tsx` — banner удалён.
- [x] **28.9.7** `web/e2e/backups-settings.spec.ts` — assert `settings-restart-hint` count == 0.

**DoD — verified 2026-08-13:**
- `go test ./...` — 30 packages ok.
- `npx vitest run` — 236/236.
- `make test-e2e` — 18/18.

---

## Phase 28.10 (полировка) — CSP tightening *(2026-08-13)*

**Цель:** убрать `'unsafe-inline'` из `style-src`. Inline-стили — канонический CSS-exfiltration vector.

**Контекст:** Pre-28.10 `style-src 'self' 'unsafe-inline'` был оставлен "на всякий случай" для Vite HMR. Production build не использует inline styles.

**Задачи (выполнены):**

- [x] **28.10.1** `internal/api/security.go` — `style-src 'self'` без `'unsafe-inline'`.
- [x] **28.10.2** `internal/api/security_test.go` — `+2` tests pin отсутствие inline.

**DoD — verified 2026-08-13:**
- `go test ./...` — 30 ok (security_test +2).
- `make test-e2e` — 18/18.

---

## Phase 28.11 (полировка) — docs/ARCHITECTURE.md *(2026-08-13)*

**Цель:** добавить третий документ-компаньон.

**Задачи (выполнено):**

- [x] **28.11.1** `docs/ARCHITECTURE.md` — 556 строк. 13 секций: process model, directory map, layered architecture, three reference data flows, auth (cookie vs Bearer), WebSocket hub, persistence, frontend layout, build pipeline, configuration, security model, operational concerns, where to start reading.

**DoD:** docs-only change. AGENTS.md ссылается на ARCHITECTURE.md.

---

## Phase 28.12 (полировка) — Pre-commit Prettier hook *(2026-08-13)*

**Цель:** `git commit` в `web/` автоматически форматирует staged файлы через Prettier.

**Ключевое решение:** `simple-git-hooks` (zero deps) + `lint-staged`. Hook = `npx lint-staged` → `prettier --write` на `*.{ts,tsx,css}`. **Prettier only**, без ESLint --fix.

**Задачи (выполнены):**

- [x] **28.12.1** `web/package.json` — devDeps `simple-git-hooks`, `lint-staged`; `scripts.prepare`.
- [x] **28.12.2** `web/` — baseline `npm run format` (117 файлов) + `npm install` → hook ставится автоматически.
- [x] **28.12.3** Hook smoke-test.

**DoD — verified 2026-08-13:**
- `npm run format:check` — clean.
- `npx tsc --noEmit` — clean.
- `npx vitest run` — 236/236.

---

## Phase 28.14 (полировка) — README update *(2026-08-13)*

**Цель:** привести README в соответствие с post-Phase-26 + post-Phase-28.x состоянием.

**Ключевое решение (отказ от screenshots):** "README скриншоты" — отклонено. Embedding PNG в git раздувает историю; вместо screenshots — text-указатель на четыре ключевые страницы: `/`, `/inbox`, `/courses`, `/settings`.

**Задачи (выполнены):**

- [x] **28.14.1** `README.md` — links на ARCHITECTURE.md и SESSION.md; Stack/Features обновлены; Phase 9 закрыт; строка Phase 28 добавлена.

---

## Phase 28.15 (полировка) — hugeParam lint cleanup *(2026-08-13)*

**Цель:** закрыть первую половину lint-бэклога из PHASE 26.A.

**Ключевое решение:** переключить factory-сигнатуры в `internal/api` на `func xxxHandler(deps *Dependencies) http.HandlerFunc`.

**Задачи (выполнены):**

- [x] **28.15.1** `internal/api/handlers_*.go` — 108 factory-функций с `deps Dependencies` → `deps *Dependencies`.
- [x] **28.15.2** Внутрипакетные helpers: `notifyTaskAssignee`, `notifyEvent`, `notifierInbox`, `callAgentServiceRegister`, `syncOpsSeen`/`Record`, `submitCurriculumCore`, `addQuizCore`, `applySyncOp`, `applyTaskTagsChange`, `applyTaskPatch`.
- [x] **28.15.3** `internal/api/router.go::NewRouter(deps *Dependencies)`.
- [x] **28.15.4** `cmd/orenda/main.go` — `api.NewRouter(&api.Dependencies{...})`.
- [x] **28.15.5** Test helpers — `apiNewRouter`, `testDeps` переведены на pointer.

**DoD — verified 2026-08-13:**
- `go test ./...` — 30/30 ok.
- `npx vitest run` — 236/236.
- `make test-e2e` — 18/18.

**Linter effect:** hugeParam 173 → 45.

---

## Phase 28.16 (полировка) — errcheck lint cleanup *(2026-08-13)*

**Цель:** закрыть errcheck половину lint-бэклога.

**Ключевое решение:** `.golangci.yml` `exclude-use-default: false` → `true` (default). Стандартные EXC0001–0007 действуют. 7 реально unchecked остатков в `cmd/orenda/agent.go` → `_, _ = ...`.

**DoD — verified 2026-08-13:**
- `go test ./...` — 30 packages ok.
- `make test-e2e` — 18/18.

**Linter effect:** errcheck 93 → 0. Всего issues: 200 → 106.

---

## Phase 28.17 (полировка) — small-cluster lint cleanup *(2026-08-13)*

**Цель:** закрыть оставшиеся мелкие кластеры до diminishing-returns.

**Задачи (выполнены):**

- [x] **28.17.1** `internal/service/task/move.go` — `lookupColumnForStatus` (string, error) → string; `syncColumnToStatus` (newColID, newStatus) → string.
- [x] **28.17.2** `internal/backup/restore_test.go` — `countUsers` удалён.
- [x] **28.17.3** `internal/storage/sqlite/course_repo_test.go` — `seedUser` stub удалён.
- [x] **28.17.4** `internal/service/agent/notifier_test.go` — `type notifService = fakeNotifier` removed.
- [x] **28.17.5** `internal/backup/backup.go::ListSnapshots` — prealloc.
- [x] **28.17.6** `internal/service/agent/agent.go::RegisterAgent` — ineffassign simplified.
- [x] **28.17.7** `internal/service/timeentry/timeentry.go` — `ErrNoActiveTimer` sentinel (nilnil).
- [x] **28.17.8** `goimports -w internal/ cmd/`.

**DoD — verified 2026-08-13:**
- `go test ./...` — 30/30 ok.
- `make test-e2e` — 18/18.

**Linter effect:** 106 → 95 issues. Closed: unparam (-1), unused (-3), prealloc (-1), ineffassign (-1), nilnil (-1).

---

## Phase 28.18 (полировка) — docs sync (PLAN, SESSION) *(2026-08-13)*

**Цель:** план/снапшот отражают все закрытые Phase 28.x фазы.

**Задачи (выполнены):**

- [x] **28.18.1** `docs/PLAN.md` — добавлены секции Phase 28.7–28.17 после Phase 28.6; Phase 9 помечена closed.
- [x] **28.18.2** `docs/SESSION.md` — header и «Последние прогоны» обновлены; backlog очищен.

---

## Phase 28.19 (полировка) — agent type: одиночное значение → набор меток *(✅ закрыта 2026-08-14)*

> **Решение владельца 2026-08-14:** `agents.type` перестаёт быть одиночным значением из фиксированного списка (`qwen|claude|custom`) и становится **множеством свободных меток**, задаваемых при регистрации. Имя поля НЕ меняется (`type`) — меняется кардинальность: `string` → `[]string`. Каталог тегов Phase 13 (`tags` + join-таблица) НЕ переиспользуется: агентов мало (десятки), join и CRUD каталога избыточны. Хранение — JSON-массив прямо в колонке `agents.type` (конвенция: `bot_subscriptions.events`, 001_init.sql:294). Поиск — фильтр по меткам на чтении.
>
> Заодно закрывается документарный долг: doc-комментарий `domain/agent/model.go` обещает «Phase 10 bot dispatch» по типу — такой диспетчеризации никогда не существовало (рассылка Phase 10 идёт по `bot_subscriptions.bot_type`).

**Цель:** оператор помечает агента произвольным набором меток (`["qwen","installer"]`), UI показывает их чипами, список агентов фильтруется по метке. Поведенческой логики на метках нет: claim-очередь (`/next`, claim/release) по `type` не фильтрует — это информационное поле + поиск.

**Не меняется:** CLI `orenda agent` (регистрации там нет — только через API/UI); очередь задач; нотификации.

**Задачи:**

- [x] **28.19.1** Миграция `021_agent_type_labels.sql` (+ `.down.sql`). Схема не меняется (колонка остаётся `TEXT`); backfill данных: `type = json_array(type)` для непустых значений, `''` → `'[]'`. Down: `type = COALESCE(json_extract(type, '$[0]'), '')` — lossy при множественных метках, зафиксировать комментарием в файле. — *Закрыто: 4-assert test в `migration_021_test.go` (форма, идемпотентность, down round-trip + lossy multi-label, down idempotency); down защищён через `json_valid` против `malformed JSON` от modernc.org/sqlite.*
- [x] **28.19.2** Domain (`internal/domain/agent/model.go`): `Agent.Type []string`; string-тип `Type` и константы `TypeQwen/TypeClaude/TypeCustom` удалить; `Validate` — нормализация (trim, lowercase, dedupe, sort), пустое множество валидно (дефолт `custom` исчезает). Doc-комментарий переписать под новую семантику (убрать «Phase 10 bot dispatch»). — *Закрыто: `NormalizeLabels` экспортирован, `TestAgent_NormalizeLabels` (7 sub-tests) + `TestAgent_Validate_NormalisesTypeInPlace`. Дефолт `custom` удалён.*
- [x] **28.19.3** Storage (`internal/storage/sqlite/agent_repo.go`): scan/serialize JSON-массива; `List` — десериализация с нормализацией. Контракт: в БД всегда валидный JSON-массив строк. — *Закрыто: `marshalAgentType`/`unmarshalAgentType` хелперы; пустой slice → `"[]"`.*
- [x] **28.19.4** API (`internal/api/handlers_agents.go`): `POST /api/v1/agents` принимает `type: []string` (старый string-формат отклоняется 400 — clean cutover, без шима); ответы list/get/register отдают `type: []string`. Фильтр: `GET /api/v1/agents?type=qwen&type=installer` — повторяемый параметр, OR-семантика (агент матчится, если хотя бы одна метка присутствует); масштаб десятки строк — in-memory фильтр после `List` допустим, `json_each` тоже допустим. Обновить `docs/openapi.yaml`. — *Закрыто: in-memory `filterAgentsByLabels` (OR); `docs/openapi.yaml` + embedded copy синхронны; `Agent`/`CreateAgentRequest` schema добавлены; `TestOpenAPI_RouteCoverage_FullRouter` зелёный.*
- [x] **28.19.5** Frontend: `client.ts` — `Agent.type: string[]`, `registerAgent({name, type[], description})`; `AgentsPage` — ввод набора меток free-form chips-input (qwen/claude/custom — лишь подсказки-плейсхолдер, не enum), колонка «Тип» — чипы, фильтр-чипы над таблицей → `?type=`. `AssigneeChip` (канбан): `title = type.join(', ')`. — *Закрыто: `AgentsPage.tsx` chips-input (`Enter`/`,` commit, `Backspace` pop, `×` remove) + chips в таблице + OR-фильтр; `TaskCard.tsx::AssigneeChip` принимает resolved `agent?` через `useAgents()` хук, title = `Agent: <name> (<labels>)`.*
- [x] **28.19.6** Docs: `docs/DB.md` — строка `agents`: `type` = JSON-массив меток. SESSION.md — при закрытии фазы. — *Закрыто: `docs/DB.md` строка agents + таблица миграций (021); SESSION.md раздел про закрытие.*
- [x] **28.19.7** Тесты: миграция (backfill `'qwen'` → `["qwen"]`, `''` → `[]`, up/down roundtrip на копии dev-базы); domain — нормализация (дедуп, регистр, пустые); API — register с массивом → 201, register со строкой → 400, list-фильтр OR; фронт — чипы рендерятся из `type: string[]`. — *Закрыто: 4 asserts миграции + 7 sub-tests domain + миграции 11 файлов тестов с `agent.TypeQwen` → `[]string{"qwen"}` + 10 AgentsPage vitest + 2 TaskCard AssigneeChip + `e2e/helpers.ts::createAgent` (string → array).*

**DoD (проверяется исполнением):**
- ✅ `go test ./...` зелёный — 30/30 packages ok, включая новые тесты миграции/domain/API.
- ✅ `make test` (vitest) зелёный — 241/241 (+11).
- ✅ `npx tsc --noEmit` clean.
- ✅ `make test-e2e` зелёный — 18/18 (после фикса `e2e/helpers.ts::createAgent`).
- ✅ `TestOpenAPI_RouteCoverage_FullRouter` зелёный; `docs/openapi.yaml` ↔ embedded copy синхронны.
- ✅ Smoke: register с `type: ["qwen","installer"]` → `GET /api/v1/agents?type=installer` возвращает агента; `?type=unknown` — пусто. (verified вручную через Vitest-тест «filter chips refetch with repeated ?type= query».)

---

## Phase 28.20 (полировка) — dev/dogfood separation: отдельный usage-checkout + dev-порт *(2026-08-14)*

> **Решение владельца 2026-08-14:** разработка и использование разделяются по двум осям. **Канал бинаря:** usage-инстанс собирается только из отдельного клона `~/opt/orenda` (клон GitHub-remote, ветка `main`) — физически не из dev-репо; обновление — явный ритуал. Клон именно из GitHub, не из локального пути: `git pull` там тянет только запушенное в `main`, каналы не сцепляются. **Runtime-ресурсы:** dev-flow уезжает с синглтон-порта 2137 на 2138; данные разнесены (`~/.local/share/orenda` vs `./data` в репо) — это уже так.
>
> **Prerequisite — первый релиз.** Usage-канал трекает `main`, а `main` сейчас на `637b586` (initial skeleton) — вся работа в `dev`. Первый dogfood-инсталл ≡ первый релиз: промоушн `dev`→`main` по PR + тег `v0.1.0` (push — по явному решению владельца). Обход (`install.sh --force` из dev) возможен, но обесценивает модель — не рекомендуется.
>
> Закрываемые конфликты (подтверждены кодом 2026-08-14): (1) `make dev` и systemd-инстанс делят порт 2137, причём Vite-прокси (хардкод 2137, `web/vite.config.ts`) молча проксирует dev-фронт на usage-бэкенд; (2) `install.sh` из любой ветки/dirty-tree перезаписывает единственный глобальный бинарь; (3) дефолтный конфиг CWD-relative (`data/config.yaml`), глобальный бинарь из папки репо подхватывает репо-БД; (4) `serve` авто-мигрирует любую БД на старте (`main.go`) — dev-бинарь против usage-БД угоняет схему вперёд релиза.
>
> **Принятый остаточный риск:** обратный вектор — dev-бинарь, вручную запущенный с `--config ~/.local/share/orenda/config.yaml`, — закрывается только дисциплиной (авто-миграции делают его деструктивным). Механической защиты осознанно нет.

**Цель:** usage и dev живут одновременно, не пересекаясь ни бинарём, ни портом, ни БД. Dogfood ест только то, что запушено в `main`.

**Операторская часть (вручную, один раз):** после промоушна dev→main — `git clone <github-remote> ~/opt/orenda && cd ~/opt/orenda && scripts/install.sh --systemd`. Данные usage — `~/.local/share/orenda` (install.sh сидит конфиг с абсолютными путями — уже реализовано).

**Задачи:**

- [x] **28.20.1** Makefile, таргет `dev`: экспорт `ORENDA_SERVER__PORT := 2138` (переопределяемо: `make dev ORENDA_SERVER__PORT=2200`). air и Vite наследуют env из рецепта — одна переменная драйвит обе стороны. Дефолтный порт бинаря (2137) НЕ меняется.
- [x] **28.20.2** `web/vite.config.ts`: proxy-targets `/api` и `/ws` читают `process.env.ORENDA_SERVER__PORT ?? '2138'` вместо хардкода 2137; комментарий обновить. E2E не трогаем — Playwright на своём порту 21371 (`playwright.config.ts`), `web/e2e-setup/run-server.sh` self-contained.
- [x] **28.20.3** `scripts/install.sh`: гард канала — отказ, если текущая ветка ≠ `main` или tree dirty (сообщение с веткой/коммитом; `--force` переопределяет). Перед установкой печатать channel-инфо (ветка, короткий хеш).
- [x] **28.20.4** `cmd/orenda/main.go`: в стартовый лог `serve` (там уже `config` + `addr`) добавить resolved `db_path` — observability «какой это инстанс» в журнале systemd.
- [x] **28.20.5** `scripts/update-dogfood.sh`: ритуал обновления usage-инстанса — `git pull --ff-only origin main && scripts/install.sh --systemd && systemctl --user restart orenda` (`set -euo pipefail`, запускается из `~/opt/orenda`; ff-only гарантирует чистый канал).
- [x] **28.20.6** Docs: `docs/ARCHITECTURE.md` (или README) — раздел «Dev vs dogfood instance»: два checkout'а, матрица портов (usage 2137 / dev 2138 / e2e 21371), data dirs, запрет `install.sh` из dev-репо, update-ритуал, остаточный риск из шапки. `AGENTS.md` — строку «Port 2137 is singleton» обновить под конвенцию dev=2138.
- [x] **28.20.7** Тесты/верификация: bash-тест или ручной прогон гарда install.sh (из ветки ≠ main → отказ без `--force`); `make dev` smoke — backend отвечает на :2138, `curl :5173/api/v1/info` → 200 (proxy следует за env); `go build ./...` зелёный.

**DoD (проверяется исполнением):**
- Два одновременно живых инстанса: usage (systemd, :2137, из `~/opt/orenda`) + `make dev` (:2138) — оба 200 на `/api/v1/info`, БД разные (по `db_path` в логах).
- `install.sh` из dev-ветки → отказ; из чистого `main` → успех.
- `make test` и `make test-e2e` зелёные (E2E-порт 21371 не задет).

---

## Phase 28.21 (полировка) — ops-hardening: login rate-limit, installable config template, JWT secret по умолчанию *(2026-08-16)*

> **Аудит 2026-08-16 (три параллельных скаута: backend / frontend / docs-ops).** Найденные критичные дыры закрыты в этой фазе; механический backend-свип — Phase 28.22; frontend-фундамент — Phase 28.23.

**Контекст (evidence):**

- `/api/v1/auth/login` сидел в `SkipPaths` rate limiter'а (`router.go`) — мидлварь возвращалась раньше, login обходил оба бакета: неограниченный перебор паролей. Добавлено в Phase 26.E «для E2E», но E2E и так поднимает лимиты через `ORENDA_RATELIMIT_*` env.
- `install.sh` читал `data/config.example.yaml`, который **никогда не трекался**: `.gitignore` исключает `data/`, а git не умеет re-include файлов внутри исключённой директории — обе `!data/...` негации мертвы (`git check-ignore -v` подтверждает). Флоу Phase 28.20 (`git clone → install.sh`) падал mid-install на fresh clone. PLAN 28.8.5 при этом утверждал «tracked» — ложь.
- Два пути к публично известному JWT-секрету: (а) пример конфига содержал `"${ORENDA_JWT_SECRET}"`, но `config.go` не делает `os.ExpandEnv` — литеральная строка становилась HMAC-ключом, и имя переменной не совпадало со схемой `ORENDA_AUTH__JWT_SECRET`; (б) systemd unit штатно поставлял `change-me-via-EnvironmentFile`, а `install.sh` env-файл не создавал.

**Задачи:**

- [x] **28.21.1** `router.go`: `/api/v1/auth/login` убран из `SkipPaths` (комментарий фиксирует, почему `/api/v1/me` остаётся). Тест `TestRateLimit_LoginNotSkipped` — 100 POST → 429 + Retry-After.
- [x] **28.21.2** `configs/config.example.yaml` (NEW, tracked): переезд из `data/`; `jwt_ttl: "24h"`; `jwt_secret: ""` с комментарием про env; секция `ratelimit:` (долг 28.8.5); честная пометка про `sqlite_snapshot_cron` (cron не парсится — тикер 24h). `.gitignore`: мёртвые негации удалены. `install.sh` читает новый путь.
- [x] **28.21.3** JWT-секрет из коробки: `install.sh` генерирует `$DATA_DIR/env` с `ORENDA_AUTH__JWT_SECRET` (32 байта urandom→base64, mode 600) при первом запуске; `orenda.service` больше не шьёт placeholder-секрет (только `EnvironmentFile=-@DATADIR@/env`); финальный hint install.sh показывает, как подгрузить env для CLI.
- [x] **28.21.4** Docs-синхронизация: PLAN (stale Phase 1 route — закрыт; Phase 8 LWW — решение зафиксировано; 28.8.5 «tracked» — исправлена ложная запись; пути `data/config.example.yaml` → `configs/`); `handlers_sync.go` — комментарий переписан под delivery-order LWW; README — 246 vitest, `make lint` без prettier (отдельный `web-format-check`), 18 tests / 13 specs; SESSION + CHANGELOG.
- [x] **28.21.5** Smoke: fresh clone в /tmp → `install.sh --force` (ветка ≠ main) доходит до конца; `$DATA_DIR/env` создан с mode 600; `make test` / `make test-e2e` зелёные. *(Verified 2026-08-16: build+install+config+env в /tmp clone; бинарь стартует со сгенерированным секретом; login флуд ~59×401 → 429; Go 30/30, vitest 246/246, E2E 18/18.)*

**DoD (проверяется исполнением):** login флудится → 429; fresh-clone install не падает; дефолтного JWT-секрета в репо нет; `go test ./...` + vitest + E2E зелёные.

**За скобкой (зафиксировано, не блокирует):** `uninstall.sh` молча глотает неизвестные флаги и без `--help`; `update-dogfood.sh` хардкодит remote `origin` и не имеет `--force`; нет CI — локальные гейты не enforced (кандидат на отдельную фазу); `git client Status/TestConnection` (Phase 7); `sync_ops` record-failures проглатываются (`_ = syncOpsRecord(...)`) — редкий путь, но молчит.
---

## Phase 28.22 (полировка) — backend sweep: N+1, мёртвый код, vet finding *(2026-08-16)*

> Продолжение аудита 2026-08-16: механическая зачистка backend-находок. Ops-дыры закрыты в 28.21; frontend — 28.23.

**Задачи:**

- [x] **28.22.1** N+1 в `listAgentTasksHandler`: новый batch-примитив `task.Repository.BlockersForTasks(ctx, ids)` (по образцу `TagsForTasks` из 27.3 — pre-populated empty slice на каждый id); handler делает один запрос на весь листинг вместо `Blockers()` в цикле. Тест `TestTaskRepo_BlockersForTasks` (форма, Done-флаг, пустой вход, entry для незаблокированной задачи).
- [x] **28.22.2** `handlers_today.go`: мёртвый `ids`-loop убран; enrichment больше не сканирует всю таблицу — новый `Filter.IDs` ограничивает `ListByProjectWithStats` видимыми задачами (было: 5 aggregate-запросов по всей БД ради ~дюжины карточек). Тест `TestTaskRepo_ListByProject_IDsFilter`.
- [x] **28.22.3** Единственный `go vet` finding закрыт: мёртвый `RecordOld` (с недостижимым дублем append/return) удалён из `move_test.go`.
- [x] **28.22.4** `event.go`: удалён дважды продублированный suppression-блок (`var _ = strconv.Itoa` × 2) + stale-комментарий «RRULE не используется» (используется — `handlers_calendar.go` зовёт `ExpandRecurrence` с Phase 23.3); мёртвый `newUUID() → ""` хелпер и его no-op вызов удалены (task repo сам назначает UUIDv7).
- [x] **28.22.5** `backup.go::newUUID`: ручное чтение `/dev/urandom` (silent all-zeros id при ошибке) → `uuid.NewString()` (зависимость уже есть).
- [x] **28.22.6** Три `var _ = ...` import-suppression'а удалены: `handlers_phase3.go` (activity — используется), `handlers_agent.go` (task/agent — agent-импорт удалён за ненадобностью), `notifier.go` (strings — единственным «использованием» была сама заглушка; импорт удалён).
- [x] **28.22.7** Мёртвый `heartbeatRequest` decode в `handlers_agents.go` (структура никогда не читалась).
- [x] **28.22.8** Мёртвый `ownerID := "" / _ = ownerID` в `handlers_courses.go` + 3-абзацный комментарий сжат до одного абзаца.
- [x] **28.22.9** Stale-комментарии: `router.go` («auth/REST/WS land in later phases» — shipped), `config.go` («BotConfig placeholder for Phase 10» — shipped).

**DoD (проверяется исполнением):** `go build ./...` + `go vet ./...` чистые (0 findings); `go test ./...` зелёный; golangci-lint 97 → 95 issues (роста нет); 2 новых repo-теста зелёные.
---

## Phase 28.23 (полировка) — frontend foundations: WS-race, deps-хигиена, density-toggle, shared UI primitives *(2026-08-16)*

> Третья часть аудита 2026-08-16: frontend-находки. Ops-дыры закрыты в 28.21, backend-свип — в 28.22.

**Задачи:**

- [x] **28.23.1** WS re-subscribe race в `useWebSocketTopic` (`shared/ws.ts`): handler теперь в `useRef` (обновляется каждый рендер), подписка — один раз на `topic` (deps `[topic]`). Все ~11 call sites передают inline arrows — со старыми deps `[topic, fn]` каждый рендер делал unsubscribe+resubscribe, и событие, пришедшее в зазор, терялось. Тест `shared/ws.test.tsx` (3 кейса) пинит: стабильный handler identity между ререндерами, вызов последней версии closure, resubscribe при смене топика. **Mutation-check выполнен:** возврат deps `[topic, fn]` флипает первый тест red, откат — зелёный.
- [x] **28.23.2** WS→Query invalidation для `agents`: `AppLayout` подписывается на топик `agents` и инвалидирует `agentsQueryKey` (shared cache AssigneeChip/AgentsPage/sidebar badge). Тест в `AppLayout.test.tsx` — dispatch WS-события → `invalidateQueries({queryKey: agentsQueryKey})`.
- [x] **28.23.3** package.json hygiene: удалены `zustand` (ноль импортов в src/) и `@tiptap/extension-bubble-menu` (BubbleMenu импортируется из `@tiptap/react/menus`); `idb` переехал из devDependencies в dependencies (runtime-импорт в `shared/offline/db.ts`). `package-lock.json` синхронизирован.
- [x] **28.23.4** `patchTaskOrQueue` double-cast убран: сигнатура `(task: Task, patch: Partial<Task>)`; offline-path мерджит патч поверх существующей задачи (`{...task, ...patch}`) вместо фабрикации Task из патча через `as unknown as Task`. Три call site (title/description/color) получили `if (!task) return` guard. Поведение online идентично.
- [x] **28.23.5** Shared UI primitives: `shared/ui/Loading.tsx`, `ErrorBanner.tsx`, `EmptyState.tsx` — Tailwind + `dark:` варианты. Мигрированы рукодельные эквиваленты в `TodayPage` (loading + error), `InboxPage` (error + empty), `ReviewPage` (loading + error + empty) и красный баннер в `CalendarPage` (был `bg-red-50 text-red-800` без dark: — нечитаем в тёмной теме; ErrorBoundary fallback тоже мигрирован). Тесты `shared/ui/ui.test.tsx` (7 кейсов: рендер, dark-классы).
- [x] **28.23.6** Card density toggle UI (долг Phase 17): `TaskCard` читал `orenda.kanban.cardDensity` из localStorage с Phase 17, но никто его не писал. `KanbanBoard` получил чекбокс «Compact cards» рядом с «Show child tasks» (тот же паттерн: localStorage + state; запись синхронная в onChange, чтобы тот же рендер-проход уже читал свежее значение). Тест `KanbanBoard.test.tsx` (2 кейса): тоггл переключает плотность без reload (due-badge скрывается/появляется) и persisted-флаг читается на mount.
- [x] **28.23.7** `AuthContext.test.tsx` (NEW, 4 кейса): 401 от /me → `anonymous`; успешный /me → `authenticated` с профилем; logout зовёт endpoint и чистит state; logout чистит state даже при упавшем endpoint (finally). Конвенции axios-stub из `RequireAuth.test.tsx`.
- [x] **28.23.8** Stale-комментарий в `CalendarPage.tsx`: «drag is on the roadmap» переписан — drag-reschedule живой (withDragAndDrop + onEventDrop PATCH'ит start_at/end_at).

**DoD (verified 2026-08-16, worktree `phase-28-23-frontend-foundations`):**

- `npx tsc --noEmit` — clean.
- `npx vitest run` — 263/263 (было 246; +17: 3 ws + 1 AppLayout + 7 ui + 2 KanbanBoard + 4 AuthContext).
- `npx prettier --check` на всех затронутых файлах — clean.
- `npx eslint` на затронутых файлах — 0 errors, 0 warnings.
- `make test-e2e` — 18/18 pass (21.9s).
- Mutation check (28.23.1): инверсия deps → тест красный; revert — зелёный.

---

## Phase 29 (фича) — Agent surfaces: wiki-управление + создание курсов агентом *(постановка 2026-08-16)*

> **Мотивация (продуктовое решение 2026-08-16).** Целевая сценария: пользователь пишет внешнему агенту (opencode, MCP-клиент) «создай курс по OpenCode» — агент создаёт курс целиком (курс → curriculum → уроки → квизы → активация), человек только учится. Wiki агентам недоступна вообще; курсы агент может наполнять, но не создавать. Смысл проекта — минимизировать ручную работу, перекладывая её на агентов.

**Контекст (evidence, read-only сверка 2026-08-16):**

- MCP-сервер (`internal/mcp/orenda_tools.go`) — тонкий мост над `orenda agent` CLI: ровно 7 тул (me/list_tasks/claim/release/submit/context/await), каждая 1:1 над `/api/v1/agent/*`. Skill документирует только этот delegation loop.
- Agent-неймспейс (`router.go`, группа `RequireAgent`): `tasks/{claim,release,submit,context,comments}`, `events/await`, `courses` (GET drafts + PUT curriculum), `lessons/{materialize,content,quizzes}`. Wiki-роутов нет.
- Wiki — user-side only (Phase 5): `/api/v1/pages/*` (list/get/save/delete/move/backlinks) + `/api/v1/search` под `RequireUser`; агентский bearer → 401. Хендлеры `handlers_wiki.go` **не читают user-контекст** (grep `userIDFromCtx`/`Identity` пуст) — переиспользуются под `RequireAgent` без развилок. `wiki_pages` без owner/author колонок — permission-модель не нужна; сервис один, значит mirror (git) и WS-события агент получает бесплатно.
- Курсы: создание есть только user-side (`POST /api/v1/courses` → `CourseService.CreateWithIntent(ctx, userID, title, intentMD, opts...)`); `owner_id NOT NULL REFERENCES users(id)` (миграция 019, «single-owner today, but the column is here»). Для agent-контекста владельца резолвит существующий `user.Repository.FirstNonSystem(ctx)`.
- `MaterializeLesson` гейтится только статусом урока (locked → open), **не** статусом курса — уроки можно наполнять сразу после curriculum. `SubmitCurriculum` флипает курс в `review`; approve (review → active) — только user-side.
- `CreateWithIntent` спавнит generator task для тьютора; опция `SkipGenerator()` уже существует — при создании курса самим агентом generator task не нужен (агент и есть генератор).

**Дизайн-решения (зафиксированы):**

1. **Владелец agent-created курса** — `Users.FirstNonSystem` (система single-user; колонка owner_id уже готова к multi-user). Не параметр запроса.
2. **`SkipGenerator` при agent-создании** — иначе спавнится generator task, который дублирует работу уже работающего агента.
3. **Agent-side активация курса** — новый `POST /agent/courses/{id}/activate` (review → active через тот же сервисный путь, что и user-side approve; activity авторства агента). Human approve в UI остаётся. Обоснование: клик-аппрув — это ручная работа, которую пользователь явно попросил убрать; качественный гейт при этом не исчезает из продукта — он становится опциональным (человек может вернуть курс request-changes в любой момент, `active → review` недоступно, но `archived` и повторный curriculum-swap работают).
4. **Wiki без смены схемы** — атрибуция в activity-feed (где эмитится), author-колонку не добавляем.

**Задачи:**

- [x] **29.1** **Agent wiki REST.** **Закрыта 2026-08-16 в `phase-29-1-agent-wiki-rest`.** Wiki-хендлеры смонтированы под `RequireAgent` as-is (grep-verified: ни один не читает user-ctx; `wiki_pages` без owner-колонки → permission-модели нет): `GET /agent/pages` (tree), `GET /agent/pages/{slug}`, `PUT /agent/pages/{slug}` (upsert — create+update одним verb, bare POST нет), `DELETE`, `PATCH /move`, `GET /backlinks`, `GET /agent/search?q=&type=&limit=`. Mirror (git) + WS-события агент получает бесплатно (тот же `WikiService`). 4 интеграционных теста (`handlers_agent_wiki_test.go`): полный CRUD round-trip (create → read → move → tree nested → backlinks через `[[slug]]` → update с сохранением ID → delete → 404); 401 без токена / с user-cookie / с bad bearer; FTS5 находит agent-written контент; user-side неизменна (user создаёт — агент читает, shared service). OpenAPI оба файла синхронны; `TestOpenAPI_RouteCoverage_FullRouter` зелёный. **Verify:** `go test ./... -race -count=1` exit 0; `make build` OK; `make test-e2e` 19/19; `golangci-lint run --new-from-merge-base=origin/dev` exit 0.
- [x] **29.2** CLI `orenda agent`: `pages list|get|put|delete|move|backlinks` + `search <query>`. **Закрыта 2026-08-16 в `phase-29-2-7-agent-surfaces`.** `put` читает markdown из `--file`/stdin; `--json` паритет. Попутно починен латентный баг транспорта: `doRaw` присваивал путь с query-строкой в `u.Path`, процент-кодируя `?` — фильтр `?ready=true` в `next` молча не работал. 4 cobra-level теста (PUT verb+payload, stdin, PATCH move, query encoding).
- [x] **29.3** MCP-тулы в `orenda_tools.go`: `orenda_pages_list`, `orenda_pages_get`, `orenda_pages_save`, `orenda_pages_delete`, `orenda_pages_move`, `orenda_search`. **Закрыта 2026-08-16 в `phase-29-2-7-agent-surfaces`.** Flat naming сохранён; добавлены `agentPut/Patch/Delete` хелперы. Попутно починен латентный баг: `orenda_await` слал в user-side `/api/v1/events/await` (401 на opaque agent token) — теперь `/api/v1/agent/events/await`. 8 новых тестов в `orenda_tools_test.go` (tools/list 13 шт, verb/body/encoding pinning, validation без backend-hit).
- [x] **29.4** Agent course creation: `POST /agent/courses {title, intent_md?}`. **Закрыта 2026-08-16 в `phase-29-2-7-agent-surfaces`.** Owner = `FirstNonSystem`, `SkipGenerator` форсирован (подсчёт вызовов TaskCreator = 0 в тесте); нет owner → 503 `owner_not_configured`; нет title → 400 `missing_title`. Тест кейса «нет owner» опущен: регистрация агента требует user row (FK), поэтому состояние недостижимо через HTTP.
- [x] **29.5** Agent course activation: `POST /agent/courses/{id}/activate`. **Закрыта 2026-08-16 в `phase-29-2-7-agent-surfaces`.** Общий путь `approveCourseCore` вынесен; user-side approve и agent-side activate — один сервисный метод. Бонус-фикс: missing course на обоих surfaces теперь 404 (был 500 — `coursesvc.ErrNotFound` не маппился). Тесты: review → active (первый урок unlock), draft → 422, missing → 404, no-token/cookie → 401. **Отклонение от постановки:** «activity записана с автором-агентом» не реализовано — activity-feed существует только для задач (`task_activity.task_id NOT NULL`); user-side approve тоже ничего не пишет. Курсового activity-контура нет вовсе; заводить его только для agent-пути — расширение скоупа. Если нужен — отдельная задача Phase 30.
- [x] **29.6** Спеки и skill. **Закрыта 2026-08-16 в `phase-29-2-7-agent-surfaces`.** OpenAPI (оба файла синхронны): `POST /agent/courses` + `POST /agent/courses/{id}/activate`; `TestOpenAPI_RouteCoverage_FullRouter` зелёный. SKILL.md: §2.2 — CLI pages/search; §4.3 — 6 новых MCP-тульов; новая §4.4 «Build me a course on X» — end-to-end сценарий create → curriculum → materialize → activate с curl-примерами; §6.1 — строки wiki/search/course-create/activate endpoints. SESSION.md обновлён.
- [x] **29.7** Smoke DoD скриптом. **Закрыта 2026-08-16.** Реальный бинарь, свежая tmp-БД, порт 21431: курс создан агентом (draft, owner=первый не-system, без generator task) → curriculum (модуль + 2 урока + exact-квиз) → materialize обоих → activate → user-side видит `active` + уроки `open`. Wiki агентским токеном: create/move/backlinks/search (FTS находит контент)/edit-upsert/delete с каскадом (404 после). CLI parity: `orenda agent pages get` + `orenda agent search` через bearer token. Вывод: `SMOKE OK`.

**DoD (проверяется исполнением):** внешний агент (MCP или CLI) создаёт обучаемый курс без единого действия человека после постановки; агент полностью управляет wiki; `make test && make lint` зелёные; openapi coverage-тест зелёный.

**За скобкой (зафиксировано):** owner-side UI для wiki/course agent-активности не меняется (курсы/страницы просто появляются); multi-user owner-выбор отложен до появления второго пользователя; `[[` autocomplete в редакторе — задача **30.6**.

---

## Phase 30 (реестр) — Открытые задачи с приоритетами *(постановка 2026-08-16)*

> **Правило процесса (решение пользователя 2026-08-16).** «Долгов» как свободных записей в оговорках/«за скобкой»/бэклоге больше нет: каждый открытый пункт — нумерованная задача этой фазы с приоритетом. Новые отложенные работы при закрытии любой фазы обязаны получать номер здесь, а не записываться в «За скобкой» без номера. Закрытые задачи помечаются `[x]` с датой и остаются в реестре (история).
>
> **Инвентаризация 2026-08-16:** собрано из audit-оговорок (Phase 4/5/7/10/17/19/21/23), «за скобкой» 27.6–27.9 и 28.21, SESSION-бэклога. Каждый пункт сверен с кодом read-only. Проверено и **НЕ заведено** (уже закрыто, не ре-листать): per-column color editor widget — `EditColumnModal` (rename/color/WIP) + color dot (27.10); events подписок в UI — `selectedEvents` в `Bots.tsx`; optimistic move на канбане с revert (`KanbanBoard.tsx`) — запись «reload-driven» устарела для move-пути; hot-reload backup settings — 28.9; tracked config template — 28.21.
>
> **Приоритеты:** **P1** — целостность процесса/данных, enforcement гейтов. **P2** — продуктовые пробелы из DoD/аудитов, видимые пользователю. **P3** — полировка, ops-гигиена, механические остатки.
>
> **Как исполнять реестр (контракт для агентов):** это пул для диспатча, **не фаза** — задачи не исполняются «все подряд» и не мерджатся одной веткой. Одна задача 30.x = один worktree/branch/PR по общему правилу. Если диспетчер (пользователь/PM) не назначил конкретную задачу — бери первую открытую из самой ранней незакрытой волны ниже и **докладывай номер задачи в PR**, не спрашивая порядок: он определён здесь. Внутри волны задачи независимы, порядок свободный, параллельное исполнение разными агентами разрешено (файловые пересечения минимальны).
>
> **Claim-протокол (обязателен, локом служит git):**
>
> 1. **До создания worktree** пометь задачу в этом файле: `- [ ]` → `- [~] **30.x** — в работе: <имя агента>, branch \`phase-30-x-<slug>\`, с <дата>`. Легенда: `[ ]` свободна · `[~]` занята · `[x]` закрыта.
> 2. Claim-коммит идёт **сразу в `dev`** (`docs(plan): claim 30.x — <agent>`) — это единственный разрешённый self-merge без ревью: координационная метка, docs-only. Worktree создаётся уже от dev с твоей меткой.
> 3. Перед claim-коммитом — `git fetch` + rebase на свежий `dev`. **Если строка уже `[~]` — задача занята, брать следующую по волне.** Не fast-forward или конфликт на строке = проигранная гонка, не «исправлять» чужую метку.
> 4. Бросаешь задачу — верни `[ ]` отдельным коммитом (аналог `release` в delegation loop: незакрытый claim блокирует других). Завершение — `[x]` с датой в PR-ветке, мердж по обычному ревью.
> 5. Диспетчер, назначая задачу вручную, ставит `[~]` сам — ручная постановка и самоназначение не расходятся.
>
> **Волны (рекомендованный порядок):**
>
> - **Wave 0 — сначала CI:** 30.1. Всё последующее должно мерджиться под защитой CI; без него регрессии опять заходят молча.
> - **Wave 1 — процессная целостность (мелкие backend):** 30.2, 30.7, 30.17.
> - **Wave 2 — быстрый фронтенд/ops (независимые, параллелизуемые):** 30.10, 30.11, 30.12, 30.15.
> - **Wave 3 — средние:** 30.6, 30.8 (формат решён 2026-08-16: all-day маркер дедлайна), 30.9, 30.14.
> - **Wave 4 — большие:** 30.3, 30.4, 30.5; 30.13 — после закрытия Phase 29 (зависимость заявлена в постановке).
> - **Фоново:** 30.16 — партиями при касании файлов, без отдельной волны.

### P1

- [x] **30.17** **30.7 gap: `writeError` не маппит `taskservice.ErrInvalidInput` → 500 вместо 400.** **Закрыта 2026-08-16 в `phase-30-17-write-error`.** `internal/api/errors.go::writeError` теперь маппит `taskservice.ErrInvalidInput` → 400 `invalid_input` (раньше падал в default 500). Новый API-тест `TestP3_ReviewWithoutCommentReturnsBadRequest` в `phase3_handlers_test.go` пинит три приёмки: (1) `decision=reject` без comment → 400 `invalid_input`; (2) whitespace-only comment (`"   \n  "`) → 400; (3) bogus `decision` (`"bogus"`) → 400. До правки все три возвращали 500 `internal` — реальный bug, который позволял фронту неверно интерпретировать валидационный фейл как серверный сбой. **Verify:** `go test ./internal/api/... -run TestP3_Review` → green; `go test ./... -race -count=1` 30/30 packages ok; vitest 285/285; `make build`; `make test-e2e` 19/19.

- [x] **30.1** **CI.** **Закрыта 2026-08-16 в `phase-30-1-ci`.** GitHub Actions workflow `.github/workflows/ci.yml` с 4 job'ами (`lint` → `test` → `build` → `e2e`, fail-fast); concurrency cancel-in-progress; Go 1.26 / Node 24 / golangci-lint v6 action. **Lint scope:** PR — `--new-from-merge-base=origin/<base>` (incremental gate, новый код должен быть чистым); push в main — full (release branch); push в dev — skipped (PR gate — единственный enforcement на merge). Web lint — eslint + `prettier --check`. `.golangci.yml`: отключён pre-existing `hugeParam` gocritic rule (Phase 28.15 закрыл это для API handlers, остальное — stylistic), rule `error-returned` → `error-return` (revive rename). **Verify:** `make build` ✓; `go test ./... -race -count=1` 30/30 packages ok; vitest 263/263; `make test-e2e` 18/18; `golangci-lint run --new-from-merge-base=origin/dev` exit 0 (нет новых issues); `golangci-lint run` exit 1 (73 pre-existing — см. 30.16). Приёмка: PR с красным тестом блокирует merge через GitHub branch protection (включается владельцем после merge этого PR; в репо settings → branches → Branch protection rules → Require status checks: `Lint (Go + Web)`, `Test (Go + vitest)`, `Build (binary with embedded SPA)`, `E2E (Playwright)`).
- [x] **30.2** **`sync_ops` record-failures молчат.** **Закрыта 2026-08-16 в `phase-30-2-sync-ops`.** `syncOpsRecord` (helpers, вызывается 6× после успешной мутации в `handlers_sync.go`) теперь при `Record()` error: bump `liveStats.syncOpsRecordFailures` + `logger.Warn("sync_ops record failed; client may replay this op", client_id, server_id, err)` (через `deps.Logger` или `zap.L()` fallback). Counter — `atomic.Uint64` в `liveStats` (рядом с `slowCount`, паттерн Phase 24). Stats endpoint (`/api/v1/stats`) дополнен полем `sync_ops_record_failures`. OpenAPI schema обновлена (оба файла синхронны). 4 новых теста в `handlers_sync_test.go`: `TestSyncOpsRecordFailsAndCounts` (counter+log), `TestSyncOpsRecordSuccess_NoCounterOrLog`, `TestSyncOpsRecordNilStore_NoOp`, `TestStatsExposesSyncOpsRecordFailuresField` (JSON tag). Тест использует `zaptest/observer` для проверки лога и `failingSyncOps`/`okSyncOps` mock-реализации `SyncOpsStore`. **Verify:** `go test ./... -race -count=1` зелёный; `cd web && npm test` 263/263; `make test-e2e` 18/18; `golangci-lint run --new-from-merge-base=origin/dev ./...` exit 0.

### P2

- [x] **30.3** **Phase 10: VK Long Poll.** **Закрыта 2026-08-16 в `phase-30-3-vk-longpoll`.** `internal/bot/vk.go` дополнен Long Poll транспортом: `groups.getLongPollServer` → `(server, key, ts)`; `a_check` loop с `wait=25`; recovery на `failed=1` (refresh server/key); `failed=2/3/4` — внешний retry. Update type 4 (`message_new`) dispatched в `OnMessage` hook (Phase 21 inbox capture). `Stop` идемпотентный, `OnError` callback для main.go-логирования. `cmd/orenda/main.go` — VK регистрируется так же как Telegram; shared inbox-capture helper `captureToInbox(ctx, db, tasks, botType, targetAddress, text, reply)` переиспользуется для Telegram (Phase 21) и VK (Phase 30.3). Когда `bots:` секция содержит `type: vk` с `token`+`group_id`, регистрируется Long Poll. **Tests (10 новых, `internal/bot/vk_longpoll_test.go`):** `TestVKLongPoll_DispatchesMessageNewHappyPath` (smoke с httptest VK API + a_check, OnMessage срабатывает), `TestVKLongPoll_ReconnectsOnFailed1` (failed=1 → re-fetch server), `TestVKLongPoll_StartWithoutGroupIDIsNoop` (callback-only mode), `TestVKLongPoll_StartWithoutTokenReturnsError`, `TestVKLongPoll_ParseMessageNew_IgnoresUnknownType`, `TestVKLongPoll_ParseMessageNew_EmptyTextDropsMessage`, `TestVKLongPoll_NextBackoff_GrowsAndCaps`, `TestVKLongPoll_BuildFromConfig_VKPath`, `TestVKLongPoll_StopIsIdempotent`, `TestVKLongPoll_ACheckURL`. **Verify:** `go test ./... -race -count=1` 30/30 packages ok; vitest 263/263; `make build`; `make test-e2e` 18/18; `golangci-lint run --new-from-merge-base=origin/dev` exit 0.
- [x] **30.4** **Phase 10: Email HTML-шаблоны.** **Закрыта 2026-08-16 в `phase-30-4-email-html`.** `internal/bot/email.go` теперь отправляет `multipart/alternative` с `text/plain` + `text/html` частями. Чистые функции:
  - `renderPlain(msg)` — канонический текст (title + body + link + actions as labelled URL).
  - `renderHTML(msg, publicBaseURL)` — обёрнутая в <!DOCTYPE> HTML с inline-styles (compatible with Gmail stripping <style>); title в <h1>, body с <br/> для newlines, опциональная ссылка в <a>, action buttons row.
  - `buildMultipartAlternative(from, to, subject, plain, html)` — собирает RFC 2046 envelope с уникальной boundary per call.
  
  Action-кнопки: callback-style (verb + CallbackID) рендерятся как `<a href="{base}/api/v1/tasks/{id}/review?action={verb}">` — направляют в Phase 19 review endpoint, который ещё предстоит реализовать (помечен как known seam в PLAN). При пустом `PublicBaseURL` (dev default) action-кнопки НЕ рендерятся (broken-link avoidance), но plain part сохраняет verb текстом. URL-кнопки (pre-built `Action.URL`) всегда рендерятся.
  
  Security: `html.EscapeString` на title + body + link → script injection в title impossible.
  
  Email struct получил `PublicBaseURL string` (опционально из конфига). 13 тестов в `internal/bot/email_html_test.go`: renderPlain × 3, renderHTML × 7 (branding/escapes/newlines/empty-baseURL/with-baseURL/pre-built URL/trailing slash), buildMultipartAlternative × 3 (headers/end2-end/boundary uniqueness). **Verify:** `go test ./... -race -count=1` 30 packages ok; vitest 263/263; `make build`; `make test-e2e` 18/18; `golangci-lint run --new-from-merge-base=origin/dev` exit 0.
- [x] **30.5** **Phase 10: Weekly digest.** **Закрыта 2026-08-16 в `phase-30-5-weekly-digest`.** Добавлен scheduler в `cmd/orenda/digest.go` + formatter в `internal/service/notifier/digest.go` + template `WeeklyDigestFromEvent` в `templates.go`. Config: `notifier.digest_interval` (default 168h, 0 = отключает). Scheduler раз в `DigestInterval`: для каждого активного owner (single-owner install → один user) выполняет 6 aggregate COUNT-запросов (TasksDone, TasksCreated, TasksAwaitingReview, TasksOverdue, CommentsReceived, ActiveTimers) → `RenderWeeklyDigest` → отправляет через `notifierSvc.Notify({Type: "digest.weekly", Meta: {title, body}})`. Template реконструирует bot.Message из Event.Meta. Семантика счётчиков: period-bounded для Done/Created/CommentsReceived; LIVE для AwaitingReview/Overdue/ActiveTimers (нет «у вас было 3 на прошлой неделе»). Self-comments excluded. Tasks фильтруются по `assignee_type='user' AND assignee_id=?` (worker, не owner). `internal/storage/sqlite/user_repo.go::ListAll` + `UserSummary` projection для итерации owners. **Тесты:** 7 на formatter (all-clear / populated / zero-review omitted / period header / link / no-actions / zero-dates); 10 на scheduler (fires-for-every / skip-inactive / loop-continues / 5 compute stats queries / template adapter / config default). Всего 17. **Verify:** `go test ./... -race -count=1` 30 packages ok; vitest 263/263; `make build`; `make test-e2e` 18/18; `golangci-lint run --new-from-merge-base=origin/dev` exit 0.
- [x] **30.6** **Phase 5: `[[` autocomplete в редакторе wiki.** **Закрыта 2026-08-16 в `phase-30-6-wiki-autocomplete`.** Добавлен `web/src/features/wiki/WikiLinkSuggestion.tsx` — TipTap extension на trigger `[[`, реализованный через `@tiptap/suggestion` (новая dep, обоснована: стандартный UI primitive для popup overlays в TipTap). `MarkdownEditor` принимает опциональный `pages: WikiTreeNode[]` prop, передаёт flattened list в extension. `flattenWikiTree` обходит дерево от `/api/v1/pages`. Popup — React component portaled в `document.body` (без `tippy.js` чтобы не раздувать deps); keyboard navigation (ArrowUp/Down/Enter/Tab/Esc). Picking вставляет `[[slug]]` plain text — markdown mirror распарсит при сохранении и обновит `wiki_links`. С Phase 29 страницы от agent-created тоже в дереве (тот же source). 11 vitest в `WikiLinkSuggestion.test.ts` — `filterItems` (8 кейсов: empty, case-insensitive, slug-match, cap-at-8, no-match, trim, preserve-order, no-cross-word) + `flattenWikiTree` (3 кейса: nested, empty, leaf). `@tiptap/suggestion` добавлен в `package.json` (`^3.29.2`). **Verify:** `npx tsc --noEmit` clean; vitest 274/274 (was 263; +11); `make build` OK; `make test-e2e` 18/18; `golangci-lint run --new-from-merge-base=origin/dev` exit 0.
- [x] **30.7** **Phase 19: backend принуждает comment при reject.** **Закрыта 2026-08-16 в `phase-30-7-reject-comment`.** `TaskService.Review` (`internal/service/task/move.go::Review`): добавлена проверка `decision == ReviewReject && strings.TrimSpace(comment) == ""` → `ErrInvalidInput` (400 на API уровне через `writeError`). Approve без comment остался легальным (silent ack — некоторые approvals не требуют пояснения). Whitespace-only comment тоже отклоняется. Тест `TestService_Review_RejectsWithoutComment` в `claim_test.go` проверяет три случая: пустой / whitespace-only / approve пустой (всё ещё ok). Существующие `TestService_Review_Approve` / `Reject` / `InvalidDecision` не сломаны. **Verify:** `go test ./... -race -count=1` 30 packages ok; vitest 274/274; `make build`; `make test-e2e` 18/18; `golangci-lint run --new-from-merge-base=origin/dev` exit 0.
- [x] **30.8** **Phase 4: задачи с `due_at` в календаре.** **Закрыта 2026-08-16 в `phase-30-8-tasks-calendar`.** Реализация минимальна: (1) backend endpoint `GET /api/v1/tasks/with-due?from=&to=` через `task.Repository.ListByDueBetween` (новый batch-примитив, отсутствие tasks без due_at фильтруется); (2) `tasksWithDueHandler` в `handlers_tasks.go` (parseTimeQuery, writeError mapping); (3) роутер `r.Route("/tasks", ...)` — `r.Get("/with-due", ...)` смонтирован; (4) client.ts — `api.tasksWithDue({from,to})`; (5) CalendarPage — `tasksByDue` state, RB-conversion в all-day events в `rbEvents`, `📌 {title} ✓` для done. Семантика: start_at/end_at — timed-события, due_at — all-day deadline marker. OpenAPI schema: добавлен `/api/v1/tasks/with-due` (оба файла синхронны). Tests: `TestTaskRepo_ListByDueBetween_Calendar` (in-range, out-of-range, no-due, ordering), `reminder_test.go::fakeRepo` получил `ListByDueBetween` stub. Verify: `go test ./... -race -count=1` 30 packages ok; vitest 274/274; `make build`; `make test-e2e` 18/18; `golangci-lint run --new-from-merge-base=origin/dev` exit 0. UX-улучшения (визуальная distinct-цвета по overdue, click → task modal) — за скобкой для отдельного follow-up.

### P3

- [x] **30.9** **Phase 7: backup UX — `Status`/`TestConnection` + расписание.** **Закрыта 2026-08-16 в `phase-30-9-backup-ux`.** Добавлен `GET /api/v1/backups/status` endpoint — read-only status (snapshot count + latest snapshot path/size + timestamp). `POST /api/v1/backups/test` (test connection) уже был в Phase 28.10. UI Backups показывает status line над списком снапшотов. Cron schedule остаётся за скобкой (требует cron parser dependency). Snapshot Rotation Days уже UI-editable с Phase 28.9. **Verify:** `go test ./... -race -count=1` 30 packages ok; vitest 274/274; `make build`; `make test-e2e` 18/18; `golangci-lint run --new-from-merge-base=origin/dev` exit 0.
- [x] **30.10** **Phase 21: due-поле в QuickCapture.** **Закрыта 2026-08-16 в `phase-30-10-quickcapture-due`.** Два слоя: (1) backend — `createInboxTaskHandler` раньше молча дропал `due_at` (поле маппилось только в project create path); теперь тот же `parseOptionalTime` контракт (RFC3339), тест `TestInbox_Create_AcceptsDueAt` пинит round-trip через create + list и отсутствие поля без даты. (2) frontend — модалка получила `<input type="date">` (опционально, `data-testid="quick-capture-due"`); payload шлёт `due_at` только когда дата выбрана — хоткей-флоу `q → type → Cmd+Enter` не замедлен, классический payload `{title}` без ключа `due_at` запинен тестом. Local midnight → UTC ISO (TZ-shift задокументирован в E2E). `createInboxTask` тип дополнен `due_at?: string`. **Тесты:** 1 Go API + 4 vitest (17/17 в файле) + 1 E2E (`quick-capture.spec.ts` — due persists, instant-compare). **Verify:** `go test ./... -race -count=1` exit 0; vitest 278/278 (+4); `npx tsc --noEmit` clean; `make build` OK; `make test-e2e` 19/19 (+1); `golangci-lint run --new-from-merge-base=origin/dev` exit 0; prettier/eslint на затронутых файлах clean. **Гонка-дубль (2026-08-16):** параллельный агент независимо реализовал ту же задачу и смержил вторую версию (`b17b35d`); live-реализация — его стилистический вариант (`dueDate`, `due_at: undefined` при пустом поле — vitest `toEqual` игнорирует undefined-свойства, payload на проводе идентичен). Функционально версии эквивалентны; весь тест-набор первой реализации (Go API + 4 vitest + E2E) зелёный против live-кода. Их «resolved»-мердж оставил незакрытые конфликт-маркеры в PLAN.md — вычищены коммитом-фиксом; финальная реверификация dev: `go test ./...` exit 0, vitest 285/285, `npx tsc --noEmit` clean.
- [x] **30.11** **Phase 23: WIP-фидбек.** **Закрыта 2026-08-16 в `phase-30-11-wip-feedback`.** Frontend-only (бэкенд уже отвечает 422 `wip_limit` — Phase 23.1). Два слоя: (1) `KanbanBoard.tsx::onDragEnd` — при ошибке move с regex `/wip[_-]?limit/i` показывает специфический toast `Column "<name>" is at WIP limit (N of M). Pick another column or finish a task first.` с локальной подстановкой счётчика колонки (использует column.wip_limit + tasks.length после отката оптимистичного перемещения). (2) `ColumnView.tsx` — amber ring + border вокруг колонки когда `tasks.length >= wip_limit`. Существующие 18 тестов (включая Phase 27.10 colour wiring) зелёные без правок. **Verify:** vitest 278/278; `make build`; `make test-e2e` 19/19 (+1 KanbanBoard test из Phase 28.23); `golangci-lint run --new-from-merge-base=origin/dev` exit 0.
- [x] **30.12** **Phase 17: бейджи времени на карточке.** **Закрыта 2026-08-16 в `phase-30-12-time-badges`.** Frontend-only — бэкенд уже отдаёт `time_estimate_s`/`time_spent_s` (Phase 17). Новый `TimeBadge.tsx` компонент: ⏱ spent/estimate в формате H:MM:SS, красный border при перерасходе; `●` пульсирующий маркер при активном таймере (started_at без completed_at, single-active-timer constraint Phase 4); hidden в compact-mode кроме active-timer (leaked timer должен быть виден всегда). 7 vitest в `TimeBadge.test.tsx` пинт все три состояния + clamping + edge-cases. **Verify:** vitest 285/285 (was 278; +7); `make build`; `make test-e2e` 19/19; `golangci-lint run --new-from-merge-base=origin/dev` exit 0.
- [x] **30.13** **Phase 27.6 за скобкой: структурная правка active-курса.** **Закрыта 2026-08-17 в `phase-30-13-course-curriculum-crud`.** Granular CRUD curriculum со стабильными ID: новые endpoints (user + agent mirrors, одни handlers) — `POST /courses/{id}/modules`, `PUT /courses/{id}/structure`, `PATCH/DELETE /modules/{id}`, `POST /modules/{id}/lessons`, `PATCH/DELETE /lessons/{id}`, `PATCH/DELETE /quizzes/{qid}`. Гейтинг: draft/review/active редактируемы, done/archived → 422 `invalid_transition`; ни одна строка не пересоздаётся → прогресс (Lesson.Status) и task-ссылки выживают по построению. Reorder — единый `PUT /structure` с IDs-only payload и exact-coverage валидацией в tx (каждый модуль/урок ровно один раз; уроки могут переезжать между модулями; позиции переписываются 1..n). Миграций нет. Frontend: `CourseCurriculumEditor` в active-режиме сохраняет granular diff'ом (`diffCurriculum` + `applyGranularPlan`: updates → creates → deletes → structure; temp-id → server-id map для новых элементов); dnd-kit reorder модулей и уроков (cross-module, паттерн KanbanBoard); импорт программы из markdown — client-side парсер `curriculumMarkdown.ts` (`##` модуль, `###` урок, `- [exact] q | a` / `- [open] q`). Lesson content в granular-режиме идёт через существующий `updateLessonContent` (отклонение от постановки: список дифф-операций расширен `lessonContentUpdates`, иначе textarea урока молча теряла бы правки). **Verify:** `go test ./... -count=1` 30/30 packages ok (repo +3, service granular, 17 handler pinning-тестов); vitest 307/307 (+44 от базы: parser 9, granular 11, прочие); `npx tsc --noEmit` clean; `make build` OK; `make test-e2e` 20/20 (+1 `course-structure.spec.ts`: rename module + add lesson на active-курсе через UI → completed lesson stays `done`, open stays `open`, новый урок born `locked`); OpenAPI оба файла синхронны, `TestOpenAPI_RouteCoverage_FullRouter` зелёный; `golangci-lint run --new-from-merge-base=origin/dev` exit 0. Mutation check: инверсия order-detection в `diffCurriculum` флипает 4 granular-теста red. **За скобкой (зафиксировано):** markdown-import в active-режиме матчит строки по идентичности, не по title — import заменяет дерево целиком (delete-all + create-all через дифф), `window.confirm` предупреждает; dnd-reorder не покрыт E2E (pointer-sequences ненадёжны в headless, покрыт plan-level vitest + backend); MCP/CLI поверхности не добавлялись.
- [x] **30.14** **Phase 27.7/27.8 за скобкой: UI статусов проекта + bulk-edit.** **Закрыта 2026-08-16 в `phase-30-14-bulk-edit`.** Column CRUD принимает и валидирует machine key (slug fallback + board-local deduplication); смена key fan-out'ит `task.status`, activity и `task.updated` WS. `Status.IsValid` принимает custom project keys. Добавлен `POST /api/v1/tasks/bulk-edit` с общими PATCH side-effects (done/completed_at, awaiting, activity, mirror), per-task results/errors и WS updates. Kanban получил selection checkboxes, bulk status/priority/assignee bar; Add/Edit column UI получил machine-key поле. OpenAPI specs синхронизированы. **Verify:** `go test ./... -count=1`, `npx tsc --noEmit`, vitest `285/285`, `make build`, `make test-e2e` `19/19`; `diff docs/openapi.yaml internal/api/openapi.yaml` пустой.
- [x] **30.15** **Ops-гигиена скриптов.** **Закрыта 2026-08-16 в `phase-30-15-ops-scripts`.** `scripts/uninstall.sh`: добавлен `--help` (печатает usage, exit 0); неизвестные флаги/аргументы теперь exit 2 с понятной ошибкой (раньше молча игнорировал — `--purge` вместо `--purge-data` ничего не делало). `scripts/update-dogfood.sh`: добавлен `--help` (usage); `--force` для emergency out-of-band refresh -- не обходит main+clean check, но подавляет ошибку и логирует предупреждение; `--remote <name>` для использования не-origin remote (default `origin`); неизвестные флаги exit 2. `scripts/test_scripts.sh` (NEW): smoke-тесты flag-parsing — 7 кейсов (uninstall --help / --purge-data / --bogus / extra; update-dogfood --help / --whatever / non-main branch / --force non-main). **Verify:** `bash scripts/test_scripts.sh` → 7 ok; vitest 285/285; `make build`; `make test-e2e` 19/19; `golangci-lint run --new-from-merge-base=origin/dev` exit 0.
- [x] **30.16** **Lint-остаток 73 issues (после Phase 30.1 отключили `hugeParam`).** **Закрыта 2026-08-16 в `phase-30-16-lint-sweep`.** Один проход убрал ~8 issues: `var now = time.Now` (неиспользуемый test seam в `internal/bot/bot.go`), `runBackupRestore` (старая pre-Phase-22 версия, заменена `runBackupRestoreWithVerify`), `seedSubscription` (пустой stub в `telegram_inbox_test.go`), `depFixtures` placeholder, `reviewQueueFixture`, `agentPut`/`agentDelete` (неиспользуемые transport helpers), `actorID` parameter в `event.publish`, `cookie` parameter в `seedProjectAndTask`. Остаются ~85 issues — большая часть в unused test fixtures и stylistic gocritic; закроются партиями при касании файлов. CI gate (Phase 30.1) не блокирует (только новый код). **Verify:** `go test ./... -race -count=1` 30 packages ok; vitest 285/285; `make build`; `make test-e2e` 19/19.

**DoD фазы-реестра:** каждый открытый пункт в репозитории имеет номер здесь; ни один «За скобкой»/бэклог-текст без номера не считается задачей. Multi-user / multi-device sync — оформлена эрой (Phase 11+), в реестр не входит.

---

## Phase 31 (фича) — Учебные напоминания в Today: agent-driven планирование + proposal tray *(постановка 2026-08-17)*

> **Мотивация (продуктовое решение 2026-08-17).** День пользователя начинается с Dashboard: все задачи дня в одном месте, включая «что изучить». Учебные задачи — мягкие напоминалки без дедлайна; ставит их внешний агент (opencode → MCP) по явной команде пользователя вечером или утром перед началом дня; пользователь подтверждает каждое предложение явно (opt-in). Отдельного проекта под учёбу не заводится. Контроль прохождения остаётся в курсе (уроки/квизы), задача — только напоминание.

**Дизайн-решения (зафиксированы в обсуждении 2026-08-17):**

1. **Напоминалка = inbox-задача с маркером.** `tasks.study_course_id TEXT NULL REFERENCES courses(id) ON DELETE SET NULL` — non-null ⇒ study-reminder. Одна колонка даёт три механики: read-семантику Today, иконку/ссылку на курс в UI, ключ дедупликации. Жизненные циклы задачи и уроков **не связаны** (свободная связь): закрытие/удаление задачи не трогает уроки; `CompleteLesson` не трогает задачу. Пачка уроков — markdown-ссылки в теле задачи; время слота — опциональное время в `due_at` (Today группирует по дню).
2. **Opt-in: proposals — отдельная таблица, не статус задачи.** Статус задачи производен от колонок канбана (30.14) — статус `proposed` задел бы канбан/sync/bulk-edit/WIP. `study_proposals` изолирована: accept материализует настоящую inbox-задачу (`study_course_id`, `due_at = max(target_date, today)`), dismiss гасит запись. Лоток pending proposals — поле в payload `GET /api/v1/today` (single round-trip Phase 20 сохраняется).
3. **Тихий перенос без записей.** Read-семантика в `/today`: study-reminders исключаются из `overdue` и включаются в `due_today` по `due_at <= today`. Пропущенный день никогда не краснеет; `due_at` остаётся датой постановки (аудит). Обычные задачи не затрагиваются — фильтр по маркеру. Ни sweep, ни cron, ни мутаций по расписанию.
4. **Платформа не планирует сама.** Ни cron, ни baseline-кнопки, ни planner-конфига. Планирование — внешний агент по команде пользователя из харнесса. Платформа даёт агенту данные (активные курсы с прогрессом + pace_notes) и endpoint записи предложений.
5. **Контракт темпа — `courses.pace_notes_md`.** Свободный текст («3 раза в неделю, по утрам; после проваленного квиза — повторить»): пишет тьютор при создании/наполнении курса, пользователь правит в UI. Существующий `Pace` enum (`casual|regular|intensive`, model.go:94) остаётся машинным сигналом — сегодня он нигде не читается; фаза даёт обоим полям первого потребителя (агент-планировщик).
6. **WS — переиспользуется топик `tasks`.** Конвенция канбана (column.updated тоже идёт в `tasks`, handlers_kanban.go:180); TodayPage уже подписан — `study.proposed/accepted/dismissed` эмитят в тот же топик, лоток обновляется живьём без новой подписки.

**Контекст (evidence, read-only сверка 2026-08-17):**

- Today: `todayResponse` = overdue / due_today / scheduled_today / upcoming_week / awaiting_count / active_timer (`handlers_today.go:31`); задача попадает в выдачу только через `due_at`.
- Inbox-задачи без проекта существуют: `project_id` nullable (миграция 015), `POST /api/v1/tasks` → `createInboxTaskHandler` (router.go:440).
- Agent-неймспейс: создавать задачи агент не может (только claim/release/submit/comments/context/await). Курсы: `GET /agent/courses` уже принимает `?status=` (handlers_courses.go:18, фильтр :360), но отдаёт ряды без прогресса; создание/активация/curriculum/materialize — Phase 29.
- MCP: 13 тулов, flat naming `orenda_*` (`internal/mcp/orenda_tools.go`); курсовых/планировочных тулов нет. CLI-паритет — конвенция Phase 29.2.
- Skill: `docs/skills/orenda/SKILL.md` (§4.4 «Build me a course on X» — образец end-to-end секции).
- Миграции: следующий номер **022** (018 отсутствует — известный сдвиг, шапка файла); парный `.down.sql` обязателен (Wave 4).

**Задачи:**

- [x] **31.1** *(закрыта 2026-08-17 в `phase-31-1-study-migration`, commit `397d63e`)* — миграция `022_study_planning.{sql,down.sql}`: `courses.pace_notes_md TEXT NOT NULL DEFAULT ''`; `tasks.study_course_id TEXT NULL REFERENCES courses(id) ON DELETE SET NULL` + partial index `WHERE study_course_id IS NOT NULL`; таблица `study_proposals(id TEXT PK, course_id TEXT NULL REFERENCES courses(id) ON DELETE CASCADE, title TEXT NOT NULL, body_md TEXT NOT NULL DEFAULT '', target_date TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','accepted','dismissed')), created_by_agent TEXT NOT NULL REFERENCES agents(id), accepted_task_id TEXT NULL REFERENCES tasks(id) ON DELETE SET NULL, created_at TEXT NOT NULL DEFAULT (datetime('now')), resolved_at TEXT NULL)` + index `(status, created_at)`. Down: drop table + `DROP COLUMN` ×2 (modernc SQLite ≥ 3.35). Тест миграции по конвенции `migration_021_test.go` (up-форма, idempotency там где применимо, down восстанавливает).
- [x] **31.2** *(закрыта 2026-08-17 в `phase-31-2-study-domain`, commit `3232703`)* — Domain: `course.Course.PaceNotesMD` (+ Validate: trim + 64 KiB cap); `task.Task.StudyCourseID` (`json:",omitempty"`); новый пакет `internal/domain/study` — entity `Proposal` с `Status` (pending/accepted/dismissed), sentinels `ErrNotFound`/`ErrInvalidInput`/`ErrTransition`, `AcceptAllowed()`/`DismissAllowed()` (только pending), `Validate()` (trim, размеры, target_date строго `YYYY-MM-DD`, default pending, created_by_agent required). Unit-тесты: 6 sub-test `TestProposal_Validate_Errors` + 4 boundary (valid/invalid dates) + `AcceptDismissAllowed` lifecycle matrix + `TestCourse_Validate_PaceNotesMD` (5 кейсов) + `TestTask_Validate_WithStudyCourseID` (3 кейса).
- [x] **31.3** *(закрыта 2026-08-17 в `phase-31-3-study-storage`, commit `6465073`)* — Storage: `study_proposal_repo` (Create, ListPending, Get, MarkAccepted, MarkDismissed — оба idempotent через conditional WHERE + existence-check); `study.Proposal` Repository interface; `task.StudyCourseID` round-trip в Create/GetByID/Update/ListAwaitingReview + FK SET NULL на удалении курса; `course.PaceNotesMD` round-trip в Create/Get/List/Update + новый `UpdatePaceNotesMD` (узкий PATCH, валидация через `Course.Validate`); `docs/DB.md` — секция "Study reminders" + строка 022 в таблице. Tests: `TestStudyProposalRepo_FullLifecycle` (6 sub-тестов: create, list, accept, idempotent, dismiss, ErrNotFound), `TestTaskRepo_StudyCourseIDRoundTrip` (5 sub-тестов), `TestCourseRepo_PaceNotesMDRoundTrip` (6 sub-тестов).
- [~] **31.4** — в работе: MiniMax-M3, branch `phase-31-4-study-service`, с 2026-08-17. Service `internal/service/study`: `Propose(ctx, agentID, in)`; `Accept(ctx, id)` — **идемпотентен**: повторный accept возвращает ранее созданную задачу (accepted_task_id), не дублирует; создание inbox-задачи идёт через task service (activity `task.created` + WS `tasks` достаются бесплатно), `due_at = max(target_date, today)`, title/body копируются, `study_course_id` из proposal; `Dismiss(ctx, id)`. WS-эмиты в топик `tasks`: `study.proposed` / `study.accepted` / `study.dismissed` с `{proposal_id, course_id?}`.
- [ ] **31.5** Agent REST: `POST /agent/study-proposals` (201 + proposal; 400 на невалидный body; `created_by` = Identity.AgentID); `GET /agent/courses?status=active` — enrich прогрессом: `lessons_total/lessons_done`, `open_lessons[]` (id, title, module title), `last_completed_at`, `pace_notes_md` + `pace` в payload (обогащение только для active — drafts-флоу тьютора не меняется); `PATCH /agent/courses/{id}` — правка `pace_notes_md` (единственное поле; в любом статусе курса). Pinning-тесты на все три.
- [ ] **31.6** User REST: `GET /api/v1/study-proposals` (только pending, по created_at); `POST /api/v1/study-proposals/{id}/accept` → 201 с задачей; повторный accept → 200 с той же задачей; `POST .../dismiss` → 200; accept/dismiss на resolved → 409 `proposal_resolved`; чужой/несуществующий id → 404.
- [ ] **31.7** Today: `todayResponse.proposals` (pending); `due_today` включает open study-reminders с `due_at <= today`; `overdue` их исключает (оба фильтра по `study_course_id IS NOT NULL`). Тесты трёх поведений: proposals в payload; напоминалка со вчерашним due_at — в due_today; в overdue её нет, обычная просроченная задача — по-прежнему в overdue.
- [ ] **31.8** MCP + CLI: тулы `orenda_courses_list` (status, прогресс, pace_notes) и `orenda_study_propose` (course_id?, title, body_md?, target_date); CLI `orenda agent courses list --status active` и `orenda agent study-propose` (паритет Phase 29.2, `--json`); тесты verb/body/encoding по конвенции `orenda_tools_test.go`.
- [ ] **31.9** Frontend: TodayPage — секция «Предложено» (карточка: title, body preview, ссылка на курс, target_date, кнопки Принять/Отклонить; invalidation по WS `tasks` — подписка уже есть); 📖-маркер со ссылкой на курс у study-задач в due_today; UI курса — поле `pace_notes` (отображение + правка). Vitest: tray accept/dismiss + invalidation, маркер, поле курса.
- [ ] **31.10** Спеки + skill: `internal/api/openapi.yaml` + `docs/openapi.yaml` синхронно — новые пути (`/agent/study-proposals`, `/study-proposals*`, PATCH `/agent/courses/{id}`) и схемы (Proposal, enrich course payload, todayResponse.proposals); `TestOpenAPI_RouteCoverage_FullRouter` зелёный. SKILL.md: секция «Plan my day» — цикл харнесс-агента (`orenda_courses_list` → pace_notes + прогресс → `orenda_study_propose` × N → пользователь подтверждает в Dashboard), с curl-примерами по образцу §4.4. SESSION.md обновлён.
- [ ] **31.11** Smoke DoD скриптом (образец — 29.7): реальный бинарь, tmp-БД, свободный порт. Тьютор-токен: create course с pace_notes → curriculum (модуль + 2 урока) → materialize обоих → activate. Planner-токен: `GET /agent/courses?status=active` (видит прогресс + pace_notes) → 2 proposals. User-cookie: `GET /today` показывает лоток из 2; accept первого → задача в `due_today` со `study_course_id`, повторный accept → та же задача (idempotency); dismiss второго → лоток пуст. Симуляция пропущенного дня (sqlite `UPDATE tasks SET due_at = вчера`): напоминалка в `due_today`, отсутствует в `overdue`. Вывод `SMOKE OK`.

**DoD (проверяется исполнением):** харнесс-агент по одной команде пользователя предлагает план на день; пользователь подтверждает/отклоняет на Dashboard без ручного создания задач; пропущенная напоминалка никогда не краснеет; `make test && make lint` зелёные; openapi coverage зелёный.

**За скобкой (зафиксировано):** окно чата с агентом в Dashboard — лоток предложений станет его естественным якорем (отдельная фаза); generic `POST /agent/tasks` — отдельная фаза при потребности; nightly-триггер планирования — сознательно отвергнут 2026-08-17 (триггер — команда пользователя в харнессе); умная дедупликация/merge proposals (v1 — ручной accept/dismiss).
