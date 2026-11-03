import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  DEFAULT_MAP_ARRANGEMENT,
  briefScopeHasOthers,
  flowAccessibleName,
  flowModel,
  mapArrangementStorageKey,
  readMapArrangement,
  writeMapArrangement
} from './hq-map-flow.js';
import { tacticalTermsIn } from './hq-labels.js';

// The HQ's stations in registry order, as mapStationRegistry() lists them.
function stations(overrides = {}) {
  const base = {
    watchtower: {
      value: 'All clear',
      description: 'no cross-workspace attention items',
      tone: 'clear'
    },
    email: { value: '2 follow-ups', description: 'Email Ops', tone: 'attention' },
    'calendar-ops': {
      value: 'Set up',
      description: 'Calendar Ops is not set up yet',
      tone: 'attention'
    },
    'daily-brief': { value: 'Ready 07:30', description: "today's brief is ready", tone: 'clear' },
    memory: { value: '14 entries', description: '14 entries, 2 watching', tone: 'clear' },
    'second-brain': { value: '3 open', description: '2 came due', tone: 'clear' }
  };
  const labels = {
    watchtower: 'Watchtower',
    email: 'Email',
    'calendar-ops': 'Calendar Ops',
    'daily-brief': 'Daily Brief',
    memory: 'Memory',
    'second-brain': 'Notes'
  };
  return Object.keys(base).map(key => ({
    key,
    label: labels[key],
    state: overrides[key] === undefined ? base[key] : overrides[key]
  }));
}

const agents = [
  { name: 'Researcher', entry: false, status: 'Idle' },
  { name: 'Ori', encodedName: 'Ori', entry: true, status: 'Reading your sources' },
  { name: 'Scheduler', entry: false, status: 'Idle' }
];

test('sources are Notes, Email, Calendar and Memory in that order', () => {
  const { sources } = flowModel({ stations: stations() });
  assert.deepEqual(
    sources.map(node => [node.key, node.label]),
    [
      ['second-brain', 'Notes'],
      ['email', 'Email'],
      ['calendar-ops', 'Calendar'],
      ['memory', 'Memory']
    ]
  );
});

test('Other workspaces joins last only when the brief reads another workspace', () => {
  const without = flowModel({ stations: stations(), briefScopeHasOthers: false });
  assert.ok(!without.sources.some(node => node.key === 'watchtower'));
  const withOthers = flowModel({ stations: stations(), briefScopeHasOthers: true });
  const last = withOthers.sources.at(-1);
  assert.equal(last.key, 'watchtower');
  assert.equal(last.label, 'Other workspaces');
  assert.equal(last.station, 'watchtower');
});

test('a station missing from the registry is simply absent', () => {
  const { sources } = flowModel({ stations: stations().filter(s => s.key !== 'memory') });
  assert.deepEqual(
    sources.map(node => node.key),
    ['second-brain', 'email', 'calendar-ops']
  );
});

test('source state words match the Details grid and dashed follows configured', () => {
  const { sources } = flowModel({ stations: stations() });
  const byKey = Object.fromEntries(sources.map(node => [node.key, node]));
  assert.equal(byKey.email.stateWord, 'Needs you');
  assert.equal(byKey.email.tone, 'attention');
  assert.equal(byKey.email.dashed, false);
  assert.equal(byKey['calendar-ops'].stateWord, 'Not set up');
  assert.equal(byKey['calendar-ops'].dashed, true);
  assert.equal(byKey['calendar-ops'].tone, 'unset');
  // The setup sentence is the station's own words ("Set up" is said by it).
  assert.equal(byKey['calendar-ops'].subtitle, 'Calendar Ops is not set up yet');
  assert.equal(byKey.memory.stateWord, 'Ready');
  assert.equal(byKey.memory.dashed, false);
  // "14 entries" is said by the description, so it is not said twice.
  assert.equal(byKey.memory.subtitle, '14 entries, 2 watching');
});

test('a node subtitle never repeats the label or the state word', () => {
  const { sources } = flowModel({
    stations: stations({ 'second-brain': { value: 'Notes', description: 'ready', tone: 'clear' } })
  });
  const notes = sources.find(node => node.key === 'second-brain');
  assert.equal(notes.stateWord, 'Ready');
  assert.equal(notes.subtitle, '');
  assert.equal(notes.accessibleName, 'Notes, Ready');
});

