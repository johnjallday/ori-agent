import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import { lstatSync, readFileSync, readdirSync } from 'node:fs';
import { join } from 'node:path';

// The Notes station's panel, in the browser, against the sandboxes that
// scripts/second-brain-demo.sh prints. One spec file covers both shapes; each
// case skips unless its sandbox is named.
//
// A connected vault:
//   ./scripts/second-brain-demo.sh --flavour research 8947
//   PLAYWRIGHT_BASE_URL=http://localhost:8947 SECOND_BRAIN_VAULT="<VAULT from READY>" \
//     npx playwright test tests/second-brain-panel.spec.ts --project=chromium --workers=1
//
// A second brain Ori set up:
//   ./scripts/second-brain-demo.sh --create 8948
//   PLAYWRIGHT_BASE_URL=http://localhost:8948 SECOND_BRAIN_CREATE=1 \
//     npx playwright test tests/second-brain-panel.spec.ts --project=chromium --workers=1

const vault = process.env.SECOND_BRAIN_VAULT || '';
const create = process.env.SECOND_BRAIN_CREATE === '1';

test.describe.configure({ mode: 'serial' });

const ENDPOINT = '/api/personal-assistant/second-brain';

// Every entry's path, mode, modification time and bytes.
function folderSnapshot(root: string): string {
  const lines: string[] = [];
  const walk = (dir: string) => {
    for (const name of readdirSync(dir).sort()) {
      const path = join(dir, name);
      const info = lstatSync(path);
      lines.push(`${path}|${info.mode}|${info.mtimeMs}`);
      if (info.isDirectory()) walk(path);
      else if (info.isFile()) lines.push(readFileSync(path, 'utf8'));
    }
  };
  walk(root);
  return lines.join('\n');
}

async function secondBrain(request: APIRequestContext) {
  const response = await request.get(ENDPOINT);
  expect(response.ok(), await response.text()).toBeTruthy();
  return (await response.json()).second_brain;
}

async function waitForLook(request: APIRequestContext, notLook = '') {
  await expect
    .poll(async () => {
      const response = await request.get(`${ENDPOINT}/look`);
      if (!response.ok()) return '';
      const look = (await response.json()).look;
      return look.state === 'finished' && look.look_id !== notLook ? look.look_id : '';
    })
    .not.toBe('');
}

async function hqSlug(request: APIRequestContext): Promise<string> {
  const response = await request.get('/api/personal-assistant/today');
  expect(response.ok(), await response.text()).toBeTruthy();
  const slug = (await response.json()).today?.hq_workspace_slug;
  expect(slug).toBeTruthy();
  return slug;
}

const panel = (page: Page) => page.locator('.ws-cmd-modal-panel.is-station');
const section = (page: Page, key: string) => panel(page).locator(`[data-notes-section="${key}"]`);

async function openPanel(page: Page, request: APIRequestContext) {
  await page.goto(`/workspaces/${await hqSlug(request)}?station=second-brain`);
  await expect(panel(page).locator('.ws-cmd-modal-title')).toHaveText('Notes');
}

