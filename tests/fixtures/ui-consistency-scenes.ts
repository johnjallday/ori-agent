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

export const principalWidths = [320, 390, 768, 1440] as const;
export const viewportHeight = 900;
export const themes = ['light', 'dark'] as const;
