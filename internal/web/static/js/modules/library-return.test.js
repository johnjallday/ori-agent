import test from 'node:test';
import assert from 'node:assert/strict';
import {
  clearLibraryReturn,
  libraryReturnURL,
  readLibraryReturn,
  validLibraryReturn,
  writeLibraryReturn
} from './library-return.js';

const NOW = Date.UTC(2026, 8, 30, 12, 0, 0);
const good = (extra = {}) => ({
  home_id: 'home-1',
  entry_id: 'entry-9',
  quest_id: 'install_ori_reaper',
  return_path: '/workspaces/music-home/assistant',
  created_at: NOW - 1000,
  ...extra
});

function withStorage(run, { throwing = false } = {}) {
  const original = Object.getOwnPropertyDescriptor(globalThis, 'sessionStorage');
  const map = new Map();
  Object.defineProperty(globalThis, 'sessionStorage', {
    configurable: true,
    value: {
      getItem: key => {
        if (throwing) throw new Error('blocked');
        return map.has(key) ? map.get(key) : null;
      },
      setItem: (key, value) => {
        if (throwing) throw new Error('blocked');
        map.set(key, String(value));
      },
      removeItem: key => {
        if (throwing) throw new Error('blocked');
        map.delete(key);
      }
    }
  });
  try {
    return run(map);
  } finally {
    if (original) Object.defineProperty(globalThis, 'sessionStorage', original);
    else delete globalThis.sessionStorage;
  }
}

test('a well-formed, fresh hint is accepted and normalized to its own fields only', () => {
  const hint = validLibraryReturn(good({ extra: 'ignored', path: '/etc/passwd' }), NOW);
  assert.deepEqual(hint, {
    home_id: 'home-1',
    entry_id: 'entry-9',
    quest_id: 'install_ori_reaper',
    return_path: '/workspaces/music-home/assistant',
    created_at: NOW - 1000
  });
});

test('a tampered, foreign, or stale hint is no hint at all', () => {
  const hostile = {
    'external URL': { return_path: 'https://evil.example/workspaces/x/assistant' },
    'scheme-relative': { return_path: '//evil.example/workspaces/x/assistant' },
    'dot segments': { return_path: '/workspaces/../settings/assistant' },
    'encoded dot segments': { return_path: '/workspaces/%2e%2e/assistant' },
    'query on the path': { return_path: '/workspaces/music-home/assistant?next=/x' },
    'hash on the path': { return_path: '/workspaces/music-home/assistant#x' },
    'another page': { return_path: '/settings' },
    'a project page': { return_path: '/workspaces/music-home' },
    'nested route': { return_path: '/workspaces/a/b/assistant' },
    'backslash path': { return_path: '/workspaces/a\\b/assistant' },
    'javascript URL': { return_path: 'javascript:alert(1)' },
    'blank path': { return_path: '' },
    'uppercase quest': { quest_id: 'Install_Ori_Reaper' },
    'path-shaped quest': { quest_id: '../install' },
    'blank quest': { quest_id: '' },
    'oversized quest': { quest_id: 'a'.repeat(65) },
    'oversized entry': { entry_id: 'e'.repeat(161) },
    'padded entry': { entry_id: ' entry-9 ' },
    'blank entry': { entry_id: '' },
    'number entry': { entry_id: 9 },
    'blank home': { home_id: '' },
    expired: { created_at: NOW - 60 * 60 * 1000 - 1 },
    'from the future': { created_at: NOW + 10 * 60 * 1000 },
    'no time': { created_at: undefined },
    'text time': { created_at: 'yesterday' }
  };
  for (const [name, patch] of Object.entries(hostile)) {
    assert.equal(validLibraryReturn(good(patch), NOW), null, name);
  }
  for (const value of [null, undefined, 'x', 42, [], true]) {
    assert.equal(validLibraryReturn(value, NOW), null);
  }
  // The edge of the window is still valid.
  assert.notEqual(validLibraryReturn(good({ created_at: NOW - 60 * 60 * 1000 }), NOW), null);
});

test('the hint round-trips through the tab’s storage and is cleared on request', () => {
  withStorage(map => {
    assert.equal(readLibraryReturn(NOW), null);
    assert.equal(
      writeLibraryReturn(
        {
          homeID: 'home-1',
          entryID: 'entry-9',
          questID: 'install_ori_reaper',
          path: '/workspaces/music-home/assistant'
        },
        NOW
      ),
      true
    );
    assert.equal(map.size, 1);
    assert.equal(readLibraryReturn(NOW + 5000).entry_id, 'entry-9');
    clearLibraryReturn();
    assert.equal(readLibraryReturn(NOW), null);
    assert.equal(map.size, 0);
  });
});

test('a hint that cannot be valid is never written, and a bad stored one is dropped', () => {
  withStorage(map => {
    assert.equal(
      writeLibraryReturn(
        { homeID: 'h', entryID: 'e', questID: 'install_ori_reaper', path: 'https://evil.example/' },
        NOW
      ),
      false
    );
    assert.equal(map.size, 0);
    map.set('ori:library-return', JSON.stringify(good({ return_path: '/settings' })));
    assert.equal(readLibraryReturn(NOW), null);
    assert.equal(map.size, 0, 'a malformed hint is removed');
    map.set('ori:library-return', '{not json');
    assert.equal(readLibraryReturn(NOW), null);
    map.set('ori:library-return', JSON.stringify(good({ created_at: NOW - 2 * 3600 * 1000 })));
    assert.equal(readLibraryReturn(NOW), null);
    assert.equal(map.size, 0, 'an expired hint is removed');
  });
});

test('blocked or missing storage means no hint, never an error', () => {
  withStorage(
    () => {
      assert.equal(readLibraryReturn(NOW), null);
      assert.equal(
        writeLibraryReturn(
          {
            homeID: 'h',
            entryID: 'e',
            questID: 'install_ori_reaper',
            path: '/workspaces/m/assistant'
          },
          NOW
        ),
        false
      );
      assert.doesNotThrow(() => clearLibraryReturn());
    },
    { throwing: true }
  );
  const original = Object.getOwnPropertyDescriptor(globalThis, 'sessionStorage');
  delete globalThis.sessionStorage;
  try {
    assert.equal(readLibraryReturn(NOW), null);
  } finally {
    if (original) Object.defineProperty(globalThis, 'sessionStorage', original);
  }
});

test('the return URL is the Home’s own page at its library, or nothing', () => {
  assert.equal(
    libraryReturnURL(good({ created_at: Date.now() })),
    '/workspaces/music-home/assistant#projectLibraryPanel'
  );
  assert.equal(libraryReturnURL(good({ return_path: '/settings', created_at: Date.now() })), '');
  assert.equal(libraryReturnURL(null), '');
});
