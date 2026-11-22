import { test, expect } from '@playwright/test';
import { realpathSync, mkdirSync, writeFileSync } from 'node:fs';
import { join, relative } from 'node:path';
import { tmpdir } from 'node:os';
import { installLocalCdn } from './helpers/offline-cdn';

const sandbox = process.env.ORI_MUSIC_HIERARCHY_SANDBOX;
const evidence = 'tasks/evidence-music-folder-hierarchy';
test.skip(!sandbox, 'requires a fresh staged Music Home sandbox');

const both = process.env.ORI_MUSIC_HIERARCHY_BOTH === '1';
test(
  both
    ? 'review both branches and choose only the two tracks on the shelf'
    : 'confirming only Picker Songs shelves two tracks without activating them',
  async ({ page, request }) => {
    test.setTimeout(180000);
    const root = realpathSync(sandbox!);
    expect(relative(realpathSync(tmpdir()), root)).toMatch(/^ori-music-hierarchy\.[^/]+$/);
    mkdirSync(evidence, { recursive: true });
    // A branch over the walk's per-directory budget is unknown, not empty.
    const partial = join(root, 'fixture', 'Ori Music Demo', 'Uninspected', 'Too large');
    mkdirSync(partial, { mode: 0o750, recursive: true });
    for (let i = 0; i <= 5000; i++) writeFileSync(join(partial, String(i)), '', { mode: 0o600 });
    await installLocalCdn(page);
    await page.goto('/?panel=today&folder=show');
    await page
      .locator('.folder-path-entry__input:visible')
      .first()
      .fill(join(root, 'fixture', 'Ori Music Demo'));
    await page.getByRole('button', { name: 'Use this path', exact: true }).first().click();
    const started = page.waitForResponse(r => r.url().endsWith('/folder-agent/start'));
    await page
      .locator('[data-folder-agent-card="choose"]:visible')
      .getByRole('button', { name: 'Hire & build', exact: true })
      .click();
    const start = await started;
    expect(start.ok(), await start.text()).toBeTruthy();
    const id = (await start.json()).folder_agent.workspace_id;
    const base = `/api/personal-assistant/folder-agent/${id}`;
    const read = async () => (await (await request.get(base)).json()).folder_agent;
    await expect.poll(async () => (await read()).state, { timeout: 120000 }).toBe('reported');
    const report = await read();
    if (report.premise?.state === 'asked')
      expect(
        (await request.post(`${base}/premise`, { data: { answer: 'confirmed' } })).ok()
      ).toBeTruthy();
    await page.goto(report.route);
    const close = page.getByRole('button', { name: 'Close personal assistant', exact: true });
    if (await close.isVisible()) await close.click();
    await page.getByRole('button', { name: 'Open in chat', exact: true }).click();
    const card = page.locator('.folder-agent-card--report:visible').last();
    const home = card.locator('input[data-step-handle="music-production-home"]');
    if (await home.count()) await home.check();
    else {
      await card.locator('.folder-agent-card__alternatives-toggle').click();
      await card.locator('[data-alt-step="music-production-home"]').click();
    }
    const branches = card.getByRole('group', { name: 'Folders to review' });
    await expect(branches.getByRole('checkbox', { name: /Albums/ })).toBeChecked();
    await expect(branches.getByRole('checkbox', { name: /Picker Songs/ })).toBeChecked();
    await expect(branches).toContainText('Filed · Empty in the look');
    await expect(branches).toContainText('Uninspected · Not fully inspected');
    await branches.screenshot({ path: join(evidence, 'intake-both.png') });
    if (!both) {
      await branches.getByRole('checkbox', { name: /Albums/ }).uncheck();
      await expect(
        branches.getByRole('checkbox', { name: /Albums.*Not selected/ })
      ).not.toBeChecked();
      await branches.screenshot({ path: join(evidence, 'intake-picker.png') });
    }
    await card.locator('[data-folder-agent-action="set_up"]').click();
    await expect
      .poll(
        async () => {
          const r = await read();
          return r.setup?.stop_reason || r.setup?.status;
        },
        { timeout: 100000 }
      )
      .toBe('done');
    const receipt = (await read()).setup.receipt;
    const listed = await request.get(
      `/api/workspaces/${receipt.home.id}/assistant-program/library/projects?page_size=50`
    );
    expect(listed.ok()).toBeTruthy();
    const rows = (await listed.json()).rows;
    expect(rows.every((r: any) => r.connection === 'catalog_only')).toBeTruthy();
    if (!both) {
      expect(rows.map((r: any) => r.name).sort()).toEqual(['Track-1', 'Track-2']);
      return;
    }
    expect(rows).toHaveLength(8);
    const landing = (await read()).landing.route;
    await page.goto(landing);
    const sheet = page.locator(`[data-library-sheet="${receipt.home.id}"]`);
    const tracks = sheet.getByRole('checkbox', {
      name: /Select projects with a chosen file in .*Picker Songs/
    });
    await tracks.check();
    await expect(
      sheet.getByRole('button', { name: 'Put 2 on the map', exact: true })
    ).toBeEnabled();
    await expect(
      sheet.getByRole('checkbox', { name: /Select projects with a chosen file in .*Albums/ })
    ).not.toBeChecked();
    await expect(
      sheet.getByRole('radiogroup', { name: 'Project file for Album-5' }).getByRole('radio')
    ).toHaveCount(2);
    await expect(
      sheet.locator('li.is-dashboard').filter({ hasText: 'Logic Sketch' })
    ).toBeVisible();
    await sheet.screenshot({ path: join(evidence, 'sheet-two-tracks.png') });
    await sheet.getByRole('button', { name: 'Cancel', exact: true }).click();
    const after = await request.get(
      `/api/workspaces/${receipt.home.id}/assistant-program/library/projects?page_size=50`
    );
    expect((await after.json()).rows.filter((r: any) => r.connection === 'connected')).toHaveLength(
      0
    );
  }
);
