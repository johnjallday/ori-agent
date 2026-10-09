import {
  collectWorkspaceContext,
  workspaceContextLabel
} from './personal-assistant-workspace-context.js';

const PANEL_DRAFT_KEY = 'ori.personalAssistant.panelDraft';
const PANEL_OPEN_KEY = 'ori.personalAssistant.panelOpen';

function tabValue(key) {
  try {
    return window.sessionStorage.getItem(key) || '';
  } catch (_) {
    return '';
  }
}
function saveTabValue(key, value) {
  try {
    window.sessionStorage.setItem(key, String(value || ''));
  } catch (_) {
    /* Storage may be disabled. */
  }
}

const STATUS_ENDPOINT = '/api/personal-assistant';
const TODAY_ENDPOINT = '/api/personal-assistant/today';
const HANDOFF_LIMIT = 400;

export function personalAssistantPanelView(personalAssistant) {
  const state = String(personalAssistant?.state || 'unavailable');
  const known = state !== 'unavailable';
  const available = state === 'active' || state === 'paused';
  const name = String(personalAssistant?.display_name || '').trim() || 'Personal assistant';
  return {
    state,
    known,
    available,
    helpOnly: known,
    name,
    role: 'Personal Assistant',
    paused: state === 'paused',
    repair: state === 'repair_needed',
    needsHire: state === 'needs_hire' || state === 'hiring',
    // A real hire with no home base yet. The launcher may show this identity —
    // unlike needsHire, where no name is trustworthy yet — but normal
    // submission stays closed until HQ exists.
    needsHQ: state === 'needs_hq' || state === 'provisioning_hq',
    // Launcher visibility: shown once there is a real identity to show, even
    // before HQ exists or while its durable records need repair; submission
    // itself is gated by `available` alone.
    visible:
      available || state === 'needs_hq' || state === 'provisioning_hq' || state === 'repair_needed',
    placeholder: available
      ? `Ask ${name} a question or describe what you want help with`
      : state === 'needs_hq' || state === 'provisioning_hq'
        ? `Build ${name}’s Personal HQ before sending work`
        : 'Finish personal assistant setup before sending work'
  };
}

export function boundedAssistantHandoff(value) {
  return Array.from(String(value || '').trim())
    .slice(0, HANDOFF_LIMIT)
    .join('');
}

export function canSubmitAssistantWork({ available, pending, busy, text }) {
  return (
    available === true && pending !== true && busy !== true && String(text || '').trim().length > 0
  );
}

/**
 * The composer text after an unsent message comes back. Text typed since the
 * failed send is never overwritten.
 */
export function restoredDraft(current, unsent) {
  const typed = String(current || '');
  return typed.trim() ? typed : String(unsent || '');
}

/**
 * Where keyboard focus goes when the drawer opens. The composer is the point of
 * the drawer, so it gets focus whenever the assistant can accept work. When it
 * cannot (not hired, HQ not built, repair needed) the composer is disabled, and
 * focus goes to the first control in the drawer instead.
 */
export function assistantOpenFocusTarget(view) {
  return view?.available === true ? 'composer' : 'first-control';
}

export function assistantPanelShouldCloseOnKey(key, open, topmostOverlayOpen = false) {
  return key === 'Escape' && open === true && topmostOverlayOpen !== true;
}

export function restoreAssistantPanelFocus(trigger, ownerDocument) {
  if (!trigger || !ownerDocument?.contains?.(trigger) || typeof trigger.focus !== 'function') {
    return false;
  }
  trigger.focus();
  return true;
}

/** A link the drawer may render: a same-origin path with no tricks in it. */
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

/**
 * The line under the assistant's name: when the next check-in is. `time` is an
 * ISO instant to render after `text`. Until Today is known, or when it cannot
 * say (not hired, no HQ, repair, unavailable), the line is the plain role.
 */
