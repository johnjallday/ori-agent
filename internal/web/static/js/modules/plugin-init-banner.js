// Plugin Initialization Banner Module
// Shows a banner when plugins need configuration before use

let bannerElement = null;
let uninitializedPlugins = [];

/**
 * Initialize the plugin init banner
 * Checks for uninitialized plugins and shows banner if needed
 */
export async function initPluginBanner() {
  // Check on page load
  await checkUninitializedPlugins();
}

/**
 * Check for uninitialized plugins for the current agent
 */
export async function checkUninitializedPlugins() {
  try {
    const response = await fetch('/api/plugins/init-status');
    if (!response.ok) {
      console.warn('Failed to check uninitialized plugins');
      return;
    }

    const data = await response.json();

    if (data.requires_initialization && data.uninitialized_plugins) {
      uninitializedPlugins = data.uninitialized_plugins;
      showBanner(uninitializedPlugins);
    } else {
      uninitializedPlugins = [];
      hideBanner();
    }
  } catch (error) {
    console.error('Error checking uninitialized plugins:', error);
  }
}

/**
 * Show the plugin initialization banner
 * @param {Array} plugins - List of uninitialized plugins
 */
function showBanner(plugins) {
  const chatContainer = document.getElementById('chatContainer');
  if (!chatContainer) return;

  // Create banner if it doesn't exist
  if (!bannerElement) {
    bannerElement = createBannerElement();
  }

  // Insert banner at the top of chat container if not already there
  if (!chatContainer.contains(bannerElement)) {
    chatContainer.insertBefore(bannerElement, chatContainer.firstChild);
  }

  // Update banner content
  updateBannerContent(plugins);
  bannerElement.style.display = 'flex';
}

/**
 * Hide the plugin initialization banner
 */
function hideBanner() {
  if (bannerElement) {
    bannerElement.style.display = 'none';
  }
}

/**
 * Create the banner DOM element
 * @returns {HTMLElement}
 */
function createBannerElement() {
  const div = document.createElement('div');
  div.id = 'pluginInitBanner';
  div.className = 'plugin-init-banner';
  div.setAttribute('role', 'alert');
  div.innerHTML = `
    <div class="banner-icon">
      <svg width="18" height="18" viewBox="0 0 24 24" fill="currentColor">
        <path d="M12,2A10,10 0 0,0 2,12A10,10 0 0,0 12,22A10,10 0 0,0 22,12A10,10 0 0,0 12,2M12,17L7,12L8.41,10.59L12,14.17L19.59,6.58L21,8L12,17Z"/>
      </svg>
    </div>
    <div class="banner-content">
      <span class="banner-title">Plugins need configuration</span>
      <span class="banner-plugins"></span>
    </div>
    <div class="banner-actions"></div>
    <button type="button" class="banner-dismiss" title="Dismiss" aria-label="Dismiss banner">
      <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor">
        <path d="M19 6.41L17.59 5 12 10.59 6.41 5 5 6.41 10.59 12 5 17.59 6.41 19 12 13.41 17.59 19 19 17.59 13.41 12z"/>
      </svg>
    </button>
  `;

  // Add dismiss handler
  div.querySelector('.banner-dismiss').addEventListener('click', () => {
    hideBanner();
  });

  return div;
}

/**
 * Update banner content with plugin list and configure buttons
 * @param {Array} plugins - List of uninitialized plugins
 */
function updateBannerContent(plugins) {
  const pluginsSpan = bannerElement.querySelector('.banner-plugins');
  const actionsDiv = bannerElement.querySelector('.banner-actions');

  // Show plugin names
  const pluginNames = plugins.map(p => {
    if (typeof p === 'object') {
      return p.metadata?.name || stripVersionSuffix(p.name || p.plugin_name || '');
    }
    return stripVersionSuffix(p);
  }).join(', ');
  pluginsSpan.textContent = pluginNames;

  // Clear existing buttons
  actionsDiv.innerHTML = '';

  // Add configure button for each plugin (max 2, then "Configure All")
  if (plugins.length <= 2) {
    plugins.forEach(plugin => {
      const pluginName = plugin.name || plugin.plugin_name || plugin;
      const displayName = typeof plugin === 'object' ? (plugin.metadata?.name || stripVersionSuffix(pluginName)) : stripVersionSuffix(pluginName);
      const btn = document.createElement('button');
      btn.type = 'button';
      btn.className = 'banner-configure-btn';
      btn.textContent = `Configure ${displayName}`;
      btn.addEventListener('click', () => {
        if (window.showPluginConfigModal) {
          window.showPluginConfigModal(pluginName);
        }
      });
      actionsDiv.appendChild(btn);
    });
  } else {
    // Multiple plugins - show count and single button
    const btn = document.createElement('button');
    btn.type = 'button';
    btn.className = 'banner-configure-btn';
    btn.textContent = `Configure ${plugins.length} Plugins`;
    btn.addEventListener('click', () => {
          // Configure first plugin, user can configure others after
          const firstPlugin = plugins[0];
          const pluginName = firstPlugin.name || firstPlugin.plugin_name || firstPlugin;
          
          if (window.showPluginConfigModal) {
            window.showPluginConfigModal(pluginName);
          }
      
    });
    actionsDiv.appendChild(btn);
  }
}

/**
 * Handle chat response that indicates plugins need initialization
 * Called from sendMessage() when response has requires_initialization
 * @param {Array} plugins - Uninitialized plugins from response
 */
export function handleInitializationRequired(plugins) {
  uninitializedPlugins = plugins;
  showBanner(plugins);
}

/**
 * Refresh banner after plugin configuration
 */
export async function refreshBanner() {
  await checkUninitializedPlugins();
}

// Make functions available globally
window.checkUninitializedPlugins = checkUninitializedPlugins;
window.refreshPluginBanner = refreshBanner;
