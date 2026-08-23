# Orenda

> **Local-first productivity suite**, где AI-агенты — полноправные участники. Задачи, проекты, календарь, база знаний — всё в вашей жизни, на вашей машине.

*Имя — от ирокезского «orenda» — внутренняя сила, пронизывающая всё сущее.*

[English version](README.md)

## Зачем Orenda?

В стандартных task-менеджерах AI — внешний инструмент, приклеенный через интеграции. В Orenda агенты — **полноправные участники workflow**: создают задачи, берут в работу, оставляют комментарии, получают контекст от владельца. Человек — владелец, ревьюер, инициатор.

## Стек

- **Backend:** Go 1.22+ (chi, modernc.org/sqlite, JWT, gorilla/websocket, cobra)
- **Frontend:** React 18 + TypeScript + Vite + Tailwind + shadcn/ui
- **БД:** SQLite (WAL, FTS5, pure-Go через modernc.org/sqlite, без CGO)
- **Backup:** git-зеркало + sqlite-снапшоты (настраиваемый remote; hot-reload с 28.9)
- **Уведомления:** подключаемые боты (VK, Telegram, Email, Webhook, Console)
- **Realtime:** WebSocket-хаб (cookie-auth, 8 топиков) + long-poll fallback для агентов
- **Agent DX:** REST + MCP-сервер (Streamable HTTP) + `orenda agent` cobra CLI
- **Дефолты безопасности:** bcrypt-12 пароли, opaque API-токены, JWT-cookie 24ч, rate-limiting, строгий CSP, opt-in pprof только на 127.0.0.1
- **PWA:** Workbox service worker, IndexedDB outbox, `/api/v1/sync` flush

## Быстрый старт

```bash
# Установить зависимости
make web-install

# Собрать и запустить
make build
./bin/orenda migrate up
echo "hunter2!" | ./bin/orenda user create \
    --email you@example.com --display-name You --password-stdin \
    --config data/config.yaml
ORENDA_AUTH__JWT_SECRET=$(head -c32 /dev/urandom | base64) ./bin/orenda serve
# → http://127.0.0.1:2137
```

Или установка одной командой:

```bash
make web-install               # обязательно один раз — установщик собирает SPA
scripts/install.sh --systemd   # собирает, ставит в ~/.local/bin, включает user service
```

