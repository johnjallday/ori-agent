import { test, expect } from '@playwright/test';
import { mkdir, readFile, writeFile } from 'node:fs/promises';
import { basename, join } from 'node:path';

// Fresh isolated exact candidate; no existing Home, vendor model or live REAPER.
test('declared new Home: explicit setup persists resulting parent and reload reuses outcome', async ({
  page,
  request
}) => {
  const sandbox = process.env.ORI_MUSIC_REAPER_SANDBOX;
  const portfolio = process.env.ORI_WORKSPACE_COLLECTION_ACCEPTANCE === '1';
  const evidencePrefix = portfolio ? 'portfolio-new-home' : 'new-home';
  test.skip(
    process.env.ORI_WORKSPACE_NEW_HOME_ACCEPTANCE !== '1' || !sandbox,
    'Use assistant-workspace-demo.py --new-home with both exact candidate sources'
  );
  test.setTimeout(180_000);
  expect(basename(sandbox!)).toMatch(/^ori-reaper-demo\./);
  const evidence = join(process.cwd(), 'tasks', 'evidence-assistant-workspace-awareness');
  await mkdir(evidence, { recursive: true, mode: 0o750 });
  await request.post('/api/onboarding/skip');
  expect(
    (await request.post('/api/settings/workspace-root', { data: { workspace_root: '' } })).ok()
  ).toBeTruthy();
  const initial = (await (await request.get('/api/personal-assistant')).json()).personal_assistant;
  const hire = await request.post('/api/personal-assistant/hire', {
    data: {
      request_id: 'new-home-hire',
      if_version: initial.state_version,
      display_name: 'Atlas',
      mandate: 'Review fixture projects.',
      focus_areas: ['plan_my_day']
    }
  });
  expect(hire.status(), await hire.text()).toBe(201);
  const hired = (await hire.json()).personal_assistant;
  const hq = await request.post('/api/personal-assistant/hq', {
    data: { request_id: 'new-home-hq', if_version: hired.state_version, name: 'My HQ' }
  });
  expect(hq.status(), await hq.text()).toBe(201);
  expect(
    (
      await request.post('/api/settings/system-model', {
        data: { provider: 'ollama', model: 'ori-workspace-fixture' }
      })
    ).ok()
  ).toBeTruthy();
  const before = (await (await request.get('/api/workspaces')).json()).folders;
  expect(before.filter((row: any) => row.kind === 'group')).toHaveLength(0);
  const folder = portfolio
    ? join(sandbox!, 'Documents')
    : join(sandbox!, 'Documents', 'New Home Album fixture');
  await mkdir(folder, { recursive: true, mode: 0o750 });
  const source = join(folder, portfolio ? 'sentinel.txt' : 'new-home.rpp');
  const bytes = '<REAPER_PROJECT 0.1 "7.0" 1234\n  TEMPO 120 4 4\n>\n';
  await writeFile(source, bytes, { mode: 0o600 });
  if (portfolio) {
    for (const name of ['Album-1', 'Album-2', 'Album-3', 'Album-4', 'Album-5']) {
      const album = join(folder, name);
      await mkdir(album, { recursive: true, mode: 0o750 });
      await writeFile(join(album, 'album.rpp'), bytes, { mode: 0o600 });
    }
  }
  await page.goto('/');
  await page.locator('#personalAssistantLauncher').click();
  await page.locator('#personalAssistantInput').fill('Add this to my workspace');
  await page.locator('#personalAssistantFolderChip').click();
  await page
    .locator('#personalAssistantFolderChoices')
    .getByRole('button', { name: 'Documents', exact: true })
    .click();
  await page.getByRole('button', { name: 'Review workspace setup', exact: true }).click();
  const candidate = await page
    .locator('#personalAssistantFolderSetupCandidate option')
    .filter({ hasText: portfolio ? 'Documents (whole folder)' : 'New Home Album fixture' })
    .getAttribute('value');
  expect(candidate).toBeTruthy();
  await page.locator('#personalAssistantFolderSetupCandidate').selectOption(candidate!);
  await page.getByRole('button', { name: 'Review selection', exact: true }).click();
  const card = page.locator('#homeAssistantConversation #personalAssistantFolderOffer');
  await expect(card).toBeVisible({ timeout: 60_000 });
  const current = await page.evaluate(() =>
    (window as any).PersonalAssistantFolderContext.current()
  );
  const historyURL = `/api/home-assistant/conversations/${current.conversationId}`;
  const pending = await (await request.get(historyURL)).json();
  const review = pending.folder_reviews[current.offerId];
  await writeFile(
    join(evidence, `${evidencePrefix}-preparation.json`),
    JSON.stringify(
      {
        status: review.status,
        subject: review.subject,
        destination: review.destination,
        plan: review.plan,
        blocker: review.setup_unavailable_reason,
        boundary: 'Exact local candidate preview only; no vendor model or live REAPER'
      },
      null,
      2
    ),
    { mode: 0o600 }
  );
  expect(review.destination).toMatchObject({ status: 'new', kind: 'home' });
  expect(review.destination.name).toBeTruthy();
  expect(review.destination.workspace_id).toBeFalsy();
  expect(review.plan).toBeTruthy();
  expect(review.plan.destination).toEqual(review.destination);
  await expect(card).toContainText(review.destination.name);
  expect((await (await request.get('/api/workspaces')).json()).folders).toHaveLength(before.length);
  await card.screenshot({ path: join(evidence, `${evidencePrefix}-before-confirmation.png`) });
  await card.getByRole('button', { name: 'Set up', exact: true }).click();
  await expect
    .poll(
      async () => (await (await request.get(historyURL)).json()).folder_review_context?.status,
      { timeout: 120_000 }
    )
    .toBe('completed');
  const saved = await (await request.get(historyURL)).json();
  const completed = saved.folder_reviews[current.offerId];
  if (!portfolio)
    expect(completed.outcome.parent).toMatchObject({
      status: 'existing',
      kind: 'home',
      name: review.destination.name
    });
  const rows = (await (await request.get('/api/workspaces')).json()).folders;
  expect(rows.filter((row: any) => row.kind === 'group')).toHaveLength(1);
  if (portfolio) {
    expect(review.portfolio).toBeTruthy();
    expect(rows.find((row: any) => row.id === completed.outcome.workspace_id)).toMatchObject({
      kind: 'group',
      name: review.destination.name
    });
    expect(completed.outcome.receipt.some((row: any) => row.kind === 'library')).toBe(true);
  } else {
    expect(rows.find((row: any) => row.id === completed.outcome.workspace_id).parent_id).toBe(
      completed.outcome.parent.workspace_id
    );
    expect(rows).toHaveLength(before.length + 2);
  }
  expect(await readFile(source, 'utf8')).toBe(bytes);
  if (portfolio)
    for (const name of ['Album-1', 'Album-2', 'Album-3', 'Album-4', 'Album-5'])
      expect(await readFile(join(folder, name, 'album.rpp'), 'utf8')).toBe(bytes);
  await expect(page.locator('#personalAssistantInput')).toHaveValue('Add this to my workspace');
  await page.reload();
  await expect(card).toBeVisible();
  await expect(card).toContainText(review.destination.name);
  // Re-review the same canonical candidate cannot allocate another parent/project.
  const replay = await request.post('/api/home-assistant/folder-context/review', {
    data: {
      conversation_id: current.conversationId,
      revision: saved.folder_context.revision,
      selection_id: current.observation.id,
      candidate_id: candidate
    }
  });
  expect(replay.ok(), await replay.text()).toBeTruthy();
  const recovered = await (await request.get(historyURL)).json();
  expect(recovered.folder_context.offer_id).toBe(current.offerId);
  // The outcome's memory note is written by a hook that runs just after the
  // review reports completed, so the first read may precede it. Everything that
  // says what was set up must be identical; a note already read must not change.
  const { note: noteBefore, ...outcomeBefore } = completed.outcome;
  const { note: noteAfter, ...outcomeAfter } = recovered.folder_reviews[current.offerId].outcome;
  expect(outcomeAfter).toEqual(outcomeBefore);
  if (noteBefore !== undefined) expect(noteAfter).toBe(noteBefore);
  expect((await (await request.get('/api/workspaces')).json()).folders).toHaveLength(rows.length);
  await page.screenshot({ path: join(evidence, `${evidencePrefix}-completed.png`) });
  await writeFile(
    join(evidence, `${evidencePrefix}-completed.json`),
    JSON.stringify(
      {
        subject: completed.subject,
        destination: completed.destination,
        resultingParent: completed.outcome.parent,
        receipt: completed.outcome.receipt,
        workspaceCount: rows.length,
        sourceUnchanged: true,
        draftUnchanged: true,
        boundary:
          'Explicit drawer setup and reload/re-review against exact local candidates and loopback provider; no task/result workspace opened, vendor model or live REAPER'
      },
      null,
      2
    ),
    { mode: 0o600 }
  );
});
