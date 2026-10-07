import test from 'node:test';
import assert from 'node:assert/strict';
import {
  HomeProfilePanel,
  READ_ONLY_NOTE,
  appRowView,
  appsView,
  defaultsInput,
  defaultsView,
  detectView,
  joinNames,
  mainAppView,
  profileDate,
  profileStatus,
  profileView,
  sampleRateLabel,
  templatesView
} from './home-profile.js';

const DATE = { locale: 'en-US', timeZone: 'UTC' };

const CHOICES = {
  min_tempo: 40,
  max_tempo: 240,
  time_signatures: [
    { value: '4 4', label: '4/4' },
    { value: '3 4', label: '3/4' }
  ],
  sample_rates: [44100, 48000, 96000],
  bit_depths: [16, 24, 32]
};

test('new-project defaults show Not set until the owner saves some', () => {
  const view = defaultsView({ available: true, choices: CHOICES, profile: null }, DATE);
  assert.equal(view.summary, 'Not set');
  assert.equal(
    view.note,
    'Not set. A new project starts from its own defaults until you save some here.'
  );
  assert.equal(view.disabled, false);
  assert.deepEqual(view.tempo, { value: '', min: 40, max: 240, placeholder: 'Not set' });
  assert.deepEqual(view.timeSignature.options, [
    { value: '', label: 'Not set' },
    { value: '4 4', label: '4/4' },
    { value: '3 4', label: '3/4' }
  ]);
  assert.deepEqual(
    view.sampleRate.options.map(option => option.label),
    ['Not set', '44.1 kHz', '48 kHz', '96 kHz']
  );
  assert.deepEqual(
    view.bitDepth.options.map(option => option.label),
    ['Not set', '16-bit', '24-bit', '32-bit']
  );
  assert.equal(sampleRateLabel(88200), '88.2 kHz');
});

test('saved defaults fill the row and say they are the owner’s', () => {
  const view = defaultsView(
    {
      available: true,
      choices: CHOICES,
      profile: {
        defaults: {
          tempo_bpm: 120,
          time_signature: '4 4',
          sample_rate_hz: 48000,
          bit_depth: 24,
          source: 'owner',
          confirmed_at: '2026-10-08T12:00:00Z'
        }
      }
    },
    DATE
  );
  assert.equal(view.tempo.value, '120');
  assert.equal(view.timeSignature.value, '4 4');
  assert.equal(view.sampleRate.value, '48000');
  assert.equal(view.bitDepth.value, '24');
  assert.equal(view.summary, '120 BPM, 4/4, 48 kHz, 24-bit');
  assert.equal(view.note, '120 BPM, 4/4, 48 kHz, 24-bit. Set by you Oct 8, 2026.');
  // A part that was never set stays Not set beside the ones that were.
  const partial = defaultsView(
    {
      available: true,
      choices: CHOICES,
      profile: {
        defaults: { tempo_bpm: 92, source: 'owner', confirmed_at: '2026-10-08T12:00:00Z' }
      }
    },
    DATE
  );
  assert.equal(partial.summary, '92 BPM');
  assert.equal(partial.timeSignature.value, '');
  assert.equal(partial.sampleRate.value, '');
});

test('a time signature cannot be chosen while there are no choices, and a read-only Home disables the row', () => {
  const none = defaultsView({ available: true, choices: { ...CHOICES, time_signatures: [] } });
  assert.equal(none.timeSignature.disabled, true);
  assert.deepEqual(none.timeSignature.options, [{ value: '', label: 'Not set' }]);
  const readOnly = defaultsView({ available: true, read_only: true, choices: CHOICES });
  assert.equal(readOnly.disabled, true);
  assert.equal(readOnly.timeSignature.disabled, true);
});

