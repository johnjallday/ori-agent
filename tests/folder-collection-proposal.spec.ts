import { expect, test, type APIRequestContext, type Page } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import { createHash, randomUUID } from 'node:crypto';
import { lstatSync, readFileSync, readdirSync, realpathSync } from 'node:fs';
import { join, relative } from 'node:path';
import { chooseAttach, chooseConversation } from './helpers/assistant-menus';
import {
  collectionModel, existingCollectionHome,
  watchFolderRequests as watch, expectNoFolderExecution as expectNoExecution
} from './helpers/folder-aware-collection';

// A folder that holds several projects, from the moment it is shown to the
// group and workspaces it becomes, in a real browser against the real server
// and store.
//
// Run it with its own wrapper, which starts one isolated demo server per phase
// on a sandbox of its own and seeds the folder with
// scripts/folder-collection-seed.sh:
//
//   ./scripts/e2e-folder-collection.sh
//
// Recognition and setup use the real host and store. The existing-Home phase
// uses published reviewed packages and an explicitly controlled loopback model
// for staffing; all other phases have no model or an explicitly failing model.
// No real folder is touched: the folders are in the server's HOME, which is the
// sandbox.
const sandbox = process.env.ORI_COLLECTION_SANDBOX || '';
const phase = process.env.ORI_COLLECTION_PHASE || '';
test.skip(
  !sandbox ||
    ![
      'composer',
      'home',
      'declined',
      'model-down',
      'existing-home',
      'no-home',
      'generic',
      'pending-return'
    ].includes(phase),
  'requires the sandbox and phase that scripts/e2e-folder-collection.sh provides'
);
test.describe.configure({ mode: 'serial' });
test.setTimeout(180_000);
test.use({ actionTimeout: 15_000 });

const members = ['Album-1', 'Album-2', 'Album-3', 'Album-4', 'Album-5', 'Logic Sketch'];

// hashFolder digests every entry's relative name, kind and bytes.
function hashFolder(root: string) {
  const lines: string[] = [];
  const walk = (dir: string) => {
    for (const name of readdirSync(dir).sort()) {
      const full = join(dir, name);
      const rel = relative(root, full);
      if (lstatSync(full).isDirectory()) {
        lines.push(`d ${rel}`);
        walk(full);
      } else
        lines.push(`f ${rel} ${createHash('sha256').update(readFileSync(full)).digest('hex')}`);
    }
  };
  walk(root);
  return createHash('sha256').update(lines.join('\n')).digest('hex');
}

async function ok(response: Awaited<ReturnType<APIRequestContext['get']>>) {
  const text = await response.text();
  expect(response.ok(), `${response.url()}: ${text}`).toBeTruthy();
  return text ? JSON.parse(text) : {};
}

// hireAssistant brings a fresh server to the point a person reaches after
// onboarding: an assistant, and the HQ it works from. No model is chosen.
async function hireAssistant(request: APIRequestContext) {
  await request.post('/api/onboarding/skip');
  await ok(await request.post('/api/settings/workspace-root', { data: { workspace_root: '' } }));
  let relationship = (await ok(await request.get('/api/personal-assistant'))).personal_assistant;
  if (relationship.state !== 'needs_hire') return;
  await ok(
    await request.post('/api/personal-assistant/hire', {
      data: {
        request_id: 'collection-e2e-hire',
        if_version: relationship.state_version ?? 0,
        display_name: 'Atlas',
        mandate: 'Keep my projects moving.',
        focus_areas: ['plan_my_day']
      }
    })
  );
  relationship = (await ok(await request.get('/api/personal-assistant'))).personal_assistant;
  await ok(
    await request.post('/api/personal-assistant/hq', {
      data: {
        request_id: 'collection-e2e-hq',
        if_version: relationship.state_version,
        name: 'My HQ',
        timezone: 'UTC'
      }
    })
  );
}

type Listed = {
  id: string;
  name: string;
  kind: string;
  parent_id?: string;
  open_task_count?: number;
  shared_data?: Record<string, unknown>;
  directory_references?: { id: string; path: string }[];
};

async function workspaces(request: APIRequestContext) {
  const listed = await ok(await request.get('/api/workspaces'));
  return (Array.isArray(listed) ? listed : listed.folders || listed.workspaces || []) as Listed[];
}

// groupOf waits for the group of this name and returns it with its workspaces.
async function groupOf(request: APIRequestContext, name: string, children: number) {
  await expect
    .poll(
      async () => {
        const all = await workspaces(request);
        const group = all.find(item => item.kind === 'group' && item.name === name);
        return group ? all.filter(item => item.parent_id === group.id).length : -1;
      },
      { timeout: 90_000 }
    )
    .toBe(children);
  const all = await workspaces(request);
  const group = all.find(item => item.kind === 'group' && item.name === name)!;
  return { group, children: all.filter(item => item.parent_id === group.id) };
}

