import { expect, type APIRequestContext, type Page } from '@playwright/test';
import { mkdtempSync, mkdirSync, realpathSync, writeFileSync, utimesSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

// Only for the isolated, explicitly enabled scripted-model demo. These helpers
// use real APIs and synthetic folders; they never run against a user's HOME.
export async function prepareFolderCreationRuntime(request: APIRequestContext) {
  const model = await request.post('/api/settings/system-model', {
    data: { provider: 'lmstudio', model: 'ori-synthetic-model' }
  });
  expect(model.ok(), await model.text()).toBeTruthy();
  let state = (await (await request.get('/api/personal-assistant')).json()).personal_assistant;
  if (state.state === 'needs_hire') {
    const hired = await request.post('/api/personal-assistant/hire', {
      data: {
        request_id: 'synthetic-creation-hire-' + Date.now(),
        if_version: state.state_version || 0,
        display_name: 'Synthetic Assistant',
        mandate: 'Help review synthetic folders.',
        focus_areas: ['plan_my_day']
      }
    });
    expect(hired.ok(), await hired.text()).toBeTruthy();
    state = (await (await request.get('/api/personal-assistant')).json()).personal_assistant;
  }
  if (state.state === 'needs_hq') {
    const hq = await request.post('/api/personal-assistant/hq', {
      data: {
        request_id: 'synthetic-creation-hq-' + Date.now(),
        if_version: state.state_version,
        name: 'Synthetic HQ',
        timezone: 'UTC'
      }
    });
    expect(hq.ok(), await hq.text()).toBeTruthy();
  }
  const availability = await request.get('/api/workspaces/build-sessions/availability');
  expect((await availability.json()).available).toBeTruthy();
}

export async function currentBuild(request: APIRequestContext) {
  return (
    await (
      await request.post('/api/workspaces/build-sessions', {
        data: { entry_point: 'home_cockpit_create' }
      })
    ).json()
  ).session;
}

export async function abandonOpenBuild(request: APIRequestContext) {
  const old = await currentBuild(request);
  if (old?.status === 'open') {
    const abandoned = await request.post(`/api/workspaces/build-sessions/${old.id}/abandon`, {
      data: { version: old.version }
    });
    expect(abandoned.ok(), await abandoned.text()).toBeTruthy();
  }
}

export async function attachFirst(
  page: Page,
  request: APIRequestContext,
  root: string,
  unsent = '',
  beforeAttach?: () => Promise<void>
) {
  await abandonOpenBuild(request);
  await request.post('/api/onboarding/skip');
  await page.goto('/');
  await page.locator('#cockpitCreateWorkspaceBtn').click();
  const composer = page.locator('#workspaceBuildComposerInput');
  await expect(composer).toBeEnabled();
  if (unsent) await composer.fill(unsent);
  if (beforeAttach) await beforeAttach();
  await page
    .locator('#workspaceBuildPane')
    .getByRole('button', { name: 'Add folder', exact: true })
    .click();
  await expect(page.locator('#workspaceBuildPane')).toContainText(
    'shared with your configured model'
  );
  await page.getByPlaceholder('~/Projects/my-app').fill(root);
  await page.getByRole('button', { name: 'Use this path', exact: true }).click();
  return composer;
}

export async function resources(request: APIRequestContext) {
  const workspaces = (await (await request.get('/api/workspaces')).json()).folders;
  const permissions = await Promise.all(
    workspaces.map(
      async (w: { id: string; directory_references: unknown; mcp_bindings: unknown }) => {
        const base = `/api/workspaces/${w.id}/file-janitor`;
        const status = (await (await request.get(base)).json()).status;
        const { checked_at: _checkedAt, ...readiness } = status.readiness;
        return {
          id: w.id,
          references: w.directory_references,
          bindings: w.mcp_bindings,
          settings: status.settings,
          readiness,
          batches: await (await request.get(`${base}/batches`)).json(),
          history: await (await request.get(`${base}/history`)).json()
        };
      }
    )
  );
  return {
    workspaces: workspaces.map((w: { id: string }) => w.id).sort(),
    permissions: permissions.sort((a, b) => a.id.localeCompare(b.id)),
    plugins: await (await request.get('/api/plugins')).json(),
    agents: (await (await request.get('/api/agents')).json()).agents.sort(
      (a: { name: string }, b: { name: string }) => a.name.localeCompare(b.name)
    )
  };
}

export function inbox(name = '') {
  let root = realpathSync(mkdtempSync(join(tmpdir(), 'ori-janitor-creation-')));
  if (name) {
    root = join(root, name);
    mkdirSync(root, { mode: 0o700 });
  }
  const file = join(root, 'invoice.pdf');
  writeFileSync(file, 'synthetic source bytes', { mode: 0o600 });
  const old = new Date(Date.now() - 3600000);
  utimesSync(file, old, old);
  return root;
}
