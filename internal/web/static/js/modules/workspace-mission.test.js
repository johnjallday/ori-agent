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

test('a workspace with no goal asks for one so agents can suggest work (FR8)', () => {
  const mission = loadWorkspaceMission();
  const summary = mission.summarize({ mission: '' });
  assert.equal(summary.title, 'No goal yet');
  assert.equal(summary.text, 'Set a goal so your agents can suggest work');
  assert.equal(summary.canRun, false);
  assert.equal(summary.runTitle, 'Set a goal before running');
});
