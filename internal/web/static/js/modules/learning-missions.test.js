import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import {
  LearningMissions,
  createLearningAPI,
  featuredLearning,
  learningTicketHref
} from './learning-missions.js';
import { LearningTicketReview } from './learning-mission-review.js';

function fixtureDOM() {
  const doc = { activeElement: null };
  doc.createElement = tag => {
    const node = {
      tag,
      ownerDocument: doc,
      children: [],
      dataset: {},
      attributes: {},
      listeners: {},
      value: '',
      append(...children) {
        this.children.push(...children);
      },
      replaceChildren(...children) {
        this.children = children;
        this._text = '';
      },
      addEventListener(type, run) {
        this.listeners[type] = run;
      },
      setAttribute(key, value) {
        this.attributes[key] = value;
      },
      removeAttribute(key) {
        delete this.attributes[key];
      },
      contains(value) {
        return this === value || this.children.some(child => child.contains(value));
      },
      focus() {
        doc.activeElement = this;
      },
      querySelector(selector) {
        return (
          this.children.find(child => child.tag === selector) ||
          this.children.map(child => child.querySelector(selector)).find(Boolean)
        );
      },
      get textContent() {
        return (this._text || '') + this.children.map(child => child.textContent).join(' ');
      },
      set textContent(value) {
        this._text = String(value);
        this.children = [];
      },
      set innerHTML(_value) {
        throw new Error('Learning must render untrusted content as text');
      },
      get options() {
        return this.children;
      }
    };
    return node;
  };
  return doc.createElement('section');
}
const status = instances => ({
  owner: { revision: 3, preferences: { mode: 'on_request' } },
  instances
});
const instance = (state = 'active') => ({
  id: 'mission',
  state,
  revision: 2,
  definition: {
    goal: '<img src=x onerror=alert(1)>',
    scope: 'Only supplied text',
    why: 'An explicit choice',
    studio_id: 'ws'
  },
  progress: {}
});
const response = body => ({ ok: true, status: 200, json: async () => body });

test('one active learning contract takes priority over one offer; no terminal recommendation feed', () => {
  const offer = instance('offered'),
    active = instance();
  assert.equal(featuredLearning(status([offer, active])), active);
  assert.equal(featuredLearning(status([instance('completed')])), null);
  assert.equal(
    learningTicketHref({ ticket_href: '/workspaces/ws%2Fx/task/t%3Fx' }),
    '/workspaces/ws%2Fx/task/t%3Fx'
  );
  assert.equal(learningTicketHref(instance()), '');
  assert.equal(learningTicketHref({ ticket_href: 'javascript:alert(1)' }), '');
  assert.equal(learningTicketHref({ ticket_href: '//outside.test/task/x' }), '');
});

test('board hydration renders hostile prose as text and makes only the saved-state GET', async () => {
  const root = fixtureDOM(),
    calls = [];
  const board = new LearningMissions(
    root,
    createLearningAPI(async (url, options) => {
      calls.push([url, options]);
      return response(status([instance('offered')]));
    })
  );
  await board.refresh();
  assert.equal(calls.length, 1);
  assert.equal(calls[0][1].method, 'GET');
  assert.equal(calls[0][0], '/api/learning-missions');
  assert.match(root.textContent, /<img src=x onerror=alert\(1\)>/);
  assert.match(root.textContent, /Start learning — no execution/);
  assert.match(root.textContent, /Not now/);
  assert.equal(root.ownerDocument.activeElement, null);
});

test('capacity errors explain explicit clearing without replaying a mutation or rendering server content', async () => {
  const calls = [];
  const api = createLearningAPI(
    async (path, options) => {
      calls.push([path, options]);
      return {
        ok: false,
        status: 429,
        json: async () => ({
          error: 'storage_limit_reached',
          private_note: 'NEVER_RENDER_SERVER_CONTENT'
        })
      };
    },
    () => 'capacity-test'
  );
  await assert.rejects(
    api.mutate('/api/learning-missions/proposals', 3, {}),
    error =>
      error.status === 429 &&
      /Actual Tickets and Runs remain/.test(error.message) &&
      /does not reset recent call limits/.test(error.message) &&
      !error.message.includes('NEVER_RENDER')
  );
  assert.equal(calls.length, 1);
  assert.equal(calls[0][1].method, 'POST');
});

