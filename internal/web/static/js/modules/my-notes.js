import { notesPost, notesRequest } from './notes-api.js';
import { initialNotesFolder, MyNotesBrowser } from './my-notes-browser.js';
import {
  isCreatedByOri,
  lookTooSoonLine,
  LOOK_STILL_READING_LINE,
  nextNotesTab,
  notesPanelHTML,
  notesSections,
  notesTabs,
  NOTES_ITEM_ACTIONS
} from './notes-station.js';
import {
  fetchSecondBrainPreview,
  followUpReceiptLine,
  LOOK_POLL_LIMIT,
  LOOK_POLL_MS,
  newRequestID,
  openSecondBrainFolder,
  secondBrainCaptureProblem,
  secondBrainCaptureReceipt
} from './personal-assistant-second-brain.js';

export function myNotesConnection(view) {
  if (view?.setup_required === 'assistant') {
    return {
      title: 'Meet your assistant first',
      detail: 'Notes setup uses your personal assistant and Personal HQ.',
      href: '/?quest=meet-assistant',
      label: 'Meet your assistant'
    };
  }
  if (view?.setup_required === 'hq') {
    return {
      title: 'Build My HQ first',
      detail: 'Personal HQ keeps track of your connection. Your notes stay in their folder.',
      href: '/?quest=build-hq',
      label: 'Build My HQ'
    };
  }
  if (view?.status === 'not_connected') {
    return {
      title: 'No notes folder connected',
      detail: 'Choose an existing folder or set up a new notebook with Ori.',
      manage: 'Set up notes…'
    };
  }
  if (view?.status === 'missing') {
    return {
      title: 'Notes folder not found',
      detail: `${view.source?.name || 'Your folder'} may have moved or be unavailable.`,
      manage: 'Choose again…'
    };
  }
  if (view?.status === 'connected' && view.source?.id) {
    return {
      connected: true,
      title: view.source.name,
      detail: isCreatedByOri(view)
        ? 'Set up by Ori · capture to Inbox'
        : 'Connected folder · read-only for Ori',
      manage: 'Connection settings…'
    };
  }
  return {
    title: 'Unable to check your notes',
    detail: 'Ori could not check your connection. Try again; nothing has been cleared.',
    retry: true
  };
}

// myNotesLanding is what arrival shows, decided from the lightweight connection
// view alone. Setup and repair keep their existing flows; arrival never opens
// settings, creates a notebook or lists a folder that is not connected. A
// connected notebook opens in the browser: an Ori notebook at Inbox, any other
// folder at its own root. ?view=inbox and ?view=loops still open those views.
export function myNotesLanding(view, arrival = null) {
  if (view?.setup_required || view?.status === 'not_connected') return { kind: 'setup' };
  if (view?.status === 'missing') return { kind: 'repair' };
  if (!myNotesConnection(view).connected) return { kind: 'retry' };
  if (!view.browse_ref) return { kind: 'connected' };
  const created = isCreatedByOri(view);
  return {
    kind: 'browse',
    folder: initialNotesFolder(view),
    tools: arrival === 'loops' || (arrival === 'inbox' && created) ? arrival : null
  };
}

// Requests can finish after a retry, a connection change or a bfcache departure.
// Only the latest request in this document's active lifetime can update the view.
export function latestNotesRequest() {
  let current;
  return {
    begin() {
      current?.abort();
      const request = new AbortController();
      current = request;
      return request;
    },
    isCurrent(request) {
      return request === current && !request.signal.aborted;
    },
    cancel() {
      current?.abort();
    }
  };
}

function node(tag, text) {
  const element = document.createElement(tag);
  element.textContent = text;
  return element;
}

function button(text, action) {
  const el = node('button', text);
  el.type = 'button';
  el.className = 'modern-btn modern-btn-secondary modern-btn-sm';
  el.dataset.myNotesAction = action;
  return el;
}

function connectionKey(view) {
  return [view?.status, view?.source?.id, view?.source?.connected_at].join(':');
}

