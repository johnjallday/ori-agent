import { test, expect } from '@playwright/test';
import { installLocalCdn } from './helpers/offline-cdn';

// Happy-path regression for the game-inspired Agents page (roster + stage).
// Assumes a running server; create/cleanup a throwaway agent via the API so the
// test is self-contained and order-independent.
const baseUrl = process.env.PLAYWRIGHT_BASE_URL || process.env.BASE_URL || 'http://localhost:8765';

// Serve Bootstrap from node_modules. Without it the shared vault modal renders
// unstyled and its backdrop intercepts every click on the page, which fails
// these specs for a reason that has nothing to do with the Agents surface.
test.beforeEach(async ({ page }) => {
  await installLocalCdn(page);
});

test.describe('Agents roster', () => {
  test('browse, select, edit, assign workspace, and delete', async ({ page, request }) => {
    const name = `PW Roster ${Date.now()}`;

    const create = await request.post(`${baseUrl}/api/agents`, {
      data: { name, type: 'tool-calling', model: 'gpt-4o-mini' }
    });
    expect(create.ok()).toBeTruthy();

    try {
      // Keep onboarding out of the way and pin a theme for determinism.
      await page.addInitScript(() => window.localStorage.setItem('ori-theme', 'dark'));
      await page.route('**/api/onboarding/status', route =>
        route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({ needs_onboarding: false, completed: true })
        })
      );

      // Roster is the default Agents view; deep-link straight to our agent.
      await page.goto(`${baseUrl}/agents?agent=${encodeURIComponent(name)}`, {
        waitUntil: 'domcontentloaded'
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
      await request
        .delete(`${baseUrl}/api/agents?name=${encodeURIComponent(name)}`)
        .catch(() => undefined);
    }
  });

  test('multi-select: independent focus/check, select all, range, hidden count, clear, reload', async ({
    page,
    request
  }) => {
    // A unique prefix so a search narrows the roster to exactly our test agents.
    const prefix = `PWMulti${Date.now()}`;
    const names = [`${prefix} Alpha`, `${prefix} Bravo`, `${prefix} Charlie`];

    for (const n of names) {
      const r = await request.post(`${baseUrl}/api/agents`, {
        data: { name: n, type: 'tool-calling', model: 'gpt-4o-mini' }
      });
      expect(r.ok()).toBeTruthy();
    }

    try {
      await page.addInitScript(() => window.localStorage.setItem('ori-theme', 'dark'));
      await page.route('**/api/onboarding/status', route =>
        route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({ needs_onboarding: false, completed: true })
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
        await request
          .delete(`${baseUrl}/api/agents?name=${encodeURIComponent(n)}`)
          .catch(() => undefined);
      }
    }
  });

  test('bulk delete: mixed selection deletes eligible and reports skipped', async ({
    page,
    request
  }) => {
    const prefix = `PWDel${Date.now()}`;
    // Two plain (deletable) agents + one attached agent (skipped).
    const loose1 = `${prefix} Loose1`;
    const loose2 = `${prefix} Loose2`;
    const attached = `${prefix} Attached`;
    const names = [loose1, loose2, attached];
    for (const n of names) {
      const r = await request.post(`${baseUrl}/api/agents`, {
        data: { name: n, type: 'tool-calling', model: 'gpt-4o-mini' }
      });
      expect(r.ok()).toBeTruthy();
    }

    // Attach one agent to a fresh workspace so it is protected from deletion.
    let wsId = '';
    const wsResp = await request.post(`${baseUrl}/api/workspaces`, {
      data: { name: `${prefix} WS`, entry_agent_name: attached }
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
          body: JSON.stringify({ needs_onboarding: false, completed: true })
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
        .poll(async () =>
          (await request.get(`${baseUrl}/api/agents/${encodeURIComponent(loose1)}/detail`)).status()
        )
        .toBe(404);
      await expect
        .poll(async () =>
          (
            await request.get(`${baseUrl}/api/agents/${encodeURIComponent(attached)}/detail`)
          ).status()
        )
        .toBe(200);
    } finally {
      if (wsId)
        await request
          .delete(`${baseUrl}/api/workspaces/${encodeURIComponent(wsId)}`)
          .catch(() => undefined);
      for (const n of names) {
        await request
          .delete(`${baseUrl}/api/agents?name=${encodeURIComponent(n)}`)
          .catch(() => undefined);
      }
    }
  });

  test('bulk metadata: add tags and favorite across a selection', async ({ page, request }) => {
    const prefix = `PWMeta${Date.now()}`;
    const names = [`${prefix} One`, `${prefix} Two`];
    for (const n of names) {
      const r = await request.post(`${baseUrl}/api/agents`, {
        data: { name: n, type: 'tool-calling', model: 'gpt-4o-mini', tags: ['keep'] }
      });
      expect(r.ok()).toBeTruthy();
    }

    try {
      await page.addInitScript(() => window.localStorage.setItem('ori-theme', 'dark'));
      await page.route('**/api/onboarding/status', route =>
        route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({ needs_onboarding: false, completed: true })
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
          const r = await request.get(
            `${baseUrl}/api/agents/${encodeURIComponent(names[0])}/detail`
          );
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
          const r = await request.get(
            `${baseUrl}/api/agents/${encodeURIComponent(names[1])}/detail`
          );
          const tags = (await r.json()).metadata?.tags || [];
          return tags.slice().sort().join(',');
        })
        .toBe('content,keep');
    } finally {
      for (const n of names) {
        await request
          .delete(`${baseUrl}/api/agents?name=${encodeURIComponent(n)}`)
          .catch(() => undefined);
      }
    }
  });

  test('single-agent overview: edit tags and favorite persist', async ({ page, request }) => {
    const name = `PWOv ${Date.now()}`;
    const create = await request.post(`${baseUrl}/api/agents`, {
      data: { name, type: 'tool-calling', model: 'gpt-4o-mini' }
    });
    expect(create.ok()).toBeTruthy();

    try {
      await page.addInitScript(() => window.localStorage.setItem('ori-theme', 'dark'));
      await page.route('**/api/onboarding/status', route =>
        route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({ needs_onboarding: false, completed: true })
        })
      );

      await page.goto(`${baseUrl}/agents?agent=${encodeURIComponent(name)}`, {
        waitUntil: 'domcontentloaded'
      });
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
      await request
        .delete(`${baseUrl}/api/agents?name=${encodeURIComponent(name)}`)
        .catch(() => undefined);
    }
  });

  test('filters + URL: tag filter narrows roster, survives reload, excludes checked', async ({
    page,
    request
  }) => {
    const prefix = `PWFil${Date.now()}`;
    const tagged = `${prefix} Tagged`;
    const plain = `${prefix} Plain`;
    const r1 = await request.post(`${baseUrl}/api/agents`, {
      data: { name: tagged, type: 'tool-calling', model: 'gpt-4o-mini', tags: [`${prefix}tag`] }
    });
    const r2 = await request.post(`${baseUrl}/api/agents`, {
      data: { name: plain, type: 'tool-calling', model: 'gpt-4o-mini' }
    });
    expect(r1.ok() && r2.ok()).toBeTruthy();

    try {
      await page.addInitScript(() => window.localStorage.setItem('ori-theme', 'dark'));
      await page.route('**/api/onboarding/status', route =>
        route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({ needs_onboarding: false, completed: true })
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
      await request
        .delete(`${baseUrl}/api/agents?name=${encodeURIComponent(tagged)}`)
        .catch(() => undefined);
      await request
        .delete(`${baseUrl}/api/agents?name=${encodeURIComponent(plain)}`)
        .catch(() => undefined);
    }
  });

  test('edge cases: no-eligible delete disabled, focused-agent deletion falls back', async ({
    page,
    request
  }) => {
    const prefix = `PWEdge${Date.now()}`;
    const a = `${prefix} A`;
    const b = `${prefix} B`;
    for (const n of [a, b]) {
      const r = await request.post(`${baseUrl}/api/agents`, {
        data: { name: n, type: 'tool-calling', model: 'gpt-4o-mini' }
      });
      expect(r.ok()).toBeTruthy();
    }

    try {
      await page.addInitScript(() => window.localStorage.setItem('ori-theme', 'dark'));
      await page.route('**/api/onboarding/status', route =>
        route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({ needs_onboarding: false, completed: true })
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
      await expect(page.locator('#bulkDeleteBody')).toContainText(
        'None of the selected agents can be deleted'
      );
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
        .poll(async () =>
          (await request.get(`${baseUrl}/api/agents/${encodeURIComponent(a)}/detail`)).status()
        )
        .toBe(404);
      await expect(page.locator('#stageName')).not.toHaveText(a);
    } finally {
      for (const n of [a, b]) {
        await request
          .delete(`${baseUrl}/api/agents?name=${encodeURIComponent(n)}`)
          .catch(() => undefined);
      }
    }
  });
});

