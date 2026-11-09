// commander-map.js — where the Commander stands on each map (PRD
// commander-speaks-on-map). An adapter per host gives the balloon its map
// root (the non-scaling viewport), its anchor and visible area, and tells
// the folder agent host whether a map is showing and when it was redrawn.
//
// The maps themselves only announce: the workspace page dispatches
// 'ori:workspace-map-rendered' after each Map render, OriWorkspaceMap
// 'ori:map-mounted' after each mount and 'ori:map-camera' on each camera move.

import { BALLOON_NARROW_PX, commanderBalloon } from './commander-balloon.js';
import {
  commanderAutoOpen,
  commanderQueue,
  commanderSpeech,
  renderCommanderOneLine
} from './commander-speech.js';
import { COMMANDER_ACTIVITY_EVENT } from './commander-tickets.js';

export const WORKSPACE_MAP_RENDERED_EVENT = 'ori:workspace-map-rendered';
export const MAP_MOUNTED_EVENT = 'ori:map-mounted';
export const VIEW_MODE_EVENT = 'ori:workspace-view-mode-changed';
export const HOME_VIEW_EVENT = 'ori:home-view-changed';

// A stand-in anchor for a map with no Commander unit (FR1): a small spot at
// the top centre of the map, where the workspace's own lot sits.
export function lotAnchor(root) {
  const r = root?.getBoundingClientRect?.();
  if (!r) return null;
  return { left: r.left + r.width / 2 - 20, top: r.top + 48, width: 40, height: 40 };
}

// workspaceMapRoot finds the Map tab's viewport on the workspace page: the
// group's own OriWorkspaceMap canvas, else the operations map.
export function workspaceMapRoot(doc) {
  return (
    doc?.querySelector?.('.ws-cmd-detachment-host .ws-map-canvas') ||
    doc?.querySelector?.('.ws-cmd-opmap:not(.is-combined)') ||
    null
  );
}

// workspaceCommanderAnchor is the unit marked Commander on that map: the
// operations map's command node, or a group's Commander unit (FR1).
export function workspaceCommanderAnchor(root) {
  if (!root) return null;
  return (
    root.querySelector?.('.ws-cmd-map-agent.is-command-node') ||
    root.querySelector?.('.ws-map-unit.is-commander') ||
    lotAnchor(root)
  );
}

// workspaceCommanderMap is the workspace page's adapter for one workspace.
export function workspaceCommanderMap(
  workspaceID,
  { doc = globalThis.document, win = globalThis.window } = {}
) {
  const balloon = commanderBalloon(`workspace:${workspaceID}`, { doc });
  const root = () => workspaceMapRoot(doc);
  const narrow = () => {
    const width = Number(win?.innerWidth) || 0;
    return width > 0 && width < BALLOON_NARROW_PX;
  };
  const adapter = {
    balloon,
    narrow,
    // The owner is on the Map tab (FR10's "on this map").
    showing: () => doc?.body?.dataset?.workspaceViewMode === 'map',
    // The Map tab is showing and its map is on the page (FR13, FR14).
    visible: () => doc?.body?.dataset?.workspaceViewMode === 'map' && Boolean(root()),
    attach({ onNarrow } = {}) {
      const at = root();
      if (!at) return false;
      balloon.attach(at, {
        anchor: () => workspaceCommanderAnchor(root()),
        narrow,
        onNarrow,
        visibleArea: () =>
          visibleMapArea(
            root()?.getBoundingClientRect?.(),
            pageObstacles(doc, win).concat(mapObstacles(root())),
            windowRect(win)
          )
      });
      return true;
    },
    // reveal brings the map into view before the balloon opens (Review).
    reveal() {
      scrollClear(root(), { doc, win });
    },
    // onActivity hands fn the Commander's ticket activity on this
    // workspace's map (operations-map-activity.js), or null when it has none.
    onActivity(fn) {
      const handler = event => {
        const detail = event?.detail || null;
        if (detail && String(detail.workspaceId || '') !== String(workspaceID)) return;
        fn(detail);
      };
      doc?.addEventListener?.(COMMANDER_ACTIVITY_EVENT, handler);
      return () => doc?.removeEventListener?.(COMMANDER_ACTIVITY_EVENT, handler);
    },
    // onChange runs fn after a Map render, a map mount or a tab change.
    onChange(fn) {
      const handler = () => fn();
      const events = [WORKSPACE_MAP_RENDERED_EVENT, MAP_MOUNTED_EVENT, VIEW_MODE_EVENT];
      events.forEach(type => doc?.addEventListener?.(type, handler));
      win?.addEventListener?.('resize', handler);
      win?.addEventListener?.('scroll', handler, { passive: true });
      return () => {
        events.forEach(type => doc?.removeEventListener?.(type, handler));
        win?.removeEventListener?.('resize', handler);
        win?.removeEventListener?.('scroll', handler);
      };
    }
  };
  return adapter;
}

