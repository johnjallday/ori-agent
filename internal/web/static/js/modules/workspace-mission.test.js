import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const source = readFileSync(new URL('./workspace-mission.js', import.meta.url), 'utf8');

// Loads the module with no goal card on the page, so init() bails and only
// the exposed API is exercised.
function loadWorkspaceMission(options = {}) {
  const window = { currentWorkspaceId: 'ws-1' };
  const document = {
    readyState: 'complete',
    querySelector: selector => options.elements?.[selector] || null,
    querySelectorAll: () => [],
    addEventListener() {},
    body: { dataset: {} }
  };
  vm.runInNewContext(source, {
    window,
    document,
    console,
    clearTimeout,
    setTimeout: options.setTimeout || setTimeout,
    fetch: options.fetch
  });
  return window.workspaceMission;
}

test('a goal card leads with the goal text and drops the empty-state line (FR8)', () => {
  const mission = loadWorkspaceMission();
  const summary = mission.summarize({ mission: '  Keep the brand voice consistent  ' });
  assert.equal(summary.mission, 'Keep the brand voice consistent');
  assert.equal(summary.title, 'Keep the brand voice consistent');
  assert.equal(summary.text, '');
  assert.equal(summary.canRun, true);
});

// FR21: run history shows each run's counts; a failed run shows its error and
// keeps the raw answer behind "Show output", escaped.
test('run history lists counts and a failed run raw output', () => {
  const mission = loadWorkspaceMission();
  const html = mission.runHistoryHTML([
    {
      origin: 'manual',
      succeeded: false,
      parse_status: 'failed',
      error: 'the answer is not JSON',
      output_tail: 'I think <b>everything</b> is fine',
      finished_at: new Date().toISOString()
    },
    {
      origin: 'cadence',
      succeeded: true,
      tasks_created: 3,
      findings_created: 2,
      duplicates_skipped: 1,
      dropped_by_cap: 2,
      finished_at: new Date().toISOString()
    }
  ]);
  assert.match(html, /<strong>Failed<\/strong>\s*<span>Run now<\/span>/);
  assert.match(html, /the answer is not JSON/);
  assert.match(
    html,
    /<summary>Show output<\/summary><pre>I think &lt;b&gt;everything&lt;\/b&gt; is fine<\/pre>/
  );
  assert.match(html, /<strong>Completed<\/strong>\s*<span>Scheduled<\/span>/);
  assert.match(html, /3 tasks suggested · 2 findings · 1 duplicate skipped · 2 over the limit/);
  assert.equal((html.match(/Show output/g) || []).length, 1, 'only the failed run keeps output');
  assert.match(mission.runHistoryHTML([]), /No goal runs yet\./);
});

test('an unset autonomy level reads as Watch, the level a goal run applies', () => {
  const mission = loadWorkspaceMission();
  assert.equal(mission.effectiveAutonomy(''), 'watch');
  assert.equal(mission.effectiveAutonomy(undefined), 'watch');
  assert.equal(mission.effectiveAutonomy('watch'), 'watch');
  assert.equal(mission.effectiveAutonomy('propose'), 'propose');
});

// FR27: while a goal run executes — started anywhere — the card says so and
// Run now is disabled; a run that could not start shows why (FR24).
test('the card shows a run in flight and why a run could not start', () => {
  const mission = loadWorkspaceMission();
  const running = mission.summarize({ mission: 'Ship the launch', run_in_flight: true });
  assert.equal(running.label, 'Running');
  assert.equal(running.actionStatus, 'Looking for things to work on…');
  assert.equal(running.canRun, false);
  assert.equal(running.runTitle, 'A goal run is already in progress');

  const blocked = mission.summarize({
    mission: 'Ship the launch',
    needs_attention: 'Set up a model to get suggestions'
  });
  assert.equal(blocked.actionStatus, 'Set up a model to get suggestions');
  assert.equal(blocked.actionStatusKind, 'error');
  assert.equal(blocked.canRun, true);

  const idle = mission.summarize({ mission: 'Ship the launch' });
  assert.equal(idle.actionStatus, '');
});

