# Orenda

> **Local-first productivity suite**, где AI-агенты — полноправные участники. Задачи, проекты, календарь, база знаний — всё в вашей жизни, на вашей машине.

*Имя — от ирокезского «orenda» — внутренняя сила, пронизывающая всё сущее.*

[English](README.md)

## Зачем Orenda?

В стандартных task-менеджерах AI — внешний инструмент, приклеенный через интеграции. В Orenda агенты — **полноправные участники workflow**: создают задачи, берут в работу, оставляют комментарии, получают контекст от владельца. Человек — владелец, ревьюер, инициатор.

## Стек

- **Backend:** Go 1.22+ (chi, modernc.org/sqlite, JWT, gorilla/websocket, cobra)
- **Frontend:** React 18 + TypeScript + Vite + Tailwind + shadcn/ui
- **БД:** SQLite (WAL, FTS5, pure-Go через modernc.org/sqlite, без CGO) — по умолчанию; PostgreSQL тем же бинарем (`storage.driver=postgres`: embedded / внешний / Docker)
- **Backup:** git-зеркало + снапшоты по драйверу (sqlite `VACUUM INTO` / `pg_dump -Fc`; настраиваемый remote; hot-reload с 28.9)
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
> [docs/context/ARCHITECTURE.md §12.4](docs/context/ARCHITECTURE.md#124-dev-vs-dogfood-instance-phase-2820).

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
> [docs/context/ARCHITECTURE.md §12.4](docs/context/ARCHITECTURE.md#124-dev-vs-dogfood-instance-phase-2820),
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

### Базы данных: SQLite или PostgreSQL

Два storage-драйвера за одним seam-слоем (wiki:storage-adapters, T360–T367):
**SQLite** (по умолчанию — поведение не меняется) и **PostgreSQL** (по
конфигу). Схема, миграции, бэкапы и поиск следуют за драйвером
автоматически; бинарь тот же, статический, `CGO_ENABLED=0`
(sqlite — pure-Go `modernc.org/sqlite`, postgres — pure-Go `jackc/pgx`).

```yaml
storage:
  driver: sqlite          # "sqlite" (по умолчанию) | "postgres"
  postgres:
    dsn: ""               # libpq DSN — приоритетнее отдельных частей (T363)
    host: 127.0.0.1
    port: 5432
    database: ""          # обязателен для driver=postgres
    ssl_mode: ""          # libpq-дефолт "prefer"; embedded пинит "disable"
    embedded: false       # true → локальный кластер (режим «из коробки»)
    embedded_port: 5433
    binaries_url: ""      # Maven-зеркало бинарей embedded (оффлайн)
    dump_bin: ""          # pg_dump для бэкапов (голое имя → PATH)
```

У каждого ключа есть env-оверрайд (`__` разделяет секции, строчные буквы):
`ORENDA_STORAGE__DRIVER`, `ORENDA_STORAGE__DB_PATH`,
`ORENDA_STORAGE__BUSY_TIMEOUT_MS`, `ORENDA_STORAGE__POSTGRES__DSN`,
`ORENDA_STORAGE__POSTGRES__HOST/PORT/USER/PASSWORD/DATABASE/SSL_MODE`,
`ORENDA_STORAGE__POSTGRES__EMBEDDED/EMBEDDED_PORT`,
`ORENDA_STORAGE__POSTGRES__BINARIES_URL`, `ORENDA_STORAGE__POSTGRES__DUMP_BIN`
и др. — полная таблица с дефолтами в
[README.md → Database backends](README.md#database-backends-sqlite-or-postgresql).

**Три режима PostgreSQL:**

1. **Embedded** — локальный кластер без настройки: `ORENDA_STORAGE__DRIVER=postgres`,
   `ORENDA_STORAGE__POSTGRES__EMBEDDED=true`,
   `ORENDA_STORAGE__POSTGRES__DATABASE=orenda`. Бинарь сам стартует/останавливает
   PostgreSQL 16 (zonky-бинари с Maven Central, кэш `~/.embedded-postgres-go`;
   первый старт скачивает ~15 МБ); данные в `data/postgres/`, порт `127.0.0.1:5433`,
   логи постмастера — в структурированном zap-логе. Локаль кластера пинится в
   `C.UTF-8` (фоллбек `en_US.UTF-8`), C-локаль громко отвергается на старте.
2. **Внешний сервер** — `dsn` (приоритет) либо части
   `host/port/user/password/database/ssl_mode`. Для удалённых серверов ставьте
   `ssl_mode=require` (для интернета — `verify-full` + CA): libpq-дефолт `prefer`
   шифрует, но не аутентифицирует сервер.
3. **Docker** — стоковый `postgres:16` рядом с приложением (штатный
   `docker-compose.yml` держит приложение на SQLite):

   ```bash
   docker run -d --name orenda-pg -e POSTGRES_USER=orenda -e POSTGRES_PASSWORD=secret \
     -e POSTGRES_DB=orenda -p 5432:5432 -v orenda-pgdata:/var/lib/postgresql/data postgres:16-alpine
   # затем ORENDA_STORAGE__POSTGRES__DSN=postgres://orenda:secret@127.0.0.1:5432/orenda?sslmode=disable
   ```

Порты: `2137` usage · `2138` dev · `21371` E2E · `21400–21499` QA-preview ·
`5432` внешний PostgreSQL · `5433` embedded-кластер.

**Бэкапы и обслуживание следуют драйверу (T366):** снапшоты на postgres —
`pg_dump --format=custom` в тот же `data/snapshots/` с той же нумерацией и
ротацией; `orenda backup restore` восстанавливает в scratch-базу, проверяет
(`pg_restore --list` + применённые миграции) и удаляет её, `--to <база>` —
сохранить для промоушена. **Честный контракт тулзов:** в embedded-комплект
входят только серверные бинари — `pg_dump`/`pg_restore` берутся из `PATH`
(поставьте `postgresql-client`) или из `storage.postgres.dump_bin`; ошибка
бэкапа называет оба варианта.

**Известные ограничения:** числа ранжирования bm25 (sqlite) и ts_rank (PG)
не сравнимы между движками — хит-сеты и порядок совпадают (PR #266);
стемминга нет ни там, ни там; кросс-платформенная политика локалей
embedded — T368; автоматического переноса данных sqlite→postgres нет —
переключение действующего инстанса — отдельное решение владельца.
QA-конвенция: preview-инстанс на общем PGDATA кладёт в каталог маркер
`PREVIEW_OWNER` с именем владельца-инстанса — проверяйте его перед сносом.

### Запуск в Docker

Docker — дополнительный канал поставки; основной путь — systemd
(`scripts/install.sh --systemd`, см. выше).

```bash
# Сборка образа (multi-stage: SPA на node → Go-бинарь со встроенным SPA → alpine runtime)
docker build -t orenda:local .

# Запуск: порт хоста задаётся ORENDA_PORT, секрет обязателен (compose
# откажется стартовать без него — в сообщении будет подсказка; переменная
# нужна и для `docker compose exec`/`logs`)
ORENDA_PORT=8080 ORENDA_AUTH__JWT_SECRET=$(openssl rand -hex 32) docker compose up -d --build
# → http://127.0.0.1:8080
```

- **Данные:** named volume `orenda-data` → `/app/data` (SQLite + WAL);
  `docker compose down` их сохраняет, при следующем `up` база подхватывается.
- **Первый пользователь:**

  ```bash
  echo "ваш-пароль" | docker compose exec -T app orenda user create \
      --email you@example.com --display-name You --password-stdin
  ```

- **Бэкап:** `docker compose exec app orenda backup snapshot` — sqlite-снапшот
  пишется в volume (`/app/data/snapshots/`). `orenda backup push` (git) тоже
  доступен — git в образе есть, но remote/учётные данные нужно настроить
  изнутри контейнера самостоятельно.
- **Обновление:** `docker compose down`, затем снова `ORENDA_PORT=…
  ORENDA_AUTH__JWT_SECRET=… docker compose up -d --build` — данные в volume
  переживают пересоздание контейнера.

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