// Each workspace is linked to its own folder, where it is, and was given no task.
function expectLinked(children: Listed[], root: string) {
  const linked = children
    .map(child => {
      const primary = String(child.shared_data?.primary_directory_id || '');
      const reference = (child.directory_references || []).find(item => item.id === primary);
      expect(reference, `${child.name} has no primary directory`).toBeTruthy();
      expect(child.kind).toBe('workspace');
      expect(child.open_task_count || 0, `${child.name} was given a task`).toBe(0);
      return reference!.path;
    })
    .sort();
  expect(linked).toEqual(members.map(name => join(realpathSync(root), name)).sort());
}

// attachFolder opens the drawer and attaches a folder from the composer's
// chooser, returning the attach response.
async function attachFolder(page: Page, name: string) {
  await page.goto('/');
  await page.waitForFunction(() => Boolean((window as any).PersonalAssistantFolderContext));
  if (!(await page.locator('#personalAssistantPanel').isVisible())) {
    await expect(page.locator('#personalAssistantLauncher')).toBeVisible({ timeout: 30_000 });
    await page.locator('#personalAssistantLauncher').click();
  }
  await chooseAttach(page, '#personalAssistantFolderChip');
  const attached = page.waitForResponse(
    response =>
      response.request().method() === 'POST' &&
      /\/api\/home-assistant\/folder-context\/select$/.test(response.url()),
    { timeout: 60_000 }
  );
  await page
    .locator('#personalAssistantFolderChoices')
    .getByRole('button', { name, exact: true })
    .click();
  const response = await attached;
  expect(response.ok()).toBeTruthy();
  return response.json();
}

const card = (page: Page) => page.locator('#personalAssistantFolderOffer');
const planLines = async (page: Page) => {
  const details = card(page).locator('#personalAssistantFolderDetails');
  if (!(await details.evaluate(node => (node as HTMLDetailsElement).open)))
    await details.getByText('Full setup details', { exact: true }).click();
  return (await card(page).innerText()).split('\n').map(line => line.trim());
};

// Choosing the alternative opens a canonical group review; it does not create
// anything. The unmarked-sketch regression uses the same explicit choice.
async function reviewGeneric(page: Page) {
  const proposal = page.locator('#personalAssistantFolderStructure');
  await proposal.getByRole('button', { name: 'Choose another setup…', exact: true }).click();
  const sheet = proposal.locator('[data-folder-adjust]');
  await sheet.locator('input[value="group"]').check();
  await sheet.getByRole('button', { name: 'Done', exact: true }).click();
  await expect(card(page)).toBeVisible({ timeout: 60_000 });
  await expect(card(page)).toContainText('Creates group');
}

function expectExactAlbums(selected: any) {
  expect(selected.observation.entries).toBe(15);
  expect(selected.observation.files).toBe(8);
  expect(selected.structure.members).toHaveLength(6);
  // Discovery is independent of the editable generic structure. Only the
  // explicit review action adopts the recommended operation into consent.
  expect(selected.structure.recommendation.operation).toBe('library');
}

