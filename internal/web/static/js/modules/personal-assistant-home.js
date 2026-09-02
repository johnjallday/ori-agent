const TODAY_ENDPOINT = '/api/personal-assistant/today';

export function personalAssistantTodayView(today) {
  const state = String(today?.state || 'unavailable');
  return {
    state,
    loading: state === 'loading',
    active: state === 'active' || state === 'model_unavailable' || state === 'healthy_empty',
    paused: state === 'paused',
    partial: state === 'partial',
    needsHire: state === 'needs_hire',
    // A real hire with no home base yet. The name/appearance are already
    // trustworthy at this point — only the HQ-backed sections are missing.
    needsHQ: state === 'needs_hq',
    repair: state === 'repair_needed',
    unavailable: state === 'unavailable',
    modelUnavailable: state === 'model_unavailable',
    displayName: String(today?.display_name || '').trim() || 'your assistant'
  };
}

export function todaySectionRows(section) {
  const health = String(section?.health?.status || 'unavailable');
  const rows = Array.isArray(section?.items) ? section.items.slice(0, 10) : [];
  if (health === 'unavailable') {
    return [
      { kind: 'status', title: 'Source unavailable — other Today sections are still current.' }
    ];
  }
  if (!rows.length) return [{ kind: 'status', title: 'Nothing here right now.' }];
  return rows.map(item => ({
    kind: String(item?.kind || 'item'),
    title: String(item?.title || '').trim(),
    detail: String(item?.detail || '').trim(),
    attribution: String(item?.attribution || '').trim(),
    route: safeTodayRoute(item?.route) ? String(item.route) : ''
  }));
}

// studioSectionView decides whether the studio region appears at all and what
// it says. It only ever describes watching and reporting: the assistant cannot
// hand work to the specialist, and addressing that agent directly in its own
// workspace is the first-class route, never a fallback.
export function studioSectionView(studio) {
  if (!studio) return { visible: false, heading: '', note: '', route: '' };
  const specialist = String(studio.specialist_name || '').trim();
  const domain = String(studio.domain || '').trim();
  const workspace = String(studio.workspace_name || '').trim();
  const route = safeTodayRoute(studio.route) ? String(studio.route) : '';
  const who = specialist || 'your specialist';
  return {
    visible: true,
    heading: workspace ? `From ${workspace}` : domain ? `From your ${domain}` : 'From your studio',
    note: route
      ? `${who} works in this workspace. Open it to ask ${who} directly:`
      : `${who} works in its own workspace.`,
    route,
    linkLabel: workspace ? `Open ${workspace}` : 'Open the workspace',
    section: { health: studio.health, items: studio.items }
  };
}

export function safeTodayRoute(value) {
  const route = String(value || '');
  if (!route.startsWith('/') || route.startsWith('//') || route.includes('://')) return false;
  try {
    const parsed = new URL(route, 'http://ori.local');
    return parsed.origin === 'http://ori.local' && !parsed.pathname.includes('..');
  } catch (_) {
    return false;
  }
}

const state = { today: null, root: null, sequence: 0 };

function elements() {
  const root = document.getElementById('personalAssistantToday');
  if (!root) return null;
  return {
    root,
    eyebrow: document.getElementById('personalAssistantTodayEyebrow'),
    title: document.getElementById('personalAssistantTodayTitle'),
    meta: document.getElementById('personalAssistantTodayMeta'),
    banner: document.getElementById('personalAssistantTodayBanner'),
    sections: document.getElementById('personalAssistantTodaySections'),
    decisions: document.getElementById('personalAssistantTodayDecisions'),
    priorities: document.getElementById('personalAssistantTodayPriorities'),
    followUps: document.getElementById('personalAssistantTodayFollowUps'),
    results: document.getElementById('personalAssistantTodayResults'),
    studioSection: document.getElementById('personalAssistantTodayStudioSection'),
    studioTitle: document.getElementById('personalAssistantTodayStudioTitle'),
    studio: document.getElementById('personalAssistantTodayStudio'),
    studioNote: document.getElementById('personalAssistantTodayStudioNote'),
    briefMount: document.getElementById('personalAssistantTodayBriefMount'),
    links: {
      personal_hq: document.getElementById('personalAssistantTodayHQ'),
      working_agreement: document.getElementById('personalAssistantTodayAgreement'),
      memory: document.getElementById('personalAssistantTodayMemory'),
      advanced: document.getElementById('personalAssistantTodayAdvanced')
    }
  };
}

