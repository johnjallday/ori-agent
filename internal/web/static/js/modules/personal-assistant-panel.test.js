import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

import {
  EXPLORE_FOLDER_URL,
  LEGACY_ASSISTANT_HANDOFF_KEY,
  readLegacyAssistantHandoff,
  clearLegacyAssistantHandoff,
  NEEDS_YOU_URL,
  assistantCheckInLine,
  assistantFocusNeedsScroll,
  assistantChipView,
  assistantMoreLinks,
  assistantNeedsLine,
  assistantPanelOpenIntent,
  assistantOpenFocusTarget,
  assistantPanelShouldCloseOnKey,
  boundedAssistantHandoff,
  canSubmitAssistantWork,
  clampDrawerWidth,
  DRAWER_EXPANDED_KEY,
  DRAWER_WIDTH_KEY,
  drawerLayoutView,
  drawerWidthBounds,
  drawerWidthForKey,
  expandedDrawerWidth,
  readDrawerPrefs,
  writeDrawerPrefs,
  ENTRY_HYDRATION_FAILED,
  entryAgentSendRecipient,
  entrySessionStripView,
  personalAssistantPanelView,
  recipientChromeView,
  recipientIdentityView,
  restoreAssistantPanelFocus,
  restoredDraft,
  composerPlaceholder,
  safeTodayRoute,
  suggestedReplyDraft
} from './personal-assistant-panel.js';
import { resolveRecipient } from './personal-assistant-recipient.js';

const workspacePage = {
  surface: 'workspace_detail',
  workspaceId: 'ws-1',
  workspaceName: 'test_reaper',
  entry: { status: 'ready', name: 'REAPER Assistant', role: 'orchestrator' },
  personal: { name: 'Mina', available: true }
};

test('the header, launcher and composer name the entry agent on a workspace page', () => {
  const view = personalAssistantPanelView({ state: 'active', display_name: 'Mina' });
  const who = recipientIdentityView(view, resolveRecipient(workspacePage));
  assert.equal(who.personal, false);
  assert.equal(who.name, 'REAPER Assistant');
  assert.equal(who.role, 'Commander · test_reaper');
  assert.equal(who.subline, 'Commander · test_reaper');
  assert.equal(
    who.placeholder,
    'Ask REAPER Assistant about this workspace… (/task creates a task)'
  );
  assert.equal(who.available, true);
  assert.equal(who.launcherVisible, true);
  // Never the hired assistant's name while a workspace agent is the target.
  assert.notEqual(who.name, view.name);
});

test('the Personal Assistant identity is unchanged when it is the recipient', () => {
  const view = personalAssistantPanelView({ state: 'active', display_name: 'Mina' });
  for (const resolution of [
    resolveRecipient({ ...workspacePage, surface: 'home' }),
    resolveRecipient({ ...workspacePage, pick: 'personal' }),
    null
  ]) {
    const who = recipientIdentityView(view, resolution);
    assert.equal(who.personal, true);
    assert.equal(who.name, 'Mina');
    assert.equal(who.role, 'Personal Assistant');
    assert.equal(who.placeholder, view.placeholder);
    assert.equal(who.available, true);
  }
});

test('the launcher stays for a reachable entry agent when the assistant is not set up (FR6)', () => {
  for (const state of ['needs_hire', 'needs_hq', 'provisioning_hq', 'repair_needed']) {
    const view = personalAssistantPanelView({ state, display_name: 'Mina' });
    const entry = recipientIdentityView(view, resolveRecipient(workspacePage));
    assert.equal(entry.launcherVisible, true, state);
    assert.equal(entry.available, true, state);
    // Picking the unavailable assistant keeps the launcher and its setup copy.
    const picked = recipientIdentityView(
      view,
      resolveRecipient({ ...workspacePage, pick: 'personal' })
    );
    assert.equal(picked.launcherVisible, true, state);
    assert.equal(picked.available, false, state);
    assert.equal(picked.placeholder, view.placeholder, state);
  }
  // With a vacant Commander only the assistant's own visibility counts.
  const unhired = personalAssistantPanelView({ state: 'needs_hire' });
  const vacant = recipientIdentityView(
    unhired,
    resolveRecipient({ ...workspacePage, entry: { status: 'vacant' } })
  );
  assert.equal(vacant.launcherVisible, false);
});

test('before the assistant status answers, no workspace agent is named or offered', () => {
  // The page may be the Personal HQ, whose own agent must never flash in.
  const view = personalAssistantPanelView(null);
  const resolution = resolveRecipient({ ...workspacePage, personal: { known: false } });
  const who = recipientIdentityView(view, resolution);
  assert.equal(who.personal, true);
  assert.equal(who.name, view.name);
  assert.equal(who.launcherVisible, false);
  assert.equal(who.available, false);
  assert.equal(
    entrySessionStripView({ recipient: resolution.current, session: { id: '' } }).visible,
    false
  );
});

