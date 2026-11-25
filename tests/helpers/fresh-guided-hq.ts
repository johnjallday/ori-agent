import { expect, type APIRequestContext } from '@playwright/test';

export async function freshGuidedHQ(request: APIRequestContext) {
  async function post(url: string, data: unknown) {
    expect((await request.post(url, { data })).ok(), url).toBeTruthy();
  }
  await post('/api/onboarding/skip', {});
  const { default_workspace_root } = await (
    await request.get('/api/settings/workspace-root')
  ).json();
  await post('/api/settings/workspace-root', { workspace_root: default_workspace_root });
  const pa = async () =>
    (await (await request.get('/api/personal-assistant')).json()).personal_assistant;
  const before = await pa();
  expect(before.state, 'requires a fresh isolated server').toBe('needs_hire');
  await post('/api/personal-assistant/hire', {
    request_id: 'guided-hire',
    if_version: before.state_version,
    display_name: 'Atlas',
    mandate: 'Keep my week moving.',
    focus_areas: ['plan_my_day']
  });
  await post('/api/personal-assistant/hq', {
    request_id: 'guided-build',
    if_version: (await pa()).state_version,
    name: 'My HQ',
    timezone: 'UTC',
    scope: 'all',
    include_future_workspaces: true,
    notes: { include: false }
  });
  const { status } = await (await request.get('/api/personal-hq/status')).json();
  expect(status.first_visit_state).toBe('pending');
  return '/workspaces/' + encodeURIComponent(status.workspace.folder_slug);
}
