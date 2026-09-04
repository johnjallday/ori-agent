import test from 'node:test';
import assert from 'node:assert/strict';

import {
  personalAssistantLauncherCue,
  personalAssistantTodayView,
  safeTodayRoute,
  specialistOfferIsOpen,
  specialistOfferView,
  studioSectionView,
  todaySectionRows
} from './personal-assistant-home.js';

const musicEntry = Object.freeze({
  slug: 'music_production',
  display_name: 'music projects',
  offer_copy: {
    headline: 'I found REAPER on this Mac.',
    question: 'Want me to help with your music projects?',
    accept_label: 'Yes, help with my music',
    decline_label: 'No thanks',
    accepted_note: 'Your assistant will keep an eye on your music projects.',
    manual_label: 'I work on music'
  }
});

test('Today view distinguishes active, paused, partial, no-model, empty, and fatal states', () => {
  assert.equal(personalAssistantTodayView({ state: 'active' }).active, true);
  assert.equal(personalAssistantTodayView({ state: 'paused' }).paused, true);
  assert.equal(personalAssistantTodayView({ state: 'partial' }).partial, true);
  assert.equal(personalAssistantTodayView({ state: 'model_unavailable' }).modelUnavailable, true);
  assert.equal(personalAssistantTodayView({ state: 'healthy_empty' }).active, true);
  assert.equal(personalAssistantTodayView({ state: 'unavailable' }).unavailable, true);
});

test('Today distinguishes a hired assistant with no HQ from needs_hire and does not claim active/paused', () => {
  const view = personalAssistantTodayView({ state: 'needs_hq', display_name: 'Atlas' });
  assert.equal(view.needsHQ, true);
  assert.equal(view.needsHire, false);
  assert.equal(view.active, false);
  assert.equal(view.paused, false);
  assert.equal(view.partial, false);
  assert.equal(view.displayName, 'Atlas');
});

test('launcher cues are textual, bounded, and derived only from canonical states', () => {
  assert.equal(personalAssistantLauncherCue({ state: 'active' }, null), 'Loading Today');
  assert.equal(
    personalAssistantLauncherCue({ state: 'active' }, { state: 'healthy_empty' }),
    'Today ready'
  );
  assert.equal(personalAssistantLauncherCue({ state: 'paused' }, { state: 'active' }), 'Paused');
  assert.equal(personalAssistantLauncherCue({ state: 'needs_hq' }, null), 'Build HQ');
  assert.equal(
    personalAssistantLauncherCue({ state: 'active' }, { state: 'partial' }),
    'Sources unavailable'
  );
  assert.equal(
    personalAssistantLauncherCue({ state: 'active' }, { state: 'unavailable' }),
    'Sources unavailable'
  );
  assert.equal(
    personalAssistantLauncherCue({ state: 'active' }, { state: 'model_unavailable' }),
    'Model unavailable'
  );
  assert.equal(personalAssistantLauncherCue({ state: 'repair_needed' }, null), 'Repair needed');
  assert.equal(personalAssistantLauncherCue(null, null), '');
});

test('Today section never turns unavailable into a healthy empty all-clear', () => {
  assert.match(
    todaySectionRows({ health: { status: 'unavailable' }, items: [] })[0].title,
    /unavailable/
  );
  assert.match(
    todaySectionRows({ health: { status: 'healthy_empty' }, items: [] })[0].title,
    /Nothing here/
  );
});

test('Today section caps records and only preserves server-owned internal routes', () => {
  const items = Array.from({ length: 15 }, (_, index) => ({
    kind: 'ticket',
    title: `Ticket ${index}`,
    route: `/workspaces/personal-hq?ticket=${index}`
  }));
  const rows = todaySectionRows({ health: { status: 'available' }, items });
  assert.equal(rows.length, 10);
  assert.ok(rows.every(row => safeTodayRoute(row.route)));

  for (const route of [
    'https://evil.example',
    '//evil.example',
    'javascript:alert(1)',
    'workspaces/x'
  ]) {
    assert.equal(safeTodayRoute(route), false, route);
  }
});

test('Today rows carry who did the work, by name, only when the record says so', () => {
  const rows = todaySectionRows({
    health: { status: 'available' },
    items: [
      { title: 'Rough mix of Ivory', attribution: 'Reaper Producer', route: '/workspaces/ivory' },
      { title: 'Archived the stems', route: '/workspaces/ivory' }
    ]
  });
  assert.equal(rows[0].attribution, 'Reaper Producer');
  assert.equal(rows[1].attribution, '');
});

