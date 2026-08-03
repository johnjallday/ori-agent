// Dedicated, read-only details page for the Codex CLI agent.
// Renders the synced ~/.codex state via the shared CodexSync module.

function getCodexAgentNameFromURL() {
  const parts = window.location.pathname.split('/').filter(Boolean);
  // /agents/{name}
  const raw = parts.length >= 2 ? parts[1] : 'Codex';
  try {
    return decodeURIComponent(raw);
  } catch (error) {
    return raw;
  }
}

const codexAgentName = getCodexAgentNameFromURL();

function setCodexDetailMessage(message) {
  const missing = document.getElementById('codexDetailMissing');
  const content = document.getElementById('codexSyncContent');
  if (missing) {
    missing.hidden = !message;
    missing.textContent = message || '';
  }
  if (content && message) {
    content.innerHTML = '';
  }
}

function renderCodexAgentPage(detail) {
  const content = document.getElementById('codexSyncContent');
  if (!content || !window.CodexSync) {
    return;
  }

  const titleEl = document.getElementById('codexDetailTitle');
  if (titleEl && detail?.name) {
    titleEl.textContent = detail.name;
  }

  setCodexDetailMessage('');
  content.innerHTML = window.CodexSync.renderHtml(detail?.codex_sync || null);
  window.CodexSync.wireRefresh(content, codexAgentName, reloaded => {
    renderCodexAgentPage(reloaded);
  });
}

async function loadCodexAgentDetail() {
  setCodexDetailMessage('Loading...');
  try {
    const response = await fetch(`/api/agents/${encodeURIComponent(codexAgentName)}/detail`);
    if (response.status === 404) {
      setCodexDetailMessage(
        'Codex agent not found. The Codex CLI may not be installed or detected.'
      );
      return;
    }
    if (!response.ok) {
      throw new Error(`Failed to load (${response.status})`);
    }
    const detail = await response.json();

    if (!window.CodexSync || !window.CodexSync.isCodexAgent(detail)) {
      // Not actually the Codex agent - send the user to the generic page.
      window.location.replace('/agents');
      return;
    }

    renderCodexAgentPage(detail);
  } catch (error) {
    console.error('Failed to load Codex agent detail:', error);
    setCodexDetailMessage(error?.message || 'Failed to load Codex agent details.');
  }
}

document.addEventListener('DOMContentLoaded', loadCodexAgentDetail);
