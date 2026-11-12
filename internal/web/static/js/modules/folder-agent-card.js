// Start an agent on <folder>: the one card every folder entry shows, the
// receipt after Start, and the folder agent's report with Set it up, Keep the
// folder as it is and Look further. The server holds the folder; nothing here
// sends a path. Words come from the server's view (facts line, stage line,
// tools); this module only lays them out.

export const FOLDER_AGENT_ENDPOINT = '/api/personal-assistant/folder-agent';
export const FOLDER_AGENT_LOOK_KIND = 'folder_agent_look';
export const FOLDER_AGENT_REPORT_KIND = 'folder_agent_report';

const ACTION_LABELS = {
  start_next: 'Start this',
  set_up: 'Set it up',
  keep: 'Keep the folder as it is',
  look_further: 'Look further'
};

const ACTION_PATHS = {
  start_next: 'start-next',
  set_up: 'setup',
  keep: 'keep',
  look_further: 'look-further',
  // The goal offer's answers (Use this goal / Save after Edit, and Not now).
  use_goal: 'goal',
  dismiss_goal: 'goal',
  // The premise's answers (Manage them as …, and It's something else).
  premise_confirm: 'premise',
  premise_other: 'premise'
};

export function isFolderAgentKind(kind) {
  return kind === FOLDER_AGENT_LOOK_KIND || kind === FOLDER_AGENT_REPORT_KIND;
}

// folderAgentCardView is the card before Start, from the server's card.
export function folderAgentCardView(card) {
  if (!card || !card.choice_token) return null;
  return {
    token: String(card.choice_token),
    folder: String(card.folder_name || ''),
    title: String(card.title || `Start an agent on ${card.folder_name || 'this folder'}`),
    field: String(card.field || 'What should it do?'),
    brief: String(card.brief || ''),
    max: Number(card.brief_max) > 0 ? Number(card.brief_max) : 300,
    action: String(card.action || 'Hire & start'),
    note: String(card.note || ''),
    hire: folderAgentHireView(card.hire, card.folder_name)
  };
}

// folderAgentHireView is the card's Hire section: a new agent (name, model,
// mandate) or one of the owner's own agents. Nothing is hired until Start.
export function folderAgentHireView(hire, folderName = '') {
  const source = hire && typeof hire === 'object' ? hire : {};
  const models = (Array.isArray(source.models) ? source.models : [])
    .filter(m => m && m.model)
    .map(m => ({
      provider: String(m.provider || ''),
      model: String(m.model),
      label: String(m.label || m.model)
    }));
  const agents = (Array.isArray(source.agents) ? source.agents : [])
    .filter(a => a && a.name)
    .map(a => ({ name: String(a.name), model: String(a.model || '') }));
  const existing = agents.some(a => a.name === source.existing) ? String(source.existing) : '';
  return {
    name: String(source.agent_name || `${folderName || 'Folder'} Manager`),
    nameMax: Number(source.name_max) > 0 ? Number(source.name_max) : 60,
    mandate: String(source.mandate || ''),
    provider: String(source.model ? source.provider || '' : ''),
    model: String(source.model || ''),
    models,
    agents,
    existing,
    mode: existing ? 'existing' : 'new'
  };
}

// folderAgentHireChoice is what Start sends for the Hire section.
export function folderAgentHireChoice(hire) {
  if (!hire) return undefined;
  if (hire.mode === 'existing' && hire.existing) return { existing: hire.existing };
  const choice = {
    name: String(hire.name || '').trim(),
    mandate: String(hire.mandate || '').trim()
  };
  if (hire.model) {
    choice.provider = hire.provider;
    choice.model = hire.model;
  }
  return choice;
}

export function folderAgentActionLabel(id) {
  return ACTION_LABELS[id] || '';
}

export function folderAgentActionPath(workspaceID, action) {
  const path = ACTION_PATHS[action];
  if (!path || !workspaceID) return '';
  return `${FOLDER_AGENT_ENDPOINT}/${encodeURIComponent(workspaceID)}/${path}`;
}

// folderAgentTileLine is what a workspace tile's status slot shows while the
// folder agent looks or sets up; empty otherwise.
export function folderAgentTileLine(view) {
  if (!view) return '';
  if (view.state === 'looking') return String(view.progress_line || 'Looking around…');
  if (view.state === 'setting_up') return 'Setting up…';
  return '';
}

function toolView(candidate) {
  const label =
    candidate.source === 'search' && candidate.marketplace
      ? `Found by search · ${candidate.marketplace}`
      : '';
  return {
    id: String(candidate.id || ''),
    name: String(candidate.name || ''),
    detail: String(candidate.detail || ''),
    why: String(candidate.why || ''),
    label,
    required: Boolean(candidate.required),
    greyed: candidate.action === 'already_here'
  };
}

// folderAgentReportView lays out one folder agent view: the receipt while it
// looks, then the report. Required tools come first; "already here" tools are
// greyed and carry no action.
export function folderAgentReportView(view) {
  if (!view || !view.workspace_id) return null;
  const agent = String(view.agent_name || 'The agent');
  const folder = String(view.folder_name || 'this folder');
  const out = {
    workspaceID: String(view.workspace_id),
    workspace: String(view.workspace_name || folder),
    folder,
    route: String(view.route || ''),
    landing: landingOf(view.landing),
    agent,
    // The walk's narration (base names only, bounded by the server): the
    // folder it is in, how many it has opened, and whether the agent's own
    // turn has begun.
    look: {
      folder: String(view.progress?.folder || ''),
      opened: Math.max(0, Number(view.progress?.opened) || 0),
      turn: Boolean(view.progress?.turn)
    },
    state: String(view.state || ''),
    title: '',
    receipt: '',
    progress: '',
    facts: String(view.facts_line || ''),
    stage: String(view.stage_line || ''),
    policyNote: String(view.policy_note || ''),
    exploreFolder: Boolean(view.explore_folder),
    tools: [],
    toolsSentence: String(view.tools_sentence || ''),
    modelSentence: String(view.model_sentence || ''),
    nextFolders: Array.isArray(view.next_folders) ? view.next_folders.map(String) : [],
    failure: String(view.failure || ''),
    actions: [],
    plan: null,
    setup: null,
    nested: view.nested?.name ? `Inside ${view.nested.name}'s folder` : '',
    // The agent's words, as text (never markup), or the sentence in their place.
    whatItIs: String(view.what_it_is || ''),
    question: String(view.question || ''),
    askFolders: (Array.isArray(view.ask_folders) ? view.ask_folders : [])
      .filter(folder => folder?.name && folder?.ref)
      .map(folder => ({ name: String(folder.name), ref: String(folder.ref) })),
    leftOut: view.left_out && typeof view.left_out === 'object' ? { ...view.left_out } : {},
    // What the folder is, in the host's words, and the owner's answer
    // (confirm-and-visible-installs FR1–FR6); null when it is not asked.
    premise: premiseView(view.premise),
    nextTicket: view.next_ticket?.title
      ? { title: String(view.next_ticket.title), started: Boolean(view.next_ticket.started) }
      : null,
    // The agent's suggested goal, offered once the place exists and only
    // while the workspace has no goal and the owner has not answered
    // (folder-setup-ends-in-home FR17–FR19). Never on the report card.
    goalOffer: view.goal_offer?.goal
      ? {
          goal: String(view.goal_offer.goal),
          maxRunes: Number(view.goal_offer.max_runes) > 0 ? Number(view.goal_offer.max_runes) : 300
        }
      : null
  };
  if (out.state === 'looking') {
    out.title = `${agent} is looking around ${folder}`;
    out.receipt = String(view.receipt || 'Looking around…');
    out.progress = String(view.progress_line || '');
    return out;
  }
  if (out.state === 'failed') {
    out.title = `${agent} could not finish looking around ${folder}`;
  } else if (out.state === 'setting_up') {
    out.title = `Setting up ${out.workspace}`;
  } else if (out.state === 'set_up') {
    out.title = `${out.workspace} is set up`;
  } else {
    out.title = `${agent} looked around ${folder}`;
  }
  const candidates = Array.isArray(view.candidates) ? view.candidates : [];
  out.tools = [...candidates.filter(c => c.required), ...candidates.filter(c => !c.required)].map(
    toolView
  );
  out.actions = (Array.isArray(view.actions) ? view.actions : [])
    .filter(id => ACTION_LABELS[id])
    .map(id => ({ id, label: ACTION_LABELS[id] }));
  if (view.plan) {
    out.plan = planView(view.plan);
    // Set it up waits for a plan that can run; the card says why it cannot.
    if (!out.plan.runnable) out.actions = out.actions.filter(a => a.id !== 'set_up');
    // The button says what it confirms: how many plugins it installs.
    out.actions = out.actions.map(a =>
      a.id === 'set_up' ? { ...a, label: folderAgentSetUpLabel(out.plan) } : a
    );
  }
  if (view.setup) {
    out.setup = {
      status: String(view.setup.status || ''),
      stopReason: String(view.setup.stop_reason || ''),
      startedAt: timeOf(view.setup.started_at),
      finishedAt: timeOf(view.setup.finished_at),
      lines: (view.setup.lines || []).map(line => lineView(line, 'waiting')),
      receipt: receiptView(view.setup.receipt)
    };
  }
  return out;
}

function premiseView(raw) {
  if (!raw?.state || !raw?.sentence) return null;
  return {
    state: String(raw.state),
    sentence: String(raw.sentence),
    confirm: String(raw.confirm || ''),
    other: String(raw.other || ''),
    withdrawn: Boolean(raw.withdrawn)
  };
}

// The premise is asked: the card shows only it until the owner answers.
export function folderAgentPremiseAsked(view) {
  return view?.premise?.state === 'asked' && !view?.setup;
}

// A workspace route the receipt may link: the app's own workspace pages only.
function workspaceRoute(route) {
  const value = String(route || '');
  return value.startsWith('/workspaces/') && !value.includes('//') ? value : '';
}

function refView(ref) {
  return ref?.name ? { name: String(ref.name), route: workspaceRoute(ref.route) } : null;
}

