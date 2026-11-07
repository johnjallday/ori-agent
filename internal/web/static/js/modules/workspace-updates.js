// Shared, bounded read state. Server records own unread/review truth; this
// cache is a projection, never a counter or acknowledgement authority.
export function createWorkspaceUpdates({
  fetchImpl = (...args) => fetch(...args),
  maxEntries = 500
} = {}) {
  const entries = new Map();
  const listeners = new Set();
  const committedListeners = new Set();
  let generation = 0;
  let serial = 0;
  let acknowledgementEpoch = 0;
  const capacity = Math.max(200, Math.min(500, Number(maxEntries) || 500));
  const pageListeners = new Set();
  const emptyPage = () => ({
    state: 'loading',
    ids: [],
    after: '',
    nextAfter: '',
    hasMore: false,
    stale: false
  });
  let page = emptyPage();
  let pageRead = null;
  let pageInvalidated = false;
  const empty = () => ({ state: 'loading', data: null, stale: false });
  function entry(id) {
    if (!entries.has(id)) {
      entries.set(id, { view: empty(), version: 0, read: null, mutation: null });
    } else {
      const item = entries.get(id);
      entries.delete(id);
      entries.set(id, item);
    }
    return entries.get(id);
  }
  function prune() {
    const visible = new Set(page.ids);
    for (const [id, item] of entries) {
      if (entries.size <= capacity) break;
      if (!visible.has(id) && !item.read && !item.mutation) {
        entries.delete(id);
        publish(id, item, empty());
      }
    }
  }
  function publishPage(view) {
    page = view;
    prune();
    for (const listener of pageListeners) {
      try {
        listener(page);
      } catch (_) {
        /* A presenter cannot change source authority. */
      }
    }
  }
  function publish(id, item, view) {
    item.view = view;
    for (const listener of listeners) {
      try {
        listener(id, view);
      } catch (_) {
        // A broken presenter cannot change the saved/source state.
      }
    }
  }
  function accept(id, item, data) {
    if (!data || data.workspace_id !== id || !data.welcome?.state) return false;
    // Failure of one source does not erase a previously verified observation.
    // Its qualification is explicit; it can never authorize navigation/seen.
    data = { ...data, review: data.review || { state: 'unavailable' } };
    for (const source of ['welcome', 'review']) {
      if (data[source].state === 'unavailable' && item.view.data?.[source]) {
        data[source] = { ...item.view.data[source], state: 'unavailable', stale: true };
      }
    }
    publish(id, item, { state: 'ready', data, stale: false });
    return true;
  }
  function unavailable(id, item, status = 0) {
    const gone = status === 403 || status === 404;
    publish(id, item, {
      state: gone ? 'not_found' : 'unavailable',
      data: gone ? null : item.view.data,
      stale: !gone && Boolean(item.view.data)
    });
  }
  function refresh(id) {
    if (!id) return Promise.resolve(empty());
    const item = entry(id);
    if (item.mutation) return item.mutation.then(() => item.view);
    if (item.read) return item.read;
    const scope = generation;
    const version = ++serial;
    item.version = version;
    const current = () =>
      scope === generation && version === item.version && entries.get(id) === item;
    const work = (async () => {
      try {
        const response = await fetchImpl(`/api/workspaces/${encodeURIComponent(id)}/updates`, {
          headers: { Accept: 'application/json' },
          cache: 'no-store'
        });
        const data = response.ok ? await response.json() : null;
        if (current() && (!response.ok || !accept(id, item, data)))
          unavailable(id, item, response.status);
      } catch (_) {
        if (current()) unavailable(id, item);
      }
      return scope === generation && entries.get(id) === item ? item.view : empty();
    })();
    item.read = work;
    void work.finally(() => {
      if (item.read === work) item.read = null;
      prune();
    });
    return work;
  }
  function invalidate(id) {
    if (!id) return;
    const item = entry(id);
    const pending = item.mutation || item.read;
    if (!pending) return void refresh(id);
    // A commit hint during a read must cause one subsequent canonical read,
    // not be lost by coalescing into the pre-commit response.
    if (item.invalidated) return;
    item.invalidated = true;
    const scope = generation;
    void pending.finally(() => {
      item.invalidated = false;
      if (scope === generation && entries.get(id) === item) void refresh(id);
    });
  }
  // A page replaces the visible window, not a fictitious whole-tree snapshot.
  // Previous observations share this cache; unvisited/evicted IDs remain unknown.
  // Only the caller asks for the next page. No automatic unbounded traversal.
  function refreshPage({ after = '' } = {}) {
    const requestedScope = generation;
    const later = () => (requestedScope === generation ? refreshPage({ after }) : page);
    if (pageRead) {
      if (pageRead.after === after) return pageRead.work;
      return pageRead.work.then(later);
    }
    const mutations = [...entries.values()].map(item => item.mutation).filter(Boolean);
    if (mutations.length) return Promise.all(mutations).then(later);
    const scope = generation;
    const epoch = acknowledgementEpoch;
    const version = ++serial;
    const previous = page;
    publishPage({ ...page, state: 'loading', after });
    const work = (async () => {
      try {
        const query = new URLSearchParams({ limit: '100', ...(after ? { after } : {}) });
        const response = await fetchImpl(`/api/workspace-updates?${query}`, {
          headers: { Accept: 'application/json' },
          cache: 'no-store'
        });
        const data = response.ok ? await response.json() : null;
        if (scope !== generation) return page;
        if (epoch !== acknowledgementEpoch) {
          pageInvalidated = true; // retry only this page after the exact ack settles
          return page;
        }
        const valid =
          data &&
          Array.isArray(data.items) &&
          data.items.length <= 100 &&
          typeof data.has_more === 'boolean' &&
          (data.has_more
            ? typeof data.next_after === 'string' &&
              data.next_after.length > 0 &&
              data.next_after.length <= 128 &&
              data.next_after !== after
            : !data.next_after) &&
          data.items.every(
            item =>
              typeof item?.workspace_id === 'string' &&
              item.workspace_id &&
              item.welcome?.state &&
              item.review?.state
          ) &&
          new Set(data.items.map(item => item.workspace_id)).size === data.items.length;
        if (!response.ok || !valid) {
          if (response.status === 401 || response.status === 403) {
            generation++;
            entries.clear();
            pageRead = null;
            pageInvalidated = false;
            for (const listener of listeners) {
              try {
                listener('', empty());
              } catch (_) {
                /* Other presenters still clear. */
              }
            }
            publishPage({ ...emptyPage(), state: 'unavailable' });
          } else {
            publishPage({
              ...previous,
              state: 'unavailable',
              after,
              stale: previous.ids.length > 0
            });
          }
          return page;
        }
        for (const dataItem of data.items) {
          const item = entry(dataItem.workspace_id);
          // An individual read/action requested after this page started wins.
          if (item.version > version || item.mutation) continue;
          item.version = version;
          accept(dataItem.workspace_id, item, dataItem);
        }
        // Only a complete first page proves that omitted cached identities are
        // gone. A partial window must never erase work on another page.
        if (!after && !data.has_more) {
          const owned = new Set(data.items.map(item => item.workspace_id));
          for (const [id, item] of entries) {
            if (!owned.has(id) && item.version <= version && !item.mutation) {
              item.version = version;
              unavailable(id, item, 404);
            }
          }
        }
        publishPage({
          state: 'ready',
          ids: data.items.map(item => item.workspace_id),
          after,
          nextAfter: data.has_more ? data.next_after : '',
          hasMore: data.has_more,
          stale: false
        });
      } catch (_) {
        if (scope === generation)
          publishPage({ ...previous, state: 'unavailable', after, stale: previous.ids.length > 0 });
      }
      return page;
    })();
    const request = { after, work };
    pageRead = request;
    void work.finally(() => {
      if (pageRead !== request) return;
      pageRead = null;
      prune();
      if (pageInvalidated && scope === generation) {
        pageInvalidated = false;
        void refreshPage({ after });
      }
    });
    return work;
  }
  function invalidatePage(changedID) {
    // A hint can concern a cached workspace outside the selected window. Keep
    // its observation, but revoke its action authority without walking/fetching
    // other pages. An older pending acknowledgement must not unqualify it.
    const changed = typeof changedID === 'string' ? entries.get(changedID) : null;
    if (changed?.view.data && !page.ids.includes(changedID)) {
      changed.minimumVersion = changed.version = ++serial;
      unavailable(changedID, changed);
    }
    if (pageRead) pageInvalidated = true;
    else void refreshPage({ after: page.after });
  }
  function acknowledge(id, message) {
    const item = entry(id);
    const scope = generation;
    const prior = item.mutation || Promise.resolve();
    // Serialize exact-message mutations so old GETs or parallel acks cannot
    // repaint an older unread snapshot after a committed seen response.
    const version = ++serial;
    item.version = version;
    acknowledgementEpoch++;
    item.read = null;
    const work = prior.then(async () => {
      if (scope !== generation) return false;
      try {
        const response = await fetchImpl(`/api/workspaces/${encodeURIComponent(id)}/updates/seen`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            session_id: message.session_id,
            message_id: message.message_id,
            revision: message.revision
          })
        });
        const data = response.ok ? await response.json() : null;
        if (scope !== generation) return false;
        if (
          response.ok &&
          data?.workspace_id === id &&
          data.welcome?.state &&
          (version < (item.minimumVersion || 0) || accept(id, item, data))
        ) {
          for (const listener of committedListeners) {
            try {
              listener(id);
            } catch (_) {
              /* Hints never decide committed seen state. */
            }
          }
          return true;
        }
        unavailable(id, item, response.status);
      } catch (_) {
        if (scope === generation) unavailable(id, item);
      }
      return false; // lost response is not an optimistic acknowledgement
    });
    item.mutation = work;
    void work.finally(() => {
      if (item.mutation === work) item.mutation = null;
      prune();
    });
    return work;
  }
  return {
    refresh,
    invalidate,
    acknowledge,
    refreshPage,
    invalidatePage,
    readPage: () => page,
    subscribePage(listener) {
      pageListeners.add(listener);
      return () => pageListeners.delete(listener);
    },
    read: id => entries.get(id)?.view || empty(),
    subscribeCommitted(listener) {
      committedListeners.add(listener);
      return () => committedListeners.delete(listener);
    },
    subscribe(listener) {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    reset() {
      generation++;
      entries.clear();
      pageRead = null;
      pageInvalidated = false;
      publishPage(emptyPage());
      for (const listener of listeners) {
        try {
          listener('', empty());
        } catch (_) {
          /* Other presenters still clear. */
        }
      }
    }
  };
}

