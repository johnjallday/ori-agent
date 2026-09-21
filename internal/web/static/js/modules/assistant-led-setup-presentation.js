// Read-only "Assistant setup" presentation for the File Janitor workspace/team
// preparation. Everything shown comes from the server's `milestones`; timers
// here may only change which already-receipted panel is in view, never a status.

import { createModalReplay, replaySources } from './assistant-led-setup-replay.js';

const STATUS_LABELS = {
  pending: 'Waiting',
  creating: 'Creating',
  reusing: 'Reusing',
  created: 'Created',
  reused: 'Reused',
  failed: 'Failed',
  needs_review: 'Needs review'
};
const RECEIPTED = new Set(['created', 'reused']);
const IN_FLIGHT = new Set(['creating', 'reusing']);
const STOPPED = new Set(['failed', 'needs_review']);

export const MODE_LABELS = {
  requested: 'Requested — waiting for Ori',
  live: 'Live — Ori is preparing this now',
  walkthrough: 'Receipt walkthrough',
  stopped: 'Stopped — needs your attention'
};

const MODE_NOTES = {
  requested: 'Showing what you reviewed. Nothing is recorded yet.',
  live: 'Ori reported this step in progress. The status updates only from what Ori records.',
  walkthrough:
    'This replays what Ori already recorded. Nothing is being created now, and closing it changes nothing.',
  stopped:
    'Ori stopped at this step. What was already recorded is kept; nothing is retried for you.'
};

// User-safe explanations for the closed set of server error codes.
const STOP_MESSAGES = {
  agent_root_unavailable: 'Ori could not reach your agents folder, so nothing was created.',
  team_plan_changed:
    'The File Curator setup changed after your review, so Ori stopped before creating anything.',
  preparation_interrupted:
    'Ori stopped before it finished. Continuing checks what already exists first, so nothing is created twice.',
  workspace_outcome_unresolved:
    'Ori could not prove whether this step finished, so it will not retry it for you.',
  agent_not_attached: 'The File Curator profile exists but is not attached to the workspace.'
};

export function stopMessage(code) {
  return Object.hasOwn(STOP_MESSAGES, code)
    ? STOP_MESSAGES[code]
    : 'Ori stopped at this step, so it will not retry it for you.';
}

// Turns one server milestone into display data. Identity is the server's `id`;
// the display name is never read as an identifier. A status the client does not
// know is shown as "Needs review", never as success.
export function assistantSetupMilestoneView(milestone) {
  const status = Object.hasOwn(STATUS_LABELS, milestone?.status)
    ? milestone.status
    : 'needs_review';
  const fallbackName = milestone?.kind === 'workspace' ? 'File Janitor' : 'Team member';
  const details = [];
  if (milestone?.needs_model) details.push('Configured; chat needs a model');
  const blueprint = milestone?.blueprint_id
    ? `${milestone.blueprint_id}${milestone.blueprint_version ? ` v${milestone.blueprint_version}` : ''}`
    : '';
  return {
    key: String(milestone?.id || ''),
    kind: String(milestone?.kind || ''),
    name: String(milestone?.name || fallbackName),
    roleID: String(milestone?.role_id || ''),
    action: milestone?.action === 'reuse' ? 'reuse' : 'create',
    status,
    statusLabel: STATUS_LABELS[status],
    details,
    blueprint,
    ownership: String(milestone?.ownership || ''),
    recordedAt: String(milestone?.recorded_at || ''),
    errorCode: String(milestone?.error_code || ''),
    resourceID: String(milestone?.resource_id || '')
  };
}

export function assistantSetupMilestoneViews(milestones) {
  return Array.isArray(milestones) ? milestones.map(assistantSetupMilestoneView) : [];
}