// receiptView is what a run made, as the host reported it: plugins with
// versions, the Home, a group's members, the agents hired, the songs listed.
function receiptView(receipt) {
  if (!receipt || typeof receipt !== 'object') return null;
  const strings = list => (Array.isArray(list) ? list.map(String).filter(Boolean) : []);
  return {
    installed: strings(receipt.installed),
    home: refView(receipt.home),
    homeCreated: Boolean(receipt.home_created),
    group: Boolean(receipt.group),
    members: (Array.isArray(receipt.members) ? receipt.members : []).map(refView).filter(Boolean),
    hired: strings(receipt.hired),
    listed: Math.max(0, Number(receipt.listed) || 0),
    listedNoun: String(receipt.listed_noun || ''),
    unreached: Math.max(0, Number(receipt.unreached) || 0)
  };
}

// folderAgentReceiptRows are the receipt's rows (PRD FR10), each left out when
// empty: what was installed, created, hired, listed and where the folder now
// sits, each with the one link where the owner can check it.
export function folderAgentReceiptRows(view) {
  const receipt = view?.setup?.receipt;
  if (!receipt) return [];
  const rows = [];
  const row = (label, words, link) => {
    if (words) rows.push({ label, words, link: link?.href ? link : null });
  };
  row('Installed', receipt.installed.join(' · '), { text: 'Plugins', href: '/plugins' });
  const created = [];
  if (receipt.home && receipt.homeCreated) created.push(receipt.home.name);
  if (receipt.group && receipt.members.length)
    created.push(
      `${receipt.members.length} ${plural(receipt.members.length, 'workspace', 'workspaces')} in ${view.workspace}`
    );
  row(
    'Created',
    created.join(' · '),
    receipt.home && receipt.homeCreated ? { text: 'Open', href: receipt.home.route } : null
  );
  row('Hired', receipt.hired.join(' · '), { text: 'Agents', href: '/agents' });
  if (receipt.listed || receipt.unreached) {
    const noun = receipt.listedNoun || plural(receipt.listed, 'song', 'songs');
    const words = [`${receipt.listed} ${noun} on the shelf`];
    if (receipt.unreached)
      words.push(
        `${receipt.unreached} ${plural(receipt.unreached, 'folder', 'folders')} not reached yet`
      );
    row('Listed', words.join(' · '));
  }
  let placed = '';
  if (receipt.home) placed = `${view.workspace} inside your ${receipt.home.name}`;
  else if (receipt.group)
    placed = `${view.workspace} is now a group with ${receipt.members.length} ${plural(receipt.members.length, 'workspace', 'workspaces')}`;
  row('Placed', placed, view.route ? { text: `Open ${view.workspace}`, href: view.route } : null);
  return rows;
}

// renderReceipt draws the receipt rows as a list of label, words and link.
function renderReceipt(doc, rows) {
  const list = el(doc, 'dl', 'folder-agent-card__receipt-rows');
  rows.forEach(row => {
    const item = el(doc, 'div', 'folder-agent-card__receipt-row');
    item.dataset.kind = row.label.toLowerCase();
    const words = el(doc, 'dd', 'folder-agent-card__receipt-words');
    words.append(el(doc, 'span', 'folder-agent-card__receipt-text', row.words));
    if (row.link) {
      const link = el(doc, 'a', 'folder-agent-card__receipt-link', row.link.text);
      link.href = row.link.href;
      words.append(link);
    }
    item.append(el(doc, 'dt', 'folder-agent-card__receipt-label', row.label), words);
    list.append(item);
  });
  return list;
}

// timeOf reads a server timestamp as epoch milliseconds; 0 when absent.
function timeOf(value) {
  const ms = Date.parse(String(value || ''));
  return Number.isFinite(ms) && ms > 0 ? ms : 0;
}

// lineView is one plan or run line: what it does, and while a run is in
// progress, its state, the runner's words for it and when it started and
// finished (server times).
function lineView(line, defaultState = '') {
  return {
    kind: String(line.kind || ''),
    step: String(line.step || ''),
    name: String(line.name || ''),
    detail: String(line.detail || ''),
    state: String(line.state || defaultState),
    progress: String(line.progress || ''),
    startedAt: timeOf(line.started_at),
    finishedAt: timeOf(line.finished_at)
  };
}

// formatFolderAgentDuration words an action's time: "<1 s", "6 s", "1:05".
export function formatFolderAgentDuration(ms) {
  const value = Math.max(0, Number(ms) || 0);
  if (value < 1000) return '<1 s';
  const seconds = Math.floor(value / 1000);
  if (seconds < 60) return `${seconds} s`;
  return formatFolderAgentClock(value);
}

