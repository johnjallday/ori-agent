import type { Page } from '@playwright/test';
import { readFixture } from '../helpers/ui-consistency';
import { longModelName, modelProviders } from './ui-consistency-scenes';

// Coherent fictional GET records for gallery/inspector presentation, not saved
// definitions, successful mutations, CLI availability or provider execution.
export const collectionAgentName = `Lumen_${'evidence'.repeat(10)}`;
export const collectionAgents = [
  {
    name: collectionAgentName,
    source: 'user',
    source_kind: 'user',
    role: 'researcher',
    model: longModelName,
    llm_provider: 'layout-fixture',
    status: 'idle',
    version: 1,
    metadata: {
      description:
        'A fictional source-finder. Literal <angles> stay text. ' +
        'Keeps the evidence and uncertainty visible. '.repeat(5),
      favorite: true,
      tags: ['field-notes', 'an-unbroken-tag-for-readable-inspector-layout-and-filter-verification']
    },
    appearance: { mode: 'character', character: { catalog_id: 'research-archivist' } },
    evolution: { level: 2, stage: 'kindled', experience: 245 },
    capabilities: ['research', 'local-evidence'],
    workspaces: [],
    workspace_count: 0,
    system_prompt:
      'Fictional read-only prompt.\nLiteral <markup> is not HTML.\n' +
      'A long technical identifier: '.repeat(4) +
      'abcdefghij'.repeat(20)
  },
  {
    name: 'Bram — evidence editor',
    source: 'user',
    source_kind: 'user',
    role: 'writer',
    model: longModelName,
    llm_provider: 'layout-fixture',
    status: 'error',
    version: 1,
    metadata: {
      description: 'Fictional error-state identity; no job was attempted.',
      tags: ['field-notes'],
      favorite: false
    },
    appearance: { mode: 'generated' },
    evolution: { level: 0, stage: 'spark', experience: 0 },
    capabilities: [],
    workspaces: [],
    workspace_count: 0,
    system_prompt: 'Fictional editable library definition, read only in this tour.'
  },
  {
    name: 'Codex',
    source: 'cli',
    source_kind: 'cli',
    is_permanent: true,
    role: 'worker',
    model: 'fictional-cli-model',
    llm_provider: 'codex',
    status: 'disabled',
    metadata: {
      description: 'Fictional built-in metadata; native CLI is not executed.',
      tags: [],
      favorite: false
    },
    appearance: { mode: 'generated' },
    capabilities: [],
    workspaces: [],
    workspace_count: 0,
    system_prompt: ''
  }
];

export async function collectionReads(
  page: Page,
  options: { empty?: boolean; fail?: boolean; unhired?: boolean } = {}
) {
  const sceneAgents = () => (options.empty ? [] : collectionAgents);
  await readFixture(page, '**/api/updates/check', { updateAvailable: false });
  await readFixture(page, '**/api/vault/vaults', { count: 0, vaults: [] });
  await readFixture(page, /\/api\/personal-assistant$/, {
    personal_assistant: options.unhired
      ? {
          state: 'needs_hire',
          state_version: 1,
          availability: { model: { status: 'not_configured', available: false } }
        }
      : {
          state: 'active',
          state_version: 1,
          assistant_id: 'collection-read-scene',
          display_name: 'Atlas',
          hq_workspace_id: 'collection-read-hq',
          next_action: 'ask',
          availability: { model: { status: 'not_configured', available: false } }
        }
  });
  await readFixture(page, '**/api/providers', modelProviders);
  await readFixture(page, '**/api/usage/limits*', {
    providers: options.empty
      ? []
      : [
          {
            provider: 'codex',
            display_name: 'Codex',
            state: 'ok',
            level: 'warn',
            source: 'fictional_read_scene',
            fetched_at: new Date().toISOString(),
            windows: [{ id: 'weekly', label: 'Weekly', used_percent: 80, level: 'warn' }]
          }
        ]
  });
  await page.route('**/api/agents/dashboard/list*', route => {
    if (route.request().method() !== 'GET') return route.fallback();
    return options.fail
      ? route.fulfill({ status: 503, json: { error: 'Fictional list failure' } })
      : route.fulfill({ json: { agents: sceneAgents(), xp_per_level: 100 } });
  });
  await page.route(/\/api\/agents\/[^/]+\/(detail|evolution)(?:\?.*)?$/, route => {
    if (route.request().method() !== 'GET') return route.fallback();
    const url = new URL(route.request().url());
    const parts = url.pathname.split('/');
    const agent = sceneAgents().find(value => value.name === decodeURIComponent(parts.at(-2)!));
    if (!agent) return route.fulfill({ status: 404, json: { error: 'Unknown scene identity' } });
    return route.fulfill({
      json:
        parts.at(-1) === 'detail'
          ? agent
          : { evolution: 'evolution' in agent ? agent.evolution : {}, xp_per_level: 100 }
    });
  });
  await readFixture(page, /\/api\/skills\?agent=/, {
    skills: [],
    loadout: { slots_used: 0, slot_cap: 4, expert_mode: false }
  });
}
