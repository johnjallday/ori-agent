/**
 * Marketplace Settings Module
 * Handles marketplace configuration in the Settings page
 */
class MarketplaceSettings {
    constructor() {
        this.marketplaces = [];
        this.editingId = null;
        this.modal = null;
        this.draggedItem = null;
    }

    async init() {
        await this.loadMarketplaces();
        this.setupEventListeners();
        this.render();
    }

    async loadMarketplaces() {
        try {
            const response = await fetch('/api/marketplaces');
            if (!response.ok) {
                throw new Error('Failed to load marketplaces');
            }
            const data = await response.json();
            this.marketplaces = data.marketplaces || [];
        } catch (error) {
            console.error('Error loading marketplaces:', error);
            this.showAlert('danger', 'Failed to load marketplaces: ' + error.message);
        }
    }

    setupEventListeners() {
        // Add Marketplace button
        const addBtn = document.getElementById('addMarketplaceBtn');
        if (addBtn) {
            addBtn.addEventListener('click', () => this.showAddModal());
        }

        // Test Marketplace button
        const testBtn = document.getElementById('testMarketplaceBtn');
        if (testBtn) {
            testBtn.addEventListener('click', () => this.testMarketplace());
        }

        // Save Marketplace button
        const saveBtn = document.getElementById('saveMarketplaceBtn');
        if (saveBtn) {
            saveBtn.addEventListener('click', () => this.saveMarketplace());
        }

        // Initialize modal
        const modalEl = document.getElementById('marketplaceModal');
        if (modalEl) {
            this.modal = new bootstrap.Modal(modalEl);
        }
    }

    render() {
        const container = document.getElementById('marketplacesList');
        if (!container) return;

        if (this.marketplaces.length === 0) {
            container.innerHTML = `
                <div class="text-center py-3" style="color: var(--text-secondary);">
                    No marketplaces configured.
                </div>`;
            return;
        }

        container.innerHTML = this.marketplaces.map((mp, index) => `
            <div class="marketplace-item d-flex align-items-center p-3 mb-2"
                 data-id="${mp.id}"
                 draggable="${!mp.is_official}"
                 style="background: var(--bg-secondary); border-radius: var(--radius-md); cursor: ${mp.is_official ? 'default' : 'grab'};">
                <div class="drag-handle me-3" style="${mp.is_official ? 'visibility: hidden;' : 'cursor: grab;'}">
                    <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" style="opacity: 0.5;">
                        <path d="M3,15H21V13H3V15M3,19H21V17H3V19M3,11H21V9H3V11M3,5V7H21V5H3Z"/>
                    </svg>
                </div>
                <div class="flex-grow-1">
                    <div class="d-flex align-items-center flex-wrap gap-2">
                        <strong style="color: var(--text-primary);">${this.escapeHtml(mp.name)}</strong>
                        ${mp.is_official ? '<span class="badge bg-primary">Official</span>' : ''}
                        ${!mp.enabled ? '<span class="badge bg-secondary">Disabled</span>' : ''}
                        ${mp.last_error ? '<span class="badge bg-danger" title="' + this.escapeHtml(mp.last_error) + '">Error</span>' : ''}
                    </div>
                    <small style="color: var(--text-secondary);">
                        ${this.escapeHtml(mp.source)}
                        ${mp.source_type === 'github' ? ' <span class="badge bg-dark">GitHub</span>' : mp.source_type === 'file' ? ' <span class="badge bg-info text-dark">Local File</span>' : ''}
                    </small>
                </div>
                <div class="form-check form-switch me-3">
                    <input class="form-check-input" type="checkbox" role="switch"
                           id="mpEnabled${mp.id}"
                           ${mp.enabled ? 'checked' : ''}
                           onchange="marketplaceSettings.toggleEnabled('${mp.id}', this.checked)">
                    <label class="form-check-label visually-hidden" for="mpEnabled${mp.id}">Enable/Disable</label>
                </div>
                ${!mp.is_official ? `
                    <button class="btn btn-sm btn-outline-danger"
                            onclick="marketplaceSettings.deleteMarketplace('${mp.id}')"
                            title="Remove marketplace">
                        <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
                            <path d="M19,4H15.5L14.5,3H9.5L8.5,4H5V6H19M6,19A2,2 0 0,0 8,21H16A2,2 0 0,0 18,19V7H6V19Z"/>
                        </svg>
                    </button>
                ` : ''}
            </div>
        `).join('');

        // Setup drag-and-drop
        this.setupDragAndDrop();
    }

