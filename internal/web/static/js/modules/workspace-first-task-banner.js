// The banner a workspace shows for its first task, the read-only first look a
// folder card seeded. Before the look is started it offers the one button that
// starts it (model tokens are spent on that click, never on opening the page);
// after that it makes plain that the agent is working, and says where the task
// ended up.

const WORKING = new Set(['', 'pending', 'in_progress', 'running', 'started']);
const NEEDS_YOU = new Set(['blocked', 'waiting_for_choice', 'needs_input']);
const FAILED = new Set(['failed', 'timeout', 'cancelled', 'canceled']);

// Why Start first look did not start, in the user's words. The codes are the
// start endpoint's own; an unknown one says only that it did not start.
const START_REASONS = {
  setup_wizard_opening: 'Finish this workspace’s setup first, then start the first look.',
  unassigned: 'The first task has no agent yet. Assign one, then start the first look.',
  local_activation_required: 'Activate this workspace on this computer to run the first look.'
};

// firstTaskStartMessage is the sentence shown when a start was refused.
export function firstTaskStartMessage(reason) {
  return START_REASONS[String(reason || '').trim()] || 'The first look could not start. Try again.';
}

// firstTaskBannerView turns a task's state into the banner's words. It is pure:
// `state` is the page's own execution state for the task and `agentName` the
// agent it was given to. `seeded` marks a look nobody has started yet, and
// `message` is what a refused start said. Nothing here is trusted markup; every
// field is text.
export function firstTaskBannerView({
  state,
  agentName,
  taskTitle,
  href,
  seeded = false,
  starting = false,
  message = ''
} = {}) {
  const key = String(state || '')
    .trim()
    .toLowerCase();
  const named = String(agentName || '').trim();
  const agent = named && named.toLowerCase() !== 'unassigned' ? named : '';
  const who = agent || 'Your agent';
  const title = String(taskTitle || '').trim();
  const link = String(href || '').trim();
  // Only a same-site path is ever linked.
  const safeHref = link.startsWith('/') && !link.startsWith('//') ? link : '';

  if (seeded) {
    const note = String(message || '').trim();
    const readOnly = title
      ? `${title}. It is read-only, so nothing in your folder changes.`
      : 'It is read-only, so nothing in your folder changes.';
    return {
      visible: true,
      phase: 'ready',
      heading: agent
        ? `${agent} is ready for its first look at this folder`
        : 'The first look at this folder is ready',
      detail: note || `${readOnly} Starting it spends model tokens.`,
      href: '',
      linkLabel: '',
      // No agent means nothing can run it: say so instead of offering a button
      // the server would refuse.
      action: agent
        ? { id: 'start-first-look', label: starting ? 'Starting…' : 'Start first look', starting }
        : null,
      note: agent ? '' : firstTaskStartMessage('unassigned')
    };
  }

  let phase = 'working';
  let heading = `${who} is working on its first task`;
  let detail = title ? `${title}. It is read-only, so nothing in your folder changes.` : '';
  if (key === 'completed') {
    phase = 'done';
    heading = `${who} finished its first task`;
    detail = 'Open the task to read what it found.';
  } else if (NEEDS_YOU.has(key)) {
    phase = 'needs-you';
    heading = `${who} needs you on its first task`;
    detail = 'It paused and is waiting for your input.';
  } else if (FAILED.has(key)) {
    phase = 'failed';
    heading = `${who} could not finish its first task`;
    detail = 'Open the task to see what happened and try again.';
  } else if (!WORKING.has(key)) {
    // An unknown state is shown as the neutral working one, never trusted.
    phase = 'working';
  }
  return {
    visible: true,
    phase,
    heading,
    detail,
    href: safeHref,
    linkLabel: phase === 'working' ? 'Watch the task' : 'Open the task',
    action: null,
    note: ''
  };
}

// --- A software project's first assessment -----------------------------------

