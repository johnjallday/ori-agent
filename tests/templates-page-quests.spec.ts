import { expect, test, type Page } from '@playwright/test';

const plugin = {
  id: 'plugin:reaper-plugin:reaper-song',
  name: 'Reaper Song',
  plugin_owner: { plugin_id: 'reaper-plugin', blueprint_id: 'reaper-song' },
  setup_quest: 'reaper_setup',
  agents: [{ name: 'Producer', role: 'specialist' }]
};
const local = {
  id: 'local-project',
  name: 'Local Project',
  agents: [{ name: 'Local Lead', role: 'orchestrator' }]
};
const quest = {
  plugin_id: 'reaper-plugin',
  id: 'reaper_setup',
  template_id: plugin.id,
  title: 'Music production setup',
  description: 'Build a group and prepare a project.',
  ownership: 'plugin'
};

async function library(page: Page) {
  const writes: string[] = [];
  page.on('request', request => {
    if (new URL(request.url()).pathname.startsWith('/api/') && request.method() !== 'GET')
      writes.push(request.url());
  });
  await page.route('**/api/onboarding/status', route =>
    route.fulfill({ json: { completed: true } })
  );
  await page.route('**/api/project-templates', route =>
    route.fulfill({ json: { templates: [plugin, local] } })
  );
  return writes;
}

async function select(page: Page, name: string) {
  await page.locator('#tplList [role="listitem"]').filter({ hasText: name }).click();
}

test('Templates quest discovery recovers without editing plugin declarations or blocking local templates', async ({
  page
}) => {
  const writes = await library(page);
  let available = false;
  await page.route('**/api/setup-quests', route =>
    available
      ? route.fulfill({ json: { quests: [quest] } })
      : route.fulfill({ status: 503, json: { error: 'unavailable' } })
  );
  await page.goto('/templates');
  await select(page, 'Reaper Song');
  await expect(page.locator('#tplQuestStatus')).toContainText('could not be loaded');
  await expect(page.locator('#tplQuestOpen')).toBeHidden();
  await expect(page.locator('#tplSaveBtn')).toBeDisabled();
  available = true;
  await page.locator('#tplQuestRetry').click();
  await expect(page.locator('#tplQuestHeading')).toHaveText(quest.title);
  await expect(page.locator('#tplQuestOwnership')).toContainText(
    'plugin-owned declaration · read-only'
  );
  await expect(page.locator('#tplQuestOpen')).toBeVisible();
  await expect(
    page.locator('#tplSetupQuest input, #tplSetupQuest textarea, #tplSetupQuest select')
  ).toHaveCount(0);
  await page.locator('#tplTabAgents').click();
  await expect(page.locator('#tplAgentsReadOnlyNotice')).toBeVisible();
  await expect(page.locator('#tplAgentsList .tpl-agent-card')).toHaveAttribute(
    'draggable',
    'false'
  );
  for (const control of await page
    .locator('#tplAgentsList input, #tplAgentsList textarea, #tplAgentsList select')
    .all()) {
    await expect(control).toBeDisabled();
  }
  await select(page, 'Local Project');
  await expect(page.locator('#tplQuestOpen')).toBeHidden();
  await expect(page.locator('#tplQuestOpen')).not.toHaveAttribute('href');
  await expect(page.locator('#tplQuestStatus')).toContainText(
    'does not have an available setup quest'
  );
  await expect(page.locator('#tplAgentsSaveBtn')).toBeEnabled();
  await page.locator('#tplTabOverview').click();
  await expect(page.locator('#tplEditName')).toBeEnabled();
  await expect(page.locator('#tplSaveBtn')).toBeEnabled();
  expect(writes).toEqual([]);
});

test('a late quest catalog response follows the current template instead of restoring a stale launch link', async ({
  page
}) => {
  const writes = await library(page);
  let release!: () => void;
  const held = new Promise<void>(resolve => {
    release = resolve;
  });
  await page.route('**/api/setup-quests', async route => {
    await held;
    await route.fulfill({ json: { quests: [quest] } });
  });
  await page.goto('/templates');
  await select(page, 'Reaper Song');
  await expect(page.locator('#tplQuestStatus')).toHaveText('Checking available setup…');
  await expect(page.locator('#tplQuestOpen')).toBeHidden();
  await select(page, 'Local Project');
  release();
  await expect(page.locator('#tplQuestStatus')).toContainText(
    'does not have an available setup quest'
  );
  await expect(page.locator('#tplQuestOpen')).toBeHidden();
  await expect(page.locator('#tplQuestOpen')).not.toHaveAttribute('href');
  await select(page, 'Reaper Song');
  await expect(page.locator('#tplQuestOpen')).toHaveAttribute(
    'href',
    '/?setup=quest&plugin=reaper-plugin&quest=reaper_setup'
  );
  expect(writes).toEqual([]);
});
