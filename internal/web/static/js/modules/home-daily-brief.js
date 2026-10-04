// home-daily-brief.js — the Home Daily Brief region (PRD FR53/FR97, task
// 7.2-7.4/7.9). Primary Home orientation surface once a valid Personal HQ is
// designated; entirely hidden (no fetch loop, no hidden brief store) when it
// is not (FR69). Purely additive: no-op on pages without #homeDailyBrief.
//
// Pure rendering/decision helpers are exported (loaded as type="module",
// mirroring personal-hq-onboarding.js) so home-daily-brief.test.js can
// exercise them under plain Node with no DOM/network — `document`/`window`
// are genuinely undefined there, so the DOM-wiring IIFE below simply no-ops.

import { loadOnboardingStatus, onboardingGateDecision } from './onboarding-gate.js';
import { openParcelByRef } from './parcel-open.js';

// parseContent safely decodes a Revision's ContentJSON. Returns {} (never
// throws) on missing/invalid JSON so a corrupt revision degrades to an
// empty brief rather than breaking the page.
export function parseContent(revision) {
  if (!revision || !revision.content_json) return {};
  try {
    const parsed = JSON.parse(revision.content_json);
    return parsed && typeof parsed === 'object' ? parsed : {};
  } catch (_) {
    return {};
  }
}

// hrefForRef builds a real, validated navigation target from a brief item's
// stable source reference — never from the visible title (PRD FR91). Every
// action_type routes to the owning workspace's existing page; mutating
// action types (retry, create_followup) deliberately do not perform the
// mutation from Home (PRD FR95) — they route to where the user can act
// through the workspace's own authorized controls.
export function hrefForRef(ref) {
  if (!ref) return '#';
  // Email threads open in Gmail's web UI by their provider thread ID — a fixed,
  // known-safe destination (no account token, not an arbitrary URL). Email refs
  // are HQ-scoped and may not carry a workspace_id, so they are handled before
  // the workspace-id guard (task 4.9).
  if (ref.entity_type === 'email_thread' && ref.entity_id) {
    return `https://mail.google.com/mail/u/0/#all/${encodeURIComponent(ref.entity_id)}`;
  }
  if (ref.entity_type === 'follow_up') {
    const workspaceId = String(ref.workspace_id || '');
    const workspaceSlug = String(ref.workspace_slug || '');
    const recordId = String(ref.entity_id || '');
    if (
      !workspaceId ||
      !/^[a-z0-9][a-z0-9-]{0,79}$/.test(workspaceSlug) ||
      !/^[A-Za-z0-9][A-Za-z0-9._-]{0,199}$/.test(recordId)
    ) {
      return '#';
    }
    return `/workspaces/${encodeURIComponent(workspaceSlug)}?follow_up=${encodeURIComponent(recordId)}`;
  }
  // A meeting opens in its Calendar Ops workspace's console, on today, with
  // that meeting's drawer (and meeting prep) open — the same link Today uses.
  if (ref.entity_type === 'calendar_event') {
    const workspaceSlug = String(ref.workspace_slug || '');
    const eventId = String(ref.entity_id || '');
    const calendarId = String(ref.calendar_id || '');
    const safeId = value =>
      value.length <= 512 &&
      ![...value].some(character => {
        const code = character.charCodeAt(0);
        return code < 32 || code === 127;
      });
    if (
      !/^[a-z0-9][a-z0-9-]{0,79}$/.test(workspaceSlug) ||
      !eventId ||
      !safeId(eventId) ||
      !safeId(calendarId)
    ) {
      return '#';
    }
    const params = new URLSearchParams({ panel: 'calendar', event: eventId });
    if (calendarId) params.set('calendar', calendarId);
    return `/workspaces/${encodeURIComponent(workspaceSlug)}?${params.toString()}`;
  }
  if (!ref.workspace_slug) return '#';
  const workspaceSlug = encodeURIComponent(ref.workspace_slug);
  if (ref.entity_type === 'task' && ref.entity_id) {
    return `/workspaces/${workspaceSlug}/task/${encodeURIComponent(ref.entity_id)}`;
  }
  return `/workspaces/${workspaceSlug}`;
}

