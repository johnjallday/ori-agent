import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

import {
  FIRST_LOOK_FOOTNOTE,
  folderOfferView,
  folderReceiptView,
  hireLookView,
  hireView,
  setupResultView as setupModalView,
  setupRunView
} from './personal-assistant-folder.js';

test('stop families keep distinct reviewed recovery actions and never guess a plugin update', () => {
  const cases = [
    ['needs_pick', {}, ['repick', 'resume']],
    ['needs_choice', {}, ['choose-0', 'resume']],
    ['needs_model', {}, ['model', 'retry', 'resume']],
    ['plan_changed', {}, ['resume']],
    ['consent_stale', {}, ['resume']],
    ['assistant_missing', {}, ['resume']],
    ['install_failed', {}, ['retry', 'resume']],
    ['unknown_failure', {}, ['retry', 'resume']],
    ['member_failed', { structure: true }, ['retry']],
    ['plan_changed', { structure: true }, ['review-saved']],
    ['plan_changed', { software: true }, ['review']],
    ['interrupted', { software: true }, ['retry']]
  ];
  for (const [reason, variant, ids] of cases) {
    const view = setupRunView(
      { status: 'stopped', stop_reason: reason, entry_candidates: ['Actual.rpp'] },
      'Project',
      variant
    );
    assert.deepEqual(
      view.actions.map(action => action.id),
      ids,
      reason
    );
    if (reason.includes('_')) assert.doesNotMatch(view.question, new RegExp(reason));
    assert.ok(!view.actions.some(action => action.label === 'Retry update'));
    if (reason === 'needs_choice') assert.equal(view.actions[0].entry, 'Actual.rpp');
  }
});

const source = readFileSync(new URL('./personal-assistant-folder.js', import.meta.url), 'utf8');
const template = readFileSync(
  new URL('../../../templates/components/personal-assistant-folder.tmpl', import.meta.url),
  'utf8'
);

const text = segments => segments.map(part => part.text).join('');
const strong = segments => segments.filter(part => part.strong).map(part => part.text);

const BRIEF =
  'Understand this project, identify evidence-backed risks, and propose prioritized improvements before changing code.';

// A recognized project offer whose plan names who it hires, as the server sends it.
const hireOffer = (hire = {}, overrides = {}) => ({
  id: 'offer-1',
  verdict: 'project',
  status: 'pending',
  folder: 'Songs',
  subject: { name: 'My Song', marker: 'REAPER project' },
  reason: 'My Song.rpp is a REAPER project file.',
  capability: {
    recognized: 'REAPER song',
    workspace: 'REAPER Song workspace',
    integration: 'Setup will install the Ori integration plugin.',
    evidence: 'My Song.rpp is a REAPER project file.',
    setup_quest_id: 'install_ori_reaper'
  },
  plan: {
    digest: 'abc',
    lines: [
      { kind: 'workspace', name: 'Creates a REAPER Song workspace named My Song' },
      { kind: 'folder', name: 'Linked where it is; nothing is moved, copied or run' },
      { kind: 'task', name: 'Queues a first read-only task for you to start' }
    ],
    hire: {
      mode: 'create',
      agent_name: 'Song Assistant',
      role_label: 'Producer',
      model: 'sonnet',
      ...hire
    },
    brief_default: "Understand this song's project and suggest the next production step."
  },
  ...overrides
});

test('a plan that names a hire is a hire card: Hire and start, Not now, and Adjust beside its lines', () => {
  const view = folderOfferView(hireOffer());
  assert.equal(text(view.headline), 'Hire Song Assistant for My Song');
  assert.deepEqual(strong(view.headline), ['Song Assistant', 'My Song']);
  assert.deepEqual(
    view.actions.map(action => [action.id, action.label]),
    [
      ['setup', 'Hire & start'],
      ['no', 'Not now']
    ]
  );
  assert.equal(view.actions[0].oneCard, true);
  // Not now is the same visible, undoable decline the card always had.
  assert.equal(view.actions[1].decision, 'no');
  assert.equal(view.hire.adjust.label, 'Adjust');
  assert.equal(view.hire.adjust.journey, true);
  assert.equal(view.hire.linesHeading, 'Also, in the background');
  // The lines are the plan's, unchanged, with the same digest.
  assert.equal(view.plan.digest, 'abc');
  assert.equal(view.plan.lines.length, 3);
  assert.equal(view.question, 'My Song looks like a REAPER song project.');
});

