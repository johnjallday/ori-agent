// member-commanders.js — the balloon a wait mark opens on a map
// (waiting-agents-on-parent-map).
//
// A workspace whose agent waits on the owner wears a quiet mark on its parent's
// map: a group page's, or Home's top-level map. When the owner activates it, the
// agent speaks its own collapsed line from that place, and its button (Answer or
// Review) opens the same compact card Home's map balloons carry, so the owner
// answers without leaving the page. The defaults are a group page's map; Home
// passes its own root, showing check, events and key prefix.
//
// Rules this module keeps:
//  - Only a mark activation creates a balloon (show). A wait that merely appears
//    adds a mark and nothing else: sync() closes or refreshes the balloon that is
//    shown, it never creates one, so nothing opens by itself and no focus moves.
//  - At most one member balloon at a time, on the map root the page's own
//    Commander also uses. Balloons on one root exclude each other, so the member
//    and the page's Commander are never both expanded. This module never
//    touches the page's Commander and posts nothing to its chat.
//  - The balloon exists only while its member waits. The mark carries
//    aria-expanded for as long as it is shown, and this module is its only
//    writer.
//  - One card for every waiting moment: the words come from commanderSpeech and
//    the card from renderCard, with no branch per phase.

import { BALLOON_NARROW_PX } from './commander-balloon.js';
import {
  commanderPresenter,
  MAP_MOUNTED_EVENT,
  placeCommanderMap,
  VIEW_MODE_EVENT,
  WORKSPACE_MAP_RENDERED_EVENT,
  workspaceMapRoot
} from './commander-map.js';
import { memberWaits } from './member-folder-agents.js';

/**
 * @param {object} options
 * @param {Element|(() => Element|null)} options.host the map's host element,
 *   which holds the marks.
 * @param {(raw: object) => object} options.reportView a server view as the card
 *   and the Commander read it (folderAgentReportView).
 * @param {(view: object, options: object) => Element} options.renderCard draws the
 *   compact card (renderFolderAgentReport).
 * @param {(view: object, action: string, extra?: object) => Promise<{view?: object, error?: string}>} options.act
 *   posts one of the card's actions. It must announce the change itself
 *   (ori:folder-agent-changed); this module does not.
 * @param {(workspaceID: string) => void} [options.open] opens the member's own
 *   page, for a press on the line after the window narrowed.
 * @param {(name: string) => string} [options.face] the agent's face as markup.
 */
