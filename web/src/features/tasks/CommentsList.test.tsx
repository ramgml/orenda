// @vitest-environment jsdom
/**
 * Task 114: comment bodies render as Markdown (GFM) instead of raw
 * text.
 *
 * Contracts pinned here (CommentsList):
 *
 *   1. Markdown source (headings, lists, GFM tables, links, code)
 *      comes out as real HTML — raw source must not leak as text.
 *   2. @user:<id> / @agent:<id> mentions keep the pill highlight
 *      (blue pill, id truncated to 8 chars) inside rendered text.
 *   3. Plain-text line breaks survive: a single `\n` renders as
 *      <br/> (ReactMarkdown would collapse it otherwise).
 *   4. Links open in a new tab (target="_blank").
 *   5. Editing (Task 112) is untouched — covered elsewhere; here only
 *      the display branch is exercised.
 */
import { cleanup, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { CommentsList } from '@/features/tasks/CommentsList';
import type { Comment } from '@/shared/api/client';

// AuthContext mock: CommentsList reads `user` to decide which
// comments the viewer may edit (Task 112 branch — not under test
// here, but the hook must resolve).
vi.mock('@/features/auth/AuthContext', () => ({
  useAuth: () => ({ user: { user_id: 'u-me', email: 'me@x.io', display_name: 'Me' } }),
}));

function comment(overrides: Partial<Comment> = {}): Comment {
  return {
    id: 'c-1',
    target_type: 'task',
    target_id: 't-1',
    author_type: 'user',
    author_id: 'u-other',
    body_md: '',
    created_at: '2026-08-31T12:00:00Z',
    ...overrides,
  };
}

afterEach(() => {
  cleanup();
});

describe('CommentsList — markdown display (Task 114)', () => {
  it('renders markdown as HTML (heading, list, table, link, code), not raw source', () => {
    const body = [
      '# Заголовок',
      '',
      '- пункт один',
      '- пункт два',
      '',
      '| a | b |',
      '|---|---|',
      '| 1 | 2 |',
      '',
      '[ссылка](https://example.com)',
      '',
      'инлайн `код` здесь',
    ].join('\n');
    const { container } = render(<CommentsList comments={[comment({ body_md: body })]} />);

    expect(screen.getByRole('heading', { level: 1, name: 'Заголовок' })).toBeTruthy();
    const markdownList = container.querySelector('article ul');
    expect(markdownList).toBeTruthy();
    expect(markdownList?.children).toHaveLength(2);
    expect(markdownList?.children[0]?.textContent).toBe('пункт один');
    expect(markdownList?.children[1]?.textContent).toBe('пункт два');
    // GFM table renders as a real <table>.
    const table = container.querySelector('article table');
    expect(table).toBeTruthy();
    expect(table?.querySelector('th')?.textContent).toBe('a');
    expect(table?.querySelector('tbody td')?.textContent).toBe('1');
    expect(screen.getByRole('link', { name: 'ссылка' })).toBeTruthy();
    expect(container.querySelector('code')?.textContent).toBe('код');
    // The raw markdown source must not leak as plain text.
    expect(screen.queryByText(body)).toBeNull();
  });

  it('highlights @user mentions with the pill inside rendered text', () => {
    render(
      <CommentsList
        comments={[comment({ body_md: 'взял на себя @user:1234567890abcdef, см. деталь' })]}
      />,
    );

    const pill = screen.getByText('@user:12345678');
    expect(pill.tagName).toBe('SPAN');
    expect(pill.className).toContain('bg-blue-100');
    expect(pill.className).toContain('font-mono');
  });

  it('highlights @agent mentions with the truncated id', () => {
    render(<CommentsList comments={[comment({ body_md: 'проверено @agent:abcdef1234567890' })]} />);

    const pill = screen.getByText('@agent:abcdef12');
    expect(pill.className).toContain('bg-blue-100');
  });

  it('keeps plain-text line breaks (single \\n becomes <br/>)', () => {
    const { container } = render(
      <CommentsList comments={[comment({ body_md: 'первая строка\nвторая строка' })]} />,
    );

    expect(container.querySelector('br')).toBeTruthy();
    const p = container.querySelector('article p');
    expect(p?.textContent).toContain('первая строка');
    expect(p?.textContent).toContain('вторая строка');
  });

  it('renders links with target=_blank', () => {
    render(<CommentsList comments={[comment({ body_md: '[docs](https://example.com/docs)' })]} />);

    const link = screen.getByRole('link', { name: 'docs' }) as HTMLAnchorElement;
    expect(link.getAttribute('target')).toBe('_blank');
    expect(link.getAttribute('rel')).toBe('noreferrer noopener');
  });

  it('renders empty body_md as an empty block without raw text', () => {
    const { container } = render(<CommentsList comments={[comment({ body_md: '' })]} />);

    expect(container.querySelector('article')).toBeTruthy();
    expect(container.querySelector('article')?.textContent).toBe('');
    expect(container.textContent).not.toContain('undefined');
  });
});
