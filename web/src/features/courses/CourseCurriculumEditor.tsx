import { useCallback, useEffect, useMemo, useState } from 'react';
import {
  DndContext,
  DragEndEvent,
  DragOverEvent,
  PointerSensor,
  closestCorners,
  useSensor,
  useSensors,
} from '@dnd-kit/core';
import {
  SortableContext,
  arrayMove,
  useSortable,
  verticalListSortingStrategy,
} from '@dnd-kit/sortable';
import { CSS } from '@dnd-kit/utilities';

import { api, type Course } from '@/shared/api/client';
import { Button } from '@/shared/ui/button';
import { Input } from '@/shared/ui/input';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/shared/ui/select';
import { Textarea } from '@/shared/ui/textarea';

import { parseCurriculumMarkdown } from './curriculumMarkdown';

/**
 * Phase 27.6: inline curriculum editor for the owner.
 *
 * Phase 30.13: two save modes.
 *
 *   - Swap mode (draft/review): the agent's curriculum swap endpoint
 *     is reused verbatim — same shape, same atomicity. The swap is
 *     destructive (rows are deleted and reinserted), which is fine
 *     before the course goes live because there is no progress yet.
 *   - Granular mode (active): saving diffs the local tree against
 *     the initial tree and issues the granular endpoints (create /
 *     rename / delete module|lesson|quiz, content PUT, and a final
 *     IDs-only structure reorder when positions shifted). Stable
 *     server IDs are preserved so student progress (lesson status,
 *     task links) survives the edit. Granular edits apply one row at
 *     a time, so a mid-plan failure leaves the course PARTIALLY
 *     edited by design — the error handler still calls onSaved() so
 *     the parent reloads ground truth and the editor can't drift.
 *
 * The shape of the editor's local state is intentionally close to
 * the wire payload: each element has a client-side id (uuid) so
 * reorders/removals stay referentially stable while the user is
 * typing. New elements get fresh uuids; existing elements keep their
 * server IDs — membership in the initial id sets is how the diff
 * tells creates from updates.
 *
 * Why a separate component: the editor has its own state lifecycle
 * (dirty / saving / error) and is reused only by CourseDetailPage,
 * but pulling it out keeps that file readable. The component is
 * presentational — it does no fetching; the parent supplies the
 * initial tree and is called back on success.
 */
export interface EditorQuiz {
  id: string;
  position: number;
  question_md: string;
  expected_md: string;
  kind: 'exact' | 'open';
}

export interface EditorLesson {
  id: string;
  title: string;
  position: number;
  content_md: string;
  quizzes: EditorQuiz[];
}

export interface EditorModule {
  id: string;
  title: string;
  description: string;
  position: number;
  lessons: EditorLesson[];
}

/**
 * Phase 30.13: the granular save plan — what changed between the
 * initial tree (server state at editor mount) and the local tree.
 */
export interface DiffPlan {
  /** Existing modules whose title/description changed. */
  moduleUpdates: { id: string; title: string; description: string }[];
  /** Modules with a local-only id (not in the initial tree). */
  moduleCreates: EditorModule[];
  /** Initial module ids missing from the current tree. */
  moduleDeletes: string[];
  /** Existing lessons whose title changed. */
  lessonUpdates: { id: string; title: string }[];
  /** Existing lessons whose content_md changed (content endpoint). */
  lessonContentUpdates: { id: string; content_md: string }[];
  /** New lessons plus the LOCAL id of their current module — the
   *  final server module id is resolved at execution time. */
  lessonCreates: { lesson: EditorLesson; moduleId: string }[];
  lessonDeletes: string[];
  quizUpdates: { id: string; question_md: string; expected_md: string; kind: 'exact' | 'open' }[];
  /** New quizzes plus the LOCAL id of their lesson. */
  quizCreates: { quiz: EditorQuiz; lessonId: string }[];
  quizDeletes: string[];
  /** True when the structure endpoint must run: module order
   *  changed, a lesson moved module or changed order within one, or
   *  any module/lesson was created or deleted (positions shift).
   *  Pure quiz changes never need it — quizzes aren't part of the
   *  structure payload. */
  structureNeeded: boolean;
}

