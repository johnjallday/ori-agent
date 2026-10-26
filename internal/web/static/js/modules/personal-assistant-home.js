import {
  MEET_ASSISTANT_AGENTS_ROUTE,
  MEET_ASSISTANT_QUEST_ROUTE
} from './personal-assistant-hire.js';
import { safeTodayRoute } from './personal-assistant-panel.js';
import { prepareTodaysBrief } from './home-daily-brief.js';
import {
  dailyBriefRowStatus,
  dailyBriefStationLink,
  dailyBriefStatus
} from './daily-brief-station.js';
import { loadOnboardingStatus, onboardingGateDecision } from './onboarding-gate.js';
import { isFirstLookReportKind, mountFirstLookReports } from './first-look-report.js';
import { reportRevealKey, takeNeedsReveal } from './personal-assistant-needs-reveal.js';

// The drawer's shared link check lives with the drawer; it is re-exported here
// for the callers and tests that have always read it from Home.
export { safeTodayRoute };

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

export function partialTodaySummary(today) {
  const summary = String(today?.brief?.opening_summary || '').trim();
  return (
    'Some Today sources are unavailable; no all-clear is implied.' + (summary ? ' ' + summary : '')
  );
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

// The controls an unfinished workspace build may offer from Today (FR42).
const WORKSPACE_BUILD_ACTIONS = ['resume', 'discard'];
// The protection line's only in-place control; "Check protection" is its route.
const BACKUP_COVERAGE_ACTIONS = ['not_now'];

export function todaySectionItems(section) {
  const seen = new Set();
  return (Array.isArray(section?.items) ? section.items : [])
    .filter(item => {
      const ref = item?.ref;
      const key =
        ref?.workspace_id && ref?.entity_type && ref?.entity_id
          ? JSON.stringify([ref.workspace_id, ref.entity_type, ref.entity_id])
          : item?.id
            ? JSON.stringify([item.kind, item.id, item.route])
            : '';
      if (key && seen.has(key)) return false;
      if (key) seen.add(key);
      return true;
    })
    .slice(0, 10)
    .map(item => {
      const row = {
        id: String(item?.id || '').trim(),
        ref: item?.ref ? { ...item.ref } : null,
        title: String(item?.title || '').replace(/\b[a-z]+(?:_[a-z]+)+\b/g, todayLabel),
        detail: String(item?.detail || '').replace(/\b[a-z]+(?:_[a-z]+)+\b/g, todayLabel),
        attribution: String(item?.attribution || '').trim(),
        route: safeTodayRoute(item?.route) ? String(item.route) : '',
        kind: String(item?.kind || ''),
        sourceAt: String(item?.source_at || '')
      };
      // Every row retains canonical identity; only builds and the protection
      // line have in-place controls.
      if (row.kind === 'workspace_build') {
        row.id = String(item?.id || '').trim();
        row.actions = (Array.isArray(item?.actions) ? item.actions : []).filter(action =>
          WORKSPACE_BUILD_ACTIONS.includes(action)
        );
      }
      if (row.kind === 'backup_coverage') {
        row.actions = (Array.isArray(item?.actions) ? item.actions : []).filter(action =>
          BACKUP_COVERAGE_ACTIONS.includes(action)
        );
      }
      return row;
    });
}

// The protection line's controls: "Check protection" follows the server's
// route; "Not now" is quiet and only offered when the server lists it.
export function backupCoverageControls(row) {
  const controls = [];
  if (row?.route) {
    controls.push({ kind: 'link', label: 'Check protection', href: row.route, primary: true });
  }
  if (row?.actions?.includes('not_now')) {
    controls.push({ kind: 'button', label: 'Not now', action: 'not_now', primary: false });
  }
  return controls;
}

// "Not now" sends no data: the server records the decline from its own count.
export async function declineBackupCoverage(fetchImpl = fetch) {
  try {
    const response = await fetchImpl('/api/personal-assistant/today/backup-coverage/decline', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
      body: '{}'
    });
    return Boolean(response?.ok);
  } catch (_) {
    return false;
  }
}

// Resume opens the Create Workspace dialog in build mode on the open build.
// A page without the dialog goes Home, which has it.
export function resumeWorkspaceBuild(win = window) {
  const manager = win.sessionManager;
  if (manager?.showAddWorkspaceModal && win.document?.getElementById?.('addFolderModal')) {
    win.PersonalAssistantPanel?.close?.({ restoreFocus: false });
    manager.showAddWorkspaceModal({ entryPoint: 'personal_assistant_ask', buildResume: true });
    return 'opened';
  }
  win.location.href = '/?build=resume';
  return 'navigated';
}

