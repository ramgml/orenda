# RELEASE.md — релизный процесс Orenda

> Живая копия — wiki:release-process в dogfood-инстансе (конвенция `docs/DOGFOOD.md`). Этот файл — снапшот процесса в репо; при расхождении правит wiki, а расхождение устраняется при ближайшем релизе.
>
> **Снапшот обновлён 2026-08-22 по wiki (v0.6.0).**

## Модель веток и тегов

- `main` — только стабильные релизы; прямые коммиты запрещены; теги `vX.Y.Z` ставятся только здесь.
- `dev` — вся разработка; фазовые майлстоуны опционально `vX.Y.Z-phaseN` (по факту почти не используется).
- `phase-X-Y-<name>` / `task-N-<name>` — рабочие ветки от `dev`; одна задача = одна ветка = один worktree.
- Semver pre-1.0: `0.MINOR.PATCH`; совместимость между минорами не гарантируется.
- Источник истины по версии — файл `VERSION` в корне; Makefile берёт версию бинаря через `git describe`, поэтому релизный тег на `main` — то, что увидит бинарь.

## Пошагово

### 1. Подготовка релиза (worktree от `dev`)

1. Свернуть `## [Unreleased]` в `CHANGELOG.md` в `## [X.Y.Z] — YYYY-MM-DD` (Keep a Changelog: Added / Changed / Fixed / Security / Docs / Known gaps; образец — секции 0.2.0 и 0.3.0).
2. Бампнуть `VERSION`.
3. Локальные гейты (с базой main):
   - `make lint-new BASE_REF=origin/main`
   - `make test-full` (uncached — релизный контракт; `make test` кэшированный и для релиза ничего не доказывает — task 40)
4. Поздние фиксы из `dev` влить в релизную подготовку до PR (прецедент: merge-коммит `9c3d06b` в v0.3.0).

### 2. Промоушн `dev` → `main` через PR

- PR с названием `Release vX.Y.Z: промоушн dev → main` (прецеденты: PR #4 → v0.2.0, PR #10 → v0.3.0, PR #55 → v0.5.0, PR #64 → v0.6.0).
- Это единственная точка полного CI: **release gate** на PR/push в `main` и теги `v*` (lint new-vs-main → test → build → e2e, `.github/workflows/ci.yml`). PR-to-dev CI молчит — by design, per-PR гейт живёт в локальных хуках (wiki:ci-local-gates-hooks, `make hooks`).
- Опыт v0.5.0/v0.6.0: build и e2e джобы существуют только здесь — именно они ловят tsc/e2e-регрессии, невидимые на dev (#43, #45, #54). До закрытия #44 жди красного build/e2e и чини отдельными urgent-задачами.
- Merge в `main` — по явной команде владельца.

### 3. Тег и обратная синхронизация

```bash
git tag vX.Y.Z <merge-commit-on-main>
git push origin main vX.Y.Z   # по команде владельца
```

- Тег `v*` повторно запускает release gate.
- После релиза — merge `main` обратно в `dev` (прецедент: `3e33309` после v0.2.0), иначе расходятся `VERSION`/`CHANGELOG.md`.

### 4. GitHub Release

Release gate на тегах (`ci.yml`) прогоняет только lint/test/build/e2e — **GitHub Release он не создаёт**, шаг ручной. Без него тег есть, а на странице Releases версия отсутствует (инцидент v0.4.0, замечен 2026-08-20).

```bash
# body = секция [X.Y.Z] из CHANGELOG.md (от `## [X.Y.Z]` до следующего `## [`);
# границы секций: grep -n '^## \[' CHANGELOG.md
gh release create vX.Y.Z \
  --title "Orenda vX.Y.Z — <краткий фокус>" \
  --notes-file <section.md> --latest
```

Проверка: `gh release list` показывает новый тег как `Latest`, в body — только своя секция changelog (без заголовка следующей).

### 5. Обновление dogfood-инстанса

```bash
cd ~/opt/orenda && scripts/update-dogfood.sh
```

Скрипт требует `main` + чистое дерево → `git pull --ff-only origin main` → `scripts/install.sh --systemd` → restart user-unit. Channel guard в `install.sh` отказывается ставить не-main/грязную сборку без `--force` (Phase 28.20: usage-канал никогда не видит unreleased-код случайно).

Проверка после рестарта: `curl -s http://127.0.0.1:2137/api/v1/info` — поле `version` должно быть ровно `vX.Y.Z`; если вида `vX.Y.Z-N-g<sha>` — в клоне ~/opt/orenda не было свежих тегов на момент сборки: `git fetch origin --tags` и перезапустить update (инцидент v0.5.0).

## Запреты

- Не тегать релизные `vX.Y.Z` на `dev` — релизные теги живут только на `main`.
- Не мержить в `main` без PR — теряется release gate.
- Не считать релиз выпущенным без GitHub Release (шаг 4) — тег + зелёный гейт ≠ опубликованный релиз.
- Не выпускать с красным гейтом. Full-lint на `main` приведён к new-vs-main (`6c75411`): перманентно красный гейт — не гейт; долг Phase 30.16 закрывается фоном, релизы не блокирует.
- Не обходить хуки `--no-verify`; исключение — `SKIP_ORENDA_HOOKS=1` с фиксацией в PR.

## Связанные документы

- wiki:release-process — живая версия этого процесса.
- wiki:ci-local-gates-hooks — локальные per-PR гейты (pre-commit / pre-push).
- `CHANGELOG.md` — versioning policy + история релизов.
- `.github/workflows/ci.yml` — release gate.
- `scripts/update-dogfood.sh`, `scripts/install.sh` — выкатка в usage-канал.
