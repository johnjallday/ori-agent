/**
 * task-presentation.js
 *
 * Single, pure, fixture-testable resolver that maps a raw workspace task
 * (plus its human-loop / retry context) to one canonical presentation used by
 * EVERY workspace task surface: the Operations Map rows, the task drawer rows
 * and preview, the execution tray, the workspace-detail task surfaces, and the
 * dedicated task page.
 *
 * Design (see PRD "Workspace Task Execution UX", section C, FR29–FR45):
 *  - Same raw task + human-loop data must resolve to the same label, tone,
 *    count categories, sort priority, and actions on every surface (FR39).
 *  - Unknown raw states get a safe neutral presentation that exposes the raw
 *    value and is NEVER silently presented as `Pending` (FR38).
 *  - `timeout` stays distinct from `failed` — this resolver reads raw `status`
 *    and does not inherit the legacy `getTaskExecutionState` flattening
 *    (workspace-detail.js:49-53) that collapsed timeout → failed.
 *
 * This module is intentionally free of DOM/window/network access so it can be
 * exercised directly from fixtures. It attaches to `window.TaskPresentation`
 * for browser callers when a window exists, and exports named functions for
 * tests.
 */

// Canonical presentation state keys. These are surface-agnostic; each surface
// maps the `primaryAction.id` to its own handler + copy, but the label/tone
// defaults below keep wording consistent when a surface has no override.
export const PRESENTATION_STATE = Object.freeze({
  NEEDS_ASSIGNMENT: 'needs_assignment',
  READY: 'ready',
  RUNNING: 'running',
  NEEDS_INPUT: 'needs_input',
  BLOCKED: 'blocked',
  FAILED: 'failed',
  TIMED_OUT: 'timed_out',
  COMPLETED: 'completed',
  CANCELLED: 'cancelled',
  SKIPPED: 'skipped',
  UNKNOWN: 'unknown'
});

// Drawer filter keys (FR16).
export const FILTER = Object.freeze({
  ACTIONABLE: 'actionable',
  ACTIVE: 'active',
  NEEDS_ATTENTION: 'needs_attention',
  COMPLETED: 'completed',
  ALL: 'all'
});

// Per-state static metadata: label, tone, which count buckets it belongs to,
// sort priority (lower sorts earlier — FR24), and default primary/secondary
// actions. `retry`/`rerun`/`cancel` availability is layered on at resolve time
// from the task's own data (see resolveTaskPresentation).
const STATE_META = Object.freeze({
  [PRESENTATION_STATE.NEEDS_INPUT]: {
    label: 'Needs Input',
    tone: 'attention',
    counts: [FILTER.ACTIONABLE, FILTER.ACTIVE, FILTER.NEEDS_ATTENTION],
    sortPriority: 0,
    primaryAction: { id: 'respond', label: 'Respond' }
  },
  [PRESENTATION_STATE.BLOCKED]: {
    label: 'Blocked',
    tone: 'attention',
    counts: [FILTER.ACTIONABLE, FILTER.ACTIVE, FILTER.NEEDS_ATTENTION],
    sortPriority: 1,
    primaryAction: { id: 'inspect', label: 'Inspect' }
  },
  [PRESENTATION_STATE.NEEDS_ASSIGNMENT]: {
    label: 'Needs Assignment',
    tone: 'attention',
    counts: [FILTER.ACTIONABLE, FILTER.NEEDS_ATTENTION],
    sortPriority: 2,
    primaryAction: { id: 'assign_start', label: 'Assign & Start' }
  },
  [PRESENTATION_STATE.RUNNING]: {
    label: 'Running',
    tone: 'active',
    counts: [FILTER.ACTIONABLE, FILTER.ACTIVE],
    sortPriority: 3,
    primaryAction: { id: 'track', label: 'Track' }
  },
  [PRESENTATION_STATE.FAILED]: {
    label: 'Failed',
    tone: 'danger',
    counts: [FILTER.ACTIONABLE, FILTER.NEEDS_ATTENTION],
    sortPriority: 4,
    primaryAction: { id: 'inspect', label: 'Inspect' }
  },
  [PRESENTATION_STATE.TIMED_OUT]: {
    label: 'Timed Out',
    tone: 'danger',
    counts: [FILTER.ACTIONABLE, FILTER.NEEDS_ATTENTION],
    sortPriority: 4,
    primaryAction: { id: 'inspect', label: 'Inspect' }
  },
  [PRESENTATION_STATE.READY]: {
    label: 'Ready',
    tone: 'info',
    counts: [FILTER.ACTIONABLE, FILTER.ACTIVE],
    sortPriority: 5,
    primaryAction: { id: 'start', label: 'Start' }
  },
  [PRESENTATION_STATE.COMPLETED]: {
    label: 'Completed',
    tone: 'success',
    counts: [FILTER.COMPLETED],
    sortPriority: 6,
    primaryAction: { id: 'view_result', label: 'View Result' }
  },
  [PRESENTATION_STATE.CANCELLED]: {
    label: 'Cancelled',
    tone: 'neutral',
    counts: [],
    sortPriority: 7,
    primaryAction: { id: 'inspect', label: 'Inspect' }
  },
  [PRESENTATION_STATE.SKIPPED]: {
    label: 'Skipped',
    tone: 'neutral',
    counts: [],
    sortPriority: 7,
    primaryAction: { id: 'inspect', label: 'Inspect' }
  },
  [PRESENTATION_STATE.UNKNOWN]: {
    label: 'Unknown',
    tone: 'neutral',
    counts: [],
    sortPriority: 8,
    primaryAction: { id: 'inspect', label: 'Inspect' }
  }
});

