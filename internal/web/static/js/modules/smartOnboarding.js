// Smart Onboarding module - AI-powered profile detection and plugin suggestions
export class SmartOnboardingManager {
  constructor() {
    this.detectedApps = [];
    this.userProfile = null;
    this.onboardingConfig = null;
    this.availablePlugins = [];
    this.installedPlugins = new Set();
    this.selectedPlugins = [];
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
    const skipPluginsBtn = document.getElementById('skipPluginsBtn');

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
      confirmProfileBtn.addEventListener('click', () => this.installSelectedPlugins());
    }

    if (editProfileBtn) {
      editProfileBtn.addEventListener('click', () => this.editProfile());
    }

    if (skipPluginsBtn) {
      skipPluginsBtn.addEventListener('click', () => this.skipPlugins());
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

  // Generate onboarding configuration from profile and load plugins
  async generateConfig() {
    try {
      // We don't need the old config endpoint anymore, just fetch plugins
      await this.fetchAvailablePlugins();

      // Show the plugin selection screen
      this.showProfileConfirmation();
    } catch (error) {
      console.error('Error loading plugins:', error);
      this.showError('Failed to load plugins. Please try again.');
      this.showSection('mode-selection');
    }
  }

  // Fetch available plugins from registry
  async fetchAvailablePlugins() {
    const response = await fetch('/api/plugin-registry');
    if (!response.ok) {
      throw new Error('Failed to fetch plugin registry');
    }

    const data = await response.json();
    const allPlugins = data.plugins || [];

    // Separate online (downloadable) and local (installed) plugins
    const onlinePlugins = allPlugins.filter(p => p.github_repo);
    const localPlugins = allPlugins.filter(p => !p.github_repo);

    this.installedPlugins = new Set(localPlugins.map(p => p.name));
    this.availablePlugins = onlinePlugins.filter(p => !this.installedPlugins.has(p.name));
  }

  // Display profile confirmation screen with plugin selection
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

    // Display suggested plugins
    this.displaySuggestedPlugins();
  }

  // Display suggested plugins for selection
  displaySuggestedPlugins() {
    const container = document.getElementById('suggestedPluginsList');
    const loadingEl = document.getElementById('pluginsLoadingState');
    const noPluginsEl = document.getElementById('noPluginsMessage');
    const countEl = document.getElementById('selectedPluginCount');

    if (loadingEl) loadingEl.classList.add('d-none');

    if (!container) return;

    if (this.availablePlugins.length === 0) {
      container.classList.add('d-none');
      if (noPluginsEl) noPluginsEl.classList.remove('d-none');
      if (countEl) countEl.textContent = '0';
      return;
    }

    if (noPluginsEl) noPluginsEl.classList.add('d-none');
    container.classList.remove('d-none');

    // Sort plugins - recommend some based on profile
    const sortedPlugins = this.sortPluginsByRelevance(this.availablePlugins);

    const pluginsHtml = sortedPlugins.map((plugin, index) => {
      const isRecommended = this.isPluginRecommended(plugin);
      return `
        <div class="plugin-item mb-2 p-2" style="border: 1px solid var(--border-color); border-radius: 6px; background: var(--bg-secondary);">
          <div class="form-check">
            <input class="form-check-input plugin-checkbox" type="checkbox"
                   value="${this.escapeHtml(plugin.name)}" id="plugin-${index}" ${isRecommended ? 'checked' : ''}>
            <label class="form-check-label w-100" for="plugin-${index}">
              <div class="d-flex justify-content-between align-items-start">
                <div>
                  <strong>${this.escapeHtml(plugin.name)}</strong>
                  ${isRecommended ? '<span class="badge bg-success ms-1" style="font-size: 0.65rem;">Recommended</span>' : ''}
                  <small class="text-muted d-block">${this.escapeHtml(plugin.description || 'No description')}</small>
                </div>
                <span class="badge" style="background: var(--accent-color); color: white; font-size: 0.65rem;">v${plugin.version || '?'}</span>
              </div>
            </label>
          </div>
        </div>
      `;
    }).join('');

    container.innerHTML = pluginsHtml;

    // Setup change listeners to update count
    container.querySelectorAll('.plugin-checkbox').forEach(cb => {
      cb.addEventListener('change', () => this.updateSelectedCount());
    });

    // Initial count update
    this.updateSelectedCount();
  }

  // Sort plugins by relevance to user profile
  sortPluginsByRelevance(plugins) {
    return [...plugins].sort((a, b) => {
      const aRecommended = this.isPluginRecommended(a) ? 1 : 0;
      const bRecommended = this.isPluginRecommended(b) ? 1 : 0;
      return bRecommended - aRecommended;
    });
  }

