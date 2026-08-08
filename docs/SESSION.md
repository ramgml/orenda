# Session Snapshot — 2026-08-08

> Файл для восстановления контекста сессии. Читай первым делом при возобновлении работы.
> Подхватывается автоматически через AGENTS.md и через `instructions` в opencode.json.

## Метаданные

- **Дата:** 2026-08-08
- **Ветка:** `dev` (на ней работаем)
- **Версия:** `0.1.0` (тег `v0.1.0` на `main`)
- **Remote:** `git@github.com:ramgml/orenda.git`
- **Последний коммит:** `b97ba07` — fix(opencode): remove invalid keys

## Что обсуждалось в сессии

1. **Идея проекта** — локальный аналог Weeek с AI-агентами как полноправными пользователями.
2. **Имя** — выбрано **Orenda** (Оренда, ирокез. «внутренняя сила»). Означает: всё, что пронизывает жизнь человека (работа + личное).
3. **Порт** — `2137`.
4. **Стек** — Go 1.22+ (chi, modernc.org/sqlite, jwt, gorilla/websocket, cobra) + React 18 + TS + Vite + Tailwind + shadcn/ui.
5. **Хранилище** — SQLite (WAL, FTS5).
6. **Бэкапы** — markdown-зеркало в git + sqlite snapshots; remote настраивается пользователем в UI.
7. **Боты** — pluggable интерфейс, реализации (VK, Telegram, Email, Webhook, Console) в Фазе 10.
8. **Специфика** — мобильный интернет в Ижевске, белые списки, Telegram/VPN недоступны → VK критичен.
9. **Workflow** — агенты через REST API, человек через web UI; задача = общий контекст (комментарии, файлы, упоминания).
10. **Версионирование** — SemVer, ветки `main`/`dev`/`phase-*`, теги `vX.Y.Z` и `vX.Y.Z-phaseN`.

## Скоуп согласованной документации

- `docs/PRD.md` — полный PRD: vision, scope, пользователи, user stories, фичи, NFR, стек, безопасность, глоссарий.
- `docs/PLAN.md` — детальный план по фазам 0–10 с задачами, критериями готовности и DB-схемой.
- `AGENTS.md` — guide для AI-агентов: правила кода, git workflow, conventions.
- `CHANGELOG.md` — формат Keep a Changelog + политика версионирования.
- `data/config.example.yaml` — шаблон конфига.

## Текущее состояние репозитория

### Файлы (на диске)

```
orenda/
├── AGENTS.md                  ← rules для AI
├── CHANGELOG.md               ← версии
├── Makefile                   ← dev/build/test/lint/backup
├── README.md                  ← quickstart
├── VERSION                    ← 0.1.0
├── .gitignore                 ← +opencode runtime files
├── go.mod                     ← module github.com/ramgml/orenda
├── data/
│   └── config.example.yaml
├── docs/
│   ├── PRD.md
│   ├── PLAN.md
│   └── SESSION.md             ← этот файл
└── opencode.json              ← build/plan agents, tools, formatter
```

### Git

```
* dev  ← b97ba07 (HEAD)
  main ← 637b586 (tag: v0.1.0)
```

### Что НЕ создано (по решению пользователя — отложено)

- ❌ Go-код (откатил по просьбе пользователя)
- ❌ Миграции БД
- ❌ Vite-скелет в `web/`
- ❌ Структуры `cmd/`, `internal/`, `scripts/`

## Решения, которые нельзя забыть

- **Перед написанием любого Go-кода** — согласовать структуру пакета и интерфейсы.
- **Перед миграцией БД** — согласовать полную DDL.
- **README и AGENTS.md** — обновлять по мере роста проекта.
- **Версия** — менять `VERSION` и добавлять запись в `CHANGELOG.md` при релизе на `main`.
- **opencode.json** — не использовать нестандартные ключи (проверять по схеме https://opencode.ai/config.json).

## Следующие шаги (по плану)

**Phase 0 — Инициализация** (ещё не завершена):

- [ ] **0.1** — ✅ `go.mod` создан
- [ ] **0.2** — ⏳ `cmd/orenda/main.go` с cobra (нужно согласовать с пользователем)
- [ ] **0.3** — ✅ `Makefile`
- [ ] **0.4** — ⏳ Vite + React + TS скелет в `web/` (нужно согласовать)
- [ ] **0.5** — ⏳ миграция `001_init.sql` (DDL уже в PLAN.md, нужно согласовать)
- [ ] **0.6** — ⏳ `internal/config/config.go`
- [ ] **0.7** — ⏳ `internal/storage/sqlite/db.go`
- [ ] **0.8** — ⏳ chi router + middleware
- [ ] **0.9** — ⏳ `/healthz` endpoint
- [ ] **0.10** — ⏳ `/api/v1/info` endpoint
- [ ] **0.11** — ⏳ `embed.FS` для статики
- [ ] **0.12** — ⏳ wildcard static handler
- [ ] **0.13** — ✅ `.gitignore`
- [ ] **0.14** — ⏳ `.editorconfig`, `.golangci.yml`, `.eslintrc`
- [ ] **0.15** — ✅ `README.md`
- [ ] **0.16** — ✅ `data/config.example.yaml`

## Открытые вопросы

1. **Phase 0.2 — структура `cmd/orenda/main.go`**: какие команды нужны на старте? Минимум `serve`, `version`. `migrate`, `backup`, `user` — обсуждать.
2. **Phase 0.4 — Vite scaffold**: какие плагины сразу? Минимум react-ts + tailwind. `shadcn/ui` подключать?
3. **Phase 0.5 — миграция 001_init.sql**: использовать DDL из PLAN.md как есть или сначала обсудить?
4. **Phase 0.7 — SQLite driver**: `modernc.org/sqlite` (pure Go, без CGO) — ок?

## Команды для быстрого старта следующей сессии

```bash
cd /work/projects/orenda
git log --oneline -5
git branch -a
cat docs/SESSION.md      # ← прочитать первым
cat docs/PLAN.md | head  # ← затем план
cat AGENTS.md            # ← затем правила
```

## Параметры opencode (для справки)

- Модель: `minimax/MiniMax-M3`
- Агенты: `build`, `plan`
- Tools: bash, edit, write
- Formatter: встроенный

## Известные ограничения opencode config

- Нет ключа `linter` (для линтинга используется `make lint`)
- Нет ключа `theme` (тема задаётся в `tui.json`, который runtime-генерируется)
- `tools` принимает только boolean, не объект
- `formatter` принимает boolean или объект со спецструктурой