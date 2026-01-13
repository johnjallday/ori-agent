// Onboarding module - Handles the first-time user onboarding flow
import { smartOnboardingManager } from './smartOnboarding.js';

export class OnboardingManager {
  constructor() {
    this.currentStep = 0;
    this.totalSteps = 7;
    this.modal = null;
    this.modalInstance = null;
    this.deviceInfo = null;
    this.availableProviders = [];
    this.smartOnboarding = smartOnboardingManager;
    this.completedSteps = new Set();
    this.skippedSteps = new Set();
    this.web3Connected = false;
    this.web3Address = null;
    this.web3ChainId = null;

    // Supported chains for Web3
    this.CHAINS = {
      1: { name: 'Ethereum Mainnet', symbol: 'ETH' },
      137: { name: 'Polygon', symbol: 'MATIC' },
      42161: { name: 'Arbitrum', symbol: 'ETH' },
      10: { name: 'Optimism', symbol: 'ETH' },
      8453: { name: 'Base', symbol: 'ETH' }
    };

    // Step names for dynamic title updates
    this.stepTitles = [
      'Welcome to Ori Agent',
      'Device Detection',
      'Configure API Keys',
      'Configure System Model',
      'Connect Web3 Wallet',
      'Personalize Your Experience',
      'Setup Complete'
    ];
  }