export class MyNotes {
  constructor(root) {
    this.root = root;
    this.title = root.querySelector('#my-notes-title');
    this.status = root.querySelector('[data-my-notes-status]');
    this.tools = root.querySelector('[data-my-notes-tools]');
    this.browser = new MyNotesBrowser(root.querySelector('[data-my-notes-browser]'), {
      openFolder: () => this.openFolder(),
      // A listing that says the connection changed or the folder is gone asks
      // for the connection again; a changed one clears and reopens the browser.
      recheck: () => {
        if (!this.suspended) void this.load();
      }
    });
    this.request = latestNotesRequest();
    this.arrival = new URLSearchParams(window.location.search).get('view');
    root.addEventListener('click', event => this.click(event));
    root.addEventListener('input', event => {
      if (this.panel && event.target.matches('[data-notes-capture]')) {
        this.panel.captureDraft = event.target.value;
        this.panel.captureID = null;
      }
    });
    root.addEventListener('keydown', event => this.keydown(event));
    this.tools.addEventListener(
      'toggle',
      event => {
        if (this.panel && event.target.matches('.ws-notes-handled')) {
          this.panel.handledOpen = event.target.open;
        }
      },
      true
    );
    document.addEventListener('personal-assistant:second-brain', () => {
      if (!this.suspended) void this.load();
    });
    window.addEventListener('pagehide', () => {
      this.suspended = true;
      this.request.cancel();
      this.browser.clear();
      this.clearTools();
    });
    window.addEventListener('pageshow', event => {
      if (!event.persisted) return;
      this.suspended = false;
      void this.load();
    });
    this.title.focus({ preventScroll: true });
    void this.load();
  }

  async load() {
    const request = this.request.begin();
    this.status.setAttribute('aria-busy', 'true');
    let view;
    try {
      const payload = await notesRequest('?view=connection', { signal: request.signal });
      view = payload.second_brain;
    } catch {
      // A failed request is not the same as having no source.
    }
    if (!this.request.isCurrent(request)) return;
    if (
      connectionKey(view) !== connectionKey(this.view) ||
      view?.browse_ref !== this.view?.browse_ref
    ) {
      this.browser.clear();
      this.clearTools();
    }
    this.view = view;
    this.paintStatus();
    this.status.removeAttribute('aria-busy');
    this.land();
  }

  // land opens the browser once per connection. A refresh of the same
  // connection keeps whatever folder and note the reader chose.
  land() {
    const landing = myNotesLanding(this.view, this.arrival);
    if (landing.kind !== 'browse') return;
    if (!this.browser.view) this.browser.start(this.view, landing.folder);
    this.arrival = null;
    if (landing.tools) void this.openTools(landing.tools, { focus: false });
  }

  paintStatus() {
    const state = myNotesConnection(this.view);
    const focused = document.activeElement?.dataset.myNotesAction;
    const detail = node('p', state.detail);
    if (state.retry) detail.setAttribute('role', 'alert');
    const title = node('h2', state.title);
    title.tabIndex = -1;
    const actions = node('div', '');
    actions.className = 'my-notes-actions';
    if (state.connected) {
      // The notebook itself is the browser below; these are its tools.
      actions.setAttribute('role', 'group');
      actions.setAttribute('aria-label', 'Notebook tools');
      actions.append(button('Open loops', 'loops'));
      actions.append(button(isCreatedByOri(this.view) ? 'People & Projects' : 'People', 'more'));
      actions.append(button('Open folder', 'folder'));
    }
    if (state.manage) actions.append(button(state.manage, 'manage'));
    if (state.retry) actions.append(button('Try again', 'retry'));
    if (state.href) {
      const link = node('a', state.label);
      link.href = state.href;
      link.className = 'modern-btn modern-btn-primary modern-btn-sm';
      actions.append(link);
    }
    const notice = node('p', '');
    notice.dataset.myNotesNotice = '';
    notice.setAttribute('role', 'status');
    this.status.replaceChildren(title, detail, actions, notice);
    if (focused) {
      const same = Array.from(actions.children).find(el => el.dataset.myNotesAction === focused);
      (same || title).focus({ preventScroll: true });
    }
  }

  async manage(trigger) {
    const panel = window.PersonalAssistantPanel;
    const notes = window.PersonalAssistantSecondBrain;
    if (notes && panel?.open?.(trigger, { focus: false })) {
      if (await notes.openManage()) return;
    }
    this.notice('Open your assistant to manage this connection, then try again.');
  }

  notice(message) {
    if (!this.suspended) {
      const el = this.status.querySelector('[data-my-notes-notice]');
      if (el) el.textContent = message;
    }
  }

  clearTools() {
    clearTimeout(this.poll);
    this.panel = null;
    this.tools.hidden = true;
    this.tools.replaceChildren();
  }