export function memberCommanders({
  host,
  doc = globalThis.document,
  win = globalThis.window,
  reportView,
  renderCard,
  act,
  open = null,
  face = null,
  // Which map this is. The defaults are a group page's; Home's cockpit passes
  // its own so the same balloon serves a mark on its top-level map.
  root = () => workspaceMapRoot(doc),
  showing = () => doc?.body?.dataset?.workspaceViewMode === 'map',
  docEvents = [MAP_MOUNTED_EVENT, VIEW_MODE_EVENT, WORKSPACE_MAP_RENDERED_EVENT],
  keyPrefix = 'member'
} = {}) {
  // The one balloon that is shown, or null: {id, placeId, presenter, raw, view,
  // error, adapter}.
  let shown = null;

  const hostElement = () => (typeof host === 'function' ? host() : host) || null;
  const narrow = () => {
    const width = Number(win?.innerWidth) || 0;
    return width > 0 && width < BALLOON_NARROW_PX;
  };
  // Values are compared as attributes and never built into a selector: an id is
  // data, and a mark may have been re-created since the balloon opened.
  const findBy = (selector, attribute, value) => {
    const list = hostElement()?.querySelectorAll?.(selector);
    return list ? Array.from(list).find(node => node.getAttribute?.(attribute) === value) : null;
  };
  const focusPlace = placeId => {
    const target =
      findBy('[data-wait-mark]', 'data-wait-mark', placeId) ||
      findBy('.ws-map-tile', 'data-ws-id', placeId) ||
      findBy('.ws-map-district-tag', 'data-ws-id', placeId);
    if (!target) return false;
    target.focus?.({ preventScroll: true });
    return true;
  };

  // syncMarks writes aria-expanded on every mark that stands for one wait: true
  // on the one whose balloon is shown, false on the rest. A mark that stands for
  // several opens no balloon, so it carries none. Called after every change and
  // after each map mount, because a mount draws its marks without it.
  const syncMarks = () => {
    const list = hostElement()?.querySelectorAll?.('[data-wait-mark]');
    if (!list) return;
    Array.from(list).forEach(mark => {
      if (mark.getAttribute('data-wait-count') !== '1') {
        mark.removeAttribute?.('aria-expanded');
        return;
      }
      const open = Boolean(shown) && shown.placeId === mark.getAttribute('data-wait-mark');
      mark.setAttribute('aria-expanded', open ? 'true' : 'false');
    });
  };

  const close = (id, { returnFocus = false } = {}) => {
    if (!shown || shown.id !== id) return false;
    const entry = shown;
    shown = null;
    // Focus moves first, so the balloon's focusout clears its own focus flag and
    // removing it does not also pull focus to the anchor.
    if (returnFocus) focusPlace(entry.placeId);
    entry.presenter.dispose();
    syncMarks();
    return true;
  };

  const present = (entry, raw) => {
    const view = reportView(raw);
    entry.raw = raw;
    entry.view = view;
    const card = compact =>
      renderCard(view, {
        compact,
        error: entry.error,
        opening: false,
        onAction: (action, button, extra) => answer(entry, action, button, extra)
      });
    entry.presenter.present({
      view,
      raw,
      previous: null,
      followed: false,
      error: entry.error,
      opening: false,
      host: null,
      doc,
      card
    });
  };

  // answer posts one of the card's actions. When the agent no longer waits (it
  // went to work, or finished), the balloon closes; otherwise it follows to the
  // next waiting moment, or shows the error and stays. The 2-second read shows
  // the work as the tile's line; the next wait shows up only as a mark.
  const answer = async (entry, action, button, extra) => {
    if (button) button.disabled = true;
    const result = await act(entry.view, action, extra);
    // Closed while the request ran: nothing is left to draw.
    if (shown !== entry) return;
    entry.error = result?.error || '';
    const next = result?.view || entry.raw;
    // The pressed button was disabled, which drops focus off it, but the balloon
    // still remembers focus was inside its card; it is false only when the owner
    // moved focus elsewhere meanwhile.
    const inside = entry.adapter.balloon.focusInside === true;
    if (!entry.error && !memberWaits([next])[entry.id]) {
      close(entry.id, { returnFocus: inside });
      return;
    }
    present(entry, next);
    // Following to the next question replaces the card the owner was in, so focus
    // goes into the new one instead of falling to the page.
    if (inside && entry.adapter.balloon.expanded) entry.adapter.balloon.focusFirst();
  };

  return {
    /**
     * Show one member's line, because its mark was activated. `detail` is the
     * map's ori:map-wait-show detail; `state` is {views, waits} as the page last
     * read them. Returns whether it took the activation: false leaves the map to
     * open the member's own page (more than one wait, no map, a narrow window,
     * or nothing listed for it).
     */
    show(detail, { views = [], waits = {} } = {}) {
      const id = String(detail?.workspaceId || '');
      const placeId = String(detail?.placeId || id);
      if (!id || Number(detail?.count) !== 1) return false;
      const raw = (Array.isArray(views) ? views : []).find(view => view?.workspace_id === id);
      if (!raw || !waits[id]) return false;
      if (!root() || narrow()) return false;
      // The mark is its own toggle: activating it again closes the line.
      if (shown && shown.id === id) {
        close(id);
        return true;
      }
      if (shown) close(shown.id);
      const adapter = placeCommanderMap(placeId, {
        key: `${keyPrefix}:${id}`,
        root,
        showing,
        docEvents,
        pinLine: true,
        onDismiss: () => close(id, { returnFocus: true }),
        onNarrow: () => open?.(id),
        doc,
        win
      });
      const entry = {
        id,
        placeId,
        adapter,
        error: '',
        raw,
        view: null,
        presenter: commanderPresenter(adapter, { watched: false, face })
      };
      shown = entry;
      present(entry, raw);
      // present() attaches the balloon BEFORE it updates it, and a balloon that
      // was dismissed earlier is kept (removed) and ignores attach until update()
      // brings it back. So a line shown again after the map was re-mounted (a
      // collapse, a layout settle, a refresh) would still hang in the OLD canvas:
      // no line, nothing to focus, until the next mount event. Attach again, now
      // that it is live, so it lands in the map that is on the page.
      adapter.attach();
      adapter.balloon.focusLine();
      syncMarks();
      return true;
    },

    /**
     * The page read the listing again. A shown balloon is drawn from its fresh
     * view, or closed when its member no longer waits. Never creates one.
     */
    sync({ views = [], waits = {} } = {}) {
      if (shown) {
        const raw = (Array.isArray(views) ? views : []).find(
          view => view?.workspace_id === shown.id
        );
        if (!raw || !waits[shown.id]) close(shown.id);
        else present(shown, raw);
      }
      syncMarks();
    },

    syncMarks,

    // The map is gone, or the owner left the Map tab: no balloon outlives it.
    closeAll() {
      if (shown) close(shown.id);
      else syncMarks();
    },

    shownId: () => (shown ? shown.id : '')
  };
}
