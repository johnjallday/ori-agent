// Plan detail: the one canonical review, edit, and approval surface for a
// durable Plan (FR-148, FR-149).
//
// This file starts as the read surface — overview, next action, derived
// progress, and lifecycle activity. Drafting, clarification, review,
// comparison, and approval controls attach to the same page rather than to a
// second editor elsewhere in the app.

import {
  escapeHtml,
  formatTimestamp,
  nextActionFor,
  progressSummary,
  statusMeta,
  versionLabel
} from './workspace-plan.js';
import {
  addGroup,
  addItem,
  duplicateItem,
  EditorState,
  moveGroup,
  moveItem,
  removeGroup,
  removeItem,
  resolveDependencies,
  saveStateMeta,
  unavailableAssignees
} from './workspace-plan-editor.js';

// clarificationLine renders one question with its answer state. A skipped
// question reads as skipped, not as unanswered: they mean different things
// (FR-24, FR-28).
export function clarificationLine(question) {
  switch (question?.status) {
    case 'answered':
      return `Answered: ${question.answer}`;
    case 'skipped':
      return question.skip_reason ? `Skipped — ${question.skip_reason}` : 'Skipped';
    default:
      return question?.required ? 'Required — not yet answered' : 'Optional — not yet answered';
  }
}

// AUTOSAVE_DELAY_MS is how long editing pauses before a recovery point is
// written. Long enough not to snapshot every keystroke, short enough that a
// lost tab costs a sentence rather than a session.
export const AUTOSAVE_DELAY_MS = 4000;

// activityLine renders one lifecycle history entry. Transitions read as a move
// between two named states; audit entries read as what happened (FR-15, FR-80).
export function activityLine(entry) {
  const when = formatTimestamp(entry?.created_at);
  const actor = entry?.actor ? ` by ${entry.actor}` : '';
  const reason = entry?.reason ? ` — ${entry.reason}` : '';

  if (entry?.kind === 'status_change' && entry.from && entry.to) {
    return `${statusMeta(entry.from).label} → ${statusMeta(entry.to).label}${actor}${reason} (${when})`;
  }
  if (entry?.kind === 'created') {
    return `Plan created${actor} (${when})`;
  }
  const label = String(entry?.kind || 'activity').replace(/_/g, ' ');
  return `${label.charAt(0).toUpperCase()}${label.slice(1)}${actor}${reason} (${when})`;
}

export class WorkspacePlanPage {
  constructor(workspaceId, planId, options = {}) {
    this.workspaceId = workspaceId;
    this.planId = planId;
    this.doc = options.document || (typeof document !== 'undefined' ? document : null);
    this.fetchImpl =
      options.fetch || (typeof fetch !== 'undefined' ? fetch.bind(globalThis) : null);
    this.plan = null;
    this.editor = null;
    // availableAgents stays null until the roster is known. Null means "not
    // checked"; an empty array means "this workspace has no agents". Treating
    // the first as the second would flag every assignment as unavailable.
    this.availableAgents = null;
    this.autosaveTimer = null;
    this.setTimeoutImpl = options.setTimeout || ((fn, ms) => setTimeout(fn, ms));
    this.clearTimeoutImpl = options.clearTimeout || (id => clearTimeout(id));
  }

  el(selector) {
    return this.doc ? this.doc.querySelector(selector) : null;
  }

  async init() {
    this.bindEvents();
    await this.reload();
  }

  // Editor controls are delegated from one listener, because the task list is
  // re-rendered on every change and per-button listeners would be rebound each
  // time. Every control is a real button, so this works for click and for the
  // keyboard activation the browser turns into a click (FR-160).
  bindEvents() {
    const groups = this.el('#plan-editor-groups');
    if (groups) {
      groups.addEventListener('click', event => this.handleEditorAction(event));
    }

    const addGroupBtn = this.el('#plan-add-group');
    if (addGroupBtn) {
      addGroupBtn.addEventListener('click', () => this.addGroup('New task group'));
    }
    const saveBtn = this.el('#plan-save');
    if (saveBtn) {
      saveBtn.addEventListener('click', () => void this.save());
    }
    const keepMine = this.el('#plan-conflict-keep-mine');
    if (keepMine) {
      keepMine.addEventListener('click', () => void this.keepMineAfterConflict());
    }
    const discard = this.el('#plan-conflict-discard');
    if (discard) {
      discard.addEventListener('click', () => void this.discardConflict());
    }
  }