test('a loading Commander is named by role and cannot be sent to yet', () => {
  const view = personalAssistantPanelView({ state: 'active', display_name: 'Mina' });
  const who = recipientIdentityView(
    view,
    resolveRecipient({ ...workspacePage, entry: { status: 'loading' } })
  );
  assert.equal(who.name, 'Commander');
  assert.equal(who.available, false);
  assert.equal(who.placeholder, 'Finding this workspace’s Commander…');
  // The launcher waits for the agent's name; a drawer reopened on reload may
  // already show, and says it is still looking.
  assert.equal(who.launcherVisible, false);
  assert.equal(who.openable, true);
  assert.equal(recipientIdentityView(view, resolveRecipient(workspacePage)).launcherVisible, true);
});

test('only a ready entry agent gives a send its recipient and current session', () => {
  const resolution = resolveRecipient(workspacePage);
  assert.deepEqual(entryAgentSendRecipient(resolution.current, 'sess-1'), {
    kind: 'workspace_entry',
    name: 'REAPER Assistant',
    workspaceId: 'ws-1',
    workspaceName: 'test_reaper',
    appearance: null,
    sessionId: 'sess-1'
  });
  assert.equal(entryAgentSendRecipient(resolution.current, '').sessionId, '');
  assert.equal(entryAgentSendRecipient(resolution.options[1], 'sess-1'), null);
  assert.equal(
    entryAgentSendRecipient(
      resolveRecipient({ ...workspacePage, entry: { status: 'loading' } }).current,
      ''
    ),
    null
  );
});

test('the launcher role line can be updated per recipient', () => {
  const markup = readFileSync(
    new URL('../../../templates/components/ori-guide.tmpl', import.meta.url),
    'utf8'
  );
  assert.match(markup, /id="personalAssistantLauncherRole"/);
  // The entry agent's session strip is its own, never the personal one's.
  assert.match(markup, /id="personalAssistantEntrySessionBar"[^>]*hidden/);
  assert.match(markup, /id="personalAssistantEntrySessionTitle"[^>]*>New conversation</);
  assert.match(markup, /id="personalAssistantEntrySessionNew"/);
});

test('the session strip names the entry agent session or a new conversation (FR22)', () => {
  const entry = resolveRecipient(workspacePage).current;
  assert.deepEqual(
    entrySessionStripView({ recipient: entry, session: { id: 's1', title: 'Add a reverb bus' } }),
    { visible: true, title: 'Add a reverb bus', newDisabled: false }
  );
  assert.equal(
    entrySessionStripView({ recipient: entry, session: { id: 's1' } }).title,
    'Conversation'
  );
  assert.deepEqual(entrySessionStripView({ recipient: entry, session: { id: '' } }), {
    visible: true,
    title: 'New conversation',
    newDisabled: true
  });
  // Unsaved rows (a turn that failed before a session existed) can be cleared.
  assert.equal(
    entrySessionStripView({ recipient: entry, session: { id: '' }, hasMessages: true }).newDisabled,
    false
  );
  const personal = resolveRecipient({ ...workspacePage, pick: 'personal' }).current;
  assert.equal(
    entrySessionStripView({ recipient: personal, session: { id: 's1' } }).visible,
    false
  );
  assert.equal(entrySessionStripView({}).visible, false);
});

test('the hired assistant chrome shows only while it is the recipient (FR17, FR18)', () => {
  const view = personalAssistantPanelView({ state: 'active', display_name: 'Mina' });
  const entry = recipientIdentityView(view, resolveRecipient(workspacePage));
  const personal = recipientIdentityView(
    view,
    resolveRecipient({ ...workspacePage, pick: 'personal' })
  );
  assert.deepEqual(recipientChromeView(entry, { visible: true }), {
    needsLine: false,
    attachments: false,
    personalConversation: false,
    entrySession: true,
    context: true
  });
  assert.deepEqual(recipientChromeView(personal, { visible: true }), {
    needsLine: true,
    attachments: true,
    personalConversation: true,
    entrySession: false,
    context: true
  });
  // Nothing needs the user: the line stays hidden for the assistant too.
  assert.equal(recipientChromeView(personal, { visible: false }).needsLine, false);
});

test('the header name is one element that moves into the menu button', () => {
  const markup = readFileSync(
    new URL('../../../templates/components/ori-guide.tmpl', import.meta.url),
    'utf8'
  );
  const heading = markup.match(/<h2 id="personalAssistantPanelTitle"[^>]*>([\s\S]*?)<\/h2>/)?.[1];
  assert.ok(heading, 'the drawer keeps its heading');
  // Only the name has text, so the heading reads as the name in either state.
  assert.equal(heading.replace(/<[^>]+>/g, '').trim(), 'Personal assistant');
  assert.match(heading, /^<span id="personalAssistantPanelName"/);
  const button = heading.match(/<button[\s\S]*?>/)?.[0] || '';
  assert.match(button, /id="personalAssistantRecipientButton"/);
  assert.match(button, /aria-haspopup="menu"/);
  assert.match(button, /aria-expanded="false"/);
  assert.match(button, /aria-controls="personalAssistantRecipientMenu"/);
  assert.match(button, /\bhidden\b/);
  assert.match(
    markup,
    /id="personalAssistantRecipientMenu"[^>]*role="menu"[\s\S]*?aria-label="Who to talk to" hidden>/
  );
});