test('defaultsInput sends only the parts that are set and refuses a tempo out of bounds', () => {
  assert.deepEqual(
    defaultsInput(
      { tempo: ' 96 ', timeSignature: '3 4', sampleRate: '48000', bitDepth: '24' },
      CHOICES
    ),
    { input: { tempo_bpm: 96, time_signature: '3 4', sample_rate_hz: 48000, bit_depth: 24 } }
  );
  // Everything empty is a valid save: it clears the defaults.
  assert.deepEqual(
    defaultsInput({ tempo: '', timeSignature: '', sampleRate: '', bitDepth: '' }, CHOICES),
    {
      input: {}
    }
  );
  assert.deepEqual(defaultsInput({ tempo: '120' }, CHOICES), { input: { tempo_bpm: 120 } });
  for (const tempo of ['39', '241', '92.5', 'fast']) {
    assert.deepEqual(defaultsInput({ tempo }, CHOICES), {
      error: 'Tempo is a whole number from 40 to 240 BPM.'
    });
  }
});

const FIELDS = [
  { id: 'apps', kind: 'apps', label: 'DAWs on this Mac' },
  { id: 'main_app', kind: 'main_app', label: 'Main DAW' },
  { id: 'templates', kind: 'templates', label: 'Project templates' },
  { id: 'defaults', kind: 'defaults', label: 'New-song defaults' }
];

function card(profile = null, extra = {}) {
  return {
    available: true,
    read_only: false,
    title: 'Your studio',
    intro: 'What Ori knows about where you make music.',
    fields: FIELDS,
    revision: profile?.revision || 0,
    profile,
    ...extra
  };
}

const reaper = {
  id: 'reaper',
  name: 'REAPER',
  detected: true,
  detected_at: '2026-10-07T09:00:00Z'
};
const logic = {
  id: 'logic-pro',
  name: 'Logic Pro',
  detected: true,
  detected_at: '2026-10-07T09:00:00Z'
};

function profile(extra = {}) {
  return {
    schema_version: 1,
    revision: 1,
    detected_at: '2026-10-07T09:00:00Z',
    apps: [reaper, logic],
    ...extra
  };
}

test('joinNames and profileDate word lists and dates', () => {
  assert.equal(joinNames([]), '');
  assert.equal(joinNames(['REAPER']), 'REAPER');
  assert.equal(joinNames(['REAPER', 'Logic Pro']), 'REAPER and Logic Pro');
  assert.equal(joinNames(['A', ' ', 'B', 'C']), 'A, B and C');
  assert.equal(profileDate('2026-10-07T09:00:00Z', 'en-US', 'UTC'), 'Oct 7, 2026');
  assert.equal(profileDate(''), '');
  assert.equal(profileDate('not a date'), '');
});

test('the card is hidden unless the package declares a profile', () => {
  assert.deepEqual(profileView(null), { visible: false, rows: [] });
  assert.deepEqual(profileView({ available: false }), { visible: false, rows: [] });
  assert.equal(profileView(card(null, { fields: [] })).visible, false);
});

test('the card shows the declared title, intro and rows in declared order', () => {
  const view = profileView(card());
  assert.equal(view.visible, true);
  assert.equal(view.title, 'Your studio');
  assert.equal(view.intro, 'What Ori knows about where you make music.');
  assert.deepEqual(
    view.rows.map(row => [row.kind, row.label]),
    [
      ['apps', 'DAWs on this Mac'],
      ['main_app', 'Main DAW'],
      ['templates', 'Project templates'],
      ['defaults', 'New-song defaults']
    ]
  );
  assert.equal(view.readOnly, false);
  assert.equal(view.readOnlyNote, '');
  // A package may omit a kind, reorder them, or (in a later release) declare
  // one this build does not know: the card then has no such row.
  const fewer = profileView(
    card(null, {
      fields: [FIELDS[1], { id: 'gear', kind: 'hardware', label: 'Hardware' }, FIELDS[0]]
    })
  );
  assert.deepEqual(
    fewer.rows.map(row => row.kind),
    ['main_app', 'apps']
  );
});

