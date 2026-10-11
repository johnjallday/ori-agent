import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import { installLocalCdn } from './helpers/offline-cdn';
import { checkNextActionReflow } from './helpers/next-action-reflow';

test.beforeEach(async ({ page }) => {
  await installLocalCdn(page);
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await page.route('**/api/onboarding/status', route =>
    route.fulfill({ json: { needs_onboarding: false, completed: true } })
  );
});

async function fixture(request: APIRequestContext, entry = false) {
  const name = `PW Membership ${Date.now()}`;
  const created = await request.post('/api/agents', { data: { name, model: 'gpt-4o-mini' } });
  expect(created.ok()).toBeTruthy();
  const keeper = `${name} Keeper`;
  if (entry)
    expect(
      (await request.post('/api/agents', { data: { name: keeper, model: 'gpt-4o-mini' } })).ok()
    ).toBeTruthy();
  const workspaces: any[] = [];
  for (const suffix of ['One', 'Two']) {
    const result = await request.post('/api/workspaces', {
      data: {
        name: `${name} ${suffix}`,
        ...(entry ? { entry_agent_name: suffix === 'One' ? name : keeper } : {})
      }
    });
    expect(result.ok(), await result.text()).toBeTruthy();
    workspaces.push((await result.json()).folder);
  }
  const detailURL = `/api/agents/${encodeURIComponent(name)}/detail`;
  return {
    name,
    workspaces,
    detailURL,
    detail: async () => (await request.get(detailURL)).json(),
    cleanup: async () => {
      for (const ws of workspaces)
        expect(
          (await request.delete(`/api/workspaces/${ws.id}?confirm=true&delete_sessions=true`)).ok()
        ).toBeTruthy();
      await request.delete(`/api/agents?name=${encodeURIComponent(name)}`);
      if (entry) await request.delete(`/api/agents?name=${encodeURIComponent(keeper)}`);
    }
  };
}

async function openEditor(page: Page, name: string) {
  await page.goto(`/agents/${encodeURIComponent(name)}?tab=workspaces`);
  await expect(page.locator('[data-workspace-status]')).toContainText('Membership loaded');
}

test('real assignment editor returns to contextual work and offers a keyboard workspace choice', async ({
  page,
  request
}, testInfo) => {
  const f = await fixture(request);
  const [one, two] = f.workspaces;
  try {
    const before = await f.detail();
    await page.goto(`/agents?agent=${encodeURIComponent(f.name)}&q=Membership&view=list`);
    await page.locator('#stageNextStep').getByRole('link', { name: 'Add to a workspace' }).click();
    await expect(page).toHaveURL(/tab=workspaces/);
    await expect(page.locator('[data-workspace-status]')).toContainText('Membership loaded');
    await expect(page.locator('#agentWorkspacesTitle')).toBeFocused();
    const checkbox = page.locator(`[data-workspace-id="${one.id}"]`);
    await checkbox.check();
    await page.screenshot({
      path: testInfo.outputPath('agent-membership-edit.png'),
      fullPage: true
    });
    await page.locator('[data-workspace-save]').click();
    await expect(page.locator('[data-workspace-status]')).toContainText('membership saved');
    const after = await f.detail();
    expect(after.workspace_count).toBe(1);
    expect(after.workspaces[0].id).toBe(one.id);
    expect(after.version).toBe(before.version); // membership is not a settings edit
    await page.goBack();
    await expect(page).toHaveURL(/q=Membership/);
    await expect(page.locator('#stageName')).toHaveText(f.name);
    await expect(page.locator('#stageNextStep a')).toHaveAttribute(
      'href',
      `/workspaces/${one.folder_slug}?agent=${encodeURIComponent(f.name.toLowerCase())}`
    );
    await page.locator('#stageNextStep a').click();
    await expect(page).toHaveURL(new RegExp(`/workspaces/${one.folder_slug}\\?agent=`));
    await page.goBack();
    expect(
      (
        await request.post(`/api/workspaces/${two.id}/agents`, { data: { agent_name: f.name } })
      ).ok()
    ).toBeTruthy();
    // Simulate a BFCache return: it uses the same list owner, not per-agent polls.
    await page.evaluate(() =>
      window.dispatchEvent(new PageTransitionEvent('pageshow', { persisted: true }))
    );
    const summary = page.locator('#stageNextStep summary');
    await expect(summary).toHaveText('Choose a workspace');
    await summary.focus();
    await page.keyboard.press('Enter');
    await expect(page.locator('#stageNextStep a')).toHaveCount(2);
    await page.keyboard.press('Tab');
    await expect(page.locator('#stageNextStep a').first()).toBeFocused();
    await page.screenshot({
      path: testInfo.outputPath('agent-workspace-choice.png'),
      fullPage: true
    });
    await page
      .locator('#stageNextStep')
      .getByRole('link', { name: `Open ${two.name}` })
      .click();
    await expect(page).toHaveURL(new RegExp(`/workspaces/${two.folder_slug}\\?agent=`));
  } finally {
    await f.cleanup();
  }
});

