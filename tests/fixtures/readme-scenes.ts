// Deterministic fictional data shared by the README screenshot suite. These
// values deliberately contain no local paths, real accounts, provider state,
// or user-authored content. Keep IDs stable so every scene tells one story.

export const README_CAPTURE_CLOCK = '2026-07-17T14:30:00.000Z';

export const README_SCENES = {
  personal_hq: {
    id: 'ws-personal-hq',
    name: 'Northstar Personal HQ',
    kind: 'personal',
    status: 'active',
    updated_at: '2026-07-17T14:20:00.000Z',
  },
  workspaces: [
    {
      id: 'ws-personal-hq',
      name: 'Northstar Personal HQ',
      kind: 'personal',
      status: 'active',
      updated_at: '2026-07-17T14:20:00.000Z',
      agent_count: 2,
      open_task_count: 3,
      entry_agent_name: 'Avery',
    },
    {
      id: 'ws-product-launch',
      name: 'Product Launch',
      kind: 'project',
      status: 'active',
      updated_at: '2026-07-17T13:45:00.000Z',
      agent_count: 3,
      open_task_count: 5,
      entry_agent_name: 'Mira',
    },
    {
      id: 'ws-research-lab',
      name: 'Research Lab',
      kind: 'project',
      status: 'active',
      updated_at: '2026-07-16T18:00:00.000Z',
      agent_count: 1,
      open_task_count: 2,
      entry_agent_name: 'Theo',
    },
  ],
  action_center: {
    items: [
      {
        id: 'finding-launch-risks',
        workspace_id: 'ws-product-launch',
        workspace_name: 'Product Launch',
        title: 'Resolve launch readiness risks',
        summary: 'Two launch tasks need a named owner before the review.',
        priority: 'high',
        status: 'new',
        seen_at: null,
        updated_at: '2026-07-17T14:00:00.000Z',
      },
      {
        id: 'finding-brief-review',
        workspace_id: 'ws-personal-hq',
        workspace_name: 'Northstar Personal HQ',
        title: 'Review your Daily Brief',
        summary: 'One follow-up is due today and ready to resume.',
        priority: 'medium',
        status: 'new',
        seen_at: '2026-07-17T13:30:00.000Z',
        updated_at: '2026-07-17T13:30:00.000Z',
      },
      {
        id: 'finding-research-decision',
        workspace_id: 'ws-research-lab',
        workspace_name: 'Research Lab',
        title: 'Choose the research direction',
        summary: 'The comparison note has a clear next decision.',
        priority: 'low',
        status: 'new',
        seen_at: null,
        updated_at: '2026-07-17T12:00:00.000Z',
      },
    ],
    total: 3,
  },
  daily_brief: {
    status: { status: 'ready' },
    config: {
      timezone: 'UTC',
      schedule_time: '09:00',
      schedule_days: ['mon', 'tue', 'wed', 'thu', 'fri'],
      schedule_enabled: true,
      notify_on_ready: false,
    },
    current: {
      revision: {
        id: 'brief-2026-07-17',
        local_date: '2026-07-17',
        status: 'ready',
        generated_at: '2026-07-17T14:20:00.000Z',
        content_json: JSON.stringify({
          opening_summary: 'You have a focused day with one launch decision to unblock.',
          needs_attention: [
            {
              title: 'Resolve launch readiness risks',
              workspace_name: 'Product Launch',
              reason: 'two tasks need ownership',
              ref: { entity_type: 'workspace', entity_id: 'ws-product-launch', workspace_id: 'ws-product-launch' },
            },
          ],
          todays_plan: [
            {
              title: 'Review the launch checklist',
              workspace_name: 'Product Launch',
              reason: 'planned for today',
              ref: { entity_type: 'workspace', entity_id: 'ws-product-launch', workspace_id: 'ws-product-launch' },
            },
          ],
          resume: [
            {
              title: 'Choose the research direction',
              workspace_name: 'Research Lab',
              last_known_state: 'Comparison note is ready.',
              next_step: 'Select the preferred approach.',
              ref: { entity_type: 'workspace', entity_id: 'ws-research-lab', workspace_id: 'ws-research-lab' },
            },
          ],
          suggested_next_actions: [
            {
              label: 'Open Product Launch',
              action_type: 'navigate',
              ref: { entity_type: 'workspace', entity_id: 'ws-product-launch', workspace_id: 'ws-product-launch' },
            },
          ],
        }),
      },
    },
  },
  workspace_command: {
    workspace_id: 'ws-product-launch',
    agents: [
      { name: 'Mira', role: 'Release lead', status: 'working' },
      { name: 'Theo', role: 'Research analyst', status: 'ready' },
    ],
    tasks: [
      { id: 'task-launch-checklist', title: 'Review the launch checklist', status: 'in_progress' },
      { id: 'task-owner-assignment', title: 'Assign owners to launch risks', status: 'todo' },
    ],
    notes: [{ id: 'note-launch-brief', title: 'Launch decision brief', status: 'ready' }],
    files: [{ id: 'file-launch-plan', name: 'launch-plan.md', status: 'synced' }],
    mission: { title: 'Ship a confident product launch', cadence: 'weekly', status: 'watching' },
  },
} as const;

export function assertReadmeSceneContract() {
  const requiredWorkspaceIDs = new Set(README_SCENES.workspaces.map((workspace) => workspace.id));
  if (!requiredWorkspaceIDs.has(README_SCENES.personal_hq.id)) {
    throw new Error('README fixtures must include the designated Personal HQ workspace.');
  }
  if (README_SCENES.action_center.items.length !== 3 || README_SCENES.action_center.total !== 3) {
    throw new Error('Action Center fixture must contain exactly three findings.');
  }
  for (const item of README_SCENES.action_center.items) {
    if (!item.id || !item.workspace_id || !item.title || !item.summary || !item.priority || !item.status) {
      throw new Error(`Action Center fixture is missing required response fields: ${item.id || 'unknown'}.`);
    }
    if (!requiredWorkspaceIDs.has(item.workspace_id)) {
      throw new Error(`Action Center fixture references an unknown workspace: ${item.workspace_id}.`);
    }
  }
  if (!README_SCENES.daily_brief.current.revision.content_json || README_SCENES.daily_brief.status.status !== 'ready') {
    throw new Error('Daily Brief fixture must contain a ready, populated revision.');
  }
  if (!README_SCENES.workspace_command.agents.length || !README_SCENES.workspace_command.tasks.length) {
    throw new Error('Workspace Command fixture requires agents and tasks.');
  }
}
