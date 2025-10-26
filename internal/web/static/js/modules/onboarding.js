// Onboarding module - Handles the first-time user onboarding flow
export class OnboardingManager {
  constructor() {
    this.currentStep = 0;
    this.totalSteps = 5;
    this.modal = null;
    this.modalInstance = null;
    this.deviceInfo = null;
  }

  // Initialize the onboarding system
  async init() {
    this.modal = document.getElementById('onboardingModal');
    if (!this.modal) {
      console.error('Onboarding modal not found');
      return;
    }

    // Initialize Bootstrap modal
    this.modalInstance = new bootstrap.Modal(this.modal, {
      backdrop: 'static',
      keyboard: false
    });

    // Setup event listeners
    this.setupEventListeners();

    // Check if onboarding is needed
    const status = await this.checkOnboardingStatus();
    if (status.needs_onboarding) {
      // Show modal after a short delay to let the page load
      setTimeout(() => {
        this.showOnboarding();
      }, 500);
    }
  }

  // Setup event listeners for modal buttons
  setupEventListeners() {
    const nextBtn = document.getElementById('nextStepBtn');
    const prevBtn = document.getElementById('prevStepBtn');
    const skipBtn = document.getElementById('skipOnboardingBtn');
    const completeBtn = document.getElementById('completeOnboardingBtn');
    const closeBtn = document.getElementById('onboardingCloseBtn');

    if (nextBtn) {
      nextBtn.addEventListener('click', () => this.nextStep());
    }

    if (prevBtn) {
      prevBtn.addEventListener('click', () => this.previousStep());
    }

    if (skipBtn) {
      console.log('✅ Skip button found, adding click listener');
      skipBtn.addEventListener('click', () => {
        console.log('🖱️ Skip button clicked!');
        this.skipOnboarding();
      });
    } else {
      console.warn('⚠️ Skip button not found in DOM!');
    }

    if (completeBtn) {
      completeBtn.addEventListener('click', () => this.completeOnboarding());
    }

    if (closeBtn) {
      closeBtn.addEventListener('click', () => this.skipOnboarding());
    }

    // API Keys password toggle buttons
    const toggleOpenaiBtn = document.getElementById('toggleOpenaiKey');
    const toggleAnthropicBtn = document.getElementById('toggleAnthropicKey');

    if (toggleOpenaiBtn) {
      toggleOpenaiBtn.addEventListener('click', () => {
        const input = document.getElementById('openaiApiKey');
        input.type = input.type === 'password' ? 'text' : 'password';
      });
    }

    if (toggleAnthropicBtn) {
      toggleAnthropicBtn.addEventListener('click', () => {
        const input = document.getElementById('anthropicApiKey');
        input.type = input.type === 'password' ? 'text' : 'password';
      });
    }

    // Keyboard navigation
    this.keyboardHandler = (event) => {
      // Only handle keyboard events when modal is visible
      if (!this.modal.classList.contains('show')) {
        return;
      }

      switch(event.key) {
        case 'ArrowRight':
        case 'ArrowDown':
          event.preventDefault();
          if (this.currentStep < this.totalSteps - 1) {
            this.nextStep();
          }
          break;
        case 'ArrowLeft':
        case 'ArrowUp':
          event.preventDefault();
          if (this.currentStep > 0) {
            this.previousStep();
          }
          break;
        case 'Escape':
          event.preventDefault();
          this.skipOnboarding();
          break;
        case 'Enter':
          event.preventDefault();
          if (this.currentStep === this.totalSteps - 1) {
            this.completeOnboarding();
          } else {
            this.nextStep();
          }
          break;
      }
    };

    document.addEventListener('keydown', this.keyboardHandler);
  }

  // Check onboarding status from backend
  async checkOnboardingStatus() {
    try {
      const response = await fetch('/api/onboarding/status');
      if (!response.ok) {
        throw new Error('Failed to fetch onboarding status');
      }
      return await response.json();
    } catch (error) {
      console.error('Error checking onboarding status:', error);
      return { needs_onboarding: false };
    }
  }

  // Show the onboarding modal
  showOnboarding() {
    if (this.modalInstance) {
      this.currentStep = 0;
      this.updateStepDisplay();
      this.modalInstance.show();

      // Fetch device info when modal is shown
      this.fetchDeviceInfo();

      // Check if API keys are already configured
      this.checkAPIKeyStatus();
    }
  }