function lc(value) {
  return String(value == null ? '' : value)
    .trim()
    .toLowerCase();
}

// Raw statuses treated as "ready-ish": not yet running, resolves to Ready or
// Needs Assignment depending on whether a runnable assignee exists.
const READYISH_STATUSES = new Set([
  'pending',
  'assigned',
  'ready',
  'queued',
  'todo',
  'not_started',
  ''
]);
const COMPLETED_STATUSES = new Set(['completed', 'success', 'done']);
const FAILED_STATUSES = new Set(['failed', 'error']);

/** Human-loop sub-state, e.g. `blocked` or `waiting_for_choice` (FR33-34). */
export function taskHumanLoopState(task) {
  return lc(task && task.context && task.context.human_loop && task.context.human_loop.state);
}

/**
 * The structured repair for a repair-gated block, or null.
 *
 * A task blocked on a missing connection cannot be helped by retrying — the
 * retry reproduces the block and costs another model call. When the server
 * attaches a repair action, that action is what the UI must offer; Retry only
 * becomes the primary action once the precondition is actually fixed.
 *
 * @returns {{code:string,label:string,url:string,reason:string,message:string}|null}
 */
export function taskBlockedRepair(task) {
  const loop = task && task.context && task.context.human_loop;
  const repair = loop && loop.repair;
  if (!repair || !String(repair.label || '').trim()) return null;
  return {
    code: String(repair.code || '').trim(),
    label: String(repair.label).trim(),
    url: String(repair.url || '').trim(),
    reason: String(loop.reason_code || '').trim(),
    message: String(loop.reason || loop.question || '').trim()
  };
}

/**
 * True when a task is blocked on something a retry cannot fix. Callers use it
 * to disable or hide Retry rather than offering an action that is guaranteed to
 * fail again.
 */
export function isRepairGatedTask(task) {
  return taskBlockedRepair(task) !== null;
}

/** Assignee display string, mirroring the existing `to || agent_name || assigned_to` order. */
export function taskAssignee(task) {
  if (!task) return '';
  return String(task.to || task.agent_name || task.assigned_to || '').trim();
}

/**
 * "Latest meaningful activity" timestamp (ms) — the most recent status change
 * or non-heartbeat run event we can see, approximated by `updated_at` and
 * falling back to `created_at` (PRD review-gap #6 definition; matches the
 * existing `taskUpdatedTime` helper). Returns 0 when unknown so it sorts last.
 */
export function taskLatestActivityAt(task) {
  const raw = (task && (task.updated_at || task.created_at)) || '';
  const ms = Date.parse(raw);
  return Number.isFinite(ms) ? ms : 0;
}

/*
 * Raw legacy predicates — exact single-status / human-loop checks, extracted
 * verbatim from the Operations Map so every surface shares one definition
 * instead of maintaining parallel copies (FR29). Unlike resolveTaskState, these
 * add NO precedence: e.g. a blocked task that is also awaiting input reports
 * true from both isBlockedTask and isNeedsInputTask, exactly as the Map did.
 */

/** status is `blocked`, or human-loop is blocked. */
export function isBlockedTask(task) {
  const status = lc(task && task.status);
  return status === 'blocked' || taskHumanLoopState(task) === 'blocked';
}

