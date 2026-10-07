import test from 'node:test';
import assert from 'node:assert/strict';
import {
  HomeProfilePanel,
  READ_ONLY_NOTE,
  appRowView,
  appsView,
  detectView,
  joinNames,
  mainAppView,
  profileDate,
  profileStatus,
  profileView
} from './home-profile.js';

const DATE = { locale: 'en-US', timeZone: 'UTC' };

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

test('a read-only card sends nothing', async () => {
  const { panel, requests } = panelWith([]);
  panel.view = card(profile(), { read_only: true });
  await panel.run('Saving…', 'detect', () => panel.post('/detect'));
  assert.equal(requests.length, 0);
});
