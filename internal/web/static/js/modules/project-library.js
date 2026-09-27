function label(value) {
  return String(value == null || value === '' ? 'Unknown' : value).replaceAll('_', ' ');
}

function operationKey(action) {
  return `library-${action}-${globalThis.crypto.randomUUID()}`;
}

function node(tag, className, value) {
  const element = document.createElement(tag);
  if (className) element.className = className;
  if (value != null) element.textContent = String(value);
  return element;
}

async function payload(response) {
  try {
    return await response.json();
  } catch (_) {
    return {};
  }
}

export function libraryQuery({
  text = '',
  stage = '',
  status = '',
  format = '',
  rootID = '',
  connection = '',
  availability = '',
  priority = '',
  sort = 'name',
  direction = 'asc',
  cursor = ''
} = {}) {
  const query = new URLSearchParams({ page_size: '25', sort, direction });
  for (const [key, value] of Object.entries({
    text: text.trim(),
    stage,
    status,
    format,
    root_id: rootID,
    connection,
    availability,
    priority,
    cursor
  })) {
    if (value) query.set(key, value);
  }
  return query.toString();
}

// Only queue navigation (opaque entry IDs and a user-confirmed retry) lives in
// this browser tab. Every item still requires a fresh server review and a
// distinct user confirmation; storage never grants folder/creator authority.
const QUEUE_LIMIT = 100;
export function readActivationQueue(homeID, storage = globalThis.sessionStorage, now = Date.now()) {
  try {
    const saved = JSON.parse(storage?.getItem(`ori:library-queue:${homeID}`) || 'null');
    if (
      !saved ||
      saved.home_id !== homeID ||
      !Array.isArray(saved.ids) ||
      saved.ids.length < 2 ||
      saved.ids.length > QUEUE_LIMIT ||
      !Number.isInteger(saved.index) ||
      saved.index < 0 ||
      saved.index >= saved.ids.length ||
      !Number.isFinite(saved.created_at) ||
      now - saved.created_at > 24 * 60 * 60 * 1000 ||
      saved.created_at > now ||
      new Set(saved.ids).size !== saved.ids.length ||
      saved.ids.some(id => typeof id !== 'string' || !id || id.length > 160)
    )
      return null;
    const pending = saved.pending;
    if (
      pending &&
      (pending.id !== saved.ids[saved.index] ||
        ![pending.token, pending.key].every(
          value => typeof value === 'string' && value.length > 0 && value.length <= 160
        ))
    )
      return null;
    return {
      home_id: homeID,
      ids: [...saved.ids],
      index: saved.index,
      created_at: saved.created_at,
      pending: pending ? { id: pending.id, token: pending.token, key: pending.key } : null
    };
  } catch (_) {
    return null;
  }
}

// This panel belongs to the exact Home. No browser path, child workspace ID,
// source-file content, or model call is accepted as an authority input.
export class ProjectLibraryPanel {
  constructor({ workspaceId, program, fetchImpl = globalThis.fetch } = {}) {
    this.workspaceId = String(workspaceId || '').trim();
    this.program = program || {};
    this.fetchImpl = (...args) => fetchImpl(...args);
    this.state = null;
    this.cursor = '';
    this.rows = [];
    this.searchGeneration = 0;
    this.searchInFlight = false;
    this.busy = false;
    this.selectedProjects = new Set();
    this.queue = readActivationQueue(this.workspaceId);
    const fromOffer =
      new URLSearchParams(globalThis.location?.search || '').get('folder_offer_id') || '';
    this.offerID = fromOffer.length <= 160 ? fromOffer : '';
  }

  get panel() {
    return document.getElementById('projectLibraryPanel');
  }
  get readOnly() {
    return this.state?.provider_read_only !== false;
  }
  url(path = '') {
    return `/api/workspaces/${encodeURIComponent(this.workspaceId)}/assistant-program/library${path}`;
  }
  async request(path, options = {}) {
    const response = await this.fetchImpl(this.url(path), {
      headers: {
        Accept: 'application/json',
        ...(options.body ? { 'Content-Type': 'application/json' } : {})
      },
      ...options
    });
    const result = await payload(response);
    if (!response.ok) {
      throw new Error(
        typeof result.error === 'string'
          ? result.error
          : result.error?.message || result.message || `Library request failed (${response.status})`
      );
    }
    return result;
  }
  post(path, body = {}) {
    return this.request(path, { method: 'POST', body: JSON.stringify(body) });
  }
  status(message) {
    const element = document.getElementById('projectLibraryStatus');
    if (element) element.textContent = message;
  }
  async run(trigger, message, work) {
    if (this.busy) return;
    this.busy = true;
    this.panel?.setAttribute('aria-busy', 'true');
    if (trigger) trigger.disabled = true;
    this.status(message);
    try {
      await work();
    } catch (error) {
      this.status(error.message || 'The library could not be updated. Nothing was confirmed.');
    } finally {
      this.busy = false;
      this.renderQueueControls();
      this.panel?.setAttribute('aria-busy', 'false');
      if (trigger?.isConnected) {
        trigger.disabled =
          (trigger.id === 'projectLibraryAdd' &&
            (!this.state?.initialized ||
              this.readOnly ||
              (this.state.picker_available === false && !this.offerID))) ||
          (trigger.id === 'projectLibraryInitialize' && this.readOnly);
        if (
          !trigger.disabled &&
          !(trigger.id === 'projectLibraryInitialize' && this.state?.initialized)
        ) {
          trigger.focus();
        } else {
          const heading = document.getElementById('projectLibraryTitle');
          heading?.setAttribute('tabindex', '-1');
          heading?.focus();
        }
      }
    }
  }

  async init() {
    if (!this.workspaceId || !this.panel || !this.program?.is_station) return;
    this.panel.hidden = false;
    const jump = document.getElementById('projectLibraryJump');
    if (jump) jump.hidden = false;
    document
      .getElementById('projectLibraryInitialize')
      ?.addEventListener('click', event => void this.initialize(event.currentTarget));
    document
      .getElementById('projectLibraryAdd')
      ?.addEventListener('click', event => void this.addFolder(event.currentTarget));
    document.getElementById('projectLibrarySearchForm')?.addEventListener('submit', event => {
      event.preventDefault();
      void this.search(false);
    });
    document
      .getElementById('projectLibraryMore')
      ?.addEventListener('click', () => void this.search(true));
    document.getElementById('projectLibraryQueueStart')?.addEventListener('click', event => {
      void this.startQueue(event.currentTarget);
    });
    document.getElementById('projectLibraryQueueResume')?.addEventListener('click', event => {
      void this.continueQueue(event.currentTarget);
    });
    document.getElementById('projectLibraryQueueDiscard')?.addEventListener('click', event => {
      void this.discardQueue(event.currentTarget);
    });
    document
      .getElementById('projectLibraryMoreRoots')
      ?.addEventListener('click', event => void this.moreRoots(event.currentTarget));
    await this.refresh();
  }

  async refresh() {
    try {
      this.state = await this.request('/roots');
      const initialized = this.state.initialized === true;
      document.getElementById('projectLibrarySetup').hidden = initialized;
      document.getElementById('projectLibraryContent').hidden = !initialized;
      const add = document.getElementById('projectLibraryAdd');
      add.hidden = !initialized || this.readOnly;
      add.disabled = this.state.picker_available === false && !this.offerID;
      document.getElementById('projectLibraryInitialize').disabled = this.readOnly;
      if (!initialized) {
        this.status(
          this.readOnly
            ? 'The Home provider is unavailable. Saved library setup needs its exact installed package.'
            : 'No library yet. Review the saved project links before choosing a folder.'
        );
        return;
      }
      this.renderFormats();
      this.renderRoots();
      this.renderQueueControls();
      await this.renderResume();
      await this.search(false);
      if (!this.readOnly && this.state.picker_available === false && !this.offerID)
        this.status(
          'The native folder picker is unavailable here. You can still review approved folders for another scan; use Ori desktop to add a folder.'
        );
    } catch (error) {
      this.state = null;
      this.status(
        error.message || 'Could not load the Home library. Retry by reopening this Home.'
      );
      document.getElementById('projectLibraryAdd').hidden = true;
      document.getElementById('projectLibrarySetup').hidden = true;
      document.getElementById('projectLibraryContent').hidden = true;
    }
  }

