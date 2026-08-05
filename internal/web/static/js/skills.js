// Skills Page

let skillsAll = [];
let skillsFiltered = [];
let selectedSource = 'all';
let selectedAgentName = '';
let defaultAgentName = '';
let editingSkillName = '';
let marketplaceResults = [];
let marketplaceInstalledSkills = [];
let marketplaceSearchBusy = false;
let marketplaceInstallBusy = false;
let marketplaceManageBusy = false;
let promptGenerationAbortController = null;
let promptGenerationRequestId = 0;

function getSkillPageDefaultAgent() {
  const page = document.getElementById('skillsPage');
  if (!page) return '';
  return page.dataset.currentAgent || '';
}

function getAgentDisplayName(agent) {
  if (!agent) return '';
  if (typeof agent === 'string') return agent;
  return agent.name || agent.id || '';
}

function safeText(value) {
  const text = value == null ? '' : String(value);
  if (typeof window.escapeHtml === 'function') {
    return window.escapeHtml(text);
  }
  return text
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');
}

function styleSystemModelProviderBadge(providerEl, providerName) {
  if (!providerEl) return;
  const provider = String(providerName || '').toLowerCase();
  switch (provider) {
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
}

async function refreshSystemModelDisplay() {
  const modelNameEl = document.getElementById('systemModelName');
  const providerEl = document.getElementById('navSystemModelProvider');
  const indicatorEl = document.getElementById('systemModelIndicator');
  if (!modelNameEl || !providerEl) return;

  try {
    const response = await fetch('/api/settings/system-model');
    if (!response.ok) throw new Error('Failed to fetch system model');
    const data = await response.json();

    if (data?.configured && data?.model) {
      const fullModelName = String(data.model);
      modelNameEl.textContent =
        fullModelName.length > 20 ? `${fullModelName.substring(0, 18)}...` : fullModelName;
      modelNameEl.title = fullModelName;

      if (data.provider) {
        providerEl.textContent = data.provider;
        providerEl.style.display = 'inline';
        styleSystemModelProviderBadge(providerEl, data.provider);
      } else {
        providerEl.style.display = 'none';
      }

      if (indicatorEl) {
        indicatorEl.title = `System Model: ${data.model} (${data.provider || 'unknown'}) - Click to configure`;
      }
      return;
    }

    modelNameEl.textContent = 'Not configured';
    providerEl.style.display = 'none';
    if (indicatorEl) {
      indicatorEl.title = 'System Model not configured - Click to set up';
    }
  } catch (error) {
    console.error('Failed to load system model:', error);
    modelNameEl.textContent = 'Error';
    providerEl.style.display = 'none';
  }
}

function setSkillsCount(count) {
  const countEl = document.getElementById('skillsCount');
  if (countEl) countEl.textContent = String(count);
}

function setSkillsMessage(message, isError) {
  const container = document.getElementById('skillsList');
  if (!container) return;
  const color = isError ? 'var(--danger-color)' : 'var(--text-secondary)';
  container.innerHTML = `<div class="text-center py-3" style="color: ${color};">${safeText(message)}</div>`;
}

function showSkillsError(messageHtml) {
  const box = document.getElementById('skillsError');
  if (!box) return;
  box.innerHTML = messageHtml;
  box.classList.remove('d-none');
}

function clearSkillsError() {
  const box = document.getElementById('skillsError');
  if (!box) return;
  box.classList.add('d-none');
  box.innerHTML = '';
}

function getSourceLabel(source) {
  if (!source) return 'agent';
  const lower = String(source).toLowerCase();
  if (lower === 'local') return 'agent';
  if (lower === 'repo') return 'repo';
  if (lower === '.agents' || lower === 'agents') return '.agents';
  return lower;
}

function getSourceTheme(source) {
  switch (String(source || '').toLowerCase()) {
    case 'agent':
      return { bg: 'rgba(59, 130, 246, 0.22)', color: 'var(--text-primary)' };
    case 'repo':
      return { bg: 'rgba(168, 85, 247, 0.22)', color: 'var(--text-primary)' };
    case '.agents':
      return { bg: 'rgba(16, 185, 129, 0.22)', color: 'var(--text-primary)' };
    case 'personal':
      return { bg: 'rgba(92, 92, 94, 0.28)', color: 'var(--text-primary)' };
    default:
      return { bg: 'rgba(148, 163, 184, 0.22)', color: 'var(--text-primary)' };
  }
}

function renderSourceFilters() {
  const container = document.getElementById('skillsSourceFilters');
  if (!container) return;

  if (!Array.isArray(skillsAll) || skillsAll.length === 0) {
    container.classList.add('d-none');
    container.innerHTML = '';
    return;
  }

  const counts = new Map();
  skillsAll.forEach(skill => {
    const source = getSourceLabel(skill?.source);
    counts.set(source, (counts.get(source) || 0) + 1);
  });

  // Hide filter row when only one source is present
  if (counts.size <= 1) {
    container.classList.add('d-none');
    container.innerHTML = '';
    if (selectedSource !== 'all') selectedSource = 'all';
    return;
  }

  if (selectedSource !== 'all' && !counts.has(selectedSource)) {
    selectedSource = 'all';
  }

  const sources = ['all', ...[...counts.keys()].sort()];
  container.innerHTML = sources
    .map(source => {
      const count = source === 'all' ? skillsAll.length : counts.get(source) || 0;
      const isActive = selectedSource === source;
      const theme =
        source === 'all'
          ? { bg: 'var(--bg-tertiary)', color: 'var(--text-primary)' }
          : getSourceTheme(source);
      const label = source === 'all' ? 'All' : source;
      const style = [
        'font-size: 12px',
        'padding: 4px 12px',
        'border-radius: 999px',
        'cursor: pointer',
        `border: 1px solid ${isActive ? theme.color : 'var(--border-color)'}`,
        `background: ${isActive ? theme.bg : 'transparent'}`,
        `color: ${isActive ? theme.color : 'var(--text-secondary)'}`,
        'display: inline-flex',
        'align-items: center',
        'gap: 6px',
        'transition: background 120ms ease, border-color 120ms ease, color 120ms ease'
      ].join('; ');
      return `<button type="button" class="skill-source-chip" data-source="${safeText(source)}" style="${style}">${safeText(label)}<span style="opacity: 0.7;">${count}</span></button>`;
    })
    .join('');
  container.classList.remove('d-none');

  container.querySelectorAll('.skill-source-chip').forEach(btn => {
    btn.addEventListener('click', () => {
      selectedSource = btn.getAttribute('data-source') || 'all';
      renderSourceFilters();
      applySkillsFilter();
    });
  });
}

let pluginSkillNames = null; // null until first fetched
let lastRenderedSkills = null;

// ensurePluginSkillNames lazily fetches which skills are plugin-owned (read-only)
// so they can be badged, then re-renders once. Cached; safe if /api/plugins is absent.
function ensurePluginSkillNames() {
  if (pluginSkillNames !== null) return;
  pluginSkillNames = new Set();
  fetch('/api/plugins')
    .then(r => (r.ok ? r.json() : { plugins: [] }))
    .then(d => {
      (d.plugins || []).forEach(p => (p.skills || []).forEach(s => pluginSkillNames.add(s)));
      if (lastRenderedSkills) renderSkills(lastRenderedSkills);
    })
    .catch(() => {});
}

function renderSkills(skills) {
  const container = document.getElementById('skillsList');
  if (!container) return;

  if (!skills || skills.length === 0) {
    setSkillsCount(0);
    setSkillsMessage('No skills available for this agent.', false);
    return;
  }

  lastRenderedSkills = skills;
  ensurePluginSkillNames();
  const pNames = pluginSkillNames || new Set();
  container.innerHTML = '';
  skills.forEach(skill => {
    const name = skill?.name || '(unnamed skill)';
    const description = skill?.description || 'No description';
    const source = getSourceLabel(skill?.source);
    const isPlugin = pNames.has(name);
    const isEditable =
      !isPlugin && (source === 'agent' || source === '.agents' || source === 'personal');
    const canDelete = source === 'agent';
    const validationErrors = Array.isArray(skill?.validation_errors) ? skill.validation_errors : [];
    const hasErrors = validationErrors.length > 0;
    const hasScripts = Boolean(skill?.has_scripts);
    const isTrusted = Boolean(skill?.trusted);
    const requiresTrust = hasScripts && !isTrusted;

    const card = document.createElement('div');
    card.className = 'plugin-item';
    card.style.cursor = 'default';
    card.style.display = 'flex';
    card.style.flexDirection = 'column';
    card.style.height = '100%';
    card.style.overflow = 'visible';

    const sourceTheme = getSourceTheme(source);
    const badges = [];
    badges.push(
      `<span class="badge" style="background: ${sourceTheme.bg}; color: ${sourceTheme.color}; font-size: 10px; text-transform: uppercase; letter-spacing: 0.3px;">${safeText(source)}</span>`
    );
    if (isPlugin) {
      badges.push(
        '<span class="badge bg-info" style="font-size: 10px; text-transform: uppercase; letter-spacing: 0.3px;">plugin</span>'
      );
    }
    if (hasErrors) {
      badges.push(
        '<span class="badge bg-danger" style="font-size: 10px; text-transform: uppercase; letter-spacing: 0.3px;">invalid</span>'
      );
    }
    if (hasScripts) {
      badges.push(
        `<span class="badge ${isTrusted ? 'bg-success' : 'bg-danger'}" style="font-size: 10px; text-transform: uppercase; letter-spacing: 0.3px;">${isTrusted ? 'trusted' : 'untrusted'}</span>`
      );
    }

    const errorSummary = hasErrors
      ? `<div style="font-size: 11px; color: var(--danger-color); margin-top: 6px;">${validationErrors.length} validation error${validationErrors.length === 1 ? '' : 's'}</div>`
      : '';

    const skillPath = skill?.path || '';
    const pathHtml = skillPath
      ? `<div style="font-size: 11px; color: var(--text-secondary); margin-top: 6px; opacity: 0.7; display: flex; align-items: center; gap: 4px; min-width: 0;" title="${safeText(skillPath)}">
           <svg width="10" height="10" viewBox="0 0 24 24" fill="currentColor" style="flex-shrink: 0;">
             <path d="M10,4H4C2.89,4 2,4.89 2,6V18A2,2 0 0,0 4,20H20A2,2 0 0,0 22,18V8C22,6.89 21.1,6 20,6H12L10,4Z"/>
           </svg><span style="white-space: nowrap; overflow: hidden; text-overflow: ellipsis; min-width: 0;">${safeText(skillPath)}</span></div>`
      : '';

    const cleanDescription = cleanSkillDescriptionForCard(description);

    card.innerHTML = `
      <div style="display: flex; align-items: flex-start; justify-content: space-between; gap: 8px; flex: 1 1 auto;">
        <div style="min-width: 0; flex: 1;">
          <div style="font-weight: 600; color: var(--text-primary);">${safeText(name)}</div>
          <div style="font-size: 12px; color: var(--text-secondary); margin-top: 4px; display: -webkit-box; -webkit-line-clamp: 3; -webkit-box-orient: vertical; overflow: hidden; line-height: 1.4;">${safeText(cleanDescription)}</div>
          ${pathHtml}
          ${errorSummary}
        </div>
        <div style="display: flex; flex-wrap: wrap; gap: 4px; justify-content: flex-end;">${badges.join('')}</div>
      </div>
      <div style="display: flex; align-items: center; gap: 8px; flex-wrap: wrap; margin-top: auto; padding-top: 12px;">
        <button class="modern-btn modern-btn-primary btn-sm" data-action="details">Details</button>
        <button class="modern-btn modern-btn-secondary btn-sm" data-action="run" ${requiresTrust || hasErrors ? 'disabled' : ''}>Run</button>
        <div class="dropdown" style="margin-left: auto;">
          <button class="modern-btn modern-btn-secondary btn-sm" type="button" data-bs-toggle="dropdown" aria-expanded="false" aria-label="More actions" style="padding: 4px 8px;">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor"><path d="M12,16A2,2 0 0,1 14,18A2,2 0 0,1 12,20A2,2 0 0,1 10,18A2,2 0 0,1 12,16M12,10A2,2 0 0,1 14,12A2,2 0 0,1 12,14A2,2 0 0,1 10,12A2,2 0 0,1 12,10M12,4A2,2 0 0,1 14,6A2,2 0 0,1 12,8A2,2 0 0,1 10,6A2,2 0 0,1 12,4Z"/></svg>
          </button>
          <ul class="dropdown-menu dropdown-menu-end">
            ${
              isEditable
                ? `<li><button class="dropdown-item" type="button" data-action="edit">Edit</button></li>
               ${canDelete ? '<li><button class="dropdown-item text-danger" type="button" data-action="delete">Delete</button></li>' : ''}`
                : '<li><button class="dropdown-item" type="button" data-action="clone">Clone & Edit</button></li>'
            }
            ${hasScripts ? `<li><button class="dropdown-item" type="button" data-action="trust">${isTrusted ? 'Untrust' : 'Trust'}</button></li>` : ''}
          </ul>
        </div>
      </div>
    `;

    const detailsBtn = card.querySelector('[data-action="details"]');
    if (detailsBtn) {
      detailsBtn.addEventListener('click', () => openSkillDetails(skill));
    }

    const runBtn = card.querySelector('[data-action="run"]');
    if (runBtn) {
      runBtn.addEventListener('click', () => runSkill(name));
    }

    const editBtn = card.querySelector('[data-action="edit"]');
    if (editBtn) {
      editBtn.addEventListener('click', () => openSkillEditor(skill));
    }

    const deleteBtn = card.querySelector('[data-action="delete"]');
    if (deleteBtn) {
      deleteBtn.addEventListener('click', () => deleteSkill(skill));
    }

    const cloneBtn = card.querySelector('[data-action="clone"]');
    if (cloneBtn) {
      cloneBtn.addEventListener('click', () => cloneSkillToAgent(skill));
    }

    const trustBtn = card.querySelector('[data-action="trust"]');
    if (trustBtn) {
      trustBtn.addEventListener('click', () => toggleSkillTrust(skill));
    }

    container.appendChild(card);
  });

  setSkillsCount(skills.length);
}

function applySkillsFilter() {
  const input = document.getElementById('skillsSearch');
  const query = input ? input.value.trim().toLowerCase() : '';

  let filtered = skillsAll.slice();

  if (selectedSource && selectedSource !== 'all') {
    filtered = filtered.filter(skill => getSourceLabel(skill?.source) === selectedSource);
  }

  if (query) {
    filtered = filtered.filter(skill => {
      const name = (skill?.name || '').toLowerCase();
      const description = (skill?.description || '').toLowerCase();
      const source = (skill?.source || '').toLowerCase();
      return name.includes(query) || description.includes(query) || source.includes(query);
    });
  }

  skillsFiltered = filtered;
  renderSkills(skillsFiltered);
}

async function loadSkills(agentName) {
  setSkillsMessage('Loading skills...', false);
  clearSkillsError();

  const url = agentName ? `/api/skills?agent=${encodeURIComponent(agentName)}` : '/api/skills';

  try {
    const response = await fetch(url);
    if (response.status === 409) {
      const data = await response.json();
      const conflicts = Array.isArray(data.conflicts) ? data.conflicts : [];
      const conflictHtml = conflicts
        .map(conflict => {
          const paths = (conflict.paths || []).map(path => `<li>${safeText(path)}</li>`).join('');
          return `<li><strong>${safeText(conflict.name)}</strong><ul>${paths}</ul></li>`;
        })
        .join('');
      showSkillsError(
        `<strong>Duplicate skill names detected.</strong><br/>Resolve these conflicts to continue.<ul>${conflictHtml}</ul>`
      );
      skillsAll = [];
      skillsFiltered = [];
      setSkillsCount(0);
      renderSourceFilters();
      setSkillsMessage('Resolve skill conflicts to view skills.', true);
      return;
    }
    if (!response.ok) {
      throw new Error('Failed to load skills');
    }
    const data = await response.json();
    skillsAll = Array.isArray(data.skills) ? data.skills : [];
    skillsAll.sort((a, b) =>
      (a.name || '').localeCompare(b.name || '', undefined, { sensitivity: 'base' })
    );
    renderSourceFilters();
    applySkillsFilter();
  } catch (error) {
    console.error('Failed to load skills:', error);
    skillsAll = [];
    skillsFiltered = [];
    setSkillsCount(0);
    renderSourceFilters();
    setSkillsMessage('Failed to load skills.', true);
  }
}

function populateAgentSelect(agents, selected) {
  const select = document.getElementById('skillsAgentSelect');
  if (!select) return;

  const names = (agents || []).map(getAgentDisplayName).filter(name => name);

  names.sort((a, b) => a.localeCompare(b, undefined, { sensitivity: 'base' }));

  select.innerHTML = '';

  if (names.length === 0) {
    const option = document.createElement('option');
    option.value = '';
    option.textContent = 'No agents found';
    select.appendChild(option);
    select.disabled = true;
    selectedAgentName = '';
    return;
  }

  select.disabled = false;
  names.forEach(name => {
    const option = document.createElement('option');
    option.value = name;
    option.textContent = name;
    if (name === selected) {
      option.selected = true;
    }
    select.appendChild(option);
  });

  if (!names.includes(selected)) {
    select.selectedIndex = 0;
    selectedAgentName = names[0];
  } else {
    selectedAgentName = selected;
  }
}

async function loadAgents() {
  const select = document.getElementById('skillsAgentSelect');
  if (!select) return;

  try {
    const response = await fetch('/api/agents');
    if (!response.ok) {
      throw new Error('Failed to load agents');
    }
    const data = await response.json();
    const sessionAgent = window.sessionManager?.getActiveSession?.()?.agent_name;
    const assistantAgent = (data.agents || []).find(
      agent => getAgentDisplayName(agent) === 'Workspace Manager'
    );
    const selected = sessionAgent || defaultAgentName || getAgentDisplayName(assistantAgent);
    populateAgentSelect(data.agents || [], selected);
  } catch (error) {
    console.error('Failed to load agents:', error);
    populateAgentSelect([], defaultAgentName);
  }
}

function setupSkillsEvents() {
  const select = document.getElementById('skillsAgentSelect');
  const search = document.getElementById('skillsSearch');
  const refresh = document.getElementById('skillsRefreshBtn');
  const createBtn = document.getElementById('skillsCreateBtn');
  const generatePromptBtn = document.getElementById('skillsGeneratePromptBtn');
  const discoverBtn = document.getElementById('skillsDiscoverBtn');
  const saveBtn = document.getElementById('skillsSaveBtn');
  const generateConfigBtn = document.getElementById('skillsGenerateConfigBtn');
  const marketQuery = document.getElementById('skillsMarketplaceSearchQuery');
  const marketSearchBtn = document.getElementById('skillsMarketplaceSearchBtn');
  const marketPackageInput = document.getElementById('skillsMarketplacePackageInput');
  const marketInstallBtn = document.getElementById('skillsMarketplaceInstallBtn');
  const marketQuickFindSkillsBtn = document.getElementById('skillsMarketplaceQuickFindSkillsBtn');
  const marketManageTab = document.getElementById('skillsMarketplaceManageTab');
  const marketManageRefreshBtn = document.getElementById('skillsMarketplaceManageRefreshBtn');
  const marketCheckUpdatesBtn = document.getElementById('skillsMarketplaceCheckUpdatesBtn');
  const marketUpdateAllBtn = document.getElementById('skillsMarketplaceUpdateAllBtn');
  const marketRemoveInput = document.getElementById('skillsMarketplaceRemoveSkillInput');
  const marketRemoveBtn = document.getElementById('skillsMarketplaceRemoveBtn');

  if (select) {
    select.addEventListener('change', () => {
      selectedAgentName = select.value;
      loadSkills(selectedAgentName);
    });
  }

  if (search) {
    search.addEventListener('input', () => {
      applySkillsFilter();
    });
  }

  if (refresh) {
    refresh.addEventListener('click', () => {
      loadSkills(selectedAgentName);
    });
  }

  if (createBtn) {
    createBtn.addEventListener('click', () => openSkillEditor(null));
  }

  if (generatePromptBtn) {
    generatePromptBtn.addEventListener('click', () => {
      generatePromptWithAssistant(true);
    });
  }

  if (discoverBtn) {
    discoverBtn.addEventListener('click', () => openSkillsMarketplace());
  }

  if (saveBtn) {
    saveBtn.addEventListener('click', () => saveSkillEditor());
  }

  if (generateConfigBtn) {
    generateConfigBtn.addEventListener('click', () => generateSkillConfiguration());
  }

  if (marketSearchBtn) {
    marketSearchBtn.addEventListener('click', () => searchMarketplaceSkills());
  }

  if (marketQuery) {
    marketQuery.addEventListener('keydown', event => {
      if (event.key === 'Enter') {
        event.preventDefault();
        searchMarketplaceSkills();
      }
    });
  }

  if (marketInstallBtn) {
    marketInstallBtn.addEventListener('click', () => {
      const value = marketPackageInput ? marketPackageInput.value : '';
      installMarketplacePackage(value);
    });
  }

  if (marketPackageInput) {
    marketPackageInput.addEventListener('keydown', event => {
      if (event.key === 'Enter') {
        event.preventDefault();
        installMarketplacePackage(marketPackageInput.value);
      }
    });
  }

  if (marketQuickFindSkillsBtn) {
    marketQuickFindSkillsBtn.addEventListener('click', () => {
      const findSkillsPackage = 'vercel-labs/skills@find-skills';
      if (marketPackageInput) marketPackageInput.value = findSkillsPackage;
      installMarketplacePackage(findSkillsPackage);
    });
  }

  if (marketManageTab) {
    marketManageTab.addEventListener('shown.bs.tab', () => {
      if (!Array.isArray(marketplaceInstalledSkills) || marketplaceInstalledSkills.length === 0) {
        loadInstalledMarketplaceSkills();
      }
    });
  }

  if (marketManageRefreshBtn) {
    marketManageRefreshBtn.addEventListener('click', () => loadInstalledMarketplaceSkills());
  }

  if (marketCheckUpdatesBtn) {
    marketCheckUpdatesBtn.addEventListener('click', () => checkMarketplaceUpdates());
  }

  if (marketUpdateAllBtn) {
    marketUpdateAllBtn.addEventListener('click', () => updateMarketplaceSkills());
  }

  if (marketRemoveBtn) {
    marketRemoveBtn.addEventListener('click', () => {
      const value = marketRemoveInput ? marketRemoveInput.value : '';
      removeMarketplaceSkill(value);
    });
  }

  if (marketRemoveInput) {
    marketRemoveInput.addEventListener('keydown', event => {
      if (event.key === 'Enter') {
        event.preventDefault();
        removeMarketplaceSkill(marketRemoveInput.value);
      }
    });
  }
}

function cleanSkillDescriptionForCard(description) {
  const text = String(description || '').trim();
  if (!text) return 'No description';
  return (
    text
      .replace(/<example>[\s\S]*?<\/example>/gi, '')
      .replace(/<commentary>[\s\S]*?<\/commentary>/gi, '')
      .replace(/<[^>]+>/g, ' ')
      .replace(/\s+/g, ' ')
      .trim() || text
  );
}

function getDetailsModal() {
  const el = document.getElementById('skillsDetailsModal');
  if (!el) return null;
  return bootstrap.Modal.getOrCreateInstance(el);
}

async function openSkillDetails(skill) {
  if (!skill) return;

  const titleEl = document.getElementById('skillsDetailsTitle');
  const badgesEl = document.getElementById('skillsDetailsBadges');
  const descEl = document.getElementById('skillsDetailsDescription');
  const pathRow = document.getElementById('skillsDetailsPathRow');
  const pathEl = document.getElementById('skillsDetailsPath');
  const errorsEl = document.getElementById('skillsDetailsErrors');
  const promptEl = document.getElementById('skillsDetailsPrompt');
  const promptStatus = document.getElementById('skillsDetailsPromptStatus');
  const actionsEl = document.getElementById('skillsDetailsActions');

  const name = skill?.name || '(unnamed skill)';
  const description = skill?.description || 'No description';
  const source = getSourceLabel(skill?.source);
  const isEditable = source === 'agent' || source === '.agents' || source === 'personal';
  const canDelete = source === 'agent';
  const validationErrors = Array.isArray(skill?.validation_errors) ? skill.validation_errors : [];
  const hasErrors = validationErrors.length > 0;
  const hasScripts = Boolean(skill?.has_scripts);
  const isTrusted = Boolean(skill?.trusted);
  const requiresTrust = hasScripts && !isTrusted;
  const skillPath = skill?.path || '';

  if (titleEl) titleEl.textContent = name;

  if (badgesEl) {
    const sourceTheme = getSourceTheme(source);
    const badges = [
      `<span class="badge" style="background: ${sourceTheme.bg}; color: ${sourceTheme.color}; font-size: 10px; text-transform: uppercase; letter-spacing: 0.3px;">${safeText(source)}</span>`
    ];
    if (hasErrors) {
      badges.push(
        '<span class="badge bg-danger" style="font-size: 10px; text-transform: uppercase; letter-spacing: 0.3px;">invalid</span>'
      );
    }
    if (hasScripts) {
      badges.push(
        `<span class="badge ${isTrusted ? 'bg-success' : 'bg-danger'}" style="font-size: 10px; text-transform: uppercase; letter-spacing: 0.3px;">${isTrusted ? 'trusted' : 'untrusted'}</span>`
      );
    }
    badgesEl.innerHTML = badges.join('');
  }

  if (descEl) descEl.textContent = description;

  if (pathRow && pathEl) {
    if (skillPath) {
      pathEl.textContent = skillPath;
      pathRow.classList.remove('d-none');
    } else {
      pathRow.classList.add('d-none');
    }
  }

  if (errorsEl) {
    if (hasErrors) {
      errorsEl.innerHTML = `<strong>Validation errors</strong><ul class="mb-0 mt-1">${validationErrors.map(err => `<li>${safeText(err)}</li>`).join('')}</ul>`;
      errorsEl.classList.remove('d-none');
    } else {
      errorsEl.classList.add('d-none');
      errorsEl.innerHTML = '';
    }
  }

  if (promptEl) promptEl.textContent = '';
  if (promptStatus) promptStatus.textContent = 'Loading...';

  if (actionsEl) {
    const buttons = [];
    buttons.push(
      `<button type="button" class="modern-btn modern-btn-secondary btn-sm" data-detail-action="run" ${requiresTrust || hasErrors ? 'disabled' : ''}>Run</button>`
    );
    if (isEditable) {
      buttons.push(
        '<button type="button" class="modern-btn modern-btn-secondary btn-sm" data-detail-action="edit">Edit</button>'
      );
      if (canDelete) {
        buttons.push(
          '<button type="button" class="modern-btn modern-btn-secondary btn-sm" data-detail-action="delete">Delete</button>'
        );
      }
    } else {
      buttons.push(
        '<button type="button" class="modern-btn modern-btn-secondary btn-sm" data-detail-action="clone">Clone & Edit</button>'
      );
    }
    if (hasScripts) {
      buttons.push(
        `<button type="button" class="modern-btn modern-btn-secondary btn-sm" data-detail-action="trust">${isTrusted ? 'Untrust' : 'Trust'}</button>`
      );
    }
    actionsEl.innerHTML = buttons.join('');

    actionsEl.querySelectorAll('[data-detail-action]').forEach(btn => {
      const action = btn.getAttribute('data-detail-action');
      btn.addEventListener('click', () => {
        const modal = getDetailsModal();
        switch (action) {
          case 'run':
            if (modal) modal.hide();
            runSkill(name);
            break;
          case 'edit':
            if (modal) modal.hide();
            openSkillEditor(skill);
            break;
          case 'delete':
            if (modal) modal.hide();
            deleteSkill(skill);
            break;
          case 'clone':
            if (modal) modal.hide();
            cloneSkillToAgent(skill);
            break;
          case 'trust':
            toggleSkillTrust(skill);
            if (modal) modal.hide();
            break;
        }
      });
    });
  }

  const modal = getDetailsModal();
  if (modal) modal.show();

  if (!skill?.name || !selectedAgentName) {
    if (promptStatus) promptStatus.textContent = '';
    if (promptEl) promptEl.textContent = 'Prompt unavailable.';
    return;
  }

  try {
    const response = await fetch(
      `/api/skills/${encodeURIComponent(skill.name)}?agent=${encodeURIComponent(selectedAgentName)}`
    );
    if (!response.ok) {
      throw new Error('Failed to load skill prompt.');
    }
    const data = await response.json();
    if (promptEl) promptEl.textContent = data?.prompt || '(empty prompt)';
    if (promptStatus) promptStatus.textContent = '';
  } catch (error) {
    console.error('Failed to load skill details:', error);
    if (promptEl) promptEl.textContent = '';
    if (promptStatus) promptStatus.textContent = 'Failed to load prompt.';
  }
}

function getEditorModal() {
  const el = document.getElementById('skillsEditorModal');
  if (!el) return null;
  return bootstrap.Modal.getOrCreateInstance(el);
}

function getRunModal() {
  const el = document.getElementById('skillsRunModal');
  if (!el) return null;
  return bootstrap.Modal.getOrCreateInstance(el);
}

function getMarketplaceModal() {
  const el = document.getElementById('skillsMarketplaceModal');
  if (!el) return null;
  return bootstrap.Modal.getOrCreateInstance(el);
}

function setMarketplaceStatus(message, isError) {
  const box = document.getElementById('skillsMarketplaceStatus');
  if (!box) return;
  const text = (message || '').trim();
  if (!text) {
    box.classList.add('d-none');
    box.textContent = '';
    box.classList.remove('alert-danger', 'alert-success');
    return;
  }
  box.classList.remove('d-none', 'alert-danger', 'alert-success');
  box.classList.add(isError ? 'alert-danger' : 'alert-success');
  box.textContent = text;
}

function setMarketplaceResultsMessage(message) {
  const container = document.getElementById('skillsMarketplaceResults');
  if (!container) return;
  container.innerHTML = `<div class="text-center py-3" style="color: var(--text-secondary);">${safeText(message)}</div>`;
}

function setMarketplaceActionBusy(isBusy) {
  const searchBtn = document.getElementById('skillsMarketplaceSearchBtn');
  const installBtn = document.getElementById('skillsMarketplaceInstallBtn');
  const quickBtn = document.getElementById('skillsMarketplaceQuickFindSkillsBtn');
  const queryInput = document.getElementById('skillsMarketplaceSearchQuery');
  const packageInput = document.getElementById('skillsMarketplacePackageInput');
  const refreshBtn = document.getElementById('skillsMarketplaceManageRefreshBtn');
  const checkBtn = document.getElementById('skillsMarketplaceCheckUpdatesBtn');
  const updateBtn = document.getElementById('skillsMarketplaceUpdateAllBtn');
  const removeInput = document.getElementById('skillsMarketplaceRemoveSkillInput');
  const removeBtn = document.getElementById('skillsMarketplaceRemoveBtn');
  if (searchBtn) searchBtn.disabled = isBusy;
  if (installBtn) installBtn.disabled = isBusy;
  if (quickBtn) quickBtn.disabled = isBusy;
  if (queryInput) queryInput.disabled = isBusy;
  if (packageInput) packageInput.disabled = isBusy;
  if (refreshBtn) refreshBtn.disabled = isBusy;
  if (checkBtn) checkBtn.disabled = isBusy;
  if (updateBtn) updateBtn.disabled = isBusy;
  if (removeInput) removeInput.disabled = isBusy;
  if (removeBtn) removeBtn.disabled = isBusy;

  document.querySelectorAll('[data-market-install]').forEach(button => {
    button.disabled = isBusy;
  });
  document.querySelectorAll('[data-market-remove]').forEach(button => {
    button.disabled = isBusy;
  });
}

function renderMarketplaceResults(results) {
  const container = document.getElementById('skillsMarketplaceResults');
  if (!container) return;

  if (!Array.isArray(results) || results.length === 0) {
    setMarketplaceResultsMessage('No matching skills found. Try another query.');
    return;
  }

  const anyMarketplaceBusy =
    marketplaceSearchBusy || marketplaceInstallBusy || marketplaceManageBusy;

  container.innerHTML = results
    .map(result => {
      const packageSpec = result?.package || '';
      const repository = result?.repository || '';
      const skillName = result?.skill || '';
      const installs = result?.installs || '';
      const url = result?.url || '';
      const urlHtml = url
        ? `<a href="${safeText(url)}" target="_blank" rel="noopener noreferrer" style="font-size: 12px;">View on skills.sh</a>`
        : '';

      return `
      <div class="plugin-item" style="display: flex; flex-direction: column; gap: 10px;">
        <div style="display: flex; justify-content: space-between; align-items: flex-start; gap: 8px;">
          <div style="min-width: 0;">
            <div style="font-weight: 600; color: var(--text-primary); word-break: break-word;">${safeText(skillName || packageSpec)}</div>
            <div style="font-size: 12px; color: var(--text-secondary); margin-top: 4px; word-break: break-word;">${safeText(repository)}</div>
            <div style="font-size: 11px; color: var(--text-secondary); margin-top: 6px; opacity: 0.85; word-break: break-all;">${safeText(packageSpec)}</div>
          </div>
          ${installs ? `<span class="badge bg-secondary" style="font-size: 10px;">${safeText(installs)}</span>` : ''}
        </div>
        <div style="display: flex; justify-content: space-between; align-items: center; gap: 8px;">
          ${urlHtml}
          <button class="modern-btn modern-btn-primary btn-sm" data-market-install="${safeText(packageSpec)}" ${anyMarketplaceBusy ? 'disabled' : ''}>
            Install
          </button>
        </div>
      </div>
    `;
    })
    .join('');

  container.querySelectorAll('[data-market-install]').forEach(button => {
    button.addEventListener('click', () => {
      const packageSpec = button.getAttribute('data-market-install') || '';
      installMarketplacePackage(packageSpec);
    });
  });
}

function setMarketplaceInstalledMessage(message) {
  const container = document.getElementById('skillsMarketplaceInstalledList');
  if (!container) return;
  container.innerHTML = `<div class="text-center py-3" style="color: var(--text-secondary);">${safeText(message)}</div>`;
}

function renderInstalledMarketplaceSkills(skills) {
  const container = document.getElementById('skillsMarketplaceInstalledList');
  if (!container) return;

  if (!Array.isArray(skills) || skills.length === 0) {
    setMarketplaceInstalledMessage('No global skills are installed yet.');
    return;
  }

  const anyMarketplaceBusy =
    marketplaceSearchBusy || marketplaceInstallBusy || marketplaceManageBusy;

  container.innerHTML = skills
    .map(skill => {
      const name = skill?.name || '';
      const path = skill?.path || '';
      const agents = skill?.agents || '';
      const scope = skill?.scope || '';

      return `
      <div class="plugin-item" style="display: flex; flex-direction: column; gap: 10px;">
        <div style="display: flex; justify-content: space-between; align-items: flex-start; gap: 8px;">
          <div style="min-width: 0;">
            <div style="font-weight: 600; color: var(--text-primary); word-break: break-word;">${safeText(name)}</div>
            <div style="font-size: 11px; color: var(--text-secondary); margin-top: 6px; opacity: 0.85; word-break: break-all;">${safeText(path)}</div>
          </div>
          ${scope ? `<span class="badge bg-secondary" style="font-size: 10px;">${safeText(scope)}</span>` : ''}
        </div>
        <div style="display: flex; justify-content: space-between; align-items: center; gap: 8px;">
          <small style="color: var(--text-secondary);">Agents: ${safeText(agents || 'unknown')}</small>
          <button class="modern-btn modern-btn-secondary btn-sm" data-market-remove="${safeText(name)}" ${anyMarketplaceBusy ? 'disabled' : ''}>
            Remove
          </button>
        </div>
      </div>
    `;
    })
    .join('');

  container.querySelectorAll('[data-market-remove]').forEach(button => {
    button.addEventListener('click', () => {
      const skillName = button.getAttribute('data-market-remove') || '';
      removeMarketplaceSkill(skillName);
    });
  });
}

async function loadInstalledMarketplaceSkills(force = false) {
  if (!force && (marketplaceSearchBusy || marketplaceInstallBusy || marketplaceManageBusy)) return;

  marketplaceManageBusy = true;
  setMarketplaceActionBusy(true);
  setMarketplaceStatus('Loading installed skills...', false);
  setMarketplaceInstalledMessage('Loading installed skills...');

  try {
    const response = await fetch('/api/skills/marketplace/installed');
    const data = await response.json().catch(() => ({}));
    if (!response.ok) {
      const details = data?.details ? ` ${data.details}` : '';
      throw new Error((data?.error || 'Failed to list installed skills.') + details);
    }

    marketplaceInstalledSkills = Array.isArray(data?.skills) ? data.skills : [];
    renderInstalledMarketplaceSkills(marketplaceInstalledSkills);
    setMarketplaceStatus(
      `Loaded ${marketplaceInstalledSkills.length} installed skill${marketplaceInstalledSkills.length === 1 ? '' : 's'}.`,
      false
    );
  } catch (error) {
    console.error('Failed to load installed marketplace skills:', error);
    setMarketplaceStatus(error?.message || 'Failed to load installed skills.', true);
    setMarketplaceInstalledMessage('Could not load installed skills.');
  } finally {
    marketplaceManageBusy = false;
    setMarketplaceActionBusy(false);
  }
}

async function checkMarketplaceUpdates() {
  if (marketplaceSearchBusy || marketplaceInstallBusy || marketplaceManageBusy) return;

  marketplaceManageBusy = true;
  setMarketplaceActionBusy(true);
  setMarketplaceStatus('Checking for skill updates...', false);

  try {
    const response = await fetch('/api/skills/marketplace/check', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: '{}'
    });
    const data = await response.json().catch(() => ({}));
    if (!response.ok) {
      const details = data?.details ? ` ${data.details}` : '';
      throw new Error((data?.error || 'Failed to check updates.') + details);
    }

    const summary = data?.summary || 'Update check complete.';
    setMarketplaceStatus(summary, false);
    if (typeof showToast === 'function') {
      showToast(summary, 'success');
    }
  } catch (error) {
    console.error('Failed to check marketplace updates:', error);
    setMarketplaceStatus(error?.message || 'Failed to check updates.', true);
    if (typeof showToast === 'function') {
      showToast('Failed to check skill updates.', 'error');
    }
  } finally {
    marketplaceManageBusy = false;
    setMarketplaceActionBusy(false);
  }
}

