// Agent Detail Page JavaScript

let currentAgent = null;
let agentName = '';
let isEditingConfig = false;
let isEditingPrompt = false;
let isEditingDescription = false;
let availableProviders = []; // Cache for available providers and models from API
let currentAgentSkills = [];
let globalMCPServers = [];
let globalMCPStats = {};

function supportsCodexReasoning(providerName, modelName) {
  const provider = String(providerName || '').trim().toLowerCase();
  const model = String(modelName || '').trim().toLowerCase();
  return provider === 'codex' || model.includes('codex');
}

function formatAgentTypeLabel(typeValue) {
  return capitalize(typeValue || 'tool-calling');
}

function formatAgentRoleLabel(roleValue) {
  return capitalize(roleValue || 'general');
}

function updateEditReasoningVisibility() {
  const field = document.getElementById('editReasoningField');
  const select = document.getElementById('editReasoningEffort');
  const modelSelect = document.getElementById('editModel');
  const providerFilter = document.getElementById('editProviderFilter');
  if (!field || !select || !modelSelect) {
    return;
  }

  const selectedOption = modelSelect.selectedOptions?.[0];
  const provider = selectedOption?.getAttribute('data-provider')
    || providerFilter?.value
    || currentAgent?.provider
    || '';
  const show = supportsCodexReasoning(provider, modelSelect.value);

  field.style.display = show ? '' : 'none';
  select.disabled = !show;
  if (show && !select.value) {
    select.value = 'medium';
  }
}

// Fetch and display system model in navbar
async function refreshSystemModelDisplay() {
  const modelNameEl = document.getElementById('systemModelName');
  const providerEl = document.getElementById('navSystemModelProvider');
  const indicatorEl = document.getElementById('systemModelIndicator');

  if (!modelNameEl || !providerEl) return;

  try {
    const response = await fetch('/api/settings/system-model');
    if (!response.ok) {
      throw new Error('Failed to fetch system model');
    }
    const data = await response.json();

    if (data.configured && data.model) {
      const modelName = data.model.length > 20 ? data.model.substring(0, 18) + '...' : data.model;
      modelNameEl.textContent = modelName;
      modelNameEl.title = data.model;

      if (data.provider) {
        providerEl.textContent = data.provider;
        providerEl.style.display = 'inline';

        switch (data.provider.toLowerCase()) {
          case 'openai':
            providerEl.style.background = 'rgba(16, 163, 127, 0.2)';
            providerEl.style.color = '#10a37f';
            break;
          case 'claude':
          case 'anthropic':
            providerEl.style.background = 'rgba(204, 147, 102, 0.2)';
            providerEl.style.color = '#cc9366';
            break;
          case 'gemini':
            providerEl.style.background = 'rgba(66, 133, 244, 0.2)';
            providerEl.style.color = '#4285f4';
            break;
          case 'ollama':
            providerEl.style.background = 'rgba(59, 130, 246, 0.2)';
            providerEl.style.color = '#3b82f6';
            break;
          case 'lmstudio':
            providerEl.style.background = 'rgba(14, 165, 233, 0.2)';
            providerEl.style.color = '#0ea5e9';
            break;
          case 'mlx_lm':
            providerEl.style.background = 'rgba(249, 115, 22, 0.2)';
            providerEl.style.color = '#f97316';
            break;
          default:
            providerEl.style.background = 'var(--bg-tertiary)';
            providerEl.style.color = 'var(--text-muted)';
        }
      } else {
        providerEl.style.display = 'none';
      }

      if (indicatorEl) {
        indicatorEl.title = `System Model: ${data.model} (${data.provider}) - Click to configure`;
      }
    } else {
      modelNameEl.textContent = 'Not configured';
      providerEl.style.display = 'none';
      if (indicatorEl) {
        indicatorEl.title = 'System Model not configured - Click to set up';
      }
    }
  } catch (error) {
    console.error('Failed to load system model:', error);
    modelNameEl.textContent = 'Error';
    providerEl.style.display = 'none';
  }
}

// Get agent name from URL - supports both /agents/{name} and ?name={name}
function getAgentNameFromURL() {
  // First try path-based URL: /agents/{agent-name}
  const pathMatch = window.location.pathname.match(/^\/agents\/([^/]+)$/);
  if (pathMatch) {
    return decodeURIComponent(pathMatch[1]);
  }
  // Fall back to query parameter for backward compatibility
  const params = new URLSearchParams(window.location.search);
  return params.get('name');
}

function normalizeAgentLookupToken(value) {
  return String(value || '').trim().toLowerCase();
}

function isAgentNotFoundError(error) {
  return normalizeAgentLookupToken(error?.message || error) === 'agent not found';
}

function flattenWorkspaceRecords(items, output = []) {
  const list = Array.isArray(items) ? items : [];
  list.forEach((item) => {
    if (!item || typeof item !== 'object') return;
    output.push(item);
    if (Array.isArray(item.children) && item.children.length > 0) {
      flattenWorkspaceRecords(item.children, output);
    }
  });
  return output;
}

function findMissingAgentWorkspaceMatch(workspace, requestedAgentName) {
  const target = normalizeAgentLookupToken(requestedAgentName);
  if (!target || !workspace || typeof workspace !== 'object') {
    return null;
  }

  const workspaceId = String(workspace.id || '').trim();
  if (!workspaceId) {
    return null;
  }

  const workspaceName = String(workspace.name || 'Untitled Workspace').trim() || 'Untitled Workspace';
  const directEntryName = String(workspace.entry_agent_name || '').trim();
  const sharedEntryName = String(workspace?.shared_data?.entry_agent_name || '').trim();
  if (normalizeAgentLookupToken(directEntryName) === target) {
    return {
      workspaceId,
      workspaceName,
      isEntryAgent: true,
      matchKind: 'entry_agent_name'
    };
  }
  if (normalizeAgentLookupToken(sharedEntryName) === target) {
    return {
      workspaceId,
      workspaceName,
      isEntryAgent: true,
      matchKind: 'shared_entry_agent_name'
    };
  }

  const instances = Array.isArray(workspace.agent_instances) ? workspace.agent_instances : [];
  for (const instance of instances) {
    if (normalizeAgentLookupToken(instance?.name) !== target) {
      continue;
    }
    const isEntryAgent = Boolean(instance?.entry_point || instance?.entryPoint);
    return {
      workspaceId,
      workspaceName,
      isEntryAgent,
      matchKind: isEntryAgent ? 'entry_agent_instance' : 'agent_instance'
    };
  }

  const agents = Array.isArray(workspace.agents) ? workspace.agents : [];
  if (agents.some((name) => normalizeAgentLookupToken(name) === target)) {
    return {
      workspaceId,
      workspaceName,
      isEntryAgent: false,
      matchKind: 'legacy_agent'
    };
  }

  return null;
}

function scoreMissingAgentWorkspaceMatch(match) {
  switch (match?.matchKind) {
    case 'entry_agent_name':
      return 120;
    case 'shared_entry_agent_name':
      return 110;
    case 'entry_agent_instance':
      return 100;
    case 'agent_instance':
      return 70;
    case 'legacy_agent':
      return 60;
    default:
      return 0;
  }
}

async function loadMissingAgentWorkspaceRecovery(requestedAgentName) {
  const requestedName = String(requestedAgentName || '').trim();
  if (!requestedName) {
    return null;
  }

  try {
    const response = await fetch('/api/workspaces');
    if (!response.ok) {
      throw new Error('Failed to load workspaces');
    }

    const payload = await response.json();
    const workspaces = flattenWorkspaceRecords(payload.workspaces || payload.folders || []);
    const matches = workspaces
      .map((workspace) => findMissingAgentWorkspaceMatch(workspace, requestedName))
      .filter(Boolean)
      .sort((a, b) => scoreMissingAgentWorkspaceMatch(b) - scoreMissingAgentWorkspaceMatch(a));

    return matches[0] || null;
  } catch (error) {
    console.error('Failed to load missing agent workspace recovery context:', error);
    return null;
  }
}

function buildWorkspaceEntryAgentRecoveryURL(workspaceId, requestedAgentName) {
  const params = new URLSearchParams();
  params.set('addAgent', '1');
  if (requestedAgentName) {
    params.set('seedAgentName', requestedAgentName);
  }
  return `/workspaces/${encodeURIComponent(workspaceId)}?${params.toString()}`;
}

function hideMissingAgentState() {
  const missingState = document.getElementById('missingAgentState');
  if (missingState) {
    missingState.style.display = 'none';
  }
}