test('a finished run reports its counts; a failed one its error', () => {
  const mission = loadWorkspaceMission();
  const done = mission.settleRun({
    done: true,
    failed: false,
    record: { succeeded: true, tasks_created: 3, findings_created: 2, duplicates_skipped: 1 }
  });
  assert.equal(
    done.message,
    'Run complete — 3 tasks suggested · 2 findings · 1 duplicate skipped.'
  );
  assert.equal(done.kind, null);

  const failed = mission.settleRun({
    done: true,
    failed: true,
    record: { succeeded: false, error: 'the answer is not JSON' }
  });
  assert.equal(
    failed.message,
    'Run failed: the answer is not JSON. See Goal Settings for details.'
  );
  const punctuated = mission.settleRun({
    done: true,
    failed: true,
    record: { succeeded: false, error: 'the model timed out.' }
  });
  assert.equal(
    punctuated.message,
    'Run failed: the model timed out. See Goal Settings for details.'
  );
  assert.equal(failed.kind, 'error');

  const slow = mission.settleRun({ done: false });
  assert.match(slow.message, /Still running/);
  assert.equal(
    mission.runCountsText({ tasks_created: 1, findings_created: 0 }),
    '1 task suggested · 0 findings'
  );
});

// The result line outlives a re-render of the card (the Backlog refresh at the
// end of a run re-renders it) and gives way to the next run.
test('a finished run keeps its result under the goal until the next run', () => {
  const mission = loadWorkspaceMission();
  const { message } = mission.settleRun({
    done: true,
    failed: false,
    record: { succeeded: true, tasks_created: 3, findings_created: 2 }
  });
  const after = mission.summarize({ mission: 'Ship the launch' });
  assert.equal(after.actionStatus, message);
  assert.equal(after.actionStatusKind, null);
  assert.equal(after.label, 'Completed');
  const next = mission.summarize({ mission: 'Ship the launch', run_in_flight: true });
  assert.equal(next.actionStatus, 'Looking for things to work on…');
  assert.equal(mission.summarize({ mission: '' }).actionStatus, '', 'no goal, no result line');
});

function lifecycleFixture(
  initial,
  firstResponse = { pending: true, first_run: { started: false } }
) {
  let state = initial;
  const requests = [];
  const mission = loadWorkspaceMission({
    elements: { '#workspace-detail-mission-text': {}, '#workspace-detail-mission-enabled': {} },
    setTimeout: fn => {
      queueMicrotask(fn);
      return 1;
    },
    fetch: async (url, options) => {
      requests.push([url, options?.method || 'GET']);
      if (url.endsWith('/first-open')) {
        if (firstResponse instanceof Error) throw firstResponse;
        return { ok: true, json: async () => firstResponse };
      }
      if (url.endsWith('/trigger')) {
        state = {
          ...state,
          first_run_pending: false,
          needs_attention: '',
          mission_runs: [{ run_id: 'retry', succeeded: true }]
        };
        return { ok: true, json: async () => ({ run_id: 'retry' }) };
      }
      return { ok: true, json: async () => state };
    }
  });
  return {
    mission,
    requests,
    setState: value => {
      state = value;
    }
  };
}

const flushLifecycle = () => new Promise(resolve => setImmediate(resolve));

test('blocked pending reloads never recursively POST and explicit Run now retries after repair', async () => {
  const f = lifecycleFixture({ mission: 'Draft chapters', first_run_pending: true });
  await f.mission.reload();
  await flushLifecycle();
  for (let i = 0; i < 4; i++) await f.mission.reload();
  assert.equal(f.requests.filter(([url]) => url.endsWith('/first-open')).length, 1);
  f.setState({
    mission: 'Draft chapters',
    first_run_pending: false,
    first_run_state: 'blocked',
    needs_attention: 'Complete setup, then choose Run now'
  });
  await f.mission.reload();
  assert.match(f.mission.getSummary().actionStatus, /Complete setup/);
  f.mission.runNow();
  await flushLifecycle();
  assert.equal(f.requests.filter(([url]) => url.endsWith('/trigger')).length, 1);
  assert.equal(f.requests.filter(([url]) => url.endsWith('/first-open')).length, 1);
});

test('an in-flight run takes precedence over a stale pending marker', async () => {
  const f = lifecycleFixture({
    mission: 'Draft chapters',
    first_run_pending: true,
    run_in_flight: true,
    run_in_flight_detail: { run_id: 'other-tab' }
  });
  await f.mission.reload();
  f.setState({
    mission: 'Draft chapters',
    first_run_pending: false,
    run_in_flight: false,
    mission_runs: [{ run_id: 'other-tab', succeeded: true }]
  });
  await flushLifecycle();
  assert.equal(f.requests.filter(([, method]) => method === 'POST').length, 0);
  assert.equal(f.mission.isRunning(), false);
});