test('Detect says Detect until the first look and Detect again after', () => {
  assert.deepEqual(detectView(card()), { visible: true, label: 'Detect', disabled: false });
  assert.deepEqual(detectView(card(profile())), {
    visible: true,
    label: 'Detect again',
    disabled: false
  });
  assert.equal(detectView(card(profile(), { read_only: true })).disabled, true);
  // A card with only templates and defaults has nothing to detect.
  assert.equal(detectView(card(null, { fields: FIELDS.slice(2) })).visible, false);
});

test('a read-only Home says so and disables every control', () => {
  const view = card(
    profile({ main_app: { id: 'reaper', source: 'detected', reason: 'only_app' } }),
    { read_only: true }
  );
  assert.equal(profileView(view).readOnlyNote, READ_ONLY_NOTE);
  assert.equal(profileView(view).detect.disabled, true);
  const apps = appsView(view, DATE);
  assert.ok(apps.rows.every(row => row.disabled));
  const main = mainAppView(view, DATE);
  assert.equal(main.disabled, true);
  assert.equal(main.canConfirm, false);
  assert.equal(main.value, 'reaper');
});

test('a detected application is a hint and a confirmed one shows its date', () => {
  assert.deepEqual(appRowView(reaper, DATE), {
    id: 'reaper',
    name: 'REAPER',
    version: '',
    badge: 'Detected',
    badgeKind: 'detected',
    detail: 'Found on this Mac. A hint until you confirm it.',
    canConfirm: true,
    canHide: true,
    disabled: false
  });
  const confirmed = appRowView(
    { ...reaper, version: '7.28', confirmed_at: '2026-10-08T12:00:00Z' },
    DATE
  );
  assert.equal(confirmed.badge, 'Confirmed');
  assert.equal(confirmed.badgeKind, 'confirmed');
  assert.equal(confirmed.version, '7.28');
  assert.equal(confirmed.detail, 'Confirmed Oct 8, 2026.');
  assert.equal(confirmed.canConfirm, false);
  // An application that was confirmed and later removed is not shown as found.
  assert.equal(
    appRowView({ ...reaper, detected: false, confirmed_at: '2026-10-08T12:00:00Z' }, DATE).detail,
    'Confirmed Oct 8, 2026. Not found on this Mac now.'
  );
  assert.equal(
    appRowView({ ...reaper, detected: false }, DATE).detail,
    'Not found on this Mac now.'
  );
});

test('the applications row words each empty state by what happens next', () => {
  const never = appsView(card());
  assert.deepEqual(never.rows, []);
  assert.match(
    never.empty,
    /^Nothing has been looked for yet\. Detect checks the Applications folders/
  );
  assert.equal(appsView(card(null, { read_only: true })).empty, 'Nothing has been looked for yet.');

  const none = appsView(card(profile({ apps: [] })));
  assert.equal(none.empty, 'None was found on this Mac. Detect again after you install one.');

  const listed = appsView(card(profile()), DATE);
  assert.deepEqual(
    listed.rows.map(row => row.name),
    ['REAPER', 'Logic Pro']
  );
  assert.equal(listed.empty, '');
  assert.deepEqual(listed.hidden, []);
});

test('an application the owner hid is listed apart and never as a row', () => {
  const view = appsView(card(profile({ apps: [reaper, { ...logic, hidden: true }] })), DATE);
  assert.deepEqual(
    view.rows.map(row => row.id),
    ['reaper']
  );
  assert.deepEqual(view.hidden, [{ id: 'logic-pro', name: 'Logic Pro', disabled: false }]);
  const onlyHidden = appsView(card(profile({ apps: [{ ...logic, hidden: true }] })));
  assert.equal(onlyHidden.empty, 'Nothing else was found on this Mac.');
});

test('the main application asks for one pick when several were found', () => {
  const view = mainAppView(card(profile()), DATE);
  assert.deepEqual(view.options, [
    { id: 'reaper', name: 'REAPER' },
    { id: 'logic-pro', name: 'Logic Pro' }
  ]);
  assert.equal(view.value, '');
  assert.equal(view.badge, '');
  assert.equal(view.note, 'Not chosen. REAPER and Logic Pro were found. Pick one.');
  assert.equal(view.disabled, false);
  assert.equal(view.canConfirm, false);
});