test.describe('[collection:composer] the legacy unmarked-sketch collection', () => {
  test.skip(phase !== 'composer', 'runs in the composer phase');
  const root = join(sandbox, 'Downloads');

  const attach = (page: Page) => attachFolder(page, 'Downloads');

  test('offers an explicit generic review and creates it only after confirmation', async ({
    page,
    request
  }) => {
    await hireAssistant(request);
    const before = { hash: hashFolder(root), workspaces: (await workspaces(request)).length };
    const seen = watch(page);

    // The proposal is on screen before anything is typed or sent.
    const selected = await attach(page);
    expect(selected.structure?.members?.length).toBe(6);
    const proposal = page.locator('#personalAssistantFolderStructure');
    await expect(proposal).toBeVisible({ timeout: 30_000 });
    await expect(proposal).toContainText('5 REAPER session folders');
    await expect(proposal).not.toContainText('1 Logic Pro project folder');
    await expect(proposal.getByRole('button', { name: 'Review library setup' })).toBeVisible();
    expectNoExecution(seen);
    await reviewGeneric(page);
    await expect(card(page)).toBeVisible({ timeout: 30_000 });
    await expect(card(page)).toContainText('Set up Downloads as a group?');
    const lines = await planLines(page);
    expect(lines).toContain('Creates group Downloads');
    expect(lines).toContain('Creates 6 workspaces, each linked to its own folder');
    expect(lines).toContain('Nothing is moved, copied or read');
    expect(lines).toContain('No task is created or started');
    // The plan lists each workspace once: the card does not repeat the proposal.
    expect(lines.filter(line => line === 'Album-1').length).toBe(1);
    expect((await workspaces(request)).length, 'reviewing created a workspace').toBe(
      before.workspaces
    );

    await card(page).getByRole('button', { name: 'Set up', exact: true }).click();
    // The receipt is the end of the run; a workspace exists a moment before its
    // folder is linked, so nothing is read until the run says it is done.
    const done = card(page);
    await expect(done).toContainText('Workspace · Downloads (group)', { timeout: 90_000 });
    await expect(done).toContainText('No automatic task');
    if (process.env.ORI_VISUAL_EVIDENCE) {
      await page.setViewportSize({ width: 1440, height: 1100 });
      await page.evaluate(() =>
        (window as any).PersonalAssistantTranscript?.revealControl(
          document.getElementById('personalAssistantFolderOfferHeadline'),
          true
        )
      );
      await page.screenshot({
        path: `${process.env.ORI_VISUAL_EVIDENCE}/04-real-group-result.png`
      });
    }
    await expect(done.getByRole('link', { name: 'Open Downloads' })).toBeVisible();
    const made = await groupOf(request, 'Downloads', 6);
    expectLinked(made.children, root);
    expect((await workspaces(request)).length).toBe(before.workspaces + 7);

    expect(hashFolder(root), 'the attached folder changed').toBe(before.hash);
    expect(seen.asks, 'a model was asked').toEqual([]);
    expect(seen.errors).toEqual([]);
  });

  test('reattachment does not duplicate the already-linked group or workspaces', async ({
    page,
    request
  }) => {
    const before = (await workspaces(request)).length;
    const seen = watch(page);
    const selected = await attach(page);
    expect(selected.structure).toBeUndefined();
    await expect(page.locator('#personalAssistantFolderStructure')).toBeHidden();
    expectNoExecution(seen);
    expect((await workspaces(request)).length).toBe(before);
    expect(seen.errors).toEqual([]);
  });
});