test('preference editing is read-only until explicit scoped automatic consent is saved', async () => {
  const root = fixtureDOM(),
    writes = [],
    reads = [];
  const saved = {
    ...status([]),
    model: { provider: 'test', model: 'bounded-model', fingerprint: 'reviewed-destination' }
  };
  const board = new LearningMissions(root, {
    get: async path => {
      reads.push(path);
      return path === '/api/workspaces'
        ? { workspaces: [{ id: 'ws', name: '<script>Workshop</script>' }] }
        : saved;
    },
    mutate: async (...args) => {
      writes.push(args);
      return { operation: { status: 'succeeded' } };
    }
  });
  await board.refresh();
  await board.preferencesForm();
  assert.equal(writes.length, 0);
  assert.deepEqual(reads, ['/api/learning-missions', '/api/workspaces']);
  assert.equal(root.ownerDocument.activeElement.textContent, 'Your learning preferences');
  assert.equal(
    named(root, 'Approve automatic suggestions with this model and these scopes').checked,
    false
  );
  named(root, 'Coaching mode').value = 'automatic';
  named(root, 'Include <script>Workshop</script>').checked = true;
  await named(root, 'Save learning preferences').listeners.click();
  assert.equal(writes.length, 0);
  assert.match(root.textContent, /Review and approve/);
  named(root, 'Approve automatic suggestions with this model and these scopes').checked = true;
  named(root, 'Observe new bounded activity and reviewed work in these workspaces').checked = true;
  named(root, 'Keep full optional coaching in guided mode').checked = true;
  await named(root, 'Save learning preferences').listeners.click();
  assert.equal(writes.length, 1);
  assert.equal(writes[0][0], '/api/learning-missions/preferences');
  assert.deepEqual(writes[0][2].workspace_ids, ['ws']);
  assert.equal(writes[0][2].model_fingerprint, 'reviewed-destination');
  assert.equal(writes[0][2].consent_version, 1);
  assert.equal(writes[0][2].observe, true);
  assert.equal(writes[0][2].full_guidance, true);
  assert.equal(Object.hasOwn(writes[0][2], 'scope_bindings'), false);
});

test('coaching off keeps the real Ticket link and stop control but quiets practice prompts', async () => {
  const root = fixtureDOM(),
    row = instance();
  row.ticket_href = '/workspaces/workshop/task/ticket';
  const saved = status([row]);
  saved.owner.preferences.mode = 'off';
  const board = new LearningMissions(root, { get: async () => saved });
  await board.refresh();
  assert.match(root.textContent, /Coaching is off/);
  assert.match(root.textContent, /Open existing Ticket/);
  assert.match(root.textContent, /Stop mission — keep actual work/);
  assert.doesNotMatch(root.textContent, /Prepare a draft Ticket|Ori’s criteria|<img/);
  assert.ok(named(root, 'Learning preferences'));
});

test('clearing preferences requires its own confirmation and never clears practice history', async () => {
  const root = fixtureDOM(),
    writes = [];
  const board = new LearningMissions(root, {
    get: async path => (path === '/api/workspaces' ? { workspaces: [] } : status([])),
    mutate: async (...args) => {
      writes.push(args);
      return { operation: { status: 'succeeded' } };
    }
  });
  await board.refresh();
  await board.preferencesForm();
  await named(root, 'Clear learning preferences').listeners.click();
  assert.equal(writes.length, 0);
  assert.match(root.textContent, /Confirm which learning preferences/);
  named(
    root,
    'Confirm clearing preferences, scope consent and exclusions — keep recorded practice'
  ).checked = true;
  await named(root, 'Clear learning preferences').listeners.click();
  assert.deepEqual(writes, [['/api/learning-missions/clear', 3, { scope: 'preferences' }]]);
});

