const ACTIVE_STATUSES = new Set(['pending', 'preparing', 'preparing_context', 'executing', 'validating']);
const TRACE_PAGE_SIZE = 200;

function escapeHtml(value) {
  return String(value ?? '')
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');
}

function formatDateTime(value) {
  if (!value) return '';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return '';
  return date.toLocaleString([], {
    dateStyle: 'medium',
    timeStyle: 'short'
  });
}

function formatStatus(value) {
  const normalized = String(value || '').trim().replace(/_/g, ' ');
  if (!normalized) return 'Recorded';
  return normalized.replace(/\b\w/g, (char) => char.toUpperCase());
}

function formatKind(value) {
  const normalized = String(value || '').trim().toLowerCase();
  const labels = {
    task_raw_result: 'Task Raw Result',
    task_normalized_row: 'Task Normalized Row',
    task_output_validation: 'Task Output Validation',
    task_output_repair: 'Task Output Repair',
    task_storage_receipt: 'Task Storage Receipt',
  };
  if (labels[normalized]) return labels[normalized];
  return formatStatus(value);
}

export function formatTaskOutputStatus(output) {
  const validation = String(output?.validation_status || '').trim().toLowerCase();
  const storage = String(output?.storage_status || '').trim().toLowerCase();
  if (validation === 'dismissed') return 'Dismissed';
  if (validation === 'manually_approved' || storage === 'manually_appended') return 'Manually Approved';
  if (validation === 'needs_review' || storage === 'skipped_invalid') return 'Needs Review';
  if (validation === 'passed' && (storage === 'saved' || storage === 'appended')) return 'Saved';
  if (validation === 'passed') return 'Validated';
  if (validation === 'not_applicable') return 'Not Applicable';
  return formatStatus(validation || storage || '');
}

function compactText(value) {
  return String(value || '').replace(/\s+/g, ' ').trim();
}

export class WorkspaceRunPage {
  constructor(workspaceId, runId) {
    this.workspaceId = workspaceId;
    this.runId = runId;
    this.workspace = null;
    this.run = null;
    this.artifacts = [];
    this.traceEvents = [];
    this.traceNextSince = 0;
    this.traceHasMore = false;
    this.pollTimer = null;
    this.loadingAction = false;
    this.elements = {};
  }

  async init() {
    this.cacheElements();
    this.bindEvents();
    await this.loadData();
  }

  destroy() {
    if (this.pollTimer) {
      window.clearTimeout(this.pollTimer);
      this.pollTimer = null;
    }
  }

  cacheElements() {
    this.elements = {
      root: document.getElementById('workspaceRunPageRoot'),
      alert: document.getElementById('workspace-run-alert'),
      loading: document.getElementById('workspace-run-loading'),
      empty: document.getElementById('workspace-run-empty'),
      content: document.getElementById('workspace-run-content'),
      backlink: document.getElementById('workspace-run-backlink'),
      workspaceName: document.getElementById('workspace-run-workspace-name'),
      breadcrumbTitle: document.getElementById('workspace-run-breadcrumb-title'),
      title: document.getElementById('workspace-run-title'),
      subtitle: document.getElementById('workspace-run-subtitle'),
      status: document.getElementById('workspace-run-status'),
      refreshBtn: document.getElementById('workspace-run-refresh'),
      actions: document.getElementById('workspace-run-actions'),
      overview: document.getElementById('workspace-run-overview'),
      contextCard: document.getElementById('workspace-run-context-card'),
      context: document.getElementById('workspace-run-context'),
      reportCard: document.getElementById('workspace-run-report-card'),
      report: document.getElementById('workspace-run-report'),
      validationCard: document.getElementById('workspace-run-validation-card'),
      validation: document.getElementById('workspace-run-validation'),
      memoryCard: document.getElementById('workspace-run-memory-card'),
      memory: document.getElementById('workspace-run-memory'),
      artifactsCard: document.getElementById('workspace-run-artifacts-card'),
      artifacts: document.getElementById('workspace-run-artifacts'),
      trace: document.getElementById('workspace-run-trace'),
      traceCount: document.getElementById('workspace-run-trace-count'),
      traceMoreBtn: document.getElementById('workspace-run-trace-more')
    };
  }

