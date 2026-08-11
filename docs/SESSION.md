# Session Snapshot — 2026-08-08 (финал)

> Файл для восстановления контекста сессии. Читай первым делом при возобновлении работы.
> Подхватывается автоматически через AGENTS.md и через `instructions` в opencode.json.

## Метаданные

- **Дата:** 2026-08-08 (вечер)
- **Ветка:** `dev`
- **Статус:** **все 10 фаз завершены** и запушены в `git@github.com:ramgml/orenda.git`
- **Теги:** `v0.1.0-phase0` … `v0.1.0-phase10`

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

## Ключевые решения (не забыть)

- **Phase 14: subtasks → child tasks (Weeek-style).** Раньше `subtasks` и `checklists` дублировали друг друга — два API с одинаковым UI-смыслом. Теперь: subtasks стали полноценными задачами через `tasks.parent_task_id` (поле было в схеме с 001, но никем не заполнялось); checklists остались локальными чекбоксами. Миграция `013_subtasks_to_children.sql` переливает существующие subtasks в tasks и дропает таблицу. Activity log получил `task.child_added`, `task.checklist_added`, `task.checklist_item_added`, `task.checklist_item_done`. `GET /api/v1/tasks/:id/context` теперь возвращает и `children`, и `checklists` (раньше агенты видели только subtasks). Маркдаун-зеркало пишет checklists вместо subtasks.
- **Видение (согласовано 2026-08-11):** Orenda — персональная ОС делегирования: человек формулирует намерения, внешние AI-агенты исполняют через API как first-class участники workflow; задачи/календарь/wiki — общая память и контекст для человека и агентов.
- **Аудитория:** личный инструмент сейчас, публичный self-hosted продукт потом. Код и архитектуру делаем «на вырост» (миграции, docs, install-flow не ломаем), но фичи выбираем по собственному UX, а не по гипотетическим пользователям.
- **Phase 19: review queue** — закрыл асимметрию делегирования: человек→агент работало давно (claim/submit), агент→человек ограничивалось одним notification, легко теряемым. Теперь `GET /api/v1/review-queue` отдаёт всё ожидающее решения (`awaiting='human' OR status='review'`) одним запросом с джойном проектов; сайдбар показывает live-бейдж с числом, `/review` — плоский список с inline Accept/Return. Return требует комментарий (агентам нужна обратная связь). WS auto-refresh на `/review` и на бейдже.
- **Phase 15: зависимости задач + ready-листинг для агентов** — задачи могут блокировать друг друга через новую таблицу `task_dependencies` (миграция 016). `PUT /tasks/{id}/dependencies` replace-семантика, цикл/self-loop → 422. `Service.Claim` отказывается брать заблокированную задачу → 422 с `unfinished_blockers: [id,...]`. `GET /api/v1/agent/tasks?ready=true` — листинг для агентов с фильтром «готово к параллельной работе» (блокеры done + лок свободен + не in_progress + не assigned to other agent). Cycle prevention — DFS на service-слое до записи. WS-событие `task.deps_changed`.
- **Phase 22: restore-from-snapshot** — закрыл бэкап-контур Phase 7: снепшот без проверенного restore — не бэкап. CLI `orenda backup restore --from <path> --yes` теперь делает полный pipeline: server-running guard → safety-copy в `<dest>.pre-restore-<unix-ts>` (операторский escape hatch) → атомарный swap → `migrate up` до текущей схемы → `PRAGMA integrity_check` + `PRAGMA foreign_key_check`. Restore переименован в старую логику; новая `runBackupRestoreWithVerify` — точка входа. UI-кнопка в Settings → Backups уже подсказывает CLI hint (HTTP-эндпоинт отдаёт `hint` потому что сервер бежит).
- **Phase 17: rich task cards** — лицевая сторона карточки отвечает на 80% вопросов без открытия задачи. Бэк: `ListByProjectWithStats` — один listByProject + 4 агрегатных запроса (comments / attachments / children / checklist_items) + open-blockers count (Phase 15). Фронт: `TaskCard` декомпозирован на `PriorityBorder` (urgent=red, high=orange, low=slate), `TaskDueBadge` (overdue/today/upcoming/done), `AssigneeChip` (agent=🤖, user=инициалы), `AwaitingBadge`, `BlockedBadge`, `Counters`. Плотность: localStorage-флаг `orenda.kanban.cardDensity` (compact/detailed). Чистые функции `taskCardBadges.ts` — unit-тестируемые отдельно. **Hotfix migration 015**: обнаружено, что migration 015 оставила `checklist_items.checklist_id` FK с reference на удалённую `checklists_old` — `INSERT INTO checklist_items` падал с `no such table: main.checklists_old`. Migration 017 пересоздаёт таблицу с правильным FK и сохраняет данные. Чек-лист-фича фактически была сломана с момента шеринга Phase 16; никто не замечал, потому что никто не вставлял checklist items в проде.
- **Phase 21: quick capture в Inbox** — захват мысли ≤ 1 хоткей из любого экрана: глобальная модалка-палитра через React Portal, открывается `q` или `Cmd/Ctrl+K` (с авто-фокусом на textarea), `Cmd/Ctrl+Enter` сабмитит, Esc закрывает. Кнопка `+` в правом нижнем углу (fixed, всегда видна) открывает ту же модалку. Submit → Inbox (Phase 16 endpoint), затем toast с кнопкой «Open task» (навигирует на `/tasks/:id`) либо Dismiss.
- **Phase 20: экран «Сегодня» (daily driver)** — домашняя страница отвечает «что у меня сегодня»: просроченное (красная секция), due сегодня (янтарная), запланированное по времени (scheduled_today, из календарных задач), ожидающие меня (ссылка на Phase 19 /review). Один round-trip: GET /api/v1/today возвращает overdue/due_today/scheduled_today/awaiting_count одной выдачей; фронт ре-фетчит по WS topic 'tasks'. Активный таймер в payload пока не отдаётся (Phase 4 single-active timer API не экспонирует lookup — TODO для следующего захода). Старый Dashboard с stats-карточками снят: та же информация теперь живёт на TodayPage + Reports.
- **Phase 24: OpenAPI + наблюдаемость** — машиночитаемый контракт для внешних агентов (генерация клиентов) + минимальная наблюдаемость self-hosted инстанса.
- **Phase 18: личные курсы, создаваемые ИИ-агентами (LMS-модель)** — courses → course_modules → course_lessons → course_quizzes (свои таблицы, не композиция «проект + wiki»). Миграция 019_courses.sql. Оркестрация через задачи (course.GeneratorTaskID — placeholder, future enhancement). Сейчас MVP: createWithIntent, submitCurriculum, approveCurriculum, requestChanges, completeLesson. Agent-side: listAgentCourses, putAgentCurriculum (atomic swap). MaterializeLesson и AnswerQuiz — отдельный набор фич, на следующий заход.
- **Phase 25: agent DX — CLI + SKILL** — `orenda agent` cobra-сабкоманды (`me`, `next`, `context`, `claim`, `release`, `submit`, `comment`, `await`) поверх уже существующего REST API. Конфиг: флаги > env > `~/.config/orenda/agent.yaml`. Exit code 2 = "no work" для shell-циклов. Документ `docs/skills/orenda/SKILL.md` — полный workflow + этикет + reference. **MCP server опущен** (Phase 25 плана предполагает новый SDK modelcontextprotocol — это километр работы; CLI + SKILL покрывают 80% случаев, MCP остаётся на следующую фазу).
- **Модель агентов:** внешние, через REST/long-poll (`/api/v1/agent/*`, claim/submit/review). Встроенный LLM-рантайм — опция, не цель; домен не должен жёстко зашивать предположение «агент всегда снаружи».
- **Горизонт:** dogfooding и стабильность. Критерий ценности задачи — «использую каждый день без трения». Приоритетные пробелы: restore-from-snapshot (снапшоты есть, восстановления нет), UX-трение ежедневных сценариев, наблюдаемость. Multi-user и интеграции календарей — за скобкой, пока ядро не «живётся».
- **Миграции аддитивны** — 001_init.sql содержит всю схему; 002+ добавляют только индексы/триггеры. Исключение: 007 пересоздаёт `time_entries` чтобы убрать FK на agent_id.
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

`go test ./...` — 27 пакетов, всё зелёное. Coverage: domain 100%, services 70–100%, api 61%, storage 72%.

## Что можно дальше (за рамками PLAN)

- Restore-from-snapshot flow (CLI/UI) — snapshot есть, restore — заглушка
- Telegram onboarding (chat_id через /start)
- OpenAPI генерация
- Playwright E2E (осознанно пропущено по решению пользователя)
- Multi-user / multi-device sync (Phase 11+)

## Файлы

- `docs/PRD.md` — видение продукта
- `docs/PLAN.md` — фазы и задачи (все выполнены)
- `docs/API.md` — REST reference
- `docs/DB.md` — схема БД по миграциям
- `docs/SESSION.md` — этот файл
- `AGENTS.md` — правила для AI-агентов