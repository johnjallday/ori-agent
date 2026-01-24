// Skills Page

let skillsAll = [];
let skillsFiltered = [];
let selectedAgentName = '';
let defaultAgentName = '';

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

function renderSkills(skills) {
  const container = document.getElementById('skillsList');
  if (!container) return;

  if (!skills || skills.length === 0) {
    setSkillsCount(0);
    setSkillsMessage('No skills available for this agent.', false);
    return;
  }

  container.innerHTML = '';
  skills.forEach(skill => {
    const name = skill?.name || '(unnamed skill)';
    const description = skill?.description || 'No description';
    const source = skill?.source || 'local';

    const card = document.createElement('div');
    card.className = 'plugin-item';
    card.style.cursor = 'default';
    card.innerHTML = `
      <div style="display: flex; align-items: flex-start; justify-content: space-between; gap: 8px;">
        <div style="min-width: 0;">
          <div style="font-weight: 600; color: var(--text-primary);">${safeText(name)}</div>
          <div style="font-size: 12px; color: var(--text-secondary); margin-top: 4px;">${safeText(description)}</div>
        </div>
        <span class="badge bg-secondary" style="font-size: 10px; text-transform: uppercase; letter-spacing: 0.3px;">${safeText(source)}</span>
      </div>
    `;
    container.appendChild(card);
  });

  setSkillsCount(skills.length);
}

function applySkillsFilter() {
  const input = document.getElementById('skillsSearch');
  const query = input ? input.value.trim().toLowerCase() : '';

  if (!query) {
    skillsFiltered = [...skillsAll];
    renderSkills(skillsFiltered);
    return;
  }

  skillsFiltered = skillsAll.filter(skill => {
    const name = (skill?.name || '').toLowerCase();
    const description = (skill?.description || '').toLowerCase();
    const source = (skill?.source || '').toLowerCase();
    return name.includes(query) || description.includes(query) || source.includes(query);
  });

  renderSkills(skillsFiltered);
}

async function loadSkills(agentName) {
  setSkillsMessage('Loading skills...', false);

  const url = agentName
    ? `/api/skills?agent=${encodeURIComponent(agentName)}`
    : '/api/skills';

  try {
    const response = await fetch(url);
    if (!response.ok) {
      throw new Error('Failed to load skills');
    }
    const data = await response.json();
    skillsAll = Array.isArray(data.skills) ? data.skills : [];
    skillsAll.sort((a, b) => (a.name || '').localeCompare(b.name || '', undefined, { sensitivity: 'base' }));
    applySkillsFilter();
  } catch (error) {
    console.error('Failed to load skills:', error);
    skillsAll = [];
    skillsFiltered = [];
    setSkillsCount(0);
    setSkillsMessage('Failed to load skills.', true);
  }
}

function populateAgentSelect(agents, selected) {
  const select = document.getElementById('skillsAgentSelect');
  if (!select) return;

  const names = (agents || [])
    .map(getAgentDisplayName)
    .filter(name => name);

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
    const current = data.current || defaultAgentName;
    populateAgentSelect(data.agents || [], current);
  } catch (error) {
    console.error('Failed to load agents:', error);
    populateAgentSelect([], defaultAgentName);
  }
}

function setupSkillsEvents() {
  const select = document.getElementById('skillsAgentSelect');
  const search = document.getElementById('skillsSearch');
  const refresh = document.getElementById('skillsRefreshBtn');

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
}

async function initializeSkillsPage() {
  defaultAgentName = getSkillPageDefaultAgent();
  setupSkillsEvents();
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
