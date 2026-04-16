// Dashboard Module
// Fetches and renders Ori dashboard data: assistant progress, stats, and agent list
/* global renderDependencyResolutionModal, normalizeDependencyResolution */

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
  const HOME_ASSISTANT_THINKING_MIN_VISIBLE_MS = 1400;
  const HOME_ASSISTANT_WORKSPACE_INLINE_TIMEOUT_MS = 120000;

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
    calendar_check: {
      key: 'calendar_check',
      label: 'calendar or schedule',
      keywords: ['calendar', 'schedule', 'meeting', 'meetings', 'appointment', 'appointments', 'availability', 'free time', 'busy', 'events'],
      preferredPlugins: ['calendar', 'schedule', 'google-calendar'],
      preferredTypes: ['tool-calling', 'general'],
      defaultType: 'tool-calling',
      suggestedName: 'Calendar Assistant',
      tags: ['calendar', 'schedule', 'planning']
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

  const HOME_PLANNING_SPECIALISTS = {
    travel_itinerary: {
      key: 'travel_itinerary',
      agentName: 'Travel Itinerary Planner',
      label: 'Travel Itinerary Planner',
      type: 'research',
      subtaskIndex: 1,
      taskTitle: 'Build the day-by-day itinerary',
      scorePhrases: ['day by day', 'day-by-day', 'itinerary', 'trip plan', 'travel plan', 'restaurant', 'restaurants', 'food', 'museum', 'museums', 'nightlife', 'day trip', 'day trips', 'budget breakdown', 'budget', 'accommodation', 'accommodation areas', 'neighborhood', 'neighbourhood'],
      tags: ['travel', 'itinerary', 'planning', 'workspace-specialist'],
      description: 'Plans multi-city trips with day-by-day pacing, neighborhood suggestions, food highlights, local logistics, and route-aware recommendations.',
      systemPrompt: 'You are a travel itinerary planner. Build practical, day-by-day trip plans with realistic pacing, local food and neighborhood recommendations, transit notes, and concise options. Ask clarifying questions when key details are missing and avoid inventing bookings or confirmed reservations.',
      handoffInstruction: 'Use the reviewed intake to build a practical day-by-day itinerary. Keep the pacing realistic, include local logistics, and ask only the minimum follow-up needed if a critical detail is still missing.'
    },
    hotel_booking: {
      key: 'hotel_booking',
      agentName: 'Hotel Booking Agent',
      label: 'Hotel Booking Agent',
      type: 'research',
      subtaskIndex: 2,
      taskTitle: 'Recommend hotels and neighborhoods',
      scorePhrases: ['hotel', 'hotels', 'stay', 'stays', 'lodging', 'accommodation', 'where to stay', 'book hotel', 'book hotels'],
      tags: ['travel', 'hotels', 'lodging', 'workspace-specialist'],
      description: 'Finds and compares hotels by neighborhood, budget, amenities, and travel constraints.',
      systemPrompt: 'You are a hotel booking assistant. Help compare neighborhoods, lodging tradeoffs, budget fit, and stay logistics. Be explicit about assumptions, keep recommendations concise, and ask for missing constraints before making suggestions.',
      handoffInstruction: 'Use the reviewed intake to compare lodging areas, hotel tradeoffs, and budget fit. Focus on neighborhoods, stay logistics, and the strongest shortlist.'
    },
    flight_booking: {
      key: 'flight_booking',
      agentName: 'Flight Booking Agent',
      label: 'Flight Booking Agent',
      type: 'research',
      subtaskIndex: 3,
      taskTitle: 'Fill the booking gaps for flights and transfers',
      scorePhrases: ['flight', 'flights', 'airfare', 'airport', 'route option', 'route options', 'connection', 'connections', 'transfer', 'transfer timing'],
      tags: ['travel', 'flights', 'transport', 'workspace-specialist'],
      description: 'Helps fill booking gaps for flights and longer-distance travel legs with schedule and transfer considerations.',
      systemPrompt: 'You are a flight booking assistant. Help identify missing flight or long-distance travel legs, compare route options, call out tradeoffs, and confirm timing constraints before recommending bookings.',
      handoffInstruction: 'Use the reviewed intake to identify missing flights or long-distance travel legs, compare route options, and call out timing or transfer tradeoffs.'
    }
  };

  const HOME_CAPABILITY_REQUIREMENTS = [
    {
      key: 'calendar_access',
      label: 'calendar access',
      intents: ['calendar_check'],
      phrases: ['check my schedule', 'calendar', 'my calendar', 'schedule', 'meeting', 'meetings', 'appointment', 'appointments', 'availability', 'am i free', 'free time', 'busy', 'events'],
      preferredSkillNames: ['calendar-assistant'],
      skillMarketplaceQueries: ['calendar assistant', 'calendar'],
      preferredServerNames: ['google-calendar'],
      preferredCategories: ['productivity'],
      preferredAgentType: 'tool-calling',
      defaultAgentName: 'Calendar Assistant',
      canAnswerInline: true,
      requiresMCP: true,
      dispatchMode: 'calendar',
      workspaceEscalationPolicy: 'feature_gap'
    },
    {
      key: 'github_ops',
      label: 'GitHub operations',
      intents: ['general_task'],
      phrases: ['github', 'repository', 'repo', 'pull request', 'pull-request', 'issue', 'commit', 'branch', 'release'],
      preferredSkillNames: [],
      skillMarketplaceQueries: ['github'],
      preferredServerNames: ['github'],
      preferredCategories: ['development'],
      preferredAgentType: 'tool-calling',
      defaultAgentName: 'GitHub Assistant',
      canAnswerInline: false,
      requiresMCP: true,
      dispatchMode: 'generic',
      workspaceEscalationPolicy: 'feature_gap'
    },
    {
      key: 'web_research',
      label: 'web research',
      intents: ['general_task'],
      phrases: ['search the web', 'web search', 'search online', 'look up', 'lookup', 'internet search', 'latest news'],
      preferredSkillNames: [],
      skillMarketplaceQueries: ['research'],
      preferredServerNames: ['brave-search'],
      preferredCategories: ['search'],
      preferredAgentType: 'research',
      defaultAgentName: 'Research Assistant',
      canAnswerInline: false,
      requiresMCP: true,
      dispatchMode: 'generic',
      workspaceEscalationPolicy: 'feature_gap'
    },
    {
      key: 'email_inbox',
      label: 'email inbox access',
      intents: ['email_check'],
      phrases: ['check my email', 'check email', 'email', 'inbox', 'mailbox', 'gmail', 'outlook', 'unread', 'reply to email', 'triage'],
      preferredSkillNames: [],
      skillMarketplaceQueries: ['email assistant', 'email'],
      preferredServerNames: ['gmail', 'outlook', 'imap', 'microsoft-graph'],
      preferredCategories: ['communication', 'email', 'productivity'],
      preferredAgentType: 'tool-calling',
      defaultAgentName: 'Email Assistant',
      canAnswerInline: true,
      requiresMCP: true,
      dispatchMode: 'email',
      workspaceEscalationPolicy: 'feature_gap'
    },
    {
      key: 'browser_automation',
      label: 'browser automation',
      intents: ['general_task'],
      phrases: ['browser automation', 'control browser', 'use browser', 'website automation', 'automate website', 'playwright', 'browserbase', 'puppeteer'],
      preferredSkillNames: [],
      skillMarketplaceQueries: ['browser automation'],
      preferredServerNames: ['playwright', 'browserbase', 'puppeteer'],
      preferredCategories: ['automation', 'development', 'productivity'],
      preferredAgentType: 'tool-calling',
      defaultAgentName: 'Browser Assistant',
      canAnswerInline: false,
      requiresMCP: true,
      dispatchMode: 'generic',
      workspaceEscalationPolicy: 'feature_gap'
    },
    {
      key: 'database_query',
      label: 'database query',
      intents: ['general_task'],
      phrases: ['postgres', 'postgresql', 'database', 'sql query', 'run sql', 'db query', 'schema'],
      preferredSkillNames: [],
      skillMarketplaceQueries: ['database'],
      preferredServerNames: ['postgres'],
      preferredCategories: ['database'],
      preferredAgentType: 'tool-calling',
      defaultAgentName: 'Database Assistant',
      canAnswerInline: false,
      requiresMCP: true,
      dispatchMode: 'generic',
      workspaceEscalationPolicy: 'feature_gap'
    },
    {
      key: 'filesystem_ops',
      label: 'filesystem access',
      intents: ['general_task'],
      phrases: ['filesystem', 'file system', 'local files', 'read file', 'write file', 'directory', 'folder on my computer'],
      preferredSkillNames: [],
      skillMarketplaceQueries: ['filesystem'],
      preferredServerNames: ['filesystem'],
      preferredCategories: ['file-system'],
      preferredAgentType: 'tool-calling',
      defaultAgentName: 'Filesystem Assistant',
      canAnswerInline: false,
      requiresMCP: true,
      dispatchMode: 'generic',
      workspaceEscalationPolicy: 'feature_gap'
    }
  ];

  const _HOME_MCP_REQUIREMENTS = HOME_CAPABILITY_REQUIREMENTS.filter(function (requirement) {
    return Boolean(requirement && requirement.requiresMCP);
  });

  const homeAssistantState = {
    pendingPrompt: '',
    pendingIntent: HOME_INTENTS.general_task,
    pendingIntentVariant: '',
    pendingAgentName: '',
    pendingSuggestedName: '',
    pendingSuggestedType: '',
    pendingAppLaunch: null,
    pendingCapabilityPlan: null,
    pendingCapabilityBrief: '',
    awaitingCreateConfirmation: false,
    busy: false,
    recentSessions: [],
    mode: 'new_task',
    routingSummary: null,
    planningState: null,
    inlineReplyState: null,
    conversationCollapsed: false,
    automationMode: 'semi_auto',
    workspaceEntryAgentName: '',
    workspaceEntryWorkspaceId: ''
  };

  var homeAssistantThinkingModalInstance = null;
  var homeAssistantThinkingOpenedAt = 0;
  var homeAssistantThinkingCloseTimer = null;
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
    if (!current.id) return null;
    return {
      id: String(current.id),
      agent_name: String(current.agent_name || ''),
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

  function parseCreateWorkspaceCommand(prompt) {
    var raw = String(prompt || '').trim();
    if (!raw) return null;

    var normalized = normalizeToken(raw).replace(/\s+/g, ' ').trim();
    if (!normalized) return null;

    // Supported patterns:
    // - "create workspace"
    // - "create workspace called test2"
    // - "create a workspace named test2"
    // - "new workspace test2"
    var patterns = [
      /^create\s+(?:a\s+|an\s+)?workspace(?:\s+(?:called|named)\s+(.+)|\s+(.+))?$/,
      /^new\s+workspace(?:\s+(?:called|named)\s+(.+)|\s+(.+))?$/
    ];
    var name = '';
    for (var i = 0; i < patterns.length; i++) {
      var match = normalized.match(patterns[i]);
      if (!match) continue;
      name = String((match[1] || match[2] || match[3] || '')).trim();
      break;
    }

    if (!name && normalized !== 'create workspace' && normalized !== 'create a workspace' && normalized !== 'create an workspace' && normalized !== 'new workspace') {
      return null;
    }

    name = name.replace(/^[`"':\-\s]+|[`"':\-\s]+$/g, '').trim();
    if (!name) {
      name = 'New Workspace';
    } else {
      name = toTitleCase(name);
    }

    if (name.length > 56) name = name.slice(0, 56).trim();
    if (!name) name = 'New Workspace';
    return {
      name: name
    };
  }

  function parseWorkspaceSlashCommand(prompt) {
    var raw = String(prompt || '').trim();
    if (!raw || raw.charAt(0) !== '/') return null;

    var match = raw.match(/^\/(task|chat|c|note|directory|dir|file|upload)(?:\s+([\s\S]*))?$/i);
    if (!match) return null;

    var command = normalizeToken(match[1]);
    if (command === 'c') command = 'chat';
    if (command === 'dir') command = 'directory';
    if (command === 'upload') command = 'file';

    return {
      command: command,
      content: String(match[2] || '').trim()
    };
  }

  function sanitizeWorkspaceCommandContent(content) {
    var text = String(content || '').trim();
    if (!text) return '';
    if ((text.charAt(0) === '"' && text.charAt(text.length - 1) === '"') ||
        (text.charAt(0) === '\'' && text.charAt(text.length - 1) === '\'') ||
        (text.charAt(0) === '`' && text.charAt(text.length - 1) === '`')) {
      text = text.slice(1, -1).trim();
    }
    text = text.replace(/^(?:about|for|to|at|from|path|called|named)\s+/i, '').trim();
    return text;
  }

  function extractLikelyPathFromText(text) {
    var value = String(text || '').trim();
    if (!value) return '';

    var quoted = value.match(/["'`]([^"'`]*(?:\/|\\)[^"'`]*)["'`]/);
    if (quoted && quoted[1]) return sanitizeWorkspaceCommandContent(quoted[1]);

    var inline = value.match(/(?:^|\s)(~\/[^\s,;]+|\/[^\s,;]+|[A-Za-z]:\\[^\s,;]+)/);
    if (inline && inline[1]) return sanitizeWorkspaceCommandContent(inline[1]);

    return '';
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

    if (target.indexOf('://') >= 0 || target.indexOf('/') >= 0 || target.indexOf('\\') >= 0 || looksLikeWebHostTarget(target)) {
      return null;
    }

    return {
      appName: toTitleCase(target),
      rawTarget: target
    };
  }

  function promptContainsAnyPhrase(normalizedPrompt, phrases) {
    if (!normalizedPrompt || !Array.isArray(phrases) || phrases.length === 0) return false;
    for (var i = 0; i < phrases.length; i++) {
      var phrase = normalizeToken(phrases[i]);
      if (!phrase) continue;
      if (normalizedPrompt.indexOf(phrase) >= 0) return true;
    }
    return false;
  }

  function countPromptPhraseMatches(normalizedPrompt, phrases) {
    if (!normalizedPrompt || !Array.isArray(phrases) || phrases.length === 0) return 0;
    var count = 0;
    for (var i = 0; i < phrases.length; i++) {
      var phrase = normalizeToken(phrases[i]);
      if (!phrase) continue;
      if (normalizedPrompt.indexOf(phrase) >= 0) count += 1;
    }
    return count;
  }

  function isComplexProjectPrompt(prompt, intent) {
    if (!intent || intent.key !== 'general_task') return false;
    if (parseAppLaunchRequest(prompt)) return false;

    var normalizedPrompt = normalizeToken(prompt);
    if (!normalizedPrompt) return false;

    var buildVerbs = ['build', 'create', 'develop', 'design', 'implement', 'make', 'ship', 'start', 'set up', 'setup'];
    var projectTargets = ['website', 'web site', 'web app', 'app', 'application', 'landing page', 'dashboard', 'product', 'project', 'platform', 'system'];
    var complexitySignals = ['from scratch', 'full stack', 'frontend', 'backend', 'database', 'authentication', 'auth', 'api', 'deploy', 'deployment', 'production', 'mvp', 'architecture', 'roadmap', 'requirements'];

    var hasBuildVerb = promptContainsAnyPhrase(normalizedPrompt, buildVerbs);
    var hasProjectTarget = promptContainsAnyPhrase(normalizedPrompt, projectTargets);
    if (hasBuildVerb && hasProjectTarget) return true;

    var complexitySignalCount = countPromptPhraseMatches(normalizedPrompt, complexitySignals);
    if (hasProjectTarget && complexitySignalCount >= 1) return true;

    var tokenCount = uniqueValues(normalizedPrompt.split(/[^a-z0-9]+/g)).filter(Boolean).length;
    if (hasBuildVerb && complexitySignalCount >= 1 && tokenCount >= 8) return true;

    return false;
  }

  function looksLikeWebHostTarget(target) {
    var candidate = normalizeToken(target);
    if (!candidate || candidate.indexOf('.') < 0) return false;
    if (/\s/.test(candidate)) return false;
    if (!/^[a-z0-9.-]+$/.test(candidate)) return false;

    var labels = candidate.split('.');
    if (!labels || labels.length < 2) return false;
    for (var i = 0; i < labels.length; i++) {
      var label = labels[i];
      if (!label) return false;
      if (label.charAt(0) === '-' || label.charAt(label.length - 1) === '-') return false;
      if (!/^[a-z0-9-]+$/.test(label)) return false;
    }

    return labels[labels.length - 1].length >= 2;
  }

  function hasWorkspaceRouteContext(routeContext) {
    if (!routeContext || typeof routeContext !== 'object') return false;
    return Boolean(String(routeContext.workspace_id || '').trim());
  }

  function buildEmailDispatchMessage(prompt, _) {
    var userPrompt = String(prompt || '').trim();
    var lines = [];
    lines.push(
      'Email task:',
      userPrompt,
      '',
      'Execution requirements:',
      '- Use configured MCP connectors/tools first (email connector or browser-control connector).',
      '- If authentication is required, guide the user through login and continue.',
      '- Do not claim lack of access before attempting available MCP tools.',
      '- Keep operations read-only unless the user explicitly approves send/delete actions.'
    );
    return lines.join('\n');
  }

  function buildCalendarDispatchMessage(prompt, _) {
    var userPrompt = String(prompt || '').trim();
    var lines = [];
    lines.push(
      'Calendar task:',
      userPrompt,
      '',
      'Execution requirements:',
      '- Use configured calendar skills and MCP connectors first.',
      '- If the time range is omitted, default to today in the user\'s local timezone.',
      '- Do not claim lack of access before attempting available calendar capabilities.',
      '- Keep operations read-only unless the user explicitly approves create/update/delete actions.'
    );
    return lines.join('\n');
  }

  function buildWorkspaceManagerDispatchMessage(prompt) {
    return String(prompt || '').trim();
  }

  function buildAskOriDispatchMessage(prompt, appLaunchRequest, intent, routeContext) {
    if (intent && intent.key === 'email_check') {
      return buildEmailDispatchMessage(prompt, routeContext);
    }
    if (intent && intent.key === 'calendar_check') {
      return buildCalendarDispatchMessage(prompt, routeContext);
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
      identityName: document.getElementById('homeAssistantIdentityName'),
      sendBtn: document.getElementById('homeAssistantSendBtn'),
      conversationSection: document.getElementById('homeAssistantConversationSection'),
      conversation: document.getElementById('homeAssistantConversation'),
      conversationToggleBtn: document.getElementById('homeAssistantConversationToggleBtn'),
      conversationSummary: document.getElementById('homeAssistantConversationSummary'),
      routingSummary: document.getElementById('homeAssistantRoutingSummary'),
      planning: document.getElementById('homeAssistantPlanning'),
      inlineReply: document.getElementById('homeAssistantInlineReply'),
      thinkingModal: document.getElementById('homeAssistantThinkingModal'),
      thinkingModalLabel: document.getElementById('homeAssistantThinkingModalLabel'),
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
      launcherBtn: document.getElementById('homeAssistantReopenBtn'),
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

  function clearHomeAssistantThinkingCloseTimer() {
    if (!homeAssistantThinkingCloseTimer) return;
    window.clearTimeout(homeAssistantThinkingCloseTimer);
    homeAssistantThinkingCloseTimer = null;
  }

  function isHomeAssistantThinkingModalVisible() {
    var els = getHomeAssistantElements();
    return Boolean(els.thinkingModal && els.thinkingModal.classList.contains('show'));
  }

  function hasVisibleHomeAssistantActions() {
    var els = getHomeAssistantElements();
    return Boolean(els.actions && !els.actions.classList.contains('d-none') && els.actions.children.length > 0);
  }

  function hasVisibleHomeAssistantPlanning() {
    var els = getHomeAssistantElements();
    return Boolean(els.planning && !els.planning.classList.contains('d-none') && els.planning.children.length > 0);
  }

  function hasVisibleHomeAssistantInlineReply() {
    var els = getHomeAssistantElements();
    return Boolean(els.inlineReply && !els.inlineReply.classList.contains('d-none') && els.inlineReply.children.length > 0);
  }

  function hasHomeAssistantConversation() {
    var els = getHomeAssistantElements();
    return Boolean(els.conversation && els.conversation.dataset.initialized === 'true' && els.conversation.children.length > 0);
  }

  function shouldKeepHomeAssistantThinkingModalOpen() {
    if (homeAssistantState.busy) return true;
    if (homeAssistantState.routingSummary && homeAssistantState.routingSummary.text) return true;
    if (hasVisibleHomeAssistantPlanning()) return true;
    if (hasVisibleHomeAssistantInlineReply()) return true;
    if (hasVisibleHomeAssistantActions()) return true;
    return false;
  }

  function getHomeAssistantSummaryState() {
    var summary = homeAssistantState.routingSummary;
    if (!summary || !summary.text) return '';

    var explicitState = normalizeToken(summary.state);
    if (explicitState) return explicitState;

    var title = normalizeToken(summary.title);
    if (title.indexOf('delayed') >= 0 || title.indexOf('timeout') >= 0) return 'timeout';
    if (title.indexOf('unavailable') >= 0 || title.indexOf('failed') >= 0) return 'error';
    return 'info';
  }

  function hasHomeAssistantFailureState() {
    var state = getHomeAssistantSummaryState();
    return state === 'error' || state === 'timeout';
  }

  function getHomeAssistantActivityLabel() {
    var els = getHomeAssistantElements();
    var label = els.identityName ? String(els.identityName.textContent || '').trim() : '';
    if (label) return label;
    if (homeAssistantState.workspaceEntryAgentName) return getWorkspaceHomeAssistantDisplayName();
    return 'Ori';
  }

  function syncHomeAssistantModalHeading() {
    var els = getHomeAssistantElements();
    if (!els.thinkingModalLabel) return;

    var label = getHomeAssistantActivityLabel();
    var summaryState = getHomeAssistantSummaryState();
    var summary = homeAssistantState.routingSummary;

    if (summary && summary.heading) {
      els.thinkingModalLabel.textContent = String(summary.heading);
      return;
    }

    if (summaryState === 'timeout') {
      els.thinkingModalLabel.textContent = label + ' is delayed';
      return;
    }
    if (summaryState === 'error') {
      els.thinkingModalLabel.textContent = label + ' is unavailable';
      return;
    }
    if (hasVisibleHomeAssistantPlanning()) {
      els.thinkingModalLabel.textContent = label + ' needs your input';
      return;
    }
    if (hasVisibleHomeAssistantInlineReply()) {
      els.thinkingModalLabel.textContent = label + ' has a planning subtask ready';
      return;
    }
    if (hasVisibleHomeAssistantActions() && hasHomeAssistantConversation() && !homeAssistantState.busy) {
      els.thinkingModalLabel.textContent = label + ' has an update ready';
      return;
    }
    els.thinkingModalLabel.textContent = label + ' is working';
  }

  function setHomeAssistantConversationCollapsed(collapsed) {
    homeAssistantState.conversationCollapsed = Boolean(collapsed);
    syncHomeAssistantConversationSection();
  }

  function syncHomeAssistantConversationSection() {
    var els = getHomeAssistantElements();
    var section = els.conversationSection;
    if (!section) return;

    var hasConversation = hasHomeAssistantConversation();
    var hasStructuredStep = hasVisibleHomeAssistantPlanning() || hasVisibleHomeAssistantInlineReply();
    var hasFailureState = hasHomeAssistantFailureState();
    var canCollapse = hasConversation && (hasStructuredStep || hasFailureState);
    var isCollapsed = canCollapse && homeAssistantState.conversationCollapsed;

    section.classList.toggle('is-collapsible', canCollapse);
    section.classList.toggle('is-collapsed', isCollapsed);
    section.classList.toggle('is-empty', !hasConversation);

    if (els.conversationToggleBtn) {
      els.conversationToggleBtn.hidden = !canCollapse;
      els.conversationToggleBtn.textContent = isCollapsed ? 'Show Conversation' : 'Hide Conversation';
      if (!els.conversationToggleBtn.dataset.bound) {
        els.conversationToggleBtn.dataset.bound = 'true';
        els.conversationToggleBtn.addEventListener('click', function () {
          setHomeAssistantConversationCollapsed(!homeAssistantState.conversationCollapsed);
        });
      }
    }

    if (els.conversationSummary) {
      if (hasFailureState && hasConversation) {
        els.conversationSummary.textContent = homeAssistantState.routingSummary && homeAssistantState.routingSummary.conversationSummary
          ? String(homeAssistantState.routingSummary.conversationSummary)
          : 'Open this only if you want the original prompt, failing step, and error details.';
      } else if (hasStructuredStep && hasConversation) {
        els.conversationSummary.textContent = 'The active step is above. Open this only if you want the full transcript and manager notes.';
      } else if (hasConversation) {
        els.conversationSummary.textContent = 'Full transcript, progress notes, and manager replies appear here.';
      } else {
        els.conversationSummary.textContent = 'Progress updates will appear here after you send a task.';
      }
    }
  }

  function openHomeAssistantThinkingModal() {
    clearHomeAssistantThinkingCloseTimer();
    var modal = getHomeAssistantThinkingModalInstance();
    if (!modal) return;
    if (!isHomeAssistantThinkingModalVisible()) {
      homeAssistantThinkingOpenedAt = Date.now();
    }
    modal.show();
  }

  function closeHomeAssistantThinkingModal(options) {
    var force = Boolean(options && options.force);
    clearHomeAssistantThinkingCloseTimer();
    if (!force && shouldKeepHomeAssistantThinkingModalOpen()) {
      return;
    }
    var modal = getHomeAssistantThinkingModalInstance();
    if (!modal) return;
    var elapsed = homeAssistantThinkingOpenedAt > 0 ? (Date.now() - homeAssistantThinkingOpenedAt) : HOME_ASSISTANT_THINKING_MIN_VISIBLE_MS;
    var remaining = force ? 0 : Math.max(0, HOME_ASSISTANT_THINKING_MIN_VISIBLE_MS - elapsed);

    function hideNow() {
      if (!force && shouldKeepHomeAssistantThinkingModalOpen()) {
        return;
      }
      modal.hide();
    }

    if (remaining > 0) {
      homeAssistantThinkingCloseTimer = window.setTimeout(hideNow, remaining);
      return;
    }
    hideNow();
  }

  function syncHomeAssistantThinkingStatus() {
    var els = getHomeAssistantElements();
    if (!els.thinkingStatus) return;

    var statusText = '';
    if (getHomeAssistantSummaryState() === 'timeout') {
      statusText = 'The manager may still be working. Retry here or open full chat to continue there.';
    } else if (getHomeAssistantSummaryState() === 'error') {
      statusText = homeAssistantState.routingSummary && homeAssistantState.routingSummary.text
        ? String(homeAssistantState.routingSummary.text)
        : 'Review the failure details, retry here, or open full chat.';
    } else if (homeAssistantState.routingSummary && homeAssistantState.routingSummary.text) {
      statusText = homeAssistantState.routingSummary.text;
    } else if (homeAssistantState.busy) {
      statusText = 'Working...';
    } else if (hasVisibleHomeAssistantPlanning()) {
      statusText = 'Complete the active planning step below.';
    } else if (hasVisibleHomeAssistantInlineReply()) {
      statusText = 'Answer the manager here or open full chat.';
    } else if (hasVisibleHomeAssistantActions()) {
      statusText = 'Review the latest manager update, open full chat, or dismiss this window.';
    } else {
      statusText = 'Ready for your next task.';
    }

    var textNode = els.thinkingStatus.querySelector('span:last-child');
    if (textNode) textNode.textContent = statusText;
    if (els.thinkingSpinner) {
      els.thinkingSpinner.classList.toggle('d-none', !homeAssistantState.busy);
    }

    syncHomeAssistantModalHeading();
    syncHomeAssistantLauncher();
  }

  function syncHomeAssistantLauncher() {
    var els = getHomeAssistantElements();
    var button = els.launcherBtn;
    if (!button) return;

    var available = Boolean(els.thinkingModal);
    button.classList.toggle('d-none', !available);
    button.disabled = !available;

    if (!available) {
      button.setAttribute('aria-hidden', 'true');
      return;
    }

    button.removeAttribute('aria-hidden');

    var active = Boolean(
      homeAssistantState.busy ||
      (homeAssistantState.routingSummary && homeAssistantState.routingSummary.text) ||
      hasVisibleHomeAssistantPlanning() ||
      hasVisibleHomeAssistantInlineReply() ||
      hasVisibleHomeAssistantActions() ||
      hasHomeAssistantConversation()
    );

    button.classList.toggle('modern-btn-primary', active);
    button.classList.toggle('modern-btn-secondary', !active);
    button.setAttribute('aria-pressed', active ? 'true' : 'false');
    button.setAttribute('title', active ? 'Reopen Ask Ori activity' : 'Open Ask Ori activity');

    var label = button.querySelector('[data-home-assistant-launcher-label]');
    if (label) {
      label.textContent = homeAssistantState.busy ? 'Live Activity' : 'Task Activity';
    }

    syncHomeAssistantModalHeading();
    syncHomeAssistantConversationSection();
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

    var detailText = String(summary.detail || '').trim();
    if (detailText) {
      var detail = document.createElement('span');
      detail.className = 'home-assistant-routing-detail';
      detail.textContent = detailText;
      container.appendChild(title);
      container.appendChild(text);
      container.appendChild(detail);
    } else {
      container.appendChild(title);
      container.appendChild(text);
    }
    container.classList.remove('d-none');
    syncHomeAssistantThinkingStatus();
  }

  function setHomeAssistantRoutingSummary(title, text, options) {
    if (!title || !text) {
      homeAssistantState.routingSummary = null;
      renderHomeAssistantRoutingSummary();
      if (!homeAssistantState.busy) {
        closeHomeAssistantThinkingModal();
      }
      syncHomeAssistantThinkingStatus();
      return;
    }
    homeAssistantState.routingSummary = {
      title: String(title),
      text: String(text),
      state: options && options.state ? String(options.state) : '',
      detail: options && options.detail ? String(options.detail) : '',
      heading: options && options.heading ? String(options.heading) : '',
      conversationSummary: options && options.conversationSummary ? String(options.conversationSummary) : ''
    };
    renderHomeAssistantRoutingSummary();
    if (homeAssistantState.busy || hasVisibleHomeAssistantActions()) {
      openHomeAssistantThinkingModal();
    }
  }

  function getWorkspaceHomeAssistantDisplayName() {
    return String(homeAssistantState.workspaceEntryAgentName || '').trim() || 'Workspace Manager';
  }

  function buildHomeAssistantPlaceholder(routeContext) {
    if (!routeContext) return 'Ask Ori to do something...';
    if (routeContext.surface === 'workspace_canvas') {
      return 'Message ' + getWorkspaceHomeAssistantDisplayName() + ' about this workspace canvas...';
    }
    if (hasWorkspaceRouteContext(routeContext)) {
      return 'Ask ' + getWorkspaceHomeAssistantDisplayName() + ' about this workspace... (or use /task, /note, /directory, /file, /chat)';
    }
    return 'Ask Ori to do something...';
  }

  function renderHomeAssistantWorkspaceIdentity(routeContext) {
    var normalizedContext = normalizeHomeRouteContext(routeContext);
    if (!hasWorkspaceRouteContext(normalizedContext)) return;

    var els = getHomeAssistantElements();
    var displayName = getWorkspaceHomeAssistantDisplayName();

    if (els.identityName) {
      els.identityName.textContent = displayName;
      els.identityName.setAttribute('title', displayName);
    }

    if (els.input) {
      els.input.placeholder = buildHomeAssistantPlaceholder(normalizedContext);
      els.input.setAttribute('aria-label', displayName + ' prompt');
    }

    syncHomeAssistantModalHeading();
  }

  async function refreshHomeAssistantWorkspaceIdentity(routeContext) {
    var normalizedContext = normalizeHomeRouteContext(routeContext);
    if (!hasWorkspaceRouteContext(normalizedContext)) {
      homeAssistantState.workspaceEntryAgentName = '';
      homeAssistantState.workspaceEntryWorkspaceId = '';
      return;
    }

    var workspaceId = String(normalizedContext.workspace_id || '').trim();
    homeAssistantState.workspaceEntryWorkspaceId = workspaceId;
    homeAssistantState.workspaceEntryAgentName = '';
    renderHomeAssistantWorkspaceIdentity(normalizedContext);

    if (!workspaceId) return;

    var entryAgentName = await fetchWorkspaceEntryAgentName(workspaceId);
    if (homeAssistantState.workspaceEntryWorkspaceId !== workspaceId) return;
    if (!entryAgentName) return;

    homeAssistantState.workspaceEntryAgentName = entryAgentName;
    renderHomeAssistantWorkspaceIdentity(normalizedContext);
  }

  function setHomeAssistantMode(mode) {
    var nextMode = mode === 'continue_session' ? 'continue_session' : 'new_task';
    homeAssistantState.mode = nextMode;
    var els = getHomeAssistantElements();
    var routeContext = buildHomeRouteContext();

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
      els.input.placeholder = buildHomeAssistantPlaceholder(routeContext);
    }
    if (hasWorkspaceRouteContext(routeContext)) {
      renderHomeAssistantWorkspaceIdentity(routeContext);
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
    clearHomeAssistantPlanning();
    clearHomeAssistantInlineReply();
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
    if (els.sendBtn && els.input) {
      if (!els.sendBtn.dataset.defaultLabel) {
        els.sendBtn.dataset.defaultLabel = els.sendBtn.textContent || 'Ask';
      }
      els.sendBtn.disabled = homeAssistantState.busy;
      els.input.disabled = homeAssistantState.busy;
      els.sendBtn.textContent = homeAssistantState.busy ? (busyLabel || 'Working...') : els.sendBtn.dataset.defaultLabel;
    }
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
    } else {
      closeHomeAssistantThinkingModal();
    }
    syncHomeAssistantThinkingStatus();
    renderHomeAssistantPlanning();
    renderHomeAssistantInlineReply();
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

    var messageText = typeof text === 'string' ? text : String(text == null ? '' : text);
    var structuredText = formatHomeAssistantStructuredMessage(messageText);
    var bubble = document.createElement('div');
    bubble.style.maxWidth = '85%';
    bubble.style.padding = '0.55rem 0.75rem';
    bubble.style.borderRadius = '10px';
    bubble.style.whiteSpace = 'pre-wrap';
    bubble.style.overflowWrap = 'anywhere';
    bubble.style.wordBreak = 'break-word';
    bubble.style.minWidth = '0';
    bubble.style.fontSize = '0.85rem';
    bubble.style.lineHeight = '1.4';
    bubble.style.border = '1px solid var(--border-color)';
    bubble.style.color = 'var(--text-primary)';
    bubble.style.background = role === 'user' ? 'var(--primary-color-light)' : 'var(--bg-primary)';

    if (structuredText) {
      bubble.style.padding = '0.65rem 0.8rem';
      bubble.style.maxHeight = '320px';
      bubble.style.overflow = 'auto';

      var pre = document.createElement('pre');
      pre.style.margin = '0';
      pre.style.whiteSpace = 'pre-wrap';
      pre.style.overflowWrap = 'anywhere';
      pre.style.wordBreak = 'break-word';
      pre.style.fontSize = '0.8rem';
      pre.style.lineHeight = '1.45';
      pre.style.fontFamily = 'var(--font-family-mono, ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace)';
      pre.textContent = structuredText;
      bubble.appendChild(pre);
    } else {
      bubble.textContent = messageText;
    }

    row.appendChild(bubble);
    conversation.appendChild(row);

    while (conversation.children.length > HOME_ASSISTANT_MESSAGE_LIMIT) {
      conversation.removeChild(conversation.firstChild);
    }
    conversation.scrollTop = conversation.scrollHeight;
    syncHomeAssistantConversationSection();
    syncHomeAssistantLauncher();
    openHomeAssistantThinkingModal();
  }

  function formatHomeAssistantStructuredMessage(text) {
    var trimmed = String(text || '').trim();
    if (!trimmed) return '';
    if (trimmed.charAt(0) !== '{' && trimmed.charAt(0) !== '[') return '';

    try {
      return JSON.stringify(JSON.parse(trimmed), null, 2);
    } catch (error) {
      return '';
    }
  }

  function parseHomeAssistantJSON(text) {
    var trimmed = String(text || '').trim();
    if (!trimmed) return null;
    if (trimmed.charAt(0) !== '{' && trimmed.charAt(0) !== '[') return null;
    try {
      return JSON.parse(trimmed);
    } catch (error) {
      return null;
    }
  }

  function isLikelyHomeAssistantRawToolPayload(text, data) {
    if (!data || !Array.isArray(data.toolCalls) || data.toolCalls.length === 0) return false;

    var parsed = parseHomeAssistantJSON(text);
    if (!parsed || Array.isArray(parsed) || typeof parsed !== 'object') return false;
    if (parsed.displayType || parsed.data || parsed.title || parsed.description) return false;

    var workspaceKeys = ['sessions', 'tasks', 'notes', 'files', 'directories', 'message', 'total'];
    for (var i = 0; i < workspaceKeys.length; i++) {
      if (Object.prototype.hasOwnProperty.call(parsed, workspaceKeys[i])) {
        return true;
      }
    }
    return false;
  }

  function getPlanningQuestions(planningState) {
    if (!planningState || !planningState.schema || !Array.isArray(planningState.schema.questions)) {
      return [];
    }
    return planningState.schema.questions;
  }

  function findPlanningQuestionById(planningState, questionId) {
    var questions = getPlanningQuestions(planningState);
    var target = String(questionId || '').trim();
    if (!target) return null;
    for (var i = 0; i < questions.length; i++) {
      var question = questions[i];
      if (String(question && question.id || '').trim() === target) {
        return question;
      }
    }
    return null;
  }

  function shouldShowPlanningQuestion(question, planningState) {
    if (!question) return false;
    var visibility = question.visible_when;
    if (!visibility || !visibility.question_id) return true;

    var formData = planningState && planningState.formData ? planningState.formData : {};
    var sourceValue = formData[visibility.question_id];
    var normalizedSource = normalizeToken(sourceValue);
    if (visibility.not_empty) {
      return Boolean(String(sourceValue || '').trim());
    }

    var anyOf = Array.isArray(visibility.any_of) ? visibility.any_of : [];
    if (anyOf.length > 0) {
      for (var i = 0; i < anyOf.length; i++) {
        if (normalizedSource === normalizeToken(anyOf[i])) {
          return true;
        }
      }
      return false;
    }

    return true;
  }

  function getPlanningOptionLabel(question, value) {
    var options = question && Array.isArray(question.options) ? question.options : [];
    var raw = String(value || '');
    for (var i = 0; i < options.length; i++) {
      var option = options[i] || {};
      if (String(option.value || '') === raw) {
        return String(option.label || raw).trim();
      }
    }
    return raw;
  }

  function getPlanningQuestionDisplayValue(question, value) {
    if (!question) return String(value || '').trim();
    if (question.type === 'select') {
      return getPlanningOptionLabel(question, value);
    }
    return String(value || '').trim();
  }

  function buildPlanningFormAnswerSummary(planningState) {
    if (!planningState || !planningState.schema) return '';

    var questions = getPlanningQuestions(planningState);
    var formData = planningState.formData || {};
    var uploads = planningState.uploads || {};
    var lines = [String(planningState.schema.title || 'Planning update').trim() + ':'];

    for (var i = 0; i < questions.length; i++) {
      var question = questions[i];
      if (!shouldShowPlanningQuestion(question, planningState)) continue;

      if (question.type === 'file') {
        if (uploads[question.id]) {
          lines.push('- ' + question.label + ': upload modal opened in this workspace');
        }
        continue;
      }

      var rawValue = formData[question.id];
      var displayValue = getPlanningQuestionDisplayValue(question, rawValue);
      if (displayValue) {
        lines.push('- ' + question.label + ': ' + displayValue);
      } else if (question.required) {
        lines.push('- ' + question.label + ': not answered yet');
      }
    }

    return lines.join('\n');
  }

  function buildStructuredPlanningFormPrompt(planningState) {
    if (!planningState || !planningState.schema) return '';

    var questions = getPlanningQuestions(planningState);
    var formData = planningState.formData || {};
    var uploads = planningState.uploads || {};
    var answers = [];
    var attachments = [];

    for (var i = 0; i < questions.length; i++) {
      var question = questions[i];
      if (!shouldShowPlanningQuestion(question, planningState)) continue;

      if (question.type === 'file') {
        attachments.push({
          id: question.id,
          label: question.label,
          attachment_kind: question.file_config && question.file_config.attachment_kind ? question.file_config.attachment_kind : question.id,
          upload_modal_opened: Boolean(uploads[question.id])
        });
        continue;
      }

      var rawValue = formData[question.id];
      answers.push({
        id: question.id,
        label: question.label,
        type: question.type,
        value: rawValue == null ? '' : rawValue,
        display_value: getPlanningQuestionDisplayValue(question, rawValue),
        required: Boolean(question.required)
      });
    }

    var payload = {
      form_id: planningState.schema.id || '',
      form_kind: planningState.schema.kind || '',
      form_title: planningState.schema.title || '',
      original_request: String(planningState.prompt || '').trim(),
      answers: answers,
      attachments: attachments
    };

    return [
      'Structured planning form submission:',
      JSON.stringify(payload, null, 2),
      '',
      'Follow-up instructions:',
      String(planningState.schema.submit_instructions || 'Use this structured planning intake to continue the conversation.').trim()
    ].join('\n');
  }

  function normalizePlanningFormSchema(schema) {
    if (!schema || typeof schema !== 'object') return null;

    return {
      id: String(schema.id || '').trim(),
      kind: String(schema.kind || '').trim(),
      title: String(schema.title || 'Planning Step').trim(),
      subtitle: String(schema.subtitle || '').trim(),
      summary: String(schema.summary || '').trim(),
      submit_label: String(schema.submit_label || '').trim(),
      submit_instructions: String(schema.submit_instructions || '').trim(),
      questions: Array.isArray(schema.questions) ? schema.questions : []
    };
  }

  function convertWorkflowFormToPlanningSchema(step) {
    if (!step || String(step.step_type || '').trim() !== 'ask_form') return null;
    var form = step.form;
    if (!form || typeof form !== 'object') return null;

    var fields = Array.isArray(form.fields) ? form.fields : [];
    var questions = [];
    for (var i = 0; i < fields.length; i++) {
      var field = fields[i] || {};
      questions.push({
        id: String(field.id || '').trim(),
        type: String(field.type || '').trim(),
        label: String(field.label || '').trim(),
        help_text: String(field.help_text || '').trim(),
        placeholder: String(field.placeholder || '').trim(),
        required: Boolean(field.required),
        rows: Number(field.rows || 0),
        options: Array.isArray(field.options) ? field.options : [],
        visible_when: field.visible_when || null,
        file_config: field.file_config || null
      });
    }

    return normalizePlanningFormSchema({
      id: String(form.id || step.step_id || '').trim(),
      kind: String(form.kind || '').trim(),
      title: String(form.title || step.title || 'Planning Step').trim(),
      subtitle: String(form.subtitle || step.preview_markdown || '').trim(),
      summary: String(form.summary || step.summary || '').trim(),
      submit_label: String(form.submit_label || '').trim(),
      submit_instructions: String(form.submit_instructions || '').trim(),
      questions: questions
    });
  }

  function buildWorkflowFormResponsePayload(planningState) {
    if (!planningState || !planningState.schema || !planningState.workflowStep) return null;

    var questions = getPlanningQuestions(planningState);
    var formData = planningState.formData || {};
    var uploads = planningState.uploads || {};
    var answers = [];
    var attachments = [];

    for (var i = 0; i < questions.length; i++) {
      var question = questions[i];
      if (!shouldShowPlanningQuestion(question, planningState)) continue;

      if (question.type === 'file') {
        attachments.push({
          id: question.id,
          label: question.label,
          attachment_kind: question.file_config && question.file_config.attachment_kind ? question.file_config.attachment_kind : question.id,
          upload_modal_opened: Boolean(uploads[question.id])
        });
        continue;
      }

      var rawValue = formData[question.id];
      answers.push({
        id: question.id,
        label: question.label,
        type: question.type,
        value: rawValue == null ? '' : rawValue,
        display_value: getPlanningQuestionDisplayValue(question, rawValue),
        required: Boolean(question.required)
      });
    }

    return {
      workflow_id: String(planningState.workflowStep.workflow_id || '').trim(),
      step_id: String(planningState.workflowStep.step_id || '').trim(),
      response_type: 'form',
      form: {
        form_id: planningState.schema.id || '',
        form_kind: planningState.schema.kind || '',
        form_title: planningState.schema.title || '',
        original_request: String(planningState.prompt || '').trim(),
        answers: answers,
        attachments: attachments
      }
    };
  }

  function activateHomeAssistantPlanningForm(schema, options) {
    var normalizedSchema = normalizePlanningFormSchema(schema);
    if (!normalizedSchema) return;

    clearHomeAssistantInlineReply();
    homeAssistantState.planningState = {
      kind: 'planning_form',
      prompt: String(options && options.prompt || '').trim(),
      routeContext: normalizeHomeRouteContext(options && options.routeContext),
      intent: options && options.intent ? options.intent : HOME_INTENTS.general_task,
      agentLabel: String(options && options.agentLabel || getWorkspaceHomeAssistantDisplayName()).trim() || getWorkspaceHomeAssistantDisplayName(),
      workflowStep: options && options.workflowStep ? options.workflowStep : null,
      schema: normalizedSchema,
      formData: {},
      uploads: {},
      submitting: false,
      focusField: ''
    };

    var questions = getPlanningQuestions(homeAssistantState.planningState);
    for (var i = 0; i < questions.length; i++) {
      var question = questions[i];
      homeAssistantState.planningState.formData[question.id] = question.default_value || '';
      if (!homeAssistantState.planningState.focusField && question.required) {
        homeAssistantState.planningState.focusField = question.id;
      }
    }

    homeAssistantState.conversationCollapsed = true;
    renderHomeAssistantPlanning();
  }

  function derivePlanningReviewNoteName(planningState) {
    var prompt = String(planningState && planningState.prompt || '').trim();
    if (/\bspain\b/i.test(prompt)) return 'Spain Trip Intake';
    if (planningState && planningState.schema && planningState.schema.kind === 'travel_intake') {
      return 'Travel Intake Summary';
    }
    return truncateText((planningState && planningState.schema && planningState.schema.title) || 'Planning Summary', 60);
  }

  function buildPlanningReviewNoteContent(planningState) {
    var lines = [
      '# ' + derivePlanningReviewNoteName(planningState),
      '',
      'Original request:',
      String(planningState && planningState.prompt || '').trim(),
      '',
      buildPlanningFormAnswerSummary(planningState)
    ];
    return lines.join('\n').trim();
  }

  function buildPlanningTaskDescriptionFromPrompt(prompt, fallbackTitle) {
    var normalized = String(prompt || '').trim()
      .replace(/^(please\s+)?help me\s+/i, '')
      .replace(/^(can|could|would|will)\s+you\s+/i, '')
      .replace(/^i (want|need)\s+(you\s+)?to\s+/i, '')
      .replace(/^please\s+/i, '')
      .replace(/\s+/g, ' ')
      .trim();

    if (!normalized) {
      normalized = String(fallbackTitle || 'Planning task').trim();
    }
    if (!normalized) {
      normalized = 'Planning task';
    }

    return truncateText(normalized.charAt(0).toUpperCase() + normalized.slice(1), 140);
  }

  function buildPlanningTaskDetails(prompt, summaryText, noteName) {
    var lines = [];
    var normalizedPrompt = String(prompt || '').trim();
    if (normalizedPrompt) {
      lines.push('Original request:');
      lines.push(normalizedPrompt);
    }

    var normalizedSummary = String(summaryText || '').trim();
    if (normalizedSummary) {
      if (lines.length > 0) lines.push('');
      lines.push('Planning intake:');
      lines.push(normalizedSummary);
    }

    if (String(noteName || '').trim()) {
      if (lines.length > 0) lines.push('');
      lines.push('Planning note: ' + String(noteName || '').trim());
    }

    return lines.join('\n').trim();
  }

  function buildPlanningReviewTaskDescription(planningState) { // eslint-disable-line no-unused-vars
    if (!planningState) return 'Planning task';
    return buildPlanningTaskDescriptionFromPrompt(
      planningState.prompt,
      planningState.schema && planningState.schema.title
    );
  }

  function buildPlanningReviewTaskDetails(planningState) {
    if (!planningState) return '';
    return buildPlanningTaskDetails(
      planningState.prompt,
      planningState.summaryText,
      planningState.noteSaved && planningState.noteSaved.name
    );
  }

  function buildPlanningTaskExecutionDetails(planningState) {
    if (!planningState) return '';

    var details = buildPlanningReviewTaskDetails(planningState);
    var structuredSubmission = String(planningState.dispatchPrompt || '').trim();
    if (!structuredSubmission) return details;

    return [details, structuredSubmission].filter(Boolean).join('\n\n').trim();
  }

  function buildPlanningReviewTaskContext(planningState) {
    if (!planningState) return {};

    var context = {
      planning_review_kind: String(planningState.schema && planningState.schema.kind || '').trim() || null,
      planning_review_title: String(planningState.schema && planningState.schema.title || '').trim() || null,
      planning_review_summary: String(planningState.summaryText || '').trim() || null,
      planning_note_name: String(planningState.noteSaved && planningState.noteSaved.name || '').trim() || null,
      planning_review_completed_at: new Date().toISOString(),
      human_loop: null,
      planning_latest_reply: null,
      planning_workflow_step: null,
      planning_session_id: null,
      user_assist_message: null,
      user_assist_choice: null
    };

    if (planningState.workflowResponse) {
      try {
        context.planning_workflow_response = JSON.stringify(planningState.workflowResponse, null, 2);
      } catch (_error) {
        context.planning_workflow_response = String(planningState.workflowResponse);
      }
    } else {
      context.planning_workflow_response = null;
    }

    return context;
  }

  function activateHomeAssistantPlanningReview(planningState) {
    if (!planningState || planningState.kind !== 'planning_form' || !planningState.schema) return;

    clearHomeAssistantInlineReply();
    homeAssistantState.planningState = {
      kind: 'planning_review',
      prompt: planningState.prompt,
      routeContext: normalizeHomeRouteContext(planningState.routeContext),
      intent: planningState.intent || HOME_INTENTS.general_task,
      agentLabel: planningState.agentLabel || getWorkspaceHomeAssistantDisplayName(),
      workflowStep: planningState.workflowStep || null,
      schema: planningState.schema,
      summaryText: buildPlanningFormAnswerSummary(planningState),
      dispatchPrompt: buildStructuredPlanningFormPrompt(planningState),
      workflowResponse: buildWorkflowFormResponsePayload(planningState),
      noteName: derivePlanningReviewNoteName(planningState),
      noteSaved: null,
      noteSaving: false,
      mainTask: null,
      mainTaskCreating: false,
      specialistStatuses: {},
      specialistBusy: '',
      continuing: false
    };

    homeAssistantState.conversationCollapsed = true;
    renderHomeAssistantPlanning();
  }

  function getPlanningReviewState() {
    var planningState = homeAssistantState.planningState;
    if (!planningState || planningState.kind !== 'planning_review') return null;
    return planningState;
  }

  function isTravelPlanningReviewState(planningState) {
    if (!planningState || planningState.kind !== 'planning_review' || !planningState.schema) return false;
    return String(planningState.schema.kind || '').trim() === 'travel_intake' ||
      String(planningState.intent && planningState.intent.key || '').trim() === 'travel_planning';
  }

  function isWorkspaceManagerMetaActionPrompt(prompt) {
    var normalized = normalizeToken(prompt);
    if (!normalized) return false;

    var actionMatch = /\b(save|create|add|attach|upload|import|export|move|rename|delete|remove|switch|assign|list|show|open|bind)\b/.test(normalized);
    if (!actionMatch) return false;

    return /\b(note|notes|task|tasks|subtask|subtasks|file|files|pdf|folder|folders|directory|directories|workspace|agent|agents|binding|bindings|canvas)\b/.test(normalized);
  }

  function detectWorkspacePlanningSpecialist(prompt, intent) {
    var normalized = normalizeToken(prompt);
    if (!normalized || isWorkspaceManagerMetaActionPrompt(normalized)) {
      return null;
    }

    var travelSignals = ['travel', 'trip', 'itinerary', 'hotel', 'flight', 'vacation', 'restaurant', 'restaurants', 'museum', 'museums', 'nightlife', 'day trip', 'day trips', 'accommodation', 'lodging', 'neighborhood', 'neighbourhood', 'budget'];
    var looksLikeTravel = travelSignals.some(function (signal) {
      return normalized.indexOf(normalizeToken(signal)) >= 0;
    });
    if (!looksLikeTravel && String(intent && intent.key || '').trim() !== 'travel_planning') {
      return null;
    }

    var bestConfig = null;
    var bestScore = 0;
    Object.keys(HOME_PLANNING_SPECIALISTS).forEach(function (specialistKey) {
      var config = HOME_PLANNING_SPECIALISTS[specialistKey];
      var score = 0;
      var phrases = Array.isArray(config && config.scorePhrases) ? config.scorePhrases : [];
      for (var i = 0; i < phrases.length; i++) {
        var phrase = normalizeToken(phrases[i]);
        if (!phrase || normalized.indexOf(phrase) === -1) continue;
        score += phrase.indexOf(' ') >= 0 ? 3 : 1;
      }
      if (score > bestScore) {
        bestScore = score;
        bestConfig = config;
      }
    });

    if (bestConfig) return bestConfig;
    return looksLikeTravel ? HOME_PLANNING_SPECIALISTS.travel_itinerary : null;
  }

  function getActiveLinkedPlanningTask() {
    if (homeAssistantState.inlineReplyState &&
        homeAssistantState.inlineReplyState.linkedTask &&
        homeAssistantState.inlineReplyState.linkedTask.id) {
      return homeAssistantState.inlineReplyState.linkedTask;
    }
    if (homeAssistantState.planningState &&
        homeAssistantState.planningState.mainTask &&
        homeAssistantState.planningState.mainTask.id) {
      return homeAssistantState.planningState.mainTask;
    }
    return null;
  }

  function normalizeCreatedPlanningTask(task) {
    if (!task || !task.id) return null;
    return {
      id: String(task.id || '').trim(),
      description: String(task.description || '').trim(),
      details: String(task.details || '').trim(),
      to: String(task.to || '').trim() || String(task.assigned_node_id || '')
        .replace(/-node-\d+$/, '')
        .trim()
    };
  }

  async function ensureWorkspacePlanningTask(routeContext, prompt, agentLabel, options) {
    var existingTask = options && options.existingTask && options.existingTask.id
      ? normalizeCreatedPlanningTask(options.existingTask)
      : null;
    if (existingTask) return existingTask;

    var workspaceId = hasWorkspaceRouteContext(routeContext)
      ? String(routeContext.workspace_id || '').trim()
      : '';
    if (!workspaceId) {
      throw new Error('Workspace context is required to create the planning task');
    }

    var response = await createWorkspaceTaskRecord(workspaceId, {
      description: buildPlanningTaskDescriptionFromPrompt(
        prompt,
        options && options.fallbackTitle ? options.fallbackTitle : 'Planning task'
      ),
      details: buildPlanningTaskDetails(
        prompt,
        options && options.summaryText ? options.summaryText : '',
        options && options.noteName ? options.noteName : ''
      ),
      to: String(agentLabel || '').trim()
    });
    var createdTask = response && response.task ? response.task : response;
    var normalizedTask = normalizeCreatedPlanningTask(createdTask);
    if (!normalizedTask || !normalizedTask.id) {
      throw new Error('Failed to create the main workspace task');
    }

    syncCreatedTaskIntoWorkspaceDetail(normalizedTask);
    await refreshWorkspaceDetailTaskPanels();
    return normalizedTask;
  }

  async function ensurePlanningReviewMainTask(planningState) {
    if (!planningState) throw new Error('Missing planning review state');
    if (planningState.mainTask && planningState.mainTask.id) {
      return planningState.mainTask;
    }

    planningState.mainTask = await ensureWorkspacePlanningTask(
      planningState.routeContext,
      planningState.prompt,
      planningState.agentLabel,
      {
        summaryText: planningState.summaryText,
        noteName: planningState.noteSaved && planningState.noteSaved.name,
        fallbackTitle: planningState.schema && planningState.schema.title
      }
    );
    return planningState.mainTask;
  }

  async function savePlanningReviewToNote() {
    var planningState = getPlanningReviewState();
    if (!planningState || planningState.noteSaving || planningState.noteSaved) return;

    var workspaceId = hasWorkspaceRouteContext(planningState.routeContext)
      ? String(planningState.routeContext.workspace_id || '').trim()
      : '';
    if (!workspaceId) {
      if (window.Toast) Toast.warning('Open this flow from a workspace before saving a note.');
      return;
    }

    planningState.noteSaving = true;
    renderHomeAssistantPlanning();

    try {
      var notePayload = {
        name: planningState.noteName || 'Planning Summary',
        content: buildPlanningReviewNoteContent(planningState)
      };
      var data = null;
      if (typeof API !== 'undefined' && typeof API.post === 'function') {
        data = await API.post('/api/workspaces/' + encodeURIComponent(workspaceId) + '/notes', notePayload);
      } else {
        var response = await fetch('/api/workspaces/' + encodeURIComponent(workspaceId) + '/notes', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(notePayload)
        });
        if (!response.ok) throw new Error('Failed to create note');
        data = await response.json();
      }

      var savedName = data && data.note && data.note.name
        ? String(data.note.name).trim()
        : notePayload.name;
      planningState.noteSaved = {
        id: data && data.note && data.note.id ? String(data.note.id).trim() : '',
        name: savedName
      };

      if (window.workspaceDetail &&
          String(window.workspaceDetail.workspaceId || '').trim() === workspaceId &&
          typeof window.workspaceDetail.loadWorkspace === 'function') {
        window.workspaceDetail.loadWorkspace();
      }

      appendHomeAssistantMessage('assistant', 'Saved the intake summary to note "' + savedName + '".');
      setHomeAssistantRoutingSummary('Planning Review', 'Intake summary saved to this workspace.');
    } catch (error) {
      dashLog.debug('Failed to save planning review note', { error: error && error.message || error });
      appendHomeAssistantMessage('assistant', 'I could not save the intake summary to a note right now.');
      setHomeAssistantRoutingSummary('Planning Review', 'Could not save the intake summary to a note.');
      if (window.Toast) Toast.error('Failed to save note');
    } finally {
      planningState.noteSaving = false;
      renderHomeAssistantPlanning();
    }
  }

  function findExactAgentByName(agents, agentName) {
    var target = normalizeToken(agentName);
    if (!target || !Array.isArray(agents)) return null;
    for (var i = 0; i < agents.length; i++) {
      var agentInfo = agents[i];
      var name = typeof agentInfo === 'string' ? agentInfo : agentInfo && agentInfo.name;
      if (normalizeToken(name) === target) return agentInfo;
    }
    return null;
  }

  async function createPlanningSpecialistAgent(config, planningState) {
    if (!config) throw new Error('Missing specialist configuration');
    if (typeof API === 'undefined' || typeof API.post !== 'function' || typeof API.get !== 'function') {
      throw new Error('Agent API unavailable');
    }

    var listData = await API.get('/api/agents');
    var existing = (listData && listData.agents) || [];
    var existingNames = [];
    for (var i = 0; i < existing.length; i++) {
      var agentInfo = existing[i];
      existingNames.push(typeof agentInfo === 'string' ? agentInfo : agentInfo.name);
    }

    var agentName = buildUniqueAgentName(config.agentName, existingNames);
    var payload = {
      name: agentName,
      type: config.type,
      system_prompt: config.systemPrompt,
      description: config.description + ' Workspace context: "' + truncateText(String(planningState.summaryText || '').trim(), 180) + '".',
      tags: uniqueValues((config.tags || []).concat(['workspace-specialist', 'planning-review']))
    };

    var selectedModel = await resolveAutoSelectedModel(payload.type, '');
    if (selectedModel) payload.model = selectedModel;

    if (isSemiAutoMode() || !payload.model) {
      var confirmation = await confirmAgentCreationWithModal(payload);
      if (!confirmation || confirmation.status !== 'created') {
        if (confirmation && confirmation.status === 'unavailable') {
          throw new Error('Create Agent modal unavailable');
        }
        throw new Error('Agent creation canceled');
      }
      return confirmation.agentName;
    }

    await API.post('/api/agents', payload);
    return agentName;
  }

  function buildWorkspaceSpecialistTaskDetails(prompt, managerLabel, config, agentName) {
    var sections = [];
    var normalizedPrompt = String(prompt || '').trim();
    var normalizedManager = String(managerLabel || getWorkspaceHomeAssistantDisplayName()).trim() || getWorkspaceHomeAssistantDisplayName();
    var normalizedAgent = String(agentName || config && config.label || '').trim();
    var handoffInstruction = String(config && config.handoffInstruction || '').trim();

    sections.push([
      'Workspace manager handoff:',
      normalizedManager + ' routed this workspace task to ' + normalizedAgent + '.'
    ].join('\n'));

    if (normalizedPrompt) {
      sections.push([
        'Original request:',
        normalizedPrompt
      ].join('\n'));
    }

    if (handoffInstruction) {
      sections.push([
        'Specialist goal:',
        handoffInstruction
      ].join('\n'));
    }

    return sections.join('\n\n').trim();
  }

  function buildWorkspaceSpecialistTaskContext(config, managerLabel) {
    return {
      planning_specialist_key: String(config && config.key || '').trim() || null,
      planning_specialist_label: String(config && config.label || '').trim() || null,
      planning_specialist_agent_name: String(config && config.agentName || '').trim() || null,
      planning_handoff_source: String(managerLabel || '').trim() || null,
      workspace_specialist_handoff: true,
      workspace_specialist_handoff_at: new Date().toISOString()
    };
  }

  function buildPlanningSpecialistTaskDescription(planningState, config) {
    var prefix = String(config && (config.taskTitle || config.label) || '').trim();
    var base = buildPlanningTaskDescriptionFromPrompt(
      planningState && planningState.prompt,
      prefix || (config && config.label) || 'Specialist task'
    );
    if (!prefix) return base;
    if (normalizeToken(base).indexOf(normalizeToken(prefix)) === 0) {
      return base;
    }
    return truncateText(prefix + ': ' + base, 140);
  }

  function buildPlanningSpecialistTaskDetails(planningState, config, agentName) {
    var sections = [];
    var assignee = String(agentName || config && config.label || '').trim();
    var managerLabel = String(planningState && planningState.agentLabel || getWorkspaceHomeAssistantDisplayName()).trim();
    var handoffInstruction = String(config && config.handoffInstruction || '').trim();
    var planningDetails = buildPlanningTaskExecutionDetails(planningState);

    sections.push([
      'Workspace manager handoff:',
      managerLabel + ' handed this travel-planning task to ' + assignee + '.'
    ].join('\n'));

    if (handoffInstruction) {
      sections.push([
        'Specialist goal:',
        handoffInstruction
      ].join('\n'));
    }

    if (planningDetails) {
      sections.push(planningDetails);
    }

    return sections.join('\n\n').trim();
  }

  function buildPlanningSpecialistTaskContext(planningState, specialistKey, config, agentName, parentTaskID) {
    var context = buildPlanningReviewTaskContext(planningState);
    context.planning_specialist_key = String(specialistKey || '').trim() || null;
    context.planning_specialist_label = String(config && config.label || '').trim() || null;
    context.planning_specialist_agent_name = String(agentName || '').trim() || null;
    context.planning_parent_task_id = String(parentTaskID || '').trim() || null;
    context.planning_handoff_source = String(planningState && planningState.agentLabel || '').trim() || null;
    return context;
  }

  function findWorkspaceDetailTaskById(taskId) {
    var normalizedTaskId = String(taskId || '').trim();
    if (!normalizedTaskId || !window.workspaceDetail || !Array.isArray(window.workspaceDetail.tasks)) {
      return null;
    }

    for (var i = 0; i < window.workspaceDetail.tasks.length; i++) {
      var task = window.workspaceDetail.tasks[i];
      if (task && String(task.id || '').trim() === normalizedTaskId) {
        return task;
      }
    }
    return null;
  }

  async function fetchWorkspaceTaskRecord(taskId) {
    var normalizedTaskId = String(taskId || '').trim();
    if (!normalizedTaskId) {
      throw new Error('Task ID is required');
    }

    if (typeof API !== 'undefined' && typeof API.get === 'function') {
      return await API.get('/api/orchestration/tasks?id=' + encodeURIComponent(normalizedTaskId));
    }

    var response = await fetch('/api/orchestration/tasks?id=' + encodeURIComponent(normalizedTaskId));
    if (!response.ok) {
      var text = '';
      try {
        text = await response.text();
      } catch (_error) {
        text = '';
      }
      throw new Error(text || 'Failed to load task');
    }
    return await response.json();
  }

  function updatePlanningSpecialistStatus(planningState, specialistKey, updates) {
    if (!planningState || !specialistKey) return null;
    var current = planningState.specialistStatuses[specialistKey] || {};
    planningState.specialistStatuses[specialistKey] = Object.assign({}, current, updates || {});
    return planningState.specialistStatuses[specialistKey];
  }

  function getPlanningSpecialistButtonLabel(planningState, specialistKey, config) {
    var status = planningState && planningState.specialistStatuses
      ? planningState.specialistStatuses[specialistKey] || null
      : null;

    if (planningState && planningState.specialistBusy === specialistKey) {
      if (status && status.taskId) {
        return 'Opening ' + config.label + ' Task...';
      }
      if (status && status.agentName) {
        return 'Handing Off To ' + status.agentName + '...';
      }
      return 'Creating ' + config.label + '...';
    }

    if (status && status.taskId) {
      return 'Open ' + config.label + ' Task';
    }
    if (status && status.agentName) {
      return 'Handoff To ' + status.agentName;
    }
    return 'Create ' + config.label + ' + Handoff';
  }

  async function findPlanningSpecialistTask(planningState, specialistKey, agentName) {
    if (!planningState || !specialistKey) return null;

    var status = planningState.specialistStatuses[specialistKey] || null;
    if (status && status.taskId) {
      var statusTask = findWorkspaceDetailTaskById(status.taskId);
      if (statusTask) return statusTask;
      try {
        var fetchedStatusTask = await fetchWorkspaceTaskRecord(status.taskId);
        if (fetchedStatusTask && fetchedStatusTask.id) {
          syncUpdatedTaskIntoWorkspaceDetail(fetchedStatusTask);
          return fetchedStatusTask;
        }
      } catch (_error) {
        // fall through to workspace search
      }
    }

    var mainTaskId = String(planningState.mainTask && planningState.mainTask.id || '').trim();
    if (!mainTaskId || !window.workspaceDetail || !Array.isArray(window.workspaceDetail.tasks)) {
      return null;
    }

    var targetAgent = normalizeToken(agentName || status && status.agentName || '');
    for (var i = 0; i < window.workspaceDetail.tasks.length; i++) {
      var task = window.workspaceDetail.tasks[i];
      if (!task || String(task.parent_task_id || '').trim() !== mainTaskId) continue;

      var contextKey = normalizeToken(task.context && task.context.planning_specialist_key);
      if (contextKey && contextKey === normalizeToken(specialistKey)) {
        return task;
      }
      if (targetAgent && normalizeToken(task.to) === targetAgent) {
        return task;
      }
    }

    return null;
  }

  async function ensurePlanningReviewSpecialistTask(planningState, mainTask, specialistKey, config, agentName) {
    if (!planningState || !mainTask || !mainTask.id || !config || !agentName) {
      throw new Error('Missing specialist task context');
    }

    var existingTask = await findPlanningSpecialistTask(planningState, specialistKey, agentName);
    if (existingTask && existingTask.id) {
      updatePlanningSpecialistStatus(planningState, specialistKey, {
        status: 'task_ready',
        agentName: agentName,
        taskId: String(existingTask.id || '').trim(),
        taskStatus: String(existingTask.status || '').trim(),
        taskDescription: String(existingTask.description || '').trim()
      });
      return existingTask;
    }

    var workspaceId = hasWorkspaceRouteContext(planningState.routeContext)
      ? String(planningState.routeContext.workspace_id || '').trim()
      : '';
    if (!workspaceId) {
      throw new Error('Workspace context is required to hand off to a specialist');
    }

    var createdResponse = await createWorkspaceTaskRecord(workspaceId, {
      from: String(planningState.agentLabel || '').trim(),
      to: String(agentName || '').trim(),
      description: buildPlanningSpecialistTaskDescription(planningState, config),
      details: buildPlanningSpecialistTaskDetails(planningState, config, agentName),
      parent_task_id: String(mainTask.id || '').trim(),
      subtask_index: Number.isFinite(Number(config.subtaskIndex)) ? Number(config.subtaskIndex) : undefined
    });
    var createdTask = createdResponse && createdResponse.task ? createdResponse.task : createdResponse;
    if (!createdTask || !createdTask.id) {
      throw new Error('Failed to create the specialist task');
    }

    syncCreatedTaskIntoWorkspaceDetail(createdTask);

    var updatedTask = await updateWorkspaceTaskRecord(createdTask.id, {
      context: buildPlanningSpecialistTaskContext(planningState, specialistKey, config, agentName, mainTask.id)
    });
    syncUpdatedTaskIntoWorkspaceDetail(updatedTask);
    await refreshWorkspaceDetailTaskPanels();

    updatePlanningSpecialistStatus(planningState, specialistKey, {
      status: 'task_ready',
      agentName: agentName,
      taskId: String(updatedTask.id || '').trim(),
      taskStatus: String(updatedTask.status || '').trim(),
      taskDescription: String(updatedTask.description || '').trim()
    });

    return updatedTask;
  }

  async function openPlanningSpecialistTask(task, routeContext) {
    if (!task || !task.id) {
      throw new Error('Specialist task is missing');
    }

    var latestTask = task;
    try {
      var fetchedTask = await fetchWorkspaceTaskRecord(task.id);
      if (fetchedTask && fetchedTask.id) {
        latestTask = fetchedTask;
        syncUpdatedTaskIntoWorkspaceDetail(fetchedTask);
      }
    } catch (_error) {
      // Keep the local task object if the refresh fails.
    }

    var detail = window.workspaceDetail;
    var targetWorkspaceId = String(
      routeContext && routeContext.workspace_id ||
      latestTask.workspace_id ||
      latestTask.folder_id ||
      ''
    ).trim();
    var detailWorkspaceId = String(detail && (detail.workspaceId || detail.workspace && detail.workspace.id) || '').trim();
    var canUseWorkspaceDetail = Boolean(
      detail &&
      (!targetWorkspaceId || !detailWorkspaceId || targetWorkspaceId === detailWorkspaceId)
    );

    await dismissHomeAssistantThinkingModalForTaskLaunch();

    try {
      if (canUseWorkspaceDetail) {
        var detailTask = findWorkspaceDetailTaskById(latestTask.id) || latestTask;
        var humanLoop = detailTask.context && detailTask.context.human_loop;
        var blocked = humanLoop && String(humanLoop.state || '').trim().toLowerCase() === 'blocked';
        var normalizedStatus = String(detailTask.status || '').trim().toLowerCase();

        if (blocked && typeof detail.openTaskAssistModal === 'function') {
          detail.openTaskAssistModal(detailTask.id);
          return detailTask;
        }
        if ((normalizedStatus === 'completed' || normalizedStatus === 'failed' || normalizedStatus === 'cancelled' || normalizedStatus === 'timeout') &&
            typeof detail.showTaskResult === 'function') {
          detail.showTaskResult(detailTask.id);
          return detailTask;
        }
        if (normalizedStatus === 'in_progress' && typeof detail.openTaskExecutionModal === 'function') {
          detail.openTaskExecutionModal(detailTask);
          if (typeof detail.startExecutionMonitor === 'function') {
            detail.startExecutionMonitor(detailTask.id);
          }
          return detailTask;
        }
        if (typeof detail.executeTask === 'function') {
          await detail.executeTask(detailTask.id, { skipConfirm: true });
          return findWorkspaceDetailTaskById(detailTask.id) || detailTask;
        }
      }

      var fallbackStatus = String(latestTask.status || '').trim().toLowerCase();
      if (!fallbackStatus || fallbackStatus === 'pending' || fallbackStatus === 'assigned') {
        await executeWorkspaceTaskRecord(latestTask.id);
      }
      return latestTask;
    } catch (error) {
      openHomeAssistantThinkingModal();
      throw error;
    }
  }

  async function addPlanningReviewSpecialist(specialistKey) {
    var planningState = getPlanningReviewState();
    var config = HOME_PLANNING_SPECIALISTS[specialistKey];
    if (!planningState || !config) return;

    var existingStatus = planningState.specialistStatuses[specialistKey];
    if (planningState.specialistBusy) return;

    planningState.specialistBusy = specialistKey;
    renderHomeAssistantPlanning();

    try {
      if (existingStatus && existingStatus.taskId) {
        var existingTask = await findPlanningSpecialistTask(planningState, specialistKey, existingStatus.agentName);
        if (existingTask && existingTask.id) {
          await openPlanningSpecialistTask(existingTask, planningState.routeContext);
          clearHomeAssistantTaskLaunchState();
          return;
        }
      }

      var agents = await fetchAgentsForMatching();
      var existingAgent = findExactAgentByName(agents, config.agentName);
      var agentName = existingAgent
        ? (typeof existingAgent === 'string' ? existingAgent : existingAgent.name)
        : await createPlanningSpecialistAgent(config, planningState);

      var addedToWorkspace = await addAgentToWorkspaceIfNeeded(agentName, planningState.routeContext);
      if (!addedToWorkspace) {
        throw new Error('Failed to attach specialist to workspace');
      }

      updatePlanningSpecialistStatus(planningState, specialistKey, {
        status: 'ready',
        agentName: agentName,
        created: !existingAgent
      });

      var mainTask = await preparePlanningReviewMainTaskForExecution(planningState);
      var specialistTask = await ensurePlanningReviewSpecialistTask(
        planningState,
        mainTask,
        specialistKey,
        config,
        agentName
      );

      appendHomeAssistantMessage(
        'assistant',
        (existingAgent ? 'Added ' : 'Created and added ') + '"' + agentName + '" to this workspace, then handed off a specialist task.'
      );
      setHomeAssistantRoutingSummary(config.label, '"' + agentName + '" is handling a workspace task now.');
      await openPlanningSpecialistTask(specialistTask, planningState.routeContext);
      clearHomeAssistantTaskLaunchState();
    } catch (error) {
      dashLog.debug('Failed to add planning specialist', {
        specialistKey: specialistKey,
        error: error && error.message || error
      });
      appendHomeAssistantMessage('assistant', 'I could not hand off to "' + config.label + '" right now.');
      setHomeAssistantRoutingSummary(config.label, 'Could not create the specialist handoff task right now.');
    } finally {
      planningState.specialistBusy = '';
      renderHomeAssistantPlanning();
    }
  }

  async function continuePlanningReviewWithManager() {
    var planningState = getPlanningReviewState();
    if (!planningState || planningState.continuing) return;

    planningState.continuing = true;
    planningState.mainTaskCreating = false;
    renderHomeAssistantPlanning();

    try {
      if (!planningState.mainTask || !planningState.mainTask.id) {
        planningState.mainTaskCreating = true;
        renderHomeAssistantPlanning();
        setHomeAssistantBusy(true, 'Adding Main Task...');
        setHomeAssistantRoutingSummary(
          'Planning Review',
          'Adding the main task to this workspace before the planning subtasks continue.'
        );

        var createdTask = await ensurePlanningReviewMainTask(planningState);
        if (createdTask && createdTask.description) {
          appendHomeAssistantMessage('assistant', 'Added main workspace task "' + createdTask.description + '".');
        }
      }

      planningState.mainTaskCreating = false;
      renderHomeAssistantPlanning();
      setHomeAssistantBusy(true, 'Starting Task...');
      setHomeAssistantRoutingSummary(
        planningState.agentLabel,
        isTravelPlanningReviewState(planningState)
          ? 'Starting the workspace task with the workspace manager using the reviewed intake summary.'
          : 'Starting the workspace task using the reviewed intake summary.'
      );

      var preparedTask = await preparePlanningReviewMainTaskForExecution(planningState);
      await launchWorkspaceTaskExecutionFromHomeAssistant(preparedTask, planningState.routeContext);
      clearHomeAssistantTaskLaunchState();
    } catch (error) {
      dashLog.debug('Planning review handoff failed', { error: error && error.message || error });
      planningState.mainTaskCreating = false;
      if (planningState.mainTask && planningState.mainTask.id) {
        appendHomeAssistantMessage('assistant', 'I could not start the workspace task right now.');
        setHomeAssistantRoutingSummary('Planning Review', 'Could not start the workspace task right now.');
      } else {
        appendHomeAssistantMessage('assistant', 'I could not add the main workspace task right now.');
        setHomeAssistantRoutingSummary('Planning Review', 'Could not add the main workspace task right now.');
      }
    } finally {
      planningState.mainTaskCreating = false;
      planningState.continuing = false;
      setHomeAssistantBusy(false);
      renderHomeAssistantPlanning();
    }
  }

  function clearHomeAssistantPlanning() {
    homeAssistantState.planningState = null;
    if (!homeAssistantState.inlineReplyState) {
      homeAssistantState.conversationCollapsed = false;
    }
    renderHomeAssistantPlanning();
  }

  function clearHomeAssistantInlineReply() {
    homeAssistantState.inlineReplyState = null;
    if (!homeAssistantState.planningState) {
      homeAssistantState.conversationCollapsed = false;
    }
    renderHomeAssistantInlineReply();
  }

  function buildHomeAssistantInlineReplyPlaceholder(intent, agentLabel) {
    if (intent && intent.key === 'travel_planning') {
      return 'Example: 1A, 2C, 3B, 4B, and I care about food and museums.';
    }
    return 'Reply to ' + (agentLabel || getWorkspaceHomeAssistantDisplayName()) + '...';
  }

  function getInlineReplyWorkspaceName(routeContext) {
    var workspaceId = String(routeContext && routeContext.workspace_id || '').trim();
    var detailWorkspace = window.workspaceDetail && window.workspaceDetail.workspace;
    if (detailWorkspace && String(detailWorkspace.id || '').trim() === workspaceId) {
      return String(detailWorkspace.name || '').trim();
    }
    var crumb = document.getElementById('workspace-breadcrumb-name');
    return crumb ? String(crumb.textContent || '').trim() : '';
  }

  function cleanInlineReplyQuestion(text) {
    return String(text || '')
      .replace(/^[\s>*-]+/, '')
      .replace(/^\d+[.)]\s*/, '')
      .replace(/\*\*/g, '')
      .trim();
  }

  function cleanInlineReplyChoiceText(text) {
    return String(text || '')
      .replace(/^\s*[-*]\s*/, '')
      .replace(/^\s*\d+[.)]\s*/, '')
      .replace(/\*\*/g, '')
      .replace(/`/g, '')
      .replace(/\[(.*?)\]\((.*?)\)/g, '$1')
      .replace(/\s+/g, ' ')
      .trim();
  }

  function buildInlineReplyChoiceId(number, label) {
    var normalized = normalizeToken(label).replace(/[^a-z0-9]+/g, '_').replace(/^_+|_+$/g, '');
    if (!normalized) normalized = 'option';
    return 'choice_' + String(number || 'x').trim() + '_' + normalized.slice(0, 48);
  }

  function extractInlineReplyQuestions(text) {
    var lines = String(text || '').split(/\r?\n/);
    var questions = [];
    var seen = Object.create(null);

    for (var i = 0; i < lines.length; i++) {
      var line = cleanInlineReplyQuestion(lines[i]);
      if (!line || line.length < 8) continue;
      if (line.charAt(line.length - 1) !== '?') continue;

      var normalized = normalizeToken(line);
      if (seen[normalized]) continue;
      seen[normalized] = true;
      questions.push(line);
      if (questions.length >= 4) break;
    }

    return questions;
  }

  function extractInlineReplyChoices(text) {
    var lines = String(text || '').split(/\r?\n/);
    var cueIndex = -1;
    var cues = ['next steps', 'next step', 'reply with your preference', 'choose one', 'choose the next step'];

    for (var i = 0; i < lines.length; i++) {
      var cleaned = normalizeToken(cleanInlineReplyChoiceText(lines[i]));
      for (var c = 0; c < cues.length; c++) {
        if (cleaned.indexOf(cues[c]) !== -1) {
          cueIndex = i;
          break;
        }
      }
      if (cueIndex !== -1) break;
    }

    if (cueIndex === -1) return [];

    var choices = [];
    var started = false;
    for (var lineIndex = cueIndex + 1; lineIndex < lines.length; lineIndex++) {
      var rawLine = String(lines[lineIndex] || '');
      var match = rawLine.match(/^\s*(\d+)[.)]\s*(.+)$/);
      if (match) {
        var choiceNumber = String(match[1] || '').trim();
        var choiceLabel = cleanInlineReplyChoiceText(match[2]);
        if (!choiceLabel) continue;
        choices.push({
          id: buildInlineReplyChoiceId(choiceNumber, choiceLabel),
          label: choiceLabel,
          number: choiceNumber
        });
        started = true;
        if (choices.length >= 5) break;
        continue;
      }

      if (!started) continue;
      if (!rawLine.trim()) continue;
      break;
    }

    return choices.length >= 2 ? choices : [];
  }

  function buildSyntheticInlineReplyWorkflowStep(latestReplyText, routeContext) {
    var choices = extractInlineReplyChoices(latestReplyText);
    if (!choices.length) return null;

    var normalizedContext = normalizeHomeRouteContext(routeContext);
    var sessionId = String(normalizedContext && normalizedContext.session_id || '').trim() || 'inline';
    return {
      workflow_id: 'workflow:' + sessionId + ':inline-choice',
      step_id: 'step:inline-choice',
      step_type: 'ask_choice',
      title: 'Choose the next step',
      summary: 'Pick one option below to continue the plan.',
      choices: choices,
      free_text_allowed: false
    };
  }

  function buildInlineReplyWorkflowResponse(replyState, text) {
    if (!replyState || !replyState.workflowStep) return null;

    var step = replyState.workflowStep;
    if (String(step.step_type || '').trim() !== 'ask_choice') return null;
    if (!replyState.selectedChoiceId) return null;

    return {
      workflow_id: String(step.workflow_id || '').trim(),
      step_id: String(step.step_id || '').trim(),
      response_type: 'choice',
      choice_id: String(replyState.selectedChoiceId || '').trim(),
      choice_label: String(replyState.selectedChoiceLabel || '').trim(),
      choice_number: String(replyState.selectedChoiceNumber || '').trim(),
      text: String(text || '').trim()
    };
  }

  function enableHomeAssistantInlineReply(routeContext, intent, agentLabel, latestReplyText, workflowStep, linkedTask) {
    var normalizedReply = String(latestReplyText || '').trim();
    var resolvedWorkflowStep = workflowStep && String(workflowStep.step_type || '').trim() === 'ask_choice'
      ? workflowStep
      : buildSyntheticInlineReplyWorkflowStep(normalizedReply, routeContext);
    var questionPrompts = extractInlineReplyQuestions(normalizedReply);
    var hasChoiceStep = resolvedWorkflowStep &&
      String(resolvedWorkflowStep.step_type || '').trim() === 'ask_choice' &&
      Array.isArray(resolvedWorkflowStep.choices) &&
      resolvedWorkflowStep.choices.length > 0;

    if (!hasChoiceStep && questionPrompts.length === 0) {
      homeAssistantState.inlineReplyState = null;
      if (!homeAssistantState.planningState) {
        homeAssistantState.conversationCollapsed = false;
      }
      renderHomeAssistantInlineReply();
      return null;
    }

    homeAssistantState.inlineReplyState = {
      routeContext: normalizeHomeRouteContext(routeContext),
      intent: intent || HOME_INTENTS.general_task,
      agentLabel: String(agentLabel || getWorkspaceHomeAssistantDisplayName()).trim() || getWorkspaceHomeAssistantDisplayName(),
      latestReplyText: normalizedReply,
      workflowStep: resolvedWorkflowStep,
      questionPrompts: questionPrompts,
      linkedTask: linkedTask && linkedTask.id ? linkedTask : getActiveLinkedPlanningTask(),
      selectedChoiceId: '',
      selectedChoiceLabel: '',
      selectedChoiceNumber: '',
      draft: '',
      submitting: false
    };
    homeAssistantState.conversationCollapsed = true;
    renderHomeAssistantInlineReply();
    return homeAssistantState.inlineReplyState;
  }

  function openWorkspacePlanningFileUpload(questionId) {
    var planningState = homeAssistantState.planningState;
    if (!planningState || planningState.kind !== 'planning_form') return;

    var question = findPlanningQuestionById(planningState, questionId);
    if (!question || question.type !== 'file') return;

    var statusText = question.file_config && question.file_config.opened_status
      ? String(question.file_config.opened_status).trim()
      : 'Upload modal opened for this planning step.';

    try {
      if (window.workspaceDetail && typeof window.workspaceDetail.showFileModal === 'function') {
        window.workspaceDetail.showFileModal();
      } else if (window.WorkspaceHubFiles && typeof window.WorkspaceHubFiles.openAddFileModal === 'function') {
        window.WorkspaceHubFiles.openAddFileModal();
      } else {
        throw new Error('File upload modal unavailable');
      }

      planningState.uploads[question.id] = true;
      setHomeAssistantRoutingSummary(planningState.schema.title || 'Planning Step', statusText);
      renderHomeAssistantPlanning();
    } catch (error) {
      dashLog.debug('Failed to open workspace planning upload modal', {
        questionId: questionId,
        error: error && error.message || error
      });
      if (window.Toast) {
        Toast.error('File upload modal is unavailable right now.');
      }
      setHomeAssistantRoutingSummary(planningState.schema.title || 'Planning Step', 'Could not open the upload modal right now.');
    }
  }

  function validatePlanningFormState(planningState) {
    var questions = getPlanningQuestions(planningState);
    var formData = planningState && planningState.formData ? planningState.formData : {};

    for (var i = 0; i < questions.length; i++) {
      var question = questions[i];
      if (!question || !question.required || !shouldShowPlanningQuestion(question, planningState)) continue;
      if (question.type === 'file') continue;

      var value = formData[question.id];
      if (!String(value || '').trim()) {
        return question;
      }
    }

    return null;
  }

  function renderHomeAssistantPlanning() {
    var els = getHomeAssistantElements();
    var container = els.planning;
    if (!container) return;

    var planningState = homeAssistantState.planningState;
    container.innerHTML = '';
    if (!planningState || !planningState.schema || (planningState.kind !== 'planning_form' && planningState.kind !== 'planning_review')) {
      container.classList.add('d-none');
      syncHomeAssistantLauncher();
      if (!homeAssistantState.busy) {
        closeHomeAssistantThinkingModal();
      }
      return;
    }

    if (planningState.kind === 'planning_review') {
      var specialistFirstReview = isTravelPlanningReviewState(planningState);
      var isReviewBusy = Boolean(
        homeAssistantState.busy ||
        planningState.noteSaving ||
        planningState.mainTaskCreating ||
        planningState.specialistBusy ||
        planningState.continuing
      );

      var reviewCard = document.createElement('div');
      reviewCard.className = 'home-assistant-planning-card';

      var reviewEyebrow = document.createElement('div');
      reviewEyebrow.className = 'home-assistant-planning-eyebrow';
      reviewEyebrow.textContent = 'Planning Review';
      reviewCard.appendChild(reviewEyebrow);

      var reviewTitle = document.createElement('div');
      reviewTitle.className = 'home-assistant-planning-title';
      reviewTitle.textContent = planningState.schema.title || 'Review Intake Summary';
      reviewCard.appendChild(reviewTitle);

      var reviewSubtitle = document.createElement('p');
      reviewSubtitle.className = 'home-assistant-planning-subtitle';
      if (specialistFirstReview) {
        reviewSubtitle.textContent = planningState.mainTask && planningState.mainTask.description
          ? 'Review the intake summary, then add the right specialist to the workspace. Keep it with the workspace manager only if you want a lightweight follow-up.'
          : 'Review the intake summary, then add the right specialist to the workspace. The main task will be added before specialist work begins.';
      } else {
        reviewSubtitle.textContent = planningState.mainTask && planningState.mainTask.description
          ? 'Review the summary, save it to a note if you want, and continue when you are ready. The main task is already in the workspace.'
          : 'Review the summary, save it to a note if you want, and continue when you are ready. The main task will be added to the workspace before subtasks begin.';
      }
      reviewCard.appendChild(reviewSubtitle);

      var reviewSummary = document.createElement('div');
      reviewSummary.className = 'home-assistant-planning-summary';
      reviewSummary.textContent = planningState.summaryText || 'No planning summary available.';
      reviewCard.appendChild(reviewSummary);

      if (planningState.noteSaved && planningState.noteSaved.name) {
        var noteStatus = document.createElement('div');
        noteStatus.className = 'home-assistant-planning-help';
        noteStatus.textContent = 'Saved to workspace note "' + planningState.noteSaved.name + '".';
        reviewCard.appendChild(noteStatus);
      }

      if (planningState.mainTask && planningState.mainTask.description) {
        var taskStatus = document.createElement('div');
        taskStatus.className = 'home-assistant-planning-help';
        taskStatus.textContent = 'Added main workspace task "' + planningState.mainTask.description + '".';
        reviewCard.appendChild(taskStatus);
      }

      var noteActions = document.createElement('div');
      noteActions.className = 'home-assistant-planning-actions';

      var saveNoteButton = document.createElement('button');
      saveNoteButton.type = 'button';
      saveNoteButton.className = 'modern-btn modern-btn-secondary';
      saveNoteButton.disabled = isReviewBusy || Boolean(planningState.noteSaved);
      saveNoteButton.textContent = planningState.noteSaving
        ? 'Saving Note...'
        : (planningState.noteSaved && planningState.noteSaved.name
            ? 'Saved To "' + planningState.noteSaved.name + '"'
            : 'Save Summary To Note');
      saveNoteButton.addEventListener('click', function () {
        savePlanningReviewToNote();
      });
      noteActions.appendChild(saveNoteButton);

      var continueButton = document.createElement('button');
      continueButton.type = 'button';
      continueButton.className = specialistFirstReview
        ? 'modern-btn modern-btn-secondary'
        : 'modern-btn modern-btn-primary';
      continueButton.disabled = isReviewBusy;
      continueButton.textContent = planningState.mainTaskCreating
        ? 'Adding Main Task...'
        : planningState.continuing
        ? 'Continuing...'
        : specialistFirstReview
        ? ('Keep Task With ' + planningState.agentLabel)
        : ('Start Task With ' + planningState.agentLabel);
      continueButton.addEventListener('click', function () {
        continuePlanningReviewWithManager();
      });
      noteActions.appendChild(continueButton);
      reviewCard.appendChild(noteActions);

      var specialistHelp = document.createElement('div');
      specialistHelp.className = 'home-assistant-planning-help';
      specialistHelp.style.marginTop = '0.35rem';
      specialistHelp.textContent = specialistFirstReview
        ? 'Recommended: hand off full planning to a specialist task now. Use the workspace manager only for lighter follow-ups.'
        : 'Create specialist tasks now, or open an existing specialist task.';
      reviewCard.appendChild(specialistHelp);

      var specialistActions = document.createElement('div');
      specialistActions.className = 'home-assistant-planning-actions';

      Object.keys(HOME_PLANNING_SPECIALISTS).forEach(function (specialistKey) {
        var config = HOME_PLANNING_SPECIALISTS[specialistKey];
        var specialistButton = document.createElement('button');
        specialistButton.type = 'button';
        specialistButton.className = specialistFirstReview && specialistKey === 'travel_itinerary'
          ? 'modern-btn modern-btn-primary'
          : 'modern-btn modern-btn-secondary';
        specialistButton.disabled = isReviewBusy && planningState.specialistBusy !== specialistKey;
        if (planningState.specialistBusy === specialistKey) {
          specialistButton.disabled = true;
        }
        specialistButton.textContent = getPlanningSpecialistButtonLabel(planningState, specialistKey, config);
        specialistButton.addEventListener('click', function () {
          addPlanningReviewSpecialist(specialistKey);
        });
        specialistActions.appendChild(specialistButton);
      });

      var askAnotherButton = document.createElement('button');
      askAnotherButton.type = 'button';
      askAnotherButton.className = 'modern-btn modern-btn-secondary';
      askAnotherButton.disabled = isReviewBusy;
      askAnotherButton.textContent = 'Ask Another Task';
      askAnotherButton.addEventListener('click', function () {
        focusHomeAssistantInput();
      });
      specialistActions.appendChild(askAnotherButton);

      reviewCard.appendChild(specialistActions);
      container.appendChild(reviewCard);
      container.classList.remove('d-none');
      syncHomeAssistantLauncher();
      openHomeAssistantThinkingModal();
      return;
    }

    var formData = planningState.formData || {};
    var uploads = planningState.uploads || {};
    var isSubmitting = Boolean(planningState.submitting || homeAssistantState.busy);
    var card = document.createElement('div');
    card.className = 'home-assistant-planning-card';

    var eyebrow = document.createElement('div');
    eyebrow.className = 'home-assistant-planning-eyebrow';
    eyebrow.textContent = 'Planning Step';
    card.appendChild(eyebrow);

    var title = document.createElement('div');
    title.className = 'home-assistant-planning-title';
    title.textContent = planningState.schema.title || 'Planning Step';
    card.appendChild(title);

    if (planningState.schema.subtitle) {
      var subtitle = document.createElement('p');
      subtitle.className = 'home-assistant-planning-subtitle';
      subtitle.textContent = planningState.schema.subtitle;
      card.appendChild(subtitle);
    }

    if (planningState.schema.summary) {
      var summary = document.createElement('div');
      summary.className = 'home-assistant-planning-summary';
      summary.textContent = planningState.schema.summary;
      card.appendChild(summary);
    }

    var form = document.createElement('form');
    form.className = 'home-assistant-planning-form';
    form.noValidate = true;

    function addFieldLabel(field, question) {
      var label = document.createElement('label');
      label.className = 'home-assistant-planning-label';
      label.textContent = question.label || '';
      field.appendChild(label);
      if (question.help_text) {
        var help = document.createElement('div');
        help.className = 'home-assistant-planning-help';
        help.textContent = question.help_text;
        field.appendChild(help);
      }
    }

    function buildChoiceGroup(question, selectedValue) {
      var wrapper = document.createElement('div');
      wrapper.className = 'home-assistant-planning-choice-group';
      wrapper.setAttribute('data-planning-focus', question.id);

      var options = Array.isArray(question.options) ? question.options : [];
      for (var i = 0; i < options.length; i++) {
        var optionConfig = options[i] || {};
        var optionValue = String(optionConfig.value || '');
        if (!optionValue) continue;

        var choice = document.createElement('button');
        choice.type = 'button';
        choice.className = 'home-assistant-planning-choice';
        choice.disabled = isSubmitting;
        choice.classList.toggle('is-selected', optionValue === String(selectedValue || ''));
        choice.setAttribute('aria-pressed', optionValue === String(selectedValue || '') ? 'true' : 'false');
        choice.addEventListener('click', function (event) {
          if (!homeAssistantState.planningState) return;
          homeAssistantState.planningState.formData[question.id] = event.currentTarget.dataset.value;
          renderHomeAssistantPlanning();
        });
        choice.dataset.value = optionValue;

        var choiceLabel = document.createElement('span');
        choiceLabel.className = 'home-assistant-planning-choice-label';
        choiceLabel.textContent = optionConfig.label || optionValue;
        choice.appendChild(choiceLabel);

        wrapper.appendChild(choice);
      }

      return wrapper;
    }

    function buildQuestionField(question) {
      var field = document.createElement('div');
      field.className = question.type === 'file'
        ? 'home-assistant-planning-upload'
        : 'home-assistant-planning-field';

      if (question.type === 'file') {
        var uploadText = document.createElement('p');
        uploadText.className = 'home-assistant-planning-upload-text';
        uploadText.textContent = question.help_text || question.label || '';
        if (uploads[question.id]) {
          var uploadStatus = document.createElement('span');
          uploadStatus.className = 'home-assistant-planning-upload-status';
          uploadStatus.textContent = question.file_config && question.file_config.opened_status
            ? question.file_config.opened_status
            : 'Upload modal opened for this file.';
          uploadText.appendChild(document.createElement('br'));
          uploadText.appendChild(uploadStatus);
        }

        var uploadButton = document.createElement('button');
        uploadButton.type = 'button';
        uploadButton.className = 'modern-btn modern-btn-secondary';
        uploadButton.textContent = question.file_config && question.file_config.button_label
          ? question.file_config.button_label
          : (question.label || 'Attach File');
        uploadButton.disabled = isSubmitting;
        uploadButton.addEventListener('click', function () {
          openWorkspacePlanningFileUpload(question.id);
        });

        field.appendChild(uploadText);
        field.appendChild(uploadButton);
        return field;
      }

      addFieldLabel(field, question);

      if (question.type === 'select') {
        field.classList.add('is-choice-field');
        field.appendChild(buildChoiceGroup(question, formData[question.id]));
        if (!String(formData[question.id] || '').trim()) {
          var choiceHint = document.createElement('div');
          choiceHint.className = 'home-assistant-planning-choice-hint';
          choiceHint.textContent = 'Choose one option to continue.';
          field.appendChild(choiceHint);
        }
        return field;
      }

      if (question.type === 'textarea') {
        var textarea = document.createElement('textarea');
        textarea.className = 'home-assistant-planning-textarea';
        textarea.name = question.id;
        textarea.disabled = isSubmitting;
        textarea.required = Boolean(question.required);
        textarea.rows = question.rows > 0 ? question.rows : 3;
        textarea.value = formData[question.id] || '';
        textarea.placeholder = question.placeholder || '';
        textarea.addEventListener('input', function (event) {
          if (!homeAssistantState.planningState) return;
          homeAssistantState.planningState.formData[question.id] = event.target.value;
        });
        field.appendChild(textarea);
        return field;
      }

      var input = document.createElement('input');
      input.type = 'text';
      input.className = 'home-assistant-planning-input';
      input.name = question.id;
      input.disabled = isSubmitting;
      input.required = Boolean(question.required);
      input.value = formData[question.id] || '';
      input.placeholder = question.placeholder || '';
      input.addEventListener('input', function (event) {
        if (!homeAssistantState.planningState) return;
        homeAssistantState.planningState.formData[question.id] = event.target.value;
      });
      field.appendChild(input);
      return field;
    }

    var visibleQuestions = [];
    var questions = getPlanningQuestions(planningState);
    for (var i = 0; i < questions.length; i++) {
      if (shouldShowPlanningQuestion(questions[i], planningState)) {
        visibleQuestions.push(questions[i]);
      }
    }

    var grid = null;
    for (var qIndex = 0; qIndex < visibleQuestions.length; qIndex++) {
      var question = visibleQuestions[qIndex];
      var field = buildQuestionField(question);
      if (question.type === 'select') {
        if (!grid) {
          grid = document.createElement('div');
          grid.className = 'home-assistant-planning-grid';
        }
        grid.appendChild(field);
        continue;
      }

      if (grid && grid.children.length > 0) {
        form.appendChild(grid);
        grid = null;
      }
      form.appendChild(field);
    }
    if (grid && grid.children.length > 0) {
      form.appendChild(grid);
    }

    var actions = document.createElement('div');
    actions.className = 'home-assistant-planning-actions';

    var submitButton = document.createElement('button');
    submitButton.type = 'submit';
    submitButton.className = 'modern-btn modern-btn-primary';
    submitButton.textContent = isSubmitting
      ? 'Sending...'
      : (planningState.schema.submit_label || ('Continue With ' + planningState.agentLabel));
    submitButton.disabled = isSubmitting;
    actions.appendChild(submitButton);

    var resetButton = document.createElement('button');
    resetButton.type = 'button';
    resetButton.className = 'modern-btn modern-btn-secondary';
    resetButton.textContent = 'Ask Another Task';
    resetButton.disabled = isSubmitting;
    resetButton.addEventListener('click', function () {
      focusHomeAssistantInput();
    });
    actions.appendChild(resetButton);

    form.appendChild(actions);
    form.addEventListener('submit', function (event) {
      event.preventDefault();
      submitHomeAssistantPlanningForm();
    });

    card.appendChild(form);
    container.appendChild(card);
    container.classList.remove('d-none');
    syncHomeAssistantLauncher();
    openHomeAssistantThinkingModal();

    if (planningState.focusField) {
      var focusName = planningState.focusField;
      planningState.focusField = '';
      window.setTimeout(function () {
        var field = container.querySelector('[name="' + focusName + '"]');
        if (!field) {
          field = container.querySelector('[data-planning-focus="' + focusName + '"] .home-assistant-planning-choice:not([disabled])');
        }
        if (field && typeof field.focus === 'function') {
          field.focus();
        }
      }, 80);
    }
  }

  function renderHomeAssistantInlineReply() {
    var els = getHomeAssistantElements();
    var container = els.inlineReply;
    if (!container) return;

    var replyState = homeAssistantState.inlineReplyState;
    container.innerHTML = '';
    if (!replyState) {
      container.classList.add('d-none');
      syncHomeAssistantLauncher();
      if (!homeAssistantState.busy) {
        closeHomeAssistantThinkingModal();
      }
      return;
    }

    var isSubmitting = Boolean(replyState.submitting || homeAssistantState.busy);
    var workflowStep = replyState.workflowStep && typeof replyState.workflowStep === 'object'
      ? replyState.workflowStep
      : null;
    var choiceStep = workflowStep &&
      String(workflowStep.step_type || '').trim() === 'ask_choice' &&
      Array.isArray(workflowStep.choices) &&
      workflowStep.choices.length > 0
      ? workflowStep
      : null;
    var choiceRequiresText = Boolean(choiceStep && choiceStep.free_text_allowed);
    var card = document.createElement('div');
    card.className = 'home-assistant-inline-reply-card';

    var header = document.createElement('div');
    header.className = 'home-assistant-inline-reply-header';
    header.textContent = 'Planning Subtask';
    card.appendChild(header);

    var meta = document.createElement('div');
    meta.className = 'home-assistant-inline-reply-meta';

    var statusChip = document.createElement('span');
    statusChip.className = 'home-assistant-inline-reply-chip is-waiting';
    statusChip.textContent = isSubmitting ? 'Submitting' : 'Waiting on you';
    meta.appendChild(statusChip);

    var workspaceName = getInlineReplyWorkspaceName(replyState.routeContext);
    if (workspaceName) {
      var workspaceChip = document.createElement('span');
      workspaceChip.className = 'home-assistant-inline-reply-chip';
      workspaceChip.textContent = workspaceName;
      meta.appendChild(workspaceChip);
    }

    var planChip = document.createElement('span');
    planChip.className = 'home-assistant-inline-reply-chip';
    planChip.textContent = replyState.linkedTask && replyState.linkedTask.id
      ? 'Main task added'
      : 'Linked to workspace plan';
    meta.appendChild(planChip);
    card.appendChild(meta);

    if (replyState.linkedTask && replyState.linkedTask.description) {
      var linkedTaskMeta = document.createElement('div');
      linkedTaskMeta.className = 'home-assistant-inline-reply-preview-meta';
      linkedTaskMeta.textContent = 'Main workspace task: ' + replyState.linkedTask.description;
      card.appendChild(linkedTaskMeta);
    }

    var title = document.createElement('div');
    title.className = 'home-assistant-inline-reply-title';
    title.textContent = choiceStep && String(choiceStep.title || '').trim()
      ? String(choiceStep.title || '').trim()
      : 'Review the current plan, then complete this subtask.';
    card.appendChild(title);

    if (replyState.latestReplyText) {
      var preview = document.createElement('section');
      preview.className = 'home-assistant-inline-reply-preview';

      var previewEyebrow = document.createElement('div');
      previewEyebrow.className = 'home-assistant-inline-reply-preview-eyebrow';
      previewEyebrow.textContent = 'Current Plan';
      preview.appendChild(previewEyebrow);

      var previewMeta = document.createElement('div');
      previewMeta.className = 'home-assistant-inline-reply-preview-meta';
      previewMeta.textContent = (replyState.agentLabel || getWorkspaceHomeAssistantDisplayName()) + ' is carrying this planning thread.';
      preview.appendChild(previewMeta);

      var previewBody = document.createElement('div');
      previewBody.className = 'home-assistant-inline-reply-preview-body';
      previewBody.textContent = replyState.latestReplyText;
      preview.appendChild(previewBody);
      card.appendChild(preview);
    }

    if (choiceStep) {
      var choicePrompts = document.createElement('section');
      choicePrompts.className = 'home-assistant-inline-reply-prompts';

      var choicePromptsTitle = document.createElement('div');
      choicePromptsTitle.className = 'home-assistant-inline-reply-prompts-title';
      choicePromptsTitle.textContent = 'Complete This Subtask';
      choicePrompts.appendChild(choicePromptsTitle);

      var choicePromptsBody = document.createElement('div');
      choicePromptsBody.className = 'home-assistant-inline-reply-copy';
      choicePromptsBody.style.marginBottom = '0';
      choicePromptsBody.textContent = String(choiceStep.summary || 'Choose one option below to continue this plan.').trim();
      choicePrompts.appendChild(choicePromptsBody);

      card.appendChild(choicePrompts);
    } else if (replyState.questionPrompts && replyState.questionPrompts.length > 0) {
      var prompts = document.createElement('section');
      prompts.className = 'home-assistant-inline-reply-prompts';

      var promptsTitle = document.createElement('div');
      promptsTitle.className = 'home-assistant-inline-reply-prompts-title';
      promptsTitle.textContent = 'Complete This Subtask';
      prompts.appendChild(promptsTitle);

      var promptsList = document.createElement('ul');
      promptsList.className = 'home-assistant-inline-reply-prompts-list';

      for (var i = 0; i < replyState.questionPrompts.length; i++) {
        var item = document.createElement('li');
        item.textContent = replyState.questionPrompts[i];
        promptsList.appendChild(item);
      }

      prompts.appendChild(promptsList);
      card.appendChild(prompts);
    }

    var copy = document.createElement('div');
    copy.className = 'home-assistant-inline-reply-copy';
    copy.textContent = choiceStep
      ? 'Choose one option to move the workspace plan forward. Open full chat instead if this subtask needs more explanation.'
      : replyState.questionPrompts && replyState.questionPrompts.length > 0
      ? 'Answer the open planning questions below, or move this to full chat if you want a longer back-and-forth.'
      : 'Complete this subtask below, or move this to full chat if the plan needs a longer back-and-forth.';
    card.appendChild(copy);

    var form = document.createElement('form');
    form.className = 'home-assistant-inline-reply-form';
    form.noValidate = true;

    if (choiceStep) {
      var choiceGroup = document.createElement('div');
      choiceGroup.className = 'home-assistant-planning-choice-group';

      for (var i = 0; i < choiceStep.choices.length; i++) { // eslint-disable-line no-redeclare
        (function (choice) {
          var choiceButton = document.createElement('button');
          choiceButton.type = 'button';
          choiceButton.className = 'home-assistant-planning-choice' + (replyState.selectedChoiceId === choice.id ? ' is-selected' : '');
          choiceButton.disabled = isSubmitting;
          choiceButton.setAttribute('aria-pressed', replyState.selectedChoiceId === choice.id ? 'true' : 'false');
          choiceButton.addEventListener('click', function () {
            if (!homeAssistantState.inlineReplyState || isSubmitting) return;
            homeAssistantState.inlineReplyState.selectedChoiceId = choice.id;
            homeAssistantState.inlineReplyState.selectedChoiceLabel = choice.label || '';
            homeAssistantState.inlineReplyState.selectedChoiceNumber = choice.number || '';
            renderHomeAssistantInlineReply();
          });

          var choiceLabel = document.createElement('span');
          choiceLabel.className = 'home-assistant-planning-choice-label';
          choiceLabel.textContent = choice.number ? choice.number + '. ' + choice.label : choice.label;
          choiceButton.appendChild(choiceLabel);

          if (choice.description) {
            var choiceHint = document.createElement('span');
            choiceHint.className = 'home-assistant-planning-choice-hint';
            choiceHint.textContent = choice.description;
            choiceButton.appendChild(choiceHint);
          }

          choiceGroup.appendChild(choiceButton);
        })(choiceStep.choices[i] || {});
      }

      form.appendChild(choiceGroup);
    }

    if (!choiceStep || choiceRequiresText) {
      var label = document.createElement('label');
      label.className = 'home-assistant-inline-reply-input-label';
      label.setAttribute('for', 'homeAssistantInlineReplyMessage');
      label.textContent = choiceStep ? 'Optional Planning Note' : 'Subtask Response';
      form.appendChild(label);

      var textarea = document.createElement('textarea');
      textarea.id = 'homeAssistantInlineReplyMessage';
      textarea.className = 'home-assistant-inline-reply-textarea';
      textarea.name = 'inlineReplyMessage';
      textarea.disabled = isSubmitting;
      textarea.value = replyState.draft || '';
      textarea.placeholder = choiceStep
        ? 'Add extra planning context for this selected choice (optional)'
        : buildHomeAssistantInlineReplyPlaceholder(replyState.intent, replyState.agentLabel);
      textarea.addEventListener('input', function (event) {
        if (!homeAssistantState.inlineReplyState) return;
        homeAssistantState.inlineReplyState.draft = event.target.value;
      });
      form.appendChild(textarea);
    }

    var actions = document.createElement('div');
    actions.className = 'home-assistant-inline-reply-actions';

    var sendButton = document.createElement('button');
    sendButton.type = 'submit';
    sendButton.className = 'modern-btn modern-btn-primary';
    sendButton.disabled = isSubmitting || (choiceStep ? !replyState.selectedChoiceId : false);
    sendButton.textContent = isSubmitting
      ? 'Sending...'
      : choiceStep
      ? 'Complete Subtask'
      : 'Submit Subtask';
    actions.appendChild(sendButton);

    var openChatButton = document.createElement('button');
    openChatButton.type = 'button';
    openChatButton.className = 'modern-btn modern-btn-secondary';
    openChatButton.disabled = isSubmitting;
    openChatButton.textContent = choiceStep ? 'Move to Full Chat' : 'Open Chat Instead';
    openChatButton.addEventListener('click', function () {
      openChatPanel();
    });
    actions.appendChild(openChatButton);

    form.appendChild(actions);
    form.addEventListener('submit', function (event) {
      event.preventDefault();
      submitHomeAssistantInlineReply();
    });

    card.appendChild(form);
    container.appendChild(card);
    container.classList.remove('d-none');
    syncHomeAssistantLauncher();
    openHomeAssistantThinkingModal();
  }

  async function submitHomeAssistantInlineReply() {
    var replyState = homeAssistantState.inlineReplyState;
    if (!replyState || replyState.submitting) return;

    var workflowResponse = buildInlineReplyWorkflowResponse(replyState, replyState.draft || '');
    var choiceStep = replyState.workflowStep &&
      String(replyState.workflowStep.step_type || '').trim() === 'ask_choice' &&
      Array.isArray(replyState.workflowStep.choices) &&
      replyState.workflowStep.choices.length > 0;
    var text = String(replyState.draft || '').trim();
    var submittedText = text;
    if (choiceStep) {
      if (!workflowResponse) {
        if (window.Toast) {
          Toast.warning('Choose one next step before sending it to the workspace manager.');
        }
        renderHomeAssistantInlineReply();
        return;
      }
      submittedText = String(replyState.selectedChoiceLabel || replyState.selectedChoiceId || '').trim();
      if (text) {
        submittedText += ': ' + text;
      }
    }

    if (!submittedText) {
      if (window.Toast) {
        Toast.warning('Enter a reply before sending it to the workspace manager.');
      }
      renderHomeAssistantInlineReply();
      return;
    }

    replyState.submitting = true;
    renderHomeAssistantInlineReply();
    appendHomeAssistantMessage('user', submittedText);
    setHomeAssistantBusy(true, 'Sending Reply...');
    setHomeAssistantRoutingSummary(replyState.agentLabel, 'Continuing inline with the workspace manager.');

    try {
      await openWorkspaceAssistantForPrompt(submittedText, replyState.routeContext, replyState.intent, {
        reuseExistingSession: true,
        workflowResponse: workflowResponse
      });
    } catch (error) {
      dashLog.debug('Inline workspace reply failed', { error: error && error.message || error });
      if (homeAssistantState.inlineReplyState === replyState) {
        replyState.submitting = false;
        renderHomeAssistantInlineReply();
      }
      appendHomeAssistantMessage('assistant', 'I could not send that reply right now. Please try again or open chat.');
      setHomeAssistantRoutingSummary('Inline Reply Failed', 'Could not continue with the workspace manager right now.');
    } finally {
      if (homeAssistantState.inlineReplyState === replyState) {
        replyState.submitting = false;
      }
      setHomeAssistantBusy(false);
      renderHomeAssistantInlineReply();
    }
  }

  function renderHomeAssistantActions(actions) {
    var els = getHomeAssistantElements();
    var container = els.actions;
    if (!container) return;

    container.innerHTML = '';
    if (!actions || actions.length === 0) {
      container.classList.add('d-none');
      syncHomeAssistantLauncher();
      if (!homeAssistantState.busy) {
        closeHomeAssistantThinkingModal();
      }
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
    syncHomeAssistantLauncher();
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
        empty.textContent = 'No recent Assistant sessions yet. Send a message to start one.';
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
        meta.textContent = String(item.agent_name || 'Assistant') + ' • ' + formatRelativeTime(item.created_at);

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
    var keys = ['calendar_check', 'utility_direct', 'travel_planning', 'email_check'];
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

  function normalizeSkillName(name) {
    return normalizeToken(name).replace(/_/g, '-');
  }

  function scoreCapabilityRequirement(promptText, requirement, intentKey) {
    if (!requirement) return 0;
    var score = scoreMCPRequirement(promptText, requirement);
    var preferredIntents = Array.isArray(requirement.intents) ? requirement.intents : [];
    if (intentKey && preferredIntents.indexOf(intentKey) >= 0) {
      score += 4;
    }
    return score;
  }

  function detectCapabilityRequirement(prompt, intent, intentVariant) {
    if (normalizeToken(intentVariant) === 'workspace_schedule') return null;

    var promptText = normalizeToken(prompt);
    if (!promptText) return null;

    var intentKey = normalizeToken(intent && intent.key ? intent.key : intent);
    var best = null;
    var bestScore = 0;
    for (var i = 0; i < HOME_CAPABILITY_REQUIREMENTS.length; i++) {
      var requirement = HOME_CAPABILITY_REQUIREMENTS[i];
      var score = scoreCapabilityRequirement(promptText, requirement, intentKey);
      if (score > bestScore) {
        best = requirement;
        bestScore = score;
      }
    }
    if (bestScore <= 0) return null;
    return best;
  }

  function findCapabilityRequirementByKey(requirementKey) {
    if (!requirementKey) return null;
    for (var i = 0; i < HOME_CAPABILITY_REQUIREMENTS.length; i++) {
      var requirement = HOME_CAPABILITY_REQUIREMENTS[i];
      if (requirement && requirement.key === requirementKey) return requirement;
    }
    return null;
  }

  function detectMCPRequirement(prompt) {
    var requirement = detectCapabilityRequirement(prompt, homeAssistantState.pendingIntent, homeAssistantState.pendingIntentVariant);
    return requirement && requirement.requiresMCP ? requirement : null;
  }

  function findMCPRequirementByKey(requirementKey) {
    var requirement = findCapabilityRequirementByKey(requirementKey);
    return requirement && requirement.requiresMCP ? requirement : null;
  }

  function findCapabilityRequirementSkill(requirement, skills, enabledOnly) {
    if (!requirement || !Array.isArray(requirement.preferredSkillNames) || !Array.isArray(skills)) return null;
    var preferred = requirement.preferredSkillNames.map(normalizeSkillName);
    for (var i = 0; i < skills.length; i++) {
      var skill = skills[i];
      var skillName = normalizeSkillName(skill && skill.name);
      if (!skillName) continue;
      if (preferred.indexOf(skillName) < 0) continue;
      if (enabledOnly && skill && skill.enabled === false) continue;
      return skill;
    }
    return null;
  }

  function looksLikeImplementationRequest(prompt) {
    var text = normalizeToken(prompt);
    if (!text) return false;

    var phrases = [
      'make ori able to',
      'make ask ori able to',
      'make the assistant able to',
      'add support for',
      'build a way to',
      'create a way to',
      'implement support for',
      'feature request',
      'new feature',
      'capability gap'
    ];

    for (var i = 0; i < phrases.length; i++) {
      if (text.indexOf(phrases[i]) >= 0) {
        return true;
      }
    }
    if (text.indexOf('ori') >= 0 && (text.indexOf('implement') >= 0 || text.indexOf('support') >= 0)) {
      return true;
    }
    return false;
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

  async function fetchAgentSkills(agentName) {
    if (typeof API === 'undefined' || typeof API.get !== 'function') return [];
    try {
      var path = '/api/skills';
      if (String(agentName || '').trim()) {
        path += '?agent=' + encodeURIComponent(agentName);
      }
      var data = await API.get(path);
      return Array.isArray(data && data.skills) ? data.skills : [];
    } catch (error) {
      dashLog.debug('Failed to fetch agent skills', { agent: agentName, error: error && error.message || error });
      return [];
    }
  }

  async function searchCapabilitySkillPackages(query, limit) {
    var normalizedQuery = String(query || '').trim();
    if (!normalizedQuery || typeof API === 'undefined' || typeof API.post !== 'function') return [];
    try {
      var data = await API.post('/api/skills/marketplace/search', {
        query: normalizedQuery,
        limit: typeof limit === 'number' && limit > 0 ? limit : 6
      });
      return Array.isArray(data && data.results) ? data.results : [];
    } catch (error) {
      dashLog.debug('Failed to search skills marketplace', { query: normalizedQuery, error: error && error.message || error });
      return [];
    }
  }

  async function installCapabilitySkillPackage(packageSpec) {
    var normalizedPackage = String(packageSpec || '').trim();
    if (!normalizedPackage || typeof API === 'undefined' || typeof API.post !== 'function') {
      return { status: 'invalid_package', message: 'Skill package is required.' };
    }
    try {
      await API.post('/api/skills/marketplace/install', { package: normalizedPackage });
      return {
        status: 'installed',
        package: normalizedPackage,
        message: 'Installed skill package "' + normalizedPackage + '".'
      };
    } catch (error) {
      return {
        status: 'install_failed',
        package: normalizedPackage,
        message: 'Failed to install skill package "' + normalizedPackage + '": ' + String(error && error.message || error || '')
      };
    }
  }

  async function enableSkillForAgent(agentName, skillName) {
    if (!agentName || !skillName || typeof API === 'undefined' || typeof API.post !== 'function') {
      return { status: 'enable_failed', message: 'Agent name and skill are required.' };
    }
    try {
      await API.post('/api/skills/' + encodeURIComponent(skillName) + '/enable', {
        agent: agentName,
        enabled: true
      });
      return {
        status: 'enabled',
        skillName: skillName,
        message: 'Enabled skill "' + skillName + '" for "' + agentName + '".'
      };
    } catch (error) {
      return {
        status: 'enable_failed',
        skillName: skillName,
        message: 'Failed to enable skill "' + skillName + '" for "' + agentName + '": ' + String(error && error.message || error || '')
      };
    }
  }

  function scoreMarketplaceSkillResult(requirement, result) {
    if (!requirement || !result) return 0;
    var score = 0;
    var preferredNames = Array.isArray(requirement.preferredSkillNames)
      ? requirement.preferredSkillNames.map(normalizeSkillName)
      : [];
    var skillName = normalizeSkillName(result && result.skill);
    var repository = normalizeToken(result && result.repository);
    var packageName = normalizeToken(result && result.package);

    if (skillName && preferredNames.indexOf(skillName) >= 0) {
      score += 100;
    }
    if (packageName && preferredNames.indexOf(packageName) >= 0) {
      score += 80;
    }
    if (repository) {
      score += scoreMCPRequirement(repository, requirement) * 4;
    }
    if (packageName) {
      score += scoreMCPRequirement(packageName.replace(/[@/]/g, ' '), requirement) * 4;
    }
    return score;
  }

  function chooseMarketplaceSkillResult(requirement, results) {
    if (!requirement || !Array.isArray(results)) return null;
    var best = null;
    var bestScore = 0;
    for (var i = 0; i < results.length; i++) {
      var score = scoreMarketplaceSkillResult(requirement, results[i]);
      if (score > bestScore) {
        best = results[i];
        bestScore = score;
      }
    }
    if (bestScore <= 0) return null;
    return best;
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

  async function fetchWorkspaceMCPState(workspaceId) {
    if (!workspaceId || typeof API === 'undefined' || typeof API.get !== 'function') return null;
    try {
      return await API.get('/api/workspaces/' + encodeURIComponent(workspaceId));
    } catch (error) {
      dashLog.debug('Failed to fetch workspace MCP state', { workspaceId: workspaceId, error: error && error.message || error });
      return null;
    }
  }

  async function fetchWorkspaceEntryAgentName(workspaceId) {
    if (!workspaceId) return '';

    var cached = getLoadedWorkspaceEntryAgentName(workspaceId);
    if (cached) return cached;

    if (typeof API === 'undefined' || typeof API.get !== 'function') return '';
    try {
      var data = await API.get('/api/workspaces/' + encodeURIComponent(workspaceId));
      return inferWorkspaceEntryAgentNameFromData(data);
    } catch (error) {
      dashLog.debug('Failed to fetch workspace entry agent', { workspaceId: workspaceId, error: error && error.message || error });
      return '';
    }
  }

  function inferWorkspaceEntryAgentNameFromData(workspaceData) {
    if (!workspaceData || typeof workspaceData !== 'object') return '';

    var direct = String(workspaceData.entry_agent_name || '').trim();
    if (direct) return direct;

    return '';
  }

  function getLoadedWorkspaceEntryAgentName(workspaceId) {
    var targetWorkspace = String(workspaceId || '').trim();
    if (!targetWorkspace) return '';

    var detailWorkspace = window.workspaceDetail && window.workspaceDetail.workspace;
    if (detailWorkspace && String(detailWorkspace.id || '').trim() === targetWorkspace) {
      return inferWorkspaceEntryAgentNameFromData(detailWorkspace);
    }

    return '';
  }

  function getWorkspaceAgentInstanceByName(workspaceData, agentName) {
    if (!workspaceData || !agentName) return null;
    var target = normalizeToken(agentName);
    var instances = Array.isArray(workspaceData.agent_instances) ? workspaceData.agent_instances : [];
    for (var i = 0; i < instances.length; i++) {
      var instance = instances[i];
      if (normalizeToken(instance && instance.name) === target) {
        return instance;
      }
    }
    return null;
  }

  function getWorkspaceAgentMCPAccessEntry(workspaceData, agentInstanceId) {
    if (!workspaceData || !agentInstanceId) return null;
    var target = normalizeToken(agentInstanceId);
    var entries = Array.isArray(workspaceData.agent_mcp_access) ? workspaceData.agent_mcp_access : [];
    for (var i = 0; i < entries.length; i++) {
      var entry = entries[i];
      if (normalizeToken(entry && entry.agent_instance_id) === target) {
        return entry;
      }
    }
    return null;
  }

  function findWorkspaceMCPBindingByServerName(workspaceData, serverName) {
    if (!workspaceData || !serverName) return null;
    var target = normalizeMCPServerName(serverName);
    var bindings = Array.isArray(workspaceData.mcp_bindings) ? workspaceData.mcp_bindings : [];
    for (var i = 0; i < bindings.length; i++) {
      var binding = bindings[i];
      if (normalizeMCPServerName(binding && binding.server_name) === target) {
        return binding;
      }
    }
    return null;
  }

  function normalizeWorkspaceMCPBinding(binding, configuredSnapshot) {
    if (!binding || !binding.server_name) return null;
    var serverName = String(binding.server_name || '').trim();
    if (!serverName) return null;

    var snapshot = configuredSnapshot || { servers: [], stats: {} };
    var globalConfig = findMCPServerByName(snapshot.servers, serverName);
    var stats = lookupMCPServerStats(snapshot.stats, serverName);
    var enabled = binding.enabled !== false;
    var status = normalizeToken(stats && stats.status);
    if (!status) {
      if (!globalConfig) {
        status = 'missing';
      } else if (!enabled) {
        status = 'disabled';
      } else {
        status = 'configured';
      }
    }

    return {
      id: String(binding.id || '').trim(),
      name: serverName,
      enabled: enabled,
      status: status,
      tool_count: Number(stats && (stats.tool_count || stats.toolCount) || 0),
      globally_configured: Boolean(globalConfig),
      workspace_binding: true
    };
  }

  function buildWorkspaceAccessibleMCPBindings(workspaceData, agentName, configuredSnapshot) {
    if (!workspaceData) return [];

    var bindings = Array.isArray(workspaceData.mcp_bindings) ? workspaceData.mcp_bindings : [];
    if (bindings.length === 0) return [];

    var allowedIds = null;
    if (agentName) {
      var instance = getWorkspaceAgentInstanceByName(workspaceData, agentName);
      if (instance && instance.id) {
        var access = getWorkspaceAgentMCPAccessEntry(workspaceData, instance.id);
        if (access) {
          allowedIds = Object.create(null);
          var ids = Array.isArray(access.enabled_binding_ids) ? access.enabled_binding_ids : [];
          for (var i = 0; i < ids.length; i++) {
            var normalizedId = normalizeToken(ids[i]);
            if (normalizedId) {
              allowedIds[normalizedId] = true;
            }
          }
        }
      }
    }

    var out = [];
    for (var j = 0; j < bindings.length; j++) {
      var binding = bindings[j];
      var bindingId = normalizeToken(binding && binding.id);
      if (allowedIds && (!bindingId || !allowedIds[bindingId])) {
        continue;
      }
      var normalizedBinding = normalizeWorkspaceMCPBinding(binding, configuredSnapshot);
      if (!normalizedBinding) continue;
      out.push(normalizedBinding);
    }
    return out;
  }

  async function fetchAgentMCPServers(agentName, routeContext) {
    var normalizedContext = normalizeHomeRouteContext(routeContext);
    var workspaceId = hasWorkspaceRouteContext(normalizedContext) ? String(normalizedContext.workspace_id || '').trim() : '';
    if (!workspaceId) {
      return [];
    }

    try {
      var loaded = await Promise.all([
        fetchWorkspaceMCPState(workspaceId),
        fetchConfiguredMCPServerSnapshot()
      ]);
      var workspaceData = loaded[0];
      var configuredSnapshot = loaded[1] || { servers: [], stats: {} };
      return buildWorkspaceAccessibleMCPBindings(workspaceData, agentName, configuredSnapshot);
    } catch (error) {
      dashLog.debug('Failed to fetch workspace MCP bindings for agent', {
        agent: agentName,
        workspaceId: workspaceId,
        error: error && error.message || error
      });
      return [];
    }
  }

  async function bindMCPServerForWorkspace(workspaceId, agentName, serverName) {
    var normalizedWorkspaceId = String(workspaceId || '').trim();
    var normalizedServerName = String(serverName || '').trim();
    if (!normalizedWorkspaceId || !normalizedServerName || typeof API === 'undefined' || typeof API.post !== 'function') {
      return { status: 'bind_failed', message: 'Workspace and MCP server are required.' };
    }

    var workspaceData = await fetchWorkspaceMCPState(normalizedWorkspaceId);
    if (!workspaceData) {
      return {
        status: 'bind_failed',
        serverName: normalizedServerName,
        message: 'Failed to load workspace MCP bindings before applying "' + normalizedServerName + '".'
      };
    }

    var binding = findWorkspaceMCPBindingByServerName(workspaceData, normalizedServerName);
    var createdBinding = false;
    var enabledBinding = false;
    var accessUpdated = false;

    try {
      if (!binding) {
        var createResult = await API.post('/api/workspaces/' + encodeURIComponent(normalizedWorkspaceId) + '/mcp-bindings', {
          server_name: normalizedServerName,
          enabled: true
        });
        binding = createResult && createResult.binding ? createResult.binding : {
          id: '',
          server_name: normalizedServerName,
          enabled: true
        };
        createdBinding = true;
      } else if (binding.enabled === false) {
        var updateResult = await API.put('/api/workspaces/' + encodeURIComponent(normalizedWorkspaceId) + '/mcp-bindings/' + encodeURIComponent(binding.id), {
          enabled: true
        });
        binding = updateResult && updateResult.binding ? updateResult.binding : Object.assign({}, binding, { enabled: true });
        enabledBinding = true;
      }
    } catch (error) {
      return {
        status: 'bind_failed',
        serverName: normalizedServerName,
        message: 'Failed to bind MCP connector "' + normalizedServerName + '" in this workspace: ' + String(error && error.message || error || '')
      };
    }

    if (agentName && binding && binding.id) {
      var instance = getWorkspaceAgentInstanceByName(workspaceData, agentName);
      if (instance && instance.id) {
        var access = getWorkspaceAgentMCPAccessEntry(workspaceData, instance.id);
        if (access) {
          var nextIds = uniqueValues((Array.isArray(access.enabled_binding_ids) ? access.enabled_binding_ids : [])
            .map(function (value) { return String(value || '').trim(); })
            .filter(Boolean)
            .concat([String(binding.id || '').trim()]));
          if (nextIds.length !== (Array.isArray(access.enabled_binding_ids) ? access.enabled_binding_ids.length : 0)
            || nextIds.indexOf(String(binding.id || '').trim()) < 0) {
            try {
              await API.put('/api/workspaces/' + encodeURIComponent(normalizedWorkspaceId) + '/agent-mcp-access/' + encodeURIComponent(instance.id), {
                enabled_binding_ids: nextIds
              });
              accessUpdated = true;
            } catch (error) {
              return {
                status: 'bind_failed',
                serverName: normalizedServerName,
                message: 'Bound MCP connector "' + normalizedServerName + '" in the workspace, but failed to update agent access: ' + String(error && error.message || error || '')
              };
            }
          }
        }
      }
    }

    var message = 'MCP connector "' + normalizedServerName + '" is already bound in this workspace.';
    var status = 'already_bound';
    if (createdBinding) {
      status = 'bound_existing';
      message = 'Bound MCP connector "' + normalizedServerName + '" in this workspace.';
    } else if (enabledBinding || accessUpdated) {
      status = 'bound_existing';
      message = 'Updated the workspace binding for MCP connector "' + normalizedServerName + '".';
    }

    return {
      status: status,
      serverName: normalizedServerName,
      workspaceId: normalizedWorkspaceId,
      bindingId: binding && binding.id ? binding.id : '',
      message: message
    };
  }

  async function bindMCPServerForRouteContext(agentName, serverName, routeContext) {
    var normalizedContext = normalizeHomeRouteContext(routeContext);
    var workspaceId = hasWorkspaceRouteContext(normalizedContext) ? String(normalizedContext.workspace_id || '').trim() : '';
    if (!workspaceId) {
      return {
        status: 'requires_workspace',
        serverName: String(serverName || '').trim(),
        message: 'MCP connectors are workspace-scoped now. Open a workspace first so I can bind "' + String(serverName || '').trim() + '".'
      };
    }
    return bindMCPServerForWorkspace(workspaceId, agentName, serverName);
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

  async function applyEmailMCPCandidate(agentName, candidate, routeContext) {
    var installOutcome = await installMCPServerCandidate(candidate);
    var status = normalizeToken(installOutcome && installOutcome.status);
    if (!status || ['installed', 'already_installed'].indexOf(status) < 0) {
      return installOutcome;
    }

    var bindOutcome = await bindMCPServerForRouteContext(agentName, installOutcome.serverName || candidate && candidate.name, routeContext);
    var bindStatus = normalizeToken(bindOutcome && bindOutcome.status);
    if (bindStatus === 'requires_workspace') {
      return {
        status: status === 'installed' ? 'installed_only' : 'already_installed',
        serverName: installOutcome.serverName || candidate && candidate.name,
        message: status === 'installed'
          ? 'Installed MCP connector "' + (installOutcome.serverName || candidate && candidate.name || 'selected server') + '". Open a workspace to bind it before continuing.'
          : 'MCP connector "' + (installOutcome.serverName || candidate && candidate.name || 'selected server') + '" is already installed. Open a workspace to bind it before continuing.'
      };
    }

    if (bindStatus === 'bound_existing' || bindStatus === 'already_bound') {
      return {
        status: status === 'installed' ? 'installed_and_bound' : bindOutcome.status,
        serverName: bindOutcome.serverName || installOutcome.serverName || candidate && candidate.name,
        message: status === 'installed'
          ? 'Installed and bound MCP connector "' + (bindOutcome.serverName || installOutcome.serverName || candidate && candidate.name || 'selected server') + '" in this workspace.'
          : bindOutcome.message
      };
    }

    return bindOutcome;
  }

  async function openScopedMCPBrowseModal(agentName, prompt, routeContext, options) {
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
            note.textContent = 'Already configured globally.';
          } else if (candidate.isInstalled) {
            note.textContent = 'Installed. Selecting this will bind it from the active workspace.';
          } else {
            note.textContent = String(modalOptions.pendingInstallText || 'Will be installed and then bound from the active workspace.');
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
            var outcome = await applyEmailMCPCandidate(agentName, selectedCandidate, routeContext);
            var outcomeStatus = normalizeToken(outcome && outcome.status);
            if (['already_bound', 'bound_existing', 'installed_and_bound', 'installed_only', 'already_installed'].indexOf(outcomeStatus) >= 0) {
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

  async function openEmailMCPBrowseModal(agentName, prompt, routeContext) {
    return openScopedMCPBrowseModal(agentName, prompt, routeContext, {
      modalPrefix: 'homeEmailMCPBrowseModal',
      title: 'Browse Email MCP Connectors',
      description: 'Select an email connector for Gmail, Outlook, or IMAP. Installed connectors are bound from the active workspace when one is available.',
      searchPlaceholder: 'gmail, outlook, imap...',
      emptyStateText: 'No matching email MCP connectors found.',
      pendingInstallText: 'Will be installed and then bound in the active workspace.',
      switchLabel: 'Use Browser Control',
      switchTarget: 'browser_control',
      buildCandidates: buildEmailMCPBrowseCandidates
    });
  }

  async function openBrowserControlMCPBrowseModal(agentName, prompt, routeContext) {
    return openScopedMCPBrowseModal(agentName, prompt, routeContext, {
      modalPrefix: 'homeBrowserMCPBrowseModal',
      title: 'Browse Browser Control MCP',
      description: 'Select a browser-control connector (Playwright, Browserbase, or Puppeteer). Installed connectors are bound from the active workspace when one is available.',
      searchPlaceholder: 'playwright, browserbase, puppeteer...',
      emptyStateText: 'No matching browser-control MCP connectors found.',
      pendingInstallText: 'Will be installed and then bound in the active workspace.',
      switchLabel: 'Use Email Connector',
      switchTarget: 'email_connector',
      buildCandidates: buildBrowserMCPBrowseCandidates
    });
  }

  async function runEmailAccessMCPSelection(agentName, prompt, startingMode, routeContext) {
    var mode = normalizeToken(startingMode) === 'browser' ? 'browser' : 'email';
    for (var i = 0; i < 4; i++) {
      var result = mode === 'browser'
        ? await openBrowserControlMCPBrowseModal(agentName, prompt, routeContext)
        : await openEmailMCPBrowseModal(agentName, prompt, routeContext);
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
    lines.push('3) Create a dedicated Email Assistant and bind MCP from the target workspace after setup.');
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
    var routeContext = buildHomeRouteContext();
    if (summary) {
      appendHomeAssistantMessage('assistant', summary);
    }

    homeAssistantState.awaitingCreateConfirmation = true;
    setHomeAssistantRoutingSummary('Email Setup Options', 'Choose email MCP, browser control MCP, or create a dedicated email agent.');

    function openSelection(mode) {
      runEmailAccessMCPSelection('', prompt, mode, routeContext).then(function (result) {
        var resultStatus = normalizeToken(result && result.status);
        if (resultStatus === 'installed_only' || resultStatus === 'already_installed' || resultStatus === 'already_bound' || resultStatus === 'bound_existing' || resultStatus === 'installed_and_bound') {
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
    if (['already_bound', 'bound_existing', 'installed_and_bound'].indexOf(status) >= 0) {
      return false;
    }
    return true;
  }

  async function maybeResolveEmailMCPBeforeHandoff(agentName, prompt, mcpOutcome, routeContext) {
    if (!agentName) {
      return { continueHandoff: false };
    }
    if (!shouldPauseForEmailMCPSelection(mcpOutcome)) {
      return { continueHandoff: true };
    }

    appendHomeAssistantMessage('assistant', 'Before handoff, select an email connector or browser-control connector so this task can access your inbox.');
    setHomeAssistantRoutingSummary('Email MCP Required', 'Select an email or browser-control connector before continuing.');

    var selection = await runEmailAccessMCPSelection(agentName, prompt, 'email', routeContext);
    var selectionStatus = normalizeToken(selection && selection.status);

    if (selectionStatus === 'already_bound' || selectionStatus === 'bound_existing' || selectionStatus === 'installed_and_bound') {
      appendHomeAssistantMessage('assistant', selection.message || ('Bound MCP connector "' + (selection.serverName || 'selected server') + '" in this workspace.'));
      if (isLegacyMCPServerName(selection && selection.serverName)) {
        appendHomeAssistantMessage('assistant', 'Note: Puppeteer MCP is legacy/deprecated. Playwright is recommended for browser control.');
      }
      return { continueHandoff: true };
    }

    if (selectionStatus === 'continue_without_mcp') {
      appendHomeAssistantMessage('assistant', 'Continuing without an MCP connector. Email access may be unavailable.');
      return { continueHandoff: true };
    }

    if (selectionStatus === 'requires_workspace') {
      appendHomeAssistantMessage('assistant', selection.message || 'Selected MCP connector still needs a workspace binding before handoff.');
      setHomeAssistantRoutingSummary('Workspace Required', 'Open a workspace before continuing with MCP-dependent email access.');
      renderHomeAssistantActions([
        {
          label: 'Open Workspaces',
          variant: 'primary',
          onClick: function () { window.location.href = '/workspaces'; }
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
    var routeContext = options && options.routeContext ? options.routeContext : buildHomeRouteContext();
    var normalizedContext = normalizeHomeRouteContext(routeContext);
    var workspaceId = hasWorkspaceRouteContext(normalizedContext) ? String(normalizedContext.workspace_id || '').trim() : '';

    if (!workspaceId) {
      var missingWorkspaceMessage = 'MCP connectors are workspace-scoped now. Open a workspace before I bind the connector required for ' + requirement.label + '.';
      return {
        status: 'requires_workspace',
        message: missingWorkspaceMessage
      };
    }

    var currentServers = await fetchAgentMCPServers(agentName, normalizedContext);
    var existing = selectExistingMCPServer(requirement, currentServers);

    if (existing && existing.enabled) {
      return {
        status: 'already_bound',
        serverName: existing.name
      };
    }

    if (existing && !allowMutations) {
      return {
        status: 'existing_disabled',
        serverName: existing.name,
        message: 'MCP connector "' + existing.name + '" matches this task but is not enabled in this workspace.'
      };
    }

    var targetServerName = existing && existing.name ? existing.name : '';
    var installCandidate = null;
    var configuredServers = await fetchConfiguredMCPServers();

    if (!targetServerName) {
      var configuredCandidate = selectExistingMCPServer(requirement, configuredServers);
      if (configuredCandidate) {
        targetServerName = configuredCandidate.name;
      } else {
        var marketplaceServers = await fetchMarketplaceMCPServers();
        installCandidate = chooseMarketplaceMCPServer(requirement, prompt, marketplaceServers);
        if (!installCandidate) {
          return {
            status: 'not_found',
            message: 'This task may need MCP (' + requirement.label + '), but no matching connector is currently configured.'
          };
        }
        targetServerName = installCandidate.name;
      }

      if (!allowMutations) {
        return {
          status: 'candidate_available',
          serverName: targetServerName,
          message: 'Found MCP connector "' + targetServerName + '" for ' + requirement.label + '. Select it to install and bind in this workspace.'
        };
      }

      if (installCandidate) {
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
    }

    if (!allowMutations) {
      return {
        status: 'needs_bind',
        serverName: targetServerName,
        message: 'MCP connector "' + targetServerName + '" is available. Select it to bind in this workspace before continuing.'
      };
    }

    var bindOutcome = await bindMCPServerForRouteContext(agentName, targetServerName, normalizedContext);
    var bindStatus = normalizeToken(bindOutcome && bindOutcome.status);
    if (bindStatus !== 'already_bound' && bindStatus !== 'bound_existing') {
      return {
        status: bindOutcome && bindOutcome.status ? bindOutcome.status : 'bind_failed',
        serverName: targetServerName,
        message: bindOutcome && bindOutcome.message
          ? bindOutcome.message
          : 'I found MCP server "' + targetServerName + '" for ' + requirement.label + ' but failed to bind it in this workspace.'
      };
    }

    if (installCandidate) {
      return {
        status: 'installed_and_bound',
        serverName: targetServerName,
        message: 'Installed and bound MCP connector "' + targetServerName + '" in this workspace.'
      };
    }

    return {
      status: bindOutcome.status || 'bound_existing',
      serverName: targetServerName,
      message: bindOutcome.message || ('Bound MCP connector "' + targetServerName + '" in this workspace.')
    };
  }

  function inferCalendarIntentVariant(prompt, routeContext, routeData) {
    var backendVariant = normalizeToken(routeData && routeData.intent_variant);
    if (backendVariant) return backendVariant;

    var normalized = normalizeToken(prompt);
    if (!normalized) return 'personal_calendar';

    var workspaceSignals = [
      'workspace schedule', 'scheduled task', 'scheduled tasks', 'scheduler',
      'next run', 'cron', 'run today in this workspace', 'workspace tasks'
    ];
    var personalSignals = [
      'my calendar', 'calendar', 'meeting', 'meetings', 'appointment', 'appointments',
      'am i free', 'availability', 'free time', 'busy', 'events'
    ];

    for (var i = 0; i < workspaceSignals.length; i++) {
      if (normalized.indexOf(workspaceSignals[i]) >= 0) return 'workspace_schedule';
    }
    for (var j = 0; j < personalSignals.length; j++) {
      if (normalized.indexOf(personalSignals[j]) >= 0) return 'personal_calendar';
    }
    if (hasWorkspaceRouteContext(routeContext) && normalized.indexOf('schedule') >= 0) {
      return 'ambiguous';
    }
    return 'personal_calendar';
  }

  async function chooseCapabilitySkillPackage(requirement) {
    if (!requirement || !Array.isArray(requirement.skillMarketplaceQueries) || requirement.skillMarketplaceQueries.length === 0) {
      return null;
    }
    var best = null;
    var bestScore = 0;
    for (var i = 0; i < requirement.skillMarketplaceQueries.length; i++) {
      var query = String(requirement.skillMarketplaceQueries[i] || '').trim();
      if (!query) continue;
      var results = await searchCapabilitySkillPackages(query, 8);
      var candidate = chooseMarketplaceSkillResult(requirement, results);
      var score = scoreMarketplaceSkillResult(requirement, candidate);
      if (score > bestScore) {
        best = candidate;
        bestScore = score;
      }
    }
    return best;
  }

  function describeCapabilityAction(action) {
    if (!action || !action.type) return '';
    switch (action.type) {
      case 'create_agent':
        return 'Create "' + String(action.desiredAgentName || 'new agent') + '"';
      case 'enable_skill':
        return 'Enable skill "' + String(action.skillName || '') + '"';
      case 'install_skill_package':
        return 'Install skill package "' + String(action.packageSpec || '') + '"';
      case 'attach_mcp':
        return 'Bind MCP "' + String(action.serverName || '') + '" in the workspace';
      case 'install_and_attach_mcp':
        return 'Install and bind MCP "' + String(action.serverName || '') + '" in the workspace';
      case 'handoff':
        return 'Hand off to chat';
      case 'offer_workspace':
        return 'Create a workspace to implement support';
      default:
        return toTitleCase(action.type);
    }
  }

  function buildCapabilityPlanSummary(plan) {
    if (!plan) return '';
    var actionLabels = [];
    for (var i = 0; i < plan.actions.length; i++) {
      var label = describeCapabilityAction(plan.actions[i]);
      if (!label || plan.actions[i].type === 'handoff' || plan.actions[i].type === 'pause_for_user_choice') continue;
      actionLabels.push(label);
    }

    if (plan.classification === 'solvable_now') {
      if (plan.executionMode === 'workspace_schedule') {
        return 'I can check the scheduled tasks in this workspace directly.';
      }
      return 'This request is already solvable with the current capabilities.';
    }
    if (plan.classification === 'user_setup_only') {
      return plan.summary || 'This needs manual connector setup before I can continue.';
    }
    if (plan.classification === 'implementation_request') {
      return 'This looks like product work rather than an executable task. I can create a workspace to implement it.';
    }
    if (plan.classification === 'reusable_feature_gap') {
      return 'I can’t satisfy this with the current capabilities, but I can create a workspace to implement support for it.';
    }
    if (actionLabels.length === 0) {
      return plan.summary || 'I found a capability plan for this request.';
    }
    return 'I can make this work by: ' + actionLabels.join(' -> ') + '.';
  }

  function buildCapabilityImplementationBrief(plan, prompt) {
    var lines = [];
    var requirementLabel = plan && plan.requirement ? plan.requirement.label : 'this capability';
    var gaps = Array.isArray(plan && plan.gaps) ? plan.gaps : [];

    lines.push('Problem:');
    lines.push('- User request: "' + String(prompt || '').trim() + '"');
    lines.push('- Ask Ori cannot satisfy this today using the currently available agents, skills, and workspace MCP bindings.');
    lines.push('');
    lines.push('Observed gaps:');
    if (gaps.length === 0) {
      lines.push('- No executable capability plan was available for ' + requirementLabel + '.');
    } else {
      for (var i = 0; i < gaps.length; i++) {
        lines.push('- ' + gaps[i]);
      }
    }
    lines.push('');
    lines.push('Recommended path:');
    if (plan && Array.isArray(plan.actions) && plan.actions.length > 0) {
      for (var j = 0; j < plan.actions.length; j++) {
        var label = describeCapabilityAction(plan.actions[j]);
        if (!label || plan.actions[j].type === 'offer_workspace') continue;
        lines.push('- ' + label);
      }
    } else {
      lines.push('- Add a capability requirement entry and the supporting skill/MCP flow for ' + requirementLabel + '.');
    }
    lines.push('');
    lines.push('Acceptance criteria:');
    lines.push('- Ask Ori recognizes the request and produces a capability plan instead of refusing.');
    lines.push('- Setup steps require explicit user confirmation before any mutation.');
    lines.push('- Once the capability is bound in the workspace, Ask Ori can hand off or answer inline as appropriate.');
    lines.push('');
    lines.push('Starter actions:');
    lines.push('1. Add or refine the capability requirement entry for this request.');
    lines.push('2. Wire the required skill and MCP discovery into the Ask Ori planner.');
    lines.push('3. Add the confirmation UI and execution path for the chosen setup steps.');
    lines.push('4. Add regression coverage for direct execution, setup, and workspace escalation.');

    return lines.join('\n');
  }

  function buildCapabilityWorkspaceSeed(plan, prompt) {
    var title = truncateText(String(prompt || '').trim(), 120) || 'Capability gap';
    return {
      seedNote: {
        name: 'Capability Gap Brief',
        content: plan && plan.brief ? plan.brief : buildCapabilityImplementationBrief(plan, prompt)
      },
      seedTask: {
        description: 'Implement support for: ' + title,
        details: plan && plan.brief ? plan.brief : buildCapabilityImplementationBrief(plan, prompt),
        priority: 2
      }
    };
  }

  async function planCapabilityResolution(options) {
    var prompt = String(options && options.prompt || '').trim();
    var intent = options && options.intent ? options.intent : homeAssistantState.pendingIntent;
    var routeContext = options && options.routeContext ? options.routeContext : buildHomeRouteContext();
    var routeData = options && options.routeData ? options.routeData : null;
    var matchedAgentName = String(options && options.matchedAgentName || '').trim();
    var intentVariant = normalizeToken(options && options.intentVariant || homeAssistantState.pendingIntentVariant || routeData && routeData.intent_variant);

    if (intent && intent.key === 'calendar_check') {
      intentVariant = intentVariant || inferCalendarIntentVariant(prompt, routeContext, routeData);
      if (intentVariant === 'ambiguous' && hasWorkspaceRouteContext(routeContext)) {
        var ambiguousPlan = {
          classification: 'solvable_now',
          intentVariant: intentVariant,
          actions: [{ type: 'pause_for_user_choice' }],
          summary: 'This could mean your personal calendar or the scheduled tasks in this workspace.',
          gaps: [],
          requirement: findCapabilityRequirementByKey('calendar_access')
        };
        ambiguousPlan.brief = buildCapabilityImplementationBrief(ambiguousPlan, prompt);
        return ambiguousPlan;
      }
      if (intentVariant === 'workspace_schedule') {
        var workspacePlan = {
          classification: hasWorkspaceRouteContext(routeContext) ? 'solvable_now' : 'user_setup_only',
          intentVariant: intentVariant,
          executionMode: hasWorkspaceRouteContext(routeContext) ? 'workspace_schedule' : '',
          actions: [],
          summary: hasWorkspaceRouteContext(routeContext)
            ? 'I can inspect the scheduled tasks in this workspace directly.'
            : 'I need you to choose or open a workspace before I can inspect scheduled tasks.',
          gaps: hasWorkspaceRouteContext(routeContext) ? [] : ['No workspace context is available for the requested scheduled-task lookup.'],
          requirement: null
        };
        workspacePlan.brief = buildCapabilityImplementationBrief(workspacePlan, prompt);
        return workspacePlan;
      }
    }

    var requirement = detectCapabilityRequirement(prompt, intent, intentVariant);
    if (!requirement) {
      if (!looksLikeImplementationRequest(prompt)) return null;
      var implementationPlan = {
        classification: 'implementation_request',
        intentVariant: intentVariant,
        requirement: null,
        actions: [{ type: 'offer_workspace' }],
        summary: 'This looks like implementation work rather than an executable task.',
        gaps: ['No existing capability requirement matches this request.']
      };
      implementationPlan.brief = buildCapabilityImplementationBrief(implementationPlan, prompt);
      return implementationPlan;
    }

    var plan = {
      classification: 'solvable_now',
      intentVariant: intentVariant,
      requirement: requirement,
      targetAgentName: matchedAgentName,
      actions: [],
      summary: '',
      gaps: [],
      evidence: []
    };

    var visibleSkills = await fetchAgentSkills(matchedAgentName);
    var enabledSkill = matchedAgentName ? findCapabilityRequirementSkill(requirement, visibleSkills, true) : null;
    var availableSkill = matchedAgentName ? findCapabilityRequirementSkill(requirement, visibleSkills, false) : null;
    var repoScopedSkill = !matchedAgentName ? findCapabilityRequirementSkill(requirement, visibleSkills, false) : null;
    var skillCandidate = null;

    if (!enabledSkill && !availableSkill && !repoScopedSkill && Array.isArray(requirement.skillMarketplaceQueries) && requirement.skillMarketplaceQueries.length > 0) {
      skillCandidate = await chooseCapabilitySkillPackage(requirement);
    }

    var workspaceContextAvailable = hasWorkspaceRouteContext(routeContext);
    var agentServers = matchedAgentName && requirement.requiresMCP
      ? await fetchAgentMCPServers(matchedAgentName, routeContext)
      : [];
    var existingServer = requirement.requiresMCP ? selectExistingMCPServer(requirement, agentServers) : null;
    var existingServerStatus = normalizeToken(existingServer && existingServer.status);

    if (!matchedAgentName) {
      plan.classification = 'solvable_with_setup';
      plan.actions.push({
        type: 'create_agent',
        desiredAgentName: requirement.defaultAgentName || (intent && intent.suggestedName) || 'Task Assistant',
        desiredAgentType: requirement.preferredAgentType || (intent && intent.defaultType) || 'tool-calling'
      });
      plan.gaps.push('No existing agent is a strong match for ' + requirement.label + '.');
      if (repoScopedSkill) {
        plan.evidence.push('Repo skill "' + repoScopedSkill.name + '" is already available for new agents.');
      }
    }

    if (Array.isArray(requirement.preferredSkillNames) && requirement.preferredSkillNames.length > 0 && !enabledSkill) {
      plan.classification = 'solvable_with_setup';
      if (availableSkill) {
        plan.actions.push({ type: 'enable_skill', skillName: availableSkill.name });
        plan.gaps.push('Skill "' + availableSkill.name + '" is available but not enabled for this agent.');
      } else if (skillCandidate && skillCandidate.package) {
        plan.actions.push({ type: 'install_skill_package', packageSpec: skillCandidate.package, skillName: skillCandidate.skill || requirement.preferredSkillNames[0] });
        plan.actions.push({ type: 'enable_skill', skillName: skillCandidate.skill || requirement.preferredSkillNames[0] });
        plan.gaps.push('No matching skill is installed locally for ' + requirement.label + '.');
      } else {
        plan.gaps.push('No matching skill is available locally or in the skills marketplace for ' + requirement.label + '.');
      }
    }

    if (requirement.requiresMCP) {
      if (!workspaceContextAvailable) {
        plan.classification = 'user_setup_only';
        plan.actions = [];
        plan.summary = 'MCP connectors are workspace-scoped. Open a workspace before I bind the connector required for ' + requirement.label + '.';
        plan.gaps.push('No workspace context is available for the required MCP connector.');
      } else if (existingServer && existingServer.enabled && existingServerStatus !== 'missing') {
        plan.evidence.push('MCP "' + existingServer.name + '" is already bound in this workspace.');
      } else if (existingServer) {
        plan.classification = 'solvable_with_setup';
        plan.actions.push({ type: 'attach_mcp', serverName: existingServer.name });
        plan.gaps.push('MCP "' + existingServer.name + '" exists but is not enabled for this workspace context.');
      } else {
        var configuredServers = await fetchConfiguredMCPServers();
        var configuredCandidate = selectExistingMCPServer(requirement, configuredServers);
        if (configuredCandidate) {
          plan.classification = 'solvable_with_setup';
          plan.actions.push({ type: 'attach_mcp', serverName: configuredCandidate.name });
          plan.gaps.push('MCP "' + configuredCandidate.name + '" is configured globally but not yet bound in this workspace.');
        } else {
          var marketplaceServers = await fetchMarketplaceMCPServers();
          var mcpCandidate = chooseMarketplaceMCPServer(requirement, prompt, marketplaceServers);
          if (mcpCandidate) {
            var manualReason = getMCPManualConfigReason(mcpCandidate);
            if (manualReason) {
              plan.classification = 'user_setup_only';
              plan.summary = 'I found "' + mcpCandidate.name + '" for ' + requirement.label + ', but it ' + manualReason + '.';
              plan.gaps.push('Suggested MCP "' + mcpCandidate.name + '" needs manual configuration.');
            } else {
              plan.classification = 'solvable_with_setup';
              plan.actions.push({
                type: 'install_and_attach_mcp',
                serverName: mcpCandidate.name,
                candidate: mcpCandidate
              });
              plan.gaps.push('No matching MCP connector is currently bound in this workspace for ' + requirement.label + '.');
            }
          } else {
            plan.gaps.push('No matching MCP connector is available for ' + requirement.label + '.');
          }
        }
      }
    }

    if ((plan.classification === 'solvable_now' || plan.classification === 'solvable_with_setup') && (plan.gaps.length > 0 && plan.actions.length === 0)) {
      plan.classification = looksLikeImplementationRequest(prompt) ? 'implementation_request' : 'reusable_feature_gap';
      plan.actions = [{ type: 'offer_workspace' }];
    }

    if (plan.classification === 'solvable_with_setup') {
      plan.actions.push({ type: 'handoff' });
    }

    if (!plan.summary) {
      plan.summary = buildCapabilityPlanSummary(plan);
    }
    plan.brief = buildCapabilityImplementationBrief(plan, prompt);
    return plan;
  }

  async function createAgentFromCapabilityPlan(plan, prompt, intent) {
    var llmAvailability = await checkHomeAssistantLLMAvailability();
    if (!llmAvailability.available) {
      throw new Error(getHomeAssistantLLMRequirementMessage(llmAvailability));
    }

    var listData = await API.get('/api/agents');
    var existing = Array.isArray(listData && listData.agents) ? listData.agents : [];
    var existingNames = [];
    for (var i = 0; i < existing.length; i++) {
      var agentInfo = existing[i];
      existingNames.push(typeof agentInfo === 'string' ? agentInfo : agentInfo.name);
    }

    var requirement = plan && plan.requirement ? plan.requirement : null;
    var seedName = requirement && requirement.defaultAgentName
      ? requirement.defaultAgentName
      : (intent && intent.suggestedName) || 'Task Assistant';
    var description = buildAutoConfigDescription(prompt, intent || HOME_INTENTS.general_task);
    var autoConfig = await maybeLoadAutoConfig(description);
    var agentName = buildUniqueAgentName(
      autoConfig && autoConfig.agent_name ? autoConfig.agent_name : seedName,
      existingNames
    );
    var agentType = autoConfig && autoConfig.agent_type
      ? autoConfig.agent_type
      : (requirement && requirement.preferredAgentType) || (intent && intent.defaultType) || 'tool-calling';

    var payload = {
      name: agentName,
      type: agentType,
      system_prompt: autoConfig && autoConfig.system_prompt ? autoConfig.system_prompt : buildDefaultSystemPrompt(intent || HOME_INTENTS.general_task),
      description: autoConfig && autoConfig.description ? autoConfig.description : description,
      tags: uniqueValues(((intent && intent.tags) || []).concat(['auto-created', 'home-assistant', 'capability-plan']))
    };

    var selectedModel = await resolveAutoSelectedModel(payload.type, autoConfig && autoConfig.model);
    if (selectedModel) payload.model = selectedModel;
    if (autoConfig && typeof autoConfig.temperature === 'number') payload.temperature = autoConfig.temperature;

    if (!payload.model) {
      throw new Error('I could not auto-select a model for the planned agent.');
    }

    await API.post('/api/agents', payload);
    return agentName;
  }

  async function executeCapabilityPlan(plan, prompt, routeContext, appLaunchRequest) {
    if (!plan) return;

    setHomeAssistantBusy(true, 'Applying Plan...');
    renderHomeAssistantActions([]);
    appendHomeAssistantMessage('assistant', 'Applying the capability plan now.');

    var agentName = String(plan.targetAgentName || '').trim();
    var requirement = plan.requirement;

    try {
      for (var i = 0; i < plan.actions.length; i++) {
        var action = plan.actions[i];
        if (!action || !action.type || action.type === 'handoff') continue;

        if (action.type === 'create_agent') {
          agentName = await createAgentFromCapabilityPlan(plan, prompt, homeAssistantState.pendingIntent);
          homeAssistantState.pendingAgentName = agentName;
          appendHomeAssistantMessage('assistant', 'Created "' + agentName + '" for this capability plan.');
          continue;
        }

        if (action.type === 'install_skill_package') {
          var skillInstallOutcome = await installCapabilitySkillPackage(action.packageSpec);
          if (normalizeToken(skillInstallOutcome && skillInstallOutcome.status) !== 'installed') {
            throw new Error(skillInstallOutcome && skillInstallOutcome.message || 'Failed to install skill package.');
          }
          appendHomeAssistantMessage('assistant', skillInstallOutcome.message);
          continue;
        }

        if (action.type === 'enable_skill') {
          var enableSkillOutcome = await enableSkillForAgent(agentName, action.skillName);
          if (normalizeToken(enableSkillOutcome && enableSkillOutcome.status) !== 'enabled') {
            throw new Error(enableSkillOutcome && enableSkillOutcome.message || 'Failed to enable skill.');
          }
          appendHomeAssistantMessage('assistant', enableSkillOutcome.message);
          continue;
        }

        if (action.type === 'attach_mcp') {
          var attachOutcome = await bindMCPServerForRouteContext(agentName, action.serverName, routeContext);
          var attachStatus = normalizeToken(attachOutcome && attachOutcome.status);
          if (attachStatus !== 'already_bound' && attachStatus !== 'bound_existing') {
            throw new Error(attachOutcome && attachOutcome.message || 'Failed to bind MCP connector in this workspace.');
          }
          appendHomeAssistantMessage('assistant', attachOutcome.message);
          continue;
        }

        if (action.type === 'install_and_attach_mcp') {
          var installOutcome = await installMCPServerCandidate(buildEmailMCPCandidateFromRegistry(action.candidate));
          var installStatus = normalizeToken(installOutcome && installOutcome.status);
          if (installStatus !== 'installed' && installStatus !== 'already_installed') {
            throw new Error(installOutcome && installOutcome.message || 'Failed to install MCP server.');
          }
          var attachInstalledOutcome = await bindMCPServerForRouteContext(agentName, action.serverName, routeContext);
          var installedBindStatus = normalizeToken(attachInstalledOutcome && attachInstalledOutcome.status);
          if (installedBindStatus !== 'already_bound' && installedBindStatus !== 'bound_existing') {
            throw new Error(attachInstalledOutcome && attachInstalledOutcome.message || 'Failed to bind MCP connector in this workspace.');
          }
          appendHomeAssistantMessage('assistant', attachInstalledOutcome.message);
        }
      }

      homeAssistantState.pendingAgentName = agentName;
      homeAssistantState.pendingCapabilityPlan = null;
      homeAssistantState.pendingCapabilityBrief = '';

      if (requirement && requirement.canAnswerInline && matchedAgentCanAnswerInline(agentName)) {
        await runCapabilityTaskDirect(prompt, agentName, {
          routeContext: routeContext,
          intent: homeAssistantState.pendingIntent,
          appLaunchRequest: appLaunchRequest
        });
        return;
      }

      appendHomeAssistantMessage('assistant', 'The capability plan is ready. Handing off to chat now.');
      await runPendingTaskWithAgent(prompt, agentName, { appLaunchRequest: appLaunchRequest, routeContext: routeContext });
    } catch (error) {
      dashLog.debug('Capability plan execution failed', { error: error && error.message || error, plan: plan });
      appendHomeAssistantMessage('assistant', String(error && error.message || error || 'Capability plan failed.'));
      setHomeAssistantRoutingSummary('Capability Plan Failed', 'The capability plan did not complete.');
      renderHomeAssistantActions([
        {
          label: 'Show Plan',
          variant: 'primary',
          onClick: function () { appendHomeAssistantMessage('assistant', plan.brief); }
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

  function matchedAgentCanAnswerInline(agentName) {
    return Boolean(String(agentName || '').trim());
  }

  function renderCapabilityWorkspaceActions(plan, prompt, routeContext, appLaunchRequest) {
    var seed = buildCapabilityWorkspaceSeed(plan, prompt);
    setHomeAssistantRoutingSummary('Workspace Available', 'This looks like missing product capability. Create a workspace or review the plan.');
    renderHomeAssistantActions([
      {
        label: 'Create Workspace',
        variant: 'primary',
        onClick: function () {
          createWorkspaceFromPrompt(prompt, {
            agentName: plan && plan.targetAgentName ? plan.targetAgentName : '',
            appLaunchRequest: appLaunchRequest,
            routeContext: routeContext,
            seedNote: seed.seedNote,
            seedTask: seed.seedTask
          });
        }
      },
      {
        label: 'Show Plan',
        variant: 'secondary',
        onClick: function () { appendHomeAssistantMessage('assistant', plan.brief); }
      },
      {
        label: 'Not Now',
        variant: 'secondary',
        onClick: function () {
          appendHomeAssistantMessage('assistant', 'No problem. The implementation brief is ready whenever you want to turn this into workspace work.');
          focusHomeAssistantInput();
        }
      }
    ]);
  }

  function renderCapabilitySetupActions(plan, prompt, routeContext, appLaunchRequest) {
    setHomeAssistantRoutingSummary('Capability Plan', 'Review the proposed setup steps before continuing.');
    renderHomeAssistantActions([
      {
        label: 'Apply Plan',
        variant: 'primary',
        onClick: function () { executeCapabilityPlan(plan, prompt, routeContext, appLaunchRequest); }
      },
      {
        label: 'Show Plan',
        variant: 'secondary',
        onClick: function () { appendHomeAssistantMessage('assistant', plan.brief); }
      },
      {
        label: 'Continue Without Setup',
        variant: 'secondary',
        disabled: !plan.targetAgentName,
        onClick: function () { runPendingTaskWithAgent(prompt, plan.targetAgentName, { appLaunchRequest: appLaunchRequest, routeContext: routeContext }); }
      },
      {
        label: 'Not Now',
        variant: 'secondary',
        onClick: function () {
          appendHomeAssistantMessage('assistant', 'Okay. I kept the capability plan visible above.');
          focusHomeAssistantInput();
        }
      }
    ]);
  }

  async function handleCapabilityResolutionFlow(options) {
    var prompt = String(options && options.prompt || '').trim();
    var routeContext = options && options.routeContext ? options.routeContext : buildHomeRouteContext();
    var routeData = options && options.routeData ? options.routeData : null;
    var matchedAgentName = String(options && options.matchedAgentName || '').trim();
    var appLaunchRequest = options && options.appLaunchRequest ? options.appLaunchRequest : null;
    var capabilityCandidate = detectCapabilityRequirement(prompt, homeAssistantState.pendingIntent, homeAssistantState.pendingIntentVariant);
    if (homeAssistantState.pendingIntent.key === 'calendar_check' || capabilityCandidate || looksLikeImplementationRequest(prompt)) {
      setHomeAssistantRoutingSummary('Capability Planning', 'Checking available agents, skills, and MCP connectors.');
    }

    var plan = await planCapabilityResolution({
      prompt: prompt,
      intent: homeAssistantState.pendingIntent,
      intentVariant: homeAssistantState.pendingIntentVariant,
      routeContext: routeContext,
      routeData: routeData,
      matchedAgentName: matchedAgentName
    });
    if (!plan) return false;

    homeAssistantState.pendingCapabilityPlan = plan;
    homeAssistantState.pendingCapabilityBrief = plan.brief || '';

    if (plan.actions.length > 0 && plan.actions[0].type === 'pause_for_user_choice') {
      appendHomeAssistantMessage('assistant', plan.summary || 'Do you mean your personal calendar or scheduled tasks in this workspace?');
      setHomeAssistantRoutingSummary('Schedule Choice', 'Choose your calendar or this workspace schedule.');
      renderHomeAssistantActions([
        {
          label: 'My Calendar',
          variant: 'primary',
          onClick: async function () {
            homeAssistantState.pendingIntentVariant = 'personal_calendar';
            var followUpHandled = await handleCapabilityResolutionFlow({
              prompt: prompt,
              routeContext: routeContext,
              routeData: routeData,
              matchedAgentName: matchedAgentName,
              appLaunchRequest: appLaunchRequest
            });
            if (!followUpHandled && matchedAgentName) {
              await runPendingTaskWithAgent(prompt, matchedAgentName, { appLaunchRequest: appLaunchRequest, routeContext: routeContext });
            }
          }
        },
        {
          label: 'This Workspace',
          variant: 'secondary',
          onClick: function () {
            homeAssistantState.pendingIntentVariant = 'workspace_schedule';
            runWorkspaceScheduleSummaryDirect(prompt, routeContext);
          }
        },
        {
          label: 'Ask Another Task',
          variant: 'secondary',
          onClick: function () { focusHomeAssistantInput(); }
        }
      ]);
      return true;
    }

    if (plan.executionMode === 'workspace_schedule') {
      await runWorkspaceScheduleSummaryDirect(prompt, routeContext);
      return true;
    }

    if (plan.classification === 'solvable_now' && matchedAgentName && plan.requirement && plan.requirement.canAnswerInline) {
      await runCapabilityTaskDirect(prompt, matchedAgentName, {
        routeContext: routeContext,
        intent: homeAssistantState.pendingIntent,
        appLaunchRequest: appLaunchRequest
      });
      return true;
    }

    if (plan.classification === 'solvable_with_setup') {
      appendHomeAssistantMessage('assistant', plan.summary || buildCapabilityPlanSummary(plan));
      renderCapabilitySetupActions(plan, prompt, routeContext, appLaunchRequest);
      return true;
    }

    if (plan.classification === 'user_setup_only') {
      if (plan.intentVariant === 'workspace_schedule' && !hasWorkspaceRouteContext(routeContext)) {
        appendHomeAssistantMessage('assistant', plan.summary || 'I need a workspace before I can inspect scheduled tasks.');
        setHomeAssistantRoutingSummary('Workspace Needed', 'Choose or open a workspace before continuing.');
        renderHomeAssistantActions([
          {
            label: 'Open Workspaces',
            variant: 'primary',
            onClick: function () { window.location.href = '/workspaces'; }
          },
          {
            label: 'Ask Another Task',
            variant: 'secondary',
            onClick: function () { focusHomeAssistantInput(); }
          }
        ]);
        return true;
      }
      appendHomeAssistantMessage('assistant', plan.summary || 'This needs manual setup before I can continue.');
      setHomeAssistantRoutingSummary('Manual Setup Needed', 'Open MCP settings or review the implementation plan.');
      renderHomeAssistantActions([
        {
          label: 'Open MCP Page',
          variant: 'primary',
          onClick: function () { window.location.href = '/mcp'; }
        },
        {
          label: 'Show Plan',
          variant: 'secondary',
          onClick: function () { appendHomeAssistantMessage('assistant', plan.brief); }
        },
        {
          label: 'Continue Without Setup',
          variant: 'secondary',
          disabled: !matchedAgentName,
          onClick: function () { runPendingTaskWithAgent(prompt, matchedAgentName, { appLaunchRequest: appLaunchRequest, routeContext: routeContext }); }
        }
      ]);
      return true;
    }

    if (plan.classification === 'reusable_feature_gap' || plan.classification === 'implementation_request') {
      appendHomeAssistantMessage('assistant', plan.summary || buildCapabilityPlanSummary(plan));
      renderCapabilityWorkspaceActions(plan, prompt, routeContext, appLaunchRequest);
      return true;
    }

    return false;
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
    } else if (intent.key === 'calendar_check') {
      base = 'Create a calendar assistant that checks schedule availability, summarizes upcoming events, and answers calendar questions. It must default to read-only behavior and always use configured skills or MCP connectors before claiming lack of access.';
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
    if (intent.key === 'calendar_check') {
      return 'You are a calendar assistant. Use configured calendar skills and MCP connectors first, default to today when the user omits a range, and stay read-only unless the user explicitly asks to create or edit events.';
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

  function extractWorkspaceIdFromPath(pathname) {
    var path = String(pathname || '').trim();
    var match = path.match(/^\/workspaces\/([^/]+)/i);
    if (!match || !match[1]) return '';
    try {
      return decodeURIComponent(match[1]);
    } catch (error) {
      return match[1];
    }
  }

  function inferHomeRouteSurface(pathname) {
    var path = normalizeToken(pathname || '');
    if (!path) return 'dashboard';
    if (path.indexOf('/workspaces/') === 0) {
      if (path.indexOf('/canvas') >= 0) return 'workspace_canvas';
      return 'workspace_detail';
    }
    if (path.indexOf('/workspaces') === 0) return 'workspace_hub';
    if (path.indexOf('/chat') === 0) return 'chat';
    if (path.indexOf('/dashboard') === 0 || path === '/') return 'dashboard';
    return 'dashboard';
  }

  function buildHomeRouteContext() {
    var pathname = '';
    if (window.location && typeof window.location.pathname === 'string') {
      pathname = window.location.pathname;
    }
    var workspaceId = extractWorkspaceIdFromPath(pathname);
    var sessionId = getCurrentHomeSessionId();

    return {
      surface: inferHomeRouteSurface(pathname),
      page_path: pathname || '/',
      workspace_id: workspaceId,
      session_id: sessionId,
      origin: 'ask_ori'
    };
  }

  function normalizeHomeRouteContext(routeContext) {
    var fallback = buildHomeRouteContext();
    if (!routeContext || typeof routeContext !== 'object') return fallback;

    var pagePath = String(routeContext.page_path || fallback.page_path || '/').trim() || '/';
    var sessionId = routeContext.session_id;
    if (sessionId === undefined) {
      sessionId = getCurrentHomeSessionId();
    }

    return {
      surface: String(routeContext.surface || fallback.surface || inferHomeRouteSurface(pagePath)).trim() || inferHomeRouteSurface(pagePath),
      page_path: pagePath,
      workspace_id: String(routeContext.workspace_id || '').trim(),
      session_id: String(sessionId || '').trim(),
      origin: String(routeContext.origin || fallback.origin || 'ask_ori').trim() || 'ask_ori'
    };
  }

  function getCurrentHomeSessionId() {
    var manager = window.sessionManager;
    if (!manager) return '';
    if (typeof manager.getActiveSessionId === 'function') {
      var activeId = manager.getActiveSessionId();
      if (activeId) return String(activeId).trim();
    }
    if (manager.activeSessionId) return String(manager.activeSessionId).trim();
    if (manager.currentSessionId) return String(manager.currentSessionId).trim();
    return '';
  }

  function routeContextTargetsCurrentWorkspace(routeData, routeContext) {
    var mode = normalizeToken(routeData && routeData.route_mode);
    var target = normalizeToken(routeData && routeData.target_surface);
    return mode === 'workspace_task' && target === 'workspace' && hasWorkspaceRouteContext(routeContext);
  }

  function routePolicyRequiresSpecialist(routeData) {
    return normalizeToken(routeData && routeData.routing_policy) === 'specialist_required';
  }

  function isWorkspaceSpecialistIntent(routeData) {
    var intentKey = normalizeToken(routeData && routeData.intent);
    var intentVariant = normalizeToken(routeData && routeData.intent_variant);
    var routeMode = normalizeToken(routeData && routeData.route_mode);

    if (routeMode === 'utility_direct' || intentKey === 'utility_direct') {
      return true;
    }

    switch (intentKey) {
      case 'email_check':
      case 'app_launch':
        return true;
      case 'calendar_check':
        return intentVariant !== 'workspace_schedule';
      default:
        return false;
    }
  }

  function routeMatchesSystemAssistant(routeData) {
    var matchedAgent = normalizeToken(routeData && routeData.matched_agent);
    return matchedAgent === 'ori' || matchedAgent === 'system assistant';
  }

  function routeMatchesWorkspaceEntryAgent(routeData) {
    var matchedAgent = normalizeToken(routeData && routeData.matched_agent);
    var entryAgent = normalizeToken(homeAssistantState.workspaceEntryAgentName);
    if (!matchedAgent || !entryAgent) return false;
    return matchedAgent === entryAgent;
  }

  function shouldOpenWorkspaceAssistantForRoute(routeData, routeContext) {
    if (!hasWorkspaceRouteContext(routeContext)) return false;
    if (!routeData) return true;
    if (routeContextTargetsCurrentWorkspace(routeData, routeContext)) return true;
    if (isWorkspaceSpecialistIntent(routeData)) return false;
    if (routePolicyRequiresSpecialist(routeData)) return false;
    if (shouldAcceptBackendRouteMatch(routeData) &&
        !routeMatchesWorkspaceEntryAgent(routeData) &&
        !routeMatchesSystemAssistant(routeData)) {
      return false;
    }
    return true;
  }

  async function openWorkspaceAssistantForPrompt(prompt, routeContext, intent, options) {
    var assistantSessionResult = await runWorkspaceAssistantInline(prompt, routeContext, intent, options);
    if (!assistantSessionResult || !assistantSessionResult.session) {
      throw new Error('Failed to run workspace manager');
    }

    var entryLabel = assistantSessionResult.entryAgentName ? assistantSessionResult.entryAgentName : getWorkspaceHomeAssistantDisplayName();
    var sessionRouteContext = normalizeHomeRouteContext({
      surface: routeContext && routeContext.surface,
      page_path: routeContext && routeContext.page_path,
      workspace_id: routeContext && routeContext.workspace_id,
      session_id: assistantSessionResult.session && assistantSessionResult.session.id,
      origin: routeContext && routeContext.origin
    });
    var responseData = assistantSessionResult.responseData || null;
    var workflowStep = responseData && responseData.workflow_step ? responseData.workflow_step : null;
    var planningForm = responseData && responseData.planning_form ? responseData.planning_form : null;
    var taskAssistHandoffTask = null;
    if (assistantSessionResult.rawToolPayload) {
      clearHomeAssistantPlanning();
      homeAssistantState.inlineReplyState = null;
      renderHomeAssistantInlineReply();
      setHomeAssistantRoutingSummary(
        entryLabel,
        'The workspace manager returned raw tool data instead of a reply. Retry or open chat.'
      );
    } else if (workflowStep && String(workflowStep.step_type || '').trim() === 'ask_form' && workflowStep.form) {
      activateHomeAssistantPlanningForm(convertWorkflowFormToPlanningSchema(workflowStep), {
        prompt: prompt,
        routeContext: sessionRouteContext,
        intent: intent,
        agentLabel: entryLabel,
        workflowStep: workflowStep
      });
      setHomeAssistantRoutingSummary(
        entryLabel,
        'The workspace manager switched this into a guided planning step. Complete it below or open full chat.'
      );
    } else if (planningForm) {
      activateHomeAssistantPlanningForm(planningForm, {
        prompt: prompt,
        routeContext: sessionRouteContext,
        intent: intent,
        agentLabel: entryLabel
      });
      setHomeAssistantRoutingSummary(
        entryLabel,
        assistantSessionResult.reused
          ? 'Use the active planning step below, or open full chat if you want a longer back-and-forth.'
          : 'The workspace manager switched this into a guided planning step. Complete it below or open full chat.'
      );
    } else {
      var keepPlanningReview = Boolean(options && options.preservePlanningReview);
      if (!keepPlanningReview && homeAssistantState.planningState && homeAssistantState.planningState.kind === 'planning_review') {
        keepPlanningReview = true;
      }
      if (!keepPlanningReview) {
        clearHomeAssistantPlanning();
      }
      var inlineReplyState = enableHomeAssistantInlineReply(
        sessionRouteContext,
        intent,
        entryLabel,
        assistantSessionResult.responseText || '',
        workflowStep && String(workflowStep.step_type || '').trim() === 'ask_choice' ? workflowStep : null,
        options && options.linkedTask ? options.linkedTask : getActiveLinkedPlanningTask()
      );
      if (inlineReplyState && (!inlineReplyState.linkedTask || !inlineReplyState.linkedTask.id)) {
        try {
          var createdInlineTask = await ensureWorkspacePlanningTask(
            sessionRouteContext,
            prompt,
            entryLabel,
            {
              existingTask: inlineReplyState.linkedTask,
              summaryText: assistantSessionResult.responseText || '',
              fallbackTitle: workflowStep && String(workflowStep.title || '').trim()
                ? String(workflowStep.title || '').trim()
                : 'Planning task'
            }
          );
          if (createdInlineTask && homeAssistantState.inlineReplyState === inlineReplyState) {
            inlineReplyState.linkedTask = createdInlineTask;
            renderHomeAssistantInlineReply();
          }
        } catch (taskError) {
          dashLog.debug('Failed to create inline planning task', {
            error: taskError && taskError.message || taskError
          });
        }
      }
      if (inlineReplyState && inlineReplyState.linkedTask && inlineReplyState.linkedTask.id) {
        try {
          taskAssistHandoffTask = await persistPlanningSubtaskToTask(inlineReplyState);
        } catch (taskContextError) {
          dashLog.debug('Failed to persist planning subtask onto task', {
            error: taskContextError && taskContextError.message || taskContextError
          });
        }
      }
      var hasChoiceStep = inlineReplyState &&
        inlineReplyState.workflowStep &&
        String(inlineReplyState.workflowStep.step_type || '').trim() === 'ask_choice' &&
        Array.isArray(inlineReplyState.workflowStep.choices) &&
        inlineReplyState.workflowStep.choices.length > 0;
      setHomeAssistantRoutingSummary(
        entryLabel,
        hasChoiceStep
          ? 'The workspace manager created a planning subtask for this workspace. Complete it below, or open full chat.'
          : inlineReplyState
          ? assistantSessionResult.reused
            ? 'The manager needs one more planning answer. Review the subtask below, or open full chat.'
            : 'The workspace manager replied inline. Review the planning subtask below, or open full chat.'
          : assistantSessionResult.reused
          ? 'The workspace manager updated the workspace. Review the latest update below, or open full chat if you want to continue.'
          : 'The workspace manager replied with an update. Review the latest result below, or open full chat if you want to continue.'
      );
    }
    renderHomeAssistantDependencyResolution(
      assistantSessionResult.responseData,
      prompt,
      sessionRouteContext,
      intent,
      options
    );
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
    if (taskAssistHandoffTask) {
      handoffPlanningSubtaskToWorkspaceTaskModal(taskAssistHandoffTask, sessionRouteContext);
    }
  }

  function normalizeHomeAssistantDependencyResolution(data) {
    if (typeof normalizeDependencyResolution !== 'function') return null;
    try {
      return normalizeDependencyResolution(data);
    } catch (error) {
      dashLog.debug('Failed to normalize dependency resolution payload', { error: error && error.message || error });
      return null;
    }
  }

  function isLikelyHomeAssistantRequestTimeout(error) {
    if (!error) return false;
    var message = String(error && error.message || '').toLowerCase();
    var name = String(error && error.name || '').toLowerCase();
    if (name.indexOf('abort') !== -1) return true;
    if (message.indexOf('cancel') !== -1) return true;
    if (message.indexOf('abort') !== -1) return true;
    if (message.indexOf('timed out') !== -1) return true;
    if (message.indexOf('timeout') !== -1) return true;
    return false;
  }

  function normalizeWorkspaceManagerErrorMessage(error) {
    if (!error) return '';
    var direct = String(error && error.message || '').trim();
    if (direct) return direct;
    var fallback = String(error && error.error || '').trim();
    return fallback;
  }

  function buildWorkspaceManagerError(error, metadata) {
    var wrapped = error instanceof Error
      ? error
      : new Error(String(error || (metadata && metadata.message) || 'Workspace manager request failed'));
    if (metadata && metadata.stage) wrapped.homeAssistantStage = String(metadata.stage);
    if (metadata && metadata.message) wrapped.homeAssistantUserMessage = String(metadata.message);
    if (metadata && metadata.requestUrl && !wrapped.url) wrapped.url = String(metadata.requestUrl);
    return wrapped;
  }

  async function buildWorkspaceManagerResponseError(response, stage, message) {
    var detail = '';
    try {
      detail = await response.text();
    } catch (_error) {
      detail = '';
    }
    var err = new Error(detail || response.statusText || message || 'Request failed');
    err.status = Number(response && response.status || 0);
    err.url = String(response && response.url || '').trim();
    return buildWorkspaceManagerError(err, {
      stage: stage,
      message: message,
      requestUrl: err.url
    });
  }

  function formatWorkspaceManagerFailure(error, workspaceManagerLabel) {
    var label = String(workspaceManagerLabel || 'Workspace Manager').trim() || 'Workspace Manager';
    var stage = normalizeToken(error && error.homeAssistantStage || error && error.stage);
    var status = Number(error && error.status || 0);
    var url = String(error && error.url || '').trim();
    var rawMessage = normalizeWorkspaceManagerErrorMessage(error);
    var lowerMessage = rawMessage.toLowerCase();
    var browserOffline = typeof navigator !== 'undefined' && navigator.onLine === false;
    var networkFailure = browserOffline ||
      status === 0 ||
      lowerMessage.indexOf('network error') !== -1 ||
      lowerMessage.indexOf('failed to fetch') !== -1 ||
      lowerMessage.indexOf('load failed') !== -1;
    var detailParts = [];
    if (status > 0) {
      detailParts.push('HTTP ' + status + (url ? ' from ' + url : ''));
    } else if (url) {
      detailParts.push(url);
    }
    if (rawMessage) {
      detailParts.push(rawMessage);
    }
    var detail = detailParts.join(' — ');

    if (stage === 'client_session_manager_missing') {
      return {
        heading: label + ' Session UI Unavailable',
        title: 'Session UI Unavailable',
        text: 'This page could not open a workspace session in the browser.',
        detail: detail || 'Session manager was not initialized in this view.',
        conversationSummary: 'Open this only if you want the original prompt and browser-side error details.',
        state: 'error'
      };
    }

    if (stage === 'workspace_entry_session_create_failed' || stage === 'workspace_session_missing') {
      return {
        heading: label + ' Session Failed',
        title: 'Workspace Session Failed',
        text: 'Could not create or reuse the workspace manager session for this workspace.',
        detail: detail || 'The browser did not get a usable workspace session back.',
        conversationSummary: 'Open this only if you want the original prompt and session-creation error details.',
        state: 'error'
      };
    }

    if (stage === 'assistant_session_create_failed') {
      return {
        heading: 'Assistant Session Failed',
        title: 'Assistant Session Failed',
        text: 'Could not create the fallback assistant session for this workspace request.',
        detail: detail || 'The browser did not get a usable assistant session back.',
        conversationSummary: 'Open this only if you want the original prompt and assistant-session error details.',
        state: 'error'
      };
    }

    if (stage === 'inline_api_unavailable') {
      return {
        heading: label + ' Inline API Unavailable',
        title: 'Inline API Unavailable',
        text: 'This browser view could not send the inline workspace request.',
        detail: detail || 'The shared API client was not available in this page context.',
        conversationSummary: 'Open this only if you want the original prompt and client-side availability details.',
        state: 'error'
      };
    }

    if (networkFailure) {
      return {
        heading: 'Connection Failed',
        title: 'Connection Failed',
        text: 'Your browser could not reach the server while sending the workspace request.',
        detail: detail || (browserOffline ? 'Browser appears to be offline.' : 'Network request failed before the server replied.'),
        conversationSummary: 'Open this only if you want the original prompt and connection error details.',
        state: 'error'
      };
    }

    if (stage === 'inline_chat_failed' && status >= 500) {
      return {
        heading: label + ' Server Error',
        title: 'Server Error',
        text: 'The server returned an error while running the workspace agent inline.',
        detail: detail || 'The inline /api/chat request failed on the server.',
        conversationSummary: 'Open this only if you want the original prompt and server error details.',
        state: 'error'
      };
    }

    if (stage === 'inline_chat_failed' && status >= 400) {
      return {
        heading: label + ' Request Rejected',
        title: 'Request Rejected',
        text: 'The server rejected the inline workspace request.',
        detail: detail || 'The inline /api/chat request returned a client error.',
        conversationSummary: 'Open this only if you want the original prompt and request error details.',
        state: 'error'
      };
    }

    if (stage === 'inline_chat_failed') {
      return {
        heading: label + ' Inline Request Failed',
        title: 'Inline Request Failed',
        text: 'The workspace session opened, but the inline manager request did not complete.',
        detail: detail || 'The inline /api/chat request failed before a usable reply arrived.',
        conversationSummary: 'Open this only if you want the original prompt and inline-request error details.',
        state: 'error'
      };
    }

    if (status >= 500) {
      return {
        heading: label + ' Server Error',
        title: 'Server Error',
        text: 'The server returned an error before the workspace flow completed.',
        detail: detail || 'A server-side error interrupted the request.',
        conversationSummary: 'Open this only if you want the original prompt and server error details.',
        state: 'error'
      };
    }

    if (status >= 400) {
      return {
        heading: label + ' Request Failed',
        title: 'Request Failed',
        text: 'The request was rejected before the workspace flow completed.',
        detail: detail || 'A client-side request error interrupted the flow.',
        conversationSummary: 'Open this only if you want the original prompt and request error details.',
        state: 'error'
      };
    }

    return {
      heading: label + ' Request Failed',
      title: 'Request Failed',
      text: 'The workspace flow did not complete.',
      detail: detail || 'An unexpected error interrupted the inline handoff.',
      conversationSummary: 'Open this only if you want the original prompt and error details.',
      state: 'error'
    };
  }

  function renderHomeAssistantDependencyResolution(data, prompt, routeContext, intent, options) {
    var resolution = normalizeHomeAssistantDependencyResolution(data);
    if (!resolution || typeof renderDependencyResolutionModal !== 'function') {
      return false;
    }

    return renderDependencyResolutionModal(resolution, data, prompt, false, {
      retry: async function () {
        await openWorkspaceAssistantForPrompt(prompt, routeContext, intent, options);
      }
    });
  }

  async function routePromptWithBackend(prompt, routeContext) {
    if (typeof API === 'undefined' || typeof API.post !== 'function') return null;
    try {
      var normalizedContext = normalizeHomeRouteContext(routeContext);
      var data = await API.post('/api/home-assistant/route', {
        prompt: prompt,
        context: normalizedContext
      });
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
      return { status: 'unavailable', reason: 'modal_prerequisites_missing' };
    }

    var nameInput = document.getElementById('agentName');
    var typeInput = document.getElementById('agentType');
    var modelInput = document.getElementById('agentModel');
    var tempInput = document.getElementById('agentTemperature');
    var tempValue = document.getElementById('temperatureValue');
    var promptInput = document.getElementById('agentSystemPrompt');
    var allowWebSearchInput = document.getElementById('agentAllowWebSearch');
    if (!nameInput || !typeInput || !modelInput || !tempInput || !promptInput) {
      return { status: 'unavailable', reason: 'modal_fields_missing' };
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
    closeHomeAssistantThinkingModal({ force: true });
    modalInstance.show();

    return await new Promise(function (resolve) {
      var settled = false;
      var submitting = false;
      var originalHtml = createButton.innerHTML;

      function finalize(result) {
        if (settled) return;
        settled = true;
        createButton.disabled = false;
        createButton.innerHTML = originalHtml;
        createButton.removeEventListener('click', onCreateCapture, true);
        form.removeEventListener('submit', onSubmitCapture, true);
        modalElement.removeEventListener('hidden.bs.modal', onHidden, true);
        resolve(result || { status: 'cancelled' });
      }

      function onHidden() {
        finalize({ status: 'cancelled' });
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
          finalize({ status: 'created', agentName: name });
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
        description: autoConfig && autoConfig.description ? autoConfig.description : description,
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
        setHomeAssistantBusy(false);
        var semiAutoConfirmation = await confirmAgentCreationWithModal(payload);
        if (!semiAutoConfirmation || semiAutoConfirmation.status !== 'created') {
          if (semiAutoConfirmation && semiAutoConfirmation.status === 'unavailable') {
            appendHomeAssistantMessage('assistant', 'I could not open the Create Agent modal. Please open Agents and create it manually.');
            setHomeAssistantRoutingSummary('Agent Creation', 'Create Agent modal was unavailable.');
          } else {
            appendHomeAssistantMessage('assistant', 'Agent creation canceled. Ask again when you want to continue.');
            setHomeAssistantRoutingSummary('Agent Creation', 'Agent creation canceled.');
          }
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
        agentName = semiAutoConfirmation.agentName;
        setHomeAssistantBusy(true, 'Finalizing...');
      } else if (!payload.model) {
        appendHomeAssistantMessage('assistant',
          'I could not auto-select a model. Please review and confirm in the Create Agent modal.');
        setHomeAssistantRoutingSummary('Agent Creation', 'Model selection needs your confirmation.');
        setHomeAssistantBusy(false);
        var confirmation = await confirmAgentCreationWithModal(payload);
        if (!confirmation || confirmation.status !== 'created') {
          if (confirmation && confirmation.status === 'unavailable') {
            appendHomeAssistantMessage('assistant', 'I could not open the Create Agent modal. Please open Agents and create it manually.');
            setHomeAssistantRoutingSummary('Agent Creation', 'Create Agent modal was unavailable.');
          } else {
            appendHomeAssistantMessage('assistant', 'Agent creation canceled. Ask again when you want to continue.');
            setHomeAssistantRoutingSummary('Agent Creation', 'Agent creation canceled.');
          }
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
        agentName = confirmation.agentName;
        setHomeAssistantBusy(true, 'Finalizing...');
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
      var routeContext = buildHomeRouteContext();
      var addedToWorkspace = await addAgentToWorkspaceIfNeeded(agentName, routeContext);
      if (addedToWorkspace) {
        appendHomeAssistantMessage('assistant', 'Added "' + agentName + '" to this workspace.');
      }

      var createdAgentMCP = await ensureMCPForTask(
        agentName,
        prompt,
        intent.key === 'email_check'
          ? { allowMutations: false, routeContext: routeContext }
          : { routeContext: routeContext }
      );
      if (createdAgentMCP && createdAgentMCP.message) {
        appendHomeAssistantMessage('assistant', createdAgentMCP.message);
      }
      if (intent.key === 'email_check') {
        var createdEmailMCPResolution = await maybeResolveEmailMCPBeforeHandoff(agentName, prompt, createdAgentMCP, routeContext);
        if (!createdEmailMCPResolution || !createdEmailMCPResolution.continueHandoff) {
          return;
        }
      }

      if (isSemiAutoMode()) {
        appendHomeAssistantMessage('assistant', 'Agent is ready. Handing off this task to chat now.');
        setHomeAssistantRoutingSummary('Semi-auto', '"' + agentName + '" is ready. Handing off to chat.');
      }
      await runPendingTaskWithAgent(prompt, agentName, { appLaunchRequest: appLaunchRequest, routeContext: routeContext });

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

  function findSessionForAgentInWorkspace(agentName, workspaceId) {
    var manager = window.sessionManager;
    if (!manager || !Array.isArray(manager.sessions) || !agentName) return null;

    var targetAgent = normalizeToken(agentName);
    var targetWorkspace = String(workspaceId || '').trim();
    for (var i = 0; i < manager.sessions.length; i++) {
      var session = manager.sessions[i];
      if (normalizeToken(session && session.agent_name) !== targetAgent) continue;
      if (String(session && session.folder_id || '').trim() === targetWorkspace) {
        return session;
      }
    }
    return null;
  }

  function findSessionById(sessionId) {
    var manager = window.sessionManager;
    if (!manager || !Array.isArray(manager.sessions) || !sessionId) return null;
    for (var i = 0; i < manager.sessions.length; i++) {
      var session = manager.sessions[i];
      if (String(session && session.id) === String(sessionId)) return session;
    }
    return null;
  }

  async function submitHomeAssistantPlanningForm() {
    var planningState = homeAssistantState.planningState;
    if (!planningState || planningState.kind !== 'planning_form' || planningState.submitting) return;

    var missingQuestion = validatePlanningFormState(planningState);
    if (missingQuestion) {
      planningState.focusField = missingQuestion.id;
      renderHomeAssistantPlanning();
      if (window.Toast) {
        Toast.warning('Answer "' + missingQuestion.label + '" before continuing.');
      }
      return;
    }

    var submittingState = planningState;
    submittingState.submitting = true;
    renderHomeAssistantPlanning();
    appendHomeAssistantMessage('user', buildPlanningFormAnswerSummary(submittingState));
    activateHomeAssistantPlanningReview(submittingState);
    setHomeAssistantRoutingSummary(
      'Planning Review',
      'Review the intake summary, save it to a note if needed, and choose the next step.'
    );
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
  }

  async function openOrCreateWorkspaceAssistantSession(routeContext, prompt, options) {
    var manager = window.sessionManager;
    if (!manager) {
      throw buildWorkspaceManagerError(
        new Error('Session manager is unavailable in this browser view.'),
        { stage: 'client_session_manager_missing' }
      );
    }

    var normalizedContext = normalizeHomeRouteContext(routeContext);
    var workspaceId = hasWorkspaceRouteContext(normalizedContext) ? String(normalizedContext.workspace_id).trim() : '';
    var reuseExistingSession = Boolean(options && options.reuseExistingSession);
    var requestedSessionId = reuseExistingSession
      ? String(normalizedContext.session_id || '').trim()
      : '';
    var entryAgentName = workspaceId ? await fetchWorkspaceEntryAgentName(workspaceId) : '';
    if (entryAgentName) {
      if (reuseExistingSession) {
        var currentEntrySession = findSessionById(requestedSessionId || getCurrentHomeSessionId());
        if (currentEntrySession &&
            normalizeToken(currentEntrySession.agent_name) === normalizeToken(entryAgentName) &&
            String(currentEntrySession.folder_id || '').trim() === workspaceId) {
          return { session: currentEntrySession, reused: true, entryAgentName: entryAgentName };
        }

        var existingEntrySession = findSessionForAgentInWorkspace(entryAgentName, workspaceId);
        if (existingEntrySession && existingEntrySession.id) {
          if (typeof manager.switchToSession === 'function') {
            await manager.switchToSession(existingEntrySession.id, false);
          }
          return { session: existingEntrySession, reused: true, entryAgentName: entryAgentName };
        }
      }

      var entrySession = null;
      if (typeof manager.createSessionWithAgentInFolder === 'function') {
        try {
          entrySession = await manager.createSessionWithAgentInFolder(entryAgentName, workspaceId, false);
        } catch (error) {
          throw buildWorkspaceManagerError(error, {
            stage: 'workspace_entry_session_create_failed',
            message: 'Could not create a workspace manager session.'
          });
        }
      } else {
        var entryResponse = await fetch('/api/sessions', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            folder_id: workspaceId,
            title: truncateText(String(prompt || '').trim(), 50) || 'Workspace',
            agent_name: entryAgentName
          })
        });
        if (!entryResponse.ok) {
          throw await buildWorkspaceManagerResponseError(
            entryResponse,
            'workspace_entry_session_create_failed',
            'Could not create a workspace manager session.'
          );
        }
        entrySession = await entryResponse.json();
        if (entrySession && entrySession.id && manager && typeof manager.switchToSession === 'function') {
          await manager.switchToSession(entrySession.id, false);
        }
      }

      if (!entrySession) {
        throw buildWorkspaceManagerError(
          new Error('Workspace manager session returned no session object.'),
          {
            stage: 'workspace_entry_session_create_failed',
            message: 'Could not create a workspace manager session.'
          }
        );
      }
      return { session: entrySession.session || entrySession, reused: false, entryAgentName: entryAgentName };
    }

    var title = truncateText(String(prompt || '').trim(), 50) || 'Assistant';
    var created = null;
    if (typeof manager.createAssistantSession === 'function') {
      try {
        created = await manager.createAssistantSession(workspaceId, title, false);
      } catch (error) {
        throw buildWorkspaceManagerError(error, {
          stage: 'assistant_session_create_failed',
          message: 'Could not create the fallback workspace assistant session.'
        });
      }
    } else if (window.workspaceDetail && typeof window.workspaceDetail.createSimpleSession === 'function' && workspaceId) {
      try {
        created = await window.workspaceDetail.createSimpleSession(false);
      } catch (error) {
        throw buildWorkspaceManagerError(error, {
          stage: 'assistant_session_create_failed',
          message: 'Could not create the fallback workspace assistant session.'
        });
      }
    } else {
      var response = await fetch('/api/sessions', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          folder_id: workspaceId,
          title: title
        })
      });
      if (!response.ok) {
        throw await buildWorkspaceManagerResponseError(
          response,
          'assistant_session_create_failed',
          'Could not create the fallback workspace assistant session.'
        );
      }
      created = await response.json();
      if (created && created.id && manager && typeof manager.switchToSession === 'function') {
        await manager.switchToSession(created.id, false);
      }
    }

    if (!created) {
      throw buildWorkspaceManagerError(
        new Error('Assistant session returned no session object.'),
        {
          stage: 'assistant_session_create_failed',
          message: 'Could not create the fallback workspace assistant session.'
        }
      );
    }
    return { session: created.session || created, reused: false };
  }

  async function dispatchPromptToWorkspaceAssistantSession(prompt, routeContext) {
    var result = await openOrCreateWorkspaceAssistantSession(routeContext, prompt, { reuseExistingSession: false });
    if (!result || !result.session) return null;

    openChatPanel();
    if (prompt && typeof window.sendMessageToChat === 'function') {
      await waitForDelay(120);
      await window.sendMessageToChat(prompt, {
        routeContext: normalizeHomeRouteContext({
          surface: routeContext && routeContext.surface,
          page_path: routeContext && routeContext.page_path,
          workspace_id: routeContext && routeContext.workspace_id,
          session_id: result.session.id,
          origin: routeContext && routeContext.origin
        })
      });
    }

    return result;
  }

  async function runWorkspaceAssistantInline(prompt, routeContext, intent, options) {
    var result = await openOrCreateWorkspaceAssistantSession(routeContext, prompt, options);
    if (!result || !result.session) {
      throw buildWorkspaceManagerError(
        new Error('Workspace manager session could not be opened.'),
        { stage: 'workspace_session_missing' }
      );
    }
    if (typeof API === 'undefined' || typeof API.post !== 'function') {
      throw buildWorkspaceManagerError(
        new Error('Inline chat API is unavailable in this browser view.'),
        { stage: 'inline_api_unavailable' }
      );
    }

    var session = result.session;
    var entryLabel = result.entryAgentName ? result.entryAgentName : getWorkspaceHomeAssistantDisplayName();
    var dispatchMessage = options && typeof options.dispatchPrompt === 'string'
      ? String(options.dispatchPrompt || '').trim()
      : buildWorkspaceManagerDispatchMessage(prompt, routeContext, intent);
    var requestContext = normalizeHomeRouteContext({
      surface: routeContext && routeContext.surface,
      page_path: routeContext && routeContext.page_path,
      workspace_id: routeContext && routeContext.workspace_id,
      session_id: session.id,
      origin: routeContext && routeContext.origin
    });
    var workflowResponse = options && options.workflowResponse ? options.workflowResponse : null;
    var payload = {
      question: workflowResponse ? '' : dispatchMessage,
      agent_name: String(result.entryAgentName || session.agent_name || '').trim(),
      route_context: requestContext
    };
    if (workflowResponse) {
      payload.workflow_response = workflowResponse;
    }

    var data;
    try {
      data = await API.post('/api/chat', payload, {
        timeout: HOME_ASSISTANT_WORKSPACE_INLINE_TIMEOUT_MS,
        headers: {
          'Content-Type': 'application/json',
          'X-Session-ID': String(session.id)
        }
      });
    } catch (error) {
      throw buildWorkspaceManagerError(error, {
        stage: 'inline_chat_failed',
        message: 'The workspace manager session opened, but the inline request failed.',
        requestUrl: '/api/chat'
      });
    }

    var responseText = String(data && data.response || '').trim();
    var rawToolPayload = isLikelyHomeAssistantRawToolPayload(responseText, data);
    if (rawToolPayload) {
      responseText = entryLabel + ' checked the workspace context but returned raw tool data instead of a reply. Please retry or open chat.';
    }
    if (!responseText) {
      responseText = entryLabel + ' is ready for your next answer in chat.';
    }

    trackHomeAssistantSession(session, prompt, entryLabel);
    setHomeAssistantMode('continue_session');
    appendHomeAssistantMessage('assistant', responseText);
    result.responseData = data;
    result.responseText = responseText;
    result.rawToolPayload = rawToolPayload;

    return result;
  }

  async function openOrCreateChatSession(agentName, routeContext) {
    var manager = window.sessionManager;
    if (!manager) return null;
    var workspaceId = hasWorkspaceRouteContext(routeContext) ? String(routeContext.workspace_id).trim() : '';

    if (workspaceId && typeof manager.createSessionWithAgentInFolder === 'function') {
      await manager.createSessionWithAgentInFolder(agentName, workspaceId);
      if (manager.activeSessionId) {
        var workspaceSession = findSessionById(manager.activeSessionId);
        if (workspaceSession && workspaceSession.id) return workspaceSession;
      }
      var matchedWorkspaceSession = findSessionForAgent(agentName);
      if (matchedWorkspaceSession && matchedWorkspaceSession.id) return matchedWorkspaceSession;
    }

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

  async function addAgentToWorkspaceIfNeeded(agentName, routeContext) {
    var workspaceId = hasWorkspaceRouteContext(routeContext) ? String(routeContext.workspace_id).trim() : '';
    if (!workspaceId || !agentName || typeof API === 'undefined' || typeof API.post !== 'function') {
      return false;
    }

    try {
      await API.post('/api/workspaces/' + encodeURIComponent(workspaceId) + '/agents', {
        agent_name: agentName
      });

      if (window.workspaceDetail &&
          String(window.workspaceDetail.workspaceId || '').trim() === workspaceId &&
          typeof window.workspaceDetail.loadWorkspace === 'function') {
        window.workspaceDetail.loadWorkspace();
      }

      return true;
    } catch (error) {
      dashLog.debug('Failed to attach created agent to workspace', {
        workspaceId: workspaceId,
        agentName: agentName,
        error: error && error.message || error
      });
      return false;
    }
  }

  async function dispatchPromptToChatSession(prompt, agentName, routeContext) {
    if (!prompt || !agentName) return null;
    var session = await openOrCreateChatSession(agentName, routeContext);
    if (!session) return null;
    openChatPanel();
    if (typeof window.sendMessageToChat !== 'function') return null;
    await waitForDelay(120);
    await window.sendMessageToChat(prompt, { routeContext: normalizeHomeRouteContext(routeContext) });
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

  async function runCapabilityTaskDirect(prompt, agentName, options) {
    if (!prompt || !agentName) return;
    var routeContext = options && options.routeContext ? options.routeContext : buildHomeRouteContext();
    var dispatchIntent = options && options.intent ? options.intent : homeAssistantState.pendingIntent;
    var appLaunchRequest = options && options.appLaunchRequest ? options.appLaunchRequest : null;
    var dispatchMessage = buildAskOriDispatchMessage(prompt, appLaunchRequest, dispatchIntent, routeContext);

    setHomeAssistantBusy(true, 'Running Capability...');
    renderHomeAssistantActions([]);
    appendHomeAssistantMessage('assistant', 'Using the current capability setup to answer this directly.');
    setHomeAssistantRoutingSummary('Capability Direct', 'Running the request inline with the selected agent.');

    try {
      var data = await API.post('/api/chat', {
        question: dispatchMessage,
        agent_name: agentName,
        route_context: normalizeHomeRouteContext(routeContext)
      });
      var responseText = String(data && data.response || '').trim();
      if (!responseText) {
        responseText = 'The capability ran, but no text response was returned.';
      }

      appendHomeAssistantMessage('assistant', responseText);
      setHomeAssistantRoutingSummary('Capability Direct', 'Completed inline with "' + agentName + '".');
      renderHomeAssistantActions([
        {
          label: 'Continue in Chat',
          variant: 'primary',
          onClick: function () { runPendingTaskWithAgent(prompt, agentName, options); }
        },
        {
          label: 'Ask Another Task',
          variant: 'secondary',
          onClick: function () { focusHomeAssistantInput(); }
        }
      ]);
    } catch (error) {
      dashLog.debug('Direct capability execution failed', { error: error && error.message || error, agent: agentName });
      appendHomeAssistantMessage('assistant', 'I could not run that capability directly right now.');
      setHomeAssistantRoutingSummary('Capability Direct Failed', 'Direct execution failed.');
      renderHomeAssistantActions([
        {
          label: 'Retry',
          variant: 'primary',
          onClick: function () { runCapabilityTaskDirect(prompt, agentName, options); }
        },
        {
          label: 'Continue in Chat',
          variant: 'secondary',
          onClick: function () { runPendingTaskWithAgent(prompt, agentName, options); }
        }
      ]);
    } finally {
      setHomeAssistantBusy(false);
    }
  }

  function formatWorkspaceScheduleTask(task) {
    if (!task || typeof task !== 'object') return 'Scheduled task';
    if (task.schedule_summary) return String(task.schedule_summary);
    if (task.schedule_expression) return String(task.schedule_expression);
    if (task.schedule_name) return String(task.schedule_name);
    if (task.schedule && typeof task.schedule === 'object') {
      if (task.schedule.description) return String(task.schedule.description);
      if (task.schedule.expression) return String(task.schedule.expression);
      if (task.schedule.cron) return String(task.schedule.cron);
      if (task.schedule.type) return String(task.schedule.type).replace(/_/g, ' ');
    }
    if (task.schedule_type) return String(task.schedule_type).replace(/_/g, ' ');
    return 'Scheduled';
  }

  function formatWorkspaceScheduleNextRun(value) {
    var raw = String(value || '').trim();
    if (!raw) return 'not scheduled';
    var parsed = new Date(raw);
    if (Number.isNaN(parsed.getTime())) return raw;
    return parsed.toLocaleString();
  }

  async function runWorkspaceScheduleSummaryDirect(prompt, routeContext) {
    var activeContext = normalizeHomeRouteContext(routeContext);
    var workspaceId = String(activeContext && activeContext.workspace_id || '').trim();
    if (!workspaceId) {
      appendHomeAssistantMessage('assistant', 'I need a workspace before I can inspect scheduled tasks.');
      setHomeAssistantRoutingSummary('Workspace Needed', 'Choose or open a workspace before continuing.');
      renderHomeAssistantActions([
        {
          label: 'Open Workspaces',
          variant: 'primary',
          onClick: function () { window.location.href = '/workspaces'; }
        },
        {
          label: 'Ask Another Task',
          variant: 'secondary',
          onClick: function () { focusHomeAssistantInput(); }
        }
      ]);
      return;
    }

    setHomeAssistantBusy(true, 'Checking Workspace Schedule...');
    renderHomeAssistantActions([]);
    appendHomeAssistantMessage('assistant', 'Checking scheduled tasks in the current workspace.');
    setHomeAssistantRoutingSummary('Workspace Schedule', 'Loading scheduled tasks for this workspace.');

    try {
      var data = await API.get('/api/orchestration/tasks?workspace_id=' + encodeURIComponent(workspaceId));
      var tasks = Array.isArray(data && data.tasks) ? data.tasks : [];
      var scheduledTasks = tasks.filter(function (task) {
        return Boolean(task && (task.schedule_enabled || task.schedule || task.next_run || task.schedule_type || task.schedule_expression));
      });

      scheduledTasks.sort(function (a, b) {
        var left = String(a && a.next_run || '').trim();
        var right = String(b && b.next_run || '').trim();
        if (!left && !right) return 0;
        if (!left) return 1;
        if (!right) return -1;
        return new Date(left).getTime() - new Date(right).getTime();
      });

      if (scheduledTasks.length === 0) {
        appendHomeAssistantMessage('assistant', 'This workspace does not have any scheduled tasks yet.');
        setHomeAssistantRoutingSummary('Workspace Schedule', 'No scheduled tasks found.');
        renderHomeAssistantActions([
          {
            label: 'Open Workspace',
            variant: 'primary',
            onClick: function () { window.location.href = '/workspaces/' + encodeURIComponent(workspaceId); }
          },
          {
            label: 'Ask Another Task',
            variant: 'secondary',
            onClick: function () { focusHomeAssistantInput(); }
          }
        ]);
        return;
      }

      var enabledCount = scheduledTasks.filter(function (task) {
        return Boolean(task && task.schedule_enabled);
      }).length;
      var lines = [
        'Scheduled tasks in this workspace: ' + scheduledTasks.length,
        'Enabled schedules: ' + enabledCount,
        ''
      ];
      for (var i = 0; i < scheduledTasks.length && i < 5; i++) {
        var task = scheduledTasks[i];
        var description = String(task && (task.description || task.name || task.id) || 'Untitled task').trim();
        lines.push(
          (i + 1) + '. ' + description +
          ' | ' + formatWorkspaceScheduleTask(task) +
          ' | next run: ' + formatWorkspaceScheduleNextRun(task && task.next_run)
        );
      }
      if (scheduledTasks.length > 5) {
        lines.push('');
        lines.push((scheduledTasks.length - 5) + ' more scheduled task(s) available in the workspace.');
      }

      appendHomeAssistantMessage('assistant', lines.join('\n'));
      setHomeAssistantRoutingSummary('Workspace Schedule', 'Scheduled task summary is ready.');
      renderHomeAssistantActions([
        {
          label: 'Open Workspace',
          variant: 'primary',
          onClick: function () { window.location.href = '/workspaces/' + encodeURIComponent(workspaceId); }
        },
        {
          label: 'Continue in Chat',
          variant: 'secondary',
          disabled: !homeAssistantState.pendingAgentName,
          onClick: function () {
            if (!homeAssistantState.pendingAgentName) return;
            runPendingTaskWithAgent(prompt, homeAssistantState.pendingAgentName, { routeContext: activeContext });
          }
        },
        {
          label: 'Ask Another Task',
          variant: 'secondary',
          onClick: function () { focusHomeAssistantInput(); }
        }
      ]);
    } catch (error) {
      dashLog.debug('Workspace schedule summary failed', { error: error && error.message || error, workspaceId: workspaceId });
      appendHomeAssistantMessage('assistant', 'I could not load scheduled tasks for this workspace right now.');
      setHomeAssistantRoutingSummary('Workspace Schedule Failed', 'Could not load scheduled tasks.');
      renderHomeAssistantActions([
        {
          label: 'Retry',
          variant: 'primary',
          onClick: function () { runWorkspaceScheduleSummaryDirect(prompt, activeContext); }
        },
        {
          label: 'Open Workspace',
          variant: 'secondary',
          onClick: function () { window.location.href = '/workspaces/' + encodeURIComponent(workspaceId); }
        }
      ]);
    } finally {
      setHomeAssistantBusy(false);
    }
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

  async function createWorkspaceChatSessionWithMessage(workspaceId, message) {
    var initialMessage = String(message || '').trim();
    var baseContext = buildHomeRouteContext();
    var routeContext = normalizeHomeRouteContext({
      surface: baseContext.surface,
      page_path: baseContext.page_path,
      workspace_id: workspaceId,
      origin: 'workspace_chat'
    });
    var result = await dispatchPromptToWorkspaceAssistantSession(initialMessage, routeContext);
    return Boolean(result && result.session && result.session.id);
  }

  function hasRecurringScheduleLanguage(prompt) {
    var text = normalizeToken(prompt);
    if (!text) return false;
    if (/\b(?:daily|weekly|monthly|weekdays|weekends|recurring)\b/.test(text)) return true;
    if (/\bevery\s+(?:day|week|month|weekday|weekend|monday|tuesday|wednesday|thursday|friday|saturday|sunday)\b/.test(text)) return true;
    if (/\bevery\s+\d+\s*(?:min|mins|minute|minutes|hour|hours|day|days)\b/.test(text)) return true;
    return false;
  }

  function promptHasSchedulingIntent(prompt) {
    var text = normalizeToken(prompt);
    if (!text) return false;
    if (hasRecurringScheduleLanguage(text)) return true;
    if (/\b(?:schedule|scheduled|remind me|reminder)\b/.test(text)) return true;
    if (/\b(?:today|tomorrow|tonight|next\s+(?:monday|tuesday|wednesday|thursday|friday|saturday|sunday))\b/.test(text)) return true;
    if (/\b(?:at|@)\s*\d{1,2}(?::\d{2})?\s*(?:am|pm)\b/i.test(String(prompt || ''))) return true;
    if (/\b(?:at|@)\s*\d{1,2}:\d{2}\b/i.test(String(prompt || ''))) return true;
    return false;
  }

  function parseClockTimeFromText(prompt) {
    var raw = String(prompt || '');
    var match = raw.match(/\b(?:at|@)\s*(\d{1,2})(?::(\d{2}))?\s*(am|pm)?\b/i);
    if (!match) {
      match = raw.match(/\b(\d{1,2}):(\d{2})\s*(am|pm)?\b/i);
    }
    if (!match) return null;

    var hour = Number(match[1]);
    var minute = Number(match[2] || 0);
    var meridiem = normalizeToken(match[3] || '');
    if (!Number.isFinite(hour) || !Number.isFinite(minute)) return null;
    if (minute < 0 || minute > 59) return null;

    if (meridiem === 'am' || meridiem === 'pm') {
      if (hour < 1 || hour > 12) return null;
      if (hour === 12) hour = 0;
      if (meridiem === 'pm') hour += 12;
    } else {
      if (hour === 24 && minute === 0) {
        hour = 0;
      } else if (hour < 0 || hour > 23) {
        return null;
      }
    }

    var hh = String(hour).padStart(2, '0');
    var mm = String(minute).padStart(2, '0');
    return {
      hour24: hour,
      minute: minute,
      hhmm: hh + ':' + mm
    };
  }

  function normalizeScheduleDayOfWeek(value) {
    if (Number.isInteger(value) && value >= 0 && value <= 6) return value;
    var parsed = Number(value);
    if (Number.isFinite(parsed) && parsed >= 0 && parsed <= 6) return Math.floor(parsed);

    var day = normalizeToken(value);
    var map = {
      sunday: 0,
      monday: 1,
      tuesday: 2,
      wednesday: 3,
      thursday: 4,
      friday: 5,
      saturday: 6
    };
    if (Object.prototype.hasOwnProperty.call(map, day)) {
      return map[day];
    }
    return 0;
  }

  function formatScheduleTimeLabel(hhmm) {
    var value = String(hhmm || '').trim();
    var match = value.match(/^(\d{1,2}):(\d{2})$/);
    if (!match) return value || '09:00';
    var hour = Number(match[1]);
    var minute = Number(match[2]);
    if (!Number.isFinite(hour) || !Number.isFinite(minute)) return value;
    var suffix = hour >= 12 ? 'PM' : 'AM';
    var hour12 = hour % 12;
    if (hour12 === 0) hour12 = 12;
    return hour12 + ':' + String(minute).padStart(2, '0') + ' ' + suffix;
  }

  function buildNextRunAtISO(hour, minute, options) {
    var now = new Date();
    var target = new Date(now.getTime());
    target.setSeconds(0, 0);
    target.setHours(hour, minute, 0, 0);

    var forceTomorrow = Boolean(options && options.forceTomorrow);
    var forceToday = Boolean(options && options.forceToday);
    if (forceTomorrow) {
      target.setDate(target.getDate() + 1);
    } else if (!forceToday && target.getTime() <= now.getTime()) {
      target.setDate(target.getDate() + 1);
    }

    return target.toISOString();
  }

  function normalizeScheduleConfigForTaskCreation(rawSchedule) {
    if (!rawSchedule || typeof rawSchedule !== 'object') return null;
    var schedule = Object.assign({}, rawSchedule);
    var type = normalizeToken(schedule.type);
    if (!type) return null;
    schedule.type = type;

    if (schedule.once_at && !schedule.run_at) {
      schedule.run_at = schedule.once_at;
    }

    if (type === 'daily' || type === 'weekly') {
      if (!schedule.time && schedule.time_of_day) {
        schedule.time = schedule.time_of_day;
      }
      if (!schedule.time || typeof schedule.time !== 'string') {
        schedule.time = '09:00';
      }
      if (type === 'weekly') {
        schedule.day_of_week = normalizeScheduleDayOfWeek(schedule.day_of_week);
      } else {
        delete schedule.day_of_week;
      }
      delete schedule.time_of_day;
      delete schedule.interval_minutes;
      delete schedule.run_at;
      delete schedule.execute_at;
      delete schedule.once_at;
      return schedule;
    }

    if (type === 'interval') {
      var minutes = Number(schedule.interval_minutes);
      if (!Number.isFinite(minutes) || minutes <= 0) return null;
      schedule.interval_minutes = Math.round(minutes);
      delete schedule.time;
      delete schedule.time_of_day;
      delete schedule.day_of_week;
      delete schedule.run_at;
      delete schedule.execute_at;
      delete schedule.once_at;
      return schedule;
    }

    if (type === 'once') {
      var runAt = String(schedule.run_at || schedule.execute_at || '').trim();
      if (!runAt) return null;
      schedule.run_at = runAt;
      delete schedule.execute_at;
      delete schedule.once_at;
      delete schedule.time;
      delete schedule.time_of_day;
      delete schedule.day_of_week;
      delete schedule.interval_minutes;
      return schedule;
    }

    return null;
  }

  function inferScheduleFromPromptFallback(prompt) {
    var text = normalizeToken(prompt);
    if (!text) return null;

    var intervalMatch = text.match(/\bevery\s+(\d+)\s*(min|mins|minute|minutes|hour|hours|day|days)\b/);
    if (intervalMatch) {
      var amount = Number(intervalMatch[1]);
      if (Number.isFinite(amount) && amount > 0) {
        var unit = normalizeToken(intervalMatch[2]);
        var intervalMinutes = amount;
        if (unit === 'hour' || unit === 'hours') {
          intervalMinutes = amount * 60;
        } else if (unit === 'day' || unit === 'days') {
          intervalMinutes = amount * 1440;
        }
        var intervalSchedule = { type: 'interval', interval_minutes: intervalMinutes };
        return { schedule: intervalSchedule, schedule_name: buildScheduleNameFromConfig(intervalSchedule) };
      }
    }

    var clock = parseClockTimeFromText(prompt);
    var weekdayMatch = text.match(/\bevery\s+(sunday|monday|tuesday|wednesday|thursday|friday|saturday)\b/);
    if (weekdayMatch) {
      var weeklySchedule = {
        type: 'weekly',
        day_of_week: normalizeScheduleDayOfWeek(weekdayMatch[1]),
        time: clock ? clock.hhmm : '09:00'
      };
      return { schedule: weeklySchedule, schedule_name: buildScheduleNameFromConfig(weeklySchedule) };
    }

    if (/\b(?:every day|daily|each day)\b/.test(text)) {
      var dailySchedule = { type: 'daily', time: clock ? clock.hhmm : '09:00' };
      return { schedule: dailySchedule, schedule_name: buildScheduleNameFromConfig(dailySchedule) };
    }

    if (clock) {
      var onceSchedule = {
        type: 'once',
        run_at: buildNextRunAtISO(clock.hour24, clock.minute, {
          forceTomorrow: /\btomorrow\b/.test(text),
          forceToday: /\btoday\b/.test(text)
        })
      };
      return { schedule: onceSchedule, schedule_name: buildScheduleNameFromConfig(onceSchedule) };
    }

    return null;
  }

  function buildScheduleNameFromConfig(schedule) {
    if (!schedule || typeof schedule !== 'object') return 'Scheduled Task';
    var type = normalizeToken(schedule.type);
    var days = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday'];

    if (type === 'daily') {
      return 'Daily at ' + formatScheduleTimeLabel(schedule.time || schedule.time_of_day || '09:00');
    }

    if (type === 'weekly') {
      var dayIndex = normalizeScheduleDayOfWeek(schedule.day_of_week);
      var dayLabel = days[dayIndex] || 'Weekly';
      return 'Every ' + dayLabel + ' at ' + formatScheduleTimeLabel(schedule.time || schedule.time_of_day || '09:00');
    }

    if (type === 'interval') {
      var minutes = Number(schedule.interval_minutes || 0);
      if (!Number.isFinite(minutes) || minutes <= 0) return 'Recurring schedule';
      if (minutes % 1440 === 0) {
        var daysCount = minutes / 1440;
        return 'Every ' + daysCount + ' day' + (daysCount === 1 ? '' : 's');
      }
      if (minutes % 60 === 0) {
        var hoursCount = minutes / 60;
        return 'Every ' + hoursCount + ' hour' + (hoursCount === 1 ? '' : 's');
      }
      return 'Every ' + minutes + ' minute' + (minutes === 1 ? '' : 's');
    }

    if (type === 'once') {
      var runAt = String(schedule.run_at || '').trim();
      var runDate = runAt ? new Date(runAt) : null;
      if (runDate && !Number.isNaN(runDate.getTime())) {
        return 'One-time on ' + runDate.toLocaleString();
      }
      return 'One-time schedule';
    }

    return 'Scheduled Task';
  }

  function formatScheduleSummary(schedule, scheduleName) {
    var explicitName = String(scheduleName || '').trim();
    if (explicitName) return explicitName;
    return buildScheduleNameFromConfig(schedule);
  }

  function shouldAskScheduleFrequencyChoice(prompt, schedule) {
    if (!schedule || normalizeToken(schedule.type) !== 'once') return false;
    var text = normalizeToken(prompt);
    if (!text) return false;
    if (hasRecurringScheduleLanguage(text)) return false;
    if (/\b(?:once|one-time|one time|today|tomorrow|tonight)\b/.test(text)) return false;
    if (/\b(?:at|@)\s*\d{1,2}(?::\d{2})?\s*(?:am|pm)?\b/i.test(String(prompt || ''))) return true;
    if (/\b\d{1,2}:\d{2}\s*(?:am|pm)?\b/i.test(String(prompt || ''))) return true;
    return false;
  }

  function buildDailyScheduleFromPrompt(prompt, schedule) {
    var clock = parseClockTimeFromText(prompt);
    var fallbackTime = '09:00';
    if (clock && clock.hhmm) {
      fallbackTime = clock.hhmm;
    } else if (schedule && schedule.run_at) {
      var runDate = new Date(schedule.run_at);
      if (!Number.isNaN(runDate.getTime())) {
        fallbackTime = String(runDate.getHours()).padStart(2, '0') + ':' + String(runDate.getMinutes()).padStart(2, '0');
      }
    }
    return { type: 'daily', time: fallbackTime };
  }

  async function parseWorkspaceTaskScheduleDraft(prompt, workspaceId) {
    var description = String(prompt || '').trim();
    var draft = {
      hasSchedulingIntent: promptHasSchedulingIntent(description),
      schedule_enabled: false,
      schedule: null,
      schedule_name: '',
      parsedAgentName: '',
      needsFrequencyChoice: false,
      dailyAlternative: null
    };
    if (!draft.hasSchedulingIntent) return draft;

    var parsed = null;
    if (typeof API !== 'undefined' && typeof API.post === 'function') {
      try {
        parsed = await API.post('/api/orchestration/tasks/auto-parse', {
          description: description,
          workspace_id: workspaceId
        });
      } catch (error) {
        dashLog.debug('Auto-parse schedule draft unavailable', { error: error && error.message || error });
      }
    } else {
      try {
        var response = await fetch('/api/orchestration/tasks/auto-parse', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            description: description,
            workspace_id: workspaceId
          })
        });
        if (response.ok) {
          parsed = await response.json();
        }
      } catch (error) {
        dashLog.debug('Fallback auto-parse schedule draft unavailable', { error: error && error.message || error });
      }
    }

    if (parsed && typeof parsed === 'object') {
      draft.parsedAgentName = String(parsed.agent_name || '').trim();
      if (parsed.schedule_enabled && parsed.schedule) {
        var normalized = normalizeScheduleConfigForTaskCreation(parsed.schedule);
        if (normalized) {
          draft.schedule_enabled = true;
          draft.schedule = normalized;
          draft.schedule_name = String(parsed.schedule_name || '').trim();
        }
      }
    }

    if (!draft.schedule_enabled || !draft.schedule) {
      var fallback = inferScheduleFromPromptFallback(description);
      if (fallback && fallback.schedule) {
        var normalizedFallback = normalizeScheduleConfigForTaskCreation(fallback.schedule);
        if (normalizedFallback) {
          draft.schedule_enabled = true;
          draft.schedule = normalizedFallback;
          draft.schedule_name = String(fallback.schedule_name || '').trim();
        }
      }
    }

    if (draft.schedule_enabled && draft.schedule) {
      draft.needsFrequencyChoice = shouldAskScheduleFrequencyChoice(description, draft.schedule);
      if (draft.needsFrequencyChoice) {
        draft.dailyAlternative = buildDailyScheduleFromPrompt(description, draft.schedule);
      }
      if (!draft.schedule_name) {
        draft.schedule_name = buildScheduleNameFromConfig(draft.schedule);
      }
    }

    return draft;
  }

  function normalizeAgentNameList(names) {
    if (!Array.isArray(names)) return [];
    var seen = Object.create(null);
    var out = [];
    for (var i = 0; i < names.length; i++) {
      var name = String(names[i] || '').trim();
      if (!name) continue;
      var key = normalizeToken(name);
      if (!key || seen[key]) continue;
      seen[key] = true;
      out.push(name);
    }
    return out;
  }

  function extractAgentName(agent) {
    if (typeof agent === 'string') return String(agent).trim();
    if (!agent || typeof agent !== 'object') return '';
    return String(agent.name || '').trim();
  }

  async function fetchWorkspaceTaskAgentInventory(workspaceId) {
    var workspaceAgents = [];
    if (window.workspaceDetail && typeof window.workspaceDetail.getWorkspaceAgentNames === 'function') {
      workspaceAgents = normalizeAgentNameList(window.workspaceDetail.getWorkspaceAgentNames());
    }

    if (workspaceAgents.length === 0) {
      try {
        var workspaceData = null;
        if (typeof API !== 'undefined' && typeof API.get === 'function') {
          workspaceData = await API.get('/api/orchestration/workspace?id=' + encodeURIComponent(workspaceId));
        } else {
          var wsResponse = await fetch('/api/orchestration/workspace?id=' + encodeURIComponent(workspaceId));
          if (wsResponse.ok) workspaceData = await wsResponse.json();
        }
        if (workspaceData && typeof workspaceData === 'object') {
          var merged = [];
          if (Array.isArray(workspaceData.agent_instances)) {
            for (var i = 0; i < workspaceData.agent_instances.length; i++) {
              merged.push(workspaceData.agent_instances[i] && workspaceData.agent_instances[i].name);
            }
          }
          if (Array.isArray(workspaceData.agents)) {
            for (var j = 0; j < workspaceData.agents.length; j++) {
              merged.push(workspaceData.agents[j]);
            }
          }
          workspaceAgents = normalizeAgentNameList(merged);
        }
      } catch (error) {
        dashLog.debug('Failed to load workspace agents for scheduled task', {
          workspaceId: workspaceId,
          error: error && error.message || error
        });
      }
    }

    var allAgents = [];
    try {
      allAgents = await fetchAgentsForMatching();
    } catch (error) {
      dashLog.debug('Failed to load global agents for scheduled task', { error: error && error.message || error });
      allAgents = [];
    }

    var allAgentNames = normalizeAgentNameList(allAgents.map(extractAgentName));
    return {
      workspaceAgents: workspaceAgents,
      allAgents: allAgents,
      allAgentNames: allAgentNames
    };
  }

  function openWorkspaceTaskModalForConfiguration(workspaceId, description) {
    if (window.taskModalController && typeof window.taskModalController.openForCreate === 'function') {
      window.taskModalController.openForCreate(workspaceId, String(description || '').trim(), function () {
        if (window.workspaceDetail && typeof window.workspaceDetail.loadTasks === 'function') {
          window.workspaceDetail.loadTasks();
        }
        if (window.workspaceDetail && typeof window.workspaceDetail.loadSchedules === 'function') {
          window.workspaceDetail.loadSchedules();
        }
      });
      return true;
    }
    if (window.workspaceDetail && typeof window.workspaceDetail.showAddTaskModal === 'function') {
      window.workspaceDetail.showAddTaskModal();
      return true;
    }
    return false;
  }

  function openWorkspaceAgentAddFlow(workspaceId) {
    if (window.workspaceDetail && typeof window.workspaceDetail.openAddAgentModal === 'function') {
      window.workspaceDetail.openAddAgentModal();
      return true;
    }
    if (workspaceId) {
      window.location.href = '/workspaces/' + encodeURIComponent(workspaceId);
      return true;
    }
    return false;
  }

  function openAgentCreationFlow() {
    if (window.workspaceDetail && typeof window.workspaceDetail.openCreateAgentFlow === 'function') {
      window.workspaceDetail.openCreateAgentFlow();
      return;
    }
    if (typeof window.showAddAgentModal === 'function') {
      window.showAddAgentModal();
      return;
    }
    window.location.href = '/agents';
  }

  async function createWorkspaceTaskRecord(workspaceId, payload) {
    var body = Object.assign({
      workspace_id: workspaceId,
      details: '',
      status: 'pending'
    }, payload || {});

    if (typeof API !== 'undefined' && typeof API.post === 'function') {
      return await API.post('/api/orchestration/tasks', body);
    }

    var response = await fetch('/api/orchestration/tasks', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body)
    });
    if (!response.ok) {
      var text = '';
      try {
        text = await response.text();
      } catch (_error) {
        text = '';
      }
      throw new Error(text || 'Failed to create task');
    }
    return await response.json();
  }

  async function updateWorkspaceTaskRecord(taskId, payload) {
    var normalizedTaskId = String(taskId || '').trim();
    if (!normalizedTaskId) {
      throw new Error('Task ID is required');
    }

    if (typeof API !== 'undefined' && typeof API.put === 'function') {
      return await API.put('/api/orchestration/tasks/' + encodeURIComponent(normalizedTaskId), payload || {});
    }

    var response = await fetch('/api/orchestration/tasks/' + encodeURIComponent(normalizedTaskId), {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload || {})
    });
    if (!response.ok) {
      var text = '';
      try {
        text = await response.text();
      } catch (_error) {
        text = '';
      }
      throw new Error(text || 'Failed to update task');
    }
    return await response.json();
  }

  async function executeWorkspaceTaskRecord(taskId) {
    var normalizedTaskId = String(taskId || '').trim();
    if (!normalizedTaskId) {
      throw new Error('Task ID is required');
    }

    if (typeof API !== 'undefined' && typeof API.post === 'function') {
      return await API.post('/api/orchestration/tasks/execute', {
        task_id: normalizedTaskId
      });
    }

    var response = await fetch('/api/orchestration/tasks/execute', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        task_id: normalizedTaskId
      })
    });
    if (!response.ok) {
      var text = '';
      try {
        text = await response.text();
      } catch (_error) {
        text = '';
      }
      throw new Error(text || 'Failed to execute task');
    }
    return await response.json();
  }

  async function refreshWorkspaceDetailTaskPanels() {
    if (!window.workspaceDetail) return;
    var refreshCalls = [];
    if (typeof window.workspaceDetail.loadWorkspace === 'function') {
      refreshCalls.push(window.workspaceDetail.loadWorkspace());
    }
    if (typeof window.workspaceDetail.loadTasks === 'function') {
      refreshCalls.push(window.workspaceDetail.loadTasks());
    }
    if (typeof window.workspaceDetail.loadSchedules === 'function') {
      refreshCalls.push(window.workspaceDetail.loadSchedules());
    }
    if (refreshCalls.length > 0) {
      await Promise.allSettled(refreshCalls);
    }
    if (typeof window.workspaceDetail.renderAgentGroups === 'function') {
      window.workspaceDetail.renderAgentGroups();
    }
  }

  function syncCreatedTaskIntoWorkspaceDetail(createdTask) {
    if (!window.workspaceDetail || !createdTask || !createdTask.id) return;

    var detail = window.workspaceDetail;
    if (!Array.isArray(detail.tasks)) {
      detail.tasks = [];
    }

    var normalizedTask = Object.assign({}, createdTask);
    if (!normalizedTask.to && normalizedTask.assigned_node_id) {
      normalizedTask.to = String(normalizedTask.assigned_node_id || '')
        .replace(/-node-\d+$/, '')
        .trim();
    }

    var existingIndex = detail.tasks.findIndex(function (task) {
      return task && task.id === normalizedTask.id;
    });
    if (existingIndex >= 0) {
      detail.tasks[existingIndex] = Object.assign({}, detail.tasks[existingIndex], normalizedTask);
    } else {
      detail.tasks = [normalizedTask].concat(detail.tasks);
    }

    detail.tasksLoading = false;
    detail.tasksLoadFailed = false;

    if (detail.elements && detail.elements.taskCount) {
      detail.elements.taskCount.textContent = String(detail.tasks.length);
    }

    if (typeof detail.renderTasks === 'function') {
      detail.renderTasks();
    } else if (typeof detail.renderAgentGroups === 'function') {
      detail.renderAgentGroups();
    }
  }

  function syncUpdatedTaskIntoWorkspaceDetail(updatedTask) {
    if (!window.workspaceDetail || !updatedTask || !updatedTask.id) return;

    var detail = window.workspaceDetail;
    if (!Array.isArray(detail.tasks)) {
      detail.tasks = [];
    }

    var idx = detail.tasks.findIndex(function (task) {
      return task && task.id === updatedTask.id;
    });

    if (idx >= 0) {
      var existing = detail.tasks[idx] || {};
      var nextContext = Object.assign({}, existing.context || {}, updatedTask.context || {});
      detail.tasks[idx] = Object.assign({}, existing, updatedTask, { context: nextContext });
    } else {
      detail.tasks = [updatedTask].concat(detail.tasks);
    }

    detail.tasksLoading = false;
    detail.tasksLoadFailed = false;
    if (detail.elements && detail.elements.taskCount) {
      detail.elements.taskCount.textContent = String(detail.tasks.length);
    }
    if (typeof detail.renderTasks === 'function') {
      detail.renderTasks();
    } else if (typeof detail.renderAgentGroups === 'function') {
      detail.renderAgentGroups();
    }
  }

  function dismissHomeAssistantThinkingModalForTaskLaunch() {
    var els = getHomeAssistantElements();
    var modalElement = els.thinkingModal;
    if (!modalElement || !isHomeAssistantThinkingModalVisible()) {
      return Promise.resolve();
    }

    return new Promise(function (resolve) {
      var settled = false;
      var fallbackTimer = null;

      function finalize() {
        if (settled) return;
        settled = true;
        if (fallbackTimer) {
          window.clearTimeout(fallbackTimer);
          fallbackTimer = null;
        }
        modalElement.removeEventListener('hidden.bs.modal', onHidden, true);
        resolve();
      }

      function onHidden() {
        finalize();
      }

      modalElement.addEventListener('hidden.bs.modal', onHidden, true);
      fallbackTimer = window.setTimeout(finalize, 500);
      closeHomeAssistantThinkingModal({ force: true });
    });
  }

  function clearHomeAssistantTaskLaunchState() {
    setHomeAssistantBusy(false);
    renderHomeAssistantActions([]);
    clearHomeAssistantInlineReply();
    clearHomeAssistantPlanning();
    setHomeAssistantRoutingSummary('', '');
  }

  async function preparePlanningReviewMainTaskForExecution(planningState) {
    var mainTask = await ensurePlanningReviewMainTask(planningState);
    if (!mainTask || !mainTask.id) {
      throw new Error('Failed to create the main workspace task');
    }

    var updatedTask = await updateWorkspaceTaskRecord(mainTask.id, {
      details: buildPlanningTaskExecutionDetails(planningState),
      context: buildPlanningReviewTaskContext(planningState)
    });
    syncUpdatedTaskIntoWorkspaceDetail(updatedTask);
    planningState.mainTask = updatedTask;
    return updatedTask;
  }

  async function launchWorkspaceTaskExecutionFromHomeAssistant(task, routeContext) {
    if (!task || !task.id) {
      throw new Error('Workspace task is missing');
    }

    var detail = window.workspaceDetail;
    var targetWorkspaceId = String(
      routeContext && routeContext.workspace_id ||
      task.workspace_id ||
      task.folder_id ||
      ''
    ).trim();
    var detailWorkspaceId = String(detail && (detail.workspaceId || detail.workspace && detail.workspace.id) || '').trim();
    var canUseWorkspaceDetail = Boolean(
      detail &&
      typeof detail.executeTask === 'function' &&
      (!targetWorkspaceId || !detailWorkspaceId || targetWorkspaceId === detailWorkspaceId)
    );

    await dismissHomeAssistantThinkingModalForTaskLaunch();

    try {
      if (canUseWorkspaceDetail) {
        await detail.executeTask(task.id, { skipConfirm: true });
        return;
      }
      await executeWorkspaceTaskRecord(task.id);
    } catch (error) {
      openHomeAssistantThinkingModal();
      throw error;
    }
  }

  function showWorkspaceSpecialistCreationPrompt(prompt, routeContext, intent, config) {
    if (!config) return;

    appendHomeAssistantMessage(
      'assistant',
      '"' + config.label + '" should own this travel-planning task. Create it first, then hand the task off there?'
    );
    setHomeAssistantRoutingSummary(
      config.label,
      'Create the travel specialist, then start the workspace task there.'
    );
    renderHomeAssistantActions([
      {
        label: 'Create ' + config.label + ' + Handoff',
        variant: 'primary',
        onClick: function () {
          routeWorkspacePromptToPlanningSpecialist(prompt, routeContext, intent, {
            config: config,
            allowCreate: true
          });
        }
      },
      {
        label: 'Keep With ' + getWorkspaceHomeAssistantDisplayName(),
        variant: 'secondary',
        onClick: function () { openWorkspaceAssistantForPrompt(prompt, routeContext, intent); }
      },
      {
        label: 'Ask Another Task',
        variant: 'secondary',
        onClick: function () { focusHomeAssistantInput(); }
      }
    ]);
  }

  async function routeWorkspacePromptToPlanningSpecialist(prompt, routeContext, intent, options) {
    var normalizedContext = normalizeHomeRouteContext(routeContext);
    var workspaceId = hasWorkspaceRouteContext(normalizedContext) ? String(normalizedContext.workspace_id || '').trim() : '';
    if (!workspaceId) return false;

    var config = options && options.config ? options.config : detectWorkspacePlanningSpecialist(prompt, intent);
    if (!config) return false;

    var allowCreate = options && options.allowCreate === true;
    var managerLabel = getWorkspaceHomeAssistantDisplayName();

    setHomeAssistantBusy(true, allowCreate ? 'Creating Specialist...' : 'Routing Task...');
    renderHomeAssistantActions([]);
    appendHomeAssistantMessage(
      'assistant',
      allowCreate
        ? 'Creating "' + config.label + '" and handing this task off there.'
        : 'This looks like specialist-owned travel work. Routing it to "' + config.label + '" before execution starts.'
    );
    setHomeAssistantRoutingSummary(config.label, 'Preparing a specialist-owned workspace task.');

    try {
      var inventory = await fetchWorkspaceTaskAgentInventory(workspaceId);
      var globalAgent = findExactAgentByName(inventory && inventory.allAgents, config.agentName);
      if (!globalAgent && !allowCreate) {
        showWorkspaceSpecialistCreationPrompt(prompt, normalizedContext, intent, config);
        return true;
      }

      var agentName = globalAgent
        ? (typeof globalAgent === 'string' ? globalAgent : globalAgent.name)
        : await createPlanningSpecialistAgent(config, {
          summaryText: String(prompt || '').trim(),
          agentLabel: managerLabel
        });

      var workspaceAgent = findExactAgentByName(inventory && inventory.workspaceAgents, agentName);
      if (!workspaceAgent) {
        var added = await addAgentToWorkspaceIfNeeded(agentName, normalizedContext);
        if (!added) {
          throw new Error('Failed to attach specialist to workspace');
        }
      }

      var createdResponse = await createWorkspaceTaskRecord(workspaceId, {
        from: managerLabel,
        to: String(agentName || '').trim(),
        description: buildPlanningTaskDescriptionFromPrompt(prompt, config.taskTitle || config.label),
        details: buildWorkspaceSpecialistTaskDetails(prompt, managerLabel, config, agentName)
      });
      var createdTask = createdResponse && createdResponse.task ? createdResponse.task : createdResponse;
      if (!createdTask || !createdTask.id) {
        throw new Error('Failed to create the specialist task');
      }

      syncCreatedTaskIntoWorkspaceDetail(createdTask);
      var updatedTask = await updateWorkspaceTaskRecord(createdTask.id, {
        context: buildWorkspaceSpecialistTaskContext(Object.assign({}, config, {
          agentName: agentName
        }), managerLabel)
      });
      syncUpdatedTaskIntoWorkspaceDetail(updatedTask);
      await refreshWorkspaceDetailTaskPanels();

      appendHomeAssistantMessage(
        'assistant',
        '"' + agentName + '" is handling this workspace task now.'
      );
      setHomeAssistantRoutingSummary(config.label, 'Task handed off to "' + agentName + '".');
      await launchWorkspaceTaskExecutionFromHomeAssistant(updatedTask, normalizedContext);
      clearHomeAssistantTaskLaunchState();
      return true;
    } catch (error) {
      dashLog.debug('Failed to hand off workspace travel task to specialist', {
        prompt: prompt,
        specialist: config && config.key,
        error: error && error.message || error
      });
      appendHomeAssistantMessage('assistant', 'I could not hand off this task to "' + config.label + '" right now.');
      setHomeAssistantRoutingSummary(config.label, 'Could not create the specialist handoff right now.');
      renderHomeAssistantActions([
        {
          label: 'Retry Specialist Handoff',
          variant: 'primary',
          onClick: function () {
            routeWorkspacePromptToPlanningSpecialist(prompt, normalizedContext, intent, {
              config: config,
              allowCreate: allowCreate
            });
          }
        },
        {
          label: 'Keep With ' + managerLabel,
          variant: 'secondary',
          onClick: function () { openWorkspaceAssistantForPrompt(prompt, normalizedContext, intent); }
        },
        {
          label: 'Ask Another Task',
          variant: 'secondary',
          onClick: function () { focusHomeAssistantInput(); }
        }
      ]);
      return true;
    } finally {
      setHomeAssistantBusy(false);
    }
  }

  function handoffPlanningSubtaskToWorkspaceTaskModal(updatedTask, routeContext) {
    if (!window.workspaceDetail || !updatedTask || !updatedTask.id) return false;

    var detail = window.workspaceDetail;
    if (typeof detail.openTaskAssistModal !== 'function') return false;

    var targetWorkspaceId = String(
      routeContext && routeContext.workspace_id ||
      updatedTask.workspace_id ||
      updatedTask.folder_id ||
      ''
    ).trim();
    var detailWorkspaceId = String(detail.workspaceId || detail.workspace && detail.workspace.id || '').trim();
    if (targetWorkspaceId && detailWorkspaceId && targetWorkspaceId !== detailWorkspaceId) {
      return false;
    }

    var humanLoop = updatedTask.context &&
      typeof updatedTask.context === 'object' &&
      updatedTask.context.human_loop &&
      typeof updatedTask.context.human_loop === 'object'
      ? Object.assign({}, updatedTask.context.human_loop)
      : null;
    var eventData = humanLoop
      ? Object.assign({ task_id: String(updatedTask.id), human_loop: humanLoop }, humanLoop)
      : { task_id: String(updatedTask.id) };

    function openTaskAssist() {
      try {
        detail.openTaskAssistModal(updatedTask.id, eventData);
      } catch (error) {
        dashLog.debug('Failed to hand off planning subtask to task assist modal', {
          taskId: updatedTask.id,
          error: error && error.message || error
        });
      }
    }

    var els = getHomeAssistantElements();
    var modalElement = els.thinkingModal;
    if (!modalElement || !isHomeAssistantThinkingModalVisible()) {
      openTaskAssist();
      return true;
    }

    var settled = false;
    var fallbackTimer = null;

    function finalizeOpen() {
      if (settled) return;
      settled = true;
      if (fallbackTimer) {
        window.clearTimeout(fallbackTimer);
        fallbackTimer = null;
      }
      modalElement.removeEventListener('hidden.bs.modal', onHidden, true);
      openTaskAssist();
    }

    function onHidden() {
      finalizeOpen();
    }

    modalElement.addEventListener('hidden.bs.modal', onHidden, true);
    fallbackTimer = window.setTimeout(finalizeOpen, 500);
    closeHomeAssistantThinkingModal({ force: true });
    return true;
  }

  function buildPlanningTaskQuestion(replyState) {
    if (!replyState) return 'How should I continue this planning task?';

    if (replyState.workflowStep &&
        String(replyState.workflowStep.step_type || '').trim() === 'ask_choice' &&
        Array.isArray(replyState.workflowStep.choices) &&
        replyState.workflowStep.choices.length > 0) {
      var choiceLabels = replyState.workflowStep.choices.map(function (choice) {
        var number = String(choice && choice.number || '').trim();
        var label = String(choice && choice.label || '').trim();
        return (number ? number + '. ' : '') + label;
      }).filter(Boolean);
      var prefix = String(replyState.workflowStep.summary || replyState.workflowStep.title || '').trim();
      var prompt = prefix || 'Choose the next step for this planning task.';
      if (choiceLabels.length > 0) {
        prompt += '\n\nOptions:\n- ' + choiceLabels.join('\n- ');
      }
      return prompt;
    }

    if (Array.isArray(replyState.questionPrompts) && replyState.questionPrompts.length > 0) {
      return replyState.questionPrompts.join('\n');
    }

    return 'How should I continue this planning task?';
  }

  async function persistPlanningSubtaskToTask(replyState) {
    if (!replyState || !replyState.linkedTask || !replyState.linkedTask.id) return null;

    var humanLoop = {
      state: 'blocked',
      block_id: 'planning:' + String(replyState.linkedTask.id || '').trim(),
      reason_code: 'planning_input_required',
      reason: 'This planning task needs your input before it can continue.',
      question: buildPlanningTaskQuestion(replyState),
      agent_response: String(replyState.latestReplyText || '').trim(),
      workflow_step: replyState.workflowStep || null,
      suggested_actions: ['continue_with_instruction', 'mark_failed'],
      updated_at: new Date().toISOString()
    };

    var payload = {
      context: {
        human_loop: humanLoop,
        planning_latest_reply: String(replyState.latestReplyText || '').trim(),
        planning_workflow_step: replyState.workflowStep || null,
        planning_session_id: String(replyState.routeContext && replyState.routeContext.session_id || '').trim()
      }
    };

    var response = await fetch('/api/orchestration/tasks/' + encodeURIComponent(replyState.linkedTask.id), {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload)
    });
    if (!response.ok) {
      var text = '';
      try {
        text = await response.text();
      } catch (_error) {
        text = '';
      }
      throw new Error(text || 'Failed to update planning task');
    }

    var updatedTask = await response.json();
    syncUpdatedTaskIntoWorkspaceDetail(updatedTask);
    replyState.linkedTask = updatedTask;
    return updatedTask;
  }

  async function createScheduledWorkspaceTask(workspaceId, description, agentName, scheduleConfig, scheduleName) {
    var assignedAgent = String(agentName || '').trim();
    if (!assignedAgent) {
      throw new Error('Scheduled tasks require an assigned agent.');
    }
    var normalizedSchedule = normalizeScheduleConfigForTaskCreation(scheduleConfig);
    if (!normalizedSchedule) {
      throw new Error('Could not parse schedule details from the prompt.');
    }
    var label = String(scheduleName || '').trim() || buildScheduleNameFromConfig(normalizedSchedule);
    return await createWorkspaceTaskRecord(workspaceId, {
      description: String(description || '').trim(),
      to: assignedAgent,
      schedule: normalizedSchedule,
      schedule_enabled: true,
      schedule_name: label
    });
  }

  async function handleWorkspaceScheduledTaskCreation(content, workspaceId) {
    var prompt = String(content || '').trim();
    if (!prompt) return false;

    var scheduleDraft = await parseWorkspaceTaskScheduleDraft(prompt, workspaceId);
    if (!scheduleDraft.hasSchedulingIntent) {
      return false;
    }

    var inventory = await fetchWorkspaceTaskAgentInventory(workspaceId);
    var workspaceAgents = inventory.workspaceAgents || [];
    var globalAgents = inventory.allAgents || [];
    var candidateAgents = workspaceAgents.length > 0 ? workspaceAgents.slice() : (inventory.allAgentNames || []);

    if (candidateAgents.length === 0) {
      appendHomeAssistantMessage(
        'assistant',
        'This task looks scheduled, and scheduled tasks need an assigned agent. There are no agents available yet.'
      );
      setHomeAssistantRoutingSummary('Scheduled Task Needs Agent', 'Create an agent first, then assign the scheduled task.');
      renderHomeAssistantActions([
        {
          label: 'Create Agent',
          variant: 'primary',
          onClick: function () { openAgentCreationFlow(); }
        },
        {
          label: 'Open Task Modal',
          variant: 'secondary',
          onClick: function () { openWorkspaceTaskModalForConfiguration(workspaceId, prompt); }
        },
        {
          label: 'Ask Another Task',
          variant: 'secondary',
          onClick: function () { focusHomeAssistantInput(); }
        }
      ]);
      return true;
    }

    if (!scheduleDraft.schedule_enabled || !scheduleDraft.schedule) {
      appendHomeAssistantMessage(
        'assistant',
        'I detected scheduling intent, but I need one more step to confirm the schedule details.'
      );
      setHomeAssistantRoutingSummary('Schedule Confirmation', 'Review schedule details in the task modal.');
      renderHomeAssistantActions([
        {
          label: 'Configure in Task Modal',
          variant: 'primary',
          onClick: function () { openWorkspaceTaskModalForConfiguration(workspaceId, prompt); }
        },
        {
          label: 'Ask Another Task',
          variant: 'secondary',
          onClick: function () { focusHomeAssistantInput(); }
        }
      ]);
      return true;
    }

    var preferredAgent = '';
    var preferredKey = normalizeToken(scheduleDraft.parsedAgentName);
    if (preferredKey) {
      for (var i = 0; i < candidateAgents.length; i++) {
        if (normalizeToken(candidateAgents[i]) === preferredKey) {
          preferredAgent = candidateAgents[i];
          break;
        }
      }
    }

    var candidateLookup = Object.create(null);
    for (var j = 0; j < candidateAgents.length; j++) {
      candidateLookup[normalizeToken(candidateAgents[j])] = true;
    }

    var candidateAgentObjects = [];
    for (var g = 0; g < globalAgents.length; g++) {
      var globalAgentName = extractAgentName(globalAgents[g]);
      if (!globalAgentName) continue;
      if (!candidateLookup[normalizeToken(globalAgentName)]) continue;
      candidateAgentObjects.push(globalAgents[g]);
    }

    var selectedAgent = preferredAgent;
    if (!selectedAgent && candidateAgentObjects.length > 0) {
      var intent = detectHomeIntent(prompt);
      var match = findSuitableAgent(candidateAgentObjects, intent, prompt);
      if (match && match.agent && match.agent.name) {
        selectedAgent = String(match.agent.name).trim();
      }
    }
    if (!selectedAgent) selectedAgent = candidateAgents[0];

    var scheduleSummary = formatScheduleSummary(scheduleDraft.schedule, scheduleDraft.schedule_name);
    var workspaceHasSelectedAgent = false;
    for (var a = 0; a < workspaceAgents.length; a++) {
      if (normalizeToken(workspaceAgents[a]) === normalizeToken(selectedAgent)) {
        workspaceHasSelectedAgent = true;
        break;
      }
    }

    var openSchedulesAction = {
      label: 'Open Schedules',
      variant: 'secondary',
      onClick: function () {
        if (window.workspaceDetail && typeof window.workspaceDetail.showSchedulesModal === 'function') {
          window.workspaceDetail.showSchedulesModal();
          return;
        }
        if (workspaceId) {
          window.location.href = '/workspaces/' + encodeURIComponent(workspaceId);
        }
      }
    };

    async function finalizeScheduledCreation(agentName, scheduleConfig, scheduleName) {
      var chosenAgent = String(agentName || '').trim();
      if (!chosenAgent) return;

      setHomeAssistantBusy(true, 'Creating Scheduled Task...');
      renderHomeAssistantActions([]);
      appendHomeAssistantMessage('assistant', 'Creating a scheduled task and assigning it to "' + chosenAgent + '"...');
      setHomeAssistantRoutingSummary('Scheduled Task', 'Creating and assigning scheduled task...');

      try {
        await createScheduledWorkspaceTask(workspaceId, prompt, chosenAgent, scheduleConfig, scheduleName);
        await refreshWorkspaceDetailTaskPanels();

        var summary = formatScheduleSummary(scheduleConfig, scheduleName);
        appendHomeAssistantMessage('assistant', 'Scheduled task created for "' + chosenAgent + '" (' + summary + ').');
        if (!workspaceHasSelectedAgent) {
          appendHomeAssistantMessage('assistant', '"' + chosenAgent + '" was added to this workspace for this scheduled task.');
        }
        setHomeAssistantRoutingSummary('Scheduled Task Created', 'Assigned to "' + chosenAgent + '" with ' + summary + '.');
        renderHomeAssistantActions([
          openSchedulesAction,
          {
            label: 'Ask Another Task',
            variant: 'secondary',
            onClick: function () { focusHomeAssistantInput(); }
          }
        ]);
      } catch (error) {
        dashLog.debug('Failed to create scheduled workspace task', {
          workspaceId: workspaceId,
          error: error && error.message || error
        });
        appendHomeAssistantMessage('assistant', 'I could not create the scheduled task right now. Please try again.');
        setHomeAssistantRoutingSummary('Scheduled Task Failed', 'Could not create scheduled task.');
        renderHomeAssistantActions([
          {
            label: 'Retry',
            variant: 'primary',
            onClick: function () { finalizeScheduledCreation(chosenAgent, scheduleConfig, scheduleName); }
          },
          {
            label: 'Open Task Modal',
            variant: 'secondary',
            onClick: function () { openWorkspaceTaskModalForConfiguration(workspaceId, prompt); }
          }
        ]);
      } finally {
        setHomeAssistantBusy(false);
      }
    }

    if (scheduleDraft.needsFrequencyChoice) {
      var dailyAlternative = normalizeScheduleConfigForTaskCreation(scheduleDraft.dailyAlternative || buildDailyScheduleFromPrompt(prompt, scheduleDraft.schedule));
      var onceName = buildScheduleNameFromConfig(scheduleDraft.schedule);
      var dailyName = dailyAlternative ? buildScheduleNameFromConfig(dailyAlternative) : 'Daily schedule';
      appendHomeAssistantMessage(
        'assistant',
        'I detected a scheduled task. Should this run once or every day? I can assign it to "' + selectedAgent + '".'
      );
      setHomeAssistantRoutingSummary('Schedule Choice', 'Choose one-time or daily schedule and confirm assignment.');
      renderHomeAssistantActions([
        {
          label: 'Run Once',
          variant: 'primary',
          onClick: function () { finalizeScheduledCreation(selectedAgent, scheduleDraft.schedule, onceName); }
        },
        {
          label: 'Run Daily',
          variant: 'secondary',
          disabled: !dailyAlternative,
          onClick: function () { finalizeScheduledCreation(selectedAgent, dailyAlternative, dailyName); }
        },
        {
          label: 'Pick Agent',
          variant: 'secondary',
          onClick: function () { openWorkspaceAgentAddFlow(workspaceId); }
        },
        {
          label: 'Open Task Modal',
          variant: 'secondary',
          onClick: function () { openWorkspaceTaskModalForConfiguration(workspaceId, prompt); }
        }
      ]);
      return true;
    }

    appendHomeAssistantMessage(
      'assistant',
      'This task includes scheduling. I can assign it to "' + selectedAgent + '" and set "' + scheduleSummary + '".'
    );
    if (!workspaceHasSelectedAgent) {
      appendHomeAssistantMessage(
        'assistant',
        '"' + selectedAgent + '" is not in this workspace yet. I can add and assign this agent when creating the task.'
      );
    }
    setHomeAssistantRoutingSummary('Scheduled Task Ready', 'Confirm agent assignment and schedule.');

    var actions = [
      {
        label: workspaceHasSelectedAgent ? 'Create Scheduled Task' : 'Add + Create Scheduled Task',
        variant: 'primary',
        onClick: function () { finalizeScheduledCreation(selectedAgent, scheduleDraft.schedule, scheduleDraft.schedule_name); }
      }
    ];

    var alternateAgents = [];
    for (var k = 0; k < candidateAgents.length; k++) {
      if (normalizeToken(candidateAgents[k]) === normalizeToken(selectedAgent)) continue;
      alternateAgents.push(candidateAgents[k]);
      if (alternateAgents.length >= 1) break;
    }
    if (alternateAgents.length > 0) {
      (function (altAgent) {
        actions.push({
          label: 'Use ' + altAgent,
          variant: 'secondary',
          onClick: function () { finalizeScheduledCreation(altAgent, scheduleDraft.schedule, scheduleDraft.schedule_name); }
        });
      })(alternateAgents[0]);
    }

    actions.push({
      label: 'Open Task Modal',
      variant: 'secondary',
      onClick: function () { openWorkspaceTaskModalForConfiguration(workspaceId, prompt); }
    });
    actions.push({
      label: 'Ask Another Task',
      variant: 'secondary',
      onClick: function () { focusHomeAssistantInput(); }
    });

    renderHomeAssistantActions(actions);
    return true;
  }

  async function confirmWorkspaceAssistantTaskCreation(content) {
    var taskText = String(content || '').trim();
    if (!taskText) return false;

    var options = {
      eyebrow: 'Assistant Task',
      title: 'Create this task?',
      message: 'Assistant wants to add this task to the workspace.',
      confirmLabel: 'Create Task',
      cancelLabel: 'Cancel',
      metaItems: ['Assistant', 'Task'],
      details: [taskText]
    };

    if (window.workspaceDetail && typeof window.workspaceDetail.showTaskConfirmDialog === 'function') {
      return window.workspaceDetail.showTaskConfirmDialog(options);
    }

    if (window.WorkspaceHubModals && typeof window.WorkspaceHubModals.showExecutionConfirm === 'function') {
      return window.WorkspaceHubModals.showExecutionConfirm(options);
    }

    return window.confirm([options.title, taskText].join('\n\n'));
  }

  async function handleWorkspaceSlashCommand(commandPayload, routeContext, options) {
    if (!commandPayload || !commandPayload.command) return false;
    if (!hasWorkspaceRouteContext(routeContext)) return false;

    var workspaceId = String(routeContext.workspace_id || '').trim();
    if (!workspaceId) return false;

    var command = String(commandPayload.command).trim();
    var content = String(commandPayload.content || '').trim();
    var displayCommand = '/' + command;
    var isInferredAction = Boolean(options && options.inferred);
    var commandSummaryTitle = isInferredAction ? 'Workspace Action' : 'Workspace Command';

    setHomeAssistantBusy(true, 'Running Command...');
    renderHomeAssistantActions([]);
    setHomeAssistantRoutingSummary(commandSummaryTitle, 'Executing ' + displayCommand + ' in this workspace.');

    try {
      if (command === 'task') {
        if (!content) {
          var taskUsage = 'Usage: /task <task description>';
          appendHomeAssistantMessage('assistant', taskUsage);
          setHomeAssistantRoutingSummary('Workspace Command', taskUsage);
          return true;
        }

        var scheduledTaskHandled = await handleWorkspaceScheduledTaskCreation(content, workspaceId);
        if (scheduledTaskHandled) {
          return true;
        }

        if (window.workspaceDetail && typeof window.workspaceDetail.createTask === 'function') {
          var createdTask = await window.workspaceDetail.createTask(content, '', '', {
            requireConfirmation: true,
            source: 'assistant',
            successToast: false,
            cancelToast: false
          });
          if (createdTask === false) {
            appendHomeAssistantMessage('assistant', 'Task creation cancelled.');
            setHomeAssistantRoutingSummary('Task Cancelled', 'Task creation was cancelled before anything was created.');
            renderHomeAssistantActions([
              {
                label: 'Ask Another Task',
                variant: 'secondary',
                onClick: function () { focusHomeAssistantInput(); }
              }
            ]);
            return true;
          }
          if (!createdTask) {
            throw new Error('Failed to create task');
          }
        } else if (typeof API !== 'undefined' && typeof API.post === 'function') {
          var confirmedByApi = await confirmWorkspaceAssistantTaskCreation(content);
          if (!confirmedByApi) {
            appendHomeAssistantMessage('assistant', 'Task creation cancelled.');
            setHomeAssistantRoutingSummary('Task Cancelled', 'Task creation was cancelled before anything was created.');
            renderHomeAssistantActions([
              {
                label: 'Ask Another Task',
                variant: 'secondary',
                onClick: function () { focusHomeAssistantInput(); }
              }
            ]);
            return true;
          }
          await API.post('/api/orchestration/tasks', {
            workspace_id: workspaceId,
            description: content,
            details: '',
            status: 'pending'
          });
        } else {
          var confirmedByFetch = await confirmWorkspaceAssistantTaskCreation(content);
          if (!confirmedByFetch) {
            appendHomeAssistantMessage('assistant', 'Task creation cancelled.');
            setHomeAssistantRoutingSummary('Task Cancelled', 'Task creation was cancelled before anything was created.');
            renderHomeAssistantActions([
              {
                label: 'Ask Another Task',
                variant: 'secondary',
                onClick: function () { focusHomeAssistantInput(); }
              }
            ]);
            return true;
          }
          var taskResponse = await fetch('/api/orchestration/tasks', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
              workspace_id: workspaceId,
              description: content,
              details: '',
              status: 'pending'
            })
          });
          if (!taskResponse.ok) throw new Error('Failed to create task');
        }

        appendHomeAssistantMessage('assistant', 'Created a task in this workspace.');
        setHomeAssistantRoutingSummary('Task Created', 'Task added to this workspace.');
        renderHomeAssistantActions([
          {
            label: 'Ask Another Task',
            variant: 'secondary',
            onClick: function () { focusHomeAssistantInput(); }
          }
        ]);
        return true;
      }

      if (command === 'note') {
        if (!content) {
          var noteUsage = 'Usage: /note <note content>';
          appendHomeAssistantMessage('assistant', noteUsage);
          setHomeAssistantRoutingSummary('Workspace Command', noteUsage);
          return true;
        }

        if (window.workspaceDetail && typeof window.workspaceDetail.createNote === 'function') {
          await window.workspaceDetail.createNote('Quick Note', content);
        } else if (typeof API !== 'undefined' && typeof API.post === 'function') {
          await API.post('/api/workspaces/' + encodeURIComponent(workspaceId) + '/notes', {
            name: 'Quick Note',
            content: content
          });
        } else {
          var noteResponse = await fetch('/api/workspaces/' + encodeURIComponent(workspaceId) + '/notes', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ name: 'Quick Note', content: content })
          });
          if (!noteResponse.ok) throw new Error('Failed to create note');
        }

        appendHomeAssistantMessage('assistant', 'Created a note in this workspace.');
        setHomeAssistantRoutingSummary('Note Created', 'Note added to this workspace.');
        renderHomeAssistantActions([
          {
            label: 'Ask Another Task',
            variant: 'secondary',
            onClick: function () { focusHomeAssistantInput(); }
          }
        ]);
        return true;
      }

      if (command === 'chat') {
        await createWorkspaceChatSessionWithMessage(workspaceId, content);
        if (content) {
          appendHomeAssistantMessage('assistant', 'Opened chat and sent your initial message.');
          setHomeAssistantRoutingSummary('Chat Started', 'Workspace chat opened and message sent.');
        } else {
          appendHomeAssistantMessage('assistant', 'Opened a new chat in this workspace.');
          setHomeAssistantRoutingSummary('Chat Started', 'Workspace chat opened.');
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
        return true;
      }

      if (command === 'directory') {
        var directoryPath = sanitizeWorkspaceCommandContent(content);
        if (!directoryPath) {
          directoryPath = extractLikelyPathFromText(content);
        }

        if (window.workspaceDetail && typeof window.workspaceDetail.addDirectory === 'function' && directoryPath) {
          await window.workspaceDetail.addDirectory(directoryPath);
        } else if (window.workspaceDetail && typeof window.workspaceDetail.showAddDirectoryModal === 'function') {
          await window.workspaceDetail.showAddDirectoryModal();
        } else if (directoryPath) {
          var directorySegments = String(directoryPath).split(/[\\/]/).filter(Boolean);
          var directoryTitle = directorySegments.length > 0 ? directorySegments[directorySegments.length - 1] : directoryPath;
          var directoryResponse = await fetch(`/api/workspaces/${encodeURIComponent(workspaceId)}/attachments`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
              type: 'directory',
              path: directoryPath,
              title: directoryTitle
            })
          });
          if (!directoryResponse.ok) throw new Error('Failed to add directory');
        } else {
          throw new Error('Directory picker unavailable');
        }

        if (directoryPath) {
          appendHomeAssistantMessage('assistant', 'Added directory "' + directoryPath + '" to this workspace.');
          setHomeAssistantRoutingSummary('Directory Added', 'Directory linked to this workspace.');
        } else {
          appendHomeAssistantMessage('assistant', 'Opened the folder picker. Select a directory to add it to this workspace.');
          setHomeAssistantRoutingSummary('Directory Picker', 'Select a folder to add it to this workspace.');
        }
        renderHomeAssistantActions([
          {
            label: 'Ask Another Task',
            variant: 'secondary',
            onClick: function () { focusHomeAssistantInput(); }
          }
        ]);
        return true;
      }

      if (command === 'file') {
        if (window.workspaceDetail && typeof window.workspaceDetail.showFileModal === 'function') {
          window.workspaceDetail.showFileModal();
        } else if (window.WorkspaceHubFiles && typeof window.WorkspaceHubFiles.openAddFileModal === 'function') {
          window.WorkspaceHubFiles.openAddFileModal();
        } else {
          throw new Error('File upload modal unavailable');
        }

        if (content) {
          appendHomeAssistantMessage('assistant', 'Opened the upload modal for "' + content + '". Select the file to attach it to this workspace.');
        } else {
          appendHomeAssistantMessage('assistant', 'Opened the upload modal. Select file(s) to attach to this workspace.');
        }
        setHomeAssistantRoutingSummary('File Upload', 'File upload modal is ready for this workspace.');
        renderHomeAssistantActions([
          {
            label: 'Ask Another Task',
            variant: 'secondary',
            onClick: function () { focusHomeAssistantInput(); }
          }
        ]);
        return true;
      }
    } catch (error) {
      dashLog.debug('Workspace slash command failed', {
        command: command,
        workspaceId: workspaceId,
        error: error && error.message || error
      });
      appendHomeAssistantMessage('assistant', 'I could not run ' + displayCommand + ' right now. Please try again.');
      setHomeAssistantRoutingSummary('Workspace Command Failed', 'Could not execute ' + displayCommand + ' right now.');
      renderHomeAssistantActions([
        {
          label: 'Retry',
          variant: 'primary',
          onClick: function () { focusHomeAssistantInput(); }
        },
        {
          label: 'Ask Another Task',
          variant: 'secondary',
          onClick: function () { focusHomeAssistantInput(); }
        }
      ]);
      return true;
    } finally {
      setHomeAssistantBusy(false);
    }

    return false;
  }

  function buildWorkspaceNameFromPrompt(prompt) {
    var normalized = normalizeToken(prompt);
    if (!normalized) return 'New Workspace';

    var ignored = {
      'a': true,
      'an': true,
      'and': true,
      'for': true,
      'from': true,
      'help': true,
      'i': true,
      'is': true,
      'it': true,
      'lets': true,
      'my': true,
      'of': true,
      'please': true,
      'that': true,
      'the': true,
      'to': true,
      'with': true,
      'you': true,
      'build': true,
      'create': true,
      'develop': true,
      'design': true,
      'implement': true,
      'make': true,
      'setup': true,
      'start': true
    };

    var tokens = uniqueValues(normalized.split(/[^a-z0-9]+/g));
    var selected = [];
    for (var i = 0; i < tokens.length; i++) {
      var token = tokens[i];
      if (!token || ignored[token]) continue;
      if (token.length <= 1) continue;
      selected.push(token);
      if (selected.length >= 6) break;
    }

    if (selected.length === 0) {
      for (var j = 0; j < tokens.length; j++) {
        var fallback = tokens[j];
        if (!fallback || fallback.length <= 1) continue;
        selected.push(fallback);
        if (selected.length >= 4) break;
      }
    }

    var candidate = toTitleCase(selected.join(' '));
    if (!candidate) candidate = 'New Workspace';
    if (candidate.length > 56) candidate = candidate.slice(0, 56).trim();
    return candidate || 'New Workspace';
  }

  function buildWorkspaceDescriptionFromPrompt(prompt) {
    var text = truncateText(String(prompt || '').trim(), 280);
    if (!text) return 'Created from Ask Ori.';
    return 'Created from Ask Ori task: "' + text + '"';
  }

  function buildWorkspaceBootstrapFromPrompt(prompt) {
    var text = truncateText(String(prompt || '').trim(), 420);
    if (!text) return null;
    return {
      goal: text,
      systems: '',
      context: ''
    };
  }

  async function openCreateWorkspaceModalWithSeed(seedPayload) {
    var modalElement = document.getElementById('addFolderModal');
    var nameInput = document.getElementById('folderNameInput');
    var descriptionInput = document.getElementById('folderDescriptionInput');
    var primaryGoalInput = document.getElementById('folderPrimaryGoalInput');
    var systemsInput = document.getElementById('folderSystemsInput');
    var contextInput = document.getElementById('folderContextInput');
    var parentSelect = document.getElementById('folderParentSelect');
    if (!modalElement || !nameInput || typeof bootstrap === 'undefined' || !bootstrap.Modal) {
      return { status: 'unavailable', reason: 'workspace_modal_prerequisites_missing' };
    }

    nameInput.value = String(seedPayload && seedPayload.name || '').trim();
    if (descriptionInput) descriptionInput.value = String(seedPayload && seedPayload.description || '').trim();
    if (primaryGoalInput) {
      primaryGoalInput.value = String(seedPayload && seedPayload.workspaceBootstrap && seedPayload.workspaceBootstrap.goal || '').trim();
    }
    if (systemsInput) {
      systemsInput.value = String(seedPayload && seedPayload.workspaceBootstrap && seedPayload.workspaceBootstrap.systems || '').trim();
    }
    if (contextInput) {
      contextInput.value = String(seedPayload && seedPayload.workspaceBootstrap && seedPayload.workspaceBootstrap.context || '').trim();
    }
    if (parentSelect) {
      parentSelect.value = '';
    }
    modalElement.dataset.askOriPostCreate = 'open_workspace_dashboard';
    if (seedPayload && seedPayload.seedNote) {
      modalElement.dataset.askOriSeedNote = JSON.stringify(seedPayload.seedNote);
    } else {
      delete modalElement.dataset.askOriSeedNote;
    }
    if (seedPayload && seedPayload.seedTask) {
      modalElement.dataset.askOriSeedTask = JSON.stringify(seedPayload.seedTask);
    } else {
      delete modalElement.dataset.askOriSeedTask;
    }
    if (!modalElement.dataset.askOriCleanupBound) {
      modalElement.addEventListener('hidden.bs.modal', function () {
        delete modalElement.dataset.askOriPostCreate;
        delete modalElement.dataset.askOriSeedNote;
        delete modalElement.dataset.askOriSeedTask;
      }, true);
      modalElement.dataset.askOriCleanupBound = '1';
    }

    var colorButtons = modalElement.querySelectorAll('.folder-color-btn');
    for (var i = 0; i < colorButtons.length; i++) {
      colorButtons[i].classList.remove('active');
    }
    var defaultColorBtn = modalElement.querySelector('.folder-color-btn[data-color=""]');
    if (defaultColorBtn) {
      defaultColorBtn.classList.add('active');
    } else if (colorButtons.length > 0) {
      colorButtons[0].classList.add('active');
    }

    closeHomeAssistantThinkingModal({ force: true });
    var modalInstance = bootstrap.Modal.getInstance(modalElement) || new bootstrap.Modal(modalElement);
    modalInstance.show();

    window.setTimeout(function () {
      if (!nameInput) return;
      nameInput.focus();
      if (typeof nameInput.select === 'function') {
        nameInput.select();
      }
    }, 120);

    return { status: 'opened' };
  }

  async function createWorkspaceByName(workspaceName, prompt) {
    var name = String(workspaceName || '').trim() || 'New Workspace';
    var sourcePrompt = String(prompt || '').trim();
    var payload = {
      name: name,
      description: buildWorkspaceDescriptionFromPrompt(sourcePrompt || ('Create workspace: ' + name)),
      workspaceBootstrap: buildWorkspaceBootstrapFromPrompt(sourcePrompt || ('Create workspace: ' + name))
    };

    setHomeAssistantBusy(true, 'Preparing Workspace...');
    renderHomeAssistantActions([]);
    appendHomeAssistantMessage('assistant', 'Opening the Create Workspace modal for "' + name + '"...');
    setHomeAssistantRoutingSummary('Workspace', 'Preparing Create Workspace modal.');

    try {
      var modalResult = await openCreateWorkspaceModalWithSeed(payload);
      if (!modalResult || modalResult.status !== 'opened') {
        throw new Error('Workspace create modal unavailable');
      }

      appendHomeAssistantMessage('assistant', 'Create Workspace is ready. Review details and click Create. I will open its Workspace Dashboard next.');
      setHomeAssistantRoutingSummary('Workspace', 'Review and confirm in the Create Workspace modal.');
      renderHomeAssistantActions([
        {
          label: 'Ask Another Task',
          variant: 'secondary',
          onClick: function () { focusHomeAssistantInput(); }
        }
      ]);
    } catch (error) {
      dashLog.debug('Opening workspace create modal from Ask Ori failed', { error: error && error.message || error });
      appendHomeAssistantMessage('assistant', 'I could not open the Create Workspace modal right now. You can open Workspaces and create it there.');
      setHomeAssistantRoutingSummary('Workspace Modal Failed', 'Could not open Create Workspace modal automatically.');
      renderHomeAssistantActions([
        {
          label: 'Open Workspaces',
          variant: 'primary',
          onClick: function () { window.location.href = '/workspaces'; }
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

  async function createWorkspaceFromPrompt(prompt, options) {
    var text = String(prompt || '').trim();
    if (!text) return;

    var agentName = options && options.agentName ? String(options.agentName).trim() : '';
    var appLaunchRequest = options && options.appLaunchRequest ? options.appLaunchRequest : null;
    var routeContext = options && options.routeContext ? options.routeContext : buildHomeRouteContext();
    var payload = {
      name: buildWorkspaceNameFromPrompt(text),
      description: buildWorkspaceDescriptionFromPrompt(text),
      workspaceBootstrap: buildWorkspaceBootstrapFromPrompt(text),
      seedNote: options && options.seedNote ? options.seedNote : null,
      seedTask: options && options.seedTask ? options.seedTask : null
    };

    setHomeAssistantBusy(true, 'Preparing Workspace...');
    renderHomeAssistantActions([]);
    appendHomeAssistantMessage('assistant', 'Opening Create Workspace so you can review details first...');
    setHomeAssistantRoutingSummary('Workspace', 'Preparing Create Workspace modal.');

    try {
      var modalResult = await openCreateWorkspaceModalWithSeed(payload);
      if (!modalResult || modalResult.status !== 'opened') {
        throw new Error('Workspace create modal unavailable');
      }

      appendHomeAssistantMessage('assistant', 'Create Workspace is ready. Review details and click Create. I will open its Workspace Dashboard next.');
      setHomeAssistantRoutingSummary('Workspace', 'Review and confirm in the Create Workspace modal.');
      renderHomeAssistantActions([
        {
          label: 'Continue in Chat',
          variant: 'secondary',
          disabled: !agentName,
          onClick: function () { runPendingTaskWithAgent(text, agentName, { appLaunchRequest: appLaunchRequest, routeContext: routeContext }); }
        },
        {
          label: 'Ask Another Task',
          variant: 'secondary',
          onClick: function () { focusHomeAssistantInput(); }
        }
      ]);
    } catch (error) {
      dashLog.debug('Opening workspace create modal from Ask Ori failed', { error: error && error.message || error });
      appendHomeAssistantMessage('assistant', 'I could not open Create Workspace automatically. You can open Workspaces manually or continue in chat.');
      setHomeAssistantRoutingSummary('Workspace Modal Failed', 'Could not open Create Workspace modal automatically.');
      renderHomeAssistantActions([
        {
          label: 'Open Workspaces',
          variant: 'primary',
          onClick: function () { window.location.href = '/workspaces'; }
        },
        {
          label: 'Continue in Chat',
          variant: 'secondary',
          disabled: !agentName,
          onClick: function () { runPendingTaskWithAgent(text, agentName, { appLaunchRequest: appLaunchRequest, routeContext: routeContext }); }
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
    var routeContext = options && options.routeContext ? options.routeContext : buildHomeRouteContext();
    var dispatchMessage = buildAskOriDispatchMessage(prompt, appLaunchRequest, dispatchIntent, routeContext);

    setHomeAssistantBusy(true, 'Opening Chat...');
    renderHomeAssistantActions([]);
    appendHomeAssistantMessage('assistant', 'Opening a chat session with "' + agentName + '"...');
    setHomeAssistantRoutingSummary('Handoff', 'Routing task to "' + agentName + '"...');
    if (appLaunchRequest && appLaunchRequest.appName) {
      appendHomeAssistantMessage('assistant',
        'Routing steps: 1) Start a new session. 2) Execute /openapp ' + appLaunchRequest.appName + ' to launch the app.');
    }

    try {
      var session = await dispatchPromptToChatSession(dispatchMessage, agentName, routeContext);
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

  function shouldConfirmMatchedAgentBeforeHandoff(routeContext) {
    return hasWorkspaceRouteContext(routeContext) || isSemiAutoMode();
  }

  function promptForMatchedAgentSelection(prompt, agentName, options) {
    if (!prompt || !agentName) return;
    var routeContext = options && options.routeContext ? options.routeContext : buildHomeRouteContext();
    var appLaunchRequest = options && options.appLaunchRequest ? options.appLaunchRequest : null;
    var inWorkspaceContext = hasWorkspaceRouteContext(routeContext);
    var shortAgentName = truncateText(agentName, 32);

    appendHomeAssistantMessage(
      'assistant',
      inWorkspaceContext
        ? 'I found "' + agentName + '" for this workspace task. Do you want to use this agent, or create a new one before I continue?'
        : 'I found "' + agentName + '" for this task. Do you want to use this agent, or create a new one before I continue?'
    );
    setHomeAssistantRoutingSummary(
      'Confirmation Required',
      inWorkspaceContext
        ? 'Choose an existing agent or create a new one for this workspace task.'
        : 'Choose an existing agent or create a new one before handoff.'
    );
    renderHomeAssistantActions([
      {
        label: 'Use ' + shortAgentName,
        variant: 'primary',
        onClick: function () {
          runPendingTaskWithAgent(prompt, agentName, {
            appLaunchRequest: appLaunchRequest,
            routeContext: routeContext
          });
        }
      },
      {
        label: 'Create New Agent',
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

  async function handleHomeAssistantPrompt(prompt, options) {
    var text = String(prompt || '').trim();
    if (!text) return;
    clearHomeAssistantPlanning();
    clearHomeAssistantInlineReply();
    setHomeAssistantMode('new_task');
    var appLaunchRequest = parseAppLaunchRequest(text);
    var routeContext = normalizeHomeRouteContext(options && options.routeContext);
    var inWorkspaceContext = hasWorkspaceRouteContext(routeContext);
    var directWorkspaceCommand = parseCreateWorkspaceCommand(text);
    var workspaceSlashCommand = parseWorkspaceSlashCommand(text);

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
    homeAssistantState.pendingIntentVariant = '';
    homeAssistantState.pendingAgentName = '';
    homeAssistantState.pendingSuggestedName = '';
    homeAssistantState.pendingSuggestedType = '';
    homeAssistantState.pendingAppLaunch = appLaunchRequest;
    homeAssistantState.pendingCapabilityPlan = null;
    homeAssistantState.pendingCapabilityBrief = '';
    homeAssistantState.awaitingCreateConfirmation = false;

    appendHomeAssistantMessage('user', text);

    if (workspaceSlashCommand) {
      var slashHandled = await handleWorkspaceSlashCommand(workspaceSlashCommand, routeContext);
      if (slashHandled) return;
    }

    if (directWorkspaceCommand && directWorkspaceCommand.name) {
      await createWorkspaceByName(directWorkspaceCommand.name, text);
      return;
    }

    if (inWorkspaceContext) {
      var specialistHandoffHandled = await routeWorkspacePromptToPlanningSpecialist(
        text,
        routeContext,
        homeAssistantState.pendingIntent
      );
      if (specialistHandoffHandled) {
        return;
      }

      var workspaceManagerLabel = getWorkspaceHomeAssistantDisplayName();
      setHomeAssistantBusy(true, 'Asking...');
      renderHomeAssistantActions([]);
      setHomeAssistantRoutingSummary(
        workspaceManagerLabel,
        'Sending your request to the workspace manager...'
      );

      try {
        await openWorkspaceAssistantForPrompt(text, routeContext, homeAssistantState.pendingIntent);
      } catch (error) {
        dashLog.debug('Workspace manager handoff failed', { error: error && error.message || error });
        homeAssistantState.awaitingCreateConfirmation = false;
        var workspaceManagerTimedOut = isLikelyHomeAssistantRequestTimeout(error);
        var failureSummary = workspaceManagerTimedOut
          ? null
          : formatWorkspaceManagerFailure(error, workspaceManagerLabel);
        setHomeAssistantRoutingSummary(
          workspaceManagerTimedOut ? workspaceManagerLabel + ' Delayed' : failureSummary.title,
          workspaceManagerTimedOut
            ? 'The workspace manager took too long to respond inline.'
            : failureSummary.text,
          workspaceManagerTimedOut
            ? { state: 'timeout' }
            : {
              state: failureSummary.state,
              detail: failureSummary.detail,
              heading: failureSummary.heading,
              conversationSummary: failureSummary.conversationSummary
            }
        );
        if (hasHomeAssistantConversation()) {
          homeAssistantState.conversationCollapsed = true;
          syncHomeAssistantConversationSection();
        }
        renderHomeAssistantActions([
          {
            label: 'Retry',
            variant: 'primary',
            onClick: function () { handleHomeAssistantPrompt(text, { routeContext: routeContext }); }
          },
          {
            label: 'Open Full Chat',
            variant: 'secondary',
            onClick: function () { openChatPanel(); }
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
      return;
    }

    setHomeAssistantBusy(true, 'Routing...');
    renderHomeAssistantActions([]);
    setHomeAssistantRoutingSummary('Routing', 'Analyzing task and selecting the best agent...');

    try {
      var routeData = await routePromptWithBackend(text, routeContext);
      var match = null;
      var useFallbackRouting = !routeData;
      var workspaceRecommended = false;

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
        if (typeof routeData.intent_variant === 'string') {
          homeAssistantState.pendingIntentVariant = normalizeToken(routeData.intent_variant);
        }
        if (appLaunchRequest) {
          if (!homeAssistantState.pendingSuggestedName) {
            homeAssistantState.pendingSuggestedName = HOME_INTENTS.app_launch.suggestedName;
          }
          if (!homeAssistantState.pendingSuggestedType) {
            homeAssistantState.pendingSuggestedType = HOME_INTENTS.app_launch.defaultType;
          }
        }
        if (typeof routeData.workspace_recommended === 'boolean') {
          workspaceRecommended = routeData.workspace_recommended;
        }
        if (routeContextTargetsCurrentWorkspace(routeData, routeContext)) {
          workspaceRecommended = false;
        }

        if (inWorkspaceContext && shouldOpenWorkspaceAssistantForRoute(routeData, routeContext)) {
          await openWorkspaceAssistantForPrompt(text, routeContext, homeAssistantState.pendingIntent);
          return;
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

      if (inWorkspaceContext && shouldOpenWorkspaceAssistantForRoute(routeData, routeContext)) {
        await openWorkspaceAssistantForPrompt(text, routeContext, homeAssistantState.pendingIntent);
        return;
      }

      if (useFallbackRouting) {
        var agents = await fetchAgentsForMatching();
        match = findSuitableAgent(agents, homeAssistantState.pendingIntent, text);
      }
      if ((!routeData || typeof routeData.workspace_recommended !== 'boolean') && !inWorkspaceContext) {
        workspaceRecommended = isComplexProjectPrompt(text, homeAssistantState.pendingIntent);
      }
      if (inWorkspaceContext) {
        workspaceRecommended = false;
      }
      if (homeAssistantState.pendingIntent.key === 'calendar_check' && !homeAssistantState.pendingIntentVariant) {
        homeAssistantState.pendingIntentVariant = inferCalendarIntentVariant(text, routeContext, routeData);
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
        var matchedCapabilityHandled = await handleCapabilityResolutionFlow({
          prompt: text,
          routeContext: routeContext,
          routeData: routeData,
          matchedAgentName: match.agent.name,
          appLaunchRequest: appLaunchRequest
        });
        if (matchedCapabilityHandled) {
          return;
        }
        if (workspaceRecommended) {
          appendHomeAssistantMessage('assistant',
            'This looks like a complex project. I recommend starting in a Workspace so files, tasks, and context stay organized. Do you want me to create one now?');
          setHomeAssistantRoutingSummary('Workspace Recommended', 'Complex project detected. Create a workspace or continue in chat.');
          renderHomeAssistantActions([
            {
              label: 'Create Workspace',
              variant: 'primary',
              onClick: function () {
                createWorkspaceFromPrompt(text, {
                  agentName: match.agent.name,
                  appLaunchRequest: appLaunchRequest,
                  routeContext: routeContext
                });
              }
            },
            {
              label: 'Continue in Chat',
              variant: 'secondary',
              onClick: function () { runPendingTaskWithAgent(text, match.agent.name, { appLaunchRequest: appLaunchRequest, routeContext: routeContext }); }
            },
            {
              label: 'Open Workspaces',
              variant: 'secondary',
              onClick: function () { window.location.href = '/workspaces'; }
            },
            {
              label: 'Ask Another Task',
              variant: 'secondary',
              onClick: function () { focusHomeAssistantInput(); }
            }
          ]);
          return;
        }
        if (shouldConfirmMatchedAgentBeforeHandoff(routeContext)) {
          promptForMatchedAgentSelection(text, match.agent.name, {
            appLaunchRequest: appLaunchRequest,
            routeContext: routeContext
          });
          return;
        }
        await runPendingTaskWithAgent(text, match.agent.name, { appLaunchRequest: appLaunchRequest, routeContext: routeContext });
      } else {
        var unmatchedCapabilityHandled = await handleCapabilityResolutionFlow({
          prompt: text,
          routeContext: routeContext,
          routeData: routeData,
          matchedAgentName: '',
          appLaunchRequest: appLaunchRequest
        });
        if (unmatchedCapabilityHandled) {
          return;
        }
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
    var supportsRecentSessions = Boolean(els.recentSection || els.recentSessions || els.viewAllBtn || els.clearRecentBtn);
    var routeContext = buildHomeRouteContext();

    homeAssistantState.automationMode = loadHomeAssistantAutomationMode();
    homeAssistantState.recentSessions = supportsRecentSessions ? loadHomeAssistantRecentSessions() : [];
    setHomeAssistantRoutingSummary('', '');
    renderHomeAssistantRecentSessions();
    if (supportsRecentSessions) {
      hydrateHomeAssistantRecentSessions();
    }
    setHomeAssistantMode(homeAssistantState.mode);
    if (hasWorkspaceRouteContext(routeContext)) {
      refreshHomeAssistantWorkspaceIdentity(routeContext);
    }
    setHomeAssistantAutomationMode(homeAssistantState.automationMode);
    syncHomeAssistantThinkingStatus();
    syncHomeAssistantLauncher();

    if (window.EventBus && typeof EventBus.on === 'function') {
      EventBus.on('session:deleted', function (payload) {
        if (!payload || !payload.sessionId) return;
        removeTrackedSession(payload.sessionId);
      });
    }

    if (els.form && els.input) {
      els.form.addEventListener('submit', handleHomeAssistantSubmit);
    }
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

  async function submitPromptViaAskOri(prompt, options) {
    var text = String(prompt || '').trim();
    if (!text) {
      return { handled: false, reason: 'empty_prompt' };
    }

    var routeContext = normalizeHomeRouteContext(options && options.routeContext);
    if (!options || options.openThinkingModal !== false) {
      openHomeAssistantThinkingModal();
    }

    await handleHomeAssistantPrompt(text, { routeContext: routeContext });
    return { handled: true, routeContext: routeContext };
  }

  window.OriAskRouting = window.OriAskRouting || {};
  window.OriAskRouting.submit = submitPromptViaAskOri;
  window.OriAskRouting.buildRouteContext = normalizeHomeRouteContext;
  window.OriAskRouting.refreshWorkspaceIdentity = refreshHomeAssistantWorkspaceIdentity;

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
