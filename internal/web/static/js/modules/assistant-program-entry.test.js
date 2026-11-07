import test from 'node:test';
import assert from 'node:assert/strict';

import { AssistantProgramEntry } from './assistant-program-entry.js';

function withEntryDocument(callback) {
  const previous = globalThis.document;
  const inserted = [];
  const switcher = {
    querySelector(selector) {
      if (selector === '[data-assistant-program-entry]') {
        return inserted.find(item => Object.hasOwn(item.dataset, 'assistantProgramEntry')) || null;
      }
      if (selector === '[data-home-profile-entry]') {
        return inserted.find(item => Object.hasOwn(item.dataset, 'homeProfileEntry')) || null;
      }
      if (selector === '[data-resource-catalog-entry]') {
        return inserted.find(item => Object.hasOwn(item.dataset, 'resourceCatalogEntry')) || null;
      }
      return null;
    },
    appendChild(node) {
      inserted.push(node);
    }
  };
  globalThis.document = {
    body: {},
    querySelector(selector) {
      return selector === '[data-cmd-program-destinations]' ? switcher : null;
    },
    createElement() {
      return {
        className: '',
        dataset: {},
        href: '',
        textContent: '',
        attributes: {},
        setAttribute(name, value) {
          this.attributes[name] = value;
        }
      };
    }
  };
  return Promise.resolve(callback(inserted)).finally(() => {
    globalThis.document = previous;
  });
}

test('assistant navigation appears only for declared programs and uses slug route', async () => {
  await withEntryDocument(async inserted => {
    const entry = new AssistantProgramEntry({
      workspaceId: 'workspace-uuid',
      workspaceSlug: 'neon-song',
      fetchImpl: async url => {
        assert.equal(url, '/api/workspaces/workspace-uuid/assistant-program');
        return {
          ok: true,
          json: async () => ({
            available: true,
            declaration: { station_name: 'Producer Home' }
          })
        };
      }
    });
    await entry.init();
    assert.equal(inserted.length, 1);
    assert.equal(inserted[0].textContent, 'Producer Home');
    assert.equal(inserted[0].href, '/workspaces/neon-song/assistant');
  });
});

test('hired assistant navigation keeps progression on the owning page', async () => {
  await withEntryDocument(async inserted => {
    const entry = new AssistantProgramEntry({
      workspaceId: 'workspace-uuid',
      workspaceSlug: 'neon-song',
      fetchImpl: async () => ({
        ok: true,
        json: async () => ({
          available: true,
          hired: true,
          stage_label: 'Collaborator',
          level: 2,
          declaration: { station_name: 'Producer Home' }
        })
      })
    });
    await entry.init();
    assert.equal(inserted[0].textContent, 'Producer Home');
    assert.equal(inserted[0].attributes['aria-label'], 'Open Producer Home');
  });
});

test('an activation-only legacy program gets the generic optional-home fallback', async () => {
  await withEntryDocument(async inserted => {
    const entry = new AssistantProgramEntry({
      workspaceId: 'legacy-uuid',
      workspaceSlug: 'legacy',
      fetchImpl: async () => ({
        ok: true,
        json: async () => ({ available: false, activation_needed: true })
      })
    });
    await entry.init();
    assert.equal(inserted[0].textContent, 'Team Home');
    assert.equal(inserted[0].attributes['aria-label'], 'Open Team Home');
  });
});

test('a declared profile shares its Home destination instead of duplicating top-level links', async () => {
  const program = extra => ({
    available: true,
    is_station: true,
    hired: true,
    stage_label: 'Helper',
    level: 1,
    declaration: { station_name: 'Music Production Home' },
    ...extra
  });
  const entryFor = body =>
    new AssistantProgramEntry({
      workspaceId: 'home-uuid',
      workspaceSlug: 'music-home',
      fetchImpl: async () => ({ ok: true, json: async () => body })
    });

  await withEntryDocument(async inserted => {
    const entry = entryFor(program({ home_profile_title: 'Your studio' }));
    await entry.init();
    assert.equal(inserted.length, 1);
    assert.equal(inserted[0].textContent, 'Music Production Home');
    assert.equal(inserted[0].href, '/workspaces/music-home/assistant');
    // The profile is linked on that page, and refresh cannot duplicate its Home.
    entry.render();
    entry.render();
    assert.equal(inserted.length, 1);
  });
  // No declared card, no link.
  await withEntryDocument(async inserted => {
    await entryFor(program({})).init();
    assert.equal(inserted.length, 1);
  });
  // A linked project has no card of its own, whatever the answer carries.
  await withEntryDocument(async inserted => {
    await entryFor(program({ is_station: false, home_profile_title: 'Your studio' })).init();
    assert.equal(inserted.length, 1);
  });
});

test('a resource catalog is separate, literal non-music names survive repeated renders', async () => {
  await withEntryDocument(async inserted => {
    const entry = new AssistantProgramEntry({
      workspaceId: 'research',
      workspaceSlug: 'research',
      fetchImpl: async () => ({
        ok: true,
        json: async () => ({
          available: true,
          resource_workspace: true,
          declaration: { station_name: '<Research & learning>' }
        })
      })
    });
    await entry.init();
    entry.render();
    assert.equal(inserted.length, 2);
    assert.equal(inserted[0].textContent, '<Research & learning>');
    assert.equal(inserted[1].href, '/workspaces/research/catalog');
  });
});

test('failed, not-yet-loaded and absent declarations never guess a destination', async () => {
  await withEntryDocument(async inserted => {
    for (const fetchImpl of [
      async () => ({ ok: false }),
      async () => {
        throw new Error('offline');
      }
    ]) {
      const entry = new AssistantProgramEntry({ workspaceId: 'w', workspaceSlug: 'w', fetchImpl });
      entry.render();
      assert.equal(inserted.length, 0);
      await entry.init();
      assert.equal(inserted.length, 0);
    }
  });
});

test('ordinary workspaces do not get assistant navigation', async () => {
  await withEntryDocument(async inserted => {
    const entry = new AssistantProgramEntry({
      workspaceId: 'ordinary-uuid',
      workspaceSlug: 'ordinary',
      fetchImpl: async () => ({ ok: true, json: async () => ({ available: false }) })
    });
    await entry.init();
    assert.equal(inserted.length, 0);
  });
});