test('practice inspection is explicit, read-only and keeps unknown support distinct', async () => {
  const root = fixtureDOM(),
    reads = [],
    key = 'a'.repeat(64);
  const saved = {
    ...status([]),
    competencies: {
      coverage: 'available',
      items: [
        {
          competency: 'evaluate',
          state: 'support_unknown',
          attempts: 1,
          guided_attempts: 0,
          independent_attempts: 0,
          unknown_support_attempts: 1,
          evidence_keys: [key]
        }
      ]
    }
  };
  const board = new LearningMissions(root, {
    get: async path => {
      reads.push(path);
      return path === '/api/learning-missions'
        ? saved
        : {
            review: { decision: 'reject', reason: 'incorrect', note: '<script>note</script>' },
            result_text: '<script>exact result</script>',
            input_text: 'Exact input'
          };
    },
    mutate: async () => {
      throw new Error('Inspection must not mutate');
    }
  });
  await board.refresh();
  board.practiceForm();
  assert.deepEqual(reads, ['/api/learning-missions']);
  assert.match(root.textContent, /Practice recorded · support unknown/);
  await named(root, 'Inspect evaluate review 1').listeners.click();
  assert.deepEqual(reads, ['/api/learning-missions', `/api/learning-missions/evidence/${key}`]);
  assert.match(root.textContent, /<script>exact result<\/script>/);
  assert.match(root.textContent, /<script>note<\/script>/);
  assert.equal(
    root.ownerDocument.activeElement.textContent,
    'Saved decision · exact reviewed result'
  );
});

test('host rationale is literal and independent of generated relevance prose', async () => {
  const root = fixtureDOM(),
    row = instance('offered');
  row.definition.host_signals = ['no_recorded_practice_for_structure', 'toString', '<script>'];
  const board = new LearningMissions(root, { get: async () => status([row]) });
  await board.refresh();
  assert.match(root.textContent, /Other experience is unknown/);
  assert.doesNotMatch(root.textContent, /native code|<script>/);
});

test('offline inspection gives a recovery step without exposing raw network details', async () => {
  const api = createLearningAPI(async () => {
    throw new Error('NEVER_EXPOSE_RAW_DESTINATION');
  });
  await assert.rejects(
    api.get('/api/learning-missions'),
    error =>
      /connection and refresh saved learning/.test(error.message) &&
      !error.message.includes('NEVER_EXPOSE')
  );
});

test('lost mutation response recovers by the same operation ID without replaying effects', async () => {
  const calls = [];
  const api = createLearningAPI(
    async (path, options) => {
      calls.push([path, options]);
      if (options.method === 'POST') throw new Error('connection lost');
      return response({ operation: { id: 'reserved', status: 'succeeded' } });
    },
    () => 'reserved'
  );
  const out = await api.mutate('/api/learning-missions/proposals', 2, { studio_id: 'ws' });
  assert.equal(out.operation.id, 'reserved');
  assert.deepEqual(
    calls.map(([path, options]) => [path, options.method]),
    [
      ['/api/learning-missions/proposals', 'POST'],
      ['/api/learning-missions/operations/reserved', 'GET']
    ]
  );
  assert.deepEqual(JSON.parse(calls[0][1].body), {
    studio_id: 'ws',
    expected_revision: 2,
    operation_id: 'reserved'
  });
});

test('unknown Ticket start never automatically retries or creates a learning operation', async () => {
  let calls = 0;
  const api = createLearningAPI(async () => {
    calls++;
    throw new Error('unknown start');
  });
  await assert.rejects(api.start('/ticket/start', 'reviewed'), /request could not be confirmed/);
  assert.equal(calls, 1);
});