  // Check if plugin is recommended based on user profile
  isPluginRecommended(plugin) {
    if (!this.userProfile) return false;

    const category = this.userProfile.primary_category || '';
    const specs = (this.userProfile.specializations || []).map(s => s.toLowerCase());
    const pluginName = (plugin.name || '').toLowerCase();
    const pluginDesc = (plugin.description || '').toLowerCase();

    // Define profile-to-plugin mapping
    const recommendations = {
      'developer': ['git', 'code', 'script', 'api', 'debug', 'test'],
      'devops': ['docker', 'kubernetes', 'aws', 'cloud', 'deploy', 'ci', 'monitor'],
      'data_scientist': ['data', 'python', 'analysis', 'ml', 'chart', 'csv', 'sql'],
      'designer': ['image', 'design', 'color', 'ui', 'figma'],
      'writer': ['write', 'text', 'document', 'markdown', 'note'],
      'project_manager': ['task', 'project', 'calendar', 'team', 'slack']
    };

    const keywords = recommendations[category] || [];

    // Check if plugin matches any keywords
    for (const keyword of keywords) {
      if (pluginName.includes(keyword) || pluginDesc.includes(keyword)) {
        return true;
      }
    }

    // Check specializations
    for (const spec of specs) {
      if (pluginName.includes(spec) || pluginDesc.includes(spec)) {
        return true;
      }
    }

    return false;
  }

  // Update selected plugin count
  updateSelectedCount() {
    const countEl = document.getElementById('selectedPluginCount');
    if (countEl) {
      const checked = document.querySelectorAll('.plugin-checkbox:checked').length;
      countEl.textContent = checked;
    }
  }

  // Install selected plugins
  async installSelectedPlugins() {
    const checkboxes = document.querySelectorAll('.plugin-checkbox:checked');
    this.selectedPlugins = Array.from(checkboxes).map(cb => cb.value);

    if (this.selectedPlugins.length === 0) {
      this.skipPlugins();
      return;
    }

    this.showSection('applying');

    const progressList = document.getElementById('installProgressList');
    const installedList = [];
    const failedList = [];

    for (const pluginName of this.selectedPlugins) {
      // Update progress
      if (progressList) {
        progressList.innerHTML += `
          <div class="d-flex align-items-center mb-2" id="progress-${this.escapeHtml(pluginName)}">
            <div class="spinner-border spinner-border-sm text-primary me-2" role="status"></div>
            <span>${this.escapeHtml(pluginName)}</span>
          </div>
        `;
      }

      try {
        const response = await fetch('/api/plugins/download', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ name: pluginName })
        });

        const result = await response.json();

        if (response.ok && result.success) {
          installedList.push(pluginName);
          // Update progress to success
          const progressEl = document.getElementById(`progress-${pluginName}`);
          if (progressEl) {
            progressEl.innerHTML = `
              <svg width="16" height="16" viewBox="0 0 24 24" fill="#28a745" class="me-2">
                <path d="M21,7L9,19L3.5,13.5L4.91,12.09L9,16.17L19.59,5.59L21,7Z"/>
              </svg>
              <span>${this.escapeHtml(pluginName)}</span>
            `;
          }
        } else {
          throw new Error(result.message || 'Install failed');
        }
      } catch (error) {
        console.error(`Failed to install ${pluginName}:`, error);
        failedList.push(pluginName);
        // Update progress to failed
        const progressEl = document.getElementById(`progress-${pluginName}`);
        if (progressEl) {
          progressEl.innerHTML = `
            <svg width="16" height="16" viewBox="0 0 24 24" fill="#dc3545" class="me-2">
              <path d="M19,6.41L17.59,5L12,10.59L6.41,5L5,6.41L10.59,12L5,17.59L6.41,19L12,13.41L17.59,19L19,17.59L13.41,12L19,6.41Z"/>
            </svg>
            <span class="text-danger">${this.escapeHtml(pluginName)} (failed)</span>
          `;
        }
      }
    }

    // Show success
    this.showPluginSuccess(installedList, failedList);
  }

  // Skip plugin installation
  skipPlugins() {
    this.showSection('success');
    const successMsg = document.getElementById('successMessage');
    const headerEl = document.getElementById('installedListHeader');
    const listEl = document.getElementById('createdAgentsList');

    if (successMsg) successMsg.textContent = 'Profile saved! You can install plugins later.';
    if (headerEl) headerEl.classList.add('d-none');
    if (listEl) listEl.innerHTML = '';
  }

  // Show plugin installation success
  showPluginSuccess(installed, failed) {
    this.showSection('success');

    const successMsg = document.getElementById('successMessage');
    const headerEl = document.getElementById('installedListHeader');
    const listEl = document.getElementById('createdAgentsList');

    if (installed.length > 0) {
      if (successMsg) successMsg.textContent = `${installed.length} plugin${installed.length > 1 ? 's' : ''} installed successfully!`;
      if (headerEl) {
        headerEl.textContent = 'Installed Plugins:';
        headerEl.classList.remove('d-none');
      }
      if (listEl) {
        listEl.innerHTML = installed.map(name =>
          `<li class="mb-1"><strong>${this.escapeHtml(name)}</strong></li>`
        ).join('');
      }
    } else {
      if (successMsg) successMsg.textContent = 'No plugins were installed.';
      if (headerEl) headerEl.classList.add('d-none');
      if (listEl) listEl.innerHTML = '';
    }

    if (failed.length > 0 && listEl) {
      listEl.innerHTML += `<li class="text-danger mt-2">Failed: ${failed.join(', ')}</li>`;
    }

    // Refresh plugins in sidebar if available
    if (typeof window.loadPluginsForSidebar === 'function') {
      window.loadPluginsForSidebar();
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
    this.availablePlugins = [];
    this.installedPlugins = new Set();
    this.selectedPlugins = [];
    this.mode = null;
    this.showSection('mode-selection');
  }
}

// Create a singleton instance
export const smartOnboardingManager = new SmartOnboardingManager();