  async renderResume() {
    const section = document.getElementById('projectLibraryResume');
    const container = document.getElementById('projectLibraryResumeCards');
    if (!section || !container) return;
    container.replaceChildren();
    let view;
    try {
      view = await this.request('/resume');
    } catch (_) {
      section.hidden = false;
      container.append(
        node('p', '', 'Resume is unavailable. Saved project details may still be readable below.')
      );
      return;
    }
    section.hidden = !view.cards?.length;
    for (const card of (view.cards || []).slice(0, 3)) {
      const item = node('article', 'project-library-resume-card');
      const heading = node('h4', '', card.name);
      const session = card.session;
      item.append(
        heading,
        node('p', '', `Saved goal: ${session.goal}`),
        node('p', '', session.recap ? `Your saved recap: ${session.recap}` : 'No recap saved yet.'),
        node(
          'p',
          '',
          card.project_next_action
            ? `Current saved project next action: ${card.project_next_action}`
            : session.next_action
              ? `Session’s saved next action: ${session.next_action}`
              : 'No next action saved.'
        ),
        node(
          'small',
          '',
          `User-authored note · saved ${new Date(session.updated_at).toLocaleDateString()}`
        )
      );
      const open = node('button', 'modern-btn modern-btn-secondary', `View ${card.name} session`);
      open.type = 'button';
      open.addEventListener('click', () => void this.details(card.entry_id, open));
      item.append(open);
      container.append(item);
    }
  }

  async moreRoots(trigger) {
    if (!this.state?.next_offset) return;
    await this.run(trigger, 'Loading more saved discovery folders…', async () => {
      const page = await this.request(`/roots?offset=${this.state.next_offset}`);
      if (
        page.revision !== this.state.revision ||
        page.total_roots !== this.state.total_roots ||
        page.provider_read_only !== this.state.provider_read_only
      ) {
        await this.refresh();
        this.status('Discovery folders changed. Start with the refreshed list.');
        return;
      }
      this.state.roots = [...(this.state.roots || []), ...(page.roots || [])];
      this.state.next_offset = page.next_offset || 0;
      this.renderRoots();
    });
  }

  renderFormats() {
    const filter = document.getElementById('projectLibraryFormat');
    if (!filter) return;
    const selected = filter.value;
    const all = node('option', '', 'All formats');
    all.value = '';
    const choices = (this.state?.formats || []).slice(0, 16).map(format => {
      const option = node('option', '', format.label);
      option.value = format.id;
      return option;
    });
    filter.replaceChildren(all, ...choices);
    filter.value = choices.some(choice => choice.value === selected) ? selected : '';
  }

  renderRoots() {
    const container = document.getElementById('projectLibraryRoots');
    container.replaceChildren();
    const roots = Array.isArray(this.state?.roots) ? this.state.roots : [];
    const filter = document.getElementById('projectLibraryRoot');
    if (filter) {
      const selected = filter.value;
      const all = node('option', '', 'All discovery folders');
      all.value = '';
      const choices = roots.map(root => {
        const option = node('option', '', root.path);
        option.value = root.id;
        return option;
      });
      filter.replaceChildren(all, ...choices);
      filter.value = choices.some(choice => choice.value === selected) ? selected : '';
    }
    for (const root of roots) {
      const card = node('article', 'project-library-root');
      const copy = node('div');
      copy.append(
        node('strong', '', root.path),
        node(
          'small',
          '',
          root.revoked_at
            ? 'Disconnected · historical entries retained'
            : root.needs_review
              ? 'Restore requires a new reviewed folder grant'
              : root.last_scan
                ? `${label(root.last_scan.status)} · ${root.last_scan.scope || 'root'} · ${root.last_scan.entries_seen} names checked · ${root.last_scan.skipped_entries} skipped${root.last_scan.partial_reason ? ` · ${label(root.last_scan.partial_reason)}` : ''}`
                : 'Connected · not scanned yet'
        )
      );
      const actions = node('div', 'project-library-root-actions');
      if (!root.revoked_at && !root.needs_review && !this.readOnly) {
        const scan = node('button', 'modern-btn modern-btn-secondary', 'Review scan');
        scan.type = 'button';
        scan.addEventListener('click', () => void this.scanRoot(root, scan));
        actions.append(scan);
        if (root.has_scopes) {
          const narrower = node(
            'button',
            'modern-btn modern-btn-secondary',
            'Review smaller folder'
          );
          narrower.type = 'button';
          narrower.addEventListener('click', () => void this.knownScopes(root, narrower));
          actions.append(narrower);
        }
      }
      if (!root.revoked_at) {
        const revoke = node('button', 'modern-btn modern-btn-secondary', 'Disconnect');
        revoke.type = 'button';
        revoke.addEventListener('click', () => void this.revokeRoot(root, revoke));
        actions.append(revoke);
      }
      card.append(copy, actions);
      container.append(card);
    }
    document.getElementById('projectLibraryRootCount').textContent =
      `${roots.length} of ${this.state?.total_roots || 0} discovery folders`;
    document.getElementById('projectLibraryMoreRoots').hidden = !this.state?.next_offset;
    if (!roots.length)
      container.append(
        node(
          'p',
          'project-library-empty',
          'No discovery folders connected. Add one only when you want to review its metadata.'
        )
      );
  }

  async search(more) {
    if (more && (this.searchInFlight || !this.cursor)) return;
    const tbody = document.getElementById('projectLibraryRows');
    if (!more) {
      this.searchGeneration++;
      this.cursor = '';
      this.rows = [];
      tbody.replaceChildren();
    }
    const generation = this.searchGeneration;
    const moreButton = document.getElementById('projectLibraryMore');
    if (more) {
      this.searchInFlight = true;
      moreButton.disabled = true;
    }
    const params = libraryQuery({
      text: document.getElementById('projectLibrarySearch')?.value || '',
      stage: document.getElementById('projectLibraryStage')?.value || '',
      status: document.getElementById('projectLibraryStatusFilter')?.value || '',
      format: document.getElementById('projectLibraryFormat')?.value || '',
      rootID: document.getElementById('projectLibraryRoot')?.value || '',
      connection: document.getElementById('projectLibraryConnection')?.value || '',
      availability: document.getElementById('projectLibraryAvailability')?.value || '',
      priority: document.getElementById('projectLibraryPriority')?.value || '',
      sort: document.getElementById('projectLibrarySort')?.value || 'name',
      direction: document.getElementById('projectLibraryDirection')?.value || 'asc',
      cursor: more ? this.cursor : ''
    });
    try {
      const page = await this.request(`/projects?${params}`);
      if (generation !== this.searchGeneration) return; // A newer search owns the visible results.
      this.rows.push(...(page.rows || []));
      this.cursor = page.next_cursor || '';
      this.renderRows();
      this.renderQueueControls();
      moreButton.hidden = !this.cursor;
      document.getElementById('projectLibraryCount').textContent =
        `${this.rows.length} of ${page.total} projects`;
      this.status(
        this.readOnly
          ? 'Saved records are readable; the Home provider is unavailable for new changes.'
          : page.total
            ? 'Saved observations only. Open a row to inspect its source and notes.'
            : 'No projects match. Try another filter or review a folder scan.'
      );
    } catch (error) {
      if (generation !== this.searchGeneration) return;
      this.cursor = '';
      moreButton.hidden = true;
      this.status(error.message || 'Could not load this page. Refresh the library and retry.');
    } finally {
      if (more) {
        this.searchInFlight = false;
        moreButton.disabled = false;
      }
    }
  }

