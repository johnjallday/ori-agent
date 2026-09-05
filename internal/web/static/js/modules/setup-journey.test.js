import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';

// The module's initializer is a no-op without a browser document.
globalThis.document ||= { readyState: 'complete', getElementById: () => null };
globalThis.window ||= { addEventListener() {}, location: { search: '' } };

const {
  newJourneyIdempotencyKey,
  setupJourneyControlDisabled,
  setupJourneyCurrentStep,
  setupJourneyReceiptRows
} = await import('./setup-journey.js');

test('current step follows server identity and preserves an explicit rail selection', () => {
  const journey = {
    current_step_id: 'project',
    steps: [
      { id: 'integration', status: 'complete' },
      { id: 'project', status: 'current' },
      { id: 'staffing', status: 'pending' }
    ]
  };
  assert.equal(setupJourneyCurrentStep(journey).id, 'project');
  assert.equal(setupJourneyCurrentStep(journey, 'integration').id, 'integration');
  assert.equal(
    setupJourneyCurrentStep({ steps: [{ id: 'pending', status: 'pending' }] }).id,
    'pending'
  );
});

test('file-only receipt stays honest about unconfigured and untested live control', () => {
  const rows = setupJourneyReceiptRows(
    {},
    {
      kind: 'workspace_setup',
      workspace_setup: {
        mode_label: 'File-only',
        files_connected: true,
        live_control_configured: false,
        live_control_tested: false
      }
    }
  );
  assert.deepEqual(rows, [
    ['Files connected', 'Yes'],
    ['Operating mode', 'File-only'],
    ['Live control configured', 'No'],
    ['Live control tested', 'No']
  ]);
});

test('busy work keeps close available except during the atomic commit section', () => {
  assert.equal(setupJourneyControlDisabled(true, false, true), false);
  assert.equal(setupJourneyControlDisabled(true, false, false), true);
  assert.equal(setupJourneyControlDisabled(true, true, true), true);
  assert.equal(setupJourneyControlDisabled(false, true, false), false);
});

test('journey shell has labelled progress, associated errors, live status, focus targets, and responsive CSS', async () => {
  const template = await readFile(
    new URL('../../../templates/components/modals.tmpl', import.meta.url),
    'utf8'
  );
  const css = await readFile(new URL('../../css/setup-journey.css', import.meta.url), 'utf8');
  assert.match(template, /aria-label="Setup progress"/);
  assert.match(template, /data-bs-keyboard="false"/);
  assert.match(
    template,
    /aria-describedby="specialistSetupJourneyStepDescription specialistSetupJourneyError"/
  );
  assert.match(template, /id="specialistSetupJourneyError"[^>]+tabindex="-1"/);
  assert.match(template, /id="specialistSetupJourneyLiveStatus"[^>]+aria-live="polite"/);
  assert.match(css, /:focus-visible/);
  assert.match(css, /@media \(max-width: 760px\)/);
  assert.match(css, /@media \(prefers-reduced-motion: reduce\)/);
});

test('idempotency keys are non-empty and distinct', () => {
  const first = newJourneyIdempotencyKey();
  const second = newJourneyIdempotencyKey();
  assert.ok(first.length >= 16);
  assert.notEqual(first, second);
});
