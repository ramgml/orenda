import { useCallback, useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { createPortal } from 'react-dom';

import { api, type Task } from '@/shared/api/client';

/**
 * Phase 21: global quick-capture modal.
 *
 * Two trigger surfaces, one component:
 *  - Global hotkey `q` (works everywhere except inside text inputs,
 *    to avoid stealing keystrokes); `Cmd/Ctrl+K` as an alias.
 *  - The "+" button in the sidebar (rendered by AppLayout).
 *
 * Submit creates an Inbox task (Phase 16: project_id IS NULL), then
 * shows a "Created — open?" toast with a link. Escape closes; clicking
 * the backdrop closes.
 *
 * Phase 30.10: an optional due date rides along. The field is off the
 * hotkey path — q → type → Cmd/Ctrl+Enter still captures without
 * touching it; it only adds one Tab stop for keyboard users who want
 * a deadline.
 *
 * The modal is rendered via React Portal at document.body so it
 * escapes any overflow:hidden on the kanban surface.
 */
export function QuickCapture() {
  const [open, setOpen] = useState(false);
  const [title, setTitle] = useState('');
  const [due, setDue] = useState('');
  const [busy, setBusy] = useState(false);
  const [created, setCreated] = useState<Task | null>(null);
  const navigate = useNavigate();

  const close = useCallback(() => {
    setOpen(false);
    setTitle('');
    setDue('');
  }, []);

  // Global hotkey: 'q' or Cmd/Ctrl+K. Skip when the user is typing
  // inside an input/textarea/contenteditable — those keystrokes
  // belong to the field, not us.
  useEffect(() => {
    function onKey(e: KeyboardEvent): void {
      // Skip if user is typing in an editable element.
      const target = e.target as HTMLElement | null;
      if (target) {
        const tag = target.tagName;
        if (tag === 'INPUT' || tag === 'TEXTAREA' || target.isContentEditable) {
          return;
        }
      }
      if (e.key === 'q' || (e.key === 'k' && (e.ctrlKey || e.metaKey))) {
        e.preventDefault();
        setOpen(true);
      } else if (e.key === 'Escape' && open) {
        close();
      }
    }
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [open, close]);

  // Cmd/Ctrl+Enter submits from inside the textarea.
  const onKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>): void => {
    if ((e.ctrlKey || e.metaKey) && e.key === 'Enter') {
      e.preventDefault();
      void submit();
    } else if (e.key === 'Escape') {
      e.preventDefault();
      close();
    }
  };

  async function submit(): Promise<void> {
    const t = title.trim();
    if (!t || busy) return;
    setBusy(true);
    try {
      // Phase 30.10: optional due date. Only send the key when the
      // user picked one — the payload stays {title} for the classic
      // hotkey flow (q → type → Cmd+Enter), so capture is not slowed.
      // The <input type="date"> yields YYYY-MM-DD; the API contract
      // is RFC3339, so anchor to local midnight and serialise.
      const input: { title: string; due_at?: string } = { title: t };
      if (due) {
        input.due_at = new Date(`${due}T00:00:00`).toISOString();
      }
      const created = await api.createInboxTask(input);
      setCreated(created);
      setTitle('');
      setDue('');
    } catch {
      // Swallow: the modal stays open so the user can retry. The
      // create-inbox-task endpoint shouldn't fail in practice (it
      // only requires a title); a future phase may surface this.
    } finally {
      setBusy(false);
    }
  }

  function openTaskAndClose(): void {
    if (created) {
      navigate(`/tasks/${created.id}`);
    }
    setCreated(null);
    close();
  }

  function dismissToast(): void {
    setCreated(null);
    close();
  }

  return (
    <>
      <button
        type="button"
        onClick={() => setOpen(true)}
        data-testid="quick-capture-toggle"
        title="Quick capture (q or Cmd/Ctrl+K)"
        aria-label="Quick capture"
        className="fixed bottom-4 right-4 z-40 h-12 w-12 rounded-full bg-orenda-600 hover:bg-orenda-700 text-white text-2xl shadow-lg"
      >
        +
      </button>
      {open &&
        createPortal(
          <CaptureModal
            title={title}
            due={due}
            busy={busy}
            created={created}
            setTitle={setTitle}
            setDue={setDue}
            onKeyDown={onKeyDown}
            onSubmit={() => void submit()}
            onClose={close}
            onOpenTask={openTaskAndClose}
            onDismissToast={dismissToast}
          />,
          document.body,
        )}
    </>
  );
}

function CaptureModal({
  title,
  due,
  busy,
  created,
  setTitle,
  setDue,
  onKeyDown,
  onSubmit,
  onClose,
  onOpenTask,
  onDismissToast,
}: {
  title: string;
  due: string;
  busy: boolean;
  created: Task | null;
  setTitle: (s: string) => void;
  setDue: (s: string) => void;
  onKeyDown: (e: React.KeyboardEvent<HTMLTextAreaElement>) => void;
  onSubmit: () => void;
  onClose: () => void;
  onOpenTask: () => void;
  onDismissToast: () => void;
}): JSX.Element {
  // Auto-focus the textarea once mounted.
  const ref = useCallback((el: HTMLTextAreaElement | null) => {
    if (el) el.focus();
  }, []);

  return (
    <div
      role="dialog"
      aria-modal="true"
      aria-label="Quick capture"
      className="fixed inset-0 z-50 flex items-start justify-center pt-24 bg-black/40"
      onClick={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <div className="bg-white dark:bg-slate-950 rounded-lg shadow-xl w-full max-w-lg p-4 space-y-3">
        {created ? (
          <div className="space-y-3" data-testid="quick-capture-toast">
            <p className="text-sm text-emerald-700">✓ Captured to Inbox</p>
            <p className="text-slate-800 dark:text-slate-100">{created.title}</p>
            <div className="flex gap-2">
              <button
                type="button"
                onClick={onOpenTask}
                className="px-3 py-1.5 rounded bg-orenda-600 hover:bg-orenda-700 text-white text-sm"
              >
                Open task
              </button>
              <button
                type="button"
                onClick={onDismissToast}
                className="px-3 py-1.5 rounded border border-slate-300 text-slate-700 text-sm"
              >
                Dismiss
              </button>
            </div>
          </div>
        ) : (
          <>
            <h2 className="text-sm font-semibold text-slate-500">Capture to Inbox</h2>
            <textarea
              ref={ref}
              value={title}
              onChange={(e) => setTitle(e.target.value)}
              onKeyDown={onKeyDown}
              rows={3}
              placeholder="What's on your mind? (Cmd/Ctrl+Enter to save)"
              data-testid="quick-capture-input"
              className="w-full px-3 py-2 rounded border border-slate-300 dark:border-slate-700 bg-transparent text-sm"
            />
            <label className="flex items-center gap-2 text-xs text-slate-500">
              <span>Due (optional)</span>
              <input
                type="date"
                value={due}
                onChange={(e) => setDue(e.target.value)}
                data-testid="quick-capture-due"
                className="px-2 py-1 rounded border border-slate-300 dark:border-slate-700 bg-transparent text-sm text-slate-800 dark:text-slate-100"
              />
            </label>
            <div className="flex justify-end gap-2">
              <button
                type="button"
                onClick={onClose}
                className="px-3 py-1.5 rounded border border-slate-300 text-slate-700 text-sm"
              >
                Cancel
              </button>
              <button
                type="button"
                onClick={onSubmit}
                disabled={busy || !title.trim()}
                data-testid="quick-capture-submit"
                className="px-3 py-1.5 rounded bg-orenda-600 hover:bg-orenda-700 disabled:opacity-50 text-white text-sm"
              >
                Capture
              </button>
            </div>
          </>
        )}
      </div>
    </div>
  );
}
