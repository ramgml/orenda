// @vitest-environment jsdom
/**
 * registerServiceWorker tests (Task 351).
 *
 * The stale-bundle bug: the SW was registered by a fire-and-forget
 * script, so a deployed update stayed in waiting and the page kept
 * running the old precache. The contract pinned here is about the
 * *wiring*, not the browser SW lifecycle itself:
 *   - the module must import the plugin's register helper
 *     (`virtual:pwa-register`) — autoUpdate mode makes that helper send
 *     SKIP_WAITING to a waiting update and reload once on activation;
 *   - it must be wired on capable contexts (returns true);
 *   - it must no-op (return false, never touch registerSW) without
 *     `navigator.serviceWorker` or on insecure origins, so http LAN
 *     previews and old browsers keep booting without an SW.
 *
 * jsdom (about:blank origin) has neither `serviceWorker` nor a secure
 * context, so the default globalThis covers the negative paths; the
 * positive path installs the missing pieces explicitly.
 */
import { cleanup } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const { registerSWMock } = vi.hoisted(() => ({
  registerSWMock: vi.fn(),
}));

// The plugin's virtual module is resolved by Vite at app build time; in
// vitest it is just the specifier this module imports, so mock it.
vi.mock('virtual:pwa-register', () => ({
  registerSW: registerSWMock,
}));

import { registerServiceWorker } from '@/shared/pwa/registerServiceWorker';

beforeEach(() => {
  vi.clearAllMocks();
});

afterEach(() => {
  cleanup();
});

function resetSWSupport(): void {
  Reflect.deleteProperty(globalThis.navigator, 'serviceWorker');
}

describe('registerServiceWorker', () => {
  it('calls the plugin register helper on a capable secure context', () => {
    jsdomStubs();
    Object.defineProperty(globalThis.navigator, 'serviceWorker', {
      value: { register: vi.fn() },
      configurable: true,
    });
    Object.defineProperty(window, 'isSecureContext', {
      value: true,
      configurable: true,
    });

    expect(registerServiceWorker()).toBe(true);
    expect(registerSWMock).toHaveBeenCalledTimes(1);
  });

  it('passes immediate:true so registration does not wait for load', () => {
    jsdomStubs();
    Object.defineProperty(globalThis.navigator, 'serviceWorker', {
      value: { register: vi.fn() },
      configurable: true,
    });
    Object.defineProperty(window, 'isSecureContext', {
      value: true,
      configurable: true,
    });

    registerServiceWorker();
    expect(registerSWMock.mock.calls[0][0]).toMatchObject({ immediate: true });
  });

  it('returns false without navigator.serviceWorker support', () => {
    jsdomStubs();
    resetSWSupport();
    Object.defineProperty(window, 'isSecureContext', {
      value: true,
      configurable: true,
    });

    expect(registerServiceWorker()).toBe(false);
    expect(registerSWMock).not.toHaveBeenCalled();
  });

  it('returns false on an insecure context (http LAN preview)', () => {
    jsdomStubs();
    Object.defineProperty(globalThis.navigator, 'serviceWorker', {
      value: { register: vi.fn() },
      configurable: true,
    });
    Object.defineProperty(window, 'isSecureContext', {
      value: false,
      configurable: true,
    });

    expect(registerServiceWorker()).toBe(false);
    expect(registerSWMock).not.toHaveBeenCalled();
  });
});

/** jsdom starts insecure and SW-less; align both knobs per test. */
function jsdomStubs(): void {
  resetSWSupport();
  Object.defineProperty(window, 'isSecureContext', {
    value: false,
    configurable: true,
  });
}