async function updateMarketplaceSkills() {
  if (marketplaceSearchBusy || marketplaceInstallBusy || marketplaceManageBusy) return;

  marketplaceManageBusy = true;
  setMarketplaceActionBusy(true);
  setMarketplaceStatus('Updating installed skills...', false);

  try {
    const response = await fetch('/api/skills/marketplace/update', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: '{}'
    });
    const data = await response.json().catch(() => ({}));
    if (!response.ok) {
      const details = data?.details ? ` ${data.details}` : '';
      throw new Error((data?.error || 'Failed to update installed skills.') + details);
    }

    const summary = data?.summary || 'Installed skills updated.';
    setMarketplaceStatus(summary, false);
    if (typeof showToast === 'function') {
      showToast(summary, 'success');
    }
    await loadInstalledMarketplaceSkills(true);
    await loadSkills(selectedAgentName);
  } catch (error) {
    console.error('Failed to update marketplace skills:', error);
    setMarketplaceStatus(error?.message || 'Failed to update installed skills.', true);
    if (typeof showToast === 'function') {
      showToast('Failed to update installed skills.', 'error');
    }
  } finally {
    marketplaceManageBusy = false;
    setMarketplaceActionBusy(false);
    renderInstalledMarketplaceSkills(marketplaceInstalledSkills);
  }
}

