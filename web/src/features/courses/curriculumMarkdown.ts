import type { EditorModule, EditorQuiz } from './CourseCurriculumEditor';

/**
 * Phase 30.13: markdown → curriculum parser for the editor's
 * "Import markdown" flow. Pure function, no I/O.
 *
 * Format:
 *   # Course title            — h1 (and any text before the first
 *                               `##`) is preamble and is ignored.
 *   ## Module title           — starts a module. Plain paragraph
 *                               lines directly under it (before the
 *                               first `###`) become its description.
 *   ### Lesson title          — starts a lesson inside the current
 *                               module. Following paragraph lines
 *                               accumulate into its `content_md`.
 *   - [exact] question | expected answer
 *                             — exact quiz under the current lesson.
 *   - [open] question         — open quiz (no expected answer).
 *
 * Rules:
 *   - Every element gets a fresh `crypto.randomUUID()` id and a
 *     0-based `position` within its parent (matching how the editor
 *     maps positions at save time).
 *   - Blank/whitespace-only document → `[]`.
 *   - Headings deeper than `###` are ignored entirely.
 *   - Lessons before the first `##` and quizzes before the first
 *     `###` are skipped (no orphan elements).
 *   - Empty module/lesson titles are skipped.
 *   - Malformed quiz lines (e.g. `[exact]` without `|`) are skipped,
 *     never thrown.
 */
export function parseCurriculumMarkdown(md: string): EditorModule[] {
  const modules: EditorModule[] = [];
  let currentModule: EditorModule | null = null;
  let currentLesson: EditorModule['lessons'][number] | null = null;

  const appendLine = (line: string): void => {
    if (currentLesson) {
      currentLesson.content_md = currentLesson.content_md
        ? `${currentLesson.content_md}\n${line}`
        : line;
    } else if (currentModule) {
      currentModule.description = currentModule.description
        ? `${currentModule.description}\n${line}`
        : line;
    }
    // Preamble (before the first ##) is ignored.
  };

  for (const rawLine of md.split('\n')) {
    const line = rawLine.trim();
    if (!line) continue;

    if (line.startsWith('### ')) {
      const title = line.slice(4).trim();
      if (!currentModule || !title) continue;
      currentLesson = {
        id: crypto.randomUUID(),
        title,
        position: currentModule.lessons.length,
        content_md: '',
        quizzes: [],
      };
      currentModule.lessons.push(currentLesson);
      continue;
    }

    if (line.startsWith('## ')) {
      const title = line.slice(3).trim();
      if (!title) continue;
      currentModule = {
        id: crypto.randomUUID(),
        title,
        description: '',
        position: modules.length,
        lessons: [],
      };
      modules.push(currentModule);
      currentLesson = null;
      continue;
    }

    // Any other heading (h1, h4+) is ignored — h1 is the course
    // title / preamble, deeper headings are not part of the format.
    if (line.startsWith('#')) continue;

    const quizMatch = /^- \[(exact|open)\]\s+(.*)$/.exec(line);
    if (quizMatch) {
      if (!currentLesson) continue;
      const kind = quizMatch[1] as EditorQuiz['kind'];
      const rest = quizMatch[2];
      let question = rest.trim();
      let expected = '';
      if (kind === 'exact') {
        const sep = rest.indexOf('|');
        if (sep < 0) continue; // malformed: exact quiz needs `|`
        question = rest.slice(0, sep).trim();
        expected = rest.slice(sep + 1).trim();
      }
      if (!question) continue;
      currentLesson.quizzes.push({
        id: crypto.randomUUID(),
        position: currentLesson.quizzes.length,
        question_md: question,
        expected_md: expected,
        kind,
      });
      continue;
    }

    appendLine(line);
  }

  return modules;
}
