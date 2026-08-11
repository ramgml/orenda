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
import { DndContext } from '@dnd-kit/core'
import { fireEvent, render } from '@testing-library/react'
import { MemoryRouter, Route, Routes } from 'react-router-dom'
import { describe, expect, it, vi } from 'vitest'

import type { Task } from '@/shared/api/client'
import { TaskCard } from '@/features/projects/TaskCard'

function makeTask(): Task {
  return {
    id: 'task-42',
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
  }
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
  const iter = document.createNodeIterator(container, NodeFilter.SHOW_TEXT)
  let node: Node | null
  while ((node = iter.nextNode())) {
    if (node.textContent === 'Sample task') {
      let el: HTMLElement | null = (node.parentElement as HTMLElement | null)
      while (el && el.getAttribute('aria-roledescription') !== 'draggable') {
        el = el.parentElement
      }
      if (!el) {
        throw new Error('TaskCard draggable wrapper not found')
      }
      return el
    }
  }
  throw new Error('Sample task text not found in DOM')
}

describe('TaskCard', () => {
  it('renders as a div (not an anchor) — dnd-kit + <a> was the bug', () => {
    const { container, queryByRole } = render(
      <MemoryRouter>
        <DndContext>
          <TaskCard task={makeTask()} />
        </DndContext>
      </MemoryRouter>,
    )
    // The card must NOT be an anchor; the old impl wrapped TaskCard in
    // <a href="/tasks/:id"> which dnd-kit's pointer listeners would
    // steal clicks from.
    expect(queryByRole('link')).toBeNull()
    // …and it must be a clickable element (dnd-kit sets role=button
    // and tabindex=0 via its `attributes` spread).
    expect(getCardRoot(container)).toBeTruthy()
  })

  it('calls onOpen(taskId) on click instead of relying on href navigation', () => {
    const onOpen = vi.fn()
    const { container } = render(
      <MemoryRouter>
        <DndContext>
          <TaskCard task={makeTask()} onOpen={onOpen} />
        </DndContext>
      </MemoryRouter>,
    )
    fireEvent.click(getCardRoot(container))
    expect(onOpen).toHaveBeenCalledTimes(1)
    expect(onOpen).toHaveBeenCalledWith('task-42')
  })

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
      <MemoryRouter initialEntries={['/']}>
        <DndContext>
          <Routes>
            <Route path="/" element={<TaskCard task={makeTask()} />} />
            <Route path="/tasks/:id" element={<div>ARRIVED</div>} />
          </Routes>
        </DndContext>
      </MemoryRouter>,
    )
    fireEvent.click(getCardRoot(container))
    expect(queryByText('ARRIVED')).toBeTruthy()
  })

  // Phase 13: the card's left stripe is derived from `task.color`.
  it('renders a left colour stripe when the task has a color', () => {
    const { container } = render(
      <MemoryRouter>
        <DndContext>
          <TaskCard task={makeTask()} />
        </DndContext>
      </MemoryRouter>,
    )
    // No colour → no stripe (borderLeftWidth default 0).
    const cardNoColor = getCardRoot(container)
    expect(cardNoColor.style.borderLeftWidth).toBe('')

    const withColor = render(
      <MemoryRouter>
        <DndContext>
          <TaskCard task={{ ...makeTask(), id: 't-coloured', color: '#0ea5e9' }} />
        </DndContext>
      </MemoryRouter>,
    )
    const cardColored = getCardRoot(withColor.container)
    // Phase 17: the stripe comes from the inline `borderLeftColor` (the
    // width is now a Tailwind class `border-l-4`, so the inline style
    // only covers the colour). The colour is the one the user picked.
    expect(cardColored.style.borderLeftColor).toBe('rgb(14, 165, 233)')
  })

  // Phase 13: tag chips render below the title when the task has tags.
  it('renders tag chips when the task has tags', () => {
    const { container } = render(
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
    )
    expect(container.textContent).toContain('frontend')
    expect(container.textContent).toContain('backend')
  })

  it('omits tag chips when the task has no tags', () => {
    const { container } = render(
      <MemoryRouter>
        <DndContext>
          <TaskCard task={{ ...makeTask(), tags: [] }} />
        </DndContext>
      </MemoryRouter>,
    )
    // The title is rendered; no tag-pill spans should be present.
    // We anchor on the TaskTagChip class signature: inline-flex items-center
    // px-1.5 py-0.5 text-[10px] font-medium rounded border max-w-[8rem].
    const card = getCardRoot(container)
    const tagPills = card.querySelectorAll('span.inline-flex.items-center')
    expect(tagPills.length).toBe(0)
  })
})
