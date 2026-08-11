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