test('failed first-open requests are not silently retried by reloads', async () => {
  const f = lifecycleFixture(
    { mission: 'Draft chapters', first_run_pending: true },
    new Error('offline')
  );
  await f.mission.reload();
  await flushLifecycle();
  assert.match(f.mission.getSummary().actionStatus, /could not be confirmed/);
  await f.mission.reload();
  assert.equal(f.requests.filter(([, method]) => method === 'POST').length, 1);
});

test('cleared or already attempted goals do not POST and consent wording distinguishes the two actions', async () => {
  for (const state of [
    { mission: '', first_run_pending: true },
    { mission: 'Read the project', first_run_pending: true, first_run_state: 'starting' },
    { mission: 'Read the project', read_consent_required: true, first_run_state: 'blocked' }
  ]) {
    const f = lifecycleFixture(state);
    await f.mission.reload();
    await flushLifecycle();
    assert.equal(f.requests.filter(([, method]) => method === 'POST').length, 0);
    if (state.read_consent_required) {
      assert.match(f.mission.getSummary().actionStatus, /Run now reads authorized project context/);
      assert.match(
        f.mission.getSummary().actionStatus,
        /Start first look runs only the separate assessment/
      );
    }
  }
});

// The five parts as the mission GET sends them (goal_charter_shape).
const SHAPE = [
  { name: 'Outcome', hint: 'What this workspace exists to produce or keep true.' },
  { name: 'Maintain', hint: 'What the agent keeps in good order on its own initiative.' },
  { name: 'Build', hint: 'What the agent should work towards that does not exist yet.' },
  { name: 'Watch', hint: 'What the agent looks for and reports when it changes.' },
  {
    name: 'Boundaries',
    hint: "What the agent must not do or touch, and what needs the user's say."
  }
];