test('Ticket hydration performs GETs only; exact review text is not parsed as HTML', async () => {
  const root = fixtureDOM(),
    calls = [];
  root.dataset = { studioId: 'ws', ticketId: 'ticket' };
  const row = instance();
  row.progress = { ticket_id: 'ticket', run_id: 'run' };
  const api = createLearningAPI(async (path, options) => {
    calls.push([path, options.method]);
    if (path === '/api/learning-missions') return response(status([row]));
    if (path.endsWith('/execution'))
      return response({ executor: {}, input: 'input', disclosure: 'No tools' });
    return response({ state: 'review' });
  });
  const view = new LearningTicketReview(root, api);
  await view.refresh();
  assert.ok(calls.every(([, method]) => method === 'GET'));
  assert.match(root.textContent, /Inspect the exact result/);
  view.renderReview({
    instance: row,
    review: { id: 'nonce', result: { run_id: 'run', artifact_id: 'artifact' } },
    result_text: '<script>unsafe()</script>'
  });
  assert.match(root.textContent, /<script>unsafe\(\)<\/script>/);
  assert.equal(root.ownerDocument.activeElement.tag, 'h3');
});

test('new hosts preserve the finite onboarding owner and make execution review visible before a result exists', () => {
  const home = readFileSync(
    new URL('../../../templates/components/dashboard.tmpl', import.meta.url),
    'utf8'
  );
  const ticket = readFileSync(
    new URL('../../../templates/pages/workspace-task.tmpl', import.meta.url),
    'utf8'
  );
  assert.match(home, /Getting Started/);
  assert.equal((home.match(/id="learningMissions"/g) || []).length, 1);
  assert.match(home, /id="questLog"/);
  assert.ok(
    ticket.indexOf('id="learningTicketReview"') < ticket.indexOf('id="workspace-task-output-card"')
  );
});

function named(root, name) {
  if (
    root.attributes['aria-label'] === name ||
    (root.tag === 'button' && root.textContent === name)
  )
    return root;
  return root.children.map(child => named(child, name)).find(Boolean);
}

test('personalization is opt-in per explicit request; pending replies never trigger a second call', async () => {
  const root = fixtureDOM(),
    writes = [],
    reads = [];
  const saved = {
    ...status([]),
    model: { provider: 'test', model: 'bounded-model', fingerprint: 'reviewed-destination' }
  };
  const board = new LearningMissions(root, {
    get: async path => {
      reads.push(path);
      return path === '/api/workspaces'
        ? { workspaces: [{ id: 'ws', name: '<script>name</script>' }] }
        : saved;
    },
    mutate: async (...args) => {
      writes.push(args);
      return { operation: { status: 'pending' } };
    }
  });
  await board.refresh();
  await board.chooseWorkspace();
  const consent = named(root, 'Personalize this request with the configured Default model');
  assert.equal(consent.checked, false);
  assert.equal(writes.length, 0);
  assert.match(root.textContent, /test \/ bounded-model/);
  assert.match(root.textContent, /No file contents, conversations or result bodies/);
  named(root, 'Workspace').value = 'ws';
  named(root, 'Practice structure').value = 'improve_result';
  consent.checked = true;
  await named(root, 'Request a practice').listeners.click();
  assert.equal(writes.length, 1);
  assert.deepEqual(writes[0], [
    '/api/learning-missions/proposals',
    3,
    {
      studio_id: 'ws',
      structure: 'improve_result',
      personalize: true,
      consent_version: 1,
      model_fingerprint: 'reviewed-destination'
    }
  ]);
  assert.match(board.message.textContent, /No automatic retry/);
  await board.refresh();
  assert.equal(writes.length, 1);
  assert.ok(reads.every(path => path === '/api/learning-missions' || path === '/api/workspaces'));
});

test('unavailable personalization leaves an explicit model-free generic request', async () => {
  const root = fixtureDOM(),
    writes = [];
  const board = new LearningMissions(root, {
    get: async path => (path === '/api/workspaces' ? { workspaces: [{ id: 'ws' }] } : status([])),
    mutate: async (...args) => {
      writes.push(args);
      return { operation: { status: 'succeeded' } };
    }
  });
  await board.refresh();
  await board.chooseWorkspace();
  assert.equal(
    named(root, 'Personalize this request with the configured Default model').disabled,
    true
  );
  named(root, 'Workspace').value = 'ws';
  await named(root, 'Request a practice').listeners.click();
  assert.deepEqual(writes[0][2], { studio_id: 'ws', structure: '' });
});

