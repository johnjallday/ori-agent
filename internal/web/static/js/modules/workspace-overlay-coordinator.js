/**
 * workspace-overlay-coordinator.js
 *
 * One overlay owner and one documented layer scale for the workspace detail
 * surface (PRD FR116–FR123, FR267). It exists because the current code escalates
 * raw z-index locally — the Map window at `10010` sits above the task-manager
 * modal at `1050`, so opening the task manager from the Map puts it UNDERNEATH
 * the window that launched it (the reported bug).
 *
 * Responsibilities:
 *  - Provide the single documented layer scale (`LAYER`) mirrored by the CSS
 *    custom properties in workspace-command.css. No surface should invent a
 *    local z-index; it registers with a `kind` and gets its layer from here.
 *  - Enforce that only ONE modal dialog is active at a time. Opening a required
 *    child modal SUSPENDS/REPLACES the owning modal through the coordinator
 *    rather than stacking on a higher arbitrary z-index (FR119).
 *  - Resolve Escape in a predictable topmost-first order: menu/popover, then a
 *    bounded dialog, then collapse the expanded tray/drawer, then leave the
 *    underlying Map untouched (FR123).
 *  - Return focus to an overlay's trigger on close (FR121).
 *
 * DOM side effects (inert marking, focus trap/restore) are injected so the core
 * ordering/single-modal logic is unit-testable without a browser. Group 2 lands
 * this INERT: the coordinator and layer tokens exist and existing modals are
 * routed through it with no behavior change; the drawer/tray adopt it later.
 */

// Documented layer scale. Mirror of the --wsx-layer-* CSS custom properties.
// Values are ordered and spaced so a surface never needs a local escalation.
export const LAYER = Object.freeze({
  BASE: 0,
  MAP: 100, // Operations Map and its in-map windows
  DRAWER: 200, // non-modal task drawer
  TRAY: 300, // sticky execution tray
  POPOVER: 900, // popovers / inline pickers
  MODAL_BACKDROP: 1000, // bounded-dialog backdrop
  MODAL: 1010, // bounded dialog (confirmations/forms)
  MENU: 1100, // context menus / dropdowns (above a dialog)
  TOAST: 2000 // transient notifications, always on top
});

// Overlay kinds and their Escape-dismissal priority (lower = dismissed first).
// menu/popover outrank a dialog; a dialog outranks collapsing a tray/drawer.
const KIND_ESCAPE_PRIORITY = Object.freeze({
  menu: 0,
  popover: 0,
  modal: 1,
  tray: 2,
  drawer: 2
});

const MODAL_KINDS = new Set(['modal']);

export class OverlayCoordinator {
  constructor(effects = {}) {
    this._effects = {
      setInert: effects.setInert || (() => {}),
      releaseInert: effects.releaseInert || (() => {}),
      trapFocus: effects.trapFocus || (() => {}),
      restoreFocus: effects.restoreFocus || (() => {})
    };
    /** @type {Array<object>} open overlays, most-recently-opened last */
    this._stack = [];
  }

  /** z-index for a surface kind (mirrors the CSS tokens). */
  layerFor(kind) {
    switch (kind) {
      case 'map':
        return LAYER.MAP;
      case 'drawer':
        return LAYER.DRAWER;
      case 'tray':
        return LAYER.TRAY;
      case 'popover':
        return LAYER.POPOVER;
      case 'menu':
        return LAYER.MENU;
      case 'toast':
        return LAYER.TOAST;
      case 'modal':
        return LAYER.MODAL;
      default:
        return LAYER.BASE;
    }
  }

