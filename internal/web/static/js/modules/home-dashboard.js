// home-dashboard.js — adaptive home page behavior.
//
// Wires the suggested prompt chips (populate-and-focus the Ask Ori input,
// no auto-submit), the global Cmd+J / Ctrl+J shortcut that focuses the
// hero input, and the three dashboard sections (Recent Workspaces,
// Upcoming Scheduled Tasks, Recent Activity) populated from their
// respective API endpoints. Also instruments time-to-first-action (TTfA)
// for the home dashboard — the primary success metric from the PRD.
//
// Loaded globally via base.tmpl. All listeners and fetches early-return
// when the home page's elements aren't present, so this module is a no-op
// on other pages.

(function () {
  if (typeof document === 'undefined') return;

  // ----- Time-to-first-action instrumentation -----

  // Capture the page start time as soon as the script runs. The "first
  // action" is whichever qualifying interaction happens first (chip click,
  // card click, hero submit, skip click, etc.) — see fireTTFA callers.
  const TTFA_START = (typeof performance !== 'undefined' && performance.now) ? performance.now() : Date.now();
  let ttfaFired = false;

  function fireTTFA(source) {
    if (ttfaFired) return;
    ttfaFired = true;
    const now = (typeof performance !== 'undefined' && performance.now) ? performance.now() : Date.now();
    const ms = Math.round(now - TTFA_START);
    // Structured console marker — easy to grep, easy to swap for a real
    // analytics beacon (navigator.sendBeacon) once an endpoint exists.
    try {
      // eslint-disable-next-line no-console
      console.info('[home.ttfa]', { source, ms });
    } catch (_) { /* ignore */ }
  }

  // ----- HTML utilities -----

  function escapeHtml(s) {
    return String(s ?? '').replace(/[&<>"']/g, (c) => (
      { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]
    ));
  }

  function relTime(ts) {
    const fn = (typeof window !== 'undefined' && window.RelativeTime?.formatRelativeTime);
    if (!fn) return ''; // utility not yet loaded; skip rather than crash
    return fn(ts);
  }

  // ----- Chips + focus shortcut (carried over from earlier phase) -----

  function wireChips() {
    const chips = document.querySelectorAll('.home-prompt-chip');
    if (!chips.length) return;
    const input = document.getElementById('homeAssistantInput');
    if (!input) return;
    chips.forEach((chip) => {
      chip.addEventListener('click', (e) => {
        e.preventDefault();
        fireTTFA('chip');
        const prompt = (chip.getAttribute('data-prompt') || chip.textContent || '').trim();
        if (!prompt) return;
        input.value = prompt;
        input.focus();
        try {
          const len = input.value.length;
          input.setSelectionRange(len, len);
        } catch (_) { /* ignore */ }
      });
    });

    // Hero submit and ⌘J focus are first-class actions too — observe them
    // at the surface level so TTfA fires regardless of which path the user
    // takes. dashboard.js owns the actual submit handler.
    const form = document.getElementById('homeAssistantForm');
    if (form) {
      form.addEventListener('submit', () => fireTTFA('hero-submit'), { capture: true });
    }
  }

  function wireFocusShortcut() {
    document.addEventListener('keydown', (e) => {
      if (!(e.metaKey || e.ctrlKey) || e.altKey || e.shiftKey) return;
      if (e.key !== 'j' && e.key !== 'J') return;

      const input = document.getElementById('homeAssistantInput');
      if (!input) return;

      const target = e.target;
      if (target && (target.tagName === 'INPUT' || target.tagName === 'TEXTAREA' || target.isContentEditable)) {
        if (target !== input) return;
      }

      e.preventDefault();
      fireTTFA('cmd-j');
      input.focus();
      try {
        const len = input.value.length;
        input.setSelectionRange(len, len);
      } catch (_) { /* ignore */ }
    });
  }

  // ----- Section: Recent Workspaces -----

  async function loadRecentWorkspaces() {
    const section = document.getElementById('homeRecentWorkspaces');
    if (!section) return;
    const body = section.querySelector('[data-role="content"]');
    if (!body) return;

    try {
      const resp = await fetch('/api/workspaces');
      if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
      const data = await resp.json();
      const all = Array.isArray(data.workspaces) ? data.workspaces : [];
      all.sort((a, b) => new Date(b.updated_at || b.created_at || 0) - new Date(a.updated_at || a.created_at || 0));
      const visible = all.slice(0, 6);

      const viewAll = section.querySelector('[data-role="view-all"]');
      if (viewAll) viewAll.hidden = all.length <= 6;

      body.innerHTML = renderWorkspaceCards(visible);
      wireWorkspaceCardClicks(body);
    } catch (err) {
      // eslint-disable-next-line no-console
      console.error('home-dashboard: failed to load workspaces', err);
      body.innerHTML = '<div class="home-section-placeholder">Could not load workspaces.</div>';
    }
  }

  function renderWorkspaceCards(workspaces) {
    const cards = workspaces.map((ws) => {
      const id = escapeHtml(ws.id || '');
      const name = escapeHtml(ws.name || 'Untitled workspace');
      const desc = escapeHtml(ws.description || '');
      const updated = relTime(ws.updated_at || ws.created_at);
      // `agent_instances` isn't on the list endpoint; fall back to `agents`.
      const agentCount = Array.isArray(ws.agents) ? ws.agents.length : 0;
      const countLabel = agentCount === 1 ? '1 agent' : `${agentCount} agents`;
      return `
        <a href="/workspaces/${id}" class="home-workspace-card" data-role="workspace-card">
          <div class="home-workspace-card-name">${name}</div>
          ${desc ? `<div class="home-workspace-card-desc">${desc}</div>` : ''}
          <div class="home-workspace-card-meta">
            <span class="home-workspace-card-agents">${escapeHtml(countLabel)}</span>
            ${updated ? `<span class="home-workspace-card-time">${escapeHtml(updated)}</span>` : ''}
          </div>
        </a>
      `;
    }).join('');

    const newTile = `
      <a href="/workspaces" class="home-workspace-card home-workspace-card-new" data-role="new-workspace">
        <div class="home-workspace-card-new-icon" aria-hidden="true">+</div>
        <div class="home-workspace-card-new-label">New workspace</div>
      </a>
    `;

    return `<div class="home-workspace-strip">${cards}${newTile}</div>`;
  }

  function wireWorkspaceCardClicks(_body) {
    // Cards are anchor elements — native navigation already works. The
    // hook stays here so future enhancements (e.g. middle-click hint)
    // have a clean attach point. TTfA is fired via the delegated
    // listener on #homeDashboardSections (see wireDashboardActions).
  }

  // wireDashboardActions installs a single delegated click listener on the
  // sections container that fires the TTfA beacon for any qualifying user
  // action (workspace card, new-workspace tile, view-all link, upcoming row,
  // activity row, view-all link). One listener instead of N keeps the JS
  // small and survives re-renders.
  function wireDashboardActions() {
    const sections = document.getElementById('homeDashboardSections');
    if (!sections) return;
    sections.addEventListener('click', (e) => {
      const t = e.target;
      if (!t) return;
      const card = t.closest('[data-role="workspace-card"]');
      if (card) { fireTTFA('workspace-card'); return; }
      const newTile = t.closest('[data-role="new-workspace"]');
      if (newTile) { fireTTFA('new-workspace'); return; }
      const viewAll = t.closest('[data-role="view-all"]');
      if (viewAll) { fireTTFA('view-all'); return; }
      const rowLink = t.closest('.home-row-link');
      if (rowLink) {
        const inUpcoming = rowLink.closest('#homeUpcomingTasks');
        fireTTFA(inUpcoming ? 'upcoming-row' : 'activity-row');
      }
    });
  }

  // ----- Section: Upcoming Scheduled Tasks -----

  async function loadUpcoming() {
    const section = document.getElementById('homeUpcomingTasks');
    if (!section) return;
    const body = section.querySelector('[data-role="content"]');
    if (!body) return;

    try {
      const resp = await fetch('/api/orchestration/scheduled-tasks/upcoming?limit=5');
      if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
      const data = await resp.json();
      const rows = Array.isArray(data.upcoming) ? data.upcoming : [];

      if (rows.length === 0) {
        body.innerHTML = `
          <div class="home-section-empty">
            No scheduled tasks. <a href="/workspaces" class="home-section-link">Schedule one →</a>
          </div>
        `;
        return;
      }
      body.innerHTML = `<ul class="home-row-list">${rows.map(renderUpcomingRow).join('')}</ul>`;
    } catch (err) {
      // eslint-disable-next-line no-console
      console.error('home-dashboard: failed to load upcoming tasks', err);
      body.innerHTML = '<div class="home-section-placeholder">Could not load upcoming tasks.</div>';
    }
  }

  function renderUpcomingRow(row) {
    const wsId = encodeURIComponent(row.workspace_id || '');
    const href = wsId ? `/workspaces/${wsId}` : '#';
    return `
      <li class="home-row">
        <a href="${href}" class="home-row-link">
          <span class="home-row-primary">${escapeHtml(row.task_name || '(untitled task)')}</span>
          <span class="home-row-meta">
            <span class="home-row-workspace">${escapeHtml(row.workspace_name || '')}</span>
            ${row.agent_name ? `<span class="home-row-agent">${escapeHtml(row.agent_name)}</span>` : ''}
            <span class="home-row-time">${escapeHtml(relTime(row.next_run))}</span>
          </span>
        </a>
      </li>
    `;
  }

  // ----- Section: Recent Activity -----

  async function loadRecentActivity() {
    const section = document.getElementById('homeRecentActivity');
    if (!section) return;
    const body = section.querySelector('[data-role="content"]');
    if (!body) return;

    try {
      const resp = await fetch('/api/activity/recent?limit=5');
      if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
      const data = await resp.json();
      const events = Array.isArray(data.events) ? data.events : [];

      if (events.length === 0) {
        body.innerHTML = '<div class="home-section-empty">Nothing to show yet.</div>';
        return;
      }
      body.innerHTML = `<ul class="home-row-list">${events.map(renderActivityRow).join('')}</ul>`;
    } catch (err) {
      // eslint-disable-next-line no-console
      console.error('home-dashboard: failed to load recent activity', err);
      body.innerHTML = '<div class="home-section-placeholder">Could not load recent activity.</div>';
    }
  }

  function activityIcon(kind) {
    switch (kind) {
      case 'note_edited':           return '✎';
      case 'task_completed':        return '✓';
      case 'scheduled_task_fired':  return '⏰';
      case 'scheduled_task_failed': return '⚠';
      default:                      return '•';
    }
  }

  function renderActivityRow(ev) {
    const wsId = encodeURIComponent(ev.workspace_id || '');
    // Best-effort target navigation. Notes get a deep link via the workspace
    // notes route; tasks/fires route to the workspace itself.
    let href = wsId ? `/workspaces/${wsId}` : '#';
    if (ev.kind === 'note_edited' && wsId && ev.target_id) {
      href = `/workspaces/${wsId}/notes/${encodeURIComponent(ev.target_id)}`;
    }
    return `
      <li class="home-row home-row-activity">
        <a href="${href}" class="home-row-link">
          <span class="home-row-icon" aria-hidden="true">${escapeHtml(activityIcon(ev.kind))}</span>
          <span class="home-row-primary">${escapeHtml(ev.description || '')}</span>
          <span class="home-row-meta">
            <span class="home-row-workspace">${escapeHtml(ev.workspace_name || '')}</span>
            <span class="home-row-time">${escapeHtml(relTime(ev.timestamp))}</span>
          </span>
        </a>
      </li>
    `;
  }

  // ----- First-run onboarding (CTA + Skip) -----

  const ONBOARDING_DISMISSED_KEY = 'home.onboardingDismissed';

  function sessionFlagSet() {
    try { return sessionStorage.getItem(ONBOARDING_DISMISSED_KEY) === '1'; }
    catch (_) { return false; }
  }

  function setSessionFlag() {
    try { sessionStorage.setItem(ONBOARDING_DISMISSED_KEY, '1'); }
    catch (_) { /* private mode etc. — skip flag, dashboard still shows */ }
  }

  function applyFirstRunState() {
    // Only meaningful when both CTA and a hidden dashboard are present
    // (first-run server context). For returning users, only the dashboard
    // is rendered and this function is a no-op.
    const cta = document.getElementById('homeFirstRunCTA');
    const sections = document.getElementById('homeDashboardSections');
    if (!cta || !sections) return false; // not first-run, or markup missing

    if (sessionFlagSet()) {
      cta.hidden = true;
      sections.hidden = false;
      return true; // dashboard is visible; caller should fetch
    }
    return false; // CTA still showing; skip fetches
  }

  function wireFirstRunButtons() {
    const skipBtn = document.getElementById('homeFirstRunSkip');
    if (skipBtn) {
      skipBtn.addEventListener('click', (e) => {
        e.preventDefault();
        fireTTFA('first-run-skip');
        setSessionFlag();
        if (applyFirstRunState()) {
          wireDashboardActions();
          loadRecentWorkspaces();
          loadUpcoming();
          loadRecentActivity();
        }
      });
    }
    const startBtn = document.getElementById('homeFirstRunStart');
    if (startBtn) {
      // CTA is a plain anchor — native navigation handles routing. Fire
      // TTfA here so the metric captures the click before the page unloads.
      startBtn.addEventListener('click', () => fireTTFA('first-run-start'));
    }
  }

  // ----- Init -----

  function init() {
    wireChips();
    wireFocusShortcut();
    wireFirstRunButtons();

    const sections = document.getElementById('homeDashboardSections');
    if (!sections) return;

    // First-run with no session flag: dashboard stays hidden; skip fetches.
    if (sections.hasAttribute('hidden') && !applyFirstRunState()) {
      return;
    }

    wireDashboardActions();
    loadRecentWorkspaces();
    loadUpcoming();
    loadRecentActivity();
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();
