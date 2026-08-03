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

// Characterization of the single-agent editing paths, pinned before the stage
// becomes the Inspector so the reshape cannot quietly drop one
// (PRD FR57–FR59, FR64–FR65, FR75, FR96, FR101).
test.describe('Agents single-agent editing', () => {
  async function openAgent(page, name: string) {
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
  }

  test('uploading an avatar from Overview updates the card and the hero together', async ({
    page,
    request
  }) => {
    const name = `PWAvUp${Date.now()}`;
    await request.post(`${baseUrl}/api/agents`, {
      data: { name, type: 'tool-calling', model: 'gpt-4o-mini' }
    });
    const png = Buffer.from(
      'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==',
      'base64'
    );

    try {
      await openAgent(page, name);
      const card = page.locator(`.roster-card[data-name="${name}"]`);
      await expect(card.locator('.agent-avatar')).toHaveClass(/agent-avatar--fallback/);

      await page.locator('#ov-avatar-file').setInputFiles({
        name: 'a.png',
        mimeType: 'image/png',
        buffer: png
      });
      await expect(page.locator('#ov-avatar-status')).toHaveText('Uploaded.');

      // Both surfaces must move together; a stale projection would leave the
      // hero on the old identity while the card updated.
      await expect(card.locator('.agent-avatar')).toHaveClass(/agent-avatar--image/);
      await expect(page.locator('#stageAvatar')).toHaveClass(/agent-avatar--image/);

      await page.locator('#ov-avatar-remove').click();
      await expect(page.locator('#ov-avatar-status')).toHaveText('Removed.');
      await expect(card.locator('.agent-avatar')).toHaveClass(/agent-avatar--fallback/);
      await expect(page.locator('#stageAvatar')).toHaveClass(/agent-avatar--fallback/);
    } finally {
      await request
        .delete(`${baseUrl}/api/agents?name=${encodeURIComponent(name)}`)
        .catch(() => undefined);
    }
  });

  test('a stale save offers reload-latest recovery and keeps the roster usable', async ({
    page,
    request
  }) => {
    const name = `PWStale${Date.now()}`;
    await request.post(`${baseUrl}/api/agents`, {
      data: { name, type: 'tool-calling', model: 'gpt-4o-mini' }
    });

    try {
      await openAgent(page, name);
      // Force the version-conflict response the server returns when the agent
      // changed underneath the open form.
      await page.route(`**/api/agents/${encodeURIComponent(name)}`, route => {
        if (route.request().method() !== 'PATCH') return route.continue();
        return route.fulfill({
          status: 409,
          contentType: 'application/json',
          body: JSON.stringify({ error: 'stale_agent_edit', current_version: 'abc123' })
        });
      });

      await page.locator('#ov-description').fill('Edited against a stale version.');
      await page.locator('#savebar-overview [data-role="save"]').click();

      const banner = page.locator('#panel-overview .conflict-banner');
      await expect(banner).toBeVisible();
      await expect(banner).toContainText('changed elsewhere');
      await expect(banner.locator('.conflict-banner__action')).toHaveText('Reload latest');
      // The collection stays usable while the conflict is unresolved (FR101).
      await expect(page.locator('.roster-card').first()).toBeVisible();
    } finally {
      await request
        .delete(`${baseUrl}/api/agents?name=${encodeURIComponent(name)}`)
        .catch(() => undefined);
    }
  });

  test('an unsaved edit guards a focus change and cancelling leaves it intact', async ({
    page,
    request
  }) => {
    const prefix = `PWGuard${Date.now()}`;
    const a = `${prefix} Alpha`;
    const b = `${prefix} Bravo`;
    for (const n of [a, b]) {
      await request.post(`${baseUrl}/api/agents`, {
        data: { name: n, type: 'tool-calling', model: 'gpt-4o-mini' }
      });
    }

    try {
      await openAgent(page, a);
      await page.locator('#ov-description').fill('Half-written thought.');

      // Decline the guard: focus must stay put and the edit must survive.
      page.once('dialog', d => d.dismiss());
      await page.locator(`.roster-card[data-name="${b}"] .roster-card__open`).click();
      await expect(page.locator('#stageName')).toHaveText(a);
      await expect(page.locator('#ov-description')).toHaveValue('Half-written thought.');

      // Accept it: focus moves and the edit is abandoned.
      page.once('dialog', d => d.accept());
      await page.locator(`.roster-card[data-name="${b}"] .roster-card__open`).click();
      await expect(page.locator('#stageName')).toHaveText(b);
    } finally {
      for (const n of [a, b]) {
        await request
          .delete(`${baseUrl}/api/agents?name=${encodeURIComponent(n)}`)
          .catch(() => undefined);
      }
    }
  });

  test('the New Agent panel creates an agent and focuses the new definition', async ({
    page,
    request
  }) => {
    const name = `PWCreate${Date.now()}`;
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

      // Cancelling leaves the collection untouched.
      await page.locator('#newAgentBtn').click();
      await expect(page.locator('#createPanel')).toBeVisible();
      await page.locator('#createCancel').click();
      await expect(page.locator('#createPanel')).toBeHidden();

      await page.locator('#newAgentBtn').click();
      await page.locator('#cr-name').fill(name);
      await page.locator('#cr-description').fill('Made by the create panel.');
      await page.locator('#createSubmit').click();

      // The created definition is focused and present in the collection (FR65).
      await expect(page.locator('#stageName')).toHaveText(name);
      await expect(page.locator(`.roster-card[data-name="${name}"]`)).toBeVisible();
      await expect
        .poll(async () =>
          (await request.get(`${baseUrl}/api/agents/${encodeURIComponent(name)}/detail`)).status()
        )
        .toBe(200);
    } finally {
      await request
        .delete(`${baseUrl}/api/agents?name=${encodeURIComponent(name)}`)
        .catch(() => undefined);
    }
  });

  test('a failed detail request is reported without breaking the collection', async ({
    page,
    request
  }) => {
    const name = `PWDetailErr${Date.now()}`;
    await request.post(`${baseUrl}/api/agents`, {
      data: { name, type: 'tool-calling', model: 'gpt-4o-mini' }
    });

    try {
      await page.addInitScript(() => window.localStorage.setItem('ori-theme', 'dark'));
      await page.route('**/api/onboarding/status', route =>
        route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({ needs_onboarding: false, completed: true })
        })
      );
      await page.route(`**/api/agents/${encodeURIComponent(name)}/detail`, route =>
        route.fulfill({ status: 500, body: 'boom' })
      );

      await page.goto(`${baseUrl}/agents`, { waitUntil: 'domcontentloaded' });
      await expect(page.locator('#rosterList')).toBeVisible();
      await page.locator(`.roster-card[data-name="${name}"] .roster-card__open`).click();

      // The collection keeps working even though this agent's detail failed.
      await expect(page.locator('.roster-card').first()).toBeVisible();
      await page.locator('#rosterSearch').fill(name);
      await expect(page.locator('.roster-card')).toHaveCount(1);
    } finally {
      await request
        .delete(`${baseUrl}/api/agents?name=${encodeURIComponent(name)}`)
        .catch(() => undefined);
    }
  });

  test('a failed workspace save preserves the checkbox selection and reports the error', async ({
    page,
    request
  }) => {
    const prefix = `PWWsFail${Date.now()}`;
    const name = `${prefix} Agent`;
    const wsName = `${prefix} Target`;
    await request.post(`${baseUrl}/api/agents`, {
      data: { name, type: 'tool-calling', model: 'gpt-4o-mini' }
    });
    let wsId = '';
    const ws = await request.post(`${baseUrl}/api/workspaces`, { data: { name: wsName } });
    if (ws.ok()) {
      const j = await ws.json();
      wsId = j?.folder?.id || j?.id || '';
    }
    expect(wsId).toBeTruthy();

    try {
      await page.addInitScript(() => window.localStorage.setItem('ori-theme', 'dark'));
      await page.route('**/api/onboarding/status', route =>
        route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({ needs_onboarding: false, completed: true })
        })
      );
      await page.route(`**/api/agents/${encodeURIComponent(name)}/workspaces`, route =>
        route.fulfill({
          status: 500,
          contentType: 'application/json',
          body: JSON.stringify({ message: 'Simulated workspace save failure.' })
        })
      );

      await page.goto(`${baseUrl}/agents?agent=${encodeURIComponent(name)}&tab=workspaces`, {
        waitUntil: 'domcontentloaded'
      });
      await expect(page.locator('#stageName')).toHaveText(name);
      await expect(page.locator('#panel-workspaces')).toBeVisible();

      const box = page.locator(`input[data-ws-id="${wsId}"]`);
      await box.check();
      await page.locator('#savebar-workspaces [data-role="save"]').click();

      // The failure is reported, and the user's checked box is not reverted or
      // silently discarded (PRD FR100/FR101).
      await expect(page.locator('#panel-workspaces .save-status')).toContainText(
        'Simulated workspace save failure.'
      );
      await expect(box).toBeChecked();
    } finally {
      if (wsId)
        await request
          .delete(`${baseUrl}/api/workspaces/${encodeURIComponent(wsId)}`)
          .catch(() => undefined);
      await request
        .delete(`${baseUrl}/api/agents?name=${encodeURIComponent(name)}`)
        .catch(() => undefined);
    }
  });
});