export function assistantCheckInLine(today) {
  const state = String(today?.state || '');
  if (!['active', 'paused', 'partial', 'model_unavailable', 'healthy_empty'].includes(state)) {
    return { text: 'Personal Assistant', time: '' };
  }
  if (state === 'paused') return { text: 'Check-ins paused', time: '' };
  if (!today.next_check_in) return { text: 'No check-in scheduled', time: '' };
  if (Number.isNaN(new Date(today.next_check_in).getTime())) {
    return { text: 'Next check-in unavailable', time: '' };
  }
  return { text: 'Next check-in · ', time: String(today.next_check_in) };
}

/** Which of the More menu's links to show, from Today's validated routes. */
export function assistantMoreLinks(today) {
  const links = today?.links || {};
  const route = key => (safeTodayRoute(links[key]) ? String(links[key]) : '');
  return {
    personal_hq: route('personal_hq'),
    working_agreement: route('working_agreement'),
    memory: route('memory'),
    advanced: route('advanced'),
    interview: ['available', 'offered', 'deferred'].includes(today?.interview_status)
  };
}

/** Where what needs the user is listed: Home, with the drawer open. */
export const NEEDS_YOU_URL = '/?panel=today';

/**
 * The one line a page other than Home shows for what needs the user: how many
 * things, linking to Home, where they are listed. Nothing is shown when nothing
 * needs them. When the number cannot be read the line still points at Home,
 * because a missing line would read as "nothing needs you".
 */
export function assistantNeedsLine(today, { failed = false } = {}) {
  const unknown = { visible: true, text: 'Open Home to see what needs you' };
  if (failed || !today || String(today.state || '') === 'unavailable') return unknown;
  const needs = today.needs_you;
  if (String(needs?.health?.status || '') === 'unavailable') return unknown;
  const count = Array.isArray(needs?.items) ? needs.items.length : 0;
  if (!count) return { visible: false, text: '' };
  return { visible: true, text: count === 1 ? '1 needs you' : `${count} need you` };
}

/** Where "Explore a folder" goes from a page that is not Home. */
export const EXPLORE_FOLDER_URL = '/?panel=today&folder=show';

/**
 * The one suggestion above the composer, "Explore a folder". It is offered when
 * the assistant can accept work, which already means a Personal HQ exists, and
 * is disabled while a folder is being chosen or explored.
 */
export function assistantChipView({ available, folderBusy } = {}) {
  return { visible: available === true, disabled: folderBusy === true };
}

const state = {
  view: personalAssistantPanelView(null),
  personalAssistant: null,
  today: null,
  pending: false,
  open: false,
  draft: '',
  workspaceSequence: 0,
  lastTrigger: null,
  // True while Home's folder flow is waiting for a folder or exploring one.
  folderBusy: false,
  els: null
};

function setStatus(message) {
  if (state.els?.status) state.els.status.textContent = String(message || '');
}

function syncPanelViewport() {
  if (!state.els?.root) return;
  const panel = state.els.panel;
  const scale = panel?.offsetWidth
    ? Math.max(1, Math.round((panel.getBoundingClientRect().width / panel.offsetWidth) * 100) / 100)
    : 1;
  state.els.root.style.setProperty('--pa-viewport-width', `${window.innerWidth / scale}px`);
  state.els.root.style.setProperty('--pa-viewport-height', `${window.innerHeight / scale}px`);
  panel?.classList.toggle(
    'personal-assistant-panel--explorer-narrow',
    window.innerWidth / scale <= 760
  );
  const navbar = document.querySelector?.('nav.navbar');
  const bottom = navbar?.getBoundingClientRect?.().bottom;
  const inlineToolbar =
    window.innerWidth / scale >= 600 && (window.innerHeight - (bottom || 0)) / scale <= 520;
  panel?.classList.toggle('personal-assistant-panel--inline-toolbar', inlineToolbar);
  const clear = document.getElementById('personalAssistantFolderFocusClear');
  const clearMount =
    inlineToolbar && panel?.classList.contains('personal-assistant-panel--exploring')
      ? state.els.chips
      : document.getElementById('personalAssistantFolderFocus');
  if (clear && clearMount && clear.parentElement !== clearMount) clearMount.append(clear);
  if (Number.isFinite(bottom)) {
    state.els.root.style.setProperty(
      '--ori-navbar-bottom',
      `${Math.max(0, Math.ceil(bottom / scale))}px`
    );
  }
}

