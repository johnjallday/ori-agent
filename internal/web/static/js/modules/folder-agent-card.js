// Hire an agent for <folder>: the one card every folder entry shows, the
// receipt after Start, and the folder agent's report with Set it up, Keep the
// folder as it is and Look further. The server holds the folder; nothing here
// sends a path. Words come from the server's view (facts line, stage line,
// tools); this module only lays them out.

import { documentsOrganizationOffer } from './documents-organization.js';
import { renderHireDraftEditor } from './folder-agent-hire-editor.js';
import { publishFolderAgentChat } from './folder-agent-chat.js';
import {
  chatDestination,
  chatFollowUp,
  chatPlacementLine,
  chatSetupOf
} from './folder-agent-chat-flow.js';
import { fetchWorkspaceProtection, protectionFactView } from './workspace-protection.js';

export const FOLDER_AGENT_ENDPOINT = '/api/personal-assistant/folder-agent';
// How many times the workspace page asks again for the report's message after
// the look ended (folder-agent-chat.js).
const CHAT_RETRIES = 4;
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
  // The offer to learn the songs (Learn them, Not now), asked before the goal.
  learn_songs: 'songs',
  dismiss_songs: 'songs',
  // Try again on a reading that stopped (the owner already said yes).
  retry_songs: 'songs',
  // The premise's answers (Yes, show me the plan, and It's something else).
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
    title: String(card.title || `Hire an agent for ${card.folder_name || 'this folder'}`),
    purpose: String(card.purpose || ''),
    field: String(card.field || 'What should it do?'),
    brief: String(card.brief || ''),
    max: Number(card.brief_max) > 0 ? Number(card.brief_max) : 300,
    action: String(card.action || 'Hire & build'),
    actionExisting: String(card.action_existing || 'Assign & build'),
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
    .map(a => ({ name: String(a.name), model: String(a.model || ''), runs: String(a.runs || '') }));
  const existing = agents.some(a => a.name === source.existing) ? String(source.existing) : '';
  return {
    name: String(source.agent_name || `${folderName || 'Folder'} Manager`),
    nameMax: Number(source.name_max) > 0 ? Number(source.name_max) : 60,
    character: String(source.character || ''),
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
    mandate: String(hire.mandate || '').trim(),
    character: String(hire.character || '')
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
  // Reading the songs after "Learn them": the host's own line, "Learning <song>…
  // (2 of 7)". It goes when the reading ends; the card keeps the closing line.
  if (folderAgentReading(view) && view.learning.line) return String(view.learning.line);
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
    projectHolders: view.project_holders || null,
    rootPolicy: String(view.root_policy || ''),
    organizationReview: view.organization_review === true,
    policyNote: String(view.policy_note || ''),
    // The setup is done but filing was not allowed yet: one step left.
    filingPermissionRequired: Boolean(view.filing_permission_required),
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
          maxRunes: Number(view.goal_offer.max_runes) > 0 ? Number(view.goal_offer.max_runes) : 300,
          // "learned": written by the agent after it learned the songs.
          basis: String(view.goal_offer.basis || '')
        }
      : null,
    // The Commander's offer to learn the songs, made once after a setup that
    // put the folder in a Home, and before the goal (learn-the-songs FR1–FR5).
    // Every word is the host's; nothing is read until the owner says yes.
    songsOffer: view.songs_offer?.ask
      ? {
          count: Number(view.songs_offer.count) || 0,
          noun: String(view.songs_offer.noun || 'songs'),
          // The one application every readable song is from; empty for
          // a mixed folder or a format the host cannot name.
          format: String(view.songs_offer.format || ''),
          ask: String(view.songs_offer.ask),
          reads: String(view.songs_offer.reads || ''),
          notRead: String(view.songs_offer.not_read || ''),
          // The reviewed app plugin that is installed but cannot read these songs
          // yet: the card says to update it, and there is nothing to learn.
          update: String(view.songs_offer.update || '')
        }
      : null,
    // The reading of the songs once "Learn them" was said: the line to show
    // while it runs, and how it ended (FR14).
    learning: view.learning?.line
      ? {
          status: String(view.learning.status || ''),
          line: String(view.learning.line),
          done: Number(view.learning.done) || 0,
          total: Number(view.learning.total) || 0,
          notRead: String(view.learning.not_read || ''),
          // The agent's own words about what it learned, written after the
          // facts (FR17); empty when the host's line stands in (FR18), which
          // is then part of `line`.
          summary: String(view.learning.summary || ''),
          summaryBy: String(view.learning.summary_by || ''),
          // The app assistant reading the songs, and why a stopped reading
          // stopped with the one thing to do about it (assistant-reads-projects
          // FR33, FR35). The host's words; the card only draws them.
          reader: String(view.learning.reader || ''),
          reason: String(view.learning.reason || ''),
          action: String(view.learning.action || ''),
          // The reviewed app plugin that owns some of these songs and is
          // installed without a reader, once the reading ended: the card says
          // to update it rather than calling those songs unreadable (FR49).
          update: String(view.learning.update || '')
        }
      : null
  };
  // A refused confirmation redraws from a fresh server view, but its pending
  // picks still belong to this card. Keep the same by-name choice state.
  if (view.hireFaces instanceof Map) out.hireFaces = view.hireFaces;
  if (view.hireDrafts instanceof Map) out.hireDrafts = view.hireDrafts;
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
    out.title = view.filing_permission_required
      ? `${agent} is hired. One step left: allow filing.`
      : `${out.workspace} is set up`;
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
    if (!out.plan.runnable && out.rootPolicy !== 'documents_inbox')
      out.actions = out.actions.filter(a => a.id !== 'set_up');
    // The button names installation without repeating the block's count.
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
    // The names of a few of the owner's own songs, so they can check the
    // recognition, and how many more there are. Folder names from disk: only
    // ever drawn as text.
    names: Array.isArray(raw.names)
      ? raw.names.map(name => String(name ?? '').trim()).filter(Boolean)
      : [],
    namesMore: Math.max(0, Math.trunc(Number(raw.names_more) || 0)),
    // What the plan after the answer will make, in one sentence (absent when
    // the host cannot say it truthfully), and that the answer changes nothing.
    outcome: String(raw.outcome || '').trim(),
    nothingYet: String(raw.nothing_yet || '').trim(),
    confirm: String(raw.confirm || ''),
    other: String(raw.other || ''),
    withdrawn: Boolean(raw.withdrawn)
  };
}

