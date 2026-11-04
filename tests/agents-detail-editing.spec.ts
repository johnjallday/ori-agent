import { test, expect, type APIRequestContext, type Page, type Request } from '@playwright/test';
import { installLocalCdn } from './helpers/offline-cdn';

/**
 * Editing an existing agent on its full page (/agents/{name}).
 *
 * Each describe group is a slice of the reliability contract and can be run
 * on its own with -g: "save contract" (versions, refusals, shared
 * confirmation), "draft isolation" (sections never reset each other), and
 * "recovery" (conflicts, lost answers, partial outcomes).
 *
 * Assertions read input values, the server's stored values, and the PATCH
 * bodies actually sent — never just a toast.
 */

test.beforeEach(async ({ page }) => {
  await installLocalCdn(page);
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await page.route('**/api/onboarding/status', route =>
    route.fulfill({ json: { needs_onboarding: false, completed: true, skipped: true } })
  );
});

let seq = 0;
async function createAgent(request: APIRequestContext, prefix = 'PW Edit') {
  const name = `${prefix} ${Date.now()} ${++seq}`;
  const res = await request.post('/api/agents', {
    data: { name, model: 'gpt-4o-mini', system_prompt: 'Original prompt.', description: 'Original' }
  });
  expect(res.ok(), await res.text()).toBeTruthy();
  return name;
}

async function detail(request: APIRequestContext, name: string) {
  const res = await request.get(`/api/agents/${encodeURIComponent(name)}/detail`);
  expect(res.ok(), await res.text()).toBeTruthy();
  return res.json();
}

async function removeAgent(request: APIRequestContext, name: string) {
  await request.delete(`/api/agents?name=${encodeURIComponent(name)}`).catch(() => undefined);
}

async function openAgent(page: Page, name: string) {
  await page.goto(`/agents/${encodeURIComponent(name)}`);
  await expect(page.locator('#agentName')).toHaveText(name);
}

// Records the JSON body of every definition PATCH the page sends.
function recordPatches(page: Page) {
  const bodies: any[] = [];
  page.on('request', (req: Request) => {
    if (req.method() === 'PATCH' && /\/api\/agents(\/|\?)/.test(req.url())) {
      try {
        bodies.push(JSON.parse(req.postData() || '{}'));
      } catch {
        bodies.push({});
      }
    }
  });
  return bodies;
}

async function editPrompt(page: Page, text: string) {
  if (!(await page.locator('#editSystemPrompt').isVisible())) {
    await page.locator('#editPromptBtn').click();
  }
  await page.locator('#editSystemPrompt').fill(text);
}