  renderRows() {
    const tbody = document.getElementById('projectLibraryRows');
    tbody.replaceChildren();
    for (const row of this.rows) {
      const tr = node('tr');
      const pick = node('td');
      if (!this.readOnly && row.connection === 'catalog_only') {
        const checkbox = node('input');
        checkbox.type = 'checkbox';
        checkbox.checked = this.selectedProjects.has(row.id);
        checkbox.setAttribute('aria-label', `Select ${row.name} for serial project review`);
        checkbox.addEventListener('change', () => {
          if (checkbox.checked && this.selectedProjects.size >= QUEUE_LIMIT) {
            checkbox.checked = false;
            this.status(`Review at most ${QUEUE_LIMIT} projects per queue.`);
            return;
          }
          if (checkbox.checked) this.selectedProjects.add(row.id);
          else this.selectedProjects.delete(row.id);
          this.renderQueueControls();
        });
        pick.append(checkbox);
      }
      const name = node('td');
      name.append(
        node('strong', '', row.name),
        node('small', '', row.next_action || 'No next action saved')
      );
      const stage = node('td');
      stage.append(node('span', '', label(row.stage)), node('small', '', label(row.status)));
      const connection = node('td', '', label(row.connection));
      const observed = node('td');
      observed.append(
        node('span', '', label(row.last_observed_availability)),
        node(
          'small',
          '',
          row.last_scanned_at
            ? `Scanned ${new Date(row.last_scanned_at).toLocaleDateString()}`
            : 'Not scanned'
        )
      );
      const action = node('td');
      const button = node('button', 'modern-btn modern-btn-secondary', 'Details');
      button.type = 'button';
      button.setAttribute('aria-label', `Review ${row.name}`);
      button.addEventListener('click', () => void this.details(row.id, button));
      action.append(button);
      tr.append(pick, name, stage, connection, observed, action);
      tbody.append(tr);
    }
  }

  renderQueueControls() {
    const message = document.getElementById('projectLibraryQueueStatus');
    const start = document.getElementById('projectLibraryQueueStart');
    const resume = document.getElementById('projectLibraryQueueResume');
    const discard = document.getElementById('projectLibraryQueueDiscard');
    if (!message || !start || !resume || !discard) return;
    const pending = this.queue;
    message.textContent = pending
      ? `${pending.index} of ${pending.ids.length} handled · each remaining song needs its own review. Already connected songs stay connected.`
      : this.selectedProjects.size
        ? `${this.selectedProjects.size} selected · no project will be created until each one is confirmed.`
        : 'Choose at least two catalog-only projects to review one at a time.';
    start.disabled = this.busy || this.readOnly || !!pending || this.selectedProjects.size < 2;
    resume.hidden = discard.hidden = !pending;
    resume.disabled = this.busy || this.readOnly;
    discard.disabled = this.busy;
  }

  saveQueue() {
    const key = `ori:library-queue:${this.workspaceId}`;
    try {
      if (this.queue) globalThis.sessionStorage.setItem(key, JSON.stringify(this.queue));
      else globalThis.sessionStorage.removeItem(key);
    } catch (_) {
      throw new Error(
        'This browser cannot retain the review queue. Use one-project setup instead.'
      );
    }
    this.renderQueueControls();
  }

  async startQueue(trigger) {
    if (
      this.queue ||
      this.readOnly ||
      this.selectedProjects.size < 2 ||
      this.selectedProjects.size > QUEUE_LIMIT
    )
      return;
    try {
      this.queue = {
        home_id: this.workspaceId,
        ids: [...this.selectedProjects],
        index: 0,
        created_at: Date.now(),
        pending: null
      };
      this.saveQueue();
      this.selectedProjects.clear();
      this.renderRows();
      await this.continueQueue(document.getElementById('projectLibraryQueueResume') || trigger);
    } catch (error) {
      this.queue = null;
      this.status(error.message);
      this.renderQueueControls();
    }
  }

  async discardQueue(trigger) {
    if (
      !this.queue ||
      this.busy ||
      !(await this.confirm(
        'Discard this review queue?',
        [
          'This removes only the local queue. Any project you separately confirmed stays connected.'
        ],
        'Discard queue',
        trigger
      ))
    )
      return;
    try {
      this.queue = null;
      this.saveQueue();
      this.status('Local review queue discarded. Connected projects were not changed.');
    } catch (error) {
      this.status(error.message);
    }
  }

  queueChoice(name, position, count, trigger) {
    return new Promise(resolve => {
      const dialog = node('dialog', 'assistant-program-hire-dialog project-library-dialog');
      const form = node('form');
      form.method = 'dialog';
      const heading = node('h2', '', `Song ${position} of ${count} · ${name}`);
      heading.id = operationKey('queue-heading');
      dialog.setAttribute('aria-labelledby', heading.id);
      const actions = node('div', 'assistant-program-dialog-actions');
      const pause = node('button', 'modern-btn modern-btn-secondary', 'Pause queue');
      const skip = node('button', 'modern-btn modern-btn-secondary', 'Skip this song');
      const proceed = node('button', 'modern-btn modern-btn-primary', 'Review this song');
      pause.type = skip.type = 'button';
      proceed.type = 'submit';
      let choice = 'pause';
      pause.addEventListener('click', () => dialog.close());
      skip.addEventListener('click', () => {
        choice = 'skip';
        dialog.close();
      });
      form.addEventListener('submit', () => {
        choice = 'review';
      });
      actions.append(pause, skip, proceed);
      form.append(
        heading,
        node(
          'p',
          '',
          'This song needs its own authoritative-file choice and final confirmation. Skipping creates nothing; pausing keeps the remaining IDs for this tab.'
        ),
        actions
      );
      dialog.append(form);
      document.body.append(dialog);
      dialog.addEventListener(
        'close',
        () => {
          dialog.remove();
          trigger?.focus?.();
          resolve(choice);
        },
        { once: true }
      );
      dialog.showModal();
      pause.focus();
    });
  }

