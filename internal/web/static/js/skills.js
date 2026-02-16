// Skills Page

let skillsAll = [];
let skillsFiltered = [];
let selectedAgentName = '';
let defaultAgentName = '';
let editingSkillName = '';

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
    const source = getSourceLabel(skill?.source);
    const isEditable = source === 'agent';
    const validationErrors = Array.isArray(skill?.validation_errors) ? skill.validation_errors : [];
    const hasErrors = validationErrors.length > 0;
    const isEnabled = skill?.enabled !== false;
    const hasScripts = Boolean(skill?.has_scripts);
    const isTrusted = Boolean(skill?.trusted);
    const requiresTrust = hasScripts && !isTrusted;

    const card = document.createElement('div');
    card.className = 'plugin-item';
    card.style.cursor = 'default';
    card.style.display = 'flex';
    card.style.flexDirection = 'column';
    card.style.height = '100%';

    const badges = [];
    badges.push(`<span class="badge bg-secondary" style="font-size: 10px; text-transform: uppercase; letter-spacing: 0.3px;">${safeText(source)}</span>`);
    if (!isEnabled) {
      badges.push('<span class="badge bg-warning" style="font-size: 10px; text-transform: uppercase; letter-spacing: 0.3px;">disabled</span>');
    }
    if (hasErrors) {
      badges.push('<span class="badge bg-danger" style="font-size: 10px; text-transform: uppercase; letter-spacing: 0.3px;">invalid</span>');
    }
    if (hasScripts) {
      badges.push(`<span class="badge ${isTrusted ? 'bg-success' : 'bg-danger'}" style="font-size: 10px; text-transform: uppercase; letter-spacing: 0.3px;">${isTrusted ? 'trusted' : 'untrusted'}</span>`);
    }

    const errorHtml = hasErrors
      ? `<div style="font-size: 11px; color: var(--danger-color); margin-top: 6px;">${safeText(validationErrors.join('; '))}</div>`
      : '';

    card.innerHTML = `
      <div style="display: flex; align-items: flex-start; justify-content: space-between; gap: 8px; flex: 1 1 auto;">
        <div style="min-width: 0;">
          <div style="font-weight: 600; color: var(--text-primary);">${safeText(name)}</div>
          <div style="font-size: 12px; color: var(--text-secondary); margin-top: 4px;">${safeText(description)}</div>
          ${errorHtml}
        </div>
        <div style="display: flex; flex-wrap: wrap; gap: 4px; justify-content: flex-end;">${badges.join('')}</div>
      </div>
      <div style="display: flex; align-items: center; gap: 8px; flex-wrap: wrap; margin-top: auto; padding-top: 12px;">
        <button class="modern-btn modern-btn-secondary btn-sm" data-action="run" ${(!isEnabled || requiresTrust || hasErrors) ? 'disabled' : ''}>Run</button>
        <button class="modern-btn modern-btn-secondary btn-sm" data-action="edit" ${isEditable ? '' : 'disabled'}>Edit</button>
        <button class="modern-btn modern-btn-secondary btn-sm" data-action="delete" ${isEditable ? '' : 'disabled'}>Delete</button>
        ${hasScripts ? `<button class="modern-btn modern-btn-secondary btn-sm" data-action="trust">${isTrusted ? 'Untrust' : 'Trust'}</button>` : ''}
        <div class="form-check form-switch" style="margin-left: auto;">
          <input class="form-check-input" type="checkbox" ${isEnabled ? 'checked' : ''} data-action="toggle" />
          <label class="form-check-label" style="font-size: 12px; color: var(--text-secondary);">Enabled</label>
        </div>
      </div>
    `;

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

    const trustBtn = card.querySelector('[data-action="trust"]');
    if (trustBtn) {
      trustBtn.addEventListener('click', () => toggleSkillTrust(skill));
    }

    const toggle = card.querySelector('[data-action="toggle"]');
    if (toggle) {
      toggle.addEventListener('change', () => setSkillEnabled(skill, toggle.checked));
    }

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
  clearSkillsError();

  const url = agentName
    ? `/api/skills?agent=${encodeURIComponent(agentName)}`
    : '/api/skills';

  try {
    const response = await fetch(url);
    if (response.status === 409) {
      const data = await response.json();
      const conflicts = Array.isArray(data.conflicts) ? data.conflicts : [];
      const conflictHtml = conflicts.map(conflict => {
        const paths = (conflict.paths || []).map(path => `<li>${safeText(path)}</li>`).join('');
        return `<li><strong>${safeText(conflict.name)}</strong><ul>${paths}</ul></li>`;
      }).join('');
      showSkillsError(`<strong>Duplicate skill names detected.</strong><br/>Resolve these conflicts to continue.<ul>${conflictHtml}</ul>`);
      skillsAll = [];
      skillsFiltered = [];
      setSkillsCount(0);
      setSkillsMessage('Resolve skill conflicts to view skills.', true);
      return;
    }
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
  const createBtn = document.getElementById('skillsCreateBtn');
  const saveBtn = document.getElementById('skillsSaveBtn');

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

  if (saveBtn) {
    saveBtn.addEventListener('click', () => saveSkillEditor());
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

function openSkillEditor(skill) {
  editingSkillName = skill?.name || '';
  const title = document.getElementById('skillsEditorTitle');
  const nameInput = document.getElementById('skillNameInput');
  const descriptionInput = document.getElementById('skillDescriptionInput');
  const promptInput = document.getElementById('skillPromptInput');
  const enabledInput = document.getElementById('skillEnabledInput');
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
  if (enabledInput) enabledInput.checked = skill ? (skill.enabled !== false) : true;

  const modal = getEditorModal();
  if (modal) modal.show();

  if (skill && promptInput) {
    fetch(`/api/skills/${encodeURIComponent(skill.name)}?agent=${encodeURIComponent(selectedAgentName)}`)
      .then(res => res.ok ? res.json() : Promise.reject(new Error('Failed to load skill')))
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
  }
}

async function saveSkillEditor() {
  const nameInput = document.getElementById('skillNameInput');
  const descriptionInput = document.getElementById('skillDescriptionInput');
  const promptInput = document.getElementById('skillPromptInput');
  const enabledInput = document.getElementById('skillEnabledInput');
  const errorBox = document.getElementById('skillsEditorError');

  const payload = {
    agent: selectedAgentName,
    name: nameInput ? nameInput.value.trim() : '',
    description: descriptionInput ? descriptionInput.value.trim() : '',
    prompt: promptInput ? promptInput.value : '',
    enabled: enabledInput ? enabledInput.checked : true,
  };

  if (payload.prompt.trim() === 'Loading...') {
    if (errorBox) {
      errorBox.textContent = 'Skill content is still loading. Try again in a moment.';
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
  const url = isEdit
    ? `/api/skills/${encodeURIComponent(editingSkillName)}`
    : '/api/skills';
  const method = isEdit ? 'PUT' : 'POST';

  try {
    const response = await fetch(url, {
      method,
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
    });

    if (!response.ok) {
      const data = await response.json().catch(() => ({}));
      const message = data?.error || 'Failed to save skill.';
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
    const response = await fetch(`/api/skills/${encodeURIComponent(skill.name)}?agent=${encodeURIComponent(selectedAgentName)}`, {
      method: 'DELETE',
    });
    if (!response.ok && response.status !== 204) {
      throw new Error('Failed to delete skill');
    }
    await loadSkills(selectedAgentName);
  } catch (error) {
    console.error('Failed to delete skill:', error);
    showToast('Failed to delete skill.', 'error');
  }
}

async function setSkillEnabled(skill, enabled) {
  if (!skill?.name || !selectedAgentName) return;
  try {
    const response = await fetch(`/api/skills/${encodeURIComponent(skill.name)}/enable`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ agent: selectedAgentName, enabled }),
    });
    if (!response.ok) {
      throw new Error('Failed to update skill state');
    }
    await loadSkills(selectedAgentName);
  } catch (error) {
    console.error('Failed to update skill state:', error);
    showToast('Failed to update skill state.', 'error');
  }
}

async function toggleSkillTrust(skill) {
  if (!skill?.name || !selectedAgentName) return;
  try {
    const response = await fetch(`/api/skills/${encodeURIComponent(skill.name)}/trust`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ agent: selectedAgentName, trusted: !skill.trusted }),
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
      body: JSON.stringify({ question: `/${name}`, agent_name: selectedAgentName }),
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
