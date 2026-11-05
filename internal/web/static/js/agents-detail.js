// Agent Detail Page JavaScript

let currentAgent = null;
// Per-section saved baselines, versions and drafts (modules/agent-edit-session.js).
let editSession = null;
let workspaceEditor = null;
// Read-only "Where settings come from" view. It has no Save and never changes
// which record the editors on this page write to.
let ownershipView = null;
// Set when the page itself confirmed a rename, so membership keeps its draft.
let membershipRenamed = false;
let agentName = '';
let isEditingConfig = false;
let isEditingPrompt = false;
let availableProviders = []; // Cache for available providers and models from API
let currentAgentSkills = [];
let currentAgentLoadout = null; // {stage, slot_cap, slots_used, expert_mode} or null
let globalMCPServers = [];
let globalMCPStats = {};
let profileSelectedTags = [];

// Which models take a reasoning level (Codex, Claude Code) is decided once, in
// modules/reasoning-effort.js.
function supportsReasoning(providerName, modelName) {
  return Boolean(window.OriReasoningEffort?.supports(providerName, modelName));
}

// The level last chosen in the editor, kept across model changes so browsing a
// model without "max" does not lose it.
let editReasoningPreference = '';

function reasoningLabel(providerName, modelName, effort) {
  const api = window.OriReasoningEffort;
  const level = api?.normalize(providerName, modelName, effort) || '';
  const fallback = api?.defaultFor(providerName, modelName) || '';
  const shown = level || fallback;
  if (!shown) return 'Claude Code default';
  return (
    api?.options(providerName, modelName).find(option => option.value === shown)?.label || shown
  );
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
  const provider =
    selectedOption?.getAttribute('data-provider') ||
    providerFilter?.value ||
    currentAgent?.provider ||
    '';
  const show = supportsReasoning(provider, modelSelect.value);

  field.style.display = show ? '' : 'none';
  select.disabled = !show;
  if (show) {
    window.OriReasoningEffort.syncSelect(
      select,
      provider,
      modelSelect.value,
      editReasoningPreference
    );
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
        indicatorEl.title = `Default model: ${data.model} (${data.provider}) - Click to configure`;
      }
    } else {
      modelNameEl.textContent = 'Not configured';
      providerEl.style.display = 'none';
      if (indicatorEl) {
        indicatorEl.title = 'Default model not configured - Click to set up';
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
  return String(value || '')
    .trim()
    .toLowerCase();
}

function isAgentNotFoundError(error) {
  return normalizeAgentLookupToken(error?.message || error) === 'agent not found';
}

function flattenWorkspaceRecords(items, output = []) {
  const list = Array.isArray(items) ? items : [];
  list.forEach(item => {
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

  const workspaceSlug = String(workspace.folder_slug || '').trim();
  const workspaceName =
    String(workspace.name || 'Untitled Workspace').trim() || 'Untitled Workspace';
  const directEntryName = String(workspace.entry_agent_name || '').trim();
  const sharedEntryName = String(workspace?.shared_data?.entry_agent_name || '').trim();
  if (normalizeAgentLookupToken(directEntryName) === target) {
    return {
      workspaceId,
      workspaceSlug,
      workspaceName,
      isEntryAgent: true,
      matchKind: 'entry_agent_name'
    };
  }
  if (normalizeAgentLookupToken(sharedEntryName) === target) {
    return {
      workspaceId,
      workspaceSlug,
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
      workspaceSlug,
      workspaceName,
      isEntryAgent,
      matchKind: isEntryAgent ? 'entry_agent_instance' : 'agent_instance'
    };
  }

  const agents = Array.isArray(workspace.agents) ? workspace.agents : [];
  if (agents.some(name => normalizeAgentLookupToken(name) === target)) {
    return {
      workspaceId,
      workspaceSlug,
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
      .map(workspace => findMissingAgentWorkspaceMatch(workspace, requestedName))
      .filter(Boolean)
      .sort((a, b) => scoreMissingAgentWorkspaceMatch(b) - scoreMissingAgentWorkspaceMatch(a));

    return matches[0] || null;
  } catch (error) {
    console.error('Failed to load missing agent workspace recovery context:', error);
    return null;
  }
}

function buildWorkspaceEntryAgentRecoveryURL(workspaceSlug, requestedAgentName) {
  const params = new URLSearchParams();
  params.set('addAgent', '1');
  if (requestedAgentName) {
    params.set('seedAgentName', requestedAgentName);
  }
  return `/workspaces/${encodeURIComponent(workspaceSlug)}?${params.toString()}`;
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
    : normalizedMessage || genericMessage;

  let title = 'Agent not found';
  let body = fallbackBody;
  let detail =
    'Return to the agents directory or open the relevant workspace to repair the missing reference.';

  if (recovery?.workspaceId && recovery.isEntryAgent) {
    title = 'Workspace Commander is missing';
    body = requestedName
      ? `"${requestedName}" is still referenced as the Commander for "${recovery.workspaceName}", but the runnable agent definition no longer exists.`
      : `The Commander for "${recovery.workspaceName}" no longer exists.`;
    detail =
      'Create a replacement Commander to restore workspace routing, chats, and ticket runs. You can also open the workspace first to inspect the current configuration.';
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
        value: recovery.isEntryAgent ? 'Create Commander' : 'Repair workspace link'
      });
    }

    if (metaItems.length > 0) {
      metaEl.style.display = 'flex';
      metaEl.innerHTML = metaItems
        .map(
          item => `
        <span class="agent-missing-chip">
          <span class="agent-missing-chip-label">${escapeHtml(item.label)}</span>
          <span>${escapeHtml(item.value)}</span>
        </span>
      `
        )
        .join('');
    } else {
      metaEl.style.display = 'none';
      metaEl.innerHTML = '';
    }
  }

  if (createBtn) {
    if (recovery?.workspaceSlug && recovery.isEntryAgent) {
      createBtn.href = buildWorkspaceEntryAgentRecoveryURL(recovery.workspaceSlug, requestedName);
      createBtn.style.display = 'inline-flex';
    } else {
      createBtn.style.display = 'none';
    }
  }

  if (openWorkspaceBtn) {
    if (recovery?.workspaceSlug) {
      openWorkspaceBtn.href = `/workspaces/${encodeURIComponent(recovery.workspaceSlug)}`;
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

  if (!modelSelect || availableProviders.length === 0) {
    return;
  }

  const selectedProvider = providerFilter ? providerFilter.value : '';

  // Store current value to re-select it after populating
  const currentModelValue = currentAgent?.model || '';

  // Clear existing options
  modelSelect.innerHTML = '';

  // Map provider display names to our filter values
  const providerNameMap = {
    OpenAI: 'openai',
    'OpenAI Codex (CLI)': 'codex',
    Anthropic: 'claude',
    'Anthropic Claude': 'claude',
    'Claude Code (CLI)': 'claude_code',
    Ollama: 'ollama',
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
      const option = document.createElement('option');
      option.value = model.value;
      option.textContent = model.label;
      option.setAttribute('data-provider', providerKey);
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

  // Select the current model, keeping it selectable even when its provider no
  // longer lists it (only in the unfiltered view, so a filter stays honest).
  if (currentModelValue) {
    let found = false;
    for (let i = 0; i < modelSelect.options.length; i++) {
      if (modelSelect.options[i].value === currentModelValue) {
        modelSelect.selectedIndex = i;
        found = true;
        break;
      }
    }
    if (!found && !selectedProvider) {
      const current = document.createElement('option');
      current.value = currentModelValue;
      current.textContent = `${currentModelValue} (current)`;
      current.setAttribute('data-provider', currentAgent?.provider || '');
      modelSelect.insertBefore(current, modelSelect.firstChild);
      modelSelect.value = currentModelValue;
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

  leaveGuard.register('definition', () => Boolean(editSession?.atRisk().length));
  window.addEventListener('beforeunload', leaveGuard.onBeforeUnload);
  workspaceEditor = window.AgentWorkspaceEditor?.mount({
    root: document.getElementById('agentWorkspacesSection'),
    readAgent: fetchAgentDetail,
    leaveGuard,
    // Membership saves share the page's one-write-at-a-time slot.
    writeSlot: {
      begin: () => editSession?.beginExternal('membership'),
      end: () => {
        editSession?.endExternal('membership');
        updateEditIndicators();
      }
    },
    onSaved: membership => {
      if (currentAgent) Object.assign(currentAgent, membership);
    }
  });

  // Load providers in parallel with agent details
  await Promise.all([loadAvailableProviders(), loadGlobalMCPServers(), loadAgentDetails()]);

  if (currentAgent) await initAfterFirstLoad();
});

// initAfterFirstLoad wires everything that needs a loaded agent. It runs once:
// right after the first load, or after a retry of a failed first load.
let initializedAfterLoad = false;
async function initAfterFirstLoad() {
  if (initializedAfterLoad) return;
  initializedAfterLoad = true;

  // CLI agents have no saved definition to inspect.
  if (currentAgent?.source !== 'cli' && currentAgent?.role !== 'cli_agent') {
    ownershipView =
      window.AgentSettingsOwnership?.mount({
        root: document.getElementById('settingsOwnershipSection'),
        agentName: currentAgent.name || agentName,
        systemModelLinked: () => Boolean(currentAgent?.system_model_linked),
        // A ?tab=ownership deep link lands again once the section has content:
        // cards above it fill in after the first scroll and push it down.
        // Cards above keep growing as their own reads finish, so keep landing
        // on layout changes for a few seconds, until the reader moves.
        onRendered: (() => {
          let started = false;
          return () => {
            if (started) return;
            started = true;
            if (new URLSearchParams(window.location.search).get('tab') !== 'ownership') return;
            const section = document.getElementById('settingsOwnershipSection');
            const land = () => section?.scrollIntoView({ block: 'start', behavior: 'instant' });
            land();
            if (typeof ResizeObserver !== 'function') return;
            const observer = new ResizeObserver(land);
            const stop = () => {
              observer.disconnect();
              for (const type of ['wheel', 'touchstart', 'keydown', 'mousedown']) {
                window.removeEventListener(type, stop);
              }
            };
            for (const type of ['wheel', 'touchstart', 'keydown', 'mousedown']) {
              window.addEventListener(type, stop, { passive: true });
            }
            observer.observe(document.getElementById('content') || document.body);
            window.setTimeout(stop, 8000);
          };
        })()
      }) || null;
    ownershipView?.syncAgent({ name: currentAgent.name, version: currentAgent.version });
    ownershipView?.setUnsaved(Boolean(editSession?.atRisk().length));
  }

  document.getElementById('editProviderFilter')?.addEventListener('change', () => {
    window.requestAnimationFrame(updateEditReasoningVisibility);
  });
  document.getElementById('editModel')?.addEventListener('change', updateEditReasoningVisibility);
  document.getElementById('editReasoningEffort')?.addEventListener('change', event => {
    editReasoningPreference = event.target.value || '';
  });
  setupProfileEditor();

  const expertToggle = document.getElementById('expertModeToggle');
  if (expertToggle) {
    expertToggle.addEventListener('change', async () => {
      const desired = expertToggle.checked;
      expertToggle.disabled = true;
      // false: refused, so the switch goes back; "unknown": it already shows
      // the saved setting from a fresh read.
      const result = await setAgentExpertMode(desired);
      if (result === false) {
        expertToggle.checked = !desired;
      }
      expertToggle.disabled = false;
    });
  }

  const pluginPanel = document.getElementById('pluginManagerPanel');
  if (pluginPanel) {
    await loadAvailablePlugins();
    updateAvailablePluginsToggle();
  }

  if (window.EventBus && typeof EventBus.on === 'function') {
    EventBus.on(
      'plugin:uploaded',
      async () => {
        if (pluginManagerVisible) {
          await loadAvailablePlugins();
        }
      },
      'agents-detail'
    );
  }

  // Returning to the tab reads the latest confirmed values even while editing:
  // reconciliation keeps every draft and flags a section whose fields moved.
  // Only a save in flight defers the read (its answer is about to arrive).
  const refreshOnReturn = () => {
    if (document.hidden) return;
    if (editSession?.pending()) {
      refreshAgentConversationModel();
      return;
    }
    void refreshAgentDetails().then(() => {
      for (const section of ['profile', 'configuration', 'prompt']) {
        if (editSession?.state(section) === 'conflict') {
          statusSetter(section)(
            'This agent was changed elsewhere while you were editing. Your changes are still here and were not saved. Review the latest values below before saving.',
            'error'
          );
          showConflictReview(section);
        }
      }
    });
  };
  window.addEventListener('focus', refreshOnReturn);
  document.addEventListener('visibilitychange', refreshOnReturn);
  window.EventBus?.on?.('systemModel:changed', refreshAgentConversationModel);
  focusRequestedSection();
}

// The roster Inspector is a reader; this page is the editor of record. When
// someone reading a tab there chooses to edit, "Open full page" carries that
// tab as ?tab= and this brings them to the matching section rather than the top
// of a long page (agents-page-ux FR-47).
//
// This page is one scrolling document, not a tabbed one, so a "tab" resolves to
// a section to scroll to. Overview still lands at the top.
const TAB_SECTIONS = {
  configuration: 'agentConfigurationSection',
  prompt: 'promptSection',
  toolbox: 'capabilitiesSection',
  workspaces: 'agentWorkspacesSection',
  appearance: 'appearanceSection',
  ownership: 'settingsOwnershipSection'
};

function focusRequestedSection() {
  const tab = new URLSearchParams(window.location.search).get('tab');
  const id = TAB_SECTIONS[tab];
  if (!id) return;

  // Sections finish rendering after this runs, and one of them (capabilities)
  // stays display:none until the agent declares any, so the target may not be
  // scrollable-to yet. Retry briefly rather than scrolling to a zero-height
  // box or giving up on the first miss; if it never appears — which is the
  // correct outcome for an agent with no capabilities — the deep link simply
  // leaves the reader at the top.
  let attempts = 0;
  const tryScroll = () => {
    const section = document.getElementById(id);
    if (section && section.offsetParent !== null && section.getBoundingClientRect().height > 0) {
      // behavior:'instant', not 'smooth'. This page sets scroll-behavior:smooth
      // globally, and a smooth scroll started during load is cancelled by the
      // rendering still going on behind it — the scroll silently never happens.
      // Arriving from a deep link should also just BE at the destination rather
      // than animating there.
      section.scrollIntoView({ block: 'start', behavior: 'instant' });
      if (tab === 'workspaces')
        document.getElementById('agentWorkspacesTitle')?.focus({ preventScroll: true });
      return;
    }
    if (++attempts < 20) window.setTimeout(tryScroll, 100);
  };
  tryScroll();
}

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
    editSession = window.AgentEditSession?.createSession(currentAgent) || null;
    renderAgentDetails();
  } catch (error) {
    console.error('Error loading agent details:', error);
    if (isAgentNotFoundError(error)) {
      currentAgent = null;
      await showMissingAgentState(error.message || 'Agent not found');
      return;
    }
    showLoadFailure(error.message || 'Failed to load agent details');
    return;
  } finally {
    if (currentAgent || !document.getElementById('agentLoadFailure')) showLoading(false);
  }
  hideLoadFailure();
}

// showLoadFailure keeps the reader on this page with a retry, instead of an
// alert that navigates away on its own.
function showLoadFailure(message) {
  const loading = document.getElementById('loadingState');
  if (!loading) return;
  loading.style.display = 'flex';
  let box = document.getElementById('agentLoadFailure');
  if (!box) {
    box = document.createElement('div');
    box.id = 'agentLoadFailure';
    box.className = 'text-center';
    box.setAttribute('role', 'alert');
    loading.replaceChildren(box);
  }
  box.replaceChildren();
  const text = document.createElement('p');
  text.style.color = 'var(--text-primary)';
  text.textContent = `${message}. Nothing was changed. Check that Ori is running, then try again.`;
  const retry = document.createElement('button');
  retry.type = 'button';
  retry.id = 'agentLoadRetry';
  retry.className = 'modern-btn modern-btn-primary';
  retry.textContent = 'Try again';
  retry.addEventListener('click', async () => {
    retry.disabled = true;
    retry.textContent = 'Loading…';
    await loadAgentDetails();
    if (currentAgent) await initAfterFirstLoad();
  });
  const back = document.createElement('a');
  back.href = '/agents';
  back.className = 'modern-btn modern-btn-secondary ms-2';
  back.textContent = 'Back to Agents';
  box.append(text, retry, back);
  retry.focus();
}

function hideLoadFailure() {
  document.getElementById('agentLoadFailure')?.remove();
}

// Ordering for reads. Every read takes a ticket; a save that adopts its
// receipt moves confirmedEpoch. A read answers only if it is still the newest
// read, nothing was confirmed while it was in flight, and the agent was not
// renamed meanwhile — otherwise an older answer could repaint newer state.
let readTicket = 0;
let confirmedEpoch = 0;

async function refreshAgentDetails() {
  const ticket = ++readTicket;
  const epoch = confirmedEpoch;
  const requestedName = agentName;
  try {
    const latest = await fetchAgentDetail();
    if (ticket !== readTicket || epoch !== confirmedEpoch || requestedName !== agentName) {
      return true; // superseded: a newer read or receipt owns the page
    }
    currentAgent = latest;
    // Reconcile open drafts with the confirmed values before repainting: a
    // section whose fields did not move keeps its draft on the newer version;
    // one whose fields did becomes a conflict that keeps the draft.
    lastReconciliation = editSession ? editSession.observe(currentAgent) : {};
    renderAgentDetails();
    return true;
  } catch (error) {
    console.error('Error refreshing agent details:', error);
    if (ticket !== readTicket || requestedName !== agentName) return false;
    if (isAgentNotFoundError(error)) {
      if (editSession?.atRisk().length) {
        // Keep the page and its drafts; the agent may have been renamed elsewhere.
        setConfigStatus(
          'This agent could not be found any more. Your unsaved changes are kept on this page.',
          'error'
        );
        return false;
      }
      currentAgent = null;
      await showMissingAgentState(error.message || 'Agent not found');
      return false;
    }
    setConfigStatus(error.message || 'Failed to refresh agent details', 'error');
    return false;
  }
}

// Render agent details on page
function renderAgentDetails() {
  if (!currentAgent) return;

  hideMissingAgentState();

  renderCurrentAgentAvatar();
  syncAppearanceEditor();

  const nameEl = document.getElementById('agentName');
  if (nameEl) nameEl.textContent = currentAgent.name;

  const description = currentAgent.metadata?.description || 'No description provided';
  const descEl = document.getElementById('agentDescription');
  if (descEl) descEl.textContent = description;

  renderAgentOrigin();
  // After a confirmed rename the membership editor keeps unsaved choices.
  workspaceEditor?.setAgent(currentAgent, { renamed: membershipRenamed });
  membershipRenamed = false;
  // Re-reads only after a confirmed change (name or version), never a draft.
  ownershipView?.syncAgent({ name: currentAgent.name, version: currentAgent.version });

  const modelEl = document.getElementById('agentModel');
  if (modelEl) modelEl.textContent = currentAgent.model || 'Not set';

  // Keep the saved profile on display and in the form. The conversation's
  // independently resolved default is a read-only note, never form input.
  const systemModelNote = document.getElementById('agentModelSystemNote');
  if (systemModelNote) systemModelNote.hidden = !currentAgent.system_model_linked;
  for (const id of ['agentModelLabel', 'editModelLabel']) {
    const label = document.getElementById(id);
    if (label) label.textContent = currentAgent.system_model_linked ? 'Saved model' : 'Model';
  }
  refreshAgentConversationModel();

  const providerEl = document.getElementById('agentProvider');
  const provider = currentAgent.provider || currentAgent.llm_provider || '';
  if (providerEl) providerEl.textContent = provider || 'Not set';

  const reasoningRow = document.getElementById('agentReasoningRow');
  const reasoningEl = document.getElementById('agentReasoningEffort');
  const reasoningEffort = currentAgent.reasoning_effort || '';
  const showReasoning = supportsReasoning(provider, currentAgent.model);
  if (reasoningRow) reasoningRow.style.display = showReasoning ? '' : 'none';
  if (reasoningEl && showReasoning) {
    reasoningEl.textContent = reasoningLabel(provider, currentAgent.model, reasoningEffort);
  }

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

  const avgTokens =
    stats.message_count > 0 ? Math.round(stats.token_usage / stats.message_count) : 0;
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
  const configRole = document.getElementById('configRole');
  if (configRole)
    configRole.textContent = currentAgent.presentation_role
      ? currentAgent.presentation_role
      : formatAgentRoleLabel(currentAgent.role);

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

  // A refresh repaints what is displayed; it never reinitializes an open
  // editor. A section being edited keeps its inputs, except fields the
  // session rebased onto newer confirmed values.
  // A clean open editor whose saved values changed ("refreshed") is repainted
  // too, or its stale inputs would be sent as changes on the next keystroke.
  const repaint = section => ['rebased', 'refreshed'].includes(lastReconciliation[section]);
  if (!isProfileEditorOpen()) populateProfileForm();
  else if (repaint('profile')) paintProfileFields(sectionValues('profile'));
  if (!isEditingConfig) populateConfigForm();
  else if (repaint('configuration')) populateConfigForm(sectionValues('configuration'));
  showConfigEditMode(isEditingConfig);
  if (!isEditingPrompt) populatePromptForm();
  else if (repaint('prompt')) populatePromptForm(sectionValues('prompt'));
  showPromptEditMode(isEditingPrompt);
  lastReconciliation = {};
  updateEditIndicators();
}

// What reconciliation decided on the most recent confirmed read.
let lastReconciliation = {};

// The values a section's inputs should show: the draft when there is one,
// otherwise the saved baseline.
function sectionValues(section) {
  if (!editSession) return null;
  return editSession.draft(section) || editSession.baseline(section);
}

function isProfileEditorOpen() {
  return Boolean(document.getElementById('profileEditModal')?.classList.contains('show'));
}

// One leave warning for the whole page. Each editor registers what it would
// lose; membership joins through its optional seam instead of warning twice.
// The browser's own wording is all a page may show, so it never claims that a
// save already sent will be cancelled by staying or leaving.
const leaveGuard = {
  checks: new Map(),
  register(name, atRisk) {
    this.checks.set(name, atRisk);
  },
  atRisk() {
    return [...this.checks.values()].some(check => {
      try {
        return Boolean(check());
      } catch {
        return false;
      }
    });
  },
  onBeforeUnload(event) {
    if (!leaveGuard.atRisk()) return;
    event.preventDefault();
    event.returnValue = '';
  }
};

const SECTION_UI = {
  profile: {
    badge: 'profileEditBadge',
    save: 'profileSaveBtn',
    confirm: 'profileDiscardConfirm',
    container: 'profileEditModal',
    status: 'profileEditStatus'
  },
  configuration: {
    badge: 'configEditBadge',
    save: 'saveConfigBtn',
    confirm: 'configDiscardConfirm',
    fields: 'configEditForm',
    container: 'agentConfigurationSection',
    status: 'configEditStatus'
  },
  prompt: {
    badge: 'promptEditBadge',
    save: 'savePromptBtn',
    confirm: 'promptDiscardConfirm',
    fields: 'promptEditForm',
    container: 'promptSection',
    status: 'promptEditStatus'
  }
};

// The control that had focus when a section froze for its save.
const frozenFocus = {};

// rememberFocus records the focused control before a save disables it.
function rememberFocus(section) {
  const container = document.getElementById(SECTION_UI[section].container);
  if (container?.contains(document.activeElement)) frozenFocus[section] = document.activeElement;
}

const BADGE_TEXT = {
  dirty: 'Unsaved changes',
  pending: 'Saving…',
  stale: 'Checking latest…',
  conflict: 'Changed elsewhere — review',
  unconfirmed: 'Save not confirmed',
  error: 'Not saved'
};

function statusSetter(section) {
  return { profile: setProfileStatus, configuration: setConfigStatus, prompt: setPromptStatus }[
    section
  ];
}

// sectionDisplayState is what a section's badge says: its session state, or
// "dirty" for an ordinary draft.
function sectionDisplayState(section) {
  if (!editSession) return '';
  const state = editSession.state(section);
  if (state === 'pending' || state === 'stale' || state === 'conflict' || state === 'unconfirmed')
    return state;
  if (editSession.isDirty(section)) return state === 'error' ? 'error' : 'dirty';
  return '';
}

// updateEditIndicators repaints every section's badge and Save button. While
// one definition write is in flight no other section can start one; inputs of
// the section being saved are frozen so the submitted snapshot is exactly what
// was on screen. Other sections stay editable.
function updateEditIndicators() {
  if (!editSession) return;
  const pending = editSession.pending();
  ownershipView?.setUnsaved(editSession.atRisk().length > 0);
  // Assistive tech hears which section is busy saving.
  for (const [id, section] of [
    ['agentConfigurationSection', 'configuration'],
    ['promptSection', 'prompt'],
    ['profileEditModal', 'profile'],
    ['appearanceSection', 'appearance']
  ]) {
    document.getElementById(id)?.setAttribute('aria-busy', String(pending === section));
  }
  for (const [section, ui] of Object.entries(SECTION_UI)) {
    const state = sectionDisplayState(section);
    const badge = document.getElementById(ui.badge);
    if (badge) {
      badge.hidden = !state;
      badge.dataset.state = state;
      badge.textContent = BADGE_TEXT[state] || '';
    }
    const save = document.getElementById(ui.save);
    if (save && pending !== section) {
      save.disabled =
        Boolean(pending) ||
        !editSession.isDirty(section) ||
        editSession.state(section) === 'unconfirmed';
    }
    if (editSession.state(section) !== 'conflict') hideConflictReview(section);
    const frozen = pending === section;
    const scope =
      section === 'profile'
        ? document.querySelector('#profileEditModal .modal-body')
        : document.getElementById(ui.fields);
    const container = document.getElementById(ui.container);
    // Freezing disables the focused control; remember it so focus can come
    // back after the save instead of falling to the page.
    if (frozen && !frozenFocus[section] && container?.contains(document.activeElement)) {
      frozenFocus[section] = document.activeElement;
    }
    let thawed = false;
    scope?.querySelectorAll('input, textarea, select').forEach(input => {
      if (frozen) {
        if (!input.disabled) input.dataset.frozenBySave = '1';
        input.disabled = true;
      } else if (input.dataset.frozenBySave) {
        delete input.dataset.frozenBySave;
        input.disabled = false;
        thawed = true;
      }
    });
    if (!frozen && frozenFocus[section]) {
      const remembered = frozenFocus[section];
      delete frozenFocus[section];
      const lost = !document.activeElement || document.activeElement === document.body;
      if (lost || thawed) {
        const target =
          remembered.isConnected && !remembered.disabled && remembered.offsetParent !== null
            ? remembered
            : document.getElementById(ui.status);
        if (lost) target?.focus?.();
      }
    }
  }
}

const DRAFT_READERS = {
  profile: () => readProfileFields(),
  configuration: () => readConfigFields(),
  prompt: () => ({ system_prompt: document.getElementById('editSystemPrompt')?.value ?? '' })
};

// syncDraft records what the user typed, so a section knows it is dirty
// before Save is pressed.
function syncDraft(section) {
  if (!editSession || editSession.pending() === section) return;
  editSession.setDraft(section, DRAFT_READERS[section]());
  hideDiscardConfirm(section);
  updateEditIndicators();
}

// The inline "keep editing / discard" confirmation for one section.
function showDiscardConfirm(section, onDiscard) {
  const bar = document.getElementById(SECTION_UI[section].confirm);
  if (!bar) return;
  const returnFocus = document.activeElement;
  bar.hidden = false;
  const keep = bar.querySelector('[data-discard-keep]');
  const discard = bar.querySelector('[data-discard-confirm]');
  keep.onclick = () => {
    bar.hidden = true;
    (returnFocus && document.contains(returnFocus) ? returnFocus : keep)?.focus?.();
  };
  discard.onclick = () => {
    bar.hidden = true;
    onDiscard();
  };
  keep.focus();
}

function hideDiscardConfirm(section) {
  const bar = document.getElementById(SECTION_UI[section].confirm);
  if (bar) bar.hidden = true;
}

// requestCloseSection closes an inline editor, asking first when it holds
// unsaved changes. A section with a save in flight is not closed: the request
// may already have reached the server.
function requestCloseSection(section, close) {
  if (editSession?.pending() === section) {
    statusSetter(section)(
      'Still saving. The request may already have reached the server — wait for the result.',
      'error'
    );
    return;
  }
  if (editSession?.isDirty(section) || editSession?.state(section) === 'unconfirmed') {
    showDiscardConfirm(section, () => {
      editSession.discard(section);
      close();
      updateEditIndicators();
    });
    return;
  }
  editSession?.discard(section);
  close();
  updateEditIndicators();
}

// The hero goes through the shared renderer, so this page shows the same active
// source as the Agents roster instead of its own upload-or-initials rule — the
// exact drift this feature exists to remove (FR-80/FR-85).
function renderCurrentAgentAvatar() {
  const avatar = document.getElementById('agentAvatar');
  if (!avatar || !currentAgent) return;

  if (!window.AgentAvatar) {
    // The renderer is a deferred script; initials keep the box filled if it
    // somehow has not installed yet.
    avatar.innerHTML = '';
    avatar.style.background = getAgentColor(currentAgent);
    avatar.textContent = getAgentInitials(currentAgent.name);
    return;
  }

  const characterId = currentAgent.appearance?.character?.catalog_id || '';
  avatar.innerHTML = window.AgentAvatar.markup(
    {
      name: currentAgent.name || '',
      source: currentAgent.source || 'user',
      role: currentAgent.role || '',
      appearance: currentAgent.appearance || null,
      character:
        characterId && window.CharacterCatalog ? window.CharacterCatalog.get(characterId) : null
    },
    { size: 88 }
  );
  // The renderer owns the box entirely, so the inline background and overflow
  // this used to set would only fight its own styles.
  avatar.style.background = 'transparent';
  avatar.style.overflow = 'hidden';
}

// A workspace's own agent (read from that workspace's copy) is edited in the
// workspace, so the page hides its editing controls and offers "Add to my
// agents" instead. One of the user's own agents names the workspaces that hold
// a customised copy of it.
const WORKSPACE_EDIT_CONTROLS = [
  'editProfileButton',
  'deleteButton',
  'editConfigBtn',
  'editPromptBtn'
];

function isWorkspaceOwnedAgent() {
  return Boolean(window.AgentOrigin && window.AgentOrigin.isWorkspaceOwned(currentAgent));
}

function renderAgentOrigin() {
  const origin = window.AgentOrigin;
  const owned = isWorkspaceOwnedAgent();
  const note = document.getElementById('agentOriginNote');
  if (note) {
    const text = !origin
      ? ''
      : owned
        ? `${origin.workspaceMarker(currentAgent)}. Edit it in that workspace, or add it to your agents.`
        : origin.customisedInLabel(currentAgent);
    note.textContent = text;
    note.hidden = !text;
  }
  const addButton = document.getElementById('addToMyAgentsButton');
  if (addButton) addButton.hidden = !owned;
  // One of the user's own agents has a folder in the Workspace Directory.
  const finderButton = document.getElementById('showInFinderButton');
  if (finderButton) finderButton.hidden = currentAgent?.origin?.source !== 'roster';
  WORKSPACE_EDIT_CONTROLS.forEach(id => {
    const control = document.getElementById(id);
    if (control) control.hidden = owned;
  });
}

async function addToMyAgents() {
  if (!isWorkspaceOwnedAgent()) return;
  const button = document.getElementById('addToMyAgentsButton');
  if (button) button.disabled = true;
  try {
    const response = await fetch('/api/agents/add-from-workspace', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        workspace_id: currentAgent.origin.workspace_id,
        name: currentAgent.name || agentName
      })
    });
    if (!response.ok) {
      throw new Error(await readResponseError(response, 'This agent could not be added.'));
    }
    showToast(`${currentAgent.name || agentName} is now one of your agents.`, 'success');
    await refreshAgentDetails();
  } catch (error) {
    showToast(error.message || 'This agent could not be added.', 'error');
  } finally {
    if (button) button.disabled = false;
  }
}

