// Onboarding module - Simplified 3-phase conversational onboarding with Ori character

import { loadOnboardingStatus } from './onboarding-gate.js';

const FALLBACK_TIMEZONES = [
  'UTC',
  'Africa/Cairo',
  'Africa/Johannesburg',
  'Africa/Lagos',
  'Africa/Nairobi',
  'America/Anchorage',
  'America/Argentina/Buenos_Aires',
  'America/Bogota',
  'America/Chicago',
  'America/Denver',
  'America/Los_Angeles',
  'America/Mexico_City',
  'America/New_York',
  'America/Phoenix',
  'America/Sao_Paulo',
  'America/Toronto',
  'America/Vancouver',
  'Asia/Bangkok',
  'Asia/Dubai',
  'Asia/Hong_Kong',
  'Asia/Jakarta',
  'Asia/Jerusalem',
  'Asia/Kolkata',
  'Asia/Seoul',
  'Asia/Shanghai',
  'Asia/Singapore',
  'Asia/Tokyo',
  'Australia/Adelaide',
  'Australia/Brisbane',
  'Australia/Melbourne',
  'Australia/Perth',
  'Australia/Sydney',
  'Europe/Amsterdam',
  'Europe/Berlin',
  'Europe/Dublin',
  'Europe/Istanbul',
  'Europe/Lisbon',
  'Europe/London',
  'Europe/Madrid',
  'Europe/Moscow',
  'Europe/Paris',
  'Europe/Rome',
  'Europe/Stockholm',
  'Pacific/Auckland',
  'Pacific/Honolulu'
];

export function personalAssistantResumeMessage(state = {}) {
  const name = String(state.display_name || '').trim();
  const hasAssistant = Boolean(state.assistant_id || name);
  const hasHQ = Boolean(state.hq_workspace_id);
  if (hasAssistant && hasHQ) {
    return `${name || 'Your assistant'} and Personal HQ are already saved. Retry to finish the remaining setup step.`;
  }
  if (hasHQ) return 'Personal HQ is already saved. Retry to finish the remaining setup step.';
  if (hasAssistant) {
    return `${name || 'Your assistant'} is already saved. Retry to finish the remaining setup step.`;
  }
  return 'This hire is already in progress. Retry to finish the remaining setup step.';
}

export function buildPersonalAssistantHirePayload({
  requestId,
  ifVersion = 0,
  displayName = 'Assistant',
  appearance = null,
  mandate = '',
  focusAreas = [],
  timezone = 'UTC',
  scheduleDays = [],
  scheduleTime = '08:00',
  notifyOnReady = false
} = {}) {
  return {
    request_id: String(requestId || '').trim(),
    if_version: Number(ifVersion) || 0,
    display_name: String(displayName || '').trim(),
    appearance: appearance || { mode: 'generated', generated: {} },
    mandate: String(mandate || '').trim(),
    focus_areas: Array.from(new Set((focusAreas || []).map(value => String(value).trim()))).filter(
      Boolean
    ),
    timezone: String(timezone || 'UTC').trim() || 'UTC',
    schedule_days: Array.from(
      new Set((scheduleDays || []).map(value => String(value).trim().toLowerCase()))
    ).filter(Boolean),
    schedule_time: String(scheduleTime || '08:00').trim() || '08:00',
    notify_on_ready: notifyOnReady === true
  };
}

const firstAssignmentTypes = new Set(['priority', 'i_owe', 'waiting_on', 'fixed_commitment']);

export function normalizeFirstAssignmentRows(rows = []) {
  return (Array.isArray(rows) ? rows : [])
    .map(row => ({
      type: String(row?.type || '')
        .trim()
        .toLowerCase(),
      title: String(row?.title || '').trim(),
      action: String(row?.action || '').trim(),
      detail: String(row?.detail || '').trim(),
      counterparty: String(row?.counterparty || '').trim(),
      due: String(row?.due || '').trim()
    }))
    .filter(
      row =>
        firstAssignmentTypes.has(row.type) &&
        Boolean(row.title || row.action || row.detail || row.counterparty || row.due)
    );
}

export function buildFirstAssignmentPreviewPayload(ifVersion, rows) {
  return {
    if_version: Number(ifVersion) || 0,
    rows: normalizeFirstAssignmentRows(rows)
  };
}

export function buildFirstAssignmentApplyPayload({ preview, stateVersion, applyRequestId } = {}) {
  return {
    preview_id: String(preview?.preview_id || '').trim(),
    preview_version: Number(preview?.assignment_version) || 0,
    payload_hash: String(preview?.payload_hash || '').trim(),
    if_version: Number(stateVersion) || 0,
    apply_request_id: String(applyRequestId || '').trim()
  };
}

export function canSubmitFirstAssignment({ confirmed = false, inFlight = false, preview } = {}) {
  return (
    confirmed === true && inFlight !== true && Boolean(String(preview?.preview_id || '').trim())
  );
}

export function firstAssignmentResumeView(current = {}) {
  const status = String(current.status || '').trim();
  return {
    stage:
      status === 'completed'
        ? 'complete'
        : status === 'applying'
          ? 'partial'
          : current.preview
            ? 'preview'
            : 'form',
    stateVersion: Number(current.state_version) || 0,
    preview: current.preview || null,
    applyRequestId: String(current.apply_request_id || '').trim(),
    brief: current.brief || null
  };
}

export function firstAssignmentResultView(result = {}) {
  const total = Math.max(0, Number(result.total_count) || 0);
  const applied = Math.max(0, Number(result.applied_count) || 0);
  const outcome = String(result.outcome || '').trim();
  const brief = result.brief || null;
  return {
    complete: outcome === 'complete' || outcome === 'complete_empty',
    empty: outcome === 'complete_empty' || total === 0,
    partialBrief: outcome === 'records_saved_brief_failed',
    retryable: result.retryable === true,
    summary:
      total === 0
        ? 'Nothing was created. Your brief honestly reflects an empty first assignment.'
        : `${applied} of ${total} confirmed record${total === 1 ? '' : 's'} saved.`,
    topItems: Array.isArray(brief?.top_items)
      ? brief.top_items
          .slice(0, 3)
          .map(item => String(typeof item === 'string' ? item : item?.title || '').trim())
          .filter(Boolean)
      : [],
    nextCheckIn: String(brief?.next_scheduled_check_in || ''),
    route: String(brief?.route || '/api/personal-hq/brief/current')
  };
}

export function workspaceRootSetupView(state) {
  const source = String(state?.source || 'unconfirmed')
    .trim()
    .toLowerCase();
  const configuredRoot = String(state?.workspace_root || '').trim();
  const effectiveRoot = String(state?.effective_workspace_root || '').trim();
  const suggestedRoot = String(state?.default_workspace_root || '').trim();
  const confirmed =
    state?.confirmed === true ||
    source === 'settings' ||
    source === 'environment' ||
    source === 'default';

  return {
    source,
    confirmed,
    path: configuredRoot || effectiveRoot || suggestedRoot,
    status: confirmed
      ? 'Confirmed. Ori will scan only this directory.'
      : 'Suggested location — Ori will not scan it until you continue.'
  };
}

export class OnboardingManager {
  constructor() {
    this.currentPhase = 0;
    this.totalPhases = 3;
    this.modal = null;
    this.modalInstance = null;
    this.availableProviders = [];
    this.userName = '';
    this.assistantName = 'Ori';
    this.timezone = '';
    this.workspaceRootState = null;
    this.personalAssistantState = null;
    this.personalAssistantAppearanceEditor = null;
    this.hireRequestId = '';
    this.hireStep = 0;
    this.modelConfigured = null;
    this.assignmentPreview = null;
    this.assignmentStep = 0;
    this.assignmentQuestMode = false;
    this.assignmentRows = [];
    this.assignmentStateVersion = 0;
    this.assignmentApplyRequestId = '';
    this.assignmentApplying = false;
    this.assignmentPreviewing = false;
  }

  async init() {
    this.modal = document.getElementById('onboardingModal');
    if (!this.modal) {
      return;
    }

    this.modalInstance = new bootstrap.Modal(this.modal, {
      backdrop: 'static',
      keyboard: false
    });

    this.setupEventListeners();

    const status = await this.checkOnboardingStatus();
    this.userName = status.user_name || '';
    this.assistantName = status.assistant_name || 'Ori';
    this.timezone = status.timezone || this.detectTimezone();
    await this.loadPersonalAssistantState();
    this.populateTimezoneSelect();
    if (status.needs_onboarding) {
      await this.loadWorkspaceRoot();
      setTimeout(() => this.showOnboarding(), 500);
    } else if (this.shouldOpenFirstAssignmentQuest()) {
      setTimeout(() => this.showFirstAssignmentQuest(), 100);
    }
  }

