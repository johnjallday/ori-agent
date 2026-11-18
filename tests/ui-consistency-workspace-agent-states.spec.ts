import { expect, test } from '@playwright/test';
import { capture, preparePage } from './helpers/ui-consistency';
import {
  createWorkspaceSeed,
  freezeWorkspaceSeed,
  retireWorkspaceSeed,
  type WorkspaceSeed
} from './helpers/ui-consistency-workspace';
import {
  guardWorkspaceReadTour,
  workspaceMetadataReads
} from './fixtures/ui-consistency-workspace';

let seed: WorkspaceSeed | undefined;
let check: (() => Promise<void>) | undefined;
test.afterEach(async ({ page, request }) => {
  test.setTimeout(120_000);
  await page.goto('about:blank');
  try {
    if (check) await check();
  } finally {
    if (seed) await retireWorkspaceSeed(request, seed);
    seed = undefined;
    check = undefined;
  }
});
for (const theme of ['light', 'dark'] as const)
  for (const width of [320, 1440]) {
    test(`workspace agent loading read-only and genuine missing ownership ${theme} ${width}`, async ({
      page,
      request
    }, info) => {
      seed = await createWorkspaceSeed(request, 'group');
      await freezeWorkspaceSeed(request, seed);
      const workspace = await (await request.get(`/api/workspaces/${seed.workspace.id}`)).json();
      const agent = workspace.entry_agent_name;
      const base = `/api/workspaces/${seed.workspace.id}`;
      const path = `/workspaces/${seed.workspace.folder_slug}/agents/${encodeURIComponent(agent)}`;
      await preparePage(page, theme, width);
      await workspaceMetadataReads(page);
      check = await guardWorkspaceReadTour(page, [seed]);
      let release!: () => void;
      const gate = new Promise<void>(resolve => {
        release = resolve;
      });
      let first = true;
      await page.route(`**${base}/agents`, async route => {
        expect(route.request().method()).toBe('GET');
        if (first) {
          first = false;
          await gate;
        }
        return route.continue();
      });
      try {
        await page.goto(path, { waitUntil: 'domcontentloaded' });
        await expect(page.locator('#wad-loading')).toBeVisible();
        await capture(
          page,
          info,
          'workspace-agent-loading',
          { workspaceId: seed.workspace.id, fixture: 'Deferred genuine profile GET; no execution' },
          { fullPage: false }
        );
      } finally {
        release();
      }
      await expect(page.locator('#wad-content')).toBeVisible();
      await expect(page.locator('#wad-prompt-permission')).toContainText(
        'Edits stay in this workspace'
      );
      await page.route(`**${base}/agents/${encodeURIComponent(agent)}/system-prompt`, route => {
        expect(route.request().method()).toBe('GET');
        return route.fulfill({
          status: 403,
          json: { message: 'Fictional read-only local prompt view' }
        });
      });
      await page.reload();
      await expect(page.locator('#wad-content')).toBeVisible();
      await expect(page.locator('#wad-prompt-permission')).toContainText('Read-only here');
      await expect(page.locator('#wad-prompt-edit')).toBeHidden();
      await expect(page.locator('#wad-prompt-editor')).toBeHidden();
      await page.locator('#wad-prompt').scrollIntoViewIfNeeded();
      await capture(
        page,
        info,
        'workspace-agent-readonly',
        {
          workspaceId: seed.workspace.id,
          fixture:
            'Real owned profile plus named GET-only local-prompt refusal; no library fallback writes'
        },
        { fullPage: false }
      );
      await page.goto(
        `/workspaces/${seed.workspace.folder_slug}/agents/Not-attached-${seed.workspace.id}`
      );
      await expect(page.locator('#wad-error')).toBeVisible();
      await expect(page.locator('#wad-error-detail')).toContainText('not attached');
      await expect(page.locator('#wad-content')).toBeHidden();
      await expect(page.locator('#wad-loading')).toBeHidden();
      await check();
    });
  }
