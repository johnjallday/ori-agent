// Shared, bounded read state. Server records own unread/review truth; this
// cache is a projection, never a counter or acknowledgement authority.
export function createWorkspaceUpdates({ fetchImpl = (...args) => fetch(...args) } = {}) {
  const entries = new Map();
  const listeners = new Set();
  let generation = 0;
  const empty = () => ({ state: 'loading', data: null, stale: false });
  function entry(id) {
    if (!entries.has(id))
      entries.set(id, { view: empty(), version: 0, read: null, mutation: null });
    return entries.get(id);
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
    if (data.welcome.state === 'unavailable' && item.view.data?.welcome) {
      data = { ...data, welcome: { ...item.view.data.welcome, state: 'unavailable', stale: true } };
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
    const version = ++item.version;
    const current = () => scope === generation && version === item.version;
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
      return item.view;
    })();
    item.read = work;
    void work.finally(() => {
      if (item.read === work) item.read = null;
    });
    return work;
  }
  function acknowledge(id, message) {
    const item = entry(id);
    const scope = generation;
    const prior = item.mutation || Promise.resolve();
    // Serialize exact-message mutations so old GETs or parallel acks cannot
    // repaint an older unread snapshot after a committed seen response.
    item.version += 1;
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
        if (response.ok && accept(id, item, data)) return true;
        unavailable(id, item, response.status);
      } catch (_) {
        if (scope === generation) unavailable(id, item);
      }
      return false; // lost response is not an optimistic acknowledgement
    });
    item.mutation = work;
    void work.finally(() => {
      if (item.mutation === work) item.mutation = null;
    });
    return work;
  }
  return {
    refresh,
    acknowledge,
    read: id => entries.get(id)?.view || empty(),
    subscribe(listener) {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    reset() {
      generation++;
      entries.clear();
      for (const listener of listeners) listener('', empty());
    }
  };
}

export const workspaceUpdates = createWorkspaceUpdates();

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
