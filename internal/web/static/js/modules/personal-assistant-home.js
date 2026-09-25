import {
  MEET_ASSISTANT_AGENTS_ROUTE,
  MEET_ASSISTANT_QUEST_ROUTE
} from './personal-assistant-hire.js';

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

// needsHireBanner is everything Today says before the assistant is hired (PRD
// FR8): one sentence, and its only link is Mission 01. Today has nothing else
// to offer until there is an assistant to prepare it.
export function needsHireBanner() {
  return {
    linkText: 'Meet your assistant',
    href: MEET_ASSISTANT_QUEST_ROUTE,
    trail: ' to start Today.'
  };
}

function renderNeedsHireBanner(els) {
  const banner = needsHireBanner();
  const link = document.createElement('a');
  link.href = banner.href;
  link.textContent = banner.linkText;
  els.banner.replaceChildren(link, banner.trail);
}

const TODAY_LABELS = Object.freeze({
  waiting_for_choice: 'Waiting for your choice',
  needs_review: 'Needs review',
  needs_attention: 'Needs attention',
  in_progress: 'In progress',
  prep_pending: 'Preparing',
  prep_ready: 'Prep ready',
  back_to_back: 'Back-to-back',
  follow_up: 'Follow-up'
});

export function todayLabel(value) {
  const text = String(value || '').trim();
  if (!text) return '';
  if (TODAY_LABELS[text]) return TODAY_LABELS[text];
  return text.includes('_')
    ? text.replaceAll('_', ' ').replace(/^./, char => char.toUpperCase())
    : text;
}

export function todaySectionItems(section) {
  return (Array.isArray(section?.items) ? section.items : []).slice(0, 10).map(item => ({
    title: String(item?.title || '').replace(/\b[a-z]+(?:_[a-z]+)+\b/g, todayLabel),
    detail: String(item?.detail || '').replace(/\b[a-z]+(?:_[a-z]+)+\b/g, todayLabel),
    attribution: String(item?.attribution || '').trim(),
    route: safeTodayRoute(item?.route) ? String(item.route) : '',
    kind: String(item?.kind || '')
  }));
}

