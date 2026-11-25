import { test, expect, type Locator } from '@playwright/test';
import { freshGuidedHQ } from './helpers/fresh-guided-hq';
import { mkdir } from 'node:fs/promises';
import { join } from 'node:path';
import { resumeLegacyFolderGuide } from './helpers/legacy-hq-folder';
import { installLocalCdn } from './helpers/offline-cdn';

// Live updates can replace the receipt between locating it and taking the
// screenshot. Resolve the locator again if that happens.
async function capture(control: Locator, path: string) {
  await expect(async () => {
    await control.screenshot({ path, animations: 'disabled', timeout: 1500 });
  }).toPass({ timeout: 6000 });
}

let base: string;
let other: string;
test.describe.configure({ mode: 'serial' });
test.beforeAll(async ({ request }) => {
  base = await freshGuidedHQ(request);
  const created = await request.post('/api/workspaces', { data: { name: 'Another workspace' } });
  expect(created.ok()).toBeTruthy();
  const body = await created.json();
  other = '/workspaces/' + encodeURIComponent((body.folder || body.workspace).folder_slug);
});

for (const mode of ['details', 'map'])
  test(`one invitation on ${mode}; voluntary reopen after leaving`, async ({ page }, info) => {
    await installLocalCdn(page);
    const reducedMotion = mode === 'map';
    await page.emulateMedia({ reducedMotion: reducedMotion ? 'reduce' : 'no-preference' });
    if (mode === 'map') await page.setViewportSize({ width: 390, height: 844 });
    const access: string[] = [];
    page.on('request', r => {
      if (
        r.method() === 'POST' &&
        /folder-agent\/(choose|start)|email-setup\/connect|onboarding\/(detect|personalize)/.test(
          r.url()
        )
      )
        access.push(r.url());
    });
    // HQ build can finish in the Home drawer. Its remembered open state must
    // not hide or steal focus from the first HQ invitation.
    await page.addInitScript(() => {
      if (!sessionStorage.getItem('seededDrawer')) {
        sessionStorage.setItem('ori.personalAssistant.panelOpen', '1');
        sessionStorage.setItem('seededDrawer', '1');
      }
    });
    await page.goto(`${base}?mode=${mode}`);
    const card = page.locator('[data-hq-firstvisit]');
    await expect(card).toHaveAccessibleName('What would you like to do next?');
    await expect(card).toBeFocused();
    await expect(page.locator('#personalAssistantPanel')).toBeHidden();
    const dismiss = card.getByRole('button', { name: 'Maybe later' });
    await expect(dismiss).toHaveAccessibleDescription(
      /open Things to try at the top of Personal HQ/
    );
    await expect(card.locator('#hq-suggestions-reopen')).toBeInViewport();
    await page.screenshot({ path: info.outputPath('offer.png'), animations: 'disabled' });
    await dismiss.click();
    const door = page.getByRole('button', { name: 'Things to try' });
    await expect(door).toBeFocused();
    await expect(door).toBeInViewport();
    await expect(door).toHaveAttribute('aria-expanded', 'false');
    await page.screenshot({ path: info.outputPath('just-dismissed.png'), animations: 'disabled' });
    await page.goto(other + '?mode=details');
    await expect(card).toHaveCount(0);
    await expect(door).toHaveCount(0);
    await page.goto(`${base}?mode=${mode}`);
    await expect(door).toBeVisible();
    await expect(card).toHaveCount(0);
    await page.screenshot({
      path: info.outputPath('voluntary-reopen.png'),
      animations: 'disabled'
    });
    await door.click();
    await card.getByRole('button', { name: /Create a workspace from Documents/ }).press('Enter');
    const home = card.getByRole('link', { name: 'Go to Home' });
    const url = await home.getAttribute('href');
    await home.press('Enter');
    const button = page.getByRole('button', { name: 'New Workspace', exact: true });
    await expect(button).toBeFocused();
    await expect(button).toHaveAccessibleDescription(/review your Documents/);
    await expect(button).toHaveCSS('animation-name', reducedMotion ? 'none' : 'hq-workspace-pulse');
    // An incidental click on the dashboard isn't a decision about the guide.
    await page.locator('#homeCockpit').dispatchEvent('click');
    await expect(page.locator('#hqWorkspaceHint')).toBeVisible();
    if (!reducedMotion) {
      await expect(button).toHaveCSS('animation-iteration-count', 'infinite');
      await expect
        .poll(() => button.evaluate(el => Number(getComputedStyle(el).scale)))
        .toBeGreaterThan(1.08);
    }
    await expect(page.locator('#addFolderModal')).toBeHidden();
    await expect(page.locator('#personalAssistantPanel')).toBeHidden();
    await page.screenshot({ path: info.outputPath('home-hint.png'), animations: 'disabled' });
    if (reducedMotion) await button.press('Escape');
    else await page.getByRole('button', { name: 'Dismiss hint' }).click();
    await expect(button).toHaveCSS('animation-name', 'none');
    await expect(page.locator('#hqWorkspaceHint')).toBeHidden();
    await page.goto(url!);
    await expect(page.locator('#hqWorkspaceHint')).toBeHidden();
    await page.goto('/');
    await expect(button).toHaveCSS('animation-name', 'none');
    await expect(button).not.toBeFocused();
    await page.goBack();
    await expect(button).not.toBeFocused();
    await expect(page.locator('#hqWorkspaceHint')).toBeHidden();
    expect(access).toEqual([]);
  });

