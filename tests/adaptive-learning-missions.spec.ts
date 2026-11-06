import { test, expect } from '@playwright/test';
import { join } from 'node:path';
import { seedLearningWorkspace, learningStatus, learningPost } from './helpers/learning-missions';

test.describe('adaptive learning through production routes', () => {
  test.describe.configure({ mode: 'serial' });
  test.use({ viewport: { width: 1365, height: 1100 } });
  test.skip(
    !process.env.ORI_LEARNING_DEMO_SANDBOX,
    'Run node scripts/learning-missions-demo.mjs (isolated HOME + controlled provider).'
  );
  let workspaceID: string;
  const base = process.env.PLAYWRIGHT_BASE_URL || '';
  const evidence = process.env.ORI_LEARNING_DEMO_EVIDENCE || '';
  const provider = process.env.ORI_LEARNING_DEMO_PROVIDER || '';
  test.beforeAll(async ({ request }) => {
    workspaceID = await seedLearningWorkspace(request, base);
  });

  test('explicit delegation, exact rejection and reload preserve finite onboarding', async ({
    page,
    request
  }) => {
    const errors: string[] = [];
    page.on('pageerror', error => errors.push(error.message));
    const count = async () => (await (await request.get(provider + '/counts')).json()).calls;
    const before = await (await request.get(base + '/api/progression')).json();
    await page.goto('/');
    await page.locator('#cockpitQuestsToggle').click();
    const board = page.locator('#learningMissions');
    await expect(board).toBeVisible();
    await board.getByRole('button', { name: 'Choose a workspace', exact: true }).click();
    await board.getByLabel('Workspace', { exact: true }).selectOption(workspaceID);
    await board.getByRole('button', { name: 'Request a practice', exact: true }).click();
    await expect(
      board.getByRole('button', { name: 'Start learning — no execution', exact: true })
    ).toBeVisible();
    await page.screenshot({ path: join(evidence, '01-offer.png') });
    await board.getByRole('button', { name: 'Start learning — no execution', exact: true }).click();
    await board.getByRole('button', { name: 'Prepare a draft Ticket', exact: true }).click();
    await board
      .getByLabel('Text and drafting requirements (maximum 4 KiB)', { exact: true })
      .fill(
        'Draft a short workshop welcome. Bring curiosity and one small idea. Start with introductions, then write a short draft together.'
      );
    await board.getByLabel('Assigned agent', { exact: true }).selectOption('Learning writer');
    await board
      .getByRole('button', { name: 'Save reviewed input as a Ticket', exact: true })
      .click();
    await board.getByRole('link', { name: 'Open Ticket to review execution', exact: true }).click();
    const review = page.locator('#learningTicketReview');
    await expect(review).toBeVisible();
    await expect(
      review.getByRole('button', {
        name: 'Start this Ticket with this input and model',
        exact: true
      })
    ).toBeVisible();
    await review.scrollIntoViewIfNeeded();
    await page.screenshot({ path: join(evidence, '02-execution-review.png') });
    expect(await count()).toBe(0);
    const saved = await learningStatus(request, base);
    expect(saved.instances[0].state).toBe('active');
    expect(saved.instances[0].progress.run_id).toBeUndefined();
    await review
      .getByRole('button', { name: 'Start this Ticket with this input and model', exact: true })
      .click();
    await expect
      .poll(async () => (await learningStatus(request, base)).instances[0].progress.result, {
        timeout: 15000
      })
      .toBeTruthy();
    expect((await learningStatus(request, base)).instances[0].state).toBe('active');
    await review
      .getByRole('button', { name: 'Refresh learning and execution status', exact: true })
      .click();
    await review
      .getByRole('button', { name: 'Inspect the exact result for learning', exact: true })
      .click();
    await review.getByLabel('Your decision', { exact: true }).selectOption('reject');
    await review.getByLabel('Reason', { exact: true }).selectOption('missing_detail');
    await review
      .getByLabel('Optional note (500 characters)', { exact: true })
      .fill('I need a specific start time before using this welcome.');
    await review.scrollIntoViewIfNeeded();
    await page.screenshot({ path: join(evidence, '03-exact-review.png') });
    await review
      .getByRole('button', { name: 'Record this result-specific decision', exact: true })
      .click();
    await expect(
      review.getByText(
        'Result-specific practice recorded. It is not a quality rating or proof of mastery.',
        { exact: true }
      )
    ).toBeVisible();
    await page.reload();
    await expect(
      review.getByText(
        'Result-specific practice recorded. It is not a quality rating or proof of mastery.',
        { exact: true }
      )
    ).toBeVisible();
    await review.scrollIntoViewIfNeeded();
    await page.screenshot({ path: join(evidence, '04-completed-reload.png') });
    const after = await (await request.get(base + '/api/progression')).json();
    expect(after.total_count).toBe(before.total_count);
    expect(
      after.tiers.flatMap((tier: { quests: { id: string }[] }) => tier.quests.map(q => q.id))
    ).toEqual(
      before.tiers.flatMap((tier: { quests: { id: string }[] }) => tier.quests.map(q => q.id))
    );
    expect(await count()).toBe(1);
    expect(errors).toEqual([]);
  });

  test('forged owner, actor, completion and result fields cannot change saved credit', async ({
    request
  }) => {
    const before = await learningStatus(request, base);
    const row = before.instances[0];
    for (const forged of [
      { owner: 'foreign' },
      { actor: 'user' },
      { complete: true },
      { run_id: 'wrong-run' }
    ]) {
      await learningPost(
        request,
        base,
        `/api/learning-missions/instances/${row.id}/review`,
        {
          operation_id: 'forged-' + Object.keys(forged)[0],
          expected_revision: before.owner.revision,
          instance_revision: row.revision,
          action: 'prepare',
          ...forged
        },
        400
      );
    }
    await learningPost(
      request,
      base,
      '/api/learning-missions/instances/unavailable/decisions',
      {
        operation_id: 'foreign-instance',
        expected_revision: before.owner.revision,
        instance_revision: 1,
        decision: 'stop'
      },
      404
    );
    const denied = await request.post(base + '/api/learning-missions/proposals', {
      data: {
        operation_id: 'foreign-origin',
        expected_revision: before.owner.revision,
        studio_id: workspaceID
      },
      headers: { Origin: 'http://127.0.0.1:1' }
    });
    expect(denied.status()).toBe(403);
    const after = await learningStatus(request, base);
    expect(after).toEqual(before);
    expect((await (await request.get(provider + '/counts')).json()).calls).toBe(1);
  });
});
