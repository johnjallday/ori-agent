import { test, expect, type APIRequestContext, type Page } from '@playwright/test';
import { installLocalCdn } from './helpers/offline-cdn';

/**
 * "Where settings come from" on the full agent page (/agents/{name}).
 *
 * Real host data seeded by ./scripts/settings-ownership-demo.sh (run it as the
 * demo server, then point ./scripts/e2e.sh at its port). Workspace copies are
 * written by the server's startup snapshot, so they cannot be created from a
 * spec; the script restarts its own sandboxed server once to take them.
 * The specs only read: they never change the seeded records.
 *
 * Groups can run alone with -g: "slice 1" (library vs workspace value/source,
 * unavailable context, read-only).
 */

test.beforeEach(async ({ page }) => {
  await installLocalCdn(page);
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await page.route('**/api/onboarding/status', route =>
    route.fulfill({ json: { needs_onboarding: false, completed: true, skipped: true } })
  );
});

type Ref = { id: string; name: string };

// fixture finds the seeded records through the inspection API itself.
async function fixture(request: APIRequestContext) {
  const name = 'Ownership Writer';
  const res = await request.get(`/api/agents/${encodeURIComponent(name)}/settings-ownership`);
  expect(
    res.ok(),
    'Seeded fixture missing: run ./scripts/settings-ownership-demo.sh <port> and target that port'
  ).toBeTruthy();
  const body = await res.json();
  const byName = (wanted: string): Ref => {
    const ws = body.contexts.workspaces.find((w: Ref) => w.name === wanted);
    expect(ws, `seeded workspace ${wanted}`).toBeTruthy();
    return { id: ws.id, name: ws.name };
  };
  return {
    name,
    same: byName('Ownership Same'),
    custom: byName('Ownership Custom'),
    direct: byName('Ownership Direct'),
    scratch: byName('Ownership Scratch'),
    cleanup: async () => undefined
  };
}

const section = (page: Page) => page.locator('#settingsOwnershipSection');
const row = (page: Page, key: string) => section(page).locator(`[data-field="${key}"]`);

async function openAgent(page: Page, name: string, query = '') {
  await page.goto(`/agents/${encodeURIComponent(name)}${query}`);
  await expect(page.locator('#agentName')).toHaveText(name);
  await expect(section(page)).toBeVisible();
}

// Every non-GET request the page sends while only inspecting.
function recordWrites(page: Page) {
  const writes: string[] = [];
  page.on('request', req => {
    if (req.method() !== 'GET' && req.url().includes('/api/'))
      writes.push(`${req.method()} ${req.url()}`);
  });
  return writes;
}

