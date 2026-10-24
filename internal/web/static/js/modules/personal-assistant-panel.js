import {
  collectCurrentWorkspaceContext,
  workspaceContextLabel
} from './personal-assistant-workspace-context.js';
import {
  RECIPIENT_WORKSPACE_ENTRY,
  createRecipientMenu,
  entryAgentPlaceholder,
  entrySessionRows,
  menuKeyIntent,
  nextMenuIndex,
  recipientButtonLabel,
  recipientMenuRows,
  switchLockMessage
} from './personal-assistant-recipient.js';
import { mountFirstLookReports } from './first-look-report.js';

const PANEL_DRAFT_KEY = 'ori.personalAssistant.panelDraft';
const PANEL_OPEN_KEY = 'ori.personalAssistant.panelOpen';
export const LEGACY_ASSISTANT_HANDOFF_KEY = 'ori-guide-handoff';

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
  const state =
    typeof personalAssistant?.state === 'string' ? personalAssistant.state : 'unavailable';
  const known = [
    'needs_hire',
    'hiring',
    'needs_hq',
    'provisioning_hq',
    'active',
    'paused',
    'repair_needed'
  ].includes(state);
  const available = state === 'active' || state === 'paused';
  const named = known && !['needs_hire', 'hiring'].includes(state);
  const name =
    (named && typeof personalAssistant?.display_name === 'string'
      ? personalAssistant.display_name.trim()
      : '') || 'Personal assistant';
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
      ? 'Ask or describe what you want help with'
      : state === 'needs_hq' || state === 'provisioning_hq'
        ? `Build ${name}’s Personal HQ before sending work`
        : 'Finish personal assistant setup before sending work'
  };
}

/**
 * What the launcher, header and composer say for the drawer's current
 * recipient. `view` is the hired assistant's (personalAssistantPanelView);
 * `resolution` is the recipient resolver's answer for this page. The launcher
 * stays visible while the workspace's entry agent can be addressed, even when
 * the hired assistant is not set up.
 */
export function recipientIdentityView(view, resolution) {
  const recipient = resolution?.current;
  const entryOption = (resolution?.options || []).find(
    option => option.kind === RECIPIENT_WORKSPACE_ENTRY
  );
  const entryReachable = Boolean(entryOption && !entryOption.disabled && !entryOption.pending);
  // The hired assistant's status has not answered yet, so this page may be its
  // Personal HQ: nobody is named or offered, as before the status was known.
  if (recipient?.kind === RECIPIENT_WORKSPACE_ENTRY && recipient.pending) {
    return {
      personal: true,
      kind: 'personal',
      name: view.name,
      role: 'Personal Assistant',
      subline: '',
      placeholder: view.placeholder,
      available: false,
      launcherVisible: false,
      openable: false,
      appearance: null
    };
  }
  if (recipient?.kind === RECIPIENT_WORKSPACE_ENTRY) {
    return {
      personal: false,
      kind: RECIPIENT_WORKSPACE_ENTRY,
      name: recipient.name || 'Commander',
      role: recipient.subline,
      subline: recipient.subline,
      placeholder: entryAgentPlaceholder(recipient),
      available: recipient.available === true,
      // The launcher appears once it can name the agent; the drawer itself may
      // already open (a reload of an open drawer) and says it is still looking.
      launcherVisible: recipient.loading !== true,
      openable: true,
      appearance: recipient.appearance || null
    };
  }
  return {
    personal: true,
    kind: 'personal',
    name: view.name,
    role: 'Personal Assistant',
    subline: '',
    placeholder: view.placeholder,
    available: view.available,
    launcherVisible: view.visible || entryReachable,
    openable: view.visible || entryReachable,
    appearance: null
  };
}

/** The strip above the entry agent's conversation: its session, and New. */
export function entrySessionStripView({ recipient, session, hasMessages } = {}) {
  const id = String(session?.id || '');
  return {
    visible: recipient?.kind === RECIPIENT_WORKSPACE_ENTRY && recipient.pending !== true,
    title: id ? String(session?.title || '').trim() || 'Conversation' : 'New conversation',
    // A new conversation that has nothing in it yet has nothing to leave.
    newDisabled: !id && hasMessages !== true
  };
}

export const ENTRY_HYDRATION_FAILED =
  'Could not load this conversation. Your next message starts where the server left off.';

export function boundedAssistantHandoff(value) {
  return Array.from(String(value || '').trim())
    .slice(0, HANDOFF_LIMIT)
    .join('');
}

export function readLegacyAssistantHandoff(storage) {
  try {
    const original = storage.getItem(LEGACY_ASSISTANT_HANDOFF_KEY);
    const text = boundedAssistantHandoff(original);
    return text ? { original, text } : null;
  } catch (_) {
    return null;
  }
}

export function clearLegacyAssistantHandoff(storage, original) {
  try {
    // Do not delete a different request another consumer published meanwhile.
    const current = storage.getItem(LEGACY_ASSISTANT_HANDOFF_KEY);
    if (current !== null && current !== original) return false;
    storage.removeItem(LEGACY_ASSISTANT_HANDOFF_KEY);
    return true;
  } catch (_) {
    return false;
  }
}

export function canSubmitAssistantWork({ available, pending, busy, text }) {
  return (
    available === true && pending !== true && busy !== true && String(text || '').trim().length > 0
  );
}

/**
 * The composer's placeholder. With a folder attached it says what an empty Send
 * asks for; an assistant that cannot take work says why instead.
 */
