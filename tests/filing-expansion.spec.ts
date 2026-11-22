import { expect, test, type APIRequestContext, type Page } from '@playwright/test';
import { createRequire } from 'node:module';

const require = createRequire(import.meta.url);
import {
  existsSync,
  mkdirSync,
  readFileSync,
  realpathSync,
  readdirSync,
  utimesSync,
  writeFileSync
} from 'node:fs';
import { join, resolve } from 'node:path';
import { tmpdir } from 'node:os';

const configSandbox = process.env.ORI_FILING_EXPANSION_SANDBOX || '';
const theme = process.env.ORI_FILING_EXPANSION_THEME === 'dark' ? 'dark' : 'light';
const small = process.env.ORI_FILING_EXPANSION_NARROW === '1';

test('earn Craft, build HQ, buy Filing and separately approve its first review', async ({
  page,
  request
}, info) => {
  test.skip(
    !configSandbox,
    'Use scripts/e2e-fresh.sh --sandbox-env ORI_FILING_EXPANSION_SANDBOX or an explicitly isolated wt demo.'
  );
  test.setTimeout(75_000);
  const root = realpathSync(configSandbox);
  const sandboxPrefix = resolve(realpathSync(tmpdir()), 'ori-');
  expect(root.startsWith(sandboxPrefix)).toBeTruthy();
  expect(readFileSync(join(root, '.ori-demo-owner'), 'utf8')).toContain(process.cwd());
  const downloads = join(root, 'Downloads');
  const inbox = join(root, 'Sandbox Inbox');
  mkdirSync(downloads);
  mkdirSync(inbox);
  writeFileSync(join(downloads, 'leave-me.pdf'), 'untouched', { mode: 0o600 });
  const file = join(inbox, 'invoice.pdf');
  writeFileSync(file, 'synthetic source bytes', { mode: 0o600 });
  const old = new Date(Date.now() - 3600000);
  utimesSync(file, old, old);

  await page.setViewportSize(small ? { width: 390, height: 844 } : { width: 1440, height: 980 });
  await page.emulateMedia({ colorScheme: theme, reducedMotion: 'reduce' });
  await page.addInitScript(theme => localStorage.setItem('ori-theme', theme), theme);
  async function get(path: string) {
    const response = await request.get(path);
    expect(response.ok(), await response.text()).toBeTruthy();
    return response.json();
  }
  const progression = () => get('/api/progression');
  const evidenceDir = process.env.ORI_FILING_EXPANSION_EVIDENCE;
  async function capture(selector: string, name: string) {
    if (evidenceDir) mkdirSync(evidenceDir, { recursive: true });
    await page.locator(selector).screenshot({
      path: evidenceDir ? join(evidenceDir, `${name}.png`) : info.outputPath(`${name}.png`)
    });
  }

  await post(request, '/api/onboarding/skip', {});
  await post(request, '/api/settings/workspace-root', { workspace_root: '' });
  const initial = (await get('/api/personal-assistant')).personal_assistant;
  expect(initial.state).toBe('needs_hire');
  await post(request, '/api/personal-assistant/hire', {
    request_id: 'expansion-golden-hire',
    if_version: initial.state_version ?? 0,
    display_name: 'Atlas',
    mandate: 'Keep my week moving.',
    focus_areas: ['plan_my_day']
  });
  expect((await get('/api/economy')).craft).toBe(7);
  expect((await progression()).filing_expansion.state).toBe('needs_hq');
  await page.goto('/');
  await page.locator('#cockpitWelcome [data-home-welcome-action="primary"]').click();
  await expect(page.locator('#personalAssistantHQBuild')).toBeEnabled();
  await page.locator('#personalAssistantHQNotesOptions summary').click();
  await page.locator('#personalAssistantHQNotesInclude').uncheck();
  await page.locator('#personalAssistantHQBuild').click();
  await expect
    .poll(async () => (await get('/api/personal-assistant')).personal_assistant.state)
    .toBe('active');
  const welcome = page.locator('#personalHQResultHost');
  await expect(welcome).toContainText('Your HQ is ready');
  await welcome.getByRole('button', { name: 'Dismiss HQ welcome' }).click();
  await expect(welcome).toBeHidden();
  await page.locator('#cockpitQuestsToggle').click();
  const offer = page.locator('[data-role="filing-expansion"]');
  await expect(offer).toContainText('7 Craft to build. You have 7 Craft.');
  await expect(offer.getByRole('link', { name: 'Show a folder for free' })).toHaveAttribute(
    'href',
    '/?quest=show-folder'
  );
  await checkAccessibility(page, '[data-role="filing-expansion"]');
  await offer.scrollIntoViewIfNeeded();
  await capture('#cockpitQuestsFlyout', 'hq-filing-offer');
  // Losing the live projection must disable a previously quoted build. Only
  // the test simulates timing out this read; every quote and write below is real.
  const projectionRoute = '**/api/progression';
  await page.route(projectionRoute, route => route.abort('failed'));
  await page.evaluate(() => window.dispatchEvent(new Event('ori:economy-changed')));
  await expect(offer.getByRole('button', { name: 'Build for 7 Craft' })).toBeHidden();
  await expect(offer).toContainText('A build price is unavailable');
  await page.unroute(projectionRoute);
  await page.evaluate(() => window.dispatchEvent(new Event('ori:economy-changed')));
  await expect(offer.getByRole('button', { name: 'Build for 7 Craft' })).toBeEnabled();
  // Prove the free route is usable, not merely present. It opens the
  // assistant chooser without authorizing a folder or making a purchase.
  await offer.getByRole('link', { name: 'Show a folder for free' }).click();
  await expect(page.locator('#personalAssistantPanel')).toBeVisible();
  await expect(page.locator('#personalAssistantFolderChooser')).toBeVisible();
  expect((await get('/api/economy')).craft).toBe(7);
  await page.locator('#personalAssistantClose').click();
  await page.locator('#cockpitQuestsToggle').click();
  await expect(offer.getByRole('button', { name: 'Build for 7 Craft' })).toBeEnabled();
  page.once('dialog', async dialog => {
    expect(dialog.message()).toContain('does not grant access');
    await dialog.accept();
  });
  await offer.getByRole('button', { name: 'Build for 7 Craft' }).click();
  await expect(offer).toContainText('7 Craft spent. You have 0 Craft.');
  const open = offer.getByRole('link', { name: 'Open Filing' });
  await expect(open).toBeFocused();
  const route = await open.getAttribute('href');
  expect(route).toMatch(/^\/workspaces\/filing-.+\?panel=file-janitor$/);
  const folders = (await get('/api/workspaces')).folders;
  const filing = folders.filter((workspace: { name: string }) => workspace.name === 'Filing');
  expect(filing).toHaveLength(1);
  const id = filing[0].id;
  await page.getByRole('button', { name: 'Close Quests' }).click();
  await expect(page.locator(`#cockpitMap .ws-map-tile[data-ws-id="${id}"]`)).toBeVisible();
  await capture('#cockpitMap', 'filing-on-map');
  await page.locator('#cockpitQuestsToggle').click();
  await open.click();

  const wizard = page.locator('#setupWizardDialog');
  await expect(wizard).toBeVisible();
  await expect(wizard.getByText(/Craft built this workspace/)).toBeVisible();
  await wizard.getByRole('button', { name: 'Review Downloads' }).click();
  await expect(wizard.locator('.fj-setup-path')).toHaveText(downloads);
  expect(readdirSync(downloads)).toEqual(['leave-me.pdf']);
  await wizard.getByRole('button', { name: 'Not now' }).click();
  await expect(wizard).not.toBeVisible();
  expect((await get('/api/economy')).craft).toBe(0);
  expect((await get(`/api/workspaces/${id}/file-janitor`)).status.settings.root_path || '').toBe(
    ''
  );
  expect((await get(`/api/workspaces/${id}/file-janitor/batches`)).batches).toHaveLength(0);
  await capture('body', 'deferred');
  await page.goto('/');
  await page.locator('#cockpitQuestsToggle').click();
  await expect(offer).toContainText('You have 0 Craft.');
  await offer.getByRole('link', { name: 'Open Filing' }).click();
  const opened = page.waitForResponse(response => response.url().endsWith('/setup-wizard/open'));
  await page.locator('#fileJanitorOpenSetup').click();
  await opened;
  await expect(wizard).toBeVisible();
  const typed = wizard.getByRole('textbox', { name: 'Or paste a folder path' });
  await typed.fill(inbox);
  await typed.press('Enter');
  await expect(wizard.locator('.fj-setup-path')).toHaveText(inbox);
  await expect(wizard.getByRole('checkbox', { name: /watch new arrivals/ })).not.toBeChecked();
  await expect(wizard).toContainText('Create or reuse Filed/');
  await expect(wizard).toContainText('No file contents are opened');
  expect(existsSync(join(inbox, 'Filed'))).toBeFalsy();
  const before = (await progression()).missions;
  expect(before.find((q: { id: string }) => q.id === 'pa-show-folder').status).not.toBe(
    'completed'
  );
  expect(before.find((q: { id: string }) => q.id === 'pa-folder-first-look').status).not.toBe(
    'completed'
  );
  const allow = wizard.getByRole('button', { name: 'Allow filing in Sandbox Inbox' });
  await expect(allow).toBeFocused();
  await checkAccessibility(page, '#setupWizardDialog .fj-setup-review');
  await wizard.locator('.fj-setup-path').scrollIntoViewIfNeeded();
  await capture('#setupWizardDialog', 'folder-permission');
  await allow.press('Enter');
  await expect(wizard).not.toBeVisible();
  await expect(page.locator('#fileJanitorConsoleBody')).toContainText('invoice.pdf');
  await capture('#fileJanitorConsole', 'first-review');
  expect(readFileSync(file, 'utf8')).toBe('synthetic source bytes');
  expect(readdirSync(downloads)).toEqual(['leave-me.pdf']);
  expect(existsSync(join(inbox, 'Filed'))).toBeTruthy();
  const status = (await get(`/api/workspaces/${id}/file-janitor`)).status;
  expect(status.settings.root_path).toBe(inbox);
  expect(status.settings.setup_decision.mode).toBe('manual');
  expect(status.settings.automation_approved_at || '').toMatch(/^$|^0001-/);
  const batches = (await get(`/api/workspaces/${id}/file-janitor/batches`)).batches;
  expect(batches).toHaveLength(1);
  await page.reload();
  await expect(page.locator('#fileJanitorConsoleBody')).toContainText('invoice.pdf');
  expect(
    (await get(`/api/workspaces/${id}/file-janitor/batches`)).batches.map(
      (b: { id: string }) => b.id
    )
  ).toEqual(batches.map((b: { id: string }) => b.id));
});

async function checkAccessibility(page: Page, selector: string) {
  if (!(await page.evaluate(() => Boolean((window as any).axe)))) {
    await page.addScriptTag({ path: require.resolve('axe-core/axe.min.js') });
  }
  const violations = await page.evaluate(async target => {
    const result = await (window as any).axe.run(target, {
      runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa'] }
    });
    return result.violations.map((v: { id: string }) => v.id);
  }, selector);
  expect(violations).toEqual([]);
}

async function post(request: APIRequestContext, path: string, data: unknown) {
  const response = await request.post(path, { data });
  expect(response.ok(), await response.text()).toBeTruthy();
}
