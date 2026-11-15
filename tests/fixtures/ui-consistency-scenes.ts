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

export const principalWidths = [320, 390, 768, 1440] as const;
export const viewportHeight = 900;
export const themes = ['light', 'dark'] as const;