test.describe('[collection:home] the legacy collection explored from Home', () => {
  test.skip(phase !== 'home', 'runs in the home phase');
  const root = join(sandbox, 'Music');

  test('offers explicit planning and a rule-based manual review without a model', async ({
    page,
    request
  }, info) => {
    await hireAssistant(request);
    const before = { hash: hashFolder(root), workspaces: (await workspaces(request)).length };
    const seen = watch(page);

    await page.goto('/?panel=today&folder=show');
    const chooser = page.locator('#personalAssistantFolderChoices');
    await expect(chooser).toBeVisible({ timeout: 30_000 });
    const scanned = page.waitForResponse(
      response =>
        response.request().method() === 'POST' && /\/folder-context\/select$/.test(response.url()),
      { timeout: 60_000 }
    );
    await chooser.getByRole('button', { name: 'Music', exact: true }).click();
    const selected = await (await scanned).json();
    expect(selected.structure?.members?.length).toBe(6);
    await expect(page.locator('#personalAssistantFolderStructure')).toContainText(
      '5 REAPER session folders'
    );
    await chooseAttach(page, '#personalAssistantArrangeFolder');
    const planning = page.locator('#personalAssistantArrangement');
    await expect(
      planning.getByText('Configure a tool-capable API model', { exact: false })
    ).toBeVisible();
    await expect(planning.getByRole('button', { name: 'Send organizing goal' })).toBeDisabled();
    await page.screenshot({ path: info.outputPath('explore-no-model-manual.png'), fullPage: true });
    await chooseAttach(page, '#personalAssistantArrangeFolder');
    await reviewGeneric(page);

    await expect(card(page)).toBeVisible({ timeout: 30_000 });
    await card(page).scrollIntoViewIfNeeded();
    await expect(page.locator('#personalAssistantFolderOfferHeadline')).toContainText(
      'Music has 5'
    );
    await expect(page.locator('#personalAssistantFolderOfferQuestion')).toContainText(
      'Set up Music as a group?'
    );
    expect(
      await card(page)
        .locator('#personalAssistantFolderOfferActions')
        .locator('button, a')
        .allInnerTexts()
    ).toEqual(['Set up', 'Adjust…', 'Keep chatting']);
    expect(await planLines(page)).toContain('Creates group Music');

    // Listing the folder in its Home is the other choice in Adjust. With the
    // Home's plugin not installed it is listed, unavailable, with why: it is
    // never the dead end of a review that cannot proceed.
    await card(page).getByRole('button', { name: 'Adjust…', exact: true }).click();
    const types = card(page).locator('[data-folder-adjust]').first();
    await expect(types).toContainText('Add to Music Production Home instead');
    await expect(types).toContainText('Music Production Home is not installed.');
    await expect(types.locator('input[type=radio][value=library]')).toBeDisabled();
    await expect(types.locator('input[type=radio][value=group]')).toBeChecked();
    await types.getByRole('button', { name: 'Cancel', exact: true }).click();
    await expect(page.locator('#personalAssistantFolderOfferQuestion')).toContainText(
      'Set up Music as a group?'
    );
    expect((await workspaces(request)).length, 'opening Adjust created a workspace').toBe(
      before.workspaces
    );

    // Adjust renames the group in the card; the plan is re-stamped in place.
    await card(page).getByRole('button', { name: 'Adjust…', exact: true }).click();
    const sheet = card(page).locator('[data-folder-adjust]').first();
    await expect(sheet).toBeVisible({ timeout: 15_000 });
    const groupName = sheet.locator('input[type=text], input:not([type])').first();
    // A name a workspace already has is refused in the sheet, with the reason.
    await groupName.fill('My HQ');
    await sheet.getByRole('button', { name: 'Done', exact: true }).click();
    await expect(
      page
        .getByRole('status')
        .filter({ hasText: 'A workspace named My HQ already exists. Nothing was saved' })
    ).toBeVisible();
    await expect(card(page)).toContainText('Creates group Music');
    await card(page).getByRole('button', { name: 'Adjust…', exact: true }).click();
    await groupName.fill('Studio');
    await sheet.getByRole('button', { name: 'Done', exact: true }).click();
    await expect(card(page)).toContainText('Creates group Studio', { timeout: 30_000 });
    expect((await workspaces(request)).length, 'adjusting created a workspace').toBe(
      before.workspaces
    );

    await card(page).getByRole('button', { name: 'Set up', exact: true }).click();
    const done = card(page);
    await expect(done).toContainText('Workspace · Studio (group)', { timeout: 90_000 });
    await expect(done.getByRole('link', { name: 'Open Studio' })).toBeVisible();
    const made = await groupOf(request, 'Studio', 6);
    expectLinked(made.children, root);
    expect((await workspaces(request)).length).toBe(before.workspaces + 7);

    expect(hashFolder(root), 'the explored folder changed').toBe(before.hash);
    expect(seen.asks, 'a model was asked').toEqual([]);
    expect(seen.errors).toEqual([]);
  });
});

test.describe('[collection:declined] a collection declined earlier', () => {
  test.skip(phase !== 'declined', 'runs in the declined phase');
  const root = join(sandbox, 'Downloads');

  test('is still proposed, says when, and Ask again brings Set up back', async ({
    page,
    request
  }) => {
    await hireAssistant(request);
    // The HQ's folder digest exists once its first-folder prompt is recorded;
    // the seed then writes the old No thanks into it, as one sat in a real HQ.
    await ok(await request.post('/api/personal-assistant/folder-digest/prompted'));
    execFileSync(join(process.cwd(), 'scripts', 'folder-collection-seed.sh'), [
      '--tombstone',
      sandbox,
      'Downloads'
    ]);
    const before = { hash: hashFolder(root), workspaces: (await workspaces(request)).length };
    const seen = watch(page);

    const selected = await attachFolder(page, 'Downloads');
    expect(selected.structure?.members?.length).toBe(6);
    expect(selected.declined?.name).toBe('Downloads');
    const proposal = page.locator('#personalAssistantFolderStructure');
    await expect(proposal).toContainText('5 REAPER session folders');
    // The No thanks stands where Set up would, with when.
    const askAgain = proposal.getByRole('button', {
      name: 'You said no to Downloads on Oct 6 · Ask again',
      exact: true
    });
    await expect(askAgain).toBeVisible();
    await expect(proposal.getByRole('button', { name: 'Review library setup' })).toHaveCount(0);

    const forgot = page.waitForResponse(
      response =>
        response.request().method() === 'POST' &&
        /\/api\/personal-assistant\/folder-digest\/forget$/.test(response.url())
    );
    await askAgain.click();
    const forget = await forgot;
    expect(forget.ok()).toBeTruthy();
    expect(Object.keys(forget.request().postDataJSON())).toEqual(['folder_key']);
    await expect(proposal.getByRole('button', { name: 'Review library setup' })).toBeEnabled();
    await reviewGeneric(page);
    await expect(card(page)).toContainText('Set up Downloads as a group?', { timeout: 30_000 });
    await card(page).getByRole('button', { name: 'Set up', exact: true }).click();
    const done = card(page);
    await expect(done).toContainText('Workspace · Downloads (group)', { timeout: 90_000 });
    const made = await groupOf(request, 'Downloads', 6);
    expectLinked(made.children, root);
    expect((await workspaces(request)).length).toBe(before.workspaces + 7);

    expect(hashFolder(root), 'the attached folder changed').toBe(before.hash);
    expect(seen.asks, 'a model was asked').toEqual([]);
    expect(seen.errors).toEqual([]);
  });
});