  async continueQueue(trigger) {
    if (!this.queue || this.readOnly || this.busy) return;
    await this.run(trigger, 'Checking the next saved project before review…', async () => {
      while (this.queue && this.queue.index < this.queue.ids.length) {
        const id = this.queue.ids[this.queue.index];
        const path = `/projects/${encodeURIComponent(id)}`;
        try {
          const detail = await this.request(path);
          if (detail.row.connection === 'connected') {
            this.queue.pending = null;
            this.queue.index++;
          } else if (this.queue.pending) {
            await this.post(`${path}/activation/commit`, {
              review_token: this.queue.pending.token,
              idempotency_key: this.queue.pending.key,
              confirm: true
            });
            this.queue.pending = null;
            this.queue.index++;
            await this.refresh();
          } else {
            const eligibility = await this.request(`${path}/activation`);
            if (!['review_available', 'file_choice_required'].includes(eligibility.state)) {
              this.status(
                `${detail.row.name}: ${eligibility.reason || 'Project setup needs a fresh review.'} Queue paused.`
              );
              return;
            }
            const action = await this.queueChoice(
              detail.row.name,
              this.queue.index + 1,
              this.queue.ids.length,
              trigger
            );
            if (action === 'pause') {
              this.status('Review queue paused; no other song was connected.');
              return;
            }
            if (action === 'skip') {
              this.queue.index++;
            } else {
              const input = await this.activationInput(detail, eligibility, trigger);
              if (!input) {
                this.status('Review queue paused before project setup.');
                return;
              }
              const review = await this.post(`${path}/activation/review`, input);
              if (
                !(await this.confirm(
                  'Connect this one project?',
                  [
                    `Queue item ${this.queue.index + 1} of ${this.queue.ids.length}: ${review.workspace_name}`,
                    `Authoritative file: ${review.project_file}`,
                    `Installed project roles: ${(review.project_role_labels || []).join(', ')}`,
                    review.statement,
                    'Other queued songs remain untouched until separately confirmed.'
                  ],
                  'Connect project',
                  trigger
                ))
              ) {
                this.status('Review queue paused without connecting this song.');
                return;
              }
              this.queue.pending = {
                id,
                token: review.token,
                key: operationKey('queue-activation')
              };
              this.saveQueue(); // Persist the exact retry *before* crossing the creator boundary.
              await this.post(`${path}/activation/commit`, {
                review_token: review.token,
                idempotency_key: this.queue.pending.key,
                confirm: true
              });
              this.queue.pending = null;
              this.queue.index++;
              await this.refresh();
            }
          }
          if (this.queue.index === this.queue.ids.length) {
            this.queue = null;
            this.saveQueue();
            this.status('Review queue complete. Only separately confirmed songs were connected.');
            if (typeof document !== 'undefined')
              document.getElementById('projectLibraryStatus')?.focus();
            return;
          }
          this.saveQueue();
        } catch (error) {
          this.status(
            `${error.message || 'Project setup needs a fresh review.'} Queue paused; previously confirmed projects remain connected.`
          );
          return;
        }
      }
    });
  }

  confirm(title, lines, action, trigger) {
    return new Promise(resolve => {
      const dialog = node('dialog', 'assistant-program-hire-dialog project-library-dialog');
      const form = node('form');
      form.method = 'dialog';
      const heading = node('h2', '', title);
      heading.id = operationKey('heading');
      dialog.setAttribute('aria-labelledby', heading.id);
      const list = node('ul');
      for (const line of lines) list.append(node('li', '', line));
      const actions = node('div', 'assistant-program-dialog-actions');
      const cancel = node('button', 'modern-btn modern-btn-secondary', 'Cancel');
      cancel.type = 'button';
      const accept = node('button', 'modern-btn modern-btn-primary', action);
      accept.type = 'submit';
      actions.append(cancel, accept);
      form.append(heading, list, actions);
      dialog.append(form);
      document.body.append(dialog);
      let accepted = false;
      cancel.addEventListener('click', () => dialog.close());
      form.addEventListener('submit', () => {
        accepted = true;
      });
      dialog.addEventListener(
        'close',
        () => {
          dialog.remove();
          trigger?.focus?.();
          resolve(accepted);
        },
        { once: true }
      );
      dialog.showModal();
      cancel.focus();
    });
  }

  async initialize(trigger) {
    await this.run(trigger, 'Preparing your saved project links for review…', async () => {
      const review = await this.post('/initialize/review');
      if (
        !(await this.confirm(
          'Start a Home project library?',
          [
            `${review.linked_count} exact linked projects will be represented without creating a workspace or scanning a folder.`,
            'Saved user notes are copied once; existing project files stay unchanged.'
          ],
          'Start library',
          trigger
        ))
      )
        return;
      await this.post('/initialize/commit', {
        review_token: review.token,
        idempotency_key: operationKey('initialize'),
        confirm: true
      });
      await this.refresh();
    });
    if (this.offerID && this.state?.initialized === true)
      await this.addFolder(trigger, this.offerID);
  }

  async addFolder(trigger, offerID = this.offerID) {
    await this.run(trigger, 'Preparing the folder selection…', async () => {
      let picked;
      if (offerID) {
        try {
          picked = await this.post('/roots/pick-offer', { offer_id: offerID });
        } catch (_) {
          if (
            !(await this.confirm(
              'Choose the folder again?',
              [
                'The original selection expired or changed. Your Home remains ready, but a new native selection is required.'
              ],
              'Open folder picker',
              trigger
            ))
          )
            return;
        }
      }
      if (!picked) picked = await this.post('/roots/pick');
      const review = await this.post('/roots/review', {
        selection_token: picked.selection_token,
        if_revision: this.state.revision
      });
      if (
        !(await this.confirm(
          'Connect this discovery folder?',
          [
            review.root_path,
            review.scope,
            'Connecting permits metadata-only reads; it does not start a scan, open a session, or activate a project.'
          ],
          'Connect folder',
          trigger
        ))
      )
        return;
      const receipt = await this.post('/roots/commit', {
        review_token: review.token,
        idempotency_key: operationKey('root'),
        confirm: true
      });
      this.offerID = '';
      if (offerID && globalThis.history?.replaceState && globalThis.location?.href) {
        const url = new URL(globalThis.location.href);
        url.searchParams.delete('folder_offer_id');
        globalThis.history.replaceState(globalThis.history.state, '', url);
      }
      await this.refresh();
      if (
        await this.confirm(
          'Scan the folder now?',
          [
            'One bounded scan will inspect folder names and project markers only. Source files will not be opened or changed.'
          ],
          'Review scan',
          trigger
        )
      )
        await this.scanRootFlow(receipt.root_id, trigger);
    });
  }

  async knownScopes(root, trigger) {
    await this.run(trigger, 'Loading previously observed folders…', async () => {
      const dialog = node('dialog', 'assistant-program-hire-dialog project-library-dialog');
      const heading = node('h2', '', 'Choose a smaller folder');
      heading.id = operationKey('scope-heading');
      dialog.setAttribute('aria-labelledby', heading.id);
      const explanation = node(
        'p',
        '',
        'These names were observed during an earlier scan. Choosing one only opens a separate metadata-scan review; unavailable folders cannot be scanned.'
      );
      const choices = node('ul', 'project-library-scope-list');
      const note = node('p', '', '');
      note.setAttribute('role', 'status');
      const actions = node('div', 'assistant-program-dialog-actions');
      const more = node('button', 'modern-btn modern-btn-secondary', 'More folders');
      more.type = 'button';
      more.hidden = true;
      const close = node('button', 'modern-btn modern-btn-secondary', 'Close');
      close.type = 'button';
      close.addEventListener('click', () => dialog.close());
      actions.append(more, close);
      dialog.append(heading, explanation, choices, note, actions);
      let nextOffset = 0;
      const load = async offset => {
        const page = await this.request(
          `/roots/${encodeURIComponent(root.id)}/scopes?${new URLSearchParams({ if_revision: String(this.state.revision), offset: String(offset) })}`
        );
        for (const choice of page.rows || []) {
          const item = node('li');
          const select = node('button', 'modern-btn modern-btn-secondary', choice.relative_folder);
          select.type = 'button';
          select.addEventListener('click', () => {
            dialog.close();
            void this.scanRoot(root, trigger, choice.id);
          });
          item.append(select);
          choices.append(item);
        }
        nextOffset = page.next_offset || 0;
        more.hidden = !nextOffset;
        note.textContent = `${choices.children.length} of ${page.total} previously observed folders`;
      };
      await load(0);
      more.addEventListener('click', async () => {
        more.disabled = true;
        try {
          await load(nextOffset);
        } catch (error) {
          note.textContent = `${error.message || 'The library changed.'} Close and retry.`;
          more.hidden = true;
        } finally {
          more.disabled = false;
        }
      });
      document.body.append(dialog);
      dialog.addEventListener(
        'close',
        () => {
          dialog.remove();
          trigger?.focus?.();
        },
        { once: true }
      );
      dialog.showModal();
      close.focus();
    });
  }

