import { test, expect } from '@playwright/test';
import { mkdirSync, realpathSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, relative } from 'node:path';
import { installLocalCdn } from './helpers/offline-cdn';

// Opt-in real setup proof. Start scripts/reaper-demo.sh with current clean
// candidates, both plugins and with-keys; configure the Claude API system model
// with scripts/folder-agent-demo-setup.sh. Never point this at personal state.
const sandbox = process.env.ORI_VISUAL_SETUP_SANDBOX || '';
const evidence =
  process.env.ORI_VISUAL_SETUP_EVIDENCE || 'tasks/evidence-visual-setup-card/group-1';
test.skip(!sandbox, 'requires an isolated ORI_VISUAL_SETUP_SANDBOX');

test('the suggested faces on the real plan are the agents hired by Set it up', async ({
  page,
  request
}) => {
  test.setTimeout(240_000);
  const root = realpathSync(sandbox);
  expect(relative(realpathSync(tmpdir()), root)).toMatch(/^ori-visual-setup-card\.[^/]+$/);
  const folder = join(root, 'Albums');
  for (let i = 1; i <= 6; i++) {
    const song = join(folder, `Song ${i}`);
    mkdirSync(song, { recursive: true, mode: 0o750 });
    writeFileSync(join(song, `Song ${i}.rpp`), '<REAPER_PROJECT 0.1 "7.0"\n>\n', { mode: 0o600 });
  }
  mkdirSync(evidence, { recursive: true, mode: 0o750 });
  await installLocalCdn(page);
  await page.setViewportSize({ width: 1440, height: 1000 });
  let workspaceID = process.env.ORI_VISUAL_SETUP_WORKSPACE || '';
  if (!workspaceID) {
    await page.goto('/?panel=today&folder=show');
    await page.locator('.folder-path-entry__input:visible').first().fill(folder);
    await page.getByRole('button', { name: 'Use this path', exact: true }).first().click();
    const hire = page.locator('[data-folder-agent-card="choose"]:visible');
    await expect(hire).toBeVisible();
    const started = page.waitForResponse(
      r => r.url().endsWith('/folder-agent/start') && r.request().method() === 'POST'
    );
    await hire.getByRole('button', { name: 'Hire & build', exact: true }).click();
    const response = await started;
    expect(response.ok(), await response.text()).toBe(true);
    workspaceID = (await response.json()).folder_agent.workspace_id;
  }
  const read = async () =>
    (await (await request.get(`/api/personal-assistant/folder-agent/${workspaceID}`)).json())
      .folder_agent;
  await expect.poll(async () => (await read()).state, { timeout: 180_000 }).toBe('reported');
  let view = await read();
  if (view.premise?.state === 'asked') {
    const confirmed = await request.post(
      `/api/personal-assistant/folder-agent/${workspaceID}/premise`,
      { data: { answer: 'confirmed' } }
    );
    expect(confirmed.ok(), await confirmed.text()).toBe(true);
    view = await read();
  }
  if (process.env.ORI_VISUAL_SETUP_PICK === '1') {
    const configured = await request.post('/api/settings/system-model', {
      data: { provider: 'claude', model: 'claude-sonnet-4-6' }
    });
    expect(configured.ok()).toBe(true);
  }
  // Use the real workspace's conversation card, not a renderer mounted in a
  // fixture container. The model may leave Home unticked; choose it explicitly.
  await page.goto(view.route);
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
  await expect(card.locator('.folder-agent-card__hire')).toHaveCount(2);
  const portraits = card.locator('.folder-agent-card__hire-face [data-aa-character]');
  await expect(portraits).toHaveCount(2);
  if (process.env.ORI_VISUAL_SETUP_PICK === '1') {
    const face = card.getByRole('button', {
      name: 'Change face for Portfolio Manager',
      exact: true
    });
    await face.focus();
    await page.keyboard.press('Shift+Tab');
    await page.keyboard.press('Tab');
    await expect(face).toBeFocused();
    await page.keyboard.press('Enter');
    await expect(
      page.getByRole('dialog', { name: 'Choose a character', exact: true })
    ).toBeVisible();
    await page.locator('#charPickerCancel').click();
    await expect(face).toBeFocused();
    await page.keyboard.press('Space');
    await expect(
      page.getByRole('dialog', { name: 'Choose a character', exact: true })
    ).toBeVisible();
    const taken = await portraits.evaluateAll(nodes =>
      nodes.map(node => node.getAttribute('data-aa-character'))
    );
    await page
      .locator(`.char-card${taken.map(id => `:not([data-char-id="${id}"])`).join('')}`)
      .first()
      .click();
    const picked = await page
      .locator('.char-card[aria-checked="true"]')
      .getAttribute('data-char-id');
    await page.locator('#charPickerConfirm').click();
    await expect(face).toBeFocused();
    await expect(face.locator('[data-aa-character]')).toHaveAttribute('data-aa-character', picked!);
    const homePick = card.locator('input[data-step-handle="music-production-home"]');
    await homePick.uncheck();
    await expect(card.locator('.folder-agent-card__hire')).toHaveCount(0);
    await homePick.check();
    await expect(
      card
        .getByRole('button', { name: 'Change face for Portfolio Manager', exact: true })
        .locator('[data-aa-character]')
    ).toHaveAttribute('data-aa-character', picked!);
  }
  let editedModel = '';
  if (process.env.ORI_VISUAL_SETUP_EDIT === '1') {
    const profileSnapshot = (data: any) =>
      (Array.isArray(data) ? data : data.agents)
        .map((agent: any) => ({
          name: agent.name,
          model: agent.model,
          appearance: agent.appearance
        }))
        .sort((a: any, b: any) => a.name.localeCompare(b.name));
    const profilesBefore = profileSnapshot(
      await (await request.get('/api/agents/dashboard/list')).json()
    );
    await card
      .getByRole('button', { name: 'Edit planned agent Portfolio Manager', exact: true })
      .click();
    const editor = card.getByRole('form', { name: 'Edit planned agent Portfolio Manager' });
    await expect(editor).toBeVisible();
    await editor.locator('[data-agent-create-field="name"]').fill('Albums Director');
    editedModel = 'claude-haiku-4-5';
    await expect(
      editor.locator(`[data-agent-create-field="model"] option[value="${editedModel}"]`)
    ).toHaveCount(1);
    await editor.locator('[data-agent-create-field="model"]').selectOption(editedModel);
    const reviewed = page.waitForResponse(
      r => r.url().endsWith(`/${workspaceID}/plan`) && r.request().method() === 'POST'
    );
    await editor.getByRole('button', { name: 'Save draft', exact: true }).click();
    const preview = await reviewed;
    expect(preview.ok(), await preview.text()).toBe(true);
    const pending = preview.request().postDataJSON();
    const editedPlan = (await preview.json()).plan;
    const manager = editedPlan.hires.find((hire: any) => hire.name === 'Albums Director');
    expect(pending.hire_edits[manager.key]).toEqual({
      name: 'Albums Director',
      provider: 'claude',
      model: editedModel
    });
    const old = await request.post(`/api/personal-assistant/folder-agent/${workspaceID}/plan`, {
      data: { selected: pending.selected }
    });
    expect(old.ok(), await old.text()).toBe(true);
    const stale = await request.post(`/api/personal-assistant/folder-agent/${workspaceID}/setup`, {
      data: {
        selected: pending.selected,
        hire_edits: pending.hire_edits,
        plan_digest: (await old.json()).plan.digest
      }
    });
    expect(stale.status()).toBe(409);
    expect((await stale.json()).plan_changed).toBe(true);
    expect(profileSnapshot(await (await request.get('/api/agents/dashboard/list')).json())).toEqual(
      profilesBefore
    );
    const homePick = card.locator('input[data-step-handle="music-production-home"]');
    await homePick.uncheck();
    await expect(card.locator('.folder-agent-card__hire')).toHaveCount(0);
    await homePick.check();
    await expect(
      card.getByRole('button', { name: 'Edit planned agent Albums Director', exact: true })
    ).toBeVisible();
    await expect(card.locator('.folder-agent-card__hire-row-model').first()).toHaveText(
      `${editedModel} (claude)`
    );
  }
  if (editedModel) {
    await card
      .getByRole('button', { name: 'Edit planned agent REAPER Assistant', exact: true })
      .click();
    const editor = card.getByRole('form', { name: 'Edit planned agent REAPER Assistant' });
    await editor.locator('[data-agent-create-field="name"]').fill('Session Guide');
    await editor.getByRole('radio', { name: /^Generated/ }).check();
    await editor.getByRole('button', { name: 'Save draft', exact: true }).click();
    await expect(
      card.getByRole('button', { name: 'Edit planned agent Session Guide', exact: true })
    ).toBeVisible();
  }
  const shown = await card
    .locator('.folder-agent-card__hire')
    .evaluateAll(rows =>
      Object.fromEntries(
        rows.map(row => [
          row.querySelector('.folder-agent-card__hire-name')!.textContent,
          row.querySelector('[data-aa-character]')?.getAttribute('data-aa-character') || ''
        ])
      )
    );
  expect(new Set(Object.values(shown)).size).toBe(2);
  await card.screenshot({ path: join(evidence, 'suggested-team.png') });
  const accepted = page.waitForResponse(
    r => r.url().endsWith(`/${workspaceID}/setup`) && r.request().method() === 'POST'
  );
  await card.locator('[data-folder-agent-action="set_up"]').click();
  const setupResponse = await accepted;
  expect(setupResponse.ok(), await setupResponse.text()).toBe(true);
  const confirmed = setupResponse.request().postDataJSON();
  const planned = (await setupResponse.json()).folder_agent.plan.hires;
  expect(confirmed.hire_faces).toEqual(
    Object.fromEntries(planned.map((hire: any) => [hire.key || hire.name, shown[hire.name]]))
  );
  if (editedModel) {
    const manager = planned.find((hire: any) => hire.name === 'Albums Director');
    expect(confirmed.hire_edits[manager.key]).toEqual({
      name: 'Albums Director',
      provider: 'claude',
      model: editedModel
    });
  }
  await expect.poll(async () => (await read()).setup?.status, { timeout: 120_000 }).toBe('done');
  view = await read();
  expect(view.setup.receipt.hired.sort()).toEqual(Object.keys(shown).sort());
  const records = await (await request.get('/api/agents/dashboard/list')).json();
  const agents = Array.isArray(records) ? records : records.agents;
  for (const [name, id] of Object.entries(shown)) {
    const appearance = agents.find((agent: any) => agent.name === name)?.appearance;
    if (id) expect(appearance).toMatchObject({ mode: 'character', character: { catalog_id: id } });
    else expect(appearance?.mode).toBe('generated');
  }
  if (editedModel) {
    const detail = await (await request.get('/api/agents/Albums%20Director')).json();
    expect(detail.model).toBe(editedModel);
    expect(detail.provider).toBe('claude');
    expect(
      agents.some((agent: any) => ['Portfolio Manager', 'REAPER Assistant'].includes(agent.name))
    ).toBe(false);
    const shared = await (await request.get('/api/agents/Session%20Guide')).json();
    expect(shared.model).toBe('claude-sonnet-4-6');
    expect(shared.provider).toBe('claude');
  }
  await page.goto('/agents');
  const closeDrawer = page.getByRole('button', { name: 'Close personal assistant', exact: true });
  if (await closeDrawer.isVisible()) await closeDrawer.click();
  for (const id of Object.values(shown).filter(Boolean))
    await expect(page.locator(`[data-aa-character="${id}"]`).first()).toBeVisible();
  await page.screenshot({ path: join(evidence, 'hired-agents.png') });
  writeFileSync(
    join(evidence, 'result.json'),
    JSON.stringify(
      {
        workspaceID,
        shown,
        hired: view.setup.receipt.hired,
        editedModel,
        modelSentence: view.model_sentence
      },
      null,
      2
    )
  );
});
