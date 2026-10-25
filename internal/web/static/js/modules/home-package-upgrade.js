// The reviewed Home package upgrade dialog, shared by the Home page and the
// Plugins page (docs/architecture/independent-program-homes.md §6.1).
//
// Both pages show the same review of every Home the package backs and commit
// it through the same service; only the transport differs. The Home page
// addresses its own Home, the Plugins page addresses the plugin. Nothing here
// navigates, and nothing a page supplies grants authority: the server derives
// the owner, the package and the plan, and a commit applies only the review
// its token names.

// agentEffect sums up what the upgrade does to one staffed agent's two saved
// prompts, its profile and its copy in the Home: each is replaced only while
// it still equals the old role prompt. "mixed" is one kept and one replaced,
// which is neither "kept" nor "updated".
export function agentEffect(agent = {}) {
  const actions = [agent.profile, agent.home_copy].filter(
    action => action === 'replace' || action === 'keep'
  );
  if (!actions.length) return 'missing';
  if (actions.includes('replace') && actions.includes('keep')) return 'mixed';
  return actions[0];
}

// providerUpgradeAgentLine says what the reviewed Home package upgrade does to
// one staffed agent. With done, it says what the committed upgrade did.
export function providerUpgradeAgentLine(agent = {}, { done = false } = {}) {
  switch (agentEffect(agent)) {
    case 'replace':
      return done ? 'got the new guidance' : 'gets the new guidance';
    case 'keep':
      return done ? 'kept your edited prompt' : 'keeps your edited prompt';
    case 'mixed':
      return agent.profile === 'keep'
        ? done
          ? 'kept your edited profile prompt; its Home copy got the new guidance'
          : 'keeps your edited profile prompt; its Home copy gets the new guidance'
        : done
          ? 'got the new guidance in its profile; its Home copy kept your edits'
          : 'gets the new guidance in its profile; its Home copy keeps your edits';
    default:
      return done
        ? 'had no saved profile; nothing was updated'
        : 'has no saved profile; nothing to update';
  }
}

function plural(count, one, many = `${one}s`) {
  return `${count} ${count === 1 ? one : many}`;
}

// providerUpgradeImpact is the review's one-line count of what moves.
export function providerUpgradeImpact(review) {
  const homes = reviewHomes(review);
  const projects = homes.reduce(
    (total, home) => total + (Array.isArray(home.projects) ? home.projects.length : 0),
    0
  );
  const agents = homes.reduce(
    (total, home) => total + (Array.isArray(home.agents) ? home.agents.length : 0),
    0
  );
  return `${plural(homes.length, 'Home')}, ${plural(projects, 'linked project')} and ${plural(
    agents,
    'staffed agent'
  )} affected.`;
}

