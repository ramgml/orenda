# Session Snapshot — 2026-08-17 (смержено: фазы 0–26 + Wave 4 + 27.1–27.11 + 27.8.4 + 28.1–28.21 + Phase 10 subphase (Test send UI) + Phase 15 (close-out); «Полировка» + 28.19 + 28.20 + Phase 10 Test send + Phase 15 close-out + 28.21 ops-hardening + 28.22 backend sweep + 28.23 frontend foundations + Phase 29 + Phase 30 реестр целиком (последняя — 30.13) закрыты)

> Файл для восстановления контекста сессии. Читай первым делом при возобновлении работы.
> Подхватывается автоматически через AGENTS.md и через `instructions` в opencode.json.

## Аудит PLAN.md (2026-08-12) — Phase 26 завершён

Все шесть саб-PR Phase 26 (A–F) смержены в `dev`. Полная сверка плана с кодом (параллельные read-only скауты по группам фаз, статический анализ с evidence; `go build ./...` зелёный). Реальные статусы проставлены в PLAN.md под заголовками фаз и в шапке файла. Главное:

- ✅ **Phase 26 закрыт:** 5 Playwright E2E specs (8/8 pass, 5 runs no flakes) + 188 vitest unit/component tests + `make test` инклюдит vitest + новый `make test-e2e` target.
- ✅ **D1 закрыт (2026-08-12, Phase 27.2):** WS-апгрейд через cookie — `AuthContext` больше не хранит JWT (и не должен), `useWebSocketConnection` подключён в `AppLayout`, фронт реально открывает `/api/v1/ws` после login. `ws-live.spec.ts` ловит настоящий WS-фрейм без `page.reload()`. 5 прогонов подряд без флейков.
- ✅ **D2 закрыт (2026-08-12, Phase 27.1):** `make build` теперь встраивает SPA через `//go:embed all:dist`. Бинарь self-contained — `/` отдаёт 661B index.html без `web/dist/` на диске. Verify: `strings bin/orenda | grep '<div id="root"'` → 1.
- ✅ **D3 закрыт (2026-08-12, Phase 27.3):** `Task.Tags []Tag` в `ListByProjectWithStats`, +1 batch-запрос `TagsForTasks`. Чипы на канбан-карточке видны (через `task.tags`) одним round-trip; vitest 189/189; E2E `kanban.spec.ts` создаёт тег через REST, привязывает, проверяет чип.
- **Phase 18 DoD закрыт:** 27.4.A (backend) + 27.4.B (frontend) **смержены 2026-08-12** (MaterializeLesson, AnswerQuiz, GeneratorTask wire, LessonPage, E2E happy-path; worktree'и удалены). Открытый LMS-долг — ручное наполнение + quiz surface — вынесен в Phase 27.6 (2026-08-13).
- **Частично (🟡):** фазы 0, 1, 2, 6, 7, 8, 9, 10, 13, 15, 17 — пробелы перечислены в PLAN.md под каждым заголовком. **Wave 4 целиком смержена:** PR 1 (down-миграции) и PR 2 (mirror + notifier + PWA outbox + InboxPage).

## Wave 1 (D2 → D1 → D3) — выполнено 2026-08-12 (лог)

Все три саб-PR смержены (worktree'и удалены после merge):

- **PR 1.1 / Phase 27.1 — D2 (web_dist).** ✅ Готово в worktree `phase-27-1-web-dist-embed`. См. ниже.
- **PR 1.2 / Phase 27.2 — D1 (WS-cookie).** ✅ Готово в worktree `phase-27-2-ws-cookie`. См. ниже.
- **PR 1.3 / Phase 27.3 — D3 (теги в payload).** ✅ Готово в worktree `phase-27-3-tags-in-payload`. См. ниже.

## Phase 27.1 (D2) — web_dist embed, детали реализации

- `//go:embed all:dist` + `//go:embed placeholder.txt` в `internal/embed/web/embed.go`. `dist/.gitkeep` поддерживает пустую директорию в git, чтобы embed компилировался в `go test` / `go vet` / `make dev`.
- Makefile: новый таргет `embed-dists` (rsync `web/dist/` → `internal/embed/web/dist/`); `build` теперь имеет зависимость `web-build embed-dists`. `clean` восстанавливает gitkeep-only состояние.
- 6 тестов в `internal/embed/web/embed_test.go`: embed-compiles, embedded-or-empty, placeholder, valid-FS, disk fallback, embedded-precedence.
- Verify smoke: `bin/orenda serve` в `/tmp/orenda-embed-test` (нет `web/dist/`); `/`, `/healthz`, `/api/v1/{info,openapi.yaml}`, `/assets/*.css` — все 200.

## Phase 27.2 (D1) — WS cookie upgrade, детали реализации

- Backend: `internal/api/ws/client.go::Handler` принимает `cookieName` и в `extractWSToken` пробует cookie → `Authorization: Bearer` → `?token=` (precedence такая же, как в `RequireUser`). `internal/api/router.go` пробрасывает `deps.CookieName` (стандартный дефолт `orenda_session`).
- 6 новых тестов `client_test.go` покрывают precedence, пустой cookie, кастомное имя, missing-token.
- Frontend: `AuthContext.tsx` убирает поле `token` (было always null). `ws.ts::WSClient.connect()` — без аргументов, URL `/api/v1/ws` без query. `useWebSocketConnection` срабатывает на `status === 'authenticated'` без проверки токена. Mounted в `AppLayout.tsx::AppLayoutInner` так, что каждый авторизованный route получает WS автоматически.
- E2E `ws-live.spec.ts` — `page.waitForEvent('websocket')` подписывается до login, ловит `framereceived` с topic `tasks`, проверяет, что `/today`-баннер меняется без `page.reload()`.
- Manual smoke с реальным бинарём: cookie → 101 Switching Protocols; без cookie → 401; `?token=` всё ещё работает (back-compat для curl/внешних клиентов).

## Phase 27.3 (D3) — tags in list-payload, детали реализации

- Domain: `internal/domain/task/model.go::Task` получил `Tags []Tag \`json:"tags,omitempty"\``. `omitempty` — обратная совместимость (старые клиенты без поля читают как `undefined`, фронт `|| []` уже работает).
- Repo: `ListByProjectWithStats` теперь делает **5-й aggregate** — `TagsForTasks(ctx, ids)` одним запросом на N задач (без N+1). `TagsForTasks` пре-популирует `out[id]=[]` для каждого input id, так что untagged задачи получают empty-slice не-nil. Сортировка по `t.name ASC` детерминирует порядок чипов.
- Handler: `getTaskHandler` теперь гидратирует `tr.Tags` через `ListTagsForTask` — single-task и list-payload всегда консистентны.
- Repo-тест: `TestTaskRepo_ListByProjectWithStats` расширен — два тега на `A`, `B` без тегов; ассертится ordered by name + len на обоих сторонах + non-nil empty slice у `B`.
- E2E: `kanban.spec.ts` новый кейс — `createTag × 2` + `createTask` + `setTaskTags` → reload → `page.getByTitle(tagBug.name).toBeVisible()` × 2. Подтверждает, что один round-trip доставляет чипы на доску.
- Vitest: +1 тест в `TaskCard.test.tsx` — chip реально получает `backgroundColor: rgb(34, 197, 94)` от тега `#22c55e` (предохраняет от регрессии «chip рендерится с slate-фоллбэком, обогащение молча сломано»).
- Миграции: `.down.sql` нет нигде; нумерация съехала относительно текста фаз (courses=019, 018 отсутствует, `tasks.color` в 012). **✅ закрыт в `phase-down-migrations` (Wave 4 PR 1)** — runner с `-- orenda:irreversible` маркером + 18 парных `.down.sql` файлов; необратимые (001/013/015) возвращают `ErrMigrationIrreversible` с reason, остальные роллбэкаются.

## Phase 28.19 — agent type как свободный набор меток (2026-08-14)

**Цель:** закрыть план-секцию Phase 28.19 из `docs/PLAN.md:2579-2602`. `agents.type` перестаёт быть скаляром из фиксированного enum (`qwen|claude|custom`) и становится свободным набором меток, задаваемых при регистрации. Хранение — JSON-массив прямо в колонке `agents.type` (конвенция: `bot_subscriptions.events`, 001_init.sql:294; агенты — десятки строк, отдельная join-таблица избыточна). Doc-обещание «Phase 10 bot dispatch» (domain/agent/model.go:14-15) — ложное (рассылка Phase 10 идёт по `bot_subscriptions.bot_type`); комментарий удалён.

**Cutover:** старые payload'ы с `"type": "qwen"` (строкой) отклоняются 400 — клиент должен репослать как `"type": ["qwen"]`. Это явно: «implicit migration path не предоставляем» (PLAN §28.19.4 «clean cutover, без шима»).

**Ключевые решения:**

- **JSON-массив в TEXT-колонке**, не отдельная join-таблица. Конвенция с `bot_subscriptions.events` уже отработана в repo. `migration_021_agent_type_labels.sql` backfill'ит существующие значения: `''` → `'[]'`, `'qwen'` → `'["qwen"]'`. **Idempotency через `json_valid(type)`** (первая попытка через `LIKE '[%'` отказала — `json_valid` надёжнее).
- **Down защищён через `json_valid`.** modernc.org/sqlite в строгом режиме падает с «malformed JSON» на `json_extract` от невалидной строки — `CASE WHEN json_valid(type) THEN COALESCE(json_extract(type, '$[0]'), '') ELSE type END` делает down идемпотентным на скалярах (иначе round-trip ломается после первого же down).
- **Нормализация в domain, не в handler.** `agent.NormalizeLabels(in)` (trim/lowercase/dedupe/sort) — вызывается из `Agent.Validate()`. `api.listAgents` тоже использует её для query-параметров. Single source of truth для канонической формы.
- **Пустой `Type []string` валиден.** Раньше был дефолт `custom`; теперь ничего нет. Агент без меток — легальный кейс.
- **Серверный фильтр OR через repeatable query-param.** `GET /api/v1/agents?type=qwen&type=installer` — повторяемый параметр. **Не путать с `URLSearchParams({type: ['a','b']})`** — это даёт comma-joined `type=a,b`, а не `type=a&type=b`. Специально прошёл через `params.append('type', t)` цикл — задокументировано в client.ts.
- **In-memory фильтр в handler**, не в SQL. Десятки агентов; `json_each` не нужен.
- **AssigneeChip lookup через TanStack Query.** Новый `useAgents()` хук + cache key `['agents']` — TaskCard на доске делит один round-trip. При cache miss — fallback на legacy `assignee_id.slice(0,6)`. Pre-existing QueryClientProvider тесты оборачивают TaskCard (TaskCard.test, InboxPage.test, ColumnView.test).

**Задачи (все выполнены):**

- `internal/storage/sqlite/migrations/021_agent_type_labels.{sql,down.sql}` — up backfill (`CASE WHEN type='' THEN '[]' WHEN json_valid(type) THEN type ELSE json_array(type) END`), down с json_valid защитой, идемпотентность.
- `internal/storage/sqlite/migration_021_test.go` (NEW) — 4 контракта: (1) backfill форма, (2) idempotency на JSON-rows, (3) down восстанавливает скаляры + lossy multi-label, (4) down идемпотентность на скалярах.
- `internal/domain/agent/model.go` — `Type []string`, удалены `TypeQwen/TypeClaude/TypeCustom` и doc-обещание про «Phase 10 bot dispatch», новая `NormalizeLabels`, Validate не ставит дефолт.
- `internal/domain/agent/model_test.go` — переписан `TestAgent_Validate_Defaults` → `…AppliesDefaultsExceptLabels`, новый `TestAgent_NormalizeLabels` (7 sub-tests), `TestAgent_Validate_NormalisesTypeInPlace`.
- `internal/storage/sqlite/agent_repo.go` — `marshalAgentType`/`unmarshalAgentType` хелперы; INSERT/UPDATE/Scan через них; пустой `Type` → `"[]"`.
- `internal/service/agent/agent.go::Register` — сигнатура `(name string, labels []string, desc string, scopes []string)`.
- `internal/api/handlers_agents.go` — `Type []string` в request body, repeatable `?type=` query в list, новая `filterAgentsByLabels` (in-memory OR).
- **Миграция всех использующих тестов:** `agent.TypeQwen/Claude/Custom` → `[]string{...}` в 11 файлах; `{"type":"qwen"}` → `{"type":["qwen"]}` в 2 файлах; ассерты `assert.Equal(t, agent.TypeQwen, got.Type)` → `assert.Equal(t, []string{"qwen"}, got.Type)`. Импорт `agentdomain` → `agent` где не используется.
- `internal/api/openapi.yaml` + `docs/openapi.yaml` — новые схемы `Agent` + `CreateAgentRequest`, `POST /api/v1/agents` requestBody, `GET /api/v1/agents` parameters (multi-value `?type=`). Синхронно. `TestOpenAPI_RouteCoverage_FullRouter` зелёный.
- `web/src/shared/api/client.ts` — `Agent.type: string[]`, `listAgents({type})` принимает фильтр, `createAgent` input обновлён. URLSearchParams ловушка → `params.append('type', t)` цикл с комментарием.
- `web/src/shared/hooks/useAgents.ts` (NEW) — TanStack Query wrapper с `agentsQueryKey` экспортом.
- `web/src/features/agents/AgentsPage.tsx` — chips-input (`Enter`/`,` commit, `Backspace` pop, `×` remove), таблица чипов с em-dash fallback для пустых, серверный фильтр через ту же chips-input над таблицей. `ChipsInput` компонент вынесен для переиспользования.
- `web/src/features/agents/AgentsPage.test.tsx` — фикстуры `makeAgent({type: string[]})`, новый тест «renders label chips per agent», «chips input: Enter commits; × removes», «submit posts createAgent with the label set», «filter chips refetch with repeated ?type=». 10 тестов всего.
- `web/src/features/projects/TaskCard.tsx::AssigneeChip` — принимает `agent?: Agent`, `title = Agent: <name> (<labels>)` при cache hit, fallback на `id.slice(0,6)` при miss. TaskCard вызывает `useAgents()` (1 round-trip на страницу через cache dedup).
- `web/src/features/projects/TaskCard.test.tsx` — обёрнут в QueryClientProvider; новые «AssigneeChip title surfaces agent labels» + «falls back to id when agent lookup misses».
- `web/src/features/inbox/InboxPage.test.tsx` + `web/src/features/projects/ColumnView.test.tsx` — обёрнуты в QueryClientProvider (TaskCard transitive dep).
- `web/src/shared/api/client.ts::listAgents` — explicit комментарий про URLSearchParams массив-ловушку.
- `web/e2e/helpers.ts::createAgent` — `type: 'qwen'` → `type: ['qwen']`.
- `docs/DB.md` — `agents` строка: `type (JSON array of free-form labels, 021)`; таблица миграций обновлена.

**DoD (verified 2026-08-14):**

- ✅ `go test ./...` — 30 пакетов ok, включая `TestMigrate_021AgentTypeLabels` (4 assert), `TestAgent_NormalizeLabels` (7 sub-tests), `TestAgentRepo_*` (5), обновлённые handler/service тесты.
- ✅ `npx vitest run` — 241/241 (было 230; +11: 4 в AgentsPage, 2 в TaskCard AssigneeChip, 5 прочих миграций фикстур).
- ✅ `npx tsc --noEmit` — clean.
- ✅ `make build` — OK (бинарь содержит embedded web/dist из Phase 27.1).
- ✅ `make test-e2e` — 18/18 pass (было 18; 0 regressions). 4 specs упали на первом прогоне (`ws-live`, `today`, `review`, `course`) — фиксились `helpers.ts::createAgent` (`type: ['qwen']`).
- ✅ `TestOpenAPI_RouteCoverage` + `TestOpenAPI_RouteCoverage_FullRouter` — оба зелёные.
- ✅ `docs/openapi.yaml` ↔ `internal/api/openapi.yaml` — синхронны (`diff` пустой).
- ✅ `golangci-lint` на изменённых пакетах — без новых issues.

**За скобкой (явно отложено):**

- **Хотфикс для мигрирующих агентов** — `orenda migrate up` конвертирует существующих агентов в JSON-array, но API сразу после up отвергает старый string-формат (clean cutover). Оператор с внешними интеграциями должен один раз поправить payload'ы. Задокументировано в `openapi.yaml::POST /agents description` и `handlers_agents.go::createAgentRequest` doc-комментарии.
- **Bulk-edit labels** — нет endpoint'а «обновить type существующего агента», только re-через Register flow. `PATCH /api/v1/agents/{id}` принимает `type` — отдельная фаза при необходимости.
- **UI color picker для type** — `tags` имеет color picker (Phase 13), `agents.type` сейчас монохромный. Кандидат на будущую полировку.

## Phase 28.20 — dev/dogfood separation (2026-08-14)

**Цель:** разделить dev (active worktree) и usage (operator's daily-use instance) по двум осям — канал бинаря и runtime-ресурсы — так, чтобы они не пересекались. Закрывает дефект, описанный в PLAN §28.20: «Vite-прокси (хардкод 2137) молча проксирует dev-фронт на usage-бэкенд; install.sh из любой ветки перезаписывает единственный глобальный бинарь; dev-бинарь против usage-БД угоняет схему вперёд релиза через авто-миграции». Документ-конвенция: usage = `~/opt/orenda` clone of GitHub, ветка `main`, port 2137; dev = worktree, port 2138; e2e = 21371.

**Ключевые решения:**

- **Один env var = обе стороны.** `ORENDA_SERVER__PORT` драйвит и Go (через `cmd/orenda/main.go::runServe`), и Vite (через `web/vite.config.ts`). Дефолт dev = 2138; дефолт production binary = 2137 (не менялся). Один источник правды — никаких «а мы в этот раз ещё одну переменную забыли синхронизировать».
- **Channel guard в install.sh — single line of defense.** `git rev-parse --abbrev-ref HEAD != main` или `git status --porcelain` nonempty → exit 1 с диагностикой. Это единственный путь обновления usage-инстанса; обход --force пишет в лог «bypassing channel guard» (не молча).
- **Channel guard в update-dogfood.sh — двойная защита.** Сначала `git pull --ff-only` (гарант, что канал линеен), потом `install.sh --systemd` (внутри свой guard). Если ~/opt/orenda не на main или dirty — обновление отказывается на первом же шаге.
- **Startup log несёт db_path.** `logger.Info("http listening", addr, db_path)` — observability, "какой это инстанс" отвечает `journalctl --user -u orenda` без угадывания. Раньше две инстансы на одной машине было неотличимы без grep по конфигу.
- **Vite proxy-targets — env, не хардкод.** `http://127.0.0.1:${ORENDA_SERVER__PORT ?? 2138}` для `/api`, `/healthz`, `/ws`. Это убирает молчаливый redirect dev-фронта на usage-бэкенд (главный баг, зафиксированный в PLAN §28.20).
- **Makefile `dev` — `export` в shell recipe.** `export ORENDA_SERVER__PORT=$${ORENDA_SERVER__PORT:-2138}`; air и Vite оба его наследуют. Override через `make dev ORENDA_SERVER__PORT=2200` или env в shell; default остаётся «всё работает без флагов».
- **Residual risk задокументирован, не механически заблокирован.** dev-бинарь с `--config ~/.local/share/orenda/config.yaml` запустит авто-миграции usage-БД — опасно. Защита только дисциплина + startup log db_path. Автодетект (сравнение resolved db_path с дефолтным `$HOME/.local/share/orenda`) — out of scope, механический детект легитимного override ещё не придумали.

**Задачи (все выполнены):**

- `Makefile` — таргет `dev`: `export ORENDA_SERVER__PORT=$${ORENDA_SERVER__PORT:-2138}` в shell recipe; одна строка echo про override.
- `web/vite.config.ts` — `backendPort = process.env.ORENDA_SERVER__PORT ?? '2138'`; три proxy-target используют его. Комментарий: dev не «по умолчанию 2137», а usage.
- `web/playwright.config.ts` — comment про `:2137` → «не clobber either usage :2137 or dev :2138». E2E-порт 21371 не задет.
- `Makefile::test-e2e` — comment обновлён: «не конфликтует с usage 2137 или `make dev` 2138».
- `scripts/install.sh` — переписан: (a) `for arg in "$@"` парсит `--systemd | --force | --help | unknown`; (b) `git rev-parse --abbrev-ref HEAD` + `git status --porcelain` для guard; (c) `Channel: branch @ hash (dirty: yes|no)` в начале; (d) `ERROR: refusing to install from a non-main or dirty checkout` с подсказкой `--force`; (e) `help` показывает из комментария шапки.
- `scripts/update-dogfood.sh` (NEW) — `set -euo pipefail`; guard на main+clean; `git pull --ff-only origin main`; `scripts/install.sh --systemd`; `systemctl --user restart orenda`. chmod +x.
- `cmd/orenda/main.go` — `serverErr goroutine` старт: `logger.Info("http listening", addr, db_path=`cfg.ResolveDBPath(".")`)`. Не требует пересборки schema, не зависит от `Dependencies.DBPath` (хотя оно уже там есть).
- `docs/ARCHITECTURE.md` — новая секция 12.4 «Dev vs dogfood instance (Phase 28.20)»: таблица каналов (usage 2137 / dev 2138 / e2e 21371), намёк почему 2137 — канонический usage, логика `--force`, ритуал update-dogfood, residual risk ("dev против usage-BD = дисциплина + лог").
- `docs/ARCHITECTURE.md` — секция 9 Build pipeline (`:2137` → `:2138` для `make dev`).
- `AGENTS.md` — intro: «port 2137 (usage) / 2138 (make dev)»; Worktree placement: «Port 2137 reserved for usage, 2138 — dev default, 21371 — E2E».
- `README.md` — два места: install (про install.sh guard) + `make dev` (про деление 2137/2138). Ссылки на ARCHITECTURE.md §12.4.

**DoD (verified 2026-08-14, worktree `phase-28-20-dev-dogfood`):**

- ✅ **Два одновременно живых инстанса** — usage-like на 2137 (DB=/tmp/orenda-usage-smoke/data/orenda.db) + dev на 2138 (DB=./data/orenda.db), оба 200 на `/api/v1/info`, в логах разные `db_path`. Smoke script `/tmp/opencode/smoke-28-20.sh`, оба процесса убиты после теста.
- ✅ **install.sh guard** — `bash scripts/install.sh` из ветки `phase-28-20-dev-dogfood` (≠ main) → exit 1, диагностика (ветка, hash, dirty, hint --force). `bash scripts/install.sh --help` → exit 0, usage. `bash scripts/install.sh --bogus` → exit 2, error.
- ✅ **update-dogfood.sh guard** — `bash scripts/update-dogfood.sh` из той же ветки → exit 1, `(currently on 'phase-28-20-dev-dogfood' @ ab4687d)`.
- ✅ **Startup log** — `{"msg":"http listening","addr":"127.0.0.1:2138","db_path":"data/orenda.db"}`, видно в выводе smoke.
- ✅ **go build ./...** — clean.
- ✅ **`make test`** — Go 30/30 packages ok + vitest 241/241 — зелёные (никаких регрессий, изменения mostly в shell/TS/docs).
- ✅ **`make test-e2e`** — 18/18 pass (37.2s). E2E свой порт 21371, изменений не видит.
- ✅ **`npx tsc --noEmit`** — clean.

**За скобкой (явно отложено):**

- **Автодетект dev vs usage по `db_path`.** Если dev-бинарь стартует с `--config ~/.local/share/orenda/config.yaml`, можно было бы warn'ить и запрашивать `--force` — но это легитимный override (оператор знает, что делает). Механическая защита от operator error — отдельная фаза при реальной потребности.
- **Prettier** — Phase 28.7 уже настроен; `pre-commit` hook через simple-git-hooks (28.12). Перед коммитом: `cd web && npx prettier --check <files>` для проверки.
- **Чистка data/ в worktree перед коммитом** — `data/orenda.db` и `data/uploads/` создаются smoke-тестом; gitignore их уже игнорирует, `git status` чист.
- **Первая операция после merge** — оператор делает `git pull` в `~/opt/orenda`, чтобы обновлённый install.sh в его checkout'е тоже имел guard. До этого `git pull` тривиально пройдёт (старый install.sh не имеет guard, но md5-хеш не валидирует). Pre-existing problem — отдельная фаза «post-merge operator onboarding».

## Phase 10 subphase — Test send UI (2026-08-14)

**Цель:** закрыть один из «🟡 долгов» Phase 10 (PLAN §10 аудит 2026-08-12): «нет "Test send" в UI». Оператор раньше не мог проверить credentials бота, не дождавшись реального события (5 минут до ближайшего `task.review_needed`, или хуже — день до weekly digest). Теперь — POST `/api/v1/bots/test` доставляет one-off сообщение через люблюной зарегистрированный бот за ~10 секунд (включая сетевой round-trip).

**Ключевые решения:**

- **Endpoint независим от subscription store.** Тест идёт через `bot.Registry` напрямую, без чтения/записи `bot_subscriptions`. Оператор проверяет wiring до того, как вообще привязывает подписку.
- **`console` исключён из whitelist.** Console пишет в server stderr — нет user-facing signal, тест через него выглядел бы как silent failure. Whitelist `webhook|email|telegram|vk` живёт на сервере (`knownTestBotTypes`) и в UI (`TEST_BOT_TYPES`); UI исключает console из дропдауна, backend отказывает 400 если кто-то шлёт `bot_type: console` напряно.
- **Per-bot target pre-check.** Дешёвая проверка на сервере до transport round-trip: webhook требует `http(s)://`, email требует `@` и `.`, telegram/vk требуют numeric id (telegram допускает leading `-` для group/channel id). Транспорт всё равно делает sourceную проверку (webhook URL parse, SMTP dial, chat id numeric) — это UX pre-filter, не security boundary.
- **Status codes:** 200 ok / 400 invalid_input (нет полей) / 400 unknown_bot_type / 400 per-bot pre-check / 503 bot_not_running / 503 bot_registry_not_wired / 502 send_failed (с transport error в hint).
- **Recording bot тест fixture.** `handlers_bots_test.go::recordingBot` под `aliasingBot` регистрируется под 4 именами (webhook/email/telegram/vk) одним инстансом — pre-check-тесты могут слать `bot_type: telegram` и доходить до target-валидатора, а не отскакивать от registry с 503. Mutex защищает calls slice.
- **Frontend Test send UI.** Новая карточка в `Bots.tsx` между header и Telegram bind: dropdown (TEST_BOT_TYPES, console excluded) + target input (placeholder per type, через существующий `targetPlaceholder`) + submit button + green success banner / red error banner. Pattern-match на `error: '<key>'` в axios error.message для friendly hints (как в Telegram bind).
- **Disambiguating testids.** Add subscription form уже использовал `screen.getByRole('combobox')` для bot-type select; с двумя select'ами это ломается. Добавил `data-testid="add-subscription-bot-type"` для add-subscription select, `bot-test-type` для test send. Existing tests обновлены под testid.

**Задачи (все выполнены):**

- `internal/bot` не тронут — handler зовёт существующий `bot.Send(ctx, target, msg)`.
- `internal/api/router.go::Dependencies` — `BotRegistry *bot.Registry` (phase 10 subphase); комментарий об использовании.
- `internal/api/handlers_subscriptions.go` — `testBotRequest`, `knownTestBotTypes`, `testBotHandler`, `validateTestTarget`. Импорты `strings`, `time` добавлены.
- `internal/api/router.go` — `r.Post("/bots/test", testBotHandler(deps))` рядом с telegram bind.
- `cmd/orenda/main.go` — `BotRegistry: botRegistry` в deps; комментарий о nil-safety для partial-router fixtures.
- `internal/api/handlers_bots_test.go` (NEW) — `testBotFixture`, `recordingBot`, `aliasingBot`. 9 tests, 16 subtests: success, missing-fields (3 subtests), unknown_bot_type, bot_not_registered, send_failed, target_pre_check (7 subtests), invalid_json, registry_not_wired.
- `internal/api/openapi.yaml` + `docs/openapi.yaml` — `/api/v1/bots/test` POST с requestBody/responses 200/400/502/503.
- `web/src/shared/api/client.ts` — `api.testBot({bot_type, target_address})` метод.
- `web/src/features/settings/Bots.tsx` — `TEST_BOT_TYPES` константа (без console); state `testBotType/testTarget/testing/testResult`; `onTestSend` handler с axios-error pattern-matching; новая секция `data-testid="bot-test-send"` с form/buttons/banners; `data-testid="add-subscription-bot-type"` для disambiguation.
- `web/src/features/settings/Bots.test.tsx` — 5 new tests (omits console, success banner, bot_not_running, send_failed, submit disabled); 2 existing tests обновлены под testid (использовали `getByRole('combobox')`).

**DoD (verified 2026-08-14, worktree `phase-10-test-send`):**

- ✅ **Go test ./... — 30 packages ok**, включая `TestBotsTestHandler_*` (9 + 16 subtests) и `TestOpenAPI_RouteCoverage_FullRouter` (новый endpoint в спеке).
- ✅ **vitest 246/246** (+5 от 241). Старые тесты Bots не сломаны (только disambiguation через testid).
- ✅ **`npx tsc --noEmit` clean.**
- ✅ **`make build` OK** (бинарь содержит embedded web/dist).
- ✅ **`make test-e2e` 18/18 pass** (E2E-порт 21371 не задет; ни один существующий spec не упал).
- ✅ **Manual smoke** (`/tmp/opencode/smoke-bots-test.sh`): реальный webhook bot + Python HTTP sink. 4 кейса проверены:
  - webhook happy path → 200, sink получил `{"title":"Orenda test message","kind":"test",...}`
  - telegram (не зарегистрирован) → 503 `bot_not_running` с friendly hint
  - console (refused) → 400 `unknown_bot_type`
  - webhook bad URL (pre-check) → 400 `webhook_target_must_be_http_url` до transport round-trip
- ✅ **`TestOpenAPI_RouteCoverage_FullRouter`** — новый endpoint в спеке.
- ✅ **`docs/openapi.yaml` ↔ `internal/api/openapi.yaml`** — синхронны (`diff` пустой).

**За скобкой (явно отложено):**

- **Per-bot operator instructions в UI** — webhook URL hint говорит «URL must be reachable»; telegram требует numeric chat_id (а не @username); vk — peer id. Можно дать input hints через `placeholder` (уже есть частично) или help text. Низкий приоритет: в текущей формете operator либо знает свои id, либо copy-paste'ит из subscription row.
- **Per-subscription "Test this one" кнопка** — заполнить target автоматически из subscription row, не руками. Удобно, но не блокер: текущая форма требует одно действие — operator знает куда отправлял (иначе subscription не работала).
- **Rate limit на /bots/test** — спам бота сейчас не ограничен (POST /api/v1/bots/test → немедленный webhook POST). При abuse это плохо. Auth-cookie нужен (есть), но throttle нет. Отдельная мини-фаза при необходимости.
- **Per-target «Send test to me» для Telegram** — `bind` flow уже возвращает chat_id, можно pre-fill при наличии Telegram-подписки. Low value vs complexity.

## Phase 15 (close-out) — agent UX контракт: lock_taken holder + context blockers/lock + ready self-assigned *(2026-08-14)*

**Цель:** закрыть три «🟡 долга» Phase 15 из `docs/PLAN.md:1002` (аудит 2026-08-12):
1. `409 lock_taken` без holder-полей (агент не знает кого спрашивать)
2. `GET /agent/tasks/{id}/context` без `blocked_by`/lock holder (агент не видит блокеров и holder-а)
3. `GET /agent/tasks?ready=true` включает задачи, занятые самим агентом (шум в очереди)

**Ключевые решения:**

- **Узкий `TaskLockHolder` интерфейс.** В `internal/api/service_interfaces.go` добавлен `TaskLockHolder { Holder(ctx, taskID) (agentID, acquiredAt, err) }`. Реализуется уже существующим `*sqlite.taskLockRepo` (метод `Holder` был написан ранее, но не был подключён к API). nil-safe — handlers должны guard.
- **Лучше два контекст-хелпера, чем обвешанные if'ы.** `populateContextBlockers(deps, ctx, taskID, out)` и `populateContextLockHolder(deps, ctx, taskID, out)` вызываются из обоих эндпоинтов (`/tasks/:id/context` user-side и `/agent/tasks/:id/context` agent-side). Удалять пустые поля через `out.LockHolder = nil` / `out.BlockedBy = nil` (JSON encoder тогда опускает ключ — backward-compat).
- **`lockTakenResponse` хелпер.** Один источник для 409 payload. На `ErrLockTaken` зовёт `Holder(ctx, taskID)` → если есть holder, идёт `Agents.GetByID` для имени. Если lookup вернул пусто или ошибку — fallback на голый `{error: "lock_taken"}` (backwards-compat с любым клиентом, который матчит на эту форму).
- **ready=true self-assigned фильтр.** В `listAgentTasksHandler` рядом с уже существующим "exclude tasks assigned to another agent" добавил "exclude tasks assigned to THIS agent". Минимальный diff — два `if ready && tr.AssigneeID == id.AgentID` блока, понятных из названия.

**Задачи (все выполнены):**

- `internal/api/service_interfaces.go` — `TaskContext` теперь имеет `BlockedBy []string` и `LockHolder *LockHolder`; новый тип `LockHolder { AgentID, AgentName, AcquiredAt }`; `TaskLockHolder` интерфейс. Импорт `time` добавлен.
- `internal/api/router.go::Dependencies` — новое поле `TaskLockHolder TaskLockHolder` с комментарием про nil-safety.
- `cmd/orenda/main.go` — wire `taskLocks` (тот же `*sqlite.taskLockRepository`) в deps.TaskLockHolder.
- `internal/api/handlers_phase3.go`:
  - `claimTaskHandler` — на `ErrLockTaken` зовёт `lockTakenResponse(deps, ctx, taskID)`.
  - `lockTakenResponse` хелпер — единственный путь для 409 body.
  - `populateContextBlockers` и `populateContextLockHolder` хелперы — best-effort lookup, omit на пусто/ошибку.
  - `getTaskContextHandler` — два вызова helper'ов после Blocklists/Children блока.
  - Импорт `context` добавлен.
- `internal/api/handlers_agent.go`:
  - `agentClaimTaskHandler` — на `ErrLockTaken` зовёт `lockTakenResponse` (раньше возвращал голый 409, был TODO-комментарий «Phase 15.3: extend 409 with the current holder» — закрыт).
  - `agentTaskContextHandler` — те же populate*Helper вызовы.
- `internal/api/handlers_dependencies.go::listAgentTasksHandler` — добавлен второй «exclude self-assigned» guard (раньше был только «exclude other agents»).
- `internal/api/openapi.yaml` + `docs/openapi.yaml` (sync) — обновлены описания 409 для обоих /claim и `description` для `/context` (user + agent) с упоминанием новых полей.
- `internal/api/handlers_phase15_test.go` (NEW) — `phase15Fixture` (двухагентный, с user login helper), 6 тестов:
  - `TestPhase15_LockTaken_IncludesHolderAgentIDAndName` — holder claims, rival claims same → 409 + holder_agent_id/name/claimed_at.
  - `TestPhase15_LockTaken_FallsBackToBareErrorWhenHolderRepoUnwired` — backwards-compat: без TaskLockHolder в deps → голый 409 без holder-полей.
  - `TestPhase15_AgentContext_BlockedByAndLockHolder` — holder claims task BEFORE blocker wired (иначе claim 422), потом blocker добавлен → context показывает blocker + holder.
  - `TestPhase15_UserContext_SameHelpers` — user-side /tasks/:id/context использует те же populate-хелперы (cross-check).
  - `TestPhase15_AgentContext_LockHolderAbsentWhenNoLock` — unclaimed task → LockHolder nil, BlockedBy пустой.
  - `TestPhase15_ListAgentTasks_ReadyExcludesSelfAssigned` — holder claims task A, rival claims task B → holder's ?ready=true содержит ни одну из них.

**DoD (verified 2026-08-14, worktree `phase-15-agent-context`):**

- ✅ `go test ./...` — 30 packages ok; новые `TestPhase15_*` (6 тестов) проходят.
- ✅ `npx vitest run` — 246/246 (без изменений во фронте).
- ✅ `make build` — OK.
- ✅ `make test-e2e` — 18/18 (Phase 15 — чисто бэкенд-контракт, фронт не трогаем).
- ✅ `npx tsc --noEmit` — clean.
- ✅ `TestOpenAPI_RouteCoverage_FullRouter` — зелёный (новые поля в 409-context payload не добавляют routes).
- ✅ `docs/openapi.yaml` ↔ `internal/api/openapi.yaml` — синхронны (`diff` пустой).

**За скобкой (явно отложено):**

- **`agentservice` surface holder-agent-name в `/agent/me`.** Сейчас holder_agent_name резолвится через `Agents.GetByID`, что работает для всех агентов, зарегистрированных в системе. Если когда-нибудь захотим показывать имя зарегистрированного человеком агента (где agent ID есть, но row нет) — потребуется fallback. Out of scope.
- **Расширить 409 на «столкновение блокеров» vs «просто lock_taken».** Сейчас 422 task_blocked уже несёт `unfinished_blockers`. 409 lock_taken остаётся atomic claim conflict. Оба достаточно различимы — не сливаем.
- **Cache `Agents.GetByID` в 409-path.** Сейчас holderAgentName lookup синхронный на каждой 409. При высокой contention — отдельный in-memory cache. Out of scope (single-owner install).

## Phase 28.21 — ops-hardening: login rate-limit + tracked config template + JWT secret *(2026-08-16)*

**Цель:** закрыть три критичные дыры, найденные аудитом 2026-08-16 (три параллельных read-only скаута: backend / frontend / docs-ops).

1. **Login обходил rate limit.** `/api/v1/auth/login` сидел в `SkipPaths` (наследие Phase 26.E «для E2E») — неограниченный перебор паролей. Убран; `/api/v1/me` остаётся (дешёвый auth-probe на каждый mount). E2E не задет — `run-server.sh` выставляет `ORENDA_RATELIMIT_*` override'ы. Тест `TestRateLimit_LoginNotSkipped` (100 POST → 429 + Retry-After).
2. **install.sh падал на fresh clone.** `data/config.example.yaml` никогда не трекался — git не re-include'ит файлы внутри исключённой директории, обе `!data/...` негации в `.gitignore` были мертвы (PLAN 28.8.5 при этом врал «tracked»). Шаблон переехал в `configs/config.example.yaml` (tracked): `jwt_ttl: 24h`, `jwt_secret: ""` + комментарий про env, секция `ratelimit:`, честная пометка про `sqlite_snapshot_cron` (тикер 24h, cron не парсится — Phase 7 долг).
3. **Два пути к публично известному JWT-секрету.** (а) `${ORENDA_JWT_SECRET}` в примере никогда не expand'ился (нет `os.ExpandEnv`, имя не совпадает со схемой `ORENDA_AUTH__JWT_SECRET`) — литерал становился HMAC-ключом; (б) systemd unit шил `change-me-via-EnvironmentFile`. Теперь `install.sh` генерирует `$DATA_DIR/env` (32B urandom→base64, mode 600) при первом запуске; unit читает только `EnvironmentFile`, placeholder удалён.

**Smoke (verified):** fresh clone в /tmp → `install.sh --force` проходит end-to-end (build + install + config + env); бинарь стартует со сгенерированным секретом; флуд login: ~59×401 → 429. **Tests:** `go test ./...` зелёный (30/30), vitest 246/246, `make test-e2e` 18/18.

**За скобкой (новый бэклог из аудита 2026-08-16):** нет CI (все гейты локальные); `uninstall.sh` глотает неизвестные флаги; `update-dogfood.sh` хардкодит `origin`; `sync_ops` record-errors проглатываются; backend-свип (N+1 в `listAgentTasksHandler`, мёртвый код, vet-finding) → **закрыт в Phase 28.22**; frontend-фундамент (WS-race в `useWebSocketTopic`, deps-хигиена, density-toggle UI) → Phase 28.23.

## Phase 28.22 — backend sweep *(2026-08-16)*

**Цель:** механическая зачистка backend-находок аудита 2026-08-16.

- **N+1 закрыт:** `task.Repository.BlockersForTasks(ctx, ids)` — batch-форма `Blockers` (по образцу `TagsForTasks`, 27.3); `listAgentTasksHandler` делает 1 запрос на листинг вместо N. Тест `TestTaskRepo_BlockersForTasks`.
- **Full-scan в `/today` закрыт:** новый `Filter.IDs`; enrichment идёт только по видимым id (раньше — 5 aggregate-запросов по всей БД + мёртвый `ids`-loop). Тест `TestTaskRepo_ListByProject_IDsFilter`.
- **`go vet` чистый:** единственный finding (мёртвый `RecordOld` с недостижимым дублем в `move_test.go`) удалён.
- **Мёртвый код:** дважды дублированный suppression-блок + stale RRULE-комментарий в `event.go` (RRULE жив — `handlers_calendar.go` зовёт `ExpandRecurrence`); no-op `newUUID() → ""`; ручной `/dev/urandom` UUID в `backup.go` → `uuid.NewString()`; три `var _ = ...` import-заглушки; мёртвый `heartbeatRequest` decode; мёртвый `ownerID` в `handlers_courses.go`; stale-комментарии `router.go`/`config.go`.

**Verify:** `go build` + `go vet` чистые; `go test ./...` 30/30 ok; golangci-lint 97 → 95 (роста нет).

## Phase 28.23 — frontend foundations *(2026-08-16)*

**Цель:** frontend-находки аудита 2026-08-16 (третья часть; 28.21 — ops, 28.22 — backend).

- **WS re-subscribe race закрыт:** `useWebSocketTopic` держит handler в `useRef`, подписка — один раз на топик (раньше inline-arrow в deps `[topic, fn]` → unsubscribe+resubscribe на каждый рендер, событие в зазоре терялось). Тест пинит стабильный handler identity; **mutation-check**: возврат `[topic, fn]` → тест красный.
- **WS→Query для agents:** `AppLayout` инвалидирует `agentsQueryKey` по топику `agents` — AssigneeChip-лейблы больше не протухают.
- **package.json hygiene:** удалены `zustand` (0 импортов) и `@tiptap/extension-bubble-menu` (BubbleMenu из `@tiptap/react/menus`); `idb` → dependencies (runtime-импорт в offline/db.ts).
- **`patchTaskOrQueue` без double-cast:** offline-path мерджит патч поверх существующей задачи вместо фабрикации Task из патча.
- **Shared UI primitives:** `shared/ui/{Loading,ErrorBanner,EmptyState}.tsx` с dark:-вариантами; мигрированы Today/Inbox/Review/Calendar (красный баннер календаря был без dark: — нечитаем в тёмной теме).
- **Density toggle UI (долг Phase 17):** чекбокс «Compact cards» на канбане пишет `orenda.kanban.cardDensity` (TaskCard читал флаг с Phase 17, писателя не было).
- **AuthContext тесты** (4 кейса: anonymous / authenticated / logout / logout при упавшем endpoint) + stale-комментарий в CalendarPage (drag-reschedule живой, не «on the roadmap»).

**Verify:** `npx tsc --noEmit` clean; vitest **263/263** (было 246, +17); prettier/eslint на затронутых файлах clean; `make test-e2e` 18/18.

## Phase 29 (постановка) — Agent surfaces: wiki + создание курсов агентом *(2026-08-16)*

**Продуктовое решение пользователя:** максимум работы перекладывается на агентов. Целевая сценария: «создай курс по X» внешнему агенту (opencode/MCP) → курс готов к обучению без единого действия человека. Диагностика сессии: wiki агентам недоступна вообще (user-side `/api/v1/pages/*`, agent-неймспейс без pages → MCP/skill wiki не покрывают); курсы агент наполняет (curriculum/materialize/quizzes есть), но создать не может (POST /courses только user-side).

**Зафиксированные дизайн-решения** (полная постановка — PLAN.md Phase 29, задачи 29.1–29.7):

1. Agent wiki REST — переиспользуем существующие хендлеры (user-ctx не читают, проверено grep'ом); схема без изменений (нет owner/author); mirror + WS достаются бесплатно.
2. Владелец agent-created курса — `Users.FirstNonSystem`; `SkipGenerator` (агент сам генератор, generator task не спавнится).
3. `POST /agent/courses/{id}/activate` (review → active) — общий сервисный путь с user-side approve; human approve в UI остаётся, request-changes доступен всегда. Аппрув-клик убран сознательно: это и есть «ручная работа», которую сценарий исключает.
4. Поверхности: REST `/agent/pages/*` + `/agent/search` + CLI `orenda agent pages …` + MCP-тулы `orenda_pages_*`/`orenda_search` + skill-секция; openapi.yaml синхронизируется (coverage-тест 27.11.2 принудит).

**Закрыта 2026-08-16** (29.1 — `phase-29-1-agent-wiki-rest`; 29.2–29.7 — `phase-29-2-7-agent-surfaces`):

- **29.2 CLI:** `orenda agent pages list|get|put|delete|move|backlinks` + `orenda agent search`. Латентный баг транспорта: `doRaw` присваивал путь с query в `u.Path`, процент-кодируя `?` — `?ready=true` фильтр `next` молча не работал. Починен, запинен тестом.
- **29.3 MCP:** 6 новых тулов (`orenda_pages_*`, `orenda_search`); латентный баг: `orenda_await` слал в user-side `/events/await` (401 на agent token) → теперь agent-namespace. 8 тестов.
- **29.4/29.5 REST:** `POST /agent/courses` (owner=`FirstNonSystem`, `SkipGenerator` форсирован, 503 `owner_not_configured` без human user) + `POST /agent/courses/{id}/activate` (общий `approveCourseCore` с user-side approve; missing course теперь 404 на обоих surfaces — был 500). Отклонение: «activity с автором-агентом» не реализовано — курсового activity-контура нет вовсе (activity только у задач); user-side approve тоже молчит. Зафиксировано в PLAN 29.5.
- **29.6:** openapi оба файла, SKILL.md §2.2/§4.3/§4.4 (end-to-end сценарий «build me a course on X») + §6.1 reference.
- **29.7 smoke:** реальный бинарь + agent token + curl — курс создан→curriculum→materialize→activate, user-side видит `active`+`open`; wiki create/move/backlinks/search/edit/delete(cascade); CLI parity. `SMOKE OK`.

## Phase 30 (реестр) — открытые задачи с приоритетами *(2026-08-16)*

**Решение пользователя:** «долгов» как свободных записей нет — всё оформляется задачами с приоритетами. Полная инвентаризация PLAN/SESSION (каждый пункт сверен с кодом read-only) → **Phase 30 в PLAN.md**: 16 задач, P1 ×2 (CI 30.1, sync_ops 30.2), P2 ×6 (30.3–30.8: Phase 10 ×3, autocomplete, reject-comment, due в календаре), P3 ×8 (30.9–30.16). **Правило процесса:** новые отложенные работы при закрытии фазы получают номер 30.x, а не строку «за скобкой». Проверено и не заведено (уже закрыто в коде, не ре-листать): per-column color editor (`EditColumnModal` + color dot 27.10), events UI подписок (`selectedEvents` в Bots.tsx), optimistic move с revert (`KanbanBoard.tsx`), hot-reload backup (28.9), tracked config template (28.21). **Диспатч-контракт (2026-08-16):** порядок — волны W0–W4 в шапке реестра (30.8 разблокирована: all-day маркер дедлайна); claim-протокол — `[ ]`→`[~]` коммитом сразу в `dev` до создания worktree (единственный разрешённый self-merge), git-rebase решает гонки, бросил — верни `[ ]`.

## Метаданные

- **Дата снапшота:** 2026-08-17
- **Ветка:** `dev`
- **Статус:** смержено: фазы 0–26 (частичные 🟡 расписаны в PLAN.md), Wave 4, 27.1–27.11, 27.8.4, 28.1–28.21 (полировка полностью закрыта + agent-type-labels + dev/dogfood separation + ops-hardening), Phase 10 subphase (Test send UI), Phase 15 close-out, Phase doc-audit. Multi-user / multi-device sync — следующая эра после полировки.
- **Теги:** `v0.1.0-phase0` … `v0.1.0-wave4-minor` (после тега — серия phase- и docs-коммитов, +27.6/27.7/27.8/27.8.4/27.9/27.10/27.11/28.1/.../28.20/phase-10-test-send/phase-15-agent-context/phase-doc-audit)

## Что сделано за сессию (кратко)

| Phase | Содержание |
|---|---|
| 0 | cobra CLI, chi router, /healthz, /api/v1/info, embed SPA, Vite scaffold |
| 1 | users (CLI), JWT cookie, projects/tasks CRUD, auth middleware, login UI |
| 2 | kanban dnd (@dnd-kit), WS hub (gorilla), fractional positions, move endpoint |
| 3 | agents (Bearer API tokens), claim/release/submit/review, comments + @mentions, attachments (sha256 dedup), activity log, long-poll `/events/await`, `/api/v1/agent/*` namespace |
| 4 | events (RRULE DAILY/WEEKLY/MONTHLY), time_entries (single-active timer), /reports/time |
| 5 | wiki_pages + wiki_links + FTS5 (pages/tasks/comments, unicode61 + diacritics), backlinks, /search |
| 6 | notifications inbox (dedup_key), bot_subscriptions, Console bot, retry/backoff, bell UI |
| 7 | markdown mirror (Obsidian frontmatter), git push scheduler (5m), sqlite snapshots (VACUUM INTO, rotation), WAL archive, CLI + settings UI |
| 8 | PWA: vite-plugin-pwa, IndexedDB outbox, `/api/v1/sync` с идемпотентностью (sync_ops) |
| 9 | security headers + rate limit (429 + Retry-After), zap → lumberjack file rotation, install.sh + systemd unit, docs/API.md + docs/DB.md, benchmarks, dark mode toggle |
| 10 | Bot platform: Webhook (HMAC), Email (SMTP), Telegram (long-poll + inline buttons), VK (callback keyboard), callback handler с replay protection, subscriptions UI |
| **26.A** | **Playwright E2E scaffold** (`@playwright/test`, `e2e-setup/run-server.sh` с `migrate up` + seed user, `webServer` на порту 21371, npm scripts `test:e2e`/`test:e2e:ui`, 1 spec — auth redirect). |
| **26.B** | **vitest auth + layout** (LoginPage + RequireAuth + AppLayout; 84 теста, 11 → 14 файлов). `export function RequireAuth` — `false-friend-proof` testing. |
| **26.C** | **vitest today + inbox + review + notifications** (TodayPage + InboxPage + QuickCapture + ReviewPage + NotificationsBell; 135 тестов). Латентный баг: QuickCapture `submit()` без `catch {}` — unhandled rejection. |
| **26.D** | **vitest long-tail** (calendar + wiki + search + settings/backups + settings/bots + agents + reports + usePasteImage; 188 тестов). |
| **26.E** | **Playwright E2E specs** (today + quick-capture + kanban + review + ws-live; 8/8 pass, 5 прогонов подряд без флейков). Два минимальных прод-изменения под капотом: env-конфиг rate limit (`ORENDA_RATELIMIT_*`) и `/api/v1/me` + `/api/v1/auth/login` в `SkipPaths`. |
| **26.F** | **Makefile wiring + docs** — `make test` += vitest, новый `make test-e2e` target; `docs/SESSION.md` отражает закрытие E2E-пропуска. |
| **27.1** | **web_dist embed** — `//go:embed all:dist` + Makefile `embed-dists` target; бинарь self-contained (index.html внутри), 6 тестов `internal/embed/web/embed_test.go`. |
| **27.2** | **WS cookie auth** — handler читает `orenda_session` cookie first, потом `Authorization: Bearer`, потом `?token=` (deprecated). `useWebSocketConnection` подключён в `AppLayout`. `AuthContext` больше не хранит `token`. `ws-live.spec.ts` ловит реальный WS-фрейм без `page.reload()`. 5 прогонов E2E без флейков. |
| **27.3** | **Tags in list-payload** — `Task.Tags []Tag`, `ListByProjectWithStats` зовёт `TagsForTasks` (5-й aggregate запрос, без N+1). `getTaskHandler` тоже подгружает теги через `ListTagsForTask` (консистентность single-task ↔ list). Чипы на канбане видны одним round-trip; vitest 189/189 (+1 цвет-бейдж тест); E2E `kanban.spec.ts` «Phase 27.3: kanban card renders coloured tag chips from list-payload». 4 прогона подряд без флейков. |
| **27.4.A** | **Course close-out backend** — `MaterializeLesson(lessonID, contentMD, taskID)` (locked→open), `AnswerQuiz(quizID, answer)` (exact+open), `CreateWithIntent` wire'ит `GeneratorTaskID` через `TaskCreator` адаптер. Endpoints: `POST /lessons/{id}/quizzes/{qid}/answer` (user), `POST /agent/lessons/{id}/materialize`, `PUT /agent/lessons/{id}/content` (agent). 14 service-тестов. |
| **27.4.B** | **Course close-out frontend** — `LessonPage.tsx` (markdown + quiz-forms + complete-button), route `/lessons/:id`, `api.answerQuiz` + расширенные типы. E2E `course.spec.ts` happy-path (owner → tutor → student через UI). Vitest 193/193 (+4 lesson-page); E2E 10/10 (+1 course), 3 прогона подряд. Backend bugfix: `UpdateLessonContent` без `updated_at` (таблица `course_lessons` без колонки). |
| **22.3 UI** | **Restore via UI** — модалка Backups теперь предлагает «Restore in this window» (3-step: maintenance on → force restore → reload). Maintenance остаётся вкл после успеха; failure path откатывает maintenance off. Vitest 195/195 (+2 Backups теста); E2E 10/10 (regress нет). Закрывает долг «UI-кнопка в Settings → Backups подсказывает CLI hint» — теперь restore полностью в UI. |
| **22.3+ TG** | **Telegram /start onboarding** — бот на `/start` генерирует 6-hex code (TTL 10min), отвечает юзеру в чат. UI в Settings → Bots → Telegram принимает code, backend дёргает `POST /bots/telegram/bind` → резолвит chat_id → создаёт `bot_subscriptions` с дефолтным event set. 404/410/503 mapped на inline-errors. Vitest 199/199 (+4 bind теста, +6 bind-codes store тестов в Go); E2E 10/10. |
| **27.6** | **Course manual fill (owner-side curriculum + quiz surface + generator-task seam)** — дефект зафиксирован 2026-08-13: курс нельзя собрать без агента. Бэкенд: `Service.SubmitCurriculum(ctx, courseID, modules, lessons, quizzes)` — quiz'ы в той же tx (закрывает Phase 18.6 долг); `StatusTransitionOK` расширен `review→review` (правка на ревью без кругов); `MaybeCompleter` интерфейс + `courseTaskCreatorAdapter.CompleteTask(ctx, taskID, note)` гасит generator-задачу при user-side submit (иначе проснувшийся тьютор перезапишет ручное дерево); `CreateWithIntent(SkipGenerator())` option для wizard-режима «соберу сам». Endpoints: `PUT /api/v1/courses/{id}/curriculum` (user), `POST /api/v1/lessons/{id}/quizzes` (user + agent), `PUT /api/v1/lessons/{id}/content` (user-зеркало materialize). Frontend: новый `CourseCurriculumEditor` (add/rename/delete модулей/уроков/quiz'ов, валидация, atomic save одним PUT); toggle «Edit curriculum» на `CourseDetailPage` для draft/review; «Edit content» на `LessonPage` для owner в active (markdown textarea → PUT content); `CoursesPage` wizard с чекбоксом «I'll build the curriculum myself» + автопереход в editor. OpenAPI (оба файла) + route-coverage sync. Tests: 7 service + 4 SQLite-repo + 7 handler + 6 editor-vitest + 3 LessonPage-edit-vitest; новый E2E `course-manual.spec.ts`. Verify: `make test` Go+vitest 208/208, `make test-e2e` 11/11 (+1 manual-path spec), TypeScript clean, OpenAPI coverage зелёный. |
| **27.7** | **Task fields editable (status / priority / assignee)** — дефект зафиксирован 2026-08-13 (скриншот владельца): три поля read-only в сайдбаре карточки, status ручных задач навсегда `todo`. Бэкенд: `patchTaskHandler` снимает prev state (status/priority/assignee); после `applyTaskPatch` — side-effects: `status=done` без явного `completed_at` → `time.Now().UTC()`; awaiting нормализация (done→none, review→human, иначе→none — владелец взял руль); activity rows пишутся только когда поле реально поменялось (typed JSON payload `{from, to}`). Новый `ActionPriorityChanged` в `activity/model.go`. Frontend: новый `TaskFieldControls` (3 select: status/priority/assignee), инкапсулирует PATCH + label-resolve через `useAuth()` + `api.listAgents()`; «Me» / «owner» / «agent-name (status)». Под select — «currently: …» для fallback-имени. Интегрирован в `TaskViewBody` (работает и в TaskModal через общий body). Tests: 7 handler-side-effect (SQLite+router) + 7 vitest на TaskFieldControls + новый E2E `task-fields.spec.ts`. Verify: `make test` Go+vitest 206/206 (+7 vitest), `make test-e2e` 11/11 (+1 task-fields), TypeScript clean. Колонка на канбане НЕ двигается при смене status (оси разделены; 27.8 их сольёт). |
| **27.8** | **Канбан: колонки = статусы (single axis collapse)** — решение владельца 2026-08-13: «в этом и суть канбана, мы визуализируем статусы». Backend (коммит `9c54817`): миграция `020_columns_status` (backfill имени в `status` + UNIQUE(board_id, status)), `Repository.FindColumnByStatus`, `task.Service.SyncStatusAndColumn` (public seam), agent-flow методы (Claim/Release/Submit/Review) синхронизируют `column_id`, `applyTaskPatch` двунаправленный. Frontend (TaskFieldControls) рендерит Status select из колонок проекта через `api.getBoard(projectID)`; inbox-карточки → read-only label fallback. Tests: миграция test + 6 service tests + 9 vitest (включая 2 новых кейса Phase 27.8) + обновлённый E2E `task-fields.spec.ts` (`column_id !== initialColumnID` после PATCH status). Verify: `make test` Go+vitest 208/208, `make test-e2e` 11/11 (regress нет). |
| **27.9** | **Known gaps** — аудит отложенных швов 2026-08-13 закрыт. **(1)** WS fan-out: `internal/api/ws.AllTopics` (8 топиков) + `subscribeAll(hub, userID)` мерджит каналы; `Handler` зовёт `subscribeAll` вместо `hub.Subscribe(…, "tasks")`. Tests: `TestSubscribeAll_FansOutAcrossTopics` + `TestSubscribeAll_CleanupReleasesAllSubscriptions`. Live-обновления колокольчика/календаря/wiki/таймера теперь реально доходят до браузера (ранее молча терялись на сервере). **(2)** Report titles: `task.Repository.TitlesByIDs(ctx, ids)` — batch SQL; `timeentry.Service` получил узкий интерфейс `TaskTitleLookup` + `WithTitles` builder; `Report` обогащает каждую строку одним запросом (без N+1). Test: `TestTimeEntryService_Report_PopulatesTitles` (3 кейса). **(3)** Course adapter WS/activity: `courseTaskCreatorAdapter` принял `hub ws.Hub` и узкий `courseTaskActivityRecorder`; `notifyCreated` публикует `task.created` (source = `course_generator`/`course_quiz_review`) + пишет activity row `task.created`. Best-effort — nil hub/recorder не паникует. Tests: 3 в `cmd/orenda/main_course_adapter_test.go`. **(4)** Activity verb map unified: backend константы (`ActionCreated`/`Claimed`/…) приняли префикс `task.*` (покрывает старые и новые); фронт verb map в `TaskViewBody.tsx` имеет fallback со старыми spelling — старые audit-rows читаются. **(5)** Comment-debt cleanup: 5 из 6 в списке PLAN.md вычищены (`main.go:650`, `notifier.go:6`, `bot/bot.go:3`, `handlers_dependencies.go:83`, `domain/timeentry/model.go:60`); исторические преамбулы «Phase 1/2 will…» оставлены (low priority). Verify: `make test` Go — все пакеты ok; vitest 215/215 (нет регрессии); `make test-e2e` 12/12 (`task-fields.spec.ts` обновлён под `task.*` prefix); TypeScript clean; OpenAPI coverage зелёный. |
| **27.10** | **Цвет колонки (init/рендер/WS)** — дефект зафиксирован 2026-08-13. Frontend: `ColumnView` получил пропсы `color?` / `wipLimit?`; рендерит `data-testid="column-color-dot"` слева от заголовка (slate fallback если пусто). `EditColumnModal` теперь инициализирует `useState(initialColor ?? '#94a3b8')` и `useState(initialWipLimit?.toString() ?? '')` — reopen показывает сохранённое. Submit отправляет `color` в PATCH только если он отличается от `initialColor` — rename больше не затирает цвет. Backend: `patchColumnHandler` публикует `column.updated` на топик `tasks` (parity с created/deleted). Tests: 5 vitest (`ColumnView.test.tsx`: dot рендерит сохранённый цвет + fallback; модалка открывается с initialColor/initialWipLimit; PATCH rename не содержит `color`; PATCH смены цвета содержит) + `TestPatchColumn_BroadcastsColumnUpdated` (subscribe → patch → assert WS event) + E2E `kanban.spec.ts:Phase 27.10` (dot виден, rgb соответствует hex, переживает rename + reload). Verify: `make test` Go — все пакеты ok; vitest 220/220 (+5); `make test-e2e` 13/13 (+1); TypeScript clean. |
| **27.11** | **Agent comment/await 401 + openapi coverage (аудит документации)** — дефекты зафиксированы 2026-08-13. **(1)** Agent-namespace aliases: `POST /api/v1/agent/tasks/{id}/comments` (author=agent + `Identity.AgentID`) и `POST /api/v1/agent/events/await` (long-poll подписывает WS hub под agent's id). Оба хэндлера в новом `handlers_agent_namespace.go`; оба под `RequireAgent` middleware. CLI `comment`/`await` (`cmd/orenda/agent.go`) переведены на новые endpoints. Tests: `TestAgent_CommentCreatesAgentAuthoredComment` (agent-токен → 201, `author_type=agent`, `author_id=agentID`), `TestAgent_CommentRejectsUserCookie` (cookie на agent-namespace → 401), `TestAgent_AwaitRequiresAgentToken` (no/bad/valid bearer). **(2)** OpenAPI route-coverage против полного роутера: новый `fullRouterDeps` фикстура подключает все deps (users/projects/tasks/tokens/agents/comments/activities/event/time/wiki/search/notifier/courses + WS hub); `TestOpenAPI_RouteCoverage_FullRouter` walks every (method, path) и ассертит наличие в `docs/openapi.yaml`. Сразу поймал две пропущенные routes — добавлены в обе спеки (`docs/openapi.yaml` + embedded `internal/api/openapi.yaml`). SKILL.md known-issue сняты; `comment`/`await` помечены как bearer-token endpoints с явным указанием, что фильтр идёт по `agent_id`. Verify: `make test` Go — все пакеты ok; vitest 220/220; `make test-e2e` 13/13; TypeScript clean. |
| **27.8.4** | **Status select из колонок проекта + латентный gap в `Service.Move`** — дефекты зафиксированы 2026-08-13. **(1) Backend gap в `Service.Move`:** Phase 27.8 закрыл инвариант `task.status ≡ column.status` для agent-flow (claim/release/submit/review) и PATCH (applyTaskPatch), но **пропустил `Move`** — DnD менял `column_id`, оставляя `status` на старом значении. Фикс в `internal/service/task/move.go::Move`: после fixup `tr.ProjectID` (Phase 16) добавлен блок `if s.Columns != nil { col, err := s.Columns.GetColumn(ctx, opts.TargetColumnID); if err == nil && col.Status != "" { tr.Status = task.Status(col.Status) } }` — симметрично существующему `syncColumnToStatus` (status → column). Заодно: `internal/storage/sqlite/project_repo.go::GetColumn` не возвращал `status` (SQL не подтягивал поле) — добавил `c.status` в SELECT/Scan. **(2) Frontend:** `BoardColumn` TS тип получил `status?: string`; `TaskFieldControls` теперь принимает prop `projectID: string`, `useEffect` зовёт `api.getBoard(projectID)`, деривит `statusOptions` из `columns`, отсортированных по `position` (отображаются кастомные колонки Phase 12). Defensive fallback на канонический enum при ошибке сети. **Inbox fallback:** `projectID === ''` → рендерится `SidebarReadOnlyField` с label «Inbox task — assign to a project to change status» (у inbox-задачи нет колонки, статус некуда перемещать). `TaskViewBody` пробрасывает `projectID={task.project_id ?? ''}`. Tests: `TestService_Move_ColumnDrivesStatus` (seed todo+done колонки, Move → status=done, persisted check, обратное направление); vitest `TaskFieldControls` 7→9 тестов (helper `mockBoard()`, новый кейс sorted+custom columns, новый кейс inbox readonly). E2E `Phase 27.8.4: moving a task to the done column flips status to done` — move API → reload → assert `task.status==='done'` И `task-status` select reads `'done'`. Verify: `make test` Go — все пакеты ok; vitest 222/222 (+2); `make test-e2e` 14/14 (+1); TypeScript clean. **Phase 27.8 закрыт полностью.** |
| **28.1** | **Полировка — backup_settings write path** — дефект зафиксирован 2026-08-13: `PUT /api/v1/backups/settings` → 501, UI Settings → Backups read-only, единственный путь настроить remote — ssh + vim config.yaml + restart. **Backend:** новый `internal/storage/sqlite/backup_settings_repo.go` с JSON-blob storage поверх существующей `backup_settings` таблицы (001_init:303, ранее никем не использовалась — 0 hits INSERT/UPDATE/DELETE/SELECT). `handlers_backup.go::putBackupSettingsHandler` 501 → 200 с валидацией (URL `Parse`, схемы http/https/ssh/git, `enabled=true` требует URL). `listBackupSettingsHandler` теперь мерджит DB override над in-memory cfg: `source_hint=ui_override_restart_to_apply` когда DB diverges. PUT response тоже несёт `source_hint` (без него форма теряет restart banner после Save — поймано первым прогоном E2E). Новое поле в `Dependencies.BackupSettings` (SQLite-only repo, no filesystem deps — wire-test friendly). **Wiring:** `cmd/orenda/main.go` создаёт repo рядом с другими. `Service` иммутабельный после `New()`, settings применяются на следующем старте процесса — отдельный hot-reload долг за скобкой (см. PLAN §28.1). **Frontend:** `web/src/features/settings/Backups.tsx` read-only `<dl>` → редактируемая форма (checkbox enabled, URL input, password input для token, Save). `formInitialized` flag — форма синкается с сервером только на initial load, не после каждого fetch (не пре-fillит auth поле — secret никогда не возвращается). Restart banner показывается при non-empty `source_hint`. `api.setBackupSettings(body)` + `BackupSettingsInput` тип в client. **Tests:** 8 repo + 7 handler (без них уже проходил PUT 401, потому что без cookie; добавил `loginAndCookie` helper в фикстуру) + vitest Backups обновлены (3 → 12 тестов, old «read-only settings panel» заменён на editable); E2E `backups-settings.spec.ts` (1 новый happy-path через UI: fill + Save → reload → GET reflects + source_hint). **DoD:** `go test ./...` 0 fail; `npx vitest run` 224/224 (+2); `make test-e2e` 15/15 (+1); `npx tsc --noEmit` clean; OpenAPI `PUT` теперь документирует `requestBody` + 400/503 codes. |
| **28.2** | **Полировка — Settings hub page** — дефект зафиксирован 2026-08-13: `/settings` рендерил `<Placeholder title="Settings" />`, из сайдбара (⚙ Settings) пользователь попадал в пустоту; подстраницы `/settings/backups` и `/settings/bots` рабочие, но из индекса недостижимы. **Frontend-only:** новый `web/src/features/settings/SettingsHome.tsx` — 4 карточки-ссылки (Backups, Bots & notifications, Agents, Reports) + блок About (uptime, DB size, requests, WS clients) с типизированного `/api/v1/stats` (Phase 24 observable, public endpoint). `App.tsx` подмонтирован `/settings → SettingsHome` (вместо `<Placeholder>`). `client.ts` дополнен `api.getStats()` + `StatsResponse` interface, mirroring `internal/api/handlers_stats.go::statsResponse`. Заметка «Theme lives in the top bar» — вводный абзац (тема — не отдельная страница). **Не наступал на `/api/v1/info`:** version уже рендерится в Footer (Phase 0), дублировать в About избыточно. **Тесты:** 4 vitest на SettingsHome (карточные hrefs, About fields populated, graceful failure на /stats error, byte formatting), E2E `settings-home.spec.ts` (sidebar ⚙ → /settings → click Backups → /settings/backups → About populated). **DoD:** `go test ./...` 30/30 ok; `npx vitest run` 228/228 (+4); `make test-e2e` 16/16 (+1); `npx tsc --noEmit` clean. Бэкенд не тронут. |
| **28.3** | **Полировка — TaskModal scroll fix** — баг зафиксирован 2026-08-13: при длинном контенте карточки появляются 2 полоски прокрутки + верх карточки недостижим скроллом. **Диагноз по коду (в PLAN.md):** backdrop `flex items-start md:items-center` + `overflow-y-auto` → при переполнении `items-center` уводит верх flex-item в отрицательное переполнение; нет body scroll-lock (фон прокручивается под модалкой). **Фикс:** (1) backdrop без `md:items-center` (всегда `items-start`); карточка получила `my-auto` — auto-margin центрирует короткую карточку и схлопывается к padding при переполнении, верх достижим скроллом. (2) Scroll-lock вынесен в переиспользуемый hook `useBodyScrollLock` (`web/src/shared/hooks/useBodyScrollLock.ts`): snapshot предыдущего `body.style.overflow` на mount, `hidden` пока смонтировано, restore снапшота на unmount (не «clobber» чужого inline style). Hook без зависимостей — смена `id` внутри модалки (Navigate на child task) сохраняет lock, потому что `TaskModal` остаётся смонтированным, меняется только `useParams().id`. TaskModal использует hook вместо inline `useEffect`. **Тесты:** 4 vitest на хук (`mount → hidden`, `unmount → restore`, `non-empty previous overflow restored verbatim`, `lock persists across rerenders`) + 4 vitest на структуру `TaskModal` (backdrop без `items-center` + `overflow-y-auto`; карточка `my-auto` без `my-4 md:my-0`; body lock end-to-end через хук; клик по карточке не bubble'ится в backdrop) + E2E `task-modal-scroll.spec.ts` (viewport 1280×600, описание 5 KiB, click card → модалка; проверки `dialog.overflowY==='auto'`, `body.overflow==='hidden'`, `scrollHeight>clientHeight`, `scrollTop=0` достижим, `document.scrollY===0`, Esc + клик вне карточки закрывают, lock отпускается). **DoD:** `go test ./...` 30/30 ok; `npx vitest run` 236/236 (+8); `make test-e2e` 17/17 (+1); `npx tsc --noEmit` clean. Бэкенд не тронут. |
| **28.4** | **Полировка — security defaults** — дефекты зафиксированы 2026-08-13: `config.DefaultConfig.JWTTTL` 168h (OWASP-рекомендация для cookie-session — 24h) и `Secure: false` хардкод в `handlers_auth.go:65` (+ logout без Secure). Оба forward-only: выпущенные до изменения cookie валидны до истечения (JWT exp вшит в токен), новые выпускаются строже. **Backend:** `config.go:136` `JWTTTL: 168h → 24h`; `router.go::Dependencies` получил `CookieSecure bool` + `JWTTTL time.Duration`; `main.go` прокидывает `cfg.Auth.CookieSecure` и `cfg.Auth.JWTTTL` в deps; `loginHandler` использует `Secure: deps.CookieSecure` + `Expires: time.Now().Add(deps.JWTTTL)` (раньше было хардкод `false` и `24 * time.Hour`); `logoutHandler` тоже берёт `deps.CookieSecure` (иначе MaxAge=-1 удалит только не-secure cookie set, secure login cookie переживёт logout). Поле `AuthConfig.CookieSecure` уже существовало — фазе нужно было просто прокинуть его до handlers. **Test seams:** новые `LoginHandlerForTest` / `LogoutHandlerForTest` экспорты в `handlers_auth.go` — тесты атрибутов cookie живут рядом с handlers, а не тянут весь `NewRouter`. **Тесты:** `config_test.go` (`TestDefaultConfig` asserts TTL=24h, новый `TestLoad_JWTTTLFromYAML`, `TestLoad_EnvOverridesYAML` расширен env-ами `JWT_TTL`/`COOKIE_SECURE`) + `handlers_auth_test.go` NEW (in-memory `pwUserRepo`, 3 кейса `TestLogin_CookieAttributes` loopback/HTTPS/168h-legacy, `TestLogin_InvalidCredentials_Returns401` regression-guard на «не выставлять cookie при провале», 2 кейса `TestLogout_CookieAttributes`). **DoD:** `go test ./...` 30/30 ok (config +2, api +5); `npx vitest run` 236/236 (фронт не тронут); `make test-e2e` 17/17 (регрессии login/logout нет); `npx tsc --noEmit` clean. **За скобкой:** `data/config.example.yaml` не в git (живёт только в working tree оператора + через `!data/config.example.yaml` в `.gitignore`); `install.sh` использует его как шаблон. Стоит обновить `jwt_ttl: "168h" → "24h"` локально у оператора при следующем install. Альтернатива — выкатить `docs/config.example.yaml` под версионирование и переключить `install.sh` (отдельная мини-фаза). |
| **28.5** | **Полировка — activity emission + Bot.Stop on shutdown** — два small audit-debt'а: `task.commented` / `task.attachment_added` (declared never emitted с Phase 6) и `Bot.Stop()` никогда не вызывался на shutdown (long-poll транспорты SIGKILL'ились после `ShutdownTimeout`). **Backend:** новый узкий `ActivityRecorder` интерфейс в `api/service_interfaces.go` (1 метод, nil-safe, паттерн `deps.Notifier`); `Dependencies.ActivityRecorder` wired к `*activityservice.Recorder` (structural satisfaction, без explicit адаптера); `createTaskCommentHandler` + `agentCreateTaskCommentHandler` + `addTaskAttachmentHandler` зовут `RecordTask` после primary mutation. Log-on-error (`zap.Warn`) — audit gap recoverable, failed user-visible request нет. Dedup-attach (`res.Duplicate=true`) пропускается — `res.Attachment` указывает на existing row, лишний «added» event обманывает timeline. `cmd/orenda/main.go` shutdown loop теперь `for _, b := range botRegistry.List() { b.Stop(shutdownCtx) }` после `srv.Shutdown`; best-effort — failing bot не валит loop. **Тесты:** `internal/bot/bot_test.go` +3 (`TestConsole_Stop_ReturnsNil` defensive double-stop, `TestRegistry_ShutdownLoop_StopsEveryBot`, `TestRegistry_ShutdownLoop_OneFailingBot_Continues` — пинит best-effort contract). **Pre-existing infra fix:** `web/e2e-setup/run-server.sh` теперь `mkdir -p data/uploads/` — attachment service `CreateTemp(s.UploadDir, ...)` требует директорию; без неё первый attachment upload 500'ит на свежем worktree. Попутный полезный фикс для будущих attachment E2E. **E2E:** `task-activity.spec.ts` NEW — POST comment → GET /activity → assert `task.commented` row + decoded payload. Attachment emission в том же handler-коммите, но в E2E не пинится (нужен uploads/, scope small). **DoD:** `go test ./...` 30 packages ok (bot +3); `npx vitest run` 236/236; `make test-e2e` 18/18 (+1); `npx tsc --noEmit` clean. Бэкенд + минимальный e2e-setup fix. |
| **28.6** | **Полировка — opt-in pprof + govulncheck target** — два small infra-долга. **Backend (`config.go`):** `ServerConfig` получил `DebugPProf bool` (default false) + `PProfAddr string` (default `127.0.0.1:6060`). Loopback-only by design — exposing heap/goroutine state на любом reachable port = information leak. Env overrides `ORENDA_SERVER__DEBUG_PPROF` / `ORENDA_SERVER__PPROF_ADDR`. **Backend (`main.go`):** `_ "net/http/pprof"` import (side-effect registration на DefaultServeMux); если `cfg.Server.DebugPProf`, запускается второй `http.Server` в goroutine, биндится на PProfAddr; на shutdown `pprofSrv.Shutdown(shutdownCtx)` под тот же timeout. **Тесты:** `config_test.go` (`TestDefaultConfig` asserts `DebugPProf=false` + `PProfAddr="127.0.0.1:6060"`; `TestLoad_EnvOverridesYAML` расширен env-ами `DEBUG_PPROF`/`PPROF_ADDR`). **Infra (`Makefile`):** новый target `govulncheck` с install-gate — `go install golang.org/x/vuln/cmd/govulncheck@latest` если `which govulncheck` пусто; затем `go run @latest ./...` для скан. Симметрично существующему `lint`. **Manual smoke:** `ORENDA_SERVER__DEBUG_PPROF=true bin/orenda serve` → лог `pprof listening (debug only)` + `/debug/pprof/heap` возвращает 200; без флага порт 6060 closed. `make govulncheck` нашёл GO-2026-5856 в stdlib 1.26.4 (Encrypted Client Hello privacy leak, фиксится в 1.26.5) — exit 3, **не моя проблема**, target работает корректно. **DoD:** `go test ./...` 30/30 ok (config +2 assertion); `npx vitest run` 236/236 (фронт не тронут); `npx tsc --noEmit` clean; manual smoke (pprof on/off) + `make govulncheck` exit 3. Бэкенд + Makefile. |
| **10.ts** | **Phase 10 subphase — Test send UI** — дефект зафиксирован 2026-08-12 в аудите: «нет "Test send" в UI» — оператор не мог проверить credentials бота, не дождавшись реального события. **Endpoint:** `POST /api/v1/bots/test {bot_type, target_address}` — независим от subscription store, диспатчит через существующий `bot.Registry`. Console исключён из whitelist (нет user-facing signal). Per-bot target pre-check (webhook http(s), email @+., telegram/vk numeric) — UX pre-filter до transport round-trip. **Status codes:** 200 / 400 invalid_input / 400 unknown_bot_type / 400 per-bot pre-check / 503 bot_not_running / 503 bot_registry_not_wired / 502 send_failed. **Frontend:** новая карточка в `Bots.tsx` (dropdown + target input + submit + green/red banners). `data-testid="bot-test-type"` + `"bot-test-target"` + `"bot-test-submit"` + `"bot-test-result-{ok,err}"`. Add subscription select получил `data-testid="add-subscription-bot-type"` для disambiguation от нового test-send select'а. **Tests:** 9 Go tests + 16 subtests (`handlers_bots_test.go::TestBotsTestHandler_*`); 5 vitest tests на UI; `TestOpenAPI_RouteCoverage_FullRouter` обновлён. **Manual smoke:** Python HTTP sink + webhook → sink получил payload с `kind: "test"`, `title: "Orenda test message"`. **DoD:** `go test ./...` 30/30; `npx vitest run` 246/246 (+5); `make test-e2e` 18/18; `npx tsc --noEmit` clean; OpenAPI оба файла синхронны. |

## Ключевые решения (не забыть)

- **Phase 14: subtasks → child tasks (Weeek-style).** Раньше `subtasks` и `checklists` дублировали друг друга — два API с одинаковым UI-смыслом. Теперь: subtasks стали полноценными задачами через `tasks.parent_task_id` (поле было в схеме с 001, но никем не заполнялось); checklists остались локальными чекбоксами. Миграция `013_subtasks_to_children.sql` переливает существующие subtasks в tasks и дропает таблицу. Activity log получил `task.child_added`, `task.checklist_added`, `task.checklist_item_added`, `task.checklist_item_done`. `GET /api/v1/tasks/:id/context` теперь возвращает и `children`, и `checklists` (раньше агенты видели только subtasks). Маркдаун-зеркало пишет checklists вместо subtasks.
- **Видение (согласовано 2026-08-11):** Orenda — персональная ОС делегирования: человек формулирует намерения, внешние AI-агенты исполняют через API как first-class участники workflow; задачи/календарь/wiki — общая память и контекст для человека и агентов.
- **Аудитория:** личный инструмент сейчас, публичный self-hosted продукт потом. Код и архитектуру делаем «на вырост» (миграции, docs, install-flow не ломаем), но фичи выбираем по собственному UX, а не по гипотетическим пользователям.
- **Phase 26: верификация фронтенда (решение 2026-08-11):** ранее зафиксированный пропуск E2E отменён. В план добавлена Phase 26 — два слоя: Playwright smoke против реального бинаря (только Chromium, tmp-БД, тестовый порт) + добивка vitest component-покрытия (13 feature-директорий без тестов). После фазы `make test` включает vitest; E2E — отдельный `make test-e2e`, локальный/предрелизный гейт (CI нет).
- **Worktree placement (2026-08-11):** AGENTS.md ужесточён — только вложенный `.worktrees/<task>`, sibling `../` запрещён.
- **Phase 19: review queue** — закрыл асимметрию делегирования: человек→агент работало давно (claim/submit), агент→человек ограничивалось одним notification, легко теряемым. Теперь `GET /api/v1/review-queue` отдаёт всё ожидающее решения (`awaiting='human' OR status='review'`) одним запросом с джойном проектов; сайдбар показывает live-бейдж с числом, `/review` — плоский список с inline Accept/Return. Return требует комментарий (агентам нужна обратная связь). WS auto-refresh на `/review` и на бейдже.
- **Phase 27.9: WS multi-topic fan-out** — single-owner продукт, per-project подписки не нужны; одно соединение = все 8 топиков (tasks/agents/attachments/comments/events/notifications/timers/wiki). Контрактная граница: `internal/api/ws.AllTopics` — добавляешь топик, он автоматически доходит до UI (без per-surface wiring). Per-project фильтр встанет с multi-user/ACL — пока не нужен.
- **Phase 15: зависимости задач + ready-листинг для агентов** — задачи могут блокировать друг друга через новую таблицу `task_dependencies` (миграция 016). `PUT /tasks/{id}/dependencies` replace-семантика, цикл/self-loop → 422. `Service.Claim` отказывается брать заблокированную задачу → 422 с `unfinished_blockers: [id,...]`. `GET /api/v1/agent/tasks?ready=true` — листинг для агентов с фильтром «готово к параллельной работе» (блокеры done + лок свободен + не in_progress + не assigned to other agent). Cycle prevention — DFS на service-слое до записи. WS-событие `task.deps_changed`.
- **Phase 22: restore-from-snapshot** — закрыл бэкап-контур Phase 7: снепшот без проверенного restore — не бэкап. CLI `orenda backup restore --from <path> --yes` теперь делает полный pipeline: server-running guard → safety-copy в `<dest>.pre-restore-<unix-ts>` (операторский escape hatch) → атомарный swap → `migrate up` до текущей схемы → `PRAGMA integrity_check` + `PRAGMA foreign_key_check`. Restore переименован в старую логику; новая `runBackupRestoreWithVerify` — точка входа. UI-кнопка в Settings → Backups уже подсказывает CLI hint (HTTP-эндпоинт отдаёт `hint` потому что сервер бежит). **Phase 22.3 follow-up:** maintenance mode (POST /maintenance/on, /off, GET /maintenance) — atomic.Bool блокирует non-GET методы; in-process restore через `POST /backups/restore {force: true}` с maintenance-on. WS drain через `deps.WSHub.Close()`. Maintenance остаётся вкл после успешного restore — оператор сам выключает, чтобы успеть проверить.
- **Phase 17: rich task cards** — лицевая сторона карточки отвечает на 80% вопросов без открытия задачи. Бэк: `ListByProjectWithStats` — один listByProject + 4 агрегатных запроса (comments / attachments / children / checklist_items) + open-blockers count (Phase 15). Фронт: `TaskCard` декомпозирован на `PriorityBorder` (urgent=red, high=orange, low=slate), `TaskDueBadge` (overdue/today/upcoming/done), `AssigneeChip` (agent=🤖, user=инициалы), `AwaitingBadge`, `BlockedBadge`, `Counters`. Плотность: localStorage-флаг `orenda.kanban.cardDensity` (compact/detailed). Чистые функции `taskCardBadges.ts` — unit-тестируемые отдельно. **Hotfix migration 015**: обнаружено, что migration 015 оставила `checklist_items.checklist_id` FK с reference на удалённую `checklists_old` — `INSERT INTO checklist_items` падал с `no such table: main.checklists_old`. Migration 017 пересоздаёт таблицу с правильным FK и сохраняет данные. Чек-лист-фича фактически была сломана с момента шеринга Phase 16; никто не замечал, потому что никто не вставлял checklist items в проде.
- **Phase 21: quick capture в Inbox** — захват мысли ≤ 1 хоткей из любого экрана: глобальная модалка-палитра через React Portal, открывается `q` или `Cmd/Ctrl+K` (с авто-фокусом на textarea), `Cmd/Ctrl+Enter` сабмитит, Esc закрывает. Кнопка `+` в правом нижнем углу (fixed, всегда видна) открывает ту же модалку. Submit → Inbox (Phase 16 endpoint), затем toast с кнопкой «Open task» (навигирует на `/tasks/:id`) либо Dismiss. **Phase 21.3 follow-up:** Telegram auto-capture — приватное сообщение в бот → Inbox task. Bot poll loop wire'ит OnMessage; lookup в bot_subscriptions по chat_id → user_id; create inbox task + reply "✅ Captured to Inbox". Длинные сообщения (>200 символов) обрезаются с "…".
- **Phase 20: экран «Сегодня» (daily driver)** — домашняя страница отвечает «что у меня сегодня»: просроченное (красная секция), due сегодня (янтарная), запланированное по времени (scheduled_today, из календарных задач), ожидающие меня (ссылка на Phase 19 /review). Один round-trip: GET /api/v1/today возвращает overdue/due_today/scheduled_today/awaiting_count одной выдачей; фронт ре-фетчит по WS topic 'tasks'. Старый Dashboard с stats-карточками снят: та же информация теперь живёт на TodayPage + Reports. **Phase 20 follow-up:** active_timer (lookup через TimeService.ActiveTimer → /today), upcoming_week (next-7-days bucketed by date), TZ boundary tests (overdue < midnight < due_today < endOfDay).
- **Phase 24: OpenAPI + наблюдаемость** — машиночитаемый контракт для внешних агентов (генерация клиентов) + минимальная наблюдаемость self-hosted инстанса. `docs/openapi.yaml` (вручную поддерживаемый, все 105 routes задокументированы), `GET /api/v1/openapi.yaml` (публичный, embed.FS — спека живёт внутри бинаря). Route-coverage тест (`TestOpenAPI_RouteCoverage`) обходит chi router и сверяет каждый роут с спекой — забытый endpoint ловит CI. `/api/v1/stats` + slow-request log (>500ms → zap.Warn) уже были. Phase 24 final: всё.
- **Phase 18: личные курсы, создаваемые ИИ-агентами (LMS-модель)** — courses → course_modules → course_lessons → course_quizzes (свои таблицы, не композиция «проект + wiki»). Миграция 019_courses.sql. Оркестрация через задачи: создание курса порождает generator-задачу (`awaiting=agent`), open-ответ на quiz — review-задачу тьютору (обе wired в Phase 27.4, claim штатным agent-flow). Полный цикл: createWithIntent → submitCurriculum (atomic swap, draft→review) → approveCurriculum (→active, первый урок open) → MaterializeLesson (locked→open, контент + задача-упражнение) → AnswerQuiz (exact — нормализованный compare; open — review-задача) → CompleteLesson (следующий open; последний — курс done). **Phase 18.7 follow-up:** frontend страницы — CoursesPage (list + create wizard) + CourseDetailPage (tree view с progress). Sidebar навигация с глифом 🎓. API client расширен методами listCourses / createCourse / getCourse / approveCourse / requestCourseChanges / completeLesson.
- **Курсы: ручное наполнение (решение 2026-08-13).** Наполнение курса только агентом — дефект, не дизайн: владелец должен собрать программу сам. Проверенные пробелы: user-side мутаций дерева нет вообще; quiz creation не экспонирован ни в одном namespace (долг 18.6, `course.spec.ts` это фиксирует комментарием); curriculum-swap деструктивен для прогресса → структурное редактирование только в draft/review, в active — только контент уроков; ручной submit обязан гасить generator-задачу, иначе проснувшийся тьютор перезапишет ручное дерево. План — **Phase 27.6** в PLAN.md.
- **Правило «DoD is binary» (2026-08-13).** AGENTS.md: новая секция «Definition of Done is binary» + пункт в «What NOT to do». Задача сдаётся либо целиком (каждый пункт DoD подтверждён выполненной проверкой — тест, smoke, вывод команды), либо как частичная с явным списком пробелов. Чекбокс в PLAN.md `[x]` — только при полном DoD. Причина: Phase 18 дважды «закрывалась» без MaterializeLesson/AnswerQuiz/страницы урока; quiz-creation endpoint так и не появился ни в одном namespace.
- **PR-шаблон с DoD-чеклистом (2026-08-13).** `.github/PULL_REQUEST_TEMPLATE.md` механизирует правило «DoD is binary»: таблица «пункт DoD → evidence (команда → результат)», обязательная секция «Не сделано» (непустая = DRAFT), чеклист проверок (`make test`/`lint`/`test-e2e`, чекбоксы PLAN.md по факту, no-stubs, reindex knowledge graph, аддитивность миграций). Шаг 7 «When you start a phase» в AGENTS.md ссылается на шаблон.
- **Карточка задачи: Status/Priority/Assignee (дефект + решение 2026-08-13).** Две оси разведены по дизайну: `status` (workflow, двигается agent-flow) и `column_id` (канбан, двигается DnD); исторически совпадали (`DefaultColumns` = имена статусов, Phase 1) — отсюда ложное ожидание синка. Дефект: UI не даёт менять ни одно из трёх полей (PATCH API всё принимает), status ручных задач навсегда `todo`, assignee рисуется сырым `type:id`. Авто-синк колонка↔статус: сначала отклонён, **в тот же день принят владельцем → Phase 27.8 (колонки = статусы)**. План — **Phase 27.7**: три поля — редактируемые контролы в карточке, сайд-эффекты статуса на бэке (`done`→`completed_at`, нормализация `awaiting`), assignee по имени.
- **Колонки = статусы (решение владельца 2026-08-13, отменяет «оси не сливаем» из 27.7).** «В этом и суть канбана — мы визуализируем статусы». Единая ось: `columns.status` — machine key; кастомные колонки = кастомные статусы; инвариант `task.status ≡ status колонки` с синком обеих сторон (DnD, select карточки, agent-flow — всё двигает и статус, и карточку). Риск обхода review-flow перетаскиванием осознанно принят (single-owner, обратимо, activity). План — **Phase 27.8**.
- **Аудит отложенных швов (2026-08-13).** Проход по маркерам (TODO/FIXME/for now/«Phase N will»/placeholder/later/skip) нашёл 3 реальных дефекта: WS fan-out только топика `tasks` (live колокольчика/календаря/wiki/таймера молча мёртв — 27.2 чинила подключение, не подписки); пустые заголовки в `/reports` («Phase 5 adds it» не приземлилась, фоллбэк `task_id[:8]`); course-задачи без WS/activity (бейдж review-очереди не загорается до рефетча). Плюс comment-debt (`main.go` «not yet exposed», notifier «console for now», bot «Phase 10 adds Telegram» и др.). Чистых TODO/FIXME в коде нет — молча устаревают именно комментарии-обещания. План — **Phase 27.9**.
- **Цвет колонки (дефект 2026-08-13, закрыт 27.10).** Бэкенд сохранял корректно (в живой БД `done` = `#1463d2`), фронт был сломан втройне: цвет нигде не рендерился (`ColumnView` без пропа color), `EditColumnModal` инициализировал цвет хардкодом `#94a3b8` (reopen показывал дефолт), submit всегда слал color → rename затирал выбор. Плюс `patchColumnHandler` не публиковал WS `column.updated`. **Закрыто в Phase 27.10:** dot в шапке через `data-testid="column-color-dot"`; `useState(initialColor ?? '#94a3b8')` в модалке; submit шлёт `color` только при отличии от `initialColor`; `patchColumnHandler` публикует `column.updated`.
- **SESSION.md: реновация + правило гигиены (2026-08-13).** По реплике разработчика «SESSION.md устарел» сверка с git log подтвердила: шапка 2026-08-12, «Метаданные» от 2026-08-08 («все 10 фаз завершены», теги до phase10 при реальном `v0.1.0-wave4-minor`), бэклог содержал 6 уже смерженных пунктов (27.1, 27.2, 27.3, restore, telegram, openapi), «Файлы» утверждали «все выполнены» при открытых 27.6–27.10, счётчики тестов (188/8) отставали от последних прогонов (199/10). **Правило:** при закрытии пункта бэклога его строка удаляется из SESSION.md «Бэклога» в том же коммите, что и статус в PLAN.md; шапка и «Метаданные» обновляются при каждой смене даты снапшота.
- **docs/CONTEXT.md (2026-08-13, с уточнением владельца).** Файл **доменного контекста**: общие ментальные модели (канбан, LMS, делегирование, inbox, auth, бэкап) — «что это такое в мире», без директив, номеров фаз и статусов. Цель: не изобретать заново то, что индустрия придумала (кейс-обоснование: отдельная ось статусов рядом с колонками). Не дублирует PLAN/API/SESSION; зарегистрирован в AGENTS.md «Key files to read first».
- **Аудит консистентности документации (2026-08-13).** Три скаута + сверка с git: DB.md отставал на 9 миграций («17 таблиц» при 25; перечислены дропнутые subtasks/events; нет courses, task_dependencies, columns.status; down-миграции не описаны); API.md — минус ~30 роутов + фантомные POST на `/courses/{id}` и `/events/{id}`; openapi.yaml — 6 broken точек (фантомный PATCH /pages/{slug}, maintenance без auth, «4 columns»), а embedded-копия протухла (без блоков 22.3/27.4) — синхронизирована; SKILL.md — MCP «planned» (shipped), нет claim/materialize/content, comment/await задокументированы, но 401 — код-баг → **закрыт в Phase 27.11** (agent-namespace aliases); AGENTS.md — нет internal/mcp, «golang-migrate» вместо кастомного runner'а; README — roadmap до Phase 10, install без node_modules; CHANGELOG — пустой скелет → заполнен по фазам. Исправлено одним проходом; код-дефекты (27.11) — **закрыты 2026-08-13**.
- **Prometheus отклонён (2026-08-13).** Для single-binary single-user local-first приложения pull-модель Prometheus (второй always-on процесс + TSDB + scrape-конфиг) не окупается: «жив ли / где ошибки / что тормозит» покрыто `/api/v1/stats` + slow-request log (Phase 24). Имеет смысл только в эру публичного self-hosted (экспорт `/metrics` для чужих инстансов) или multi-user с SLO. Вычеркнут из PLAN (§9.4, аудит Phase 9, §27 out-of-scope) и SESSION «Полировка»; pprof и prettier остаются.
- **Phase 25: agent DX — CLI + SKILL** — `orenda agent` cobra-сабкоманды (`me`, `next`, `context`, `claim`, `release`, `submit`, `comment`, `await`) поверх уже существующего REST API. Конфиг: флаги > env > `~/.config/orenda/agent.yaml`. Exit code 2 = "no work" для shell-циклов. Документ `docs/skills/orenda/SKILL.md` — полный workflow + этикет + reference. **MCP server (Phase 25.1+25.2 follow-up):** stdio JSON-RPC 2.0 сервер в `internal/mcp` (zero deps) + `orenda mcp-proxy` CLI. Инструменты: orenda_me, orenda_list_tasks, orenda_claim, orenda_release, orenda_submit, orenda_context, orenda_await. Без новых SDK зависимостей.
- **Модель агентов:** внешние, через REST/long-poll (`/api/v1/agent/*`, claim/submit/review). Встроенный LLM-рантайм — опция, не цель; домен не должен жёстко зашивать предположение «агент всегда снаружи».
- **Горизонт:** dogfooding и стабильность. Критерий ценности задачи — «использую каждый день без трения». Приоритетные пробелы: restore-from-snapshot (снапшоты есть, восстановления нет), UX-трение ежедневных сценариев, наблюдаемость. Multi-user и интеграции календарей — за скобкой, пока ядро не «живётся».
- **Миграции аддитивны** — 001_init.sql содержит всю схему; 002+ добавляют только индексы/триггеры. Исключение: 007 пересоздаёт `time_entries` чтобы убрать FK на agent_id.
- **Phase 26 — верификация фронтенда закрыта (A–F, 2026-08-12).** Решение `make test && npx vitest` свернулось в `make test` (Go + vitest в одном таргете). E2E — отдельный `make test-e2e` (Chromium only, tmp-БД на порту 21371, локальный/предрелизный гейт). Worktree placement ужесточён в 26.C: только вложенный `.worktrees/<task>`, sibling `../` запрещён. Покрытие потоков (а не процентов): 188 vitest (auth/layout/today/inbox/review/notifications/calendar/wiki/search/settings/agents/reports/attachments) + 5 Playwright E2E (today/quick-capture/kanban/review/ws-live). Mutation check (PLAN §26 DoD) использован — инверсия `t.DueAt.Before(startOfDay)` в `handlers_today.go` флипает today.spec.ts red; инверсия `comment === null` в ReviewPage — оставлена как будущий pin (текущий E2E test проверяет data path, а не event handler). Капот правки: `internal/api/ratelimit.go` теперь читает `ORENDA_RATELIMIT_{AUTH,ANON}_{BURST,PER_SEC}` env vars (production defaults 300/100 auth, 60/20 anon не изменились); `/api/v1/me` + `/api/v1/auth/login` добавлены в `SkipPaths` (cheap auth state probes, called on every page mount — не должны жечь bucket). `e2e-setup/run-server.sh` hard-codes test override (1M / 100k) чтобы ~200 auth'd calls per spec не триггерили 429.
- **Две auth-модели**: cookie JWT для UI (`RequireUser`), Bearer API-token для агентов (`RequireAgent`, namespace `/api/v1/agent/*`).
- **task_locks PK(task_id)** — атомарный claim; FK нарушение → `ErrLockNotFound` → 404.
- **Service-структура**: `internal/service/{task,agent,comment,attachment,activity,event,timeentry,wiki,search,notifier}`; адаптеры в `cmd/orenda/main.go` (tokenMinterFor, commentAdderFor, taskRecorderFor, attachmentServiceFor, reviewDeciderAdapter, ownerResolverAdapter).
- **Hub**: `ws.Hub` в `internal/api/ws`; non-blocking publish, drop-on-full; per-user filter через body.user_id.
- **Notifier dedup**: `(user_id, dedup_key)` перезаписывает последний unread.
- **Bots**: `internal/bot` интерфейс + Registry; config-driven через `cfg.Bots`.
- **Uploads**: `data/uploads/YYYY/MM/{uuid}-{sanitized}`, mime allowlist в config.
- **Версия из git**: `-X main.version=$(git describe --tags --always --dirty)`.

## Быстрый старт

```bash
make build
./bin/orenda migrate up
echo "pw" | ./bin/orenda user create --email you@x.com --display-name You --password-stdin
ORENDA_AUTH__JWT_SECRET=$(head -c32 /dev/urandom | base64) ./bin/orenda serve
# → http://127.0.0.1:2137
```

Или `scripts/install.sh --systemd` для user-service установки.

## Тесты

**Последние зафиксированные прогоны** (30.13 granular curriculum CRUD, 2026-08-17):
- `make test` — Go (30/30 packages ok) + vitest (307/307).
- `make test-e2e` — 20/20 pass (+1 `course-structure.spec.ts`).
- `npx tsc --noEmit` — clean; `go build ./...` — clean; `golangci-lint --new-from-merge-base=origin/dev` — exit 0.
- Mutation check: инверсия order-detection в `diffCurriculum` флипает 4 granular vitest red; revert — зелёный.
- Coverage: domain 100%, services 70–100%, api 61%, storage 72%; фронт — компонентный/юнит (vitest + jsdom), e2e (Playwright + реальный бинарь).

### Запуск E2E локально

```bash
make build                 # Phase 27.1: SPA встроена в бинарь через //go:embed
make test-e2e              # Playwright spec'ы против тестового сервера
```

Если порт 21371 занят, override:
```bash
ORENDA_SERVER__PORT=21372 make test-e2e   # скрипт читает env, playwright config — фиксированный 21371
```

(В текущей версии порт 21371 зашит в `web/playwright.config.ts`; смена требует правки двух файлов. Это сознательно — `make test-e2e` не должен конфликтовать с usage-инстансом на 2137 или `make dev` на 2138 (Phase 28.20).)

## Бэклог (открыто)

- **«Полировка» (Phase 28.x)** — **полностью закрыта 2026-08-13** через Phase 28.7–28.18 (см. PLAN.md):
  - ✅ Hot-reload backup settings → Phase 28.9
  - ✅ Prettier + автоформат в pre-commit → Phase 28.7 + 28.12
  - ✅ CSP-tightening (style-src без unsafe-inline) → Phase 28.10
  - ✅ `docs/ARCHITECTURE.md` → Phase 28.11
  - ✅ README скриншоты → Phase 28.14 (отклонено embedding PNG; вместо — text pointer)
  - ✅ `rate_limit` секция в config.go + YAML → Phase 28.8
  - ✅ PHASE 26.A lint warnings cleanup → Phases 28.15 + 28.16 + 28.17 (closed 230 из 325 issues)
  - ✅ `handlers_backup.go` комментарии → обновлено в Phase 28.9
- **Phase 28.19 (agent type as free-form label set)** — **закрыта 2026-08-14** в `phase-28-19-agent-type-labels`. Чипы меток на канбан-карточке (`Agent: <name> (<labels>)`), серверный OR-фильтр `?type=a&type=b`, OpenAPI schema + route coverage зелёные.
- **Phase 28.20 (dev/dogfood separation)** — **закрыта 2026-08-14** в `phase-28-20-dev-dogfood`. dev-flow на 2138, usage/dogfood (= `~/opt/orenda` на `main`) на 2137, e2e на 21371. `install.sh` channel guard (refuse non-main + dirty, `--force` override), `update-dogfood.sh` одной командой, vite прокси следует env, startup log несёт `db_path` для observability. Документация: ARCHITECTURE.md §12.4, AGENTS.md, README + SESSION.md.
- **Phase 10 остаток:** VK Long Poll / Email HTML / Weekly digest — оформлены задачами **30.3–30.5** (P2). **Test send UI закрыт 2026-08-14** в `phase-10-test-send` (POST /api/v1/bots/test, dropdown + submit в Settings → Bots, console исключён, per-bot pre-check).
- **Phase 15 close-out (agent UX контракт)** — **закрыта 2026-08-14** в `phase-15-agent-context`. (1) `409 lock_taken` теперь несёт `holder_agent_id`/`holder_agent_name`/`claimed_at` (раньше `taskLockRepo.Holder` был написан, но не подключён); (2) `/tasks/:id/context` и `/agent/tasks/{id}/context` несут `blocked_by` (open dependency ids) + `lock_holder` (agent_id/agent_name/acquired_at); (3) `?ready=true` исключает задачи, занятые самим агентом (раньше шумели в очереди). 6 handler-тестов, OpenAPI оба файла синхронны.
- **Phase 28.21 (ops-hardening)** — **закрыта 2026-08-16** в `phase-28-21-ops-hardening`. Login rate-limit восстановлен, `configs/config.example.yaml` tracked (install.sh больше не падает на fresh clone), JWT-секрет генерируется в `$DATA_DIR/env` (mode 600), LWW-семантика Phase 8 зафиксирована документально.
- **Phase 29 (agent surfaces: wiki + course creation)** — **закрыта 2026-08-16** (29.1 в `phase-29-1-agent-wiki-rest`, 29.2–29.7 в `phase-29-2-7-agent-surfaces`). Wiki целиком в agent-неймспейс (REST+CLI+MCP+skill), `POST /agent/courses` (owner=FirstNonSystem, SkipGenerator), `POST /agent/courses/{id}/activate`. Сценарий «агент создаёт курс целиком, человек только учится» воспроизводится smoke-скриптом.
- **Phase 30 (реестр открытых задач)** — **создан 2026-08-16; реестр полностью закрыт 2026-08-17** (последняя — 30.13, `phase-30-13-course-curriculum-crud`). Правило: «долгов» как свободных записей больше нет; каждый открытый пункт — нумерованная задача 30.x с приоритетом (P1 процесс/данные, P2 продуктовые пробелы, P3 полировка/ops). Туда перенесены: Phase 10 подфазы (VK Long Poll / Email HTML / weekly digest → 30.3–30.5), `[[` autocomplete (→ 30.6), comment при reject (→ 30.7), due_at в календаре (→ 30.8), backup UX (→ 30.9), due в QuickCapture (→ 30.10), WIP-фидбек (→ 30.11), бейджи времени (→ 30.12), правка active-курса (→ 30.13), UI статусов + bulk-edit (→ 30.14), CI (→ 30.1), sync_ops молчание (→ 30.2), ops-скрипты (→ 30.15), lint-остаток 95 (→ 30.16). Проверено и НЕ заведено (закрыто в коде): color editor widget, events UI подписок, optimistic move, hot-reload backup, config template.
- **30.13 (правка active-курса, granular CRUD)** — **закрыта 2026-08-17**. Granular endpoints со стабильными ID (user + agent mirrors): `POST /courses/{id}/modules`, `PUT /courses/{id}/structure` (IDs-only reorder, exact coverage в tx), `PATCH/DELETE /modules/{id}`, `POST /modules/{id}/lessons`, `PATCH/DELETE /lessons/{id}`, `PATCH/DELETE /quizzes/{qid}`; done/archived курсы заморожены (422). Прогресс студента выживает по построению (строки не пересоздаются). Editor в active-режиме — granular diff-save + dnd reorder + импорт программы из markdown (client-side парсер `curriculumMarkdown.ts`). E2E `course-structure.spec.ts` доказывает: rename/add на active-курсе не сбрасывает `done`/`open` уроки.
- **Phase 31 (учебные напоминания в Today)** — **закрыта полностью 2026-08-17** (31.1–31.11). Все задачи [x]:
  - 31.1 migration `022_study_planning` (pace_notes_md, study_course_id, study_proposals).
  - 31.2 domain `study.Proposal` + sentinels; `Task.StudyCourseID`, `Course.PaceNotesMD`.
  - 31.3 storage: `study_proposal_repo` + новые колонки в tasks/courses; `docs/DB.md` обновлён.
  - 31.4 service `Propose/Accept (idempotent)/Dismiss` + WS-эмиты.
  - 31.5 agent REST (`POST /agent/study-proposals`, `GET /agent/courses?status=active` enriched, `PATCH /agent/courses/{id}`).
  - 31.6 user REST (`GET /api/v1/study-proposals`, `POST .../accept|dismiss`).
  - 31.7 Today handler — `proposals[]` в payload + study-reminders в due_today, исключены из overdue.
  - 31.8 MCP tools `orenda_courses_list`/`orenda_study_propose` + CLI parity.
  - 31.9 frontend TodayPage tray с Accept/Dismiss + 📖-маркер + pace_notes display.
  - 31.10 openapi schemas (`StudyProposal*`, `ActiveCourseProgress`, `TodayResponse`) + SKILL.md §4.5 «Plan my day».
  - 31.11 smoke DoD: `scripts/smoke_phase31.sh` end-to-end (tutor create+pace_notes+curriculum+materialize+activate → planner GET active → 2 proposals → user GET /today → accept (201, idempotent re-accept 200, dismiss 200) → missed-day semantic → `SMOKE OK`). **Production gap закрыт:** `cmd/orenda/main.go` теперь wire-ит `studySvc` (без этого каждый study endpoint возвращал 503). Миграция `022_study_planning` (pace_notes_md, study_course_id, study_proposals); domain `study.Proposal` с sentinels; storage `study_proposal_repo` + новые колонки в tasks/courses; service `Propose/Accept (idempotent)/Dismiss` с WS-эмитами в topic `tasks`; agent REST (`POST /agent/study-proposals`, `GET /agent/courses?status=active` enriched с progress + pace_notes, `PATCH /agent/courses/{id}`); user REST (`GET /api/v1/study-proposals`, `POST .../accept|dismiss`); today handler с `proposals[]` в payload + исключение study-reminders из overdue; MCP tools `orenda_courses_list`/`orenda_study_propose` + CLI parity (`orenda agent courses list --status active`, `orenda agent study-propose`); frontend TodayPage tray с Accept/Dismiss + 📖-маркер на study-reminders в due_today + pace_notes display в CourseDetailPage; openapi schemas (StudyProposalView/Full, TodayResponse, ActiveCourseProgress); SKILL.md §4.5 «Plan my day». **31.11 в работе:** smoke DoD скриптом.
- **Multi-user / multi-device sync (Phase 11+)** — следующая эра.

## Файлы

- `docs/PRD.md` — видение продукта
- `docs/PLAN.md` — фазы и задачи (открыто: **Phase 31 — постановка 2026-08-17** (учебные напоминания в Today), multi-user эра; **Phase 30 реестр полностью закрыт 2026-08-17** — все 17 задач; Phase 29 закрыта 2026-08-16; «Полировка» полностью закрыта Phase 28.7–28.18 + Phase 28.19 + Phase 15 close-out)
- `docs/CONTEXT.md` — концепции продукта (семантика домена; хартия в шапке файла)
- `docs/API.md` — REST reference
- `docs/DB.md` — схема БД по миграциям
- `docs/SESSION.md` — этот файл
- `docs/openapi.yaml` — OpenAPI 3.1 (source of truth; embedded copy `internal/api/openapi.yaml` синхронна)
- `docs/skills/orenda/SKILL.md` — workflow + etiquette для агентов
- `CHANGELOG.md` — версионная политика + записи по фазам
- `AGENTS.md` — правила для AI-агентов