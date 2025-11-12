// Location Settings Manager Module
// Manages location zones and settings in the settings page

class LocationSettingsManager {
  constructor() {
    this.zones = [];
    this.editingZoneId = null;
  }

  // Initialize the location settings section
  async init() {
    await this.loadZones();
    this.renderZonesList();
    this.setupEventListeners();
  }

  // Load zones from API
  async loadZones() {
    try {
      const response = await fetch('/api/location/zones');
      if (!response.ok) {
        throw new Error('Failed to fetch zones');
      }
      this.zones = await response.json() || [];
    } catch (error) {
      console.error('Error loading zones:', error);
      this.showAlert('Failed to load location zones', 'danger');
      this.zones = [];
    }
  }

  // Render zones list in the UI
  renderZonesList() {
    const container = document.getElementById('locationZonesList');
    if (!container) return;

    if (this.zones.length === 0) {
      container.innerHTML = `
        <div class="text-center py-4" style="color: var(--text-secondary);">
          <svg width="48" height="48" viewBox="0 0 24 24" fill="none" xmlns="http://www.w3.org/2000/svg" class="mb-2" style="opacity: 0.5;">
            <path d="M12 2C8.13 2 5 5.13 5 9c0 5.25 7 13 7 13s7-7.75 7-13c0-3.87-3.13-7-7-7zm0 9.5c-1.38 0-2.5-1.12-2.5-2.5s1.12-2.5 2.5-2.5 2.5 1.12 2.5 2.5-1.12 2.5-2.5 2.5z" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
          </svg>
          <p class="mb-0">No location zones configured</p>
          <small>Click "Add Zone" to create your first location zone</small>
        </div>
      `;
      return;
    }

    const zonesHTML = this.zones.map(zone => `
      <div class="zone-item modern-card p-3 mb-3" data-zone-id="${zone.id}">
        <div class="d-flex justify-content-between align-items-start">
          <div class="flex-grow-1">
            <h6 class="mb-1" style="color: var(--text-primary);">${this.escapeHtml(zone.name)}</h6>
            <p class="mb-2 text-muted" style="font-size: 0.875rem;">${this.escapeHtml(zone.description || '')}</p>
            <div class="d-flex flex-wrap gap-2">
              ${zone.detection_rules.map(rule => `
                <span class="badge" style="background: var(--bg-tertiary); color: var(--text-secondary); border: 1px solid var(--border-color);">
                  <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor" class="me-1">
                    <path d="M12,21L15.6,16.2C14.6,15.45 13.35,15 12,15C10.65,15 9.4,15.45 8.4,16.2L12,21M12,3C7.95,3 4.21,4.34 1.2,6.6L3,9C5.5,7.12 8.62,6 12,6C15.38,6 18.5,7.12 21,9L22.8,6.6C19.79,4.34 16.05,3 12,3M12,9C9.3,9 6.81,9.89 4.8,11.4L6.6,13.8C8.1,12.67 9.97,12 12,12C14.03,12 15.9,12.67 17.4,13.8L19.2,11.4C17.19,9.89 14.7,9 12,9Z"/>
                  </svg>
                  WiFi: ${this.escapeHtml(rule.ssid)}
                </span>
              `).join('')}
            </div>
          </div>
          <div class="d-flex gap-2">
            <button class="btn btn-sm btn-outline-primary edit-zone-btn" data-zone-id="${zone.id}" title="Edit zone">
              <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
                <path d="M20.71,7.04C21.1,6.65 21.1,6 20.71,5.63L18.37,3.29C18,2.9 17.35,2.9 16.96,3.29L15.12,5.12L18.87,8.87M3,17.25V21H6.75L17.81,9.93L14.06,6.18L3,17.25Z"/>
              </svg>
            </button>
            <button class="btn btn-sm btn-outline-danger delete-zone-btn" data-zone-id="${zone.id}" title="Delete zone">
              <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
                <path d="M19,4H15.5L14.5,3H9.5L8.5,4H5V6H19M6,19A2,2 0 0,0 8,21H16A2,2 0 0,0 18,19V7H6V19Z"/>
              </svg>
            </button>
          </div>
        </div>
      </div>
    `).join('');

    container.innerHTML = zonesHTML;
  }

  // Setup event listeners
  setupEventListeners() {
    // Add zone button
    const addZoneBtn = document.getElementById('addLocationZoneBtn');
    if (addZoneBtn) {
      addZoneBtn.addEventListener('click', () => this.openZoneModal());
    }

    // Edit zone buttons
    document.addEventListener('click', (e) => {
      if (e.target.closest('.edit-zone-btn')) {
        const zoneId = e.target.closest('.edit-zone-btn').dataset.zoneId;
        this.editZone(zoneId);
      }
    });

    // Delete zone buttons
    document.addEventListener('click', (e) => {
      if (e.target.closest('.delete-zone-btn')) {
        const zoneId = e.target.closest('.delete-zone-btn').dataset.zoneId;
        this.deleteZone(zoneId);
      }
    });

    // Save zone button in modal
    const saveZoneBtn = document.getElementById('saveLocationZoneBtn');
    if (saveZoneBtn) {
      saveZoneBtn.addEventListener('click', () => this.saveZone());
    }

    // Modal close handler - reset form
    const modal = document.getElementById('locationZoneModal');
    if (modal) {
      modal.addEventListener('hidden.bs.modal', () => {
        this.resetZoneForm();
      });
    }
  }