function assistantAvatarMarkup(name, appearance) {
  if (window.AgentAvatar?.markup) {
    return window.AgentAvatar.markup({ name, appearance: appearance || {} }, { size: 40 });
  }
  return '<span class="agent-avatar agent-avatar--generated">P</span>';
}

function renderIdentity() {
  const { els, view, personalAssistant } = state;
  if (!els) return;
  els.launcher.hidden = !view.visible;
  els.launcherName.textContent = view.name;
  els.title.textContent = view.name;
  els.input.placeholder = view.placeholder;
  els.input.disabled = !view.available;
  els.send.disabled = !view.available || state.pending;
  const avatar = assistantAvatarMarkup(view.name, personalAssistant?.appearance);
  els.launcherAvatar.innerHTML = avatar;
  els.panelAvatar.innerHTML = avatar;
  els.panel.dataset.relationshipState = view.state;
  renderChip();
  // Home says these in the banner under the header. A page without that banner
  // says them here, above the composer, so they are never said twice.
  if (els.todayBanner) return;
  if (view.paused) {
    setStatus('Paused proactively. Direct questions still use the same confirmation gates.');
  } else if (view.needsHQ && els.status) {
    els.status.replaceChildren(`${view.name} needs a home base before you can work together. `);
    const link = document.createElement('a');
    link.href = '/?quest=build-hq';
    link.textContent = 'Build Personal HQ';
    els.status.append(link);
  }
}

function renderChip() {
  const els = state.els;
  if (!els?.chips || !els.folderChip) return;
  const chip = assistantChipView({
    available: state.view.available,
    folderBusy: state.folderBusy
  });
  els.chips.hidden = !chip.visible;
  els.folderChip.disabled = chip.disabled;
}

/** Home's folder flow says when a folder is being chosen or explored. */
function setFolderBusy(busy, source = 'legacy') {
  // The old Home offer chooser can remain open for an unrelated setup. Its
  // visibility is not an in-flight composer selection and must not lock Add.
  if (source === 'legacy' && window.PersonalAssistantFolderContext) return;
  state.folderBusy = busy === true;
  renderChip();
}

/**
 * "Explore a folder". Home runs the flow in this drawer's conversation; every
 * other page has no folder flow of its own, so it goes to Home's.
 */
function exploreFolder() {
  if (window.PersonalAssistantFolderContext?.open) {
    window.PersonalAssistantFolderContext.open();
    return;
  }
  const folder = window.PersonalAssistantFolder;
  if (folder && typeof folder.open === 'function') folder.open();
  else window.location.assign(EXPLORE_FOLDER_URL);
}

function setMenuLink(link, route) {
  if (!link) return;
  link.hidden = !route;
  if (route) link.href = route;
}

/** The header's check-in line and More links, from the Today the drawer has. */
function renderHeader() {
  const els = state.els;
  if (!els) return;
  if (els.checkIn) {
    const line = assistantCheckInLine(state.today);
    els.checkIn.replaceChildren(line.text);
    if (line.time) {
      const date = new Date(line.time);
      const time = document.createElement('time');
      time.dateTime = line.time;
      time.textContent = new Intl.DateTimeFormat(undefined, {
        weekday: 'short',
        hour: 'numeric',
        minute: '2-digit'
      }).format(date);
      time.title = date.toLocaleString();
      els.checkIn.append(time);
    }
  }
  const links = assistantMoreLinks(state.today);
  setMenuLink(els.links.personal_hq, links.personal_hq);
  setMenuLink(els.links.working_agreement, links.working_agreement);
  setMenuLink(els.links.memory, links.memory);
  // Manage agents is always offered; Today may only move where it points.
  if (els.links.advanced && links.advanced) els.links.advanced.href = links.advanced;
  if (els.links.interview) els.links.interview.hidden = !links.interview;
}

