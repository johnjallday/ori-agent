// On-demand suggestions. The header owns only the door; the persistent overlay
// survives HQ repaints and hands every action to its existing owner.
import { HQ_LABELS as L } from './hq-labels.js';
import { firstVisitView, firstVisitResolved } from './hq-first-visit.js';
import { EXPLORE_FOLDER_URL, DOCUMENTS_FOLDER_URL } from './personal-assistant-panel.js';
import { workspaceOverlayCoordinator } from './workspace-overlay-coordinator.js';

const escape = value =>
  String(value ?? '').replace(
    /[&<>"']/g,
    ch => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[ch]
  );
const DOOR = '[data-hq-firstvisit-action="open"]';
const OVERLAY = 'hq-suggestions';

export class HQFirstVisitCard {
  constructor(owner) {
    this.owner = owner;
    this.state = typeof document === 'undefined' ? '' : document.body?.dataset?.hqFirstVisit || '';
    this.missions = null;
    this.started = false;
    this.busy = false;
    this.error = '';
    this.resolvedOnLoad = null;
    this.node = null;
  }

  view() {
    return firstVisitView({
      state: this.state,
      missions: this.missions,
      notes: this.owner.notesState().secondBrain,
      resolvedOnLoad: this.resolvedOnLoad === true && !this.error
    });
  }

  ensure() {
    if (
      this.node &&
      (!this.owner.active ||
        this.owner.viewMode !== 'details' ||
        this.id !== this.owner.workspaceId())
    )
      this.close(false);
    if (this.state !== 'pending' || this.started || !this.owner.isPersonalHQ()) return;
    this.started = true;
    if (typeof window !== 'undefined') {
      window.addEventListener?.('pagehide', () => this.close(false));
      window.addEventListener?.('pageshow', event => {
        if (event.persisted) {
          this.close(false);
          void this.load();
        }
      });
    }
    this.owner.requestNotesData();
    void this.load();
  }

  async load() {
    if (this.loading || this.state !== 'pending') return;
    const id = this.owner.workspaceId();
    this.loading = true;
    this.error = '';
    try {
      const payload = await this.owner.stationJSON('/api/progression');
      if (id !== this.owner.workspaceId()) return;
      if (!Array.isArray(payload?.missions)) throw new Error('missing missions');
      if (this.resolvedOnLoad === null) this.resolvedOnLoad = firstVisitResolved(payload.missions);
      this.missions = payload.missions;
    } catch (_) {
      this.error = L.firstVisitLoadError;
    } finally {
      this.loading = false;
    }
    if (this.view().finishSilently) await this.finish();
    else if (this.owner.active) this.owner.render();
    this.paint();
    // The missions are what the assistant's greeting waits for.
    this.owner.notifyHQGreeting?.();
  }

  door() {
    return this.owner.container?.querySelector?.(DOOR);
  }

  headerHTML() {
    if (this.view().card === 'none') return '';
    return `<button type="button" class="ws-cmd-nav-btn ws-hq-suggestions-trigger" data-hq-firstvisit-action="open" aria-haspopup="dialog" aria-controls="${OVERLAY}" aria-expanded="${Boolean(this.node)}">${escape(L.firstVisitTitle)}</button>`;
  }

  html() {
    const v = this.view();
    if (v.card === 'none') return '';
    const row = step => {
      const icon = { folder: 'folder2', brief: 'sun', note: 'pencil' }[step.key];
      const mark = step.state === 'completed' ? '✓' : step.state === 'skipped' ? '—' : '';
      const status = step.resolved
        ? `<span class="visually-hidden"> — ${escape(step.state === 'skipped' ? L.firstVisitSkipped : L.firstVisitCompleted)}</span>`
        : '';
      const body = `<span class="ws-hq-suggestion-icon" aria-hidden="true">${mark || `<i class="bi bi-${icon}"></i>`}</span><span><strong>${escape(step.title)}${status}</strong><small>${escape(step.hint)}</small></span><span aria-hidden="true">→</span>`;
      const href =
        step.action === 'folder' ? DOCUMENTS_FOLDER_URL : step.action === 'home' ? '/' : '';
      const control =
        href && !step.disabled
          ? `<a class="ws-hq-suggestion" data-hq-firstvisit-action="${step.action}" href="${href}">${body}</a>`
          : `<button type="button" class="ws-hq-suggestion" data-hq-firstvisit-action="${step.action}"${step.disabled ? ' disabled' : ''}>${body}</button>`;
      const alternative =
        step.action === 'folder' && !step.disabled
          ? `<a class="ws-hq-suggestion-alternative" data-hq-firstvisit-action="folder" href="${EXPLORE_FOLDER_URL}">${escape(L.firstFolderChoose)}</a>`
          : '';
      return `<li${step.resolved ? ' class="is-resolved"' : ''}>${control}${alternative}</li>`;
    };
    return `<section id="${OVERLAY}" class="ws-hq-suggestions" data-hq-firstvisit role="dialog"${this.sheet ? ' aria-modal="true"' : ''} aria-labelledby="hq-suggestions-title" aria-describedby="hq-suggestions-hint" tabindex="-1">
      <header><div><h2 id="hq-suggestions-title">${escape(L.firstVisitTitle)}</h2><p id="hq-suggestions-hint">${escape(L.firstVisitLead)}</p></div><button type="button" data-hq-firstvisit-action="close" aria-label="${escape(L.firstVisitClose)}">×</button></header>
      <ul>${v.steps.map(row).join('')}</ul>
      <footer><button type="button" data-hq-firstvisit-action="hide"${this.busy ? ' disabled' : ''}>${escape(L.firstVisitHide)}</button>${this.error === L.firstVisitLoadError ? `<button type="button" data-hq-firstvisit-action="retry">${escape(L.firstVisitRetry)}</button>` : ''}</footer>
      <p class="ws-hq-suggestions-status${this.error ? '' : ' visually-hidden'}" role="status">${escape(this.error || (v.loading ? L.firstVisitLoading : ''))}</p>
    </section>`;
  }

  bind() {
    this.door()?.addEventListener?.('click', () => (this.node ? this.close() : this.open()));
    this.paint();
  }

  open() {
    if (this.node || this.view().card === 'none') return;
    this.id = this.owner.workspaceId();
    this.sheet = window.matchMedia('(max-width: 600px)').matches;
    this.node = document.createElement('div');
    this.node.className = `ws-cmd ws-hq-suggestions-layer${this.sheet ? ' is-sheet' : ''}`;
    document.body.appendChild(this.node);
    this.paint();
    this.node.addEventListener('click', event => {
      const control = event.target.closest('[data-hq-firstvisit-action]');
      if (!control || control.disabled) return;
      const action = control.getAttribute('data-hq-firstvisit-action');
      if (control.tagName === 'A') {
        // Let the browser own links (including modifier-click); no folder work
        // starts here. The existing Home consent card owns the next action.
        if (!event.metaKey && !event.ctrlKey && !event.shiftKey && !event.altKey) this.close(false);
      } else void this.act(action);
    });
    this.node.addEventListener('keydown', event => {
      if (event.key === 'Escape') {
        event.preventDefault();
        event.stopPropagation();
        workspaceOverlayCoordinator().escapeTopmost();
      }
    });
    this.node.addEventListener('focusout', event => {
      if (
        !this.sheet &&
        event.relatedTarget &&
        !this.node?.contains(event.relatedTarget) &&
        event.relatedTarget !== this.door()
      )
        this.close(false);
    });
    this.outside = event => {
      if (!this.node?.contains(event.target) && !this.door()?.contains(event.target))
        this.close(false);
      else if (event.target === this.node) {
        // Backdrop presses must not move focus to body after close restores
        // the trigger (the pointer's default focus happens after this handler).
        event.preventDefault();
        this.close();
      }
    };
    this.resize = () => {
      if (this.sheet !== window.matchMedia('(max-width: 600px)').matches) this.close();
      else this.position();
    };
    document.addEventListener('pointerdown', this.outside, true);
    window.addEventListener('resize', this.resize);
    window.addEventListener('scroll', this.resize, true);
    workspaceOverlayCoordinator().open({
      id: OVERLAY,
      kind: this.sheet ? 'modal' : 'popover',
      container: this.sheet ? this.node : null,
      // The header is replaced by live updates. Resolve its current button,
      // never return focus to a detached original trigger.
      trigger: {
        focus: () => {
          if (this.restoreOnClose !== false) this.door()?.focus({ preventScroll: true });
        }
      },
      onClose: info => {
        if (info.reason === 'suspended') this.node.hidden = true;
        else this.cleanup();
      },
      onResume: () => {
        this.node.hidden = false;
        this.node.querySelector('[role="dialog"]').focus();
      }
    });
    this.restoreOnClose = true;
    this.door()?.setAttribute('aria-expanded', 'true');
    this.node.querySelector('[role="dialog"]').focus({ preventScroll: true });
    void this.load();
  }

  position() {
    if (!this.node || this.sheet) return;
    const dialog = this.node.querySelector('[role="dialog"]');
    const anchor = this.door()?.getBoundingClientRect();
    if (!anchor) return;
    const width = dialog.getBoundingClientRect().width;
    const height = dialog.getBoundingClientRect().height;
    dialog.style.left = `${Math.max(12, Math.min(anchor.right - width, window.innerWidth - width - 12))}px`;
    dialog.style.top = `${Math.max(12, Math.min(anchor.bottom + 8, window.innerHeight - height - 12))}px`;
  }

  paint() {
    if (!this.node) return;
    if (this.view().card === 'none') {
      this.close();
      return;
    }
    const html = this.html();
    if (html !== this.lastHTML) {
      const active = document.activeElement;
      const focused = this.node.contains(active);
      const action = focused && active?.getAttribute('data-hq-firstvisit-action');
      this.node.innerHTML = html;
      this.lastHTML = html;
      if (focused)
        (
          this.node.querySelector(
            action ? `[data-hq-firstvisit-action="${action}"]` : '[role="dialog"]'
          ) || this.node.querySelector('[role="dialog"]')
        ).focus({ preventScroll: true });
    }
    this.position();
  }

  cleanup() {
    document.removeEventListener('pointerdown', this.outside, true);
    window.removeEventListener('resize', this.resize);
    window.removeEventListener('scroll', this.resize, true);
    this.node?.remove();
    this.node = null;
    this.lastHTML = '';
    this.door()?.setAttribute('aria-expanded', 'false');
  }

  close(restore = true) {
    if (!this.node) return;
    this.restoreOnClose = restore;
    workspaceOverlayCoordinator().close(OVERLAY);
  }

  async act(action) {
    if (action === 'close') return this.close();
    if (this.busy) return;
    if (action === 'hide') return this.finish();
    if (action === 'retry') return this.load();
    const trigger = this.door();
    this.close(false);
    if (action === 'note') this.owner.focusHQNoteCapture(trigger);
    else if (action === 'notes') this.owner.openNotesStation(trigger);
    else if (action === 'brief') this.owner.openDailyBriefPanel(trigger);
  }

  async briefClosed() {
    // Ordinary station use counts too, without opening suggestions or moving
    // focus away from the panel's own restored trigger.
    await this.load();
  }

  async finish() {
    if (this.busy) return;
    this.busy = true;
    this.error = '';
    this.paint();
    try {
      const response = await fetch('/api/personal-hq/first-visit-state', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ state: 'done' })
      });
      if (!response.ok) throw new Error('finish failed');
      this.state = 'done';
      if (typeof document !== 'undefined' && document.body?.dataset)
        delete document.body.dataset.hqFirstVisit;
      const hadFocus = this.node?.contains(document.activeElement);
      this.close(false);
      if (this.owner.active) this.owner.render();
      if (hadFocus)
        this.owner.container
          ?.querySelector?.('[data-cmd-view-mode="details"]')
          ?.focus({ preventScroll: true });
    } catch (_) {
      this.error = L.firstVisitFinishError;
      // A silent finish failure leaves a usable door; never force it open.
      if (this.owner.active) this.owner.render();
    } finally {
      this.busy = false;
      this.paint();
      // Hide suggestions ends the greeting too.
      this.owner.notifyHQGreeting?.();
    }
  }
}