// safeHomeLink accepts only a Home page path on this host, as the server
// builds it; anything else is no link at all.
export function safeHomeLink(link) {
  const value = text(link);
  return /^\/workspaces\/[^/?#\\]+\/assistant$/.test(value) ? value : '';
}

// providerUpgradeAdditions lists what a newer release adds to a Home besides
// guidance (for example a profile card), as plain sentences from the server.
export function providerUpgradeAdditions(review) {
  return (Array.isArray(review?.additions) ? review.additions : [])
    .map(line => String(line || '').trim())
    .filter(Boolean)
    .slice(0, 8);
}

// reviewHomes lists the review's Homes by name, so several read in order.
function reviewHomes(review) {
  return (Array.isArray(review?.homes) ? [...review.homes] : []).sort((a, b) =>
    String(a?.name ?? '').localeCompare(String(b?.name ?? ''), undefined, { sensitivity: 'base' })
  );
}

// providerUpgradeSummary is the review's opening sentence: which Homes move,
// and honestly how much changes. "Only Home guidance changes" is true only for
// a release that adds nothing.
export function providerUpgradeSummary(review) {
  const homes = reviewHomes(review);
  let subject = 'This Home and its linked projects move';
  if (homes.length === 1 && text(homes[0]?.name).trim()) {
    subject = `${text(homes[0].name).trim()} and its linked projects move`;
  } else if (homes.length > 1) {
    subject = `${homes.length} Homes and their linked projects move together`;
  }
  const moves = `${subject} from ${text(review?.from_version)} to ${text(review?.to_version)}.`;
  if (!providerUpgradeAdditions(review).length) return `${moves} Only Home guidance changes.`;
  const guidance = Array.isArray(review?.roles) && review.roles.length > 0;
  return guidance
    ? `${moves} It changes Home guidance and adds what is listed below. Nothing else changes.`
    : `${moves} It adds what is listed below. Nothing else changes.`;
}

// providerUpgradeGuidanceNote says why no role prompt is shown. A release
// that adds something else is not described as a skill text change.
export function providerUpgradeGuidanceNote(review) {
  return providerUpgradeAdditions(review).length
    ? 'Role prompts are unchanged.'
    : 'Role prompts are unchanged; only packaged skill text changes.';
}

function text(value) {
  return String(value == null ? '' : value);
}

function homesPhrase(review) {
  const homes = reviewHomes(review);
  if (homes.length === 1 && text(homes[0]?.name).trim()) return text(homes[0].name).trim();
  return homes.length > 1 ? `${homes.length} Homes` : 'the Home';
}

function lostAnswer(message, cause = null) {
  const error = new Error(message);
  // transport: Ori's own answer never arrived (or could not be read), so it
  // says nothing about what Ori did.
  error.transport = true;
  error.cause = cause;
  return error;
}

// upgradeRequest performs one upgrade call. A refusal throws with the
// server's message, stable code and details (an operation's ID and status).
// When Ori's answer is lost — no response, an unreadable body, or an error
// page without Ori's own code — the error is marked transport.
export async function upgradeRequest(fetchImpl, url, options = {}) {
  if (typeof fetchImpl !== 'function') throw new Error('Network access is unavailable');
  let response;
  try {
    response = await fetchImpl(url, {
      headers: {
        Accept: 'application/json',
        ...(options.body ? { 'Content-Type': 'application/json' } : {})
      },
      ...options
    });
  } catch (cause) {
    throw lostAnswer('Ori could not be reached, so its answer was lost.', cause);
  }
  let payload = null;
  try {
    payload = await response.json();
  } catch (_) {
    payload = null;
  }
  if (!response.ok) {
    // Ori's own refusals carry a code; a few (the reset fence) carry it as
    // error beside a message. Anything else is not Ori's answer.
    const code =
      typeof payload?.code === 'string' && payload.code !== ''
        ? payload.code
        : typeof payload?.error === 'string' && typeof payload?.message === 'string'
          ? payload.error
          : '';
    const error = code
      ? new Error(
          text(
            (payload.code ? payload.error || payload.message : payload.message) ||
              `Request failed (${response.status})`
          )
        )
      : lostAnswer(`Ori's answer was lost (${response.status}).`);
    error.status = response.status;
    error.code = code;
    error.details = (code && payload.details) || null;
    throw error;
  }
  if (payload == null || typeof payload !== 'object') {
    throw lostAnswer("Ori's answer could not be read.");
  }
  return payload;
}

function upgradeTransport(base, fetchImpl) {
  // Keep browser fetch a plain invocation: a stored Window.fetch called as a
  // method gets the wrong receiver.
  const call = (suffix, options) =>
    upgradeRequest((...args) => fetchImpl(...args), base + suffix, options);
  const named = token => JSON.stringify({ token });
  return {
    status: () => call('', { method: 'GET' }),
    review: () => call('/review', { method: 'POST', body: '{}' }),
    commit: token => call('/commit', { method: 'POST', body: named(token) }),
    // outcome asks what became of one review's commit; it never commits.
    outcome: token => call('/outcome', { method: 'POST', body: named(token) })
  };
}

// homeUpgradeTransport addresses the upgrade from one Home, by its workspace
// ID; it covers every Home the package backs all the same.
export function homeUpgradeTransport(workspaceId, { fetchImpl = globalThis.fetch } = {}) {
  return upgradeTransport(
    `/api/workspaces/${encodeURIComponent(text(workspaceId))}/assistant-program/provider-upgrade`,
    fetchImpl
  );
}

// pluginUpgradeTransport addresses the upgrade by plugin. The name is only a
// lookup key; the server decides which Homes the review covers.
export function pluginUpgradeTransport(name, { fetchImpl = globalThis.fetch } = {}) {
  return upgradeTransport(`/api/plugins/${encodeURIComponent(text(name))}/home-upgrade`, fetchImpl);
}

function element(doc, tag, { className = '', text: content = null, attributes = {} } = {}) {
  const node = doc.createElement(tag);
  if (className) node.className = className;
  for (const [name, value] of Object.entries(attributes)) node.setAttribute(name, value);
  if (content != null) node.textContent = text(content);
  return node;
}

// renderUpgradeReview builds the review's sections. Every name, prompt and
// warning is set as text, never parsed as markup.
export function renderUpgradeReview(review, doc = globalThis.document) {
  const container = element(doc, 'div', { className: 'home-package-upgrade__sections' });
  const section = (title, ...children) => {
    const wrapper = element(doc, 'section', { className: 'home-package-upgrade__section' });
    wrapper.append(element(doc, 'h3', { text: title }), ...children);
    container.append(wrapper);
  };
  const list = lines => {
    const items = element(doc, 'ul', { className: 'home-package-upgrade__list' });
    lines.forEach(line => items.append(element(doc, 'li', { text: line })));
    return items;
  };

  // The version transition and the size of the change come first.
  const versions = element(doc, 'p', { className: 'home-package-upgrade__versions' });
  versions.append(
    element(doc, 'span', {
      className: 'home-package-upgrade__version',
      text: review?.from_version
    }),
    element(doc, 'span', {
      className: 'home-package-upgrade__arrow',
      text: '→',
      attributes: { 'aria-hidden': 'true' }
    }),
    element(doc, 'span', { className: 'visually-hidden', text: ' to ' }),
    element(doc, 'span', {
      className: 'home-package-upgrade__version home-package-upgrade__version--new',
      text: review?.to_version
    })
  );
  const overview = element(doc, 'div', { className: 'home-package-upgrade__overview' });
  overview.append(
    versions,
    element(doc, 'p', {
      className: 'home-package-upgrade__impact',
      text: providerUpgradeImpact(review)
    })
  );
  container.append(overview);

  // The plugin review's own warnings are read before anything else.
  const warnings = (Array.isArray(review?.warnings) ? review.warnings : [])
    .map(warning => text(warning).trim())
    .filter(Boolean);
  if (warnings.length) section('Before you confirm', list(warnings));

  // What the newer release adds besides guidance, in the server's words.
  const additions = providerUpgradeAdditions(review);
  if (additions.length) section('What this release adds', list(additions));

  const roles = Array.isArray(review?.roles) ? review.roles : [];
  if (roles.length) {
    section(
      'Guidance changes',
      ...roles.map(role => {
        // Long prompts stay folded unless there is only one to read.
        const details = element(doc, 'details');
        details.open = roles.length === 1;
        const diff = element(doc, 'div', { className: 'home-package-upgrade__diff' });
        for (const [label, value] of [
          ['Current', role.old],
          ['New', role.new]
        ]) {
          const column = element(doc, 'div');
          column.append(
            element(doc, 'strong', { text: label }),
            element(doc, 'pre', { text: value, attributes: { tabindex: '0' } })
          );
          diff.append(column);
        }
        details.append(element(doc, 'summary', { text: role.label || role.role_id }), diff);
        return details;
      })
    );
  } else {
    section('Guidance changes', element(doc, 'p', { text: providerUpgradeGuidanceNote(review) }));
  }

  const homes = reviewHomes(review);
  const homeLines = homes.map(home => {
    const projects = Array.isArray(home.projects) ? home.projects : [];
    return projects.length
      ? `${text(home.name)}: ${plural(projects.length, 'linked project')} (${projects.join(', ')})`
      : `${text(home.name)}: no linked projects`;
  });
  if (Number(review?.trashed_homes || 0) > 0) {
    homeLines.push(
      `${plural(Number(review.trashed_homes), 'Home')} in the Trash ${
        Number(review.trashed_homes) === 1 ? 'keeps' : 'keep'
      } the old release.`
    );
  }
  section('Homes and projects', list(homeLines));

  // Each staffed agent's outcome, grouped by Home when there are several.
  const agentLines = home =>
    (Array.isArray(home.agents) ? home.agents : []).map(
      agent =>
        `${text(agent.agent_name)} (${text(agent.role_label)}): ${providerUpgradeAgentLine(agent)}`
    );
  const staffed = homes.filter(home => agentLines(home).length);
  if (!staffed.length) {
    section('Staffed agents', list(['No staffed agent uses a changed role.']));
  } else if (homes.length === 1) {
    section('Staffed agents', list(agentLines(homes[0])));
  } else {
    section(
      'Staffed agents',
      ...staffed.flatMap(home => [
        element(doc, 'p', { className: 'home-package-upgrade__group', text: home.name }),
        list(agentLines(home))
      ])
    );
  }

  // What the upgrade stops is told apart from what it leaves alone.
  section(
    'What is cancelled',
    list([
      `Pending library reviews and scans in ${
        homes.length === 1 ? 'this Home' : 'these Homes'
      } are cancelled; start them again afterwards.`
    ])
  );
  const stays = ['Approved project library folders stay approved.'];
  const skills = Array.isArray(review?.skills) ? review.skills : [];
  if (skills.length) stays.push(`Skills in the new release: ${skills.join(', ')}.`);
  section('What stays the same', list(stays));
  return container;
}

// renderUpgradeResult says what the succeeded operation did: which Homes are
// on the new release (each with its page when homeLinks is set and it is not
// the page already open) and what happened to each staffed agent's prompt.
export function renderUpgradeResult(
  review,
  operation,
  { homeLinks = false, currentPath = '' } = {},
  doc = globalThis.document
) {
  const container = element(doc, 'div', { className: 'home-package-upgrade__sections' });
  const section = (title, items) => {
    const wrapper = element(doc, 'section', { className: 'home-package-upgrade__section' });
    const list = element(doc, 'ul', { className: 'home-package-upgrade__list' });
    items.forEach(item => list.append(item));
    wrapper.append(element(doc, 'h3', { text: title }), list);
    container.append(wrapper);
  };
  const item = content => element(doc, 'li', { text: content });

  const homes = reviewHomes(review).map(home => {
    const entry = item(text(home.name));
    const link = homeLinks ? safeHomeLink(home.link) : '';
    if (link && link !== currentPath) {
      const anchor = element(doc, 'a', {
        className: 'home-package-upgrade__home-link',
        text: `Open ${text(home.name)}`,
        attributes: { href: link }
      });
      entry.append(anchor);
    }
    return entry;
  });
  if (Number(review?.trashed_homes || 0) > 0) {
    homes.push(item(`${review.trashed_homes} in the Trash keep the old release.`));
  }
  section(`Homes now on ${text(operation?.to_version)}`, homes);

  const labels = new Map(
    (Array.isArray(review?.roles) ? review.roles : []).map(role => [role.role_id, role.label])
  );
  const agents = (Array.isArray(operation?.agents) ? operation.agents : []).map(agent => {
    const label = text(labels.get(agent.role_id) || agent.role_id);
    return item(
      `${text(agent.agent_name)}${label ? ` (${label})` : ''}: ${providerUpgradeAgentLine(agent, { done: true })}`
    );
  });
  section(
    'Staffed agents',
    agents.length ? agents : [item('No staffed agent used a changed role.')]
  );
  container.append(
    element(doc, 'p', {
      className: 'home-package-upgrade__operation',
      text: `Operation ${text(operation?.id)}`
    })
  );
  return container;
}

let dialogSequence = 0;

// A refusal after which a fresh review is the way forward: the review was
// used or changed, or nothing was installed.
const REVIEW_AGAIN_CODES = new Set([
  'home_upgrade_review_stale',
  'home_upgrade_cancelled',
  'home_upgrade_unavailable'
]);
// Refusals that mean an operation holds the package.
const HOLD_CODES = new Set(['home_upgrade_active', 'home_upgrade_reconcile_required']);
// How long to keep asking what became of a commit whose answer was lost:
// about fifteen seconds in all, then the outcome is said to be unknown.
const OUTCOME_DELAYS = [0, 1000, 2000, 4000, 8000];

// operationHold describes an operation that still holds the package. One
// that needs reconciliation is not something this dialog can repair.
export function operationHold(operation = {}) {
  const id = text(operation.id);
  const to = text(operation.to_version);
  if (operation.status === 'reconcile_required') {
    return {
      title: 'This update needs attention',
      text: [
        `The update to ${to} stopped partway (operation ${id}).`,
        text(operation.reason).trim(),
        'Nothing is chosen automatically, and it cannot be reviewed again until it is reconciled.'
      ]
        .filter(Boolean)
        .join(' '),
      retry: false
    };
  }
  return {
    title: 'An update is in progress',
    text: `Ori is still moving the Homes to ${to} (operation ${id}). Check again in a moment.`,
    retry: true
  };
}

// homeUpgradeRowNotice is the persistent line on a Plugins row for a plugin
// Homes use, from its GET status: an operation that holds the package stays
// visible whatever the update badge says, an unreadable status says so, and
// otherwise the row names the Homes an Update moves together.
export function homeUpgradeRowNotice(view) {
  const operation = view?.operation;
  if (operation?.status === 'reconcile_required') {
    return {
      state: 'attention',
      text: [
        `Home update to ${text(operation.to_version)} needs attention (operation ${text(operation.id)}).`,
        text(operation.reason).trim(),
        'Nothing is chosen automatically.'
      ]
        .filter(Boolean)
        .join(' ')
    };
  }
  if (operation?.status === 'claimed' || operation?.status === 'replaced') {
    return {
      state: 'running',
      text: `Home update to ${text(operation.to_version)} is in progress (operation ${text(operation.id)}).`
    };
  }
  if (view?.status_unavailable) {
    return { state: 'unavailable', text: 'Home update status is unavailable right now.' };
  }
  const names = (Array.isArray(view?.homes) ? view.homes : [])
    .map(home => text(home?.name).trim())
    .filter(Boolean);
  if (!names.length) return null;
  const listed =
    names.length === 1
      ? names[0]
      : `${names.slice(0, -1).join(', ')} and ${names[names.length - 1]}`;
  return {
    state: 'homes',
    text:
      names.length === 1
        ? `Used by ${listed}; Update moves it with the plugin.`
        : `Used by ${listed}; Update moves them together.`
  };
}

// HomePackageUpgradeDialog is one review-and-confirm of the upgrade. Its
// states are loading, review, failed (no review could be made), held (an
// operation holds the package), committing, checking (the commit's answer was
// lost), unknown, settled (not applied), succeeded, commit-failed and closed.
// It commits at most once per review, only from review, never replays a
// commit, and cannot be closed while a commit is in flight: closing would not
// stop the server's work, so it must not look as if it could.
export class HomePackageUpgradeDialog {
  constructor({
    transport,
    trigger = null,
    returnFocus = null,
    documentRef = globalThis.document,
    windowRef = globalThis.window,
    label = 'Review package update',
    homeLinks = false,
    currentPath = '',
    onSucceeded = null,
    onClosed = null,
    outcomeDelays = OUTCOME_DELAYS
  } = {}) {
    this.transport = transport;
    this.trigger = trigger;
    // returnFocus names the control to focus on close when the page may have
    // replaced the trigger meanwhile (a re-rendered list row).
    this.returnFocus = returnFocus;
    this.document = documentRef;
    this.window = windowRef;
    this.label = label;
    // After success the result may link each Home's page; currentPath is the
    // page already open, which is not linked again.
    this.homeLinks = homeLinks;
    this.currentPath = currentPath;
    this.onSucceeded = onSucceeded;
    this.onClosed = onClosed;
    this.outcomeDelays = outcomeDelays;
    this.state = 'idle';
    this.review = null;
    this.operation = null;
    this.parts = null;
    this.unloadGuard = null;
    // run numbers each load or outcome check, so a late answer to an earlier
    // one changes nothing.
    this.run = 0;
    this.timer = null;
    this.wake = null;
  }

  get closed() {
    return this.state === 'closed';
  }

  stale(run) {
    return this.closed || run !== this.run;
  }

  setState(state) {
    this.state = state;
    const dialog = this.parts?.dialog;
    if (!dialog) return;
    dialog.dataset.state = state;
    dialog.setAttribute(
      'aria-busy',
      state === 'loading' || state === 'committing' || state === 'checking' ? 'true' : 'false'
    );
  }

  // setActions replaces the dialog's extra buttons (Cancel/Close stays first).
  setActions(actions = []) {
    for (const button of this.parts.extras || []) button.remove();
    this.parts.extras = actions.map(({ label, onClick, primary = true }) => {
      const button = element(this.document, 'button', {
        className: `modern-btn ${primary ? 'modern-btn-primary' : 'modern-btn-secondary'}`,
        text: label,
        attributes: { type: 'button' }
      });
      button.addEventListener('click', () => void onClick());
      this.parts.controls.append(button);
      return button;
    });
    return this.parts.extras;
  }

  // settle shows a state with nothing in flight: Close and the given actions.
  settle(state, heading, statusText, actions = []) {
    const { cancel, error, body } = this.parts;
    this.guardUnload(false);
    this.setState(state);
    this.parts.heading.textContent = heading;
    this.parts.status.textContent = statusText;
    error.textContent = '';
    body.replaceChildren();
    this.parts.confirm?.remove();
    this.parts.confirm = null;
    cancel.disabled = false;
    cancel.textContent = 'Close';
    const buttons = this.setActions(actions);
    (buttons[0] || cancel).focus();
  }

  wait(ms) {
    return new Promise(resolve => {
      this.wake = resolve;
      this.timer = setTimeout(() => {
        this.timer = null;
        this.wake = null;
        resolve();
      }, ms);
    });
  }

  stopWaiting() {
    if (this.timer !== null) clearTimeout(this.timer);
    this.timer = null;
    const wake = this.wake;
    this.wake = null;
    wake?.();
  }

  build() {
    const doc = this.document;
    const id = `homePackageUpgrade${++dialogSequence}`;
    const dialog = element(doc, 'dialog', {
      className: 'home-package-upgrade',
      attributes: { 'aria-labelledby': `${id}Title`, 'aria-describedby': `${id}Status` }
    });
    const eyebrow = element(doc, 'p', {
      className: 'home-package-upgrade__eyebrow',
      text: this.label
    });
    const heading = element(doc, 'h2', {
      className: 'home-package-upgrade__title',
      text: 'Review the Home upgrade',
      attributes: { id: `${id}Title`, tabindex: '-1' }
    });
    const status = element(doc, 'p', {
      className: 'home-package-upgrade__status',
      text: 'Inspecting the new release…',
      attributes: { id: `${id}Status`, role: 'status', 'aria-live': 'polite' }
    });
    const body = element(doc, 'div', { className: 'home-package-upgrade__body' });
    const error = element(doc, 'p', {
      className: 'home-package-upgrade__error',
      attributes: { role: 'alert' }
    });
    const controls = element(doc, 'div', { className: 'home-package-upgrade__actions' });
    const cancel = element(doc, 'button', {
      className: 'modern-btn modern-btn-secondary',
      text: 'Cancel',
      attributes: { type: 'button' }
    });
    cancel.addEventListener('click', () => this.close());
    controls.append(cancel);
    dialog.append(eyebrow, heading, status, body, error, controls);
    dialog.addEventListener('cancel', event => {
      event.preventDefault();
      this.close();
    });
    this.parts = {
      dialog,
      heading,
      status,
      body,
      error,
      controls,
      cancel,
      confirm: null,
      extras: []
    };
    return dialog;
  }

  async open() {
    if (this.state !== 'idle') return this;
    const dialog = this.build();
    this.document.body.append(dialog);
    if (typeof dialog.showModal === 'function') dialog.showModal();
    else dialog.setAttribute('open', '');
    await this.load();
    return this;
  }

  // load reads the package's status before reviewing, so a reopened dialog
  // shows an operation that still holds the package instead of offering a
  // second commit. Review again comes back here.
  async load() {
    const run = ++this.run;
    const { heading, status, body, error, cancel } = this.parts;
    this.guardUnload(false);
    this.review = null;
    this.operation = null;
    heading.textContent = 'Review the Home upgrade';
    status.textContent = 'Inspecting the new release…';
    error.textContent = '';
    body.replaceChildren();
    this.parts.confirm?.remove();
    this.parts.confirm = null;
    this.setActions([]);
    cancel.disabled = false;
    cancel.textContent = 'Cancel';
    this.setState('loading');
    cancel.focus();
    if (typeof this.transport.status === 'function') {
      let summary = null;
      try {
        summary = await this.transport.status();
      } catch (_) {
        summary = null; // the review below answers for itself
      }
      if (this.stale(run)) return;
      if (summary?.operation) {
        this.showHold(summary.operation);
        return;
      }
    }
    let review;
    try {
      review = await this.transport.review();
    } catch (requestError) {
      // A late answer for a dialog already closed, or for an earlier load,
      // changes nothing.
      if (this.stale(run)) return;
      if (HOLD_CODES.has(requestError?.code)) {
        this.showHold(requestError.details, requestError.message);
        return;
      }
      this.setState('failed');
      status.textContent = '';
      error.textContent = requestError?.message || 'The update could not be reviewed.';
      cancel.textContent = 'Close';
      cancel.focus();
      return;
    }
    if (this.stale(run)) return;
    this.showReview(review);
  }

  // showHold says an operation holds the package. Only one that is still
  // running offers Check again; nothing here repairs or retries a commit.
  showHold(operation, message = '') {
    if (!operation?.id) {
      this.settle(
        'held',
        'Another update is in progress or needs attention',
        message || 'Another update of this plugin is in progress or needs attention.',
        [{ label: 'Check again', onClick: () => this.load() }]
      );
      return;
    }
    const hold = operationHold(operation);
    this.operation = operation;
    this.settle(
      'held',
      hold.title,
      hold.text,
      hold.retry ? [{ label: 'Check again', onClick: () => this.load() }] : []
    );
  }

  showReview(review) {
    const { heading, status, body, controls } = this.parts;
    this.parts.confirm?.remove();
    this.review = review;
    heading.textContent = `Update ${text(review?.plugin_id)} to ${text(review?.to_version)}?`;
    status.textContent = providerUpgradeSummary(review);
    body.replaceChildren(renderUpgradeReview(review, this.document));
    const confirm = element(this.document, 'button', {
      className: 'modern-btn modern-btn-primary',
      text: 'Confirm update',
      attributes: { type: 'button' }
    });
    confirm.addEventListener('click', () => void this.commit());
    controls.append(confirm);
    this.parts.confirm = confirm;
    this.setState('review');
    // The review is read from its heading; Enter must not confirm an update
    // nobody has read yet, so Confirm update is reached by Tab.
    heading.focus();
  }

  async commit() {
    // One commit per reviewed token: a second click, or a click after the
    // dialog moved on, does nothing.
    if (this.state !== 'review' || !this.review) return null;
    const { status, error, cancel, confirm } = this.parts;
    const review = this.review;
    this.setState('committing');
    confirm.disabled = true;
    cancel.disabled = true;
    error.textContent = '';
    status.textContent = `Installing ${text(review.to_version)} and moving ${homesPhrase(review)}… Keep this open; closing it would not stop the update.`;
    this.guardUnload(true);
    let operation;
    try {
      operation = await this.transport.commit(review.token);
    } catch (commitError) {
      this.guardUnload(false);
      // Ori's answer was lost: the update may or may not have happened, so
      // ask what became of this review instead of guessing or committing again.
      if (commitError?.transport) return this.resolveOutcome();
      this.showCommitFailure(commitError);
      return null;
    }
    // Only the service's own succeeded status is success.
    if (operation?.status !== 'succeeded') return this.showSettled(operation);
    return this.finishSucceeded(operation);
  }

  async finishSucceeded(operation) {
    this.showResult(operation);
    if (typeof this.onSucceeded === 'function') {
      try {
        await this.onSucceeded(operation, this);
      } catch (refreshError) {
        // The update itself succeeded; only this page's refresh did not.
        this.parts.error.textContent = `The update succeeded, but this page could not refresh: ${
          refreshError?.message || 'reload it to see the new version'
        }.`;
      }
    }
    return operation;
  }

  // resolveOutcome asks, for a bounded while, what became of the commit of
  // this dialog's review. Only the review's own operation answers; success is
  // only its succeeded status. It never commits again.
  async resolveOutcome() {
    const run = ++this.run;
    this.settle(
      'checking',
      'Checking what happened',
      'The answer to your update was lost on the way back, so whether it happened is not known yet. Checking with Ori…'
    );
    let last = 'unreachable';
    for (const delay of this.outcomeDelays) {
      if (delay) await this.wait(delay);
      if (this.stale(run)) return null;
      let attempt;
      try {
        attempt = await this.transport.outcome(this.review.token);
      } catch (checkError) {
        if (this.stale(run)) return null;
        if (checkError?.transport) continue;
        return this.showUnknown(
          `Ori could not say what became of the update: ${checkError?.message || 'its status is unavailable'}. Nothing will be committed again.`
        );
      }
      if (this.stale(run)) return null;
      const operation = attempt?.operation;
      if (operation?.status === 'succeeded') return this.finishSucceeded(operation);
      if (operation && !['claimed', 'replaced'].includes(operation.status)) {
        return this.showSettled(operation);
      }
      if (!operation && attempt?.review === 'expired') {
        return this.showNotApplied('This review expired before Ori applied it. Nothing changed.');
      }
      if (!operation && attempt?.review === 'unknown') {
        return this.showNotApplied(
          'Ori has no record of this review, so nothing was applied from it.'
        );
      }
      last = operation ? 'running' : 'pending';
    }
    const where = {
      running: 'Ori is still applying the update.',
      pending: 'Ori has no record that this update started; your request may still be on its way.',
      unreachable: 'Ori could not be reached to check.'
    }[last];
    return this.showUnknown(
      `${where} The outcome is not known yet, and nothing will be committed again.`
    );
  }

  showUnknown(message) {
    this.settle('unknown', 'The outcome is not known yet', message, [
      { label: 'Check again', onClick: () => this.resolveOutcome() }
    ]);
    return null;
  }

  // showSettled reports an operation that ended without success.
  showSettled(operation) {
    if (operation?.status === 'cancelled') {
      return this.showNotApplied(
        text(operation.reason).trim() || 'The new release could not be installed. Nothing changed.',
        operation
      );
    }
    if (
      operation?.status === 'reconcile_required' ||
      operation?.status === 'claimed' ||
      operation?.status === 'replaced'
    ) {
      this.showHold(operation);
      return null;
    }
    return this.showUnknown(
      `The update did not report success (${text(operation?.status) || 'no status'}). Nothing will be committed again.`
    );
  }

  // showNotApplied says, from the server's own record, that this review
  // changed nothing; a new review and confirmation are the way forward.
  showNotApplied(message, operation = null) {
    this.operation = operation;
    this.settle(
      'settled',
      'The update was not applied',
      operation?.id ? `${message} Operation ${text(operation.id)}.` : message,
      [{ label: 'Review again', onClick: () => this.load() }]
    );
    return null;
  }

  showResult(operation) {
    const { heading, status, body, cancel, confirm } = this.parts;
    this.operation = operation;
    this.guardUnload(false);
    this.setState('succeeded');
    heading.textContent = `${text(this.review?.plugin_id)} is updated to ${text(operation.to_version)}`;
    status.textContent = `Updated to ${text(operation.to_version)}.`;
    body.replaceChildren(
      renderUpgradeResult(
        this.review,
        operation,
        { homeLinks: this.homeLinks, currentPath: this.currentPath },
        this.document
      )
    );
    confirm?.remove();
    this.parts.confirm = null;
    this.setActions([]);
    this.parts.error.textContent = '';
    cancel.disabled = false;
    cancel.textContent = 'Close';
    cancel.focus();
  }

  // guardUnload asks before leaving the page while a commit is in flight:
  // leaving would not stop the update, and its result would go unseen.
  guardUnload(on) {
    const target = this.window;
    if (typeof target?.addEventListener !== 'function') return;
    if (on && !this.unloadGuard) {
      this.unloadGuard = event => {
        event.preventDefault();
        event.returnValue = '';
      };
      target.addEventListener('beforeunload', this.unloadGuard);
    } else if (!on && this.unloadGuard) {
      target.removeEventListener('beforeunload', this.unloadGuard);
      this.unloadGuard = null;
    }
  }

  // showCommitFailure reports Ori's own refusal of the commit: nothing was
  // applied from this review, or an operation holds the package.
  showCommitFailure(commitError) {
    if (HOLD_CODES.has(commitError?.code)) {
      this.showHold(commitError.details, commitError.message);
      return;
    }
    const { status, error, cancel } = this.parts;
    this.setState('commit-failed');
    status.textContent = '';
    error.textContent = commitError?.details?.id
      ? `${commitError.message} Operation ${text(commitError.details.id)}.`
      : commitError?.message || 'The update was not applied.';
    this.parts.confirm?.remove();
    this.parts.confirm = null;
    cancel.disabled = false;
    cancel.textContent = 'Close';
    const buttons = REVIEW_AGAIN_CODES.has(commitError?.code)
      ? this.setActions([{ label: 'Review again', onClick: () => this.load() }])
      : this.setActions([]);
    (buttons[0] || cancel).focus();
  }

  // close ends the dialog unless a commit is still in flight; it stops any
  // outcome check. It reports whether the dialog closed.
  close() {
    if (this.state === 'committing') {
      this.parts.status.textContent = `Still installing ${text(this.review?.to_version)}. Closing this would not stop the update; wait for it to finish.`;
      return false;
    }
    if (this.closed || !this.parts) return false;
    const { dialog } = this.parts;
    this.run++;
    this.stopWaiting();
    this.guardUnload(false);
    this.setState('closed');
    if (dialog.open && typeof dialog.close === 'function') dialog.close();
    dialog.remove();
    const focus =
      typeof this.returnFocus === 'function' ? this.returnFocus() || this.trigger : this.trigger;
    if (focus?.isConnected !== false) focus?.focus?.();
    if (typeof this.onClosed === 'function') this.onClosed(this);
    return true;
  }
}

// openHomePackageUpgrade opens the review dialog and returns it while the
// review loads.
export function openHomePackageUpgrade(options) {
  const dialog = new HomePackageUpgradeDialog(options);
  void dialog.open();
  return dialog;
}

// The Plugins page is a classic script; it reaches this dialog through a
// global, as it reaches window.PluginLifecycle. Module scripts run before
// DOMContentLoaded, so the global exists before any Update button does.
if (typeof window !== 'undefined') {
  window.HomePackageUpgrade = Object.freeze({
    homeUpgradeRowNotice,
    openHomePackageUpgrade,
    pluginUpgradeTransport
  });
}