export function todayThreeSectionView(today) {
  const working = todaySectionItems(today?.working_on);
  const needs = todaySectionItems(today?.needs_you);
  const done = todaySectionItems(today?.done);
  const sources = [
    ...new Set(
      (Array.isArray(today?.unavailable_sources) ? today.unavailable_sources : [])
        .map(source => todayLabel(source))
        .filter(Boolean)
    )
  ];
  return {
    working,
    needs,
    done,
    sources,
    footer: sources.length ? `Couldn't read: ${sources.join(', ')}.` : '',
    allClear: !needs.length && !sources.length
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
  const projected = rows.map(item => ({
    kind: String(item?.kind || 'item'),
    title: String(item?.title || '').trim(),
    detail: String(item?.detail || '').trim(),
    attribution: String(item?.attribution || '').trim(),
    route: safeTodayRoute(item?.route) ? String(item.route) : ''
  }));
  if (health === 'partial') {
    return [
      { kind: 'status', title: 'Some sources are unavailable — showing verified items.' },
      ...projected
    ];
  }
  if (!projected.length) return [{ kind: 'status', title: 'Nothing here right now.' }];
  return projected;
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
    const state = todayLabel(run?.lifecycle || 'not_started');
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
    const state = todayLabel(sample.state || 'unavailable');
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

// The short badge each meeting row state renders as. A state the server does
// not send renders no badge.
export const MEETING_BADGES = Object.freeze({
  conflict: 'Overlaps',
  back_to_back: 'Back-to-back',
  prep_ready: 'Prep ready',
  prep_pending: 'Preparing…',
  needs_prep: 'Prepare'
});

function plural(count, word) {
  return `${count} ${word}${count === 1 ? '' : 's'}`;
}

// meetingsSectionView decides whether Today's Meetings appears and what it
// says. The server decides presence (no `meetings` means no section); this
// only turns its states into copy, and every route it renders is re-checked.
export function meetingsSectionView(meetings) {
  const hidden = {
    visible: false,
    heading: '',
    rows: [],
    note: '',
    noteRoute: '',
    noteLinkLabel: ''
  };
  if (!meetings || typeof meetings !== 'object') return hidden;
  const state = String(meetings.state || 'unavailable');
  const health = String(meetings?.health?.status || 'unavailable');
  const reason = String(meetings?.health?.reason || '');
  const count = key => Math.max(0, Number(meetings?.[key]) || 0);
  const route = safeTodayRoute(meetings.route) ? String(meetings.route) : '';
  const setupRoute = safeTodayRoute(meetings.setup_route) ? String(meetings.setup_route) : '';
  const setupLabel = String(meetings.setup_label || '').trim();

  if (state === 'not_connected' || state === 'needs_setup') {
    return {
      visible: true,
      heading: 'Meetings',
      rows: [],
      note:
        state === 'not_connected'
          ? 'Connect a calendar and Today will list your meetings.'
          : 'Your calendar connection needs attention before Today can list your meetings.',
      noteRoute: setupRoute,
      noteLinkLabel: setupRoute ? setupLabel || 'Open calendar setup' : ''
    };
  }

  if (health === 'unavailable') {
    return {
      visible: true,
      heading: 'Meetings',
      rows: [
        {
          kind: 'status',
          title:
            reason === 'timed_out'
              ? 'Your calendar did not answer in time — other Today sections are still current.'
              : 'Your calendar could not be read — other Today sections are still current.'
        }
      ],
      note: '',
      noteRoute: route,
      noteLinkLabel: route ? 'Open calendar' : ''
    };
  }

  const items = Array.isArray(meetings.items) ? meetings.items.slice(0, 12) : [];
  const rows = items.map(item => {
    const rowState = String(item?.state || '');
    return {
      kind: 'meeting',
      title: String(item?.title || '').trim() || 'Untitled event',
      detail: String(item?.detail || '').trim(),
      route: safeTodayRoute(item?.route) ? String(item.route) : '',
      state: rowState,
      badge: Object.hasOwn(MEETING_BADGES, rowState) ? MEETING_BADGES[rowState] : ''
    };
  });
  if (!rows.length) rows.push({ kind: 'status', title: 'Nothing scheduled today.' });
  if (health === 'partial') {
    rows.unshift({
      kind: 'status',
      title: 'Some calendars could not be read — showing the meetings that could.'
    });
  }

  let heading = 'Meetings';
  if (count('conflict_count')) heading += ` · ${plural(count('conflict_count'), 'overlap')}`;
  if (count('back_to_back_count')) heading += ` · ${count('back_to_back_count')} back-to-back`;
  const more = count('more_count');
  return {
    visible: true,
    heading,
    rows,
    note: more ? `${plural(more, 'more meeting')} today.` : '',
    noteRoute: route,
    noteLinkLabel: route ? 'Open calendar' : ''
  };
}

export function safeTodayRoute(value) {
  const route = String(value || '');
  if (!route.startsWith('/') || route.startsWith('//') || route.includes('://')) return false;
  try {
    const rawPath = route.split(/[?#]/, 1)[0];
    const decodedPath = decodeURIComponent(rawPath);
    if (
      decodedPath.includes('\\') ||
      [...decodedPath].some(character => {
        const code = character.charCodeAt(0);
        return code < 32 || code === 127;
      }) ||
      decodedPath.split('/').some(segment => segment === '.' || segment === '..')
    ) {
      return false;
    }
    const parsed = new URL(route, 'http://ori.local');
    return parsed.origin === 'http://ori.local';
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

// Whether a launcher cue asks something of the user. Home's compact resting
// launcher shows 'action' cues and lets 'info' ones rest (they are routine
// progress), so a paused, HQ-less, broken, or degraded assistant is never
// hidden to save space.
const INFO_CUES = new Set(['Loading Today', 'Today ready']);

export function personalAssistantLauncherCueTone(cue) {
  const text = String(cue || '');
  if (!text) return '';
  return INFO_CUES.has(text) ? 'info' : 'action';
}

const state = {
  today: null,
  relationship: null,
  root: null,
  sequence: 0,
  emptyEligible: false
};

function elements() {
  const root = document.getElementById('personalAssistantToday');
  if (!root) return null;
  return {
    root,
    launcherStatus: document.getElementById('personalAssistantLauncherStatus'),
    title: document.getElementById('personalAssistantTodayTitle'),
    meta: document.getElementById('personalAssistantTodayMeta'),
    banner: document.getElementById('personalAssistantTodayBanner'),
    sections: document.getElementById('personalAssistantTodaySections'),
    workingSection: document.getElementById('personalAssistantWorkingOn'),
    workingContent: document.getElementById('personalAssistantWorkingOnContent'),
    workingItems: document.getElementById('personalAssistantWorkingOnItems'),
    needsSection: document.getElementById('personalAssistantNeedsYou'),
    needsCards: document.getElementById('personalAssistantNeedsYouCards'),
    needsQueue: document.getElementById('personalAssistantNeedsYouQueue'),
    needsQueueTitle: document.getElementById('personalAssistantNeedsYouQueueTitle'),
    needsQueueCards: document.getElementById('personalAssistantNeedsYouQueueCards'),
    needsItems: document.getElementById('personalAssistantNeedsYouItems'),
    allClear: document.getElementById('personalAssistantTodayAllClear'),
    doneSection: document.getElementById('personalAssistantDone'),
    doneItems: document.getElementById('personalAssistantDoneItems'),
    footer: document.getElementById('personalAssistantTodayFooter'),
    unavailable: document.getElementById('personalAssistantTodayUnavailable'),
    retry: document.getElementById('personalAssistantTodayRetry'),
    decisions: document.getElementById('personalAssistantTodayDecisions'),
    remembered: document.getElementById('personalAssistantTodayRemembered'),
    interview: document.getElementById('personalAssistantTodayInterview'),
    priorities: document.getElementById('personalAssistantTodayPriorities'),
    followUps: document.getElementById('personalAssistantTodayFollowUps'),
    results: document.getElementById('personalAssistantTodayResults'),
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
    meetingsSection: document.getElementById('personalAssistantTodayMeetingsSection'),
    meetingsTitle: document.getElementById('personalAssistantTodayMeetingsTitle'),
    meetings: document.getElementById('personalAssistantTodayMeetings'),
    meetingsNote: document.getElementById('personalAssistantTodayMeetingsNote'),
    links: {
      personal_hq: document.getElementById('personalAssistantTodayHQ'),
      working_agreement: document.getElementById('personalAssistantTodayAgreement'),
      memory: document.getElementById('personalAssistantTodayMemory'),
      advanced: document.getElementById('personalAssistantTodayAdvanced')
    }
  };
}

function renderCompactRows(list, rows, skipCards = false) {
  if (!list) return;
  list.replaceChildren();
  rows.forEach(row => {
    // Offer cards provide their own single actions. Never render another
    // inert row that looks like a competing action.
    if (skipCards && (row.kind === 'folder_offer' || row.kind === 'hq_setup')) return;
    const li = document.createElement('li');
    if (row.route) {
      const link = document.createElement('a');
      link.href = row.route;
      link.textContent = row.title;
      li.append(link);
    } else li.textContent = row.title;
    if (row.detail) {
      const detail = document.createElement('span');
      detail.textContent = row.detail;
      li.append(detail);
    }
    if (row.attribution) {
      const by = document.createElement('span');
      by.className = 'personal-assistant-today__attribution';
      by.textContent = row.attribution;
      li.append(by);
    }
    if (row.kind === 'hq_setup') {
      const details = document.createElement('details');
      details.className = 'personal-assistant-today__hq-receipt';
      const summary = document.createElement('summary');
      summary.textContent = 'Setup receipt';
      details.append(summary);
      const receipt = window.PersonalAssistantHQCard?.receiptRows?.() || [];
      if (receipt.length) {
        const ul = document.createElement('ul');
        receipt.forEach(entry => {
          const line = document.createElement('li');
          const label = { workspace: 'Workspace', schedule: 'Schedule', directory: 'Directory' }[
            entry.kind
          ];
          if (!label) return;
          line.append(`${label} · `);
          if (entry.route && safeTodayRoute(entry.route)) {
            const link = document.createElement('a');
            link.href = entry.route;
            link.textContent = entry.name;
            line.append(link);
          } else line.append(document.createTextNode(entry.name));
          ul.append(line);
        });
        details.append(ul);
      } else {
        const note = document.createElement('p');
        note.textContent = 'Open My HQ to review its current details.';
        details.append(note);
      }
      li.append(details);
    }
    list.append(li);
  });
}

function renderSpecialistSetup(els, setup) {
  if (!els.setup) return;
  const view = specialistSetupView(setup);
  els.setup.hidden = !view.visible || setup?.health?.status === 'unavailable';
  if (els.setup.hidden) return;
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

function renderMeetings(els, meetings) {
  if (!els.meetingsSection) return;
  const view = meetingsSectionView(meetings);
  els.meetingsSection.hidden = !view.visible;
  if (!view.visible) return;
  if (els.meetingsTitle) els.meetingsTitle.textContent = view.heading;
  if (els.meetings) {
    els.meetings.replaceChildren();
    els.meetings.hidden = !view.rows.length;
    els.meetingsSection.dataset.empty = view.rows.every(row => row.kind === 'status');
    view.rows.forEach(row => {
      const li = document.createElement('li');
      if (row.kind === 'status') li.className = 'personal-assistant-today__empty';
      if (row.route) {
        const link = document.createElement('a');
        link.href = row.route;
        link.textContent = row.title;
        li.appendChild(link);
      } else {
        li.append(row.title);
      }
      if (row.badge) {
        const badge = document.createElement('span');
        badge.className = 'personal-assistant-today__badge';
        badge.dataset.state = row.state;
        badge.textContent = row.badge;
        li.appendChild(badge);
      }
      if (row.detail) {
        const detail = document.createElement('span');
        detail.textContent = row.detail;
        li.appendChild(detail);
      }
      els.meetings.appendChild(li);
    });
  }
  if (!els.meetingsNote) return;
  els.meetingsNote.replaceChildren();
  if (view.note) els.meetingsNote.append(view.note);
  if (view.noteRoute && view.noteLinkLabel) {
    const link = document.createElement('a');
    link.href = view.noteRoute;
    link.textContent = view.noteLinkLabel;
    if (view.note) els.meetingsNote.append(' ');
    els.meetingsNote.append(link);
  }
  els.meetingsNote.hidden = !els.meetingsNote.childNodes.length;
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
  els.launcherStatus.dataset.tone = personalAssistantLauncherCueTone(cue);
}

function syncNeedsQueue(els = elements()) {
  if (!els?.needsQueue) return 0;
  const cards = Array.from(els.needsQueueCards?.children || []).filter(card => !card.hidden).length;
  const count = cards + (els.needsItems?.childElementCount || 0);
  if (els.needsQueue.hidden !== (count === 0)) els.needsQueue.hidden = count === 0;
  const title = `Also needs you (${count})`;
  if (els.needsQueueTitle && els.needsQueueTitle.textContent !== title)
    els.needsQueueTitle.textContent = title;
  return count;
}

function syncAllClear(els = elements()) {
  if (!els?.allClear) return;
  // A folder offer can arrive after the Today read. Do not say "Nothing needs
  // you" over an open chooser, an offer, or an HQ confirmation.
  const visibleCard = [
    document.getElementById('personalAssistantFolderScene'),
    document.getElementById('personalAssistantFolderOffer'),
    document.getElementById('personalAssistantHQCard')
  ].some(card => card && !card.hidden);
  const hidden = !state.emptyEligible || visibleCard || syncNeedsQueue(els) > 0;
  if (els.allClear.hidden !== hidden) els.allClear.hidden = hidden;
}

function renderToday(today) {
  const els = elements();
  if (!els) return;
  state.today = today;
  const view = personalAssistantTodayView(today);
  els.root.hidden = false;
  els.root.dataset.state = view.state;
  els.title.textContent = `Today from ${view.displayName}`;
  if (view.paused) {
    els.meta.textContent = 'Check-ins paused';
  } else if (today?.next_check_in) {
    const date = new Date(today.next_check_in);
    if (Number.isNaN(date.getTime())) {
      els.meta.textContent = 'Next check-in unavailable';
    } else {
      const time = document.createElement('time');
      time.dateTime = today.next_check_in;
      time.textContent = new Intl.DateTimeFormat(undefined, {
        weekday: 'short',
        hour: 'numeric',
        minute: '2-digit'
      }).format(date);
      time.title = date.toLocaleString();
      els.meta.replaceChildren('Next check-in · ', time);
    }
  } else {
    els.meta.textContent = 'No check-in scheduled';
  }

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
    renderNeedsHireBanner(els);
  } else if (view.unavailable) {
    els.banner.textContent = 'Personal assistant status is unavailable. No all-clear is implied.';
  } else if (view.paused) {
    els.banner.textContent = `${view.displayName} is paused proactively. Your records and prior briefs are unchanged.`;
  } else if (view.partial) {
    els.banner.textContent = String(today?.brief?.opening_summary || '');
  } else if (view.modelUnavailable) {
    els.banner.textContent =
      'Conversational answers are paused until a model is configured. Deterministic Today records remain available.';
  } else if (view.state === 'healthy_empty') {
    els.banner.textContent = '';
  } else {
    els.banner.textContent = String(
      today?.brief?.opening_summary || 'Your current Personal HQ records are ready.'
    );
  }

  const sections = todayThreeSectionView(today);
  if (els.interview)
    els.interview.hidden = !['available', 'offered', 'deferred'].includes(today?.interview_status);
  renderMeetings(els, today?.meetings);
  // The unavailable source appears once in the footer, not as an empty
  // calendar or results placeholder.
  if (today?.meetings?.health?.status === 'unavailable' && els.meetingsSection) {
    els.meetingsSection.hidden = true;
  }
  renderSpecialistSetup(els, today?.specialist_setup);
  if (els.setup?.hidden === false && today?.specialist_setup?.lifecycle === 'in_progress') {
    els.workingContent?.append(els.setup);
  } else if (els.setup) els.needsQueueCards?.append(els.setup);
  renderCompactRows(els.workingItems, sections.working);
  renderCompactRows(els.needsItems, sections.needs, true);
  renderCompactRows(els.doneItems, sections.done);
  const queueCount = syncNeedsQueue(els);
  if (els.workingSection)
    els.workingSection.hidden = !(
      sections.working.length ||
      !document.getElementById('homeDailyBrief')?.hidden ||
      !els.meetingsSection?.hidden
    );
  if (els.needsSection)
    els.needsSection.hidden = !(
      sections.needs.length ||
      !document.getElementById('personalAssistantFolder')?.hidden ||
      !document.getElementById('personalAssistantHQCard')?.hidden ||
      queueCount > 0
    );
  if (els.doneSection) els.doneSection.hidden = !sections.done.length;
  state.emptyEligible = sections.allClear && view.active;
  syncAllClear(els);
  if (els.footer) els.footer.hidden = !sections.footer;
  if (els.unavailable) els.unavailable.textContent = sections.footer;
  if (els.sections)
    els.sections.hidden = !(view.active || view.paused || view.partial || view.needsHQ);
  Object.entries(els.links).forEach(([key, link]) => setLink(link, today?.links?.[key]));
  els.banner.hidden = !els.banner.textContent.trim();
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
  els.banner.hidden = false;
  els.root.dataset.state = 'loading';
  // A hired assistant with no HQ yet already has a real, trustworthy name —
  // unlike needsHire, where nothing has been chosen yet.
  const named = view.available || view.needsHQ;
  els.title.textContent = named ? `Today from ${view.name}` : 'Your personal assistant';
  els.meta.textContent = view.available ? 'Loading the latest Today records…' : '';
  els.sections.hidden = !view.available && !view.needsHQ;
  if (els.needsSection && view.needsHQ) els.needsSection.hidden = false;
  if (els.workingSection && view.needsHQ) els.workingSection.hidden = true;
  if (els.doneSection && view.needsHQ) els.doneSection.hidden = true;
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
    // Repair happens where the hire happens: the Agents page opens its
    // reconnect, resume, or blocked view on arrival (PRD FR28). There is
    // nothing to walk through first, so the link goes straight there.
    link.href = MEET_ASSISTANT_AGENTS_ROUTE;
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
    // The confirm card is the default action; the Map walkthrough remains an
    // alternate, but a second Build link here would compete with the card.
    els.banner.replaceChildren();
    els.banner.hidden = true;
    return;
  }
  if (!view.available) {
    // Not hired yet (needs_hire, or a hire in flight).
    renderNeedsHireBanner(els);
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
  // Place existing card controllers inside the new three-section hierarchy
  // without remounting or duplicating any of their event handlers.
  const working = document.getElementById('personalAssistantWorkingOnContent');
  const needs = document.getElementById('personalAssistantNeedsYouCards');
  if (working) {
    const brief = document.getElementById('homeDailyBrief');
    if (brief) working.append(brief);
  }
  if (needs && typeof MutationObserver !== 'undefined') {
    new MutationObserver(() => {
      const els = elements();
      const queueCount = syncNeedsQueue(els);
      if (state.today && els?.needsSection) {
        const visible =
          todayThreeSectionView(state.today).needs.length ||
          !document.getElementById('personalAssistantFolder')?.hidden ||
          !document.getElementById('personalAssistantHQCard')?.hidden ||
          queueCount > 0;
        if (els.needsSection.hidden === !!visible) els.needsSection.hidden = !visible;
      }
      syncAllClear(els);
    }).observe(document.getElementById('personalAssistantNeedsYou'), {
      subtree: true,
      attributes: true,
      childList: true,
      attributeFilter: ['hidden']
    });
  }
  ['personalAssistantHQCard', 'personalAssistantFolder'].forEach(id => {
    const node = document.getElementById(id);
    if (node) needs?.append(node);
  });
  ['personalAssistantSpecialistSetup', 'assistantLedSetup'].forEach(id => {
    const node = document.getElementById(id);
    if (node) document.getElementById('personalAssistantNeedsYouQueueCards')?.append(node);
  });
  document.addEventListener('personal-assistant:hq-receipt', () => {
    if (state.today)
      renderCompactRows(elements()?.doneItems, todayThreeSectionView(state.today).done);
  });
  const more = document.getElementById('personalAssistantTodayMore');
  more?.addEventListener('keydown', event => {
    if (event.key !== 'Escape' || !more.open) return;
    event.preventDefault();
    event.stopPropagation(); // Escape closes this menu, not the assistant drawer.
    more.open = false;
    more.querySelector('summary')?.focus();
  });
  document.addEventListener('click', event => {
    if (more?.open && !more.contains(event.target)) more.open = false;
  });
  document
    .getElementById('personalAssistantTodayRetry')
    ?.addEventListener('click', () => void loadToday());
  document.addEventListener('personal-assistant:status', event => {
    renderRelationship(event.detail?.personalAssistant, event.detail?.view);
  });
  const panelState = window.PersonalAssistantPanel?._state;
  if (panelState?.personalAssistant) {
    renderRelationship(panelState.personalAssistant, panelState.view);
  }
  window.PersonalAssistantToday = { refresh: loadToday };
}

if (typeof document !== 'undefined') {
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', init);
  else init();
}
