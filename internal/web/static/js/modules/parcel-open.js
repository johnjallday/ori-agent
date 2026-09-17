/**
 * parcel-open.js — seeing a result anywhere opens its parcel (task-run-show FR40).
 *
 * A finished run waits on the map as a parcel until the user looks at it. The
 * map's own result card opens it directly; every OTHER place a result can be
 * seen — a task's result modal, the Daily Brief, the File Janitor console —
 * calls this, so the parcel does not keep waiting for something already read.
 *
 * It is the sibling of economy-harvest.js and deliberately fire-and-forget: the
 * user opened the result to read it, so a failure here is logged and never
 * interrupts them. A 404 means the feature is switched off, which is not worth
 * a warning at all.
 *
 * @module parcel-open
 */

/**
 * Open every waiting parcel for one task, brief, or janitor scan.
 *
 * @param {{kind: string, workspaceId: string, refId: string}} target
 * @returns {Promise<number|null>} how many parcels were opened, or null when
 *   there was nothing to ask about or the call did not succeed.
 */
export async function openParcelByRef({ kind, workspaceId, refId } = {}) {
  const payload = {
    kind: String(kind || '').trim(),
    workspace_id: String(workspaceId || '').trim(),
    ref_id: String(refId || '').trim()
  };
  if (!payload.kind || !payload.workspace_id || !payload.ref_id) return null;

  try {
    const response = await fetch('/api/workspace-map/parcels/open-by-ref', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload)
    });
    if (response.status === 404) return null;
    if (!response.ok) throw new Error(`HTTP ${response.status}`);
    const result = await response.json();
    return Number((result && result.opened) || 0);
  } catch (err) {
    console.warn('parcel-open: could not mark the result parcel opened', err);
    return null;
  }
}

// Classic scripts (home-daily-brief's older callers, dashboard.js) cannot import
// a module, so the same function is registered on window as well.
if (typeof window !== 'undefined') {
  window.OriParcels = Object.assign(window.OriParcels || {}, { openParcelByRef });
}