test('entry membership is locked while ordinary membership can be removed', async ({
  page,
  request
}) => {
  const f = await fixture(request, true);
  const [one, two] = f.workspaces;
  try {
    await request.post(`/api/workspaces/${two.id}/agents`, { data: { agent_name: f.name } });
    await openEditor(page, f.name);
    await expect(page.locator(`[data-workspace-id="${one.id}"]`)).toBeChecked();
    await expect(page.locator(`[data-workspace-id="${one.id}"]`)).toBeDisabled();
    await expect(page.locator('#agentWorkspacesSection')).toContainText(
      'choose another entry agent'
    );
    await page.locator(`[data-workspace-id="${two.id}"]`).uncheck();
    await page.locator('[data-workspace-save]').click();
    await expect(page.locator('[data-workspace-status]')).toContainText('membership saved');
    expect((await f.detail()).workspaces.map((ws: any) => ws.id)).toEqual([one.id]);
  } finally {
    await f.cleanup();
  }
});

test('failed list read is not empty membership and recovers through Reload', async ({
  page,
  request
}) => {
  const f = await fixture(request);
  let fail = true;
  try {
    await page.route('**/api/workspaces', route =>
      fail
        ? route.fulfill({ status: 503, json: { error: 'Injected read failure' } })
        : route.continue()
    );
    await page.goto(`/agents/${encodeURIComponent(f.name)}?tab=workspaces`);
    await expect(page.locator('[data-workspace-status]')).toContainText('could not be loaded');
    await expect(page.locator('[data-workspace-save]')).toBeDisabled();
    await expect(page.locator('#agentWorkspacesSection')).not.toContainText('No workspaces yet');
    fail = false;
    await page.locator('[data-workspace-reload]').click();
    await expect(page.locator('[data-workspace-status]')).toContainText('Membership loaded');
    await expect(page.locator(`[data-workspace-id="${f.workspaces[0].id}"]`)).toBeEnabled();
  } finally {
    await f.cleanup();
  }
});

test('unconfirmed save retains choices, blocks repeats, and reloads a partial write before retry', async ({
  page,
  request
}) => {
  const f = await fixture(request);
  const [one, two] = f.workspaces;
  let puts = 0;
  try {
    await openEditor(page, f.name);
    await page.locator(`[data-workspace-id="${one.id}"]`).check();
    await page.locator(`[data-workspace-id="${two.id}"]`).check();
    await page.route(`**/api/agents/${encodeURIComponent(f.name)}/workspaces`, async route => {
      puts++;
      if (puts === 1) {
        // Fault injection after one real server mutation: no rollback fiction.
        await request.post(`/api/workspaces/${one.id}/agents`, { data: { agent_name: f.name } });
        await route.fulfill({ status: 500, json: { error: 'Injected partial save' } });
      } else await route.continue();
    });
    await page.locator('[data-workspace-save]').click();
    await expect(page.locator('[data-workspace-status]')).toContainText('Save was not confirmed');
    await expect(page.locator('[data-workspace-save]')).toBeDisabled();
    await expect(page.locator(`[data-workspace-id="${two.id}"]`)).toBeChecked();
    expect(puts).toBe(1);
    await page.locator('[data-workspace-reload]').click();
    await expect(page.locator('[data-workspace-status]')).toContainText(
      'Review your unsaved choices'
    );
    await page.locator('[data-workspace-save]').click();
    await expect(page.locator('[data-workspace-status]')).toContainText('membership saved');
    expect((await f.detail()).workspace_count).toBe(2);
    expect(puts).toBe(2);
  } finally {
    await f.cleanup();
  }
});

