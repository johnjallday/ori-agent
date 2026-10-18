// Who the assistant drawer is talking to. Inside a workspace the drawer opens
// as that workspace's entry agent (its Commander); everywhere else, and in the
// Personal HQ, it is the hired Personal Assistant. The pure resolver below
// decides that from page facts; the rest of the module keeps the per-tab pick
// and the entry agent's current session beside the personal conversation's
// own sessionStorage key, never in it.

import { collectCurrentWorkspaceContext } from './personal-assistant-workspace-context.js';

export const RECIPIENT_PERSONAL = 'personal';
export const RECIPIENT_WORKSPACE_ENTRY = 'workspace_entry';

// The surfaces the page-context collector already calls a workspace page.
const WORKSPACE_SURFACES = new Set(['workspace_detail', 'workspace_canvas', 'workspace_task']);

export const PICK_KEY_PREFIX = 'ori.personalAssistant.recipient.';
export const SESSION_KEY_PREFIX = 'ori.personalAssistant.recipientSession.';
// The Personal HQ id the hired assistant last reported in this tab, so a
// reload of the HQ page is recognised before the status request answers.
export const HQ_KEY = 'ori.personalAssistant.hqWorkspaceId';
// Stored in place of a session id after New: this tab chose a new
// conversation, so the most recent session is not offered again until the
// next message creates one (FR22).
export const NEW_CONVERSATION_MARK = '-';

// The map's command node label: "Commander" for an orchestrator, "Acting
// Commander" for any other agent holding the slot (workspace-command.js).
export function commanderLabel(role) {
  return String(role || '')
    .trim()
    .toLowerCase() === 'orchestrator'
    ? 'Commander'
    : 'Acting Commander';
}

function text(value) {
  return String(value ?? '').trim();
}

export function isWorkspaceSurface(surface) {
  return WORKSPACE_SURFACES.has(String(surface || ''));
}

/**
 * Resolve the drawer's recipients for one page.
 *
 * input.surface / input.workspaceId — from collectCurrentWorkspaceContext.
 * input.workspaceName — display name of that workspace.
 * input.entry — { status: 'loading' | 'ready' | 'vacant', name, role, appearance }.
 * input.hqWorkspaceId — the Personal HQ id the assistant status reports.
 * input.personal — { name, available, state, subline, appearance }.
 * input.pick — an explicit pick for this workspace in this tab, or ''.
 *
 * Returns { current, options, pickerVisible }. `options` is empty when the
 * Personal Assistant is the only recipient; otherwise it lists the entry agent
 * first and the Personal Assistant second.
 */
export function resolveRecipient(input = {}) {
  const personalInput = input.personal || {};
  const personal = {
    kind: RECIPIENT_PERSONAL,
    key: RECIPIENT_PERSONAL,
    name: text(personalInput.name) || 'Assistant',
    role: 'Personal Assistant',
    subline: text(personalInput.subline),
    appearance: personalInput.appearance || null,
    available: personalInput.available !== false,
    state: text(personalInput.state),
    disabled: false,
    disabledReason: ''
  };
  const only = { current: personal, options: [], pickerVisible: false };

  const workspaceId = text(input.workspaceId);
  if (!isWorkspaceSurface(input.surface) || !workspaceId) return only;
  // The Personal HQ is decided by id, never by a display name.
  const hqId = text(input.hqWorkspaceId);
  if (hqId && hqId === workspaceId) return only;

  const entryInput = input.entry || {};
  const name = text(entryInput.name);
  let status = ['ready', 'vacant', 'error'].includes(entryInput.status)
    ? entryInput.status
    : 'loading';
  if (status === 'ready' && !name) status = 'vacant';
  // Until the hired assistant's status says which workspace is its Personal
  // HQ, this page may be that HQ, so its own agent is neither addressable nor
  // named yet.
  const pending = personalInput.known === false && status !== 'vacant' && status !== 'error';
  if (pending) status = 'loading';
  const workspaceName = text(input.workspaceName);
  const label = commanderLabel(entryInput.role);
  const roleLine = workspaceName ? `${label} · ${workspaceName}` : label;
  const vacant = status === 'vacant';
  // A failed lookup is not a vacancy; it only means this page cannot address
  // the Commander until it loads.
  const unreachable = status === 'error';
  const disabledReason = vacant
    ? 'No Commander yet'
    : unreachable
      ? 'Commander could not be loaded'
      : '';
  const entry = {
    kind: RECIPIENT_WORKSPACE_ENTRY,
    key: RECIPIENT_WORKSPACE_ENTRY,
    workspaceId,
    workspaceName,
    name: status === 'ready' ? name : '',
    role: disabledReason || roleLine,
    subline: roleLine,
    appearance: entryInput.appearance || null,
    available: status === 'ready',
    loading: status === 'loading',
    pending,
    vacant,
    disabled: vacant || unreachable,
    disabledReason
  };

  const options = [entry, personal];
  // A Commander that cannot be addressed is never the recipient; while it is
  // still being looked up it stays the default so the header does not flash
  // the Personal Assistant's name first.
  const current = entry.disabled || input.pick === RECIPIENT_PERSONAL ? personal : entry;
  return { current, options, pickerVisible: true };
}

