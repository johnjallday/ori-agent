// Agent Creation Form JavaScript

let availablePlugins = [];
let selectedTags = [];
let availableProviders = []; // Cache for available providers and models from API

// Initialize page
document.addEventListener('DOMContentLoaded', () => {
    loadPlugins();
    setupTagsInput();
    loadAvailableProviders();
});

// Fetch available providers and models from API
async function loadAvailableProviders() {
    try {
        const response = await fetch('/api/providers');
        if (!response.ok) {
            throw new Error('Failed to load providers');
        }
        const data = await response.json();
        availableProviders = data.providers || [];

        // Populate the model select with data from API
        updateModelOptions();

        return availableProviders;
    } catch (error) {
        console.error('Failed to load providers:', error);
        // Fall back to showing an error in the model select
        const modelSelect = document.getElementById('llmModel');
        if (modelSelect) {
            modelSelect.innerHTML = '<option value="">Failed to load models</option>';
        }
        return [];
    }
}

// Load available plugins
async function loadPlugins() {
    try {
        const response = await fetch('/api/plugins');

        if (!response.ok) {
            throw new Error('Failed to load plugins');
        }

        const data = await response.json();
        availablePlugins = data.plugins || [];
        renderPlugins();

    } catch (error) {
        console.error('Error loading plugins:', error);
        showError('Failed to load plugins. Some features may be unavailable.');
    }
}

// Render plugins list
function renderPlugins() {
    const container = document.getElementById('pluginsList');

    if (availablePlugins.length === 0) {
        container.innerHTML = '<div style="text-align: center; padding: 20px; color: var(--text-muted, #666);">No plugins available</div>';
        return;
    }

    container.innerHTML = '';
    availablePlugins.forEach((plugin, index) => {
        const item = document.createElement('div');
        item.className = 'plugin-item';
        item.innerHTML = `
            <input type="checkbox" id="plugin-${index}" class="plugin-checkbox" value="${escapeHtml(plugin.name)}">
            <label for="plugin-${index}" class="plugin-info" style="cursor: pointer;">
                <div class="plugin-name">${escapeHtml(plugin.name)}</div>
                ${plugin.description ? `<div class="plugin-description">${escapeHtml(plugin.description)}</div>` : ''}
            </label>
        `;
        container.appendChild(item);
    });
}

// Setup tags input
function setupTagsInput() {
    const input = document.getElementById('tagsInput');
    const container = document.getElementById('tagsContainer');

    input.addEventListener('keydown', (e) => {
        if (e.key === 'Enter' && input.value.trim()) {
            e.preventDefault();
            addTag(input.value.trim());
            input.value = '';
        } else if (e.key === 'Backspace' && !input.value && selectedTags.length > 0) {
            removeTag(selectedTags[selectedTags.length - 1]);
        }
    });
}

// Add tag
function addTag(tag) {
    if (!selectedTags.includes(tag)) {
        selectedTags.push(tag);
        renderTags();
    }
}

// Remove tag
function removeTag(tag) {
    selectedTags = selectedTags.filter(t => t !== tag);
    renderTags();
}

// Render tags
function renderTags() {
    const container = document.getElementById('tagsContainer');
    const input = document.getElementById('tagsInput');

    // Clear existing tags
    const existingTags = container.querySelectorAll('.tag-item');
    existingTags.forEach(tag => tag.remove());

    // Add tags before input
    selectedTags.forEach(tag => {
        const tagEl = document.createElement('div');
        tagEl.className = 'tag-item';
        tagEl.innerHTML = `
            ${escapeHtml(tag)}
            <span class="tag-remove" onclick="removeTag('${escapeHtml(tag)}')">×</span>
        `;
        container.insertBefore(tagEl, input);
    });
}

