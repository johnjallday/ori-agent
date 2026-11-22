// On-demand suggestions. The header owns only the door; the persistent overlay
// survives HQ repaints and hands every action to its existing owner.
import { HQ_LABELS as L } from './hq-labels.js';
import { firstVisitView, firstVisitResolved } from './hq-first-visit.js';
import { EXPLORE_FOLDER_URL, DOCUMENTS_FOLDER_URL } from './personal-assistant-panel.js';
import { beginHQAttempt } from './hq-guided-attempt.js';
import { workspaceOverlayCoordinator } from './workspace-overlay-coordinator.js';

const escape = value =>
  String(value ?? '').replace(
    /[&<>"']/g,
    ch => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[ch]
  );
const DOOR = '[data-hq-firstvisit-action="open"]';
const OVERLAY = 'hq-suggestions';

const choices = Object.freeze({
  folder: [
    'Explore Documents',
    'Review a folder with an agent.',
    'Review Documents or choose another folder. You’ll review the agent, model and workspace before anything is built.'
  ],
  email: [
    'Connect email',
    'Bring your mailbox into Email Ops.',
    'Email Ops reads and searches a linked mailbox. Setup can create an Email Ops workspace. Nothing is sent.'
  ],
  apps: [
    'Scan recent apps',
    'Choose tools to keep in your profile.',
    'Review recent apps from this computer. Only the tools you choose to save belong in your profile.'
  ]
});

export function offerOnEntry(location) {
  const params = new URLSearchParams(location.search);
  return (
    !location.hash &&
    [...params].every(([key, value]) => key === 'mode' && ['details', 'map'].includes(value))
  );
}

export class HQFirstVisitCard {
  constructor(owner) {
    this.owner = owner;
    this.explicitEntry =
      typeof window !== 'undefined' && window.location && !offerOnEntry(window.location);
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
        !['details', 'map'].includes(this.owner.viewMode) ||
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
    if (!this.owner.isPersonalHQ()) return '';
    return `<button type="button" class="ws-cmd-nav-btn ws-hq-suggestions-trigger" data-hq-firstvisit-action="open" aria-haspopup="dialog" aria-controls="${OVERLAY}" aria-expanded="${Boolean(this.node)}">${escape(L.firstVisitTitle)}</button>`;
  }

  html() {
    const choice = choices[this.path];
    const row = (key, [label, hint]) =>
      `<li><button type="button" class="ws-hq-suggestion" data-hq-firstvisit-action="${key}"><span class="ws-hq-suggestion-icon" aria-hidden="true"><i class="bi bi-${{ folder: 'folder2', email: 'envelope', apps: 'grid' }[key]}"></i></span><span><strong>${label}</strong><small>${hint}</small></span><span aria-hidden="true">→</span></button></li>`;
    const link = (url, label) =>
      `<li><a class="ws-hq-suggestion" data-hq-firstvisit-action="continue" href="${escape(url)}">${label}</a></li>`;
    const steps =
      this.path === 'folder'
        ? link(DOCUMENTS_FOLDER_URL, 'Review Documents') +
          link(EXPLORE_FOLDER_URL, 'Choose another folder')
        : this.path === 'email'
          ? link('/?setup=email', 'Review mailbox setup')
          : this.path === 'apps'
            ? link('/profile#personalization', 'Open Profile')
            : Object.entries(choices)
                .map(([key, value]) => row(key, value))
                .join('');
    return `<section id="${OVERLAY}" class="ws-hq-suggestions" data-hq-firstvisit role="dialog"${this.sheet ? ' aria-modal="true"' : ''} aria-labelledby="hq-suggestions-title" aria-describedby="hq-suggestions-hint" tabindex="-1">
      <header><div><small>Your personal assistant</small><h2 id="hq-suggestions-title">${choice ? choice[0] : 'What would you like to do next?'}</h2><p id="hq-suggestions-hint">${choice ? choice[2] : 'Pick one to see how it works. Nothing starts here.'}</p></div><button type="button" data-hq-firstvisit-action="close" aria-label="${escape(L.firstVisitClose)}">×</button></header>
      <ul>${steps}</ul>
      <footer><button type="button" data-hq-firstvisit-action="close">Maybe later</button></footer>
    </section>`;
  }

  bind() {
    this.door()?.addEventListener?.('click', () => (this.node ? this.close() : this.open()));
    this.paint();
    if (!this.door() || this.entryChecked || this.state !== 'pending' || !this.owner.active) return;
    this.entryChecked = true;
    if (
      this.explicitEntry ||
      !offerOnEntry(window.location) ||
      workspaceOverlayCoordinator().openIds().length ||
      document.querySelector(
        '.modal.show, dialog[open], .offcanvas.show, #personalAssistantPanel:not([hidden])'
      ) ||
      document.activeElement !== document.body
    )
      return;
    try {
      const key = `ori.hq.guidedOffered.${this.owner.workspaceId()}`;
      if (localStorage.getItem(key)) return;
      localStorage.setItem(key, '1');
    } catch (_) {
      return;
    }
    this.open();
  }

  open() {
    if (this.node || !this.owner.isPersonalHQ()) return;
    this.path = '';
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
    if (Object.hasOwn(choices, action)) {
      this.path = action;
      this.attempt = beginHQAttempt(this.owner.workspaceId(), action);
      this.paint();
      this.node?.querySelector('[role="dialog"]')?.focus({ preventScroll: true });
      return;
    }
    if (action === 'close') return this.close();
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
      // Mission completion changes the visit layout, not an open chooser.
      if (this.owner.active) this.owner.render();
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
