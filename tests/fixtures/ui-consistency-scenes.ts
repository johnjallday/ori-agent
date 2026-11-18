// Fictional read fixtures only. A configured-looking provider here does not
// certify credentials or model execution. All local writes use the real API.
export const longModelName =
  'fictional-layout-model-with-a-very-long-unbroken-identifier-2026-11-14';
export const modelProviders = {
  providers: [
    {
      name: 'layout-fixture',
      display_name: 'Fictional provider for layout verification',
      type: 'local',
      available: true,
      requires_key: false,
      models: [{ value: longModelName, label: longModelName, pricing: 'Local' }]
    }
  ]
};

export const skillScene = {
  skills: [
    {
      name: 'fictional-long-skill-for-layout-and-permission-display-verification',
      description: 'A fictional read fixture. Script execution remains disabled until trusted.',
      source: 'repo',
      has_scripts: true,
      trusted: false,
      path: '/fictional/skills/a-long-path-for-provenance-layout/SKILL.md'
    },
    {
      name: 'fictional-local-notes',
      description: 'A fictional editable skill for filter coverage.',
      source: 'agent',
      has_scripts: false,
      trusted: true
    }
  ]
};

export const pluginScene = {
  plugins: [
    {
      name: 'fictional-plugin-with-a-long-name-for-responsive-layout-verification',
      version: '1.2.3',
      format: 'claude',
      enabled: false,
      description: 'Fictional installed read fixture, not a live integration.',
      mcp_servers: ['fictional-layout-service'],
      skills: ['fictional-layout-skill'],
      install_dir: '/fictional/plugins/a-long-directory-for-layout-verification'
    },
    {
      name: 'fictional-enabled-plugin',
      version: '1.0.0',
      format: 'codex',
      enabled: true,
      mcp_servers: [],
      skills: ['fictional-notes']
    }
  ]
};

export const templateScene = {
  templates_root:
    '/fictional/library/a-long-unbroken-folder-name-for-template-provenance-verification',
  templates: [
    {
      id: 'plugin:fictional-layout-plugin:long-template-identifier-for-provenance-verification',
      name: 'Field notes — plugin-supplied project template',
      description:
        'Fictional read-only library entry. No plugin is installed or executed by this scene.',
      tags: ['field-notes', 'a-very-long-unbroken-tag-for-filter-and-detail-layout-verification'],
      plugin_owner: {
        plugin_id: 'fictional-layout-plugin',
        blueprint_id: 'long-template-identifier-for-provenance-verification'
      },
      setup_quest: 'fictional_setup',
      agents: [
        { name: 'Fictional research coordinator with a long display name', role: 'orchestrator' }
      ]
    },
    {
      id: 'fictional-local-template',
      name: 'Local notebook',
      description: 'Fictional editable read fixture; writes are rejected.',
      tags: ['notebook'],
      agents: [{ name: 'Notebook coordinator', role: 'orchestrator' }]
    }
  ]
};
export const templateQuestScene = {
  quests: [
    {
      plugin_id: 'fictional-layout-plugin',
      id: 'fictional_setup',
      template_id: templateScene.templates[0].id,
      title: 'Plugin-supplied field notebook setup',
      description: 'Fictional declaration; opening setup is not part of this read tour.',
      ownership: 'plugin'
    }
  ]
};

export const mcpScene = {
  servers: [
    {
      name: 'fictional-server-with-a-long-unbroken-name-for-layout-verification',
      command: 'fictional-not-executable',
      args: ['--fictional-argument-with-a-long-value-for-layout'],
      enabled: false,
      transport: 'stdio'
    },
    {
      name: 'fictional-plugin/research-server',
      command: 'fictional-not-executable',
      args: [],
      enabled: true,
      transport: 'stdio'
    }
  ],
  stats: {
    'fictional-server-with-a-long-unbroken-name-for-layout-verification': { status: 'error' },
    'fictional-plugin/research-server': { status: 'running', tool_count: 3 }
  }
};
export const mcpRegistryScene = [
  {
    name: 'fictional-registry-server-with-a-long-unbroken-name-for-layout',
    description: 'Fictional registry read. No software is installed or connected.',
    command: 'fictional-not-executable',
    category: 'research',
    source: 'Fictional registry with a long display name',
    tags: ['notes']
  }
];
export const mcpSourcesScene = [
  { id: 'fictional-built-in', name: 'Fictional built-in registry', is_builtin: true },
  {
    id: 'fictional-custom',
    name: 'Fictional registry with a long display name',
    url: 'https://example.test/a-long-unbroken-registry-path-for-provenance-layout/registry.json',
    is_builtin: false
  }
];