async function removeMarketplaceSkill(skillName) {
  const normalized = (skillName || '').trim();
  if (!normalized) {
    setMarketplaceStatus('Skill name is required.', true);
    return;
  }
  if (marketplaceSearchBusy || marketplaceInstallBusy || marketplaceManageBusy) return;

  const confirmed = window.confirm(`Remove skill "${normalized}" from global skills?`);
  if (!confirmed) return;

  marketplaceManageBusy = true;
  setMarketplaceActionBusy(true);
  setMarketplaceStatus(`Removing ${normalized}...`, false);

  const removeInput = document.getElementById('skillsMarketplaceRemoveSkillInput');
  if (removeInput) removeInput.value = normalized;

  try {
    const response = await fetch('/api/skills/marketplace/remove', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ skill: normalized })
    });
    const data = await response.json().catch(() => ({}));
    if (!response.ok) {
      const details = data?.details ? ` ${data.details}` : '';
      throw new Error((data?.error || 'Failed to remove skill.') + details);
    }

    const status = data?.status || '';
    if (status === 'not_found') {
      setMarketplaceStatus(`No installed skill matched "${normalized}".`, true);
      if (typeof showToast === 'function') {
        showToast(`No installed skill matched "${normalized}"`, 'error');
      }
    } else {
      const summary = data?.summary || `Removed ${normalized}.`;
      setMarketplaceStatus(summary, false);
      if (typeof showToast === 'function') {
        showToast(`Removed ${normalized}`, 'success');
      }
    }

    await loadInstalledMarketplaceSkills(true);
    await loadSkills(selectedAgentName);
  } catch (error) {
    console.error('Failed to remove marketplace skill:', error);
    setMarketplaceStatus(error?.message || 'Failed to remove skill.', true);
    if (typeof showToast === 'function') {
      showToast('Failed to remove skill.', 'error');
    }
  } finally {
    marketplaceManageBusy = false;
    setMarketplaceActionBusy(false);
    renderInstalledMarketplaceSkills(marketplaceInstalledSkills);
  }
}

