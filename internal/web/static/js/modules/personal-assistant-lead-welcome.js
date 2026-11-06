import { workspaceUpdates } from './workspace-updates.js';

// Quiet discovery plus the existing explicit/on-entry legacy fallback. Host
// delivery never selects a session, hydrates chat, opens the drawer or marks
// anything seen. New-workspace recovery does not depend on this controller.
export function createLeadWelcome({
  target,
  ready,
  busy,
  changed = () => {},
  updates = workspaceUpdates,
  fetchImpl = (...args) => fetch(...args),
  setTimer = setTimeout,
  clearTimer = clearTimeout
}) {
  let key = '';
  let generation = 0;
  let inFlight = false;
  let attempts = 0;
  let timer = null;
  let done = false;
  let stopped = false;
  const unsubscribe = updates.subscribe((id, view) => {
    if (!stopped && id === target()?.workspaceId) changed(view);
  });

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
      generation++;
      inFlight = false;
      attempts = 0;
      done = false;
      if (timer !== null) clearTimer(timer);
      timer = null;
      changed(current ? updates.read(current.workspaceId) : null);
    }
    if (!current || inFlight) return;
    const token = generation;
    const stillCurrent = () =>
      !stopped &&
      token === generation &&
      target()?.workspaceId === current.workspaceId &&
      target()?.name === current.name;
    inFlight = true;
    try {
      const view = await updates.refresh(current.workspaceId);
      if (!stillCurrent()) return;
      changed(view);
      // Draft/New/transition guards apply to legacy enrollment only, not to
      // displaying an update the server has already saved independently.
      if (done || target()?.fresh || !ready() || busy()) return;
      attempts++;
      const response = await fetchImpl(
        `/api/workspaces/${encodeURIComponent(current.workspaceId)}/lead-conversation/open`,
        { method: 'POST' }
      );
      if (!response.ok) return;
      const result = await response.json();
      if (!stillCurrent()) return;
      done = result.state !== 'waiting_setup' && !result.awaiting_assessment;
      if (result.changed) {
        const updated = await updates.refresh(current.workspaceId);
        if (stillCurrent()) changed(updated);
      }
    } catch (_) {
      // Uncertain delivery retries only the existing idempotent host receipt.
    } finally {
      if (token === generation) {
        inFlight = false;
        schedule();
      }
    }
  }
  return {
    refresh,
    destroy() {
      stopped = true;
      generation++;
      if (timer !== null) clearTimer(timer);
      unsubscribe();
    }
  };
}
