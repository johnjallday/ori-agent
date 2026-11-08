import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import fs from 'node:fs';
import path from 'node:path';
import { installLocalCdn } from './helpers/offline-cdn';

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

    await page.goto('/');
    await page.waitForFunction(() => Boolean((window as any).PersonalAssistantHQCard?._state));
    const retried = await page.evaluate(async () => {
      const response = await fetch('/api/personal-assistant/second-brain/setup/retry', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
        body: '{}'
      });
      return { status: response.status, body: await response.json() };
    });
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
});
