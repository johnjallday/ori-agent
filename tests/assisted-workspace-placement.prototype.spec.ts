import { test, expect, type Page } from '@playwright/test';
import { mkdir } from 'node:fs/promises';

// Inert review-component fixture only, injected by Playwright. Real reviewed
// click/Enter placement is exercised in assisted-workspace-placement.spec.ts.
// No product fixture route or successful create/grant/scan response exists here.
async function prototype(page: Page) {
  await page.goto('/');
  await page.waitForFunction(() => Boolean((window as any).OriWorkspaceMap));
  const effects: string[] = [];
  await page.route('**/api/**', route => {
    if (route.request().method() !== 'GET') {
      effects.push(`${route.request().method()} ${new URL(route.request().url()).pathname}`);
      return route.abort('blockedbyclient');
    }
    return route.fallback();
  });
  await page.addStyleTag({ url: '/css/assisted-workspace-placement.css' });
  await page.addStyleTag({
    content:
      '#placement-prototype { position:fixed; inset:0; z-index:10010; overflow:auto; padding:16px; background:var(--bg-primary); }'
  });
  await page.evaluate(async () => {
    const moduleURL = '/js/modules/assisted-workspace-placement.js';
    const { mountPlacementPanel } = await import(moduleURL);
    const host = document.createElement('main');
    host.id = 'placement-prototype';
    document.body.append(host);
    const view: any = {
      prototype: true,
      automatic: true,
      draft: { name: 'Music', mission: 'Help me review and file my incoming music documents.' },
      team: [{ name: 'File Curator', action: 'create' }],
      folder: { name: 'Music', path: '/synthetic/Inbox/Music' },
      monitoring: false,
      message:
        'Prototype only. No setup API is connected; this is not permission or creation evidence.'
    };
    const panel = mountPlacementPanel(host, (intent: any) => {
      if (intent.action === 'dirty') return;
      if (intent.action === 'edit') {
        if (intent.field === 'monitoring') view.monitoring = intent.value;
        else view.draft[intent.field] = intent.value;
      } else if (intent.action === 'agent') {
        view.team = [{ name: 'Saved Curator', action: 'attach' }];
      } else if (intent.action === 'folder') {
        view.folder = { name: 'Music', path: '/synthetic/Another/Music' };
      } else if (intent.action === 'cancel') {
        view.closed = true;
        view.message =
          'Draft cancelled. No workspace, agent, access, Filed folder, scan or monitoring was created.';
      }
      render();
    });
    function render() {
      view.review = {
        workspace: view.draft,
        team: view.team,
        folder: view.folder,
        monitoring: view.monitoring,
        terms: { daily_scan_local_time: '09:00' }
      };
      view.canConfirm = !view.closed;
      // Map authority/confirmation is intentionally absent from this fixture.
      view.canPlace = false;
      panel.update(view);
    }
    render();
  });
  return effects;
}

for (const narrow of [false, true]) {
  test(`Prototype: ${narrow ? 'narrow keyboard' : 'desktop'} review and cancel are inert`, async ({
    page
  }) => {
    await page.setViewportSize(
      narrow ? { width: 390, height: 844 } : { width: 1440, height: 1100 }
    );
    await page.emulateMedia({ reducedMotion: 'reduce' });
    const effects = await prototype(page);
    const panel = page.locator('#placement-prototype');
    await panel.getByLabel('Workspace name').fill('Music inbox');
    await page.keyboard.press('Tab');
    await expect(panel.getByRole('region', { name: 'Before you confirm' })).toContainText(
      'Music inbox'
    );
    await panel.getByRole('button', { name: 'Choose agent' }).click();
    await expect(panel).toContainText('Saved Curator · saved agent');
    await expect(panel).toContainText('Off · manual scans only.');
    await expect(panel.getByRole('button', { name: 'Create workspace & scan' })).toBeEnabled();
    await expect(panel.getByRole('button', { name: 'Place & create on map' })).toBeDisabled();
    await mkdir('tasks/evidence-assisted-workspace-placement/group-1', { recursive: true });
    const prefix = `tasks/evidence-assisted-workspace-placement/group-1/prototype-${narrow ? 'narrow' : 'desktop'}`;
    await page.screenshot({ path: `${prefix}-review.png` });
    const cancel = panel.getByRole('button', { name: 'Cancel draft' });
    if (narrow) {
      await cancel.focus();
      await page.keyboard.press('Enter');
    } else await cancel.click();
    await expect(panel.getByRole('status')).toContainText('No workspace, agent, access');
    await expect(panel.getByRole('button', { name: 'Create workspace & scan' })).toBeDisabled();
    await page.screenshot({ path: `${prefix}-cancel.png` });
    expect(effects).toEqual([]);
  });
}
