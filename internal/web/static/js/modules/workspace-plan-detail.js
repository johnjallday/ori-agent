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
  }

  el(selector) {
    return this.doc ? this.doc.querySelector(selector) : null;
  }

  async init() {
    await this.reload();
  }

  async request(path) {
    if (!this.fetchImpl) throw new Error('fetch is unavailable');
    const response = await this.fetchImpl(path, {
      headers: { 'Content-Type': 'application/json' }
    });
    const body = await response.json().catch(() => ({}));
    if (!response.ok) {
      const error = new Error(body.message || 'Request failed');
      error.code = body.code || 'internal_error';
      error.status = response.status;
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
      const activity = await this.request(`${this.basePath()}/activity`).catch(() => ({
        activity: []
      }));
      this.render(plan, activity.activity || []);
    } catch (error) {
      this.renderMissing(error);
    }
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
