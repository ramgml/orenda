// @vitest-environment jsdom
/**
 * Shared UI primitives tests (Phase 28.23).
 *
 * Pins the contracts that matter: text content renders verbatim and
 * the dark: variants are present (the whole point of centralising was
 * the hand-rolled banners being light-only).
 */
import { cleanup, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';

import { EmptyState } from '@/shared/ui/EmptyState';
import { ErrorBanner } from '@/shared/ui/ErrorBanner';
import { Loading } from '@/shared/ui/Loading';

afterEach(() => {
  cleanup();
});

describe('Loading', () => {
  it('renders the default label', () => {
    render(<Loading />);
    expect(screen.getByText('Loading…')).toBeTruthy();
  });

  it('renders a custom label with dark-aware styling', () => {
    render(<Loading label="Fetching tasks…" />);
    const el = screen.getByText('Fetching tasks…');
    expect(el.className).toContain('italic');
    expect(el.className).toContain('dark:text-slate-500');
  });
});

describe('ErrorBanner', () => {
  it('renders the message with role=alert', () => {
    render(<ErrorBanner message="boom" />);
    const el = screen.getByRole('alert');
    expect(el.textContent).toBe('boom');
  });

  it('carries dark variants', () => {
    render(<ErrorBanner message="boom" />);
    const el = screen.getByRole('alert');
    expect(el.className).toContain('dark:bg-red-900/20');
    expect(el.className).toContain('dark:text-red-300');
    expect(el.className).toContain('dark:border-red-800');
  });
});

describe('EmptyState', () => {
  it('renders the message and optional icon', () => {
    render(<EmptyState icon="✓" message="All done." />);
    expect(screen.getByText('All done.')).toBeTruthy();
    expect(screen.getByText('✓')).toBeTruthy();
  });

  it('omits the icon block when not provided', () => {
    const { container } = render(<EmptyState message="Nothing here." />);
    expect(container.querySelector('.text-2xl')).toBeNull();
  });

  it('carries dark variants', () => {
    const { container } = render(<EmptyState message="Nothing here." />);
    const el = container.firstElementChild;
    expect(el?.className).toContain('border-border');
    expect(el?.className).toContain('dark:text-slate-500');
  });
});
