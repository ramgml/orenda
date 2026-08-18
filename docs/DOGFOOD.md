# DOGFOOD.md — управление проектом внутри Orenda

> Конвенция Phase 32 (2026-08-17). Проект Orenda управляет сам собой:
> исполняемый бэклог, постановки и decision log живут в dogfood-инстансе
> (`http://127.0.0.1:2137`, проект «Orenda dev»), git — только для кода и код-ревью.
>
> Этот файл — входная точка агента: **откуда брать работу и куда писать результаты**.
> Правила кода — в `AGENTS.md`; концепции домена — в `docs/CONTEXT.md`;
> процедуры agent API — в `docs/skills/orenda/SKILL.md`.

## Таблица переноса (было → стало)

| Было (git) | Стало (Orenda) |
|---|---|
| Задачи реестра / фаз | Задачи в проекте «Orenda dev»; claim — нативный `POST /agent/tasks/{id}/claim` (lock + 409 с holder вместо гонок по маркерам `[~]` в git) |
| Постановки фаз (дизайн-решения, evidence, DoD) | Wiki-страницы (`/pages`), ссылка из описания задачи; агенты читают/пишут через MCP (Phase 29) |
| SESSION.md (восстановление контекста) | `/agent/tasks/{id}/context` для задачного контекста; нарратив решений — wiki-страница «Decision log» |
| PLAN.md / SESSION.md | Замороженный архив с пометкой; история не переносится |
| AGENTS.md + opencode.json `instructions` | Новая входная точка агента: `orenda agent next` / MCP `orenda_list_tasks`, а не SESSION.md |

## Workflow задачи

1. **Постановка** — wiki-страница в инстансе: мотивация, дизайн-решения, evidence, DoD. Агенты пишут через MCP (`orenda_pages_save`) или REST `PUT /api/v1/agent/pages/{slug}`.
2. **Задача** — в проекте «Orenda dev», в описании ссылка на постановку. Критерий готовности — в самой задаче (CONTEXT.md: «задача без критерия тлеет»).
3. **Claim** — `orenda agent next` (готовая к работе задача) → `orenda agent claim <id>`. 409 `lock_taken` несёт holder-поля — спроси holder'а или возьми следующую.
4. **Работа** — код в git по правилам `AGENTS.md` (worktree per task, тесты, минимальные диффы). Контекст задачи: `orenda agent context <id>` — блокеры, комментарии, дети, lock holder.
5. **PR** — по шаблону `.github/PULL_REQUEST_TEMPLATE.md`; merge в `dev` после ревью.
6. **Review в Orenda** — `orenda agent submit <id>` → человек принимает/возвращает на `/review`. Возврат всегда с комментарием — это обратная связь, а не сбой.
7. **Принятые решения** — короткая запись на wiki-странице «Decision log» (что решили и почему, одна запись на решение).

## Правило новой работы

**Новая работа = задача в инстансе, не строка в PLAN.md.** Это правило заменяет
реестр Phase 30 («каждый открытый пункт — нумерованная задача 30.x») и правило
гигиены SESSION.md. Открыл gap при закрытии задачи → заведи новую задачу в
инстансе (постановка в wiki, если решение нетривиально). PLAN.md и SESSION.md
замораживаются архивом в Phase 32.6 — туда больше не пишем.

**Исполнимо агентом (Phase 33.1).** Агент заводит задачу сам —
`orenda agent propose --project <id> --title ... --description-file ...`
(REST `POST /api/v1/agent/tasks`, MCP `orenda_task_propose`). Задача падает в
`status=backlog, awaiting=human` и видна владельцу в review queue; принятие =
kanban-move в `todo` (сбрасывает `awaiting`, задача появляется в
`GET /api/v1/agent/tasks?ready=true`), отклонение = delete. Агент НЕ начинает
работу над предложенной задачей до триажа человеком.

## Входная точка агента

Начало сессии:

```bash
orenda agent me          # кто я, жив ли токен
orenda agent next        # первая готовая задача (exit 2 = работы нет)
orenda agent context <id>
```

или через MCP: `orenda_list_tasks` → `orenda_claim` → `orenda_context`.

SESSION.md больше не читается при старте — снапшот-сессии заменён контекстом
задачи. PLAN.md остаётся справочником по фазам ≤ 32 (постановки, аудиты) до
его заморозки.

## Статус перехода

Конвенция активируется полностью после Phase 32.1 (релиз v0.2.0) и 32.2
(обновление dogfood-инстанса): текущий dogfood-бинарь старше `dev` и не имеет
agent wiki-surfaces (Phase 29) и study-planning (Phase 31). До обновления
инстанса постановки пишутся в PLAN.md как раньше, а этот файл описывает
целевое состояние. Проверить готовность инстанса:

```bash
curl -s http://127.0.0.1:2137/api/v1/info   # version ≥ v0.2.0
```
