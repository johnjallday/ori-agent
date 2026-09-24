import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import {
  currentTier,
  completedCount,
  resolvedCount,
  tierInsignia,
  compactSummaryView,
  missionKicker,
  firstMissionView,
  firstMissionOfferView,
  tierQuestRows,
  questRowState,
  renderQuestRow,
  diffAnnouncements
} from './progression-widget.js';

function quest(overrides) {
  return { id: 'q1', title: 'Quest', status: 'available', optional: false, ...overrides };
}

function tier(overrides) {
  return { tier: 2, name: 'Establish a Base', complete: false, quests: [], ...overrides };
}

test('currentTier finds the tier matching current_tier', () => {
  const t1 = tier({ tier: 1 });
  const t2 = tier({ tier: 2 });
  assert.equal(currentTier({ current_tier: 2, tiers: [t1, t2] }), t2);
});

test('currentTier falls back to the first tier when current_tier is not found', () => {
  const t1 = tier({ tier: 1 });
  assert.equal(currentTier({ current_tier: 99, tiers: [t1] }), t1);
});

test('completedCount counts only completed quests, not skipped ones', () => {
  const t = tier({
    quests: [
      quest({ status: 'completed' }),
      quest({ status: 'skipped' }),
      quest({ status: 'available' })
    ]
  });
  assert.equal(completedCount(t), 1);
});

test('resolvedCount counts completed and skipped quests together (task 3.4)', () => {
  const t = tier({
    quests: [
      quest({ status: 'completed' }),
      quest({ status: 'skipped' }),
      quest({ status: 'available' })
    ]
  });
  assert.equal(resolvedCount(t), 2);
});

test('tierInsignia zero-pads a positive tier number', () => {
  assert.equal(tierInsignia(2), '02');
  assert.equal(tierInsignia(11), '11');
});

test('tierInsignia falls back to an em dash for an invalid tier', () => {
  assert.equal(tierInsignia(0), '—');
  assert.equal(tierInsignia(undefined), '—');
});

// ===========================================================================
// compactSummaryView — the always-available Quests header button (Issue #334)
// ===========================================================================

test('compactSummaryView: nothing to show yet stays invisible rather than rendering an empty summary', () => {
  assert.deepEqual(compactSummaryView(null), { visible: false, text: '' });
  assert.deepEqual(compactSummaryView({ tiers: [] }), { visible: false, text: '' });
});

test('compactSummaryView: pending — no quest resolved yet', () => {
  const status = {
    current_tier: 1,
    tiers: [tier({ tier: 1, quests: [quest({ status: 'available' }), quest({ id: 'q2' })] })]
  };
  const view = compactSummaryView(status);
  assert.equal(view.visible, true);
  assert.equal(view.allComplete, false);
  assert.equal(view.resolved, 0);
  assert.equal(view.total, 2);
  assert.equal(view.text, 'Tier 1 · 0/2');
});

test('compactSummaryView: partial — some but not all quests resolved', () => {
  const status = {
    current_tier: 2,
    tiers: [
      tier({
        tier: 2,
        quests: [quest({ status: 'completed' }), quest({ id: 'q2', status: 'available' })]
      })
    ]
  };
  const view = compactSummaryView(status);
  assert.equal(view.resolved, 1);
  assert.equal(view.total, 2);
  assert.equal(view.text, 'Tier 2 · 1/2');
});

test('compactSummaryView: a skipped optional quest counts toward resolved, distinguishing it from pending', () => {
  const status = {
    current_tier: 2,
    tiers: [
      tier({
        tier: 2,
        quests: [
          quest({ status: 'completed' }),
          quest({ id: 'hq', status: 'skipped', optional: true })
        ]
      })
    ]
  };
  const view = compactSummaryView(status);
  assert.equal(
    view.resolved,
    2,
    'a skip resolves the quest for summary purposes, same as resolvedCount'
  );
  assert.equal(view.total, 2);
  assert.equal(view.text, 'Tier 2 · 2/2');
});