/** status/human-loop is waiting for a choice, or a step is waiting for input (FR33). */
export function isNeedsInputTask(task) {
  const status = lc(task && task.status);
  return (
    status === 'waiting_for_choice' ||
    taskHumanLoopState(task) === 'waiting_for_choice' ||
    Boolean(task && task.context && task.context.execution_step_waiting === true)
  );
}

/** status is `in_progress`. */
export function isWorkingTask(task) {
  return lc(task && task.status) === 'in_progress';
}

/** status is `pending`. */
export function isQueuedTask(task) {
  return lc(task && task.status) === 'pending';
}

/** Whether the task is awaiting an answerable input request (FR33). */
function hasAnswerableInput(task, opts) {
  if (opts && typeof opts.hasAnswerableInput === 'boolean') return opts.hasAnswerableInput;
  return isNeedsInputTask(task);
}

/** Whether retry is supported for a failed/timed-out task (FR35). */
function retrySupported(task, opts) {
  if (opts && typeof opts.retrySupported === 'boolean') return opts.retrySupported;
  const retry = task && task.context && task.context.execution_retry;
  if (retry && Number(retry.max_attempts) > 0) {
    return Number(retry.attempts_used || 0) < Number(retry.max_attempts);
  }
  // No retry metadata → assume retry is available; the server is authoritative
  // and will reject an unsupported retry, which the caller surfaces (FR44).
  return true;
}

/**
 * Resolve the canonical presentation state key for a task. Ordering matters:
 * terminal outcomes win first, then intervention states (needs-input before
 * blocked, per FR34), then running, then ready-ish, then a safe unknown.
 */
export function resolveTaskState(task, opts) {
  const status = lc(task && task.status);
  const humanLoop = taskHumanLoopState(task);

  if (COMPLETED_STATUSES.has(status)) return PRESENTATION_STATE.COMPLETED;
  if (status === 'cancelled') return PRESENTATION_STATE.CANCELLED;
  if (status === 'skipped') return PRESENTATION_STATE.SKIPPED;
  if (status === 'timeout') return PRESENTATION_STATE.TIMED_OUT;
  if (FAILED_STATUSES.has(status)) return PRESENTATION_STATE.FAILED;

  // Needs-input outranks blocked: a blocked task WITH an answerable input
  // request is Needs Input, not Blocked (FR33-34).
  if (hasAnswerableInput(task, opts)) return PRESENTATION_STATE.NEEDS_INPUT;
  if (status === 'blocked' || humanLoop === 'blocked') return PRESENTATION_STATE.BLOCKED;

  if (status === 'in_progress') return PRESENTATION_STATE.RUNNING;

  if (READYISH_STATUSES.has(status)) {
    const runnable =
      opts && typeof opts.hasRunnableAssignee === 'boolean'
        ? opts.hasRunnableAssignee
        : Boolean(taskAssignee(task));
    return runnable ? PRESENTATION_STATE.READY : PRESENTATION_STATE.NEEDS_ASSIGNMENT;
  }

  return PRESENTATION_STATE.UNKNOWN;
}

/**
 * Resolve the full canonical presentation for a task.
 *
 * @param {object} task - raw task payload
 * @param {object} [opts] - optional overrides:
 *   hasRunnableAssignee, hasAnswerableInput, retrySupported (booleans),
 *   cancelSupported (default true), rerunSupported (default true).
 * @returns {{
 *   state: string, label: string, tone: string, rawState: string,
 *   isUnknown: boolean, countCategories: string[], sortPriority: number,
 *   primaryAction: {id:string,label:string}|null,
 *   secondaryActions: Array<{id:string,label:string,confirm?:boolean}>,
 *   assignee: string, latestActivityAt: number
 * }}
 */
