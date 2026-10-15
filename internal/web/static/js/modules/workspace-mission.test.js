import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';

const source = readFileSync(new URL('./workspace-mission.js', import.meta.url), 'utf8');

// Loads the module with no goal card on the page, so init() bails and only
// the exposed API is exercised.
function loadWorkspaceMission() {
  const window = { currentWorkspaceId: 'ws-1' };
  const document = {
    readyState: 'complete',
    querySelector: () => null,
    querySelectorAll: () => [],
    addEventListener() {},
    body: { dataset: {} }
  };
  vm.runInNewContext(source, { window, document, console, setTimeout, clearTimeout });
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

test('a workspace with no goal asks for one so agents can suggest work (FR8)', () => {
  const mission = loadWorkspaceMission();
  const summary = mission.summarize({ mission: '' });
  assert.equal(summary.title, 'No goal yet');
  assert.equal(summary.text, 'Set a goal so your agents can suggest work');
  assert.equal(summary.canRun, false);
  assert.equal(summary.runTitle, 'Set a goal before running');
});