test('the agent row names the role and the model, or that there is no model yet', () => {
  const view = hireView(hireOffer().plan, 'My Song');
  assert.deepEqual(view.agent, { initials: 'SA', detail: 'Producer role · uses sonnet' });
  const noModel = hireView(hireOffer({ model: '' }).plan, 'My Song');
  assert.equal(noModel.agent.detail, 'Producer role · no model yet');
});

test('an existing agent is assigned, and a plugin not installed yet names the capability', () => {
  const assigned = hireView(hireOffer({ mode: 'bind', agent_name: 'Studio Lead' }).plan, 'My Song');
  assert.equal(text(assigned.headline), 'Assign Studio Lead to My Song');
  assert.equal(assigned.confirmLabel, 'Assign & start');
  assert.equal(folderOfferView(hireOffer({ mode: 'bind' })).actions[0].label, 'Assign & start');
  const pending = hireView(
    hireOffer({ mode: 'pending', agent_name: '', role_label: 'REAPER song', model: '' }).plan,
    'My Song'
  );
  assert.equal(text(pending.headline), 'Hire a REAPER song assistant for My Song');
  assert.equal(pending.agent.detail, 'REAPER song role · named once its plugin is installed');
  assert.equal(pending.linesHeading, 'Also, once installed');
  const article = hireView(
    hireOffer({ mode: 'pending', agent_name: '', role_label: 'Audio', model: '' }).plan,
    'Mix'
  );
  assert.equal(text(article.headline), 'Hire an Audio assistant for Mix');
});

test('a plan that still installs or enables a plugin puts its lines under "Also, once installed"', () => {
  const plan = hireOffer().plan;
  assert.equal(
    hireView(plan, 'My Song', { needs: [{ kind: 'integration', state: 'install' }] }).linesHeading,
    'Also, once installed'
  );
  assert.equal(
    hireView(plan, 'My Song', { needs: [{ kind: 'integration', state: 'ready' }] }).linesHeading,
    'Also, in the background'
  );
});

test('the one question starts from the plan default, and a plan without one asks nothing', () => {
  const view = hireView(hireOffer().plan, 'My Song');
  assert.deepEqual(view.brief, {
    label: 'What should it do with it?',
    value: "Understand this song's project and suggest the next production step.",
    hint: 'This becomes the workspace goal. You can change it later.'
  });
  const plan = { ...hireOffer().plan, brief_default: '' };
  assert.equal(hireView(plan, 'My Song').brief, null);
});

test('a plan without a hire, or with one the card does not know, keeps the Set up card', () => {
  const { hire: _omit, ...plain } = hireOffer().plan;
  const view = folderOfferView({ ...hireOffer(), plan: plain });
  assert.equal(view.hire, null);
  assert.deepEqual(
    view.actions.map(action => action.label),
    ['Set up', 'Adjust…', 'No thanks', 'Later']
  );
  assert.equal(hireView({ ...plain, hire: { mode: 'promote', agent_name: 'X' } }, 'My Song'), null);
  // A collection is not a hire (decision 1A).
  const collection = folderOfferView(hireOffer({}, { portfolio: { projects: 6 } }));
  assert.equal(collection.hire, null);
});

test('after Adjust… the hire keeps its plan beside Continue setup', () => {
  const view = folderOfferView(hireOffer({}, { status: 'awaiting_outcome', decision: 'yes' }));
  assert.deepEqual(
    view.actions.map(action => action.label),
    ['Hire & start', 'Continue setup']
  );
  assert.equal(view.hire.adjust, null);
});

// A reviewed software project, as the server sends it once its review is read.
const reviewedSoftware = (overrides = {}) => ({
  id: 'offer-2',
  status: 'pending',
  verdict: 'project',
  reason: '12 Go files, edited today',
  folder: 'widget',
  subject: { name: 'widget', kind: 'project', shape: 'code', marker: 'Go module', is_root: true },
  blueprint: 'code-project',
  blueprint_label: 'Code project',
  software: {
    recipe: 'software_project',
    marker: 'Go module',
    repository: 'directory',
    setup_available: true,
    workspace_name: 'widget',
    assistant: 'Development Assistant',
    review: { evidence_revision: 'a'.repeat(64), stack: ['Go'], files: ['README.md'] }
  },
  plan: {
    digest: 'd'.repeat(64),
    lines: [
      { kind: 'workspace', name: 'Creates a Code project workspace named widget' },
      {
        kind: 'folder',
        name: 'Linked where it is; nothing is moved, copied or run',
        detail: 'Read-only.'
      },
      { kind: 'agents', name: 'Adds one development assistant, Development Assistant' }
    ],
    hire: { mode: 'create', agent_name: 'Development Assistant', role_label: 'Code project' },
    brief_default: BRIEF
  },
  ...overrides
});