function sameIds(a: string[], b: string[]): boolean {
  return a.length === b.length && a.every((x, i) => x === b[i]);
}

/**
 * diffCurriculum computes the granular edit plan between the initial
 * (server-loaded) tree and the editor's current local tree. Pure —
 * exported for tests.
 */
export function diffCurriculum(initial: EditorModule[], current: EditorModule[]): DiffPlan {
  const initModById = new Map(initial.map((m) => [m.id, m]));
  const initLesById = new Map<string, EditorLesson>();
  const initQuizById = new Map<string, EditorQuiz>();
  for (const m of initial) {
    for (const l of m.lessons) {
      initLesById.set(l.id, l);
      for (const q of l.quizzes) initQuizById.set(q.id, q);
    }
  }

  const curModIds = new Set(current.map((m) => m.id));
  const curLesIds = new Set(current.flatMap((m) => m.lessons.map((l) => l.id)));
  const curQuizIds = new Set(
    current.flatMap((m) => m.lessons.flatMap((l) => l.quizzes.map((q) => q.id))),
  );

  const moduleCreates = current.filter((m) => !initModById.has(m.id));
  const moduleDeletes = initial.filter((m) => !curModIds.has(m.id)).map((m) => m.id);
  const moduleUpdates = current
    .filter((m) => {
      const i = initModById.get(m.id);
      return i !== undefined && (i.title !== m.title || i.description !== m.description);
    })
    .map((m) => ({ id: m.id, title: m.title, description: m.description }));

  const lessonCreates: DiffPlan['lessonCreates'] = [];
  const lessonUpdates: DiffPlan['lessonUpdates'] = [];
  const lessonContentUpdates: DiffPlan['lessonContentUpdates'] = [];
  const quizCreates: DiffPlan['quizCreates'] = [];
  const quizUpdates: DiffPlan['quizUpdates'] = [];
  for (const m of current) {
    for (const l of m.lessons) {
      const il = initLesById.get(l.id);
      if (!il) {
        lessonCreates.push({ lesson: l, moduleId: m.id });
      } else {
        if (il.title !== l.title) lessonUpdates.push({ id: l.id, title: l.title });
        if (il.content_md !== l.content_md)
          lessonContentUpdates.push({ id: l.id, content_md: l.content_md });
      }
      for (const q of l.quizzes) {
        const iq = initQuizById.get(q.id);
        if (!iq) {
          quizCreates.push({ quiz: q, lessonId: l.id });
        } else if (
          iq.question_md !== q.question_md ||
          iq.expected_md !== q.expected_md ||
          iq.kind !== q.kind
        ) {
          quizUpdates.push({
            id: q.id,
            question_md: q.question_md,
            expected_md: q.expected_md,
            kind: q.kind,
          });
        }
      }
    }
  }
  const lessonDeletes = [...initLesById.keys()].filter((id) => !curLesIds.has(id));
  const quizDeletes = [...initQuizById.keys()].filter((id) => !curQuizIds.has(id));

  let structureNeeded =
    moduleCreates.length > 0 ||
    moduleDeletes.length > 0 ||
    lessonCreates.length > 0 ||
    lessonDeletes.length > 0;

  if (!structureNeeded) {
    // No creates/deletes — module order changed?
    const survivingInitialOrder = initial.filter((m) => curModIds.has(m.id)).map((m) => m.id);
    if (
      !sameIds(
        survivingInitialOrder,
        current.map((m) => m.id),
      )
    )
      structureNeeded = true;
  }
  if (!structureNeeded) {
    // Lesson order within a module changed, or a lesson moved across
    // modules (it appears in a different module's list now).
    for (const m of current) {
      const im = initModById.get(m.id);
      if (!im) continue;
      const survivingLessonOrder = im.lessons.filter((l) => curLesIds.has(l.id)).map((l) => l.id);
      if (
        !sameIds(
          survivingLessonOrder,
          m.lessons.map((l) => l.id),
        )
      ) {
        structureNeeded = true;
        break;
      }
    }
  }

  return {
    moduleUpdates,
    moduleCreates,
    moduleDeletes,
    lessonUpdates,
    lessonContentUpdates,
    lessonCreates,
    lessonDeletes,
    quizUpdates,
    quizCreates,
    quizDeletes,
    structureNeeded,
  };
}