  async scanRoot(root, trigger, scopeID = '') {
    await this.run(trigger, 'Preparing a one-time metadata scan review…', () =>
      this.scanRootFlow(root.id, trigger, scopeID)
    );
  }

  async scanRootFlow(rootID, trigger, scopeID = '') {
    const review = await this.post(`/roots/${encodeURIComponent(rootID)}/scans/review`, {
      if_revision: this.state.revision,
      ...(scopeID ? { scope_id: scopeID } : {})
    });
    if (
      !(await this.confirm(
        scopeID ? 'Scan this selected folder once?' : 'Scan this discovery folder once?',
        [
          review.root_path,
          ...(scopeID ? [review.relative_folder] : []),
          review.scope,
          `At most ${review.max_entries} entries. Partial or skipped coverage will remain visible.`,
          ...(review.interrupts_prior_scan
            ? ['This also marks an interrupted prior scan as interrupted.']
            : [])
        ],
        'Scan metadata',
        trigger
      ))
    )
      return;
    const receipt = await this.post(`/roots/${encodeURIComponent(rootID)}/scans/commit`, {
      review_token: review.token,
      idempotency_key: operationKey('scan'),
      confirm: true,
      ...(scopeID ? { scope_id: scopeID } : {})
    });
    await this.refresh();
    this.status(
      `${label(receipt.status)} scan · ${receipt.entries_seen} entries seen · ${receipt.skipped_links + receipt.skipped_other} skipped${receipt.partial_reason ? ` · ${receipt.partial_reason}` : ''}.`
    );
  }

  async revokeRoot(root, trigger) {
    await this.run(trigger, 'Preparing root disconnect impact…', async () => {
      const review = await this.post(`/roots/${encodeURIComponent(root.id)}/revoke/review`, {
        if_revision: this.state.revision
      });
      if (
        !(await this.confirm(
          'Disconnect this discovery folder?',
          [
            root.path,
            `${review.entry_count} catalog records retain their historical notes.`,
            'Further scans stop. Already connected projects and external source files remain unchanged.'
          ],
          'Disconnect folder',
          trigger
        ))
      )
        return;
      await this.post(`/roots/${encodeURIComponent(root.id)}/revoke/commit`, {
        review_token: review.token,
        idempotency_key: operationKey('revoke'),
        confirm: true
      });
      await this.refresh();
    });
  }

  async details(entryID, trigger) {
    await this.run(trigger, 'Loading this saved project…', async () => {
      const detail = await this.request(`/projects/${encodeURIComponent(entryID)}`);
      // Eligibility is an inert, fresh installed-provider/source check. A
      // failed eligibility read must not hide saved notes or offer setup.
      const activation = await this.request(
        `/projects/${encodeURIComponent(entryID)}/activation`
      ).catch(() => ({
        reason: 'Setup options cannot be checked right now. Saved notes remain available.'
      }));
      const sessions = await this.request(
        `/projects/${encodeURIComponent(entryID)}/sessions`
      ).catch(() => null);
      const dialog = node(
        'dialog',
        'assistant-program-hire-dialog project-library-dialog project-library-detail-dialog'
      );
      const heading = node('h2', '', detail.row.name);
      heading.id = operationKey('detail');
      dialog.setAttribute('aria-labelledby', heading.id);
      const summary = node(
        'p',
        '',
        `${label(detail.row.stage)} · ${label(detail.row.status)} · ${label(detail.row.connection)} · ${label(detail.row.availability || detail.row.last_observed_availability)}`
      );
      const sourceList = node('ul');
      for (const source of detail.sources || [])
        sourceList.append(
          node(
            'li',
            '',
            `${source.relative_folder || 'Selected root'} · ${label(source.format)} · ${label(source.last_observed_availability)}${source.total_alternates ? ` · ${source.total_alternates} project-file choices (selection requires a separate review)` : ''}`
          )
        );
      if (!sourceList.children.length)
        sourceList.append(
          node('li', '', 'An exact existing link is recorded; no folder was scanned.')
        );
      const sourceCount =
        (detail.total_sources || 0) > (detail.sources || []).length
          ? node(
              'p',
              '',
              `Showing ${(detail.sources || []).length} of ${detail.total_sources} observed sources. Additional source history is not shown here.`
            )
          : null;
      const next = node('p', '', `Next action: ${detail.fields.next_action || 'Not set'}`);
      const setup = node('section', 'project-library-setup-preview');
      setup.append(node('h3', '', 'Project setup'), node('p', '', activation.reason));
      if (activation.state === 'project_provider_unavailable') {
        const plugins = node('a', '', 'Review integrations on the Plugins page');
        plugins.href = '/plugins';
        setup.append(plugins);
      }
      if (activation.project_role_labels?.length)
        setup.append(
          node('p', '', `Installed project roles: ${activation.project_role_labels.join(', ')}`)
        );
      if (activation.project_files?.length)
        setup.append(
          node(
            'p',
            '',
            `Observed project files: ${activation.project_files.join(', ')}. File selection and project creation require a separate review.`
          )
        );
      const sessionPanel = node('section', 'project-library-session-history');
      sessionPanel.append(node('h3', '', 'Studio sessions'));
      const sessionList = node('ul');
      if (!sessions)
        sessionPanel.append(node('p', '', 'Saved session history is unavailable right now.'));
      else if (!sessions.total)
        sessionPanel.append(
          node(
            'p',
            '',
            'No saved goals yet. Plan a session without connecting a project or starting a task.'
          )
        );
      else {
        const appendSessions = records => {
          for (const record of records) {
            const item = node('li');
            item.append(
              node('strong', '', record.goal),
              node(
                'small',
                '',
                `Saved by ${record.author} · ${new Date(record.updated_at).toLocaleDateString()}`
              ),
              node('p', '', record.recap ? `User recap: ${record.recap}` : 'No recap saved yet.')
            );
            if (record.next_action)
              item.append(node('p', '', `Saved session next action: ${record.next_action}`));
            const full = node('button', 'modern-btn modern-btn-secondary', 'View full session');
            full.type = 'button';
            full.addEventListener('click', () => {
              dialog.close();
              void this.fullStudioSession(detail, record, trigger);
            });
            item.append(full);
            if (!record.recap && !this.readOnly) {
              const wrap = node('button', 'modern-btn modern-btn-secondary', 'Wrap up session');
              wrap.type = 'button';
              wrap.addEventListener('click', () => {
                dialog.close();
                this.sessionForm(detail, record, trigger);
              });
              item.append(wrap);
            }
            sessionList.append(item);
          }
        };
        appendSessions(sessions.rows);
        sessionPanel.append(sessionList);
        if (sessions.next_offset) {
          const more = node('button', 'modern-btn modern-btn-secondary', 'Load older sessions');
          more.type = 'button';
          let offset = sessions.next_offset;
          more.addEventListener(
            'click',
            () =>
              void this.run(more, 'Loading older studio sessions…', async () => {
                const page = await this.request(
                  `/projects/${encodeURIComponent(entryID)}/sessions?revision=${sessions.revision}&offset=${offset}`
                );
                appendSessions(page.rows);
                offset = page.next_offset;
                more.hidden = !offset;
              })
          );
          sessionPanel.append(more);
        }
      }
      const actions = node('div', 'assistant-program-dialog-actions');
      const close = node('button', 'modern-btn modern-btn-secondary', 'Close');
      close.type = 'button';
      close.addEventListener('click', () => dialog.close());
      actions.append(close);
      if (
        activation.state === 'connected' &&
        detail.row.connection === 'connected' &&
        activation.workspace_id
      ) {
        const open = node('a', 'modern-btn modern-btn-secondary', 'Open connected workspace');
        open.href = `/workspaces/${encodeURIComponent(activation.workspace_id)}`;
        actions.append(open);
      }
      if (
        !this.readOnly &&
        ['review_available', 'file_choice_required'].includes(activation.state)
      ) {
        const activate = node('button', 'modern-btn modern-btn-primary', 'Review project setup');
        activate.type = 'button';
        activate.addEventListener('click', () => {
          dialog.close();
          this.activationForm(detail, activation, trigger);
        });
        actions.append(activate);
      }
      if (!this.readOnly) {
        const plan = node('button', 'modern-btn modern-btn-secondary', 'Plan a session');
        plan.type = 'button';
        plan.addEventListener('click', () => {
          dialog.close();
          this.sessionForm(detail, null, trigger);
        });
        actions.append(plan);
        const edit = node('button', 'modern-btn modern-btn-primary', 'Edit project notes');
        edit.type = 'button';
        edit.addEventListener('click', () => {
          dialog.close();
          void this.editFields(detail, trigger);
        });
        actions.append(edit);
      }
      dialog.append(
        heading,
        summary,
        sourceList,
        ...(sourceCount ? [sourceCount] : []),
        next,
        setup,
        sessionPanel,
        actions
      );
      document.body.append(dialog);
      dialog.addEventListener(
        'close',
        () => {
          dialog.remove();
          if (!document.querySelector('dialog[open]')) trigger?.focus?.();
        },
        { once: true }
      );
      dialog.showModal();
      close.focus();
    });
  }

