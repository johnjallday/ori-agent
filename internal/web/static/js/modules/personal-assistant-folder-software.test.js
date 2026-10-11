import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

import {
  conversationFolderOfferView,
  folderOfferView,
  softwareOfferView
} from './personal-assistant-folder.js';

const source = readFileSync(new URL('./personal-assistant-folder.js', import.meta.url), 'utf8');

// A pending offer about one recognized software project, as the server sends it.
const softwareOffer = (overrides = {}) => ({
  id: 'offer-1',
  status: 'pending',
  verdict: 'project',
  reason: '12 Go files, edited today',
  folder: 'widget',
  subject: { name: 'widget', kind: 'project', shape: 'code', marker: 'Go module', is_root: true },
  blueprint: 'code-project',
  blueprint_label: 'Code project',
  create_available: true,
  software: { recipe: 'software_project', marker: 'Go module', repository: 'directory' },
  ...overrides
});

const headlineText = view => view.headline.map(part => part.text).join('');
const actionIds = view => view.actions.map(action => action.id);
// An action that would create, confirm, or open the creator.
const setsUp = action =>
  action.create === true ||
  action.modal === true ||
  action.oneCard === true ||
  action.journey === true ||
  action.decision === 'yes';

test('a recognized software project says what marked it and that nothing was opened', () => {
  const view = folderOfferView(softwareOffer());
  assert.equal(headlineText(view), 'widget looks like a Go module.');
  assert.equal(
    view.capabilityDetail,
    'Recognized from its Go module and its Git repository. Nothing in it was opened.'
  );
  // The existing reviewed create stays the one way to set it up in this slice.
  assert.deepEqual(actionIds(view), ['setup', 'adjust', 'no', 'later']);

  const worktree = folderOfferView(
    softwareOffer({
      software: { recipe: 'software_project', marker: 'Go module', repository: 'file' }
    })
  );
  assert.match(worktree.capabilityDetail, /linked Git checkout \(a worktree or submodule\)/);
  assert.match(worktree.capabilityDetail, /Nothing in it was opened\.$/);

  const plain = folderOfferView(
    softwareOffer({
      software: { recipe: 'software_project', marker: 'Node.js package', repository: '' }
    })
  );
  assert.equal(
    plain.capabilityDetail,
    'Recognized from its Node.js package. It is not under Git. Nothing in it was opened.'
  );

  const bare = folderOfferView(
    softwareOffer({
      software: { recipe: 'software_project', marker: 'git repository', repository: 'directory' }
    })
  );
  assert.equal(
    bare.capabilityDetail,
    'Recognized from its Git repository. Nothing in it was opened.'
  );
});

test('several software projects are a question, and no setup is offered until one is chosen', () => {
  const offer = softwareOffer({
    verdict: 'mixed',
    folder: 'client-work',
    projects_count: 2,
    create_available: false,
    subject: { name: 'api', kind: 'project', shape: 'code', marker: 'Go module' },
    software: {
      recipe: 'software_project',
      marker: 'Go module',
      repository: 'directory',
      needs_candidate: true,
      candidates: [
        {
          id: 'aaaa1111',
          name: 'api',
          marker: 'Go module',
          repository: 'directory',
          current: true
        },
        { id: 'bbbb2222', name: 'site', marker: 'Node.js package', repository: 'directory' }
      ]
    }
  });
  for (const confirm of ['', 'project']) {
    const view = folderOfferView(offer, { confirm });
    assert.equal(headlineText(view), 'client-work holds 2 software projects.');
    assert.match(
      view.question,
      /^Which one do you want to set up\? Nothing is set up until you choose/
    );
    assert.equal(view.plan, null);
    assert.deepEqual(actionIds(view), ['candidate-0', 'candidate-1', 'later']);
    assert.deepEqual(
      view.actions.slice(0, 2).map(action => [action.label, action.candidate]),
      [
        ['api · Go module', 'aaaa1111'],
        ['site · Node.js package', 'bbbb2222']
      ]
    );
    assert.equal(view.actions.some(setsUp), false, 'a setup was offered before the choice');
    // Neither candidate is favoured: the card does not guess.
    assert.equal(
      view.actions.some(action => action.style === 'primary'),
      false
    );
  }
});

