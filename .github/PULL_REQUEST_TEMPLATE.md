<!--
  Контракт сдачи работы — см. AGENTS.md «Definition of Done is binary».
  Задача либо сделана целиком, либо PR — DRAFT с явным списком пробелов.
  «Реализовано» ≠ проверено: у каждого пункта DoD должна быть evidence-строка.
-->

## Scope

- Фаза/задача: `phase X.Y` — <название> (refs `docs/PLAN.md`)
- Ветка: `phase-X-Y-<name>` ← `dev`

## Definition of Done — evidence по каждому пункту

<!-- Скопируй каждый пункт DoD из PLAN.md и приложи выполненную проверку:
     команда + результат (тест, smoke, вывод). Пункт без evidence = PR не готов. -->

| Пункт DoD | Evidence (команда → результат) |
|---|---|
|  |  |

## Не сделано (обязательна для частичной сдачи)

<!-- Тихое сокращение скоупа запрещено. Перечисли каждый недостающий
     пункт и причину. Секция не «Нет» → PR помечается DRAFT. -->

- Нет — DoD покрыт целиком.

## Проверки

- [ ] `make lint-new` чист (0 новых issues; pre-existing долг — не считается, см. Phase 30.16)
- [ ] `pre-push` прошёл (`make lint-new` + `make test`) — без `--no-verify`
- [ ] `pre-commit` прошёл (`gofmt -l` + `prettier --check`) — без `--no-verify`
- [ ] `make test` зелёный (Go + vitest)
- [ ] `make test-e2e` зелёный (если затронут UI-flow)
- [ ] Чекбоксы в `docs/PLAN.md` проставлены `[x]` только при полном DoD
- [ ] Нет заглушек / `TODO: implement` / мёртвых endpoints; отложенные швы записаны в `docs/PLAN.md` как known gap
- [ ] Knowledge graph переиндексирован (`index_repository`, mode `fast`)
- [ ] Shipped-миграции не редактировались; новые — только аддитивными файлами
