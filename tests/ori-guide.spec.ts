import { test, expect, Page } from '@playwright/test';
import { mockHiredAssistant } from './helpers/hired-assistant';
import { installLocalCdn } from './helpers/offline-cdn';

// Real guide endpoint, isolated wt demo only. Relationship fixtures suppress
// unrelated first-run overlays; they do not mock Help or demonstrate model work.
async function gotoPage(page: Page, route: string) {
  await installLocalCdn(page);
  await page.route('**/api/onboarding/status', route =>
    route.fulfill({
      json: { needs_onboarding: false, completed: true, skipped: true }
    })
  );
  await mockHiredAssistant(page);
  await page.goto(route, { waitUntil: 'domcontentloaded' });
  await expect(page.getByRole('button', { name: 'Help', exact: true })).toBeVisible();
}

async function openGuide(page: Page) {
  await page.getByRole('button', { name: 'Help', exact: true }).click();
  await expect(page.locator('#oriGuidePanel')).toBeVisible();
  await expect(page.locator('#oriGuideReply')).toHaveAttribute(
    'data-status',
    /unknown|answered|unavailable/
  );
}

async function ask(page: Page, question: string) {
  await page
    .locator('#oriGuideReply')
    .evaluate(el => el.setAttribute('data-status', 'awaiting-test'));
  await page.getByLabel('Search help', { exact: true }).fill(question);
  await page.getByRole('button', { name: 'Search', exact: true }).click();
  await expect(page.locator('#oriGuideReply')).not.toHaveAttribute('data-status', 'awaiting-test');
}

for (const [route, screen] of [
  ['/', 'Home'],
  ['/agents', 'Agents'],
  ['/settings', 'Settings'],
  ['/workspaces', 'Workspaces']
]) {
  test(`navbar Help opens and closes on ${screen}`, async ({ page, request }, testInfo) => {
    let workspaceId = '';
    let targetRoute = route;
    if (route === '/workspaces') {
      const created = await request.post('/api/workspaces', {
        data: { name: `Help screen ${Date.now()}`, workspace_preset: 'general' }
      });
      expect(created.ok()).toBeTruthy();
      const workspace = (await created.json()).folder;
      workspaceId = workspace.id;
      // Canvas is a real workspace shell without the detail page's mandatory
      // Commander-creation dialog on an empty fixture.
      targetRoute = `/workspaces/${workspace.folder_slug}/canvas`;
    }
    await gotoPage(page, targetRoute);
    await expect(page.locator('#oriGuideLauncher')).toHaveCount(1);
    await expect(page.locator('#oriGuidePanel')).toHaveCount(1);
    await expect(page.locator('#personalAssistantLauncher')).toHaveCount(1);
    await expect(page.locator('#oriGuideMapTrigger, .ori-guide__launcher')).toHaveCount(0);
    await openGuide(page);
    await expect(page.locator('#oriGuideTitle')).toHaveText('Help');
    await expect(page.locator('#oriGuideContext')).toHaveText(screen);
    await expect(page.getByRole('heading', { name: 'About this screen' })).toBeVisible();
    await expect(page.locator('#oriGuideAbout')).not.toHaveText(/Loading/);
    await expect(page.getByRole('heading', { name: 'Common questions' })).toBeVisible();
    await expect(page.locator('.ori-guide__topic').first()).toBeVisible();
    await expect(page.locator('#oriGuideQuestPortrait')).toBeHidden();
    await expect(
      page.locator('#oriGuidePanel #homeAssistantThinkingModal, #oriGuideActivity')
    ).toHaveCount(0);
    await expect(
      page.locator('#personalAssistantActivityMount #homeAssistantThinkingModal')
    ).toHaveCount(1);
    if (route === '/' || route === '/agents') {
      await page.screenshot({
        path: testInfo.outputPath(`help-${screen.toLowerCase()}-initial.png`)
      });
    }
    await ask(page, 'what is a vault');
    await expect(page.locator('#oriGuideReply')).toHaveAttribute('data-status', 'answered');
    const action = page.locator('.ori-guide__action', { hasText: 'Vaults' });
    await expect(action).toHaveJSProperty('tagName', 'A');
    await expect(action).toHaveAttribute('href', '/vaults');
    if (route === '/' || route === '/agents') {
      await page.screenshot({
        path: testInfo.outputPath(`help-${screen.toLowerCase()}-topic.png`)
      });
    }
    await page.locator('#oriGuideClose').click();
    await expect(page.locator('#oriGuidePanel')).toBeHidden();
    await expect(page.locator('#oriGuideLauncher')).toBeFocused();
    await expect(page.locator('#personalAssistantLauncher')).toBeVisible();
    await openGuide(page);
    await action.click();
    await expect(page).toHaveURL(/\/vaults$/);
    if (workspaceId) await request.delete(`/api/workspaces/${workspaceId}`);
  });
}

