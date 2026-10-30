import test from 'node:test';
import assert from 'node:assert/strict';
import { folderPickCopy } from './folder-pick-copy.js';
import { folderOfferView, setupRunView } from './personal-assistant-folder.js';

const sentences = {
  moved: 'Album-5 has moved or been renamed since you showed it to me. Pick it again to carry on.',
  changed: 'Album-5 is not the folder you showed me: it was replaced. Pick it again to carry on.',
  lost: 'Ori no longer has Album-5 open. Pick it again to carry on.',
  expired: 'Ori no longer has Album-5 open. Pick it again to carry on.'
};

for (const [reason, sentence] of Object.entries(sentences)) {
  test(`folder review copy: ${reason}`, () => {
    assert.equal(folderPickCopy('Album-5', reason), sentence);
    for (const status of ['pending', 'awaiting_outcome']) {
      const view = folderOfferView({
        id: 'review',
        verdict: 'project',
        status,
        folder: 'Album-5',
        subject: { name: 'Album-5', kind: 'project' },
        needs_pick: true,
        needs_pick_reason: reason
      });
      assert.equal(view.question, sentence);
      assert.deepEqual(
        view.actions.map(action => action.id),
        ['repick']
      );
    }
    for (const variant of [
      {},
      { software: true },
      { structure: true },
      { inbox: true },
      { portfolio: true }
    ]) {
      const view = setupRunView(
        { status: 'stopped', stop_reason: 'needs_pick', needs_pick_reason: reason },
        'Album-5',
        variant
      );
      assert.equal(view.question, sentence);
    }
  });
}

test('legacy and unknown reasons do not invent a cause', () => {
  for (const reason of [undefined, 'unknown', 'toString', '__proto__']) {
    assert.equal(folderPickCopy('Album-5', reason), sentences.lost);
  }
});

test('a restored review renders its ordinary controls, not a picker', () => {
  const view = folderOfferView({
    id: 'review',
    status: 'pending',
    verdict: 'project',
    folder: 'Album-5',
    subject: { name: 'Album-5', kind: 'project' },
    create_available: true,
    needs_pick: false
  });
  assert.equal(view.needsPick, false);
  assert.ok(!view.actions.some(action => action.repick));
});