  setupEventListeners() {
    // Phase 0: Welcome
    const welcomeNextBtn = document.getElementById('welcomeNextBtn');
    if (welcomeNextBtn) {
      welcomeNextBtn.addEventListener('click', () => this.advanceFromWelcome());
    }

    // Phase 1: Model
    const modelNextBtn = document.getElementById('modelNextBtn');
    if (modelNextBtn) {
      modelNextBtn.addEventListener('click', () => this.advanceFromModel());
    }
    document
      .getElementById('continueWithoutModelBtn')
      ?.addEventListener('click', () => this.advanceFromModel({ skipModel: true }));

    const modelBackBtn = document.getElementById('modelBackBtn');
    if (modelBackBtn) {
      modelBackBtn.addEventListener('click', () => this.showPhase(0));
    }

    document
      .getElementById('pafHireBackBtn')
      ?.addEventListener('click', () => this.backPersonalAssistantHire());
    document
      .getElementById('pafHireNextBtn')
      ?.addEventListener('click', () => this.advancePersonalAssistantHire());
    document.getElementById('pafHireBtn')?.addEventListener('click', () => this.hireAssistant());
    document
      .getElementById('pafHireConfirm')
      ?.addEventListener('change', () => this.updateHireButtonState());
    document.getElementById('pafAssistantName')?.addEventListener('input', event => {
      this.personalAssistantAppearanceEditor?.setAgentName(event.target.value);
      this.updateHireButtonState();
    });
    document
      .getElementById('pafAssistantMandate')
      ?.addEventListener('input', () => this.updateHireButtonState());
    document
      .querySelectorAll('[name="pafFocus"], [name="pafBriefDay"]')
      .forEach(input => input.addEventListener('change', () => this.updateHireButtonState()));
    document
      .getElementById('pafBriefTime')
      ?.addEventListener('change', () => this.updateHireButtonState());

    document.querySelectorAll('[data-paf-add-row]').forEach(button => {
      button.addEventListener('click', () => this.addAssignmentRow(button.dataset.pafAddRow));
    });
    document
      .getElementById('pafAssignmentBackBtn')
      ?.addEventListener('click', () => this.backFirstAssignmentStep());
    document
      .getElementById('pafPreviewAssignmentBtn')
      ?.addEventListener('click', () => this.advanceFirstAssignmentStep());
    document.getElementById('pafAssignmentForm')?.addEventListener('input', () => {
      this.updateAssignmentStepNavigation();
    });
    document.getElementById('pafEditAssignmentBtn')?.addEventListener('click', () => {
      this.showAssignmentStep(0);
    });
    document
      .getElementById('pafReplacePreviewBtn')
      ?.addEventListener('click', () => this.saveAssignmentPreview(this.readEditedPreviewRows()));
    document.getElementById('pafAssignmentConfirm')?.addEventListener('change', event => {
      const apply = document.getElementById('pafApplyAssignmentBtn');
      if (apply) apply.disabled = !event.target.checked || this.assignmentApplying;
    });
    document
      .getElementById('pafApplyAssignmentBtn')
      ?.addEventListener('click', () => this.applyFirstAssignment());
    document.getElementById('pafOpenTodayBtn')?.addEventListener('click', () => {
      window.location.href = '/?view=today';
    });
    this.modal.addEventListener('keydown', event => {
      if (event.key === 'Escape' && this.assignmentQuestMode) {
        event.preventDefault();
        this.deferFirstAssignmentQuest();
      }
    });

    // Skip link
    const skipLink = document.getElementById('skipOnboardingLink');
    if (skipLink) {
      skipLink.addEventListener('click', e => {
        e.preventDefault();
        this.skipOnboarding();
      });
    }

    // API key toggle buttons
    this.setupKeyToggle('toggleOpenaiKey', 'openaiApiKey');
    this.setupKeyToggle('toggleAnthropicKey', 'anthropicApiKey');
    this.setupKeyToggle('toggleGeminiKey', 'geminiApiKey');

    // Save API key button
    const saveApiKeyBtn = document.getElementById('saveApiKeyBtn');
    if (saveApiKeyBtn) {
      saveApiKeyBtn.addEventListener('click', () => this.saveApiKeyAndReload());
    }

    // Provider change handler
    const providerSelect = document.getElementById('onboardingSystemProvider');
    if (providerSelect) {
      providerSelect.addEventListener('change', async e => {
        this.clearModelError();
        this.updateReasoningVisibility(e.target.value);
        await this.loadModels(e.target.value);
      });
    }

    const modelSelect = document.getElementById('onboardingSystemModel');
    if (modelSelect) {
      modelSelect.addEventListener('change', () => {
        this.clearModelError();
        this.updateModelStepState();
      });
    }

    // Enter key on name inputs advances
    const nameInput = document.getElementById('onboardingUserName');
    if (nameInput) {
      nameInput.addEventListener('keydown', e => {
        if (e.key === 'Enter') {
          e.preventDefault();
          const timezoneSelect = document.getElementById('onboardingTimezone');
          if (timezoneSelect) timezoneSelect.focus();
        }
      });
    }

    const timezoneSelect = document.getElementById('onboardingTimezone');
    if (timezoneSelect) {
      timezoneSelect.addEventListener('change', e => {
        this.timezone = e.target.value || '';
      });
    }

    const assistantNameInput = document.getElementById('onboardingAssistantName');
    if (assistantNameInput) {
      assistantNameInput.addEventListener('keydown', e => {
        if (e.key === 'Enter') {
          e.preventDefault();
          this.advanceFromWelcome();
        }
      });
    }

    const workspaceRootBrowseBtn = document.getElementById('onboardingWorkspaceRootBrowseBtn');
    if (workspaceRootBrowseBtn) {
      workspaceRootBrowseBtn.addEventListener('click', () => this.browseWorkspaceRoot());
    }
    const workspaceRootInput = document.getElementById('onboardingWorkspaceRootInput');
    if (workspaceRootInput) {
      workspaceRootInput.addEventListener('input', () => {
        const status = document.getElementById('onboardingWorkspaceRootStatus');
        const savedPath = workspaceRootSetupView(this.workspaceRootState).path;
        if (!status) return;
        if (workspaceRootInput.value.trim() === savedPath) {
          this.renderWorkspaceRootState();
          return;
        }
        status.textContent = 'Changed. Continue to confirm this directory.';
        status.classList.remove('is-confirmed');
        status.classList.add('is-unconfirmed');
      });
    }
  }

  setupKeyToggle(btnId, inputId) {
    const btn = document.getElementById(btnId);
    if (btn) {
      btn.addEventListener('click', () => {
        const input = document.getElementById(inputId);
        if (input) input.type = input.type === 'password' ? 'text' : 'password';
      });
    }
  }

  // --- API calls ---

  async checkOnboardingStatus() {
    return loadOnboardingStatus();
  }

  async loadPersonalAssistantState() {
    try {
      const response = await fetch('/api/personal-assistant', {
        headers: { Accept: 'application/json' }
      });
      if (!response.ok) throw new Error(`Personal assistant request failed (${response.status})`);
      const payload = await response.json();
      this.personalAssistantState = payload?.personal_assistant || null;
      this.hireRequestId = String(this.personalAssistantState?.hire_request_id || '').trim();
      const modelStatus = this.personalAssistantState?.availability?.model?.status;
      if (modelStatus) this.modelConfigured = modelStatus === 'available';
      return this.personalAssistantState;
    } catch (error) {
      console.warn('Could not load personal assistant setup state:', error);
      this.personalAssistantState = null;
      return null;
    }
  }

