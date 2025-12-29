// Smart Onboarding module - AI-powered profile detection and agent auto-configuration
export class SmartOnboardingManager {
  constructor() {
    this.detectedApps = [];
    this.userProfile = null;
    this.onboardingConfig = null;
    this.mode = null; // 'detect' or 'describe'
  }

  // Initialize the smart onboarding step
  async init() {
    this.setupEventListeners();
  }

  // Setup event listeners for smart onboarding UI
  setupEventListeners() {
    // Mode selection buttons
    const detectBtn = document.getElementById('smartOnboardingDetectBtn');
    const describeBtn = document.getElementById('smartOnboardingDescribeBtn');
    const analyzeAppsBtn = document.getElementById('analyzeAppsBtn');
    const submitDescriptionBtn = document.getElementById('submitDescriptionBtn');
    const confirmProfileBtn = document.getElementById('confirmProfileBtn');
    const editProfileBtn = document.getElementById('editProfileBtn');

    if (detectBtn) {
      detectBtn.addEventListener('click', () => this.startDetection());
    }

    if (describeBtn) {
      describeBtn.addEventListener('click', () => this.showDescribeMode());
    }

    if (analyzeAppsBtn) {
      analyzeAppsBtn.addEventListener('click', () => this.continueWithAnalysis());
    }

    if (submitDescriptionBtn) {
      submitDescriptionBtn.addEventListener('click', () => this.submitDescription());
    }

    if (confirmProfileBtn) {
      confirmProfileBtn.addEventListener('click', () => this.confirmProfile());
    }

    if (editProfileBtn) {
      editProfileBtn.addEventListener('click', () => this.editProfile());
    }
  }