// FR5: the fill button's three cases.
test('Use the charter shape fills an empty goal, wraps a sentence and never touches a charter', () => {
  const mission = loadWorkspaceMission();
  const empty = mission.charterFill('  ', SHAPE);
  assert.equal(
    empty,
    '## Outcome\n_What this workspace exists to produce or keep true._\n\n' +
      '## Maintain\n_What the agent keeps in good order on its own initiative._\n\n' +
      '## Build\n_What the agent should work towards that does not exist yet._\n\n' +
      '## Watch\n_What the agent looks for and reports when it changes._\n\n' +
      "## Boundaries\n_What the agent must not do or touch, and what needs the user's say._\n"
  );

  const wrapped = mission.charterFill('  help me write a newsletter \n', SHAPE);
  assert.match(
    wrapped,
    /^## Outcome\nhelp me write a newsletter\n\n## Maintain\n_What the agent keeps/
  );
  assert.equal((wrapped.match(/^## /gm) || []).length, 5);
  assert.doesNotMatch(wrapped, /_What this workspace exists/);

  for (const charter of [
    '## Build\nA release pipeline.',
    'Ship v2.\n**Boundaries**\nNever push to main.',
    '### watch:\nCI'
  ]) {
    assert.equal(mission.charterFill(charter, SHAPE), charter);
  }
  // A heading that names no part, or four hashes, is not a charter.
  assert.match(mission.charterFill('#### Outcome', SHAPE), /^## Outcome\n#### Outcome\n/);
  // Without the shape (state not loaded yet) the button changes nothing.
  assert.equal(mission.charterFill('a sentence', []), 'a sentence');
});

test('the goal count runs against 4,000 and the placeholder is the charter shape', () => {
  const mission = loadWorkspaceMission();
  assert.deepEqual({ ...mission.goalCount('héllo') }, { text: '5 / 4000', over: false });
  assert.deepEqual(
    { ...mission.goalCount('é'.repeat(4000)) },
    { text: '4000 / 4000', over: false }
  );
  assert.equal(mission.goalCount('a'.repeat(4001)).over, true);
  assert.equal(
    mission.charterPlaceholder(SHAPE.slice(0, 2)),
    '## Outcome\nWhat this workspace exists to produce or keep true.\n## Maintain\nWhat the agent keeps in good order on its own initiative.'
  );
});

// FR7: the card shows the Outcome line and, for a charter, the parts it has.
test('the Goal card shows the Outcome line and a Charter line only for a charter', () => {
  const mission = loadWorkspaceMission();
  const charter = mission.summarize({
    mission: '## Outcome\nA monthly newsletter.\n## Build\nA template.\n## Boundaries\nNever send.',
    goal_outcome: 'A monthly newsletter.',
    goal_parts: ['Outcome', 'Build', 'Boundaries']
  });
  assert.equal(charter.title, 'A monthly newsletter.');
  assert.equal(charter.charterLine, 'Charter · Outcome, Build, Boundaries');
  assert.match(
    charter.extrasHTML,
    /<p class="ws-cmd-mission-charter">Charter · Outcome, Build, Boundaries<\/p>/
  );

  const plain = mission.summarize({
    mission: 'Keep the brand voice consistent',
    goal_outcome: 'Keep the brand voice consistent',
    goal_parts: []
  });
  assert.equal(plain.title, 'Keep the brand voice consistent');
  assert.equal(plain.charterLine, '');
  assert.equal(plain.extrasHTML, '');

  // A state without goal_outcome (an older server) still shows the goal.
  assert.equal(mission.summarize({ mission: ' Ship it ' }).title, 'Ship it');
});

// FR14/FR16: the card shows the newest successful run's assessment as escaped
// paragraphs with bold part lead-ins, its origin and time, and Show earlier.
test('the Goal card shows the latest assessment from the newest successful run', () => {
  const mission = loadWorkspaceMission();
  const finished = new Date(Date.now() - 3 * 60 * 1000).toISOString();
  const state = {
    mission: '## Outcome\nA newsletter.\n## Boundaries\nNever send.',
    goal_outcome: 'A newsletter.',
    goal_parts: ['Outcome', 'Boundaries'],
    goal_charter_shape: SHAPE,
    mission_runs: [
      { origin: 'manual', succeeded: false, error: 'timed out', assessment: 'stale failed text' },
      {
        origin: 'hire_first_look',
        succeeded: true,
        finished_at: finished,
        assessment:
          'Outcome: nothing <b>written</b> yet.\nNo notes.\n\n**Build** A template first.\n\n## Watch\nNothing to watch.\n\nAside: not a part.'
      },
      { origin: 'cadence', succeeded: true, assessment: 'older run' }
    ]
  };
  const html = mission.summarize(state).extrasHTML;
  assert.match(html, /<strong>Latest assessment<\/strong><span>First look<\/span>/);
  assert.match(html, /<span title="[^"]+">3m ago<\/span>/);
  assert.match(
    html,
    /<p><strong>Outcome<\/strong> nothing &lt;b&gt;written&lt;\/b&gt; yet\.<br>No notes\.<\/p>/
  );
  assert.match(html, /<p><strong>Build<\/strong> A template first\.<\/p>/);
  assert.match(html, /<p><strong>Watch<\/strong> Nothing to watch\.<\/p>/);
  assert.match(html, /<p>Aside: not a part\.<\/p>/, 'only part names lead with a colon');
  assert.doesNotMatch(html, /stale failed text|older run/);
  assert.match(html, /data-cmd-mission-action="show-run-history">Show earlier<\/button>/);
  assert.ok(html.indexOf('Charter · Outcome, Boundaries') < html.indexOf('Latest assessment'));

  // No run with an assessment: no block. A run in flight keeps the previous one.
  assert.doesNotMatch(
    mission.summarize({ mission: 'Ship', mission_runs: [{ succeeded: true }] }).extrasHTML,
    /Latest assessment/
  );
  assert.match(
    mission.summarize({ ...state, run_in_flight: true }).extrasHTML,
    /nothing &lt;b&gt;written/
  );
});

// FR15: run history keeps each run's assessment behind a collapsed details.
test('run history shows a run assessment collapsed under Assessment', () => {
  const mission = loadWorkspaceMission();
  const html = mission.runHistoryHTML([
    { origin: 'manual', succeeded: true, tasks_created: 0, assessment: 'All <i>fine</i>.' },
    { origin: 'cadence', succeeded: true, tasks_created: 1 }
  ]);
  assert.match(
    html,
    /<details class="workspace-mission-run-assessment"><summary>Assessment<\/summary><p>All &lt;i&gt;fine&lt;\/i&gt;\.<\/p><\/details>/
  );
  assert.equal((html.match(/<summary>Assessment<\/summary>/g) || []).length, 1);
});

// FR13: an assessed run with nothing new to suggest is a success, said plainly.
test('a run that assessed and suggested nothing says so', () => {
  const mission = loadWorkspaceMission();
  const quiet = mission.settleRun({
    done: true,
    failed: false,
    record: {
      succeeded: true,
      tasks_created: 0,
      findings_created: 0,
      duplicates_skipped: 2,
      assessment: 'Everything is in order.'
    }
  });
  assert.equal(quiet.message, 'Assessed the workspace; nothing to suggest right now.');
  assert.equal(quiet.kind, null);
  const old = mission.settleRun({
    done: true,
    failed: false,
    record: { succeeded: true, tasks_created: 0, findings_created: 0 }
  });
  assert.equal(old.message, 'Run complete — 0 tasks suggested · 0 findings.');
});

// FR19: a plain goal whose newest run proposed a charter gets the Suggested
// charter block with Use and Edit; it goes once the goal changes.
const SUGGESTED = '## Outcome\nA monthly <newsletter>.\n## Boundaries\nNever send anything.';

function charterFixture() {
  let state = {
    mission: 'help me write a newsletter',
    goal_outcome: 'help me write a newsletter',
    goal_parts: [],
    goal_digest: 'plain-1',
    goal_charter_shape: SHAPE,
    mission_runs: [
      {
        origin: 'hire_first_look',
        succeeded: true,
        finished_at: new Date().toISOString(),
        assessment: 'Outcome: nothing yet.',
        suggested_charter: SUGGESTED,
        goal_digest: 'plain-1'
      }
    ]
  };
  const puts = [];
  const modalText = { value: '', placeholder: '', focus() {} };
  const mission = loadWorkspaceMission({
    elements: {
      '#workspace-detail-mission-text': {},
      '#workspace-detail-mission-enabled': {},
      '#workspace-detail-goal-modal-text': modalText
    },
    setTimeout: fn => {
      queueMicrotask(fn);
      return 1;
    },
    fetch: async (url, options) => {
      if (options?.method === 'PUT') {
        const body = JSON.parse(options.body);
        puts.push(body);
        state = {
          ...state,
          mission: body.mission,
          goal_outcome: 'A monthly <newsletter>.',
          goal_parts: ['Outcome', 'Boundaries'],
          goal_digest: 'charter-1'
        };
        return { ok: true, json: async () => ({}) };
      }
      return { ok: true, json: async () => state };
    }
  });
  return { mission, puts, modalText, state: () => state };
}

test('a suggested charter shows with Use and Edit while the goal is the plain one it was written for', async () => {
  const f = charterFixture();
  await f.mission.reload();
  const html = f.mission.getSummary().extrasHTML;
  assert.match(html, /<strong>Suggested charter<\/strong><span>First look<\/span>/);
  assert.match(html, /<p><strong>Outcome<\/strong> A monthly &lt;newsletter&gt;\.<\/p>/);
  assert.match(html, /<p><strong>Boundaries<\/strong> Never send anything\.<\/p>/);
  assert.match(html, /data-cmd-mission-action="use-suggested-charter">Use<\/button>/);
  assert.match(html, /data-cmd-mission-action="edit-suggested-charter">Edit<\/button>/);
  assert.ok(html.indexOf('Suggested charter') < html.indexOf('Latest assessment'));

  // Absent when the goal changed since the run, is a charter, or the newest
  // successful run proposed none.
  const base = f.state();
  for (const state of [
    { ...base, goal_digest: 'plain-2' },
    { ...base, goal_parts: ['Outcome'] },
    {
      ...base,
      mission_runs: [{ origin: 'manual', succeeded: true }, ...base.mission_runs]
    }
  ]) {
    assert.doesNotMatch(f.mission.summarize(state).extrasHTML, /Suggested charter/);
  }
});

test('Edit opens the Goal modal with the suggested charter; Use saves it and the block goes', async () => {
  const f = charterFixture();
  await f.mission.reload();
  f.mission.cardAction('edit-suggested-charter');
  assert.equal(f.modalText.value, SUGGESTED);
  assert.equal(f.puts.length, 0, 'Edit saves nothing');

  await f.mission.cardAction('use-suggested-charter');
  assert.deepEqual(f.puts, [{ mission: SUGGESTED }]);
  const after = f.mission.getSummary();
  assert.equal(after.title, 'A monthly <newsletter>.');
  assert.equal(after.charterLine, 'Charter · Outcome, Boundaries');
  assert.doesNotMatch(after.extrasHTML, /Suggested charter/);
});

test('a workspace with no goal asks for one so agents can suggest work (FR8)', () => {
  const mission = loadWorkspaceMission();
  const summary = mission.summarize({ mission: '' });
  assert.equal(summary.title, 'No goal yet');
  assert.equal(summary.text, 'Set a goal so your agents can suggest work');
  assert.equal(summary.canRun, false);
  assert.equal(summary.runTitle, 'Set a goal before running');
});
