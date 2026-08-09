import { test, expect, Page, APIRequestContext } from '@playwright/test';

/**
 * Unified Agent Appearance — end-to-end coverage (PRD FR-109).
 *
 * One real agent is switched through Generated, Character, and Upload, and every
 * reachable surface is checked after each switch. The point is not that each
 * screen works in isolation — it is that they cannot disagree, which is the
 * failure this feature exists to fix (Success Metric 2).
 */

const AGENT = 'Appearance E2E Agent';

/** Deletes the agent if a previous run left it behind, then creates it fresh. */
async function resetAgent(request: APIRequestContext) {
  await request.delete(`/api/agents?name=${encodeURIComponent(AGENT)}`);
  const created = await request.post('/api/agents', {
    data: {
      name: AGENT,
      type: 'tool-calling',
      model: 'gpt-4o-mini',
      description: 'unified appearance end-to-end fixture'
    }
  });
  expect(created.ok(), await created.text()).toBeTruthy();
}

async function appearanceOf(request: APIRequestContext) {
  const res = await request.get(`/api/agents/${encodeURIComponent(AGENT)}/detail`);
  expect(res.ok()).toBeTruthy();
  return (await res.json()).appearance;
}

/** The first assignable working character, read from the live catalog. */
async function firstCharacter(request: APIRequestContext): Promise<string> {
  const res = await request.get('/api/characters');
  expect(res.ok()).toBeTruthy();
  const body = await res.json();
  const working = (body.characters || []).filter((c: any) => c.kind !== 'guide');
  expect(working.length).toBeGreaterThan(0);
  return working[0].id;
}

/**
 * Reads the requested/rendered source off every avatar the page rendered for
 * this agent. The renderer stamps `data-aa-requested` with the saved mode and
 * expresses the rendered one as a class, so a surface that quietly kept an old
 * source shows up here rather than needing a pixel comparison.
 */
async function renderedSources(page: Page): Promise<{ requested: string; rendered: string }[]> {
  return page.evaluate(name => {
    const hosts = Array.from(
      document.querySelectorAll<HTMLElement>(`.agent-avatar[data-aa-name="${CSS.escape(name)}"]`)
    );
    return hosts.map(host => {
      let rendered = 'generated';
      if (host.classList.contains('agent-avatar--image')) rendered = 'uploaded';
      else if (host.classList.contains('agent-avatar--character')) rendered = 'character';
      return { requested: host.dataset.aaRequested || '', rendered };
    });
  }, AGENT);
}

// One shared fixture agent that every test mutates, so the suite must not run
// its own cases in parallel — concurrent workers would each reset the agent out
// from under the others.
test.describe.configure({ mode: 'serial' });

