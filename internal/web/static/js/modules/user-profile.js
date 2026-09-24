const USER_PROFILE_FALLBACK_TIMEZONES = [
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

const userProfileManager = {
  profile: null,

  async init() {
    this.form = document.getElementById('userProfileForm');
    this.loading = document.getElementById('userProfileContent');
    if (!this.form) return;
    this.populateTimezoneSelect(this.detectTimezone());

    this.form.addEventListener('submit', event => {
      event.preventDefault();
      this.saveProfile();
    });
    this.form.querySelectorAll('[data-profile-field][data-profile-action]').forEach(button => {
      button.addEventListener('click', () =>
        this.savePreference(button.dataset.profileField, button.dataset.profileAction, button)
      );
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
      this.emitProfileUpdated();
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
        this.loading
          .querySelector('.user-profile-retry')
          ?.addEventListener('click', () => this.loadProfile());
      }
    }
  },

  populateForm(profile) {
    const preferences = profile.preferences || {};
    this.populateTimezoneSelect(profile.timezone || this.detectTimezone());
    this.setValue('profileDisplayName', profile.display_name || '');
    this.setValue('profileEmail', profile.email || '');
    this.setValue('profileTimezone', profile.timezone || this.detectTimezone());
    this.setValue('profileLocale', profile.locale || navigator.language || '');
    this.setValue('profileRoleCategory', profile.role_category || '');
    this.setValue(
      'profileSpecializations',
      Array.isArray(profile.specializations) ? profile.specializations.join(', ') : ''
    );
    this.setValue('profileResponseStyle', preferences.response_style || '');
    this.setValue('profileUnits', preferences.units || '');
    this.setValue('profileLanguage', preferences.language || '');
    this.setValue('profileAbout', profile.about || '');
  },

  async savePreference(key, action, button) {
    const fields = {
      response_style: 'profileResponseStyle',
      units: 'profileUnits',
      language: 'profileLanguage'
    };
    const input = document.getElementById(fields[key]);
    if (
      !input ||
      !this.profile ||
      this.preferenceBusy ||
      this.fullProfileBusy ||
      !['save', 'forget'].includes(action)
    )
      return;
    const status = document.getElementById('userProfileFieldStatus');
    const announce = message => {
      if (status) status.textContent = message;
    };
    const before = this.profile.preferences?.[key] || '';
    const value = action === 'forget' ? '' : input.value.trim();
    if (action === 'forget') {
      if (input.value.trim() !== before) {
        announce('Save or discard your unsaved wording before forgetting this preference.');
        input.focus();
        return;
      }
      if (!before) {
        announce('No saved preference to forget.');
        return;
      }
      if (!window.confirm(`Forget your saved ${key.replace('_', ' ')} preference?`)) return;
    } else if (value === before) {
      announce('This preference is already saved.');
      return;
    } else if (
      !value ||
      new TextEncoder().encode(value).length > 500 ||
      value !== value.split(/\s+/u).join(' ') ||
      Array.from(value).some(character => {
        const code = character.codePointAt(0);
        return (
          code < 32 ||
          (code >= 127 && code <= 159) ||
          (code >= 0x202a && code <= 0x202e) ||
          (code >= 0x2066 && code <= 0x2069)
        );
      })
    ) {
      announce('Use one exact, safe line of at most 500 UTF-8 bytes for a single-field save.');
      input.focus();
      return;
    }
    if (!this.profile.updated_at) {
      announce('Save your global profile first, then edit one preference at a time.');
      return;
    }
    this.preferenceBusy = true;
    button.disabled = true;
    const saveProfileButton = document.getElementById('saveUserProfileBtn');
    if (saveProfileButton) saveProfileButton.disabled = true;
    announce(
      action === 'forget'
        ? 'Clearing only this global preference…'
        : 'Saving only this global preference…'
    );
    try {
      const response = await fetch('/api/user/profile', {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'same-origin',
        body: JSON.stringify({
          field: `preferences.${key}`,
          expected_updated_at: this.profile.updated_at,
          expected_value: before,
          value
        })
      });
      if (!response.ok) {
        const error = new Error('The canonical preference could not be verified.');
        error.status = response.status;
        throw error;
      }
      const data = await response.json();
      if (!data.profile || !data.profile.updated_at) throw new Error('Save status is unknown');
      this.profile = data.profile;
      input.value = data.profile.preferences?.[key] || '';
      this.emitProfileUpdated();
      document.dispatchEvent(new Event('personal-assistant-knowledge-changed'));
      announce(
        action === 'forget'
          ? 'Preference forgotten from your global profile.'
          : 'Global preference saved.'
      );
    } catch (error) {
      announce(
        error.status === 409
          ? 'The global profile changed. Your wording is still here; refresh and review before trying again.'
          : 'Could not verify the change. Your wording is still here; refresh before trying again.'
      );
    } finally {
      this.preferenceBusy = false;
      button.disabled = false;
      if (saveProfileButton) saveProfileButton.disabled = false;
    }
  },

  async saveProfile() {
    if (this.preferenceBusy || this.fullProfileBusy) return;
    this.fullProfileBusy = true;
    const button = document.getElementById('saveUserProfileBtn');
    const fieldButtons =
      this.form?.querySelectorAll('[data-profile-field][data-profile-action]') || [];
    if (button) button.disabled = true;
    fieldButtons.forEach(fieldButton => {
      fieldButton.disabled = true;
    });
    try {
      const body = this.readForm();
      const response = await fetch('/api/user/profile', {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body)
      });
      if (!response.ok) {
        if (response.status === 409) {
          const status = document.getElementById('userProfileFieldStatus');
          if (status)
            status.textContent =
              'The global profile changed. Your unsaved wording is still here; refresh and review before saving.';
          return;
        }
        throw new Error('Could not verify the profile save. Refresh before trying again.');
      }
      const data = await response.json();
      this.profile = data.profile || body;
      this.populateForm(this.profile);
      this.emitProfileUpdated();
      this.notify('Profile saved', 'success');
    } catch (error) {
      console.error('Error saving profile:', error);
      this.notify('Failed to save profile: ' + error.message, 'error');
    } finally {
      this.fullProfileBusy = false;
      if (button) button.disabled = false;
      fieldButtons.forEach(fieldButton => {
        fieldButton.disabled = false;
      });
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
      updated_at: this.profile?.updated_at || undefined,
      display_name: this.getValue('profileDisplayName'),
      email: this.getValue('profileEmail'),
      timezone: this.getValue('profileTimezone'),
      locale: this.getValue('profileLocale'),
      role_category: this.getValue('profileRoleCategory'),
      specializations: this.getValue('profileSpecializations')
        .split(',')
        .map(item => item.trim())
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

  getTimezoneOptions(selectedTimezone) {
    let zones = USER_PROFILE_FALLBACK_TIMEZONES;
    if (typeof Intl !== 'undefined' && typeof Intl.supportedValuesOf === 'function') {
      try {
        const supported = Intl.supportedValuesOf('timeZone');
        if (Array.isArray(supported) && supported.length > 0) {
          zones = supported;
        }
      } catch (_) {
        zones = USER_PROFILE_FALLBACK_TIMEZONES;
      }
    }

    const allZones = new Set(
      [selectedTimezone, this.detectTimezone(), 'UTC', ...zones].filter(Boolean)
    );

    return Array.from(allZones).sort((a, b) => {
      if (a === 'UTC') return -1;
      if (b === 'UTC') return 1;
      return a.localeCompare(b);
    });
  },

  formatTimezoneLabel(timezone) {
    if (!timezone || timezone === 'UTC') {
      return timezone || '';
    }
    return timezone
      .split('/')
      .map(part => part.replace(/_/g, ' '))
      .join(' / ');
  },

  populateTimezoneSelect(selectedTimezone) {
    const select = document.getElementById('profileTimezone');
    if (!select || select.tagName !== 'SELECT') return;

    const selected = selectedTimezone || this.detectTimezone();
    const zones = this.getTimezoneOptions(selected);
    select.innerHTML = '';

    if (!selected) {
      const placeholder = document.createElement('option');
      placeholder.value = '';
      placeholder.textContent = 'Select timezone...';
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
  },

  emitProfileUpdated() {
    document.dispatchEvent(
      new CustomEvent('ori:user-profile-updated', { detail: { profile: this.profile } })
    );
  },

  notify(message, type) {
    if (window.Toast && typeof window.Toast[type] === 'function') {
      window.Toast[type](message);
    } else if (typeof SettingsController !== 'undefined') {
      SettingsController.notify(message, type);
    }
  }
};

document.addEventListener('DOMContentLoaded', function () {
  if (document.getElementById('userProfileForm')) {
    userProfileManager.init();
  }
});