test('the main application says where its value came from', () => {
  const only = mainAppView(
    card(
      profile({
        apps: [reaper],
        main_app: { id: 'reaper', source: 'detected', reason: 'only_app' }
      })
    ),
    DATE
  );
  assert.equal(only.value, 'reaper');
  assert.equal(only.badge, 'Detected');
  assert.equal(only.note, 'REAPER is the only one found on this Mac. A hint until you confirm it.');
  assert.equal(only.canConfirm, true);

  const library = mainAppView(
    card(profile({ main_app: { id: 'reaper', source: 'detected', reason: 'library_majority' } })),
    DATE
  );
  assert.equal(
    library.note,
    'Most of the projects in your library are REAPER projects. A hint until you confirm it.'
  );

  const confirmed = mainAppView(
    card(
      profile({
        main_app: {
          id: 'reaper',
          source: 'detected',
          reason: 'only_app',
          confirmed_at: '2026-10-08T12:00:00Z'
        }
      })
    ),
    DATE
  );
  assert.equal(confirmed.badge, 'Confirmed');
  assert.equal(confirmed.badgeKind, 'confirmed');
  assert.equal(confirmed.note, 'Confirmed Oct 8, 2026.');
  assert.equal(confirmed.canConfirm, false);

  const chosen = mainAppView(
    card(
      profile({
        main_app: { id: 'logic-pro', source: 'owner', confirmed_at: '2026-10-08T12:00:00Z' }
      })
    ),
    DATE
  );
  assert.equal(chosen.value, 'logic-pro');
  assert.equal(chosen.badge, 'Chosen by you');
  assert.equal(chosen.note, 'Chosen by you Oct 8, 2026.');
});

test('the main application has nothing to pick before a detection or when none is visible', () => {
  const before = mainAppView(card());
  assert.equal(before.disabled, true);
  assert.equal(before.note, 'Detect first, then pick one.');
  const none = mainAppView(card(profile({ apps: [] })));
  assert.equal(none.note, 'Nothing to pick from yet.');
  const one = mainAppView(card(profile({ apps: [reaper] })));
  assert.equal(one.note, 'Not chosen. REAPER was found. Pick it if it is yours.');
  // A stored pick whose application is hidden is not offered as the value.
  const hidden = mainAppView(
    card(
      profile({
        apps: [reaper, { ...logic, hidden: true }],
        main_app: { id: 'logic-pro', source: 'owner', confirmed_at: '2026-10-08T12:00:00Z' }
      })
    )
  );
  assert.equal(hidden.value, '');
  assert.deepEqual(hidden.options, [{ id: 'reaper', name: 'REAPER' }]);
});

test('the status line says what an action did', () => {
  assert.equal(profileStatus('detect', card(profile())), 'Found REAPER and Logic Pro.');
  assert.equal(
    profileStatus('detect', card(profile({ apps: [] }))),
    'Nothing was found on this Mac.'
  );
  assert.equal(profileStatus('hide', card(profile())), 'Hidden. Agents no longer see it.');
  assert.equal(profileStatus('main_app', card(profile())), 'Cleared.');
  assert.equal(
    profileStatus('main_app', card(profile({ main_app: { id: 'reaper', source: 'owner' } }))),
    'Saved.'
  );
  assert.equal(profileStatus('confirm', card(profile())), 'Confirmed.');
  assert.equal(profileStatus('show', card(profile())), 'Shown again as a hint.');
});

