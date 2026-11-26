/** Pure workspace guidance. Operation permissions and dispatch stay with their owners. */
import {
  FILTER,
  resolveTaskPresentation,
  sortTasksForDrawer,
  taskMatchesFilter,
  resolveTicketStatePill,
  resolveRunSignal
} from './task-presentation.js';
import { commanderOneLine } from './commander-speech.js';

/**
 * folderAgentWaiting is the words of the moment the workspace's Commander waits
 * on the person (the question, a plan, a stopped step, the songs offer, a
 * suggested goal, …), or null. They come from commanderOneLine, the one source
 * the one-line card, the Commander card's status and the balloon's folded line
 * also use, so the strip cannot say something else.
 */
export function folderAgentWaiting(view, workspaceId) {
  if (!view || String(view.workspaceID || '') !== String(workspaceId || '')) return null;
  const words = commanderOneLine(view);
  return words?.waiting ? words : null;
}

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

export function updateSourceRows(saved) {
  if (!saved) return [];
  const rows = [];
  const when = value =>
    completionAt(value) === null ? 'Unavailable' : new Date(value).toLocaleString();
  const review = saved.review;
  if (review?.batch_id) {
    rows.push(['Filing source', 'Saved Filing review']);
    rows.push(['Batch', review.batch_id]);
    rows.push(['Batch completed', when(review.batch_completed_at)]);
    rows.push(['Review observed', when(review.observed_at)]);
  }
  for (const message of saved.welcome?.messages || []) {
    if (![1, 2].includes(message.revision)) continue;
    rows.push([`Message ${message.revision} saved`, when(message.delivered_at)]);
    rows.push([
      `Message ${message.revision} exposed`,
      message.seen_at ? when(message.seen_at) : 'Not yet exposed'
    ]);
  }
  return rows;
}

