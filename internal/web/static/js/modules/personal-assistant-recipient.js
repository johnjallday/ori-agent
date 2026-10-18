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

/**
 * The drawer's recipient for this tab. Pure decisions come from
 * resolveRecipient; this keeps the facts it needs (page, entry agent, hired
 * assistant, pick) and tells listeners when the answer changes.
 */
export function createRecipientController({
  fetchImpl = typeof fetch === 'function' ? (url, init) => fetch(url, init) : null,
  storage,
  pageContext = () => ({}),
  loadedWorkspace = () => null
} = {}) {
  let page = { surface: '', workspaceId: '' };
  let workspaceName = '';
  let entry = { status: 'loading' };
  let personal = {};
  let hqWorkspaceId = '';
  let sequence = 0;
  let resolution = resolveRecipient({});
  const listeners = new Set();

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
      hqWorkspaceId = text(next.hqWorkspaceId);
      return recompute();
    },
    pick(kind) {
      writePick(page.workspaceId, kind, storage);
      return recompute();
    },
    sessionId: () => readEntrySession(page.workspaceId, storage),
    rememberSession(workspaceId, session) {
      const id = text(session?.id ?? session);
      if (!text(workspaceId) || !id) return;
      writeEntrySession(workspaceId, id, storage);
    },
    forgetSession() {
      writeEntrySession(page.workspaceId, '', storage);
    },
    onChange(listener) {
      if (typeof listener !== 'function') return () => {};
      listeners.add(listener);
      return () => listeners.delete(listener);
    }
  };
}

// One controller per tab. The panel drives it; the personal conversation and
// the work controller read it at call time and listen for its event.
if (typeof window !== 'undefined' && typeof document !== 'undefined') {
  const controller = createRecipientController({
    pageContext: () => collectCurrentWorkspaceContext(),
    loadedWorkspace: () => window.workspaceDetail?.workspace || null
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
  window.PersonalAssistantRecipient = controller;
}