/**
 * Gives the drawer the Today it should describe in its header. Home passes the
 * projection it already loaded; null means "not known yet".
 */
function setToday(today) {
  state.today = today || null;
  renderHeader();
}

function renderNeedsLine(line) {
  const els = state.els;
  if (!els?.needsLine) return;
  els.needsLine.hidden = !line.visible;
  if (els.needsLineText) els.needsLineText.textContent = line.text;
}

let todayRead = 0;

/**
 * A page other than Home has no Today of its own, so the drawer reads it when
 * it opens: for the header's check-in line and More links, and for the one
 * line that says how much needs the user. Home gives the drawer its Today
 * through setToday and never comes here.
 */
async function readTodayForThisPage() {
  if (!state.els?.needsLine) return;
  const read = ++todayRead;
  try {
    const response = await fetch(TODAY_ENDPOINT, { headers: { Accept: 'application/json' } });
    if (!response.ok) throw new Error(`today ${response.status}`);
    const today = (await response.json())?.today || null;
    if (read !== todayRead) return;
    setToday(today);
    renderNeedsLine(assistantNeedsLine(today));
  } catch (_) {
    if (read !== todayRead) return;
    setToday(null);
    renderNeedsLine(assistantNeedsLine(null, { failed: true }));
  }
}

function moveSharedWorkActivity() {
  const activity = document.getElementById('homeAssistantThinkingModal');
  if (!activity || !state.els?.activityMount || !state.view.available) return;
  if (activity.parentElement !== state.els.activityMount) {
    state.els.activityMount.appendChild(activity);
  }
  activity.hidden = false;
  activity.dataset.homeAssistantPanelScope = 'personal-assistant';
}

function applyPersonalAssistant(personalAssistant) {
  state.personalAssistant = personalAssistant || null;
  state.view = personalAssistantPanelView(personalAssistant);
  if (state.view.helpOnly && window.OriGuide?.setHelpOnly) {
    window.OriGuide.setHelpOnly({
      available: state.view.available,
      assistantName: state.view.name,
      needsHQ: state.view.needsHQ
    });
  }
  renderIdentity();
  moveSharedWorkActivity();
  if (state.view.available && window.OriAskRouting?.setPersonalAssistantIdentity) {
    window.OriAskRouting.setPersonalAssistantIdentity(state.view.name);
  }
  try {
    document.dispatchEvent(
      new CustomEvent('personal-assistant:status', {
        detail: { personalAssistant: state.personalAssistant, view: state.view }
      })
    );
  } catch (_) {
    // Status events are a local coordination seam, never a requirement to render.
  }
  return state.view;
}

async function refresh() {
  try {
    const response = await fetch(STATUS_ENDPOINT, { headers: { Accept: 'application/json' } });
    if (!response.ok) throw new Error(`status ${response.status}`);
    const payload = await response.json();
    return applyPersonalAssistant(payload?.personal_assistant || null);
  } catch (_) {
    // Keep the existing Help surface intact until relationship status is known.
    setStatus('Personal assistant status is unavailable. Reload to try again.');
    return state.view;
  }
}

function closeMoreMenu() {
  if (state.els?.more?.open) state.els.more.open = false;
}

function close(options = {}) {
  if (!state.open || !state.els) return;
  state.open = false;
  state.draft = state.els.input.value;
  saveTabValue(PANEL_OPEN_KEY, '');
  saveTabValue(PANEL_DRAFT_KEY, state.draft);
  closeMoreMenu();
  window.PersonalAssistantFolderContext?.close?.();
  window.PersonalAssistantTranscript?.closed?.();
  state.els.panel.hidden = true;
  state.els.launcher.setAttribute('aria-expanded', 'false');
  const trigger = state.lastTrigger;
  state.lastTrigger = null;
  if (options?.restoreFocus !== false) restoreAssistantPanelFocus(trigger, document);
}

/** The first control in the drawer, in reading order, that can take focus. */
function firstControl() {
  const candidates = state.els.panel.querySelectorAll(
    'a[href], button:not([disabled]), summary, input:not([disabled]), textarea:not([disabled]), select:not([disabled])'
  );
  return Array.from(candidates).find(el => el.getClientRects().length > 0) || null;
}