async function searchMarketplaceSkills() {
  if (marketplaceSearchBusy || marketplaceInstallBusy || marketplaceManageBusy) return;
  const queryInput = document.getElementById('skillsMarketplaceSearchQuery');
  const query = queryInput ? queryInput.value.trim() : '';
  if (!query) {
    setMarketplaceStatus('Enter a search query first.', true);
    return;
  }

  marketplaceSearchBusy = true;
  setMarketplaceActionBusy(true);
  setMarketplaceStatus('Searching skills marketplace...', false);
  setMarketplaceResultsMessage('Searching...');

  try {
    const response = await fetch('/api/skills/marketplace/search', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ query, limit: 12 })
    });
    const data = await response.json().catch(() => ({}));
    if (!response.ok) {
      const details = data?.details ? ` ${data.details}` : '';
      throw new Error((data?.error || 'Failed to search marketplace.') + details);
    }

    marketplaceResults = Array.isArray(data?.results) ? data.results : [];
    renderMarketplaceResults(marketplaceResults);
    if (marketplaceResults.length === 0) {
      setMarketplaceStatus('Search completed. No skills matched your query.', true);
    } else {
      setMarketplaceStatus(
        `Found ${marketplaceResults.length} matching skill${marketplaceResults.length === 1 ? '' : 's'}.`,
        false
      );
    }
  } catch (error) {
    console.error('Failed to search marketplace skills:', error);
    setMarketplaceStatus(error?.message || 'Failed to search marketplace skills.', true);
    setMarketplaceResultsMessage('Could not load search results.');
  } finally {
    marketplaceSearchBusy = false;
    setMarketplaceActionBusy(false);
  }
}

