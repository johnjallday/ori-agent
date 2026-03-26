(function() {
  'use strict';

  const CATALOG_AGENT = '__workspace_bootstrap_catalog__';
  const GENERIC_SYSTEM_TERMS = new Set([
    'browser',
    'chrome',
    'directory',
    'directories',
    'email',
    'file',
    'files',
    'filesystem',
    'finder',
    'folder',
    'folders',
    'notes',
    'safari',
    'web',
    'workspace'
  ]);
  const STOPWORDS = new Set([
    'a', 'an', 'and', 'app', 'apps', 'be', 'build', 'create', 'for', 'from', 'goal', 'help',
    'in', 'into', 'is', 'it', 'its', 'later', 'manage', 'need', 'of', 'on', 'or', 'project',
    'setup', 'system', 'systems', 'the', 'this', 'to', 'use', 'using', 'with', 'work', 'workspace'
  ]);

  const state = {
    reviewedFingerprint: '',
    reviewedInput: null,
    plan: null,
    planning: false,
    applying: false,
    initialized: false
  };

  function escapeHtml(value) {
    if (typeof window.escapeHtml === 'function') {
      return window.escapeHtml(value);
    }
    return String(value == null ? '' : value)
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;')
      .replace(/'/g, '&#39;');
  }

  function showToast(message, level = 'info') {
    if (!message) return;
    if (window.Toast && typeof window.Toast[level] === 'function') {
      window.Toast[level](message);
      return;
    }
    if (typeof window.showToast === 'function') {
      window.showToast(message, level === 'warning' ? 'warning' : (level === 'error' ? 'error' : 'success'));
    }
  }

  function normalizeText(value) {
    return String(value || '').trim().toLowerCase();
  }

  function slugifyWords(value) {
    return String(value || '')
      .trim()
      .replace(/\s+/g, ' ')
      .replace(/[^a-zA-Z0-9 _-]/g, '')
      .replace(/\s+/g, ' ')
      .trim();
  }

  function tokenize(value) {
    return normalizeText(value)
      .split(/[^a-z0-9]+/)
      .map((token) => token.trim())
      .filter((token) => token && token.length > 2 && !STOPWORDS.has(token));
  }

  function uniqueList(values) {
    const seen = new Set();
    const result = [];
    (Array.isArray(values) ? values : []).forEach((value) => {
      const trimmed = String(value || '').trim();
      if (!trimmed) return;
      const key = trimmed.toLowerCase();
      if (seen.has(key)) return;
      seen.add(key);
      result.push(trimmed);
    });
    return result;
  }

  function getElement(id) {
    return document.getElementById(id);
  }

  function getCreateButton() {
    return getElement('createFolderBtn');
  }

  function getBackButton() {
    return getElement('folderReviewBackBtn');
  }

  function getReviewCard() {
    return getElement('folderBootstrapReviewCard');
  }

  function getReviewSummary() {
    return getElement('folderBootstrapReviewSummary');
  }

  function getReviewMeta() {
    return getElement('folderBootstrapReviewMeta');
  }

  function getReviewLoading() {
    return getElement('folderBootstrapReviewLoading');
  }

  function getReviewContent() {
    return getElement('folderBootstrapReviewContent');
  }

  function getModalElement() {
    return getElement('addFolderModal');
  }

  function getModalInput() {
    const workspaceName = String(getElement('folderNameInput')?.value || '').trim();
    const description = String(getElement('folderDescriptionInput')?.value || '').trim();
    const goal = String(getElement('folderPrimaryGoalInput')?.value || '').trim();
    const systems = String(getElement('folderSystemsInput')?.value || '').trim();
    const context = String(getElement('folderContextInput')?.value || '').trim();
    const importEnabled = Boolean(getElement('folderImportToggle')?.checked);
    const importPath = String(getElement('folderImportPathInput')?.value || '').trim();
    const systemsList = uniqueList(
      systems
        ? systems.split(/[\n,;]+/).map((value) => value.trim()).filter(Boolean)
        : []
    );

    return {
      workspaceName,
      description,
      goal,
      systems,
      systemsList,
      context,
      importEnabled,
      importPath
    };
  }

  function computeFingerprint(input) {
    const payload = {
      workspaceName: input.workspaceName || '',
      description: input.description || '',
      goal: input.goal || '',
      systems: input.systems || '',
      context: input.context || '',
      importEnabled: Boolean(input.importEnabled),
      importPath: input.importPath || ''
    };
    return JSON.stringify(payload);
  }

  function getBaseActionLabel() {
    const importEnabled = Boolean(getElement('folderImportToggle')?.checked);
    return importEnabled ? 'Review Import Setup' : 'Review Setup';
  }

  function refreshPrimaryActionLabel() {
    const createBtn = getCreateButton();
    if (!createBtn || state.applying) return;

    createBtn.textContent = state.reviewedFingerprint ? 'Create Workspace' : getBaseActionLabel();
  }

  function setReviewVisibility(visible) {
    const card = getReviewCard();
    const backBtn = getBackButton();
    const modal = getModalElement();
    if (card) card.hidden = !visible;
    if (backBtn) backBtn.hidden = !visible;
    if (modal) {
      modal.classList.toggle('workspace-bootstrap-review-active', Boolean(visible));
    }
  }

  function setReviewLoading(isLoading) {
    const loading = getReviewLoading();
    const content = getReviewContent();
    const createBtn = getCreateButton();
    const backBtn = getBackButton();

    if (loading) loading.hidden = !isLoading;
    if (content) content.hidden = isLoading;
    if (createBtn && !state.applying) {
      createBtn.disabled = isLoading;
      createBtn.textContent = isLoading ? 'Reviewing...' : (state.reviewedFingerprint ? 'Create Workspace' : getBaseActionLabel());
    }
    if (backBtn) backBtn.disabled = isLoading;
  }

  function resetReviewState(options = {}) {
    const { preserveVisibility = false } = options;
    state.reviewedFingerprint = '';
    state.reviewedInput = null;
    state.plan = null;
    if (!preserveVisibility) {
      setReviewVisibility(false);
    }
    const summary = getReviewSummary();
    const meta = getReviewMeta();
    const content = getReviewContent();
    if (summary) {
      summary.textContent = 'Ori can review this brief, recommend agents, and propose the MCPs and skills the workspace should start with.';
    }
    if (meta) {
      meta.textContent = '';
      meta.hidden = true;
    }
    if (content) {
      content.innerHTML = '';
      content.hidden = true;
    }
    refreshPrimaryActionLabel();
  }

  function markDirty() {
    if (!state.reviewedFingerprint && !state.plan) {
      refreshPrimaryActionLabel();
      return;
    }
    resetReviewState();
  }

  async function apiRequest(path, options = {}) {
    if (window.API) {
      if ((!options.method || options.method === 'GET') && typeof window.API.get === 'function') {
        return window.API.get(path, options);
      }
      if (options.method === 'POST' && typeof window.API.post === 'function') {
        return window.API.post(path, options.body, options);
      }
      if (options.method === 'PUT' && typeof window.API.put === 'function') {
        return window.API.put(path, options.body, options);
      }
      if (options.method === 'DELETE' && typeof window.API.delete === 'function') {
        return window.API.delete(path, options);
      }
    }

    const response = await fetch(path, {
      method: options.method || 'GET',
      headers: options.body ? { 'Content-Type': 'application/json' } : undefined,
      body: options.body ? JSON.stringify(options.body) : undefined
    });
    const isJSON = String(response.headers.get('content-type') || '').includes('application/json');
    const payload = isJSON ? await response.json().catch(() => ({})) : await response.text().catch(() => '');
    if (!response.ok) {
      const message = payload?.error || payload?.message || payload || `Request failed (${response.status})`;
      throw new Error(String(message));
    }
    return payload;
  }

  async function fetchAgents() {
    try {
      const data = await apiRequest('/api/agents');
      return Array.isArray(data?.agents) ? data.agents : [];
    } catch (_error) {
      try {
        const fallback = await apiRequest('/api/agents/dashboard/list');
        return Array.isArray(fallback?.agents) ? fallback.agents : [];
      } catch (_fallbackError) {
        return [];
      }
    }
  }

  async function fetchInstalledSkills() {
    try {
      const data = await apiRequest(`/api/skills?agent=${encodeURIComponent(CATALOG_AGENT)}`);
      return Array.isArray(data?.skills) ? data.skills : [];
    } catch (_error) {
      return [];
    }
  }

  async function fetchConfiguredMCPServers() {
    try {
      const data = await apiRequest('/api/mcp/servers');
      return Array.isArray(data?.servers) ? data.servers : [];
    } catch (_error) {
      return [];
    }
  }

  async function fetchMarketplaceInstalledSkills() {
    try {
      const data = await apiRequest('/api/skills/marketplace/installed');
      return Array.isArray(data?.skills) ? data.skills : [];
    } catch (_error) {
      return [];
    }
  }

  async function searchMarketplaceSkills(query) {
    try {
      const data = await apiRequest('/api/skills/marketplace/search', {
        method: 'POST',
        body: { query, limit: 6 }
      });
      return Array.isArray(data?.results) ? data.results : [];
    } catch (_error) {
      return [];
    }
  }

  async function searchMCPRegistry(query) {
    try {
      const data = await apiRequest(`/api/mcp/search?q=${encodeURIComponent(query)}`);
      return Array.isArray(data) ? data : (Array.isArray(data?.servers) ? data.servers : []);
    } catch (_error) {
      return [];
    }
  }

  async function checkAutoConfigAvailability() {
    try {
      const data = await apiRequest('/api/agents/auto-config/availability');
      return Boolean(data?.available);
    } catch (_error) {
      return false;
    }
  }

  function deriveSearchQueries(input) {
    const queries = [];
    input.systemsList.forEach((system) => {
      if (queries.length < 3) queries.push(system);
    });

    const fallbackChunks = [
      input.goal
    ]
      .map((value) => String(value || '').trim())
      .filter(Boolean)
      .flatMap((value) => value.split(/[.;,\n]+/))
      .map((value) => value.trim())
      .filter((value) => value.length >= 4);

    fallbackChunks.forEach((chunk) => {
      if (queries.length >= 3) return;
      if (!queries.some((existing) => normalizeText(existing) === normalizeText(chunk))) {
        queries.push(chunk);
      }
    });

    if (queries.length === 0 && input.workspaceName) {
      queries.push(input.workspaceName);
    }

    return queries.slice(0, 3);
  }

  function buildSearchCorpus(input) {
    return [
      input.workspaceName,
      input.description,
      input.goal,
      input.systems,
      input.context
    ].join(' ');
  }

  function scoreTextAgainstQueries(text, queries) {
    const haystack = normalizeText(text);
    if (!haystack) return 0;

    let score = 0;
    queries.forEach((query) => {
      const normalizedQuery = normalizeText(query);
      if (!normalizedQuery) return;
      if (haystack.includes(normalizedQuery)) {
        score += 14;
      }
      tokenize(query).forEach((token) => {
        if (haystack.includes(token)) {
          score += 4;
        }
      });
    });

    return score;
  }

  function scoreSystemMatch(text, system) {
    const haystack = normalizeText(text);
    const normalizedSystem = normalizeText(system);
    if (!haystack || !normalizedSystem) return 0;
    let score = 0;
    if (haystack.includes(normalizedSystem)) {
      score += 18;
    }
    tokenize(system).forEach((token) => {
      if (haystack.includes(token)) {
        score += 5;
      }
    });
    return score;
  }

  function shouldCreateSpecialist(system) {
    const normalized = normalizeText(system);
    return Boolean(normalized) && !GENERIC_SYSTEM_TERMS.has(normalized);
  }

  function buildPrimaryAgentName(input) {
    const base = slugifyWords(input.workspaceName || input.importPath.split(/[\\/]/).filter(Boolean).pop() || 'Workspace');
    return `${base} Manager`.trim();
  }

  function buildSpecialistAgentName(system) {
    return `${slugifyWords(system)} Specialist`.trim();
  }

  function buildPrimaryAgentDescription(input) {
    const systems = input.systemsList.length > 0 ? `Primary systems: ${input.systemsList.join(', ')}.` : '';
    return uniqueList([
      `Lead the "${input.workspaceName || 'workspace'}" workspace and coordinate the work required to achieve: ${input.goal}.`,
      systems
    ]).join(' ');
  }

  function buildSpecialistDescription(input, system) {
    return uniqueList([
      `Handle ${system} work inside the "${input.workspaceName || 'workspace'}" workspace.`,
      `Primary goal: ${input.goal}.`
    ]).join(' ');
  }

  function normalizeAgentEntries(rawAgents) {
    const source = Array.isArray(rawAgents) ? rawAgents : [];
    const result = [];
    const seen = new Set();

    source.forEach((agent) => {
      const name = String(agent?.name || agent || '').trim();
      if (!name) return;
      const key = name.toLowerCase();
      if (seen.has(key)) return;
      seen.add(key);
      result.push({
        name,
        description: String(agent?.description || agent?.metadata?.description || '').trim(),
        type: String(agent?.type || '').trim()
      });
    });

    return result;
  }

  function buildAgentPlan(input, agents) {
    const searchCorpus = buildSearchCorpus(input);
    const normalizedAgents = normalizeAgentEntries(agents);
    const ranked = normalizedAgents
      .map((agent) => ({
        ...agent,
        score: scoreTextAgainstQueries(`${agent.name} ${agent.description}`, [searchCorpus, ...input.systemsList])
      }))
      .filter((agent) => agent.score > 0)
      .sort((left, right) => right.score - left.score);

    const selectedAgentKeys = new Set();
    const planAgents = [];

    const primaryExisting = ranked[0] && ranked[0].score >= 16 ? ranked[0] : null;
    if (primaryExisting) {
      selectedAgentKeys.add(primaryExisting.name.toLowerCase());
      planAgents.push({
        id: `agent-primary-${primaryExisting.name.toLowerCase().replace(/[^a-z0-9]+/g, '-')}`,
        name: primaryExisting.name,
        summary: primaryExisting.description || 'Existing agent matched from the workspace brief.',
        action: 'invite',
        role: 'lead',
        selected: true,
        locked: true,
        type: primaryExisting.type || 'general',
        autoDescription: buildPrimaryAgentDescription(input)
      });
    } else {
      planAgents.push({
        id: 'agent-primary-new',
        name: buildPrimaryAgentName(input),
        summary: buildPrimaryAgentDescription(input),
        action: 'create',
        role: 'lead',
        selected: true,
        locked: true,
        type: input.systemsList.length > 0 ? 'tool-calling' : 'general',
        autoDescription: buildPrimaryAgentDescription(input)
      });
    }

    input.systemsList.slice(0, 2).forEach((system) => {
      let specialist = null;
      ranked.forEach((agent) => {
        if (specialist || selectedAgentKeys.has(agent.name.toLowerCase())) return;
        const score = scoreSystemMatch(`${agent.name} ${agent.description}`, system);
        if (score >= 18) {
          specialist = {
            id: `agent-existing-${agent.name.toLowerCase().replace(/[^a-z0-9]+/g, '-')}`,
            name: agent.name,
            summary: agent.description || `Existing agent aligned with ${system}.`,
            action: 'invite',
            role: 'specialist',
            selected: true,
            locked: false,
            type: agent.type || 'tool-calling',
            focusSystem: system,
            autoDescription: buildSpecialistDescription(input, system)
          };
        }
      });

      if (specialist) {
        selectedAgentKeys.add(specialist.name.toLowerCase());
        planAgents.push(specialist);
        return;
      }

      if (!shouldCreateSpecialist(system)) {
        return;
      }

      const specialistName = buildSpecialistAgentName(system);
      if (selectedAgentKeys.has(specialistName.toLowerCase())) {
        return;
      }
      selectedAgentKeys.add(specialistName.toLowerCase());
      planAgents.push({
        id: `agent-new-${specialistName.toLowerCase().replace(/[^a-z0-9]+/g, '-')}`,
        name: specialistName,
        summary: buildSpecialistDescription(input, system),
        action: 'create',
        role: 'specialist',
        selected: true,
        locked: false,
        type: 'tool-calling',
        focusSystem: system,
        autoDescription: buildSpecialistDescription(input, system)
      });
    });

    return planAgents;
  }

  function normalizeSkillCandidates(installedSkills, marketplaceResults, queries) {
    const candidates = [];
    const seen = new Set();

    (Array.isArray(installedSkills) ? installedSkills : []).forEach((skill) => {
      const name = String(skill?.name || '').trim();
      if (!name) return;
      const key = name.toLowerCase();
      if (seen.has(key)) return;
      const description = String(skill?.description || '').trim();
      const score = scoreTextAgainstQueries(
        `${name} ${description} ${Array.isArray(skill?.required_mcp_servers) ? skill.required_mcp_servers.join(' ') : ''}`,
        queries
      );
      if (score <= 0) return;
      seen.add(key);
      candidates.push({
        id: `skill-${key.replace(/[^a-z0-9]+/g, '-')}`,
        name,
        description,
        source: String(skill?.source || 'installed').trim() || 'installed',
        action: 'attach',
        selected: true,
        trusted: true,
        score
      });
    });

    (Array.isArray(marketplaceResults) ? marketplaceResults : []).forEach((skill) => {
      const skillName = String(skill?.skill || skill?.name || '').trim();
      const packageName = String(skill?.package || '').trim();
      const url = String(skill?.url || '').trim();
      const label = skillName || packageName;
      if (!label) return;
      const key = label.toLowerCase();
      if (seen.has(key)) return;
      const description = [skill?.repository, skill?.installs].filter(Boolean).join(' · ');
      const score = scoreTextAgainstQueries(
        `${label} ${packageName} ${skill?.repository || ''}`,
        queries
      );
      if (score <= 0) return;
      seen.add(key);
      candidates.push({
        id: `skill-market-${key.replace(/[^a-z0-9]+/g, '-')}`,
        name: skillName || label,
        description: description || 'Suggested from the skills marketplace.',
        source: 'marketplace',
        action: 'install_attach',
        selected: candidates.length === 0,
        trusted: true,
        packageName,
        url,
        score
      });
    });

    return candidates
      .sort((left, right) => right.score - left.score)
      .slice(0, 4);
  }

  function normalizeMCPCandidates(configuredServers, registryResults, queries) {
    const candidates = [];
    const seen = new Set();

    (Array.isArray(configuredServers) ? configuredServers : []).forEach((server) => {
      const name = String(server?.name || '').trim();
      if (!name || /^ws:/i.test(name) || normalizeText(name) === 'filesystem') return;
      const key = name.toLowerCase();
      if (seen.has(key)) return;
      const description = String(server?.description || '').trim();
      const score = scoreTextAgainstQueries(`${name} ${description}`, queries);
      if (score <= 0) return;
      seen.add(key);
      candidates.push({
        id: `mcp-${key.replace(/[^a-z0-9]+/g, '-')}`,
        name,
        description,
        source: 'configured',
        action: 'bind',
        selected: true,
        score
      });
    });

    (Array.isArray(registryResults) ? registryResults : []).forEach((server) => {
      const name = String(server?.name || '').trim();
      if (!name || normalizeText(name) === 'filesystem') return;
      const key = name.toLowerCase();
      if (seen.has(key)) return;
      const description = String(server?.description || '').trim();
      const score = scoreTextAgainstQueries(`${name} ${description} ${(server?.tags || []).join(' ')}`, queries);
      if (score <= 0) return;
      seen.add(key);
      candidates.push({
        id: `mcp-market-${key.replace(/[^a-z0-9]+/g, '-')}`,
        name,
        description,
        source: 'registry',
        action: 'install_bind',
        selected: candidates.length === 0,
        command: String(server?.command || '').trim(),
        args: Array.isArray(server?.args) ? server.args : [],
        env: server?.env && typeof server.env === 'object' ? server.env : {},
        transport: String(server?.transport || 'stdio').trim() || 'stdio',
        score
      });
    });

    return candidates
      .sort((left, right) => right.score - left.score)
      .slice(0, 4);
  }

  async function buildPlan(input) {
    const queries = deriveSearchQueries(input);
    const [agents, installedSkills, configuredMCPs] = await Promise.all([
      fetchAgents(),
      fetchInstalledSkills(),
      fetchConfiguredMCPServers()
    ]);

    const marketplaceSkillResults = [];
    const registryMCPResults = [];
    await Promise.all(queries.map(async (query) => {
      const [skillResults, mcpResults] = await Promise.all([
        searchMarketplaceSkills(query),
        searchMCPRegistry(query)
      ]);
      marketplaceSkillResults.push(...(Array.isArray(skillResults) ? skillResults : []));
      registryMCPResults.push(...(Array.isArray(mcpResults) ? mcpResults : []));
    }));

    const planAgents = buildAgentPlan(input, agents);
    const goalQueries = uniqueList([
      ...queries,
      input.goal
    ]).slice(0, 5);
    const planSkills = normalizeSkillCandidates(installedSkills, marketplaceSkillResults, goalQueries);
    const planMCPs = normalizeMCPCandidates(configuredMCPs, registryMCPResults, goalQueries);

    return {
      summary: buildPlanSummary(planAgents, planMCPs, planSkills),
      agents: planAgents,
      mcps: planMCPs,
      skills: planSkills,
      notes: buildPlanNotes(planAgents, planMCPs, planSkills),
      queries
    };
  }

  function buildPlanSummary(agents, mcps, skills) {
    const selectedAgents = agents.filter((agent) => agent.selected);
    const selectedMCPs = mcps.filter((item) => item.selected);
    const selectedSkills = skills.filter((item) => item.selected);
    const parts = [];
    parts.push(`${selectedAgents.length} agent${selectedAgents.length === 1 ? '' : 's'}`);
    if (selectedMCPs.length > 0) {
      parts.push(`${selectedMCPs.length} MCP${selectedMCPs.length === 1 ? '' : 's'}`);
    }
    if (selectedSkills.length > 0) {
      parts.push(`${selectedSkills.length} skill${selectedSkills.length === 1 ? '' : 's'}`);
    }
    return `Ori reviewed the brief and prepared a starter setup with ${parts.join(', ')}.`;
  }

  function buildPlanNotes(agents, mcps, skills) {
    const notes = [];
    if (agents.some((agent) => agent.action === 'create')) {
      notes.push('New agents will be auto-configured from this workspace brief after the workspace is created.');
    }
    if (mcps.some((item) => item.action === 'install_bind')) {
      notes.push('Registry MCP suggestions will be installed globally before they are bound to the workspace.');
    }
    if (skills.some((item) => item.action === 'install_attach')) {
      notes.push('Marketplace skills will be installed globally before they are attached to the workspace.');
    }
    notes.push('Selected MCPs and skills will be shared with every agent Ori adds through this setup.');
    return notes;
  }

  function countSelections(selector) {
    return Array.from(document.querySelectorAll(selector))
      .filter((input) => input.checked)
      .length;
  }

  function updateRenderedSummary() {
    const summary = getReviewSummary();
    const meta = getReviewMeta();
    if (!state.plan || !summary || !meta) return;

    const leadCount = 1;
    const extraAgentCount = countSelections('input[data-workspace-bootstrap-agent]');
    const mcpCount = countSelections('input[data-workspace-bootstrap-mcp]');
    const skillCount = countSelections('input[data-workspace-bootstrap-skill]');
    const agentTotal = leadCount + extraAgentCount;

    const parts = [`${agentTotal} agent${agentTotal === 1 ? '' : 's'}`];
    if (mcpCount > 0) parts.push(`${mcpCount} MCP${mcpCount === 1 ? '' : 's'}`);
    if (skillCount > 0) parts.push(`${skillCount} skill${skillCount === 1 ? '' : 's'}`);
    summary.textContent = `Ori will create this workspace with ${parts.join(', ')}.`;
    meta.textContent = 'You can adjust the optional invites and capability suggestions below before creating the workspace.';
    meta.hidden = false;
  }

  function renderAgentCards(agents) {
    const lead = agents.find((agent) => agent.role === 'lead');
    const optionalAgents = agents.filter((agent) => agent.role !== 'lead');

    const leadHtml = lead ? `
      <div class="modern-card p-3 mb-2" style="border: 1px solid var(--border-color); background: color-mix(in srgb, var(--bg-secondary) 88%, transparent);">
        <div class="d-flex justify-content-between align-items-start gap-2">
          <div>
            <div style="font-size: 12px; color: var(--text-secondary); text-transform: uppercase; letter-spacing: 0.06em;">Primary Agent</div>
            <div style="font-weight: 600; color: var(--text-primary);">${escapeHtml(lead.name)}</div>
            <div style="font-size: 12px; color: var(--text-secondary); margin-top: 4px;">${escapeHtml(lead.summary)}</div>
          </div>
          <span class="workspace-detail-mcp-chip status">${lead.action === 'create' ? 'Create' : 'Invite'}</span>
        </div>
      </div>
    ` : '';

    const optionalHtml = optionalAgents.length > 0 ? optionalAgents.map((agent) => `
      <label class="workspace-setup-option modern-card p-3 d-flex align-items-start gap-2 mb-2" style="border: 1px solid var(--border-color);">
        <input type="checkbox"
               data-workspace-bootstrap-agent="true"
               value="${escapeHtml(agent.id)}"
               ${agent.selected ? 'checked' : ''}>
        <span style="display: block;">
          <span style="display: flex; align-items: center; gap: 8px; flex-wrap: wrap;">
            <span style="font-weight: 600; color: var(--text-primary);">${escapeHtml(agent.name)}</span>
            <span class="workspace-detail-mcp-chip ${agent.action === 'create' ? 'source' : 'access'}">${agent.action === 'create' ? 'Create' : 'Invite'}</span>
          </span>
          <span style="display: block; margin-top: 4px; font-size: 12px; color: var(--text-secondary);">${escapeHtml(agent.summary)}</span>
        </span>
      </label>
    `).join('') : `
      <div class="workspace-detail-empty">
        Ori did not find additional agents worth inviting yet.
      </div>
    `;

    return `
      <div class="workspace-setup-section">
        <div class="workspace-setup-label">Agents</div>
        ${leadHtml}
        ${optionalHtml}
      </div>
    `;
  }

  function renderCapabilityCards(items, kind) {
    if (!Array.isArray(items) || items.length === 0) {
      return `
        <div class="workspace-detail-empty">
          No ${kind === 'mcp' ? 'MCP' : 'skill'} suggestions yet.
        </div>
      `;
    }

    const attribute = kind === 'mcp' ? 'data-workspace-bootstrap-mcp' : 'data-workspace-bootstrap-skill';
    return items.map((item) => `
      <div class="modern-card p-3 d-flex align-items-start gap-2 mb-2" style="border: 1px solid var(--border-color);">
        <input type="checkbox"
               id="${escapeHtml(`workspace-bootstrap-${kind}-${item.id}`)}"
               ${attribute}="true"
               value="${escapeHtml(item.id)}"
               ${item.selected ? 'checked' : ''}>
        <div style="display: block; flex: 1; min-width: 0;">
          <label for="${escapeHtml(`workspace-bootstrap-${kind}-${item.id}`)}" style="display: block; cursor: pointer;">
            <span style="display: flex; align-items: center; gap: 8px; flex-wrap: wrap;">
              <span style="font-weight: 600; color: var(--text-primary);">${escapeHtml(item.name)}</span>
              <span class="workspace-detail-mcp-chip ${item.action.indexOf('install') >= 0 ? 'source' : 'status'}">
                ${item.action.indexOf('install') >= 0 ? (kind === 'mcp' ? 'Install Globally + Bind' : 'Install Globally + Attach') : (kind === 'mcp' ? 'Bind' : 'Attach')}
              </span>
              ${(
                (kind === 'mcp' && item.source === 'configured') ||
                (kind === 'skill' && item.source !== 'marketplace')
              ) ? '<span class="workspace-detail-mcp-chip local">Local</span>' : ''}
            </span>
            <span style="display: block; margin-top: 4px; font-size: 12px; color: var(--text-secondary);">${escapeHtml(item.description || (kind === 'mcp' ? 'Suggested MCP connector.' : 'Suggested workspace skill.'))}</span>
          </label>
          ${kind === 'skill' && item.url ? `
            <a href="${escapeHtml(item.url)}"
               target="_blank"
               rel="noopener noreferrer"
               onclick="event.stopPropagation()"
               style="display: inline-block; margin-top: 8px; font-size: 12px; color: var(--text-secondary); text-decoration: underline; word-break: break-all;">
              ${escapeHtml(item.url)}
            </a>
          ` : ''}
        </div>
      </div>
    `).join('');
  }

  function bindRenderedSelectionListeners() {
    document.querySelectorAll(
      'input[data-workspace-bootstrap-agent], input[data-workspace-bootstrap-mcp], input[data-workspace-bootstrap-skill]'
    ).forEach((input) => {
      input.addEventListener('change', updateRenderedSummary);
    });
  }

  function renderPlan(plan) {
    const content = getReviewContent();
    if (!content) return;

    content.innerHTML = `
      <div class="workspace-bootstrap-review-layout">
        <div class="workspace-bootstrap-review-column">
          ${renderAgentCards(plan.agents)}
          <div class="workspace-setup-section">
            <div class="workspace-setup-label">Workspace MCPs</div>
            ${renderCapabilityCards(plan.mcps, 'mcp')}
          </div>
        </div>
        <div class="workspace-bootstrap-review-column">
          <div class="workspace-setup-section">
            <div class="workspace-setup-label">Workspace Skills</div>
            ${renderCapabilityCards(plan.skills, 'skill')}
          </div>
        </div>
        <div class="workspace-setup-preview">
          <div class="workspace-setup-label">How Ori Will Apply This</div>
          <ul class="workspace-setup-preview-list">
            ${plan.notes.map((note) => `<li>${escapeHtml(note)}</li>`).join('')}
          </ul>
        </div>
      </div>
    `;
    content.hidden = false;
    bindRenderedSelectionListeners();
    updateRenderedSummary();
  }

  function getSelectedPlan() {
    if (!state.plan) return null;

    const lead = state.plan.agents.find((agent) => agent.role === 'lead');
    const selectedAgents = [];
    if (lead) {
      selectedAgents.push(lead);
    }

    document.querySelectorAll('input[data-workspace-bootstrap-agent]').forEach((input) => {
      if (!input.checked) return;
      const match = state.plan.agents.find((agent) => agent.id === input.value);
      if (match) {
        selectedAgents.push(match);
      }
    });

    const selectedMCPs = [];
    document.querySelectorAll('input[data-workspace-bootstrap-mcp]').forEach((input) => {
      if (!input.checked) return;
      const match = state.plan.mcps.find((item) => item.id === input.value);
      if (match) selectedMCPs.push(match);
    });

    const selectedSkills = [];
    document.querySelectorAll('input[data-workspace-bootstrap-skill]').forEach((input) => {
      if (!input.checked) return;
      const match = state.plan.skills.find((item) => item.id === input.value);
      if (match) selectedSkills.push(match);
    });

    return {
      agents: selectedAgents,
      mcps: selectedMCPs,
      skills: selectedSkills,
      queries: state.plan.queries || []
    };
  }

  async function ensureReviewed() {
    if (state.planning) {
      return { ready: false };
    }

    const input = getModalInput();
    const fingerprint = computeFingerprint(input);
    if (state.reviewedFingerprint && state.reviewedFingerprint === fingerprint && state.plan) {
      return { ready: true, plan: getSelectedPlan() };
    }

    state.planning = true;
    setReviewVisibility(true);
    setReviewLoading(true);

    try {
      const plan = await buildPlan(input);
      state.plan = plan;
      state.reviewedFingerprint = fingerprint;
      state.reviewedInput = input;
      renderPlan(plan);
      return { ready: false, plan };
    } catch (error) {
      console.error('Failed to build workspace bootstrap review:', error);
      showToast(error.message || 'Failed to review workspace setup', 'error');
      resetReviewState({ preserveVisibility: true });
      return { ready: false, error };
    } finally {
      state.planning = false;
      setReviewLoading(false);
    }
  }

  async function maybeAutoConfigureAgent(description, fallbackType) {
    const available = await checkAutoConfigAvailability();
    if (!available) {
      return {
        agent_type: fallbackType || 'general',
        provider: '',
        model: '',
        temperature: 0.4,
        system_prompt: ''
      };
    }

    try {
      return await apiRequest('/api/agents/auto-config', {
        method: 'POST',
        body: { description }
      });
    } catch (_error) {
      return {
        agent_type: fallbackType || 'general',
        provider: '',
        model: '',
        temperature: 0.4,
        system_prompt: ''
      };
    }
  }

  async function ensureAgentExists(agentPlan) {
    if (!agentPlan || agentPlan.action !== 'create') {
      return agentPlan?.name || '';
    }

    const requestConfig = await maybeAutoConfigureAgent(agentPlan.autoDescription || agentPlan.summary || '', agentPlan.type);
    const payload = {
      name: agentPlan.name,
      type: requestConfig?.agent_type || agentPlan.type || 'general',
      description: agentPlan.summary || agentPlan.autoDescription || '',
      allow_web_search: true
    };

    if (requestConfig?.model) {
      payload.model = requestConfig.model;
    }
    if (requestConfig?.provider) {
      payload.llm_provider = requestConfig.provider;
    }
    if (typeof requestConfig?.temperature === 'number') {
      payload.temperature = requestConfig.temperature;
    }
    if (requestConfig?.system_prompt) {
      payload.system_prompt = requestConfig.system_prompt;
    }

    try {
      await apiRequest('/api/agents', { method: 'POST', body: payload });
      return agentPlan.name;
    } catch (error) {
      const message = String(error?.message || '');
      if (message.toLowerCase().includes('already exists')) {
        return agentPlan.name;
      }
      throw error;
    }
  }

  async function addAgentToWorkspace(workspaceId, agentName) {
    const result = await apiRequest(`/api/workspaces/${encodeURIComponent(workspaceId)}/agents`, {
      method: 'POST',
      body: { agent_name: agentName }
    });
    return {
      instanceId: String(result?.agent_instance?.id || '').trim(),
      workspace: result?.workspace || null
    };
  }

  async function loadWorkspaceState(workspaceId) {
    return apiRequest(`/api/orchestration/workspace?id=${encodeURIComponent(workspaceId)}`);
  }

  function buildMCPInstallPayload(candidate) {
    return {
      name: String(candidate?.name || '').trim(),
      command: String(candidate?.command || '').trim(),
      args: Array.isArray(candidate?.args) ? candidate.args : [],
      env: candidate?.env && typeof candidate.env === 'object' ? candidate.env : {},
      transport: String(candidate?.transport || 'stdio').trim() || 'stdio',
      enabled: true
    };
  }

  function findConfiguredMCPServer(servers, candidate) {
    const candidateName = normalizeText(candidate?.name);
    return (Array.isArray(servers) ? servers : []).find((server) => {
      const serverName = normalizeText(server?.name);
      return serverName && serverName === candidateName;
    }) || null;
  }

  async function ensureGlobalMCPReady(candidate) {
    if (!candidate) {
      return '';
    }

    let configured = findConfiguredMCPServer(await fetchConfiguredMCPServers(), candidate);
    if (configured) {
      if (configured.enabled === false) {
        await apiRequest(`/api/mcp/servers/${encodeURIComponent(configured.name)}/enable`, { method: 'POST' });
      }
      return String(configured.name || candidate.name || '').trim();
    }

    if (candidate.action !== 'install_bind') {
      throw new Error(`MCP ${candidate.name} is not installed globally`);
    }

    const payload = buildMCPInstallPayload(candidate);
    if (!payload.name || !payload.command) {
      throw new Error(`MCP ${candidate.name} is missing install details`);
    }

    try {
      await apiRequest('/api/mcp/servers', { method: 'POST', body: payload });
      return payload.name;
    } catch (error) {
      const message = String(error?.message || '');
      if (!message.toLowerCase().includes('already exists')) {
        throw error;
      }
    }

    configured = findConfiguredMCPServer(await fetchConfiguredMCPServers(), candidate);
    if (configured) {
      if (configured.enabled === false) {
        await apiRequest(`/api/mcp/servers/${encodeURIComponent(configured.name)}/enable`, { method: 'POST' });
      }
      return String(configured.name || payload.name).trim();
    }

    throw new Error(`MCP ${candidate.name} was not installed globally`);
  }

  async function ensureWorkspaceMCPBinding(workspaceId, workspaceState, candidate) {
    const serverName = await ensureGlobalMCPReady(candidate);
    const bindings = Array.isArray(workspaceState?.mcp_bindings) ? workspaceState.mcp_bindings : [];
    const existing = bindings.find((binding) => normalizeText(binding?.server_name) === normalizeText(serverName));

    if (existing && existing.enabled !== false) {
      return String(existing.id || '').trim();
    }

    if (existing && existing.enabled === false) {
      const updated = await apiRequest(
        `/api/workspaces/${encodeURIComponent(workspaceId)}/mcp-bindings/${encodeURIComponent(existing.id)}`,
        {
          method: 'PUT',
          body: { enabled: true }
        }
      );
      const binding = updated?.binding || { ...existing, enabled: true };
      workspaceState.mcp_bindings = bindings.map((item) => (item?.id === binding.id ? binding : item));
      return String(binding.id || '').trim();
    }

    const created = await apiRequest(`/api/workspaces/${encodeURIComponent(workspaceId)}/mcp-bindings`, {
      method: 'POST',
      body: {
        server_name: serverName,
        enabled: true
      }
    });
    const binding = created?.binding || null;
    if (binding) {
      workspaceState.mcp_bindings = [...bindings, binding];
      return String(binding.id || '').trim();
    }
    throw new Error(`Failed to bind MCP ${serverName}`);
  }

  function getMCPAccessEntry(workspaceState, agentInstanceId) {
    const entries = Array.isArray(workspaceState?.agent_mcp_access) ? workspaceState.agent_mcp_access : [];
    return entries.find((entry) => String(entry?.agent_instance_id || '').trim() === agentInstanceId) || null;
  }

  function getSkillAccessEntry(workspaceState, agentInstanceId) {
    const entries = Array.isArray(workspaceState?.agent_skill_access) ? workspaceState.agent_skill_access : [];
    return entries.find((entry) => String(entry?.agent_instance_id || '').trim() === agentInstanceId) || null;
  }

  function mergeBindingIDs(values, nextValue) {
    return uniqueList([...(Array.isArray(values) ? values : []), nextValue]);
  }

  async function grantMCPAccess(workspaceId, workspaceState, bindingId, agentInstanceIds) {
    for (const agentInstanceId of agentInstanceIds) {
      if (!agentInstanceId) continue;
      const current = getMCPAccessEntry(workspaceState, agentInstanceId);
      const enabledBindingIds = mergeBindingIDs(current?.enabled_binding_ids || [], bindingId);
      await apiRequest(
        `/api/workspaces/${encodeURIComponent(workspaceId)}/agent-mcp-access/${encodeURIComponent(agentInstanceId)}`,
        {
          method: 'PUT',
          body: { enabled_binding_ids: enabledBindingIds }
        }
      );
      const entries = Array.isArray(workspaceState.agent_mcp_access) ? workspaceState.agent_mcp_access.slice() : [];
      const nextEntry = { agent_instance_id: agentInstanceId, enabled_binding_ids: enabledBindingIds };
      const index = entries.findIndex((entry) => String(entry?.agent_instance_id || '').trim() === agentInstanceId);
      if (index >= 0) {
        entries[index] = nextEntry;
      } else {
        entries.push(nextEntry);
      }
      workspaceState.agent_mcp_access = entries;
    }
  }

  function parseMarketplaceSkillPackage(packageName) {
    const trimmed = String(packageName || '').trim();
    if (!trimmed) {
      return { skillName: '' };
    }
    const atIndex = trimmed.lastIndexOf('@');
    if (atIndex <= 0 || atIndex >= trimmed.length - 1) {
      return { skillName: '' };
    }
    return {
      skillName: trimmed.slice(atIndex + 1).trim()
    };
  }

  function resolveInstalledSkillName(installedSkills, candidate) {
    const requestedName = normalizeText(candidate?.name);
    const packageSkillName = normalizeText(parseMarketplaceSkillPackage(candidate?.packageName).skillName);
    const match = (Array.isArray(installedSkills) ? installedSkills : []).find((skill) => {
      const installedName = normalizeText(skill?.name);
      if (!installedName) return false;
      return installedName === requestedName || (packageSkillName && installedName === packageSkillName);
    });
    return String(match?.name || '').trim();
  }

  async function ensureInstalledSkill(candidate) {
    if (!candidate) {
      return '';
    }

    if (candidate.action !== 'install_attach') {
      return String(candidate?.name || '').trim();
    }

    if (!candidate.packageName) {
      throw new Error(`Skill ${candidate.name} is missing install details`);
    }

    const installedBefore = await fetchMarketplaceInstalledSkills();
    const existingSkillName = resolveInstalledSkillName(installedBefore, candidate);
    if (existingSkillName) {
      return existingSkillName;
    }

    try {
      await apiRequest('/api/skills/marketplace/install', {
        method: 'POST',
        body: { package: candidate.packageName }
      });
    } catch (error) {
      const message = String(error?.message || '');
      if (!message.toLowerCase().includes('already installed')) {
        throw error;
      }
    }

    const installedAfter = await fetchMarketplaceInstalledSkills();
    const installedSkillName = resolveInstalledSkillName(installedAfter, candidate);
    if (installedSkillName) {
      return installedSkillName;
    }

    throw new Error(`Skill ${candidate.name} was not installed globally`);
  }

  async function ensureWorkspaceSkillBinding(workspaceId, workspaceState, candidate) {
    const skillName = await ensureInstalledSkill(candidate);
    const bindings = Array.isArray(workspaceState?.skill_bindings) ? workspaceState.skill_bindings : [];
    const existing = bindings.find((binding) => normalizeText(binding?.skill_name || binding?.skillName) === normalizeText(skillName));

    if (existing && existing.enabled !== false) {
      return String(existing.id || '').trim();
    }

    if (existing && existing.enabled === false) {
      const updated = await apiRequest(
        `/api/workspaces/${encodeURIComponent(workspaceId)}/skill-bindings/${encodeURIComponent(existing.id)}`,
        {
          method: 'PUT',
          body: { enabled: true, trusted: true }
        }
      );
      const binding = updated?.binding || { ...existing, enabled: true, trusted: true };
      workspaceState.skill_bindings = bindings.map((item) => (item?.id === binding.id ? binding : item));
      return String(binding.id || '').trim();
    }

    const created = await apiRequest(`/api/workspaces/${encodeURIComponent(workspaceId)}/skill-bindings`, {
      method: 'POST',
      body: {
        skill_name: skillName,
        enabled: true,
        trusted: candidate.trusted !== false,
        config: {}
      }
    });
    const binding = created?.binding || null;
    if (binding) {
      workspaceState.skill_bindings = [...bindings, binding];
      return String(binding.id || '').trim();
    }
    throw new Error(`Failed to attach skill ${skillName}`);
  }

  async function grantSkillAccess(workspaceId, workspaceState, bindingId, agentInstanceIds) {
    for (const agentInstanceId of agentInstanceIds) {
      if (!agentInstanceId) continue;
      const current = getSkillAccessEntry(workspaceState, agentInstanceId);
      const enabledBindingIds = mergeBindingIDs(current?.enabled_binding_ids || [], bindingId);
      await apiRequest(
        `/api/workspaces/${encodeURIComponent(workspaceId)}/agent-skill-access/${encodeURIComponent(agentInstanceId)}`,
        {
          method: 'PUT',
          body: { enabled_binding_ids: enabledBindingIds }
        }
      );
      const entries = Array.isArray(workspaceState.agent_skill_access) ? workspaceState.agent_skill_access.slice() : [];
      const nextEntry = { agent_instance_id: agentInstanceId, enabled_binding_ids: enabledBindingIds };
      const index = entries.findIndex((entry) => String(entry?.agent_instance_id || '').trim() === agentInstanceId);
      if (index >= 0) {
        entries[index] = nextEntry;
      } else {
        entries.push(nextEntry);
      }
      workspaceState.agent_skill_access = entries;
    }
  }

  async function applyPlan(workspaceId) {
    const selectedPlan = getSelectedPlan();
    if (!selectedPlan) {
      return {
        invitedAgents: 0,
        boundMCPs: 0,
        attachedSkills: 0,
        failures: []
      };
    }

    state.applying = true;
    const createBtn = getCreateButton();
    if (createBtn) {
      createBtn.disabled = true;
      createBtn.textContent = 'Applying Setup...';
    }
    const backBtn = getBackButton();
    if (backBtn) backBtn.disabled = true;

    const summary = {
      invitedAgents: 0,
      boundMCPs: 0,
      attachedSkills: 0,
      failures: []
    };

    try {
      const agentInstanceIds = [];

      for (const agentPlan of selectedPlan.agents) {
        try {
          const agentName = await ensureAgentExists(agentPlan);
          const added = await addAgentToWorkspace(workspaceId, agentName);
          if (added.instanceId) {
            agentInstanceIds.push(added.instanceId);
          }
          summary.invitedAgents += 1;
        } catch (error) {
          summary.failures.push(`Agent ${agentPlan.name}: ${error.message || 'failed to add to workspace'}`);
        }
      }

      let workspaceState;
      try {
        workspaceState = await loadWorkspaceState(workspaceId);
      } catch (error) {
        summary.failures.push(`Workspace load: ${error.message || 'failed to load workspace state'}`);
        return summary;
      }

      for (const mcpCandidate of selectedPlan.mcps) {
        try {
          const bindingId = await ensureWorkspaceMCPBinding(workspaceId, workspaceState, mcpCandidate);
          if (bindingId && agentInstanceIds.length > 0) {
            await grantMCPAccess(workspaceId, workspaceState, bindingId, agentInstanceIds);
          }
          summary.boundMCPs += 1;
        } catch (error) {
          summary.failures.push(`MCP ${mcpCandidate.name}: ${error.message || 'failed to bind'}`);
        }
      }

      for (const skillCandidate of selectedPlan.skills) {
        try {
          const bindingId = await ensureWorkspaceSkillBinding(workspaceId, workspaceState, skillCandidate);
          if (bindingId && agentInstanceIds.length > 0) {
            await grantSkillAccess(workspaceId, workspaceState, bindingId, agentInstanceIds);
          }
          summary.attachedSkills += 1;
        } catch (error) {
          summary.failures.push(`Skill ${skillCandidate.name}: ${error.message || 'failed to attach'}`);
        }
      }

      return summary;
    } catch (error) {
      summary.failures.push(error.message || 'Workspace setup could not be completed');
      return summary;
    } finally {
      state.applying = false;
      if (createBtn) {
        createBtn.disabled = false;
        createBtn.textContent = 'Create Workspace';
      }
      if (backBtn) backBtn.disabled = false;
    }
  }

  function reset() {
    resetReviewState();
    refreshPrimaryActionLabel();
  }

  function initializeListeners() {
    if (state.initialized) return;
    state.initialized = true;

    const dirtyIds = [
      'folderNameInput',
      'folderPrimaryGoalInput',
      'folderSystemsInput',
      'folderContextInput',
      'folderImportToggle',
      'folderImportPathInput'
    ];

    dirtyIds.forEach((id) => {
      const element = getElement(id);
      if (!element) return;
      const eventName = element.tagName === 'SELECT' || element.type === 'checkbox' ? 'change' : 'input';
      element.addEventListener(eventName, markDirty);
    });

    const backBtn = getBackButton();
    if (backBtn) {
      backBtn.addEventListener('click', () => {
        resetReviewState();
      });
    }

    const addFolderModal = getElement('addFolderModal');
    addFolderModal?.addEventListener('show.bs.modal', () => {
      resetReviewState();
    });
  }

  initializeListeners();
  refreshPrimaryActionLabel();

  window.WorkspaceBootstrapReview = {
    ensureReviewed,
    getSelectedPlan,
    applyPlan,
    reset,
    markDirty,
    refreshPrimaryActionLabel
  };
})();