export const modelsLibraryScene = {
  providers: [
    {
      name: 'fictional-cloud',
      display_name: 'Fictional cloud provider with a long display name',
      type: 'cloud',
      available: false,
      requires_key: true,
      models: [
        {
          value: longModelName,
          label: longModelName,
          pricing: 'Fictional pricing metadata',
          good_for: ['Long-form analysis and citation verification', 'Notes']
        },
        { value: 'fictional-legacy-model', label: 'Fictional legacy model', is_legacy: true }
      ]
    },
    {
      name: 'fictional-local',
      display_name: 'Fictional local provider',
      type: 'local',
      available: true,
      requires_key: false,
      models: [
        {
          value: 'fictional-local-model',
          label: 'Fictional local model',
          deprecation_date: '2099-12-31',
          pricing: 'Local'
        }
      ]
    }
  ]
};
export const modelCategoriesScene = {
  categories: [
    {
      id: 'fictional-default',
      name: 'Fictional default category',
      color: '#ffffff',
      icon: 'book',
      is_default: true,
      is_hidden: false
    },
    {
      id: 'fictional-custom',
      name: 'Fictional long category for narrow assignments',
      color: '#ffff00',
      icon: 'code-slash',
      is_default: false,
      is_hidden: false
    },
    {
      id: 'fictional-hidden',
      name: 'Fictional hidden category',
      color: '#3b82f6',
      icon: 'gear',
      is_default: true,
      is_hidden: true
    }
  ],
  model_assignments: { [longModelName]: ['fictional-custom'] },
  view_preference: 'provider',
  predefined_colors: [
    '#ffffff',
    '#ffff00',
    '#3b82f6',
    '#ef4444',
    '#10b981',
    '#f59e0b',
    '#8b5cf6',
    '#ec4899'
  ],
  predefined_icons: [
    'book',
    'code-slash',
    'tools',
    'chat-dots',
    'search',
    'lightbulb',
    'gear',
    'file-text',
    'graph-up',
    'pencil',
    'cpu',
    'globe',
    'shield-check',
    'terminal',
    'robot',
    'magic'
  ],
  max_categories: 20
};

export const workflowScene = {
  templates: [
    {
      id: 'fictional-read-only-orchestration-skill-with-a-long-identifier',
      name: 'Fictional release evidence and independent review coordination',
      description:
        'Fictional presentation fixture; no orchestration skill is executed or installed.',
      category: 'long-category-for-orchestration-filter-layout',
      source: 'builtin',
      parameters: [
        {
          name: 'evidence_folder',
          type: 'string',
          description: 'Fictional input location',
          required: true,
          default_value: 'fictional-only'
        }
      ],
      steps: [
        {
          id: 'collect-evidence',
          name: 'Collect fictional evidence',
          description: 'Summarize fictional notes without external actions.',
          role: 'researcher',
          depends_on: [],
          priority: 3,
          timeout: 600000000000,
          context: { fictional: true }
        },
        {
          id: 'independent-review',
          name: 'Independent fictional review',
          description: 'Inspect the fictional evidence; never execute for photography.',
          role: 'validator',
          depends_on: ['collect-evidence'],
          priority: 3,
          timeout: 600000000000,
          context: {}
        }
      ],
      orchestration_mode: 'graph',
      result_combination_mode: 'structured_outputs',
      output_schema: {
        name: 'fictional_review',
        description: 'Fictional structured result',
        strict: true,
        fields: [
          { name: 'a-long-output-field-for-readable-wrapping', type: 'string', required: true }
        ]
      }
    },
    {
      id: 'fictional-custom-orchestration',
      name: 'Fictional editable orchestration',
      description:
        'Fixture reads only; persistence is verified separately with a uniquely owned real record.',
      category: 'notes',
      source: 'custom',
      parameters: [],
      steps: [
        {
          id: 'notes',
          name: 'Fictional notes',
          description: 'Keep a local fictional draft.',
          role: 'general',
          priority: 3,
          timeout: 600000000000,
          context: {}
        }
      ],
      orchestration_mode: 'graph',
      result_combination_mode: 'last_result'
    }
  ]
};