function focusOnOpen() {
  if (assistantOpenFocusTarget(state.view) === 'composer') state.els.input.focus();
  else firstControl()?.focus();
}

/**
 * Opens the drawer. There is one view, so there is nothing to select; a caller
 * that is about to put focus somewhere of its own passes `{ focus: false }`.
 */
function open(trigger, options = {}) {
  if (!state.view.visible || !state.els) return false;
  if (window.OriGuide?.close) window.OriGuide.close();
  moveSharedWorkActivity();
  syncPanelViewport();
  state.open = true;
  saveTabValue(PANEL_OPEN_KEY, '1');
  state.lastTrigger = trigger || document.activeElement;
  state.els.panel.hidden = false;
  state.els.launcher.setAttribute('aria-expanded', 'true');
  state.els.input.value = state.draft;
  if (options.focus !== false) focusOnOpen();
  // Rename/pause/repair changes are server-owned. Refresh on every open rather
  // than trusting the hire-time name or local storage.
  void refresh();
  void refreshWorkspaceContext();
  void readTodayForThisPage();
  try {
    document.dispatchEvent(new CustomEvent('personal-assistant:opened'));
  } catch (_) {
    // Listeners only tidy their own surface; none is required to open.
  }
  return true;
}

export function suggestedReplyDraft({
  current = '',
  text = '',
  maxLength = 2000,
  available,
  pending,
  busy,
  loading,
  folderPending
} = {}) {
  const draft = String(current);
  const suggestion = String(text);
  if (!available)
    return {
      draft,
      accepted: false,
      notice: 'The assistant is unavailable. Your draft is unchanged.'
    };
  if (pending || busy || loading || folderPending)
    return {
      draft,
      accepted: false,
      notice:
        'Wait for the current reply, folder selection or conversation to finish. Your draft is unchanged.'
    };
  if (draft !== '')
    return {
      draft,
      accepted: false,
      notice: 'Send or clear your draft before using a suggestion. Your text is unchanged.'
    };
  if (!suggestion.trim() || suggestion.length > maxLength)
    return {
      draft,
      accepted: false,
      notice:
        'This suggestion does not fit the composer. Write your question directly; nothing was inserted.'
    };
  return {
    draft: suggestion,
    accepted: true,
    notice: 'Review, then Send. Nothing has been transmitted.'
  };
}

// Unlike prefill(), a contextual shortcut never replaces an existing draft,
// appends text, opens a second draft, or transmits a message.
function suggestReply(text) {
  if (!state.els?.input || !state.open) return false;
  const result = suggestedReplyDraft({
    current: state.els.input.value,
    text,
    maxLength: state.els.input.maxLength > 0 ? state.els.input.maxLength : 2000,
    available: state.view.available,
    pending: state.pending,
    busy: window.OriAskRouting?.getState?.().busy === true,
    loading: window.PersonalAssistantConversation?.isLoading?.() === true,
    folderPending: window.PersonalAssistantFolderContext?.isPending?.() === true
  });
  if (result.accepted) {
    state.draft = result.draft;
    state.els.input.value = result.draft;
    state.els.input.dispatchEvent(new Event('input', { bubbles: true }));
  }
  state.els.input.focus();
  setStatus(result.notice);
  return result.accepted;
}

function prefill(text) {
  if (!state.view.available) return false;
  if (!open(state.els?.launcher)) return false;
  const bounded = boundedAssistantHandoff(text);
  state.draft = bounded;
  state.els.input.value = bounded;
  state.els.input.dispatchEvent(new Event('input', { bubbles: true }));
  state.els.input.focus();
  setStatus(`Review this request, then press Send to confirm it goes to ${state.view.name}.`);
  return true;
}

let replyWatch = null;

/**
 * Replaces the "still replying" status once the reply is in, so the line does
 * not keep describing a wait that is over.
 */
