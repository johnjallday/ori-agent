// Plugin Health Module
// Handles fetching and displaying plugin health status

class PluginHealthManager {
    constructor() {
        this.healthData = null;
        this.refreshInterval = null;
    }

    /**
     * Initialize the health manager
     */
    async init() {
        await this.refreshHealth();
        // Refresh health data every 30 seconds
        this.refreshInterval = setInterval(() => this.refreshHealth(), 30000);
    }

    /**
     * Fetch health data from API
     */
    async refreshHealth() {
        try {
            const response = await fetch('/api/plugins/health');
            if (!response.ok) {
                throw new Error('Failed to fetch plugin health');
            }
            this.healthData = await response.json();
            this.renderHealthDashboard();
        } catch (error) {
            console.error('Error fetching plugin health:', error);
        }
    }

    /**
     * Render the health dashboard in the sidebar
     */
    renderHealthDashboard() {
        const container = document.getElementById('pluginHealthDashboard');
        if (!container || !this.healthData) return;

        const plugins = this.healthData.plugins || [];

        if (plugins.length === 0) {
            container.innerHTML = '<p class="small mb-0" style="color: var(--text-muted);">No plugins loaded</p>';
            return;
        }

        // Count plugins by status
        const statusCounts = {
            healthy: 0,
            degraded: 0,
            unhealthy: 0
        };

        plugins.forEach(plugin => {
            if (statusCounts.hasOwnProperty(plugin.status)) {
                statusCounts[plugin.status]++;
            }
        });

        // Build the HTML
        let html = `
            <div class="health-summary mb-3">
                <div class="d-flex justify-content-between align-items-center mb-2">
                    <span class="small" style="color: var(--text-muted);">Status Overview</span>
                    <button class="btn btn-link btn-sm p-0" style="color: var(--text-muted);" onclick="pluginHealth.toggleExpanded()" id="healthToggleBtn">
                        <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor">
                            <path d="M7.41,8.58L12,13.17L16.59,8.58L18,10L12,16L6,10L7.41,8.58Z"/>
                        </svg>
                    </button>
                </div>
                <div class="d-flex gap-2 flex-wrap">
        `;

        if (statusCounts.healthy > 0) {
            html += `
                <div class="badge bg-success-subtle text-success d-flex align-items-center gap-1">
                    <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor">
                        <path d="M21,7L9,19L3.5,13.5L4.91,12.09L9,16.17L19.59,5.59L21,7Z"/>
                    </svg>
                    ${statusCounts.healthy} Healthy
                </div>
            `;
        }

        if (statusCounts.degraded > 0) {
            html += `
                <div class="badge bg-warning-subtle text-warning d-flex align-items-center gap-1">
                    <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor">
                        <path d="M13,14H11V10H13M13,18H11V16H13M1,21H23L12,2L1,21Z"/>
                    </svg>
                    ${statusCounts.degraded} Degraded
                </div>
            `;
        }

        if (statusCounts.unhealthy > 0) {
            html += `
                <div class="badge bg-danger-subtle text-danger d-flex align-items-center gap-1">
                    <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor">
                        <path d="M19,6.41L17.59,5L12,10.59L6.41,5L5,6.41L10.59,12L5,17.59L6.41,19L12,13.41L17.59,19L19,17.59L13.41,12L19,6.41Z"/>
                    </svg>
                    ${statusCounts.unhealthy} Unhealthy
                </div>
            `;
        }

        html += `
                </div>
            </div>
            <div id="healthPluginList" class="health-plugin-list" style="display: none;">
        `;

        // Render individual plugin health cards
        plugins.forEach(plugin => {
            const statusIcon = this.getStatusIcon(plugin.status);
            const statusColor = this.getStatusColor(plugin.status);
            const successRate = plugin.call_success_rate?.toFixed(1) || 'N/A';
            const avgResponseTime = plugin.avg_response_time ? this.formatDuration(plugin.avg_response_time) : 'N/A';

            html += `
                <div class="health-plugin-card mb-2 p-2 border rounded" style="border-color: ${statusColor}20 !important; background: ${statusColor}05;">
                    <div class="d-flex align-items-start justify-content-between mb-1">
                        <div class="d-flex align-items-center gap-2">
                            ${statusIcon}
                            <div>
                                <div class="fw-semibold small d-flex align-items-center gap-1">
                                    ${plugin.name}
                                    ${plugin.update_available ? `
                                        <span class="badge bg-info-subtle text-info" style="font-size: 0.6rem;">
                                            <svg width="10" height="10" viewBox="0 0 24 24" fill="currentColor" style="margin-right: 2px;">
                                                <path d="M12,18.17L8.83,15L7.42,16.41L12,21L16.59,16.41L15.17,15M12,5.83L15.17,9L16.58,7.59L12,3L7.41,7.59L8.83,9L12,5.83Z"/>
                                            </svg>
                                            Update
                                        </span>
                                    ` : ''}
                                </div>
                                <div style="font-size: 0.7rem; color: var(--text-muted);">
                                    ${plugin.version || 'unknown'}
                                    ${plugin.update_available && plugin.latest_version ? ` → v${plugin.latest_version}` : ''}
                                </div>
                            </div>
                        </div>
                    </div>
            `;

            // Show stats if available
            if (plugin.total_calls > 0) {
                html += `
                    <div class="health-stats mt-2 pt-2 border-top" style="font-size: 0.7rem;">
                        <div class="d-flex justify-content-between" style="color: var(--text-muted);">
                            <span>Success Rate</span>
                            <span class="fw-semibold">${successRate}%</span>
                        </div>
                        <div class="d-flex justify-content-between" style="color: var(--text-muted);">
                            <span>Avg Response</span>
                            <span class="fw-semibold">${avgResponseTime}</span>
                        </div>
                        <div class="d-flex justify-content-between" style="color: var(--text-muted);">
                            <span>Calls</span>
                            <span class="fw-semibold">${plugin.total_calls}</span>
                        </div>
                    </div>
                `;
            }
            }

            // Show errors/warnings if any
            if (plugin.errors && plugin.errors.length > 0) {
                html += `
                    <div class="alert alert-danger p-2 mb-0 mt-2" style="font-size: 0.7rem;">
                        ${plugin.errors.join('<br>')}
                    </div>
                `;
            } else if (plugin.warnings && plugin.warnings.length > 0) {
                html += `
                    <div class="alert alert-warning p-2 mb-0 mt-2" style="font-size: 0.7rem;">
                        ${plugin.warnings.join('<br>')}
                    </div>
                `;
            }

            // Show update recommendation if available
            if (plugin.update_available && plugin.update_recommendation) {
                html += `
                    <div class="alert alert-info p-2 mb-0 mt-2" style="font-size: 0.7rem;">
                        <div class="d-flex align-items-start gap-2">
                            <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" style="flex-shrink: 0; margin-top: 1px;">
                                <path d="M13,9H11V7H13M13,17H11V11H13M12,2A10,10 0 0,0 2,12A10,10 0 0,0 12,22A10,10 0 0,0 22,12A10,10 0 0,0 12,2Z"/>
                            </svg>
                            <div style="flex: 1;">
                                <strong>Update Available</strong><br>
                                ${plugin.update_recommendation}
                            </div>
                        </div>
                    </div>
                `;
            }

            html += `</div>`;
        });