test.describe('[collection:model-down] a collection while the model cannot answer', () => {
  test.skip(phase !== 'model-down', 'runs in the model-down phase');

  test('keeps its card, says so in one line, and Retry sends the same turn again', async ({
    page,
    request
  }) => {
    await hireAssistant(request);
    // This phase's server has Ollama pointed at a closed port: every turn fails.
    await ok(
      await request.post('/api/settings/system-model', {
        data: { provider: 'ollama', model: 'ori-e2e-unreachable' }
      })
    );
    const seen = watch(page);
    const asked: { prompt: string; folder_context: unknown }[] = [];
    page.on('request', item => {
      if (/\/api\/home-assistant\/ask$/.test(new URL(item.url()).pathname))
        asked.push(item.postDataJSON());
    });

    await attachFolder(page, 'Downloads');
    const proposal = page.locator('#personalAssistantFolderStructure');
    await expect(proposal).toContainText('5 REAPER session folders');
    const panel = page.locator('#personalAssistantPanel');
    // The line shares its message row with the turn's own status ("App-wide").
    const failure = panel.getByText(
      'Assistant could not answer (the model is unavailable). Your draft is kept.'
    );
    const settled = () =>
      page.waitForFunction(() => !(window as any).OriAskRouting.getState().busy, null, {
        timeout: 60_000
      });

    // An empty Send asks for the proposal.
    await page.locator('#personalAssistantSend').click();
    await expect.poll(() => asked.length, { timeout: 60_000 }).toBe(1);
    await settled();
    await expect(failure).toHaveCount(1);
    await expect(panel).not.toContainText(/not completed/i);
    await expect(panel).not.toContainText('System conversation model');
    await expect(panel.getByRole('button', { name: 'Open workspace form manually' })).toHaveCount(
      0
    );
    // The card never depended on the model: it is still there, with Set up.
    await expect(proposal).toContainText('5 REAPER session folders');
    await expect(proposal.getByRole('button', { name: 'Review library setup' })).toBeVisible();

    await panel.getByRole('button', { name: 'Retry', exact: true }).click();
    await expect.poll(() => asked.length, { timeout: 60_000 }).toBe(2);
    await settled();
    // The retried turn replaces the failed line; it is never stacked.
    await expect(failure).toHaveCount(1);
    expect(asked[0].prompt).toBe('Explain the setup recommendation for this folder');
    expect(asked[1].prompt).toBe(asked[0].prompt);
    expect(asked[1].folder_context).toEqual(asked[0].folder_context);
    expect(seen.asks).toHaveLength(2);
    expect(seen.errors).toEqual([]);
  });
});

