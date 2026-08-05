/**
 * Workspace Input Router
 * Bridges Workspace Hub smart input with Ask Ori routing.
 *
 * @module workspace-input-router
 */
(function () {
  'use strict';

  const ASK_COMMAND_RE = /^\/ask\b/i;

  function getCurrentSessionId() {
    if (window.sessionManager && window.sessionManager.currentSessionId) {
      return String(window.sessionManager.currentSessionId).trim();
    }
    return '';
  }

  function canUseAskOri() {
    return Boolean(window.OriAskRouting && typeof window.OriAskRouting.submit === 'function');
  }

  function isAskCommand(input) {
    return ASK_COMMAND_RE.test(String(input || '').trim());
  }

  function extractAskPrompt(input) {
    return String(input || '')
      .replace(ASK_COMMAND_RE, '')
      .trim();
  }

  function buildWorkspaceHubRouteContext(workspaceId) {
    const pagePath =
      (window.location &&
        typeof window.location.pathname === 'string' &&
        window.location.pathname) ||
      '/workspaces';
    return {
      surface: 'workspace_hub',
      page_path: pagePath,
      workspace_id: String(workspaceId || '').trim(),
      session_id: getCurrentSessionId(),
      origin: 'workspace_hub_input'
    };
  }

  async function dispatchToAskOri(input, options = {}) {
    if (!canUseAskOri()) {
      throw new Error('Ask Ori routing is unavailable');
    }

    const prompt = extractAskPrompt(input);
    if (!prompt) {
      throw new Error('Usage: /ask <what you want Ori to do>');
    }

    const routeContext = buildWorkspaceHubRouteContext(options.workspaceId);
    await window.OriAskRouting.submit(prompt, {
      routeContext,
      openThinkingModal: true
    });

    return { routed: 'ask_ori', routeContext };
  }

  window.WorkspaceInputRouter = {
    canUseAskOri,
    isAskCommand,
    extractAskPrompt,
    buildWorkspaceHubRouteContext,
    dispatchToAskOri
  };
})();