test('the card copy uses plain words and names no application of its own', () => {
  const views = [
    card(),
    card(profile()),
    card(profile({ apps: [] })),
    card(profile({ main_app: { id: 'reaper', source: 'detected', reason: 'library_majority' } }))
  ];
  const fixture = {
    title: 'T',
    intro: 'I',
    fields: FIELDS.map(field => ({ ...field, label: 'L' }))
  };
  const words = [];
  for (const view of views) {
    const anonymous = {
      ...view,
      ...fixture,
      profile: view.profile && {
        ...view.profile,
        apps: view.profile.apps.map(app => ({ ...app, name: 'App' }))
      }
    };
    words.push(
      JSON.stringify([profileView(anonymous), appsView(anonymous), mainAppView(anonymous)])
    );
  }
  const copy = words.join(' ');
  for (const jargon of [
    'provider',
    'declaration',
    'catalog',
    'discovery',
    'REAPER',
    'Logic',
    'Ableton'
  ]) {
    assert.equal(copy.includes(jargon), false, `card copy says "${jargon}"`);
  }
});

function panelWith(responses) {
  const requests = [];
  const panel = new HomeProfilePanel({
    workspaceId: 'home 1',
    program: { is_station: true },
    fetchImpl: async (url, options = {}) => {
      requests.push({
        url,
        method: options.method || 'GET',
        body: options.body ? JSON.parse(options.body) : null
      });
      const next = responses.shift();
      return { ok: next.status < 400, status: next.status, json: async () => next.body };
    }
  });
  // No DOM in this runner: the panel's rendering is exercised in the browser.
  panel.render = () => {};
  panel.status = message => {
    panel.lastStatus = message;
  };
  return { panel, requests };
}

test('every save names the revision the card was showing and a fresh request_id', async () => {
  const saved = card(profile({ revision: 4 }));
  const { panel, requests } = panelWith([
    { status: 200, body: saved },
    { status: 200, body: card(profile({ revision: 5 })) }
  ]);
  panel.view = card(profile({ revision: 3 }));
  await panel.run('Saving…', 'confirm', () => panel.fields({ confirm_apps: ['reaper'] }));
  await panel.run('Saving…', 'detect', () => panel.post('/detect'));
  assert.equal(requests[0].url, '/api/workspaces/home%201/assistant-program/profile/fields');
  assert.equal(requests[0].method, 'POST');
  assert.equal(requests[0].body.if_revision, 3);
  assert.deepEqual(requests[0].body.confirm_apps, ['reaper']);
  assert.match(requests[0].body.request_id, /^profile-/);
  assert.equal(requests[1].url, '/api/workspaces/home%201/assistant-program/profile/detect');
  assert.deepEqual(Object.keys(requests[1].body), ['request_id']);
  assert.notEqual(requests[0].body.request_id, requests[1].body.request_id);
  assert.equal(panel.view.revision, 5);
  assert.equal(panel.lastStatus, 'Found REAPER and Logic Pro.');
});

test('a profile that changed somewhere else is re-read, never overwritten', async () => {
  const fresh = card(profile({ revision: 9, apps: [reaper] }));
  const { panel, requests } = panelWith([
    { status: 409, body: { code: 'home_profile_changed', message: 'changed' } },
    { status: 200, body: fresh }
  ]);
  panel.view = card(profile({ revision: 3 }));
  await panel.run('Saving…', 'main_app', () => panel.fields({ main_app: 'logic-pro' }));
  assert.equal(requests.length, 2);
  assert.equal(requests[1].method, 'GET');
  assert.equal(panel.view.revision, 9);
  assert.equal(
    panel.lastStatus,
    'This profile changed somewhere else, so nothing was saved. It has been refreshed.'
  );
  assert.equal(panel.busy, false);
});

function templatesCard(state, stored = undefined, extra = {}) {
  return {
    available: true,
    read_only: false,
    templates: {
      state,
      app_id: 'reaper',
      app_name: 'REAPER',
      folders: ['ProjectTemplates', 'TrackTemplates']
    },
    profile: stored ? { templates: stored } : {},
    ...extra
  };
}