async function showMissingAgentState(message) {
  const missingState = document.getElementById('missingAgentState');
  if (!missingState) {
    showError(message || 'Agent not found');
    return;
  }

  const requestedName = String(agentName || '').trim();
  const recovery = await loadMissingAgentWorkspaceRecovery(requestedName);

  const titleEl = document.getElementById('missingAgentTitle');
  const messageEl = document.getElementById('missingAgentMessage');
  const metaEl = document.getElementById('missingAgentMeta');
  const detailEl = document.getElementById('missingAgentDetail');
  const createBtn = document.getElementById('missingAgentCreateEntryBtn');
  const openWorkspaceBtn = document.getElementById('missingAgentOpenWorkspaceBtn');
  const backBtn = document.getElementById('missingAgentBackBtn');
  const content = document.getElementById('content');

  const genericMessage = requestedName
    ? `We couldn't find a runnable agent named "${requestedName}". It may have been deleted, renamed, or never created in this environment.`
    : 'We could not identify the requested agent.';
  const normalizedMessage = String(message || '').trim();
  const fallbackBody = isAgentNotFoundError(normalizedMessage)
    ? genericMessage
    : (normalizedMessage || genericMessage);

  let title = 'Agent not found';
  let body = fallbackBody;
  let detail = 'Return to the agents directory or open the relevant workspace to repair the missing reference.';

  if (recovery?.workspaceId && recovery.isEntryAgent) {
    title = 'Workspace entry agent is missing';
    body = requestedName
      ? `"${requestedName}" is still referenced as the entry agent for "${recovery.workspaceName}", but the runnable agent definition no longer exists.`
      : `The entry agent for "${recovery.workspaceName}" no longer exists.`;
    detail = 'Create a replacement entry agent to restore workspace routing, chats, and task execution. You can also open the workspace first to inspect the current configuration.';
  } else if (recovery?.workspaceId) {
    body = requestedName
      ? `"${requestedName}" is still referenced inside "${recovery.workspaceName}", but the runnable agent definition no longer exists.`
      : `This workspace still references an agent that no longer exists.`;
    detail = 'Open the workspace to repair or replace the stale agent reference before continuing.';
  }

  if (titleEl) {
    titleEl.textContent = title;
  }
  if (messageEl) {
    messageEl.textContent = body;
  }
  if (detailEl) {
    detailEl.textContent = detail;
  }
  if (metaEl) {
    const metaItems = [];
    if (requestedName) {
      metaItems.push({ label: 'Requested', value: requestedName });
    }
    if (recovery?.workspaceName) {
      metaItems.push({ label: 'Workspace', value: recovery.workspaceName });
    }
    if (recovery?.workspaceId) {
      metaItems.push({
        label: 'Recovery',
        value: recovery.isEntryAgent ? 'Create entry agent' : 'Repair workspace link'
      });
    }

    if (metaItems.length > 0) {
      metaEl.style.display = 'flex';
      metaEl.innerHTML = metaItems.map((item) => `
        <span class="agent-missing-chip">
          <span class="agent-missing-chip-label">${escapeHtml(item.label)}</span>
          <span>${escapeHtml(item.value)}</span>
        </span>
      `).join('');
    } else {
      metaEl.style.display = 'none';
      metaEl.innerHTML = '';
    }
  }

  if (createBtn) {
    if (recovery?.workspaceId && recovery.isEntryAgent) {
      createBtn.href = buildWorkspaceEntryAgentRecoveryURL(recovery.workspaceId, requestedName);
      createBtn.style.display = 'inline-flex';
    } else {
      createBtn.style.display = 'none';
    }
  }

  if (openWorkspaceBtn) {
    if (recovery?.workspaceId) {
      openWorkspaceBtn.href = `/workspaces/${encodeURIComponent(recovery.workspaceId)}`;
      openWorkspaceBtn.style.display = 'inline-flex';
    } else {
      openWorkspaceBtn.style.display = 'none';
    }
  }

  if (backBtn) {
    backBtn.href = '/agents';
  }

  if (content) {
    content.style.display = 'none';
  }
  missingState.style.display = 'flex';
}

// Fetch available providers and models from API
async function loadAvailableProviders() {
  try {
    const response = await fetch('/api/providers');
    if (!response.ok) {
      throw new Error('Failed to load providers');
    }
    const data = await response.json();
    availableProviders = data.providers || [];
    return availableProviders;
  } catch (error) {
    console.error('Failed to load providers:', error);
    return [];
  }
}

async function loadGlobalMCPServers() {
  try {
    const response = await fetch('/api/mcp/servers');
    if (!response.ok) {
      throw new Error('Failed to load MCP servers');
    }
    const data = await response.json();
    globalMCPServers = Array.isArray(data.servers) ? data.servers : [];
    globalMCPStats = data?.stats && typeof data.stats === 'object' ? data.stats : {};
  } catch (error) {
    console.error('Failed to load global MCP servers:', error);
    globalMCPServers = [];
    globalMCPStats = {};
  } finally {
    renderMCPServers();
    renderSetupHealthBanner();
    renderCapabilitiesCard();
  }
}

