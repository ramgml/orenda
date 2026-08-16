// @vitest-environment jsdom
/**
 * BotsSettingsPage thin-coverage smoke tests.
 *
 *   - Empty subscriptions state.
 *   - "Add subscription" opens the form with the default event
 *     (task.review_needed) selected.
 *   - Toggling an event checkbox adds/removes it from the list.
 *   - Submitting the form calls createSubscription with the right
 *     payload; the success banner shows up.
 *   - Delete calls deleteSubscription and reloads the list.
 */
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { BotsSettingsPage } from '@/features/settings/Bots';

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

function stubSubs(subs: unknown[] = []) {
  stubHttp.get.mockResolvedValue({ data: { subscriptions: subs } });
}

describe('BotsSettingsPage', () => {
  it('renders the empty state when there are no subscriptions', async () => {
    stubSubs([]);

    render(<BotsSettingsPage />);

    expect(await screen.findByText('No subscriptions yet.')).toBeTruthy();
  });

  it('renders one row per subscription', async () => {
    stubSubs([
      {
        id: 's-1',
        bot_type: 'webhook',
        target_address: 'https://example.com/hook',
        events: ['task.review_needed'],
        enabled: true,
      },
      {
        id: 's-2',
        bot_type: 'email',
        target_address: 'me@x.com',
        events: ['mention.created', 'agent.offline'],
        enabled: true,
      },
    ]);

    render(<BotsSettingsPage />);

    expect(await screen.findByText('https://example.com/hook')).toBeTruthy();
    expect(screen.getByText('me@x.com')).toBeTruthy();
    expect(screen.getByText('mention.created, agent.offline')).toBeTruthy();
  });

  it('opens the create form and pre-selects task.review_needed', async () => {
    stubSubs([]);

    render(<BotsSettingsPage />);

    fireEvent.click(await screen.findByRole('button', { name: /add subscription/i }));

    // The default selection includes task.review_needed.
    const checkbox = screen.getByRole('checkbox', {
      name: /task\.review_needed/,
    }) as HTMLInputElement;
    expect(checkbox.checked).toBe(true);
  });

  it('toggling an event adds and removes it from the selection', async () => {
    stubSubs([]);

    render(<BotsSettingsPage />);
    fireEvent.click(await screen.findByRole('button', { name: /add subscription/i }));

    const mention = screen.getByRole('checkbox', { name: /mention\.created/ }) as HTMLInputElement;
    expect(mention.checked).toBe(false);
    fireEvent.click(mention);
    expect(mention.checked).toBe(true);
    fireEvent.click(mention);
    expect(mention.checked).toBe(false);
  });

  it('submitting the form posts createSubscription with the payload', async () => {
    stubSubs([]);
    stubHttp.post.mockResolvedValueOnce({
      data: {
        id: 's-new',
        bot_type: 'telegram',
        target_address: '123',
        events: ['task.review_needed', 'mention.created'],
        enabled: true,
      },
    });

    render(<BotsSettingsPage />);
    fireEvent.click(await screen.findByRole('button', { name: /add subscription/i }));

    // Change bot type to telegram so the placeholder makes sense.
    // Use a testid because the page now has two bot-type selects
    // (add-subscription form + Test send UI).
    fireEvent.change(screen.getByTestId('add-subscription-bot-type'), {
      target: { value: 'telegram' },
    });
    fireEvent.change(screen.getByPlaceholderText(/chat id/i), { target: { value: '123' } });
    // Add mention.created.
    fireEvent.click(screen.getByRole('checkbox', { name: /mention\.created/ }));
    // Submit (the form has a button labelled "Add subscription" inside).
    const formButton = screen.getAllByRole('button', { name: /add subscription/i })[0];
    fireEvent.click(formButton);

    await waitFor(() => {
      expect(stubHttp.post).toHaveBeenCalledWith('/api/v1/notifications/subscriptions', {
        bot_type: 'telegram',
        target_address: '123',
        events: ['task.review_needed', 'mention.created'],
        enabled: true,
      });
    });
    expect(await screen.findByText('Subscription added.')).toBeTruthy();
  });

  it('surfaces an inline error when createSubscription rejects', async () => {
    stubSubs([]);
    stubHttp.post.mockRejectedValueOnce(new Error('boom'));

    render(<BotsSettingsPage />);
    fireEvent.click(await screen.findByRole('button', { name: /add subscription/i }));
    // Switch to telegram so the target placeholder is "chat id".
    fireEvent.change(screen.getByTestId('add-subscription-bot-type'), {
      target: { value: 'telegram' },
    });
    fireEvent.change(screen.getByPlaceholderText(/chat id/i), { target: { value: 'x' } });

    const formButton = screen.getAllByRole('button', { name: /add subscription/i })[0];
    fireEvent.click(formButton);

    expect(await screen.findByText('boom')).toBeTruthy();
  });

  it('Delete removes a subscription via the api', async () => {
    stubSubs([
      {
        id: 's-1',
        bot_type: 'webhook',
        target_address: 'https://x.com',
        events: ['task.review_needed'],
        enabled: true,
      },
    ]);
    stubHttp.delete.mockResolvedValueOnce({ data: undefined });

    render(<BotsSettingsPage />);
    await screen.findByText('https://x.com');

    fireEvent.click(screen.getByRole('button', { name: /delete/i }));

    await waitFor(() => {
      expect(stubHttp.delete).toHaveBeenCalledWith('/api/v1/notifications/subscriptions/s-1');
    });
  });

  it('surfaces an inline error when listSubscriptions rejects', async () => {
    stubHttp.get.mockRejectedValueOnce(new Error('boom'));

    render(<BotsSettingsPage />);

    expect(await screen.findByText('boom')).toBeTruthy();
  });

  // ---- Phase 22.3 follow-up: Telegram bind handshake ----

  it('Telegram bind: success surfaces the chat id and refreshes the list', async () => {
    stubSubs([]);
    // First POST is /bots/telegram/bind (success); subsequent POSTs
    // are /notifications/subscriptions for the refresh — those are
    // list-only so we just let them return the seed.
    stubHttp.post.mockImplementation((url: string) => {
      if (url === '/api/v1/bots/telegram/bind') {
        return Promise.resolve({
          data: { chat_id: 12345, username: 'alice', subscription_id: 's-bind' },
        });
      }
      return Promise.resolve({ data: { subscriptions: [] } });
    });

    render(<BotsSettingsPage />);
    fireEvent.change(await screen.findByTestId('telegram-bind-input'), {
      target: { value: 'abc123' },
    });
    fireEvent.click(screen.getByTestId('telegram-bind-submit'));

    await waitFor(() => {
      expect(stubHttp.post).toHaveBeenCalledWith('/api/v1/bots/telegram/bind', {
        code: 'ABC123',
      });
    });
    expect(await screen.findByText(/Telegram bound to chat 12345/)).toBeTruthy();
    expect(screen.getByText(/@alice/)).toBeTruthy();
  });

  it('Telegram bind: maps server hints to readable inline errors', async () => {
    stubSubs([]);
    stubHttp.post.mockImplementation((url: string) => {
      if (url === '/api/v1/bots/telegram/bind') {
        return Promise.reject(new Error('{"error":"code_expired"}'));
      }
      return Promise.resolve({ data: { subscriptions: [] } });
    });

    render(<BotsSettingsPage />);
    fireEvent.change(await screen.findByTestId('telegram-bind-input'), {
      target: { value: 'X1' },
    });
    fireEvent.click(screen.getByTestId('telegram-bind-submit'));

    expect(await screen.findByText(/That code expired/)).toBeTruthy();
  });

  it('Telegram bind: surfaces telegram_bot_not_running hint when the bot is offline', async () => {
    stubSubs([]);
    stubHttp.post.mockImplementation((url: string) => {
      if (url === '/api/v1/bots/telegram/bind') {
        return Promise.reject(
          new Error('{"error":"telegram_bot_not_running","hint":"set the token"}'),
        );
      }
      return Promise.resolve({ data: { subscriptions: [] } });
    });

    render(<BotsSettingsPage />);
    fireEvent.change(await screen.findByTestId('telegram-bind-input'), {
      target: { value: 'X1' },
    });
    fireEvent.click(screen.getByTestId('telegram-bind-submit'));

    expect(await screen.findByText(/Telegram bot is not running/)).toBeTruthy();
  });

  it('Telegram bind: input uppercases the code so the user can paste in any case', async () => {
    stubSubs([]);
    stubHttp.post.mockResolvedValueOnce({
      data: { chat_id: 7, subscription_id: 's' },
    });

    render(<BotsSettingsPage />);
    const input = await screen.findByTestId('telegram-bind-input');
    fireEvent.change(input, { target: { value: 'abc123' } });
    fireEvent.click(screen.getByTestId('telegram-bind-submit'));

    await waitFor(() => {
      expect(stubHttp.post).toHaveBeenCalledWith('/api/v1/bots/telegram/bind', {
        code: 'ABC123',
      });
    });
  });

  // ---- Phase 10 (Test send UI): one-off bot message ----

  it('Test send: dropdown omits console (no user-facing signal)', async () => {
    stubSubs([]);

    render(<BotsSettingsPage />);

    const select = (await screen.findByTestId('bot-test-type')) as HTMLSelectElement;
    const options = Array.from(select.options).map((o) => o.value);
    expect(options).toEqual(['webhook', 'email', 'telegram', 'vk']);
    expect(options).not.toContain('console');
  });

  it('Test send: success surfaces the green confirmation banner', async () => {
    stubSubs([]);
    stubHttp.post.mockResolvedValueOnce({
      data: {
        ok: true,
        bot_type: 'webhook',
        target: 'https://example.com/hook',
        sentinel: 'If you got this, your bot is configured correctly.',
        sent_at: '2026-08-14T00:00:00Z',
      },
    });

    render(<BotsSettingsPage />);
    fireEvent.change(await screen.findByTestId('bot-test-target'), {
      target: { value: 'https://example.com/hook' },
    });
    fireEvent.click(screen.getByTestId('bot-test-submit'));

    await waitFor(() => {
      expect(stubHttp.post).toHaveBeenCalledWith('/api/v1/bots/test', {
        bot_type: 'webhook',
        target_address: 'https://example.com/hook',
      });
    });
    expect(await screen.findByTestId('bot-test-result-ok')).toBeTruthy();
  });

  it('Test send: bot_not_running error renders the friendly hint', async () => {
    stubSubs([]);
    stubHttp.post.mockRejectedValueOnce(
      new Error(
        '{"error":"bot_not_running","hint":"Bot telegram is not running. Set its credentials in data/config.yaml and restart the server."}',
      ),
    );

    render(<BotsSettingsPage />);
    fireEvent.change(await screen.findByTestId('bot-test-type'), {
      target: { value: 'telegram' },
    });
    fireEvent.change(screen.getByTestId('bot-test-target'), {
      target: { value: '123456' },
    });
    fireEvent.click(screen.getByTestId('bot-test-submit'));

    expect(await screen.findByTestId('bot-test-result-err')).toBeTruthy();
    expect(await screen.findByText(/bot_not_running/)).toBeTruthy();
  });

  it('Test send: send_failed (502) shows the transport error hint', async () => {
    stubSubs([]);
    stubHttp.post.mockRejectedValueOnce(
      new Error('{"error":"send_failed","hint":"smtp: dial: connection refused"}'),
    );

    render(<BotsSettingsPage />);
    fireEvent.change(await screen.findByTestId('bot-test-type'), {
      target: { value: 'email' },
    });
    fireEvent.change(screen.getByTestId('bot-test-target'), {
      target: { value: 'me@example.com' },
    });
    fireEvent.click(screen.getByTestId('bot-test-submit'));

    expect(await screen.findByTestId('bot-test-result-err')).toBeTruthy();
    expect(await screen.findByText(/smtp: dial/)).toBeTruthy();
  });

  it('Test send: submit is disabled while target is empty', async () => {
    stubSubs([]);

    render(<BotsSettingsPage />);
    const submit = (await screen.findByTestId('bot-test-submit')) as HTMLButtonElement;
    expect(submit.disabled).toBe(true);
  });
});