// humanizeReason turns the deterministic machine reason tags for email
// attention into friendly labels. Non-email reasons (and model-written prose
// reasons) pass through unchanged, so existing rendering is untouched.
export function humanizeReason(reason) {
  switch (reason) {
    case 'email_waiting_on_user':
      return 'Waiting on your reply';
    case 'email_unread':
      return 'Unread email';
    case 'calendar_conflict':
      return 'Overlaps another meeting';
    case 'meeting_needs_prep':
      return 'No prep note yet';
    case 'waiting_for_choice':
      return 'Waiting for your choice';
    default: {
      const text = String(reason || '');
      return /^[a-z]+(?:_[a-z]+)+$/.test(text)
        ? text.replaceAll('_', ' ').replace(/^./, char => char.toUpperCase())
        : text;
    }
  }
}

// formatMeetingTime renders a meeting's time range in `timeZone` (the brief's
// own zone; the browser's when absent): "All day", or "9:00 AM – 9:30 AM".
export function formatMeetingTime(item, timeZone) {
  if (item && item.all_day) return 'All day';
  const clock = iso => {
    const d = new Date(iso || '');
    if (Number.isNaN(d.getTime())) return '';
    const options = { hour: 'numeric', minute: '2-digit' };
    try {
      return d.toLocaleTimeString(undefined, timeZone ? { ...options, timeZone } : options);
    } catch (_) {
      return d.toLocaleTimeString(undefined, options);
    }
  };
  const start = clock(item && item.start_time);
  const end = clock(item && item.end_time);
  if (!start) return '';
  return end ? `${start} – ${end}` : start;
}

// meetingPrepText is the prep fact a meeting row shows. Nothing is said about
// prep for a private or all-day meeting that has none.
export function meetingPrepText(item) {
  switch (item && item.prep_status) {
    case 'ready':
      return 'Prep note ready';
    case 'pending':
      return 'Preparing…';
    case 'stale':
      return 'Prep note is out of date';
    case 'failed':
      return 'Prep did not finish — try again';
    default:
      return item && !item.private && !item.all_day ? 'No prep note yet' : '';
  }
}

// localDateInZone returns "YYYY-MM-DD" for `date` (defaults to now) as
// observed in the IANA zone `timezone`, matching the server's LocalDateKey
// convention — used purely to detect whether the displayed revision is for
// today (client-side "is this stale" hint), never to compute anything the
// server relies on.
export function localDateInZone(timezone, date) {
  try {
    return new Intl.DateTimeFormat('en-CA', { timeZone: timezone || 'UTC' }).format(
      date || new Date()
    );
  } catch (_) {
    return new Intl.DateTimeFormat('en-CA', { timeZone: 'UTC' }).format(date || new Date());
  }
}

function formatClock(iso, timezone) {
  if (!iso) return '';
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return '';
  try {
    return d.toLocaleString(undefined, {
      timeZone: timezone || 'UTC',
      hour: 'numeric',
      minute: '2-digit'
    });
  } catch (_) {
    return d.toLocaleString();
  }
}

// formatMeta renders the "generated at / timezone / stale" line required by
// PRD FR85. Never blank when a revision exists. relativeTimeFn is injected
// (rather than reading window.RelativeTime directly) so this stays testable
// under plain Node.
export function formatMeta(revision, config, relativeTimeFn) {
  if (!revision) return '';
  const tz = (config && config.timezone) || 'UTC';
  const rel = typeof relativeTimeFn === 'function' ? relativeTimeFn(revision.generated_at) : '';
  const clock = formatClock(revision.generated_at, tz);
  let text = 'Generated';
  if (rel) text += ` ${rel}`;
  if (clock) text += ` (${clock})`;
  text += ` · ${tz}`;
  const today = localDateInZone(tz);
  if (revision.local_date && revision.local_date !== today) {
    text += ' · showing an earlier day while today’s brief updates';
  }
  return text;
}

