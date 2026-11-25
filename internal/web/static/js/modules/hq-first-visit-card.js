// One automatic invitation; the header remains a voluntary door. The overlay
// survives HQ repaints and hands every action to its existing owner.
import { offerOnEntry, offerKey } from './hq-guided-entry.js';
import { HQ_LABELS as L } from './hq-labels.js';
import { firstVisitView, firstVisitResolved } from './hq-first-visit.js';
import { reviewNames, saveReviewedTools, toolsReceipt } from './hq-guided-apps.js';
import { emailNextStep } from './hq-guided-email.js';
import {
  beginHQAttempt,
  bindEmailAttempt,
  currentHQAttempt,
  receiptHint,
  markReturned,
  guidedReturnURL,
  handoffURL
} from './hq-guided-attempt.js';
import { folderReceipt } from './hq-guided-folder.js';
import { workspaceOverlayCoordinator } from './workspace-overlay-coordinator.js';

const escape = value =>
  String(value ?? '').replace(
    /[&<>"']/g,
    ch => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[ch]
  );
const DOOR = '[data-hq-firstvisit-action="open"]';
const OVERLAY = 'hq-suggestions';
const guideMark = `<svg class="ws-hq-guide-mark" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linejoin="round" aria-hidden="true" focusable="false"><circle cx="12" cy="12" r="9"/><path d="m16.5 7.5-3 6-6 3 3-6z"/></svg>`;

const choices = Object.freeze({
  workspace: [L.firstFolderDocuments, 'Learn where to start a workspace.', L.firstFolderHint],
  email: [
    'Connect email',
    'Bring your mailbox into Email Ops.',
    'Email Ops reads and searches a linked mailbox. Setup can create an Email Ops workspace. Nothing is sent.'
  ],
  apps: [
    'Scan recent apps',
    'Choose tools to keep in your profile.',
    'Look for app use in the last seven days on this computer, where available. Review the names first. Only tools you select and save are added to Profile.'
  ]
});

export class HQFirstVisitCard {
  constructor(owner) {
    this.owner = owner;
    this.explicitEntry =
      typeof window !== 'undefined' && window.location && !offerOnEntry(window.location);
    this.state = typeof document === 'undefined' ? '' : document.body?.dataset?.hqFirstVisit || '';
    this.missions = null;
    this.guidedHQ = typeof document === 'undefined' ? '' : document.body?.dataset?.guidedHq || '';
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
    if (this.started || !this.owner.isPersonalHQ()) return;
    this.started = true;
    if (typeof window !== 'undefined') {
      window.addEventListener?.('pagehide', () => this.close(false));
      window.addEventListener?.('pageshow', event => {
        if (event.persisted) {
          this.close(false);
          this.checkedAttempt = null;
          void this.loadReturn();
          void this.load();
        }
      });
    }
    if (this.state !== 'pending') return;
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

  available() {
    return Boolean(
      this.guidedHQ && this.owner.isPersonalHQ() && this.guidedHQ === this.owner.workspaceId()
    );
  }

  door() {
    return this.owner.container?.querySelector?.(DOOR);
  }

  async loadReturn() {
    if (!this.available() || typeof window === 'undefined') return;
    const hint = receiptHint(window.location, this.owner.workspaceId());
    if (!hint || hint.path === 'workspace') {
      this.checkedAttempt = null;
      this.returnRead = null;
      if (this.receipt) {
        this.receipt = null;
        if (this.owner.active) this.owner.render();
      }
      return;
    }
    if (this.checkedAttempt === hint.attempt) return;
    this.checkedAttempt = hint.attempt;
    const readToken = Symbol();
    this.returnRead = readToken;
    let result;
    try {
      const read = url => this.owner.stationJSON(url);
      result =
        hint.path === 'apps'
          ? await toolsReceipt(hint, read)
          : hint.path === 'email'
            ? await emailNextStep(read, hint)
            : await folderReceipt(hint, read);
    } catch (_) {
      /* unavailable, not completed */
    }
    if (
      this.returnRead !== readToken ||
      receiptHint(window.location, this.owner.workspaceId())?.attempt !== hint.attempt
    )
      return;
    markReturned(hint);
    if (result?.href && hint.path === 'email' && result.label !== 'Review email in Email Ops')
      result = { ...result, href: handoffURL(result.href, hint) };
    this.receipt = result || {
      text: 'The result could not be verified. Reload to check again.'
    };
    if (this.owner.active) this.owner.render();
  }

  receiptHTML() {
    const projects = this.receipt?.offer?.projects || [];
    const draft = p =>
      this.receipt.offer.writable || this.receipt.offer.setup
        ? `<button type="button" class="ws-cmd-nav-btn" data-documents-draft="${escape(p.ref)}">Draft ${escape(p.name)} note</button>`
        : '';
    const decision = this.receipt?.offer?.decision;
    const action = (id, label) =>
      `<button type="button" class="ws-cmd-nav-btn" data-documents-action="${id}">${escape(label)}</button>`;
    let evidence = projects
      .map(
        (p, i) =>
          `${i === 0 ? draft(p) : ''}<details><summary>Review ${escape(p.location)}</summary><p>The folder report found ${escape(p.evidence)} here. No file text was used.</p>${i > 0 ? draft(p) : ''}</details>`
      )
      .join('');
    if (decision?.state === 'dismissed')
      evidence = `<details><summary>Review project choices</summary>${evidence}</details>`;
    else if (decision)
      evidence =
        decision.state === 'saved'
          ? action('open', `Open ${decision.name} note`)
          : decision.state === 'pending'
            ? action('recover', 'Check save confirmation')
            : '';
    else if (projects.length && (this.receipt.offer.writable || this.receipt.offer.setup))
      evidence += action('dismiss', 'Not now');
    if (this.receipt?.checkAgain) evidence += action('check', 'Check again');
    const vaults = (this.receipt?.offer?.vaults || [])
      .map(
        v =>
          `<button type="button" class="ws-cmd-nav-btn" data-documents-vault="${escape(v.ref)}">Connect ${escape(v.location)} read-only</button>`
      )
      .join('');
    const notes =
      this.receipt?.offer &&
      (decision?.state === 'unverified' ||
        (!this.receipt.offer.writable && !this.receipt.offer.setup))
        ? '<a href="/my-notes">Open Notes</a> '
        : '';
    return this.receipt
      ? `<section class="ws-cmd-card" role="status" data-guided-receipt><strong>From your assistant</strong><p>${escape(this.receipt.text)}</p>${evidence}${vaults}${notes}${this.receipt.href ? `<a href="${escape(this.receipt.href)}">${escape(this.receipt.label)}</a>` : ''}</section>`
      : '';
  }

  headerHTML() {
    if (!this.available()) return '';
    return `<button type="button" class="ws-cmd-nav-btn ws-hq-suggestions-trigger" data-hq-firstvisit-action="open" aria-haspopup="dialog" aria-controls="${OVERLAY}" aria-expanded="${Boolean(this.node)}">${guideMark}${escape(L.firstVisitTitle)}</button>`;
  }

  html() {
    const choice = choices[this.path];
    const row = (key, [label, hint]) =>
      `<li><button type="button" class="ws-hq-suggestion" data-hq-firstvisit-action="${key}"><span class="ws-hq-suggestion-icon" aria-hidden="true"><i class="bi bi-${{ workspace: 'folder2', email: 'envelope', apps: 'grid' }[key]}"></i></span><span><strong>${label}</strong><small>${hint}</small></span><span aria-hidden="true">→</span></button></li>`;
    const link = (url, label) =>
      `<li><a class="ws-hq-suggestion" data-hq-firstvisit-action="continue" href="${escape(handoffURL(url, this.attempt))}">${label}</a></li>`;
    const steps =
      this.path === 'workspace'
        ? link('/', 'Go to Home')
        : this.path === 'email'
          ? this.email?.setup
            ? '<li><button type="button" class="ws-hq-suggestion" data-hq-firstvisit-action="setup">Review mailbox setup</button></li>'
            : this.email?.href
              ? link(this.email.href, this.email.label)
              : ''
          : this.path === 'apps'
            ? this.appSteps()
            : Object.entries(choices)
                .map(([key, value]) => row(key, value))
                .join('');
    return `<section id="${OVERLAY}" class="ws-hq-suggestions" data-hq-firstvisit role="dialog"${this.sheet ? ' aria-modal="true"' : ''} aria-labelledby="hq-suggestions-title" aria-describedby="hq-suggestions-hint" tabindex="-1">
      <header><div><small class="ws-hq-guide-label">${guideMark}Your assistant · ${escape(L.firstVisitTitle)}</small><h2 id="hq-suggestions-title">${choice ? choice[0] : 'What would you like to do next?'}</h2><p id="hq-suggestions-hint">${choice ? choice[2] : 'Pick one to see how it works. Nothing starts here.'}</p></div><button type="button" data-hq-firstvisit-action="close" aria-label="${escape(L.firstVisitClose)}">×</button></header>
      ${this.path === 'email' ? `<p role="status">${escape(this.email?.text || 'Checking Email Ops…')}</p>` : ''}<ul>${steps}</ul>
      <footer><p id="hq-suggestions-reopen">Come back anytime: open <strong>${escape(L.firstVisitTitle)}</strong> at the top of Personal HQ.</p><button type="button" data-hq-firstvisit-action="close" aria-describedby="hq-suggestions-reopen">Maybe later</button></footer>
    </section>`;
  }

  appSteps() {
    const ctl = (action, label, disabled = false) =>
      `<li><button type="button" class="ws-hq-suggestion" data-hq-firstvisit-action="${action}"${disabled ? ' disabled' : ''}>${label}</button></li>`;
    const status = this.appMessage ? `<li role="status">${escape(this.appMessage)}</li>` : '';
    if (!this.names)
      return (
        status +
        ctl('scan', this.appBusy ? 'Checking…' : 'Start scan', this.appBusy || !this.attempt)
      );
    return (
      status +
      this.names
        .map(
          (name, i) =>
            `<li><label class="ws-hq-suggestion"><input type="checkbox" data-tool-index="${i}"${this.appBusy ? ' disabled' : ''}${this.selected.has(i) ? ' checked' : ''}> ${escape(name)}</label></li>`
        )
        .join('') +
      ctl('save', 'Save selected tools', !this.selected.size || this.appBusy) +
      ctl('none', 'Save none', this.appBusy)
    );
  }

  async appAction(action) {
    if (this.appBusy || !this.attempt) return;
    const hint = this.attempt,
      node = this.node;
    const active = () => this.node === node && this.attempt === hint;
    const post = async (url, data) => {
      const response = await fetch(url, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(data)
      });
      if (!response.ok)
        throw Error(
          response.status === 503
            ? 'Recent app evidence is unavailable on this host.'
            : 'The request failed. Try again or check Profile.'
        );
      return response.json();
    };
    this.appBusy = true;
    this.appMessage = '';
    this.paint();
    try {
      if (action === 'scan') {
        const names = reviewNames(
          (await post('/api/onboarding/detect?scope=recent-use', {})).names
        );
        if (!active()) return;
        this.names = names;
        this.selected = new Set();
        this.appMessage = names.length
          ? 'Choose tools to add to Profile.'
          : 'No recently used apps found.';
      } else {
        const status = (await this.owner.stationJSON('/api/personal-hq/status'))?.status;
        const url = guidedReturnURL(hint, status);
        if (!active()) return;
        if (!url) throw Error('The starting HQ could not be verified. Reopen the review.');
        await saveReviewedTools(
          hint,
          action === 'none' ? [] : this.names.filter((_, i) => this.selected.has(i)),
          post
        );
        if (!active()) return;
        this.close();
        if (this.owner.viewMode !== 'details')
          this.owner.setCommandViewMode('details', { focus: false });
        window.history.replaceState(window.history.state, '', url);
        this.checkedAttempt = null;
        await this.loadReturn();
        this.door()?.focus({ preventScroll: true });
      }
    } catch (err) {
      if (active()) this.appMessage = err.message;
    } finally {
      if (active()) {
        this.appBusy = false;
        this.paint();
      }
    }
  }

  bind() {
    void this.loadReturn();
    const refresh = () => {
      this.checkedAttempt = null;
      return this.loadReturn();
    };
    const returnFocus = async hint => {
      if (receiptHint(window.location, this.owner.workspaceId())?.attempt !== hint.attempt) return;
      await refresh();
      if (
        receiptHint(window.location, this.owner.workspaceId())?.attempt !== hint.attempt ||
        !this.owner.active
      )
        return;
      const controls =
        this.owner.container?.querySelectorAll?.(
          '[data-guided-receipt] button,[data-guided-receipt] summary,[data-guided-receipt] a'
        ) || [];
      [...controls].find(el => el.getClientRects().length)?.focus({ preventScroll: true });
    };
    for (const b of this.owner.container?.querySelectorAll?.('[data-documents-action]') || []) {
      b.onclick = async () => {
        const hint = receiptHint(window.location, this.owner.workspaceId());
        if (!hint?.workspace || !this.available()) return;
        const offer = this.receipt?.offer;
        const action = b.dataset.documentsAction;
        if (action === 'check') {
          await refresh();
          return;
        }
        if (action === 'open') {
          await window.PersonalAssistantSecondBrain?.openDocumentsDraft?.({
            hint,
            offer,
            trigger: b,
            result: offer.decision,
            onClose: () => returnFocus(hint)
          });
          return;
        }
        const payload =
          action === 'dismiss'
            ? { report: offer.report, candidate: offer.projects[0].ref }
            : { request_id: offer.decision.request_id };
        try {
          const r = await fetch(
            `/api/personal-assistant/folder-agent/${encodeURIComponent(hint.workspace)}/documents-draft/${action}`,
            {
              method: 'POST',
              headers: { 'Content-Type': 'application/json' },
              body: JSON.stringify({ hq: hint.hq, attempt: hint.attempt, ...payload })
            }
          );
          if (!r.ok) throw Error();
        } catch (_) {
          if (receiptHint(window.location, this.owner.workspaceId())?.attempt !== hint.attempt)
            return;
          this.receipt = {
            ...this.receipt,
            checkAgain: true,
            text: 'That decision could not be confirmed. Check again.'
          };
          this.owner.render();
          return;
        }
        await refresh();
      };
    }
    for (const button of this.owner.container?.querySelectorAll?.('[data-documents-draft]') || []) {
      button.onclick = () => {
        const hint = receiptHint(window.location, this.owner.workspaceId());
        const offer = this.receipt?.offer;
        const project = offer?.projects?.find(p => p.ref === button.dataset.documentsDraft);
        if (!hint?.workspace || !project || !this.available()) return;
        void window.PersonalAssistantSecondBrain?.openDocumentsDraft?.({
          hint,
          offer,
          project,
          trigger: button,
          onClose: () => returnFocus(hint)
        });
      };
    }
    for (const b of this.owner.container?.querySelectorAll?.('[data-documents-vault]') || []) {
      b.onclick = async () => {
        const hint = receiptHint(window.location, this.owner.workspaceId());
        const offer = this.receipt?.offer;
        const v = offer?.vaults?.find(row => row.ref === b.dataset.documentsVault);
        if (!hint?.workspace || !v) return;
        try {
          const r = await fetch(
            `/api/personal-assistant/folder-agent/${encodeURIComponent(hint.workspace)}/documents-draft/vault`,
            {
              method: 'POST',
              headers: { 'Content-Type': 'application/json' },
              body: JSON.stringify({
                hq: hint.hq,
                attempt: hint.attempt,
                report: offer.report,
                candidate: v.ref
              })
            }
          );
          const value = await r.json();
          if (!r.ok || !value.selection_token) throw Error();
          if (receiptHint(window.location, this.owner.workspaceId())?.attempt !== hint.attempt)
            return;
          await window.PersonalAssistantSecondBrain.connectSelection({
            selectionToken: value.selection_token,
            name: v.name,
            path: v.location
          });
          window.PersonalAssistantPanel?.open?.();
        } catch (_) {
          if (receiptHint(window.location, this.owner.workspaceId())?.attempt !== hint.attempt)
            return;
          this.receipt = {
            ...this.receipt,
            text: 'The vault could not be verified. Open Notes to check your connection.'
          };
          this.owner.render();
        }
      };
    }
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
      const key = offerKey(this.owner.workspaceId());
      if (localStorage.getItem(key)) return;
      localStorage.setItem(key, '1');
      document.body.dataset.guidedOfferedHere = '1';
    } catch (_) {
      return;
    }
    this.open();
  }

  open() {
    if (this.node || !this.available()) return;
    this.path = '';
    this.id = this.owner.workspaceId();
    this.sheet = window.matchMedia('(max-width: 600px)').matches;
    this.owner.hqTipLayer?.hide?.('scope');
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
    this.node.addEventListener('change', event => {
      const input = event.target.closest('[data-tool-index]');
      if (!input || this.appBusy) return;
      const index = Number(input.dataset.toolIndex);
      if (input.checked) this.selected.add(index);
      else this.selected.delete(index);
      this.node.querySelector('[data-hq-firstvisit-action="save"]').disabled = !this.selected.size;
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
      const index = focused && active?.getAttribute('data-tool-index');
      const selector =
        typeof index === 'string' && /^\d+$/.test(index)
          ? `[data-tool-index="${index}"]`
          : action
            ? `[data-hq-firstvisit-action="${action}"]`
            : '[role="dialog"]';
      this.node.innerHTML = html;
      this.lastHTML = html;
      if (focused)
        (this.node.querySelector(selector) || this.node.querySelector('[role="dialog"]')).focus({
          preventScroll: true
        });
    }
    this.position();
  }

  cleanup() {
    document.removeEventListener('pointerdown', this.outside, true);
    window.removeEventListener('resize', this.resize);
    window.removeEventListener('scroll', this.resize, true);
    this.node?.remove();
    this.node = null;
    this.attempt = null;
    this.names = null;
    this.selected = null;
    this.appMessage = '';
    this.appBusy = false;
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
      this.email = null;
      const hadReceipt = Boolean(this.receipt);
      this.receipt = null;
      this.attempt = beginHQAttempt(this.owner.workspaceId(), action);
      if (hadReceipt && this.owner.active) this.owner.render();
      if (!this.attempt)
        this.appMessage =
          'Guided return is unavailable. Allow tab storage or use the standalone destination.';
      this.paint();
      this.node?.querySelector('[role="dialog"]')?.focus({ preventScroll: true });
      if (action === 'email') {
        const hint = this.attempt;
        try {
          const next = await emailNextStep(url => this.owner.stationJSON(url));
          if (this.attempt !== hint) return;
          this.email =
            next.workspace && !bindEmailAttempt(hint, next.workspace, next.account)
              ? { text: 'Cannot safely resume mailbox setup in this tab.' }
              : next;
        } catch (_) {
          this.email = { text: 'Mailbox readiness could not be verified. Reopen to try again.' };
        }
        this.paint();
      }
      return;
    }
    if (this.path === 'apps' && ['scan', 'save', 'none'].includes(action))
      return this.appAction(action);
    if (action === 'setup' && this.path === 'email' && this.email?.setup) {
      const hint = this.attempt;
      if (!hint || !window.OriEmailSetup) {
        this.email = { text: 'Mailbox setup is unavailable in this tab.' };
        this.paint();
        return;
      }
      const status = (await this.owner.stationJSON('/api/personal-hq/status').catch(() => null))
        ?.status;
      const url = guidedReturnURL(hint, status);
      if (!this.node || this.attempt !== hint) return;
      if (!url) {
        this.email = {
          text: 'The starting HQ could not be verified. Reopen Connect email to try again.'
        };
        this.paint();
        return;
      }
      this.close(false);
      if (this.owner.viewMode !== 'details')
        this.owner.setCommandViewMode('details', { focus: false });
      window.history.replaceState(window.history.state, '', url);
      const resume = result => {
        if (!currentHQAttempt(hint.hq, 'email', hint.attempt)) return;
        if (result && !bindEmailAttempt(hint, result.workspace?.id, result.account_id)) {
          this.checkedAttempt = hint.attempt;
          this.receipt = {
            text: 'The mailbox destination changed. Reopen Connect email to verify it.'
          };
          this.owner.render();
        } else {
          this.checkedAttempt = null;
          void this.loadReturn();
        }
        this.door()?.focus({ preventScroll: true });
      };
      window.OriEmailSetup.open({
        guided: true,
        onConnected: result => bindEmailAttempt(hint, result.workspace?.id, result.account_id),
        onDone: resume,
        onCancel: () => resume(null)
      });
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