  bindEvents() {
    this.elements.refreshBtn?.addEventListener('click', () => {
      this.refreshLiveData({ resetTrace: true });
    });
    this.elements.traceMoreBtn?.addEventListener('click', () => {
      this.loadMoreTrace();
    });
    this.elements.actions?.addEventListener('click', (event) => {
      const button = event.target.closest('[data-run-action]');
      if (!button) return;
      this.handleRunAction(button.dataset.runAction);
    });
  }

  async loadData() {
    this.setState('loading');
    this.setAlert('');

    try {
      const [workspace, run, artifacts, tracePage] = await Promise.all([
        this.fetchWorkspace().catch(() => null),
        this.fetchRun(),
        this.fetchArtifacts(),
        this.fetchTracePage(0)
      ]);

      this.workspace = workspace;
      this.run = run;
      this.artifacts = artifacts;
      this.traceEvents = Array.isArray(tracePage?.events) ? tracePage.events : [];
      this.traceNextSince = Number(tracePage?.next_since || 0);
      this.traceHasMore = Boolean(tracePage?.has_more);

      this.render();
      this.setState('content');
      this.schedulePolling();
    } catch (error) {
      if (error?.status === 404) {
        if (this.elements.empty) this.elements.empty.textContent = 'Workspace run not found.';
        this.setState('empty');
        return;
      }
      console.error('Failed to load workspace run page:', error);
      this.setAlert(error?.message || 'Failed to load this workspace run.');
      if (this.elements.empty) this.elements.empty.textContent = 'Workspace run could not be loaded.';
      this.setState('empty');
    }
  }

  async refreshLiveData({ resetTrace = false } = {}) {
    if (!this.run) return this.loadData();

    try {
      const [run, artifacts] = await Promise.all([
        this.fetchRun(),
        this.fetchArtifacts()
      ]);

      this.run = run;
      this.artifacts = artifacts;

      if (resetTrace) {
        const page = await this.fetchTracePage(0);
        this.traceEvents = Array.isArray(page?.events) ? page.events : [];
        this.traceNextSince = Number(page?.next_since || 0);
        this.traceHasMore = Boolean(page?.has_more);
      } else {
        const page = await this.fetchTracePage(this.traceNextSince);
        const events = Array.isArray(page?.events) ? page.events : [];
        if (events.length > 0) {
          this.traceEvents = this.mergeTraceEvents(this.traceEvents, events);
          this.traceNextSince = Number(page?.next_since || this.traceNextSince);
        }
        this.traceHasMore = Boolean(page?.has_more);
      }

      this.render();
      this.schedulePolling();
    } catch (error) {
      console.error('Failed to refresh workspace run:', error);
      this.setAlert(error?.message || 'Failed to refresh this workspace run.');
    }
  }

  async fetchWorkspace() {
    const response = await fetch(`/api/workspaces/${encodeURIComponent(this.workspaceId)}`);
    if (!response.ok) {
      throw new Error('Failed to load workspace details.');
    }
    return response.json();
  }

  async fetchRun() {
    const response = await fetch(`/api/workspaces/${encodeURIComponent(this.workspaceId)}/runs/${encodeURIComponent(this.runId)}`);
    if (!response.ok) {
      const error = new Error(response.status === 404 ? 'Workspace run not found.' : 'Failed to load workspace run.');
      error.status = response.status;
      throw error;
    }
    return response.json();
  }

  async fetchArtifacts() {
    const response = await fetch(`/api/workspaces/${encodeURIComponent(this.workspaceId)}/runs/${encodeURIComponent(this.runId)}/artifacts`);
    if (!response.ok) {
      throw new Error('Failed to load workspace run artifacts.');
    }
    const payload = await response.json();
    return Array.isArray(payload?.artifacts) ? payload.artifacts : [];
  }

  async fetchTracePage(since) {
    const params = new URLSearchParams({
      since: String(Number(since) || 0),
      limit: String(TRACE_PAGE_SIZE)
    });
    const response = await fetch(`/api/workspaces/${encodeURIComponent(this.workspaceId)}/runs/${encodeURIComponent(this.runId)}/trace?${params.toString()}`);
    if (!response.ok) {
      throw new Error('Failed to load workspace run trace.');
    }
    return response.json();
  }

