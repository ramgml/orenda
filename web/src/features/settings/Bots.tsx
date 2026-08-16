import { FormEvent, useEffect, useState } from 'react';

import { api } from '@/shared/api/client';

interface Subscription {
  id: string;
  bot_type: string;
  target_address: string;
  events: string[];
  enabled: boolean;
}

const BOT_TYPES = ['console', 'webhook', 'email', 'telegram', 'vk'];
// Phase 10 Test send UI: the dropdown deliberately omits "console" —
// a console bot writes to server stderr and has no user-facing
// signal, so a "test send" through it would look like a silent
// failure. The backend enforces the same exclusion via
// knownTestBotTypes.
const TEST_BOT_TYPES = ['webhook', 'email', 'telegram', 'vk'] as const;
const EVENT_TYPES = [
  'task.review_needed',
  'task.assigned_to_me',
  'mention.created',
  'task.commented',
  'agent.offline',
  'backup.failed',
];

/**
 * /settings/bots — manage bot subscriptions (channel × events).
 *
 * The bot credentials themselves live in config.yaml; this page manages
 * which user/channel receives which events.
 *
 * Phase 22.3 follow-up: Telegram has a one-click "bind via /start"
 * path — the user DMs the bot, gets a 6-char code, and pastes it
 * here. The server resolves the code → chat id and creates the
 * subscription with a sensible default event set (same as the
 * manual form below).
 */