test.describe('save contract', () => {
  test('a stale second tab keeps its draft and cannot overwrite the first tab', async ({
    browser,
    request
  }) => {
    const name = await createAgent(request);
    try {
      const ctx = await browser.newContext();
      const [a, b] = [await ctx.newPage(), await ctx.newPage()];
      for (const page of [a, b]) {
        await installLocalCdn(page);
        await page.route('**/api/onboarding/status', route =>
          route.fulfill({ json: { needs_onboarding: false, completed: true, skipped: true } })
        );
        await openAgent(page, name);
      }
      const sentByB = recordPatches(b);
      const loaded = (await detail(request, name)).version;

      await editPrompt(b, 'From tab B');
      await editPrompt(a, 'From tab A');
      await a.locator('#savePromptBtn').click();
      await expect(a.locator('#promptEditStatus')).toContainText('System prompt saved');
      // The editor closes on success; the outcome must still be on screen.
      await expect(a.locator('#editSystemPrompt')).toBeHidden();
      await expect(a.locator('#promptEditStatus')).toBeVisible();

      await b.locator('#savePromptBtn').click();
      await expect(b.locator('#promptEditStatus')).toContainText(
        'They changed what you are editing'
      );
      await expect(b.locator('#editSystemPrompt')).toHaveValue('From tab B');
      expect((await detail(request, name)).system_prompt).toBe('From tab A');
      expect(sentByB).toEqual([{ system_prompt: 'From tab B', expected_version: loaded }]);

      // Saving again without reviewing is still refused: no force-save.
      await b.locator('#savePromptBtn').click();
      await expect(b.locator('#promptEditStatus')).toContainText('Someone else saved');
      expect((await detail(request, name)).system_prompt).toBe('From tab A');
      await ctx.close();
    } finally {
      await removeAgent(request, name);
    }
  });

  test('a save elsewhere that did not touch my fields lets me save again on purpose', async ({
    page,
    request
  }) => {
    const name = await createAgent(request);
    try {
      await openAgent(page, name);
      const sent = recordPatches(page);
      await editPrompt(page, 'Mine');

      const other = await request.patch(`/api/agents/${encodeURIComponent(name)}`, {
        data: { description: 'Changed elsewhere' }
      });
      expect(other.ok()).toBeTruthy();

      await page.locator('#savePromptBtn').click();
      await expect(page.locator('#promptEditStatus')).toContainText(
        'Nothing you edited was changed'
      );
      await expect(page.locator('#editSystemPrompt')).toHaveValue('Mine');
      expect((await detail(request, name)).system_prompt).toBe('Original prompt.');

      await page.locator('#savePromptBtn').click();
      await expect(page.locator('#promptEditStatus')).toContainText('System prompt saved');
      const saved = await detail(request, name);
      expect(saved.system_prompt).toBe('Mine');
      expect(saved.metadata.description).toBe('Changed elsewhere');
      expect(sent).toHaveLength(2);
      expect(sent[1].expected_version).not.toBe(sent[0].expected_version);
      expect(Object.keys(sent[1]).sort()).toEqual(['expected_version', 'system_prompt']);
    } finally {
      await removeAgent(request, name);
    }
  });

  test('an invalid rename saves nothing and keeps the whole profile draft', async ({
    page,
    request
  }) => {
    const name = await createAgent(request);
    try {
      await openAgent(page, name);
      await page.locator('#editProfileButton').click();
      await expect(page.locator('#profileEditModal')).toBeVisible();
      await page.locator('#profileAgentNameInput').fill('bad/name');
      await page.locator('#profileDescriptionInput').fill('New description');
      await page.locator('#profileSaveBtn').click();

      await expect(page.locator('#profileEditStatus')).toContainText('invalid characters');
      await expect(page.locator('#profileEditModal')).toBeVisible();
      await expect(page.locator('#profileAgentNameInput')).toHaveValue('bad/name');
      await expect(page.locator('#profileDescriptionInput')).toHaveValue('New description');
      const stored = await detail(request, name);
      expect(stored.metadata.description).toBe('Original');
    } finally {
      await removeAgent(request, name);
    }
  });

  test('a protected rename is refused before the other fields save', async ({ page, request }) => {
    const name = await createAgent(request);
    const ws = await request.post('/api/workspaces', { data: { name: `${name} WS` } });
    expect(ws.ok(), await ws.text()).toBeTruthy();
    const folder = (await ws.json()).folder;
    try {
      expect(
        (
          await request.post(`/api/workspaces/${folder.id}/agents`, { data: { agent_name: name } })
        ).ok()
      ).toBeTruthy();
      await openAgent(page, name);
      await page.locator('#editProfileButton').click();
      await page.locator('#profileAgentNameInput').fill(`${name} Renamed`);
      await page.locator('#profileDescriptionInput').fill('Should not save');
      await page.locator('#profileSaveBtn').click();

      await expect(page.locator('#profileEditStatus')).toContainText('cannot be renamed');
      await expect(page.locator('#profileDescriptionInput')).toHaveValue('Should not save');
      expect((await detail(request, name)).metadata.description).toBe('Original');
    } finally {
      await request.delete(`/api/workspaces/${folder.id}?confirm=true&delete_sessions=true`);
      await removeAgent(request, name);
    }
  });

  test('a shared edit can be declined without a write, then confirmed with the same version', async ({
    page,
    request
  }) => {
    const name = await createAgent(request);
    const folders: any[] = [];
    try {
      for (const suffix of ['One', 'Two']) {
        const ws = await request.post('/api/workspaces', { data: { name: `${name} ${suffix}` } });
        expect(ws.ok(), await ws.text()).toBeTruthy();
        const folder = (await ws.json()).folder;
        folders.push(folder);
        expect(
          (
            await request.post(`/api/workspaces/${folder.id}/agents`, {
              data: { agent_name: name }
            })
          ).ok()
        ).toBeTruthy();
      }
      await openAgent(page, name);
      const sent = recordPatches(page);
      await editPrompt(page, 'Shared change');

      page.once('dialog', dialog => dialog.dismiss());
      await page.locator('#savePromptBtn').click();
      await expect(page.locator('#promptEditStatus')).toContainText('Change cancelled');
      await expect(page.locator('#editSystemPrompt')).toHaveValue('Shared change');
      expect((await detail(request, name)).system_prompt).toBe('Original prompt.');
      expect(sent).toHaveLength(1);

      let dialogText = '';
      page.once('dialog', dialog => {
        dialogText = dialog.message();
        void dialog.accept();
      });
      await page.locator('#savePromptBtn').click();
      await expect(page.locator('#promptEditStatus')).toContainText('System prompt saved');
      expect(dialogText).toContain('2 workspaces');
      expect((await detail(request, name)).system_prompt).toBe('Shared change');
      expect(sent).toHaveLength(3);
      expect(sent[2]).toEqual({ ...sent[1], confirm_shared_edit: true });
      expect(sent[1].expected_version).toBe(sent[0].expected_version);
    } finally {
      for (const folder of folders)
        await request.delete(`/api/workspaces/${folder.id}?confirm=true&delete_sessions=true`);
      await removeAgent(request, name);
    }
  });

  test('a change outside the editable definition does not stale an open draft', async ({
    page,
    request
  }) => {
    const name = await createAgent(request);
    try {
      await openAgent(page, name);
      await editPrompt(page, 'After expert mode');
      // Expert mode is excluded from the definition version, like runtime
      // statistics (covered against the real store in agent_edit_test.go).
      const toggle = await request.patch(`/api/agents/${encodeURIComponent(name)}`, {
        data: { expert_mode: true }
      });
      expect(toggle.ok()).toBeTruthy();
      await page.locator('#savePromptBtn').click();
      await expect(page.locator('#promptEditStatus')).toContainText('System prompt saved');
      expect((await detail(request, name)).system_prompt).toBe('After expert mode');
    } finally {
      await removeAgent(request, name);
    }
  });
});