    setupDragAndDrop() {
        const container = document.getElementById('marketplacesList');
        if (!container) return;

        const items = container.querySelectorAll('.marketplace-item[draggable="true"]');

        items.forEach(item => {
            item.addEventListener('dragstart', (e) => {
                this.draggedItem = item;
                item.style.opacity = '0.5';
                e.dataTransfer.effectAllowed = 'move';
            });

            item.addEventListener('dragend', () => {
                item.style.opacity = '1';
                this.draggedItem = null;
            });

            item.addEventListener('dragover', (e) => {
                e.preventDefault();
                e.dataTransfer.dropEffect = 'move';
            });

            item.addEventListener('drop', async (e) => {
                e.preventDefault();
                if (!this.draggedItem || this.draggedItem === item) return;

                // Reorder items in DOM
                const allItems = [...container.querySelectorAll('.marketplace-item')];
                const draggedIndex = allItems.indexOf(this.draggedItem);
                const dropIndex = allItems.indexOf(item);

                if (draggedIndex < dropIndex) {
                    item.parentNode.insertBefore(this.draggedItem, item.nextSibling);
                } else {
                    item.parentNode.insertBefore(this.draggedItem, item);
                }

                // Get new order
                const newOrder = [...container.querySelectorAll('.marketplace-item')]
                    .map(el => el.dataset.id);

                await this.handleReorder(newOrder);
            });
        });
    }

