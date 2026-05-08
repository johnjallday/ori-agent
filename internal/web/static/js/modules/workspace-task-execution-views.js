// Execution-trace + execution-breakdown render methods for WorkspaceTaskPage.
//
// This module exists purely to keep workspace-task.js navigable. The methods
// here are mixed onto WorkspaceTaskPage.prototype via Object.assign at the
// bottom of workspace-task.js — they are NOT a separate component. They
// continue to use `this` to access the page instance's elements/state and
// invoke other page methods (escapeHtml, getSubtasks, etc.).
//
// Module-level helpers (formatDateTime / stringifyTraceValue / etc.) are
// imported from workspace-task.js. ES modules handle the cyclic import
// correctly: the imported names are live bindings that resolve to the
// fully-initialized helpers at method call time, not at module-eval time.

import {
  _formatRelativeDate,
  formatDateTime,
  getDisplayStatus,
  getStatusClass,
  getTaskEventData,
  stringifyTraceValue,
} from './workspace-task.js';

// Trace pagination page size, exported so the WorkspaceTaskPage constructor
// can seed _traceVisibleCount with it. The static getter on the class
// surfaces the same value for any external caller that previously read
// WorkspaceTaskPage.TRACE_PAGE_SIZE.
export const TRACE_PAGE_SIZE = 50;