const OBSTACLE_GAP = 8;

// visibleMapArea is the part of the map the owner can see: the map's rect
// inside the browser window (viewport), less whatever floats over it (the
// assistant drawer, its launcher). Each obstacle cuts the one side that
// keeps the most room ("must not hide under the drawer"). All rects in
// client coordinates; closed or hidden obstacles are left out.
export function visibleMapArea(mapRect, obstacles = [], viewport = null) {
  if (!mapRect) return null;
  const area = {
    left: mapRect.left,
    top: mapRect.top,
    right: mapRect.left + mapRect.width,
    bottom: mapRect.top + mapRect.height
  };
  if (viewport && viewport.width > 0 && viewport.height > 0) {
    area.left = Math.max(area.left, viewport.left || 0);
    area.top = Math.max(area.top, viewport.top || 0);
    area.right = Math.min(area.right, (viewport.left || 0) + viewport.width);
    area.bottom = Math.min(area.bottom, (viewport.top || 0) + viewport.height);
  }
  const list = Array.isArray(obstacles) ? obstacles : [obstacles];
  list.forEach(rect => {
    if (!rect || !(rect.width > 0) || !(rect.height > 0)) return;
    const r = {
      left: rect.left,
      top: rect.top,
      right: rect.left + rect.width,
      bottom: rect.top + rect.height
    };
    const overlaps =
      r.left < area.right && r.right > area.left && r.top < area.bottom && r.bottom > area.top;
    if (!overlaps) return;
    const width = area.right - area.left;
    const height = area.bottom - area.top;
    const cuts = [
      { side: 'right', value: r.left - OBSTACLE_GAP, room: (r.left - area.left) * height },
      { side: 'left', value: r.right + OBSTACLE_GAP, room: (area.right - r.right) * height },
      { side: 'bottom', value: r.top - OBSTACLE_GAP, room: (r.top - area.top) * width },
      { side: 'top', value: r.bottom + OBSTACLE_GAP, room: (area.bottom - r.bottom) * width }
    ];
    const best = cuts.reduce((a, b) => (b.room > a.room ? b : a));
    if (best.side === 'right') area.right = Math.max(area.left, best.value);
    if (best.side === 'left') area.left = Math.min(area.right, best.value);
    if (best.side === 'bottom') area.bottom = Math.max(area.top, best.value);
    if (best.side === 'top') area.top = Math.min(area.bottom, best.value);
  });
  return {
    left: area.left,
    top: area.top,
    width: Math.max(0, area.right - area.left),
    height: Math.max(0, area.bottom - area.top)
  };
}

// pageObstacles are the floating things on every page that a balloon must
// not hide under: the assistant drawer while open and its launcher.
export function pageObstacles(doc = globalThis.document, win = globalThis.window) {
  const rects = [];
  const rectOf = node =>
    node && !node.hidden && typeof node.getBoundingClientRect === 'function'
      ? node.getBoundingClientRect()
      : null;
  if (win?.PersonalAssistantPanel?.isOpen?.())
    rects.push(rectOf(doc?.getElementById?.('personalAssistantPanel')));
  rects.push(rectOf(doc?.getElementById?.('personalAssistantLauncher')));
  return rects.filter(Boolean);
}