function announceWhenReplyArrives() {
  if (replyWatch) return;
  replyWatch = window.setInterval(() => {
    if (window.OriAskRouting?.getState?.().busy === true) return;
    window.clearInterval(replyWatch);
    replyWatch = null;
    if (String(state.els?.input?.value || '').trim()) {
      setStatus('The reply is in. Send your message when you are ready.');
    }
  }, 400);
}

/** Puts a message that was not sent back in the composer. */
function restoreDraft(text) {
  if (!state.els?.input) return false;
  const next = restoredDraft(state.els.input.value, text);
  state.draft = next;
  state.els.input.value = next;
  state.els.input.dispatchEvent(new Event('input', { bubbles: true }));
  return next === String(text || '');
}

async function refreshWorkspaceContext() {
  if (!state.els?.workspaceContext || !state.open) return;
  const sequence = ++state.workspaceSequence;
  state.els.workspaceContext.textContent = 'Checking workspace context…';
  try {
    const response = await fetch('/api/home-assistant/context', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ context: routeContext() })
    });
    if (!response.ok) throw new Error('Context unavailable');
    const data = await response.json();
    if (sequence !== state.workspaceSequence) return;
    state.els.workspaceContext.textContent = 'Context · ' + workspaceContextLabel(data);
  } catch (_) {
    if (sequence === state.workspaceSequence)
      state.els.workspaceContext.textContent =
        'Workspace context unavailable · refresh or choose an app-wide page';
  }
}

function routeContext() {
  if (window.OriGuide?._collectContext) {
    const context = window.OriGuide._collectContext();
    return { ...context, origin: 'personal_assistant_panel' };
  }
  return collectWorkspaceContext({
    pathname: window.location.pathname,
    workspaceId: document.body?.dataset?.workspaceId,
    workspaceSlug: document.body?.dataset?.workspaceSlug
  });
}

function submit(event) {
  event?.preventDefault();
  moveSharedWorkActivity();
  const text =
    String(state.els?.input?.value || '').trim() ||
    (window.PersonalAssistantFolderContext?.hasFolder?.() ? 'Explore this folder' : '');
  if (window.PersonalAssistantConversation?.isLoading?.()) {
    setStatus('Wait for the conversation to open before sending. Your draft is kept.');
    return false;
  }
  // While a reply is in flight the text stays in the box: sending it now would
  // start a second turn before the first one has a conversation to join.
  const busy = window.OriAskRouting?.getState?.().busy === true;
  if (window.PersonalAssistantFolderContext?.isPending?.()) {
    setStatus('Wait for the local folder preview before sending. Your message is kept here.');
    return false;
  }
  if (busy && text) {
    setStatus(
      `${state.view.name} is still replying. Your message is kept here; send it when the reply arrives.`
    );
    announceWhenReplyArrives();
    return false;
  }
  if (
    !canSubmitAssistantWork({ available: state.view.available, pending: state.pending, busy, text })
  ) {
    return false;
  }
  if (!window.OriAskRouting || typeof window.OriAskRouting.submit !== 'function') {
    setStatus('The work controller is unavailable on this page. Nothing was submitted.');
    return false;
  }
  if (window.OriAskRouting.setPersonalAssistantIdentity) {
    window.OriAskRouting.setPersonalAssistantIdentity(state.view.name);
  }
  window.PersonalAssistantTranscript?.send?.();
  state.pending = true;
  // Disabling a focused button drops focus to the page at once, even when it is
  // enabled again a moment later. Remember it, so Send from the keyboard does
  // not lose the user's place in the drawer.
  const sendHadFocus = document.activeElement === state.els.send;
  state.els.send.disabled = true;
  const sentStatus = `Sent to ${state.view.name}.`;
  setStatus(sentStatus);
  const operation = Promise.resolve(
    window.OriAskRouting.submit(text, {
      routeContext: routeContext(),
      openThinkingModal: false
    })
  );
  state.draft = '';
  state.els.input.value = '';
  saveTabValue(PANEL_DRAFT_KEY, '');
  // Planning can intentionally remain pending while the user reviews a choice;
  // the existing work controller owns that lifecycle. Release only this
  // composer's duplicate-submit guard after delegation has been accepted.
  state.pending = false;
  state.els.send.disabled = false;
  if (sendHadFocus && state.els.panel?.hidden !== true) state.els.send.focus();
  operation.then(
    () => {
      // Keep recovery/busy notices produced since Send; only clear our receipt.
      if (state.els?.status?.textContent === sentStatus) setStatus('');
    },
    () => setStatus('The request could not be routed. Nothing ran without confirmation.')
  );
  try {
    document.dispatchEvent(new CustomEvent('personal-assistant:sent'));
  } catch (_) {
    // Listeners only tidy their own surface; none is required to send.
  }
  return true;
}

