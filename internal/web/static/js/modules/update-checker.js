// Update Checker Module
// Checks for available updates and shows notification in navbar

let updateCheckInterval = null;
let cachedUpdateInfo = null;
let updateMessageShown = false; // Track if we've shown the update message this session

// Check for updates on page load and periodically
async function initUpdateChecker() {
  // Check immediately on load
  const updateInfo = await checkForUpdates();

  // If update is available on first load, show a chat message (only once per session)
  if (updateInfo && updateInfo.updateAvailable && !updateMessageShown) {
    showUpdateChatMessage(updateInfo);
    updateMessageShown = true;
  }

  // Check every 30 minutes
  updateCheckInterval = setInterval(checkForUpdates, 30 * 60 * 1000);
}

// Check for available updates
async function checkForUpdates(showNotification = false) {
  try {
    const response = await fetch('/api/updates/check');
    if (!response.ok) {
      console.error('Failed to check for updates:', response.status);
      return;
    }

    const updateInfo = await response.json();
    cachedUpdateInfo = updateInfo;

    if (updateInfo.updateAvailable) {
      showUpdateNotificationButton(updateInfo);

      if (showNotification) {
        showUpdateToast(updateInfo);
      }
    } else {
      hideUpdateNotificationButton();
    }

    return updateInfo;
  } catch (error) {
    console.error('Error checking for updates:', error);
  }
}

// Show the update notification button in navbar
function showUpdateNotificationButton(updateInfo) {
  const btn = document.getElementById('updateNotificationBtn');
  if (btn) {
    btn.style.display = 'flex';
    btn.setAttribute('data-version', updateInfo.latestVersion);
  }
}

// Hide the update notification button
function hideUpdateNotificationButton() {
  const btn = document.getElementById('updateNotificationBtn');
  if (btn) {
    btn.style.display = 'none';
  }
}

// Show update modal with details
async function showUpdateModal() {
  try {
    await ensureBootstrapModal();
  } catch (error) {
    console.error('Bootstrap not available for update modal:', error);
    return;
  }

  const modalElement = ensureUpdateModalElement();
  if (!modalElement) {
    console.error('Update modal container is missing and could not be created.');
    return;
  }

  const modal = new bootstrap.Modal(modalElement);
  modal.show();

  // If we have cached update info, use it; otherwise fetch fresh
  let updateInfo = cachedUpdateInfo;
  if (!updateInfo) {
    updateInfo = await checkForUpdates();
  }

  if (!updateInfo) {
    document.getElementById('updateModalBody').innerHTML = `
      <div class="alert alert-danger" role="alert">
        Failed to fetch update information. Please try again later.
      </div>
    `;
    return;
  }

  displayUpdateInfo(updateInfo);
}