  async fullStudioSession(detail, session, trigger) {
    await this.run(trigger, 'Loading the saved studio session…', async () => {
      const record = await this.request(
        `/projects/${encodeURIComponent(detail.row.id)}/sessions/${encodeURIComponent(session.id)}`
      );
      const dialog = node(
        'dialog',
        'assistant-program-hire-dialog project-library-dialog project-library-detail-dialog'
      );
      const heading = node('h2', '', `Studio session · ${detail.row.name}`);
      heading.id = operationKey('full-session');
      dialog.setAttribute('aria-labelledby', heading.id);
      const facts = node('div', 'project-library-session-history');
      facts.append(
        node('p', '', `User goal: ${record.goal}`),
        node('p', '', `Desired outcome: ${record.outcome || 'Not specified'}`),
        node(
          'p',
          '',
          `Available time: ${record.time_minutes ? `${record.time_minutes} minutes` : 'Not specified'}`
        ),
        node('p', '', `Planned date: ${record.planned_date || 'Not specified'}`),
        node('p', '', `User-entered actual date: ${record.actual_date || 'Not specified'}`),
        node('p', '', `Saved recap: ${record.recap || 'No recap yet'}`),
        node('p', '', `Session next action: ${record.next_action || 'Not set'}`),
        node(
          'small',
          '',
          `User-authored by ${record.author} · saved ${new Date(record.updated_at).toLocaleDateString()}`
        )
      );
      for (const [title, values] of [
        ['Decisions', record.decisions || []],
        ['Blockers', record.blockers || []]
      ]) {
        if (!values.length) continue;
        const list = node('ul');
        for (const value of values) list.append(node('li', '', value));
        facts.append(node('h3', '', title), list);
      }
      const close = node('button', 'modern-btn modern-btn-secondary', 'Close');
      close.type = 'button';
      close.addEventListener('click', () => dialog.close());
      dialog.append(heading, facts, close);
      document.body.append(dialog);
      dialog.addEventListener(
        'close',
        () => {
          dialog.remove();
          if (!document.querySelector('dialog[open]')) trigger?.focus?.();
        },
        { once: true }
      );
      dialog.showModal();
      close.focus();
    });
  }

  activationForm(detail, eligibility, trigger) {
    void this.activationInput(detail, eligibility, trigger).then(input => {
      if (input) void this.saveActivation(detail, input, trigger);
    });
  }

  activationInput(detail, eligibility, trigger) {
    return new Promise(resolve => {
      const dialog = node('dialog', 'assistant-program-hire-dialog project-library-dialog');
      const form = node('form');
      const heading = node('h2', '', `Set up ${detail.row.name}`);
      heading.id = operationKey('activate-heading');
      dialog.setAttribute('aria-labelledby', heading.id);
      const fields = node('div', 'project-library-fields');
      const name = node('input', 'form-control');
      name.required = true;
      name.maxLength = 128;
      name.value = detail.row.name.slice(0, 128);
      const nameLabel = node('label', '', 'Project workspace name');
      nameLabel.append(name);
      fields.append(nameLabel);
      const choice = node('select', 'form-select');
      choice.required = true;
      if ((eligibility.project_files || []).length !== 1) {
        const placeholder = node('option', '', 'Choose the authoritative file…');
        placeholder.value = '';
        placeholder.disabled = true;
        placeholder.selected = true;
        choice.append(placeholder);
      }
      for (const filename of eligibility.project_files || []) {
        const option = node('option', '', filename);
        option.value = filename;
        choice.append(option);
      }
      const fileLabel = node('label', '', 'Authoritative project file');
      fileLabel.append(choice);
      fields.append(fileLabel);
      const actions = node('div', 'assistant-program-dialog-actions');
      const cancel = node('button', 'modern-btn modern-btn-secondary', 'Cancel');
      cancel.type = 'button';
      cancel.addEventListener('click', () => dialog.close());
      const review = node('button', 'modern-btn modern-btn-primary', 'Review this project');
      review.type = 'submit';
      actions.append(cancel, review);
      form.append(
        heading,
        node(
          'p',
          '',
          'One saved song only. The selected file remains in place. No agent, live connection or file-opening action is granted here.'
        ),
        fields,
        actions
      );
      dialog.append(form);
      document.body.append(dialog);
      let input = null;
      dialog.addEventListener(
        'close',
        () => {
          dialog.remove();
          if (!document.querySelector('dialog[open]')) trigger?.focus?.();
          resolve(input);
        },
        { once: true }
      );
      form.addEventListener('submit', event => {
        event.preventDefault();
        input = {
          workspace_name: name.value.trim(),
          project_file: choice.value,
          if_revision: detail.revision
        };
        dialog.close();
      });
      dialog.showModal();
      choice.focus();
    });
  }

  async saveActivation(detail, input, trigger) {
    await this.run(trigger, 'Preparing one project for review…', async () => {
      const path = `/projects/${encodeURIComponent(detail.row.id)}/activation`;
      const review = await this.post(`${path}/review`, input);
      if (
        !(await this.confirm(
          'Connect this one project?',
          [
            `Project: ${review.workspace_name}`,
            `Authoritative file: ${review.project_file}`,
            `Installed project roles: ${(review.project_role_labels || []).join(', ')}`,
            review.statement,
            'No other catalog projects are created. Staffing and live project access are separate choices.'
          ],
          'Connect project',
          trigger
        ))
      )
        return;
      const result = await this.post(`${path}/commit`, {
        review_token: review.token,
        idempotency_key: operationKey('activation'),
        confirm: true
      });
      await this.refresh();
      this.status(
        'One project connected. Open its workspace to review project roles and File-only setup.'
      );
      const dialog = node('dialog', 'assistant-program-hire-dialog project-library-dialog');
      const form = node('form');
      const heading = node('h2', '', `${review.workspace_name} is connected`);
      heading.id = operationKey('connected-project');
      dialog.setAttribute('aria-labelledby', heading.id);
      const open = node('a', 'modern-btn modern-btn-primary', 'Open project workspace');
      open.href = `/workspaces/${encodeURIComponent(result.workspace_id)}`;
      const close = node('button', 'modern-btn modern-btn-secondary', 'Stay in library');
      close.type = 'button';
      close.addEventListener('click', () => dialog.close());
      const actions = node('div', 'assistant-program-dialog-actions');
      actions.append(close, open);
      form.append(
        heading,
        node(
          'p',
          '',
          'The saved folder is referenced, not copied or launched. No project role was staffed and no live access was granted; review those separately in the project workspace.'
        ),
        actions
      );
      dialog.append(form);
      document.body.append(dialog);
      dialog.addEventListener(
        'close',
        () => {
          dialog.remove();
          if (!document.querySelector('dialog[open]')) trigger?.focus?.();
        },
        { once: true }
      );
      dialog.showModal();
      close.focus();
    });
  }

