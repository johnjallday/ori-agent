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
      if (selector === 'a[href$="/plans"]') return null;
      return null;
    },
    insertBefore(node) {
      inserted.push(node);
    }
  };
  globalThis.document = {
    body: {},
    querySelector(selector) {
      return selector === '.ws-cmd-view-switch' ? switcher : null;
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

test('hired assistant navigation carries the compact stage and level summary', async () => {
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
    assert.equal(inserted[0].textContent, 'Producer Home · Collaborator L2');
    assert.match(inserted[0].attributes['aria-label'], /level 2/);
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
