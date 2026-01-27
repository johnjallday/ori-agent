// Plugin Pages Module
// Fetches and displays available plugin web pages in the navbar dropdown

(function() {
  'use strict';

  // Fetch and populate plugin pages in the navbar dropdown
  async function loadPluginPages() {
    const dropdown = document.getElementById('pluginPagesDropdown');
    const delimiter = document.getElementById('pluginPagesDelimiter');
    const menu = document.getElementById('pluginPagesMenu');

    if (!dropdown || !menu) {
      return;
    }

    try {
      const response = await fetch('/api/plugins/all-pages');
      if (!response.ok) {
        console.warn('Failed to fetch plugin pages:', response.status);
        return;
      }

      const data = await response.json();
      const pages = data.pages || [];

      if (pages.length === 0) {
        // No pages available, keep hidden
        return;
      }

      // Clear existing menu items
      menu.innerHTML = '';

      // Group pages by plugin
      const groupedPages = {};
      pages.forEach(page => {
        if (!groupedPages[page.plugin]) {
          groupedPages[page.plugin] = [];
        }
        groupedPages[page.plugin].push(page);
      });

      // Build menu items
      const pluginNames = Object.keys(groupedPages).sort();

      pluginNames.forEach((pluginName, index) => {
        const pluginPages = groupedPages[pluginName];

        // Add plugin header
        if (pluginNames.length > 1) {
          const header = document.createElement('li');
          header.innerHTML = `<h6 class="dropdown-header" style="color: var(--text-muted); font-size: 0.75rem; text-transform: uppercase; letter-spacing: 0.5px;">${formatPluginName(pluginName)}</h6>`;
          menu.appendChild(header);
        }

        // Add page items
        pluginPages.forEach(page => {
          const li = document.createElement('li');
          const a = document.createElement('a');
          a.className = 'dropdown-item';
          a.href = page.url;
          a.target = '_blank';
          a.innerHTML = `
            <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" style="opacity: 0.7;">
              <path d="M14,2H6A2,2 0 0,0 4,4V20A2,2 0 0,0 6,22H18A2,2 0 0,0 20,20V8L14,2M18,20H6V4H13V9H18V20Z"/>
            </svg>
            ${formatPageName(page.page)}
          `;

          li.appendChild(a);
          menu.appendChild(li);
        });

        // Add divider between plugins (not after the last one)
        if (index < pluginNames.length - 1) {
          const divider = document.createElement('li');
          divider.innerHTML = '<hr class="dropdown-divider" style="border-color: var(--border-color); margin: 0.5rem 0;">';
          menu.appendChild(divider);
        }
      });

      // Show the dropdown and delimiter
      dropdown.style.display = 'block';
      if (delimiter) {
        delimiter.style.display = 'inline';
      }

    } catch (error) {
      console.error('Error loading plugin pages:', error);
    }
  }

  // Format plugin name for display (e.g., "ori-music-project-manager" -> "Music Project Manager")
  function formatPluginName(name) {
    return stripVersionSuffix(name)
      .replace(/^ori-/, '')  // Remove ori- prefix
      .replace(/-/g, ' ')     // Replace hyphens with spaces
      .replace(/\b\w/g, c => c.toUpperCase());  // Capitalize first letter of each word
  }

  // Format page name for display (e.g., "audio-plugins" -> "Audio Plugins")
  function formatPageName(name) {
    return name
      .replace(/-/g, ' ')     // Replace hyphens with spaces
      .replace(/\b\w/g, c => c.toUpperCase());  // Capitalize first letter of each word
  }

  // Initialize when DOM is ready
  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', loadPluginPages);
  } else {
    loadPluginPages();
  }

  // Expose for manual refresh if needed
  window.refreshPluginPages = loadPluginPages;
})();