async function showAgentInFinder() {
  const name = currentAgent?.name || agentName;
  try {
    const response = await fetch(`/api/agents/${encodeURIComponent(name)}/reveal`, {
      method: 'POST'
    });
    if (!response.ok) {
      throw new Error(
        await readResponseError(response, "This agent's folder could not be opened.")
      );
    }
  } catch (error) {
    showToast(error.message || "This agent's folder could not be opened.", 'error');
  }
}

function getAvatarURL(filename) {
  return `/avatars/${encodeURIComponent(String(filename || ''))}`;
}

function setupProfileEditor() {
  const editButton = document.getElementById('editProfileButton');
  const saveButton = document.getElementById('profileSaveBtn');
  const tagsInput = document.getElementById('profileTagsInput');
  const tagsContainer = document.getElementById('profileTagsContainer');
  const modalElement = document.getElementById('profileEditModal');

  editButton?.addEventListener('click', openProfileEditor);
  saveButton?.addEventListener('click', saveProfileChanges);
  document.getElementById('addToMyAgentsButton')?.addEventListener('click', addToMyAgents);
  document.getElementById('showInFinderButton')?.addEventListener('click', showAgentInFinder);
  tagsContainer?.addEventListener('click', () => tagsInput?.focus());
  tagsInput?.addEventListener('keydown', event => {
    const value = tagsInput.value.trim();
    if (event.key === 'Enter' && value) {
      event.preventDefault();
      addProfileTag(value);
      tagsInput.value = '';
    } else if (event.key === 'Backspace' && !tagsInput.value && profileSelectedTags.length > 0) {
      removeProfileTag(profileSelectedTags[profileSelectedTags.length - 1]);
    }
  });

  modalElement?.addEventListener('shown.bs.modal', () => {
    document.getElementById('profileAgentNameInput')?.focus();
  });

  // Draft tracking: each editor records input as it happens.
  for (const id of ['profileAgentNameInput', 'profileDescriptionInput']) {
    document.getElementById(id)?.addEventListener('input', () => syncDraft('profile'));
  }
  document
    .getElementById('profileFavoriteToggle')
    ?.addEventListener('change', () => syncDraft('profile'));
  const configForm = document.getElementById('configEditForm');
  configForm?.addEventListener('input', () => syncDraft('configuration'));
  configForm?.addEventListener('change', () => syncDraft('configuration'));
  document.getElementById('editSystemPrompt')?.addEventListener('input', () => syncDraft('prompt'));

  // Every way of dismissing the modal (Cancel, ×, backdrop, Escape) arrives
  // here. A dirty profile asks first; a save in flight keeps the modal open.
  modalElement?.addEventListener('hide.bs.modal', event => {
    if (allowProfileHide) {
      allowProfileHide = false;
      return;
    }
    if (editSession?.pending() === 'profile') {
      event.preventDefault();
      setProfileStatus(
        'Still saving. The request may already have reached the server — wait for the result.',
        'error'
      );
      return;
    }
    if (editSession?.isDirty('profile')) {
      event.preventDefault();
      showDiscardConfirm('profile', () => {
        editSession.discard('profile');
        paintProfileFields(profileValuesFromAgent());
        updateEditIndicators();
        closeProfileModal();
      });
    }
  });
  modalElement?.addEventListener('hidden.bs.modal', () => {
    hideDiscardConfirm('profile');
    const edit = document.getElementById('editProfileButton');
    if (edit && !edit.hidden) edit.focus();
  });
}