  mergeTraceEvents(existing, incoming) {
    const merged = new Map();
    [...existing, ...incoming].forEach((event) => {
      const key = String(event?.id || event?.sequence || '');
      if (key) merged.set(key, event);
    });
    return [...merged.values()].sort((left, right) => Number(left?.sequence || 0) - Number(right?.sequence || 0));
  }

  schedulePolling() {
    if (this.pollTimer) {
      window.clearTimeout(this.pollTimer);
      this.pollTimer = null;
    }
    if (!this.run || !ACTIVE_STATUSES.has(String(this.run.status || ''))) {
      return;
    }
    this.pollTimer = window.setTimeout(() => {
      this.refreshLiveData();
    }, 2000);
  }

  setState(state) {
    if (this.elements.loading) this.elements.loading.hidden = state !== 'loading';
    if (this.elements.empty) this.elements.empty.hidden = state !== 'empty';
    if (this.elements.content) this.elements.content.hidden = state !== 'content';
  }

  setAlert(message) {
    if (!this.elements.alert) return;
    const value = String(message || '').trim();
    this.elements.alert.hidden = !value;
    this.elements.alert.textContent = value;
  }

  render() {
    this.renderHero();
    this.renderOverview();
    this.renderContext();
    this.renderReport();
    this.renderValidation();
    this.renderMemoryLearned();
    this.renderArtifacts();
    this.renderTrace();
    this.renderActions();
  }

  // renderMemoryLearned surfaces the run's memory diff (the memory_diff trace
  // artifact) as a human-readable "what this run learned" card. Hidden when the
  // run wrote nothing to workspace memory.
  renderMemoryLearned() {
    if (!this.elements.memoryCard || !this.elements.memory) return;
    const artifacts = Array.isArray(this.artifacts) ? this.artifacts : [];
    const diff = artifacts.find((a) => a?.metadata && a.metadata.role === 'memory_diff');
    const added = Array.isArray(diff?.metadata?.added) ? diff.metadata.added : [];
    const removed = Array.isArray(diff?.metadata?.removed) ? diff.metadata.removed : [];

    if (!added.length && !removed.length) {
      this.elements.memoryCard.hidden = true;
      this.elements.memory.innerHTML = '';
      return;
    }

    const renderGroup = (label, entries, cls) => {
      if (!entries.length) return '';
      const items = entries
        .map((line) => `<li class="workspace-run-memory-entry ${cls}">${escapeHtml(String(line))}</li>`)
        .join('');
      return `
        <div class="workspace-run-memory-group">
          <div class="workspace-run-memory-group-label">${escapeHtml(label)}</div>
          <ul class="workspace-run-memory-entries">${items}</ul>
        </div>
      `;
    };

    this.elements.memoryCard.hidden = false;
    this.elements.memory.innerHTML =
      renderGroup(`Remembered (${added.length})`, added, 'is-added') +
      renderGroup(`Forgotten (${removed.length})`, removed, 'is-removed');
  }

  renderHero() {
    if (!this.run) return;

    const workspaceName = String(this.workspace?.name || 'Workspace').trim() || 'Workspace';
    const prompt = compactText(this.run?.prompt);
    const taskID = String(this.run?.scope?.target_task_id || '').trim();

    if (this.elements.workspaceName) {
      this.elements.workspaceName.textContent = workspaceName;
    }
    if (this.elements.title) {
      this.elements.title.textContent = `${formatStatus(this.run.status)} Run`;
    }
    if (this.elements.breadcrumbTitle) {
      this.elements.breadcrumbTitle.textContent = this.shortID(this.run.id);
    }
    if (this.elements.subtitle) {
      // Lead with what the run was asked to do; profile/executor are
      // developer details and live in the Run summary's Technical details.
      this.elements.subtitle.textContent = prompt || 'Run detail.';
    }
    if (this.elements.status) {
      this.elements.status.dataset.state = String(this.run.status || '');
      this.elements.status.textContent = formatStatus(this.run.status);
    }
    if (taskID && this.elements.backlink) {
      this.elements.backlink.href = this.taskHref(taskID);
      this.elements.backlink.lastChild.textContent = 'Back to Task';
    }
  }

