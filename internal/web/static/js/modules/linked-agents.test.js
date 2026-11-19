import test from 'node:test';
import assert from 'node:assert/strict';
import { linkedAgents, nodeAgentNames, placesLine, rowAgentsLine } from './linked-agents.js';

// Music Production Home → Albums (a group with its own manager) → five songs
// that share one REAPER Assistant.
function home() {
  const song = n => ({
    id: 'song-' + n,
    name: 'Album-' + n,
    kind: 'workspace',
    agents: ['REAPER Assistant'],
    entry_agent_name: 'REAPER Assistant',
    children: []
  });
  return {
    id: 'home',
    name: 'Music Production Home',
    kind: 'group',
    agents: ['Portfolio Manager'],
    entry_agent_name: 'Portfolio Manager',
    children: [
      {
        id: 'albums',
        name: 'Albums',
        kind: 'group',
        agents: ['Albums Manager'],
        entry_agent_name: 'Albums Manager',
        children: [1, 2, 3, 4, 5].map(song)
      }
    ]
  };
}

test('a node names its lead first and each agent once', () => {
  assert.deepEqual(
    nodeAgentNames({ entry_agent_name: 'Lead', agents: ['Scout', 'lead', ' Scout ', ''] }),
    ['Lead', 'Scout']
  );
  assert.deepEqual(nodeAgentNames({ agents: [{ name: 'A' }, { agent_name: 'B' }, null, 7] }), [
    'A',
    'B'
  ]);
  assert.deepEqual(nodeAgentNames({ entry_agent_name: 'Solo' }), ['Solo']);
  for (const nothing of [null, undefined, 'x', {}, { agents: 'REAPER Assistant' }])
    assert.deepEqual(nodeAgentNames(nothing), []);
});

test('agents two levels down are listed once each, nested group first, never the group’s own', () => {
  const agents = linkedAgents(home());
  assert.deepEqual(
    agents.map(agent => agent.name),
    ['Albums Manager', 'REAPER Assistant']
  );
  assert.deepEqual(agents[0].places, [{ id: 'albums', name: 'Albums' }]);
  assert.deepEqual(
    agents[1].places.map(place => place.name),
    ['Album-1', 'Album-2', 'Album-3', 'Album-4', 'Album-5']
  );
  assert.equal(placesLine(agents[0].places), 'Albums');
  assert.equal(placesLine(agents[1].places), '5 workspaces');
});

test('the nested group lists only what is inside it', () => {
  const albums = home().children[0];
  assert.deepEqual(
    linkedAgents(albums).map(agent => [agent.name, placesLine(agent.places)]),
    [['REAPER Assistant', '5 workspaces']]
  );
});

test('a leaf, an empty group and an unread tree have no linked agents', () => {
  for (const group of [null, undefined, {}, { id: 'g', children: [] }, { id: 'w', agents: ['A'] }])
    assert.deepEqual(linkedAgents(group), []);
  assert.deepEqual(
    linkedAgents({ id: 'g', children: [{ id: 'm', name: 'Empty', agents: [] }] }),
    []
  );
  assert.equal(placesLine([]), '');
  assert.equal(placesLine(null), '');
});

test('names that differ only by case are one agent; an unnamed place reads Untitled', () => {
  const agents = linkedAgents({
    id: 'g',
    children: [
      { id: 'a', name: '', agents: ['Scout'] },
      { id: 'b', name: 'B', agents: ['scout'] },
      { name: 'no id is skipped', agents: ['Ghost'] }
    ]
  });
  assert.deepEqual(agents, [
    {
      name: 'Scout',
      places: [
        { id: 'a', name: 'Untitled' },
        { id: 'b', name: 'B' }
      ]
    }
  ]);
});

test('a tree that repeats a node is walked once', () => {
  const loop = { id: 'loop', name: 'Loop', agents: ['A'], children: [] };
  loop.children.push(loop);
  assert.deepEqual(linkedAgents({ id: 'g', children: [loop] }), [
    { name: 'A', places: [{ id: 'loop', name: 'Loop' }] }
  ]);
});

test('a row names up to three agents, then counts the rest', () => {
  assert.equal(rowAgentsLine([]), '');
  assert.equal(rowAgentsLine(['A']), 'A');
  assert.equal(rowAgentsLine(['A', 'B', 'C']), 'A, B, C');
  assert.equal(rowAgentsLine(['A', 'B', 'C', 'D', 'E']), 'A, B, C +2 more');
  assert.equal(rowAgentsLine(null), '');
});