// Inspector slice: responsive open/close, the four-tab contract, and truthful
// Toolbox content (PRD FR49–FR65, FR79–FR96).
test.describe('Agents inspector', () => {
  async function open(page, query = '') {
    await page.addInitScript(() => window.localStorage.setItem('ori-theme', 'dark'));
    await page.route('**/api/onboarding/status', route =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ needs_onboarding: false, completed: true })
      })
    );
    await page.goto(`${baseUrl}/agents${query}`, { waitUntil: 'domcontentloaded' });
    await expect(page.locator('#rosterList')).toBeVisible();
  }

  test('desktop: closing reclaims collection width and keeps focus and selection', async ({
    page,
    request
  }) => {
    const prefix = `PWInsp${Date.now()}`;
    const a = `${prefix} Alpha`;
    const b = `${prefix} Bravo`;
    for (const n of [a, b]) {
      await request.post(`${baseUrl}/api/agents`, {
        data: { name: n, type: 'tool-calling', model: 'gpt-4o-mini' }
      });
    }

    try {
      await page.setViewportSize({ width: 1440, height: 950 });
      await open(page);
      await page.locator('#rosterSearch').fill(prefix);
      await expect(page.locator('.roster-card')).toHaveCount(2);

      await page.locator(`.roster-card[data-name="${a}"] .roster-card__open`).click();
      await page.locator(`.roster-card[data-name="${b}"] .roster-card__check`).check();
      await expect(page.locator('#inspector')).toBeVisible();
      const openWidth = (await page.locator('#rosterList').boundingBox())!.width;

      await page.locator('#inspectorClose').click();
      await expect(page.locator('#inspector')).toBeHidden();

      // The collection genuinely gets the space back (FR51)…
      const closedWidth = (await page.locator('#rosterList').boundingBox())!.width;
      expect(closedWidth).toBeGreaterThan(openWidth);
      // …and neither the focused agent nor the checked set is disturbed.
      await expect(page.locator(`.roster-card[data-name="${a}"]`)).toHaveClass(/is-focused/);
      await expect(page.locator('#bulkCount')).toHaveText('1 selected');

      // Reopening restores the same agent's context.
      await page.locator(`.roster-card[data-name="${a}"] .roster-card__open`).click();
      await expect(page.locator('#inspector')).toBeVisible();
      await expect(page.locator('#stageName')).toHaveText(a);
    } finally {
      for (const n of [a, b]) {
        await request
          .delete(`${baseUrl}/api/agents?name=${encodeURIComponent(n)}`)
          .catch(() => undefined);
      }
    }
  });

  test('mobile: the Inspector is a modal sheet that Escape closes and returns focus', async ({
    page,
    request
  }) => {
    const name = `PWSheet${Date.now()}`;
    await request.post(`${baseUrl}/api/agents`, {
      data: { name, type: 'tool-calling', model: 'gpt-4o-mini' }
    });

    try {
      await page.setViewportSize({ width: 640, height: 900 });
      await open(page);
      // Nothing focused yet, so the sheet must not be covering the collection.
      await expect(page.locator('#inspector')).toBeHidden();

      await page.locator('#rosterSearch').fill(name);
      const opener = page.locator(`.roster-card[data-name="${name}"] .roster-card__open`);
      await opener.click();

      const inspector = page.locator('#inspector');
      await expect(inspector).toBeVisible();
      await expect(inspector).toHaveAttribute('role', 'dialog');
      await expect(inspector).toHaveAttribute('aria-modal', 'true');
      await expect(page.locator('#inspectorBackdrop')).toBeVisible();
      // The collection behind the sheet must not scroll (FR52).
      await expect(page.locator('body')).toHaveClass(/inspector-sheet-open/);

      await page.keyboard.press('Escape');
      await expect(inspector).toBeHidden();
      await expect(page.locator('#inspectorBackdrop')).toBeHidden();
      await expect(page.locator('body')).not.toHaveClass(/inspector-sheet-open/);
      // Focus goes back to the control that opened it (FR53).
      await expect(opener).toBeFocused();
    } finally {
      await request
        .delete(`${baseUrl}/api/agents?name=${encodeURIComponent(name)}`)
        .catch(() => undefined);
    }
  });

  test('four tabs follow the ARIA tabs pattern and survive a reload', async ({ page, request }) => {
    const name = `PWTabs${Date.now()}`;
    await request.post(`${baseUrl}/api/agents`, {
      data: { name, type: 'tool-calling', model: 'gpt-4o-mini' }
    });

    try {
      await open(page, `?agent=${encodeURIComponent(name)}`);
      await expect(page.locator('#stageName')).toHaveText(name);

      const ids = ['tab-overview', 'tab-prompt', 'tab-workspaces', 'tab-toolbox'];
      const panels = ['panel-overview', 'panel-prompt', 'panel-workspaces', 'panel-toolbox'];
      for (let i = 0; i < ids.length; i++) {
        await expect(page.locator(`#${ids[i]}`)).toHaveAttribute('aria-controls', panels[i]);
      }

      // Roving tab stop: only the selected tab is reachable by Tab.
      await expect(page.locator('#tab-overview')).toHaveAttribute('aria-selected', 'true');
      await expect(page.locator('#tab-toolbox')).toHaveAttribute('tabindex', '-1');

      // Arrow keys move selection; Home/End jump to the ends.
      await page.locator('#tab-overview').focus();
      await page.keyboard.press('ArrowRight');
      await expect(page.locator('#tab-prompt')).toHaveAttribute('aria-selected', 'true');
      await page.keyboard.press('End');
      await expect(page.locator('#tab-toolbox')).toHaveAttribute('aria-selected', 'true');
      await expect(page.locator('#panel-toolbox')).toBeVisible();
      await page.keyboard.press('Home');
      await expect(page.locator('#tab-overview')).toHaveAttribute('aria-selected', 'true');

      // The active tab is represented in the URL and restored (FR56).
      await page.locator('#tab-toolbox').click();
      await expect.poll(() => new URL(page.url()).searchParams.get('tab')).toBe('toolbox');
      await page.reload({ waitUntil: 'domcontentloaded' });
      await expect(page.locator('#tab-toolbox')).toHaveAttribute('aria-selected', 'true');
      await expect(page.locator('#panel-toolbox')).toBeVisible();
    } finally {
      await request
        .delete(`${baseUrl}/api/agents?name=${encodeURIComponent(name)}`)
        .catch(() => undefined);
    }
  });

  test('Toolbox reports real capabilities and claims nothing it cannot know', async ({
    page,
    request
  }) => {
    const plain = `PWTbox${Date.now()}`;
    await request.post(`${baseUrl}/api/agents`, {
      data: { name: plain, type: 'tool-calling', model: 'gpt-4o-mini' }
    });

    try {
      // A built-in agent has real capabilities from the detail contract.
      await open(page, '?agent=Claude%20Code&tab=toolbox');
      await expect(page.locator('#panel-toolbox')).toBeVisible();
      const builtInText = await page.locator('#toolboxBody').innerText();
      expect(builtInText).toContain('File Operations');
      expect(builtInText).toContain('Code Generation');

      // A plain agent declares none, and says so — not "unavailable", which
      // would mean we failed to find out (FR20/FR62).
      await open(page, `?agent=${encodeURIComponent(plain)}&tab=toolbox`);
      await expect(page.locator('#panel-toolbox')).toBeVisible();
      const plainText = await page.locator('#toolboxBody').innerText();
      expect(plainText).toContain('No capabilities are declared');
      expect(plainText).toContain('Web search');

      // None of the prototype's unbacked toolbox claims may appear (FR62).
      for (const banned of ['Focus', 'Readiness', 'Connected', 'Version', 'Operations Lead']) {
        expect(plainText, `Toolbox must not claim "${banned}"`).not.toContain(banned);
      }
      // Workspace-scoped permissions stay on the workspace surfaces (FR63).
      expect(plainText).toContain('managed on each workspace');
    } finally {
      await request
        .delete(`${baseUrl}/api/agents?name=${encodeURIComponent(plain)}`)
        .catch(() => undefined);
    }
  });

  test('Toolbox reports honestly when the detail request fails, and retries', async ({
    page,
    request
  }) => {
    const name = `PWTboxErr${Date.now()}`;
    await request.post(`${baseUrl}/api/agents`, {
      data: { name, type: 'tool-calling', model: 'gpt-4o-mini' }
    });

    try {
      await page.addInitScript(() => window.localStorage.setItem('ori-theme', 'dark'));
      await page.route('**/api/onboarding/status', route =>
        route.fulfill({
          status: 200,
          contentType: 'application/json',
          body: JSON.stringify({ needs_onboarding: false, completed: true })
        })
      );
      let fail = true;
      await page.route(`**/api/agents/${encodeURIComponent(name)}/detail`, route =>
        fail ? route.fulfill({ status: 500, body: 'boom' }) : route.continue()
      );

      await page.goto(`${baseUrl}/agents?agent=${encodeURIComponent(name)}&tab=toolbox`, {
        waitUntil: 'domcontentloaded'
      });
      await expect(page.locator('#toolboxBody')).toContainText('Capabilities unavailable');

      fail = false;
      await page.locator('#toolboxRetry').click();
      await expect(page.locator('#toolboxBody')).toContainText('Web search');
    } finally {
      await request
        .delete(`${baseUrl}/api/agents?name=${encodeURIComponent(name)}`)
        .catch(() => undefined);
    }
  });

  test('a built-in agent Inspector exposes no editable or destructive control', async ({
    page
  }) => {
    await open(page, '?agent=Claude%20Code');
    await expect(page.locator('#stageName')).toHaveText('Claude Code');

    // No delete affordance and no editable Overview form for a built-in (FR18).
    await expect(page.locator('#stageDelete')).toBeHidden();
    await expect(page.locator('#ov-description')).toHaveCount(0);
    await expect(page.locator('#savebar-overview')).toHaveCount(0);

    // Prompt states its read-only reality instead of offering an editor.
    await page.locator('#tab-prompt').click();
    await expect(page.locator('#pr-prompt')).toHaveCount(0);

    // The other tabs still report their real data.
    await page.locator('#tab-toolbox').click();
    await expect(page.locator('#toolboxBody')).toContainText('File Operations');
  });
});