test('compactSummaryView: tier-complete (but not the last tier) still reads as a tier summary, not all-complete', () => {
  const status = {
    current_tier: 1,
    total_tiers: 3,
    tiers: [tier({ tier: 1, complete: true, quests: [quest({ status: 'completed' })] })]
  };
  const view = compactSummaryView(status);
  assert.equal(view.allComplete, false);
  assert.equal(view.text, 'Tier 1 · 1/1');
});

test('compactSummaryView: all-complete shows one compact congratulatory summary', () => {
  const status = { all_complete: true, total_count: 9, resolved_count: 9, tiers: [] };
  const view = compactSummaryView(status);
  assert.equal(view.visible, true);
  assert.equal(view.allComplete, true);
  assert.equal(view.text, 'All complete');
});

test('compactSummaryView text never resembles the bare-number Updates attention badge', () => {
  const status = { current_tier: 1, tiers: [tier({ tier: 1, quests: [quest()] })] };
  const view = compactSummaryView(status);
  // The Updates badge renders a bare count ("3"); Quests must always carry
  // words alongside its numbers so the two are never visually interchangeable.
  assert.doesNotMatch(view.text, /^\d+$/);
});

test('compactSummaryView is unaffected by missions: it reads the current tier only', () => {
  const status = {
    current_tier: 1,
    missions: starterMissions({}).map(m => ({ ...m, status: 'completed' })),
    tiers: [tier({ tier: 1, quests: [quest({ status: 'available' }), quest({ id: 'q2' })] })]
  };
  assert.equal(compactSummaryView(status).text, 'Tier 1 · 0/2');
});

// ===========================================================================
// The featured card, driven by status.missions (starter missions FR22-FR27)
// ===========================================================================

// The IDs below are fixture data only: the module under test must never know
// them, which the source scan at the end of this section enforces.
function mission(order, overrides = {}) {
  return quest({
    id: `mission-${order}`,
    tier: 1,
    order,
    featured: true,
    optional: true,
    title: `Mission title ${order}`,
    why: `Why ${order}`,
    action_url: `/mission-${order}`,
    action_label: 'Start',
    ...overrides
  });
}

// starterMissions builds the four missions with per-order overrides.
function starterMissions(overrides) {
  return [1, 2, 3, 4].map(order => mission(order, overrides[order] || {}));
}

function missionStatus(overrides = {}, extra = {}) {
  const missions = starterMissions(overrides);
  return {
    current_tier: 1,
    all_complete: false,
    missions,
    tiers: [tier({ tier: 1, name: 'Starter', quests: missions })],
    ...extra
  };
}

test('missionKicker zero-pads the server order', () => {
  assert.equal(missionKicker(2), 'Mission 02');
  assert.equal(missionKicker(12), 'Mission 12');
  assert.equal(missionKicker(0), '');
  assert.equal(missionKicker(undefined), '');
});

test('firstMissionView: Ready shows the first unresolved mission with its own action', () => {
  const view = firstMissionView(missionStatus({ 1: { status: 'completed' } }));
  assert.deepEqual(view, {
    visible: true,
    questID: 'mission-2',
    kicker: 'Mission 02',
    completed: false,
    skipped: false,
    inProgress: false,
    title: 'Mission title 2',
    why: 'Why 2',
    statusLabel: 'Ready',
    actionLabel: 'Start',
    actionURL: '/mission-2',
    showAction: true,
    showSkip: true
  });
});

test('firstMissionView: In progress comes from the server, with its resolved action', () => {
  const view = firstMissionView(
    missionStatus({
      1: { status: 'completed' },
      2: { in_progress: true, action_url: '/workspaces/tidy', action_label: 'Finish setup' }
    })
  );
  assert.equal(view.questID, 'mission-2');
  assert.equal(view.inProgress, true);
  assert.equal(view.statusLabel, 'In progress');
  assert.equal(view.actionLabel, 'Finish setup');
  assert.equal(view.actionURL, '/workspaces/tidy');
  assert.equal(view.showSkip, true);
});

test('firstMissionView: a deferred mission never blocks the next one', () => {
  const view = firstMissionView(
    missionStatus({ 1: { status: 'completed' }, 2: { status: 'skipped' } })
  );
  assert.equal(view.questID, 'mission-3');
  assert.equal(view.kicker, 'Mission 03');
  assert.equal(view.statusLabel, 'Ready');
});