function safeStorage(storage) {
  if (storage) return storage;
  try {
    return typeof sessionStorage === 'undefined' ? null : sessionStorage;
  } catch (_) {
    return null;
  }
}

function read(storage, key) {
  try {
    return text(safeStorage(storage)?.getItem(key));
  } catch (_) {
    return '';
  }
}

function write(storage, key, value) {
  const store = safeStorage(storage);
  if (!store) return;
  try {
    if (value) store.setItem(key, value);
    else store.removeItem(key);
  } catch (_) {
    // Private windows can refuse storage; the pick then lasts for the page.
  }
}

export function pickKey(workspaceId) {
  return PICK_KEY_PREFIX + text(workspaceId);
}

export function sessionKey(workspaceId) {
  return SESSION_KEY_PREFIX + text(workspaceId);
}

export function readPick(workspaceId, storage) {
  if (!text(workspaceId)) return '';
  const value = read(storage, pickKey(workspaceId));
  return value === RECIPIENT_PERSONAL || value === RECIPIENT_WORKSPACE_ENTRY ? value : '';
}

export function writePick(workspaceId, kind, storage) {
  if (!text(workspaceId)) return;
  const value = kind === RECIPIENT_PERSONAL || kind === RECIPIENT_WORKSPACE_ENTRY ? kind : '';
  write(storage, pickKey(workspaceId), value);
}

// The pick lasts for one visit to one workspace: arriving on another
// workspace, or on an app-wide page, drops every other workspace's pick.
export function clearOtherPicks(workspaceId, storage) {
  const store = safeStorage(storage);
  if (!store) return;
  const keep = text(workspaceId) ? pickKey(workspaceId) : '';
  try {
    const stale = [];
    for (let i = 0; i < store.length; i += 1) {
      const key = store.key(i);
      if (key && key.startsWith(PICK_KEY_PREFIX) && key !== keep) stale.push(key);
    }
    stale.forEach(key => store.removeItem(key));
  } catch (_) {
    // Nothing to clear when storage is unavailable.
  }
}

export function readEntrySession(workspaceId, storage) {
  return text(workspaceId) ? read(storage, sessionKey(workspaceId)) : '';
}

export function writeEntrySession(workspaceId, sessionId, storage) {
  if (!text(workspaceId)) return;
  write(storage, sessionKey(workspaceId), text(sessionId));
}

/** The composer placeholder for the entry agent, in the workspace's own copy. */
export function entryAgentPlaceholder(recipient) {
  if (recipient?.loading) return 'Finding this workspace’s Commander…';
  return `Ask ${text(recipient?.name) || 'the Commander'} about this workspace… (/task creates a task)`;
}

function sameName(a, b) {
  return text(a).toLowerCase() === text(b).toLowerCase();
}

function workspaceFromPayload(payload) {
  if (!payload || typeof payload !== 'object') return null;
  const candidate = payload.workspace || payload.data?.workspace || payload.data || payload;
  return candidate && typeof candidate === 'object' ? candidate : null;
}

export function entryAgentNameOf(workspace) {
  return text(workspace?.entry_agent_name) || text(workspace?.shared_data?.entry_agent_name);
}

async function getJSON(fetchImpl, url) {
  const response = await fetchImpl(url, { headers: { Accept: 'application/json' } });
  let body = null;
  try {
    body = await response.json();
  } catch (_) {
    body = null;
  }
  return { ok: response.ok, status: response.status, body };
}

/**
 * Looks up a workspace's entry agent the way /api/chat resolves a workspace
 * default: the agent is runnable when the agent store knows it, or when the
 * workspace holds its own copy. A failed read is reported as `error`, never
 * as a vacancy.
 */
