import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

import {
  FIRST_LOOK_FOOTNOTE,
  confirmationSummary,
  conversationFolderOfferView,
  planWithWatching,
  folderOfferView,
  folderReceiptView,
  isInboxFolderOffer,
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
      [...ids, 'remove'],
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

// An inbox hire, resolved: its first look is its first batch.
function inboxHire(batch) {
  return {
    id: 'offer-inbox',
    status: 'resolved',
    subject: { name: 'Downloads', shape: 'inbox', is_root: true },
    outcome: {
      kind: 'project',
      workspace_id: 'ws-downloads',
      route: '/workspaces/downloads',
      first_look: { started: false, batch, at: '2026-10-24T09:00:00Z' },
      receipt: [
        { kind: 'agent', name: 'File Curator', detail: 'file curator, added', assigned: true },
        { kind: 'workspace', name: 'Downloads', detail: 'created', route: '/workspaces/downloads' },
        { kind: 'folder', name: 'Downloads', detail: 'linked where it is' },
        {
          kind: 'batch',
          name: 'First batch',
          detail: '23 proposed · 4 need review',
          route: '/workspaces/downloads?panel=file-janitor&batch_id=batch-1'
        }
      ]
    }
  };
}

test('an inbox hire ends on its first batch: Review batch, never Start first look', () => {
  const receipt = folderReceiptView(
    inboxHire({
      outcome: 'batch',
      batch_id: 'batch-1',
      eligible: 23,
      needs_review: 4,
      route: '/workspaces/downloads?panel=file-janitor&batch_id=batch-1'
    })
  );
  assert.equal(receipt.visible, true);
  assert.equal(receipt.openLabel, 'Review batch');
  assert.equal(receipt.route, '/workspaces/downloads?panel=file-janitor&batch_id=batch-1');
  assert.equal('firstLook' in receipt, false);
  assert.equal('hireLook' in receipt, false);
  assert.deepEqual(
    receipt.rows.map(row => [row.kind, row.detail]),
    [
      ['agent', 'file curator, added'],
      ['workspace', 'created'],
      ['folder', 'linked where it is'],
      ['batch', '23 proposed · 4 need review']
    ]
  );
  // Nothing to file opens File Janitor itself.
  const empty = folderReceiptView(
    inboxHire({ outcome: 'no_eligible', route: '/workspaces/downloads?panel=file-janitor' })
  );
  assert.equal(empty.openLabel, 'Open File Janitor');
  assert.equal(empty.route, '/workspaces/downloads?panel=file-janitor');
  // A route that is not a File Janitor review is never followed.
  const elsewhere = folderReceiptView(
    inboxHire({ outcome: 'batch', route: 'https://example.com/?panel=file-janitor' })
  );
  assert.equal(elsewhere.openLabel, 'Open Downloads');
  assert.equal(elsewhere.route, '/workspaces/downloads');
});

// An inbox folder's review, as the server sends it: no capability, no software
// block, the host's File Janitor plan with its hire.
const inboxOffer = (hire = {}, overrides = {}) => ({
  id: 'offer-inbox',
  verdict: 'project',
  status: 'pending',
  folder: 'Downloads',
  reason: '25 files of 6 kinds, edited today',
  subject: { name: 'Downloads', kind: 'project', shape: 'inbox', is_root: true },
  recognition: { verdict: 'project', label: 'inbox folder' },
  create_available: false,
  plan: {
    digest: 'e'.repeat(64),
    lines: [
      { kind: 'agents', name: 'Hire File Curator' },
      { kind: 'workspace', name: 'A File Janitor workspace named Downloads' },
      { kind: 'folder', name: 'Linked where it is' },
      {
        kind: 'task',
        name: 'A first look: a metadata-only scan that proposes the first filing batch'
      }
    ],
    hire: { mode: 'create', agent_name: 'File Curator', role_label: 'specialist', ...hire },
    brief_default:
      'Prepare reviewable filing batches for the approved folder, explain uncertain classifications, and require approval before moving anything.',
    visual: { requirements_checked: true, builtin: 'File Janitor', model: { state: 'not_needed' } }
  },
  ...overrides
});

test('an inbox folder is a hire card for File Curator: Hire & start or Not now, no Adjust', () => {
  assert.equal(isInboxFolderOffer(inboxOffer()), true);
  const view = folderOfferView(inboxOffer());
  assert.equal(text(view.headline), 'Hire File Curator for Downloads');
  assert.deepEqual(
    view.actions.map(action => [action.id, action.label]),
    [
      ['setup', 'Hire & start'],
      ['no', 'Not now']
    ]
  );
  assert.equal(view.actions[0].oneCard, true);
  assert.equal(view.hire.adjust, null, 'no step-by-step journey to adjust');
  assert.equal(view.hire.brief.hint, 'This becomes the workspace goal. You can change it later.');
  assert.match(view.hire.brief.value, /^Prepare reviewable filing batches/);
  assert.equal(view.plan.lines.length, 4);
  assert.ok(!view.actions.some(action => action.recheck), 'no model to check again');
  // A saved File Curator is assigned, never hired twice.
  const assigned = folderOfferView(inboxOffer({ mode: 'bind', model: 'gpt-5-mini' }));
  assert.equal(text(assigned.headline), 'Assign File Curator to Downloads');
  assert.equal(assigned.actions[0].label, 'Assign & start');
  // Another kind of folder is never mistaken for one.
  assert.equal(isInboxFolderOffer(hireOffer()), false);
  assert.equal(isInboxFolderOffer(reviewedSoftware()), false);
});

test('a folder another workspace already tidies, or a refused root, is said and creates nothing', () => {
  const tidied = folderOfferView({
    ...inboxOffer(),
    plan: undefined,
    setup_unavailable_reason: 'folder_tidied',
    tidied: { name: 'Old Downloads', route: '/workspaces/old-downloads?panel=file-janitor' }
  });
  assert.equal(text(tidied.headline), 'File Curator already tidies Downloads');
  assert.deepEqual(
    tidied.actions.map(action => [action.label, action.href]),
    [['Open it', '/workspaces/old-downloads?panel=file-janitor']]
  );
  assert.equal(tidied.plan, null);
  assert.match(tidied.question, /Old Downloads already keeps this folder tidy/);
  // A route that is not a workspace page is never offered.
  const odd = folderOfferView({
    ...inboxOffer(),
    plan: undefined,
    setup_unavailable_reason: 'folder_tidied',
    tidied: { name: 'x', route: 'https://example.com/' }
  });
  assert.deepEqual(odd.actions, []);
  const refused = folderOfferView({
    ...inboxOffer(),
    plan: undefined,
    setup_unavailable_reason: 'root_refused',
    setup_unavailable_message: "Ori's own workspaces folder cannot be tidied."
  });
  assert.equal(text(refused.headline), 'Hire File Curator for Downloads (unavailable)');
  assert.equal(refused.question, "Ori's own workspaces folder cannot be tidied.");
  assert.deepEqual(refused.actions, []);
});

test('watching is offered only by a plan that names its time, off until ticked', () => {
  const none = hireView(inboxOffer().plan, 'Downloads');
  assert.equal(none.monitoring, null);
  const plan = { ...inboxOffer().plan, monitoring: { time: '09:00' } };
  const view = hireView(plan, 'Downloads');
  assert.deepEqual(view.monitoring, {
    label: 'Also watch this folder and run a daily catch-up scan at 09:00',
    hint: 'Ori notices new files after a short settle time and proposes them for review. Nothing is filed by itself.',
    line: 'Watching turned on'
  });
  assert.equal(hireView({ ...plan, monitoring: { time: 'later' } }, 'Downloads').monitoring, null);
  // The tick is sent with the click and only when chosen.
  assert.match(
    source,
    /if \(offer\.plan\?\.monitoring && monitoringChosen\(offer\)\) body\.monitoring = true;/
  );
  assert.match(template, /<input id="personalAssistantFolderMonitoring"[^>]*type="checkbox"/);
  assert.doesNotMatch(template, /id="personalAssistantFolderMonitoring"[^>]*checked/);
});

test('a folder with a project beside loose files shows why one hire leads and the other one link away', () => {
  const reason =
    '23 loose files of 6 kinds sit beside a Go module changed on Oct 20: the loose files are the bigger job.';
  const tidy = folderOfferView({
    ...inboxOffer(),
    lead: {
      leading: true,
      reason,
      alternative_id: 'candidate-1',
      alternative_label: 'hire Development Assistant for widget instead'
    }
  });
  assert.equal(tidy.leadReason, reason, 'the reason sits under "Why this suggestion?"');
  assert.deepEqual(
    tidy.actions.map(action => [action.id, action.label, action.swapCandidate || '']),
    [
      ['setup', 'Hire & start', ''],
      ['no', 'Not now', ''],
      ['lead-swap', 'Or: hire Development Assistant for widget instead', 'candidate-1']
    ]
  );
  // The hire reached by the link does not lead: no reason, a link back.
  const project = folderOfferView({
    ...reviewedSoftware(),
    lead: {
      leading: false,
      alternative_id: 'candidate-0',
      alternative_label: 'hire File Curator to tidy the loose files instead'
    }
  });
  assert.equal(project.leadReason, '');
  assert.equal(project.actions.at(-1).swapCandidate, 'candidate-0');
  assert.equal(
    project.actions.at(-1).label,
    'Or: hire File Curator to tidy the loose files instead'
  );
  // Without a lead, or once decided, there is no link.
  assert.ok(!folderOfferView(inboxOffer()).actions.some(action => action.swapCandidate));
  const decided = folderOfferView({
    ...inboxOffer(),
    status: 'awaiting_outcome',
    lead: { leading: true, reason, alternative_id: 'candidate-1', alternative_label: 'x' }
  });
  assert.ok(!decided.actions.some(action => action.swapCandidate));
  // The link only reviews the other hire; it posts nothing itself.
  assert.match(
    source,
    /window\.PersonalAssistantFolderSetup\?\.reviewCandidate\?\.\(action\.swapCandidate\)/
  );
});

test('ticking watching adds its fifth line to what the card lists, and the plan stays the same', () => {
  const offer = { ...inboxOffer(), plan: { ...inboxOffer().plan, monitoring: { time: '09:00' } } };
  assert.equal(planWithWatching(offer, false).lines.length, 4);
  const ticked = planWithWatching(offer, true);
  assert.equal(ticked.lines.length, 5);
  assert.equal(ticked.lines[4].name, 'Watching turned on');
  assert.equal(ticked.digest, offer.plan.digest, 'the tick is not in the digest');
  assert.equal(offer.plan.lines.length, 4, 'the offer itself is not changed');
  // A plan that offers no watching ignores a tick.
  assert.equal(planWithWatching(inboxOffer(), true).lines.length, 4);
  const effects = globalThis.OriFolderSetupVisual.reviewConsequences(ticked);
  assert.ok(effects.some(effect => effect.startsWith('Watching turned on')));
});

test('the confirmation line names plugins only for a plan that installs one', () => {
  assert.equal(
    confirmationSummary(folderOfferView(inboxOffer()).plan),
    'Confirmation covers the whole setup above.'
  );
  assert.equal(
    confirmationSummary({ lines: [{ kind: 'integration', name: 'Installs REAPER' }] }),
    'Confirmation covers the whole setup above, not just plugin installation.'
  );
});

test('in a conversation the inbox hire keeps its own words and closes with Keep chatting', () => {
  const view = conversationFolderOfferView(inboxOffer({}, { conversation_id: 'conversation-1' }));
  assert.doesNotMatch(view.question, /unavailable here/);
  assert.deepEqual(
    view.actions.map(action => action.label),
    ['Hire & start', 'Keep chatting']
  );
});

test('a stopped inbox run says what finished and offers its own recovery', () => {
  const stopped = (reason, state = 'failed') =>
    folderOfferView(
      inboxOffer(
        {},
        {
          status: 'awaiting_outcome',
          decision: 'yes',
          setup: {
            status: 'stopped',
            stop_reason: reason,
            lines: [
              { kind: 'agents', name: 'Hire File Curator', state: 'done' },
              {
                kind: 'workspace',
                name: 'A File Janitor workspace named Downloads',
                state: 'done'
              },
              { kind: 'folder', name: 'Linked where it is', state }
            ]
          }
        }
      )
    );
  const failed = stopped('failed');
  assert.match(failed.question, /^2 of 3 steps finished\. A step did not finish\./);
  assert.deepEqual(
    failed.actions.map(action => [action.id, action.label]),
    [
      ['retry', 'Try again'],
      ['remove', 'Remove']
    ]
  );
  const changed = stopped('plan_changed');
  assert.match(changed.question, /File Janitor will not take this folder/);
  assert.deepEqual(
    changed.actions.map(action => [action.id, action.label]),
    [
      ['repick', 'Pick it again'],
      ['remove', 'Remove']
    ]
  );
});