const CLEAR_GAP = 12;

// clearScroll is how far to scroll the page so el is in view below the
// sticky navbar (navBottom, client px) and above the fold (viewportHeight):
// its top just under the navbar when top is asked for, it does not fit, or
// it starts above; else its bottom at the fold when it ends below. 0 when it
// is already in view.
export function clearScroll(rect, navBottom, viewportHeight, { top = false } = {}) {
  if (!rect) return 0;
  const ceiling = Math.max(0, Number(navBottom) || 0) + CLEAR_GAP;
  const floor = Math.max(ceiling, (Number(viewportHeight) || 0) - CLEAR_GAP);
  const height = rect.height ?? rect.bottom - rect.top;
  const bottom = rect.top + height;
  if (top || rect.top < ceiling || height > floor - ceiling) return rect.top - ceiling;
  if (bottom > floor) return bottom - floor;
  return 0;
}

// scrollClear brings el into view below the page's sticky navbar.
export function scrollClear(
  el,
  { top = false, doc = globalThis.document, win = globalThis.window } = {}
) {
  const rect = el?.getBoundingClientRect?.();
  if (!rect || typeof win?.scrollBy !== 'function') return;
  const nav = doc?.querySelector?.('nav.navbar')?.getBoundingClientRect?.();
  const by = clearScroll(rect, nav ? nav.bottom : 0, win.innerHeight, { top });
  if (by) win.scrollBy({ top: by, behavior: 'smooth' });
}

// mapObstacles are a map's own floating controls (zoom, Fit all, Arrange,
// the dock tools and the compass).
// They float beside the viewport, not inside it, so they are found from the
// map's own container (Home's #cockpitMap, a group page's detachment host).
export function mapObstacles(root) {
  const container =
    root?.closest?.('#cockpitMap, .ws-cmd-detachment-host, [data-ws-map-root]') ||
    root?.parentElement ||
    root;
  return Array.from(
    container?.querySelectorAll?.('.ws-map-controls, .ws-map-dock-tools, .ws-map-compass') || []
  )
    .filter(node => !node.hidden)
    .map(node => node.getBoundingClientRect?.())
    .filter(rect => rect && rect.width > 0 && rect.height > 0);
}

// showTile answers a docked balloon's Show (FR11): the map reveals the tile,
// then the camera moves once more so the tile sits in the middle of the part
// of the map the owner can see, not under the drawer.
export function showTile(workspaceID, anchor, visibleArea, win = globalThis.window) {
  const map = win?.OriWorkspaceMap;
  if (!map?.reveal) return false;
  map.reveal(workspaceID);
  const at = anchor()?.getBoundingClientRect?.();
  const area = visibleArea();
  if (at && area && typeof map.panBy === 'function') {
    map.panBy(
      at.left + at.width / 2 - (area.left + area.width / 2),
      at.top + at.height / 2 - (area.top + area.height / 2)
    );
  }
  return true;
}

// windowRect is the browser window, for keeping a balloon above the fold.
export function windowRect(win = globalThis.window) {
  const width = Number(win?.innerWidth) || 0;
  const height = Number(win?.innerHeight) || 0;
  return width && height ? { left: 0, top: 0, width, height } : null;
}

function selectorValue(value) {
  const id = String(value || '');
  if (globalThis.CSS?.escape) return globalThis.CSS.escape(id);
  return /^[A-Za-z0-9_-]+$/.test(id) ? id : '';
}

