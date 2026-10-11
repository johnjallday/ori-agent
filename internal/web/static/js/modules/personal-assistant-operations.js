/**
 * personal-assistant-operations.js
 *
 * The Watch control and the operation status line in the Personal Assistant
 * drawer. It says only what was observed: a step is "opening" until the
 * arriving page reported arrival, and "selected" only once the page's own
 * controller said so. Help's coachmarks are untouched; this highlight is its
 * own, and it follows a step that already happened.
 */

import { OperationClient, PHASE, TAB_CHANNEL } from './ori-operation-client.js';
import {
  createOperationTargets,
  browserOperationEnvironment,
  operationWorkspaceURL
} from './ori-operation-targets.js';

const HIGHLIGHT_CLASS = 'ori-operation-highlight';
const HIGHLIGHT_MS = 4000;
const HIGHLIGHT_FOLLOW_MS = 200;

const PAUSE_TEXT = {
  moved_elsewhere: 'Paused: you are on another page.',
  unsaved_work: 'Paused: this page has unsaved changes.',
  reloaded: 'Paused after the page reloaded.',
  unreachable: 'Ori could not be reached. Nothing was changed.',
  conflict: 'This request changed while Ori was working on it.',
  refused: 'This page could not confirm the step.'
};

const ERROR_TEXT = {
  turn_not_saved: 'That request was not recorded, so nothing will open.'
};

function command(snapshot, action) {
  return ((snapshot && snapshot.commands) || []).find(item => item.action === action) || null;
}

function target(snapshot) {
  const context = (snapshot && snapshot.context) || {};
  const workspace = String(context.workspace_name || 'the workspace');
  const agent = String(context.agent_name || '');
  return { workspace, agent, slug: String(context.workspace_slug || '') };
}

/**
 * What the drawer shows for an operation, from recorded facts only.
 *
 * @param {object} state
 * @param {object|null} state.snapshot - the operation as this tab may see it
 * @param {string} state.phase - the client's phase
 * @param {string} [state.pauseReason]
 * @param {boolean} [state.capable] - whether the assistant can operate here
 * @param {boolean} [state.watch] - this tab's Watch choice
 * @param {string} [state.error] - why a requested operation was not recorded
 * @returns {{watchVisible:boolean, watchChecked:boolean, watchHint:string, text:string,
 *   tone:string, actions:Array<{id:string,label:string,primary?:boolean}>, link:{href:string,label:string}|null}}
 */
