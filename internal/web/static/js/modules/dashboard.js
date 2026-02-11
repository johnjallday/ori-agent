// Dashboard Module
// Fetches and renders Ori dashboard data: assistant progress, stats, and agent list

(function () {
  'use strict';

  const dashLog = typeof Logger !== 'undefined' ? Logger.withContext('Dashboard') : console;
  const XP_PER_LEVEL = 100;
  const HOME_ASSISTANT_MESSAGE_LIMIT = 60;
  const HOME_ASSISTANT_RECENT_SESSION_LIMIT = 8;
  const HOME_ASSISTANT_RECENT_SESSION_RENDER_LIMIT = 5;
  const HOME_ASSISTANT_BACKEND_LOOKUP_LIMIT = 50;
  const HOME_ASSISTANT_SESSION_STORAGE_KEY = 'ori.homeAssistant.recentSessions';

  const HOME_INTENTS = {
    travel_planning: {
      key: 'travel_planning',
      label: 'travel planning',
      keywords: ['trip', 'travel', 'itinerary', 'vacation', 'los angeles', 'la', 'weekend', 'hotel', 'flight'],
      preferredPlugins: ['web', 'weather', 'maps', 'search', 'travel'],
      preferredTypes: ['research', 'general', 'tool-calling'],
      defaultType: 'research',
      suggestedName: 'Travel Planner',
      tags: ['travel', 'itinerary', 'planning']
    },
    email_check: {
      key: 'email_check',
      label: 'email triage',
      keywords: ['email', 'inbox', 'mail', 'gmail', 'outlook', 'unread', 'reply', 'messages'],
      preferredPlugins: ['email', 'gmail', 'outlook', 'imap'],
      preferredTypes: ['tool-calling', 'general'],
      defaultType: 'tool-calling',
      suggestedName: 'Email Assistant',
      tags: ['email', 'inbox', 'communication']
    },
    app_launch: {
      key: 'app_launch',
      label: 'app launch',
      keywords: ['open', 'launch', 'start', 'run', 'application', 'app', 'obsidian', 'reaper', 'finder'],
      preferredPlugins: ['shell', 'executor', 'desktop', 'automation', 'os-shell', 'command'],
      preferredTypes: ['tool-calling', 'general'],
      defaultType: 'tool-calling',
      suggestedName: 'Desktop Launcher',
      tags: ['desktop', 'automation', 'apps']
    },
    general_task: {
      key: 'general_task',
      label: 'general task',
      keywords: [],
      preferredPlugins: [],
      preferredTypes: ['general', 'tool-calling', 'research'],
      defaultType: 'general',
      suggestedName: 'Task Assistant',
      tags: ['tasks', 'assistant']
    }
  };

  const HOME_MCP_REQUIREMENTS = [
    {
      key: 'github_ops',
      label: 'GitHub operations',
      phrases: ['github', 'repository', 'repo', 'pull request', 'pull-request', 'issue', 'commit', 'branch', 'release'],
      preferredServerNames: ['github'],
      preferredCategories: ['development']
    },
    {
      key: 'web_research',
      label: 'web research',
      phrases: ['search the web', 'web search', 'search online', 'look up', 'lookup', 'internet search', 'latest news'],
      preferredServerNames: ['brave-search'],
      preferredCategories: ['search']
    },
    {
      key: 'database_query',
      label: 'database query',
      phrases: ['postgres', 'postgresql', 'database', 'sql query', 'run sql', 'db query', 'schema'],
      preferredServerNames: ['postgres'],
      preferredCategories: ['database']
    },
    {
      key: 'filesystem_ops',
      label: 'filesystem access',
      phrases: ['filesystem', 'file system', 'local files', 'read file', 'write file', 'directory', 'folder on my computer'],
      preferredServerNames: ['filesystem'],
      preferredCategories: ['file-system']
    }
  ];

  const homeAssistantState = {
    pendingPrompt: '',
    pendingIntent: HOME_INTENTS.general_task,
    pendingAgentName: '',
    pendingSuggestedName: '',
    pendingSuggestedType: '',
    pendingAppLaunch: null,
    awaitingCreateConfirmation: false,
    busy: false,
    recentSessions: [],
    mode: 'new_task',
    routingSummary: null
  };

  var providersCache = null;
  var HOME_ASSISTANT_COMMON_TOKENS = {
    a: true, an: true, and: true, are: true, can: true, do: true, for: true, help: true,
    i: true, in: true, is: true, it: true, my: true, of: true, on: true, open: true,
    or: true, please: true, task: true, that: true, the: true, this: true, to: true,
    want: true, with: true, you: true
  };

  function toTitleCase(value) {
    return String(value || '')
      .split(/[\s_-]+/)
      .filter(Boolean)
      .map(function (word) { return word.charAt(0).toUpperCase() + word.slice(1).toLowerCase(); })
      .join(' ');
  }

  function formatNumber(value) {
    return new Intl.NumberFormat().format(Number(value || 0));
  }

  function formatCost(value) {
    var num = Number(value || 0);
    return '$' + num.toFixed(4);
  }

  function setGreeting() {
    var el = document.getElementById('dashboardGreeting');
    if (!el) return;
    var hour = new Date().getHours();
    var greeting;
    if (hour < 12) greeting = 'Good morning!';
    else if (hour < 17) greeting = 'Good afternoon!';
    else greeting = 'Good evening!';
    el.textContent = greeting;
  }

  function normalizeToken(value) {
    return String(value || '').trim().toLowerCase();
  }

  function uniqueValues(values) {
    var seen = Object.create(null);
    var out = [];
    for (var i = 0; i < values.length; i++) {
      var item = values[i];
      if (!item || seen[item]) continue;
      seen[item] = true;
      out.push(item);
    }
    return out;
  }

  function normalizeConfirmationText(value) {
    return normalizeToken(value).replace(/[.!?]/g, '').trim();
  }

  function truncateText(value, maxLength) {
    var text = String(value || '').trim();
    if (text.length <= maxLength) return text;
    return text.slice(0, Math.max(0, maxLength - 3)) + '...';
  }

  function formatRelativeTime(timestamp) {
    var ts = normalizeTimestamp(timestamp);
    var diffMs = Date.now() - ts;
    if (diffMs < 0) return 'just now';

    var minute = 60 * 1000;
    var hour = 60 * minute;
    var day = 24 * hour;

    if (diffMs < minute) return 'just now';
    if (diffMs < hour) return Math.floor(diffMs / minute) + 'm ago';
    if (diffMs < day) return Math.floor(diffMs / hour) + 'h ago';
    if (diffMs < 7 * day) return Math.floor(diffMs / day) + 'd ago';

    var dt = new Date(ts);
    var month = dt.getMonth() + 1;
    var dayOfMonth = dt.getDate();
    return month + '/' + dayOfMonth;
  }

  function getPersistentStorage() {
    if (window.localStorage) return window.localStorage;
    if (window.sessionStorage) return window.sessionStorage;
    return null;
  }

  function normalizeTimestamp(value) {
    if (typeof value === 'number' && isFinite(value)) return value;
    if (typeof value === 'string') {
      var parsedDate = Date.parse(value);
      if (!Number.isNaN(parsedDate)) return parsedDate;
      var parsedNumber = Number(value);
      if (isFinite(parsedNumber)) return parsedNumber;
    }
    return Date.now();
  }

  function normalizeRecentSessionItem(item) {
    var current = item || {};
    if (!current.id || !current.agent_name) return null;
    return {
      id: String(current.id),
      agent_name: String(current.agent_name),
      title: String(current.title || 'New Session'),
      prompt: String(current.prompt || ''),
      created_at: normalizeTimestamp(current.created_at)
    };
  }

  function normalizeRecentSessionList(items) {
    if (!Array.isArray(items)) return [];
    var result = [];
    for (var i = 0; i < items.length; i++) {
      var normalized = normalizeRecentSessionItem(items[i]);
      if (!normalized) continue;
      result.push(normalized);
      if (result.length >= HOME_ASSISTANT_RECENT_SESSION_LIMIT) break;
    }
    return result;
  }

  function loadHomeAssistantRecentSessions() {
    try {
      var storage = getPersistentStorage();
      if (!storage) return [];
      var raw = storage.getItem(HOME_ASSISTANT_SESSION_STORAGE_KEY);
      if (!raw && window.sessionStorage && storage !== window.sessionStorage) {
        // Backward compatibility with older tab-scoped storage key.
        raw = window.sessionStorage.getItem(HOME_ASSISTANT_SESSION_STORAGE_KEY);
      }
      if (!raw) return [];
      var parsed = JSON.parse(raw);
      return normalizeRecentSessionList(parsed);
    } catch (error) {
      dashLog.debug('Failed to load Ask Ori recent sessions', { error: error && error.message || error });
      return [];
    }
  }

  function saveHomeAssistantRecentSessions() {
    try {
      var storage = getPersistentStorage();
      if (!storage) return;
      storage.setItem(
        HOME_ASSISTANT_SESSION_STORAGE_KEY,
        JSON.stringify(normalizeRecentSessionList(homeAssistantState.recentSessions))
      );
      // Keep sessionStorage in sync for existing tabs.
      if (window.sessionStorage && storage !== window.sessionStorage) {
        window.sessionStorage.setItem(
          HOME_ASSISTANT_SESSION_STORAGE_KEY,
          JSON.stringify(normalizeRecentSessionList(homeAssistantState.recentSessions))
        );
      }
    } catch (error) {
      dashLog.debug('Failed to persist Ask Ori recent sessions', { error: error && error.message || error });
    }
  }

  async function fetchRecentSessionsFromBackend() {
    try {
      var data = await API.get('/api/sessions?limit=' + HOME_ASSISTANT_BACKEND_LOOKUP_LIMIT + '&sort=updated_desc');
      var sessions = Array.isArray(data && data.sessions) ? data.sessions : [];
      var recent = [];
      for (var i = 0; i < sessions.length; i++) {
        var normalized = normalizeRecentSessionItem({
          id: sessions[i].id,
          agent_name: sessions[i].agent_name,
          title: sessions[i].title,
          created_at: sessions[i].updated_at
        });
        if (!normalized) continue;
        recent.push(normalized);
        if (recent.length >= HOME_ASSISTANT_RECENT_SESSION_LIMIT) break;
      }
      return recent;
    } catch (error) {
      dashLog.debug('Failed to fetch Ask Ori recent sessions from backend', { error: error && error.message || error });
      return [];
    }
  }

  async function hydrateHomeAssistantRecentSessions() {
    var backendRecent = await fetchRecentSessionsFromBackend();
    if (!Array.isArray(backendRecent) || backendRecent.length === 0) return;

    var byId = Object.create(null);
    var merged = [];
    for (var i = 0; i < backendRecent.length; i++) {
      byId[String(backendRecent[i].id)] = backendRecent[i];
    }

    for (var j = 0; j < homeAssistantState.recentSessions.length; j++) {
      var localItem = homeAssistantState.recentSessions[j];
      if (!localItem || !localItem.id) continue;
      var backendItem = byId[String(localItem.id)];
      if (!backendItem) continue;
      merged.push({
        id: String(localItem.id),
        agent_name: String(backendItem.agent_name || localItem.agent_name || ''),
        title: String(backendItem.title || localItem.title || 'New Session'),
        prompt: String(localItem.prompt || ''),
        created_at: normalizeTimestamp(localItem.created_at || backendItem.created_at)
      });
      delete byId[String(localItem.id)];
      if (merged.length >= HOME_ASSISTANT_RECENT_SESSION_LIMIT) break;
    }

    if (merged.length < HOME_ASSISTANT_RECENT_SESSION_LIMIT) {
      for (var k = 0; k < backendRecent.length; k++) {
        var candidate = backendRecent[k];
        if (!candidate || !candidate.id) continue;
        if (!byId[String(candidate.id)]) continue;
        merged.push(candidate);
        if (merged.length >= HOME_ASSISTANT_RECENT_SESSION_LIMIT) break;
      }
    }

    homeAssistantState.recentSessions = normalizeRecentSessionList(merged);
    saveHomeAssistantRecentSessions();
    renderHomeAssistantRecentSessions();
  }

  function isSignalPromptToken(token) {
    var normalized = normalizeToken(token);
    if (normalized.length < 4) return false;
    return HOME_ASSISTANT_COMMON_TOKENS[normalized] !== true;
  }

  function parseAppLaunchRequest(prompt) {
    var text = String(prompt || '').trim();
    if (!text) return null;

    var normalized = normalizeToken(text);
    var politePrefixes = ['please ', 'can you ', 'could you ', 'would you ', 'hey '];
    for (var i = 0; i < politePrefixes.length; i++) {
      if (normalized.indexOf(politePrefixes[i]) === 0) {
        normalized = normalized.slice(politePrefixes[i].length).trim();
        break;
      }
    }

    var commandPrefixes = ['open up ', 'open ', 'launch ', 'start ', 'run '];
    var target = '';
    for (var p = 0; p < commandPrefixes.length; p++) {
      if (normalized.indexOf(commandPrefixes[p]) === 0) {
        target = normalized.slice(commandPrefixes[p].length).trim();
        break;
      }
    }
    if (!target) return null;

    target = target.replace(/^[\s"'`]+|[\s"'`.,!?;:]+$/g, '');
    target = target.replace(/^the\s+/, '');
    target = target.replace(/\s+(app|application)\s*$/, '');
    target = target.trim();
    if (!target) return null;

    if (target.indexOf('://') >= 0 || target.indexOf('/') >= 0 || target.indexOf('\\') >= 0) {
      return null;
    }

    return {
      appName: toTitleCase(target),
      rawTarget: target
    };
  }

  function buildAskOriDispatchMessage(prompt, appLaunchRequest) {
    if (!appLaunchRequest || !appLaunchRequest.appName) {
      return String(prompt || '').trim();
    }
    return '/openapp ' + appLaunchRequest.appName;
  }

  function isAffirmativeConfirmation(value) {
    var text = normalizeConfirmationText(value);
    var accepted = {
      'yes': true,
      'y': true,
      'yeah': true,
      'yep': true,
      'sure': true,
      'ok': true,
      'okay': true,
      'yes please': true,
      'create one': true,
      'do it': true,
      'go ahead': true,
      'please do': true,
      '1': true,
      'iok': true
    };
    if (accepted[text]) return true;
    return text.indexOf('create') >= 0 && text.indexOf('agent') >= 0;
  }

  function isNegativeConfirmation(value) {
    var text = normalizeConfirmationText(value);
    var rejected = {
      'no': true,
      'n': true,
      'nope': true,
      'not now': true,
      'cancel': true,
      'stop': true,
      'later': true
    };
    return Boolean(rejected[text]);
  }

  function getHomeAssistantElements() {
    return {
      card: document.getElementById('homeAssistantCard'),
      form: document.getElementById('homeAssistantForm'),
      input: document.getElementById('homeAssistantInput'),
      sendBtn: document.getElementById('homeAssistantSendBtn'),
      conversation: document.getElementById('homeAssistantConversation'),
      routingSummary: document.getElementById('homeAssistantRoutingSummary'),
      quickPrompts: document.getElementById('homeAssistantQuickPrompts'),
      actions: document.getElementById('homeAssistantActions'),
      recentSection: document.getElementById('homeAssistantRecentSection'),
      recentSessions: document.getElementById('homeAssistantRecentSessions'),
      modeNewBtn: document.getElementById('homeAssistantModeNewBtn'),
      modeContinueBtn: document.getElementById('homeAssistantModeContinueBtn'),
      viewAllBtn: document.getElementById('homeAssistantViewAllBtn'),
      clearRecentBtn: document.getElementById('homeAssistantClearRecentBtn'),
      quickButtons: document.querySelectorAll('.home-assistant-quick-btn'),
      avatarBtn: document.getElementById('dashboardAssistantAvatarBtn'),
      bubbleBtn: document.getElementById('dashboardAssistantBubbleBtn')
    };
  }

  function renderHomeAssistantRoutingSummary() {
    var els = getHomeAssistantElements();
    var container = els.routingSummary;
    if (!container) return;

    var summary = homeAssistantState.routingSummary;
    if (!summary || !summary.text) {
      container.innerHTML = '';
      container.classList.add('d-none');
      return;
    }

    container.innerHTML = '';
    var title = document.createElement('span');
    title.className = 'home-assistant-routing-title';
    title.textContent = summary.title || 'Routing';

    var text = document.createElement('span');
    text.className = 'home-assistant-routing-text';
    text.textContent = summary.text;

    container.appendChild(title);
    container.appendChild(text);
    container.classList.remove('d-none');
  }

  function setHomeAssistantRoutingSummary(title, text) {
    if (!title || !text) {
      homeAssistantState.routingSummary = null;
      renderHomeAssistantRoutingSummary();
      return;
    }
    homeAssistantState.routingSummary = {
      title: String(title),
      text: String(text)
    };
    renderHomeAssistantRoutingSummary();
  }

  function setHomeAssistantMode(mode) {
    var nextMode = mode === 'continue_session' ? 'continue_session' : 'new_task';
    homeAssistantState.mode = nextMode;
    var els = getHomeAssistantElements();

    if (els.modeNewBtn) {
      var isNew = nextMode === 'new_task';
      els.modeNewBtn.classList.toggle('is-active', isNew);
      els.modeNewBtn.setAttribute('aria-selected', isNew ? 'true' : 'false');
    }
    if (els.modeContinueBtn) {
      var isContinue = nextMode === 'continue_session';
      els.modeContinueBtn.classList.toggle('is-active', isContinue);
      els.modeContinueBtn.setAttribute('aria-selected', isContinue ? 'true' : 'false');
    }

    if (els.quickPrompts) {
      els.quickPrompts.classList.toggle('d-none', nextMode === 'continue_session');
    }
    if (els.conversation) {
      els.conversation.classList.toggle('d-none', nextMode === 'continue_session');
    }
    if (els.input) {
      els.input.placeholder = nextMode === 'continue_session'
        ? 'Type a new task or open one of your recent sessions...'
        : 'Ask Ori to do something...';
    }

    renderHomeAssistantRecentSessions();
  }

  function focusHomeAssistantInput() {
    setHomeAssistantMode('new_task');
    var els = getHomeAssistantElements();
    if (!els.input) return;
    if (els.card && typeof els.card.scrollIntoView === 'function') {
      els.card.scrollIntoView({ behavior: 'smooth', block: 'center' });
    }
    window.setTimeout(function () {
      els.input.focus();
    }, 120);
  }

  function setHomeAssistantBusy(isBusy, busyLabel) {
    homeAssistantState.busy = Boolean(isBusy);
    var els = getHomeAssistantElements();
    if (!els.sendBtn || !els.input) return;
    if (!els.sendBtn.dataset.defaultLabel) {
      els.sendBtn.dataset.defaultLabel = els.sendBtn.textContent || 'Ask';
    }
    els.sendBtn.disabled = homeAssistantState.busy;
    els.input.disabled = homeAssistantState.busy;
    els.sendBtn.textContent = homeAssistantState.busy ? (busyLabel || 'Working...') : els.sendBtn.dataset.defaultLabel;
    for (var i = 0; i < els.quickButtons.length; i++) {
      els.quickButtons[i].disabled = homeAssistantState.busy;
    }
    if (els.modeNewBtn) els.modeNewBtn.disabled = homeAssistantState.busy;
    if (els.modeContinueBtn) els.modeContinueBtn.disabled = homeAssistantState.busy;
    if (els.viewAllBtn) els.viewAllBtn.disabled = homeAssistantState.busy;
    if (els.clearRecentBtn) els.clearRecentBtn.disabled = homeAssistantState.busy;
  }

  function appendHomeAssistantMessage(role, text) {
    var els = getHomeAssistantElements();
    var conversation = els.conversation;
    if (!conversation) return;

    if (!conversation.dataset.initialized) {
      conversation.innerHTML = '';
      conversation.dataset.initialized = 'true';
    }

    var row = document.createElement('div');
    row.style.display = 'flex';
    row.style.marginBottom = '0.65rem';
    row.style.justifyContent = role === 'user' ? 'flex-end' : 'flex-start';

    var bubble = document.createElement('div');
    bubble.style.maxWidth = '85%';
    bubble.style.padding = '0.55rem 0.75rem';
    bubble.style.borderRadius = '10px';
    bubble.style.whiteSpace = 'pre-wrap';
    bubble.style.fontSize = '0.85rem';
    bubble.style.lineHeight = '1.4';
    bubble.style.border = '1px solid var(--border-color)';
    bubble.style.color = 'var(--text-primary)';
    bubble.style.background = role === 'user' ? 'var(--primary-color-light)' : 'var(--bg-primary)';
    bubble.textContent = text;

    row.appendChild(bubble);
    conversation.appendChild(row);

    while (conversation.children.length > HOME_ASSISTANT_MESSAGE_LIMIT) {
      conversation.removeChild(conversation.firstChild);
    }
    conversation.scrollTop = conversation.scrollHeight;
  }

  function renderHomeAssistantActions(actions) {
    var els = getHomeAssistantElements();
    var container = els.actions;
    if (!container) return;

    container.innerHTML = '';
    if (!actions || actions.length === 0) {
      container.classList.add('d-none');
      return;
    }

    for (var i = 0; i < actions.length; i++) {
      (function (action) {
        var button = document.createElement('button');
        button.type = 'button';
        button.className = 'modern-btn ' + (action.variant === 'secondary' ? 'modern-btn-secondary' : 'modern-btn-primary');
        button.style.fontSize = '0.82rem';
        button.textContent = action.label;
        button.disabled = Boolean(action.disabled);
        button.addEventListener('click', function () {
          if (homeAssistantState.busy || typeof action.onClick !== 'function') return;
          action.onClick();
        });
        container.appendChild(button);
      })(actions[i]);
    }

    container.classList.remove('d-none');
  }

  function removeTrackedSession(sessionId) {
    if (!sessionId) return;
    var filtered = [];
    for (var i = 0; i < homeAssistantState.recentSessions.length; i++) {
      var current = homeAssistantState.recentSessions[i];
      if (!current || String(current.id) === String(sessionId)) continue;
      filtered.push(current);
    }
    homeAssistantState.recentSessions = filtered;
    saveHomeAssistantRecentSessions();
    renderHomeAssistantRecentSessions();
  }

  async function deleteTrackedSession(sessionId, sessionTitle) {
    if (!sessionId) return;
    var manager = window.sessionManager;
    var deleted = false;

    setHomeAssistantBusy(true, 'Deleting...');
    renderHomeAssistantActions([]);
    try {
      if (manager && typeof manager.deleteSession === 'function') {
        deleted = await manager.deleteSession(sessionId);
      } else {
        if (!window.confirm('Are you sure you want to delete this session?')) return;
        var response = await fetch('/api/sessions/' + encodeURIComponent(sessionId), { method: 'DELETE' });
        deleted = Boolean(response && response.ok);
      }

      if (!deleted) return;
      removeTrackedSession(sessionId);
      appendHomeAssistantMessage('assistant', 'Deleted session "' + (sessionTitle || 'Session') + '".');
    } catch (error) {
      dashLog.debug('Failed to delete tracked session', { error: error && error.message || error, sessionId: sessionId });
      appendHomeAssistantMessage('assistant', 'I could not delete that session right now.');
    } finally {
      setHomeAssistantBusy(false);
    }
  }

  async function openTrackedSession(sessionId) {
    if (!sessionId) return;
    var manager = window.sessionManager;
    if (!manager || typeof manager.switchToSession !== 'function') return;
    try {
      await manager.switchToSession(sessionId, true);
      setHomeAssistantRoutingSummary('Session Opened', 'Opened selected session in chat.');
      openChatPanel();
    } catch (error) {
      dashLog.debug('Failed to open tracked session', { error: error && error.message || error, sessionId: sessionId });
    }
  }

  function renderHomeAssistantRecentSessions() {
    var els = getHomeAssistantElements();
    var container = els.recentSessions;
    if (!container) return;

    container.innerHTML = '';
    var section = els.recentSection;
    if (!homeAssistantState.recentSessions || homeAssistantState.recentSessions.length === 0) {
      if (section && homeAssistantState.mode === 'continue_session') {
        var empty = document.createElement('div');
        empty.className = 'home-assistant-empty-note';
        empty.textContent = 'No recent sessions yet. Ask a task to create one.';
        container.appendChild(empty);
        section.classList.remove('d-none');
      } else if (section) {
        section.classList.add('d-none');
      }
      return;
    }

    for (var i = 0; i < homeAssistantState.recentSessions.length && i < HOME_ASSISTANT_RECENT_SESSION_RENDER_LIMIT; i++) {
      (function (item) {
        var titleText = String(item.prompt || item.title || 'Session');
        var row = document.createElement('div');
        row.className = 'home-assistant-session-row';

        var main = document.createElement('div');
        main.className = 'home-assistant-session-main';

        var title = document.createElement('div');
        title.className = 'home-assistant-session-title';
        title.textContent = truncateText(titleText, 52);

        var meta = document.createElement('div');
        meta.className = 'home-assistant-session-meta';
        meta.textContent = String(item.agent_name || 'Agent') + ' • ' + formatRelativeTime(item.created_at);

        main.appendChild(title);
        main.appendChild(meta);

        var actions = document.createElement('div');
        actions.className = 'home-assistant-session-actions';

        var openButton = document.createElement('button');
        openButton.type = 'button';
        openButton.className = 'modern-btn modern-btn-secondary';
        openButton.textContent = 'Open';
        openButton.title = 'Open session';
        openButton.addEventListener('click', function () {
          if (homeAssistantState.busy) return;
          setHomeAssistantMode('continue_session');
          openTrackedSession(item.id);
        });

        var deleteButton = document.createElement('button');
        deleteButton.type = 'button';
        deleteButton.className = 'modern-btn modern-btn-secondary';
        deleteButton.textContent = 'Delete';
        deleteButton.title = 'Delete session';
        deleteButton.addEventListener('click', function () {
          if (homeAssistantState.busy) return;
          deleteTrackedSession(item.id, item.title || titleText);
        });

        actions.appendChild(openButton);
        actions.appendChild(deleteButton);
        row.appendChild(main);
        row.appendChild(actions);
        container.appendChild(row);
      })(homeAssistantState.recentSessions[i]);
    }

    if (homeAssistantState.recentSessions.length > HOME_ASSISTANT_RECENT_SESSION_RENDER_LIMIT) {
      var more = document.createElement('div');
      more.className = 'home-assistant-empty-note';
      more.textContent = (homeAssistantState.recentSessions.length - HOME_ASSISTANT_RECENT_SESSION_RENDER_LIMIT) + ' more session(s) available in chat history.';
      container.appendChild(more);
    }

    if (section) {
      if (homeAssistantState.mode === 'continue_session') {
        section.classList.remove('d-none');
      } else {
        section.classList.add('d-none');
      }
    }
  }

  function clearHomeAssistantRecentSessionList() {
    homeAssistantState.recentSessions = [];
    saveHomeAssistantRecentSessions();
    renderHomeAssistantRecentSessions();
    appendHomeAssistantMessage('assistant', 'Cleared the recent session list from this panel.');
  }

  function trackHomeAssistantSession(session, prompt, agentName) {
    if (!session || !session.id) return;
    var entry = {
      id: String(session.id),
      agent_name: String(session.agent_name || agentName || ''),
      title: String(session.title || 'New Session'),
      prompt: String(prompt || ''),
      created_at: Date.now()
    };
    var next = [entry];
    for (var i = 0; i < homeAssistantState.recentSessions.length; i++) {
      var current = homeAssistantState.recentSessions[i];
      if (!current || String(current.id) === entry.id) continue;
      next.push(current);
      if (next.length >= HOME_ASSISTANT_RECENT_SESSION_LIMIT) break;
    }
    homeAssistantState.recentSessions = next;
    saveHomeAssistantRecentSessions();
    renderHomeAssistantRecentSessions();
  }

  function detectHomeIntent(prompt) {
    var text = normalizeToken(prompt);
    var selected = HOME_INTENTS.general_task;
    var selectedScore = 0;
    var keys = ['travel_planning', 'email_check'];
    for (var i = 0; i < keys.length; i++) {
      var intent = HOME_INTENTS[keys[i]];
      var score = 0;
      for (var k = 0; k < intent.keywords.length; k++) {
        if (text.indexOf(intent.keywords[k]) >= 0) score += 1;
      }
      if (score > selectedScore) {
        selected = intent;
        selectedScore = score;
      }
    }
    if (selectedScore > 0) return selected;

    if (parseAppLaunchRequest(prompt)) {
      return HOME_INTENTS.app_launch;
    }

    return selected;
  }

  function normalizeMCPServerName(name) {
    return normalizeToken(name).replace(/_/g, '-');
  }

  function scoreMCPRequirement(promptText, requirement) {
    if (!promptText || !requirement || !Array.isArray(requirement.phrases)) return 0;
    var score = 0;
    for (var i = 0; i < requirement.phrases.length; i++) {
      var phrase = normalizeToken(requirement.phrases[i]);
      if (!phrase) continue;
      if (promptText.indexOf(phrase) >= 0) {
        score += phrase.length >= 8 ? 2 : 1;
      }
    }
    return score;
  }

  function detectMCPRequirement(prompt) {
    var promptText = normalizeToken(prompt);
    if (!promptText) return null;

    var best = null;
    var bestScore = 0;
    for (var i = 0; i < HOME_MCP_REQUIREMENTS.length; i++) {
      var requirement = HOME_MCP_REQUIREMENTS[i];
      var score = scoreMCPRequirement(promptText, requirement);
      if (score > bestScore) {
        best = requirement;
        bestScore = score;
      }
    }
    if (bestScore <= 0) return null;
    return best;
  }

  function selectExistingMCPServer(requirement, servers) {
    if (!requirement || !Array.isArray(servers) || servers.length === 0) return null;
    var preferredNames = (requirement.preferredServerNames || []).map(normalizeMCPServerName);
    for (var i = 0; i < servers.length; i++) {
      var server = servers[i];
      var serverName = normalizeMCPServerName(server && server.name);
      if (!serverName) continue;
      if (preferredNames.indexOf(serverName) >= 0) return server;
    }
    return null;
  }

  function scoreMarketplaceMCPServer(requirement, server, promptText) {
    if (!requirement || !server) return 0;
    var score = 0;
    var serverName = normalizeMCPServerName(server.name);
    var category = normalizeToken(server.category);
    var description = normalizeToken(server.description);
    var preferredNames = (requirement.preferredServerNames || []).map(normalizeMCPServerName);
    var preferredCategories = (requirement.preferredCategories || []).map(normalizeToken);

    if (preferredNames.indexOf(serverName) >= 0) score += 100;
    if (preferredCategories.indexOf(category) >= 0) score += 40;
    if (description && scoreMCPRequirement(description, requirement) > 0) score += 20;
    if (promptText && serverName && promptText.indexOf(serverName) >= 0) score += 10;

    return score;
  }

  function chooseMarketplaceMCPServer(requirement, prompt, marketplaceServers) {
    if (!requirement || !Array.isArray(marketplaceServers)) return null;
    var promptText = normalizeToken(prompt);
    var best = null;
    var bestScore = 0;
    for (var i = 0; i < marketplaceServers.length; i++) {
      var server = marketplaceServers[i];
      var score = scoreMarketplaceMCPServer(requirement, server, promptText);
      if (score > bestScore) {
        best = server;
        bestScore = score;
      }
    }
    if (bestScore <= 0) return null;
    return best;
  }

  function getMCPManualConfigReason(server) {
    if (!server) return '';
    var envRequired = server.env_required && typeof server.env_required === 'object'
      ? Object.keys(server.env_required)
      : [];
    if (envRequired.length > 0) {
      return 'requires environment variables: ' + envRequired.join(', ');
    }

    var args = Array.isArray(server.args) ? server.args : [];
    for (var i = 0; i < args.length; i++) {
      var arg = String(args[i] || '');
      if (arg.indexOf('/path/to/allowed/directory') >= 0) {
        return 'needs a filesystem path before it can run';
      }
    }
    return '';
  }

  function buildMCPServerInstallPayload(server) {
    var args = Array.isArray(server && server.args) ? server.args.slice() : [];
    return {
      name: String(server && server.name || '').trim(),
      command: String(server && server.command || '').trim(),
      args: args,
      env: {},
      transport: String(server && server.transport || 'stdio').trim() || 'stdio',
      enabled: true
    };
  }

  async function fetchAgentMCPServers(agentName) {
    if (!agentName || typeof API === 'undefined' || typeof API.get !== 'function') return [];
    try {
      var data = await API.get('/api/agents/' + encodeURIComponent(agentName) + '/mcp-servers');
      return Array.isArray(data && data.servers) ? data.servers : [];
    } catch (error) {
      dashLog.debug('Failed to fetch agent MCP servers', { agent: agentName, error: error && error.message || error });
      return [];
    }
  }

  async function fetchMarketplaceMCPServers() {
    if (typeof API === 'undefined' || typeof API.get !== 'function') return [];
    try {
      var data = await API.get('/api/mcp/marketplace');
      return Array.isArray(data && data.servers) ? data.servers : [];
    } catch (error) {
      dashLog.debug('Failed to fetch MCP marketplace', { error: error && error.message || error });
      return [];
    }
  }

  async function ensureMCPForTask(agentName, prompt) {
    var requirement = detectMCPRequirement(prompt);
    if (!requirement || !agentName) return null;

    var currentServers = await fetchAgentMCPServers(agentName);
    var existing = selectExistingMCPServer(requirement, currentServers);

    if (existing && existing.enabled) {
      var currentStatus = normalizeToken(existing.status);
      if (currentStatus && currentStatus !== 'running') {
        return {
          status: 'enabled_not_running',
          serverName: existing.name,
          message: 'MCP server "' + existing.name + '" is enabled for this task, but currently "' + (existing.status || 'unknown') + '".'
        };
      }
      return {
        status: 'already_enabled',
        serverName: existing.name
      };
    }

    var targetServerName = existing && existing.name ? existing.name : '';
    var installCandidate = null;

    if (!targetServerName) {
      var marketplaceServers = await fetchMarketplaceMCPServers();
      installCandidate = chooseMarketplaceMCPServer(requirement, prompt, marketplaceServers);
      if (!installCandidate) {
        return {
          status: 'not_found',
          message: 'This task may need MCP (' + requirement.label + '), but I could not find a suitable MCP server to auto-install.'
        };
      }
      targetServerName = installCandidate.name;

      var manualReason = getMCPManualConfigReason(installCandidate);
      if (manualReason) {
        return {
          status: 'needs_manual_config',
          serverName: targetServerName,
          message: 'Found MCP server "' + targetServerName + '" for ' + requirement.label + ', but it ' + manualReason + '. Configure it in MCP settings first.'
        };
      }

      var payload = buildMCPServerInstallPayload(installCandidate);
      if (!payload.name || !payload.command) {
        return {
          status: 'invalid_candidate',
          message: 'Found an MCP candidate for ' + requirement.label + ', but its install configuration is incomplete.'
        };
      }

      try {
        await API.post('/api/mcp/servers', payload);
      } catch (error) {
        var installError = String(error && error.message || error || '');
        if (installError.toLowerCase().indexOf('already exists') < 0) {
          return {
            status: 'install_failed',
            serverName: payload.name,
            message: 'I found MCP server "' + payload.name + '" for ' + requirement.label + ' but failed to install it: ' + installError
          };
        }
      }
    }

    try {
      await API.post('/api/agents/' + encodeURIComponent(agentName) + '/mcp-servers/' + encodeURIComponent(targetServerName) + '/enable', {});
    } catch (error) {
      var enableError = String(error && error.message || error || '');
      return {
        status: 'enable_failed',
        serverName: targetServerName,
        message: 'I found MCP server "' + targetServerName + '" for ' + requirement.label + ' but failed to enable it: ' + enableError
      };
    }

    var refreshedServers = await fetchAgentMCPServers(agentName);
    var refreshed = selectExistingMCPServer(requirement, refreshedServers);
    var refreshedStatus = normalizeToken(refreshed && refreshed.status);
    if (refreshedStatus && refreshedStatus !== 'running') {
      return {
        status: 'enabled_not_running',
        serverName: targetServerName,
        message: 'Enabled MCP server "' + targetServerName + '" for this task, but it is currently "' + (refreshed && refreshed.status || 'unknown') + '".'
      };
    }

    if (installCandidate) {
      return {
        status: 'installed_and_enabled',
        serverName: targetServerName,
        message: 'Installed and enabled MCP server "' + targetServerName + '" for this task.'
      };
    }

    return {
      status: 'enabled_existing',
      serverName: targetServerName,
      message: 'Enabled MCP server "' + targetServerName + '" for this task.'
    };
  }

  function normalizePluginNames(plugins) {
    if (!Array.isArray(plugins)) return [];
    return plugins.map(function (name) {
      return normalizeToken(name).replace(/_/g, '-');
    });
  }

  function getAgentSummaryText(agent) {
    var parts = [];
    parts.push(normalizeToken(agent && agent.name));
    parts.push(normalizeToken(agent && agent.type));
    parts.push(normalizeToken(agent && agent.role));

    var metadata = agent && agent.metadata;
    if (metadata && typeof metadata.description === 'string') {
      parts.push(normalizeToken(metadata.description));
    }
    if (metadata && Array.isArray(metadata.tags)) {
      parts.push(normalizeToken(metadata.tags.join(' ')));
    }
    var plugins = normalizePluginNames(agent && agent.enabled_plugins);
    if (plugins.length > 0) {
      parts.push(plugins.join(' '));
    }
    var mcpServers = normalizePluginNames(agent && agent.mcp_servers);
    if (mcpServers.length > 0) {
      parts.push(mcpServers.join(' '));
    }

    return parts.join(' ').trim();
  }

  function scoreAgentForIntent(agent, intent, prompt) {
    var score = 0;
    var reasons = [];
    var summary = getAgentSummaryText(agent);
    var plugins = normalizePluginNames(agent && agent.enabled_plugins);

    for (var i = 0; i < intent.keywords.length; i++) {
      var keyword = intent.keywords[i];
      if (!keyword) continue;
      if (summary.indexOf(keyword) >= 0) {
        score += 2;
        if (reasons.length < 3) reasons.push('matches "' + keyword + '"');
      }
    }

    for (var p = 0; p < intent.preferredPlugins.length; p++) {
      var preferred = intent.preferredPlugins[p];
      for (var j = 0; j < plugins.length; j++) {
        if (plugins[j].indexOf(preferred) >= 0) {
          score += 3;
          if (reasons.length < 3) reasons.push('has plugin support for ' + preferred);
          break;
        }
      }
    }

    if (intent.preferredTypes.indexOf(normalizeToken(agent && agent.type)) >= 0) {
      score += 1;
    }
    if (normalizeToken(agent && agent.status) === 'active') {
      score += 1;
    }
    if (agent && agent.metadata && agent.metadata.favorite) {
      score += 1;
    }

    var promptTokens = uniqueValues(normalizeToken(prompt).split(/[^a-z0-9]+/g));
    var name = normalizeToken(agent && agent.name);
    for (var t = 0; t < promptTokens.length; t++) {
      var token = promptTokens[t];
      if (!isSignalPromptToken(token)) continue;
      if (name.indexOf(token) >= 0) {
        score += 1;
        if (reasons.length < 3) reasons.push('name overlaps "' + token + '"');
      }
    }

    if (intent && intent.key === 'general_task') {
      for (var s = 0; s < promptTokens.length; s++) {
        var summaryToken = promptTokens[s];
        if (!isSignalPromptToken(summaryToken)) continue;
        if (summary.indexOf(summaryToken) >= 0) {
          score += 2;
          if (reasons.length < 3) reasons.push('context overlaps "' + summaryToken + '"');
        }
      }
    }

    return {
      agent: agent,
      score: score,
      reasons: uniqueValues(reasons)
    };
  }

  function findSuitableAgent(agents, intent, prompt) {
    if (!Array.isArray(agents) || agents.length === 0) return null;
    var scored = [];
    for (var i = 0; i < agents.length; i++) {
      scored.push(scoreAgentForIntent(agents[i], intent, prompt));
    }
    scored.sort(function (a, b) { return b.score - a.score; });
    var best = scored[0];
    if (!best) return null;

    var minimumScore = intent.key === 'general_task' ? 3 : 4;
    if (best.score < minimumScore) {
      return null;
    }
    if (intent.key === 'general_task' && (!best.reasons || best.reasons.length === 0)) {
      return null;
    }
    return best;
  }

  function buildAutoConfigDescription(prompt, intent) {
    var base = '';
    if (intent.key === 'travel_planning') {
      base = 'Create an agent that plans multi-day travel itineraries with day-by-day plans, transportation ideas, budget ranges, and local recommendations.';
    } else if (intent.key === 'email_check') {
      base = 'Create an email triage agent that summarizes unread mail, categorizes urgency, and drafts replies. It must default to read-only behavior and never send without explicit user confirmation.';
    } else if (intent.key === 'app_launch') {
      base = 'Create a desktop launcher agent that can interpret app-launch requests, execute safe local launch commands, and confirm completion clearly.';
    } else {
      base = 'Create a practical task execution assistant that can route and complete user requests from the home dashboard.';
    }
    return base + ' User task: "' + String(prompt || '').trim() + '".';
  }

  function buildDefaultSystemPrompt(intent) {
    if (intent.key === 'travel_planning') {
      return 'You are a travel planning assistant. Build realistic day-by-day itineraries with concise options, practical transit notes, and budget-aware recommendations.';
    }
    if (intent.key === 'email_check') {
      return 'You are an email assistant. Summarize inbox content and draft responses. Never send or delete email without explicit user approval. Start in read-only mode.';
    }
    if (intent.key === 'app_launch') {
      return 'You are a desktop app launcher assistant. For requests like "open obsidian", launch the requested app immediately and confirm success or report the exact failure reason.';
    }
    return 'You are a helpful assistant focused on completing practical user tasks with clear, concise outputs.';
  }

  function buildUniqueAgentName(baseName, existingNames) {
    var sanitized = String(baseName || 'Task Assistant').replace(/[^a-zA-Z0-9 _-]/g, '').trim();
    if (!sanitized) sanitized = 'Task Assistant';
    var lowerNames = Object.create(null);
    for (var i = 0; i < existingNames.length; i++) {
      lowerNames[normalizeToken(existingNames[i])] = true;
    }
    if (!lowerNames[normalizeToken(sanitized)]) return sanitized;
    for (var suffix = 2; suffix <= 99; suffix++) {
      var candidate = sanitized + ' ' + suffix;
      if (!lowerNames[normalizeToken(candidate)]) return candidate;
    }
    return sanitized + ' ' + Date.now();
  }

  async function fetchAgentsForMatching() {
    if (typeof API === 'undefined' || typeof API.get !== 'function') return [];
    var data = await API.get('/api/agents/dashboard/list');
    return (data && data.agents) || [];
  }

  async function routePromptWithBackend(prompt) {
    if (typeof API === 'undefined' || typeof API.post !== 'function') return null;
    try {
      var data = await API.post('/api/home-assistant/route', { prompt: prompt });
      if (!data || typeof data.intent !== 'string') return null;
      return data;
    } catch (error) {
      dashLog.debug('Backend routing unavailable, falling back to local matching', { error: error && error.message || error });
      return null;
    }
  }

  function shouldAcceptBackendRouteMatch(routeData) {
    if (!routeData || routeData.requires_creation === true) return false;
    var matchedName = typeof routeData.matched_agent === 'string' ? routeData.matched_agent.trim() : '';
    if (!matchedName) return false;

    var score = Number(routeData.score || 0);
    var reasons = Array.isArray(routeData.reasons) ? routeData.reasons : [];
    var intentKey = normalizeToken(routeData.intent);

    if (intentKey === 'general_task') {
      return score >= 3 && reasons.length > 0;
    }
    return score >= 4;
  }

  async function fetchProvidersCatalog() {
    if (providersCache) return providersCache;
    if (typeof API === 'undefined' || typeof API.get !== 'function') return [];
    try {
      var data = await API.get('/api/providers');
      providersCache = (data && Array.isArray(data.providers)) ? data.providers : [];
      return providersCache;
    } catch (error) {
      dashLog.debug('Failed to load providers catalog', { error: error && error.message || error });
      return [];
    }
  }

  function buildModelRowsFromProviders(providers) {
    var rows = [];
    for (var i = 0; i < providers.length; i++) {
      var provider = providers[i] || {};
      var providerLabel = provider.display_name || provider.provider || 'Provider';
      var models = Array.isArray(provider.models) ? provider.models : [];
      for (var j = 0; j < models.length; j++) {
        var model = models[j] || {};
        if (!model.value) continue;
        rows.push({
          provider: providerLabel,
          type: normalizeToken(model.type),
          value: String(model.value),
          label: model.label || model.value
        });
      }
    }
    return rows;
  }

  function findModelValueForType(modelRows, agentType, preferredValue) {
    if (!Array.isArray(modelRows) || modelRows.length === 0) return '';

    var preferred = String(preferredValue || '').trim();
    if (preferred) {
      for (var i = 0; i < modelRows.length; i++) {
        if (modelRows[i].value === preferred) return preferred;
      }
    }

    var normalizedType = normalizeToken(agentType);
    for (var m = 0; m < modelRows.length; m++) {
      if (modelRows[m].type === normalizedType) return modelRows[m].value;
    }

    if (normalizedType !== 'general') {
      for (var g = 0; g < modelRows.length; g++) {
        if (modelRows[g].type === 'general') return modelRows[g].value;
      }
    }

    if (normalizedType !== 'tool-calling') {
      for (var t = 0; t < modelRows.length; t++) {
        if (modelRows[t].type === 'tool-calling') return modelRows[t].value;
      }
    }

    return modelRows[0].value;
  }

  async function resolveAutoSelectedModel(agentType, preferredValue) {
    var providers = await fetchProvidersCatalog();
    var rows = buildModelRowsFromProviders(providers);
    return findModelValueForType(rows, agentType, preferredValue);
  }

  function populateModalModelSelect(modelSelect, providers, agentType, preferredModel) {
    if (!modelSelect || !Array.isArray(providers)) return '';
    modelSelect.innerHTML = '';

    var normalizedType = normalizeToken(agentType);
    function appendOptionsByType(onlyMatchingType) {
      var count = 0;
      for (var i = 0; i < providers.length; i++) {
        var provider = providers[i] || {};
        var models = Array.isArray(provider.models) ? provider.models : [];
        if (models.length === 0) continue;

        var group = document.createElement('optgroup');
        group.label = provider.display_name || provider.provider || 'Provider';
        var groupVisible = 0;

        for (var j = 0; j < models.length; j++) {
          var model = models[j] || {};
          if (!model.value) continue;
          if (onlyMatchingType && normalizedType && normalizeToken(model.type) !== normalizedType) continue;

          var option = document.createElement('option');
          option.value = String(model.value);
          option.textContent = model.label || model.value;
          option.setAttribute('data-type', model.type || '');
          option.setAttribute('data-provider', model.provider || '');
          group.appendChild(option);
          groupVisible += 1;
        }

        if (groupVisible > 0) {
          count += groupVisible;
          modelSelect.appendChild(group);
        }
      }
      return count;
    }

    var totalVisible = appendOptionsByType(true);
    if (totalVisible === 0) {
      totalVisible = appendOptionsByType(false);
    }
    if (totalVisible === 0) {
      var emptyOption = document.createElement('option');
      emptyOption.value = '';
      emptyOption.textContent = 'No models available';
      modelSelect.appendChild(emptyOption);
      return '';
    }

    var selected = findModelValueForType(buildModelRowsFromProviders(providers), agentType, preferredModel);
    if (selected) modelSelect.value = selected;
    if (!modelSelect.value && modelSelect.options.length > 0) {
      modelSelect.selectedIndex = 0;
    }
    return modelSelect.value || '';
  }

  async function confirmAgentCreationWithModal(seedPayload) {
    var modalElement = document.getElementById('addAgentModal');
    var createButton = document.getElementById('createAgentBtn');
    var form = document.getElementById('addAgentForm');
    if (!modalElement || !createButton || !form || typeof bootstrap === 'undefined' || !bootstrap.Modal) {
      return null;
    }

    var nameInput = document.getElementById('agentName');
    var typeInput = document.getElementById('agentType');
    var modelInput = document.getElementById('agentModel');
    var tempInput = document.getElementById('agentTemperature');
    var tempValue = document.getElementById('temperatureValue');
    var promptInput = document.getElementById('agentSystemPrompt');
    if (!nameInput || !typeInput || !modelInput || !tempInput || !promptInput) {
      return null;
    }

    if (typeof resetBaseAutoConfigState === 'function') {
      resetBaseAutoConfigState();
    }

    nameInput.value = seedPayload.name || '';
    typeInput.value = seedPayload.type || 'tool-calling';
    tempInput.value = typeof seedPayload.temperature === 'number'
      ? String(seedPayload.temperature)
      : String(tempInput.value || '1.0');
    if (tempValue) tempValue.textContent = tempInput.value;
    promptInput.value = seedPayload.system_prompt || '';

    var providers = await fetchProvidersCatalog();
    if (providers.length > 0) {
      populateModalModelSelect(modelInput, providers, typeInput.value, seedPayload.model || '');
    } else if (seedPayload.model) {
      modelInput.value = seedPayload.model;
    }

    var modalInstance = bootstrap.Modal.getInstance(modalElement) || new bootstrap.Modal(modalElement);
    modalInstance.show();

    return await new Promise(function (resolve) {
      var settled = false;
      var submitting = false;
      var originalHtml = createButton.innerHTML;

      function finalize(agentName) {
        if (settled) return;
        settled = true;
        createButton.disabled = false;
        createButton.innerHTML = originalHtml;
        createButton.removeEventListener('click', onCreateCapture, true);
        form.removeEventListener('submit', onSubmitCapture, true);
        modalElement.removeEventListener('hidden.bs.modal', onHidden, true);
        resolve(agentName || null);
      }

      function onHidden() {
        finalize(null);
      }

      async function submitCreate() {
        if (submitting) return;
        var name = String(nameInput.value || '').trim();
        if (!name) {
          if (window.Toast) Toast.warning('Please enter an agent name');
          nameInput.focus();
          return;
        }

        var model = String(modelInput.value || '').trim();
        if (!model) {
          if (window.Toast) Toast.warning('Please select a model before creating the agent');
          modelInput.focus();
          return;
        }

        submitting = true;
        createButton.disabled = true;
        createButton.innerHTML = '<span class="spinner-border spinner-border-sm me-2" role="status"></span>Creating...';

        try {
          var requestBody = {
            name: name,
            type: typeInput.value || seedPayload.type || 'tool-calling',
            model: model,
            system_prompt: promptInput.value || seedPayload.system_prompt || '',
            description: seedPayload.description || '',
            tags: Array.isArray(seedPayload.tags) ? seedPayload.tags : []
          };
          var parsedTemp = parseFloat(tempInput.value);
          if (!Number.isNaN(parsedTemp)) requestBody.temperature = parsedTemp;

          await API.post('/api/agents', requestBody);
          modalInstance.hide();
          finalize(name);
        } catch (error) {
          dashLog.debug('Modal-confirmed agent creation failed', { error: error && error.message || error });
          if (window.Toast) Toast.error('Failed to create agent: ' + (error && error.message ? error.message : error));
        } finally {
          submitting = false;
          if (!settled) {
            createButton.disabled = false;
            createButton.innerHTML = originalHtml;
          }
        }
      }

      function onCreateCapture(event) {
        event.preventDefault();
        event.stopPropagation();
        event.stopImmediatePropagation();
        submitCreate();
      }

      function onSubmitCapture(event) {
        event.preventDefault();
        event.stopPropagation();
        event.stopImmediatePropagation();
        submitCreate();
      }

      createButton.addEventListener('click', onCreateCapture, true);
      form.addEventListener('submit', onSubmitCapture, true);
      modalElement.addEventListener('hidden.bs.modal', onHidden, true);
    });
  }

  async function maybeLoadAutoConfig(description) {
    if (typeof API === 'undefined' || typeof API.get !== 'function' || typeof API.post !== 'function') return null;
    try {
      var availability = await API.get('/api/agents/auto-config/availability');
      if (!availability || !availability.available) return null;
      return await API.post('/api/agents/auto-config', { description: description });
    } catch (err) {
      dashLog.debug('Auto-config unavailable', { error: err && err.message || err });
      return null;
    }
  }

  async function createAgentForPendingTask() {
    if (!homeAssistantState.pendingPrompt) return;
    var prompt = homeAssistantState.pendingPrompt;
    var intent = homeAssistantState.pendingIntent || HOME_INTENTS.general_task;
    var appLaunchRequest = homeAssistantState.pendingAppLaunch;
    homeAssistantState.awaitingCreateConfirmation = false;

    setHomeAssistantBusy(true, 'Creating...');
    renderHomeAssistantActions([]);
    appendHomeAssistantMessage('assistant', 'Creating a new agent for this task...');
    setHomeAssistantRoutingSummary('Agent Creation', 'Creating a new agent for this task...');

    try {
      var listData = await API.get('/api/agents');
      var existing = (listData && listData.agents) || [];
      var existingNames = [];
      for (var i = 0; i < existing.length; i++) {
        var agentInfo = existing[i];
        existingNames.push(typeof agentInfo === 'string' ? agentInfo : agentInfo.name);
      }

      var description = buildAutoConfigDescription(prompt, intent);
      var autoConfig = await maybeLoadAutoConfig(description);
      var desiredBaseName = autoConfig && autoConfig.agent_name
        ? autoConfig.agent_name
        : (homeAssistantState.pendingSuggestedName || intent.suggestedName);
      var agentName = buildUniqueAgentName(desiredBaseName, existingNames);
      var fallbackType = homeAssistantState.pendingSuggestedType || intent.defaultType;

      var payload = {
        name: agentName,
        type: autoConfig && autoConfig.agent_type ? autoConfig.agent_type : fallbackType,
        system_prompt: autoConfig && autoConfig.system_prompt ? autoConfig.system_prompt : buildDefaultSystemPrompt(intent),
        description: description,
        tags: uniqueValues((intent.tags || []).concat(['auto-created', 'home-assistant']))
      };

      var selectedModel = await resolveAutoSelectedModel(payload.type, autoConfig && autoConfig.model);
      if (selectedModel) payload.model = selectedModel;
      if (autoConfig && typeof autoConfig.temperature === 'number') payload.temperature = autoConfig.temperature;

      if (!payload.model) {
        appendHomeAssistantMessage('assistant',
          'I could not auto-select a model. Please review and confirm in the Create Agent modal.');
        setHomeAssistantRoutingSummary('Agent Creation', 'Model selection needs your confirmation.');
        var confirmedAgentName = await confirmAgentCreationWithModal(payload);
        if (!confirmedAgentName) {
          appendHomeAssistantMessage('assistant', 'Agent creation canceled. Ask again when you want to continue.');
          setHomeAssistantRoutingSummary('Agent Creation', 'Canceled by user.');
          renderHomeAssistantActions([
            {
              label: 'Create Agent',
              variant: 'primary',
              onClick: function () { createAgentForPendingTask(); }
            },
            {
              label: 'Ask Another Task',
              variant: 'secondary',
              onClick: function () { focusHomeAssistantInput(); }
            }
          ]);
          return;
        }
        agentName = confirmedAgentName;
      } else {
        appendHomeAssistantMessage('assistant', 'Auto-selected model "' + payload.model + '" for "' + agentName + '".');
        await API.post('/api/agents', payload);
      }

      homeAssistantState.pendingAgentName = agentName;
      appendHomeAssistantMessage('assistant', 'Created "' + agentName + '".');
      setHomeAssistantRoutingSummary('Agent Ready', '"' + agentName + '" is ready. Handing off to chat.');

      if (intent.key === 'email_check') {
        appendHomeAssistantMessage('assistant',
          'Email idea: connect Gmail/Outlook via OAuth, start with read-only scopes, summarize unread first, and require explicit approval before sending replies.');
      }

      var createdAgentMCP = await ensureMCPForTask(agentName, prompt);
      if (createdAgentMCP && createdAgentMCP.message) {
        appendHomeAssistantMessage('assistant', createdAgentMCP.message);
      }

      await runPendingTaskWithAgent(prompt, agentName, { appLaunchRequest: appLaunchRequest });

      API.get('/api/agents/dashboard/list').then(function (agentData) {
        if (agentData) renderAgentList(agentData);
      }).catch(function () {});
    } catch (error) {
      dashLog.debug('Failed to create agent', { error: error && error.message || error });
      appendHomeAssistantMessage('assistant', 'I could not create an agent right now. Please check model/provider settings and try again.');
      setHomeAssistantRoutingSummary('Agent Creation Failed', 'Could not create an agent right now.');
      renderHomeAssistantActions([
        {
          label: 'Retry Create Agent',
          variant: 'primary',
          onClick: function () { createAgentForPendingTask(); }
        }
      ]);
    } finally {
      setHomeAssistantBusy(false);
    }
  }

  function openChatPanel() {
    if (window.chatPanel && typeof window.chatPanel.open === 'function') {
      window.chatPanel.open();
    }
  }

  function waitForDelay(ms) {
    return new Promise(function (resolve) {
      window.setTimeout(resolve, ms);
    });
  }

  function findSessionForAgent(agentName) {
    var manager = window.sessionManager;
    if (!manager || !Array.isArray(manager.sessions)) return null;
    var target = normalizeToken(agentName);
    for (var i = 0; i < manager.sessions.length; i++) {
      var session = manager.sessions[i];
      if (normalizeToken(session && session.agent_name) === target) {
        return session;
      }
    }
    return null;
  }

  async function openOrCreateChatSession(agentName) {
    var manager = window.sessionManager;
    if (!manager) return null;

    // Ask Ori should start a fresh session per task handoff.
    if (typeof manager.createSessionWithAgent === 'function') {
      var created = await manager.createSessionWithAgent(agentName);
      if (created && created.id) return created;
    }

    var existing = findSessionForAgent(agentName);
    if (existing && existing.id && typeof manager.switchToSession === 'function') {
      await manager.switchToSession(existing.id, true);
      return existing;
    }

    return null;
  }

  async function dispatchPromptToChatSession(prompt, agentName) {
    if (!prompt || !agentName) return null;
    var session = await openOrCreateChatSession(agentName);
    if (!session) return null;
    openChatPanel();
    if (typeof window.sendMessageToChat !== 'function') return null;
    await waitForDelay(120);
    window.sendMessageToChat(prompt);
    return session;
  }

  async function runPendingTaskWithAgent(prompt, agentName, options) {
    if (!prompt || !agentName) return;
    var appLaunchRequest = options && options.appLaunchRequest ? options.appLaunchRequest : null;
    var dispatchMessage = buildAskOriDispatchMessage(prompt, appLaunchRequest);

    setHomeAssistantBusy(true, 'Opening Chat...');
    renderHomeAssistantActions([]);
    appendHomeAssistantMessage('assistant', 'Opening a chat session with "' + agentName + '"...');
    setHomeAssistantRoutingSummary('Handoff', 'Routing task to "' + agentName + '"...');
    if (appLaunchRequest && appLaunchRequest.appName) {
      appendHomeAssistantMessage('assistant',
        'Routing steps: 1) Start a new session. 2) Execute /openapp ' + appLaunchRequest.appName + ' to launch the app.');
    }

    try {
      var session = await dispatchPromptToChatSession(dispatchMessage, agentName);
      if (!session) throw new Error('Failed to launch chat session');
      trackHomeAssistantSession(session, prompt, agentName);
      setHomeAssistantMode('continue_session');
      appendHomeAssistantMessage('assistant', 'Started session "' + (session.title || 'New Session') + '" with "' + agentName + '".');
      setHomeAssistantRoutingSummary('Session Started', 'Session "' + (session.title || 'New Session') + '" is ready in chat.');
      if (appLaunchRequest && appLaunchRequest.appName) {
        appendHomeAssistantMessage('assistant', 'Launch command queued for "' + appLaunchRequest.appName + '". Continue in chat.');
      } else {
        appendHomeAssistantMessage('assistant', 'Your task was queued in chat. Continue in chat.');
      }

      renderHomeAssistantActions([
        {
          label: 'Open Chat',
          variant: 'primary',
          onClick: function () { openChatPanel(); }
        },
        {
          label: 'Ask Another Task',
          variant: 'secondary',
          onClick: function () { focusHomeAssistantInput(); }
        }
      ]);
    } catch (error) {
      dashLog.debug('Task session launch failed', { error: error && error.message || error });
      appendHomeAssistantMessage('assistant', 'I could not open chat for this task. You can retry or open agent settings.');
      setHomeAssistantRoutingSummary('Handoff Failed', 'Could not open a chat session. Retry to continue.');
      renderHomeAssistantActions([
        {
          label: 'Retry',
          variant: 'primary',
          onClick: function () { runPendingTaskWithAgent(prompt, agentName, options); }
        },
        {
          label: 'Open Agent Settings',
          variant: 'secondary',
          onClick: function () { window.location.href = '/agents'; }
        }
      ]);
    } finally {
      setHomeAssistantBusy(false);
    }
  }

  async function handleHomeAssistantPrompt(prompt) {
    var text = String(prompt || '').trim();
    if (!text) return;
    setHomeAssistantMode('new_task');
    var appLaunchRequest = parseAppLaunchRequest(text);

    if (homeAssistantState.awaitingCreateConfirmation && isAffirmativeConfirmation(text)) {
      appendHomeAssistantMessage('user', text);
      setHomeAssistantRoutingSummary('Agent Creation', 'Confirmed. Creating a new agent...');
      await createAgentForPendingTask();
      return;
    }

    if (homeAssistantState.awaitingCreateConfirmation && isNegativeConfirmation(text)) {
      appendHomeAssistantMessage('user', text);
      homeAssistantState.awaitingCreateConfirmation = false;
      appendHomeAssistantMessage('assistant', 'No problem. Ask another task when you are ready.');
      setHomeAssistantRoutingSummary('Agent Creation', 'Canceled. Ready for another task.');
      renderHomeAssistantActions([
        {
          label: 'Ask Another Task',
          variant: 'primary',
          onClick: function () { focusHomeAssistantInput(); }
        }
      ]);
      return;
    }

    homeAssistantState.pendingPrompt = text;
    homeAssistantState.pendingIntent = detectHomeIntent(text);
    homeAssistantState.pendingAgentName = '';
    homeAssistantState.pendingSuggestedName = '';
    homeAssistantState.pendingSuggestedType = '';
    homeAssistantState.pendingAppLaunch = appLaunchRequest;
    homeAssistantState.awaitingCreateConfirmation = false;

    appendHomeAssistantMessage('user', text);
    setHomeAssistantBusy(true, 'Routing...');
    renderHomeAssistantActions([]);
    setHomeAssistantRoutingSummary('Routing', 'Analyzing task and selecting the best agent...');

    try {
      var routeData = await routePromptWithBackend(text);
      var match = null;
      var useFallbackRouting = !routeData;

      if (routeData) {
        homeAssistantState.pendingIntent = HOME_INTENTS[routeData.intent] || detectHomeIntent(text);
        if (appLaunchRequest && homeAssistantState.pendingIntent.key === 'general_task') {
          homeAssistantState.pendingIntent = HOME_INTENTS.app_launch;
        }
        if (typeof routeData.suggested_agent_name === 'string') {
          homeAssistantState.pendingSuggestedName = routeData.suggested_agent_name.trim();
        }
        if (typeof routeData.suggested_agent_type === 'string') {
          homeAssistantState.pendingSuggestedType = routeData.suggested_agent_type.trim();
        }
        if (appLaunchRequest) {
          if (!homeAssistantState.pendingSuggestedName) {
            homeAssistantState.pendingSuggestedName = HOME_INTENTS.app_launch.suggestedName;
          }
          if (!homeAssistantState.pendingSuggestedType) {
            homeAssistantState.pendingSuggestedType = HOME_INTENTS.app_launch.defaultType;
          }
        }

        if (shouldAcceptBackendRouteMatch(routeData)) {
          match = {
            agent: { name: routeData.matched_agent.trim() },
            reasons: Array.isArray(routeData.reasons) ? routeData.reasons : []
          };
          useFallbackRouting = false;
        } else if (routeData.requires_creation === true) {
          useFallbackRouting = false;
        }
      }

      if (useFallbackRouting) {
        var agents = await fetchAgentsForMatching();
        match = findSuitableAgent(agents, homeAssistantState.pendingIntent, text);
      }

      if (match && match.agent) {
        homeAssistantState.pendingAgentName = match.agent.name;
        homeAssistantState.awaitingCreateConfirmation = false;
        var summary = 'I found "' + match.agent.name + '" for ' + homeAssistantState.pendingIntent.label + '.';
        if (match.reasons.length > 0) {
          summary += ' Reason: ' + match.reasons.join(', ') + '.';
        }
        appendHomeAssistantMessage('assistant', summary);
        setHomeAssistantRoutingSummary('Match Found', '"' + match.agent.name + '" is best for this task.');

        if (homeAssistantState.pendingIntent.key === 'email_check') {
          appendHomeAssistantMessage('assistant',
            'Idea for email handling: add OAuth (Gmail/Outlook), start read-only, summarize unread, and require explicit confirmation before any send action.');
        }

        var matchedAgentMCP = await ensureMCPForTask(match.agent.name, text);
        if (matchedAgentMCP && matchedAgentMCP.message) {
          appendHomeAssistantMessage('assistant', matchedAgentMCP.message);
        }
        await runPendingTaskWithAgent(text, match.agent.name, { appLaunchRequest: appLaunchRequest });
      } else {
        homeAssistantState.awaitingCreateConfirmation = true;
        appendHomeAssistantMessage('assistant',
          'No suitable agent found for this task. Would you like me to create one?');
        setHomeAssistantRoutingSummary('No Match', 'No suitable agent was found. Create a new agent to continue.');

        if (homeAssistantState.pendingIntent.key === 'email_check') {
          appendHomeAssistantMessage('assistant',
            'Email setup idea: connect mailbox via OAuth, request read-only scope first, then add a separate confirmed send step.');
        }

        renderHomeAssistantActions([
          {
            label: 'Create Agent',
            variant: 'primary',
            onClick: function () { createAgentForPendingTask(); }
          }
        ]);
      }
    } catch (error) {
      dashLog.debug('Task routing failed', { error: error && error.message || error });
      homeAssistantState.awaitingCreateConfirmation = false;
      appendHomeAssistantMessage('assistant', 'I could not evaluate agent suitability right now. Please retry.');
      setHomeAssistantRoutingSummary('Routing Failed', 'Could not evaluate agent suitability right now.');
    } finally {
      setHomeAssistantBusy(false);
    }
  }

  function handleHomeAssistantSubmit(event) {
    if (event) event.preventDefault();
    if (homeAssistantState.busy) return;
    var els = getHomeAssistantElements();
    if (!els.input) return;
    var prompt = els.input.value.trim();
    if (!prompt) return;
    els.input.value = '';
    handleHomeAssistantPrompt(prompt);
  }

  function initHomeAssistant() {
    var els = getHomeAssistantElements();
    if (!els.form || !els.input) return;
    homeAssistantState.recentSessions = loadHomeAssistantRecentSessions();
    setHomeAssistantRoutingSummary('', '');
    renderHomeAssistantRecentSessions();
    hydrateHomeAssistantRecentSessions();
    setHomeAssistantMode(homeAssistantState.mode);

    if (window.EventBus && typeof EventBus.on === 'function') {
      EventBus.on('session:deleted', function (payload) {
        if (!payload || !payload.sessionId) return;
        removeTrackedSession(payload.sessionId);
      });
    }

    els.form.addEventListener('submit', handleHomeAssistantSubmit);
    if (els.modeNewBtn) {
      els.modeNewBtn.addEventListener('click', function () {
        if (homeAssistantState.busy) return;
        setHomeAssistantMode('new_task');
      });
    }
    if (els.modeContinueBtn) {
      els.modeContinueBtn.addEventListener('click', function () {
        if (homeAssistantState.busy) return;
        setHomeAssistantMode('continue_session');
      });
    }
    if (els.viewAllBtn) {
      els.viewAllBtn.addEventListener('click', function () {
        if (homeAssistantState.busy) return;
        openChatPanel();
      });
    }
    if (els.clearRecentBtn) {
      els.clearRecentBtn.addEventListener('click', function () {
        if (homeAssistantState.busy) return;
        clearHomeAssistantRecentSessionList();
      });
    }

    for (var i = 0; i < els.quickButtons.length; i++) {
      els.quickButtons[i].addEventListener('click', function (event) {
        if (homeAssistantState.busy) return;
        var prompt = event.currentTarget && event.currentTarget.getAttribute('data-home-prompt');
        if (!prompt) return;
        els.input.value = prompt;
        handleHomeAssistantSubmit();
      });
    }

    if (els.avatarBtn) {
      els.avatarBtn.addEventListener('click', function () {
        focusHomeAssistantInput();
      });
    }
    if (els.bubbleBtn) {
      els.bubbleBtn.addEventListener('click', function () {
        focusHomeAssistantInput();
      });
    }
  }

  function renderAssistantProgress(data) {
    var card = document.getElementById('dashboardProgressCard');
    if (!card) return;

    var assistant = data && data.assistant;
    if (!assistant) {
      card.classList.add('d-none');
      return;
    }

    var experience = Math.max(0, Number(assistant.experience) || 0);
    var level = Math.max(0, Number(assistant.level) || 0);
    var rank = (typeof assistant.rank === 'string' && assistant.rank.trim()) ? assistant.rank.trim() : 'novice';

    var progressWithinLevel = experience % XP_PER_LEVEL;
    var progressPercent = Math.min(100, Math.max(0, Math.round((progressWithinLevel / XP_PER_LEVEL) * 100)));

    var rankBadge = document.getElementById('dashboardRankBadge');
    var levelText = document.getElementById('dashboardLevelText');
    var xpText = document.getElementById('dashboardXpText');
    var xpBar = document.getElementById('dashboardXpBar');

    if (rankBadge) rankBadge.textContent = toTitleCase(rank);
    if (levelText) levelText.textContent = 'Level ' + level;
    if (xpText) xpText.textContent = formatNumber(experience) + ' XP';
    if (xpBar) {
      xpBar.style.width = progressPercent + '%';
      xpBar.setAttribute('aria-valuenow', String(progressPercent));
    }

    card.classList.remove('d-none');
  }

  function renderStats(data) {
    var stats = data || {};
    var elAgents = document.getElementById('dashboardStatAgents');
    var elActive = document.getElementById('dashboardStatActive');
    var elMessages = document.getElementById('dashboardStatMessages');
    var elCost = document.getElementById('dashboardStatCost');

    if (elAgents) elAgents.textContent = formatNumber(stats.total_agents);
    if (elActive) elActive.textContent = formatNumber(stats.active_agents);
    if (elMessages) elMessages.textContent = formatNumber(stats.total_messages);
    if (elCost) elCost.textContent = formatCost(stats.total_cost);
  }

  function renderAgentList(data) {
    var container = document.getElementById('dashboardAgentList');
    if (!container) return;

    var agents = (data && data.agents) || [];
    if (agents.length === 0) {
      container.innerHTML =
        '<div class="text-center py-4">' +
        '<p style="color: var(--text-muted); margin-bottom: 0.5rem;">No agents configured yet.</p>' +
        '<a href="/agents" class="modern-btn modern-btn-primary" style="font-size: 0.85rem;">Create Your First Agent</a>' +
        '</div>';
      return;
    }

    var evolutionEnabled = Boolean(window.oriFeatures && window.oriFeatures.evolutionEnabled);
    var html = '';
    for (var i = 0; i < agents.length; i++) {
      var agent = agents[i];
      var name = agent.name || 'Unknown';
      var model = agent.model || 'N/A';
      var msgCount = formatNumber(agent.message_count);
      var color = agent.avatar_color || '#4f46e5';
      var initial = name.charAt(0).toUpperCase();

      // Evolution info
      var evoHtml = '';
      if (evolutionEnabled && agent.evolution) {
        var stage = toTitleCase(agent.evolution.stage || '');
        var evoLevel = agent.evolution.level || 0;
        if (stage) {
          evoHtml =
            '<span class="badge ms-2" style="background: var(--bg-tertiary); color: var(--text-secondary); font-size: 0.7rem;">' +
            stage + ' Lv.' + evoLevel +
            '</span>';
        }
      }

      html +=
        '<div class="d-flex align-items-center justify-content-between py-2' + (i < agents.length - 1 ? ' border-bottom' : '') + '" style="border-color: var(--border-color) !important;">' +
        '  <div class="d-flex align-items-center gap-3">' +
        '    <div style="width: 36px; height: 36px; border-radius: 50%; background: ' + color + '; display: flex; align-items: center; justify-content: center; color: white; font-weight: 600; font-size: 0.85rem; flex-shrink: 0;">' + initial + '</div>' +
        '    <div>' +
        '      <div style="color: var(--text-primary); font-weight: 500;">' + name + evoHtml + '</div>' +
        '      <div style="color: var(--text-muted); font-size: 0.8rem;">' + model + '</div>' +
        '    </div>' +
        '  </div>' +
        '  <div style="color: var(--text-muted); font-size: 0.8rem;">' + msgCount + ' msgs</div>' +
        '</div>';
    }
    container.innerHTML = html;
  }

  function renderPersonalizeBanner(data) {
    var banner = document.getElementById('dashboardPersonalizeBanner');
    if (!banner) return;

    var profile = data && data.profile;
    // Show banner if no profile or personalized_at is zero/missing
    var isPersonalized = profile && profile.personalized_at && profile.personalized_at !== '0001-01-01T00:00:00Z';

    if (isPersonalized) {
      banner.classList.add('d-none');
    } else {
      banner.classList.remove('d-none');
    }
  }

  function initDashboard() {
    setGreeting();
    initHomeAssistant();

    if (typeof API === 'undefined' || typeof API.get !== 'function') {
      dashLog.debug('API not available, skipping dashboard data load');
      return;
    }

    var evolutionEnabled = Boolean(window.oriFeatures && window.oriFeatures.evolutionEnabled);

    var promises = [
      API.get('/api/agents/dashboard/stats').catch(function (err) {
        dashLog.debug('Failed to load dashboard stats', { error: err && err.message || err });
        return null;
      }),
      API.get('/api/agents/dashboard/list').catch(function (err) {
        dashLog.debug('Failed to load agent list', { error: err && err.message || err });
        return null;
      }),
      API.get('/api/onboarding/user-profile').catch(function (err) {
        dashLog.debug('Failed to load user profile', { error: err && err.message || err });
        return null;
      })
    ];

    if (evolutionEnabled) {
      promises.push(
        API.get('/api/evolution/assistant').catch(function (err) {
          dashLog.debug('Failed to load assistant progress', { error: err && err.message || err });
          return null;
        })
      );
    }

    Promise.allSettled(promises).then(function (results) {
      var statsData = results[0].status === 'fulfilled' ? results[0].value : null;
      var agentData = results[1].status === 'fulfilled' ? results[1].value : null;
      var profileData = results[2].status === 'fulfilled' ? results[2].value : null;

      if (statsData) renderStats(statsData);
      if (agentData) renderAgentList(agentData);
      renderPersonalizeBanner(profileData);

      if (evolutionEnabled && results.length > 3) {
        var evoData = results[3].status === 'fulfilled' ? results[3].value : null;
        if (evoData) renderAssistantProgress(evoData);
      }
    });
  }

  document.addEventListener('DOMContentLoaded', initDashboard);
})();