test('Help has one labelled search, no work composer or ordinary avatar', async ({ page }) => {
  await gotoPage(page, '/');
  await openGuide(page);
  await expect(
    page.locator('#oriGuidePanel input[type="text"], #oriGuidePanel textarea')
  ).toHaveCount(1);
  await expect(page.getByLabel('Search help', { exact: true })).toHaveAttribute('maxlength', '400');
  await expect(page.locator('#oriGuidePanel')).not.toContainText('App Guide');
  await expect(page.locator('#oriGuidePanel')).not.toContainText('Send');
  await expect(page.locator('#oriGuideSearchHint')).toContainText('not full-text');
});

for (const status of ['loading', 'failed', 'active']) {
  test(`Help never calls work endpoints with ${status} relationship status`, async ({ page }) => {
    await installLocalCdn(page);
    await page.route('**/api/onboarding/status', route =>
      route.fulfill({ json: { completed: true, needs_onboarding: false } })
    );
    if (status === 'active') await mockHiredAssistant(page);
    else
      await page.route('**/api/personal-assistant', route =>
        status === 'failed' ? route.fulfill({ status: 503, json: {} }) : new Promise<void>(() => {})
      );
    let workCalls = 0;
    page.on('request', request => {
      if (/\/api\/home-assistant\/(route|ask)(?:\?|$)/.test(request.url())) workCalls += 1;
    });
    await page.goto('/settings', { waitUntil: 'domcontentloaded' });
    await openGuide(page);
    for (const text of [
      'what is the airspeed velocity of an unladen swallow',
      'draft a note',
      '/task run this',
      '/ask what is a workspace',
      '/note private idea'
    ]) {
      await ask(page, text);
      await expect(page.locator('#oriGuidePanel')).toBeVisible();
      await expect(page.locator('#oriGuideReply')).not.toHaveAttribute(
        'data-status',
        /routed|delegated/
      );
      expect(page.url()).not.toContain('private');
    }
    expect(workCalls).toBe(0);
  });
}

test('unknown questions are honest misses with common topics, not automatic handoffs', async ({
  page
}) => {
  await gotoPage(page, '/');
  await openGuide(page);
  await ask(page, 'what is the airspeed velocity of an unladen swallow');
  await expect(page.locator('#oriGuideReply')).toHaveAttribute('data-status', 'unknown');
  await expect(page.locator('.ori-guide__answer')).toContainText('No matching help topic');
  await expect(page.locator('.ori-guide__action')).toHaveCount(0);
  await expect(page.locator('.ori-guide__topic').first()).toBeVisible();
});

test('suggested topics can be searched by clicking', async ({ page }) => {
  await gotoPage(page, '/agents');
  await openGuide(page);
  await page.locator('.ori-guide__topic', { hasText: 'Agent' }).first().click();
  await expect(page.locator('#oriGuideReply')).toHaveAttribute('data-status', 'answered');
});

test('vault guidance reveals no secrets and preserves the read-only boundary', async ({ page }) => {
  await gotoPage(page, '/');
  await openGuide(page);
  await ask(page, 'where are my stored credentials');
  const answer = await page.locator('#oriGuideReply').innerText();
  expect(answer).toContain('write-only');
  expect(answer).toContain('cannot read them');
  expect(answer).not.toMatch(/sk-[A-Za-z0-9]/);
});