  // Start automatic app detection
  async startDetection() {
    this.mode = 'detect';
    this.showSection('detecting');

    try {
      const response = await fetch('/api/onboarding/detect', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' }
      });

      if (!response.ok) {
        throw new Error('Failed to detect apps');
      }

      const data = await response.json();
      this.detectedApps = data.apps || [];

      if (this.detectedApps.length === 0) {
        // No apps detected, show describe mode
        this.showSection('no-apps');
        return;
      }

      // Show detected apps first, then try to infer profile
      this.showSection('apps-detected');
      this.showDetectedAppsPreview();
    } catch (error) {
      console.error('Error detecting apps:', error);
      this.showError('Failed to detect apps. Please try describing yourself instead.');
      this.showSection('mode-selection');
    }
  }

  // Show detected apps preview before analysis
  showDetectedAppsPreview() {
    const container = document.getElementById('detectedAppsPreview');
    if (!container) return;

    const appsHtml = this.detectedApps.slice(0, 15).map(app => `
      <span class="badge bg-secondary me-1 mb-1">${this.escapeHtml(app.name)}</span>
    `).join('');

    const moreCount = this.detectedApps.length - 15;
    const moreHtml = moreCount > 0 ? `<span class="badge bg-light text-dark me-1 mb-1">+${moreCount} more</span>` : '';

    container.innerHTML = appsHtml + moreHtml;

    // Update count
    const countEl = document.getElementById('detectedAppsCount');
    if (countEl) {
      countEl.textContent = this.detectedApps.length;
    }
  }

  // Continue with profile inference after showing apps
  async continueWithAnalysis() {
    await this.inferProfile();
  }

  // Show the describe yourself mode
  showDescribeMode() {
    this.mode = 'describe';
    this.showSection('describe');
  }

  // Show detected apps in the UI
  showDetectedApps() {
    const container = document.getElementById('detectedAppsList');
    if (!container) return;

    const appsHtml = this.detectedApps.slice(0, 10).map(app => `
      <span class="badge bg-secondary me-1 mb-1">${this.escapeHtml(app.name)}</span>
    `).join('');

    const moreCount = this.detectedApps.length - 10;
    const moreHtml = moreCount > 0 ? `<span class="badge bg-light text-dark me-1 mb-1">+${moreCount} more</span>` : '';

    container.innerHTML = appsHtml + moreHtml;
  }

  // Infer user profile from detected apps
  async inferProfile() {
    this.showSection('analyzing');

    try {
      const response = await fetch('/api/onboarding/profile', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ apps: this.detectedApps })
      });

      if (!response.ok) {
        throw new Error('Failed to analyze profile');
      }

      const data = await response.json();
      this.userProfile = data.profile;

      // Generate configuration
      await this.generateConfig();
    } catch (error) {
      console.error('Error inferring profile:', error);
      this.showError('Failed to analyze your profile. Please try describing yourself.');
      this.showSection('describe');
    }
  }

  // Submit user description for profile inference
  async submitDescription() {
    const descriptionInput = document.getElementById('userDescription');
    const description = descriptionInput?.value.trim();

    if (!description) {
      this.showError('Please enter a description of yourself.');
      return;
    }

    this.showSection('analyzing');

    try {
      const response = await fetch('/api/onboarding/describe', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ description })
      });

      if (!response.ok) {
        throw new Error('Failed to analyze description');
      }

      const data = await response.json();
      this.userProfile = data.profile;

      // Generate configuration
      await this.generateConfig();
    } catch (error) {
      console.error('Error analyzing description:', error);
      this.showError('Failed to analyze your description. Please try again.');
      this.showSection('describe');
    }
  }

  // Generate onboarding configuration from profile
  async generateConfig() {
    try {
      const response = await fetch('/api/onboarding/config', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ profile: this.userProfile })
      });

      if (!response.ok) {
        throw new Error('Failed to generate configuration');
      }

      const data = await response.json();
      this.onboardingConfig = data.config;

      // Show the profile confirmation screen
      this.showProfileConfirmation();
    } catch (error) {
      console.error('Error generating config:', error);
      this.showError('Failed to generate configuration. Please try again.');
      this.showSection('mode-selection');
    }
  }

  // Display profile confirmation screen
  showProfileConfirmation() {
    this.showSection('confirmation');

    if (!this.userProfile) return;

    // Display profile summary
    const summaryEl = document.getElementById('profileSummary');
    if (summaryEl) {
      summaryEl.textContent = this.userProfile.summary || 'AI assistant user';
    }

    // Display primary category
    const categoryEl = document.getElementById('profileCategory');
    if (categoryEl) {
      categoryEl.textContent = this.formatCategory(this.userProfile.primary_category);
    }

    // Display specializations
    const specsEl = document.getElementById('profileSpecializations');
    if (specsEl) {
      const specs = this.userProfile.specializations || [];
      if (specs.length > 0) {
        specsEl.innerHTML = specs.map(s =>
          `<span class="badge bg-info me-1">${this.escapeHtml(s)}</span>`
        ).join('');
      } else {
        specsEl.innerHTML = '<span class="text-muted">None detected</span>';
      }
    }

    // Display confidence
    const confidenceEl = document.getElementById('profileConfidence');
    if (confidenceEl) {
      const confidence = Math.round((this.userProfile.confidence || 0) * 100);
      confidenceEl.innerHTML = `
        <div class="progress" style="height: 20px;">
          <div class="progress-bar ${confidence >= 70 ? 'bg-success' : confidence >= 40 ? 'bg-warning' : 'bg-danger'}"
               role="progressbar" style="width: ${confidence}%;"
               aria-valuenow="${confidence}" aria-valuemin="0" aria-valuemax="100">
            ${confidence}%
          </div>
        </div>
      `;
    }

    // Display suggested agents
    this.displaySuggestedAgents();
  }

  // Display suggested agents for confirmation
  displaySuggestedAgents() {
    const container = document.getElementById('suggestedAgentsList');
    if (!container || !this.onboardingConfig) return;

    const agents = this.onboardingConfig.agents || [];

    if (agents.length === 0) {
      container.innerHTML = '<p class="text-muted">No agents suggested. You can create agents manually later.</p>';
      return;
    }

    const agentsHtml = agents.map((agent, index) => `
      <div class="card mb-2">
        <div class="card-body py-2">
          <div class="form-check">
            <input class="form-check-input agent-checkbox" type="checkbox"
                   value="${index}" id="agent-${index}" checked>
            <label class="form-check-label" for="agent-${index}">
              <strong>${this.escapeHtml(agent.name)}</strong>
              <small class="text-muted d-block">${this.escapeHtml(agent.description)}</small>
            </label>
          </div>
        </div>
      </div>
    `).join('');

    container.innerHTML = agentsHtml;
  }

  // Confirm profile and create agents
  async confirmProfile() {
    const checkboxes = document.querySelectorAll('.agent-checkbox:checked');
    const selectedIndices = Array.from(checkboxes).map(cb => parseInt(cb.value));

    // Filter agents to only selected ones
    const selectedAgents = selectedIndices.map(i => this.onboardingConfig.agents[i]);
    const configToApply = {
      ...this.onboardingConfig,
      agents: selectedAgents
    };

    this.showSection('applying');

    try {
      const response = await fetch('/api/onboarding/apply-config', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ config: configToApply })
      });

      if (!response.ok) {
        throw new Error('Failed to apply configuration');
      }

      const data = await response.json();

      // Show success
      this.showSuccess(data.agents_created || []);
    } catch (error) {
      console.error('Error applying config:', error);
      this.showError('Failed to create agents. Please try again or create agents manually.');
      this.showSection('confirmation');
    }
  }

  // Edit profile - go back to describe mode
  editProfile() {
    this.showSection('describe');
    // Pre-fill with profile summary if available
    const descriptionInput = document.getElementById('userDescription');
    if (descriptionInput && this.userProfile?.summary) {
      descriptionInput.value = this.userProfile.summary;
    }
  }

  // Show success message
  showSuccess(createdAgents) {
    this.showSection('success');

    const agentsListEl = document.getElementById('createdAgentsList');
    if (agentsListEl && createdAgents.length > 0) {
      agentsListEl.innerHTML = createdAgents.map(name =>
        `<li class="mb-1"><strong>${this.escapeHtml(name)}</strong></li>`
      ).join('');
    } else if (agentsListEl) {
      agentsListEl.innerHTML = '<li class="text-muted">No new agents created (may already exist)</li>';
    }
  }

  // Show a specific section
  showSection(sectionId) {
    const sections = [
      'mode-selection', 'detecting', 'no-apps', 'apps-detected', 'describe',
      'analyzing', 'confirmation', 'applying', 'success'
    ];

    sections.forEach(id => {
      const el = document.getElementById(`smart-onboarding-${id}`);
      if (el) {
        if (id === sectionId) {
          el.classList.remove('d-none');
        } else {
          el.classList.add('d-none');
        }
      }
    });
  }

  // Show error message
  showError(message) {
    const errorEl = document.getElementById('smartOnboardingError');
    if (errorEl) {
      errorEl.textContent = message;
      errorEl.classList.remove('d-none');
      setTimeout(() => errorEl.classList.add('d-none'), 5000);
    }
  }

  // Format category for display
  formatCategory(category) {
    const categories = {
      'developer': 'Software Developer',
      'devops': 'DevOps Engineer',
      'designer': 'Designer',
      'data_scientist': 'Data Scientist',
      'writer': 'Writer / Content Creator',
      'project_manager': 'Project Manager',
      'general': 'General User'
    };
    return categories[category] || category || 'General User';
  }

  // Escape HTML to prevent XSS
  escapeHtml(text) {
    const div = document.createElement('div');
    div.textContent = text || '';
    return div.innerHTML;
  }

  // Reset the smart onboarding state
  reset() {
    this.detectedApps = [];
    this.userProfile = null;
    this.onboardingConfig = null;
    this.mode = null;
    this.showSection('mode-selection');
  }
}

// Create a singleton instance
export const smartOnboardingManager = new SmartOnboardingManager();
