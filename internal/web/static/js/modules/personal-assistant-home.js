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
export function specialistSetupView(setup) {
  if (!setup) return { visible: false, title: '', status: '', runs: [], actions: [] };
  const health = String(setup?.health?.status || 'unavailable');
  const lifecycle = String(setup?.lifecycle || 'not_started');
  const connected = Math.max(0, Number(setup?.connected_project_count) || 0);
  const childCount = Math.max(0, Number(setup?.child_run_count) || 0);
  const unfinished = Math.max(0, Number(setup?.unfinished_child_count) || 0);
  let status = `${connected} connected project${connected === 1 ? '' : 's'}.`;
  if (health === 'unavailable') {
    status = 'Setup status is temporarily unavailable. Existing work is unchanged.';
  } else if (lifecycle === 'ready') {
    status += unfinished
      ? ` The first setup is ready; ${unfinished} later setup ${unfinished === 1 ? 'needs' : 'need'} attention.`
      : ' Setup is ready.';
  } else if (lifecycle === 'needs_attention') {
    status += ' Setup needs attention.';
  } else if (lifecycle === 'in_progress') {
    status += ' Setup is in progress.';
  } else {
    status += ' Setup has not started.';
  }
  const runs = (Array.isArray(setup?.runs) ? setup.runs : []).slice(0, 64).map(run => {
    const kind = String(run?.run_kind || '') === 'child' ? 'Later project' : 'First project';
    const name = String(run?.project_name || '').trim();
    const state = String(run?.lifecycle || 'not_started').replaceAll('_', ' ');
    return `${name || kind} — ${state}`;
  });
  const allowed = new Set([
    'continue_setup',
    'review_setup',
    'connect_another',
    'open_home',
    'open_project',
    'manage_samples',
    'live_setup'
  ]);
  const routeRequired = new Set(['open_home', 'open_project', 'manage_samples', 'live_setup']);
  const actions = (Array.isArray(setup?.actions) ? setup.actions : [])
    .slice(0, 8)
    .map(action => ({
      id: String(action?.id || ''),
      label: String(action?.label || '').trim(),
      route: safeTodayRoute(action?.route) ? String(action.route) : ''
    }))
    .filter(
      action =>
        allowed.has(action.id) &&
        action.label &&
        (!routeRequired.has(action.id) || Boolean(action.route))
    );
  const sample = setup?.sample_library;
  let sampleStatus = '';
  if (sample) {
    const state = String(sample.state || 'unavailable').replaceAll('_', ' ');
    const roots = Math.max(0, Number(sample.active_root_count) || 0);
    const indexed = Math.max(0, Number(sample.indexed_root_count) || 0);
    sampleStatus = `Sample library: ${state}; ${roots} approved folder${roots === 1 ? '' : 's'}, ${indexed} indexed.`;
  }
  return {
    visible: true,
    title: String(setup?.title || '').trim() || 'Specialist setup',
    status,
    runs,
    actions,
    sampleStatus,
    childCount
  };
}

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

export function personalAssistantLauncherCue(personalAssistant, today) {
  const relationshipState = String(personalAssistant?.state || 'unavailable');
  const todayState = String(today?.state || 'loading');
  if (relationshipState === 'paused' || todayState === 'paused') return 'Paused';
  if (relationshipState === 'needs_hq' || relationshipState === 'provisioning_hq') {
    return 'Build HQ';
  }
  if (relationshipState === 'repair_needed') return 'Repair needed';
  if (todayState === 'partial' || todayState === 'unavailable') return 'Sources unavailable';
  if (todayState === 'model_unavailable') return 'Model unavailable';
  if (todayState === 'active' || todayState === 'healthy_empty') return 'Today ready';
  if (relationshipState === 'active') return 'Loading Today';
  return '';
}

// --- The post-hire domain offer ----------------------------------------
//
// Detection runs here, not in the hire wizard: hiring is one decision, and a
// user naming their assistant has not yet been given a reason to care about a
// domain. Once the relationship exists, "I found X on this Mac" is an offer
// made to an assistant that is already theirs.

// specialistOfferView is the whole render decision. It carries no domain
// wording of its own: every user-visible string comes from the server-side
// mapping entry, so a second domain is copy plus a row.
export function specialistOfferView(entry, decision = 'unanswered') {
  const copy = entry?.offer_copy || {};
  const answered = decision === 'accepted' || decision === 'declined';
  return {
    // A declined offer is never shown again — the answer is durable.
    visible: Boolean(entry?.slug) && decision !== 'declined',
    decision: answered ? decision : 'unanswered',
    slug: String(entry?.slug || ''),
    headline: String(copy.headline || ''),
    question: String(copy.question || ''),
    acceptLabel: String(copy.accept_label || 'Yes'),
    declineLabel: String(copy.decline_label || 'No thanks'),
    acceptedNote: String(copy.accepted_note || ''),
    showActions: Boolean(entry?.slug) && decision === 'unanswered'
  };
}

