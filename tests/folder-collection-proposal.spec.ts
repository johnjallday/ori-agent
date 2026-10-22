import { expect, test, type APIRequestContext, type Page } from '@playwright/test';
import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { lstatSync, readFileSync, readdirSync, realpathSync } from 'node:fs';
import { join, relative } from 'node:path';

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
// Nothing here stands in for the product, and no model is configured: the
// proposal is the server's own reading of directory entries, and the setup is
// the server's own run. A request to the assistant's model would be a failure.
// No real folder is touched: the folders are in the server's HOME, which is the
// sandbox.
const sandbox = process.env.ORI_COLLECTION_SANDBOX || '';
const phase = process.env.ORI_COLLECTION_PHASE || '';
test.skip(
  !sandbox || !['composer', 'home', 'declined', 'model-down'].includes(phase),
  'requires the sandbox and phase that scripts/e2e-folder-collection.sh provides'
);
test.describe.configure({ mode: 'serial' });
test.setTimeout(180_000);

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

// watch records what would make a run fail its promises: a page error, or any
// request to the assistant's model.
function watch(page: Page) {
  const seen = { errors: [] as string[], asks: [] as string[] };
  page.on('pageerror', error => seen.errors.push(error.message));
  page.on('request', request => {
    if (/\/api\/home-assistant\/ask$/.test(new URL(request.url()).pathname))
      seen.asks.push(request.url());
  });
  return seen;
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
  await expect(page.locator('#personalAssistantLauncher')).toBeVisible({ timeout: 30_000 });
  if (!(await page.locator('#personalAssistantPanel').isVisible()))
    await page.locator('#personalAssistantLauncher').click();
  await page.locator('#personalAssistantFolderChip').click();
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
const planLines = async (page: Page) =>
  (await card(page).innerText()).split('\n').map(line => line.trim());

test.describe('a folder of projects attached in the assistant', () => {
  test.skip(phase !== 'composer', 'runs in the composer phase');
  const root = join(sandbox, 'Downloads');

  const attach = (page: Page) => attachFolder(page, 'Downloads');

  test('is proposed as a group at once, and one confirmation creates it', async ({
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
    // One question and one line; three members shown, the rest behind Show all.
    await expect(proposal).toContainText('Set up Downloads as a group?');
    await expect(proposal).toContainText(
      '6 workspaces, one per folder · linked to their folders · nothing is moved'
    );
    await expect(proposal.getByRole('button', { name: 'Show all 6' })).toBeVisible();
    for (const name of members) await expect(proposal).toContainText(name);
    // REAPER is not installed: a short note per member, never a sentence.
    await expect(proposal).toContainText('plain for now');
    await expect(proposal).not.toContainText('is not installed');
    await expect(page.locator('#personalAssistantInput')).toHaveAttribute(
      'placeholder',
      'Propose workspaces for Downloads'
    );

    await proposal.getByRole('button', { name: 'Set up', exact: true }).click();
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
    const done = page.locator('.modal.show').first();
    await expect(done).toContainText('Workspace · Downloads (group)', { timeout: 90_000 });
    await expect(done).toContainText('no task was created');
    await expect(done.getByRole('link', { name: 'Open Downloads' })).toBeVisible();
    const made = await groupOf(request, 'Downloads', 6);
    expectLinked(made.children, root);
    expect((await workspaces(request)).length).toBe(before.workspaces + 7);

    expect(hashFolder(root), 'the attached folder changed').toBe(before.hash);
    expect(seen.asks, 'a model was asked').toEqual([]);
    expect(seen.errors).toEqual([]);
  });

  test('is not proposed a second time once its folders are workspaces', async ({
    page,
    request
  }) => {
    const before = (await workspaces(request)).length;
    const seen = watch(page);
    const selected = await attach(page);
    expect(selected.structure, 'a second group was proposed').toBeUndefined();
    await expect(page.locator('#personalAssistantFolderStructure')).toBeHidden();
    expect((await workspaces(request)).length).toBe(before);
    expect(seen.errors).toEqual([]);
  });
});

test.describe("a folder of projects explored from Home's folder link", () => {
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
      'Set up Music as a group?'
    );
    await page.getByRole('button', { name: 'Suggest arrangement', exact: true }).click();
    const planning = page.locator('#personalAssistantArrangement');
    await expect(
      planning.getByText('Configure a tool-capable API model', { exact: false })
    ).toBeVisible();
    await expect(planning.getByRole('button', { name: 'Send organizing goal' })).toBeDisabled();
    await page.screenshot({ path: info.outputPath('explore-no-model-manual.png'), fullPage: true });
    await page.getByRole('button', { name: 'Suggest arrangement', exact: true }).click();
    await page
      .locator('#personalAssistantFolderStructure')
      .getByRole('button', { name: 'Set up', exact: true })
      .click();

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
    const done = page.locator('.modal.show').first();
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

test.describe('a folder of projects the user once said No thanks to', () => {
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
    await expect(proposal).toContainText('Set up Downloads as a group?');
    // The No thanks stands where Set up would, with when.
    const askAgain = proposal.getByRole('button', {
      name: 'You said no to Downloads on Oct 6 · Ask again',
      exact: true
    });
    await expect(askAgain).toBeVisible();
    await expect(proposal.getByRole('button', { name: 'Set up', exact: true })).toHaveCount(0);

    const forgot = page.waitForResponse(
      response =>
        response.request().method() === 'POST' &&
        /\/api\/personal-assistant\/folder-digest\/forget$/.test(response.url())
    );
    await askAgain.click();
    const forget = await forgot;
    expect(forget.ok()).toBeTruthy();
    expect(Object.keys(forget.request().postDataJSON())).toEqual(['folder_key']);
    const setUp = proposal.getByRole('button', { name: 'Set up', exact: true });
    await expect(setUp).toBeEnabled();

    await setUp.click();
    await expect(card(page)).toContainText('Set up Downloads as a group?', { timeout: 30_000 });
    await card(page).getByRole('button', { name: 'Set up', exact: true }).click();
    const done = page.locator('.modal.show').first();
    await expect(done).toContainText('Workspace · Downloads (group)', { timeout: 90_000 });
    const made = await groupOf(request, 'Downloads', 6);
    expectLinked(made.children, root);
    expect((await workspaces(request)).length).toBe(before.workspaces + 7);

    expect(hashFolder(root), 'the attached folder changed').toBe(before.hash);
    expect(seen.asks, 'a model was asked').toEqual([]);
    expect(seen.errors).toEqual([]);
  });
});

test.describe('a folder of projects attached while the model cannot answer', () => {
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
    await expect(proposal).toContainText('Set up Downloads as a group?');
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
    await expect(proposal).toContainText('Set up Downloads as a group?');
    await expect(proposal.getByRole('button', { name: 'Set up', exact: true })).toBeVisible();

    await panel.getByRole('button', { name: 'Retry', exact: true }).click();
    await expect.poll(() => asked.length, { timeout: 60_000 }).toBe(2);
    await settled();
    // The retried turn replaces the failed line; it is never stacked.
    await expect(failure).toHaveCount(1);
    expect(asked[0].prompt).toBe('Propose workspaces for this folder');
    expect(asked[1].prompt).toBe(asked[0].prompt);
    expect(asked[1].folder_context).toEqual(asked[0].folder_context);
    expect(seen.asks).toHaveLength(2);
    expect(seen.errors).toEqual([]);
  });
});
