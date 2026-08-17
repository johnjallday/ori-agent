import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

const source = readFileSync(new URL('./workspace-task.js', import.meta.url), 'utf8');
const template = readFileSync(
  new URL('../../../templates/pages/workspace-task.tmpl', import.meta.url),
  'utf8'
);

test('REAPER repair is a semantic keyboard action and generic Retry remains independently gated', () => {
  assert.match(
    template,
    /<a id="workspace-task-assist-repair"[^>]*href="#"[^>]*hidden>/,
    'the exact repair must be a native keyboard-operable link'
  );
  assert.match(source, /assistRepair\.setAttribute\('href', repair\?\.url \|\| '#'/);
  assert.match(source, /assistRetryBtn\.hidden = !this\.isAssistActionSuggested\('retry'\)/);
});

test('explicit file fallback uses semantic choice buttons and is disabled during submission', () => {
  assert.match(source, /<button\s+type="button"[\s\S]*data-assist-choice-id=/);
  assert.match(source, /this\.elements\.assistContinueBtn,[\s\S]*this\.elements\.assistSwitchBtn/);
  assert.match(source, /querySelectorAll\('button, input, select, textarea'\)/);
  assert.match(source, /control\.disabled = disabled/);
  assert.match(source, /choice_id:/, 'the selected fallback choice must be posted explicitly');
});

test('file fallback shows its unverified-live summary and does not invite forbidden free text', () => {
  assert.match(template, /id="workspace-task-assist-extra"/);
  assert.match(source, /workflowStep\?\.summary \|\|/);
  assert.match(source, /assistExtra\.hidden = workflowStep\?\.freeTextAllowed === false/);
  assert.match(source, /workflowStep\.freeTextAllowed === false[\s\S]*Choose 1 of/);
});