// Populate model select with options from available providers
function populateEditModelOptions() {
  const modelSelect = document.getElementById('editModel');
  const providerFilter = document.getElementById('editProviderFilter');
  const typeSelect = document.getElementById('editType');

  if (!modelSelect || availableProviders.length === 0) {
    return;
  }

  const selectedProvider = providerFilter ? providerFilter.value : '';
  const selectedAgentType = typeSelect ? typeSelect.value : (currentAgent?.type || 'tool-calling');

  // Store current value to re-select it after populating
  const currentModelValue = currentAgent?.model || '';

  // Clear existing options
  modelSelect.innerHTML = '';

  // Map provider display names to our filter values
  const providerNameMap = {
    'OpenAI': 'openai',
    'OpenAI Codex (CLI)': 'codex',
    'Anthropic': 'claude',
    'Anthropic Claude': 'claude',
    'Claude Code (CLI)': 'claude_code',
    'Ollama': 'ollama',
    'LM Studio (Local)': 'lmstudio',
    'MLX-LM (Local)': 'mlx_lm',
    'Google Gemini': 'gemini'
  };

  // Find models from the API data
  availableProviders.forEach(provider => {
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

  // Try to select the current model
  if (currentModelValue) {
    for (let i = 0; i < modelSelect.options.length; i++) {
      if (modelSelect.options[i].value === currentModelValue) {
        modelSelect.selectedIndex = i;
        break;
      }
    }
  }

  updateEditReasoningVisibility();
}

// Filter models by provider (called when provider filter changes)
function filterEditModelOptions() {
  populateEditModelOptions();
}

// Initialize page
document.addEventListener('DOMContentLoaded', async () => {
  refreshSystemModelDisplay();

  // Get agent name from URL
  agentName = getAgentNameFromURL();

  if (!agentName) {
    showError('No agent specified');
    setTimeout(() => {
      window.location.href = '/agents';
    }, 2000);
    return;
  }

  const skillsPageLink = document.getElementById('openSkillsPageLink');
  if (skillsPageLink) {
    skillsPageLink.href = `/skills?agent=${encodeURIComponent(agentName)}`;
  }

  // Load providers in parallel with agent details
  await Promise.all([
    loadAvailableProviders(),
    loadGlobalMCPServers(),
    loadAgentDetails()
  ]);

  if (!currentAgent) {
    return;
  }

  document.getElementById('editProviderFilter')?.addEventListener('change', () => {
    window.requestAnimationFrame(updateEditReasoningVisibility);
  });
  document.getElementById('editModel')?.addEventListener('change', updateEditReasoningVisibility);

  const pluginPanel = document.getElementById('pluginManagerPanel');
  if (pluginPanel) {
    await loadAvailablePlugins();
    updateAvailablePluginsToggle();
  }

  if (window.EventBus && typeof EventBus.on === 'function') {
    EventBus.on('plugin:uploaded', async () => {
      if (pluginManagerVisible) {
        await loadAvailablePlugins();
      }
    }, 'agents-detail');
  }
});

async function fetchAgentDetail() {
  const response = await fetch(`/api/agents/${encodeURIComponent(agentName)}/detail`);

  if (!response.ok) {
    if (response.status === 404) {
      throw new Error('Agent not found');
    }
    throw new Error('Failed to load agent details');
  }

  return response.json();
}

// Load agent details from API
async function loadAgentDetails() {
  try {
    showLoading(true);
    currentAgent = await fetchAgentDetail();
    renderAgentDetails();
  } catch (error) {
    console.error('Error loading agent details:', error);
    if (isAgentNotFoundError(error)) {
      currentAgent = null;
      await showMissingAgentState(error.message || 'Agent not found');
      return;
    }
    showError(error.message || 'Failed to load agent details');
    setTimeout(() => {
      window.location.href = '/agents';
    }, 3000);
  } finally {
    showLoading(false);
  }
}

async function refreshAgentDetails() {
  try {
    currentAgent = await fetchAgentDetail();
    renderAgentDetails();
  } catch (error) {
    console.error('Error refreshing agent details:', error);
    if (isAgentNotFoundError(error)) {
      currentAgent = null;
      await showMissingAgentState(error.message || 'Agent not found');
      return;
    }
    setConfigStatus(error.message || 'Failed to refresh agent details', 'error');
  }
}

// Render agent details on page
function renderAgentDetails() {
  if (!currentAgent) return;

  hideMissingAgentState();

  // Header
  const avatar = document.getElementById('agentAvatar');
  if (avatar) {
    // Check for custom avatar image
    if (currentAgent.metadata?.avatar_image) {
      avatar.innerHTML = `<img src="/avatars/${escapeHtml(currentAgent.metadata.avatar_image)}" alt="${escapeHtml(currentAgent.name)}" style="width: 100%; height: 100%; object-fit: cover; border-radius: inherit;">`;
      avatar.style.background = 'transparent';
      avatar.style.overflow = 'hidden';
    } else {
      avatar.style.background = getAgentColor(currentAgent);
      avatar.textContent = getAgentInitials(currentAgent.name);
    }
  }

  const nameEl = document.getElementById('agentName');
  if (nameEl) nameEl.textContent = currentAgent.name;

  const description = currentAgent.metadata?.description || 'No description provided';
  const descEl = document.getElementById('agentDescription');
  if (descEl) descEl.textContent = description;

  const typeEl = document.getElementById('agentType');
  if (typeEl) typeEl.textContent = capitalize(currentAgent.type || 'tool-calling');

  const modelEl = document.getElementById('agentModel');
  if (modelEl) modelEl.textContent = currentAgent.model || 'Not set';

  const providerEl = document.getElementById('agentProvider');
  const provider = currentAgent.provider || currentAgent.llm_provider || '';
  if (providerEl) providerEl.textContent = provider || 'Not set';

  const reasoningRow = document.getElementById('agentReasoningRow');
  const reasoningEl = document.getElementById('agentReasoningEffort');
  const reasoningEffort = currentAgent.reasoning_effort || '';
  const showReasoning = supportsCodexReasoning(provider, currentAgent.model);
  if (reasoningRow) reasoningRow.style.display = showReasoning ? '' : 'none';
  if (reasoningEl) reasoningEl.textContent = reasoningEffort || 'Medium';

  const tempEl = document.getElementById('agentTemperature');
  if (tempEl) tempEl.textContent = currentAgent.temperature ?? 'Not set';

  const maxTokensEl = document.getElementById('agentMaxTokens');
  if (maxTokensEl) maxTokensEl.textContent = formatMaxTokens(currentAgent.max_output_tokens);

  const pluginCountEl = document.getElementById('pluginCount');
  if (pluginCountEl) pluginCountEl.textContent = currentAgent.enabled_plugins?.length || 0;

  // Statistics
  const stats = currentAgent.statistics || {};
  const statMessages = document.getElementById('statMessages');
  if (statMessages) statMessages.textContent = formatNumber(stats.message_count || 0);
  const statTokens = document.getElementById('statTokens');
  if (statTokens) statTokens.textContent = formatNumber(stats.token_usage || 0);
  const statCost = document.getElementById('statCost');
  if (statCost) statCost.textContent = '$' + (stats.total_cost || 0).toFixed(4);

  const avgTokens = stats.message_count > 0
    ? Math.round(stats.token_usage / stats.message_count)
    : 0;
  const statAvgTokens = document.getElementById('statAvgTokens');
  if (statAvgTokens) statAvgTokens.textContent = formatNumber(avgTokens);

  const createdAt = document.getElementById('createdAt');
  if (createdAt) createdAt.textContent = formatFullDate(stats.created_at);
  const updatedAt = document.getElementById('updatedAt');
  if (updatedAt) updatedAt.textContent = formatFullDate(stats.updated_at);

  // Configuration
  const configModel = document.getElementById('configModel');
  if (configModel) configModel.textContent = currentAgent.model || 'Not set';
  const configTemp = document.getElementById('configTemp');
  if (configTemp) configTemp.textContent = currentAgent.temperature ?? 1.0;
  const configType = document.getElementById('configType');
  if (configType) configType.textContent = formatAgentTypeLabel(currentAgent.type);
  const configRole = document.getElementById('configRole');
  if (configRole) configRole.textContent = formatAgentRoleLabel(currentAgent.role, currentAgent.type);

  const systemPrompt = currentAgent.system_prompt || 'Default system prompt';
  const promptEl = document.getElementById('configPrompt');
  if (promptEl) {
    promptEl.textContent = systemPrompt || 'No system prompt set';
  }
  const promptDisplay = document.getElementById('promptDisplay');
  if (promptDisplay) {
    promptDisplay.textContent = systemPrompt || 'No system prompt set';
  }

  // Plugins
  renderPlugins();

  // Tags
  renderTags();

  // MCP dependencies
  renderMCPServers();

  // Skills (async render)
  renderSkills();

  // Setup health + capabilities
  renderSetupHealthBanner();
  renderCapabilitiesCard();

  // Show content
  const header = document.getElementById('agentHeader');
  if (header) header.style.display = 'flex';
  const setupHealthBanner = document.getElementById('setupHealthBanner');
  if (setupHealthBanner) setupHealthBanner.style.display = 'block';
  const capabilitiesSection = document.getElementById('capabilitiesSection');
  if (capabilitiesSection) capabilitiesSection.style.display = 'block';
  const grid = document.getElementById('contentGrid');
  if (grid) grid.style.display = 'grid';

  // Keep edit form in sync with latest data
  populateConfigForm();
  setConfigEditMode(isEditingConfig);
  populatePromptForm();
  setPromptEditMode(isEditingPrompt);
  populateDescriptionForm();
  setDescriptionEditMode(isEditingDescription);
}

function setConfigEditMode(enabled) {
  const display = document.getElementById('configDisplay');
  const form = document.getElementById('configEditForm');
  const editBtn = document.getElementById('editConfigBtn');
  const actions = document.getElementById('configEditActions');
  const saveBtn = document.getElementById('saveConfigBtn');

  isEditingConfig = enabled;

  if (display) display.style.display = enabled ? 'none' : 'block';
  if (form) form.style.display = enabled ? 'block' : 'none';
  if (actions) actions.style.display = enabled ? 'flex' : 'none';
  if (saveBtn) saveBtn.style.display = enabled ? 'inline-flex' : 'none';
  if (editBtn) {
    editBtn.textContent = enabled ? 'Cancel' : 'Edit';
  }

  if (enabled) {
    populateConfigForm();
    setConfigStatus('');
  }
}

function toggleConfigEditMode() {
  if (isEditingConfig) {
    setConfigEditMode(false);
    setConfigStatus('');
    populateConfigForm(); // reset any unsaved edits
  } else {
    setConfigEditMode(true);
  }
}

function populateConfigForm() {
  if (!currentAgent) return;
  const modelSelect = document.getElementById('editModel');
  const providerFilter = document.getElementById('editProviderFilter');
  const tempInput = document.getElementById('editTemperature');
  const maxTokensInput = document.getElementById('editMaxTokens');
  const typeSelect = document.getElementById('editType');
  const roleSelect = document.getElementById('editRole');
  const reasoningSelect = document.getElementById('editReasoningEffort');

  // Set type and role first as they affect model filtering
  if (typeSelect) typeSelect.value = currentAgent.type || 'tool-calling';
  if (roleSelect) roleSelect.value = currentAgent.role || 'general';
  if (reasoningSelect) reasoningSelect.value = currentAgent.reasoning_effort || 'medium';
  if (tempInput) tempInput.value = currentAgent.temperature ?? '';
  if (maxTokensInput) maxTokensInput.value = currentAgent.max_output_tokens || '';

  // Reset provider filter and populate models
  if (providerFilter) providerFilter.value = '';

  // Populate the model dropdown and select current model
  populateEditModelOptions();

  // Select the current model if available
  if (modelSelect && currentAgent.model) {
    for (let i = 0; i < modelSelect.options.length; i++) {
      if (modelSelect.options[i].value === currentAgent.model) {
        modelSelect.selectedIndex = i;
        break;
      }
    }
  }

  updateEditReasoningVisibility();
}

async function saveConfigChanges() {
  if (!currentAgent) return;

  const modelSelect = document.getElementById('editModel');
  const model = modelSelect?.value || '';
  const tempRaw = document.getElementById('editTemperature')?.value;
  const maxTokensRaw = document.getElementById('editMaxTokens')?.value;
  const type = document.getElementById('editType')?.value || 'tool-calling';
  const role = document.getElementById('editRole')?.value || 'general';
  const reasoningEffort = document.getElementById('editReasoningEffort')?.value || 'medium';

  if (!model) {
    setConfigStatus('Model is required to save configuration.', 'error');
    return;
  }

  // Get provider from the selected model's data attribute
  const selectedOption = modelSelect?.options[modelSelect.selectedIndex];
  const provider = selectedOption?.getAttribute('data-provider') || '';

  const payload = {
    model,
    type,
    role
  };

  const temp = parseFloat(tempRaw);
  if (!Number.isNaN(temp)) {
    payload.temperature = temp;
  }

  const maxTokens = parseInt(maxTokensRaw, 10);
  if (!Number.isNaN(maxTokens)) {
    payload.max_output_tokens = maxTokens;
  }

  if (provider) {
    payload.llm_provider = provider;
  }
  if (supportsCodexReasoning(provider, model)) {
    payload.reasoning_effort = reasoningEffort;
  }

  try {
    setConfigSavingState(true);
    setConfigStatus('Saving changes...');

    const response = await fetch(`/api/agents/${encodeURIComponent(agentName)}`, {
      method: 'PATCH',
      headers: {
        'Content-Type': 'application/json'
      },
      body: JSON.stringify(payload)
    });

    if (!response.ok) {
      const errorText = await response.text();
      throw new Error(errorText || 'Failed to save changes');
    }

    await refreshAgentDetails();
    setConfigStatus('Configuration updated successfully.', 'success');
    setConfigEditMode(false);
  } catch (error) {
    console.error('Failed to save configuration:', error);
    setConfigStatus(error.message || 'Failed to save configuration', 'error');
  } finally {
    setConfigSavingState(false);
  }
}

function setConfigSavingState(isSaving) {
  const saveBtn = document.getElementById('saveConfigBtn');
  const editBtn = document.getElementById('editConfigBtn');
  if (saveBtn) {
    saveBtn.disabled = isSaving;
    if (isSaving) {
      saveBtn.innerHTML = '<span class="spinner-border spinner-border-sm me-1" role="status"></span>Saving...';
    } else {
      saveBtn.innerHTML = 'Save';
    }
  }
  if (editBtn) {
    editBtn.disabled = isSaving;
  }
}

function setConfigStatus(message, type = 'info') {
  const statusEl = document.getElementById('configEditStatus');
  if (!statusEl) return;
  statusEl.textContent = message || '';
  if (!message) return;

  if (type === 'error') {
    statusEl.style.color = 'var(--danger-color)';
  } else if (type === 'success') {
    statusEl.style.color = 'var(--success-color, #22c55e)';
  } else {
    statusEl.style.color = 'var(--text-secondary)';
  }
}

function setPromptEditMode(enabled) {
  const display = document.getElementById('promptDisplay');
  const form = document.getElementById('promptEditForm');
  const editBtn = document.getElementById('editPromptBtn');
  const actions = document.getElementById('promptEditActions');
  const saveBtn = document.getElementById('savePromptBtn');

  isEditingPrompt = enabled;

  if (display) display.style.display = enabled ? 'none' : 'block';
  if (form) form.style.display = enabled ? 'block' : 'none';
  if (actions) actions.style.display = enabled ? 'flex' : 'none';
  if (saveBtn) saveBtn.style.display = enabled ? 'inline-flex' : 'none';
  if (editBtn) {
    editBtn.textContent = enabled ? 'Cancel' : 'Edit';
  }

  if (enabled) {
    populatePromptForm();
    setPromptStatus('');
  }
}

function togglePromptEditMode() {
  if (isEditingPrompt) {
    setPromptEditMode(false);
    setPromptStatus('');
    populatePromptForm(); // reset unsaved edits
  } else {
    setPromptEditMode(true);
  }
}

function populatePromptForm() {
  if (!currentAgent) return;
  const promptInput = document.getElementById('editSystemPrompt');
  if (promptInput) promptInput.value = currentAgent.system_prompt || '';
}

async function savePromptChanges() {
  if (!currentAgent) return;
  const promptInput = document.getElementById('editSystemPrompt');
  const systemPrompt = promptInput ? promptInput.value.trim() : '';

  try {
    setPromptSavingState(true);
    setPromptStatus('Saving system prompt...');

    const response = await fetch(`/api/agents/${encodeURIComponent(agentName)}`, {
      method: 'PATCH',
      headers: {
        'Content-Type': 'application/json'
      },
      body: JSON.stringify({ system_prompt: systemPrompt })
    });

    if (!response.ok) {
      const errorText = await response.text();
      throw new Error(errorText || 'Failed to save system prompt');
    }

    await refreshAgentDetails();
    setPromptStatus('System prompt updated successfully.', 'success');
    setPromptEditMode(false);
  } catch (error) {
    console.error('Failed to save system prompt:', error);
    setPromptStatus(error.message || 'Failed to save system prompt', 'error');
  } finally {
    setPromptSavingState(false);
  }
}

function setPromptSavingState(isSaving) {
  const saveBtn = document.getElementById('savePromptBtn');
  const editBtn = document.getElementById('editPromptBtn');
  if (saveBtn) {
    saveBtn.disabled = isSaving;
    if (isSaving) {
      saveBtn.innerHTML = '<span class="spinner-border spinner-border-sm me-1" role="status"></span>Saving...';
    } else {
      saveBtn.innerHTML = 'Save';
    }
  }
  if (editBtn) {
    editBtn.disabled = isSaving;
  }
}

function setPromptStatus(message, type = 'info') {
  const statusEl = document.getElementById('promptEditStatus');
  if (!statusEl) return;
  statusEl.textContent = message || '';
  if (!message) return;

  if (type === 'error') {
    statusEl.style.color = 'var(--danger-color)';
  } else if (type === 'success') {
    statusEl.style.color = 'var(--success-color, #22c55e)';
  } else {
    statusEl.style.color = 'var(--text-secondary)';
  }
}

// Description editing functions
function setDescriptionEditMode(enabled) {
  const display = document.getElementById('agentDescription');
  const form = document.getElementById('descriptionEditForm');
  const editBtn = document.getElementById('editDescriptionBtn');

  isEditingDescription = enabled;

  if (display) display.style.display = enabled ? 'none' : 'block';
  if (form) form.style.display = enabled ? 'block' : 'none';
  if (editBtn) editBtn.style.display = enabled ? 'none' : 'inline-flex';

  if (enabled) {
    populateDescriptionForm();
    setDescriptionStatus('');
  }
}

function toggleDescriptionEditMode() {
  if (isEditingDescription) {
    setDescriptionEditMode(false);
    setDescriptionStatus('');
    populateDescriptionForm();
  } else {
    setDescriptionEditMode(true);
  }
}

function populateDescriptionForm() {
  if (!currentAgent) return;
  const descInput = document.getElementById('editDescription');
  if (descInput) descInput.value = currentAgent.metadata?.description || '';
}

async function saveDescriptionChanges() {
  if (!currentAgent) return;
  const descInput = document.getElementById('editDescription');
  const description = descInput ? descInput.value.trim() : '';

  try {
    setDescriptionSavingState(true);
    setDescriptionStatus('Saving description...');

    const response = await fetch(`/api/agents/${encodeURIComponent(agentName)}`, {
      method: 'PATCH',
      headers: {
        'Content-Type': 'application/json'
      },
      body: JSON.stringify({ description: description })
    });

    if (!response.ok) {
      const errorText = await response.text();
      throw new Error(errorText || 'Failed to save description');
    }

    await refreshAgentDetails();
    setDescriptionStatus('Description updated successfully.', 'success');
    setDescriptionEditMode(false);
  } catch (error) {
    console.error('Failed to save description:', error);
    setDescriptionStatus(error.message || 'Failed to save description', 'error');
  } finally {
    setDescriptionSavingState(false);
  }
}

function setDescriptionSavingState(isSaving) {
  const saveBtn = document.getElementById('saveDescriptionBtn');
  if (saveBtn) {
    saveBtn.disabled = isSaving;
    if (isSaving) {
      saveBtn.innerHTML = '<span class="spinner-border spinner-border-sm me-1" role="status"></span>Saving...';
    } else {
      saveBtn.innerHTML = 'Save';
    }
  }
}

function setDescriptionStatus(message, type = 'info') {
  const statusEl = document.getElementById('descriptionEditStatus');
  if (!statusEl) return;
  statusEl.textContent = message || '';
  if (!message) return;

  if (type === 'error') {
    statusEl.style.color = 'var(--danger-color)';
  } else if (type === 'success') {
    statusEl.style.color = 'var(--success-color, #22c55e)';
  } else {
    statusEl.style.color = 'var(--text-secondary)';
  }
}

function formatMaxTokens(value) {
  if (!value || value <= 0) return 'Not set';
  return value.toLocaleString();
}

function getEnabledPluginsArray() {
  const pluginsRaw = currentAgent?.enabled_plugins;
  if (!Array.isArray(pluginsRaw)) {
    return pluginsRaw ? [pluginsRaw] : [];
  }
  return pluginsRaw;
}

function normalizeMCPServerList(values) {
  if (!Array.isArray(values)) {
    return [];
  }
  return values
    .map((value) => String(value || '').trim())
    .filter((value, index, array) => value && array.indexOf(value) === index);
}

function isMCPServerRunningStatus(status) {
  return String(status || '').toLowerCase() === 'running';
}

function isMCPServerStartingStatus(status) {
  const normalized = String(status || '').toLowerCase();
  return normalized === 'starting' || normalized === 'restarting';
}

function getRequiredSkillMCPServerNames() {
  const skills = Array.isArray(currentAgentSkills) ? currentAgentSkills : [];
  const enabledSkills = skills.filter((skill) => skill?.enabled !== false);
  const dependencyMap = new Map();

  enabledSkills.forEach((skill) => {
    const required = normalizeMCPServerList(skill?.required_mcp_servers || []);
    required.forEach((serverName) => {
      const existing = dependencyMap.get(serverName) || {
        name: serverName,
        requiredBy: [],
      };
      existing.requiredBy.push(skill?.name || '(Unnamed skill)');
      dependencyMap.set(serverName, existing);
    });
  });

  return Array.from(dependencyMap.values());
}

function getGlobalMCPServerConfig(name) {
  return (globalMCPServers || []).find((server) => server?.name === name) || null;
}

function getGlobalMCPServerStatus(name) {
  const stat = globalMCPStats && typeof globalMCPStats === 'object' ? globalMCPStats[name] : null;
  if (stat && typeof stat.status === 'string' && stat.status.trim()) {
    return stat.status;
  }
  return getGlobalMCPServerConfig(name) ? 'configured' : 'missing';
}

function getGlobalMCPToolCount(name) {
  const stat = globalMCPStats && typeof globalMCPStats === 'object' ? globalMCPStats[name] : null;
  return Number(stat?.tool_count ?? stat?.toolCount ?? 0);
}

function collectCapabilityState() {
  if (!currentAgent) {
    return null;
  }

  const enabledPlugins = getEnabledPluginsArray();
  const globalMCPNames = new Set((globalMCPServers || []).map((server) => server?.name).filter(Boolean));
  const skills = Array.isArray(currentAgentSkills) ? currentAgentSkills : [];
  const enabledSkills = skills.filter((skill) => skill?.enabled !== false);
  const dependencies = getRequiredSkillMCPServerNames().map((dependency) => {
    const status = getGlobalMCPServerStatus(dependency.name);
    const existsGlobal = globalMCPNames.has(dependency.name);
    return {
      ...dependency,
      requiredBy: normalizeMCPServerList(dependency.requiredBy),
      existsGlobal,
      status,
      running: isMCPServerRunningStatus(status),
      starting: isMCPServerStartingStatus(status),
      toolCount: getGlobalMCPToolCount(dependency.name),
    };
  });

  const missingModel = !String(currentAgent.model || '').trim();
  const unavailableRequiredMCP = dependencies.filter((dependency) => !dependency.existsGlobal);

  const issues = [];
  if (missingModel) {
    issues.push({
      key: 'missing-model',
      title: 'Model not configured',
      detail: 'Set a model so this agent can process requests.',
      action: 'edit-config',
      actionLabel: 'Edit Config',
    });
  }
  if (unavailableRequiredMCP.length > 0) {
    issues.push({
      key: 'missing-global-mcp',
      title: `${unavailableRequiredMCP.length} required MCP server${unavailableRequiredMCP.length > 1 ? 's are' : ' is'} missing globally`,
      detail: unavailableRequiredMCP.map((dependency) => dependency.name).join(', '),
      action: 'open-mcp-page',
      actionLabel: 'Open MCP Page',
    });
  }

  return {
    enabledPlugins,
    skills,
    enabledSkills,
    dependencies,
    requiredMCPCount: dependencies.length,
    availableRequiredMCPCount: dependencies.filter((dependency) => dependency.existsGlobal).length,
    missingModel,
    issues,
  };
}

function renderSetupHealthBanner() {
  const banner = document.getElementById('setupHealthBanner');
  if (!banner) return;

  const capabilityState = collectCapabilityState();
  if (!capabilityState) {
    banner.style.display = 'none';
    return;
  }

  const { issues } = capabilityState;
  banner.style.display = 'block';
  banner.classList.toggle('is-ready', issues.length === 0);

  if (issues.length === 0) {
    banner.innerHTML = `
      <div class="setup-health-title">Setup Health: Ready</div>
      <p class="setup-health-subtitle">This agent has model configuration and its required MCP connectors are available globally. Concrete MCP access is configured from workspaces.</p>
    `;
    return;
  }

  banner.innerHTML = `
    <div class="setup-health-title">Setup Health: Needs Attention</div>
    <p class="setup-health-subtitle">Fix the items below to make sure this agent's workspace dependencies can be satisfied.</p>
    <div class="setup-health-list">
      ${issues.map((issue) => `
        <div class="setup-health-item">
          <div>
            <p class="setup-health-item-title">${escapeHtml(issue.title || 'Issue')}</p>
            <p class="setup-health-item-detail">${escapeHtml(issue.detail || '')}</p>
          </div>
          ${issue.action ? `
            <button
              type="button"
              class="modern-btn modern-btn-secondary"
              style="padding: 6px 10px; font-size: 12px;"
              data-health-action="${escapeHtml(issue.action)}"
            >
              ${escapeHtml(issue.actionLabel || 'Fix')}
            </button>
          ` : ''}
        </div>
      `).join('')}
    </div>
  `;

  banner.querySelectorAll('[data-health-action]').forEach((button) => {
    button.addEventListener('click', async () => {
      const action = button.getAttribute('data-health-action');
      await handleHealthAction(action);
    });
  });
}

function renderCapabilitiesCard() {
  const section = document.getElementById('capabilitiesSection');
  const badge = document.getElementById('capabilitiesReadinessBadge');
  const summaryText = document.getElementById('capabilitiesSummaryText');
  const summaryGrid = document.getElementById('capabilitiesSummaryGrid');
  const dependenciesContainer = document.getElementById('capabilitiesDependencies');
  if (!section || !badge || !summaryText || !summaryGrid || !dependenciesContainer) {
    return;
  }

  const capabilityState = collectCapabilityState();
  if (!capabilityState) {
    section.style.display = 'none';
    return;
  }

  section.style.display = 'block';
  const readinessClass = capabilityState.issues.length === 0 ? 'ready' : 'warning';
  badge.className = `capability-pill ${readinessClass}`;
  badge.textContent = capabilityState.issues.length === 0 ? 'Ready' : 'Needs Setup';

  summaryText.textContent = capabilityState.issues.length === 0
    ? 'Global prerequisites are available. Bind MCP connectors from a workspace when this agent is assigned there.'
    : `Resolve ${capabilityState.issues.length} setup issue${capabilityState.issues.length > 1 ? 's' : ''} so workspaces can bind the required MCP connectors.`;

  summaryGrid.innerHTML = `
    <div class="capability-summary-item">
      <div class="capability-summary-label">Skills</div>
      <div class="capability-summary-value">${capabilityState.enabledSkills.length} / ${capabilityState.skills.length}</div>
      <span class="capability-pill ${capabilityState.enabledSkills.length > 0 ? 'ready' : 'warning'}">${capabilityState.enabledSkills.length > 0 ? 'Enabled' : 'None enabled'}</span>
    </div>
    <div class="capability-summary-item">
      <div class="capability-summary-label">MCP</div>
      <div class="capability-summary-value">${capabilityState.availableRequiredMCPCount} / ${capabilityState.requiredMCPCount}</div>
      <span class="capability-pill ${capabilityState.requiredMCPCount === 0 || capabilityState.availableRequiredMCPCount === capabilityState.requiredMCPCount ? 'ready' : 'warning'}">${capabilityState.requiredMCPCount === 0 ? 'Not required' : 'Installed globally'}</span>
    </div>
    <div class="capability-summary-item">
      <div class="capability-summary-label">Plugins</div>
      <div class="capability-summary-value">${capabilityState.enabledPlugins.length}</div>
      <span class="capability-pill ${capabilityState.enabledPlugins.length > 0 ? 'ready' : 'warning'}">${capabilityState.enabledPlugins.length > 0 ? 'Connected' : 'None connected'}</span>
    </div>
  `;

  if (capabilityState.dependencies.length === 0) {
    dependenciesContainer.innerHTML = `
      <div class="capability-summary-item">
        <div class="capability-summary-label">Dependencies</div>
        <div style="font-size: 13px; color: var(--text-secondary);">No skill-level MCP dependencies declared.</div>
      </div>
    `;
    return;
  }

  dependenciesContainer.innerHTML = capabilityState.dependencies.map((dependency) => {
    let actionButton = '';
    if (!dependency.existsGlobal) {
      actionButton = `
        <a class="modern-btn modern-btn-secondary" style="padding: 6px 10px; font-size: 12px;" href="/mcp">Add Server</a>
      `;
    } else {
      actionButton = `
        <a class="modern-btn modern-btn-secondary" style="padding: 6px 10px; font-size: 12px;" href="/workspaces">Bind in Workspace</a>
      `;
    }

    return `
      <div class="capability-dependency-item">
        <div>
          <p class="capability-dependency-name">${escapeHtml(dependency.name)}</p>
          <p class="capability-dependency-meta">Required by: ${escapeHtml(dependency.requiredBy.join(', '))}</p>
        </div>
        <div class="capability-dependency-status">
          <span class="capability-pill ${dependency.existsGlobal ? 'ready' : 'error'}">${dependency.existsGlobal ? 'Installed' : 'Missing'}</span>
          <span class="capability-pill ${dependency.existsGlobal ? 'warning' : 'error'}">${dependency.existsGlobal ? 'Workspace scoped' : 'Unavailable'}</span>
          <span class="capability-pill ${dependency.running ? 'ready' : (dependency.starting ? 'warning' : 'warning')}">${dependency.running ? 'Global: running' : (dependency.starting ? 'Global: starting' : `Global: ${dependency.status}`)}</span>
          ${actionButton}
        </div>
      </div>
    `;
  }).join('');
}

async function handleHealthAction(action) {
  switch (action) {
    case 'edit-config':
      toggleConfigEditMode();
      break;
    case 'open-mcp-page':
      window.location.href = '/mcp';
      break;
    default:
      break;
  }
}

// Check if a plugin has configuration by checking the default-settings endpoint
async function checkPluginHasConfig(pluginName) {
  try {
    const response = await fetch(`/api/plugins/${encodeURIComponent(pluginName)}/default-settings`);
    return response.ok;
  } catch (error) {
    return false;
  }
}

// Render plugins list
async function renderPlugins() {
  const container = document.getElementById('enabledPluginsList');
  if (!container) return;

  const pluginsRaw = currentAgent?.enabled_plugins;
  const plugins = Array.isArray(pluginsRaw) ? pluginsRaw : (pluginsRaw ? [pluginsRaw] : []);

  if (plugins.length === 0) {
    container.innerHTML = '<div class="empty-message">No plugins enabled</div>';
    return;
  }

  // Show loading while checking config status
  container.innerHTML = '<div class="text-center py-4" style="color: var(--text-secondary);">Loading plugins...</div>';

  // Check configuration status for all plugins in parallel
  const configChecks = await Promise.all(
    plugins.map(async (plugin) => {
      const name = typeof plugin === 'string' ? plugin : (plugin?.name || plugin?.id || plugin?.plugin || '');
      const hasConfig = await checkPluginHasConfig(name);
      return { name, hasConfig };
    })
  );

  const configStatus = new Map();
  configChecks.forEach(result => {
    configStatus.set(result.name, result.hasConfig);
  });

  container.innerHTML = '';
  plugins.forEach(plugin => {
    const rawName = typeof plugin === 'string' ? plugin : (plugin?.name || plugin?.id || plugin?.plugin || '');
    const displayName = typeof plugin === 'object' && plugin?.metadata?.name ? plugin.metadata.name : stripVersionSuffix(rawName);
    const version = typeof plugin === 'object' && plugin ? (plugin.version || plugin?.meta?.version || '') : '';
    const hasConfig = configStatus.get(rawName) || false;

    const item = document.createElement('div');
    item.className = 'plugin-item';
    item.style.cssText = 'display: flex; justify-content: space-between; align-items: center;';
    item.innerHTML = `
            <div>
                <div class="plugin-name">${escapeHtml(displayName || '(unknown plugin)')}</div>
                ${version ? `<div class="plugin-version">v${escapeHtml(version)}</div>` : ''}
            </div>
            ${hasConfig ? `
                <button class="modern-btn modern-btn-secondary plugin-config-btn"
                        data-plugin-name="${escapeHtml(rawName)}"
                        title="Configure plugin"
                        style="padding: 0.4rem 0.8rem; font-size: 12px;">
                    <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
                        <path d="M12,15.5A3.5,3.5 0 0,1 8.5,12A3.5,3.5 0 0,1 12,8.5A3.5,3.5 0 0,1 15.5,12A3.5,3.5 0 0,1 12,15.5M19.43,12.97C19.47,12.65 19.5,12.33 19.5,12C19.5,11.67 19.47,11.34 19.43,11L21.54,9.37C21.73,9.22 21.78,8.95 21.66,8.73L19.66,5.27C19.54,5.05 19.27,4.96 19.05,5.05L16.56,6.05C16.04,5.66 15.5,5.32 14.87,5.07L14.5,2.42C14.46,2.18 14.25,2 14,2H10C9.75,2 9.54,2.18 9.5,2.42L9.13,5.07C8.5,5.32 7.96,5.66 7.44,6.05L4.95,5.05C4.73,4.96 4.46,5.05 4.34,5.27L2.34,8.73C2.22,8.95 2.27,9.22 2.46,9.37L4.57,11C4.53,11.34 4.5,11.67 4.5,12C4.5,12.33 4.53,12.65 4.57,12.97L2.46,14.63C2.27,14.78 2.22,15.05 2.34,15.27L4.34,18.73C4.46,18.95 4.73,19.03 4.95,18.95L7.44,17.94C7.96,18.34 8.5,18.68 9.13,18.93L9.5,21.58C9.54,21.82 9.75,22 10,22H14C14.25,22 14.46,21.82 14.5,21.58L14.87,18.93C15.5,18.68 16.04,18.34 16.56,17.94L19.05,18.95C19.27,19.03 19.54,18.95 19.66,18.73L21.66,15.27C21.78,15.05 21.73,14.78 21.54,14.63L19.43,12.97Z"/>
                    </svg>
                    Configure
                </button>
            ` : ''}
        `;
    container.appendChild(item);
  });

  // Setup click handlers for configure buttons
  setupPluginConfigButtons();
}

// Setup event listeners for plugin configure buttons
function setupPluginConfigButtons() {
  const configButtons = document.querySelectorAll('#enabledPluginsList .plugin-config-btn');
  configButtons.forEach(button => {
    button.addEventListener('click', async (e) => {
      e.stopPropagation();
      const pluginName = button.dataset.pluginName;
      if (pluginName && typeof showPluginConfigModal === 'function') {
        await showPluginConfigModal(pluginName);
      } else {
        console.error('showPluginConfigModal is not available or plugin name is missing');
      }
    });
  });
}

// Render tags
function renderTags() {
  const container = document.getElementById('tagsList');
  const tags = currentAgent.metadata?.tags || [];

  if (tags.length === 0) {
    document.getElementById('tagsSection').style.display = 'none';
    return;
  }

  container.innerHTML = '';
  tags.forEach(tag => {
    const tagEl = document.createElement('span');
    tagEl.className = 'tag';
    tagEl.textContent = tag;
    container.appendChild(tagEl);
  });
}

// Render skills list
async function renderSkills() {
  const section = document.getElementById('skillsSection');
  const container = document.getElementById('skillsList');
  if (!section || !container) return;

  const name = currentAgent?.name || agentName;
  if (!name) {
    section.style.display = 'none';
    return;
  }

  section.style.display = 'block';
  container.innerHTML = '<div class="text-center py-3" style="color: var(--text-secondary);">Loading skills...</div>';

  try {
    const response = await fetch(`/api/skills?agent=${encodeURIComponent(name)}`);
    if (response.status === 409) {
      const data = await response.json();
      const conflicts = Array.isArray(data.conflicts) ? data.conflicts : [];
      const conflictList = conflicts.map(conflict => {
        const paths = (conflict.paths || []).map(path => `<li>${escapeHtml(path)}</li>`).join('');
        return `<li><strong>${escapeHtml(conflict.name || '')}</strong><ul>${paths}</ul></li>`;
      }).join('');
      currentAgentSkills = [];
      container.innerHTML = `<div class="text-center py-3" style="color: var(--danger-color);">Resolve duplicate skill names to view skills.<ul style="text-align: left; margin-top: 8px;">${conflictList}</ul></div>`;
      renderMCPServers();
      renderSetupHealthBanner();
      renderCapabilitiesCard();
      return;
    }
    if (!response.ok) {
      throw new Error('Failed to load skills');
    }
    const data = await response.json();
    const skills = Array.isArray(data.skills) ? data.skills : [];
    currentAgentSkills = skills;

    if (skills.length === 0) {
      container.innerHTML = '<div class="text-center py-3" style="color: var(--text-secondary);">No skills available for this agent.</div>';
      renderMCPServers();
      renderSetupHealthBanner();
      renderCapabilitiesCard();
      return;
    }

    skills.sort((a, b) => (a.name || '').localeCompare(b.name || '', undefined, { sensitivity: 'base' }));

    container.innerHTML = '';
    skills.forEach(skill => {
      const skillName = skill?.name || '(unnamed skill)';
      const rawSource = skill?.source || 'agent';
      const source = rawSource === 'local' ? 'agent' : rawSource;
      const description = skill?.description || 'No description';
      const isEnabled = skill?.enabled !== false;
      const hasScripts = Boolean(skill?.has_scripts);
      const isTrusted = Boolean(skill?.trusted);
      const validationErrors = Array.isArray(skill?.validation_errors) ? skill.validation_errors : [];
      const hasErrors = validationErrors.length > 0;

      const item = document.createElement('div');
      item.style.cssText = 'padding: 12px; border: 1px solid var(--border-color); border-radius: 8px; background: var(--bg-secondary); display: flex; flex-direction: column; gap: 8px;';
      item.innerHTML = `
        <div style="display: flex; align-items: center; gap: 8px; flex-wrap: wrap;">
          <div style="font-weight: 600; color: var(--text-primary);">${escapeHtml(skillName)}</div>
          <span style="font-size: 10px; padding: 2px 6px; border-radius: 4px; background: var(--bg-tertiary); color: var(--text-secondary); text-transform: uppercase; letter-spacing: 0.3px;">${escapeHtml(source)}</span>
          ${!isEnabled ? '<span style="font-size: 10px; padding: 2px 6px; border-radius: 4px; background: var(--warning-color); color: var(--text-primary); text-transform: uppercase; letter-spacing: 0.3px;">disabled</span>' : ''}
          ${hasErrors ? '<span style="font-size: 10px; padding: 2px 6px; border-radius: 4px; background: var(--danger-color); color: var(--text-primary); text-transform: uppercase; letter-spacing: 0.3px;">invalid</span>' : ''}
          ${hasScripts ? `<span style="font-size: 10px; padding: 2px 6px; border-radius: 4px; background: ${isTrusted ? 'var(--success-color)' : 'var(--danger-color)'}; color: var(--text-primary); text-transform: uppercase; letter-spacing: 0.3px;">${isTrusted ? 'trusted' : 'untrusted'}</span>` : ''}
        </div>
        <div style="font-size: 12px; color: var(--text-secondary); margin-top: 6px;">${escapeHtml(description)}</div>
        ${hasErrors ? `<div style="font-size: 11px; color: var(--danger-color);">${escapeHtml(validationErrors.join('; '))}</div>` : ''}
        <div style="display: flex; align-items: center; justify-content: space-between; gap: 8px; margin-top: auto; padding-top: 4px;">
          <span style="font-size: 12px; color: var(--text-secondary);">Enabled</span>
          <div class="form-check form-switch m-0">
            <input class="form-check-input" type="checkbox" data-action="toggle-skill-enabled" ${isEnabled ? 'checked' : ''}>
          </div>
        </div>
      `;

      const toggle = item.querySelector('[data-action="toggle-skill-enabled"]');
      if (toggle) {
        toggle.addEventListener('change', async () => {
          const desiredState = toggle.checked;
          toggle.disabled = true;
          const success = await setSkillEnabled(name, skillName, desiredState);
          if (!success) {
            toggle.checked = !desiredState;
          }
          toggle.disabled = false;
        });
      }

      container.appendChild(item);
    });

    renderMCPServers();
    renderSetupHealthBanner();
    renderCapabilitiesCard();
  } catch (error) {
    console.error('Failed to load skills:', error);
    currentAgentSkills = [];
    container.innerHTML = '<div class="text-center py-3" style="color: var(--danger-color);">Failed to load skills.</div>';
    renderMCPServers();
    renderSetupHealthBanner();
    renderCapabilitiesCard();
  }
}

async function setSkillEnabled(agent, skillName, enabled) {
  try {
    const response = await fetch(`/api/skills/${encodeURIComponent(skillName)}/enable`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ agent, enabled })
    });

    if (!response.ok) {
      const data = await response.json().catch(() => ({}));
      const message = data?.error || 'Failed to update skill state';
      console.error('Failed to update skill state:', message);
      return false;
    }

    await renderSkills();
    return true;
  } catch (error) {
    console.error('Failed to update skill state:', error);
    return false;
  }
}