  // Initialize the onboarding system
  async init() {
    this.modal = document.getElementById('onboardingModal');
    if (!this.modal) {
      // Modal only exists on main chat page - silently return on other pages
      return;
    }

    // Initialize Bootstrap modal
    this.modalInstance = new bootstrap.Modal(this.modal, {
      backdrop: 'static',
      keyboard: false
    });

    // Setup event listeners
    this.setupEventListeners();

    // Initialize smart onboarding
    await this.smartOnboarding.init();

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

    // Web3 wallet connection buttons
    const web3ConnectBtn = document.getElementById('onboardingWeb3ConnectBtn');
    const web3DisconnectBtn = document.getElementById('onboardingWeb3DisconnectBtn');

    if (web3ConnectBtn) {
      web3ConnectBtn.addEventListener('click', () => this.connectWeb3Wallet());
    }

    if (web3DisconnectBtn) {
      web3DisconnectBtn.addEventListener('click', () => this.disconnectWeb3Wallet());
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

    // Step indicator click handlers
    this.setupStepIndicatorListeners();
  }

  // Setup step indicator click handlers
  setupStepIndicatorListeners() {
    const stepDots = document.querySelectorAll('#stepIndicator .step-dot');
    stepDots.forEach((dot) => {
      dot.addEventListener('click', (e) => {
        const stepIndex = parseInt(dot.dataset.step, 10);
        // Only allow clicking on completed steps
        if (this.completedSteps.has(stepIndex) || this.skippedSteps.has(stepIndex)) {
          this.navigateToStep(stepIndex);
        }
      });
    });
  }

  // Navigate to a specific step
  navigateToStep(stepIndex) {
    if (stepIndex >= 0 && stepIndex < this.totalSteps) {
      this.currentStep = stepIndex;
      this.updateStepDisplay();
    }
  }

  // Skip current step (individual step skip)
  async skipCurrentStep() {
    const stepName = `step-${this.currentStep}`;
    this.skippedSteps.add(this.currentStep);

    try {
      await fetch('/api/onboarding/skip-step', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ step_name: stepName })
      });
    } catch (error) {
      console.error('Error skipping step:', error);
    }

    if (this.currentStep < this.totalSteps - 1) {
      this.currentStep++;
      this.updateStepDisplay();
    }
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
      // Check API keys
      const response = await fetch('/api/api-key');
      if (response.ok) {
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
      }

      // Check for Ollama
      await this.checkOllamaStatus();
    } catch (error) {
      console.error('Error checking API key status:', error);
    }
  }

  // Check if Ollama is installed/running
  async checkOllamaStatus() {
    try {
      const response = await fetch('/api/device/ollama');
      if (!response.ok) {
        return;
      }

      const data = await response.json();

      // Show Ollama status if installed or running
      if (data.installed || data.running) {
        const ollamaInfo = document.getElementById('ollamaInfo');
        if (ollamaInfo) {
          let message = '';
          let alertClass = 'alert-info';

          if (data.running && data.models && data.models.length > 0) {
            alertClass = 'alert-success';
            const modelList = data.models.slice(0, 3).join(', ');
            const moreModels = data.models.length > 3 ? ` and ${data.models.length - 3} more` : '';
            message = `
              <strong>Ollama is running!</strong> You have local AI models available: ${modelList}${moreModels}.
              <br><small class="text-muted">You can use Ollama without API keys for offline AI capabilities.</small>
            `;
          } else if (data.running) {
            alertClass = 'alert-success';
            message = `
              <strong>Ollama is running!</strong> No models installed yet.
              <br><small class="text-muted">Run <code>ollama pull llama3.2</code> to download a model.</small>
            `;
          } else if (data.installed) {
            message = `
              <strong>Ollama is installed</strong>${data.version ? ` (v${data.version})` : ''} but not running.
              <br><small class="text-muted">Start Ollama to use local AI models without API keys.</small>
            `;
          }

          ollamaInfo.innerHTML = `<div class="alert ${alertClass}">${message}</div>`;
          ollamaInfo.classList.remove('d-none');
        }
      }
    } catch (error) {
      console.error('Error checking Ollama status:', error);
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

    // Show machine name and chip type if available (macOS)
    const machineSection = document.getElementById('deviceMachineSection');
    if (machineSection && (this.deviceInfo.machine_name || this.deviceInfo.chip_type)) {
      document.getElementById('detectedMachineName').textContent = this.deviceInfo.machine_name || '-';
      document.getElementById('detectedChipType').textContent = this.deviceInfo.chip_type || '-';
      machineSection.classList.remove('d-none');
    }

    // Show RAM in the grid
    const ramEl = document.getElementById('detectedRAM');
    if (ramEl && this.deviceInfo.total_ram_bytes > 0) {
      ramEl.textContent = this.formatBytes(this.deviceInfo.total_ram_bytes);
    }

    // Show GPU row if available
    const gpuRow = document.getElementById('deviceGPURow');
    const gpuEl = document.getElementById('detectedGPU');
    if (gpuRow && gpuEl && this.deviceInfo.gpu && this.deviceInfo.gpu.name) {
      gpuEl.textContent = this.deviceInfo.gpu.name;
      gpuRow.classList.remove('d-none');
    }

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

  // Format bytes to human-readable string
  formatBytes(bytes) {
    if (bytes === 0) return '0 bytes';
    const gb = bytes / (1024 * 1024 * 1024);
    if (gb >= 1) {
      return gb === Math.floor(gb) ? `${Math.floor(gb)} GB` : `${gb.toFixed(1)} GB`;
    }
    const mb = bytes / (1024 * 1024);
    if (mb >= 1) {
      return `${Math.floor(mb)} MB`;
    }
    return `${bytes} bytes`;
  }

  // Update device type when user changes selection
  async updateDeviceType(newType) {
    try {
      const response = await fetch('/api/device/type', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json'
        },
        body: JSON.stringify({ device_type: newType })
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

    // If both are empty and no existing keys, just allow continuing
    // User can add keys later in Settings
    if (!openaiKey && !anthropicKey && !hasExistingKeys) {
      return true;
    }

    // If both are empty but keys exist, skip saving (user chose to use env vars)
    if (!openaiKey && !anthropicKey) {
      return true;
    }

    try {
      const response = await fetch('/api/api-key', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json'
        },
        body: JSON.stringify({
          openai_api_key: openaiKey || undefined,
          anthropic_api_key: anthropicKey || undefined
        })
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

  // Load available providers for system model selection
  async loadSystemModelProviders() {
    const providerSelect = document.getElementById('onboardingSystemProvider');
    const modelSelect = document.getElementById('onboardingSystemModel');
    const statusIcon = document.getElementById('onboardingSystemModelStatusIcon');
    const statusText = document.getElementById('onboardingSystemModelStatusText');
    const statusDetails = document.getElementById('onboardingSystemModelStatusDetails');

    if (!providerSelect || !modelSelect) return;

    try {
      const response = await fetch('/api/providers');
      if (!response.ok) {
        throw new Error('Failed to fetch providers');
      }
      const data = await response.json();
      this.availableProviders = data.providers || [];

      // Populate provider dropdown
      providerSelect.innerHTML = '<option value="">Select a provider...</option>';
      const availableCount = this.availableProviders.filter(p => p.available).length;

      // Check if Ollama is available to auto-select it
      const ollamaProvider = this.availableProviders.find(p => p.available && p.name === 'ollama');

      this.availableProviders
        .filter(p => p.available)
        .forEach(provider => {
          const option = document.createElement('option');
          option.value = provider.name;
          option.textContent = provider.display_name;
          providerSelect.appendChild(option);
        });

      // Auto-select Ollama if available (local = no API costs)
      if (ollamaProvider) {
        providerSelect.value = 'ollama';
        // Trigger model loading for Ollama
        await this.loadSystemModelModels('ollama');
      }

      // Update status
      if (availableCount > 0) {
        statusIcon.innerHTML = `
          <svg width="24" height="24" viewBox="0 0 24 24" fill="#28a745">
            <path d="M12,2A10,10 0 0,1 22,12A10,10 0 0,1 12,22A10,10 0 0,1 2,12A10,10 0 0,1 12,2M12,4A8,8 0 0,0 4,12A8,8 0 0,0 12,20A8,8 0 0,0 20,12A8,8 0 0,0 12,4M11,16.5L6.5,12L7.91,10.59L11,13.67L16.59,8.09L18,9.5L11,16.5Z"/>
          </svg>
        `;
        statusText.textContent = `${availableCount} provider${availableCount > 1 ? 's' : ''} available`;
        statusDetails.textContent = 'Select a provider and model below.';
      } else {
        statusIcon.innerHTML = `
          <svg width="24" height="24" viewBox="0 0 24 24" fill="#ffc107">
            <path d="M13,14H11V10H13M13,18H11V16H13M1,21H23L12,2L1,21Z"/>
          </svg>
        `;
        statusText.textContent = 'No providers available';
        statusDetails.textContent = 'Please configure an API key first.';
      }

      // Setup provider change handler
      providerSelect.addEventListener('change', async (e) => {
        const provider = e.target.value;
        await this.loadSystemModelModels(provider);
      });
    } catch (error) {
      console.error('Error loading providers:', error);
      statusIcon.innerHTML = `
        <svg width="24" height="24" viewBox="0 0 24 24" fill="#dc3545">
          <path d="M12,2C17.53,2 22,6.47 22,12C22,17.53 17.53,22 12,22C6.47,22 2,17.53 2,12C2,6.47 6.47,2 12,2M15.59,7L12,10.59L8.41,7L7,8.41L10.59,12L7,15.59L8.41,17L12,13.41L15.59,17L17,15.59L13.41,12L17,8.41L15.59,7Z"/>
        </svg>
      `;
      statusText.textContent = 'Error loading providers';
      statusDetails.textContent = 'Please try again later.';
    }
  }

  // Load models for a specific provider
  async loadSystemModelModels(providerName) {
    const modelSelect = document.getElementById('onboardingSystemModel');
    if (!modelSelect) return;

    modelSelect.innerHTML = '<option value="">Loading models...</option>';
    modelSelect.disabled = true;

    if (!providerName) {
      modelSelect.innerHTML = '<option value="">Select provider first...</option>';
      return;
    }

    try {
      const response = await fetch(`/api/settings/available-models?provider=${encodeURIComponent(providerName)}`);
      if (!response.ok) {
        throw new Error('Failed to fetch models');
      }
      const data = await response.json();

      if (!data.available) {
        modelSelect.innerHTML = '<option value="">Provider not available</option>';
        return;
      }

      modelSelect.innerHTML = '<option value="">Select a model...</option>';
      const models = data.models || [];
      models.forEach(model => {
        const option = document.createElement('option');
        option.value = model;
        option.textContent = model;
        // Recommend fast models
        if (model.includes('haiku') || model.includes('4o-mini')) {
          option.textContent = model + ' (Recommended)';
        }
        modelSelect.appendChild(option);
      });

      modelSelect.disabled = false;
    } catch (error) {
      console.error('Error loading models:', error);
      modelSelect.innerHTML = '<option value="">Error loading models</option>';
    }
  }

  // Save system model configuration
  async saveSystemModel() {
    const providerSelect = document.getElementById('onboardingSystemProvider');
    const modelSelect = document.getElementById('onboardingSystemModel');
    const successAlert = document.getElementById('onboardingSystemModelSuccess');

    const provider = providerSelect?.value;
    const model = modelSelect?.value;

    // If both are empty, user is skipping - that's fine
    if (!provider && !model) {
      return true;
    }

    // If only one is selected, don't save
    if (!provider || !model) {
      return true;
    }

    try {
      const response = await fetch('/api/settings/system-model', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ provider, model })
      });

      if (!response.ok) {
        throw new Error('Failed to save system model');
      }

      if (successAlert) {
        successAlert.classList.remove('d-none');
      }
      console.log('System model saved successfully');
      return true;
    } catch (error) {
      console.error('Error saving system model:', error);
      return true; // Don't block onboarding progress
    }
  }

  // Initialize Web3 step UI based on wallet availability
  async initWeb3Step() {
    const statusIcon = document.getElementById('onboardingWeb3StatusIcon');
    const statusText = document.getElementById('onboardingWeb3StatusText');
    const statusDetails = document.getElementById('onboardingWeb3StatusDetails');
    const noWalletDiv = document.getElementById('onboardingWeb3NoWallet');
    const disconnectedDiv = document.getElementById('onboardingWeb3Disconnected');
    const connectedDiv = document.getElementById('onboardingWeb3Connected');

    // Check if ethereum provider exists
    if (typeof window.ethereum === 'undefined') {
      // No wallet detected
      statusIcon.innerHTML = `
        <svg width="24" height="24" viewBox="0 0 24 24" fill="#ffc107">
          <path d="M13,14H11V10H13M13,18H11V16H13M1,21H23L12,2L1,21Z"/>
        </svg>
      `;
      statusText.textContent = 'No Wallet Detected';
      statusDetails.textContent = 'Install MetaMask or another Web3 wallet';

      if (noWalletDiv) noWalletDiv.classList.remove('d-none');
      if (disconnectedDiv) disconnectedDiv.classList.add('d-none');
      if (connectedDiv) connectedDiv.classList.add('d-none');
      return;
    }

    // Check if already connected (from settings)
    try {
      const response = await fetch('/api/web3-wallet');
      const data = await response.json();

      if (data.connected) {
        // Check if wallet is still connected in browser
        const accounts = await window.ethereum.request({ method: 'eth_accounts' });

        if (accounts.length > 0 && accounts[0].toLowerCase() === data.address.toLowerCase()) {
          // Wallet is connected
          this.web3Connected = true;
          this.web3Address = data.address;
          this.web3ChainId = data.chain_id;
          this.showWeb3Connected(data);
          return;
        }
      }
    } catch (error) {
      console.error('Error checking Web3 wallet status:', error);
    }

    // Wallet available but not connected
    statusIcon.innerHTML = `
      <svg width="24" height="24" viewBox="0 0 24 24" fill="var(--bs-secondary)">
        <path d="M21,18V19A2,2 0 0,1 19,21H5C3.89,21 3,20.1 3,19V5A2,2 0 0,1 5,3H19A2,2 0 0,1 21,5V6H12C10.89,6 10,6.9 10,8V16A2,2 0 0,0 12,18M12,16H22V8H12M16,13.5A1.5,1.5 0 0,1 14.5,12A1.5,1.5 0 0,1 16,10.5A1.5,1.5 0 0,1 17.5,12A1.5,1.5 0 0,1 16,13.5Z"/>
      </svg>
    `;
    statusText.textContent = 'Ready to Connect';
    statusDetails.textContent = 'Click the button below to connect your wallet';

    if (noWalletDiv) noWalletDiv.classList.add('d-none');
    if (disconnectedDiv) disconnectedDiv.classList.remove('d-none');
    if (connectedDiv) connectedDiv.classList.add('d-none');
  }

  // Connect Web3 wallet
  async connectWeb3Wallet() {
    if (typeof window.ethereum === 'undefined') {
      this.showWeb3Alert('No Web3 wallet detected. Please install MetaMask.', 'warning');
      return;
    }

    const statusIcon = document.getElementById('onboardingWeb3StatusIcon');
    const statusText = document.getElementById('onboardingWeb3StatusText');
    const statusDetails = document.getElementById('onboardingWeb3StatusDetails');

    // Show connecting state
    statusIcon.innerHTML = '<span class="spinner-border spinner-border-sm" role="status"></span>';
    statusText.textContent = 'Connecting...';
    statusDetails.textContent = 'Please approve the connection in your wallet';

    try {
      // Request accounts
      const accounts = await window.ethereum.request({ method: 'eth_requestAccounts' });

      if (accounts.length === 0) {
        throw new Error('No accounts found');
      }

      this.web3Address = accounts[0];

      // Get chain ID
      const chainId = await window.ethereum.request({ method: 'eth_chainId' });
      this.web3ChainId = parseInt(chainId, 16);

      // Try to resolve ENS name (only on mainnet)
      let ensName = null;
      if (this.web3ChainId === 1 && typeof window.ethers !== 'undefined') {
        try {
          const provider = new window.ethers.BrowserProvider(window.ethereum);
          ensName = await provider.lookupAddress(this.web3Address);
        } catch {
          // ENS resolution failed, that's ok
        }
      }

      // Save to server
      await fetch('/api/web3-wallet', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          address: this.web3Address,
          chain_id: this.web3ChainId,
          ens_name: ensName || ''
        })
      });

      this.web3Connected = true;

      // Show connected state
      this.showWeb3Connected({
        address: this.web3Address,
        address_masked: this.maskWeb3Address(this.web3Address),
        chain_id: this.web3ChainId,
        chain_name: this.CHAINS[this.web3ChainId]?.name || `Chain ${this.web3ChainId}`,
        ens_name: ensName
      });

      this.showWeb3Alert('Wallet connected successfully!', 'success');
    } catch (error) {
      console.error('Failed to connect wallet:', error);

      // Reset to disconnected state
      await this.initWeb3Step();

      if (error.code === 4001) {
        this.showWeb3Alert('Connection request was rejected.', 'warning');
      } else {
        this.showWeb3Alert('Failed to connect wallet. Please try again.', 'danger');
      }
    }
  }

  // Disconnect Web3 wallet
  async disconnectWeb3Wallet() {
    try {
      await fetch('/api/web3-wallet', { method: 'DELETE' });

      this.web3Connected = false;
      this.web3Address = null;
      this.web3ChainId = null;

      await this.initWeb3Step();
      this.showWeb3Alert('Wallet disconnected.', 'info');
    } catch (error) {
      console.error('Failed to disconnect wallet:', error);
      this.showWeb3Alert('Failed to disconnect wallet.', 'danger');
    }
  }

  // Show Web3 connected state
  showWeb3Connected(data) {
    const statusIcon = document.getElementById('onboardingWeb3StatusIcon');
    const statusText = document.getElementById('onboardingWeb3StatusText');
    const statusDetails = document.getElementById('onboardingWeb3StatusDetails');
    const noWalletDiv = document.getElementById('onboardingWeb3NoWallet');
    const disconnectedDiv = document.getElementById('onboardingWeb3Disconnected');
    const connectedDiv = document.getElementById('onboardingWeb3Connected');

    // Update status
    statusIcon.innerHTML = `
      <svg width="24" height="24" viewBox="0 0 24 24" fill="#28a745">
        <path d="M12,2A10,10 0 0,1 22,12A10,10 0 0,1 12,22A10,10 0 0,1 2,12A10,10 0 0,1 12,2M12,4A8,8 0 0,0 4,12A8,8 0 0,0 12,20A8,8 0 0,0 20,12A8,8 0 0,0 12,4M11,16.5L6.5,12L7.91,10.59L11,13.67L16.59,8.09L18,9.5L11,16.5Z"/>
      </svg>
    `;
    statusText.textContent = 'Wallet Connected';
    statusDetails.textContent = data.address_masked || this.maskWeb3Address(data.address);

    // Update connected info
    const addressEl = document.getElementById('onboardingWeb3Address');
    const ensEl = document.getElementById('onboardingWeb3ENSName');
    const networkEl = document.getElementById('onboardingWeb3Network');

    if (addressEl) addressEl.textContent = data.address_masked || this.maskWeb3Address(data.address);
    if (ensEl) {
      if (data.ens_name) {
        ensEl.textContent = data.ens_name;
        ensEl.classList.remove('d-none');
      } else {
        ensEl.textContent = '';
        ensEl.classList.add('d-none');
      }
    }
    if (networkEl) networkEl.textContent = data.chain_name;

    // Show/hide sections
    if (noWalletDiv) noWalletDiv.classList.add('d-none');
    if (disconnectedDiv) disconnectedDiv.classList.add('d-none');
    if (connectedDiv) connectedDiv.classList.remove('d-none');
  }

  // Mask Web3 address
  maskWeb3Address(address) {
    if (!address || address.length < 10) return address;
    return `${address.slice(0, 6)}...${address.slice(-4)}`;
  }

  // Show Web3 alert
  showWeb3Alert(message, type = 'info') {
    const alertsContainer = document.getElementById('onboardingWeb3Alerts');
    if (!alertsContainer) return;

    const alertId = `web3Alert${Date.now()}`;
    const alertHtml = `
      <div id="${alertId}" class="alert alert-${type} alert-dismissible fade show" role="alert">
        ${message}
        <button type="button" class="btn-close" data-bs-dismiss="alert" aria-label="Close"></button>
      </div>
    `;

    alertsContainer.insertAdjacentHTML('beforeend', alertHtml);

    // Auto-dismiss after 5 seconds
    setTimeout(() => {
      const alertEl = document.getElementById(alertId);
      if (alertEl) alertEl.remove();
    }, 5000);
  }

  // Move to next step
  async nextStep() {
    if (this.currentStep < this.totalSteps - 1) {
      // Step 1: Device Detection - fetch device info if not already done
      if (this.currentStep === 1 && !this.deviceInfo) {
        await this.fetchDeviceInfo();
      }

      // Step 2: Save API keys if leaving API Keys step
      if (this.currentStep === 2) {
        const saved = await this.saveApiKeys();
        // Don't proceed if validation failed
        if (saved === false) {
          return;
        }
        // Load providers for step 3 (system model)
        await this.loadSystemModelProviders();
      }

      // Step 3: Save system model if leaving System Model step
      if (this.currentStep === 3) {
        await this.saveSystemModel();
        // Initialize Web3 step for step 4
        await this.initWeb3Step();
      }

      // Step 4: Web3 wallet step - no save needed, user can skip
      // The wallet state is saved immediately when connected

      // Track completed step locally
      this.completedSteps.add(this.currentStep);

      // Mark current step as completed in backend
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

    // Update modal title dynamically
    const modalTitle = document.getElementById('onboardingModalLabel');
    if (modalTitle && this.stepTitles[this.currentStep]) {
      modalTitle.textContent = this.stepTitles[this.currentStep];
    }

    // Update step indicator
    this.updateStepIndicator();

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

    // Update UI highlights based on current step
    this.updateHighlights();

    // Populate completion summary on last step
    if (this.currentStep === this.totalSteps - 1) {
      this.populateCompletionSummary();
    }
  }

  // Update step indicator dots and connectors
  updateStepIndicator() {
    const stepDots = document.querySelectorAll('#stepIndicator .step-dot');
    const connectors = document.querySelectorAll('#stepIndicator .step-connector');

    stepDots.forEach((dot, index) => {
      dot.classList.remove('active', 'completed');
      dot.setAttribute('aria-selected', 'false');

      if (index === this.currentStep) {
        dot.classList.add('active');
        dot.setAttribute('aria-selected', 'true');
        dot.setAttribute('aria-current', 'step');
      } else if (this.completedSteps.has(index) || this.skippedSteps.has(index) || index < this.currentStep) {
        dot.classList.add('completed');
        dot.removeAttribute('aria-current');
      } else {
        dot.removeAttribute('aria-current');
      }
    });

    // Update connectors
    connectors.forEach((connector, index) => {
      connector.classList.remove('completed');
      if (this.completedSteps.has(index) || this.skippedSteps.has(index) || index < this.currentStep) {
        connector.classList.add('completed');
      }
    });
  }

  // Populate completion summary with gathered info
  populateCompletionSummary() {
    // Device info
    const deviceEl = document.getElementById('completionDeviceInfo');
    if (deviceEl && this.deviceInfo) {
      deviceEl.textContent = `${this.deviceInfo.machine_name || this.deviceInfo.type || 'Detected'}`;
    }

    // API Keys info
    const apiKeysEl = document.getElementById('completionApiKeysInfo');
    if (apiKeysEl) {
      const providers = [];
      if (this.availableProviders.some(p => p.available && p.name === 'openai')) providers.push('OpenAI');
      if (this.availableProviders.some(p => p.available && p.name === 'anthropic')) providers.push('Claude');
      if (this.availableProviders.some(p => p.available && p.name === 'ollama')) providers.push('Ollama');
      apiKeysEl.textContent = providers.length > 0 ? providers.join(', ') : 'Configure in Settings';
    }

    // Profile info
    const profileEl = document.getElementById('completionProfileInfo');
    if (profileEl && this.smartOnboarding && this.smartOnboarding.userProfile) {
      const profile = this.smartOnboarding.userProfile;
      profileEl.textContent = profile.primary_category || 'General';
    }

    // Installed plugins
    const pluginsSummary = document.getElementById('completionPluginsSummary');
    const pluginsList = document.getElementById('completionPluginsList');
    if (pluginsSummary && pluginsList && this.smartOnboarding && this.smartOnboarding.installedPlugins) {
      const plugins = this.smartOnboarding.installedPlugins;
      if (plugins.length > 0) {
        pluginsSummary.classList.remove('d-none');
        pluginsList.innerHTML = plugins.map(p =>
          `<span class="badge bg-secondary">${p}</span>`
        ).join('');
      }
    }
  }

  // Highlight UI elements based on current onboarding step
  updateHighlights() {
    // Remove all existing highlights
    document.querySelectorAll('.onboarding-highlight').forEach(el => {
      el.classList.remove('onboarding-highlight');
    });

    // No highlights needed for the new simplified flow
    // The step indicator provides clear navigation
  }

  // Remove all highlights (called when modal closes)
  removeAllHighlights() {
    document.querySelectorAll('.onboarding-highlight').forEach(el => {
      el.classList.remove('onboarding-highlight');
    });
  }

  // Mark a step as completed in the backend
  async completeStep(stepName) {
    try {
      const response = await fetch('/api/onboarding/step', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json'
        },
        body: JSON.stringify({ step_name: stepName })
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
          'Content-Type': 'application/json'
        }
      });

      console.log(`📊 Skip response: status=${response.status}, ok=${response.ok}`);

      if (!response.ok) {
        throw new Error('Failed to skip onboarding');
      }

      // Remove highlights before closing
      this.removeAllHighlights();

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
          'Content-Type': 'application/json'
        }
      });

      if (!response.ok) {
        throw new Error('Failed to complete onboarding');
      }

      // Remove highlights before closing
      this.removeAllHighlights();

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
          'Content-Type': 'application/json'
        }
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