export const workspaceUpdates = createWorkspaceUpdates();

// A mounted workspace shares one canonical summary watcher with its presenters.
// Existing console notifications cover preparation/manual scans/decisions/apply/
// undo. They are hints only. The slow active-workspace read also catches automatic
// scans or lost hints; it is not whole-tree polling or an execution callback.
export function watchWorkspaceUpdates(
  id,
  {
    updates = workspaceUpdates,
    document: doc = globalThis.document,
    window: win = globalThis.window,
    setTimer = setInterval,
    clearTimer = clearInterval
  } = {}
) {
  let disposed = false;
  let signature = '';
  const transport = workspaceUpdateHintTransport({
    updates,
    document: doc,
    window: win,
    changed: changedID => {
      if (changedID === id) refresh();
    }
  });
  const refresh = () => {
    if (!disposed && doc?.visibilityState !== 'hidden') updates.invalidate(id);
  };
  const changed = event => {
    const snapshot = event.detail?.state;
    const state = snapshot?.actionState;
    if (
      snapshot?.workspaceId !== id ||
      !state ||
      [state.status?.state, state.batch?.state].includes('loading')
    )
      return;
    const next = JSON.stringify([
      snapshot.generation,
      state.status,
      state.batch,
      state.applies,
      state.rootId,
      state.batchId,
      state.pendingCount,
      state.needsReviewCount,
      state.readiness,
      state.scanning,
      state.latestReceipt
    ]);
    if (next === signature) return;
    signature = next;
    transport.publish(id);
    refresh();
  };
  doc?.addEventListener('ori:file-janitor-changed', changed);
  doc?.addEventListener('visibilitychange', refresh);
  for (const event of ['focus', 'pageshow', 'popstate']) win?.addEventListener(event, refresh);
  const timer = setTimer(refresh, 30000);
  refresh();
  return () => {
    disposed = true;
    transport.dispose();
    clearTimer(timer);
    doc?.removeEventListener('ori:file-janitor-changed', changed);
    doc?.removeEventListener('visibilitychange', refresh);
    for (const event of ['focus', 'pageshow', 'popstate']) win?.removeEventListener(event, refresh);
  };
}

