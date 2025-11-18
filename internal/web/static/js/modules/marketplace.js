// Plugin Marketplace Module
// Handles plugin browsing, installation, and updates

class PluginMarketplace {
    constructor() {
        this.plugins = [];
        this.installedPlugins = new Set();
        this.updates = [];
        this.filter = 'all';
        this.searchTerm = '';
        this.currentPlatform = '';
        this.currentPlatformDisplay = '';
        this.showIncompatible = false; // Track compatibility filter state
    }

    async init() {
        // Get current platform information from hidden inputs
        this.currentPlatform = document.getElementById('currentPlatform')?.value || '';
        this.currentPlatformDisplay = document.getElementById('currentPlatformDisplay')?.value || '';

        // Restore compatibility filter state from localStorage
        const savedState = localStorage.getItem('showIncompatiblePlugins');
        this.showIncompatible = savedState === 'true';

        // Load marketplace data
        await this.loadMarketplaceData();

        // Setup event listeners
        this.setupEventListeners();

        // Set initial toggle state
        const toggle = document.getElementById('showIncompatibleToggle');
        if (toggle) {
            toggle.checked = this.showIncompatible;
        }

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
            const installedPluginNames = new Set();
            (installedData.plugins || []).forEach(plugin => {
                this.getPluginNameVariants(plugin).forEach(variant => {
                    if (variant) {
                        installedPluginNames.add(variant);
                    }
                });
            });

            // Mark plugins as installed if they exist locally
            this.installedPlugins = installedPluginNames;
            this.plugins.forEach(plugin => {
                const variants = this.getPluginNameVariants(plugin);
                plugin.installed = variants.some(variant => installedPluginNames.has(variant));
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

        // Platform compatibility toggle
        document.getElementById('showIncompatibleToggle')?.addEventListener('change', (e) => {
            this.showIncompatible = e.target.checked;
            // Save state to localStorage
            localStorage.setItem('showIncompatiblePlugins', this.showIncompatible.toString());
            this.render();
        });
    }

    // Check if plugin is compatible with current platform
    isPluginCompatible(plugin) {
        if (!this.currentPlatform) {
            return true; // If we can't detect platform, assume compatible
        }

        // Check platforms array first
        if (plugin.platforms && plugin.platforms.length > 0) {
            // If platforms contains "unknown", fall through to OS/arch check
            if (plugin.platforms.length === 1 && plugin.platforms[0] === 'unknown') {
                // Fall through to OS/arch check below
            } else {
                // Check if current platform is in the platforms array
                return plugin.platforms.includes(this.currentPlatform) || plugin.platforms.includes('all');
            }
        }

        // Fallback to checking supported_os and supported_arch
        const [os, arch] = this.currentPlatform.split('-');

        // If no OS support specified, assume compatible
        if (!plugin.supported_os || plugin.supported_os.length === 0) {
            return true;
        }

        // Check OS compatibility
        const osCompatible = plugin.supported_os.includes(os) || plugin.supported_os.includes('all');

        // If no arch support specified, just check OS
        if (!plugin.supported_arch || plugin.supported_arch.length === 0) {
            return osCompatible;
        }

        // Check arch compatibility
        const archCompatible = plugin.supported_arch.includes(arch) || plugin.supported_arch.includes('all');

        return osCompatible && archCompatible;
    }

    // Get platform icon badges for a plugin
    getPlatformBadges(plugin) {
        const badges = [];
        const platformIcons = {
            'darwin': '🍎',
            'linux': '🐧',
            'windows': '🪟',
            'freebsd': '🐡'
        };

        // Use supported_os if available
        if (plugin.supported_os && plugin.supported_os.length > 0) {
            plugin.supported_os.forEach(os => {
                if (os !== 'all' && platformIcons[os]) {
                    const archInfo = plugin.supported_arch && plugin.supported_arch.length > 0
                        ? ` (${plugin.supported_arch.filter(a => a !== 'all').join(', ')})`
                        : '';
                    badges.push(`<span class="badge bg-secondary me-1" title="${os}${archInfo}">${platformIcons[os]} ${os}</span>`);
                }
            });
        }

        // If "all" OS or no OS specified, show "All Platforms"
        if (!plugin.supported_os || plugin.supported_os.length === 0 || plugin.supported_os.includes('all')) {
            badges.push('<span class="badge bg-secondary me-1">All Platforms</span>');
        }

        return badges.join('');
    }

    // Get compatibility indicator for a plugin
    getCompatibilityIndicator(plugin) {
        const compatible = this.isPluginCompatible(plugin);

        if (compatible) {
            return {
                compatible: true,
                badge: '<span class="badge bg-success-subtle text-success me-1" title="Compatible with your platform">✅ Compatible</span>',
                cssClass: 'compatible'
            };
        } else {
            return {
                compatible: false,
                badge: '<span class="badge bg-warning-subtle text-warning me-1" title="Not available for your platform">⚠️ Not Available</span>',
                cssClass: 'incompatible'
            };
        }
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

            // Platform compatibility filter
            if (!this.showIncompatible && !this.isPluginCompatible(plugin)) {
                return false;
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
            const compatibility = this.getCompatibilityIndicator(plugin);
            const platformBadges = this.getPlatformBadges(plugin);

            html += `
                <div class="col-md-6 col-lg-4 mb-4" data-compatible="${compatibility.compatible}">
                    <div class="card h-100 plugin-card ${compatibility.cssClass}" style="cursor: pointer; ${!compatibility.compatible ? 'opacity: 0.7;' : ''}" onclick="marketplace.showPluginDetails('${plugin.name}')">
                        <div class="card-body">
                            <div class="d-flex justify-content-between align-items-start mb-2">
                                <h5 class="card-title mb-0">${plugin.name}</h5>
                                <div class="d-flex flex-column align-items-end gap-1">
                                    ${isInstalled ? `
                                        <span class="badge bg-success-subtle text-success">Installed</span>
                                    ` : ''}
                                    ${hasUpdate ? `
                                        <span class="badge bg-warning-subtle text-warning">Update</span>
                                    ` : ''}
                                </div>
                            </div>

                            <p class="card-text text-muted small">${plugin.description || 'No description available'}</p>

                            <!-- Platform badges -->
                            ${platformBadges ? `
                                <div class="mb-2">
                                    ${platformBadges}
                                </div>
                            ` : ''}

                            <!-- Compatibility indicator -->
                            <div class="mb-2">
                                ${compatibility.badge}
                            </div>

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
                            ${!isInstalled && !compatibility.compatible ? `
                                <button class="btn btn-sm btn-outline-secondary w-100" disabled title="Not compatible with ${this.currentPlatformDisplay}">
                                    <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" class="me-1">
                                        <path d="M12,2C17.53,2 22,6.47 22,12C22,17.53 17.53,22 12,22C6.47,22 2,17.53 2,12C2,6.47 6.47,2 12,2M15.59,7L12,10.59L8.41,7L7,8.41L10.59,12L7,15.59L8.41,17L12,13.41L15.59,17L17,15.59L13.41,12L17,8.41L15.59,7Z"/>
                                    </svg>
                                    Not Available
                                </button>
                            ` : !isInstalled ? `
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

        // Check compatibility before installation
        const plugin = this.plugins.find(p => p.name === pluginName);
        if (plugin && !this.isPluginCompatible(plugin)) {
            this.showPlatformIncompatibleModal(plugin);
            return;
        }

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

            // Handle platform incompatibility error from backend
            if (!response.ok && result.error === 'platform_incompatible') {
                this.showPlatformIncompatibleModal(plugin, result);
                return;
            }

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

    // Show platform incompatibility modal (Task 6)
    showPlatformIncompatibleModal(plugin, errorData = null) {
        const modal = new bootstrap.Modal(document.getElementById('platformIncompatibleModal') || this.createPlatformIncompatibleModal());

        const supportedPlatforms = errorData?.supported_platforms || plugin.platforms || [];
        const supportedOS = errorData?.supported_os || plugin.supported_os || [];
        const userPlatform = errorData?.user_platform || this.currentPlatform;

        const platformList = supportedPlatforms.length > 0
            ? supportedPlatforms.map(p => `<li>${this.formatPlatformName(p)}</li>`).join('')
            : supportedOS.map(os => `<li>${os}</li>`).join('');

        document.getElementById('platformIncompatibleModalBody').innerHTML = `
            <div class="alert alert-warning">
                <h6>Plugin Not Available for Your Platform</h6>
                <p class="mb-0">This plugin is not compatible with <strong>${this.currentPlatformDisplay}</strong> (${userPlatform}).</p>
            </div>

            <h6>Supported Platforms:</h6>
            <ul>
                ${platformList}
            </ul>

            <h6>What you can do:</h6>
            <ul>
                <li>Contact the plugin maintainer to request support for your platform</li>
                <li>Build the plugin manually from source if available</li>
                <li>Look for similar plugins that support your platform</li>
            </ul>
        `;

        modal.show();
    }

    // Helper to create platform incompatibility modal if it doesn't exist
    createPlatformIncompatibleModal() {
        const modalHtml = `
            <div class="modal fade" id="platformIncompatibleModal" tabindex="-1">
                <div class="modal-dialog">
                    <div class="modal-content">
                        <div class="modal-header">
                            <h5 class="modal-title">⚠️ Plugin Not Available</h5>
                            <button type="button" class="btn-close" data-bs-dismiss="modal"></button>
                        </div>
                        <div class="modal-body" id="platformIncompatibleModalBody">
                            <!-- Content populated by showPlatformIncompatibleModal -->
                        </div>
                        <div class="modal-footer">
                            <button type="button" class="btn btn-secondary" data-bs-dismiss="modal">Close</button>
                        </div>
                    </div>
                </div>
            </div>
        `;
        document.body.insertAdjacentHTML('beforeend', modalHtml);
        return document.getElementById('platformIncompatibleModal');
    }

    // Helper to format platform names for display
    formatPlatformName(platform) {
        const names = {
            'darwin-amd64': 'macOS (Intel)',
            'darwin-arm64': 'macOS (Apple Silicon)',
            'linux-amd64': 'Linux (x86_64)',
            'linux-arm64': 'Linux (ARM64)',
            'windows-amd64': 'Windows (x86_64)',
            'windows-arm64': 'Windows (ARM64)',
            'freebsd-amd64': 'FreeBSD (x86_64)',
            'freebsd-arm64': 'FreeBSD (ARM64)'
        };
        return names[platform] || platform;
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

    // Get possible name variants for matching installed plugins (handles -0.0.x suffixes)
    getPluginNameVariants(plugin) {
        const variants = new Set();
        const addVariant = (name) => {
            if (!name || typeof name !== 'string') {
                return;
            }
            const normalized = name.toLowerCase();
            if (!normalized) {
                return;
            }
            variants.add(normalized);

            // Strip trailing version (e.g., "-0.0.8" or "-0.0.8-alpha")
            const versionStripped = normalized.replace(/-\d+\.\d+\.\d+(?:[-+][\w\.]+)?$/, '');
            if (versionStripped && versionStripped !== normalized) {
                variants.add(versionStripped);
            }
        };

        addVariant(plugin.name);
        if (plugin.metadata?.name) {
            addVariant(plugin.metadata.name);
        }
        if (plugin.definition?.name) {
            addVariant(plugin.definition.name);
        }

        return Array.from(variants);
    }
}

const marketplace = new PluginMarketplace();

document.addEventListener('DOMContentLoaded', () => {
    marketplace.init();
});
