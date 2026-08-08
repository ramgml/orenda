# Session Snapshot — 2026-08-08 (вечер)

> Файл для восстановления контекста сессии. Читай первым делом при возобновлении работы.
> Подхватывается автоматически через AGENTS.md и через `instructions` в opencode.json.

## Метаданные

- **Дата:** 2026-08-08 (вечер)
- **Ветка:** `dev` (на ней работаем)
- **Версия:** `0.1.0-dev` (после `phase(0.x): ...` коммитов будет `v0.1.0-phase0`)
- **Remote:** `git@github.com:ramgml/orenda.git`
- **Последний коммит до начала работы:** `fef57a2` — docs(session): update opencode params

## Что сделано в этой сессии

1. ✅ **Phase 0 завершена** по Definition of Done из `docs/PLAN.md`:
   - `./bin/orenda version` → `orenda v0.1.0-5-gfef57a2-dirty (commit dev, built unknown)`
   - `./bin/orenda serve` → слушает на `127.0.0.1:2137`
   - `curl /healthz` → `{"status":"ok","version":"..."}`
   - `curl /api/v1/info` → capabilities payload
   - `curl /` → embedded React SPA index.html (200 text/html)
   - Vite proxy `http://localhost:5173/healthz` → Go `127.0.0.1:2137/healthz` ✓
   - Graceful shutdown по SIGTERM
   - CORS loopback-only, preflight 204

2. ✅ Решены **открытые вопросы Phase 0** (дефолты из PLAN.md):
   - **0.2** — команды: `serve`, `version`, `migrate {up,down,status}`, `backup {push,snapshot,status}` (backup — заглушка до Phase 7)
   - **0.4** — Vite + React 18 + TS + Tailwind + react-query + zustand + react-router-dom. **shadcn/ui отложен** на Phase 1 (Phase 0 не требует компонентов).
   - **0.5** — миграция `001_init.sql` взята **как есть** из PLAN.md (DB Schema section). Все 17 таблиц.
   - **0.7** — SQLite driver: `modernc.org/sqlite` (pure Go, без CGO). Подтверждено — билд `CGO_ENABLED=0` проходит, бинарь 12 МБ.

## Что создано (на диске)

### Backend (Go)
```
cmd/orenda/
├── main.go                    # cobra entry, все команды
internal/
├── api/
│   ├── router.go              # chi router, /healthz, /api/v1/info
│   ├── middleware.go          # requestID, realIP, recoverer, logger(zap), CORS loopback
│   ├── spa.go                 # embedded SPA handler с client-side fallback
│   └── router_test.go         # httptest smoke + CORS
├── config/
│   ├── config.go              # yaml + env (ORENDA_*), Validate
│   └── config_test.go
├── embed/web/
│   ├── embed.go               # embed.FS с fallback на on-disk web/dist
│   └── placeholder.txt
└── storage/sqlite/
    ├── db.go                  # Open + Migrate (embedded SQL runner)
    ├── db_test.go             # pragma + migrate tests
    ├── migrations/001_init.sql
    └── migrations_fs.go       # //go:embed migrations/*.sql → MigrationsFS
```

### Frontend (Web)
```
web/
├── package.json               # vite 5, react 18, ts 5, tailwind 3
├── vite.config.ts             # proxy /api, /healthz, /ws → :2137
├── tsconfig.json
├── tsconfig.node.json
├── tailwind.config.js
├── postcss.config.js
├── .eslintrc.cjs
├── index.html
└── src/
    ├── main.tsx               # QueryClientProvider + BrowserRouter
    ├── App.tsx                # Dashboard + placeholder routes
    ├── index.css              # tailwind directives
    ├── vite-env.d.ts
    ├── shared/
    │   ├── api/client.ts      # typed axios wrapper
    │   └── ui/HealthBadge.tsx
    └── features/, pwa/        # пустые плейсхолдеры для Phase 1+
```

### Прочее
- `.golangci.yml` — линтер конфиг (errcheck, govet, staticcheck, gofmt, goimports, revive, gocritic, …)
- `.editorconfig` — utf-8, LF, spaces 4 для Go, tabs для Makefile

## Definition of Done — Phase 0 ✅

```bash
make build                          # → bin/orenda (12 MB, static)
./bin/orenda version                # orenda v0.1.0-…
./bin/orenda migrate up             # → applied: [001_init]
./bin/orenda serve                  # http://127.0.0.1:2137
curl /healthz                       # {"status":"ok","version":"…"}
curl /api/v1/info                   # capabilities payload
curl /                              # embedded SPA index.html
make dev                            # air + vite, proxy работает
go test ./...                       # все ок
go vet ./...                        # чисто
```