test('Help links respect the real unsaved-changes guard', async ({ page }) => {
  await gotoPage(page, '/workflows');
  await page.locator('#behaviorStudioNewBtn').click();
  let guarded = false;
  page.on('dialog', async dialog => {
    if (dialog.type() === 'beforeunload') guarded = true;
    await dialog.dismiss();
  });
  await openGuide(page);
  await ask(page, 'what is a vault');
  await page.locator('.ori-guide__action', { hasText: 'Vaults' }).click();
  await expect.poll(() => guarded).toBe(true);
  await expect(page).toHaveURL(/\/workflows/);
});

test('naming a real workspace offers its validated destination', async ({ page, request }) => {
  const name = `PW Guide WS ${Date.now()}`;
  const created = await request.post('/api/workspaces', {
    data: { name, workspace_preset: 'general' }
  });
  expect(created.ok()).toBeTruthy();
  const workspace = (await created.json())?.folder;
  expect(workspace?.folder_slug).toBeTruthy();
  await gotoPage(page, '/');
  await openGuide(page);
  await ask(page, `where is my ${name} workspace`);
  await expect(page.locator('.ori-guide__action', { hasText: name })).toHaveAttribute(
    'href',
    `/workspaces/${workspace.folder_slug}`
  );
  await request.delete(`/api/workspaces/${workspace.id}`);
});

test('a nonexistent workspace cannot invent a destination', async ({ page }) => {
  await gotoPage(page, '/');
  await openGuide(page);
  await ask(page, 'open my Nonexistent Zeta Workspace');
  const hrefs = await page
    .locator('.ori-guide__action')
    .evaluateAll(els => els.map(el => el.getAttribute('href') || ''));
  expect(hrefs.some(href => /^\/workspaces\/[^/]+/.test(href))).toBe(false);
});

test('coachmarks focus real controls without activating them and dismiss in layers', async ({
  page
}) => {
  await gotoPage(page, '/agents');
  await openGuide(page);
  await ask(page, 'what is an agent');
  await page.locator('.ori-guide__action', { hasText: 'Show me where' }).click();
  const target = page.locator('#newAgentBtn');
  await expect(target).toHaveClass(/is-ori-coachmark/);
  await expect(target).toBeFocused();
  await expect(page.locator('#addAgentModal')).toBeHidden();
  await page.keyboard.press('Escape');
  await expect(target).not.toHaveClass(/is-ori-coachmark/);
  await expect(page.locator('#oriGuidePanel')).toBeVisible();
  await page.keyboard.press('Escape');
  await expect(page.locator('#oriGuidePanel')).toBeHidden();
});

test('coachmarks are not offered on routes without their target', async ({ page }) => {
  await gotoPage(page, '/vaults');
  await openGuide(page);
  await ask(page, 'what is an agent');
  await expect(page.locator('.ori-guide__action', { hasText: 'Show me where' })).toHaveCount(0);
  await expect(page.locator('.ori-guide__action', { hasText: 'Agents' })).toBeVisible();
});

test('closing Help clears marks and preserves Agents collection state', async ({ page }) => {
  await gotoPage(page, '/agents');
  await page.locator('#rosterSearch').fill('a');
  const before = await page.locator('.roster-card').count();
  await openGuide(page);
  await ask(page, 'what is an agent');
  await page.locator('.ori-guide__action', { hasText: 'Show me where' }).click();
  await page.locator('#oriGuideClose').click();
  await expect(page.locator('#newAgentBtn')).not.toHaveClass(/is-ori-coachmark/);
  await expect(page.locator('#rosterSearch')).toHaveValue('a');
  await expect(page.locator('.roster-card')).toHaveCount(before);
});