test('the hired assistant flows in the shared thread are hidden for the entry agent', () => {
  const css = readFileSync(new URL('../../css/ori-guide.css', import.meta.url), 'utf8');
  const rule = css.match(
    /\.personal-assistant-panel\[data-recipient="workspace_entry"\]\s*:is\(([^)]*)\)\s*\{\s*display: none;/
  );
  assert.ok(rule, 'one rule hides them while the entry agent is the recipient');
  for (const id of [
    'personalAssistantFolder',
    'personalAssistantDraftReview',
    'personalAssistantMemoryReview',
    'assistantLedSetup',
    'personalAssistantFolderExplorer',
    'personalAssistantOperation'
  ])
    assert.match(rule[1], new RegExp(`#${id}\\b`), id);
});

test('a failed hydration says what happens next and does not block sending (FR21)', () => {
  assert.equal(
    ENTRY_HYDRATION_FAILED,
    'Could not load this conversation. Your next message starts where the server left off.'
  );
});

test('backup-console navigation owns focus and Today consumes only its own query', () => {
  assert.deepEqual(assistantPanelOpenIntent('?panel=backup-recovery&tab=overview', false), {
    requestedOpen: false,
    restoreAllowed: false
  });
  assert.deepEqual(assistantPanelOpenIntent('?panel=today', true), {
    requestedOpen: true,
    restoreAllowed: true
  });
  for (const query of ['', '?panel=calendar', '?panel=file-janitor'])
    assert.deepEqual(assistantPanelOpenIntent(query, false), {
      requestedOpen: false,
      restoreAllowed: true
    });
});

test('only a focused control outside the scroll viewport needs reflow recovery', () => {
  const viewport = { top: 100, bottom: 500 };
  assert.equal(assistantFocusNeedsScroll(viewport, { top: 100, bottom: 500 }), false);
  assert.equal(assistantFocusNeedsScroll(viewport, { top: 99, bottom: 140 }), true);
  assert.equal(assistantFocusNeedsScroll(viewport, { top: 480, bottom: 524 }), true);
  assert.equal(assistantFocusNeedsScroll(null, null), false);
});

test('suggested replies fill only an empty composer and preserve exact existing text', () => {
  const text = 'Discuss “同じ名前 🎼”';
  const result = suggestedReplyDraft({ available: true, text });
  assert.equal(result.accepted, true);
  assert.equal(result.draft, text);
  assert.match(result.notice, /Review, then Send/);
  for (const current of ['My exact draft', '  ', '\n🎼  ']) {
    const rejected = suggestedReplyDraft({ available: true, text, current });
    assert.equal(rejected.accepted, false);
    assert.equal(rejected.draft, current);
    assert.match(rejected.notice, /Send or clear/);
  }
});

test('suggested reply length uses the composer UTF-16 bound without splitting a name', () => {
  assert.equal(suggestedReplyDraft({ available: true, text: '🎼'.repeat(1000) }).accepted, true);
  const tooLong = suggestedReplyDraft({ available: true, text: '🎼'.repeat(1001) });
  assert.equal(tooLong.accepted, false);
  assert.equal(tooLong.draft, '');
  assert.equal(
    suggestedReplyDraft({ available: true, text: 'Question', maxLength: 4 }).accepted,
    false
  );
});

test('suggested replies reject unavailable, busy, pending selection and loading history without draft loss', () => {
  for (const block of [
    { available: false },
    { pending: true },
    { busy: true },
    { loading: true },
    { folderPending: true }
  ]) {
    const result = suggestedReplyDraft({
      available: true,
      current: 'Keep this',
      text: 'Suggestion',
      ...block
    });
    assert.equal(result.accepted, false);
    assert.equal(result.draft, 'Keep this');
  }
});

test('personal assistant panel covers unavailable, pre-hire, active, paused, and repair states', () => {
  assert.equal(personalAssistantPanelView(null).known, false);

  const preHire = personalAssistantPanelView({ state: 'needs_hire' });
  assert.equal(preHire.known, true);
  assert.equal(preHire.helpOnly, true);
  assert.equal(preHire.available, false);
  assert.equal(preHire.needsHire, true);

  const active = personalAssistantPanelView({ state: 'active', display_name: 'Nova' });
  assert.equal(active.available, true);
  assert.equal(active.name, 'Nova');
  assert.match(active.placeholder, /Ask Nova/);

  const paused = personalAssistantPanelView({ state: 'paused', display_name: 'Nova' });
  assert.equal(paused.available, true);
  assert.equal(paused.paused, true);

  const repair = personalAssistantPanelView({ state: 'repair_needed', display_name: 'Nova' });
  assert.equal(repair.repair, true);
  assert.equal(repair.available, false);
  assert.equal(repair.visible, true, 'repair guidance stays reachable without enabling work');
});