test('a choice the server could not list offers no setup on a guess', () => {
  const view = folderOfferView(
    softwareOffer({
      create_available: false,
      software: {
        recipe: 'software_project',
        marker: 'Go module',
        needs_candidate: true,
        candidates: [{ id: 'aaaa1111', name: 'api' }, { id: '', name: 'site' }, null]
      }
    })
  );
  assert.equal(headlineText(view), 'widget holds more than one software project.');
  assert.deepEqual(actionIds(view), ['another', 'later']);
  assert.equal(view.actions.some(setsUp), false);
});

test('a folder that already has a workspace opens it and offers no second setup', () => {
  const view = folderOfferView(
    softwareOffer({
      create_available: false,
      software: {
        recipe: 'software_project',
        marker: 'Go module',
        repository: 'directory',
        existing: [
          { workspace_id: 'ws-1', name: 'Widget', route: '/workspaces/widget', software: true }
        ]
      }
    })
  );
  assert.equal(headlineText(view), 'widget already has a workspace.');
  assert.equal(view.question, 'It is set up as Widget. Open it instead of setting up another.');
  assert.equal(view.plan, null);
  assert.deepEqual(actionIds(view), ['open-existing-0', 'no', 'later']);
  assert.deepEqual(view.actions[0], {
    id: 'open-existing-0',
    label: 'Open Widget',
    style: 'primary',
    href: '/workspaces/widget'
  });
  assert.equal(view.actions.some(setsUp), false);
});

test('several workspaces on one folder are each offered, and none is chosen for the user', () => {
  const view = folderOfferView(
    softwareOffer({
      create_available: false,
      software: {
        recipe: 'software_project',
        marker: 'Rust crate',
        existing: [
          { workspace_id: 'ws-a', name: 'Gauge', route: '/workspaces/gauge' },
          { workspace_id: 'ws-b', name: 'Gauge tools', route: '/workspaces/gauge-tools' }
        ]
      }
    })
  );
  assert.equal(headlineText(view), 'widget already has 2 workspaces.');
  assert.match(view.question, /^Open the one you want\. I will not set up another/);
  assert.deepEqual(
    view.actions.filter(action => action.href).map(action => [action.label, action.href]),
    [
      ['Open Gauge', '/workspaces/gauge'],
      ['Open Gauge tools', '/workspaces/gauge-tools']
    ]
  );
  assert.equal(view.actions.some(setsUp), false);
});

test('a workspace made for a folder since replaced is named as a different folder', () => {
  const view = folderOfferView(
    softwareOffer({
      create_available: false,
      software: {
        recipe: 'software_project',
        marker: 'Go module',
        existing: [
          {
            workspace_id: 'ws-1',
            name: 'Widget',
            route: '/workspaces/widget',
            software: true,
            replaced: true
          }
        ]
      }
    })
  );
  assert.match(
    view.question,
    /The folder here now is a different one, so I have not connected them\./
  );
  assert.equal(view.actions.some(setsUp), false);
});

test('an unreadable or unlinkable workspace answer offers no setup either', () => {
  const unknown = folderOfferView(
    softwareOffer({
      create_available: false,
      software: { recipe: 'software_project', marker: 'Go module', existing_unavailable: true }
    })
  );
  assert.match(unknown.question, /^I could not check whether this folder already has a workspace/);
  assert.deepEqual(actionIds(unknown), ['no', 'later']);

  // A workspace is reported, but its route is not one this page may open.
  for (const route of [
    'https://elsewhere.example/x',
    '//elsewhere.example',
    'javascript:alert(1)',
    ''
  ]) {
    const view = folderOfferView(
      softwareOffer({
        create_available: false,
        software: {
          recipe: 'software_project',
          marker: 'Go module',
          existing: [{ workspace_id: 'ws-1', name: 'Widget', route }]
        }
      })
    );
    assert.equal(
      view.actions.some(action => action.href),
      false,
      `linked ${route}`
    );
    assert.equal(view.actions.some(setsUp), false, `offered setup beside ${route}`);
  }
});

