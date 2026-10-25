/** Pure workspace guidance. Operation permissions and dispatch stay with their owners. */
import {
  FILTER,
  resolveTaskPresentation,
  sortTasksForDrawer,
  taskMatchesFilter
} from './task-presentation.js';

/** @typedef {'loading'|'ready'|'unavailable'|'not_applicable'} SourceState */
/**
 * @typedef {object} NextAction
 * @property {string} kind
 * @property {string} title
 * @property {string} description
 * @property {string} tone
 * @property {boolean} isLoading
 * @property {{owner:'task'|'console'|'workspace', kind:string, id:string, key:string, label:string}|null} action
 * @property {string[]} qualifications
 * @property {string[]} ongoing
 */

function completionAt(value) {
  if (!value || String(value).startsWith('0001-')) return null;
  const at = Date.parse(value);
  return Number.isFinite(at) ? at : null;
}

function taskTitle(task) {
  return String(task.description || task.name || task.title || 'Untitled task');
}

function resultOrder(a, b) {
  // Missing completion time stays unknown; it is never approximated by activity.
  if (a.at !== b.at) {
    if (a.at === null) return 1;
    if (b.at === null) return -1;
    return b.at - a.at;
  }
  return a.source.localeCompare(b.source) || a.id.localeCompare(b.id);
}

/**
 * @param {{workspaceId:string, tasks:{state:SourceState,items?:object[]}, console?:object|null}} input
 * @returns {NextAction}
 */