function openUploadPluginModal() {
  if (typeof showPluginUploadModal === 'function') {
    showPluginUploadModal();
    return;
  }
  showToast('Plugin upload is not available right now', 'error');
}

// ============================================
// Plugin Management Functions
// ============================================

let pluginManagerVisible = true;
let allAvailablePlugins = [];

function updateAvailablePluginsToggle() {
  const toggleBtn = document.getElementById('toggleAvailablePluginsBtn');
  if (!toggleBtn) return;

  if (pluginManagerVisible) {
    toggleBtn.innerHTML = `
      <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" class="me-1">
        <path d="M7.41,15.41L12,10.83L16.59,15.41L18,14L12,8L6,14L7.41,15.41Z"/>
      </svg>
      Hide Available
    `;
    toggleBtn.setAttribute('aria-expanded', 'true');
  } else {
    toggleBtn.innerHTML = `
      <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" class="me-1">
        <path d="M7.41,8.58L12,13.17L16.59,8.58L18,10L12,16L6,10L7.41,8.58Z"/>
      </svg>
      Show Available
    `;
    toggleBtn.setAttribute('aria-expanded', 'false');
  }
}

// Toggle available plugins panel
async function toggleAvailablePluginsPanel() {
  const panel = document.getElementById('pluginManagerPanel');
  if (!panel) return;

  pluginManagerVisible = !pluginManagerVisible;
  panel.style.display = pluginManagerVisible ? 'block' : 'none';
  updateAvailablePluginsToggle();

  if (pluginManagerVisible) {
    await loadAvailablePlugins();
  }
}