test('a reviewed software project is hired the same way, keeping what was read', () => {
  const view = folderOfferView(reviewedSoftware());
  assert.equal(text(view.headline), 'Hire Development Assistant for widget');
  assert.equal(view.hire.agent.detail, 'Code project role · no model yet');
  assert.equal(view.hire.brief.value, BRIEF);
  assert.equal(view.question, 'What I read in widget:');
  assert.deepEqual(
    view.actions.map(action => action.label),
    ['Hire & start', 'Not now', 'Review again']
  );
  assert.equal(view.hire.adjust.softwareAdjust, true);
  assert.ok(view.evidence.length > 0);
  // A host that cannot carry the plan out keeps the review and offers no hire.
  const unavailable = folderOfferView(
    reviewedSoftware({ software: { ...reviewedSoftware().software, setup_available: false } })
  );
  assert.ok(!unavailable.hire);
  assert.ok(!unavailable.actions.some(action => action.oneCard));
});

test('Hire and start sends the brief only when the user changed it', () => {
  const body = source.slice(
    source.indexOf('async function startOneCardSetup('),
    source.indexOf('function syncSetupPolling(')
  );
  assert.match(body, /const brief = offer\.plan\?\.brief_default \? briefFor\(offer\) : null;/);
  assert.match(body, /if \(brief !== null\) body\.brief = brief;/);
  // What the user typed is kept per offer and survives a re-render.
  assert.match(
    source,
    /state\.brief = \{ offerId: String\(state\.offer\?\.id \|\| ''\), value: els\.brief\.value, typed: true \}/
  );
  assert.match(source, /document\.activeElement !== els\.brief/);
});

test('the brief field is a real labelled control, bounded like a workspace goal', () => {
  assert.match(
    template,
    /<label for="personalAssistantFolderBrief"[^>]*>What should it do with it\?<\/label>/
  );
  assert.match(
    template,
    /<textarea id="personalAssistantFolderBrief"[\s\S]*?aria-describedby="personalAssistantFolderBriefHint"/
  );
  assert.doesNotMatch(template, /<textarea id="personalAssistantFolderBrief"[^>]*maxlength=/);
  assert.match(template, /Up to 300 characters/);
  assert.equal(globalThis.OriFolderSetupVisual.briefError('🎼'.repeat(300)), '');
  assert.match(globalThis.OriFolderSetupVisual.briefError('🎼'.repeat(301)), /300 characters/);
});

// A set-up folder whose hire started its agent's first look, as the server
// sends it once the outcome is verified.
const hiredOffer = (look = {}, record = { started: true, task_id: 'look-1' }) => ({
  id: 'offer-3',
  status: 'resolved',
  verdict: 'project',
  subject: { name: 'elite_school' },
  outcome: {
    kind: 'project',
    route: '/workspaces/elite-school',
    receipt: [
      { kind: 'workspace', name: 'elite_school', route: '/workspaces/elite-school' },
      { kind: 'task', name: 'Assess this project and suggest a first task' }
    ],
    ...(record ? { first_look: record } : {})
  },
  first_task: {
    state: 'running',
    offer_id: 'offer-3',
    task_id: 'look-1',
    workspace_id: 'ws-1',
    workspace_name: 'elite_school',
    workspace_route: '/workspaces/elite-school',
    agent: 'Development Assistant',
    detail: 'Running…',
    ...look
  }
});

const lineStates = view => view.lines.map(line => [line.kind, line.state]);

