import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import { installLocalCdn } from './helpers/offline-cdn';

/**
 * "Where settings come from" on the full agent page (/agents/{name}).
 *
 * Real host data seeded by ./scripts/settings-ownership-demo.sh (run it as the
 * demo server, then point ./scripts/e2e.sh at its port). Workspace copies are
 * written by the server's startup snapshot, so they cannot be created from a
 * spec; the script restarts its own sandboxed server once to take them.
 * The specs only read: they never change the seeded records.
 *
 * Groups can run alone with -g: "slice 1" (library vs workspace value/source,
 * unavailable context, read-only).
 */

test.beforeEach(async ({ page }) => {
  await installLocalCdn(page);
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await page.route('**/api/onboarding/status', route =>
    route.fulfill({ json: { needs_onboarding: false, completed: true, skipped: true } })
  );
});

type Ref = { id: string; name: string };

// fixture finds the seeded records through the inspection API itself.
async function fixture(request: APIRequestContext) {
  const name = 'Ownership Writer';
  const res = await request.get(`/api/agents/${encodeURIComponent(name)}/settings-ownership`);
  expect(
    res.ok(),
    'Seeded fixture missing: run ./scripts/settings-ownership-demo.sh <port> and target that port'
  ).toBeTruthy();
  const body = await res.json();
  const byName = (wanted: string): Ref => {
    const ws = body.contexts.workspaces.find((w: Ref) => w.name === wanted);
    expect(ws, `seeded workspace ${wanted}`).toBeTruthy();
    return { id: ws.id, name: ws.name };
  };
  return {
    name,
    same: byName('Ownership Same'),
    custom: byName('Ownership Custom'),
    direct: byName('Ownership Direct'),
    cleanup: async () => undefined
  };
}

const section = (page: Page) => page.locator('#settingsOwnershipSection');
const row = (page: Page, key: string) => section(page).locator(`[data-field="${key}"]`);

async function openAgent(page: Page, name: string, query = '') {
  await page.goto(`/agents/${encodeURIComponent(name)}${query}`);
  await expect(page.locator('#agentName')).toHaveText(name);
  await expect(section(page)).toBeVisible();
}

// Every non-GET request the page sends while only inspecting.
function recordWrites(page: Page) {
  const writes: string[] = [];
  page.on('request', req => {
    if (req.method() !== 'GET' && req.url().includes('/api/'))
      writes.push(`${req.method()} ${req.url()}`);
  });
  return writes;
}

test.describe('slice 1', () => {
  test('library and workspace contexts show their own saved value and source', async ({
    page,
    request
  }) => {
    const f = await fixture(request);
    try {
      const writes = recordWrites(page);
      await openAgent(page, f.name);
      await expect(section(page)).toContainText('Library profile');
      await expect(row(page, 'model')).toContainText('gpt-4o-mini');
      await expect(section(page).locator('.settings-ownership-source')).toContainText(
        'Library profile'
      );

      const picker = page.getByLabel('Viewing');
      await expect(picker).toBeEnabled();
      await picker.selectOption({ label: `Workspace: ${f.custom.name}` });
      await expect(section(page)).toContainText(`In workspace “${f.custom.name}”`);
      await expect(row(page, 'model')).toContainText('gpt-4.1');
      await expect(row(page, 'model')).toContainText('Differs from library');
      await expect(section(page).locator('.settings-ownership-source')).toContainText(
        "This workspace's own saved copy"
      );
      await expect(page).toHaveURL(new RegExp(`studio_id=${f.custom.id}`));

      // The equal copy is still the workspace's own, independent copy.
      await picker.selectOption({ label: `Workspace: ${f.same.name}` });
      await expect(row(page, 'model')).toContainText('gpt-4o-mini');
      await expect(row(page, 'model')).toContainText('Same as library');
      await expect(section(page)).toContainText('independent copy');

      // A workspace without its own copy reads the library: not "inherited".
      await picker.selectOption({ label: `Workspace: ${f.direct.name}` });
      await expect(section(page).locator('.settings-ownership-source')).toContainText(
        'read directly'
      );
      await expect(row(page, 'model')).toContainText('gpt-4o-mini');
      await expect(row(page, 'model')).not.toContainText('Same as library');
      await picker.selectOption({ label: `Workspace: ${f.same.name}` });
      await expect(row(page, 'model')).toContainText('Same as library');

      // Reload keeps the context; Back returns to the previous one.
      await page.reload();
      await expect(section(page)).toContainText(`In workspace “${f.same.name}”`);
      await page.goBack();
      await expect(section(page)).toContainText(`In workspace “${f.direct.name}”`);
      await expect(page.getByLabel('Viewing')).toHaveValue(`workspace:${f.direct.id}`);
      await expect(section(page).locator('.settings-ownership-source')).toContainText(
        'read directly'
      );

      // Inspection added no Save and sent no write.
      await expect(section(page).getByRole('button', { name: /save/i })).toHaveCount(0);
      expect(writes).toEqual([]);
    } finally {
      await f.cleanup();
    }
  });

  test('an unavailable workspace is a recoverable state, never another workspace', async ({
    page,
    request
  }) => {
    const f = await fixture(request);
    try {
      await openAgent(page, f.name, '?ctx=workspace&studio_id=no-such-workspace');
      await expect(section(page)).toContainText('In an unavailable workspace');
      await expect(section(page)).toContainText('not available');
      await expect(section(page).locator('[data-field]')).toHaveCount(0);
      await expect(page.getByLabel('Viewing')).toHaveValue('workspace:no-such-workspace');
      await page.getByLabel('Viewing').selectOption({ label: 'Library profile' });
      await expect(row(page, 'model')).toContainText('gpt-4o-mini');
    } finally {
      await f.cleanup();
    }
  });

  test('a failed read offers retry in place (injected failure)', async ({ page, request }) => {
    const f = await fixture(request);
    try {
      let fail = true;
      await page.route('**/settings-ownership*', route =>
        fail ? route.fulfill({ status: 500, json: { error: 'injected' } }) : route.continue()
      );
      await openAgent(page, f.name);
      await expect(section(page)).toContainText('could not be loaded');
      fail = false;
      await section(page).getByRole('button', { name: 'Retry' }).click();
      await expect(row(page, 'model')).toContainText('gpt-4o-mini');
    } finally {
      await f.cleanup();
    }
  });
});