test.describe('[collection:existing-home] exact Albums in a compatible Home', () => {
  test.skip(phase !== 'existing-home', 'runs in the existing-home phase');

  test('completes the exact library listing and preserves its identity on replay and reload', async ({
    page,
    request
  }, info) => {
    test.setTimeout(300_000);
    const model = await collectionModel(Number(process.env.ORI_COLLECTION_MODEL_PORT || 0));
    try {
      await hireAssistant(request);
      const home = await existingCollectionHome(request, sandbox);
      await info.attach('reviewed-packages-and-home-receipt', {
        body: JSON.stringify(home, null, 2),
        contentType: 'application/json'
      });
      await ok(
        await request.post('/api/settings/system-model', {
          data: { provider: 'lmstudio', model: 'collection-fixture' }
        })
      );
      const root = join(sandbox, 'Downloads');
      const before = { hash: hashFolder(root), workspaces: await workspaces(request) };
      const library = `/api/workspaces/${home.id}/assistant-program/library`;
      expect((await ok(await request.get(`${library}/summary`))).initialized).toBe(false);
      const seen = watch(page);
      const selected = await attachFolder(page, 'Downloads');
      expectExactAlbums(selected);
      const proposal = page.locator('#personalAssistantFolderStructure');
      await expect(proposal).toContainText('5 REAPER session folders, 1 Logic Pro project folder');
      await expect(proposal).toContainText('your Music Home collection fixture library');
      await proposal.getByRole('button', { name: 'Review library setup', exact: true }).click();
      await expect(card(page)).toContainText('Lists the 6 music projects', { timeout: 90_000 });
      const reviewed = await page.evaluate(() => (window as any).PersonalAssistantFolder.current());
      expect(reviewed.plan.destination.workspace_id).toBe(home.id);
      expect(reviewed.structure.group.type).toBe('library');
      await planLines(page);
      await expect(card(page)).toContainText('collection-fixture');
      await expect(card(page)).not.toContainText('Creates 6 workspaces');
      expect(await workspaces(request)).toEqual(before.workspaces);
      expect(hashFolder(root)).toBe(before.hash);
      expect(model.calls.filter(call => !call.daily)).toEqual([]);
      expectNoExecution(seen);
      await page.screenshot({ path: info.outputPath('exact-existing-home-review.png') });

      const accepted = page.waitForResponse(
        response =>
          response.request().method() === 'POST' &&
          response.url().endsWith(`/offers/${reviewed.id}/setup`)
      );
      await card(page).getByRole('button', { name: 'Set up', exact: true }).click();
      const acceptedResponse = await accepted;
      await ok(acceptedResponse);
      const acceptedBody = acceptedResponse.request().postDataJSON();
      await expect
        .poll(
          () => page.evaluate(() => (window as any).PersonalAssistantFolder.current()?.status),
          { timeout: 180_000 }
        )
        .toBe('resolved');
      const completed = await page.evaluate(() =>
        (window as any).PersonalAssistantFolder.current()
      );
      expect(completed.outcome).toMatchObject({
        kind: 'home',
        workspace_id: home.id,
        existing: true
      });
      expect(completed.outcome.receipt).toEqual(
        expect.arrayContaining([
          expect.objectContaining({ kind: 'home', operation: 'reuse' }),
          expect.objectContaining({ kind: 'library', name: 'Listed 6 music projects in Downloads' })
        ])
      );
      const roots = await ok(await request.get(`${library}/roots`));
      const projects = await ok(await request.get(`${library}/projects`));
      const summary = await ok(await request.get(`${library}/summary`));
      expect(roots.roots).toHaveLength(1);
      expect(roots.roots[0].path).toBe(realpathSync(root));
      expect(summary.digest).toMatchObject({
        root_id: roots.roots[0].id,
        projects: 6,
        coverage: 'complete',
        unsupported_format: 1,
        activatable: 5,
        needs_file_choice: 1
      });
      expect(projects.rows.map((row: any) => row.name).sort()).toEqual([...members].sort());
      expect(projects.rows.filter((row: any) => row.format === 'reaper')).toHaveLength(5);
      expect(projects.rows.every((row: any) => row.connection === 'catalog_only')).toBe(true);
      expect(projects.rows.find((row: any) => row.name === 'Album-5')).toMatchObject({
        alternatives: 2,
        needs_choice: true,
        can_open: true
      });
      const logic = projects.rows.find((row: any) => row.name === 'Logic Sketch');
      expect(logic.format).toBe('logic');
      expect(Boolean(logic.can_open)).toBe(false);
      const after = await workspaces(request);
      expect(after.map(item => item.id).sort()).toEqual(
        before.workspaces.map(item => item.id).sort()
      );
      expect(after.filter(item => item.parent_id === home.id)).toEqual([]);
      expect(hashFolder(root)).toBe(before.hash);
      await page.screenshot({ path: info.outputPath('exact-library-completion.png') });

      const repeat = await ok(
        await request.post(`/api/personal-assistant/folder-digest/offers/${reviewed.id}/setup`, {
          data: acceptedBody
        })
      );
      expect(repeat.offer.outcome.workspace_id).toBe(home.id);
      await page.reload();
      if (!(await page.locator('#personalAssistantPanel').isVisible()))
        await page.locator('#personalAssistantLauncher').click();
      await expect(card(page)).toContainText('Music Home collection fixture', { timeout: 30_000 });
      await page.goto(completed.outcome.receipt.find((row: any) => row.kind === 'home').route);
      await expect(page.locator('#projectLibraryPanel')).toBeVisible();
      await expect(page.locator('#projectLibraryRoots')).toContainText('Downloads');
      await expect(page.locator('#projectLibraryRows')).toContainText('Album-5');
      await expect(page.locator('#projectLibraryRows')).toContainText('Logic Sketch');
      await attachFolder(page, 'Downloads');
      await expect(page.locator('#personalAssistantFolderStructure')).toContainText(
        'your Music Home collection fixture library'
      );
      expect(await workspaces(request)).toEqual(after);
      expect((await ok(await request.get(`${library}/roots`))).roots).toEqual(roots.roots);
      expect((await ok(await request.get(`${library}/projects`))).rows).toEqual(projects.rows);
      expect(hashFolder(root)).toBe(before.hash);
      expect(seen.asks).toEqual([]);
      expect(seen.errors).toEqual([]);
      await info.attach('exact-library-completion-and-repeat', {
        body: JSON.stringify(
          { completed, roots, projects, summary, repeat, calls: model.calls },
          null,
          2
        ),
        contentType: 'application/json'
      });
    } finally {
      await model.close();
    }
  });
});

