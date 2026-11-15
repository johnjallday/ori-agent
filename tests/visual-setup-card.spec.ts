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
  const shown = await card
    .locator('.folder-agent-card__hire')
    .evaluateAll(rows =>
      Object.fromEntries(
        rows.map(row => [
          row.querySelector('.folder-agent-card__hire-name')!.textContent,
          row.querySelector('[data-aa-character]')!.getAttribute('data-aa-character')
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
  expect(setupResponse.request().postDataJSON().hire_faces).toEqual(shown);
  await expect.poll(async () => (await read()).setup?.status, { timeout: 120_000 }).toBe('done');
  view = await read();
  expect(view.setup.receipt.hired.sort()).toEqual(Object.keys(shown).sort());
  const records = await (await request.get('/api/agents/dashboard/list')).json();
  const agents = Array.isArray(records) ? records : records.agents;
  for (const [name, id] of Object.entries(shown)) {
    expect(agents.find((agent: any) => agent.name === name)?.appearance).toMatchObject({
      mode: 'character',
      character: { catalog_id: id }
    });
  }
  await page.goto('/agents');
  for (const id of Object.values(shown))
    await expect(page.locator(`[data-aa-character="${id}"]`).first()).toBeVisible();
  await page.screenshot({ path: join(evidence, 'hired-agents.png') });
  writeFileSync(
    join(evidence, 'result.json'),
    JSON.stringify(
      { workspaceID, shown, hired: view.setup.receipt.hired, modelSentence: view.model_sentence },
      null,
      2
    )
  );
});