test('reuse discloses original input and refuses unchanged input or a missing explanation before mutation', async () => {
  const root = fixtureDOM(),
    writes = [];
  const row = instance();
  row.definition.structure = 'repeat_success';
  const board = new LearningMissions(root, {
    get: async path =>
      path.endsWith('/original')
        ? {
            result: { run_id: 'old-run', artifact_id: 'old-artifact' },
            input: 'Old input',
            result_text: '<b>Old result</b>'
          }
        : path === '/api/learning-missions'
          ? status([row])
          : { agent_instances: [{ name: 'Writer' }] },
    mutate: async (...args) => {
      writes.push(args);
      return {};
    }
  });
  await board.refresh();
  await board.prepareForm(row);
  assert.match(root.textContent, /<b>Old result<\/b>/);
  const input = named(root, 'New text and drafting requirements (maximum 4 KiB)');
  const change = named(root, 'What is materially different? (required, 500 characters)');
  named(root, 'Assigned agent').value = 'Writer';
  const save = named(root, 'Save reviewed new input as a Ticket');
  input.value = ' Old   input ';
  change.value = 'This claim is insufficient';
  await save.listeners.click();
  assert.equal(writes.length, 0);
  assert.match(board.message.textContent, /Whitespace changes/);
  input.value = 'A different audience and event';
  change.value = '';
  await save.listeners.click();
  assert.equal(writes.length, 0);
  change.value = 'A new audience and event';
  await save.listeners.click();
  assert.deepEqual(writes[0][2], {
    instance_revision: 2,
    input: input.value,
    assignee: 'Writer',
    input_change: change.value
  });
});

test('revision comparison shows both exact results and requires a change report, not a positive rating', async () => {
  const root = fixtureDOM(),
    writes = [];
  const row = instance();
  row.definition.structure = 'improve_result';
  row.progress.input = 'Add a start time';
  const view = new LearningTicketReview(root, {
    mutate: async (...args) => {
      writes.push(args);
    }
  });
  view.status = status([row]);
  view.refresh = async () => {};
  view.renderReview({
    instance: row,
    review: {
      id: 'nonce',
      original: { run_id: 'old', artifact_id: 'old-artifact' },
      result: { run_id: 'new', artifact_id: 'new-artifact' }
    },
    original_text: '<script>original</script>',
    result_text: '<script>revision</script>'
  });
  assert.match(root.textContent, /<script>original<\/script>/);
  assert.match(root.textContent, /<script>revision<\/script>/);
  assert.match(root.textContent, /Add a start time/);
  assert.equal(named(root, 'Selected original result').tabIndex, 0);
  named(root, 'Your decision').value = 'reject';
  named(root, 'Reason').value = 'missing_detail';
  const save = named(root, 'Record this result-specific decision');
  await save.listeners.click();
  assert.equal(writes.length, 0);
  named(root, 'What changed? (required, 500 characters)').value =
    'Adds a time but still omits the place.';
  await save.listeners.click();
  assert.deepEqual(writes[0][2], {
    instance_revision: 2,
    action: 'submit',
    review_id: 'nonce',
    decision: 'reject',
    reason: 'missing_detail',
    note: '',
    change: 'Adds a time but still omits the place.'
  });
});

test('ordinary result review records a decision without a mission or assumed guidance', async () => {
  const root = fixtureDOM(),
    writes = [];
  const view = new LearningTicketReview(root, {
    mutate: async (...args) => {
      writes.push(args);
    }
  });
  view.status = status([]);
  view.refresh = async () => {};
  view.renderReview({
    observation: { id: 'observed', revision: 4 },
    review: { id: 'nonce', result: { run_id: 'exact', artifact_id: 'artifact' } },
    observed_input: '<b>reviewed input</b>',
    result_text: '<script>exact result</script>'
  });
  assert.match(root.textContent, /No mission is created/);
  assert.match(root.textContent, /<b>reviewed input<\/b>/);
  assert.equal(writes.length, 0);
  named(root, 'Your decision').value = 'reject';
  named(root, 'Reason').value = 'incorrect';
  await named(root, 'Record this result-specific decision').listeners.click();
  assert.deepEqual(writes[0], [
    '/api/learning-missions/observations/observed/review',
    3,
    {
      observed_revision: 4,
      action: 'submit',
      review_id: 'nonce',
      decision: 'reject',
      reason: 'incorrect',
      note: ''
    }
  ]);
  assert.match(view.message.textContent, /No mission was created/);
});

