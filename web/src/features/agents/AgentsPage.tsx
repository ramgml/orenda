import { FormEvent, KeyboardEvent, useEffect, useState } from 'react';

import { useAuth } from '@/features/auth/AuthContext';
import { api, type Agent } from '@/shared/api/client';
import { Button } from '@/shared/ui/button';
import { Input } from '@/shared/ui/input';

/**
 * /agents — list registered agents + create new ones.
 *
 * Phase 3 ships the minimum: a list with online/offline status, a form
 * to create a new agent (the plaintext token is shown exactly once),
 * and a delete button. Agent-token namespace endpoints (claim/heartbeat)
 * live under /api/v1/agent/* and are documented in the API client.
 *
 * Phase 28.19: an agent's `type` is now a free-form, operator-curated
 * set of labels (e.g. ["qwen"], ["qwen","installer"]). The create form
 * accepts any combination via a chips input; the table column renders
 * each label as its own chip; the table filter narrows the list to
 * agents that carry at least one of the selected labels (OR semantics,
 * matching the server filter).
 */
export function AgentsPage(): JSX.Element {
  const { user } = useAuth();
  const [agents, setAgents] = useState<Agent[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const [createdToken, setCreatedToken] = useState<string | null>(null);

  // Create-form labels (free-form, normalised on the server).
  const [labelDraft, setLabelDraft] = useState('');
  const [pendingLabels, setPendingLabels] = useState<string[]>([]);

  // List filter (chips over the table; empty = no filter).
  const [filterLabels, setFilterLabels] = useState<string[]>([]);
  const [filterDraft, setFilterDraft] = useState('');

  async function load(filter: string[]): Promise<void> {
    try {
      const list = await api.listAgents(filter.length > 0 ? { type: filter } : undefined);
      setAgents(list);
      setError(null);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }

  useEffect(() => {
    void load(filterLabels);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [filterLabels]);

  async function onCreate(e: FormEvent<HTMLFormElement>): Promise<void> {
    e.preventDefault();
    if (!name.trim()) return;
    try {
      const { agent, plain_token } = await api.createAgent({
        name: name.trim(),
        type: pendingLabels,
        description: description.trim() || undefined,
      });
      setCreatedToken(plain_token);
      setAgents((prev) => (prev ? [agent, ...prev] : [agent]));
      setName('');
      setDescription('');
      setPendingLabels([]);
      setLabelDraft('');
      setCreating(false);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  }

  async function onDelete(id: string): Promise<void> {
    if (!confirm('Delete this agent? Its API token stops working immediately.')) return;
    try {
      await api.deleteAgent(id);
      setAgents((prev) => (prev ?? []).filter((a) => a.id !== id));
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  }

  // Chip-input handlers (shared between the create form and the
  // table filter — both are free-form label sets).
  function commitLabel(
    draft: string,
    setDraft: (v: string) => void,
    setChips: (updater: (prev: string[]) => string[]) => void,
  ): void {
    const label = draft.trim().toLowerCase();
    if (!label) return;
    setChips((prev) => (prev.includes(label) ? prev : [...prev, label]));
    setDraft('');
  }

  function onLabelKey(
    e: KeyboardEvent<HTMLInputElement>,
    draft: string,
    setDraft: (v: string) => void,
    setChips: (updater: (prev: string[]) => string[]) => void,
  ): void {
    if (e.key === 'Enter' || e.key === ',') {
      e.preventDefault();
      commitLabel(draft, setDraft, setChips);
      return;
    }
    // Backspace on an empty input pops the last chip — common UX
    // for tag inputs (lets users undo a mistake without grabbing
    // the mouse).
    if (e.key === 'Backspace' && draft === '') {
      setChips((prev) => prev.slice(0, -1));
    }
  }

  return (
    <section>
      <header className="flex items-center justify-between mb-4">
        <div>
          <h1 className="text-2xl font-semibold">Agents</h1>
          <p className="text-sm text-slate-500">
            AI clients that work on tasks via the API.
            {user ? ` Signed in as ${user.email}.` : ''}
          </p>
        </div>
        <Button type="button" onClick={() => setCreating((v) => !v)} variant="default" size="sm">
          {creating ? 'Cancel' : 'New agent'}
        </Button>
      </header>

      {error && (
        <div className="mb-4 rounded border border-red-300 bg-red-50 text-red-800 px-3 py-2 text-sm">
          {error}
        </div>
      )}

      {createdToken && (
        <div className="mb-4 rounded border border-amber-300 bg-amber-50 text-amber-900 px-4 py-3 text-sm">
          <p className="font-semibold mb-1">API token — copy now, it won't be shown again.</p>
          <code className="block px-2 py-1 bg-white rounded text-xs font-mono break-all">
            {createdToken}
          </code>
          <Button
            type="button"
            onClick={() => setCreatedToken(null)}
            variant="ghost"
            size="sm"
            className="mt-2 text-xs underline"
          >
            Dismiss
          </Button>
        </div>
      )}

      {creating && (
        <form
          onSubmit={onCreate}
          className="mb-4 p-4 rounded border border-border bg-background dark:bg-background grid gap-2"
        >
          <Input
            type="text"
            placeholder="Agent name (unique)"
            value={name}
            onChange={(e) => setName(e.target.value)}
            autoFocus
            required
          />
          <ChipsInput
            placeholder="Labels (press Enter to add, e.g. qwen, claude, installer)"
            draft={labelDraft}
            onDraftChange={setLabelDraft}
            chips={pendingLabels}
            onChipsChange={setPendingLabels}
            onKeyDown={(e) => onLabelKey(e, labelDraft, setLabelDraft, setPendingLabels)}
          />
          <Input
            type="text"
            placeholder="Description (optional)"
            value={description}
            onChange={(e) => setDescription(e.target.value)}
          />
          <Button type="submit" variant="default" size="sm">
            Create
          </Button>
        </form>
      )}

      <div className="mb-3">
        <label className="text-xs uppercase tracking-wide text-slate-500 block mb-1">
          Filter by label (OR — agents carrying any of these show up)
        </label>
        <ChipsInput
          placeholder="Type a label and press Enter…"
          draft={filterDraft}
          onDraftChange={setFilterDraft}
          chips={filterLabels}
          onChipsChange={setFilterLabels}
          onKeyDown={(e) => onLabelKey(e, filterDraft, setFilterDraft, setFilterLabels)}
        />
      </div>

      {agents === null || agents === undefined ? (
        <p className="text-slate-500">Loading…</p>
      ) : agents.length === 0 ? (
        <p className="text-slate-500">
          {filterLabels.length > 0
            ? `No agents match the label filter (${filterLabels.join(', ')}).`
            : 'No agents yet. Create one to issue an API token.'}
        </p>
      ) : (
        <table className="w-full text-sm">
          <thead>
            <tr className="text-left text-slate-500 border-b border-border">
              <th className="py-2">Name</th>
              <th>Labels</th>
              <th>Status</th>
              <th>Last seen</th>
              <th>Created</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {agents.map((a) => (
              <tr key={a.id} className="border-b border-border dark:border-border">
                <td className="py-2 font-mono">{a.name}</td>
                <td>
                  {a.type.length === 0 ? (
                    <span className="text-slate-400">—</span>
                  ) : (
                    <span className="inline-flex flex-wrap gap-1">
                      {a.type.map((l) => (
                        <span
                          key={l}
                          className="inline-block px-1.5 py-0.5 rounded bg-slate-100 text-slate-700 dark:bg-slate-800 dark:text-slate-200 text-xs font-mono"
                          title={`Label: ${l}`}
                        >
                          {l}
                        </span>
                      ))}
                    </span>
                  )}
                </td>
                <td>
                  <span
                    className={`inline-block px-2 py-0.5 rounded text-xs ${
                      a.status === 'online'
                        ? 'bg-green-100 text-green-800'
                        : a.status === 'disabled'
                          ? 'bg-slate-200 text-slate-600'
                          : 'bg-amber-100 text-amber-800'
                    }`}
                  >
                    {a.status}
                  </span>
                </td>
                <td>{a.last_seen_at ?? '—'}</td>
                <td className="text-slate-500 text-xs">{a.created_at}</td>
                <td>
                  <Button
                    type="button"
                    onClick={() => onDelete(a.id)}
                    variant="ghost"
                    size="sm"
                    className="text-red-600 text-xs hover:underline"
                  >
                    Delete
                  </Button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </section>
  );
}

interface ChipsInputProps {
  placeholder: string;
  draft: string;
  onDraftChange: (v: string) => void;
  chips: string[];
  onChipsChange: (updater: (prev: string[]) => string[]) => void;
  onKeyDown: (e: KeyboardEvent<HTMLInputElement>) => void;
}

/**
 * ChipsInput — free-form tag entry. Phase 28.19 replaces the old
 * three-option `<select>` with a text field + Enter/comma commit +
 * Backspace pop + per-chip remove button. Both the create form and
 * the table filter use this primitive.
 */
function ChipsInput(props: ChipsInputProps): JSX.Element {
  return (
    <div className="flex flex-wrap items-center gap-1 px-2 py-1.5 rounded border border dark:border-border bg-transparent focus-within:border-orenda-500">
      {props.chips.map((label) => (
        <span
          key={label}
          className="inline-flex items-center gap-1 px-2 py-0.5 rounded bg-orenda-100 text-orenda-900 dark:bg-orenda-900 dark:text-orenda-100 text-xs font-mono"
          data-testid="label-chip"
        >
          {label}
          <button
            type="button"
            onClick={() => props.onChipsChange((prev) => prev.filter((l) => l !== label))}
            className="opacity-70 hover:opacity-100"
            aria-label={`Remove ${label}`}
          >
            ×
          </button>
        </span>
      ))}
      <input
        type="text"
        value={props.draft}
        placeholder={props.chips.length === 0 ? props.placeholder : ''}
        onChange={(e) => props.onDraftChange(e.target.value)}
        onKeyDown={props.onKeyDown}
        className="flex-1 min-w-[8rem] px-1 py-0.5 bg-transparent outline-none text-sm"
      />
    </div>
  );
}
