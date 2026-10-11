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
  // Before a review the card offers to look, and says what looking reads. It
  // offers no setup: a software project is never set up by a bare yes.
  assert.deepEqual(actionIds(view), ['review', 'no', 'later']);
  assert.match(
    view.question,
    /^Want me to look at how widget is set up\? Review setup reads its README, instructions and build files to suggest a setup\. Nothing is run, and nothing in the folder is changed\.$/
  );
  assert.equal(view.actions.some(setsUp), false);
  assert.equal(view.plan, null);

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
  // Reviewing just this folder is still the user's to choose.
  assert.deepEqual(actionIds(web), ['review', 'enclosing', 'no', 'later']);

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

// A reviewed offer, as the server sends it after Review setup.
const reviewedOffer = (overrides = {}, software = {}) =>
  softwareOffer({
    create_available: false,
    software: {
      recipe: 'software_project',
      marker: 'Go module',
      repository: 'directory',
      setup_available: true,
      workspace_name: 'widget',
      assistant: 'Development Assistant',
      review: {
        inspected_at: '2026-10-11T09:00:00Z',
        evidence_revision: 'a'.repeat(64),
        evidence_held: true,
        purpose: 'Widget counts widgets.',
        purpose_source: 'README.md',
        stack: ['Go', 'Node.js'],
        guides: ['AGENTS.md'],
        files: ['AGENTS.md', 'README.md', 'go.mod'],
        commands: [
          { kind: 'test', command: 'make test', source: 'Makefile', declared: true },
          { kind: 'build', command: 'go build ./...', source: 'go.mod', declared: false }
        ],
        git: {
          layout: 'worktree',
          branch: 'feature/login',
          remote: 'example.invalid/acme/widget.git'
        },
        tools: [{ name: 'Herdr devflow', source: '.herdr/devflow.toml' }],
        gaps: [
          { code: 'no_declared_commands' },
          { code: 'withheld_lines', source: 'README.md' },
          { code: 'something_new', source: 'x' }
        ]
      },
      ...software
    },
    plan: {
      digest: 'd'.repeat(64),
      lines: [
        { kind: 'workspace', name: 'Creates a Code project workspace named widget' },
        { kind: 'folder', name: 'Links widget where it is', detail: 'Read-only.' },
        { kind: 'agents', name: 'Adds one development assistant, Development Assistant' },
        { kind: 'mode', name: 'Starts with understanding and planning' },
        { kind: 'task', name: 'Queues a first assessment for you to start' }
      ],
      prerequisites: [
        {
          id: 'folder',
          label: 'Folder access',
          state: 'verified',
          detail: 'Held.',
          source: 'your folder selection',
          required: true
        },
        {
          id: 'model',
          label: "Assistant's model",
          state: 'absent',
          detail: 'No model yet.',
          source: "Ori's system model setting",
          required: true,
          route: '/settings#system-model'
        },
        {
          id: 'github',
          label: 'GitHub',
          state: 'absent',
          detail: 'Not connected.',
          source: "Ori's saved connections",
          required: false,
          route: '/settings#github-account'
        },
        {
          id: 'execution_tool',
          label: 'Development tools',
          state: 'configured',
          detail: 'Configured.',
          source: "this project's files",
          required: false
        }
      ]
    },
    ...overrides
  });

test('a reviewed project shows what was read, the whole plan, and what the setup needs', () => {
  const view = folderOfferView(reviewedOffer());
  assert.equal(headlineText(view), 'Here is what I found in widget.');
  assert.equal(
    view.question,
    'Set up widget as a Code project workspace? Here is everything Set up will do:'
  );
  assert.deepEqual(actionIds(view), ['setup', 'adjust', 'review', 'no', 'later']);
  assert.deepEqual(view.actions[0], {
    id: 'setup',
    label: 'Set up',
    style: 'primary',
    oneCard: true
  });
  assert.equal(view.plan.digest, 'd'.repeat(64));
  assert.deepEqual(
    view.plan.lines.map(line => line.kind),
    ['workspace', 'folder', 'agents', 'mode', 'task']
  );
  // Nothing in a built-in plan installs anything.
  assert.equal(
    view.plan.lines.some(line => ['integration', 'provider'].includes(line.kind)),
    false
  );

  assert.deepEqual(view.evidence, [
    { label: 'It says', text: 'Widget counts widgets. (README.md)' },
    { label: 'Built with', text: 'Go, Node.js' },
    { label: 'It declares', text: 'make test. None of these was run.' },
    {
      label: 'Usual for its files',
      text: 'go build ./.... Not declared by the project, and not run.'
    },
    { label: 'Its instructions', text: 'AGENTS.md' },
    { label: 'I read', text: 'AGENTS.md, README.md, go.mod' },
    {
      label: 'Version control',
      text: 'A linked Git worktree on branch feature/login, with origin example.invalid/acme/widget.git. Other worktrees of the same repository are separate folders.'
    },
    {
      label: 'Tool configuration',
      text: 'Herdr devflow (.herdr/devflow.toml). Not checked to be installed or working.'
    },
    {
      // An unknown gap code is dropped rather than shown raw.
      label: 'Not found or not read',
      text: 'no declared build or test command; lines in README.md that looked like credentials were withheld.'
    }
  ]);

  assert.deepEqual(
    view.prerequisites.map(row => [row.id, row.stateWord, row.href, row.linkLabel]),
    [
      ['folder', 'Ready', '', ''],
      ['model', 'Needed', '/settings#system-model', 'Set up a model'],
      ['github', 'Not set (optional)', '/settings#github-account', 'Connect GitHub'],
      ['execution_tool', 'Set, not checked', '', '']
    ]
  );
  // A missing model is said twice, in words, and neither place blocks the review.
  assert.equal(view.prerequisites[1].text, 'No model yet.');
  assert.equal(view.prerequisites[1].source, "Ori's system model setting");
});