// homeTileAnchor is where a folder workspace stands on Home's map (FR2): its
// district's header once the Group stage made it a district, else its tile
// (which, after the Home stage, sits inside the Home's district).
export function homeTileAnchor(root, workspaceID) {
  const id = selectorValue(workspaceID);
  if (!root || !id) return null;
  return (
    root.querySelector?.(`.ws-map-district[data-group-id="${id}"] .ws-map-district-header`) ||
    root.querySelector?.(`.ws-map-tile[data-ws-id="${id}"]`) ||
    null
  );
}

// homeCommanderMap is Home's adapter for one folder workspace: the balloon
// speaks from its tile on the cockpit map, laid out beside the drawer.
export function homeCommanderMap(
  workspaceID,
  { doc = globalThis.document, win = globalThis.window } = {}
) {
  const balloon = commanderBalloon(`home:${workspaceID}`, { doc });
  const root = () => doc?.querySelector?.('#cockpitMap .ws-map-canvas') || null;
  const narrow = () => {
    const width = Number(win?.innerWidth) || 0;
    return width > 0 && width < BALLOON_NARROW_PX;
  };
  const anchor = () => homeTileAnchor(root(), workspaceID);
  const visibleArea = () =>
    visibleMapArea(
      root()?.getBoundingClientRect?.(),
      pageObstacles(doc, win).concat(mapObstacles(root())),
      windowRect(win)
    );
  return {
    balloon,
    narrow,
    // Home shows its map (FR10's "on this map"), drawn or about to be.
    showing: () =>
      Boolean(doc?.getElementById?.('cockpitMap')) &&
      doc?.getElementById?.('homeCockpit')?.dataset?.view !== 'tree',
    // The tile is on the map the owner is looking at: Map view (not Tree),
    // drawn, and not filtered out (FR15).
    visible: () => {
      if (doc?.getElementById?.('homeCockpit')?.dataset?.view === 'tree') return false;
      const at = anchor();
      if (!at) return false;
      const tile = at.closest?.('.ws-map-tile') || null;
      return !tile?.classList?.contains?.('is-filtered-out');
    },
    attach({ onNarrow } = {}) {
      const at = root();
      if (!at) return false;
      balloon.attach(at, {
        anchor,
        narrow,
        onNarrow,
        visibleArea,
        // Show pans the camera to the tile; the camera never moves by itself.
        onShow: () => showTile(workspaceID, anchor, visibleArea, win)
      });
      return true;
    },
    reveal() {
      scrollClear(root(), { doc, win });
    },
    onChange(fn) {
      const handler = () => fn();
      // The placement slide plays behind the balloon; it moves once the
      // motion settles (FR12). The map is told to expect the motion by the
      // same event, so the hold asks after it.
      const placement = event => {
        if (!event?.detail?.placement) return;
        setTimeout(() => {
          balloon.holdUntil(win?.OriWorkspaceMap?.whenMotionSettled?.(1000));
        }, 0);
      };
      const docEvents = [MAP_MOUNTED_EVENT, HOME_VIEW_EVENT];
      const winEvents = [
        'resize',
        'scroll',
        'personal-assistant:opened',
        'personal-assistant:closed'
      ];
      docEvents.forEach(type => doc?.addEventListener?.(type, handler));
      winEvents.forEach(type => win?.addEventListener?.(type, handler));
      doc?.addEventListener?.('personal-assistant:opened', handler);
      doc?.addEventListener?.('personal-assistant:closed', handler);
      win?.addEventListener?.('ori:folder-agent-changed', placement);
      return () => {
        docEvents.forEach(type => doc?.removeEventListener?.(type, handler));
        winEvents.forEach(type => win?.removeEventListener?.(type, handler));
        doc?.removeEventListener?.('personal-assistant:opened', handler);
        doc?.removeEventListener?.('personal-assistant:closed', handler);
        win?.removeEventListener?.('ori:folder-agent-changed', placement);
      };
    }
  };
}