test.describe('[collection:no-home] exact Albums without its Home provider', () => {
  test.skip(phase !== 'no-home', 'runs in the no-home phase');

  test('reviews named Home prerequisites without creating, installing, or losing the draft', async ({
    page,
    request
  }, info) => {
    await hireAssistant(request);
    const root = join(sandbox, 'Downloads');
    const before = { hash: hashFolder(root), workspaces: await workspaces(request) };
    const seen = watch(page);
    expectExactAlbums(await attachFolder(page, 'Downloads'));
    const draft = 'Keep this exact Albums note unsent 🎼';
    await page.locator('#personalAssistantInput').fill(draft);
    await page
      .locator('#personalAssistantFolderStructure')
      .getByRole('button', { name: 'Review library setup', exact: true })
      .click();
    await expect(card(page)).toContainText('Music Project Management', { timeout: 60_000 });
    await expect(card(page)).toContainText('REAPER');
    await expect(
      card(page).getByRole('button', { name: 'Check again', exact: true })
    ).toBeVisible();
    await expect(card(page).getByRole('button', { name: 'Set up', exact: true })).toHaveCount(0);
    const current = await page.evaluate(() => (window as any).PersonalAssistantFolder.current());
    expect(current.destination_status).toBe('unavailable');
    expect(current.destination_blocker.home_provider_key).toBe('music_project_management');
    expect(current.prerequisites_available).toBe(true);
    await card(page).getByRole('button', { name: 'Check again', exact: true }).click();
    await expect(card(page)).toContainText('Music Project Management');
    await expect(page.locator('#personalAssistantInput')).toHaveValue(draft);
    expect(await workspaces(request)).toEqual(before.workspaces);
    expect(hashFolder(root)).toBe(before.hash);
    expectNoExecution(seen);
    await page.screenshot({ path: info.outputPath('exact-no-home-readiness.png') });
    await card(page).getByRole('button', { name: 'Keep chatting', exact: true }).click();
    await expect(page.locator('#personalAssistantInput')).toHaveValue(draft);
  });
});

test.describe('[collection:generic] exact Albums with an explicit plain group', () => {
  test.skip(phase !== 'generic', 'runs in the generic phase');

  test('creates the six chosen workspaces only after its separate group confirmation', async ({
    page,
    request
  }, info) => {
    await hireAssistant(request);
    const root = join(sandbox, 'Downloads');
    const before = { hash: hashFolder(root), workspaces: await workspaces(request) };
    const seen = watch(page);
    expectExactAlbums(await attachFolder(page, 'Downloads'));
    await reviewGeneric(page);
    const current = await page.evaluate(() => (window as any).PersonalAssistantFolder.current());
    expect(current.structure.group.type).toBe('group');
    expect(await planLines(page)).toContain('Creates 6 workspaces, each linked to its own folder');
    expect(await workspaces(request)).toEqual(before.workspaces);
    expectNoExecution(seen);
    await card(page).getByRole('button', { name: 'Set up', exact: true }).click();
    await expect(card(page)).toContainText('Workspace · Downloads (group)', { timeout: 90_000 });
    const made = await groupOf(request, 'Downloads', 6);
    expectLinked(made.children, root);
    expect((await workspaces(request)).length).toBe(before.workspaces.length + 7);
    expect(hashFolder(root)).toBe(before.hash);
    expect(seen.asks).toEqual([]);
    expect(seen.errors).toEqual([]);
    await page.screenshot({ path: info.outputPath('exact-generic-completion.png') });
  });
});

