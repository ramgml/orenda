import { useCallback, useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';

import { Dialog, DialogContent } from '@/shared/ui/dialog';
import { api, type Task } from '@/shared/api/client';
import { Button } from '@/shared/ui/button';
import { Input } from '@/shared/ui/input';
import { Textarea } from '@/shared/ui/textarea';

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
 * Phase 32.13 (shadcn/ui migration): the overlay is now a
 * `@radix-ui/react-dialog` Dialog primitive — it portals to
 * `document.body` itself (so the kanban `overflow:hidden` doesn't
 * clip it) and handles Esc / click-outside / focus trap / focus
 * return. We keep the global 'q' / Cmd+K hotkey to OPEN the modal;
 * close is delegated to the Dialog's `onOpenChange`.
 */
export function QuickCapture() {
  const [open, setOpen] = useState(false);
  const [title, setTitle] = useState('');
  // Phase 30.10: optional due date. Stored as YYYY-MM-DD from the
  // <input type="date">; converted to a local-midnight ISO string at
  // submit so the backend receives a parseable timestamp without
  // surprise from local-vs-UTC drift.
  const [dueDate, setDueDate] = useState('');
  const [busy, setBusy] = useState(false);
  const [created, setCreated] = useState<Task | null>(null);
  const navigate = useNavigate();

  const close = useCallback(() => {
    setOpen(false);
    setTitle('');
    setDueDate('');
  }, []);

  // Global hotkey: 'q' or Cmd/Ctrl+K. Skip when the user is typing
  // inside an input/textarea/contenteditable — those keystrokes
  // belong to the field, not us. Esc-while-open is handled by the
  // Dialog primitive, so this listener only owns the open trigger.
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
      }
    }
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, []);

  // Cmd/Ctrl+Enter submits from inside the textarea.
  // Esc-while-inside is handled by the Dialog primitive.
  const onKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>): void => {
    if ((e.ctrlKey || e.metaKey) && e.key === 'Enter') {
      e.preventDefault();
      void submit();
    }
  };

  async function submit(): Promise<void> {
    const t = title.trim();
    if (!t || busy) return;
    setBusy(true);
    try {
      // Phase 30.10: optional due_at. Local-midnight → ISO 8601 —
      // date-only inputs are TZ-naive so the operator's "tomorrow"
      // resolves to tomorrow in their browser, not UTC+0. The
      // server stores the raw string and the calendar renders it as
      // an all-day deadline (Phase 30.8).
      const due_at = dueDate ? new Date(`${dueDate}T00:00:00`).toISOString() : undefined;
      const created = await api.createInboxTask({ title: t, due_at });
      setCreated(created);
      setTitle('');
      setDueDate('');
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
      <Dialog
        open={open}
        onOpenChange={(o) => {
          if (!o) close();
        }}
      >
        <DialogContent
          aria-label="Quick capture"
          className="w-full max-w-lg gap-3 p-4 sm:rounded-lg"
          onOpenAutoFocus={(e) => {
            // Phase 32.13: pre-migration, a ref-callback on the
            // textarea called `el.focus()` on mount. Radix's
            // FocusScope otherwise focuses the first focusable
            // element — which is the hidden × button inside
            // DialogContent — so the hotkey flow 'q → start typing'
            // would lose the caret. Keep the textarea first.
            e.preventDefault();
            const ta = document.querySelector<HTMLTextAreaElement>(
              '[data-testid="quick-capture-input"]',
            );
            ta?.focus();
          }}
        >
          {created ? (
            <div className="space-y-3" data-testid="quick-capture-toast">
              <p className="text-sm text-emerald-700">✓ Captured to Inbox</p>
              <p className="text-foreground dark:text-foreground">{created.title}</p>
              <div className="flex gap-2">
                <Button size="sm" onClick={openTaskAndClose} className="px-3 py-1.5 h-auto text-sm">
                  Open task
                </Button>
                <Button
                  variant="outline"
                  size="sm"
                  onClick={dismissToast}
                  className="px-3 py-1.5 h-auto text-sm"
                >
                  Dismiss
                </Button>
              </div>
            </div>
          ) : (
            <div className="space-y-3">
              <h2 className="text-sm font-semibold text-slate-500">Capture to Inbox</h2>
              <Textarea
                value={title}
                onChange={(e) => setTitle(e.target.value)}
                onKeyDown={onKeyDown}
                rows={3}
                placeholder="What's on your mind? (Cmd/Ctrl+Enter to save)"
                data-testid="quick-capture-input"
                className="text-sm"
              />
              {/* Phase 30.10: optional due date. Tab order keeps title
                  first, then due, then buttons — the hotkey flow stays
                  one keystroke from thinking to "save". */}
              <Input
                type="date"
                value={dueDate}
                onChange={(e) => setDueDate(e.target.value)}
                data-testid="quick-capture-due"
                aria-label="Optional due date"
                className="text-sm"
              />
              <div className="flex justify-end gap-2">
                <Button
                  variant="outline"
                  size="sm"
                  onClick={close}
                  className="px-3 py-1.5 h-auto text-sm"
                >
                  Cancel
                </Button>
                <Button
                  size="sm"
                  onClick={() => void submit()}
                  disabled={busy || !title.trim()}
                  data-testid="quick-capture-submit"
                  className="px-3 py-1.5 h-auto text-sm"
                >
                  Capture
                </Button>
              </div>
            </div>
          )}
        </DialogContent>
      </Dialog>
    </>
  );
}