export async function loadEntryAgent(workspaceId, { fetchImpl, loadedWorkspace } = {}) {
  const id = text(workspaceId);
  let workspace = loadedWorkspace && text(loadedWorkspace.id) === id ? loadedWorkspace : null;
  if (!workspace) {
    try {
      const result = await getJSON(fetchImpl, `/api/workspaces/${encodeURIComponent(id)}`);
      workspace = result.ok ? workspaceFromPayload(result.body) : null;
    } catch (_) {
      workspace = null;
    }
    if (!workspace) return { workspaceName: '', entry: { status: 'error' } };
  }
  const workspaceName = text(workspace.name);
  const name = entryAgentNameOf(workspace);
  if (!name) return { workspaceName, entry: { status: 'vacant' } };
  try {
    const agent = await getJSON(fetchImpl, `/api/agents?name=${encodeURIComponent(name)}`);
    if (agent.ok && agent.body) {
      return {
        workspaceName,
        entry: {
          status: 'ready',
          name: text(agent.body.name) || name,
          role: text(agent.body.role),
          appearance: agent.body.appearance || null
        }
      };
    }
    if (agent.status === 404) {
      const copies = await getJSON(fetchImpl, `/api/workspaces/${encodeURIComponent(id)}/agents`);
      const list = Array.isArray(copies.body?.agents) ? copies.body.agents : [];
      const copy = copies.ok ? list.find(item => sameName(item?.name, name)) : null;
      if (copies.ok && !copy) return { workspaceName, entry: { status: 'vacant' } };
      return {
        workspaceName,
        entry: {
          status: 'ready',
          name: text(copy?.name) || name,
          role: text(copy?.role),
          appearance: copy?.appearance || null
        }
      };
    }
  } catch (_) {
    // The name is known; the server decides whether it answers.
  }
  return { workspaceName, entry: { status: 'ready', name, role: '', appearance: null } };
}

/** The conversation rows of one workspace chat session, oldest first. */
export function entrySessionRows(session) {
  const messages = Array.isArray(session?.messages) ? session.messages : [];
  return messages
    .filter(message => ['user', 'assistant'].includes(message?.role))
    .map(message => ({ role: message.role, content: String(message.content ?? '') }))
    .filter(row => row.content.trim());
}

/**
 * The entry agent's current session for this tab (FR20): the one the tab is
 * already in, else the most recently updated session in this workspace with
 * that agent, else none. A remembered session that is gone or belongs to
 * another agent is forgotten. Returns { session, stored, error } where
 * `session` is the full session with its messages, or null for a new
 * conversation; `error` means the server could not be read, and the
 * remembered id (if any) is kept.
 */
export async function loadEntrySession({ fetchImpl, workspaceId, agentName, storedId }) {
  const id = text(workspaceId);
  const agent = text(agentName);
  const read = async sessionId => {
    const result = await getJSON(fetchImpl, `/api/sessions/${encodeURIComponent(sessionId)}`);
    if (result.ok && result.body?.id) return { session: result.body };
    return { gone: result.status === 404, failed: result.status !== 404 };
  };
  if (text(storedId) === NEW_CONVERSATION_MARK) {
    return { session: null, stored: true, fresh: true, error: false };
  }
  try {
    if (text(storedId)) {
      const stored = await read(text(storedId));
      if (stored.session && sameName(stored.session.agent_name, agent)) {
        return { session: stored.session, stored: true, error: false };
      }
      if (stored.failed) return { session: null, stored: true, error: true };
    }
    const query = new URLSearchParams({
      folder_id: id,
      agent_name: agent,
      sort: 'updated_desc',
      limit: '1'
    });
    const list = await getJSON(fetchImpl, `/api/sessions?${query}`);
    if (!list.ok) return { session: null, stored: false, error: true };
    const latest = (Array.isArray(list.body?.sessions) ? list.body.sessions : []).find(
      item => item?.id && sameName(item.agent_name, agent)
    );
    if (!latest) return { session: null, stored: false, error: false };
    const full = await read(String(latest.id));
    if (full.session) return { session: full.session, stored: false, error: false };
    return { session: null, stored: false, error: full.failed === true };
  } catch (_) {
    return { session: null, stored: Boolean(text(storedId)), error: true };
  }
}

/**
 * The drawer's recipient for this tab. Pure decisions come from
 * resolveRecipient; this keeps the facts it needs (page, entry agent, hired
 * assistant, pick) and tells listeners when the answer changes.
 */
