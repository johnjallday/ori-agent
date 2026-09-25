import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import {
  SHOW_FOLDER_QUEST_PARAM,
  showFolderQuestRequested,
  scrubbedQuestURL,
  startShowFolderQuest
} from './show-folder-quest.js';

test('the quest is requested only by ?quest=show-folder', () => {
  assert.equal(showFolderQuestRequested('?quest=show-folder'), true);
  assert.equal(showFolderQuestRequested('?view=list&quest=show-folder'), true);
  assert.equal(showFolderQuestRequested('?quest=meet-assistant'), false);
  assert.equal(showFolderQuestRequested(''), false);
  assert.equal(showFolderQuestRequested(undefined), false);
});

test('the quest parameter matches the mission action URL the server sends', () => {
  const quests = readFileSync(
    new URL('../../../../progression/quests.go', import.meta.url),
    'utf8'
  );
  assert.match(quests, new RegExp(`ShowFolderActionURL = "/\\?quest=${SHOW_FOLDER_QUEST_PARAM}"`));
});

test('scrubbing drops only the quest parameter and keeps the rest of the URL', () => {
  assert.equal(
    scrubbedQuestURL('http://localhost/?quest=show-folder&view=list#today'),
    '/?view=list#today'
  );
  assert.equal(scrubbedQuestURL('http://localhost/?quest=show-folder'), '/');
  assert.equal(scrubbedQuestURL('http://localhost/?view=list'), null, 'nothing to scrub');
  assert.equal(scrubbedQuestURL('not a url'), null);
});

test('starting opens the panel on Today, then the chooser', () => {
  const calls = [];
  const panel = {
    open(trigger, options) {
      calls.push(['panel', trigger, options.view]);
      return true;
    }
  };
  const folder = { open: () => calls.push(['chooser']) };
  const launcher = { id: 'launcher' };
  assert.equal(startShowFolderQuest({ panel, folder, launcher }), true);
  assert.deepEqual(calls, [['panel', launcher, 'today'], ['chooser']]);
});

test('a panel that cannot open yet leaves the chooser closed and reports it', () => {
  const calls = [];
  const panel = { open: () => false };
  const folder = { open: () => calls.push('chooser') };
  assert.equal(startShowFolderQuest({ panel, folder }), false);
  assert.deepEqual(calls, []);
  assert.equal(startShowFolderQuest({}), false, 'no panel at all');
  assert.equal(startShowFolderQuest({ panel: { open: () => true } }), true, 'no chooser is fine');
});

test('the module makes no request and never decides or completes anything', () => {
  const source = readFileSync(new URL('./show-folder-quest.js', import.meta.url), 'utf8');
  assert.doesNotMatch(source, /fetch\(/);
  assert.doesNotMatch(source, /\/api\//);
  assert.doesNotMatch(source, /progression/);
});