// Update model options based on provider and agent type
function updateModelOptions() {
    const providerSelect = document.getElementById('llmProvider');
    const modelSelect = document.getElementById('llmModel');
    const agentTypeSelect = document.getElementById('agentType');

    if (!modelSelect || availableProviders.length === 0) {
        return;
    }

    const selectedProvider = providerSelect ? providerSelect.value : null;
    const selectedAgentType = agentTypeSelect ? agentTypeSelect.value : 'tool-calling';

    // Clear existing options
    modelSelect.innerHTML = '';

    // Find models from the API data
    availableProviders.forEach(provider => {
        // Map provider display names to our provider select values
        const providerNameMap = {
            'OpenAI': 'openai',
            'Anthropic': 'claude',
            'Ollama': 'ollama'
        };
        const providerKey = providerNameMap[provider.display_name] || provider.name;

        // If a specific provider is selected, only show models from that provider
        if (selectedProvider && providerKey !== selectedProvider) {
            return;
        }

        // Create optgroup for this provider
        const providerGroup = document.createElement('optgroup');
        providerGroup.label = provider.display_name;
        let hasMatchingModels = false;

        provider.models.forEach(model => {
            // Filter by agent type if the model has a type specified
            if (model.type && model.type !== selectedAgentType) {
                return;
            }

            const option = document.createElement('option');
            option.value = model.value;
            option.textContent = model.label;
            option.setAttribute('data-provider', providerKey);
            if (model.type) {
                option.setAttribute('data-type', model.type);
            }
            providerGroup.appendChild(option);
            hasMatchingModels = true;
        });

        // Only add the provider group if it has matching models
        if (hasMatchingModels) {
            modelSelect.appendChild(providerGroup);
        }
    });

    // If no models found, show a message
    if (modelSelect.options.length === 0) {
        const option = document.createElement('option');
        option.value = '';
        option.textContent = 'No models available for this configuration';
        modelSelect.appendChild(option);
    }
}

// Create agent
async function createAgent() {
    // Validate required fields
    const name = document.getElementById('agentName').value.trim();
    if (!name) {
        showError('Agent name is required');
        return;
    }

    const type = document.getElementById('agentType').value;
    const role = document.getElementById('agentRole').value;
    const modelSelect = document.getElementById('llmModel');
    const model = modelSelect.value;

    if (!type || !role || !model) {
        showError('Please fill in all required fields');
        return;
    }

    // Get provider from the selected model's data attribute
    const selectedOption = modelSelect.options[modelSelect.selectedIndex];
    const provider = selectedOption ? selectedOption.getAttribute('data-provider') : null;

    // Gather optional fields
    const description = document.getElementById('agentDescription').value.trim();
    const temperature = parseFloat(document.getElementById('temperature').value);
    const systemPrompt = document.getElementById('systemPrompt').value.trim();
    const avatarColor = document.getElementById('avatarColor').value;

    // Get selected plugins
    const pluginCheckboxes = document.querySelectorAll('.plugin-checkbox:checked');
    const enabledPlugins = Array.from(pluginCheckboxes).map(cb => cb.value);

    // Build request
    const requestData = {
        name: name,
        type: type,
        role: role,
        model: model,
        temperature: temperature
    };

    // Add provider if we could determine it
    if (provider) {
        requestData.llm_provider = provider;
    }

    // Add optional fields
    if (description) requestData.description = description;
    if (systemPrompt) requestData.system_prompt = systemPrompt;
    if (avatarColor) requestData.avatar_color = avatarColor;
    if (selectedTags.length > 0) requestData.tags = selectedTags;
    if (enabledPlugins.length > 0) requestData.enabled_plugins = enabledPlugins;

    // Show loading
    showLoading(true);
    document.getElementById('createBtn').disabled = true;

    try {
        const response = await fetch('/api/agents', {
            method: 'POST',
            headers: {
                'Content-Type': 'application/json'
            },
            body: JSON.stringify(requestData)
        });

        if (!response.ok) {
            const error = await response.json();
            throw new Error(error.error || 'Failed to create agent');
        }

        // Success - redirect to agents page
        window.location.href = '/agents';

    } catch (error) {
        console.error('Error creating agent:', error);
        showError(error.message || 'Failed to create agent');
        showLoading(false);
        document.getElementById('createBtn').disabled = false;
    }
}

// Helper functions
function showError(message) {
    const errorEl = document.getElementById('errorMessage');
    errorEl.textContent = message;
    errorEl.style.display = 'block';

    // Scroll to error
    errorEl.scrollIntoView({ behavior: 'smooth', block: 'nearest' });

    // Auto-hide after 5 seconds
    setTimeout(() => {
        errorEl.style.display = 'none';
    }, 5000);
}

function showLoading(show) {
    document.getElementById('loadingOverlay').style.display = show ? 'flex' : 'none';
}

function escapeHtml(text) {
    const div = document.createElement('div');
    div.textContent = text;
    return div.innerHTML;
}
