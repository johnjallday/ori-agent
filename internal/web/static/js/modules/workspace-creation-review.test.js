import test from 'node:test';
import assert from 'node:assert/strict';
import {
  workspaceCreationReviewHTML,
  creationReviewSlotsHTML,
  creationReviewNavigationHTML
} from './workspace-creation-review.js';
import { placementPanelHTML } from './assisted-workspace-placement.js';

const blank = {
  ownerKind: 'ordinary',
  name: 'My notebook',
  goal: 'Keep notes',
  destination: 'Standalone workspace · Position does not join a district.',
  rows: [
    {
      label: 'Team',
      value: 'No agents. You can add teammates later.',
      action: 'team',
      actionLabel: 'Edit team'
    }
  ],
  facts: [
    { label: 'Now', value: 'Create one workspace. No tasks run.' },
    { label: 'Later', value: 'Add notes, files or teammates when you need them.' }
  ],
  reviewed: true,
  canPlace: true,
  canConfirm: true,
  confirmLabel: 'Create workspace',
  mapHint:
    'Click a valid position or press Enter to create this workspace. Escape returns to review.'
};

test('Blank uses the same review language without Janitor permissions, slots or inferred actions', () => {
  const html = placementPanelHTML(blank);
  for (const label of [
    'Make room for My notebook',
    'Workspace name',
    'Goal',
    'No agents',
    'Standalone workspace',
    'Before you confirm',
    'No tasks run.',
    'Use automatic placement',
    'Place &amp; create on map'
  ])
    assert.ok(html.includes(label), label);
  assert.doesNotMatch(
    html,
    /File Curator|scan|monitoring|Filed\/|Choose folder|data-placement-slot=/i
  );
  assert.doesNotMatch(html, /data-placement-action="confirm"|Use selected/);
  assert.equal(html, workspaceCreationReviewHTML(blank));
});

test('automatic selection exposes exactly one explicit action and never implies a completed operation', () => {
  const html = workspaceCreationReviewHTML({ ...blank, automatic: true });
  assert.match(html, /data-placement-action="confirm">Create workspace</);
  assert.match(html, /Automatic placement selected. Confirm below/);
  assert.equal((html.match(/data-placement-action="confirm"/g) || []).length, 1);
  assert.doesNotMatch(html, /Workspace created/);
});

test('absent review, pending edits, unavailable maps and conflicts cannot offer an enabled final gesture', () => {
  for (const state of [
    { reviewed: false },
    { dirty: true },
    { busy: true },
    { conflict: true },
    { closed: true }
  ]) {
    const html = workspaceCreationReviewHTML({ ...blank, automatic: true, ...state });
    assert.match(html, /data-placement-action="confirm" disabled/);
    assert.match(html, /data-placement-action="place" disabled/);
  }
  const html = workspaceCreationReviewHTML({ ...blank, automatic: true, canPlace: false });
  assert.match(html, /data-placement-action="place" disabled/);
  assert.match(html, /data-placement-action="confirm">Create workspace</);
});

test('teams and resources are declaration-driven text rows, not a fixed one-agent recipe', () => {
  const rows = [
    { label: 'Research lead', value: 'Ada · saved agent · required' },
    { label: 'Editor', value: 'Lin · new agent · optional' },
    { label: 'Coordinator', value: 'Shared Home role · host-filled' },
    { label: 'Repository', value: 'Reviewed link', detail: 'https://example.invalid/project' }
  ];
  const html = workspaceCreationReviewHTML({ ...blank, rows });
  for (const row of rows) assert.ok(html.includes(row.label) && html.includes(row.value));
  assert.equal((html.match(/class="assisted-placement__slot"/g) || []).length, 4);
  assert.doesNotMatch(html, /File Curator|scan|monitoring/i);
  assert.doesNotMatch(creationReviewSlotsHTML([]), /<section/);
});

test('untrusted names, resources, labels and action copy are escaped, not executable plans or markup', () => {
  const hostile = '<img src=x onerror="alert(1)">';
  const html = workspaceCreationReviewHTML({
    ...blank,
    name: hostile,
    goal: hostile,
    destination: hostile,
    mapHint: hostile,
    message: hostile,
    rows: [
      { label: hostile, value: hostile, detail: hostile, action: hostile, actionLabel: hostile }
    ],
    facts: [{ label: hostile, value: hostile }]
  });
  assert.doesNotMatch(html, /<img|onerror="alert/);
  assert.match(html, /&lt;img/);
  assert.match(html, /&quot;/);
});

test('the map has no competing editor; accepted progress and separate position results remain readable', () => {
  assert.equal(workspaceCreationReviewHTML({ ...blank, onMap: true }), '');
  const pending = workspaceCreationReviewHTML({ ...blank, onMap: true, accepted: true });
  assert.match(pending, /Reading status never continues creation/);
  assert.match(pending, /Read saved status/);
  assert.doesNotMatch(
    pending,
    /Cancel draft|data-placement-field=|data-placement-action="confirm"/
  );
  const result = workspaceCreationReviewHTML({
    ...blank,
    result: { name: 'Actual notebook' },
    positionFailed: true
  });
  assert.match(result, /Workspace created/);
  assert.match(result, /Actual notebook/);
  assert.match(result, /Retry position only/);
  assert.doesNotMatch(result, /data-placement-action="confirm"/);
});

test('the navigation shared with File Janitor preserves its distinct disclosure without baking it into Blank', () => {
  const html = creationReviewNavigationHTML({
    ...blank,
    mapHint: 'Click creates the workspace and scans.'
  });
  assert.match(html, /Click creates the workspace and scans/);
  assert.doesNotMatch(creationReviewNavigationHTML(blank), /scan/);
  const janitor = placementPanelHTML({
    draft: { name: 'Inbox' },
    team: [],
    folder: {},
    review: {},
    canPlace: true
  });
  for (const css of [
    'assisted-placement__header',
    'assisted-placement__slots',
    'assisted-placement__navigation'
  ]) {
    assert.ok(janitor.includes(css) && workspaceCreationReviewHTML(blank).includes(css));
  }
  assert.match(janitor, /aria-label="File Curator role"/);
  assert.match(janitor, /Prepare a filing review/);
});
