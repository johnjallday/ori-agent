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
    this.populateTimezoneSelect();
    if (status.needs_onboarding) {
      await this.loadWorkspaceRoot();
      setTimeout(() => this.showOnboarding(), 500);
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

    const modelBackBtn = document.getElementById('modelBackBtn');
    if (modelBackBtn) {
      modelBackBtn.addEventListener('click', () => this.showPhase(0));
    }

    // Phase 2: Done
    const startBtn = document.getElementById('startBtn');
    if (startBtn) {
      startBtn.addEventListener('click', () => this.completeOnboarding());
    }

    const doneBackBtn = document.getElementById('doneBackBtn');
    if (doneBackBtn) {
      doneBackBtn.addEventListener('click', () => this.showPhase(1));
    }

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

  // --- Phase navigation ---

  showOnboarding() {
    if (!this.modalInstance) return;

    this.currentPhase = 0;

    // Pre-fill names if returning
    const nameInput = document.getElementById('onboardingUserName');
    if (nameInput && this.userName) {
      nameInput.value = this.userName;
    }
    const assistantInput = document.getElementById('onboardingAssistantName');
    if (assistantInput && this.assistantName) {
      assistantInput.value = this.assistantName;
    }
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

  async advanceFromModel() {
    const modelSaved = await this.saveSystemModel();
    if (!modelSaved) {
      return;
    }
    await this.completeStep('step-model');

    // Update done speech with user's name
    const name = this.userName || 'friend';
    const doneSpeech = document.getElementById('doneSpeech');
    if (doneSpeech) {
      doneSpeech.textContent = `All set, ${name}! Your first mission is ready: let’s build the Personal HQ we’ll use to run everything else.`;
    }

    this.showPhase(2);
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

  async completeOnboarding() {
    try {
      await this.completeStep('step-done');

      const response = await fetch('/api/onboarding/complete', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' }
      });

      if (!response.ok) throw new Error('Failed to complete onboarding');

      if (this.modalInstance) this.modalInstance.hide();
      window.location.href = '/?focus=personal-hq';
    } catch (error) {
      console.error('Error completing onboarding:', error);
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