test.describe('slice 1', () => {
  test('library and workspace contexts show their own saved value and source', async ({
    page,
    request
  }) => {
    const f = await fixture(request);
    try {
      const writes = recordWrites(page);
      await openAgent(page, f.name);
      await expect(section(page)).toContainText('Library profile');
      await expect(row(page, 'model')).toContainText('gpt-4o-mini');
      await expect(section(page).locator('[data-ownership-source="definition"]')).toContainText(
        'Library profile'
      );

      const picker = page.getByLabel('Viewing');
      await expect(picker).toBeEnabled();
      await picker.selectOption({ label: `Workspace: ${f.custom.name}` });
      await expect(section(page)).toContainText(`In workspace “${f.custom.name}”`);
      await expect(row(page, 'model')).toContainText('gpt-4.1');
      await expect(row(page, 'model')).toContainText('Differs from library');
      await expect(section(page).locator('[data-ownership-source="definition"]')).toContainText(
        "This workspace's own saved copy"
      );
      await expect(page).toHaveURL(new RegExp(`studio_id=${f.custom.id}`));

      // The equal copy is still the workspace's own, independent copy.
      await picker.selectOption({ label: `Workspace: ${f.same.name}` });
      await expect(row(page, 'model')).toContainText('gpt-4o-mini');
      await expect(row(page, 'model')).toContainText('Same as library');
      await expect(section(page)).toContainText('independent copy');

      // A workspace without its own copy reads the library: not "inherited".
      await picker.selectOption({ label: `Workspace: ${f.direct.name}` });
      await expect(section(page).locator('[data-ownership-source="definition"]')).toContainText(
        'read directly'
      );
      await expect(row(page, 'model')).toContainText('gpt-4o-mini');
      await expect(row(page, 'model')).not.toContainText('Same as library');
      await picker.selectOption({ label: `Workspace: ${f.same.name}` });
      await expect(row(page, 'model')).toContainText('Same as library');

      // Reload keeps the context; Back returns to the previous one.
      await page.reload();
      await expect(section(page)).toContainText(`In workspace “${f.same.name}”`);
      await page.goBack();
      await expect(section(page)).toContainText(`In workspace “${f.direct.name}”`);
      await expect(page.getByLabel('Viewing')).toHaveValue(`workspace:${f.direct.id}`);
      await expect(section(page).locator('[data-ownership-source="definition"]')).toContainText(
        'read directly'
      );

      // Inspection added no Save and sent no write.
      await expect(section(page).getByRole('button', { name: /save/i })).toHaveCount(0);
      expect(writes).toEqual([]);
    } finally {
      await f.cleanup();
    }
  });

  test('an unavailable workspace is a recoverable state, never another workspace', async ({
    page,
    request
  }) => {
    const f = await fixture(request);
    try {
      await openAgent(page, f.name, '?ctx=workspace&studio_id=no-such-workspace');
      await expect(section(page)).toContainText('In an unavailable workspace');
      await expect(section(page)).toContainText('not available');
      await expect(section(page).locator('[data-field]')).toHaveCount(0);
      await expect(page.getByLabel('Viewing')).toHaveValue('workspace:no-such-workspace');
      await page.getByLabel('Viewing').selectOption({ label: 'Library profile' });
      await expect(row(page, 'model')).toContainText('gpt-4o-mini');
    } finally {
      await f.cleanup();
    }
  });

  test('a failed read offers retry in place (injected failure)', async ({ page, request }) => {
    const f = await fixture(request);
    try {
      let fail = true;
      await page.route('**/settings-ownership*', route =>
        fail ? route.fulfill({ status: 500, json: { error: 'injected' } }) : route.continue()
      );
      await openAgent(page, f.name);
      await expect(section(page)).toContainText('could not be loaded');
      fail = false;
      await section(page).getByRole('button', { name: 'Retry' }).click();
      await expect(row(page, 'model')).toContainText('gpt-4o-mini');
    } finally {
      await f.cleanup();
    }
  });
});

