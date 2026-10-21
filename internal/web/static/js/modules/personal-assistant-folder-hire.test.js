import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

import { folderOfferView, hireView } from './personal-assistant-folder.js';

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
      ['setup', 'Hire and start'],
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
    ['Hire and start', 'Continue setup']
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
    ['Hire and start', 'Not now', 'Review again']
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
    /<textarea id="personalAssistantFolderBrief"[^>]*maxlength="300"[\s\S]*?aria-describedby="personalAssistantFolderBriefHint"/
  );
  assert.match(template, /This becomes the workspace goal\. You can change it later\./);
});
