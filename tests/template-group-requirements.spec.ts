import { expect, test, type Page } from '@playwright/test';

const program = {
  schema_version: 1,
  id: 'music-producer-assistant',
  station_name: 'Music Production Home',
  roles: [{ id: 'producer', name: 'Producer', scope: 'project' }]
};
const required = {
  schema_version: 1,
  policy: 'required',
  assistant_program_id: program.id,
  missing_home: 'offer_create',
  default_home_name: program.station_name
};
const source = {
  id: 'plugin:reaper-plugin:reaper-song',
  name: 'Reaper Song',
  description: 'Trusted candidate fixture',
  revision: 'a'.repeat(64),
  plugin_owner: { plugin_id: 'reaper-plugin', blueprint_id: 'reaper-song' },
  assistant_program: program,
  standalone_composition: {
    schema_version: 1,
    roles: [{ role_id: 'producer' }],
    omit_pre_workspace_setup: true
  },
  group_requirement: required,
  readiness: { state: 'ready' }
};
const variant = {
  ...source,
  id: 'reaper-standalone',
  name: 'Reaper Standalone',
  plugin_owner: undefined,
  revision: 'b'.repeat(64),
  variant_revision: 'c'.repeat(64),
  variant_source_state: 'ready',
  template_variant: {
    schema_version: 1,
    variant_id: 'tv_fixture',
    owner_user_id: 'local',
    source: {
      plugin_id: 'reaper-plugin',
      plugin_version: '0.6.0',
      blueprint_id: 'reaper-song',
      blueprint_version: 6,
      definition_digest: 'd'.repeat(64)
    }
  },
  group_requirement: { schema_version: 1, policy: 'none' }
};

async function select(page: Page, name: string) {
  await page.locator('#tplList [role="listitem"]').filter({ hasText: name }).click();
}

async function routeCommon(page: Page, templates: () => unknown[]) {
  await page.route('**/api/onboarding/status', route =>
    route.fulfill({ json: { completed: true } })
  );
  await page.route('**/api/setup-quests', route => route.fulfill({ json: { quests: [] } }));
  await page.route('**/api/project-templates', route =>
    route.fulfill({ json: { templates: templates() } })
  );
}

test('a Required plugin source is read-only and Customize creates an editable source-linked variant', async ({
  page
}) => {
  let catalog: unknown[] = [source];
  await routeCommon(page, () => catalog);
  let createBody: Record<string, unknown> | undefined;
  await page.route(
    '**/api/project-templates/plugin%3Areaper-plugin%3Areaper-song/variants',
    async route => {
      createBody = route.request().postDataJSON();
      catalog = [source, variant];
      await route.fulfill({ status: 201, json: { success: true, template: variant } });
    }
  );

  await page.goto('/templates');
  await select(page, 'Reaper Song');
  await expect(page.locator('#tplGroupSourceBadge')).toHaveText('Plugin source · read-only');
  await expect(page.locator('#tplGroupPolicy')).toHaveValue('required');
  await expect(page.locator('#tplGroupPolicy')).toBeDisabled();
  await expect(page.locator('#tplGroupComposition')).toContainText(
    'resolved from trusted source identity—not by name'
  );
  await page.locator('#tplGroupCustomizeBtn').click();
  await page.locator('#tplNameModalInput').fill('Reaper Standalone');
  await page.locator('#tplNameModalConfirm').click();

  await expect(page.locator('#tplGroupSourceBadge')).toHaveText('User-owned source variant');
  await expect(page.locator('#tplGroupPolicy')).toHaveValue('none');
  await expect(page.locator('#tplGroupPolicy')).toBeEnabled();
  expect(createBody).toMatchObject({ name: 'Reaper Standalone', group_requirement: required });
});

test('mobile preview states standalone consequences without creating a workspace or Home', async ({
  page
}) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await routeCommon(page, () => [variant]);
  const writes: string[] = [];
  page.on('request', request => {
    if (request.method() !== 'GET' && !request.url().includes('/group-requirement/preview')) {
      writes.push(`${request.method()} ${new URL(request.url()).pathname}`);
    }
  });
  let previewBody: Record<string, unknown> | undefined;
  await page.route(
    '**/api/project-templates/reaper-standalone/group-requirement/preview',
    async route => {
      previewBody = route.request().postDataJSON();
      await route.fulfill({
        json: {
          label: 'Preview — no workspace or Home created',
          standalone: {
            assistant_program: false,
            project_roles: ['Producer'],
            capabilities: ['reaper_live_control'],
            runtime_modes: ['file_only'],
            pre_workspace_setup: false
          }
        }
      });
    }
  );

  await page.goto('/templates');
  await select(page, 'Reaper Standalone');
  await expect(page.locator('#tplGroupPolicy')).toHaveAccessibleName('Policy');
  await expect(page.locator('#tplGroupComposition')).toContainText(
    'no Assistant Program Home, Home roster, project link'
  );
  await page.locator('#tplGroupPreviewBtn').click();
  await expect(page.locator('#tplGroupPreview')).toContainText(
    'Preview — no workspace or Home created'
  );
  await expect(page.locator('#tplGroupPreview')).toContainText(
    'Creates no Assistant Program Home or link.'
  );
  await expect(page.locator('#tplGroupPreview')).toContainText('Project roles: Producer.');
  expect(previewBody).toMatchObject({
    if_revision: variant.variant_revision,
    group_requirement: { schema_version: 1, policy: 'none' }
  });
  expect(writes).toEqual([]);
});