// homeCommanders keeps a balloon for every folder agent Home's map lists
// that the drawer is not following (FR2, FR3): each speaks its collapsed line
// from its tile and opens only when the owner asks. sync(views, owned) takes
// the listing's raw views and the drawer's workspace id; actions post through
// act(view, action, extra), which returns the next raw view or null.
export function homeCommanders({
  doc = globalThis.document,
  win = globalThis.window,
  reportView,
  renderCard,
  act
} = {}) {
  const live = new Map();
  const present = (id, raw) => {
    let entry = live.get(id);
    if (!entry) {
      entry = {
        presenter: commanderPresenter(homeCommanderMap(id, { doc, win }), { watched: false })
      };
      live.set(id, entry);
    }
    const view = reportView(raw);
    let error = '';
    const card = compact =>
      renderCard(view, {
        compact,
        error,
        onAction: async (action, button, extra) => {
          if (button) button.disabled = true;
          const result = await act(view, action, extra);
          error = result?.error || '';
          present(id, result?.view || raw);
        }
      });
    entry.presenter.present({
      view,
      raw,
      previous: null,
      followed: false,
      error,
      opening: false,
      host: null,
      doc,
      card
    });
  };
  return {
    sync(views, owned = '') {
      const keep = new Set();
      (Array.isArray(views) ? views : []).forEach(raw => {
        const id = String(raw?.workspace_id || '');
        if (!id || id === owned) return;
        keep.add(id);
        present(id, raw);
      });
      [...live.keys()].forEach(id => {
        if (keep.has(id)) return;
        // The drawer now speaks for this one: hand its balloon over as it is.
        if (id === owned) live.get(id).presenter.release();
        else live.get(id).presenter.dispose();
        live.delete(id);
      });
    },
    ids: () => [...live.keys()]
  };
}

function pageVisible(doc) {
  return !doc?.visibilityState || doc.visibilityState === 'visible';
}

// commanderPresenter joins a folder agent host to a map adapter: the
// Commander's balloon carries the compact card and its collapsed line, and
// the host shows the one-line card while the map is showing (FR13) or the
// full card everywhere else (FR14, FR17). present(args) returns whether it
// drew the host; present(null) takes the balloon off the map (FR30).
// watched false is an agent the owner is not following: its balloon never
// opens by itself (FR10). A null host draws the balloon alone.
// The folder report's place in the urgency order (FR25).
const REPORT_KIND = {
  report: 'report',
  cause: 'cause',
  stopped: 'stopped',
  done: 'done',
  running: 'working',
  looking: 'working'
};

