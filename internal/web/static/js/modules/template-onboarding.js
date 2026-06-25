/**
 * Inline template onboarding intake panel.
 *
 * Owns the generated form for template-authored onboarding sessions. The
 * backend session is the source of truth; this panel mirrors it, pushes field
 * edits through PATCH /values, and lets workspace assistant messages update the
 * same visible controls through POST /extract.
 */

const TERMINAL_STATUSES = new Set(['succeeded', 'cancelled']);
const LOCKED_STATUSES = new Set(['running', 'succeeded', 'cancelled']);
const EXTRACTABLE_STATUSES = new Set(['collecting', 'ready_to_complete', 'blocked', 'failed']);

const STATUS_META = {
  pending_entry_agent: { label: 'Waiting for entry agent', tone: 'warning' },
  collecting: { label: 'Collecting answers', tone: 'active' },
  ready_to_complete: { label: 'Ready', tone: 'ready' },
  running: { label: 'Running', tone: 'active' },
  blocked: { label: 'Blocked', tone: 'warning' },
  failed: { label: 'Failed', tone: 'danger' },
  succeeded: { label: 'Complete', tone: 'success' },
  cancelled: { label: 'Cancelled', tone: 'muted' }
};

export class TemplateOnboardingPanel {
  constructor({ workspaceId, mount, onRefresh } = {}) {
    this.workspaceId = String(workspaceId || '').trim();
    this.mount = mount || null;
    this.onRefresh = typeof onRefresh === 'function' ? onRefresh : null;
    this.session = null;
    this.patchTimer = null;
    this.saving = false;
    this.completing = false;
    this.extracting = false;
    this.lastExtractKey = '';
    this.assistantListener = event => this.handleAssistantMessage(event);
  }

  async init() {
    if (!this.workspaceId || !this.mount) return;
    window.addEventListener('ori:workspace-assistant-message', this.assistantListener);
    await this.load();
    if (this.session && window.location.hash === '#template-onboarding') {
      window.setTimeout(() => this.mount.scrollIntoView({ behavior: 'smooth', block: 'start' }), 60);
    }
  }

  destroy() {
    window.removeEventListener('ori:workspace-assistant-message', this.assistantListener);
    if (this.patchTimer) {
      window.clearTimeout(this.patchTimer);
      this.patchTimer = null;
    }
  }

  async load() {
    if (!this.workspaceId || !this.mount) return;
    try {
      const response = await fetch(this.endpoint(''));
      if (response.status === 404) {
        this.session = null;
        this.mount.hidden = true;
        this.mount.innerHTML = '';
        return;
      }
      if (!response.ok) throw new Error('Failed to load template onboarding');
      this.applyPayload(await response.json());
    } catch (error) {
      this.session = {
        status: 'failed',
        fields: [],
        values: {},
        action_error: error.message || 'Failed to load template onboarding'
      };
      this.render();
    }
  }

  endpoint(path) {
    return `/api/workspaces/${encodeURIComponent(this.workspaceId)}/template-onboarding${path}`;
  }

  applyPayload(payload, options = {}) {
    const activeField = options.preserveFocus ? this.currentFocusedField() : '';
    this.session = normalizeSession(payload);
    this.render();
    if (activeField) this.restoreFocusedField(activeField);
  }

  render() {
    if (!this.mount || !this.session) return;
    this.mount.hidden = false;
    this.mount.classList.add('template-onboarding-panel');
    this.mount.dataset.status = this.session.status || '';

    const meta = STATUS_META[this.session.status] || { label: this.session.status || 'Unknown', tone: 'muted' };
    const locked = LOCKED_STATUSES.has(this.session.status);
    const missing = this.session.missing_required_fields || [];
    const blockers = uniqueStrings([
      ...(this.session.dependency_blockers || []),
      ...(this.session.blockers || [])
    ]);
    const canComplete = this.session.entry_agent_name && !locked && !missing.length;
    const primaryLabel = this.session.status === 'failed' ? 'Retry' : 'Create project';

    this.mount.innerHTML = `
      <div class="template-onboarding-header">
        <div>
          <div class="template-onboarding-kicker">Template Intake</div>
          <h2 class="template-onboarding-title">Project setup</h2>
        </div>
        <span class="template-onboarding-status is-${escapeAttr(meta.tone)}">${escapeHtml(meta.label)}</span>
      </div>
      <div class="template-onboarding-body">
        <form class="template-onboarding-form" data-template-onboarding-form>
          ${this.renderFields(locked)}
        </form>
        <aside class="template-onboarding-side" aria-live="polite">
          ${this.renderStateSummary(missing, blockers)}
          ${this.renderActionResult()}
          <div class="template-onboarding-actions">
            ${this.renderPrimaryAction(canComplete, primaryLabel)}
            ${this.renderCancelAction(locked)}
          </div>
        </aside>
      </div>
    `;

    this.bindRenderedEvents();
  }