test('templates say Not read until the owner reviews, and never list a name before', () => {
  const view = templatesView(templatesCard('not_read'), DATE);
  assert.equal(view.note, 'Not read. Review to let Ori list the templates folder of REAPER.');
  assert.equal(view.canReview, true);
  assert.equal(view.canReadAgain, false);
  assert.equal(view.canForget, false);
  assert.deepEqual([view.project, view.track], [[], []]);
  // A consent the owner took back reads the same way, whatever is left stored.
  const revoked = templatesView(
    templatesCard('not_read', {
      consent: { granted_at: '2026-10-07T09:00:00Z', revoked_at: '2026-10-08T09:00:00Z' }
    }),
    DATE
  );
  assert.equal(revoked.canReview, true);
  assert.deepEqual(revoked.project, []);
});

test('listed templates show counts, the read date and the names by kind', () => {
  const stored = {
    read_at: '2026-10-07T10:00:00Z',
    items: [
      { name: 'Band Session', kind: 'project', file: 'Band Session.RPP' },
      { name: 'Vocal Comp', kind: 'project', file: 'Vocal Comp.RPP' },
      { name: 'Drum Bus', kind: 'track', file: 'Drum Bus.RTrackTemplate' }
    ]
  };
  const view = templatesView(templatesCard('listed', stored), DATE);
  assert.equal(view.note, '2 project templates and 1 track template, read Oct 7, 2026.');
  assert.deepEqual(view.project, ['Band Session', 'Vocal Comp']);
  assert.deepEqual(view.track, ['Drum Bus']);
  assert.equal(view.canReview, false);
  assert.equal(view.canReadAgain, true);
  assert.equal(view.canForget, true);
  // File names never reach the card copy.
  assert.equal(JSON.stringify(view).includes('.RPP'), false);
  const more = templatesView(templatesCard('listed', { ...stored, truncated: true }), DATE);
  assert.equal(
    more.note,
    '2 project templates and 1 track template, read Oct 7, 2026. The folders hold more than are listed here.'
  );
  const none = templatesView(templatesCard('empty', { read_at: '2026-10-07T10:00:00Z' }), DATE);
  assert.equal(
    none.note,
    'No templates were found in ProjectTemplates and TrackTemplates, read Oct 7, 2026.'
  );
  assert.equal(none.canReadAgain, true);
});

test('templates name the application for every state that cannot be read', () => {
  assert.equal(
    templatesView(templatesCard('other_app')).note,
    'Templates are read for REAPER only for now.'
  );
  assert.equal(
    templatesView(templatesCard('update_plugin')).note,
    'Update the REAPER plugin to read templates.'
  );
  assert.equal(
    templatesView(templatesCard('plugin_missing')).note,
    'Install the REAPER plugin to read templates.'
  );
  assert.equal(
    templatesView(templatesCard('detect_first')).note,
    'Not read. Detect first; templates are listed for an application found on this Mac.'
  );
  assert.equal(
    templatesView({ available: true }).note,
    'Templates cannot be listed for this Home yet.'
  );
  for (const state of [
    'other_app',
    'update_plugin',
    'plugin_missing',
    'detect_first',
    'unsupported'
  ]) {
    const view = templatesView(templatesCard(state));
    assert.deepEqual(
      [view.canReview, view.canReadAgain, view.canForget],
      [false, false, false],
      state
    );
  }
});

test('a failed read says what happened and how to retry', () => {
  const failed = templatesView(templatesCard('problem', { problem: 'read_failed' }));
  assert.equal(failed.note, 'The last read failed, so nothing is listed. Read again to retry.');
  assert.equal(failed.canReadAgain, true);
  assert.equal(failed.canForget, true);
  const old = templatesView(templatesCard('problem', { problem: 'operation_unavailable' }));
  assert.equal(old.note, 'Update the REAPER plugin to read templates.');
  assert.equal(old.canReadAgain, false);
  assert.equal(old.canForget, true);
});

test('a read-only Home shows the templates and disables every templates control', () => {
  const stored = {
    read_at: '2026-10-07T10:00:00Z',
    items: [{ name: 'Band Session', kind: 'project', file: 'Band Session.RPP' }]
  };
  const listed = templatesView(templatesCard('listed', stored, { read_only: true }), DATE);
  assert.deepEqual(listed.project, ['Band Session']);
  assert.deepEqual(
    [listed.canReview, listed.canReadAgain, listed.canForget, listed.disabled],
    [false, false, false, true]
  );
  assert.equal(
    templatesView(templatesCard('not_read', undefined, { read_only: true })).canReview,
    false
  );
});