// The reviewed values shown before Ori has reported anything. Every entry is
// `pending`: this is what was approved, never a claim that it happened.
export function requestedMilestones(proposal) {
  if (!proposal) return [];
  const blueprint = {
    blueprint_id: proposal.blueprint_id,
    blueprint_version: proposal.blueprint_version
  };
  if (proposal.mode === 'adopt') {
    return [
      {
        id: 'workspace',
        kind: 'workspace',
        name: 'File Janitor',
        action: 'reuse',
        status: 'pending'
      },
      {
        id: 'existing_team',
        kind: 'existing_team',
        name: 'Existing team kept as is',
        action: 'reuse',
        status: 'pending'
      }
    ];
  }
  const roles = (proposal.team || []).map(role => ({
    id: `role:${role?.role_id || ''}`,
    kind: 'agent_role',
    name: role?.name,
    role_id: role?.role_id,
    action: role?.action,
    status: 'pending',
    needs_model: role?.model_configured === false,
    ...blueprint
  }));
  return [
    {
      id: 'workspace',
      kind: 'workspace',
      name: 'File Janitor',
      action: 'create',
      status: 'pending',
      ...blueprint
    },
    ...roles
  ];
}

// requested: nothing reported yet; live: the server reports an attempt in
// flight; stopped: a failed/needs-review step; walkthrough: everything is
// receipted (the normal case, because preparation takes milliseconds).
export function presentationMode(milestones) {
  const views = assistantSetupMilestoneViews(milestones);
  if (!views.length) return 'requested';
  if (views.some(view => IN_FLIGHT.has(view.status))) return 'live';
  if (views.some(view => STOPPED.has(view.status))) return 'stopped';
  if (views.every(view => RECEIPTED.has(view.status))) return 'walkthrough';
  return 'requested';
}

export function panelStatus(entries) {
  if (!entries.length) return 'pending';
  const statuses = entries.map(entry => entry.status);
  for (const status of ['failed', 'needs_review', 'creating', 'reusing', 'pending']) {
    if (statuses.includes(status)) return status;
  }
  return statuses.includes('created') ? 'created' : 'reused';
}

function plural(count, one, many) {
  return count === 1 ? one : many;
}

// Three panels, list-driven so several roles need no code change: the
// workspace, every agent (or the adopted team), and a receipt summary of what
// was actually recorded.
export function buildPanels(milestones) {
  const views = assistantSetupMilestoneViews(milestones);
  const workspaces = views.filter(view => view.kind === 'workspace');
  const agents = views.filter(view => view.kind !== 'workspace');
  const receipted = views.filter(view => RECEIPTED.has(view.status));
  const reuseAll = agents.length > 0 && agents.every(view => view.action === 'reuse');
  const adopted = agents.some(view => view.kind === 'existing_team');
  const workspaceTitle = workspaces.some(view => view.action === 'reuse')
    ? 'Reuse Workspace'
    : 'Create Workspace';
  let agentTitle = plural(agents.length, 'Create Agent', 'Create Agents');
  if (adopted) agentTitle = 'Existing team';
  else if (reuseAll) agentTitle = plural(agents.length, 'Reuse Agent', 'Reuse Agents');
  return [
    {
      id: 'workspace',
      title: workspaceTitle,
      entries: workspaces,
      status: panelStatus(workspaces)
    },
    { id: 'agents', title: agentTitle, entries: agents, status: panelStatus(agents) },
    { id: 'receipt', title: 'Receipt summary', entries: receipted, status: panelStatus(receipted) }
  ];
}

// How many panels may be viewed. In walkthrough mode every panel. A stopped
// walkthrough ends on the panel that stopped, with the panels before it (and
// their receipts) still reachable. Otherwise a panel opens only once its own
// entries are receipted or live, and the receipt summary needs at least one
// receipt. The first panel always shows.
export function availablePanelCount(mode, panels) {
  if (mode === 'walkthrough') return panels.length;
  if (mode === 'stopped') {
    const stoppedAt = panels.findIndex(panel => STOPPED.has(panel.status));
    if (stoppedAt >= 0) return stoppedAt + 1;
  }
  let count = 1;
  for (let index = 1; index < panels.length; index += 1) {
    const panel = panels[index];
    const reachable =
      panel.id === 'receipt' ? panel.entries.length > 0 : panel.status !== 'pending';
    if (!reachable) break;
    count = index + 1;
  }
  return count;
}

