// @ts-check
/**
 * Phase 28.5 (polish): task.commented activity emission.
 *
 * The action constant `task.commented` has existed in
 * internal/domain/activity since Phase 6, but nothing ever wrote
 * the row — the comment handler was the only task mutation that
 * didn't go through taskSvc and therefore didn't get the
 * standard side-effect. This spec seeds a task through the REST
 * surface, POSTs a comment, and asserts the activity feed
 * reports the new event.
 *
 * Attachment emission (`task.attachment_added`) is the symmetric
 * fix landed in the same handler change; we don't pin it via
 * Playwright here because the upload path needs `data/uploads/`
 * to exist on disk, which is infra setup outside this spec.
 * Coverage for the symmetric path lives in the broader integration
 * suite (course.spec / task-fields.spec exercise attachments).
 */
import { expect, test } from '@playwright/test';

import { createProject, createTask, loginAsUser } from './helpers';

test.describe('Task activity emission (Phase 28.5)', () => {
  test('POST /comments shows up in /activity as task.commented', async () => {
    const ctx = await loginAsUser();
    const project = await createProject(ctx);
    const task = await createTask(ctx, project.id, {
      title: `Activity emission ${Date.now()}`,
    });

    // Comment #1 (user side — exercises createTaskCommentHandler).
    const commentResp = await ctx.post(`/api/v1/tasks/${task.id}/comments`, {
      data: { body_md: 'first comment — should emit task.commented' },
    });
    expect(commentResp.status(), `comment: ${await commentResp.text()}`).toBe(201);

    // Read the activity feed — Phase 3 endpoint, also wired into
    // the task sidebar via TaskFieldControls.
    const activityResp = await ctx.get(`/api/v1/tasks/${task.id}/activity`);
    expect(activityResp.status()).toBe(200);
    const body = (await activityResp.json()) as {
      activity: { action: string; actor_type: string; payload?: string }[];
    };

    const actions = body.activity.map((a) => a.action);
    // Phase 28.5 contract: the new emission lands in the feed
    // (server sorts newest-first internally).
    expect(actions).toContain('task.commented');

    // Sanity: the row carries a payload we can decode + the
    // expected actor type. Catches the regression where a
    // future refactor swaps the actor to "system" or strips
    // the payload.
    const commentedRow = body.activity.find((a) => a.action === 'task.commented');
    expect(commentedRow).toBeDefined();
    expect(commentedRow!.actor_type).toBe('user');
    const payload = JSON.parse(commentedRow!.payload ?? '{}');
    expect(payload.length).toBeGreaterThan(0);
    expect(payload.comment_id).toBeTruthy();

    await ctx.dispose();
  });
});
