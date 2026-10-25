import { test, expect, type APIRequestContext } from '@playwright/test';
import { existsSync, mkdirSync, writeFileSync, readFileSync } from 'node:fs';
import { join } from 'node:path';
import { chooseAttach } from './helpers/assistant-menus';
import { verifySavedFirstLook } from './helpers/folder-saved-report';

// Real host, no fixtures or preinstalled packages. Installation requires the
// explicit disposable ORI_READINESS_INSTALL=1 opt-in. The default run does not
// execute a model. Only a paired, explicitly authorized live provider/model
// extends the final canonical review through real Settings and a saved report.
const sandbox = process.env.ORI_READINESS_SANDBOX || '';
const evidence = process.env.ORI_READINESS_EVIDENCE || '';
const liveProvider = process.env.ORI_READINESS_LIVE_PROVIDER || '';
const liveModel = process.env.ORI_READINESS_LIVE_MODEL || '';
const audit = process.env.ORI_READINESS_CALL_AUDIT || '';
const reportTimeout = Number(process.env.ORI_READINESS_REPORT_TIMEOUT_MS || 180_000);
if (Boolean(liveProvider) !== Boolean(liveModel))
  throw new Error('Supply both live provider and model');
if (liveProvider && process.env.ORI_READINESS_INSTALL !== '1')
  throw new Error('Live acceptance requires the explicit fresh-install opt-in');
if (!Number.isSafeInteger(reportTimeout) || reportTimeout < 1000 || reportTimeout > 900_000)
  throw new Error('ORI_READINESS_REPORT_TIMEOUT_MS must be 1000–900000');
const modelCalls = () =>
  audit && existsSync(audit)
    ? readFileSync(audit, 'utf8')
        .trim()
        .split('\n')
        .filter(Boolean)
        .map(line => JSON.parse(line))
        .filter(event => event.allowed).length
    : 0;
let initialModelCalls = 0;
test.skip(!sandbox, 'requires an explicitly isolated ORI_READINESS_SANDBOX');
test.describe.configure({ mode: 'serial' });
test.setTimeout(90_000);
const source = '<REAPER_PROJECT 0.1 "7.0"\n>\n';

async function json(request: APIRequestContext, path: string, data?: object) {
  const response = data ? await request.post(path, { data }) : await request.get(path);
  expect(response.ok(), await response.text()).toBeTruthy();
  return response.json();
}

test.beforeAll(async ({ request }) => {
  initialModelCalls = modelCalls();
  await json(request, '/api/onboarding/skip', {});
  expect((await json(request, '/api/plugins')).plugins).toEqual([]);
  expect((await json(request, '/api/settings/system-model')).configured).toBe(false);
  await json(request, '/api/settings/workspace-root', { workspace_root: '' });
  let assistant = (await json(request, '/api/personal-assistant')).personal_assistant;
  if (assistant.state === 'needs_hire') {
    await json(request, '/api/personal-assistant/hire', {
      request_id: 'readiness-hire',
      if_version: assistant.state_version || 0,
      display_name: 'Atlas',
      mandate: 'Help me plan.',
      focus_areas: ['plan_my_day']
    });
    assistant = (await json(request, '/api/personal-assistant')).personal_assistant;
  }
  if (assistant.state === 'needs_hq')
    await json(request, '/api/personal-assistant/hq', {
      request_id: 'readiness-hq',
      if_version: assistant.state_version,
      name: 'My HQ',
      timezone: 'UTC'
    });
  mkdirSync(join(sandbox, 'Desktop'), { recursive: true, mode: 0o750 });
  writeFileSync(join(sandbox, 'Desktop', 'Session.rpp'), source, { mode: 0o600 });
  if (evidence) mkdirSync(evidence, { recursive: true, mode: 0o750 });
});