// Set for one hide that the page itself requested after a save or a discard.
let allowProfileHide = false;

function closeProfileModal() {
  const modalElement = document.getElementById('profileEditModal');
  const modal =
    modalElement && typeof bootstrap !== 'undefined'
      ? bootstrap.Modal.getInstance(modalElement)
      : null;
  if (!modal) return;
  allowProfileHide = true;
  modal.hide();
}

// Appearance has its own section and saves each choice immediately through
// the same shared editor every other host uses, so this page cannot drift
// back into its own upload-or-initials rule (FR-26/FR-85). The profile
// modal's Save and Cancel never touch it.
//
// Every appearance write — a JSON change, an upload, a removal — carries the
// page's confirmed version, asks before changing a shared agent, and takes
// the page's one-write slot, so it can neither overlap a section save nor
// silently overwrite someone else's change.
let appearanceEditor = null;
let appearanceEditorKey = '';

function appearanceReadOnly() {
  return isReadOnlyAgent() || isWorkspaceOwnedAgent() || Boolean(editSession?.externalUnknown?.());
}

// syncAppearanceEditor mounts the editor once per agent (and read-only
// state), and otherwise adopts newer confirmed appearance without remounting.
function syncAppearanceEditor() {
  const host = document.getElementById('agentAppearanceHost');
  if (!host || !window.AgentAppearanceEditor || !currentAgent) return;
  // Never repaint or remount under a request in flight: the editor settles it
  // first, and scheduleAppearanceResync catches up afterwards.
  if (appearanceEditor?.status() === 'saving') return;
  const readOnly = appearanceReadOnly();
  const key = `${currentAgent.name}|${readOnly}`;
  if (appearanceEditor && appearanceEditorKey === key) {
    const confirmed = JSON.stringify(appearanceEditor.confirmed());
    const latest = JSON.stringify(currentAgent.appearance || null);
    if (confirmed !== latest) appearanceEditor.setConfirmed(currentAgent.appearance);
    return;
  }
  appearanceEditor?.destroy?.();
  appearanceEditorKey = key;
  watchCharacterCatalog();
  appearanceEditor = window.AgentAppearanceEditor.create({
    host,
    idPrefix: 'agentAppearance',
    mode: 'edit',
    readOnly,
    appearance: currentAgent.appearance,
    agent: {
      name: currentAgent.name || agentName,
      source: currentAgent.source || 'user',
      role: currentAgent.role || ''
    },
    adapter: {
      saveAppearance: patch =>
        appearanceWrite(({ version, confirm }) => [
          `/api/agents?name=${encodeURIComponent(agentName)}`,
          {
            method: 'PATCH',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
              appearance: patch,
              expected_version: version,
              ...(confirm ? { confirm_shared_edit: true } : {})
            })
          }
        ]),
      uploadImage: file =>
        appearanceWrite(({ version, confirm }) => {
          const form = new FormData();
          form.append('image', file);
          form.append('expected_version', version);
          if (confirm) form.append('confirm_shared_edit', 'true');
          return [appearanceUploadURL(), { method: 'POST', body: form }];
        }),
      removeImage: () =>
        appearanceWrite(({ version, confirm }) => {
          const query = new URLSearchParams({ expected_version: version });
          if (confirm) query.set('confirm_shared_edit', 'true');
          return [`${appearanceUploadURL()}?${query}`, { method: 'DELETE' }];
        })
    }
  });
}

