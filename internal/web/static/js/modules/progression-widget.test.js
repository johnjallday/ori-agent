import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  currentTier,
  completedCount,
  resolvedCount,
  tierInsignia,
  compactSummaryView,
  firstMissionView,
  tierQuestRows,
  questRowState,
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
  assert.equal(view.resolved, 2, 'a skip resolves the quest for summary purposes, same as resolvedCount');
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

test('firstMissionView exposes the HQ quest before its tier unlocks', () => {
  const status = {
    current_tier: 1,
    tiers: [
      tier({ tier: 1, quests: [quest({ id: 'hello' })] }),
      tier({
        tier: 2,
        quests: [
          quest({
            id: 't2-build-hq',
            title: 'Build My HQ',
            why: 'Give Ori a home base.',
            status: 'locked-tier',
            action_url: '/workspaces?view=map&focus=personal-hq',
            action_label: 'Build My HQ'
          })
        ]
      })
    ]
  };

  assert.deepEqual(firstMissionView(status), {
    visible: true,
    completed: false,
    skipped: false,
    title: 'Build My HQ',
    why: 'Give Ori a home base.',
    statusLabel: 'Ready',
    actionLabel: 'Build My HQ',
    actionURL: '/workspaces?view=map&focus=personal-hq',
    showAction: true
  });
});

test('firstMissionView turns a skipped HQ quest into a resumable mission', () => {
  const status = {
    tiers: [
      tier({
        quests: [
          quest({
            id: 't2-build-hq',
            status: 'skipped',
            action_url: '/workspaces?view=map&focus=personal-hq',
            action_label: 'Build My HQ'
          })
        ]
      })
    ]
  };

  const view = firstMissionView(status);
  assert.equal(view.statusLabel, 'Not set up');
  assert.equal(view.actionLabel, 'Build My HQ');
  assert.equal(view.showAction, true);
});

test('firstMissionView keeps completion visible without a redundant action', () => {
  const status = {
    tiers: [tier({ quests: [quest({ id: 't2-build-hq', status: 'completed' })] })]
  };

  const view = firstMissionView(status);
  assert.equal(view.visible, true);
  assert.equal(view.statusLabel, 'Complete');
  assert.equal(view.showAction, false);
});

test('firstMissionView hides once all progression is complete', () => {
  const status = {
    all_complete: true,
    tiers: [tier({ quests: [quest({ id: 't2-build-hq', status: 'completed' })] })]
  };
  assert.deepEqual(firstMissionView(status), { visible: false });
});

test('tierQuestRows omits the HQ quest because Mission 01 renders it separately', () => {
  const rows = tierQuestRows(
    tier({
      quests: [quest({ id: 'workspace' }), quest({ id: 't2-build-hq' }), quest({ id: 'note' })]
    })
  );
  assert.deepEqual(
    rows.map(item => item.id),
    ['workspace', 'note']
  );
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
