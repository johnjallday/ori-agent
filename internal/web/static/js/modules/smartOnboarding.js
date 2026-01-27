// Smart Onboarding module - AI-powered profile detection and plugin suggestions
export class SmartOnboardingManager {
  constructor() {
    this.detectedApps = [];
    this.userProfile = null;
    this.onboardingConfig = null;
    this.availablePlugins = [];
    this.installedPlugins = new Set();
    this.selectedPlugins = [];
    this.addedMarketplaces = [];
    this.mode = null; // 'detect' or 'describe'
    this.aiRecommendations = []; // AI-generated plugin recommendations
    this.aiRecommendationsLoaded = false;
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

    // Marketplace buttons
    const testMarketplaceBtn = document.getElementById('testOnboardingMarketplaceBtn');
    const addMarketplaceBtn = document.getElementById('addOnboardingMarketplaceBtn');
    const marketplaceBackBtn = document.getElementById('marketplaceBackBtn');
    const marketplaceContinueBtn = document.getElementById('marketplaceContinueBtn');
    const marketplaceSkipBtn = document.getElementById('marketplaceSkipBtn');

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

    // Marketplace event listeners
    if (testMarketplaceBtn) {
      testMarketplaceBtn.addEventListener('click', () => this.testMarketplace());
    }

    if (addMarketplaceBtn) {
      addMarketplaceBtn.addEventListener('click', () => this.addMarketplace());
    }

    if (marketplaceBackBtn) {
      marketplaceBackBtn.addEventListener('click', () => this.marketplaceBack());
    }

    if (marketplaceContinueBtn) {
      marketplaceContinueBtn.addEventListener('click', () => this.continueFromMarketplace());
    }

    if (marketplaceSkipBtn) {
      marketplaceSkipBtn.addEventListener('click', () => this.skipMarketplace());
    }

    // Quick add marketplace buttons
    const addMusicMarketplaceBtn = document.getElementById('addMusicMarketplaceBtn');
    if (addMusicMarketplaceBtn) {
      addMusicMarketplaceBtn.addEventListener('click', () => this.quickAddMarketplace(
        'Ori Music Plugins',
        'https://gitlab.com/johnjallday/ori-music-plugin-registry'
      ));
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

  // Generate onboarding configuration from profile and show marketplace step
  async generateConfig() {
    try {
      // Show the marketplace step first (before plugin selection)
      this.showMarketplaceStep();
    } catch (error) {
      console.error('Error in generateConfig:', error);
      this.showError('Something went wrong. Please try again.');
      this.showSection('mode-selection');
    }
  }

  // Show the marketplace step
  showMarketplaceStep() {
    this.showSection('marketplace');

    // Display profile info in marketplace step
    if (this.userProfile) {
      const categoryEl = document.getElementById('marketplaceProfileCategory');
      const summaryEl = document.getElementById('marketplaceProfileSummary');

      if (categoryEl) {
        categoryEl.textContent = this.formatCategory(this.userProfile.primary_category);
      }
      if (summaryEl) {
        summaryEl.textContent = this.userProfile.summary || 'AI assistant user';
      }
    }

    // Reset marketplace form
    const nameInput = document.getElementById('onboardingMarketplaceName');
    const sourceInput = document.getElementById('onboardingMarketplaceSource');
    const resultEl = document.getElementById('onboardingMarketplaceResult');
    const addBtn = document.getElementById('addOnboardingMarketplaceBtn');

    if (nameInput) nameInput.value = '';
    if (sourceInput) sourceInput.value = '';
    if (resultEl) resultEl.classList.add('d-none');
    if (addBtn) addBtn.disabled = true;
  }

  // Test marketplace connection
  async testMarketplace() {
    const nameInput = document.getElementById('onboardingMarketplaceName');
    const sourceInput = document.getElementById('onboardingMarketplaceSource');
    const resultEl = document.getElementById('onboardingMarketplaceResult');
    const addBtn = document.getElementById('addOnboardingMarketplaceBtn');
    const testBtn = document.getElementById('testOnboardingMarketplaceBtn');

    const name = nameInput?.value.trim();
    const source = sourceInput?.value.trim();

    if (!name || !source) {
      if (resultEl) {
        resultEl.innerHTML = '<div class="alert alert-warning py-2">Please enter both name and source.</div>';
        resultEl.classList.remove('d-none');
      }
      return;
    }

    // Show loading state
    if (testBtn) {
      testBtn.disabled = true;
      testBtn.innerHTML = '<span class="spinner-border spinner-border-sm me-1"></span>Testing...';
    }

    try {
      const response = await fetch('/api/marketplaces/test', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name, source })
      });

      const data = await response.json();

      if (response.ok && data.valid) {
        if (resultEl) {
          resultEl.innerHTML = `
            <div class="alert alert-success py-2">
              <strong>Connection successful!</strong> Found ${data.plugin_count || 0} plugins.
            </div>
          `;
          resultEl.classList.remove('d-none');
        }
        if (addBtn) addBtn.disabled = false;
      } else {
        if (resultEl) {
          resultEl.innerHTML = `
            <div class="alert alert-danger py-2">
              ${this.escapeHtml(data.error || 'Failed to connect to marketplace.')}
            </div>
          `;
          resultEl.classList.remove('d-none');
        }
        if (addBtn) addBtn.disabled = true;
      }
    } catch (error) {
      console.error('Error testing marketplace:', error);
      if (resultEl) {
        resultEl.innerHTML = '<div class="alert alert-danger py-2">Failed to test connection.</div>';
        resultEl.classList.remove('d-none');
      }
      if (addBtn) addBtn.disabled = true;
    } finally {
      if (testBtn) {
        testBtn.disabled = false;
        testBtn.innerHTML = 'Test Connection';
      }
    }
  }

  // Add marketplace
  async addMarketplace() {
    const nameInput = document.getElementById('onboardingMarketplaceName');
    const sourceInput = document.getElementById('onboardingMarketplaceSource');
    const resultEl = document.getElementById('onboardingMarketplaceResult');
    const addBtn = document.getElementById('addOnboardingMarketplaceBtn');

    const name = nameInput?.value.trim();
    const source = sourceInput?.value.trim();

    if (!name || !source) return;

    if (addBtn) {
      addBtn.disabled = true;
      addBtn.innerHTML = '<span class="spinner-border spinner-border-sm me-1"></span>Adding...';
    }

    try {
      const response = await fetch('/api/marketplaces', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name, source })
      });

      const data = await response.json();

      if (response.ok) {
        // Add to local list
        this.addedMarketplaces.push({ name, source });

        // Update UI
        this.updateAddedMarketplacesList();

        // Clear form
        if (nameInput) nameInput.value = '';
        if (sourceInput) sourceInput.value = '';
        if (resultEl) {
          resultEl.innerHTML = '<div class="alert alert-success py-2">Marketplace added successfully!</div>';
          resultEl.classList.remove('d-none');
          setTimeout(() => resultEl.classList.add('d-none'), 3000);
        }
      } else {
        if (resultEl) {
          resultEl.innerHTML = `<div class="alert alert-danger py-2">${this.escapeHtml(data.error || 'Failed to add marketplace.')}</div>`;
          resultEl.classList.remove('d-none');
        }
      }
    } catch (error) {
      console.error('Error adding marketplace:', error);
      if (resultEl) {
        resultEl.innerHTML = '<div class="alert alert-danger py-2">Failed to add marketplace.</div>';
        resultEl.classList.remove('d-none');
      }
    } finally {
      if (addBtn) {
        addBtn.disabled = true;
        addBtn.innerHTML = 'Add Marketplace';
      }
    }
  }

  // Quick add a predefined marketplace
  async quickAddMarketplace(name, source) {
    const resultEl = document.getElementById('onboardingMarketplaceResult');
    const btn = document.getElementById('addMusicMarketplaceBtn');

    // Check if already added
    if (this.addedMarketplaces.some(mp => mp.source === source)) {
      if (resultEl) {
        resultEl.innerHTML = '<div class="alert alert-info py-2">This marketplace is already added.</div>';
        resultEl.classList.remove('d-none');
        setTimeout(() => resultEl.classList.add('d-none'), 3000);
      }
      return;
    }

    // Show loading state
    if (btn) {
      btn.disabled = true;
      btn.innerHTML = `
        <div class="d-flex align-items-center">
          <div class="me-3" style="background: rgba(255,255,255,0.2); border-radius: 10px; padding: 10px;">
            <div class="spinner-border text-light" style="width: 28px; height: 28px;" role="status"></div>
          </div>
          <div style="flex: 1;">
            <strong style="font-size: 1.1rem;">Adding Marketplace...</strong>
            <div style="font-size: 0.85rem; opacity: 0.9;">Please wait</div>
          </div>
        </div>
      `;
    }

    try {
      const response = await fetch('/api/marketplaces', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name, source })
      });

      const data = await response.json();

      if (response.ok) {
        // Add to local list
        this.addedMarketplaces.push({ name, source });

        // Update UI
        this.updateAddedMarketplacesList();

        if (resultEl) {
          resultEl.innerHTML = `<div class="alert alert-success py-2">${this.escapeHtml(name)} added successfully!</div>`;
          resultEl.classList.remove('d-none');
          setTimeout(() => resultEl.classList.add('d-none'), 3000);
        }

        // Update button to show added state
        if (btn) {
          btn.disabled = true;
          btn.style.background = 'linear-gradient(135deg, #28a745 0%, #20c997 100%)';
          btn.innerHTML = `
            <div class="d-flex align-items-center">
              <div class="me-3" style="background: rgba(255,255,255,0.2); border-radius: 10px; padding: 10px;">
                <svg width="28" height="28" viewBox="0 0 24 24" fill="white">
                  <path d="M21,7L9,19L3.5,13.5L4.91,12.09L9,16.17L19.59,5.59L21,7Z"/>
                </svg>
              </div>
              <div style="flex: 1;">
                <strong style="font-size: 1.1rem;">Ori Music Plugins</strong>
                <div style="font-size: 0.85rem; opacity: 0.9;">Added successfully!</div>
              </div>
            </div>
          `;
        }
      } else {
        if (resultEl) {
          resultEl.innerHTML = `<div class="alert alert-danger py-2">${this.escapeHtml(data.error || 'Failed to add marketplace.')}</div>`;
          resultEl.classList.remove('d-none');
        }
        if (btn) {
          btn.disabled = false;
          this.resetMusicMarketplaceBtn(btn);
        }
      }
    } catch (error) {
      console.error('Error adding marketplace:', error);
      if (resultEl) {
        resultEl.innerHTML = '<div class="alert alert-danger py-2">Failed to add marketplace.</div>';
        resultEl.classList.remove('d-none');
      }
      if (btn) {
        btn.disabled = false;
        this.resetMusicMarketplaceBtn(btn);
      }
    }
  }

  // Reset the music marketplace button to its original state
  resetMusicMarketplaceBtn(btn) {
    btn.style.background = 'linear-gradient(135deg, #667eea 0%, #764ba2 100%)';
    btn.innerHTML = `
      <div class="d-flex align-items-center">
        <div class="me-3" style="background: rgba(255,255,255,0.2); border-radius: 10px; padding: 10px;">
          <svg width="28" height="28" viewBox="0 0 24 24" fill="white">
            <path d="M21,3V15.5A3.5,3.5 0 0,1 17.5,19A3.5,3.5 0 0,1 14,15.5A3.5,3.5 0 0,1 17.5,12C18.04,12 18.55,12.12 19,12.34V6.47L9,8.6V17.5A3.5,3.5 0 0,1 5.5,21A3.5,3.5 0 0,1 2,17.5A3.5,3.5 0 0,1 5.5,14C6.04,14 6.55,14.12 7,14.34V6L21,3Z"/>
          </svg>
        </div>
        <div style="flex: 1;">
          <strong style="font-size: 1.1rem;">Ori Music Plugins</strong>
          <div style="font-size: 0.85rem; opacity: 0.9;">Audio production tools, DAW integration, MIDI utilities</div>
        </div>
        <svg width="20" height="20" viewBox="0 0 24 24" fill="white" style="opacity: 0.7;">
          <path d="M19,13H13V19H11V13H5V11H11V5H13V11H19V13Z"/>
        </svg>
      </div>
    `;
  }

  // Update the list of added marketplaces
  updateAddedMarketplacesList() {
    const container = document.getElementById('onboardingAddedMarketplaces');
    const listEl = document.getElementById('onboardingMarketplaceList');

    if (!container || !listEl) return;

    if (this.addedMarketplaces.length === 0) {
      container.classList.add('d-none');
      return;
    }

    container.classList.remove('d-none');
    listEl.innerHTML = this.addedMarketplaces.map(mp => `
      <div class="badge bg-success me-1 mb-1">${this.escapeHtml(mp.name)}</div>
    `).join('');
  }

  // Go back from marketplace step
  marketplaceBack() {
    if (this.mode === 'detect') {
      this.showSection('apps-detected');
    } else {
      this.showSection('describe');
    }
  }

  // Continue from marketplace to plugin selection
  async continueFromMarketplace() {
    try {
      // Now fetch plugins (including from any newly added marketplaces)
      await this.fetchAvailablePlugins();

      // Show the plugin selection screen
      this.showProfileConfirmation();
    } catch (error) {
      console.error('Error loading plugins:', error);
      this.showError('Failed to load plugins. Please try again.');
    }
  }

  // Skip marketplace step and go directly to plugin selection
  async skipMarketplace() {
    try {
      // Fetch plugins from default marketplace only
      await this.fetchAvailablePlugins();

      // Show the plugin selection screen
      this.showProfileConfirmation();
    } catch (error) {
      console.error('Error loading plugins:', error);
      this.showError('Failed to load plugins. Please try again.');
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

  // Fetch AI-generated plugin recommendations
  async fetchAIRecommendations() {
    if (!this.userProfile || this.availablePlugins.length === 0) {
      this.aiRecommendations = [];
      this.aiRecommendationsLoaded = true;
      return;
    }

    try {
      // Build plugin info for the request
      const pluginInfos = this.availablePlugins.map(p => ({
        name: p.name,
        description: p.description || '',
        tags: p.tags || []
      }));

      const response = await fetch('/api/onboarding/recommend-plugins', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          profile: this.userProfile,
          plugins: pluginInfos,
          max_count: 5
        })
      });

      if (!response.ok) {
        throw new Error('Failed to get AI recommendations');
      }

      const data = await response.json();
      this.aiRecommendations = data.recommendations || [];
      this.aiRecommendationsLoaded = true;
    } catch (error) {
      console.error('Error fetching AI recommendations:', error);
      // Fall back to keyword-based recommendations
      this.aiRecommendations = [];
      this.aiRecommendationsLoaded = true;
    }
  }

  // Get AI recommendation for a specific plugin
  getAIRecommendation(pluginName) {
    return this.aiRecommendations.find(rec => rec.name === pluginName);
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
  async displaySuggestedPlugins() {
    const container = document.getElementById('suggestedPluginsList');
    const loadingEl = document.getElementById('pluginsLoadingState');
    const noPluginsEl = document.getElementById('noPluginsMessage');
    const countEl = document.getElementById('selectedPluginCount');

    if (!container) return;

    if (this.availablePlugins.length === 0) {
      if (loadingEl) loadingEl.classList.add('d-none');
      container.classList.add('d-none');
      if (noPluginsEl) noPluginsEl.classList.remove('d-none');
      if (countEl) countEl.textContent = '0';
      return;
    }

    if (noPluginsEl) noPluginsEl.classList.add('d-none');
    container.classList.remove('d-none');

    // Show loading state while fetching AI recommendations
    if (loadingEl) {
      loadingEl.classList.remove('d-none');
      loadingEl.innerHTML = `
        <div class="text-center py-3">
          <div class="spinner-border spinner-border-sm text-primary me-2" role="status"></div>
          <span class="text-muted">Generating personalized recommendations...</span>
        </div>
      `;
    }

    // Fetch AI recommendations
    await this.fetchAIRecommendations();

    if (loadingEl) loadingEl.classList.add('d-none');

    // Separate recommended and other plugins
    const recommendedPluginNames = new Set(this.aiRecommendations.map(r => r.name));
    const recommendedPlugins = this.availablePlugins.filter(p => recommendedPluginNames.has(p.name));
    const otherPlugins = this.availablePlugins.filter(p => !recommendedPluginNames.has(p.name));

    // Sort recommended plugins by relevance level
    const relevanceOrder = { high: 0, medium: 1, low: 2 };
    recommendedPlugins.sort((a, b) => {
      const recA = this.getAIRecommendation(a.name);
      const recB = this.getAIRecommendation(b.name);
      return (relevanceOrder[recA?.relevance] || 2) - (relevanceOrder[recB?.relevance] || 2);
    });

    let pluginsHtml = '';

    // Recommended plugins section
    if (recommendedPlugins.length > 0) {
      pluginsHtml += `
        <div class="mb-3">
          <h6 class="text-muted mb-2" style="font-size: 0.85rem;">
            <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" class="me-1" style="vertical-align: -3px;">
              <path d="M12,17.27L18.18,21L16.54,13.97L22,9.24L14.81,8.62L12,2L9.19,8.62L2,9.24L7.45,13.97L5.82,21L12,17.27Z"/>
            </svg>
            Recommended for You
          </h6>
          ${recommendedPlugins.map((plugin, index) => this.renderPluginItem(plugin, index, true)).join('')}
        </div>
      `;
    }

    // Other available plugins section
    if (otherPlugins.length > 0) {
      pluginsHtml += `
        <div class="mb-2">
          <h6 class="text-muted mb-2" style="font-size: 0.85rem;">Other Available Plugins</h6>
          ${otherPlugins.map((plugin, index) => this.renderPluginItem(plugin, recommendedPlugins.length + index, false)).join('')}
        </div>
      `;
    }

    container.innerHTML = pluginsHtml;

    // Setup change listeners to update count
    container.querySelectorAll('.plugin-checkbox').forEach(cb => {
      cb.addEventListener('change', () => this.updateSelectedCount());
    });

    // Initial count update
    this.updateSelectedCount();
  }

  // Render a single plugin item
  renderPluginItem(plugin, index, isRecommended) {
    const aiRec = this.getAIRecommendation(plugin.name);
    const displayName = plugin.metadata?.name || stripVersionSuffix(plugin.name || '');
    const relevanceBadgeColor = aiRec?.relevance === 'high' ? 'bg-success' :
      aiRec?.relevance === 'medium' ? 'bg-info' : 'bg-secondary';

    let reasonHtml = '';
    if (aiRec?.reason) {
      reasonHtml = `<div class="mt-1 small" style="color: var(--text-secondary); font-style: italic;">"${this.escapeHtml(aiRec.reason)}"</div>`;
    }
    if (aiRec?.use_case) {
      reasonHtml += `<div class="mt-1 small text-muted"><strong>Use case:</strong> ${this.escapeHtml(aiRec.use_case)}</div>`;
    }

    return `
      <div class="plugin-item mb-2 p-2" style="border: 1px solid var(--border-color); border-radius: 6px; background: var(--bg-secondary);">
        <div class="form-check">
          <input class="form-check-input plugin-checkbox" type="checkbox"
                 value="${this.escapeHtml(plugin.name)}" id="plugin-${index}" ${isRecommended ? 'checked' : ''}>
          <label class="form-check-label w-100" for="plugin-${index}">
            <div class="d-flex justify-content-between align-items-start">
              <div style="flex: 1;">
                <strong>${this.escapeHtml(displayName)}</strong>
                ${isRecommended && aiRec ? `<span class="badge ${relevanceBadgeColor} ms-1" style="font-size: 0.65rem;">${aiRec.relevance === 'high' ? 'Highly Recommended' : 'Recommended'}</span>` : ''}
                <small class="text-muted d-block">${this.escapeHtml(plugin.description || 'No description')}</small>
                ${reasonHtml}
              </div>
              <span class="badge ms-2" style="background: var(--accent-color); color: white; font-size: 0.65rem; white-space: nowrap;">v${plugin.version || '?'}</span>
            </div>
          </label>
        </div>
      </div>
    `;
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
      listEl.innerHTML += `<li class="text-danger mt-2">Failed: ${failed.map(name => this.escapeHtml(name)).join(', ')}</li>`;
    }

    // Refresh plugins in sidebar if available
    if (typeof window.loadPluginsForSidebar === 'function') {
      window.loadPluginsForSidebar();
    }
  }

  // Edit profile - go back to marketplace step
  editProfile() {
    this.showMarketplaceStep();
  }

  // Show a specific section
  showSection(sectionId) {
    const sections = [
      'mode-selection', 'detecting', 'no-apps', 'apps-detected', 'describe',
      'analyzing', 'marketplace', 'confirmation', 'applying', 'success'
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
    this.addedMarketplaces = [];
    this.mode = null;
    this.aiRecommendations = [];
    this.aiRecommendationsLoaded = false;
    this.showSection('mode-selection');
  }
}

// Create a singleton instance
export const smartOnboardingManager = new SmartOnboardingManager();