/**
 * applyGranularPlan executes a DiffPlan against the granular
 * endpoints. Sequential awaits are fine (dozens of rows max).
 *
 * Ordering matters: updates first (they never reference new ids),
 * then creates (modules → lessons → quizzes so the tempId → serverId
 * map resolves parents), then deletes (children before parents —
 * the server cascades anyway, but quiz-first keeps 404 noise down),
 * then the structure reorder with every id resolved to server ids.
 *
 * Partial application is possible by design: a mid-plan failure
 * leaves earlier rows applied. The caller (onSave) reports the error
 * and reloads ground truth.
 */
export async function applyGranularPlan(
  courseId: string,
  plan: DiffPlan,
  current: EditorModule[],
): Promise<void> {
  const idMap = new Map<string, string>();
  const resolve = (id: string): string => idMap.get(id) ?? id;

  for (const u of plan.moduleUpdates) {
    await api.updateModule(u.id, { title: u.title, description: u.description });
  }
  for (const u of plan.lessonUpdates) {
    await api.renameLesson(u.id, u.title);
  }
  for (const u of plan.lessonContentUpdates) {
    await api.updateLessonContent(u.id, { content_md: u.content_md });
  }
  for (const u of plan.quizUpdates) {
    await api.updateQuiz(u.id, {
      question_md: u.question_md,
      expected_md: u.expected_md,
      kind: u.kind,
    });
  }

  for (const m of plan.moduleCreates) {
    const created = await api.createCourseModule(courseId, {
      title: m.title,
      description: m.description,
    });
    idMap.set(m.id, created.id);
  }
  for (const c of plan.lessonCreates) {
    const created = await api.createModuleLesson(resolve(c.moduleId), { title: c.lesson.title });
    idMap.set(c.lesson.id, created.id);
    // New lessons are born locked with empty content; push the typed
    // body through the content endpoint so imports survive.
    if (c.lesson.content_md.trim()) {
      await api.updateLessonContent(created.id, { content_md: c.lesson.content_md });
    }
  }
  for (const c of plan.quizCreates) {
    await api.addQuiz(resolve(c.lessonId), {
      question_md: c.quiz.question_md,
      expected_md: c.quiz.kind === 'exact' ? c.quiz.expected_md : undefined,
      kind: c.quiz.kind,
    });
  }

  for (const id of plan.quizDeletes) await api.deleteQuiz(id);
  for (const id of plan.lessonDeletes) await api.deleteLesson(id);
  for (const id of plan.moduleDeletes) await api.deleteModule(id);

  if (plan.structureNeeded) {
    await api.applyCourseStructure(
      courseId,
      current.map((m) => ({
        module_id: resolve(m.id),
        lesson_ids: m.lessons.map((l) => resolve(l.id)),
      })),
    );
  }
}

/** validateTree is shared by both save modes — runs before any
 *  network call. Returns the first problem found, or null. */
function validateTree(modules: EditorModule[]): string | null {
  for (const m of modules) {
    if (!m.title.trim()) return 'Every module needs a title.';
    for (const l of m.lessons) {
      if (!l.title.trim()) return 'Every lesson needs a title.';
      for (const q of l.quizzes) {
        if (!q.question_md.trim()) return 'Every quiz needs a question.';
        if (q.kind === 'exact' && !q.expected_md.trim())
          return 'Exact quizzes need an expected answer.';
      }
    }
  }
  return null;
}