test('firstMissionView: all resolved rests on the last mission, Complete, with no action', () => {
  const view = firstMissionView(
    missionStatus({
      1: { status: 'completed' },
      2: { status: 'skipped' },
      3: { status: 'completed' },
      4: { status: 'completed' }
    })
  );
  assert.equal(view.visible, true);
  assert.equal(view.questID, 'mission-4');
  assert.equal(view.kicker, 'Mission 04');
  assert.equal(view.statusLabel, 'Complete');
  assert.equal(view.completed, true);
  assert.equal(view.showAction, false);
  assert.equal(view.showSkip, false);
});

test('firstMissionView: a deferred last mission stays resumable from the card (Saved for later)', () => {
  const view = firstMissionView(
    missionStatus({
      1: { status: 'completed' },
      2: { status: 'completed' },
      3: { status: 'completed' },
      4: { status: 'skipped' }
    })
  );
  assert.equal(view.questID, 'mission-4');
  assert.equal(view.statusLabel, 'Saved for later');
  assert.equal(view.actionLabel, 'Resume quest');
  assert.equal(view.showAction, true);
  assert.equal(view.showSkip, false, 'an already-deferred mission cannot be deferred again');
});

test('firstMissionView: a completed mission is never in progress', () => {
  const view = firstMissionView(
    missionStatus({
      1: { status: 'completed' },
      2: { status: 'completed', in_progress: true },
      3: { status: 'completed' },
      4: { status: 'completed' }
    })
  );
  assert.equal(view.inProgress, false);
});

test('firstMissionView: a required mission offers no Skip', () => {
  const view = firstMissionView(missionStatus({ 1: { optional: false } }));
  assert.equal(view.questID, 'mission-1');
  assert.equal(view.showSkip, false);
});

test('firstMissionView: order comes from the server, not from list position', () => {
  const status = missionStatus();
  status.missions = [mission(3), mission(1, { status: 'completed' })];
  // The server sends missions sorted; the view trusts that and reads order.
  const view = firstMissionView(status);
  assert.equal(view.questID, 'mission-3');
  assert.equal(view.kicker, 'Mission 03');
});

test('firstMissionView hides without missions or once all progression is complete', () => {
  assert.deepEqual(firstMissionView(null), { visible: false });
  assert.deepEqual(firstMissionView({ tiers: [tier({ quests: [quest()] })] }), {
    visible: false
  });
  assert.deepEqual(firstMissionView(missionStatus({}, { missions: [] })), { visible: false });
  assert.deepEqual(firstMissionView(missionStatus({}, { all_complete: true })), {
    visible: false
  });
});

test('tierQuestRows omits only the mission on the card', () => {
  const status = missionStatus({ 1: { status: 'completed' } });
  const shown = firstMissionView(status).questID;
  const rows = tierQuestRows(status.tiers[0], shown).map(row => row.id);
  assert.deepEqual(rows, ['mission-1', 'mission-3', 'mission-4']);
});

test('tierQuestRows keeps every row when no mission is shown', () => {
  const t = tier({ quests: [quest({ id: 'a' }), quest({ id: 'b' })] });
  assert.deepEqual(
    tierQuestRows(t, '').map(row => row.id),
    ['a', 'b']
  );
  assert.deepEqual(
    tierQuestRows(t).map(row => row.id),
    ['a', 'b']
  );
});

test('checklist rows keep their Skip and Resume affordances beside the card', () => {
  const status = missionStatus({ 1: { status: 'skipped' }, 2: { status: 'completed' } });
  const shown = firstMissionView(status).questID;
  assert.equal(shown, 'mission-3');
  const rows = tierQuestRows(status.tiers[0], shown);
  const states = Object.fromEntries(rows.map(row => [row.id, questRowState(row)]));
  assert.equal(states['mission-1'].showResume, true, 'a deferred mission stays reachable');
  assert.equal(states['mission-2'].mark, '✓');
  assert.equal(states['mission-4'].showSkip, true);
});

