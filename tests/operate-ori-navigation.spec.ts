import { test, expect } from '@playwright/test';
import { mkdir } from 'node:fs/promises';
import { basename, join } from 'node:path';
import {
  seedHiredAssistant,
  seedWorkspace,
  listWorkspaces,
  taskCount,
  drawer,
  readOperation
} from './helpers/operate-ori';

// Operate Ori, group 1: ask the assistant to open a workspace and select an
// agent, and watch the real app do it.
//
// Production browser, Route/Ask, the operation tool registry, the SQLite
// operation store and the page's own controllers, under `wt demo`. PROVIDER
// FIXTURE: the model is the loopback stand-in `--operate` starts. It calls
// Ori's real operation tool with the user's own words and repeats only what
// that tool returned, so this proves the host, protocol and browser wiring. It
// is not evidence of how a vendor model behaves.
test('wt demo: Ori opens a workspace and selects an agent for the one tab that watches', async ({
  page,
  context,
  request
}) => {
  const sandbox = process.env.ORI_WORKSPACE_OPERATE_SANDBOX;
  const provider = process.env.ORI_WORKSPACE_PROVIDER_FIXTURE;
  test.skip(!sandbox || !provider, 'Use python3 scripts/assistant-workspace-demo.py --operate');
  test.setTimeout(240_000);
  expect(basename(sandbox!)).toMatch(/^ori-demo\./);
  const evidence = join(process.cwd(), 'tasks', 'evidence', 'operate-ori', 'group-1');
  await mkdir(evidence, { recursive: true, mode: 0o750 });
  const shot = (target: typeof page, name: string) =>
    target.screenshot({ path: join(evidence, name), fullPage: false });

  await seedHiredAssistant(request, 'operate-navigation');
  const album = await seedWorkspace(request, 'Album-1 operate fixture', ['Mixer', 'Engineer']);
  const second = await seedWorkspace(request, 'Second operate fixture');
  const albumPath = `/workspaces/${album.folder_slug}`;
  const before = {
    workspaces: (await listWorkspaces(request)).length,
    albumTasks: await taskCount(request, album.id),
    secondTasks: await taskCount(request, second.id)
  };

  // The drawer learns that Ori can operate here from the status it already reads.
  const status = (await (await request.get('/api/personal-assistant')).json()).personal_assistant;
  expect(status.availability.operations).toMatchObject({ available: true, status: 'available' });

  // --- 1. Watch on: the asking tab opens the workspace and selects the agent.
  const other = await context.newPage();
  await other.goto('/');
  await page.goto('/');
  const here = drawer(page);
  await here.open();
  await here.openMore();
  await expect(here.watchRow).toBeVisible();
  await expect(here.watch).not.toBeChecked(); // Watch is an explicit choice.
  await here.setWatch(true);
  await shot(page, '01-watch-on-home.png');

  const asked = await here.send('Open the Album-1 operate fixture workspace and select Mixer');
  expect(asked.conversation.stored).toBe(true);
  expect(asked.operation).toMatchObject({ status: 'requested', watching: true, pending: 2 });
  expect(asked.operation.commands.map((c: any) => c.action)).toEqual([
    'open_workspace',
    'select_agent'
  ]);
  expect(asked.operation.context).toMatchObject({
    workspace_id: album.id,
    workspace_slug: album.folder_slug,
    agent_name: 'Mixer'
  });
  // The model was told a request was recorded; its reply does not claim a view.
  expect(asked.response).toBe(
    'I asked Ori to open Album-1 operate fixture and select Mixer in this tab.'
  );

  await page.waitForURL(url => url.pathname === albumPath);
  await here.open(); // the drawer's open state is this tab's and survives the navigation
  await expect(here.status).toHaveText('Opened Album-1 operate fixture and selected Mixer.', {
    timeout: 20_000
  });
  // The real control on the real page was used: the roster's own selection.
  const mixer = page.locator(
    '[data-agent-key="mixer"][aria-pressed="true"], [data-agent-key="mixer"].is-selected'
  );
  await expect(mixer.first()).toBeVisible();
  expect(new URL(page.url()).searchParams.get('agent')).toBe('mixer');
  await shot(page, '02-opened-and-selected.png');
  // The page itself, without the drawer over it: the control Ori used is the
  // highlighted one.
  await expect(
    page.locator('.ori-operation-highlight[data-agent-key="mixer"]').first()
  ).toBeVisible();
  await page.evaluate(() => (window as any).PersonalAssistantPanel.close());
  await shot(page, '02b-page-with-highlighted-agent.png');
  await here.open();
  // The conversation is the same one, shown on the new page.
  await expect(here.answers.last()).toContainText('I asked Ori to open Album-1 operate fixture');

  // The server's record: both steps acknowledged, as two distinct view facts.
  const tabA = await here.tabState();
  expect(tabA.operation.id).toBe(asked.operation.id);
  const recorded = await readOperation(request, asked.operation.id, tabA.tab);
  expect(recorded.body.operation.pending).toBe(0);
  expect(recorded.body.operation.commands.map((c: any) => c.status)).toEqual([
    'acknowledged',
    'acknowledged'
  ]);
  expect(recorded.body.operation.receipts.map((r: any) => r.kind)).toEqual([
    'view_opened',
    'agent_selected'
  ]);

  // --- 2. The second tab never moved and was handed nothing.
  expect(new URL(other.url()).pathname).toBe('/');
  const there = drawer(other);
  await there.open();
  await expect(there.status).toBeHidden();
  const tabB = await there.tabState();
  expect(tabB.tab).not.toBe(tabA.tab);
  expect(tabB.operation).toBeNull();
  const seenByOther = await readOperation(request, asked.operation.id, tabB.tab || 'tab-b');
  expect(seenByOther.body.operation.watching).toBe(false);
  await shot(other, '03-second-tab-stayed-home.png');
  await other.close();

  // --- 3. A reload after the acknowledgements repeats nothing.
  const revision = recorded.body.operation.revision;
  await page.reload();
  await here.open();
  await expect(here.status).toHaveText('Opened Album-1 operate fixture and selected Mixer.');
  expect(new URL(page.url()).pathname).toBe(albumPath);
  expect((await readOperation(request, asked.operation.id, tabA.tab)).body.operation.revision).toBe(
    revision
  );

  // --- 4. Watch off: nothing moves; an explicit button opens it.
  await page.goto('/');
  await here.open();
  await here.setWatch(false);
  const offered = await here.send('Open the Second operate fixture workspace');
  expect(offered.operation).toMatchObject({ status: 'requested', watching: false, pending: 1 });
  expect(offered.operation.commands ?? []).toEqual([]);
  expect(offered.response).toContain('Nothing moves until you choose.');
  await expect(here.status).toHaveText(
    'Ready to open Second operate fixture. Nothing moves until you choose.'
  );
  expect(new URL(page.url()).pathname).toBe('/');
  await shot(page, '04-watch-off-open-button.png');
  await here.actions.getByRole('button', { name: 'Open Second operate fixture' }).click();
  await page.waitForURL(url => url.pathname === `/workspaces/${second.folder_slug}`);
  await here.open();
  await expect(here.status).toHaveText('Opened Second operate fixture.', { timeout: 20_000 });

  // --- 5. A lost acknowledgement pauses truthfully; user navigation is respected.
  await page.goto('/');
  await here.open();
  await here.setWatch(true);
  let dropped = 0;
  await page.route('**/api/home-assistant/operations/*/ack', route => {
    dropped += 1;
    return route.abort();
  });
  const lost = await here.send('Open the Album-1 operate fixture workspace and select Engineer');
  expect(lost.operation.pending).toBe(2);
  await page.waitForURL(url => url.pathname === albumPath);
  await here.open();
  await expect(here.status).toHaveText('Ori could not be reached. Nothing was changed.', {
    timeout: 20_000
  });
  expect(dropped).toBeGreaterThan(0);
  // The server never heard of arrival, so it still records nothing as opened.
  const unheard = await readOperation(request, lost.operation.id, tabA.tab);
  expect(unheard.body.operation.pending).toBe(2);
  expect(unheard.body.operation.receipts ?? []).toEqual([]);
  await shot(page, '05-lost-acknowledgement-paused.png');
  await page.unroute('**/api/home-assistant/operations/*/ack');

  // The user goes somewhere else. Ori does not pull the tab back by itself.
  await page.goto('/');
  await here.open();
  await expect(here.status).toHaveText('Paused: you are on another page.');
  await page.waitForTimeout(1_500);
  expect(new URL(page.url()).pathname).toBe('/');
  await shot(page, '06-user-navigated-resume-offered.png');
  await here.actions.getByRole('button', { name: 'Resume' }).click();
  await page.waitForURL(url => url.pathname === albumPath);
  await here.open();
  await expect(here.status).toHaveText('Opened Album-1 operate fixture and selected Engineer.', {
    timeout: 20_000
  });
  expect(new URL(page.url()).searchParams.get('agent')).toBe('engineer');

  // --- 6. A missing target requests nothing and the page stays put.
  await page.goto('/');
  await here.open();
  const missing = await here.send('Open the Album-1 operate fixture workspace and select Editor');
  expect(missing.operation).toBeUndefined();
  expect(missing.response).toContain('agent_not_in_workspace');
  expect(missing.response).toContain('Nothing was requested.');
  const nowhere = await here.send('Open the Nowhere workspace');
  expect(nowhere.operation).toBeUndefined();
  expect(nowhere.response).toContain('workspace_not_found');
  expect(new URL(page.url()).pathname).toBe('/');

  // --- 7. Ordinary conversation is not an operation.
  const chat = await here.send('What do you think about the Album-1 operate fixture workspace?');
  expect(chat.operation).toBeUndefined();
  expect(chat.operation_error).toBeUndefined();
  expect(new URL(page.url()).pathname).toBe('/');

  // --- 8. The capability is missing: no Watch, no diversion, manual route kept.
  // Here the system model is cleared, which this sandbox can do for real. A
  // provider that is configured but cannot run Ori's tools takes the same path;
  // that case is covered against the handler and the real host in Go, because
  // the sandbox has no such provider to configure.
  expect(
    (await request.post('/api/settings/system-model', { data: { provider: '', model: '' } })).ok()
  ).toBeTruthy();
  const limited = (await (await request.get('/api/personal-assistant')).json()).personal_assistant;
  expect(limited.availability.operations).toMatchObject({
    available: false,
    status: 'not_configured',
    reason: 'model_not_configured'
  });
  const routed = await (
    await request.post('/api/home-assistant/route', {
      data: {
        prompt: 'Open the Album-1 operate fixture workspace',
        context: {
          context_version: 1,
          origin: 'personal_assistant_panel',
          surface: 'home',
          page_path: '/'
        }
      }
    })
  ).json();
  expect(routed.intent).toBe('app_navigation'); // the existing owner keeps the request
  await page.goto('/');
  await here.open();
  // Not just folded away in "…": the row itself is not offered here.
  await here.openMore();
  await expect(here.watchRow).toBeHidden();
  await shot(page, '07-capability-missing-no-watch.png');
  expect(
    (
      await request.post('/api/settings/system-model', {
        data: { provider: 'ollama', model: 'ori-workspace-fixture' }
      })
    ).ok()
  ).toBeTruthy();

  // Nothing in this whole journey created a workspace or a task.
  expect((await listWorkspaces(request)).length).toBe(before.workspaces);
  expect(await taskCount(request, album.id)).toBe(before.albumTasks);
  expect(await taskCount(request, second.id)).toBe(before.secondTasks);
});
