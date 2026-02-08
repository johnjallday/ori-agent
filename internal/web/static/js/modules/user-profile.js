// User Profile module - displays read-only profile summary on settings page

const userProfileManager = {
  profile: null,

  // Initialize the user profile section
  async init() {
    await this.loadProfile();
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

  // Render the profile data (read-only summary)
  renderProfile() {
    if (!this.profile) return;

    // Category badge
    const categoryBadge = document.getElementById('profileCategoryBadge');
    if (categoryBadge) {
      categoryBadge.textContent = this.formatCategory(this.profile.primary_category);
    }

    // Summary
    const summaryText = document.getElementById('profileSummaryText');
    if (summaryText) {
      summaryText.textContent = this.profile.summary || 'No summary available';
    }
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
  }
};

// Initialize on page load
document.addEventListener('DOMContentLoaded', function() {
  // Only initialize on settings page
  if (document.getElementById('userProfileContent')) {
    userProfileManager.init();
  }
});