test('existing ordinary reviews win over new offers without generating or preparing a nonce', async () => {
  const root = fixtureDOM(),
    reads = [];
  const board = new LearningMissions(root, {
    get: async path => {
      reads.push(path);
      return {
        ...status([instance('offered')]),
        observations: [
          { id: 'observed', ready: true, ticket_href: '/workspaces/practice/task/ticket' }
        ]
      };
    },
    mutate: async () => {
      throw new Error('read must not mutate');
    }
  });
  await board.refresh();
  assert.match(root.textContent, /Review existing work first/);
  assert.doesNotMatch(root.textContent, /Start learning — no execution/);
  assert.deepEqual(reads, ['/api/learning-missions']);
});

test('unsupported offers expose only dismissal, not a current structure or inferred completion', async () => {
  const root = fixtureDOM(),
    row = instance('offered'),
    writes = [];
  row.definition = {
    structure: 'delegate_inspect',
    version: 99,
    goal: 'Learning evidence unavailable'
  };
  row.progress.blocker = 'definition_unavailable';
  const board = new LearningMissions(root, {
    get: async () => status([row]),
    mutate: async (...args) => writes.push(args)
  });
  await board.refresh();
  assert.doesNotMatch(
    root.textContent,
    /Review a small input|Start learning — no execution|Not interested|Not now/
  );
  await named(root, 'Dismiss unsupported learning offer — keep actual work').listeners.click();
  assert.deepEqual(writes, [
    [
      '/api/learning-missions/instances/mission/decisions',
      3,
      { instance_revision: 2, decision: 'stop' }
    ]
  ]);
});

test('brief optional coaching explains bounded path evidence without replacing full criteria or claiming independence', async () => {
  const root = fixtureDOM(),
    row = instance();
  row.definition.coaching_depth = 'brief';
  row.definition.personalized = true;
  row.definition.guidance = 'guided';
  row.definition.hints = ['Compare the supplied text and result'];
  const board = new LearningMissions(root, { get: async () => status([row]) });
  await board.refresh();
  assert.match(root.textContent, /Guided · concise optional coaching/);
  assert.match(root.textContent, /not a mastery claim/);
  assert.match(root.textContent, /Keep full coaching/);
  assert.match(root.textContent, /Compare the supplied text and result/);
  assert.match(root.textContent, /Ori’s criteria/);
});

test('local diagnostics are explicit, non-billing counts and history clear needs its own confirmation', async () => {
  const root = fixtureDOM(),
    reads = [],
    writes = [];
  const board = new LearningMissions(root, {
    get: async path => {
      reads.push(path);
      return path.endsWith('/diagnostics')
        ? {
            coverage: 'partial',
            offers: 2,
            design_calls: 1,
            unknown_usage_calls: 1,
            known_tokens: 0,
            prompt: 'NEVER_RENDER_RAW_CONTENT',
            practice: { coverage: 'unknown', items: [] }
          }
        : status([]);
    },
    mutate: async (...args) => {
      writes.push(args);
    }
  });
  await board.refresh();
  assert.deepEqual(reads, ['/api/learning-missions']);
  await named(root, 'Local learning activity').listeners.click();
  assert.match(root.textContent, /not a provider bill/);
  assert.match(root.textContent, /Coverage is partial/);
  assert.match(root.textContent, /Practice evidence is unavailable/);
  assert.doesNotMatch(root.textContent, /NEVER_RENDER_RAW_CONTENT/);
  await named(root, 'Clear learning history').listeners.click();
  assert.equal(writes.length, 0);
  named(
    root,
    'Confirm clearing learning history and recorded practice — keep actual work'
  ).checked = true;
  await named(root, 'Clear learning history').listeners.click();
  assert.deepEqual(writes, [['/api/learning-missions/clear', 3, { scope: 'history' }]]);
  assert.match(
    board.message.textContent,
    /Actual work, preferences and recent call-limit accounting remain/
  );
});