// The editor mounts with the page, usually before the character catalog has
// loaded; it then repaints only its preview, which leaves a saved character
// unnamed in its source card. Re-render once the catalog lands, unless the
// user is working inside the editor (a full render would move focus).
let appearanceCatalogWatch = false;
function watchCharacterCatalog() {
  if (appearanceCatalogWatch || typeof window.CharacterCatalog?.onChange !== 'function') return;
  appearanceCatalogWatch = true;
  window.CharacterCatalog.onChange(() => {
    const host = document.getElementById('agentAppearanceHost');
    if (!appearanceEditor || appearanceEditor.status() === 'saving') return;
    if (host?.contains(document.activeElement)) return;
    appearanceEditor.render();
  });
}

function appearanceUploadURL() {
  return `/api/agents/${encodeURIComponent(agentName)}/appearance/upload`;
}

function setAppearanceStatus(message, type = 'info') {
  const status = document.getElementById('appearanceStatus');
  if (!status) return;
  status.replaceChildren();
  if (!message) return;
  status.append(document.createTextNode(message));
  status.style.color =
    type === 'error'
      ? 'var(--agent-edit-danger)'
      : type === 'success'
        ? 'var(--agent-edit-success)'
        : 'var(--text-secondary)';
}

// sendAppearanceRequest sends one request and classifies the answer by the
// server's structured codes. A request that may have reached the server but
// gave no readable answer is "unconfirmed".
async function sendAppearanceRequest(url, init) {
  let response;
  try {
    response = await fetch(url, init);
  } catch (error) {
    return { kind: 'unconfirmed', message: error?.message || '' };
  }
  let body = null;
  let json = true;
  try {
    body = await response.json();
  } catch {
    json = false;
  }
  return window.AgentEditSession.classify(response.status, body, { json });
}

// appearanceWrite runs one appearance mutation and resolves with the
// canonical appearance the server committed. It rejects (and the shared
// editor shows the last confirmed state) on any refusal; it never resends a
// request whose outcome is unknown.
async function appearanceWrite(buildRequest) {
  try {
    editSession.beginExternal('appearance');
  } catch (error) {
    throw new Error(error.message);
  }
  updateEditIndicators();
  setAppearanceStatus('');
  let ended = false;
  const end = outcome => {
    if (ended) return;
    ended = true;
    editSession.endExternal('appearance', outcome);
    updateEditIndicators();
  };
  try {
    const version = currentAgent?.version || '';
    let outcome = await sendAppearanceRequest(...buildRequest({ version, confirm: false }));
    if (outcome.kind === 'needs_confirmation') {
      if (!(await confirmSharedEdit(outcome.body || {}))) {
        // Declining is not an error: once the editor has restored its last
        // confirmed state, show the outcome as neutral information.
        window.setTimeout(
          () =>
            appearanceEditor?.setMessage('Change cancelled — shared agent not modified.', 'info'),
          0
        );
        throw new Error('Change cancelled — shared agent not modified.');
      }
      outcome = await sendAppearanceRequest(...buildRequest({ version, confirm: true }));
    }
    switch (outcome.kind) {
      case 'saved':
        end('done');
        adoptReceipt(outcome.receipt);
        return outcome.receipt.appearance;
      case 'stale':
      case 'changed_on_disk': {
        end('done');
        const reloaded = await refreshAgentDetails();
        scheduleAppearanceResync();
        throw new Error(
          reloaded
            ? 'This agent was changed elsewhere, so that change was not saved. The latest appearance is shown — make your choice again.'
            : 'This agent was changed elsewhere, so that change was not saved. The latest values could not be loaded; reload the page.'
        );
      }
      case 'unconfirmed':
        end('unknown');
        await checkAppearanceAfterUnknown();
        scheduleAppearanceResync();
        throw new Error(
          'Ori could not confirm whether that change was saved. It was not sent again.'
        );
      default:
        throw new Error(outcome.message || 'That change could not be saved.');
    }
  } finally {
    end('done');
  }
}

// After a refusal the shared editor restores its own last-confirmed state;
// once it has, show the page's newer confirmed appearance instead.
function scheduleAppearanceResync() {
  window.setTimeout(syncAppearanceEditor, 0);
}

// checkAppearanceAfterUnknown reads what is saved now. A successful read
// establishes the current state (not which request wrote it) and unblocks
// writes; a failed read keeps appearance read-only with a "Check again".
async function checkAppearanceAfterUnknown() {
  const reloaded = await refreshAgentDetails();
  if (reloaded && editSession.externalUnknown()) {
    editSession.resolveExternal();
    updateEditIndicators();
    renderAgentDetails();
    setAppearanceStatus(
      'Ori could not confirm whether your last appearance change was saved, so it was not sent again. The appearance shown is what is saved now.',
      'error'
    );
    return true;
  }
  setAppearanceStatus(
    'Ori could not confirm whether your last appearance change was saved, and could not load the current appearance. Changes are paused until it can check. ',
    'error'
  );
  const retry = document.createElement('button');
  retry.type = 'button';
  retry.className = 'modern-btn modern-btn-secondary btn-sm';
  retry.textContent = 'Check again';
  retry.addEventListener('click', () => void checkAppearanceAfterUnknown());
  document.getElementById('appearanceStatus')?.append(retry);
  renderAgentDetails();
  return false;
}

// Built-in CLI agents have no editable definition, so they get the editor's
// read-only explanation rather than controls the server rejects (FR-44).
function isReadOnlyAgent() {
  const source = String(currentAgent?.source || '').toLowerCase();
  const role = String(currentAgent?.role || '').toLowerCase();
  return source === 'cli' || role === 'cli_agent';
}

function openProfileEditor() {
  if (!currentAgent) return;
  const pageStatus = document.getElementById('profilePageStatus');
  if (pageStatus) pageStatus.textContent = '';
  populateProfileForm();
  const modalElement = document.getElementById('profileEditModal');
  if (!modalElement || typeof bootstrap === 'undefined') return;
  bootstrap.Modal.getOrCreateInstance(modalElement).show();
}

function populateProfileForm() {
  if (!currentAgent) return;
  paintProfileFields(sectionValues('profile') || profileValuesFromAgent());
  setProfileStatus('');
  setProfileSavingState(false);
}