  /**
   * Open (register) an overlay.
   * @param {object} o
   * @param {string} o.id - unique id
   * @param {string} o.kind - modal|menu|popover|drawer|tray
   * @param {Function} [o.onClose] - called when the coordinator closes it
   * @param {*} [o.trigger] - element/opaque ref focus returns to on close
   * @param {*} [o.container] - element to trap focus within / mark siblings inert
   * @returns {object} the registered record
   */
  open(o = {}) {
    const record = {
      id: String(o.id || ''),
      kind: String(o.kind || 'modal'),
      onClose: typeof o.onClose === 'function' ? o.onClose : null,
      trigger: o.trigger || null,
      container: o.container || null,
      suspended: false
    };
    if (!record.id) return null;

    // Re-opening an already-open id is a no-op re-focus, not a duplicate.
    const existing = this._stack.find(r => r.id === record.id);
    if (existing) return existing;

    // Single-modal rule: opening a modal suspends any currently-active modal so
    // there is never modal-on-modal stacking (FR117, FR119).
    if (MODAL_KINDS.has(record.kind)) {
      const activeModal = this._topModal();
      if (activeModal) {
        activeModal.suspended = true;
        if (activeModal.onClose) activeModal.onClose({ reason: 'suspended', by: record.id });
        this._effects.releaseInert(activeModal.container);
      }
      if (record.container) this._effects.setInert(record.container);
    }

    this._stack.push(record);
    if (record.container) this._effects.trapFocus(record.container);
    return record;
  }

  /**
   * Close an overlay by id. Returns focus to its trigger. If closing a modal
   * that had suspended a prior modal, the prior one resumes (FR119, FR121).
   */
  close(id) {
    const idx = this._stack.findIndex(r => r.id === String(id || ''));
    if (idx === -1) return false;
    const [record] = this._stack.splice(idx, 1);
    if (record.onClose) record.onClose({ reason: 'closed' });
    if (record.container) this._effects.releaseInert(record.container);
    if (record.trigger) this._effects.restoreFocus(record.trigger);

    if (MODAL_KINDS.has(record.kind)) {
      const resume = this._topModalAny();
      if (resume && resume.suspended) {
        resume.suspended = false;
        if (resume.container) {
          this._effects.setInert(resume.container);
          this._effects.trapFocus(resume.container);
        }
      }
    }
    return true;
  }

  /**
   * Handle Escape: close the single topmost-priority dismissable overlay and
   * report which one, without touching the underlying Map (FR123). Tray/drawer
   * are collapsed (closed) only if present; returns the closed id or null.
   */
  escapeTopmost() {
    if (this._stack.length === 0) return null;
    // Choose by (priority asc, most-recently-opened) so a menu above a dialog
    // closes before the dialog, and the newest of equal priority wins.
    let target = null;
    let best = Infinity;
    for (let i = this._stack.length - 1; i >= 0; i--) {
      const r = this._stack[i];
      if (r.suspended) continue;
      const p = KIND_ESCAPE_PRIORITY[r.kind];
      if (p == null) continue;
      if (p < best) {
        best = p;
        target = r;
      }
    }
    if (!target) return null;
    this.close(target.id);
    return target.id;
  }

  /** The active (non-suspended) modal, or null. At most one ever exists. */
  activeModal() {
    return this._stack.find(r => MODAL_KINDS.has(r.kind) && !r.suspended) || null;
  }

  /** Count of currently-registered modals (active + suspended). */
  modalCount() {
    return this._stack.filter(r => MODAL_KINDS.has(r.kind)).length;
  }

  /** All open overlay ids, base-first. */
  openIds() {
    return this._stack.map(r => r.id);
  }

  _topModal() {
    for (let i = this._stack.length - 1; i >= 0; i--) {
      if (MODAL_KINDS.has(this._stack[i].kind) && !this._stack[i].suspended) return this._stack[i];
    }
    return null;
  }

  // Topmost modal regardless of suspended state — used to resume the owner when
  // the child modal that suspended it closes.
  _topModalAny() {
    for (let i = this._stack.length - 1; i >= 0; i--) {
      if (MODAL_KINDS.has(this._stack[i].kind)) return this._stack[i];
    }
    return null;
  }
}

if (typeof window !== 'undefined') {
  window.OverlayCoordinator = OverlayCoordinator;
  window.OVERLAY_LAYER = LAYER;
}
