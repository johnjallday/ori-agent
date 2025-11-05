// Plugin Marketplace Module
// Handles plugin browsing, installation, and updates

class PluginMarketplace {
    constructor() {
        this.plugins = [];
        this.installedPlugins = new Set();
        this.updates = [];
        this.filter = 'all';
        this.searchTerm = '';
    }

    async init() {
        // Load marketplace data
        await this.loadMarketplaceData();

        // Setup event listeners
        this.setupEventListeners();

        // Render initial view
        this.render();
    }

    async loadMarketplaceData() {
        try {
            // Load all plugins from registry (includes GitHub plugins + local plugins)
            const registryResp = await fetch('/api/plugin-registry');
            const registryData = await registryResp.json();
            this.plugins = registryData.plugins || [];

            // Load locally installed plugins to mark them as installed
            const installedResp = await fetch('/api/plugins');
            const installedData = await installedResp.json();
            const installedPluginNames = new Set(
                (installedData.plugins || []).map(p => p.name)
            );

            // Mark plugins as installed if they exist locally
            this.installedPlugins = installedPluginNames;
            this.plugins.forEach(plugin => {
                plugin.installed = installedPluginNames.has(plugin.name);
            });

            // Load available updates
            const updatesResp = await fetch('/api/plugins/check-updates');
            const updatesData = await updatesResp.json();
            this.updates = updatesData.updates || [];

        } catch (error) {
            console.error('Error loading marketplace data:', error);
        }
    }

    setupEventListeners() {
        // Search input
        document.getElementById('searchPlugins')?.addEventListener('input', (e) => {
            this.searchTerm = e.target.value.toLowerCase();
            this.render();
        });

        // Filter buttons
        document.getElementById('filterAll')?.addEventListener('change', () => {
            this.filter = 'all';
            this.render();
        });

        document.getElementById('filterInstalled')?.addEventListener('change', () => {
            this.filter = 'installed';
            this.render();
        });

        document.getElementById('filterAvailable')?.addEventListener('change', () => {
            this.filter = 'available';
            this.render();
        });

        document.getElementById('filterUpdates')?.addEventListener('change', () => {
            this.filter = 'updates';
            this.render();
        });

        // Refresh button
        document.getElementById('refreshMarketplaceBtn')?.addEventListener('click', async () => {
            await this.loadMarketplaceData();
            this.render();
        });
    }