test.describe('slice 2', () => {
  test('saved prompt layers and the workspace chat model are shown per context', async ({
    page,
    request
  }) => {
    const f = await fixture(request);
    const prompt = () => section(page).locator('[data-ownership-prompt]');
    await openAgent(page, f.name);
    await expect(prompt()).toContainText('Base prompt from: Library profile');
    await prompt().getByText('Base prompt', { exact: true }).click();
    await expect(prompt().locator('pre').first()).toHaveText('Library prompt.');
    // A library card is not a conversation reader for an ordinary agent.
    await expect(section(page).locator('[data-ownership-conversation]')).toHaveCount(0);

    await page.getByLabel('Viewing').selectOption({ label: `Workspace: ${f.custom.name}` });
    await expect(prompt()).toContainText("Base prompt from: This workspace's own saved copy");
    await expect(prompt()).toContainText('Role here');
    await expect(prompt()).toContainText('Reviewer');
    await prompt().getByText('Saved text together').click();
    const together = prompt().locator('details').nth(1).locator('pre');
    await expect(together).toContainText('Custom workspace prompt.');
    await expect(together).toContainText('Keep notes short.');
    await expect(together).not.toContainText('Library prompt.');
    await expect(prompt()).toContainText('not the complete prompt');

    const conversation = section(page).locator('[data-ownership-conversation]');
    await expect(conversation).toContainText('Workspace chat model:');
    await expect(conversation).not.toContainText('checking');
    await expect(conversation).toContainText('gpt-4.1');
    const link = conversation.getByRole('link', { name: 'Where to change it' });
    await expect(link).toHaveAttribute(
      'href',
      /^\/workspaces\/ownership-custom\?editAgentModel=Ownership\+Writer$/
    );
  });

  test('switching context keeps a profile draft and its Save target', async ({ page, request }) => {
    const f = await fixture(request);
    const writes: string[] = [];
    page.on('request', req => {
      if (req.method() !== 'GET' && req.url().includes('/api/')) writes.push(req.url());
    });
    await openAgent(page, f.name);
    await page.locator('#editConfigBtn').click();
    await page.locator('#editTemperature').fill('1.3');
    await expect(section(page).locator('[data-ownership-unsaved]')).toContainText(
      'not shown here until they are saved'
    );
    await page.getByLabel('Viewing').selectOption({ label: `Workspace: ${f.custom.name}` });
    await expect(row(page, 'model')).toContainText('gpt-4.1');
    await page.goBack();
    await expect(row(page, 'model')).toContainText('gpt-4o-mini');
    // The draft, its editor and the unsaved notice all survive the switches.
    await expect(page.locator('#editTemperature')).toHaveValue('1.3');
    await expect(page.locator('#configEditBadge')).toBeVisible();
    await expect(page.locator('#saveConfigBtn')).toBeEnabled();
    await expect(section(page).locator('[data-ownership-unsaved]')).toBeVisible();
    expect(writes).toEqual([]);
  });

  test('duplicate instances stay unselected until chosen (injected legacy data)', async ({
    page,
    request
  }) => {
    // Same-named duplicate instances are legacy data: current workspaces
    // collapse them on read, so this case uses an injected response.
    const f = await fixture(request);
    const base = {
      agent: f.name,
      library: { state: 'present', owner: 'library' },
      contexts: { state: 'ok', workspaces: [{ id: 'legacy-ws', name: 'Legacy Studio' }] },
      context: {
        kind: 'workspace',
        state: 'ok',
        workspace: { id: 'legacy-ws', name: 'Legacy Studio', folder_slug: 'legacy-studio' }
      },
      definition: {
        state: 'ok',
        source: 'workspace_copy',
        disabled: false,
        fields: [
          { key: 'model', label: 'Model', value: 'gpt-4o-mini', empty: false, comparison: 'same' }
        ]
      },
      tracking: { state: 'untracked' },
      excluded: 'API keys … are not shown here.'
    };
    const items = [
      {
        id: 'i-1',
        name: f.name,
        instance_number: 1,
        role: 'Drafts',
        custom_instructions: 'Draft quickly.'
      },
      {
        id: 'i-2',
        name: f.name,
        instance_number: 2,
        role: 'Edits',
        custom_instructions: 'Edit carefully.'
      }
    ];
    await page.route('**/settings-ownership*', route => {
      const chosen = new URL(route.request().url()).searchParams.get('instance_id');
      const selected = items.find(i => i.id === chosen);
      route.fulfill({
        json: {
          ...base,
          instances: selected
            ? { state: 'selected', items, selected_id: selected.id }
            : { state: 'ambiguous', items },
          prompt: selected
            ? {
                state: 'ok',
                base_source: 'workspace_copy',
                base: 'Base.',
                layers: [
                  {
                    key: 'custom_instructions',
                    label: 'Custom instructions',
                    value: selected.custom_instructions
                  }
                ],
                preview: `Base.\n\n---\n${selected.custom_instructions}`,
                note: 'Saved text only.'
              }
            : {
                state: 'needs_instance',
                base_source: 'workspace_copy',
                base: 'Base.',
                note: 'Saved text only.'
              }
        }
      });
    });
    await openAgent(page, f.name, '?ctx=workspace&studio_id=legacy-ws');
    const picker = page.getByLabel('Instance');
    await expect(picker).toHaveValue('');
    await expect(section(page)).toContainText('Choose an instance to see its workspace guidance');
    await expect(section(page)).not.toContainText('Draft quickly.');

    await picker.focus();
    await picker.selectOption('i-2');
    await expect(page).toHaveURL(/instance_id=i-2/);
    await expect(section(page)).toContainText('Edit carefully.');
    await expect(section(page)).not.toContainText('Draft quickly.');
    await expect(page.getByLabel('Instance')).toBeFocused();
  });
});