/** moveLessonAcrossModules appends the lesson to the target module
 *  (mid-drag preview; the final index is committed in onDragEnd). */
function moveLessonAcrossModules(
  mods: EditorModule[],
  lessonId: string,
  targetModuleId: string,
): EditorModule[] {
  let moved: EditorLesson | null = null;
  const without = mods.map((m) => {
    const idx = m.lessons.findIndex((l) => l.id === lessonId);
    if (idx < 0) return m;
    moved = m.lessons[idx];
    return {
      ...m,
      lessons: m.lessons.filter((l) => l.id !== lessonId).map((l, i) => ({ ...l, position: i })),
    };
  });
  if (!moved) return mods;
  const lesson: EditorLesson = moved;
  return without.map((m) =>
    m.id === targetModuleId
      ? { ...m, lessons: [...m.lessons, { ...lesson, position: m.lessons.length }] }
      : m,
  );
}

/** Row-level callbacks shared by the sortable rows. */
interface RowHandlers {
  updateModule: (id: string, patch: Partial<EditorModule>) => void;
  removeModule: (id: string) => void;
  addLesson: (moduleID: string) => void;
  updateLesson: (moduleID: string, lessonID: string, patch: Partial<EditorLesson>) => void;
  removeLesson: (moduleID: string, lessonID: string) => void;
  addQuiz: (moduleID: string, lessonID: string) => void;
  updateQuiz: (
    moduleID: string,
    lessonID: string,
    quizID: string,
    patch: Partial<EditorQuiz>,
  ) => void;
  removeQuiz: (moduleID: string, lessonID: string, quizID: string) => void;
}

function SortableLessonRow(props: {
  m: EditorModule;
  l: EditorLesson;
  h: RowHandlers;
}): JSX.Element {
  const { m, l, h } = props;
  const { attributes, listeners, setNodeRef, transform, transition, isDragging } = useSortable({
    id: l.id,
  });
  return (
    <li
      ref={setNodeRef}
      style={{ transform: CSS.Transform.toString(transform), transition }}
      data-testid="editor-lesson"
      className={`space-y-1 rounded bg-slate-50 dark:bg-slate-900 p-2 ${isDragging ? 'opacity-50' : ''}`}
    >
      <div className="flex gap-2">
        <button
          type="button"
          {...attributes}
          {...listeners}
          data-testid="editor-lesson-drag"
          aria-label="Drag lesson"
          className="cursor-grab px-1 text-slate-400 hover:text-slate-600"
        >
          ⠿
        </button>
        <Input
          type="text"
          value={l.title}
          onChange={(e) => h.updateLesson(m.id, l.id, { title: e.target.value })}
          placeholder="Lesson title"
          data-testid="editor-lesson-title"
          className="flex-1 text-sm"
        />
        <Button
          type="button"
          onClick={() => h.removeLesson(m.id, l.id)}
          data-testid="editor-lesson-remove"
          variant="ghost"
          size="sm"
          className="text-red-700 hover:bg-red-100 text-xs"
        >
          Remove
        </Button>
      </div>
      <Textarea
        value={l.content_md}
        onChange={(e) => h.updateLesson(m.id, l.id, { content_md: e.target.value })}
        placeholder="Optional lesson body (markdown)"
        rows={2}
        className="text-xs"
      />
      <div className="pl-2 border-l border-slate-200 dark:border-slate-700">
        <ul className="space-y-1">
          {l.quizzes.map((q) => (
            <li key={q.id} className="space-y-1" data-testid="editor-quiz">
              <div className="flex gap-2 items-center">
                <Select
                  value={q.kind}
                  onValueChange={(v) =>
                    h.updateQuiz(m.id, l.id, q.id, {
                      kind: v as 'exact' | 'open',
                    })
                  }
                >
                  <SelectTrigger className="w-20 text-xs h-7">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="exact">exact</SelectItem>
                    <SelectItem value="open">open</SelectItem>
                  </SelectContent>
                </Select>
                <Input
                  type="text"
                  value={q.question_md}
                  onChange={(e) => h.updateQuiz(m.id, l.id, q.id, { question_md: e.target.value })}
                  placeholder="Question"
                  data-testid="editor-quiz-question"
                  className="flex-1 text-xs h-7"
                />
                <Button
                  type="button"
                  onClick={() => h.removeQuiz(m.id, l.id, q.id)}
                  variant="ghost"
                  size="sm"
                  className="text-red-700 hover:bg-red-100 text-xs"
                >
                  ×
                </Button>
              </div>
              {q.kind === 'exact' && (
                <Input
                  type="text"
                  value={q.expected_md}
                  onChange={(e) => h.updateQuiz(m.id, l.id, q.id, { expected_md: e.target.value })}
                  placeholder="Expected answer"
                  data-testid="editor-quiz-expected"
                  className="w-full text-xs"
                />
              )}
            </li>
          ))}
        </ul>
        <Button
          type="button"
          onClick={() => h.addQuiz(m.id, l.id)}
          data-testid="editor-add-quiz"
          variant="link"
          size="sm"
          className="mt-1 text-xs text-orenda-700 hover:underline"
        >
          + Add quiz
        </Button>
      </div>
    </li>
  );
}