  handleEditorAction(event) {
    const button = event?.target?.closest?.('button[data-action]');
    if (!button) return;

    const { action, itemId, groupId } = button.dataset;
    switch (action) {
      case 'item-up':
        this.moveItem(itemId, 'up');
        break;
      case 'item-down':
        this.moveItem(itemId, 'down');
        break;
      case 'item-duplicate':
        this.duplicateItem(itemId);
        break;
      case 'item-remove':
        this.removeItem(itemId);
        break;
      case 'group-up':
        this.moveGroup(groupId, 'up');
        break;
      case 'group-down':
        this.moveGroup(groupId, 'down');
        break;
      case 'group-add-item':
        this.addItem(groupId, 'New task');
        break;
      case 'group-remove':
        this.removeGroup(groupId);
        break;
      default:
        break;
    }
  }

  async request(path, options = {}) {
    if (!this.fetchImpl) throw new Error('fetch is unavailable');
    const response = await this.fetchImpl(path, {
      headers: { 'Content-Type': 'application/json' },
      ...options
    });
    const body = await response.json().catch(() => ({}));
    if (!response.ok) {
      const error = new Error(body.message || 'Request failed');
      error.code = body.code || 'internal_error';
      error.status = response.status;
      error.details = body.details || null;
      throw error;
    }
    return body;
  }

  basePath() {
    return `/api/workspaces/${encodeURIComponent(this.workspaceId)}/plans/${encodeURIComponent(this.planId)}`;
  }

  async reload() {
    try {
      const plan = await this.request(this.basePath());
      this.plan = plan;
      // A reload replaces the editor only when there is nothing unsaved to
      // lose. Refreshing away someone's in-progress edit is the bug autosave
      // exists to prevent.
      if (!this.editor || this.editor.state === 'saved') {
        this.editor = new EditorState(plan);
      }
      const activity = await this.request(`${this.basePath()}/activity`).catch(() => ({
        activity: []
      }));
      this.render(plan, activity.activity || []);
    } catch (error) {
      this.renderMissing(error);
    }
  }

  // --- Editing -------------------------------------------------------------

  // mutate applies a structural operation, reports a refusal, and schedules an
  // autosave recovery point.
  mutate(operation, { announce = '' } = {}) {
    if (!this.editor) return null;
    const result = this.editor.apply(operation);

    if (result && result.removed === false) {
      const names = result.blockedBy.map(entry => entry.description || entry.id).join(', ');
      this.announce(
        `Cannot remove this yet — ${names} still depends on it. Remove that dependency first.`,
        'error'
      );
      this.renderEditor();
      this.renderSaveState();
      return result;
    }

    if (announce) this.announce(announce);
    this.renderEditor();
    // The indicator has to move the moment content changes, or the page keeps
    // claiming "All changes saved" over an edit that is not saved (FR-151).
    this.renderSaveState();
    this.scheduleAutosave();
    return result;
  }

  addGroup(title) {
    return this.mutate(content => addGroup(content, title), { announce: 'Task group added' });
  }

  addItem(groupId, description) {
    return this.mutate(content => addItem(content, groupId, description), {
      announce: 'Task added'
    });
  }

  duplicateItem(itemId) {
    return this.mutate(content => duplicateItem(content, itemId), { announce: 'Task duplicated' });
  }

  moveGroup(groupId, direction) {
    return this.mutate(content => moveGroup(content, groupId, direction), {
      announce: `Task group moved ${direction}`
    });
  }

  moveItem(itemId, direction) {
    return this.mutate(content => moveItem(content, itemId, direction), {
      announce: `Task moved ${direction}`
    });
  }

  removeItem(itemId) {
    return this.mutate(content => removeItem(content, itemId), { announce: 'Task removed' });
  }

  removeGroup(groupId) {
    return this.mutate(content => removeGroup(content, groupId), {
      announce: 'Task group removed'
    });
  }

  // resolveDependencies is the explicit repair a refused removal points at. It
  // is never applied on the user's behalf (FR-51).
  resolveDependencies(itemId) {
    return this.mutate(content => resolveDependencies(content, itemId), {
      announce: 'Dependencies on this task were removed'
    });
  }

  scheduleAutosave() {
    if (!this.editor) return;
    this.clearTimeoutImpl(this.autosaveTimer);
    this.autosaveTimer = this.setTimeoutImpl(() => {
      void this.save({ autosave: true });
    }, AUTOSAVE_DELAY_MS);
  }