function profileValuesFromAgent() {
  const metadata = currentAgent?.metadata || {};
  return {
    name: currentAgent?.name || '',
    description: metadata.description || '',
    tags: Array.isArray(metadata.tags) ? metadata.tags : [],
    favorite: Boolean(metadata.favorite)
  };
}

// paintProfileFields writes profile values into the modal's inputs.
function paintProfileFields(values) {
  const nameInput = document.getElementById('profileAgentNameInput');
  const descriptionInput = document.getElementById('profileDescriptionInput');
  const favoriteToggle = document.getElementById('profileFavoriteToggle');

  profileSelectedTags = Array.isArray(values.tags) ? values.tags.slice() : [];
  if (nameInput) {
    nameInput.value = values.name || '';
    nameInput.disabled =
      String(currentAgent?.name || '')
        .trim()
        .toLowerCase() === 'ori';
  }
  if (descriptionInput) descriptionInput.value = values.description || '';
  if (favoriteToggle) favoriteToggle.checked = Boolean(values.favorite);
  renderProfileTags();
}

// readProfileFields is the modal's current input, exactly as typed.
function readProfileFields() {
  return {
    name: document.getElementById('profileAgentNameInput')?.value ?? '',
    description: document.getElementById('profileDescriptionInput')?.value ?? '',
    tags: profileSelectedTags.slice(),
    favorite: Boolean(document.getElementById('profileFavoriteToggle')?.checked)
  };
}

function renderProfileTags() {
  const container = document.getElementById('profileTagsContainer');
  const input = document.getElementById('profileTagsInput');
  if (!container || !input) return;

  container.querySelectorAll('.agent-profile-tag-chip').forEach(chip => chip.remove());
  profileSelectedTags.forEach(tag => {
    const chip = document.createElement('span');
    chip.className = 'agent-profile-tag-chip';

    const label = document.createElement('span');
    label.textContent = tag;

    const remove = document.createElement('button');
    remove.type = 'button';
    remove.className = 'agent-profile-tag-remove';
    remove.setAttribute('aria-label', `Remove ${tag}`);
    remove.textContent = '×';
    remove.addEventListener('click', () => removeProfileTag(tag));

    chip.appendChild(label);
    chip.appendChild(remove);
    container.insertBefore(chip, input);
  });
}

function addProfileTag(tag) {
  const normalized = String(tag || '').trim();
  if (!normalized || profileSelectedTags.includes(normalized)) return;
  profileSelectedTags.push(normalized);
  renderProfileTags();
  syncDraft('profile');
}

function removeProfileTag(tag) {
  profileSelectedTags = profileSelectedTags.filter(item => item !== tag);
  renderProfileTags();
  syncDraft('profile');
}

async function saveProfileChanges() {
  if (!currentAgent || !editSession) return;
  // Appearance is deliberately absent: the shared editor saves each change on
  // its own the moment it happens, so sending it here too would give one
  // setting two write paths that could disagree (FR-41).
  editSession.setDraft('profile', readProfileFields());

  rememberFocus('profile');
  setProfileSavingState(true);
  const outcome = await saveSection('profile', setProfileStatus, 'profile');
  setProfileSavingState(false);
  if (!outcome) return;

  if (outcome.kind === 'rename_failed') {
    const message = outcome.message || 'Your other changes were saved, but the name was not.';
    setProfileStatus(`${message} The new name is still in the form.`, 'error');
    return;
  }
  if (outcome.kind !== 'saved') return;

  const updatedName = outcome.receipt.name || agentName;
  if (updatedName !== agentName) {
    agentName = updatedName;
    membershipRenamed = true;
    // Keep the query (viewing context, ?tab=) and hash: only the name moved.
    window.history.replaceState(
      {},
      '',
      `/agents/${encodeURIComponent(updatedName)}${window.location.search}${window.location.hash}`
    );
    const skillsPageLink = document.getElementById('openSkillsPageLink');
    if (skillsPageLink) skillsPageLink.href = `/skills?agent=${encodeURIComponent(updatedName)}`;
  }
  setProfileStatus('Profile saved.', 'success');
  // The modal may close; the outcome stays on the page where the profile shows.
  const pageStatus = document.getElementById('profilePageStatus');
  if (pageStatus) pageStatus.textContent = 'Profile saved.';
  if (!editSession.isDirty('profile')) {
    closeProfileModal();
    showToast('Agent profile saved.', 'success');
  }
}

// saveSection saves one section's draft through the edit session and reports
// the outcome in that section's status line. It returns the classified
// outcome, or null when nothing was sent.
//
// A refusal, conflict, or lost answer never touches the draft. A confirmed
// save adopts the server's receipt — the values this request committed — and
// reconciles the other sections against it instead of refetching over input.
async function saveSection(section, setStatus, label) {
  let payload;
  try {
    payload = editSession.beginSubmit(section);
  } catch (error) {
    setStatus(error.message, 'error');
    return null;
  }
  hideDiscardConfirm(section);
  updateEditIndicators();
  setStatus(`Saving ${label}…`);
  const outcome = await window.AgentEditSession.submitPatch({
    fetchImpl: (url, init) => fetch(url, init),
    name: agentName,
    payload,
    confirmShared: confirmSharedEdit
  });
  editSession.finishSubmit(section, outcome);
  updateEditIndicators();

  switch (outcome.kind) {
    case 'saved':
      adoptReceipt(outcome.receipt);
      return outcome;
    case 'declined':
      setStatus('Change cancelled — shared agent not modified. Your changes are still here.');
      return outcome;
    case 'stale':
    case 'changed_on_disk': {
      const reason =
        outcome.kind === 'stale'
          ? 'Someone else saved this agent after you opened it.'
          : 'This agent was changed on disk outside Ori.';
      await loadLatestForReview(section, reason);
      return outcome;
    }
    case 'rename_failed':
      if (outcome.body?.agent) adoptReceipt(outcome.body);
      return outcome;
    case 'unconfirmed':
      await verifyUnconfirmed(section);
      return outcome;
    default:
      setStatus(
        `${outcome.message || `Your ${label} could not be saved.`} Your changes are still here.`,
        'error'
      );
      return outcome;
  }
}

// loadLatestForReview reads the latest saved values after a refused save and
// says what happened. The draft is never touched; a failed read keeps a
// "Load latest values" action.
async function loadLatestForReview(section, reason) {
  const setStatus = statusSetter(section);
  const reloaded = await refreshAgentDetails();
  if (!reloaded) {
    setStatus(
      `${reason} Your changes are still here, but the latest values could not be loaded.`,
      'error'
    );
    appendStatusAction(section, 'Load latest values', () => loadLatestForReview(section, reason));
    return;
  }
  if (editSession.state(section) === 'conflict') {
    setStatus(
      `${reason} They changed what you are editing. Your changes are still here and were not saved. Review the latest values below before saving again.`,
      'error'
    );
    showConflictReview(section);
    return;
  }
  setStatus(
    `${reason} Nothing you edited was changed there. Your changes are still here — save again to apply them.`,
    'error'
  );
}

// appendStatusAction adds one recovery button to a section's status line.
function appendStatusAction(section, label, onClick) {
  const status = document.getElementById(
    { profile: 'profileEditStatus', configuration: 'configEditStatus', prompt: 'promptEditStatus' }[
      section
    ]
  );
  if (!status) return;
  const button = document.createElement('button');
  button.type = 'button';
  button.className = 'modern-btn modern-btn-secondary btn-sm ms-2';
  button.textContent = label;
  button.addEventListener('click', () => {
    button.disabled = true;
    void Promise.resolve(onClick()).finally(() => button.remove());
  });
  status.append(' ', button);
}

const FIELD_LABELS = {
  name: 'Name',
  description: 'Description',
  tags: 'Tags',
  favorite: 'Favorite',
  role: 'Role',
  model: 'Model',
  llm_provider: 'Provider',
  temperature: 'Temperature',
  max_output_tokens: 'Max tokens',
  reasoning_effort: 'Reasoning',
  system_prompt: 'System prompt'
};

function formatFieldValue(value) {
  if (Array.isArray(value)) return value.length ? value.join(', ') : '(none)';
  if (typeof value === 'boolean') return value ? 'Yes' : 'No';
  const text = String(value ?? '').trim();
  if (!text) return '(empty)';
  return text.length > 400 ? `${text.slice(0, 400)}…` : text;
}

// showConflictReview lists, for each field changed both here and elsewhere,
// the value saved now and the user's own, with two explicit choices. Neither
// choice saves anything; "Keep mine" rebases the draft for a deliberate Save.
function showConflictReview(section) {
  const box = document.getElementById(
    {
      profile: 'profileConflictReview',
      configuration: 'configConflictReview',
      prompt: 'promptConflictReview'
    }[section]
  );
  if (!box || !editSession) return;
  const latest = editSession.latest(section);
  const draft = editSession.draft(section);
  if (!latest || !draft) {
    box.hidden = true;
    return;
  }
  box.replaceChildren();
  const title = document.createElement('strong');
  title.textContent = 'Changed elsewhere';
  const list = document.createElement('dl');
  const touched = editSession.touched(section);
  for (const key of editSession.conflictFields(section)) {
    const label = FIELD_LABELS[key] || key;
    // A field only coupled to one the user changed (the model beside a
    // reasoning level) is shown as saved, not labelled as the user's.
    const rows = [[`${label} — saved now`, latest[key]]];
    if (touched.includes(key)) rows.push([`${label} — yours`, draft[key]]);
    for (const [term, value] of rows) {
      const dt = document.createElement('dt');
      dt.textContent = term;
      const dd = document.createElement('dd');
      dd.textContent = formatFieldValue(value);
      list.append(dt, dd);
    }
  }
  const actions = document.createElement('div');
  actions.className = 'actions';
  const useSaved = document.createElement('button');
  useSaved.type = 'button';
  useSaved.className = 'modern-btn modern-btn-secondary';
  useSaved.dataset.conflictUseSaved = '';
  useSaved.textContent = 'Use saved version';
  const keepMine = document.createElement('button');
  keepMine.type = 'button';
  keepMine.className = 'modern-btn modern-btn-secondary';
  keepMine.dataset.conflictKeepMine = '';
  keepMine.textContent = 'Keep mine and review';
  useSaved.addEventListener('click', () => {
    editSession.useLatest(section);
    paintSection(section, editSession.baseline(section));
    box.hidden = true;
    statusSetter(section)('Showing the saved version. Your unsaved changes were discarded.');
    updateEditIndicators();
    focusSection(section);
  });
  keepMine.addEventListener('click', () => {
    editSession.reapply(section);
    paintSection(section, editSession.draft(section) || editSession.baseline(section));
    box.hidden = true;
    statusSetter(section)(
      'Your changes are kept on top of the latest saved version. Review them, then save. Nothing has been saved yet.'
    );
    updateEditIndicators();
    focusSection(section);
  });
  actions.append(useSaved, keepMine);
  box.append(title, list, actions);
  box.hidden = false;
}

function hideConflictReview(section) {
  const box = document.getElementById(
    `${section === 'configuration' ? 'config' : section}ConflictReview`
  );
  if (box) box.hidden = true;
}

// paintSection writes values into a section's inputs.
function paintSection(section, values) {
  if (section === 'profile') paintProfileFields(values);
  else if (section === 'configuration') populateConfigForm(values);
  else populatePromptForm(values);
}