  renderOverview() {
    if (!this.elements.overview || !this.run) return;

    const taskID = String(this.run?.scope?.target_task_id || '').trim();
    const parentRunID = String(this.run?.parent_run_id || '').trim();
    const repoPath = String(this.run?.scope?.repo_path || '').trim();
    const targetNoteID = String(this.run?.scope?.target_note_id || '').trim();
    const referenceURL = String(this.run?.reference_url || '').trim();

    // Friendly essentials shown by default: timing, cost, an error if any,
    // and a link back to the task. Everything else is configuration detail.
    const summaryItems = [
      { label: 'Created', value: formatDateTime(this.run.created_at) || 'Unknown' },
      { label: 'Started', value: formatDateTime(this.run.started_at) || 'Not started' },
      { label: 'Finished', value: formatDateTime(this.run.finished_at) || 'Not finished' }
    ];
    if (this.run?.cost) {
      summaryItems.push({
        label: 'Cost',
        value: [
          Number.isFinite(Number(this.run.cost.total_tokens)) ? `${Number(this.run.cost.total_tokens)} tokens` : '',
          Number.isFinite(Number(this.run.cost.usd)) ? `$${Number(this.run.cost.usd).toFixed(4)}` : ''
        ].filter(Boolean).join(' / ') || 'Tracked'
      });
    }
    if (taskID) summaryItems.push({ label: 'Task', value: taskID, href: this.taskHref(taskID) });
    if (referenceURL) summaryItems.push({ label: 'Reference URL', value: referenceURL, href: referenceURL, external: true, full: true });
    if (this.run?.error) summaryItems.push({ label: 'Error', value: this.run.error, full: true });

    // Developer-facing run internals, tucked into a collapsed disclosure so
    // they don't dominate the page for non-technical users.
    const technicalItems = [
      { label: 'Run ID', value: this.run.id, full: true },
      { label: 'Profile', value: this.run?.profile_snapshot?.id || this.run?.profile_id || 'general' },
      { label: 'Executor', value: [this.run?.executor?.kind, this.run?.executor?.ref].filter(Boolean).join(' / ') || 'Unknown' },
      { label: 'Approval', value: this.run?.policy?.approval || 'none' },
      { label: 'Mutation', value: this.run?.policy?.mutation || 'unknown' }
    ];
    if (targetNoteID) technicalItems.push({ label: 'Target Note', value: targetNoteID });
    if (repoPath) technicalItems.push({ label: 'Repo Path', value: repoPath, full: true });
    if (parentRunID) technicalItems.push({ label: 'Parent Run', value: parentRunID, href: this.runHref(parentRunID), full: true });

    const renderItem = (item) => `
      <article class="workspace-run-overview-item${item.full ? ' full' : ''}">
        <div class="workspace-run-overview-label">${escapeHtml(item.label)}</div>
        <div class="workspace-run-overview-value">
          ${item.href
            ? `<a href="${escapeHtml(item.href)}" class="workspace-run-inline-link"${item.external ? ' target="_blank" rel="noopener noreferrer"' : ''}>${escapeHtml(item.value)}</a>`
            : escapeHtml(item.value)}
        </div>
      </article>
    `;

    const technicalHtml = technicalItems.length
      ? `<details class="workspace-run-advanced">
          <summary class="workspace-run-advanced-summary">Technical details</summary>
          <div class="workspace-run-overview-grid">${technicalItems.map(renderItem).join('')}</div>
        </details>`
      : '';

    this.elements.overview.innerHTML = summaryItems.map(renderItem).join('') + technicalHtml;
  }