function SortableModuleRow(props: { m: EditorModule; h: RowHandlers }): JSX.Element {
  const { m, h } = props;
  const { attributes, listeners, setNodeRef, transform, transition, isDragging } = useSortable({
    id: m.id,
  });
  return (
    <li
      ref={setNodeRef}
      style={{ transform: CSS.Transform.toString(transform), transition }}
      data-testid="editor-module"
      className={`rounded border border-border p-3 bg-white dark:bg-slate-950 space-y-2 ${isDragging ? 'opacity-50' : ''}`}
    >
      <div className="flex gap-2">
        <button
          type="button"
          {...attributes}
          {...listeners}
          data-testid="editor-module-drag"
          aria-label="Drag module"
          className="cursor-grab px-1 text-slate-400 hover:text-slate-600"
        >
          ⠿
        </button>
        <Input
          type="text"
          value={m.title}
          onChange={(e) => h.updateModule(m.id, { title: e.target.value })}
          placeholder="Module title (e.g. Basics)"
          data-testid="editor-module-title"
          className="flex-1 text-sm"
        />
        <Button
          type="button"
          onClick={() => h.removeModule(m.id)}
          data-testid="editor-module-remove"
          variant="ghost"
          size="sm"
          className="text-red-700 hover:bg-red-50"
        >
          Remove
        </Button>
      </div>
      <Textarea
        value={m.description}
        onChange={(e) => h.updateModule(m.id, { description: e.target.value })}
        placeholder="Optional description"
        rows={2}
        className="text-xs"
      />

      <SortableContext items={m.lessons.map((l) => l.id)} strategy={verticalListSortingStrategy}>
        <ul className="space-y-2 pl-3 border-l border-border">
          {m.lessons.map((l) => (
            <SortableLessonRow key={l.id} m={m} l={l} h={h} />
          ))}
        </ul>
      </SortableContext>
      <Button
        type="button"
        onClick={() => h.addLesson(m.id)}
        data-testid="editor-add-lesson"
        variant="link"
        size="sm"
        className="text-xs text-orenda-700 hover:underline"
      >
        + Add lesson
      </Button>
    </li>
  );
}

