const userProfileManager = {
  profile: null,

  async init() {
    this.form = document.getElementById('userProfileForm');
    this.loading = document.getElementById('userProfileContent');
    if (!this.form) return;

    this.form.addEventListener('submit', (event) => {
      event.preventDefault();
      this.saveProfile();
    });
    await this.loadProfile();
  },

  async loadProfile() {
    try {
      const response = await fetch('/api/user/profile');
      if (!response.ok) throw new Error('Failed to load profile');
      const data = await response.json();
      this.profile = data.profile || { preferences: {} };
      this.populateForm(this.profile);
      if (this.loading) this.loading.classList.add('d-none');
      this.form.classList.remove('d-none');
    } catch (error) {
      console.error('Error loading profile:', error);
      if (this.loading) {
        this.loading.innerHTML = `
          <div class="alert alert-warning mb-0">
            <small>Could not load profile. <button type="button" class="btn btn-link btn-sm p-0 align-baseline user-profile-retry">Retry</button></small>
          </div>
        `;
        this.loading.querySelector('.user-profile-retry')?.addEventListener('click', () => this.loadProfile());
      }
    }
  },

  populateForm(profile) {
    const preferences = profile.preferences || {};
    this.setValue('profileDisplayName', profile.display_name || '');
    this.setValue('profileEmail', profile.email || '');
    this.setValue('profileTimezone', profile.timezone || this.detectTimezone());
    this.setValue('profileLocale', profile.locale || navigator.language || '');
    this.setValue('profileRoleCategory', profile.role_category || '');
    this.setValue('profileSpecializations', Array.isArray(profile.specializations) ? profile.specializations.join(', ') : '');
    this.setValue('profileResponseStyle', preferences.response_style || '');
    this.setValue('profileUnits', preferences.units || '');
    this.setValue('profileLanguage', preferences.language || '');
    this.setValue('profileAbout', profile.about || '');
  },

  async saveProfile() {
    const button = document.getElementById('saveUserProfileBtn');
    if (button) button.disabled = true;
    try {
      const body = this.readForm();
      const response = await fetch('/api/user/profile', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body)
      });
      if (!response.ok) {
        const error = await response.text();
        throw new Error(error || 'Failed to save profile');
      }
      const data = await response.json();
      this.profile = data.profile || body;
      this.populateForm(this.profile);
      this.notify('Profile saved', 'success');
    } catch (error) {
      console.error('Error saving profile:', error);
      this.notify('Failed to save profile: ' + error.message, 'error');
    } finally {
      if (button) button.disabled = false;
    }
  },

  readForm() {
    const preferences = {};
    for (const [key, id] of Object.entries({
      response_style: 'profileResponseStyle',
      units: 'profileUnits',
      language: 'profileLanguage'
    })) {
      const value = this.getValue(id);
      if (value) preferences[key] = value;
    }

    return {
      display_name: this.getValue('profileDisplayName'),
      email: this.getValue('profileEmail'),
      timezone: this.getValue('profileTimezone'),
      locale: this.getValue('profileLocale'),
      role_category: this.getValue('profileRoleCategory'),
      specializations: this.getValue('profileSpecializations')
        .split(',')
        .map((item) => item.trim())
        .filter(Boolean),
      preferences,
      about: this.getValue('profileAbout')
    };
  },

  getValue(id) {
    return document.getElementById(id)?.value?.trim() || '';
  },

  setValue(id, value) {
    const el = document.getElementById(id);
    if (el) el.value = value;
  },

  detectTimezone() {
    try {
      return Intl.DateTimeFormat().resolvedOptions().timeZone || '';
    } catch (_) {
      return '';
    }
  },

  notify(message, type) {
    if (window.Toast && typeof window.Toast[type] === 'function') {
      window.Toast[type](message);
    } else if (typeof SettingsController !== 'undefined') {
      SettingsController.notify(message, type);
    }
  }
};

document.addEventListener('DOMContentLoaded', function() {
  if (document.getElementById('userProfileForm')) {
    userProfileManager.init();
  }
});