test('a long list of commands names the first few and counts the rest', () => {
  const offer = reviewedOffer();
  const commands = Array.from({ length: 11 }, (_, index) => ({
    kind: 'test',
    command: `make target-${index + 1}`,
    source: 'Makefile',
    declared: true
  }));
  const view = folderOfferView({
    ...offer,
    software: {
      ...offer.software,
      review: {
        ...offer.software.review,
        commands,
        gaps: [{ code: 'more_workflows', source: '.github/workflows' }]
      }
    }
  });
  const declares = view.evidence.find(line => line.label === 'It declares');
  assert.equal(
    declares.text,
    'make target-1, make target-2, make target-3, make target-4, make target-5, make target-6, make target-7, make target-8, and 3 more. None of these was run.'
  );
  assert.deepEqual(view.evidence.at(-1), {
    label: 'Not found or not read',
    text: 'only its first few CI workflow files were read.'
  });
});

test('a host that cannot carry the plan out shows the review and offers no Set up', () => {
  const view = folderOfferView(reviewedOffer({}, { setup_available: false }));
  assert.deepEqual(actionIds(view), ['adjust', 'review', 'no', 'later']);
  assert.equal(view.actions.some(setsUp), false);
  assert.equal(
    view.question,
    'Here is what setting up widget as a Code project workspace would do. Setting it up from this review is not available here yet; nothing below has been done.'
  );
  assert.equal(view.plan.lines.length, 5);
});

test('adjusting names swaps the actions for Save and Cancel and keeps the plan in view', () => {
  const view = folderOfferView(reviewedOffer(), { softwareAdjusting: true });
  assert.equal(view.adjusting, true);
  assert.deepEqual(actionIds(view), ['save-names', 'cancel-names']);
  assert.equal(view.actions.some(setsUp), false);
  assert.equal(view.plan.lines.length, 5);
  assert.match(view.question, /Nothing is created until you press Set up\.$/);
  // Adjusting is only for a reviewed plan.
  const unreviewed = folderOfferView(softwareOffer(), { softwareAdjusting: true });
  assert.equal(unreviewed.adjusting, undefined);
  assert.deepEqual(actionIds(unreviewed), ['review', 'no', 'later']);
});

test('a review with no plan says why and offers no setup', () => {
  for (const [reason, pattern] of [
    ['project_exists', /already has a workspace that I cannot complete from here/],
    ['assistant_unavailable', /blueprint or its assistant is not available/],
    ['setup_unavailable', /could not plan this setup just now/],
    ['', /could not plan this setup just now/]
  ]) {
    const view = folderOfferView(reviewedOffer({ plan: null, setup_unavailable_reason: reason }));
    assert.match(view.question, pattern, reason);
    assert.equal(view.plan, null);
    assert.deepEqual(view.prerequisites, []);
    assert.deepEqual(actionIds(view), ['review', 'no', 'later']);
    assert.equal(view.actions.some(setsUp), false);
    // What was read is still shown.
    assert.ok(view.evidence.length > 0);
  }
});

test('one workspace linked another way can be completed; the card says so and keeps Open', () => {
  const legacy = { workspace_id: 'ws-1', name: 'Old widget', route: '/workspaces/old-widget' };
  const before = folderOfferView(
    softwareOffer({
      create_available: false,
      software: { recipe: 'software_project', marker: 'Go module', existing: [legacy] }
    })
  );
  assert.match(
    before.question,
    /^widget is already linked to your workspace Old widget\. A review can complete that workspace for software work instead of making another\. Want me to look/
  );
  assert.deepEqual(actionIds(before), ['review', 'open-existing-0', 'no', 'later']);
  assert.equal(before.actions[1].href, '/workspaces/old-widget');

  const after = folderOfferView(reviewedOffer({}, { existing: [legacy] }));
  assert.equal(
    after.question,
    'Complete your workspace Old widget for widget? Here is everything Set up will do:'
  );
  assert.deepEqual(actionIds(after), [
    'setup',
    'adjust',
    'review',
    'open-existing-0',
    'no',
    'later'
  ]);

  // One already set up for the folder is only opened, reviewed or not.
  const done = folderOfferView(reviewedOffer({}, { existing: [{ ...legacy, software: true }] }));
  assert.deepEqual(actionIds(done), ['open-existing-0', 'no', 'later']);
  assert.equal(done.plan, null);
});