// Gallery slice: every visible card value must come from the dashboard list
// response, and the avatar must follow Avatar Identity v1 (PRD FR2–FR24,
// FR67–FR78, FR95–FR98, FR102).
test.describe('Agents gallery', () => {
  async function openAgents(page) {
    await page.addInitScript(() => window.localStorage.setItem('ori-theme', 'dark'));
    await page.route('**/api/onboarding/status', route =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ needs_onboarding: false, completed: true })
      })
    );
    await page.goto(`${baseUrl}/agents`, { waitUntil: 'domcontentloaded' });
    await expect(page.locator('#rosterList')).toBeVisible();
    await expect(page.locator('.roster-card').first()).toBeVisible();
  }

  function card(page, name) {
    return page.locator(`.roster-card[data-name="${name}"]`);
  }

  test('header metrics and card content are computed from the real list response', async ({
    page,
    request
  }) => {
    const prefix = `PWGal${Date.now()}`;
    const rich = `${prefix} Rich`;
    const bare = `${prefix} Bare`;
    await request.post(`${baseUrl}/api/agents`, {
      data: {
        name: rich,
        type: 'tool-calling',
        role: 'researcher',
        model: 'gpt-4o-mini',
        description: 'Finds primary sources and leaves an evidence trail.',
        tags: ['research']
      }
    });
    await request.post(`${baseUrl}/api/agents`, { data: { name: bare, type: 'general' } });

    try {
      await openAgents(page);

      // Summary counts are derived from the same list the cards render.
      const list = await (
        await request.get(`${baseUrl}/api/agents/dashboard/list?sort_by=name&order=asc`)
      ).json();
      const agents = list.agents || [];
      const disabled = agents.filter(a => String(a.status).toLowerCase() === 'disabled').length;
      const needs = agents.filter(
        a =>
          String(a.status).toLowerCase() !== 'disabled' &&
          (String(a.status).toLowerCase() === 'error' || !String(a.model || '').trim())
      ).length;
      await expect(page.locator('.roster-stat--total .roster-stat__value')).toHaveText(
        String(agents.length)
      );
      await expect(page.locator('.roster-stat--needs .roster-stat__value')).toHaveText(
        String(needs)
      );
      await expect(page.locator('.roster-stat--ready .roster-stat__value')).toHaveText(
        String(agents.length - needs - disabled)
      );

      // Card facts match this agent's own record, field for field.
      const record = agents.find(a => a.name === rich);
      const target = card(page, rich);
      await expect(target.locator('.agent-card__name')).toHaveText(rich);
      await expect(target.locator('.agent-card__status')).toContainText('Active');
      await expect(target.locator('.agent-card__class')).toContainText('Researcher');
      await expect(target.locator('.agent-card__class')).toContainText('Lv 0');
      await expect(target.locator('.agent-card__purpose')).toHaveText(record.metadata.description);
      await expect(target.locator('.agent-card__model')).toHaveText(record.model);

      // An agent with no description says so instead of borrowing the role's
      // tagline, and an unattached agent reads as library-only (FR20/FR22).
      const bareCard = card(page, bare);
      await expect(bareCard.locator('.agent-card__purpose')).toHaveText('No description yet');
      await expect(bareCard.locator('.agent-card__purpose')).toHaveClass(/is-missing/);
      await expect(bareCard.locator('.agent-card__pill')).toHaveText('Library only');
      await expect(bareCard.locator('.agent-card__toolbox-value')).toHaveText(
        'No capabilities listed'
      );
    } finally {
      for (const n of [rich, bare]) {
        await request
          .delete(`${baseUrl}/api/agents?name=${encodeURIComponent(n)}`)
          .catch(() => undefined);
      }
    }
  });

  test('workspace orbit shows at most two labels plus a +N overflow', async ({ page, request }) => {
    const prefix = `PWOrbit${Date.now()}`;
    const name = `${prefix} Hub`;
    await request.post(`${baseUrl}/api/agents`, {
      data: { name, type: 'tool-calling', model: 'gpt-4o-mini' }
    });

    const wsIds: string[] = [];
    for (const suffix of ['One', 'Two', 'Three']) {
      const r = await request.post(`${baseUrl}/api/workspaces`, {
        data: { name: `${prefix} ${suffix}`, entry_agent_name: name }
      });
      if (r.ok()) {
        const j = await r.json();
        wsIds.push(j?.folder?.id || j?.id || '');
      }
    }

    try {
      await openAgents(page);
      const target = card(page, name);
      const pills = target.locator('.agent-card__pill');
      // Two named workspaces plus one overflow chip — never all three.
      await expect(pills).toHaveCount(3);
      await expect(pills.nth(2)).toHaveText('+1');
      await expect(pills.nth(2)).toHaveClass(/is-more/);
    } finally {
      for (const id of wsIds.filter(Boolean)) {
        await request
          .delete(`${baseUrl}/api/workspaces/${encodeURIComponent(id)}`)
          .catch(() => undefined);
      }
      await request
        .delete(`${baseUrl}/api/agents?name=${encodeURIComponent(name)}`)
        .catch(() => undefined);
    }
  });

  test('built-in CLI agents are labeled, read-only, and given distinct system avatars', async ({
    page
  }) => {
    await openAgents(page);

    const builtIns = ['Claude Code', 'Codex', 'Gemini CLI'];
    const signatures: string[] = [];
    for (const name of builtIns) {
      const target = card(page, name);
      await expect(target).toHaveClass(/is-permanent/);
      await expect(target.locator('.agent-card__badge')).toHaveText('Built-in');
      // Their real source data still shows: CLI role and the actual model.
      await expect(target.locator('.agent-card__class')).toContainText('Cli Agent');
      await expect(target.locator('.agent-card__model')).not.toBeEmpty();
      await expect(target.locator('.agent-card__toolbox-value')).toContainText('File Operations');

      const avatar = target.locator('.agent-avatar');
      await expect(avatar).toHaveAttribute('data-aa-system', '1');
      signatures.push(
        JSON.stringify(
          await avatar.evaluate(el => ({
            motif: (el as HTMLElement).dataset.aaMotif,
            turn: (el as HTMLElement).dataset.aaTurn,
            tone: (el as HTMLElement).dataset.aaTone,
            base: (el as HTMLElement).style.getPropertyValue('--aa-base'),
            initials: el.querySelector('.agent-avatar__initials')?.textContent
          }))
        )
      );
    }
    // Recognisably system, but not interchangeable with one another (FR73).
    expect(new Set(signatures).size).toBe(builtIns.length);
  });

  test('fallback avatars are deterministic across reloads and distinct per agent', async ({
    page,
    request
  }) => {
    const prefix = `PWAva${Date.now()}`;
    const names = [`${prefix} Alpha`, `${prefix} Bravo`, `${prefix} Charlie`];
    for (const n of names) {
      await request.post(`${baseUrl}/api/agents`, {
        // Same role for all three: they must still look different (FR71).
        data: { name: n, type: 'tool-calling', role: 'researcher', model: 'gpt-4o-mini' }
      });
    }

    const read = async () => {
      const out: Record<string, string> = {};
      for (const n of names) {
        out[n] = await card(page, n)
          .locator('.agent-avatar')
          .evaluate(
            el =>
              `${(el as HTMLElement).dataset.aaMotif}|${(el as HTMLElement).dataset.aaTurn}|` +
              `${(el as HTMLElement).dataset.aaTone}|${(el as HTMLElement).style.getPropertyValue('--aa-base')}|` +
              `${el.querySelector('.agent-avatar__initials')?.textContent}`
          );
      }
      return out;
    };

    try {
      await openAgents(page);
      await page.locator('#rosterSearch').fill(prefix);
      await expect(page.locator('.roster-card')).toHaveCount(3);
      const first = await read();

      // Same three agents, same three identities after a full reload (FR69).
      await page.reload({ waitUntil: 'domcontentloaded' });
      await expect(page.locator('#rosterList')).toBeVisible();
      await page.locator('#rosterSearch').fill(prefix);
      await expect(page.locator('.roster-card')).toHaveCount(3);
      expect(await read()).toEqual(first);

      // Three same-role agents, three different signatures.
      expect(new Set(Object.values(first)).size).toBe(3);

      // Every fallback renders locally — no <img>, no remote reference (FR98).
      const imgs = await page.locator('.roster-card .agent-avatar--fallback img').count();
      expect(imgs).toBe(0);
    } finally {
      for (const n of names) {
        await request
          .delete(`${baseUrl}/api/agents?name=${encodeURIComponent(n)}`)
          .catch(() => undefined);
      }
    }
  });

  test('an uploaded avatar renders ahead of the fallback and reserves its box', async ({
    page,
    request
  }) => {
    const name = `PWImg${Date.now()}`;
    await request.post(`${baseUrl}/api/agents`, {
      data: { name, type: 'tool-calling', model: 'gpt-4o-mini' }
    });

    // Smallest valid PNG: 1x1 transparent.
    const png = Buffer.from(
      'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==',
      'base64'
    );

    try {
      const upload = await request.post(
        `${baseUrl}/api/agents/${encodeURIComponent(name)}/avatar`,
        { multipart: { avatar: { name: 'a.png', mimeType: 'image/png', buffer: png } } }
      );
      expect(upload.ok()).toBeTruthy();

      await openAgents(page);
      await page.locator('#rosterSearch').fill(name);
      const target = card(page, name);
      const avatar = target.locator('.agent-avatar');
      await expect(avatar).toHaveClass(/agent-avatar--image/);
      const img = avatar.locator('img.agent-avatar__img');
      await expect(img).toHaveAttribute('loading', 'lazy');
      await expect(img).toHaveAttribute('decoding', 'async');
      await expect(img).toHaveAttribute('alt', '');
      // The box is reserved before the bitmap decodes (FR97).
      await expect(img).toHaveAttribute('width', /\d+/);
      await expect(img).toHaveAttribute('height', /\d+/);
      await expect(avatar.locator('.agent-avatar__initials')).toHaveCount(0);

      // Removing it restores the deterministic identity (FR67/FR75).
      const removed = await request.delete(
        `${baseUrl}/api/agents/${encodeURIComponent(name)}/avatar`
      );
      expect(removed.ok()).toBeTruthy();
      await page.reload({ waitUntil: 'domcontentloaded' });
      await page.locator('#rosterSearch').fill(name);
      await expect(card(page, name).locator('.agent-avatar')).toHaveClass(/agent-avatar--fallback/);
      await expect(card(page, name).locator('.agent-avatar__initials')).toBeVisible();
    } finally {
      await request
        .delete(`${baseUrl}/api/agents?name=${encodeURIComponent(name)}`)
        .catch(() => undefined);
    }
  });

  test('a broken avatar image falls back without leaving a broken-image element', async ({
    page,
    request
  }) => {
    const name = `PWBroken${Date.now()}`;
    await request.post(`${baseUrl}/api/agents`, {
      data: { name, type: 'tool-calling', model: 'gpt-4o-mini' }
    });
    const png = Buffer.from(
      'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==',
      'base64'
    );

    try {
      await request.post(`${baseUrl}/api/agents/${encodeURIComponent(name)}/avatar`, {
        multipart: { avatar: { name: 'a.png', mimeType: 'image/png', buffer: png } }
      });

      await page.addInitScript(() => window.localStorage.setItem('ori-theme', 'dark'));
      await page.route('**/api/onboarding/status', route =>
        route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({ needs_onboarding: false, completed: true })
        })
      );
      // Make the stored avatar unreachable so the failure path runs (FR74).
      await page.route('**/avatars/**', route => route.abort());
      await page.goto(`${baseUrl}/agents`, { waitUntil: 'domcontentloaded' });
      await expect(page.locator('#rosterList')).toBeVisible();
      await page.locator('#rosterSearch').fill(name);

      const avatar = card(page, name).locator('.agent-avatar');
      await expect(avatar).toHaveClass(/agent-avatar--fallback/);
      await expect(avatar.locator('img')).toHaveCount(0);
      await expect(avatar.locator('.agent-avatar__initials')).toBeVisible();
    } finally {
      await request
        .delete(`${baseUrl}/api/agents?name=${encodeURIComponent(name)}`)
        .catch(() => undefined);
    }
  });

  test('open and check are separate sibling controls on every card', async ({ page }) => {
    await openAgents(page);
    const nesting = await page.evaluate(() => {
      const cards = [...document.querySelectorAll('.roster-card')];
      return cards.map(c => {
        const check = c.querySelector('.roster-card__check')!;
        const open = c.querySelector('.roster-card__open')!;
        return {
          name: (c as HTMLElement).dataset.name,
          checkInsideOpen: open.contains(check),
          openInsideCheck: check.contains(open),
          checkHasName: (check.closest('label')?.textContent || '').includes(
            (c as HTMLElement).dataset.name || ''
          ),
          openLabel: open.getAttribute('aria-label') || ''
        };
      });
    });
    expect(nesting.length).toBeGreaterThan(0);
    for (const n of nesting) {
      // Neither control may be nested inside the other (FR40).
      expect(n.checkInsideOpen, `${n.name} checkbox nested in open control`).toBe(false);
      expect(n.openInsideCheck, `${n.name} open control nested in checkbox`).toBe(false);
      // Both carry the agent's name for assistive technology (FR84).
      expect(n.checkHasName, `${n.name} checkbox missing its agent name`).toBe(true);
      expect(n.openLabel).toContain(n.name!);
    }
  });

  test('the collection contains no hardcoded prototype records', async ({ page, request }) => {
    await openAgents(page);
    const rendered = await page
      .locator('.roster-card')
      .evaluateAll(cards => cards.map(c => (c as HTMLElement).dataset.name));
    const list = await (
      await request.get(`${baseUrl}/api/agents/dashboard/list?sort_by=name&order=asc`)
    ).json();
    const real = (list.agents || []).map(a => a.name);

    // Exactly the server's agents — no extras invented by the page (FR2/FR102).
    expect(rendered.slice().sort()).toEqual(real.slice().sort());

    // None of the prototype's simulated concepts reach production markup.
    const body = (await page.locator('.roster-layout').innerText()).toLowerCase();
    for (const banned of ['operations lead', 'native cli tools', 'interactive concept']) {
      expect(body, `prototype fixture "${banned}" leaked into the page`).not.toContain(banned);
    }
  });

  test('collection load failure offers a retry that recovers', async ({ page }) => {
    await page.addInitScript(() => window.localStorage.setItem('ori-theme', 'dark'));
    await page.route('**/api/onboarding/status', route =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ needs_onboarding: false, completed: true })
      })
    );

    let fail = true;
    await page.route('**/api/agents/dashboard/list**', route => {
      if (fail) return route.fulfill({ status: 500, body: 'boom' });
      return route.continue();
    });

    await page.goto(`${baseUrl}/agents`, { waitUntil: 'domcontentloaded' });
    await expect(page.locator('#rosterError')).toBeVisible();
    await expect(page.locator('#rosterCount')).toHaveText('Agents could not be loaded.');

    // Retry succeeds and the collection appears (FR100).
    fail = false;
    await page.locator('#rosterRetry').click();
    await expect(page.locator('#rosterError')).toBeHidden();
    await expect(page.locator('.roster-card').first()).toBeVisible();
  });

  test('rendering the collection issues exactly one list request and no per-card detail', async ({
    page
  }) => {
    const listCalls: string[] = [];
    const detailCalls: string[] = [];
    page.on('request', r => {
      const u = r.url();
      if (u.includes('/api/agents/dashboard/list')) listCalls.push(u);
      if (/\/api\/agents\/[^/]+\/detail/.test(u)) detailCalls.push(u);
    });

    await openAgents(page);
    const cards = await page.locator('.roster-card').count();
    expect(cards).toBeGreaterThan(1);
    // One list request drives every card (FR95).
    expect(listCalls.length).toBe(1);
    // Detail is lazy: at most the one focused agent, never one per card.
    expect(detailCalls.length).toBeLessThanOrEqual(1);
  });
});