test('a hired look follows the receipt as two lines whose states come from where it stands', () => {
  const running = hireLookView(hiredOffer());
  assert.deepEqual(lineStates(running), [
    ['first_look_read', 'working'],
    ['first_look_suggest', 'waiting']
  ]);
  assert.deepEqual(
    running.lines.map(line => line.name),
    ['Reading the project, read-only', 'Writes its first suggestion']
  );
  assert.equal(running.footnote, FIRST_LOOK_FOOTNOTE);
  assert.equal(
    FIRST_LOOK_FOOTNOTE,
    'Reads only. Nothing in the folder is run or changed. Stop at any time.'
  );
  assert.equal(running.stoppable, true);

  const finished = hireLookView(
    hiredOffer({ state: 'finished', next_request: 'Add a CI workflow for pytest.' })
  );
  assert.deepEqual(lineStates(finished), [
    ['first_look_read', 'done'],
    ['first_look_suggest', 'done']
  ]);
  assert.equal(finished.lines[1].detail, 'Suggests: Add a CI workflow for pytest.');
  assert.equal(finished.footnote, '');
  assert.equal(finished.stoppable, false);
  assert.equal(
    hireLookView(hiredOffer({ state: 'finished' })).lines[1].detail,
    'It did not name one next task.'
  );

  const waiting = hireLookView(
    hiredOffer({ state: 'waiting', message: 'Development Assistant is waiting for your answer.' })
  );
  assert.equal(waiting.lines[0].detail, 'Development Assistant is waiting for your answer.');
  const failed = hireLookView(
    hiredOffer({ state: 'failed', message: "Development Assistant's first look did not finish." })
  );
  assert.deepEqual(lineStates(failed)[0], ['first_look_read', 'failed']);
  assert.match(failed.lines[0].detail, /did not finish/);
});

test('a look the hire could not start, or that was stopped, says so on its line', () => {
  const blocked = hireLookView(
    hiredOffer(
      { state: 'seeded', can_start: false, message: 'Choose a model that can read without tools.' },
      { started: false, reason: 'model_unsupported' }
    )
  );
  assert.deepEqual(lineStates(blocked), [
    ['first_look_read', 'waiting'],
    ['first_look_suggest', 'waiting']
  ]);
  assert.equal(blocked.lines[0].detail, 'Choose a model that can read without tools.');
  const stopped = hireLookView(hiredOffer({ state: 'seeded', can_start: true, message: '' }));
  assert.equal(stopped.lines[0].detail, 'Stopped. Nothing in the folder was changed.');
  // A setup that hired no one keeps the plain receipt.
  assert.equal(hireLookView(hiredOffer({}, null)).visible, false);
  assert.equal('hireLook' in folderReceiptView(hiredOffer({}, null)), false);
});

test('a running hired look can be stopped from the receipt, and Start first look comes back after', () => {
  const receipt = folderReceiptView(hiredOffer());
  assert.equal(receipt.hireLook.visible, true);
  assert.deepEqual(
    receipt.firstLook.actions.map(action => [action.id, action.label]),
    [
      ['stop-first-look', 'Stop'],
      ['open', 'Open elite_school']
    ]
  );
  assert.equal(receipt.firstLook.actions[0].stop, true);
  const after = folderReceiptView(hiredOffer({ state: 'seeded', can_start: true }));
  assert.equal(after.firstLook.actions[0].label, 'Start first look');
  assert.ok(!after.firstLook.actions.some(action => action.stop));
  // Stop posts to the look's own stop route, and nothing else.
  const body = source.slice(
    source.indexOf('async function stopFirstLook('),
    source.indexOf('async function pollSetup(')
  );
  assert.match(body, /\/folder-first-task\/stop/);
  assert.match(body, /look\.state !== 'running'/);
  assert.match(template, /id="personalAssistantFolderLookNote"/);
  // A poll that redraws the card keeps the same Stop button under the pointer:
  // the action row is rebuilt only when what it offers changes.
  assert.match(source, /const rebuild = els\.actions\.dataset\.signature !== signature;/);
  assert.match(source, /if \(rebuild && firstLook\.visible\)/);
  assert.match(source, /\} else if \(rebuild && receipt\.route\)/);
});

test('the run pop-up ends a hire on its agent at work, or on why the look waits', () => {
  const running = setupModalView(hiredOffer());
  assert.equal(running.eyebrow, 'Setup ready');
  assert.equal(
    running.status,
    "Development Assistant is reading elite_school, read-only. I'll tell you here when it has something."
  );
  const blocked = setupModalView(
    hiredOffer(
      { state: 'seeded', can_start: false, message: 'Choose a model that can read without tools.' },
      { started: false, reason: 'model_unsupported' }
    )
  );
  assert.equal(
    blocked.status,
    'Here is what I set up. Choose a model that can read without tools.'
  );
  assert.equal(setupModalView(hiredOffer({}, null)).eyebrow, 'Setup ready');
  const finished = setupModalView(hiredOffer({ state: 'finished' }));
  assert.match(finished.status, /finished the first look/);
  assert.doesNotMatch(finished.status, /is reading/);
});
