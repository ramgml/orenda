import type { Comment } from '@/shared/api/client';

/**
 * Renders a list of comments with @mentions highlighted.
 *
 * Phase 3 ships read-only display; inline editing/reactions land in
 * Phase 4. The mention rendering uses a simple regex for @user:<id> /
 * @agent:<id> tokens (the same syntax the comment_repo extracts).
 */
const MENTION_RE = /@(user|agent):([A-Za-z0-9_-]+)/g;

export function CommentsList({ comments }: { comments: Comment[] }): JSX.Element {
  if (comments.length === 0) {
    return <p className="text-slate-500 text-sm">No comments yet.</p>;
  }
  return (
    <ul className="space-y-2">
      {comments.map((c) => (
        <li key={c.id} className="rounded border border-border p-2 text-sm">
          <div className="flex items-center gap-2 text-xs text-slate-500 mb-1">
            <span className="font-mono">
              {c.author_type}:{c.author_id.slice(0, 8)}
            </span>
            <span>·</span>
            <span>{c.created_at}</span>
          </div>
          <p className="whitespace-pre-wrap">{renderBody(c.body_md)}</p>
        </li>
      ))}
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