  renderFields(locked) {
    const fields = this.session.fields || [];
    if (!fields.length) {
      return '<div class="template-onboarding-empty">No template fields are required.</div>';
    }
    return fields.map(field => this.renderField(field, locked)).join('');
  }

  renderField(field, locked) {
    const id = String(field.id || '').trim();
    const type = String(field.type || 'string').trim();
    const value = this.fieldValue(field);
    const required = Boolean(field.required);
    const missing = (this.session.missing_required_fields || []).includes(id);
    const controlId = `template-onboarding-field-${id}`;
    const describedBy = `${id}-template-onboarding-hint`;
    const disabled = locked ? ' disabled' : '';

    let control = '';
    if (type === 'enum') {
      const options = Array.isArray(field.options) ? field.options : [];
      control = `
        <select id="${escapeAttr(controlId)}" class="template-onboarding-control" data-field-id="${escapeAttr(id)}" aria-describedby="${escapeAttr(describedBy)}"${disabled}>
          <option value=""></option>
          ${options.map(option => {
            const selected = String(option) === String(value) ? ' selected' : '';
            return `<option value="${escapeAttr(option)}"${selected}>${escapeHtml(option)}</option>`;
          }).join('')}
        </select>`;
    } else if (type === 'boolean') {
      control = `
        <label class="template-onboarding-toggle">
          <input id="${escapeAttr(controlId)}" type="checkbox" data-field-id="${escapeAttr(id)}" ${value === true ? 'checked' : ''}${disabled}>
          <span>${escapeHtml(field.label || id)}${required ? '<span aria-hidden="true">*</span>' : ''}</span>
        </label>`;
    } else {
      const inputType = type === 'number' ? 'number' : 'text';
      const min = field.validation && field.validation.min != null ? ` min="${escapeAttr(field.validation.min)}"` : '';
      const max = field.validation && field.validation.max != null ? ` max="${escapeAttr(field.validation.max)}"` : '';
      const pattern = field.validation && field.validation.pattern ? ` pattern="${escapeAttr(field.validation.pattern)}"` : '';
      control = `
        <input id="${escapeAttr(controlId)}" class="template-onboarding-control" type="${inputType}" data-field-id="${escapeAttr(id)}" value="${escapeAttr(value == null ? '' : value)}"${min}${max}${pattern} aria-describedby="${escapeAttr(describedBy)}"${disabled}>`;
    }

    return `
      <div class="template-onboarding-field${missing ? ' is-missing' : ''}">
        ${type === 'boolean' ? '' : `<label class="template-onboarding-label" for="${escapeAttr(controlId)}">${escapeHtml(field.label || id)}${required ? '<span aria-hidden="true">*</span>' : ''}</label>`}
        ${control}
        <div id="${escapeAttr(describedBy)}" class="template-onboarding-hint">
          ${escapeHtml(field.prompt || (missing ? 'Required' : ''))}
        </div>
      </div>`;
  }

  renderStateSummary(missing, blockers) {
    const status = this.session.status;
    const lines = [];
    if (status === 'pending_entry_agent') {
      lines.push('Choose an entry agent before continuing.');
    }
    if (status === 'running') {
      lines.push('Creating the project and running the entry agent.');
    }
    if (status === 'succeeded') {
      lines.push('Template onboarding finished.');
    }
    if (status === 'cancelled') {
      lines.push('Template onboarding was cancelled.');
    }
    if (this.saving) lines.push('Saving answers.');
    if (this.extracting) lines.push('Reading the latest assistant message.');
    if (missing.length) lines.push(`Missing: ${missing.join(', ')}`);
    if (this.session.action_error && status !== 'blocked') lines.push(this.session.action_error);
    lines.push(...blockers);

    if (!lines.length) {
      lines.push(this.session.entry_agent_name ? `Entry agent: ${this.session.entry_agent_name}` : 'Ready for answers.');
    }

    return `
      <div class="template-onboarding-summary">
        ${lines.map(line => `<div class="template-onboarding-summary-line">${escapeHtml(line)}</div>`).join('')}
      </div>`;
  }

