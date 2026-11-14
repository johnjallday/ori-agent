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
import { chatRefOf, FOLDER_AGENT_CHAT_SHOWN_EVENT } from './folder-agent-chat.js';

// The drawer opening or closing, or the report's card appearing in its thread,
// moves the plan between the chat and the balloon (workspace chat sync).
const CHAT_EVENTS = ['personal-assistant:opened', 'personal-assistant:closed'];

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
        // The page shows no line of its own above the header on the Map tab, so
        // the Commander's line stays on the map at any zoom or pan.
        pinLine: true,
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
      // The map may have spoken before this balloon listened (a reload while
      // a ticket waits): ask it to say it again.
      if (typeof CustomEvent === 'function') {
        doc?.dispatchEvent?.(
          new CustomEvent(`${COMMANDER_ACTIVITY_EVENT}-request`, {
            detail: { workspaceId: workspaceID }
          })
        );
      }
      return () => doc?.removeEventListener?.(COMMANDER_ACTIVITY_EVENT, handler);
    },
    // onChange runs fn after a Map render, a map mount or a tab change, and when
    // the drawer opens, closes or shows the report in its thread.
    onChange(fn) {
      const handler = () => fn();
      const events = [
        WORKSPACE_MAP_RENDERED_EVENT,
        MAP_MOUNTED_EVENT,
        VIEW_MODE_EVENT,
        ...CHAT_EVENTS
      ];
      events.forEach(type => doc?.addEventListener?.(type, handler));
      win?.addEventListener?.('resize', handler);
      win?.addEventListener?.('scroll', handler, { passive: true });
      win?.addEventListener?.(FOLDER_AGENT_CHAT_SHOWN_EVENT, handler);
      return () => {
        events.forEach(type => doc?.removeEventListener?.(type, handler));
        win?.removeEventListener?.('resize', handler);
        win?.removeEventListener?.('scroll', handler);
        win?.removeEventListener?.(FOLDER_AGENT_CHAT_SHOWN_EVENT, handler);
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
// the dock tools and the compass, and the workspace Map's Resources button in
// its corner).
// They float beside the viewport, not inside it, so they are found from the
// map's own container (Home's #cockpitMap, a group page's detachment host).
export function mapObstacles(root) {
  const container =
    root?.closest?.('#cockpitMap, .ws-cmd-detachment-host, [data-ws-map-root]') ||
    root?.parentElement ||
    root;
  return Array.from(
    container?.querySelectorAll?.(
      '.ws-map-controls, .ws-map-dock-tools, .ws-map-compass, .ws-cmd-map-toolbar'
    ) || []
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
// district's header once it is a group (the Group stage, or the Home stage,
// which now draws it as a district inside the Home's), else its tile. Its goal
// offer speaks from the same place, so it is one offer, answered once.
export function homeTileAnchor(root, workspaceID) {
  const id = selectorValue(workspaceID);
  if (!root || !id) return null;
  return (
    root.querySelector?.(`.ws-map-district[data-group-id="${id}"] .ws-map-district-header`) ||
    root.querySelector?.(`.ws-map-tile[data-ws-id="${id}"]`) ||
    null
  );
}

// homeMapShowing is whether Home's map is on screen: its map is on the page and
// the Tree view is not showing. It is the check behind Hire & build narrowing the
// chat box (visual-hire FR15, FR21): the workspace page, Needs you and any page
// without the Home map answer false. Same rule as homeCommanderMap's showing().
export function homeMapShowing({ hasMap = false, view = '' } = {}) {
  return hasMap === true && view !== 'tree';
}

// tileIntoAreaDelta is how far the map must pan so a tile lies wholly inside the
// part of the map the owner can see: null when it already does, else the move
// that puts its centre at the centre of that area (showTile's own formula).
export function tileIntoAreaDelta(rect, area) {
  if (!rect || !area || !(area.width > 0) || !(area.height > 0)) return null;
  const inside =
    rect.left >= area.left &&
    rect.top >= area.top &&
    rect.left + rect.width <= area.left + area.width &&
    rect.top + rect.height <= area.top + area.height;
  if (inside) return null;
  return {
    dx: rect.left + rect.width / 2 - (area.left + area.width / 2),
    dy: rect.top + rect.height / 2 - (area.top + area.height / 2)
  };
}

// revealHiredTile brings a just-hired folder's tile into the part of Home's map
// beside the narrowed chat box (visual-hire FR17), once. It waits for the tile
// (the map draws it after its own refresh), lets any placement motion settle, then
// pans only if the tile is not already in the visible area. It never refits the
// map (that would discard the person's pan and zoom) and never calls reveal, which
// would also select the tile. Answers whether the tile is in view.
export async function revealHiredTile(
  workspaceID,
  { doc = globalThis.document, win = globalThis.window, waitMs = 5000 } = {}
) {
  const map = win?.OriWorkspaceMap;
  const root = () => doc?.querySelector?.('#cockpitMap .ws-map-canvas') || null;
  const anchor = () => homeTileAnchor(root(), workspaceID);
  if (!anchor()) {
    const found = await new Promise(resolve => {
      let timer = 0;
      const check = () => {
        if (anchor()) done(true);
      };
      const done = value => {
        clearTimeout(timer);
        doc?.removeEventListener?.(MAP_MOUNTED_EVENT, check);
        resolve(value);
      };
      doc?.addEventListener?.(MAP_MOUNTED_EVENT, check);
      timer = setTimeout(() => done(false), waitMs);
    });
    if (!found) return false;
  }
  await map?.whenMotionSettled?.(1000);
  await new Promise(resolve =>
    win?.requestAnimationFrame ? win.requestAnimationFrame(() => resolve()) : resolve()
  );
  const at = anchor()?.getBoundingClientRect?.();
  if (!at) return false;
  const area = visibleMapArea(
    root()?.getBoundingClientRect?.(),
    pageObstacles(doc, win).concat(mapObstacles(root())),
    windowRect(win)
  );
  const delta = tileIntoAreaDelta(at, area);
  if (!delta) return true;
  return typeof map?.panBy === 'function' && map.panBy(delta.dx, delta.dy) !== false;
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
  // opening is the line a balloon says while Home leaves for where its run
  // landed ("Opening Music Production Home…"), or false.
  const present = (id, raw, opening = false) => {
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
        opening: Boolean(opening),
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
      opening,
      host: null,
      doc,
      card
    });
  };
  return {
    // openings maps a workspace id to the line its balloon says while Home
    // leaves for where that run landed.
    sync(views, owned = '', openings = new Map()) {
      const keep = new Set();
      (Array.isArray(views) ? views : []).forEach(raw => {
        const id = String(raw?.workspace_id || '');
        if (!id || id === owned) return;
        keep.add(id);
        present(id, raw, openings.get(id) || false);
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
  // A goal offer asks the owner something, like a report; so does the
  // premise.
  goal: 'report',
  // The offer to learn the songs asks the owner something too; the reading
  // itself is live work, like a setup run.
  songs: 'report',
  premise: 'report',
  withdrawn: 'report',
  done: 'done',
  running: 'live',
  learning: 'live',
  looking: 'live'
};

// ticketItems(activity) turns the Commander's ticket activity (the
// 'ori:commander-activity' detail, through map.onActivity) into balloon items
// (commander-tickets.js); the balloon then carries both, most urgent first.
// hostLine false is a host that shows nothing while the map speaks (the
// workspace page's Map tab): the Commander's line on the map is the status, and
// the host comes back with the full card wherever no balloon can open.
// onNarrated(ticketID) hears which ticket the report itself narrates (the folder
// agent's own look), '' when none, each time that changes.
// chatHolds() says whether the open chat shows the report (a host that cannot
// pass it with each present, like the workspace page); chatLine.open(ref,
// button) opens the report's message in the chat. With chatLine, a report that
// was posted to the conversation (raw.chat) shows off the map as one line with
// "Open in chat" instead of the full card, and as nothing while the chat shows
// it (workspace chat sync, Plan B).
export function commanderPresenter(
  map,
  {
    onReview,
    action,
    watched = true,
    ticketItems = null,
    hostLine = true,
    onNarrated = null,
    chatHolds = null,
    chatLine = null
  } = {}
) {
  let last = null;
  let narrated = '';
  let tickets = [];
  let activity = null;
  let shift = 0;
  let lastPhase = '';
  let hostMode = '';
  let pendingOpen = '';
  // Whether the chat box holds the plan (visual-hire FR24). Asked live, on every
  // draw, so opening or closing the box moves the plan between it and the balloon.
  const chatHeld = () => {
    const asks = typeof last?.chatHolds === 'function' ? last.chatHolds : chatHolds;
    return typeof asks === 'function' && asks() === true;
  };
  let lastHeld = false;
  // Where the report sits in the conversation, when this host can open it there.
  const chatRef = () => (chatLine?.open ? chatRefOf(last?.raw) : null);

  const queue = () => {
    // The report goes first among equals: a look or a setup run in progress
    // outranks the ticket line the same work produces ("Thinking it through…").
    const items = [];
    const speech = last ? commanderSpeech(last.view) : null;
    if (speech) items.push({ kind: REPORT_KIND[speech.phase] || 'working', report: true, speech });
    // The folder agent's own look ticket is the report's work, narrated by
    // the report itself; its ticket line (often stuck at "On it.") stays out.
    const lookTicket = String(last?.raw?.ticket_id || '');
    items.push(...tickets.filter(item => !lookTicket || item.taskId !== lookTicket));
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
      // While the chat box shows the full plan the balloon keeps its line and its
      // announcement but carries no body, so the plan's controls exist in one
      // place (visual-hire FR24, FR25: the arrival is announced once, here).
      const held = chatHeld();
      return {
        speaker: { name: view.agent, role: 'Commander' },
        // A balloon leaving with its run says where it goes (a line); the
        // workspace page's own follower passes true and keeps its speech.
        line: typeof opening === 'string' && opening ? opening : item.speech.line,
        headline: item.speech.headline,
        announce: item.speech.announce,
        signature: `report|${JSON.stringify(raw)}|${error}|${opening}|${index}|${held}`,
        // A new phase (the plan becoming the run, the run the receipt)
        // starts at its top, not at the old scroll position.
        scrollKey: `report|${item.speech.phase}`,
        body: held ? null : () => card(true),
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

  // showCard draws the full card in the host. A host that keeps no line was
  // hidden while the map spoke, so it is shown again here.
  const showCard = fresh => {
    const { host, card } = last;
    if (hostMode !== 'card' || fresh) host?.replaceChildren(card(false));
    if (!hostLine && host) host.hidden = false;
    hostMode = 'card';
    return true;
  };

  // quiet: the host says nothing and takes no room. Its owner unhides it
  // before every draw, so it is hidden again each time.
  const quiet = () => {
    const { host } = last;
    if (hostMode !== 'quiet') host?.replaceChildren();
    if (host) host.hidden = true;
    hostMode = 'quiet';
    return true;
  };

  // showChatLine: the report is a message in the conversation, so the host is
  // one line whose button opens it there (Plan B), not a second full card.
  const showChatLine = fresh => {
    const { view, host, doc } = last;
    if (hostMode !== 'chat' || fresh) {
      const line = renderCommanderOneLine(view, doc, {
        action: 'Open in chat',
        onReview: button => chatLine.open(chatRef(), button)
      });
      if (line) line.dataset.folderAgentChatLine = 'true';
      host?.replaceChildren(line);
    }
    if (host) host.hidden = false;
    hostMode = 'chat';
    return true;
  };

  // draw: report says the folder agent's view is new (auto-open may apply).
  const draw = ({ report = false, refresh = false } = {}) => {
    const attached = map.attach({ onNarrow: revealHost });
    const held = chatHeld();
    // The plan moving between the chat box and the balloon redraws the balloon.
    if (report || refresh || held !== lastHeld || !map.balloon.content)
      map.balloon.update(content());
    lastHeld = held;
    if (!last) {
      hostMode = '';
      return false;
    }
    const fresh = report;
    const { view, previous, followed, host, doc } = last;
    const speech = commanderSpeech(view);
    // Nothing to say on the map (a kept folder): the full card, unless the
    // chat already shows it under the report's message.
    if (!speech) return chatRef() && held ? quiet() : showCard(fresh);
    const onMap = attached && map.visible() && !map.narrow();
    // The chat box holds the full plan: it shows the whole card, and the balloon
    // stays collapsed to its one line, so it never opens by itself over the plan
    // (visual-hire FR22, FR24). A closed or stepped-aside box holds nothing, and
    // the balloon carries the plan as before.
    if (onMap && held) {
      pendingOpen = '';
      if (map.balloon.expanded) map.balloon.collapse({ returnFocus: false });
      // A host whose report is a message in the thread already shows it there.
      return hostLine ? showCard(fresh) : quiet();
    }
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
      // The Commander's line on the map is the status: this host says nothing
      // and takes no room.
      if (!hostLine) return quiet();
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
    // Off the map (another tab, or too narrow for a balloon): a report posted to
    // the conversation is one line there, or nothing while the chat shows it.
    if (chatRef()) return held ? quiet() : showChatLine(fresh);
    return showCard(fresh);
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
      // A report that changed phase (the plan became the run, the run the
      // receipt) shows from the most urgent item again, not from where the
      // owner had stepped with "and N more".
      const phase = args ? commanderSpeech(args.view)?.phase || '' : '';
      if (phase !== lastPhase) shift = 0;
      lastPhase = phase;
      last = args;
      const drawn = draw({ report: true });
      // While the report has something to say, its own look ticket is told by
      // the report: a host that lists tickets leaves that one to the Commander.
      const ticket = phase ? String(args?.raw?.ticket_id || '') : '';
      if (ticket !== narrated) {
        narrated = ticket;
        onNarrated?.(ticket);
      }
      return drawn;
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
