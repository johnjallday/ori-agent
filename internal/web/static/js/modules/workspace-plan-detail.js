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
import { POLL_INTERVAL_MS, primaryBlocker, shouldPoll } from './workspace-plan-blockers.js';
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

// materializedMessage says what was created AND what happens next.
//
// Both halves matter. "Created 3 tasks" alone leaves the user guessing whether
// anything is now running, which is exactly the question the approval label
// was careful to answer (FR-64, FR-65).
export function materializedMessage(result) {
  const tasks = result?.task_ids?.length || 0;
  const files = result?.artifact_paths?.length || 0;

  const created = result?.replayed
    ? `This approval was already used. It created ${tasks} task(s)`
    : `Created ${tasks} task(s)`;
  const withFiles = files > 0 ? `${created} and ${files} file(s)` : created;
  const next = result?.start_execution
    ? 'Eligible tasks will start automatically.'
    : 'Nothing starts until you start it.';
  const handoff = result?.handoff?.command
    ? ` Planning is complete; run “${result.handoff.command}”. Do not run wt plan again.`
    : '';

  return `${withFiles}. ${next}${handoff}`;
}

// approvalButtonLabel returns the exact wording the primary action must carry.
// It comes from the server's contract rather than being derived here, so the
// label and the behavior behind it cannot drift apart (FR-64, FR-65).
export function approvalButtonLabel(contract) {
  const label = String(contract?.action_label || '').trim();
  if (label) return label;
  // A contract with no label is a bug, not a reason to render a generic
  // "Approve" over an action that might start agents (PRD 6.3).
  return contract?.starts_execution ? 'Approve and Start' : 'Approve and Create Tasks';
}

// diffSummary turns a version comparison into one line per change, saying what
// happened rather than only that something did (FR-36).
export function diffSummary(diff) {
  if (!diff || diff.identical) return ['No approval-relevant changes'];

  const lines = [];
  if (diff.objective) {
    lines.push(`Objective changed to “${diff.objective.after}”`);
  }
  for (const change of diff.in_scope || []) {
    lines.push(`Scope ${change.kind}: ${change.value}`);
  }
  for (const change of diff.non_goals || []) {
    lines.push(`Non-goal ${change.kind}: ${change.value}`);
  }
  for (const change of diff.groups || []) {
    lines.push(groupChangeLine(change));
  }
  for (const change of diff.items || []) {
    lines.push(itemChangeLine(change));
  }
  for (const change of diff.artifacts || []) {
    lines.push(`Artifact ${change.kind}: ${change.label}${fieldSuffix(change.fields)}`);
  }
  for (const change of diff.validations || []) {
    lines.push(`Validation ${change.kind}: ${change.label}${fieldSuffix(change.fields)}`);
  }
  if (diff.execution) {
    lines.push(`Execution mode changed from ${diff.execution.before} to ${diff.execution.after}`);
  }
  for (const change of diff.preconditions || []) {
    lines.push(`Enforced precondition ${change.kind}: ${change.value}`);
  }
  return lines.length > 0 ? lines : ['No approval-relevant changes'];
}

// DISPOSITION_LABELS says what happens to one piece of existing work, in the
// words a user would use. The disposition keys are the server's; the wording is
// the UI's, so a schema value never leaks onto the screen (FR-162).
const DISPOSITION_LABELS = {
  retained: 'Kept',
  created: 'New',
  cancel: 'Will be cancelled',
  replace: 'Will be cancelled and recreated',
  immutable: 'Left alone',
  follow_up: 'Follow-up work added'
};

// reconcileLine renders one entry of the preview: what happens, to what, and
// why. The reason comes from the server so the screen cannot claim a different
// justification than the one the decision was made on.
export function reconcileLine(entry) {
  const label = DISPOSITION_LABELS[entry?.disposition] || 'Unchanged';
  const description = entry?.description || entry?.item_id || 'this step';
  const reason = entry?.reason ? ` — ${entry.reason}` : '';
  return `${label}: ${description}${reason}`;
}