for (const route of ['/?panel=today', '/settings']) {
  test(`real single-project attachment immediately shows requirements without a model on ${route}`, async ({
    page,
    request
  }) => {
    const forbidden: string[] = [];
    const before = await json(request, '/api/workspaces');
    page.on('request', request => {
      const path = new URL(request.url()).pathname;
      if (
        /\/home-assistant\/(ask|route)$|\/offers\/[^/]+\/(setup|decide|prerequisites)$|\/tasks.*execute/.test(
          path
        ) ||
        (request.method() !== 'GET' && /\/plugins\/.*(install|enable|update)/.test(path))
      )
        forbidden.push(`${request.method()} ${path}`);
    });
    if (route.startsWith('/settings')) await page.setViewportSize({ width: 390, height: 844 });
    await page.goto(route);
    await page.waitForFunction(() => Boolean((window as any).PersonalAssistantPanel?.open));
    await page.evaluate(() =>
      (window as any).PersonalAssistantPanel.open(
        document.getElementById('personalAssistantLauncher')
      )
    );
    const draft = '  Keep 🎼 this exact\ndraft  ';
    await page.locator('#personalAssistantInput').fill(draft);
    await chooseAttach(page, '#personalAssistantFolderChip:enabled');
    await page
      .locator('#personalAssistantFolderChoices')
      .getByRole('button', { name: 'Desktop', exact: true })
      .click();
    const requirements = page.getByRole('region', { name: 'Setup requirements' });
    await expect(requirements).toBeVisible({ timeout: 60_000 });
    await expect(requirements).toContainText('REAPER');
    await expect(requirements).toContainText('Music Project Management');
    await expect(requirements).toContainText('Ori plugins are separate');
    await expect(
      requirements.getByRole('button', { name: 'Install REAPER', exact: true })
    ).toBeVisible();
    await expect(
      requirements.getByRole('button', { name: 'Install Music Project Management', exact: true })
    ).toBeVisible();
    await expect(page.getByRole('link', { name: /Guided Setup/ })).toHaveCount(0);
    await expect(page.locator('#personalAssistantInput')).toHaveValue(draft);
    await page.evaluate(() => document.fonts.ready);
    // Findings and the full recommendation now lead the review. Scroll to the
    // single decision area to check that its actionable package rows are usable.
    await requirements.scrollIntoViewIfNeeded();
    const initial = await requirements.evaluate(section => {
      const viewport = section
        .closest('.personal-assistant-panel__scroll')!
        .getBoundingClientRect();
      return Array.from(
        section.querySelectorAll(
          '.folder-requirements__row > strong, .folder-requirements__row > .folder-requirements__status'
        )
      ).map(node => {
        const rect = node.getBoundingClientRect();
        return {
          text: node.textContent,
          top: rect.top,
          bottom: rect.bottom,
          visible: rect.top >= viewport.top - 1 && rect.bottom <= viewport.bottom + 1
        };
      });
    });
    expect(
      initial.every(row => row.visible),
      JSON.stringify(initial)
    ).toBe(true);
    if (evidence)
      await page.screenshot({
        path: join(
          evidence,
          `${route.startsWith('/settings') ? 'settings' : 'home'}-immediate-requirements.png`
        )
      });
    expect(forbidden).toEqual([]);
    expect((await json(request, '/api/plugins')).plugins).toEqual([]);
    expect(await json(request, '/api/workspaces')).toEqual(before);
    expect(readFileSync(join(sandbox, 'Desktop', 'Session.rpp'), 'utf8')).toBe(source);
    // Each Playwright page is a separate owner. Explicitly close this review
    // so the next route does not steal another conversation's pending card.
    const closed = page.waitForResponse(r =>
      new URL(r.url()).pathname.endsWith('/folder-context/review/close')
    );
    await page
      .locator('#personalAssistantFolderOffer')
      .getByRole('button', { name: 'Keep chatting', exact: true })
      .click();
    expect((await closed).ok()).toBe(true);
  });
}