function renderRows(list, section) {
  if (!list) return;
  list.replaceChildren();
  todaySectionRows(section).forEach(row => {
    const li = document.createElement('li');
    if (row.kind === 'status') li.className = 'personal-assistant-today__empty';
    if (row.route) {
      const link = document.createElement('a');
      link.href = row.route;
      link.textContent = row.title;
      li.appendChild(link);
    } else {
      li.textContent = row.title;
    }
    if (row.detail) {
      const detail = document.createElement('span');
      detail.textContent = row.detail;
      li.appendChild(detail);
    }
    if (row.attribution) {
      // Who did it, by name. Deliberately text: the shared portrait renderer
      // (OriWorkspaceAgentPortrait) draws a 76x64 card with its own name label,
      // which is right on the Map and the workspace page — where this agent
      // already renders — and wrong in a list row. Shrinking it here would be
      // a second portrait system, which is exactly what must not happen.
      const by = document.createElement('span');
      by.className = 'personal-assistant-today__attribution';
      by.textContent = row.attribution;
      li.appendChild(by);
    }
    list.appendChild(li);
  });
}

function renderStudio(els, studio) {
  if (!els.studioSection) return;
  const view = studioSectionView(studio);
  els.studioSection.hidden = !view.visible;
  if (!view.visible) return;
  if (els.studioTitle) els.studioTitle.textContent = view.heading;
  renderRows(els.studio, view.section);
  if (!els.studioNote) return;
  els.studioNote.replaceChildren();
  if (!view.route) {
    els.studioNote.textContent = view.note;
    return;
  }
  // The link is the direct route to the specialist, offered as a peer of
  // everything else here — not as a workaround for something the assistant
  // cannot do.
  const link = document.createElement('a');
  link.href = view.route;
  link.textContent = view.linkLabel;
  els.studioNote.append(view.note, ' ', link);
}

function setLink(link, route) {
  if (!link) return;
  const safe = safeTodayRoute(route);
  link.hidden = !safe;
  if (safe) link.href = String(route);
}

function moveDailyBrief(els) {
  const brief = document.getElementById('homeDailyBrief');
  if (!brief || !els.briefMount || brief.parentElement === els.briefMount) return;
  els.briefMount.appendChild(brief);
}

function renderToday(today) {
  const els = elements();
  if (!els) return;
  state.today = today;
  const view = personalAssistantTodayView(today);
  els.root.hidden = false;
  els.root.dataset.state = view.state;
  els.eyebrow.textContent = `Today from ${view.displayName}`;
  els.title.textContent = 'Today';
  els.meta.textContent = today?.next_check_in
    ? `Next scheduled check-in: ${new Date(today.next_check_in).toLocaleString()}`
    : view.paused
      ? 'Proactive check-ins are paused.'
      : 'No scheduled check-in is enabled.';

  if (view.repair) {
    els.banner.textContent =
      'Your existing assistant or Personal HQ needs repair before work can continue.';
  } else if (view.needsHQ) {
    els.banner.replaceChildren(
      `${view.displayName} is hired and needs a home base before Today can prepare a brief. `
    );
    const link = document.createElement('a');
    link.href = '/?quest=build-hq';
    link.textContent = 'Build Personal HQ';
    els.banner.append(link);
  } else if (view.needsHire) {
    els.banner.textContent = 'Hire your personal assistant to start Today.';
  } else if (view.unavailable) {
    els.banner.textContent = 'Personal assistant status is unavailable. No all-clear is implied.';
  } else if (view.paused) {
    els.banner.textContent = `${view.displayName} is paused proactively. Your records and prior briefs are unchanged.`;
  } else if (view.partial) {
    els.banner.textContent =
      'Some Today sources are unavailable. Available sections remain visible; this is not an all-clear.';
  } else if (view.modelUnavailable) {
    els.banner.textContent =
      'Conversational answers are paused until a model is configured. Deterministic Today records remain available.';
  } else if (view.state === 'healthy_empty') {
    els.banner.textContent = 'Today is honestly empty based on the sources that were available.';
  } else {
    els.banner.textContent = String(
      today?.brief?.opening_summary || 'Your current Personal HQ records are ready.'
    );
  }

  renderRows(els.decisions, today?.decisions);
  renderRows(els.priorities, today?.priorities);
  renderRows(els.followUps, today?.follow_ups);
  renderRows(els.results, today?.results);
  renderStudio(els, today?.studio);
  Object.entries(els.links).forEach(([key, link]) => setLink(link, today?.links?.[key]));
  if (view.active || view.paused || view.partial) moveDailyBrief(els);
}