        html += `</div>`;

        container.innerHTML = html;
    }

    /**
     * Toggle expanded view
     */
    toggleExpanded() {
        const list = document.getElementById('healthPluginList');
        const btn = document.getElementById('healthToggleBtn');

        if (!list || !btn) return;

        if (list.style.display === 'none') {
            list.style.display = 'block';
            btn.innerHTML = `
                <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor">
                    <path d="M7.41,15.41L12,10.83L16.59,15.41L18,14L12,8L6,14L7.41,15.41Z"/>
                </svg>
            `;
        } else {
            list.style.display = 'none';
            btn.innerHTML = `
                <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor">
                    <path d="M7.41,8.58L12,13.17L16.59,8.58L18,10L12,16L6,10L7.41,8.58Z"/>
                </svg>
            `;
        }
    }

    /**
     * Get status icon SVG
     */
    getStatusIcon(status) {
        switch (status) {
            case 'healthy':
                return `<svg width="16" height="16" viewBox="0 0 24 24" fill="#28a745">
                    <path d="M21,7L9,19L3.5,13.5L4.91,12.09L9,16.17L19.59,5.59L21,7Z"/>
                </svg>`;
            case 'degraded':
                return `<svg width="16" height="16" viewBox="0 0 24 24" fill="#ffc107">
                    <path d="M13,14H11V10H13M13,18H11V16H13M1,21H23L12,2L1,21Z"/>
                </svg>`;
            case 'unhealthy':
                return `<svg width="16" height="16" viewBox="0 0 24 24" fill="#dc3545">
                    <path d="M19,6.41L17.59,5L12,10.59L6.41,5L5,6.41L10.59,12L5,17.59L6.41,19L12,13.41L17.59,19L19,17.59L13.41,12L19,6.41Z"/>
                </svg>`;
            default:
                return `<svg width="16" height="16" viewBox="0 0 24 24" fill="#6c757d">
                    <path d="M13,9H11V7H13M13,17H11V11H13M12,2A10,10 0 0,0 2,12A10,10 0 0,0 12,22A10,10 0 0,0 22,12A10,10 0 0,0 12,2Z"/>
                </svg>`;
        }
    }

    /**
     * Get status color
     */
    getStatusColor(status) {
        switch (status) {
            case 'healthy':
                return '#28a745';
            case 'degraded':
                return '#ffc107';
            case 'unhealthy':
                return '#dc3545';
            default:
                return '#6c757d';
        }
    }

    /**
     * Format duration in nanoseconds to human readable
     */
    formatDuration(nanos) {
        if (!nanos) return 'N/A';

        const ms = nanos / 1000000;

        if (ms < 1) {
            return '<1ms';
        } else if (ms < 1000) {
            return `${Math.round(ms)}ms`;
        } else {
            return `${(ms / 1000).toFixed(2)}s`;
        }
    }

    /**
     * Cleanup
     */
    destroy() {
        if (this.refreshInterval) {
            clearInterval(this.refreshInterval);
        }
    }
}

// Create global instance
const pluginHealth = new PluginHealthManager();

// Initialize on page load
document.addEventListener('DOMContentLoaded', () => {
    pluginHealth.init();
});