test('Documents review can return without a hire, then open its actual reported workspace', async ({
  page,
  request
}, info) => {
  const sandbox = process.env.ORI_HQ_FIRST_VISIT_SANDBOX;
  test.skip(!sandbox, 'requires a disposable Documents folder');
  await mkdir(join(sandbox!, 'Documents'), { recursive: true, mode: 0o750 });
  await installLocalCdn(page);
  const starts: string[] = [];
  const choices: string[] = [];
  page.on('request', req => {
    if (req.url().endsWith('/folder-agent/choose')) choices.push(req.method());
    if (req.url().endsWith('/folder-agent/start')) starts.push(req.method());
  });
  await page.goto(base + '?mode=details');
  const hasArrow = (control: Locator) =>
    control.evaluate(el => getComputedStyle(el, '::before').clipPath !== 'none');
  const doc = async (hideTips = false) => {
    const before = choices.length;
    await resumeLegacyFolderGuide(page);
    const guide = page.getByRole('complementary', { name: 'Explore Documents guide' });
    // Home loads its cockpit and resolves the assistant relationship before
    // it can offer the real Attach control, especially on a cold navigation.
    await expect(guide).toContainText('Open + (Attach)', { timeout: 15000 });
    const attach = page.getByRole('button', { name: 'Attach', exact: true });
    await expect(attach).toHaveAttribute('aria-expanded', 'false');
    await expect(attach).toBeFocused();
    expect(await hasArrow(attach)).toBe(true);
    const motion = await attach.evaluate(el => {
      const arrow = getComputedStyle(el, '::before');
      return [arrow.animationName, arrow.animationIterationCount];
    });
    if (hideTips) expect(motion[0]).toBe('none');
    else expect(motion).toEqual(['pa-guide-point', '3']);
    if (!hideTips)
      await page.screenshot({
        path: info.outputPath('folder-guide-attach.png'),
        animations: 'disabled'
      });
    await expect(page.locator('[data-folder-agent-card="choose"]')).toHaveCount(0);
    if (hideTips) {
      await guide.getByRole('button', { name: 'Hide tips' }).click();
      await expect(guide).toBeHidden();
    }
    await attach.press('ArrowDown');
    const add = page.getByRole('menuitem', { name: 'Add folder', exact: true });
    await expect(add).toBeFocused();
    expect(await hasArrow(attach)).toBe(false);
    expect(await hasArrow(add)).toBe(!hideTips);
    if (!hideTips)
      await page.screenshot({
        path: info.outputPath('folder-guide-menu.png'),
        animations: 'disabled'
      });
    await add.press('Enter');
    const documents = page.locator('#personalAssistantFolderChips [data-chip="documents"]');
    await expect(documents).toBeVisible();
    expect(await hasArrow(documents)).toBe(!hideTips);
    if (hideTips) await expect(guide).toBeHidden();
    else {
      await expect(guide).toContainText('Select Documents');
      await page.screenshot({
        path: info.outputPath('folder-guide-documents.png'),
        animations: 'disabled'
      });
    }
    expect(choices).toHaveLength(before);
    if (hideTips) await documents.click();
    else {
      await expect(documents).toBeFocused();
      await documents.press('Enter');
    }
    await expect(guide).toBeHidden();
    await expect(page.locator('[data-folder-agent-card="choose"]')).toContainText(
      'Hire an agent for Documents',
      { timeout: 15000 }
    );
  };
  const back = page.locator('#personalAssistantPanel [data-guided-return]');
  const receipt = page.locator('[data-guided-receipt]');
  await page.emulateMedia({ reducedMotion: 'no-preference' });
  await doc();
  const cancelled = await back.getAttribute('href');
  await back.click();
  await expect(receipt).toContainText('No folder exploration was confirmed');
  await capture(receipt, info.outputPath('folder-cancelled.png'));
  expect(starts).toEqual([]);
  await page.setViewportSize({ width: 390, height: 844 });
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await page.getByRole('button', { name: 'Things to try' }).click();
  await doc(true);
  expect(choices).toEqual(['POST', 'POST']);
  expect(starts).toEqual([]);
  await page.locator('[data-folder-agent-action="start"]').click();
  await expect
    .poll(
      async () =>
        (await (await request.get('/api/personal-assistant/folder-agent')).json())
          .folder_agents?.[0]?.state,
      { timeout: 30000 }
    )
    .toBe('reported');
  await back.click();
  await expect(receipt).toContainText('first look at Documents is ready');
  await page.reload();
  await expect(receipt).toContainText('Documents');
  await capture(receipt, info.outputPath('folder-receipt.png'));
  await receipt.getByRole('link', { name: 'Open folder workspace' }).click();
  await expect(page.locator('body')).not.toHaveAttribute('data-hq-details', '1');
  await page.goto(base + '?mode=details');
  await expect(receipt).toContainText('first look at Documents is ready');
  await page.goto(cancelled!);
  await expect(receipt).toHaveCount(0);
  expect(starts).toEqual(['POST']);
});