test('progression-widget.js contains no quest ID literals', () => {
  const source = readFileSync(new URL('./progression-widget.js', import.meta.url), 'utf8');
  for (const pattern of [/['"`]t\d-[a-z-]+['"`]/, /['"`]pa-[a-z-]+['"`]/]) {
    assert.doesNotMatch(source, pattern);
  }
  assert.doesNotMatch(source, /FIRST_MISSION_QUEST_ID|featuredMissionIDs/);
});

test('questRowState: an available optional quest shows the Skip control and a link', () => {
  const state = questRowState(
    quest({ status: 'available', optional: true, action_url: '/workspaces?hq=1' })
  );
  assert.equal(state.done, false);
  assert.equal(state.skipped, false);
  assert.equal(state.resolved, false);
  assert.equal(state.mark, '○');
  assert.equal(state.showLink, true);
  assert.equal(state.showSkip, true);
  assert.equal(state.showResume, false);
});

test('questRowState: a non-optional available quest never shows Skip', () => {
  const state = questRowState(quest({ status: 'available', optional: false }));
  assert.equal(state.showSkip, false);
});

test('questRowState: a skipped quest is not "done", offers Resume instead of a title link, and never Skip again', () => {
  const state = questRowState(
    quest({
      status: 'skipped',
      optional: true,
      action_url: '/workspaces?hq=1',
      action_label: 'Build My HQ'
    })
  );
  assert.equal(state.done, false, 'a skip must never be labeled as the action being completed');
  assert.equal(state.skipped, true);
  assert.equal(state.resolved, true);
  assert.equal(state.mark, '⏭');
  assert.equal(
    state.showLink,
    false,
    'the quest title itself is no longer the actionable link once skipped'
  );
  assert.equal(state.showResume, true);
  assert.equal(state.showSkip, false, 'an already-skipped quest cannot be skipped again');
});

test('questRowState: a skipped quest with no action_url shows no Resume link', () => {
  const state = questRowState(quest({ status: 'skipped', optional: true }));
  assert.equal(state.showResume, false);
});

test('questRowState: a completed quest shows a checkmark and no link/skip/resume', () => {
  const state = questRowState(quest({ status: 'completed', optional: true, action_url: '/x' }));
  assert.equal(state.done, true);
  assert.equal(state.mark, '✓');
  assert.equal(state.showLink, false);
  assert.equal(state.showSkip, false);
  assert.equal(state.showResume, false);
});

test('questRowState: a locked-tier quest renders like any other unresolved quest (still linkable once available)', () => {
  const state = questRowState(quest({ status: 'locked-tier', action_url: '/x' }));
  assert.equal(state.resolved, false);
  assert.equal(state.showLink, true);
});

// Missions 02-05 wait on Mission 01 (meet-your-assistant FR7). The server says
// so; the row shows it and offers nothing to press.
test('questRowState: a server-locked mission shows its lock and reason, and no action', () => {
  const state = questRowState(
    quest({
      status: 'available',
      optional: true,
      action_url: '/?quest=build-hq',
      locked: true,
      locked_reason: 'Meet your assistant first'
    })
  );
  assert.equal(state.locked, true);
  assert.equal(state.lockedReason, 'Meet your assistant first');
  assert.equal(state.mark, '🔒');
  assert.equal(state.showLink, false);
  assert.equal(state.showSkip, false);
  assert.equal(state.showResume, false);
});

test('questRowState: a resolved mission is never shown locked', () => {
  for (const status of ['completed', 'skipped']) {
    const state = questRowState(
      quest({ status, optional: true, action_url: '/x', locked: true, locked_reason: 'Wait' })
    );
    assert.equal(state.locked, false, status);
    assert.equal(state.lockedReason, '');
  }
});

// A minimal document: enough of createElement for renderQuestRow.
function stubDocument() {
  const make = tag => ({
    tag,
    className: '',
    textContent: '',
    attributes: {},
    children: [],
    listeners: {},
    setAttribute(k, v) {
      this.attributes[k] = v;
    },
    addEventListener(type, fn) {
      this.listeners[type] = fn;
    },
    append(...nodes) {
      this.children.push(...nodes);
    }
  });
  return { createElement: make };
}

