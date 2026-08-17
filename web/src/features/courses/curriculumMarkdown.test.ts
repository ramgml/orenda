/**
 * curriculumMarkdown tests (Phase 30.13).
 *
 * The parser feeds the editor's "Import markdown" flow. Pinned
 * contracts:
 *   1. Happy path: multiple modules with lessons and quizzes.
 *   2. Module description lines (paragraphs under `##`).
 *   3. Lesson content lines (paragraphs under `###`).
 *   4. Both quiz kinds; `|` separator only for exact.
 *   5. Preamble / h1 before the first `##` is ignored.
 *   6. Malformed quiz lines are skipped, never thrown.
 *   7. Blank document → [].
 */
import { describe, expect, it } from 'vitest';

import { parseCurriculumMarkdown } from './curriculumMarkdown';

describe('parseCurriculumMarkdown', () => {
  it('parses a multi-module document with lessons and quizzes', () => {
    const md = [
      '## Basics',
      '### Hello world',
      '- [exact] 2+2? | 4',
      '## Advanced',
      '### Lifetimes',
      '- [open] Explain variance',
    ].join('\n');
    const modules = parseCurriculumMarkdown(md);
    expect(modules).toHaveLength(2);
    expect(modules[0].title).toBe('Basics');
    expect(modules[0].position).toBe(0);
    expect(modules[1].title).toBe('Advanced');
    expect(modules[1].position).toBe(1);
    expect(modules[0].lessons).toHaveLength(1);
    expect(modules[0].lessons[0].title).toBe('Hello world');
    expect(modules[0].lessons[0].quizzes[0]).toMatchObject({
      kind: 'exact',
      question_md: '2+2?',
      expected_md: '4',
      position: 0,
    });
    expect(modules[1].lessons[0].quizzes[0]).toMatchObject({
      kind: 'open',
      question_md: 'Explain variance',
      expected_md: '',
    });
  });

  it('collects module description lines under ##', () => {
    const modules = parseCurriculumMarkdown('## Basics\nFirst line.\nSecond line.\n### L1');
    expect(modules[0].description).toBe('First line.\nSecond line.');
  });

  it('collects lesson content lines under ###', () => {
    const modules = parseCurriculumMarkdown('## M\n### L\nSome body.\nMore body.');
    expect(modules[0].lessons[0].content_md).toBe('Some body.\nMore body.');
    // Content lines must not leak into the module description.
    expect(modules[0].description).toBe('');
  });

  it('trims around the exact-quiz separator', () => {
    const modules = parseCurriculumMarkdown('## M\n### L\n- [exact]   Q text  |  A text  ');
    expect(modules[0].lessons[0].quizzes[0]).toMatchObject({
      question_md: 'Q text',
      expected_md: 'A text',
    });
  });

  it('ignores preamble and h1 before the first ##', () => {
    const md = '# Course title\nSome intro paragraph.\n## Real module\n### Real lesson';
    const modules = parseCurriculumMarkdown(md);
    expect(modules).toHaveLength(1);
    expect(modules[0].title).toBe('Real module');
    expect(modules[0].description).toBe('');
  });

  it('skips malformed quiz lines without throwing', () => {
    const md = [
      '## M',
      '### L',
      '- [exact] no separator here',
      '- [exact] | no question',
      '- [open] valid open question',
    ].join('\n');
    const modules = parseCurriculumMarkdown(md);
    expect(modules[0].lessons[0].quizzes).toHaveLength(1);
    expect(modules[0].lessons[0].quizzes[0].question_md).toBe('valid open question');
  });

  it('returns [] for a blank document', () => {
    expect(parseCurriculumMarkdown('')).toEqual([]);
    expect(parseCurriculumMarkdown('   \n\n  \n')).toEqual([]);
  });

  it('skips orphan lessons/quizzes before any module', () => {
    const md = '### Orphan lesson\n- [open] orphan quiz\n## M\n### L';
    const modules = parseCurriculumMarkdown(md);
    expect(modules).toHaveLength(1);
    expect(modules[0].lessons).toHaveLength(1);
    expect(modules[0].lessons[0].title).toBe('L');
  });

  it('assigns fresh ids and 0-based positions within each parent', () => {
    const modules = parseCurriculumMarkdown('## M\n### A\n### B\n- [open] q1\n- [open] q2');
    const m = modules[0];
    expect(m.id).toBeTruthy();
    expect(m.lessons.map((l) => l.position)).toEqual([0, 1]);
    const b = m.lessons[1];
    expect(b.quizzes.map((q) => q.position)).toEqual([0, 1]);
    // Ids are unique across elements.
    const ids = [m.id, ...m.lessons.map((l) => l.id), ...b.quizzes.map((q) => q.id)];
    expect(new Set(ids).size).toBe(ids.length);
  });
});