    render() {
        const grid = document.getElementById('pluginGrid');
        if (!grid) return;

        // Filter plugins
        let filteredPlugins = this.plugins.filter(plugin => {
            // Search filter
            if (this.searchTerm) {
                const matchesSearch = plugin.name.toLowerCase().includes(this.searchTerm) ||
                    (plugin.description && plugin.description.toLowerCase().includes(this.searchTerm));
                if (!matchesSearch) return false;
            }

            // Category filter
            switch (this.filter) {
                case 'installed':
                    return this.installedPlugins.has(plugin.name);
                case 'available':
                    return !this.installedPlugins.has(plugin.name);
                case 'updates':
                    return this.updates.some(u => u.plugin_name === plugin.name);
                default:
                    return true;
            }
        });

        if (filteredPlugins.length === 0) {
            grid.innerHTML = `
                <div class="col-12 text-center py-5">
                    <svg width="64" height="64" viewBox="0 0 24 24" fill="currentColor" opacity="0.3">
                        <path d="M20.5,11H19V7C19,5.89 18.1,5 17,5H13V3.5A2.5,2.5 0 0,0 10.5,1A2.5,2.5 0 0,0 8,3.5V5H4C2.89,5 2,5.89 2,7V10.8H3.5C5,10.8 6.2,12 6.2,13.5C6.2,15 5,16.2 3.5,16.2H2V20C2,21.11 2.89,22 4,22H7.8V20.5C7.8,19 9,17.8 10.5,17.8C12,17.8 13.2,19 13.2,20.5V22H17C18.11,22 19,21.11 19,20V16H20.5A2.5,2.5 0 0,0 23,13.5A2.5,2.5 0 0,0 20.5,11Z"/>
                    </svg>
                    <p class="mt-3 text-muted">No plugins found</p>
                </div>
            `;
            return;
        }

        let html = '';
        filteredPlugins.forEach(plugin => {
            const isInstalled = this.installedPlugins.has(plugin.name);
            const hasUpdate = this.updates.find(u => u.plugin_name === plugin.name);

            html += `
                <div class="col-md-6 col-lg-4 mb-4">
                    <div class="card h-100 plugin-card" style="cursor: pointer;" onclick="marketplace.showPluginDetails('${plugin.name}')">
                        <div class="card-body">
                            <div class="d-flex justify-content-between align-items-start mb-2">
                                <h5 class="card-title mb-0">${plugin.name}</h5>
                                ${isInstalled ? `
                                    <span class="badge bg-success-subtle text-success">Installed</span>
                                ` : ''}
                                ${hasUpdate ? `
                                    <span class="badge bg-warning-subtle text-warning">Update</span>
                                ` : ''}
                            </div>

                            <p class="card-text text-muted small">${plugin.description || 'No description available'}</p>

                            <div class="mt-3">
                                <div class="d-flex justify-content-between align-items-center small text-muted">
                                    <span>
                                        <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" class="me-1">
                                            <path d="M12,17.56L16.07,16.43L16.62,10.33H9.38L9.2,8.3H16.8L17,6.31H7L7.56,12.32H14.45L14.22,14.9L12,15.5L9.78,14.9L9.64,13.24H7.64L7.93,16.43L12,17.56M4.07,3H19.93L18.5,19.2L12,21L5.5,19.2L4.07,3Z"/>
                                        </svg>
                                        v${plugin.version || '0.0.0'}
                                    </span>
                                    ${hasUpdate ? `
                                        <span class="text-warning">
                                            <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
                                                <path d="M12,18.17L8.83,15L7.42,16.41L12,21L16.59,16.41L15.17,15M12,5.83L15.17,9L16.58,7.59L12,3L7.41,7.59L8.83,9L12,5.83Z"/>
                                            </svg>
                                            v${hasUpdate.latest_version}
                                        </span>
                                    ` : ''}
                                </div>

                                ${plugin.metadata && plugin.metadata.maintainers && plugin.metadata.maintainers.length > 0 ? `
                                    <div class="mt-2 small text-muted">
                                        <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" class="me-1">
                                            <path d="M12,4A4,4 0 0,1 16,8A4,4 0 0,1 12,12A4,4 0 0,1 8,8A4,4 0 0,1 12,4M12,14C16.42,14 20,15.79 20,18V20H4V18C4,15.79 7.58,14 12,14Z"/>
                                        </svg>
                                        ${plugin.metadata.maintainers.find(m => m.primary)?.name || plugin.metadata.maintainers[0].name}
                                    </div>
                                ` : ''}

                                ${plugin.metadata && plugin.metadata.license ? `
                                    <div class="mt-2 small text-muted">
                                        <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" class="me-1">
                                            <path d="M9,10H7V16H9V10M13,10H11V16H13V10M17,10H15V16H17V10M19,3H5C3.89,3 3,3.89 3,5V19A2,2 0 0,0 5,21H19A2,2 0 0,0 21,19V5C21,3.89 20.1,3 19,3M19,19H5V5H19V19Z"/>
                                        </svg>
                                        ${plugin.metadata.license}
                                    </div>
                                ` : ''}

                                ${plugin.supported_os ? `
                                    <div class="mt-2 small text-muted">
                                        <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" class="me-1">
                                            <path d="M17,19H7V5H17M17,1H7C5.89,1 5,1.89 5,3V21A2,2 0 0,0 7,23H17A2,2 0 0,0 19,21V3C19,1.89 18.1,1 17,1Z"/>
                                        </svg>
                                        ${plugin.supported_os.join(', ')}
                                    </div>
                                ` : ''}
                            </div>
                        </div>
                        <div class="card-footer bg-transparent border-top-0">
                            ${!isInstalled ? `
                                <button class="btn btn-sm btn-primary w-100" onclick="event.stopPropagation(); marketplace.installPlugin('${plugin.name}')">
                                    <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" class="me-1">
                                        <path d="M5,20H19V18H5M19,9H15V3H9V9H5L12,16L19,9Z"/>
                                    </svg>
                                    Install
                                </button>
                            ` : hasUpdate ? `
                                <button class="btn btn-sm btn-warning w-100" onclick="event.stopPropagation(); marketplace.updatePlugin('${plugin.name}')">
                                    <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" class="me-1">
                                        <path d="M12,18.17L8.83,15L7.42,16.41L12,21L16.59,16.41L15.17,15M12,5.83L15.17,9L16.58,7.59L12,3L7.41,7.59L8.83,9L12,5.83Z"/>
                                    </svg>
                                    Update
                                </button>
                            ` : `
                                <button class="btn btn-sm btn-outline-secondary w-100" disabled>
                                    <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" class="me-1">
                                        <path d="M21,7L9,19L3.5,13.5L4.91,12.09L9,16.17L19.59,5.59L21,7Z"/>
                                    </svg>
                                    Installed
                                </button>
                            `}
                        </div>
                    </div>
                </div>
            `;
        });

        grid.innerHTML = html;
    }

