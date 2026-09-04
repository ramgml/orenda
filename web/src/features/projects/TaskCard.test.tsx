// @vitest-environment jsdom
/**
 * Regression test for the kanban-click bug.
 *
 * Symptom: clicking a TaskCard on the kanban failed to navigate to the
 * task detail page. Root cause: TaskCard was wrapped in `<a href>`,
 * and @dnd-kit/core's `listeners`/`attributes` spread on the same
 * wrapper registered `onPointerDown` that, in some pointer event
 * orders, cancelled the user-anchor click.
 *
 * Fix: TaskCard now navigates via `onClick` + react-router's
 * `useNavigate`. This test pins the two contracts:
 *   1. The card is rendered as a `<div>` (NOT an `<a>`) — the old
 *      implementation relied on `<a href>` and broke under dnd-kit.
 *   2. A click on the card invokes `onOpen(taskId)` so the parent
 *      ColumnView routes via useNavigate.
 */
import { DndContext } from '@dnd-kit/core';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router';
import { describe, expect, it, vi } from 'vitest';

import type { Task } from '@/shared/api/client';
import { agentsQueryKey } from '@/shared/hooks/useAgents';
import { TaskCard } from '@/features/projects/TaskCard';

// Phase 28.19: TaskCard pulls in `useAgents` for the AssigneeChip
// title hint, which lives behind React Query. Wrap every render in a
// throwaway QueryClient so the hook doesn't blow up in jsdom. The
// tests never inspect the agents list themselves; the cache just
// has to exist for the hook to mount.
function withQuery(node: JSX.Element, seedAgents?: unknown[]): JSX.Element {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: 0 } },
  });
  if (seedAgents) qc.setQueryData(agentsQueryKey, seedAgents);
  return <QueryClientProvider client={qc}>{node}</QueryClientProvider>;
}

function makeTask(): Task {
  return {
    id: 'task-42',
    number: 42,
    project_id: 'p1',
    column_id: 'col-1',
    title: 'Sample task',
    status: 'todo',
    priority: 'medium',
    awaiting: 'none',
    time_spent_s: 0,
    position: 0,
    color: '',
    created_at: '',
    updated_at: '',
  };
}

/**
 * @dnd-kit/core inserts sensor nodes into the DOM that ALSO expose
 * role=button, so a query like `getByRole('button', { name })` would
 * match both the card and the sensor. We anchor on the title text
 * directly (which the card renders as a literal child node, not as a
 * sensor), then walk up to the draggable wrapper that owns the click
 * handler (the element with `aria-roledescription="draggable"`).
 */
function getCardRoot(container: HTMLElement): HTMLElement {
  // Walk every text node for the exact title.
  const iter = document.createNodeIterator(container, NodeFilter.SHOW_TEXT);
  let node: Node | null;
  while ((node = iter.nextNode())) {
    if (node.textContent === 'Sample task') {
      let el: HTMLElement | null = node.parentElement as HTMLElement | null;
      while (el && el.getAttribute('aria-roledescription') !== 'draggable') {
        el = el.parentElement;
      }
      if (!el) {
        throw new Error('TaskCard draggable wrapper not found');
      }
      return el;
    }
  }
  throw new Error('Sample task text not found in DOM');
}