  sessionForm(detail, session, trigger) {
    const wrapping = Boolean(session);
    const dialog = node('dialog', 'assistant-program-hire-dialog project-library-dialog');
    const form = node('form');
    const heading = node('h2', '', wrapping ? 'Wrap up studio session' : 'Plan a studio session');
    heading.id = operationKey('studio-heading');
    dialog.setAttribute('aria-labelledby', heading.id);
    const fields = node('div', 'project-library-fields');
    const notes = node('textarea', 'form-control');
    notes.required = true;
    notes.maxLength = wrapping ? 2000 : 500;
    notes.rows = wrapping ? 5 : 3;
    const extra = node(wrapping ? 'input' : 'textarea', 'form-control');
    extra.maxLength = wrapping ? 240 : 500;
    if (!wrapping) extra.rows = 2;
    const date = node('input', 'form-control');
    date.type = 'date';
    const labeled = (text, input) => {
      const labelNode = node('label', '', text);
      labelNode.append(input);
      fields.append(labelNode);
    };
    labeled(wrapping ? 'Your recap' : 'Session goal', notes);
    labeled(wrapping ? 'Proposed next action' : 'Desired outcome', extra);
    labeled(wrapping ? 'Actual date (optional)' : 'Planned date (optional)', date);
    let time;
    let update;
    let decisions;
    let blockers;
    if (wrapping) {
      decisions = node('textarea', 'form-control');
      blockers = node('textarea', 'form-control');
      for (const control of [decisions, blockers]) {
        control.rows = 2;
        control.maxLength = 4096;
      }
      labeled('Decisions (optional, one per line; up to 16)', decisions);
      labeled('Blockers (optional, one per line; up to 16)', blockers);
      for (const control of [decisions, blockers, extra])
        control.addEventListener('input', () => control.setCustomValidity(''));
      update = node('input');
      update.type = 'checkbox';
      labeled(
        'Also update this project’s saved next action (separate from the session note)',
        update
      );
    } else {
      time = node('input', 'form-control');
      time.type = 'number';
      time.min = '1';
      time.max = '480';
      time.placeholder = 'Optional';
      labeled('Available minutes (optional, 1–480)', time);
    }
    const actions = node('div', 'assistant-program-dialog-actions');
    const cancel = node('button', 'modern-btn modern-btn-secondary', 'Cancel');
    cancel.type = 'button';
    cancel.addEventListener('click', () => dialog.close());
    const review = node('button', 'modern-btn modern-btn-primary', 'Review session');
    review.type = 'submit';
    actions.append(cancel, review);
    form.append(
      heading,
      node('p', '', 'Your notes do not start a task, open a DAW, or prove work happened.'),
      fields,
      actions
    );
    dialog.append(form);
    document.body.append(dialog);
    dialog.addEventListener(
      'close',
      () => {
        dialog.remove();
        if (!document.querySelector('dialog[open]')) trigger?.focus?.();
      },
      { once: true }
    );
    form.addEventListener('submit', event => {
      event.preventDefault();
      const lines = control =>
        control.value
          .split(/\r?\n/)
          .map(value => value.trim())
          .filter(Boolean);
      if (wrapping) {
        for (const control of [decisions, blockers]) {
          const items = lines(control);
          control.setCustomValidity(
            items.length > 16 || items.some(item => item.length > 240)
              ? 'Use at most 16 lines of up to 240 characters each.'
              : ''
          );
          if (!control.reportValidity()) return;
        }
        extra.setCustomValidity(
          update.checked && !extra.value.trim()
            ? 'Enter a next action to update the project note.'
            : ''
        );
        if (!extra.reportValidity()) return;
      }
      const input = wrapping
        ? {
            recap: notes.value.trim(),
            decisions: lines(decisions),
            blockers: lines(blockers),
            next_action: extra.value.trim(),
            actual_date: date.value,
            update_project_next_action: update.checked
          }
        : {
            goal: notes.value.trim(),
            desired_outcome: extra.value.trim(),
            planned_date: date.value,
            time_minutes: time.value ? Number(time.value) : 0
          };
      dialog.close();
      void this.saveStudioSession(detail, session, input, trigger);
    });
    dialog.showModal();
    notes.focus();
  }

  async saveStudioSession(detail, session, input, trigger) {
    await this.run(trigger, 'Preparing your studio session for review…', async () => {
      const base = `/projects/${encodeURIComponent(detail.row.id)}/sessions`;
      const path = session ? `${base}/${encodeURIComponent(session.id)}/recaps` : `${base}/goals`;
      const request = session
        ? {
            if_session_revision: session.revision,
            if_fields_revision: detail.row.fields_revision,
            recap: input
          }
        : { if_entry_revision: detail.entry_revision, goal: input };
      const preview = await this.post(`${path}/review`, request);
      const consequences = session
        ? [
            detail.row.name,
            `Recap: ${input.recap}`,
            ...input.decisions.map(value => `Decision: ${value}`),
            ...input.blockers.map(value => `Blocker: ${value}`),
            `Next action: ${input.next_action || 'None saved'}`,
            input.update_project_next_action
              ? 'Also replaces the project’s saved next action.'
              : 'The project’s saved next action is unchanged.'
          ]
        : [
            detail.row.name,
            `Goal: ${input.goal}`,
            `Desired outcome: ${input.desired_outcome || 'Not specified'}`,
            'No project connection, task, or DAW operation is created.'
          ];
      if (
        !(await this.confirm(
          session ? 'Save this recap?' : 'Save this goal?',
          consequences,
          session ? 'Save recap' : 'Save goal',
          trigger
        ))
      )
        return;
      await this.post(`${path}/commit`, {
        ...request,
        review_token: preview.token,
        idempotency_key: operationKey(session ? 'recap' : 'goal'),
        confirm: true
      });
      await this.refresh();
      this.status(
        session
          ? 'Recap saved. Return to Details to resume.'
          : 'Goal saved. Return to Details to wrap up later.'
      );
    });
  }