    showPluginDetails(pluginName) {
        const plugin = this.plugins.find(p => p.name === pluginName);
        if (!plugin) return;

        const modal = new bootstrap.Modal(document.getElementById('pluginDetailsModal'));
        const isInstalled = this.installedPlugins.has(plugin.name);
        const hasUpdate = this.updates.find(u => u.plugin_name === plugin.name);

        document.getElementById('pluginDetailsTitle').textContent = plugin.name;
        document.getElementById('pluginDetailsBody').innerHTML = `
            <div class="mb-3">
                <h6>Description</h6>
                <p>${plugin.description || 'No description available'}</p>
            </div>

            <div class="mb-3">
                <h6>Version</h6>
                <p>
                    ${plugin.version || '0.0.0'}
                    ${hasUpdate ? ` <span class="badge bg-warning-subtle text-warning">Update available: v${hasUpdate.latest_version}</span>` : ''}
                </p>
            </div>

            ${plugin.metadata && plugin.metadata.maintainers && plugin.metadata.maintainers.length > 0 ? `
                <div class="mb-3">
                    <h6>Maintainers</h6>
                    ${plugin.metadata.maintainers.map(m => `
                        <div class="d-flex align-items-start mb-2">
                            <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" class="me-2 mt-1 text-muted">
                                <path d="M12,4A4,4 0 0,1 16,8A4,4 0 0,1 12,12A4,4 0 0,1 8,8A4,4 0 0,1 12,4M12,14C16.42,14 20,15.79 20,18V20H4V18C4,15.79 7.58,14 12,14Z"/>
                            </svg>
                            <div>
                                <strong>${m.name}</strong>
                                ${m.primary ? '<span class="badge bg-primary-subtle text-primary ms-2">Primary</span>' : ''}
                                ${m.role ? `<span class="badge bg-secondary-subtle text-secondary ms-2">${m.role}</span>` : ''}
                                ${m.email ? `<br><small class="text-muted">${m.email}</small>` : ''}
                                ${m.organization ? `<br><small class="text-muted">${m.organization}</small>` : ''}
                                ${m.website ? `<br><small><a href="${m.website}" target="_blank">${m.website}</a></small>` : ''}
                            </div>
                        </div>
                    `).join('')}
                </div>
            ` : ''}

            ${plugin.metadata && plugin.metadata.license ? `
                <div class="mb-3">
                    <h6>License</h6>
                    <p>
                        <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" class="me-1 text-muted">
                            <path d="M9,10H7V16H9V10M13,10H11V16H13V10M17,10H15V16H17V10M19,3H5C3.89,3 3,3.89 3,5V19A2,2 0 0,0 5,21H19A2,2 0 0,0 21,19V5C21,3.89 20.1,3 19,3M19,19H5V5H19V19Z"/>
                        </svg>
                        ${plugin.metadata.license}
                    </p>
                </div>
            ` : ''}

            ${plugin.metadata && plugin.metadata.repository ? `
                <div class="mb-3">
                    <h6>Repository</h6>
                    <p>
                        <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" class="me-1 text-muted">
                            <path d="M12,2A10,10 0 0,0 2,12C2,16.42 4.87,20.17 8.84,21.5C9.34,21.58 9.5,21.27 9.5,21C9.5,20.77 9.5,20.14 9.5,19.31C6.73,19.91 6.14,17.97 6.14,17.97C5.68,16.81 5.03,16.5 5.03,16.5C4.12,15.88 5.1,15.9 5.1,15.9C6.1,15.97 6.63,16.93 6.63,16.93C7.5,18.45 8.97,18 9.54,17.76C9.63,17.11 9.89,16.67 10.17,16.42C7.95,16.17 5.62,15.31 5.62,11.5C5.62,10.39 6,9.5 6.65,8.79C6.55,8.54 6.2,7.5 6.75,6.15C6.75,6.15 7.59,5.88 9.5,7.17C10.29,6.95 11.15,6.84 12,6.84C12.85,6.84 13.71,6.95 14.5,7.17C16.41,5.88 17.25,6.15 17.25,6.15C17.8,7.5 17.45,8.54 17.35,8.79C18,9.5 18.38,10.39 18.38,11.5C18.38,15.32 16.04,16.16 13.81,16.41C14.17,16.72 14.5,17.33 14.5,18.26C14.5,19.6 14.5,20.68 14.5,21C14.5,21.27 14.66,21.59 15.17,21.5C19.14,20.16 22,16.42 22,12A10,10 0 0,0 12,2Z"/>
                        </svg>
                        <a href="${plugin.metadata.repository}" target="_blank">${plugin.metadata.repository}</a>
                    </p>
                </div>
            ` : plugin.github_repo ? `
                <div class="mb-3">
                    <h6>Repository</h6>
                    <p><a href="https://github.com/${plugin.github_repo}" target="_blank">${plugin.github_repo}</a></p>
                </div>
            ` : ''}

            ${plugin.supported_os ? `
                <div class="mb-3">
                    <h6>Supported Platforms</h6>
                    <p>${plugin.supported_os.join(', ')}</p>
                </div>
            ` : ''}

            ${plugin.supported_arch ? `
                <div class="mb-3">
                    <h6>Supported Architectures</h6>
                    <p>${plugin.supported_arch.join(', ')}</p>
                </div>
            ` : ''}
        `;

        // Show appropriate action button
        document.getElementById('installPluginBtn').style.display = !isInstalled ? 'inline-block' : 'none';
        document.getElementById('updatePluginBtn').style.display = hasUpdate ? 'inline-block' : 'none';

        document.getElementById('installPluginBtn').onclick = () => {
            modal.hide();
            this.installPlugin(plugin.name);
        };

        document.getElementById('updatePluginBtn').onclick = () => {
            modal.hide();
            this.updatePlugin(plugin.name);
        };

        modal.show();
    }

