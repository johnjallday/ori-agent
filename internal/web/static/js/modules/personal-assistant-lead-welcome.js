// One-time host-written arrival conversation. No /api/chat, synthetic user
// turn, model invocation or task action. The server owns durable deduplication.
export function createLeadWelcome({
  target,
  ready,
  busy,
  deliver,
  fetchImpl = (...args) => fetch(...args),
  setTimer = setTimeout,
  clearTimer = clearTimeout
}) {
  let key = '';
  let generation = 0;
  let inFlight = false;
  let attempts = 0;
  let timer = null;
  let queued = null;
  let quiet = false;
  let done = false;
  let stopped = false;

  function schedule() {
    if (!stopped && !done && attempts < 60 && timer === null) {
      timer = setTimer(() => {
        timer = null;
        void refresh();
      }, 5000);
    }
  }

  async function refresh() {
    if (stopped) return;
    const current = target();
    const nextKey = current ? `${current.workspaceId}\n${current.name}` : '';
    if (key !== nextKey) {
      key = nextKey;
      generation += 1;
      inFlight = false;
      attempts = 0;
      queued = null;
      quiet = false;
      done = false;
    }
    if (!current || current.fresh || done) return;
    if (inFlight) return;
    if (!ready() || busy()) {
      schedule();
      return;
    }
    if (queued) {
      const delivery = queued;
      queued = null;
      if (current.sessionId !== delivery.expectedSessionId) {
        done = true;
        return;
      }
      const token = generation;
      inFlight = true;
      try {
        await deliver(delivery.session_id, { reveal: !quiet });
        if (token === generation && !delivery.awaiting_assessment) done = true;
      } catch (_) {
        // The persisted message remains available through ordinary hydration.
      } finally {
        if (token === generation) {
          inFlight = false;
          schedule();
        }
      }
      return;
    }
    const token = generation;
    const sessionId = current.sessionId;
    inFlight = true;
    attempts += 1;
    try {
      const response = await fetchImpl(
        `/api/workspaces/${encodeURIComponent(current.workspaceId)}/lead-conversation/open`,
        { method: 'POST' }
      );
      if (!response.ok) {
        schedule();
        return;
      }
      const result = await response.json();
      const now = target();
      if (
        token !== generation ||
        !now ||
        now.workspaceId !== current.workspaceId ||
        now.name !== current.name ||
        now.fresh ||
        now.sessionId !== sessionId
      )
        return;
      if (result.state === 'waiting_setup') {
        schedule();
        return;
      }
      if (result.changed && result.session_id) {
        queued = { ...result, expectedSessionId: sessionId };
        if (ready() && !busy()) {
          queued = null;
          await deliver(result.session_id, { reveal: !quiet });
        }
      }
      if (token === generation) done = !result.awaiting_assessment && !queued;
    } catch (_) {
      // An uncertain delivery is never replayed as a new conversation. A
      // retry reads the same server receipt; ordinary chat remains available.
    } finally {
      if (token === generation) {
        inFlight = false;
        schedule();
      }
    }
  }
  return {
    refresh,
    quiet() {
      quiet = true;
    },
    destroy() {
      stopped = true;
      generation += 1;
      if (timer !== null) clearTimer(timer);
    }
  };
}