export function composerPlaceholder(view, folderPlaceholder = '') {
  return (view?.available && String(folderPlaceholder || '')) || view?.placeholder || '';
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

// A console deep link owns its panel/focus. Restoring this drawer must neither
// consume the other owner's query nor hide the requested backup console.
export function assistantPanelOpenIntent(search, hasToday) {
  const panel = new URLSearchParams(search).get('panel');
  return {
    requestedOpen: Boolean(hasToday) && panel === 'today',
    restoreAllowed: panel !== 'backup-recovery'
  };
}

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
  if (!count && needs?.health?.status === 'partial') return unknown;
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

// The desktop sheet's width belongs to the user (FR2–FR5). It is kept in this
// browser, not on the server, and every number here is in CSS pixels of the
// zoom-corrected viewport that syncPanelViewport measures.
export const DRAWER_WIDTH_KEY = 'ori.assistant.drawer.width';
export const DRAWER_EXPANDED_KEY = 'ori.assistant.drawer.expanded';
export const DRAWER_DEFAULT_WIDTH = 640;
const DRAWER_MIN_WIDTH = 560;
const DRAWER_MAX_WIDTH = 960;
const DRAWER_EXPANDED_MAX_WIDTH = 1180;
// At or below this viewport width the drawer is the phone bottom sheet.
export const DRAWER_SHEET_BREAKPOINT = 760;

/** How far right of the page edge the sheet may reach: the page keeps this. */
function drawerRoom(viewportWidth) {
  const width = Number(viewportWidth) || 0;
  return Math.floor(width - Math.min(180, 0.2 * width) - 20);
}

/**
 * The widths the handle can reach on this viewport. The page keeps at least
 * min(180px, 20% of the viewport) on the left, so on a narrow desktop the top
 * of the range can fall below 560; the bottom then follows it down.
 */
export function drawerWidthBounds(viewportWidth) {
  const max = Math.max(0, Math.min(DRAWER_MAX_WIDTH, drawerRoom(viewportWidth)));
  return { min: Math.min(DRAWER_MIN_WIDTH, max), max };
}

/** A remembered width fitted to this viewport. It is clamped, never dropped. */
export function clampDrawerWidth(width, viewportWidth) {
  const { min, max } = drawerWidthBounds(viewportWidth);
  const value = Number.isFinite(width) ? Math.round(width) : DRAWER_DEFAULT_WIDTH;
  return Math.min(max, Math.max(min, value));
}

/** Expand, and Tree + Chat: the widest sheet the page allows. */
export function expandedDrawerWidth(viewportWidth) {
  return Math.max(0, Math.min(DRAWER_EXPANDED_MAX_WIDTH, drawerRoom(viewportWidth)));
}

/**
 * The saved width and Expand choice. Storage may be missing, may throw, or may
 * hold something else under the key; each of those reads as the default.
 */
export function readDrawerPrefs(storage) {
  const prefs = { width: DRAWER_DEFAULT_WIDTH, expanded: false };
  try {
    const width = String(storage?.getItem?.(DRAWER_WIDTH_KEY) ?? '').trim();
    if (/^\d{1,5}$/.test(width) && Number(width) > 0) prefs.width = Number(width);
    prefs.expanded = storage?.getItem?.(DRAWER_EXPANDED_KEY) === '1';
  } catch (_) {
    /* Storage may be disabled. */
  }
  return prefs;
}

/** Saves the width as a whole number and Expand as "1" or "0". */
export function writeDrawerPrefs(storage, prefs = {}) {
  try {
    if (Number.isFinite(prefs.width) && prefs.width > 0)
      storage.setItem(DRAWER_WIDTH_KEY, String(Math.round(prefs.width)));
    if (typeof prefs.expanded === 'boolean')
      storage.setItem(DRAWER_EXPANDED_KEY, prefs.expanded ? '1' : '0');
    return true;
  } catch (_) {
    return false;
  }
}

/**
 * The handle's next width for a key, or null when the key is not one it uses.
 * The handle sits on the sheet's left edge, so Left moves it outwards and
 * widens the sheet.
 */
export function drawerWidthForKey(key, { width, min, max, shift = false }) {
  const step = shift ? 64 : 16;
  const next = {
    ArrowLeft: width + step,
    ArrowRight: width - step,
    Home: min,
    End: max
  }[key];
  return next === undefined ? null : Math.min(max, Math.max(min, next));
}

/**
 * What the sheet looks like now. `exploring` is Tree + Chat, which takes the
 * expanded width while it is open without changing the saved choice (FR6);
 * Expand steps aside meanwhile, since there is nothing for it to change.
 * Off the desktop sheet there is no handle and no Expand (FR9).
 */
export function drawerLayoutView({ viewportWidth, prefs, exploring = false }) {
  const sheet = Number(viewportWidth) > DRAWER_SHEET_BREAKPOINT;
  const expanded = prefs?.expanded === true;
  const wide = expanded || exploring === true;
  const handleWidth = clampDrawerWidth(prefs?.width, viewportWidth);
  return {
    sheet,
    bounds: drawerWidthBounds(viewportWidth),
    width: wide ? expandedDrawerWidth(viewportWidth) : handleWidth,
    // The handle's own value: the remembered width, fitted to this viewport.
    handleWidth,
    handleVisible: sheet,
    handleInert: wide,
    expandVisible: sheet && exploring !== true,
    expanded,
    expandLabel: expanded ? 'Shrink' : 'Expand'
  };
}

/**
 * The header's conversation menu (FR21). The hired assistant's has New,
 * Continue… and the check-in row; the entry agent's has New only, for its
 * workspace session. The button shows while there is a conversation to name.
 */
export function conversationMenuView({ personal = true, available = false, entry = null } = {}) {
  if (personal !== false) {
    const shown = available === true;
    return {
      visible: shown,
      personalTitle: shown,
      personalItems: shown,
      entryTitle: false,
      entryNew: false,
      checkIn: shown
    };
  }
  const shown = entry?.visible === true;
  return {
    visible: shown,
    personalTitle: false,
    personalItems: false,
    entryTitle: shown,
    entryNew: shown,
    checkIn: false
  };
}

// The text box starts two rows tall and grows with what is typed (FR23).
export const COMPOSER_MIN_HEIGHT = 56;
export const COMPOSER_MAX_HEIGHT = 160;

/** The text box's height for its content, where the browser cannot size it. */
export function composerHeight(scrollHeight) {
  const height = Math.ceil(Number(scrollHeight) || 0);
  return Math.min(COMPOSER_MAX_HEIGHT, Math.max(COMPOSER_MIN_HEIGHT, height));
}

/** The items a menu's arrow keys move through: shown, and not disabled. */
export function menuItemsIn(menu) {
  return Array.from(menu?.querySelectorAll?.('[role="menuitem"]') || []).filter(
    item => !item.hidden && !item.disabled && item.getClientRects().length > 0
  );
}

/**
 * A button that opens a menu of existing controls: the attach "+" and the
 * header's conversation menu (FR24). It follows the recipient menu's keys:
 * arrows move, Enter or Space chooses, Escape closes only the menu and
 * returns focus to the button, Tab and a click outside close it. `items` are
 * the controls it holds; they keep their own ids, handlers and hidden and
 * disabled rules. Each is a menu item only while it is inside the menu (a
 * module may move one out and back), and a module that returns focus to one
 * while the menu is closed reaches the button instead.
 */
export function createActionMenu({ button, menu, doc, items = [] }) {
  if (!button || !menu || !doc) return null;
  let open = false;

  function syncRoles() {
    for (const item of items) {
      if (menu.contains(item)) {
        item.setAttribute('role', 'menuitem');
        item.tabIndex = -1;
      } else {
        item.removeAttribute('role');
        item.removeAttribute('tabindex');
      }
    }
  }

  for (const item of items) {
    const focus = item.focus;
    item.focus = function (options) {
      if (!open && menu.contains(item)) button.focus(options);
      else focus.call(item, options);
    };
  }

  function show(at = 'first') {
    syncRoles();
    open = true;
    menu.hidden = false;
    button.setAttribute('aria-expanded', 'true');
    const list = menuItemsIn(menu);
    const usable = list.filter(item => item.getAttribute('aria-disabled') !== 'true');
    (at === 'last' ? list.at(-1) : usable[0] || list[0])?.focus();
  }

  function close({ focus = false } = {}) {
    if (!open) return;
    const inside = menu.contains(doc.activeElement);
    open = false;
    menu.hidden = true;
    button.setAttribute('aria-expanded', 'false');
    if (focus || inside) button.focus();
  }

  button.addEventListener('click', event => {
    event.preventDefault();
    if (open) close({ focus: true });
    else show();
  });
  button.addEventListener('keydown', event => {
    if (event.key === 'Escape' && open) {
      event.preventDefault();
      event.stopPropagation();
      close({ focus: true });
    } else if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
      event.preventDefault();
      show(event.key === 'ArrowUp' ? 'last' : 'first');
    }
  });
  menu.addEventListener('keydown', event => {
    const intent = menuKeyIntent(event.key);
    if (!intent) return;
    if (intent === 'close') {
      // Escape closes this menu, never the drawer around it.
      event.preventDefault();
      event.stopPropagation();
      close({ focus: true });
      return;
    }
    if (intent === 'tab') {
      close();
      return;
    }
    const list = menuItemsIn(menu);
    const index = list.indexOf(doc.activeElement);
    if (intent === 'choose') {
      event.preventDefault();
      if (list[index]?.getAttribute('aria-disabled') !== 'true') list[index]?.click();
      return;
    }
    event.preventDefault();
    list[nextMenuIndex(index, list.length, intent)]?.focus();
  });
  // After an item's own handler has run: the menu closes, and focus that the
  // handler left on the item goes back to the button.
  menu.addEventListener('click', event => {
    const item = event.target?.closest?.('[role="menuitem"]');
    if (item && item.getAttribute('aria-disabled') !== 'true') close();
  });
  doc.addEventListener('click', event => {
    if (open && !menu.contains(event.target) && !button.contains(event.target)) close();
  });
  if (typeof MutationObserver !== 'undefined')
    new MutationObserver(syncRoles).observe(menu, { childList: true, subtree: true });
  syncRoles();

  return { isOpen: () => open, show, close };
}