  renderReport() {
    if (!this.elements.reportCard || !this.elements.report) return;
    const report = this.run?.report;
    if (!report) {
      this.elements.reportCard.hidden = true;
      this.elements.report.innerHTML = '';
      return;
    }

    const changedFiles = Array.isArray(report.changed_files) ? report.changed_files : [];
    const followUps = Array.isArray(report.follow_ups) ? report.follow_ups : [];
    const inspection = report.reference_url_inspection || null;
    const blocks = [
      report.summary ? `<div>${escapeHtml(report.summary)}</div>` : '',
      `<div class="workspace-run-list-block">
        <div class="workspace-run-list-title">Validation</div>
        <div>${escapeHtml(report.validation_status || 'unknown')}</div>
      </div>`,
      inspection ? `
        <div class="workspace-run-list-block">
          <div class="workspace-run-list-title">Reference URL Inspection</div>
          <div>${escapeHtml(inspection.status || 'unknown')}${inspection.detail ? ` - ${escapeHtml(inspection.detail)}` : ''}</div>
        </div>
      ` : '',
      changedFiles.length ? `
        <div class="workspace-run-list-block">
          <div class="workspace-run-list-title">Changed Files</div>
          <ul class="workspace-run-list">
            ${changedFiles.map((file) => `<li>${escapeHtml(file)}</li>`).join('')}
          </ul>
        </div>
      ` : '',
      followUps.length ? `
        <div class="workspace-run-list-block">
          <div class="workspace-run-list-title">Follow Ups</div>
          <ul class="workspace-run-list">
            ${followUps.map((item) => `<li>${escapeHtml(item)}</li>`).join('')}
          </ul>
        </div>
      ` : '',
      report.human_review_needed ? `
        <div class="workspace-run-list-block">
          <div class="workspace-run-list-title">Review</div>
          <div>Human review needed.</div>
        </div>
      ` : ''
    ].filter(Boolean);

    this.elements.reportCard.hidden = false;
    this.elements.report.innerHTML = blocks.join('');
  }

  renderContext() {
    if (!this.elements.contextCard || !this.elements.context) return;
    const plan = this.run?.context_plan || null;
    const prepared = this.run?.prepared_context || null;
    const items = Array.isArray(prepared?.items) ? prepared.items : [];
    const tools = Array.isArray(prepared?.available_tools) ? prepared.available_tools : [];
    const hasPlan = plan && Object.keys(plan).length > 0;

    if (!hasPlan && !prepared) {
      this.elements.contextCard.hidden = true;
      this.elements.context.innerHTML = '';
      return;
    }

    const planRows = hasPlan ? [
      ['Strategy', plan.strategy || 'unspecified'],
      ['Workspace snapshot', plan.include_workspace_snapshot ? 'included' : 'not requested'],
      ['Attached files', plan.include_attached_files ? 'included' : 'not requested'],
      ['Workspace tools', plan.expose_workspace_tools ? 'available' : 'not requested']
    ] : [];

    const planBlock = planRows.length ? `
      <div class="workspace-run-list-block">
        <div class="workspace-run-list-title">Plan</div>
        <div class="workspace-run-overview-grid">
          ${planRows.map(([label, value]) => `
            <article class="workspace-run-overview-item">
              <div class="workspace-run-overview-label">${escapeHtml(label)}</div>
              <div class="workspace-run-overview-value">${escapeHtml(value)}</div>
            </article>
          `).join('')}
        </div>
      </div>
    ` : '';

    const preparedHeader = prepared ? `
      <div class="workspace-run-list-block">
        <div class="workspace-run-list-title">Prepared</div>
        <div>${escapeHtml(prepared.summary || 'Context prepared')}</div>
        ${prepared.prepared_at ? `<div class="workspace-run-meta">Prepared ${escapeHtml(formatDateTime(prepared.prepared_at))}</div>` : ''}
      </div>
    ` : '';

    const itemBlock = items.length ? `
      <div class="workspace-run-context-list">
        ${items.map((item) => `
          <article class="workspace-run-context-item">
            <div class="workspace-run-context-head">
              <strong>${escapeHtml(item?.name || formatKind(item?.kind))}</strong>
              <span class="workspace-run-context-access" data-state="${escapeHtml(item?.access || '')}">
                ${escapeHtml(formatStatus(item?.access))}
              </span>
            </div>
            ${item?.detail ? `<div class="workspace-run-trace-copy">${escapeHtml(item.detail)}</div>` : ''}
            ${item?.ref ? `<div class="workspace-run-code">${escapeHtml(item.ref)}</div>` : ''}
          </article>
        `).join('')}
      </div>
    ` : '';

    const toolsBlock = tools.length ? `
      <div class="workspace-run-list-block">
        <div class="workspace-run-list-title">Available Tools</div>
        <ul class="workspace-run-list">
          ${tools.map((tool) => `<li>${escapeHtml(tool)}</li>`).join('')}
        </ul>
      </div>
    ` : '';

    this.elements.contextCard.hidden = false;
    this.elements.context.innerHTML = [planBlock, preparedHeader, itemBlock, toolsBlock].filter(Boolean).join('');
  }