// Display update information in modal
function displayUpdateInfo(updateInfo) {
  const modalBody = document.getElementById('updateModalBody');
  const downloadBtn = document.getElementById('downloadUpdateBtn');

  // Detect current platform
  const platform = detectPlatform();
  const asset = findAssetForPlatform(updateInfo.assets, platform);
  const assetName = asset ? asset.name || asset.Name || '' : '';
  const assetSize = asset ? asset.size ?? asset.Size ?? 0 : 0;
  const assetURL = asset ? asset.url || asset.URL || '' : '';
  const githubReleaseUrl = `https://github.com/${updateInfo.repository}/releases/tag/${updateInfo.latestVersion}`;

  // Format release date
  const releaseDate = new Date(updateInfo.releaseDate).toLocaleDateString('en-US', {
    year: 'numeric',
    month: 'long',
    day: 'numeric'
  });

  // Format file size
  const fileSize = asset ? formatFileSize(assetSize) : 'N/A';
  const assetLabel = asset ? describeAsset(assetName, platform) : '';

  modalBody.innerHTML = `
    <div class="mb-4">
      <div class="d-flex justify-content-between align-items-center mb-3">
        <div>
          <h5 class="mb-1">Current Version</h5>
          <span class="badge bg-secondary">${updateInfo.currentVersion}</span>
        </div>
        <svg width="24" height="24" viewBox="0 0 24 24" fill="currentColor" style="color: var(--primary-color);">
          <path d="M8.59,16.58L13.17,12L8.59,7.41L10,6L16,12L10,18L8.59,16.58Z"/>
        </svg>
        <div>
          <h5 class="mb-1">Latest Version</h5>
          <span class="badge bg-success">${updateInfo.latestVersion}</span>
        </div>
      </div>

      <div class="alert alert-info d-flex align-items-center" role="alert">
        <svg width="20" height="20" viewBox="0 0 24 24" fill="currentColor" class="me-2">
          <path d="M13,9H11V7H13M13,17H11V11H13M12,2A10,10 0 0,0 2,12A10,10 0 0,0 12,22A10,10 0 0,0 22,12A10,10 0 0,0 12,2Z"/>
        </svg>
        <div>
          <strong>Released:</strong> ${releaseDate}
          ${asset ? `<br><strong>Download Size:</strong> ${fileSize}` : ''}
        </div>
      </div>
    </div>

    <div class="mb-3">
      <h6 class="mb-2">What's New:</h6>
      <div class="p-3" style="background: var(--bg-secondary); border-radius: 8px; max-height: 300px; overflow-y: auto;">
        <pre class="mb-0" style="white-space: pre-wrap; font-size: 0.875rem; color: var(--text-primary);">${updateInfo.releaseNotes}</pre>
      </div>
    </div>

    ${asset ? `
      <div class="alert alert-success" role="alert">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" class="me-2">
          <path d="M21,7L9,19L3.5,13.5L4.91,12.09L9,16.17L19.59,5.59L21,7Z"/>
        </svg>
        <div>
          <strong>Installer available for your platform (${platform}).</strong><br>
          ${assetLabel ? `${assetLabel}<br>` : ''}
          <small class="text-muted">File: ${assetName} (${fileSize})</small>
        </div>
      </div>
    ` : `
      <div class="alert alert-warning" role="alert">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" class="me-2">
          <path d="M13,14H11V10H13M13,18H11V16H13M1,21H23L12,2L1,21Z"/>
        </svg>
        No binary found for your platform (${platform}). Please visit the GitHub releases page.
      </div>
    `}
  `;

  // Setup download button
  if (asset) {
    downloadBtn.href = assetURL;
    downloadBtn.target = '_blank';
    downloadBtn.rel = 'noopener noreferrer';
    downloadBtn.setAttribute('download', assetName || 'installer');
    downloadBtn.textContent = 'Download Installer';
    downloadBtn.onclick = null;
    downloadBtn.style.display = 'inline-flex';
  } else {
    downloadBtn.href = githubReleaseUrl;
    downloadBtn.target = '_blank';
    downloadBtn.textContent = 'View on GitHub';
    downloadBtn.onclick = null;
    downloadBtn.style.display = 'inline-flex';
  }
}

// Download update using the API
async function downloadUpdate(version, asset) {
  const downloadBtn = document.getElementById('downloadUpdateBtn');
  const downloadStatus = document.getElementById('downloadStatus');
  const originalHTML = downloadBtn.innerHTML;

  try {
    // Disable button and show loading state
    downloadBtn.disabled = true;
    downloadBtn.innerHTML = `
      <div class="spinner-border spinner-border-sm me-2" role="status">
        <span class="visually-hidden">Downloading...</span>
      </div>
      Downloading...
    `;

    downloadStatus.innerHTML = `
      <div class="alert alert-info" role="alert">
        <div class="d-flex align-items-center">
          <div class="spinner-border spinner-border-sm me-2" role="status"></div>
          <div>Downloading ${asset.name}...</div>
        </div>
      </div>
    `;

    const response = await fetch('/api/updates/download', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json'
      },
      body: JSON.stringify({
        version: version,
        autoRestart: true
      })
    });

    const result = await response.json();

    if (!response.ok || !result.success) {
      throw new Error(result.message || 'Download failed');
    }

    // Success
    if (result.autoRestart) {
      downloadStatus.innerHTML = `
        <div class="alert alert-success" role="alert">
          <svg width="20" height="20" viewBox="0 0 24 24" fill="currentColor" class="me-2">
            <path d="M21,7L9,19L3.5,13.5L4.91,12.09L9,16.17L19.59,5.59L21,7Z"/>
          </svg>
          <strong>Update Complete!</strong>
          <br>
          <small>File saved to: ${result.filePath}</small>
          <br>
          <small class="text-muted">Application is restarting... Please refresh the page in a few seconds.</small>
        </div>
      `;
    } else {
      downloadStatus.innerHTML = `
        <div class="alert alert-success" role="alert">
          <svg width="20" height="20" viewBox="0 0 24 24" fill="currentColor" class="me-2">
            <path d="M21,7L9,19L3.5,13.5L4.91,12.09L9,16.17L19.59,5.59L21,7Z"/>
          </svg>
          <strong>Download Complete!</strong>
          <br>
          <small>File saved to: ${result.filePath}</small>
          <br>
          <small class="text-muted">Please restart ori-agent to use the new version.</small>
        </div>
      `;
    }

    downloadBtn.innerHTML = `
      <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" class="me-1">
        <path d="M21,7L9,19L3.5,13.5L4.91,12.09L9,16.17L19.59,5.59L21,7Z"/>
      </svg>
      Downloaded
    `;
    downloadBtn.className = 'modern-btn modern-btn-secondary';

  } catch (error) {
    console.error('Download error:', error);

    downloadStatus.innerHTML = `
      <div class="alert alert-danger" role="alert">
        <svg width="20" height="20" viewBox="0 0 24 24" fill="currentColor" class="me-2">
          <path d="M13,14H11V10H13M13,18H11V16H13M1,21H23L12,2L1,21Z"/>
        </svg>
        <strong>Download Failed</strong>
        <br>
        <small>${error.message}</small>
      </div>
    `;

    // Reset button
    downloadBtn.disabled = false;
    downloadBtn.innerHTML = originalHTML;
  }
}