test('the templates status line says what a read or a Forget did', () => {
  const stored = {
    read_at: '2026-10-07T10:00:00Z',
    items: [
      { name: 'A', kind: 'project', file: 'A.RPP' },
      { name: 'B', kind: 'track', file: 'B.RTrackTemplate' }
    ]
  };
  assert.equal(profileStatus('templates', templatesCard('listed', stored)), 'Listed 2 templates.');
  assert.equal(
    profileStatus('templates', templatesCard('empty', { read_at: '2026-10-07T10:00:00Z' })),
    'No templates were found.'
  );
  assert.equal(profileStatus('templates', templatesCard('not_read')), 'Nothing was read.');
  assert.equal(
    profileStatus('templates', templatesCard('problem', { problem: 'read_failed' })),
    'The last read failed, so nothing is listed. Read again to retry.'
  );
  assert.equal(
    profileStatus('forget', templatesCard('not_read')),
    'Forgotten. Nothing is read until you review it again.'
  );
});

test('Review reads nothing until the dialog’s own button is pressed', async () => {
  const review = {
    review_id: 'r1',
    app_name: 'REAPER',
    folders: ['ProjectTemplates'],
    sentence: 'S'
  };
  const listed = templatesCard('listed', {
    read_at: '2026-10-07T10:00:00Z',
    items: [{ name: 'A', kind: 'project' }]
  });
  // Dismissed: one review request, no commit.
  const dismissed = panelWith([{ status: 200, body: review }]);
  dismissed.panel.view = templatesCard('not_read');
  dismissed.panel.confirmTemplatesRead = async () => false;
  await dismissed.panel.readTemplates({ confirm: true });
  assert.deepEqual(
    dismissed.requests.map(request => request.url.split('/profile')[1]),
    ['/templates/review']
  );
  assert.equal(dismissed.panel.lastStatus, 'Nothing was read.');
  // Agreed: the commit names the review it was shown.
  const agreed = panelWith([
    { status: 200, body: review },
    { status: 200, body: listed }
  ]);
  agreed.panel.view = templatesCard('not_read');
  let shown = null;
  agreed.panel.confirmTemplatesRead = async value => {
    shown = value;
    return true;
  };
  await agreed.panel.readTemplates({ confirm: true });
  assert.deepEqual(shown, review);
  assert.equal(agreed.requests[1].url.endsWith('/profile/templates/commit'), true);
  assert.equal(agreed.requests[1].body.review_id, 'r1');
  assert.equal(agreed.panel.lastStatus, 'Listed 1 template.');
  // Read again, under a consent already given, shows no second dialog.
  const again = panelWith([
    { status: 200, body: review },
    { status: 200, body: listed }
  ]);
  again.panel.view = listed;
  again.panel.confirmTemplatesRead = async () => {
    throw new Error('the dialog must not open');
  };
  await again.panel.readTemplates({ confirm: false });
  assert.equal(again.requests.length, 2);
});

test('a plugin that cannot list templates is said plainly and the card is not left busy', async () => {
  const { panel, requests } = panelWith([
    {
      status: 409,
      body: {
        code: 'plugin_operation_unavailable',
        message: 'The installed project plugin cannot list templates. Update it, then try again.'
      }
    }
  ]);
  panel.view = templatesCard('not_read');
  await panel.readTemplates({ confirm: true });
  assert.equal(requests.length, 1);
  assert.equal(
    panel.lastStatus,
    'The installed project plugin cannot list templates. Update it, then try again.'
  );
  assert.equal(panel.busy, false);
});

test('a read-only card sends nothing', async () => {
  const { panel, requests } = panelWith([]);
  panel.view = card(profile(), { read_only: true });
  await panel.run('Saving…', 'detect', () => panel.post('/detect'));
  assert.equal(requests.length, 0);
});
