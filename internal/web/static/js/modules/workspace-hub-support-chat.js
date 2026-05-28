/**
 * Workspace Hub Support Chat
 *
 * Floating, Intercom-style chat widget pinned to the bottom-right of the
 * workspace-hub page. Sends messages directly to /api/chat using the agent
 * resolved from the currently-open workspace, and renders the conversation
 * inside its own panel (independent of the main chat UI).
 *
 * @module workspace-hub-support-chat
 */
(function () {
  'use strict';

  const SESSION_HEADER = 'X-Session-ID';
  const WIDGET_SESSION_PREFIX = 'support-widget-';

  let elements = null;
  let widgetSessionId = '';
  let sending = false;

  function $(id) {
    return document.getElementById(id);
  }

  function escapeHtml(value) {
    return String(value == null ? '' : value)
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;')
      .replace(/'/g, '&#39;');
  }

  function renderMarkdown(text) {
    if (window.marked && typeof window.marked.parse === 'function') {
      try {
        const html = window.marked.parse(String(text || ''), { breaks: true });
        if (window.DOMPurify && typeof window.DOMPurify.sanitize === 'function') {
          return window.DOMPurify.sanitize(html);
        }
        return html;
      } catch (_) {
        // fall through to plain text
      }
    }
    return escapeHtml(text).replace(/\n/g, '<br>');
  }

  function pickFirstAgentName(workspace) {
    if (!workspace || typeof workspace !== 'object') return '';
    const instances = Array.isArray(workspace.agent_instances) ? workspace.agent_instances : null;
    if (instances && instances.length > 0) {
      const first = instances[0];
      if (first && typeof first === 'object' && first.name) return first.name;
      if (typeof first === 'string') return first;
    }
    const names = Array.isArray(workspace.agents) ? workspace.agents : null;
    if (names && names.length > 0) return names[0];
    return '';
  }

  function resolveWorkspaceAgent() {
    const detail = window.workspaceDetail;
    if (detail) {
      const fromDetail = pickFirstAgentName(detail.workspace);
      if (fromDetail) return fromDetail;
    }
    const taskPage = window.workspaceTaskPage;
    if (taskPage) {
      const fromTask = pickFirstAgentName(taskPage.workspace) || pickFirstAgentName(taskPage.workspaceData);
      if (fromTask) return fromTask;
      if (taskPage.task && typeof taskPage.task === 'object' && taskPage.task.agent_name) {
        return taskPage.task.agent_name;
      }
    }
    const activeAgent = window.sessionManager
      && typeof window.sessionManager.getActiveSession === 'function'
      ? window.sessionManager.getActiveSession()?.agent_name
      : '';
    return activeAgent || '';
  }

  function resolveWorkspaceId() {
    if (window.workspaceDetail && window.workspaceDetail.workspaceId) {
      return window.workspaceDetail.workspaceId;
    }
    if (window.workspaceTaskPage && window.workspaceTaskPage.workspaceId) {
      return window.workspaceTaskPage.workspaceId;
    }
    if (window.currentWorkspaceId) return window.currentWorkspaceId;
    return '';
  }

  function ensureSessionId() {
    if (widgetSessionId) return widgetSessionId;
    const rand = Math.random().toString(36).slice(2, 10);
    widgetSessionId = `${WIDGET_SESSION_PREFIX}${Date.now().toString(36)}-${rand}`;
    return widgetSessionId;
  }

  function clearEmptyState() {
    const empty = elements.body.querySelector('.hub-support-chat-empty');
    if (empty) empty.remove();
  }

  function appendMessage(text, kind) {
    clearEmptyState();
    const node = document.createElement('div');
    node.className = `hub-support-chat-msg hub-support-chat-msg-${kind}`;
    if (kind === 'bot') {
      node.innerHTML = renderMarkdown(text);
    } else {
      node.textContent = String(text || '');
    }
    elements.body.appendChild(node);
    elements.body.scrollTop = elements.body.scrollHeight;
    return node;
  }

  function showTyping() {
    const node = document.createElement('div');
    node.className = 'hub-support-chat-typing';
    node.setAttribute('aria-label', 'Assistant is typing');
    node.innerHTML = '<span></span><span></span><span></span>';
    elements.body.appendChild(node);
    elements.body.scrollTop = elements.body.scrollHeight;
    return node;
  }

  function setSending(isSending) {
    sending = isSending;
    elements.sendBtn.disabled = isSending;
    elements.input.disabled = isSending;
  }

  async function sendMessage(text) {
    const trimmed = String(text || '').trim();
    if (!trimmed || sending) return;

    appendMessage(trimmed, 'user');
    elements.input.value = '';
    autoResizeInput();
    setSending(true);
    const typingNode = showTyping();

    try {
      const headers = {
        'Content-Type': 'application/json',
        [SESSION_HEADER]: ensureSessionId()
      };
      const body = { question: trimmed };
      const agentName = resolveWorkspaceAgent();
      if (agentName) body.agent_name = agentName;
      const workspaceId = resolveWorkspaceId();
      const routeContext = {
        page_path: window.location.pathname || '',
        origin: 'support_widget'
      };
      if (workspaceId) routeContext.workspace_id = workspaceId;
      body.route_context = routeContext;

      const response = await fetch('/api/chat', {
        method: 'POST',
        headers,
        body: JSON.stringify(body)
      });

      typingNode.remove();

      if (!response.ok) {
        throw new Error(`HTTP ${response.status}`);
      }
      const data = await response.json();
      const replyText = data && (data.response || data.message || data.text);
      if (replyText) {
        appendMessage(replyText, 'bot');
      } else {
        appendMessage('(no response)', 'bot');
      }
    } catch (err) {
      typingNode.remove();
      appendMessage(`Couldn't reach the assistant: ${err && err.message ? err.message : err}`, 'error');
    } finally {
      setSending(false);
      elements.input.focus();
    }
  }

  function autoResizeInput() {
    const el = elements.input;
    el.style.height = 'auto';
    el.style.height = Math.min(el.scrollHeight, 96) + 'px';
  }

  function openPanel() {
    elements.root.setAttribute('data-state', 'open');
    elements.launcher.setAttribute('aria-expanded', 'true');
    elements.panel.setAttribute('aria-hidden', 'false');
    setTimeout(() => elements.input.focus(), 30);
  }

  function closePanel() {
    elements.root.setAttribute('data-state', 'closed');
    elements.launcher.setAttribute('aria-expanded', 'false');
    elements.panel.setAttribute('aria-hidden', 'true');
  }

  function togglePanel() {
    if (elements.root.getAttribute('data-state') === 'open') {
      closePanel();
    } else {
      openPanel();
    }
  }

  function bindHandlers() {
    elements.launcher.addEventListener('click', togglePanel);
    elements.closeBtn.addEventListener('click', closePanel);
    elements.form.addEventListener('submit', (e) => {
      e.preventDefault();
      sendMessage(elements.input.value);
    });
    elements.input.addEventListener('keydown', (e) => {
      if (e.key === 'Enter' && !e.shiftKey) {
        e.preventDefault();
        sendMessage(elements.input.value);
      }
    });
    elements.input.addEventListener('input', autoResizeInput);
    document.addEventListener('keydown', (e) => {
      if (e.key === 'Escape' && elements.root.getAttribute('data-state') === 'open') {
        closePanel();
      }
    });
  }

  function init() {
    const root = $('hubSupportChat');
    if (!root) return;
    elements = {
      root,
      launcher: $('hubSupportChatLauncher'),
      panel: $('hubSupportChatPanel'),
      closeBtn: $('hubSupportChatCloseBtn'),
      body: $('hubSupportChatBody'),
      form: $('hubSupportChatForm'),
      input: $('hubSupportChatInput'),
      sendBtn: $('hubSupportChatSendBtn')
    };
    if (!elements.launcher || !elements.panel || !elements.form) return;
    bindHandlers();
    window.hubSupportChat = {
      open: openPanel,
      close: closePanel,
      toggle: togglePanel,
      send: sendMessage
    };
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();
