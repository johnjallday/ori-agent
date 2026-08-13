// Workspace Plans: the canonical list/create surface for durable Plans.
//
// Two rules shape this module:
//
//   - The server is the authority. This file renders what the API returned and
//     asks the API to change things; it never decides a Plan's status, whether
//     an action is allowed, or what an approval authorizes (FR-14, FR-168).
//   - Status is never communicated by color alone. Every state renders a label
//     and an icon alongside its color (FR-162).

export const PLAN_STATUS_META = {
  draft: { label: 'Draft', icon: '✎', tone: 'neutral' },
  needs_input: { label: 'Needs input', icon: '?', tone: 'attention' },
  in_review: { label: 'In review', icon: '⏸', tone: 'info' },
  approved: { label: 'Approved', icon: '✓', tone: 'info' },
  executing: { label: 'Executing', icon: '▶', tone: 'active' },
  paused: { label: 'Paused', icon: '‖', tone: 'attention' },
  completed: { label: 'Completed', icon: '✓', tone: 'success' },
  failed: { label: 'Failed', icon: '!', tone: 'danger' },
  cancelled: { label: 'Cancelled', icon: '×', tone: 'neutral' },
  superseded: { label: 'Superseded', icon: '⇢', tone: 'neutral' }
};

export function escapeHtml(value) {
  return String(value ?? '')
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');
}

// statusMeta always returns a label and an icon, including for a status this
// build does not know about, so an unexpected value degrades to readable text
// rather than an unlabeled colored dot (FR-162).
export function statusMeta(status) {
  const key = String(status || '').trim();
  if (PLAN_STATUS_META[key]) return PLAN_STATUS_META[key];
  const label = key ? key.replace(/_/g, ' ').replace(/\b\w/g, c => c.toUpperCase()) : 'Unknown';
  return { label, icon: '•', tone: 'neutral' };
}

// nextActionFor answers the question the Plans list exists to answer: what does
// this Plan need from me next (FR-147)? It reads the server's status and
// progress and never infers state from anything else.
export function nextActionFor(plan) {
  const status = String(plan?.status || '');
  const progress = plan?.progress || null;

  switch (status) {
    case 'draft':
      return { label: 'Continue editing', kind: 'edit' };
    case 'needs_input': {
      const open = countOpenClarifications(plan);
      return {
        label:
          open === 1
            ? 'Answer 1 question'
            : `Answer ${open || ''} questions`.replace('  ', ' ').trim(),
        kind: 'answer'
      };
    }
    case 'in_review':
      return { label: 'Review and approve', kind: 'review' };
    case 'approved':
      return { label: 'Start work', kind: 'start' };
    case 'executing':
      return { label: 'Monitor progress', kind: 'monitor' };
    case 'paused':
      return { label: progress?.failed ? 'Resolve failure' : 'Resume', kind: 'resume' };
    case 'failed':
      return { label: 'Retry or revise', kind: 'retry' };
    case 'completed':
      return { label: 'Review report', kind: 'report' };
    case 'cancelled':
      return { label: 'Inspect history', kind: 'inspect' };
    case 'superseded':
      return { label: 'Open replacement', kind: 'replacement' };
    default:
      return { label: 'Open plan', kind: 'open' };
  }
}

export function countOpenClarifications(plan) {
  const questions = plan?.draft?.clarifications || [];
  return questions.filter(question => question && question.status === 'open').length;
}

// progressSummary renders the derived counts the server computed from real
// Tasks and Runs. A Plan with no progress reads as "no tasks yet" rather than
// as zero-of-zero complete, because those are different claims (FR-12).
export function progressSummary(plan) {
  const progress = plan?.progress;
  if (!progress || !progress.total) {
    return { text: 'No tasks yet', percent: 0, detail: '' };
  }
  const done = progress.completed || 0;
  const percent = Math.round((done / progress.total) * 100);
  const parts = [];
  if (progress.running) parts.push(`${progress.running} running`);
  if (progress.blocked) parts.push(`${progress.blocked} blocked`);
  if (progress.failed) parts.push(`${progress.failed} failed`);
  if (progress.waiting_for_slot) parts.push('waiting for the execution slot');
  return {
    text: `${done} of ${progress.total} tasks complete`,
    percent,
    detail: parts.join(' · ')
  };
}

export function formatTimestamp(value) {
  if (!value) return '';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return '';
  return date.toLocaleString([], { dateStyle: 'medium', timeStyle: 'short' });
}

export function planDetailPath(workspaceId, planId) {
  return `/workspaces/${encodeURIComponent(workspaceId)}/plans/${encodeURIComponent(planId)}`;
}

// versionLabel distinguishes "no immutable version yet" from "version 1", which
// matters because approval binds to an exact version number (FR-61).
export function versionLabel(plan) {
  const current = Number(plan?.current_version || 0);
  if (!current) return 'No review version yet';
  const approved = Number(plan?.approved_version || 0);
  if (approved === current) return `Version ${current} · approved`;
  if (approved > 0) return `Version ${current} · v${approved} approved`;
  return `Version ${current}`;
}