export function receiptTime(milestones) {
  const stamps = assistantSetupMilestoneViews(milestones)
    .filter(view => RECEIPTED.has(view.status) && view.recordedAt)
    .map(view => new Date(view.recordedAt))
    .filter(date => !Number.isNaN(date.getTime()))
    .sort((a, b) => b - a);
  return stamps.length ? stamps[0] : null;
}

export function modeLine(mode, milestones, locale) {
  const label = MODE_LABELS[mode] || MODE_LABELS.requested;
  const when = mode === 'walkthrough' ? receiptTime(milestones) : null;
  return when ? `${label} · recorded ${when.toLocaleString(locale)}` : label;
}

export function modeNote(mode) {
  return MODE_NOTES[mode] || MODE_NOTES.requested;
}

// One short message per status change: the panel's title and its status.
export function announcementFor(mode, panels) {
  const parts = panels
    .filter(panel => panel.entries.length)
    .map(panel => `${panel.title}: ${STATUS_LABELS[panel.status] || 'Needs review'}`);
  return `${MODE_LABELS[mode] || MODE_LABELS.requested}. ${parts.join('. ')}`.trim();
}

// Auto-advance across already-available panels. The timer only calls
// onAdvance(index); it cannot read or change a milestone. Inert under reduced
// motion. Timers are injected so tests never wait.
export function createStepScheduler({
  setTimer = (fn, ms) => globalThis.setTimeout(fn, ms),
  clearTimer = id => globalThis.clearTimeout(id),
  intervalMs = 5200,
  reducedMotion = false,
  onAdvance = () => {}
} = {}) {
  let handle = null;
  function stop() {
    if (handle !== null) clearTimer(handle);
    handle = null;
  }
  function start({ current, available, delay }) {
    stop();
    if (reducedMotion || current + 1 >= available) return false;
    handle = setTimer(() => {
      handle = null;
      onAdvance(current + 1);
    }, delay ?? intervalMs);
    return true;
  }
  return {
    start,
    stop,
    get pending() {
      return handle !== null;
    }
  };
}

// "Workspace", "Agent", or "Team" (an adopted team has no reviewed role).
export function kindLabel(view) {
  if (view?.kind === 'workspace') return 'Workspace';
  if (view?.kind === 'existing_team') return 'Team';
  return 'Agent';
}

// Up to two letters for an avatar; identity is never derived from them.
export function initialsFor(name) {
  const words = String(name || '')
    .trim()
    .split(/\s+/)
    .filter(Boolean);
  if (!words.length) return '?';
  const letters = words.length === 1 ? words[0].slice(0, 2) : words[0][0] + words[1][0];
  return letters.toUpperCase();
}

function actionLabel(action) {
  return action === 'reuse' ? 'Reuse' : 'Create';
}

function ownershipLabel(ownership) {
  if (ownership === 'created') return 'Created by this setup';
  if (ownership === 'adopted' || ownership === 'updated') return 'Existing, kept as is';
  return '';
}

export function entryFacts(view, locale) {
  const facts = [['Reviewed action', actionLabel(view.action)]];
  if (view.roleID) facts.push(['Role', view.roleID]);
  if (view.blueprint) facts.push(['Blueprint', view.blueprint]);
  const ownership = ownershipLabel(view.ownership);
  if (ownership) facts.push(['Ownership', ownership]);
  if (view.recordedAt) {
    const when = new Date(view.recordedAt);
    if (!Number.isNaN(when.getTime())) facts.push(['Recorded', when.toLocaleString(locale)]);
  }
  if (view.resourceID) facts.push(['ID', view.resourceID]);
  if (view.errorCode) {
    facts.push(['What happened', stopMessage(view.errorCode)]);
    facts.push(['Reason code', view.errorCode]);
  }
  return facts;
}

