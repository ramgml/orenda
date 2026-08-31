// @vitest-environment jsdom
/**
 * QuickCapture component tests.
 *
 * Global capture modal triggered by:
 *   - The 'q' hotkey (anywhere except inside text inputs)
 *   - The Cmd/Ctrl+K alias
 *   - The "+" button in the corner (data-testid="quick-capture-toggle")
 *
 * Submit creates an Inbox task via api.createInboxTask, then shows a
 * success toast with two actions: "Open task" (opens /tasks/:id as a
 * modal via the router's backgroundLocation contract) and "Dismiss".
 * Esc closes the modal.
 */
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, useLocation } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { QuickCapture } from '@/features/inbox/QuickCapture';

const { stubHttp } = vi.hoisted(() => ({
  stubHttp: {
    get: vi.fn(),
    post: vi.fn(),
    patch: vi.fn(),
    put: vi.fn(),
    delete: vi.fn(),
    interceptors: { response: { use: vi.fn() } },
  },
}));

vi.mock('axios', async (importOriginal) => {
  const actual = await importOriginal<typeof import('axios')>();
  return {
    ...actual,
    default: { ...actual.default, create: vi.fn(() => stubHttp) },
  };
});

beforeEach(() => {
  vi.clearAllMocks();
});

afterEach(() => {
  cleanup();
});

function mount() {
  return render(
    <MemoryRouter>
      <QuickCapture />
    </MemoryRouter>,
  );
}

