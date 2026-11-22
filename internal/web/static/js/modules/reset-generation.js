(function () {
  const generationKey = 'ori.reset.generation';
  const tabGenerationKey = 'ori.reset.tab_generation';
  const exactLocal = [
    'ori-theme',
    'ori-ui-density',
    'voiceSettings',
    'note.openBehavior',
    'enterToSend',
    'planBeforeAction',
    'chatPanelWidth',
    'sidebarWidth',
    'sessionFolderCollapsed',
    'sessionSidebarCollapsed',
    'sessionSidebarWidth',
    'oriWorkspaceHubLauncherView',
    'oriWorkspaceCommandViewMode',
    'oriWorkspaceDetailView',
    'canvas-bg-color',
    'note.leftRail.tab',
    'note.toc.collapsed',
    'note.aiAssist.collapsed',
    'ori_chat_assistant',
    'activeSessionId',
    'ori.homeAssistant.recentSessions',
    'oriWorkspaceSyncDismissed',
    'note.tabs',
    'note.search.recent',
    'ori.roster.selectedAgent',
    'ori-selected-vault-id',
    'ori-vault-active-tab',
    'ori.personalAssistantHireRequestId',
    'ori.personalAssistantHQRequestId',
    'ori.homeAssistant.automationMode'
  ];
  const exactSession = [
    'activeSessionId',
    'ori.homeAssistant.recentSessions',
    'oriWorkspaceHubOverviewExpanded',
    'oriWorkspaceHubLauncherTab',
    'oriWorkspaceHubSelectedId',
    'currentWorkspaceId',
    'workspace-detail-task-assist-specialist',
    'ori.homeAssistant.pendingWorkspacePrompt',
    'ori-guide-handoff',
    'ori.homeAssistant.automationMode',
    'ori-keyboard-nav-persistent'
  ];
  const prefixes = [
    'ori_chat_session_',
    'ori_chat_assistant_',
    'activeSessionId_',
    'note.tabs.workspace.',
    'ori.evolution.lastStage.',
    'ori.personalAssistantApplyRequestId.',
    'workspace-directory-explorer:',
    'ori-workspace-command-agent:',
    'oriSetupWizardResume:',
    'oriProjectOpenNotice:',
    'workspace-detail-entry-agent-prompt-dismissed:'
  ];

  const clear = (storage, exact) => {
    exact.forEach(key => storage.removeItem(key));
    const matches = [];
    for (let index = 0; index < storage.length; index += 1) {
      const key = storage.key(index);
      if (key && prefixes.some(prefix => key.startsWith(prefix))) matches.push(key);
    }
    matches.forEach(key => storage.removeItem(key));
  };

  function alignTab(id) {
    if (!id) return;
    try {
      const storage = window.sessionStorage;
      if (storage && storage.getItem(tabGenerationKey) !== id) {
        clear(storage, exactSession);
        storage.setItem(tabGenerationKey, id);
      }
    } catch {
      // Optional browser storage does not affect the verified server outcome.
    }
  }

  if (window.location.pathname === '/reset/status') {
    // Only the reset status controller calls this, after verifying this exact
    // Start Fresh receipt and normal-app readiness in the same response.
    window.OriResetGeneration = {
      apply(id) {
        if (!id) return;
        try {
          const storage = window.localStorage;
          if (storage && storage.getItem(generationKey) !== id) {
            clear(storage, exactLocal);
            storage.setItem(generationKey, id);
          }
        } catch {
          // Continue to clean this tab when only session storage is usable.
        }
        alignTab(id);
      }
    };
    return; // Watching another tab's generation must not reload this status page.
  }

  const currentGeneration = () => {
    try {
      return window.localStorage?.getItem(generationKey) || '';
    } catch {
      return '';
    }
  };
  const loaded = currentGeneration();
  alignTab(loaded);
  let reloading = false;
  function reloadStaleTab() {
    const current = currentGeneration();
    if (reloading || !current || current === loaded) return;
    reloading = true;
    alignTab(current);
    window.location.reload();
  }
  window.addEventListener('storage', event => {
    if (event.key === generationKey) reloadStaleTab();
  });
  window.addEventListener('pageshow', event => {
    if (event.persisted) reloadStaleTab();
  });
})();