test('freshness check refuses to overwrite membership changed in another editor', async ({
  page,
  request
}) => {
  const f = await fixture(request);
  const [one, two] = f.workspaces;
  let puts = 0;
  try {
    await openEditor(page, f.name);
    await page.locator(`[data-workspace-id="${one.id}"]`).check();
    await request.post(`/api/workspaces/${two.id}/agents`, { data: { agent_name: f.name } });
    page.on('request', r => {
      if (r.method() === 'PUT' && r.url().endsWith('/workspaces')) puts++;
    });
    await page.locator('[data-workspace-save]').click();
    await expect(page.locator('[data-workspace-status]')).toContainText(
      'Membership changed elsewhere'
    );
    expect(puts).toBe(0);
    await page.locator('[data-workspace-reload]').click();
    await expect(page.locator(`[data-workspace-id="${two.id}"]`)).toBeChecked();
    await expect(page.locator(`[data-workspace-id="${one.id}"]`)).toBeChecked();
    // Complete the explicitly reviewed rebase so leaving does not discard a draft.
    await page.locator('[data-workspace-save]').click();
    await expect(page.locator('[data-workspace-status]')).toContainText('membership saved');
  } finally {
    await f.cleanup();
  }
});

test('workspace-owned definitions expose destinations but never an assignment editor', async ({
  page,
  request
}) => {
  const f = await fixture(request);
  try {
    await page.route(`**${f.detailURL}`, async route => {
      const response = await route.fetch();
      const body = await response.json();
      await route.fulfill({
        json: { ...body, origin: { source: 'workspace', workspace_name: 'Owner' } }
      });
    });
    await page.goto(`/agents/${encodeURIComponent(f.name)}?tab=workspaces`);
    await expect(page.locator('[data-workspace-status]')).toContainText('read-only here');
    await expect(page.locator(`[data-workspace-id="${f.workspaces[0].id}"]`)).toBeDisabled();
    await expect(page.locator('[data-workspace-save]')).toBeDisabled();
  } finally {
    await f.cleanup();
  }
});

for (const theme of ['light', 'dark']) {
  test(`full-page workspace editor has keyboard focus and narrow contrast (${theme})`, async ({
    page,
    request
  }, testInfo) => {
    const f = await fixture(request);
    try {
      await page.addInitScript(value => localStorage.setItem('ori-theme', value), theme);
      await page.setViewportSize({ width: 390, height: 844 });
      await openEditor(page, f.name);
      await expect(page.locator('#contentGrid')).toHaveCSS('opacity', '1');
      const checkbox = page.locator(`[data-workspace-id="${f.workspaces[0].id}"]`);
      await checkbox.focus();
      await expect(checkbox).toHaveCSS('outline-style', 'solid');
      await page.keyboard.press('Space');
      await expect(checkbox).toBeChecked();
      await page.keyboard.press('Space'); // restore saved selection before leaving
      await page.addScriptTag({ url: 'https://cdn.jsdelivr.net/npm/axe-core@4.10.3/axe.min.js' });
      const scan = await page.evaluate(async () => {
        const result = await (window as any).axe.run('#agentWorkspacesSection', {
          runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa'] }
        });
        return {
          violations: result.violations,
          contrast:
            result.passes.find((rule: any) => rule.id === 'color-contrast')?.nodes.length || 0
        };
      });
      expect(scan.violations).toEqual([]);
      expect(scan.contrast).toBeGreaterThan(0);
      const box = await page.locator('#agentWorkspacesSection').boundingBox();
      expect(box!.x + box!.width).toBeLessThanOrEqual(391);
      await page.screenshot({
        path: testInfo.outputPath(`agent-membership-${theme}.png`),
        fullPage: true
      });
      await checkNextActionReflow(page, '#agentWorkspacesSection');
      await page.screenshot({
        path: testInfo.outputPath(`agent-membership-zoom-${theme}.png`),
        fullPage: true
      });
    } finally {
      await f.cleanup();
    }
  });
}
