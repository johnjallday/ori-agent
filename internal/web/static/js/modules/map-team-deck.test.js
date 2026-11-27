import test from 'node:test';
import assert from 'node:assert/strict';
import { deckMembers, teamSnapshot } from './map-team-deck.js';

const group = {
  id: 'h',
  children: [
    {
      id: 'nested',
      name: 'Folder',
      agents: ['Mix'],
      children: [
        { id: 'a', name: 'First', agents: ['Keeper', 'Lead'] },
        { id: 'b', name: 'Last', agents: ['keeper'] }
      ]
    }
  ]
};

test('own agents lead, the Commander is omitted everywhere and nested places retain their owners', () => {
  const cards = deckMembers({
    own: [{ name: 'Lead' }, { name: 'Mix' }],
    commander: 'LEAD',
    group,
    groupName: 'Home',
    songIds: new Set(['a', 'b'])
  });
  assert.deepEqual(
    cards.map(c => [c.name, c.where]),
    [
      ['Mix', '2 workspaces'],
      ['Keeper', '2 songs']
    ]
  );
  assert.deepEqual(cards[0].places, [
    { own: true, name: 'Home' },
    { id: 'nested', name: 'Folder' }
  ]);
});

test('a failed or superseded membership read exposes no previous names', () => {
  const panel = { group, readFailed: true };
  const view = {
    page: { workspace: { entry_agent_name: 'Lead' }, membersPanel: panel },
    workspaceId: () => 'h',
    agentGroups: () => ({ agents: [{ name: 'Lead' }] })
  };
  assert.deepEqual(teamSnapshot(view), { cards: [], incomplete: true });
  panel.readFailed = false;
  view.workspaceId = () => 'other';
  assert.deepEqual(teamSnapshot(view), { cards: [], incomplete: true });
});
