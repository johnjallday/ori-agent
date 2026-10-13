import test from 'node:test';
import assert from 'node:assert/strict';
import { placementChoiceView } from './personal-assistant-folder-setup.js';

test('a placement answer keeps known operations and builds setup links from quest keys only', () => {
  const view = placementChoiceView({
    message: "Setting this folder up needs Ori's reviewed Loom Studio integration.",
    options: [
      { operation: 'create_project_workspace', label: 'Review a separate project' },
      { operation: 'run_setup', label: 'Run setup now' }
    ],
    actions: [
      {
        type: 'open_setup_quest',
        label: 'Open Guided Setup: Loom Studio',
        arguments: { quest_id: 'install_ori_loom', source: 'host', display_name: 'Loom Studio' }
      },
      {
        type: 'open_setup_quest',
        label: 'Continue setup: Loom Studio',
        arguments: { quest_id: 'loom_setup', source: 'plugin', plugin_id: 'loom-plugin' }
      },
      // A link the server sent is never followed; without a key there is nothing to open.
      {
        type: 'open_setup_quest',
        label: 'Broken',
        href: '/elsewhere',
        arguments: { source: 'host' }
      },
      { type: 'open_workspace', label: 'Open', arguments: { quest_id: 'x', source: 'host' } }
    ]
  });
  assert.equal(
    view.message,
    "Setting this folder up needs Ori's reviewed Loom Studio integration."
  );
  assert.deepEqual(
    view.options.map(option => option.operation),
    ['create_project_workspace']
  );
  assert.deepEqual(view.setups, [
    {
      label: 'Open Guided Setup: Loom Studio',
      href: '/?setup=quest&source=host&quest=install_ori_loom'
    },
    {
      label: 'Continue setup: Loom Studio',
      href: '/?setup=quest&plugin=loom-plugin&quest=loom_setup'
    }
  ]);
});

test('a placement answer with nothing attached still has a sentence and no controls', () => {
  assert.deepEqual(placementChoiceView({}), {
    message: 'Choose the operation to review.',
    options: [],
    setups: []
  });
  assert.deepEqual(placementChoiceView(null).setups, []);
  assert.deepEqual(placementChoiceView({ actions: [{ type: 'open_setup_quest' }] }).setups, []);
});
