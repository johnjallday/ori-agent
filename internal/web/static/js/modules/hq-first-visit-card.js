// Stateful adapter for the pure first-visit model. Workspace Command owns the
// page and its existing panels; this card never creates an alternate station.
import { HQ_LABELS as L } from './hq-labels.js';
import {
  firstVisitView,
  firstVisitResolved,
  firstVisitShowAll,
  rememberFirstVisitShowAll
} from './hq-first-visit.js';
import { readNoteDraft, writeNoteDraft, saveFirstNote } from './hq-first-note.js';
import { dailyBriefStatus } from './daily-brief-station.js';
import { EXPLORE_FOLDER_URL } from './personal-assistant-panel.js';

const escape = value =>
  String(value ?? '').replace(
    /[&<>"']/g,
    ch => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[ch]
  );
const storage = () => {
  try {
    return window.localStorage;
  } catch (_) {
    return null;
  }
};

export class HQFirstVisitCard {
  constructor(owner) {
    this.owner = owner;
    this.state = typeof document === 'undefined' ? '' : document.body?.dataset?.hqFirstVisit || '';
    this.missions = null;
    this.started = false;
    this.busy = false;
    this.error = '';
    this.savedText = '';
    this.resolvedOnLoad = null;
    this.showAll = null;
  }

  view() {
    const owner = this.owner;
    if (this.showAll === null) this.showAll = firstVisitShowAll(storage(), owner.workspaceId());
    return firstVisitView({
      state: this.state,
      missions: this.missions,
      notes: owner.notesState().secondBrain,
      hasBrief: dailyBriefStatus(owner.dailyBriefStationData()).today === true,
      showAll: this.showAll,
      resolvedOnLoad: this.resolvedOnLoad === true && !this.error,
      savedText: this.savedText,
      tickets: owner.page?.tasks || []
    });
  }

  ensure() {
    if (this.state !== 'pending' || this.started || !this.owner.isPersonalHQ()) return;
    this.started = true;
    if (typeof window !== 'undefined') {
      window.addEventListener?.('pageshow', event => {
        if (event.persisted) void this.load();
      });
    }
    this.owner.requestNotesData();
    this.owner.requestDailyBriefStationData();
    void this.load();
  }

  async load({ focus = false } = {}) {
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
    if (focus) this.focus();
  }

  focus(action) {
    const selector = action ? `[data-hq-firstvisit-action="${action}"]` : '[data-hq-firstvisit]';
    this.owner.container?.querySelector?.(selector)?.focus?.({ preventScroll: true });
  }

  captureFocus() {
    const input = this.owner.container?.querySelector?.('[data-hq-firstvisit-note]');
    if (input && typeof document !== 'undefined' && document.activeElement === input)
      this.caret = [input.selectionStart, input.selectionEnd];
  }

  html() {
    const v = this.view();
    if (v.card === 'none') return '';
    const button = (action, label, disabled = false) =>
      `<button type="button" class="ws-cmd-agent-action" data-hq-firstvisit-action="${action}"${disabled || this.busy ? ' disabled' : ''}>${escape(label)}</button>`;
    const live = `<p class="ws-hq-firstvisit-status${this.error ? '' : ' visually-hidden'}" aria-live="polite" aria-atomic="true">${escape(this.error || (this.busy ? L.firstVisitSaving : v.loading ? L.firstVisitLoading : this.savedText ? L.firstNoteSaved.replace('{text}', this.savedText) : ''))}</p>`;
    const start = `<section class="ws-hq-panel ws-hq-firstvisit is-${v.card}" data-hq-firstvisit tabindex="-1" aria-labelledby="hq-firstvisit-title">`;
    if (v.card === 'bar')
      return (
        start +
        `<h3 id="hq-firstvisit-title">${escape(v.journey[2])} · ${escape(v.barProgress)}</h3><div class="ws-hq-firstvisit-controls">${button('continue', L.firstVisitContinue)}${button('hide', L.firstVisitHide)}</div>${live}</section>`
      );
    if (v.card === 'finished')
      return (
        start +
        `<h3 id="hq-firstvisit-title">${escape(L.firstVisitFinishedTitle)}</h3><p>${escape(L.firstVisitFinishedText)}</p>${button('done', L.firstVisitDone)}${live}</section>`
      );
    const lead = this.owner.hqTeam().find(agent => agent.isLead);
    const face =
      (lead &&
        this.owner.page
          ?.getAgentAvatarPresentation?.(lead.name)
          ?.markup?.('ws-hq-firstvisit-avatar')) ||
      '';
    const draft = readNoteDraft();
    const source = this.owner.notesState().secondBrain?.source;
    if (!draft.sourceId && source?.origin === 'created_by_ori')
      writeNoteDraft({ ...draft, sourceId: source.id });
    return (
      start +
      `<ol class="ws-hq-firstvisit-journey">${v.journey.map((label, index) => `<li${index === 2 ? ' aria-current="step"' : ''}>${index < 2 ? '<span aria-hidden="true">✓</span> ' : ''}${escape(label)}${index === 2 ? ' · ' + escape(v.progress) : ''}</li>`).join('')}</ol>` +
      `<header class="ws-hq-firstvisit-intro">${face}<div>${lead ? `<span class="ws-hq-firstvisit-lead">${escape(lead.name)}</span>` : ''}<h3 id="hq-firstvisit-title">${escape(L.firstVisitTitle)}</h3><p>${escape(L.firstVisitLead)}</p></div></header>` +
      `<ol class="ws-hq-firstvisit-steps">${v.steps
        .map((step, index) => {
          let action = '';
          if (step.action === 'save')
            action = `<label for="hq-firstvisit-note">${escape(L.firstNoteField)}</label><textarea id="hq-firstvisit-note" data-hq-firstvisit-note maxlength="4000" rows="2"${this.busy || draft.pendingText ? ' readonly' : ''}>${escape(draft.text)}</textarea>${button('save', step.button, step.disabled)}`;
          else if (step.action === 'folder' || step.action === 'home')
            action = step.disabled
              ? button(step.action, step.button, true)
              : `<a class="ws-cmd-agent-action" href="${step.action === 'folder' ? EXPLORE_FOLDER_URL : '/'}">${escape(step.button)}</a>`;
          else if (step.action) action = button(step.action, step.button, step.disabled);
          return `<li class="ws-hq-firstvisit-step${step.resolved ? ' is-resolved' : ''}"><span class="ws-hq-firstvisit-number" aria-hidden="true">${step.resolved ? '✓' : index + 1}</span><div><h4>${escape(step.title)}</h4><p>${escape(step.hint)}</p>${action}</div></li>`;
        })
        .join('')}</ol>` +
      (v.footer
        ? `<p class="ws-hq-firstvisit-footer">${escape(v.footer)} ${button('tickets', L.firstVisitSeeTickets)}</p>`
        : '') +
      `<div class="ws-hq-firstvisit-controls">${button('show-all', L.firstVisitShowAll)}${this.error === L.firstVisitLoadError ? button('retry', L.firstVisitRetry) : ''}</div>${live}</section>`
    );
  }

  bind() {
    const root = this.owner.container?.querySelector?.('[data-hq-firstvisit]');
    root?.addEventListener?.('click', event => {
      const button = event.target.closest?.('[data-hq-firstvisit-action]');
      if (button && !button.disabled)
        void this.act(button.getAttribute('data-hq-firstvisit-action'), button);
    });
    const input = root?.querySelector?.('[data-hq-firstvisit-note]');
    input?.addEventListener?.('input', () =>
      writeNoteDraft({ ...readNoteDraft(), text: input.value })
    );
    if (input && this.caret) {
      input.focus({ preventScroll: true });
      input.setSelectionRange?.(...this.caret);
      this.caret = null;
    }
  }

  async act(action, trigger) {
    if (this.busy) return;
    if (action === 'save') return this.save();
    if (action === 'hide' || action === 'done') return this.finish();
    if (action === 'retry') return this.load();
    if (action === 'show-all' || action === 'continue') {
      this.showAll = action === 'show-all';
      rememberFirstVisitShowAll(storage(), this.owner.workspaceId(), this.showAll);
      this.owner.render();
      this.focus(this.showAll ? 'continue' : 'show-all');
    } else if (action === 'notes') this.owner.openNotesStation(trigger);
    else if (action === 'brief') {
      this.briefOpened = true;
      this.owner.openDailyBriefPanel(trigger);
    } else if (action === 'tickets') this.owner.setCommandViewMode('tickets');
  }

  async briefClosed() {
    if (!this.briefOpened) return;
    this.briefOpened = false;
    await this.load({ focus: true });
  }

  async save() {
    this.busy = true;
    this.error = '';
    this.owner.render();
    try {
      const saved = await saveFirstNote(readNoteDraft().text, {
        readStatus: async () =>
          (await this.owner.stationJSON('/api/personal-assistant/second-brain')).second_brain
      });
      this.savedText = saved.text;
      await this.load();
      this.owner.requestNotesData(true);
      this.owner.notesChanged();
    } catch (error) {
      this.error = error.message || L.firstNoteError;
    } finally {
      this.busy = false;
      this.owner.render();
      this.focus(this.savedText ? 'brief' : 'save');
    }
  }

  async finish() {
    this.busy = true;
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
      rememberFirstVisitShowAll(storage(), this.owner.workspaceId(), false);
    } catch (_) {
      this.error = L.firstVisitFinishError;
    } finally {
      this.busy = false;
      if (this.owner.active) this.owner.render();
    }
  }
}
