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
  operationTaskURL,
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
  refused: 'This page could not confirm the step.',
  conversation_changed: 'Paused: you moved to another conversation.',
  stood_down: 'Paused.'
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
 * @param {string} [state.note] - a local remark about what the user just did here
 * @param {boolean} [state.onWorkspace] - whether this page is the operation's workspace
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
    } else if (state.note) {
      view.text = String(state.note);
    }
    return view;
  }

  const shown =
    snapshot.kind === 'task' && snapshot.draft
      ? taskView(view, state)
      : navigationView(view, state);
  if (state.note) {
    // A short, local remark about what the user just did here. It replaces
    // the sentence, never the choices beside it.
    shown.text = String(state.note);
    shown.tone = 'quiet';
  }
  return shown;
}

/** What the drawer shows for opening a workspace and selecting an agent. */
function navigationView(view, state) {
  const snapshot = state.snapshot;
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

function shorten(text, limit) {
  const characters = Array.from(String(text || '').trim());
  return characters.length <= limit
    ? characters.join('')
    : characters.slice(0, limit).join('') + '…';
}

/**
 * What the drawer shows for a prepared task draft. A draft is never called a
 * task: until the user saves the form, nothing exists but the proposal.
 */
function taskView(view, state) {
  const snapshot = state.snapshot;
  // Once the reviewed save created the task, the draft is history: what is
  // shown comes from the bound task and the operation's own receipts.
  if (snapshot.task && snapshot.task.task_id) return committedTaskView(view, state);
  const { workspace, slug } = target(snapshot);
  const agent = String(snapshot.draft.agent_name || 'the agent');
  const title = shorten(snapshot.draft.title, 60);
  const open = command(snapshot, 'open_workspace');
  const form = command(snapshot, 'open_task_draft');
  const failed = (snapshot.commands || []).find(item => item.status === 'unavailable');
  const opened = Boolean(open && open.status === 'acknowledged');
  const shown = Boolean(form && form.status === 'acknowledged');
  const unsaved = 'Nothing is created until you save it.';
  const dismiss = { id: 'dismiss', label: 'Dismiss' };
  const manual = () => {
    const href = operationWorkspaceURL(slug);
    return href ? { href, label: `Open ${workspace} yourself` } : null;
  };

  switch (state.phase) {
    case PHASE.ENDED:
      if (snapshot.status === 'expired') {
        view.text = 'That draft ran out of time. Ask again to prepare it.';
      } else if (snapshot.status === 'invalidated') {
        view.text =
          'That draft is no longer valid because your assistant, the conversation, the workspace or its team changed.';
      }
      if (view.text) {
        view.tone = 'attention';
        view.actions = [dismiss];
      }
      return view;
    case PHASE.RUNNING:
    case PHASE.NAVIGATING:
      view.text = opened ? `Opened ${workspace}. Opening the task form…` : `Opening ${workspace}…`;
      view.tone = 'working';
      return view;
    case PHASE.WAITING:
      view.text = `Draft ready for ${agent}: “${title}”. ${unsaved}`;
      view.actions = [{ id: 'resume', label: 'Open draft', primary: true }, dismiss];
      return view;
    case PHASE.PAUSED:
      view.tone = 'attention';
      if (state.pauseReason === 'unsaved_work') {
        // The user's edits win. Replacing them is their choice, said plainly.
        view.text = `This page has unsaved changes. Ori's draft for ${agent} is waiting.`;
        view.actions = [
          { id: 'resume', label: "Replace with Ori's draft", primary: true },
          dismiss
        ];
        return view;
      }
      view.text = PAUSE_TEXT[state.pauseReason] || 'Paused.';
      view.actions = [{ id: 'resume', label: 'Open draft', primary: true }, dismiss];
      view.link = manual();
      return view;
    case PHASE.SETTLED:
      if (failed) {
        const reason = String(failed.reason || '').trim();
        view.text = `Ori could not ${failed.action === 'open_workspace' ? `open ${workspace}` : 'open the task form'}.${reason ? ' ' + reason : ''}`;
        view.tone = 'attention';
        view.actions = [dismiss];
        view.link = opened ? null : manual();
      } else if (shown && state.restored && state.onWorkspace && !state.formOpen) {
        // A reload closed the form. What was typed there cannot be restored,
        // and it is not replaced by a silent re-open either.
        view.text = `Draft for ${agent}: “${title}”. This page was reloaded, so anything typed into the form is gone. Open the draft again to start from Ori's version. ${unsaved}`;
        view.tone = 'attention';
        view.actions = [{ id: 'open_draft', label: 'Open draft again', primary: true }, dismiss];
      } else if (shown) {
        // True whether the form is still open or the user has closed it.
        view.text = `Draft for ${agent}: “${title}”. Review it in the task form. ${unsaved}`;
        view.tone = 'done';
        // The form can be reopened from the same draft on its own page.
        view.actions = state.onWorkspace
          ? [{ id: 'open_draft', label: 'Open draft again' }, dismiss]
          : [dismiss];
        view.link = state.onWorkspace ? null : manual();
      }
      return view;
    default:
      return view;
  }
}

function lastReceipt(snapshot, kinds) {
  const receipts = (snapshot && snapshot.receipts) || [];
  for (let i = receipts.length - 1; i >= 0; i -= 1) {
    if (kinds.includes(receipts[i].kind)) return receipts[i];
  }
  return null;
}

/** A sentence, ending in a full stop, or '' for nothing. */
function sentence(text) {
  const value = String(text || '').trim();
  if (!value) return '';
  return /[.!?…]$/.test(value) ? value : value + '.';
}

/**
 * What the drawer shows once the operation has a task. Three kinds of fact are
 * kept apart: the server's receipt that the task was created, the server's
 * answer to Start (accepted or refused), and the task's own state, which alone
 * says that work is happening, waiting, failed or finished.
 */
function committedTaskView(view, state) {
  const snapshot = state.snapshot;
  const { workspace, slug } = target(snapshot);
  const context = snapshot.context || {};
  const agent = String(context.agent_name || snapshot.draft.agent_name || 'the agent');
  const title = shorten(state.taskTitle || snapshot.draft.title, 60);
  // Stopping here is not cancelling there: cancelling work is the task's own
  // control, and this button says so.
  const stop = {
    id: 'dismiss',
    label: 'Stop following',
    hint: 'Stops updates here. The task keeps going; cancel it from the task itself.'
  };
  const dismiss = { id: 'dismiss', label: 'Dismiss' };
  const taskHref = operationTaskURL(slug, snapshot.task.task_id);
  view.link = taskHref ? { href: taskHref, label: 'Open the task' } : null;

  if (state.phase === PHASE.ENDED) {
    // The request ended. The task is the workspace's own and is still there,
    // unless it is the task itself that is gone.
    const ended = lastReceipt(snapshot, ['continuation_ended']);
    if (ended && /no longer exists/.test(String(ended.reason || ''))) {
      view.text = 'The task was deleted, so there is nothing left to follow.';
      view.link = null;
    } else if (snapshot.status === 'expired') {
      view.text = `That request ran out of time. The task for ${agent} is still in ${workspace}.`;
    } else if (snapshot.status === 'invalidated') {
      view.text = `That request is no longer valid. The task for ${agent} is still in ${workspace}.`;
    }
    if (view.text) {
      view.tone = 'attention';
      view.actions = [dismiss];
    } else {
      view.link = null;
    }
    return view;
  }
  if (state.startUnconfirmed) {
    view.text =
      'Ori could not be reached, so it is not known whether the task started. Check before starting it again.';
    view.tone = 'attention';
    view.actions = [{ id: 'check', label: 'Check again', primary: true }, dismiss];
    return view;
  }

  switch (snapshot.status) {
    case 'reviewed': {
      if (state.starting) {
        view.text = `Asking Ori to start the task for ${agent}…`;
        view.tone = 'working';
        return view;
      }
      const last = lastReceipt(snapshot, ['start_refused', 'start_accepted', 'task_created']);
      if (last && last.kind === 'start_refused') {
        view.text = `The task for ${agent} was not started. ${sentence(last.reason)}`.trim();
        view.tone = 'attention';
        view.actions = [{ id: 'start', label: 'Try starting again', primary: true }, dismiss];
        return view;
      }
      view.text = `Task created for ${agent}: “${title}”. It has not started.`;
      view.tone = 'done';
      view.actions = [{ id: 'start', label: 'Start task', primary: true }, dismiss];
      return view;
    }
    case 'start_requested':
      // Accepted is not running: the task has not reported a run yet.
      view.text = `Start accepted. Waiting for ${agent} to begin…`;
      view.tone = 'working';
      view.actions = state.canShowTask
        ? [{ id: 'show_task', label: 'Show progress' }, stop]
        : [stop];
      return view;
    case 'unresolved': {
      const why = lastReceipt(snapshot, ['start_unresolved']);
      view.text =
        `Ori could not confirm that the task for ${agent} started. ${sentence(why && why.reason)} Nothing was started again.`.replace(
          /\s+/g,
          ' '
        );
      view.tone = 'attention';
      view.actions = [
        { id: 'check', label: 'Check again', primary: true },
        { id: 'start', label: 'Try starting again' },
        stop
      ];
      return view;
    }
    case 'started':
      return runningTaskView(view, state, { agent, title, stop });
    default:
      return view;
  }
}

/** The bound task's own state, in the task presentation's vocabulary. */
function runningTaskView(view, state, { agent, title, stop }) {
  const presentation = state.taskPresentation || null;
  const task = state.task || null;
  const outcome = (state.snapshot && state.snapshot.outcome) || null;
  const later = outcome ? Number(outcome.later_runs || 0) : 0;
  const dismiss = { id: 'dismiss', label: 'Dismiss' };
  const progress = state.canShowTask ? [{ id: 'show_task', label: 'Show progress' }] : [];
  view.actions = [...progress, stop];

  // First, what became of this request's own run when the task has moved
  // past it. A later run's state or result is never shown as this one's.
  if (outcome && outcome.run === 'interrupted') {
    view.text =
      later > 0
        ? `This request's run of “${title}” was interrupted before it finished. The task has run again since; that result belongs to the later run.`
        : `The run of “${title}” was interrupted before it finished, and nothing was started again. Run it from the task when you are ready.`;
    view.tone = 'attention';
    view.actions = [dismiss];
    return view;
  }
  if (outcome && later > 0) {
    if (outcome.run === 'succeeded' && outcome.result === 'history') {
      view.text = `${agent} finished “${title}”. The task has run again since, so what it shows now is a later run's result. This request's result is in the task's run history.`;
    } else if (outcome.run === 'succeeded') {
      view.text = `${agent} finished “${title}”. The task has run again since, and this request's own result is no longer kept.`;
    } else if (outcome.run === 'stopped') {
      view.text = `This request's run of “${title}” stopped and asked for input. It was continued from the task, and the result there is from that later attempt.`;
    } else if (outcome.run === 'unknown') {
      view.text = `“${title}” has run again since, and the task no longer keeps this request's own run.`;
    } else {
      view.text = `This request's run of “${title}” did not finish. The task has run again since.`;
    }
    view.tone = 'quiet';
    view.actions = [dismiss];
    return view;
  }
  if (state.taskPhase === 'reconnecting') {
    // Not hearing from the task is not the task finishing.
    const last =
      presentation && presentation.label ? ` It last reported: ${presentation.label}.` : '';
    view.text = `Reconnecting to “${title}”…${last}`;
    view.tone = 'quiet';
    return view;
  }

  const taskState = presentation ? String(presentation.state || '') : '';
  switch (taskState) {
    case 'running':
      view.text = `${agent} is working on “${title}”.`;
      view.tone = 'working';
      return view;
    case 'needs_input':
      view.text = `${agent} needs your input to continue “${title}”.`;
      view.tone = 'attention';
      return view;
    case 'blocked':
      view.text = `“${title}” is blocked. Open the task to see what it needs.`;
      view.tone = 'attention';
      return view;
    case 'failed':
    case 'timed_out': {
      const reason = sentence(shorten(task && task.error, 140));
      view.text =
        `“${title}” ${taskState === 'timed_out' ? 'timed out' : 'failed'}. ${reason}`.trim();
      view.tone = 'attention';
      view.actions = [stop];
      return view;
    }
    case 'completed':
      view.text = `${agent} finished “${title}”.`;
      view.tone = 'done';
      view.actions = [dismiss];
      if (outcome && outcome.result === 'current') {
        // The server confirmed the task's present result is this run's.
        view.actions = [{ id: 'open_result', label: 'Open result', primary: true }, dismiss];
      } else if (outcome && outcome.result === 'gone') {
        view.text += ' Its result is no longer kept on the task.';
        view.tone = 'quiet';
      }
      return view;
    case 'cancelled':
      view.text = `“${title}” was cancelled.`;
      view.tone = 'attention';
      view.actions = [dismiss];
      return view;
    case '':
      // The run was observed by the server; this tab has not read the task yet.
      view.text = `The task for ${agent} started.`;
      view.tone = 'working';
      return view;
    case 'ready':
    case 'needs_assignment':
      // Started, and now waiting again without a result: not finished.
      view.text = `“${title}” started and is waiting again. Open the task to see why.`;
      view.tone = 'attention';
      return view;
    default:
      view.text = `Ori cannot tell what state “${title}” is in. Open the task to check.`;
      view.tone = 'quiet';
      return view;
  }
}

const RECAP_SOURCE = Object.freeze({
  tab: 'Seen in this tab',
  ori: 'Recorded by Ori',
  task: 'From the task'
});

const OUTCOME_SENTENCE = Object.freeze({
  running: 'It is running.',
  waiting: 'It stopped and is waiting for you.',
  stopped: 'It stopped and asked for input.',
  succeeded: 'It finished.',
  failed: 'It failed.',
  cancelled: 'It was cancelled.',
  interrupted: 'It was interrupted before it finished.',
  unknown: 'The task no longer keeps this run.'
});

/**
 * A recap of one operation, from recorded facts only: what this tab reported
 * seeing, what the server recorded, and what the task itself says now. Each
 * line names its source, so a view step is never read as a write and an
 * accepted Start is never read as a finished task. It is a list to read:
 * opening, closing or reopening it sends nothing and changes nothing.
 *
 * @param {object} state - as for operationView
 * @returns {{label:string, items:Array<{source:string, sourceLabel:string, text:string}>}|null}
 */
export function operationRecap(state = {}) {
  const snapshot = state.snapshot || null;
  if (!snapshot) return null;
  const { workspace } = target(snapshot);
  const context = snapshot.context || {};
  const draft = snapshot.draft || {};
  const selected = String(context.agent_name || 'the agent');
  const drafted = String(draft.agent_name || selected);
  const items = [];
  const add = (source, text) =>
    items.push({ source, sourceLabel: RECAP_SOURCE[source], text: String(text).trim() });

  for (const receipt of snapshot.receipts || []) {
    const reason = sentence(receipt.reason);
    switch (receipt.kind) {
      case 'draft_prepared':
        add('ori', `A draft was prepared for ${drafted}.`);
        break;
      case 'view_opened':
        add('tab', `${workspace} was opened.`);
        break;
      case 'agent_selected':
        add('tab', `${selected} was selected.`);
        break;
      case 'draft_opened':
        add('tab', 'The draft was shown in the task form.');
        break;
      case 'target_unavailable':
        add('tab', `A step could not be shown here. ${reason}`);
        break;
      case 'task_created':
        add('ori', 'You saved the form, and the task was created.');
        break;
      case 'start_accepted':
        add('ori', 'You pressed Start, and it was accepted.');
        break;
      case 'start_refused':
        add('ori', `A Start was refused. ${reason}`);
        break;
      case 'start_unresolved':
        add('ori', `A Start could not be confirmed. ${reason}`);
        break;
      case 'run_established':
        add('task', 'The task reported that it had started.');
        break;
      case 'continuation_ended':
        add('ori', `This request ended${receipt.ref ? ` (${receipt.ref})` : ''}.`);
        break;
      default:
        break;
    }
  }
  const outcome = snapshot.outcome || null;
  if (outcome && OUTCOME_SENTENCE[outcome.run]) {
    const later = Number(outcome.later_runs || 0);
    const again =
      later > 0 ? ` The task has run ${later === 1 ? 'once' : `${later} times`} more since.` : '';
    add('task', `${OUTCOME_SENTENCE[outcome.run]}${again}`);
  }
  // One line is the status line again, not a recap.
  return items.length > 1 ? { label: 'Recap', items } : null;
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
  // How to take the mark away early, when the request is no longer current.
  return () => {
    window.clearInterval(follow);
    const element = live();
    if (element) element.classList.remove(HIGHLIGHT_CLASS);
  };
}

/**
 * This tab's storage. Where the browser withholds it, the tab still works for
 * the page it is on: it just follows nothing across a navigation.
 */
function tabStorage() {
  try {
    const storage = window.sessionStorage;
    storage.getItem('ori.personalAssistant.tab');
    return storage;
  } catch (_) {
    const memory = new Map();
    return {
      getItem: key => (memory.has(key) ? memory.get(key) : null),
      setItem: (key, value) => memory.set(key, String(value)),
      removeItem: key => memory.delete(key)
    };
  }
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

/**
 * The execution monitor for one workspace's task, as the operation client
 * uses it. On that workspace's own page it is the page's existing execution
 * controller, whose tray shows the live run; anywhere else it is a controller
 * of the same class that only reads the task. Either way the task's state
 * comes from the same code the task pages use. `pending` asks the client to
 * try again shortly: the page's controller, or the module, is still loading.
 */
function browserTaskMonitor() {
  let Controller = null;
  let loading = null;
  return (workspaceId, { final = false } = {}) => {
    if (!workspaceId) return null;
    const command = window.workspaceCommand;
    if (String(window.currentWorkspaceId || '') === workspaceId) {
      if (command && typeof command.ensureExecController === 'function') {
        return {
          controller: command.ensureExecController(),
          owned: false,
          show: taskId => command.trackAndShowTray(taskId)
        };
      }
      if (!final) return { pending: true };
    }
    if (!Controller) {
      // Loaded only when a task is first followed away from its page.
      loading =
        loading ||
        import('./workspace-execution-controller.js')
          .then(module => {
            Controller = module.WorkspaceExecutionController;
          })
          .catch(() => {
            loading = null;
          });
      return final ? null : { pending: true };
    }
    return {
      controller: new Controller({
        workspaceId,
        fetchTask: async id => {
          const response = await fetch('/api/orchestration/tasks?id=' + encodeURIComponent(id), {
            cache: 'no-store',
            headers: { Accept: 'application/json' }
          });
          return response.ok ? await response.json() : null;
        }
      }),
      owned: true,
      show: null
    };
  };
}

function mount() {
  const els = {
    root: document.getElementById('personalAssistantOperation'),
    watchRow: document.getElementById('personalAssistantWatchRow'),
    watch: document.getElementById('personalAssistantWatch'),
    hint: document.getElementById('personalAssistantWatchHint'),
    status: document.getElementById('personalAssistantOperationStatus'),
    actions: document.getElementById('personalAssistantOperationActions'),
    recap: document.getElementById('personalAssistantOperationRecap'),
    recapList: document.getElementById('personalAssistantOperationRecapList')
  };
  if (!els.root || !els.watch || !els.status || !els.actions) return null;

  const client = new OperationClient({
    request: apiRequest,
    storage: tabStorage(),
    targets: createOperationTargets(browserOperationEnvironment()),
    navigate: url => window.location.assign(url),
    taskMonitor: browserTaskMonitor()
  });
  // The drawer may have read the relationship before this module mounted.
  const known = window.PersonalAssistantPanel?._state?.personalAssistant?.availability?.operations;
  const state = {
    capable: Boolean(known && known.available),
    error: '',
    note: '',
    shownHighlight: '',
    clearHighlight: null,
    actionsKey: '',
    recapKey: '',
    // The conversation the operation in front of the user was asked in.
    conversation: ''
  };

  function render() {
    const snapshot = client.snapshot;
    const viewState = {
      snapshot,
      phase: client.phase,
      pauseReason: client.pauseReason,
      capable: state.capable,
      watch: client.watchPreference(),
      error: state.error,
      // The form the user saved created the task; this only says so.
      note:
        state.note ||
        (!snapshot && client.saved ? 'Task saved from the draft. It is in the workspace now.' : ''),
      onWorkspace: Boolean(
        snapshot &&
        snapshot.context &&
        String(window.currentWorkspaceId || '') === String(snapshot.context.workspace_id || '')
      ),
      // The bound task as its monitor reads it. Its title is the saved one,
      // which may differ from the draft the assistant prepared.
      task: client.task,
      taskPresentation: client.taskPresentation,
      taskTitle: String(
        (client.task && client.task.description) ||
          (client.saved && client.saved.task && client.saved.task.description) ||
          ''
      ),
      taskPhase: client.taskPhase,
      starting: client.starting,
      startUnconfirmed: client.startUnconfirmed,
      canShowTask: client.canShowTask(),
      // After a reload: whether the page was rebuilt from the server, and
      // whether the task form is showing.
      restored: client.restored,
      formOpen: Boolean(
        window.taskModalController &&
        typeof window.taskModalController.isModalOpen === 'function' &&
        window.taskModalController.isModalOpen()
      )
    };
    const view = operationView(viewState);
    // The Watch chip lives in the composer's chip row, so it costs the drawer
    // no height. Its hint is the chip's description and tooltip.
    if (els.watchRow) {
      els.watchRow.hidden = !view.watchVisible;
      els.watchRow.title = view.watchHint;
    }
    els.watch.checked = view.watchChecked;
    if (els.hint) els.hint.textContent = view.watchHint;
    // The status line is a polite live region: it is written only when it
    // changes, so the same sentence is not announced again on every read.
    if (els.status.textContent !== view.text) els.status.textContent = view.text;
    els.status.dataset.tone = view.tone;
    els.status.hidden = !view.text;

    // The choices are rebuilt only when they change. Progress updates arrive
    // every few seconds; a button the user is on must not vanish under them.
    const actionsKey = JSON.stringify([view.actions, view.link]);
    if (actionsKey !== state.actionsKey) {
      state.actionsKey = actionsKey;
      const hadFocus = els.actions.contains(document.activeElement);
      els.actions.replaceChildren();
      view.actions.forEach(action => {
        const button = document.createElement('button');
        button.type = 'button';
        button.className = 'pa-operation__action' + (action.primary ? ' is-primary' : '');
        button.dataset.operationAction = action.id;
        button.textContent = action.label;
        if (action.hint) button.title = action.hint;
        els.actions.appendChild(button);
      });
      if (view.link) {
        const link = document.createElement('a');
        link.className = 'pa-operation__link';
        link.href = view.link.href;
        link.textContent = view.link.label;
        els.actions.appendChild(link);
      }
      // Focus that was on a choice stays among the choices; focus that was
      // anywhere else is left exactly where it was.
      if (hadFocus && els.actions.firstElementChild) els.actions.firstElementChild.focus();
    }
    els.actions.hidden = !els.actions.childElementCount;
    // The status section exists only while there is something to say.
    els.root.hidden = !view.text;

    // The recap is a list the user opens. It is rebuilt only when its facts
    // change, and whether it is open is the user's and is left alone.
    if (els.recap && els.recapList) {
      const recap = view.text ? operationRecap(viewState) : null;
      const recapKey = JSON.stringify(recap);
      if (recapKey !== state.recapKey) {
        state.recapKey = recapKey;
        els.recapList.replaceChildren();
        (recap ? recap.items : []).forEach(item => {
          const row = document.createElement('li');
          const source = document.createElement('span');
          source.className = 'pa-operation__recap-source';
          source.dataset.source = item.source;
          // The colon is text, not styling: it is read aloud and copied.
          source.textContent = item.sourceLabel + ':';
          row.append(source, document.createTextNode(' ' + item.text));
          els.recapList.appendChild(row);
        });
      }
      els.recap.hidden = !recap;
      if (!recap) els.recap.open = false;
    }

    const highlight = client.highlight;
    if (highlight && highlight.commandId !== state.shownHighlight) {
      state.shownHighlight = highlight.commandId;
      if (state.clearHighlight) state.clearHighlight();
      state.clearHighlight = applyHighlight(highlight);
    } else if (!highlight && state.clearHighlight) {
      // The request was dismissed, paused or taken over: the mark goes now,
      // not when its timer would have ended.
      state.clearHighlight();
      state.clearHighlight = null;
      state.shownHighlight = '';
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
    state.note = '';
    if (button.dataset.operationAction === 'resume') void client.resume();
    if (button.dataset.operationAction === 'dismiss') void client.dismiss();
    // Start is the user's own click, here. Nothing else in this module, and no
    // reply from the assistant, calls it.
    if (button.dataset.operationAction === 'start') void client.start();
    if (button.dataset.operationAction === 'check') void client.check();
    if (button.dataset.operationAction === 'show_task') client.showTask();
    if (button.dataset.operationAction === 'open_result') {
      // The user asked to see it. The result view takes focus as its own
      // dialog does; nothing is reported and nothing is changed.
      if (client.openResult() === 'unavailable') {
        state.note = 'The result could not be opened here. Open the task to see it.';
        render();
      }
    }
    if (button.dataset.operationAction === 'open_draft') {
      void client.openDraft().then(outcome => {
        // The user's own edits are never replaced by reopening the proposal.
        if (outcome === 'kept') {
          state.note = 'The task form is open with your changes. Nothing was replaced.';
        } else if (outcome === 'unavailable') {
          state.note = 'The task form could not be opened here.';
        }
        render();
      });
    }
  });
  document.addEventListener('personal-assistant:status', event => {
    const operations = event.detail?.personalAssistant?.availability?.operations;
    state.capable = Boolean(operations && operations.available);
    render();
  });
  /**
   * The drawer turned to another conversation. An operation asked for in the
   * one the user left stops moving this tab. It is not cancelled, its task
   * keeps running, and Resume carries on if they want it.
   */
  function conversationChanged(id) {
    const next = String(id || '');
    if (!client.snapshot || !state.conversation || next === state.conversation) return;
    client.standDown('conversation_changed');
  }
  document.addEventListener('personal-assistant:conversation', event => {
    conversationChanged(event.detail && event.detail.id);
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
      state.note = '';
      if (data.operation) {
        client.saved = null;
        state.conversation = String((data.conversation && data.conversation.id) || '');
        void client.adopt(data.operation);
        return;
      }
      render();
    },
    conversationChanged,
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