async function installMarketplacePackage(packageSpec) {
  const normalized = (packageSpec || '').trim();
  if (!normalized) {
    setMarketplaceStatus('Package is required (owner/repo@skill).', true);
    return;
  }
  if (marketplaceSearchBusy || marketplaceInstallBusy || marketplaceManageBusy) return;

  marketplaceInstallBusy = true;
  setMarketplaceActionBusy(true);
  setMarketplaceStatus(`Installing ${normalized}...`, false);

  const packageInput = document.getElementById('skillsMarketplacePackageInput');
  if (packageInput) packageInput.value = normalized;

  try {
    const response = await fetch('/api/skills/marketplace/install', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ package: normalized })
    });
    const data = await response.json().catch(() => ({}));
    if (!response.ok) {
      const details = data?.details ? ` ${data.details}` : '';
      throw new Error((data?.error || 'Failed to install skill package.') + details);
    }

    setMarketplaceStatus(`Installed ${normalized}. Refreshing your skills list...`, false);
    if (typeof showToast === 'function') {
      showToast(`Installed ${normalized}`, 'success');
    }
    await loadSkills(selectedAgentName);
    await loadInstalledMarketplaceSkills(true);
  } catch (error) {
    console.error('Failed to install marketplace skill package:', error);
    setMarketplaceStatus(error?.message || 'Failed to install skill package.', true);
    if (typeof showToast === 'function') {
      showToast('Failed to install skill package.', 'error');
    }
  } finally {
    marketplaceInstallBusy = false;
    setMarketplaceActionBusy(false);
    renderMarketplaceResults(marketplaceResults);
  }
}

