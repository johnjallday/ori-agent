// calendar-console.js — the Calendar Ops day/week agenda console: date
// navigation, chronological/all-day event rendering, deterministic conflict
// flagging, free-window suggestions, and the manual create/edit
// preview-then-confirm flow.
//
// It reads GET /api/calendar-ops/capabilities?workspace_id=ID, which reports
// applicable:false for every non-Calendar-Ops workspace so this module stays
// dormant everywhere else (same convention as calendar-ops-setup.js). The
// console lives inside the "Calendar" tab of the workspace configuration
// strip; a compact #calendarConsoleChip in the shared summary strip (which
// workspace-command.js relocates into Command view) is this module's
// "station" entry point, mirroring #reaperReadinessChip.
//
// This module never calls an MCP tool directly. Every read/write goes
// through internal/calendarhttp's CalendarMCPGateway, which owns ownership
// checks, tool-allowlist enforcement, and the confirmation boundary.
(function () {
  'use strict';

  // Captured synchronously at parse time, before workspace-command.js's
  // WorkspaceCommandView bootstrap has a chance to run: it fetches workspace
  // data, then calls sanitizeWorkspaceURLState (workspace-url-state.js),
  // which strips any panel value outside its own VALID_PANELS ('tasks') and
  // rewrites the URL via history.replaceState -- by the time an async
  // handler here would read window.location.search, "?panel=calendar" may
  // already be gone. This module's own script tag executes at classic-defer
  // time (same phase as the module bootstrap), so racing on a later read is
  // not safe; capturing the raw query string as the very first statement in
  // this IIFE is.
  const initialPanelParam = (typeof window !== 'undefined' && window.location && window.location.search)
    ? new URLSearchParams(window.location.search).get('panel')
    : null;

  // ---------------------------------------------------------------------
  // Pure logic (no DOM) -- exported below for direct unit testing.
  // ---------------------------------------------------------------------

  /**
   * getViewRange returns the [start, end) window for a day or week view
   * anchored at anchorDate, using local-time Date arithmetic so DST
   * transitions are handled by the platform's calendar math rather than a
   * fixed millisecond offset (a week spanning a DST change is still 7
   * *calendar* days, not 168 hours).
   */
  function getViewRange(view, anchorDate) {
    const start = new Date(anchorDate.getFullYear(), anchorDate.getMonth(), anchorDate.getDate(), 0, 0, 0, 0);
    if (view === 'week') {
      // Week starts on Sunday, matching Date#getDay()'s 0-indexed convention.
      start.setDate(start.getDate() - start.getDay());
      const end = new Date(start);
      end.setDate(end.getDate() + 7);
      return { start, end };
    }
    const end = new Date(start);
    end.setDate(end.getDate() + 1);
    return { start, end };
  }

  function shiftAnchor(view, anchorDate, direction) {
    const next = new Date(anchorDate);
    const days = view === 'week' ? 7 : 1;
    next.setDate(next.getDate() + days * direction);
    return next;
  }

  /**
   * isBlockingEvent reports whether an event should participate in conflict
   * detection and free-window derivation: a timed (not all-day), not
   * declined, not canceled event. All-day entries and declined/canceled
   * events never "block" time (FR38).
   */
  function isBlockingEvent(evt) {
    if (!evt || evt.all_day || evt.canceled) return false;
    if (evt.response_status === 'declined') return false;
    return !!(evt.start_time && evt.end_time);
  }

  /**
   * computeConflicts deterministically flags overlapping timed events. No
   * LLM, no heuristics: two blocking events conflict iff their [start,end)
   * intervals overlap. Returns a Set of event ids that participate in at
   * least one overlap.
   */
  function computeConflicts(events) {
    const blocking = (events || [])
      .filter(isBlockingEvent)
      .map(evt => ({ id: evt.id, start: Date.parse(evt.start_time), end: Date.parse(evt.end_time) }))
      .filter(e => Number.isFinite(e.start) && Number.isFinite(e.end) && e.end > e.start)
      .sort((a, b) => a.start - b.start);

    const conflicted = new Set();
    for (let i = 0; i < blocking.length; i++) {
      for (let j = i + 1; j < blocking.length; j++) {
        if (blocking[j].start >= blocking[i].end) break; // sorted by start; no later j can overlap i
        conflicted.add(blocking[i].id);
        conflicted.add(blocking[j].id);
      }
    }
    return conflicted;
  }

  // Working-day bounds for the event-derived free-window fallback: suggesting
  // a "free window" at 3am is not useful even though it is technically open,
  // so gaps are only derived within [WORK_START_HOUR, WORK_END_HOUR) of each
  // loaded day rather than the full midnight-to-midnight range.
  const WORK_START_HOUR = 9;
  const WORK_END_HOUR = 18;

  /**
   * workingDayWindows returns one [start, end) local-time window per calendar
   * day covered by the view's loaded range, each clamped to
   * [WORK_START_HOUR, WORK_END_HOUR). Local (viewer) time is used rather than
   * the display timezone -- exact IANA-zone wall-clock construction needs a
   * library this module doesn't have, and this is a best-effort UX fallback,
   * not a safety-relevant computation.
   */
  function workingDayWindows(view, anchorDate) {
    const { start, end } = getViewRange(view, anchorDate);
    const windows = [];
    const cursor = new Date(start);
    while (cursor < end) {
      windows.push({
        start: new Date(cursor.getFullYear(), cursor.getMonth(), cursor.getDate(), WORK_START_HOUR, 0, 0, 0),
        end: new Date(cursor.getFullYear(), cursor.getMonth(), cursor.getDate(), WORK_END_HOUR, 0, 0, 0)
      });
      cursor.setDate(cursor.getDate() + 1);
    }
    return windows;
  }

  /**
   * deriveEventFreeWindows computes bounded gaps between blocking events
   * within [dayStart, dayEnd) purely from already-loaded events -- the
   * fallback path when neither freebusy nor suggest_time is mapped (FR39).
   * Callers must label the result "event-derived" and never imply
   * provider-confirmed availability.
   */
  function deriveEventFreeWindows(events, dayStart, dayEnd) {
    const busy = (events || [])
      .filter(isBlockingEvent)
      .map(evt => ({ start: Math.max(Date.parse(evt.start_time), dayStart.getTime()), end: Math.min(Date.parse(evt.end_time), dayEnd.getTime()) }))
      .filter(e => Number.isFinite(e.start) && Number.isFinite(e.end) && e.end > e.start)
      .sort((a, b) => a.start - b.start);

    const merged = [];
    for (const b of busy) {
      const last = merged[merged.length - 1];
      if (last && b.start <= last.end) {
        last.end = Math.max(last.end, b.end);
      } else {
        merged.push({ ...b });
      }
    }

    const windows = [];
    let cursor = dayStart.getTime();
    for (const b of merged) {
      if (b.start > cursor) windows.push({ start_time: new Date(cursor).toISOString(), end_time: new Date(b.start).toISOString() });
      cursor = Math.max(cursor, b.end);
    }
    if (cursor < dayEnd.getTime()) {
      windows.push({ start_time: new Date(cursor).toISOString(), end_time: new Date(dayEnd.getTime()).toISOString() });
    }
    return windows;
  }

  /**
   * isSafeHttpUrl mirrors the gateway's server-side URL validation
   * (calendar.sanitizeURL) as a client-side defense-in-depth check before an
   * href is ever set on an anchor -- a link must already have passed the
   * backend, but this module never trusts a single layer for XSS safety
   * (FR41).
   */
  function isSafeHttpUrl(value) {
    if (typeof value !== 'string' || !value) return false;
    let parsed;
    try {
      parsed = new URL(value);
    } catch (_) {
      return false;
    }
    return parsed.protocol === 'http:' || parsed.protocol === 'https:';
  }

  /**
   * attendeeImpactLabel describes whether saving this mutation may notify
   * attendees, matching FR30's "whether invitations or updates may be sent."
   */
  function attendeeImpactLabel(attendees, operation) {
    if (!attendees || attendees.length === 0) return 'No attendees will be notified.';
    const verb = operation === 'update_event' ? 'may receive an update notification' : 'may receive an invitation';
    return attendees.length + ' attendee' + (attendees.length === 1 ? '' : 's') + ' ' + verb + '.';
  }

  function formatTimeRangeLabel(evt, timeZone) {
    if (evt.all_day) return 'All day';
    const opts = { hour: 'numeric', minute: '2-digit' };
    if (timeZone) opts.timeZone = timeZone;
    try {
      const start = new Date(evt.start_time);
      const end = new Date(evt.end_time);
      const fmt = new Intl.DateTimeFormat(undefined, opts);
      return fmt.format(start) + ' – ' + fmt.format(end);
    } catch (_) {
      return evt.start_time + ' – ' + evt.end_time;
    }
  }

  function dayKey(evt, timeZone) {
    try {
      const opts = { year: 'numeric', month: '2-digit', day: '2-digit' };
      if (timeZone) opts.timeZone = timeZone;
      return new Intl.DateTimeFormat('en-CA', opts).format(new Date(evt.start_time));
    } catch (_) {
      return (evt.start_time || '').slice(0, 10);
    }
  }

  // ---------------------------------------------------------------------
  // DOM plumbing (mirrors calendar-ops-setup.js's conventions).
  // ---------------------------------------------------------------------

  function el(tag, opts = {}, children = []) {
    const node = document.createElement(tag);
    if (opts.className) node.className = opts.className;
    if (opts.text !== undefined) node.textContent = opts.text;
    if (opts.attrs) {
      for (const [k, v] of Object.entries(opts.attrs)) node.setAttribute(k, v);
    }
    if (opts.style) node.style.cssText = opts.style;
    if (opts.onClick) node.addEventListener('click', opts.onClick);
    for (const c of children) if (c) node.appendChild(c);
    return node;
  }

  function button(label, opts = {}) {
    return el('button', {
      className: 'modern-btn ' + (opts.primary ? 'modern-btn-primary' : 'modern-btn-secondary'),
      text: label,
      style: 'font-size:12px;',
      attrs: Object.assign({ type: 'button' }, opts.attrs || {}),
      onClick: opts.onClick
    });
  }

  // safeLink builds an <a> only when href passes isSafeHttpUrl; otherwise
  // returns null so callers simply omit the link rather than ever setting an
  // unsafe href.
  function safeLink(text, href, opts = {}) {
    if (!isSafeHttpUrl(href)) return null;
    return el('a', {
      text,
      attrs: Object.assign({ href, target: '_blank', rel: 'noopener noreferrer' }, opts.attrs || {}),
      className: opts.className,
      style: opts.style
    });
  }

  const els = () => ({
    chip: document.getElementById('calendarConsoleChip'),
    root: document.getElementById('calendarConsoleRoot'),
    status: document.getElementById('calendarConsoleStatus'),
    toolbar: document.getElementById('calendarConsoleToolbar'),
    body: document.getElementById('calendarConsoleBody'),
    drawer: document.getElementById('calendarConsoleDrawer'),
    live: document.getElementById('calendarConsoleLiveRegion')
  });

  let workspaceId = '';
  let busy = false;
  let view = 'day'; // 'day' | 'week'
  let anchorDate = new Date();
  let capabilities = null;
  // Set only when checkApplicability's capabilities fetch fails. The chip
  // stays silent for that failure (see checkApplicability's comment), but an
  // explicit open of the tab -- the chip itself, ?panel=calendar, or the
  // Tools modal's Calendar tab -- still needs a reason to show instead of a
  // permanently blank body; openCalendarTab reads this to decide.
  let lastCapabilitiesError = null;
  let allCalendars = [];
  let selectedCalendarIds = new Set();
  let currentEvents = [];
  let lastRangeStartISO = '';
  let lastRangeEndISO = '';
  let openFormOperation = null; // 'create_event' | 'update_event' | null
  let openFormSeed = null;

  function wsId() {
    return workspaceId || (typeof window !== 'undefined' && window.currentWorkspaceId) || '';
  }

  function announce(message) {
    const { live } = els();
    if (live) live.textContent = message;
  }

  async function apiGet(url) {
    const resp = await fetch(url);
    const data = await resp.json().catch(() => ({}));
    if (!resp.ok) {
      const message = (data && (data.error || data.message)) || 'request failed: ' + resp.status;
      const err = new Error(message);
      err.code = data && data.code;
      throw err;
    }
    return data;
  }

  async function apiPost(url, body) {
    const resp = await fetch(url, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body || {})
    });
    const data = await resp.json().catch(() => ({}));
    if (!resp.ok) {
      const message = (data && (data.error || data.message)) || 'request failed: ' + resp.status;
      const err = new Error(message);
      err.code = data && data.code;
      throw err;
    }
    return data;
  }

  function setBusy(on) {
    busy = on;
    const { toolbar, body } = els();
    [toolbar, body].forEach(host => {
      if (host) host.querySelectorAll('button, input, select, textarea').forEach(n => (n.disabled = on));
    });
  }

  // --- top-level applicability / chip -----------------------------------

  async function checkApplicability() {
    const id = wsId();
    if (!id) return;
    try {
      capabilities = await apiGet('/api/calendar-ops/capabilities?workspace_id=' + encodeURIComponent(id));
      lastCapabilitiesError = null;
      showChip(true);
      await loadCalendarsAndRender();
    } catch (err) {
      capabilities = null;
      lastCapabilitiesError = err;
      // A workspace with no calendar binding at all (connector_missing on a
      // non-Calendar-Ops workspace) is the overwhelmingly common case and
      // must stay silent; only surface a degraded/auth-required state when
      // the console is actually open (openCalendarTab renders it from
      // lastCapabilitiesError instead).
      showChip(false);
    }
  }

  function showChip(applicable) {
    const { chip } = els();
    if (!chip) return;
    chip.hidden = !applicable;
    if (applicable) {
      chip.textContent = 'Calendar';
      chip.setAttribute('aria-label', 'Open Calendar console');
    }
  }

  function openCalendarTab() {
    // The workspace page is Command view (see workspace-command.js); the
    // classic #workspace-detail-config-content tab strip this module's pane
    // lives in only ever becomes visible when relocated into Command view's
    // "Tools" stat-box modal (mountSharedSurface('config', ...) -- the same
    // mechanism calendar-ops-setup.js's card already relies on). Reaching it
    // therefore means opening that modal and selecting the "calendar" tab
    // registered in workspace-command.js's toolsTabs()/configTabIdFor(),
    // not clicking the (potentially still-hidden) raw tab/toggle elements
    // directly -- that only activates Bootstrap's tab classes without ever
    // making the surrounding chrome visible.
    const cmd = typeof window !== 'undefined' && window.workspaceCommand;
    if (cmd && typeof cmd.openStatModal === 'function' && typeof cmd.setToolsTab === 'function') {
      cmd.openStatModal('tools');
      cmd.setToolsTab('calendar');
      renderOpenState();
      return;
    }
    // Fallback for a page layout without Command view (e.g. a future/older
    // build): fall back to the classic toggle+tab click.
    document.getElementById('workspace-detail-config-toggle')?.click?.();
    document.getElementById('workspace-detail-config-calendar-tab')?.click?.();
    const { root } = els();
    root?.scrollIntoView?.({ behavior: 'smooth', block: 'start' });
    root?.focus?.();
    renderOpenState();
  }

  // renderOpenState fills the pane the moment it's actually opened. Neither
  // branch of checkApplicability's fetch renders into the DOM on its own:
  // the success path already did (loadCalendarsAndRender), but the failure
  // path deliberately only silences the chip (see its comment) and would
  // otherwise leave calendarConsoleBody permanently blank for any workspace
  // whose setup isn't finished yet -- exactly the state a "finish setup"
  // link (the Home/HQ portal, Daily Brief) lands a user on.
  function renderOpenState() {
    if (capabilities) {
      renderReady();
    } else if (lastCapabilitiesError) {
      renderErrorState(lastCapabilitiesError);
    }
  }

  // --- data loading -------------------------------------------------------

  async function loadCalendarsAndRender() {
    const id = wsId();
    if (!id) return;
    try {
      const resp = await apiGet('/api/calendar-ops/calendars?workspace_id=' + encodeURIComponent(id));
      allCalendars = resp.calendars || [];
      const preselected = resp.selected_calendar_ids || [];
      if (selectedCalendarIds.size === 0 && preselected.length) {
        selectedCalendarIds = new Set(preselected);
      }
      await loadEventsAndRender();
    } catch (err) {
      renderErrorState(err);
    }
  }

  async function loadEventsAndRender() {
    const id = wsId();
    if (!id || busy) return;
    setBusy(true);
    renderLoadingState();
    try {
      const { start, end } = getViewRange(view, anchorDate);
      const params = new URLSearchParams();
      params.set('workspace_id', id);
      params.set('start', start.toISOString());
      params.set('end', end.toISOString());
      if (capabilities && capabilities.display_time_zone) params.set('time_zone', capabilities.display_time_zone);
      const ids = selectedCalendarIds.size ? Array.from(selectedCalendarIds) : Array.from(new Set(allCalendars.map(c => c.id)));
      ids.forEach(id2 => params.append('calendar_id', id2));

      const resp = await apiGet('/api/calendar-ops/events?' + params.toString());
      currentEvents = resp.events || [];
      lastRangeStartISO = resp.start_time;
      lastRangeEndISO = resp.end_time;
      renderReady();
    } catch (err) {
      renderErrorState(err);
    } finally {
      setBusy(false);
    }
  }

  // --- rendering: states ---------------------------------------------------

  function renderLoadingState() {
    const { status, body } = els();
    if (status) status.textContent = 'Loading…';
    if (body) body.setAttribute('aria-busy', 'true');
  }

  const stateMessages = {
    connector_missing: 'No calendar connector is configured yet. Finish setup in the MCP tab.',
    auth_required: 'The calendar connector needs to be reconnected. Finish setup in the MCP tab.',
    mapping_required: 'Calendar setup is not complete. Finish mapping in the MCP tab.',
    degraded: 'The calendar connector is temporarily unavailable. Try again shortly.'
  };

  function renderErrorState(err) {
    const { status, body } = els();
    if (body) body.removeAttribute('aria-busy');
    const message = (err && err.code && stateMessages[err.code]) || (err && err.message) || 'Something went wrong loading the calendar.';
    if (status) status.textContent = message;
    if (body) {
      body.textContent = '';
      body.appendChild(el('div', { className: 'calendar-console-empty', text: message }));
    }
  }

  function renderReady() {
    const { status, body } = els();
    if (body) body.removeAttribute('aria-busy');
    if (status) {
      const tz = (capabilities && capabilities.display_time_zone) || Intl.DateTimeFormat().resolvedOptions().timeZone;
      status.textContent = tz;
    }
    renderToolbar();
    renderAgenda();
  }

  // --- toolbar: nav, view switch, filters ---------------------------------

  function renderToolbar() {
    const { toolbar } = els();
    if (!toolbar) return;
    toolbar.textContent = '';

    const nav = el('div', { className: 'calendar-console-nav', attrs: { role: 'group', 'aria-label': 'Date navigation' } });
    nav.appendChild(button('Today', { onClick: () => { anchorDate = new Date(); void loadEventsAndRender(); } }));
    nav.appendChild(button('◀', { onClick: () => { anchorDate = shiftAnchor(view, anchorDate, -1); void loadEventsAndRender(); }, attrs: { 'aria-label': 'Previous' } }));
    nav.appendChild(button('▶', { onClick: () => { anchorDate = shiftAnchor(view, anchorDate, 1); void loadEventsAndRender(); }, attrs: { 'aria-label': 'Next' } }));
    nav.appendChild(el('span', { text: formatAnchorLabel(), className: 'calendar-console-nav-label', attrs: { 'aria-live': 'polite' } }));
    toolbar.appendChild(nav);

    const viewSwitch = el('div', { className: 'calendar-console-view-switch', attrs: { role: 'group', 'aria-label': 'Day or week view' } });
    ['day', 'week'].forEach(v => {
      const b = button(v === 'day' ? 'Day' : 'Week', {
        primary: view === v,
        attrs: { 'aria-pressed': String(view === v) },
        onClick: () => { view = v; void loadEventsAndRender(); }
      });
      viewSwitch.appendChild(b);
    });
    toolbar.appendChild(viewSwitch);

    toolbar.appendChild(button('Refresh', { onClick: () => loadEventsAndRender() }));

    if (capabilities && capabilities.can_create) {
      toolbar.appendChild(button('New event', { primary: true, onClick: () => openForm('create_event', null) }));
    }

    if (allCalendars.length) {
      const filters = el('div', { className: 'calendar-console-filters', attrs: { role: 'group', 'aria-label': 'Visible calendars' } });
      allCalendars.forEach(cal => {
        const label = el('label', { className: 'calendar-console-filter' });
        const checkbox = el('input', { attrs: { type: 'checkbox' } });
        checkbox.checked = selectedCalendarIds.size === 0 || selectedCalendarIds.has(cal.id);
        checkbox.addEventListener('change', () => {
          if (checkbox.checked) selectedCalendarIds.add(cal.id);
          else selectedCalendarIds.delete(cal.id);
          void loadEventsAndRender();
        });
        label.appendChild(checkbox);
        label.appendChild(document.createTextNode(' ' + (cal.name || cal.id)));
        filters.appendChild(label);
      });
      toolbar.appendChild(filters);
    }
  }

  function formatAnchorLabel() {
    const { start, end } = getViewRange(view, anchorDate);
    const opts = { month: 'short', day: 'numeric', year: 'numeric' };
    const fmt = new Intl.DateTimeFormat(undefined, opts);
    if (view === 'day') return fmt.format(start);
    const endInclusive = new Date(end.getTime() - 1);
    return fmt.format(start) + ' – ' + fmt.format(endInclusive);
  }

  // --- agenda rendering -----------------------------------------------------

  function calendarById(id) {
    return allCalendars.find(c => c.id === id);
  }

  function renderAgenda() {
    const { body } = els();
    if (!body) return;
    body.textContent = '';

    if (currentEvents.length === 0) {
      body.appendChild(el('div', { className: 'calendar-console-empty', text: 'No events in this range.' }));
      renderFreeWindowsSection(body);
      return;
    }

    const conflicts = computeConflicts(currentEvents);
    const tz = capabilities && capabilities.display_time_zone;
    const byDay = new Map();
    currentEvents.forEach(evt => {
      const key = dayKey(evt, tz);
      if (!byDay.has(key)) byDay.set(key, []);
      byDay.get(key).push(evt);
    });

    Array.from(byDay.keys())
      .sort()
      .forEach(key => {
        const dayEvents = byDay.get(key);
        const allDay = dayEvents.filter(e => e.all_day);
        const timed = dayEvents.filter(e => !e.all_day).sort((a, b) => Date.parse(a.start_time) - Date.parse(b.start_time));

        const daySection = el('section', { className: 'calendar-console-day', attrs: { 'aria-label': key } });
        daySection.appendChild(el('h3', { text: key, className: 'calendar-console-day-heading' }));

        if (allDay.length) {
          const allDayRow = el('div', { className: 'calendar-console-allday-row' });
          allDay.forEach(evt => allDayRow.appendChild(renderEventCard(evt, false)));
          daySection.appendChild(allDayRow);
        }

        const list = el('ol', { className: 'calendar-console-timed-list' });
        timed.forEach(evt => {
          const li = el('li');
          li.appendChild(renderEventCard(evt, conflicts.has(evt.id)));
          list.appendChild(li);
        });
        daySection.appendChild(list);

        body.appendChild(daySection);
      });

    renderFreeWindowsSection(body);
  }

  function renderEventCard(evt, hasConflict) {
    const tz = capabilities && capabilities.display_time_zone;
    const card = el('button', {
      className: 'calendar-console-event' + (hasConflict ? ' calendar-console-event-conflict' : '') + (evt.private ? ' calendar-console-event-private' : ''),
      attrs: { type: 'button' },
      onClick: () => openDetailDrawer(evt)
    });

    if (evt.private) {
      card.appendChild(el('div', { className: 'calendar-console-event-time', text: formatTimeRangeLabel(evt, tz) }));
      card.appendChild(el('div', { className: 'calendar-console-event-title', text: 'Private event' }));
      card.setAttribute('aria-label', 'Private event, ' + formatTimeRangeLabel(evt, tz));
      return card;
    }

    const cal = calendarById(evt.calendar_id);
    const labelParts = [formatTimeRangeLabel(evt, tz), evt.title || 'Untitled event'];
    if (hasConflict) labelParts.push('Conflict');
    if (evt.canceled) labelParts.push('Canceled');
    if (evt.recurring) labelParts.push('Recurring');
    card.setAttribute('aria-label', labelParts.join(', '));

    card.appendChild(el('div', { className: 'calendar-console-event-time', text: formatTimeRangeLabel(evt, tz) }));
    const titleRow = el('div', { className: 'calendar-console-event-title-row' });
    if (cal && cal.color) {
      titleRow.appendChild(el('span', { className: 'calendar-console-color-dot', style: 'background:' + cssColorOrNone(cal.color) + ';' }));
    }
    titleRow.appendChild(el('span', { className: 'calendar-console-event-title', text: evt.title || 'Untitled event' }));
    card.appendChild(titleRow);

    const badges = el('div', { className: 'calendar-console-event-badges' });
    if (hasConflict) badges.appendChild(el('span', { className: 'calendar-console-badge calendar-console-badge-conflict', text: 'Conflict' }));
    if (evt.canceled) badges.appendChild(el('span', { className: 'calendar-console-badge', text: 'Canceled' }));
    if (evt.recurring) badges.appendChild(el('span', { className: 'calendar-console-badge', text: 'Recurring' }));
    if (evt.response_status) badges.appendChild(el('span', { className: 'calendar-console-badge', text: evt.response_status }));
    if (badges.children.length) card.appendChild(badges);

    return card;
  }

  // cssColorOrNone only allows a small, safe set of CSS color syntaxes
  // (#hex or a plain CSS identifier) into a style attribute -- connector
  // color strings are untrusted and must never carry arbitrary CSS.
  function cssColorOrNone(value) {
    if (typeof value !== 'string') return 'transparent';
    if (/^#[0-9a-fA-F]{3,8}$/.test(value) || /^[a-zA-Z]{3,20}$/.test(value)) return value;
    return 'transparent';
  }

  // --- event detail drawer --------------------------------------------------

  function openDetailDrawer(evt) {
    const { drawer } = els();
    if (!drawer) return;
    drawer.textContent = '';
    drawer.hidden = false;

    const closeBtn = button('Close', { onClick: () => closeDetailDrawer() });
    const head = el('div', { className: 'calendar-console-drawer-head' });
    head.appendChild(el('h3', { text: evt.private ? 'Private event' : evt.title || 'Untitled event' }));
    head.appendChild(closeBtn);
    drawer.appendChild(head);
    drawer.setAttribute('role', 'dialog');
    drawer.setAttribute('aria-modal', 'false');
    drawer.setAttribute(
      'aria-label',
      evt.private ? 'Private event details' : (evt.title || 'Untitled event') + ' details'
    );

    const tz = capabilities && capabilities.display_time_zone;
    drawer.appendChild(el('div', { text: formatTimeRangeLabel(evt, tz) }));

    if (!evt.private) {
      if (evt.location) drawer.appendChild(el('div', { text: 'Location: ' + evt.location }));
      if (evt.description) drawer.appendChild(el('div', { text: evt.description, className: 'calendar-console-drawer-description' }));
      if (evt.attendees && evt.attendees.length) {
        const list = el('ul', { className: 'calendar-console-attendee-list' });
        evt.attendees.forEach(a => {
          list.appendChild(el('li', { text: (a.display_name || a.email) + (a.response_status ? ' — ' + a.response_status : '') }));
        });
        drawer.appendChild(list);
      }
      const links = el('div', { className: 'calendar-console-drawer-links' });
      const conf = safeLink('Join conference', evt.conference_link);
      if (conf) links.appendChild(conf);
      const src = safeLink('Open in connector', evt.source_link);
      if (src) links.appendChild(src);
      if (links.children.length) drawer.appendChild(links);

      if (capabilities && capabilities.can_edit && evt.id) {
        drawer.appendChild(button('Edit', { primary: true, onClick: () => openForm('update_event', evt) }));
      }

      if (isPreparableEvent(evt)) {
        const prepSection = el('div', { className: 'calendar-console-prep-section' });
        drawer.appendChild(prepSection);
        void renderPrepSection(prepSection, evt);
      }
    }

    closeBtn.focus();
  }

  /**
   * isPreparableEvent mirrors the backend's validatePreparableEvent gate
   * (task 6.2): only events with a stable id, a title, and a usable
   * [start,end) range get a Prepare-me action. The backend re-validates this
   * independently and never trusts this client-side gate.
   */
  function isPreparableEvent(evt) {
    if (!evt || !evt.id || !evt.title) return false;
    const start = Date.parse(evt.start_time);
    const end = Date.parse(evt.end_time);
    return Number.isFinite(start) && Number.isFinite(end) && end > start;
  }

  const prepPollIntervalMs = 2000;
  const prepPollMaxAttempts = 30; // ~1 minute

  async function renderPrepSection(container, evt) {
    container.textContent = '';
    container.appendChild(el('h4', { text: 'Meeting prep' }));
    const body = el('div', { className: 'calendar-console-prep-body' });
    container.appendChild(body);

    const params = new URLSearchParams({
      workspace_id: wsId(), calendar_id: evt.calendar_id || '', event_id: evt.id,
      title: evt.title || '', start_time: evt.start_time || '', end_time: evt.end_time || '',
      location: evt.location || '', description: evt.description || ''
    });
    let status;
    try {
      status = await apiGet('/api/calendar-ops/events/prep-status?' + params.toString());
    } catch (err) {
      body.appendChild(el('div', { className: 'calendar-console-form-error', text: 'Could not load prep status: ' + err.message }));
      return;
    }
    renderPrepStatusBody(body, evt, status, 0);
  }

  function renderPrepStatusBody(body, evt, status, pollAttempt) {
    body.textContent = '';

    if (!status.linked || status.status === 'failed') {
      if (status.linked && status.status === 'failed') {
        body.appendChild(el('div', { className: 'calendar-console-form-error', text: 'Prep failed: ' + (status.error || 'unknown error') }));
      }
      body.appendChild(
        button(status.linked ? 'Retry prepare' : 'Prepare me', {
          primary: true,
          onClick: () => startPrepare(body, evt)
        })
      );
      return;
    }

    if (status.status === 'pending') {
      body.appendChild(el('div', { text: 'Preparing…', attrs: { 'aria-live': 'polite' } }));
      if (pollAttempt < prepPollMaxAttempts) {
        setTimeout(async () => {
          if (!body.isConnected) return; // drawer was closed/replaced; stop polling
          try {
            const params = new URLSearchParams({
              workspace_id: wsId(), calendar_id: evt.calendar_id || '', event_id: evt.id,
              title: evt.title || '', start_time: evt.start_time || '', end_time: evt.end_time || '',
              location: evt.location || '', description: evt.description || ''
            });
            const next = await apiGet('/api/calendar-ops/events/prep-status?' + params.toString());
            renderPrepStatusBody(body, evt, next, pollAttempt + 1);
          } catch (_) {
            // Transient fetch failure while polling; try again next tick.
            renderPrepStatusBody(body, evt, status, pollAttempt + 1);
          }
        }, prepPollIntervalMs);
      }
      return;
    }

    // Ready.
    const row = el('div', { className: 'calendar-console-prep-ready' });
    row.appendChild(el('a', { text: 'View prep note', attrs: { href: '/notes/' + encodeURIComponent(status.note_id) } }));
    if (status.is_stale) {
      row.appendChild(el('span', { className: 'calendar-console-badge', text: 'may be outdated' }));
    }
    body.appendChild(row);
    body.appendChild(button('Re-prepare', { onClick: () => startPrepare(body, evt) }));
  }

  async function startPrepare(body, evt) {
    body.textContent = '';
    body.appendChild(el('div', { text: 'Starting…' }));
    try {
      await apiPost('/api/calendar-ops/events/prepare', { workspace_id: wsId(), event: evt });
      renderPrepStatusBody(body, evt, { linked: true, status: 'pending' }, 0);
    } catch (err) {
      body.textContent = '';
      body.appendChild(el('div', { className: 'calendar-console-form-error', text: 'Could not start prep: ' + err.message }));
      body.appendChild(button('Prepare me', { primary: true, onClick: () => startPrepare(body, evt) }));
    }
  }

  function closeDetailDrawer() {
    const { drawer } = els();
    if (!drawer) return;
    drawer.hidden = true;
    drawer.textContent = '';
  }

  // --- free windows -----------------------------------------------------

  async function renderFreeWindowsSection(container) {
    const section = el('section', { className: 'calendar-console-free-windows', attrs: { 'aria-label': 'Free windows' } });
    section.appendChild(el('h4', { text: 'Free windows' }));
    const list = el('div', { className: 'calendar-console-free-windows-list' });
    section.appendChild(list);
    container.appendChild(section);

    if (!lastRangeStartISO || !lastRangeEndISO) return;

    if (capabilities && (capabilities.can_freebusy || capabilities.can_suggest_time)) {
      try {
        const params = new URLSearchParams({ workspace_id: wsId(), start: lastRangeStartISO, end: lastRangeEndISO });
        const resp = await apiGet('/api/calendar-ops/free-windows?' + params.toString());
        if (resp.mapped) {
          renderFreeWindowList(list, resp.windows || [], false);
          return;
        }
      } catch (_) {
        // Fall through to the event-derived path below.
      }
    }

    const windows = workingDayWindows(view, anchorDate)
      .flatMap(w => deriveEventFreeWindows(currentEvents, w.start, w.end));
    renderFreeWindowList(list, windows, true);
  }

  function renderFreeWindowList(container, windows, eventDerived) {
    container.textContent = '';
    if (!windows.length) {
      container.appendChild(el('div', { className: 'calendar-console-empty', text: 'No free windows found in this range.' }));
      return;
    }
    const tz = capabilities && capabilities.display_time_zone;
    windows.slice(0, 20).forEach(w => {
      const row = el('div', { className: 'calendar-console-free-window' });
      row.appendChild(el('span', { text: formatTimeRangeLabel({ start_time: w.start_time, end_time: w.end_time }, tz) }));
      if (eventDerived) row.appendChild(el('span', { className: 'calendar-console-badge', text: 'event-derived' }));
      container.appendChild(row);
    });
  }

  // --- create/edit preview → confirm flow ---------------------------------

  function openForm(operation, seedEvent) {
    openFormOperation = operation;
    openFormSeed = seedEvent;
    renderForm();
  }

  function closeForm() {
    openFormOperation = null;
    openFormSeed = null;
    const { drawer } = els();
    if (drawer) closeDetailDrawer();
    renderFormHost(null);
  }

  function formHostElement() {
    return document.getElementById('calendarConsoleFormHost');
  }

  function renderFormHost(node) {
    const host = formHostElement();
    if (!host) return;
    host.textContent = '';
    if (node) host.appendChild(node);
    host.hidden = !node;
  }

  function renderForm() {
    if (!openFormOperation) {
      renderFormHost(null);
      return;
    }
    const isUpdate = openFormOperation === 'update_event';
    const seed = openFormSeed || {};
    const form = el('form', { className: 'calendar-console-form', attrs: { 'aria-label': isUpdate ? 'Edit event' : 'New event' } });

    const titleInput = el('input', { className: 'form-control', attrs: { type: 'text', placeholder: 'Title', required: 'required' } });
    titleInput.value = seed.title || '';
    const calendarSelect = el('select', { className: 'form-select' });
    allCalendars.forEach(cal => {
      const opt = el('option', { text: cal.name || cal.id, attrs: { value: cal.id } });
      if (cal.id === seed.calendar_id) opt.setAttribute('selected', 'selected');
      calendarSelect.appendChild(opt);
    });
    const startInput = el('input', { className: 'form-control', attrs: { type: 'datetime-local' } });
    if (seed.start_time) startInput.value = toDateTimeLocal(seed.start_time);
    const endInput = el('input', { className: 'form-control', attrs: { type: 'datetime-local' } });
    if (seed.end_time) endInput.value = toDateTimeLocal(seed.end_time);
    const tzInput = el('input', { className: 'form-control', attrs: { type: 'text', placeholder: 'Timezone (e.g. America/New_York)' } });
    tzInput.value = seed.time_zone || (capabilities && capabilities.display_time_zone) || '';
    const locationInput = el('input', { className: 'form-control', attrs: { type: 'text', placeholder: 'Location' } });
    locationInput.value = seed.location || '';
    const descriptionInput = el('textarea', { className: 'form-control', attrs: { placeholder: 'Description', rows: '3' } });
    descriptionInput.value = seed.description || '';
    const attendeesInput = el('input', {
      className: 'form-control',
      attrs: { type: 'text', placeholder: 'Attendees (comma-separated emails)' }
    });
    attendeesInput.value = (seed.attendees || []).map(a => a.email).filter(Boolean).join(', ');

    [
      ['Title', titleInput],
      ['Calendar', calendarSelect],
      ['Start', startInput],
      ['End', endInput],
      ['Timezone', tzInput],
      ['Location', locationInput],
      ['Description', descriptionInput],
      ['Attendees', attendeesInput]
    ].forEach(([label, input]) => {
      const row = el('label', { className: 'calendar-console-form-row' });
      row.appendChild(el('span', { text: label }));
      row.appendChild(input);
      form.appendChild(row);
    });

    const errorBox = el('div', { className: 'calendar-console-form-error', attrs: { role: 'alert' } });
    form.appendChild(errorBox);

    const actions = el('div', { className: 'calendar-console-form-actions' });
    actions.appendChild(button('Cancel', { onClick: () => closeForm() }));
    actions.appendChild(
      button('Preview', {
        primary: true,
        onClick: async () => {
          errorBox.textContent = '';
          const payload = {
            workspace_id: wsId(),
            operation: openFormOperation,
            calendar_id: calendarSelect.value,
            event_id: isUpdate ? seed.id : undefined,
            title: titleInput.value,
            start_time: fromDateTimeLocal(startInput.value, tzInput.value),
            end_time: fromDateTimeLocal(endInput.value, tzInput.value),
            time_zone: tzInput.value.trim(),
            location: locationInput.value,
            description: descriptionInput.value,
            attendees: attendeesInput.value
              .split(',')
              .map(s => s.trim())
              .filter(Boolean)
              .map(email => ({ email }))
          };
          setBusy(true);
          try {
            const preview = await apiPost('/api/calendar-ops/mutations/preview', payload);
            renderCheckpoint(preview, payload);
          } catch (err) {
            errorBox.textContent = err.message;
          } finally {
            setBusy(false);
          }
        }
      })
    );
    form.appendChild(actions);

    renderFormHost(form);
    titleInput.focus();
  }

  function toDateTimeLocal(iso) {
    const d = new Date(iso);
    if (Number.isNaN(d.getTime())) return '';
    const pad = n => String(n).padStart(2, '0');
    return d.getFullYear() + '-' + pad(d.getMonth() + 1) + '-' + pad(d.getDate()) + 'T' + pad(d.getHours()) + ':' + pad(d.getMinutes());
  }

  function fromDateTimeLocal(value) {
    if (!value) return '';
    const d = new Date(value);
    if (Number.isNaN(d.getTime())) return '';
    return d.toISOString();
  }

  // renderCheckpoint shows the explicit confirmation screen (FR29/FR30):
  // target calendar, title, start/end/timezone, location, description,
  // attendees, and invitation/update impact. Nothing here has written
  // anything yet -- Preview performed zero MCP calls.
  function renderCheckpoint(preview, originalPayload) {
    const box = el('div', { className: 'calendar-console-checkpoint', attrs: { role: 'group', 'aria-label': 'Confirm calendar change' } });
    box.appendChild(el('h4', { text: 'Confirm this change' }));

    const cal = calendarById(preview.calendar_id);
    const rows = [
      ['Calendar', (cal && cal.name) || preview.calendar_id],
      ['Title', preview.title],
      ['Start', preview.start_time],
      ['End', preview.end_time],
      ['Timezone', preview.time_zone || '(not set)'],
      ['Location', preview.location || '(none)'],
      ['Description', preview.description || '(none)']
    ];
    rows.forEach(([label, value]) => {
      const row = el('div', { className: 'calendar-console-checkpoint-row' });
      row.appendChild(el('strong', { text: label + ': ' }));
      row.appendChild(document.createTextNode(value));
      box.appendChild(row);
    });
    box.appendChild(el('div', { text: attendeeImpactLabel(preview.attendees, preview.operation), className: 'calendar-console-checkpoint-impact' }));

    const errorBox = el('div', { className: 'calendar-console-form-error', attrs: { role: 'alert' } });
    const actions = el('div', { className: 'calendar-console-form-actions' });
    actions.appendChild(button('Cancel', { onClick: () => renderForm() }));
    actions.appendChild(
      button('Confirm', {
        primary: true,
        onClick: async () => {
          errorBox.textContent = '';
          setBusy(true);
          try {
            const confirmPayload = Object.assign({}, originalPayload, { confirmation_id: preview.confirmation_id });
            const result = await apiPost('/api/calendar-ops/mutations/confirm', confirmPayload);
            if (!result.success) {
              errorBox.textContent = 'The connector reported a failure: ' + (result.error || 'unknown error');
              return;
            }
            closeForm();
            announce('Calendar change saved.');
            await loadEventsAndRender();
            if (result.event) openDetailDrawer(result.event);
          } catch (err) {
            errorBox.textContent = err.message;
          } finally {
            setBusy(false);
          }
        }
      })
    );
    box.appendChild(errorBox);
    box.appendChild(actions);

    renderFormHost(box);
    box.querySelector('button')?.focus?.();
  }

  // --- bootstrap ------------------------------------------------------------

  function waitForWorkspaceId(onReady) {
    const started = Date.now();
    const attempt = () => {
      const id = (typeof window !== 'undefined' && window.currentWorkspaceId) || '';
      if (id) {
        onReady(id);
        return;
      }
      if (Date.now() - started > 5000) return;
      setTimeout(attempt, 100);
    };
    attempt();
  }

  function handlePanelQueryParam() {
    if (initialPanelParam === 'calendar') openCalendarTab();
  }

  function init(id) {
    els().chip?.addEventListener?.('click', openCalendarTab);
    if (id) {
      workspaceId = id;
      void checkApplicability().then(handlePanelQueryParam);
      return;
    }
    waitForWorkspaceId(resolvedId => {
      workspaceId = resolvedId;
      void checkApplicability().then(handlePanelQueryParam);
    });
  }

  if (typeof document !== 'undefined') {
    if (document.readyState === 'loading') {
      document.addEventListener('DOMContentLoaded', () => init(), { once: true });
    } else {
      init();
    }
  }

  // setTestState lets tests seed module-scope variables (capabilities,
  // calendars, events, view/anchor) before calling an _internal render
  // function directly, without exposing a public state-mutation API to the
  // page itself.
  function setTestState(patch) {
    if ('view' in patch) view = patch.view;
    if ('anchorDate' in patch) anchorDate = patch.anchorDate;
    if ('capabilities' in patch) capabilities = patch.capabilities;
    if ('allCalendars' in patch) allCalendars = patch.allCalendars;
    if ('currentEvents' in patch) currentEvents = patch.currentEvents;
    if ('lastRangeStartISO' in patch) lastRangeStartISO = patch.lastRangeStartISO;
    if ('lastRangeEndISO' in patch) lastRangeEndISO = patch.lastRangeEndISO;
  }

  window.CalendarConsole = {
    init,
    openCalendarTab,
    _pure: {
      getViewRange,
      shiftAnchor,
      isBlockingEvent,
      computeConflicts,
      deriveEventFreeWindows,
      workingDayWindows,
      isSafeHttpUrl,
      attendeeImpactLabel,
      formatTimeRangeLabel,
      dayKey,
      cssColorOrNone,
      isPreparableEvent
    },
    _internal: {
      renderAgenda,
      renderForm,
      renderCheckpoint,
      renderToolbar,
      renderErrorState,
      openDetailDrawer,
      closeDetailDrawer,
      openForm,
      closeForm,
      els,
      setTestState
    }
  };
})();
