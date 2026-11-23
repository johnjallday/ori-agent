import { test, expect } from '@playwright/test';
import { readFileSync, mkdirSync, writeFileSync, utimesSync, existsSync } from 'node:fs';
import { join } from 'node:path';
import { seedQuestionFolder } from './helpers/commander-question-fixture';
import { installLocalCdn } from './helpers/offline-cdn';

const fixture = process.env.ORI_SEARCH_FIRST_FIXTURE;
const evidence = 'tasks/evidence-files-map-navigation';
test.skip(
  !fixture || process.env.ORI_SEARCH_FIRST_SCENARIO !== 'navigation',
  'requires a marked disposable profile'
);
test('distinguish duplicate copies by location and date before acting', async ({
  page,
  request,
  baseURL
}) => {
  expect(new URL(baseURL!).hostname).toBe('localhost');
  expect(readFileSync(join(fixture!, '.search-first-fixture'), 'utf8')).toBe('synthetic-v1\n');
  expect(readFileSync(join(fixture!, '.search-first-test-url'), 'utf8').trim()).toBe(baseURL);
  const original = join(fixture!, 'home/Downloads/Saved/proposal.pdf');
  for (const [dir, stamp] of [
    ['Documents/Final', 1704067200],
    ['Downloads/Saved', 1735689600]
  ] as const) {
    const root = join(fixture!, 'home', dir);
    mkdirSync(root, { recursive: true });
    const copy = join(root, 'proposal.pdf');
    writeFileSync(copy, 'Synthetic', { flag: 'wx' });
    utimesSync(copy, stamp, stamp);
  }
  for (let n = 0; n < 27; n++)
    writeFileSync(join(fixture!, `home/Downloads/sample-${n}.unknown`), '', { flag: 'wx' });
  await page.goto('/search');
  for (const kind of ['Documents', 'Downloads']) {
    await page.getByRole('button', { name: kind, exact: true }).click();
    await page.getByRole('dialog').getByRole('button', { name: 'Allow search' }).click();
    await expect(page.locator('#sources li').filter({ hasText: kind })).toContainText('ready');
  }
  const term = page.getByRole('searchbox');
  await term.fill('proposal');
  const rows = page.locator('#hits li');
  await expect(rows).toHaveCount(2);
  await expect(rows.first()).toContainText('Downloads / Saved');
  await expect(rows.last()).toContainText('Documents / Final');
  await rows.first().getByRole('button').click();
  await expect(page.locator('#selectedFile')).toContainText('Downloads / Saved');
  await page.screenshot({ path: join(evidence, 'copies.png') });
  await page.locator('#scope').selectOption({ label: 'Downloads' });
  await expect(rows).toHaveCount(1);
  await expect(page.locator('#selectedFile')).toBeHidden();
  await term.fill('sample-');
  await expect(rows).toHaveCount(25);
  await page.getByRole('button', { name: 'More results' }).click();
  await expect(rows).toHaveCount(27);
  await rows.last().getByRole('button').click();
  await expect(page.locator('#selectedFile button')).toHaveCount(1);
  await expect(page.locator('#selectedFile button')).toHaveText('Reveal in Finder');
  await page.locator('#selectedFile').scrollIntoViewIfNeeded();
  await page.screenshot({ path: join(evidence, 'scoped-last-page.png') });
  await page
    .locator('#sources li')
    .filter({ hasText: 'Downloads' })
    .getByRole('button', { name: 'Remove from search' })
    .click();
  await expect(rows).toHaveCount(0);
  await expect(page.locator('#selectedFile')).toBeHidden();
  expect(existsSync(original)).toBeTruthy();
  await expect(page.getByRole('link', { name: 'Map', exact: true })).toHaveCount(0);
  await expect(page.getByRole('link', { name: 'Start or resume workspace setup' })).toBeVisible();

  await installLocalCdn(page);
  const music = await seedQuestionFolder(request, join(fixture!, 'home'), 'Map Albums', true);
  const other = await seedQuestionFolder(request, join(fixture!, 'home'), 'Other Albums', true);
  const read = async (id: string) =>
    (await (await request.get(`/api/personal-assistant/folder-agent/${id}`)).json()).folder_agent;
  // Seed a pending-question record, not a simulated answer or a live model turn.
  // Only this marked, newly minted split profile is writable here. With a
  // degraded turn the real launcher correctly leads with the failure instead.
  for (const slug of ['map-albums', 'other-albums']) {
    const path = join(fixture!, 'data/workspace-staging', slug, '.ori/folder-agent-v1.json');
    const record = JSON.parse(readFileSync(path, 'utf8'));
    delete record.turn;
    writeFileSync(path, JSON.stringify(record));
  }
  await page.goto('/search');
  const mutations: string[] = [];
  page.on('request', r => {
    if (r.method() === 'POST') mutations.push(new URL(r.url()).pathname);
  });
  await page.getByRole('link', { name: 'Map', exact: true }).click();
  await expect(page.locator('[data-map-destination]')).toBeVisible();
  const tile = page
    .locator(
      `.ws-map-tile[data-ws-id="${music.workspace_id}"], .ws-map-district-tag[data-ws-id="${music.workspace_id}"]`
    )
    .first();
  await tile.click();
  const open = page.locator(`[data-cockpit-rail-open][data-workspace-id="${music.workspace_id}"]`);
  await expect(open).toBeVisible();
  await page.screenshot({ path: join(evidence, 'workspace-selected.png') });
  await open.click();
  const launcher = page.locator('#personalAssistantLauncher');
  await expect(launcher.locator('[data-question]')).toBeVisible();
  expect(
    mutations.filter(p =>
      /\/file-search\/(begin|select|open|reveal|refresh|remove|preference)$|\/premise$/.test(p)
    )
  ).toEqual([]);
  await launcher.press('Enter');
  const answer = page.locator(
    `[data-folder-agent-chat-card][data-message-id="${music.chat.message_id}"] [data-folder-agent-action="premise_confirm"]`
  );
  await expect(answer).toBeVisible();
  await page.screenshot({ path: join(evidence, 'commander-question.png') });
  await answer.click();
  await expect(answer).toHaveCount(0);
  await expect(launcher.locator('[data-question]')).toHaveCount(0);
  expect((await read(other.workspace_id)).premise.state).toBe(other.premise.state);
  await page.goBack();
  await page.getByRole('link', { name: 'Files', exact: true }).click();
  await expect(term).toBeVisible();
});