export function resolveTaskPresentation(task, opts) {
  const options = opts || {};
  const state = resolveTaskState(task, options);
  const meta = STATE_META[state] || STATE_META[PRESENTATION_STATE.UNKNOWN];
  const rawState = lc(task && task.status);

  let primaryAction = meta.primaryAction ? { ...meta.primaryAction } : null;
  const secondaryActions = [];

  // A repair-gated block outranks every other action: the task is stopped on a
  // missing connection, so the only useful button is the one that fixes it.
  // Retry is deliberately NOT offered here — it would reproduce the same block.
  const repair = taskBlockedRepair(task);
  if (repair) {
    return {
      state,
      label: meta.label,
      tone: meta.tone,
      rawState,
      isUnknown: state === PRESENTATION_STATE.UNKNOWN,
      countCategories: [FILTER.ALL, ...meta.counts],
      sortPriority: meta.sortPriority,
      primaryAction: { id: 'repair', label: repair.label, url: repair.url, code: repair.code },
      secondaryActions: [],
      repair,
      assignee: taskAssignee(task),
      latestActivityAt: taskLatestActivityAt(task)
    };
  }

  if (state === PRESENTATION_STATE.FAILED || state === PRESENTATION_STATE.TIMED_OUT) {
    if (retrySupported(task, options)) {
      primaryAction = { id: 'retry', label: 'Inspect & Retry' };
    }
  } else if (state === PRESENTATION_STATE.RUNNING) {
    const cancelSupported = options.cancelSupported !== false;
    if (cancelSupported) {
      secondaryActions.push({ id: 'cancel', label: 'Cancel', confirm: true });
    }
  } else if (state === PRESENTATION_STATE.COMPLETED) {
    const rerunSupported = options.rerunSupported !== false;
    if (rerunSupported) {
      secondaryActions.push({ id: 'rerun', label: 'Re-run' });
    }
  }

  return {
    state,
    label: meta.label,
    tone: meta.tone,
    rawState,
    isUnknown: state === PRESENTATION_STATE.UNKNOWN,
    countCategories: [FILTER.ALL, ...meta.counts],
    sortPriority: meta.sortPriority,
    primaryAction,
    secondaryActions,
    repair: null,
    assignee: taskAssignee(task),
    latestActivityAt: taskLatestActivityAt(task)
  };
}

/** True when a task belongs in the given drawer filter (FR16-19). */
export function taskMatchesFilter(task, filter, opts) {
  if (filter === FILTER.ALL) return true;
  const pres = resolveTaskPresentation(task, opts);
  return pres.countCategories.indexOf(filter) !== -1;
}

/**
 * Count tasks per filter bucket using the same categories as the rows they
 * summarize, so a badge can never say "Open Tasks" while excluding visible
 * attention-required tasks (FR45).
 */
export function resolveTaskCounts(tasks, opts) {
  const counts = {
    [FILTER.ACTIONABLE]: 0,
    [FILTER.ACTIVE]: 0,
    [FILTER.NEEDS_ATTENTION]: 0,
    [FILTER.COMPLETED]: 0,
    [FILTER.ALL]: 0
  };
  (Array.isArray(tasks) ? tasks : []).forEach(task => {
    const pres = resolveTaskPresentation(task, opts);
    pres.countCategories.forEach(cat => {
      if (counts[cat] != null) counts[cat] += 1;
    });
  });
  return counts;
}

/**
 * Deterministic drawer ordering (FR24): intervention states first, then
 * running, then failed/timed-out, then ready, then terminal history. Ties
 * break by most-recently-updated, then ascending task ID. Returns a new array
 * (does not mutate the input).
 */
export function sortTasksForDrawer(tasks, opts) {
  const list = (Array.isArray(tasks) ? tasks : []).slice();
  return list.sort((a, b) => {
    const pa = resolveTaskPresentation(a, opts);
    const pb = resolveTaskPresentation(b, opts);
    if (pa.sortPriority !== pb.sortPriority) return pa.sortPriority - pb.sortPriority;
    if (pa.latestActivityAt !== pb.latestActivityAt)
      return pb.latestActivityAt - pa.latestActivityAt;
    return String((a && a.id) || '').localeCompare(String((b && b.id) || ''));
  });
}

/**
 * Stable short identifier derived from the task ID (FR15) — never the array
 * index. Reordering/filtering the list must not renumber a task. Produces a
 * compact, uppercase suffix of the ID for display next to the title.
 */
export function taskShortId(task) {
  const id = String((task && task.id) || '').trim();
  if (!id) return '';
  const tail = id
    .replace(/[^A-Za-z0-9]/g, '')
    .slice(-4)
    .toUpperCase();
  return tail ? `#${tail}` : '';
}

export const TaskPresentation = {
  PRESENTATION_STATE,
  FILTER,
  resolveTaskState,
  resolveTaskPresentation,
  taskMatchesFilter,
  resolveTaskCounts,
  sortTasksForDrawer,
  taskShortId,
  taskAssignee,
  taskHumanLoopState,
  taskLatestActivityAt,
  isBlockedTask,
  isNeedsInputTask,
  isWorkingTask,
  isQueuedTask
};

if (typeof window !== 'undefined') {
  window.TaskPresentation = TaskPresentation;
}
