// The reviewed Home package upgrade dialog, shared by the Home page and the
// Plugins page (docs/architecture/independent-program-homes.md §6.1).
//
// Both pages show the same review of every Home the package backs and commit
// it through the same service; only the transport differs. The Home page
// addresses its own Home, the Plugins page addresses the plugin. Nothing here
// navigates, and nothing a page supplies grants authority: the server derives
// the owner, the package and the plan, and a commit applies only the review
// its token names.

// providerUpgradeAgentLine says what the reviewed Home package upgrade does to
// one staffed agent: a prompt is replaced only while it still equals the old
// role prompt, so an edited profile or Home copy keeps the owner's text.
export function providerUpgradeAgentLine(agent = {}) {
  if (agent.profile === 'keep' || agent.home_copy === 'keep') return 'keeps your edited prompt';
  if (agent.profile === 'replace' || agent.home_copy === 'replace') return 'gets the new guidance';
  return 'has no saved profile; nothing to update';
}

// providerUpgradeAdditions lists what a newer release adds to a Home besides
// guidance (for example a profile card), as plain sentences from the server.
export function providerUpgradeAdditions(review) {
  return (Array.isArray(review?.additions) ? review.additions : [])
    .map(line => String(line || '').trim())
    .filter(Boolean)
    .slice(0, 8);
}