export function assistantFocusNeedsScroll(viewport, target) {
  return Boolean(
    viewport && target && (target.top < viewport.top || target.bottom > viewport.bottom)
  );
}

function revealFocusedControl() {
  const scroll = document.getElementById('personalAssistantScroll');
  const active = document.activeElement;
  if (
    !state.open ||
    !active ||
    !scroll?.contains(active) ||
    !active.closest(
      '[data-folder-discussion], [data-folder-event-id], #personalAssistantFolderDiscussionChooser, #personalAssistantFolderSetupChoices, .personal-assistant-message__setup'
    )
  )
    return;
  if (window.PersonalAssistantTranscript) {
    // Resize recovery must not replace the owner's pre-mutation reading anchor.
    if (!window.PersonalAssistantTranscript.isSettled()) {
      requestAnimationFrame(revealFocusedControl);
      return;
    }
    window.PersonalAssistantTranscript.revealControl(active);
    return;
  }
  const viewport = scroll.getBoundingClientRect();
  const target = active.getBoundingClientRect();
  if (!assistantFocusNeedsScroll(viewport, target)) return;
  // Move only our scroll container. Native scrollIntoView may leave a few
  // clipped pixels under the pinned footer, or scroll outer page ancestors.
  const scale = viewport.height / scroll.offsetHeight || 1;
  const offset =
    target.top < viewport.top ? target.top - viewport.top - 1 : target.bottom - viewport.bottom + 1;
  scroll.scrollTop += offset / scale;
}

