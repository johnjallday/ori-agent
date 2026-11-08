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
  look_further: 'look-further'
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
    action: String(card.action || 'Start'),
    note: String(card.note || '')
  };
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
    route: String(view.route || ''),
    agent,
    state: String(view.state || ''),
    title: '',
    receipt: '',
    progress: '',
    facts: String(view.facts_line || ''),
    stage: String(view.stage_line || ''),
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
    suggestedGoal: String(view.suggested_goal || ''),
    firstTicket: String(view.first_ticket || ''),
    firstTicketWhy: String(view.first_ticket_why || ''),
    question: String(view.question || ''),
    askFolders: (Array.isArray(view.ask_folders) ? view.ask_folders : [])
      .filter(folder => folder?.name && folder?.ref)
      .map(folder => ({ name: String(folder.name), ref: String(folder.ref) })),
    leftOut: view.left_out && typeof view.left_out === 'object' ? { ...view.left_out } : {},
    nextTicket: view.next_ticket?.title
      ? { title: String(view.next_ticket.title), started: Boolean(view.next_ticket.started) }
      : null,
    // The goal is offered while the report waits for an answer.
    offersGoal: false
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
    out.plan = {
      digest: String(view.plan.digest || ''),
      runnable: Boolean(view.plan.runnable),
      unavailable: String(view.plan.unavailable || ''),
      lines: (view.plan.lines || []).map(line => ({
        kind: String(line.kind || ''),
        name: String(line.name || ''),
        detail: String(line.detail || ''),
        state: String(line.state || '')
      }))
    };
    // Set it up waits for a plan that can run; the card says why it cannot.
    if (!out.plan.runnable) out.actions = out.actions.filter(a => a.id !== 'set_up');
  }
  out.offersGoal = Boolean(out.suggestedGoal) && out.actions.some(action => action.id === 'set_up');
  if (view.setup) {
    out.setup = {
      status: String(view.setup.status || ''),
      stopReason: String(view.setup.stop_reason || ''),
      lines: (view.setup.lines || []).map(line => ({
        name: String(line.name || ''),
        detail: String(line.detail || ''),
        state: String(line.state || 'waiting')
      }))
    };
  }
  return out;
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

// renderFolderAgentCard draws the card before Start. onStart(brief, button)
// posts Start; the caller swaps in the receipt.
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
  const note = el(doc, 'p', 'folder-agent-card__note', view.note);
  const actions = el(doc, 'div', 'folder-agent-card__actions');
  const start = el(doc, 'button', 'btn btn-primary folder-agent-card__start', view.action);
  start.type = 'button';
  start.dataset.folderAgentAction = 'start';
  start.addEventListener('click', () => onStart?.(String(input.value || ''), start));
  actions.append(start);
  card.append(title, label, note, actions);
  if (error) {
    const alert = el(doc, 'p', 'folder-agent-card__error', error);
    alert.setAttribute('role', 'alert');
    card.append(alert);
  }
  return card;
}

function renderLines(doc, lines, className) {
  const list = el(doc, 'ul', className);
  lines.forEach(line => {
    const item = el(doc, 'li', 'folder-agent-card__line');
    if (line.state) item.dataset.state = line.state;
    item.append(el(doc, 'span', 'folder-agent-card__line-name', line.name));
    if (line.detail) item.append(el(doc, 'span', 'folder-agent-card__line-detail', line.detail));
    const words = LINE_STATE_WORDS[line.state];
    if (words) item.append(el(doc, 'span', 'folder-agent-card__line-state', words));
    list.append(item);
  });
  return list;
}