export function operationView(state = {}) {
  const snapshot = state.snapshot || null;
  const watch = Boolean(state.watch);
  const view = {
    watchVisible: Boolean(state.capable) || Boolean(snapshot),
    watchChecked: watch,
    watchHint: watch
      ? 'Ori may move this tab when you ask it to open something.'
      : 'Ori will ask before it moves this tab.',
    text: '',
    tone: 'quiet',
    actions: [],
    link: null
  };
  if (!snapshot) {
    if (state.error) {
      view.text =
        ERROR_TEXT[state.error] || 'Ori could not record that request. Nothing will open.';
      view.tone = 'attention';
    }
    return view;
  }

  const { workspace, agent, slug } = target(snapshot);
  const open = command(snapshot, 'open_workspace');
  const select = command(snapshot, 'select_agent');
  const failed = (snapshot.commands || []).find(item => item.status === 'unavailable');
  const opened = Boolean(open && open.status === 'acknowledged');
  const selected = Boolean(select && select.status === 'acknowledged');
  const both = agent ? ` and select ${agent}` : '';
  const manual = () => {
    const href = operationWorkspaceURL(slug);
    return href ? { href, label: `Open ${workspace} yourself` } : null;
  };
  const dismiss = { id: 'dismiss', label: 'Dismiss' };

  switch (state.phase) {
    case PHASE.ENDED:
      if (snapshot.status === 'expired') {
        view.text = 'That request ran out of time. Ask again to repeat it.';
      } else if (snapshot.status === 'invalidated') {
        view.text =
          'That request is no longer valid because your assistant, the conversation or the workspace changed.';
      }
      if (view.text) {
        view.tone = 'attention';
        view.actions = [dismiss];
        view.link = manual();
      }
      return view;
    case PHASE.RUNNING:
    case PHASE.NAVIGATING:
      view.text =
        opened && agent ? `Opened ${workspace}. Selecting ${agent}…` : `Opening ${workspace}…`;
      view.tone = 'working';
      return view;
    case PHASE.WAITING:
      view.text = `Ready to open ${workspace}${both}. Nothing moves until you choose.`;
      view.actions = [{ id: 'resume', label: `Open ${workspace}`, primary: true }, dismiss];
      return view;
    case PHASE.PAUSED:
      view.text = PAUSE_TEXT[state.pauseReason] || 'Paused.';
      view.tone = 'attention';
      view.actions = [{ id: 'resume', label: 'Resume', primary: true }, dismiss];
      view.link = manual();
      return view;
    case PHASE.SETTLED:
      if (failed) {
        const what =
          failed.action === 'select_agent' ? `select ${agent || 'the agent'}` : `open ${workspace}`;
        const reason = String(failed.reason || '').trim();
        view.text = `Ori could not ${what}${opened && failed.action === 'select_agent' ? ` after opening ${workspace}` : ''}.${reason ? ' ' + reason : ''}`;
        view.tone = 'attention';
        view.actions = [dismiss];
        view.link = opened ? null : manual();
      } else if (opened) {
        view.text = selected
          ? `Opened ${workspace} and selected ${agent}.`
          : `Opened ${workspace}.`;
        view.tone = 'done';
        view.actions = [dismiss];
      }
      // Steps that were replaced by a newer request say nothing here.
      return view;
    default:
      return view;
  }
}

/**
 * Mark the control Ori just used. The page re-renders its controls, replacing
 * the marked node with an identical one, so the mark follows the live node for
 * as long as it is shown instead of surviving on a detached one.
 */
function applyHighlight(highlight) {
  const live = () => {
    const located = typeof highlight.locate === 'function' ? highlight.locate() : null;
    return (
      located || (highlight.element && highlight.element.isConnected ? highlight.element : null)
    );
  };
  const first = live();
  if (!first) return;
  first.classList.add(HIGHLIGHT_CLASS);
  // Bring the real control into view, clear of the page edge, without taking
  // focus from the user.
  if (typeof first.scrollIntoView === 'function') {
    first.scrollIntoView({ block: 'center', inline: 'nearest' });
  }
  const until = Date.now() + HIGHLIGHT_MS;
  const follow = window.setInterval(() => {
    const element = live();
    if (Date.now() >= until) {
      window.clearInterval(follow);
      if (element) element.classList.remove(HIGHLIGHT_CLASS);
      return;
    }
    if (element && !element.classList.contains(HIGHLIGHT_CLASS))
      element.classList.add(HIGHLIGHT_CLASS);
  }, HIGHLIGHT_FOLLOW_MS);
}

async function apiRequest(method, path, body) {
  const response = await fetch(path, {
    method,
    cache: 'no-store',
    headers: body
      ? { Accept: 'application/json', 'Content-Type': 'application/json' }
      : { Accept: 'application/json' },
    body: body ? JSON.stringify(body) : undefined
  });
  let data = null;
  try {
    data = await response.json();
  } catch (_) {
    data = null;
  }
  if (!response.ok) {
    const error = new Error('operation request failed');
    error.status = response.status;
    error.data = data;
    throw error;
  }
  return data;
}

