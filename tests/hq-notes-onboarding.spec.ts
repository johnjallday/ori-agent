import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import fs from 'node:fs';
import path from 'node:path';
import { installLocalCdn } from './helpers/offline-cdn';

const AXE_URL = 'https://cdn.jsdelivr.net/npm/axe-core@4.10.3/axe.min.js';

// Build My HQ with notes included, end to end against the real server, store
// and a real (sandboxed) notes folder (assistant-hq-onboarding 3.5–3.10).
//
// A build happens once per profile, so this needs a server of its own:
//
//   ./scripts/e2e-fresh.sh tests/hq-notes-onboarding.spec.ts
//
// Path through the product: the welcome card's Build My HQ → the HQ card's
// notes review → Build. The reviewed folder is occupied by the user's own
// file first, so HQ must come up anyway with notes reported as failed; once
// the user moves their folder, the notes-only retry makes the reviewed folder.
test.describe.configure({ mode: 'serial' });
test.beforeEach(async ({ page }) => installLocalCdn(page));

async function ok(response: Awaited<ReturnType<APIRequestContext['get']>>) {
  const text = await response.text();
  expect(response.ok(), `${response.url()}: ${text}`).toBeTruthy();
  return text ? JSON.parse(text) : {};
}

async function relationship(request: APIRequestContext) {
  return (await ok(await request.get('/api/personal-assistant'))).personal_assistant;
}

// The drawer restores itself open after a reload; open it only when closed.
async function openDrawer(page: Page) {
  await page.waitForFunction(() => Boolean((window as any).PersonalAssistantPanel?.isOpen));
  if (!(await page.evaluate(() => (window as any).PersonalAssistantPanel.isOpen()))) {
    await page.locator('#personalAssistantLauncher').click();
  }
}

async function notesStatus(page: Page) {
  // The notes routes are origin-guarded; read them as the page does.
  return page.evaluate(async () => {
    const response = await fetch('/api/personal-assistant/second-brain', {
      headers: { Accept: 'application/json' }
    });
    return (await response.json()).second_brain;
  });
}

