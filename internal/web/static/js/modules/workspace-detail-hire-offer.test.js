// The workspace page invites a hire where it used to name what was missing:
// the list view's empty roster.
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