test("the right lane is Needs you, Follow-ups, Today's brief and Tickets", () => {
  const { needs } = flowModel({ stations: stations() });
  assert.deepEqual(
    needs.map(node => [node.key, node.label]),
    [
      ['needs-you', 'Needs you'],
      ['follow-ups', 'Follow-ups'],
      ['brief', "Today's brief"],
      ['tasks', 'Tickets']
    ]
  );
});

test('right-lane subtitles come from the counts', () => {
  const { needs } = flowModel({
    stations: stations(),
    counts: {
      needsYou: 3,
      needsYouFrom: ['Lead', 'Email', 'Email', 'Notes'],
      followUps: 2,
      openTickets: 5,
      ticketLine: '1 blocked · 2 in progress'
    }
  });
  const byKey = Object.fromEntries(needs.map(node => [node.key, node]));
  assert.equal(byKey['needs-you'].stateWord, '3');
  assert.equal(byKey['needs-you'].subtitle, 'Lead, Email, Notes');
  assert.equal(byKey['needs-you'].tone, 'attention');
  assert.equal(byKey['follow-ups'].stateWord, '2 open');
  assert.equal(byKey['follow-ups'].subtitle, '2 replies and promises to keep');
  assert.equal(byKey.tasks.stateWord, '5 open');
  assert.equal(byKey.tasks.subtitle, '1 blocked · 2 in progress');
  assert.equal(byKey.brief.station, 'daily-brief');
  assert.equal(byKey.brief.stateWord, 'Ready');
  assert.equal(byKey.brief.subtitle, "Ready 07:30 · Today's brief is ready");
});

test('right-lane nodes read clear when nothing waits and checking when unknown', () => {
  const clear = flowModel({
    stations: stations(),
    counts: { needsYou: 0, followUps: 0, openTickets: 0 }
  }).needs;
  assert.equal(clear[0].stateWord, 'Clear');
  assert.equal(clear[0].subtitle, 'Nothing needs you right now.');
  assert.equal(clear[0].tone, 'ready');
  assert.equal(clear[1].subtitle, 'Nothing to chase');
  assert.equal(clear[3].subtitle, 'No open tickets');

  const unknown = flowModel({ stations: stations(), counts: { followUps: null } }).needs;
  assert.equal(unknown[1].stateWord, 'Checking');
  assert.equal(unknown[1].tone, 'muted');
  assert.equal(unknown[3].stateWord, 'Checking');
});

test('without a Daily Brief station the brief node is dashed and not set up', () => {
  const { needs } = flowModel({ stations: stations().filter(s => s.key !== 'daily-brief') });
  const brief = needs.find(node => node.key === 'brief');
  assert.equal(brief.dashed, true);
  assert.equal(brief.stateWord, 'Not set up');
  assert.equal(brief.station, '');
});

test('the lead card names the lead, its status and its specialists', () => {
  const { lead } = flowModel({ stations: stations(), agents });
  assert.equal(lead.name, 'Ori');
  assert.equal(lead.status, 'Reading your sources');
  assert.equal(lead.waiting, '');
  assert.deepEqual(lead.specialists, ['Researcher', 'Scheduler']);
  assert.equal(lead.specialistsLine, 'Specialists: Researcher, Scheduler');
  assert.equal(
    lead.accessibleName,
    'Ori, Lead, Reading your sources, Specialists: Researcher, Scheduler'
  );
});

test('the lead card shows what the lead waits on', () => {
  const { lead } = flowModel({
    stations: stations(),
    agents,
    waiting: '  Approve the reply\nto Dana '
  });
  assert.equal(lead.waiting, 'Approve the reply to Dana');
  assert.match(lead.accessibleName, /Waiting on you: Approve the reply to Dana/);
});

test('a lead alone has no specialists, and no lead is null', () => {
  const alone = flowModel({ agents: [{ name: 'Ori', entry: true }] }).lead;
  assert.equal(alone.specialistsLine, 'No specialists yet');
  assert.equal(alone.status, 'Idle');
  assert.equal(flowModel({ agents: [{ name: 'Helper', entry: false }] }).lead, null);
  assert.equal(flowModel().lead, null);
});

