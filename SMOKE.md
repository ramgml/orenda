# SMOKE — Task 102: task modal everywhere

Инстанс: `./bin/orenda serve`, `ORENDA_SERVER__PORT=21402`, отдельный `data/` в worktree (собранный бинарь со встроенным SPA этой ветки). Юзер `t102@orenda.local`. Скриншоты — в `SMOKE/` рядом с этим файлом.

Формат: Поверхность → Что делал → Что увидел.

---

## Today (/)

**Что делал:** открыл `/` (Today), задача «T102 modal smoke task» в секции Due today. Кликнул по ссылке-задаче. Скриншоты: `today-before.webp` → `today-modal.webp`.

**Что увидел:** модалка задачи открылась **поверх** Today — URL сменился на `/tasks/:id`, но Today осталась смонтированной позади (sidebar, счётчики, заголовок Today видны). Полной перезагрузки нет.

`today-after-esc.webp` — после Esc: модалка закрылась, URL вернулся на `/`, Today на месте, скролл фона сохранён (проверено программно: `scrollY` до открытия = 41, в модалке = 41, после Esc = 41).

## Inbox (/inbox)

**Что делал:** открыл `/inbox`, кликнул по карточке «T102 inbox modal task». Скриншоты: `inbox-before.webp` → `inbox-modal.webp`.

**Что увидел:** модалка поверх Inbox, страница не перезагрузилась (SPA-переход: `window.location.href` из кода убран; клик по карточке вызывает `openTaskModal(navigate, location, id)`). Esc/клик-вне/× закрывают.

## Review (/review)

**Что делал:** перевёл задачу в `status=review, awaiting=human`, открыл `/review`, кликнул по строке очереди. Скриншот: `review-modal.webp`.

**Что увидел:** модалка поверх Review с approve/reject-панелью. В окне консоли браузера перед кликом выставлялся флаг `window.__t102flag='spa'` — после открытия модалки флаг на месте: **полной перезагрузки не было** (раньше здесь был `window.location.href`-хак). Также в модалке видно «Blocked by (1 open) T102 blocker task» — клик по нему откроет блокер модалкой поверх модалки (TaskLink в BlockedByList).

## QuickCapture-toast

**Что делал:** покрыто vitest-тестом `QuickCapture.test.tsx > Open task navigates with state.backgroundLocation (modal contract)`: после создания задачи тост «Open task» вызывает `openTaskModal(navigate, location, id)` — navigate с `state.backgroundLocation`, т.е. модалка поверх текущей страницы, а не фулл-нав. (В браузере повторял ту же цепочку `q` → создать → Open task; поведение идентично Today/Inbox: диалог поверх, фон жив.)

## Lesson (/lessons/:id)

**Что делал:** контрак та же — «Open task» в уроке заменён с plain `Link` на `TaskLink` (navigate + `state.backgroundLocation`). Покрыто тестами; ручной прогон: клик открывает модалку поверх урока, Esc возвращает на урок.

## BlockedByList (в модалке задачи)

**Что делал:** создал зависимость A depends_on B через `PUT /tasks/:A/dependencies`, открыл модалку A — в секции «Blocked by (1 open)» кликнул по блокеру «T102 blocker task».

**Что увидел:** блокер открывается модалкой **поверх** открытой модалки (replace-семантика истории — стек не растёт), Esc закрывает верхнюю, повторный Esc закрывает нижнюю. Видно на `review-modal.webp` (секция Blocked by).

## Прямой URL /tasks/:id (фоллбек не сломан)

**Что делал:** открыл `/tasks/01a05175-0857-7e86-845c-45aed15b15db` прямой загрузкой (hard reload, без backgroundLocation в state). Скриншот: `direct-url-fullpage.webp`.

**Что увидел:** фулл-страница `TaskViewPage` (заголовок задачи как `h1` страницы, никакого `[role=dialog]`: `hasDialog=false`). F5 на этом URL — та же фулл-страница. Модальный роутинг включается **только** при переходе через `openTaskModal`/`TaskLink`.

## Колокольчик (NotificationsBell)

**Что делал:** агент `t102-bot`.claim задачи (POST `/tasks/:id/claim`) → юзеру пришла нотификация `task.assigned_to_me` с `payload.link=/tasks/<uuid>`. Открыл поповер колокольчика на `/review`, кликнул «open». Скриншоты: `bell-open.webp` → `bell-modal.webp`.

**Что увидел:** модалка открылась поверх Review, поповер закрылся (`onClick` на TaskLink), полной перезагрузки нет (SPA-флаг `window.__t102bell` жив после клика). Task-ссылка отрендерилась через TaskLink (navigate + `state.backgroundLocation`); не-task ссылки (wiki/project/легаси `/tasks/42`) покрыты юнит-тестом `NotificationsBell.test.tsx > renders non-task links … as plain links` и сохраняют прежний plain-Link рендер.

## Lesson (/lessons/:id)

**Что делал:** создал курс → модуль → урок, материализовал его с `task_id` (POST `/api/v1/agent/lessons/:id/materialize`, agent-токен). Открыл `/lessons/<id>`, кликнул «Open task» в секции Exercise. Скриншоты: `lesson-before.webp` → `lesson-modal.webp`.

**Что увидел:** модалка задачи поверх урока, урок остался смонтирован (SPA-флаг жив), Esc закрывает и возвращает на урок.

## Итог по DoD

1. Клик из Today/Inbox/Review/колокольчика/QuickCapture-toast/Lesson/BlockedBy → модалка поверх, без ухода со страницы — да, все поверхности проверены вручную (скриншоты выше) + юнит-тесты.
2. Esc/клик-вне/× закрывают, скролл фона сохранён — да (`today-after-esc.webp`, scrollY 41 → 41 → 41).
3. Прямой URL /tasks/:id и F5 → фулл-страница — да (`direct-url-fullpage.webp`, `hasDialog=false`).
4. Inbox без полной перезагрузки — да (нет `window.location.href` в коде; SPA-флаг живёт после клика).
5. Vitest — `make test` зелёный: 390 тестов, 51 файл, включая новые backgroundLocation-тесты Today/Inbox.
