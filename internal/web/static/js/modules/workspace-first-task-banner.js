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

// renderFirstTaskBanner draws the view into its mount (a hidden <section>).
// onStart is called when Start first look is pressed.
export function renderFirstTaskBanner(mount, view, doc = document, { onStart } = {}) {
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
    if (typeof onStart === 'function') button.addEventListener('click', () => onStart(button));
    mount.append(button);
  }
  if (view.href) {
    const link = doc.createElement('a');
    link.className = 'btn btn-sm btn-outline-secondary';
    link.href = view.href;
    link.textContent = view.linkLabel;
    mount.append(link);
  }
}
