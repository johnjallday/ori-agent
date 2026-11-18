import type { Page } from '@playwright/test';
import { readFixture } from '../helpers/ui-consistency';
import { collectionAgents, collectionAgentName, collectionReads } from './ui-consistency-agents';

// Fictional, coherent GET snapshots only. These records never authorize a
// mutation, provisioning, hiring, a schedule, provider use or a map save.
export const homeLeafName =
  'Field lab — evidence and provenance across a very long research workspace name';
export const homeLeaf = {
  id: 'ui-home-field-lab',
  folder_slug: 'ui-home-field-lab',
  name: homeLeafName,
  kind: 'workspace',
  parent_id: 'ui-home-research',
  active: false,
  entry_agent_name: collectionAgentName,
  agent_count: 1,
  agents: [collectionAgentName],
  open_task_count: 3,
  needs_attention_count: 1,
  task_summary_available: true,
  completed_task_count: 2,
  mission: 'Keep literal <angles> as evidence, not markup. '.repeat(4),
  ticket_counts: { backlog: 0, ready: 2, in_progress: 0, review: 1, done: 2, cancelled: 0 },
  tags: ['evidence', 'a-long-tag-for-provenance-and-readable-workspace-context'],
  children: []
};
export const homeHQ = {
  id: 'ui-home-hq',
  folder_slug: 'ui-home-hq',
  name: 'Fictional Personal HQ',
  kind: 'workspace',
  workspace_role: 'personal_hq',
  active: false,
  entry_agent_name: 'Atlas',
  agent_count: 1,
  agents: ['Atlas'],
  open_task_count: 1,
  needs_attention_count: 0,
  task_summary_available: true,
  ticket_counts: { backlog: 0, ready: 1, in_progress: 0, review: 0, done: 0, cancelled: 0 },
  children: []
};
export const homeTree = [
  homeHQ,
  {
    id: 'ui-home-research',
    folder_slug: 'ui-home-research',
    name: 'Research collection',
    kind: 'group',
    children: [
      homeLeaf,
      {
        id: 'ui-home-reading',
        folder_slug: 'ui-home-reading',
        name: 'Quiet reading room',
        kind: 'workspace',
        parent_id: 'ui-home-research',
        active: false,
        open_task_count: 0,
        needs_attention_count: 0,
        task_summary_available: true,
        agent_count: 0,
        agents: [],
        ticket_counts: { backlog: 0, ready: 0, in_progress: 0, review: 0, done: 0, cancelled: 0 },
        children: []
      }
    ]
  }
];