  // save writes the draft under its revision token. A conflict is not a
  // failure to retry: it keeps both versions and asks the user to choose
  // (FR-30, FR-151).
  async save({ autosave = false } = {}) {
    if (!this.editor) return null;
    this.clearTimeoutImpl(this.autosaveTimer);

    this.editor.markSaving();
    this.renderSaveState();
    try {
      const plan = await this.request(`${this.basePath()}/draft`, {
        method: 'PATCH',
        body: JSON.stringify(this.editor.payload({ autosave }))
      });
      this.plan = plan;
      this.editor.markSaved(plan);
      this.renderSaveState();
      if (!autosave) this.announce('All changes saved');
      return plan;
    } catch (error) {
      if (error.code === 'stale_draft') {
        this.editor.markConflicted(error.details);
        this.renderSaveState();
        this.renderConflict();
        this.announce(
          'Someone else saved this plan first. Your changes are kept — choose which version to keep.',
          'error'
        );
        return null;
      }
      this.editor.state = 'unsaved';
      this.renderSaveState();
      this.announce(`Could not save: ${error.message}`, 'error');
      return null;
    }
  }

  // discardConflict abandons the user's version in favour of the one that won.
  // It is only ever reached by an explicit choice.
  async discardConflict() {
    this.editor = null;
    await this.reload();
    this.announce('Your unsaved changes were discarded');
  }

  // keepMineAfterConflict re-applies the user's content on top of the winning
  // revision, so their work survives a conflict they chose to win.
  async keepMineAfterConflict() {
    if (!this.editor?.conflict) return null;
    const winning = this.editor.conflict.currentRevision;
    if (winning != null) this.editor.revision = winning;
    this.editor.state = 'unsaved';
    return this.save();
  }

  render(plan, activity) {
    const loading = this.el('#plan-loading');
    if (loading) loading.hidden = true;
    const missing = this.el('#plan-missing');
    if (missing) missing.hidden = true;
    const content = this.el('#plan-content');
    if (content) content.hidden = false;

    this.setText('#plan-title', plan.title || 'Untitled plan');
    this.setText('#plan-breadcrumb-title', plan.title || 'Plan');
    this.setText('#plan-objective', plan.objective || '');
    this.setText('#plan-original-request', plan.original_request || '');
    this.setText('#plan-version-label', versionLabel(plan));
    this.setText('#plan-next-action', nextActionFor(plan).label);

    const progress = progressSummary(plan);
    this.setText(
      '#plan-progress',
      progress.detail ? `${progress.text} · ${progress.detail}` : progress.text
    );

    const badge = this.el('#plan-status-badge');
    if (badge) {
      const meta = statusMeta(plan.status);
      badge.dataset.tone = meta.tone;
      badge.innerHTML =
        `<span class="plan-status-icon" aria-hidden="true">${escapeHtml(meta.icon)}</span>` +
        `<span class="plan-status-label">${escapeHtml(meta.label)}</span>`;
    }

    const list = this.el('#plan-activity-list');
    if (list) {
      list.innerHTML = activity
        .map(entry => `<li>${escapeHtml(activityLine(entry))}</li>`)
        .join('');
    }

    this.renderClarifications(plan);
    this.renderEditor();
    this.renderSaveState();
  }

  renderClarifications(plan) {
    const section = this.el('#plan-clarifications-section');
    const list = this.el('#plan-clarifications-list');
    const questions = plan?.draft?.clarifications || [];

    if (section) section.hidden = questions.length === 0;
    if (!list) return;

    list.innerHTML = questions
      .map(question => {
        const required = question.required ? 'Required' : 'Optional';
        return `
          <li class="plan-clarification" data-clarification-id="${escapeHtml(question.id)}" data-status="${escapeHtml(question.status || 'open')}">
            <p class="plan-clarification-prompt">${escapeHtml(question.prompt)}</p>
            <p class="plan-clarification-state">
              <span class="plan-clarification-required">${escapeHtml(required)}</span>
              <span>${escapeHtml(clarificationLine(question))}</span>
            </p>
          </li>`;
      })
      .join('');
  }

