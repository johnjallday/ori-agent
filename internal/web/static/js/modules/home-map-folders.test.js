import { test } from 'node:test';
import assert from 'node:assert/strict';
import { homeFolderChips, pendingHomeFeeds } from './home-map-folders.js';

test('folders are named in order, four on the bar; pending and placed never duplicate', () => {
  const folders = ['z', 'a', 'b', 'c', 'd'].map(name => ({ workspace_id: name, name, songs: 1 }));
  const pending = pendingHomeFeeds(
    [
      { workspace_id: 'z', origin_home_id: 'h', folder_name: 'Z <pending>', state: 'reported' },
      { workspace_id: 'x', origin_home_id: 'other', state: 'looking' }
    ],
    'h'
  );
  const html = homeFolderChips({ folders, outside: 1 }, pending);
  assert.match(
    html,
    /data-cmd-frame-folder="a"[\s\S]*data-cmd-frame-folder="d"[\s\S]*1 more folder[\s\S]*Z &lt;pending&gt; · Waiting on Set up/
  );
  assert.doesNotMatch(html, /data-cmd-frame-folder="z"|data-cmd-home-feed="x"/);
  assert.match(html, /1 other workspace/);
  assert.equal(homeFolderChips({ folders: [] }, []), '');
});