export function createRecipientController({
  fetchImpl = typeof fetch === 'function' ? (url, init) => fetch(url, init) : null,
  storage,
  pageContext = () => ({}),
  loadedWorkspace = () => null,
  // The page's controller waits for the hired assistant's status before it
  // lets a workspace's own agent be addressed (it may be the Personal HQ).
  waitForPersonal = false
} = {}) {
  let page = { surface: '', workspaceId: '' };
  let workspaceName = '';
  let entry = { status: 'loading' };
  let personal = waitForPersonal ? { known: false } : {};
  let hqWorkspaceId = waitForPersonal ? read(storage, HQ_KEY) : '';
  let sequence = 0;
  let resolution = resolveRecipient({});
  const listeners = new Set();
  // The title of the entry agent's current session, for the session strip.
  let session = { workspaceId: '', id: '', title: '' };
  const sessionListeners = new Set();

  function sessionId() {
    const stored = readEntrySession(page.workspaceId, storage);
    return stored === NEW_CONVERSATION_MARK ? '' : stored;
  }

  function setSession(workspaceId, id, title) {
    writeEntrySession(workspaceId, id, storage);
    const real = text(id) && text(id) !== NEW_CONVERSATION_MARK ? text(id) : '';
    session = { workspaceId: text(workspaceId), id: real, title: real ? text(title) : '' };
    for (const listener of sessionListeners) {
      try {
        listener({ ...session });
      } catch (_) {
        // A strip that fails to render never stops the conversation.
      }
    }
  }

  function recompute() {
    const previous = resolution;
    resolution = resolveRecipient({
      surface: page.surface,
      workspaceId: page.workspaceId,
      workspaceName,
      entry,
      hqWorkspaceId,
      personal,
      pick: readPick(page.workspaceId, storage)
    });
    for (const listener of listeners) {
      try {
        listener(resolution, previous);
      } catch (_) {
        // One listener's failure never stops another from rendering.
      }
    }
    return resolution;
  }

  async function refresh() {
    const context = pageContext() || {};
    const workspaceId = isWorkspaceSurface(context.surface) ? text(context.workspace_id) : '';
    clearOtherPicks(workspaceId, storage);
    const moved = workspaceId !== page.workspaceId;
    page = { surface: String(context.surface || ''), workspaceId };
    if (moved) {
      entry = { status: 'loading' };
      workspaceName = '';
    }
    const ticket = ++sequence;
    if (!workspaceId || !fetchImpl) {
      if (!fetchImpl && workspaceId) entry = { status: 'error' };
      return recompute();
    }
    if (moved) recompute();
    const loaded = await loadEntryAgent(workspaceId, {
      fetchImpl,
      loadedWorkspace: loadedWorkspace()
    });
    // A slower lookup for a page the tab has left never lands.
    if (ticket !== sequence) return resolution;
    entry = loaded.entry;
    workspaceName = loaded.workspaceName || workspaceName;
    return recompute();
  }

  return {
    refresh,
    current: () => resolution.current,
    resolution: () => resolution,
    workspaceId: () => page.workspaceId,
    setPersonal(next = {}) {
      personal = {
        name: next.name,
        available: next.available,
        state: next.state,
        subline: next.subline,
        appearance: next.appearance
      };
      // A status that could not be read says nothing about the HQ; the id
      // already known is kept.
      if (next.hqWorkspaceId !== undefined) {
        hqWorkspaceId = text(next.hqWorkspaceId);
        write(storage, HQ_KEY, hqWorkspaceId);
      }
      return recompute();
    },
    pick(kind) {
      writePick(page.workspaceId, kind, storage);
      return recompute();
    },
    sessionId,
    session: () => ({
      workspaceId: page.workspaceId,
      id: sessionId(),
      title: session.workspaceId === page.workspaceId ? session.title : ''
    }),
    /** The entry agent's current session, with its messages (FR20). */
    async loadSession() {
      const current = resolution.current;
      const workspaceId = page.workspaceId;
      if (current?.kind !== RECIPIENT_WORKSPACE_ENTRY || !current.available || !fetchImpl) {
        return { session: null, error: false };
      }
      const loaded = await loadEntrySession({
        fetchImpl,
        workspaceId,
        agentName: current.name,
        storedId: readEntrySession(workspaceId, storage)
      });
      // The tab moved on while the server answered; that answer is not ours.
      if (page.workspaceId !== workspaceId) return { session: null, error: false, stale: true };
      if (loaded.session) setSession(workspaceId, loaded.session.id, loaded.session.title);
      else if (!loaded.error && !loaded.fresh) setSession(workspaceId, '', '');
      return loaded;
    },
    rememberSession(workspaceId, next) {
      const id = text(next?.id ?? next);
      if (!text(workspaceId) || !id) return;
      setSession(workspaceId, id, next?.title);
    },
    /** New: this tab starts a new conversation with the next message. */
    forgetSession() {
      setSession(page.workspaceId, NEW_CONVERSATION_MARK, '');
    },
    onSession(listener) {
      if (typeof listener !== 'function') return () => {};
      sessionListeners.add(listener);
      return () => sessionListeners.delete(listener);
    },
    onChange(listener) {
      if (typeof listener !== 'function') return () => {};
      listeners.add(listener);
      return () => listeners.delete(listener);
    }
  };
}

