import { useEffect, useState } from 'react';
import type { AxiosError } from 'axios';

import { api } from '@/shared/api/client';
import type { Comment } from '@/shared/api/client';
import { useAuth } from '@/features/auth/AuthContext';
import { Button } from '@/shared/ui/button';
import { Textarea } from '@/shared/ui/textarea';

/**
 * Renders a list of comments with @mentions highlighted and inline
 * editing for the viewer's own comments (Task 112).
 *
 * The component owns a local copy of the list: the parent passes
 * `comments` as the initial value, and a successful PATCH updates the
 * local copy in place — parent state stays untouched (TaskViewBody
 * keeps its own fetch cycle). New props from the parent re-sync the
 * local copy via effect.
 *
 * The mention rendering uses a simple regex for @user:<id> /
 * @agent:<id> tokens (the same syntax the comment_repo extracts).
 */
const MENTION_RE = /@(user|agent):([A-Za-z0-9_-]+)/g;

// Friendly text for the failure statuses the endpoint is specified to
// return (400/403/404); anything else falls back to the status code.
function describeError(status: number | undefined): string {
  switch (status) {
    case 400:
      return 'Комментарий не может быть пустым.';
    case 403:
      return 'Редактировать можно только свои комментарии.';
    case 404:
      return 'Комментарий не найден — возможно, он уже удалён.';
    default:
      return `Не удалось сохранить (ошибка ${status ?? 'сети'}).`;
  }
}

interface EditingState {
  id: string;
  draft: string;
  busy: boolean;
  error: string | null;
}

export function CommentsList({ comments }: { comments: Comment[] }): JSX.Element {
  const { user } = useAuth();
  const [items, setItems] = useState<Comment[]>(comments);
  const [editing, setEditing] = useState<EditingState | null>(null);

  // Re-sync when the parent delivers a fresh list (new comment
  // posted, task refetched, ...). Editing state is preserved: if the
  // comment under edit disappeared, the editor closes.
  useEffect(() => {
    setItems(comments);
    setEditing((cur) => (cur && comments.some((c) => c.id === cur.id) ? cur : null));
  }, [comments]);

  if (items.length === 0) {
    return <p className="text-slate-500 text-sm">No comments yet.</p>;
  }

  const startEdit = (c: Comment): void => {
    setEditing({ id: c.id, draft: c.body_md, busy: false, error: null });
  };

  const saveEdit = (): void => {
    if (!editing) return;
    const body = editing.draft.trim();
    if (!body) {
      setEditing({ ...editing, error: 'Текст не может быть пустым.' });
      return;
    }
    const target = items.find((c) => c.id === editing.id);
    if (!target) {
      setEditing(null);
      return;
    }
    setEditing({ ...editing, busy: true, error: null });
    api
      .updateTaskComment(target.target_id, target.id, body)
      .then((updated) => {
        setItems((cur) => cur.map((c) => (c.id === updated.id ? updated : c)));
        setEditing(null);
      })
      .catch((err: AxiosError) => {
        const status = err.response?.status;
        setEditing({
          id: editing.id,
          draft: editing.draft,
          busy: false,
          error: describeError(status),
        });
      });
  };

  return (
    <ul className="space-y-2">
      {items.map((c) => {
        const isEditing = editing?.id === c.id;
        const canEdit = c.author_type === 'user' && !!user && c.author_id === user.user_id;
        return (
          <li key={c.id} className="rounded border border-border p-2 text-sm">
            <div className="flex items-center gap-2 text-xs text-slate-500 mb-1">
              <span className="font-mono">
                {c.author_type}:{c.author_id.slice(0, 8)}
              </span>
              <span>·</span>
              <span>{c.created_at}</span>
              {c.edited_at && (
                <span
                  className="rounded bg-slate-100 px-1.5 py-0.5 text-[10px] uppercase tracking-wide text-slate-500 dark:bg-slate-800"
                  title={c.edited_at}
                >
                  изменено
                </span>
              )}
              {canEdit && !isEditing && (
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  className="ml-auto h-6 px-2 text-xs"
                  onClick={() => startEdit(c)}
                >
                  Редактировать
                </Button>
              )}
            </div>
            {isEditing && editing ? (
              <div className="space-y-2">
                <Textarea
                  autoFocus
                  value={editing.draft}
                  onChange={(e) => setEditing({ ...editing, draft: e.target.value })}
                  onKeyDown={(e) => {
                    if (e.key === 'Escape') {
                      setEditing(null);
                    }
                  }}
                  className="min-h-[60px]"
                />
                {editing.error && (
                  <p className="text-xs text-red-600" role="alert">
                    {editing.error}
                  </p>
                )}
                <div className="flex gap-2">
                  <Button
                    type="button"
                    size="sm"
                    className="text-xs"
                    disabled={editing.busy || !editing.draft.trim()}
                    onClick={() => void saveEdit()}
                  >
                    {editing.busy ? 'Сохранение…' : 'Сохранить'}
                  </Button>
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    className="text-xs"
                    disabled={editing.busy}
                    onClick={() => setEditing(null)}
                  >
                    Отмена
                  </Button>
                </div>
              </div>
            ) : (
              <p className="whitespace-pre-wrap">{renderBody(c.body_md)}</p>
            )}
          </li>
        );
      })}
    </ul>
  );
}

// renderBody highlights @user:<id> / @agent:<id> tokens with a different
// colour. We keep it as a string-rewrite so the rendered HTML stays
// safe (no raw-HTML injection).
function renderBody(body: string): JSX.Element {
  const parts: Array<string | { kind: string; id: string }> = [];
  let lastIndex = 0;
  for (const m of body.matchAll(MENTION_RE)) {
    if (m.index === undefined) continue;
    if (m.index > lastIndex) {
      parts.push(body.slice(lastIndex, m.index));
    }
    parts.push({ kind: m[1]!, id: m[2]! });
    lastIndex = m.index + m[0].length;
  }
  if (lastIndex < body.length) {
    parts.push(body.slice(lastIndex));
  }
  return (
    <>
      {parts.map((p, i) =>
        typeof p === 'string' ? (
          <span key={i}>{p}</span>
        ) : (
          <span key={i} className="px-1 rounded bg-blue-100 text-blue-800 font-mono text-xs">
            @{p.kind}:{p.id.slice(0, 8)}
          </span>
        ),
      )}
    </>
  );
}