// renderFolderAgentReport draws the receipt or the report. onAction(id,
// button, extra) posts one action; the caller refreshes.
export function renderFolderAgentReport(view, doc = document, { onAction, error = '' } = {}) {
  const card = el(doc, 'article', 'folder-agent-card folder-agent-card--report');
  card.dataset.folderAgentCard = view.state;
  card.dataset.workspaceId = view.workspaceID;
  card.append(el(doc, 'div', 'folder-agent-card__by', view.agent));
  card.append(el(doc, 'h3', 'folder-agent-card__title', view.title));
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
  if (view.failure) card.append(el(doc, 'p', 'folder-agent-card__failure', view.failure));
  if (view.modelSentence)
    card.append(el(doc, 'p', 'folder-agent-card__degraded', view.modelSentence));
  const leftOut = section =>
    view.leftOut[section]
      ? el(doc, 'p', 'folder-agent-card__degraded', view.leftOut[section])
      : null;
  const whatItIs = view.whatItIs
    ? el(doc, 'p', 'folder-agent-card__what', view.whatItIs)
    : leftOut('what_it_is');
  if (whatItIs) card.append(whatItIs);
  if (view.facts) card.append(el(doc, 'p', 'folder-agent-card__facts', view.facts));
  if (view.tools.length || view.toolsSentence) {
    const tools = el(doc, 'section', 'folder-agent-card__tools');
    tools.append(el(doc, 'h4', 'folder-agent-card__heading', 'Tools'));
    const list = el(doc, 'ul', 'folder-agent-card__tool-list');
    view.tools.forEach(tool => {
      const item = el(doc, 'li', 'folder-agent-card__tool');
      if (tool.greyed) item.dataset.greyed = 'true';
      if (tool.required) item.dataset.required = 'true';
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
  // Suggested goal: an editable line with "Use this goal" checked while the
  // report waits; plain text afterwards.
  let goalInput = null;
  let goalUse = null;
  if (view.offersGoal) {
    const goal = el(doc, 'section', 'folder-agent-card__goal');
    goal.append(el(doc, 'h4', 'folder-agent-card__heading', 'Suggested goal'));
    goalInput = doc.createElement('input');
    goalInput.type = 'text';
    goalInput.className = 'folder-agent-card__goal-input';
    goalInput.maxLength = 300;
    goalInput.value = view.suggestedGoal;
    goalInput.setAttribute('aria-label', 'Suggested goal');
    const label = el(doc, 'label', 'folder-agent-card__goal-use');
    goalUse = doc.createElement('input');
    goalUse.type = 'checkbox';
    goalUse.checked = true;
    label.append(goalUse, el(doc, 'span', '', 'Use this goal'));
    goal.append(goalInput, label);
    card.append(goal);
  } else if (view.suggestedGoal) {
    card.append(
      el(doc, 'p', 'folder-agent-card__goal-text', `Suggested goal: ${view.suggestedGoal}`)
    );
  } else {
    const missing = leftOut('suggested_goal');
    if (missing) card.append(missing);
  }
  if (view.firstTicket) {
    const ticket = el(doc, 'section', 'folder-agent-card__ticket');
    ticket.append(el(doc, 'h4', 'folder-agent-card__heading', 'Suggested first ticket'));
    ticket.append(el(doc, 'p', 'folder-agent-card__ticket-title', view.firstTicket));
    if (view.firstTicketWhy)
      ticket.append(el(doc, 'p', 'folder-agent-card__ticket-why', view.firstTicketWhy));
    card.append(ticket);
  } else {
    const missing = leftOut('suggested_first_ticket');
    if (missing) card.append(missing);
  }
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
  if (view.setup) {
    card.append(renderLines(doc, view.setup.lines, 'folder-agent-card__plan'));
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
  } else if (view.stage) {
    card.append(el(doc, 'p', 'folder-agent-card__stage', view.stage));
  }
  if (view.plan && !view.setup && view.state !== 'setting_up') {
    if (view.plan.runnable) {
      card.append(renderLines(doc, view.plan.lines, 'folder-agent-card__plan'));
    } else if (view.plan.unavailable) {
      card.append(el(doc, 'p', 'folder-agent-card__note', view.plan.unavailable));
    }
  }
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
      button.addEventListener('click', () => {
        // Set it up carries the goal line as the owner left it.
        const extra =
          action.id === 'set_up' && goalInput
            ? { use_goal: Boolean(goalUse?.checked), goal: String(goalInput.value || '') }
            : undefined;
        return onAction?.(action.id, button, extra);
      });
      actions.append(button);
    });
    card.append(actions);
  }
  if (view.route) {
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

export async function startFolderAgent(token, brief, fetchImpl = globalThis.fetch) {
  const { ok, payload } = await post(
    `${FOLDER_AGENT_ENDPOINT}/start`,
    { choice_token: token, brief },
    fetchImpl
  );
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
  const body = action === 'set_up' ? { plan_digest: view.plan?.digest || '', ...extra } : extra;
  const { ok, payload } = await post(url, body, fetchImpl);
  if (!ok) {
    return {
      error: String(payload?.error || 'That did not go through.'),
      view: payload?.folder_agent || null
    };
  }
  return { view: payload?.folder_agent || null };
}

// mountFolderAgentReports draws every folder agent report among Today items
// into host, wiring the actions. onDone runs after an action posted.
export function mountFolderAgentReports(host, items, { doc = document, onDone, fetchImpl } = {}) {
  if (!host) return 0;
  const views = (Array.isArray(items) ? items : [])
    .filter(item => item?.kind === FOLDER_AGENT_REPORT_KIND)
    .map(folderAgentNeedsView)
    .filter(Boolean);
  const draw = (view, error = '') =>
    renderFolderAgentReport(view, doc, {
      error,
      onAction: async (action, button, extra) => {
        button.disabled = true;
        const result = await postFolderAgentAction(
          view,
          action,
          fetchImpl || globalThis.fetch,
          extra
        );
        if (result.error) {
          const next = result.view ? folderAgentReportView(result.view) : view;
          const card = draw(next, result.error);
          button.closest?.('.folder-agent-card')?.replaceWith?.(card);
          return;
        }
        onDone?.();
      }
    });
  host.replaceChildren(...views.map(view => draw(view)));
  return views.length;
}

// mountWorkspaceFolderAgent shows a folder workspace's own report on its
// page: the receipt while the agent looks (polled), then the report and its
// actions. A workspace not started from a folder leaves the host hidden.
export async function mountWorkspaceFolderAgent(
  host,
  workspaceID,
  { doc = document, fetchImpl = globalThis.fetch, isActive = () => true, pollMs = 1500 } = {}
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
    return null;
  }
  const draw = (current, error = '') => {
    const view = folderAgentReportView(current);
    host.hidden = false;
    host.replaceChildren(
      renderFolderAgentReport(view, doc, {
        error,
        onAction: async (action, button, extra) => {
          if (button) button.disabled = true;
          const result = await postFolderAgentAction(view, action, fetchImpl, extra);
          draw(result.view || current, result.error || '');
          follow(result.view || current);
        }
      })
    );
  };
  let timer = 0;
  const follow = current => {
    if (timer) clearTimeout(timer);
    timer = 0;
    if (current?.state !== 'looking' && current?.state !== 'setting_up') return;
    timer = setTimeout(async () => {
      if (!isActive()) return;
      const next = await fetchFolderAgent(workspaceID, fetchImpl).catch(() => null);
      if (next && isActive()) {
        draw(next);
        follow(next);
      }
    }, pollMs);
  };
  draw(raw);
  follow(raw);
  return raw;
}

if (typeof window !== 'undefined') {
  window.FolderAgentCard = {
    folderAgentReportView,
    folderAgentTileLine,
    listFolderAgents,
    mountFolderAgentReports
  };
}