// A blocker is navigation only: read/display its exact Home-owned card, then
// use the shared drawer's reveal seam. No conversation switch or return slot.
export async function revealFolderReview(offerId, valid = () => true, win = window) {
  if (!offerId || !valid()) return false;
  if (!(await win.PersonalAssistantFolder?.showHomeReview?.(offerId, valid)) || !valid())
    return false;
  const offer = win.PersonalAssistantFolder?.current?.();
  const card = win.document?.getElementById?.('personalAssistantFolderOffer');
  if (offer?.id !== offerId || offer.conversation_id || !card || card.hidden) return false;
  if (!win.PersonalAssistantToday?.reveal?.('folder-review', { item: card })) return false;
  card.tabIndex = -1;
  card.focus?.({ preventScroll: true });
  return true;
}

async function discardWorkspaceBuild(id, button) {
  if (!id) return;
  if (button) button.disabled = true;
  try {
    await fetch(`/api/workspaces/build-sessions/${encodeURIComponent(id)}/abandon`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
      body: '{}'
    });
  } catch (_) {
    // Today re-reads either way; an item that is still open simply stays.
  }
  void loadToday();
}

// Whether an item's own timestamp falls on the viewer's current day.
function happenedToday(sourceAt, now) {
  const at = new Date(sourceAt || '');
  if (Number.isNaN(at.getTime()) || at.getFullYear() < 2000) return false;
  return at.toDateString() === now.toDateString();
}

export function todayThreeSectionView(today, now = new Date()) {
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
    // What the progress row counts. The HQ's own status line sits in Working
    // on but is not work in progress, and Done keeps a week of results, of
    // which only today's are "done today".
    inProgress: working.filter(row => row.kind !== 'hq_status').length,
    doneToday: done.filter(row => happenedToday(row.sourceAt, now)).length
  };
}