test('a hired assistant with no HQ is named but disabled, unlike needsHire', () => {
  const needsHQ = personalAssistantPanelView({ state: 'needs_hq', display_name: 'Atlas' });
  assert.equal(needsHQ.known, true);
  assert.equal(needsHQ.available, false, 'submission must stay closed before HQ exists');
  assert.equal(needsHQ.needsHQ, true);
  assert.equal(needsHQ.needsHire, false);
  assert.equal(needsHQ.name, 'Atlas');
  // Unlike needsHire, the launcher may show this real identity.
  assert.equal(needsHQ.visible, true);
  assert.match(needsHQ.placeholder, /Build Atlas.s Personal HQ/);

  const provisioning = personalAssistantPanelView({
    state: 'provisioning_hq',
    display_name: 'Atlas'
  });
  assert.equal(provisioning.needsHQ, true);
  assert.equal(provisioning.available, false);
  assert.equal(provisioning.visible, true);

  // needsHire still has nothing trustworthy to show.
  const preHire = personalAssistantPanelView({ state: 'needs_hire' });
  assert.equal(preHire.visible, false);
});

test('handoff text is exact, trimmed, unicode-safe, and bounded without submitting', () => {
  assert.equal(boundedAssistantHandoff('  send the notes  '), 'send the notes');
  assert.equal(Array.from(boundedAssistantHandoff('🦊'.repeat(500))).length, 400);
});

test('legacy handoff reads are bounded, unsent, and never consume storage', () => {
  const original = '🦊'.repeat(500);
  let removes = 0;
  const storage = {
    getItem: key => {
      assert.equal(key, LEGACY_ASSISTANT_HANDOFF_KEY);
      return original;
    },
    removeItem: () => {
      removes += 1;
    }
  };
  const recovered = readLegacyAssistantHandoff(storage);
  assert.equal(Array.from(recovered.text).length, 400);
  assert.equal(recovered.original, original);
  assert.equal(removes, 0);
  assert.deepEqual(
    readLegacyAssistantHandoff(storage),
    recovered,
    'reloads retain the offer until explicitly accepted or dismissed'
  );
  assert.equal(clearLegacyAssistantHandoff(storage, original), true);
  assert.equal(removes, 1);
});

test('legacy recovery handles disabled storage and does not delete a newer request', () => {
  const denied = {
    getItem() {
      throw new Error('denied');
    },
    removeItem() {
      throw new Error('denied');
    }
  };
  assert.equal(readLegacyAssistantHandoff(denied), null);
  assert.equal(clearLegacyAssistantHandoff(denied, 'old'), false);
  let removed = false;
  const replaced = {
    getItem: () => 'new',
    removeItem: () => {
      removed = true;
    }
  };
  assert.equal(clearLegacyAssistantHandoff(replaced, 'old'), false);
  assert.equal(removed, false);
});

test('malformed relationship state is unavailable, never a confirmed identity', () => {
  const view = personalAssistantPanelView({ state: 'unexpected', display_name: 'Unconfirmed' });
  assert.equal(view.known, false);
  assert.equal(view.visible, false);
  assert.equal(view.available, false);
  assert.equal(view.name, 'Personal assistant');
  assert.equal(
    personalAssistantPanelView({ state: ['active'], display_name: 'Unconfirmed' }).known,
    false
  );
});

test('assistant composer refuses empty, unavailable, pending, and double-click states', () => {
  assert.equal(canSubmitAssistantWork({ available: true, pending: false, text: 'help' }), true);
  assert.equal(canSubmitAssistantWork({ available: false, pending: false, text: 'help' }), false);
  assert.equal(canSubmitAssistantWork({ available: true, pending: true, text: 'help' }), false);
  assert.equal(canSubmitAssistantWork({ available: true, pending: false, text: '  ' }), false);
  // A reply still in flight blocks a second turn: it has no conversation to join yet.
  assert.equal(
    canSubmitAssistantWork({ available: true, pending: false, busy: true, text: 'help' }),
    false
  );
  assert.equal(
    canSubmitAssistantWork({ available: true, pending: false, busy: false, text: 'help' }),
    true
  );
});

test('an unsent message returns to the composer without overwriting newer typing', () => {
  assert.equal(restoredDraft('', 'make it warmer'), 'make it warmer');
  assert.equal(restoredDraft('   ', 'make it warmer'), 'make it warmer');
  assert.equal(restoredDraft('typed since', 'make it warmer'), 'typed since');
  assert.equal(restoredDraft('', '생일 축하해 🎂'), '생일 축하해 🎂');
  assert.equal(restoredDraft(null, null), '');
});