// Load all available plugins from registry
async function loadAvailablePlugins() {
  const container = document.getElementById('availablePluginsList');
  container.innerHTML = '<div class="text-center py-3" style="color: var(--text-secondary);">Loading plugins...</div>';

  try {
    const response = await fetch('/api/plugins');
    if (!response.ok) throw new Error('Failed to load plugins');

    const data = await response.json();
    allAvailablePlugins = data.plugins || [];

    renderAvailablePlugins();
  } catch (error) {
    console.error('Error loading plugins:', error);
    container.innerHTML = '<div class="text-center py-3" style="color: var(--danger-color);">Failed to load plugins</div>';
  }
}

// Render available plugins with toggle switches
function renderAvailablePlugins() {
  const container = document.getElementById('availablePluginsList');
  const enabledPlugins = currentAgent?.enabled_plugins || [];
  // enabled_plugins can be an array of strings or objects with 'name' property
  const enabledNames = enabledPlugins.map(p => typeof p === 'string' ? p : (p?.name || p));
  const enabledSet = new Set(enabledNames);

  if (allAvailablePlugins.length === 0) {
    container.innerHTML = `
      <div class="text-center py-3" style="color: var(--text-secondary);">
        <div>No plugins available yet.</div>
        <button class="modern-btn modern-btn-secondary mt-2" type="button" onclick="openUploadPluginModal()">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" class="me-1">
            <path d="M9,16V10H5L12,3L19,10H15V16H9M5,20V18H19V20H5Z"/>
          </svg>
          Upload Plugin
        </button>
      </div>
    `;
    return;
  }

  container.innerHTML = '';
  allAvailablePlugins.forEach(plugin => {
    const pluginName = plugin.name || plugin;
    const displayName = plugin.metadata?.name || stripVersionSuffix(pluginName);
    const isEnabled = enabledSet.has(pluginName);

    const item = document.createElement('div');
    item.className = 'plugin-item';
    item.style.cssText = 'display: flex; justify-content: space-between; align-items: center; padding: 12px; background: var(--bg-secondary); border: 1px solid var(--border-color); border-radius: 8px;';
    item.innerHTML = `
            <div style="flex: 1;">
                <div style="font-weight: 500; color: var(--text-primary);">${escapeHtml(displayName)}</div>
                ${plugin.description ? `<div style="font-size: 12px; color: var(--text-secondary); margin-top: 4px;">${escapeHtml(plugin.description)}</div>` : ''}
            </div>
            <label class="toggle-switch" style="margin-left: 12px;">
                <input type="checkbox" id="plugin_${pluginName}" ${isEnabled ? 'checked' : ''} onchange="togglePlugin('${escapeHtml(pluginName)}', this.checked)">
                <span class="toggle-slider"></span>
            </label>
        `;
    container.appendChild(item);
  });
}

