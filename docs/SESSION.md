# Session Snapshot — 2026-08-13 (смержено: фазы 0–26 + Wave 4 + 27.1–27.5; открыт бэклог 27.6–27.10)

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

## Метаданные

- **Дата снапшота:** 2026-08-13
- **Ветка:** `dev`
- **Статус:** смержено: фазы 0–26 (частичные 🟡 расписаны в PLAN.md), Wave 4, 27.1–27.6. Открыты: 27.7–27.10 (см. «Бэклог» ниже).
- **Теги:** `v0.1.0-phase0` … `v0.1.0-wave4-minor` (после тега +10 коммитов документации + 27.6)

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

## Ключевые решения (не забыть)

- **Phase 14: subtasks → child tasks (Weeek-style).** Раньше `subtasks` и `checklists` дублировали друг друга — два API с одинаковым UI-смыслом. Теперь: subtasks стали полноценными задачами через `tasks.parent_task_id` (поле было в схеме с 001, но никем не заполнялось); checklists остались локальными чекбоксами. Миграция `013_subtasks_to_children.sql` переливает существующие subtasks в tasks и дропает таблицу. Activity log получил `task.child_added`, `task.checklist_added`, `task.checklist_item_added`, `task.checklist_item_done`. `GET /api/v1/tasks/:id/context` теперь возвращает и `children`, и `checklists` (раньше агенты видели только subtasks). Маркдаун-зеркало пишет checklists вместо subtasks.
- **Видение (согласовано 2026-08-11):** Orenda — персональная ОС делегирования: человек формулирует намерения, внешние AI-агенты исполняют через API как first-class участники workflow; задачи/календарь/wiki — общая память и контекст для человека и агентов.
- **Аудитория:** личный инструмент сейчас, публичный self-hosted продукт потом. Код и архитектуру делаем «на вырост» (миграции, docs, install-flow не ломаем), но фичи выбираем по собственному UX, а не по гипотетическим пользователям.
- **Phase 26: верификация фронтенда (решение 2026-08-11):** ранее зафиксированный пропуск E2E отменён. В план добавлена Phase 26 — два слоя: Playwright smoke против реального бинаря (только Chromium, tmp-БД, тестовый порт) + добивка vitest component-покрытия (13 feature-директорий без тестов). После фазы `make test` включает vitest; E2E — отдельный `make test-e2e`, локальный/предрелизный гейт (CI нет).
- **Worktree placement (2026-08-11):** AGENTS.md ужесточён — только вложенный `.worktrees/<task>`, sibling `../` запрещён.
- **Phase 19: review queue** — закрыл асимметрию делегирования: человек→агент работало давно (claim/submit), агент→человек ограничивалось одним notification, легко теряемым. Теперь `GET /api/v1/review-queue` отдаёт всё ожидающее решения (`awaiting='human' OR status='review'`) одним запросом с джойном проектов; сайдбар показывает live-бейдж с числом, `/review` — плоский список с inline Accept/Return. Return требует комментарий (агентам нужна обратная связь). WS auto-refresh на `/review` и на бейдже.
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
- **Цвет колонки (дефект 2026-08-13).** Бэкенд сохраняет корректно (в живой БД `done` = `#1463d2`), фронт сломан втройне: цвет нигде не рендерится (`ColumnView` без пропа color), `EditColumnModal` инициализирует цвет хардкодом `#94a3b8` (reopen показывает дефолт), submit всегда шлёт color → rename затирает выбор. Плюс `patchColumnHandler` не публикует WS `column.updated`. План — **Phase 27.10**.
- **SESSION.md: реновация + правило гигиены (2026-08-13).** По реплике разработчика «SESSION.md устарел» сверка с git log подтвердила: шапка 2026-08-12, «Метаданные» от 2026-08-08 («все 10 фаз завершены», теги до phase10 при реальном `v0.1.0-wave4-minor`), бэклог содержал 6 уже смерженных пунктов (27.1, 27.2, 27.3, restore, telegram, openapi), «Файлы» утверждали «все выполнены» при открытых 27.6–27.10, счётчики тестов (188/8) отставали от последних прогонов (199/10). **Правило:** при закрытии пункта бэклога его строка удаляется из SESSION.md «Бэклога» в том же коммите, что и статус в PLAN.md; шапка и «Метаданные» обновляются при каждой смене даты снапшота.
- **docs/CONTEXT.md (2026-08-13, с уточнением владельца).** Файл **доменного контекста**: общие ментальные модели (канбан, LMS, делегирование, inbox, auth, бэкап) — «что это такое в мире», без директив, номеров фаз и статусов. Цель: не изобретать заново то, что индустрия придумала (кейс-обоснование: отдельная ось статусов рядом с колонками). Не дублирует PLAN/API/SESSION; зарегистрирован в AGENTS.md «Key files to read first».
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

**Последние зафиксированные прогоны** (сессия 22.3+TG, 2026-08-12):
- `make test` — Go + vitest (199/199) — зелёные.
- `make test-e2e` — Playwright smoke против свежесобранного бинаря; 10/10 pass на чистой БД, без флейков. Требует `make build` (бинарь должен быть свежим); spawns test server on port 21371 (override `ORENDA_SERVER__PORT` чтобы не конфликтовать с dev-сервером на 2137).
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

(В текущей версии порт 21371 зашит в `web/playwright.config.ts`; смена требует правки двух файлов. Это сознательно — `make test-e2e` не должен конфликтовать с dev-сервером на 2137.)

## Бэклог (открыто)

- **Курсы: ручное наполнение (Phase 27.6)** — user-side curriculum editor + quiz surface: без агента курс сейчас ненаполняем (дефект зафиксирован 2026-08-13). 27.4 close-out (MaterializeLesson/AnswerQuiz/LessonPage) смержен.
- **Карточка задачи: редактируемые Status/Priority/Assignee (Phase 27.7)** — ✅ **закрыто в worktree `phase-27-7-task-fields`** — см. секцию выше.
- **Канбан: колонки = статусы (Phase 27.8)** — единая ось: DnD меняет статус, agent-flow двигает карточку (решение владельца 2026-08-13).
- **Known gaps (Phase 27.9)** — WS multi-topic fan-out, заголовки в /reports, WS/activity для course-задач, comment-debt (аудит отложенных швов 2026-08-13).
- **Цвет колонки: init/рендер/WS (Phase 27.10)** — сохранение в БД работает, но цвет невидим на доске и затирается при rename (дефект 2026-08-13).
- **Фаза «Полировка»** — хвосты Phase 9: backup_settings write path (PUT → 501), prettier/pprof/Prometheus, CSP-tightening.
- Multi-user / multi-device sync (Phase 11+)

## Файлы

- `docs/PRD.md` — видение продукта
- `docs/PLAN.md` — фазы и задачи (открытый бэклог: Phase 27.6–27.10)
- `docs/CONTEXT.md` — концепции продукта (семантика домена; хартия в шапке файла)
- `docs/API.md` — REST reference
- `docs/DB.md` — схема БД по миграциям
- `docs/SESSION.md` — этот файл
- `AGENTS.md` — правила для AI-агентов