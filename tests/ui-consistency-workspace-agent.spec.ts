import { expect, test } from '@playwright/test';
import { capture, contrast, preparePage, reachable } from './helpers/ui-consistency';
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
  for (const width of [320, 390, 768, 1440]) {
    test(`workspace-owned agent scope prompt draft and keyboard cancel ${theme} ${width}`, async ({
      page,
      request
    }, info) => {
      seed = await createWorkspaceSeed(request, 'group');
      await freezeWorkspaceSeed(request, seed);
      const base = `/api/workspaces/${seed.workspace.id}`;
      const workspace = await (await request.get(base)).json();
      const profile = (await (await request.get(`${base}/agents`)).json()).agents.find(
        agent => agent.name === workspace.entry_agent_name
      );
      expect(profile).toBeTruthy();
      const promptPath = `${base}/agents/${encodeURIComponent(profile.name)}/system-prompt`;
      const promptResponse = await request.get(promptPath);
      expect(promptResponse.ok()).toBe(true);
      const prompt = await promptResponse.json();
      await preparePage(page, theme, width);
      await workspaceMetadataReads(page);
      check = await guardWorkspaceReadTour(page, [seed]);
      await page.goto(
        `/workspaces/${seed.workspace.folder_slug}/agents/${encodeURIComponent(profile.name)}`
      );
      await expect(page.locator('#wad-content')).toBeVisible();
      await expect(page.locator('#wad-name')).toHaveText(profile.name);
      await expect(page.locator('#wad-badges')).toContainText('Workspace-local');
      await expect(page.locator('#wad-prompt-edit')).toBeVisible();
      await page.addScriptTag({ url: 'https://cdn.jsdelivr.net/npm/axe-core@4.10.3/axe.min.js' });
      const access = await page.evaluate(() =>
        window.axe.run(document.querySelector('#wad-content'), {
          runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa'] }
        })
      );
      expect(access.violations).toEqual([]);
      for (const badge of await page.locator('.wad-badge').all())
        expect((await contrast(badge)).ratio).toBeGreaterThanOrEqual(4.5);
      await capture(
        page,
        info,
        'workspace-agent-header',
        {
          workspaceId: seed.workspace.id,
          agent: profile.name,
          fixture: 'Real reviewed local group manager; no hiring, setup or provider execution'
        },
        { fullPage: false }
      );
      const edit = page.locator('#wad-prompt-edit');
      await edit.focus();
      await edit.press('Enter');
      const field = page.locator('#wad-prompt-editor');
      await expect(field).toBeFocused();
      await field.fill(
        'Unsent workspace-local prompt. Do not write to the library or run anything.'
      );
      const draft = await field.inputValue();
      await page.waitForTimeout(250);
      await expect(field).toHaveValue(draft);
      expect((await field.boundingBox())!.width).toBeGreaterThanOrEqual(160);
      await reachable(page.locator('#wad-prompt-cancel'), width <= 768 ? 44 : 32);
      await page.locator('#wad-prompt-cancel').focus();
      expect(
        await page.locator('#wad-prompt-cancel').evaluate(node => {
          const r = node.getBoundingClientRect();
          if (r.left < 0 || r.top < 0 || r.right > innerWidth + 1 || r.bottom > innerHeight + 1)
            return false;
          for (const selector of ['.navbar', '.personal-assistant-launcher']) {
            const b = document.querySelector(selector)?.getBoundingClientRect();
            if (
              b &&
              b.width &&
              b.height &&
              r.left < b.right &&
              r.right > b.left &&
              r.top < b.bottom &&
              r.bottom > b.top
            )
              return false;
          }
          return [r.left + 8, r.right - 8].every(x =>
            [r.top + 8, r.bottom - 8].every(y => {
              const hit = document.elementFromPoint(x, y);
              return hit === node || node.contains(hit);
            })
          );
        })
      ).toBe(true);
      await page.locator('#wad-prompt-cancel').press('Enter');
      await expect(field).toBeHidden();
      await expect(edit).toBeFocused();
      await edit.press('Enter');
      await expect(field).toHaveValue(String(prompt.system_prompt || ''));
      await page.locator('#wad-prompt-cancel').click();
      expect(await (await request.get(promptPath)).json()).toEqual(prompt);
      expect(
        await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1)
      ).toBe(true);
      await check();
    });
  }