// Collection slice: Gallery/List parity, discovery, sorting, and restorable URL
// state (PRD FR11–FR36, FR99–FR101).
test.describe('Agents collection controls', () => {
  async function openAgents(page, query = '') {
    await page.addInitScript(() => window.localStorage.setItem('ori-theme', 'dark'));
    await page.route('**/api/onboarding/status', route =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ needs_onboarding: false, completed: true })
      })
    );
    await page.goto(`${baseUrl}/agents${query}`, { waitUntil: 'domcontentloaded' });
    await expect(page.locator('#rosterList')).toBeVisible();
  }

  const names = page =>
    page.locator('.roster-card').evaluateAll(cards => cards.map(c => c.dataset.name));

  test('Gallery is the default view and switching to List keeps the same results', async ({
    page,
    request
  }) => {
    const prefix = `PWView${Date.now()}`;
    const created = [`${prefix} Cee`, `${prefix} Aay`, `${prefix} Bee`];
    for (const n of created) {
      await request.post(`${baseUrl}/api/agents`, {
        data: { name: n, type: 'tool-calling', model: 'gpt-4o-mini' }
      });
    }

    try {
      await openAgents(page);
      await expect(page.locator('#viewGallery')).toHaveAttribute('aria-pressed', 'true');
      await expect(page.locator('#viewList')).toHaveAttribute('aria-pressed', 'false');
      await expect(page.locator('#rosterList')).not.toHaveClass(/is-list/);

      await page.locator('#rosterSearch').fill(prefix);
      await expect(page.locator('.roster-card')).toHaveCount(3);
      const galleryOrder = await names(page);

      await page.locator('#viewList').click();
      await expect(page.locator('#rosterList')).toHaveClass(/is-list/);
      await expect(page.locator('#viewList')).toHaveAttribute('aria-pressed', 'true');
      await expect(page.locator('#viewGallery')).toHaveAttribute('aria-pressed', 'false');

      // Same collection, same order, same search — only the presentation moved.
      expect(await names(page)).toEqual(galleryOrder);
      await expect(page.locator('#rosterSearch')).toHaveValue(prefix);

      // A List row still exposes both controls and the same identity (FR17).
      const row = page.locator(`.roster-card[data-name="${created[1]}"]`);
      await expect(row.locator('.roster-card__check')).toBeAttached();
      await expect(row.locator('.roster-card__open')).toBeVisible();
      await expect(row.locator('.agent-card__name')).toHaveText(created[1]);
      await expect(row.locator('.agent-avatar')).toBeVisible();
    } finally {
      for (const n of created) {
        await request
          .delete(`${baseUrl}/api/agents?name=${encodeURIComponent(n)}`)
          .catch(() => undefined);
      }
    }
  });

  test('view switching preserves focused agent, checked agents, and tab', async ({
    page,
    request
  }) => {
    const prefix = `PWKeep${Date.now()}`;
    const a = `${prefix} Alpha`;
    const b = `${prefix} Bravo`;
    for (const n of [a, b]) {
      await request.post(`${baseUrl}/api/agents`, {
        data: { name: n, type: 'tool-calling', model: 'gpt-4o-mini' }
      });
    }

    try {
      await openAgents(page);
      await page.locator('#rosterSearch').fill(prefix);
      await expect(page.locator('.roster-card')).toHaveCount(2);

      await page.locator(`.roster-card[data-name="${a}"] .roster-card__open`).click();
      await expect(page.locator('#stageName')).toHaveText(a);
      await page.locator(`.roster-card[data-name="${b}"] .roster-card__check`).check();
      await page.locator('#tab-workspaces').click();
      await expect(page.locator('#bulkCount')).toHaveText('1 selected');

      await page.locator('#viewList').click();

      // Focus, selection, and the open tab all survive the switch (FR15).
      await expect(page.locator('#stageName')).toHaveText(a);
      await expect(page.locator('#bulkCount')).toHaveText('1 selected');
      await expect(page.locator(`.roster-card[data-name="${b}"]`)).toHaveClass(/is-checked/);
      await expect(page.locator(`.roster-card[data-name="${a}"]`)).toHaveClass(/is-focused/);
      await expect(page.locator('#tab-workspaces')).toHaveAttribute('aria-selected', 'true');
    } finally {
      for (const n of [a, b]) {
        await request
          .delete(`${baseUrl}/api/agents?name=${encodeURIComponent(n)}`)
          .catch(() => undefined);
      }
    }
  });

  test('an unsaved Overview edit survives a Gallery/List view switch (FR64)', async ({
    page,
    request
  }) => {
    const name = `PWViewGuard${Date.now()}`;
    await request.post(`${baseUrl}/api/agents`, {
      data: { name, type: 'tool-calling', model: 'gpt-4o-mini' }
    });

    try {
      await openAgents(page, `?agent=${encodeURIComponent(name)}`);
      await expect(page.locator('#stageName')).toHaveText(name);
      await page.locator('#ov-description').fill('Not yet saved.');

      // The view switch renders only the collection grid; it must not touch
      // the Inspector's form or silently discard the edit.
      await page.locator('#viewList').click();
      await expect(page.locator('#ov-description')).toHaveValue('Not yet saved.');
      await page.locator('#viewGallery').click();
      await expect(page.locator('#ov-description')).toHaveValue('Not yet saved.');

      const save = page.locator('#savebar-overview [data-role="save"]');
      await expect(save).toBeEnabled();
      await save.click();
      await expect
        .poll(async () => {
          const r = await request.get(`${baseUrl}/api/agents/${encodeURIComponent(name)}/detail`);
          return (await r.json()).metadata?.description;
        })
        .toBe('Not yet saved.');
    } finally {
      await request
        .delete(`${baseUrl}/api/agents?name=${encodeURIComponent(name)}`)
        .catch(() => undefined);
    }
  });

  test('search matches model and workspace name, not just name and tags', async ({
    page,
    request
  }) => {
    const stamp = Date.now();
    const modelAgent = `PWSrchModel${stamp}`;
    const wsAgent = `PWSrchWs${stamp}`;
    const wsName = `PWSrchSpace${stamp}`;
    await request.post(`${baseUrl}/api/agents`, {
      data: { name: modelAgent, type: 'tool-calling', model: 'gpt-4o-mini' }
    });
    await request.post(`${baseUrl}/api/agents`, {
      data: { name: wsAgent, type: 'tool-calling', model: 'gpt-4o-mini' }
    });
    let wsId = '';
    const ws = await request.post(`${baseUrl}/api/workspaces`, {
      data: { name: wsName, entry_agent_name: wsAgent }
    });
    if (ws.ok()) {
      const j = await ws.json();
      wsId = j?.folder?.id || j?.id || '';
    }

    try {
      await openAgents(page);

      // Workspace name (FR25) — matches the attached agent only.
      await page.locator('#rosterSearch').fill(wsName);
      await expect(page.locator('.roster-card')).toHaveCount(1);
      await expect(page.locator('.roster-card').first()).toHaveAttribute('data-name', wsAgent);

      // Model (FR25), case-insensitively.
      await page.locator('#rosterSearch').fill('GPT-4O-MINI');
      const matched = await names(page);
      expect(matched).toContain(modelAgent);
      expect(matched.length).toBeGreaterThan(1);
    } finally {
      if (wsId)
        await request
          .delete(`${baseUrl}/api/workspaces/${encodeURIComponent(wsId)}`)
          .catch(() => undefined);
      for (const n of [modelAgent, wsAgent]) {
        await request
          .delete(`${baseUrl}/api/agents?name=${encodeURIComponent(n)}`)
          .catch(() => undefined);
      }
    }
  });

  test('quick collections drive the same filter model as the selects', async ({ page }) => {
    await openAgents(page);
    const chip = (q: string) => page.locator(`[data-quick="${q}"]`);

    await expect(chip('all')).toHaveAttribute('aria-pressed', 'true');

    // Built-in narrows to CLI agents and lights the matching source select.
    await chip('builtin').click();
    await expect(chip('builtin')).toHaveAttribute('aria-pressed', 'true');
    await expect(chip('all')).toHaveAttribute('aria-pressed', 'false');
    await expect(page.locator('#filterSource')).toHaveValue('cli');
    for (const n of await names(page)) {
      expect(['Claude Code', 'Codex', 'Gemini CLI']).toContain(n);
    }

    // Setting the same thing through the select lights the chip (one model).
    await chip('all').click();
    await expect(page.locator('#filterSource')).toHaveValue('');
    await page.locator('#filterSource').selectOption('cli');
    await expect(chip('builtin')).toHaveAttribute('aria-pressed', 'true');

    await chip('all').click();
    await expect(chip('all')).toHaveAttribute('aria-pressed', 'true');
    await expect(page.locator('.roster-card').first()).toBeVisible();
  });

  test('the workspace picker filters by real membership and survives a reload', async ({
    page,
    request
  }) => {
    const stamp = Date.now();
    const inside = `PWWsIn${stamp}`;
    const outside = `PWWsOut${stamp}`;
    const wsName = `PWWsPick${stamp}`;
    for (const n of [inside, outside]) {
      await request.post(`${baseUrl}/api/agents`, {
        data: { name: n, type: 'tool-calling', model: 'gpt-4o-mini' }
      });
    }
    let wsId = '';
    const ws = await request.post(`${baseUrl}/api/workspaces`, {
      data: { name: wsName, entry_agent_name: inside }
    });
    if (ws.ok()) {
      const j = await ws.json();
      wsId = j?.folder?.id || j?.id || '';
    }
    expect(wsId).toBeTruthy();

    try {
      await openAgents(page);
      // The picker offers the real workspace by name.
      await expect(page.locator(`#filterWorkspace option[value="${wsId}"]`)).toHaveText(wsName);

      await page.locator('#filterWorkspace').selectOption(wsId);
      const shown = await names(page);
      expect(shown).toContain(inside);
      expect(shown).not.toContain(outside);

      // Represented in the URL and restored on reload (FR32).
      await expect.poll(() => new URL(page.url()).searchParams.get('ws')).toBe(wsId);
      await page.reload({ waitUntil: 'domcontentloaded' });
      await expect(page.locator('#filterWorkspace')).toHaveValue(wsId);
      expect(await names(page)).toContain(inside);
    } finally {
      if (wsId)
        await request
          .delete(`${baseUrl}/api/workspaces/${encodeURIComponent(wsId)}`)
          .catch(() => undefined);
      for (const n of [inside, outside]) {
        await request
          .delete(`${baseUrl}/api/agents?name=${encodeURIComponent(n)}`)
          .catch(() => undefined);
      }
    }
  });

  test('sorts are deterministic and break ties by name', async ({ page, request }) => {
    const prefix = `PWSort${Date.now()}`;
    // Same level and same workspace count: only the name can order them.
    const created = [`${prefix} Charlie`, `${prefix} Alpha`, `${prefix} Bravo`];
    for (const n of created) {
      await request.post(`${baseUrl}/api/agents`, {
        data: { name: n, type: 'tool-calling', model: 'gpt-4o-mini' }
      });
    }

    try {
      await openAgents(page);
      await page.locator('#rosterSearch').fill(prefix);
      await expect(page.locator('.roster-card')).toHaveCount(3);

      const sorted = created.slice().sort();
      await expect(page.locator('#rosterSort')).toHaveValue('name-asc');
      expect(await names(page)).toEqual(sorted);

      await page.locator('#rosterSort').selectOption('name-desc');
      expect(await names(page)).toEqual(sorted.slice().reverse());

      // Level and workspace-count sorts tie for all three, so name decides.
      for (const mode of ['level-desc', 'workspaces-desc']) {
        await page.locator('#rosterSort').selectOption(mode);
        expect(await names(page), `${mode} must break ties by name`).toEqual(sorted);
      }
    } finally {
      for (const n of created) {
        await request
          .delete(`${baseUrl}/api/agents?name=${encodeURIComponent(n)}`)
          .catch(() => undefined);
      }
    }
  });

  test('view, search, sort and filters are shareable and restored from the URL', async ({
    page,
    request
  }) => {
    const prefix = `PWUrl${Date.now()}`;
    const tag = `${prefix.toLowerCase()}tag`;
    const name = `${prefix} Tagged`;
    await request.post(`${baseUrl}/api/agents`, {
      data: { name, type: 'tool-calling', model: 'gpt-4o-mini', tags: [tag] }
    });

    try {
      await openAgents(
        page,
        `?view=list&q=${encodeURIComponent(prefix)}&sort=name-desc&tag=${tag}&fav=0`
      );
      // Every represented value is applied on first paint (FR32).
      await expect(page.locator('#rosterList')).toHaveClass(/is-list/);
      await expect(page.locator('#viewList')).toHaveAttribute('aria-pressed', 'true');
      await expect(page.locator('#rosterSearch')).toHaveValue(prefix);
      await expect(page.locator('#rosterSort')).toHaveValue('name-desc');
      await expect(page.locator('#filterTag')).toHaveValue(tag);
      await expect(page.locator('.roster-card')).toHaveCount(1);
    } finally {
      await request
        .delete(`${baseUrl}/api/agents?name=${encodeURIComponent(name)}`)
        .catch(() => undefined);
    }
  });

  test('invalid URL values fail safely one at a time', async ({ page }) => {
    // A bad view, sort, source, health, and workspace all at once: each is
    // discarded on its own and the roster still loads (FR33).
    await openAgents(
      page,
      '?view=hologram&sort=chaos&source=aliens&health=melting&ws=not-a-workspace&tab=nope'
    );
    await expect(page.locator('.roster-card').first()).toBeVisible();
    await expect(page.locator('#rosterList')).not.toHaveClass(/is-list/);
    await expect(page.locator('#viewGallery')).toHaveAttribute('aria-pressed', 'true');
    await expect(page.locator('#rosterSort')).toHaveValue('name-asc');
    await expect(page.locator('#filterSource')).toHaveValue('');
    await expect(page.locator('#filterWorkspace')).toHaveValue('');
    await expect(page.locator('#tab-overview')).toHaveAttribute('aria-selected', 'true');
  });

  test('Back and Forward restore view and focused agent', async ({ page, request }) => {
    const prefix = `PWHist${Date.now()}`;
    const a = `${prefix} Alpha`;
    const b = `${prefix} Bravo`;
    for (const n of [a, b]) {
      await request.post(`${baseUrl}/api/agents`, {
        data: { name: n, type: 'tool-calling', model: 'gpt-4o-mini' }
      });
    }

    try {
      await openAgents(page);
      await page.locator('#rosterSearch').fill(prefix);
      await expect(page.locator('.roster-card')).toHaveCount(2);

      await page.locator(`.roster-card[data-name="${a}"] .roster-card__open`).click();
      await expect(page.locator('#stageName')).toHaveText(a);
      await page.locator(`.roster-card[data-name="${b}"] .roster-card__open`).click();
      await expect(page.locator('#stageName')).toHaveText(b);

      await page.goBack();
      await expect(page.locator('#stageName')).toHaveText(a);
      await page.goForward();
      await expect(page.locator('#stageName')).toHaveText(b);
    } finally {
      for (const n of [a, b]) {
        await request
          .delete(`${baseUrl}/api/agents?name=${encodeURIComponent(n)}`)
          .catch(() => undefined);
      }
    }
  });

  test('Select visible binds to the current filtered result in either view', async ({
    page,
    request
  }) => {
    const prefix = `PWVis${Date.now()}`;
    const names3 = [`${prefix} Alpha`, `${prefix} Bravo`, `${prefix} Charlie`];
    for (const n of names3) {
      await request.post(`${baseUrl}/api/agents`, {
        data: { name: n, type: 'tool-calling', model: 'gpt-4o-mini' }
      });
    }

    try {
      await openAgents(page);
      await page.locator('#viewList').click();
      await page.locator('#rosterSearch').fill(prefix);
      await expect(page.locator('.roster-card')).toHaveCount(3);

      // Select visible means the post-search result, in List exactly as in
      // Gallery (FR31).
      await page.locator('#rosterSelectAll').click();
      await expect(page.locator('#bulkCount')).toHaveText('3 selected');

      // Narrowing hides two of them but keeps them selected, with an accurate
      // hidden count (FR43).
      await page.locator('#rosterSearch').fill(`${prefix} Alpha`);
      await expect(page.locator('.roster-card')).toHaveCount(1);
      await expect(page.locator('#bulkCount')).toHaveText('3 selected · 2 hidden by filters');

      // Switching back to Gallery changes none of that (FR15).
      await page.locator('#viewGallery').click();
      await expect(page.locator('#bulkCount')).toHaveText('3 selected · 2 hidden by filters');
    } finally {
      for (const n of names3) {
        await request
          .delete(`${baseUrl}/api/agents?name=${encodeURIComponent(n)}`)
          .catch(() => undefined);
      }
    }
  });
});