const state = {
  view: personalAssistantPanelView(null),
  personalAssistant: null,
  today: null,
  pending: false,
  open: false,
  draft: '',
  workspaceSequence: 0,
  legacyHandoff: null,
  suggestionNotice: '',
  workActivityOnly: false,
  relationshipSequence: 0,
  lastTrigger: null,
  // True while Home's folder flow is waiting for a folder or exploring one.
  folderBusy: false,
  // The recipient picker's menu, once the drawer is mounted.
  recipientMenu: null,
  // The header's conversation menu and the composer's attach menu.
  conversationMenu: null,
  attachMenu: null,
  // The last "N need you" line read for this page, shown only with the hired
  // assistant selected.
  needsLine: { visible: false, text: '' },
  // The desktop sheet: the saved width and Expand choice, the layout last
  // applied, and the drag in progress, if any.
  drawer: { width: DRAWER_DEFAULT_WIDTH, expanded: false },
  drawerLayout: null,
  drag: null,
  viewport: { width: 0, scale: 1 },
  els: null
};

function setStatus(message) {
  if (state.els?.status) state.els.status.textContent = String(message || '');
}

function browserStorage() {
  try {
    return window.localStorage;
  } catch (_) {
    return null;
  }
}

/** Applies the sheet width, the handle's range and the Expand toggle's state. */
function applyDrawerLayout() {
  const { panel, resize, expand } = state.els || {};
  if (!panel) return;
  const view = drawerLayoutView({
    viewportWidth: state.viewport.width,
    prefs: state.drawer,
    exploring: panel.classList.contains('personal-assistant-panel--exploring')
  });
  state.drawerLayout = view;
  const width = `${view.width}px`;
  if (panel.style.getPropertyValue('--pa-drawer-width') !== width)
    panel.style.setProperty('--pa-drawer-width', width);
  if (resize) {
    resize.hidden = !view.handleVisible;
    resize.inert = view.handleInert;
    resize.setAttribute('aria-valuemin', String(view.bounds.min));
    resize.setAttribute('aria-valuemax', String(view.bounds.max));
    resize.setAttribute('aria-valuenow', String(view.handleWidth));
  }
  if (expand) {
    expand.hidden = !view.expandVisible;
    expand.setAttribute('aria-pressed', String(view.expanded));
    expand.setAttribute('aria-label', view.expandLabel);
    expand.title = view.expandLabel;
    const icon = expand.querySelector('i');
    if (icon)
      icon.className = view.expanded ? 'bi bi-arrows-angle-contract' : 'bi bi-arrows-angle-expand';
  }
}

function startDrawerResize(event) {
  const view = state.drawerLayout;
  if (event.button !== 0 || !view?.sheet || view.handleInert) return;
  event.preventDefault();
  state.drag = {
    pointerId: event.pointerId,
    x: event.clientX,
    width: view.handleWidth,
    scale: state.viewport.scale
  };
  state.els.resize.setPointerCapture?.(event.pointerId);
  document.documentElement.classList.add('personal-assistant-resizing');
}