  async openTools(tab, { focus = true } = {}) {
    if (this.panel?.status === 'ready') {
      this.setTab(tab, focus);
      return;
    }
    const panel = { status: 'loading', tab };
    this.panel = panel;
    this.tools.hidden = false;
    this.paintTools(panel);
    try {
      const created = isCreatedByOri(this.view);
      const [look, full, inbox, people, progression] = await Promise.all([
        notesRequest('/look'),
        created ? notesRequest() : null,
        created ? notesRequest('/inbox') : null,
        created ? notesRequest('/people').catch(() => null) : null,
        created
          ? fetch('/api/progression')
              .then(response => response.json())
              .catch(() => null)
          : null
      ]);
      if (panel !== this.panel) return;
      if (full && connectionKey(full.second_brain) !== connectionKey(this.view)) {
        this.clearTools();
        void this.load();
        return;
      }
      Object.assign(panel, {
        status: 'ready',
        look: look.look,
        view: full?.second_brain || this.view,
        inbox: inbox?.inbox,
        people: people?.people,
        missions: progression?.missions
      });
    } catch {
      if (panel !== this.panel) return;
      this.clearTools();
      this.notice('Ori could not load that view. Choose it again to retry.');
      return;
    }
    this.paintTools(panel);
    if (
      focus &&
      (this.status.contains(document.activeElement) || document.activeElement === document.body)
    )
      this.focusTab();
    this.pollLook(panel);
  }

  paintTools(panel) {
    if (panel !== this.panel) return;
    const focused = this.tools.contains(document.activeElement) ? document.activeElement : null;
    this.tools.innerHTML = notesPanelHTML({
      secondBrain: panel.view || this.view,
      panel,
      embedded: true
    });
    if (!focused) return;
    const replacement = focused.matches('[data-notes-capture]')
      ? this.tools.querySelector('[data-notes-capture]')
      : [...this.tools.querySelectorAll('[data-cmd-modal-action]')].find(
          el =>
            el.dataset.cmdModalAction === focused.dataset.cmdModalAction &&
            el.dataset.cmdId === focused.dataset.cmdId
        );
    if (replacement && !replacement.disabled) replacement.focus({ preventScroll: true });
    else this.focusTab({ preventScroll: true });
  }

  setTab(tab, focus = true) {
    this.panel.tab = tab;
    this.panel.preview = null;
    this.paintTools(this.panel);
    if (focus) this.focusTab();
  }

  focusTab(options) {
    this.tools.querySelector('[data-notes-tab][aria-selected="true"]')?.focus(options);
  }