function openSkillsMarketplace() {
  const modal = getMarketplaceModal();
  if (modal) modal.show();

  setMarketplaceStatus('', false);
  if (!Array.isArray(marketplaceResults) || marketplaceResults.length === 0) {
    setMarketplaceResultsMessage('Search to discover installable skills.');
  } else {
    renderMarketplaceResults(marketplaceResults);
  }

  const queryInput = document.getElementById('skillsMarketplaceSearchQuery');
  if (queryInput && !queryInput.value.trim()) {
    queryInput.value = 'find-skills';
  }

  if (!Array.isArray(marketplaceInstalledSkills) || marketplaceInstalledSkills.length === 0) {
    loadInstalledMarketplaceSkills();
  } else {
    renderInstalledMarketplaceSkills(marketplaceInstalledSkills);
  }
}

function toYamlQuoted(value) {
  const normalized = value == null ? '' : String(value).trim();
  return JSON.stringify(normalized);
}

function resetPromptGenerationState() {
  if (promptGenerationAbortController) {
    promptGenerationAbortController.abort();
    promptGenerationAbortController = null;
  }
}

function extractAssistantPrompt(raw) {
  let text = String(raw || '').trim();
  if (!text) return '';

  if (text.startsWith('```')) {
    text = text.replace(/^```[a-zA-Z0-9_-]*\s*/u, '');
    text = text.replace(/\s*```$/u, '').trim();
  }

  const lower = text.toLowerCase();
  const promptPrefixIdx = lower.indexOf('prompt:');
  if (promptPrefixIdx === 0) {
    text = text.slice('prompt:'.length).trim();
  }

  if (
    (text.startsWith('"') && text.endsWith('"')) ||
    (text.startsWith("'") && text.endsWith("'"))
  ) {
    text = text.slice(1, -1).trim();
  }

  return text;
}