test('automatic provenance is host-labelled and legacy consent stays paused without a read-triggered call', async () => {
  const root = fixtureDOM(),
    reads = [];
  const offer = instance('offered');
  offer.definition.personalized = true;
  offer.definition.automatic = true;
  const saved = status([offer]);
  saved.owner.preferences.mode = 'automatic';
  const board = new LearningMissions(root, {
    get: async path => {
      reads.push(path);
      return saved;
    },
    mutate: async () => {
      throw new Error('reads must never generate');
    }
  });
  await board.refresh();
  assert.match(root.textContent, /Offered after a new recorded review/);
  assert.match(root.textContent, /Automatic suggestions are paused/);
  assert.deepEqual(reads, ['/api/learning-missions']);
});

test('replacement preview is explicit and leaves a safe current-work handoff', async () => {
  const root = fixtureDOM();
  const current = instance();
  current.ticket_href = '/workspaces/practice/task/current';
  const preview = instance('offered');
  preview.id = 'replacement';
  preview.definition.replaces_id = current.id;
  const saved = status([current, preview]);
  const writes = [];
  const board = new LearningMissions(root, {
    get: async () => saved,
    mutate: async (...args) => {
      writes.push(args);
    }
  });
  await board.refresh();
  assert.equal(featuredLearning(saved), preview);
  assert.match(root.textContent, /Current mission: <img/);
  assert.match(root.textContent, /Existing Tickets and Runs continue/);
  assert.ok(named(root, 'Accept replacement — keep actual work'));
  assert.equal(writes.length, 0);
  await named(root, 'Keep current mission').listeners.click();
  assert.deepEqual(writes[0], [
    '/api/learning-missions/instances/replacement/decisions',
    3,
    { instance_revision: 2, decision: 'discard' }
  ]);
});

test('smaller preview and byte-limited input require separate explicit actions', async () => {
  const root = fixtureDOM(),
    writes = [];
  const row = instance();
  row.definition.input_limit = 512;
  const board = new LearningMissions(root, {
    get: async path =>
      path.startsWith('/api/workspaces/')
        ? { agent_instances: [{ name: 'Writer' }] }
        : status([row]),
    mutate: async (...args) => {
      writes.push(args);
    }
  });
  await board.refresh();
  await board.adjustForm(row, 'smaller');
  assert.equal(writes.length, 0);
  await named(root, 'Preview smaller practice').listeners.click();
  assert.deepEqual(writes[0], [
    '/api/learning-missions/instances/mission/alternatives',
    3,
    { instance_revision: 2, kind: 'smaller' }
  ]);
  row.definition.input_limit = 256;
  await board.prepareForm(row);
  const input = named(root, 'Text and drafting requirements (maximum 256 bytes)');
  input.value = 'é'.repeat(129);
  named(root, 'Assigned agent').value = 'Writer';
  await named(root, 'Save reviewed input as a Ticket').listeners.click();
  assert.equal(writes.length, 1);
  assert.match(board.message.textContent, /256 bytes/);
});

test('revision execution still discloses both inputs after learning has been cleared', () => {
  const root = fixtureDOM();
  const view = new LearningTicketReview(root, {});
  view.ticket = { state: 'ready' };
  view.execution = {
    executor: { agent: 'Writer', provider: 'test', model: 'bounded' },
    input: 'Reviewed correction',
    disclosure: 'One bounded call',
    original: { run_id: 'old', artifact_id: 'old-artifact' },
    original_text: '<script>original bytes</script>'
  };
  view.render();
  assert.match(root.textContent, /<script>original bytes<\/script>/);
  assert.match(root.textContent, /Reviewed correction/);
  assert.match(root.textContent, /No active learning contract/);
  assert.ok(named(root, 'Start this Ticket with this input and model'));
});