// Broadcast only an opaque workspace identity, never counts, paths, messages or
// authority. Receiving tabs re-read their own owner-scoped source; no echo and
// no localStorage unread counters. A missing channel recovers on return/poll.
export function workspaceUpdateHintTransport({
  updates = workspaceUpdates,
  document: doc = globalThis.document,
  window: win = globalThis.window,
  changed = () => {}
} = {}) {
  const source = globalThis.crypto?.randomUUID?.() || `${Date.now()}-${Math.random()}`;
  let channel = null;
  let disposed = false;
  let suspended = false;
  const validID = id => typeof id === 'string' && /^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$/.test(id);
  const received = event => {
    const hint = event.data;
    if (
      disposed ||
      suspended ||
      doc?.visibilityState === 'hidden' ||
      hint?.kind !== 'workspace-updates' ||
      hint.source === source ||
      !validID(hint.workspace_id)
    )
      return;
    changed(hint.workspace_id);
  };
  const connect = () => {
    suspended = false;
    if (disposed || channel || typeof win?.BroadcastChannel !== 'function') return;
    try {
      channel = new win.BroadcastChannel('ori.workspace-updates');
      channel.addEventListener('message', received);
    } catch (_) {
      channel = null;
    }
  };
  const suspend = () => {
    suspended = true;
    channel?.removeEventListener('message', received);
    channel?.close();
    channel = null;
  };
  const publish = id => {
    if (disposed || suspended || !validID(id)) return;
    try {
      channel?.postMessage({ kind: 'workspace-updates', workspace_id: id, source });
    } catch (_) {
      /* Return reads recover lost hints. */
    }
  };
  const reset = event => {
    if (event.key === 'ori.reset.generation' && event.newValue && event.newValue !== event.oldValue)
      updates.reset?.();
  };
  const release = updates.subscribeCommitted?.(publish);
  win?.addEventListener('storage', reset);
  win?.addEventListener('pagehide', suspend);
  win?.addEventListener('pageshow', connect);
  connect();
  return {
    publish,
    dispose() {
      disposed = true;
      suspend();
      release?.();
      win?.removeEventListener('storage', reset);
      win?.removeEventListener('pagehide', suspend);
      win?.removeEventListener('pageshow', connect);
    }
  };
}

