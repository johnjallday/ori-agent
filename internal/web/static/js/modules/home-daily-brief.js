// home-daily-brief.js — the Home Daily Brief region (PRD FR53/FR97, task
// 7.2-7.4/7.9). Primary Home orientation surface once a valid Personal HQ is
// designated; entirely hidden (no fetch loop, no hidden brief store) when it
// is not (FR69). Purely additive: no-op on pages without #homeDailyBrief.
//
// Pure rendering/decision helpers are exported (loaded as type="module",
// mirroring personal-hq-onboarding.js) so home-daily-brief.test.js can
// exercise them under plain Node with no DOM/network — `document`/`window`
// are genuinely undefined there, so the DOM-wiring IIFE below simply no-ops.

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
  if (!ref.workspace_id) return '#';
  const wsId = encodeURIComponent(ref.workspace_id);
  if (ref.entity_type === 'task' && ref.entity_id) {
    return `/workspaces/${wsId}/task/${encodeURIComponent(ref.entity_id)}`;
  }
  return `/workspaces/${wsId}`;
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
    default:
      return reason || '';
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
    !(content.suggested_actions && content.suggested_actions.length)
  );
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
// suggestion text is rendered inside a ".is-suggestion" span.
export function renderContent(content) {
  const parts = [];
  if (content.opening_summary) {
    parts.push(`<p class="home-daily-brief-opening">${escapeHtml(content.opening_summary)}</p>`);
  }
  if (content.data_gaps && content.data_gaps.length) {
    parts.push(
      `<p class="home-daily-brief-gaps">Data gaps: ${escapeHtml(content.data_gaps.join('; '))}</p>`
    );
  }
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

// ---- DOM wiring (no-op without #homeDailyBrief; genuinely no-op under
// plain Node, where window/document don't exist at all) ----
(function () {
  if (typeof document === 'undefined') return;
  const section = document.getElementById('homeDailyBrief');
  if (!section) return;

  const titleEl = document.getElementById('homeDailyBriefTitle');
  const metaEl = document.getElementById('homeDailyBriefMeta');
  const bodyEl = document.getElementById('homeDailyBriefBody');
  const bannerEl = document.getElementById('homeDailyBriefBanner');
  const openHQLink = document.getElementById('homeDailyBriefOpenHQ');
  const refreshBtn = document.getElementById('homeDailyBriefRefreshBtn');
  const settingsBtn = document.getElementById('homeDailyBriefSettingsBtn');

  let currentConfig = null;
  let hqWorkspaceId = null;
  let polling = false;

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

  function render(revision, config, latestClaim) {
    currentConfig = config;
    if (!revision) {
      if (metaEl) metaEl.textContent = '';
      renderBanner(computeBanner(null, latestClaim));
      if (bodyEl) {
        bodyEl.innerHTML =
          latestClaim && latestClaim.status === 'failed'
            ? '<div class="home-daily-brief-placeholder">Your Daily Brief could not be generated.</div>'
            : '<div class="home-daily-brief-placeholder">Generating your Daily Brief…</div>';
      }
      return;
    }
    if (titleEl)
      titleEl.textContent =
        revision.local_date === localDateInZone((config && config.timezone) || 'UTC')
          ? 'Today'
          : revision.local_date;
    const relativeTimeFn =
      window.RelativeTime && typeof window.RelativeTime.formatRelativeTime === 'function'
        ? window.RelativeTime.formatRelativeTime
        : null;
    if (metaEl) metaEl.textContent = formatMeta(revision, config, relativeTimeFn);
    renderBanner(computeBanner(revision, latestClaim));
    if (bodyEl) bodyEl.innerHTML = renderContent(parseContent(revision));
  }

  async function pollUntilSettled() {
    if (polling) return;
    polling = true;
    try {
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
      render(revision, currentConfig, statusResp);
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
    await pollUntilSettled();
    if (refreshBtn) refreshBtn.disabled = false;
  }

  function openSettingsModal() {
    if (!hqWorkspaceId) return;
    const modalEl = document.getElementById('homeDailyBriefSettingsModal');
    if (!modalEl) return;
    loadSettingsAndHistory();
    if (window.bootstrap && window.bootstrap.Modal) {
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

  function wireSettingsForm() {
    const form = document.getElementById('homeDailyBriefSettingsForm');
    if (!form) return;
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
      } catch (_) {
        if (window.Toast && typeof window.Toast.error === 'function') {
          window.Toast.error('Could not save Daily Brief settings.', 'Save failed');
        }
      } finally {
        if (submitBtn) submitBtn.disabled = false;
      }
    });
  }

  async function bootstrap() {
    let status;
    try {
      const statusResp = await fetchJSON('/api/personal-hq/status');
      status = statusResp.status;
    } catch (_) {
      section.hidden = true;
      return;
    }
    if (!status || !status.valid) {
      section.hidden = true;
      return;
    }
    hqWorkspaceId = status.workspace_id;
    section.hidden = false;
    if (openHQLink) openHQLink.href = `/workspaces/${encodeURIComponent(hqWorkspaceId)}`;

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

    render(revision, config, null);

    const today = localDateInZone(config ? config.timezone : 'UTC');
    const needsFreshBrief = !revision || revision.local_date !== today;
    if (needsFreshBrief) {
      try {
        await fetch('/api/personal-hq/brief/open', { method: 'POST' });
      } catch (_) {
        // pollUntilSettled will simply observe idle/no-op and keep showing
        // whatever was already rendered above
      }
      await pollUntilSettled();
    }
  }

  if (refreshBtn) refreshBtn.addEventListener('click', () => runRefresh());
  if (settingsBtn) settingsBtn.addEventListener('click', () => openSettingsModal());
  wireSettingsForm();
  bootstrap();
})();