test('unavailable Help keeps search text and the page usable', async ({ page }) => {
  await gotoPage(page, '/agents');
  await page.route('**/api/ori-guide', route => route.abort());
  await openGuide(page);
  await ask(page, 'what is an agent');
  await expect(page.locator('#oriGuideReply')).toHaveAttribute('data-status', 'unavailable');
  await expect(page.locator('#oriGuideInput')).toHaveValue('what is an agent');
  await expect(page.locator('.ori-guide__answer')).toContainText('page still works');
  await expect(page.locator('#oriGuideSend')).toBeEnabled();
  await page.locator('#oriGuideClose').click();
  await page.locator('#rosterSearch').fill('zzz');
  await expect(page.locator('#rosterSearch')).toHaveValue('zzz');
});

test('work searches never claim completion or expose destructive controls', async ({ page }) => {
  await gotoPage(page, '/agents');
  await openGuide(page);
  await ask(page, 'delete all my agents');
  await expect(page.locator('#oriGuideReply')).toHaveAttribute('data-status', 'answered');
  const answer = (await page.locator('.ori-guide__answer').innerText()).toLowerCase();
  expect(answer).toContain('nothing has been sent');
  expect(answer).not.toMatch(/i deleted|i.ve deleted|taken care of/);
  const labels = await page.locator('.ori-guide__action').allInnerTexts();
  expect(labels.join(' ').toLowerCase()).not.toMatch(/delete|confirm|remove|execute/);
});

test('keyboard-only Help has correct focus, expanded state and non-modal semantics', async ({
  page
}) => {
  await gotoPage(page, '/');
  await page.locator('#oriGuideLauncher').focus();
  await page.keyboard.press('Enter');
  await expect(page.locator('#oriGuideInput')).toBeFocused();
  await page.keyboard.type('what is a workspace');
  await page.keyboard.press('Enter');
  await expect(page.locator('#oriGuideReply')).toHaveAttribute('data-status', 'answered');
  await expect(page.locator('#oriGuidePanel')).toHaveAttribute('role', 'dialog');
  await expect(page.locator('#oriGuidePanel')).not.toHaveAttribute('aria-modal', 'true');
  await expect(page.locator('#oriGuideReply')).toHaveAttribute('aria-live', 'polite');
  await expect(page.locator('#oriGuideLauncher')).toHaveAttribute('aria-expanded', 'true');
  await page.keyboard.press('Escape');
  await expect(page.locator('#oriGuideLauncher')).toBeFocused();
});

test('Home has one Help utility and one assistant work composer, no retired strip', async ({
  page
}) => {
  await gotoPage(page, '/');
  await expect(
    page.locator('#homeAssistantInput, .home-command-kicker, .home-command-strip, .cockpit-map-ori')
  ).toHaveCount(0);
  await expect(page.locator('#oriGuideInput')).toHaveCount(1);
  await expect(page.locator('#personalAssistantInput')).toHaveCount(1);
  await expect(page.locator('#oriGuidePanel')).toBeHidden();
});

test('closed panel roots do not intercept Home controls or narrow-screen taps', async ({
  page
}) => {
  for (const [width, height] of [
    [1280, 800],
    [390, 844]
  ]) {
    await page.setViewportSize({ width, height });
    await gotoPage(page, '/');
    for (const selector of [
      '#cockpitCaptureBtn',
      '#cockpitSummaryBtn',
      '#cockpitRailToggle',
      '#oriGuideLauncher'
    ]) {
      const clickable = await page.locator(selector).evaluate(el => {
        const rect = el.getBoundingClientRect();
        const hit = document.elementFromPoint(
          rect.left + rect.width / 2,
          rect.top + rect.height / 2
        );
        return hit === el || el.contains(hit);
      });
      expect(clickable, `${selector} is covered at ${width}px`).toBe(true);
    }
  }
});

test('narrow Help sheet has reachable search/results and no horizontal overflow', async ({
  page
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await gotoPage(page, '/');
  await openGuide(page);
  await ask(page, 'what is a workspace');
  await expect(page.locator('#oriGuideReply')).toHaveAttribute('data-status', 'answered');
  const overflow = await page.evaluate(
    () => document.documentElement.scrollWidth - window.innerWidth
  );
  expect(overflow).toBeLessThanOrEqual(1);
  await expect(page.locator('#oriGuideClose')).toBeVisible();
});