// What the read-only "window" for one milestone shows: a title bar, the fields
// the reviewed setup filled in, and the button the creator pressed. Every value
// is a reviewed or receipted fact (no path, no prompt, no digest). The window is
// a picture of what Ori recorded, not a form: it has no input to type into.
export function windowSpec(view, agents = []) {
  const reuse = view.action === 'reuse';
  if (view.kind === 'workspace') {
    const team = agents.length
      ? agents
          .map(agent => `${reuse ? 'Keep' : actionLabel(agent.action)} ${agent.name}`)
          .join(', ')
      : 'Reviewed team';
    return {
      title: reuse ? 'Reuse Workspace' : 'Create Workspace',
      steps: ['Blueprint', 'Details', 'Team', 'Review'],
      fields: [
        ['Blueprint', view.blueprint || 'file-janitor'],
        ['Name', view.name],
        ['Team', team],
        ['Location', 'Your Ori Workspaces folder']
      ],
      button: reuse ? 'Use workspace' : 'Create Workspace'
    };
  }
  if (view.kind === 'existing_team') {
    return {
      title: 'Existing team',
      steps: [],
      fields: [
        ['Team', view.name],
        ['Reviewed action', 'Reuse']
      ],
      button: 'Keep team'
    };
  }
  return {
    title: reuse ? 'Reuse Agent' : 'Create Agent',
    steps: [],
    fields: [
      ['Name', view.name],
      ['Role', view.roleID || 'Team member'],
      ['Source', reuse ? 'Existing agent in your roster' : 'New agent in your roster'],
      ['Model', view.details[0] || 'Configured']
    ],
    button: reuse ? 'Reuse Agent' : 'Create Agent'
  };
}