> `scripts/install.sh` — **единственный** санкционированный способ обновить
> usage-бинарник. Он отказывается ставить из чего-либо, кроме чистого
> checkout на `main` (переопределяется флагом `--force`). См.
> [docs/ARCHITECTURE.md §12.4](docs/ARCHITECTURE.md#124-dev-vs-dogfood-instance-phase-2820).

Для разработки с hot reload:

```bash
make dev
# → Vite dev-server: http://localhost:5173 (проксирует API на :2138)
# → Go-сервер: http://127.0.0.1:2138
```

> Phase 28.20 разделяет dev (`:2138`) и usage (`:2137`), чтобы оба могли
> работать на одной машине. Usage/dogfood-инстанс собирается из отдельного
> checkout на `main`; модель каналов — в
> [docs/ARCHITECTURE.md §12.4](docs/ARCHITECTURE.md#124-dev-vs-dogfood-instance-phase-2820),
> обновление одной командой — `scripts/update-dogfood.sh`.

Проверка кодовой базы перед открытием PR:

```bash
make test              # Go + vitest (с кэшем, быстро)
make test-full         # Полный прогон без кэша (CI backstop / release gate)
make lint-new          # golangci-lint только на НОВЫЙ код (гейт pre-push)
make lint              # полный lint (golangci-lint + eslint) — показывает существующий долг
make test-e2e          # Playwright на свежем embedded-билде на :21371 (18 тестов / 13 спеков)
make govulncheck       # скан по Go vulnerability DB
```

**Локальные гейты — git hooks (Phase 32.6).** Устанавливаются один раз на clone
(идемпотентно — безопасно перезапускать):

```bash
make hooks   # выставляет core.hooksPath = scripts/git-hooks (общий git config;
             # все текущие и будущие worktree наследуют его)
```

После этого каждый `git commit` запускает `pre-commit` (`gofmt -l` +
`prettier --check` на staged-файлах, <2 с), а каждый `git push` — `pre-push`
(`make lint-new` + `make test`, ~1 мин). `--no-verify` запрещён; используйте
`SKIP_ORENDA_HOOKS=1` только для явных, названных исключений. См.
[AGENTS.md](AGENTS.md#local-gates--git-hooks-phase-326) и wiki-страницу
[ci-local-gates-hooks](http://localhost:2137/wiki/ci-local-gates-hooks).

## Возможности

- 📋 Проекты, доски, kanban с drag-and-drop, колонки-как-статусы (Phase 27.8)
- ✅ Задачи со статусами (backlog → todo → in_progress → review → done)
- 🤖 AI-агенты с API-токенами, атомарный claim, heartbeat, граф блокировок
- 💬 Комментарии, вложения, упоминания между пользователем и агентами, аудит авторства агента
- 📅 Календарь (события + задачи с дедлайнами, развёртка RRULE, WIP-лимиты)
- 📚 Wiki с markdown, wiki-ссылками, backlinks, поиск FTS5 BM25
- 🎓 Персональные LMS-курсы — собранные AI-тьютором или вручную (LessonPage, квизы exact/open)
- 🔍 Очередь ревью — работа агентов, ждущая вашего решения, в один клик
- ⏱️ Учёт времени: таймер + ручные записи, страница-драйвер /today
- 🔔 Подключаемые уведомления (VK, Telegram, Email, Webhook, Console)
- 💾 Git-based бэкапы (GitHub, Bitbucket, SourceCraft, custom) + sqlite .backup + WAL-архив + восстановление из UI
- 📱 PWA (offline-first) — IndexedDB outbox, sync flush
- ⚡ Живые обновления UI по WebSocket на 8 топиках (tasks, agents, attachments, comments, events, notifications, timers, wiki)
- 🔐 Две параллельные модели аутентификации: cookie JWT (UI) и Bearer API-token (агенты)
- 🛠️ `orenda agent` CLI + MCP-сервер (Streamable HTTP) для tool-using агентов

## Документация

- [PRD](docs/PRD.md) — Product Requirements Document (видение)
- [PLAN](docs/PLAN.md) — фазы разработки ≤ 32 (❄️ замороженный архив; живой backlog — dogfood-инстанс, см. [DOGFOOD](docs/DOGFOOD.md))
- [ARCHITECTURE](docs/ARCHITECTURE.md) — что в бинарнике, справочник data-flow
- [CONTEXT](docs/CONTEXT.md) — доменные концепции (kanban, курсы, делегирование)
- [API](docs/API.md) — справочник REST API (+ [openapi.yaml](docs/openapi.yaml))
- [DB](docs/DB.md) — схема базы данных (по миграциям)
- [SESSION](docs/SESSION.md) — снапшот сессии (❄️ замороженный архив; текущее состояние живёт в dogfood-инстансе)
- [AGENTS.md](AGENTS.md) — гайдлайны для AI-агентов, расширяющих кодовую базу
- [SKILL](docs/skills/orenda/SKILL.md) — workflow и этикет агента

## Roadmap

| Фаза | Статус | Описание |
|-------|--------|-------------|
| 0 — Init | ✅ | Скелет проекта, healthcheck |
| 1 — Core | ✅ | Пользователи, auth, проекты, CRUD задач |
| 2 — Kanban | ✅ | Доски, drag-and-drop, WS |
| 3 — Agents + Collaboration | ✅ | Agent API, комментарии, упоминания, long-poll |
| 4 — Calendar + Time | ✅ | События, повторения, таймер |
| 5 — Wiki + Search | ✅ | Страницы, wiki-ссылки, FTS5 |
| 6 — Notifications (facade) | ✅ | In-app + абстракция ботов |
| 7 — Backups | ✅ | Git-зеркало + sqlite-снапшоты + restore |
| 8 — PWA | ✅ | Офлайн-поддержка, IndexedDB outbox, /sync flush |
| 9 — Polish (initial) | ✅ | Тесты, security headers, установщик, тёмная тема |
| 10 — Bot platform | ✅ | VK, Telegram, Email, Webhook |
| 11–27 | ✅ | Projects UI, колонки kanban, теги, зависимости, inbox, богатые карточки, LMS-курсы, очередь ревью, today, quick capture, restore, OpenAPI, agent CLI + MCP, E2E-сьют — см. [PLAN](docs/PLAN.md) |
| 28 (Polish backlog close-out) | ✅ | Settings hub, скролл TaskModal, дефолты безопасности (JWT 24ч, Secure из конфига), эмиссия activity, Bot.Stop на shutdown, opt-in pprof, таргет govulncheck, Prettier, hot-reload backup, ужесточение CSP, ARCHITECTURE.md |
| 30.1 (CI) | ✅ | GitHub Actions: `lint` → `test` → `build` → `e2e`. PR-гейт был инкрементальным (`--new-from-merge-base`); release-ветка (`main`) получала полный lint; 73 pre-existing lint-проблемы остались (см. [PLAN](docs/PLAN.md) §30.16). Замещено Phase 32.6 — PR-to-dev молчаливый, полный release-гейт на main/теги `v*`, test-only backstop на push в dev |
| 32.6 (local CI hooks) | ✅ | Per-PR enforcement переехал с GitHub Actions на локальные git hooks (`scripts/git-hooks/{pre-commit,pre-push}`, ставятся через `make hooks` → `core.hooksPath`). `pre-commit`: gofmt -l + prettier --check на staged-файлах (<2 с). `pre-push`: `make lint-new` (golangci-lint --new-from-merge-base=origin/dev) + `make test` (~1 мин). GitHub Actions теперь запускает только release-гейт; PR-to-dev намеренно молчаливый. `--no-verify` запрещён. См. [wiki:ci-local-gates-hooks](http://localhost:2137/wiki/ci-local-gates-hooks) и [AGENTS.md](AGENTS.md#local-gates--git-hooks-phase-326) |
| 30.2 (sync_ops observability) | ✅ | Ошибки `sync_ops.Record()` теперь инкрементируют `sync_ops_record_failures` в `/api/v1/stats` и эмитят `zap.Warn` с client/server id — больше никакого молчаливого цикла реплея PWA outbox |
| 30.3 (VK Long Poll) | ✅ | VK-бот теперь long-poll'ит `groups.getLongPollServer` + `a_check` для входящих сообщений (альтернатива Callback API; работает за NAT). `bots[].type: vk` с `token` + `group_id` регистрирует цикл. События `message_new` текут в тот же inbox-capture helper, что и Telegram (Phase 21) |
| 30.4 (Email HTML) | ✅ | Email-бот отправляет `multipart/alternative` (text + HTML). HTML-часть с inline-стилями в бренде Orenda, кнопками действий ревью (когда задан `PublicBaseURL`) и HTML-экранированием против инъекций скриптов. Plain-часть сохраняется для доступности / plain-only клиентов |
| 30.5 (Weekly digest) | ✅ | Фоновый тикер (по умолчанию 168ч) шлёт еженедельную сводку каждому подписанному оператором боту: задачи done / created / awaiting / overdue, полученные комментарии, активные таймеры. `notifier.digest_interval <= 0` отключает |
| 30.6 (wiki [[ autocomplete) | ✅ | В wiki-редакторе ввод `[[` открывает попап со списком всех страниц; выбор вставляет `[[slug]]`. Зеркало парсит его при сохранении и записывает `wiki_links`, так что backlinks работают |
| 30.7 (reject needs comment) | ✅ | `POST /tasks/{id}/review {decision: "reject", comment: ""}` → 400 `invalid_input`. Approve без комментария по-прежнему разрешён (молчаливый ack). Агент теперь всегда знает, *почему* задачу вернули на доработку |
| 30.8 (tasks on calendar) | ✅ | Задачи с `due_at` рендерятся как all-day маркеры на календаре (`📌 Title ✓` для done). Новый endpoint `GET /api/v1/tasks/with-due?from=&to=` питает полосу дедлайнов календаря |
| 30.9 (backup status) | ✅ | `GET /api/v1/backups/status` возвращает число снапшотов + путь/размер последнего + timestamp; Settings → Backups показывает число и последний timestamp. Cron-парсер остаётся отложенным |
| 30.10 (QuickCapture due) | ✅ | Вызванное по хоткею модальное окно QuickCapture теперь имеет опциональный `<input type="date">` для установки дедлайна захваченной задачи. Оставьте пустым для захвата в одно нажатие; выберите дату, чтобы запланировать идею |
| 30.11 (WIP feedback) | ✅ | Перетаскивание в колонку на WIP-лимите теперь показывает конкретный toast со счётчиком N-из-M колонки (вместо сырой ошибки бэкенда). Колонки на лимите подсвечены янтарным кольцом, так что узкое место видно без открытия заголовка колонки |
| 30.12 (time badges) | ✅ | `TaskCard` показывает ⏱ spent/estimate в H:MM:SS (красным при перерасходе) и пульсирующий маркер ● при открытом single-active-таймере. Утечшие таймеры остаются видимыми даже в компактном режиме |
| 30.15 (ops scripts) | ✅ | `uninstall.sh` отклоняет неизвестные флаги (раньше молча отбрасывал их) и имеет `--help`. `update-dogfood.sh` имеет `--help`, `--force` и `--remote <name>`. Smoke-тесты в `scripts/test_scripts.sh` покрывают парсинг флагов обоих скриптов |
| 30.16 (lint sweep) | ✅ | Первый механический проход закрыл ~8 pre-existing lint-проблем (неиспользуемый тестовый шов `var now`, мёртвый `runBackupRestore`, пустой стаб `seedSubscription`, placeholder'ы `depFixtures`/`reviewQueueFixture`, неиспользуемые `agentPut`/`agentDelete`, параметр `actorID` в публикации событий, параметр `cookie` в `seedProjectAndTask`). ~85 проблем остаются — закрываются оппортунистически |
| 30.17 (writeError bug) | ✅ | `writeError` теперь маппит `taskservice.ErrInvalidInput` в 400 `invalid_input` (было 500). Новый API-тест фиксирует три режима отказа. Закрывает acceptance-пробел Phase 30.7 — фронтенд получал 500 на валидационном отказе |

> Скриншоты: не включены в репозиторий (держим его лёгким — без бинарных blob'ов).
> Запустите `make build && bin/orenda serve` и откройте четыре ключевые страницы:
> `/`, `/inbox`, `/courses`, `/settings`, чтобы увидеть текущий UI.

## Лицензия

MIT (TBD)