test('renderQuestRow: a locked row reads as locked, with no link, reward, Skip, or Resume', () => {
  const li = renderQuestRow(
    quest({
      title: 'Build My HQ',
      status: 'available',
      optional: true,
      action_url: '/?quest=build-hq',
      reward_craft: 5,
      locked: true,
      locked_reason: 'Meet your assistant first'
    }),
    { doc: stubDocument() }
  );
  assert.match(li.className, /quest-item-locked/);
  const [mark, title, reason, ...rest] = li.children;
  assert.equal(mark.textContent, '🔒');
  assert.equal(title.tag, 'span', 'a locked title must not be a link');
  assert.equal(title.textContent, 'Build My HQ');
  assert.equal(reason.className, 'quest-status quest-status-locked');
  assert.equal(reason.textContent, 'Meet your assistant first');
  assert.deepEqual(rest, [], 'a locked row offered something to press');
});

test('renderQuestRow: an open optional row keeps its link, reward, and Skip', () => {
  const skipped = [];
  const li = renderQuestRow(
    quest({
      id: 'q-open',
      title: 'Show your assistant a folder',
      status: 'available',
      optional: true,
      action_url: '/?quest=show-folder',
      reward_craft: 5
    }),
    { doc: stubDocument(), onSkip: id => skipped.push(id) }
  );
  const [, title, reward, skip] = li.children;
  assert.equal(title.tag, 'a');
  assert.equal(title.href, '/?quest=show-folder');
  assert.equal(reward.textContent, '+5 Craft');
  assert.equal(skip.textContent, 'Skip');
  skip.listeners.click();
  assert.deepEqual(skipped, ['q-open']);
});

// The folder mission's card carries the assistant's pending offer, with the
// chooser's own copy and buttons (FR21). Other missions never show it.
const folderMission = {
  id: 'pa-show-folder',
  order: 3,
  title: 'Show your assistant a folder',
  status: 'available',
  optional: true,
  action_url: '/?quest=show-folder',
  action_label: 'Start'
};
const pendingProjectOffer = {
  id: 'offer-1',
  status: 'pending',
  verdict: 'project',
  folder: 'Documents',
  subject: { name: 'Thesis' },
  reason: '14 LaTeX files, edited yesterday',
  remember: true
};

test('firstMissionOfferView: the folder mission shows a pending offer inline', () => {
  const view = firstMissionView({ missions: [folderMission] });
  const offer = firstMissionOfferView(view, pendingProjectOffer);
  assert.equal(offer.visible, true);
  assert.equal(offer.verdict, 'project');
  assert.equal(offer.headline.map(part => part.text).join(''), 'You have been working in Thesis.');
  assert.match(offer.question, /set up a workspace/);
  assert.equal(offer.reason, '14 LaTeX files, edited yesterday');
  assert.deepEqual(
    offer.actions.map(action => action.id),
    ['yes', 'no', 'later']
  );
  assert.equal(offer.note, '');
});

test('firstMissionOfferView: a decided offer keeps its note and drops the buttons', () => {
  const view = firstMissionView({ missions: [folderMission] });
  const offer = firstMissionOfferView(view, {
    ...pendingProjectOffer,
    status: 'later',
    outcome: {}
  });
  assert.equal(offer.visible, true);
  assert.equal(offer.decided, true);
  assert.deepEqual(offer.actions, []);
  assert.ok(offer.note, 'a decided offer explains what happened');
});

test('firstMissionOfferView: hidden without an offer, on other missions, and once resolved', () => {
  const view = firstMissionView({ missions: [folderMission] });
  assert.equal(firstMissionOfferView(view, null).visible, false, 'no offer');
  assert.equal(
    firstMissionOfferView(view, { verdict: 'unknown' }).visible,
    false,
    'unknown verdict'
  );

  const other = firstMissionView({
    missions: [{ ...folderMission, id: 't2-build-hq', action_url: '/?quest=build-hq' }]
  });
  assert.equal(firstMissionOfferView(other, pendingProjectOffer).visible, false, 'other mission');

  for (const status of ['completed', 'skipped']) {
    const resolved = firstMissionView({ missions: [{ ...folderMission, status }] });
    assert.equal(firstMissionOfferView(resolved, pendingProjectOffer).visible, false, status);
  }
  assert.equal(firstMissionOfferView({ visible: false }, pendingProjectOffer).visible, false);
});

