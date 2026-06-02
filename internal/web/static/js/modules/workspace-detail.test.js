import { test } from 'node:test';
import assert from 'node:assert/strict';

function escapeHTML(value) {
  return String(value || '')
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');
}

global.window = { workspaceDetail: null };
global.document = {
  createElement() {
    return {
      _text: '',
      set textContent(value) {
        this._text = String(value || '');
      },
      get innerHTML() {
        return escapeHTML(this._text);
      }
    };
  }
};

const { WorkspaceDetailPage } = await import('./workspace-detail.js');

test('workspace detail renders reference URL task indicators', () => {
  const page = new WorkspaceDetailPage('workspace-1');
  const indicator = page.renderTaskReferenceURLIndicator({
    reference_url: 'https://example.com/spec?version=1&section=intro'
  });

  assert.match(indicator, /workspace-detail-task-reference-indicator/);
  assert.match(indicator, /href="https:\/\/example\.com\/spec\?version=1&amp;section=intro"/);
  assert.match(indicator, /target="_blank"/);
  assert.match(indicator, /rel="noopener noreferrer"/);
  assert.match(indicator, /onclick="event\.stopPropagation\(\);"/);

  const boardIndicator = page.renderTaskReferenceURLIndicator(
    { reference_url: 'https://example.com/board' },
    'board'
  );
  assert.match(boardIndicator, /workspace-detail-task-reference-indicator-board/);

  assert.equal(page.renderTaskReferenceURLIndicator({ reference_url: '   ' }), '');
});

test('workspace detail summary chip counts tasks with reference URLs', () => {
  const page = new WorkspaceDetailPage('workspace-1');
  const referenceChip = { hidden: true, textContent: '' };
  page.elements = { configReferenceChip: referenceChip };
  page.getWorkspaceMCPBindings = () => [];
  page.getWorkspaceSkillBindings = () => [];
  page.tasks = [
    { id: 'task-1', reference_url: 'https://example.com/one' },
    { id: 'task-2', reference_url: '   ' },
    { id: 'task-3', reference_url: 'https://example.com/two' }
  ];

  page.renderWorkspaceConfigSummary();

  assert.equal(referenceChip.hidden, false);
  assert.equal(referenceChip.textContent, 'Refs: 2');

  page.tasks = [{ id: 'task-4' }];
  page.renderWorkspaceConfigSummary();

  assert.equal(referenceChip.hidden, true);
  assert.equal(referenceChip.textContent, 'Refs: 0');
});
