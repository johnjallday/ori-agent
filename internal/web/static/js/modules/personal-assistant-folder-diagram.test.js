import test from 'node:test';
import assert from 'node:assert/strict';

import { folderDiagramVisible, folderReceiptView } from './personal-assistant-folder.js';

const visual = {
  entity: { kind: 'workspace', name: 'Album-4', operation: 'create' }
};

// The shape of a setup saved before the diagram carried its own data: lines and
// a destination, but no visual.
const preDiagramOffer = {
  id: 'offer-1',
  status: 'awaiting_outcome',
  folder: 'Album-4',
  subject: { name: 'Album-4' },
  setup: {
    status: 'stopped',
    stop_reason: 'interrupted',
    destination: { status: 'existing', name: 'Music Home', kind: 'plugin' },
    lines: [{ kind: 'workspace', name: 'Creates a workspace', state: 'done' }]
  }
};

test('a saved run without visual data draws no diagram', () => {
  assert.equal(folderDiagramVisible(preDiagramOffer, false), false);
  assert.equal(folderDiagramVisible({ ...preDiagramOffer, setup: undefined }, false), false);
  assert.equal(folderDiagramVisible(null, false), false);
  assert.equal(folderDiagramVisible(undefined), false);
});

test('a plan without visual data draws no diagram either', () => {
  const pending = { id: 'offer-2', status: 'pending', plan: { digest: 'd', lines: [] } };
  assert.equal(folderDiagramVisible(pending, false), false);
});

test('visual data on the run or the plan draws the diagram', () => {
  assert.equal(
    folderDiagramVisible({ ...preDiagramOffer, setup: { ...preDiagramOffer.setup, visual } }),
    true
  );
  assert.equal(
    folderDiagramVisible({ id: 'offer-3', status: 'pending', plan: { digest: 'd', visual } }),
    true
  );
});

test('a resolved receipt draws the diagram', () => {
  const resolved = {
    id: 'offer-4',
    status: 'resolved',
    folder: 'Album-4',
    subject: { name: 'Album-4' },
    outcome: {
      kind: 'project',
      route: '/workspaces/album-4',
      receipt: [{ kind: 'workspace', name: 'Album-4' }]
    }
  };
  const receipt = folderReceiptView(resolved);
  assert.equal(receipt.visible, true);
  assert.equal(folderDiagramVisible(resolved, receipt.visible), true);
});