// reconcileSummary states the headline in counts, leading with what is lost.
//
// Cancellations come first because they are the part a user can regret. A
// summary that opened with "3 new tasks" would bury the sentence that actually
// needs reading.
export function reconcileSummary(preview) {
  if (!preview) return '';

  const summary = preview.summary || {};
  const cancelled = (summary.cancel || 0) + (summary.replace || 0);
  const parts = [];
  if (cancelled > 0) parts.push(`${cancelled} existing task(s) will be cancelled`);
  if (summary.immutable > 0) parts.push(`${summary.immutable} already ran and are left alone`);
  if (summary.follow_up > 0) parts.push(`${summary.follow_up} follow-up task(s) will be added`);
  if (summary.created > 0) parts.push(`${summary.created} new task(s) will be created`);
  if (summary.retained > 0) parts.push(`${summary.retained} kept as-is`);

  if (parts.length === 0) return 'This revision changes no existing work.';
  return `${parts.join(', ')}.`;
}

// reconcileRunsNote names the Runs attached to work that is about to be
// cancelled, so a cancellation never quietly discards a run's results (FR-154).
export function reconcileRunsNote(preview) {
  const runs = preview?.affected_run_ids || [];
  if (runs.length === 0) return '';
  return `${runs.length} run(s) are attached to the affected work. Their records are kept.`;
}

function groupChangeLine(change) {
  if (change.kind === 'moved') {
    return `Group moved: ${change.title} (position ${change.from_index + 1} → ${change.to_index + 1})`;
  }
  return `Group ${change.kind}: ${change.title}${fieldSuffix(change.fields)}`;
}

function itemChangeLine(change) {
  if (change.kind === 'moved') {
    const across = change.from_group_id && change.from_group_id !== change.group_id;
    return across
      ? `Task moved to another group: ${change.description}`
      : `Task reordered: ${change.description} (position ${change.from_index + 1} → ${change.to_index + 1})`;
  }
  return `Task ${change.kind}: ${change.description}${fieldSuffix(change.fields)}`;
}