test.describe('Personal HQ with notes included', () => {
  let reviewed = '';

  test.beforeAll(async ({ request }) => {
    await request.post('/api/onboarding/skip');
    await ok(await request.post('/api/settings/workspace-root', { data: { workspace_root: '' } }));
    const initial = await relationship(request);
    test.skip(
      initial.state !== 'needs_hire',
      `needs a server that has never hired (found "${initial.state}"); run it with ./scripts/e2e-fresh.sh`
    );
    await ok(
      await request.post('/api/personal-assistant/hire', {
        data: {
          request_id: 'hq-notes-hire',
          if_version: initial.state_version ?? 0,
          display_name: 'Atlas',
          mandate: 'Keep my week moving.',
          focus_areas: ['plan_my_day']
        }
      })
    );
  });

  test('the review shows where notes go and what Ori keeps, without writing anything', async ({
    page
  }) => {
    await page.goto('/');
    await page.waitForFunction(() => Boolean((window as any).PersonalAssistantHQCard?.show));
    const status = await notesStatus(page);
    expect(status.status).toBe('not_connected');
    reviewed = status.setup_default.path;
    expect(fs.existsSync(reviewed)).toBe(false);

    await page.locator('#cockpitWelcome [data-home-welcome-action="primary"]').click();
    const notes = page.locator('#personalAssistantHQNotes');
    await expect(notes).toBeVisible();
    await expect(notes.getByRole('heading', { name: 'Notes, ready to use' })).toBeVisible();
    await expect(page.locator('#personalAssistantHQNotesPath')).toHaveText(reviewed);
    await expect(page.locator('#personalAssistantHQNotesScope')).toContainText(
      'Everything else in the folder is yours'
    );
    await page.locator('#personalAssistantHQNotesOptions summary').click();
    await expect(page.locator('#personalAssistantHQNotesInclude')).toBeChecked();
    // Unchecking defers: the location leaves the review and says so.
    await page.locator('#personalAssistantHQNotesInclude').uncheck();
    await expect(page.locator('#personalAssistantHQNotesLocation')).toBeHidden();
    await expect(page.locator('#personalAssistantHQNotesMessage')).toContainText(
      'later from Notes'
    );
    await page.locator('#personalAssistantHQNotesInclude').check();
    await expect(page.locator('#personalAssistantHQNotesLocation')).toBeVisible();
    expect(fs.existsSync(reviewed)).toBe(false);
    expect((await notesStatus(page)).status).toBe('not_connected');
  });

  test('an occupied notes folder never blocks HQ, and is never adopted', async ({
    page,
    request
  }) => {
    // The user already has a folder with that name.
    fs.mkdirSync(reviewed, { recursive: true });
    fs.writeFileSync(path.join(reviewed, 'mine.md'), 'my own note\n');

    await page.goto('/');
    await page.waitForFunction(() => Boolean((window as any).PersonalAssistantHQCard?.show));
    await page.locator('#cockpitWelcome [data-home-welcome-action="primary"]').click();
    await expect(page.locator('#personalAssistantHQNotes')).toBeVisible();
    const hqRequests: string[] = [];
    page.on('request', r => {
      if (r.method() === 'POST' && r.url().endsWith('/api/personal-assistant/hq')) {
        hqRequests.push(r.postData() || '');
      }
    });
    await page.locator('#personalAssistantHQBuild').click();
    await expect.poll(async () => (await relationship(request)).state).toBe('active');
    expect(hqRequests).toHaveLength(1);
    const sent = JSON.parse(hqRequests[0]);
    expect(sent.notes).toEqual({ include: true, witness: reviewed });
    // Focus leaves the hidden pending status for the compact result. No chat
    // drawer or backdrop covers the real HQ or the independent Notes warning.
    await expect(page.locator('#personalHQResultHost')).toBeVisible();
    await expect(page.locator('#personalAssistantPanel')).toBeHidden();
    await expect(page.locator('#personalAssistantHQHeadline')).toBeFocused();
    await expect(page.locator('#personalAssistantHQHeadline')).toHaveText('Your HQ is ready');
    const retry = page.getByRole('button', { name: 'Retry notes setup' });
    for (let i = 0; i < 6 && !(await retry.evaluate(n => n === document.activeElement)); i += 1) {
      await page.keyboard.press('Tab');
    }
    await expect(retry).toBeFocused();
    fs.mkdirSync('tasks/evidence-hq-onboarding-reveal/group-4', { recursive: true });
    await page.screenshot({
      path: `tasks/evidence-hq-onboarding-reveal/group-4/${test.info().project.name}-occupied-notes.png`
    });

    const status = await notesStatus(page);
    expect(status.status).toBe('not_connected');
    expect(status.setup?.status).toBe('failed');
    expect(status.setup?.reason).toBe('occupied');
    expect(fs.readdirSync(reviewed)).toEqual(['mine.md']);
    expect(fs.readFileSync(path.join(reviewed, 'mine.md'), 'utf8')).toBe('my own note\n');
  });

  test('after a reload, the notes-only retry makes the reviewed folder and leaves HQ alone', async ({
    page,
    request
  }) => {
    const before = await relationship(request);
    const workspaces = (await ok(await request.get('/api/workspaces'))).length ?? null;
    // The user moves their folder out of the way.
    fs.renameSync(reviewed, `${reviewed} (mine)`);

    // New test context restores only the presentation markers of the prior
    // browser; the occupied folder and HQ remain the real server's state.
    await page.addInitScript(id => {
      localStorage.setItem('ori.hq.firstAction.v1', JSON.stringify({ workspaceId: id }));
      sessionStorage.setItem('ori.hq.compactResult.v1', id);
    }, before.hq_workspace_id);
    await page.goto('/');
    await expect(page.locator('#personalHQResultHost')).toBeVisible();
    const retryResponse = page.waitForResponse(
      r => r.url().endsWith('/second-brain/setup/retry') && r.request().method() === 'POST'
    );
    await page.locator('#personalAssistantHQNotesRetry').click();
    const response = await retryResponse;
    const retried = { status: response.status(), body: await response.json() };
    expect(retried.status).toBe(200);
    expect(retried.body.notes_setup.status).toBe('ready');
    expect(retried.body.notes_setup.path).toBe(reviewed);

    const status = await notesStatus(page);
    expect(status.status).toBe('connected');
    expect(status.source.origin).toBe('created_by_ori');
    expect(status.setup).toBeUndefined();
    expect(fs.existsSync(path.join(reviewed, 'Inbox', 'README.md'))).toBe(true);
    expect(fs.readFileSync(path.join(`${reviewed} (mine)`, 'mine.md'), 'utf8')).toBe(
      'my own note\n'
    );

    const after = await relationship(request);
    expect(after.state).toBe('active');
    expect(after.hq_workspace_id).toBe(before.hq_workspace_id);
    expect(after.daily_brief).toEqual(before.daily_brief);
    await expect(page.locator('.is-assembling')).toHaveCount(0);
    await expect(page.locator('#personalAssistantHQFirstNote')).toBeVisible();
    await page.screenshot({
      path: `tasks/evidence-hq-onboarding-reveal/group-4/${test.info().project.name}-notes-recovered.png`
    });
    if (workspaces !== null) {
      expect((await ok(await request.get('/api/workspaces'))).length).toBe(workspaces);
    }
    // A second retry has nothing left to do.
    const again = await page.evaluate(async () => {
      const response = await fetch('/api/personal-assistant/second-brain/setup/retry', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: '{}'
      });
      return response.status;
    });
    expect(again).toBe(409);
  });

  test('the result leads with an optional first note, saved once even when the response is lost', async ({
    page
  }) => {
    // This browser built nothing in this context, so mark the first step as
    // the build would have (assistant-hq-onboarding 4.1).
    const hq = await page.request.get('/api/personal-hq/status');
    const workspaceId = (await hq.json()).status.workspace_id;
    await page.addInitScript(id => {
      if (sessionStorage.getItem('spec-seeded')) return;
      sessionStorage.setItem('spec-seeded', '1');
      localStorage.setItem('ori.hq.firstAction.v1', JSON.stringify({ workspaceId: id }));
      sessionStorage.setItem('ori.hq.compactResult.v1', id);
    }, workspaceId);
    await page.goto('/');
    await expect(page.locator('#personalHQResultHost')).toBeVisible();
    await expect(page.locator('#personalAssistantPanel')).toBeHidden();
    const card = page.locator('#personalAssistantHQCard');
    await expect(card).toBeVisible();
    await expect(card).toHaveAttribute('data-result', 'active');
    expect(await card.evaluate(c => c.parentElement?.id)).toBe('personalHQResultHost');
    await expect(page.locator('.is-assembling')).toHaveCount(0);
    await expect(page.locator('#personalAssistantHQFirstLead')).toContainText('Notes are ready');
    await expect(page.locator('#personalAssistantHQAdjust')).toBeHidden();
    const note = page.getByRole('button', { name: /Save a first note/ });
    await expect(note).toBeVisible();
    await expect(page.locator('#personalAssistantHQFirstFolder')).toBeHidden();
    // The result card has no A/AA violations in this state.
    await page.addScriptTag({ url: AXE_URL });
    const violations = await page.evaluate(async () =>
      (
        await (window as any).axe.run('#personalAssistantHQCard', {
          runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa'] }
        })
      ).violations.map((v: any) =>
        [
          v.id,
          ...v.nodes.map((n: any) => n.target.join(' ') + ' :: ' + (n.any?.[0]?.message || ''))
        ].join(' | ')
      )
    );
    expect(violations).toEqual([]);

    // An empty note is refused in place.
    await note.click();
    const text = page.locator('#personalAssistantHQNoteText');
    await expect(text).toBeFocused();
    await page.locator('#personalAssistantHQNoteSave').click();
    await expect(page.locator('#personalAssistantHQNoteError')).toHaveText(
      'Write something to save, or go back.'
    );

    // The first save reaches the server, but its answer is lost.
    const inbox = path.join(reviewed, 'Inbox');
    const before = fs.readdirSync(inbox).filter(name => name !== 'README.md');
    let lost = true;
    await page.route('**/api/personal-assistant/second-brain/capture', async route => {
      if (!lost) return route.continue();
      lost = false;
      await route.fetch();
      await route.abort('connectionreset');
    });
    await text.fill('Call the venue about Friday');
    await page.locator('#personalAssistantHQNoteSave').click();
    await expect(page.locator('#personalAssistantHQNoteError')).toContainText('never saved twice');
    await expect(text).toHaveValue('Call the venue about Friday');
    expect(fs.readdirSync(inbox).filter(name => name !== 'README.md')).toHaveLength(
      before.length + 1
    );
    // Back and reload retain the exact uncertain payload and request ID.
    // Editing it would turn a replay into a different logical write.
    await expect(text).toHaveAttribute('readonly', '');
    await page.locator('#personalAssistantHQNoteBack').click();
    await note.click();
    await expect(text).toHaveValue('Call the venue about Friday');
    await page.reload();
    await note.click();
    await expect(text).toHaveValue('Call the venue about Friday');
    await expect(text).toHaveAttribute('readonly', '');
    // Saving again replays the same save: still exactly one new note.
    await page.locator('#personalAssistantHQNoteSave').click();
    const saved = page.locator('#personalAssistantHQNoteSavedTitle');
    await expect(saved).toContainText('Saved to Inbox · Inbox/');
    await expect(saved).toBeFocused();
    await expect(page.locator('#personalAssistantHQNoteSavedText')).toHaveText(
      'Call the venue about Friday'
    );
    const after = fs.readdirSync(inbox).filter(name => name !== 'README.md');
    expect(after).toHaveLength(before.length + 1);
    expect(
      fs.readFileSync(path.join(inbox, after.find(n => !before.includes(n))!), 'utf8')
    ).toContain('Call the venue about Friday');

    // A reload keeps the first step; leaving it ends it for good.
    await page.reload();
    await expect(page.locator('#personalAssistantHQFirst')).toBeVisible();
    await expect(page.locator('#personalAssistantPanel')).toBeHidden();
    await expect(page.locator('.is-assembling')).toHaveCount(0);
    await page.getByRole('button', { name: 'Dismiss HQ welcome' }).click();
    await expect(card).toBeHidden();
    await expect(page.locator('[data-ws-map-viewport]')).toBeFocused();
    expect(await page.evaluate(() => localStorage.getItem('ori.hq.firstAction.v1'))).toBeNull();
    await page.reload();
    await openDrawer(page);
    await page.waitForFunction(
      () => (window as any).PersonalAssistantHQCard?._state?.relationship?.state === 'active'
    );
    await expect(card).toBeHidden();
    await expect(page.locator('#personalAssistantTodayStrip')).toBeVisible();
  });
});