    async installPlugin(pluginName) {
        console.log('Installing plugin:', pluginName);

        // Show confirmation dialog
        if (!confirm(`Download and install ${pluginName}?`)) {
            return;
        }

        try {
            const response = await fetch('/api/plugins/download', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ name: pluginName })
            });

            const result = await response.json();

            if (result.success) {
                alert(`Successfully installed ${pluginName}!\n\nThe plugin has been downloaded to: ${result.path}\n\nRefreshing marketplace...`);

                // Reload marketplace data to show the plugin as installed
                await this.loadMarketplaceData();
                this.render();
            } else {
                alert(`Failed to install ${pluginName}: ${result.message}`);
            }
        } catch (error) {
            console.error('Error installing plugin:', error);
            alert(`Error installing ${pluginName}: ${error.message}`);
        }
    }

    async updatePlugin(pluginName) {
        try {
            const response = await fetch(`/api/plugins/${pluginName}/update`, {
                method: 'POST',
            });

            if (response.ok) {
                const result = await response.json();
                if (result.success) {
                    alert(`Successfully updated ${pluginName} from v${result.old_version} to v${result.new_version}`);
                    await this.loadMarketplaceData();
                    this.render();
                } else {
                    alert(`Failed to update ${pluginName}: ${result.error}`);
                }
            } else {
                alert(`Failed to update ${pluginName}: ${response.statusText}`);
            }
        } catch (error) {
            console.error('Error updating plugin:', error);
            alert(`Error updating ${pluginName}: ${error.message}`);
        }
    }
}

// Initialize marketplace
const marketplace = new PluginMarketplace();

document.addEventListener('DOMContentLoaded', () => {
    marketplace.init();
});
