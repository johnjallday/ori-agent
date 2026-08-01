// home-dashboard.js — adaptive home page behavior.
//
// Wires the suggested prompt chips (populate-and-focus the Ask Ori input,
// no auto-submit), the global Cmd+J / Ctrl+J shortcut that focuses the
// hero input, and the Today rail's Upcoming Scheduled Tasks and Recent
// Activity sections from their respective API endpoints. Also instruments
// time-to-first-action (TTfA) for Home — the primary success metric from
// the PRD.
//
// The Recent Workspaces card strip and its stat readout used to live here.
// They were retired with the Operations Board: the Map is now Home's single
// workspace overview, and home-workspace-cockpit.js owns the one shared
// /api/workspaces fetch that Map, Tree, Summary, and the context rail all
// read from (PRD FR22, FR111). This module must not fetch workspaces again.
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
  const TTFA_START =
    typeof performance !== 'undefined' && performance.now ? performance.now() : Date.now();
  let ttfaFired = false;

  function fireTTFA(source) {
    if (ttfaFired) return;
    ttfaFired = true;
    const now =
      typeof performance !== 'undefined' && performance.now ? performance.now() : Date.now();
    const ms = Math.round(now - TTFA_START);
    // Structured console marker — easy to grep, easy to swap for a real
    // analytics beacon (navigator.sendBeacon) once an endpoint exists. The PRD
    // is explicit that this feature adds NO network telemetry endpoint (FR141).
    try {
      console.info('[home.ttfa]', { source, ms });
    } catch (_) {
      /* ignore */
    }
    // Test-visible event, so a browser test can assert the marker without
    // scraping console output (FR141).
    try {
      window.dispatchEvent(new CustomEvent('ori:home-ttfa-fired', { detail: { source, ms } }));
    } catch (_) {
      /* ignore */
    }
  }

  // The cockpit reports its own distinct action sources (Map select, Tree
  // select, Open Workspace, view toggle, Quick Capture, Create Workspace)
  // through one event so every Home surface shares a single TTfA contract.
  function wireCockpitTTFA() {
    window.addEventListener('ori:home-ttfa', e => {
      const source = (e && e.detail && e.detail.source) || 'cockpit';
      fireTTFA(source);
    });
  }

  // ----- HTML utilities -----

  function escapeHtml(s) {
    return String(s ?? '').replace(
      /[&<>"']/g,
      c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c]
    );
  }

  function relTime(ts) {
    const fn = typeof window !== 'undefined' && window.RelativeTime?.formatRelativeTime;
    if (!fn) return ''; // utility not yet loaded; skip rather than crash
    return fn(ts);
  }

  // ----- Chips + focus shortcut (carried over from earlier phase) -----

  function wireChips() {
    const chips = document.querySelectorAll('.home-prompt-chip');
    if (!chips.length) return;
    const input = document.getElementById('homeAssistantInput');
    if (!input) return;
    chips.forEach(chip => {
      chip.addEventListener('click', e => {
        e.preventDefault();
        fireTTFA('chip');
        const prompt = (chip.getAttribute('data-prompt') || chip.textContent || '').trim();
        if (!prompt) return;
        input.value = prompt;
        input.focus();
        try {
          const len = input.value.length;
          input.setSelectionRange(len, len);
        } catch (_) {
          /* ignore */
        }
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
    document.addEventListener('keydown', e => {
      if (!(e.metaKey || e.ctrlKey) || e.altKey || e.shiftKey) return;
      if (e.key !== 'j' && e.key !== 'J') return;

      const input = document.getElementById('homeAssistantInput');
      if (!input) return;

      const target = e.target;
      if (
        target &&
        (target.tagName === 'INPUT' || target.tagName === 'TEXTAREA' || target.isContentEditable)
      ) {
        if (target !== input) return;
      }

      e.preventDefault();
      fireTTFA('cmd-j');
      input.focus();
      try {
        const len = input.value.length;
        input.setSelectionRange(len, len);
      } catch (_) {
        /* ignore */
      }
    });
  }

  // ----- Delegated Today actions (TTfA) -----

  // One delegated click listener on the context rail fires the TTfA marker for
  // any qualifying Today action (an upcoming row, an activity row). One
  // listener instead of N keeps the JS small and survives re-renders.
  function wireTodayActions() {
    const rail = document.getElementById('cockpitRailToday');
    if (!rail) return;
    rail.addEventListener('click', e => {
      const t = e.target;
      if (!t) return;
      const rowLink = t.closest && t.closest('.home-row-link');
      if (!rowLink) return;
      fireTTFA(rowLink.closest('#homeUpcomingTasks') ? 'upcoming-row' : 'activity-row');
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
            No scheduled tasks.
          </div>
        `;
        return;
      }
      body.innerHTML = `<ul class="home-row-list">${rows.map(renderUpcomingRow).join('')}</ul>`;
    } catch (err) {
      console.error('home-dashboard: failed to load upcoming tasks', err);
      body.innerHTML = '<div class="home-section-placeholder">Schedule data unavailable.</div>';
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
        body.innerHTML = `
          <div class="home-section-empty">
            Awaiting first operation.
          </div>
        `;
        return;
      }
      body.innerHTML = `<ul class="home-row-list">${events.map(renderActivityRow).join('')}</ul>`;
    } catch (err) {
      console.error('home-dashboard: failed to load recent activity', err);
      body.innerHTML = '<div class="home-section-placeholder">Activity data unavailable.</div>';
    }
  }

  function activityIcon(kind) {
    switch (kind) {
      case 'note_edited':
        return '✎';
      case 'task_completed':
        return '✓';
      case 'scheduled_task_fired':
        return '⏰';
      case 'scheduled_task_failed':
        return '⚠';
      default:
        return '•';
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

  // ----- Init -----

  function init() {
    // Ask Ori chips and the ⌘J shortcut live above the cockpit and are wired on
    // every Home render.
    wireChips();
    wireFocusShortcut();
    wireCockpitTTFA();

    // Today's sections. Each loads independently, so one failing source leaves
    // the others — and Map, Tree, and Ask Ori — usable (FR85, FR113).
    if (!document.getElementById('cockpitRailToday')) return;
    wireTodayActions();
    loadUpcoming();
    loadRecentActivity();
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', init);
  } else {
    init();
  }
})();