export const usageSummaryScene = {
  today: { requests: 1234, tokens: 987654, cost: 12.3456, currency: 'USD' },
  this_month: { requests: 23456, tokens: 12345678, cost: 234.5678, currency: 'USD' },
  all_time: { requests: 123456, tokens: 123456789, cost: 1234.5678, currency: 'USD' }
};
export const usageDetailsScene = {
  by_provider: {
    fictional: {
      provider: 'fictional-provider-with-a-long-display-name',
      requests: 1234,
      total_tokens: 987654,
      total_cost: 12.3456
    }
  },
  by_agent: {
    fictional: {
      agent_name: 'Fictional research and independent review coordinator',
      requests: 1234,
      total_tokens: 987654,
      total_cost: 12.3456
    }
  },
  by_model: {
    fictional: {
      model: longModelName,
      provider: 'fictional-provider',
      requests: 1234,
      total_tokens: 987654,
      total_cost: 12.3456
    }
  }
};
export const reviewIssuesScene = {
  issues: [
    {
      id: 'fictional-issue',
      issue_type: 'user_retry',
      agent_name: 'Fictional coordinator with a long agent display name',
      tool_name: 'fictional-only-tool-with-a-long-identifier',
      occurrence_count: 1234,
      context_summary: 'Fictional stored review content, not a live review result. '.repeat(12),
      created_at: '2026-11-14T12:00:00Z'
    },
    {
      id: 'fictional-error',
      issue_type: 'ignored_error',
      agent_name: 'Fictional notes agent',
      tool_name: 'fictional-parser',
      occurrence_count: 2,
      context_summary: '<Fictional literal error> — no commands or tools are executed.',
      created_at: '2026-11-14T12:00:00Z'
    }
  ]
};
export const reviewRunsScene = {
  runs: [
    {
      id: 'fictional-failed-run',
      status: 'failed',
      started_at: '2026-11-14T12:00:00Z',
      completed_at: '2026-11-14T12:01:00Z',
      sessions_reviewed: 12,
      issues_found: 2,
      error_message: 'Fictional failed stored run with a long explanation. '.repeat(8)
    },
    {
      id: 'fictional-completed-run',
      status: 'completed',
      started_at: '2026-11-13T12:00:00Z',
      completed_at: '2026-11-13T12:01:00Z',
      sessions_reviewed: 24,
      issues_found: 0
    }
  ]
};

export const actionCenterScene = {
  items: [
    {
      id: 'fictional-long-finding',
      workspace_id: 'fictional-workspace',
      workspace_slug: 'fictional-review-workspace',
      workspace_name: 'Fictional workspace with a long name for scope display',
      title: 'Review fictional evidence — ' + 'LongFinding'.repeat(10),
      summary: 'Fictional stored finding; no agent or schedule was run to produce this row.',
      evidence: 'Fictional evidence with literal <metadata> and a long explanation. '.repeat(8),
      recommended_action: 'Review these fictional notes before deciding what to do.',
      priority: 'critical',
      status: 'new',
      updated_at: '2026-11-14T12:00:00Z'
    },
    {
      id: 'fictional-assistant-finding',
      workspace_id: 'fictional-workspace',
      workspace_slug: 'fictional-review-workspace',
      workspace_name: 'Fictional workspace with a long name for scope display',
      title: 'Fictional assistant suggestion',
      summary: 'A fixture label is not a live assistant recommendation.',
      source_type: 'assistant_suggestion',
      source_label: 'Fictional read fixture',
      source_url: '/workspaces/fictional-review-workspace/assistant',
      confidence: 'high',
      evidence: 'A fictional explanation; no personal data or provider result.',
      priority: 'high',
      status: 'new',
      seen_at: '2026-11-14T12:00:00Z',
      updated_at: '2026-11-14T12:00:00Z'
    },
    {
      id: 'fictional-planned-finding',
      workspace_id: 'fictional-workspace',
      workspace_slug: 'fictional-review-workspace',
      workspace_name: 'Fictional workspace',
      title: 'Fictional planned finding with an existing ticket link',
      summary: 'Fixture linked-ticket display only; no ticket is created.',
      linked_task_id: 'fictional-ticket',
      linked_workspace_slug: 'fictional-review-workspace',
      priority: 'low',
      status: 'planned',
      updated_at: '2026-11-14T12:00:00Z'
    }
  ]
};
export const actionCenterLibraryScene = {
  items: [
    {
      home_id: 'fictional-workspace',
      home_name: 'Fictional Home library with long provenance and suggestion text',
      new: 3,
      activatable: 2,
      ready_proposals: 1,
      coverage: 'partial',
      scanned_at: '2026-11-14T12:00:00Z',
      route: '/workspaces/fictional-home/assistant#projectLibraryProposals'
    }
  ]
};

export const principalWidths = [320, 390, 768, 1440] as const;
export const viewportHeight = 900;
export const themes = ['light', 'dark'] as const;