test('diffAnnouncements is silent on the very first load (knownCompleted === null)', () => {
  const status = {
    tiers: [tier({ quests: [quest({ id: 'a', status: 'completed' })] })]
  };
  const diff = diffAnnouncements(status, null, {});
  assert.deepEqual(diff.newCompletions, []);
  assert.deepEqual(diff.newTierCompletions, []);
  assert.equal(
    diff.completedNow.has('a'),
    true,
    'the baseline must still be recorded for next time'
  );
});

test('diffAnnouncements reports a newly completed quest', () => {
  const status = {
    tiers: [tier({ quests: [quest({ id: 'a', status: 'completed', title: 'First quest' })] })]
  };
  const diff = diffAnnouncements(status, new Set(), {});
  assert.deepEqual(diff.newCompletions, [{ id: 'a', title: 'First quest' }]);
});

test('diffAnnouncements never reports a skip as a completion', () => {
  const status = {
    tiers: [tier({ quests: [quest({ id: 'a', status: 'skipped' })] })]
  };
  const diff = diffAnnouncements(status, new Set(), {});
  assert.deepEqual(diff.newCompletions, [], 'skip must never toast as a quest completion');
});

test('diffAnnouncements never toasts a skipped starter mission', () => {
  const status = missionStatus({ 1: { status: 'completed' }, 2: { status: 'skipped' } });
  const diff = diffAnnouncements(status, new Set(['mission-1']), { 1: false });
  assert.deepEqual(diff.newCompletions, []);
  assert.deepEqual(diff.newTierCompletions, []);
});

test('diffAnnouncements reports a tier-complete transition when a real completion drove it', () => {
  const status = {
    tiers: [
      tier({
        tier: 1,
        complete: true,
        quests: [quest({ id: 'a', status: 'completed', title: 'Last quest' })]
      })
    ]
  };
  const diff = diffAnnouncements(status, new Set(), { 1: false });
  assert.deepEqual(diff.newTierCompletions, [{ tier: 1, name: 'Establish a Base' }]);
});

test('diffAnnouncements suppresses a tier-complete transition caused solely by a skip (task 3.8)', () => {
  const status = {
    tiers: [
      tier({
        tier: 2,
        complete: true,
        // Every quest in this tier is already known-completed except the
        // optional one, which just got skipped this round — nothing in this
        // diff is a *new* completion.
        quests: [
          quest({ id: 'a', status: 'completed' }),
          quest({ id: 'hq', status: 'skipped', optional: true })
        ]
      })
    ]
  };
  const diff = diffAnnouncements(status, new Set(['a']), { 2: false });
  assert.deepEqual(diff.newCompletions, []);
  assert.deepEqual(
    diff.newTierCompletions,
    [],
    'a tier resolved solely by a skip must not toast as tier-complete'
  );
});

test('diffAnnouncements still toasts tier-complete when a skip and a real completion land in the same round', () => {
  const status = {
    tiers: [
      tier({
        tier: 2,
        complete: true,
        quests: [
          quest({ id: 'a', status: 'completed', title: 'Real one' }),
          quest({ id: 'hq', status: 'skipped', optional: true })
        ]
      })
    ]
  };
  const diff = diffAnnouncements(status, new Set(), { 2: false });
  assert.deepEqual(diff.newCompletions, [{ id: 'a', title: 'Real one' }]);
  assert.deepEqual(
    diff.newTierCompletions,
    [{ tier: 2, name: 'Establish a Base' }],
    'a real completion in the same round still earns the tier-complete toast'
  );
});

test('diffAnnouncements does not re-toast a tier that was already known complete', () => {
  const status = {
    tiers: [tier({ tier: 1, complete: true, quests: [quest({ id: 'a', status: 'completed' })] })]
  };
  const diff = diffAnnouncements(status, new Set(['a']), { 1: true });
  assert.deepEqual(diff.newTierCompletions, []);
});