function moveDrawerResize(event) {
  const drag = state.drag;
  if (!drag || event.pointerId !== drag.pointerId) return;
  // The handle is the left edge: moving it left widens the sheet.
  state.drawer.width = clampDrawerWidth(
    drag.width + (drag.x - event.clientX) / drag.scale,
    state.viewport.width
  );
  applyDrawerLayout();
}

function endDrawerResize(event) {
  const drag = state.drag;
  if (!drag || event.pointerId !== drag.pointerId) return;
  state.drag = null;
  if (state.els.resize.hasPointerCapture?.(event.pointerId))
    state.els.resize.releasePointerCapture(event.pointerId);
  document.documentElement.classList.remove('personal-assistant-resizing');
  writeDrawerPrefs(browserStorage(), { width: state.drawer.width });
}

function keyDrawerResize(event) {
  const view = state.drawerLayout;
  if (!view?.sheet || view.handleInert) return;
  const next = drawerWidthForKey(event.key, {
    width: view.handleWidth,
    ...view.bounds,
    shift: event.shiftKey
  });
  if (next === null) return;
  event.preventDefault();
  state.drawer.width = next;
  applyDrawerLayout();
  writeDrawerPrefs(browserStorage(), { width: next });
}

function toggleDrawerExpanded() {
  if (!state.drawerLayout?.expandVisible) return;
  state.drawer.expanded = !state.drawer.expanded;
  writeDrawerPrefs(browserStorage(), { expanded: state.drawer.expanded });
  applyDrawerLayout();
}