test('opening the drawer focuses the composer whenever the assistant can accept work', () => {
  for (const state of ['active', 'paused']) {
    assert.equal(assistantOpenFocusTarget(personalAssistantPanelView({ state })), 'composer');
  }
  // Not hired, HQ not built, repair needed, unknown: the composer is disabled,
  // so focus goes to the first control in the drawer instead.
  for (const state of [
    'needs_hire',
    'hiring',
    'needs_hq',
    'provisioning_hq',
    'repair_needed',
    'unavailable'
  ]) {
    assert.equal(
      assistantOpenFocusTarget(personalAssistantPanelView({ state })),
      'first-control',
      state
    );
  }
  assert.equal(assistantOpenFocusTarget(null), 'first-control');
});

test('"Explore a folder" is offered when the assistant can accept work, and waits its turn', () => {
  // Active or paused already means a Personal HQ exists.
  for (const state of ['active', 'paused']) {
    const { available } = personalAssistantPanelView({ state });
    assert.deepEqual(assistantChipView({ available, folderBusy: false }), {
      visible: true,
      disabled: false
    });
    // A folder is being chosen or explored: the chip stays, and cannot be pressed.
    assert.deepEqual(assistantChipView({ available, folderBusy: true }), {
      visible: true,
      disabled: true
    });
  }
  for (const state of ['needs_hire', 'needs_hq', 'provisioning_hq', 'repair_needed']) {
    const { available } = personalAssistantPanelView({ state });
    assert.equal(assistantChipView({ available }).visible, false, state);
  }
  assert.equal(assistantChipView().visible, false);
  // Off Home the chip goes to Home's flow, which the drawer there starts.
  assert.equal(EXPLORE_FOLDER_URL, '/?panel=today&folder=show');
});

test('a page that is not Home says how much needs the user, in one line that goes there', () => {
  const items = count => Array.from({ length: count }, (_, i) => ({ kind: 'task', title: `${i}` }));
  const today = count => ({ state: 'active', needs_you: { items: items(count) } });

  assert.deepEqual(assistantNeedsLine(today(3)), { visible: true, text: '3 need you' });
  assert.deepEqual(assistantNeedsLine(today(12)), { visible: true, text: '12 need you' });
  assert.deepEqual(assistantNeedsLine(today(1)), { visible: true, text: '1 needs you' });
  // Nothing needs the user: no line at all.
  assert.deepEqual(assistantNeedsLine(today(0)), { visible: false, text: '' });
  assert.deepEqual(assistantNeedsLine({ state: 'paused' }), { visible: false, text: '' });

  // The number cannot be read. A missing line would read as "nothing needs
  // you", so the line stays and points at Home.
  const unknown = { visible: true, text: 'Open Home to see what needs you' };
  assert.deepEqual(
    assistantNeedsLine({
      state: 'active',
      needs_you: { health: { status: 'partial', reason: 'backup_source_unavailable' }, items: [] }
    }),
    unknown
  );
  assert.deepEqual(
    assistantNeedsLine({
      state: 'active',
      needs_you: { health: { status: 'partial' }, items: [{ kind: 'backup_observation' }] }
    }),
    { visible: true, text: '1 needs you' }
  );
  assert.deepEqual(assistantNeedsLine(null, { failed: true }), unknown);
  assert.deepEqual(assistantNeedsLine(today(2), { failed: true }), unknown);
  assert.deepEqual(assistantNeedsLine(null), unknown);
  assert.deepEqual(assistantNeedsLine({ state: 'unavailable' }), unknown);
  assert.deepEqual(
    assistantNeedsLine({
      state: 'partial',
      needs_you: { health: { status: 'unavailable' }, items: [] }
    }),
    unknown
  );
});

test('the needs-you line is rendered on every page but Home, and links to Home', () => {
  const drawer = readFileSync(
    new URL('../../../templates/components/ori-guide.tmpl', import.meta.url),
    'utf8'
  );
  assert.equal((drawer.match(/id="personalAssistantNeedsLine"/g) || []).length, 1);
  // Home renders its Today there instead: the line is the other branch.
  assert.match(
    drawer,
    /\{\{if eq \.CurrentPage "index"\}\}\s*\{\{template "personal-assistant-today\.tmpl" \.\}\}\s*\{\{else\}\}[\s\S]*?id="personalAssistantNeedsLine"[\s\S]*?\{\{end\}\}/
  );
  const link = drawer.match(/<a id="personalAssistantNeedsLine"[^>]*>/)[0];
  assert.ok(link.includes(`href="${NEEDS_YOU_URL}"`), link);
  assert.match(link, /\shidden>/, 'hidden until the number is known');
  assert.equal(NEEDS_YOU_URL, '/?panel=today');
});