  // Open zone modal (create or edit)
  openZoneModal(zone = null) {
    this.editingZoneId = zone ? zone.id : null;

    const modal = document.getElementById('locationZoneModal');
    const modalTitle = document.getElementById('locationZoneModalLabel');
    const zoneName = document.getElementById('zoneNameInput');
    const zoneDescription = document.getElementById('zoneDescriptionInput');
    const zoneSSID = document.getElementById('zoneSSIDInput');

    if (zone) {
      modalTitle.textContent = 'Edit Location Zone';
      zoneName.value = zone.name;
      zoneDescription.value = zone.description || '';
      zoneSSID.value = zone.detection_rules[0]?.ssid || '';
    } else {
      modalTitle.textContent = 'Add Location Zone';
      this.resetZoneForm();
    }

    const bsModal = new bootstrap.Modal(modal);
    bsModal.show();
  }

  // Reset zone form
  resetZoneForm() {
    this.editingZoneId = null;
    document.getElementById('zoneNameInput').value = '';
    document.getElementById('zoneDescriptionInput').value = '';
    document.getElementById('zoneSSIDInput').value = '';
  }

  // Save zone (create or update)
  async saveZone() {
    const name = document.getElementById('zoneNameInput').value.trim();
    const description = document.getElementById('zoneDescriptionInput').value.trim();
    const ssid = document.getElementById('zoneSSIDInput').value.trim();

    // Validation
    if (!name) {
      this.showAlert('Zone name is required', 'warning');
      return;
    }

    if (!ssid) {
      this.showAlert('WiFi SSID is required', 'warning');
      return;
    }

    const zoneData = {
      name: name,
      description: description,
      detection_rules: [{ ssid: ssid }]
    };

    try {
      let response;
      if (this.editingZoneId) {
        // Update existing zone
        zoneData.id = this.editingZoneId;
        response = await fetch(`/api/location/zones/${this.editingZoneId}`, {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(zoneData)
        });
      } else {
        // Create new zone
        response = await fetch('/api/location/zones', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(zoneData)
        });
      }

      if (!response.ok) {
        throw new Error('Failed to save zone');
      }

      // Close modal
      const modal = bootstrap.Modal.getInstance(document.getElementById('locationZoneModal'));
      modal.hide();

      // Reload zones
      await this.loadZones();
      this.renderZonesList();

      // Refresh location indicator
      if (typeof window.refreshLocation === 'function') {
        window.refreshLocation();
      }

      this.showAlert(
        this.editingZoneId ? 'Zone updated successfully' : 'Zone created successfully',
        'success'
      );
    } catch (error) {
      console.error('Error saving zone:', error);
      this.showAlert('Failed to save zone', 'danger');
    }
  }

  // Edit zone
  editZone(zoneId) {
    const zone = this.zones.find(z => z.id === zoneId);
    if (zone) {
      this.openZoneModal(zone);
    }
  }

  // Delete zone
  async deleteZone(zoneId) {
    const zone = this.zones.find(z => z.id === zoneId);
    if (!zone) return;

    if (!confirm(`Are you sure you want to delete the zone "${zone.name}"?`)) {
      return;
    }

    try {
      const response = await fetch(`/api/location/zones/${zoneId}`, {
        method: 'DELETE'
      });

      if (!response.ok) {
        throw new Error('Failed to delete zone');
      }

      // Reload zones
      await this.loadZones();
      this.renderZonesList();

      // Refresh location indicator
      if (typeof window.refreshLocation === 'function') {
        window.refreshLocation();
      }

      this.showAlert('Zone deleted successfully', 'success');
    } catch (error) {
      console.error('Error deleting zone:', error);
      this.showAlert('Failed to delete zone', 'danger');
    }
  }

  // Show alert message
  showAlert(message, type = 'info') {
    const alertContainer = document.getElementById('locationSettingsAlerts');
    if (!alertContainer) {
      console.log(message);
      return;
    }

    const alert = document.createElement('div');
    alert.className = `alert alert-${type} alert-dismissible fade show`;
    alert.role = 'alert';
    alert.innerHTML = `
      ${message}
      <button type="button" class="btn-close" data-bs-dismiss="alert" aria-label="Close"></button>
    `;

    alertContainer.appendChild(alert);

    // Auto-dismiss after 5 seconds
    setTimeout(() => {
      alert.remove();
    }, 5000);
  }

  // Escape HTML to prevent XSS
  escapeHtml(text) {
    const div = document.createElement('div');
    div.textContent = text;
    return div.innerHTML;
  }
}

// Initialize location settings manager when settings page loads
let locationSettingsManager = null;

function initLocationSettings() {
  if (document.getElementById('locationZonesList')) {
    locationSettingsManager = new LocationSettingsManager();
    locationSettingsManager.init();
  }
}

// Auto-initialize on page load
if (document.readyState === 'loading') {
  document.addEventListener('DOMContentLoaded', initLocationSettings);
} else {
  initLocationSettings();
}

// Export for global access
window.locationSettingsManager = locationSettingsManager;
window.initLocationSettings = initLocationSettings;
