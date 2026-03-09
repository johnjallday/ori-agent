// Agent Edit Page JavaScript

let agentName = '';
let currentAgent = null;
let selectedTags = [];
let currentAvatarImage = null;
const modelOptions = [
  'gpt-4o',
  'gpt-4o-mini',
  'gpt-4-turbo',
  'gpt-3.5-turbo',
  'gpt-5.4',
  'gpt-5-codex',
  'gpt-5-codex-mini',
  'gpt-5.1-codex',
  'gpt-5.1-codex-mini',
  'gpt-5.1-codex-max',
  'gpt-5.2-codex',
  'gpt-5.3-codex',
  'codex-mini-latest',
  'claude-3-5-sonnet-20241022',
  'claude-3-opus-20240229',
  'claude-3-sonnet-20240229',
  'claude-3-haiku-20240307',
  'llama3.2',
  'llama3.1',
  'mistral',
  'codellama'
];

function supportsCodexReasoning(providerName, modelName) {
  const provider = String(providerName || '').trim().toLowerCase();
  const model = String(modelName || '').trim().toLowerCase();
  return provider === 'codex' || model.includes('codex');
}

function updateReasoningVisibility() {
  const field = document.getElementById('agentReasoningField');
  const select = document.getElementById('agentReasoningEffort');
  const modelInput = document.getElementById('agentModel');
  if (!field || !select || !modelInput) {
    return;
  }

  const provider = currentAgent?.provider || '';
  const show = supportsCodexReasoning(provider, modelInput.value);
  field.style.display = show ? 'block' : 'none';
  select.disabled = !show;
  if (show && !select.value) {
    select.value = 'medium';
  }
}

// Get agent name from URL - supports both path and query parameter
function getAgentNameFromURL() {
  // First try query parameter (agents-edit.html?name=xxx)
  const params = new URLSearchParams(window.location.search);
  const queryName = params.get('name');
  if (queryName) return queryName;
  // Fall back to path-based URL if ever needed
  const pathMatch = window.location.pathname.match(/\/agents\/([^/]+)\/edit/);
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
  setupAvatarUpload();
  populateModelDatalist();
  document.getElementById('agentModel')?.addEventListener('input', updateReasoningVisibility);
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
  const reasoningSelect = document.getElementById('agentReasoningEffort');
  if (reasoningSelect) {
    reasoningSelect.value = agent.reasoning_effort || 'medium';
  }

  const metadata = agent.metadata || {};
  document.getElementById('agentDescription').value = metadata.description || '';

  const color = metadata.avatar_color || '#4f46e5';
  document.getElementById('avatarColor').value = color;
  updateColorPreview(color);

  // Handle avatar image
  currentAvatarImage = metadata.avatar_image || null;
  updateAvatarPreview();

  selectedTags = metadata.tags || [];
  renderTags();

  document.getElementById('favoriteToggle').checked = Boolean(metadata.favorite);
  updateReasoningVisibility();
}