function mount() {
  const els = {
    root: document.getElementById('personalAssistantOperation'),
    watchRow: document.getElementById('personalAssistantWatchRow'),
    watch: document.getElementById('personalAssistantWatch'),
    hint: document.getElementById('personalAssistantWatchHint'),
    status: document.getElementById('personalAssistantOperationStatus'),
    actions: document.getElementById('personalAssistantOperationActions')
  };
  if (!els.root || !els.watch || !els.status || !els.actions) return null;

  const client = new OperationClient({
    request: apiRequest,
    storage: window.sessionStorage,
    targets: createOperationTargets(browserOperationEnvironment()),
    navigate: url => window.location.assign(url)
  });
  // The drawer may have read the relationship before this module mounted.
  const known = window.PersonalAssistantPanel?._state?.personalAssistant?.availability?.operations;
  const state = { capable: Boolean(known && known.available), error: '', shownHighlight: '' };

  function render() {
    const view = operationView({
      snapshot: client.snapshot,
      phase: client.phase,
      pauseReason: client.pauseReason,
      capable: state.capable,
      watch: client.watchPreference(),
      error: state.error
    });
    // The Watch chip lives in the composer's chip row, so it costs the drawer
    // no height. Its hint is the chip's description and tooltip.
    if (els.watchRow) {
      els.watchRow.hidden = !view.watchVisible;
      els.watchRow.title = view.watchHint;
    }
    els.watch.checked = view.watchChecked;
    if (els.hint) els.hint.textContent = view.watchHint;
    els.status.textContent = view.text;
    els.status.dataset.tone = view.tone;
    els.status.hidden = !view.text;
    els.actions.replaceChildren();
    view.actions.forEach(action => {
      const button = document.createElement('button');
      button.type = 'button';
      button.className = 'pa-operation__action' + (action.primary ? ' is-primary' : '');
      button.dataset.operationAction = action.id;
      button.textContent = action.label;
      els.actions.appendChild(button);
    });
    if (view.link) {
      const link = document.createElement('a');
      link.className = 'pa-operation__link';
      link.href = view.link.href;
      link.textContent = view.link.label;
      els.actions.appendChild(link);
    }
    els.actions.hidden = !els.actions.childElementCount;
    // The status section exists only while there is something to say.
    els.root.hidden = !view.text;

    const highlight = client.highlight;
    if (highlight && highlight.commandId !== state.shownHighlight) {
      state.shownHighlight = highlight.commandId;
      applyHighlight(highlight);
    }
  }

  els.watch.addEventListener('change', () => {
    const on = els.watch.checked;
    client.setWatchPreference(on);
    // The choice applies to the operation in front of the user too. Turning it
    // off stops this tab being moved; it cancels nothing.
    if (client.snapshot && client.phase !== PHASE.ENDED && client.phase !== PHASE.SETTLED) {
      void (on ? client.resume() : client.setWatching(false));
    }
  });
  els.actions.addEventListener('click', event => {
    const button = event.target.closest('[data-operation-action]');
    if (!button) return;
    state.error = '';
    if (button.dataset.operationAction === 'resume') void client.resume();
    if (button.dataset.operationAction === 'dismiss') void client.dismiss();
  });
  document.addEventListener('personal-assistant:status', event => {
    const operations = event.detail?.personalAssistant?.availability?.operations;
    state.capable = Boolean(operations && operations.available);
    render();
  });
  client.subscribe(render);

  if (typeof window.BroadcastChannel === 'function') {
    try {
      client.guardTab(new window.BroadcastChannel(TAB_CHANNEL));
    } catch (_) {
      // Without a channel a copied tab is still refused by the server's lease.
    }
  }
  render();
  void client.restore();

  return {
    /** What a turn sends about this tab. */
    askRef: () => client.askRef(),
    /** A reply arrived: follow the operation its stored turn requested, if any. */
    handleReply(data) {
      if (!data) return;
      if (data.operation_capability) state.capable = Boolean(data.operation_capability.available);
      state.error = data.operation ? '' : String(data.operation_error || '');
      if (data.operation) {
        void client.adopt(data.operation);
        return;
      }
      render();
    },
    client
  };
}

if (typeof window !== 'undefined' && typeof document !== 'undefined') {
  const start = () => {
    const api = mount();
    if (api) window.PersonalAssistantOperations = api;
  };
  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', start, { once: true });
  } else {
    start();
  }
}
