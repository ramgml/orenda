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

## Phase 0 — Инициализация *(1–2 дня)*

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

## История версий плана

| Дата | Версия | Изменения |
|------|--------|-----------|
| 2026-08-08 | 0.1.0 | Начальная версия |
| 2026-08-11 | 0.2.0 | Phase 12: кастомные колонки канбана (create/reorder/rename UX) |
| 2026-08-11 | 0.3.0 | Phase 13: теги и цветовые метки задач |
| 2026-08-11 | 0.4.0 | Phase 14: разделение subtasks/checklists по смыслу (Weeek-style) |