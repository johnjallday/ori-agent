import { test, expect, type Page } from '@playwright/test';

// Global memory: the Personal HQ memory, proposed into from any workspace,
// reviewed on the Profile, promoted from and copied into workspaces.
//
// Runs against an isolated real server (`./scripts/e2e-fresh.sh`, `wt demo`).
// No model is used: the direct `/tool` chat command runs the real workspace
// tool. The only mock keeps first-run onboarding out of the way.
test.describe.serial('Global memory across workspaces', () => {
  const proposal = 'I always deliver stems at 48 kHz.';
  // The project workspace's own agent; no model is ever called.
  const projectAgent = 'Scout';

  // A sandbox server was never onboarded and would open the welcome wizard
  // over every page; that is unrelated to global memory.
  async function skipOnboarding(page: Page) {
    await page.route('**/api/onboarding/status', route =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ needs_onboarding: false, completed: true })
      })
    );
  }

  // A brand-new workspace without agents asks for a Commander, and creating
  // one starts a first task behind a confirmation. Clear both the way a user
  // would (see tests/file-janitor.spec.ts).
  async function clearBlockingModals(page: Page) {
    const nudge = page.getByRole('button', { name: 'Create Commander' });
    if (await nudge.isVisible().catch(() => false)) {
      await nudge.click();
      await expect(nudge).toBeHidden({ timeout: 15000 });
    }
    const gate = page.locator('#workspace-detail-task-confirm-modal');
    if (await gate.isVisible().catch(() => false)) {
      await page.locator('#workspace-detail-task-confirm-close').click();
      await expect(gate).toBeHidden({ timeout: 15000 });
    }
  }

  async function openMemoryTab(page: Page, slug: string) {
    await page.goto(`/workspaces/${slug}`);
    await clearBlockingModals(page);
    await page.locator('[data-cmd-view-mode="details"]').click();
    await page.locator('[data-cmd-manage-section="systems"]').click();
    await page.locator('[data-cmd-system-tab="memory"]').click();
    const pane = page.locator('#workspace-detail-config-memory-pane');
    await expect(pane).toBeVisible();
    return pane;
  }

  async function ensurePersonalHQ(page: Page) {
    const current = await (await page.request.get('/api/personal-assistant')).json();
    if (current.personal_assistant?.state === 'active') return;
    const root = await page.request.post('/api/settings/workspace-root', {
      data: { workspace_root: '' }
    });
    expect(root.status(), await root.text()).toBe(200);
    const hire = await page.request.post('/api/personal-assistant/hire', {
      data: {
        request_id: 'global-memory-hire',
        if_version: 0,
        display_name: 'Atlas',
        mandate: 'Keep my priorities visible.',
        focus_areas: ['plan_my_day']
      }
    });
    expect(hire.status(), await hire.text()).toBe(201);
    const hq = await page.request.post('/api/personal-assistant/hq', {
      data: {
        request_id: 'global-memory-hq',
        if_version: (await hire.json()).personal_assistant.state_version,
        name: 'My HQ',
        timezone: 'UTC'
      }
    });
    expect(hq.status(), await hq.text()).toBe(201);
  }

  // projectWorkspace returns a workspace with an entry agent (its Commander),
  // which the chat route needs before it runs any tool.
  async function projectWorkspace(page: Page, name: string) {
    const listed = (await (await page.request.get('/api/workspaces')).json()).workspaces as Array<{
      id: string;
      name: string;
      folder_slug: string;
    }>;
    const existing = listed.find(ws => ws.name === name);
    if (existing) return existing;
    const agent = await page.request.post('/api/agents', {
      data: { name: projectAgent, model: 'gpt-4o-mini' }
    });
    expect(agent.ok(), await agent.text()).toBeTruthy();
    const created = await page.request.post('/api/workspaces', {
      data: { name, entry_agent_name: projectAgent }
    });
    expect(created.status(), await created.text()).toBeLessThan(300);
    return (await created.json()).folder as { id: string; name: string; folder_slug: string };
  }

  // proposeFromWorkspace runs the real tool through the workspace's chat
  // route with the direct `/tool` command, as its entry agent.
  async function proposeFromWorkspace(
    page: Page,
    workspace: { id: string; folder_slug: string },
    args: Record<string, string>
  ) {
    const chat = await page.request.post('/api/chat', {
      data: {
        question: `/tool memory_propose_global ${JSON.stringify(args)}`,
        route_context: {
          surface: 'workspace_detail',
          workspace_id: workspace.id,
          page_path: `/workspaces/${workspace.folder_slug}`
        }
      }
    });
    expect(chat.status(), await chat.text()).toBe(200);
    return chat.text();
  }

  test.beforeEach(async ({ page }) => {
    await skipOnboarding(page);
  });

  test('without an assistant, the tool and the Memory tab explain how to get global memory', async ({
    page
  }) => {
    const album = await projectWorkspace(page, 'Album Three');
    const reply = await proposeFromWorkspace(page, album, {
      text: proposal,
      category: 'how_you_work'
    });
    expect(reply).toContain(
      'Global memory needs a personal assistant with a Personal HQ. Set one up from Home.'
    );

    const added = await page.request.post(`/api/workspaces/${album.id}/memory/entries`, {
      data: { text: 'Album masters at -14 LUFS.', type: 'fact' }
    });
    expect(added.status(), await added.text()).toBe(200);
    const pane = await openMemoryTab(page, album.folder_slug);
    const entry = pane
      .locator('.workspace-detail-memory-entry')
      .filter({ hasText: 'Album masters at -14 LUFS.' });
    const promote = entry.getByRole('button', { name: 'Promote to global' });
    await expect(promote).toHaveAttribute('aria-disabled', 'true');
    // aria-disabled, not disabled: it stays focusable and clickable so its
    // explanation can be opened. Playwright treats it as not enabled.
    await promote.click({ force: true });
    await expect(pane.locator('.workspace-detail-memory-notice')).toContainText(
      'Global memory needs a personal assistant with a Personal HQ. Set one up from Home.'
    );
    await page.screenshot({ path: 'test-results/global-memory-no-hq.png', fullPage: true });
  });

  test('an agent in a project workspace proposes a fact that the Profile attributes and approves', async ({
    page
  }) => {
    await ensurePersonalHQ(page);
    const album = await projectWorkspace(page, 'Album Three');
    // The forged workspace and agent fields must have no effect.
    const reply = await proposeFromWorkspace(page, album, {
      text: proposal,
      category: 'how_you_work',
      workspace_id: 'forged',
      agent: 'Forged'
    });
    expect(reply).toContain('proposed');
    const pending = (
      await (await page.request.get('/api/personal-assistant/knowledge')).json()
    ).items.find((item: { text?: string }) => item.text === proposal);
    expect(pending?.source?.workspace_id).toBe(album.id);
    // Attribution is the workspace's own entry agent, never the forged one.
    const agent = pending?.source?.agent as string;
    expect(agent).toBe(projectAgent);

    await page.goto('/profile#personalHQKnowledge');
    await expect(page.locator('#personalHQKnowledge')).toHaveAttribute('aria-busy', 'false');
    await expect(page.locator('#reviewedKnowledgeTitle')).toHaveText('Global memory');
    await expect(page.locator('#personalHQKnowledge .profile-knowledge-subtitle')).toContainText(
      'Facts your assistant knows everywhere.'
    );
    await expect(
      page.locator('.profile-context-row').filter({ hasText: 'Global memory' })
    ).toContainText('Read by your assistant on every page.');
    const card = page
      .locator('#reviewedKnowledgeQueue .reviewed-knowledge-item')
      .filter({ hasText: proposal });
    const chip = card.locator('.reviewed-knowledge-source');
    await expect(chip).toContainText(`Proposed by ${agent} in Album Three`);
    await expect(chip).toContainText('from chat');
    await expect(chip).not.toContainText('Forged');
    await expect(chip.getByRole('link', { name: 'Album Three' })).toHaveAttribute(
      'href',
      `/workspaces/${album.folder_slug}`
    );
    await page.screenshot({ path: 'test-results/global-memory-queue-chip.png', fullPage: true });

    page.on('dialog', dialog => dialog.accept());
    await card.getByRole('button', { name: 'Approve this wording' }).click();
    const fact = page
      .locator('#reviewedKnowledgeFacts .reviewed-knowledge-item')
      .filter({ hasText: proposal });
    await expect(fact.locator('.reviewed-knowledge-source')).toContainText(
      `Proposed by ${agent} in Album Three`
    );
    const review = await (await page.request.get('/api/personal-assistant/knowledge')).json();
    const approved = review.items.find((item: { text?: string }) => item.text === proposal);
    expect(approved?.state).toBe('approved');
    expect(approved?.source?.kind).toBe('workspace_agent');
    // The workspace's own memory never received the proposal.
    const albumMemory = await (await page.request.get(`/api/workspaces/${album.id}/memory`)).json();
    expect(albumMemory.entries.some((entry: { text: string }) => entry.text === proposal)).toBe(
      false
    );
  });

  test('a fact is copied into a workspace, listed as a copy, and the copy removed', async ({
    page
  }) => {
    await ensurePersonalHQ(page);
    const album = await projectWorkspace(page, 'Album Three');
    await page.goto('/profile#personalHQKnowledge');
    await expect(page.locator('#personalHQKnowledge')).toHaveAttribute('aria-busy', 'false');
    const fact = page
      .locator('#reviewedKnowledgeFacts .reviewed-knowledge-item')
      .filter({ hasText: proposal });
    await fact.getByRole('button', { name: 'Copy to workspace…' }).click();
    const picker = fact.locator('.reviewed-knowledge-copy select');
    await expect(picker.locator('option', { hasText: 'Album Three' })).toHaveCount(1);
    await expect(picker.locator('option', { hasText: 'My HQ' })).toHaveCount(0);
    await picker.selectOption({ label: 'Album Three' });
    await fact.getByRole('button', { name: 'Copy', exact: true }).click();
    await expect(fact.locator('.reviewed-knowledge-copy')).toContainText('Copied into Album Three');
    await page.screenshot({ path: 'test-results/global-memory-copied.png', fullPage: true });

    const memory = await (await page.request.get(`/api/workspaces/${album.id}/memory`)).json();
    const copied = memory.entries.find((entry: { text: string }) => entry.text === proposal);
    expect(copied?.provenance).toMatch(/^global:/);

    await fact.getByRole('button', { name: 'Edit', exact: true }).click();
    const copies = fact.locator('.reviewed-knowledge-copies-box');
    await expect(copies).toContainText('Copies in: Album Three');
    await copies.getByRole('button', { name: 'Remove copy' }).click();
    await expect(copies).toContainText('No workspace holds a copy of this fact.');
    const after = await (await page.request.get(`/api/workspaces/${album.id}/memory`)).json();
    expect(after.entries.some((entry: { text: string }) => entry.text === proposal)).toBe(false);
  });

  test('an entry is promoted from the Memory tab and the HQ shows reviewed lines read-only', async ({
    page
  }) => {
    await ensurePersonalHQ(page);
    const album = await projectWorkspace(page, 'Album Three');
    const added = await page.request.post(`/api/workspaces/${album.id}/memory/entries`, {
      data: { text: 'I reference mixes at -14 LUFS.', type: 'feedback' }
    });
    expect(added.status(), await added.text()).toBe(200);

    const pane = await openMemoryTab(page, album.folder_slug);
    await expect(pane).toContainText('Promote an entry to global memory');
    const entry = pane
      .locator('.workspace-detail-memory-entry')
      .filter({ hasText: 'I reference mixes at -14 LUFS.' });
    await entry.getByRole('button', { name: 'Promote to global' }).click();
    const form = pane.locator('.workspace-detail-memory-entry.is-promoting');
    await expect(form.locator('select')).toHaveValue('how_you_work');
    await form.locator('input').fill('I reference my mixes at -14 LUFS.');
    await form.getByRole('button', { name: 'Promote', exact: true }).click();
    await expect(pane.locator('.workspace-detail-memory-notice')).toContainText(
      'Promoted to global memory.'
    );
    // Promote copies; the workspace entry stays.
    await expect(pane).toContainText('I reference mixes at -14 LUFS.');
    await page.screenshot({ path: 'test-results/global-memory-promoted.png', fullPage: true });

    const hq = (await (await page.request.get('/api/personal-hq/status')).json()).status.workspace;
    const hqPane = await openMemoryTab(page, hq.folder_slug);
    const reviewed = hqPane
      .locator('.workspace-detail-memory-entry.is-global')
      .filter({ hasText: 'I reference my mixes at -14 LUFS.' });
    await expect(reviewed).toContainText('Global memory (reviewed)');
    await expect(reviewed.getByRole('link', { name: 'Review on Profile' })).toHaveAttribute(
      'href',
      '/profile#personalHQKnowledge'
    );
    await expect(reviewed.getByRole('button')).toHaveCount(0);
  });
});