describe('TaskCard', () => {
  it('renders as a div (not an anchor) — dnd-kit + <a> was the bug', () => {
    const { container, queryByRole } = render(
      withQuery(
        <MemoryRouter>
          <DndContext>
            <TaskCard task={makeTask()} />
          </DndContext>
        </MemoryRouter>,
      ),
    );
    // The card must NOT be an anchor; the old impl wrapped TaskCard in
    // <a href="/tasks/:id"> which dnd-kit's pointer listeners would
    // steal clicks from.
    expect(queryByRole('link')).toBeNull();
    // …and it must be a clickable element (dnd-kit sets role=button
    // and tabindex=0 via its `attributes` spread).
    expect(getCardRoot(container)).toBeTruthy();
  });

  it('calls onOpen(taskId) on click instead of relying on href navigation', () => {
    const onOpen = vi.fn();
    const { container } = render(
      withQuery(
        <MemoryRouter>
          <DndContext>
            <TaskCard task={makeTask()} onOpen={onOpen} />
          </DndContext>
        </MemoryRouter>,
      ),
    );
    fireEvent.click(getCardRoot(container));
    expect(onOpen).toHaveBeenCalledTimes(1);
    expect(onOpen).toHaveBeenCalledWith('task-42');
  });

  it('navigates to /tasks/:id via useNavigate when no onOpen is provided', () => {
    // We render the card inside a real Router + a Route that captures
    // the destination, so we can assert that programmatic navigation
    // actually arrived at the right URL.
    //
    // After the modal refactor the navigation also carries
    // `state.backgroundLocation` (so the kanban stays mounted behind
    // the modal). The Routes below only check the path; the state is
    // just an implementation detail of openTaskModal.
    const { container, queryByText } = render(
      withQuery(
        <MemoryRouter initialEntries={['/']}>
          <DndContext>
            <Routes>
              <Route path="/" element={<TaskCard task={makeTask()} />} />
              <Route path="/tasks/:id" element={<div>ARRIVED</div>} />
            </Routes>
          </DndContext>
        </MemoryRouter>,
      ),
    );
    fireEvent.click(getCardRoot(container));
    expect(queryByText('ARRIVED')).toBeTruthy();
  });

  // Phase 13: the card's left stripe is derived from `task.color`.
  it('renders a left colour stripe when the task has a color', () => {
    const { container } = render(
      withQuery(
        <MemoryRouter>
          <DndContext>
            <TaskCard task={makeTask()} />
          </DndContext>
        </MemoryRouter>,
      ),
    );
    // No colour → no stripe (borderLeftWidth default 0).
    const cardNoColor = getCardRoot(container);
    expect(cardNoColor.style.borderLeftWidth).toBe('');

    const withColor = render(
      withQuery(
        <MemoryRouter>
          <DndContext>
            <TaskCard task={{ ...makeTask(), id: 't-coloured', color: '#0ea5e9' }} />
          </DndContext>
        </MemoryRouter>,
      ),
    );
    const cardColored = getCardRoot(withColor.container);
    // Phase 17: the stripe comes from the inline `borderLeftColor` (the
    // width is now a Tailwind class `border-l-4`, so the inline style
    // only covers the colour). The colour is the one the user picked.
    expect(cardColored.style.borderLeftColor).toBe('rgb(14, 165, 233)');
  });

  // Phase 13: tag chips render below the title when the task has tags.
  it('renders tag chips when the task has tags', () => {
    const { container } = render(
      withQuery(
        <MemoryRouter>
          <DndContext>
            <TaskCard
              task={{
                ...makeTask(),
                tags: [
                  { id: 't-1', name: 'frontend', color: '#22c55e' },
                  { id: 't-2', name: 'backend' },
                ],
              }}
            />
          </DndContext>
        </MemoryRouter>,
      ),
    );
    expect(container.textContent).toContain('frontend');
    expect(container.textContent).toContain('backend');
  });

  // Phase 27.3: the backend now populates task.tags on list payloads.
  // The chip's background colour must come from the tag's colour, not
  // the slate fallback — otherwise the enrichment is silently broken
  // even when the chip renders.
  it('applies tag colours to chip backgrounds (Phase 27.3 enrichment)', () => {
    const { container } = render(
      withQuery(
        <MemoryRouter>
          <DndContext>
            <TaskCard
              task={{
                ...makeTask(),
                tags: [
                  { id: 't-1', name: 'frontend', color: '#22c55e' },
                  { id: 't-2', name: 'backend', color: '#2563eb' },
                ],
              }}
            />
          </DndContext>
        </MemoryRouter>,
      ),
    );
    const chips = container.querySelectorAll('span.inline-flex.items-center.px-1\\.5.py-0\\.5');
    expect(chips.length).toBeGreaterThanOrEqual(2);
    // Each chip should pick up the colour from its tag.
    const colors = Array.from(chips).map((c) => (c as HTMLElement).style.backgroundColor);
    expect(colors).toContain('rgb(34, 197, 94)'); // #22c55e
    expect(colors).toContain('rgb(37, 99, 235)'); // #2563eb
  });

  it('omits tag chips when the task has no tags', () => {
    const { container } = render(
      withQuery(
        <MemoryRouter>
          <DndContext>
            <TaskCard task={{ ...makeTask(), tags: [] }} />
          </DndContext>
        </MemoryRouter>,
      ),
    );
    // The title is rendered; no tag-pill spans should be present.
    // We anchor on the TaskTagChip class signature: inline-flex items-center
    // px-1.5 py-0.5 text-[10px] font-medium rounded border max-w-[8rem].
    // The task-number chip also carries inline-flex/items-center, so it
    // is excluded by testid — this assertion is about tag pills only.
    const card = getCardRoot(container);
    const tagPills = card.querySelectorAll(
      'span.inline-flex.items-center:not([data-testid="task-number-chip"])',
    );
    expect(tagPills.length).toBe(0);
  });

  // Phase 28.19: the AssigneeChip's title carries the agent's name +
  // free-form label set when the agents cache is warm. Without that
  // enrichment the kanban card silently regresses to "Agent: <id>"
  // and operators lose the at-a-glance signal of which model the
  // card is bound to.
  it('AssigneeChip title surfaces agent labels (Phase 28.19 enrichment)', () => {
    const { container } = render(
      withQuery(
        <MemoryRouter>
          <DndContext>
            <TaskCard
              task={{
                ...makeTask(),
                assignee_type: 'agent',
                assignee_id: 'agent-qwen',
              }}
            />
          </DndContext>
        </MemoryRouter>,
        [
          {
            id: 'agent-qwen',
            name: 'qwen-alpha',
            type: ['qwen', 'installer'],
            description: '',
            token_id: 't-1',
            status: 'online',
            max_concurrent: 3,
            created_at: '',
          },
        ],
      ),
    );
    const chip = container.querySelector('[data-testid="assignee-agent"]') as HTMLElement;
    expect(chip).toBeTruthy();
    expect(chip.title).toBe('Agent: qwen-alpha (qwen, installer)');
    // Visible label still uses the agent's name (not just an id slice).
    expect(chip.textContent).toContain('qwen-alpha');
  });

  // Phase 28.19: when the agents cache is cold (or the lookup misses),
  // the chip must still render the legacy "Agent: <id>" title and the
  // id slice — falling back gracefully rather than crashing on
  // `agent.type.join`.
  it('AssigneeChip falls back to id when agent lookup misses', () => {
    const { container } = render(
      withQuery(
        <MemoryRouter>
          <DndContext>
            <TaskCard
              task={{
                ...makeTask(),
                assignee_type: 'agent',
                assignee_id: 'agent-missing',
              }}
            />
          </DndContext>
        </MemoryRouter>,
        [
          {
            id: 'some-other-agent',
            name: 'someone-else',
            type: ['claude'],
            description: '',
            token_id: 't-2',
            status: 'offline',
            max_concurrent: 1,
            created_at: '',
          },
        ],
      ),
    );
    const chip = container.querySelector('[data-testid="assignee-agent"]') as HTMLElement;
    expect(chip).toBeTruthy();
    expect(chip.title).toBe('Agent: agent-missing');
    // Visible label uses the first 6 chars of the id, like before.
    expect(chip.textContent).toContain('agent-');
  });

  // Task #31: cards overflowed their column on narrow viewports /
  // long unbroken titles. The fix pins two classes on the card:
  // `w-full min-w-0` on the root (a flex item of the row <li>, so it
  // fills the track but may shrink below its content's intrinsic
  // width) and `break-words` on the title span so long URLs/wraps
  // instead of widening the card. Pin both so a styling pass can't
  // silently reintroduce the overflow.
  it('root carries w-full min-w-0 and the title wraps long words (task #31)', () => {
    const longTitle = 'https://example.com/some/deeply/nested/path/without-any-space-0123456789';
    const { container } = render(
      withQuery(
        <MemoryRouter>
          <DndContext>
            <TaskCard
              task={{
                ...makeTask(),
                title: longTitle,
                assignee_type: 'agent',
                assignee_id: 'agent-x',
              }}
            />
          </DndContext>
        </MemoryRouter>,
      ),
    );
    const root = container.querySelector('[data-testid="task-card"]') as HTMLElement;
    expect(root).toBeTruthy();
    expect(root.className).toMatch(/\bw-full\b/);
    expect(root.className).toMatch(/\bmin-w-0\b/);
    // The title span must allow wrapping — a single unbreakable
    // string was enough to push the card past the column border.
    const titleSpan = root.querySelector(`span.break-words`) as HTMLElement;
    expect(titleSpan).toBeTruthy();
    expect(titleSpan.textContent).toBe(longTitle);
    // The agent assignee chip must be able to shrink too — at very
    // narrow columns it otherwise pokes past the card border.
    const chip = root.querySelector('[data-testid="assignee-agent"]') as HTMLElement;
    expect(chip).toBeTruthy();
    expect(chip.className).toMatch(/\bmin-w-0\b/);
    expect(chip.className).toMatch(/\bmax-w-full\b/);
  });

  // Task #34: the assignee chip used to live in the title row, where
  // a long agent name (Phase 28.19's labels) compressed the title
  // into a narrow column. The chip now sits at the bottom of the card
  // so the title gets the full width. These tests pin the new layout.

  /**
   * Walk up from the title text node to the row that used to host the
   * assignee chip (`<div className="flex items-start gap-1">`). We
   * anchor on the title text rather than a Tailwind class selector so
   * the test fails loudly if the row's structure ever changes — a class
   * query would silently keep matching whatever happens to carry those
   * classes, masking real regressions.
   */
  function getTitleRow(container: HTMLElement): HTMLElement {
    const iter = document.createNodeIterator(container, NodeFilter.SHOW_TEXT);
    let node: Node | null;
    while ((node = iter.nextNode())) {
      if (node.textContent === 'Sample task') {
        // text → <div className="text-slate-800 …"> → <div className="flex-1 min-w-0">
        //     → <div className="flex items-start gap-1">   ← title row
        let el: HTMLElement | null = node.parentElement;
        for (let i = 0; i < 3 && el; i++) el = el.parentElement;
        if (!el) throw new Error('TaskCard title row not found');
        return el;
      }
    }
    throw new Error('Sample task text not found in DOM');
  }

  /**
   * The detailed badge row carries the class signature
   * `flex items-center gap-2 mt-1.5 flex-wrap text-[10px]`. We anchor
   * on it directly because it has a single, unambiguous role in the
   * card, so a class query is safe (and the assertion that follows
   * fails noisily if the class set ever drifts).
   */
  function getDetailedBadgesRow(container: HTMLElement): HTMLElement {
    const row = container.querySelector(
      '.flex.items-center.gap-2.mt-1\\.5.flex-wrap.text-\\[10px\\]',
    );
    if (!row) throw new Error('TaskCard detailed badges row not found');
    return row as HTMLElement;
  }

  it('does not place the assignee chip in the title row (Task #34)', () => {
    const { container } = render(
      withQuery(
        <MemoryRouter>
          <DndContext>
            <TaskCard
              task={{
                ...makeTask(),
                assignee_type: 'agent',
                assignee_id: 'agent-qwen-alpha',
              }}
            />
          </DndContext>
        </MemoryRouter>,
      ),
    );
    const chip = container.querySelector('[data-testid="assignee-agent"]') as HTMLElement;
    expect(chip).toBeTruthy();
    // The chip must NOT be a descendant of the title row — a long
    // agent name used to live there and squeezed the title.
    expect(getTitleRow(container).contains(chip)).toBe(false);
    // And it MUST be a descendant of the detailed badges row.
    expect(getDetailedBadgesRow(container).contains(chip)).toBe(true);
  });

  it('keeps the assignee chip visible in compact mode (Task #34)', () => {
    // Compact mode is persisted in localStorage; seed it before render.
    window.localStorage.setItem('orenda.kanban.cardDensity', 'compact');
    try {
      const { container } = render(
        withQuery(
          <MemoryRouter>
            <DndContext>
              <TaskCard
                task={{
                  ...makeTask(),
                  assignee_type: 'agent',
                  assignee_id: 'agent-qwen-alpha',
                }}
              />
            </DndContext>
          </MemoryRouter>,
        ),
      );
      // In compact mode the detailed badges row is hidden — the
      // assignee chip re-mounts on its own line so the assignee
      // signal stays visible.
      expect(container.querySelector('.flex.items-center.gap-2.mt-1\\.5.flex-wrap')).toBeNull();
      const chip = container.querySelector('[data-testid="assignee-agent"]') as HTMLElement;
      expect(chip).toBeTruthy();
      // And it must NOT be inside the title row either — that was
      // the old layout we explicitly moved away from.
      expect(getTitleRow(container).contains(chip)).toBe(false);
    } finally {
      window.localStorage.removeItem('orenda.kanban.cardDensity');
    }
  });

  it('renders the user assignee chip alongside the agent chip placement (Task #34)', () => {
    // Same structural contract for the user-avatar branch: it must
    // move out of the title row too, so the avatar is consistent with
    // where the agent chip now lives (and doesn't add a stray
    // fixed-size circle to the title row at compact density).
    const { container } = render(
      withQuery(
        <MemoryRouter>
          <DndContext>
            <TaskCard
              task={{
                ...makeTask(),
                assignee_type: 'user',
                assignee_id: 'abc-def-ghi',
              }}
            />
          </DndContext>
        </MemoryRouter>,
      ),
    );
    const chip = container.querySelector('[data-testid="assignee-user"]') as HTMLElement;
    expect(chip).toBeTruthy();
    expect(getTitleRow(container).contains(chip)).toBe(false);
    expect(getDetailedBadgesRow(container).contains(chip)).toBe(true);
  });
});