// Toggle plugin for this agent
async function togglePlugin(pluginName, enabled) {
  const checkbox = document.getElementById(`plugin_${pluginName}`);

  try {
    const endpoint = `/api/plugins/${encodeURIComponent(pluginName)}/${enabled ? 'enable' : 'disable'}?agent=${encodeURIComponent(agentName)}`;
    const response = await fetch(endpoint, {
      method: 'POST'
    });

    if (response.ok) {
      showToast(`${pluginName} ${enabled ? 'enabled' : 'disabled'}`, 'success');
      // Reload agent details to refresh the enabled plugins list
      await loadAgentDetails();
      if (pluginManagerVisible) {
        renderAvailablePlugins();
      }
      // Refresh plugin pages dropdown
      if (window.refreshPluginPages) {
        window.refreshPluginPages();
      }
    } else {
      const error = await response.text();
      showToast(`Failed: ${error}`, 'error');
      if (checkbox) checkbox.checked = !enabled;
    }
  } catch (error) {
    console.error('Toggle plugin error:', error);
    showToast(`Failed to ${enabled ? 'enable' : 'disable'} plugin`, 'error');
    if (checkbox) checkbox.checked = !enabled;
  }
}

// Render MCP servers
function renderMCPServers() {
  const container = document.getElementById('mcpList');
  const section = document.getElementById('mcpSection');
  if (!container) return;
  if (section) {
    section.style.display = 'block';
  }

  const dependencies = getRequiredSkillMCPServerNames();
  if (dependencies.length === 0) {
    container.innerHTML = '<p style="color: var(--text-secondary); font-size: 14px;">No enabled skills currently declare MCP dependencies for this agent.</p>';
    return;
  }

  container.innerHTML = '';
  dependencies.forEach((dependency) => {
    const status = String(getGlobalMCPServerStatus(dependency.name) || 'missing').toLowerCase();
    const toolCount = getGlobalMCPToolCount(dependency.name);
    const existsGlobal = Boolean(getGlobalMCPServerConfig(dependency.name));
    const statusClass = !existsGlobal
      ? 'error'
      : (isMCPServerRunningStatus(status) ? 'ready' : (isMCPServerStartingStatus(status) ? 'warning' : 'warning'));
    const statusLabel = !existsGlobal
      ? 'missing globally'
      : (isMCPServerRunningStatus(status) ? 'global: running' : (isMCPServerStartingStatus(status) ? 'global: starting' : `global: ${status}`));

    const item = document.createElement('div');
    item.className = 'plugin-item';
    item.innerHTML = `
            <div style="display: flex; flex-direction: column; gap: 10px; width: 100%;">
              <div style="display: flex; justify-content: space-between; align-items: center; gap: 10px;">
                <div class="plugin-name">${escapeHtml(dependency.name)}</div>
                <span class="capability-pill ${statusClass}">${escapeHtml(statusLabel)}</span>
              </div>
              <div style="font-size: 12px; color: var(--text-secondary);">
                Required by: ${escapeHtml(normalizeMCPServerList(dependency.requiredBy).join(', '))}
              </div>
              <div style="display: flex; gap: 8px; flex-wrap: wrap;">
                <span class="capability-pill ${existsGlobal ? 'ready' : 'error'}">${existsGlobal ? 'Installed' : 'Missing'}</span>
                <span class="capability-pill ${existsGlobal ? 'warning' : 'error'}">${existsGlobal ? 'Workspace scoped' : 'Unavailable'}</span>
                ${toolCount > 0 ? `<span class="capability-pill ready">${toolCount} tool${toolCount === 1 ? '' : 's'}</span>` : ''}
              </div>
            </div>
        `;
    container.appendChild(item);
  });
}