export async function homeReads(
  page: Page,
  options: { empty?: boolean; unhired?: boolean; fail?: boolean } = {}
) {
  await collectionReads(page, { unhired: options.unhired });
  const atlas = {
    name: 'Atlas',
    source: 'user',
    role: 'general',
    status: 'idle',
    model: 'fictional-model',
    appearance: { mode: 'generated' },
    metadata: { description: 'Fictional established relationship, not an actual hire.' }
  };
  await readFixture(page, '**/api/agents/dashboard/list*', {
    agents: [...collectionAgents, atlas],
    xp_per_level: 100
  });
  await readFixture(page, '**/api/agents/Atlas/detail', atlas);
  await readFixture(page, '**/api/onboarding/status', { needs_onboarding: false, completed: true });
  await readFixture(page, /\/api\/personal-assistant$/, {
    personal_assistant: {
      state: options.unhired ? 'needs_hire' : 'active',
      state_version: 1,
      assistant_id: options.unhired ? '' : 'ui-home-atlas',
      display_name: 'Atlas',
      hq_workspace_id: options.unhired ? '' : homeHQ.id,
      next_action: options.unhired ? 'hire' : 'ask',
      availability: { model: { status: 'not_configured', available: false } }
    }
  });
  await readFixture(page, '**/api/personal-hq/status', {
    status: options.unhired
      ? { valid: false, hq_onboarding_state: 'unseen' }
      : {
          valid: true,
          workspace_id: homeHQ.id,
          folder_slug: homeHQ.folder_slug,
          provisioned_version: 1,
          current_version: 1
        }
  });
  await page.route('**/api/workspaces?tree=true', route => {
    if (route.request().method() !== 'GET') return route.fallback();
    return options.fail
      ? route.fulfill({ status: 503, json: { error: 'Fictional Home read failure' } })
      : route.fulfill({ json: { folders: options.empty ? [] : homeTree } });
  });
  await page.route(
    /\/api\/workspaces\/ui-home-[^/?]+\/(notes|tickets|files\/tree|outputs\/tree|directories|output-dir|memory|agents)(?:\?.*)?$/,
    route => {
      if (route.request().method() !== 'GET') return route.fallback();
      const url = new URL(route.request().url());
      const workspace = url.pathname.split('/')[3];
      const section = url.pathname.split('/').slice(4).join('/');
      const leaf = workspace === homeLeaf.id;
      const responses = {
        notes: {
          notes: leaf
            ? [{ id: 'ui-home-note', name: 'Evidence notes', updated_at: '2026-11-14T12:00:00Z' }]
            : []
        },
        tickets: {
          tickets: leaf
            ? [1, 2, 3].map(number => ({
                id: `ui-home-ticket-${number}`,
                title: `Fictional evidence ticket ${number}`,
                state: number === 1 ? 'review' : 'ready'
              }))
            : workspace === homeHQ.id
              ? [{ id: 'ui-home-hq-ticket', title: 'Read the shared library', state: 'ready' }]
              : []
        },
        'files/tree': { files: [] },
        'outputs/tree': { files: [] },
        directories: { directories: [] },
        'output-dir': { output_dir: '' },
        memory: { entries: [], managed_learnings: [] },
        agents: { agents: leaf ? [collectionAgents[0]] : workspace === homeHQ.id ? [atlas] : [] }
      };
      return route.fulfill({ json: responses[section] });
    }
  );
  await readFixture(page, '**/api/notes/ui-home-note', {
    id: 'ui-home-note',
    workspace_id: homeLeaf.id,
    name: 'Evidence notes',
    content: '# Evidence notes\n\nFictional read-only notes; literal <angles> are data.',
    updated_at: '2026-11-14T12:00:00Z',
    tags: ['evidence']
  });
  await readFixture(page, '**/api/sessions?folder_id=ui-home-*', { sessions: [], total: 0 });
  await readFixture(page, '**/api/workspace-map/layout', {
    layout: { schema_version: 1, revision: 1, positions: {}, groups: {}, snap_to_grid: true }
  });
  await readFixture(page, '**/api/personal-assistant/folder-agent', { folder_agents: [] });
  await readFixture(page, '**/api/orchestration/scheduled-tasks/upcoming*', { tasks: [] });
  await readFixture(page, '**/api/economy', {
    craft: 42,
    harvest: 18,
    creative_mode: false,
    energy: { used_today: 340000, daily_figure: 1000000 },
    farms: [],
    pending_by_workspace: {}
  });
  await readFixture(page, '**/api/workspace-updates?*', {
    items: [],
    has_more: false,
    next_after: ''
  });
  await readFixture(page, '**/api/activity/recent*', { activities: [] });
  await readFixture(page, '**/api/plugins/updates', { updates: [] });
  await readFixture(page, '**/api/calendar-ops/home-portal-summary', { available: false });
  await readFixture(page, '**/api/personal-assistant/today', {
    today: {
      state: 'not_ready',
      display_name: 'Atlas',
      needs_you: { items: [] },
      unavailable_sources: []
    }
  });
  await readFixture(page, '**/api/personal-assistant/setup/file-janitor', { setup: { run: null } });
  await readFixture(page, '**/api/progression', {
    current_tier: 1,
    total_tiers: 1,
    all_complete: false,
    tiers: [
      {
        tier: 1,
        name: 'Getting Started',
        quests: [
          {
            id: 'ui-read-one',
            title: 'Inspect the fictional workspace',
            status: 'completed',
            order: 1
          },
          { id: 'ui-read-two', title: 'Read the shared library', status: 'available', order: 2 }
        ]
      }
    ]
  });
}