function fieldSuffix(fields) {
  if (!fields || fields.length === 0) return '';
  return ` (${fields.join(', ')})`;
}

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
    // contract is the loaded review contract. Approval binds to the version
    // and hash it carries, so it is held rather than re-derived.
    this.contract = null;
    // materialization is the last result of spending an approval, including a
    // failure, so the panel can offer a retry.
    this.materialization = null;
    // availableAgents stays null until the roster is known. Null means "not
    // checked"; an empty array means "this workspace has no agents". Treating
    // the first as the second would flag every assignment as unavailable.
    this.availableAgents = null;
    this.autosaveTimer = null;
    // pollTimer refreshes a plan whose work can still move. Null when nothing
    // can change, so a finished plan stops costing requests.
    this.pollTimer = null;
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

    const requestReview = this.el('#plan-request-review');
    if (requestReview) {
      requestReview.addEventListener('click', () => void this.requestReview());
    }
    const approve = this.el('#plan-approve');
    if (approve) {
      approve.addEventListener('click', () => void this.approve());
    }
    const requestChanges = this.el('#plan-request-changes');
    if (requestChanges) {
      requestChanges.addEventListener('click', () => void this.decide('request_changes'));
    }
    const reject = this.el('#plan-reject');
    if (reject) {
      reject.addEventListener('click', () => void this.decide('reject'));
    }
    const retry = this.el('#plan-materialization-retry');
    if (retry) {
      retry.addEventListener('click', () => {
        const approvalID = retry.dataset.approvalId;
        if (approvalID) void this.materialize(approvalID);
      });
    }

    const confirmReconcile = this.el('#plan-reconcile-confirm');
    if (confirmReconcile) {
      confirmReconcile.addEventListener('click', () => void this.confirmReconciliation());
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
      // Reconciliation is loaded after the plan renders rather than as part of
      // it: most plans are not revisions, so this request usually 4xxs, and
      // waiting on it would delay the page everyone else sees.
      await this.loadReconciliation();
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

  // --- Review and approval -------------------------------------------------

  // requestReview snapshots the current draft as an immutable version and
  // loads its review contract.
  async requestReview(actor = '') {
    try {
      const version = await this.request(`${this.basePath()}/versions`, {
        method: 'POST',
        body: JSON.stringify({ actor })
      });
      this.announce(`Version ${version.version} is ready for review`);
      await this.reload();
      await this.loadReviewContract(version.version);
      return version;
    } catch (error) {
      this.announce(`Could not request review: ${error.message}`, 'error');
      return null;
    }
  }

  // loadReviewContract fetches everything the user must see before approving.
  // The contract is rendered as-is; the page never composes its own summary of
  // effects, because a summary that drifts from the behavior is worse than no
  // summary (FR-62, FR-63).
  async loadReviewContract(version) {
    try {
      const contract = await this.request(`${this.basePath()}/versions/${version}`);
      this.contract = contract;
      this.renderReviewContract(contract);
      return contract;
    } catch (error) {
      this.announce(`Could not load the review: ${error.message}`, 'error');
      return null;
    }
  }

  renderReviewContract(contract) {
    const panel = this.el('#plan-review-panel');
    if (panel) panel.hidden = !contract;
    if (!contract) return;

    this.setText('#plan-review-version', `Version ${contract.version}`);
    this.setText('#plan-review-objective', contract.objective || '');
    this.setText(
      '#plan-review-counts',
      `${contract.task_count} task(s) in ${contract.group_count} group(s) · ` +
        `${contract.dependency_count} dependency(ies) · ${contract.unassigned} unassigned`
    );

    const effects = this.el('#plan-review-effects');
    if (effects) {
      effects.innerHTML = (contract.effects || [])
        .map(effect => `<li>${escapeHtml(effect)}</li>`)
        .join('');
    }

    // The primary action says what it does. A side-effecting approval never
    // hides behind a generic label (FR-64, FR-65).
    const approve = this.el('#plan-approve');
    if (approve) {
      approve.textContent = approvalButtonLabel(contract);
      approve.disabled = !contract.approvable;
      approve.dataset.startsExecution = String(Boolean(contract.starts_execution));
    }

    // Blockers disable the action with a reason rather than letting it fail on
    // click (FR-48).
    const blockers = this.el('#plan-review-blockers');
    if (blockers) {
      const list = contract.blockers || [];
      blockers.hidden = list.length === 0;
      blockers.innerHTML = list.map(reason => `<li>${escapeHtml(reason)}</li>`).join('');
    }
  }

  // approve binds to the exact version and hash the contract carried. The
  // server rejects it if either moved, which is what makes "the version you
  // read is the version you approved" true (FR-61, FR-69).
  async approve(user = {}) {
    if (!this.contract) {
      this.announce('Load the review before approving', 'error');
      return null;
    }
    try {
      const approval = await this.request(`${this.basePath()}/approvals`, {
        method: 'POST',
        body: JSON.stringify({
          version: this.contract.version,
          content_hash: this.contract.content_hash,
          effect: this.contract.effect,
          user_id: user.id || '',
          user_name: user.name || '',
          // One key per contract view, so a double-click replays the original
          // approval instead of creating a second one (FR-73).
          idempotency_key: this.approvalKey()
        })
      });
      // Approving records the decision; spending it is the separate step that
      // creates the work. Doing both here keeps the user's single click
      // meaning what the button said it meant, and the message that lands
      // describes what actually happened rather than what was about to.
      await this.materialize(approval.id);
      return approval;
    } catch (error) {
      if (error.code === 'approval_mismatch' || error.code === 'stale_version') {
        // The plan moved under the reviewer. Reload so they read the real
        // current version rather than retrying against a stale one.
        this.announce(`${error.message} Reloading the current version…`, 'error');
        await this.reload();
        if (this.plan?.current_version) {
          await this.loadReviewContract(this.plan.current_version);
        }
        return null;
      }
      this.announce(`Could not approve: ${error.message}`, 'error');
      return null;
    }
  }

  // approvalKey is stable for one contract view, so retrying the same approval
  // is idempotent while a genuinely new review gets its own key.
  approvalKey() {
    if (!this.contract) return '';
    return `${this.planId}:${this.contract.version}:${this.contract.content_hash}`;
  }

  // materialize spends an approval and creates the work it authorized.
  //
  // A failure here is recoverable by design: the approval stays usable, so the
  // panel offers a retry rather than sending the user back to re-approve
  // something they already approved (FR-99).
  async materialize(approvalID) {
    try {
      const result = await this.request(`${this.basePath()}/materialize`, {
        method: 'POST',
        body: JSON.stringify({ approval_id: approvalID })
      });
      this.materialization = { ...result, approval_id: approvalID, error: null };
      this.announce(materializedMessage(result));
      await this.reload();
      return result;
    } catch (error) {
      this.materialization = { approval_id: approvalID, error: error.message, task_ids: [] };
      this.renderMaterialization();
      this.announce(`Could not create the approved work: ${error.message}`, 'error');
      return null;
    }
  }

  // renderBlocker shows the single most blocking reason, or hides the panel.
  //
  // One blocker, not a list. Several can apply at once, and showing all of them
  // asks the user to work out which to try first — which is exactly the job the
  // precedence order already did (FR-156).
  renderBlocker(plan) {
    const panel = this.el('#plan-blocker');
    if (!panel) return;

    const blocker = primaryBlocker(plan);
    panel.hidden = !blocker;
    if (!blocker) return;

    const reason = this.el('#plan-blocker-reason');
    if (reason) reason.textContent = blocker.reason;

    const action = this.el('#plan-blocker-action');
    if (action) {
      action.textContent = blocker.action.label;
      // A blocker whose action points nowhere still explains itself; hiding the
      // link beats rendering one that goes to the current page.
      const href = blocker.action.href;
      action.hidden = !href;
      if (href) action.setAttribute('href', href);
    }
  }

  // startProgressPolling refreshes a plan whose progress can still change.
  //
  // Bounded, and only while something can move: a finished plan never changes
  // again, and a page left open overnight polling it forever becomes a
  // load-bearing source of traffic (FR-155).
  startProgressPolling(plan) {
    this.stopProgressPolling();
    if (!shouldPoll(plan)) return;

    this.pollTimer = this.setTimeoutImpl(() => {
      void this.reload();
    }, POLL_INTERVAL_MS);
  }

  stopProgressPolling() {
    if (this.pollTimer) {
      this.clearTimeoutImpl(this.pollTimer);
      this.pollTimer = null;
    }
  }

  // loadReconciliation fetches what approving this revision would do to work
  // that already exists.
  //
  // A plan with nothing to reconcile answers with an error rather than an empty
  // preview, and that is not a failure worth showing: most plans are not
  // revisions. The panel simply stays hidden.
  async loadReconciliation() {
    this.reconciliation = null;
    try {
      this.reconciliation = await this.request(`${this.basePath()}/reconcile`);
    } catch {
      this.reconciliation = null;
    }
    this.renderReconciliation();
  }

  // renderReconciliation shows the affected work before the approval, not after.
  //
  // The confirm button appears only when the server says a separate
  // confirmation is required. Rendering it for an additive revision would ask
  // for a decision that has no consequence, and train the user to click past
  // the one that does (FR-77, FR-154).
  renderReconciliation() {
    const panel = this.el('#plan-reconcile-panel');
    if (!panel) return;
    const preview = this.reconciliation;
    panel.hidden = !preview;
    if (!preview) return;

    const summary = this.el('#plan-reconcile-summary');
    if (summary) summary.textContent = reconcileSummary(preview);

    const list = this.el('#plan-reconcile-list');
    if (list) {
      list.innerHTML = (preview.entries || [])
        .map(
          entry =>
            `<li class="plan-reconcile-item plan-reconcile-${escapeHtml(entry.disposition || '')}">` +
            `${escapeHtml(reconcileLine(entry))}</li>`
        )
        .join('');
    }

    const runs = this.el('#plan-reconcile-runs');
    if (runs) {
      const note = reconcileRunsNote(preview);
      runs.textContent = note;
      runs.hidden = note === '';
    }

    const actions = this.el('#plan-reconcile-actions');
    if (actions) actions.hidden = !preview.requires_confirmation;

    const status = this.el('#plan-reconcile-status');
    if (status && !preview.requires_confirmation) {
      status.textContent = 'This revision only adds work, so no extra confirmation is needed.';
    }
  }

  // confirmReconciliation records acceptance of the exact preview on screen.
  //
  // The token comes from the preview the user is looking at. If work moved
  // since it was drawn, the server refuses and the panel reloads to show what
  // is true now — which is the whole point of confirming a preview rather than
  // confirming an intention (FR-77).
  async confirmReconciliation() {
    const preview = this.reconciliation;
    if (!preview?.token) return null;

    try {
      const confirmation = await this.request(`${this.basePath()}/reconcile`, {
        method: 'POST',
        body: JSON.stringify({ token: preview.token })
      });
      this.announce('Confirmed. Approving this version will now apply these changes.');
      await this.loadReconciliation();
      return confirmation;
    } catch (error) {
      const status = this.el('#plan-reconcile-status');
      if (status) status.textContent = error.message;
      this.announce(`Could not confirm: ${error.message}`, 'error');
      // Reload so the panel shows the state that refused the confirmation,
      // rather than leaving the stale preview on screen next to its own error.
      await this.loadReconciliation();
      return null;
    }
  }

  // renderMaterialization reports what an approval produced, linking to the
  // tasks and files rather than restating their state — the Task and Run
  // records remain the truth about how that work is going (FR-11).
  renderMaterialization() {
    const panel = this.el('#plan-materialization-panel');
    if (!panel) return;
    const state = this.materialization;
    panel.hidden = !state;
    if (!state) return;

    const summary = this.el('#plan-materialization-summary');
    if (summary) {
      if (state.error) {
        summary.textContent = `Could not create the approved work: ${state.error}`;
      } else {
        const files = state.artifact_paths?.length
          ? ` and ${state.artifact_paths.length} file(s)`
          : '';
        const handoff = state.handoff?.command
          ? ` Planning is complete; run “${state.handoff.command}”. Do not run wt plan again.`
          : '';
        summary.textContent = state.replayed
          ? `This approval had already been used. It created ${state.task_ids.length} task(s)${files}.${handoff}`
          : `Created ${state.task_ids.length} task(s)${files}.${handoff}`;
      }
    }

    const links = this.el('#plan-materialization-tasks');
    if (links) {
      links.innerHTML = (state.task_ids || [])
        .map(
          id =>
            `<li><a href="/workspaces/${escapeHtml(this.workspaceId)}/task/${escapeHtml(id)}">${escapeHtml(id)}</a></li>`
        )
        .join('');
    }

    const files = this.el('#plan-materialization-artifacts');
    if (files) {
      const paths = state.artifact_paths || [];
      files.hidden = paths.length === 0;
      files.innerHTML = paths.map(path => `<li>${escapeHtml(path)}</li>`).join('');
    }

    // The retry is offered only when something actually failed; a successful
    // materialization has nothing to retry.
    const retry = this.el('#plan-materialization-retry');
    if (retry) {
      retry.hidden = !state.error;
      retry.dataset.approvalId = state.approval_id || '';
    }
  }

  async decide(decision, reason = '', actor = '') {
    try {
      const plan = await this.request(`${this.basePath()}/decision`, {
        method: 'POST',
        body: JSON.stringify({ decision, reason, actor, version: this.contract?.version || 0 })
      });
      this.contract = null;
      const panel = this.el('#plan-review-panel');
      if (panel) panel.hidden = true;
      this.announce(
        decision === 'reject'
          ? 'Version rejected. It is kept in the plan’s history.'
          : 'Changes requested. The reviewed version is kept and the plan is editable again.'
      );
      this.plan = plan;
      await this.reload();
      return plan;
    } catch (error) {
      this.announce(`Could not record the decision: ${error.message}`, 'error');
      return null;
    }
  }

  async compareVersions(from, to) {
    try {
      const diff = await this.request(`${this.basePath()}/compare?from=${from}&to=${to}`);
      this.renderDiff(diff);
      return diff;
    } catch (error) {
      this.announce(`Could not compare versions: ${error.message}`, 'error');
      return null;
    }
  }

  renderDiff(diff) {
    const panel = this.el('#plan-compare-panel');
    if (panel) panel.hidden = false;
    const list = this.el('#plan-compare-list');
    if (!list) return;
    list.innerHTML = diffSummary(diff)
      .map(line => `<li>${escapeHtml(line)}</li>`)
      .join('');
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
    this.renderMaterialization();
    this.renderBlocker(plan);
    this.startProgressPolling(plan);
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