// The states the server reports for a first task.
const ASSESSMENT_STATES = new Set(['seeded', 'running', 'waiting', 'finished', 'failed']);
// The full report is read on the task's own page in its workspace, which shows
// what the run wrote, and nowhere else.
const ASSESSMENT_REPORT = /^\/workspaces\/[a-z0-9][a-z0-9-]*\/task\/[A-Za-z0-9_-]+$/;
// The link that opens the Plans page with the assessment's suggestion placed,
// unsubmitted, in its "Start a plan" box. Nothing else is accepted as that link.
const ASSESSMENT_NEXT_ROUTE = /^\/workspaces\/[a-z0-9][a-z0-9-]*\/plans\?next=assessment$/;
const ASSESSMENT_COST =
  'Starting it spends model tokens once. The model gets what Ori read from the project and no tools, so nothing in the project is run or changed.';
const ASSESSMENT_NOT_STARTED = 'The assessment could not start. Try again.';

// assessmentStartNote is what a refused start says at once. A reason the server
// can explain has no sentence here: the banner is read again, and the server's
// own sentence and repair are shown instead.
export function assessmentStartNote(reason) {
  switch (String(reason || '').trim()) {
    case 'start_failed':
    case 'execution_unavailable':
    case 'workspace_unavailable':
    case 'admission_unavailable':
    case 'no_task_store':
      return ASSESSMENT_NOT_STARTED;
    default:
      return '';
  }
}

// assessmentBannerView is the banner for a first task that runs under one of
// Ori's own recipes: a read-only assessment of a software project. `look` is
// the server's reading of the task, the same one Home shows, so the two never
// disagree. Whether it can start or be tried again, why not, where that is
// repaired and what it suggests next all come from it; nothing is worked out
// here. `busy` is a click still in flight, `message` what that click was told,
// `taskHref` the page's own link to the task, and `dismissed` a finished
// assessment the viewer has put away.
export function assessmentBannerView(
  look,
  { busy = false, message = '', taskHref = '', dismissed = false } = {}
) {
  const state = String(look?.state || '').trim();
  if (
    !look ||
    typeof look !== 'object' ||
    !String(look.recipe || '').trim() ||
    !ASSESSMENT_STATES.has(state)
  )
    return { visible: false };

  // Only a same-site path is ever linked.
  const local = value => {
    const target = String(value || '').trim();
    return target.startsWith('/') && !target.startsWith('//') ? target : '';
  };
  const project =
    String(look.folder_name || '').trim() ||
    String(look.workspace_name || '').trim() ||
    'this project';
  const told = String(message || '').trim();
  const said = told || String(look.message || '').trim();
  const task = local(taskHref);
  const taskLink = label => (task ? [{ id: 'task', label, href: task, primary: false }] : []);
  const repairRoute = local(look.repair_route);
  const repairLabel = String(look.repair_label || '').trim();
  const repair = primary =>
    repairRoute && repairLabel
      ? [{ id: 'repair', label: repairLabel, href: repairRoute, primary }]
      : [];
  const base = {
    visible: true,
    assessment: true,
    href: '',
    linkLabel: '',
    note: '',
    action: null,
    links: [],
    dismiss: false
  };

  switch (state) {
    case 'seeded':
      if (look.can_start === true)
        return {
          ...base,
          phase: 'ready',
          heading: `The first assessment of ${project} is ready to start`,
          detail: ASSESSMENT_COST,
          note: told,
          action: {
            id: 'start-first-look',
            label: busy ? 'Starting…' : 'Start assessment',
            starting: busy
          }
        };
      return {
        ...base,
        phase: 'needs-you',
        heading: `The assessment of ${project} cannot start yet`,
        detail: said || 'Something it needs is missing.',
        links: repair(true)
      };
    case 'running':
      return {
        ...base,
        phase: 'working',
        heading: `Assessing ${project}…`,
        detail:
          'The model is reading what Ori gathered from the project. Nothing in it is being run or changed.',
        links: taskLink('Watch the task')
      };
    case 'waiting':
      return {
        ...base,
        phase: 'needs-you',
        heading: `The assessment of ${project} is paused`,
        detail: said || 'Open the task to see what it needs.',
        links: [...repair(true), ...taskLink('Open the task')]
      };
    case 'finished': {
      if (dismissed) return { visible: false };
      const report = String(look.report_route || '').trim();
      const reportHref = ASSESSMENT_REPORT.test(report) ? report : task;
      const nextRequest = String(look.next_request || '').trim();
      const nextRoute = local(look.next_route);
      const next = Boolean(nextRequest) && ASSESSMENT_NEXT_ROUTE.test(nextRoute);
      return {
        ...base,
        phase: 'done',
        heading: `The first assessment of ${project} is ready`,
        // The suggestion is text the model wrote: it is shown as text, and
        // following it only opens the Plans page with it in the box.
        detail: next
          ? `It suggests one next task: ${nextRequest}`
          : 'Open it to read what was found.',
        links: [
          ...(reportHref
            ? [{ id: 'report', label: 'Open the assessment', href: reportHref, primary: true }]
            : []),
          ...(next
            ? [{ id: 'next', label: 'Plan the suggested task', href: nextRoute, primary: false }]
            : [])
        ],
        dismiss: true
      };
    }
    default: {
      // It did not finish. It is tried again only when nothing has to be
      // repaired first; otherwise the repair is the one thing offered.
      const retry = look.can_retry === true;
      return {
        ...base,
        phase: 'failed',
        heading: `The assessment of ${project} did not finish`,
        detail: said || 'Nothing in the project was changed.',
        action: retry
          ? { id: 'retry-first-look', label: busy ? 'Starting…' : 'Try again', starting: busy }
          : null,
        links: [...repair(!retry), ...taskLink('Open the task')]
      };
    }
  }
}

