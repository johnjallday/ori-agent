import { test } from 'node:test';
import assert from 'node:assert/strict';
import { homeFolderChips, pendingHomeFeeds } from './home-map-folders.js';

test('the scoped sheet uses its root, and a partial or unreached listing never says zero', () => {
  const folder = { name: 'Albums', workspace_id: 'a', songs: 2, to_set_up: 0, root_id: 'root' };
  const draw = (row, views = []) => homeFolderChips({ folders: [row] }, [], views);
  assert.match(draw({ ...folder, to_set_up: 4 }), /data-cmd-folder-root="root"[^>]*>4 to set up/);
  for (const html of [
    draw({ ...folder, partial: true }),
    draw(folder, [{ workspace_id: 'a', setup: { receipt: { unreached: 2 } } }])
  ]) {
    assert.match(html, /Listing incomplete/);
    assert.doesNotMatch(html, /0 to set up/);
  }
  assert.doesNotMatch(draw({ ...folder, root_id: '' }), /data-cmd-folder-root/);
});

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
