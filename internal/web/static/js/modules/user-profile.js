// User Profile module - displays and manages user profile on settings page

const userProfileManager = {
  profile: null,

  // Initialize the user profile section
  async init() {
    await this.loadProfile();
    this.setupEventListeners();
  },

  // Load user profile from API
  async loadProfile() {
    const contentEl = document.getElementById('userProfileContent');
    const emptyEl = document.getElementById('userProfileEmpty');
    const displayEl = document.getElementById('userProfileDisplay');

    if (!contentEl) return;

    try {
      const response = await fetch('/api/onboarding/user-profile');
      if (!response.ok) {
        throw new Error('Failed to load profile');
      }

      const data = await response.json();
      this.profile = data.profile;

      // Hide loading state
      contentEl.classList.add('d-none');

      if (this.profile) {
        this.renderProfile();
        emptyEl.classList.add('d-none');
        displayEl.classList.remove('d-none');
      } else {
        emptyEl.classList.remove('d-none');
        displayEl.classList.add('d-none');
      }
    } catch (error) {
      console.error('Error loading profile:', error);
      contentEl.innerHTML = `
        <div class="alert alert-warning mb-0">
          <small>Could not load profile. <a href="#" onclick="userProfileManager.loadProfile(); return false;">Retry</a></small>
        </div>
      `;
    }
  },

  // Render the profile data
  renderProfile() {
    if (!this.profile) return;

    // Category badge
    const categoryBadge = document.getElementById('profileCategoryBadge');
    if (categoryBadge) {
      categoryBadge.textContent = this.formatCategory(this.profile.primary_category);
    }

    // Confidence
    const confidenceValue = document.getElementById('profileConfidenceValue');
    if (confidenceValue) {
      const confidence = Math.round((this.profile.confidence || 0) * 100);
      confidenceValue.textContent = `${confidence}%`;
    }

    // Summary
    const summaryText = document.getElementById('profileSummaryText');
    if (summaryText) {
      summaryText.textContent = this.profile.summary || 'No summary available';
    }

    // Specializations
    const specsSection = document.getElementById('profileSpecializationsSection');
    const specsList = document.getElementById('profileSpecializationsList');
    if (specsList && specsSection) {
      const specs = this.profile.specializations || [];
      if (specs.length > 0) {
        specsList.innerHTML = specs.map(s =>
          `<span class="badge" style="background: var(--bg-secondary); color: var(--text-primary);">${this.escapeHtml(s)}</span>`
        ).join('');
        specsSection.classList.remove('d-none');
      } else {
        specsSection.classList.add('d-none');
      }
    }

    // Detected Apps
    const appsSection = document.getElementById('profileAppsSection');
    const appsList = document.getElementById('profileAppsList');
    if (appsList && appsSection) {
      const apps = this.profile.detected_apps || [];
      if (apps.length > 0) {
        const displayApps = apps.slice(0, 10);
        const moreCount = apps.length - 10;
        appsList.innerHTML = displayApps.map(a =>
          `<span class="badge" style="background: var(--bg-tertiary); color: var(--text-secondary); font-weight: normal; font-size: 0.75rem;">${this.escapeHtml(a)}</span>`
        ).join('');
        if (moreCount > 0) {
          appsList.innerHTML += `<span class="badge" style="background: var(--bg-tertiary); color: var(--text-secondary); font-weight: normal; font-size: 0.75rem;">+${moreCount} more</span>`;
        }
        appsSection.classList.remove('d-none');
      } else {
        appsSection.classList.add('d-none');
      }
    }

    // Inferred date
    const inferredAt = document.getElementById('profileInferredAt');
    if (inferredAt && this.profile.inferred_at) {
      const date = new Date(this.profile.inferred_at);
      inferredAt.textContent = `Profile created ${this.formatRelativeTime(date)}`;
    }
  },

  // Setup event listeners
  setupEventListeners() {
    const detectBtn = document.getElementById('detectProfileBtn');
    const refreshBtn = document.getElementById('refreshProfileBtn');

    if (detectBtn) {
      detectBtn.addEventListener('click', () => this.detectProfile());
    }

    if (refreshBtn) {
      refreshBtn.addEventListener('click', () => this.detectProfile());
    }
  },

  // Detect/re-detect profile
  async detectProfile() {
    const detectBtn = document.getElementById('detectProfileBtn');
    const refreshBtn = document.getElementById('refreshProfileBtn');
    const btn = detectBtn || refreshBtn;

    if (btn) {
      btn.disabled = true;
      btn.innerHTML = `
        <span class="spinner-border spinner-border-sm me-2" role="status"></span>
        Detecting...
      `;
    }

    try {
      // Step 1: Detect apps
      const detectResponse = await fetch('/api/onboarding/detect', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' }
      });

      if (!detectResponse.ok) {
        throw new Error('Failed to detect apps');
      }

      const detectData = await detectResponse.json();
      const apps = detectData.apps || [];

      if (apps.length === 0) {
        this.showAlert('No apps detected. Try describing yourself in the onboarding flow.', 'warning');
        return;
      }

      // Step 2: Infer profile
      if (btn) {
        btn.innerHTML = `
          <span class="spinner-border spinner-border-sm me-2" role="status"></span>
          Analyzing...
        `;
      }

      const profileResponse = await fetch('/api/onboarding/profile', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ apps })
      });

      if (!profileResponse.ok) {
        throw new Error('Failed to analyze profile');
      }

      const profileData = await profileResponse.json();

      // Step 3: Generate and apply config (to save profile)
      const configResponse = await fetch('/api/onboarding/config', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ profile: profileData.profile })
      });

      if (!configResponse.ok) {
        throw new Error('Failed to generate config');
      }

      const configData = await configResponse.json();

      // Step 4: Apply config (this saves the profile)
      const applyResponse = await fetch('/api/onboarding/apply-config', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ config: { ...configData.config, agents: [] } }) // Don't create agents, just save profile
      });

      if (!applyResponse.ok) {
        throw new Error('Failed to save profile');
      }

      // Reload profile
      await this.loadProfile();
      this.showAlert('Profile updated successfully!', 'success');

    } catch (error) {
      console.error('Error detecting profile:', error);
      this.showAlert('Failed to detect profile: ' + error.message, 'danger');
    } finally {
      if (btn) {
        btn.disabled = false;
        if (btn.id === 'detectProfileBtn') {
          btn.innerHTML = `
            <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" class="me-2">
              <path d="M9.5,3A6.5,6.5 0 0,1 16,9.5C16,11.11 15.41,12.59 14.44,13.73L14.71,14H15.5L20.5,19L19,20.5L14,15.5V14.71L13.73,14.44C12.59,15.41 11.11,16 9.5,16A6.5,6.5 0 0,1 3,9.5A6.5,6.5 0 0,1 9.5,3M9.5,5C7,5 5,7 5,9.5C5,12 7,14 9.5,14C12,14 14,12 14,9.5C14,7 12,5 9.5,5Z"/>
            </svg>
            Detect My Profile
          `;
        } else {
          btn.innerHTML = `
            <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" class="me-2">
              <path d="M17.65,6.35C16.2,4.9 14.21,4 12,4A8,8 0 0,0 4,12A8,8 0 0,0 12,20C15.73,20 18.84,17.45 19.73,14H17.65C16.83,16.33 14.61,18 12,18A6,6 0 0,1 6,12A6,6 0 0,1 12,6C13.66,6 15.14,6.69 16.22,7.78L13,11H20V4L17.65,6.35Z"/>
            </svg>
            Re-detect Profile
          `;
        }
      }
    }
  },

  // Show alert message
  showAlert(message, type) {
    const container = document.getElementById('userProfileContent');
    if (!container) return;

    // Remove existing alerts
    container.querySelectorAll('.alert').forEach(a => a.remove());

    const alert = document.createElement('div');
    alert.className = `alert alert-${type} alert-dismissible fade show mt-3`;
    alert.innerHTML = `
      ${message}
      <button type="button" class="btn-close" data-bs-dismiss="alert"></button>
    `;

    // Insert after the profile display
    const displayEl = document.getElementById('userProfileDisplay');
    if (displayEl && !displayEl.classList.contains('d-none')) {
      displayEl.appendChild(alert);
    } else {
      container.parentNode.insertBefore(alert, container.nextSibling);
    }

    // Auto-dismiss after 5 seconds
    setTimeout(() => {
      if (alert.parentNode) {
        alert.remove();
      }
    }, 5000);
  },

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
  },

  // Format relative time
  formatRelativeTime(date) {
    const now = new Date();
    const diffMs = now - date;
    const diffDays = Math.floor(diffMs / (1000 * 60 * 60 * 24));

    if (diffDays === 0) {
      const diffHours = Math.floor(diffMs / (1000 * 60 * 60));
      if (diffHours === 0) {
        const diffMins = Math.floor(diffMs / (1000 * 60));
        if (diffMins < 1) return 'just now';
        return `${diffMins} minute${diffMins > 1 ? 's' : ''} ago`;
      }
      return `${diffHours} hour${diffHours > 1 ? 's' : ''} ago`;
    } else if (diffDays === 1) {
      return 'yesterday';
    } else if (diffDays < 7) {
      return `${diffDays} days ago`;
    } else {
      return date.toLocaleDateString();
    }
  },

  // Escape HTML to prevent XSS
  escapeHtml(text) {
    const div = document.createElement('div');
    div.textContent = text || '';
    return div.innerHTML;
  }
};

// Initialize on page load
document.addEventListener('DOMContentLoaded', function() {
  // Only initialize on settings page
  if (document.getElementById('userProfileContent')) {
    userProfileManager.init();
  }
});