test.describe('a connected vault', () => {
  test.skip(!vault, 'Set SECOND_BRAIN_VAULT from a --flavour READY line.');

  test.beforeAll(async ({ request }) => {
    const current = await secondBrain(request);
    if (current.status === 'not_connected') {
      const response = await request.post(`${ENDPOINT}/connect`, {
        data: { detected_id: current.detected[0]?.id, request_id: `spec-connect-${Date.now()}` }
      });
      expect(response.ok(), await response.text()).toBeTruthy();
    }
    await waitForLook(request);
  });

  test('the waiting result card leads to the panel', async ({ page }) => {
    await page.goto('/');
    await page.waitForFunction(() =>
      Boolean(
        (
          window as unknown as {
            PersonalAssistantPanel?: { _state?: { personalAssistant?: unknown } };
          }
        ).PersonalAssistantPanel?._state?.personalAssistant
      )
    );
    if (!(await page.locator('#personalAssistantPanel').isVisible())) {
      await page.locator('#personalAssistantLauncher').click();
    }
    // Needs you is folded behind the strip until asked for.
    const strip = page.locator('#personalAssistantTodayStripToggle');
    await expect(strip).toBeVisible();
    if ((await strip.getAttribute('aria-expanded')) === 'false') await strip.click();
    const link = page.getByRole('link', { name: 'See it all in Notes' });
    await expect(link).toBeVisible();
    await expect(page.getByText(/Also needs you/)).toBeHidden();
    await link.click();
    await expect(panel(page).locator('.ws-cmd-modal-title')).toHaveText('Notes');
    expect(page.url()).not.toContain('station=');
  });

  test('the panel groups open loops and has no Inbox, Today, Capture or Projects', async ({
    page,
    request
  }) => {
    await openPanel(page, request);
    await expect(section(page, 'loops').locator('.ws-notes-item').first()).toBeVisible();
    await expect(section(page, 'people')).toContainText('mention');
    await expect(section(page, 'footer')).toContainText('open item');
    for (const absent of ['capture', 'inbox', 'today', 'projects']) {
      await expect(section(page, absent)).toHaveCount(0);
    }
  });

  test('triage files three items without a reload and writes nothing', async ({
    page,
    request
  }) => {
    await openPanel(page, request);
    const loops = section(page, 'loops');
    const start = await loops.locator('.ws-notes-list .ws-notes-item').count();
    expect(start).toBeGreaterThanOrEqual(3);
    const folderBefore = folderSnapshot(vault);
    const before = page.url();
    for (const [action, label] of [
      ['follow_up', 'Follow-up made'],
      ['done', 'Done'],
      ['dismiss', 'Dismissed']
    ]) {
      const row = loops.locator('.ws-notes-list:not(.ws-notes-handled *) .ws-notes-item').first();
      await row.hover();
      await row.locator(`[data-cmd-modal-action="notes-item-${action}"]`).click();
      await expect(loops.locator('.ws-notes-handled')).toContainText(label);
    }
    expect(page.url()).toBe(before);
    await expect(loops.locator('.ws-notes-handled summary')).toHaveText('3 handled');
    // An expanded list stays expanded when a Done inside it repaints the panel.
    await loops.locator('.ws-notes-handled summary').click();
    await loops.locator('.ws-notes-handled [data-cmd-modal-action="notes-item-done"]').click();
    await expect(loops.locator('.ws-notes-handled')).toHaveAttribute('open', '');
    await expect(loops.locator('.ws-notes-handled')).not.toContainText('Follow-up made');
    const after = await secondBrain(request);
    expect(after.last_look.handled).toBe(3);
    expect(folderSnapshot(vault)).toBe(folderBefore);
  });

  test('Look again inside the limit says so', async ({ page, request }) => {
    await openPanel(page, request);
    await section(page, 'footer').locator('[data-cmd-modal-action="notes-look-again"]').click();
    await expect(section(page, 'footer')).toContainText('Looked less than 10 minutes ago');
  });
});

test.describe('a second brain Ori set up', () => {
  test.skip(!create, 'Set SECOND_BRAIN_CREATE=1 against a --create READY line.');

  test.beforeAll(async ({ request }) => {
    const current = await secondBrain(request);
    if (current.status === 'not_connected') {
      const response = await request.post(`${ENDPOINT}/create`, {
        data: { request_id: `spec-create-${Date.now()}` }
      });
      expect(response.ok(), await response.text()).toBeTruthy();
    }
    await waitForLook(request);
  });

  test('the panel opens on Inbox, and a capture lands at its top', async ({ page, request }) => {
    await openPanel(page, request);
    await expect(section(page, 'inbox').locator('.ws-notes-item').first()).toBeVisible();
    await expect(section(page, 'projects')).toBeVisible();
    const note = `Call Dana about the mix ${Date.now()}`;
    await section(page, 'capture').locator('[data-notes-capture]').fill(note);
    await section(page, 'capture').locator('[data-cmd-modal-action="notes-capture"]').click();
    await expect(section(page, 'capture')).toContainText('Saved to Inbox');
    await expect(section(page, 'inbox').locator('.ws-notes-item').first()).toContainText(note);
    await expect(section(page, 'capture').locator('[data-notes-capture]')).toBeVisible({
      timeout: 6000
    });
  });

  test('Today moves from none to saved through the Journal', async ({ page, request }) => {
    await openPanel(page, request);
    const today = section(page, 'today');
    await expect(today).toContainText('No note for today yet.');
    await today.locator('[data-notes-journal]').click();
    await expect(page.locator('.hq-journal-editor')).toBeVisible();
    await page.locator('.hq-journal-editor').fill('Shipped the mix and talked to Dana.');
    await page.getByRole('button', { name: 'Save journal' }).click();
    await expect(page.locator('.hq-journal-launch')).toBeVisible();
    await openPanel(page, request);
    await expect(section(page, 'today')).toContainText('saved');
  });
});
