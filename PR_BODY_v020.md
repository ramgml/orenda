# Release v0.2.0: промоушн dev → main

## Зачем этот PR

Закрывает **Phase 32.1** из `docs/PLAN.md`: второй pre-alpha релиз.
Первый (`v0.1.0`, 2026-08-16) зафиксировал ядро: 0–28.20 + Phase 10 test-send +
Phase 15 close-out. С тех пор в `dev` смержено ещё 30 фаз — agent surfaces
(wiki + course creation через REST/MCP/CLI), реестр-30 (17 задач: CI,
ops-hardening, WIP-feedback, time-badges, bulk-edit, curriculum CRUD), и вся
Phase 31 (study planning — agent-driven предложения на Today). Размер diff
между main и dev — 367 коммитов, как и на `v0.1.0`.

## Что меняется

**CHANGELOG.md** — секция `[0.2.0] — 2026-08-17` с полным sweep'ом фаз
28.21–31.11 (Added / Changed / Fixed / Security / Docs / Known gaps);
`[Unreleased]` пустая.

**VERSION** — `0.1.0 → 0.2.0`.

Содержательно — никаких новых фич, только релизная мета (CHANGELOG/VERSION).
Скоуп фаз уже смержен в `dev`, поэтому после merge CI на main прогонит то
же самое, что прогонялось на каждой фазе при мерже в `dev`.

## Definition of Done — evidence

| Пункт DoD | Evidence |
|---|---|
| CHANGELOG содержит запись по каждой фазе 28.21–31.11 | `grep -oE "Phase (29\.[1-7]\|30\.[0-9]+\|31\.[0-9]+)\b" CHANGELOG.md \| sort -u` → полный список 29.1–29.7 + 30.1–30.17 + 31.1–31.11 (покрыто в секции 0.2.0) |
| [Unreleased] пустая | `awk '/^## \[Unreleased\]/{flag=1; next} /^## \[/{flag=0} flag' CHANGELOG.md` → пустой вывод |
| VERSION bumped | `cat VERSION` → `0.2.0` |
| Все гейты зелёные | см. ниже |

## Гейты (verified 2026-08-17 на worktree phase-32-1-release-v020 @ 65a48ba)

- ✅ `make test` — Go (30 packages ok) + vitest **314/314**
- ✅ `make build` — `bin/orenda` OK; `git describe`-stamp пока `v0.1.0-wave4-minor-…-dirty` (после tag'а на main станет `v0.2.0`)
- ✅ `make test-e2e` — **20/20** Playwright pass
- ✅ `npx tsc --noEmit` — clean
- ✅ `golangci-lint run --new-from-merge-base=dev ./...` — **0 issues** (new code чист; pre-existing 95 — Phase 30.16)
- ✅ `make lint` (web) — eslint + prettier clean

## Не сделано (явно)

- Нет — DoD покрыт целиком.

## Процедура merge (для оператора)

Push и merge **блокируются явной командой пользователя** (правило
репозитория: «Не пушить в remote без explicit user request»). После
подтверждения:

```bash
# 1. push ветки и открыть PR (с телом этого файла):
git push -u origin phase-32-1-release-v020
gh pr create --base main --head phase-32-1-release-v020 \
  --title "Release v0.2.0: промоушн dev → main (367 commits past v0.1.0)" \
  --body-file .worktrees/phase-32-1-release-v020/PR_BODY_v020.md

# 2. после зелёного CI на PR:
gh pr merge --squash --delete-branch  # один squash-commit на main

# 3. tag на main:
git checkout main && git pull
git tag -a v0.2.0 -m "v0.2.0 — pre-alpha: agent surfaces + study planning + реестр 30"
git push origin v0.2.0

# 4. worktree cleanup:
git worktree remove .worktrees/phase-32-1-release-v020
git worktree prune

# 5. update-dogfood (Phase 32.2; см. PLAN):
~/opt/orenda/scripts/update-dogfood.sh
# → guard main+clean сработает, новый бэкап + restart
# → /api/v1/info → version=v0.2.0
```

## Что дальше

Phase 32.2 (dogfood update) идёт **после** этого PR. План-секция Phase 32
остаётся — 32.4 уже смержена (DOGFOOD.md конвенция), 32.5/32.6 следующие.