function syncPanelViewport() {
  if (!state.els?.root) return;
  const panel = state.els.panel;
  const scale = panel?.offsetWidth
    ? Math.max(1, Math.round((panel.getBoundingClientRect().width / panel.offsetWidth) * 100) / 100)
    : 1;
  state.viewport = { width: window.innerWidth / scale, scale };
  state.els.root.style.setProperty('--pa-viewport-width', `${window.innerWidth / scale}px`);
  state.els.root.style.setProperty('--pa-viewport-height', `${window.innerHeight / scale}px`);
  applyDrawerLayout();
  panel?.classList.toggle(
    'personal-assistant-panel--explorer-narrow',
    window.innerWidth / scale <= 760
  );
  const navbar = document.querySelector?.('nav.navbar');
  const bottom = navbar?.getBoundingClientRect?.().bottom;
  const inlineToolbar =
    window.innerWidth / scale >= 600 && (window.innerHeight - (bottom || 0)) / scale <= 520;
  panel?.classList.toggle('personal-assistant-panel--inline-toolbar', inlineToolbar);
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

function recipients() {
  return window.PersonalAssistantRecipient || null;
}

function renderEntrySessionBar() {
  const els = state.els;
  if (!els) return;
  const view = entrySessionStripView({
    recipient: recipients()?.current?.(),
    session: recipients()?.session?.(),
    hasMessages: window.OriAskRouting?.getState?.().hasConversation === true
  });
  if (els.entrySessionTitle) els.entrySessionTitle.textContent = view.title;
  if (els.entrySessionNew) els.entrySessionNew.disabled = view.newDisabled;
  renderConversationMenu(view);
}

/**
 * Which conversation the header names, and what its menu holds, for the
 * current recipient. personal-assistant-conversation.js keeps the hired
 * assistant's title and New's disabled state; this decides what shows.
 */
function renderConversationMenu(entry) {
  const els = state.els;
  if (!els?.conversationMenu) return;
  const who = identity();
  const view = conversationMenuView({
    personal: who.personal,
    available: who.personal && state.view.available === true,
    entry
  });
  els.conversationMenu.hidden = !view.visible;
  const show = (el, shown) => {
    if (el) el.hidden = !shown;
  };
  show(els.conversationTitle, view.personalTitle);
  show(els.conversationNew, view.personalItems);
  show(els.conversationContinue, view.personalItems);
  show(els.entrySessionTitle, view.entryTitle);
  show(els.entrySessionNew, view.entryNew);
  show(els.checkIn, view.checkIn);
  if (!view.visible) state.conversationMenu?.close();
}

/** Grows the text box with its content where CSS field-sizing cannot. */
function syncComposerHeight() {
  const input = state.els?.input;
  if (!input || globalThis.CSS?.supports?.('field-sizing', 'content') === true) return;
  input.style.height = 'auto';
  input.style.height = `${composerHeight(input.scrollHeight)}px`;
}

// Which entry agent the thread was last loaded for, and a ticket so a slow
// load never lands after the user switched recipient or workspace.
const entryHydration = { sequence: 0, target: '' };

function entryTarget(recipient) {
  return `${recipient?.workspaceId || ''}\n${recipient?.name || ''}`;
}

/**
 * Loads the entry agent's current session into the drawer (FR21): user and
 * assistant rows, oldest first, scrolled to the latest. Once per entry agent,
 * unless `force` (opening the drawer, switching to it). A failure leaves one
 * status line and never blocks sending.
 */
async function hydrateEntryConversation({ force = false } = {}) {
  const ctl = recipients();
  const current = ctl?.current?.();
  if (current?.kind !== RECIPIENT_WORKSPACE_ENTRY || !current.available) {
    entryHydration.target = '';
    return false;
  }
  const target = entryTarget(current);
  if (!force && target === entryHydration.target) return false;
  // A reply in flight owns the thread; its own rows are already the latest.
  if (window.OriAskRouting?.getState?.().busy === true) return false;
  entryHydration.target = target;
  const ticket = ++entryHydration.sequence;
  let loaded;
  try {
    loaded = await ctl.loadSession();
  } catch (_) {
    loaded = { session: null, error: true };
  }
  const now = ctl.current?.();
  if (
    ticket !== entryHydration.sequence ||
    loaded.stale ||
    now?.kind !== RECIPIENT_WORKSPACE_ENTRY ||
    entryTarget(now) !== target
  )
    return false;
  if (loaded.error) {
    setStatus(ENTRY_HYDRATION_FAILED);
    renderEntrySessionBar();
    return false;
  }
  if (!window.OriAskRouting?.resetConversation?.()) return false;
  const transcript = window.PersonalAssistantTranscript;
  transcript?.beginHydration?.();
  try {
    for (const row of entrySessionRows(loaded.session)) {
      window.OriAskRouting.appendMessage(
        row.role,
        row.content,
        row.role === 'assistant'
          ? { author: { name: now.name, appearance: now.appearance } }
          : undefined
      );
    }
  } finally {
    transcript?.endHydration?.();
  }
  if (state.els?.status?.textContent === ENTRY_HYDRATION_FAILED) setStatus('');
  renderEntrySessionBar();
  return true;
}

/**
 * With two recipients on offer the name becomes the menu button (FR8). The
 * one name element moves into the button and back, so the heading's text is
 * always just the name.
 */
function renderPicker(who) {
  const els = state.els;
  if (!els?.recipientButton || !els.name) return;
  const resolution = recipients()?.resolution?.();
  const visible = resolution?.pickerVisible === true && resolution.current?.pending !== true;
  if (visible && els.name.parentElement !== els.recipientButton)
    els.recipientButton.prepend(els.name);
  if (!visible && els.name.parentElement !== els.title) els.title.prepend(els.name);
  els.recipientButton.hidden = !visible;
  els.recipientButton.setAttribute(
    'aria-label',
    recipientButtonLabel(recipients()?.current?.() || { name: who.name, role: who.role })
  );
  if (!visible) state.recipientMenu?.close?.();
  state.recipientMenu?.refresh?.();
}

/** Why switching recipient is refused right now (FR14, FR15), or ''. */
function switchLock() {
  const work = window.OriAskRouting?.getState?.() || {};
  return switchLockMessage(recipients()?.current?.(), {
    busy: work.busy === true || state.pending,
    hasPendingConfirmation: work.hasPendingConfirmation === true,
    hasPlanning: work.hasPlanning === true,
    loading: window.PersonalAssistantConversation?.isLoading?.() === true
  });
}

/** Where a vacant Commander is filled: the workspace's Roles, in Details. */
function rolesHref() {
  const slug = routeContext().workspace_slug;
  return slug ? `/workspaces/${encodeURIComponent(slug)}?mode=details` : '';
}

function menuRows() {
  return recipientMenuRows(recipients()?.resolution?.(), {
    locked: Boolean(switchLock()),
    fillHref: rolesHref()
  });
}

function refuseRecipient(row) {
  const lock = switchLock();
  if (lock) setStatus(lock);
  else if (row.fillHref)
    setStatus('This workspace has no Commander yet. Fill the role to talk to one.');
  else if (row.kind === RECIPIENT_WORKSPACE_ENTRY)
    setStatus('This workspace’s Commander could not be loaded. Reload the page to try again.');
}

/**
 * Switches who the drawer talks to (FR13, FR16): clears the thread, loads the
 * other recipient's conversation, and leaves the composer exactly as it is.
 * Nothing is sent and no session is created by the switch itself.
 */
function chooseRecipient(kind) {
  const ctl = recipients();
  const resolution = ctl?.resolution?.();
  if (!resolution || resolution.current?.kind === kind) return false;
  const target = (resolution.options || []).find(option => option.kind === kind);
  if (!target || target.disabled) return false;
  const lock = switchLock();
  if (lock || !window.OriAskRouting?.resetConversation?.()) {
    setStatus(lock || switchLockMessage(resolution.current, { busy: true }));
    return false;
  }
  // A review belongs to the conversation it was opened in.
  window.PersonalAssistantDrafts?.close?.();
  window.PersonalAssistantMemory?.close?.();
  window.PersonalAssistantFolderContext?.close?.();
  setStatus('');
  entryHydration.target = '';
  // The new recipient's conversation loads from the recipient event: the
  // hired assistant's through its own module, the entry agent's here.
  ctl.pick(kind);
  return true;
}

/** New, with the entry agent selected: forget this tab's session (FR22). */
function startNewEntryConversation() {
  const who = identity();
  if (who.personal) return false;
  if (!window.OriAskRouting?.resetConversation?.()) {
    setStatus(`Wait for ${who.name} to reply before starting a new conversation.`);
    return false;
  }
  recipients()?.forgetSession?.();
  setStatus('');
  renderEntrySessionBar();
  state.els?.input?.focus();
  return true;
}

/** Who the drawer is addressed to now, as the launcher and header show it. */
function identity() {
  return recipientIdentityView(state.view, recipients()?.resolution?.() || null);
}

function renderIdentity() {
  const { els, view, personalAssistant } = state;
  if (!els) return;
  const who = identity();
  els.launcher.hidden = !who.launcherVisible;
  els.launcherName.textContent = who.name;
  if (els.launcherRole) els.launcherRole.textContent = who.role;
  const title = state.workActivityOnly && !who.openable ? 'Work activity' : who.name;
  if (els.name) els.name.textContent = title;
  else els.title.textContent = title;
  renderPicker(who);
  // An attached folder belongs to the hired assistant's conversation only.
  els.input.placeholder = who.personal
    ? composerPlaceholder(who, state.folderPlaceholder)
    : who.placeholder;
  els.input.disabled = !who.available;
  els.send.disabled = !who.available || state.pending;
  if (els.modelSetup)
    els.modelSetup.hidden = !(
      who.personal &&
      view.available &&
      personalAssistant?.availability?.model?.available === false
    );
  const avatar = who.personal
    ? assistantAvatarMarkup(view.name, personalAssistant?.appearance)
    : assistantAvatarMarkup(who.name, who.appearance);
  els.launcherAvatar.innerHTML = avatar;
  els.panelAvatar.innerHTML = avatar;
  els.panelAvatar.hidden = !who.openable;
  els.panel.dataset.relationshipState = view.state;
  els.panel.dataset.recipient = who.kind;
  renderChip();
  renderCheckIn();
  renderEntrySessionBar();
  renderNeedsLine();
  // The hired assistant's setup notices describe it, not a workspace agent.
  if (!who.personal) return;
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
    // Folder context belongs to the hired assistant's conversation only.
    available: state.view.available && recipientChromeView(identity()).attachments,
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

/**
 * The line under the name: the entry agent's "Commander · workspace", or the
 * hired assistant's next check-in.
 */
function renderCheckIn() {
  const els = state.els;
  if (!els?.checkIn) return;
  const who = identity();
  if (!who.personal) {
    els.checkIn.replaceChildren(who.subline);
    return;
  }
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

/** The header's check-in line and More links, from the Today the drawer has. */
function renderHeader() {
  const els = state.els;
  if (!els) return;
  renderCheckIn();
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

/**
 * The hired assistant's own chrome — the "N need you" line, Add folder, Add
 * link, Watch Ori and its New/Continue strip — shows only while it is the
 * recipient; the entry agent has its session strip instead (FR17). The
 * page's "Context · …" line describes the page and stays for both (FR18).
 */
export function recipientChromeView(who, needsLine = { visible: false }) {
  const personal = who?.personal !== false;
  return {
    needsLine: personal && needsLine.visible === true,
    attachments: personal,
    personalConversation: personal,
    entrySession: !personal,
    context: true
  };
}

function renderNeedsLine(line) {
  const els = state.els;
  if (line) state.needsLine = line;
  if (!els?.needsLine) return;
  els.needsLine.hidden = !recipientChromeView(identity(), state.needsLine).needsLine;
  if (els.needsLineText) els.needsLineText.textContent = state.needsLine.text;
  // A hired agent's report is drawn in full under the line, with its actions,
  // while the hired assistant is the one the drawer speaks for.
  const reports = document.getElementById('personalAssistantNeedsReports');
  if (reports) {
    const personal = recipientChromeView(identity(), { visible: true }).needsLine;
    mountFirstLookReports(reports, personal ? state.today?.needs_you?.items : [], {
      onDone: () => void readTodayForThisPage()
    });
  }
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
  // Compatibility name: markup permanently owns this node from initial render.
  // Never reparent on relationship refresh or put confirmations in Help.
  const activity = document.getElementById('homeAssistantThinkingModal');
  if (activity && state.els?.activityMount) activity.hidden = false;
}

function applyPersonalAssistant(personalAssistant) {
  state.relationshipSequence += 1;
  state.personalAssistant = personalAssistant || null;
  state.view = personalAssistantPanelView(personalAssistant);
  // The recipient resolver needs the hired assistant's identity and its
  // Personal HQ id (FR5 is decided by that id, never by a name).
  recipients()?.setPersonal?.({
    name: state.view.name,
    available: state.view.available,
    state: state.view.state,
    appearance: personalAssistant?.appearance || null,
    // Unknown when the status could not be read: the last known HQ stands.
    hqWorkspaceId: personalAssistant ? personalAssistant.hq_workspace_id || '' : undefined
  });
  if (window.OriGuide?.setHelpOnly) {
    window.OriGuide.setHelpOnly({
      available: state.view.available,
      assistantName: state.view.name,
      needsHQ: state.view.needsHQ,
      state: state.view.known ? state.view.state : 'unavailable'
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
  const sequence = ++state.relationshipSequence;
  try {
    const response = await fetch(STATUS_ENDPOINT, { headers: { Accept: 'application/json' } });
    if (!response.ok) throw new Error(`status ${response.status}`);
    const payload = await response.json();
    if (sequence !== state.relationshipSequence) return state.view;
    return applyPersonalAssistant(payload?.personal_assistant || null);
  } catch (_) {
    if (sequence !== state.relationshipSequence) return state.view;
    // A failed refresh is not authority to keep accepting work as a cached identity.
    // Drafts and submitted activity remain owned by this host.
    applyPersonalAssistant(null);
    setStatus(
      'Personal assistant status is unavailable. Retry assistant status from Help. Nothing was sent.'
    );
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
  try {
    document.dispatchEvent(new CustomEvent('personal-assistant:closed'));
  } catch (_) {
    // Listeners only act on what the drawer no longer covers; none is required.
  }
}

/** The first control in the drawer, in reading order, that can take focus. */
function firstControl() {
  const candidates = state.els.panel.querySelectorAll(
    'a[href], button:not([disabled]), summary, input:not([disabled]), textarea:not([disabled]), select:not([disabled])'
  );
  return Array.from(candidates).find(el => el.getClientRects().length > 0) || null;
}

function focusOnOpen() {
  if (assistantOpenFocusTarget({ available: identity().available }) === 'composer')
    state.els.input.focus();
  else firstControl()?.focus();
}

/**
 * Opens the drawer. There is one view, so there is nothing to select; a caller
 * that is about to put focus somewhere of its own passes `{ focus: false }`.
 */
function open(trigger, options = {}) {
  if (!state.els || (!identity().openable && options.workActivity !== true)) return false;
  state.workActivityOnly = options.workActivity === true;
  if (window.OriGuide?.close) window.OriGuide.close({ restoreFocus: false });
  renderIdentity();
  moveSharedWorkActivity();
  syncPanelViewport();
  state.open = true;
  saveTabValue(PANEL_OPEN_KEY, '1');
  state.lastTrigger = trigger || document.activeElement;
  state.els.panel.hidden = false;
  state.els.launcher.setAttribute('aria-expanded', 'true');
  state.els.input.value = state.draft;
  syncComposerHeight();
  if (options.focus !== false) focusOnOpen();
  // Rename/pause/repair changes are server-owned. Refresh on every open rather
  // than trusting the hire-time name or local storage. The workspace's
  // Commander can change on the Roles view, so it is read again too.
  void refresh();
  void recipients()?.refresh?.();
  void hydrateEntryConversation({ force: true });
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
    available: identity().available,
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
  state.suggestionNotice = result.notice;
  setStatus(result.notice);
  return result.accepted;
}

function renderLegacyHandoff() {
  const els = state.els;
  if (!els?.legacyHandoff) return;
  els.legacyHandoff.hidden = !state.legacyHandoff;
  if (els.legacyText) els.legacyText.textContent = state.legacyHandoff?.text || '';
}

function dismissLegacyHandoff() {
  if (!state.legacyHandoff) return false;
  let cleared = false;
  try {
    cleared = clearLegacyAssistantHandoff(window.sessionStorage, state.legacyHandoff.original);
  } catch (_) {
    /* Disabled storage. */
  }
  if (!cleared) {
    setStatus(
      'The saved request could not be cleared from this tab. It is kept for recovery; nothing was sent.'
    );
    return false;
  }
  state.legacyHandoff = null;
  renderLegacyHandoff();
  return true;
}

function recoverLegacyHandoff() {
  if (!state.legacyHandoff || !suggestReply(state.legacyHandoff.text)) return false;
  // Safe acceptance is an editable, unsent draft, never execution approval.
  return dismissLegacyHandoff();
}

function prefill(text) {
  if (!identity().available) return false;
  if (!open(state.els?.launcher)) return false;
  const bounded = boundedAssistantHandoff(text);
  state.draft = bounded;
  state.els.input.value = bounded;
  state.els.input.dispatchEvent(new Event('input', { bubbles: true }));
  state.els.input.focus();
  setStatus(`Review this request, then press Send to confirm it goes to ${identity().name}.`);
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

/** The attached folder's own placeholder, or '' for the assistant's default. */
function setComposerPlaceholder(text) {
  const next = String(text || '');
  if (state.folderPlaceholder === next) return;
  state.folderPlaceholder = next;
  renderIdentity();
}

/**
 * Takes a restored message back out of the composer when it is sent again
 * (Retry). Text the user typed or changed since is never cleared.
 */
function takeDraft(text) {
  if (!state.els?.input || state.els.input.value.trim() !== String(text || '').trim()) return false;
  state.draft = '';
  state.els.input.value = '';
  state.els.input.dispatchEvent(new Event('input', { bubbles: true }));
  return true;
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
  // One line at the start of the attach row: a long label is cut short there,
  // and its tooltip holds the whole of it.
  const say = text => {
    state.els.workspaceContext.textContent = text;
    state.els.workspaceContext.title = text;
  };
  say('Checking workspace context…');
  try {
    const response = await fetch('/api/home-assistant/context', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ context: routeContext() })
    });
    if (!response.ok) throw new Error('Context unavailable');
    const data = await response.json();
    if (sequence !== state.workspaceSequence) return;
    say('Context · ' + workspaceContextLabel(data));
  } catch (_) {
    if (sequence === state.workspaceSequence)
      say('Workspace context unavailable · refresh or choose an app-wide page');
  }
}

function routeContext() {
  return collectCurrentWorkspaceContext();
}

/**
 * What a send addressed to the workspace's entry agent carries to the work
 * controller: who it is, and the session the drawer is already in with it.
 * The hired assistant's sends carry no recipient and keep their own path.
 */
export function entryAgentSendRecipient(recipient, sessionId) {
  if (recipient?.kind !== RECIPIENT_WORKSPACE_ENTRY || !recipient.available) return null;
  return {
    kind: RECIPIENT_WORKSPACE_ENTRY,
    name: recipient.name,
    workspaceId: recipient.workspaceId,
    workspaceName: recipient.workspaceName,
    appearance: recipient.appearance || null,
    sessionId: String(sessionId || '')
  };
}

function submit(event) {
  event?.preventDefault();
  moveSharedWorkActivity();
  const who = identity();
  const entry = entryAgentSendRecipient(recipients()?.current?.(), recipients()?.sessionId?.());
  const typed = String(state.els?.input?.value || '').trim();
  // An empty Send explores an attached folder or link only in the hired
  // assistant's conversation; neither is attached to a workspace agent's turn.
  const text =
    typed ||
    (who.personal && window.PersonalAssistantFolderContext?.hasFolder?.()
      ? window.PersonalAssistantFolderContext.emptySend?.()?.text || 'Explore this folder'
      : who.personal && window.PersonalAssistantLink?.hasLink?.()
        ? window.PersonalAssistantLink.sendText()
        : '');
  if (who.personal && window.PersonalAssistantConversation?.isLoading?.()) {
    setStatus('Wait for the conversation to open before sending. Your draft is kept.');
    return false;
  }
  if (who.personal && window.PersonalAssistantLink?.isPending?.()) {
    setStatus('Wait for the link to be checked before sending. Your message is kept here.');
    return false;
  }
  // While a reply is in flight the text stays in the box: sending it now would
  // start a second turn before the first one has a conversation to join.
  const busy = window.OriAskRouting?.getState?.().busy === true;
  if (who.personal && window.PersonalAssistantFolderContext?.isPending?.()) {
    setStatus('Wait for the local folder preview before sending. Your message is kept here.');
    return false;
  }
  if (busy && text) {
    setStatus(
      `${who.name} is still replying. Your message is kept here; send it when the reply arrives.`
    );
    announceWhenReplyArrives();
    return false;
  }
  if (!canSubmitAssistantWork({ available: who.available, pending: state.pending, busy, text })) {
    return false;
  }
  if (!who.personal && !entry) return false;
  if (!window.OriAskRouting || typeof window.OriAskRouting.submit !== 'function') {
    setStatus('The work controller is unavailable on this page. Nothing was submitted.');
    return false;
  }
  // Attribution is per turn: a workspace agent's turn never carries the hired
  // assistant's name, and the hired assistant's never carries the agent's.
  window.OriAskRouting.setRecipient?.(entry || { kind: 'personal' });
  if (who.personal && window.OriAskRouting.setPersonalAssistantIdentity) {
    window.OriAskRouting.setPersonalAssistantIdentity(state.view.name);
  }
  window.PersonalAssistantTranscript?.send?.();
  state.pending = true;
  // Disabling a focused button drops focus to the page at once, even when it is
  // enabled again a moment later. Remember it, so Send from the keyboard does
  // not lose the user's place in the drawer.
  const sendHadFocus = document.activeElement === state.els.send;
  state.els.send.disabled = true;
  const sentStatus = `Sent to ${who.name}.`;
  setStatus(sentStatus);
  const operation = Promise.resolve(
    window.OriAskRouting.submit(text, {
      routeContext: routeContext(),
      openThinkingModal: false,
      ...(entry ? { recipient: entry } : {})
    })
  );
  state.draft = '';
  state.els.input.value = '';
  syncComposerHeight();
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
    launcherRole: document.getElementById('personalAssistantLauncherRole'),
    name: document.getElementById('personalAssistantPanelName'),
    recipientButton: document.getElementById('personalAssistantRecipientButton'),
    recipientMenu: document.getElementById('personalAssistantRecipientMenu'),
    conversationMenu: document.getElementById('personalAssistantConversationMenu'),
    conversationItems: document.getElementById('personalAssistantConversationItems'),
    conversationTitle: document.getElementById('personalAssistantConversationTitle'),
    conversationNew: document.getElementById('personalAssistantConversationNew'),
    conversationContinue: document.getElementById('personalAssistantConversationContinue'),
    entrySessionTitle: document.getElementById('personalAssistantEntrySessionTitle'),
    entrySessionNew: document.getElementById('personalAssistantEntrySessionNew'),
    attachMenu: document.getElementById('personalAssistantAttachMenu'),
    attachItems: document.getElementById('personalAssistantAttachItems'),
    launcherAvatar: document.getElementById('personalAssistantLauncherAvatar'),
    panelAvatar: document.getElementById('personalAssistantPanelAvatar'),
    title: document.getElementById('personalAssistantPanelTitle'),
    checkIn: document.getElementById('personalAssistantCheckIn'),
    more: document.getElementById('personalAssistantMore'),
    close: document.getElementById('personalAssistantClose'),
    resize: document.getElementById('personalAssistantResize'),
    expand: document.getElementById('personalAssistantExpand'),
    form: document.getElementById('personalAssistantForm'),
    input: document.getElementById('personalAssistantInput'),
    send: document.getElementById('personalAssistantSend'),
    status: document.getElementById('personalAssistantPanelStatus'),
    modelSetup: document.getElementById('personalAssistantModelSetup'),
    workspaceContext: document.getElementById('personalAssistantWorkspaceContext'),
    legacyHandoff: document.getElementById('personalAssistantRecoveredHandoff'),
    legacyText: document.getElementById('personalAssistantRecoveredHandoffText'),
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
  state.drawer = readDrawerPrefs(browserStorage());
  const resize = state.els.resize;
  resize?.addEventListener('pointerdown', startDrawerResize);
  resize?.addEventListener('pointermove', moveDrawerResize);
  resize?.addEventListener('pointerup', endDrawerResize);
  resize?.addEventListener('pointercancel', endDrawerResize);
  resize?.addEventListener('lostpointercapture', endDrawerResize);
  resize?.addEventListener('keydown', keyDrawerResize);
  state.els.expand?.addEventListener('click', toggleDrawerExpanded);
  state.els.form?.addEventListener('submit', submit);
  // A legacy work handoff is an explicit recovery offer, not a Help search.
  try {
    state.legacyHandoff = readLegacyAssistantHandoff(window.sessionStorage);
  } catch (_) {
    /* Storage access itself may throw. */
  }
  renderLegacyHandoff();
  document
    .getElementById('personalAssistantRecoverHandoff')
    ?.addEventListener('click', recoverLegacyHandoff);
  document
    .getElementById('personalAssistantDismissHandoff')
    ?.addEventListener('click', dismissLegacyHandoff);
  state.draft = tabValue(PANEL_DRAFT_KEY);
  if (state.draft) state.els.input.value = state.draft;
  state.els.input?.addEventListener('input', () => {
    state.draft = state.els.input.value;
    saveTabValue(PANEL_DRAFT_KEY, state.draft);
    syncComposerHeight();
  });
  document.addEventListener('ori:workspace-context', () => void refreshWorkspaceContext());
  window.addEventListener('popstate', () => void refreshWorkspaceContext());
  // The recipient follows the page: the workspace's entry agent on a workspace
  // page, the hired assistant elsewhere.
  document.addEventListener('personal-assistant:recipient', event => {
    const recipient = event.detail?.recipient;
    window.OriAskRouting?.setRecipient?.(recipient);
    renderIdentity();
    void hydrateEntryConversation();
  });
  document.addEventListener('personal-assistant:entry-session', renderEntrySessionBar);
  state.els.entrySessionNew?.addEventListener('click', startNewEntryConversation);
  state.recipientMenu = createRecipientMenu({
    button: state.els.recipientButton,
    menu: state.els.recipientMenu,
    doc: document,
    rows: menuRows,
    choose: chooseRecipient,
    refuse: refuseRecipient,
    avatar: row =>
      row.kind === RECIPIENT_WORKSPACE_ENTRY
        ? assistantAvatarMarkup(row.name, row.appearance)
        : assistantAvatarMarkup(state.view.name, state.personalAssistant?.appearance)
  });
  state.conversationMenu = createActionMenu({
    button: state.els.conversationMenu,
    menu: state.els.conversationItems,
    doc: document,
    items: [
      state.els.conversationNew,
      state.els.conversationContinue,
      state.els.entrySessionNew,
      state.els.checkIn
    ].filter(Boolean)
  });
  state.attachMenu = createActionMenu({
    button: state.els.attachMenu,
    menu: state.els.attachItems,
    doc: document,
    items: [
      state.els.folderChip,
      document.getElementById('personalAssistantLinkChip'),
      document.getElementById('personalAssistantExploreAttachedFolder'),
      document.getElementById('personalAssistantReviewFolderSetup')
    ].filter(Boolean)
  });
  document.addEventListener('ori:workspace-context', () => void recipients()?.refresh?.());
  window.addEventListener('popstate', () => void recipients()?.refresh?.());
  void recipients()?.refresh?.();
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
    if (event.defaultPrevented) return;
    // Bootstrap removes `.show` before this bubbling listener runs, so the
    // event target is also part of the topmost-modal check.
    const modalOpen = Boolean(
      event.target?.closest?.('.modal, .dropdown-menu, .offcanvas') ||
      document.querySelector?.('.modal.show, .dropdown-menu.show, .offcanvas.show, dialog[open]') ||
      Array.from(document.querySelectorAll?.('[role="dialog"][aria-modal="true"]') || []).some(
        overlay =>
          overlay.getClientRects?.().length > 0 && overlay.getAttribute('aria-hidden') !== 'true'
      ) ||
      window.OriSpotlight?.isOpen?.()
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
  const intent = assistantPanelOpenIntent(window.location.search, Boolean(state.els.todayBanner));
  void refresh().then(() => {
    if (
      !intent.restoreAllowed ||
      !(intent.requestedOpen || tabValue(PANEL_OPEN_KEY) === '1') ||
      !open(launcher)
    )
      return;
    const url = new URL(window.location.href);
    if (intent.requestedOpen && url.searchParams.get('panel') === 'today') {
      url.searchParams.delete('panel');
      window.history.replaceState(null, '', url.pathname + url.search + url.hash);
    }
  });
}

const api = {
  init,
  open,
  openActivity: (trigger, options = {}) => {
    if (state.open) {
      if (options.focus === true) focusOnOpen();
      return true;
    }
    return open(trigger, { workActivity: true, focus: options.focus === true });
  },
  isOpen: () => state.open,
  hasLauncher: () => identity().launcherVisible,
  close,
  prefill,
  suggestReply,
  suggestionNotice: () => state.suggestionNotice,
  restoreDraft,
  takeDraft,
  setComposerPlaceholder,
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