// computeBanner decides which (if any) advisory banner sits above the brief
// content: a failed latest attempt (FR87, preserve-last-successful), a
// partial/degraded revision, or none. latestClaim may be null.
export function computeBanner(revision, latestClaim) {
  if (latestClaim && latestClaim.status === 'failed') {
    return {
      kind: 'failed',
      text: revision
        ? 'The latest Daily Brief generation failed. Showing your last successful brief.'
        : 'Your Daily Brief could not be generated.',
      showRetry: true
    };
  }
  if (!revision) return null;
  if (revision.status === 'partial') {
    return {
      kind: 'partial',
      text: 'Some sources were unavailable — this brief may be incomplete.',
      showRetry: false
    };
  }
  const content = parseContent(revision);
  if (content.degraded) {
    return {
      kind: 'degraded',
      text: 'AI synthesis was unavailable — showing a simplified, fact-only brief.',
      showRetry: false
    };
  }
  return null;
}

// isQuietDay is true when every content-bearing section is empty — the
// opening summary already says so; the body should not also render five
// empty section headers (PRD FR83).
export function isQuietDay(content) {
  return (
    !(content.needs_attention && content.needs_attention.length) &&
    !(content.since_last_brief && content.since_last_brief.length) &&
    !(content.todays_plan && content.todays_plan.length) &&
    !(content.resume && content.resume.length) &&
    !(content.suggested_actions && content.suggested_actions.length) &&
    !(content.todays_meetings && content.todays_meetings.length)
  );
}

// meetingsSection renders Today's Meetings: present whenever a calendar was
// read, saying so plainly when the day is empty; absent when there is no
// calendar at all. Titles and locations are untrusted and always escaped; a
// why_prepare line is the model's suggestion and styled as one.
function meetingsSection(content, timeZone) {
  const meetings = Array.isArray(content.todays_meetings) ? content.todays_meetings : [];
  if (!content.calendar_connected && !meetings.length) return '';
  if (!meetings.length) {
    return `
      <section class="home-daily-brief-section home-daily-brief-meetings">
        <h3 class="home-daily-brief-section-title">Today's Meetings</h3>
        <p class="home-daily-brief-quiet">No meetings today.</p>
      </section>`;
  }
  const more = Math.max(0, Number(content.todays_meetings_more) || 0);
  const moreNote = more
    ? `<p class="home-daily-brief-item-fact">${more} more meeting${more === 1 ? '' : 's'} today.</p>`
    : '';
  const section = listSection("Today's Meetings", meetings, item => {
    const title = item.private
      ? 'Private event'
      : String(item.title || '').trim() || 'Untitled event';
    const when = formatMeetingTime(item, timeZone);
    const where = item.private ? '' : String(item.location || '').trim();
    const badges = [
      item.conflict
        ? '<span class="home-daily-brief-badge" data-state="conflict">Overlaps</span>'
        : '',
      item.back_to_back
        ? '<span class="home-daily-brief-badge" data-state="back_to_back">Back-to-back</span>'
        : ''
    ].join('');
    const prep = meetingPrepText(item);
    return `
      <li class="home-daily-brief-item home-daily-brief-meeting">
        <a href="${hrefForRef(item.ref)}" class="home-daily-brief-item-title">${escapeHtml(title)}</a>${badges}
        <span class="home-daily-brief-item-ws">${escapeHtml([when, where].filter(Boolean).join(' · '))}</span>
        ${prep ? `<p class="home-daily-brief-item-fact">${escapeHtml(prep)}</p>` : ''}
        ${item.why_prepare && !item.private ? `<p class="home-daily-brief-item-why is-suggestion">${escapeHtml(item.why_prepare)}</p>` : ''}
      </li>`;
  });
  return moreNote ? section.replace(/<\/section>\s*$/, `${moreNote}</section>`) : section;
}