export function CourseCurriculumEditor(props: {
  course: Course;
  initialModules: EditorModule[];
  onCancel: () => void;
  onSaved: () => void;
}): JSX.Element {
  const { course, onCancel, onSaved } = props;
  const [modules, setModules] = useState<EditorModule[]>(props.initialModules);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [importOpen, setImportOpen] = useState(false);
  const [importText, setImportText] = useState('');
  const [importError, setImportError] = useState<string | null>(null);

  // Re-seed when the parent reloads (e.g. after cancel-save).
  useEffect(() => {
    setModules(props.initialModules);
  }, [props.initialModules]);

  // Phase 30.13: active courses edit granularly (stable IDs, student
  // progress preserved); done/archived are frozen.
  const granular = course.status === 'active';
  const isReadOnly = course.status === 'done' || course.status === 'archived';

  const sensors = useSensors(useSensor(PointerSensor, { activationConstraint: { distance: 4 } }));

  const addModule = useCallback(() => {
    setModules((prev) => [
      ...prev,
      {
        id: crypto.randomUUID(),
        title: '',
        description: '',
        position: prev.length,
        lessons: [],
      },
    ]);
  }, []);

  const updateModule = useCallback((id: string, patch: Partial<EditorModule>) => {
    setModules((prev) => prev.map((m) => (m.id === id ? { ...m, ...patch } : m)));
  }, []);

  const removeModule = useCallback((id: string) => {
    setModules((prev) => prev.filter((m) => m.id !== id));
  }, []);

  const addLesson = useCallback((moduleID: string) => {
    setModules((prev) =>
      prev.map((m) =>
        m.id === moduleID
          ? {
              ...m,
              lessons: [
                ...m.lessons,
                {
                  id: crypto.randomUUID(),
                  title: '',
                  position: m.lessons.length,
                  content_md: '',
                  quizzes: [],
                },
              ],
            }
          : m,
      ),
    );
  }, []);

  const updateLesson = useCallback(
    (moduleID: string, lessonID: string, patch: Partial<EditorLesson>) => {
      setModules((prev) =>
        prev.map((m) =>
          m.id === moduleID
            ? {
                ...m,
                lessons: m.lessons.map((l) => (l.id === lessonID ? { ...l, ...patch } : l)),
              }
            : m,
        ),
      );
    },
    [],
  );

  const removeLesson = useCallback((moduleID: string, lessonID: string) => {
    setModules((prev) =>
      prev.map((m) =>
        m.id === moduleID ? { ...m, lessons: m.lessons.filter((l) => l.id !== lessonID) } : m,
      ),
    );
  }, []);

  const addQuiz = useCallback((moduleID: string, lessonID: string) => {
    setModules((prev) =>
      prev.map((m) =>
        m.id === moduleID
          ? {
              ...m,
              lessons: m.lessons.map((l) =>
                l.id === lessonID
                  ? {
                      ...l,
                      quizzes: [
                        ...l.quizzes,
                        {
                          id: crypto.randomUUID(),
                          position: l.quizzes.length,
                          question_md: '',
                          expected_md: '',
                          kind: 'exact',
                        },
                      ],
                    }
                  : l,
              ),
            }
          : m,
      ),
    );
  }, []);

  const updateQuiz = useCallback(
    (moduleID: string, lessonID: string, quizID: string, patch: Partial<EditorQuiz>) => {
      setModules((prev) =>
        prev.map((m) =>
          m.id === moduleID
            ? {
                ...m,
                lessons: m.lessons.map((l) =>
                  l.id === lessonID
                    ? {
                        ...l,
                        quizzes: l.quizzes.map((q) => (q.id === quizID ? { ...q, ...patch } : q)),
                      }
                    : l,
                ),
              }
            : m,
        ),
      );
    },
    [],
  );

  const removeQuiz = useCallback((moduleID: string, lessonID: string, quizID: string) => {
    setModules((prev) =>
      prev.map((m) =>
        m.id === moduleID
          ? {
              ...m,
              lessons: m.lessons.map((l) =>
                l.id === lessonID ? { ...l, quizzes: l.quizzes.filter((q) => q.id !== quizID) } : l,
              ),
            }
          : m,
      ),
    );
  }, []);

  const handlers: RowHandlers = useMemo(
    () => ({
      updateModule,
      removeModule,
      addLesson,
      updateLesson,
      removeLesson,
      addQuiz,
      updateQuiz,
      removeQuiz,
    }),
    [
      updateModule,
      removeModule,
      addLesson,
      updateLesson,
      removeLesson,
      addQuiz,
      updateQuiz,
      removeQuiz,
    ],
  );

  const findLesson = useCallback(
    (lessonId: string): { module: EditorModule; lesson: EditorLesson } | null => {
      for (const m of modules) {
        const l = m.lessons.find((x) => x.id === lessonId);
        if (l) return { module: m, lesson: l };
      }
      return null;
    },
    [modules],
  );

  // Mid-drag: move a lesson across modules so the preview lands in
  // the target list (same pattern as tasks across kanban columns).
  // Module drags commit in onDragEnd only.
  const onDragOver = useCallback(
    (ev: DragOverEvent): void => {
      const activeId = String(ev.active.id);
      const overId = ev.over ? String(ev.over.id) : null;
      if (!overId || activeId === overId) return;
      if (modules.some((m) => m.id === activeId)) return;
      const src = findLesson(activeId);
      if (!src) return;
      let targetModuleId: string | null = null;
      if (modules.some((m) => m.id === overId)) {
        targetModuleId = overId;
      } else {
        const dst = findLesson(overId);
        if (dst) targetModuleId = dst.module.id;
      }
      if (!targetModuleId || targetModuleId === src.module.id) return;
      setModules((prev) => moveLessonAcrossModules(prev, activeId, targetModuleId));
    },
    [modules, findLesson],
  );

  const onDragEnd = useCallback(
    (ev: DragEndEvent): void => {
      const activeId = String(ev.active.id);
      const overId = ev.over ? String(ev.over.id) : null;
      if (!overId || activeId === overId) return;

      // Module reorder.
      if (modules.some((m) => m.id === activeId)) {
        if (!modules.some((m) => m.id === overId)) return;
        setModules((prev) => {
          const from = prev.findIndex((m) => m.id === activeId);
          const to = prev.findIndex((m) => m.id === overId);
          if (from < 0 || to < 0 || from === to) return prev;
          return arrayMove(prev, from, to).map((m, i) => ({ ...m, position: i }));
        });
        return;
      }

      // Lesson reorder within its (possibly new, after onDragOver)
      // module. Dropping onto a module row is an append — already
      // handled by onDragOver.
      const src = findLesson(activeId);
      if (!src) return;
      const dst = findLesson(overId);
      if (dst && dst.module.id === src.module.id) {
        setModules((prev) =>
          prev.map((m) => {
            if (m.id !== src.module.id) return m;
            const from = m.lessons.findIndex((l) => l.id === activeId);
            const to = m.lessons.findIndex((l) => l.id === overId);
            if (from < 0 || to < 0 || from === to) return m;
            return {
              ...m,
              lessons: arrayMove(m.lessons, from, to).map((l, i) => ({ ...l, position: i })),
            };
          }),
        );
      }
    },
    [modules, findLesson],
  );

  const onSave = useCallback(async () => {
    if (busy) return;
    setBusy(true);
    setError(null);
    try {
      const validationError = validateTree(modules);
      if (validationError) {
        setError(validationError);
        return;
      }
      if (granular) {
        const plan = diffCurriculum(props.initialModules, modules);
        await applyGranularPlan(course.id, plan, modules);
        onSaved();
      } else {
        const payload = {
          modules: modules.map((m, mi) => ({
            id: m.id,
            title: m.title,
            description: m.description,
            position: mi,
            lessons: m.lessons.map((l, li) => ({
              id: l.id,
              title: l.title,
              position: li,
              content_md: l.content_md,
              quizzes: l.quizzes.map((q, qi) => ({
                id: q.id,
                position: qi,
                question_md: q.question_md,
                expected_md: q.expected_md,
                kind: q.kind,
              })),
            })),
          })),
        };
        await api.submitCurriculum(course.id, payload);
        onSaved();
      }
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
      // Granular edits apply one row at a time — a failure can leave
      // the course partially edited. Reload ground truth anyway so
      // the editor state can't drift from the server.
      if (granular) onSaved();
    } finally {
      setBusy(false);
    }
  }, [busy, course.id, modules, onSaved, granular, props.initialModules]);

  const applyImport = useCallback((): void => {
    const parsed = parseCurriculumMarkdown(importText);
    if (parsed.length === 0) {
      setImportError('Nothing to import — no modules found in the markdown.');
      return;
    }
    // In granular mode imported rows are all "new" and rows missing
    // from the parse are "deleted" — the diff handles it; give the
    // owner a chance to back out first.
    if (
      modules.length > 0 &&
      !window.confirm('Replace current curriculum with imported markdown?')
    ) {
      return;
    }
    setModules(parsed);
    setImportOpen(false);
    setImportText('');
    setImportError(null);
  }, [importText, modules.length]);

  const moduleCount = modules.length;
  const lessonCount = useMemo(
    () => modules.reduce((acc, m) => acc + m.lessons.length, 0),
    [modules],
  );
  const quizCount = useMemo(
    () => modules.reduce((acc, m) => acc + m.lessons.reduce((b, l) => b + l.quizzes.length, 0), 0),
    [modules],
  );

  if (isReadOnly) {
    return (
      <p className="text-sm text-slate-500 italic" data-testid="editor-readonly">
        Curriculum is frozen once the course is done or archived.
      </p>
    );
  }

  return (
    <div className="space-y-3" data-testid="course-editor">
      <div className="flex items-center justify-between">
        <div className="text-xs text-slate-500 font-mono">
          {moduleCount} modules · {lessonCount} lessons · {quizCount} quizzes
        </div>
        <Button
          type="button"
          onClick={() => {
            setImportOpen((v) => !v);
            setImportError(null);
          }}
          data-testid="editor-import-toggle"
          variant="link"
          size="sm"
          className="text-xs text-orenda-700 hover:underline"
        >
          {importOpen ? 'Close import' : 'Import markdown'}
        </Button>
      </div>

      {importOpen && (
        <div className="space-y-2 rounded border border-border p-2">
          <Textarea
            value={importText}
            onChange={(e) => setImportText(e.target.value)}
            rows={8}
            placeholder={
              '## Module title\n### Lesson title\n- [exact] Question | Expected answer\n- [open] Open question'
            }
            data-testid="editor-import-textarea"
            className="text-xs font-mono"
          />
          {importError && <p className="text-xs text-red-600">{importError}</p>}
          <Button
            type="button"
            onClick={applyImport}
            data-testid="editor-import-apply"
            size="sm"
            className="text-xs"
          >
            Apply
          </Button>
        </div>
      )}

      {modules.length === 0 && (
        <p className="text-sm text-slate-400 italic">
          No modules yet. Add the first one to start shaping the program.
        </p>
      )}

      <DndContext
        sensors={sensors}
        collisionDetection={closestCorners}
        onDragOver={onDragOver}
        onDragEnd={onDragEnd}
      >
        <SortableContext items={modules.map((m) => m.id)} strategy={verticalListSortingStrategy}>
          <ul className="space-y-3">
            {modules.map((m) => (
              <SortableModuleRow key={m.id} m={m} h={handlers} />
            ))}
          </ul>
        </SortableContext>
      </DndContext>

      <Button
        type="button"
        onClick={addModule}
        data-testid="editor-add-module"
        variant="link"
        size="sm"
        className="text-sm text-orenda-700 hover:underline"
      >
        + Add module
      </Button>

      {error && <p className="text-sm text-red-600">{error}</p>}

      <div className="flex gap-2 pt-2">
        <Button
          type="button"
          onClick={() => void onSave()}
          disabled={busy}
          data-testid="editor-save"
          size="sm"
        >
          {busy ? 'Saving…' : granular ? 'Save changes' : 'Save curriculum'}
        </Button>
        <Button type="button" onClick={onCancel} disabled={busy} variant="outline" size="sm">
          Cancel
        </Button>
      </div>
    </div>
  );
}
