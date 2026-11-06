import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import {
  LearningMissions,
  createLearningAPI,
  featuredLearning,
  learningTicketHref
} from './learning-missions.js';
import { LearningTicketReview } from './learning-mission-review.js';

function fixtureDOM() {
  const doc = { activeElement: null };
  doc.createElement = tag => {
    const node = {
      tag,
      ownerDocument: doc,
      children: [],
      dataset: {},
      attributes: {},
      listeners: {},
      value: '',
      append(...children) {
        this.children.push(...children);
      },
      replaceChildren(...children) {
        this.children = children;
        this._text = '';
      },
      addEventListener(type, run) {
        this.listeners[type] = run;
      },
      setAttribute(key, value) {
        this.attributes[key] = value;
      },
      removeAttribute(key) {
        delete this.attributes[key];
      },
      contains(value) {
        return this === value || this.children.some(child => child.contains(value));
      },
      focus() {
        doc.activeElement = this;
      },
      querySelector(selector) {
        return (
          this.children.find(child => child.tag === selector) ||
          this.children.map(child => child.querySelector(selector)).find(Boolean)
        );
      },
      get textContent() {
        return (this._text || '') + this.children.map(child => child.textContent).join(' ');
      },
      set textContent(value) {
        this._text = String(value);
        this.children = [];
      },
      set innerHTML(_value) {
        throw new Error('Learning must render untrusted content as text');
      },
      get options() {
        return this.children;
      }
    };
    return node;
  };
  return doc.createElement('section');
}
const status = instances => ({
  owner: { revision: 3, preferences: { mode: 'on_request' } },
  instances
});
const instance = (state = 'active') => ({
  id: 'mission',
  state,
  revision: 2,
  definition: {
    goal: '<img src=x onerror=alert(1)>',
    scope: 'Only supplied text',
    why: 'An explicit choice',
    studio_id: 'ws'
  },
  progress: {}
});
const response = body => ({ ok: true, status: 200, json: async () => body });

test('one active learning contract takes priority over one offer; no terminal recommendation feed', () => {
  const offer = instance('offered'),
    active = instance();
  assert.equal(featuredLearning(status([offer, active])), active);
  assert.equal(featuredLearning(status([instance('completed')])), null);
  assert.equal(
    learningTicketHref({ ticket_href: '/workspaces/ws%2Fx/task/t%3Fx' }),
    '/workspaces/ws%2Fx/task/t%3Fx'
  );
  assert.equal(learningTicketHref(instance()), '');
  assert.equal(learningTicketHref({ ticket_href: 'javascript:alert(1)' }), '');
  assert.equal(learningTicketHref({ ticket_href: '//outside.test/task/x' }), '');
});

test('board hydration renders hostile prose as text and makes only the saved-state GET', async () => {
  const root = fixtureDOM(),
    calls = [];
  const board = new LearningMissions(
    root,
    createLearningAPI(async (url, options) => {
      calls.push([url, options]);
      return response(status([instance('offered')]));
    })
  );
  await board.refresh();
  assert.equal(calls.length, 1);
  assert.equal(calls[0][1].method, 'GET');
  assert.equal(calls[0][0], '/api/learning-missions');
  assert.match(root.textContent, /<img src=x onerror=alert\(1\)>/);
  assert.match(root.textContent, /Start learning — no execution/);
  assert.match(root.textContent, /Not now/);
  assert.equal(root.ownerDocument.activeElement, null);
});

test('lost mutation response recovers by the same operation ID without replaying effects', async () => {
  const calls = [];
  const api = createLearningAPI(
    async (path, options) => {
      calls.push([path, options]);
      if (options.method === 'POST') throw new Error('connection lost');
      return response({ operation: { id: 'reserved', status: 'succeeded' } });
    },
    () => 'reserved'
  );
  const out = await api.mutate('/api/learning-missions/proposals', 2, { studio_id: 'ws' });
  assert.equal(out.operation.id, 'reserved');
  assert.deepEqual(
    calls.map(([path, options]) => [path, options.method]),
    [
      ['/api/learning-missions/proposals', 'POST'],
      ['/api/learning-missions/operations/reserved', 'GET']
    ]
  );
  assert.deepEqual(JSON.parse(calls[0][1].body), {
    studio_id: 'ws',
    expected_revision: 2,
    operation_id: 'reserved'
  });
});

test('unknown Ticket start never automatically retries or creates a learning operation', async () => {
  let calls = 0;
  const api = createLearningAPI(async () => {
    calls++;
    throw new Error('unknown start');
  });
  await assert.rejects(api.start('/ticket/start', 'reviewed'), /unknown start/);
  assert.equal(calls, 1);
});

test('Ticket hydration performs GETs only; exact review text is not parsed as HTML', async () => {
  const root = fixtureDOM(),
    calls = [];
  root.dataset = { studioId: 'ws', ticketId: 'ticket' };
  const row = instance();
  row.progress = { ticket_id: 'ticket', run_id: 'run' };
  const api = createLearningAPI(async (path, options) => {
    calls.push([path, options.method]);
    if (path === '/api/learning-missions') return response(status([row]));
    if (path.endsWith('/execution'))
      return response({ executor: {}, input: 'input', disclosure: 'No tools' });
    return response({ state: 'review' });
  });
  const view = new LearningTicketReview(root, api);
  await view.refresh();
  assert.ok(calls.every(([, method]) => method === 'GET'));
  assert.match(root.textContent, /Inspect the exact result/);
  view.renderReview({
    instance: row,
    review: { id: 'nonce', result: { run_id: 'run', artifact_id: 'artifact' } },
    result_text: '<script>unsafe()</script>'
  });
  assert.match(root.textContent, /<script>unsafe\(\)<\/script>/);
  assert.equal(root.ownerDocument.activeElement.tag, 'select');
});

test('new hosts preserve the finite onboarding owner and make execution review visible before a result exists', () => {
  const home = readFileSync(
    new URL('../../../templates/components/dashboard.tmpl', import.meta.url),
    'utf8'
  );
  const ticket = readFileSync(
    new URL('../../../templates/pages/workspace-task.tmpl', import.meta.url),
    'utf8'
  );
  assert.match(home, /Getting Started/);
  assert.equal((home.match(/id="learningMissions"/g) || []).length, 1);
  assert.match(home, /id="questLog"/);
  assert.ok(
    ticket.indexOf('id="learningTicketReview"') < ticket.indexOf('id="workspace-task-output-card"')
  );
});