test(
  liveProvider
    ? 'real fresh inline setup continues through model Settings to a saved live report'
    : 'real fresh cards independently review and install both plugins inline without a model',
  async ({ page, request }) => {
    test.skip(
      process.env.ORI_READINESS_INSTALL !== '1',
      'explicit disposable installation opt-in required'
    );
    test.setTimeout(240_000 + (liveProvider ? reportTimeout : 0));
    const before = await json(request, '/api/workspaces');
    // Auto-detected CLI entries are returned from an unordered registry. Compare
    // the complete records in stable order; detection is not execution consent.
    const agents = async () =>
      (await json(request, '/api/agents')).agents.sort((a: any, b: any) =>
        `${a.source}:${a.name}`.localeCompare(`${b.source}:${b.name}`)
      );
    const agentsBefore = await agents();
    expect((await json(request, '/api/plugins')).plugins).toEqual([]);
    const forbidden: string[] = [],
      mutations: any[] = [];
    page.on('request', r => {
      const path = new URL(r.url()).pathname;
      if (
        /\/home-assistant\/(ask|route)$|\/offers\/[^/]+\/(setup|decide)$|\/tasks.*execute/.test(
          path
        )
      )
        forbidden.push(path);
      if (r.method() === 'POST' && path.endsWith('/prerequisites') && r.postDataJSON()?.confirm)
        mutations.push(r.postDataJSON());
    });
    await page.goto('/?panel=today');
    await page.waitForFunction(() => Boolean((window as any).PersonalAssistantPanel?.open));
    await page.evaluate(() =>
      (window as any).PersonalAssistantPanel.open(
        document.getElementById('personalAssistantLauncher')
      )
    );
    const draft = '  Keep this 🎼 draft\nwhile installing  ';
    await page.locator('#personalAssistantInput').fill(draft);
    await chooseAttach(page, '#personalAssistantFolderChip:enabled');
    await page
      .locator('#personalAssistantFolderChoices')
      .getByRole('button', { name: 'Desktop', exact: true })
      .click();
    const requirements = page.getByRole('region', { name: 'Setup requirements' });
    await expect(requirements).toBeVisible({ timeout: 60_000 });
    const provider = requirements.locator('[data-requirement-kind="provider"]');
    const integration = requirements.locator('[data-requirement-kind="integration"]');
    // Either card can be used first: the Home plugin does not need an installed
    // project blueprint in order to disclose its host-reviewed release.
    await provider
      .getByRole('button', { name: 'Install Music Project Management', exact: true })
      .click();
    await expect(
      provider.getByRole('region', { name: 'Review Music Project Management' })
    ).toBeVisible({ timeout: 60_000 });
    await expect(provider).toContainText('johnjallday/music-project-management');
    await expect(provider).toContainText('No workspace, Home, agent or model task');
    expect((await json(request, '/api/plugins')).plugins).toEqual([]);
    await provider.getByRole('button', { name: 'Cancel', exact: true }).click();
    await expect(
      provider.getByRole('button', { name: 'Install Music Project Management', exact: true })
    ).toBeVisible();
    expect(mutations).toHaveLength(0);
    await provider
      .getByRole('button', { name: 'Install Music Project Management', exact: true })
      .click();
    const confirmProvider = provider.getByRole('button', {
      name: 'Confirm installation',
      exact: true
    });
    await expect(confirmProvider).toBeEnabled({ timeout: 60_000 });
    const offerId = await page
      .locator('#personalAssistantFolderOffer')
      .evaluate(() => (window as any).PersonalAssistantFolderContext.current().offerId);
    const refused = await request.post(
      `/api/personal-assistant/folder-digest/offers/${encodeURIComponent(offerId)}/prerequisites`,
      {
        data: {
          kind: 'provider',
          confirm: true,
          request_id: 'refused-stale-provider',
          digest: '0'.repeat(64),
          reviewed_version: '0.4.0'
        }
      }
    );
    expect(refused.status()).toBe(409);
    expect((await json(request, '/api/plugins')).plugins).toEqual([]);
    if (evidence) await page.screenshot({ path: join(evidence, 'inline-provider-consent.png') });
    await confirmProvider.click();
    await expect(provider).toContainText('Ready · No download', { timeout: 120_000 });
    let plugins = (await json(request, '/api/plugins')).plugins;
    expect(plugins).toHaveLength(1);
    expect(plugins[0].name).toBe('music-project-management');
    expect(plugins[0].enabled).toBe(true);
    await integration.getByRole('button', { name: 'Install REAPER', exact: true }).click();
    await expect(
      integration.getByRole('button', { name: 'Confirm installation', exact: true })
    ).toBeEnabled({ timeout: 60_000 });
    await expect(integration).toContainText('johnjallday/reaper-plugin');
    await expect(integration).toContainText('No — enable separately afterward');
    if (evidence) await page.screenshot({ path: join(evidence, 'inline-integration-consent.png') });
    await integration.getByRole('button', { name: 'Confirm installation', exact: true }).click();
    await expect(
      integration.getByRole('button', { name: 'Enable REAPER', exact: true })
    ).toBeVisible({ timeout: 120_000 });
    plugins = (await json(request, '/api/plugins')).plugins;
    expect(plugins.find((p: any) => p.name === 'reaper-plugin').enabled).toBe(false);
    await integration.getByRole('button', { name: 'Enable REAPER', exact: true }).click();
    await integration
      .getByRole('button', { name: 'Confirm enablement', exact: true })
      .click({ timeout: 60_000 });
    await expect(requirements).toContainText('Nothing to download', { timeout: 120_000 });
    await expect(
      page.getByRole('button', { name: 'Configure project model', exact: true })
    ).toBeVisible();
    plugins = (await json(request, '/api/plugins')).plugins;
    expect(plugins.map((p: any) => p.name).sort()).toEqual([
      'music-project-management',
      'reaper-plugin'
    ]);
    expect(plugins.every((p: any) => p.enabled && p.version)).toBe(true);
    console.log(
      'Real reviewed installed packages:',
      JSON.stringify(
        plugins.map((p: any) => ({ name: p.name, version: p.version, source: p.source }))
      )
    );
    expect(mutations.map(body => [body.kind, body.action || 'install'])).toEqual([
      ['provider', 'install'],
      ['integration', 'install'],
      ['integration', 'enable']
    ]);
    expect(forbidden).toEqual([]);
    expect(page.context().pages()).toHaveLength(1);
    expect(await agents()).toEqual(agentsBefore);
    await expect(page.locator('#personalAssistantInput')).toHaveValue(draft);
    if (evidence)
      await page.screenshot({ path: join(evidence, 'inline-plugins-ready-model-missing.png') });
    expect((await json(request, '/api/settings/system-model')).configured).toBe(false);
    expect(await json(request, '/api/workspaces')).toEqual(before);
    expect(readFileSync(join(sandbox, 'Desktop', 'Session.rpp'), 'utf8')).toBe(source);
    if (!liveProvider) return;

    // Continue this same unaccepted canonical review; do not close/recreate it,
    // preinstall packages, replace its held selection, or supply a model response.
    const card = page.locator('#personalAssistantFolderOffer');
    const original = await page.evaluate(() => (window as any).PersonalAssistantFolder.current());
    expect(original.id).toBe(offerId);
    const history = await json(
      request,
      `/api/home-assistant/conversations/${original.conversation_id}`
    );
    const brief =
      'Read Session.rpp once, using the directory ID returned by workspace_directories. Report its actual contents and what cannot be inferred. Do not edit files, control REAPER, or repeat a read whose result you already have.';
    await card.getByLabel('What should it do with it?').fill(brief);
    const popup = page.waitForEvent('popup');
    await card.getByRole('button', { name: 'Configure project model', exact: true }).click();
    const settings = await popup;
    await expect(settings).toHaveURL(/\/settings#system-model$/);
    await settings.locator('#systemModelProvider').selectOption(liveProvider);
    await settings.locator('#systemModelModel').selectOption(liveModel);
    await settings.locator('#saveSystemModelBtn').click();
    await expect(settings.locator('#systemModelStatusText')).toHaveText('System Model Configured');
    await settings.close();
    await page.bringToFront();
    await expect(page.locator('#personalAssistantInput')).toHaveValue(draft);
    await expect(card.getByLabel('What should it do with it?')).toHaveValue(brief);
    const rechecked = page.waitForResponse(
      response =>
        response.url().endsWith('/folder-context/review') &&
        response.request().postDataJSON()?.recheck === true
    );
    await card.getByRole('button', { name: 'Check again', exact: true }).click();
    expect((await rechecked).ok()).toBe(true);
    const model = card.getByRole('region', { name: 'Project agent model' });
    await expect(model).toContainText('Configured');
    await expect(model).toContainText(liveModel);
    const current = await page.evaluate(() => (window as any).PersonalAssistantFolder.current());
    expect(current.id).toBe(original.id);
    expect(current.conversation_id).toBe(original.conversation_id);
    const after = await json(
      request,
      `/api/home-assistant/conversations/${current.conversation_id}`
    );
    expect(after.messages.length).toBe(history.messages.length);
    expect(after.folder_context.revision).toBe(history.folder_context.revision);
    await expect(page.locator('#personalAssistantInput')).toHaveValue(draft);
    await expect(card.getByLabel('What should it do with it?')).toHaveValue(brief);
    expect(forbidden).toEqual([]);
    expect(await json(request, '/api/workspaces')).toEqual(before);
    // Existing Settings owns the Ask Ori system-assistant bootstrap. Allow
    // exactly that definition, not a project hire or any staffing/creation.
    const afterModel = await agents();
    const systemAssistant = afterModel.filter((agent: any) => agent.name === 'Ask Ori');
    expect(systemAssistant).toHaveLength(1);
    expect(systemAssistant[0].source).toBe('user');
    const identity = (agent: any) => [agent.id, agent.source, agent.name];
    expect(afterModel.filter((agent: any) => agent.name !== 'Ask Ori').map(identity)).toEqual(
      agentsBefore.map(identity)
    );
    if (audit) expect(modelCalls()).toBe(initialModelCalls);
    if (evidence)
      await page.screenshot({ path: join(evidence, 'inline-live-configured-review.png') });

    // The sole whole-setup confirmation now grants the reviewed creation and
    // first-look effects, independently of the earlier package confirmations.
    const accepted = page.waitForResponse(response =>
      /\/offers\/[^/]+\/setup$/.test(response.url())
    );
    await card.locator('[data-folder-action="setup"]').click();
    expect((await accepted).ok()).toBe(true);
    await expect
      .poll(
        async () =>
          (await page.evaluate(() => (window as any).PersonalAssistantFolder.current()))?.status,
        { timeout: 60_000 }
      )
      .toBe('resolved');
    await expect(card.locator('#personalAssistantFolderReceipt li').first()).toBeVisible();
    const report = card.getByRole('link', { name: 'Open the full report', exact: true });
    await expect(report).toBeVisible({ timeout: reportTimeout });
    const saved = await json(
      request,
      `/api/personal-assistant/folder-digest?offer_id=${encodeURIComponent(offerId)}`
    );
    const offer = saved.folder_digest.offer;
    expect(offer.first_task.state).toBe('finished');
    if (audit) expect(modelCalls()).toBeGreaterThan(initialModelCalls);
    await expect(page.locator('#personalAssistantInput')).toHaveValue(draft);
    expect(readFileSync(join(sandbox, 'Desktop', 'Session.rpp'), 'utf8')).toBe(source);
    expect(mutations).toHaveLength(3);
    expect(forbidden.filter(path => /\/setup$/.test(path))).toHaveLength(1);
    if (evidence) {
      await page.screenshot({ path: join(evidence, 'inline-live-finished-receipt.png') });
      writeFileSync(
        join(evidence, 'inline-live-receipt.json'),
        JSON.stringify(
          {
            provider: liveProvider,
            model: liveModel,
            offer_id: offerId,
            conversation_id: original.conversation_id,
            workspace_id: offer.outcome.workspace_id,
            task_id: offer.first_task.task_id,
            report_route: offer.first_task.report_route,
            plugin_versions: plugins.map((plugin: any) => ({
              name: plugin.name,
              version: plugin.version
            })),
            audited_model_calls: audit ? modelCalls() - initialModelCalls : undefined
          },
          null,
          2
        ) + '\n',
        { mode: 0o600 }
      );
    }
    await verifySavedFirstLook(page, request, sandbox, offerId, offer.outcome.route, {
      subject: 'Desktop',
      folder: 'Desktop',
      file: 'Session.rpp'
    });
    if (await page.locator('#personalAssistantPanel').isVisible())
      await page.locator('#personalAssistantClose').click();
    if (evidence) await page.screenshot({ path: join(evidence, 'inline-live-saved-report.png') });
  }
);