// ticketItems(activity) turns the Commander's ticket activity (the
// 'ori:commander-activity' detail, through map.onActivity) into balloon items
// (commander-tickets.js); the balloon then carries both, most urgent first.
export function commanderPresenter(
  map,
  { onReview, action, watched = true, ticketItems = null } = {}
) {
  let last = null;
  let tickets = [];
  let activity = null;
  let shift = 0;
  let hostMode = '';
  let pendingOpen = '';

  const queue = () => {
    // The report goes first among equals: a look or a setup run in progress
    // outranks the ticket line the same work produces ("Thinking it through…").
    const items = [];
    const speech = last ? commanderSpeech(last.view) : null;
    if (speech) items.push({ kind: REPORT_KIND[speech.phase] || 'working', report: true, speech });
    items.push(...tickets);
    const { first, rest } = commanderQueue(items);
    return first ? [first, ...rest] : [];
  };

  // content is what the balloon says now: one item, "and N more" stepping
  // to the next. null when the Commander has nothing to say (FR30).
  const content = () => {
    const items = queue();
    if (!items.length) return null;
    const index = shift % items.length;
    const item = items[index];
    const more = items.length > 1 ? { label: `and ${items.length - 1} more` } : null;
    const onMore = () => {
      shift += 1;
      map.balloon.update(content());
      map.balloon.focusFirst?.();
    };
    if (item.report) {
      const { view, raw, error, opening, card } = last;
      return {
        speaker: { name: view.agent, role: 'Commander' },
        line: item.speech.line,
        headline: item.speech.headline,
        announce: item.speech.announce,
        signature: `report|${JSON.stringify(raw)}|${error}|${opening}|${index}`,
        // A new phase (the plan becoming the run, the run the receipt)
        // starts at its top, not at the old scroll position.
        scrollKey: `report|${item.speech.phase}`,
        body: () => card(true),
        more,
        onMore
      };
    }
    return {
      speaker: { name: item.agent || last?.view.agent || 'Commander', role: 'Commander' },
      line: item.line,
      headline: item.headline,
      announce: item.announce,
      signature: `${item.signature}|${index}`,
      scrollKey: `${item.kind}|${index}`,
      body: doc => item.body(doc),
      more,
      onMore
    };
  };

  // revealHost answers the collapsed line below 720 px: no balloon there, so
  // the full card the host shows comes into view instead (FR17).
  const revealHost = () => {
    const host = last?.host;
    if (!host) return;
    scrollClear(host, { top: true });
    const first = host.querySelector?.('button:not([disabled]), input:not([disabled]), a[href]');
    first?.focus?.({ preventScroll: true });
  };

  // draw: report says the folder agent's view is new (auto-open may apply).
  const draw = ({ report = false, refresh = false } = {}) => {
    const attached = map.attach({ onNarrow: revealHost });
    if (report || refresh || !map.balloon.content) map.balloon.update(content());
    if (!last) {
      hostMode = '';
      return false;
    }
    const fresh = report;
    const { view, previous, followed, host, doc, card } = last;
    const speech = commanderSpeech(view);
    if (!speech) {
      if (hostMode !== 'card' || fresh) host?.replaceChildren(card(false));
      hostMode = 'card';
      return true;
    }
    const onMap = attached && map.visible() && !map.narrow();
    // An opening the rules call for waits for the map when the anchor is not
    // drawn yet (a new folder's tile arrives with the next refresh).
    if (fresh && watched) {
      const watching = pageVisible(doc) && (map.showing ? map.showing() : true);
      if (commanderAutoOpen({ previous, next: view, followed, onMap: watching }))
        pendingOpen = view.state;
      else if (pendingOpen !== view.state) pendingOpen = '';
    }
    if (onMap) {
      // ...and for the anchor to be in view: a balloon opened for a map below
      // the fold would dock with nothing of it showing.
      const inView = map.balloon.anchorInView ? map.balloon.anchorInView() : true;
      if (pendingOpen && pendingOpen === view.state && inView) {
        pendingOpen = '';
        // Opening by itself never takes focus from where the owner is.
        map.balloon.expand({ focus: false });
      }
      if (hostMode !== 'line' || fresh) {
        const line = renderCommanderOneLine(view, doc, {
          action,
          onReview: () => {
            map.reveal();
            map.balloon.expand();
            onReview?.();
          }
        });
        host?.replaceChildren(line);
      }
      hostMode = 'line';
      return true;
    }
    if (map.balloon.expanded) map.balloon.collapse({ returnFocus: false });
    if (hostMode !== 'card' || fresh) host?.replaceChildren(card(false));
    hostMode = 'card';
    return true;
  };
  const retick = () => {
    tickets = ticketItems && activity ? ticketItems(activity) : [];
    draw({ refresh: true });
  };
  const stopMap = map.onChange(() => draw());
  const stopActivity =
    ticketItems && map.onActivity
      ? map.onActivity(next => {
          activity = next;
          // Something new from the Commander: the most urgent shows first.
          shift = 0;
          retick();
        })
      : null;
  const stop = () => {
    stopMap?.();
    stopActivity?.();
  };
  return {
    present(args) {
      last = args;
      return draw({ report: true });
    },
    // retick redraws the tickets after an answer, an error or a dismissal.
    retick,
    // release stops following the map but leaves the balloon to its next
    // presenter (the same workspace, spoken for by another host).
    release() {
      stop();
      last = null;
    },
    dispose() {
      stop();
      map.balloon.update(null);
      last = null;
      tickets = [];
      activity = null;
    }
  };
}
