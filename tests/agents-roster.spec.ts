import { test, expect } from '@playwright/test';

// Happy-path regression for the game-inspired Agents page (roster + stage).
// Assumes a running server; create/cleanup a throwaway agent via the API so the
// test is self-contained and order-independent.
const baseUrl = process.env.BASE_URL || 'http://localhost:8765';

test.describe('Agents roster', () => {
  test('browse, select, edit, assign workspace, and delete', async ({ page, request }) => {
    const name = `PW Roster ${Date.now()}`;

    const create = await request.post(`${baseUrl}/api/agents`, {
      data: { name, type: 'tool-calling', model: 'gpt-4o-mini' },
    });
    expect(create.ok()).toBeTruthy();

    try {
      // Keep onboarding out of the way and pin a theme for determinism.
      await page.addInitScript(() => window.localStorage.setItem('ori-theme', 'dark'));
      await page.route('**/api/onboarding/status', route =>
        route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({ needs_onboarding: false, completed: true }),
        })
      );

      // Roster is the default Agents view; deep-link straight to our agent.
      await page.goto(`${baseUrl}/agents?agent=${encodeURIComponent(name)}`, {
        waitUntil: 'domcontentloaded',
      });

      await expect(page.locator('#rosterList')).toBeVisible();
      await expect(page.locator('#stageName')).toHaveText(name);

      // Overview edit → save → persisted.
      const desc = page.locator('#ov-description');
      await expect(desc).toBeVisible();
      await desc.fill('Edited by Playwright.');
      const save = page.locator('#savebar-overview [data-role="save"]');
      await expect(save).toBeEnabled();
      await save.click();
      await expect
        .poll(async () => {
          const r = await request.get(`${baseUrl}/api/agents/${encodeURIComponent(name)}/detail`);
          return (await r.json()).metadata?.description;
        })
        .toBe('Edited by Playwright.');

      // Prompt tab lazy-loads an editable textarea.
      await page.locator('#tab-prompt').click();
      await expect(page.locator('#pr-prompt')).toBeVisible();

      // Workspaces tab renders the editable assignment list.
      await page.locator('#tab-workspaces').click();
      await expect(page.locator('#panel-workspaces')).toBeVisible();

      // Delete via the stage button (auto-accept the confirm dialog).
      page.once('dialog', dialog => dialog.accept());
      await page.locator('#stageDelete').click();
      await expect
        .poll(async () => {
          const r = await request.get(`${baseUrl}/api/agents/${encodeURIComponent(name)}/detail`);
          return r.status();
        })
        .toBe(404);
    } finally {
      // Best-effort cleanup if the test bailed before the delete step.
      await request.delete(`${baseUrl}/api/agents?name=${encodeURIComponent(name)}`).catch(() => undefined);
    }
  });

  test('multi-select: independent focus/check, select all, range, hidden count, clear, reload', async ({
    page,
    request,
  }) => {
    // A unique prefix so a search narrows the roster to exactly our test agents.
    const prefix = `PWMulti${Date.now()}`;
    const names = [`${prefix} Alpha`, `${prefix} Bravo`, `${prefix} Charlie`];

    for (const n of names) {
      const r = await request.post(`${baseUrl}/api/agents`, {
        data: { name: n, type: 'tool-calling', model: 'gpt-4o-mini' },
      });
      expect(r.ok()).toBeTruthy();
    }

    try {
      await page.addInitScript(() => window.localStorage.setItem('ori-theme', 'dark'));
      await page.route('**/api/onboarding/status', route =>
        route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({ needs_onboarding: false, completed: true }),
        })
      );

      await page.goto(`${baseUrl}/agents`, { waitUntil: 'domcontentloaded' });
      await expect(page.locator('#rosterList')).toBeVisible();

      // Narrow the roster to our three agents.
      await page.locator('#rosterSearch').fill(prefix);
      const cards = page.locator('.roster-card');
      await expect(cards).toHaveCount(3);

      const alpha = page.locator(`.roster-card[data-name="${names[0]}"]`);
      const bravo = page.locator(`.roster-card[data-name="${names[1]}"]`);
      const charlie = page.locator(`.roster-card[data-name="${names[2]}"]`);

      // Checking Alpha changes only bulk state — it does NOT focus Alpha.
      await alpha.locator('.roster-card__check').check();
      await expect(alpha).toHaveClass(/is-checked/);
      await expect(page.locator('#bulkBar')).toBeVisible();
      await expect(page.locator('#bulkCount')).toHaveText('1 selected');

      // Focusing Bravo (open button) drives the stage but leaves Alpha checked
      // and does not check Bravo.
      await bravo.locator('.roster-card__open').click();
      await expect(page.locator('#stageName')).toHaveText(names[1]);
      await expect(alpha).toHaveClass(/is-checked/);
      await expect(bravo).not.toHaveClass(/is-checked/);

      // Select all visible → all three checked.
      await page.locator('#rosterClearSelection').click();
      await page.locator('#rosterSelectAll').click();
      await expect(page.locator('#bulkCount')).toHaveText('3 selected');
      await expect(charlie).toHaveClass(/is-checked/);

      // Narrow the filter so two checked agents become hidden.
      await page.locator('#rosterSearch').fill(`${prefix} Alpha`);
      await expect(cards).toHaveCount(1);
      await expect(page.locator('#bulkCount')).toHaveText('3 selected · 2 hidden by filters');

      // Clear selection hides the bar.
      await page.locator('#rosterClearSelection').click();
      await expect(page.locator('#bulkBar')).toBeHidden();

      // A reload starts with zero checked agents.
      await page.locator('#rosterSelectAll').click();
      await expect(page.locator('#bulkBar')).toBeVisible();
      await page.reload({ waitUntil: 'domcontentloaded' });
      await expect(page.locator('#rosterList')).toBeVisible();
      await expect(page.locator('#bulkBar')).toBeHidden();
    } finally {
      for (const n of names) {
        await request.delete(`${baseUrl}/api/agents?name=${encodeURIComponent(n)}`).catch(() => undefined);
      }
    }
  });

  test('bulk delete: mixed selection deletes eligible and reports skipped', async ({ page, request }) => {
    const prefix = `PWDel${Date.now()}`;
    // Two plain (deletable) agents + one attached agent (skipped).
    const loose1 = `${prefix} Loose1`;
    const loose2 = `${prefix} Loose2`;
    const attached = `${prefix} Attached`;
    const names = [loose1, loose2, attached];
    for (const n of names) {
      const r = await request.post(`${baseUrl}/api/agents`, {
        data: { name: n, type: 'tool-calling', model: 'gpt-4o-mini' },
      });
      expect(r.ok()).toBeTruthy();
    }

    // Attach one agent to a fresh workspace so it is protected from deletion.
    let wsId = '';
    const wsResp = await request.post(`${baseUrl}/api/workspaces`, {
      data: { name: `${prefix} WS`, entry_agent_name: attached },
    });
    if (wsResp.ok()) {
      const wsJson = await wsResp.json();
      wsId = wsJson?.folder?.id || wsJson?.id || '';
    }

    try {
      await page.addInitScript(() => window.localStorage.setItem('ori-theme', 'dark'));
      await page.route('**/api/onboarding/status', route =>
        route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({ needs_onboarding: false, completed: true }),
        })
      );

      await page.goto(`${baseUrl}/agents`, { waitUntil: 'domcontentloaded' });
      await page.locator('#rosterSearch').fill(prefix);
      await expect(page.locator('.roster-card')).toHaveCount(3);

      // Check all three, open the confirmation dialog.
      await page.locator('#rosterSelectAll').click();
      await page.locator('#bulkDelete').click();
      const dialog = page.locator('#bulkDeleteDialog');
      await expect(dialog).toBeVisible();
      // Two eligible → button names the eligible count.
      await expect(page.locator('#bulkDeleteConfirm')).toHaveText(/Delete 2 agents/);
      await expect(page.locator('#bulkDeleteBody')).toContainText('Will be skipped');

      await page.locator('#bulkDeleteConfirm').click();

      // Result surface reports the skipped agent and stays inspectable.
      await expect(page.locator('#bulkResult')).toBeVisible();
      await expect(page.locator('#bulkResultSummary')).toContainText('2 deleted');
      await expect(page.locator('#bulkResultList')).toContainText(attached);

      // Server persistence: loose agents gone (404), attached survives (200).
      await expect
        .poll(async () => (await request.get(`${baseUrl}/api/agents/${encodeURIComponent(loose1)}/detail`)).status())
        .toBe(404);
      await expect
        .poll(async () => (await request.get(`${baseUrl}/api/agents/${encodeURIComponent(attached)}/detail`)).status())
        .toBe(200);
    } finally {
      if (wsId) await request.delete(`${baseUrl}/api/workspaces/${encodeURIComponent(wsId)}`).catch(() => undefined);
      for (const n of names) {
        await request.delete(`${baseUrl}/api/agents?name=${encodeURIComponent(n)}`).catch(() => undefined);
      }
    }
  });

  test('bulk metadata: add tags and favorite across a selection', async ({ page, request }) => {
    const prefix = `PWMeta${Date.now()}`;
    const names = [`${prefix} One`, `${prefix} Two`];
    for (const n of names) {
      const r = await request.post(`${baseUrl}/api/agents`, {
        data: { name: n, type: 'tool-calling', model: 'gpt-4o-mini', tags: ['keep'] },
      });
      expect(r.ok()).toBeTruthy();
    }

    try {
      await page.addInitScript(() => window.localStorage.setItem('ori-theme', 'dark'));
      await page.route('**/api/onboarding/status', route =>
        route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({ needs_onboarding: false, completed: true }),
        })
      );

      await page.goto(`${baseUrl}/agents`, { waitUntil: 'domcontentloaded' });
      await page.locator('#rosterSearch').fill(prefix);
      await expect(page.locator('.roster-card')).toHaveCount(2);

      // Select both, favorite them.
      await page.locator('#rosterSelectAll').click();
      await page.locator('#bulkFavorite').click();
      await expect
        .poll(async () => {
          const r = await request.get(`${baseUrl}/api/agents/${encodeURIComponent(names[0])}/detail`);
          return (await r.json()).metadata?.favorite;
        })
        .toBe(true);

      // Re-select (reload cleared selection) and add a tag.
      await page.locator('#rosterSearch').fill(prefix);
      await page.locator('#rosterSelectAll').click();
      await page.locator('#bulkAddTags').click();
      await expect(page.locator('#bulkTagsDialog')).toBeVisible();
      // Type into the shared tag input and commit with Enter.
      const tagField = page.locator('#bulkTagsInputHost .tag-input-field');
      await tagField.fill('content');
      await tagField.press('Enter');
      await page.locator('#bulkTagsConfirm').click();

      await expect
        .poll(async () => {
          const r = await request.get(`${baseUrl}/api/agents/${encodeURIComponent(names[1])}/detail`);
          const tags = (await r.json()).metadata?.tags || [];
          return tags.slice().sort().join(',');
        })
        .toBe('content,keep');
    } finally {
      for (const n of names) {
        await request.delete(`${baseUrl}/api/agents?name=${encodeURIComponent(n)}`).catch(() => undefined);
      }
    }
  });

  test('single-agent overview: edit tags and favorite persist', async ({ page, request }) => {
    const name = `PWOv ${Date.now()}`;
    const create = await request.post(`${baseUrl}/api/agents`, {
      data: { name, type: 'tool-calling', model: 'gpt-4o-mini' },
    });
    expect(create.ok()).toBeTruthy();

    try {
      await page.addInitScript(() => window.localStorage.setItem('ori-theme', 'dark'));
      await page.route('**/api/onboarding/status', route =>
        route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({ needs_onboarding: false, completed: true }),
        })
      );

      await page.goto(`${baseUrl}/agents?agent=${encodeURIComponent(name)}`, { waitUntil: 'domcontentloaded' });
      await expect(page.locator('#stageName')).toHaveText(name);

      // Favorite + add a tag via the Overview form.
      await page.locator('#ov-favorite').check();
      const tagField = page.locator('#ov-tags-host .tag-input-field');
      await tagField.fill('research');
      await tagField.press('Enter');
      const save = page.locator('#savebar-overview [data-role="save"]');
      await expect(save).toBeEnabled();
      await save.click();

      await expect
        .poll(async () => {
          const r = await request.get(`${baseUrl}/api/agents/${encodeURIComponent(name)}/detail`);
          const d = await r.json();
          return `${d.metadata?.favorite}:${(d.metadata?.tags || []).join(',')}`;
        })
        .toBe('true:research');
    } finally {
      await request.delete(`${baseUrl}/api/agents?name=${encodeURIComponent(name)}`).catch(() => undefined);
    }
  });

  test('filters + URL: tag filter narrows roster, survives reload, excludes checked', async ({ page, request }) => {
    const prefix = `PWFil${Date.now()}`;
    const tagged = `${prefix} Tagged`;
    const plain = `${prefix} Plain`;
    const r1 = await request.post(`${baseUrl}/api/agents`, {
      data: { name: tagged, type: 'tool-calling', model: 'gpt-4o-mini', tags: [`${prefix}tag`] },
    });
    const r2 = await request.post(`${baseUrl}/api/agents`, {
      data: { name: plain, type: 'tool-calling', model: 'gpt-4o-mini' },
    });
    expect(r1.ok() && r2.ok()).toBeTruthy();

    try {
      await page.addInitScript(() => window.localStorage.setItem('ori-theme', 'dark'));
      await page.route('**/api/onboarding/status', route =>
        route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({ needs_onboarding: false, completed: true }),
        })
      );

      await page.goto(`${baseUrl}/agents`, { waitUntil: 'domcontentloaded' });
      await page.locator('#rosterSearch').fill(prefix);
      await expect(page.locator('.roster-card')).toHaveCount(2);

      // Check both, then apply a tag filter that hides one → 1 hidden checked.
      await page.locator('#rosterSelectAll').click();
      await page.locator('#filterTag').selectOption(`${prefix}tag`);
      await expect(page.locator('.roster-card')).toHaveCount(1);
      await expect(page.locator(`.roster-card[data-name="${tagged}"]`)).toBeVisible();
      await expect(page.locator('#bulkCount')).toHaveText(/1 hidden by filters/);

      // The tag filter is reflected in the URL.
      await expect.poll(() => new URL(page.url()).searchParams.get('tag')).toBe(`${prefix}tag`);

      // Reload restores the filter (still 1 shown) but clears checked selection.
      await page.reload({ waitUntil: 'domcontentloaded' });
      await expect(page.locator('#filterTag')).toHaveValue(`${prefix}tag`);
      await page.locator('#rosterSearch').fill(prefix);
      await expect(page.locator('.roster-card')).toHaveCount(1);
      await expect(page.locator('#bulkBar')).toBeHidden();

      // Clear filters restores both.
      await page.locator('#clearFilters').click();
      await expect(page.locator('.roster-card')).toHaveCount(2);
    } finally {
      await request.delete(`${baseUrl}/api/agents?name=${encodeURIComponent(tagged)}`).catch(() => undefined);
      await request.delete(`${baseUrl}/api/agents?name=${encodeURIComponent(plain)}`).catch(() => undefined);
    }
  });

  test('edge cases: no-eligible delete disabled, focused-agent deletion falls back', async ({ page, request }) => {
    const prefix = `PWEdge${Date.now()}`;
    const a = `${prefix} A`;
    const b = `${prefix} B`;
    for (const n of [a, b]) {
      const r = await request.post(`${baseUrl}/api/agents`, {
        data: { name: n, type: 'tool-calling', model: 'gpt-4o-mini' },
      });
      expect(r.ok()).toBeTruthy();
    }

    try {
      await page.addInitScript(() => window.localStorage.setItem('ori-theme', 'dark'));
      await page.route('**/api/onboarding/status', route =>
        route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({ needs_onboarding: false, completed: true }),
        })
      );

      // No-eligible delete: select only Ori (protected) → Delete button disabled.
      await page.goto(`${baseUrl}/agents`, { waitUntil: 'domcontentloaded' });
      await page.locator('#rosterSearch').fill('Ori');
      const ori = page.locator('.roster-card[data-name="Ori"]');
      await expect(ori).toHaveCount(1);
      await ori.locator('.roster-card__check').check();
      await page.locator('#bulkDelete').click();
      await expect(page.locator('#bulkDeleteConfirm')).toBeDisabled();
      await expect(page.locator('#bulkDeleteBody')).toContainText('None of the selected agents can be deleted');
      await page.locator('#bulkDeleteCancel').click();

      // Focused-agent deletion: focus A, select A, delete → stage falls back.
      await page.locator('#rosterSearch').fill(prefix);
      await page.locator(`.roster-card[data-name="${a}"] .roster-card__open`).click();
      await expect(page.locator('#stageName')).toHaveText(a);
      await page.locator(`.roster-card[data-name="${a}"] .roster-card__check`).check();
      await page.locator('#bulkDelete').click();
      await expect(page.locator('#bulkDeleteConfirm')).toHaveText(/Delete 1 agent/);
      await page.locator('#bulkDeleteConfirm').click();

      // A is gone from the server; the stage no longer shows A.
      await expect
        .poll(async () => (await request.get(`${baseUrl}/api/agents/${encodeURIComponent(a)}/detail`)).status())
        .toBe(404);
      await expect(page.locator('#stageName')).not.toHaveText(a);
    } finally {
      for (const n of [a, b]) {
        await request.delete(`${baseUrl}/api/agents?name=${encodeURIComponent(n)}`).catch(() => undefined);
      }
    }
  });
});
