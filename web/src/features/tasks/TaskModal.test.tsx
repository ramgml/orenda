// @vitest-environment jsdom
/**
 * Phase 28.3: regression tests for the TaskModal long-content scroll
 * fix. The component carries three contracts we want to keep honest:
 *
 *   1. Backdrop layout — no more `md:items-center` (the bug was
 *      flex centring pushing the top of the card above the scroll
 *      viewport), but still `overflow-y-auto` (the scroll surface
 *      lives here). The card itself now carries `my-auto` so a
 *      short card stays vertically centred via auto margins while
 *      a tall card naturally collapses to the padding edge.
 *   2. Body scroll lock — invokes `useBodyScrollLock`; this is also
 *      covered by the hook's own unit tests, but it's worth pinning
 *      end-to-end so a future refactor that swaps the hook for an
 *      inline effect can't silently re-introduce the bug.
 *   3. Click semantics — backdrop click closes, card click does not.
 *
 * We mock `TaskViewBody` so the test doesn't have to instantiate
 * the whole data-loading subsystem (auth + API + WS); the modal's
 * own layout and scroll-lock logic is what we're pinning.
 */
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { MemoryRouter, Routes, Route } from 'react-router-dom';

import { TaskModal } from '@/features/tasks/TaskModal';

// Stub the modal body — it isn't what we're testing here, and
// pulling in its API + auth + WS surface would slow the test and
// add dependencies unrelated to the scroll fix.
vi.mock('@/features/tasks/TaskViewBody', () => ({
  TaskViewBody: () => <div data-testid="mocked-task-body" />,
}));

function renderModal(taskId = 'task-abc'): ReturnType<typeof render> {
  // Two entries — '/inbox' then '/tasks/:id' — so navigate(-1) inside
  // the modal pops back to a real route that exists. The pre-migration
  // contract was "background page stays mounted", and the modal
  // unmounts when the URL leaves the task route.
  return render(
    <MemoryRouter
      future={{ v7_startTransition: true, v7_relativeSplatPath: true }}
      initialEntries={['/inbox', `/tasks/${taskId}`]}
      initialIndex={1}
    >
      <Routes>
        <Route path="/inbox" element={<div>inbox background</div>} />
        <Route path="/tasks/:id" element={<TaskModal />} />
      </Routes>
    </MemoryRouter>,
  );
}

function backdrop(): HTMLElement {
  const dialog = screen.getByRole('dialog');
  return dialog;
}

function card(): HTMLElement {
  return screen.getByTestId('mocked-task-body').parentElement?.parentElement as HTMLElement;
}

describe('TaskModal — Phase 28.3 scroll fix', () => {
  beforeEach(() => {
    document.body.style.overflow = '';
  });

  afterEach(() => {
    // vitest has no auto-cleanup hook for RTL unless we add a setup
    // file. Without this, DOM from a prior test stays in the
    // document and `screen.getByTestId` finds both the old and the
    // new mock-body element.
    cleanup();
    document.body.style.overflow = '';
    vi.restoreAllMocks();
  });

  it('backdrop uses items-start (no md:items-center) and carries overflow-y-auto', () => {
    renderModal();
    const cls = backdrop().className;
    // The bug we're fixing: `md:items-center` clips the top of a
    // tall card outside the scroll viewport. We replaced it with
    // `items-start` on the backdrop + `my-auto` on the card.
    expect(cls).toContain('items-start');
    expect(cls).not.toContain('items-center');
    // The whole modal is one scroll container.
    expect(cls).toContain('overflow-y-auto');
    // Just-in-case sanity: p-* spacing preserved.
    expect(cls).toContain('p-2');
  });

  it('card carries my-auto so a short card centres and a tall card anchors to the top', () => {
    renderModal();
    const cls = card().className;
    expect(cls).toContain('my-auto');
    // Pre-28.3 the card said `my-4 md:my-0` — that's gone.
    expect(cls).not.toContain('my-4');
    expect(cls).not.toContain('md:my-0');
  });

  it('locks body scroll while mounted and restores on unmount', () => {
    expect(document.body.style.overflow).toBe('');
    const view = renderModal();
    expect(document.body.style.overflow).toBe('hidden');
    view.unmount();
    expect(document.body.style.overflow).toBe('');
  });

  it('click on the card does not bubble to the backdrop (only backdrop click closes)', () => {
    renderModal();
    const body = screen.getByTestId('mocked-task-body');
    // We rely on the `stopPropagation` on the inner div. Render is
    // cheap; click must not throw, and the body should still be in
    // the document after the click (no parent click handler running
    // through `close` → unmount).
    body.click();
    expect(screen.queryByTestId('mocked-task-body')).toBeTruthy();
  });

  // Phase 32.13: the migrated overlay has no separate backdrop
  // element — the DialogPrimitive.Content itself is the scroll
  // container with flex padding. Radix's onPointerDownOutside
  // only fires when the click is outside the Content box, which
  // is never true here. The migration uses a target-equals-
  // currentTarget click handler on the Content to preserve the
  // pre-migration "click the empty padding to close" contract.
  it('click on the scroll container (overlay area) closes the modal', async () => {
    renderModal();
    const dialog = screen.getByRole('dialog', { name: 'Task details' });
    // Hit the dialog itself (padding area, not the card).
    fireEvent.click(dialog);
    // close() calls navigate(-1); in a routed test, the only
    // mounted route was /tasks/:id, so navigation pops back to
    // the in-memory '/'. The component unmounts.
    await waitFor(() => {
      expect(screen.queryByRole('dialog', { name: 'Task details' })).toBeNull();
    });
  });
});
