import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

const source = readFileSync(new URL('./templates-page.js', import.meta.url), 'utf8');
const page = readFileSync(
  new URL('../../../templates/pages/templates.tmpl', import.meta.url),
  'utf8'
);

test('user setup quest editor exposes only the fixed five-stage copy surface', () => {
  const kinds = source.match(/const tplUserQuestKinds = \[([\s\S]*?)\];/)?.[1] || '';
  const expected = [
    'integration_install',
    'project_connect',
    'workspace_setup',
    'assistant_program_staffing',
    'summary'
  ];
  assert.deepEqual(
    [...kinds.matchAll(/\['([^']+)'/g)].map(match => match[1]),
    expected
  );
  assert.match(page, /Source · User template/);
  assert.match(page, /not their behavior/);
  assert.doesNotMatch(page, /tplUserQuest(?:Route|Command|Action|URL)/);
});

test('preview and save communicate zero effects and use optimistic replacement semantics', () => {
  assert.match(page, /Preview and save edit inert guidance only/);
  assert.match(page, /do not install software, create a workspace, launch an app, staff agents/);
  assert.match(source, /\/setup-quest\/preview/);
  assert.match(source, /if_revision: metadata\.revision/);
  assert.match(source, /user_setup_quest: null/);
  assert.match(page, /Preview — no setup started/);
  assert.match(source, /No setup was started/);
});

test('quest authoring has dirty-form and destructive removal guards', () => {
  assert.match(source, /Discard unsaved setup quest changes\?/);
  assert.match(source, /beforeunload/);
  assert.match(source, /Remove this setup quest from the template\?/);
  assert.match(
    source,
    /does not uninstall software or delete projects, workspaces, Homes, or agents/
  );
  assert.match(source, /changed in another window, so your edits were not saved/);
});