function renderRelationship(personalAssistant, view) {
  const els = elements();
  if (!els) return;
  if (!view?.known) {
    els.root.hidden = true;
    return;
  }
  els.root.hidden = false;
  els.root.dataset.state = 'loading';
  // A hired assistant with no HQ yet already has a real, trustworthy name —
  // unlike needsHire, where nothing has been chosen yet.
  const named = view.available || view.needsHQ;
  els.eyebrow.textContent = named ? `Today from ${view.name}` : 'Your personal assistant';
  els.title.textContent = view.available
    ? 'Loading Today…'
    : view.needsHQ
      ? `Build ${view.name}’s Personal HQ`
      : 'Hire your personal assistant';
  els.meta.textContent = '';
  els.sections.hidden = !view.available;
  if (view.repair) {
    els.title.textContent = 'Resume your personal assistant setup';
    els.banner.replaceChildren();
    const link = document.createElement('a');
    link.href = '/?hire=1';
    link.textContent = 'Repair personal assistant';
    els.banner.append('Your existing assistant or Personal HQ needs repair. ', link);
    return;
  }
  if (view.needsHQ) {
    els.banner.replaceChildren();
    const link = document.createElement('a');
    link.href = '/?quest=build-hq';
    link.textContent = 'Build Personal HQ';
    els.banner.append(
      `${view.name} is hired and needs a home base before Today can prepare a brief. `,
      link
    );
    return;
  }
  if (!view.available) {
    els.banner.replaceChildren();
    const link = document.createElement('a');
    link.href = '/?hire=1';
    link.textContent = 'Hire your personal assistant';
    els.banner.append('Choose one named assistant before sending personal work. ', link);
    return;
  }
  els.banner.textContent = 'Loading the latest canonical Today records…';
  void loadToday();
  void personalAssistant;
}

async function loadToday() {
  const seq = ++state.sequence;
  try {
    const response = await fetch(TODAY_ENDPOINT, { headers: { Accept: 'application/json' } });
    if (!response.ok) throw new Error(`today ${response.status}`);
    const payload = await response.json();
    if (seq !== state.sequence) return;
    renderToday(payload?.today || { state: 'unavailable' });
  } catch (_) {
    if (seq !== state.sequence) return;
    const els = elements();
    if (!els) return;
    els.root.hidden = false;
    els.root.dataset.state = 'unavailable';
    els.title.textContent = 'Today is temporarily unavailable';
    els.banner.textContent =
      'The Workspace Map and the rest of Home are still available below. No all-clear is being shown.';
    els.sections.hidden = true;
  }
}

function init() {
  state.root = document.getElementById('personalAssistantToday');
  if (!state.root) return;
  document.addEventListener('personal-assistant:status', event => {
    renderRelationship(event.detail?.personalAssistant, event.detail?.view);
  });
  const panelState = window.PersonalAssistantPanel?._state;
  if (panelState?.personalAssistant) {
    renderRelationship(panelState.personalAssistant, panelState.view);
  }
}

if (typeof document !== 'undefined') {
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', init);
  else init();
}