  // Check if API keys are configured (via env vars or settings)
  async checkAPIKeyStatus() {
    try {
      const response = await fetch('/api/api-key');
      if (!response.ok) {
        return;
      }

      const data = await response.json();

      // Show info message if env vars are set
      if (data.has_openai || data.has_anthropic) {
        const providers = [];
        if (data.has_openai) providers.push('OpenAI');
        if (data.has_anthropic) providers.push('Anthropic');

        const envInfo = document.getElementById('apiKeysEnvInfo');
        if (envInfo) {
          envInfo.innerHTML = `
            <div class="alert alert-success">
              <svg class="bi me-2" width="16" height="16" fill="currentColor">
                <use xlink:href="#check-circle-fill"/>
              </svg>
              ${providers.join(' and ')} API key${providers.length > 1 ? 's are' : ' is'} already configured. You can skip this step or add additional keys.
            </div>
          `;
          envInfo.classList.remove('d-none');
        }
      }
    } catch (error) {
      console.error('Error checking API key status:', error);
    }
  }

  // Fetch device information from the backend
  async fetchDeviceInfo() {
    try {
      const response = await fetch('/api/device/info');
      if (!response.ok) {
        throw new Error('Failed to fetch device info');
      }

      this.deviceInfo = await response.json();
      this.displayDeviceInfo();
    } catch (error) {
      console.error('Error fetching device info:', error);
      // Show error state
      document.getElementById('deviceInfoCard').innerHTML = `
        <div class="card-body">
          <div class="alert alert-danger">
            Failed to detect device information. Please try again later.
          </div>
        </div>
      `;
    }
  }

  // Display device information in the UI
  displayDeviceInfo() {
    if (!this.deviceInfo) return;

    // Hide loading card
    document.getElementById('deviceInfoCard').classList.add('d-none');

    // Show device info content
    document.getElementById('deviceInfoContent').classList.remove('d-none');

    // Populate detected info
    document.getElementById('detectedType').textContent = this.deviceInfo.type;
    document.getElementById('detectedOS').textContent = this.deviceInfo.os;
    document.getElementById('detectedArch').textContent = this.deviceInfo.arch;

    // Set dropdown to detected type
    const deviceTypeSelect = document.getElementById('deviceTypeSelect');
    if (deviceTypeSelect) {
      deviceTypeSelect.value = this.deviceInfo.type;

      // Listen for changes to device type
      deviceTypeSelect.addEventListener('change', async (e) => {
        await this.updateDeviceType(e.target.value);
      });
    }
  }

  // Update device type when user changes selection
  async updateDeviceType(newType) {
    try {
      const response = await fetch('/api/device/type', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
        },
        body: JSON.stringify({ device_type: newType }),
      });

      if (!response.ok) {
        throw new Error('Failed to update device type');
      }

      // Update local device info
      this.deviceInfo.type = newType;
      this.deviceInfo.user_set = true;

      // Update displayed info
      document.getElementById('detectedType').textContent = newType;

