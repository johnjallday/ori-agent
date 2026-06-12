/**
 * Floating Workspace Assistant shell.
 *
 * The assistant workflow itself lives in dashboard.js. This module owns only
 * the floating panel chrome: launcher, surface labels, focus, and textarea
 * ergonomics. It deliberately does not post directly to /api/chat.
 */
(function () {
  'use strict';

  const SURFACES = {
    workspace_hub: {
      title: 'Workspaces Assistant',
      subtitle: 'Create, find, and navigate workspaces',
      inputLabel: 'Workspaces assistant prompt'
    },
    workspace_detail: {
      title: 'Workspace Assistant',
      subtitle: 'Tasks, questions, and notes for this workspace',
      inputLabel: 'Workspace manager prompt'
    },
    workspace_task: {
      title: 'Task Assistant',
      subtitle: 'Help with this task',
      inputLabel: 'Task assistant prompt'
    }
  };

  let elements = null;
  let surface = null;
  let lastFocus = null;

  function $(id) {
    return document.getElementById(id);
  }

  function detectSurface() {
    const path = (window.location.pathname || '').toLowerCase();
    if (/^\/workspaces\/[^/]+\/(?:task|tasks)\/[^/]+/.test(path)) return 'workspace_task';
    if (/^\/workspaces\/[^/]+/.test(path)) return 'workspace_detail';
    return 'workspace_hub';
  }

  function getFocusableElements() {
    if (!elements || !elements.panel) return [];
    return Array.from(elements.panel.querySelectorAll(
      'a[href], button:not([disabled]), textarea:not([disabled]), input:not([disabled]), select:not([disabled]), [tabindex]:not([tabindex="-1"])'
    )).filter((node) => {
      if (!node || node.hidden) return false;
      const style = window.getComputedStyle(node);
      return style.display !== 'none' && style.visibility !== 'hidden';
    });
  }

  function autoResizeInput() {
    if (!elements || !elements.input) return;
    const el = elements.input;
    el.style.height = 'auto';
    el.style.height = Math.min(el.scrollHeight, 120) + 'px';
  }

  function applySurfaceToUI() {
    const titleEl = $('hubSupportChatTitle');
    const subtitleEl = $('hubSupportChatSubtitle');
    const inputEl = $('homeAssistantInput');
    const config = surface || SURFACES.workspace_detail;

    if (titleEl) titleEl.textContent = config.title;
    if (subtitleEl) subtitleEl.textContent = config.subtitle;
    if (inputEl) inputEl.setAttribute('aria-label', config.inputLabel);
  }

  // Page modules call this once they know the concrete context (e.g. the
  // workspace name) so the header names what the assistant is acting on.
  function setSubtitle(text) {
    const subtitleEl = $('hubSupportChatSubtitle');
    if (!subtitleEl) return;
    const value = String(text || '').trim();
    subtitleEl.textContent = value || (surface ? surface.subtitle : '');
  }

  function setPanelState(open) {
    if (!elements || !elements.root) return;
    elements.root.setAttribute('data-state', open ? 'open' : 'closed');
    elements.launcher.setAttribute('aria-expanded', open ? 'true' : 'false');
    elements.panel.setAttribute('aria-hidden', open ? 'false' : 'true');
    document.body.classList.toggle('hub-support-chat-open', open);
  }

  function openPanel(options = {}) {
    if (!elements || !elements.root) return;
    if (elements.root.getAttribute('data-state') !== 'open') {
      lastFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    }
    setPanelState(true);
    window.setTimeout(() => {
      const target = options.focus === 'panel' ? elements.panel : elements.input;
      if (target && typeof target.focus === 'function') target.focus();
    }, 30);
  }

  function closePanel() {
    if (!elements || !elements.root) return;
    setPanelState(false);
    if (lastFocus && typeof lastFocus.focus === 'function' && document.contains(lastFocus)) {
      window.setTimeout(() => lastFocus.focus(), 30);
    } else if (elements.launcher && typeof elements.launcher.focus === 'function') {
      window.setTimeout(() => elements.launcher.focus(), 30);
    }
  }

  function togglePanel() {
    if (!elements || !elements.root) return;
    if (elements.root.getAttribute('data-state') === 'open') {
      closePanel();
    } else {
      openPanel();
    }
  }

  function handlePanelKeydown(event) {
    if (!elements || elements.root.getAttribute('data-state') !== 'open') return;

    if (event.key === 'Escape') {
      event.preventDefault();
      closePanel();
      return;
    }

    if (event.key !== 'Tab') return;

    const focusable = getFocusableElements();
    if (focusable.length === 0) {
      event.preventDefault();
      elements.panel.focus();
      return;
    }

    const first = focusable[0];
    const last = focusable[focusable.length - 1];
    const active = document.activeElement;

    if (event.shiftKey && active === first) {
      event.preventDefault();
      last.focus();
    } else if (!event.shiftKey && active === last) {
      event.preventDefault();
      first.focus();
    }
  }

  function bindHandlers() {
    elements.launcher.addEventListener('click', togglePanel);
    elements.closeBtn.addEventListener('click', closePanel);
    elements.panel.addEventListener('keydown', handlePanelKeydown);

    if (elements.input) {
      elements.input.addEventListener('input', autoResizeInput);
      elements.input.addEventListener('keydown', (event) => {
        if (event.key === 'Enter' && !event.shiftKey) {
          event.preventDefault();
          if (elements.form && typeof elements.form.requestSubmit === 'function') {
            elements.form.requestSubmit();
          } else if (elements.form) {
            elements.form.dispatchEvent(new Event('submit', { bubbles: true, cancelable: true }));
          }
        }
      });
    }

    document.addEventListener('keydown', (event) => {
      if (event.key === 'Escape' && elements.root.getAttribute('data-state') === 'open') {
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
      form: $('homeAssistantForm'),
      input: $('homeAssistantInput')
    };
    if (!elements.launcher || !elements.panel || !elements.closeBtn) return;
    document.body.classList.add('has-hub-support-chat');

    surface = Object.assign(
      { key: detectSurface() },
      SURFACES[detectSurface()] || SURFACES.workspace_detail
    );

    applySurfaceToUI();
    bindHandlers();
    autoResizeInput();

    window.hubSupportChat = {
      open: openPanel,
      close: closePanel,
      toggle: togglePanel,
      surface: () => surface.key,
      resize: autoResizeInput,
      setSubtitle: setSubtitle
    };
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();