  renderValidation() {
    if (!this.elements.validationCard || !this.elements.validation) return;
    const checks = Array.isArray(this.run?.validation_result?.checks) ? this.run.validation_result.checks : [];
    const taskOutput = this.run?.task_output || null;
    if (!checks.length && !taskOutput) {
      this.elements.validationCard.hidden = true;
      this.elements.validation.innerHTML = '';
      return;
    }

    const taskOutputBlock = taskOutput ? `
      <article class="workspace-run-validation-item">
        <div class="workspace-run-validation-head">
          <strong>Task Output</strong>
          <span class="workspace-run-validation-status" data-state="${escapeHtml(taskOutput.validation_status || taskOutput.storage_status || '')}">
            ${escapeHtml(formatTaskOutputStatus(taskOutput))}
          </span>
        </div>
        <div class="workspace-run-meta">
          ${[
            taskOutput.contract_version ? `Contract ${taskOutput.contract_version}` : '',
            taskOutput.storage_status ? `Storage ${formatStatus(taskOutput.storage_status)}` : '',
            taskOutput.validated_at ? `Validated ${formatDateTime(taskOutput.validated_at)}` : ''
          ].filter(Boolean).map(escapeHtml).join(' | ')}
        </div>
        ${Array.isArray(taskOutput.errors) && taskOutput.errors.length ? `
          <ul class="workspace-run-list">
            ${taskOutput.errors.map((error) => `<li>${escapeHtml(error?.message || error?.code || 'Validation failed')}</li>`).join('')}
          </ul>
        ` : ''}
      </article>
    ` : '';

    this.elements.validationCard.hidden = false;
    const checkBlocks = checks.map((check) => `
      <article class="workspace-run-validation-item">
        <div class="workspace-run-validation-head">
          <strong>${escapeHtml(check?.name || 'Check')}</strong>
          <span class="workspace-run-validation-status" data-state="${escapeHtml(check?.status || '')}">
            ${escapeHtml(formatStatus(check?.status))}
          </span>
        </div>
        ${check?.soft ? `<div class="workspace-run-meta">Soft check</div>` : ''}
        ${check?.evidence ? `<div class="workspace-run-trace-copy">${escapeHtml(check.evidence)}</div>` : ''}
      </article>
    `).join('');
    this.elements.validation.innerHTML = [taskOutputBlock, checkBlocks].filter(Boolean).join('');
  }

  renderArtifacts() {
    if (!this.elements.artifactsCard || !this.elements.artifacts) return;
    const artifacts = Array.isArray(this.artifacts) ? this.artifacts : [];
    if (!artifacts.length) {
      this.elements.artifactsCard.hidden = true;
      this.elements.artifacts.innerHTML = '';
      return;
    }

    this.elements.artifactsCard.hidden = false;
    this.elements.artifacts.innerHTML = artifacts.map((artifact) => {
      const metadata = artifact?.metadata && typeof artifact.metadata === 'object'
        ? Object.entries(artifact.metadata)
            .map(([key, value]) => `${key}: ${compactText(value)}`)
            .filter(Boolean)
        : [];
      const detailParts = [
        artifact?.path ? `path ${artifact.path}` : '',
        artifact?.created_at ? `created ${formatDateTime(artifact.created_at)}` : '',
        metadata.join(' | ')
      ].filter(Boolean);
      return `
        <article class="workspace-run-artifact">
          <div class="workspace-run-artifact-head">
            <strong>${escapeHtml(this.shortID(artifact?.id))}</strong>
            <span class="workspace-run-artifact-kind">${escapeHtml(formatKind(artifact?.kind))}</span>
          </div>
          ${detailParts.length ? `<div class="workspace-run-artifact-copy">${escapeHtml(detailParts.join(' | '))}</div>` : ''}
          <div class="workspace-run-code">${escapeHtml(String(artifact?.id || ''))}</div>
        </article>
      `;
    }).join('');
  }

