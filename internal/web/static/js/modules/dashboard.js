// Dashboard Module
// Fetches and renders Ori dashboard data: assistant progress, stats, and agent list

(function () {
  'use strict';

  const dashLog = typeof Logger !== 'undefined' ? Logger.withContext('Dashboard') : console;
  const XP_PER_LEVEL = 100;
  const HOME_ASSISTANT_MESSAGE_LIMIT = 60;
  const HOME_ASSISTANT_RECENT_SESSION_LIMIT = 8;
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

  const homeAssistantState = {
    pendingPrompt: '',
    pendingIntent: HOME_INTENTS.general_task,
    pendingAgentName: '',
    pendingSuggestedName: '',
    pendingSuggestedType: '',
    pendingAppLaunch: null,
    awaitingCreateConfirmation: false,
    busy: false,
    recentSessions: []
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

  function loadHomeAssistantRecentSessions() {
    if (!window.sessionStorage) return [];
    try {
      var raw = window.sessionStorage.getItem(HOME_ASSISTANT_SESSION_STORAGE_KEY);
      if (!raw) return [];
      var parsed = JSON.parse(raw);
      if (!Array.isArray(parsed)) return [];
      var result = [];
      for (var i = 0; i < parsed.length; i++) {
        var item = parsed[i] || {};
        if (!item.id || !item.agent_name) continue;
        result.push({
          id: String(item.id),
          agent_name: String(item.agent_name),
          title: String(item.title || 'New Session'),
          prompt: String(item.prompt || ''),
          created_at: Number(item.created_at || Date.now())
        });
      }
      return result.slice(0, HOME_ASSISTANT_RECENT_SESSION_LIMIT);
    } catch (error) {
      dashLog.debug('Failed to load Ask Ori recent sessions', { error: error && error.message || error });
      return [];
    }
  }

  function saveHomeAssistantRecentSessions() {
    if (!window.sessionStorage) return;
    try {
      window.sessionStorage.setItem(
        HOME_ASSISTANT_SESSION_STORAGE_KEY,
        JSON.stringify(homeAssistantState.recentSessions.slice(0, HOME_ASSISTANT_RECENT_SESSION_LIMIT))
      );
    } catch (error) {
      dashLog.debug('Failed to persist Ask Ori recent sessions', { error: error && error.message || error });
    }
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
      actions: document.getElementById('homeAssistantActions'),
      recentSessions: document.getElementById('homeAssistantRecentSessions'),
      quickButtons: document.querySelectorAll('.home-assistant-quick-btn'),
      avatarBtn: document.getElementById('dashboardAssistantAvatarBtn'),
      bubbleBtn: document.getElementById('dashboardAssistantBubbleBtn')
    };
  }

  function focusHomeAssistantInput() {
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
    if (!homeAssistantState.recentSessions || homeAssistantState.recentSessions.length === 0) {
      container.classList.add('d-none');
      return;
    }

    for (var i = 0; i < homeAssistantState.recentSessions.length; i++) {
      (function (item) {
        var row = document.createElement('div');
        row.className = 'd-inline-flex align-items-center';
        row.style.gap = '0.25rem';

        var openButton = document.createElement('button');
        openButton.type = 'button';
        openButton.className = 'modern-btn modern-btn-secondary';
        openButton.style.fontSize = '0.76rem';
        openButton.style.padding = '0.28rem 0.5rem';
        var promptLabel = truncateText(item.prompt, 24);
        var title = item.agent_name + ' - ' + (promptLabel || 'Task');
        openButton.textContent = title;
        openButton.title = item.title || 'Open session';
        openButton.addEventListener('click', function () {
          if (homeAssistantState.busy) return;
          openTrackedSession(item.id);
        });

        var deleteButton = document.createElement('button');
        deleteButton.type = 'button';
        deleteButton.className = 'modern-btn modern-btn-secondary';
        deleteButton.style.fontSize = '0.72rem';
        deleteButton.style.padding = '0.28rem 0.45rem';
        deleteButton.textContent = 'Delete';
        deleteButton.title = 'Delete session';
        deleteButton.addEventListener('click', function () {
          if (homeAssistantState.busy) return;
          deleteTrackedSession(item.id, item.title || title);
        });

        row.appendChild(openButton);
        row.appendChild(deleteButton);
        container.appendChild(row);
      })(homeAssistantState.recentSessions[i]);
    }

    container.classList.remove('d-none');
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
        var confirmedAgentName = await confirmAgentCreationWithModal(payload);
        if (!confirmedAgentName) {
          appendHomeAssistantMessage('assistant', 'Agent creation canceled. Ask again when you want to continue.');
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

      if (intent.key === 'email_check') {
        appendHomeAssistantMessage('assistant',
          'Email idea: connect Gmail/Outlook via OAuth, start with read-only scopes, summarize unread first, and require explicit approval before sending replies.');
      }

      await runPendingTaskWithAgent(prompt, agentName, { appLaunchRequest: appLaunchRequest });

      API.get('/api/agents/dashboard/list').then(function (agentData) {
        if (agentData) renderAgentList(agentData);
      }).catch(function () {});
    } catch (error) {
      dashLog.debug('Failed to create agent', { error: error && error.message || error });
      appendHomeAssistantMessage('assistant', 'I could not create an agent right now. Please check model/provider settings and try again.');
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
    if (appLaunchRequest && appLaunchRequest.appName) {
      appendHomeAssistantMessage('assistant',
        'Routing steps: 1) Start a new session. 2) Execute /openapp ' + appLaunchRequest.appName + ' to launch the app.');
    }

    try {
      var session = await dispatchPromptToChatSession(dispatchMessage, agentName);
      if (!session) throw new Error('Failed to launch chat session');
      trackHomeAssistantSession(session, prompt, agentName);
      appendHomeAssistantMessage('assistant', 'Started session "' + (session.title || 'New Session') + '" with "' + agentName + '".');
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
    var appLaunchRequest = parseAppLaunchRequest(text);

    if (homeAssistantState.awaitingCreateConfirmation && isAffirmativeConfirmation(text)) {
      appendHomeAssistantMessage('user', text);
      await createAgentForPendingTask();
      return;
    }

    if (homeAssistantState.awaitingCreateConfirmation && isNegativeConfirmation(text)) {
      appendHomeAssistantMessage('user', text);
      homeAssistantState.awaitingCreateConfirmation = false;
      appendHomeAssistantMessage('assistant', 'No problem. Ask another task when you are ready.');
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

        if (homeAssistantState.pendingIntent.key === 'email_check') {
          appendHomeAssistantMessage('assistant',
            'Idea for email handling: add OAuth (Gmail/Outlook), start read-only, summarize unread, and require explicit confirmation before any send action.');
        }
        await runPendingTaskWithAgent(text, match.agent.name, { appLaunchRequest: appLaunchRequest });
      } else {
        homeAssistantState.awaitingCreateConfirmation = true;
        appendHomeAssistantMessage('assistant',
          'No suitable agent found for this task. Would you like me to create one?');

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
    renderHomeAssistantRecentSessions();

    if (window.EventBus && typeof EventBus.on === 'function') {
      EventBus.on('session:deleted', function (payload) {
        if (!payload || !payload.sessionId) return;
        removeTrackedSession(payload.sessionId);
      });
    }

    els.form.addEventListener('submit', handleHomeAssistantSubmit);
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