test('mailbox cancel, explicit setup and Settings return recheck only the bound workspace', async ({
  page
}, info) => {
  await installLocalCdn(page);
  let state = 'new';
  let workspace = { id: '', route: '', name: 'Email Ops' };
  // Real workspace creation/discovery; only mailbox access and its binding are synthetic.
  await page.route('**/api/workspaces/*/email/status', r =>
    !workspace.id ||
    !new URL(r.request().url()).pathname.endsWith(`/workspaces/${workspace.id}/email/status`)
      ? r.fallback()
      : r.fulfill({
          json: {
            status: {
              account_id: 'fixture-account',
              connected: true,
              setup:
                state === 'repair'
                  ? { action: 'reconnect_gmail', message: 'Reconnect Google.' }
                  : { ready: true }
            }
          }
        })
  );
  await page.route('**/api/email-setup/detect?*', r =>
    r.fulfill({
      json: { profile: { address: 'example@example.test', label: 'Example', supported: true } }
    })
  );
  let writes = 0;
  await page.route('**/api/email-setup/connect', async r => {
    writes++;
    const created = await page.request.post('/api/workspaces', {
      data: { name: 'Guided Email Ops', template_id: 'email-ops', create_template_agents: true }
    });
    expect(created.ok()).toBeTruthy();
    const body = await created.json();
    const ws = body.folder || body.workspace;
    workspace = { id: ws.id, route: `/workspaces/${ws.folder_slug}`, name: ws.name };
    state = 'ready';
    return r.fulfill({ json: { result: { account_id: 'fixture-account', workspace } } });
  });
  await page.goto(base + '?mode=details');
  const card = page.locator('[data-hq-firstvisit]'),
    modal = page.locator('#emailSetupModal'),
    receipt = page.locator('[data-guided-receipt]');
  const enter = async () => {
    await card.getByRole('button', { name: /Connect email/ }).click();
  };
  const setup = async () => {
    await enter();
    await card.getByRole('button', { name: 'Review mailbox setup' }).click();
    await expect(modal).toBeVisible();
  };
  await setup();
  await modal.getByRole('button', { name: 'Close', exact: true }).click();
  await expect(receipt).toContainText('No mailbox is ready');
  await capture(receipt, info.outputPath('email-cancelled.png'));
  expect(writes).toBe(0);
  await page.getByRole('button', { name: 'Things to try' }).click();
  await setup();
  await modal.locator('[name=address]').fill('example@example.test');
  await modal.getByRole('button', { name: 'Continue' }).click();
  await modal.locator('[name=password]').fill('fixture-only');
  await modal.locator('[type=submit]').click();
  await modal.getByRole('button', { name: 'Back to Personal HQ', exact: true }).click();
  await expect(receipt.getByRole('link', { name: 'Review email in Email Ops' })).toHaveAttribute(
    'href',
    workspace.route
  );
  await page.reload();
  await expect(receipt).toContainText('ready mailbox');
  await capture(receipt, info.outputPath('email-receipt.png'));
  await page.getByRole('button', { name: 'Things to try' }).click();
  await enter();
  await card.getByRole('link', { name: 'Review email in Email Ops' }).click();
  await expect(page.locator('body')).toHaveAttribute('data-workspace-id', workspace.id);
  await page.goto(base + '?mode=details');
  state = 'repair';
  await page.getByRole('button', { name: 'Things to try' }).click();
  await enter();
  await card.getByRole('link', { name: 'Review Google Account' }).click();
  await page.locator('nav [data-guided-return]').click();
  await expect(receipt).toContainText('Reconnect Google');
  await capture(receipt, info.outputPath('email-repair.png'));
  expect(writes).toBe(1);
});

