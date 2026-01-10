// Workspace Dashboard - Agent Management Functions
// Handles agent creation, configuration, and auto-config features

(function() {
    'use strict';

    // Ensure shared state exists
    if (!window.wsDashboard) {
        console.error('workspace-dashboard-agents.js: wsDashboard not initialized');
        return;
    }

    const state = window.wsDashboard;

    // Agent management state
    let workspaceAvailableProviders = [];
    let wsDashLLMAvailable = false;
    let wsDashSystemModelConfigured = false;
    let wsDashAutoConfigApplied = false;

    // Load providers for workspace dashboard
    async function loadWorkspaceProviders() {
        try {
            const response = await fetch('/api/providers');
            const data = await response.json();
            workspaceAvailableProviders = data.providers || [];
            return workspaceAvailableProviders;
        } catch (error) {
            console.error('Failed to load providers:', error);
            return [];
        }
    }

    // Populate model select for workspace dashboard
    function populateWorkspaceModelSelect(modelSelect, selectedType = 'tool-calling') {
        if (!modelSelect || workspaceAvailableProviders.length === 0) {
            console.warn('Cannot populate models: modelSelect or providers missing');
            return;
        }

        modelSelect.innerHTML = '';

        workspaceAvailableProviders.forEach(provider => {
            const providerGroup = document.createElement('optgroup');
            providerGroup.label = provider.display_name;

            provider.models.forEach(model => {
                const option = document.createElement('option');
                option.value = model.value;
                option.textContent = model.label;
                option.setAttribute('data-type', model.type);
                option.setAttribute('data-provider', model.provider);

                if (model.type !== selectedType) {
                    option.style.display = 'none';
                    option.disabled = true;
                }

                providerGroup.appendChild(option);
            });

            modelSelect.appendChild(providerGroup);
        });

        // Select first available option
        for (let i = 0; i < modelSelect.options.length; i++) {
            if (!modelSelect.options[i].disabled) {
                modelSelect.selectedIndex = i;
                break;
            }
        }
    }

    async function openManageAgentsModal() {
        const modal = new bootstrap.Modal(document.getElementById('manageAgentsModal'));
        modal.show();

        try {
            await loadWorkspaceProviders();
            const modelSelect = document.getElementById('new-agent-model');
            const typeSelect = document.getElementById('new-agent-type');
            if (modelSelect && typeSelect) {
                populateWorkspaceModelSelect(modelSelect, typeSelect.value);
            }
        } catch (error) {
            console.error('Error loading providers:', error);
        }
    }

    // Check LLM availability
    async function checkWsDashLLMAvailability() {
        try {
            const response = await fetch('/api/agents/auto-config/availability');
            const data = await response.json();
            wsDashLLMAvailable = data.available;
            wsDashSystemModelConfigured = data.system_model_configured || false;
            return data;
        } catch (error) {
            console.error('Failed to check LLM availability:', error);
            wsDashLLMAvailable = false;
            wsDashSystemModelConfigured = false;
            return { available: false, system_model_configured: false };
        }
    }

    // Handle config mode toggle
    function handleWsDashConfigModeChange(mode) {
        const autoConfigSection = document.getElementById('wsDashAutoConfigSection');
        const llmWarning = document.getElementById('wsDashLlmNotAvailableWarning');
        const llmWarningMessage = document.getElementById('wsDashLlmWarningMessage');
        const configModeHelp = document.getElementById('wsDashConfigModeHelp');
        const autoSelectedIndicator = document.getElementById('wsDashAutoSelectedIndicator');

        if (mode === 'auto') {
            if (configModeHelp) configModeHelp.classList.remove('d-none');
            if (wsDashLLMAvailable) {
                if (autoConfigSection) autoConfigSection.classList.remove('d-none');
                if (llmWarning) llmWarning.classList.add('d-none');
            } else {
                if (autoConfigSection) autoConfigSection.classList.add('d-none');
                if (llmWarning) llmWarning.classList.remove('d-none');
                if (llmWarningMessage) {
                    if (!wsDashSystemModelConfigured) {
                        llmWarningMessage.textContent = 'Auto-config requires a System Model to be configured.';
                    } else {
                        llmWarningMessage.textContent = 'Auto-config requires an LLM provider. Please set up an API key or install Ollama.';
                    }
                }
            }
        } else {
            if (autoConfigSection) autoConfigSection.classList.add('d-none');
            if (llmWarning) llmWarning.classList.add('d-none');
            if (configModeHelp) configModeHelp.classList.add('d-none');
            if (autoSelectedIndicator) autoSelectedIndicator.classList.add('d-none');
            wsDashAutoConfigApplied = false;
        }
    }

    function isWsDashAutoConfigFallback(config) {
        return Boolean(config && typeof config.reasoning === 'string' && config.reasoning.startsWith('Auto-config failed'));
    }

    // Generate auto-config
    async function generateWsDashAutoConfig() {
        const description = document.getElementById('wsDashAutoConfigDescription').value.trim();
        const generateBtn = document.getElementById('wsDashGenerateAutoConfigBtn');
        const autoConfigStatus = document.getElementById('wsDashAutoConfigStatus');

        if (!description) {
            alert('Please enter a description of what you want your agent to do.');
            return;
        }

        generateBtn.disabled = true;
        generateBtn.innerHTML = '<span class="spinner-border spinner-border-sm me-1" role="status"></span>Generating...';
        if (autoConfigStatus) {
            autoConfigStatus.textContent = 'Analyzing...';
            autoConfigStatus.classList.remove('d-none', 'bg-success', 'bg-danger');
            autoConfigStatus.classList.add('bg-secondary');
        }

        try {
            const response = await fetch('/api/agents/auto-config', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ description })
            });

            if (!response.ok) {
                const error = await response.text();
                throw new Error(error || 'Failed to generate configuration');
            }

            const config = await response.json();

            // Apply config
            const typeSelect = document.getElementById('new-agent-type');
            if (typeSelect && config.agent_type) {
                typeSelect.value = config.agent_type;
                typeSelect.dispatchEvent(new Event('change'));
            }

            setTimeout(() => {
                const modelSelect = document.getElementById('new-agent-model');
                if (modelSelect && config.model) {
                    for (let i = 0; i < modelSelect.options.length; i++) {
                        if (modelSelect.options[i].value === config.model) {
                            modelSelect.selectedIndex = i;
                            break;
                        }
                    }
                }
            }, 100);

            const tempSlider = document.getElementById('new-agent-temperature');
            const tempValue = document.getElementById('new-agent-temperature-value');
            if (tempSlider && config.temperature !== undefined) {
                tempSlider.value = config.temperature;
                if (tempValue) tempValue.textContent = config.temperature.toFixed(1);
            }

            const promptTextarea = document.getElementById('new-agent-prompt');
            if (promptTextarea && config.system_prompt) {
                promptTextarea.value = config.system_prompt;
            }

            const fallback = isWsDashAutoConfigFallback(config);
            if (autoConfigStatus) {
                autoConfigStatus.textContent = fallback ? 'Applied (defaults)' : 'Applied!';
                autoConfigStatus.classList.remove('bg-secondary', 'bg-success', 'bg-danger', 'bg-warning');
                autoConfigStatus.classList.add(fallback ? 'bg-warning' : 'bg-success');
            }

            const indicator = document.getElementById('wsDashAutoSelectedIndicator');
            if (indicator) indicator.classList.remove('d-none');
            wsDashAutoConfigApplied = true;

            if (config.reasoning) console.log('Auto-config reasoning:', config.reasoning);
            if (fallback) console.warn('Auto-config failed, using defaults.');

        } catch (error) {
            console.error('Auto-config error:', error);
            if (autoConfigStatus) {
                autoConfigStatus.textContent = 'Failed';
                autoConfigStatus.classList.remove('bg-secondary');
                autoConfigStatus.classList.add('bg-danger');
            }
            alert('Failed to generate configuration: ' + error.message);
        } finally {
            generateBtn.disabled = false;
            generateBtn.innerHTML = '<svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" class="me-1"><path d="M12,3L2,12H5V20H19V12H22L12,3M12,8.75A2.25,2.25 0 0,1 14.25,11A2.25,2.25 0 0,1 12,13.25A2.25,2.25 0 0,1 9.75,11A2.25,2.25 0 0,1 12,8.75Z"/></svg>Generate Config';
        }
    }

    // Initialize agent management event listeners
    function initAgentManagement() {
        // Update model dropdown when agent type changes
        const agentTypeSelect = document.getElementById('new-agent-type');
        if (agentTypeSelect) {
            agentTypeSelect.addEventListener('change', function(e) {
                const modelSelect = document.getElementById('new-agent-model');
                if (modelSelect && workspaceAvailableProviders.length > 0) {
                    populateWorkspaceModelSelect(modelSelect, e.target.value);
                }
            });
        }

        // Update temperature value display when slider changes
        const tempSlider = document.getElementById('new-agent-temperature');
        if (tempSlider) {
            tempSlider.addEventListener('input', function(e) {
                document.getElementById('new-agent-temperature-value').textContent = e.target.value;
            });
        }

        // Config mode toggle listeners
        const wsDashConfigModeManual = document.getElementById('wsDashConfigModeManual');
        const wsDashConfigModeAuto = document.getElementById('wsDashConfigModeAuto');

        if (wsDashConfigModeManual) {
            wsDashConfigModeManual.addEventListener('change', function() {
                if (this.checked) handleWsDashConfigModeChange('manual');
            });
        }

        if (wsDashConfigModeAuto) {
            wsDashConfigModeAuto.addEventListener('change', async function() {
                if (this.checked) {
                    await checkWsDashLLMAvailability();
                    handleWsDashConfigModeChange('auto');
                }
            });
        }

        const wsDashGenerateBtn = document.getElementById('wsDashGenerateAutoConfigBtn');
        if (wsDashGenerateBtn) {
            wsDashGenerateBtn.addEventListener('click', generateWsDashAutoConfig);
        }

        // Create agent form submission
        const createAgentForm = document.getElementById('createAgentForm');
        if (createAgentForm) {
            createAgentForm.addEventListener('submit', async function(e) {
                e.preventDefault();

                const name = document.getElementById('new-agent-name').value.trim();
                const type = document.getElementById('new-agent-type').value;
                const model = document.getElementById('new-agent-model').value.trim();
                const temperature = document.getElementById('new-agent-temperature').value;
                const systemPrompt = document.getElementById('new-agent-prompt').value.trim();

                if (!name) {
                    alert('Please enter an agent name');
                    return;
                }

                try {
                    const requestBody = { name, type };

                    if (model) requestBody.model = model;
                    if (temperature) requestBody.temperature = parseFloat(temperature);
                    if (systemPrompt) requestBody.system_prompt = systemPrompt;

                    const response = await fetch('/api/agents', {
                        method: 'POST',
                        headers: { 'Content-Type': 'application/json' },
                        body: JSON.stringify(requestBody)
                    });

                    if (!response.ok) {
                        const error = await response.text();
                        throw new Error(error || 'Failed to create agent');
                    }

                    const result = await response.json();

                    // Clear form
                    document.getElementById('createAgentForm').reset();

                    // Show success message
                    alert('Agent created successfully: ' + name);
                } catch (error) {
                    console.error('Error creating agent:', error);
                    alert('Failed to create agent: ' + error.message);
                }
            });
        }
    }

    // Export agent functions to global state
    Object.assign(state, {
        openManageAgentsModal,
        loadWorkspaceProviders,
        populateWorkspaceModelSelect,
        checkWsDashLLMAvailability,
        handleWsDashConfigModeChange,
        generateWsDashAutoConfig,
        initAgentManagement
    });

    // Initialize on DOM ready
    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', initAgentManagement);
    } else {
        initAgentManagement();
    }

})();