// Detect current platform
function detectPlatform() {
  const platform = navigator.platform.toLowerCase();
  const userAgent = navigator.userAgent.toLowerCase();

  let os = 'linux';
  let arch = 'amd64';

  if (platform.includes('mac') || userAgent.includes('mac')) {
    os = 'darwin';
  } else if (platform.includes('win') || userAgent.includes('win')) {
    os = 'windows';
  }

  // Detect ARM architecture
  if (platform.includes('arm') || userAgent.includes('arm') ||
      platform.includes('aarch64') || userAgent.includes('aarch64')) {
    arch = 'arm64';
  }

  return `${os}-${arch}`;
}

// Find the appropriate asset for the detected platform
function findAssetForPlatform(assets, platform) {
  if (!assets || assets.length === 0) {
    return null;
  }

  const [os, arch] = platform.split('-');

  const osTokens = {
    darwin: ['darwin', 'macos', 'osx', 'mac'],
    linux: ['linux'],
    windows: ['windows', 'win']
  };

  const archTokens = {
    amd64: ['amd64', 'x86_64', 'x64'],
    arm64: ['arm64', 'aarch64', 'arm64v8']
  };

  const matchesArch = (name) => archTokens[arch]?.some((token) => name.includes(token)) || false;
  const matchesOS = (name) => osTokens[os]?.some((token) => name.includes(token)) || false;

  const candidates = assets.map((asset) => ({
    ...asset,
    _name: (asset.name || asset.Name || '').toLowerCase()
  }));

  // Try exact match first
  let asset = candidates.find((a) => a._name.includes(platform));

  // If no exact match, look for explicit OS+arch match
  if (!asset) {
    asset = candidates.find((a) => matchesOS(a._name) && matchesArch(a._name));
  }

  // macOS DMGs often omit the OS token; prefer DMG/ZIP with correct arch
  if (!asset && os === 'darwin') {
    asset = candidates.find(
      (a) =>
        matchesArch(a._name) &&
        (a._name.endsWith('.dmg') || a._name.endsWith('.zip') || a._name.endsWith('.pkg'))
    );
  }

  // Windows installers
  if (!asset && os === 'windows') {
    asset = candidates.find(
      (a) =>
        matchesArch(a._name) &&
        (a._name.endsWith('.msi') || a._name.endsWith('.exe') || a._name.endsWith('.zip'))
    );
  }

  // Linux packages (deb/rpm/tar.gz)
  if (!asset && os === 'linux') {
    asset = candidates.find(
      (a) =>
        matchesArch(a._name) &&
        (a._name.endsWith('.deb') || a._name.endsWith('.rpm') || a._name.endsWith('.tar.gz'))
    );
  }

  // If no exact match and platform is darwin-arm64, try darwin-amd64 (Rosetta compatibility)
  if (!asset && platform === 'darwin-arm64') {
    asset = candidates.find((a) => a._name.includes('darwin-amd64') || a._name.includes('macos-amd64'));
  }

  return asset || null;
}

// Format file size in human-readable format
function formatFileSize(bytes) {
  if (bytes === 0) return '0 Bytes';

  const k = 1024;
  const sizes = ['Bytes', 'KB', 'MB', 'GB'];
  const i = Math.floor(Math.log(bytes) / Math.log(k));

  return Math.round((bytes / Math.pow(k, i)) * 100) / 100 + ' ' + sizes[i];
}

// Provide a readable label for the selected asset
function describeAsset(assetName, platform) {
  if (!assetName) return '';
  const lowerName = assetName.toLowerCase();
  if (lowerName.endsWith('.dmg')) {
    return platform.includes('arm64') ? 'macOS Apple Silicon installer (.dmg)' : 'macOS Intel installer (.dmg)';
  }
  if (lowerName.endsWith('.msi')) return 'Windows installer (.msi)';
  if (lowerName.endsWith('.exe')) return 'Windows standalone (.exe)';
  if (lowerName.endsWith('.deb')) return 'Linux Debian/Ubuntu package (.deb)';
  if (lowerName.endsWith('.rpm')) return 'Linux RPM package (.rpm)';
  if (lowerName.endsWith('.tar.gz')) return 'Compressed archive (.tar.gz)';
  if (lowerName.endsWith('.zip')) return 'Compressed archive (.zip)';
  return 'Download';
}