  // renderEditor draws the typed task structure. Every control is a real
  // button with a label, so the whole editor is keyboard operable and reorder
  // never depends on dragging (FR-160).
  renderEditor() {
    const container = this.el('#plan-editor-groups');
    if (!container || !this.editor) return;

    const groups = this.editor.content.groups || [];
    const unavailable = new Set(
      unavailableAssignees(this.editor.content, this.availableAgents).map(entry => entry.id)
    );

    container.innerHTML = groups
      .map((group, groupIndex) => {
        const items = (group.items || [])
          .map((item, itemIndex) => {
            const warning = unavailable.has(item.id)
              ? `<p class="plan-item-warning">Assignee “${escapeHtml(item.assignee)}” is no longer available. Reassign it or leave it unassigned before review.</p>`
              : '';
            return `
              <li class="plan-item" data-item-id="${escapeHtml(item.id)}">
                <p class="plan-item-description">${escapeHtml(item.description)}</p>
                ${item.assignee ? `<p class="plan-item-assignee">Assigned to ${escapeHtml(item.assignee)}</p>` : '<p class="plan-item-assignee">Unassigned</p>'}
                ${warning}
                <div class="plan-item-actions">
                  <button type="button" data-action="item-up" data-item-id="${escapeHtml(item.id)}" ${itemIndex === 0 ? 'disabled' : ''}>Move up</button>
                  <button type="button" data-action="item-down" data-item-id="${escapeHtml(item.id)}" ${itemIndex === (group.items || []).length - 1 ? 'disabled' : ''}>Move down</button>
                  <button type="button" data-action="item-duplicate" data-item-id="${escapeHtml(item.id)}">Duplicate</button>
                  <button type="button" data-action="item-remove" data-item-id="${escapeHtml(item.id)}">Remove</button>
                </div>
              </li>`;
          })
          .join('');

        return `
          <section class="plan-group" data-group-id="${escapeHtml(group.id)}" aria-label="${escapeHtml(group.title)}">
            <div class="plan-group-head">
              <h3 class="plan-group-title">${escapeHtml(group.title)}</h3>
              <div class="plan-group-actions">
                <button type="button" data-action="group-up" data-group-id="${escapeHtml(group.id)}" ${groupIndex === 0 ? 'disabled' : ''}>Move group up</button>
                <button type="button" data-action="group-down" data-group-id="${escapeHtml(group.id)}" ${groupIndex === groups.length - 1 ? 'disabled' : ''}>Move group down</button>
                <button type="button" data-action="group-add-item" data-group-id="${escapeHtml(group.id)}">Add task</button>
                <button type="button" data-action="group-remove" data-group-id="${escapeHtml(group.id)}">Remove group</button>
              </div>
            </div>
            ${group.outcome ? `<p class="plan-group-outcome">${escapeHtml(group.outcome)}</p>` : ''}
            <ol class="plan-item-list">${items}</ol>
          </section>`;
      })
      .join('');

    const empty = this.el('#plan-editor-empty');
    if (empty) empty.hidden = groups.length > 0;
  }

  // renderSaveState pairs the state with a word and an icon, so "unsaved" is
  // never conveyed by color alone (FR-151, FR-162).
  renderSaveState() {
    const node = this.el('#plan-save-state');
    if (!node || !this.editor) return;
    const meta = saveStateMeta(this.editor.state);
    node.dataset.tone = meta.tone;
    node.dataset.state = this.editor.state;
    node.textContent = `${meta.icon} ${meta.label}`;
  }

  renderConflict() {
    const panel = this.el('#plan-conflict-panel');
    if (!panel || !this.editor?.conflict) return;
    panel.hidden = false;

    const summary = this.el('#plan-conflict-summary');
    if (summary) {
      const revision = this.editor.conflict.currentRevision;
      summary.textContent =
        `Another session saved revision ${revision} while you were editing. ` +
        `Your changes are still here — keep yours, or discard them and load theirs.`;
    }
  }

  // announce routes user-visible feedback through a live region, so a screen
  // reader hears the outcome of an action it cannot see (FR-161).
  announce(message, tone = 'info') {
    const node = this.el('#plan-detail-status');
    if (!node) return;
    node.hidden = !message;
    node.dataset.tone = tone;
    node.textContent = message;
  }

  renderMissing(error) {
    const loading = this.el('#plan-loading');
    if (loading) loading.hidden = true;
    const content = this.el('#plan-content');
    if (content) content.hidden = true;
    const missing = this.el('#plan-missing');
    if (missing) {
      missing.hidden = false;
      if (error && error.code && error.code !== 'plan_not_found') {
        missing.textContent = `Could not load plan: ${error.message}`;
      }
    }
  }

  setText(selector, value) {
    const node = this.el(selector);
    if (node) node.textContent = value;
  }
}