async function generatePromptWithAssistant(force = false) {
  if (promptGenerationAbortController) {
    promptGenerationAbortController.abort();
  }

  const promptInput = document.getElementById('skillPromptInput');
  const nameInput = document.getElementById('skillNameInput');
  const descriptionInput = document.getElementById('skillDescriptionInput');
  const errorBox = document.getElementById('skillsEditorError');
  if (!promptInput || !nameInput || !descriptionInput) return;

  const name = nameInput.value.trim();
  const description = descriptionInput.value.trim();
  if (!description) {
    if (errorBox) {
      errorBox.textContent = 'Add a description first so the assistant can generate a prompt.';
      errorBox.classList.remove('d-none');
    }
    return;
  }

  if (force && promptInput.value.trim()) {
    const confirmed = window.confirm('Replace the current prompt with a newly generated one?');
    if (!confirmed) return;
  }

  const controller = new AbortController();
  promptGenerationAbortController = controller;
  const requestId = ++promptGenerationRequestId;

  if (errorBox) {
    errorBox.classList.add('d-none');
    errorBox.textContent = '';
  }

  promptInput.value = 'Generating prompt...';

  try {
    const response = await fetch('/api/skills/generate-prompt', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        agent: selectedAgentName,
        name,
        description
      }),
      signal: controller.signal
    });
    const data = await response.json().catch(() => ({}));
    if (requestId !== promptGenerationRequestId) return;

    const details = typeof data?.details === 'string' ? data.details.trim() : '';
    const baseError = typeof data?.error === 'string' ? data.error : 'Failed to generate prompt.';
    if (!response.ok) throw new Error(`${baseError}${details ? ` ${details}` : ''}`);

    const generated = extractAssistantPrompt(data?.prompt || '');
    if (!generated) {
      throw new Error('Assistant returned an empty prompt.');
    }

    promptInput.value = generated;
  } catch (error) {
    if (controller.signal.aborted) return;
    console.error('Failed to generate prompt with assistant:', error);
    if (requestId !== promptGenerationRequestId) return;

    promptInput.value = '';
    if (errorBox) {
      errorBox.textContent = error?.message || 'Failed to generate prompt.';
      errorBox.classList.remove('d-none');
    }
  } finally {
    if (requestId === promptGenerationRequestId) {
      promptGenerationAbortController = null;
    }
  }
}

function toSkillDisplayName(value) {
  const normalized = (value || '').trim();
  if (!normalized) return 'New Skill';
  return normalized
    .split('-')
    .filter(Boolean)
    .map(part => part.charAt(0).toUpperCase() + part.slice(1))
    .join(' ');
}

function toYamlList(key, values) {
  const normalized = Array.isArray(values)
    ? values.map(item => String(item || '').trim()).filter(Boolean)
    : [];
  if (normalized.length === 0) {
    return [`  ${key}: []`];
  }
  return [`  ${key}:`, ...normalized.map(item => `    - ${toYamlQuoted(item)}`)];
}

function buildGeneratedSkillConfig() {
  const nameInput = document.getElementById('skillNameInput');
  const descriptionInput = document.getElementById('skillDescriptionInput');
  const promptInput = document.getElementById('skillPromptInput');

  const skillName = nameInput ? nameInput.value.trim() : '';
  const description = descriptionInput ? descriptionInput.value.trim() : '';
  const prompt = promptInput ? promptInput.value : '';
  const cleanedPrompt =
    (prompt || '').trim() === 'Loading...' ? '' : (prompt || '').replace(/\r\n/g, '\n').trim();

  const promptLines = cleanedPrompt
    ? cleanedPrompt.split('\n').map(line => `  ${line}`)
    : ['  Add default prompt behavior for this skill.'];

  const lines = [
    `display_name: ${toYamlQuoted(toSkillDisplayName(skillName))}`,
    `short_description: ${toYamlQuoted(description || 'Skill configuration for Ori Agent')}`,
    'default_prompt: |',
    ...promptLines,
    'dependencies:',
    ...toYamlList('tools', []),
    ...toYamlList('mcp_servers', []),
    ''
  ];

  return lines.join('\n');
}

function generateSkillConfiguration() {
  const openAIYamlInput = document.getElementById('skillOpenAIYamlInput');
  if (!openAIYamlInput) return;

  const existing = openAIYamlInput.value.trim();
  if (existing) {
    const confirmed = window.confirm(
      'Replace the current OpenAI configuration with a generated template?'
    );
    if (!confirmed) return;
  }

  openAIYamlInput.value = buildGeneratedSkillConfig();
}

function normalizeCloneSkillBaseName(name) {
  let normalized = (name || '')
    .toLowerCase()
    .replace(/[^a-z0-9-]+/g, '-')
    .replace(/-+/g, '-')
    .replace(/^-|-$/g, '');

  if (!normalized) normalized = 'skill';
  if (normalized.length > 56) {
    normalized = normalized.slice(0, 56).replace(/-+$/g, '');
  }
  return normalized || 'skill';
}

function buildCloneSkillName(name) {
  const existing = new Set(
    (skillsAll || []).map(skill => (skill?.name || '').toLowerCase()).filter(Boolean)
  );

  const base = `${normalizeCloneSkillBaseName(name)}-local`;
  let candidate = base;
  let index = 2;

  while (existing.has(candidate.toLowerCase())) {
    const suffix = `-${index}`;
    const maxBaseLength = Math.max(1, 64 - suffix.length);
    candidate = `${base.slice(0, maxBaseLength)}${suffix}`;
    index += 1;
  }

  return candidate;
}

