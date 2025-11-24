// Agent Creation Form JavaScript

let availablePlugins = [];
let selectedTags = [];

// Model options by provider
const modelsByProvider = {
    openai: [
        { value: 'gpt-4o', label: 'GPT-4o (Recommended)' },
        { value: 'gpt-4-turbo', label: 'GPT-4 Turbo' },
        { value: 'gpt-3.5-turbo', label: 'GPT-3.5 Turbo' },
        { value: 'gpt-4.1-nano', label: 'GPT-4.1 Nano (if available)' }
    ],
    claude: [
        { value: 'claude-3-5-sonnet-20241022', label: 'Claude 3.5 Sonnet' },
        { value: 'claude-3-opus-20240229', label: 'Claude 3 Opus' },
        { value: 'claude-3-sonnet-20240229', label: 'Claude 3 Sonnet' },
        { value: 'claude-3-haiku-20240307', label: 'Claude 3 Haiku' }
    ],
    ollama: [
        { value: 'llama3.2', label: 'Llama 3.2' },
        { value: 'llama3.1', label: 'Llama 3.1' },
        { value: 'mistral', label: 'Mistral' },
        { value: 'codellama', label: 'Code Llama' }
    ]
};

// Initialize page
document.addEventListener('DOMContentLoaded', () => {
    loadPlugins();
    setupTagsInput();
});

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

// Update model options based on provider
function updateModelOptions() {
    const provider = document.getElementById('llmProvider').value;
    const modelSelect = document.getElementById('llmModel');
    const models = modelsByProvider[provider] || [];

    modelSelect.innerHTML = '';
    models.forEach(model => {
        const option = document.createElement('option');
        option.value = model.value;
        option.textContent = model.label;
        modelSelect.appendChild(option);
    });
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
    const provider = document.getElementById('llmProvider').value;
    const model = document.getElementById('llmModel').value;

    if (!type || !role || !provider || !model) {
        showError('Please fill in all required fields');
        return;
    }

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
        llm_provider: provider,
        model: model,
        temperature: temperature
    };

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

        // Success - redirect to dashboard
        window.location.href = '/agents-dashboard';

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