// Show toast notification
function showToast(message, type = 'info') {
  if (window.Toast) {
    if (type === 'success' && typeof window.Toast.success === 'function') {
      window.Toast.success(message);
      return;
    }
    if (type === 'error' && typeof window.Toast.error === 'function') {
      window.Toast.error(message);
      return;
    }
    if ((type === 'warning' || type === 'warn') && typeof window.Toast.warning === 'function') {
      window.Toast.warning(message);
      return;
    }
    if (typeof window.Toast.info === 'function') {
      window.Toast.info(message);
      return;
    }
  }
  console.log(`[${type}] ${message}`);
}

// Actions
function chatWithAgent() {
  fetch('/api/sessions', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
      title: 'New Session',
      agent_name: agentName
    })
  })
    .then(response => {
      if (response.ok) {
        window.location.href = '/';
      }
    })
    .catch(error => {
      console.error('Error starting agent chat:', error);
      showError('Failed to start chat with agent');
    });
}

async function confirmDelete() {
  if (!confirm(`Are you sure you want to delete agent "${agentName}"? This action cannot be undone.`)) {
    return;
  }

  try {
    const response = await fetch(`/api/agents?name=${encodeURIComponent(agentName)}`, {
      method: 'DELETE'
    });

    if (!response.ok) {
      throw new Error('Failed to delete agent');
    }

    alert(`Agent "${agentName}" deleted successfully`);
    window.location.href = '/agents';

  } catch (error) {
    console.error('Error deleting agent:', error);
    showError('Failed to delete agent');
  }
}

