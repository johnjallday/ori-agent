// home-calendar-ops-portal.js — the Home Calendar Ops portal (PRD FR50/FR51,
// task 7.3). A second, independent Home orientation region: a single bounded
// read of today's calendar via the shared Calendar Ops workspace resolver
// (FR49), entirely separate from Daily Brief in both data source and
// lifecycle (FR54: Daily Brief generation never gains a live Calendar Ops
// call). Purely additive: no-op on pages without #homeCalendarOpsPortal. A
// fetch failure hides the section rather than surfacing a broken widget
// (FR51) -- this portal degrading must never block Home rendering.
//
// Pure rendering/decision helpers are exported (loaded as type="module",
// mirroring home-daily-brief.js) so home-calendar-ops-portal.test.js can
// exercise them under plain Node with no DOM/network.

import { loadOnboardingStatus, onboardingGateDecision } from './onboarding-gate.js';

export function escapeHtml(value) {
  return String(value == null ? '' : value)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');
}

const SETUP_STATE_LABEL = {
  connector_missing: 'Calendar Ops needs a connector.',
  auth_required: 'Calendar Ops needs to be reconnected.',
  mapping_required: 'Calendar Ops setup is unfinished.',
  validation_failed: 'Calendar Ops setup needs attention.',
  degraded: 'Calendar Ops connector is temporarily unavailable.'
};

export function setupStateLabel(state) {
  return SETUP_STATE_LABEL[state] || 'Calendar Ops needs attention.';
}

// computePortalView turns the raw /api/calendar-ops/home-portal-summary
// payload into a small, render-ready view model. Kept separate from
// renderBodyHTML so tests can assert on the decision independent of markup.
export function computePortalView(payload) {
  if (!payload || !payload.has_workspace) {
    return { kind: 'no_workspace' };
  }
  const state = String(payload.state || '');
  const workspaceId = String(payload.workspace_id || '');
  const workspaceSlug = String(payload.workspace_slug || '');
  if (state !== 'ready') {
    return { kind: 'needs_setup', state, workspaceId, workspaceSlug };
  }
  return {
    kind: 'ready',
    workspaceId,
    workspaceSlug,
    nextMeeting: payload.next_meeting || null,
    eventCount: Number(payload.event_count || 0),
    conflictCount: Number(payload.conflict_count || 0),
    dataGap: !!payload.data_gap
  };
}

export function nextMeetingTimeLabel(evt) {
  const start = Date.parse(String((evt && evt.start_time) || ''));
  if (!Number.isFinite(start)) return '';
  try {
    return new Date(start).toLocaleTimeString(undefined, { hour: 'numeric', minute: '2-digit' });
  } catch (_) {
    return '';
  }
}

export function renderBodyHTML(view) {
  if (!view) return '';
  if (view.kind === 'no_workspace') {
    return (
      '<div class="home-daily-brief-placeholder">Connect a calendar to see your day at a glance.</div>' +
      '<button type="button" class="modern-btn modern-btn-primary modern-btn-sm" data-role="setup">Set up Calendar Ops</button>'
    );
  }
  if (view.kind === 'needs_setup') {
    return (
      '<div class="home-daily-brief-placeholder">' +
      escapeHtml(setupStateLabel(view.state)) +
      '</div><button type="button" class="modern-btn modern-btn-primary modern-btn-sm" data-role="finish-setup">Finish setup</button>'
    );
  }
  const meeting = view.nextMeeting;
  const meetingHTML =
    meeting && meeting.title
      ? '<div class="home-calendar-ops-next"><strong>' +
        escapeHtml(meeting.title) +
        '</strong>' +
        (nextMeetingTimeLabel(meeting)
          ? '<span>' + escapeHtml(nextMeetingTimeLabel(meeting)) + '</span>'
          : '') +
        '</div>'
      : '<div class="home-calendar-ops-next"><strong>No more meetings today</strong></div>';
  const statsHTML =
    '<div class="home-calendar-ops-stats"><span>' +
    view.eventCount +
    (view.eventCount === 1 ? ' event today' : ' events today') +
    '</span>' +
    (view.conflictCount
      ? '<span class="is-attention">' +
        view.conflictCount +
        (view.conflictCount === 1 ? ' conflict' : ' conflicts') +
        '</span>'
      : '') +
    '</div>';
  const gapHTML = view.dataGap
    ? '<div class="home-daily-brief-banner is-degraded" role="status">Some calendars could not be read.</div>'
    : '';
  return meetingHTML + statsHTML + gapHTML;
}

// ---- DOM wiring (no-op without #homeCalendarOpsPortal; genuinely no-op
// under plain Node, where window/document don't exist at all) ----
(function () {
  if (typeof document === 'undefined') return;
  const section = document.getElementById('homeCalendarOpsPortal');
  if (!section) return;

  const bodyEl = document.getElementById('homeCalendarOpsPortalBody');
  const openLink = document.getElementById('homeCalendarOpsPortalOpen');

  async function fetchJSON(url) {
    const res = await fetch(url, { headers: { Accept: 'application/json' } });
    if (!res.ok) throw new Error(`${url} -> ${res.status}`);
    return res.json();
  }

  function render(payload) {
    const view = computePortalView(payload);
    section.dataset.state = view.kind;
    section.dataset.workspaceId = view.workspaceId || '';
    section.dataset.workspaceSlug = view.workspaceSlug || '';
    if (openLink) {
      if (view.kind === 'ready' && view.workspaceSlug) {
        openLink.href = `/workspaces/${encodeURIComponent(view.workspaceSlug)}?panel=calendar`;
        openLink.hidden = false;
      } else {
        openLink.hidden = true;
      }
    }
    if (bodyEl) bodyEl.innerHTML = renderBodyHTML(view);
  }

  function wireActions() {
    if (!bodyEl) return;
    bodyEl.addEventListener('click', event => {
      const btn = event.target.closest('[data-role]');
      if (!btn) return;
      const role = btn.getAttribute('data-role');
      if (role === 'setup') {
        window.location.href = '/workspaces?create=1&blueprint=calendar-ops';
        return;
      }
      if (role === 'finish-setup') {
        const workspaceSlug = section.dataset.workspaceSlug || '';
        window.location.href = workspaceSlug
          ? `/workspaces/${encodeURIComponent(workspaceSlug)}?panel=calendar`
          : '/workspaces';
      }
    });
  }

  async function bootstrap() {
    const onboardingStatus = await loadOnboardingStatus();
    if (!onboardingGateDecision(onboardingStatus).allowWorkspaceHydration) return;

    let payload;
    try {
      payload = await fetchJSON('/api/calendar-ops/home-portal-summary');
    } catch (_) {
      // FR51: a broken/unreachable portal must never surface as broken UI --
      // it simply stays hidden, same as the rest of Home.
      section.hidden = true;
      return;
    }
    section.hidden = false;
    render(payload);
  }

  wireActions();
  bootstrap();
})();