// Show toast notification for available update
function showUpdateToast(updateInfo) {
  // Only show if browser supports notifications and user hasn't dismissed
  if (!('Notification' in window)) {
    return;
  }

  // Simple console notification for now
  console.log(`Update available: ${updateInfo.latestVersion}`);
}

// Show update message in chat
function showUpdateChatMessage(updateInfo) {
  // Wait a bit for the chat area to be ready
  setTimeout(() => {
    if (typeof addMessageToChat === 'function') {
      const message = `🎉 **Update Available!**

A new version of Ori Agent is available!

**Current Version:** ${updateInfo.currentVersion}
**Latest Version:** ${updateInfo.latestVersion}

Click the **Update** button in the top navigation bar to download and install the latest version.

---
*This message is shown once per session when an update is available.*`;

      // Pass true for isSystemNotification to prevent storing in chat history
      addMessageToChat(message, false, false, true);
    }
  }, 1000); // Wait 1 second for DOM to be ready
}

// Make functions globally available
window.initUpdateChecker = initUpdateChecker;
window.checkForUpdates = checkForUpdates;
window.showUpdateModal = showUpdateModal;

// Ensure update modal exists (for pages that don't include modals.tmpl)
function ensureUpdateModalElement() {
  let modalElement = document.getElementById('updateModal');
  if (modalElement) return modalElement;

  const wrapper = document.createElement('div');
  wrapper.innerHTML = `
    <div class="modal fade" id="updateModal" tabindex="-1" aria-labelledby="updateModalLabel" aria-hidden="true">
      <div class="modal-dialog modal-lg">
        <div class="modal-content">
          <div class="modal-header">
            <h5 class="modal-title" id="updateModalLabel">
              <svg width="20" height="20" viewBox="0 0 24 24" fill="currentColor" class="me-2">
                <path d="M5,20H19V18H5M19,9H15V3H9V9H5L12,16L19,9Z"/>
              </svg>
              Update Available
            </h5>
            <button type="button" class="btn-close" data-bs-dismiss="modal" aria-label="Close"></button>
          </div>
          <div class="modal-body" id="updateModalBody">
            <div class="text-center py-4">
              <div class="spinner-border text-primary" role="status">
                <span class="visually-hidden">Loading...</span>
              </div>
            </div>
          </div>
          <div class="modal-footer">
            <button type="button" class="modern-btn modern-btn-secondary" data-bs-dismiss="modal">Later</button>
            <a id="downloadUpdateBtn" href="#" class="modern-btn modern-btn-primary" target="_blank" style="display:none;">
              <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" class="me-1">
                <path d="M5,20H19V18H5M19,9H15V3H9V9H5L12,16L19,9Z"/>
              </svg>
              Download Update
            </a>
          </div>
        </div>
      </div>
    </div>
  `;

  const created = wrapper.firstElementChild;
  if (created) {
    document.body.appendChild(created);
    modalElement = created;
  }

  return modalElement;
}

// Ensure bootstrap is available before showing modal
let bootstrapReadyPromise = null;
function ensureBootstrapModal() {
  if (window.bootstrap && window.bootstrap.Modal) {
    return Promise.resolve(window.bootstrap);
  }

  if (!bootstrapReadyPromise) {
    bootstrapReadyPromise = new Promise((resolve, reject) => {
      // If a script tag is already loading bootstrap, reuse it
      let script = document.querySelector('script[data-ensure-bootstrap]');
      const onReady = () => (window.bootstrap ? resolve(window.bootstrap) : reject(new Error('Bootstrap failed to load')));

      if (script) {
        script.addEventListener('load', onReady, { once: true });
        script.addEventListener('error', () => reject(new Error('Bootstrap script failed to load')), { once: true });
        return;
      }

      script = document.createElement('script');
      script.src = 'https://cdn.jsdelivr.net/npm/bootstrap@5.3.0/dist/js/bootstrap.bundle.min.js';
      script.defer = true;
      script.setAttribute('data-ensure-bootstrap', 'true');
      script.addEventListener('load', onReady, { once: true });
      script.addEventListener('error', () => reject(new Error('Bootstrap script failed to load')), { once: true });
      document.head.appendChild(script);
    });
  }

  return bootstrapReadyPromise;
}

// Auto-initialize on page load
if (document.readyState === 'loading') {
  document.addEventListener('DOMContentLoaded', initUpdateChecker);
} else {
  initUpdateChecker();
}