// focusSection moves focus to a section's first input, for keyboard users
// after a review choice.
function focusSection(section) {
  const id = {
    profile: 'profileAgentNameInput',
    configuration: 'editModel',
    prompt: 'editSystemPrompt'
  }[section];
  document.getElementById(id)?.focus();
}

// fetchAgentDetailNamed reads one agent by name without touching page state.
async function fetchAgentDetailNamed(name) {
  const response = await fetch(`/api/agents/${encodeURIComponent(name)}/detail`);
  if (response.status === 404) throw new Error('Agent not found');
  if (!response.ok) throw new Error('Failed to load agent details');
  return response.json();
}

// verifyUnconfirmed settles a save whose answer never arrived (the
// connection dropped, the answer was unreadable). It reads what is saved now —
// by the old name, then by a requested new name if the old one is gone — and
// compares it with what was sent. It never sends the change again: a read that
// matches proves the current state, not which request wrote it.
async function verifyUnconfirmed(section) {
  const setStatus = statusSetter(section);
  setStatus('The save could not be confirmed. Checking what is saved now…', 'error');
  const submitted = editSession.submitted(section) || editSession.draft(section) || {};
  const requestedName = section === 'profile' ? String(submitted.name || '').trim() : '';
  let latest = null;
  let renamedTo = '';
  try {
    latest = await fetchAgentDetailNamed(agentName);
  } catch (error) {
    if (isAgentNotFoundError(error) && requestedName && requestedName !== agentName) {
      let candidate = null;
      try {
        candidate = await fetchAgentDetailNamed(requestedName);
      } catch {
        candidate = null;
      }
      // An agent under the requested name is only taken to be this one when
      // the rest of its definition is exactly what this page was editing.
      // Anything else (another agent that already had that name, say) leaves
      // the outcome unproven rather than guessed.
      if (
        candidate &&
        (!editSession.matchesBaseline('configuration', candidate) ||
          !editSession.matchesBaseline('prompt', candidate))
      ) {
        setStatus(
          `Ori could not confirm whether "${agentName}" was renamed to "${requestedName}": the old name is gone, and the agent now called "${requestedName}" does not match what you were editing. Nothing was sent again; your changes are kept here.`,
          'error'
        );
        appendStatusAction(section, 'Check again', () => verifyUnconfirmed(section));
        updateEditIndicators();
        return;
      }
      latest = candidate;
      renamedTo = candidate ? candidate.name || requestedName : '';
    }
  }
  if (!latest) {
    setStatus(
      'The save could not be confirmed, and Ori could not read what is saved now. Your changes are kept and were not sent again. Saving is paused until Ori can check.',
      'error'
    );
    appendStatusAction(section, 'Check again', () => verifyUnconfirmed(section));
    updateEditIndicators();
    return;
  }
  if (renamedTo) adoptRenamedIdentity(renamedTo);
  confirmedEpoch += 1;
  readTicket += 1;
  currentAgent = latest;
  lastReconciliation = editSession.observe(currentAgent);
  const result = editSession.reconcileUnconfirmed(section, currentAgent);
  renderAgentDetails();
  if (result === 'matched') {
    // Nothing is left to save, so the inline editor closes; the status line
    // stays outside it.
    if (!editSession.isDirty(section)) {
      if (section === 'configuration') showConfigEditMode(false);
      if (section === 'prompt') showPromptEditMode(false);
    }
    setStatus(
      'The saved values now match your changes. Ori could not confirm which save wrote them, so nothing was sent again.',
      'success'
    );
  } else if (result === 'not_saved') {
    paintSection(section, editSession.draft(section) || editSession.baseline(section));
    setStatus(
      'Your change does not appear to have been saved. Your changes are still here — save again when ready.',
      'error'
    );
  } else if (result === 'conflict') {
    setStatus(
      'The save could not be confirmed, and these fields now hold other values. Your changes are still here. Review them below.',
      'error'
    );
    showConflictReview(section);
  }
  updateEditIndicators();
}

// adoptRenamedIdentity points the page at an agent's new name once a read
// has shown the rename happened.
function adoptRenamedIdentity(name) {
  membershipRenamed = true;
  agentName = name;
  window.history.replaceState(
    {},
    '',
    `/agents/${encodeURIComponent(name)}${window.location.search}${window.location.hash}`
  );
  const skillsPageLink = document.getElementById('openSkillsPageLink');
  if (skillsPageLink) skillsPageLink.href = `/skills?agent=${encodeURIComponent(name)}`;
}

// adoptReceipt puts the values a save committed on the page, then reconciles
// every section with them.
function adoptReceipt(receipt) {
  const saved = receipt?.agent;
  if (!saved || !currentAgent) return;
  // A confirmed rename moves the page's identity before anything re-renders,
  // so every later read (membership included) asks for the new name.
  if (receipt.name && receipt.name !== agentName) adoptRenamedIdentity(receipt.name);
  confirmedEpoch += 1;
  currentAgent = {
    ...currentAgent,
    ...saved,
    metadata: { ...(currentAgent.metadata || {}), ...(saved.metadata || {}) },
    version: receipt.version || saved.version
  };
  lastReconciliation = editSession.observe(currentAgent, receipt.version);
  renderAgentDetails();
  // A follow-up step (the Default model alignment) changed the record again
  // after this save; read it so the next save starts from the newer version.
  if (receipt.current_version && receipt.current_version !== receipt.version) {
    void refreshAgentDetails();
  }
}

// confirmSharedEdit asks before changing a definition several workspaces
// share (PRD FR9), naming every affected workspace.
async function confirmSharedEdit(info) {
  const names = Array.isArray(info.workspaces)
    ? info.workspaces.map(w => w && w.name).filter(Boolean)
    : [];
  const count = Number(info.workspace_count || names.length || 0);
  const list = names.length ? `\n\n• ${names.join('\n• ')}` : '';
  const name = currentAgent?.name || agentName;
  return window.confirm(
    `"${name}" is attached to ${count} workspace${count === 1 ? '' : 's'}. ` +
      `This change affects all of them:${list}\n\nApply the change everywhere?`
  );
}

// A 409 with this code means the agent's definition file was edited outside Ori
// (a text editor, a sync tool). The server has already reloaded it, so the page
// refetches the agent and shows what is on disk instead of the user's stale copy.
const AGENT_CHANGED_ON_DISK = 'agent_changed_on_disk';

// "Also updated in 2 workspaces. Song B keeps its own changes." — where a saved
// model or prompt edit went among the workspace copies Ori keeps in step with
// this agent, or '' when it reached none.
function carriedEditNote(receipt) {
  return window.AgentOrigin ? window.AgentOrigin.carriedEditLabel(receipt && receipt.carried) : '';
}

function setProfileSavingState(isSaving) {
  const saveButton = document.getElementById('profileSaveBtn');
  if (!saveButton) return;

  saveButton.disabled = isSaving;
  saveButton.innerHTML = isSaving
    ? '<span class="spinner-border spinner-border-sm me-1" role="status"></span>Saving...'
    : 'Save Profile';
  if (!isSaving) updateEditIndicators();
}

function setProfileStatus(message, type = 'info') {
  const status = document.getElementById('profileEditStatus');
  if (!status) return;

  status.textContent = message || '';
  if (!message) return;

  if (type === 'error') {
    status.style.color = 'var(--agent-edit-danger)';
  } else if (type === 'success') {
    status.style.color = 'var(--agent-edit-success)';
  } else {
    status.style.color = 'var(--text-secondary)';
  }
}

async function readResponseError(response, fallback) {
  try {
    const data = await response.clone().json();
    // Prefer the human-readable message; fall back to the (sometimes
    // machine-code) error field, then the caller's fallback.
    return data?.message || data?.error || fallback;
  } catch {
    const text = await response.text().catch(() => '');
    return text || fallback;
  }
}

// showConfigEditMode only shows or hides the editor; it never touches inputs.
function showConfigEditMode(enabled) {
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
}

// setConfigEditMode opens the editor on the section's draft (or its saved
// values), or closes it.
function setConfigEditMode(enabled) {
  showConfigEditMode(enabled);
  if (enabled) {
    populateConfigForm(sectionValues('configuration'));
    setConfigStatus('');
  }
}

function toggleConfigEditMode() {
  if (isEditingConfig) {
    requestCloseSection('configuration', () => {
      setConfigEditMode(false);
      setConfigStatus('');
      populateConfigForm();
      document.getElementById('editConfigBtn')?.focus();
    });
  } else {
    setConfigEditMode(true);
    updateEditIndicators();
  }
}

// populateConfigForm paints configuration values into the editor; with no
// argument it paints the saved values.
function populateConfigForm(values) {
  if (!currentAgent) return;
  const v = values || {
    role: currentAgent.role,
    model: currentAgent.model,
    temperature: currentAgent.temperature,
    max_output_tokens: currentAgent.max_output_tokens,
    reasoning_effort: currentAgent.reasoning_effort
  };
  const modelSelect = document.getElementById('editModel');
  const providerFilter = document.getElementById('editProviderFilter');
  const tempInput = document.getElementById('editTemperature');
  const maxTokensInput = document.getElementById('editMaxTokens');
  const roleSelect = document.getElementById('editRole');

  if (roleSelect) roleSelect.value = v.role || 'general';
  editReasoningPreference = v.reasoning_effort || '';
  if (tempInput) tempInput.value = v.temperature ?? '';
  if (maxTokensInput) maxTokensInput.value = v.max_output_tokens || '';

  // Reset provider filter and populate models
  if (providerFilter) providerFilter.value = '';

  const systemModelHint = document.getElementById('editSystemModelHint');
  if (systemModelHint) systemModelHint.hidden = !currentAgent.system_model_linked;

  // Populate the model dropdown and select the model
  populateEditModelOptions();

  if (modelSelect && v.model) {
    for (let i = 0; i < modelSelect.options.length; i++) {
      if (modelSelect.options[i].value === v.model) {
        modelSelect.selectedIndex = i;
        break;
      }
    }
  }

  updateEditReasoningVisibility();
}

// readConfigFields is the configuration editor's current input. Provider,
// model and reasoning come as one coupled choice.
function readConfigFields() {
  const modelSelect = document.getElementById('editModel');
  const model = modelSelect?.value || '';
  const selectedOption = modelSelect?.options[modelSelect.selectedIndex];
  const provider = selectedOption?.getAttribute('data-provider') || '';
  const reasoningEffort = document.getElementById('editReasoningEffort')?.value || '';
  // The provider follows the model choice. Keeping the saved model keeps the
  // saved provider, so an option's own provider label (e.g. "openai" for a
  // record saved without one) is not mistaken for a change.
  const keptModel = model === (currentAgent?.model || '');
  return {
    role: document.getElementById('editRole')?.value || 'general',
    model,
    llm_provider: keptModel
      ? currentAgent?.provider || ''
      : provider || currentAgent?.provider || '',
    temperature: document.getElementById('editTemperature')?.value ?? '',
    max_output_tokens: document.getElementById('editMaxTokens')?.value ?? '',
    // An empty level clears a saved one (Claude Code's default sends no flag).
    reasoning_effort: supportsReasoning(provider, model)
      ? window.OriReasoningEffort.normalize(provider, model, reasoningEffort)
      : ''
  };
}