function reviewHomes(review) {
  return Array.isArray(review?.homes) ? review.homes : [];
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
    subject = `${homes.length} Homes and their linked projects move`;
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

// upgradeRequest performs one upgrade call. A refusal throws with the
// server's message, stable code and details (an operation's ID and status).
export async function upgradeRequest(fetchImpl, url, options = {}) {
  if (typeof fetchImpl !== 'function') throw new Error('Network access is unavailable');
  const response = await fetchImpl(url, {
    headers: {
      Accept: 'application/json',
      ...(options.body ? { 'Content-Type': 'application/json' } : {})
    },
    ...options
  });
  let payload = {};
  try {
    payload = (await response.json()) || {};
  } catch (_) {
    payload = {};
  }
  if (!response.ok) {
    const error = new Error(
      text(payload.error || payload.message || `Request failed (${response.status})`)
    );
    error.status = response.status;
    error.code = text(payload.code);
    error.details = payload.details || null;
    throw error;
  }
  return payload;
}

// homeUpgradeTransport addresses the upgrade from one Home. request is the
// Home page's own API call, rooted at that Home's assistant program.
export function homeUpgradeTransport(request) {
  return {
    review: () => request('/provider-upgrade/review', { method: 'POST', body: '{}' }),
    commit: token =>
      request('/provider-upgrade/commit', { method: 'POST', body: JSON.stringify({ token }) })
  };
}

// pluginUpgradeTransport addresses the upgrade by plugin. The name is only a
// lookup key; the server decides which Homes the review covers.
export function pluginUpgradeTransport(name, { fetchImpl = globalThis.fetch } = {}) {
  const base = `/api/plugins/${encodeURIComponent(text(name))}/home-upgrade`;
  // Keep browser fetch a plain invocation: a stored Window.fetch called as a
  // method gets the wrong receiver.
  const call = (suffix, options) =>
    upgradeRequest((...args) => fetchImpl(...args), base + suffix, options);
  return {
    status: () => call('', { method: 'GET' }),
    review: () => call('/review', { method: 'POST', body: '{}' }),
    commit: token => call('/commit', { method: 'POST', body: JSON.stringify({ token }) })
  };
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

  const roles = Array.isArray(review?.roles) ? review.roles : [];
  if (roles.length) {
    section(
      'Guidance changes',
      ...roles.map(role => {
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
            element(doc, 'pre', { text: value })
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

  // What the newer release adds besides guidance, in the server's words.
  const additions = providerUpgradeAdditions(review);
  if (additions.length) section('What this release adds', list(additions));

  const homes = reviewHomes(review).map(home => {
    const projects = Array.isArray(home.projects) ? home.projects : [];
    return projects.length
      ? `${text(home.name)}: ${projects.length} linked ${projects.length === 1 ? 'project' : 'projects'} (${projects.join(', ')})`
      : `${text(home.name)}: no linked projects`;
  });
  if (Number(review?.trashed_homes || 0) > 0) {
    homes.push(`${review.trashed_homes} in the Trash keep the old release.`);
  }
  section('Homes and projects', list(homes));

  const agents = reviewHomes(review).flatMap(home =>
    (Array.isArray(home.agents) ? home.agents : []).map(
      agent =>
        `${text(agent.agent_name)} (${text(agent.role_label)}): ${providerUpgradeAgentLine(agent)}`
    )
  );
  section(
    'Staffed agents',
    list(agents.length ? agents : ['No staffed agent uses a changed role.'])
  );

  const stays = [
    'Approved project library folders stay approved.',
    'Pending library reviews and scans are cancelled; start them again afterwards.'
  ];
  const skills = Array.isArray(review?.skills) ? review.skills : [];
  if (skills.length) stays.push(`Skills in the new release: ${skills.join(', ')}.`);
  (Array.isArray(review?.warnings) ? review.warnings : []).forEach(warning =>
    stays.push(text(warning))
  );
  section('What stays the same', list(stays));
  return container;
}

let dialogSequence = 0;

// HomePackageUpgradeDialog is one review-and-confirm of the upgrade. Its
// states are loading, review, failed (the review could not be made),
// committing, succeeded, commit-failed and closed. It commits at most once,
// only from review, and cannot be closed while a commit is in flight: closing
// would not stop the server's work, so it must not look as if it could.
export class HomePackageUpgradeDialog {
  constructor({
    transport,
    trigger = null,
    returnFocus = null,
    documentRef = globalThis.document,
    label = 'Review package update',
    onSucceeded = null,
    onClosed = null
  } = {}) {
    this.transport = transport;
    this.trigger = trigger;
    // returnFocus names the control to focus on close when the page may have
    // replaced the trigger meanwhile (a re-rendered list row).
    this.returnFocus = returnFocus;
    this.document = documentRef;
    this.label = label;
    this.onSucceeded = onSucceeded;
    this.onClosed = onClosed;
    this.state = 'idle';
    this.review = null;
    this.operation = null;
    this.parts = null;
  }

  get closed() {
    return this.state === 'closed';
  }

  setState(state) {
    this.state = state;
    const dialog = this.parts?.dialog;
    if (!dialog) return;
    dialog.dataset.state = state;
    dialog.setAttribute(
      'aria-busy',
      state === 'loading' || state === 'committing' ? 'true' : 'false'
    );
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
      attributes: { id: `${id}Title` }
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
    this.parts = { dialog, heading, status, body, error, controls, cancel, confirm: null };
    return dialog;
  }

  async open() {
    if (this.state !== 'idle') return this;
    const dialog = this.build();
    this.document.body.append(dialog);
    if (typeof dialog.showModal === 'function') dialog.showModal();
    else dialog.setAttribute('open', '');
    this.setState('loading');
    this.parts.cancel.focus();
    let review;
    try {
      review = await this.transport.review();
    } catch (requestError) {
      if (this.closed) return this;
      this.setState('failed');
      this.parts.status.textContent = '';
      this.parts.error.textContent = requestError?.message || 'The update could not be reviewed.';
      this.parts.cancel.focus();
      return this;
    }
    // A late answer for a dialog already closed changes nothing.
    if (this.closed) return this;
    this.showReview(review);
    return this;
  }

  showReview(review) {
    const { heading, status, body, controls } = this.parts;
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
    confirm.focus();
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
    let operation;
    try {
      operation = await this.transport.commit(review.token);
    } catch (commitError) {
      this.showCommitFailure(
        commitError?.details?.id
          ? `${commitError.message} Operation ${text(commitError.details.id)}.`
          : commitError?.message || 'The update was not applied.'
      );
      return null;
    }
    // Only the service's own succeeded status is success.
    if (operation?.status !== 'succeeded') {
      this.showCommitFailure(
        `The update did not finish (${text(operation?.status) || 'no status'}). Operation ${text(operation?.id)}.`
      );
      return null;
    }
    this.operation = operation;
    this.setState('succeeded');
    status.textContent = `Updated to ${text(operation.to_version)}.`;
    cancel.disabled = false;
    cancel.textContent = 'Close';
    confirm.remove();
    this.parts.confirm = null;
    if (typeof this.onSucceeded === 'function') await this.onSucceeded(operation, this);
    return operation;
  }

  showCommitFailure(message) {
    const { status, error, cancel } = this.parts;
    this.setState('commit-failed');
    status.textContent = '';
    error.textContent = message;
    cancel.disabled = false;
    cancel.focus();
  }

  // close ends the dialog unless a commit is still in flight. It reports
  // whether the dialog closed.
  close() {
    if (this.state === 'committing') {
      this.parts.status.textContent = `Still installing ${text(this.review?.to_version)}. Closing this would not stop the update; wait for it to finish.`;
      return false;
    }
    if (this.closed || !this.parts) return false;
    const { dialog } = this.parts;
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
  window.HomePackageUpgrade = Object.freeze({ openHomePackageUpgrade, pluginUpgradeTransport });
}
