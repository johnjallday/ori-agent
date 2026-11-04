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

async function editTemperature(page: Page, value: string) {
  if (!(await page.locator('#editTemperature').isVisible())) {
    await page.locator('#editConfigBtn').click();
  }
  await page.locator('#editTemperature').fill(value);
}

// Counts beforeunload prompts and answers each one with `stay` or leave.
function leavePrompts(page: Page, answer: 'stay' | 'leave') {
  const seen: string[] = [];
  page.on('dialog', dialog => {
    if (dialog.type() !== 'beforeunload') return;
    seen.push(dialog.type());
    void (answer === 'stay' ? dialog.dismiss() : dialog.accept());
  });
  return seen;
}

test.describe('draft isolation', () => {
  for (const order of [
    ['configuration', 'prompt'],
    ['prompt', 'configuration']
  ]) {
    test(`configuration and prompt drafts survive each other's save (${order.join(' then ')})`, async ({
      page,
      request
    }) => {
      const name = await createAgent(request);
      try {
        await openAgent(page, name);
        const sent = recordPatches(page);
        await editTemperature(page, '0.4');
        await editPrompt(page, 'Prompt draft');
        await expect(page.locator('#configEditBadge')).toHaveText('Unsaved changes');
        await expect(page.locator('#promptEditBadge')).toHaveText('Unsaved changes');

        const save = {
          configuration: async () => {
            await page.locator('#saveConfigBtn').click();
            await expect(page.locator('#configEditStatus')).toContainText(/updated|Default model/);
          },
          prompt: async () => {
            await page.locator('#savePromptBtn').click();
            await expect(page.locator('#promptEditStatus')).toContainText('System prompt saved');
          }
        };
        await save[order[0]]();
        // The other section's input is untouched by the first save.
        if (order[0] === 'configuration')
          await expect(page.locator('#editSystemPrompt')).toHaveValue('Prompt draft');
        else await expect(page.locator('#editTemperature')).toHaveValue('0.4');
        await save[order[1]]();

        const stored = await detail(request, name);
        expect(stored.system_prompt).toBe('Prompt draft');
        expect(stored.temperature).toBe(0.4);
        expect(sent).toHaveLength(2);
        const bySection = (key: string) => sent.find(body => key in body);
        expect(Object.keys(bySection('system_prompt')).sort()).toEqual([
          'expected_version',
          'system_prompt'
        ]);
        expect(Object.keys(bySection('temperature')).sort()).toEqual([
          'expected_version',
          'temperature'
        ]);
        await expect(page.locator('#configEditBadge')).toBeHidden();
        await expect(page.locator('#promptEditBadge')).toBeHidden();
      } finally {
        await removeAgent(request, name);
      }
    });
  }

  test('saving the profile keeps an open prompt draft', async ({ page, request }) => {
    const name = await createAgent(request);
    try {
      await openAgent(page, name);
      await editPrompt(page, 'Unsaved prompt');
      await page.locator('#editProfileButton').click();
      await page.locator('#profileDescriptionInput').fill('Saved description');
      await page.locator('#profileSaveBtn').click();
      await expect(page.locator('#profileEditModal')).toBeHidden();
      await expect(page.locator('#agentDescription')).toHaveText('Saved description');
      await expect(page.locator('#editSystemPrompt')).toHaveValue('Unsaved prompt');
      await expect(page.locator('#promptEditBadge')).toHaveText('Unsaved changes');
      expect((await detail(request, name)).system_prompt).toBe('Original prompt.');
    } finally {
      await removeAgent(request, name);
    }
  });

  test('an invalid profile save leaves other drafts alone', async ({ page, request }) => {
    const name = await createAgent(request);
    try {
      await openAgent(page, name);
      await editTemperature(page, '0.9');
      await page.locator('#editProfileButton').click();
      await page.locator('#profileAgentNameInput').fill('bad/name');
      await page.locator('#profileSaveBtn').click();
      await expect(page.locator('#profileEditStatus')).toContainText('invalid characters');
      await expect(page.locator('#editTemperature')).toHaveValue('0.9');
      await expect(page.locator('#configEditBadge')).toHaveText('Unsaved changes');
    } finally {
      await removeAgent(request, name);
    }
  });

  test('an older read arriving after a save cannot repaint the saved value', async ({
    page,
    request
  }) => {
    const name = await createAgent(request);
    try {
      await openAgent(page, name);
      let release: () => void = () => {};
      const held = new Promise<void>(resolve => (release = resolve));
      let holding = true;
      await page.route(`**/api/agents/${encodeURIComponent(name)}/detail`, async route => {
        if (!holding) return route.continue();
        holding = false;
        const stale = await route.fetch(); // the values before the save
        const body = await stale.text();
        await held;
        await route.fulfill({ status: 200, contentType: 'application/json', body });
      });
      // A focus read starts and is held with the pre-save values.
      await page.evaluate(() => window.dispatchEvent(new Event('focus')));
      await editPrompt(page, 'Saved while the read was held');
      await page.locator('#savePromptBtn').click();
      await expect(page.locator('#promptEditStatus')).toContainText('System prompt saved');
      release();
      await page.waitForTimeout(300);
      await expect(page.locator('#promptDisplay')).toHaveText('Saved while the read was held');
    } finally {
      await removeAgent(request, name);
    }
  });

  test('returning to the tab after an outside edit keeps the draft and flags the conflict', async ({
    page,
    request
  }) => {
    const name = await createAgent(request);
    try {
      await openAgent(page, name);
      await editPrompt(page, 'My draft');
      const other = await request.patch(`/api/agents/${encodeURIComponent(name)}`, {
        data: { system_prompt: 'Changed elsewhere' }
      });
      expect(other.ok()).toBeTruthy();
      await page.evaluate(() => window.dispatchEvent(new Event('focus')));
      await expect(page.locator('#promptEditBadge')).toHaveText('Changed elsewhere — review');
      await expect(page.locator('#editSystemPrompt')).toHaveValue('My draft');
      await expect(page.locator('#promptEditStatus')).toContainText('changed elsewhere');
    } finally {
      await removeAgent(request, name);
    }
  });

  test('double-clicking Save sends one request', async ({ page, request }) => {
    const name = await createAgent(request);
    try {
      await openAgent(page, name);
      const sent = recordPatches(page);
      await page.route(`**/api/agents/${encodeURIComponent(name)}`, async route => {
        await new Promise(resolve => setTimeout(resolve, 400)); // a slow answer
        await route.continue();
      });
      await editPrompt(page, 'Once');
      await page.locator('#savePromptBtn').dblclick();
      await expect(page.locator('#promptEditStatus')).toContainText('System prompt saved');
      expect(sent).toHaveLength(1);
    } finally {
      await removeAgent(request, name);
    }
  });

  test('while one section saves, the others stay editable but cannot start a save', async ({
    page,
    request
  }) => {
    const name = await createAgent(request);
    try {
      await openAgent(page, name);
      let release: () => void = () => {};
      const held = new Promise<void>(resolve => (release = resolve));
      await page.route(`**/api/agents/${encodeURIComponent(name)}`, async route => {
        await held;
        await route.continue();
      });
      await editTemperature(page, '0.2');
      await editPrompt(page, 'Typing during a save');
      await page.locator('#savePromptBtn').click();
      await expect(page.locator('#promptEditBadge')).toHaveText('Saving…');
      await expect(page.locator('#editSystemPrompt')).toBeDisabled();
      await expect(page.locator('#saveConfigBtn')).toBeDisabled();
      await page.locator('#editTemperature').fill('0.25');
      release();
      await expect(page.locator('#promptEditStatus')).toContainText('System prompt saved');
      await expect(page.locator('#editTemperature')).toHaveValue('0.25');
      await expect(page.locator('#saveConfigBtn')).toBeEnabled();
    } finally {
      await removeAgent(request, name);
    }
  });

  for (const how of ['Cancel', 'close button', 'Escape', 'backdrop']) {
    test(`dismissing a dirty profile by ${how} asks first and discards only the profile`, async ({
      page,
      request
    }) => {
      const name = await createAgent(request);
      try {
        await openAgent(page, name);
        await editPrompt(page, 'Prompt stays');
        await page.locator('#editProfileButton').click();
        await expect(page.locator('#profileEditModal')).toBeVisible();
        await page.locator('#profileDescriptionInput').fill('Throw me away');
        const dismiss = async () => {
          if (how === 'Cancel')
            await page.locator('#profileEditModal .modal-footer [data-bs-dismiss="modal"]').click();
          else if (how === 'close button')
            await page.locator('#profileEditModal .btn-close').click();
          else if (how === 'Escape') await page.keyboard.press('Escape');
          else await page.mouse.click(5, 5);
        };
        await dismiss();
        const confirm = page.locator('#profileDiscardConfirm');
        await expect(confirm).toBeVisible();
        await expect(page.locator('#profileEditModal')).toBeVisible();
        await expect(confirm.locator('[data-discard-keep]')).toBeFocused();

        await confirm.locator('[data-discard-keep]').click();
        await expect(confirm).toBeHidden();
        await expect(page.locator('#profileDescriptionInput')).toHaveValue('Throw me away');

        await dismiss();
        await confirm.locator('[data-discard-confirm]').click();
        await expect(page.locator('#profileEditModal')).toBeHidden();
        await expect(page.locator('#editProfileButton')).toBeFocused();
        await expect(page.locator('#editSystemPrompt')).toHaveValue('Prompt stays');
        await page.locator('#editProfileButton').click();
        await expect(page.locator('#profileDescriptionInput')).toHaveValue('Original');
      } finally {
        await removeAgent(request, name);
      }
    });
  }

  test('at phone width the page never scrolls sideways, even with edit badges shown', async ({
    page,
    request
  }) => {
    const name = await createAgent(request);
    try {
      await page.setViewportSize({ width: 390, height: 844 });
      await openAgent(page, name);
      const overflow = () =>
        page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
      expect(await overflow()).toBeLessThanOrEqual(0);
      await editTemperature(page, '0.3');
      await editPrompt(page, 'Narrow draft');
      await page.locator('#editProfileButton').click();
      await page.locator('#profileDescriptionInput').fill('Narrow profile');
      await expect(page.locator('#profileEditBadge')).toBeVisible();
      expect(await overflow()).toBeLessThanOrEqual(0);
    } finally {
      await removeAgent(request, name);
    }
  });

  test('a clean profile closes without asking', async ({ page, request }) => {
    const name = await createAgent(request);
    try {
      await openAgent(page, name);
      await page.locator('#editProfileButton').click();
      await expect(page.locator('#profileEditModal')).toBeVisible();
      await page.keyboard.press('Escape');
      await expect(page.locator('#profileEditModal')).toBeHidden();
      await expect(page.locator('#profileDiscardConfirm')).toBeHidden();
    } finally {
      await removeAgent(request, name);
    }
  });

  test('Cancel on a dirty prompt asks first and keeps the configuration draft', async ({
    page,
    request
  }) => {
    const name = await createAgent(request);
    try {
      await openAgent(page, name);
      await editTemperature(page, '0.6');
      await editPrompt(page, 'Discard me');
      await page.locator('#editPromptBtn').click(); // "Cancel"
      const confirm = page.locator('#promptDiscardConfirm');
      await expect(confirm).toBeVisible();
      await confirm.locator('[data-discard-keep]').click();
      await expect(page.locator('#editSystemPrompt')).toHaveValue('Discard me');
      await expect(page.locator('#editPromptBtn')).toBeFocused();

      await page.locator('#editPromptBtn').click();
      await confirm.locator('[data-discard-confirm]').click();
      await expect(page.locator('#editSystemPrompt')).toBeHidden();
      await expect(page.locator('#promptDisplay')).toHaveText('Original prompt.');
      await expect(page.locator('#editPromptBtn')).toBeFocused();
      await expect(page.locator('#editTemperature')).toHaveValue('0.6');
      await expect(page.locator('#configEditBadge')).toHaveText('Unsaved changes');
    } finally {
      await removeAgent(request, name);
    }
  });

  test('leaving with unsaved work warns once, staying keeps it, and a clean page leaves freely', async ({
    page,
    request
  }) => {
    const name = await createAgent(request);
    const ws = await request.post('/api/workspaces', { data: { name: `${name} WS` } });
    expect(ws.ok(), await ws.text()).toBeTruthy();
    const folder = (await ws.json()).folder;
    try {
      await page.goto(`/agents/${encodeURIComponent(name)}?tab=workspaces`);
      await expect(page.locator('[data-workspace-status]')).toContainText('Membership loaded');
      // Membership and profile both dirty: still exactly one warning.
      await page.locator(`[data-workspace-id="${folder.id}"]`).check();
      await page.locator('#editProfileButton').click();
      await page.locator('#profileDescriptionInput').fill('Dirty profile');
      await page.locator('#profileSaveBtn').focus();
      const prompts = leavePrompts(page, 'stay');
      await page.evaluate(() => {
        window.location.href = '/agents';
      });
      await expect.poll(() => prompts.length).toBe(1);
      await page.waitForTimeout(300);
      expect(prompts).toHaveLength(1);
      expect(new URL(page.url()).pathname).toBe(`/agents/${encodeURIComponent(name)}`);
      await expect(page.locator('#profileDescriptionInput')).toHaveValue('Dirty profile');
      await expect(page.locator(`[data-workspace-id="${folder.id}"]`)).toBeChecked();
    } finally {
      await request.delete(`/api/workspaces/${folder.id}?confirm=true&delete_sessions=true`);
      await removeAgent(request, name);
    }
  });

  test('a saved rename updates the address without a leave warning', async ({ page, request }) => {
    const name = await createAgent(request);
    const renamed = `${name} Renamed`;
    try {
      await openAgent(page, name);
      const prompts = leavePrompts(page, 'leave');
      await page.locator('#editProfileButton').click();
      await page.locator('#profileAgentNameInput').fill(renamed);
      await page.locator('#profileSaveBtn').click();
      await expect(page.locator('#profileEditModal')).toBeHidden();
      await expect(page).toHaveURL(new RegExp(`/agents/${encodeURIComponent(renamed)}$`));
      await expect(page.locator('#agentName')).toHaveText(renamed);
      await page.locator('#editPromptBtn').click();
      await page.reload();
      await expect(page.locator('#agentName')).toHaveText(renamed);
      expect(prompts).toHaveLength(0);
    } finally {
      await removeAgent(request, renamed);
      await removeAgent(request, name);
    }
  });
});
