import { test, expect } from '@playwright/test';
import { cpSync, mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { installLocalCdn } from './helpers/offline-cdn';

// Actual installed host declarations and reviewed records, not mocked program
// responses. The native service is the repository's harmless Surface Demo;
// no catalog inspection, external integration, or model execution is requested.
test('declared Home, owning profile jump and resource Catalog remain real native destinations', async ({
  page,
  request
}) => {
  test.setTimeout(60000);
  await installLocalCdn(page);
  await page.route('**/api/onboarding/status', route =>
    route.fulfill({ json: { needs_onboarding: false, completed: true, skipped: true } })
  );
  const run = Date.now().toString(36);
  const plugin = `navigation-home-${run}`;
  const root = mkdtempSync(path.join(tmpdir(), 'ori-navigation-home-'));
  const source = path.join(root, plugin);
  const owned: string[] = [];
  const json = async (response: Awaited<ReturnType<typeof request.get>>) => {
    expect(response.ok(), await response.text()).toBeTruthy();
    return response.json();
  };
  const contract = (name: string) =>
    JSON.parse(
      readFileSync(path.resolve('internal/resourcecatalog/testdata/contract', name), 'utf8')
    );
  const save = (file: string, value: unknown) =>
    writeFileSync(file, JSON.stringify(value, null, 2) + '\n', { mode: 0o600 });
  try {
    cpSync(path.resolve('examples/plugins/workspace-surface-demo'), source, { recursive: true });
    const manifestFile = path.join(source, '.ori-plugin/plugin.json');
    const manifest = JSON.parse(readFileSync(manifestFile, 'utf8'));
    manifest.name = plugin;
    for (const identity of ['.claude-plugin/plugin.json', '.codex-plugin/plugin.json']) {
      const file = path.join(source, identity);
      const metadata = JSON.parse(readFileSync(file, 'utf8'));
      save(file, { ...metadata, name: plugin });
    }
    // Keep this fixture independent of any other enabled example contribution.
    manifest.capabilities = [];
    manifest.blueprints = [];
    manifest.services[0].operations.push(...contract('operations.json'));
    manifest.requires_host_features = [
      'template_group_requirements_v1',
      'independent_program_homes_v1',
      'resource_workspaces_v1',
      'home_profile_v1'
    ];
    const attachment = {
      ...contract('home-attachment.valid.json'),
      provider_plugin_id: plugin,
      blueprint_id: 'reference-shelf',
      project_team_id: 'reference-team',
      label: 'Reference shelf',
      description: 'A fixture catalog, inspected only by explicit request.'
    };
    manifest.assistant_program_homes = [
      {
        schema_version: 1,
        version: 1,
        id: 'research-navigation',
        station_name: 'Research Navigation Home',
        station_description: 'Coordinates reviewed research records.',
        default_primary_name: 'Portfolio Guide',
        hire_title: 'Staff this research Home',
        hire_description: 'Keep Home and project roles separate.',
        disabled_message: 'The fixture provider is unavailable.',
        roles: [
          {
            id: 'portfolio_lead',
            label: 'Portfolio Lead',
            required: true,
            primary: true,
            role: 'orchestrator',
            system_prompt:
              'Coordinate only reviewed fixture records; do not execute work on navigation.'
          }
        ],
        stages: [{ id: 'helper', label: 'Helper', accepted_completion_threshold: 0 }],
        reflection: {
          minimum_projects: 3,
          cadence_hours: 168,
          max_projects: 8,
          max_events_per_project: 8,
          max_candidates: 4,
          max_evidence: 4,
          rubric: 'Use reviewed evidence only.'
        },
        home_profile: {
          schema_version: 1,
          title: 'Research profile',
          intro: 'Reviewed project defaults.',
          fields: [{ id: 'defaults', kind: 'defaults', label: 'Project defaults' }]
        },
        allowed_project_attachments: [attachment]
      }
    ];
    const catalog = contract('declaration.valid.json');
    catalog.id = 'reference-shelf';
    catalog.blueprint_id = 'reference-shelf';
    catalog.service_id = 'demo-service';
    manifest.resource_catalog = catalog;
    manifest.blueprints.push({
      id: 'reference-shelf',
      version: 1,
      manifest: 'blueprints/reference-shelf/template.json',
      skeleton: 'blueprints/reference-shelf/project',
      capabilities: []
    });
    const template = contract('studio-template.valid.json');
    template.name = 'Reference shelf';
    template.description = 'Disposable resource-catalog navigation fixture.';
    template.tools = {};
    template.assistant_project.id = 'reference-team';
    template.assistant_project.home.provider_plugin_id = plugin;
    template.assistant_project.home.program_id = 'research-navigation';
    template.assistant_project.roles = [
      {
        id: 'reference_guide',
        label: 'Reference Guide',
        required: true,
        primary: true,
        role: 'orchestrator',
        system_prompt: 'Explain the fixture catalog; never inspect or execute on navigation.'
      }
    ];
    template.group_requirement.assistant_project_id = 'reference-team';
    const blueprintDir = path.join(source, 'blueprints/reference-shelf');
    mkdirSync(path.join(blueprintDir, 'project'), { recursive: true, mode: 0o750 });
    writeFileSync(path.join(blueprintDir, 'project/README.md'), '# Disposable fixture\n', {
      mode: 0o600
    });
    save(path.join(blueprintDir, 'template.json'), template);
    save(manifestFile, manifest);

    await json(await request.post('/api/plugins/install', { data: { source, confirm: false } }));
    await json(await request.post('/api/plugins/install', { data: { source, confirm: true } }));
    await json(await request.post(`/api/plugins/${plugin}/enable`));
    const choices = await json(await request.get('/api/workspaces/group-templates'));
    const choice = choices.group_templates.find(
      (entry: any) => entry.provider?.plugin_id === plugin
    );
    expect(choice).toBeTruthy();
    const selection = {
      group_template_id: choice.id,
      revision: choice.revision,
      name: `Research Home ${run}`
    };
    const review = await json(
      await request.post('/api/workspaces/group-templates/review', { data: selection })
    );
    const committed = await json(
      await request.post('/api/workspaces/group-templates/commit', {
        data: {
          ...selection,
          group_review_token: review.group_template_review.review_token,
          idempotency_key: `navigation-home-${run}`
        }
      })
    );
    const homeId = committed.group_template.home_workspace_id;
    owned.push(homeId);
    await json(
      await request.put(`/api/workspaces/${homeId}/roles/portfolio_lead`, {
        data: { mode: 'create', name: `Portfolio Guide ${run}` }
      })
    );
    const home = await json(await request.get(`/api/workspaces/${homeId}`));

    const project = {
      name: `Reference shelf ${run}`,
      template_id: `plugin:${plugin}:reference-shelf`,
      create_template_agents: true,
      role_staffing: [
        { role_id: 'reference_guide', mode: 'create', name: `Reference Guide ${run}` }
      ]
    };
    const projectReview = await json(
      await request.post('/api/workspaces', {
        data: { ...project, group_requirement_review: true }
      })
    );
    const created = await json(
      await request.post('/api/workspaces', {
        data: {
          ...project,
          group_review_token: projectReview.group_requirement_review.review_token,
          idempotency_key: `navigation-shelf-${run}`
        }
      })
    );
    const shelf = created.folder;
    owned.push(shelf.id);
    const declared = await json(await request.get(`/api/workspaces/${shelf.id}/assistant-program`));
    expect(declared.resource_workspace).toBe(true);
    expect(declared.declaration.station_name).toBe('Research Navigation Home');

    const writes: string[] = [];
    page.on('request', sent => {
      if (
        sent.method() !== 'GET' &&
        /\/(?:mission\/first-open|runs|resource-catalog\/(?:inspect|sources|folders))/.test(
          new URL(sent.url()).pathname
        )
      )
        writes.push(sent.url());
    });
    await page.goto(`/workspaces/${home.folder_slug}?mode=details`);
    const entry = page.locator('[data-assistant-program-entry]');
    await expect(entry).toHaveCount(1);
    await expect(entry).toHaveText('Research Navigation Home');
    await expect(page.locator('[data-resource-catalog-entry]')).toHaveCount(0);
    await entry.click();
    await expect(page).toHaveURL(new RegExp(`/workspaces/${home.folder_slug}/assistant$`));
    await expect(page.locator('#homeProfileJump')).toHaveText('Research profile ↓');
    await page.locator('#homeProfileJump').click();
    await expect(page).toHaveURL(/#homeProfilePanel$/);
    await expect(page.locator('#homeProfilePanel')).toBeVisible();
    await expect(page.locator('#homeProfileTitle')).toHaveText('Research profile');
    if (process.env.WORKSPACE_UI_EVIDENCE)
      await page.screenshot({
        path: 'tasks/evidence-workspace-ui-simplification/group-5/declared-profile.png',
        fullPage: true
      });

    await page.goto(`/workspaces/${shelf.folder_slug}?mode=details`);
    await expect(page.locator('[data-assistant-program-entry]')).toHaveText(
      'Research Navigation Home'
    );
    const catalogEntry = page.locator('[data-resource-catalog-entry]');
    await expect(catalogEntry).toHaveCount(1);
    await expect(catalogEntry).toHaveAttribute('href', `/workspaces/${shelf.folder_slug}/catalog`);
    await catalogEntry.click();
    await expect(page.locator('#resourceCatalogPage')).toBeVisible();
    await expect(page.locator('#resourceCatalogTitle')).toBeVisible();
    if (process.env.WORKSPACE_UI_EVIDENCE)
      await page.screenshot({
        path: 'tasks/evidence-workspace-ui-simplification/group-5/declared-catalog.png',
        fullPage: true
      });
    expect(writes).toEqual([]);
  } finally {
    for (const id of owned.reverse()) await request.delete(`/api/workspaces/${id}`).catch(() => {});
    await request.delete(`/api/plugins/${plugin}`).catch(() => {});
    rmSync(root, { recursive: true, force: true });
  }
});