async function updateAgent() {
  const newName = document.getElementById('agentNameInput').value.trim();
  const type = document.getElementById('agentType').value;
  const role = document.getElementById('agentRole').value;
  const model = document.getElementById('agentModel').value.trim();
  const reasoningEffort = document.getElementById('agentReasoningEffort')?.value || 'medium';
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
  if (supportsCodexReasoning(currentAgent?.provider, model)) {
    payload.reasoning_effort = reasoningEffort;
  }

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

// ============================================
// Avatar Upload Functions
// ============================================

function setupAvatarUpload() {
  const fileInput = document.getElementById('avatarFileInput');
  const dropZone = document.getElementById('avatarDropZone');
  const removeBtn = document.getElementById('removeAvatarBtn');

  if (fileInput) {
    fileInput.addEventListener('change', handleAvatarFileSelect);
  }

  if (dropZone) {
    dropZone.addEventListener('dragover', handleDragOver);
    dropZone.addEventListener('dragleave', handleDragLeave);
    dropZone.addEventListener('drop', handleDrop);
    dropZone.addEventListener('click', () => fileInput?.click());
  }

  if (removeBtn) {
    removeBtn.addEventListener('click', removeAvatar);
  }
}

function handleDragOver(e) {
  e.preventDefault();
  e.stopPropagation();
  e.currentTarget.style.borderColor = 'var(--primary-color)';
  e.currentTarget.style.backgroundColor = 'var(--bg-tertiary)';
}

function handleDragLeave(e) {
  e.preventDefault();
  e.stopPropagation();
  e.currentTarget.style.borderColor = 'var(--border-color)';
  e.currentTarget.style.backgroundColor = 'var(--bg-secondary)';
}

function handleDrop(e) {
  e.preventDefault();
  e.stopPropagation();
  e.currentTarget.style.borderColor = 'var(--border-color)';
  e.currentTarget.style.backgroundColor = 'var(--bg-secondary)';

  const files = e.dataTransfer.files;
  if (files.length > 0) {
    uploadAvatarFile(files[0]);
  }
}

function handleAvatarFileSelect(e) {
  const files = e.target.files;
  if (files.length > 0) {
    uploadAvatarFile(files[0]);
  }
}

async function uploadAvatarFile(file) {
  // Validate file type
  const allowedTypes = ['image/png', 'image/jpeg', 'image/gif', 'image/webp'];
  if (!allowedTypes.includes(file.type)) {
    setAvatarStatus('Invalid file type. Allowed: PNG, JPG, GIF, WebP', 'error');
    return;
  }

  // Validate file size (5MB max)
  const maxSize = 5 * 1024 * 1024;
  if (file.size > maxSize) {
    setAvatarStatus('File too large. Maximum size is 5MB.', 'error');
    return;
  }

  // Show preview immediately
  previewAvatarFile(file);

  // Upload the file
  const formData = new FormData();
  formData.append('avatar', file);

  try {
    setAvatarStatus('Uploading...', 'info');

    const response = await fetch(`/api/agents/${encodeURIComponent(agentName)}/avatar`, {
      method: 'POST',
      body: formData
    });

    if (!response.ok) {
      const err = await safeParseError(response);
      throw new Error(err || 'Failed to upload avatar');
    }

    const data = await response.json();
    currentAvatarImage = data.avatar_url?.replace('/avatars/', '') || null;
    setAvatarStatus('Avatar uploaded successfully!', 'success');
    updateAvatarPreview();
  } catch (error) {
    console.error('Error uploading avatar:', error);
    setAvatarStatus(error.message || 'Failed to upload avatar', 'error');
    // Reset preview if upload failed
    updateAvatarPreview();
  }
}

function previewAvatarFile(file) {
  const reader = new FileReader();
  reader.onload = (e) => {
    const img = document.getElementById('avatarImg');
    const placeholder = document.getElementById('avatarPlaceholder');
    if (img) {
      img.src = e.target.result;
      img.style.display = 'block';
    }
    if (placeholder) {
      placeholder.style.display = 'none';
    }
  };
  reader.readAsDataURL(file);
}

async function removeAvatar() {
  if (!currentAvatarImage) return;

  try {
    setAvatarStatus('Removing...', 'info');

    const response = await fetch(`/api/agents/${encodeURIComponent(agentName)}/avatar`, {
      method: 'DELETE'
    });

    if (!response.ok) {
      const err = await safeParseError(response);
      throw new Error(err || 'Failed to remove avatar');
    }

    currentAvatarImage = null;
    setAvatarStatus('Avatar removed.', 'success');
    updateAvatarPreview();
  } catch (error) {
    console.error('Error removing avatar:', error);
    setAvatarStatus(error.message || 'Failed to remove avatar', 'error');
  }
}

function updateAvatarPreview() {
  const img = document.getElementById('avatarImg');
  const placeholder = document.getElementById('avatarPlaceholder');
  const actionsDiv = document.getElementById('avatarActions');
  const previewContainer = document.getElementById('avatarImagePreview');

  if (currentAvatarImage) {
    // Show the image
    if (img) {
      img.src = `/avatars/${currentAvatarImage}`;
      img.style.display = 'block';
    }
    if (placeholder) {
      placeholder.style.display = 'none';
    }
    if (actionsDiv) {
      actionsDiv.style.display = 'block';
    }
    if (previewContainer) {
      previewContainer.style.borderStyle = 'solid';
    }
  } else {
    // Show placeholder
    if (img) {
      img.src = '';
      img.style.display = 'none';
    }
    if (placeholder) {
      placeholder.style.display = 'block';
    }
    if (actionsDiv) {
      actionsDiv.style.display = 'none';
    }
    if (previewContainer) {
      previewContainer.style.borderStyle = 'dashed';
    }
  }
}

function setAvatarStatus(message, type = 'info') {
  const statusEl = document.getElementById('avatarUploadStatus');
  if (!statusEl) return;

  statusEl.textContent = message;

  if (type === 'error') {
    statusEl.style.color = 'var(--danger-color)';
  } else if (type === 'success') {
    statusEl.style.color = 'var(--success-color, #22c55e)';
  } else {
    statusEl.style.color = 'var(--text-secondary)';
  }

  // Clear status after 3 seconds for success/info messages
  if (type !== 'error') {
    setTimeout(() => {
      if (statusEl.textContent === message) {
        statusEl.textContent = '';
      }
    }, 3000);
  }
}
