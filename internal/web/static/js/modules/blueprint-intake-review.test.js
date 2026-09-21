import { test } from 'node:test';
import assert from 'node:assert/strict';

class FakeElement {
  constructor(tag) {
    this.tagName = String(tag).toUpperCase();
    this.children = [];
    this._text = '';
    this._listeners = {};
    this.value = '';
    this.checked = false;
    this.disabled = false;
  }
  get textContent() {
    return this._text + this.children.map(child => child.textContent || '').join('');
  }
  set textContent(value) {
    this._text = String(value || '');
    this.children = [];
  }
  appendChild(child) {
    this.children.push(child);
    return child;
  }
  append(...children) {
    this.children.push(...children);
  }
  addEventListener(name, callback) {
    (this._listeners[name] = this._listeners[name] || []).push(callback);
  }
  all() {
    return this.children.flatMap(child => [child, ...(child.all ? child.all() : [])]);
  }
}

globalThis.document = {
  createElement: tag => new FakeElement(tag),
  createTextNode: text => {
    const item = new FakeElement('#text');
    item._text = String(text);
    return item;
  },
  addEventListener() {}
};
globalThis.window = globalThis;
window.SetupWizard = {
  registered: {},
  registerStepRenderer(kind, renderer) {
    this.registered[kind] = renderer;
  }
};

await import('./blueprint-intake-review.js');
const renderer = window.SetupWizard.registered.intake_review;
const tick = () => new Promise(resolve => setImmediate(resolve));

function ctx(id, confirm = async () => {}) {
  return {
    workspaceId: id,
    step: { id: 'review', kind: 'intake_review', intake_key: 'materials', status: 'active' },
    setBusy() {},
    setError(message) {
      if (message) throw new Error(message);
    },
    announce() {},
    confirm
  };
}

function response(payload) {
  return { ok: true, json: async () => payload };
}

const proposal = {
  hash: 'reviewed-hash',
  status: 'pending',
  notice: { unsupported: 1 },
  items: [
    {
      kind: 'ticket',
      key: 'quiz-1',
      title: '<b>Quiz 1</b>',
      due_at: '2026-10-01T04:01:00Z',
      due_input: '2026-10-01',
      no_time_given: true,
      partly_read: true,
      source: { source_id: 's1', quote: '<img onerror=alert(1)> Quiz 1 is due October 1' }
    }
  ]
};

test('registers intake_review and renders counts, dates, markers, and source text safely', async () => {
  globalThis.fetch = async () => response({ proposal, skill: { ready: true } });
  const host = new FakeElement('div');
  const context = ctx('review-render');
  renderer.render(host, context);
  await tick();
  await tick();

  assert.match(host.textContent, /1 ticket/);
  assert.match(host.textContent, /No time given/);
  assert.match(host.textContent, /Source partly read/);
  const title = host.all().find(element => element.type === 'text');
  assert.equal(title.value, '<b>Quiz 1</b>');
  assert.match(host.textContent, /<img onerror=alert\(1\)>/);
  assert.equal('innerHTML' in host, false);
  assert.equal(renderer.primaryLabel(context), 'Apply selected');
});

test('renders memory, note, and unavailable calendar proposals as safe review text', async () => {
  const mixed = {
    hash: 'mixed-hash',
    status: 'pending',
    items: [
      {
        kind: 'memory',
        key: 'memory',
        text: '<img src=x> Office hours Tuesday',
        source: { source_id: 's', quote: 'Office hours Tuesday' }
      },
      {
        kind: 'note',
        key: 'note',
        title: 'Reading list',
        body: '<script>Read chapter 1</script>',
        source: { source_id: 's', quote: 'chapter 1' }
      },
      {
        kind: 'calendar_event',
        key: 'event',
        title: 'Seminar',
        start: '2026-04-03T10:00:00Z',
        end: '2026-04-03T11:00:00Z',
        disabled_reason: 'Connect a calendar in this workspace to add these.',
        source: { source_id: 's', quote: 'seminar' }
      }
    ]
  };
  globalThis.fetch = async () => response({ proposal: mixed, skill: { ready: true } });
  const host = new FakeElement('div');
  renderer.render(host, ctx('review-mixed'));
  await tick();
  await tick();

  assert.match(host.textContent, /<img src=x> Office hours Tuesday/);
  assert.match(host.textContent, /<script>Read chapter 1<\/script>/);
  assert.match(host.textContent, /Connect a calendar/);
  const checkboxes = host.all().filter(element => element.type === 'checkbox');
  assert.equal(checkboxes.length, 3);
  assert.equal(checkboxes[2].disabled, true);
  assert.equal(checkboxes[2].checked, false);
  assert.equal('innerHTML' in host, false);
});

test('shows bundled skill text as text and trusts it before a run', async () => {
  let trustBody;
  let trusted = false;
  globalThis.fetch = async (url, options = {}) => {
    if (url.endsWith('/skill/trust')) {
      trustBody = JSON.parse(options.body);
      trusted = true;
      return response({ bundled_skill: { ready: true } });
    }
    return response({
      skill: { name: 'syllabus-intake', agent: 'Manager', ready: trusted, missing: 'not trusted' },
      bundled_skill: {
        name: 'syllabus-intake',
        description: 'Read dates',
        bundled_text: '<script>not markup</script>\nReturn JSON.',
        collision: false
      }
    });
  };
  const context = ctx('review-skill');
  const host = new FakeElement('div');
  renderer.render(host, context);
  await tick();
  await tick();

  assert.match(host.textContent, /<script>not markup<\/script>/);
  const trust = host.all().find(element => element.textContent === 'Trust and enable');
  assert.ok(trust);
  await trust._listeners.click[0]();
  assert.equal(trustBody.choice, 'bundled');
  assert.match(host.textContent, /sources are ready/i);
});

test('Apply sends the reviewed hash and selected edits before confirming', async () => {
  let appliedBody;
  globalThis.fetch = async (url, options = {}) => {
    if (url.endsWith('/proposal/apply')) {
      appliedBody = JSON.parse(options.body);
      return response({
        proposal: {
          ...proposal,
          status: 'applied',
          results: [{ key: 'quiz-1', status: 'created' }]
        }
      });
    }
    return response({ proposal, skill: { ready: true } });
  };
  let confirmed = false;
  const context = ctx('review-apply', async () => (confirmed = true));
  const host = new FakeElement('div');
  renderer.render(host, context);
  await tick();
  await tick();
  await renderer.onPrimary(context);

  assert.equal(appliedBody.proposal_hash, 'reviewed-hash');
  assert.equal(appliedBody.items[0].key, 'quiz-1');
  assert.equal(appliedBody.items[0].selected, true);
  assert.equal(confirmed, true);
});