export function resolveWorkspaceNextAction(input) {
  const workspaceId = String(input.workspaceId || '');
  const taskState = input.tasks?.state || 'loading';
  const tasks = taskState === 'ready' ? sortTasksForDrawer(input.tasks?.items) : [];
  const attention = tasks.filter(task => taskMatchesFilter(task, FILTER.NEEDS_ATTENTION));
  const current = tasks.find(task => resolveTaskPresentation(task).state === 'running');
  const actionable = tasks.find(task => taskMatchesFilter(task, FILTER.ACTIONABLE));
  const unknown = tasks.filter(task => resolveTaskPresentation(task).isUnknown);
  const taskResults = tasks
    .filter(
      task => taskMatchesFilter(task, FILTER.COMPLETED) && (task.result || task.structured_result)
    )
    .map(task => ({
      source: 'task',
      id: String(task.id || ''),
      at: completionAt(task.completed_at),
      task
    }))
    .sort(resultOrder);
  const result = taskResults[0]?.task;
  const nextTask = attention[0] || current || actionable || unknown[0] || result;
  const taskWork = {
    state: taskState === 'ready' ? (nextTask ? 'work' : 'idle') : taskState,
    next: nextTask,
    current,
    result,
    attention: attention.length,
    unknown: unknown.length
  };
  const janitor = input.console?.workspaceId === workspaceId ? input.console : null;
  const statusState = janitor?.status?.state || 'loading';
  const known = statusState === 'ready' && janitor?.applies === true;
  const qualifications = [];
  const ongoing = [];
  const qualify = (name, source) => {
    if (!source || ['ready', 'not_applicable'].includes(source.state)) return;
    qualifications.push(
      `${name} ${name === 'Tasks' ? 'are' : 'is'} ${source.state === 'unavailable' ? 'unavailable' : 'loading'}.${source.stale ? ' Last known information may be out of date.' : ''}`
    );
  };
  qualify('Tasks', { state: taskState });
  qualify('File review status', janitor?.status || { state: 'loading' });
  if (known) {
    qualify('File review', janitor.batch);
    qualify('Filing history', janitor.history);
  }
  if (current) ongoing.push(`Running: ${taskTitle(current)}`);
  if (janitor?.scanning) ongoing.push('Scanning for files. Nothing moves without approval.');
  if (known && janitor.scanState === 'failed')
    ongoing.push('The last scan failed. Open File Janitor to retry explicitly.');
  if (known && janitor.history?.state === 'ready') {
    const counts = janitor.latestReceipt?.counts;
    const problems = (counts?.failed || 0) + (counts?.stale || 0);
    if (problems)
      ongoing.push(
        `Last filing: ${counts.applied || 0} applied, ${problems} not applied. See History in File Janitor.`
      );
  }
  const isLoading = [
    taskState,
    statusState,
    ...(known ? [janitor.batch?.state, janitor.history?.state] : [])
  ].includes('loading');
  const finish = descriptor => ({ ...descriptor, isLoading, qualifications, ongoing, taskWork });
  const taskDescriptor = task => {
    const presentation = resolveTaskPresentation(task);
    const action = presentation.primaryAction;
    return finish({
      kind: 'task',
      title: taskTitle(task),
      description: presentation.label,
      tone: presentation.tone,
      action:
        action && task.id
          ? {
              owner: 'task',
              kind: action.id,
              id: String(task.id),
              key: `task:${action.id}:${task.id}`,
              label: action.label
            }
          : null
    });
  };
  const consoleDescriptor = (kind, title, description, label, tone = 'neutral') =>
    finish({
      kind,
      title,
      description,
      tone,
      action: {
        owner: 'console',
        kind,
        id: kind === 'history' ? janitor?.latestReceipt?.id || '' : '',
        label,
        key: `console:${workspaceId}:${janitor?.generation || 0}:${janitor?.rootId || ''}:${janitor?.batchId || ''}:${kind}:${kind === 'history' ? janitor?.latestReceipt?.id || '' : ''}`
      }
    });
  if (attention.length) return taskDescriptor(attention[0]);
  if (known) {
    if (janitor.readiness === 'needs_attention')
      return consoleDescriptor(
        'repair',
        'File Janitor needs attention',
        'Repair folder access or setup before continuing.',
        'Repair setup',
        'attention'
      );
    if (!janitor.configured || janitor.readiness === 'setup_required')
      return consoleDescriptor(
        'setup',
        'Choose the folder to tidy',
        'Finish setup in File Janitor. Opening setup starts nothing.',
        'Set up File Janitor'
      );
    if (janitor.consentRequired)
      return consoleDescriptor(
        'consent',
        'Review content consent',
        'File Janitor needs your decision before using document extracts.',
        'Review consent',
        'attention'
      );
    if (janitor.batch?.state === 'ready' && janitor.pendingCount > 0)
      return consoleDescriptor(
        'review',
        `${janitor.pendingCount} file${janitor.pendingCount === 1 ? '' : 's'} ready for review`,
        'Review the proposals and choose what happens. Nothing moves on open.',
        'Review files',
        'attention'
      );
  }
  if (current) return taskDescriptor(current);
  if (known && janitor.scanning)
    return consoleDescriptor(
      'review',
      'Scanning for files',
      'The scan proposes a review batch. It does not move or remove files.',
      'Open File Janitor'
    );
  if (actionable) return taskDescriptor(actionable);
  if (unknown.length) return taskDescriptor(unknown[0]);
  const results = [...taskResults];
  if (known && janitor.history?.state === 'ready' && janitor.latestReceipt) {
    const receipt = janitor.latestReceipt;
    const at = completionAt(receipt.completedAt);
    if (at !== null) results.push({ source: 'console', id: receipt.id, at, receipt });
  }
  results.sort(resultOrder);
  if (results[0]?.task) return taskDescriptor(results[0].task);
  if (results[0]?.receipt) {
    const receipt = results[0].receipt;
    const partial = (receipt.counts?.failed || 0) + (receipt.counts?.stale || 0) > 0;
    return consoleDescriptor(
      'history',
      receipt.result === 'applied' && !partial
        ? 'Filing result recorded'
        : 'Some files could not be filed',
      receipt.result === 'applied' && !partial
        ? `See the recorded result.${receipt.undoable ? ' Undo is available in History.' : ''}`
        : 'Check History for the recorded failure and any files still needing review.',
      'View filing history',
      receipt.result === 'applied' && !partial ? 'success' : 'attention'
    );
  }
  if (qualifications.length) {
    const failed =
      taskState === 'unavailable' ||
      statusState === 'unavailable' ||
      (known && [janitor.batch?.state, janitor.history?.state].includes('unavailable'));
    return finish({
      kind: failed ? 'unavailable' : 'loading',
      title: failed ? 'Some workspace information is unavailable' : 'Checking workspace work',
      description: 'There may still be work waiting. Other available actions remain usable.',
      tone: 'neutral',
      action: failed
        ? {
            owner: 'workspace',
            kind: 'retry',
            id: '',
            key: 'retry',
            label: 'Retry workspace status'
          }
        : null
    });
  }
  if (known) {
    const copy =
      janitor.emptyState === 'scan_empty' || janitor.emptyState === 'no_proposals'
        ? 'The last scan found no eligible files. Scan again only when you choose.'
        : janitor.mode === 'manual'
          ? 'Manual scans only. Start a scan explicitly in File Janitor.'
          : janitor.mode === 'paused'
            ? 'Automatic scanning is paused. You can still scan on demand.'
            : 'Watching for arrivals. Nothing moves without your approval.';
    return consoleDescriptor(
      'review',
      janitor.mode === 'manual'
        ? 'Ready when you are'
        : janitor.mode === 'paused'
          ? 'File Janitor is paused'
          : 'Watching for files',
      copy,
      'Open File Janitor'
    );
  }
  return finish({
    kind: 'idle',
    title: 'No open work',
    description: 'Give your team a task to start something new.',
    tone: 'neutral',
    action: { owner: 'workspace', kind: 'create', id: '', key: 'create', label: 'Give Task' }
  });
}