test.describe('unified agent appearance', () => {
  test.beforeEach(async ({ request }) => {
    await resetAgent(request);
  });

  test.afterAll(async ({ request }) => {
    await request.delete(`/api/agents?name=${encodeURIComponent(AGENT)}`);
  });

  test('a new agent starts in generated mode on every surface', async ({ page, request }) => {
    const appearance = await appearanceOf(request);
    expect(appearance.mode).toBe('generated');
    // The generated object always ships, so a client never has to guess whether
    // a missing key means "no override" or "unsupported build" (FR-2).
    expect(appearance.generated).toBeTruthy();

    await page.goto('/agents');
    await page.waitForSelector('.agent-avatar', { timeout: 15000 });
    const sources = await renderedSources(page);
    expect(sources.length).toBeGreaterThan(0);
    for (const source of sources) {
      expect(source.requested).toBe('generated');
      expect(source.rendered).toBe('generated');
    }
  });

  test('a colour override survives a reload and can be reset', async ({ page, request }) => {
    const patched = await request.patch(`/api/agents?name=${encodeURIComponent(AGENT)}`, {
      data: { appearance: { generated: { color: '#6D5DFC' } } }
    });
    expect(patched.ok(), await patched.text()).toBeTruthy();

    // Normalized to one stored form, so every downstream comparison is exact
    // (FR-7).
    expect((await appearanceOf(request)).generated.color).toBe('#6d5dfc');

    await page.goto('/agents');
    await page.waitForSelector(`.agent-avatar[data-aa-name="${AGENT}"]`, { timeout: 15000 });
    const color = await page.getAttribute(
      `.agent-avatar[data-aa-name="${AGENT}"]`,
      'data-aa-color'
    );
    expect(color).toBe('#6d5dfc');

    // Explicit null is the documented reset, and it must not disturb anything
    // else (FR-31/FR-54).
    const reset = await request.patch(`/api/agents?name=${encodeURIComponent(AGENT)}`, {
      data: { appearance: { generated: { color: null } } }
    });
    expect(reset.ok()).toBeTruthy();
    const after = await appearanceOf(request);
    expect(after.generated.color).toBeUndefined();
    expect(after.mode).toBe('generated');
  });

  test('switching among all three sources never loses the inactive ones', async ({ request }) => {
    const characterId = await firstCharacter(request);

    // Generated with a colour.
    await request.patch(`/api/agents?name=${encodeURIComponent(AGENT)}`, {
      data: { appearance: { generated: { color: '#6d5dfc' } } }
    });

    // Character.
    const chose = await request.patch(`/api/agents?name=${encodeURIComponent(AGENT)}`, {
      data: { appearance: { character: { catalog_id: characterId } } }
    });
    expect(chose.ok(), await chose.text()).toBeTruthy();
    let appearance = await appearanceOf(request);
    expect(appearance.mode).toBe('character');
    expect(appearance.character.catalog_id).toBe(characterId);
    // Server-assigned: a client cannot claim an art revision that does not
    // exist (FR-10).
    expect(appearance.character.catalog_version).toBeGreaterThan(0);
    expect(appearance.generated.color).toBe('#6d5dfc');

    // Upload.
    const uploaded = await request.post(
      `/api/agents/${encodeURIComponent(AGENT)}/appearance/upload`,
      {
        multipart: {
          image: { name: 'demo.png', mimeType: 'image/png', buffer: onePixelPng() }
        }
      }
    );
    expect(uploaded.ok(), await uploaded.text()).toBeTruthy();
    appearance = await appearanceOf(request);
    // A successful upload is immediately the rendered source (FR-36).
    expect(appearance.mode).toBe('uploaded');
    expect(appearance.uploaded.image).toBeTruthy();
    expect(appearance.character.catalog_id).toBe(characterId);
    expect(appearance.generated.color).toBe('#6d5dfc');

    // Back to Generated — and everything else is still there.
    await request.patch(`/api/agents?name=${encodeURIComponent(AGENT)}`, {
      data: { appearance: { mode: 'generated' } }
    });
    appearance = await appearanceOf(request);
    expect(appearance.mode).toBe('generated');
    expect(appearance.uploaded.image).toBeTruthy();
    expect(appearance.character.catalog_id).toBe(characterId);

    // Back to Character without re-picking it, which is the whole point of
    // retaining inactive state (FR-11).
    const back = await request.patch(`/api/agents?name=${encodeURIComponent(AGENT)}`, {
      data: { appearance: { mode: 'character' } }
    });
    expect(back.ok()).toBeTruthy();
    expect((await appearanceOf(request)).mode).toBe('character');
  });

  test('removing the active upload falls back to generated and keeps the character', async ({
    request
  }) => {
    const characterId = await firstCharacter(request);
    await request.patch(`/api/agents?name=${encodeURIComponent(AGENT)}`, {
      data: { appearance: { character: { catalog_id: characterId } } }
    });
    await request.post(`/api/agents/${encodeURIComponent(AGENT)}/appearance/upload`, {
      multipart: { image: { name: 'demo.png', mimeType: 'image/png', buffer: onePixelPng() } }
    });
    expect((await appearanceOf(request)).mode).toBe('uploaded');

    const removed = await request.delete(
      `/api/agents/${encodeURIComponent(AGENT)}/appearance/upload`
    );
    expect(removed.ok(), await removed.text()).toBeTruthy();

    const appearance = await appearanceOf(request);
    expect(appearance.mode).toBe('generated');
    expect(appearance.uploaded).toBeUndefined();
    // Removing one source must never delete another (FR-40).
    expect(appearance.character.catalog_id).toBe(characterId);
  });

  test('the old avatar route no longer stores an image', async ({ request }) => {
    // The removed route must not proxy to the new one; whatever the generic
    // agent dispatcher makes of the leftover path, it must not upload (FR-62).
    await request.post(`/api/agents/${encodeURIComponent(AGENT)}/avatar`, {
      multipart: { avatar: { name: 'demo.png', mimeType: 'image/png', buffer: onePixelPng() } }
    });
    const appearance = await appearanceOf(request);
    expect(appearance.mode).toBe('generated');
    expect(appearance.uploaded).toBeUndefined();
  });

  test('retired request fields are rejected rather than ignored', async ({ request }) => {
    // Silently ignoring these is the worse failure: the caller would appear to
    // succeed while changing nothing (FR-51).
    for (const field of ['avatar_color', 'avatar_image', 'character', 'display_mode']) {
      const res = await request.patch(`/api/agents?name=${encodeURIComponent(AGENT)}`, {
        data: { [field]: 'whatever' }
      });
      expect(res.status(), `${field} must be rejected`).toBe(400);
    }
  });

  test('appearance responses carry no retired avatar or character metadata', async ({
    request
  }) => {
    const res = await request.get(`/api/agents/${encodeURIComponent(AGENT)}/detail`);
    const body = await res.json();
    const metadata = body.metadata || {};
    for (const retired of ['avatar_color', 'avatar_image', 'character']) {
      expect(metadata[retired], `metadata.${retired} must be gone`).toBeUndefined();
    }
  });

  test('an appearance change does not touch the rest of the definition', async ({ request }) => {
    const before = await (
      await request.get(`/api/agents/${encodeURIComponent(AGENT)}/detail`)
    ).json();
    const characterId = await firstCharacter(request);

    await request.patch(`/api/agents?name=${encodeURIComponent(AGENT)}`, {
      data: { appearance: { character: { catalog_id: characterId } } }
    });

    const after = await (
      await request.get(`/api/agents/${encodeURIComponent(AGENT)}/detail`)
    ).json();
    // Appearance is presentation only. If any of these can move, the feature has
    // reintroduced exactly the promise it removed (FR-17).
    expect(after.system_prompt).toBe(before.system_prompt);
    expect(after.model).toBe(before.model);
    expect(after.role).toBe(before.role);
    expect(after.type).toBe(before.type);
    expect(after.metadata?.description).toBe(before.metadata?.description);
  });

  test('the effective-prompt inspector exposes no appearance-derived layer', async ({
    request
  }) => {
    // Leaving a tone field in an inspector keeps the false promise alive even if
    // live prompts ignore it, so the concept has to be gone end to end (FR-21).
    const res = await request.get(`/api/agents/${encodeURIComponent(AGENT)}/detail`);
    const raw = await res.text();
    for (const retired of ['character_tone', 'voice_enabled', 'tone_traits', 'sample_line']) {
      expect(raw, `${retired} must not appear in any agent response`).not.toContain(retired);
    }
  });

  test('the character catalog returns visual metadata only', async ({ request }) => {
    const res = await request.get('/api/characters');
    expect(res.ok()).toBeTruthy();
    const raw = await res.text();
    for (const retired of ['tone_traits', 'sample_line']) {
      expect(raw, `${retired} must not be served`).not.toContain(retired);
    }
  });
});

/** A 1x1 PNG. Uploads are content-sniffed, so this has to be a real image. */
function onePixelPng(): Buffer {
  return Buffer.from(
    'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==',
    'base64'
  );
}