  renderActionResult() {
    const result = this.session.action_result;
    if (!result) return '';
    const rows = [];
    if (result.project_path) rows.push(['Project', result.project_path]);
    if (result.run_id) rows.push(['Run', result.run_id]);
    if (result.result) rows.push(['Result', result.result]);
    if (!rows.length) return '';
    return `
      <div class="template-onboarding-result">
        ${rows.map(([label, value]) => `
          <div class="template-onboarding-result-row">
            <span>${escapeHtml(label)}</span>
            <strong>${escapeHtml(value)}</strong>
          </div>`).join('')}
      </div>`;
  }

  renderPrimaryAction(canComplete, label) {
    const running = this.session.status === 'running' || this.completing;
    const terminal = TERMINAL_STATUSES.has(this.session.status);
    if (terminal) return '';
    return `
      <button type="button" class="modern-btn modern-btn-primary template-onboarding-primary" data-template-action="complete" ${(!canComplete || running) ? 'disabled' : ''}>
        <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
          <path d="M19,3H5C3.89,3 3,3.89 3,5V19A2,2 0 0,0 5,21H19A2,2 0 0,0 21,19V5C21,3.89 20.1,3 19,3M9,17L5,13L6.41,11.59L9,14.17L17.59,5.58L19,7L9,17Z"/>
        </svg>
        ${escapeHtml(running ? 'Creating' : label)}
      </button>`;
  }

  renderCancelAction(locked) {
    if (locked || TERMINAL_STATUSES.has(this.session.status)) return '';
    return `
      <button type="button" class="modern-btn modern-btn-secondary template-onboarding-cancel" data-template-action="cancel">
        Cancel
      </button>`;
  }

  bindRenderedEvents() {
    this.mount.querySelectorAll('[data-field-id]').forEach(control => {
      const eventName = control.type === 'checkbox' || control.tagName === 'SELECT' ? 'change' : 'input';
      control.addEventListener(eventName, () => this.queuePatch(control));
      control.addEventListener('blur', () => this.patchField(control));
    });

    this.mount.querySelector('[data-template-action="complete"]')?.addEventListener('click', () => {
      this.complete();
    });
    this.mount.querySelector('[data-template-action="cancel"]')?.addEventListener('click', () => {
      this.cancel();
    });
  }

  fieldValue(field) {
    const id = field.id;
    if (this.session.values && Object.prototype.hasOwnProperty.call(this.session.values, id)) {
      return this.session.values[id];
    }
    if (Object.prototype.hasOwnProperty.call(field, 'default')) {
      return field.default;
    }
    return field.type === 'boolean' ? false : '';
  }

  controlValue(control) {
    if (!control) return undefined;
    if (control.type === 'checkbox') return Boolean(control.checked);
    if (control.type === 'number') {
      const raw = String(control.value || '').trim();
      if (!raw) return undefined;
      const parsed = Number(raw);
      return Number.isFinite(parsed) ? parsed : raw;
    }
    return control.value;
  }

  queuePatch(control) {
    if (this.patchTimer) window.clearTimeout(this.patchTimer);
    this.patchTimer = window.setTimeout(() => this.patchField(control), 450);
  }

  async patchField(control) {
    const fieldId = control?.dataset?.fieldId;
    if (!fieldId || LOCKED_STATUSES.has(this.session?.status)) return;
    const value = this.controlValue(control);
    if (value === undefined) return;
    await this.patchValues({ [fieldId]: value }, { preserveFocus: true });
  }

  async patchAllCurrentValues() {
    const values = {};
    this.mount.querySelectorAll('[data-field-id]').forEach(control => {
      const value = this.controlValue(control);
      if (value !== undefined) values[control.dataset.fieldId] = value;
    });
    if (Object.keys(values).length) {
      await this.patchValues(values);
    }
  }