test.describe('slice 3', () => {
  const direct = (page: Page) => section(page).locator('[data-ownership-toolbox="direct"]');
  const pinned = (page: Page) => section(page).locator('[data-ownership-toolbox="workspace"]');

  test('Default and workspace Toolboxes are separate, without touching the Workshop', async ({
    page,
    request
  }) => {
    const f = await fixture(request);
    const toolboxCalls: string[] = [];
    const writes: string[] = [];
    page.on('request', req => {
      const url = req.url();
      if (/\/toolbox|\/workshop|\/default-toolbox/.test(url)) toolboxCalls.push(url);
      if (req.method() !== 'GET' && url.includes('/api/')) writes.push(url);
    });

    await openAgent(page, f.name);
    await expect(direct(page)).toContainText('Direct chat Default Toolbox');
    await expect(direct(page)).toContainText('Explicitly empty');
    await expect(direct(page)).toContainText('no editor for the Default Toolbox');
    await expect(pinned(page)).toHaveCount(0);

    await page.getByLabel('Viewing').selectOption({ label: `Workspace: ${f.custom.name}` });
    await expect(pinned(page)).toContainText('pinned to version 1');
    await expect(direct(page)).toContainText('outside workspaces');
    await pinned(page)
      .getByText(/selected connection/)
      .click();
    await expect(pinned(page)).toContainText('workspace-files');
    await expect(pinned(page)).toContainText('Every operation the connection allows');
    const access = section(page).locator('[data-ownership-permissions]');
    await expect(access).toContainText('Who controls access');
    await expect(access).toContainText('does not grant workspace access');
    await expect(access).toContainText('not allowed in this workspace');

    // Attached after startup: no assignment yet, stated as such.
    await page.getByLabel('Viewing').selectOption({ label: `Workspace: ${f.direct.name}` });
    await expect(pinned(page)).toContainText('no Toolbox assignment yet');

    expect(toolboxCalls).toEqual([]);
    expect(writes).toEqual([]);
  });

  test('an unreadable skill collection stays unknown (injected inventory failure)', async ({
    page,
    request
  }) => {
    const f = await fixture(request);
    await page.route('**/settings-ownership*', async route => {
      const response = await route.fetch();
      const body = await response.json();
      body.direct_toolbox = {
        state: 'selected',
        version: 4,
        inventory: 'unavailable',
        skills: [
          { id: 'summarize', name: 'Summarize', source: 'agent_learned', availability: 'unknown' }
        ]
      };
      await route.fulfill({ response, json: body });
    });
    await openAgent(page, f.name);
    await expect(direct(page)).toContainText('Selected skills (version 4)');
    await direct(page).getByText('1 selected skill').click();
    await expect(direct(page)).toContainText('Summarize');
    await expect(direct(page)).toContainText('Availability unknown');
    await expect(direct(page)).toContainText('could not be read');
    await expect(direct(page)).not.toContainText(/\bAvailable\b/);
  });
});

