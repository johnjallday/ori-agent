// The banner a workspace shows while the agent works on its first task, the one
// a folder card seeded and started on first open. It makes plain that the agent
// is working, and says where the task ended up.

const WORKING = new Set(['', 'pending', 'in_progress', 'running', 'started']);
const NEEDS_YOU = new Set(['blocked', 'waiting_for_choice', 'needs_input']);
const FAILED = new Set(['failed', 'timeout', 'cancelled', 'canceled']);

// firstTaskBannerView turns a task's state into the banner's words. It is pure:
// `state` is the page's own execution state for the task and `agentName` the
// agent it was given to. Nothing here is trusted markup; every field is text.
export function firstTaskBannerView({ state, agentName, taskTitle, href } = {}) {
  const key = String(state || '')
    .trim()
    .toLowerCase();
  const agent = String(agentName || '').trim() || 'Your agent';
  const title = String(taskTitle || '').trim();
  let phase = 'working';
  let heading = `${agent} is working on its first task`;
  let detail = title ? `${title}. It is read-only, so nothing in your folder changes.` : '';
  if (key === 'completed') {
    phase = 'done';
    heading = `${agent} finished its first task`;
    detail = 'Open the task to read what it found.';
  } else if (NEEDS_YOU.has(key)) {
    phase = 'needs-you';
    heading = `${agent} needs you on its first task`;
    detail = 'It paused and is waiting for your input.';
  } else if (FAILED.has(key)) {
    phase = 'failed';
    heading = `${agent} could not finish its first task`;
    detail = 'Open the task to see what happened and try again.';
  } else if (!WORKING.has(key)) {
    // An unknown state is shown as the neutral working one, never trusted.
    phase = 'working';
  }
  const link = String(href || '').trim();
  return {
    visible: true,
    phase,
    heading,
    detail,
    // Only a same-site path is ever linked.
    href: link.startsWith('/') && !link.startsWith('//') ? link : '',
    linkLabel: phase === 'working' ? 'Watch the task' : 'Open the task'
  };
}

// renderFirstTaskBanner draws the view into its mount (a hidden <section>).
export function renderFirstTaskBanner(mount, view, doc = document) {
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
  mount.replaceChildren(pulse, text);
  if (view.href) {
    const link = doc.createElement('a');
    link.className = 'btn btn-sm btn-outline-secondary';
    link.href = view.href;
    link.textContent = view.linkLabel;
    mount.append(link);
  }
}