test('a folder inside a larger repository is explained and the user is asked for that folder', () => {
  const web = folderOfferView(
    softwareOffer({
      folder: 'web',
      subject: {
        name: 'web',
        kind: 'project',
        shape: 'code',
        marker: 'Node.js package',
        is_root: true
      },
      software: { recipe: 'software_project', marker: 'Node.js package', repository: '' },
      enclosing: { levels: 1, name: 'widget' }
    })
  );
  assert.match(
    web.capabilityDetail,
    /This folder is inside the repository widget \(the folder above it\)\. To set up the whole project, show me widget instead\.$/
  );
  const instead = web.actions.find(action => action.id === 'enclosing');
  assert.deepEqual(instead, {
    id: 'enclosing',
    label: 'Show me widget instead',
    style: 'link',
    open: true
  });
  // Setting up just this folder is still the user's to choose.
  assert.equal(
    web.actions.some(action => action.create === true),
    true
  );

  // A folder Ori has nothing to offer for still says where it sits, once.
  const inner = folderOfferView({
    id: 'offer-2',
    status: 'closed',
    verdict: 'empty',
    folder: 'count',
    subject: { name: 'count', kind: 'tidy' },
    enclosing: { levels: 2, name: 'widget' }
  });
  assert.equal(
    inner.capabilityDetail,
    'This folder is inside the repository widget (2 folders up). To set up the whole project, show me widget instead.'
  );
  assert.deepEqual(actionIds(inner), ['another']);

  // A malformed hint adds nothing.
  for (const enclosing of [{ levels: 0, name: 'widget' }, { levels: 1, name: ' ' }, null]) {
    const view = folderOfferView(softwareOffer({ enclosing }));
    assert.equal(
      view.actions.some(action => action.id === 'enclosing'),
      false
    );
    assert.doesNotMatch(view.capabilityDetail, /inside the repository/);
  }
});

test('a software folder the server no longer holds, or that was replaced, asks for a fresh pick', () => {
  const view = folderOfferView(softwareOffer({ needs_pick: true, create_available: false }));
  assert.equal(
    view.question,
    'Ori can no longer use widget: the server was restarted, or the folder there is not the one you showed me. Pick the folder again to carry on.'
  );
  assert.deepEqual(actionIds(view), ['repick', 'no', 'later']);
  assert.equal(view.actions.some(setsUp), false);
});

test('in a conversation an existing workspace keeps its explanation and gains only Keep chatting', () => {
  const view = conversationFolderOfferView(
    softwareOffer({
      conversation_id: 'conversation-1',
      create_available: false,
      software: {
        recipe: 'software_project',
        marker: 'Go module',
        existing: [
          { workspace_id: 'ws-a', name: 'Widget', route: '/workspaces/widget' },
          { workspace_id: 'ws-b', name: 'Widget two', route: '/workspaces/widget-two' }
        ]
      }
    })
  );
  assert.match(view.question, /^Open the one you want\./);
  assert.deepEqual(actionIds(view), ['open-existing-0', 'open-existing-1', 'keep-chatting']);
  assert.equal(view.actions.some(setsUp), false);
});

test('a decided or non-software offer is left exactly as it was', () => {
  const resolved = softwareOffer({ status: 'resolved', decision: 'yes' });
  assert.equal(folderOfferView(resolved).capabilityDetail, undefined);
  const thesis = {
    id: 'offer-3',
    status: 'pending',
    verdict: 'project',
    folder: 'Thesis',
    subject: { name: 'Thesis', kind: 'project', shape: 'manuscript', marker: 'LaTeX manuscript' },
    create_available: true
  };
  const view = folderOfferView(thesis);
  assert.equal(view.capabilityDetail, undefined);
  assert.deepEqual(actionIds(view), ['setup', 'adjust', 'no', 'later']);
  // The helper alone never invents a software state from an offer without one.
  assert.deepEqual(
    actionIds(softwareOfferView(thesis, view, { subject: 'Thesis', folder: 'Thesis' })),
    ['setup', 'adjust', 'no', 'later']
  );
});

test('choosing a candidate sends its listed id and nothing that names a folder', () => {
  const start = source.indexOf('async function chooseCandidate');
  const body = source.slice(start, source.indexOf('export function folderDecisionProgress', start));
  assert.ok(start > 0 && body.length > 0);
  assert.match(
    body,
    /postOffer\(offer\.id, 'candidate', \{\s*candidate_id: action\.candidate\s*\}\)/
  );
  assert.doesNotMatch(body, /path|rel_path|folder_key|window\.location/);
});