function init() {
  const panel = document.getElementById('personalAssistantPanel');
  const launcher = document.getElementById('personalAssistantLauncher');
  if (!panel || !launcher) return;
  state.els = {
    root: document.getElementById('oriGuideRoot'),
    panel,
    launcher,
    launcherName: document.getElementById('personalAssistantLauncherName'),
    launcherAvatar: document.getElementById('personalAssistantLauncherAvatar'),
    panelAvatar: document.getElementById('personalAssistantPanelAvatar'),
    title: document.getElementById('personalAssistantPanelTitle'),
    checkIn: document.getElementById('personalAssistantCheckIn'),
    more: document.getElementById('personalAssistantMore'),
    close: document.getElementById('personalAssistantClose'),
    form: document.getElementById('personalAssistantForm'),
    input: document.getElementById('personalAssistantInput'),
    send: document.getElementById('personalAssistantSend'),
    status: document.getElementById('personalAssistantPanelStatus'),
    workspaceContext: document.getElementById('personalAssistantWorkspaceContext'),
    chips: document.getElementById('personalAssistantChips'),
    folderChip: document.getElementById('personalAssistantFolderChip'),
    activityMount: document.getElementById('personalAssistantActivityMount'),
    // Present on Home only, where it carries the paused and Build HQ messages.
    todayBanner: document.getElementById('personalAssistantTodayBanner'),
    // Present on every other page: how much needs the user, linking to Home.
    needsLine: document.getElementById('personalAssistantNeedsLine'),
    needsLineText: document.getElementById('personalAssistantNeedsLineText'),
    links: {
      personal_hq: document.getElementById('personalAssistantTodayHQ'),
      working_agreement: document.getElementById('personalAssistantTodayAgreement'),
      memory: document.getElementById('personalAssistantTodayMemory'),
      advanced: document.getElementById('personalAssistantTodayAdvanced'),
      interview: document.getElementById('personalAssistantTodayInterview')
    }
  };
  launcher.addEventListener('click', () => (state.open ? close() : open(launcher)));
  state.els.close?.addEventListener('click', close);
  state.els.form?.addEventListener('submit', submit);
  state.draft = tabValue(PANEL_DRAFT_KEY);
  if (state.draft) state.els.input.value = state.draft;
  state.els.input?.addEventListener('input', () => {
    state.draft = state.els.input.value;
    saveTabValue(PANEL_DRAFT_KEY, state.draft);
  });
  document.addEventListener('ori-guide:context', () => void refreshWorkspaceContext());
  window.addEventListener('popstate', () => void refreshWorkspaceContext());
  window.addEventListener('beforeunload', () => {
    saveTabValue(PANEL_DRAFT_KEY, state.els.input.value);
    saveTabValue(PANEL_OPEN_KEY, state.open ? '1' : '');
  });
  state.els.folderChip?.addEventListener('click', exploreFolder);

  const more = state.els.more;
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
  document.addEventListener('keydown', event => {
    // Bootstrap removes `.show` before this bubbling listener runs, so the
    // event target is also part of the topmost-modal check.
    const modalOpen = Boolean(
      event.target?.closest?.('.modal') || document.querySelector?.('.modal.show')
    );
    if (assistantPanelShouldCloseOnKey(event.key, state.open, modalOpen)) {
      // Tab may have moved outside an open message disclosure. Escape still
      // dismisses that surface first, rather than closing the entire drawer.
      const menu = state.els.panel.querySelector('.personal-assistant-message__menu[open]');
      if (menu) {
        event.preventDefault();
        menu.open = false;
        menu.querySelector('summary')?.focus();
        return;
      }
      close();
    }
  });
  window.addEventListener('personal-assistant:status', event => {
    if (event.detail?.personalAssistant) applyPersonalAssistant(event.detail.personalAssistant);
  });
  window.addEventListener('resize', syncPanelViewport);
  // Resize/reflow can leave an already-focused folder control outside the one
  // scroll viewport. Recover only this feature's keyboard position; do not
  // interfere with existing message, saved-draft or memory focus lifecycles.
  const scroll = document.getElementById('personalAssistantScroll');
  if (scroll && typeof ResizeObserver !== 'undefined') {
    new ResizeObserver(() => requestAnimationFrame(revealFocusedControl)).observe(scroll);
  }
  if (typeof ResizeObserver !== 'undefined') {
    new ResizeObserver(syncPanelViewport).observe(panel);
  }
  // CSS zoom changes rendered geometry without a content-box resize. Native
  // zoom uses the resize listener; this also covers app/style reflow honestly.
  if (typeof MutationObserver !== 'undefined') {
    const observer = new MutationObserver(syncPanelViewport);
    for (const root of [document.documentElement, document.body]) {
      if (root) observer.observe(root, { attributes: true, attributeFilter: ['style', 'class'] });
    }
  }
  syncPanelViewport();
  renderHeader();
  // `/?panel=today` opens the drawer on Home. Wait for the server-owned
  // identity first; a query string must not make an unhired assistant appear
  // hired.
  const requestedOpen =
    Boolean(state.els.todayBanner) &&
    new URLSearchParams(window.location.search).get('panel') === 'today';
  void refresh().then(() => {
    if (!(requestedOpen || tabValue(PANEL_OPEN_KEY) === '1') || !open(launcher)) return;
    const url = new URL(window.location.href);
    url.searchParams.delete('panel');
    window.history.replaceState(null, '', url.pathname + url.search + url.hash);
  });
}

const api = {
  init,
  open,
  close,
  prefill,
  suggestReply,
  restoreDraft,
  refresh,
  applyPersonalAssistant,
  setToday,
  setFolderBusy,
  syncViewport: syncPanelViewport,
  refreshWorkspaceContext,
  _state: state
};
if (typeof window !== 'undefined') window.PersonalAssistantPanel = api;
if (typeof document !== 'undefined') {
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', init);
  else init();
}
