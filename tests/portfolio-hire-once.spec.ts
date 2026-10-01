import { expect, test, type APIRequestContext, type Page } from '@playwright/test';
import { existsSync, mkdirSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';

// A whole music portfolio from one card: 200 song folders show one plan, Set up
// runs it on the server (provider, integration, Home and its agents, the
// library, the standing consent), and the songs are only listed. Opening a song
// from the library makes its workspace and adds the one shared assistant, which
// the roster then shows once, on every song it works on.
//
// Run it against a fresh isolated sandbox (the folders are written into the
// server's HOME, which is the sandbox):
//
//   ./scripts/demo-server.sh 8932            # prints SANDBOX=…
//   ./scripts/e2e.sh --port 8932 --env ORI_PORTFOLIO_SANDBOX=<sandbox dir> tests/portfolio-hire-once.spec.ts
//
// It needs the network the first time (the reviewed REAPER integration and the
// Home provider are downloaded and installed by the run itself). The steps
// after the needs-a-model stop also need a model provider (Codex); without one
// they are skipped. No REAPER app is launched and no live control is granted.
const sandbox = process.env.ORI_PORTFOLIO_SANDBOX || '';
test.skip(!sandbox, 'requires ORI_PORTFOLIO_SANDBOX (the demo sandbox directory)');
test.describe.configure({ mode: 'serial' });
test.setTimeout(300_000);

const folderApi = '/api/personal-assistant/folder-digest';
const SONGS = 200;
const ASSISTANT = 'REAPER Assistant';

async function ok(response: Awaited<ReturnType<APIRequestContext['get']>>) {
  const text = await response.text();
  expect(response.ok(), text).toBeTruthy();
  return text ? JSON.parse(text) : {};
}

const songName = (i: number) => `Song ${String(i).padStart(3, '0')}`;

// 200 song folders directly in Documents, each with its project file, a backup,
// a bounce and a Media/ folder of takes (the smoke script's seed-portfolio).
function seedPortfolio() {
  for (let i = 1; i <= SONGS; i++) {
    const name = songName(i);
    const folder = join(sandbox, 'Documents', name);
    if (existsSync(join(folder, `${name}.rpp`))) continue;
    mkdirSync(join(folder, 'Media'), { recursive: true, mode: 0o750 });
    writeFileSync(join(folder, `${name}.rpp`), '<REAPER_PROJECT 0.1 "7.0"\n>\n', { mode: 0o600 });
    writeFileSync(join(folder, `${name}.rpp-bak`), '<REAPER_PROJECT>\n', { mode: 0o600 });
    writeFileSync(join(folder, `${name} bounce.wav`), 'RIFF', { mode: 0o600 });
    for (let take = 0; take < 3; take++) {
      writeFileSync(join(folder, 'Media', `take-${take}.wav`), 'RIFF', { mode: 0o600 });
    }
  }
}

async function scan(request: APIRequestContext) {
  return (await ok(await request.post(`${folderApi}/scan`, { data: { chip: 'documents' } }))).offer;
}

async function openCard(page: Page) {
  await page.goto('/');
  // The app polls, so the network never goes idle; wait for the panel instead.
  await page.waitForFunction(() => Boolean((window as any).PersonalAssistantPanel?.open));
  await page.evaluate(() =>
    (window as any).PersonalAssistantPanel?.open?.(
      document.getElementById('personalAssistantLauncher'),
      { view: 'today' }
    )
  );
  const card = page.locator('#personalAssistantFolderOffer');
  await expect(card).toBeVisible();
  await card.scrollIntoViewIfNeeded();
  return card;
}

async function agentNames(request: APIRequestContext): Promise<string[]> {
  const payload = await ok(await request.get('/api/agents/dashboard/list'));
  return (Array.isArray(payload) ? payload : payload.agents || []).map(
    (agent: { name: string }) => agent.name
  );
}

async function songWorkspaces(request: APIRequestContext): Promise<string[]> {
  const payload = await ok(await request.get('/api/workspaces'));
  const names: string[] = [];
  const walk = (items: any[]) =>
    (items || []).forEach(item => {
      names.push(String(item?.name || ''));
      walk(item?.children || item?.sub_workspaces || []);
    });
  walk(payload.workspaces || payload.folders || payload || []);
  return names.filter(name => /^Song \d{3}$/.test(name));
}

let modelReady = false;
let libraryURL = '';

test('a folder of 200 songs shows one plan for the whole collection', async ({ page, request }) => {
  await request.post('/api/onboarding/skip');
  const relationship = (await ok(await request.get('/api/personal-assistant'))).personal_assistant;
  if (relationship.state === 'needs_hire') {
    await ok(
      await request.post('/api/personal-assistant/hire', {
        data: {
          request_id: 'portfolio-hire',
          if_version: relationship.state_version ?? 0,
          display_name: 'Atlas',
          mandate: 'Keep my songs moving.',
          focus_areas: ['plan_my_day']
        }
      })
    );
    const hired = (await ok(await request.get('/api/personal-assistant'))).personal_assistant;
    await ok(
      await request.post('/api/personal-assistant/hq', {
        data: { request_id: 'portfolio-hq', if_version: hired.state_version, name: 'My HQ' }
      })
    );
  }
  seedPortfolio();

  const offer = await scan(request);
  expect(offer.portfolio).toBeTruthy();
  const kinds = (offer.plan?.lines || []).map((line: { kind: string }) => line.kind);
  for (const kind of [
    'provider',
    'integration',
    'home',
    'agents',
    'library',
    'songs',
    'assistant'
  ]) {
    expect(kinds).toContain(kind);
  }
  expect(offer.plan.digest).toMatch(/^[0-9a-f]{64}$/);

  const card = await openCard(page);
  const lines = card.locator('#personalAssistantFolderPlan li');
  await expect(lines.first()).toBeVisible();
  const text = (await lines.allInnerTexts()).join('\n');
  // One plan for the collection: listed, not opened; one assistant for every song.
  expect(text).toContain(`Lists the ${SONGS}`);
  expect(text).toMatch(/gets its workspace the first time you open it/);
  expect(text).toMatch(/joins each .* you open/);
  expect(text).toMatch(/File-only/);
  expect(text).not.toMatch(/\/Users\/|\/var\/|\/private\//);
  await expect(card.getByRole('button', { name: 'Set up', exact: true })).toBeVisible();
  await expect(card.getByRole('button', { name: 'Adjust…' })).toBeVisible();
});

test('Adjust… opens the step-by-step Home setup and runs nothing', async ({ page, request }) => {
  const pluginNames = async () =>
    ((await ok(await request.get('/api/plugins'))).plugins || [])
      .map((row: { name: string }) => row.name)
      .sort();
  const before = await pluginNames();
  const card = await openCard(page);
  // The old path asks before it installs the Home provider; saying no stops it.
  const asked = new Promise<string>(resolve =>
    page.once('dialog', async dialog => {
      resolve(dialog.message());
      await dialog.dismiss();
    })
  );
  await card.getByRole('button', { name: 'Adjust…' }).click();
  expect(await asked).toContain('Set up Music Production Home');
  expect(await pluginNames()).toEqual(before);
  expect(await songWorkspaces(request)).toEqual([]);

  // The one-click plan stays beside the step-by-step path.
  await page.reload();
  const resumed = await openCard(page);
  await expect(resumed.getByRole('button', { name: 'Set up', exact: true })).toBeVisible();
  await expect(resumed.getByRole('button', { name: 'Continue setup' })).toBeVisible();
});

test('Set up without a model stops before any agent and keeps what finished', async ({
  page,
  request
}) => {
  await request.post('/api/settings/system-model', { data: { provider: '', model: '' } });
  const card = await openCard(page);
  await card.getByRole('button', { name: 'Set up', exact: true }).click();
  const runModal = page.locator('#folderSetupRunModal');
  await expect(runModal).toBeVisible();
  await expect(runModal.locator('#folderSetupRunStatus')).toContainText('needs a model', {
    timeout: 240_000
  });
  await expect(runModal).not.toContainText(/needs_model|plan_changed|install_failed/);
  await expect(runModal.getByRole('button', { name: 'Try again' })).toBeVisible();
  await runModal.getByRole('button', { name: /^Close/ }).click();
  await expect(
    card.locator('#personalAssistantFolderPlan li[data-state="done"]').first()
  ).toBeVisible();
  // Nothing was hired and no song got a workspace.
  const names = await agentNames(request);
  expect(names.filter(name => /Portfolio Manager|REAPER Assistant/.test(name))).toEqual([]);
  expect(await songWorkspaces(request)).toEqual([]);

  modelReady = (
    await request.post('/api/settings/system-model', {
      data: { provider: 'codex', model: 'gpt-5.6-luna' }
    })
  ).ok();
});

test('keyboard only: Try again lists the 200 songs and opens on the library', async ({
  page,
  request
}) => {
  test.skip(!modelReady, 'no model provider is available on this machine');
  const card = await openCard(page);
  const retry = card.getByRole('button', { name: 'Try again' });
  await retry.focus();
  await expect(retry).toBeFocused();
  await page.keyboard.press('Enter');

  const runModal = page.locator('#folderSetupRunModal');
  await expect(runModal.locator('#folderSetupRunEyebrow')).toHaveText('All set', {
    timeout: 240_000
  });
  const receipt = (await card.locator('#personalAssistantFolderReceipt li').allInnerTexts()).join(
    '\n'
  );
  expect(receipt).toContain(`Listed ${SONGS}`);
  expect(receipt).toMatch(/Portfolio Manager/);
  // Listing made no song workspace and hired no song assistant yet.
  expect(await songWorkspaces(request)).toEqual([]);
  expect(await agentNames(request)).not.toContain(ASSISTANT);

  const open = runModal.getByRole('link', { name: /^Open / });
  await open.focus();
  await expect(open).toBeFocused();
  await page.keyboard.press('Enter');
  await page.waitForURL('**/assistant**');
  libraryURL = new URL(page.url()).pathname;
  await expect(page.locator('#projectLibraryRows tr').first()).toBeVisible({ timeout: 30_000 });
});

async function searchLibrary(page: Page, name: string) {
  await page.goto(libraryURL);
  await page.locator('#projectLibrarySearch').fill(name);
  await page.locator('#projectLibrarySearch').press('Enter');
  const open = page.getByRole('button', { name: new RegExp(`^Open ${name}`) });
  await expect(open).toBeVisible({ timeout: 30_000 });
  return open;
}

test('three songs open in one click each and share one assistant', async ({ page, request }) => {
  test.skip(!libraryURL, 'the run did not finish');
  // A click, then keyboard only.
  await (await searchLibrary(page, songName(2))).click();
  await page.waitForURL(/\/workspaces\/song-002$/, { timeout: 120_000 });
  const viaKeyboard = await searchLibrary(page, songName(3));
  await viaKeyboard.focus();
  await page.keyboard.press('Enter');
  await page.waitForURL(/\/workspaces\/song-003$/, { timeout: 120_000 });
  await (await searchLibrary(page, songName(4))).click();
  await page.waitForURL(/\/workspaces\/song-004$/, { timeout: 120_000 });

  expect((await songWorkspaces(request)).sort()).toEqual([songName(2), songName(3), songName(4)]);
  // One assistant, never one per song.
  const names = await agentNames(request);
  expect(names.filter(name => name.startsWith(ASSISTANT))).toEqual([ASSISTANT]);
  const detail = await ok(await request.get(`/api/agents/${encodeURIComponent(ASSISTANT)}/detail`));
  expect(detail.workspace_count).toBe(3);

  // The roster card names the songs, the overflow pill the ones it hides.
  await page.goto('/agents');
  const rosterCard = page.locator(`.roster-card[data-name="${ASSISTANT}"]`);
  await expect(rosterCard).toBeVisible({ timeout: 30_000 });
  await expect(rosterCard.locator('.agent-card__pill')).toHaveCount(3);
  await expect(rosterCard.locator('.agent-card__pill.is-more')).toHaveAttribute(
    'title',
    /^Also in Song 00\d$/
  );

  // A delete says how many songs it works in before anything is sent.
  await rosterCard.locator('.roster-card__open').click();
  const refused = new Promise<string>(resolve =>
    page.once('dialog', async dialog => {
      resolve(dialog.message());
      await dialog.accept();
    })
  );
  await page.locator('#stageDelete').click();
  expect(await refused).toContain('works in 3 workspaces');
  expect(await agentNames(request)).toContain(ASSISTANT);
});

test('an edit to the shared assistant reaches its songs, except one changed there', async ({
  request
}) => {
  test.skip(!libraryURL, 'the run did not finish');
  const workspaces = (
    await ok(await request.get(`/api/agents/${encodeURIComponent(ASSISTANT)}/detail`))
  ).workspaces as { id: string; name: string }[];
  const own = workspaces.find(ws => ws.name === songName(3))!;
  const promptURL = (id: string) =>
    `/api/workspaces/${id}/agents/${encodeURIComponent(ASSISTANT)}/system-prompt`;
  await ok(
    await request.patch(promptURL(own.id), { data: { system_prompt: 'Only for this song.' } })
  );

  const shared = await ok(await request.get(`/api/agents/${encodeURIComponent(ASSISTANT)}/detail`));
  const saved = await ok(
    await request.patch(`/api/agents/${encodeURIComponent(ASSISTANT)}`, {
      data: { system_prompt: `${shared.system_prompt}\nName the song.`, confirm_shared_edit: true }
    })
  );
  expect(saved.carried.updated.map((ws: { name: string }) => ws.name).sort()).toEqual([
    songName(2),
    songName(4)
  ]);
  expect(saved.carried.customised.map((ws: { name: string }) => ws.name)).toEqual([songName(3)]);
  for (const ws of workspaces) {
    const prompt = (await ok(await request.get(promptURL(ws.id)))).system_prompt as string;
    if (ws.id === own.id) expect(prompt).toBe('Only for this song.');
    else expect(prompt.endsWith('Name the song.')).toBe(true);
  }
});