  async saveNames() {
    const nameInput = document.getElementById('onboardingUserName');
    const assistantInput = document.getElementById('onboardingAssistantName');
    const userName = nameInput?.value?.trim() || '';
    const assistantName = assistantInput?.value?.trim() || 'Ori';

    try {
      const response = await fetch('/api/onboarding/names', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ user_name: userName, assistant_name: assistantName })
      });
      if (!response.ok) throw new Error('Failed to save names');

      const data = await response.json();
      this.userName = data.user_name || '';
      this.assistantName = data.assistant_name || 'Ori';
      return true;
    } catch (error) {
      console.error('Error saving names:', error);
      return false;
    }
  }

  async saveTimezone() {
    const timezoneInput = document.getElementById('onboardingTimezone');
    const timezone = timezoneInput?.value?.trim() || this.detectTimezone();

    try {
      const response = await fetch('/api/onboarding/timezone', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ timezone })
      });
      if (!response.ok) throw new Error('Failed to save timezone');

      const data = await response.json();
      this.timezone = data.timezone || '';
      return true;
    } catch (error) {
      console.error('Error saving timezone:', error);
      return false;
    }
  }

  renderWorkspaceRootState() {
    const view = workspaceRootSetupView(this.workspaceRootState);
    const input = document.getElementById('onboardingWorkspaceRootInput');
    const status = document.getElementById('onboardingWorkspaceRootStatus');
    if (input && !input.value) input.value = view.path;
    if (status) {
      status.textContent = view.status;
      status.classList.toggle('is-confirmed', view.confirmed);
      status.classList.toggle('is-unconfirmed', !view.confirmed);
    }
  }

  showWorkspaceRootError(message) {
    const error = document.getElementById('onboardingWorkspaceRootError');
    if (!error) return;
    error.textContent = message || '';
    error.hidden = !message;
  }

  async loadWorkspaceRoot() {
    try {
      const response = await fetch('/api/settings/workspace-root', {
        headers: { Accept: 'application/json' }
      });
      if (!response.ok) throw new Error('Failed to load workspace directory');
      this.workspaceRootState = await response.json();
      this.renderWorkspaceRootState();
      return this.workspaceRootState;
    } catch (error) {
      console.error('Error loading workspace directory:', error);
      this.showWorkspaceRootError(
        'Could not load the suggested directory. Choose a folder to continue.'
      );
      return null;
    }
  }

  async browseWorkspaceRoot() {
    const button = document.getElementById('onboardingWorkspaceRootBrowseBtn');
    if (button) button.disabled = true;
    this.showWorkspaceRootError('');
    try {
      const response = await fetch('/api/folder-picker/select-path', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ title: 'Choose Workspace Directory' })
      });
      const result = await response.json().catch(() => ({}));
      if (!response.ok || !result.success)
        throw new Error(result.error || 'Folder picker unavailable');
      if (result.selected && result.path) {
        const input = document.getElementById('onboardingWorkspaceRootInput');
        const status = document.getElementById('onboardingWorkspaceRootStatus');
        if (input) input.value = result.path;
        if (status) {
          status.textContent = 'Selected. Continue to confirm this directory.';
          status.classList.remove('is-confirmed');
          status.classList.add('is-unconfirmed');
        }
      }
    } catch (error) {
      this.showWorkspaceRootError(error.message || 'Could not open the folder picker.');
    } finally {
      if (button) button.disabled = false;
    }
  }

  async saveWorkspaceRoot() {
    const input = document.getElementById('onboardingWorkspaceRootInput');
    const workspaceRoot = input?.value?.trim() || '';
    if (!workspaceRoot) {
      this.showWorkspaceRootError('Choose a workspace directory before continuing.');
      if (input) input.focus();
      return false;
    }

    this.showWorkspaceRootError('');
    try {
      const response = await fetch('/api/settings/workspace-root', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ workspace_root: workspaceRoot })
      });
      if (!response.ok)
        throw new Error((await response.text()) || 'Failed to save workspace directory');
      this.workspaceRootState = await response.json();
      this.renderWorkspaceRootState();
      return true;
    } catch (error) {
      console.error('Error saving workspace directory:', error);
      this.showWorkspaceRootError(error.message || 'Could not save the workspace directory.');
      return false;
    }
  }

  detectTimezone() {
    try {
      return Intl.DateTimeFormat().resolvedOptions().timeZone || '';
    } catch (_) {
      return '';
    }
  }

  getTimezoneOptions() {
    let zones = FALLBACK_TIMEZONES;
    if (typeof Intl !== 'undefined' && typeof Intl.supportedValuesOf === 'function') {
      try {
        const supported = Intl.supportedValuesOf('timeZone');
        if (Array.isArray(supported) && supported.length > 0) {
          zones = supported;
        }
      } catch (_) {
        zones = FALLBACK_TIMEZONES;
      }
    }

    const preferred = [this.timezone, this.detectTimezone(), 'UTC'].filter(Boolean);

    const allZones = new Set([...preferred, ...zones]);
    return Array.from(allZones).sort((a, b) => {
      if (a === 'UTC') return -1;
      if (b === 'UTC') return 1;
      return a.localeCompare(b);
    });
  }

  formatTimezoneLabel(timezone) {
    if (!timezone || timezone === 'UTC') {
      return timezone || '';
    }
    return timezone
      .split('/')
      .map(part => part.replace(/_/g, ' '))
      .join(' / ');
  }

  populateTimezoneSelect() {
    const select = document.getElementById('onboardingTimezone');
    if (!select) return;

    const selected = this.timezone || this.detectTimezone();
    const zones = this.getTimezoneOptions();
    select.innerHTML = '';

    if (!selected) {
      const placeholder = document.createElement('option');
      placeholder.value = '';
      placeholder.textContent = 'Select timezone…';
      select.appendChild(placeholder);
    }

    zones.forEach(zone => {
      const option = document.createElement('option');
      option.value = zone;
      option.textContent = this.formatTimezoneLabel(zone);
      select.appendChild(option);
    });

    if (selected) {
      select.value = selected;
    }
  }

  async loadProviders() {
    const providerSelect = document.getElementById('onboardingSystemProvider');
    if (!providerSelect) return;

    try {
      const response = await fetch('/api/providers');
      if (!response.ok) throw new Error('Failed to fetch providers');
      const data = await response.json();
      this.availableProviders = data.providers || [];

      const available = this.availableProviders.filter(p => p.available);

      if (available.length === 0) {
        // No providers — show API key section instead
        this.showApiKeySection();
        return;
      }

      // Show model selection, hide API key section
      const modelSection = document.getElementById('onboardingModelSelection');
      const apiKeySection = document.getElementById('onboardingApiKeySection');
      if (modelSection) modelSection.classList.remove('d-none');
      if (apiKeySection) apiKeySection.classList.add('d-none');

      // Populate provider dropdown
      providerSelect.innerHTML = '<option value="">Select a provider…</option>';
      const preferredLocalProvider = available.find(p =>
        ['ollama', 'lmstudio', 'mlx_lm'].includes(p.name)
      );

      available.forEach(provider => {
        const option = document.createElement('option');
        option.value = provider.name;
        option.textContent = provider.display_name;
        providerSelect.appendChild(option);
      });

      // Prefer a local provider, otherwise choose the first usable provider so
      // setup starts from a working default instead of an empty decision.
      const defaultProvider = preferredLocalProvider || available[0];
      providerSelect.value = defaultProvider.name;
      this.updateReasoningVisibility(defaultProvider.name);
      await this.loadModels(defaultProvider.name);
    } catch (error) {
      console.error('Error loading providers:', error);
      this.showApiKeySection();
    }
  }

  showApiKeySection() {
    const modelSection = document.getElementById('onboardingModelSelection');
    const apiKeySection = document.getElementById('onboardingApiKeySection');
    const speechBubble = document.getElementById('modelSpeech');

    if (modelSection) modelSection.classList.add('d-none');
    if (apiKeySection) apiKeySection.classList.remove('d-none');
    if (speechBubble) {
      speechBubble.textContent =
        'I need an AI connection to work. Add an API key below, or run Ollama, LM Studio, or MLX-LM for local AI.';
    }
    this.updateModelStepState();
  }

  async loadModels(providerName) {
    const modelSelect = document.getElementById('onboardingSystemModel');
    if (!modelSelect) return;

    this.updateReasoningVisibility(providerName);
    modelSelect.innerHTML = '<option value="">Loading models…</option>';
    modelSelect.disabled = true;

    if (!providerName) {
      modelSelect.innerHTML = '<option value="">Select provider first…</option>';
      this.updateModelStepState();
      return;
    }

    try {
      const response = await fetch(
        `/api/settings/available-models?provider=${encodeURIComponent(providerName)}`
      );
      if (!response.ok) throw new Error('Failed to fetch models');
      const data = await response.json();

      if (!data.available) {
        modelSelect.innerHTML = '<option value="">Provider not available</option>';
        this.updateModelStepState();
        return;
      }

      modelSelect.innerHTML = '<option value="">Select a model…</option>';
      const modelOptions = this.normalizeModelOptions(data.model_options, data.models);
      modelOptions.forEach(modelOption => {
        const option = document.createElement('option');
        option.value = modelOption.id;
        option.textContent = this.formatModelOptionText(modelOption);
        modelSelect.appendChild(option);
      });

      modelSelect.disabled = false;
      const defaultModel = modelOptions.find(option => option.recommended) || modelOptions[0];
      if (defaultModel) {
        modelSelect.value = defaultModel.id;
      }
      this.updateModelStepState();
    } catch (error) {
      console.error('Error loading models:', error);
      modelSelect.innerHTML = '<option value="">Error loading models</option>';
      this.updateModelStepState();
    }
  }

  normalizeModelOptions(rawModelOptions, fallbackModels) {
    if (Array.isArray(rawModelOptions) && rawModelOptions.length > 0) {
      return rawModelOptions
        .filter(option => option && typeof option.id === 'string' && option.id.length > 0)
        .map(option => ({
          id: option.id,
          label:
            typeof option.label === 'string' && option.label.length > 0 ? option.label : option.id,
          description: typeof option.description === 'string' ? option.description : '',
          recommended: Boolean(option.recommended)
        }));
    }

    const models = Array.isArray(fallbackModels) ? fallbackModels : [];
    return models
      .filter(model => typeof model === 'string' && model.length > 0)
      .map(model => ({
        id: model,
        label: model,
        description: '',
        recommended: model.includes('haiku') || model.includes('4o-mini')
      }));
  }

  formatModelOptionText(modelOption) {
    const recommended = modelOption.recommended ? ' (Recommended)' : '';
    if (modelOption.description) {
      return `${modelOption.label}${recommended} — ${modelOption.description}`;
    }
    return `${modelOption.label}${recommended}`;
  }

  updateReasoningVisibility(providerName) {
    const field = document.getElementById('onboardingSystemReasoningField');
    const select = document.getElementById('onboardingSystemReasoning');
    if (!field || !select) return;

    if (providerName === 'codex') {
      field.classList.remove('d-none');
      select.disabled = false;
    } else {
      field.classList.add('d-none');
      select.disabled = true;
      select.value = 'medium';
    }
  }

  clearModelError() {
    const errorEl = document.getElementById('onboardingModelError');
    if (!errorEl) return;
    errorEl.textContent = '';
    errorEl.classList.add('d-none');
  }

  showModelError(message) {
    const errorEl = document.getElementById('onboardingModelError');
    if (!errorEl) return;
    errorEl.textContent = message;
    errorEl.classList.remove('d-none');
  }

  hasUsableModelSelection() {
    const provider = document.getElementById('onboardingSystemProvider')?.value || '';
    const model = document.getElementById('onboardingSystemModel')?.value || '';
    return Boolean(provider && model);
  }

  updateModelStepState() {
    const nextBtn = document.getElementById('modelNextBtn');
    if (!nextBtn) return;
    nextBtn.disabled = !this.hasUsableModelSelection();
  }

  async saveSystemModel() {
    const providerSelect = document.getElementById('onboardingSystemProvider');
    const modelSelect = document.getElementById('onboardingSystemModel');
    const reasoningSelect = document.getElementById('onboardingSystemReasoning');

    const provider = providerSelect?.value;
    const model = modelSelect?.value;
    const reasoning_effort = provider === 'codex' ? reasoningSelect?.value || 'medium' : '';

    if (!provider || !model) {
      this.showModelError('Choose a provider and model before continuing, or use Set Up Later.');
      return false;
    }

    try {
      const response = await fetch('/api/settings/system-model', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ provider, model, reasoning_effort })
      });

      if (!response.ok) throw new Error('Failed to save system model');

      const successAlert = document.getElementById('onboardingSystemModelSuccess');
      if (successAlert) {
        const label =
          this.availableProviders.find(p => p.name === provider)?.display_name || provider;
        const aName = this.assistantName || 'Ori';
        successAlert.textContent = `${aName} will use ${label} (${model}).`;
        successAlert.classList.remove('d-none');
      }

      if (typeof EventBus !== 'undefined') {
        EventBus.emit('systemModel:changed', { provider, model });
      }

      return true;
    } catch (error) {
      console.error('Error saving system model:', error);
      this.showModelError('Could not save the model yet. Check the provider, then try again.');
      return false;
    }
  }

  async saveApiKeyAndReload() {
    const openaiKey = document.getElementById('openaiApiKey')?.value?.trim() || '';
    const anthropicKey = document.getElementById('anthropicApiKey')?.value?.trim() || '';
    const geminiKey = document.getElementById('geminiApiKey')?.value?.trim() || '';

    const successEl = document.getElementById('apiKeySaveSuccess');
    const errorEl = document.getElementById('apiKeySaveError');
    const errorMsg = document.getElementById('apiKeySaveErrorMessage');

    if (successEl) successEl.classList.add('d-none');
    if (errorEl) errorEl.classList.add('d-none');

    if (!openaiKey && !anthropicKey && !geminiKey) {
      if (errorEl && errorMsg) {
        errorMsg.textContent = 'Please enter at least one API key.';
        errorEl.classList.remove('d-none');
      }
      return;
    }

    try {
      const response = await fetch('/api/api-key', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          openai_api_key: openaiKey || undefined,
          anthropic_api_key: anthropicKey || undefined,
          gemini_api_key: geminiKey || undefined
        })
      });

      if (!response.ok) throw new Error('Failed to save API key');

      if (successEl) successEl.classList.remove('d-none');

      // Re-check providers after saving key
      const speechBubble = document.getElementById('modelSpeech');
      if (speechBubble) speechBubble.textContent = 'Got it! Now let’s pick a model.';

      await this.loadProviders();
    } catch (error) {
      console.error('Error saving API key:', error);
      if (errorEl && errorMsg) {
        errorMsg.textContent = 'Failed to save API key. Please try again.';
        errorEl.classList.remove('d-none');
      }
    }
  }

  async completeStep(stepName) {
    try {
      await fetch('/api/onboarding/step', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ step_name: stepName })
      });
    } catch (error) {
      console.error('Error completing step:', error);
    }
  }

  // --- Personal Assistant hire -------------------------------------------

  shouldOpenFirstAssignmentQuest() {
    const requested = new URLSearchParams(window.location.search).get('quest');
    if (requested !== 'plan-first-day') return false;
    const state = this.personalAssistantState || {};
    return (
      ['active', 'paused'].includes(state.state) && state.first_assignment_status !== 'completed'
    );
  }

  showHireStep(index, { focus = true } = {}) {
    this.hireStep = Math.max(0, Math.min(2, Number(index) || 0));
    document.querySelectorAll('[data-paf-hire-step]').forEach(panel => {
      panel.classList.toggle('d-none', Number(panel.dataset.pafHireStep) !== this.hireStep);
    });
    const labels = ['Meet your assistant', 'Choose the focus', 'Set the rhythm'];
    const label = document.getElementById('pafHireStepLabel');
    if (label) label.textContent = `Hire step ${this.hireStep + 1} of 3 · ${labels[this.hireStep]}`;
    const bar = document.getElementById('pafHireStepBar');
    const track = bar?.parentElement;
    if (bar) bar.style.width = `${((this.hireStep + 1) / 3) * 100}%`;
    if (track) track.setAttribute('aria-valuenow', String(this.hireStep + 1));

    const back = document.getElementById('pafHireBackBtn');
    if (back) back.textContent = 'Back';
    document.getElementById('pafHireNextBtn')?.classList.toggle('d-none', this.hireStep === 2);
    document.getElementById('pafHireBtn')?.classList.toggle('d-none', this.hireStep !== 2);
    this.updateHireButtonState();

    if (focus) label?.focus();
  }

  hireStepIsValid(step = this.hireStep) {
    const name = document.getElementById('pafAssistantName')?.value?.trim() || '';
    const mandate = document.getElementById('pafAssistantMandate')?.value?.trim() || '';
    const focusCount = document.querySelectorAll('[name="pafFocus"]:checked').length;
    const dayCount = document.querySelectorAll('[name="pafBriefDay"]:checked').length;
    const confirmed = document.getElementById('pafHireConfirm')?.checked === true;
    if (step === 0) return !!name;
    if (step === 1) return !!mandate || focusCount > 0;
    return dayCount > 0 && confirmed;
  }

  advancePersonalAssistantHire() {
    if (!this.hireStepIsValid() || this.hireStep >= 2) return;
    this.showHireStep(this.hireStep + 1);
  }

  backPersonalAssistantHire() {
    if (this.hireStep > 0) {
      this.showHireStep(this.hireStep - 1);
      return;
    }
    document.getElementById('onboardingProgressShell')?.classList.remove('d-none');
    this.showPhase(1);
  }

  showPersonalAssistantHire() {
    document.getElementById('onboardingPersonalAssistantAssignment')?.classList.add('d-none');
    document.getElementById('onboardingPersonalAssistantHire')?.classList.remove('d-none');
    if (this.personalAssistantState?.state === 'active') this.hireStep = 2;
    document.getElementById('welcomeAssistantReveal')?.classList.add('d-none');
    document.getElementById('onboardingProgressShell')?.classList.add('d-none');
    this.assignmentQuestMode = false;

    const state = this.personalAssistantState || {};
    const nameInput = document.getElementById('pafAssistantName');
    if (nameInput && state.display_name) nameInput.value = state.display_name;
    const mandate = document.getElementById('pafAssistantMandate');
    if (mandate && state.mandate) mandate.value = state.mandate;
    if (Array.isArray(state.focus_areas) && state.focus_areas.length) {
      const selected = new Set(state.focus_areas);
      document.querySelectorAll('[name="pafFocus"]').forEach(input => {
        input.checked = selected.has(input.value);
      });
    }
    if (state.daily_brief) {
      const selectedDays = new Set(state.daily_brief.schedule_days || []);
      document.querySelectorAll('[name="pafBriefDay"]').forEach(input => {
        input.checked = selectedDays.has(input.value);
      });
      const timeInput = document.getElementById('pafBriefTime');
      if (timeInput && state.daily_brief.schedule_time) {
        timeInput.value = state.daily_brief.schedule_time;
      }
      const notifications = document.getElementById('pafNotifyOnReady');
      if (notifications) notifications.checked = state.daily_brief.notify_on_ready === true;
    }

    const host = document.getElementById('pafAssistantAppearance');
    if (host && window.AgentAppearanceEditor && !this.personalAssistantAppearanceEditor) {
      this.personalAssistantAppearanceEditor = window.AgentAppearanceEditor.create({
        host,
        idPrefix: 'pafAssistantAppearance',
        mode: 'create',
        allowedModes: ['generated', 'character'],
        appearance: state.appearance || { mode: 'generated', generated: {} },
        agent: {
          name: nameInput?.value || 'Assistant',
          source: 'user',
          role: 'orchestrator'
        },
        onChange: () => this.updateHireButtonState()
      });
    }

    const modelNote = document.getElementById('pafHireModelNote');
    if (modelNote) {
      if (this.modelConfigured === false) {
        modelNote.innerHTML =
          'No model is configured. Hiring, Personal HQ, and structured planning still work. <a href="/settings#system-model">Choose a model in Settings</a> when you want conversational answers.';
      } else {
        modelNote.textContent =
          'You can change the model later without changing this assistant relationship.';
      }
    }
    const status = document.getElementById('pafHireStatus');
    if (status) {
      status.textContent =
        state.state === 'repair_needed'
          ? personalAssistantResumeMessage(state)
          : state.state === 'hiring'
            ? personalAssistantResumeMessage(state)
            : state.state === 'active'
              ? `${state.display_name || 'Your assistant'} is hired. Continue to finish onboarding.`
              : '';
    }
    const button = document.getElementById('pafHireBtn');
    if (button && ['repair_needed', 'hiring', 'active'].includes(state.state)) {
      button.textContent = state.state === 'active' ? 'Finish onboarding' : 'Finish setup';
      const confirmation = document.getElementById('pafHireConfirm');
      if (confirmation) confirmation.checked = true;
      this.hireStep = 2;
    }
    this.showHireStep(this.hireStep);
  }

  updateHireButtonState() {
    const hire = document.getElementById('pafHireBtn');
    const next = document.getElementById('pafHireNextBtn');
    if (next) next.disabled = !this.hireStepIsValid(this.hireStep);
    if (hire) {
      hire.disabled =
        !this.hireStepIsValid(0) || !this.hireStepIsValid(1) || !this.hireStepIsValid(2);
    }
  }

  getHireRequestId() {
    if (this.hireRequestId) return this.hireRequestId;
    const storageKey = 'ori.personalAssistantHireRequestId';
    try {
      this.hireRequestId = String(window.localStorage.getItem(storageKey) || '').trim();
    } catch (_) {
      this.hireRequestId = '';
    }
    if (!this.hireRequestId) {
      this.hireRequestId =
        globalThis.crypto && typeof globalThis.crypto.randomUUID === 'function'
          ? globalThis.crypto.randomUUID()
          : `hire-${Date.now()}-${Math.random().toString(16).slice(2)}`;
      try {
        window.localStorage.setItem(storageKey, this.hireRequestId);
      } catch (_) {
        // The durable server operation remains authoritative when storage is unavailable.
      }
    }
    return this.hireRequestId;
  }

  personalAssistantHirePayload() {
    const focusAreas = Array.from(
      document.querySelectorAll('[name="pafFocus"]:checked'),
      input => input.value
    );
    const scheduleDays = Array.from(
      document.querySelectorAll('[name="pafBriefDay"]:checked'),
      input => input.value
    );
    return buildPersonalAssistantHirePayload({
      requestId: this.getHireRequestId(),
      ifVersion: this.personalAssistantState?.state_version || 0,
      displayName: document.getElementById('pafAssistantName')?.value || 'Assistant',
      appearance: this.personalAssistantAppearanceEditor?.createRequest(),
      mandate: document.getElementById('pafAssistantMandate')?.value || '',
      focusAreas,
      timezone: this.timezone || this.detectTimezone() || 'UTC',
      scheduleDays,
      scheduleTime: document.getElementById('pafBriefTime')?.value || '08:00',
      notifyOnReady: document.getElementById('pafNotifyOnReady')?.checked === true
    });
  }

  showHireError(message) {
    const error = document.getElementById('pafHireError');
    if (!error) return;
    error.textContent = message || '';
    error.classList.toggle('d-none', !message);
  }

  async hireAssistant() {
    const button = document.getElementById('pafHireBtn');
    const originalLabel = button?.textContent || 'Hire Assistant';
    if (button) {
      button.disabled = true;
      button.textContent = 'Creating Personal HQ…';
      button.setAttribute('aria-busy', 'true');
    }
    const status = document.getElementById('pafHireStatus');
    if (status)
      status.textContent = 'Creating one assistant and Personal HQ. Keep this window open.';
    this.showHireError('');
    try {
      const response = await fetch('/api/personal-assistant/hire', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
        body: JSON.stringify(this.personalAssistantHirePayload())
      });
      const payload = await response.json().catch(() => ({}));
      if (!response.ok) {
        if (payload.durable_result) {
          this.personalAssistantState = {
            ...this.personalAssistantState,
            ...payload.durable_result,
            state: 'repair_needed',
            repair_step: payload.repair_step
          };
          const status = document.getElementById('pafHireStatus');
          if (status)
            status.textContent = personalAssistantResumeMessage(this.personalAssistantState);
          if (button) button.textContent = 'Finish setup';
        }
        if (response.status === 409) await this.loadPersonalAssistantState();
        throw new Error(payload.error || 'Could not finish hiring. Retry this same request.');
      }

      this.personalAssistantState = payload.personal_assistant || this.personalAssistantState;
      if (status) {
        if (this.modelConfigured === false) {
          status.innerHTML =
            '<strong>Hired — choose a model to chat.</strong> Structured planning is ready now. <a href="/settings#system-model">Choose a model in Settings</a> later.';
          await new Promise(resolve => setTimeout(resolve, 350));
        } else {
          status.textContent = 'Assistant hired. Personal HQ is ready.';
        }
      }
      this.assignmentStateVersion = Number(this.personalAssistantState?.state_version) || 0;
      try {
        window.localStorage.removeItem('ori.personalAssistantHireRequestId');
      } catch (_) {
        // Storage cleanup is not part of the durable hire transaction.
      }
      await this.completePersonalAssistantOnboarding();
    } catch (error) {
      console.error('Error hiring personal assistant:', error);
      this.showHireError(error.message || 'Could not finish hiring. Retry this same request.');
      if (button) {
        button.disabled = false;
        button.removeAttribute('aria-busy');
        if (button.textContent === 'Creating Personal HQ…') button.textContent = originalLabel;
      }
    }
  }

  async completePersonalAssistantOnboarding() {
    await this.completeStep('step-done');
    const response = await fetch('/api/onboarding/complete', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' }
    });
    if (!response.ok) {
      throw new Error('Your assistant is hired, but onboarding could not be closed. Retry safely.');
    }
    const status = document.getElementById('pafHireStatus');
    if (status) status.textContent = 'Assistant hired. Your first quest is ready on Home.';
    this.modalInstance?.hide();
    window.location.href = '/';
  }

  // --- First assignment quest -------------------------------------------

  async showFirstAssignmentQuest() {
    this.assignmentQuestMode = true;
    this.showPhase(2);
    document.getElementById('onboardingProgressShell')?.classList.add('d-none');
    await this.showFirstAssignment();
    this.modal.addEventListener(
      'shown.bs.modal',
      () => {
        document.querySelector(`[data-paf-assignment-step="${this.assignmentStep}"]`)?.focus();
      },
      { once: true }
    );
    this.modalInstance?.show();
  }

  async showFirstAssignment() {
    document.getElementById('onboardingPersonalAssistantHire')?.classList.add('d-none');
    document.getElementById('onboardingPersonalAssistantAssignment')?.classList.remove('d-none');
    document.getElementById('pafAssignmentPreview')?.classList.add('d-none');
    document.getElementById('pafAssignmentResult')?.classList.add('d-none');
    document.getElementById('pafAssignmentForm')?.classList.remove('d-none');
    this.seedAssignmentRows();
    this.showAssignmentStep(this.assignmentStep, { focus: false });
    try {
      const response = await fetch('/api/personal-assistant/first-assignment', {
        headers: { Accept: 'application/json' }
      });
      const payload = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(payload.error || 'Could not resume the first assignment.');
      const current = payload.first_assignment || {};
      const resume = firstAssignmentResumeView(current);
      this.assignmentStateVersion = resume.stateVersion || this.assignmentStateVersion;
      if (resume.applyRequestId) this.assignmentApplyRequestId = resume.applyRequestId;
      if (resume.preview) {
        this.assignmentPreview = resume.preview;
        this.renderAssignmentPreview(resume.preview);
      }
      if (resume.stage === 'partial') {
        this.setAssignmentStatus(
          'Some records are already saved. Confirm Retry to continue safely.'
        );
        const apply = document.getElementById('pafApplyAssignmentBtn');
        if (apply) apply.textContent = 'Retry and finish';
      } else if (resume.stage === 'complete') {
        this.renderAssignmentResult({
          outcome: Number(current.preview?.count) === 0 ? 'complete_empty' : 'complete',
          applied_count: Number(current.preview?.count) || 0,
          total_count: Number(current.preview?.count) || 0,
          brief: resume.brief || null
        });
      }
    } catch (error) {
      this.showAssignmentError(error.message || 'Could not resume the first assignment.');
    }
  }

  showAssignmentStep(index, { focus = true } = {}) {
    this.assignmentStep = Math.max(0, Math.min(2, Number(index) || 0));
    document.getElementById('pafAssignmentForm')?.classList.remove('d-none');
    document.getElementById('pafAssignmentPreview')?.classList.add('d-none');
    document.getElementById('pafAssignmentResult')?.classList.add('d-none');
    document.querySelectorAll('[data-paf-assignment-step]').forEach(panel => {
      panel.classList.toggle(
        'd-none',
        Number(panel.dataset.pafAssignmentStep) !== this.assignmentStep
      );
    });
    const labels = ['Today’s priorities', 'Owed and waiting', 'Fixed commitments'];
    const label = document.getElementById('pafAssignmentStepLabel');
    if (label) {
      label.textContent = `Quest step ${this.assignmentStep + 1} of 4 · ${labels[this.assignmentStep]}`;
    }
    const bar = document.getElementById('pafAssignmentStepBar');
    const track = bar?.parentElement;
    if (bar) bar.style.width = `${((this.assignmentStep + 1) / 4) * 100}%`;
    if (track) track.setAttribute('aria-valuenow', String(this.assignmentStep + 1));
    this.updateAssignmentStepNavigation();
    if (focus) {
      document.querySelector(`[data-paf-assignment-step="${this.assignmentStep}"]`)?.focus();
    }
  }

  assignmentStepHasContent(step = this.assignmentStep) {
    const stepTypes = [
      new Set(['priority']),
      new Set(['i_owe', 'waiting_on']),
      new Set(['fixed_commitment'])
    ];
    return Array.from(document.querySelectorAll('[data-paf-assignment-row]')).some(row => {
      if (!stepTypes[step]?.has(row.dataset.pafAssignmentRow)) return false;
      return !!row.querySelector('[data-field="title"]')?.value?.trim();
    });
  }

  updateAssignmentStepNavigation() {
    const back = document.getElementById('pafAssignmentBackBtn');
    const next = document.getElementById('pafPreviewAssignmentBtn');
    if (back) back.textContent = this.assignmentStep === 0 ? 'Do this later' : 'Back';
    if (next) {
      next.textContent =
        this.assignmentStep === 2
          ? 'Review plan'
          : this.assignmentStepHasContent()
            ? 'Continue'
            : 'Skip for now';
    }
  }

  advanceFirstAssignmentStep() {
    if (this.assignmentStep < 2) {
      this.showAssignmentStep(this.assignmentStep + 1);
      return;
    }
    this.saveAssignmentPreview(this.readAssignmentRows());
  }

  backFirstAssignmentStep() {
    if (this.assignmentStep > 0) {
      this.showAssignmentStep(this.assignmentStep - 1);
      return;
    }
    this.deferFirstAssignmentQuest();
  }

  async deferFirstAssignmentQuest() {
    if (!this.assignmentQuestMode) return;
    const button = document.getElementById('pafAssignmentBackBtn');
    const errorTarget = document
      .getElementById('pafAssignmentPreview')
      ?.classList.contains('d-none')
      ? 'pafAssignmentError'
      : 'pafAssignmentApplyError';
    if (button) button.disabled = true;
    this.showAssignmentError('', errorTarget);
    try {
      const response = await fetch('/api/progression/skip', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
        body: JSON.stringify({ quest_id: 't1-plan-first-day' })
      });
      if (!response.ok) throw new Error('Could not save this quest for later. Try again.');
      this.modalInstance?.hide();
      window.history?.replaceState?.({}, '', '/');
      this.assignmentQuestMode = false;
      window.dispatchEvent(new CustomEvent('ori:progression-refresh'));
    } catch (error) {
      this.showAssignmentError(
        error.message || 'Could not save this quest for later.',
        errorTarget
      );
    } finally {
      if (button) button.disabled = false;
    }
  }

  seedAssignmentRows() {
    if (document.querySelector('[data-paf-assignment-row]')) return;
    this.addAssignmentRow('priority');
    this.addAssignmentRow('i_owe');
    this.addAssignmentRow('waiting_on');
    this.addAssignmentRow('fixed_commitment');
  }

  addAssignmentRow(type, values = {}) {
    const targets = {
      priority: 'pafPriorityRows',
      i_owe: 'pafCommitmentRows',
      waiting_on: 'pafCommitmentRows',
      fixed_commitment: 'pafFixedRows'
    };
    const container = document.getElementById(targets[type]);
    if (!container || !firstAssignmentTypes.has(type)) return;
    const row = document.createElement('div');
    row.className = 'paf-assignment-row';
    row.dataset.pafAssignmentRow = type;

    const titleWrap = document.createElement('label');
    titleWrap.className = 'form-label mb-0';
    titleWrap.textContent =
      type === 'priority'
        ? 'Priority'
        : type === 'i_owe'
          ? 'I owe'
          : type === 'waiting_on'
            ? 'Waiting on'
            : 'Commitment';
    const title = document.createElement('input');
    title.className = 'form-control';
    title.maxLength = 200;
    title.placeholder =
      type === 'fixed_commitment' ? 'Commitment or time to keep visible' : 'What is it?';
    title.value = values.title || '';
    title.dataset.field = 'title';
    titleWrap.appendChild(title);
    row.appendChild(titleWrap);

    if (type === 'i_owe' || type === 'waiting_on') {
      const partyWrap = document.createElement('label');
      partyWrap.className = 'form-label mb-0';
      partyWrap.textContent = 'Person (optional)';
      const party = document.createElement('input');
      party.className = 'form-control';
      party.maxLength = 200;
      party.value = values.counterparty || '';
      party.dataset.field = 'counterparty';
      partyWrap.appendChild(party);
      row.appendChild(partyWrap);
    }
    if (type === 'fixed_commitment') {
      const actionWrap = document.createElement('label');
      actionWrap.className = 'form-label mb-0';
      actionWrap.textContent = 'Action (optional)';
      const action = document.createElement('input');
      action.className = 'form-control';
      action.maxLength = 200;
      action.placeholder = 'Leave blank if a decision is needed';
      action.value = values.action || '';
      action.dataset.field = 'action';
      actionWrap.appendChild(action);
      row.appendChild(actionWrap);
    }

    const dueWrap = document.createElement('label');
    dueWrap.className = 'form-label mb-0';
    dueWrap.textContent = 'Due (optional)';
    const due = document.createElement('input');
    due.type = 'date';
    due.className = 'form-control';
    due.value = values.due || '';
    due.dataset.field = 'due';
    dueWrap.appendChild(due);
    row.appendChild(dueWrap);

    const remove = document.createElement('button');
    remove.type = 'button';
    remove.className = 'btn btn-sm btn-outline-secondary';
    remove.textContent = 'Remove';
    remove.addEventListener('click', () => {
      row.remove();
      this.updateAssignmentStepNavigation();
    });
    row.appendChild(remove);
    container.appendChild(row);
    this.updateAssignmentStepNavigation();
  }

  readAssignmentRows() {
    return normalizeFirstAssignmentRows(
      Array.from(document.querySelectorAll('[data-paf-assignment-row]'), row => ({
        type: row.dataset.pafAssignmentRow,
        title: row.querySelector('[data-field="title"]')?.value || '',
        action: row.querySelector('[data-field="action"]')?.value || '',
        counterparty: row.querySelector('[data-field="counterparty"]')?.value || '',
        due: row.querySelector('[data-field="due"]')?.value || ''
      }))
    );
  }

  readEditedPreviewRows() {
    return normalizeFirstAssignmentRows(
      Array.from(document.querySelectorAll('[data-paf-preview-index]'), row => {
        const index = Number(row.dataset.pafPreviewIndex) || 0;
        const item = this.assignmentPreview?.items?.[index] || {};
        const editedTitle = row.querySelector('[data-field="title"]')?.value || item.title || '';
        const due = row.querySelector('[data-field="due"]')?.value || '';
        const base = this.assignmentRows[index] || {};
        if (item.input_type === 'fixed_commitment' && item.record_type === 'ticket') {
          const fixedTitle = String(item.detail || '')
            .split('\n')[0]
            .replace(/^Fixed commitment:\s*/i, '')
            .trim();
          return {
            ...base,
            type: item.input_type,
            title: base.title || fixedTitle || editedTitle,
            action: editedTitle,
            due
          };
        }
        return { ...base, type: item.input_type, title: editedTitle, due, action: '' };
      })
    );
  }

  showAssignmentError(message, targetId = 'pafAssignmentError') {
    const error = document.getElementById(targetId);
    if (!error) return;
    error.textContent = message || '';
    error.classList.toggle('d-none', !message);
  }

  setAssignmentStatus(message) {
    const status = document.getElementById('pafAssignmentStatus');
    if (status) status.textContent = message || '';
  }

  async saveAssignmentPreview(rows) {
    if (this.assignmentPreviewing || this.assignmentApplying) return;
    this.assignmentPreviewing = true;
    const button = document.getElementById('pafPreviewAssignmentBtn');
    const replace = document.getElementById('pafReplacePreviewBtn');
    if (button) button.disabled = true;
    if (replace) replace.disabled = true;
    this.showAssignmentError('');
    this.showAssignmentError('', 'pafAssignmentApplyError');
    try {
      const normalized = normalizeFirstAssignmentRows(rows);
      const response = await fetch('/api/personal-assistant/first-assignment/preview', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
        body: JSON.stringify(
          buildFirstAssignmentPreviewPayload(this.assignmentStateVersion, normalized)
        )
      });
      const payload = await response.json().catch(() => ({}));
      if (!response.ok) {
        if (response.status === 409 && payload.state_version) {
          this.assignmentStateVersion =
            Number(payload.state_version) || this.assignmentStateVersion;
        }
        throw new Error(payload.error || 'Could not save this preview. Refresh and try again.');
      }
      const result = payload.first_assignment || {};
      this.assignmentRows = normalized;
      this.assignmentPreview = result.preview || null;
      this.assignmentStateVersion = Number(result.state_version) || this.assignmentStateVersion;
      this.assignmentApplyRequestId = '';
      this.renderAssignmentPreview(this.assignmentPreview);
    } catch (error) {
      const target = document.getElementById('pafAssignmentPreview')?.classList.contains('d-none')
        ? 'pafAssignmentError'
        : 'pafAssignmentApplyError';
      this.showAssignmentError(error.message || 'Could not save this preview.', target);
    } finally {
      this.assignmentPreviewing = false;
      if (button) button.disabled = false;
      if (replace) replace.disabled = false;
    }
  }

  renderAssignmentPreview(preview) {
    if (!preview) return;
    document.getElementById('pafAssignmentForm')?.classList.add('d-none');
    document.getElementById('pafAssignmentResult')?.classList.add('d-none');
    document.getElementById('pafAssignmentPreview')?.classList.remove('d-none');
    const count = document.getElementById('pafAssignmentPreviewCount');
    if (count) {
      count.textContent =
        preview.count === 0
          ? 'No records will be created. You can still confirm an honestly empty first brief.'
          : `${preview.count} canonical record${preview.count === 1 ? '' : 's'} will be created.`;
    }
    const host = document.getElementById('pafAssignmentPreviewRows');
    if (host) {
      host.replaceChildren();
      (preview.items || []).forEach((item, index) => {
        const row = document.createElement('div');
        row.className = 'paf-assignment-preview-row';
        row.dataset.pafPreviewIndex = String(index);
        const titleWrap = document.createElement('label');
        titleWrap.className = 'form-label mb-0';
        titleWrap.textContent =
          item.record_type === 'ticket'
            ? 'Personal HQ Ticket'
            : `Follow-up · ${String(item.category || '').replaceAll('_', ' ')}`;
        const title = document.createElement('input');
        title.className = 'form-control';
        title.maxLength = 200;
        title.value = item.title || '';
        title.dataset.field = 'title';
        titleWrap.appendChild(title);
        row.appendChild(titleWrap);
        const dueWrap = document.createElement('label');
        dueWrap.className = 'form-label mb-0';
        dueWrap.textContent = 'Due';
        const due = document.createElement('input');
        due.type = 'date';
        due.className = 'form-control';
        due.value = item.due || '';
        due.dataset.field = 'due';
        dueWrap.appendChild(due);
        row.appendChild(dueWrap);
        const state = document.createElement('span');
        state.className = 'paf-assignment-state';
        state.textContent =
          item.record_type === 'ticket' && item.awaiting_execution_intent
            ? 'Ready · not started'
            : item.state;
        row.appendChild(state);
        host.appendChild(row);
      });
    }
    const confirmation = document.getElementById('pafAssignmentConfirm');
    if (confirmation) confirmation.checked = false;
    const apply = document.getElementById('pafApplyAssignmentBtn');
    if (apply) {
      apply.disabled = true;
      apply.textContent = 'Create these items';
    }
    this.setAssignmentStatus('Preview saved. Nothing has been created yet.');
    document.querySelector('#pafAssignmentPreview h3')?.focus();
  }

  getAssignmentApplyRequestId() {
    if (this.assignmentApplyRequestId) return this.assignmentApplyRequestId;
    const previewId = this.assignmentPreview?.preview_id || 'pending';
    const key = `ori.personalAssistantApplyRequestId.${previewId}`;
    try {
      this.assignmentApplyRequestId = String(window.localStorage.getItem(key) || '').trim();
    } catch (_) {
      this.assignmentApplyRequestId = '';
    }
    if (!this.assignmentApplyRequestId) {
      this.assignmentApplyRequestId =
        globalThis.crypto && typeof globalThis.crypto.randomUUID === 'function'
          ? globalThis.crypto.randomUUID()
          : `apply-${Date.now()}-${Math.random().toString(16).slice(2)}`;
      try {
        window.localStorage.setItem(key, this.assignmentApplyRequestId);
      } catch (_) {
        // The durable server request ID remains authoritative.
      }
    }
    return this.assignmentApplyRequestId;
  }

  async applyFirstAssignment() {
    if (
      !canSubmitFirstAssignment({
        confirmed: document.getElementById('pafAssignmentConfirm')?.checked === true,
        inFlight: this.assignmentApplying,
        preview: this.assignmentPreview
      })
    )
      return;
    this.assignmentApplying = true;
    const apply = document.getElementById('pafApplyAssignmentBtn');
    if (apply) {
      apply.disabled = true;
      apply.textContent = 'Saving records…';
      apply.setAttribute('aria-busy', 'true');
    }
    this.showAssignmentError('', 'pafAssignmentApplyError');
    this.setAssignmentStatus('Saving confirmed Personal HQ records…');
    const briefTimer = window.setTimeout(
      () => this.setAssignmentStatus('Records are saved. Generating your first Daily Brief…'),
      250
    );
    try {
      const response = await fetch('/api/personal-assistant/first-assignment/apply', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
        body: JSON.stringify(
          buildFirstAssignmentApplyPayload({
            preview: this.assignmentPreview,
            stateVersion: this.assignmentStateVersion,
            applyRequestId: this.getAssignmentApplyRequestId()
          })
        )
      });
      const payload = await response.json().catch(() => ({}));
      const result = payload.first_assignment || null;
      if (result?.state_version) this.assignmentStateVersion = Number(result.state_version);
      if (!response.ok) {
        if (result?.retryable) {
          const view = firstAssignmentResultView(result);
          this.setAssignmentStatus(
            view.partialBrief
              ? 'Your records are saved. Daily Brief generation needs a safe retry.'
              : 'Some records are saved. Retry to continue from the durable checkpoint.'
          );
          if (apply) apply.textContent = 'Retry and finish';
        }
        throw new Error(payload.error || 'Could not finish the first assignment. Retry safely.');
      }
      this.renderAssignmentResult(result);
      window.dispatchEvent(new CustomEvent('ori:progression-refresh'));
    } catch (error) {
      this.showAssignmentError(
        error.message || 'Could not finish the first assignment.',
        'pafAssignmentApplyError'
      );
    } finally {
      window.clearTimeout(briefTimer);
      this.assignmentApplying = false;
      if (apply) {
        apply.disabled = document.getElementById('pafAssignmentConfirm')?.checked !== true;
        apply.removeAttribute('aria-busy');
        if (apply.textContent === 'Saving records…') apply.textContent = 'Create these items';
      }
    }
  }

  renderAssignmentResult(result) {
    const view = firstAssignmentResultView(result);
    document.getElementById('pafAssignmentForm')?.classList.add('d-none');
    document.getElementById('pafAssignmentPreview')?.classList.add('d-none');
    const panel = document.getElementById('pafAssignmentResult');
    panel?.classList.remove('d-none');
    const title = document.getElementById('pafAssignmentResultTitle');
    if (title)
      title.textContent = view.empty
        ? 'Your first brief is honestly empty'
        : 'Your first Daily Brief is ready';
    const summary = document.getElementById('pafAssignmentResultSummary');
    if (summary) summary.textContent = view.summary;
    const list = document.getElementById('pafAssignmentResultItems');
    if (list) {
      list.replaceChildren();
      view.topItems.forEach(item => {
        const li = document.createElement('li');
        li.textContent = item;
        list.appendChild(li);
      });
      list.classList.toggle('d-none', view.topItems.length === 0);
    }
    const next = document.getElementById('pafAssignmentNextCheckIn');
    if (next) {
      next.textContent = view.nextCheckIn
        ? `Next scheduled check-in: ${new Date(view.nextCheckIn).toLocaleString()}`
        : 'No scheduled check-in is enabled. You can refresh Today whenever you want.';
    }
    title?.focus();
  }

  // --- Phase navigation ---

  showOnboarding() {
    if (!this.modalInstance) return;

    this.currentPhase = 0;
    this.assignmentQuestMode = false;
    document.getElementById('onboardingProgressShell')?.classList.remove('d-none');

    // Pre-fill names if returning
    const nameInput = document.getElementById('onboardingUserName');
    if (nameInput && this.userName) {
      nameInput.value = this.userName;
    }
    const assistantInput = document.getElementById('onboardingAssistantName');
    if (assistantInput && this.assistantName) {
      assistantInput.value = this.assistantName;
    }
    document.getElementById('welcomeAssistantReveal')?.classList.add('d-none');
    if (assistantInput) assistantInput.value = 'Ori';
    const timezoneInput = document.getElementById('onboardingTimezone');
    if (timezoneInput) {
      this.populateTimezoneSelect();
    }
    this.renderWorkspaceRootState();

    this.showPhase(0);
    this.modalInstance.show();

    // Run the egg hatch animation sequence
    this.playEggHatchAnimation().then(() => {
      if (nameInput) nameInput.focus();
    });
  }

  async playEggHatchAnimation() {
    const container = document.getElementById('oriEggContainer');
    if (!container) return;
    const phase0 = document.getElementById('onboarding-phase-0');
    const reducedMotion =
      window.matchMedia && window.matchMedia('(prefers-reduced-motion: reduce)').matches;
    if (reducedMotion) {
      container.classList.add('egg-hatching');
      if (phase0) phase0.classList.add('egg-hatched');
      return;
    }

    const wait = ms => new Promise(resolve => setTimeout(resolve, ms));

    // Keep the character moment, but get users to the first action quickly.
    await wait(120);
    container.classList.add('egg-animating');
    await wait(260);

    container.classList.add('egg-cracked');
    await wait(180);

    container.classList.remove('egg-animating');
    void container.offsetHeight; // force reflow to re-trigger
    container.classList.add('egg-animating');
    await wait(220);

    container.classList.remove('egg-animating');
    container.classList.remove('egg-cracked');
    container.classList.add('egg-hatching');
    await wait(260);

    if (phase0) phase0.classList.add('egg-hatched');
  }

  showPhase(index) {
    this.currentPhase = index;

    // Hide all phases
    for (let i = 0; i < this.totalPhases; i++) {
      const phase = document.getElementById(`onboarding-phase-${i}`);
      if (phase) phase.classList.add('d-none');
    }

    // Show target phase
    const target = document.getElementById(`onboarding-phase-${index}`);
    if (target) {
      target.classList.remove('d-none');
      // Re-trigger animation
      target.style.animation = 'none';
      target.offsetHeight; // force reflow
      target.style.animation = '';
    }

    this.updateProgress(index);
    if (index === 1) {
      this.updateModelStepState();
    }
  }

  updateProgress(index) {
    const label = document.getElementById('onboardingStepLabel');
    const progress = document.getElementById('onboardingProgressBar');
    const track = progress?.parentElement;
    const current = Math.min(this.totalPhases, Math.max(1, index + 1));
    const pct = `${(current / this.totalPhases) * 100}%`;

    if (label) label.textContent = `Step ${current} of ${this.totalPhases}`;
    if (progress) progress.style.width = pct;
    if (track) track.setAttribute('aria-valuenow', String(current));
  }

  async advanceFromWelcome() {
    const nextButton = document.getElementById('welcomeNextBtn');
    const originalLabel = nextButton?.textContent || 'Continue';
    if (nextButton) {
      nextButton.disabled = true;
      nextButton.textContent = 'Confirming…';
    }

    const workspaceRootSaved = await this.saveWorkspaceRoot();
    if (!workspaceRootSaved) {
      if (nextButton) {
        nextButton.disabled = false;
        nextButton.textContent = originalLabel;
      }
      return;
    }

    await this.saveNames();
    await this.saveTimezone();
    await this.completeStep('step-welcome');

    // Update speech bubble on model phase with user's name and assistant's name
    const name = this.userName || 'friend';
    const aName = this.assistantName || 'Ori';
    const speechBubble = document.getElementById('modelSpeech');
    if (speechBubble) {
      speechBubble.textContent = `Great to meet you, ${name}! I’m ${aName}. Let’s pick the AI model I’ll use.`;
    }

    this.showPhase(1);

    // Load providers when entering model phase
    await this.loadProviders();
    if (nextButton) {
      nextButton.disabled = false;
      nextButton.textContent = originalLabel;
    }
  }

  async advanceFromModel({ skipModel = false } = {}) {
    if (!skipModel) {
      const modelSaved = await this.saveSystemModel();
      if (!modelSaved) {
        return;
      }
    }
    this.modelConfigured = !skipModel;
    await this.completeStep('step-model');

    this.showPhase(2);
    this.showPersonalAssistantHire();
  }

  async skipOnboarding() {
    try {
      const response = await fetch('/api/onboarding/skip', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' }
      });

      if (!response.ok) throw new Error('Failed to skip onboarding');

      if (this.modalInstance) this.modalInstance.hide();
      window.location.reload();
    } catch (error) {
      console.error('Error skipping onboarding:', error);
    }
  }

  async resetOnboarding() {
    try {
      const response = await fetch('/api/onboarding/reset', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' }
      });
      if (!response.ok) throw new Error('Failed to reset onboarding');
      return await response.json();
    } catch (error) {
      console.error('Error resetting onboarding:', error);
    }
  }

  destroy() {
    // No persistent listeners to clean up beyond what bootstrap handles
  }
}

// Create a singleton instance
export const onboardingManager = new OnboardingManager();