test.describe('[collection:pending-return] exact Albums beside an Album-5 review', () => {
  test.skip(phase !== 'pending-return', 'runs in the pending-return phase');

  for (const saved of [false, true]) {
    test(`${saved ? 'saved' : 'unsaved'} origin preserves its draft, link, selection, and focus`, async ({
      page,
      request
    }, info) => {
      await hireAssistant(request);
      const makeReview = async (name: string) => {
        const draft_id = randomUUID();
        const selected = await ok(
          await request.post('/api/home-assistant/folder-context/select', {
            data: { draft_id, revision: '', mode: 'chip', chip: 'downloads' }
          })
        );
        const candidate = selected.observation.projects.find((row: any) => row.name === name);
        expect(candidate).toBeTruthy();
        return ok(
          await request.post('/api/home-assistant/folder-context/review', {
            data: {
              draft_id,
              revision: '',
              selection_id: selected.observation.id,
              candidate_id: candidate.id
            }
          })
        );
      };
      let originID = '';
      if (saved) {
        const origin = await makeReview('Album-1');
        originID = origin.conversation.id;
        await ok(
          await request.post('/api/home-assistant/folder-context/review/close', {
            data: {
              conversation_id: originID,
              revision: origin.folder_context.revision,
              offer_id: origin.folder_context.offer_id
            }
          })
        );
      }
      const blocking = await makeReview('Album-5');
      const root = join(sandbox, 'Downloads');
      const before = {
        hash: hashFolder(root),
        workspaces: await workspaces(request),
        conversations: (await ok(await request.get('/api/home-assistant/conversations')))
          .conversations.length
      };
      const seen = watch(page);
      await page.goto('/');
      await page.locator('#personalAssistantLauncher').click();
      if (saved) {
        await chooseConversation(page, '#personalAssistantConversationContinue');
        await page.locator(`[data-conversation-id="${originID}"]`).click();
        await expect(page.locator('#personalAssistantActiveFolder')).toBeVisible();
        await page.locator('#personalAssistantRemoveFolder').click();
        await expect(page.locator('#personalAssistantActiveFolder')).toBeHidden();
      }
      await chooseAttach(page, '#personalAssistantLinkChip');
      await page.locator('#personalAssistantLinkInput').fill('https://example.com');
      await page.locator('#personalAssistantLinkAttach').click();
      await expect(page.locator('#personalAssistantActiveLink')).toBeVisible({ timeout: 45_000 });
      await chooseAttach(page, '#personalAssistantFolderChip');
      await page
        .locator('#personalAssistantFolderChoices')
        .getByRole('button', { name: 'Downloads', exact: true })
        .click();
      const proposal = page.locator('#personalAssistantFolderStructure');
      await expect(proposal).toContainText('5 REAPER session folders, 1 Logic Pro project folder');
      await expect(proposal.getByRole('button', { name: 'Review library setup' })).toBeDisabled();
      await page.locator('#personalAssistantInput').fill('Keep this exact Albums draft unsent 🎼');
      const snapshot = () =>
        page.evaluate(() => ({
          id: (window as any).PersonalAssistantConversation.currentId(),
          folder: (window as any).PersonalAssistantFolderContext.snapshot(),
          link: (window as any).PersonalAssistantLink.snapshot(),
          text: (document.getElementById('personalAssistantInput') as HTMLTextAreaElement).value,
          focus: document.activeElement?.id
        }));
      const origin = await snapshot();
      await page.getByRole('button', { name: 'Open Album-5 review', exact: true }).click();
      await expect(page.locator('#personalAssistantFolderReturn')).toBeVisible();
      await expect(card(page)).toContainText('Album-5');
      expect((await snapshot()).id).toBe(blocking.conversation.id);
      await card(page).getByRole('button', { name: 'Keep chatting', exact: true }).click();
      await expect(card(page)).toBeHidden();
      await page.locator('#personalAssistantFolderReturn').click();
      await expect(page.locator('#personalAssistantFolderReturn')).toBeHidden();
      await expect(page.locator('#personalAssistantInput')).toHaveValue(origin.text);
      await expect(page.locator('#personalAssistantInput')).toBeFocused();
      await expect(proposal.getByRole('button', { name: 'Review library setup' })).toBeEnabled();
      const after = await snapshot();
      expect(after.id).toBe(originID);
      expect(after.folder.observation.id).toBe(origin.folder.observation.id);
      expect(after.folder.blocker).toBeNull();
      expect(after.link.link.id).toBe(origin.link.link.id);
      expect(after.link.active).toBe(true);
      expect(seen.posts.filter(item => item.path.endsWith('/folder-context/select'))).toHaveLength(
        1
      );
      expect(
        seen.posts.filter(item => item.path.endsWith('/folder-context/recommendation'))
      ).toHaveLength(2);
      expect(
        (await ok(await request.get('/api/home-assistant/conversations'))).conversations.length
      ).toBe(before.conversations);
      expect(await workspaces(request)).toEqual(before.workspaces);
      expect(hashFolder(root)).toBe(before.hash);
      expectNoExecution(seen);
      await page.screenshot({
        path: info.outputPath(`exact-${saved ? 'saved' : 'unsaved'}-return.png`)
      });
    });
  }
});
