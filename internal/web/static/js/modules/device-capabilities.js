/**
 * Device Capabilities Module
 * Handles loading and displaying device hardware capabilities for local AI models
 */

const deviceCapabilities = {
  /**
   * Initialize the device capabilities section
   */
  async init() {
    await this.loadCapabilities();
    this.bindEvents();
  },

  /**
   * Bind event listeners
   */
  bindEvents() {
    const redetectBtn = document.getElementById('redetectHardwareBtn');
    if (redetectBtn) {
      redetectBtn.addEventListener('click', () => this.redetectHardware());
    }
  },

  /**
   * Load device capabilities from the API
   */
  async loadCapabilities() {
    const loadingEl = document.getElementById('deviceCapabilitiesContent');
    const displayEl = document.getElementById('deviceCapabilitiesDisplay');

    if (!loadingEl || !displayEl) return;

    try {
      const response = await fetch('/api/device/capabilities');
      if (!response.ok) {
        throw new Error('Failed to fetch device capabilities');
      }

      const data = await response.json();
      this.renderCapabilities(data);

      // Hide loading, show display
      loadingEl.classList.add('d-none');
      displayEl.classList.remove('d-none');
    } catch (error) {
      console.error('Error loading device capabilities:', error);
      loadingEl.innerHTML = `
        <div class="text-center py-4" style="color: var(--text-secondary);">
          <svg width="48" height="48" viewBox="0 0 24 24" fill="currentColor" style="opacity: 0.5;" class="mb-3">
            <path d="M13,13H11V7H13M13,17H11V15H13M12,2A10,10 0 0,0 2,12A10,10 0 0,0 12,22A10,10 0 0,0 22,12A10,10 0 0,0 12,2Z"/>
          </svg>
          <p>Unable to detect hardware capabilities.</p>
          <button class="modern-btn modern-btn-secondary mt-2" onclick="deviceCapabilities.loadCapabilities()">
            Try Again
          </button>
        </div>
      `;
    }
  },

  /**
   * Render the capabilities data in the UI
   */
  renderCapabilities(data) {
    // Machine info (if available)
    const machineNameEl = document.getElementById('deviceMachineName');
    const chipTypeEl = document.getElementById('deviceChipType');
    const machineInfoSection = document.getElementById('deviceMachineInfoSection');

    if (machineNameEl && chipTypeEl) {
      if (data.machine_name || data.chip_type) {
        machineNameEl.textContent = data.machine_name || '-';
        chipTypeEl.textContent = data.chip_type || '-';
        if (machineInfoSection) {
          machineInfoSection.classList.remove('d-none');
        }
      } else {
        if (machineInfoSection) {
          machineInfoSection.classList.add('d-none');
        }
      }
    }

    // GPU info
    const gpuNameEl = document.getElementById('deviceGPUName');
    const gpuVendorEl = document.getElementById('deviceGPUVendor');

    if (data.gpu) {
      gpuNameEl.textContent = data.gpu.name || 'Unknown GPU';

      let vendorText = data.gpu.vendor || 'Unknown';
      if (data.gpu.is_apple_silicon) {
        vendorText = 'Apple Silicon (Unified Memory)';
      } else if (data.gpu.is_discrete) {
        vendorText = `${data.gpu.vendor} (Discrete)`;
      } else {
        vendorText = `${data.gpu.vendor} (Integrated)`;
      }
      gpuVendorEl.textContent = vendorText;
    } else {
      gpuNameEl.textContent = 'No GPU detected';
      gpuVendorEl.textContent = 'CPU only';
    }

    // RAM info
    const ramEl = document.getElementById('deviceRAM');
    const ramDetailsEl = document.getElementById('deviceRAMDetails');

    ramEl.textContent = data.total_ram_formatted || '-';
    if (data.gpu && data.gpu.is_apple_silicon) {
      ramDetailsEl.textContent = 'Unified memory shared with GPU';
    } else if (data.gpu && data.gpu.is_discrete && data.gpu.vram > 0) {
      ramDetailsEl.textContent = `GPU VRAM: ${this.formatBytes(data.gpu.vram)}`;
    } else {
      ramDetailsEl.textContent = 'System memory';
    }

    // Model capacity
    const maxParamsEl = document.getElementById('deviceMaxParams');
    const tierBadgeEl = document.getElementById('deviceTierBadge');
    const tierDescEl = document.getElementById('deviceTierDescription');

    maxParamsEl.textContent = data.max_model_params || '-';
    tierBadgeEl.textContent = data.memory_tier || '-';
    tierDescEl.textContent = data.tier_description || '-';

    // Set tier badge color based on tier
    const tierColors = {
      'Basic': '#6c757d',
      'Standard': '#17a2b8',
      'Advanced': '#28a745',
      'Professional': '#ffc107',
      'Enterprise': '#dc3545'
    };
    tierBadgeEl.style.background = tierColors[data.memory_tier] || 'var(--bg-tertiary)';
    tierBadgeEl.style.color = data.memory_tier === 'Professional' ? '#000' : '#fff';

    // Recommended models
    const modelsEl = document.getElementById('deviceRecommendedModels');
    if (data.recommended_models && data.recommended_models.length > 0) {
      modelsEl.innerHTML = data.recommended_models.map(model => `
        <a href="https://ollama.com/library/${model.split(':')[0]}"
           target="_blank"
           rel="noopener noreferrer"
           class="badge"
           style="background: var(--bg-tertiary); color: var(--text-primary); text-decoration: none; padding: 0.5rem 0.75rem; font-weight: 500;">
          ${model}
        </a>
      `).join('');
    } else {
      modelsEl.innerHTML = '<span style="color: var(--text-secondary);">No model recommendations available</span>';
    }

    // Ollama link
    const ollamaLinkEl = document.getElementById('deviceOllamaLink');
    if (ollamaLinkEl && data.ollama_library_url) {
      ollamaLinkEl.href = data.ollama_library_url;
    }
  },

  /**
   * Re-detect hardware capabilities
   */
  async redetectHardware() {
    const loadingEl = document.getElementById('deviceCapabilitiesContent');
    const displayEl = document.getElementById('deviceCapabilitiesDisplay');
    const btn = document.getElementById('redetectHardwareBtn');

    if (!loadingEl || !displayEl) return;

    // Show loading state
    displayEl.classList.add('d-none');
    loadingEl.classList.remove('d-none');
    loadingEl.innerHTML = `
      <div class="text-center py-4" style="color: var(--text-secondary);">
        <div class="spinner-border spinner-border-sm me-2" role="status"></div>
        Re-detecting hardware...
      </div>
    `;

    // Disable button
    if (btn) {
      btn.disabled = true;
    }

    try {
      const response = await fetch('/api/device/detect-hardware', {
        method: 'POST'
      });

      if (!response.ok) {
        throw new Error('Failed to re-detect hardware');
      }

      const data = await response.json();
      this.renderCapabilities(data);

      // Hide loading, show display
      loadingEl.classList.add('d-none');
      displayEl.classList.remove('d-none');

      // Show success toast if available
      if (typeof showToast === 'function') {
        showToast('Hardware re-detected successfully', 'success');
      }
    } catch (error) {
      console.error('Error re-detecting hardware:', error);
      loadingEl.innerHTML = `
        <div class="text-center py-4" style="color: var(--text-secondary);">
          <p>Failed to re-detect hardware.</p>
          <button class="modern-btn modern-btn-secondary" onclick="deviceCapabilities.redetectHardware()">
            Try Again
          </button>
        </div>
      `;
    } finally {
      // Re-enable button
      if (btn) {
        btn.disabled = false;
      }
    }
  },

  /**
   * Format bytes to human-readable string
   */
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
};

// Initialize when DOM is ready
document.addEventListener('DOMContentLoaded', () => {
  // Only initialize if we're on the settings page with device capabilities section
  if (document.getElementById('deviceCapabilitiesContent')) {
    deviceCapabilities.init();
  }
});