// The names line under the question: "Album-1 · Album-2 · Album-3 · and 3 more".
// '' when the host sent no names.
export function folderAgentPremiseNamesLine(premise) {
  const names = Array.isArray(premise?.names) ? premise.names : [];
  if (!names.length) return '';
  const parts = names.slice();
  if (premise.namesMore > 0) parts.push(`and ${premise.namesMore} more`);
  return parts.join(' · ');
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
    unreached: Math.max(0, Number(receipt.unreached) || 0),
    partialListing: Boolean(receipt.partial_listing)
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
  if (receipt.listed || receipt.unreached || receipt.partialListing) {
    const noun = receipt.listedNoun || plural(receipt.listed, 'song', 'songs');
    const words = [`${receipt.listed} ${noun} on the shelf`];
    if (receipt.partialListing) words.push('Partial listing');
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

function agentEditorLink(doc, name) {
  const link = el(doc, 'a', 'folder-agent-card__agent-edit-link', 'Edit agent');
  link.href = `/agents/${encodeURIComponent(name)}`;
  link.target = '_blank';
  link.rel = 'noopener noreferrer';
  link.setAttribute('aria-label', `Edit ${name} (opens in a new tab)`);
  link.setAttribute(
    'title',
    'Profile edits apply wherever this agent is used. Shared edits require confirmation.'
  );
  return link;
}

// renderReceipt draws the receipt rows as a list of label, words and link.
function renderReceipt(doc, rows, view, agentRecords) {
  const list = el(doc, 'dl', 'folder-agent-card__receipt-rows');
  rows.forEach(row => {
    const item = el(doc, 'div', 'folder-agent-card__receipt-row');
    item.dataset.kind = row.label.toLowerCase();
    const words = el(doc, 'dd', 'folder-agent-card__receipt-words');
    if (row.label === 'Hired' && view?.setup?.receipt?.hired?.length) {
      view.setup.receipt.hired.forEach(name => {
        const agent = el(doc, 'span', 'folder-agent-card__receipt-agent');
        agent.append(
          renderHiredFace(doc, name, '', agentRecords, true),
          el(doc, 'span', 'folder-agent-card__receipt-agent-name', name),
          agentEditorLink(doc, name)
        );
        words.append(agent);
      });
    } else words.append(el(doc, 'span', 'folder-agent-card__receipt-text', row.words));
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

// Pending creates can use their confirmed face. Finished/existing agents and
// receipts use only the current profile, never a historical suggestion.
function renderHiredFace(doc, name, character, agentRecords, actualOnly = false) {
  const win = cardWindow(doc);
  const face = el(doc, 'span', 'folder-agent-card__hired-face');
  face.dataset.agentName = name;
  face.setAttribute('aria-hidden', 'true');
  let records = null;
  let answered = false;
  const draw = () => {
    if (actualOnly && !answered) return;
    const record = records?.get?.(name);
    const input = record
      ? agentFace({ name }, record, win)
      : hireFace(name, win, actualOnly ? '' : character);
    const markup = faceMarkup(win, input, '', 28);
    if (markup) face.innerHTML = markup;
    else face.textContent = (name[0] || '?').toUpperCase();
  };
  draw();
  watchFaces(face, win, draw, doc);
  void Promise.resolve(agentRecords?.()).then(found => {
    if (face.isConnected === false) return;
    records = found;
    answered = true;
    draw();
  });
  return face;
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
    // The host's mark: the outcome-line item this action belongs to, or ''
    // for a detail. The card never works it out from the words.
    outcome: String(line.outcome || ''),
    agentName: String(line.agent_name || ''),
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
  const label = el(doc, 'label', 'folder-agent-card__who-field');
  label.append(el(doc, 'span', 'folder-agent-card__label', caption), control);
  return label;
}

// The agent a hire creates is an orchestrator with the default generated
// appearance (internal/sessionhttp/folder_agent_create.go, folderAgentConfig).
// The Agents page draws it from the inputs below, so the card draws the same
// face (visual-hire G3, FR4). A name-only call would draw another face: the
// generated face is seeded from the name and the source, and the role adds the
// ring and emblem.
const HIRED_AGENT_ROLE = 'orchestrator';
const HIRED_AGENT_SOURCE = 'user';
const NEUTRAL_FACE = { name: '' };
const FACE_SIZE = 48;

// hiredName is the name the server saves: a run of whitespace is one space
// (resolveHire).
function hiredName(value) {
  return String(value || '')
    .trim()
    .split(/\s+/)
    .filter(Boolean)
    .join(' ');
}

// hireFace is the renderer's input for the name typed. It is the neutral face
// while there is no name, and while the role catalog that shapes the ring has
// not arrived: never a wrong face (FR3, FR4).
function hireFace(name, win, characterId = '') {
  const who = hiredName(name);
  const roles = win?.RoleCatalog;
  if (!who || !roles?.isLoaded?.()) return NEUTRAL_FACE;
  const role = roles.entry?.(HIRED_AGENT_ROLE) || null;
  const character = characterId ? win.CharacterCatalog?.get?.(characterId) : null;
  return {
    name: who,
    source: HIRED_AGENT_SOURCE,
    role: HIRED_AGENT_ROLE,
    roleAccent: role?.accent_color || '',
    roleEmblem: role?.emblem || '',
    appearance: character
      ? { mode: 'character', generated: {}, character: { catalog_id: characterId } }
      : { mode: 'generated', generated: {} },
    ...(character ? { character } : {})
  };
}

// faceMarkup draws one face through the shared renderer, or returns '' when the
// page does not have it.
function faceMarkup(win, input, className, size = FACE_SIZE) {
  if (typeof win?.AgentAvatar?.markup !== 'function') return '';
  return win.AgentAvatar.markup(input, { size, className });
}

const AGENT_FACE_SIZE = 40;

// agentFace is the renderer's input for one of the owner's agents. Before the
// agents list answers it is the name's generated face (FR9). Once it has, it is
// built like agents-roster.js avatarInput: the canonical appearance exactly as
// returned (the renderer picks the active source; nothing here infers it), the
// source and role, the role's ring and emblem, and the character once the
// catalog has it. Without the role catalog the ring is unknown, so the face
// stays the fallback rather than a wrong one.
export function agentFace(agent, record, win) {
  const roles = win?.RoleCatalog;
  if (!record || !roles?.isLoaded?.()) return { name: agent.name, source: 'user', appearance: {} };
  const role = String(record.role || '');
  const entry = role ? roles.entry?.(role) || null : null;
  const appearance =
    record.appearance && typeof record.appearance === 'object' ? record.appearance : {};
  const characterId = String(appearance.character?.catalog_id || '').trim();
  return {
    name: String(record.name || agent.name),
    source: String(record.source || 'user').toLowerCase() === 'cli' ? 'cli' : 'user',
    role,
    roleAccent: entry?.accent_color || '',
    roleEmblem: entry?.emblem || '',
    appearance,
    character: characterId ? win.CharacterCatalog?.get?.(characterId) || null : null
  };
}

const TILE_FACE_SIZE = 32;

// folderAgentTileFaces is the face each working folder agent wears on its tile
// on Home's map (visual-hire FR17): the agent drawn from its record in the agents
// list, exactly as the hire card and the Agents page draw it, so a hired agent
// and a reused one are both the face the person saw. Only an agent whose tile
// shows a line (it looks around or sets up) has one, so it goes with the look.
// Without the shared renderer nobody gets a face. `pending` names the agents whose
// record has been asked for and not yet answered: they get no face rather than the
// name's own, which is not theirs and would flash before their real one. Once the
// lookup ends (found or failed) they are no longer pending, and a name the list
// lacks falls back to its own face. Answers { workspaceID: markup }.
export function folderAgentTileFaces(views, records, win, pending = null) {
  const out = {};
  // The role's ring and emblem are part of the face: until the role catalog is
  // here there is no face rather than a wrong one.
  if (!win?.RoleCatalog?.isLoaded?.()) return out;
  (Array.isArray(views) ? views : []).forEach(view => {
    if (!view?.workspace_id || !view.agent_name || !folderAgentTileLine(view)) return;
    if (pending?.has?.(String(view.agent_name))) return;
    const agent = { name: String(view.agent_name) };
    const markup = faceMarkup(
      win,
      agentFace(agent, records?.get?.(agent.name), win),
      '',
      TILE_FACE_SIZE
    );
    if (markup) out[view.workspace_id] = markup;
  });
  return out;
}

// fetchAgentRecords asks for the owner's agents with their appearance, because
// the hire payload names only name and model. It answers a map by name, or null
// when the list cannot be read: the faces then stay the fallback.
export async function fetchAgentRecords(fetchImpl) {
  try {
    const response = await fetchImpl('/api/agents/dashboard/list');
    if (!response?.ok) return null;
    const data = await response.json();
    const items = Array.isArray(data) ? data : Array.isArray(data?.agents) ? data.agents : [];
    return new Map(items.filter(item => item?.name).map(item => [String(item.name), item]));
  } catch {
    return null;
  }
}

const ROLE_CATALOG_SRC = '/js/modules/role-catalog.js';
const roleCatalogLoads = new WeakMap();

// ensureRoleCatalog makes the role catalog available on a page that did not load
// it (only the Agents page does): the first time a face needs its ring and
// emblem the script is added, once per window, so no page that never draws one
// of these faces pays for the fetch it makes. Answers the catalog once it has
// loaded, or null when it cannot be had (no window, no head, a script that
// fails): the face then stays neutral rather than wrong.
export function ensureRoleCatalog(doc, win) {
  if (!win) return Promise.resolve(null);
  const ready = () =>
    Promise.resolve(win.RoleCatalog?.ready?.()).then(() => win.RoleCatalog || null);
  if (win.RoleCatalog) return ready();
  if (!roleCatalogLoads.has(win)) {
    roleCatalogLoads.set(
      win,
      new Promise(resolve => {
        const script = doc?.createElement?.('script');
        if (!script || typeof doc.head?.appendChild !== 'function') return resolve(null);
        script.src = ROLE_CATALOG_SRC;
        script.async = true;
        script.onload = () => resolve(null);
        script.onerror = () => resolve(null);
        doc.head.appendChild(script);
        return undefined;
      }).then(ready)
    );
  }
  return roleCatalogLoads.get(win);
}

// watchFaces redraws when a catalog that shapes a face arrives or changes. A
// character is only drawn once the catalog has it, and the role ring only once
// the role catalog is here (fetched now if the page has not got it). The card
// stops listening when it is no longer on the page.
function watchFaces(card, win, redraw, doc = null) {
  if (!win) return;
  let stop = null;
  const again = () => {
    if (card.isConnected === false) {
      stop?.();
      stop = null;
      return;
    }
    redraw();
  };
  const characters = win.CharacterCatalog;
  if (typeof characters?.onChange === 'function') stop = characters.onChange(again);
  characters?.load?.();
  void ensureRoleCatalog(doc, win).then(again);
}

// cardWindow is the window whose renderer and catalogs the card draws with.
function cardWindow(doc, win) {
  return win || doc?.defaultView || globalThis.window || null;
}

// renderHire draws the Hire section and keeps hire (the view's own object)
// in step with what the owner picks. The new agent is a face with its name
// beside it; clicking or typing there chooses a new agent. The owner's own
// agents are folded under a counted summary; choosing one dims
// the new agent. fetchImpl reads the agents' appearance (once per card).
function renderHire(doc, hire, win, fetchImpl, onSelect, brief) {
  const group = `folder-agent-hire-${++hireSeq}`;
  const box = el(doc, 'fieldset', 'folder-agent-card__who');
  box.append(el(doc, 'legend', 'folder-agent-card__who-title', 'Hire'));

  const fresh = el(doc, 'div', 'folder-agent-card__who-new');
  fresh.dataset.folderAgentHire = 'new';
  const canPick = typeof win?.CharacterPicker?.open === 'function';
  const face = el(doc, canPick ? 'button' : 'span', 'folder-agent-card__who-face');
  if (canPick) {
    face.type = 'button';
    face.setAttribute('aria-label', 'Change face');
    face.addEventListener('click', event => {
      event?.stopPropagation?.();
      void chooseFace(face);
    });
    // Focusing the chooser must not silently replace an existing-agent choice
    // when the person then cancels the picker.
    face.addEventListener('focusin', event => event?.stopPropagation?.());
  } else face.setAttribute('aria-hidden', 'true');
  const main = el(doc, 'div', 'folder-agent-card__who-main');
  const name = doc.createElement('input');
  name.type = 'text';
  name.className = 'form-control form-control-sm folder-agent-card__who-name';
  name.maxLength = hire.nameMax;
  name.value = hire.name;
  name.autocomplete = 'off';
  name.placeholder = 'Name';
  name.setAttribute('aria-label', 'Name');
  const drawFace = () => {
    const markup = faceMarkup(
      win,
      hireFace(hire.name, win, hire.character),
      'folder-agent-card__who-avatar'
    );
    if (markup) face.innerHTML = markup;
    else face.textContent = (hiredName(hire.name)[0] || '?').toUpperCase();
  };
  name.addEventListener('input', () => {
    hire.name = String(name.value || '');
    select('new');
    drawFace();
  });
  main.append(name);
  let model = null;
  if (hire.models.length) {
    model = doc.createElement('select');
    // The model is a chip under the name. Its text is the model picked, and the
    // native list lies transparently over it, so pressing the chip opens the same
    // list with the keyboard and screen readers as before. A bare select would
    // size to its longest option and draw no caret.
    model.className = 'folder-agent-card__who-model';
    model.setAttribute('aria-label', 'Model');
    const chip = el(doc, 'span', 'folder-agent-card__who-chip');
    const chipText = el(doc, 'span', 'folder-agent-card__who-chip-text');
    chipText.setAttribute('aria-hidden', 'true');
    chip.append(chipText, model);
    // Native selects can retain :focus-visible after a pointer click (notably
    // following text input). Suppress only that pointer-origin ring; keyboard
    // interaction or leaving the chip restores the usual focus-visible rule.
    model.addEventListener('pointerdown', () => {
      chip.dataset.pointerFocus = 'true';
    });
    const keyboardFocus = () => {
      delete chip.dataset.pointerFocus;
    };
    model.addEventListener('keydown', keyboardFocus);
    model.addEventListener('blur', keyboardFocus);
    const chipLabel = () =>
      hire.models.find(item => item.provider === hire.provider && item.model === hire.model)
        ?.label || 'No model yet';
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
      chipText.textContent = chipLabel();
    });
    chipText.textContent = chipLabel();
    main.append(chip);
  } else {
    main.append(
      el(
        doc,
        'p',
        'folder-agent-card__who-note',
        'No model is set up yet. It still looks around and reports without one.'
      )
    );
  }
  fresh.append(face, main);
  box.append(fresh);
  const mandate = doc.createElement('input');
  mandate.type = 'text';
  mandate.className = 'form-control form-control-sm folder-agent-card__who-role';
  mandate.maxLength = 300;
  mandate.value = hire.mandate;
  mandate.placeholder = 'Same as what it should do';
  mandate.autocomplete = 'off';
  mandate.addEventListener('input', () => {
    hire.mandate = String(mandate.value || '');
    select('new');
  });
  // Only a role that differs from the visible brief needs the extra row open.
  const customize = el(doc, 'details', 'folder-agent-card__who-customize');
  customize.open = Boolean(
    hire.mandate.trim() && hire.mandate.trim() !== String(brief || '').trim()
  );
  customize.append(el(doc, 'summary', 'folder-agent-card__who-customize-toggle', 'Customize'));
  if (canPick) {
    const changeFace = el(doc, 'button', 'folder-agent-card__who-change-face', 'Change face');
    changeFace.type = 'button';
    changeFace.addEventListener('click', () => {
      void chooseFace(changeFace);
    });
    customize.append(changeFace);
  }
  customize.append(hireField(doc, 'Role', mandate));
  box.append(customize);

  // The owner's own agents: one row of faces, each a visually hidden radio in a
  // label, so the arrow keys, the checked state and the announcement come from the
  // browser. Each choice is announced with the agent's name and model. The row
  // scrolls sideways and never wraps (FR8, FR11, FR13).
  const choices = [];
  let reuse = null;
  let heading = null;
  if (hire.agents.length) {
    reuse = el(doc, 'details', 'folder-agent-card__who-reuse');
    reuse.open = hire.mode === 'existing';
    heading = el(doc, 'summary', 'folder-agent-card__who-heading');
    heading.id = `${group}-heading`;
    const row = el(doc, 'div', 'folder-agent-card__who-agents');
    row.setAttribute('role', 'radiogroup');
    row.setAttribute('aria-labelledby', heading.id);
    hire.agents.forEach(agent => {
      const item = el(doc, 'label', 'folder-agent-card__who-agent');
      item.setAttribute('title', agent.name);
      const radio = doc.createElement('input');
      radio.type = 'radio';
      radio.name = group;
      radio.value = agent.name;
      radio.className = 'folder-agent-card__who-agent-radio';
      radio.setAttribute(
        'aria-label',
        [agent.name, agent.model, agent.runs ? `runs ${agent.runs}` : ''].filter(Boolean).join(', ')
      );
      const agentFaceBox = el(doc, 'span', 'folder-agent-card__who-agent-face');
      agentFaceBox.setAttribute('aria-hidden', 'true');
      item.append(
        radio,
        agentFaceBox,
        el(doc, 'span', 'folder-agent-card__who-agent-name', agent.name)
      );
      if (agent.runs)
        item.append(el(doc, 'span', 'folder-agent-card__who-agent-runs', `runs ${agent.runs}`));
      radio.addEventListener('change', () => {
        if (!radio.checked) return;
        select('existing', agent.name);
        item.scrollIntoView?.({ block: 'nearest', inline: 'nearest' });
      });
      choices.push({ agent, item, radio, face: agentFaceBox });
      row.append(item);
    });
    reuse.append(heading, row);
    box.append(reuse);
  }

  // The owner's agents as the list answered them, by name; empty until it does.
  let records = new Map();
  const recordsReady = fetchImpl ? fetchAgentRecords(fetchImpl) : Promise.resolve(null);
  let picking = false;
  async function chooseFace(trigger) {
    if (!canPick || picking) return;
    picking = true;
    // Bootstrap's sheet traps document focus. The shared picker mounts beside
    // it, so let focus reach only that overlay without disabling the sheet's
    // trap or relying on Bootstrap's private fields. Restore normal handling
    // as soon as the picker resolves (including cancellation).
    const pickerFocus = event => {
      if (event.target?.closest?.('#charPickerHost')) event.stopImmediatePropagation();
    };
    try {
      const found = await recordsReady;
      if (found) records = found;
      if (box.isConnected === false) return;
      const taken = [...records.values()]
        .filter(record => record.appearance?.mode === 'character')
        .map(record => record.appearance.character?.catalog_id)
        .filter(Boolean);
      doc.addEventListener?.('focusin', pickerFocus, true);
      const answer = await win.CharacterPicker.open({
        selectedId: hire.character,
        taken,
        role: HIRED_AGENT_ROLE,
        trigger,
        showSkip: true
      });
      if (box.isConnected === false) return;
      if (answer?.action === 'choose' || answer?.action === 'skip') {
        hire.character = answer.action === 'choose' ? String(answer.catalogId || '') : '';
        select('new');
        drawFace();
      }
    } finally {
      doc.removeEventListener?.('focusin', pickerFocus, true);
      picking = false;
    }
  }
  const drawAgentFaces = () => {
    choices.forEach(({ agent, face: agentFaceBox }) => {
      const markup = faceMarkup(
        win,
        agentFace(agent, records.get(agent.name), win),
        'folder-agent-card__who-avatar folder-agent-card__who-avatar--agent',
        AGENT_FACE_SIZE
      );
      if (markup) agentFaceBox.innerHTML = markup;
      else agentFaceBox.textContent = (agent.name[0] || '?').toUpperCase();
    });
  };
  const drawFaces = () => {
    drawFace();
    drawAgentFaces();
  };

  // Exactly one is chosen: the new agent or one of the owner's. The new agent's
  // row is dimmed, not disabled, while one of theirs is chosen; clicking, focusing
  // or typing in it chooses a new agent again (FR10). The chosen agent is scrolled
  // into view.
  function select(mode, picked) {
    if (picked) hire.existing = picked;
    const chosen =
      mode === 'existing' ? choices.find(choice => choice.agent.name === hire.existing) : null;
    hire.mode = chosen ? 'existing' : 'new';
    onSelect?.(hire.mode);
    if (heading)
      heading.textContent = chosen
        ? `Using ${chosen.agent.name}`
        : `Use one of your agents (${hire.agents.length})`;
    fresh.dataset.selected = String(!chosen);
    // Role belongs to the new agent, so it dims with it.
    customize.dataset.selected = String(!chosen);
    choices.forEach(choice => {
      const on = choice === chosen;
      choice.radio.checked = on;
      choice.item.dataset.selected = String(on);
    });
    return chosen;
  }
  const chooseNew = () => select('new');
  fresh.addEventListener('click', chooseNew);
  fresh.addEventListener('focusin', chooseNew);
  const preselected = select(hire.mode);
  drawFaces();
  watchFaces(box, win, drawFaces, doc);
  // A saved manager of the same name is already chosen when the card opens
  // (FR12): bring it into view once the card is on the page.
  if (preselected && win?.requestAnimationFrame) {
    win.requestAnimationFrame(() =>
      preselected.item.scrollIntoView?.({ block: 'nearest', inline: 'nearest' })
    );
  }
  if (fetchImpl) {
    void recordsReady.then(found => {
      if (!found || box.isConnected === false) return;
      records = found;
      drawAgentFaces();
    });
  }
  return box;
}

// renderFolderAgentCard draws the card before Start. onStart(brief, button,
// hire) posts Hire & build; the caller swaps in the receipt. win is the window
// whose avatar renderer and catalogs draw the hire section's faces (the page's
// own by default); fetchImpl reads the owner's agents' appearance (the page's
// own fetch by default, none without a window).
export function renderFolderAgentCard(
  view,
  doc = document,
  { onStart, error = '', win = null, fetchImpl = null } = {}
) {
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
  const hireWindow = cardWindow(doc, win);
  const start = el(doc, 'button', 'btn btn-primary folder-agent-card__start', view.action);
  const hire = view.hire
    ? renderHire(
        doc,
        view.hire,
        hireWindow,
        fetchImpl || hireWindow?.fetch?.bind(hireWindow),
        mode => {
          start.textContent = mode === 'existing' ? view.actionExisting : view.action;
        },
        view.brief
      )
    : null;
  const note = el(doc, 'p', 'folder-agent-card__note', view.note);
  const actions = el(doc, 'div', 'folder-agent-card__actions');
  start.type = 'button';
  start.dataset.folderAgentAction = 'start';
  start.addEventListener('click', () =>
    onStart?.(String(input.value || ''), start, folderAgentHireChoice(view.hire))
  );
  actions.append(start);
  card.append(title);
  if (view.purpose) card.append(el(doc, 'p', 'folder-agent-card__purpose', view.purpose));
  card.append(label);
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
function renderAction(doc, action, now, faceForAction = null) {
  const state = action.state || 'waiting';
  const act = el(doc, 'span', 'folder-agent-card__action');
  act.dataset.state = state;
  if (action.detail) act.setAttribute('title', action.detail);
  const face = faceForAction?.(action);
  if (face) act.append(face);
  if (state === 'working') {
    const spinner = el(doc, 'span', 'folder-agent-card__spinner');
    spinner.setAttribute('aria-hidden', 'true');
    act.append(spinner);
  }
  const receipt = state === 'done' && action.progress;
  act.append(
    el(
      doc,
      'span',
      'folder-agent-card__action-name',
      receipt
        ? action.progress === 'Already in your Home' && action.agentName
          ? `${action.agentName} · ${action.progress}`
          : action.progress
        : action.name
    )
  );
  if (
    state === 'done' &&
    action.progress &&
    action.agentName &&
    ['hire', 'assistant'].includes(action.kind)
  )
    act.append(agentEditorLink(doc, action.agentName));
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

// outcomeItems are a step's outcome-line items: the distinct outcomes the host
// put on its actions, in order. Empty when the host marked none (an action
// list with no outcome stays as it always was).
function outcomeItems(actions) {
  const items = [];
  actions.forEach(action => {
    if (action.outcome && !items.includes(action.outcome)) items.push(action.outcome);
  });
  return items;
}

// Steps whose actions the owner opened stay open when the card redraws (a tick
// box or a swap re-plans); keyed by workspace and step.
const openActionLists = new Set();
let actionsSeq = 0;

// renderOutcomes is a chosen step before Set it up: its outcome line, and
// while some of its actions are not in it, a button that swaps the line for
// every action in run order (FR1, FR3). Closed by default.
function renderOutcomes(doc, view, row, shown, items, now) {
  const summary = el(doc, 'span', 'folder-agent-card__outcomes', items.join(' · '));
  // The line and its button share one row, the button wrapping below only
  // when the line leaves no room; a row of its own keeps the Home drawer's
  // broader "li > span" rule from running them into the step title.
  const line = el(doc, 'span', 'folder-agent-card__outcome-row');
  line.append(summary);
  if (shown.length <= items.length) return [line];
  const all = el(doc, 'span', 'folder-agent-card__actions-line');
  shown.forEach((action, k) => {
    if (k) all.append(el(doc, 'span', 'folder-agent-card__action-sep', ' · '));
    all.append(renderAction(doc, action, now));
  });
  actionsSeq += 1;
  all.id = `folder-agent-actions-${actionsSeq}`;
  const toggle = el(doc, 'button', 'folder-agent-card__details-toggle');
  toggle.type = 'button';
  toggle.setAttribute('aria-controls', all.id);
  const key = `${view.workspaceID || ''}:${row.handle}`;
  const show = open => {
    summary.hidden = open;
    all.hidden = !open;
    toggle.setAttribute('aria-expanded', open ? 'true' : 'false');
    toggle.textContent = open ? 'Show less' : `Show all ${shown.length} actions`;
  };
  show(openActionLists.has(key));
  toggle.addEventListener('click', () => {
    const open = Boolean(all.hidden);
    if (open) openActionLists.add(key);
    else openActionLists.delete(key);
    show(open);
  });
  line.append(all, toggle);
  return [line];
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
  {
    selecting = false,
    onTick,
    onPickFace,
    onEditHire,
    agentRecords,
    now = Date.now(),
    headFirst = false
  } = {}
) {
  const section = el(doc, 'section', 'folder-agent-card__run');
  section.dataset.phase = phase;
  const steps = plan?.steps || [];
  const faceForAction =
    phase === 'plan'
      ? null
      : action => {
          if (!['hire', 'assistant'].includes(action.kind)) return null;
          const entry = (plan?.hires || []).find(
            hire =>
              action.name === `Hires ${hire.name}` && (!action.step || hire.step === action.step)
          );
          // Legacy saved lines predate agent_name. Their host-written hire prefix
          // is a display-only fallback, never an execution or identity authority.
          const name =
            action.agentName ||
            entry?.name ||
            (action.name.startsWith('Hires ') ? action.name.slice(6) : '');
          return name
            ? renderHiredFace(
                doc,
                name,
                entry?.character || '',
                agentRecords,
                action.state === 'done'
              )
            : null;
        };
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
  // A step that cannot run with a chosen one is an alternative, not an
  // optional row: it waits under "Other ways to set this up".
  const alternatives = phase === 'plan' ? plan?.alternatives || [] : [];
  const alternativeHandles = new Set(alternatives.map(alternative => alternative.step));
  const offered =
    phase === 'plan'
      ? steps.filter(step => !chosen.has(step.handle) && !alternativeHandles.has(step.handle))
      : [];
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
    if (receiptRows.length) section.append(renderReceipt(doc, receiptRows, view, agentRecords));
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
    // Before Set it up a chosen step reads as its outcomes; from the moment
    // the run starts every action is shown with its live state (FR4).
    const teamCoversHires = (plan?.hires || []).some(entry => entry.step === row.handle);
    const summaryActions = teamCoversHires
      ? shown.filter(action => !['hire', 'assistant'].includes(action.kind))
      : shown;
    const outcomes = phase === 'plan' && isChosen ? outcomeItems(summaryActions) : [];
    if (outcomes.length) {
      body.append(...renderOutcomes(doc, view, row, shown, outcomes, now));
    } else if (shown.length) {
      const actions = el(doc, 'span', 'folder-agent-card__actions-line');
      shown.forEach((action, k) => {
        if (k) actions.append(el(doc, 'span', 'folder-agent-card__action-sep', ' · '));
        actions.append(renderAction(doc, action, now, faceForAction));
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
  if (plan?.branches?.length) {
    const branches = el(doc, 'fieldset', 'folder-agent-card__branches');
    branches.append(
      el(doc, 'legend', '', 'Folders to review'),
      el(
        doc,
        'p',
        '',
        'The Home gets access to the fed folder and lists it one level down. These choices limit only the deeper listings in this setup; existing access and shelf entries stay unchanged.'
      )
    );
    for (const branch of plan.branches) {
      const row = el(doc, 'div', '');
      const label = el(doc, 'label', '');
      if (branch.id && phase === 'plan') {
        const box = doc.createElement('input');
        box.type = 'checkbox';
        box.checked = branch.included;
        box.disabled = !selecting;
        box.dataset.stepHandle = branch.id;
        box.addEventListener('change', () => onTick(branch.id, box.checked));
        label.append(box);
      }
      const state = branch.id ? (branch.included ? 'Included' : 'Not selected') : '';
      label.append(
        el(
          doc,
          'span',
          '',
          [
            branch.name,
            branch.summary,
            state,
            branch.id && branch.partial ? 'Partly inspected' : ''
          ]
            .filter(Boolean)
            .join(' · ')
        )
      );
      row.append(label);
      branches.append(row);
    }
    section.append(branches);
  }
  // After the plan and before the install block: the other ways in, collapsed.
  if (alternatives.length)
    section.append(renderAlternatives(doc, alternatives, steps, selecting ? onTick : null));
  // Before confirmation the team leads the plugins. Run/receipt ordering is
  // unchanged: the run's own lines say who was hired.
  if (phase === 'plan') {
    const hires = renderHires(
      doc,
      plan?.hires || [],
      plan?.hiresNote || '',
      onPickFace,
      onEditHire
    );
    if (hires) section.append(hires);
  }
  if (installs.length) section.append(renderInstalls(doc, installs, lines, phase));
  notes.forEach(line => {
    const note = el(doc, 'p', 'folder-agent-card__line folder-agent-card__line--note');
    note.dataset.state = line.state || 'waiting';
    note.dataset.kind = NOTE_LINE_KIND;
    note.append(
      el(
        doc,
        'span',
        'folder-agent-card__line-name',
        phase === 'plan' && plan?.assurance ? plan.assurance : line.name
      )
    );
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
// what it gives the owner; where the application and its tools stand; then
// muted attribution with links to the source and Plugins. While a run is under
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
    item.append(head);
    if (entry.why) item.append(el(doc, 'span', 'folder-agent-card__line-detail', entry.why));
    if (entry.appSentence)
      item.append(el(doc, 'span', 'folder-agent-card__line-detail', entry.appSentence));
    item.append(source);
    list.append(item);
  });
  block.append(list);
  return block;
}

// renderHires draws the hires block (setup-card-clarity FR6, FR7, FR9) before
// Set it up: the agents the ticked steps will hire, each with a changeable
// face, its name and wrapping job. The shared model appears once in the header.
// A muted note appears when one of the owner's own agents
// already holds the name. The one "Agents" link leads to where they can be
// changed after they are hired. When the host cannot tell who the Home will
// hire it says so in one sentence instead of rows; it draws nothing when
// nobody is hired. The children are divs on purpose: the Home drawer restyles
// the spans directly inside any list item.
function renderHires(doc, hires, note, onPickFace, onEditHire) {
  if (!hires.length && !note) return null;
  const block = el(doc, 'section', 'folder-agent-card__hires');
  if (!hires.length) {
    block.append(el(doc, 'p', 'folder-agent-card__hires-note', note));
    return block;
  }
  const n = hires.length;
  const head = el(doc, 'div', 'folder-agent-card__hires-head');
  const commonModel = hires.every(entry => entry.model === hires[0].model);
  head.append(
    el(doc, 'h4', 'folder-agent-card__heading', `Hires ${n} ${plural(n, 'agent', 'agents')}`),
    el(
      doc,
      'span',
      'folder-agent-card__install-version folder-agent-card__hire-model',
      commonModel ? hires[0].model || NO_MODEL_YET : 'Models chosen per agent'
    )
  );
  const agents = el(doc, 'a', 'folder-agent-card__install-link', 'Agents');
  agents.href = '/agents';
  head.append(agents);
  block.append(head);
  const list = el(doc, 'ul', 'folder-agent-card__hire-list');
  hires.forEach(entry => {
    const item = el(doc, 'li', 'folder-agent-card__hire');
    item.dataset.step = entry.step;
    const face = el(doc, onPickFace ? 'button' : 'div', 'folder-agent-card__hire-face');
    if (onPickFace) {
      face.type = 'button';
      face.dataset.hireFace = entry.name;
      face.setAttribute('aria-label', `Change face for ${entry.name}`);
    } else face.setAttribute('aria-hidden', 'true');
    const win = cardWindow(doc);
    const draw = () => {
      const markup = faceMarkup(
        win,
        hireFace(entry.name, win, entry.character),
        '',
        AGENT_FACE_SIZE
      );
      if (markup) face.innerHTML = markup;
      else face.textContent = (entry.name[0] || '?').toUpperCase();
    };
    draw();
    if (onPickFace) face.addEventListener('click', () => onPickFace(entry, face, draw));
    watchFaces(face, win, draw, doc);
    item.append(face);
    item.append(
      el(doc, 'div', 'folder-agent-card__line-name folder-agent-card__hire-name', entry.name)
    );
    if (entry.job)
      item.append(
        el(doc, 'div', 'folder-agent-card__line-detail folder-agent-card__hire-job', entry.job)
      );
    if (!commonModel)
      item.append(el(doc, 'div', 'folder-agent-card__hire-row-model', entry.model || NO_MODEL_YET));
    if (entry.note)
      item.append(
        el(doc, 'div', 'folder-agent-card__line-detail folder-agent-card__hire-note', entry.note)
      );
    if (onEditHire && entry.key && entry.roleID) {
      const edit = el(doc, 'button', 'folder-agent-card__hire-edit', 'Edit agent');
      edit.type = 'button';
      edit.dataset.hireEdit = entry.key;
      edit.setAttribute('aria-label', `Edit planned agent ${entry.name}`);
      edit.addEventListener('click', () => onEditHire(entry, edit, item));
      item.append(edit);
    }
    list.append(item);
  });
  block.append(list);
  return block;
}

// What a hire's model reads when the host sent none (the host says the same).
const NO_MODEL_YET = 'No model yet';

let alternativesSeq = 0;

// renderAlternatives draws "Other ways to set this up" (setup-card-clarity
// FR16, FR18): the steps that cannot run together with a chosen one, kept out
// of the main list so the list holds the path being recommended. A real button
// opens it (closed by default); each alternative shows its title, the host's one
// line on what taking it gives up, and "Use this instead", which takes the same
// path as ticking that step: the re-plan puts it in place of the step it
// excludes and every part of the card follows the new plan. The children are
// divs because the Home drawer restyles the spans directly inside any list item.
function renderAlternatives(doc, alternatives, steps, onTick) {
  const block = el(doc, 'section', 'folder-agent-card__alternatives');
  alternativesSeq += 1;
  const listId = `folder-agent-alternatives-${alternativesSeq}`;
  const toggle = el(
    doc,
    'button',
    'folder-agent-card__alternatives-toggle',
    'Other ways to set this up'
  );
  toggle.type = 'button';
  toggle.setAttribute('aria-expanded', 'false');
  toggle.setAttribute('aria-controls', listId);
  const list = el(doc, 'ul', 'folder-agent-card__alternative-list');
  list.id = listId;
  list.hidden = true;
  alternatives.forEach(alternative => {
    const item = el(doc, 'li', 'folder-agent-card__alternative');
    item.dataset.step = alternative.step;
    item.append(el(doc, 'div', 'folder-agent-card__line-name', alternative.title));
    if (alternative.tradeoff)
      item.append(el(doc, 'div', 'folder-agent-card__line-detail', alternative.tradeoff));
    if (onTick) {
      const use = el(doc, 'button', 'folder-agent-card__alternative-use', 'Use this instead');
      use.type = 'button';
      use.dataset.altStep = alternative.step;
      use.addEventListener('click', () => {
        const excludes = steps.find(step => step.handle === alternative.step)?.excludes || '';
        return onTick(alternative.step, true, excludes);
      });
      item.append(use);
    }
    list.append(item);
  });
  toggle.addEventListener('click', () => {
    const open = Boolean(list.hidden);
    list.hidden = !open;
    toggle.setAttribute('aria-expanded', open ? 'true' : 'false');
  });
  block.append(toggle, list);
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

// songsOfferSection is the Commander offering to learn the songs once the
// place exists (learn-the-songs FR1, FR2): what it will read, then Learn them
// and Not now. It comes before the goal offer, which waits for the answer. The
// words are the host's and are drawn as text; nothing is read until Learn them.
function songsOfferSection(doc, offer, onAction) {
  const section = el(doc, 'section', 'folder-agent-card__songs-offer');
  section.setAttribute('aria-label', 'Learn the songs');
  section.append(el(doc, 'p', 'folder-agent-card__songs-lead', offer.ask));
  if (offer.update) {
    // An installed plugin that cannot read these songs yet: no Learn them, no
    // Not now, only the way to fix it. Never a silent blank.
    section.dataset.songsOffer = 'update';
    const updateRow = el(doc, 'div', 'folder-agent-card__actions folder-agent-card__songs-actions');
    const update = el(doc, 'a', 'btn btn-primary btn-sm', 'Update');
    update.href = '/plugins';
    updateRow.append(update);
    section.append(updateRow);
    return section;
  }
  if (offer.reads) section.append(el(doc, 'p', 'folder-agent-card__songs-reads', offer.reads));
  if (offer.notRead) {
    section.append(el(doc, 'p', 'folder-agent-card__songs-not-read', offer.notRead));
  }
  const row = el(doc, 'div', 'folder-agent-card__actions folder-agent-card__songs-actions');
  const button = (label, action, primary) => {
    const b = el(
      doc,
      'button',
      primary ? 'btn btn-primary btn-sm' : 'btn btn-sm folder-agent-card__secondary',
      label
    );
    b.type = 'button';
    b.dataset.folderAgentAction = action;
    b.addEventListener('click', () => onAction?.(action, b, {}));
    return b;
  };
  row.append(button('Learn them', 'learn_songs', true), button('Not now', 'dismiss_songs', false));
  section.append(row);
  return section;
}

// learningStopActions are the one thing to do about a reading that stopped for a
// named reason (FR35): fix it where it can be fixed, and Try again where a
// retry can help. There is no fallback reader behind any of them.
function learningStopActions(doc, learning, onAction, homeRoute) {
  const row = el(doc, 'div', 'folder-agent-card__actions folder-agent-card__learning-actions');
  const link = (text, href) => {
    const a = el(doc, 'a', 'btn btn-sm folder-agent-card__secondary', text);
    a.href = href;
    row.append(a);
  };
  const retry = () => {
    const b = el(doc, 'button', 'btn btn-primary btn-sm', 'Try again');
    b.type = 'button';
    b.addEventListener('click', () => onAction?.('retry_songs', b, {}));
    row.append(b);
  };
  switch (learning.action) {
    case 'choose_model':
      link('Choose a model', '/settings');
      retry();
      break;
    case 'add_assistant':
      if (homeRoute) link('Add it', homeRoute);
      retry();
      break;
    case 'update_plugin':
      link('Update', '/plugins');
      break;
    case 'open_switch':
      if (homeRoute) link("Open the Home's switch", homeRoute);
      break;
    case 'retry':
      retry();
      break;
    default:
      break;
  }
  return row;
}

// learningSection is the reading of the songs as it runs and once it ended
// (FR14): the host's line ("Learning <song>… (2 of 7)", then "Learned 7 songs"),
// a progress bar while it runs, and the songs that cannot be read this way.
// announce:false leaves the line without its own role=status: the drawer says a
// change once from one persistent live region instead (redrawn nodes are missed).
function learningSection(doc, learning, onAction = null, homeRoute = '', { announce = true } = {}) {
  const section = el(doc, 'section', 'folder-agent-card__learning');
  section.dataset.learningStatus = learning.status;
  if (learning.reason) section.dataset.learningReason = learning.reason;
  section.setAttribute('aria-label', 'Learning the songs');
  const line = el(doc, 'p', 'folder-agent-card__learning-line', learning.line);
  section.append(line);
  if (learning.status === 'stopped' && learning.action) {
    section.append(learningStopActions(doc, learning, onAction, homeRoute));
  }
  if (announce && (learning.status === 'running' || learning.status === 'summarizing')) {
    // The poll redraws this card; role=status announces the line as it changes.
    line.setAttribute('role', 'status');
  }
  if (learning.status === 'running') {
    if (learning.total > 0) {
      const bar = doc.createElement('progress');
      bar.className = 'folder-agent-card__learning-bar';
      bar.max = learning.total;
      bar.value = Math.min(learning.done, learning.total);
      bar.setAttribute('aria-label', 'Songs learned so far');
      section.append(bar);
    }
  }
  // The agent's own summary of what it learned, as its words in text (never
  // markup), under the host's closing line and before the songs it could not read.
  if (learning.summary && learning.summaryBy === 'agent') {
    section.append(el(doc, 'p', 'folder-agent-card__learning-summary', learning.summary));
  }
  const ended = learning.status !== 'running' && learning.status !== 'summarizing';
  if (learning.notRead && ended) {
    section.append(el(doc, 'p', 'folder-agent-card__songs-not-read', learning.notRead));
  }
  // Songs the installed plugin cannot read yet: the way to fix it, with the
  // update, never a silent blank (FR49).
  if (learning.update && ended) {
    section.append(
      el(doc, 'p', 'folder-agent-card__songs-not-read', `Update ${learning.update} to read songs.`)
    );
    const row = el(doc, 'div', 'folder-agent-card__actions folder-agent-card__learning-actions');
    const update = el(doc, 'a', 'btn btn-sm folder-agent-card__secondary', 'Update');
    update.href = '/plugins';
    row.append(update);
    section.append(row);
  }
  return section;
}

// renderFolderAgentReport draws the receipt or the report. onAction(id,
// button, extra) posts one action; the caller refreshes. opening says the
// page is about to open the place: the link gives way to that word.
// goalOfferSection is the Commander offering the agent's suggested goal once
// the place exists (folder-setup-ends-in-home FR17, FR18): the sentence, then
// Use this goal, Edit and Not now. Edit turns the sentence into a one-line
// input (max_runes characters) with Save and Cancel, in place; Escape cancels.
// draft ({editing, text}) is the drawer's memory of an edit in progress, so an
// incidental redraw brings back the owner's words in the field, not the offer.
function goalOfferSection(doc, offer, onAction, draft = null) {
  const section = el(doc, 'section', 'folder-agent-card__goal-offer');
  section.setAttribute('aria-label', 'Suggested goal');
  // A goal the agent wrote after it learned the songs says so (FR17).
  const lead = el(
    doc,
    'p',
    'folder-agent-card__goal-lead',
    offer.basis === 'learned'
      ? 'A goal for this place, from what I learned?'
      : 'A goal for this place?'
  );
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
    if (draft) draft.editing = false;
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
  function showEdit(from) {
    // A redraw restoring an edit in progress must not take focus from wherever
    // the owner is; only their own press of Edit moves it into the field.
    const restoring = from === 'restore';
    const input = doc.createElement('input');
    input.type = 'text';
    input.className = 'folder-agent-card__goal-input';
    input.maxLength = offer.maxRunes;
    if (draft) {
      if (!restoring) draft.text = offer.goal;
      draft.editing = true;
      input.value = draft.text;
      input.addEventListener('input', () => {
        draft.text = input.value;
      });
    } else input.value = offer.goal;
    input.dataset.chatControl = 'goal-text';
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
    if (!restoring) input.focus?.();
  }
  if (draft?.editing) showEdit('restore');
  else showOffer();
  return section;
}

function renderProjectHolders(doc, view, fetchImpl) {
  const section = el(doc, 'section', 'folder-agent-card__project-holders');
  section.append(el(doc, 'h4', '', 'Projects inside'));
  const list = el(doc, 'ul', 'folder-agent-card__holder-list');
  section.append(list);
  for (const holder of view.projectHolders.rows.slice(0, 3)) {
    const row = el(doc, 'li', 'folder-agent-card__holder-row');
    row.append(el(doc, 'strong', '', String(holder.name)), el(doc, 'p', '', String(holder.kinds)));
    if (view.rootPolicy === 'downloads_inbox') {
      list.append(row);
      continue;
    }
    const setup = el(
      doc,
      'button',
      'btn btn-sm folder-agent-card__secondary',
      `Set up ${holder.name}`
    );
    setup.type = 'button';
    const choiceHost = el(doc, 'div', 'folder-agent-card__holder-choice');
    let active = true;
    setup.addEventListener('click', async () => {
      setup.disabled = true;
      choiceHost.replaceChildren();
      try {
        const { ok, payload } = await post(
          `${FOLDER_AGENT_ENDPOINT}/${encodeURIComponent(view.workspaceID)}/project-holder`,
          { report_key: view.projectHolders.report_key, folder_index: holder.index },
          fetchImpl
        );
        if (!ok || !payload?.card) {
          choiceHost.append(
            el(
              doc,
              'p',
              'folder-agent-card__error',
              payload?.error || 'That project folder could not be chosen.'
            )
          );
          return;
        }
        active = true;
        const hireHost = el(doc, 'div', '');
        const cancel = el(doc, 'button', 'btn btn-sm folder-agent-card__secondary', 'Cancel');
        cancel.type = 'button';
        cancel.addEventListener('click', () => {
          active = false;
          choiceHost.replaceChildren();
          setup.focus?.();
        });
        choiceHost.append(hireHost, cancel);
        await mountFolderAgentChoice(hireHost, null, {
          doc,
          fetchImpl,
          initialCard: payload.card,
          isActive: () => active,
          onStarting: busy => {
            cancel.disabled = busy;
          },
          onStarted: view => {
            cancel.remove?.();
            const win = doc.defaultView || globalThis.window;
            if (win?.CustomEvent)
              win.dispatchEvent?.(
                new win.CustomEvent('ori:folder-agent-started', {
                  detail: { workspaceID: view?.workspace_id, built: true }
                })
              );
          }
        });
        hireHost.querySelector?.('.folder-agent-card__brief')?.focus?.();
      } catch (_) {
        choiceHost.append(
          el(
            doc,
            'p',
            'folder-agent-card__error',
            'That project folder could not be chosen right now.'
          )
        );
      } finally {
        setup.disabled = false;
      }
    });
    row.append(setup, choiceHost);
    list.append(row);
  }
  if (view.projectHolders.more > 0)
    section.append(el(doc, 'p', '', `and ${view.projectHolders.more} more`));
  return section;
}

function hireDraftPayload(drafts, selected) {
  if (!(drafts instanceof Map)) return {};
  const active = new Set(selected || []);
  const edits = Object.fromEntries(
    [...drafts]
      .filter(([, choice]) => active.has(choice.step))
      .map(([key, choice]) => [
        key,
        { name: choice.name, provider: choice.provider, model: choice.model }
      ])
  );
  return Object.keys(edits).length ? { hire_edits: edits } : {};
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
  const selecting = Boolean(view.plan?.steps?.length) && view.actions.some(a => a.id === 'set_up');
  const selected = new Set(view.plan?.selected || []);
  let planHost = null;
  let setUpButton = null;
  let planRequest = 0;
  // Keep only user picks, not server suggestions. A temporary untick keeps a
  // step's picks; a renamed/removed hire discards them once that step returns.
  const faces = view.hireFaces instanceof Map ? view.hireFaces : new Map();
  const drafts = view.hireDrafts instanceof Map ? view.hireDrafts : new Map();
  view.hireFaces = faces;
  view.hireDrafts = drafts;
  const applyFaces = plan => {
    const offered = new Map((plan?.steps || []).map(step => [step.handle, step]));
    for (const [name, choice] of faces) {
      const step = offered.get(choice.step);
      if (!step || (step.chosen && !plan.hires.some(entry => (entry.key || entry.name) === name)))
        faces.delete(name);
    }
    (plan?.hires || []).forEach(entry => {
      const key = entry.key || entry.name;
      if (faces.has(key)) entry.character = faces.get(key).character;
    });
  };
  applyFaces(view.plan);
  const win = cardWindow(doc);
  let recordsReady = null;
  const agentRecords = () => (recordsReady ||= fetchAgentRecords(fetchImpl));
  let picking = false;
  const onPickFace =
    typeof win?.CharacterPicker?.open === 'function' && selecting
      ? async (entry, trigger, draw) => {
          if (picking) return;
          picking = true;
          const pickerFocus = event => {
            if (event.target?.closest?.('#charPickerHost')) event.stopImmediatePropagation();
          };
          try {
            const records = await agentRecords();
            if (card.isConnected === false || trigger.isConnected === false) return;
            const taken = [...(records?.values() || [])]
              .filter(record => record.appearance?.mode === 'character')
              .map(record => record.appearance.character?.catalog_id)
              .filter(Boolean);
            (view.plan?.hires || []).forEach(other => {
              if (other.name !== entry.name && other.character) taken.push(other.character);
            });
            doc.addEventListener?.('focusin', pickerFocus, true);
            const answer = await win.CharacterPicker.open({
              selectedId: entry.character || '',
              taken: [...new Set(taken)],
              role: HIRED_AGENT_ROLE,
              trigger,
              showSkip: true
            });
            if (card.isConnected === false || trigger.isConnected === false) return;
            if (answer?.action === 'choose' || answer?.action === 'skip') {
              entry.character = answer.action === 'choose' ? String(answer.catalogId || '') : '';
              faces.set(entry.key || entry.name, { character: entry.character, step: entry.step });
              draw();
            }
          } finally {
            doc.removeEventListener?.('focusin', pickerFocus, true);
            picking = false;
            if (trigger.isConnected !== false) trigger.focus?.({ preventScroll: true });
          }
        }
      : null;
  let editing = false;
  const onEditHire =
    selecting && win?.AgentCreateForm?.mount && win?.AgentAppearanceEditor?.create
      ? async (entry, trigger, item) => {
          if (editing || picking) return;
          editing = true;
          ++planRequest;
          const suspended = ['button', 'input', 'select'].flatMap(tag =>
            Array.from(card.querySelectorAll(tag)).map(control => {
              const disabled = control.disabled;
              control.disabled = true;
              return [control, disabled];
            })
          );
          let editor = null;
          const close = (focus = true) => {
            editor?.destroy();
            if (editor) {
              editor.element.hidden = true;
              editor.element.remove?.();
            }
            suspended.forEach(([control, disabled]) => {
              control.disabled = disabled;
            });
            editing = false;
            if (focus && trigger.isConnected !== false) trigger.focus?.({ preventScroll: true });
          };
          try {
            const records = await agentRecords();
            if (card.isConnected === false || trigger.isConnected === false) return close(false);
            const taken = [...(records?.values() || [])]
              .filter(record => record.appearance?.mode === 'character')
              .map(record => record.appearance.character?.catalog_id)
              .filter(Boolean);
            (view.plan?.hires || []).forEach(other => {
              if ((other.key || other.name) !== entry.key && other.character)
                taken.push(other.character);
            });
            editor = renderHireDraftEditor(doc, {
              entry,
              models: view.plan.hireModels,
              taken: [...new Set(taken)],
              onCancel: close,
              onSave: async (choice, character) => {
                const pending = new Map(drafts);
                pending.set(entry.key, { ...choice, step: entry.step });
                const { ok, payload } = await post(
                  `${FOLDER_AGENT_ENDPOINT}/${encodeURIComponent(view.workspaceID)}/plan`,
                  { selected: [...selected], ...hireDraftPayload(pending, [...selected]) },
                  fetchImpl
                );
                if (!ok || !payload?.plan)
                  throw new Error(String(payload?.error || 'The draft could not be reviewed.'));
                if (card.isConnected === false || editor.element.isConnected === false)
                  return close(false);
                const plan = planView(payload.plan);
                const saved = plan.hires.find(hire => hire.key === entry.key);
                if (!saved)
                  throw new Error(
                    'That role is no longer planned. Cancel and review the current team.'
                  );
                drafts.set(entry.key, {
                  name: saved.name,
                  provider: saved.provider,
                  model: saved.modelID,
                  step: entry.step
                });
                faces.set(entry.key, { character, step: entry.step });
                close(false);
                acceptPlan(plan);
                Array.from(planHost?.querySelectorAll('button') || [])
                  .find(button => button.dataset.hireEdit === entry.key)
                  ?.focus?.({ preventScroll: true });
              }
            });
            item.append(editor.element);
            editor.focus();
          } catch (failure) {
            close();
            const message = el(
              doc,
              'p',
              'folder-agent-card__error',
              failure?.message || 'The agent draft editor could not be opened.'
            );
            message.setAttribute('role', 'alert');
            item.append(message);
          }
        }
      : null;
  const onTick = (handle, checked, excludes) => {
    if (checked) {
      selected.add(handle);
      // The owner's latest tick wins over a step it cannot run with.
      if (excludes) selected.delete(excludes);
    } else selected.delete(handle);
    return refreshPlan();
  };
  const refreshPlan = async () => {
    const request = ++planRequest;
    const edits = hireDraftPayload(drafts, [...selected]).hire_edits;
    const plan = await fetchFolderAgentPlan(view.workspaceID, [...selected], fetchImpl, edits);
    if (!plan || request !== planRequest) return;
    acceptPlan(plan);
  };
  const acceptPlan = plan => {
    applyFaces(plan);
    view.plan = plan;
    // The server may have set a step aside (two that cannot run together):
    // the boxes follow its choice.
    selected.clear();
    plan.selected.forEach(handle => selected.add(handle));
    if (planHost) {
      // The box the owner just ticked keeps focus through the redraw (a
      // keyboard user would otherwise be dropped on the page body).
      const focused = doc.activeElement?.dataset?.stepHandle || '';
      // "Use this instead" is gone after the swap; focus goes to the control
      // that now holds the way the owner just left.
      const swapped = Boolean(doc.activeElement?.dataset?.altStep);
      planHost.replaceChildren(
        renderPlan(doc, view, plan, plan.lines, 'plan', {
          selecting,
          onTick,
          onPickFace,
          onEditHire
        })
      );
      if (swapped)
        planHost
          .querySelector?.('.folder-agent-card__alternatives-toggle')
          ?.focus?.({ preventScroll: true });
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
    // What the folder is comes first (FR1–FR4, FR12): the host's sentence, the
    // owner's own song names to check it by, what the plan after the answer
    // will make, that nothing happens yet, and two real buttons, in that
    // reading order. The recommendation, the plan, the install block and the
    // report's buttons wait for the answer. Every part but the sentence and the
    // buttons is optional: what the host did not send draws nothing, and the
    // "If that's right" label is drawn only with its sentence. All of it is
    // set as text, never as markup (the names come from folder names on disk).
    const ask = el(doc, 'section', 'folder-agent-card__premise');
    ask.append(el(doc, 'p', 'folder-agent-card__premise-text', premise.sentence));
    const namesLine = folderAgentPremiseNamesLine(premise);
    if (namesLine) ask.append(el(doc, 'p', 'folder-agent-card__premise-names', namesLine));
    if (premise.outcome || premise.nothingYet) {
      const rule = el(doc, 'hr', 'folder-agent-card__premise-rule');
      rule.setAttribute('aria-hidden', 'true');
      ask.append(rule);
    }
    if (premise.outcome) {
      ask.append(
        el(doc, 'p', 'folder-agent-card__premise-label', "If that's right"),
        el(doc, 'p', 'folder-agent-card__premise-outcome', premise.outcome)
      );
    }
    if (premise.nothingYet) {
      ask.append(el(doc, 'p', 'folder-agent-card__premise-nothing', premise.nothingYet));
    }
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
  if (view.organizationReview && fetchImpl)
    card.append(
      documentsOrganizationOffer(
        doc,
        `${FOLDER_AGENT_ENDPOINT}/${encodeURIComponent(view.workspaceID)}`,
        fetchImpl
      )
    );
  if (view.policyNote) card.append(el(doc, 'p', 'folder-agent-card__note', view.policyNote));
  if (view.projectHolders?.rows?.length) {
    card.append(renderProjectHolders(doc, view, fetchImpl));
  }
  if (view.rootPolicy === 'documents_inbox' && fetchImpl) {
    const protection = el(doc, 'p', 'folder-agent-card__protection');
    card.append(protection);
    // Same stored-only fact and navigation as the workspace's Protection
    // line: no scan, metadata refresh, provider read or folder path here.
    void fetchWorkspaceProtection(view.workspaceID, fetchImpl).then(fact => {
      const current = protectionFactView(fact);
      if (!current) return;
      protection.append(el(doc, 'span', '', current.sentence));
      if (current.route) {
        const link = el(doc, 'a', 'folder-agent-card__protection-link', current.routeLabel);
        link.href = current.route;
        protection.append(el(doc, 'span', '', ' '), link);
      }
    });
  }
  const downloadsReview =
    view.rootPolicy === 'downloads_inbox' &&
    view.route.startsWith('/workspaces/') &&
    ['reported', 'set_up'].includes(view.state);
  if (downloadsReview) {
    const actions = el(doc, 'div', 'folder-agent-card__actions');
    const review = el(doc, 'a', 'btn btn-primary btn-sm', 'Review Downloads');
    const route = new URL(view.route, 'http://ori.local');
    route.searchParams.set('panel', 'file-janitor');
    route.searchParams.set('review', 'downloads');
    review.href = route.pathname + route.search;
    actions.append(review);
    card.append(actions);
  }
  if (view.exploreFolder && view.rootPolicy !== 'downloads_inbox') {
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
      agentRecords,
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
  } else if (downloadsReview) {
    // The Filing console owns the explicit install and folder-access steps.
  } else if (view.plan && (view.plan.steps.length || view.plan.runnable)) {
    planHost = el(doc, 'div', 'folder-agent-card__plan-host');
    planHost.append(
      renderPlan(doc, view, view.plan, view.plan.lines, 'plan', {
        selecting,
        onTick,
        onPickFace,
        onEditHire
      })
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
  // The Commander asks one thing at a time: the songs first, then the goal
  // (learn-the-songs FR2, FR17). The reading's line stays once it ended.
  if (view.learning)
    card.append(
      learningSection(doc, view.learning, onAction, view.setup?.receipt?.home?.route || '')
    );
  if (view.songsOffer) card.append(songsOfferSection(doc, view.songsOffer, onAction));
  if (view.goalOffer) card.append(goalOfferSection(doc, view.goalOffer, onAction));
  if (view.actions.length) {
    const actions = el(doc, 'div', 'folder-agent-card__actions');
    view.actions.forEach((action, index) => {
      if (downloadsReview && !view.setup && action.id === 'set_up') return;
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
      if (action.id === 'set_up') {
        setUpButton = button;
        button.disabled = !view.plan?.runnable;
      }
      button.addEventListener('click', () => {
        // Set it up carries only the steps the owner left selected.
        const extra =
          action.id === 'set_up'
            ? {
                selected: [...selected],
                hire_faces: Object.fromEntries(
                  (view.plan?.hires || []).map(entry => [
                    entry.key || entry.name,
                    entry.character || ''
                  ])
                ),
                ...hireDraftPayload(drafts, [...selected])
              }
            : undefined;
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

// The Commander's drawer draws a completed music setup as separate cards under
// the report's message: a short receipt, then the follow-up. The Map's balloon
// asks for the same report with compact:true, so the drawer asks for this
// presentation by name (card(true, {presentation: 'chat'})). Any report the
// view cannot show as a completed music setup gets null here and keeps the
// single card.
function chatCard(doc, part, label) {
  const card = el(
    doc,
    'article',
    `folder-agent-card folder-agent-card--chat folder-agent-card--chat-${part}`
  );
  card.dataset.folderAgentChatPart = part;
  card.setAttribute('aria-label', label);
  return card;
}

// chatReceiptCard is the compact receipt: where the folder was placed, the way
// to open that place, anything still needing attention, and the rest of what
// the run made behind one disclosure. Every word is the host's or built from
// its receipt; nothing is claimed that the receipt did not report.
function chatReceiptCard(doc, view, setup, { state, fetchImpl }) {
  const card = chatCard(doc, 'receipt', 'Setup receipt');
  card.dataset.workspaceId = view.workspaceID;
  card.append(el(doc, 'p', 'folder-agent-card__chat-line', chatPlacementLine(view, setup)));
  if (view.filingPermissionRequired) {
    card.append(el(doc, 'p', 'folder-agent-card__degraded', 'One step left: allow filing.'));
  }
  const destination = chatDestination(view);
  if (destination) {
    const open = el(doc, 'a', 'folder-agent-card__open', `Open ${destination.name || 'it'}`);
    open.href = destination.route;
    card.append(open);
  }
  const rows = folderAgentReceiptRows(view);
  if (!rows.length) return card;
  const panelID = `folder-agent-chat-details-${String(view.workspaceID).replace(/[^\w-]/g, '_')}`;
  const toggle = el(
    doc,
    'button',
    'btn btn-sm folder-agent-card__secondary folder-agent-card__details-toggle',
    'View setup details'
  );
  toggle.type = 'button';
  toggle.dataset.chatControl = 'setup-details';
  toggle.setAttribute('aria-controls', panelID);
  const panel = el(doc, 'div', 'folder-agent-card__chat-details');
  panel.id = panelID;
  // The agents' faces come from a read of the agent list; it is made only once
  // the owner opens the details.
  let recordsReady = null;
  const agentRecords = () => (recordsReady ||= fetchAgentRecords(fetchImpl));
  const show = open => {
    toggle.setAttribute('aria-expanded', String(open));
    panel.hidden = !open;
    if (open && !panel.children.length) panel.append(renderReceipt(doc, rows, view, agentRecords));
  };
  show(state?.open?.has('setup-details') === true);
  toggle.addEventListener('click', () => {
    const open = toggle.getAttribute('aria-expanded') !== 'true';
    if (open) state?.open?.add('setup-details');
    else state?.open?.delete('setup-details');
    show(open);
  });
  card.append(toggle, panel);
  return card;
}

// The drawer's follow-up cards. Which ones, and in what order, is
// chatFollowUp's decision from the refreshed view; each is its own card under
// the receipt. A card's buttons are one decision: pressing any of them quiets
// the whole card while the answer is sent, and nothing here sends anything until
// the owner presses one.
function chatStepCard(doc, step, label) {
  const card = chatCard(doc, 'follow-up', label);
  card.dataset.chatStep = step;
  return card;
}

// The heading is where focus goes when the control that was pressed is gone.
function chatHeading(doc, text) {
  const heading = el(doc, 'h4', 'folder-agent-card__chat-heading', text);
  markChatTarget(heading);
  return heading;
}

// markChatTarget makes an element the place focus lands when the control the
// owner pressed is gone (the next heading or the card's first words).
function markChatTarget(node) {
  node.tabIndex = -1;
  node.dataset.chatTarget = '';
  return node;
}

function chatButton(doc, id, label, primary, onAction) {
  const button = el(
    doc,
    'button',
    primary ? 'btn btn-primary btn-sm' : 'btn btn-sm folder-agent-card__secondary',
    label
  );
  button.type = 'button';
  button.dataset.folderAgentAction = id;
  button.dataset.chatControl = id;
  button.addEventListener('click', () => onAction?.(id, button, {}));
  return button;
}

const LEARNING_HEADINGS = {
  running: 'Learning your songs',
  summarizing: 'Learning your songs',
  stopped: 'Learning stopped'
};

// questionCard is the one decision while the offer is open: learn the songs, or
// not now. The disclosure is the host's own words, drawn as text, and the
// exception (a format that cannot be read) is named by the host.
function chatQuestionCard(doc, step, onAction) {
  const { offer, stopped } = step;
  const card = chatStepCard(doc, 'question', 'Learn the songs');
  const format = offer.format ? `${offer.format} ` : '';
  card.append(chatHeading(doc, `Learn the ${offer.count} ${format}${offer.noun}?`));
  if (offer.reads) card.append(el(doc, 'p', 'folder-agent-card__songs-reads', offer.reads));
  if (offer.notRead) card.append(el(doc, 'p', 'folder-agent-card__songs-not-read', offer.notRead));
  const why = stopped?.reason || stopped?.line;
  if (why) card.append(el(doc, 'p', 'folder-agent-card__degraded', why));
  const row = el(doc, 'div', 'folder-agent-card__actions folder-agent-card__songs-actions');
  row.append(
    chatButton(doc, 'learn_songs', `Learn ${offer.count} ${offer.noun}`, true, onAction),
    chatButton(doc, 'dismiss_songs', 'Not now', false, onAction)
  );
  card.append(row);
  return card;
}

// learningCard shows the reading as it runs, what it actually read, or why it
// stopped with the one thing to do about it: the host's line and numbers, never
// a count the owner was told earlier.
function chatLearningCard(doc, step, onAction, home) {
  const card = chatStepCard(doc, 'learning', 'Learning the songs');
  card.append(chatHeading(doc, LEARNING_HEADINGS[step.learning.status] || 'What I learned'));
  card.append(learningSection(doc, step.learning, onAction, home, { announce: false }));
  return card;
}

function chatUpdateCard(doc, step, onAction) {
  const card = chatStepCard(doc, 'update', 'Update the plugin');
  card.append(songsOfferSection(doc, step.offer, onAction));
  return card;
}

// A notice that does not block the decision under it (a reading that stopped, a
// plugin to update) sits behind a disclosure in that decision's card, so it
// stays reachable without a second prompt.
function chatDetails(doc, notice, onAction, home, state) {
  const wrap = el(doc, 'div', 'folder-agent-card__chat-notice');
  const learning = notice.kind === 'learning';
  const panelID = `folder-agent-chat-notice-${++chatNoticeSeq}`;
  const toggle = el(
    doc,
    'button',
    'btn btn-sm folder-agent-card__secondary folder-agent-card__details-toggle',
    learning ? 'Learning details' : 'Plugin update'
  );
  toggle.type = 'button';
  toggle.dataset.chatControl = 'learning-details';
  toggle.setAttribute('aria-controls', panelID);
  const panel = el(doc, 'div', 'folder-agent-card__chat-details');
  panel.id = panelID;
  const show = open => {
    toggle.setAttribute('aria-expanded', String(open));
    panel.hidden = !open;
    if (open && !panel.children.length)
      panel.append(
        learning
          ? learningSection(doc, notice.learning, onAction, home, { announce: false })
          : songsOfferSection(doc, notice.offer, onAction)
      );
  };
  show(state?.open?.has('learning-details') === true);
  toggle.addEventListener('click', () => {
    const open = toggle.getAttribute('aria-expanded') !== 'true';
    if (open) state?.open?.add('learning-details');
    else state?.open?.delete('learning-details');
    show(open);
  });
  wrap.append(toggle, panel);
  return wrap;
}
let chatNoticeSeq = 0;

function chatGoalCard(doc, step, onAction, state) {
  const card = chatStepCard(doc, 'goal', 'Suggested goal');
  // The owner's edit survives a redraw for as long as the same goal is offered.
  let draft = null;
  if (state) {
    draft = state.goal;
    if (!draft || draft.base !== step.offer.goal)
      draft = state.goal = { base: step.offer.goal, editing: false, text: '' };
  }
  card.append(goalOfferSection(doc, step.offer, onAction, draft));
  const lead = card.querySelector('.folder-agent-card__goal-lead');
  if (lead) markChatTarget(lead);
  return card;
}

// ticketCard is the first ticket once, with its real title and the answers the
// host allows; a ticket already started is shown as started. With no ticket and
// only Look further to offer, it is that one quiet action.
function chatTicketCard(doc, step, onAction) {
  const { ticket } = step;
  const card = chatStepCard(doc, ticket.title ? 'ticket' : 'further', 'Next step');
  if (ticket.title) {
    card.append(
      chatHeading(doc, ticket.started ? 'Ticket started' : 'First ticket'),
      el(doc, 'p', 'folder-agent-card__next-ticket', ticket.title)
    );
  } else {
    card.append(
      markChatTarget(
        el(doc, 'p', 'folder-agent-card__chat-line', 'I can look further into this folder.')
      )
    );
  }
  if (ticket.start || ticket.further) {
    const row = el(doc, 'div', 'folder-agent-card__actions');
    if (ticket.start)
      row.append(chatButton(doc, 'start_next', 'Start this ticket', true, onAction));
    if (ticket.further)
      row.append(chatButton(doc, 'look_further', 'Look further', false, onAction));
    card.append(row);
  }
  return card;
}

function chatStepElement(doc, step, { onAction, state }, home) {
  // One decision, one guard: pressing any button quiets every button of the card.
  let card = null;
  const guarded = (id, button, extra) => {
    card?.querySelectorAll('button').forEach(other => {
      other.disabled = true;
    });
    return onAction?.(id, button, extra);
  };
  switch (step.kind) {
    case 'question':
      card = chatQuestionCard(doc, step, guarded);
      break;
    case 'learning':
      card = chatLearningCard(doc, step, guarded, home);
      break;
    case 'update':
      card = chatUpdateCard(doc, step, guarded);
      break;
    case 'goal':
      card = chatGoalCard(doc, step, guarded, state);
      break;
    default:
      card = chatTicketCard(doc, step, guarded);
  }
  if (step.details) card.append(chatDetails(doc, step.details, guarded, home, state));
  // While an answer is in flight a redrawn card must not offer another write.
  if (state?.pending === true)
    card.querySelectorAll('button').forEach(button => {
      button.disabled = true;
    });
  return card;
}

export function renderFolderAgentChatThread(
  view,
  doc = document,
  { onAction, error = '', fetchImpl = globalThis.fetch, state = null } = {}
) {
  const setup = chatSetupOf(view);
  if (!setup) return null;
  const stack = el(doc, 'div', 'folder-agent-chat-stack');
  const receipt = chatReceiptCard(doc, view, setup, { state, fetchImpl });
  stack.append(receipt);
  const home = view.setup?.receipt?.home?.route || '';
  const steps = chatFollowUp(view).cards;
  // A goal edit belongs to the goal on offer; once it is gone so is the draft.
  if (state && !steps.some(step => step.kind === 'goal')) state.goal = null;
  const cards = steps.map(step => chatStepElement(doc, step, { onAction, state }, home));
  cards.forEach(card => stack.append(card));
  if (error) {
    // The error stays with the decision it is about: the first card that asks
    // something, else the last card, else the receipt.
    const owner =
      cards.find(card => card.querySelector('button')) || cards[cards.length - 1] || receipt;
    const alert = el(doc, 'p', 'folder-agent-card__error', error);
    alert.setAttribute('role', 'alert');
    owner.append(alert);
  }
  // What a status change would say, for the drawer's one live region: the
  // reading's own line, never a count the owner was told earlier.
  stack.dataset.announce = steps.find(step => step.kind === 'learning')?.learning.line || '';
  // After an explicit answer the pressed control may be gone: focus goes back to
  // it if the answer failed and it is still there, else to the new card's heading.
  // Only an answer sets this, so a background redraw never moves focus.
  const focus = state?.focusAfter;
  if (focus && state.pending !== true) {
    state.focusAfter = null;
    const again = focus.failed
      ? cards
          .flatMap(card => card.querySelectorAll('button'))
          .find(
            button =>
              (button.dataset.chatControl === focus.action ||
                button.dataset.folderAgentAction === focus.action) &&
              !button.disabled
          )
      : null;
    // The newest card is the one the answer revealed: a result the owner has
    // already read can sit above it, and focus must not jump back up to that.
    const target =
      again ||
      cards
        .map(card => card.querySelector('[data-chat-target]'))
        .filter(Boolean)
        .at(-1) ||
      cards
        .map(card => card.querySelector('button'))
        .filter(Boolean)
        .at(-1);
    if (target) target.dataset.chatFocus = '';
  }
  return stack;
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
// "Install plugins & set up" while the ticked steps install plugins, else
// "Set it up". Every surface that offers the action uses it.
export function folderAgentSetUpLabel(plan) {
  const n = plan?.installs?.length || 0;
  return n > 0 ? 'Install plugins & set up' : ACTION_LABELS.set_up;
}

function planView(plan) {
  return {
    digest: String(plan.digest || ''),
    assurance: String(plan.assurance || ''),
    runnable: Boolean(plan.runnable),
    unavailable: String(plan.unavailable || ''),
    selected: Array.isArray(plan.selected) ? plan.selected.map(String) : [],
    branches: Array.isArray(plan.branches) ? plan.branches : [],
    lines: (plan.lines || []).map(line => lineView(line)),
    // Who chose the steps: the agent, in its own words, or Ori's default.
    source: String(plan.source || 'default'),
    words: String(plan.words || ''),
    wordsRaw: String(plan.words_raw || ''),
    excluded: String(plan.excluded || ''),
    defaultReason: String(plan.default_reason || ''),
    reorderNote: String(plan.reorder_note || ''),
    // The plugins the ticked steps download and install, in run order: the
    // visible install block and whether the button promises installation.
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
    // The agents the ticked steps will hire, by the name each is hired under,
    // what it does and the model it runs on; or one sentence when the host
    // cannot tell yet. The host decides both, the card only draws them.
    hires: (Array.isArray(plan.hires) ? plan.hires : [])
      .map(entry => ({
        step: String(entry?.step || ''),
        key: String(entry?.key || ''),
        roleID: String(entry?.role_id || ''),
        provider: String(entry?.provider || ''),
        modelID: String(entry?.model_id || ''),
        name: String(entry?.name || ''),
        job: String(entry?.job || ''),
        character: String(entry?.character || ''),
        model: String(entry?.model || ''),
        note: String(entry?.note || '')
      }))
      .filter(entry => entry.name),
    hiresNote: String(plan.hires_note || ''),
    hireModels: Array.isArray(plan.hire_models) ? plan.hire_models : [],
    // The other ways to set this folder up: steps that cannot run with a chosen
    // one, each with the host's line on what taking it gives up.
    alternatives: (Array.isArray(plan.alternatives) ? plan.alternatives : [])
      .map(alternative => ({
        step: String(alternative?.step || ''),
        title: String(alternative?.title || ''),
        tradeoff: String(alternative?.tradeoff || '')
      }))
      .filter(alternative => alternative.step && alternative.title),
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
export async function fetchFolderAgentPlan(
  workspaceID,
  selected,
  fetchImpl = globalThis.fetch,
  hireEdits
) {
  if (!workspaceID || typeof fetchImpl !== 'function') return null;
  try {
    const { ok, payload } = await post(
      `${FOLDER_AGENT_ENDPOINT}/${encodeURIComponent(workspaceID)}/plan`,
      {
        selected,
        ...(hireEdits && Object.keys(hireEdits).length ? { hire_edits: hireEdits } : {})
      },
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
  if (action === 'set_up')
    body = {
      plan_digest: view.plan?.digest || '',
      hire_faces: Object.fromEntries(
        (view.plan?.hires || []).map(entry => [entry.key || entry.name, entry.character || ''])
      ),
      ...hireDraftPayload(view.hireDrafts, view.plan?.selected),
      ...extra
    };
  else if (action === 'use_goal') {
    const goal = String(extra?.goal || '').trim();
    body = goal ? { answer: 'use', goal } : { answer: 'use' };
  } else if (action === 'dismiss_goal') body = { answer: 'dismiss' };
  else if (action === 'learn_songs') body = { answer: 'learn' };
  else if (action === 'dismiss_songs') body = { answer: 'dismiss' };
  else if (action === 'retry_songs') body = { answer: 'retry' };
  else if (action === 'premise_confirm') body = { answer: 'confirmed' };
  else if (action === 'premise_other') body = { answer: 'something_else' };
  const { ok, payload } = await post(url, body, fetchImpl);
  if (!ok) {
    let refused = payload?.folder_agent || null;
    // A plan that changed after it was shown (an agent took a name, a model was
    // set) is redrawn on the owner's own choice of steps, not on Ori's default
    // for the folder, so what changed is on the card to read before they press
    // again. The server's own view stands if the re-plan cannot be had.
    if (payload?.plan_changed && action === 'set_up' && refused && Array.isArray(extra?.selected)) {
      try {
        const again = await post(
          `${FOLDER_AGENT_ENDPOINT}/${encodeURIComponent(view.workspaceID)}/plan`,
          {
            selected: extra.selected,
            ...(body.hire_edits ? { hire_edits: body.hire_edits } : {})
          },
          fetchImpl
        );
        if (again.ok && again.payload?.plan) refused = { ...refused, plan: again.payload.plan };
      } catch (_) {
        // keep the server's view
      }
    }
    if (refused?.plan?.hires && body?.hire_faces) {
      refused.plan.hires = refused.plan.hires.map(entry =>
        Object.hasOwn(body.hire_faces, entry.key || entry.name)
          ? { ...entry, character: String(body.hire_faces[entry.key || entry.name] || '') }
          : entry
      );
    }
    if (refused && view.hireFaces instanceof Map) refused.hireFaces = view.hireFaces;
    if (refused && view.hireDrafts instanceof Map) refused.hireDrafts = view.hireDrafts;
    return { error: String(payload?.error || 'That did not go through.'), view: refused };
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

// folderAgentLive says the agent is working in the background and a card should
// keep following it: looking, setting up, or reading the songs after "Learn
// them". Takes the server's view.
export function folderAgentLive(view) {
  return view?.state === 'looking' || view?.state === 'setting_up' || folderAgentReading(view);
}

// folderAgentReading says the agent is still on the songs: reading them, or
// writing up what it learned. Takes the server's view.
export function folderAgentReading(view) {
  return view?.learning?.status === 'running' || view?.learning?.status === 'summarizing';
}

// followFolderAgent polls one folder agent while it looks, sets up or reads the
// songs, and hands each fresh view to onView(next, finished), finished marking
// the end of a run it watched. Returns a stop function.
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
    if (!current?.workspace_id || !folderAgentLive(current)) return;
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
const workspacePermissionWatches = new WeakMap();

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
    if (!folderAgentLive(raw)) return;
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
        if (folderAgentLive(next)) return;
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
  workspacePermissionWatches.get(host)?.();
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
  // The look's report reaches the Commander's conversation a moment after the
  // view says "reported" (workspace chat sync): until the view names a message
  // newer than the one current while the agent looked, ask again a few times.
  let lookMessage = null;
  let chatRetries = 0;
  const awaitChat = current => {
    const state = String(current?.state || '');
    const id = String(current?.chat?.message_id || '');
    if (state === 'looking') {
      lookMessage = id;
      chatRetries = 0;
      return;
    }
    if (lookMessage === null || (state !== 'reported' && state !== 'failed')) return;
    if ((id && id !== lookMessage) || chatRetries >= CHAT_RETRIES) {
      lookMessage = null;
      return;
    }
    chatRetries += 1;
    setTimeout(async () => {
      if (!isActive() || lookMessage === null) return;
      let next = null;
      try {
        next = await fetchFolderAgent(workspaceID, fetchImpl);
      } catch (_) {
        next = null;
      }
      if (next && isActive()) draw(next);
      else lookMessage = null;
    }, pollMs);
  };
  let latestRaw = raw;
  // What the owner opened in the drawer's cards, kept across the page's redraws,
  // and whether an answer is in flight (a redrawn card must not offer another).
  const chatState = {
    messageID: '',
    open: new Set(),
    pending: false,
    goal: null,
    focusAfter: null
  };
  let acting = false;
  // refreshRun asks for the run again a few times; null when it cannot be had.
  const refreshRun = async () => {
    for (let attempt = 0; attempt <= CHAT_RETRIES; attempt += 1) {
      const fresh = await fetchFolderAgent(workspaceID, fetchImpl).catch(() => null);
      if (fresh || !isActive()) return fresh;
      await new Promise(resolve => setTimeout(resolve, pollMs));
    }
    return null;
  };
  // A reply that arrives late (a poll that began before an answer, an answer
  // for a report since replaced) must not draw over what is newer. Views are
  // ordered by the record's own updated_at, which every write moves forward;
  // an equal one still draws (the offer also depends on state outside the
  // record). Another workspace's view is never drawn here.
  const staleView = next => {
    if (!next || !latestRaw || next === latestRaw) return false;
    if (next.workspace_id && String(next.workspace_id) !== String(workspaceID)) return true;
    const drawn = Date.parse(latestRaw.updated_at);
    const incoming = Date.parse(next.updated_at);
    return Number.isFinite(drawn) && Number.isFinite(incoming) && incoming < drawn;
  };
  const draw = (current, error = '', opening = false) => {
    if (staleView(current)) return;
    latestRaw = current;
    const view = folderAgentReportView(current);
    const onAction = async (action, button, extra) => {
      // One write at a time for the workspace: a second press while one is in
      // flight (a double click, or a card redrawn under the first) is dropped.
      if (acting) return;
      acting = true;
      chatState.pending = true;
      if (button) button.disabled = true;
      if (action === 'set_up' || action === 'look_further' || action === 'start_next')
        followed = true;
      let result;
      try {
        result = await postFolderAgentAction(view, action, fetchImpl, extra);
      } catch (_) {
        result = { error: 'That did not go through.' };
      }
      // A refusal that carried no view leaves it unknown whether the server
      // recorded the answer. Ask what it holds before offering another write, so
      // a repeat press cannot answer twice.
      let next = result.view || null;
      if (!next && result.error) next = await refreshRun();
      if (!isActive()) {
        acting = false;
        return;
      }
      acting = false;
      // Still unknown: keep the controls quiet rather than offer a blind write.
      chatState.pending = !next && Boolean(result.error);
      const error =
        chatState.pending && result.error
          ? `${result.error} Reload to see where this stands.`
          : result.error || '';
      // The answer is explicit, so focus may follow it (see the thread renderer).
      chatState.focusAfter = { action, failed: Boolean(result.error) };
      draw(next || latestRaw, error);
      follow(latestRaw);
    };
    // What the owner opened inside the drawer's cards belongs to one report: a
    // newer report starts closed again.
    const messageID = String(current?.chat?.message_id || '');
    if (messageID !== chatState.messageID) {
      chatState.messageID = messageID;
      chatState.open.clear();
    }
    // The drawer asks for the chat presentation by name; the Map's balloon and
    // the page ask for the one card, which is also what the drawer gets for a
    // report that is not a completed music setup.
    const card = (compact, { presentation } = {}) =>
      (presentation === 'chat' &&
        renderFolderAgentChatThread(view, doc, {
          error,
          onAction,
          fetchImpl,
          state: chatState
        })) ||
      renderFolderAgentReport(view, doc, { error, opening, onAction, compact });
    // The drawer draws the same report under its message in the Commander's
    // conversation (workspace chat sync).
    publishFolderAgentChat({ workspaceID: view.workspaceID, raw: current, card, error });
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
    awaitChat(current);
  };
  let stop = () => {};
  const follow = current => {
    stop();
    stop = followFolderAgent(current, {
      fetchImpl,
      isActive,
      pollMs,
      onView: (next, finished) => {
        // A fresh view from the server settles an answer whose outcome was unknown.
        if (!acting) chatState.pending = false;
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
  // The install finishes before consent. Follow only an actual saved-grant
  // transition, not every scan/count notification, to refresh the banner.
  let stopped = false;
  let checking = false;
  const permissionChanged = async event => {
    const state = event.detail?.state?.actionState;
    if (
      stopped ||
      checking ||
      !isActive() ||
      host.isConnected === false ||
      state?.workspaceId !== workspaceID ||
      state.status?.state !== 'ready' ||
      latestRaw?.setup?.status !== 'done' ||
      !latestRaw?.plan?.steps?.some(step => step.kind === 'file_curator' && step.chosen) ||
      state.configured === !latestRaw.filing_permission_required
    )
      return;
    checking = true;
    try {
      const next = await fetchFolderAgent(workspaceID, fetchImpl).catch(() => null);
      if (next && !stopped && isActive()) draw(next);
    } finally {
      checking = false;
    }
  };
  doc.addEventListener?.('ori:file-janitor-changed', permissionChanged);
  workspacePermissionWatches.set(host, () => {
    stopped = true;
    doc.removeEventListener?.('ori:file-janitor-changed', permissionChanged);
  });
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
    onStarting,
    initialCard = null,
    pollMs,
    openPlace,
    navigate,
    openDelayMs
  } = {}
) {
  if (!host) return { error: 'That folder could not be chosen.' };
  let chosen;
  try {
    chosen = initialCard ? { card: initialCard } : await chooseFolder(body, fetchImpl);
  } catch (_) {
    chosen = { error: 'That folder could not be chosen right now.' };
  }
  if (!isActive()) return { cancelled: true };
  if (chosen.error || chosen.cancelled || !chosen.card) {
    return chosen.error ? { error: chosen.error } : { cancelled: true };
  }
  const card = folderAgentCardView(chosen.card);
  let busy = false;
  const draw = (error = '', duplicate = null) => {
    host.hidden = false;
    host.replaceChildren(renderFolderAgentCard(card, doc, { error, onStart }));
    if (duplicate?.route?.startsWith('/workspaces/') && !duplicate.route.includes('\\')) {
      const open = el(doc, 'a', 'folder-agent-card__open', 'Open it');
      open.href = duplicate.route;
      host.append(open);
    }
  };
  const onStart = async (brief, button, hire) => {
    if (busy) return;
    busy = true;
    onStarting?.(true);
    if (button) button.disabled = true;
    let result;
    try {
      result = await startFolderAgent(card.token, brief, fetchImpl, hire);
    } catch (_) {
      result = { error: 'The agent could not be started right now.' };
    }
    busy = false;
    onStarting?.(false);
    if (!isActive()) return;
    if (result.error || !result.view?.workspace_id) {
      card.brief = brief;
      const error = result.error || 'The agent could not be started right now.';
      draw(error, result.duplicate);
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