// Helper functions
function getAgentColor(agent) {
  if (agent.metadata?.avatar_color) {
    return agent.metadata.avatar_color;
  }
  // Generate color from name
  const hash = agent.name.split('').reduce((acc, char) => {
    return char.charCodeAt(0) + ((acc << 5) - acc);
  }, 0);
  const hue = hash % 360;
  return `hsl(${hue}, 60%, 50%)`;
}

function getAgentInitials(name) {
  const words = name.split(/[\s_-]+/);
  if (words.length >= 2) {
    return (words[0][0] + words[1][0]).toUpperCase();
  }
  return name.substring(0, 2).toUpperCase();
}

function capitalize(str) {
  if (!str) return '';
  return str.charAt(0).toUpperCase() + str.slice(1);
}

function formatNumber(num) {
  if (num >= 1000000) {
    return (num / 1000000).toFixed(1) + 'M';
  } else if (num >= 1000) {
    return (num / 1000).toFixed(1) + 'K';
  }
  return num.toString();
}

function formatFullDate(dateString) {
  if (!dateString) return 'Never';

  const date = new Date(dateString);
  const now = new Date();
  const diff = now - date;

  const minutes = Math.floor(diff / 60000);
  const hours = Math.floor(diff / 3600000);
  const days = Math.floor(diff / 86400000);

  if (minutes < 1) return 'Just now';
  if (minutes < 60) return `${minutes} minutes ago`;
  if (hours < 24) return `${hours} hours ago`;
  if (days < 7) return `${days} days ago`;

  return date.toLocaleString();
}

// escapeHtml is provided by dom-utils.js

function showLoading(show) {
  const loading = document.getElementById('loadingState');
  const content = document.getElementById('content');
  if (loading) loading.style.display = show ? 'flex' : 'none';
  if (content) content.style.display = show ? 'none' : (currentAgent ? 'block' : 'none');
  if (show) {
    hideMissingAgentState();
  }
}

function showError(message) {
  alert('Error: ' + message);
}
