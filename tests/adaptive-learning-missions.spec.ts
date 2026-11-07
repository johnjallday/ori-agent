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

  if (['3', '4'].includes(process.env.ORI_LEARNING_DEMO_GROUP || '')) {
    let personalizedAt = 0;
    test('personalized revision comparison and different-input reuse use the same real Ticket path', async ({
      page,
      request
    }) => {
      test.setTimeout(90000);
      const errors: string[] = [];
      page.on('pageerror', error => errors.push(error.message));
      await learningPost(request, base, '/api/settings/system-model', {
        provider: 'openai',
        model: 'gpt-4o-mini'
      });
      const count = async () => (await (await request.get(provider + '/counts')).json()).calls;
      const board = page.locator('#learningMissions');
      const review = page.locator('#learningTicketReview');
      const offer = async (structure: string, personalized = false) => {
        await page.goto('/');
        await page.locator('#cockpitQuestsToggle').click();
        await board.getByRole('button', { name: 'Choose a workspace', exact: true }).click();
        await board.getByLabel('Workspace', { exact: true }).selectOption(workspaceID);
        await board.getByLabel('Practice structure', { exact: true }).selectOption(structure);
        const checkbox = board.getByLabel(
          'Personalize this request with the configured Default model',
          { exact: true }
        );
        await expect(checkbox).not.toBeChecked();
        if (personalized) {
          await checkbox.check();
          personalizedAt = Date.now();
        }
        await board.getByRole('button', { name: 'Request a practice', exact: true }).click();
        await expect(
          board.getByRole('button', { name: 'Start learning — no execution', exact: true })
        ).toBeVisible();
      };
      const accept = async () => {
        await board
          .getByRole('button', { name: 'Start learning — no execution', exact: true })
          .click();
        await board.getByRole('button', { name: 'Prepare a draft Ticket', exact: true }).click();
      };
      const openTicket = async (save: string) => {
        await board.getByLabel('Assigned agent', { exact: true }).selectOption('Learning writer');
        await board.getByRole('button', { name: save, exact: true }).click();
        await board
          .getByRole('link', { name: 'Open Ticket to review execution', exact: true })
          .click();
        await expect(
          review.getByRole('button', {
            name: 'Start this Ticket with this input and model',
            exact: true
          })
        ).toBeVisible();
      };
      const inspect = async () => {
        const active = (await learningStatus(request, base)).instances.find(
          (row: { state: string }) => row.state === 'active'
        );
        await review
          .getByRole('button', { name: 'Start this Ticket with this input and model', exact: true })
          .click();
        await expect
          .poll(
            async () =>
              (await learningStatus(request, base)).instances.find(
                (row: { id: string }) => row.id === active.id
              )?.progress.result,
            { timeout: 15000 }
          )
          .toBeTruthy();
        await review
          .getByRole('button', { name: 'Refresh learning and execution status', exact: true })
          .click();
        await review
          .getByRole('button', { name: 'Inspect the exact result for learning', exact: true })
          .click();
        return active;
      };
      const decide = async (decision: string) => {
        await review.getByLabel('Your decision', { exact: true }).selectOption(decision);
        await review
          .getByLabel('Reason', { exact: true })
          .selectOption(decision === 'use' ? 'meets_scope' : 'missing_detail');
        await review
          .getByRole('button', { name: 'Record this result-specific decision', exact: true })
          .click();
        await expect(
          review.getByText(
            'Result-specific practice recorded. It is not a quality rating or proof of mastery.',
            { exact: true }
          )
        ).toBeVisible();
      };

      await offer('improve_result', true);
      expect(await count()).toBe(2); // One original draft, one explicit design call.
      await expect(board).toContainText('Model-personalized wording');
      const offered = (await learningStatus(request, base)).instances.find(
        (row: { state: string }) => row.state === 'offered'
      );
      await page.screenshot({ path: join(evidence, '05-personalized-revision-offer.png') });
      await board
        .getByRole('button', { name: 'Start learning — no execution', exact: true })
        .scrollIntoViewIfNeeded();
      await page.screenshot({ path: join(evidence, '05b-offer-controls.png') });
      await accept();
      await expect(
        board.getByRole('region', { name: 'Selected original result', exact: true })
      ).toContainText('Bring your curiosity');
      await board
        .getByLabel('Correction (maximum 4 KiB)', { exact: true })
        .fill('Add a specific start time of 10:00.');
      await openTicket('Save reviewed correction as a Ticket');
      await expect(
        review.getByRole('region', {
          name: 'Exact original supplied to this revision',
          exact: true
        })
      ).toContainText('Bring your curiosity');
      await review.scrollIntoViewIfNeeded();
      await page.screenshot({ path: join(evidence, '06-revision-execution-inputs.png') });
      const revised = await inspect();
      expect(revised.definition).toEqual(offered.definition);
      await expect(
        review.getByRole('region', { name: 'Current exact result', exact: true })
      ).toContainText('10:00');
      await expect(
        review.getByRole('region', { name: 'Selected original result', exact: true })
      ).not.toContainText('10:00');
      await review
        .getByLabel('What changed? (required, 500 characters)', { exact: true })
        .fill('The time was added, but I still need the location.');
      await review.scrollIntoViewIfNeeded();
      expect(
        await page
          .locator('.workspace-task-page-hero')
          .evaluate(node => getComputedStyle(node).position)
      ).toBe('relative');
      expect((await review.locator('h3').boundingBox())?.y).toBeGreaterThan(60);
      await page.screenshot({ path: join(evidence, '07-exact-revision-comparison.png') });
      await decide('reject');
      await page.reload();
      expect(
        (await learningStatus(request, base)).instances.find(
          (row: { id: string }) => row.id === revised.id
        ).definition
      ).toEqual(offered.definition);
      expect(await count()).toBe(3);

      // A revision is not silently reused as a different process. Establish a
      // useful plain supplied-text process, then reuse it with different input.
      await offer('delegate_inspect');
      await accept();
      await board
        .getByLabel('Text and drafting requirements (maximum 4 KiB)', { exact: true })
        .fill('Draft a welcome for a community workshop. Bring curiosity and an idea to share.');
      await openTicket('Save reviewed input as a Ticket');
      await inspect();
      await decide('use');
      await offer('repeat_success');
      await accept();
      const originalInput = await board
        .getByRole('region', { name: 'Previously reviewed input', exact: true })
        .textContent();
      const input = board.getByLabel('New text and drafting requirements (maximum 4 KiB)', {
        exact: true
      });
      const change = board.getByLabel('What is materially different? (required, 500 characters)', {
        exact: true
      });
      await input.fill(`  ${originalInput?.replaceAll(' ', '  ')}  `);
      await change.fill('This claim cannot make unchanged inputs different.');
      await board.getByLabel('Assigned agent', { exact: true }).selectOption('Learning writer');
      await board
        .getByRole('button', { name: 'Save reviewed new input as a Ticket', exact: true })
        .click();
      await expect(board.getByRole('status')).toContainText('Whitespace changes do not qualify');
      expect(await count()).toBe(4);
      await input.fill(
        'Draft a welcome for an evening workshop for new volunteers, with an introduction and an idea to share.'
      );
      await change.fill('An evening event for a different audience: new volunteers.');
      await openTicket('Save reviewed new input as a Ticket');
      const reused = await inspect();
      await expect(
        review.getByRole('region', { name: 'Previously reviewed input', exact: true })
      ).toHaveText(originalInput || '');
      await expect(
        review.getByRole('region', { name: 'New reviewed input', exact: true })
      ).toContainText('new volunteers');
      await review.scrollIntoViewIfNeeded();
      await page.screenshot({ path: join(evidence, '08-reuse-inputs-and-results.png') });
      await decide('use');
      await page.reload();
      const completed = (await learningStatus(request, base)).instances.find(
        (row: { id: string }) => row.id === reused.id
      );
      expect(completed.state).toBe('completed');
      expect(completed.progress.original.run_id).not.toBe(completed.progress.result.run_id);
      expect(await count()).toBe(5);
      expect(errors).toEqual([]);
    });

    test('invalid model output and unsupported CLI leave explicit generic practice available', async ({
      page,
      request
    }) => {
      test.setTimeout(60000);
      // Production cadence is not disabled for fixtures.
      await new Promise(done =>
        setTimeout(done, Math.max(0, 31000 - (Date.now() - personalizedAt)))
      );
      await learningPost(request, base, '/api/settings/system-model', {
        provider: 'openai',
        model: 'learning-invalid'
      });
      await page.goto('/');
      await page.locator('#cockpitQuestsToggle').click();
      const board = page.locator('#learningMissions');
      await board.getByRole('button', { name: 'Choose a workspace', exact: true }).click();
      await board.getByLabel('Workspace', { exact: true }).selectOption(workspaceID);
      await board
        .getByLabel('Personalize this request with the configured Default model', { exact: true })
        .check();
      await board.getByRole('button', { name: 'Request a practice', exact: true }).click();
      await expect(board.getByRole('status')).toContainText('No personalized offer was published');
      await page.screenshot({ path: join(evidence, '09-invalid-output-safe-fallback.png') });
      const before = await learningStatus(request, base);
      expect(before.instances.some((row: { state: string }) => row.state === 'offered')).toBe(
        false
      );
      expect((await (await request.get(provider + '/counts')).json()).calls).toBe(6);
      const previousFingerprint = before.model.fingerprint;
      // The launcher supplies a non-executing stub, never the installed CLI.
      await learningPost(request, base, '/api/settings/system-model', {
        provider: 'codex',
        model: 'fixture-cli'
      });
      await board.getByRole('button', { name: 'Refresh learning', exact: true }).click();
      await board.getByRole('button', { name: 'Choose a workspace', exact: true }).click();
      await expect(
        board.getByLabel('Personalize this request with the configured Default model', {
          exact: true
        })
      ).toBeDisabled();
      await expect(board).toContainText('Personalization is unavailable');
      const unavailable = await learningStatus(request, base);
      expect(unavailable.model).toBeUndefined();
      await learningPost(
        request,
        base,
        '/api/learning-missions/proposals',
        {
          operation_id: 'unsupported-cli',
          expected_revision: unavailable.owner.revision,
          studio_id: workspaceID,
          personalize: true,
          consent_version: 1,
          model_fingerprint: previousFingerprint
        },
        404
      );
      await board
        .getByRole('button', { name: 'Request a practice', exact: true })
        .scrollIntoViewIfNeeded();
      await page.screenshot({ path: join(evidence, '10-unsupported-cli-generic-choice.png') });
      await board.getByLabel('Workspace', { exact: true }).selectOption(workspaceID);
      await board.getByRole('button', { name: 'Request a practice', exact: true }).click();
      await expect(
        board.getByRole('button', { name: 'Start learning — no execution', exact: true })
      ).toBeVisible();
      await expect(board).toContainText('Generic practice, chosen by Ori');
      expect((await (await request.get(provider + '/counts')).json()).calls).toBe(6);
    });
  }

  if (process.env.ORI_LEARNING_DEMO_GROUP === '4') {
    test('scoped preferences, reload and coaching off preserve the existing Ready Ticket', async ({
      page,
      request
    }) => {
      const errors: string[] = [];
      page.on('pageerror', error => errors.push(error.message));
      const count = async () => (await (await request.get(provider + '/counts')).json()).calls;
      await learningPost(request, base, '/api/settings/system-model', {
        provider: 'openai',
        model: 'learning-design'
      });
      await page.goto('/');
      await page.locator('#cockpitQuestsToggle').click();
      const board = page.locator('#learningMissions');
      await board
        .getByRole('button', { name: 'Start learning — no execution', exact: true })
        .click();
      await board.getByRole('button', { name: 'Prepare a draft Ticket', exact: true }).click();
      await board
        .getByLabel('Text and drafting requirements (maximum 4 KiB)', { exact: true })
        .fill(
          'Draft a workshop invitation. Keep this Ticket ready; do not execute it in this consent check.'
        );
      await board.getByLabel('Assigned agent', { exact: true }).selectOption('Learning writer');
      await board
        .getByRole('button', { name: 'Save reviewed input as a Ticket', exact: true })
        .click();
      await expect(
        board.getByRole('link', { name: 'Open Ticket to review execution', exact: true })
      ).toBeVisible();
      const before = await learningStatus(request, base);
      const active = before.instances.find((row: { state: string }) => row.state === 'active');
      expect(active.progress.run_id).toBeUndefined();
      const ticketPath = `${base}/api/workspaces/${workspaceID}/tickets/${active.progress.ticket_id}`;
      const readTicket = async () => {
        const response = await request.get(ticketPath);
        expect(response.status()).toBe(200);
        return response.json();
      };
      const ticketBefore = await readTicket();
      expect(ticketBefore.state).toBe('ready');
      await board.getByRole('button', { name: 'Recorded practice', exact: true }).click();
      await board
        .getByRole('heading', { name: 'Recorded practice · not a skill score', exact: true })
        .evaluate(element => element.scrollIntoView({ block: 'start' }));
      await page.screenshot({ path: join(evidence, '14a-practice-disclosure.png') });
      const evaluation = board.getByRole('region', { name: 'Evaluate practice', exact: true });
      await expect(evaluation).toContainText('Practiced with guidance');
      await evaluation.scrollIntoViewIfNeeded();
      await page.screenshot({ path: join(evidence, '14-recorded-practice.png') });
      await board.getByRole('button', { name: 'Inspect evaluate review 1', exact: true }).click();
      await expect(
        board.getByRole('region', { name: 'Recorded review evidence', exact: true })
      ).toContainText('Decision: use');
      await board
        .getByRole('heading', { name: 'Saved decision · exact reviewed result', exact: true })
        .evaluate(element => element.scrollIntoView({ block: 'start' }));
      await page.screenshot({ path: join(evidence, '15-read-only-practice-evidence.png') });
      await board
        .getByRole('region', { name: 'Exact reviewed result', exact: true })
        .scrollIntoViewIfNeeded();
      await page.screenshot({ path: join(evidence, '15b-exact-recorded-result.png') });
      expect((await learningStatus(request, base)).owner.revision).toBe(before.owner.revision);
      expect(await count()).toBe(6);
      await board.getByRole('button', { name: 'Learning preferences', exact: true }).click();
      await board.getByLabel('Coaching mode', { exact: true }).selectOption('automatic');
      await board
        .getByLabel('My explicit focus (optional)', { exact: true })
        .fill('Workshop invitations');
      await board.getByLabel('Include Learning workshop', { exact: true }).check();
      await board
        .getByLabel('Observe new bounded activity and reviewed work in these workspaces', {
          exact: true
        })
        .check();
      await board
        .getByRole('heading', { name: 'Your learning preferences', exact: true })
        .scrollIntoViewIfNeeded();
      await page.screenshot({ path: join(evidence, '11-scope-preferences.png') });
      await board
        .getByText('Approved workspaces · choose at most five', { exact: true })
        .evaluate(element => element.scrollIntoView({ block: 'start' }));
      await page.screenshot({ path: join(evidence, '11b-approved-observation-scope.png') });
      await board.getByRole('button', { name: 'Save learning preferences', exact: true }).click();
      await expect(board.getByRole('status')).toContainText('Review and approve');
      expect((await learningStatus(request, base)).owner.revision).toBe(before.owner.revision);
      await board
        .getByLabel('Approve automatic suggestions with this model and these scopes', {
          exact: true
        })
        .check();
      await page.screenshot({ path: join(evidence, '12-model-scope-consent.png') });
      await board
        .getByRole('heading', { name: 'Automatic suggestions need separate consent', exact: true })
        .evaluate(element => element.scrollIntoView({ block: 'start' }));
      await page.screenshot({ path: join(evidence, '12b-configured-model-disclosure.png') });
      await board.getByRole('button', { name: 'Save learning preferences', exact: true }).click();
      await expect(board.getByRole('status')).toContainText('No suggestion was requested');
      for (let n = 0; n < 3; n++)
        await board.getByRole('button', { name: 'Refresh learning', exact: true }).click();
      expect(await count()).toBe(6);
      await page.reload();
      await page.locator('#cockpitQuestsToggle').click();
      await board.getByRole('button', { name: 'Learning preferences', exact: true }).click();
      await expect(board.getByLabel('Coaching mode', { exact: true })).toHaveValue('automatic');
      await expect(board.getByLabel('Include Learning workshop', { exact: true })).toBeChecked();
      await expect(
        board.getByLabel('Approve automatic suggestions with this model and these scopes', {
          exact: true
        })
      ).not.toBeChecked();
      await board.getByLabel('Coaching mode', { exact: true }).selectOption('off');
      await board.getByRole('button', { name: 'Save learning preferences', exact: true }).click();
      await expect(board).toContainText('Coaching is off');
      await expect(
        board.getByRole('link', { name: 'Open existing Ticket', exact: true })
      ).toBeVisible();
      await expect(
        board.getByRole('button', { name: 'Prepare a draft Ticket', exact: true })
      ).toHaveCount(0);
      await page.screenshot({ path: join(evidence, '13-coaching-off-keeps-work.png') });
      const after = await learningStatus(request, base);
      const unchanged = after.instances.find((row: { id: string }) => row.id === active.id);
      expect(unchanged.definition).toEqual(active.definition);
      expect(unchanged.revision).toBe(active.revision);
      expect(after.owner.preferences.observe).toBe(false);
      expect(after.owner.preferences.workspace_ids).toEqual([]);
      expect(await readTicket()).toEqual(ticketBefore);
      expect(await count()).toBe(6);
      expect(errors).toEqual([]);
    });

    test('opted-in ordinary work records exact practice without another mission', async ({
      page,
      request
    }) => {
      const errors: string[] = [];
      page.on('pageerror', error => errors.push(error.message));
      const count = async () => (await (await request.get(provider + '/counts')).json()).calls;
      const before = await learningStatus(request, base);
      const active = before.instances.find((row: { state: string }) => row.state === 'active');
      expect(active.progress.ticket_id).toBeTruthy();
      await page.goto('/');
      await page.locator('#cockpitQuestsToggle').click();
      const board = page.locator('#learningMissions');
      await board
        .getByRole('button', { name: 'Stop mission — keep actual work', exact: true })
        .click();
      await expect(
        board.getByRole('button', { name: 'Stop mission — keep actual work', exact: true })
      ).toHaveCount(0);
      await board.getByRole('button', { name: 'Learning preferences', exact: true }).click();
      await board.getByLabel('Coaching mode', { exact: true }).selectOption('on_request');
      await board.getByLabel('Guidance depth', { exact: true }).selectOption('basic');
      await board.getByLabel('Include Learning workshop', { exact: true }).check();
      await board
        .getByLabel('Observe new bounded activity and reviewed work in these workspaces', {
          exact: true
        })
        .check();
      await board.getByRole('button', { name: 'Save learning preferences', exact: true }).click();
      await expect(board.getByRole('status')).toContainText('No suggestion was requested');
      expect(await count()).toBe(6);
      await page.goto(active.ticket_href);
      const review = page.locator('#learningTicketReview');
      await expect(review).toContainText('No active learning contract tracks this Ticket');
      await review.scrollIntoViewIfNeeded();
      await page.screenshot({ path: join(evidence, '16-ordinary-reviewed-start.png') });
      await review
        .getByRole('button', { name: 'Start this Ticket with this input and model', exact: true })
        .click();
      await expect
        .poll(
          async () =>
            (await learningStatus(request, base)).observations?.some(
              (row: { ready: boolean }) => row.ready
            ),
          { timeout: 15000 }
        )
        .toBeTruthy();
      await review
        .getByRole('button', { name: 'Refresh learning and execution status', exact: true })
        .click();
      await review
        .getByRole('button', { name: 'Inspect existing result for practice', exact: true })
        .click();
      await expect(review).toContainText('No mission is created');
      await expect(
        review.getByRole('region', { name: 'Reviewed ordinary input', exact: true })
      ).not.toBeEmpty();
      await review
        .getByRole('heading', { name: 'Inspect this exact result', exact: true })
        .evaluate(element => element.scrollIntoView({ block: 'start' }));
      await page.screenshot({ path: join(evidence, '17-ordinary-exact-review.png') });
      const craftBeforeDecision = (await (await request.get(base + '/api/economy')).json()).craft;
      await review.getByLabel('Your decision', { exact: true }).selectOption('reject');
      await review.getByLabel('Reason', { exact: true }).selectOption('incorrect');
      await review
        .getByRole('button', { name: 'Record this result-specific decision', exact: true })
        .click();
      await expect(review.getByRole('status')).toContainText('No mission was created');
      const after = await learningStatus(request, base);
      expect(after.instances).toHaveLength(before.instances.length);
      expect(after.observations || []).toHaveLength(0);
      const prior = before.competencies.items.find(
        (item: { competency: string }) => item.competency === 'evaluate'
      );
      const current = after.competencies.items.find(
        (item: { competency: string }) => item.competency === 'evaluate'
      );
      expect(current.attempts).toBe(prior.attempts + 1);
      expect(current.unknown_support_attempts).toBe(prior.unknown_support_attempts + 1);
      expect(current.independent_attempts).toBe(prior.independent_attempts);
      expect((await (await request.get(base + '/api/economy')).json()).craft).toBe(
        craftBeforeDecision
      );
      const ticket = await (
        await request.get(
          `${base}/api/workspaces/${workspaceID}/tickets/${active.progress.ticket_id}`
        )
      ).json();
      expect(ticket.state).toBe('review');
      expect(await count()).toBe(7);
      await page.goto('/');
      await page.locator('#cockpitQuestsToggle').click();
      await board.getByRole('button', { name: 'Recorded practice', exact: true }).click();
      await board
        .getByRole('region', { name: 'Evaluate practice', exact: true })
        .scrollIntoViewIfNeeded();
      await page.screenshot({ path: join(evidence, '18-ordinary-support-unknown.png') });
      expect(await count()).toBe(7);
      expect(errors).toEqual([]);
    });

    test('smaller and alternative previews preserve actual work until renewed acceptance', async ({
      page,
      request
    }) => {
      const errors: string[] = [];
      page.on('pageerror', error => errors.push(error.message));
      const count = async () => (await (await request.get(provider + '/counts')).json()).calls;
      await page.goto('/');
      await page.locator('#cockpitQuestsToggle').click();
      const board = page.locator('#learningMissions');
      await board.getByRole('button', { name: 'Choose a workspace', exact: true }).click();
      await board.getByLabel('Workspace', { exact: true }).selectOption(workspaceID);
      await board
        .getByLabel('Practice structure', { exact: true })
        .selectOption('delegate_inspect');
      await board.getByRole('button', { name: 'Request a practice', exact: true }).click();
      await board
        .getByRole('button', { name: 'Start learning — no execution', exact: true })
        .click();
      await board.getByRole('button', { name: 'Prepare a draft Ticket', exact: true }).click();
      await board
        .getByLabel('Text and drafting requirements (maximum 4 KiB)', { exact: true })
        .fill('Keep this ordinary Ready Ticket available while reviewing another practice.');
      await board.getByLabel('Assigned agent', { exact: true }).selectOption('Learning writer');
      await board
        .getByRole('button', { name: 'Save reviewed input as a Ticket', exact: true })
        .click();
      await expect(
        board.getByRole('link', { name: 'Open Ticket to review execution', exact: true })
      ).toBeVisible();
      const before = await learningStatus(request, base);
      const active = before.instances.find((row: { state: string }) => row.state === 'active');
      const ticketURL = `${base}/api/workspaces/${workspaceID}/tickets/${active.progress.ticket_id}`;
      const ticketBefore = await (await request.get(ticketURL)).json();
      const adjust = async (label: string, submit: string) => {
        await board.getByText('Adjust this practice', { exact: true }).click();
        await board.getByRole('button', { name: label, exact: true }).click();
        await board.getByRole('button', { name: submit, exact: true }).click();
        await expect(
          board.getByRole('heading', {
            name: 'Replacement to review · not yet accepted',
            exact: true
          })
        ).toBeVisible();
      };
      await adjust('Make smaller', 'Preview smaller practice');
      const pending = await learningStatus(request, base);
      expect(pending.instances.find((row: { id: string }) => row.id === active.id)).toEqual(active);
      await board
        .getByRole('heading', { name: 'Replacement to review · not yet accepted', exact: true })
        .evaluate(element => element.scrollIntoView({ block: 'start' }));
      await page.screenshot({ path: join(evidence, '19-smaller-replacement-preview.png') });
      await page.reload();
      await page.locator('#cockpitQuestsToggle').click();
      await board.getByRole('button', { name: 'Keep current mission', exact: true }).click();
      await expect(
        board.getByRole('link', { name: 'Open Ticket to review execution', exact: true })
      ).toBeVisible();
      await adjust('Something else', 'Find a different option');
      const alternative = (await learningStatus(request, base)).instances.find(
        (row: { state: string }) => row.state === 'offered'
      );
      expect(alternative.definition.candidate_ref).not.toBe(active.definition.candidate_ref);
      await board.getByRole('button', { name: 'Keep current mission', exact: true }).click();
      await adjust('Make smaller', 'Preview smaller practice');
      await board
        .getByRole('button', { name: 'Accept replacement — keep actual work', exact: true })
        .click();
      await expect(
        board.getByRole('button', { name: 'Prepare a draft Ticket', exact: true })
      ).toBeVisible();
      const after = await learningStatus(request, base);
      const original = after.instances.find((row: { id: string }) => row.id === active.id);
      expect(original.state).toBe('stopped');
      expect(original.definition).toEqual(active.definition);
      expect(await (await request.get(ticketURL)).json()).toEqual(ticketBefore);
      const replacement = after.instances.find((row: { state: string }) => row.state === 'active');
      expect(replacement.definition.input_limit).toBe(2048);
      expect(replacement.progress.ticket_id).toBeUndefined();
      await board.getByRole('button', { name: 'Prepare a draft Ticket', exact: true }).click();
      await board
        .getByLabel('Text and drafting requirements (maximum 2048 bytes)', { exact: true })
        .fill('é'.repeat(1025));
      await board.getByLabel('Assigned agent', { exact: true }).selectOption('Learning writer');
      await board
        .getByRole('button', { name: 'Save reviewed input as a Ticket', exact: true })
        .click();
      await expect(board.getByRole('status')).toContainText('2048 bytes');
      expect((await learningStatus(request, base)).owner.revision).toBe(after.owner.revision);
      await page.screenshot({ path: join(evidence, '20-smaller-input-boundary.png') });
      expect(await count()).toBe(7);
      expect(errors).toEqual([]);
    });

    test('one newly reviewed outcome triggers a separately opted-in automatic suggestion', async ({
      page,
      request
    }) => {
      test.setTimeout(90000);
      // Real cadence, not a production bypass: previous explicit proposal calls
      // still count against the same 30-second admission spacing.
      await new Promise(done => setTimeout(done, 31000));
      const errors: string[] = [];
      page.on('pageerror', error => errors.push(error.message));
      const count = async () => (await (await request.get(provider + '/counts')).json()).calls;
      await page.goto('/');
      await page.locator('#cockpitQuestsToggle').click();
      const board = page.locator('#learningMissions');
      await board.getByRole('button', { name: 'Learning preferences', exact: true }).click();
      await board.getByLabel('Coaching mode', { exact: true }).selectOption('automatic');
      await board.getByLabel('Include Learning workshop', { exact: true }).check();
      await board
        .getByLabel('Approve automatic suggestions with this model and these scopes', {
          exact: true
        })
        .check();
      await board.getByRole('button', { name: 'Save learning preferences', exact: true }).click();
      await expect(board.getByRole('status')).toContainText('No suggestion was requested');
      for (let n = 0; n < 3; n++)
        await board.getByRole('button', { name: 'Refresh learning', exact: true }).click();
      expect(await count()).toBe(7);
      await board.getByRole('button', { name: 'Prepare a draft Ticket', exact: true }).click();
      await board
        .getByLabel('Text and drafting requirements (maximum 2048 bytes)', { exact: true })
        .fill(
          'Draft a short invitation for Saturday’s workshop, using only these supplied details.'
        );
      await board.getByLabel('Assigned agent', { exact: true }).selectOption('Learning writer');
      await board
        .getByRole('button', { name: 'Save reviewed input as a Ticket', exact: true })
        .click();
      await board
        .getByRole('link', { name: 'Open Ticket to review execution', exact: true })
        .click();
      const current = (await learningStatus(request, base)).instances.find(
        (row: { state: string }) => row.state === 'active'
      );
      const review = page.locator('#learningTicketReview');
      await review
        .getByRole('button', { name: 'Start this Ticket with this input and model', exact: true })
        .click();
      await expect
        .poll(
          async () =>
            (await learningStatus(request, base)).instances.find(
              (row: { id: string }) => row.id === current.id
            )?.progress.result,
          { timeout: 15000 }
        )
        .toBeTruthy();
      expect(await count()).toBe(8);
      await review
        .getByRole('button', { name: 'Refresh learning and execution status', exact: true })
        .click();
      await review
        .getByRole('button', { name: 'Inspect the exact result for learning', exact: true })
        .click();
      const craft = (await (await request.get(base + '/api/economy')).json()).craft;
      await review.getByLabel('Your decision', { exact: true }).selectOption('reject');
      await review.getByLabel('Reason', { exact: true }).selectOption('missing_detail');
      await review
        .getByRole('button', { name: 'Record this result-specific decision', exact: true })
        .click();
      await expect(
        review.getByText(
          'Result-specific practice recorded. It is not a quality rating or proof of mastery.',
          { exact: true }
        )
      ).toBeVisible();
      // Poll the controlled provider, not Ori: dispatch must follow the review
      // event without a Missions GET or visit waking generation.
      await expect.poll(count, { timeout: 15000 }).toBe(9);
      await expect
        .poll(
          async () =>
            (await learningStatus(request, base)).instances.some(
              (row: { state: string; definition: { automatic?: boolean } }) =>
                row.state === 'offered' && row.definition.automatic
            ),
          { timeout: 5000 }
        )
        .toBeTruthy();
      const generated = await learningStatus(request, base);
      const offer = generated.instances.find((row: { state: string }) => row.state === 'offered');
      expect(offer.progress.ticket_id).toBeUndefined();
      expect((await (await request.get(base + '/api/economy')).json()).craft).toBe(craft);
      await page.goto('/');
      await page.locator('#cockpitQuestsToggle').click();
      await expect(board).toContainText(
        'Offered after a new recorded review under your automatic-suggestion consent'
      );
      await board
        .getByText(/Offered after a new recorded review/)
        .evaluate(element => element.scrollIntoView({ block: 'start' }));
      await page.screenshot({ path: join(evidence, '21-consented-milestone-suggestion.png') });
      for (let n = 0; n < 3; n++)
        await board.getByRole('button', { name: 'Refresh learning', exact: true }).click();
      expect((await learningStatus(request, base)).owner.revision).toBe(generated.owner.revision);
      await board.getByRole('button', { name: 'Learning preferences', exact: true }).click();
      await board.getByLabel('Coaching mode', { exact: true }).selectOption('off');
      await board.getByRole('button', { name: 'Save learning preferences', exact: true }).click();
      await expect(board).toContainText('Coaching is off');
      expect(
        (await learningStatus(request, base)).instances.some(
          (row: { state: string }) => row.state === 'offered'
        )
      ).toBe(false);
      expect(await count()).toBe(9);
      const beforeDiagnostics = await learningStatus(request, base);
      await board.getByRole('button', { name: 'Local learning activity', exact: true }).click();
      await expect(board).toContainText('Suggestion-design calls admitted: 3 · 1 automatic');
      await expect(board).toContainText('This is not a provider bill');
      await board
        .getByRole('heading', { name: 'Local learning activity · not a score', exact: true })
        .evaluate(element => element.scrollIntoView({ block: 'start' }));
      await page.screenshot({ path: join(evidence, '22-local-learning-diagnostics.png') });
      await board
        .getByText(/Suggestion-design calls admitted:/)
        .evaluate(element => element.scrollIntoView({ block: 'start' }));
      await page.screenshot({ path: join(evidence, '22b-local-design-call-accounting.png') });
      await board.getByRole('button', { name: 'Clear learning history', exact: true }).click();
      await expect(board.getByRole('status')).toContainText(
        'Confirm the separate learning-history clear first'
      );
      expect((await learningStatus(request, base)).owner.revision).toBe(
        beforeDiagnostics.owner.revision
      );
      expect(await count()).toBe(9);
      expect(errors).toEqual([]);
    });

    test('fictional interests change subject while scoped independent paths shorten only optional coaching', async ({
      page,
      request
    }) => {
      test.setTimeout(210000);
      const errors: string[] = [];
      page.on('pageerror', error => errors.push(error.message));
      const count = async () => (await (await request.get(provider + '/counts')).json()).calls;
      const names = ['Fictional garden club', 'Fictional maker meetup'];
      const scopes: string[] = [];
      for (const name of names) {
        const made = await learningPost(request, base, '/api/orchestration/workspace', {
          name,
          participating_agents: ['Learning writer']
        });
        scopes.push(made.workspace_id);
      }
      const board = page.locator('#learningMissions');
      const openBoard = async () => {
        await page.goto('/');
        await page.locator('#cockpitQuestsToggle').click();
        await expect(board).toBeVisible();
      };
      const preferences = async (index: number, guidance = 'guided') => {
        await openBoard();
        await board.getByRole('button', { name: 'Learning preferences', exact: true }).click();
        await board.getByLabel('Coaching mode', { exact: true }).selectOption('on_request');
        await board.getByLabel('My explicit focus (optional)', { exact: true }).fill(names[index]);
        await board.getByLabel('Guidance depth', { exact: true }).selectOption(guidance);
        if (guidance === 'guided')
          await board
            .getByLabel('Keep full optional coaching in guided mode', { exact: true })
            .uncheck();
        for (const checkbox of await board.locator('.learning-scopes input[type=checkbox]').all())
          await checkbox.uncheck();
        await board.getByLabel('Include ' + names[index], { exact: true }).check();
        await board
          .getByLabel('Observe new bounded activity and reviewed work in these workspaces', {
            exact: true
          })
          .uncheck();
        const reset = board.getByLabel('Reset saved Not now and Not interested exclusions', {
          exact: true
        });
        if (await reset.count()) await reset.check();
        await board.getByRole('button', { name: 'Save learning preferences', exact: true }).click();
        await expect(board.getByRole('status')).toContainText('No suggestion was requested');
      };
      const propose = async (index: number, personalize: boolean) => {
        await board.getByRole('button', { name: 'Choose a workspace', exact: true }).click();
        await board.getByLabel('Workspace', { exact: true }).selectOption(scopes[index]);
        await board
          .getByLabel('Practice structure', { exact: true })
          .selectOption('delegate_inspect');
        if (personalize)
          await board
            .getByLabel('Personalize this request with the configured Default model', {
              exact: true
            })
            .check();
        await board.getByRole('button', { name: 'Request a practice', exact: true }).click();
        await expect(
          board.getByRole('button', { name: 'Start learning — no execution', exact: true })
        ).toBeVisible();
        return (await learningStatus(request, base)).instances.find(
          (row: { state: string }) => row.state === 'offered'
        );
      };
      // Same unobserved synthetic baseline, not a claim that real ability was measured.
      for (let index = 0; index < 2; index++) {
        await new Promise(done => setTimeout(done, 31000));
        await preferences(index);
        expect(await count()).toBe(9 + index);
        const offer = await propose(index, true);
        expect(offer.definition.coaching_depth).toBe('full');
        expect(offer.definition.hints).toHaveLength(3);
        expect(offer.definition.goal).toBe(
          index === 0 ? 'Draft a plant-swap invitation' : 'Draft a maker-meetup invitation'
        );
        await board
          .getByText(offer.definition.goal, { exact: true })
          .evaluate(element => element.scrollIntoView({ block: 'start' }));
        await board.getByText('Suggested coaching · model-authored', { exact: true }).click();
        await page.screenshot({
          path: join(
            evidence,
            index === 0 ? '23b-garden-full-guidance.png' : '24b-maker-full-guidance.png'
          )
        });
        await board
          .getByText(offer.definition.goal, { exact: true })
          .evaluate(element => element.scrollIntoView({ block: 'start' }));
        await page.screenshot({
          path: join(
            evidence,
            index === 0 ? '23-fictional-garden-profile.png' : '24-fictional-maker-profile.png'
          )
        });
        await board
          .getByRole('button', { name: index === 0 ? 'Not interested' : 'Not now', exact: true })
          .click();
        await expect(board.getByRole('status')).toContainText(
          index === 0 ? 'Not interested saved' : 'Not now saved'
        );
        await page.reload();
        expect(await count()).toBe(10 + index);
        expect((await learningStatus(request, base)).owner.preferences.exclusions).toHaveLength(1);
      }
      await preferences(1, 'basic');
      const baseline = (await learningStatus(request, base)).competencies.items.find(
        (row: { competency: string }) => row.competency === 'delegate'
      ).independent_attempts;
      for (let attempt = 0; attempt < 2; attempt++) {
        if (attempt) await openBoard();
        const offer = await propose(1, false);
        expect(offer.definition.guidance).toBe('basic');
        await board
          .getByRole('button', { name: 'Start learning — no execution', exact: true })
          .click();
        await board.getByRole('button', { name: 'Prepare a draft Ticket', exact: true }).click();
        await board
          .getByLabel('Text and drafting requirements (maximum 4 KiB)', { exact: true })
          .fill(
            attempt === 0
              ? 'Draft a fictional maker-meetup welcome about cardboard prototypes. Saturday at 11:00.'
              : 'Draft a fictional maker-meetup reminder about simple repairs. Sunday at 14:00.'
          );
        await board.getByLabel('Assigned agent', { exact: true }).selectOption('Learning writer');
        await board
          .getByRole('button', { name: 'Save reviewed input as a Ticket', exact: true })
          .click();
        await board
          .getByRole('link', { name: 'Open Ticket to review execution', exact: true })
          .click();
        const review = page.locator('#learningTicketReview');
        await review
          .getByRole('button', { name: 'Start this Ticket with this input and model', exact: true })
          .click();
        await expect
          .poll(
            async () =>
              (await learningStatus(request, base)).instances.find(
                (row: { id: string }) => row.id === offer.id
              )?.progress.result,
            { timeout: 15000 }
          )
          .toBeTruthy();
        await review
          .getByRole('button', { name: 'Refresh learning and execution status', exact: true })
          .click();
        await review
          .getByRole('button', { name: 'Inspect the exact result for learning', exact: true })
          .click();
        const craft = (await (await request.get(base + '/api/economy')).json()).craft;
        await review.getByLabel('Your decision', { exact: true }).selectOption('reject');
        await review.getByLabel('Reason', { exact: true }).selectOption('missing_detail');
        await review
          .getByRole('button', { name: 'Record this result-specific decision', exact: true })
          .click();
        await expect(
          review.getByText(
            'Result-specific practice recorded. It is not a quality rating or proof of mastery.',
            { exact: true }
          )
        ).toBeVisible();
        expect((await (await request.get(base + '/api/economy')).json()).craft).toBe(craft);
      }
      expect(await count()).toBe(13);
      const practiced = await learningStatus(request, base);
      expect(
        practiced.competencies.items.find(
          (row: { competency: string }) => row.competency === 'delegate'
        ).independent_attempts
      ).toBe(baseline + 2);
      await new Promise(done => setTimeout(done, 31000));
      await preferences(1);
      const brief = await propose(1, true);
      expect(brief.definition.coaching_depth).toBe('brief');
      expect(brief.definition.guidance).toBe('guided');
      expect(brief.definition.hints).toHaveLength(1);
      expect(brief.definition.steps).toEqual(['scope', 'execute', 'review']);
      await expect(board).toContainText('Guided · concise optional coaching');
      await board
        .getByText(brief.definition.goal, { exact: true })
        .evaluate(element => element.scrollIntoView({ block: 'start' }));
      await board.getByText('Suggested coaching · model-authored', { exact: true }).click();
      await page.screenshot({ path: join(evidence, '25b-brief-hint-full-criteria.png') });
      await board
        .getByText(brief.definition.goal, { exact: true })
        .evaluate(element => element.scrollIntoView({ block: 'start' }));
      await page.screenshot({ path: join(evidence, '25-scoped-practice-brief-guidance.png') });
      for (let visit = 0; visit < 3; visit++) {
        await openBoard();
        expect(await count()).toBe(14);
      }
      expect(
        (await learningStatus(request, base)).instances.find(
          (row: { id: string }) => row.id === brief.id
        ).definition
      ).toEqual(brief.definition);
      expect(errors).toEqual([]);
    });
  }

  if (process.env.ORI_LEARNING_DEMO_GROUP === '5') {
    test('held replies, explicit clears, process restart and source loss preserve the correct boundaries', async ({
      page,
      request
    }) => {
      test.setTimeout(180000);
      const errors: string[] = [];
      page.on('pageerror', error => errors.push(error.message));
      const controlBase = process.env.ORI_LEARNING_DEMO_CONTROL || '';
      const headers = { 'X-Ori-Demo-Control': process.env.ORI_LEARNING_DEMO_CONTROL_TOKEN || '' };
      const control = async (path: string) => {
        const reply = await request.post(controlBase + path, { headers, timeout: 180000 });
        expect(reply.status()).toBe(200);
      };
      const held = async () =>
        (await (await request.get(controlBase + '/state', { headers })).json()).held;
      const count = async () => (await (await request.get(provider + '/counts')).json()).calls;
      const board = page.locator('#learningMissions');
      const open = async () => {
        await page.goto('/');
        await page.locator('#cockpitQuestsToggle').click();
        await expect(board).toBeVisible();
      };
      await learningPost(request, base, '/api/settings/system-model', {
        provider: 'openai',
        model: 'gpt-4o-mini'
      });
      const initial = await learningStatus(request, base);
      const original = initial.instances.find(
        (row: { state: string }) => row.state === 'completed'
      );
      const originalPath = `${base}/api/workspaces/${workspaceID}/tickets/${original.progress.ticket_id}`;
      const originalTicket = await (await request.get(originalPath)).json();
      const startHeld = async (id: string) => {
        await control('/hold');
        const saved = await learningStatus(request, base);
        const pending = request.post(base + '/api/learning-missions/proposals', {
          headers: { Origin: base },
          data: {
            operation_id: id,
            expected_revision: saved.owner.revision,
            studio_id: workspaceID,
            structure: 'delegate_inspect',
            personalize: true,
            consent_version: 1,
            model_fingerprint: saved.model.fingerprint
          }
        });
        await expect.poll(held).toBe(true);
        return { pending };
      };
      const offCall = await startHeld('held-before-off');
      await open();
      await board.getByRole('button', { name: 'Learning preferences', exact: true }).click();
      await board.getByLabel('Coaching mode', { exact: true }).selectOption('off');
      await board.getByRole('button', { name: 'Save learning preferences', exact: true }).click();
      await expect(board).toContainText('Coaching is off');
      await control('/release');
      const offReply = await offCall.pending;
      expect(offReply.status()).toBe(200);
      expect((await offReply.json()).operation.status).toBe('invalidated');
      expect(
        (await learningStatus(request, base)).instances.some(
          (row: { state: string }) => row.state === 'offered'
        )
      ).toBe(false);
      expect((await learningStatus(request, base)).competencies).toEqual(initial.competencies);
      await page.screenshot({ path: join(evidence, '26-held-off-no-resurrection.png') });
      await board.getByRole('button', { name: 'Learning preferences', exact: true }).click();
      await board.getByLabel('Coaching mode', { exact: true }).selectOption('on_request');
      await board.getByRole('button', { name: 'Save learning preferences', exact: true }).click();
      await new Promise(done => setTimeout(done, 31000));
      const clearCall = await startHeld('held-before-clear');
      await open();
      await board.getByRole('button', { name: 'Local learning activity', exact: true }).click();
      await board
        .getByLabel('Confirm clearing learning history and recorded practice — keep actual work', {
          exact: true
        })
        .check();
      await board.getByRole('button', { name: 'Clear learning history', exact: true }).click();
      await expect(board.getByRole('status')).toContainText(
        'Actual work, preferences and recent call-limit accounting remain'
      );
      await control('/release');
      expect((await clearCall.pending).status()).toBe(409);
      const cleared = await learningStatus(request, base);
      expect(cleared.instances).toEqual([]);
      expect(
        cleared.competencies.items.every((row: { attempts: number }) => row.attempts === 0)
      ).toBe(true);
      expect(await (await request.get(originalPath)).json()).toEqual(originalTicket);
      expect(
        (
          await request.get(
            base +
              '/api/learning-missions/evidence/' +
              initial.competencies.items[0].evidence_keys[0]
          )
        ).status()
      ).toBe(404);
      await page.screenshot({ path: join(evidence, '27-held-clear-keeps-actual-ticket.png') });
      await board.getByRole('button', { name: 'Choose a workspace', exact: true }).click();
      await board.getByLabel('Workspace', { exact: true }).selectOption(workspaceID);
      await board.getByRole('button', { name: 'Request a practice', exact: true }).click();
      await board
        .getByRole('button', { name: 'Start learning — no execution', exact: true })
        .click();
      await board.getByRole('button', { name: 'Prepare a draft Ticket', exact: true }).click();
      await board
        .getByLabel('Text and drafting requirements (maximum 4 KiB)', { exact: true })
        .fill(
          'Draft a fictional restart-safe invitation from these supplied details: Saturday at 11:00, bring one idea.'
        );
      await board.getByLabel('Assigned agent', { exact: true }).selectOption('Learning writer');
      await board
        .getByRole('button', { name: 'Save reviewed input as a Ticket', exact: true })
        .click();
      await expect(
        board.getByRole('link', { name: 'Open Ticket to review execution', exact: true })
      ).toBeVisible();
      const active = (await learningStatus(request, base)).instances.find(
        (row: { state: string }) => row.state === 'active'
      );
      const readyPath = `${base}/api/workspaces/${workspaceID}/tickets/${active.progress.ticket_id}`;
      const readyTicket = await (await request.get(readyPath)).json();
      expect(await count()).toBe(3);
      await control('/restart');
      await open();
      const restarted = await learningStatus(request, base);
      expect(restarted.instances.find((row: { id: string }) => row.id === active.id)).toEqual(
        active
      );
      expect(await (await request.get(readyPath)).json()).toEqual(readyTicket);
      expect(await count()).toBe(3);
      await board
        .getByRole('link', { name: 'Open Ticket to review execution', exact: true })
        .scrollIntoViewIfNeeded();
      await page.screenshot({ path: join(evidence, '28-restarted-active-contract.png') });
      await learningPost(
        request,
        base,
        '/api/personal-assistant/hire',
        {
          request_id: 'lifecycle-reset-hire',
          if_version: 0,
          display_name: 'Atlas',
          mandate: 'Keep fictional commitments visible.',
          focus_areas: ['plan_my_day']
        },
        201
      );
      await learningPost(request, base, '/api/progression/reset', {});
      expect(
        (await learningStatus(request, base)).instances.find(
          (row: { id: string }) => row.id === active.id
        )
      ).toEqual(active);
      await board.getByRole('button', { name: 'Refresh learning', exact: true }).click();
      await expect(board).not.toHaveAttribute('aria-busy', 'true');
      await page.screenshot({
        path: join(evidence, '29-getting-started-reset-keeps-learning.png')
      });
      await board
        .getByRole('link', { name: 'Open Ticket to review execution', exact: true })
        .click();
      const review = page.locator('#learningTicketReview');
      await review
        .getByRole('button', { name: 'Start this Ticket with this input and model', exact: true })
        .click();
      await expect
        .poll(
          async () =>
            (await learningStatus(request, base)).instances.find(
              (row: { id: string }) => row.id === active.id
            )?.progress.result,
          { timeout: 15000 }
        )
        .toBeTruthy();
      await review
        .getByRole('button', { name: 'Refresh learning and execution status', exact: true })
        .click();
      await review
        .getByRole('button', { name: 'Inspect the exact result for learning', exact: true })
        .click();
      await review.getByLabel('Your decision', { exact: true }).selectOption('reject');
      await review.getByLabel('Reason', { exact: true }).selectOption('missing_detail');
      await review
        .getByRole('button', { name: 'Record this result-specific decision', exact: true })
        .click();
      await expect(
        review.getByText(
          'Result-specific practice recorded. It is not a quality rating or proof of mastery.',
          { exact: true }
        )
      ).toBeVisible();
      const earned = await learningStatus(request, base);
      const key = earned.competencies.items[0].evidence_keys[0];
      const removed = await request.delete(
        `${base}/api/workspaces/${workspaceID}?confirm=true&delete_sessions=true`
      );
      expect(removed.ok(), await removed.text()).toBe(true);
      await open();
      const lost = await learningStatus(request, base);
      expect(lost.competencies).toEqual(earned.competencies);
      expect(
        lost.instances.find((row: { id: string }) => row.id === active.id).progress.blocker
      ).toBe('source_unavailable');
      expect((await request.get(base + '/api/learning-missions/evidence/' + key)).status()).toBe(
        404
      );
      await board.getByRole('button', { name: 'Recorded practice', exact: true }).click();
      await board
        .getByRole('button', { name: 'Inspect delegate review 1', exact: true })
        .first()
        .click();
      await expect(board.getByRole('status')).toContainText(
        'The supporting source or full receipt is unavailable'
      );
      await board.getByRole('status').scrollIntoViewIfNeeded();
      await page.screenshot({ path: join(evidence, '30a-missing-evidence-disclosure.png') });
      await board.getByRole('button', { name: 'Recorded practice', exact: true }).click();
      await board
        .getByRole('region', { name: 'Delegate practice', exact: true })
        .scrollIntoViewIfNeeded();
      await page.screenshot({ path: join(evidence, '30-source-loss-keeps-earned-facts.png') });
      expect(await count()).toBe(4);
      expect(errors).toEqual([]);
    });
  }
});
