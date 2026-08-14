// @vitest-environment jsdom
/**
 * Tests for `useBodyScrollLock`.
 *
 * The hook mounts/unmounts via `renderHook`, mutating the real
 * `document.body.style.overflow` on the jsdom body. We snapshot the
 * body's inline style in `beforeEach`/`afterEach` so the test never
 * leaks state into other specs (the body is a global, exactly the
 * kind of side-effect that bites you in CI two weeks later).
 */
import { act, cleanup, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';

import { useBodyScrollLock } from '@/shared/hooks/useBodyScrollLock';

function snapshotBodyOverflow(): string {
  return document.body.style.overflow;
}

describe('useBodyScrollLock', () => {
  beforeEach(() => {
    // Wipe inline overflow so each test starts from "no lock".
    document.body.style.overflow = '';
  });

  afterEach(() => {
    // renderHook owns no DOM of its own, but cleanup() matches the
    // convention in our component tests — keeps a future copy-paste
    // from inheriting a dirty document.
    cleanup();
    // Belt-and-braces: every test must restore via its own unmount,
    // but if an `expect` throws in the middle of a test we still
    // don't want a stray 'hidden' on the next test.
    document.body.style.overflow = '';
  });

  it('sets body overflow to hidden on mount', () => {
    expect(snapshotBodyOverflow()).toBe('');
    renderHook(() => useBodyScrollLock());
    expect(snapshotBodyOverflow()).toBe('hidden');
  });

  it('restores the previous overflow on unmount', () => {
    document.body.style.overflow = '';
    const { unmount } = renderHook(() => useBodyScrollLock());
    expect(snapshotBodyOverflow()).toBe('hidden');
    unmount();
    expect(snapshotBodyOverflow()).toBe('');
  });

  it('restores a non-empty previous overflow verbatim (does not clobber legitimate inline style)', () => {
    // A page-level inline overflow like 'clip' would be silently
    // reset to '' if we hard-coded the restore value — that would
    // surprise a host that intentionally set it. We snapshot and
    // restore.
    document.body.style.overflow = 'clip';
    const { unmount } = renderHook(() => useBodyScrollLock());
    expect(snapshotBodyOverflow()).toBe('hidden');
    unmount();
    expect(snapshotBodyOverflow()).toBe('clip');
  });

  it('stays locked when the same hook instance re-renders with a different dep (id change inside the modal)', () => {
    // Phase 28.3 doc-comment contract: the lock keys on mount, not
    // on the caller's state. Even if the consuming component
    // re-renders (e.g. user navigates from one /tasks/:id to
    // another while the modal stays mounted), the body's overflow
    // must stay 'hidden' until the hook is unmounted.
    const { rerender, unmount } = renderHook(
      ({ tick }: { tick: number }) => {
        useBodyScrollLock();
        return tick;
      },
      { initialProps: { tick: 0 } },
    );
    expect(snapshotBodyOverflow()).toBe('hidden');
    act(() => {
      rerender({ tick: 1 });
    });
    expect(snapshotBodyOverflow()).toBe('hidden');
    act(() => {
      rerender({ tick: 2 });
    });
    expect(snapshotBodyOverflow()).toBe('hidden');
    unmount();
    expect(snapshotBodyOverflow()).toBe('');
  });
});