// Recovery reads only the currently selected <=100-workspace window once per
// visible minute. It never follows continuations or polls the entire tree.
export function watchWorkspaceUpdatePage({
  updates = workspaceUpdates,
  document: doc = globalThis.document,
  window: win = globalThis.window,
  isActive = () => true,
  setTimer = setInterval,
  clearTimer = clearInterval
} = {}) {
  let disposed = false;
  const signatures = new Map();
  const refresh = changedID => {
    if (!disposed && isActive() && doc?.visibilityState !== 'hidden')
      updates.invalidatePage(changedID);
  };
  const transport = workspaceUpdateHintTransport({
    updates,
    document: doc,
    window: win,
    changed: refresh
  });
  const filingChanged = event => {
    const snapshot = event.detail?.state;
    const state = snapshot?.actionState;
    if (
      !snapshot?.workspaceId ||
      !state ||
      [state.status?.state, state.batch?.state].includes('loading')
    )
      return;
    const signature = JSON.stringify([
      snapshot.generation,
      state.status,
      state.batch,
      state.rootId,
      state.batchId,
      state.pendingCount,
      state.needsReviewCount,
      state.latestReceipt
    ]);
    if (signatures.get(snapshot.workspaceId) === signature) return;
    signatures.set(snapshot.workspaceId, signature);
    if (signatures.size > 100) signatures.delete(signatures.keys().next().value);
    transport.publish(snapshot.workspaceId);
    refresh(snapshot.workspaceId);
  };
  doc?.addEventListener('ori:file-janitor-changed', filingChanged);
  doc?.addEventListener('visibilitychange', refresh);
  for (const event of ['focus', 'pageshow', 'popstate', 'ori:workspaces-changed'])
    win?.addEventListener(event, refresh);
  const timer = setTimer(refresh, 60000);
  refresh();
  return () => {
    disposed = true;
    clearTimer(timer);
    transport.dispose();
    doc?.removeEventListener('ori:file-janitor-changed', filingChanged);
    doc?.removeEventListener('visibilitychange', refresh);
    for (const event of ['focus', 'pageshow', 'popstate', 'ori:workspaces-changed'])
      win?.removeEventListener(event, refresh);
  };
}

// Exposure records visibility, not comprehension. For long messages, their
// beginning and at least 80px must be inside the viewport. No scroll/focus
// movement is performed here, and compact previews cannot satisfy this test.
export function exposedWelcomeMessages({
  source,
  sessionId,
  open,
  visible,
  focused,
  settled,
  blocked,
  rows = [],
  viewport
}) {
  if (
    source?.state !== 'ready' ||
    source.session_id !== sessionId ||
    !open ||
    !visible ||
    !focused ||
    !settled ||
    blocked ||
    !viewport ||
    viewport.height <= 0
  )
    return [];
  return (source.messages || []).filter(message => {
    if (message.seen_at) return false;
    const row = rows.find(
      row => row.messageId === message.message_id && row.sessionId === source.session_id
    );
    const bounds = row?.bounds;
    return (
      bounds?.height > 0 &&
      bounds.top >= viewport.top - 1 &&
      bounds.top + Math.min(bounds.height, 80) <= viewport.bottom &&
      bounds.right > viewport.left &&
      bounds.left < viewport.right
    );
  });
}
