// Agent Edit Page JavaScript

let agentName = '';
let currentAgent = null;
let selectedTags = [];
const modelOptions = [
  'gpt-4o',
  'gpt-4o-mini',
  'gpt-4-turbo',
  'gpt-3.5-turbo',
  'claude-3-5-sonnet-20241022',
  'claude-3-opus-20240229',
  'claude-3-sonnet-20240229',
  'claude-3-haiku-20240307',
  'llama3.2',
  'llama3.1',
  'mistral',
  'codellama'
];

// Get agent name from URL - supports both path and query parameter
function getAgentNameFromURL() {
  // First try query parameter (agents-edit.html?name=xxx)
  const params = new URLSearchParams(window.location.search);
  const queryName = params.get('name');
  if (queryName) return queryName;
  // Fall back to path-based URL if ever needed
  const pathMatch = window.location.pathname.match(/\/agents\/([^\/]+)\/edit/);
  if (pathMatch) return decodeURIComponent(pathMatch[1]);
  return null;
}

document.addEventListener('DOMContentLoaded', () => {
  agentName = getAgentNameFromURL();

  if (!agentName) {
    showError('No agent specified');
    document.getElementById('saveBtn').disabled = true;
    return;
  }

  document.getElementById('saveBtn').addEventListener('click', updateAgent);
  document.getElementById('cancelBtn').addEventListener('click', () => {
    window.location.href = `/agents/${encodeURIComponent(agentName)}`;
  });

  const backLink = document.getElementById('backLink');
  backLink.href = `/agents/${encodeURIComponent(agentName)}`;

  setupTagsInput();
  populateModelDatalist();
  loadAgentDetails();
});

async function loadAgentDetails() {
  try {
    showLoading(true, 'Loading agent...');
    const response = await fetch(`/api/agents/${encodeURIComponent(agentName)}/detail`);
    if (!response.ok) {
      throw new Error(response.status === 404 ? 'Agent not found' : 'Failed to load agent details');
    }

    currentAgent = await response.json();
    populateForm(currentAgent);
  } catch (error) {
    console.error('Error loading agent details:', error);
    showError(error.message || 'Failed to load agent details');
  } finally {
    showLoading(false);
  }
}

function populateForm(agent) {
  document.getElementById('agentNameInput').value = agent.name || '';
  document.getElementById('agentType').value = agent.type || 'tool-calling';
  document.getElementById('agentRole').value = agent.role || 'general';
  document.getElementById('agentModel').value = agent.model || '';

  const metadata = agent.metadata || {};
  document.getElementById('agentDescription').value = metadata.description || '';

  const color = metadata.avatar_color || '#4f46e5';
  document.getElementById('avatarColor').value = color;
  updateColorPreview(color);

  selectedTags = metadata.tags || [];
  renderTags();

  document.getElementById('favoriteToggle').checked = Boolean(metadata.favorite);
}

async function updateAgent() {
  const newName = document.getElementById('agentNameInput').value.trim();
  const type = document.getElementById('agentType').value;
  const role = document.getElementById('agentRole').value;
  const model = document.getElementById('agentModel').value.trim();
  if (!newName) {
    showError('Name is required');
    return;
  }
  if (!type || !role) {
    showError('Type and role are required');
    return;
  }
  if (!model) {
    showError('Model is required');
    return;
  }
  const description = document.getElementById('agentDescription').value.trim();
  const avatarColor = document.getElementById('avatarColor').value;
  const favorite = document.getElementById('favoriteToggle').checked;

  const payload = {
    name: newName || agentName,
    type,
    role,
    model,
    description,
    avatar_color: avatarColor,
    tags: selectedTags,
    favorite
  };

  try {
    showLoading(true, 'Saving changes...');
    document.getElementById('saveBtn').disabled = true;

    const response = await fetch(`/api/agents/${encodeURIComponent(agentName)}`, {
      method: 'PATCH',
      headers: {
        'Content-Type': 'application/json'
      },
      body: JSON.stringify(payload)
    });

    if (!response.ok) {
      const err = await safeParseError(response);
      throw new Error(err || 'Failed to update agent');
    }

    const data = await response.json();
    const updatedName = data.name || newName || agentName;
    agentName = updatedName;
    showSuccess('Agent updated successfully.');
    setTimeout(() => {
      window.location.href = `/agents/${encodeURIComponent(updatedName)}`;
    }, 800);
  } catch (error) {
    console.error('Error updating agent:', error);
    showError(error.message || 'Failed to update agent');
  } finally {
    document.getElementById('saveBtn').disabled = false;
    showLoading(false);
  }
}

async function safeParseError(response) {
  try {
    const data = await response.json();
    return data.error || data.message;
  } catch {
    return null;
  }
}

function setupTagsInput() {
  const input = document.getElementById('tagsInput');
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

function addTag(tag) {
  if (!selectedTags.includes(tag)) {
    selectedTags.push(tag);
    renderTags();
  }
}

function removeTag(tag) {
  selectedTags = selectedTags.filter(t => t !== tag);
  renderTags();
}

function renderTags() {
  const container = document.getElementById('tagsContainer');
  const input = document.getElementById('tagsInput');

  container.querySelectorAll('.tag-item').forEach(tag => tag.remove());

  selectedTags.forEach(tag => {
    const tagEl = document.createElement('div');
    tagEl.className = 'tag-item';
    const label = document.createElement('span');
    label.textContent = tag;
    const remove = document.createElement('span');
    remove.className = 'tag-remove';
    remove.textContent = '×';
    remove.addEventListener('click', () => removeTag(tag));
    tagEl.appendChild(label);
    tagEl.appendChild(remove);
    container.insertBefore(tagEl, input);
  });
}

function updateColorPreview(color) {
  document.getElementById('colorPreview').style.background = color;
}

function showError(message) {
  const el = document.getElementById('errorMessage');
  el.textContent = message;
  el.style.display = 'block';
  document.getElementById('successMessage').style.display = 'none';
}

function showSuccess(message) {
  const el = document.getElementById('successMessage');
  el.textContent = message;
  el.style.display = 'block';
  document.getElementById('errorMessage').style.display = 'none';
}

function showLoading(show, message = '') {
  const overlay = document.getElementById('loadingOverlay');
  overlay.style.display = show ? 'flex' : 'none';
  const text = overlay.querySelector('p');
  if (text && message) {
    text.textContent = message;
  }
}

function populateModelDatalist() {
  const list = document.getElementById('modelOptionsList');
  if (!list) return;
  list.innerHTML = '';
  const unique = Array.from(new Set(modelOptions));
  unique.forEach((value) => {
    const option = document.createElement('option');
    option.value = value;
    list.appendChild(option);
  });
}

function capitalize(str) {
  if (!str) return '';
  return str.charAt(0).toUpperCase() + str.slice(1);
}
