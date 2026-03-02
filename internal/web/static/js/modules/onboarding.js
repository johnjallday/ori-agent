// Onboarding module - Simplified 3-phase conversational onboarding with Ori character

export class OnboardingManager {
  constructor() {
    this.currentPhase = 0;
    this.totalPhases = 3;
    this.modal = null;
    this.modalInstance = null;
    this.availableProviders = [];
    this.userName = '';
    this.assistantName = 'Ori';
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
    if (status.needs_onboarding) {
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

    // Phase 2: Done
    const startBtn = document.getElementById('startBtn');
    if (startBtn) {
      startBtn.addEventListener('click', () => this.completeOnboarding());
    }

    // Skip link
    const skipLink = document.getElementById('skipOnboardingLink');
    if (skipLink) {
      skipLink.addEventListener('click', (e) => {
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
      providerSelect.addEventListener('change', async (e) => {
        this.updateReasoningVisibility(e.target.value);
        await this.loadModels(e.target.value);
      });
    }

    // Enter key on name inputs advances
    const nameInput = document.getElementById('onboardingUserName');
    if (nameInput) {
      nameInput.addEventListener('keydown', (e) => {
        if (e.key === 'Enter') {
          e.preventDefault();
          const assistantInput = document.getElementById('onboardingAssistantName');
          if (assistantInput) assistantInput.focus();
        }
      });
    }

    const assistantNameInput = document.getElementById('onboardingAssistantName');
    if (assistantNameInput) {
      assistantNameInput.addEventListener('keydown', (e) => {
        if (e.key === 'Enter') {
          e.preventDefault();
          this.advanceFromWelcome();
        }
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
    try {
      const response = await fetch('/api/onboarding/status');
      if (!response.ok) throw new Error('Failed to fetch onboarding status');
      return await response.json();
    } catch (error) {
      console.error('Error checking onboarding status:', error);
      return { needs_onboarding: false };
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
      providerSelect.innerHTML = '<option value="">Select a provider...</option>';
      const ollamaProvider = available.find(p => p.name === 'ollama');

      available.forEach(provider => {
        const option = document.createElement('option');
        option.value = provider.name;
        option.textContent = provider.display_name;
        providerSelect.appendChild(option);
      });

      // Auto-select Ollama if available (free, local)
      if (ollamaProvider) {
        providerSelect.value = 'ollama';
        this.updateReasoningVisibility('ollama');
        await this.loadModels('ollama');
      }
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
      speechBubble.textContent = "I need an AI connection to work. Add an API key below, or install Ollama for local AI.";
    }
  }

  async loadModels(providerName) {
    const modelSelect = document.getElementById('onboardingSystemModel');
    if (!modelSelect) return;

    this.updateReasoningVisibility(providerName);
    modelSelect.innerHTML = '<option value="">Loading models...</option>';
    modelSelect.disabled = true;

    if (!providerName) {
      modelSelect.innerHTML = '<option value="">Select provider first...</option>';
      return;
    }

    try {
      const response = await fetch(`/api/settings/available-models?provider=${encodeURIComponent(providerName)}`);
      if (!response.ok) throw new Error('Failed to fetch models');
      const data = await response.json();

      if (!data.available) {
        modelSelect.innerHTML = '<option value="">Provider not available</option>';
        return;
      }

      modelSelect.innerHTML = '<option value="">Select a model...</option>';
      const modelOptions = this.normalizeModelOptions(data.model_options, data.models);
      modelOptions.forEach(modelOption => {
        const option = document.createElement('option');
        option.value = modelOption.id;
        option.textContent = this.formatModelOptionText(modelOption);
        modelSelect.appendChild(option);
      });

      modelSelect.disabled = false;
    } catch (error) {
      console.error('Error loading models:', error);
      modelSelect.innerHTML = '<option value="">Error loading models</option>';
    }
  }

  normalizeModelOptions(rawModelOptions, fallbackModels) {
    if (Array.isArray(rawModelOptions) && rawModelOptions.length > 0) {
      return rawModelOptions
        .filter(option => option && typeof option.id === 'string' && option.id.length > 0)
        .map(option => ({
          id: option.id,
          label: (typeof option.label === 'string' && option.label.length > 0) ? option.label : option.id,
          description: (typeof option.description === 'string') ? option.description : '',
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

  async saveSystemModel() {
    const providerSelect = document.getElementById('onboardingSystemProvider');
    const modelSelect = document.getElementById('onboardingSystemModel');
    const reasoningSelect = document.getElementById('onboardingSystemReasoning');

    const provider = providerSelect?.value;
    const model = modelSelect?.value;
    const reasoning_effort = provider === 'codex' ? (reasoningSelect?.value || 'medium') : '';

    if (!provider || !model) return true;

    try {
      const response = await fetch('/api/settings/system-model', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ provider, model, reasoning_effort })
      });

      if (!response.ok) throw new Error('Failed to save system model');

      const successAlert = document.getElementById('onboardingSystemModelSuccess');
      if (successAlert) {
        const label = this.availableProviders.find(p => p.name === provider)?.display_name || provider;
        const aName = this.assistantName || 'Ori';
        successAlert.textContent = `${aName} will use ${label} (${model}).`;
        successAlert.classList.remove('d-none');
      }
      return true;
    } catch (error) {
      console.error('Error saving system model:', error);
      return true; // Don't block progress
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
      if (speechBubble) speechBubble.textContent = "Got it! Now let's pick a model.";

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

    const wait = (ms) => new Promise(resolve => setTimeout(resolve, ms));

    // Step 1: Wobble (600ms)
    await wait(400); // brief pause after modal appears
    container.classList.add('egg-animating');
    await wait(700);

    // Step 2: Show crack (300ms)
    container.classList.add('egg-cracked');
    await wait(500);

    // Step 3: Second wobble
    container.classList.remove('egg-animating');
    void container.offsetHeight; // force reflow to re-trigger
    container.classList.add('egg-animating');
    await wait(600);

    // Step 4: Hatch - shells fly apart, Ori pops up
    container.classList.remove('egg-animating');
    container.classList.remove('egg-cracked');
    container.classList.add('egg-hatching');
    await wait(600);

    // Step 5: Reveal speech bubble and input
    const phase0 = document.getElementById('onboarding-phase-0');
    if (phase0) phase0.classList.add('egg-hatched');
  }

  showPhase(index) {
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
  }

  async advanceFromWelcome() {
    await this.saveNames();
    await this.completeStep('step-welcome');

    // Update speech bubble on model phase with user's name and assistant's name
    const name = this.userName || 'friend';
    const aName = this.assistantName || 'Ori';
    const speechBubble = document.getElementById('modelSpeech');
    if (speechBubble) {
      speechBubble.textContent = `Great to meet you, ${name}! I'm ${aName}. Let's pick the AI model I'll use.`;
    }

    this.currentPhase = 1;
    this.showPhase(1);

    // Load providers when entering model phase
    await this.loadProviders();
  }

  async advanceFromModel() {
    await this.saveSystemModel();
    await this.completeStep('step-model');

    // Update done speech with user's name
    const name = this.userName || 'friend';
    const doneSpeech = document.getElementById('doneSpeech');
    if (doneSpeech) {
      doneSpeech.textContent = `All set, ${name}! I'm ready to help. Let's go!`;
    }

    this.currentPhase = 2;
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