test('no node or lead says a tactical word', () => {
  const model = flowModel({ stations: stations(), agents, briefScopeHasOthers: true });
  const text = JSON.stringify(model);
  assert.deepEqual(tacticalTermsIn(text), []);
});

// ── Accessible names (FR10) ──

test('flowAccessibleName drops what says nothing or is said elsewhere', () => {
  // renderMapHQStations(): a value the description repeats is not read twice.
  assert.equal(
    flowAccessibleName('Memory', 'Ready', '14 entries', '14 entries, 2 watching'),
    'Memory, Ready, 14 entries, 2 watching'
  );
  // ... nor a value of "—", nor one that only repeats the station's name.
  assert.equal(
    flowAccessibleName('Email', 'Checking', '—', 'checking Email Ops'),
    'Email, Checking, checking Email Ops'
  );
  assert.equal(flowAccessibleName('Notes', 'Ready', 'Notes'), 'Notes, Ready');
  // The label always stays, even when a later part contains it.
  assert.equal(
    flowAccessibleName('Email', 'Not set up', 'Set up Email Ops'),
    'Email, Not set up, Set up Email Ops'
  );
  assert.equal(flowAccessibleName('Tickets', '', '', null), 'Tickets');
});

// ── Brief scope ──

test('briefScopeHasOthers reads the Daily Brief config', () => {
  assert.equal(briefScopeHasOthers(null, 'hq'), false);
  assert.equal(briefScopeHasOthers({ scope: 'all' }, 'hq'), true);
  assert.equal(briefScopeHasOthers({ scope: 'selected', selected_workspace_ids: [] }, 'hq'), false);
  assert.equal(
    briefScopeHasOthers({ scope: 'selected', selected_workspace_ids: ['hq', ' '] }, 'hq'),
    false
  );
  assert.equal(
    briefScopeHasOthers({ scope: 'selected', selected_workspace_ids: ['hq', 'music'] }, 'hq'),
    true
  );
});

// ── Arrangement memory (FR3) ──

function memoryStorage(initial = {}) {
  const data = { ...initial };
  return {
    data,
    getItem: key => (key in data ? data[key] : null),
    setItem: (key, value) => {
      data[key] = String(value);
    }
  };
}

test('no stored value means Flow', () => {
  assert.equal(DEFAULT_MAP_ARRANGEMENT, 'flow');
  assert.equal(readMapArrangement(memoryStorage(), 'personal-hq'), 'flow');
});

test('a remembered Free is read back per workspace', () => {
  const storage = memoryStorage();
  writeMapArrangement(storage, 'personal-hq', 'free');
  assert.equal(storage.data['oriWorkspaceMapArrangement:personal-hq'], 'free');
  assert.equal(mapArrangementStorageKey('personal-hq'), 'oriWorkspaceMapArrangement:personal-hq');
  assert.equal(readMapArrangement(storage, 'personal-hq'), 'free');
  assert.equal(readMapArrangement(storage, 'other-hq'), 'flow');
  writeMapArrangement(storage, 'personal-hq', 'flow');
  assert.equal(readMapArrangement(storage, 'personal-hq'), 'flow');
});

test('a bad stored value is Flow, and a bad value is never written', () => {
  const storage = memoryStorage({ 'oriWorkspaceMapArrangement:hq': 'grid' });
  assert.equal(readMapArrangement(storage, 'hq'), 'flow');
  writeMapArrangement(storage, 'hq', 'grid');
  assert.equal(storage.data['oriWorkspaceMapArrangement:hq'], 'grid');
});

test('no storage, a throwing storage or no slug never breaks', () => {
  assert.equal(readMapArrangement(null, 'hq'), 'flow');
  assert.equal(readMapArrangement(memoryStorage(), ''), 'flow');
  const throwing = {
    getItem() {
      throw new Error('denied');
    },
    setItem() {
      throw new Error('denied');
    }
  };
  assert.equal(readMapArrangement(throwing, 'hq'), 'flow');
  assert.doesNotThrow(() => writeMapArrangement(throwing, 'hq', 'free'));
  assert.doesNotThrow(() => writeMapArrangement(undefined, 'hq', 'free'));
});