  async patchValues(values, options = {}) {
    this.saving = true;
    this.updateBusyState();
    try {
      const response = await fetch(this.endpoint('/values'), {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ values })
      });
      const payload = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(payload.error || 'Failed to save template answers');
      this.applyPayload(payload, options);
    } catch (error) {
      this.toast(error.message || 'Failed to save template answers', 'error');
    } finally {
      this.saving = false;
      this.updateBusyState();
    }
  }

  async complete() {
    if (this.completing || !this.session) return;
    this.completing = true;
    this.updateBusyState();
    try {
      await this.patchAllCurrentValues();
      const path = this.session.status === 'failed' ? '/retry' : '/complete';
      const response = await fetch(this.endpoint(path), { method: 'POST' });
      const payload = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(payload.error || 'Template onboarding could not complete');
      this.applyPayload(payload);
      if (payload.status === 'succeeded') {
        this.toast('Project setup complete', 'success');
        if (this.onRefresh) this.onRefresh();
      } else if (payload.status === 'blocked') {
        this.toast('Project setup is blocked', 'warning');
      } else if (payload.status === 'failed') {
        this.toast('Project setup failed', 'error');
      }
    } catch (error) {
      this.toast(error.message || 'Template onboarding could not complete', 'error');
    } finally {
      this.completing = false;
      this.updateBusyState();
    }
  }

  async cancel() {
    if (!this.session || LOCKED_STATUSES.has(this.session.status)) return;
    try {
      const response = await fetch(this.endpoint('/cancel'), { method: 'POST' });
      const payload = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(payload.error || 'Failed to cancel template onboarding');
      this.applyPayload(payload);
    } catch (error) {
      this.toast(error.message || 'Failed to cancel template onboarding', 'error');
    }
  }

  async handleAssistantMessage(event) {
    const detail = event?.detail || {};
    if (!this.session || detail.role !== 'user') return;
    if (String(detail.workspaceId || '').trim() !== this.workspaceId) return;
    if (!EXTRACTABLE_STATUSES.has(this.session.status)) return;
    const text = String(detail.text || '').trim();
    if (!text) return;
    const key = `${this.session.status}:${text}`;
    if (key === this.lastExtractKey) return;
    this.lastExtractKey = key;
    await this.extractFromText(text);
  }

  async extractFromText(text) {
    if (this.extracting || !text) return;
    this.extracting = true;
    this.updateBusyState();
    try {
      const response = await fetch(this.endpoint('/extract'), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ message: text })
      });
      const payload = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(payload.error || 'Failed to read onboarding answers');
      this.applyPayload(payload, { preserveFocus: true });
    } catch (error) {
      console.debug('Template onboarding extraction skipped:', error);
    } finally {
      this.extracting = false;
      this.updateBusyState();
    }
  }

  currentFocusedField() {
    const active = document.activeElement;
    if (!active || !this.mount.contains(active)) return '';
    return active.dataset?.fieldId || '';
  }

  restoreFocusedField(fieldId) {
    const control = this.mount.querySelector(`[data-field-id="${cssEscape(fieldId)}"]`);
    if (control && typeof control.focus === 'function') {
      control.focus();
    }
  }

  updateBusyState() {
    if (!this.mount) return;
    this.mount.classList.toggle('is-saving', this.saving || this.completing || this.extracting);
  }

  toast(message, type) {
    if (window.Toast && typeof window.Toast[type] === 'function') {
      window.Toast[type](message);
      return;
    }
    if (typeof window.showToast === 'function') {
      window.showToast(message, type);
    }
  }
}

function normalizeSession(payload) {
  return {
    workspace_id: String(payload?.workspace_id || ''),
    status: String(payload?.status || ''),
    fields: Array.isArray(payload?.fields) ? payload.fields : [],
    values: payload?.values && typeof payload.values === 'object' ? payload.values : {},
    missing_required_fields: Array.isArray(payload?.missing_required_fields) ? payload.missing_required_fields : [],
    dependency_blockers: Array.isArray(payload?.dependency_blockers) ? payload.dependency_blockers : [],
    entry_agent_name: String(payload?.entry_agent_name || ''),
    action_result: payload?.action_result || null,
    action_error: String(payload?.action_error || ''),
    blockers: Array.isArray(payload?.blockers) ? payload.blockers : [],
    completion: payload?.completion || {}
  };
}

function uniqueStrings(values) {
  const out = [];
  const seen = new Set();
  values.forEach(value => {
    const text = String(value || '').trim();
    if (!text || seen.has(text)) return;
    seen.add(text);
    out.push(text);
  });
  return out;
}

function escapeHtml(value) {
  const div = document.createElement('div');
  div.textContent = value == null ? '' : String(value);
  return div.innerHTML;
}

function escapeAttr(value) {
  return escapeHtml(value).replace(/"/g, '&quot;');
}

function cssEscape(value) {
  if (window.CSS && typeof window.CSS.escape === 'function') return window.CSS.escape(value);
  return String(value || '').replace(/["\\]/g, '\\$&');
}

window.TemplateOnboardingPanel = TemplateOnboardingPanel;
