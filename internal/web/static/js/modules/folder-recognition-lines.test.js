import test from 'node:test';
import assert from 'node:assert/strict';

import './folder-recognition-lines.js';
import { folderOfferView } from './personal-assistant-folder.js';

const { recognitionLine, needsLine } = globalThis.OriFolderRecognitionLines;

test('the Recognized line says what Ori recognized, with the files only when they are known', () => {
  assert.equal(
    recognitionLine({
      verdict: 'project',
      label: 'REAPER project',
      marker_noun: 'session',
      marker_count: 3
    }),
    'Recognized: REAPER project · 3 session files'
  );
  assert.equal(
    recognitionLine({
      verdict: 'project',
      label: 'REAPER project',
      marker_noun: 'session',
      marker_count: 1
    }),
    'Recognized: REAPER project · 1 session file'
  );
  // Home's card keeps no count: the label alone.
  assert.equal(
    recognitionLine({ verdict: 'project', label: 'REAPER project' }),
    'Recognized: REAPER project'
  );
  assert.equal(recognitionLine({ verdict: 'project', label: '' }), '');
  assert.equal(recognitionLine({ verdict: 'none' }), 'Not a project Ori recognizes yet');
  assert.equal(
    recognitionLine(
      { verdict: 'collection', project_count: 4 },
      { collectionHint: 'Set them up on Home' }
    ),
    'Holds 4 projects · Set them up on Home'
  );
  assert.equal(recognitionLine({ verdict: 'collection', project_count: 1 }), '');
  for (const nothing of [null, undefined, 'project', { verdict: 'maybe' }]) {
    assert.equal(recognitionLine(nothing), '');
  }
});

test('the Needs line is the build host line’s words: installed for ready, the state otherwise', () => {
  // The same string sessionhttp's buildNeedsLine writes into a build's transcript.
  assert.equal(
    needsLine([
      { kind: 'integration', name: 'REAPER integration', state: 'ready' },
      { kind: 'provider', name: 'Music Project Management', state: 'install' }
    ]),
    'Needs: REAPER integration (installed) · Music Project Management (install)'
  );
  assert.equal(
    needsLine([
      { name: 'X', state: 'enable' },
      { name: '', state: 'install' }
    ]),
    'Needs: X (enable)'
  );
  assert.equal(needsLine([]), '');
  assert.equal(needsLine(null), '');
});

// FR29: Home's card says both lines in the build pane's words.
test('Home’s card shows the Recognized and Needs lines above its plan', () => {
  const offer = {
    id: 'offer-1',
    status: 'pending',
    verdict: 'project',
    folder: 'Ori Music Demo',
    subject: { name: 'Ori Music Demo', kind: 'project', shape: 'music_production' },
    capability: { recognized: 'REAPER', workspace: 'REAPER Song workspace', domain: 'music' },
    recognition: { verdict: 'project', label: 'REAPER project' },
    needs: [
      { kind: 'integration', name: 'REAPER integration', state: 'ready' },
      { kind: 'provider', name: 'Music Project Management', state: 'install' }
    ],
    plan: {
      digest: 'd',
      lines: [{ kind: 'home', name: 'Creates Home “Music Production Home”' }]
    }
  };
  const view = folderOfferView(offer);
  assert.equal(view.recognitionLine, 'Recognized: REAPER project');
  assert.equal(
    view.needsLine,
    'Needs: REAPER integration (installed) · Music Project Management (install)'
  );
  // Without a plan nothing is said about needs; once it runs, neither line.
  const planless = folderOfferView({ ...offer, plan: null });
  assert.equal(planless.needsLine, '');
  assert.equal(planless.recognitionLine, 'Recognized: REAPER project');
});