test('Add folder sits inside the composer, on every page', () => {
  const drawer = readFileSync(
    new URL('../../../templates/components/ori-guide.tmpl', import.meta.url),
    'utf8'
  );
  const chips = drawer.indexOf('id="personalAssistantChips"');
  const form = drawer.indexOf('id="personalAssistantForm"');
  assert.ok(chips > form && form > 0);
  assert.ok(chips < drawer.indexOf('id="personalAssistantInput"'));
  // The attachment row remains outside the scrolling region.
  assert.ok(drawer.indexOf('id="personalAssistantPanelStatus"') < chips);
  assert.ok(drawer.indexOf('id="personalAssistantThread"') < chips);
  // Add folder, Tree + Chat, and Add link.
  assert.equal((drawer.match(/class="personal-assistant-panel__chip"/g) || []).length, 3);
  assert.match(drawer, /id="personalAssistantExploreAttachedFolder"[^>]+hidden>Tree \+ Chat/);
  const linkChip = drawer.indexOf('id="personalAssistantLinkChip"');
  assert.ok(linkChip > chips && linkChip < drawer.indexOf('id="personalAssistantInput"'));
  assert.match(drawer, /id="personalAssistantLinkChip"[^>]+hidden>[\s\S]{0,120}Add link/);
  assert.equal((drawer.match(/id="personalAssistantInput"/g) || []).length, 1);
  assert.equal((drawer.match(/id="personalAssistantForm"/g) || []).length, 1);
  assert.match(drawer, /id="personalAssistantFolderChip"[\s\S]{0,350}Add folder/);
  assert.match(drawer, /id="personalAssistantRemoveFolder" aria-label="Remove folder context"/);
  // Not inside a part of the drawer that only some pages render: the last
  // page condition before it has already ended.
  const before = drawer.slice(0, chips);
  const lastCondition = before.lastIndexOf('.CurrentPage "index"}}');
  assert.ok(lastCondition > 0);
  assert.match(before.slice(lastCondition), /\{\{end\}\}/);
});

test('the header line says when the next check-in is, in the wording Today used', () => {
  assert.deepEqual(
    assistantCheckInLine({ state: 'active', next_check_in: '2026-10-08T08:00:00Z' }),
    { text: 'Next check-in · ', time: '2026-10-08T08:00:00Z' }
  );
  assert.deepEqual(assistantCheckInLine({ state: 'active' }), {
    text: 'No check-in scheduled',
    time: ''
  });
  // Paused wins over a stored time: nothing is coming while paused.
  assert.deepEqual(
    assistantCheckInLine({ state: 'paused', next_check_in: '2026-10-08T08:00:00Z' }),
    { text: 'Check-ins paused', time: '' }
  );
  assert.deepEqual(assistantCheckInLine({ state: 'active', next_check_in: 'not a date' }), {
    text: 'Next check-in unavailable',
    time: ''
  });
  for (const state of ['partial', 'model_unavailable', 'healthy_empty']) {
    assert.equal(assistantCheckInLine({ state }).text, 'No check-in scheduled', state);
  }
});

test('the header line is the plain role until a check-in can be known', () => {
  for (const today of [
    null,
    undefined,
    {},
    { state: 'loading' },
    { state: 'unavailable' },
    { state: 'needs_hire' },
    { state: 'needs_hq' },
    { state: 'repair_needed', next_check_in: '2026-10-08T08:00:00Z' }
  ]) {
    assert.deepEqual(assistantCheckInLine(today), { text: 'Personal Assistant', time: '' });
  }
});

test('the More menu shows only links Today validated, and the interview only when offered', () => {
  const links = assistantMoreLinks({
    links: {
      personal_hq: '/workspaces/my-hq',
      working_agreement: '/?personal-assistant=working-agreement',
      memory: '/workspaces/my-hq#memory',
      advanced: '/agents'
    },
    interview_status: 'offered'
  });
  assert.deepEqual(links, {
    personal_hq: '/workspaces/my-hq',
    working_agreement: '/?personal-assistant=working-agreement',
    memory: '/workspaces/my-hq#memory',
    advanced: '/agents',
    interview: true
  });

  const hostile = assistantMoreLinks({
    links: { personal_hq: 'https://evil.example/', memory: '//evil.example', advanced: '' },
    interview_status: 'completed'
  });
  assert.deepEqual(hostile, {
    personal_hq: '',
    working_agreement: '',
    memory: '',
    advanced: '',
    interview: false
  });
  for (const status of ['available', 'offered', 'deferred']) {
    assert.equal(assistantMoreLinks({ interview_status: status }).interview, true, status);
  }
  assert.equal(assistantMoreLinks(null).interview, false);
});

test('safeTodayRoute accepts same-origin paths and refuses everything else', () => {
  assert.equal(safeTodayRoute('/workspaces/my-hq?station=daily-brief'), true);
  assert.equal(safeTodayRoute('/'), true);
  for (const route of [
    '',
    'workspaces/my-hq',
    '//evil.example',
    'https://evil.example/',
    '/a/../b',
    '/a\\b',
    'javascript:alert(1)'
  ]) {
    assert.equal(safeTodayRoute(route), false, route);
  }
});

