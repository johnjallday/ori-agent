// Shared setup and drawer steps for the Operate Ori browser acceptance.
//
// Everything here uses the real host: real routes, the SQLite operation store,
// canonical conversations and the production browser modules. Only the model's
// replies are a fixture (the loopback provider `--operate` starts), and each
// spec says so where it captures evidence.
import { expect } from '@playwright/test';
import type { APIRequestContext, Page } from '@playwright/test';

export const FIXTURE_MODEL = { provider: 'ollama', model: 'ori-workspace-fixture' };

export type SeededWorkspace = { id: string; name: string; folder_slug: string };

/** Hire the assistant, build its HQ and point the system model at the fixture. */
export async function seedHiredAssistant(request: APIRequestContext, tag: string): Promise<void> {
  await request.post('/api/onboarding/skip');
  for (const [path, data] of [
    ['/api/settings/workspace-root', { workspace_root: '' }],
    ['/api/settings/system-model', FIXTURE_MODEL]
  ] as const)
    expect((await request.post(path, { data })).ok()).toBeTruthy();
  let assistant = (await (await request.get('/api/personal-assistant')).json()).personal_assistant;
  const hire = await request.post('/api/personal-assistant/hire', {
    data: {
      request_id: `${tag}-hire`,
      if_version: assistant.state_version || 0,
      display_name: 'Atlas',
      mandate: 'Help me plan.',
      focus_areas: ['plan_my_day']
    }
  });
  expect(hire.status(), await hire.text()).toBe(201);
  assistant = (await hire.json()).personal_assistant;
  const hq = await request.post('/api/personal-assistant/hq', {
    data: {
      request_id: `${tag}-hq`,
      if_version: assistant.state_version,
      name: 'My HQ',
      timezone: 'UTC'
    }
  });
  expect(hq.status(), await hq.text()).toBe(201);
}

export async function listWorkspaces(request: APIRequestContext): Promise<SeededWorkspace[]> {
  return (await (await request.get('/api/workspaces')).json()).folders;
}

/**
 * Create a workspace and staff it with existing or new agents by name. With
 * `worker`, new agents run on that model, so a task they are given is really
 * executed by Ori against the loopback fixture instead of failing for want of
 * a vendor key.
 */
export async function seedWorkspace(
  request: APIRequestContext,
  name: string,
  agents: string[] = [],
  worker?: { provider: string; model: string }
): Promise<SeededWorkspace> {
  const created = await request.post('/api/workspaces', { data: { name } });
  expect(created.ok(), await created.text()).toBeTruthy();
  const workspace = (await listWorkspaces(request)).find(row => row.name === name);
  expect(workspace, `workspace ${name}`).toBeTruthy();
  for (const agent of agents) {
    // An agent that already exists is reused; either way it joins this roster.
    await request.post('/api/agents', {
      data: worker
        ? { name: agent, model: worker.model, llm_provider: worker.provider }
        : { name: agent, model: 'gpt-4o-mini' }
    });
    const joined = await request.post('/api/orchestration/workspace/agents', {
      data: { workspace_id: workspace!.id, agent_name: agent }
    });
    expect(joined.ok(), await joined.text()).toBeTruthy();
  }
  return workspace!;
}

/** What the tasks of a workspace look like to the server, for zero-write checks. */
export async function taskCount(request: APIRequestContext, workspaceId: string): Promise<number> {
  const body = await (
    await request.get(`/api/orchestration/tasks?workspace_id=${encodeURIComponent(workspaceId)}`)
  ).json();
  return (body.tasks || []).length;
}

/** The drawer on one page: opening it, the Watch switch and sending a turn. */
export function drawer(page: Page) {
  const status = page.locator('#personalAssistantOperationStatus');
  const watch = page.locator('#personalAssistantWatch');
  return {
    status,
    watch,
    watchRow: page.locator('#personalAssistantWatchRow'),
    actions: page.locator('#personalAssistantOperationActions'),
    answers: page.locator('#homeAssistantConversation [data-message-role="assistant"]'),
    async open() {
      // Wait until the drawer has read the relationship: that is when it
      // decides to reopen itself after a navigation, so a click cannot race it.
      await page.waitForFunction(
        () =>
          Boolean((window as any).PersonalAssistantOperations) &&
          Boolean((window as any).PersonalAssistantPanel?._state?.personalAssistant)
      );
      if (!(await page.locator('#personalAssistantPanel').isVisible()))
        await page.locator('#personalAssistantLauncher').click();
      await expect(page.locator('#personalAssistantPanel')).toBeVisible();
    },
    async setWatch(on: boolean) {
      await expect(watch).toBeVisible();
      if ((await watch.isChecked()) !== on) await watch.click();
      await expect(watch).toBeChecked({ checked: on });
    },
    /** Send a turn and return the Ask response body, read before any navigation. */
    async send(prompt: string) {
      await page.locator('#personalAssistantInput').fill(prompt);
      const response = page.waitForResponse(
        candidate => new URL(candidate.url()).pathname === '/api/home-assistant/ask'
      );
      await page.locator('#personalAssistantSend').click();
      return (await response).json();
    },
    /** This tab's identity and the operation it is following, from its own storage. */
    async tabState() {
      return page.evaluate(() => ({
        tab: window.sessionStorage.getItem('ori.personalAssistant.tab') || '',
        operation: JSON.parse(
          window.sessionStorage.getItem('ori.personalAssistant.operation') || 'null'
        )
      }));
    }
  };
}

/** The server's record of an operation as one tab may see it. */
export async function readOperation(request: APIRequestContext, id: string, tab: string) {
  const response = await request.get(
    `/api/home-assistant/operations/${encodeURIComponent(id)}?tab=${encodeURIComponent(tab)}`
  );
  return { status: response.status(), body: await response.json() };
}

/**
 * Record a Bootstrap dialog's lifecycle on the current page.
 *
 * Bootstrap ignores Close and Escape until a dialog has finished opening, and
 * a dialog is already "visible" before that. A check that closes a dialog
 * waits for `opened()` first; a dialog that still does not close leaves its
 * events here to say why. Call this before the dialog opens: a navigation
 * discards it.
 */
export async function watchDialog(page: Page, selector: string) {
  await page.evaluate(target => {
    const seen: string[] = [];
    const all = ((window as any).__oriDialogEvents ||= {});
    all[target] = seen;
    const dialog = document.querySelector(target);
    for (const name of ['show', 'shown', 'hide', 'hidden'])
      dialog?.addEventListener(`${name}.bs.modal`, () => seen.push(name));
  }, selector);
  const events = (): Promise<string[]> =>
    page.evaluate(target => ((window as any).__oriDialogEvents || {})[target] || [], selector);
  return {
    events,
    async opened() {
      await expect.poll(events, { timeout: 10_000 }).toContain('shown');
    },
    /** Fails with the events seen, so "never asked to hide" reads differently from "reopened". */
    async closed() {
      await expect.poll(events, { timeout: 10_000 }).toContain('hidden');
    }
  };
}