function escapeHtml(str) {
  return String(str == null ? '' : str).replace(
    /[&<>"']/g,
    c =>
      ({
        '&': '&amp;',
        '<': '&lt;',
        '>': '&gt;',
        '"': '&quot;',
        "'": '&#39;'
      })[c]
  );
}

function listSection(title, items, renderItem) {
  if (!items || !items.length) return '';
  return `
      <section class="home-daily-brief-section">
        <h3 class="home-daily-brief-section-title">${escapeHtml(title)}</h3>
        <ul class="home-daily-brief-list">${items.map(renderItem).join('')}</ul>
      </section>`;
}

// renderContent renders one brief's full body. Facts (Needs Attention,
// Since Last Brief, Resume's LastKnownState) and suggestions (WhySuggested,
// NextStep, Suggested Actions) are visually distinguishable per PRD FR82 —
// suggestion text is rendered inside a ".is-suggestion" span. options.timeZone
// is the brief's own timezone, used for meeting times.
export function renderContent(content, options = {}) {
  const parts = [];
  if (content.opening_summary) {
    parts.push(`<p class="home-daily-brief-opening">${escapeHtml(content.opening_summary)}</p>`);
  }
  if (content.data_gaps && content.data_gaps.length) {
    parts.push(
      `<p class="home-daily-brief-gaps">Data gaps: ${escapeHtml(content.data_gaps.join('; '))}</p>`
    );
  }
  // Today's Meetings leads: it is the part of the day already fixed in time.
  parts.push(meetingsSection(content, options.timeZone));
  if (isQuietDay(content)) {
    parts.push(
      '<p class="home-daily-brief-quiet">Nothing else needs your attention right now.</p>'
    );
    return parts.join('');
  }

  parts.push(
    listSection(
      'Needs Attention',
      content.needs_attention,
      item => `
      <li class="home-daily-brief-item">
        <a href="${hrefForRef(item.ref)}" class="home-daily-brief-item-title">${escapeHtml(item.title)}</a>
        <span class="home-daily-brief-item-ws">${escapeHtml(item.workspace_name)}</span>
        <p class="home-daily-brief-item-fact">${escapeHtml(humanizeReason(item.reason))}</p>
      </li>`
    )
  );

  parts.push(
    listSection(
      'Since Last Brief',
      content.since_last_brief,
      item => `
      <li class="home-daily-brief-item">
        <a href="${hrefForRef(item.ref)}" class="home-daily-brief-item-title">${escapeHtml(item.title)}</a>
        <span class="home-daily-brief-item-ws">${escapeHtml(item.workspace_name)}</span>
        ${item.summary ? `<p class="home-daily-brief-item-fact is-suggestion">${escapeHtml(item.summary)}</p>` : ''}
      </li>`
    )
  );

  parts.push(
    listSection(
      "Today's Plan",
      content.todays_plan,
      item => `
      <li class="home-daily-brief-item">
        <a href="${hrefForRef(item.ref)}" class="home-daily-brief-item-title">${escapeHtml(item.title)}</a>
        <span class="home-daily-brief-item-ws">${escapeHtml(item.workspace_name)}</span>
        <p class="home-daily-brief-item-fact">${escapeHtml(humanizeReason(item.reason))}</p>
        ${item.why_suggested ? `<p class="home-daily-brief-item-why is-suggestion">${escapeHtml(item.why_suggested)}</p>` : ''}
      </li>`
    )
  );

  parts.push(
    listSection(
      'Resume',
      content.resume,
      item => `
      <li class="home-daily-brief-item">
        <a href="${hrefForRef(item.ref)}" class="home-daily-brief-item-title">${escapeHtml(item.title)}</a>
        <span class="home-daily-brief-item-ws">${escapeHtml(item.workspace_name)}</span>
        ${item.last_known_state ? `<p class="home-daily-brief-item-fact">${escapeHtml(item.last_known_state)}</p>` : ''}
        ${item.next_step ? `<p class="home-daily-brief-item-why is-suggestion">${escapeHtml(item.next_step)}</p>` : ''}
      </li>`
    )
  );

  if (content.suggested_actions && content.suggested_actions.length) {
    parts.push(`
        <section class="home-daily-brief-section home-daily-brief-actions-section">
          <h3 class="home-daily-brief-section-title">Suggested Next Actions</h3>
          <div class="home-daily-brief-action-row">
            ${content.suggested_actions
              .map(
                a => `
              <a href="${hrefForRef(a.ref)}" class="modern-btn modern-btn-secondary modern-btn-sm is-suggestion" data-action-type="${escapeHtml(a.action_type)}">${escapeHtml(a.label)}</a>
            `
              )
              .join('')}
          </div>
        </section>`);
  }

  return parts.join('');
}

// needsFreshBrief is true when there is no brief for today in the brief's own
// time zone: none at all, or only an earlier day's.
export function needsFreshBrief(revision, config, now) {
  if (!revision) return true;
  return revision.local_date !== localDateInZone(config ? config.timezone : 'UTC', now);
}

// mountDailyBrief wires one Daily Brief surface into the elements it is given:
// it loads the current brief, asks the server for today's when there is none,
// and owns Refresh and Brief settings. The host supplies the elements, so the
// controller does not care which page or panel it lives in.
//
//   els     — { root, title, meta, body, banner, refreshBtn, settingsBtn,
//               openHQLink }; only root is required.
//   options — hq: { workspaceId } when the host already knows the page is the
//               designated Personal HQ (skips the designation read);
//             gate(): resolves false to leave the surface untouched;
//             metaText(revision, config): the line under the heading;
//             onSeen(revision, hqWorkspaceId): once per brief that is shown
//               while the surface is on screen;
//             onChange({ revision, config, generation }): after every render;
//             onUnavailable(): no valid Personal HQ.
//
// Returns { load, refresh, setOnScreen }, or null without a DOM.
export function mountDailyBrief(els, options = {}) {
  if (typeof document === 'undefined' || !els || !els.root) return null;
  const { title: titleEl, meta: metaEl, body: bodyEl, banner: bannerEl } = els;
  const { openHQLink, refreshBtn, settingsBtn } = els;

  let currentConfig = null;
  let hqWorkspaceId = (options.hq && options.hq.workspaceId) || null;
  let polling = false;

  // A brief counts as seen once it is rendered while its surface is actually
  // on screen, and only once per revision.
  let renderedRevision = null;
  let lastClaim = null;
  let seenRevisionId = '';
  let briefOnScreen = false;
  function markBriefSeen() {
    const id = String((renderedRevision && renderedRevision.id) || '');
    if (!briefOnScreen || !hqWorkspaceId || !id || id === seenRevisionId) return;
    seenRevisionId = id;
    if (typeof options.onSeen === 'function') options.onSeen(renderedRevision, hqWorkspaceId);
  }
  function setOnScreen(onScreen) {
    briefOnScreen = Boolean(onScreen);
    markBriefSeen();
  }

  async function fetchJSON(url, options) {
    const res = await fetch(
      url,
      Object.assign({ headers: { Accept: 'application/json' } }, options || {})
    );
    if (!res.ok) throw new Error(`${url} -> ${res.status}`);
    return res.json();
  }

  function sleep(ms) {
    return new Promise(resolve => setTimeout(resolve, ms));
  }

  function renderBanner(banner) {
    if (!bannerEl) return;
    if (!banner) {
      bannerEl.hidden = true;
      bannerEl.innerHTML = '';
      return;
    }
    bannerEl.hidden = false;
    bannerEl.className = `home-daily-brief-banner is-${banner.kind}`;
    bannerEl.innerHTML =
      `<span>${escapeHtml(banner.text)}</span>` +
      (banner.showRetry
        ? '<button type="button" class="modern-btn modern-btn-sm" data-role="retry">Retry</button>'
        : '');
    if (banner.showRetry) {
      const retryBtn = bannerEl.querySelector('[data-role="retry"]');
      if (retryBtn) retryBtn.addEventListener('click', () => runRefresh());
    }
  }

  function metaText(revision, config) {
    if (typeof options.metaText === 'function') return options.metaText(revision, config);
    const relativeTimeFn =
      window.RelativeTime && typeof window.RelativeTime.formatRelativeTime === 'function'
        ? window.RelativeTime.formatRelativeTime
        : null;
    return formatMeta(revision, config, relativeTimeFn);
  }

  // settled is true once nothing is being generated, so an empty surface can
  // say there is no brief instead of promising one.
  function render(revision, config, latestClaim, settled = false) {
    currentConfig = config;
    renderedRevision = revision || null;
    lastClaim = latestClaim || null;
    const generation = String((latestClaim && latestClaim.status) || '');
    if (typeof options.onChange === 'function') {
      options.onChange({ revision: revision || null, config, generation });
    }
    if (!revision) {
      if (titleEl) titleEl.textContent = 'Today';
      if (metaEl) metaEl.textContent = metaText(null, config);
      renderBanner(computeBanner(null, latestClaim));
      if (bodyEl) {
        const failed = generation === 'failed';
        const text = failed
          ? 'Your Daily Brief could not be generated.'
          : settled
            ? 'No Daily Brief yet.'
            : 'Generating your Daily Brief…';
        bodyEl.innerHTML = `<div class="home-daily-brief-placeholder">${text}</div>`;
      }
      return;
    }
    if (titleEl)
      titleEl.textContent =
        revision.local_date === localDateInZone((config && config.timezone) || 'UTC')
          ? 'Today'
          : revision.local_date;
    if (metaEl) metaEl.textContent = metaText(revision, config);
    renderBanner(computeBanner(revision, latestClaim));
    if (bodyEl)
      bodyEl.innerHTML = renderContent(parseContent(revision), {
        timeZone: (config && config.timezone) || undefined
      });
    markBriefSeen();
  }

  // requested is true right after this surface asked for a generation: the
  // server accepts that request before its claim exists, so the first status
  // read can still describe the previous attempt. Give the claim a moment.
  async function pollUntilSettled(requested = false) {
    if (polling) return;
    polling = true;
    try {
      if (requested) await sleep(400);
      const deadline = Date.now() + 90000;
      let statusResp = null;
      while (Date.now() < deadline) {
        try {
          statusResp = await fetchJSON('/api/personal-hq/brief/status');
        } catch (_) {
          break;
        }
        const st = statusResp && statusResp.status;
        if (st !== 'pending' && st !== 'running') break;
        if (typeof options.onChange === 'function') {
          options.onChange({ revision: renderedRevision, config: currentConfig, generation: st });
        }
        await sleep(1500);
      }
      let revision = null;
      try {
        const cur = await fetchJSON('/api/personal-hq/brief/current');
        revision = cur.revision;
      } catch (_) {
        // keep whatever was already rendered
        return;
      }
      render(revision, currentConfig, statusResp, true);
    } finally {
      polling = false;
    }
  }

  async function runRefresh() {
    if (refreshBtn) refreshBtn.disabled = true;
    renderBanner({ kind: 'loading', text: 'Refreshing your Daily Brief…', showRetry: false });
    try {
      await fetch('/api/personal-hq/brief/refresh', { method: 'POST' });
    } catch (_) {
      // fall through to polling — a transient network failure just means
      // nothing changes; the button re-enables and the user can retry.
    }
    await pollUntilSettled(true);
    if (refreshBtn) refreshBtn.disabled = false;
  }

  function openSettingsModal() {
    if (!hqWorkspaceId) return;
    const modalEl = document.getElementById('homeDailyBriefSettingsModal');
    if (!modalEl) return;
    loadSettingsAndHistory();
    if (window.bootstrap && window.bootstrap.Modal) {
      modalEl.addEventListener(
        'hidden.bs.modal',
        () => {
          // Back to the button that opened it, when that button is still shown.
          if (settingsBtn?.isConnected && settingsBtn.getClientRects().length) settingsBtn.focus();
        },
        { once: true }
      );
      window.bootstrap.Modal.getOrCreateInstance(modalEl).show();
    }
  }

  async function loadSettingsAndHistory() {
    try {
      const cfgResp = await fetchJSON('/api/personal-hq/brief/config');
      const cfg = cfgResp.config;
      const tzInput = document.getElementById('homeDailyBriefTimezone');
      const timeInput = document.getElementById('homeDailyBriefTime');
      const scheduleEnabled = document.getElementById('homeDailyBriefScheduleEnabled');
      const notify = document.getElementById('homeDailyBriefNotify');
      if (tzInput) tzInput.value = cfg.timezone || '';
      if (timeInput) timeInput.value = cfg.schedule_time || '';
      if (scheduleEnabled) scheduleEnabled.checked = !!cfg.schedule_enabled;
      if (notify) notify.checked = !!cfg.notify_on_ready;
      const days = cfg.schedule_days || [];
      document
        .querySelectorAll(
          '#homeDailyBriefSettingsForm .home-daily-brief-days input[type="checkbox"]'
        )
        .forEach(box => {
          box.checked = days.indexOf(box.value) !== -1;
        });
    } catch (_) {
      // settings form just stays at its defaults
    }
    try {
      const historyResp = await fetchJSON('/api/personal-hq/brief/history');
      const list = document.getElementById('homeDailyBriefHistoryList');
      if (list) {
        const history = historyResp.history || [];
        list.innerHTML = history.length
          ? history
              .map(h => `<li>${escapeHtml(h.local_date)} — ${escapeHtml(h.status)}</li>`)
              .join('')
          : '<li class="home-daily-brief-history-empty">No history yet.</li>';
      }
    } catch (_) {
      // history list just stays empty
    }
  }

  // The saved schedule changes what "next brief" means, so the surface reads
  // the config again and repaints the brief it already has.
  async function reloadConfig() {
    try {
      const cfgResp = await fetchJSON('/api/personal-hq/brief/config');
      render(renderedRevision, cfgResp.config, lastClaim, true);
    } catch (_) {
      // the surface keeps the config it had
    }
  }

  function wireSettingsForm() {
    const form = document.getElementById('homeDailyBriefSettingsForm');
    // One form per page, wired once however many times a surface mounts.
    if (!form || form.dataset.briefWired === 'true') return;
    form.dataset.briefWired = 'true';
    form.addEventListener('submit', async evt => {
      evt.preventDefault();
      const days = Array.from(form.querySelectorAll('.home-daily-brief-days input:checked')).map(
        b => b.value
      );
      const body = {
        timezone: document.getElementById('homeDailyBriefTimezone').value.trim(),
        schedule_time: document.getElementById('homeDailyBriefTime').value.trim(),
        schedule_days: days,
        schedule_enabled: document.getElementById('homeDailyBriefScheduleEnabled').checked,
        notify_on_ready: document.getElementById('homeDailyBriefNotify').checked
      };
      const submitBtn = form.querySelector('button[type="submit"]');
      if (submitBtn) submitBtn.disabled = true;
      try {
        await fetch('/api/personal-hq/brief/config', {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(body)
        });
        if (window.Toast && typeof window.Toast.success === 'function') {
          window.Toast.success('Daily Brief settings saved.', 'Saved');
        }
        await reloadConfig();
      } catch (_) {
        if (window.Toast && typeof window.Toast.error === 'function') {
          window.Toast.error('Could not save Daily Brief settings.', 'Save failed');
        }
      } finally {
        if (submitBtn) submitBtn.disabled = false;
      }
    });
  }

  let loading = null;

  // load reads the current brief and, when there is none for today, asks the
  // server to prepare it. A second call while one is running joins it.
  function load() {
    if (!loading) {
      loading = loadOnce().finally(() => {
        loading = null;
      });
    }
    return loading;
  }

  async function loadOnce() {
    if (typeof options.gate === 'function' && !(await options.gate())) return;

    if (!hqWorkspaceId) {
      let status;
      try {
        const statusResp = await fetchJSON('/api/personal-hq/status');
        status = statusResp.status;
      } catch (_) {
        status = null;
      }
      if (!status || !status.valid) {
        if (typeof options.onUnavailable === 'function') options.onUnavailable();
        return;
      }
      hqWorkspaceId = status.workspace_id;
      const hqWorkspaceSlug = String(status.workspace?.folder_slug || '').trim();
      if (openHQLink)
        openHQLink.href = hqWorkspaceSlug
          ? `/workspaces/${encodeURIComponent(hqWorkspaceSlug)}`
          : '#';
    }
    els.root.hidden = false;

    let config = null;
    try {
      const cfgResp = await fetchJSON('/api/personal-hq/brief/config');
      config = cfgResp.config;
    } catch (_) {
      // proceed without config metadata; generation can still succeed with
      // server-side defaults
    }

    let revision = null;
    try {
      const cur = await fetchJSON('/api/personal-hq/brief/current');
      revision = cur.revision;
    } catch (_) {
      // treated the same as "no revision yet" below
    }

    // What the latest attempt came to: a brief that failed stays visible as a
    // failure after a reload, and one already being prepared is waited for
    // rather than requested twice.
    let generation = null;
    try {
      generation = await fetchJSON('/api/personal-hq/brief/status');
    } catch (_) {
      // without it the surface simply shows the brief it has
    }
    const active = Boolean(generation) && ['pending', 'running'].includes(generation.status);
    const fresh = needsFreshBrief(revision, config);
    render(revision, config, generation, !fresh && !active);

    if (active) {
      await pollUntilSettled();
    } else if (fresh) {
      try {
        await fetch('/api/personal-hq/brief/open', { method: 'POST' });
      } catch (_) {
        // pollUntilSettled will simply observe idle/no-op and keep showing
        // whatever was already rendered above
      }
      await pollUntilSettled(true);
    }
  }

  if (refreshBtn) refreshBtn.addEventListener('click', () => runRefresh());
  if (settingsBtn) settingsBtn.addEventListener('click', () => openSettingsModal());
  wireSettingsForm();
  return { load, refresh: runRefresh, setOnScreen };
}

// ---- Home (no-op without #homeDailyBrief; genuinely no-op under plain Node,
// where window/document don't exist at all) ----
(function () {
  if (typeof document === 'undefined') return;
  const section = document.getElementById('homeDailyBrief');
  if (!section) return;

  // Seeing the brief opens the map's Daily Brief parcels (task-run-show FR40).
  // This section is rendered while its drawer is still closed, so "seen" waits
  // until it is actually on screen.
  const brief = mountDailyBrief(
    {
      root: section,
      title: document.getElementById('homeDailyBriefTitle'),
      meta: document.getElementById('homeDailyBriefMeta'),
      body: document.getElementById('homeDailyBriefBody'),
      banner: document.getElementById('homeDailyBriefBanner'),
      openHQLink: document.getElementById('homeDailyBriefOpenHQ'),
      refreshBtn: document.getElementById('homeDailyBriefRefreshBtn'),
      settingsBtn: document.getElementById('homeDailyBriefSettingsBtn')
    },
    {
      gate: async () =>
        onboardingGateDecision(await loadOnboardingStatus()).allowWorkspaceHydration,
      onUnavailable: () => {
        section.hidden = true;
      },
      onSeen: (_revision, hqWorkspaceId) =>
        void openParcelByRef({ kind: 'daily_brief', workspaceId: hqWorkspaceId })
    }
  );
  if (typeof IntersectionObserver === 'function') {
    new IntersectionObserver(entries => {
      brief.setOnScreen(entries.some(entry => entry.isIntersecting));
    }).observe(section);
  }
  void brief.load();
})();