export const taskExecutionViewsMethods = {
  renderExecutionBreakdown() {
    if (!this.elements.trace || !this.elements.traceCard) return;

    const steps = this.getExecutionBreakdownSteps(this.task, this.getSubtasks());
    if (steps.length === 0) {
      this.elements.traceCard.hidden = true;
      this.elements.trace.innerHTML = '';
      return;
    }

    this.elements.traceCard.hidden = false;
    this.elements.trace.innerHTML = steps.map((step, index) => {
      const statusKey = String(step?.status || this.task?.status || 'pending').trim().toLowerCase();
      const statusClass = getStatusClass(statusKey);
      const detail = String(step?.detail || '').trim();
      const defaultOpen = index < 2 ? ' open' : '';
      const title = String(step?.title || `Run ${index + 1}`).trim() || `Run ${index + 1}`;

      return `
        <details class="workspace-task-breakdown-step"${defaultOpen}>
          <summary>
            <span class="workspace-task-breakdown-title">
              <span class="workspace-task-breakdown-index">${index + 1}</span>
              <span class="workspace-task-breakdown-label">${this.escapeHtml(title)}</span>
            </span>
            <span class="workspace-task-step-status" data-state="${this.escapeHtml(statusClass)}">${this.escapeHtml(getDisplayStatus(statusClass))}</span>
          </summary>
          <div class="workspace-task-breakdown-body">${detail ? this.escapeHtml(detail) : 'No additional detail.'}</div>
        </details>
      `;
    }).join('');
  },

  renderRunHistory() {
    this.renderExecutionBreakdown();
  },

  getExecutionBreakdownSteps(task, subtasks = []) {
    if (!task) return [];

    const executionSteps = this.getExecutionStepBreakdownSteps(task);
    if (executionSteps.length > 0) {
      return executionSteps;
    }

    const sortedSubtasks = Array.isArray(subtasks)
      ? [...subtasks].sort((a, b) => {
          const aIndex = Number.isFinite(Number(a?.subtask_index))
            ? Number(a.subtask_index)
            : Number.MAX_SAFE_INTEGER;
          const bIndex = Number.isFinite(Number(b?.subtask_index))
            ? Number(b.subtask_index)
            : Number.MAX_SAFE_INTEGER;
          if (aIndex !== bIndex) return aIndex - bIndex;
          const aTime = a?.created_at ? new Date(a.created_at).getTime() : 0;
          const bTime = b?.created_at ? new Date(b.created_at).getTime() : 0;
          return aTime - bTime;
        })
      : [];

    if (sortedSubtasks.length > 0) {
      return sortedSubtasks.map((subtask, index) => ({
        title: String(subtask?.description || subtask?.name || `Step ${index + 1}`).trim(),
        status: String(subtask?.status || task?.status || 'pending').trim(),
        detail: this.buildExecutionBreakdownDetail(subtask, String(subtask?.details || '').trim())
      }));
    }

    const retryHistorySteps = this.getRetryHistoryBreakdownSteps(task);
    const historicalRunSteps = this.getExecutionHistoryBreakdownSteps(task, {
      includeLatest: retryHistorySteps.length === 0
    });
    if (historicalRunSteps.length > 0) {
      if (retryHistorySteps.length > 0) {
        const currentRunNumber = historicalRunSteps.length + 1;
        const currentRunSteps = retryHistorySteps.map((step) => ({
          ...step,
          title: `Run ${currentRunNumber} • ${step.title}`
        }));
        return [...historicalRunSteps, ...currentRunSteps];
      }
      return historicalRunSteps;
    }

    if (retryHistorySteps.length > 0) {
      return retryHistorySteps;
    }

    return this.inferSyntheticBreakdownSteps(task);
  },

  getExecutionStepBreakdownSteps(task) {
    const steps = Array.isArray(task?.execution_steps) ? task.execution_steps : [];
    if (!steps.length) return [];

    return steps.map((step, index) => {
      const detailParts = [];
      const detail = this.normalizeBreakdownField(step?.detail);
      const result = this.normalizeBreakdownField(step?.result);
      const errorText = this.normalizeBreakdownField(step?.error);
      const startedAt = this.normalizeBreakdownField(step?.started_at);
      const completedAt = this.normalizeBreakdownField(step?.completed_at);
      const tag = this.normalizeBreakdownField(step?.tag);

      if (detail) detailParts.push(detail);
      if (tag) detailParts.push(`Type: ${tag}`);
      if (startedAt) detailParts.push(`Started: ${formatDateTime(startedAt)}`);
      if (completedAt) detailParts.push(`Completed: ${formatDateTime(completedAt)}`);
      if (result) detailParts.push(`Result: ${this.truncateBreakdownText(result, 360)}`);
      if (errorText) detailParts.push(`Error: ${this.truncateBreakdownText(errorText, 360)}`);

      return {
        title: String(step?.title || `Step ${index + 1}`).trim() || `Step ${index + 1}`,
        status: String(step?.status || 'pending').trim().toLowerCase() || 'pending',
        detail: detailParts.join('\n')
      };
    });
  },

  getRetryHistoryBreakdownSteps(task) {
    const history = Array.isArray(task?.context?.execution_retry?.history)
      ? task.context.execution_retry.history
      : [];
    if (!history.length) return [];

    const outcomeToStatus = (outcome) => {
      const normalized = String(outcome || '').trim().toLowerCase();
      if (normalized === 'success') return 'completed';
      if (normalized === 'error' || normalized === 'failed') return 'failed';
      if (normalized === 'needs_input' || normalized === 'blocked') return 'blocked';
      return 'pending';
    };

    return history.map((item, index) => {
      const attemptNumber = Number.isFinite(Number(item?.attempt))
        ? Number(item.attempt)
        : index + 1;
      const outcome = String(item?.outcome || '').trim().toLowerCase();
      const summary = this.normalizeBreakdownField(item?.summary);
      const createdAt = this.normalizeBreakdownField(item?.created_at);
      const detailParts = [];

      if (createdAt) detailParts.push(`Recorded at: ${_formatRelativeDate(createdAt)}`);
      if (outcome) detailParts.push(`Outcome: ${outcome.replace(/_/g, ' ')}`);
      if (summary) detailParts.push(this.truncateBreakdownText(summary, 520));

      return {
        title: `Attempt ${attemptNumber}`,
        status: outcomeToStatus(outcome),
        detail: detailParts.join('\n')
      };
    });
  },

  getExecutionHistoryBreakdownSteps(task, options = {}) {
    const history = Array.isArray(task?.execution_history) ? task.execution_history : [];
    if (!history.length) return [];

    const includeLatest = options.includeLatest !== false;
    const relevantHistory = includeLatest ? history : history.slice(0, -1);
    if (!relevantHistory.length) return [];

    const startIndex =
      Number.isFinite(Number(options.startIndex)) && Number(options.startIndex) > 0
        ? Number(options.startIndex)
        : 1;

    const mapRecordedStatus = (status) => {
      const normalized = String(status || '').trim().toLowerCase();
      if (normalized === 'success') return 'completed';
      if (normalized === 'failed' || normalized === 'error') return 'failed';
      if (normalized === 'blocked') return 'blocked';
      return normalized || 'pending';
    };

    return relevantHistory.map((item, index) => {
      const recordedAt = this.normalizeBreakdownField(item?.executed_at);
      const rawStatus = String(item?.status || '').trim().toLowerCase();
      const summary = this.normalizeBreakdownField(item?.summary);
      const errorText = this.normalizeBreakdownField(item?.error);
      const durationMs = Number(item?.duration) || 0;
      const detailParts = [];

      if (recordedAt) detailParts.push(`Recorded at: ${_formatRelativeDate(recordedAt)}`);
      if (rawStatus) detailParts.push(`Outcome: ${rawStatus.replace(/_/g, ' ')}`);
      if (durationMs > 0) detailParts.push(`Duration: ${Math.round(durationMs / 1000)}s`);
      if (summary) {
        detailParts.push(this.truncateBreakdownText(summary, 520));
      } else if (errorText) {
        detailParts.push(this.truncateBreakdownText(errorText, 520));
      }

      return {
        title: `Run ${startIndex + index}`,
        status: mapRecordedStatus(rawStatus),
        detail: detailParts.join('\n')
      };
    });
  },

  buildExecutionBreakdownDetail(task, fallbackDetail = '') {
    const parts = [];
    const initialDetail = this.normalizeBreakdownField(fallbackDetail);
    if (initialDetail) parts.push(initialDetail);

    const currentStep = this.normalizeBreakdownField(task?.progress?.current_step);
    if (currentStep) parts.push(`Execution: ${currentStep}`);

    const attemptsUsed = Number(task?.context?.execution_retry?.attempts_used || 0);
    const maxAttempts = Number(task?.context?.execution_retry?.max_attempts || task?.context?.execution_max_attempts || 0);
    if (attemptsUsed > 0 && maxAttempts > 0) {
      parts.push(`Attempts: ${attemptsUsed}/${maxAttempts}`);
    }

    const retryFinalOutcome = this.normalizeBreakdownField(task?.context?.execution_retry?.final_outcome);
    if (retryFinalOutcome) parts.push(`Final outcome: ${retryFinalOutcome}`);

    const blockedReason = this.normalizeBreakdownField(task?.context?.human_loop?.reason);
    const blockedQuestion = this.normalizeBreakdownField(task?.context?.human_loop?.question);
    if (blockedReason) parts.push(`Blocked reason: ${this.truncateBreakdownText(blockedReason, 260)}`);
    if (blockedQuestion) parts.push(`Needs input: ${this.truncateBreakdownText(blockedQuestion, 260)}`);

    const errorText = this.normalizeBreakdownField(task?.error);
    if (errorText) parts.push(`Error: ${this.truncateBreakdownText(errorText, 360)}`);

    const resultText = this.normalizeBreakdownField(task?.result);
    if (resultText) parts.push(`Result: ${this.truncateBreakdownText(resultText, 360)}`);

    return parts.join('\n');
  },

  inferSyntheticBreakdownSteps(task) {
    const description = String(task?.description || '').trim();
    const lower = description.toLowerCase();
    const toStep = (title, detail = '') => ({ title, detail });

    let baseSteps = [];
    if ((lower.includes('wear') && lower.includes('tomorrow')) || lower.includes('what should i wear')) {
      baseSteps = [
        toStep("Checking tomorrow's weather", 'Collect forecast details such as temperature, rain chance, and wind.'),
        toStep('Recommendation for clothing based on the weather', 'Translate weather conditions into practical outfit guidance.')
      ];
    } else if (lower.includes('weather')) {
      baseSteps = [
        toStep('Checking weather conditions', 'Gather forecast or relevant weather signals.'),
        toStep('Summarizing weather insight', 'Return a concise recommendation tailored to the request.')
      ];
    } else {
      baseSteps = [
        toStep('Understanding the request', 'Clarify intent and constraints from task context.'),
        toStep('Producing final recommendation', 'Generate the final answer with clear reasoning.')
      ];
    }

    const statusSequence = this.getSyntheticStepStatuses(String(task?.status || 'pending'), baseSteps.length);
    return baseSteps.map((step, index) => ({
      ...step,
      status: statusSequence[index] || String(task?.status || 'pending')
    }));
  },

  getSyntheticStepStatuses(status, count) {
    const normalized = String(status || 'pending').trim().toLowerCase();
    if (count <= 0) return [];
    if (normalized === 'completed') return Array.from({ length: count }, () => 'completed');
    if (normalized === 'in_progress') {
      return Array.from({ length: count }, (_value, index) => index === 0 ? 'in_progress' : 'pending');
    }
    if (normalized === 'failed' || normalized === 'timeout') {
      return Array.from({ length: count }, (_value, index) => index < Math.max(0, count - 1) ? 'completed' : 'failed');
    }
    if (normalized === 'blocked') {
      return Array.from({ length: count }, (_value, index) => {
        if (index === 0) return 'completed';
        if (index === 1) return 'blocked';
        return 'pending';
      });
    }
    return Array.from({ length: count }, () => 'pending');
  },

  normalizeBreakdownField(value) {
    if (value === undefined || value === null) return '';
    if (typeof value === 'string') return value.trim();
    if (typeof value === 'number' || typeof value === 'boolean') return String(value);
    try {
      return JSON.stringify(value, null, 2).trim();
    } catch (_error) {
      return String(value).trim();
    }
  },

  truncateBreakdownText(text, maxLength = 220) {
    const normalized = this.normalizeBreakdownField(text);
    if (!normalized) return '';
    if (normalized.length <= maxLength) return normalized;
    return `${normalized.slice(0, maxLength).trim()}...`;
  },

  renderExecutionTrace() {
    if (!this.elements.executionTrace || !this.elements.executionTraceCard) return;

    const entries = this.buildExecutionTraceEntries();
    const showCount = this._traceVisibleCount || TRACE_PAGE_SIZE;

    if (entries.length === 0) {
      if (this.elements.executionTraceControls) {
        this.elements.executionTraceControls.hidden = true;
      }
      if (!this.hasExecutionActivity()) {
        this.elements.executionTraceCard.hidden = true;
        this.elements.executionTrace.innerHTML = '';
        return;
      }

      this.elements.executionTraceCard.hidden = false;
      this.elements.executionTrace.innerHTML = `
        <div class="workspace-task-trace-item">
          <div class="workspace-task-trace-status">not captured</div>
          <div>
            <div class="workspace-task-trace-summary">No detailed execution trace was captured for this run.</div>
            <div class="workspace-task-trace-meta">Re-run this task after the trace update is deployed to capture tool calls, progress events, and terminal status here.</div>
          </div>
        </div>
      `;
      return;
    }

    this.elements.executionTraceCard.hidden = false;

    const buckets = this.bucketTraceEntries(entries);
    const activeFilter = this._traceFilter && buckets[this._traceFilter] ? this._traceFilter : 'all';
    if (activeFilter !== this._traceFilter) {
      this._traceFilter = activeFilter;
    }

    this.renderTraceFilterChips(buckets, activeFilter);

    const filtered = activeFilter === 'all' ? entries : entries.filter((entry) => this.traceBucketForStatus(entry.status) === activeFilter);

    const visible = filtered.slice(0, showCount);
    const hiddenCount = Math.max(filtered.length - visible.length, 0);

    if (this.elements.executionTraceCount) {
      if (filtered.length === 0) {
        this.elements.executionTraceCount.textContent = '';
      } else if (filtered.length === entries.length) {
        this.elements.executionTraceCount.textContent = `Showing ${visible.length} of ${filtered.length}`;
      } else {
        this.elements.executionTraceCount.textContent = `Showing ${visible.length} of ${filtered.length} (filtered from ${entries.length})`;
      }
    }

    if (filtered.length === 0) {
      this.elements.executionTrace.innerHTML = `
        <div class="workspace-task-trace-empty">No trace entries match this filter.</div>
      `;
      return;
    }

    const itemsHtml = visible.map((entry) => `
      <div class="workspace-task-trace-item">
        <div class="workspace-task-trace-status">${this.escapeHtml(entry.status)}</div>
        <div>
          <div class="workspace-task-trace-summary">${this.escapeHtml(entry.summary)}</div>
          ${entry.meta ? `<div class="workspace-task-trace-meta">${this.escapeHtml(entry.meta)}</div>` : ''}
        </div>
      </div>
    `).join('');

    const showMoreHtml = hiddenCount > 0
      ? `<button type="button" class="workspace-task-trace-show-more" data-trace-action="show-more">Show ${Math.min(hiddenCount, TRACE_PAGE_SIZE)} more (${hiddenCount} hidden)</button>`
      : '';

    this.elements.executionTrace.innerHTML = itemsHtml + showMoreHtml;

    if (hiddenCount > 0) {
      const btn = this.elements.executionTrace.querySelector('[data-trace-action="show-more"]');
      if (btn) {
        btn.addEventListener('click', () => {
          this._traceVisibleCount = showCount + TRACE_PAGE_SIZE;
          this._renderCache && delete this._renderCache.executionTrace;
          this.renderExecutionTrace();
        });
      }
    }
  },

  bucketTraceEntries(entries) {
    const buckets = { all: { count: entries.length, label: 'All' } };
    for (const entry of entries) {
      const bucket = this.traceBucketForStatus(entry.status);
      if (!buckets[bucket]) {
        buckets[bucket] = { count: 0, label: this.traceBucketLabel(bucket) };
      }
      buckets[bucket].count += 1;
    }
    return buckets;
  },

  traceBucketForStatus(rawStatus) {
    const s = String(rawStatus || '').toLowerCase().trim();
    if (s === 'tool call' || s === 'tool result') return 'tool';
    if (s === 'tool error' || s === 'failed' || s === 'error') return 'errors';
    if (s === 'progress' || s === 'thinking' || s === 'waiting') return 'progress';
    if (s === 'started' || s === 'completed' || s === 'blocked' || s === 'cancelled' || s === 'timeout') return 'lifecycle';
    return 'other';
  },

  traceBucketLabel(bucket) {
    switch (bucket) {
      case 'tool': return 'Tools';
      case 'errors': return 'Errors';
      case 'progress': return 'Progress';
      case 'lifecycle': return 'Lifecycle';
      default: return 'Other';
    }
  },

  renderTraceFilterChips(buckets, activeFilter) {
    if (!this.elements.executionTraceFilters || !this.elements.executionTraceControls) return;

    const order = ['all', 'lifecycle', 'tool', 'progress', 'errors', 'other'];
    const present = order.filter((key) => buckets[key]);
    if (present.length <= 1) {
      this.elements.executionTraceControls.hidden = true;
      return;
    }
    this.elements.executionTraceControls.hidden = false;
    this.elements.executionTraceFilters.innerHTML = present.map((key) => `
      <button type="button"
              class="workspace-task-trace-filter"
              data-trace-filter="${this.escapeHtml(key)}"
              aria-pressed="${key === activeFilter ? 'true' : 'false'}">
        <span>${this.escapeHtml(buckets[key].label)}</span>
        <span class="workspace-task-trace-filter-count">${buckets[key].count}</span>
      </button>
    `).join('');

    this.elements.executionTraceFilters.querySelectorAll('[data-trace-filter]').forEach((btn) => {
      btn.addEventListener('click', () => {
        const key = btn.getAttribute('data-trace-filter') || 'all';
        if (key === this._traceFilter) return;
        this._traceFilter = key;
        this._traceVisibleCount = TRACE_PAGE_SIZE;
        this._renderCache && delete this._renderCache.executionTrace;
        this.renderExecutionTrace();
      });
    });
  },

  buildExecutionTraceEntries() {
    const persistedEntries = this.normalizePersistedExecutionTrace();
    if (persistedEntries.length > 0) {
      return persistedEntries;
    }

    const eventEntries = this.buildExecutionTraceEntriesFromEvents();
    if (eventEntries.length > 0) {
      return eventEntries;
    }

    return this.buildExecutionTraceEntriesFromSteps();
  },

  hasExecutionActivity() {
    const status = String(this.task?.status || '').trim().toLowerCase();
    if (status && status !== 'pending' && status !== 'assigned') return true;
    if (this.task?.started_at || this.task?.completed_at) return true;
    if (Array.isArray(this.task?.execution_history) && this.task.execution_history.length > 0) return true;
    if (this.task?.context?.execution_retry) return true;
    return false;
  },

  normalizePersistedExecutionTrace() {
    const trace = Array.isArray(this.task?.execution_trace) ? this.task.execution_trace : [];
    return trace
      .map((entry) => {
        const status = String(entry?.status || entry?.type || 'event').trim().replace(/_/g, ' ') || 'event';
        const title = String(entry?.title || '').trim();
        const detail = stringifyTraceValue(entry?.detail || entry?.summary || '', 1200);
        const timestamp = formatDateTime(entry?.timestamp || entry?.created_at);
        const source = String(entry?.source || '').trim();
        const meta = [timestamp !== '—' ? timestamp : '', source].filter(Boolean).join(' • ');
        const summary = [title, detail].filter(Boolean).join(detail && title ? '\n' : '');
        if (!summary) return null;
        return { status, summary, meta };
      })
      .filter(Boolean);
  },

  buildExecutionTraceEntriesFromEvents() {
    return this.getCurrentTaskEvents()
      .map((event) => this.formatExecutionTraceEvent(event))
      .filter(Boolean);
  },

  getCurrentTaskEvents() {
    const events = Array.isArray(this.taskEvents) ? this.taskEvents : [];
    const taskId = String(this.taskId || '').trim();
    return events
      .filter((event) => {
        const eventTaskId = this.getEventTaskId(event);
        return eventTaskId && eventTaskId === taskId;
      })
      .sort((left, right) => {
        const leftTime = new Date(left?.timestamp || left?.created_at || 0).getTime();
        const rightTime = new Date(right?.timestamp || right?.created_at || 0).getTime();
        return (Number.isFinite(leftTime) ? leftTime : 0) - (Number.isFinite(rightTime) ? rightTime : 0);
      });
  },

  getEventTaskId(event) {
    const data = getTaskEventData(event);
    return String(data?.task_id || data?.id || data?.task?.id || '').trim();
  },

  formatExecutionTraceEvent(event) {
    const type = String(event?.type || '').trim();
    const data = getTaskEventData(event);
    const toolName = String(data?.tool_name || '').trim();
    const timestamp = formatDateTime(event?.timestamp || data?.updated_at);
    const source = String(data?.agent || event?.source || '').trim();
    const meta = [timestamp !== '—' ? timestamp : '', source].filter(Boolean).join(' • ');

    switch (type) {
      case 'task.started':
        return {
          status: 'started',
          summary: stringifyTraceValue(data?.description || 'Task execution started.', 700),
          meta: [meta, data?.manual ? 'manual run' : ''].filter(Boolean).join(' • ')
        };
      case 'task.progress': {
        const progress = data?.progress || {};
        const currentStep = String(progress?.current_step || data?.step_title || '').trim();
        const waiting = data?.waiting_for_next_step === true;
        return {
          status: waiting ? 'waiting' : 'progress',
          summary: stringifyTraceValue(currentStep || 'Task progress updated.', 900),
          meta
        };
      }
      case 'task.thinking':
        return {
          status: 'thinking',
          summary: stringifyTraceValue(data?.message || data?.summary || 'Agent is analyzing the task.', 900),
          meta
        };
      case 'task.tool_call': {
        const args = stringifyTraceValue(data?.arguments, 900);
        return {
          status: 'tool call',
          summary: [`Calling ${toolName || 'tool'}`, args ? `Arguments:\n${args}` : ''].filter(Boolean).join('\n'),
          meta
        };
      }
      case 'task.tool_result': {
        const success = data?.success !== false;
        const detail = success
          ? stringifyTraceValue(data?.result_preview || 'Tool completed successfully.', 1000)
          : stringifyTraceValue(data?.error || 'Tool failed.', 1000);
        return {
          status: success ? 'tool result' : 'tool error',
          summary: [`${success ? 'Completed' : 'Failed'} ${toolName || 'tool'}`, detail].filter(Boolean).join('\n'),
          meta
        };
      }
      case 'task.completed':
        return {
          status: 'completed',
          summary: stringifyTraceValue(data?.result || data?.description || 'Task completed.', 1000),
          meta
        };
      case 'task.failed':
        return {
          status: 'failed',
          summary: stringifyTraceValue(data?.error || 'Task failed.', 1000),
          meta
        };
      case 'task.blocked':
        return {
          status: 'blocked',
          summary: stringifyTraceValue(data?.reason || data?.agent_response || 'Task paused for user input.', 1000),
          meta
        };
      case 'task.resumed':
        return {
          status: 'resumed',
          summary: stringifyTraceValue(data?.message || 'Task resumed after user guidance.', 700),
          meta
        };
      default:
        if (!type.startsWith('task.')) return null;
        return {
          status: type.replace(/^task\./, '').replace(/_/g, ' '),
          summary: stringifyTraceValue(data?.summary || data?.description || type, 900),
          meta
        };
    }
  },

  buildExecutionTraceEntriesFromSteps() {
    const steps = Array.isArray(this.task?.execution_steps) ? this.task.execution_steps : [];
    return steps.map((step, index) => {
      const status = String(step?.status || 'step').trim().replace(/_/g, ' ') || 'step';
      const title = String(step?.title || `Step ${index + 1}`).trim();
      const detail = String(step?.detail || '').trim();
      const result = stringifyTraceValue(step?.result || step?.error || '', 900);
      const startedAt = formatDateTime(step?.started_at);
      const completedAt = formatDateTime(step?.completed_at);
      const meta = [
        startedAt !== '—' ? `Started ${startedAt}` : '',
        completedAt !== '—' ? `Completed ${completedAt}` : '',
        step?.tag ? String(step.tag).trim() : ''
      ].filter(Boolean).join(' • ');
      const summary = [title, detail, result].filter(Boolean).join('\n');
      return summary ? { status, summary, meta } : null;
    }).filter(Boolean);
  },
};