test('explicit app review saves only chosen tools and never scans on return', async ({
  page,
  request
}, info) => {
  await installLocalCdn(page);
  let scans = 0;
  await page.route('**/api/onboarding/detect**', r => {
    scans++;
    return r.fulfill({ json: { names: ['Fixture Editor', 'Fixture Mail', 'fixture editor'] } });
  });
  const read = async () =>
    (await (await request.get('/api/onboarding/user-profile')).json()).profile;
  await page.goto(base + '?mode=map');
  const card = page.locator('[data-hq-firstvisit]');
  const receipt = page.locator('[data-guided-receipt]');
  const scan = async () => {
    await card.getByRole('button', { name: /Scan recent apps/ }).click();
    await card.getByRole('button', { name: 'Start scan' }).click();
    await expect(card.getByRole('checkbox')).toHaveCount(2);
    await expect(card.getByRole('button', { name: 'Save selected tools' })).toBeDisabled();
  };
  await scan();
  await card.getByRole('checkbox', { name: 'Fixture Editor' }).check();
  await card.screenshot({ path: info.outputPath('app-review.png') });
  await card.getByRole('button', { name: 'Save selected tools' }).click();
  await expect(receipt).toContainText('Selected tools now in Profile: Fixture Editor');
  await page.reload();
  await expect(receipt).toContainText('Fixture Editor');
  const saved = await read();
  expect(saved.preferred_tools).toEqual(['Fixture Editor']);
  expect(scans).toBe(1);
  await receipt.getByRole('link', { name: /Profile/ }).click();
  await expect(page.locator('[data-tool="Fixture Editor"]')).toHaveClass(/active/);
  await page.goBack();
  await page.getByRole('button', { name: 'Things to try' }).click();
  await scan();
  await card.getByRole('button', { name: 'Save none' }).click();
  await expect(receipt).toContainText('No tools saved');
  await capture(receipt, info.outputPath('apps-no-save.png'));
  expect(await read()).toEqual(saved);
  expect(scans).toBe(2);
});

test('no invitation on an explicit station link; detector denial is not an empty result', async ({
  page,
  request
}) => {
  await installLocalCdn(page);
  let tries = 0;
  await page.route('**/api/onboarding/detect**', route =>
    ++tries === 1
      ? route.fulfill({ status: 503, json: { error: 'unavailable' } })
      : route.fulfill({ json: { names: [] } })
  );
  await page.goto(base + '?mode=details#notes');
  const card = page.locator('[data-hq-firstvisit]');
  const door = page.getByRole('button', { name: 'Things to try' });
  await expect(door).toBeVisible();
  await expect(card).toHaveCount(0);
  await door.click();
  await card.getByRole('button', { name: /Scan recent apps/ }).click();
  await card.getByRole('button', { name: 'Start scan' }).click();
  await expect(card).toContainText('evidence is unavailable');
  await expect(card).not.toContainText('No recently used apps found');
  await card.getByRole('button', { name: 'Start scan' }).click();
  await expect(card).toContainText('No recently used apps found');
  await card.getByRole('button', { name: 'Save none' }).click();
  await expect(page.locator('[data-guided-receipt]')).toContainText('No tools saved');
  expect(tries).toBe(2);
  const finish = await request.post('/api/personal-hq/first-visit-state', {
    data: { state: 'done' }
  });
  expect(finish.ok()).toBeTruthy();
  await page.goto(base + '?mode=details');
  await expect(door).toBeVisible();
  await expect(card).toHaveCount(0);
  await door.click();
  await expect(card).toBeVisible();
});