/**
 * Why the drawer may not switch recipient right now (FR14, FR15), or ''. A
 * reply in flight, or a review the current recipient is waiting on, stays
 * with the conversation it belongs to.
 */
export function switchLockMessage(current, state = {}) {
  const name = text(current?.name) || 'the assistant';
  if (state.busy) return `Wait for ${name} to reply before switching. Your message is kept.`;
  if (state.hasPendingConfirmation || state.hasPlanning)
    return `Answer the review ${name} is waiting on before switching. Your message is kept.`;
  if (state.loading)
    return 'Wait for the conversation to open before switching. Your message is kept.';
  return '';
}

/** The menu's rows, entry agent first, from the resolver's answer. */
export function recipientMenuRows(resolution, { locked = false, fillHref = '' } = {}) {
  const current = resolution?.current;
  return (resolution?.options || []).map(option => {
    const checked = option.kind === current?.kind;
    const unavailable = option.kind === RECIPIENT_PERSONAL && option.available === false;
    return {
      kind: option.kind,
      name:
        option.kind === RECIPIENT_WORKSPACE_ENTRY
          ? option.name || 'Commander'
          : option.name || 'Assistant',
      role:
        option.kind === RECIPIENT_PERSONAL && unavailable
          ? 'Personal Assistant · not set up'
          : option.role,
      appearance: option.appearance || null,
      checked,
      // A row that cannot be chosen is still focusable, so its reason is heard.
      disabled: option.disabled === true || (locked && !checked),
      locked: locked && !checked && option.disabled !== true,
      fillHref: option.vacant && fillHref ? fillHref : ''
    };
  });
}

const MENU_KEYS = {
  ArrowDown: 'next',
  ArrowUp: 'prev',
  Home: 'first',
  End: 'last',
  Enter: 'choose',
  ' ': 'choose',
  Escape: 'close',
  Tab: 'tab'
};

export function menuKeyIntent(key) {
  return MENU_KEYS[key] || null;
}

/** The item a navigation key moves to, wrapping at either end. */
export function nextMenuIndex(index, count, intent) {
  if (count <= 0) return -1;
  if (intent === 'first') return 0;
  if (intent === 'last') return count - 1;
  const from = index < 0 ? (intent === 'prev' ? 0 : -1) : index;
  if (intent === 'next') return (from + 1) % count;
  if (intent === 'prev') return (from - 1 + count) % count;
  return index;
}

/** The menu button's accessible name: who is selected, and what it does. */
export function recipientButtonLabel(current) {
  const name = text(current?.name) || 'Assistant';
  const role = text(current?.kind === RECIPIENT_WORKSPACE_ENTRY ? current?.subline : current?.role);
  return `${role ? `${name}, ${role}` : name}. Choose who to talk to`;
}

/**
 * The recipient menu button's behaviour (FR9, FR12): open on click, Enter,
 * Space or ArrowDown; arrows move; Enter or Space chooses; Escape closes only
 * the menu and returns focus to the button; a click outside closes it.
 * `rows()` gives recipientMenuRows; `choose(kind)` switches, `refuse(row)`
 * explains a row that cannot be chosen.
 */
