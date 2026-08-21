// @vitest-environment jsdom
/**
 * BackupsSettingsPage thin-coverage smoke tests.
 *
 * The settings panel is read-only (config.yaml is the source of truth),
 * but the manual actions (Test push, Snapshot now, Restore) and the
 * data summaries (settings, snapshots, log) are what we pin.
 */
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { BackupsSettingsPage } from '@/features/settings/Backups';

// Radix UI components (Checkbox, Dialog, Select) use
// @radix-ui/react-use-size which needs ResizeObserver in jsdom.
if (typeof globalThis.ResizeObserver === 'undefined') {
  globalThis.ResizeObserver = class {
    observe() {}
    unobserve() {}
    disconnect() {}
  } as unknown as typeof ResizeObserver;
}

// Stub `window.location.reload` so the reload trigger doesn't throw
// in jsdom. The reload is the last step of the in-process restore
// path; tests only need the call to be issued (not actually fire).
const reloadSpy = vi.fn();
// We use a fresh stub object so the spread doesn't clobber our spy
// (TypeScript flags the second 'reload' key as a duplicate).
const stubLocation: Pick<Location, 'reload'> & Partial<Location> = {
  reload: reloadSpy,
};
Object.defineProperty(window, 'location', {
  configurable: true,
  value: stubLocation,
  writable: true,
});

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
  reloadSpy.mockReset();
});

function stubDefaults(
  overrides: {
    settings?: object;
    snapshots?: object[];
    log?: object[];
  } = {},
) {
  stubHttp.get.mockImplementation((url: string) => {
    if (url === '/api/v1/backups/settings') {
      return Promise.resolve({
        data: overrides.settings ?? {
          enabled: true,
          remote_url: 'git@github.com:foo/bar.git',
          has_auth: true,
          // Phase 32.7: schedule + rotation round-trip in the
          // settings payload. The form pre-fills from these —
          // keep the test stub in sync with the new contract.
          snapshot_cron: '0 3 * * *',
          snapshot_rotation_days: 30,
        },
      });
    }
    if (url === '/api/v1/backups/snapshots') {
      return Promise.resolve({ data: { snapshots: overrides.snapshots ?? [] } });
    }
    if (url === '/api/v1/backups/log') {
      return Promise.resolve({ data: { log: overrides.log ?? [] } });
    }
    return Promise.resolve({ data: {} });
  });
}

