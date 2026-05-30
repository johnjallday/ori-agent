/**
 * Workspace Hub Support Chat
 *
 * Floating, Intercom-style chat widget pinned to the bottom-right of
 * workspace pages. The widget routes to a different agent and adopts a
 * different persona depending on which surface it's loaded on:
 *
 *  - workspace_hub    -> "Ori" (system assistant); focused on workspace
 *                        creation/lookup/navigation.
 *  - workspace_detail -> workspace.shared_data.entry_agent_name (strict);
 *                        focused on what's available inside the workspace.
 *  - workspace_task   -> task.assigned_to (strict); focused on this task.
 *
 * @module workspace-hub-support-chat
 */
(function () {
  'use strict';

  const SESSION_HEADER = 'X-Session-ID';
  const WIDGET_SESSION_PREFIX = 'support-widget-';
  const SYSTEM_AGENT_NAME = 'Ori';

  const SURFACES = {
    workspace_hub: {
      title: 'Workspaces Assistant',
      subtitle: 'Create, find, and navigate workspaces',
      placeholder: 'Ask about workspaces...',
      emptyTitle: 'Hi! How can I help with your workspaces?',
      emptySub: 'I can help you create, find, or summarize workspaces.',
      resolveAgent: () => SYSTEM_AGENT_NAME,
      requireAgent: false
    },
    workspace_detail: {
      title: 'Workspace Assistant',
      subtitle: 'Ask about this workspace',
      placeholder: 'Ask about tasks, notes, files, agents...',
      emptyTitle: 'Hi! How can I help?',
      emptySub: 'I have context on this workspace.',
      resolveAgent: () => {
        const ws = window.workspaceDetail && window.workspaceDetail.workspace;
        if (!ws) return '';
        const shared = ws.shared_data || ws.SharedData || {};
        const entry = shared.entry_agent_name || ws.entry_agent_name || '';
        return typeof entry === 'string' ? entry.trim() : '';
      },
      requireAgent: true,
      missingAgentMessage:
        'No entry agent is configured for this workspace yet. ' +
        'Set one in workspace settings to enable the assistant.'
    },
    workspace_task: {
      title: 'Task Assistant',
      subtitle: 'Help with this task',
      placeholder: 'Ask about this task...',
      emptyTitle: 'Hi! How can I help with this task?',
      emptySub: 'I have context on this task and workspace.',
      resolveAgent: () => {
        const task = window.workspaceTaskPage && window.workspaceTaskPage.task;
        if (!task || typeof task !== 'object') return '';
        return typeof task.assigned_to === 'string' ? task.assigned_to.trim() : '';
      },
      requireAgent: true,
      missingAgentMessage:
        'No agent is assigned to this task yet. ' +
        'Assign an agent to enable the assistant.'
    }
  };

  let elements = null;
  let surface = null;
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

  function detectSurface() {
    const path = (window.location.pathname || '').toLowerCase();
    if (/^\/workspaces\/[^/]+\/(?:task|tasks)\/[^/]+/.test(path)) return 'workspace_task';
    if (/^\/workspaces\/[^/]+/.test(path)) return 'workspace_detail';
    return 'workspace_hub';
  }

  function resolveTaskId() {
    if (window.workspaceTaskPage && window.workspaceTaskPage.taskId) {
      return window.workspaceTaskPage.taskId;
    }
    const match = (window.location.pathname || '').match(/\/workspaces\/[^/]+\/(?:task|tasks)\/([^/]+)/i);
    return match ? decodeURIComponent(match[1]) : '';
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

    const agentName = surface.resolveAgent();
    if (surface.requireAgent && !agentName) {
      appendMessage(trimmed, 'user');
      elements.input.value = '';
      autoResizeInput();
      appendMessage(surface.missingAgentMessage, 'error');
      return;
    }

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
      if (agentName) body.agent_name = agentName;
      const routeContext = {
        page_path: window.location.pathname || '',
        origin: 'support_widget',
        surface: surface.key
      };
      const workspaceId = resolveWorkspaceId();
      if (workspaceId) routeContext.workspace_id = workspaceId;
      if (surface.key === 'workspace_task') {
        const taskId = resolveTaskId();
        if (taskId) routeContext.task_id = taskId;
      }
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

  function applySurfaceToUI() {
    const titleEl = $('hubSupportChatTitle');
    const subtitleEl = $('hubSupportChatSubtitle');
    if (titleEl) titleEl.textContent = surface.title;
    if (subtitleEl) subtitleEl.textContent = surface.subtitle;
    if (elements.input) elements.input.placeholder = surface.placeholder;
    const emptyTitle = elements.body.querySelector('.hub-support-chat-empty-title');
    const emptySub = elements.body.querySelector('.hub-support-chat-empty-sub');
    if (emptyTitle) emptyTitle.textContent = surface.emptyTitle;
    if (emptySub) emptySub.textContent = surface.emptySub;
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

    const key = detectSurface();
    surface = Object.assign({ key }, SURFACES[key] || SURFACES.workspace_hub);

    applySurfaceToUI();
    bindHandlers();
    window.hubSupportChat = {
      open: openPanel,
      close: closePanel,
      toggle: togglePanel,
      send: sendMessage,
      surface: () => surface.key
    };
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();
