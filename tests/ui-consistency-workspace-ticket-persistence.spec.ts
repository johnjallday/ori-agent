import { expect, test } from '@playwright/test';
import { capture, preparePage } from './helpers/ui-consistency';
import {
  createWorkspaceSeed,
  freezeWorkspaceSeed,
  checkpointOwnedTicket,
  retireWorkspaceSeed,
  type WorkspaceSeed
} from './helpers/ui-consistency-workspace';
import {
  guardWorkspaceReadTour,
  workspaceMetadataReads
} from './fixtures/ui-consistency-workspace';

let seed: WorkspaceSeed | undefined;
let check: (() => Promise<void>) | undefined;
test.afterEach(async ({ page, request }) => {
  test.setTimeout(120_000);
  await page.goto('about:blank');
  try {
    if (check) await check();
  } finally {
    if (seed) await retireWorkspaceSeed(request, seed);
    seed = undefined;
    check = undefined;
  }
});
for (const theme of ['light', 'dark'] as const) {
  test(`manual ticket real create edit reload and cancel ${theme}`, async ({
    page,
    request
  }, info) => {
    seed = await createWorkspaceSeed(request);
    await freezeWorkspaceSeed(request, seed);
    await preparePage(page, theme, 390);
    await workspaceMetadataReads(page);
    check = await guardWorkspaceReadTour(page, [seed]);
    const api = `/api/workspaces/${seed.workspace.id}/tickets`;
    // Reviewed host-only bookkeeping (workspace_lead_conversation.go ->
    // workspaceupdates.Deliver -> EnsureLeadConversation): no model, tools,
    // project scan or work execution. Let the real owned welcome persist;
    // never replace this POST with a fictional success or hide the assistant.
    const welcomePath = `/api/workspaces/${seed.workspace.id}/lead-conversation/open`;
    const welcomeSessions = new Set<string>();
    const welcomeErrors: string[] = [];
    const welcomeReads: Promise<void>[] = [];
    page.on('response', response => {
      if (new URL(response.url()).pathname !== welcomePath || !response.ok()) return;
      welcomeReads.push(
        response
          .json()
          .then(body => {
            if (body.session_id) welcomeSessions.add(body.session_id);
          })
          .catch(error => {
            welcomeErrors.push(String(error));
          })
      );
    });
    await page.route(`**${welcomePath}`, async route => {
      expect(route.request().method()).toBe('POST');
      expect(route.request().postData() ?? '').toBe('');
      return route.continue();
    });
    const title = `${seed.workspace.name} manual capture`;
    const description = 'Fictional local work. Do not start an agent or attach a schedule.';
    let creates = 0;
    await page.route(`**${api}`, async route => {
      if (route.request().method() !== 'POST') return route.fallback();
      const body = route.request().postDataJSON();
      expect(body.idempotency_key).toMatch(/^give-work:[0-9a-f-]+$/);
      expect(body).toEqual({
        state: 'backlog',
        title,
        description,
        tags: [],
        priority: 0,
        due_date: null,
        reference_url: '',
        source: '',
        source_id: '',
        linked_note_ids: [],
        assignee: '',
        start: false,
        idempotency_key: body.idempotency_key
      });
      expect(++creates).toBe(1);
      return route.continue();
    });
    const base = `/workspaces/${seed.workspace.folder_slug}`;
    await page.goto(`${base}?mode=details`);
    await page.waitForFunction(() => (window as any).workspaceDetail?.initialListsLoaded === true);
    await page.locator('[data-cmd-give-work]').click();
    const dialog = page.locator('dialog.give-work');
    await dialog.locator('[name="what"]').fill(title);
    await dialog.locator('[name="details"]').fill(description);
    await expect(dialog.locator('[name="who"]')).toHaveValue('');
    await expect(dialog.locator('[name="startNow"]')).not.toBeChecked();
    const createdResponse = page.waitForResponse(
      response => new URL(response.url()).pathname === api && response.request().method() === 'POST'
    );
    await dialog.locator('[data-give-work-submit]').click();
    const created = await createdResponse;
    expect(created.status()).toBe(201);
    const ticket = await created.json();
    expect(ticket.state).toBe('backlog');
    expect(ticket.assignee || '').toBe('');
    expect(ticket.start?.started || false).toBe(false);
    await checkpointOwnedTicket(request, seed, {
      id: ticket.id,
      title,
      description,
      state: 'backlog',
      version: ticket.version
    });
    await expect(dialog).toHaveCount(0);
    await page.unroute(`**${api}`);
    await page.locator('[data-cmd-view-mode="tickets"]').click();
    const card = page.locator(`#hubTicketsList [data-ticket-id="${ticket.id}"] .ticket-card-open`);
    await expect(card).toBeVisible();
    await card.evaluate(node => node.scrollIntoView({ block: 'center', behavior: 'instant' }));
    const clearance = await card.evaluate(node => {
      const card = node.getBoundingClientRect();
      const launcher = document
        .getElementById('personalAssistantLauncher')!
        .getBoundingClientRect();
      return {
        top: card.top,
        bottom: card.bottom,
        launcherTop: launcher.top,
        viewport: innerHeight
      };
    });
    expect(clearance.top).toBeGreaterThanOrEqual(0);
    expect(
      clearance.bottom,
      'the whole single card must scroll clear of the assistant'
    ).toBeLessThanOrEqual(Math.min(clearance.launcherTop, clearance.viewport));
    await card.click();
    const edit = page.locator('#hubTicketDetail .ticket-detail-edit');
    await edit.locator('summary').click();
    const updatedTitle = `${seed.workspace.name} reviewed manual capture`;
    const updatedDescription = 'Reviewed local details; still no execution or schedule.';
    await edit.locator('[name="title"]').fill(updatedTitle);
    await edit.locator('[name="description"]').fill(updatedDescription);
    let patches = 0;
    const ticketPath = `${api}/${ticket.id}`;
    await page.route(`**${ticketPath}`, async route => {
      if (route.request().method() !== 'PATCH') return route.fallback();
      expect(route.request().postDataJSON()).toEqual({
        title: updatedTitle,
        description: updatedDescription,
        tags: [],
        priority: 3,
        due_date: null,
        reference_url: '',
        version: ticket.version
      });
      expect(++patches).toBe(1);
      return route.continue();
    });
    const editedResponse = page.waitForResponse(
      response =>
        new URL(response.url()).pathname === ticketPath && response.request().method() === 'PATCH'
    );
    await edit.getByRole('button', { name: 'Save', exact: true }).click();
    const edited = await editedResponse;
    expect(edited.ok()).toBe(true);
    const saved = await edited.json();
    expect(saved.version).toBeGreaterThan(ticket.version);
    await checkpointOwnedTicket(request, seed, {
      id: ticket.id,
      title: updatedTitle,
      description: updatedDescription,
      state: 'backlog',
      version: saved.version
    });
    await page.unroute(`**${ticketPath}`);
    await expect(page.locator('#hubTicketDetailTitle')).toContainText(updatedTitle);
    await page.reload();
    await expect(page.locator('#hubTicketDetailTitle')).toContainText(updatedTitle);
    await edit.locator('summary').click();
    await expect(edit.locator('[name="title"]')).toHaveValue(updatedTitle);
    await expect(edit.locator('[name="description"]')).toHaveValue(updatedDescription);
    await edit
      .locator('[name="title"]')
      .fill('Unsent cancellation must not rewrite the saved record.');
    await page.locator('#hubTicketDetailClose').click();
    await expect(page.locator('#hubTicketDetail')).toBeHidden();
    expect((await (await request.get(ticketPath)).json()).title).toBe(updatedTitle);
    expect(creates).toBe(1);
    expect(patches).toBe(1);
    await Promise.all(welcomeReads);
    expect(welcomeErrors).toEqual([]);
    expect(welcomeSessions.size).toBeLessThanOrEqual(1);
    for (const id of welcomeSessions) {
      expect(id).toMatch(/^[0-9a-f-]{36}$/);
      const response = await request.get(`/api/sessions/${id}`);
      expect(response.ok()).toBe(true);
      const body = await response.json();
      expect((body.session ?? body).workspace_id).toBe(seed.workspace.id);
    }
    const runs = await (await request.get(`/api/workspaces/${seed.workspace.id}/runs`)).json();
    expect(runs).toHaveProperty('runs');
    expect(runs.runs || []).toEqual([]);
    await capture(page, info, 'workspace-ticket-persisted', {
      workspaceId: seed.workspace.id,
      ticketId: ticket.id,
      persistence:
        'real exact manual POST and versioned PATCH, reload readback, cancel with no write; no run'
    });
    await check();
  });
}
