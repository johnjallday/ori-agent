// API Key Management Module
class APIKeyManager {
    constructor() {
        this.modal = null;
        this.apiKeyInput = null;
        this.toggleVisibilityBtn = null;
        this.saveBtn = null;
        this.editBtn = null;
        this.apiKeyMaskedSpan = null;
        
        this.init();
    }

    init() {
        // Get modal and elements
        this.modal = new bootstrap.Modal(document.getElementById('apiKeyModal'));
        this.apiKeyInput = document.getElementById('apiKeyInput');
        this.toggleVisibilityBtn = document.getElementById('toggleApiKeyVisibility');
        this.saveBtn = document.getElementById('saveApiKeyBtn');
        this.editBtn = document.getElementById('editApiKeyBtn');
        this.apiKeyMaskedSpan = document.getElementById('apiKeyMasked');
        
        // Bind events
        this.bindEvents();
        
        // Load current API key on startup
        this.loadAPIKey();
    }

    bindEvents() {
        // Edit button click
        if (this.editBtn) {
            this.editBtn.addEventListener('click', () => {
                this.openModal();
            });
        }

        // Save button click
        if (this.saveBtn) {
            this.saveBtn.addEventListener('click', () => {
                this.saveAPIKey();
            });
        }

        // Toggle visibility button
        if (this.toggleVisibilityBtn) {
            this.toggleVisibilityBtn.addEventListener('click', () => {
                this.togglePasswordVisibility();
            });
        }

        // Form submission
        const form = document.getElementById('apiKeyForm');
        if (form) {
            form.addEventListener('submit', (e) => {
                e.preventDefault();
                this.saveAPIKey();
            });
        }

        // Modal events
        const modalElement = document.getElementById('apiKeyModal');
        if (modalElement) {
            modalElement.addEventListener('shown.bs.modal', () => {
                if (this.apiKeyInput) {
                    this.apiKeyInput.focus();
                }
            });
        }
    }

    async loadAPIKey() {
        try {
            const response = await fetch('/api/api-key');
            if (!response.ok) {
                throw new Error('Failed to load API key');
            }
            
            const data = await response.json();
            
            // Update the masked display
            if (this.apiKeyMaskedSpan) {
                this.apiKeyMaskedSpan.textContent = data.masked || 'No API key set';
            }
            
        } catch (error) {
            console.error('Error loading API key:', error);
            if (this.apiKeyMaskedSpan) {
                this.apiKeyMaskedSpan.textContent = 'Error loading API key';
            }
        }
    }

    openModal() {
        // Clear the input when opening modal for security
        if (this.apiKeyInput) {
            this.apiKeyInput.value = '';
            this.apiKeyInput.type = 'password';
        }
        
        // Reset eye icon
        this.updateEyeIcon(false);
        
        if (this.modal) {
            this.modal.show();
        }
    }

    async saveAPIKey() {
        const apiKey = this.apiKeyInput?.value?.trim() || '';
        
        try {
            // Show loading state
            if (this.saveBtn) {
                this.saveBtn.disabled = true;
                this.saveBtn.textContent = 'Saving...';
            }
            
            const response = await fetch('/api/api-key', {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json',
                },
                body: JSON.stringify({
                    api_key: apiKey
                })
            });

            if (!response.ok) {
                const errorText = await response.text();
                throw new Error(errorText || 'Failed to save API key');
            }

            // Success - close modal and refresh display
            if (this.modal) {
                this.modal.hide();
            }
            
            // Reload the API key display
            await this.loadAPIKey();
            
            // Show success message (optional)
            this.showNotification('API key updated successfully', 'success');
            
        } catch (error) {
            console.error('Error saving API key:', error);
            this.showNotification('Error saving API key: ' + error.message, 'error');
        } finally {
            // Reset button state
            if (this.saveBtn) {
                this.saveBtn.disabled = false;
                this.saveBtn.textContent = 'Save API Key';
            }
        }
    }

    togglePasswordVisibility() {
        if (!this.apiKeyInput) return;
        
        const isPassword = this.apiKeyInput.type === 'password';
        this.apiKeyInput.type = isPassword ? 'text' : 'password';
        this.updateEyeIcon(!isPassword);
    }

    updateEyeIcon(isVisible) {
        const eyeIcon = document.getElementById('eyeIcon');
        if (!eyeIcon) return;
        
        if (isVisible) {
            // Eye slash icon (hide)
            eyeIcon.innerHTML = '<path d="M2,5.27L3.28,4L20,20.72L18.73,22L15.65,18.92C14.5,19.3 13.28,19.5 12,19.5C7,19.5 2.73,16.39 1,12C1.69,10.24 2.79,8.69 4.19,7.46L2,5.27M12,9A3,3 0 0,1 15,12C15,12.35 14.94,12.69 14.83,13L11,9.17C11.31,9.06 11.65,9 12,9M12,4.5C17,4.5 21.27,7.61 23,12C22.18,14.08 20.79,15.88 19,17.19L17.58,15.76C18.94,14.82 20.06,13.54 20.82,12C19.17,8.64 15.76,6.5 12,6.5C10.91,6.5 9.84,6.68 8.84,7L7.3,5.47C8.74,4.85 10.33,4.5 12,4.5Z"/>';
        } else {
            // Eye icon (show)
            eyeIcon.innerHTML = '<path d="M12,9A3,3 0 0,0 9,12A3,3 0 0,0 12,15A3,3 0 0,0 15,12A3,3 0 0,0 12,9M12,17A5,5 0 0,1 7,12A5,5 0 0,1 12,7A5,5 0 0,1 17,12A5,5 0 0,1 12,17M12,4.5C7,4.5 2.73,7.61 1,12C2.73,16.39 7,19.5 12,19.5C17,19.5 21.27,16.39 23,12C21.27,7.61 17,4.5 12,4.5Z"/>';
        }
    }

    showNotification(message, type = 'info') {
        // Simple notification system - could be improved with a proper toast library
        const alertClass = type === 'success' ? 'alert-success' : 
                          type === 'error' ? 'alert-danger' : 'alert-info';
        
        const notification = document.createElement('div');
        notification.className = `alert ${alertClass} alert-dismissible fade show position-fixed`;
        notification.style.cssText = 'top: 20px; right: 20px; z-index: 9999; min-width: 300px;';
        notification.innerHTML = `
            ${message}
            <button type="button" class="btn-close" data-bs-dismiss="alert"></button>
        `;
        
        document.body.appendChild(notification);
        
        // Auto remove after 5 seconds
        setTimeout(() => {
            if (notification.parentNode) {
                notification.remove();
            }
        }, 5000);
    }
}

// Initialize when DOM is loaded
document.addEventListener('DOMContentLoaded', () => {
    window.apiKeyManager = new APIKeyManager();
});

// Export for module usage
if (typeof module !== 'undefined' && module.exports) {
    module.exports = APIKeyManager;
}