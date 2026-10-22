// The workspace page invites a hire where it used to name what was missing:
// the list view's empty roster, and the Commander offer that replaced the
// mandatory "cannot function" dialog.
//   node --test internal/web/static/js/modules/workspace-detail-hire-offer.test.js

import { test } from 'node:test';
import assert from 'node:assert/strict';

global.window = { workspaceDetail: null };
global.document = {
  getElementById: () => null,
  createElement() {
    return {
      _text: '',
      set textContent(value) {
        this._text = String(value || '');
      },
      get innerHTML() {
        return this._text.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
      }
    };
  }
};

const { WorkspaceDetailPage } = await import('./workspace-detail.js');

function emptyListPage() {
  const page = new WorkspaceDetailPage('workspace-1');
  page.elements = { agentsList: { innerHTML: '' } };
  page.tasksLoading = false;
  page.buildAgentGroups = () => [];
  return page;
}

test('the list view empty roster names the Command view offer and routes through it', () => {
  const requested = [];
  window.workspaceCommand = {
    hireOffer: () => ({ roleId: 'dev assistant', label: 'Hire for Development Assistant' }),
    requestHire: roleId => requested.push(roleId)
  };
  try {
    const page = emptyListPage();
    page.renderAgentGroups();
    const html = page.elements.agentsList.innerHTML;
    assert.match(html, /No one works here yet\./);
    assert.match(html, /Hire someone for this workspace\./);
    assert.doesNotMatch(html, /No agents yet/);
    assert.match(html, /hireForEmptyRoster\('dev%20assistant'\)">Hire for Development Assistant</);

    page.hireForEmptyRoster('dev%20assistant');
    assert.deepEqual(requested, ['dev assistant']);
  } finally {
    delete window.workspaceCommand;
  }
});

test('without the Command view the list view offers Hire an agent and opens Add Agent', () => {
  const page = emptyListPage();
  let opened = 0;
  page.openAddAgentModal = async () => {
    opened += 1;
  };
  page.renderAgentGroups();
  assert.match(page.elements.agentsList.innerHTML, /hireForEmptyRoster\(''\)">Hire an agent</);
  page.hireForEmptyRoster('');
  assert.equal(opened, 1);
});

// One browser session's storage, shared by every page the test opens.
function sessionStore() {
  const values = new Map();
  return {
    getItem: key => (values.has(key) ? values.get(key) : null),
    setItem: (key, value) => values.set(key, String(value)),
    removeItem: key => values.delete(key)
  };
}

// A workspace page with no entry agent whose dialog answers `answer`.
function commanderOfferPage(answer, { workspace = {} } = {}) {
  const page = new WorkspaceDetailPage('workspace-1');
  page.workspace = { id: 'workspace-1', name: 'Launch Plan', ...workspace };
  const seen = { offers: [], createFlows: 0 };
  page.showTaskConfirmDialog = async options => {
    seen.offers.push(options);
    return answer;
  };
  page.openWorkspaceEntryAgentCreateFlow = () => {
    seen.createFlows += 1;
  };
  return { page, seen };
}

function withSession(fn) {
  const previous = { storage: window.sessionStorage, command: window.workspaceCommand };
  window.sessionStorage = sessionStore();
  return Promise.resolve(fn()).finally(() => {
    window.sessionStorage = previous.storage;
    window.workspaceCommand = previous.command;
  });
}

test('a role-less workspace without a Commander is offered one, dismissibly', () =>
  withSession(async () => {
    const { page, seen } = commanderOfferPage(false);
    await page.maybePromptForMissingEntryAgent();
    assert.equal(seen.offers.length, 1);
    const offer = seen.offers[0];
    assert.equal(offer.mandatory, false);
    assert.equal(offer.eyebrow, 'Workspace setup');
    assert.equal(offer.title, 'Hire a Commander for this workspace?');
    assert.equal(
      offer.message,
      'Launch Plan routes chats and tasks through its Commander. The first agent you hire here takes that seat.'
    );
    assert.equal(offer.confirmLabel, 'Hire a Commander');
    assert.equal(offer.cancelLabel, 'Not now');
    assert.deepEqual(offer.details, [
      'The Commander receives chats and dispatches tasks.',
      'You can rename or replace it later.',
      'Nothing is created until you finish the form.'
    ]);
    const copy = JSON.stringify(offer);
    assert.doesNotMatch(copy, /cannot function|required|No Commander/);
    assert.equal(seen.createFlows, 0, 'declining creates nothing');
  }));

test('Not now holds for the session and the offer returns once the session key is gone', () =>
  withSession(async () => {
    const first = commanderOfferPage(false);
    await first.page.maybePromptForMissingEntryAgent();
    assert.equal(first.seen.offers.length, 1);

    // A reload in the same session: nothing more.
    const reload = commanderOfferPage(false);
    await reload.page.maybePromptForMissingEntryAgent();
    assert.equal(reload.seen.offers.length, 0);

    // A new session (its storage starts empty): the offer is back.
    window.sessionStorage = sessionStore();
    const nextSession = commanderOfferPage(false);
    await nextSession.page.maybePromptForMissingEntryAgent();
    assert.equal(nextSession.seen.offers.length, 1);
  }));

test('Hire a Commander opens the create flow and leaves no dismissal behind', () =>
  withSession(async () => {
    const { page, seen } = commanderOfferPage(true);
    await page.maybePromptForMissingEntryAgent();
    assert.equal(seen.createFlows, 1);
    assert.equal(
      window.sessionStorage.getItem(page.getEntryAgentPromptDismissalStorageKey()),
      null
    );
  }));

test('no offer for a workspace with blueprint roles or with an entry agent', () =>
  withSession(async () => {
    window.workspaceCommand = {
      roleRoster: { total_count: 1, filled_count: 0, roles: [] },
      loadRoleRoster: async () => window.workspaceCommand.roleRoster
    };
    const withRoles = commanderOfferPage(true);
    await withRoles.page.maybePromptForMissingEntryAgent();
    assert.equal(withRoles.seen.offers.length, 0);

    delete window.workspaceCommand;
    const withCommander = commanderOfferPage(true, { workspace: { entry_agent_name: 'Ada' } });
    await withCommander.page.maybePromptForMissingEntryAgent();
    assert.equal(withCommander.seen.offers.length, 0);
  }));