async function cloneSkillToAgent(skill) {
  if (!skill?.name) return;

  const title = document.getElementById('skillsEditorTitle');
  const nameInput = document.getElementById('skillNameInput');
  const descriptionInput = document.getElementById('skillDescriptionInput');
  const promptInput = document.getElementById('skillPromptInput');
  const openAIYamlInput = document.getElementById('skillOpenAIYamlInput');
  const errorBox = document.getElementById('skillsEditorError');

  editingSkillName = '';
  resetPromptGenerationState();

  if (errorBox) {
    errorBox.classList.add('d-none');
    errorBox.textContent = '';
  }
  if (title) title.textContent = 'Clone Skill to Agent';
  if (nameInput) {
    nameInput.value = buildCloneSkillName(skill.name);
    nameInput.disabled = false;
  }
  if (descriptionInput) descriptionInput.value = skill?.description || '';
  if (promptInput) promptInput.value = 'Loading...';
  if (openAIYamlInput) openAIYamlInput.value = '';

  const modal = getEditorModal();
  if (modal) modal.show();

  try {
    const response = await fetch(
      `/api/skills/${encodeURIComponent(skill.name)}?agent=${encodeURIComponent(selectedAgentName)}`
    );
    if (!response.ok) {
      throw new Error('Failed to load source skill content.');
    }
    const data = await response.json();
    if (promptInput) promptInput.value = data?.prompt || '';
  } catch (error) {
    console.error('Failed to clone skill:', error);
    if (promptInput) promptInput.value = '';
    if (errorBox) {
      errorBox.textContent = 'Failed to load skill content for cloning.';
      errorBox.classList.remove('d-none');
    }
  }
}

function openSkillEditor(skill) {
  editingSkillName = skill?.name || '';
  resetPromptGenerationState();
  const title = document.getElementById('skillsEditorTitle');
  const nameInput = document.getElementById('skillNameInput');
  const descriptionInput = document.getElementById('skillDescriptionInput');
  const promptInput = document.getElementById('skillPromptInput');
  const openAIYamlInput = document.getElementById('skillOpenAIYamlInput');
  const errorBox = document.getElementById('skillsEditorError');

  if (errorBox) {
    errorBox.classList.add('d-none');
    errorBox.textContent = '';
  }

  if (title) title.textContent = skill ? 'Edit Skill' : 'Create Skill';

  if (nameInput) {
    nameInput.value = skill?.name || '';
    nameInput.disabled = Boolean(skill);
  }
  if (descriptionInput) descriptionInput.value = skill?.description || '';
  if (promptInput) promptInput.value = skill ? 'Loading...' : '';
  if (openAIYamlInput) openAIYamlInput.value = '';

  const modal = getEditorModal();
  if (modal) modal.show();

  if (skill && promptInput) {
    fetch(
      `/api/skills/${encodeURIComponent(skill.name)}?agent=${encodeURIComponent(selectedAgentName)}`
    )
      .then(res => (res.ok ? res.json() : Promise.reject(new Error('Failed to load skill'))))
      .then(data => {
        promptInput.value = data.prompt || '';
      })
      .catch(() => {
        promptInput.value = '';
        if (errorBox) {
          errorBox.textContent = 'Failed to load skill content.';
          errorBox.classList.remove('d-none');
        }
      });
    return;
  }
}

async function saveSkillEditor() {
  const nameInput = document.getElementById('skillNameInput');
  const descriptionInput = document.getElementById('skillDescriptionInput');
  const promptInput = document.getElementById('skillPromptInput');
  const openAIYamlInput = document.getElementById('skillOpenAIYamlInput');
  const errorBox = document.getElementById('skillsEditorError');

  const payload = {
    agent: selectedAgentName,
    name: nameInput ? nameInput.value.trim() : '',
    description: descriptionInput ? descriptionInput.value.trim() : '',
    prompt: promptInput ? promptInput.value : ''
  };
  const openAIYamlValue = openAIYamlInput ? openAIYamlInput.value.trim() : '';
  if (openAIYamlValue !== '') {
    payload.openai_yaml = openAIYamlValue;
  }

  if (payload.prompt.trim() === 'Loading...' || payload.prompt.trim() === 'Generating prompt...') {
    if (errorBox) {
      errorBox.textContent = 'Skill prompt is still generating. Try again in a moment.';
      errorBox.classList.remove('d-none');
    }
    return;
  }

  if (!payload.agent) {
    if (errorBox) {
      errorBox.textContent = 'Select an agent to save a skill.';
      errorBox.classList.remove('d-none');
    }
    return;
  }

  const isEdit = Boolean(editingSkillName);
  const url = isEdit ? `/api/skills/${encodeURIComponent(editingSkillName)}` : '/api/skills';
  const method = isEdit ? 'PUT' : 'POST';

  try {
    const response = await fetch(url, {
      method,
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload)
    });

    if (!response.ok) {
      const data = await response.json().catch(() => ({}));
      const details = typeof data?.details === 'string' ? data.details.trim() : '';
      const message = `${data?.error || 'Failed to save skill.'}${details ? ` ${details}` : ''}`;
      if (errorBox) {
        errorBox.textContent = message;
        errorBox.classList.remove('d-none');
      }
      return;
    }

    const modal = getEditorModal();
    if (modal) modal.hide();
    await loadSkills(selectedAgentName);
  } catch (error) {
    console.error('Failed to save skill:', error);
    if (errorBox) {
      errorBox.textContent = 'Failed to save skill.';
      errorBox.classList.remove('d-none');
    }
  }
}

async function deleteSkill(skill) {
  if (!skill?.name) return;
  if (!selectedAgentName) {
    showToast('Select an agent before deleting a skill.', 'error');
    return;
  }
  const confirmed = window.confirm(`Delete skill "${skill.name}"? This cannot be undone.`);
  if (!confirmed) return;

  try {
    const response = await fetch(
      `/api/skills/${encodeURIComponent(skill.name)}?agent=${encodeURIComponent(selectedAgentName)}`,
      {
        method: 'DELETE'
      }
    );
    if (!response.ok && response.status !== 204) {
      throw new Error('Failed to delete skill');
    }
    await loadSkills(selectedAgentName);
  } catch (error) {
    console.error('Failed to delete skill:', error);
    showToast('Failed to delete skill.', 'error');
  }
}

async function toggleSkillTrust(skill) {
  if (!skill?.name || !selectedAgentName) return;
  try {
    const response = await fetch(`/api/skills/${encodeURIComponent(skill.name)}/trust`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ agent: selectedAgentName, trusted: !skill.trusted })
    });
    if (!response.ok) {
      throw new Error('Failed to update skill trust');
    }
    await loadSkills(selectedAgentName);
  } catch (error) {
    console.error('Failed to update skill trust:', error);
    showToast('Failed to update skill trust.', 'error');
  }
}

async function runSkill(name) {
  if (!name) return;
  const output = document.getElementById('skillsRunOutput');
  const title = document.getElementById('skillsRunTitle');
  if (title) title.textContent = `Run Skill: ${name}`;
  if (output) output.textContent = 'Running...';

  const modal = getRunModal();
  if (modal) modal.show();

  try {
    const response = await fetch('/api/chat', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ question: `/${name}`, agent_name: selectedAgentName })
    });
    if (!response.ok) {
      throw new Error('Failed to run skill');
    }
    const data = await response.json();
    if (output) output.textContent = data.response || 'Skill completed.';
  } catch (error) {
    console.error('Failed to run skill:', error);
    if (output) output.textContent = 'Failed to run skill.';
  }
}

async function initializeSkillsPage() {
  defaultAgentName = getSkillPageDefaultAgent();
  setupSkillsEvents();
  await refreshSystemModelDisplay();
  await loadAgents();
  if (!selectedAgentName) {
    selectedAgentName = defaultAgentName;
  }
  await loadSkills(selectedAgentName);
}

if (document.readyState === 'loading') {
  document.addEventListener('DOMContentLoaded', initializeSkillsPage);
} else {
  initializeSkillsPage();
}
