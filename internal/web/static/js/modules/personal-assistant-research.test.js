import test from 'node:test';
import assert from 'node:assert/strict';
import {
  publicResearchLink,
  researchSourcesView,
  researchReviewView,
  renderResearchSources,
  renderResearchResponse
} from './personal-assistant-research.js';

const offer = () => ({
  token: 'a'.repeat(43),
  digest: 'b'.repeat(64),
  lookup: { operation: 'skills_catalog', query: 'Telegram community management' },
  destination: 'https://skills.sh/api/search',
  redirect_policy: 'none',
  expires_at: new Date(Date.now() + 300000).toISOString()
});
const attributed = {
  research: [
    {
      key: 'S1',
      kind: 'skill_catalog_listing',
      level: 'metadata',
      name: '<img src=x onerror=alert(1)>',
      url: 'https://example.com/skill',
      freshness: 'current_observation',
      cited: true
    }
  ]
};

function domFixture() {
  const made = [];
  const create = tag => {
    const node = {
      tag,
      children: [],
      events: {},
      textContent: '',
      attributes: {},
      append(...items) {
        this.children.push(...items);
      },
      setAttribute(key, value) {
        this.attributes[key] = value;
      },
      addEventListener(event, fn) {
        this.events[event] = fn;
      },
      querySelector() {
        return null;
      }
    };
    made.push(node);
    return node;
  };
  const bubble = create('div');
  const row = {
    ownerDocument: { createElement: create },
    firstElementChild: bubble,
    isConnected: true
  };
  return { row, made, bubble };
}

test('public research links reject credentials, schemes and private destinations', () => {
  for (const bad of [
    'javascript:alert(1)',
    'file:///private/file',
    '//example.com/path',
    'https://user:secret@example.com/',
    'https://localhost/',
    'http://127.0.0.1/',
    'http://2130706433/',
    'http://10.0.0.1/',
    'https://example.com/?token=secret',
    'https://example.com:444/',
    'https://example.com/with space'
  ])
    assert.equal(publicResearchLink(bad), '', bad);
  assert.equal(publicResearchLink('https://example.com/docs'), 'https://example.com/docs');
});
test('sources are typed host references and earlier reads remain historical', () => {
  const fresh = researchSourcesView(attributed);
  assert.match(fresh[0].detail, /catalog listing.*current observation/);
  const old = researchSourcesView(attributed, { historical: true });
  assert.match(old[0].detail, /historical observation, not a fresh read/);
  assert.equal(
    researchSourcesView({
      research: [
        { ...attributed.research[0], key: 'invented' },
        { ...attributed.research[0], kind: 'shell' }
      ]
    }).length,
    0
  );
  const dom = domFixture();
  renderResearchSources(dom.row, attributed);
  const link = dom.made.find(node => node.tag === 'a');
  assert.equal(link.textContent, '[S1] <img src=x onerror=alert(1)>');
  assert.equal(link.innerHTML, undefined);
  assert.equal(link.rel, 'noopener noreferrer');
  assert.equal(link.target, '_blank');
});
test('review view refuses expired, forged, unknown and unsafe targets', () => {
  assert.equal(researchReviewView(offer()).value, 'Telegram community management');
  for (const bad of [
    null,
    { ...offer(), token: 'forged' },
    { ...offer(), expires_at: 'invalid' },
    { ...offer(), expires_at: '2000-01-01T00:00:00Z' },
    { ...offer(), lookup: { operation: 'shell', query: 'run' } },
    { ...offer(), lookup: { operation: 'public_document', url: 'http://127.0.0.1/' } }
  ])
    assert.equal(researchReviewView(bad), null);
});
test('editing invalidates the offer and requires preparation then a separate approval', async () => {
  const dom = domFixture();
  const posts = [];
  const approvals = [];
  let current = true;
  const review = offer();
  renderResearchResponse(
    dom.row,
    {
      conversation: { id: 'canonical', stored: true },
      research_context: { origin: 'personal_assistant_panel' },
      research_review: review
    },
    {
      isCurrent: () => current,
      post: async (path, body) => {
        posts.push({ path, body });
        return { review: { ...offer(), lookup: body.lookup } };
      },
      approve: async value => approvals.push(value)
    }
  );
  const input = dom.made.find(node => node.tag === 'input');
  const confirm = dom.made.find(node => node.tag === 'button');
  input.value = 'Telegram moderation';
  input.events.input();
  assert.equal(posts[0].path, '/api/home-assistant/research/cancel');
  await confirm.events.click();
  assert.equal(approvals.length, 0);
  assert.equal(posts[1].path, '/api/home-assistant/research/review');
  assert.equal(posts[1].body.lookup.query, 'Telegram moderation');
  assert.equal(confirm.textContent, 'Approve exact lookup');
  current = false;
  await confirm.events.click();
  assert.equal(approvals.length, 0);
  current = true;
  await confirm.events.click();
  assert.equal(approvals.length, 1);
  assert.equal(approvals[0].review.lookup.query, 'Telegram moderation');
  assert.equal(
    dom.made.some(node => Object.values(node.attributes).includes(review.token)),
    false,
    'token stays in closure only'
  );
});
test('cancel and unsaved replies never execute or authorize a lookup', async () => {
  for (const stored of [false, true]) {
    const dom = domFixture();
    let posts = 0;
    let executed = 0;
    renderResearchResponse(
      dom.row,
      { conversation: { id: 'canonical', stored }, research_review: offer() },
      {
        isCurrent: () => true,
        post: async () => {
          posts++;
          return {};
        },
        approve: async () => executed++
      }
    );
    const cancel = dom.made.filter(node => node.tag === 'button')[1];
    if (stored) {
      await cancel.events.click();
      assert.equal(posts, 1);
    } else assert.equal(cancel, undefined);
    assert.equal(executed, 0);
  }
});