// Selection/bulk management slice: independent checked state and guarded bulk
// actions across Gallery, List, and the Inspector (PRD FR31, FR36-FR48,
// FR82-FR90, FR101).
test.describe('Agents selection and bulk management', () => {
  async function openAgents(page, query = '') {
    await page.addInitScript(() => window.localStorage.setItem('ori-theme', 'dark'));
    await page.route('**/api/onboarding/status', route =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ needs_onboarding: false, completed: true })
      })
    );
    await page.goto(`${baseUrl}/agents${query}`, { waitUntil: 'domcontentloaded' });
    await expect(page.locator('#rosterList')).toBeVisible();
  }

  test('checked selection survives opening and closing the Inspector', async ({
    page,
    request
  }) => {
    const prefix = `PWCheckInsp${Date.now()}`;
    const a = `${prefix} Alpha`;
    const b = `${prefix} Bravo`;
    for (const n of [a, b]) {
      await request.post(`${baseUrl}/api/agents`, {
        data: { name: n, type: 'tool-calling', model: 'gpt-4o-mini' }
      });
    }

    try {
      await openAgents(page);
      await page.locator('#rosterSearch').fill(prefix);
      await expect(page.locator('.roster-card')).toHaveCount(2);

      await page.locator(`.roster-card[data-name="${a}"] .roster-card__check`).check();
      await expect(page.locator('#bulkCount')).toHaveText('1 selected');

      // Opening the Inspector on a DIFFERENT agent must not touch the checked
      // set, and closing it again must not clear it either (FR37/FR51).
      await page.locator(`.roster-card[data-name="${b}"] .roster-card__open`).click();
      await expect(page.locator('#stageName')).toHaveText(b);
      await expect(page.locator(`.roster-card[data-name="${a}"]`)).toHaveClass(/is-checked/);
      await expect(page.locator(`.roster-card[data-name="${b}"]`)).not.toHaveClass(/is-checked/);
      await expect(page.locator('#bulkCount')).toHaveText('1 selected');

      await page.locator('#inspectorClose').click();
      await expect(page.locator('#inspector')).toBeHidden();
      await expect(page.locator('#bulkCount')).toHaveText('1 selected');
      await expect(page.locator(`.roster-card[data-name="${a}"]`)).toHaveClass(/is-checked/);
    } finally {
      for (const n of [a, b]) {
        await request
          .delete(`${baseUrl}/api/agents?name=${encodeURIComponent(n)}`)
          .catch(() => undefined);
      }
    }
  });

  test('Shift-click range selection works in List view using result order', async ({
    page,
    request
  }) => {
    const prefix = `PWRangeList${Date.now()}`;
    const names = [`${prefix} Alpha`, `${prefix} Bravo`, `${prefix} Charlie`, `${prefix} Delta`];
    for (const n of names) {
      await request.post(`${baseUrl}/api/agents`, {
        data: { name: n, type: 'tool-calling', model: 'gpt-4o-mini' }
      });
    }

    try {
      await openAgents(page);
      await page.locator('#viewList').click();
      await page.locator('#rosterSearch').fill(prefix);
      await expect(page.locator('.roster-card')).toHaveCount(4);

      await page.locator(`.roster-card[data-name="${names[0]}"] .roster-card__check`).check();
      await page
        .locator(`.roster-card[data-name="${names[2]}"] .roster-card__check`)
        .click({ modifiers: ['Shift'] });

      // Alpha, Bravo, and Charlie are checked; Delta is not (FR42).
      for (const n of names.slice(0, 3)) {
        await expect(page.locator(`.roster-card[data-name="${n}"]`)).toHaveClass(/is-checked/);
      }
      await expect(page.locator(`.roster-card[data-name="${names[3]}"]`)).not.toHaveClass(
        /is-checked/
      );
      await expect(page.locator('#bulkCount')).toHaveText('3 selected');
    } finally {
      for (const n of names) {
        await request
          .delete(`${baseUrl}/api/agents?name=${encodeURIComponent(n)}`)
          .catch(() => undefined);
      }
    }
  });

  test('bulk-deleting the agent open in the Inspector leaves it on a valid fallback', async ({
    page,
    request
  }) => {
    const prefix = `PWBulkFocus${Date.now()}`;
    const target = `${prefix} Target`;
    const survivor = `${prefix} Survivor`;
    for (const n of [target, survivor]) {
      await request.post(`${baseUrl}/api/agents`, {
        data: { name: n, type: 'tool-calling', model: 'gpt-4o-mini' }
      });
    }

    try {
      await openAgents(page);
      await page.locator('#rosterSearch').fill(prefix);
      await expect(page.locator('.roster-card')).toHaveCount(2);

      // Focus Target (opens the Inspector), then check and bulk-delete it —
      // deletion happens through the collection's checkbox/bulk bar, not
      // through the Inspector itself.
      await page.locator(`.roster-card[data-name="${target}"] .roster-card__open`).click();
      await expect(page.locator('#stageName')).toHaveText(target);
      await page.locator(`.roster-card[data-name="${target}"] .roster-card__check`).check();
      await page.locator('#bulkDelete').click();
      await expect(page.locator('#bulkDeleteConfirm')).toHaveText(/Delete 1 agent/);
      await page.locator('#bulkDeleteConfirm').click();

      await expect(page.locator('#bulkResult')).toBeVisible();
      await expect
        .poll(async () =>
          (await request.get(`${baseUrl}/api/agents/${encodeURIComponent(target)}/detail`)).status()
        )
        .toBe(404);

      // The Inspector reflects a valid next state: it must not keep showing
      // the now-deleted agent, and it must not be left broken/blank while the
      // collection is still usable (FR48/FR101).
      await expect(page.locator('#stageName')).not.toHaveText(target);
      await expect(page.locator('.roster-card').first()).toBeVisible();
    } finally {
      for (const n of [target, survivor]) {
        await request
          .delete(`${baseUrl}/api/agents?name=${encodeURIComponent(n)}`)
          .catch(() => undefined);
      }
    }
  });

  test('bulk result stays inspectable after the checked set clears and survives a Gallery/List switch', async ({
    page,
    request
  }) => {
    const prefix = `PWResultPersist${Date.now()}`;
    const names = [`${prefix} One`, `${prefix} Two`];
    for (const n of names) {
      await request.post(`${baseUrl}/api/agents`, {
        data: { name: n, type: 'tool-calling', model: 'gpt-4o-mini' }
      });
    }

    try {
      await openAgents(page);
      await page.locator('#rosterSearch').fill(prefix);
      await expect(page.locator('.roster-card')).toHaveCount(2);
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

      // The result surface remains after the checked set is gone (a
      // successful metadata action clears checked selection) and after a
      // view switch (FR46/FR48).
      await expect(page.locator('#bulkResult')).toBeVisible();
      await page.locator('#viewList').click();
      await expect(page.locator('#bulkResultSummary')).toBeVisible();
      await page.locator('#viewGallery').click();
      await expect(page.locator('#bulkResultSummary')).toBeVisible();
    } finally {
      for (const n of names) {
        await request
          .delete(`${baseUrl}/api/agents?name=${encodeURIComponent(n)}`)
          .catch(() => undefined);
      }
    }
  });
});