test('the studio section is absent unless the server reports one', () => {
  for (const studio of [null, undefined]) {
    assert.equal(studioSectionView(studio).visible, false);
  }
});

test('the studio section reports and points at the specialist without claiming to direct it', () => {
  const view = studioSectionView({
    health: { status: 'available' },
    domain: 'music projects',
    specialist_name: 'Reaper Producer',
    workspace_name: 'Ivory',
    route: '/workspaces/ivory',
    items: [{ title: 'Rough mix', attribution: 'Reaper Producer' }]
  });

  assert.equal(view.visible, true);
  assert.equal(view.heading, 'From Ivory');
  assert.equal(view.route, '/workspaces/ivory');
  assert.equal(view.linkLabel, 'Open Ivory');
  // Addressing the specialist directly is offered plainly, and nothing claims
  // the assistant can hand it work — it cannot.
  assert.match(view.note, /ask Reaper Producer directly/i);
  assert.doesNotMatch(view.note, /assign|delegate|instruct|hand off|on your behalf/i);
  assert.doesNotMatch(view.note, /instead|workaround|advanced|fall ?back/i);
  assert.equal(view.section.items.length, 1);
});

test('the studio section degrades safely without a name, a workspace, or a usable route', () => {
  const noRoute = studioSectionView({
    health: { status: 'unavailable' },
    specialist_name: 'Reaper Producer',
    route: 'https://evil.example',
    items: []
  });
  assert.equal(noRoute.route, '');
  assert.match(noRoute.note, /works in its own workspace/i);

  const bare = studioSectionView({ health: { status: 'available' }, items: [] });
  assert.equal(bare.visible, true);
  assert.equal(bare.heading, 'From your studio');
  assert.equal(bare.linkLabel, 'Open the workspace');
  assert.match(bare.note, /your specialist/i);
});

// --- The post-hire domain offer ----------------------------------------

test('the offer states what was found rather than asking about it', () => {
  const view = specialistOfferView(musicEntry);
  assert.equal(view.visible, true);
  assert.equal(view.decision, 'unanswered');
  assert.equal(view.showActions, true);
  assert.equal(view.headline, 'I found REAPER on this Mac.');
  assert.equal(view.question, 'Want me to help with your music projects?');
  // The install is already known; the copy must never put it to the user as a
  // question of fact.
  assert.doesNotMatch(view.question, /do you use|are you a/i);
});

test('a declined offer never comes back', () => {
  const declined = specialistOfferView(musicEntry, 'declined');
  assert.equal(declined.visible, false);
  assert.equal(declined.showActions, false);
});

test('accepting is confirmed without implying the assistant can direct the specialist', () => {
  const accepted = specialistOfferView(musicEntry, 'accepted');
  assert.equal(accepted.visible, true);
  assert.equal(accepted.decision, 'accepted');
  assert.equal(accepted.showActions, false);
  assert.equal(accepted.acceptedNote, 'Your assistant will keep an eye on your music projects.');
  assert.doesNotMatch(accepted.acceptedNote, /delegate|assign|on your behalf/i);
});

test('nothing renders when no specialist matched', () => {
  for (const entry of [null, undefined, {}, { slug: '' }]) {
    const view = specialistOfferView(entry);
    assert.equal(view.visible, false, `expected no offer for ${JSON.stringify(entry)}`);
    assert.equal(view.showActions, false);
  }
});

// The offer waits for a settled relationship. Before a hire there is nothing
// to shape; between the hire and the built HQ, Home is running its guided HQ
// walkthrough and a second call to action there competes with the user's
// actual next step.
test('the offer is only open for a settled relationship that has not answered', () => {
  for (const relationshipState of ['active', 'paused']) {
    assert.equal(
      specialistOfferIsOpen({ state: relationshipState }),
      true,
      `expected ${relationshipState} to be offered`
    );
  }
  for (const relationshipState of [
    'needs_hire',
    'hiring',
    'needs_hq',
    'provisioning_hq',
    'repair_needed',
    '',
    undefined
  ]) {
    assert.equal(
      specialistOfferIsOpen({ state: relationshipState }),
      false,
      `expected ${relationshipState} not to be offered`
    );
  }
  assert.equal(specialistOfferIsOpen(null), false);

  // An answer is durable: it is never asked again after a reload.
  for (const answered of ['accepted', 'declined']) {
    assert.equal(
      specialistOfferIsOpen({ state: 'active', specialist_offer_state: answered }),
      false,
      `expected ${answered} not to re-ask`
    );
  }
});