export function BotsSettingsPage(): JSX.Element {
  const [subs, setSubs] = useState<Subscription[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [info, setInfo] = useState<string | null>(null);
  const [creating, setCreating] = useState(false);
  const [botType, setBotType] = useState('webhook');
  const [target, setTarget] = useState('');
  const [selectedEvents, setSelectedEvents] = useState<string[]>(['task.review_needed']);
  // Phase 22.3 follow-up: Telegram bind form state.
  const [bindCode, setBindCode] = useState('');
  const [binding, setBinding] = useState(false);

  // Phase 10 Test send UI: one-off message through any configured bot,
  // independent of the subscription store. Used to verify that bot
  // credentials are wired correctly before binding a subscription.
  const [testBotType, setTestBotType] = useState<(typeof TEST_BOT_TYPES)[number]>('webhook');
  const [testTarget, setTestTarget] = useState('');
  const [testing, setTesting] = useState(false);
  const [testResult, setTestResult] = useState<{
    kind: 'ok' | 'err';
    msg: string;
  } | null>(null);

  async function load(): Promise<void> {
    try {
      const res = await api.listSubscriptions();
      setSubs(res.subscriptions);
      setError(null);
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    }
  }

  useEffect(() => {
    load();
  }, []);

  function toggleEvent(name: string): void {
    setSelectedEvents((cur) =>
      cur.includes(name) ? cur.filter((e) => e !== name) : [...cur, name],
    );
  }

  async function onCreate(e: FormEvent<HTMLFormElement>): Promise<void> {
    e.preventDefault();
    setError(null);
    setInfo(null);
    try {
      await api.createSubscription({
        bot_type: botType,
        target_address: target.trim(),
        events: selectedEvents,
        enabled: true,
      });
      setCreating(false);
      setTarget('');
      setInfo('Subscription added.');
      await load();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  }

  async function onDelete(id: string): Promise<void> {
    try {
      await api.deleteSubscription(id);
      await load();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  }

  // Phase 22.3 follow-up: Telegram bind. We POST the code to
  // /bots/telegram/bind; the server resolves it to a chat id and
  // creates a default subscription. On success we surface the
  // resolved chat id (and username if the bot captured one) so
  // the user has immediate confirmation, then refresh the list.
  async function onBindTelegram(e: FormEvent<HTMLFormElement>): Promise<void> {
    e.preventDefault();
    if (binding || bindCode.length === 0) return;
    setBinding(true);
    setError(null);
    setInfo(null);
    try {
      const r = await api.bindTelegram({ code: bindCode });
      setBindCode('');
      const who = r.username ? ` (@${r.username})` : '';
      setInfo(`Telegram bound to chat ${r.chat_id}${who}. Default event set active.`);
      await load();
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      // The server returns a friendly hint on 503. Surface that
      // instead of the raw JSON to the user.
      if (msg.includes('telegram_bot_not_running')) {
        setError(
          'Telegram bot is not running. Set the token in data/config.yaml and restart the server.',
        );
      } else if (msg.includes('code_expired')) {
        setError('That code expired. Send /start to your bot again to get a fresh code.');
      } else if (msg.includes('code_unknown')) {
        setError(
          'Code not recognised. Double-check the message from your bot, or send /start again.',
        );
      } else {
        setError(msg);
      }
    } finally {
      setBinding(false);
    }
  }

  // Phase 10 Test send UI: deliver a single test message through the
  // chosen bot. The server returns a structured error payload; we
  // pattern-match against `error: '<key>'` to map to a friendly hint
  // without swallowing the raw text (operator wants to see the
  // transport error when something is wrong with the wiring).
  async function onTestSend(e: FormEvent<HTMLFormElement>): Promise<void> {
    e.preventDefault();
    if (testing || testTarget.trim().length === 0) return;
    setTesting(true);
    setTestResult(null);
    try {
      const r = await api.testBot({
        bot_type: testBotType,
        target_address: testTarget.trim(),
      });
      setTestResult({
        kind: 'ok',
        msg: `Sent. If you got a message with "Orenda test message" at ${testTarget.trim()}, the bot is wired correctly.`,
      });
      // r is unused beyond the side effect; keep the call so test
      // closures can spy on it.
      void r;
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err);
      // Server returns {error: ..., hint?: ...} on failure. The
      // axios error message contains the JSON; surface the most
      // useful part.
      const codeMatch = msg.match(/"error"\s*:\s*"([^"]+)"/);
      const hintMatch = msg.match(/"hint"\s*:\s*"([^"]+)"/);
      const code = codeMatch?.[1] ?? 'send_failed';
      const hint = hintMatch?.[1];
      setTestResult({
        kind: 'err',
        msg: hint ? `${code}: ${hint}` : `${code}: ${msg}`,
      });
    } finally {
      setTesting(false);
    }
  }

  return (
    <section className="space-y-4">
      <header className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-semibold">Bot subscriptions</h1>
          <p className="text-sm text-slate-500">
            Which channel receives which events. Bot credentials live in{' '}
            <code className="px-1 bg-slate-100 dark:bg-slate-800 rounded">data/config.yaml</code>.
          </p>
        </div>
        <button
          type="button"
          onClick={() => setCreating((v) => !v)}
          className="px-3 py-1.5 rounded bg-orenda-600 hover:bg-orenda-700 text-white text-sm"
        >
          {creating ? 'Cancel' : 'Add subscription'}
        </button>
      </header>

      {/* Phase 10 Test send UI: deliver a one-off message through any
       * configured bot. Independent of the subscription store — the
       * user can verify wiring before they bind a real subscription.
       * The form is intentionally separate from the "Add subscription"
       * form above so the operator can sanity-check the channel
       * they're about to subscribe to. */}
      <section
        data-testid="bot-test-send"
        className="rounded border border-slate-200 dark:border-slate-800 p-4 bg-white dark:bg-slate-950 space-y-3"
      >
        <div>
          <h2 className="font-semibold">Test send</h2>
          <p className="text-sm text-slate-500">
            Send a one-off message through a configured bot. Use this to verify credentials and
            routing before binding a subscription.
          </p>
        </div>
        <form onSubmit={onTestSend} className="grid sm:grid-cols-3 gap-3 items-end">
          <label className="grid gap-1 text-sm">
            <span className="text-slate-500">Bot type</span>
            <select
              data-testid="bot-test-type"
              value={testBotType}
              onChange={(e) => setTestBotType(e.target.value as typeof testBotType)}
              className="px-3 py-2 rounded border border-slate-300 dark:border-slate-700 bg-transparent"
            >
              {TEST_BOT_TYPES.map((t) => (
                <option key={t} value={t}>
                  {t}
                </option>
              ))}
            </select>
          </label>
          <label className="grid gap-1 text-sm sm:col-span-2">
            <span className="text-slate-500">Target address</span>
            <input
              type="text"
              data-testid="bot-test-target"
              value={testTarget}
              onChange={(e) => setTestTarget(e.target.value)}
              placeholder={targetPlaceholder(testBotType)}
              className="px-3 py-2 rounded border border-slate-300 dark:border-slate-700 bg-transparent"
              required
            />
          </label>
          <button
            type="submit"
            data-testid="bot-test-submit"
            disabled={testing || testTarget.trim().length === 0}
            className="sm:col-span-3 px-3 py-2 rounded bg-orenda-600 hover:bg-orenda-700 disabled:opacity-50 text-white text-sm"
          >
            {testing ? 'Sending…' : 'Send test'}
          </button>
        </form>
        {testResult && testResult.kind === 'ok' && (
          <div
            data-testid="bot-test-result-ok"
            className="rounded border border-green-300 bg-green-50 text-green-800 px-3 py-2 text-sm"
          >
            {testResult.msg}
          </div>
        )}
        {testResult && testResult.kind === 'err' && (
          <div
            data-testid="bot-test-result-err"
            className="rounded border border-red-300 bg-red-50 text-red-800 px-3 py-2 text-sm"
          >
            {testResult.msg}
          </div>
        )}
      </section>

      {/* Phase 22.3 follow-up: Telegram bind handshake. The bot
       * sends a one-shot code on /start; the user pastes it here
       * and the server resolves it to a chat id. */}
      <section
        data-testid="telegram-bind"
        className="rounded border border-slate-200 dark:border-slate-800 p-4 bg-white dark:bg-slate-950 space-y-2"
      >
        <h2 className="font-semibold">Telegram</h2>
        <p className="text-sm text-slate-500">
          Open your Telegram app and message your bot the command{' '}
          <code className="px-1 bg-slate-100 dark:bg-slate-800 rounded">/start</code>. The bot
          replies with a 6-character code; paste it below and hit Bind.
        </p>
        <form onSubmit={onBindTelegram} className="flex gap-2 items-center">
          <input
            type="text"
            data-testid="telegram-bind-input"
            value={bindCode}
            onChange={(e) => setBindCode(e.target.value.toUpperCase().trim())}
            placeholder="ABC123"
            maxLength={6}
            className="font-mono uppercase tracking-widest px-3 py-2 rounded border border-slate-300 dark:border-slate-700 bg-transparent w-32 text-center"
            disabled={binding}
          />
          <button
            type="submit"
            data-testid="telegram-bind-submit"
            disabled={binding || bindCode.length === 0}
            className="px-3 py-2 rounded bg-orenda-600 hover:bg-orenda-700 disabled:opacity-50 text-white text-sm"
          >
            {binding ? 'Binding…' : 'Bind'}
          </button>
        </form>
      </section>

      {error && (
        <div className="rounded border border-red-300 bg-red-50 text-red-800 px-3 py-2 text-sm">
          {error}
        </div>
      )}
      {info && (
        <div className="rounded border border-green-300 bg-green-50 text-green-800 px-3 py-2 text-sm">
          {info}
        </div>
      )}

      {creating && (
        <form
          onSubmit={onCreate}
          className="rounded border border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-950 p-4 grid gap-3"
        >
          <div className="grid sm:grid-cols-2 gap-3">
            <label className="grid gap-1 text-sm">
              <span className="text-slate-500">Bot type</span>
              <select
                data-testid="add-subscription-bot-type"
                value={botType}
                onChange={(e) => setBotType(e.target.value)}
                className="px-3 py-2 rounded border border-slate-300 dark:border-slate-700 bg-transparent"
              >
                {BOT_TYPES.map((t) => (
                  <option key={t} value={t}>
                    {t}
                  </option>
                ))}
              </select>
            </label>
            <label className="grid gap-1 text-sm">
              <span className="text-slate-500">Target address</span>
              <input
                type="text"
                value={target}
                onChange={(e) => setTarget(e.target.value)}
                placeholder={targetPlaceholder(botType)}
                className="px-3 py-2 rounded border border-slate-300 dark:border-slate-700 bg-transparent"
                required
              />
            </label>
          </div>
          <fieldset>
            <legend className="text-sm text-slate-500 mb-1">Events</legend>
            <div className="flex flex-wrap gap-2">
              {EVENT_TYPES.map((ev) => (
                <label
                  key={ev}
                  className={`px-2 py-1 rounded border text-xs cursor-pointer ${
                    selectedEvents.includes(ev)
                      ? 'border-orenda-500 bg-orenda-50 dark:bg-orenda-900/20'
                      : 'border-slate-300 dark:border-slate-700'
                  }`}
                >
                  <input
                    type="checkbox"
                    checked={selectedEvents.includes(ev)}
                    onChange={() => toggleEvent(ev)}
                    className="sr-only"
                  />
                  {ev}
                </label>
              ))}
            </div>
          </fieldset>
          <button
            type="submit"
            className="px-3 py-2 rounded bg-orenda-600 hover:bg-orenda-700 text-white text-sm"
          >
            Add subscription
          </button>
        </form>
      )}

      {subs === null ? (
        <p className="text-slate-500">Loading…</p>
      ) : subs.length === 0 ? (
        <p className="text-slate-500">No subscriptions yet.</p>
      ) : (
        <ul className="divide-y divide-slate-100 dark:divide-slate-800 rounded border border-slate-200 dark:border-slate-800">
          {subs.map((s) => (
            <li key={s.id} className="px-4 py-3 flex items-center justify-between text-sm">
              <div>
                <p className="font-medium">
                  {s.bot_type} → <span className="font-mono text-xs">{s.target_address}</span>
                </p>
                <p className="text-xs text-slate-500">{s.events.join(', ')}</p>
              </div>
              <button
                type="button"
                onClick={() => onDelete(s.id)}
                className="text-red-600 text-xs hover:underline"
              >
                Delete
              </button>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

function targetPlaceholder(botType: string): string {
  switch (botType) {
    case 'webhook':
      return 'https://example.com/hook';
    case 'email':
      return 'you@example.com';
    case 'telegram':
      return '123456789 (chat id)';
    case 'vk':
      return '2000000001 (peer id)';
    default:
      return 'console';
  }
}
