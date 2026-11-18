import type { Page } from '@playwright/test';
import { readFixture } from '../helpers/ui-consistency';
import { agentCreateReads } from './ui-consistency-agent-create';
import { collectionAgents, collectionAgentName } from './ui-consistency-agents';
import { longModelName, modelProviders } from './ui-consistency-scenes';

export const detailAgentName = collectionAgentName;
export const alternativeDetailModel = 'fictional-alternative-layout-model';
const description =
  'Fictional metadata with literal <angles>; no native account or work is connected. ';
const path = '/fictional/visual-evidence/' + 'a-long-unbroken-project-directory-'.repeat(5);
export const detailAgents = [
  {
    ...collectionAgents[0],
    provider: 'layout-fixture',
    temperature: 0.6,
    max_output_tokens: 4096,
    enabled_plugins: [],
    mcp_servers: [],
    statistics: { message_count: 42, token_usage: 12045, total_cost: 0.34 }
  },
  {
    name: 'Claude Code',
    source: 'cli',
    provider: 'claude',
    role: 'cli_agent',
    claude_sync: {
      settings: { model: longModelName, permissions: { defaultMode: 'Fictional read-only mode' } },
      agents: [{ name: 'Evidence <archivist>', description: description.repeat(3) }],
      mcpServers: [{ name: 'Fictional metadata only', transport: 'stdio' }],
      plugins: [{ name: 'Read-only <catalog> label' }],
      recentProjects: [{ path, lastCost: 0.12 }]
    }
  },
  {
    name: 'Codex',
    source: 'cli',
    provider: 'codex',
    role: 'cli_agent',
    codex_sync: {
      config: { model: longModelName, modelReasoningEffort: 'high' },
      agents: [{ name: 'Evidence <reviewer>', description: description.repeat(3) }],
      mcpServers: [
        { name: 'Fictional metadata only', transport: 'stdio', envNames: ['FICTIONAL_INPUT_NAME'] }
      ],
      skills: [{ name: 'Evidence <reader>', path }],
      rules: [{ name: 'Read-only <rule>', path }]
    }
  }
];

/** Only GET presentation. Native sync/refresh, account connection, status-line
 * mirroring, definition writes and execution are never simulated as success. */
export async function detailReads(page: Page) {
  await agentCreateReads(page);
  await readFixture(page, '**/api/workspaces', { workspaces: [] });
  await readFixture(page, '**/api/providers', {
    providers: modelProviders.providers.map(provider => ({
      ...provider,
      models: [
        ...provider.models,
        {
          value: alternativeDetailModel,
          label: 'Fictional alternative model for native keyboard selection',
          pricing: 'Local'
        }
      ]
    }))
  });
  await page.route(/\/api\/agents\/[^/]+\/detail(?:\?.*)?$/, route => {
    if (route.request().method() !== 'GET') return route.fallback();
    const name = decodeURIComponent(new URL(route.request().url()).pathname.split('/').at(-2)!);
    const agent = detailAgents.find(agent => agent.name === name);
    return route.fulfill({
      status: agent ? 200 : 404,
      json: agent || { error: 'Unknown presentation identity' }
    });
  });
  await readFixture(page, /\/api\/agents\/[^/]+\/settings-ownership(?:\?.*)?$/, {
    agent: detailAgentName,
    library: { state: 'present', owner: 'library' },
    contexts: { state: 'ok', workspaces: [] },
    context: { kind: 'library', state: 'ok' },
    definition: {
      state: 'ok',
      source: 'library',
      disabled: false,
      fields: [
        { key: 'model', label: 'Model', value: longModelName, empty: false, comparison: 'same' }
      ]
    },
    tracking: { state: 'untracked' },
    excluded: 'Credentials and native account contents are not part of this fictional scene.'
  });
}