test('Escape closes only an open drawer and focus returns only to a connected trigger', () => {
  assert.equal(assistantPanelShouldCloseOnKey('Escape', true), true);
  assert.equal(assistantPanelShouldCloseOnKey('Escape', true, true), false);
  assert.equal(assistantPanelShouldCloseOnKey('Escape', false), false);
  assert.equal(assistantPanelShouldCloseOnKey('Enter', true), false);

  let focused = false;
  const trigger = { focus: () => (focused = true) };
  assert.equal(restoreAssistantPanelFocus(trigger, { contains: value => value === trigger }), true);
  assert.equal(focused, true);
  assert.equal(restoreAssistantPanelFocus(trigger, { contains: () => false }), false);
  assert.equal(restoreAssistantPanelFocus(null, { contains: () => true }), false);
});

test('with a folder attached the placeholder says what an empty Send asks for', () => {
  const ready = {
    available: true,
    placeholder: 'Ask Atlas a question or describe what you want help with'
  };
  assert.equal(
    composerPlaceholder(ready, 'Propose workspaces for Albums'),
    'Propose workspaces for Albums'
  );
  assert.equal(composerPlaceholder(ready, 'Explore this folder'), 'Explore this folder');
  assert.equal(composerPlaceholder(ready, ''), ready.placeholder);
  // An assistant that cannot take work says why, folder or not.
  const blocked = {
    available: false,
    placeholder: 'Finish personal assistant setup before sending work'
  };
  assert.equal(composerPlaceholder(blocked, 'Explore this folder'), blocked.placeholder);
});

function memoryStorage(seed = {}) {
  const values = new Map(Object.entries(seed));
  return {
    values,
    getItem: key => (values.has(key) ? values.get(key) : null),
    setItem: (key, value) => values.set(key, String(value))
  };
}

const throwingStorage = {
  getItem() {
    throw new Error('SecurityError');
  },
  setItem() {
    throw new Error('QuotaExceededError');
  }
};

test('the sheet width range leaves the page its left margin (FR3)', () => {
  // min(180px, 20%) of the page and the 20px right gap stay outside the sheet.
  assert.deepEqual(drawerWidthBounds(1280), { min: 560, max: 960 });
  assert.deepEqual(drawerWidthBounds(1440), { min: 560, max: 960 });
  assert.deepEqual(drawerWidthBounds(1920), { min: 560, max: 960 });
  assert.deepEqual(drawerWidthBounds(900), { min: 560, max: 700 });
  assert.deepEqual(drawerWidthBounds(800), { min: 560, max: 620 });
  // A zoomed viewport narrower than the range: the bottom follows the top down.
  assert.deepEqual(drawerWidthBounds(390), { min: 292, max: 292 });
});

test('a remembered width is clamped for the viewport, never dropped (FR3)', () => {
  assert.equal(clampDrawerWidth(960, 1440), 960);
  assert.equal(clampDrawerWidth(960, 900), 700);
  assert.equal(clampDrawerWidth(400, 1440), 560);
  assert.equal(clampDrawerWidth(700.6, 1440), 701);
  assert.equal(clampDrawerWidth(Number.NaN, 1440), 640);
  assert.equal(clampDrawerWidth(undefined, 1920), 640);
});

test('Expand takes the widest sheet the page allows (FR5)', () => {
  assert.equal(expandedDrawerWidth(1280), 1080);
  assert.equal(1280 - 20 - expandedDrawerWidth(1280), 180);
  assert.equal(expandedDrawerWidth(1440), 1180);
  assert.equal(expandedDrawerWidth(1920), 1180);
  assert.ok(1440 - 20 - expandedDrawerWidth(1440) >= 180);
});

test('the saved width and Expand choice survive bad and missing storage (FR4)', () => {
  assert.deepEqual(
    readDrawerPrefs(memoryStorage({ [DRAWER_WIDTH_KEY]: '900', [DRAWER_EXPANDED_KEY]: '1' })),
    { width: 900, expanded: true }
  );
  // Above every cap is still a valid saved width; it is clamped where applied.
  assert.equal(readDrawerPrefs(memoryStorage({ [DRAWER_WIDTH_KEY]: '1400' })).width, 1400);
  for (const bad of ['abc', '-5', '12.5', '0', '', ' ', '9e3', 'null'])
    assert.equal(readDrawerPrefs(memoryStorage({ [DRAWER_WIDTH_KEY]: bad })).width, 640, bad);
  assert.equal(readDrawerPrefs(memoryStorage({ [DRAWER_EXPANDED_KEY]: 'true' })).expanded, false);
  assert.deepEqual(readDrawerPrefs(null), { width: 640, expanded: false });
  assert.deepEqual(readDrawerPrefs(throwingStorage), { width: 640, expanded: false });

  const store = memoryStorage();
  assert.equal(writeDrawerPrefs(store, { width: 700.4, expanded: false }), true);
  assert.equal(store.values.get(DRAWER_WIDTH_KEY), '700');
  assert.equal(store.values.get(DRAWER_EXPANDED_KEY), '0');
  // A drag end writes only the width; Expand writes only its choice.
  writeDrawerPrefs(store, { expanded: true });
  assert.equal(store.values.get(DRAWER_WIDTH_KEY), '700');
  assert.equal(store.values.get(DRAWER_EXPANDED_KEY), '1');
  assert.equal(writeDrawerPrefs(throwingStorage, { width: 700 }), false);
  assert.equal(writeDrawerPrefs(null, { width: 700 }), false);
});