## Тесты

```
ok  github.com/ramgml/orenda/internal/api          (7 тестов: healthz, info, CORS, SPA)
ok  github.com/ramgml/orenda/internal/config       (8 тестов: defaults, yaml, env, validate)
ok  github.com/ramgml/orenda/internal/storage/sqlite (3 теста: pragmas, migrate idempotency)
```

## Решения, которые нельзя забыть

- **Перед написанием любого Go-кода** — согласовать структуру пакета и интерфейсы (выполнено для Phase 0).
- **Перед миграцией БД** — согласовать полную DDL (DDL из PLAN.md принят целиком).
- **README и AGENTS.md** — обновлять по мере роста проекта.
- **Версия** — менять `VERSION` и добавлять запись в `CHANGELOG.md` при релизе на `main`.
- **opencode.json** — не использовать нестандартные ключи.
- **Backup** — в Phase 0 заглушка; не делать миграции ради него.
- **Версия из git**: `git describe --tags --always --dirty` инжектится через `-ldflags "-X main.version=…"`. Команда `version` показывает это.

## Известные ограничения / TODO

1. **`buildDate`** пока всегда `unknown` — Makefile не передаёт его в ldflags. Мелочь, поправить в Phase 9.
2. **`migrate down`** — заглушка (Phase 1+). Down-миграции не реализованы.
3. **`backup *`** — все три подкоманды — заглушки (Phase 7).
4. **`go.uber.org/zap` пишет только в stderr**, не в файл `data/logs/orenda.log`. Это намеренно для Phase 0 — добавим в Phase 9 с ротацией.
5. **`/readyz`** не реализован — пока только `/healthz` без DB ping.
6. **CORS** — только loopback origins; расширение в Phase 1+ когда появится reverse-proxy.

## Следующие шаги (Phase 1 — Ядро)

См. `docs/PLAN.md`, Phase 1 tasks:

- [ ] **1.1** миграция `002_auth.sql`: `users`, `api_tokens`
- [ ] **1.2** миграция `003_projects_tasks.sql`: `projects`, `boards`, `columns`, `tasks`, подзадачи, чек-листы, теги
- [ ] **1.3** Domain слой: `internal/domain/{user,project,task}/{model,repository}.go`
- [ ] **1.4** Repository слой: `internal/storage/sqlite/{user,project,task}_repo.go`
- [ ] **1.5** Auth: `internal/auth/{password,jwt,apitoken}.go` (bcrypt, JWT HS256, opaque tokens)
- [ ] **1.6** Middleware: `AuthUser`, `AuthAgent`, `RequireScope(...)`
- [ ] **1.7** Handlers: `/api/v1/auth/{login,logout}`, `/api/v1/me`, CRUD `/projects`, `/tasks`
- [ ] **1.8** CLI: `orenda user create --email --password`
- [ ] **1.9–1.11** Frontend: shell, AuthContext, `/login`, `/projects`, `/projects/:id`, react-query setup
- [ ] **1.11** Тесты: unit + integration + Playwright smoke

## Команды для быстрого старта следующей сессии

```bash
cd /work/projects/orenda
git log --oneline -5
git branch -a
cat docs/SESSION.md      # ← прочитать первым
cat docs/PLAN.md | head  # ← затем план (Phase 1)
cat AGENTS.md            # ← затем правила
```

## Файлы НЕ закоммичены (требуют review пользователя)

- `bin/orenda` (12 MB, gitignored)
- `data/orenda.db` + WAL/SHM (gitignored)
- `data/config.yaml` — копия `config.example.yaml`, но **создана вручную**, не закоммичена. `.gitignore` уже исключает `data/` кроме `config.example.yaml`. OK.
- `web/node_modules/`, `web/dist/` (gitignored)

## Открытые вопросы для следующих фаз

1. **Phase 1.5** — какой TTL для JWT? По умолчанию в config.example.yaml: `168h` (7 дней).
2. **Phase 1.7** — нужны ли `PATCH /api/v1/tasks/:id` или только `PUT`? И то и другое.
3. **Phase 1.11** — Playwright или только Vitest + httptest? Playwright — для E2E, но требует браузер.
4. **Phase 2** — drag-and-drop: `@dnd-kit/core` уже в плане, или `react-beautiful-dnd` (deprecated)? План говорит `@dnd-kit/core`.

## Параметры opencode (для справки)

Без изменений с прошлой сессии (см. предыдущий SESSION.md).