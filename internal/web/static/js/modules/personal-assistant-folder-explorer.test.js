import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import {
  explorerCountsLine,
  explorerPartialLine,
  focusStatusLine
} from './personal-assistant-folder-explorer.js';
import { folderTreeView } from './personal-assistant-folder-tree.js';

const observation = (extra = {}) => ({
  version: 1,
  id: 'a',
  folder: 'Albums',
  files: 15,
  projects: [
    { id: 'root', name: 'Albums', root: true, files: 15 },
    { id: 'c1', name: 'Album-1', files: 2 },
    { id: 'c2', name: 'Album-2', files: 2 }
  ],
  coverage: { max_depth: 3, max_entries: 5000, budget_seconds: 3, partial: false },
  tree: {
    nodes: [
      { id: 'entry-0', name: 'Album-1', kind: 'folder' },
      { id: 'entry-1', name: 'Song.rpp', kind: 'file', parent_id: 'entry-0' },
      { id: 'entry-2', name: 'Album-2', kind: 'folder' },
      { id: 'entry-3', name: 'Media', kind: 'folder', parent_id: 'entry-2' }
    ],
    omitted: 0
  },
  ...extra
});

test('the Tree pane says what was recorded in one line, privacy details in the tooltip', () => {
  const seen = observation();
  assert.equal(
    explorerCountsLine(seen, folderTreeView(seen)),
    '3 folders · 15 files · metadata only, nothing read'
  );
  // An omitted tail is said as "more than", never as an exact count.
  const omitted = observation({ tree: { ...observation().tree, omitted: 9 } });
  assert.match(explorerCountsLine(omitted, folderTreeView(omitted)), /^3\+ folders · 15 files/);
  // A saved snapshot with no tree counts its observed folders.
  assert.equal(
    explorerCountsLine(observation({ tree: undefined, files: 1 }), null),
    '2 folders · 1 file · metadata only, nothing read'
  );
  assert.equal(explorerCountsLine(null, null), '');
});

test('a partial scan says why in one line, and a complete one says nothing', () => {
  assert.equal(explorerPartialLine(observation()), '');
  assert.equal(
    explorerPartialLine(
      observation({
        coverage: { ...observation().coverage, partial: true, partial_reason: 'entries' }
      })
    ),
    'Partial scan · entry limit reached'
  );
  assert.equal(
    explorerPartialLine(
      observation({
        coverage: { ...observation().coverage, partial: true, partial_reason: 'time' }
      })
    ),
    'Partial scan · time limit reached'
  );
  assert.equal(
    explorerPartialLine(observation({ coverage: { ...observation().coverage, partial: true } })),
    'Partial scan · some entries were not recorded'
  );
});

test('the Focus status shows only while topics are checked', () => {
  assert.equal(focusStatusLine(0), '');
  assert.equal(focusStatusLine(1), '1 topic for the next message');
  assert.equal(focusStatusLine(15), '15 topics for the next message');
});

test('the default Tree pane carries no hint, status or coverage paragraph', () => {
  const template = readFileSync(
    new URL('../../../templates/components/ori-guide.tmpl', import.meta.url),
    'utf8'
  );
  const pane = template.slice(
    template.indexOf('id="personalAssistantFolderExplorer"'),
    template.indexOf('</aside>', template.indexOf('id="personalAssistantFolderExplorer"'))
  );
  for (const gone of [
    'Local snapshot',
    'Contents not read. Send shares metadata',
    'Bounded snapshot',
    'personalAssistantFolderExplorerStatus',
    'personalAssistantFolderExplorerCoverage'
  ])
    assert.equal(pane.includes(gone), false, `the pane still has ${gone}`);
  // Focus reveals the checks, Select all and the status line; they start hidden.
  assert.match(pane, /id="personalAssistantFolderFocusToggle"[^>]*aria-pressed="false"/);
  assert.match(pane, /id="personalAssistantFolderFocusControls"[^>]*hidden/);
  assert.match(pane, /id="personalAssistantFolderSelectionLine"[^>]*hidden/);
  assert.match(pane, /id="personalAssistantFolderExplorerPartial"[^>]*hidden/);
  // The conversation-area focus controls are gone; their work is the status line's.
  assert.equal(template.includes('Clear checks'), false);
  assert.equal(template.includes('id="personalAssistantFolderFocus"'), false);
  assert.equal(template.includes('Empty Send: “Explore this folder”'), false);
});