export function createRecipientMenu({ button, menu, doc, rows, choose, refuse, avatar }) {
  if (!button || !menu || !doc) return null;
  let open = false;

  function items() {
    return Array.from(menu.querySelectorAll('[role="menuitemradio"], [role="menuitem"]'));
  }

  function render() {
    const list = rows();
    menu.replaceChildren();
    for (const row of list) {
      const item = doc.createElement('button');
      item.type = 'button';
      item.className = 'personal-assistant-recipient-menu__item';
      item.setAttribute('role', 'menuitemradio');
      item.setAttribute('aria-checked', row.checked ? 'true' : 'false');
      item.tabIndex = -1;
      item.dataset.recipient = row.kind;
      if (row.disabled) item.setAttribute('aria-disabled', 'true');
      const face = doc.createElement('span');
      face.className = 'personal-assistant-recipient-menu__avatar';
      face.setAttribute('aria-hidden', 'true');
      if (typeof avatar === 'function') face.innerHTML = avatar(row);
      const copy = doc.createElement('span');
      copy.className = 'personal-assistant-recipient-menu__copy';
      const name = doc.createElement('span');
      name.className = 'personal-assistant-recipient-menu__name';
      name.textContent = row.name;
      const role = doc.createElement('span');
      role.className = 'personal-assistant-recipient-menu__role';
      role.textContent = row.role;
      copy.append(name, role);
      const check = doc.createElement('i');
      check.className = 'bi bi-check2 personal-assistant-recipient-menu__check';
      check.setAttribute('aria-hidden', 'true');
      item.append(face, copy, check);
      item.addEventListener('click', event => {
        event.preventDefault();
        activate(row);
      });
      menu.append(item);
      if (row.fillHref) {
        const fill = doc.createElement('a');
        fill.className = 'personal-assistant-recipient-menu__fill';
        fill.setAttribute('role', 'menuitem');
        fill.tabIndex = -1;
        fill.href = row.fillHref;
        fill.textContent = 'Fill the role';
        menu.append(fill);
      }
    }
    return list;
  }

  function activate(shown) {
    // Decide on the rows as they are now: a reply may have arrived since the
    // menu was drawn.
    const row = rows().find(item => item.kind === shown.kind) || shown;
    if (row.disabled) {
      refuse(row);
      refreshRows();
      return;
    }
    close({ focus: true });
    if (!row.checked) choose(row.kind);
  }

  function refreshRows() {
    if (!open) return;
    const index = items().indexOf(doc.activeElement);
    render();
    if (index >= 0) focusItem(index);
  }

  function focusItem(index) {
    const list = items();
    if (index >= 0 && list[index]) list[index].focus();
  }

  function show({ focus = 'checked' } = {}) {
    render();
    open = true;
    menu.hidden = false;
    button.setAttribute('aria-expanded', 'true');
    const list = items();
    const checked = list.findIndex(item => item.getAttribute('aria-checked') === 'true');
    focusItem(focus === 'last' ? list.length - 1 : checked >= 0 ? checked : 0);
  }

  function close({ focus = false } = {}) {
    if (!open) return;
    open = false;
    menu.hidden = true;
    button.setAttribute('aria-expanded', 'false');
    if (focus) button.focus();
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
      return;
    }
    if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
      event.preventDefault();
      show({ focus: event.key === 'ArrowUp' ? 'last' : 'checked' });
    }
  });
  menu.addEventListener('keydown', event => {
    const intent = menuKeyIntent(event.key);
    if (!intent) return;
    const list = items();
    const index = list.indexOf(doc.activeElement);
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
    if (intent === 'choose') {
      const active = list[index];
      if (active?.getAttribute('role') === 'menuitem') return; // the link follows itself
      event.preventDefault();
      active?.click();
      return;
    }
    event.preventDefault();
    focusItem(nextMenuIndex(index, list.length, intent));
  });
  doc.addEventListener('click', event => {
    if (open && !menu.contains(event.target) && !button.contains(event.target)) close();
  });

  return {
    isOpen: () => open,
    close,
    show,
    // Rows change while open when a reply arrives; keep the user's place.
    refresh: refreshRows
  };
}

// One controller per tab. The panel drives it; the personal conversation and
// the work controller read it at call time and listen for its event.
if (typeof window !== 'undefined' && typeof document !== 'undefined') {
  const controller = createRecipientController({
    pageContext: () => collectCurrentWorkspaceContext(),
    loadedWorkspace: () => window.workspaceDetail?.workspace || null,
    waitForPersonal: true
  });
  controller.onChange((resolution, previous) => {
    try {
      document.dispatchEvent(
        new CustomEvent('personal-assistant:recipient', {
          detail: { recipient: resolution.current, resolution, previous }
        })
      );
    } catch (_) {
      // The event is a coordination seam; rendering never depends on it.
    }
  });
  controller.onSession(session => {
    try {
      document.dispatchEvent(
        new CustomEvent('personal-assistant:entry-session', { detail: { session } })
      );
    } catch (_) {
      // As above: only the session strip listens.
    }
  });
  window.PersonalAssistantRecipient = controller;
}