// renderFirstTaskBanner draws the view into its mount (a hidden <section>).
// onStart is called when Start first look (or Start assessment) is pressed,
// onRetry when an assessment's Try again is, and onDismiss when a finished
// assessment is put away.
export function renderFirstTaskBanner(
  mount,
  view,
  doc = document,
  { onStart, onRetry, onDismiss } = {}
) {
  if (!mount) return;
  if (!view?.visible) {
    mount.hidden = true;
    mount.replaceChildren();
    return;
  }
  mount.hidden = false;
  mount.dataset.phase = view.phase;
  const pulse = doc.createElement('span');
  pulse.className = 'first-task-banner__pulse';
  pulse.setAttribute('aria-hidden', 'true');
  const text = doc.createElement('div');
  text.className = 'first-task-banner__text';
  const heading = doc.createElement('strong');
  heading.textContent = view.heading;
  text.append(heading);
  if (view.detail) {
    const detail = doc.createElement('span');
    detail.textContent = view.detail;
    text.append(detail);
  }
  if (view.note) {
    const note = doc.createElement('span');
    note.className = 'first-task-banner__note';
    note.textContent = view.note;
    text.append(note);
  }
  mount.replaceChildren(pulse, text);
  if (view.action) {
    const button = doc.createElement('button');
    button.type = 'button';
    button.className = 'btn btn-sm btn-primary';
    button.dataset.firstTaskAction = view.action.id;
    button.textContent = view.action.label;
    button.disabled = view.action.starting === true;
    const pressed = view.action.id === 'retry-first-look' ? onRetry : onStart;
    if (typeof pressed === 'function') button.addEventListener('click', () => pressed(button));
    mount.append(button);
  }
  if (view.href) {
    const link = doc.createElement('a');
    link.className = 'btn btn-sm btn-outline-secondary';
    link.href = view.href;
    link.textContent = view.linkLabel;
    mount.append(link);
  }
  (Array.isArray(view.links) ? view.links : []).forEach(entry => {
    const link = doc.createElement('a');
    link.className = entry.primary ? 'btn btn-sm btn-primary' : 'btn btn-sm btn-outline-secondary';
    link.dataset.firstTaskLink = entry.id;
    link.href = entry.href;
    link.textContent = entry.label;
    mount.append(link);
  });
  if (view.dismiss) {
    const dismiss = doc.createElement('button');
    dismiss.type = 'button';
    dismiss.className = 'btn btn-sm btn-link';
    dismiss.dataset.firstTaskAction = 'dismiss';
    dismiss.textContent = 'Dismiss';
    if (typeof onDismiss === 'function')
      dismiss.addEventListener('click', () => onDismiss(dismiss));
    mount.append(dismiss);
  }
}