test('after a restart the review keeps its facts and asks to be read again for the rest', () => {
  const offer = reviewedOffer();
  const review = { ...offer.software.review, evidence_held: false };
  delete review.purpose;
  delete review.commands;
  const view = folderOfferView({ ...offer, software: { ...offer.software, review } });
  assert.deepEqual(view.evidence[0], {
    label: 'Read earlier',
    text: '3 files. Press Review again to see what they say.'
  });
  assert.equal(
    view.evidence.some(line => line.label === 'It says' || line.label === 'I read'),
    false
  );
  assert.equal(
    view.evidence.some(line => line.label === 'Built with'),
    true
  );
  assert.deepEqual(actionIds(view), ['setup', 'adjust', 'review', 'no', 'later']);
});

test('a partial look says so, and a prerequisite never links off the app', () => {
  const offer = reviewedOffer();
  const partial = folderOfferView({
    ...offer,
    software: {
      ...offer.software,
      review: { ...offer.software.review, partial: true, partial_reason: 'files' }
    }
  });
  assert.deepEqual(partial.evidence.at(-1), {
    label: 'A partial look',
    text: 'there were more files than I read, so some project files were not read.'
  });

  const rows = [
    {
      id: 'model',
      label: 'Model',
      state: 'absent',
      route: 'https://elsewhere.example/',
      required: true
    },
    { id: 'github', label: 'GitHub', state: 'unknown', route: '//elsewhere.example' },
    { id: 'github', label: 'GitHub', state: 'configured', route: '/settings#github-account' },
    { id: 'other', label: 'Other', state: 'absent', route: '/settings#other' },
    { id: 'model', label: 'Model', state: 'invented', route: '/settings#system-model' },
    { id: 'model', label: '', state: 'absent' }
  ];
  const view = folderOfferView({ ...offer, plan: { ...offer.plan, prerequisites: rows } });
  // An unknown state and an unlabelled row are dropped; no row links anywhere.
  assert.deepEqual(
    view.prerequisites.map(row => [row.id, row.stateWord, row.href]),
    [
      ['model', 'Needed', ''],
      ['github', 'Could not check', ''],
      ['github', 'Set, not checked', ''],
      ['other', 'Not set (optional)', '']
    ]
  );
});

test('in a conversation the review keeps its actions and gains only Keep chatting', () => {
  const unreviewed = conversationFolderOfferView(
    softwareOffer({ conversation_id: 'conversation-1', create_available: false })
  );
  assert.deepEqual(actionIds(unreviewed), ['review', 'keep-chatting']);
  assert.match(unreviewed.question, /^Want me to look at how widget is set up\?/);

  const reviewed = conversationFolderOfferView(
    reviewedOffer({ conversation_id: 'conversation-1' })
  );
  assert.deepEqual(actionIds(reviewed), ['setup', 'adjust', 'review', 'keep-chatting']);
  assert.match(reviewed.question, /^Set up widget as a Code project workspace\?/);
});

test('Review setup sends no folder, and saving names sends only names', () => {
  const reviewStart = source.indexOf('async function reviewSoftware');
  const reviewBody = source.slice(reviewStart, source.indexOf('async function saveSoftwareNames'));
  assert.match(reviewBody, /postOffer\(offer\.id, 'review', \{\}\)/);
  assert.doesNotMatch(reviewBody, /path|file_name|rel_path/);
  // The card says what is being read while it waits.
  assert.match(reviewBody, /Reading \$\{subject\}'s README, instructions and build files…/);

  const saveStart = source.indexOf('async function saveSoftwareNames');
  const saveBody = source.slice(saveStart, source.indexOf('async function chooseCandidate'));
  assert.match(
    saveBody,
    /postOffer\(offer\.id, 'adjust', \{\s*workspace_name: String\(state\.workspaceName \|\| ''\)\.trim\(\),\s*assistant_name: String\(state\.assistantName \|\| ''\)\.trim\(\)\s*\}\)/
  );
  assert.doesNotMatch(saveBody, /model|system_prompt|template|path/);
  // A refused name keeps the fields open with what was typed.
  assert.match(saveBody, /saved = true;\s*state\.softwareAdjusting = '';/);
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
