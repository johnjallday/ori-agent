// Dedicated, read-only details page for the Claude Code CLI agent.
// Renders the synced ~/.claude state via the shared ClaudeSync module.

function getClaudeAgentNameFromURL() {
  const parts = window.location.pathname.split('/').filter(Boolean);
  // /agents/{name}
  const raw = parts.length >= 2 ? parts[1] : 'Claude Code';
  try {
    return decodeURIComponent(raw);
  } catch (error) {
    return raw;
  }
}

const claudeAgentName = getClaudeAgentNameFromURL();

function setClaudeDetailMessage(message) {
  const missing = document.getElementById('claudeDetailMissing');
  const content = document.getElementById('claudeSyncContent');
  if (missing) {
    missing.hidden = !message;
    missing.textContent = message || '';
  }
  if (content && message) {
    content.innerHTML = '';
  }
}

function renderClaudeAgentPage(detail) {
  const content = document.getElementById('claudeSyncContent');
  if (!content || !window.ClaudeSync) {
    return;
  }

  const titleEl = document.getElementById('claudeDetailTitle');
  if (titleEl && detail?.name) {
    titleEl.textContent = detail.name;
  }

  setClaudeDetailMessage('');
  content.innerHTML = window.ClaudeSync.renderHtml(detail?.claude_sync || null);
  window.ClaudeSync.wireRefresh(content, claudeAgentName, reloaded => {
    renderClaudeAgentPage(reloaded);
  });
}

async function loadClaudeAgentDetail() {
  setClaudeDetailMessage('Loading…');
  try {
    const response = await fetch(`/api/agents/${encodeURIComponent(claudeAgentName)}/detail`);
    if (response.status === 404) {
      setClaudeDetailMessage(
        'Claude Code agent not found. The Claude Code CLI may not be installed or detected.'
      );
      return;
    }
    if (!response.ok) {
      throw new Error(`Failed to load (${response.status})`);
    }
    const detail = await response.json();

    if (!window.ClaudeSync || !window.ClaudeSync.isClaudeCodeAgent(detail)) {
      // Not actually the Claude Code agent — send the user to the generic page.
      window.location.replace('/agents');
      return;
    }

    renderClaudeAgentPage(detail);
  } catch (error) {
    console.error('Failed to load Claude Code agent detail:', error);
    setClaudeDetailMessage(error?.message || 'Failed to load Claude Code agent details.');
  }
}

document.addEventListener('DOMContentLoaded', loadClaudeAgentDetail);