  keydown(event) {
    const panel = this.panel;
    if (!panel || event.defaultPrevented) return;
    if (
      event.key === 'Enter' &&
      event.target.matches('[data-notes-capture]') &&
      !event.isComposing
    ) {
      event.preventDefault();
      void this.act('notes-capture');
    } else if (
      event.target.matches('[data-notes-tab]') &&
      ['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)
    ) {
      event.preventDefault();
      const tabs = notesTabs(notesSections({ secondBrain: panel.view, ...panel }));
      const key =
        event.key === 'Home'
          ? tabs[0]?.key
          : event.key === 'End'
            ? tabs.at(-1)?.key
            : nextNotesTab(tabs, event.target.dataset.cmdId, event.key === 'ArrowLeft' ? -1 : 1);
      if (key) this.setTab(key);
    }
  }

  async openFolder() {
    const view = this.view;
    const result = await openSecondBrainFolder();
    const message = result.ok ? 'Folder handoff requested.' : result.message;
    if (connectionKey(view) === connectionKey(this.view)) this.notice(message);
    return message;
  }

  async click(event) {
    const control = event.target.closest('button');
    if (!control || control.disabled) return;
    const { myNotesAction, cmdModalAction, cmdId } = control.dataset;
    switch (myNotesAction) {
      case 'retry':
        return this.load();
      case 'manage':
        return this.manage(control);
      case 'folder':
        return this.openFolder();
      case 'inbox':
      case 'loops':
      case 'more':
        return this.openTools(myNotesAction);
      default:
        if (cmdModalAction) return this.act(cmdModalAction, cmdId);
    }
  }

  async act(action, id) {
    const panel = this.panel;
    if (!panel) return;
    if (action === 'notes-tab') return this.setTab(id);
    if (action === 'notes-show-all') {
      panel.showAll = true;
    } else if (action === 'notes-focus-capture') {
      this.tools.querySelector('[data-notes-capture]')?.focus();
      return;
    } else if (action === 'notes-preview') {
      await this.preview(panel, id);
    } else if (action === 'notes-capture') {
      await this.capture(panel);
    } else {
      await this.updateLook(panel, action, id);
    }
    this.paintTools(panel);
  }

  async preview(panel, key) {
    if (panel.preview?.key === key) {
      panel.preview = null;
      return;
    }
    const at = key.indexOf(':');
    if (at < 1) return;
    const pending = { key, status: 'loading' };
    panel.preview = pending;
    this.paintTools(panel);
    const ref = panel.view?.browse_ref;
    const result = await fetchSecondBrainPreview(
      key.slice(0, at),
      key.slice(at + 1),
      (path, options) =>
        fetch(path + '?' + new URLSearchParams({ ref }), { ...options, cache: 'no-store' })
    );
    if (panel.preview === pending) {
      panel.preview = result.ok
        ? { key, status: 'ready', note: result.preview }
        : { key, status: 'error', message: result.message };
    }
  }

  async capture(panel) {
    if (panel.captureBusy) return;
    const text = panel.captureDraft || '';
    panel.captureProblem = secondBrainCaptureProblem(text);
    if (panel.captureProblem) return;
    panel.captureBusy = true;
    panel.captureID ||= newRequestID('notes');
    this.paintTools(panel);
    try {
      const payload = await notesPost(
        '/capture',
        { text, source_ref: panel.view.browse_ref },
        panel.captureID
      );
      if (!payload.capture?.note)
        throw new Error('No save receipt was returned. Retry to check the save.');
      panel.captureDraft = '';
      panel.captureID = null;
      panel.captureReceipt = secondBrainCaptureReceipt(payload.capture);
      if (panel === this.panel) this.browser.refreshInbox();
      const [inbox, progression] = await Promise.all([
        notesRequest('/inbox').catch(() => null),
        fetch('/api/progression', { cache: 'no-store' })
          .then(response => response.json())
          .catch(() => null)
      ]);
      if (inbox) panel.inbox = inbox.inbox;
      if (progression?.missions) panel.missions = progression.missions;
    } catch (error) {
      panel.captureProblem = error.message;
    } finally {
      panel.captureBusy = false;
    }
  }

  async updateLook(panel, action, id) {
    if (panel.lookBusy) return;
    const lookID = panel.look?.look_id;
    const itemAction = action.replace(/^notes-item-/, '');
    panel.lookBusy = true;
    panel.message = '';
    try {
      if (action === 'notes-look-again') {
        panel.lookMessage = '';
        panel.look = (await notesPost('/look', {})).look;
        panel.polls = 0;
        this.pollLook(panel);
      } else if (lookID && action === 'notes-add-suggested') {
        const payload = await notesPost(`/look/${encodeURIComponent(lookID)}/follow-ups`, {
          items: 'suggested'
        });
        panel.message = followUpReceiptLine(payload.follow_ups);
        if (panel === this.panel) panel.look = (await notesRequest('/look')).look;
      } else if (lookID && id && NOTES_ITEM_ACTIONS.includes(itemAction)) {
        panel.look = (
          await notesPost(`/look/${encodeURIComponent(lookID)}/items/${encodeURIComponent(id)}`, {
            action: itemAction
          })
        ).look;
      }
    } catch (error) {
      if (error.status === 429) {
        panel.lookMessage = lookTooSoonLine({
          retryAfterSeconds: error.retryAfterSeconds,
          lastLookAt: panel.look?.result?.finished_at
        });
      } else {
        panel.message = error.message;
      }
    } finally {
      panel.lookBusy = false;
    }
  }

  pollLook(panel) {
    if (panel !== this.panel || panel.look?.state !== 'running') return;
    clearTimeout(this.poll);
    if ((panel.polls || 0) >= LOOK_POLL_LIMIT) {
      panel.lookMessage = LOOK_STILL_READING_LINE;
      this.paintTools(panel);
      return;
    }
    this.poll = setTimeout(async () => {
      if (panel !== this.panel) return;
      panel.polls = (panel.polls || 0) + 1;
      try {
        panel.look = (await notesRequest('/look')).look;
      } catch {
        panel.lookMessage = 'Ori could not refresh the Look. Reopen My Notes to try again.';
      }
      this.paintTools(panel);
      if (!panel.lookMessage) this.pollLook(panel);
    }, LOOK_POLL_MS);
  }
}

if (typeof document !== 'undefined') {
  const root = document.querySelector('[data-my-notes]');
  if (root) new MyNotes(root);
}