    async handleReorder(ids) {
        try {
            const response = await fetch('/api/marketplaces/reorder', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ ids })
            });

            if (!response.ok) {
                throw new Error('Failed to reorder marketplaces');
            }

            // Reload to get updated order
            await this.loadMarketplaces();
            this.render();
        } catch (error) {
            console.error('Error reordering marketplaces:', error);
            this.showAlert('danger', 'Failed to reorder: ' + error.message);
            // Re-render to restore original order
            this.render();
        }
    }

    showAddModal() {
        this.editingId = null;
        document.getElementById('marketplaceNameInput').value = '';
        document.getElementById('marketplaceSourceInput').value = '';
        document.getElementById('marketplaceTestResult').style.display = 'none';
        document.getElementById('saveMarketplaceBtn').disabled = true;
        document.getElementById('marketplaceModalLabel').textContent = 'Add Marketplace';
        this.modal.show();
    }

    async testMarketplace() {
        const source = document.getElementById('marketplaceSourceInput').value.trim();
        const resultDiv = document.getElementById('marketplaceTestResult');
        const saveBtn = document.getElementById('saveMarketplaceBtn');

        if (!source) {
            resultDiv.style.display = 'block';
            resultDiv.innerHTML = `
                <div class="alert alert-warning mb-0 mt-3">
                    Please enter a source URL or GitHub repo.
                </div>`;
            return;
        }

        resultDiv.style.display = 'block';
        resultDiv.innerHTML = `
            <div class="d-flex align-items-center mt-3" style="color: var(--text-secondary);">
                <div class="spinner-border spinner-border-sm me-2"></div>
                Testing connection...
            </div>`;

        try {
            const response = await fetch('/api/marketplaces/test', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ source })
            });

            const result = await response.json();

            if (result.valid) {
                const typeLabel = result.source_type === 'github'
                    ? 'GitHub Repository'
                    : result.source_type === 'gitlab'
                    ? 'GitLab URL'
                    : result.source_type === 'bitbucket'
                    ? 'Bitbucket URL'
                    : result.source_type === 'file'
                    ? 'Local File'
                    : 'Direct URL';
                resultDiv.innerHTML = `
                    <div class="alert alert-success mb-0 mt-3">
                        <strong>Valid marketplace!</strong><br>
                        <small>Found ${result.plugin_count} plugin(s)</small><br>
                        <small class="text-muted">Type: ${typeLabel}</small>
                    </div>`;
                saveBtn.disabled = false;
            } else {
                resultDiv.innerHTML = `
                    <div class="alert alert-danger mb-0 mt-3">
                        <strong>Invalid marketplace</strong><br>
                        <small>${this.escapeHtml(result.error)}</small>
                    </div>`;
                saveBtn.disabled = true;
            }
        } catch (error) {
            resultDiv.innerHTML = `
                <div class="alert alert-danger mb-0 mt-3">
                    <strong>Connection failed</strong><br>
                    <small>${this.escapeHtml(error.message)}</small>
                </div>`;
            saveBtn.disabled = true;
        }
    }

    async saveMarketplace() {
        const name = document.getElementById('marketplaceNameInput').value.trim();
        const source = document.getElementById('marketplaceSourceInput').value.trim();

        if (!name || !source) {
            this.showAlert('warning', 'Please fill in all fields');
            return;
        }

        try {
            const response = await fetch('/api/marketplaces', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ name, source })
            });

            if (!response.ok) {
                const error = await response.text();
                throw new Error(error);
            }

            this.modal.hide();
            this.showAlert('success', 'Marketplace added successfully');
            const marketplacesResp = await fetch('/api/marketplaces');
            if (marketplacesResp.ok) {
                const marketplacesData = await marketplacesResp.json();
                const matches = (marketplacesData.marketplaces || []).filter(mp => mp.name === name && mp.source === source);
                if (matches.length > 0) {
                    const newest = matches.reduce((latest, mp) => (mp.order > latest.order ? mp : latest), matches[0]);
                    await fetch(`/api/marketplaces/${newest.id}/refresh`, { method: 'POST' });
                }
            }
            await this.loadMarketplaces();
            this.render();
        } catch (error) {
            console.error('Error saving marketplace:', error);
            this.showAlert('danger', 'Failed to add marketplace: ' + error.message);
        }
    }

    async toggleEnabled(id, enabled) {
        try {
            const response = await fetch(`/api/marketplaces/${id}`, {
                method: 'PUT',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ enabled })
            });

            if (!response.ok) {
                throw new Error('Failed to update marketplace');
            }

            await this.loadMarketplaces();
            this.render();
        } catch (error) {
            console.error('Error toggling marketplace:', error);
            this.showAlert('danger', 'Failed to update: ' + error.message);
            // Re-render to restore original state
            this.render();
        }
    }

    async deleteMarketplace(id) {
        if (!confirm('Remove this marketplace? Plugins from this source will no longer appear in the marketplace.')) {
            return;
        }

        try {
            const response = await fetch(`/api/marketplaces/${id}`, {
                method: 'DELETE'
            });

            if (!response.ok) {
                const error = await response.text();
                throw new Error(error);
            }

            this.showAlert('success', 'Marketplace removed');
            await this.loadMarketplaces();
            this.render();
        } catch (error) {
            console.error('Error deleting marketplace:', error);
            this.showAlert('danger', 'Failed to remove: ' + error.message);
        }
    }

    showAlert(type, message) {
        const container = document.getElementById('marketplaceAlerts');
        if (!container) return;

        const alertId = 'alert-' + Date.now();
        container.innerHTML = `
            <div id="${alertId}" class="alert alert-${type} alert-dismissible fade show" role="alert">
                ${this.escapeHtml(message)}
                <button type="button" class="btn-close" data-bs-dismiss="alert"></button>
            </div>`;

        // Auto-dismiss after 5 seconds
        setTimeout(() => {
            const alert = document.getElementById(alertId);
            if (alert) {
                alert.remove();
            }
        }, 5000);
    }

    escapeHtml(text) {
        const div = document.createElement('div');
        div.textContent = text;
        return div.innerHTML;
    }
}

// Global instance
const marketplaceSettings = new MarketplaceSettings();

// Initialize on page load
document.addEventListener('DOMContentLoaded', () => {
    marketplaceSettings.init();
});