export function planCardHtml(plan, workspaceId) {
  const meta = statusMeta(plan.status);
  const action = nextActionFor(plan);
  const progress = progressSummary(plan);
  const href = planDetailPath(workspaceId, plan.id);

  return `
    <li class="plan-card" data-plan-id="${escapeHtml(plan.id)}">
      <a class="plan-card-link" href="${escapeHtml(href)}">
        <div class="plan-card-head">
          <h3 class="plan-card-title">${escapeHtml(plan.title || 'Untitled plan')}</h3>
          <span class="plan-status" data-tone="${escapeHtml(meta.tone)}">
            <span class="plan-status-icon" aria-hidden="true">${escapeHtml(meta.icon)}</span>
            <span class="plan-status-label">${escapeHtml(meta.label)}</span>
          </span>
        </div>
        <p class="plan-card-version">${escapeHtml(versionLabel(plan))}</p>
        <p class="plan-card-progress">
          ${escapeHtml(progress.text)}${progress.detail ? ` <span class="plan-card-progress-detail">${escapeHtml(progress.detail)}</span>` : ''}
        </p>
        <div class="plan-card-foot">
          <span class="plan-card-updated">Updated ${escapeHtml(formatTimestamp(plan.last_activity_at || plan.updated_at))}</span>
          <span class="plan-card-next" data-kind="${escapeHtml(action.kind)}">${escapeHtml(action.label)}</span>
        </div>
      </a>
    </li>`;
}

// WorkspacePlansPage drives the canonical Plans destination.
export class WorkspacePlansPage {
  constructor(workspaceId, options = {}) {
    this.workspaceId = workspaceId;
    this.doc = options.document || (typeof document !== 'undefined' ? document : null);
    this.fetchImpl =
      options.fetch || (typeof fetch !== 'undefined' ? fetch.bind(globalThis) : null);
    this.planningEnabled = true;
    this.active = [];
    this.history = [];
  }

  el(selector) {
    return this.doc ? this.doc.querySelector(selector) : null;
  }

  async init() {
    this.bindEvents();
    await Promise.all([this.loadPlanningPolicy(), this.reload()]);
  }

  bindEvents() {
    const form = this.el('#plan-create-form');
    if (form) {
      form.addEventListener('submit', event => {
        event.preventDefault();
        void this.createPlan();
      });
    }
    const refresh = this.el('#plan-refresh');
    if (refresh) {
      refresh.addEventListener('click', () => void this.reload());
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
      // The server's stable code is what the UI reacts to; the message is for
      // the human reading it (FR-166).
      const error = new Error(body.message || 'Request failed');
      error.code = body.code || 'internal_error';
      error.status = response.status;
      throw error;
    }
    return body;
  }

  // Planning being disabled hides creation but never hides history: existing
  // Plans stay inspectable and the user is pointed at Settings rather than
  // having planning silently switched on for them (FR-159).
  async loadPlanningPolicy() {
    try {
      const body = await this.request(
        `/api/workspaces/${encodeURIComponent(this.workspaceId)}/settings`
      );
      this.planningEnabled = Boolean(body?.settings?.planning?.enabled);
    } catch {
      this.planningEnabled = false;
    }
    this.renderPolicyState();
  }

  renderPolicyState() {
    const banner = this.el('#plan-disabled-banner');
    const createPanel = this.el('#plan-create-panel');
    if (banner) banner.hidden = this.planningEnabled;
    if (createPanel) createPanel.hidden = !this.planningEnabled;
  }

  async reload() {
    this.setStatus('Loading plans…');
    try {
      const [active, history] = await Promise.all([
        this.request(`/api/workspaces/${encodeURIComponent(this.workspaceId)}/plans?scope=active`),
        this.request(`/api/workspaces/${encodeURIComponent(this.workspaceId)}/plans?scope=history`)
      ]);
      this.active = active.plans || [];
      this.history = history.plans || [];
      this.render();
      this.setStatus('');
    } catch (error) {
      this.setStatus(`Could not load plans: ${error.message}`, 'error');
    }
  }

  render() {
    this.renderSection('#plan-active-list', '#plan-active-empty', this.active);
    this.renderSection('#plan-history-list', '#plan-history-empty', this.history);
    const historySection = this.el('#plan-history-section');
    if (historySection) historySection.hidden = this.history.length === 0;
  }

  renderSection(listSelector, emptySelector, plans) {
    const list = this.el(listSelector);
    const empty = this.el(emptySelector);
    if (list) {
      list.innerHTML = plans.map(plan => planCardHtml(plan, this.workspaceId)).join('');
    }
    if (empty) empty.hidden = plans.length > 0;
  }

  setStatus(message, tone = 'info') {
    const status = this.el('#plan-status');
    if (!status) return;
    status.textContent = message;
    status.dataset.tone = tone;
    status.hidden = !message;
  }

  async createPlan() {
    const input = this.el('#plan-create-request');
    const request = input ? String(input.value || '').trim() : '';
    if (!request) {
      this.setStatus('Describe what you want planned before creating a plan.', 'error');
      return null;
    }

    this.setStatus('Creating plan…');
    try {
      const plan = await this.request(
        `/api/workspaces/${encodeURIComponent(this.workspaceId)}/plans`,
        {
          method: 'POST',
          body: JSON.stringify({ request, source: 'user' })
        }
      );
      if (input) input.value = '';
      // Creating a Plan navigates to its canonical detail route; there is one
      // full editing surface and this is not it (FR-145, FR-149).
      if (this.doc?.defaultView) {
        this.doc.defaultView.location.href = planDetailPath(this.workspaceId, plan.id);
      }
      return plan;
    } catch (error) {
      this.setStatus(`Could not create plan: ${error.message}`, 'error');
      return null;
    }
  }
}