test.describe('slice 4', () => {
  const owners = (page: Page) => section(page).locator('[data-ownership-owners]');

  test('the roster and the workspace agent page open the matching context', async ({
    page,
    request
  }) => {
    const f = await fixture(request);
    const settingsReads: string[] = [];
    page.on('request', req => {
      if (req.url().includes('/settings-ownership')) settingsReads.push(req.url());
    });
    await page.goto(`/agents?agent=${encodeURIComponent(f.name)}&tab=workspaces`);
    const link = page.getByRole('link', {
      name: `Settings ${f.name} uses in ${f.custom.name}`
    });
    await expect(link).toBeVisible();
    // The Inspector stays lightweight: it never loads the ownership view.
    expect(settingsReads).toEqual([]);
    await link.click();
    await expect(page).toHaveURL(
      new RegExp(`ctx=workspace&studio_id=${f.custom.id}&tab=ownership`)
    );
    await expect(section(page)).toContainText(`In workspace “${f.custom.name}”`);
    await expect(row(page, 'model')).toContainText('gpt-4.1');

    await page.goto(`/workspaces/ownership-same/agents/${encodeURIComponent(f.name)}`);
    await page.getByRole('link', { name: "Where this agent's settings come from" }).click();
    await expect(section(page)).toContainText(`In workspace “${f.same.name}”`);
  });

  test('owner links reach the existing editors and Back keeps the context', async ({
    page,
    request
  }) => {
    const f = await fixture(request);
    await openAgent(page, f.name);
    await expect(owners(page)).toContainText('Configuration on this page');
    await expect(
      owners(page).getByRole('link', { name: 'Configuration on this page' })
    ).toHaveAttribute('href', '#agentConfigurationSection');
    const impact = section(page).locator('[data-ownership-impact]');
    await expect(impact).toContainText('What a library change reaches');
    await expect(impact).toContainText(`Independent copy: library changes don't reach it: `);
    await expect(impact).toContainText(f.custom.name);
    await expect(impact).toContainText(`Reads the library profile directly`);
    await expect(impact).toContainText(f.direct.name);
    await expect(impact).toContainText('role and description are not');
    await expect(impact).toContainText('API keys');

    await page.getByLabel('Viewing').selectOption({ label: `Workspace: ${f.custom.name}` });
    await expect(owners(page)).toContainText('every instance named Ownership Writer here');
    await expect(owners(page)).toContainText("No editor in Ori changes a workspace copy's role");
    await expect(owners(page)).toContainText('updates every instance named Ownership Writer');
    await expect(
      owners(page).getByRole('link', { name: 'Open the agent in its workspace' })
    ).toHaveAttribute('href', '/workspaces/ownership-custom?agent=ownership+writer');

    // The prompt owner: the workspace agent page, scrolled to its editor.
    await owners(page)
      .getByRole('link', { name: 'System prompt on the workspace agent page' })
      .click();
    await expect(page).toHaveURL(
      /\/workspaces\/ownership-custom\/agents\/Ownership%20Writer#wad-prompt$/
    );
    await expect(page.locator('#wad-prompt-view')).toContainText('Custom workspace prompt.');
    await expect(page.locator('#wad-prompt')).toBeInViewport();
    await page.goBack();
    await expect(section(page)).toContainText(`In workspace “${f.custom.name}”`);
    await expect(page.getByLabel('Viewing')).toHaveValue(`workspace:${f.custom.id}`);

    // The model owner: the workspace's model editor opens for this agent.
    await owners(page)
      .getByRole('link', { name: /Model editor in the workspace/ })
      .click();
    await expect(page).toHaveURL(/\/workspaces\/ownership-custom/);
    await expect(page.getByRole('dialog')).toContainText(/model/i);
  });

  test('an owner edit changes only its documented record (explicit sandbox write)', async ({
    request
  }) => {
    // A deliberate write through the existing workspace model editor endpoint,
    // separate from the read-only inspection steps above.
    const f = await fixture(request);
    const model = async (ws?: string) => {
      const q = ws ? `?studio_id=${ws}` : '';
      const res = await request.get(
        `/api/agents/${encodeURIComponent(f.name)}/settings-ownership${q}`
      );
      const body = await res.json();
      return body.definition.fields.find((x: any) => x.key === 'model').value;
    };
    const path = `/api/workspaces/${f.scratch.id}/agents/${encodeURIComponent(f.name)}`;
    try {
      const res = await request.patch(path, {
        data: { model: 'gpt-4.1-mini', llm_provider: 'openai' }
      });
      expect(res.ok(), await res.text()).toBeTruthy();
      expect(await model(f.scratch.id)).toBe('gpt-4.1-mini');
      expect(await model(f.same.id)).toBe('gpt-4o-mini');
      expect(await model()).toBe('gpt-4o-mini');
      expect(await model(f.custom.id)).toBe('gpt-4.1');
      expect(await model(f.direct.id)).toBe('gpt-4o-mini');
    } finally {
      await request.patch(path, { data: { model: 'gpt-4o-mini', llm_provider: 'openai' } });
    }
  });
});