export function todaySectionRows(section) {
  const health = String(section?.health?.status || 'unavailable');
  const rows = Array.isArray(section?.items) ? section.items.slice(0, 10) : [];
  const projected = rows.map(item => ({
    kind: String(item?.kind || 'item'),
    title: String(item?.title || '').trim(),
    detail: String(item?.detail || '').trim(),
    attribution: String(item?.attribution || '').trim(),
    route: safeTodayRoute(item?.route) ? String(item.route) : ''
  }));
  if (health === 'partial' || health === 'unavailable') {
    return [
      { kind: 'status', title: 'Some sources are unavailable — showing grounded items only.' },
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

// progressRowText is the one line that summarises work in the drawer.
export function progressRowText(working, done) {
  const count = value => Math.max(0, Number(value) || 0);
  return `${count(working)} in progress · ${count(done)} done today`;
}

// progressRowVisible: the row has nothing to summarise, and nothing to expand,
// when no work is in progress, none was done today and there are no meetings.
export function progressRowVisible({ working, done, meetings }) {
  return Number(working) > 0 || Number(done) > 0 || meetings === true;
}

// todayStripView is the one line the drawer folds Today behind: "Needs you · N"
// while something waits on the user, else "Today" while there is a brief row
// or a progress row to open. With nothing to open there is no strip, and the
// drawer is the conversation alone. A Today source that could not be read is
// said on the line, so a folded strip never reads as an all-clear. Folding
// hides the existing nodes; it never answers a card.
export function todayStripView({ count, hasBrief, hasProgress, unavailable, expanded } = {}) {
  const needs = Math.max(0, Math.floor(Number(count) || 0));
  const visible = needs > 0 || hasBrief === true || hasProgress === true || unavailable === true;
  const open = visible && expanded === true;
  let label = '';
  if (visible) label = needs > 0 ? `Needs you · ${needs}` : 'Today';
  if (visible && unavailable === true) label += ' · sources unavailable';
  return {
    visible,
    label,
    expanded: open,
    sectionsHidden: !open,
    toggleLabel: open ? 'Hide' : 'Show'
  };
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
  // The Daily Brief as Home last read it, for the drawer's brief row. hq is
  // null until Home has looked, then false (no Personal HQ) or { slug }.
  brief: { hq: null, revision: null, config: null, generation: '' },
  // How many things Needs you last counted, for the strip.
  needsCount: 0,
  // Whether the progress row's lists (Working on, Done) are expanded.
  progressOpen: false,
  // Whether the assistant's state lets Needs you and the rows show at all.
  sectionsAvailable: false,
  // The strip: folded on every opening of the drawer, until Show or this
  // tab's own new item opens it (see todayStripView, reveal). A reveal while
  // the drawer is closed waits for the next opening.
  stripExpanded: false,
  revealOnOpen: false,
  // The item a reveal brought into view. Today redraws (an opening re-reads
  // it) can move or replace it for a moment afterwards, so until `until` each
  // redraw aligns to it again, found by its key when it was redrawn.
  align: { key: '', item: null, until: 0 }
};

// How long after a reveal (or the opening it waited for) Today redraws keep
// the revealed item in view.
const REVEAL_ALIGN_MS = 4000;

function elements() {
  const root = document.getElementById('personalAssistantToday');
  if (!root) return null;
  return {
    root,
    launcherStatus: document.getElementById('personalAssistantLauncherStatus'),
    title: document.getElementById('personalAssistantTodayTitle'),
    banner: document.getElementById('personalAssistantTodayBanner'),
    strip: document.getElementById('personalAssistantTodayStrip'),
    stripText: document.getElementById('personalAssistantTodayStripText'),
    stripToggle: document.getElementById('personalAssistantTodayStripToggle'),
    sections: document.getElementById('personalAssistantTodaySections'),
    workingSection: document.getElementById('personalAssistantWorkingOn'),
    workingContent: document.getElementById('personalAssistantWorkingOnContent'),
    workingItems: document.getElementById('personalAssistantWorkingOnItems'),
    needsSection: document.getElementById('personalAssistantNeedsYou'),
    needsCount: document.getElementById('personalAssistantNeedsYouCount'),
    needsCards: document.getElementById('personalAssistantNeedsYouCards'),
    needsQueue: document.getElementById('personalAssistantNeedsYouQueue'),
    needsQueueTitle: document.getElementById('personalAssistantNeedsYouQueueTitle'),
    needsQueueCards: document.getElementById('personalAssistantNeedsYouQueueCards'),
    needsItems: document.getElementById('personalAssistantNeedsYouItems'),
    glance: document.getElementById('personalAssistantGlance'),
    briefRow: document.getElementById('personalAssistantBriefRow'),
    briefRowStatus: document.getElementById('personalAssistantBriefRowStatus'),
    progressRow: document.getElementById('personalAssistantProgressRow'),
    progressText: document.getElementById('personalAssistantProgressText'),
    progressAction: document.getElementById('personalAssistantProgressAction'),
    progressLists: document.getElementById('personalAssistantProgressLists'),
    doneSection: document.getElementById('personalAssistantDone'),
    doneItems: document.getElementById('personalAssistantDoneItems'),
    footer: document.getElementById('personalAssistantTodayFooter'),
    unavailable: document.getElementById('personalAssistantTodayUnavailable'),
    retry: document.getElementById('personalAssistantTodayRetry'),
    setup: document.getElementById('personalAssistantSpecialistSetup'),
    setupTitle: document.getElementById('personalAssistantSpecialistSetupTitle'),
    setupStatus: document.getElementById('personalAssistantSpecialistSetupStatus'),
    setupSamples: document.getElementById('personalAssistantSpecialistSetupSamples'),
    setupRuns: document.getElementById('personalAssistantSpecialistSetupRuns'),
    setupActions: document.getElementById('personalAssistantSpecialistSetupActions'),
    meetingsSection: document.getElementById('personalAssistantTodayMeetingsSection'),
    meetingsTitle: document.getElementById('personalAssistantTodayMeetingsTitle'),
    meetings: document.getElementById('personalAssistantTodayMeetings'),
    meetingsNote: document.getElementById('personalAssistantTodayMeetingsNote')
  };
}

// An unfinished workspace build is the user's own work in progress, so it
// leads Needs you instead of waiting in the collapsed queue behind the
// assistant's suggestions.
function renderUnfinishedBuild(els, rows) {
  const host = els?.needsCards;
  if (!host) return;
  const builds = rows.filter(row => row.kind === 'workspace_build');
  let list = document.getElementById('personalAssistantNeedsYouBuild');
  if (!builds.length) {
    list?.remove();
    return;
  }
  if (!list) {
    list = document.createElement('ul');
    list.id = 'personalAssistantNeedsYouBuild';
    list.className = 'personal-assistant-today__build';
  }
  host.prepend(list);
  renderCompactRows(list, builds);
}

// A hired agent's finished first look is news the user acts on in place, so its
// report is a card at the top of Needs you, after any unfinished build, never a
// row in the collapsed queue. items are the section's raw Today items.
function renderFirstLookReports(els, items) {
  const host = els?.needsCards;
  if (!host) return;
  let list = document.getElementById('personalAssistantNeedsYouReports');
  if (!list) {
    list = document.createElement('div');
    list.id = 'personalAssistantNeedsYouReports';
    list.className = 'personal-assistant-today__reports';
  }
  placeFirstLookReports(els, list);
  mountFirstLookReports(list, items, { onDone: () => void loadToday() });
  revealOwnReports(list);
}

// placeFirstLookReports keeps the report cards at the top of Needs you, after
// any unfinished build. Behind the strip they are counted in its number
// instead of being shown; this tab's own new report opens the strip.
function placeFirstLookReports(
  els,
  list = document.getElementById('personalAssistantNeedsYouReports')
) {
  const host = els?.needsCards;
  if (!host || !list) return;
  const build = document.getElementById('personalAssistantNeedsYouBuild');
  if (build?.parentElement === host) {
    if (build.nextElementSibling !== list) build.after(list);
  } else if (host.firstElementChild !== list) host.prepend(list);
}

function renderCompactRows(list, rows, skipCards = false) {
  if (!list) return;
  list.replaceChildren();
  rows.forEach(row => {
    // Offer cards provide their own single actions. Never render another
    // inert row that looks like a competing action.
    if (
      skipCards &&
      (row.kind === 'folder_offer' || row.kind === 'hq_setup' || row.kind === 'second_brain_result')
    )
      return;
    const li = document.createElement('li');
    // The protection line's route is its primary button, not its title.
    if (row.kind === 'backup_coverage') li.textContent = row.title;
    else if (row.route) {
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
    if (row.kind === 'workspace_build' && row.actions?.length) {
      const actions = document.createElement('div');
      actions.className = 'personal-assistant-today__actions';
      for (const action of row.actions) {
        const button = document.createElement('button');
        button.type = 'button';
        button.className =
          action === 'resume'
            ? 'personal-assistant-today__action personal-assistant-today__action--primary'
            : 'personal-assistant-today__action';
        button.dataset.workspaceBuildAction = action;
        button.textContent = action === 'resume' ? 'Resume' : 'Discard';
        button.addEventListener('click', () => {
          if (action === 'resume') resumeWorkspaceBuild();
          else void discardWorkspaceBuild(row.id, button);
        });
        actions.append(button);
      }
      li.append(actions);
    }
    if (row.kind === 'backup_coverage') {
      li.classList.add('personal-assistant-today__coverage');
      const actions = document.createElement('div');
      actions.className = 'personal-assistant-today__actions';
      for (const control of backupCoverageControls(row)) {
        const element = document.createElement(control.kind === 'link' ? 'a' : 'button');
        element.className = control.primary
          ? 'personal-assistant-today__action personal-assistant-today__action--primary'
          : 'personal-assistant-today__action personal-assistant-today__action--quiet';
        element.textContent = control.label;
        if (control.kind === 'link') element.href = control.href;
        else {
          element.type = 'button';
          element.dataset.backupCoverageAction = control.action;
          element.addEventListener('click', async () => {
            element.disabled = true;
            if (await declineBackupCoverage()) li.remove();
            else element.disabled = false;
            void loadToday();
          });
        }
        actions.append(element);
      }
      li.append(actions);
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

// Needs you is shown only when something is in it, and its heading carries the
// number: the cards placed in it (an unfinished build, the HQ confirmation, a
// folder offer that was waiting) plus everything in "Also needs you". Cards
// arrive and leave after the Today read, so this is counted from what is
// actually there.
function syncNeedsYou(els = elements()) {
  if (!els?.needsSection) return 0;
  const queued = syncNeedsQueue(els);
  const cards = Array.from(els.needsCards?.children || []).reduce((count, card) => {
    if (card.hidden || card.id === 'personalAssistantNeedsYouReports') return count;
    // The unfinished-build list is one element holding one row per build.
    if (card.id === 'personalAssistantNeedsYouBuild') return count + card.childElementCount;
    return count + 1;
  }, 0);
  // One card per hired agent's report. A waiting folder card sits in Needs you
  // itself and is one of the cards above.
  const reports = document.getElementById('personalAssistantNeedsYouReports');
  const reported = reports && !reports.hidden ? reports.childElementCount : 0;
  const count = cards + reported + queued;
  const hidden = count === 0;
  if (els.needsSection.hidden !== hidden) els.needsSection.hidden = hidden;
  if (els.needsCount && els.needsCount.textContent !== String(count)) {
    els.needsCount.textContent = String(count);
  }
  state.needsCount = count;
  syncGlance(els);
  syncStrip(els);
  return count;
}

// The strip and what it stands for. Folding only hides the sections: their
// cards are not redrawn, so anything typed into one is still there. A setup
// prerequisite (no HQ yet, a repair) is not folded: it is the one thing the
// drawer is for until it is done, so it shows with no strip.
function syncStrip(els = elements()) {
  if (!els?.sections) return;
  const prerequisite = ['needs_hq', 'provisioning_hq', 'repair_needed'].includes(
    state.relationship?.state
  );
  const strip = todayStripView({
    count: state.sectionsAvailable ? state.needsCount : 0,
    hasBrief: state.sectionsAvailable && els.briefRow?.hidden === false,
    hasProgress: state.sectionsAvailable && els.progressRow?.hidden === false,
    unavailable:
      state.sectionsAvailable &&
      (Boolean(state.today?.unavailable_sources?.length) ||
        ['partial', 'unavailable'].includes(state.today?.state)),
    expanded: state.stripExpanded
  });
  const visible = strip.visible && !prerequisite;
  const hidden = !state.sectionsAvailable || (!prerequisite && strip.sectionsHidden);
  if (els.sections.hidden !== hidden) els.sections.hidden = hidden;
  if (!els.strip) return;
  if (els.strip.hidden !== !visible) els.strip.hidden = !visible;
  if (els.stripText && els.stripText.textContent !== strip.label)
    els.stripText.textContent = strip.label;
  if (els.stripToggle) {
    if (els.stripToggle.textContent !== strip.toggleLabel)
      els.stripToggle.textContent = strip.toggleLabel;
    els.stripToggle.setAttribute('aria-expanded', strip.expanded ? 'true' : 'false');
  }
}

function setStripExpanded(expanded) {
  state.stripExpanded = expanded === true;
  syncStrip();
}

/**
 * Opens the strip. A request of the user's (Show is the toggle; "Needs you"
 * in the conversation, a setup walkthrough) always opens it. An arriving item
 * passes `key` and opens it only when this tab produced it, the first time
 * (personal-assistant-needs-reveal.js). `item`, if given, is brought into
 * view. Returns whether the strip was opened. `reason` names the caller.
 */
function reveal(reason, { key = '', item = null } = {}) {
  if (key && !takeNeedsReveal(key)) return false;
  setStripExpanded(true);
  if (item) state.align = { key, item, until: Date.now() + REVEAL_ALIGN_MS };
  // Opening the drawer folds the strip; a reveal that came while it was
  // closed is kept for that opening.
  if (window.PersonalAssistantPanel?.isOpen?.() === false) state.revealOnOpen = true;
  else if (item) bringIntoView(item);
  return true;
}

// A report card this tab caused that has just been drawn opens the strip.
function revealOwnReports(list) {
  for (const card of list?.querySelectorAll?.('[data-look-id]') || []) {
    reveal('report', { key: reportRevealKey(card), item: card });
  }
}

// The revealed item as it is drawn now: the same node, or the report card
// that replaced it.
function alignedItem() {
  const { key, item } = state.align;
  if (item?.isConnected) return item;
  if (!key) return null;
  const cards = document.querySelectorAll('#personalAssistantNeedsYouReports [data-look-id]');
  return Array.from(cards).find(card => reportRevealKey(card) === key) || null;
}

// After a Today redraw, keep a just-revealed item in view.
function realignReveal() {
  if (!state.stripExpanded || Date.now() > state.align.until) return;
  const item = alignedItem();
  if (item) bringIntoView(item);
}

// Scrolls the drawer to a revealed item, top first. The conversation may still
// be loading or settling on the reply the user last read; scrolling before it
// has would be undone, so wait for it (about two seconds at most).
function bringIntoView(item, frames = 0) {
  requestAnimationFrame(() => {
    if (!item.isConnected || item.getClientRects().length === 0) return;
    const transcript = window.PersonalAssistantTranscript;
    const busy =
      (transcript && transcript.isSettled?.() === false) ||
      window.PersonalAssistantConversation?.isLoading?.() === true;
    if (busy && frames < 120) {
      bringIntoView(item, frames + 1);
      return;
    }
    if (transcript?.revealControl) transcript.revealControl(item, true);
    else item.scrollIntoView?.({ block: 'nearest' });
  });
}

// The brief row and the progress row share one bordered group; it goes when
// both rows do.
function syncGlance(els = elements()) {
  if (!els?.glance) return;
  const hidden = Boolean(els.briefRow?.hidden) && Boolean(els.progressRow?.hidden);
  if (els.glance.hidden !== hidden) els.glance.hidden = hidden;
}

// The one row that stands in for the Daily Brief: where it is, and what state
// it is in. It links to the Daily Brief station in My HQ, where the brief is
// read. Hidden until Home knows there is a Personal HQ, and when there is none.
function renderBriefRow(els = elements()) {
  if (!els?.briefRow) return;
  const { hq, revision, config, generation } = state.brief;
  const link = hq ? dailyBriefStationLink(hq.slug) : '';
  const today = personalAssistantTodayView(state.today);
  // The row belongs to the assistant's Today, so it follows the sections.
  const shown = Boolean(link) && (today.active || today.paused || today.partial);
  els.briefRow.hidden = !shown;
  if (shown) {
    els.briefRow.href = link;
    const status = dailyBriefStatus({
      revision,
      config,
      generation,
      paused: today.paused || state.relationship?.state === 'paused'
    });
    if (els.briefRowStatus) els.briefRowStatus.textContent = ` · ${dailyBriefRowStatus(status)}`;
  }
  syncGlance(els);
  syncStrip(els);
}

// The row that summarises work. Activating it expands Working on (with today's
// meetings) and Done in place; activating it again collapses them.
function renderProgressRow(els, sections) {
  if (!els?.progressRow) return;
  const meetings = Boolean(els.meetingsSection) && !els.meetingsSection.hidden;
  const shown = progressRowVisible({
    working: sections.inProgress,
    done: sections.doneToday,
    meetings
  });
  els.progressRow.hidden = !shown;
  if (!shown) state.progressOpen = false;
  if (els.progressText) {
    els.progressText.textContent = progressRowText(sections.inProgress, sections.doneToday);
  }
  syncProgressLists(els);
  syncGlance(els);
  syncStrip(els);
}

function syncProgressLists(els = elements()) {
  if (!els?.progressRow) return;
  const open = state.progressOpen && !els.progressRow.hidden;
  els.progressRow.setAttribute('aria-expanded', open ? 'true' : 'false');
  if (els.progressAction) els.progressAction.textContent = open ? 'Hide' : 'Show';
  if (els.progressLists) els.progressLists.hidden = !open;
}

// The drawer's header says when the next check-in is and holds the More
// links; both come from the Today that Home already has.
function shareTodayWithDrawer(today) {
  window.PersonalAssistantPanel?.setToday?.(today || null);
}

function renderToday(today) {
  const els = elements();
  if (!els) return;
  state.today = today;
  const view = personalAssistantTodayView(today);
  // A ready Today's summary and source errors fold behind the strip (whose
  // line says when sources are unavailable). What stops the assistant working
  // (paused, no model, repair, hiring, HQ) keeps its banner above the strip.
  if ((view.active && !view.modelUnavailable) || view.partial || view.unavailable)
    els.sections?.prepend(els.banner);
  else if (els.strip) els.root.insertBefore(els.banner, els.strip);
  els.root.hidden = false;
  els.root.dataset.state = view.state;
  els.title.textContent = `Today from ${view.displayName}`;
  shareTodayWithDrawer(today);

  if (view.repair) {
    els.banner.textContent =
      'Your existing assistant or Personal HQ needs repair before work can continue.';
  } else if (view.needsHQ) {
    // The HQ confirm card under Needs you is the one Build action; Today here
    // carries only what a hire reports before the HQ exists.
    els.banner.replaceChildren();
  } else if (view.needsHire) {
    renderNeedsHireBanner(els);
  } else if (view.unavailable) {
    els.banner.textContent = 'Personal assistant status is unavailable. No all-clear is implied.';
  } else if (view.paused) {
    els.banner.textContent = `${view.displayName} is paused proactively. Your records and prior briefs are unchanged.`;
  } else if (view.partial) {
    els.banner.textContent = partialTodaySummary(today);
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
  renderUnfinishedBuild(els, sections.needs);
  renderFirstLookReports(els, today?.needs_you?.items);
  renderCompactRows(
    els.needsItems,
    sections.needs.filter(
      row => row.kind !== 'workspace_build' && !isFirstLookReportKind(row.kind)
    ),
    true
  );
  renderCompactRows(els.doneItems, sections.done);
  if (els.workingSection)
    els.workingSection.hidden = !(sections.working.length || !els.meetingsSection?.hidden);
  if (els.doneSection) els.doneSection.hidden = !sections.done.length;
  if (els.footer) els.footer.hidden = !sections.footer;
  if (els.unavailable) els.unavailable.textContent = sections.footer;
  state.sectionsAvailable = Boolean(
    view.active || view.paused || view.partial || view.needsHQ || view.unavailable
  );
  renderProgressRow(els, sections);
  renderBriefRow(els);
  syncNeedsYou(els);
  els.banner.hidden = !els.banner.textContent.trim();
  renderLauncherCue(els, today);
  realignReveal();
}

function renderRelationship(personalAssistant, view) {
  const els = elements();
  if (!els) return;
  state.relationship = personalAssistant || null;
  state.today = null;
  shareTodayWithDrawer(null);
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
  state.sectionsAvailable = Boolean(view.available || view.needsHQ);
  // Until Today is read there is no work to summarise and no brief to point at.
  if (els.progressRow) els.progressRow.hidden = true;
  syncProgressLists(els);
  renderBriefRow(els);
  syncNeedsYou(els);
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
    // reconnect, resume, or fix view on arrival (PRD FR28). There is nothing
    // to walk through first, so the link goes straight there.
    link.href = MEET_ASSISTANT_AGENTS_ROUTE;
    link.textContent = recoverable
      ? 'Review and reconnect'
      : blocked
        ? 'See what differs and fix it'
        : 'Repair personal assistant';
    const message = recoverable
      ? personalAssistant?.hq_workspace_id
        ? 'Ori found the existing assistant and Personal HQ with matching stable IDs. '
        : 'Ori found the existing assistant profile with its durable ownership marker. '
      : blocked
        ? 'Existing Personal Assistant records do not agree. Ori will not guess or create a duplicate, but it can show you what differs and offer a safe fix. '
        : 'Your existing assistant or Personal HQ needs repair. ';
    els.banner.append(message, link);
    return;
  }
  if (view.needsHQ) {
    // The confirm card is the default action; the Map walkthrough remains an
    // alternate, but a second Build link here would compete with the card.
    els.banner.replaceChildren();
    els.banner.hidden = true;
    // An agent hired from the Agents page needs no HQ, so its first look
    // reports in Today before the HQ exists.
    void loadToday();
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
    const retained = state.today;
    renderToday({
      ...retained,
      state: retained?.state || 'unavailable',
      display_name: state.relationship?.display_name,
      unavailable_sources: [...new Set([...(retained?.unavailable_sources || []), 'Today'])]
    });
    // Nothing is known, so nothing is listed: an empty drawer here would read
    // as "all clear", and this message is what stands in its place.
    els.banner.textContent = retained
      ? 'Couldn’t refresh assistant requests. Showing last-loaded records. Retry to check for changes.'
      : 'Assistant requests are temporarily unavailable. Retry, or use the Workspace Map.';
    els.banner.hidden = false;
    syncStrip(els);
    shareTodayWithDrawer(state.today);
    renderLauncherCue(els, { state: 'unavailable' });
  }
}

// Home keeps today's Daily Brief prepared. The brief is displayed in My HQ, but
// the assistant's lists are built from its items, so they must not wait for the
// user to go there. This needs no brief markup: it reads whether there is a
// Personal HQ, asks the server for today's brief when there is none, and keeps
// the drawer's brief row in step.
async function keepTodaysBriefPrepared() {
  const brief = state.brief;
  try {
    if (!onboardingGateDecision(await loadOnboardingStatus()).allowWorkspaceHydration) return;
    const response = await fetch('/api/personal-hq/status', {
      headers: { Accept: 'application/json' }
    });
    const status = response.ok ? (await response.json())?.status : null;
    const slug = String(status?.workspace?.folder_slug || '').trim();
    brief.hq = status?.valid && slug ? { slug } : false;
  } catch (_) {
    brief.hq = false;
  }
  renderBriefRow();
  if (!brief.hq) return;
  await prepareTodaysBrief({
    onState: ({ revision, config, claim }) => {
      brief.revision = revision;
      brief.config = config;
      brief.generation = String((claim && claim.status) || '');
      renderBriefRow();
    },
    onProgress: status => {
      brief.generation = status;
      renderBriefRow();
    }
  });
}

function init() {
  state.root = document.getElementById('personalAssistantToday');
  if (!state.root) return;
  // Place existing card controllers inside Needs you without remounting or
  // duplicating any of their event handlers.
  const needs = document.getElementById('personalAssistantNeedsYouCards');
  if (needs && typeof MutationObserver !== 'undefined') {
    // Cards show and hide themselves after the Today read (the HQ
    // confirmation, a setup card), and the folder flow places its offer card
    // here when one was waiting, so the section and its count follow what is
    // actually in it.
    new MutationObserver(() => syncNeedsYou()).observe(
      document.getElementById('personalAssistantNeedsYou'),
      { subtree: true, attributes: true, childList: true, attributeFilter: ['hidden'] }
    );
  }
  // The folder flow is not placed here: it runs in the conversation.
  const hqCard = document.getElementById('personalAssistantHQCard');
  if (hqCard) needs?.append(hqCard);
  ['personalAssistantSpecialistSetup', 'assistantLedSetup'].forEach(id => {
    const node = document.getElementById(id);
    if (node) document.getElementById('personalAssistantNeedsYouQueueCards')?.append(node);
  });
  document.addEventListener('personal-assistant:hq-receipt', () => {
    if (state.today)
      renderCompactRows(elements()?.doneItems, todayThreeSectionView(state.today).done);
  });
  // A saved or deferred interview changes `interview_status`, which decides
  // whether the "Optional interview" link shows.
  document.addEventListener('personal-assistant-knowledge-changed', () => void loadToday());
  document.getElementById('personalAssistantProgressRow')?.addEventListener('click', () => {
    state.progressOpen = !state.progressOpen;
    syncProgressLists();
  });
  document
    .getElementById('personalAssistantTodayRetry')
    ?.addEventListener('click', () => void loadToday());
  // The strip folds every time the drawer opens, so the conversation is what
  // shows; Show and Hide hold until it closes. A reveal while it was closed
  // opens it again on this opening.
  document.addEventListener('personal-assistant:opened', event => {
    if (event.detail?.reopened === true) return;
    // A link to what needs the user (`/?panel=today`) asks for the strip.
    const requested = window.PersonalAssistantPanel?.todayRequested?.() === true;
    setStripExpanded(state.revealOnOpen || requested);
    if (state.revealOnOpen && state.align.item) {
      state.align.until = Date.now() + REVEAL_ALIGN_MS;
      realignReveal();
    }
    state.revealOnOpen = false;
  });
  document
    .getElementById('personalAssistantTodayStripToggle')
    ?.addEventListener('click', () => setStripExpanded(!state.stripExpanded));
  document.addEventListener('personal-assistant:status', event => {
    renderRelationship(event.detail?.personalAssistant, event.detail?.view);
  });
  const panelState = window.PersonalAssistantPanel?._state;
  if (panelState?.personalAssistant) {
    renderRelationship(panelState.personalAssistant, panelState.view);
  }
  window.PersonalAssistantToday = {
    refresh: loadToday,
    // Opens the strip, for an arriving item of this tab's (`key`) or at the
    // user's request; see reveal.
    reveal,
    revealFolderReview,
    // Shows Needs you when the user asked for something under it.
    expand: () => reveal('request')
  };
  // The drawer may have opened from `/?panel=today` before this module ran.
  if (window.PersonalAssistantPanel?.todayRequested?.() === true) setStripExpanded(true);
  void keepTodaysBriefPrepared();
}

if (typeof document !== 'undefined') {
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', init);
  else init();
}
