import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  MODE,
  parseWorkspaceURLState,
  serializeWorkspaceURLState,
  sanitizeWorkspaceURLState,
  statesEqual,
  resolveEffectiveMode,
  buildReturnTarget,
  isSafeReturnTarget,
  buildWorkspaceURL
} from './workspace-url-state.js';

test('parseWorkspaceURLState reads all canonical params (FR80)', () => {
  const s = parseWorkspaceURLState('?mode=map&panel=tasks&task=t1&agent=writer&run=t1');
  assert.deepEqual(s, { mode: 'map', panel: 'tasks', task: 't1', agent: 'writer', run: 't1' });
});

test('parseWorkspaceURLState rejects an unknown mode value rather than reusing the retired view param (FR81)', () => {
  assert.equal(parseWorkspaceURLState('?mode=grid').mode, null);
  assert.equal(parseWorkspaceURLState('?view=command').mode, null, 'the legacy view param is not read at all');
});

test('parseWorkspaceURLState handles an empty/missing query string', () => {
  assert.deepEqual(parseWorkspaceURLState(''), { mode: null, panel: '', task: '', agent: '', run: '' });
  assert.deepEqual(parseWorkspaceURLState(undefined), { mode: null, panel: '', task: '', agent: '', run: '' });
});

test('serializeWorkspaceURLState omits empty fields and round-trips', () => {
  const state = { mode: MODE.MAP, panel: 'tasks', task: 't1', agent: '', run: '' };
  const q = serializeWorkspaceURLState(state);
  assert.equal(q, 'mode=map&panel=tasks&task=t1');
  assert.deepEqual(parseWorkspaceURLState(q), { mode: 'map', panel: 'tasks', task: 't1', agent: '', run: '' });
});

test('serializeWorkspaceURLState drops an invalid mode rather than emitting it', () => {
  const q = serializeWorkspaceURLState({ mode: 'bogus', task: 't1' });
  assert.equal(q, 'task=t1');
});

test('sanitizeWorkspaceURLState keeps only values present in the loaded context (FR91)', () => {
  const context = { validTaskIds: ['t1', 't2'], validAgentKeys: ['writer'] };
  const { state, dropped } = sanitizeWorkspaceURLState(
    { mode: MODE.MAP, panel: 'tasks', task: 't1', agent: 'writer', run: 't9' },
    context
  );
  assert.deepEqual(state, { mode: MODE.MAP, panel: 'tasks', task: 't1', agent: 'writer', run: '' });
  assert.deepEqual(dropped, ['run']);
});

test('sanitizeWorkspaceURLState drops a stale task and a stale agent independently', () => {
  const context = { validTaskIds: ['t1'], validAgentKeys: ['writer'] };
  const { state, dropped } = sanitizeWorkspaceURLState(
    { task: 'deleted-task', agent: 'gone-agent', panel: 'tasks' },
    context
  );
  assert.equal(state.task, '');
  assert.equal(state.agent, '');
  assert.equal(state.panel, 'tasks');
  assert.deepEqual(dropped.sort(), ['agent', 'task']);
});

test('sanitizeWorkspaceURLState drops an unknown panel value', () => {
  const { state, dropped } = sanitizeWorkspaceURLState({ panel: 'inventory' }, {});
  assert.equal(state.panel, '');
  assert.deepEqual(dropped, ['panel']);
});

test('sanitizeWorkspaceURLState allows panel=tasks with no task id (drawer opens without a restored preview)', () => {
  const { state, dropped } = sanitizeWorkspaceURLState({ panel: 'tasks' }, { validTaskIds: [] });
  assert.equal(state.panel, 'tasks');
  assert.equal(state.task, '');
  assert.deepEqual(dropped, []);
});

test('sanitizeWorkspaceURLState falls back run validation to validTaskIds when validRunTaskIds is omitted', () => {
  const { state } = sanitizeWorkspaceURLState({ run: 't1' }, { validTaskIds: ['t1'] });
  assert.equal(state.run, 't1');
});

test('statesEqual treats equivalent states as equal regardless of key order/omission (FR86)', () => {
  assert.equal(statesEqual({ mode: 'map', task: 't1' }, { mode: 'map', task: 't1', agent: '' }), true);
  assert.equal(statesEqual({ mode: 'map' }, { mode: 'details' }), false);
  assert.equal(statesEqual(null, {}), true);
});

test('resolveEffectiveMode: URL wins over local preference, which wins over the default (FR85)', () => {
  assert.equal(resolveEffectiveMode(MODE.MAP, MODE.DETAILS), MODE.MAP);
  assert.equal(resolveEffectiveMode(null, MODE.MAP), MODE.MAP);
  assert.equal(resolveEffectiveMode(null, null), MODE.DETAILS);
  assert.equal(resolveEffectiveMode('bogus', MODE.MAP), MODE.MAP, 'an invalid URL mode falls through to the preference');
});

test('buildReturnTarget produces a relative, workspace-scoped path (FR92)', () => {
  const target = buildReturnTarget('ws-1', { mode: MODE.MAP, panel: 'tasks', task: 't1' });
  assert.equal(target, '/workspaces/ws-1?mode=map&panel=tasks&task=t1');
});

test('buildReturnTarget with no extra state is just the workspace path', () => {
  assert.equal(buildReturnTarget('ws-1', {}), '/workspaces/ws-1');
});

test('buildReturnTarget returns empty for a missing workspace id', () => {
  assert.equal(buildReturnTarget('', { mode: MODE.MAP }), '');
});

test('isSafeReturnTarget accepts only this workspace\'s relative path (FR93)', () => {
  assert.equal(isSafeReturnTarget('/workspaces/ws-1', 'ws-1'), true);
  assert.equal(isSafeReturnTarget('/workspaces/ws-1?mode=map', 'ws-1'), true);
  assert.equal(isSafeReturnTarget('/workspaces/ws-1/task/t9', 'ws-1'), true);
});

test('isSafeReturnTarget rejects absolute URLs, protocol-relative links, and cross-workspace paths (open-redirect guard)', () => {
  assert.equal(isSafeReturnTarget('https://evil.example.com/workspaces/ws-1', 'ws-1'), false);
  assert.equal(isSafeReturnTarget('//evil.example.com/workspaces/ws-1', 'ws-1'), false);
  assert.equal(isSafeReturnTarget('/workspaces/other-ws', 'ws-1'), false);
  assert.equal(isSafeReturnTarget('/workspaces/ws-123', 'ws-1'), false, 'prefix match must not leak into a similar id');
  assert.equal(isSafeReturnTarget('', 'ws-1'), false);
  assert.equal(isSafeReturnTarget('/workspaces/ws-1', ''), false);
});

test('buildWorkspaceURL composes pathname + serialized state', () => {
  assert.equal(
    buildWorkspaceURL('/workspaces/ws-1', { mode: MODE.MAP, task: 't1' }),
    '/workspaces/ws-1?mode=map&task=t1'
  );
  assert.equal(buildWorkspaceURL('/workspaces/ws-1', {}), '/workspaces/ws-1');
});
