// Dashboard Module
// Fetches and renders Ori dashboard data: assistant progress, stats, and agent list

(function () {
  'use strict';

  const dashLog = typeof Logger !== 'undefined' ? Logger.withContext('Dashboard') : console;
  const XP_PER_LEVEL = 100;

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