describe('QuickCapture', () => {
  it('does not render the modal until triggered', () => {
    mount();
    expect(screen.queryByRole('dialog')).toBeNull();
  });

  it('opens the modal when the "+" button is clicked', () => {
    mount();
    fireEvent.click(screen.getByTestId('quick-capture-toggle'));
    expect(screen.getByRole('dialog')).toBeTruthy();
  });

  it('opens the modal when the "q" hotkey fires outside an input', () => {
    mount();
    fireEvent.keyDown(document.body, { key: 'q' });
    expect(screen.getByRole('dialog')).toBeTruthy();
  });

  it('opens the modal on Cmd+K', () => {
    mount();
    fireEvent.keyDown(document.body, { key: 'k', ctrlKey: true });
    expect(screen.getByRole('dialog')).toBeTruthy();
  });

  it('does NOT open the modal when the user is typing in an input', () => {
    mount();
    const input = document.createElement('input');
    document.body.appendChild(input);
    input.focus();
    fireEvent.keyDown(input, { key: 'q' });
    expect(screen.queryByRole('dialog')).toBeNull();
    document.body.removeChild(input);
  });

  it('closes the modal when Escape is pressed inside it', () => {
    mount();
    fireEvent.click(screen.getByTestId('quick-capture-toggle'));
    const dialog = screen.getByRole('dialog');
    expect(dialog).toBeTruthy();

    // Esc fires on the textarea (which is inside the dialog); the
    // component wires Esc to close.
    const textarea = screen.getByTestId('quick-capture-input');
    fireEvent.keyDown(textarea, { key: 'Escape' });
    expect(screen.queryByRole('dialog')).toBeNull();
  });

  it('submits via api.createInboxTask and shows the success toast', async () => {
    stubHttp.post.mockResolvedValueOnce({
      data: {
        id: 'new-1',
        title: 'Capture me',
        status: 'todo',
        priority: 'medium',
        awaiting: 'none',
      },
    });

    mount();
    fireEvent.click(screen.getByTestId('quick-capture-toggle'));
    fireEvent.change(screen.getByTestId('quick-capture-input'), {
      target: { value: 'Capture me' },
    });
    fireEvent.click(screen.getByTestId('quick-capture-submit'));

    await waitFor(() => {
      expect(stubHttp.post).toHaveBeenCalledWith('/api/v1/inbox/tasks', { title: 'Capture me' });
    });
    expect(await screen.findByTestId('quick-capture-toast')).toBeTruthy();
    expect(screen.getByText('✓ Captured to Inbox')).toBeTruthy();
  });

  it('disables the submit button while the request is in flight', async () => {
    let resolveCreate!: (v: unknown) => void;
    stubHttp.post.mockReturnValueOnce(
      new Promise((resolve) => {
        resolveCreate = resolve;
      }),
    );

    mount();
    fireEvent.click(screen.getByTestId('quick-capture-toggle'));
    fireEvent.change(screen.getByTestId('quick-capture-input'), {
      target: { value: 'pending' },
    });

    const submit = screen.getByTestId('quick-capture-submit');
    fireEvent.click(submit);

    await waitFor(() => {
      expect((submit as HTMLButtonElement).disabled).toBe(true);
    });

    resolveCreate({ data: { id: 'new-1', title: 'pending' } });
  });

  it('trims whitespace before submitting', async () => {
    stubHttp.post.mockResolvedValueOnce({
      data: { id: 'new-1', title: 'trimmed', status: 'todo', priority: 'medium', awaiting: 'none' },
    });

    mount();
    fireEvent.click(screen.getByTestId('quick-capture-toggle'));
    fireEvent.change(screen.getByTestId('quick-capture-input'), {
      target: { value: '  trimmed  ' },
    });
    fireEvent.click(screen.getByTestId('quick-capture-submit'));

    await waitFor(() => {
      expect(stubHttp.post).toHaveBeenCalledWith('/api/v1/inbox/tasks', { title: 'trimmed' });
    });
  });

  it('does not submit when the title is empty (whitespace-only)', async () => {
    mount();
    fireEvent.click(screen.getByTestId('quick-capture-toggle'));
    fireEvent.change(screen.getByTestId('quick-capture-input'), {
      target: { value: '   ' },
    });

    // Submit is disabled while the trimmed title is empty.
    const submit = screen.getByTestId('quick-capture-submit') as HTMLButtonElement;
    expect(submit.disabled).toBe(true);
    // Force a click anyway — submit() should early-return.
    fireEvent.click(submit);
    expect(stubHttp.post).not.toHaveBeenCalled();
  });

  it('renders the toast with "Open task" and "Dismiss" actions on success', async () => {
    stubHttp.post.mockResolvedValueOnce({
      data: { id: 'new-1', title: 'Hi', status: 'todo', priority: 'medium', awaiting: 'none' },
    });

    mount();
    fireEvent.click(screen.getByTestId('quick-capture-toggle'));
    fireEvent.change(screen.getByTestId('quick-capture-input'), { target: { value: 'Hi' } });
    fireEvent.click(screen.getByTestId('quick-capture-submit'));

    expect(await screen.findByText('Open task')).toBeTruthy();
    expect(screen.getByText('Dismiss')).toBeTruthy();
  });

  it('Cmd+Enter inside the textarea also submits', async () => {
    stubHttp.post.mockResolvedValueOnce({
      data: { id: 'new-1', title: 'kbd', status: 'todo', priority: 'medium', awaiting: 'none' },
    });

    mount();
    fireEvent.click(screen.getByTestId('quick-capture-toggle'));
    const textarea = screen.getByTestId('quick-capture-input');
    fireEvent.change(textarea, { target: { value: 'kbd' } });
    fireEvent.keyDown(textarea, { key: 'Enter', ctrlKey: true });

    await waitFor(() => {
      expect(stubHttp.post).toHaveBeenCalledWith('/api/v1/inbox/tasks', { title: 'kbd' });
    });
  });

  it('keeps the modal open when createInboxTask fails (so the user can retry)', async () => {
    // We don't render the error inline today — QuickCapture just
    // clears busy and lets the user retry. The contract pinned here
    // is that the modal stays open and no toast appears.
    stubHttp.post.mockRejectedValueOnce(new Error('boom'));

    mount();
    fireEvent.click(screen.getByTestId('quick-capture-toggle'));
    fireEvent.change(screen.getByTestId('quick-capture-input'), { target: { value: 'x' } });
    fireEvent.click(screen.getByTestId('quick-capture-submit'));

    await waitFor(() => {
      expect(stubHttp.post).toHaveBeenCalled();
    });
    expect(screen.getByRole('dialog')).toBeTruthy();
    expect(screen.queryByTestId('quick-capture-toast')).toBeNull();
  });

  it('renders an optional due-date field that starts empty', () => {
    mount();
    fireEvent.click(screen.getByTestId('quick-capture-toggle'));
    const due = screen.getByTestId('quick-capture-due') as HTMLInputElement;
    expect(due).toBeTruthy();
    expect(due.value).toBe('');
  });

  it('posts due_at as RFC3339 when a date is picked', async () => {
    stubHttp.post.mockResolvedValueOnce({
      data: {
        id: 'new-1',
        title: 'deadline',
        status: 'todo',
        priority: 'medium',
        awaiting: 'none',
      },
    });

    mount();
    fireEvent.click(screen.getByTestId('quick-capture-toggle'));
    fireEvent.change(screen.getByTestId('quick-capture-input'), { target: { value: 'deadline' } });
    fireEvent.change(screen.getByTestId('quick-capture-due'), { target: { value: '2026-09-01' } });
    fireEvent.click(screen.getByTestId('quick-capture-submit'));

    await waitFor(() => {
      expect(stubHttp.post).toHaveBeenCalledWith('/api/v1/inbox/tasks', {
        title: 'deadline',
        // Local midnight → UTC ISO; compute the same way the component does.
        due_at: new Date('2026-09-01T00:00:00').toISOString(),
      });
    });
  });

  it('does not send due_at when no date is picked (hotkey flow unchanged)', async () => {
    stubHttp.post.mockResolvedValueOnce({
      data: { id: 'new-1', title: 'plain', status: 'todo', priority: 'medium', awaiting: 'none' },
    });

    mount();
    fireEvent.click(screen.getByTestId('quick-capture-toggle'));
    const textarea = screen.getByTestId('quick-capture-input');
    fireEvent.change(textarea, { target: { value: 'plain' } });
    fireEvent.keyDown(textarea, { key: 'Enter', ctrlKey: true });

    await waitFor(() => {
      // Exact payload: no due_at key at all.
      expect(stubHttp.post).toHaveBeenCalledWith('/api/v1/inbox/tasks', { title: 'plain' });
    });
  });

  it('clears the due field after a successful capture', async () => {
    stubHttp.post.mockResolvedValueOnce({
      data: { id: 'new-1', title: 'once', status: 'todo', priority: 'medium', awaiting: 'none' },
    });

    mount();
    fireEvent.click(screen.getByTestId('quick-capture-toggle'));
    fireEvent.change(screen.getByTestId('quick-capture-input'), { target: { value: 'once' } });
    fireEvent.change(screen.getByTestId('quick-capture-due'), { target: { value: '2026-09-01' } });
    fireEvent.click(screen.getByTestId('quick-capture-submit'));

    // Toast appears; dismiss it and reopen — the date must be gone
    // (a stale deadline on the next capture would be a data bug).
    expect(await screen.findByTestId('quick-capture-toast')).toBeTruthy();
    fireEvent.click(screen.getByText('Dismiss'));
    fireEvent.click(screen.getByTestId('quick-capture-toggle'));
    expect((screen.getByTestId('quick-capture-due') as HTMLInputElement).value).toBe('');
  });

  // Phase 32.13: regression guard. Pre-migration, a ref-callback
  // called `el.focus()` on the textarea. The shadcn DialogContent
  // wrapper has a built-in × close button as its first focusable
  // child, so Radix's FocusScope would otherwise focus that hidden
  // button — breaking the 'q → start typing' hotkey flow. The
  // onOpenAutoFocus handler must redirect focus to the textarea.
  it('focuses the textarea on open so the hotkey flow stays one keystroke from thinking to typing', () => {
    mount();
    fireEvent.click(screen.getByTestId('quick-capture-toggle'));
    const textarea = screen.getByTestId('quick-capture-input') as HTMLTextAreaElement;
    expect(document.activeElement).toBe(textarea);
  });

  // Task 102: "Open task" on the toast opens the captured task as a
  // modal overlay (navigate + state.backgroundLocation), not a
  // full-page jump.
  it('Open task navigates with state.backgroundLocation (modal contract)', async () => {
    stubHttp.post.mockResolvedValueOnce({
      data: {
        id: 'modal-7',
        title: 'Toast open',
        status: 'todo',
        priority: 'medium',
        awaiting: 'none',
      },
    });

    const navigations: Array<{ pathname: string; state: unknown }> = [];
    function Probe() {
      const location = useLocation();
      navigations.push({ pathname: location.pathname, state: location.state });
      return null;
    }
    render(
      <MemoryRouter initialEntries={['/today']}>
        <Probe />
        <QuickCapture />
      </MemoryRouter>,
    );

    fireEvent.click(screen.getByTestId('quick-capture-toggle'));
    fireEvent.change(screen.getByTestId('quick-capture-input'), {
      target: { value: 'Toast open' },
    });
    fireEvent.click(screen.getByTestId('quick-capture-submit'));

    fireEvent.click(await screen.findByText('Open task'));

    const last = navigations[navigations.length - 1];
    expect(last.pathname).toBe('/tasks/modal-7');
    expect(last.state).toEqual({
      backgroundLocation: expect.objectContaining({ pathname: '/today' }),
    });
  });
});