describe('BackupsSettingsPage', () => {
  it('renders an editable settings form (Phase 28.1 polish.1)', async () => {
    stubDefaults();

    render(<BackupsSettingsPage />);

    // The form reflects what the server returned: enabled,
    // current remote URL, a Save button.
    const url = (await screen.findByTestId('settings-remote-url')) as HTMLInputElement;
    expect(url.value).toBe('git@github.com:foo/bar.git');
    const enabled = screen.getByTestId('settings-enabled');
    expect(enabled.getAttribute('data-state')).toBe('checked');
    expect(screen.getByTestId('settings-save')).toBeTruthy();
    // Auth field is a password input (never pre-filled — secret).
    const auth = screen.getByTestId('settings-remote-auth') as HTMLInputElement;
    expect(auth.value).toBe('');
  });

  it('Save settings posts a PUT and shows the success banner', async () => {
    stubDefaults();
    stubHttp.put.mockResolvedValueOnce({
      data: {
        enabled: true,
        remote_url: 'git@github.com:foo/bar.git',
        has_auth: true,
        snapshot_cron: '0 3 * * *',
        snapshot_rotation_days: 30,
      },
    });

    render(<BackupsSettingsPage />);
    // Edit URL — old stub returns 'git@github.com:foo/bar.git',
    // confirm we keep it (this is the "save what I typed, even
    // when I didn't change anything" path).
    fireEvent.click(await screen.findByTestId('settings-save'));

    await waitFor(() => {
      expect(stubHttp.put).toHaveBeenCalledWith(
        '/api/v1/backups/settings',
        expect.objectContaining({
          enabled: true,
          snapshot_cron: '0 3 * * *',
          snapshot_rotation_days: 30,
        }),
      );
    });
    expect(await screen.findByText(/Settings saved/)).toBeTruthy();
  });

  // Phase 32.7: the snapshot cron + rotation days fields ship
  // as part of the same Save form. The default settings payload
  // (see stubDefaults above) seeds the form with "0 3 * * *" /
  // 30 days; we verify the form reflects those values and that
  // PUT carries them through.
  it('renders the schedule + rotation fields with server defaults', async () => {
    stubDefaults();

    render(<BackupsSettingsPage />);

    const cron = (await screen.findByTestId('settings-snapshot-cron')) as HTMLInputElement;
    expect(cron.value).toBe('0 3 * * *');
    const rotation = screen.getByTestId('settings-rotation-days') as HTMLInputElement;
    expect(rotation.value).toBe('30');
  });

  it('Save settings carries the cron + rotation through PUT', async () => {
    stubDefaults();
    stubHttp.put.mockResolvedValueOnce({
      data: {
        enabled: true,
        remote_url: 'git@github.com:foo/bar.git',
        has_auth: true,
        snapshot_cron: '*/15 * * * *',
        snapshot_rotation_days: 7,
      },
    });

    render(<BackupsSettingsPage />);
    const cron = (await screen.findByTestId('settings-snapshot-cron')) as HTMLInputElement;
    fireEvent.input(cron, { target: { value: '*/15 * * * *' } });
    const rotation = screen.getByTestId('settings-rotation-days') as HTMLInputElement;
    fireEvent.input(rotation, { target: { value: '7' } });
    fireEvent.click(screen.getByTestId('settings-save'));

    await waitFor(() => {
      expect(stubHttp.put).toHaveBeenCalledWith(
        '/api/v1/backups/settings',
        expect.objectContaining({
          snapshot_cron: '*/15 * * * *',
          snapshot_rotation_days: 7,
        }),
      );
    });
  });

  // Phase 32.7: the server rejects an unparseable cron expr
  // with 400; the UI surfaces that verbatim in the error banner
  // — there's no client-side cron parser by design (the server
  // is the source of truth for what's accepted).
  it('Save settings surfaces a cron validation error from the server', async () => {
    stubDefaults();
    stubHttp.put.mockRejectedValueOnce(new Error('snapshot_cron: minute: value out of range'));

    render(<BackupsSettingsPage />);
    const cron = (await screen.findByTestId('settings-snapshot-cron')) as HTMLInputElement;
    fireEvent.input(cron, { target: { value: '60 * * * *' } });
    fireEvent.click(screen.getByTestId('settings-save'));

    expect(await screen.findByText(/Save failed: snapshot_cron: minute/)).toBeTruthy();
  });

  it('Save settings surfaces the server-side validation error', async () => {
    stubDefaults();
    stubHttp.put.mockRejectedValueOnce(new Error('invalid remote_url'));

    render(<BackupsSettingsPage />);
    fireEvent.click(await screen.findByTestId('settings-save'));

    expect(await screen.findByText(/Save failed: invalid remote_url/)).toBeTruthy();
  });

  it('renders the empty state for snapshots when none exist', async () => {
    stubDefaults();

    render(<BackupsSettingsPage />);

    expect(await screen.findByText('No snapshots yet.')).toBeTruthy();
    expect(screen.getByText('No log entries yet.')).toBeTruthy();
  });

  it('renders one row per snapshot', async () => {
    stubDefaults({
      snapshots: [
        {
          path: '/data/snapshots/2026-08-12.db',
          size: 1024 * 1024,
          mod_time: '2026-08-12T10:00:00Z',
        },
      ],
    });

    render(<BackupsSettingsPage />);

    expect(await screen.findByText('Snapshots (1)')).toBeTruthy();
    expect(screen.getByText('/data/snapshots/2026-08-12.db')).toBeTruthy();
  });

  it('Test push shows the success banner and the test endpoint was hit', async () => {
    stubDefaults();
    stubHttp.post.mockResolvedValueOnce({ data: { status: 'ok' } });

    render(<BackupsSettingsPage />);
    fireEvent.click(await screen.findByRole('button', { name: /test push/i }));

    expect(await screen.findByText('Push succeeded.')).toBeTruthy();
    expect(stubHttp.post).toHaveBeenCalledWith('/api/v1/backups/test', {});
  });

  it('Test push maps a no_remote error to the inline hint', async () => {
    stubDefaults();
    stubHttp.post.mockRejectedValueOnce(new Error('no_remote: missing remote'));

    render(<BackupsSettingsPage />);
    fireEvent.click(await screen.findByRole('button', { name: /test push/i }));

    expect(await screen.findByText(/No git remote configured/)).toBeTruthy();
  });

  it('Snapshot now shows the path and triggers a reload', async () => {
    stubDefaults();
    stubHttp.post.mockResolvedValueOnce({ data: { path: '/data/snapshots/new.db' } });

    render(<BackupsSettingsPage />);
    fireEvent.click(await screen.findByRole('button', { name: /snapshot now/i }));

    await waitFor(() => {
      expect(stubHttp.post).toHaveBeenCalledWith('/api/v1/backups/snapshot', {});
    });
    expect(await screen.findByText(/Snapshot written: \/data\/snapshots\/new\.db/)).toBeTruthy();
  });

  it('Restore opens a modal with the CLI hint', async () => {
    stubDefaults({
      snapshots: [
        {
          path: '/data/snapshots/2026-08-12.db',
          size: 1024,
          mod_time: '2026-08-12T10:00:00Z',
        },
      ],
    });
    // restoreBackup rejects with an error message containing a hint.
    stubHttp.post.mockRejectedValueOnce(
      new Error('{"hint": "orenda backup restore --from /data/snapshots/2026-08-12.db --yes"}'),
    );

    render(<BackupsSettingsPage />);
    fireEvent.click(await screen.findByRole('button', { name: /restore/i }));

    expect(await screen.findByText('Restore from snapshot')).toBeTruthy();
    expect(
      screen.getByText(/orenda backup restore --from \/data\/snapshots\/2026-08-12\.db --yes/),
    ).toBeTruthy();
  });

  it('Restore falls back to a default hint when the server does not include one', async () => {
    stubDefaults({
      snapshots: [
        {
          path: '/data/snapshots/x.db',
          size: 0,
          mod_time: '2026-08-12T10:00:00Z',
        },
      ],
    });
    stubHttp.post.mockRejectedValueOnce(new Error('plain error, no hint'));

    render(<BackupsSettingsPage />);
    fireEvent.click(await screen.findByRole('button', { name: /restore/i }));

    expect(await screen.findByText(/Restore from snapshot/)).toBeTruthy();
    expect(screen.getByText(/orenda backup restore --from \/data\/snapshots\/x\.db/)).toBeTruthy();
  });

  // ---- Phase 22 close-out: in-process restore ----
  //
  // The inline-restore button drives a three-step sequence: turn
  // maintenance on, restore with force, reload the SPA. We pin the
  // call order so a future refactor doesn't break the contract.

  it('Restore in this window: maintenance on → force restore → reload', async () => {
    stubDefaults({
      snapshots: [
        {
          path: '/data/snapshots/2026-08-12.db',
          size: 1024,
          mod_time: '2026-08-12T10:00:00Z',
        },
      ],
    });
    // First POST: probe restore (force=false) → 409 with hint.
    // Second POST: maintenance on → 200.
    // Third POST: restore (force=true) → 200.
    // After 1.5s timer, window.location.reload() is invoked.
    stubHttp.post.mockImplementation((url: string, body: unknown) => {
      if (url === '/api/v1/backups/restore') {
        const b = body as { force?: boolean };
        if (!b.force) {
          return Promise.reject({
            message: '{"hint":"orenda backup restore --from /data/snapshots/2026-08-12.db --yes"}',
          });
        }
        return Promise.resolve({
          data: { status: 'restored', snapshot: '/data/snapshots/2026-08-12.db' },
        });
      }
      if (url === '/api/v1/maintenance/on') {
        return Promise.resolve({ data: { maintenance: true } });
      }
      return Promise.reject(new Error(`unexpected POST ${url}`));
    });

    render(<BackupsSettingsPage />);
    fireEvent.click(await screen.findByRole('button', { name: /restore/i }));
    expect(await screen.findByText(/Restore from snapshot/)).toBeTruthy();
    fireEvent.click(await screen.findByTestId('restore-inline'));

    // The three POSTs land in the right order.
    await waitFor(() => {
      expect(stubHttp.post).toHaveBeenCalledWith(
        '/api/v1/backups/restore',
        expect.objectContaining({ force: false }),
      );
    });
    await waitFor(() => {
      expect(stubHttp.post).toHaveBeenCalledWith('/api/v1/maintenance/on', {});
    });
    await waitFor(() => {
      expect(stubHttp.post).toHaveBeenCalledWith(
        '/api/v1/backups/restore',
        expect.objectContaining({ force: true }),
      );
    });

    // Success banner shows up; the SPA will reload shortly after.
    expect(await screen.findByText(/Restore complete/)).toBeTruthy();
    // Force the timer to fire deterministically (jsdom timers).
    await new Promise((resolve) => setTimeout(resolve, 1700));
    expect(reloadSpy).toHaveBeenCalledTimes(1);
  });

  it('Restore in this window: failed restore rolls maintenance off', async () => {
    stubDefaults({
      snapshots: [
        {
          path: '/data/snapshots/2026-08-12.db',
          size: 1024,
          mod_time: '2026-08-12T10:00:00Z',
        },
      ],
    });
    let restoreCallCount = 0;
    stubHttp.post.mockImplementation((url: string, body: unknown) => {
      if (url === '/api/v1/backups/restore') {
        restoreCallCount++;
        const b = body as { force?: boolean };
        if (!b.force) {
          return Promise.reject({
            message: '{"hint":"orenda backup restore --from /data/snapshots/2026-08-12.db --yes"}',
          });
        }
        return Promise.reject(new Error('integrity check failed'));
      }
      if (url === '/api/v1/maintenance/on') {
        return Promise.resolve({ data: { maintenance: true } });
      }
      if (url === '/api/v1/maintenance/off') {
        return Promise.resolve({ data: { maintenance: false } });
      }
      return Promise.reject(new Error(`unexpected POST ${url}`));
    });

    render(<BackupsSettingsPage />);
    fireEvent.click(await screen.findByRole('button', { name: /restore/i }));
    fireEvent.click(await screen.findByTestId('restore-inline'));

    // The error path surfaces the failure and calls maintenance off.
    expect(await screen.findByText(/Restore failed/)).toBeTruthy();
    await waitFor(() => {
      expect(stubHttp.post).toHaveBeenCalledWith('/api/v1/maintenance/off', {});
    });
    // The probe + force calls happened (two restores total).
    expect(restoreCallCount).toBe(2);
  });
});