  async editFields(detail, trigger) {
    const dialog = node('dialog', 'assistant-program-hire-dialog project-library-dialog');
    const heading = node('h2', '', `Edit ${detail.row.name}`);
    heading.id = operationKey('edit');
    dialog.setAttribute('aria-labelledby', heading.id);
    const form = node('form');
    form.method = 'dialog';
    const fields = node('div', 'project-library-fields');
    const stage = document.getElementById('projectLibraryStage').cloneNode(true);
    stage.removeAttribute('id');
    stage.value = detail.fields.stage || 'unknown';
    const status = document.getElementById('projectLibraryStatusFilter').cloneNode(true);
    status.removeAttribute('id');
    status.value = detail.fields.status || 'unknown';
    const action = node('input', 'form-control');
    action.maxLength = 240;
    action.value = detail.fields.next_action || '';
    const purpose = node('input', 'form-control');
    purpose.maxLength = 240;
    purpose.value = detail.fields.purpose || '';
    const sessionDate = node('input', 'form-control');
    sessionDate.type = 'date';
    sessionDate.value = detail.fields.session_date || '';
    const releaseDate = node('input', 'form-control');
    releaseDate.type = 'date';
    releaseDate.value = detail.fields.release_date || '';
    const blockers = node('textarea', 'form-control');
    blockers.rows = 3;
    blockers.maxLength = 4096;
    blockers.value = (detail.fields.blockers || []).join('\n');
    const deliverables = node('textarea', 'form-control');
    deliverables.rows = 3;
    deliverables.maxLength = 4096;
    deliverables.value = (detail.fields.deliverables || []).join('\n');
    for (const list of [blockers, deliverables])
      list.addEventListener('input', () => list.setCustomValidity(''));
    const archive = node('select', 'form-select');
    for (const [value, text] of [
      ['', 'Not recorded'],
      ['not_ready', 'Not ready'],
      ['ready', 'Ready for review'],
      ['reviewed', 'Reviewed']
    ]) {
      const option = node('option', '', text);
      option.value = value;
      archive.append(option);
    }
    archive.value = detail.fields.archive_review_state || '';
    const milestones = node('fieldset', 'project-library-milestones');
    milestones.append(node('legend', '', 'Milestones (user-entered, up to 16)'));
    const milestoneList = node('div', 'project-library-milestone-list');
    const addMilestone = node('button', 'modern-btn modern-btn-secondary', 'Add milestone');
    addMilestone.type = 'button';
    const milestoneControls = [];
    const newMilestone = saved => {
      if (milestoneList.children.length >= 16) return;
      const row = node('div', 'project-library-milestone');
      const title = node('input', 'form-control');
      title.required = true;
      title.maxLength = 240;
      title.value = saved?.label || '';
      const date = node('input', 'form-control');
      date.type = 'date';
      date.value = saved?.due_date || '';
      const complete = node('input');
      complete.type = 'checkbox';
      complete.checked = !!saved?.complete;
      for (const [caption, control] of [
        ['Milestone', title],
        ['Due date', date],
        ['Complete', complete]
      ]) {
        const labelNode = node('label', '', caption);
        labelNode.append(control);
        row.append(labelNode);
      }
      const remove = node('button', 'modern-btn modern-btn-secondary', 'Remove milestone');
      remove.type = 'button';
      remove.addEventListener('click', () => {
        row.remove();
        addMilestone.disabled = false;
      });
      row.append(remove);
      milestoneList.append(row);
      milestoneControls.push({
        id: saved?.id || operationKey('milestone'),
        row,
        title,
        date,
        complete
      });
      addMilestone.disabled = milestoneList.children.length >= 16;
      if (!saved) title.focus();
    };
    for (const saved of detail.fields.milestones || []) newMilestone(saved);
    addMilestone.addEventListener('click', () => newMilestone());
    milestones.append(milestoneList, addMilestone);
    const priority = node('select', 'form-select');
    for (const [value, name] of [
      ['', 'Unset'],
      ...Array.from({ length: 6 }, (_, i) => [String(i), String(i)])
    ]) {
      const option = node('option', '', name);
      option.value = value;
      priority.append(option);
    }
    priority.value = detail.fields.priority == null ? '' : String(detail.fields.priority);
    for (const [name, control] of [
      ['Production stage', stage],
      ['Administrative status', status],
      ['Project purpose (user-entered)', purpose],
      ['Next action', action],
      ['Priority', priority],
      ['Session date (user-entered)', sessionDate],
      ['Release date (user-entered)', releaseDate],
      ['Blockers (one per line, up to 16)', blockers],
      ['Deliverables (one per line, up to 16)', deliverables],
      ['Archive review (metadata only; does not move files)', archive]
    ]) {
      const labelNode = node('label', '', name);
      labelNode.append(control);
      fields.append(labelNode);
    }
    fields.append(milestones);
    const actions = node('div', 'assistant-program-dialog-actions');
    const cancel = node('button', 'modern-btn modern-btn-secondary', 'Cancel');
    cancel.type = 'button';
    const save = node('button', 'modern-btn modern-btn-primary', 'Review changes');
    save.type = 'submit';
    cancel.addEventListener('click', () => dialog.close());
    actions.append(cancel, save);
    form.append(
      heading,
      node(
        'p',
        '',
        'Only the fields you changed will be reviewed. Folder observations never set musical progress.'
      ),
      fields,
      actions
    );
    dialog.append(form);
    document.body.append(dialog);
    dialog.addEventListener(
      'close',
      () => {
        dialog.remove();
        trigger?.focus?.();
      },
      { once: true }
    );
    form.addEventListener('submit', event => {
      event.preventDefault();
      const patch = {};
      const listChanges = [];
      for (const [field, input] of [
        ['blockers', blockers],
        ['deliverables', deliverables]
      ]) {
        const entries = input.value
          .split(/\r?\n/)
          .map(value => value.trim())
          .filter(Boolean);
        if (entries.length > 16 || entries.some(value => value.length > 240)) {
          input.setCustomValidity('Use no more than 16 entries, each at most 240 characters.');
          input.reportValidity();
          return;
        }
        if (JSON.stringify(entries) !== JSON.stringify(detail.fields[field] || []))
          listChanges.push([field, entries]);
      }
      for (const [name, input, previous] of [
        ['stage', stage, detail.fields.stage || ''],
        ['status', status, detail.fields.status || ''],
        ['purpose', purpose, detail.fields.purpose || ''],
        ['next_action', action, detail.fields.next_action || ''],
        ['session_date', sessionDate, detail.fields.session_date || ''],
        ['release_date', releaseDate, detail.fields.release_date || ''],
        ['archive_review_state', archive, detail.fields.archive_review_state || '']
      ]) {
        const value = input.value === 'unknown' ? '' : input.value;
        if (value !== previous) patch[name] = value;
      }
      for (const [field, entries] of listChanges) patch[field] = entries;
      const currentMilestones = milestoneControls
        .filter(control => milestoneList.contains(control.row))
        .map(control => ({
          id: control.id,
          label: control.title.value.trim(),
          due_date: control.date.value,
          complete: control.complete.checked
        }));
      const normalizedMilestones = (detail.fields.milestones || []).map(item => ({
        id: item.id,
        label: item.label,
        due_date: item.due_date || '',
        complete: !!item.complete
      }));
      if (JSON.stringify(currentMilestones) !== JSON.stringify(normalizedMilestones))
        patch.milestones = currentMilestones;
      const beforePriority = detail.fields.priority == null ? '' : String(detail.fields.priority);
      if (priority.value !== beforePriority) {
        if (priority.value === '') patch.clear_priority = true;
        else patch.priority = Number(priority.value);
      }
      dialog.close();
      if (!Object.keys(patch).length) {
        this.status('No project fields changed.');
        return;
      }
      void this.saveFields(detail, patch, trigger);
    });
    dialog.showModal();
    cancel.focus();
  }

  async saveFields(detail, patch, trigger) {
    await this.run(trigger, 'Preparing your project notes for review…', async () => {
      const path = `/projects/${encodeURIComponent(detail.row.id)}/fields`;
      const request = { if_fields_revision: detail.row.fields_revision, patch };
      const review = await this.post(`${path}/review`, request);
      if (
        !(await this.confirm(
          'Save these project notes?',
          [
            detail.row.name,
            ...Object.entries(patch).map(
              ([key, value]) =>
                `${label(key)}: ${key === 'clear_priority' ? 'Unset' : key === 'milestones' ? value.map(item => `${item.label}${item.due_date ? ` · ${item.due_date}` : ''} · ${item.complete ? 'complete' : 'open'}`).join('; ') || 'None' : Array.isArray(value) ? value.join('; ') || 'None' : String(value || 'Not set')}`
            ),
            ...(patch.archive_review_state != null
              ? ['Archive review is a note, not a file move.']
              : [])
          ],
          'Save notes',
          trigger
        ))
      )
        return;
      await this.post(`${path}/commit`, {
        ...request,
        review_token: review.token,
        idempotency_key: operationKey('fields'),
        confirm: true
      });
      await this.refresh();
    });
  }
}
