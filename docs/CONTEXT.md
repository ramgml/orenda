# CONTEXT.md — концепции и механика продукта

> Долговечные знания о домене: что означают сущности и как работают потоки.
> Для людей и агентов. Читается после `SESSION.md`.
>
> **Хартия (обязательна к соблюдению):**
> 1. Здесь — семантика («что это», «как устроено»), не статус. Статус и бэклог → `PLAN.md`/`SESSION.md`; эндпоинты → `API.md`/`openapi.yaml`; процедуры для агентов → `skills/orenda/SKILL.md`; решения с датами → `SESSION.md`; правила → `AGENTS.md`.
> 2. Записывается только то, что смержено в `dev`. Планируемое помечается «запланировано: Phase X.Y» — или ждёт merge.
> 3. Фаза, меняющая концепцию, обновляет соответствующую секцию в том же PR (см. AGENTS.md «Definition of Done is binary»).
> 4. Секции короткие и самодостаточные; детали — по ссылкам на код и PLAN.

## Канбан: колонки визуализируют статусы

- Workflow-статусы задачи: `backlog → todo → in_progress → review → done` (`internal/domain/task`). Канонические пять обслуживает контур делегирования: claim агентом → `in_progress`, submit → `review`, approve → `done`.
- Колонка доски — визуализация статуса (модель Weeek, решение владельца 2026-08-13). У колонки machine key `status`; кастомная колонка = кастомный статус. **Запланировано: Phase 27.8** — до неё `status` и `column_id` независимы (DnD меняет только колонку, agent-flow — только статус).
- Инвариант (с 27.8): `task.status ≡ status колонки`; DnD, select в карточке и agent-flow синхронно двигают обе стороны. Inbox-задачи колонки не имеют; filing в проект кладёт задачу в колонку её статуса.
- WIP-лимиты и цвет — свойства колонки, не статуса. Дефолтные колонки нового проекта названы именами пяти канонических статусов (`DefaultColumns`).

## Курсы (LMS)

- Модель: `courses → course_modules → course_lessons → course_quizzes` (миграция 019). Курс — first-class сущность, не композиция «проект + wiki».
- Жизненный цикл курса: `draft → review → active → done` (+ `archived` из любого, `review → draft` на доработку). Урока: `locked → open → done` (односторонне).
- Два пути наполнения, сходящиеся в одном lifecycle: (1) агент-тьютор — создание курса порождает generator-задачу (`awaiting=agent`), тьютор claim'ит её и шлёт программу через `PUT /api/v1/agent/courses/{id}/curriculum` (атомарный swap); (2) вручную владельцем через user-side редактор — **запланировано: Phase 27.6**.
- Quiz: `exact` — мгновенная серверная проверка (нормализация: регистр/пробелы/диакритика); `open` — создаёт review-задачу тьютору с ответом в `context_md`. Квизы не блокируют завершение урока.
- Упражнение урока — обычная задача (`course_lessons.task_id`), проверка идёт штатным review-flow. Прогресс курса = уроки done/total; последний done-урок закрывает курс.

## Агенты и контур делегирования

- Агенты — внешние процессы: REST + long-poll (`/api/v1/agent/*`), Bearer API-токены, CLI `orenda agent`, MCP-прокси. Встроенный LLM-рантайм — опция, не цель.
- Контур: задача `awaiting=agent` → claim (атомарный, `task_locks.PK(task_id)`) → работа → submit (`status=review`, `awaiting=human`) → человек принимает (`done`) или возвращает (`in_progress`, `awaiting=agent`, комментарий обязателен).
- Review queue (`/review`, сайдбар-бейдж): всё, ожидающее решения человека — `awaiting='human' OR status='review'`.
- Зависимости: задача с незакрытыми блокерами не claim'ится (422 + `unfinished_blockers`); `GET /agent/tasks?ready=true` — листинг «готово к параллельной работе».
- Регистрация агента (`POST /api/v1/agents`) выдаёт plaintext-токен один раз; в БД — bcrypt hash. Heartbeat поддерживает online/offline.

## Inbox

- Inbox — не проект, а отсутствие проекта: `tasks.project_id IS NULL`. Сюда падают quick capture (глобальные `q` / `Cmd/Ctrl+K`), Telegram-сообщения боту, ручной ввод.
- Filing в проект: `PATCH /tasks/{id} {project_id}` — колонка назначается автоматически (с 27.8 — по статусу задачи).

## Auth: две модели

- UI: cookie JWT (`RequireUser`); login ставит HttpOnly-cookie `orenda_session`; WS-апгрейд читает ту же cookie (fallback: `Authorization: Bearer`, затем `?token=`).
- Агенты/CLI: Bearer API-токены (`RequireAgent`, namespace `/api/v1/agent/*`). User-cookie в agent namespace не работает и наоборот.

## Бэкап и restore

- Два контура: git push markdown-зеркала (Obsidian-compatible frontmatter, по расписанию) + sqlite-снапшоты (`VACUUM INTO`, ротация).
- Restore: CLI `orenda backup restore --from <path> --yes` — server-running guard → safety-copy → атомарный swap → `migrate up` → integrity checks; или из UI (Settings → Backups, через maintenance mode).