// formatFolderAgentClock words a run's time as m:ss.
export function formatFolderAgentClock(ms) {
  const seconds = Math.floor(Math.max(0, Number(ms) || 0) / 1000);
  return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, '0')}`;
}

// folderAgentLineTime is how long a line has taken: a working line against
// the client clock (now) from its server start, a finished one by the
// server's own start and finish. null for a line that has not started.
export function folderAgentLineTime(line, now = Date.now()) {
  if (!line?.startedAt) return null;
  if (line.state === 'working') return Math.max(0, now - line.startedAt);
  if ((line.state === 'done' || line.state === 'failed') && line.finishedAt)
    return Math.max(0, line.finishedAt - line.startedAt);
  return null;
}

// folderAgentNeedsView reads a Today item of a folder agent kind.
export function folderAgentNeedsView(item) {
  if (!item || !isFolderAgentKind(item.kind) || !item.folder_agent) return null;
  return folderAgentReportView(item.folder_agent);
}

const LINE_STATE_WORDS = {
  done: 'Done',
  working: 'Working',
  failed: 'Did not finish',
  waiting: ''
};

function el(doc, tag, className, text) {
  const node = doc.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined && text !== null && text !== '') node.textContent = text;
  return node;
}

let hireSeq = 0;

function hireField(doc, caption, control) {
  const label = el(doc, 'label', 'folder-agent-card__hire-field');
  label.append(el(doc, 'span', 'folder-agent-card__label', caption), control);
  return label;
}

// renderHire draws the Hire section and keeps hire (the view's own object)
// in step with what the owner picks.
function renderHire(doc, hire) {
  const group = `folder-agent-hire-${++hireSeq}`;
  const box = el(doc, 'fieldset', 'folder-agent-card__hire');
  box.append(el(doc, 'legend', 'folder-agent-card__hire-title', 'Hire'));
  const radio = (mode, text) => {
    const label = el(doc, 'label', 'folder-agent-card__hire-option');
    const input = doc.createElement('input');
    input.type = 'radio';
    input.name = group;
    input.value = mode;
    input.checked = hire.mode === mode;
    input.dataset.folderAgentHire = mode;
    label.append(input, el(doc, 'span', 'folder-agent-card__hire-option-label', text));
    return { label, input };
  };

  const fresh = radio('new', 'A new agent');
  const freshFields = el(doc, 'div', 'folder-agent-card__hire-fields');
  const name = doc.createElement('input');
  name.type = 'text';
  name.className = 'form-control form-control-sm folder-agent-card__hire-name';
  name.maxLength = hire.nameMax;
  name.value = hire.name;
  name.autocomplete = 'off';
  name.addEventListener('input', () => {
    hire.name = String(name.value || '');
  });
  freshFields.append(hireField(doc, 'Name', name));
  let model = null;
  if (hire.models.length) {
    model = doc.createElement('select');
    model.className = 'form-select form-select-sm folder-agent-card__hire-model';
    // Without a system model nothing is preselected: hired without a model,
    // the agent still looks around and reports.
    if (!hire.model) {
      const none = doc.createElement('option');
      none.value = '\u0000';
      none.textContent = 'No model yet';
      model.append(none);
    }
    hire.models.forEach(option => {
      const item = doc.createElement('option');
      item.value = `${option.provider}\u0000${option.model}`;
      item.textContent = option.label;
      model.append(item);
    });
    model.value = `${hire.provider}\u0000${hire.model}`;
    model.addEventListener('change', () => {
      const [provider, picked] = String(model.value || '').split('\u0000');
      hire.provider = provider || '';
      hire.model = picked || '';
    });
    freshFields.append(hireField(doc, 'Model', model));
  } else {
    freshFields.append(
      el(
        doc,
        'p',
        'folder-agent-card__hire-note',
        'No model is set up yet. It still looks around and reports without one.'
      )
    );
  }
  const mandate = doc.createElement('input');
  mandate.type = 'text';
  mandate.className = 'form-control form-control-sm folder-agent-card__hire-mandate';
  mandate.maxLength = 300;
  mandate.value = hire.mandate;
  mandate.placeholder = 'Same as what it should do';
  mandate.autocomplete = 'off';
  mandate.addEventListener('input', () => {
    hire.mandate = String(mandate.value || '');
  });
  freshFields.append(hireField(doc, 'Role', mandate));
  box.append(fresh.label, freshFields);

  let reuse = null;
  let agent = null;
  if (hire.agents.length) {
    reuse = radio('existing', 'One of your agents');
    agent = doc.createElement('select');
    agent.className = 'form-select form-select-sm folder-agent-card__hire-agent';
    agent.setAttribute('aria-label', 'Your agent');
    hire.agents.forEach(option => {
      const item = doc.createElement('option');
      item.value = option.name;
      item.textContent = option.model ? `${option.name} · ${option.model}` : option.name;
      agent.append(item);
    });
    if (!hire.existing) hire.existing = hire.agents[0].name;
    agent.value = hire.existing;
    agent.addEventListener('change', () => {
      hire.existing = String(agent.value || '');
    });
    const reuseFields = el(doc, 'div', 'folder-agent-card__hire-fields');
    reuseFields.append(agent);
    box.append(reuse.label, reuseFields);
  }

  const sync = () => {
    const isNew = hire.mode !== 'existing';
    [name, model, mandate].forEach(control => {
      if (control) control.disabled = !isNew;
    });
    if (agent) agent.disabled = isNew;
  };
  [fresh.input, reuse?.input].forEach(input => {
    input?.addEventListener('change', () => {
      if (input.checked) hire.mode = input.value;
      sync();
    });
  });
  sync();
  return box;
}

// renderFolderAgentCard draws the card before Start. onStart(brief, button,
// hire) posts Hire & start; the caller swaps in the receipt.
export function renderFolderAgentCard(view, doc = document, { onStart, error = '' } = {}) {
  const card = el(doc, 'article', 'folder-agent-card');
  card.dataset.folderAgentCard = 'choose';
  const title = el(doc, 'h3', 'folder-agent-card__title', view.title);
  const label = el(doc, 'label', 'folder-agent-card__field');
  const caption = el(doc, 'span', 'folder-agent-card__label', view.field);
  const input = doc.createElement('textarea');
  input.className = 'folder-agent-card__brief';
  input.rows = 2;
  input.maxLength = view.max;
  input.value = view.brief;
  input.setAttribute('aria-label', view.field);
  label.append(caption, input);
  const hire = view.hire ? renderHire(doc, view.hire) : null;
  const note = el(doc, 'p', 'folder-agent-card__note', view.note);
  const actions = el(doc, 'div', 'folder-agent-card__actions');
  const start = el(doc, 'button', 'btn btn-primary folder-agent-card__start', view.action);
  start.type = 'button';
  start.dataset.folderAgentAction = 'start';
  start.addEventListener('click', () =>
    onStart?.(String(input.value || ''), start, folderAgentHireChoice(view.hire))
  );
  actions.append(start);
  card.append(title, label);
  if (hire) card.append(hire);
  card.append(note, actions);
  if (error) {
    const alert = el(doc, 'p', 'folder-agent-card__error', error);
    alert.setAttribute('role', 'alert');
    card.append(alert);
  }
  return card;
}

// The assurance line ("Nothing is moved, copied or read") is a note under
// the steps, not a step of its own.
const NOTE_LINE_KIND = 'folder';

function plural(n, one, many) {
  return n === 1 ? one : many;
}

// runHeadline says where a run that is not in progress stands, above the
// steps (renderPlan words the running headline itself).
function runHeadline(phase, done, failedAt, count) {
  const steps = `${count} ${plural(count, 'step', 'steps')}`;
  switch (phase) {
    case 'done':
      return `All done · ${steps}`;
    case 'stopped':
      return failedAt >= 0
        ? `Stopped at step ${failedAt + 1} of ${count}`
        : `Stopped · ${done} of ${count} done`;
    default:
      return `Plan · ${steps}`;
  }
}

// groupActions turns the plan's lines into rows: one per step the lines came
// from, in order of appearance, with the lines as its actions; a line with no
// step stands as its own row; the assurance line is a note.
function groupActions(lines, steps) {
  const byHandle = new Map(steps.map(step => [step.handle, step]));
  const rows = [];
  const index = new Map();
  const notes = [];
  lines.forEach(line => {
    if (line.kind === NOTE_LINE_KIND) {
      notes.push(line);
      return;
    }
    const key = line.step || `line:${rows.length}`;
    let row = index.get(key);
    if (!row) {
      const step = line.step ? byHandle.get(line.step) : null;
      row = {
        handle: line.step || '',
        title: step?.title || line.name,
        label: step?.label || '',
        excludes: step?.excludes || '',
        detail: line.step ? '' : line.detail,
        state: line.step ? '' : line.state || 'waiting',
        // A line with no step is its own row and its own action.
        line: line.step ? null : line,
        actions: []
      };
      index.set(key, row);
      rows.push(row);
    }
    if (line.step) row.actions.push(line);
  });
  return { rows, notes };
}

// rowState is a step's state from its actions: failed if one failed, done
// when all are, working once any has started.
function rowState(row) {
  if (!row.actions.length) return row.state || 'waiting';
  const states = row.actions.map(action => action.state || 'waiting');
  if (states.includes('failed')) return 'failed';
  if (states.every(state => state === 'done')) return 'done';
  if (states.some(state => state === 'working' || state === 'done')) return 'working';
  return 'waiting';
}

// rowUnits are a row's actions, or the row's own line when it has none: the
// units the progress bar and the running headline count.
function rowUnits(row) {
  if (row.actions.length) return row.actions;
  return row.line ? [row.line] : [];
}

// runStepOf is where a run stands: the step in hand (1-based) of all steps,
// and the working action's live words (its name until it has some).
function runStepOf(rows) {
  const units = rows.flatMap(rowUnits);
  const at = rows.findIndex(row => rowState(row) !== 'done');
  const working = units.find(unit => unit.state === 'working');
  const waiting = at >= 0 ? rowUnits(rows[at]).find(unit => unit.state !== 'done') : null;
  const current = working || waiting;
  return {
    step: at >= 0 ? at + 1 : rows.length,
    count: rows.length,
    words: current ? current.progress || current.name : ''
  };
}

// folderAgentRunStep is runStepOf for a report view's setup run, for the
// Commander's line on the map; null when no run is recorded.
export function folderAgentRunStep(view) {
  if (!view?.setup) return null;
  return runStepOf(groupActions(view.setup.lines || [], view.plan?.steps || []).rows);
}

// actionTime is the " · 6 s" part of a started action; a working one carries
// its server start so the clock can tick between polls.
function actionTime(doc, line, now) {
  const ms = folderAgentLineTime(line, now);
  if (ms === null) return null;
  const time = el(doc, 'span', 'folder-agent-card__action-time', formatFolderAgentDuration(ms));
  if (line.state === 'working') time.dataset.startedAt = String(line.startedAt);
  return time;
}

// renderAction draws one action: a spinner while it works, its name (or,
// once done, the runner's receipt words), the live words after the name, and
// the time last.
function renderAction(doc, action, now) {
  const state = action.state || 'waiting';
  const act = el(doc, 'span', 'folder-agent-card__action');
  act.dataset.state = state;
  if (action.detail) act.setAttribute('title', action.detail);
  if (state === 'working') {
    const spinner = el(doc, 'span', 'folder-agent-card__spinner');
    spinner.setAttribute('aria-hidden', 'true');
    act.append(spinner);
  }
  const receipt = state === 'done' && action.progress;
  act.append(
    el(doc, 'span', 'folder-agent-card__action-name', receipt ? action.progress : action.name)
  );
  if (!receipt && action.progress && (state === 'working' || state === 'failed'))
    act.append(el(doc, 'span', 'folder-agent-card__action-progress', action.progress));
  const time = actionTime(doc, action, now);
  if (time) act.append(time);
  return act;
}

// tickFolderAgentClocks moves every working action's time and the running
// headline's clock to now, between polls.
export function tickFolderAgentClocks(root, now = Date.now()) {
  const nodes = selector => Array.from(root?.querySelectorAll?.(selector) || []);
  nodes('.folder-agent-card__action-time').forEach(node => {
    const started = Number(node.dataset?.startedAt);
    if (started) node.textContent = formatFolderAgentDuration(now - started);
  });
  nodes('.folder-agent-card__run-clock').forEach(node => {
    const started = Number(node.dataset?.startedAt);
    if (started) node.textContent = formatFolderAgentClock(now - started);
  });
}

// startRunClock ticks a running section every second while it is on the
// page; each poll draws a fresh card, so the old one's clock stops at its
// next tick. A stub DOM (no isConnected) starts no clock.
function startRunClock(section) {
  if (typeof section.isConnected !== 'boolean' || typeof setInterval !== 'function') return;
  const timer = setInterval(() => {
    if (section.isConnected) tickFolderAgentClocks(section);
    else clearInterval(timer);
  }, 1000);
  timer?.unref?.();
}

// renderPlan draws the plan as one list the owner can follow: the agent's
// words (or Ori's reason for its default), a headline for where things
// stand, one numbered row per chosen step with its actions under it, the
// steps offered but not chosen (while choosing, each with a box), and the
// assurance as a note. phase is 'plan' (before Set it up), 'running', 'done'
// or 'stopped'. The state words stay in the row for screen readers; the
// marker and the action glyphs carry them for the eye.
function renderPlan(
  doc,
  view,
  plan,
  lines,
  phase,
  { selecting = false, onTick, now = Date.now(), headFirst = false } = {}
) {
  const section = el(doc, 'section', 'folder-agent-card__run');
  section.dataset.phase = phase;
  const steps = plan?.steps || [];
  if (plan) {
    section.dataset.planSource = plan.source;
    if (plan.source === 'agent' && plan.words) {
      const verb = phase === 'plan' ? 'recommends' : 'recommended';
      section.append(el(doc, 'h4', 'folder-agent-card__heading', `${view.agent} ${verb}`));
      section.append(renderWords(doc, plan));
      // Ori ran the tools ahead of the agent's order; its words stand as written.
      if (plan.reorderNote)
        section.append(el(doc, 'p', 'folder-agent-card__degraded', plan.reorderNote));
    } else if (phase === 'plan') {
      section.append(el(doc, 'h4', 'folder-agent-card__heading', 'Steps'));
      if (plan.defaultReason)
        section.append(el(doc, 'p', 'folder-agent-card__degraded', plan.defaultReason));
      if (plan.words) section.append(renderWords(doc, plan));
    }
  }
  const { rows, notes } = groupActions(lines, steps);
  const installs = plan?.installs || [];
  // An install the block lists is not repeated among its step's actions
  // (PRD FR18); its line still counts toward the step's state, which the run
  // moves. A step that installs carries a marker beside its title instead.
  const installsOf = new Map();
  installs.forEach(entry => {
    installsOf.set(entry.step, (installsOf.get(entry.step) || 0) + 1);
  });
  const covered = line =>
    installs.some(entry => entry.step === line.step && entry.line === line.kind);
  const chosen = new Set(rows.map(row => row.handle).filter(Boolean));
  // While choosing, a chosen step whose lines carry no step yet still has its
  // row. From Set it up on, the run's own lines are the steps: the plan's
  // current choice may differ from the one the run was confirmed on.
  steps.forEach(step => {
    if (phase !== 'plan' || !step.chosen || chosen.has(step.handle)) return;
    chosen.add(step.handle);
    rows.push({
      handle: step.handle,
      title: step.title,
      label: step.label,
      excludes: step.excludes,
      detail: '',
      state: 'waiting',
      actions: []
    });
  });
  const offered = phase === 'plan' ? steps.filter(step => !chosen.has(step.handle)) : [];
  const done = rows.filter(row => rowState(row) === 'done').length;
  const failedAt = rows.findIndex(row => rowState(row) === 'failed');
  const head = el(doc, 'p', 'folder-agent-card__run-head');
  head.setAttribute('role', 'status');
  if (phase === 'running') {
    // "Setting up · step X of Y · <what is happening> · m:ss": the step in
    // hand, the working action's live words (its name until it has some),
    // and the run's clock. The clock is hidden from the status region so it
    // is not announced every second; the words are.
    const units = rows.flatMap(rowUnits);
    const step = runStepOf(rows);
    const words = ['Setting up', `step ${step.step} of ${step.count}`, step.words].filter(Boolean);
    head.append(el(doc, 'span', 'folder-agent-card__run-head-text', words.join(' · ')));
    const started = view.setup?.startedAt || 0;
    if (started) {
      const clock = el(
        doc,
        'span',
        'folder-agent-card__run-clock',
        formatFolderAgentClock(now - started)
      );
      clock.dataset.startedAt = String(started);
      clock.setAttribute('aria-hidden', 'true');
      head.append(clock);
    }
    section.append(head);
    // A thin bar: done actions over all actions.
    const finished = units.filter(unit => unit.state === 'done').length;
    const bar = el(doc, 'div', 'folder-agent-card__progress');
    bar.setAttribute('role', 'progressbar');
    bar.setAttribute('aria-label', 'Setup progress');
    bar.setAttribute('aria-valuemin', '0');
    bar.setAttribute('aria-valuemax', String(units.length));
    bar.setAttribute('aria-valuenow', String(finished));
    const fill = el(doc, 'span', 'folder-agent-card__progress-fill');
    fill.setAttribute(
      'style',
      `width:${units.length ? Math.round((finished / units.length) * 100) : 0}%`
    );
    bar.append(fill);
    section.append(bar);
    // In the balloon the run's headline and bar lead, above the agent's words.
    if (headFirst && typeof section.prepend === 'function') section.prepend(head, bar);
    startRunClock(section);
  } else {
    let words = runHeadline(phase, done, failedAt, rows.length);
    const setup = view.setup;
    if (phase === 'done' && setup?.startedAt && setup?.finishedAt)
      words += ` · ${formatFolderAgentDuration(setup.finishedAt - setup.startedAt)}`;
    head.textContent = words;
    section.append(head);
    if (headFirst && phase !== 'plan' && typeof section.prepend === 'function')
      section.prepend(head);
  }
  // Once the run ends, what it made, with a link to check each: a stopped
  // run shows what finished above the line that failed.
  if (phase === 'done' || phase === 'stopped') {
    const receiptRows = folderAgentReceiptRows(view);
    if (receiptRows.length) section.append(renderReceipt(doc, receiptRows));
  }
  const list = el(doc, 'ol', 'folder-agent-card__plan');
  const renderRow = (row, index, isChosen) => {
    const item = el(doc, 'li', 'folder-agent-card__line');
    const state = isChosen ? rowState(row) : 'waiting';
    item.dataset.state = state;
    item.dataset.chosen = isChosen ? 'true' : 'false';
    if (row.handle) item.dataset.step = row.handle;
    // The final receipt reveals its checks one after another (CSS).
    item.setAttribute('style', `--step:${Math.max(index, 0)}`);
    if (selecting && row.handle) {
      item.className += ' folder-agent-card__line--pick';
      const box = doc.createElement('input');
      box.type = 'checkbox';
      box.className = 'folder-agent-card__tool-pick';
      box.setAttribute('aria-label', `Include: ${row.title}`);
      box.checked = isChosen;
      box.dataset.stepHandle = row.handle;
      box.addEventListener('change', () => onTick?.(row.handle, box.checked, row.excludes));
      item.append(box);
    }
    const mark = el(doc, 'span', 'folder-agent-card__line-mark', isChosen ? String(index + 1) : '');
    mark.setAttribute('aria-hidden', 'true');
    const body = el(doc, 'span', 'folder-agent-card__line-body');
    // A finished run's steps collapse to their titles with their time; a
    // click shows what each step did.
    const collapsible = phase === 'done' && isChosen && row.actions.length > 0;
    if (collapsible) {
      item.dataset.collapsed = 'true';
      const toggle = el(
        doc,
        'button',
        'folder-agent-card__line-name folder-agent-card__line-toggle',
        row.title
      );
      toggle.type = 'button';
      toggle.setAttribute('aria-expanded', 'false');
      toggle.addEventListener('click', () => {
        const open = item.dataset.collapsed === 'true';
        item.dataset.collapsed = open ? 'false' : 'true';
        toggle.setAttribute('aria-expanded', open ? 'true' : 'false');
      });
      // The title and its time share one line.
      const stepHead = el(doc, 'span', 'folder-agent-card__step-head');
      stepHead.append(toggle);
      const units = rowUnits(row).filter(unit => unit.startedAt && unit.finishedAt);
      if (units.length) {
        const start = Math.min(...units.map(unit => unit.startedAt));
        const end = Math.max(...units.map(unit => unit.finishedAt));
        stepHead.append(
          el(doc, 'span', 'folder-agent-card__step-time', formatFolderAgentDuration(end - start))
        );
      }
      body.append(stepHead);
    } else {
      body.append(el(doc, 'span', 'folder-agent-card__line-name', row.title));
    }
    if (row.label) body.append(el(doc, 'span', 'folder-agent-card__tool-label', row.label));
    const installing = isChosen ? installsOf.get(row.handle) || 0 : 0;
    if (installing) {
      const marker = el(
        doc,
        'span',
        'folder-agent-card__tool-label folder-agent-card__install-marker',
        `Installs ${installing} ${plural(installing, 'plugin', 'plugins')}`
      );
      body.append(marker);
    }
    const shown = row.actions.filter(action => !covered(action));
    if (shown.length) {
      const actions = el(doc, 'span', 'folder-agent-card__actions-line');
      shown.forEach((action, k) => {
        if (k) actions.append(el(doc, 'span', 'folder-agent-card__action-sep', ' · '));
        actions.append(renderAction(doc, action, now));
      });
      body.append(actions);
    } else if (!row.actions.length) {
      // A step whose every action is in the install block shows none here.
      if (row.detail) body.append(el(doc, 'span', 'folder-agent-card__line-detail', row.detail));
      // A row that is its own action shows its live words and time too.
      if (row.line && phase !== 'plan') {
        const live = el(doc, 'span', 'folder-agent-card__line-live');
        if (row.line.progress && row.line.state !== 'waiting')
          live.append(el(doc, 'span', 'folder-agent-card__action-progress', row.line.progress));
        const time = actionTime(doc, row.line, now);
        if (time) live.append(time);
        if (live.children?.length) body.append(live);
      }
    }
    item.append(mark, body);
    const words = isChosen ? LINE_STATE_WORDS[state] : '';
    if (words) item.append(el(doc, 'span', 'folder-agent-card__line-state', words));
    return item;
  };
  rows.forEach((row, index) => list.append(renderRow(row, index, true)));
  offered.forEach(step =>
    list.append(
      renderRow(
        {
          handle: step.handle,
          title: step.title,
          label: step.label,
          excludes: step.excludes,
          detail: step.why,
          actions: []
        },
        -1,
        false
      )
    )
  );
  section.append(list);
  if (installs.length) section.append(renderInstalls(doc, installs, lines, phase));
  notes.forEach(line => {
    const note = el(doc, 'p', 'folder-agent-card__line folder-agent-card__line--note');
    note.dataset.state = line.state || 'waiting';
    note.dataset.kind = NOTE_LINE_KIND;
    note.append(el(doc, 'span', 'folder-agent-card__line-name', line.name));
    section.append(note);
  });
  if (plan?.excluded) section.append(el(doc, 'p', 'folder-agent-card__degraded', plan.excluded));
  if (phase === 'plan' && view.toolsSentence)
    section.append(el(doc, 'p', 'folder-agent-card__degraded', view.toolsSentence));
  return section;
}

// installState is an install row's state from the run's line for it: the
// line of its kind under its step. Before a run there is none.
function installState(entry, lines, phase) {
  if (phase === 'plan') return { state: '', words: '' };
  const line = lines.find(l => l.step === entry.step && l.kind === entry.line);
  const state = line?.state || 'waiting';
  const progress = String(line?.progress || '');
  switch (state) {
    case 'working':
      return { state, words: progress || 'Downloading' };
    case 'done': {
      // The runner's receipt words end with the installed version ("Installed
      // <plugin> 0.9.1"); the row already names the plugin, so it shows only
      // the version. A plugin that was already in place says Installed.
      const version = progress.startsWith('Installed')
        ? (progress.match(/(\d+\.\d+[\w.+-]*)$/) || [])[1]
        : '';
      return { state, words: version ? `Installed ${version}` : 'Installed' };
    }
    case 'failed':
      return { state, words: progress ? `Failed: ${progress}` : 'Failed' };
  }
  return { state: 'waiting', words: 'Waiting' };
}

// renderInstalls draws the install block (PRD FR13, FR14, FR19): one row per
// plugin the plan downloads and installs, in run order. Name and version;
// where it comes from with a link to its source and to Plugins; what it gives
// the owner; where the application and its tools stand. While a run is under
// way and after it, each row shows its state from the run's own line.
function renderInstalls(doc, installs, lines, phase) {
  const n = installs.length;
  const block = el(doc, 'section', 'folder-agent-card__installs');
  const heading = el(
    doc,
    'h4',
    'folder-agent-card__heading',
    `Downloads and installs ${n} ${plural(n, 'plugin', 'plugins')}`
  );
  block.append(heading);
  const list = el(doc, 'ul', 'folder-agent-card__install-list');
  installs.forEach(entry => {
    const item = el(doc, 'li', 'folder-agent-card__install');
    item.dataset.step = entry.step;
    const { state, words } = installState(entry, lines, phase);
    if (state) item.dataset.state = state;
    const head = el(doc, 'span', 'folder-agent-card__install-head');
    head.append(
      el(doc, 'span', 'folder-agent-card__line-name', entry.name),
      el(
        doc,
        'span',
        'folder-agent-card__install-version',
        entry.version || 'latest reviewed release'
      )
    );
    if (words) head.append(el(doc, 'span', 'folder-agent-card__line-state', words));
    const source = el(doc, 'span', 'folder-agent-card__install-source');
    const part = node => {
      if (source.children.length)
        source.append(el(doc, 'span', 'folder-agent-card__install-sep', ' · '));
      source.append(node);
    };
    if (entry.search) {
      part(
        el(
          doc,
          'span',
          'folder-agent-card__tool-label',
          entry.marketplace ? `Found by search · ${entry.marketplace}` : 'Found by search'
        )
      );
    } else {
      part(el(doc, 'span', 'folder-agent-card__install-review', 'Reviewed release'));
      if (entry.publisher)
        part(el(doc, 'span', 'folder-agent-card__install-publisher', entry.publisher));
    }
    if (entry.sourceURL) {
      const link = el(doc, 'a', 'folder-agent-card__install-link', entry.sourceLabel || 'Source');
      link.href = entry.sourceURL;
      link.target = '_blank';
      link.rel = 'noopener noreferrer';
      part(link);
    }
    const plugins = el(doc, 'a', 'folder-agent-card__install-link', 'Plugins');
    plugins.href = '/plugins';
    part(plugins);
    item.append(head, source);
    if (entry.why) item.append(el(doc, 'span', 'folder-agent-card__line-detail', entry.why));
    if (entry.appSentence)
      item.append(el(doc, 'span', 'folder-agent-card__line-detail', entry.appSentence));
    list.append(item);
  });
  block.append(list);
  return block;
}

const HANDLE_PATTERN = /\[([a-z0-9][a-z0-9-]*)\]/g;

// renderWords draws the agent's plan with each step it named as a chip
// carrying the step's name, so the reader sees which words became steps.
// Any other handle-shaped token is left out. prefix opens the paragraph
// ("Albums Manager recommended: ").
function renderWords(doc, plan, prefix = '') {
  const words = el(doc, 'p', 'folder-agent-card__words');
  const names = new Map(plan.steps.map(step => [step.handle, step.name || step.title]));
  const text = run => {
    if (run) words.append(el(doc, 'span', 'folder-agent-card__words-text', run));
  };
  text(prefix);
  const raw = plan.wordsRaw || plan.words;
  let last = 0;
  for (const match of raw.matchAll(HANDLE_PATTERN)) {
    text(raw.slice(last, match.index));
    const name = names.get(match[1]);
    if (name) {
      const chip = el(doc, 'span', 'folder-agent-card__step-chip', name);
      chip.dataset.step = match[1];
      words.append(chip);
    }
    last = match.index + match[0].length;
  }
  text(raw.slice(last));
  return words;
}

// runPhase is the setup run's phase for renderPlanSteps.
function runPhase(setup) {
  if (setup.status === 'running') return 'running';
  if (setup.status === 'stopped') return 'stopped';
  return 'done';
}

// renderFolderAgentReport draws the receipt or the report. onAction(id,
// button, extra) posts one action; the caller refreshes. opening says the
// page is about to open the place: the link gives way to that word.
// goalOfferSection is the Commander offering the agent's suggested goal once
// the place exists (folder-setup-ends-in-home FR17, FR18): the sentence, then
// Use this goal, Edit and Not now. Edit turns the sentence into a one-line
// input (max_runes characters) with Save and Cancel, in place; Escape cancels.
function goalOfferSection(doc, offer, onAction) {
  const section = el(doc, 'section', 'folder-agent-card__goal-offer');
  section.setAttribute('aria-label', 'Suggested goal');
  const lead = el(doc, 'p', 'folder-agent-card__goal-lead', 'A goal for this place?');
  const sentence = el(doc, 'p', 'folder-agent-card__goal-text', offer.goal);
  const row = el(doc, 'div', 'folder-agent-card__actions folder-agent-card__goal-actions');
  const button = (label, action, primary = false) => {
    const b = el(
      doc,
      'button',
      primary ? 'btn btn-primary btn-sm' : 'btn btn-sm folder-agent-card__secondary',
      label
    );
    b.type = 'button';
    b.dataset.folderAgentAction = action;
    return b;
  };
  const showOffer = () => {
    const use = button('Use this goal', 'use_goal', true);
    use.addEventListener('click', () => onAction?.('use_goal', use, {}));
    const edit = button('Edit', 'edit_goal');
    edit.addEventListener('click', showEdit);
    const later = button('Not now', 'dismiss_goal');
    later.addEventListener('click', () => onAction?.('dismiss_goal', later, {}));
    section.replaceChildren(lead, sentence, row);
    row.replaceChildren(use, edit, later);
    return use;
  };
  function showEdit() {
    const input = doc.createElement('input');
    input.type = 'text';
    input.className = 'folder-agent-card__goal-input';
    input.maxLength = offer.maxRunes;
    input.value = offer.goal;
    input.setAttribute('aria-label', 'Goal');
    const save = button('Save', 'use_goal', true);
    const cancel = button('Cancel', 'cancel_goal');
    const submit = () => {
      const goal = String(input.value || '').trim();
      if (goal) onAction?.('use_goal', save, { goal });
    };
    save.addEventListener('click', submit);
    cancel.addEventListener('click', () => showOffer().focus?.());
    input.addEventListener('keydown', event => {
      if (event.key === 'Enter') submit();
      if (event.key === 'Escape') {
        event.stopPropagation?.();
        showOffer().focus?.();
      }
    });
    section.replaceChildren(lead, input, row);
    row.replaceChildren(save, cancel);
    input.focus?.();
  }
  showOffer();
  return section;
}

export function renderFolderAgentReport(
  view,
  doc = document,
  {
    onAction,
    error = '',
    fetchImpl = globalThis.fetch,
    opening = false,
    now = Date.now(),
    compact = false
  } = {}
) {
  // compact is the Commander's balloon on the map: its header names the
  // speaker and carries the live line, so the card drops its title and the
  // looking receipt (one renderer, two shells).
  const card = el(
    doc,
    'article',
    'folder-agent-card folder-agent-card--report' + (compact ? ' folder-agent-card--compact' : '')
  );
  card.dataset.folderAgentCard = view.state;
  card.dataset.workspaceId = view.workspaceID;
  // The steps left ticked, and the plan they make: a tick asks the server
  // for the plan (and digest) with that choice.
  const selecting = Boolean(view.plan?.runnable) && view.actions.some(a => a.id === 'set_up');
  const selected = new Set(view.plan?.selected || []);
  let planHost = null;
  let setUpButton = null;
  const onTick = (handle, checked, excludes) => {
    if (checked) {
      selected.add(handle);
      // The owner's latest tick wins over a step it cannot run with.
      if (excludes) selected.delete(excludes);
    } else selected.delete(handle);
    return refreshPlan();
  };
  const refreshPlan = async () => {
    const plan = await fetchFolderAgentPlan(view.workspaceID, [...selected], fetchImpl);
    if (!plan) return;
    view.plan = plan;
    // The server may have set a step aside (two that cannot run together):
    // the boxes follow its choice.
    selected.clear();
    plan.selected.forEach(handle => selected.add(handle));
    if (planHost) {
      // The box the owner just ticked keeps focus through the redraw (a
      // keyboard user would otherwise be dropped on the page body).
      const focused = doc.activeElement?.dataset?.stepHandle || '';
      planHost.replaceChildren(
        renderPlan(doc, view, plan, plan.lines, 'plan', { selecting, onTick })
      );
      if (focused) {
        const again = Array.from(planHost.querySelectorAll?.('input[type="checkbox"]') || []).find(
          box => box.dataset?.stepHandle === focused
        );
        again?.focus?.({ preventScroll: true });
      }
    }
    if (setUpButton) {
      setUpButton.disabled = !plan.runnable;
      // The label follows the install block (PRD FR16, FR21).
      setUpButton.textContent = folderAgentSetUpLabel(plan);
    }
  };
  if (!compact) card.append(el(doc, 'h3', 'folder-agent-card__title', view.title));
  if (view.state === 'looking' && compact) {
    card.append(el(doc, 'p', 'folder-agent-card__workspace', `Workspace ${view.workspace}`));
    if (view.nested) card.append(el(doc, 'p', 'folder-agent-card__note', view.nested));
    return card;
  }
  if (view.state === 'looking') {
    const receipt = el(doc, 'p', 'folder-agent-card__receipt');
    receipt.setAttribute('role', 'status');
    receipt.textContent = view.progress || view.receipt;
    card.append(
      el(doc, 'p', 'folder-agent-card__workspace', `Workspace ${view.workspace}`),
      receipt
    );
    if (view.nested) card.append(el(doc, 'p', 'folder-agent-card__note', view.nested));
    return card;
  }
  const premise = view.premise;
  const openLink = () => {
    if (!view.route) return;
    const open = el(doc, 'a', 'folder-agent-card__open', `Open ${view.workspace}`);
    open.href = view.route;
    card.append(open);
  };
  const errorLine = () => {
    if (!error) return;
    const alert = el(doc, 'p', 'folder-agent-card__error', error);
    alert.setAttribute('role', 'alert');
    card.append(alert);
  };
  if (folderAgentPremiseAsked(view)) {
    // What the folder is comes first (FR1–FR4, FR12): the host's sentence and
    // two real buttons, in that reading order. The recommendation, the plan,
    // the install block and the report's buttons wait for the answer.
    const ask = el(doc, 'section', 'folder-agent-card__premise');
    ask.append(el(doc, 'p', 'folder-agent-card__premise-text', premise.sentence));
    const answers = el(doc, 'div', 'folder-agent-card__actions');
    [
      ['premise_confirm', premise.confirm, 'btn btn-primary btn-sm'],
      ['premise_other', premise.other, 'btn btn-sm folder-agent-card__secondary']
    ].forEach(([id, label, className]) => {
      const button = el(doc, 'button', className, label);
      button.type = 'button';
      button.dataset.folderAgentAction = id;
      button.addEventListener('click', () => onAction?.(id, button));
      answers.append(button);
    });
    ask.append(answers);
    card.append(ask);
    openLink();
    errorLine();
    return card;
  }
  if (premise?.withdrawn && !view.setup) {
    // "It's something else" (FR6): nothing is recommended. A plain workspace,
    // or the owner's own words for a fresh look.
    card.append(el(doc, 'p', 'folder-agent-card__premise-text', premise.sentence));
    const row = el(doc, 'div', 'folder-agent-card__answer');
    const field = doc.createElement('input');
    field.type = 'text';
    field.maxLength = 200;
    field.className = 'folder-agent-card__answer-input';
    const label = `Tell ${view.agent} what it is`;
    field.setAttribute('aria-label', label);
    field.placeholder = label;
    const again = el(doc, 'button', 'btn btn-sm folder-agent-card__secondary', 'Look again');
    again.type = 'button';
    again.dataset.folderAgentAction = 'look_again';
    again.addEventListener('click', () => {
      const hint = String(field.value || '').trim();
      if (hint) onAction?.('look_further', again, { hint });
    });
    row.append(field, again);
    if (view.actions.some(action => action.id === 'look_further')) card.append(row);
    if (view.actions.some(action => action.id === 'keep')) {
      const actions = el(doc, 'div', 'folder-agent-card__actions');
      const keep = el(
        doc,
        'button',
        'btn btn-sm folder-agent-card__secondary',
        'Keep it as a plain workspace'
      );
      keep.type = 'button';
      keep.dataset.folderAgentAction = 'keep';
      keep.addEventListener('click', () => onAction?.('keep', keep));
      actions.append(keep);
      card.append(actions);
    }
    openLink();
    errorLine();
    return card;
  }
  if (view.failure) card.append(el(doc, 'p', 'folder-agent-card__failure', view.failure));
  if (view.modelSentence)
    card.append(el(doc, 'p', 'folder-agent-card__degraded', view.modelSentence));
  // Confirmed, the premise stays as one plain line in place of the agent's
  // own sentence and the facts line (FR5; open question 1: hide it).
  const premiseLine = premise?.state === 'confirmed';
  if (premiseLine) card.append(el(doc, 'p', 'folder-agent-card__premise-text', premise.sentence));
  const leftOut = section =>
    view.leftOut[section]
      ? el(doc, 'p', 'folder-agent-card__degraded', view.leftOut[section])
      : null;
  const whatItIs = premiseLine
    ? null
    : view.whatItIs
      ? el(doc, 'p', 'folder-agent-card__what', view.whatItIs)
      : leftOut('what_it_is');
  if (whatItIs) card.append(whatItIs);
  // The host's facts line stands in for the agent's sentence when there is
  // none; beside it, it would only repeat the counts.
  if (!premiseLine && view.facts && (!view.whatItIs || view.policyNote))
    card.append(el(doc, 'p', 'folder-agent-card__facts', view.facts));
  if (view.policyNote) card.append(el(doc, 'p', 'folder-agent-card__note', view.policyNote));
  if (view.exploreFolder) {
    // The normal chooser performs a new explicit selection; opening it neither
    // reanchors this workspace nor consents to a look or structural setup.
    const explore = el(
      doc,
      'a',
      'folder-agent-card__open folder-agent-card__explore',
      'Explore a project folder…'
    );
    explore.href = '/?panel=today&folder=show';
    card.append(explore);
  }
  // One list for the plan: while choosing, the plan's lines under their
  // steps with a box each; from Set it up on, the run's lines in the same
  // shape, with the agent's words above saying why.
  if (view.setup) {
    const run = renderPlan(doc, view, view.plan, view.setup.lines, runPhase(view.setup), {
      now,
      headFirst: compact
    });
    // In the balloon the run leads (its headline is where the owner looks);
    // the agent's words follow it.
    if (compact && typeof card.prepend === 'function') card.prepend(run);
    else card.append(run);
    if (view.setup.status === 'stopped') {
      card.append(
        el(
          doc,
          'p',
          'folder-agent-card__failure',
          'A step did not finish. Set it up again carries on from there.'
        )
      );
    }
  } else if (view.plan && (view.plan.steps.length || view.plan.runnable)) {
    planHost = el(doc, 'div', 'folder-agent-card__plan-host');
    planHost.append(
      renderPlan(doc, view, view.plan, view.plan.lines, 'plan', { selecting, onTick })
    );
    card.append(planHost);
    if (!view.plan.runnable && view.plan.unavailable)
      card.append(el(doc, 'p', 'folder-agent-card__note', view.plan.unavailable));
  } else {
    // No step list to show: Ori's one-line summary, and why it cannot run.
    if (view.stage) card.append(el(doc, 'p', 'folder-agent-card__stage', view.stage));
    if (view.plan?.unavailable)
      card.append(el(doc, 'p', 'folder-agent-card__note', view.plan.unavailable));
  }
  if (!view.plan?.steps.length && (view.tools.length || view.toolsSentence) && !view.setup) {
    const tools = el(doc, 'section', 'folder-agent-card__tools');
    tools.append(el(doc, 'h4', 'folder-agent-card__heading', 'Tools'));
    const list = el(doc, 'ul', 'folder-agent-card__tool-list');
    view.tools.forEach(tool => {
      const item = el(doc, 'li', 'folder-agent-card__tool');
      if (tool.greyed) item.dataset.greyed = 'true';
      if (tool.required) item.dataset.required = 'true';
      // While Set it up is offered: required tools are checked and locked,
      // optional ones can be unticked, "already here" ones have no box.
      if (selecting && !tool.greyed) {
        const box = doc.createElement('input');
        box.type = 'checkbox';
        box.className = 'folder-agent-card__tool-pick';
        box.setAttribute('aria-label', `Include ${tool.name}`);
        box.checked = tool.required || selected.has(tool.id);
        box.disabled = tool.required;
        box.dataset.toolId = tool.id;
        if (!tool.required) {
          box.addEventListener('change', () => {
            if (box.checked) selected.add(tool.id);
            else selected.delete(tool.id);
            return refreshPlan();
          });
        }
        item.append(box);
      }
      item.append(el(doc, 'span', 'folder-agent-card__tool-name', tool.name));
      if (tool.detail) item.append(el(doc, 'span', 'folder-agent-card__tool-detail', tool.detail));
      if (tool.label) item.append(el(doc, 'span', 'folder-agent-card__tool-label', tool.label));
      if (tool.why) item.append(el(doc, 'span', 'folder-agent-card__tool-why', tool.why));
      list.append(item);
    });
    if (view.tools.length) tools.append(list);
    if (view.toolsSentence)
      tools.append(el(doc, 'p', 'folder-agent-card__degraded', view.toolsSentence));
    card.append(tools);
  }
  if (view.nextFolders.length) {
    card.append(
      el(doc, 'p', 'folder-agent-card__next', `Not opened yet: ${view.nextFolders.join(', ')}`)
    );
  }
  // The report card shows the steps and its actions only: the goal is offered
  // once the place exists (goalOffer, below the receipt) and the first ticket
  // appears in the receipt once it was added (folder-setup-ends-in-home FR14).
  // The one question, above the actions: pick a folder or answer in a line.
  if (view.question && view.actions.some(action => action.id === 'look_further')) {
    const ask = el(doc, 'section', 'folder-agent-card__question');
    ask.append(el(doc, 'p', 'folder-agent-card__question-text', view.question));
    if (view.askFolders.length) {
      const chips = el(doc, 'div', 'folder-agent-card__chips');
      view.askFolders.forEach(folder => {
        const chip = el(doc, 'button', 'btn btn-sm folder-agent-card__secondary', folder.name);
        chip.type = 'button';
        chip.dataset.folderAgentChip = folder.name;
        chip.addEventListener('click', () =>
          onAction?.('look_further', chip, { folders: [folder.ref] })
        );
        chips.append(chip);
      });
      ask.append(chips);
    }
    const row = el(doc, 'div', 'folder-agent-card__answer');
    const answer = doc.createElement('input');
    answer.type = 'text';
    answer.maxLength = 200;
    answer.className = 'folder-agent-card__answer-input';
    answer.setAttribute('aria-label', 'Your answer');
    answer.placeholder = 'Or answer in one line';
    const send = el(doc, 'button', 'btn btn-sm folder-agent-card__secondary', 'Answer');
    send.type = 'button';
    send.dataset.folderAgentAction = 'answer';
    send.addEventListener('click', () => {
      const hint = String(answer.value || '').trim();
      if (hint) onAction?.('look_further', send, { hint });
    });
    row.append(answer, send);
    ask.append(row);
    card.append(ask);
  }
  if (view.nextTicket) {
    card.append(
      el(
        doc,
        'p',
        'folder-agent-card__next-ticket',
        view.nextTicket.started
          ? `Started: ${view.nextTicket.title}`
          : `First ticket: ${view.nextTicket.title}`
      )
    );
  }
  if (view.goalOffer) card.append(goalOfferSection(doc, view.goalOffer, onAction));
  if (view.actions.length) {
    const actions = el(doc, 'div', 'folder-agent-card__actions');
    view.actions.forEach((action, index) => {
      const button = el(
        doc,
        'button',
        // Only Set it up (or Look further on a look that failed) is primary:
        // putting a report aside is never the suggested answer.
        action.id === 'set_up' ||
          action.id === 'start_next' ||
          (view.state === 'failed' && index === 0)
          ? 'btn btn-primary btn-sm'
          : 'btn btn-sm folder-agent-card__secondary',
        action.label
      );
      button.type = 'button';
      button.dataset.folderAgentAction = action.id;
      if (action.id === 'set_up') setUpButton = button;
      button.addEventListener('click', () => {
        // Set it up carries only the steps the owner left selected.
        const extra = action.id === 'set_up' ? { selected: [...selected] } : undefined;
        return onAction?.(action.id, button, extra);
      });
      actions.append(button);
    });
    card.append(actions);
  }
  const destination = folderAgentDestination(view);
  if (opening && destination.route) {
    const status = el(doc, 'p', 'folder-agent-card__opening', folderAgentOpeningLine(view));
    status.setAttribute('role', 'status');
    card.append(status);
  } else if (
    view.route &&
    !(view.setup && folderAgentReceiptRows(view).some(row => row.link?.href === view.route))
  ) {
    // The receipt's Placed row already opens the place.
    const open = el(doc, 'a', 'folder-agent-card__open', `Open ${view.workspace}`);
    open.href = view.route;
    card.append(open);
  }
  if (error) {
    const alert = el(doc, 'p', 'folder-agent-card__error', error);
    alert.setAttribute('role', 'alert');
    card.append(alert);
  }
  return card;
}

async function readJSON(response) {
  try {
    return await response.json();
  } catch (_) {
    return null;
  }
}

async function post(url, body, fetchImpl) {
  const response = await fetchImpl(url, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
    body: JSON.stringify(body || {})
  });
  return { ok: response.ok, status: response.status, payload: await readJSON(response) };
}

// chooseFolder asks the server to choose a folder ({chip}, {picker:true} or
// {selection_token}) and returns {card} or {cancelled} or {error}.
export async function chooseFolder(body, fetchImpl = globalThis.fetch) {
  const { ok, payload } = await post(`${FOLDER_AGENT_ENDPOINT}/choose`, body, fetchImpl);
  if (!ok) return { error: String(payload?.error || 'That folder could not be chosen.') };
  if (payload?.cancelled) return { cancelled: true };
  return { card: payload?.card || null };
}

export async function startFolderAgent(token, brief, fetchImpl = globalThis.fetch, hire) {
  const body = { choice_token: token, brief };
  if (hire) body.hire = hire;
  const { ok, payload } = await post(`${FOLDER_AGENT_ENDPOINT}/start`, body, fetchImpl);
  if (!ok) {
    return {
      error: String(payload?.error || 'The agent could not be started.'),
      duplicate: payload?.duplicate || null,
      repick: payload?.repair === 'choose_folder'
    };
  }
  return { view: payload?.folder_agent || null };
}

export async function fetchFolderAgent(workspaceID, fetchImpl = globalThis.fetch) {
  const response = await fetchImpl(`${FOLDER_AGENT_ENDPOINT}/${encodeURIComponent(workspaceID)}`, {
    headers: { Accept: 'application/json' }
  });
  const payload = await readJSON(response);
  return response.ok ? payload?.folder_agent || null : null;
}

// A plugin's source link: a plain https URL only.
function safeSourceURL(value) {
  const url = String(value || '');
  return /^https:\/\/[^\s"'<>\\]+$/.test(url) ? url : '';
}

// folderAgentSetUpLabel is the primary button's words for a plan (PRD FR20):
// "Install N & set up" while the ticked steps install N plugins, else
// "Set it up". Every surface that offers the action uses it.
export function folderAgentSetUpLabel(plan) {
  const n = plan?.installs?.length || 0;
  return n > 0 ? `Install ${n} & set up` : ACTION_LABELS.set_up;
}

function planView(plan) {
  return {
    digest: String(plan.digest || ''),
    runnable: Boolean(plan.runnable),
    unavailable: String(plan.unavailable || ''),
    selected: Array.isArray(plan.selected) ? plan.selected.map(String) : [],
    lines: (plan.lines || []).map(line => lineView(line)),
    // Who chose the steps: the agent, in its own words, or Ori's default.
    source: String(plan.source || 'default'),
    words: String(plan.words || ''),
    wordsRaw: String(plan.words_raw || ''),
    excluded: String(plan.excluded || ''),
    defaultReason: String(plan.default_reason || ''),
    reorderNote: String(plan.reorder_note || ''),
    // The plugins the ticked steps download and install, in run order: the
    // install block and the count on the button.
    installs: (Array.isArray(plan.installs) ? plan.installs : [])
      .map(entry => ({
        step: String(entry?.step || ''),
        line: String(entry?.line || ''),
        name: String(entry?.name || ''),
        version: String(entry?.version || ''),
        publisher: String(entry?.publisher || ''),
        sourceLabel: String(entry?.source_label || ''),
        sourceURL: safeSourceURL(entry?.source_url),
        search: Boolean(entry?.search),
        marketplace: String(entry?.marketplace || ''),
        why: String(entry?.why || ''),
        appSentence: String(entry?.app_sentence || '')
      }))
      .filter(entry => entry.name && entry.step),
    // Every step Ori offered, chosen or not; the owner can tick any of them.
    steps: (Array.isArray(plan.steps) ? plan.steps : [])
      .map(step => ({
        handle: String(step.handle || ''),
        kind: String(step.kind || ''),
        name: String(step.name || ''),
        title: String(step.title || ''),
        why: String(step.why || ''),
        label: String(step.label || ''),
        chosen: Boolean(step.chosen),
        excludes: String(step.excludes || '')
      }))
      .filter(step => step.handle)
  };
}

// fetchFolderAgentPlan asks for the plan of a selection of optional tools.
export async function fetchFolderAgentPlan(workspaceID, selected, fetchImpl = globalThis.fetch) {
  if (!workspaceID || typeof fetchImpl !== 'function') return null;
  try {
    const { ok, payload } = await post(
      `${FOLDER_AGENT_ENDPOINT}/${encodeURIComponent(workspaceID)}/plan`,
      { selected },
      fetchImpl
    );
    return ok && payload?.plan ? planView(payload.plan) : null;
  } catch (_) {
    return null;
  }
}

export async function listFolderAgents(fetchImpl = globalThis.fetch) {
  try {
    const response = await fetchImpl(FOLDER_AGENT_ENDPOINT, {
      headers: { Accept: 'application/json' }
    });
    const payload = await readJSON(response);
    return response.ok && Array.isArray(payload?.folder_agents) ? payload.folder_agents : [];
  } catch (_) {
    return [];
  }
}

// postFolderAgentAction sends Set it up (with the plan digest the card
// showed), Keep or Look further, and returns {view} or {error}.
export async function postFolderAgentAction(
  view,
  action,
  fetchImpl = globalThis.fetch,
  extra = {}
) {
  const url = folderAgentActionPath(view?.workspaceID, action);
  if (!url) return { error: 'That action is not available.' };
  let body = extra;
  if (action === 'set_up') body = { plan_digest: view.plan?.digest || '', ...extra };
  else if (action === 'use_goal') {
    const goal = String(extra?.goal || '').trim();
    body = goal ? { answer: 'use', goal } : { answer: 'use' };
  } else if (action === 'dismiss_goal') body = { answer: 'dismiss' };
  else if (action === 'premise_confirm') body = { answer: 'confirmed' };
  else if (action === 'premise_other') body = { answer: 'something_else' };
  const { ok, payload } = await post(url, body, fetchImpl);
  if (!ok) {
    return {
      error: String(payload?.error || 'That did not go through.'),
      view: payload?.folder_agent || null
    };
  }
  // Home's map follows: a Set it up can turn a tile into a district or move
  // it into a Home.
  globalThis.window?.dispatchEvent?.(
    new globalThis.CustomEvent('ori:folder-agent-changed', {
      detail: { workspaceID: view.workspaceID, action }
    })
  );
  return { view: payload?.folder_agent || null };
}

// setupFinished says whether next ends the run previous was in: the place is
// set up and every step is done. A run that stopped is not finished.
export function setupFinished(previous, next) {
  return (
    previous?.state === 'setting_up' && next?.state === 'set_up' && next?.setup?.status === 'done'
  );
}

// landingOf keeps a server landing only when it is one of the app's own
// workspace pages, with the Home's name for the "Opening …" line.
function landingOf(raw) {
  const route = String(raw?.route || '');
  if (!route.startsWith('/workspaces/')) return null;
  return { route, name: String(raw?.name || '').trim() };
}

// folderAgentDestination is where a run the user watched goes once it
// finished: the Home's Map with its projects sheet when the run included the
// Home step (the server's landing), else the folder's own workspace. Every
// follower (the card's loop, Needs you, the Create Workspace pane, the map's
// balloons and the Home drawer) goes through openFolderAgentPlace, which asks
// this, so they all land the same way. Takes a report view or a raw view.
export function folderAgentDestination(view) {
  const landing = view?.landing ? landingOf(view.landing) : null;
  const folder = String(view?.workspace || view?.workspace_name || view?.folder_name || '');
  if (landing) return { route: landing.route, name: landing.name || folder, landing: true };
  return { route: String(view?.route || ''), name: folder, landing: false };
}

// folderAgentOpeningLine is the line a follower shows while it leaves for the
// destination: "Opening Music Production Home…" (PRD FR8).
export function folderAgentOpeningLine(view) {
  const name = folderAgentDestination(view).name;
  return name ? `Opening ${name}…` : 'Opening…';
}

// openFolderAgentPlace takes the user where a run they watched has set up
// (folderAgentDestination: the Home it joined, or the folder's workspace). The
// beat before leaving lets the finished list be seen. navigate is the page's
// own way there; the default is the link's. Returns whether it will go.
// On Home it also waits for the map behind the card to finish building the
// place, or one second, whichever is first (PRD FR20).
export function openFolderAgentPlace(
  view,
  { navigate, delayMs = 900, motion = globalThis.window?.OriWorkspaceMap } = {}
) {
  const route = folderAgentDestination(view).route;
  if (!route) return false;
  const go =
    typeof navigate === 'function' ? navigate : target => globalThis.location?.assign?.(target);
  const settled =
    typeof motion?.whenMotionSettled === 'function' ? motion.whenMotionSettled(1000) : null;
  if (!settled && delayMs <= 0) {
    go(route);
    return true;
  }
  const beat = new Promise(resolve => (delayMs > 0 ? setTimeout(resolve, delayMs) : resolve()));
  Promise.all([beat, settled ? settled.catch(() => {}) : null]).then(() => go(route));
  return true;
}

// The lines whose finish changes what Home's map draws: a Home created, the
// workspace placed, the folder turned into a group, a member made or moved in.
const PLACEMENT_LINE_KINDS = new Set(['home', 'place', 'promote', 'member', 'existing_member']);

// placementLinesDone says whether next shows a placement line done that was
// not done in previous.
export function placementLinesDone(previous, next) {
  const before = new Map((previous?.setup?.lines || []).map(line => [line.name, line.state]));
  return (next?.setup?.lines || []).some(
    line =>
      PLACEMENT_LINE_KINDS.has(line.kind) &&
      line.state === 'done' &&
      before.get(line.name) !== 'done'
  );
}

// notifyPlacement tells Home's map to build the place when a poll shows a
// placement line newly done. Every follower of a run calls it with each pair
// of views (this module's own loop and the Home drawer's), before it opens
// the place, so the map is already expecting its motion. Returns whether it
// fired.
export function notifyPlacement(previous, next) {
  if (!placementLinesDone(previous, next)) return false;
  globalThis.window?.dispatchEvent?.(
    new globalThis.CustomEvent('ori:folder-agent-changed', {
      detail: { workspaceID: String(next?.workspace_id || ''), placement: true }
    })
  );
  return true;
}

// followFolderAgent polls one folder agent while it looks or sets up and hands
// each fresh view to onView(next, finished), finished marking the end of a run
// it watched. Returns a stop function.
export function followFolderAgent(
  view,
  { fetchImpl = globalThis.fetch, isActive = () => true, pollMs = 1500, onView } = {}
) {
  let timer = 0;
  let current = view;
  const stop = () => {
    if (timer) clearTimeout(timer);
    timer = 0;
  };
  const tick = () => {
    stop();
    if (!current?.workspace_id || (current.state !== 'looking' && current.state !== 'setting_up'))
      return;
    timer = setTimeout(async () => {
      timer = 0;
      if (!isActive()) return;
      const next = await fetchFolderAgent(current.workspace_id, fetchImpl).catch(() => null);
      if (!next || !isActive()) return;
      const finished = setupFinished(current, next);
      // Home's map builds the place behind the card as it is made: it
      // refreshes, animated, on placement lines only, never on every poll.
      notifyPlacement(current, next);
      current = next;
      onView?.(next, finished);
      tick();
    }, pollMs);
  };
  tick();
  return stop;
}

// Each Needs you host's follows, by workspace: a look or a run in progress is
// polled until it is final, across the list's redraws.
const reportFollows = new WeakMap();

// mountFolderAgentReports draws every folder agent report among Today items
// into host, wiring the actions. onDone runs after an action posted and when
// a followed look or run ends. A run watched to its end opens the place.
export function mountFolderAgentReports(
  host,
  items,
  { doc = document, onDone, fetchImpl, pollMs, navigate, openDelayMs } = {}
) {
  if (!host) return 0;
  const fetcher = fetchImpl || globalThis.fetch;
  const entries = (Array.isArray(items) ? items : [])
    .filter(item => item?.kind === FOLDER_AGENT_REPORT_KIND)
    .map(item => ({ raw: item.folder_agent, view: folderAgentNeedsView(item) }))
    .filter(entry => entry.view);
  let follows = reportFollows.get(host);
  if (!follows) {
    follows = new Map();
    reportFollows.set(host, follows);
  }
  // replaceCard swaps one workspace's card in place, wherever the list put it.
  const replaceCard = (workspaceID, card) => {
    const current = Array.from(host.children || []).find(
      node => node?.dataset?.workspaceId === workspaceID
    );
    if (current) current.replaceWith(card);
  };
  const draw = (view, error = '', opening = false) =>
    renderFolderAgentReport(view, doc, {
      error,
      opening,
      onAction: async (action, button, extra) => {
        button.disabled = true;
        const result = await postFolderAgentAction(view, action, fetcher, extra);
        if (result.error) {
          const next = result.view ? folderAgentReportView(result.view) : view;
          const card = draw(next, result.error);
          button.closest?.('.folder-agent-card')?.replaceWith?.(card);
          return;
        }
        // Set it up starts a run: the card follows it here until it ends.
        if (result.view) {
          replaceCard(view.workspaceID, draw(folderAgentReportView(result.view)));
          follow(result.view);
        }
        onDone?.();
      }
    });
  const follow = raw => {
    const id = String(raw?.workspace_id || '');
    if (!id) return;
    follows.get(id)?.();
    follows.delete(id);
    if (raw.state !== 'looking' && raw.state !== 'setting_up') return;
    const options = {
      fetchImpl: fetcher,
      isActive: () => host.isConnected !== false,
      onView: (next, finished) => {
        let opening = false;
        if (finished) {
          const open = { navigate };
          if (openDelayMs !== undefined) open.delayMs = openDelayMs;
          opening = openFolderAgentPlace(next, open);
        }
        replaceCard(id, draw(folderAgentReportView(next), '', opening));
        if (next.state === 'looking' || next.state === 'setting_up') return;
        follows.delete(id);
        // The list's counts follow, unless the page is about to leave.
        if (!opening) onDone?.();
      }
    };
    if (pollMs) options.pollMs = pollMs;
    follows.set(id, followFolderAgent(raw, options));
  };
  // A listed agent's follow restarts from the list's own view, so a redraw
  // never doubles one. A run that left the list (Today shows it among the
  // working) is still followed to its end: the place opens all the same.
  host.replaceChildren(...entries.map(entry => draw(entry.view)));
  entries.forEach(entry => follow(entry.raw));
  return entries.length;
}

// mountWorkspaceFolderAgent shows a folder workspace's own report on its
// page: the receipt while the agent looks (polled), then the report and its
// actions. A run watched to its end opens the place (openPlace), the way the
// card's link would; navigate is the page's own way there. A workspace not
// started from a folder leaves the host hidden.
export async function mountWorkspaceFolderAgent(
  host,
  workspaceID,
  {
    doc = document,
    fetchImpl = globalThis.fetch,
    isActive = () => true,
    pollMs = 1500,
    openPlace = true,
    navigate,
    openDelayMs,
    presenter
  } = {}
) {
  if (!host || !workspaceID) return null;
  let raw;
  try {
    raw = await fetchFolderAgent(workspaceID, fetchImpl);
  } catch (_) {
    raw = null;
  }
  if (!isActive()) return null;
  if (!raw) {
    host.hidden = true;
    presenter?.present(null);
    return null;
  }
  // previous and followed feed the map presenter's auto-open rule: what the
  // last view said, and whether the owner acted on this page.
  let previous = null;
  let followed = false;
  const draw = (current, error = '', opening = false) => {
    const view = folderAgentReportView(current);
    const onAction = async (action, button, extra) => {
      if (button) button.disabled = true;
      if (action === 'set_up' || action === 'look_further' || action === 'start_next')
        followed = true;
      const result = await postFolderAgentAction(view, action, fetchImpl, extra);
      draw(result.view || current, result.error || '');
      follow(result.view || current);
    };
    const card = compact =>
      renderFolderAgentReport(view, doc, { error, opening, onAction, compact });
    host.hidden = false;
    // A presenter (the Map tab's Commander) may draw the card in its balloon
    // and shrink this host to one line; otherwise the full card shows here.
    const shown = presenter?.present({
      view,
      raw: current,
      previous,
      followed,
      error,
      opening,
      host,
      doc,
      card
    });
    if (!shown) host.replaceChildren(card(false));
    previous = view;
  };
  let stop = () => {};
  const follow = current => {
    stop();
    stop = followFolderAgent(current, {
      fetchImpl,
      isActive,
      pollMs,
      onView: (next, finished) => {
        let opening = false;
        if (finished && openPlace) {
          const open = { navigate };
          if (openDelayMs !== undefined) open.delayMs = openDelayMs;
          opening = openFolderAgentPlace(next, open);
        }
        draw(next, '', opening);
      }
    });
  };
  draw(raw);
  follow(raw);
  return raw;
}

// mountFolderAgentChoice is the whole card for an entry that is not Home's
// conversation (Create Workspace's pane): the server chooses the folder from
// body ({chip}, {picker:true} or {selection_token}), the card asks what the
// agent should do, Start creates it, and the receipt then follows the look in
// the same host. Resolves to {card} once the card is shown, {cancelled} for a
// closed dialog, or {error} with the server's sentence; nothing is drawn for
// the last two. onStarted(view) runs once Start succeeds. A run watched to its
// end opens the place unless openPlace is false; navigate is the page's way.
export async function mountFolderAgentChoice(
  host,
  body,
  {
    doc = document,
    fetchImpl = globalThis.fetch,
    isActive = () => true,
    onStarted,
    pollMs,
    openPlace,
    navigate,
    openDelayMs
  } = {}
) {
  if (!host) return { error: 'That folder could not be chosen.' };
  let chosen;
  try {
    chosen = await chooseFolder(body, fetchImpl);
  } catch (_) {
    chosen = { error: 'That folder could not be chosen right now.' };
  }
  if (!isActive()) return { cancelled: true };
  if (chosen.error || chosen.cancelled || !chosen.card) {
    return chosen.error ? { error: chosen.error } : { cancelled: true };
  }
  const card = folderAgentCardView(chosen.card);
  let busy = false;
  const draw = (error = '') => {
    host.hidden = false;
    host.replaceChildren(renderFolderAgentCard(card, doc, { error, onStart }));
  };
  const onStart = async (brief, button, hire) => {
    if (busy) return;
    busy = true;
    if (button) button.disabled = true;
    let result;
    try {
      result = await startFolderAgent(card.token, brief, fetchImpl, hire);
    } catch (_) {
      result = { error: 'The agent could not be started right now.' };
    }
    busy = false;
    if (!isActive()) return;
    if (result.error || !result.view?.workspace_id) {
      card.brief = brief;
      const error = result.error || 'The agent could not be started right now.';
      draw(result.duplicate?.name ? `${error} Open ${result.duplicate.name} from the map.` : error);
      return;
    }
    onStarted?.(result.view);
    const options = { doc, fetchImpl, isActive, navigate };
    if (pollMs) options.pollMs = pollMs;
    if (openPlace !== undefined) options.openPlace = openPlace;
    if (openDelayMs !== undefined) options.openDelayMs = openDelayMs;
    await mountWorkspaceFolderAgent(host, result.view.workspace_id, options);
  };
  draw();
  return { card };
}

if (typeof window !== 'undefined') {
  window.FolderAgentCard = {
    folderAgentReportView,
    folderAgentTileLine,
    listFolderAgents,
    mountFolderAgentChoice,
    mountFolderAgentReports
  };
}