async function saveConfigChanges() {
  if (!currentAgent || !editSession) return;
  const fields = readConfigFields();
  if (!fields.model) {
    setConfigStatus('Model is required to save configuration.', 'error');
    return;
  }
  editSession.setDraft('configuration', fields);

  rememberFocus('configuration');
  setConfigSavingState(true);
  const outcome = await saveSection('configuration', setConfigStatus, 'configuration');
  setConfigSavingState(false);
  if (outcome?.kind !== 'saved') return;

  // The definition save is confirmed; the Default model mirror and the
  // workspace copies are separate follow-ups, each reported as itself.
  const status = await systemModelSaveStatus(outcome.receipt.system_model);
  const followUp = followUpNote(outcome.receipt);
  setConfigStatus(
    [status.message, followUp.text].filter(Boolean).join(' '),
    followUp.failed ? 'error' : status.type
  );
  if (!editSession.isDirty('configuration')) setConfigEditMode(false);
  if (followUp.text && !followUp.failed) showToast(followUp.text, 'success');
}

// followUpNote describes what happened to the workspace copies after a
// confirmed definition save. A failure is reported next to the save, not
// dropped, and never makes the save itself look unsaved.
function followUpNote(receipt) {
  return {
    text: carriedEditNote(receipt),
    failed: Boolean(String(receipt?.carried?.error || '').trim())
  };
}

function setConfigSavingState(isSaving) {
  const saveBtn = document.getElementById('saveConfigBtn');
  const editBtn = document.getElementById('editConfigBtn');
  if (saveBtn) {
    saveBtn.disabled = isSaving;
    if (isSaving) {
      saveBtn.innerHTML =
        '<span class="spinner-border spinner-border-sm me-1" role="status"></span>Saving...';
    } else {
      saveBtn.innerHTML = 'Save';
    }
  }
  if (editBtn) {
    editBtn.disabled = isSaving;
  }
  if (!isSaving) updateEditIndicators();
}

function refreshAgentConversationModel() {
  const api = window.PersonalAssistantModel;
  if (!api) return;
  const linked = Boolean(currentAgent?.system_model_linked);
  const pending = linked ? api.readDefault() : Promise.resolve(null);
  // Handle failure in renderLinkedNote; both notes share one authoritative read.
  for (const id of ['agentConversationModel', 'editConversationModel']) {
    void api.renderLinkedNote(document.getElementById(id), linked, { read: () => pending });
  }
}

async function systemModelSaveStatus(mirror) {
  if (!mirror) return { message: 'Configuration updated successfully.', type: 'success' };
  const status = await window.PersonalAssistantModel?.saveFeedback?.(mirror);
  refreshSystemModelDisplay();
  refreshAgentConversationModel();
  void window.PersonalAssistantModel?.refresh?.();
  return (
    status || {
      message: 'Agent saved. Check Default model in Settings to verify the conversational value.',
      type: 'error'
    }
  );
}

function setConfigStatus(message, type = 'info') {
  const statusEl = document.getElementById('configEditStatus');
  if (!statusEl) return;
  statusEl.textContent = message || '';
  if (!message) return;

  if (type === 'error') {
    statusEl.style.color = 'var(--agent-edit-danger)';
  } else if (type === 'success') {
    statusEl.style.color = 'var(--agent-edit-success)';
  } else {
    statusEl.style.color = 'var(--text-secondary)';
  }
}

// showPromptEditMode only shows or hides the editor; it never touches input.
function showPromptEditMode(enabled) {
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
}

// setPromptEditMode opens the editor on the draft (or the saved prompt), or
// closes it.
function setPromptEditMode(enabled) {
  showPromptEditMode(enabled);
  if (enabled) {
    populatePromptForm(sectionValues('prompt'));
    setPromptStatus('');
  }
}

function togglePromptEditMode() {
  if (isEditingPrompt) {
    requestCloseSection('prompt', () => {
      setPromptEditMode(false);
      setPromptStatus('');
      populatePromptForm();
      document.getElementById('editPromptBtn')?.focus();
    });
  } else {
    setPromptEditMode(true);
    updateEditIndicators();
  }
}

function populatePromptForm(values) {
  if (!currentAgent) return;
  const promptInput = document.getElementById('editSystemPrompt');
  if (promptInput) promptInput.value = (values || currentAgent).system_prompt || '';
}

async function savePromptChanges() {
  if (!currentAgent || !editSession) return;
  editSession.setDraft('prompt', {
    system_prompt: document.getElementById('editSystemPrompt')?.value ?? ''
  });

  rememberFocus('prompt');
  setPromptSavingState(true);
  const outcome = await saveSection('prompt', setPromptStatus, 'system prompt');
  setPromptSavingState(false);
  if (outcome?.kind !== 'saved') return;

  const followUp = followUpNote(outcome.receipt);
  setPromptStatus(
    ['System prompt saved.', followUp.failed ? followUp.text : ''].filter(Boolean).join(' '),
    followUp.failed ? 'error' : 'success'
  );
  if (!editSession.isDirty('prompt')) setPromptEditMode(false);
  if (followUp.text && !followUp.failed) showToast(followUp.text, 'success');
}

function setPromptSavingState(isSaving) {
  const saveBtn = document.getElementById('savePromptBtn');
  const editBtn = document.getElementById('editPromptBtn');
  if (saveBtn) {
    saveBtn.disabled = isSaving;
    if (isSaving) {
      saveBtn.innerHTML =
        '<span class="spinner-border spinner-border-sm me-1" role="status"></span>Saving...';
    } else {
      saveBtn.innerHTML = 'Save';
    }
  }
  if (editBtn) {
    editBtn.disabled = isSaving;
  }
  if (!isSaving) updateEditIndicators();
}

