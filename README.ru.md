# Orenda

> **Local-first productivity suite**, где AI-агенты — полноправные участники. Задачи, проекты, календарь, база знаний — всё в вашей жизни, на вашей машине.

*Имя — от ирокезского «orenda» — внутренняя сила, пронизывающая всё сущее.*

[English](README.md)

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

# Alternative (Task 138): keep the secret out of /proc/*/environ —
# write it to a file once, then point ORENDA_AUTH__JWT_SECRET_FILE at it
# (direct ORENDA_AUTH__JWT_SECRET still wins when both are set):
printf '%s' "$(head -c32 /dev/urandom | base64)" > data/credentials/jwt
ORENDA_AUTH__JWT_SECRET_FILE=$PWD/data/credentials/jwt ./bin/orenda serve
```

Или установка одной командой:

```bash
make web-install               # обязательно один раз — установщик собирает SPA
scripts/install.sh --systemd   # собирает, ставит в ~/.local/bin, включает user service
```

### Установка через AI-агента (промт)

Вставьте этот промт вашему AI-агенту (Claude, Codex, Cursor, …), чтобы он установил и настроил Orenda:

```text
Установи Orenda (https://github.com/ramgml/orenda) на эту машину:
1. Склонируй репозиторий в ~/opt/orenda и переключись на последний релизный тег (git describe --tags --abbrev=0 на origin/main).
2. Запусти `make web-install` (нужен Node.js >= 24.11) для сборки веб-SPA.
3. Запусти `make build` — получится ./bin/orenda.
4. Запусти `./bin/orenda migrate up`.
5. Создай пользователя: `echo "<пароль>" | ./bin/orenda user create --email <email> --display-name <имя> --password-stdin --config data/config.yaml`.
6. Запусти сервер со сгенерированным JWT-секретом:
   ORENDA_AUTH__JWT_SECRET=$(head -c32 /dev/urandom | base64) ./bin/orenda serve
7. Для постоянной установки вместо шагов 2–6 выполни `scripts/install.sh --systemd` (ставит в ~/.local/bin и включает user service на http://127.0.0.1:2137).
8. Проверь: `curl -s http://127.0.0.1:2137/healthz` (или открой http://127.0.0.1:2137 в браузере) — страница логина должна загрузиться.
Не редактируй файлы в data/ вручную — используй только CLI-команды выше.
```

> `scripts/install.sh` — **единственный** санкционированный способ обновить
> usage-бинарник. Он отказывается ставить из чего-либо, кроме чистого
> checkout на `main` (переопределяется флагом `--force`). См.
> [docs/ARCHITECTURE.md §12.4](docs/ARCHITECTURE.md#124-dev-vs-dogfood-instance-phase-2820).

### Windows

Orenda собирается и работает нативно на Windows. SQLite — pure-Go
(`modernc.org/sqlite`, без CGO), C-тулчейн не нужен.

**Сборка нативно:**

```powershell
git clone https://github.com/ramgml/orenda ~/opt/orenda
cd ~/opt/orenda
git checkout v0.14.0            # последний релизный тег
make web-install                # нужен Node.js >= 24.11
make build                      # получится bin\orenda.exe
.\bin\orenda.exe migrate up
"пароль" | .\bin\orenda.exe user create `
    --email you@example.com --display-name Вы --password-stdin
$env:ORENDA_AUTH__JWT_SECRET = [Convert]::ToBase64String((1..32 | ForEach-Object { Get-Random -Maximum 256 }))
.\bin\orenda.exe serve          # → http://127.0.0.1:2137
```

Чтобы держать сервер как фоновую службу, оберните `orenda serve` в
Windows-службу (например, [WinSW](https://github.com/winsw/winsw)) или
задачу Планировщика — `scripts/install.sh` работает только на
Unix/systemd.

**Кросс-компиляция** с любой Unix-машины:

```bash
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o orenda.exe ./cmd/orenda
```

**WSL2:** выполните стандартный Linux-quickstart внутри WSL —
`http://127.0.0.1:2137` доступен из браузеров Windows.

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

> Скриншоты: не включены в репозиторий (держим его лёгким — без бинарных blob'ов).
> Запустите `make build && bin/orenda serve` и откройте четыре ключевые страницы:
> `/`, `/inbox`, `/courses`, `/settings`, чтобы увидеть текущий UI.

## Лицензия

MIT (TBD)
