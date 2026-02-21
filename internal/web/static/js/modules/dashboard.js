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
  const HOME_ASSISTANT_AUTOMATION_MODE_KEY = 'ori.homeAssistant.automationMode';

  const HOME_INTENTS = {
    utility_direct: {
      key: 'utility_direct',
      label: 'daily utility',
      keywords: ['time', 'timezone', 'clock', 'date', 'weather', 'forecast', 'temperature', 'convert', 'conversion', 'calculate', 'calculator', 'quick fact', 'fact', 'capital', 'define', 'definition'],
      preferredPlugins: ['time', 'weather', 'calculator', 'math', 'search', 'web'],
      preferredTypes: ['general', 'tool-calling', 'research'],
      defaultType: 'general',
      suggestedName: 'Utility Assistant',
      tags: ['utility', 'time', 'weather', 'facts']
    },
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
      key: 'email_inbox',
      label: 'email inbox access',
      phrases: ['check my email', 'check email', 'email', 'inbox', 'mailbox', 'gmail', 'outlook', 'unread', 'reply to email', 'triage'],
      preferredServerNames: ['gmail', 'outlook', 'imap', 'microsoft-graph'],
      preferredCategories: ['communication', 'email', 'productivity']
    },
    {
      key: 'browser_automation',
      label: 'browser automation',
      phrases: ['browser automation', 'control browser', 'use browser', 'website automation', 'automate website', 'playwright', 'browserbase', 'puppeteer'],
      preferredServerNames: ['playwright', 'browserbase', 'puppeteer'],
      preferredCategories: ['automation', 'development', 'productivity']
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
    routingSummary: null,
    automationMode: 'semi_auto'
  };

  var homeAssistantThinkingModalInstance = null;
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

  function loadHomeAssistantAutomationMode() {
    try {
      var storage = getPersistentStorage();
      if (!storage) return 'semi_auto';
      var raw = String(storage.getItem(HOME_ASSISTANT_AUTOMATION_MODE_KEY) || '').trim();
      if (raw === 'full_auto') return 'full_auto';
      if (raw === 'semi_auto') return 'semi_auto';
      return 'semi_auto';
    } catch (error) {
      dashLog.debug('Failed to load Ask Ori automation mode', { error: error && error.message || error });
      return 'semi_auto';
    }
  }

  function saveHomeAssistantAutomationMode() {
    try {
      var storage = getPersistentStorage();
      if (!storage) return;
      storage.setItem(HOME_ASSISTANT_AUTOMATION_MODE_KEY, homeAssistantState.automationMode || 'semi_auto');
      if (window.sessionStorage && storage !== window.sessionStorage) {
        window.sessionStorage.setItem(HOME_ASSISTANT_AUTOMATION_MODE_KEY, homeAssistantState.automationMode || 'semi_auto');
      }
    } catch (error) {
      dashLog.debug('Failed to persist Ask Ori automation mode', { error: error && error.message || error });
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

  function buildEmailDispatchMessage(prompt) {
    var userPrompt = String(prompt || '').trim();
    return [
      'Email task:',
      userPrompt,
      '',
      'Execution requirements:',
      '- Use configured MCP connectors/tools first (email connector or browser-control connector).',
      '- If authentication is required, guide the user through login and continue.',
      '- Do not claim lack of access before attempting available MCP tools.',
      '- Keep operations read-only unless the user explicitly approves send/delete actions.'
    ].join('\n');
  }

  function buildAskOriDispatchMessage(prompt, appLaunchRequest, intent) {
    if (intent && intent.key === 'email_check') {
      return buildEmailDispatchMessage(prompt);
    }
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
      thinkingModal: document.getElementById('homeAssistantThinkingModal'),
      thinkingStatus: document.getElementById('homeAssistantThinkingStatus'),
      thinkingSpinner: document.getElementById('homeAssistantThinkingSpinner'),
      quickPrompts: document.getElementById('homeAssistantQuickPrompts'),
      actions: document.getElementById('homeAssistantActions'),
      recentSection: document.getElementById('homeAssistantRecentSection'),
      recentSessions: document.getElementById('homeAssistantRecentSessions'),
      modeNewBtn: document.getElementById('homeAssistantModeNewBtn'),
      modeContinueBtn: document.getElementById('homeAssistantModeContinueBtn'),
      autoFullBtn: document.getElementById('homeAssistantAutoFullBtn'),
      autoSemiBtn: document.getElementById('homeAssistantAutoSemiBtn'),
      viewAllBtn: document.getElementById('homeAssistantViewAllBtn'),
      clearRecentBtn: document.getElementById('homeAssistantClearRecentBtn'),
      quickButtons: document.querySelectorAll('.home-assistant-quick-btn'),
      avatarBtn: document.getElementById('dashboardAssistantAvatarBtn'),
      bubbleBtn: document.getElementById('dashboardAssistantBubbleBtn')
    };
  }

  function isElementInsideModal(element) {
    if (!element || typeof element.closest !== 'function') return false;
    return Boolean(element.closest('.modal'));
  }

  function getHomeAssistantThinkingModalInstance() {
    var els = getHomeAssistantElements();
    if (!els.thinkingModal || typeof bootstrap === 'undefined' || !bootstrap.Modal) return null;
    if (homeAssistantThinkingModalInstance) return homeAssistantThinkingModalInstance;
    homeAssistantThinkingModalInstance = bootstrap.Modal.getOrCreateInstance(els.thinkingModal);
    return homeAssistantThinkingModalInstance;
  }

  function openHomeAssistantThinkingModal() {
    var modal = getHomeAssistantThinkingModalInstance();
    if (!modal) return;
    modal.show();
  }

  function syncHomeAssistantThinkingStatus() {
    var els = getHomeAssistantElements();
    if (!els.thinkingStatus) return;

    var statusText = '';
    if (homeAssistantState.routingSummary && homeAssistantState.routingSummary.text) {
      statusText = homeAssistantState.routingSummary.text;
    } else if (homeAssistantState.busy) {
      statusText = 'Working...';
    } else {
      statusText = 'Ready for your next task.';
    }

    var textNode = els.thinkingStatus.querySelector('span:last-child');
    if (textNode) textNode.textContent = statusText;
    if (els.thinkingSpinner) {
      els.thinkingSpinner.classList.toggle('d-none', !homeAssistantState.busy);
    }
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
    syncHomeAssistantThinkingStatus();
  }

  function setHomeAssistantRoutingSummary(title, text) {
    if (!title || !text) {
      homeAssistantState.routingSummary = null;
      renderHomeAssistantRoutingSummary();
      syncHomeAssistantThinkingStatus();
      return;
    }
    homeAssistantState.routingSummary = {
      title: String(title),
      text: String(text)
    };
    renderHomeAssistantRoutingSummary();
    if (homeAssistantState.busy) {
      openHomeAssistantThinkingModal();
    }
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
    if (els.conversation && !isElementInsideModal(els.conversation)) {
      els.conversation.classList.toggle('d-none', nextMode === 'continue_session');
    }
    if (els.input) {
      els.input.placeholder = 'Ask Ori to do something...';
    }

    renderHomeAssistantRecentSessions();
  }

  function isSemiAutoMode() {
    return homeAssistantState.automationMode === 'semi_auto';
  }

  function setHomeAssistantAutomationMode(mode) {
    var nextMode = mode === 'semi_auto' ? 'semi_auto' : 'full_auto';
    homeAssistantState.automationMode = nextMode;
    saveHomeAssistantAutomationMode();

    var els = getHomeAssistantElements();
    var hasAutomationControls = Boolean(els.autoFullBtn || els.autoSemiBtn);
    if (els.autoFullBtn) {
      var isFull = nextMode === 'full_auto';
      els.autoFullBtn.classList.toggle('is-active', isFull);
      els.autoFullBtn.setAttribute('aria-selected', isFull ? 'true' : 'false');
    }
    if (els.autoSemiBtn) {
      var isSemi = nextMode === 'semi_auto';
      els.autoSemiBtn.classList.toggle('is-active', isSemi);
      els.autoSemiBtn.setAttribute('aria-selected', isSemi ? 'true' : 'false');
    }

    if (!hasAutomationControls) return;

    if (nextMode === 'semi_auto') {
      setHomeAssistantRoutingSummary('Semi-auto', 'Step-by-step confirmations are enabled.');
    } else if (homeAssistantState.routingSummary &&
      homeAssistantState.routingSummary.title === 'Semi-auto' &&
      homeAssistantState.routingSummary.text === 'Step-by-step confirmations are enabled.') {
      setHomeAssistantRoutingSummary('', '');
    }
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
    if (els.autoFullBtn) els.autoFullBtn.disabled = homeAssistantState.busy;
    if (els.autoSemiBtn) els.autoSemiBtn.disabled = homeAssistantState.busy;
    if (els.viewAllBtn) els.viewAllBtn.disabled = homeAssistantState.busy;
    if (els.clearRecentBtn) els.clearRecentBtn.disabled = homeAssistantState.busy;
    if (homeAssistantState.busy) {
      openHomeAssistantThinkingModal();
    }
    syncHomeAssistantThinkingStatus();
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
    openHomeAssistantThinkingModal();
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
    openHomeAssistantThinkingModal();
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
    var keys = ['utility_direct', 'travel_planning', 'email_check'];
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

  function isLegacyMCPServerName(name) {
    var normalized = normalizeMCPServerName(name);
    return normalized === 'puppeteer';
  }

  function findPreferredServerIndex(preferredNames, serverName) {
    if (!Array.isArray(preferredNames) || !serverName) return -1;
    for (var i = 0; i < preferredNames.length; i++) {
      if (preferredNames[i] === serverName) return i;
    }
    return -1;
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

  function findMCPRequirementByKey(requirementKey) {
    if (!requirementKey) return null;
    for (var i = 0; i < HOME_MCP_REQUIREMENTS.length; i++) {
      var requirement = HOME_MCP_REQUIREMENTS[i];
      if (requirement && requirement.key === requirementKey) return requirement;
    }
    return null;
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

  function findMCPServerByName(servers, serverName) {
    if (!Array.isArray(servers) || !serverName) return null;
    var targetName = normalizeMCPServerName(serverName);
    for (var i = 0; i < servers.length; i++) {
      var current = servers[i];
      if (normalizeMCPServerName(current && current.name) === targetName) {
        return current;
      }
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

    var preferredNameIndex = findPreferredServerIndex(preferredNames, serverName);
    if (preferredNameIndex >= 0) {
      score += 100;
      score += (preferredNames.length - preferredNameIndex) * 8;
    }
    if (serverName && scoreMCPRequirement(serverName.replace(/-/g, ' '), requirement) > 0) score += 35;
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

  async function fetchConfiguredMCPServerSnapshot() {
    if (typeof API === 'undefined' || typeof API.get !== 'function') {
      return { servers: [], stats: {} };
    }
    try {
      var data = await API.get('/api/mcp/servers');
      return {
        servers: Array.isArray(data && data.servers) ? data.servers : [],
        stats: data && data.stats && typeof data.stats === 'object' ? data.stats : {}
      };
    } catch (error) {
      dashLog.debug('Failed to fetch configured MCP servers', { error: error && error.message || error });
      return { servers: [], stats: {} };
    }
  }

  async function fetchConfiguredMCPServers() {
    var snapshot = await fetchConfiguredMCPServerSnapshot();
    return snapshot.servers;
  }

  async function fetchMCPRegistryServers() {
    if (typeof API === 'undefined' || typeof API.get !== 'function') return [];
    try {
      var data = await API.get('/api/mcp/search');
      if (Array.isArray(data)) return data;
      if (Array.isArray(data && data.servers)) return data.servers;
      return [];
    } catch (error) {
      dashLog.debug('Failed to fetch MCP browse registry', { error: error && error.message || error });
      return [];
    }
  }

  function lookupMCPServerStats(statsMap, serverName) {
    if (!statsMap || typeof statsMap !== 'object' || !serverName) return null;
    if (statsMap[serverName]) return statsMap[serverName];
    var targetName = normalizeMCPServerName(serverName);
    var keys = Object.keys(statsMap);
    for (var i = 0; i < keys.length; i++) {
      var key = keys[i];
      if (normalizeMCPServerName(key) === targetName) {
        return statsMap[key];
      }
    }
    return null;
  }

  function scoreConfiguredMCPServer(requirement, server, promptText) {
    if (!requirement || !server) return 0;
    var score = 0;
    var serverName = normalizeMCPServerName(server.name);
    var category = normalizeToken(server.category);
    var description = normalizeToken(server.description);
    var preferredNames = (requirement.preferredServerNames || []).map(normalizeMCPServerName);
    var preferredCategories = (requirement.preferredCategories || []).map(normalizeToken);

    var preferredNameIndex = findPreferredServerIndex(preferredNames, serverName);
    if (preferredNameIndex >= 0) {
      score += 100;
      score += (preferredNames.length - preferredNameIndex) * 8;
    }
    if (serverName && scoreMCPRequirement(serverName.replace(/-/g, ' '), requirement) > 0) score += 35;
    if (preferredCategories.indexOf(category) >= 0) score += 30;
    if (description && scoreMCPRequirement(description, requirement) > 0) score += 20;
    if (promptText && serverName && promptText.indexOf(serverName) >= 0) score += 10;

    return score;
  }

  function scoreRegistryMCPServer(requirement, server, promptText) {
    if (!requirement || !server) return 0;
    var score = 0;
    var serverName = normalizeMCPServerName(server.name);
    var category = normalizeToken(server.category);
    var description = normalizeToken(server.description);
    var tags = Array.isArray(server.tags) ? normalizeToken(server.tags.join(' ')) : '';
    var preferredNames = (requirement.preferredServerNames || []).map(normalizeMCPServerName);
    var preferredCategories = (requirement.preferredCategories || []).map(normalizeToken);

    var preferredNameIndex = findPreferredServerIndex(preferredNames, serverName);
    if (preferredNameIndex >= 0) {
      score += 100;
      score += (preferredNames.length - preferredNameIndex) * 8;
    }
    if (serverName && scoreMCPRequirement(serverName.replace(/-/g, ' '), requirement) > 0) score += 35;
    if (preferredCategories.indexOf(category) >= 0) score += 30;
    if (description && scoreMCPRequirement(description, requirement) > 0) score += 20;
    if (tags && scoreMCPRequirement(tags, requirement) > 0) score += 15;
    if (promptText && serverName && promptText.indexOf(serverName) >= 0) score += 10;

    return score;
  }

  function chooseConfiguredMCPServers(requirement, prompt, configuredServers, limit) {
    if (!requirement || !Array.isArray(configuredServers) || configuredServers.length === 0) return [];
    var promptText = normalizeToken(prompt);
    var ranked = [];
    for (var i = 0; i < configuredServers.length; i++) {
      var server = configuredServers[i];
      var score = scoreConfiguredMCPServer(requirement, server, promptText);
      if (score <= 0) continue;
      ranked.push({ server: server, score: score });
    }
    if (ranked.length === 0) return [];

    ranked.sort(function (a, b) {
      return b.score - a.score;
    });

    var max = typeof limit === 'number' && limit > 0 ? Math.floor(limit) : 1;
    var selected = [];
    for (var j = 0; j < ranked.length && j < max; j++) {
      selected.push(ranked[j].server);
    }
    return selected;
  }

  function chooseRegistryMCPServers(requirement, prompt, registryServers, limit) {
    if (!requirement || !Array.isArray(registryServers) || registryServers.length === 0) return [];
    var promptText = normalizeToken(prompt);
    var ranked = [];
    for (var i = 0; i < registryServers.length; i++) {
      var server = registryServers[i];
      var score = scoreRegistryMCPServer(requirement, server, promptText);
      if (score <= 0) continue;
      ranked.push({ server: server, score: score });
    }
    if (ranked.length === 0) return [];

    ranked.sort(function (a, b) {
      return b.score - a.score;
    });

    var max = typeof limit === 'number' && limit > 0 ? Math.floor(limit) : 1;
    var selected = [];
    for (var j = 0; j < ranked.length && j < max; j++) {
      selected.push(ranked[j].server);
    }
    return selected;
  }

  function buildEmailMCPCandidateFromConfigured(server, stat) {
    var runtimeLabel = String(stat && stat.status || server && server.status || '').trim();
    var runtimeStatus = normalizeToken(runtimeLabel);
    var legacy = isLegacyMCPServerName(server && server.name);
    return {
      name: String(server && server.name || '').trim(),
      description: String(server && server.description || '').trim(),
      category: String(server && server.category || '').trim(),
      source: 'configured',
      isInstalled: true,
      selectable: true,
      enabled: Boolean(stat && stat.enabled || server && server.enabled),
      runtimeLabel: runtimeLabel,
      runtimeStatus: runtimeStatus,
      manualReason: '',
      legacy: legacy,
      legacyReason: legacy ? 'Puppeteer MCP is deprecated upstream. Playwright is recommended.' : '',
      rawServer: server
    };
  }

  function buildEmailMCPCandidateFromRegistry(server) {
    var manualReason = getMCPManualConfigReason(server);
    var legacy = isLegacyMCPServerName(server && server.name);
    return {
      name: String(server && server.name || '').trim(),
      description: String(server && server.description || '').trim(),
      category: String(server && server.category || '').trim(),
      source: String(server && server.source || 'registry').trim() || 'registry',
      isInstalled: false,
      selectable: !manualReason,
      enabled: false,
      runtimeLabel: '',
      runtimeStatus: '',
      manualReason: manualReason,
      legacy: legacy,
      legacyReason: legacy ? 'Puppeteer MCP is deprecated upstream. Playwright is recommended.' : '',
      rawServer: server
    };
  }

  function filterEmailMCPCandidates(candidates, query) {
    if (!Array.isArray(candidates) || candidates.length === 0) return [];
    var normalizedQuery = normalizeToken(query);
    if (!normalizedQuery) return candidates.slice();
    var filtered = [];
    for (var i = 0; i < candidates.length; i++) {
      var candidate = candidates[i];
      var haystack = [
        candidate.name,
        candidate.description,
        candidate.category,
        candidate.source
      ].join(' ');
      if (normalizeToken(haystack).indexOf(normalizedQuery) >= 0) {
        filtered.push(candidate);
      }
    }
    return filtered;
  }

  async function buildScopedMCPBrowseCandidates(requirement, prompt) {
    if (!requirement) return { requirement: null, candidates: [] };
    var snapshot = { servers: [], stats: {} };
    var registryServers = [];
    var marketplaceServers = [];
    try {
      var loaded = await Promise.all([
        fetchConfiguredMCPServerSnapshot(),
        fetchMCPRegistryServers(),
        fetchMarketplaceMCPServers()
      ]);
      snapshot = loaded[0] || snapshot;
      registryServers = Array.isArray(loaded[1]) ? loaded[1] : [];
      marketplaceServers = Array.isArray(loaded[2]) ? loaded[2] : [];
    } catch (error) {
      dashLog.debug('Failed to build MCP browse candidates', { error: error && error.message || error });
    }

    var configuredMatches = chooseConfiguredMCPServers(requirement, prompt, snapshot.servers, 6);
    var registryMatches = chooseRegistryMCPServers(requirement, prompt, registryServers, 12);
    if (registryMatches.length === 0) {
      var marketplaceCandidate = chooseMarketplaceMCPServer(requirement, prompt, marketplaceServers);
      if (marketplaceCandidate) {
        registryMatches = [marketplaceCandidate];
      }
    }

    var candidates = [];
    var seen = Object.create(null);

    for (var i = 0; i < configuredMatches.length; i++) {
      var configuredServer = configuredMatches[i];
      var configuredName = normalizeMCPServerName(configuredServer && configuredServer.name);
      if (!configuredName || seen[configuredName]) continue;
      seen[configuredName] = true;
      candidates.push(buildEmailMCPCandidateFromConfigured(
        configuredServer,
        lookupMCPServerStats(snapshot.stats, configuredServer.name)
      ));
    }

    for (var j = 0; j < registryMatches.length; j++) {
      var registryServer = registryMatches[j];
      var registryName = normalizeMCPServerName(registryServer && registryServer.name);
      if (!registryName || seen[registryName]) continue;
      seen[registryName] = true;
      candidates.push(buildEmailMCPCandidateFromRegistry(registryServer));
    }

    return { requirement: requirement, candidates: candidates };
  }

  function resolveEmailMCPRequirement(prompt) {
    var requirement = detectMCPRequirement(prompt);
    if (!requirement || requirement.key !== 'email_inbox') {
      requirement = findMCPRequirementByKey('email_inbox');
    }
    return requirement;
  }

  function resolveBrowserMCPRequirement() {
    return findMCPRequirementByKey('browser_automation') || {
      key: 'browser_automation',
      label: 'browser automation',
      phrases: ['browser automation', 'control browser', 'use browser', 'website automation', 'playwright', 'browserbase', 'puppeteer'],
      preferredServerNames: ['playwright', 'browserbase', 'puppeteer'],
      preferredCategories: ['automation', 'development', 'productivity']
    };
  }

  async function buildEmailMCPBrowseCandidates(prompt) {
    return buildScopedMCPBrowseCandidates(resolveEmailMCPRequirement(prompt), prompt);
  }

  async function buildBrowserMCPBrowseCandidates(prompt) {
    return buildScopedMCPBrowseCandidates(resolveBrowserMCPRequirement(), prompt);
  }

  async function installMCPServerCandidate(candidate) {
    if (!candidate || !candidate.name) {
      return { status: 'invalid_selection', message: 'Select an MCP server first.' };
    }
    if (candidate.isInstalled) {
      return { status: 'already_installed', serverName: candidate.name };
    }
    if (candidate.manualReason) {
      return {
        status: 'manual_config_required',
        serverName: candidate.name,
        message: 'The selected server "' + candidate.name + '" cannot be auto-installed because it ' + candidate.manualReason + '.'
      };
    }

    var payload = buildMCPServerInstallPayload(candidate.rawServer || candidate);
    if (!payload.name || !payload.command) {
      return {
        status: 'invalid_candidate',
        message: 'The selected MCP server has incomplete install details. Open MCP settings to configure it manually.'
      };
    }

    try {
      await API.post('/api/mcp/servers', payload);
      return {
        status: 'installed',
        serverName: payload.name
      };
    } catch (error) {
      var installError = String(error && error.message || error || '');
      if (installError.toLowerCase().indexOf('already exists') >= 0) {
        return {
          status: 'already_installed',
          serverName: payload.name
        };
      }
      return {
        status: 'install_failed',
        serverName: payload.name,
        message: 'Failed to install MCP server "' + payload.name + '": ' + installError
      };
    }
  }

  async function enableMCPServerForAgent(agentName, serverName) {
    if (!agentName || !serverName) {
      return { status: 'enable_failed', message: 'Agent name and MCP server are required.' };
    }
    try {
      await API.post('/api/agents/' + encodeURIComponent(agentName) + '/mcp-servers/' + encodeURIComponent(serverName) + '/enable', {});
    } catch (error) {
      var enableError = String(error && error.message || error || '');
      return {
        status: 'enable_failed',
        serverName: serverName,
        message: 'Failed to attach MCP server "' + serverName + '" to "' + agentName + '": ' + enableError
      };
    }

    var agentServers = await fetchAgentMCPServers(agentName);
    var attached = findMCPServerByName(agentServers, serverName);
    var runtimeStatus = normalizeToken(attached && attached.status);
    if (runtimeStatus && runtimeStatus !== 'running') {
      return {
        status: 'attached_not_running',
        serverName: serverName,
        runtimeStatus: String(attached && attached.status || 'unknown'),
        message: 'Attached MCP server "' + serverName + '" to "' + agentName + '", but it is currently "' + (attached && attached.status || 'unknown') + '".'
      };
    }

    return {
      status: 'attached_running',
      serverName: serverName,
      message: 'Attached MCP server "' + serverName + '" to "' + agentName + '".'
    };
  }

  async function applyEmailMCPCandidate(agentName, candidate) {
    var installOutcome = await installMCPServerCandidate(candidate);
    var status = normalizeToken(installOutcome && installOutcome.status);
    if (!status || ['installed', 'already_installed'].indexOf(status) < 0) {
      return installOutcome;
    }

    if (!agentName) {
      return {
        status: status === 'installed' ? 'installed_only' : 'already_installed',
        serverName: installOutcome.serverName || candidate && candidate.name,
        message: status === 'installed'
          ? 'Installed MCP server "' + (installOutcome.serverName || candidate && candidate.name || 'selected server') + '".'
          : 'MCP server "' + (installOutcome.serverName || candidate && candidate.name || 'selected server') + '" is already installed.'
      };
    }

    return enableMCPServerForAgent(agentName, installOutcome.serverName || candidate && candidate.name);
  }

  async function openScopedMCPBrowseModal(agentName, prompt, options) {
    var modalOptions = options || {};
    var buildCandidates = typeof modalOptions.buildCandidates === 'function'
      ? modalOptions.buildCandidates
      : null;
    if (!buildCandidates) return { status: 'invalid_config' };

    var bundle = await buildCandidates(prompt);
    var candidates = Array.isArray(bundle && bundle.candidates) ? bundle.candidates : [];
    var modalPrefix = String(modalOptions.modalPrefix || 'homeMCPBrowseModal').trim() || 'homeMCPBrowseModal';
    var modalId = modalPrefix + '-' + Date.now();
    var searchId = modalId + '-search';
    var countId = modalId + '-count';
    var listId = modalId + '-list';
    var noteId = modalId + '-note';
    var confirmId = modalId + '-confirm';
    var skipId = modalId + '-skip';
    var openPageId = modalId + '-open';
    var switchId = modalId + '-switch';
    var radioGroupName = modalId + '-choice';

    var switchLabel = String(modalOptions.switchLabel || '').trim();
    var switchTarget = String(modalOptions.switchTarget || '').trim();

    var modalElement = document.createElement('div');
    modalElement.className = 'modal fade';
    modalElement.id = modalId;
    modalElement.tabIndex = -1;
    modalElement.setAttribute('aria-hidden', 'true');
    modalElement.innerHTML = ''
      + '<div class="modal-dialog modal-lg modal-dialog-scrollable">'
      + '  <div class="modal-content" style="background: var(--bg-secondary); border: 1px solid var(--border-color);">'
      + '    <div class="modal-header" style="border-bottom: 1px solid var(--border-color);">'
      + '      <h5 class="modal-title" style="color: var(--text-primary);">' + String(modalOptions.title || 'Browse MCP Connectors') + '</h5>'
      + '      <button type="button" class="btn-close" data-bs-dismiss="modal" aria-label="Close"></button>'
      + '    </div>'
      + '    <div class="modal-body">'
      + '      <p class="small mb-3" style="color: var(--text-secondary);">' + String(modalOptions.description || 'Select an MCP connector and apply it with explicit approval.') + '</p>'
      + '      <div class="input-group mb-2">'
      + '        <span class="input-group-text" style="background: var(--bg-tertiary); border-color: var(--border-color); color: var(--text-secondary);">Search</span>'
      + '        <input id="' + searchId + '" type="text" class="form-control" placeholder="' + String(modalOptions.searchPlaceholder || 'Search connectors...') + '" style="background: var(--bg-tertiary); border-color: var(--border-color); color: var(--text-primary);">'
      + '      </div>'
      + '      <div id="' + countId + '" class="small mb-2" style="color: var(--text-secondary);"></div>'
      + '      <div id="' + listId + '" class="list-group" style="max-height: 360px; overflow-y: auto;"></div>'
      + '      <div id="' + noteId + '" class="small mt-3" style="color: var(--text-secondary);"></div>'
      + '    </div>'
      + '    <div class="modal-footer" style="border-top: 1px solid var(--border-color);">'
      + '      <button id="' + openPageId + '" type="button" class="modern-btn modern-btn-secondary">Open MCP Page</button>'
      + (switchLabel
        ? ('      <button id="' + switchId + '" type="button" class="modern-btn modern-btn-secondary">' + switchLabel + '</button>')
        : '')
      + '      <button id="' + skipId + '" type="button" class="modern-btn modern-btn-secondary">'
      + (agentName ? String(modalOptions.skipWithAgentLabel || 'Continue Without MCP') : String(modalOptions.skipWithoutAgentLabel || 'Close'))
      + '      </button>'
      + '      <button id="' + confirmId + '" type="button" class="modern-btn modern-btn-primary" disabled>'
      + (agentName ? String(modalOptions.confirmWithAgentLabel || 'Use Selected & Continue') : String(modalOptions.confirmWithoutAgentLabel || 'Install Selected'))
      + '      </button>'
      + '    </div>'
      + '  </div>'
      + '</div>';

    document.body.appendChild(modalElement);

    var searchInput = modalElement.querySelector('#' + searchId);
    var countElement = modalElement.querySelector('#' + countId);
    var listElement = modalElement.querySelector('#' + listId);
    var noteElement = modalElement.querySelector('#' + noteId);
    var confirmButton = modalElement.querySelector('#' + confirmId);
    var skipButton = modalElement.querySelector('#' + skipId);
    var openPageButton = modalElement.querySelector('#' + openPageId);
    var switchButton = modalElement.querySelector('#' + switchId);

    var selectedName = '';
    var finalResult = null;

    function setNote(text, isError) {
      if (!noteElement) return;
      noteElement.style.color = isError ? 'var(--danger-color, #dc3545)' : 'var(--text-secondary)';
      noteElement.textContent = String(text || '');
    }

    function updateConfirmState() {
      if (!confirmButton) return;
      confirmButton.disabled = !selectedName;
    }

    function renderCandidateList() {
      if (!listElement) return;
      listElement.innerHTML = '';

      var filtered = filterEmailMCPCandidates(candidates, searchInput && searchInput.value || '');
      if (countElement) {
        countElement.textContent = filtered.length + ' connector(s) found';
      }

      if (filtered.length === 0) {
        var empty = document.createElement('div');
        empty.className = 'text-center py-4';
        empty.style.color = 'var(--text-secondary)';
        empty.textContent = String(modalOptions.emptyStateText || 'No matching MCP connectors found.');
        listElement.appendChild(empty);
        selectedName = '';
        updateConfirmState();
        return;
      }

      for (var i = 0; i < filtered.length; i++) {
        (function (candidate) {
          var row = document.createElement('label');
          row.className = 'list-group-item';
          row.style.background = 'var(--bg-tertiary)';
          row.style.borderColor = 'var(--border-color)';
          row.style.cursor = candidate.selectable ? 'pointer' : 'not-allowed';

          var wrapper = document.createElement('div');
          wrapper.className = 'd-flex align-items-start';
          wrapper.style.gap = '0.6rem';

          var radio = document.createElement('input');
          radio.type = 'radio';
          radio.className = 'form-check-input mt-1';
          radio.name = radioGroupName;
          radio.value = candidate.name;
          radio.disabled = !candidate.selectable;
          radio.checked = selectedName === candidate.name;
          radio.addEventListener('change', function () {
            selectedName = candidate.name;
            updateConfirmState();
            setNote(candidate.manualReason ? ('Note: ' + candidate.manualReason + '.') : '', false);
          });

          var content = document.createElement('div');
          content.style.flex = '1';

          var top = document.createElement('div');
          top.className = 'd-flex align-items-center justify-content-between flex-wrap';
          top.style.gap = '0.4rem';

          var name = document.createElement('strong');
          name.style.color = 'var(--text-primary)';
          name.textContent = candidate.name;

          var badges = document.createElement('div');
          badges.className = 'd-flex align-items-center flex-wrap';
          badges.style.gap = '0.35rem';

          function addBadge(text, className) {
            var badge = document.createElement('span');
            badge.className = 'badge ' + className;
            badge.textContent = text;
            badges.appendChild(badge);
          }

          if (candidate.category) addBadge(candidate.category, 'bg-secondary');
          if (candidate.source) addBadge(candidate.source, 'bg-dark');
          if (candidate.isInstalled) addBadge('installed', 'bg-primary');
          if (candidate.runtimeLabel) addBadge(candidate.runtimeLabel, candidate.runtimeStatus === 'running' ? 'bg-success' : 'bg-warning text-dark');
          if (candidate.legacy) addBadge('legacy', 'bg-warning text-dark');
          if (!candidate.selectable && candidate.manualReason) addBadge('manual setup', 'bg-warning text-dark');

          top.appendChild(name);
          top.appendChild(badges);

          var description = document.createElement('div');
          description.className = 'small mt-1';
          description.style.color = 'var(--text-secondary)';
          description.textContent = candidate.description || 'No description provided.';

          var note = document.createElement('div');
          note.className = 'small mt-1';
          note.style.color = candidate.manualReason ? 'var(--warning-color, #ffc107)' : 'var(--text-secondary)';
          if (candidate.manualReason) {
            note.textContent = 'Requires manual setup: ' + candidate.manualReason + '.';
          } else if (candidate.legacy && candidate.legacyReason) {
            note.textContent = candidate.legacyReason;
          } else if (candidate.isInstalled && candidate.enabled) {
            note.textContent = 'Already enabled globally.';
          } else if (candidate.isInstalled) {
            note.textContent = 'Installed. Selecting this will attach it to the agent.';
          } else {
            note.textContent = String(modalOptions.pendingInstallText || 'Will be installed and then attached.');
          }

          content.appendChild(top);
          content.appendChild(description);
          content.appendChild(note);

          wrapper.appendChild(radio);
          wrapper.appendChild(content);
          row.appendChild(wrapper);

          if (candidate.selectable) {
            row.addEventListener('click', function () {
              selectedName = candidate.name;
              radio.checked = true;
              updateConfirmState();
              setNote('', false);
            });
          }

          listElement.appendChild(row);
        })(filtered[i]);
      }
    }

    return new Promise(function (resolve) {
      var bsModal = new bootstrap.Modal(modalElement, { backdrop: 'static', keyboard: false });
      var done = false;

      function closeWith(result) {
        finalResult = result;
        bsModal.hide();
      }

      modalElement.addEventListener('hidden.bs.modal', function () {
        if (done) return;
        done = true;
        modalElement.remove();
        resolve(finalResult || { status: 'cancelled' });
      }, { once: true });

      if (searchInput) {
        searchInput.addEventListener('input', function () {
          renderCandidateList();
          setNote('', false);
        });
      }

      if (openPageButton) {
        openPageButton.addEventListener('click', function () {
          closeWith({ status: 'opened_mcp_page' });
          window.location.href = '/mcp';
        });
      }

      if (switchButton) {
        switchButton.addEventListener('click', function () {
          closeWith({ status: 'switch_browse', target: switchTarget });
        });
      }

      if (skipButton) {
        skipButton.addEventListener('click', function () {
          if (agentName) {
            closeWith({ status: 'continue_without_mcp' });
          } else {
            closeWith({ status: 'cancelled' });
          }
        });
      }

      if (confirmButton) {
        confirmButton.addEventListener('click', async function () {
          if (!selectedName) return;
          var selectedCandidate = null;
          for (var i = 0; i < candidates.length; i++) {
            if (candidates[i].name === selectedName) {
              selectedCandidate = candidates[i];
              break;
            }
          }
          if (!selectedCandidate) return;

          confirmButton.disabled = true;
          skipButton.disabled = true;
          openPageButton.disabled = true;
          if (switchButton) switchButton.disabled = true;
          var originalLabel = confirmButton.textContent;
          confirmButton.textContent = agentName
            ? String(modalOptions.progressWithAgentLabel || 'Applying...')
            : String(modalOptions.progressWithoutAgentLabel || 'Installing...');

          try {
            var outcome = await applyEmailMCPCandidate(agentName, selectedCandidate);
            var outcomeStatus = normalizeToken(outcome && outcome.status);
            if (['attached_running', 'installed_only', 'already_installed'].indexOf(outcomeStatus) >= 0) {
              closeWith(outcome);
              return;
            }
            setNote(outcome && outcome.message ? outcome.message : 'Could not apply the selected MCP connector.', true);
          } catch (error) {
            setNote(String(error && error.message || error || 'Unexpected MCP setup failure'), true);
          } finally {
            if (confirmButton) {
              confirmButton.textContent = originalLabel;
              confirmButton.disabled = !selectedName;
            }
            if (skipButton) skipButton.disabled = false;
            if (openPageButton) openPageButton.disabled = false;
            if (switchButton) switchButton.disabled = false;
          }
        });
      }

      renderCandidateList();
      updateConfirmState();
      bsModal.show();
    });
  }

  async function openEmailMCPBrowseModal(agentName, prompt) {
    return openScopedMCPBrowseModal(agentName, prompt, {
      modalPrefix: 'homeEmailMCPBrowseModal',
      title: 'Browse Email MCP Connectors',
      description: 'Select an email connector for Gmail, Outlook, or IMAP. You can search, select, and apply without leaving this flow.',
      searchPlaceholder: 'gmail, outlook, imap...',
      emptyStateText: 'No matching email MCP connectors found.',
      pendingInstallText: 'Will be installed and then attached.',
      switchLabel: 'Use Browser Control',
      switchTarget: 'browser_control',
      buildCandidates: buildEmailMCPBrowseCandidates
    });
  }

  async function openBrowserControlMCPBrowseModal(agentName, prompt) {
    return openScopedMCPBrowseModal(agentName, prompt, {
      modalPrefix: 'homeBrowserMCPBrowseModal',
      title: 'Browse Browser Control MCP',
      description: 'Select a browser-control connector (Playwright, Browserbase, or Puppeteer). Use this when email APIs are unavailable and you want guided browser-based access.',
      searchPlaceholder: 'playwright, browserbase, puppeteer...',
      emptyStateText: 'No matching browser-control MCP connectors found.',
      pendingInstallText: 'Will be installed and then attached for browser-control tasks.',
      switchLabel: 'Use Email Connector',
      switchTarget: 'email_connector',
      buildCandidates: buildBrowserMCPBrowseCandidates
    });
  }

  async function runEmailAccessMCPSelection(agentName, prompt, startingMode) {
    var mode = normalizeToken(startingMode) === 'browser' ? 'browser' : 'email';
    for (var i = 0; i < 4; i++) {
      var result = mode === 'browser'
        ? await openBrowserControlMCPBrowseModal(agentName, prompt)
        : await openEmailMCPBrowseModal(agentName, prompt);
      var status = normalizeToken(result && result.status);
      if (status === 'switch_browse') {
        var target = normalizeToken(result && result.target);
        mode = target === 'browser_control' ? 'browser' : 'email';
        continue;
      }
      return result || { status: 'cancelled' };
    }
    return { status: 'cancelled' };
  }

  function formatEmailMCPOptionSummary(emailAdvice) {
    if (!emailAdvice) return '';
    var lines = [];
    var configured = Array.isArray(emailAdvice.configuredMatches) ? emailAdvice.configuredMatches : [];
    var marketplace = emailAdvice.marketplaceMatch;

    lines.push('Email setup options:');
    lines.push('1) Browse and select an email MCP connector (recommended).');
    if (configured.length > 0) {
      var configuredNames = [];
      for (var i = 0; i < configured.length; i++) {
        configuredNames.push(String(configured[i].name || '').trim());
      }
      lines.push('   Available now: ' + configuredNames.filter(Boolean).join(', ') + '.');
    } else if (marketplace && marketplace.name) {
      lines.push('   Suggested MCP from marketplace: "' + marketplace.name + '".');
    } else {
      lines.push('   If no connector is installed yet, browse and add Gmail, Outlook, or IMAP.');
    }
    lines.push('2) Use Browser Control MCP (Playwright/Browserbase/Puppeteer) when mailbox APIs are unavailable.');
    lines.push('3) Create a dedicated Email Assistant and attach MCP after setup.');
    lines.push('Safety defaults: start read-only and require explicit approval before sending or deleting.');

    return lines.join('\n');
  }

  async function buildEmailSolutionAdvice(prompt) {
    var requirement = detectMCPRequirement(prompt);
    if (!requirement || requirement.key !== 'email_inbox') {
      requirement = findMCPRequirementByKey('email_inbox');
    }
    if (!requirement) return null;

    var configuredServers = [];
    var marketplaceServers = [];
    try {
      var loaded = await Promise.all([
        fetchConfiguredMCPServers(),
        fetchMarketplaceMCPServers()
      ]);
      configuredServers = Array.isArray(loaded[0]) ? loaded[0] : [];
      marketplaceServers = Array.isArray(loaded[1]) ? loaded[1] : [];
    } catch (error) {
      dashLog.debug('Failed to prepare email MCP advice', { error: error && error.message || error });
    }

    return {
      requirement: requirement,
      configuredMatches: chooseConfiguredMCPServers(requirement, prompt, configuredServers, 2),
      marketplaceMatch: chooseMarketplaceMCPServer(requirement, prompt, marketplaceServers)
    };
  }

  async function renderEmailSolutionActions(prompt) {
    var advice = await buildEmailSolutionAdvice(prompt);
    var summary = formatEmailMCPOptionSummary(advice);
    if (summary) {
      appendHomeAssistantMessage('assistant', summary);
    }

    homeAssistantState.awaitingCreateConfirmation = true;
    setHomeAssistantRoutingSummary('Email Setup Options', 'Choose email MCP, browser control MCP, or create a dedicated email agent.');

    function openSelection(mode) {
      runEmailAccessMCPSelection('', prompt, mode).then(function (result) {
        var resultStatus = normalizeToken(result && result.status);
        if (resultStatus === 'installed_only' || resultStatus === 'already_installed') {
          appendHomeAssistantMessage('assistant', result.message || 'Selected MCP connector is ready.');
        } else if (resultStatus === 'opened_mcp_page') {
          appendHomeAssistantMessage('assistant', 'Opened MCP settings so you can review connector details.');
        }
      }).catch(function (error) {
        dashLog.debug('Email MCP browse modal failed', { error: error && error.message || error });
      });
    }

    renderHomeAssistantActions([
      {
        label: 'Browse Email MCP',
        variant: 'primary',
        onClick: function () { openSelection('email'); }
      },
      {
        label: 'Use Browser Control',
        variant: 'secondary',
        onClick: function () { openSelection('browser'); }
      },
      {
        label: 'Create Email Agent',
        variant: 'secondary',
        onClick: function () { createAgentForPendingTask(); }
      },
      {
        label: 'Ask Another Task',
        variant: 'secondary',
        onClick: function () { focusHomeAssistantInput(); }
      }
    ]);
  }

  function shouldPauseForEmailMCPSelection(mcpOutcome) {
    var status = normalizeToken(mcpOutcome && mcpOutcome.status);
    if (!status) return true;
    if (['already_enabled', 'enabled_existing', 'installed_and_enabled'].indexOf(status) >= 0) {
      return false;
    }
    return true;
  }

  async function maybeResolveEmailMCPBeforeHandoff(agentName, prompt, mcpOutcome) {
    if (!agentName) {
      return { continueHandoff: false };
    }
    if (!shouldPauseForEmailMCPSelection(mcpOutcome)) {
      return { continueHandoff: true };
    }

    appendHomeAssistantMessage('assistant', 'Before handoff, select an email connector or browser-control connector so this task can access your inbox.');
    setHomeAssistantRoutingSummary('Email MCP Required', 'Select an email or browser-control connector before continuing.');

    var selection = await runEmailAccessMCPSelection(agentName, prompt, 'email');
    var selectionStatus = normalizeToken(selection && selection.status);

    if (selectionStatus === 'attached_running') {
      appendHomeAssistantMessage('assistant', selection.message || ('Attached MCP server "' + (selection.serverName || 'selected server') + '".'));
      if (isLegacyMCPServerName(selection && selection.serverName)) {
        appendHomeAssistantMessage('assistant', 'Note: Puppeteer MCP is legacy/deprecated. Playwright is recommended for browser control.');
      }
      return { continueHandoff: true };
    }

    if (selectionStatus === 'continue_without_mcp') {
      appendHomeAssistantMessage('assistant', 'Continuing without an MCP connector. Email access may be unavailable.');
      return { continueHandoff: true };
    }

    if (selectionStatus === 'attached_not_running') {
      appendHomeAssistantMessage('assistant', selection.message || 'Selected MCP connector is not running yet. Configure it before handoff.');
      setHomeAssistantRoutingSummary('MCP Needs Configuration', 'Selected email connector is attached but not running yet.');
      renderHomeAssistantActions([
        {
          label: 'Open MCP Settings',
          variant: 'primary',
          onClick: function () { window.location.href = '/mcp'; }
        },
        {
          label: 'Ask Another Task',
          variant: 'secondary',
          onClick: function () { focusHomeAssistantInput(); }
        }
      ]);
      return { continueHandoff: false };
    }

    if (selection && selection.message) {
      appendHomeAssistantMessage('assistant', selection.message);
    } else {
      appendHomeAssistantMessage('assistant', 'Email handoff paused. Select an MCP connector when you are ready.');
    }
    setHomeAssistantRoutingSummary('Waiting for MCP Selection', 'Email task is paused until MCP setup is completed.');
    return { continueHandoff: false };
  }

  async function ensureMCPForTask(agentName, prompt, options) {
    var requirement = detectMCPRequirement(prompt);
    if (!requirement || !agentName) return null;
    var allowMutations = !(options && options.allowMutations === false);

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

    if (existing && !allowMutations) {
      return {
        status: 'existing_disabled',
        serverName: existing.name,
        message: 'MCP server "' + existing.name + '" matches this task but is not enabled for "' + agentName + '".'
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
          message: 'This task may need MCP (' + requirement.label + '), but no matching connector is currently configured.'
        };
      }
      targetServerName = installCandidate.name;

      if (!allowMutations) {
        return {
          status: 'candidate_available',
          serverName: targetServerName,
          message: 'Found MCP connector "' + targetServerName + '" for ' + requirement.label + '. Select it to install and attach.'
        };
      }

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

    if (!allowMutations) {
      return {
        status: 'needs_enable',
        serverName: targetServerName,
        message: 'MCP connector "' + targetServerName + '" is available. Select it to attach before continuing.'
      };
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
    if (intent.key === 'utility_direct') {
      base = 'Create a utility assistant for quick everyday requests such as time lookups, weather checks, simple conversions, and short factual questions.';
    } else if (intent.key === 'travel_planning') {
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
    if (intent.key === 'utility_direct') {
      return 'You are a utility assistant for quick requests. Handle time, weather, simple conversions, and short factual lookups with concise direct answers.';
    }
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
    var allowWebSearchInput = document.getElementById('agentAllowWebSearch');
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
    if (allowWebSearchInput) {
      allowWebSearchInput.checked = typeof seedPayload.allow_web_search === 'boolean' ? seedPayload.allow_web_search : true;
    }

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
            allow_web_search: allowWebSearchInput ? Boolean(allowWebSearchInput.checked) : true,
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

  async function checkHomeAssistantLLMAvailability() {
    if (typeof API === 'undefined' || typeof API.get !== 'function') {
      return { available: false, system_model_configured: false };
    }
    try {
      var data = await API.get('/api/agents/auto-config/availability');
      return {
        available: Boolean(data && data.available),
        system_model_configured: Boolean(data && data.system_model_configured),
        message: String(data && data.message || '')
      };
    } catch (error) {
      dashLog.debug('Failed to check Ask Ori model availability', { error: error && error.message || error });
      return { available: false, system_model_configured: false };
    }
  }

  function getHomeAssistantLLMRequirementMessage(availability) {
    var systemModelConfigured = Boolean(availability && availability.system_model_configured);
    if (!systemModelConfigured) {
      return 'No suitable agent is configured for this task, and a System Model must be configured before I can create one. Open Settings to configure it.';
    }
    return 'No suitable agent is configured for this task, and no LLM provider is available right now. Open Settings to configure a provider or model.';
  }

  async function createAgentForPendingTask() {
    if (!homeAssistantState.pendingPrompt) return;
    var prompt = homeAssistantState.pendingPrompt;
    var intent = homeAssistantState.pendingIntent || HOME_INTENTS.general_task;
    var appLaunchRequest = homeAssistantState.pendingAppLaunch;
    homeAssistantState.awaitingCreateConfirmation = false;

    var llmAvailability = await checkHomeAssistantLLMAvailability();
    if (!llmAvailability.available) {
      var llmRequiredMessage = getHomeAssistantLLMRequirementMessage(llmAvailability);
      appendHomeAssistantMessage('assistant', llmRequiredMessage);
      setHomeAssistantRoutingSummary('Model Configuration Required', llmRequiredMessage);
      renderHomeAssistantActions([
        {
          label: 'Go to Settings',
          variant: 'primary',
          onClick: function () { window.location.href = '/settings'; }
        },
        {
          label: 'Ask Another Task',
          variant: 'secondary',
          onClick: function () { focusHomeAssistantInput(); }
        }
      ]);
      if (window.Toast) {
        Toast.warning('System Model setup is required before creating a new agent.');
      }
      return;
    }

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

      if (isSemiAutoMode()) {
        if (payload.model) {
          appendHomeAssistantMessage('assistant',
            'Drafted "' + agentName + '" with model "' + payload.model + '". Please confirm in the Create Agent modal.');
        } else {
          appendHomeAssistantMessage('assistant',
            'I need your input to finalize model selection. Please confirm in the Create Agent modal.');
        }
        setHomeAssistantRoutingSummary('Semi-auto', 'Review and confirm agent details in the modal.');
        var confirmedSemiAutoAgentName = await confirmAgentCreationWithModal(payload);
        if (!confirmedSemiAutoAgentName) {
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
        agentName = confirmedSemiAutoAgentName;
      } else if (!payload.model) {
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

      var createdAgentMCP = await ensureMCPForTask(
        agentName,
        prompt,
        intent.key === 'email_check' ? { allowMutations: false } : null
      );
      if (createdAgentMCP && createdAgentMCP.message) {
        appendHomeAssistantMessage('assistant', createdAgentMCP.message);
      }
      if (intent.key === 'email_check') {
        var createdEmailMCPResolution = await maybeResolveEmailMCPBeforeHandoff(agentName, prompt, createdAgentMCP);
        if (!createdEmailMCPResolution || !createdEmailMCPResolution.continueHandoff) {
          return;
        }
      }

      if (isSemiAutoMode()) {
        appendHomeAssistantMessage('assistant', 'Agent is ready. Handing off this task to chat now.');
        setHomeAssistantRoutingSummary('Semi-auto', '"' + agentName + '" is ready. Handing off to chat.');
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

  function extractRouteMetadataFromChatPayload(payload) {
    if (!payload || typeof payload !== 'object') return null;
    var route = payload.route && typeof payload.route === 'object' ? payload.route : {};
    var mode = normalizeToken(route.mode || payload.route_mode);
    if (!mode) return null;
    return {
      mode: mode,
      toolName: String(route.tool_name || payload.tool_name || '').trim(),
      provider: String(route.provider || payload.provider || '').trim()
    };
  }

  function formatUtilityRouteSummary(routeMeta) {
    if (!routeMeta || routeMeta.mode !== 'utility_direct') {
      return 'Completed in the current assistant session.';
    }
    var text = routeMeta.toolName ? ('Executed "' + routeMeta.toolName + '" directly') : 'Executed utility tool directly';
    if (routeMeta.provider) {
      text += ' using ' + routeMeta.provider;
    }
    return text + '.';
  }

  async function runUtilityTaskDirect(prompt, agentName) {
    if (!prompt || !agentName) return;

    setHomeAssistantBusy(true, 'Running Utility...');
    renderHomeAssistantActions([]);
    appendHomeAssistantMessage('assistant', 'Running this as a direct utility request in the current assistant session.');
    setHomeAssistantRoutingSummary('Utility Direct', 'Executing directly without creating or handing off to another agent.');

    try {
      var data = await API.post('/api/chat', {
        question: prompt,
        agent_name: agentName
      });
      var responseText = String(data && data.response || '').trim();
      if (!responseText) {
        responseText = 'Completed utility request, but no text response was returned.';
      }

      var routeMeta = extractRouteMetadataFromChatPayload(data);
      appendHomeAssistantMessage('assistant', responseText);
      setHomeAssistantRoutingSummary('Utility Direct', formatUtilityRouteSummary(routeMeta));

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
      dashLog.debug('Direct utility execution failed', { error: error && error.message || error });
      appendHomeAssistantMessage('assistant', 'I could not execute that utility request directly right now.');
      setHomeAssistantRoutingSummary('Utility Direct Failed', 'Could not execute utility request directly.');
      renderHomeAssistantActions([
        {
          label: 'Retry',
          variant: 'primary',
          onClick: function () { runUtilityTaskDirect(prompt, agentName); }
        },
        {
          label: 'Ask Another Task',
          variant: 'secondary',
          onClick: function () { focusHomeAssistantInput(); }
        }
      ]);
    } finally {
      setHomeAssistantBusy(false);
    }
  }

  async function runPendingTaskWithAgent(prompt, agentName, options) {
    if (!prompt || !agentName) return;
    var appLaunchRequest = options && options.appLaunchRequest ? options.appLaunchRequest : null;
    var dispatchIntent = options && options.intent ? options.intent : homeAssistantState.pendingIntent;
    var dispatchMessage = buildAskOriDispatchMessage(prompt, appLaunchRequest, dispatchIntent);

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

        if (homeAssistantState.pendingIntent && homeAssistantState.pendingIntent.key === 'utility_direct') {
          await runUtilityTaskDirect(text, match.agent.name);
          return;
        }
        var matchedAgentMCP = await ensureMCPForTask(
          match.agent.name,
          text,
          homeAssistantState.pendingIntent.key === 'email_check' ? { allowMutations: false } : null
        );
        if (matchedAgentMCP && matchedAgentMCP.message) {
          appendHomeAssistantMessage('assistant', matchedAgentMCP.message);
        }
        if (homeAssistantState.pendingIntent.key === 'email_check') {
          var matchedEmailMCPResolution = await maybeResolveEmailMCPBeforeHandoff(match.agent.name, text, matchedAgentMCP);
          if (!matchedEmailMCPResolution || !matchedEmailMCPResolution.continueHandoff) {
            return;
          }
        }
        if (isSemiAutoMode()) {
          appendHomeAssistantMessage('assistant', 'Match found. Handing off to "' + match.agent.name + '" now.');
          setHomeAssistantRoutingSummary('Semi-auto', 'Match found. Handing off to "' + match.agent.name + '".');
        }
        await runPendingTaskWithAgent(text, match.agent.name, { appLaunchRequest: appLaunchRequest });
      } else {
        var llmAvailabilityForCreate = await checkHomeAssistantLLMAvailability();
        if (!llmAvailabilityForCreate.available) {
          homeAssistantState.awaitingCreateConfirmation = false;
          var llmRequiredMessage = getHomeAssistantLLMRequirementMessage(llmAvailabilityForCreate);
          appendHomeAssistantMessage('assistant', llmRequiredMessage);
          setHomeAssistantRoutingSummary('Model Configuration Required', llmRequiredMessage);
          renderHomeAssistantActions([
            {
              label: 'Go to Settings',
              variant: 'primary',
              onClick: function () { window.location.href = '/settings'; }
            },
            {
              label: 'Ask Another Task',
              variant: 'secondary',
              onClick: function () { focusHomeAssistantInput(); }
            }
          ]);
          if (window.Toast) {
            Toast.warning('System Model setup is required before creating a new agent.');
          }
          return;
        }

        if (isSemiAutoMode()) {
          if (homeAssistantState.pendingIntent.key === 'email_check') {
            appendHomeAssistantMessage('assistant',
              'No suitable email agent found yet. Choose a setup path and I can continue.');
            await renderEmailSolutionActions(text);
            return;
          }
          appendHomeAssistantMessage('assistant',
            'No suitable agent found for this task. I will open the Create Agent modal so you can review details.');
          setHomeAssistantRoutingSummary('Semi-auto', 'No match found. Review and confirm agent creation.');
          await createAgentForPendingTask();
        } else {
          homeAssistantState.awaitingCreateConfirmation = true;
          appendHomeAssistantMessage('assistant',
            'No suitable agent found for this task. Would you like me to create one?');
          setHomeAssistantRoutingSummary('No Match', 'No suitable agent was found. Create a new agent to continue.');

          if (homeAssistantState.pendingIntent.key === 'email_check') {
            await renderEmailSolutionActions(text);
            return;
          }

          renderHomeAssistantActions([
            {
              label: 'Create Agent',
              variant: 'primary',
              onClick: function () { createAgentForPendingTask(); }
            }
          ]);
        }
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
    openHomeAssistantThinkingModal();
    handleHomeAssistantPrompt(prompt);
  }

  function initHomeAssistant() {
    var els = getHomeAssistantElements();
    if (!els.form || !els.input) return;
    var supportsRecentSessions = Boolean(els.recentSection || els.recentSessions || els.viewAllBtn || els.clearRecentBtn);

    homeAssistantState.automationMode = loadHomeAssistantAutomationMode();
    homeAssistantState.recentSessions = supportsRecentSessions ? loadHomeAssistantRecentSessions() : [];
    setHomeAssistantRoutingSummary('', '');
    renderHomeAssistantRecentSessions();
    if (supportsRecentSessions) {
      hydrateHomeAssistantRecentSessions();
    }
    setHomeAssistantMode(homeAssistantState.mode);
    setHomeAssistantAutomationMode(homeAssistantState.automationMode);
    syncHomeAssistantThinkingStatus();

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
    if (els.autoFullBtn) {
      els.autoFullBtn.addEventListener('click', function () {
        if (homeAssistantState.busy) return;
        setHomeAssistantAutomationMode('full_auto');
      });
    }
    if (els.autoSemiBtn) {
      els.autoSemiBtn.addEventListener('click', function () {
        if (homeAssistantState.busy) return;
        setHomeAssistantAutomationMode('semi_auto');
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
    initHomeAssistant();

    if (typeof API === 'undefined' || typeof API.get !== 'function') {
      dashLog.debug('API not available, skipping dashboard data load');
      return;
    }

    var evolutionEnabled = Boolean(window.oriFeatures && window.oriFeatures.evolutionEnabled);
    var needsStats = Boolean(
      document.getElementById('dashboardStatAgents') ||
      document.getElementById('dashboardStatActive') ||
      document.getElementById('dashboardStatMessages') ||
      document.getElementById('dashboardStatCost')
    );
    var needsAgentList = Boolean(document.getElementById('dashboardAgentList'));
    var needsProfile = Boolean(document.getElementById('dashboardPersonalizeBanner'));
    var needsProgress = Boolean(evolutionEnabled && document.getElementById('dashboardProgressCard'));

    var tasks = [];

    if (needsStats) {
      tasks.push({
        key: 'stats',
        promise: API.get('/api/agents/dashboard/stats').catch(function (err) {
          dashLog.debug('Failed to load dashboard stats', { error: err && err.message || err });
          return null;
        })
      });
    }

    if (needsAgentList) {
      tasks.push({
        key: 'agents',
        promise: API.get('/api/agents/dashboard/list').catch(function (err) {
          dashLog.debug('Failed to load agent list', { error: err && err.message || err });
          return null;
        })
      });
    }

    if (needsProfile) {
      tasks.push({
        key: 'profile',
        promise: API.get('/api/onboarding/user-profile').catch(function (err) {
          dashLog.debug('Failed to load user profile', { error: err && err.message || err });
          return null;
        })
      });
    }

    if (needsProgress) {
      tasks.push({
        key: 'progress',
        promise: API.get('/api/evolution/assistant').catch(function (err) {
          dashLog.debug('Failed to load assistant progress', { error: err && err.message || err });
          return null;
        })
      });
    }

    if (tasks.length === 0) return;

    Promise.allSettled(tasks.map(function (task) { return task.promise; })).then(function (results) {
      for (var i = 0; i < tasks.length; i++) {
        var key = tasks[i].key;
        var value = results[i].status === 'fulfilled' ? results[i].value : null;
        if (!value) continue;
        if (key === 'stats') renderStats(value);
        if (key === 'agents') renderAgentList(value);
        if (key === 'profile') renderPersonalizeBanner(value);
        if (key === 'progress') renderAssistantProgress(value);
      }
    });
  }

  document.addEventListener('DOMContentLoaded', initDashboard);
})();