      console.log('Device type updated to:', newType);
    } catch (error) {
      console.error('Error updating device type:', error);
      alert('Failed to update device type. Please try again.');
    }
  }

  // Save API keys from the onboarding form
  async saveApiKeys() {
    const openaiKey = document.getElementById('openaiApiKey').value.trim();
    const anthropicKey = document.getElementById('anthropicApiKey').value.trim();

    // Hide previous messages
    document.getElementById('apiKeysSuccess').classList.add('d-none');
    document.getElementById('apiKeysError').classList.add('d-none');

    // Check if API keys are already configured (via env vars)
    let hasExistingKeys = false;
    try {
      const statusResponse = await fetch('/api/api-key');
      if (statusResponse.ok) {
        const data = await statusResponse.json();
        hasExistingKeys = data.has_openai || data.has_anthropic;
      }
    } catch (error) {
      console.error('Error checking API key status:', error);
    }

    // If both are empty and no existing keys, show validation error
    if (!openaiKey && !anthropicKey && !hasExistingKeys) {
      document.getElementById('apiKeysError').classList.remove('d-none');
      document.getElementById('apiKeysErrorMessage').textContent =
        'Please provide at least one API key to continue, or set OPENAI_API_KEY or ANTHROPIC_API_KEY environment variables.';
      return false;
    }

    // If both are empty but keys exist, skip saving (user chose to use env vars)
    if (!openaiKey && !anthropicKey) {
      return true;
    }

    try {
      const response = await fetch('/api/api-key', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
        },
        body: JSON.stringify({
          openai_api_key: openaiKey || undefined,
          anthropic_api_key: anthropicKey || undefined,
        }),
      });

      if (!response.ok) {
        throw new Error('Failed to save API keys');
      }

      // Show success message
      document.getElementById('apiKeysSuccess').classList.remove('d-none');
      console.log('API keys saved successfully');
      return true;
    } catch (error) {
      console.error('Error saving API keys:', error);
      // Show error message
      document.getElementById('apiKeysError').classList.remove('d-none');
      document.getElementById('apiKeysErrorMessage').textContent =
        'Failed to save API keys. You can add them later in Settings.';
      return false;
    }
  }

  // Move to next step
  async nextStep() {
    if (this.currentStep < this.totalSteps - 1) {
      // Save API keys if leaving step-1
      if (this.currentStep === 1) {
        const saved = await this.saveApiKeys();
        // Don't proceed if validation failed
        if (saved === false) {
          return;
        }
      }

      // Mark current step as completed
      await this.completeStep(`step-${this.currentStep}`);

      this.currentStep++;
      this.updateStepDisplay();
    }
  }

  // Move to previous step
  previousStep() {
    if (this.currentStep > 0) {
      this.currentStep--;
      this.updateStepDisplay();
    }
  }

  // Update the step display (show/hide steps, update progress)
  updateStepDisplay() {
    // Hide all steps
    const steps = document.querySelectorAll('.onboarding-step');
    steps.forEach(step => step.classList.add('d-none'));

    // Show current step
    const currentStepElement = document.getElementById(`step-${this.currentStep}`);
    if (currentStepElement) {
      currentStepElement.classList.remove('d-none');
    }

    // Update progress bar
    const progress = ((this.currentStep + 1) / this.totalSteps) * 100;
    const progressBar = document.getElementById('onboardingProgress');
    if (progressBar) {
      progressBar.style.width = `${progress}%`;
      progressBar.setAttribute('aria-valuenow', progress);
    }

    // Update step number
    const stepNum = document.getElementById('currentStepNum');
    if (stepNum) {
      stepNum.textContent = this.currentStep + 1;
    }

    // Update button visibility
    const prevBtn = document.getElementById('prevStepBtn');
    const nextBtn = document.getElementById('nextStepBtn');
    const completeBtn = document.getElementById('completeOnboardingBtn');

    if (prevBtn) {
      if (this.currentStep === 0) {
        prevBtn.classList.add('d-none');
      } else {
        prevBtn.classList.remove('d-none');
      }
    }

    if (nextBtn && completeBtn) {
      if (this.currentStep === this.totalSteps - 1) {
        nextBtn.classList.add('d-none');
        completeBtn.classList.remove('d-none');
      } else {
        nextBtn.classList.remove('d-none');
        completeBtn.classList.add('d-none');
      }
    }
  }

  // Mark a step as completed in the backend
  async completeStep(stepName) {
    try {
      const response = await fetch('/api/onboarding/step', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
        },
        body: JSON.stringify({ step_name: stepName }),
      });

      if (!response.ok) {
        throw new Error('Failed to complete step');
      }

      return await response.json();
    } catch (error) {
      console.error('Error completing step:', error);
    }
  }

  // Skip onboarding
  async skipOnboarding() {
    console.log('🚀 skipOnboarding called!');
    try {
      console.log('📡 Sending skip request to /api/onboarding/skip');
      const response = await fetch('/api/onboarding/skip', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
        },
      });

      console.log(`📊 Skip response: status=${response.status}, ok=${response.ok}`);

      if (!response.ok) {
        throw new Error('Failed to skip onboarding');
      }

      console.log('✅ Onboarding skipped successfully, hiding modal');
      if (this.modalInstance) {
        this.modalInstance.hide();
      }

      // Reload the page to show main UI
      console.log('🔄 Reloading page to show main interface');
      window.location.reload();
    } catch (error) {
      console.error('❌ Error skipping onboarding:', error);
      alert('Failed to skip onboarding. Please try again or check the console for errors.');
    }
  }

  // Complete onboarding
  async completeOnboarding() {
    try {
      // Mark last step as completed
      await this.completeStep(`step-${this.currentStep}`);

      // Mark onboarding as complete
      const response = await fetch('/api/onboarding/complete', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
        },
      });

      if (!response.ok) {
        throw new Error('Failed to complete onboarding');
      }

      if (this.modalInstance) {
        this.modalInstance.hide();
      }
    } catch (error) {
      console.error('Error completing onboarding:', error);
    }
  }

  // Reset onboarding (useful for testing)
  async resetOnboarding() {
    try {
      const response = await fetch('/api/onboarding/reset', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
        },
      });

      if (!response.ok) {
        throw new Error('Failed to reset onboarding');
      }

      return await response.json();
    } catch (error) {
      console.error('Error resetting onboarding:', error);
    }
  }

  // Cleanup method to remove event listeners and prevent memory leaks
  destroy() {
    if (this.keyboardHandler) {
      document.removeEventListener('keydown', this.keyboardHandler);
      this.keyboardHandler = null;
    }
  }
}

// Create a singleton instance
export const onboardingManager = new OnboardingManager();
