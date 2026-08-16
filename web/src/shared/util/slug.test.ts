import { describe, expect, it } from 'vitest';

import { slugify } from '@/shared/util/slug';

describe('slugify', () => {
  it('transliterates a Russian title', () => {
    expect(slugify('Моя первая страница')).toBe('moya-pervaya-stranitsa');
  });

  it('transliterates digraphs and soft/hard signs', () => {
    expect(slugify('Чаща щёки')).toBe('chashcha-shchyoki');
    expect(slugify('Чаща')).toBe('chashcha');
    expect(slugify('подъезд')).toBe('podezd');
  });

  it('lowercases and strips punctuation', () => {
    expect(slugify('Hello, World!')).toBe('hello-world');
    expect(slugify('  Spaces   Everywhere  ')).toBe('spaces-everywhere');
  });

  it('collapses repeated separators', () => {
    // -/- runs collapse into a single separator.
    expect(slugify('a -- b')).toBe('a-b');
    // Emoji / punctuation runs collapse too — kept as a single "-"
    // so the slug stays readable.
    expect(slugify('a!!!b')).toBe('a-b');
  });

  it('handles Ukrainian/Belarusian letters', () => {
    expect(slugify('Їжак')).toBe('yizhak');
    expect(slugify('Єнот')).toBe('yenot');
  });

  it('handles western diacritics', () => {
    expect(slugify('naïve café')).toBe('naive-cafe');
    expect(slugify('Zürich')).toBe('zurich');
  });

  it('falls back when the title has no latin-friendly chars', () => {
    const slug = slugify('日本語');
    expect(slug).toMatch(/^page-\d+$/);
  });

  it('handles empty input', () => {
    const slug = slugify('');
    expect(slug).toMatch(/^page-\d+$/);
  });

  it('passes ASCII alphanumerics through unchanged', () => {
    expect(slugify('Phase 10')).toBe('phase-10');
    expect(slugify('release-notes')).toBe('release-notes');
  });
});