test('the handle keys step by 16, 64 with Shift, and jump with Home and End (FR2)', () => {
  const at = { width: 640, min: 560, max: 960 };
  // Left moves the left edge outwards: wider.
  assert.equal(drawerWidthForKey('ArrowLeft', at), 656);
  assert.equal(drawerWidthForKey('ArrowRight', at), 624);
  assert.equal(drawerWidthForKey('ArrowLeft', { ...at, shift: true }), 704);
  assert.equal(drawerWidthForKey('ArrowRight', { ...at, shift: true }), 576);
  assert.equal(drawerWidthForKey('Home', at), 560);
  assert.equal(drawerWidthForKey('End', at), 960);
  assert.equal(drawerWidthForKey('ArrowLeft', { ...at, width: 950 }), 960);
  assert.equal(drawerWidthForKey('ArrowRight', { ...at, width: 570, shift: true }), 560);
  assert.equal(drawerWidthForKey('ArrowUp', at), null);
  assert.equal(drawerWidthForKey('Enter', at), null);
});

test('the sheet layout: width, handle and Expand per state (FR5, FR6, FR9)', () => {
  const prefs = { width: 900, expanded: false };
  const plain = drawerLayoutView({ viewportWidth: 1440, prefs });
  assert.equal(plain.sheet, true);
  assert.equal(plain.width, 900);
  assert.deepEqual(plain.bounds, { min: 560, max: 960 });
  assert.equal(plain.handleVisible, true);
  assert.equal(plain.handleInert, false);
  assert.equal(plain.expandVisible, true);
  assert.equal(plain.expanded, false);
  assert.equal(plain.expandLabel, 'Expand');

  const expanded = drawerLayoutView({ viewportWidth: 1280, prefs: { ...prefs, expanded: true } });
  assert.equal(expanded.width, 1080);
  assert.equal(expanded.handleWidth, 900);
  assert.equal(expanded.handleInert, true);
  assert.equal(expanded.expanded, true);
  assert.equal(expanded.expandLabel, 'Shrink');

  // Tree + Chat borrows the expanded width; the saved choice is untouched.
  const exploring = drawerLayoutView({ viewportWidth: 1440, prefs, exploring: true });
  assert.equal(exploring.width, 1180);
  assert.equal(exploring.handleInert, true);
  assert.equal(exploring.expandVisible, false);
  assert.equal(exploring.expanded, false);
  assert.deepEqual(prefs, { width: 900, expanded: false });

  // A 900px saved width on a 900px viewport is fitted, not discarded.
  assert.equal(drawerLayoutView({ viewportWidth: 900, prefs }).width, 700);

  // The phone sheet, or a zoom that reaches phone width: no handle, no Expand.
  for (const viewportWidth of [760, 390]) {
    const phone = drawerLayoutView({ viewportWidth, prefs });
    assert.equal(phone.sheet, false);
    assert.equal(phone.handleVisible, false);
    assert.equal(phone.expandVisible, false);
  }
});

test('the drawer carries the resize handle and the Expand toggle (FR2, FR5)', () => {
  const markup = readFileSync(
    new URL('../../../templates/components/ori-guide.tmpl', import.meta.url),
    'utf8'
  );
  const handle = markup.match(/<div id="personalAssistantResize"[^>]*>/)?.[0] || '';
  for (const attribute of [
    'role="separator"',
    'aria-orientation="vertical"',
    'aria-label="Resize assistant"',
    'aria-valuemin="560"',
    'aria-valuemax="960"',
    'aria-valuenow="640"',
    'tabindex="0"'
  ])
    assert.ok(handle.includes(attribute), attribute);
  assert.match(handle, /\bhidden\b/);
  const expand = markup.match(/<button[^>]*id="personalAssistantExpand"[^>]*>/)?.[0] || '';
  assert.match(expand, /aria-pressed="false"/);
  assert.match(expand, /aria-label="Expand"/);
  assert.match(expand, /\bhidden\b/);
  // Expand sits in the header before More and Close.
  const header =
    markup.match(/<header class="personal-assistant-panel__header">[\s\S]*?<\/header>/)?.[0] || '';
  assert.ok(header.indexOf('personalAssistantExpand') > 0);
  assert.ok(header.indexOf('personalAssistantExpand') < header.indexOf('personalAssistantMore'));
  assert.ok(header.indexOf('personalAssistantMore') < header.indexOf('personalAssistantClose'));
});