function setPromptStatus(message, type = 'info') {
  const statusEl = document.getElementById('promptEditStatus');
  if (!statusEl) return;
  statusEl.textContent = message || '';
  if (!message) return;

  if (type === 'error') {
    statusEl.style.color = 'var(--agent-edit-danger)';
  } else if (type === 'success') {
    statusEl.style.color = 'var(--agent-edit-success)';
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
    .map(value => String(value || '').trim())
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
  const enabledSkills = skills.filter(skill => skill?.enabled !== false);
  const dependencyMap = new Map();

  enabledSkills.forEach(skill => {
    const required = normalizeMCPServerList(skill?.required_mcp_servers || []);
    required.forEach(serverName => {
      const existing = dependencyMap.get(serverName) || {
        name: serverName,
        requiredBy: []
      };
      existing.requiredBy.push(skill?.name || '(Unnamed skill)');
      dependencyMap.set(serverName, existing);
    });
  });

  return Array.from(dependencyMap.values());
}

function getGlobalMCPServerConfig(name) {
  return (globalMCPServers || []).find(server => server?.name === name) || null;
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
  const globalMCPNames = new Set(
    (globalMCPServers || []).map(server => server?.name).filter(Boolean)
  );
  const skills = Array.isArray(currentAgentSkills) ? currentAgentSkills : [];
  const enabledSkills = skills.filter(skill => skill?.enabled !== false);
  const dependencies = getRequiredSkillMCPServerNames().map(dependency => {
    const status = getGlobalMCPServerStatus(dependency.name);
    const existsGlobal = globalMCPNames.has(dependency.name);
    return {
      ...dependency,
      requiredBy: normalizeMCPServerList(dependency.requiredBy),
      existsGlobal,
      status,
      running: isMCPServerRunningStatus(status),
      starting: isMCPServerStartingStatus(status),
      toolCount: getGlobalMCPToolCount(dependency.name)
    };
  });

  const missingModel = !String(currentAgent.model || '').trim();
  const unavailableRequiredMCP = dependencies.filter(dependency => !dependency.existsGlobal);

  const issues = [];
  if (missingModel) {
    issues.push({
      key: 'missing-model',
      title: 'Model not configured',
      detail: 'Set a model so this agent can process requests.',
      action: 'edit-config',
      actionLabel: 'Edit Config'
    });
  }
  if (unavailableRequiredMCP.length > 0) {
    issues.push({
      key: 'missing-global-mcp',
      title: `${unavailableRequiredMCP.length} required MCP server${unavailableRequiredMCP.length > 1 ? 's are' : ' is'} missing globally`,
      detail: unavailableRequiredMCP.map(dependency => dependency.name).join(', '),
      action: 'open-mcp-page',
      actionLabel: 'Open MCP Page'
    });
  }

  return {
    enabledPlugins,
    skills,
    enabledSkills,
    dependencies,
    requiredMCPCount: dependencies.length,
    availableRequiredMCPCount: dependencies.filter(dependency => dependency.existsGlobal).length,
    missingModel,
    issues
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
      ${issues
        .map(
          issue => `
        <div class="setup-health-item">
          <div>
            <p class="setup-health-item-title">${escapeHtml(issue.title || 'Issue')}</p>
            <p class="setup-health-item-detail">${escapeHtml(issue.detail || '')}</p>
          </div>
          ${
            issue.action
              ? `
            <button
              type="button"
              class="modern-btn modern-btn-secondary"
              style="padding: 6px 10px; font-size: 12px;"
              data-health-action="${escapeHtml(issue.action)}"
            >
              ${escapeHtml(issue.actionLabel || 'Fix')}
            </button>
          `
              : ''
          }
        </div>
      `
        )
        .join('')}
    </div>
  `;

  banner.querySelectorAll('[data-health-action]').forEach(button => {
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

  summaryText.textContent =
    capabilityState.issues.length === 0
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

  dependenciesContainer.innerHTML = capabilityState.dependencies
    .map(dependency => {
      let actionButton = '';
      if (!dependency.existsGlobal) {
        actionButton = `
        <a class="modern-btn modern-btn-secondary" style="padding: 6px 10px; font-size: 12px;" href="/mcp">Add Server</a>
      `;
      } else {
        actionButton = `
        <a class="modern-btn modern-btn-secondary" style="padding: 6px 10px; font-size: 12px;" href="/">Bind in Workspace</a>
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
          <span class="capability-pill ${dependency.running ? 'ready' : dependency.starting ? 'warning' : 'warning'}">${dependency.running ? 'Global: running' : dependency.starting ? 'Global: starting' : `Global: ${dependency.status}`}</span>
          ${actionButton}
        </div>
      </div>
    `;
    })
    .join('');
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
  const plugins = Array.isArray(pluginsRaw) ? pluginsRaw : pluginsRaw ? [pluginsRaw] : [];

  if (plugins.length === 0) {
    container.innerHTML = '<div class="empty-message">No plugins enabled</div>';
    return;
  }

  // Show loading while checking config status
  container.innerHTML =
    '<div class="text-center py-4" style="color: var(--text-secondary);">Loading plugins...</div>';

  // Check configuration status for all plugins in parallel
  const configChecks = await Promise.all(
    plugins.map(async plugin => {
      const name =
        typeof plugin === 'string' ? plugin : plugin?.name || plugin?.id || plugin?.plugin || '';
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
    const rawName =
      typeof plugin === 'string' ? plugin : plugin?.name || plugin?.id || plugin?.plugin || '';
    const displayName =
      typeof plugin === 'object' && plugin?.metadata?.name
        ? plugin.metadata.name
        : stripVersionSuffix(rawName);
    const version =
      typeof plugin === 'object' && plugin ? plugin.version || plugin?.meta?.version || '' : '';
    const hasConfig = configStatus.get(rawName) || false;

    const item = document.createElement('div');
    item.className = 'plugin-item';
    item.style.cssText = 'display: flex; justify-content: space-between; align-items: center;';
    item.innerHTML = `
            <div>
                <div class="plugin-name">${escapeHtml(displayName || '(unknown plugin)')}</div>
                ${version ? `<div class="plugin-version">v${escapeHtml(version)}</div>` : ''}
            </div>
            ${
              hasConfig
                ? `
                <button class="modern-btn modern-btn-secondary plugin-config-btn"
                        data-plugin-name="${escapeHtml(rawName)}"
                        title="Configure plugin"
                        style="padding: 0.4rem 0.8rem; font-size: 12px;">
                    <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor">
                        <path d="M12,15.5A3.5,3.5 0 0,1 8.5,12A3.5,3.5 0 0,1 12,8.5A3.5,3.5 0 0,1 15.5,12A3.5,3.5 0 0,1 12,15.5M19.43,12.97C19.47,12.65 19.5,12.33 19.5,12C19.5,11.67 19.47,11.34 19.43,11L21.54,9.37C21.73,9.22 21.78,8.95 21.66,8.73L19.66,5.27C19.54,5.05 19.27,4.96 19.05,5.05L16.56,6.05C16.04,5.66 15.5,5.32 14.87,5.07L14.5,2.42C14.46,2.18 14.25,2 14,2H10C9.75,2 9.54,2.18 9.5,2.42L9.13,5.07C8.5,5.32 7.96,5.66 7.44,6.05L4.95,5.05C4.73,4.96 4.46,5.05 4.34,5.27L2.34,8.73C2.22,8.95 2.27,9.22 2.46,9.37L4.57,11C4.53,11.34 4.5,11.67 4.5,12C4.5,12.33 4.53,12.65 4.57,12.97L2.46,14.63C2.27,14.78 2.22,15.05 2.34,15.27L4.34,18.73C4.46,18.95 4.73,19.03 4.95,18.95L7.44,17.94C7.96,18.34 8.5,18.68 9.13,18.93L9.5,21.58C9.54,21.82 9.75,22 10,22H14C14.25,22 14.46,21.82 14.5,21.58L14.87,18.93C15.5,18.68 16.04,18.34 16.56,17.94L19.05,18.95C19.27,19.03 19.54,18.95 19.66,18.73L21.66,15.27C21.78,15.05 21.73,14.78 21.54,14.63L19.43,12.97Z"/>
                    </svg>
                    Configure
                </button>
            `
                : ''
            }
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
    button.addEventListener('click', async e => {
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
  const section = document.getElementById('tagsSection');
  if (!container) return;
  const tags = currentAgent.metadata?.tags || [];

  if (tags.length === 0) {
    if (section) section.style.display = 'none';
    return;
  }

  if (section) section.style.display = 'block';
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
  container.innerHTML =
    '<div class="text-center py-3" style="color: var(--text-secondary);">Loading skills...</div>';

  try {
    const response = await fetch(`/api/skills?agent=${encodeURIComponent(name)}`);
    if (response.status === 409) {
      const data = await response.json();
      const conflicts = Array.isArray(data.conflicts) ? data.conflicts : [];
      const conflictList = conflicts
        .map(conflict => {
          const paths = (conflict.paths || []).map(path => `<li>${escapeHtml(path)}</li>`).join('');
          return `<li><strong>${escapeHtml(conflict.name || '')}</strong><ul>${paths}</ul></li>`;
        })
        .join('');
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
    currentAgentLoadout = data.loadout || null;
    renderSkillSlotUsage();

    if (skills.length === 0) {
      container.innerHTML =
        '<div class="text-center py-3" style="color: var(--text-secondary);">No skills available for this agent.</div>';
      renderMCPServers();
      renderSetupHealthBanner();
      renderCapabilitiesCard();
      return;
    }

    skills.sort((a, b) =>
      (a.name || '').localeCompare(b.name || '', undefined, { sensitivity: 'base' })
    );

    // At cap (non-expert) means no *new* skills can be enabled; already-enabled
    // ones can still be toggled off.
    const atCap =
      Boolean(currentAgentLoadout) &&
      !currentAgentLoadout.expert_mode &&
      currentAgentLoadout.slots_used >= currentAgentLoadout.slot_cap;

    container.innerHTML = '';
    skills.forEach(skill => {
      const skillName = skill?.name || '(unnamed skill)';
      const rawSource = skill?.source || 'agent';
      const source = rawSource === 'local' ? 'agent' : rawSource;
      const description = skill?.description || 'No description';
      const isEnabled = skill?.enabled !== false;
      const hasScripts = Boolean(skill?.has_scripts);
      const isTrusted = Boolean(skill?.trusted);
      const validationErrors = Array.isArray(skill?.validation_errors)
        ? skill.validation_errors
        : [];
      const hasErrors = validationErrors.length > 0;

      // Only disabled skills are blocked at cap; enabled skills can always be
      // toggled off (and grandfathered over-cap skills stay usable).
      const capBlocked = atCap && !isEnabled;
      const capTooltip = currentAgentLoadout
        ? `At the ${currentAgentLoadout.stage} stage cap of ${currentAgentLoadout.slot_cap} active skills. Disable another skill or turn on Expert mode to add more.`
        : '';

      const item = document.createElement('div');
      item.style.cssText =
        'padding: 12px; border: 1px solid var(--border-color); border-radius: 8px; background: var(--bg-secondary); display: flex; flex-direction: column; gap: 8px;';
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
          <div class="form-check form-switch m-0"${capBlocked ? ` title="${escapeAttr(capTooltip)}"` : ''}>
            <input class="form-check-input" type="checkbox" data-action="toggle-skill-enabled" ${isEnabled ? 'checked' : ''} ${capBlocked ? 'disabled' : ''}>
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
    container.innerHTML =
      '<div class="text-center py-3" style="color: var(--danger-color);">Failed to load skills.</div>';
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
      // A 409 is the slot-cap rejection; its message names the stage and cap.
      if (response.status === 409 && typeof showToast === 'function') {
        showToast(message, 'error');
      }
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

// renderSkillSlotUsage updates the "N/M slots" badge and the expert-mode row
// from the current agent's loadout (populated by the skills list response).
function renderSkillSlotUsage() {
  const badge = document.getElementById('skillSlotUsage');
  const expertRow = document.getElementById('expertModeRow');
  const expertToggle = document.getElementById('expertModeToggle');
  const loadout = currentAgentLoadout;

  if (badge) {
    if (loadout && !loadout.expert_mode) {
      badge.textContent = `${loadout.slots_used}/${loadout.slot_cap} slots`;
      badge.title = `${loadout.stage} stage grants ${loadout.slot_cap} active skill slots`;
      badge.style.display = '';
    } else if (loadout && loadout.expert_mode) {
      badge.textContent = `${loadout.slots_used} active · expert`;
      badge.title = 'Expert mode: slot cap lifted';
      badge.style.display = '';
    } else {
      badge.style.display = 'none';
    }
  }

  if (expertRow && expertToggle) {
    if (loadout) {
      expertRow.style.setProperty('display', 'flex', 'important');
      expertToggle.checked = Boolean(loadout.expert_mode);
    } else {
      expertRow.style.setProperty('display', 'none', 'important');
    }
  }
}

// setAgentExpertMode PATCHes the agent's expert_mode flag, then reloads skills
// so the slot badge and at-cap toggles reflect the new state.
async function setAgentExpertMode(enabled) {
  const name = currentAgent?.name || agentName;
  if (!name) return false;
  // One definition write at a time from this page.
  try {
    editSession?.beginExternal('expert');
  } catch (error) {
    showToast?.(`${error.message} Try again when it has finished.`, 'error');
    return false;
  }
  try {
    let response;
    try {
      response = await fetch(`/api/agents/${encodeURIComponent(name)}`, {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ expert_mode: enabled })
      });
    } catch (error) {
      console.error('Expert mode change not confirmed:', error);
      response = null;
    }
    const data = response ? await response.json().catch(() => null) : null;
    if (response?.ok && data) {
      await renderSkills();
      return true;
    }
    if (response && !response.ok) {
      showToast?.(data?.message || data?.error || 'Failed to update expert mode', 'error');
      if (data?.code === AGENT_CHANGED_ON_DISK) await refreshAgentDetails();
      return false;
    }
    // No readable answer: the change may have landed. Show what is saved
    // instead of assuming it failed; it is not sent again.
    await renderSkills();
    showToast?.(
      'Ori could not confirm the expert mode change. The switch shows the saved setting; it was not sent again.',
      'error'
    );
    return 'unknown';
  } finally {
    editSession?.endExternal('expert');
    updateEditIndicators();
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
  container.innerHTML =
    '<div class="text-center py-3" style="color: var(--text-secondary);">Loading plugins...</div>';

  try {
    const response = await fetch('/api/plugins');
    if (!response.ok) throw new Error('Failed to load plugins');

    const data = await response.json();
    allAvailablePlugins = data.plugins || [];

    renderAvailablePlugins();
  } catch (error) {
    console.error('Error loading plugins:', error);
    container.innerHTML =
      '<div class="text-center py-3" style="color: var(--danger-color);">Failed to load plugins</div>';
  }
}

// Render available plugins with toggle switches
function renderAvailablePlugins() {
  const container = document.getElementById('availablePluginsList');
  const enabledPlugins = currentAgent?.enabled_plugins || [];
  // enabled_plugins can be an array of strings or objects with 'name' property
  const enabledNames = enabledPlugins.map(p => (typeof p === 'string' ? p : p?.name || p));
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
    item.style.cssText =
      'display: flex; justify-content: space-between; align-items: center; padding: 12px; background: var(--bg-secondary); border: 1px solid var(--border-color); border-radius: 8px;';
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
    container.innerHTML =
      '<p style="color: var(--text-secondary); font-size: 14px;">No enabled skills currently declare MCP dependencies for this agent.</p>';
    return;
  }

  container.innerHTML = '';
  dependencies.forEach(dependency => {
    const status = String(getGlobalMCPServerStatus(dependency.name) || 'missing').toLowerCase();
    const toolCount = getGlobalMCPToolCount(dependency.name);
    const existsGlobal = Boolean(getGlobalMCPServerConfig(dependency.name));
    const statusClass = !existsGlobal
      ? 'error'
      : isMCPServerRunningStatus(status)
        ? 'ready'
        : isMCPServerStartingStatus(status)
          ? 'warning'
          : 'warning';
    const statusLabel = !existsGlobal
      ? 'missing globally'
      : isMCPServerRunningStatus(status)
        ? 'global: running'
        : isMCPServerStartingStatus(status)
          ? 'global: starting'
          : `global: ${status}`;

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
  // The server refuses to delete an agent that works in workspaces; say so,
  // with the count, instead of confirming a delete that cannot happen.
  const attached = window.AgentOrigin ? window.AgentOrigin.attachedDeleteMessage(currentAgent) : '';
  if (attached) {
    alert(attached);
    return;
  }
  if (
    !confirm(`Are you sure you want to delete agent "${agentName}"? This action cannot be undone.`)
  ) {
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
  if (agent.appearance?.generated?.color) {
    return agent.appearance.generated.color;
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
  if (content) content.style.display = show ? 'none' : currentAgent ? 'block' : 'none';
  if (show) {
    hideMissingAgentState();
  }
}

function showError(message) {
  alert('Error: ' + message);
}