// specialistOfferIsOpen reports whether the relationship is in a state where
// the offer should be made.
//
// Only a fully set-up assistant qualifies. Before a hire there is no working
// agreement to shape and nobody to shape it for; and between the hire and the
// built Personal HQ, Home is already running its guided HQ walkthrough. Two
// calls to action at that moment compete, and the HQ one is the user's actual
// next step — seen side by side in the browser, the domain offer reads as an
// interruption. The relationship is durable, so the offer loses nothing by
// waiting until setup is finished.
export function specialistOfferIsOpen(personalAssistant) {
  const relationshipState = String(personalAssistant?.state || '').trim();
  const settled = ['active', 'paused'].includes(relationshipState);
  const answered = String(personalAssistant?.specialist_offer_state || '').trim();
  return settled && answered === '';
}

const state = {
  today: null,
  relationship: null,
  root: null,
  sequence: 0,
  offer: null,
  offerDecision: 'unanswered'
};

function elements() {
  const root = document.getElementById('personalAssistantToday');
  if (!root) return null;
  return {
    root,
    launcherStatus: document.getElementById('personalAssistantLauncherStatus'),
    eyebrow: document.getElementById('personalAssistantTodayEyebrow'),
    title: document.getElementById('personalAssistantTodayTitle'),
    meta: document.getElementById('personalAssistantTodayMeta'),
    banner: document.getElementById('personalAssistantTodayBanner'),
    sections: document.getElementById('personalAssistantTodaySections'),
    decisions: document.getElementById('personalAssistantTodayDecisions'),
    priorities: document.getElementById('personalAssistantTodayPriorities'),
    followUps: document.getElementById('personalAssistantTodayFollowUps'),
    results: document.getElementById('personalAssistantTodayResults'),
    offer: document.getElementById('personalAssistantSpecialistOffer'),
    offerHeadline: document.getElementById('personalAssistantSpecialistOfferHeadline'),
    offerQuestion: document.getElementById('personalAssistantSpecialistOfferQuestion'),
    offerAccepted: document.getElementById('personalAssistantSpecialistOfferAccepted'),
    offerActions: document.getElementById('personalAssistantSpecialistOfferActions'),
    offerAccept: document.getElementById('personalAssistantSpecialistAcceptBtn'),
    offerDecline: document.getElementById('personalAssistantSpecialistDeclineBtn'),
    offerError: document.getElementById('personalAssistantSpecialistOfferError'),
    offerManual: document.getElementById('personalAssistantSpecialistManual'),
    setup: document.getElementById('personalAssistantSpecialistSetup'),
    setupTitle: document.getElementById('personalAssistantSpecialistSetupTitle'),
    setupStatus: document.getElementById('personalAssistantSpecialistSetupStatus'),
    setupSamples: document.getElementById('personalAssistantSpecialistSetupSamples'),
    setupRuns: document.getElementById('personalAssistantSpecialistSetupRuns'),
    setupActions: document.getElementById('personalAssistantSpecialistSetupActions'),
    studioSection: document.getElementById('personalAssistantTodayStudioSection'),
    studioTitle: document.getElementById('personalAssistantTodayStudioTitle'),
    studio: document.getElementById('personalAssistantTodayStudio'),
    studioNote: document.getElementById('personalAssistantTodayStudioNote'),
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
  const rows = todaySectionRows(section);
  const sectionElement = list.closest?.('section');
  if (sectionElement) sectionElement.dataset.empty = rows.every(row => row.kind === 'status');
  rows.forEach(row => {
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

// detectSpecialist asks the server what it found. Every failure mode — network
// error, a scan that times out at its 30s ceiling, an empty result, no match —
// resolves to no offer, which is simply Home as it is today. Detection failing
// is never an error the user sees.
async function detectSpecialist() {
  try {
    const response = await fetch('/api/onboarding/detect', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Accept: 'application/json' }
    });
    if (!response.ok) return null;
    const payload = await response.json();
    const entry = payload?.specialist;
    return entry && entry.slug ? entry : null;
  } catch (_) {
    return null;
  }
}

async function loadSpecialistCatalog() {
  try {
    const response = await fetch('/api/onboarding/specialists', {
      headers: { Accept: 'application/json' }
    });
    if (!response.ok) return [];
    const payload = await response.json();
    const entries = payload?.specialists;
    return Array.isArray(entries) ? entries.filter(entry => entry && entry.slug) : [];
  } catch (_) {
    return [];
  }
}

function renderSpecialistOffer() {
  const els = elements();
  if (!els?.offer) return;
  const view = specialistOfferView(state.offer, state.offerDecision);
  els.offer.hidden = !view.visible;
  els.offer.dataset.decision = view.decision;
  if (els.offerHeadline) els.offerHeadline.textContent = view.headline;
  if (els.offerQuestion) {
    els.offerQuestion.textContent = view.question;
    els.offerQuestion.hidden = view.decision === 'accepted';
  }
  if (els.offerAccepted) {
    els.offerAccepted.textContent = view.acceptedNote;
    els.offerAccepted.hidden = view.decision !== 'accepted' || !view.acceptedNote;
  }
  if (els.offerActions) els.offerActions.hidden = !view.showActions;
  if (els.offerAccept) els.offerAccept.textContent = view.acceptLabel;
  if (els.offerDecline) els.offerDecline.textContent = view.declineLabel;
  renderSpecialistManual();
}

// renderSpecialistManual offers a domain the scan did not find — a producer
// whose DAW lives on another machine. It is a peer of the offer, not a
// fallback for it.
function renderSpecialistManual() {
  const els = elements();
  if (!els?.offerManual) return;
  const offered = state.offer?.slug ? new Set([state.offer.slug]) : new Set();
  const candidates = (state.catalog || []).filter(
    entry => !offered.has(entry.slug) && String(entry?.offer_copy?.manual_label || '').trim()
  );
  const show = state.offerDecision === 'unanswered' && candidates.length > 0;
  els.offerManual.hidden = !show;
  els.offerManual.replaceChildren();
  if (!show) return;
  candidates.forEach(entry => {
    const button = document.createElement('button');
    button.type = 'button';
    button.className = 'btn btn-sm btn-link p-0';
    button.dataset.specialistManual = entry.slug;
    button.textContent = String(entry.offer_copy.manual_label).trim();
    button.addEventListener('click', () => answerSpecialistOffer('accepted', entry));
    els.offerManual.appendChild(button);
  });
}

function showOfferError(message) {
  const els = elements();
  if (!els?.offerError) return;
  els.offerError.textContent = message || '';
  els.offerError.hidden = !message;
}

// answerSpecialistOffer records the answer on the relationship. Accepting also
// reshapes the working agreement's focus areas server-side; it creates no
// workspace and runs no setup wizard.
async function answerSpecialistOffer(decision, entry = null) {
  const chosen = entry || state.offer;
  if (decision === 'accepted' && !chosen?.slug) return;
  showOfferError('');
  const els = elements();
  if (els?.offerAccept) els.offerAccept.disabled = true;
  if (els?.offerDecline) els.offerDecline.disabled = true;
  try {
    const response = await fetch('/api/personal-assistant/specialist', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
      body: JSON.stringify({
        decision,
        slug: decision === 'accepted' ? chosen.slug : '',
        if_version: Number(state.today?.state_version) || 0
      })
    });
    if (!response.ok) throw new Error(`answer ${response.status}`);
    const payload = await response.json();
    if (decision === 'accepted') state.offer = chosen;
    state.offerDecision = decision;
    renderSpecialistOffer();
    // Accepting changes the focus areas and the capability ordering, so the
    // page's own reads are refreshed rather than left showing stale values.
    if (decision === 'accepted') {
      await loadToday();
      // This response-only flag is true only for the transition from an
      // unanswered active/paused relationship. Ordinary read-back and replay
      // never auto-open setup; a dismissed journey is resumed by an explicit
      // launcher action instead.
      if (payload?.open_setup_journey === true) {
        window.dispatchEvent(new CustomEvent('ori:open-specialist-setup'));
      }
    }
  } catch (_) {
    showOfferError('That could not be saved. Try again.');
  } finally {
    if (els?.offerAccept) els.offerAccept.disabled = false;
    if (els?.offerDecline) els.offerDecline.disabled = false;
  }
}

// maybeOfferSpecialist runs once per page load, and only for a hired
// relationship that has not answered yet. It is never awaited by anything that
// renders Home.
async function maybeOfferSpecialist(personalAssistant) {
  if (state.offerStarted) return;
  if (!specialistOfferIsOpen(personalAssistant)) return;
  state.offerStarted = true;
  const [catalog, detected] = await Promise.all([loadSpecialistCatalog(), detectSpecialist()]);
  state.catalog = catalog;
  if (state.offerDecision !== 'unanswered') return;
  state.offer = detected;
  renderSpecialistOffer();
}

function bindSpecialistOffer() {
  const els = elements();
  els?.offerAccept?.addEventListener('click', () => answerSpecialistOffer('accepted'));
  els?.offerDecline?.addEventListener('click', () => answerSpecialistOffer('declined'));
}

function renderSpecialistSetup(els, setup) {
  if (!els.setup) return;
  const view = specialistSetupView(setup);
  els.setup.hidden = !view.visible;
  if (!view.visible) return;
  if (els.setupTitle) els.setupTitle.textContent = view.title;
  if (els.setupStatus) els.setupStatus.textContent = view.status;
  if (els.setupSamples) {
    els.setupSamples.textContent = view.sampleStatus;
    els.setupSamples.hidden = !view.sampleStatus;
  }
  if (els.setupRuns) {
    els.setupRuns.replaceChildren();
    view.runs.forEach(text => els.setupRuns.appendChild(makeTodayTextItem(text)));
    els.setupRuns.hidden = !view.runs.length;
  }
  if (els.setupActions) {
    els.setupActions.replaceChildren();
    view.actions.forEach(action => {
      if (action.route) {
        const link = document.createElement('a');
        link.href = action.route;
        link.textContent = action.label;
        els.setupActions.appendChild(link);
        return;
      }
      const button = document.createElement('button');
      button.type = 'button';
      button.textContent = action.label;
      button.addEventListener('click', () => {
        window.dispatchEvent(
          new CustomEvent('ori:open-specialist-setup', {
            detail: { intent: action.id === 'connect_another' ? 'connect_another' : 'review' }
          })
        );
      });
      els.setupActions.appendChild(button);
    });
  }
}

function makeTodayTextItem(text) {
  const item = document.createElement('li');
  item.textContent = text;
  return item;
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

function renderLauncherCue(els, today = state.today) {
  if (!els?.launcherStatus) return;
  const cue = personalAssistantLauncherCue(state.relationship, today);
  els.launcherStatus.textContent = cue;
  els.launcherStatus.hidden = !cue;
}

function renderToday(today) {
  const els = elements();
  if (!els) return;
  state.today = today;
  const view = personalAssistantTodayView(today);
  els.root.hidden = false;
  els.root.dataset.state = view.state;
  els.eyebrow.textContent = 'Personal briefing';
  els.title.textContent = `Today from ${view.displayName}`;
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
  renderSpecialistSetup(els, today?.specialist_setup);
  renderStudio(els, today?.studio);
  Object.entries(els.links).forEach(([key, link]) => setLink(link, today?.links?.[key]));
  renderLauncherCue(els, today);
}

function renderRelationship(personalAssistant, view) {
  const els = elements();
  if (!els) return;
  state.relationship = personalAssistant || null;
  state.today = null;
  if (els.setup) els.setup.hidden = true;
  renderLauncherCue(els, null);
  if (!view?.known) {
    els.root.hidden = true;
    return;
  }
  els.root.hidden = false;
  els.root.dataset.state = 'loading';
  // Fire-and-forget: the offer appears when detection answers, and Home is
  // fully usable whether it does or not.
  void maybeOfferSpecialist(personalAssistant);
  // A hired assistant with no HQ yet already has a real, trustworthy name —
  // unlike needsHire, where nothing has been chosen yet.
  const named = view.available || view.needsHQ;
  els.eyebrow.textContent = 'Personal briefing';
  els.title.textContent = named ? `Today from ${view.name}` : 'Your personal assistant';
  els.meta.textContent = view.available ? 'Loading the latest Today records…' : '';
  els.sections.hidden = !view.available;
  if (view.repair) {
    const repairStep = String(personalAssistant?.repair_step || '').trim();
    const recoverable = repairStep === 'relationship_recovery';
    const blocked = repairStep === 'relationship_recovery_blocked';
    els.title.textContent = recoverable
      ? `Reconnect ${view.name}`
      : blocked
        ? 'Assistant records need review'
        : 'Resume your personal assistant setup';
    els.banner.replaceChildren();
    const link = document.createElement('a');
    link.href = '/?hire=1';
    link.textContent = recoverable
      ? 'Review and reconnect'
      : blocked
        ? 'Review repair status'
        : 'Repair personal assistant';
    const message = recoverable
      ? personalAssistant?.hq_workspace_id
        ? 'Ori found the existing assistant and Personal HQ with matching stable IDs. '
        : 'Ori found the existing assistant profile with its durable ownership marker. '
      : blocked
        ? 'Existing Personal Assistant records do not agree, so Ori will not guess or create a duplicate. '
        : 'Your existing assistant or Personal HQ needs repair. ';
    els.banner.append(message, link);
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
    els.eyebrow.textContent = 'Personal briefing';
    els.title.textContent = `Today from ${state.relationship?.display_name || 'your assistant'}`;
    els.banner.textContent =
      'Today is temporarily unavailable. The Workspace Map and the rest of Home remain available; no all-clear is being shown.';
    els.sections.hidden = true;
    renderLauncherCue(els, { state: 'unavailable' });
  }
}

function init() {
  state.root = document.getElementById('personalAssistantToday');
  if (!state.root) return;
  bindSpecialistOffer();
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