// The dialog controller. It sends no request of its own: every button either
// changes which panel is in view or closes the dialog.
export function createPresentation({
  document: doc,
  window: win,
  fetch,
  canOpen = () => true,
  onContinue = () => {},
  onClosed = () => {}
}) {
  const dialog = doc?.getElementById?.('assistantLedSetupDialog');
  if (!dialog) return null;
  const els = {
    title: doc.getElementById('assistantLedSetupDialogTitle'),
    mode: doc.getElementById('assistantLedSetupDialogMode'),
    note: doc.getElementById('assistantLedSetupDialogNote'),
    step: doc.getElementById('assistantLedSetupDialogStep'),
    progress: doc.getElementById('assistantLedSetupDialogProgress'),
    panels: doc.getElementById('assistantLedSetupDialogPanels'),
    live: doc.getElementById('assistantLedSetupDialogLive'),
    skip: doc.getElementById('assistantLedSetupDialogSkip'),
    back: doc.getElementById('assistantLedSetupDialogBack'),
    next: doc.getElementById('assistantLedSetupDialogNext'),
    proceed: doc.getElementById('assistantLedSetupDialogContinue'),
    close: doc.getElementById('assistantLedSetupDialogClose')
  };
  const state = {
    milestones: [],
    index: 0,
    invoker: null,
    announced: '',
    mode: 'requested',
    suppressRestore: false,
    replays: [],
    pendingStarts: [],
    token: 0
  };
  const fetchImpl = fetch || win?.fetch?.bind(win);
  const reduced = () => Boolean(win?.matchMedia?.('(prefers-reduced-motion: reduce)')?.matches);
  const scheduler = createStepScheduler({
    reducedMotion: false,
    setTimer: (fn, ms) => win.setTimeout(fn, ms),
    clearTimer: id => win.clearTimeout(id),
    onAdvance: index => {
      state.index = index;
      render({ announce: true });
    }
  });

  const isOpen = () => dialog.open === true || dialog.hasAttribute('open');

  // The picture for a receipt card: the workspace's building art when this page
  // has it, otherwise a workspace glyph; an agent gets an initials avatar.
  function kindVisual(view) {
    const visual = doc.createElement('span');
    visual.className = 'assistant-led-setup-dialog__visual';
    visual.setAttribute('aria-hidden', 'true');
    if (view.kind === 'workspace') {
      const art = win?.OriWorkspaceBuildingArt;
      const id = view.blueprint.split(' ')[0];
      const variant = id ? art?.variantForBlueprint?.(id, true) : '';
      const svg = variant ? art?.svgForVariant?.(variant, { context: 'catalog' }) : '';
      if (svg) {
        visual.classList.add('has-building-art');
        visual.innerHTML = svg;
      } else {
        visual.textContent = '🏢';
      }
    } else {
      visual.classList.add('is-avatar');
      visual.textContent = initialsFor(view.name);
    }
    return visual;
  }

  // The receipt as a small tree: the workspace, with its team under it.
  function receiptTree(entries) {
    const tree = doc.createElement('div');
    tree.className = 'assistant-led-setup-dialog__tree';
    const workspaces = entries.filter(view => view.kind === 'workspace');
    const team = entries.filter(view => view.kind !== 'workspace');
    workspaces.forEach(view => tree.append(entryNode(view)));
    if (team.length) {
      const branch = doc.createElement('div');
      branch.className = 'assistant-led-setup-dialog__tree-branch';
      const label = doc.createElement('span');
      label.className = 'assistant-led-setup-dialog__tree-label';
      label.textContent = workspaces.length ? 'works in this workspace' : 'team';
      branch.append(label);
      team.forEach(view => branch.append(entryNode(view)));
      tree.append(branch);
    }
    return tree;
  }

  function entryNode(view) {
    const node = doc.createElement('div');
    node.className = 'assistant-led-setup-dialog__entry';
    node.dataset.milestoneId = view.key;
    node.dataset.status = view.status;
    node.dataset.kind = view.kind;
    // What this is, at a glance: a workspace shows its building, an agent shows
    // an avatar, and a pill names the kind.
    const head = doc.createElement('div');
    head.className = 'assistant-led-setup-dialog__entry-head';
    head.append(kindVisual(view));
    const title = doc.createElement('div');
    title.className = 'assistant-led-setup-dialog__entry-title';
    const pill = doc.createElement('span');
    pill.className = 'assistant-led-setup-dialog__kind';
    pill.textContent = kindLabel(view);
    const name = doc.createElement('strong');
    name.textContent = view.name;
    title.append(pill, name);
    const chip = doc.createElement('span');
    chip.className = 'assistant-led-setup-dialog__chip';
    chip.textContent = view.statusLabel;
    head.append(title, chip);
    node.append(head);
    const list = doc.createElement('dl');
    list.className = 'assistant-led-setup-dialog__facts';
    // The kind pill and the name already say what this is and what was asked.
    const covered = new Set(['Reviewed action', 'Role', 'Blueprint']);
    entryFacts(view, undefined)
      .filter(([term]) => !covered.has(term))
      .forEach(([term, value]) => {
        const dt = doc.createElement('dt');
        dt.textContent = term;
        const dd = doc.createElement('dd');
        dd.textContent = value;
        list.append(dt, dd);
      });
    view.details.forEach(detail => {
      const dt = doc.createElement('dt');
      dt.textContent = 'Model';
      const dd = doc.createElement('dd');
      dd.textContent = detail;
      list.append(dt, dd);
    });
    node.append(list);
    return node;
  }

  // A picture of the reviewed window being filled in and its button pressed.
  // CSS plays the fill (staggered wipes) only under full motion; the data
  // attributes carry the truth (`status`), so a screenshot or reduced motion
  // shows the finished window at once. Nothing here is an input.
  function windowNode(view, agents) {
    const spec = windowSpec(view, agents);
    const node = doc.createElement('div');
    node.className = 'assistant-led-setup-dialog__window';
    node.dataset.milestoneId = view.key;
    node.dataset.status = view.status;
    node.setAttribute('role', 'group');
    node.setAttribute('aria-label', `${spec.title} (read-only preview)`);
    node.style.setProperty('--fields', String(spec.fields.length));
    const bar = doc.createElement('div');
    bar.className = 'assistant-led-setup-dialog__window-bar';
    const title = doc.createElement('strong');
    title.textContent = spec.title;
    const hint = doc.createElement('span');
    hint.textContent = 'preview';
    bar.append(title, hint);
    node.append(bar);
    if (spec.steps.length) {
      const steps = doc.createElement('ol');
      steps.className = 'assistant-led-setup-dialog__window-steps';
      spec.steps.forEach((label, index) => {
        const item = doc.createElement('li');
        item.style.setProperty('--n', String(index));
        item.textContent = label;
        steps.append(item);
      });
      node.append(steps);
    }
    const body = doc.createElement('div');
    body.className = 'assistant-led-setup-dialog__window-body';
    spec.fields.forEach(([label, value], index) => {
      const row = doc.createElement('div');
      row.className = 'assistant-led-setup-dialog__field';
      row.style.setProperty('--i', String(index));
      const name = doc.createElement('span');
      name.className = 'assistant-led-setup-dialog__field-label';
      name.textContent = label;
      const box = doc.createElement('span');
      box.className = 'assistant-led-setup-dialog__field-value';
      box.textContent = value;
      row.append(name, box);
      body.append(row);
    });
    node.append(body);
    const footer = doc.createElement('div');
    footer.className = 'assistant-led-setup-dialog__window-footer';
    const button = doc.createElement('span');
    button.className = 'assistant-led-setup-dialog__window-button';
    button.textContent = spec.button;
    const chip = doc.createElement('span');
    chip.className = 'assistant-led-setup-dialog__chip';
    chip.textContent = view.statusLabel;
    footer.append(button, chip);
    node.append(footer);
    const receipt = doc.createElement('dl');
    receipt.className = 'assistant-led-setup-dialog__facts assistant-led-setup-dialog__receipt';
    entryFacts(view, undefined)
      .filter(([term]) => term !== 'Reviewed action' && term !== 'Role' && term !== 'Blueprint')
      .forEach(([term, value]) => {
        const dt = doc.createElement('dt');
        dt.textContent = term;
        const dd = doc.createElement('dd');
        dd.textContent = value;
        receipt.append(dt, dd);
      });
    if (receipt.childElementCount) node.append(receipt);
    return node;
  }

  // Only a receipted create step with exactly one entry replays the real
  // modal, and only when that modal is present on this page. Everything else
  // (pending, stopped, reuse, adopt, several roles) keeps the plain windows,
  // so a step that did not finish never plays a click-through it did not have.
  function replaySpecFor(panel, panels, mode) {
    if (mode !== 'walkthrough' || panel.status !== 'created') return null;
    if (panel.id !== 'workspace' && panel.id !== 'agents') return null;
    if (panel.entries.length !== 1 || panel.entries[0].action !== 'create') return null;
    const sources = replaySources(doc);
    const view = panel.entries[0];
    const recorded = view.recordedAt ? new Date(view.recordedAt).toLocaleString() : '';
    if (panel.id === 'workspace') {
      const team = panels[1].entries;
      if (!sources.workspace || !team.length) return null;
      return {
        kind: 'workspace',
        sources,
        spec: {
          summary: `Replay of the Create Workspace window: blueprint ${view.name}, name ${view.name}, team ${team.map(member => member.name).join(', ')}.`,
          blueprintLabel: view.name,
          blueprintNote: view.blueprint,
          workspaceName: view.name,
          team: team.map(member => ({
            name: member.name,
            action: member.action,
            note: member.details[0] || ''
          })),
          createdButton: 'Created ✓',
          createdLine: recorded ? `Workspace created · ${recorded}` : 'Workspace created'
        }
      };
    }
    if (!sources.agent || !sources.agentForm) return null;
    return {
      kind: 'agent',
      sources,
      spec: {
        summary: `Replay of the Create Agent window: name ${view.name}.`,
        agentName: view.name,
        modelText: view.details[0] || 'Ori default model',
        createdButton: 'Created ✓',
        createdLine: recorded ? `Agent created · ${recorded}` : 'Agent created'
      }
    };
  }

  function stopReplays() {
    state.token += 1;
    state.replays.forEach(replay => replay.stop());
    state.replays = [];
    state.pendingStarts = [];
    dialog.dataset.stage = 'plain';
  }

  function startReplays(panels, mode, reducedMotion, available) {
    const token = state.token;
    els.panels.querySelectorAll('.assistant-led-setup-dialog__stage').forEach(host => {
      const section = host.closest('.assistant-led-setup-dialog__panel');
      const panel = panels.find(item => item.id === section?.dataset.panelId);
      const plan = panel && replaySpecFor(panel, panels, mode);
      if (!plan || section.hidden) return;
      const replay = createModalReplay({
        doc,
        win,
        host,
        kind: plan.kind,
        spec: plan.spec,
        sources: plan.sources,
        fetchImpl
      });
      state.replays.push(replay);
      dialog.dataset.stage = 'modal';
      // The panel must be on screen to be measured, so this runs after the
      // dialog is open; render() is called again from open().
      const run = () => (reducedMotion ? replay.showFinal() : replay.play());
      if (isOpen()) {
        void run().then(() => {
          if (reducedMotion || state.token !== token) return;
          scheduler.start({ current: state.index, available, delay: 1600 });
        });
      } else {
        state.pendingStarts.push(() =>
          run().then(() => {
            if (reducedMotion || state.token !== token) return;
            scheduler.start({ current: state.index, available, delay: 1600 });
          })
        );
      }
    });
  }

  function render({ announce = false } = {}) {
    stopReplays();
    const mode = presentationMode(state.milestones);
    state.mode = mode;
    const panels = buildPanels(state.milestones);
    const available = availablePanelCount(mode, panels);
    const reducedMotion = reduced();
    scheduler.stop();
    state.index = Math.max(0, Math.min(state.index, available - 1));
    dialog.dataset.mode = mode;
    dialog.dataset.motion = reducedMotion ? 'reduced' : 'full';
    els.mode.textContent = modeLine(mode, state.milestones);
    els.note.textContent = modeNote(mode);
    els.panels.replaceChildren();
    panels.forEach((panel, index) => {
      const section = doc.createElement('section');
      section.className = 'assistant-led-setup-dialog__panel';
      section.dataset.panelId = panel.id;
      section.dataset.status = panel.status;
      const visible = reducedMotion ? index < available : index === state.index;
      section.hidden = !visible;
      const heading = doc.createElement('h5');
      heading.textContent = panel.title;
      section.append(heading);
      if (!panel.entries.length) {
        const empty = doc.createElement('p');
        empty.className = 'assistant-led-setup-dialog__empty';
        empty.textContent = 'Nothing has been recorded for this step yet.';
        section.append(empty);
      }
      const agents = panels[1].entries;
      if (replaySpecFor(panel, panels, mode)) {
        // The real modal, clicked through (built after the panel is attached).
        const host = doc.createElement('div');
        host.className = 'assistant-led-setup-dialog__stage';
        section.append(host);
      } else if (panel.id === 'receipt') {
        if (panel.entries.length) section.append(receiptTree(panel.entries));
      } else {
        panel.entries.forEach(view => section.append(windowNode(view, agents)));
      }
      els.panels.append(section);
    });
    startReplays(panels, mode, reducedMotion, available);
    // A visual progress rail: one node per panel, lit as far as is reachable.
    els.progress?.replaceChildren();
    panels.forEach((panel, index) => {
      const node = doc.createElement('li');
      node.textContent = panel.title;
      node.dataset.state =
        index < state.index ? 'done' : index === state.index ? 'current' : 'later';
      node.dataset.status = panel.status;
      els.progress?.append(node);
    });
    els.step.textContent = reducedMotion
      ? `${available} of ${panels.length} steps available`
      : `Step ${state.index + 1} of ${panels.length}`;
    // The end is the last panel the server's state allows, which for a stopped
    // run is the panel that stopped. Continue then hands focus to the card,
    // where only the server-projected retry or manual action is offered.
    const atEnd = state.index >= available - 1;
    els.back.hidden = reducedMotion || state.index === 0;
    els.next.hidden = reducedMotion || atEnd;
    els.skip.hidden = atEnd || reducedMotion;
    els.proceed.hidden = !(reducedMotion || atEnd);
    els.proceed.disabled = mode === 'requested' || mode === 'live';
    // A panel that replays the real modal advances when the replay ends; the
    // others advance on the plain timer.
    if (!reducedMotion && !state.replays.length) {
      scheduler.start({ current: state.index, available });
    }
    // Announce only what the viewer can reach from here.
    const message = announcementFor(mode, panels.slice(0, available));
    if (announce && message !== state.announced) els.live.textContent = message;
    state.announced = message;
  }

  function open(milestones, { invoker = null } = {}) {
    if (!canOpen()) return false;
    state.milestones = Array.isArray(milestones) ? milestones : [];
    state.index = 0;
    state.invoker = invoker || doc.activeElement || null;
    state.announced = '';
    render({ announce: true });
    if (!isOpen()) {
      if (typeof dialog.showModal === 'function') dialog.showModal();
      else dialog.setAttribute('open', '');
    }
    els.title?.focus();
    // Replays need the dialog on screen to be measured, so they start now.
    const starts = state.pendingStarts;
    state.pendingStarts = [];
    starts.forEach(start => void start());
    return true;
  }

  // Server projections replace what is shown; the dialog never invents status.
  function update(milestones) {
    const next = Array.isArray(milestones) ? milestones : [];
    // Re-render only on a real change, so a card refresh does not restart the
    // fill animation the viewer is watching.
    const changed = JSON.stringify(next) !== JSON.stringify(state.milestones);
    state.milestones = next;
    if (isOpen() && changed) render({ announce: true });
  }

  // Focus is restored in exactly one place, when the dialog has closed, so Esc,
  // Close, and Skip behave the same. Continue suppresses it: it moves focus to
  // the card's next control itself.
  function finishClose() {
    // The `close` event is asynchronous. If the dialog was reopened before it
    // arrived, this is a stale event for the previous viewing: leave the new
    // one (its replay, its timers, its focus) alone.
    if (isOpen()) {
      state.suppressRestore = false;
      return;
    }
    scheduler.stop();
    stopReplays();
    const invoker = state.invoker;
    const suppress = state.suppressRestore;
    state.invoker = null;
    state.suppressRestore = false;
    if (!suppress) onClosed(invoker);
  }

  function close({ restoreFocus = true } = {}) {
    scheduler.stop();
    state.suppressRestore = !restoreFocus;
    if (!isOpen()) {
      state.suppressRestore = false;
      return;
    }
    if (typeof dialog.close === 'function') dialog.close();
    else {
      dialog.removeAttribute('open');
      finishClose();
    }
  }

  function step(delta) {
    const panels = buildPanels(state.milestones);
    const available = availablePanelCount(state.mode, panels);
    state.index = Math.max(0, Math.min(state.index + delta, available - 1));
    render({ announce: true });
  }

  els.skip?.addEventListener('click', () => close());
  els.close?.addEventListener('click', () => close());
  els.back?.addEventListener('click', () => step(-1));
  els.next?.addEventListener('click', () => step(1));
  els.proceed?.addEventListener('click', () => {
    close({ restoreFocus: false });
    onContinue();
  });
  // Esc raises `cancel`; let the dialog close, then restore focus like Close.
  dialog.addEventListener('cancel', () => {
    scheduler.stop();
  });
  dialog.addEventListener('close', finishClose);

  return { open, update, close, isOpen, state };
}
