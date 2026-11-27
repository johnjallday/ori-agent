import test from 'node:test';
import assert from 'node:assert/strict';
import { deckMembers, deckSlots, sharedSlot, teamSnapshot } from './map-team-deck.js';

test('only current empty, writable roster slots offer a hire', t => {
  const old = globalThis.window;
  t.after(() => {
    if (old === undefined) delete globalThis.window;
    else globalThis.window = old;
  });
  globalThis.window = { WorkspaceRoleRoster: { rowsFrom: roster => roster.roles } };
  const view = {
    workspaceId: () => 'h',
    roleRoster: {
      workspace_id: 'h',
      roles: [
        { state: 'empty', roleId: 'a' },
        { state: 'filled', roleId: 'b' },
        { state: 'empty', roleId: 'c', readOnly: true },
        { state: 'empty', roleId: 'd', needsClear: true }
      ]
    },
    page: { workspace: {} }
  };
  assert.deepEqual(
    deckSlots(view).map(s => [s.row.roleId, s.canHire]),
    [
      ['a', true],
      ['c', false],
      ['d', false]
    ]
  );
  view.page.workspace.read_only = true;
  assert.ok(deckSlots(view).every(slot => !slot.canHire));
  view.roleRoster.workspace_id = 'other';
  assert.deepEqual(deckSlots(view), []);
});

test('a shared slot links only to the matching writable Home’s own offer', () => {
  const view = {
    workspaceId: () => 'h',
    page: { workspace: { folder_slug: 'my-home' } },
    groupTemplateStatus: { kind: 'managed_home', integration: { state: 'available' } },
    sharedAssistantOffer: { workspaceId: 'h', label: 'Keeper', can_hire: true }
  };
  assert.deepEqual(sharedSlot(view), {
    label: 'Keeper',
    href: '/workspaces/my-home/assistant#projectLibrarySharing'
  });
  view.page.workspace.read_only = true;
  assert.equal(sharedSlot(view).href, null);
  view.page.workspace.read_only = false;
  delete view.sharedAssistantOffer.can_hire;
  assert.equal(sharedSlot(view).href, null);
  view.groupTemplateStatus.kind = 'ordinary';
  assert.equal(sharedSlot(view), null);
  view.groupTemplateStatus.kind = 'managed_home';
  view.workspaceId = () => 'other';
  assert.equal(sharedSlot(view), null);
});

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
