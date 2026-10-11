import test from 'node:test';
import assert from 'node:assert/strict';
import {
  researchCandidateNavigation,
  researchCapabilityView,
  renderResearchResponse
} from './personal-assistant-research.js';

const candidate = () => ({
  id: 'a'.repeat(32),
  kind: 'skill_catalog',
  name: '<script>Not executable</script>',
  package: 'owner/repository@skill',
  readiness: {
    installed: 'ready',
    verified: 'unknown',
    dependencies: {
      state: 'declared',
      mcp_servers: ['community-connection'],
      tools: ['<script>never run</script>']
    }
  },
  receipt: {
    candidate_id: 'a'.repeat(32),
    source_id: 'b'.repeat(32),
    level: 'metadata',
    availability: 'available',
    freshness: 'current_observation',
    read_at: new Date().toISOString()
  }
});
function dom() {
  const made = [];
  const createElement = tag => {
    const node = {
      tag,
      children: [],
      attributes: {},
      events: {},
      textContent: '',
      append(...items) {
        this.children.push(...items);
      },
      setAttribute(key, value) {
        this.attributes[key] = value;
      },
      addEventListener(event, fn) {
        this.events[event] = fn;
      }
    };
    made.push(node);
    return node;
  };
  const doc = { createElement, body: {}, activeElement: null, getElementById: () => null };
  const bubble = createElement('div');
  return { made, doc, row: { ownerDocument: doc, firstElementChild: bubble, isConnected: true } };
}

test('candidate handoff is static manual navigation, never a package/target dispatch', () => {
  const valid = candidate();
  assert.deepEqual(researchCandidateNavigation(valid), {
    href: '/skills',
    label: 'Browse Skills setup'
  });
  assert.equal(researchCandidateNavigation({ ...valid, kind: 'mcp_catalog' }).href, '/mcp');
  for (const bad of [
    null,
    { ...valid, id: 'forged' },
    { ...valid, kind: 'shell' },
    { ...valid, receipt: { ...valid.receipt, candidate_id: 'c'.repeat(32) } },
    { ...valid, receipt: { ...valid.receipt, freshness: 'stale' } },
    { ...valid, receipt: { ...valid.receipt, read_at: '2000-01-01T00:00:00Z' } }
  ])
    assert.equal(researchCandidateNavigation(bad), null);
  // Even a fabricated well-shaped ID cannot select an installer target: only
  // static manual navigation exists, with no result/package ID in the URL.
  assert.equal(
    researchCandidateNavigation({
      ...valid,
      package: 'malicious --command',
      url: 'javascript:run()'
    }).href,
    '/skills'
  );
});

test('capability reports actual system model, no-tool and optional search honestly', () => {
  assert.match(
    researchCapabilityView({ provider: 'codex', model: 'system-model', broker_tools: true }),
    /System conversation model: codex \/ system-model.*exact review.*disabled\/unconfigured/
  );
  assert.match(
    researchCapabilityView({
      provider: 'claude_code',
      model: 'system-model',
      broker_tools: false,
      broader_search: 'configured_review_required'
    }),
    /snapshot-only.*separate exact review/
  );
  assert.match(researchCapabilityView({}), /No system conversation model.*Settings.*agent profile/);
});

test('candidate evidence is a labelled list; requirements are text and setup stays separate', () => {
  const fixture = dom();
  let dispatched = 0;
  renderResearchResponse(
    fixture.row,
    { research_result: { availability: 'available', candidates: [null, candidate()] } },
    { post: () => dispatched++, approve: () => dispatched++ }
  );
  const list = fixture.made.find(node => node.tag === 'ul');
  assert.match(list.attributes['aria-label'], /independent readiness/);
  const item = fixture.made.find(node => node.tag === 'li');
  assert.match(
    item.textContent,
    /<script>Not executable<\/script>.*installed: unknown.*verified: unknown/
  );
  assert.equal(item.innerHTML, undefined);
  const link = fixture.made.find(node => node.tag === 'a');
  assert.equal(link.href, '/skills');
  assert.equal(link.target, '_blank');
  assert.equal(link.rel, 'noopener noreferrer');
  const handoff = fixture.made.find(
    node => node.className === 'personal-assistant-research__handoff'
  );
  assert.match(handoff.children.join(' '), /manual only.*no package or access target chosen/);
  const boundaries = fixture.made.find(
    node => node.className === 'personal-assistant-research__setup'
  );
  assert.match(
    boundaries.children[1].textContent,
    /Nothing is preselected or confirmed.*separate decisions.*Keep this conversation and draft here/
  );
  assert.equal(dispatched, 0);
  assert.equal(
    fixture.made.some(node => node.tag === 'button'),
    false
  );
});

test('late lookup completion cannot retarget or steal focus from another context', async () => {
  const fixture = dom();
  let current = true;
  let focused = 0;
  fixture.doc.getElementById = () => ({ focus: () => focused++ });
  renderResearchResponse(
    fixture.row,
    {
      conversation: { id: 'owned', stored: true },
      research_review: {
        token: 'a'.repeat(43),
        digest: 'b'.repeat(64),
        lookup: { operation: 'skills_catalog', query: 'community' },
        destination: 'https://skills.sh/api/search',
        expires_at: new Date(Date.now() + 300000).toISOString()
      }
    },
    {
      isCurrent: () => current,
      post: async () => ({}),
      approve: async () => {
        current = false;
      }
    }
  );
  const button = fixture.made.find(node => node.tag === 'button');
  fixture.doc.activeElement = button;
  await button.events.click();
  assert.equal(focused, 0);
  assert.match(
    fixture.made.find(node => node.attributes.role === 'status').textContent,
    /context changed.*nothing was retargeted/
  );
});