function taskTitle(task) {
  return String(task.description || task.name || task.title || 'Untitled ticket');
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
 * @param {{workspaceId:string, tasks:{state:SourceState,items?:object[]}, console?:object|null, folderAgent?:object|null}} input
 *   folderAgent is the folder agent's report view (folderAgentReportView) for this
 *   workspace, when it has one; without it the output is exactly what it was.
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
  const updateView = input.updates;
  const saved = updateView?.data?.workspace_id === workspaceId ? updateView.data : null;
  const review = saved?.review || { state: updateView?.state || 'loading' };
  const reviewReady =
    updateView?.state === 'ready' && !updateView.stale && review.state === 'ready' && !review.stale;
  const welcome = saved?.welcome;
  const message =
    updateView?.state === 'ready' &&
    !updateView.stale &&
    welcome?.state === 'ready' &&
    !welcome.stale &&
    welcome.session_id &&
    welcome.agent_name
      ? {
          owner: 'updates',
          kind: 'welcome',
          id: welcome.session_id,
          key: `welcome:${welcome.agent_instance_id}:${welcome.session_id}`,
          label: `Ask ${welcome.agent_name}`,
          name: welcome.agent_name,
          unread: welcome.unread_count > 0
        }
      : null;
  const qualifications = [];
  const ongoing = [];
  const qualify = (name, source) => {
    if (!source || ['ready', 'not_applicable', 'unconfigured'].includes(source.state)) return;
    qualifications.push(
      `${name} ${name === 'Tickets' ? 'are' : 'is'} ${source.state === 'unavailable' ? 'unavailable' : 'loading'}.${source.stale ? ' Last known information may be out of date.' : ''}`
    );
  };
  qualify('Tickets', { state: taskState });
  qualify('File review status', janitor?.status || { state: 'loading' });
  if (updateView) {
    qualify(
      'File review',
      updateView.state === 'ready' ? review : { state: updateView.state, stale: updateView.stale }
    );
    if (!reviewReady && review.pending_count > 0)
      ongoing.push(
        `Last known: ${review.pending_count} file${review.pending_count === 1 ? '' : 's'} to review. Refresh to confirm.`
      );
  } else if (known) qualify('File review', janitor.batch);
  if (known) qualify('Filing history', janitor.history);
  // Said once: when the running ticket is itself the next action, its title is
  // already the card's heading (taskDescriptor leaves this line out).
  const runningLine = current ? `Running: ${taskTitle(current)}` : '';
  if (runningLine) ongoing.push(runningLine);
  if (janitor?.scanning) ongoing.push('Scanning for files. Nothing moves without approval.');
  if (known && janitor.scanState === 'failed')
    ongoing.push('The last scan failed. Open Filing to retry explicitly.');
  if (known && janitor.history?.state === 'ready') {
    const counts = janitor.latestReceipt?.counts;
    const problems = (counts?.failed || 0) + (counts?.stale || 0);
    if (problems)
      ongoing.push(
        `Last filing: ${counts.applied || 0} applied, ${problems} not applied. See History in Filing.`
      );
  }
  const isLoading = [
    taskState,
    statusState,
    ...(known ? [janitor.batch?.state, janitor.history?.state] : [])
  ].includes('loading');
  const finish = (descriptor, { omit = '' } = {}) => ({
    ...descriptor,
    isLoading: isLoading || updateView?.state === 'loading',
    qualifications,
    ongoing: omit ? ongoing.filter(line => line !== omit) : ongoing,
    taskWork,
    message,
    sourceRows: updateSourceRows(saved),
    updateLabel:
      saved && descriptor.kind === 'review' && reviewReady && review.pending_count > 0
        ? `${message?.name || 'File review'} · ${saved.name}`
        : ''
  });
  const taskDescriptor = task => {
    const presentation = resolveTaskPresentation(task);
    const action = presentation.primaryAction;
    return finish(
      {
        kind: 'task',
        title: taskTitle(task),
        // The ticket's state, or how its latest run went; never a legacy status.
        description: resolveRunSignal(task) || resolveTicketStatePill(task).label,
        tone: presentation.tone,
        action:
          action && task.id
            ? {
                owner: 'task',
                kind: action.id,
                id: String(task.id),
                key: `task:${action.id}:${task.id}`,
                label: 'Open ticket'
              }
            : null
      },
      { omit: task === current ? runningLine : '' }
    );
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
  const folder = input.folderAgent;
  if (
    folder?.stationHire &&
    folder?.filingPile &&
    folder.workspaceID === workspaceId &&
    !known &&
    !input.filingDeferred
  )
    return finish({
      kind: 'station_offer',
      title: `Build a Filing station for ${folder.folder}?`,
      description: `${folder.filingPile.count} loose ${folder.filingPile.count === 1 ? 'file' : 'files'}. Review what Filing will do before building.`,
      tone: 'attention',
      action: {
        owner: 'console',
        kind: 'station_build',
        id: workspaceId,
        key: `station-build:${workspaceId}`,
        label: 'Review station build'
      }
    });
  // The folder's Commander waits on the person: its question, plan, stopped step,
  // offer or goal, named once here, directly below a ticket that needs the
  // person and above everything else (running work, filing, finished results and
  // an unread message). The title and the button are the shared row's words.
  const waiting = folderAgentWaiting(input.folderAgent, workspaceId);
  if (waiting)
    return finish({
      kind: 'folder_agent',
      title: waiting.text,
      description: '',
      tone: 'attention',
      action: {
        owner: 'folder_agent',
        kind: waiting.phase,
        id: workspaceId,
        key: `folder-agent:${waiting.phase}:${workspaceId}`,
        label: waiting.action
      }
    });
  if (input.onMap && janitor?.preparingPreview) {
    return {
      ...consoleDescriptor('setup', 'Preparing your preview…', 'Nothing is moving.', 'Open Filing'),
      kind: 'preparing_preview'
    };
  }
  if (known) {
    if (janitor.readiness === 'needs_attention')
      return consoleDescriptor(
        'repair',
        'Filing needs attention',
        'Repair folder access or setup before continuing.',
        'Repair setup',
        'attention'
      );
    if (
      input.onMap &&
      ['preparation_incomplete', 'preparation_changed'].includes(janitor.preparationState)
    )
      return consoleDescriptor(
        'setup',
        'Filing preview needs attention',
        'Open Filing to review the current folder and permissions.',
        'Open Filing',
        'attention'
      );
    if (!janitor.configured || janitor.readiness === 'setup_required') {
      if (input.onMap && janitor.linkedFolderName)
        return {
          ...consoleDescriptor(
            'setup',
            `Prepare a filing preview for ${janitor.linkedFolderName}?`,
            'See where your files would go before deciding what to move.',
            'Review folder access'
          ),
          filingNote: { name: String(input.leadAgentName || '').trim() || 'Ori' }
        };
      return consoleDescriptor(
        'setup',
        janitor.linkedFolderName
          ? `Let ${String(input.leadAgentName || '').trim() || 'Ori'} file ${janitor.linkedFolderName}`
          : 'Choose the folder to file',
        janitor.linkedFolderName
          ? 'Review what Ori may read and change. Nothing moves until you approve it.'
          : 'Nothing is scanned or moved until you choose one.',
        janitor.linkedFolderName ? 'Review permission' : 'Set up filing'
      );
    }
    if (janitor.consentRequired)
      return consoleDescriptor(
        'consent',
        'Review content consent',
        'Filing needs your decision before using document extracts.',
        'Review consent',
        'attention'
      );
  }
  const pending = updateView
    ? reviewReady
      ? review.pending_count
      : 0
    : known && janitor.batch?.state === 'ready'
      ? janitor.pendingCount
      : 0;
  const preview = input.onMap && known && janitor.firstPreview;
  const exactPreview =
    preview &&
    preview.rootId === janitor.rootId &&
    preview.batchId === janitor.batchId &&
    preview.count === pending &&
    (!updateView ||
      (reviewReady &&
        review.root_id === preview.rootId &&
        (review.batch_id || '') === preview.batchId));
  if (exactPreview && !preview.batchId && pending === 0)
    return consoleDescriptor(
      'review',
      'No eligible files for this preview',
      'The initial scan finished. Nothing moved. Scan again when you choose.',
      'Open Filing'
    );
  if (pending > 0) {
    const descriptor = consoleDescriptor(
      'review',
      `${pending} file${pending === 1 ? '' : 's'} ready for review`,
      exactPreview
        ? 'Your preview is at the Filing station. Nothing moved.'
        : 'Review the proposed filing locations. Nothing moves without your approval.',
      exactPreview ? 'Review preview' : 'Review files',
      'attention'
    );
    if (updateView)
      descriptor.action.key = `review:${workspaceId}:${review.installed_at}:${review.root_id}:${review.batch_id}`;
    descriptor.reviewStation = {
      count: pending,
      batchId: updateView ? review.batch_id : janitor.batchId
    };
    if (exactPreview) descriptor.filingPreview = preview;
    return descriptor;
  }
  if (input.onMap && known && janitor.preparationState === 'result_unavailable')
    return consoleDescriptor(
      'setup',
      'Initial preview unavailable',
      'Open Filing to check the current folder. Nothing was rescanned.',
      'Open Filing',
      'attention'
    );
  if (current) return taskDescriptor(current);
  if (known && janitor.scanning)
    return consoleDescriptor(
      'review',
      'Scanning for files',
      'The scan proposes a review batch. It does not move or remove files.',
      'Open Filing'
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
  if (message?.unread)
    return finish({
      kind: 'welcome',
      title: `New message from ${message.name}`,
      description: 'Open the saved conversation when you’re ready.',
      tone: 'neutral',
      action: { ...message, label: 'Open message' }
    });
  if (qualifications.length) {
    const failed =
      taskState === 'unavailable' ||
      statusState === 'unavailable' ||
      (updateView && (updateView.state === 'unavailable' || review.state === 'unavailable')) ||
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
          ? 'Manual scans only. Start a scan explicitly in Filing.'
          : janitor.mode === 'paused'
            ? 'Automatic scanning is paused. You can still scan on demand.'
            : 'Watching for arrivals. Nothing moves without your approval.';
    return consoleDescriptor(
      'review',
      janitor.mode === 'manual'
        ? 'Ready when you are'
        : janitor.mode === 'paused'
          ? 'Filing is paused'
          : 'Watching for files',
      copy,
      'Open Filing'
    );
  }
  return finish({
    kind: 'idle',
    title: 'No open work',
    description: 'Give your team something to do.',
    tone: 'neutral',
    action: { owner: 'workspace', kind: 'create', id: '', key: 'create', label: 'Give work' }
  });
}