  renderTrace() {
    if (!this.elements.trace || !this.elements.traceCount || !this.elements.traceMoreBtn) return;
    const events = Array.isArray(this.traceEvents) ? this.traceEvents : [];
    this.elements.traceCount.textContent = `${events.length} event${events.length === 1 ? '' : 's'}`;
    this.elements.traceMoreBtn.hidden = !this.traceHasMore;

    if (!events.length) {
      this.elements.trace.innerHTML = `
        <article class="workspace-run-trace-item">
          <div class="workspace-run-trace-copy">No trace events captured yet.</div>
        </article>
      `;
      return;
    }

    this.elements.trace.innerHTML = events.map((event) => {
      const meta = [
        Number.isFinite(Number(event?.sequence)) ? `#${Number(event.sequence)}` : '',
        event?.source || '',
        event?.created_at ? formatDateTime(event.created_at) : '',
        event?.status ? `status ${event.status}` : '',
        event?.tool_name ? `tool ${event.tool_name}` : ''
      ].filter(Boolean).join(' | ');
      const message = compactText(event?.message) || this.describeTraceEvent(event);
      return `
        <article class="workspace-run-trace-item">
          <div class="workspace-run-trace-head">
            <strong>${escapeHtml(message)}</strong>
            <span class="workspace-run-trace-kind">${escapeHtml(formatKind(event?.kind))}</span>
          </div>
          ${meta ? `<div class="workspace-run-meta">${escapeHtml(meta)}</div>` : ''}
          ${event?.artifact_id ? `<div class="workspace-run-code">artifact ${escapeHtml(event.artifact_id)}</div>` : ''}
        </article>
      `;
    }).join('');
  }

  renderActions() {
    if (!this.elements.actions || !this.run) return;
    const status = String(this.run.status || '');
    const buttons = [];

    if (ACTIVE_STATUSES.has(status)) {
      buttons.push(`<button type="button" class="workspace-run-action-btn danger" data-run-action="stop">Stop</button>`);
    }
    if (status === 'awaiting_approval') {
      buttons.push(`<button type="button" class="workspace-run-action-btn primary" data-run-action="approve">Approve</button>`);
      buttons.push(`<button type="button" class="workspace-run-action-btn danger" data-run-action="reject">Reject</button>`);
    }

    this.elements.actions.innerHTML = buttons.join('');
  }

  async loadMoreTrace() {
    if (!this.traceHasMore) return;
    try {
      const page = await this.fetchTracePage(this.traceNextSince);
      const events = Array.isArray(page?.events) ? page.events : [];
      this.traceEvents = this.mergeTraceEvents(this.traceEvents, events);
      this.traceNextSince = Number(page?.next_since || this.traceNextSince);
      this.traceHasMore = Boolean(page?.has_more);
      this.renderTrace();
    } catch (error) {
      console.error('Failed to load more trace events:', error);
      this.setAlert(error?.message || 'Failed to load more trace events.');
    }
  }

  async handleRunAction(action) {
    if (!action || this.loadingAction) return;
    this.loadingAction = true;
    try {
      let payload = null;
      if (action === 'reject') {
        const reason = window.prompt('Reason for rejection (optional)') || '';
        payload = { reason };
      }
      const response = await fetch(
        `/api/workspaces/${encodeURIComponent(this.workspaceId)}/runs/${encodeURIComponent(this.runId)}/${encodeURIComponent(action)}`,
        {
          method: 'POST',
          headers: payload ? { 'Content-Type': 'application/json' } : undefined,
          body: payload ? JSON.stringify(payload) : undefined
        }
      );
      if (!response.ok) {
        throw new Error(`Failed to ${action} workspace run.`);
      }
      await this.refreshLiveData({ resetTrace: true });
    } catch (error) {
      console.error(`Failed to ${action} workspace run:`, error);
      this.setAlert(error?.message || `Failed to ${action} workspace run.`);
    } finally {
      this.loadingAction = false;
    }
  }

  describeTraceEvent(event) {
    const dataKind = compactText(event?.data?.kind);
    if (event?.kind === 'artifact_captured' && dataKind) return `${formatKind(dataKind)} captured`;
    if (event?.kind === 'validation_check') return 'Validation check';
    return formatKind(event?.kind);
  }

  shortID(value) {
    const normalized = String(value || '').trim();
    if (normalized.length <= 12) return normalized || 'Run';
    return normalized.slice(0, 12);
  }

  taskHref(taskId) {
    return `/workspaces/${encodeURIComponent(this.workspaceId)}/task/${encodeURIComponent(taskId)}`;
  }

  runHref(runId) {
    return `/workspaces/${encodeURIComponent(this.workspaceId)}/runs/${encodeURIComponent(runId)}`;
  }
}
