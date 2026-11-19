/*
 * hq-tip-layer.js — the one tip a first visit to the Personal HQ shows.
 *
 * One role="tooltip" element on document.body (the page container is rebuilt by
 * innerHTML on every render, and the Map clips), shown for a part of the page by
 * hover, keyboard focus or a tap on the part's `?` button. The layer reads the
 * page and never writes to it except two ARIA attributes on the part and its
 * `?` while a tip shows. It never calls focus() or blur(): a tip is information,
 * not a place to be.
 *
 * Anchors are found again by attribute on every show and refresh, because the
 * page replaces them wholesale:
 *   [data-hq-tip-part="<key>"]  the part (a card, a Flow node, the Notes panel)
 *   [data-hq-tip-mark="<key>"]  its `?` button, a sibling of the part
 * A part may carry data-hq-tip-trigger="mark": hovering or focusing the part
 * itself then opens nothing, only its `?` does (the Notes panel is large and
 * full of controls).
 *
 * The page calls refresh() after each render, so no MutationObserver is needed.
 * Events are delegated from the document and bound once, here.
 *
 * Escape closes only the tip: while one is open a capture-phase keydown handler
 * prevents the default and stops the event, so a panel underneath stays open.
 * The layer does not register with workspaceOverlayCoordinator: that would hand
 * focus back to a trigger, and would make the suggestions' own Escape close the
 * tip instead of the suggestions.
 */

export const TIP_ID = 'hq-part-tip';
export const PART_ATTR = 'data-hq-tip-part';
export const MARK_ATTR = 'data-hq-tip-mark';
export const TRIGGER_ATTR = 'data-hq-tip-trigger';
export const HOVER_OPEN_MS = 300;
export const CLOSE_GRACE_MS = 150;

const KEY_PATTERN = /^[a-z-]+$/;
const EDGE = 16;
const GAP = 8;
// Reasons after which the same part stays quiet until the person moves away:
// they just dismissed the tip or used the part, so a repaint that restores focus
// or a pointer that has not moved must not bring it back.
const DISMISSING = new Set(['escape', 'activate', 'retap']);

const INERT = Object.freeze({
  show: () => false,
  hide: () => {},
  refresh: () => {},
  destroy: () => {},
  isOpen: () => false,
  currentKey: () => ''
});

function describedByTokens(node) {
  return String((node && node.getAttribute && node.getAttribute('aria-describedby')) || '')
    .split(/\s+/)
    .filter(Boolean);
}

function addToken(node, token) {
  if (!node || typeof node.setAttribute !== 'function') return;
  const tokens = describedByTokens(node);
  if (!tokens.includes(token)) tokens.push(token);
  node.setAttribute('aria-describedby', tokens.join(' '));
}

function removeToken(node, token) {
  if (!node || typeof node.setAttribute !== 'function') return;
  const tokens = describedByTokens(node).filter(item => item !== token);
  if (tokens.length) node.setAttribute('aria-describedby', tokens.join(' '));
  else if (typeof node.removeAttribute === 'function') node.removeAttribute('aria-describedby');
  else node.setAttribute('aria-describedby', '');
}

function defaultKeyboardFocus(node) {
  if (!node || typeof node.matches !== 'function') return true;
  try {
    return node.matches(':focus-visible');
  } catch (_error) {
    return true;
  }
}

/**
 * @param {object} options
 * @param {() => Element|null} options.root the page container the anchors live in
 * @param {() => string} options.scope 'overview' | 'flow' | '' ('' = no tips now)
 * @param {(key: string) => ({sentence: string, name?: string, faceHTML?: string})|null} options.content
 * @param {(key: string) => void} [options.onShown] fired when a tip becomes visible
 * @param {(key: string, reason: string) => void} [options.onHidden] fired after a
 *   tip closes, with why ('escape', 'leave', 'scope', ...); not on destroy()
 * @param {(node: Element) => boolean} [options.isKeyboardFocus]
 * The rest (document, window, timers) default to the globals and exist so tests
 * can drive the layer with a fake DOM and a manual clock.
 */
export function createHqTipLayer({
  document: doc = typeof document === 'undefined' ? null : document,
  window: win = typeof window === 'undefined' ? null : window,
  root = () => null,
  scope = () => '',
  content = () => null,
  onShown = () => {},
  onHidden = () => {},
  isKeyboardFocus = defaultKeyboardFocus,
  setTimeout: later = typeof setTimeout === 'undefined' ? null : setTimeout,
  clearTimeout: cancelLater = typeof clearTimeout === 'undefined' ? null : clearTimeout,
  requestAnimationFrame: frame = win && typeof win.requestAnimationFrame === 'function'
    ? cb => win.requestAnimationFrame(cb)
    : null
} = {}) {
  if (
    !doc ||
    typeof doc.addEventListener !== 'function' ||
    typeof doc.createElement !== 'function' ||
    !doc.body ||
    typeof doc.body.appendChild !== 'function' ||
    typeof later !== 'function' ||
    typeof cancelLater !== 'function'
  )
    return INERT;
  const raf = frame || (cb => later(cb, 16));

  const el = doc.createElement('div');
  el.id = TIP_ID;
  el.className = 'ws-cmd ws-hq-tip';
  el.setAttribute('role', 'tooltip');
  el.setAttribute('data-placement', 'below');
  el.hidden = true;
  const head = doc.createElement('span');
  head.className = 'ws-hq-tip-head';
  const face = doc.createElement('span');
  face.className = 'ws-hq-tip-face';
  face.setAttribute('aria-hidden', 'true');
  const nameNode = doc.createElement('span');
  nameNode.className = 'ws-hq-tip-name';
  const textNode = doc.createElement('p');
  textNode.className = 'ws-hq-tip-text';
  head.appendChild(face);
  head.appendChild(nameNode);
  el.appendChild(head);
  el.appendChild(textNode);
  doc.body.appendChild(el);

  let destroyed = false;
  let openKey = '';
  let openScope = '';
  let origin = 'mark';
  let pinned = false;
  let applied = { part: null, mark: null };
  let dismissed = '';
  let pointerInside = false;
  let hoverTimer = null;
  let hoverKey = '';
  let closeTimer = null;
  let frameQueued = false;
  let keydownBound = false;

  const rootNode = () => {
    const node = root();
    return node && typeof node.querySelector === 'function' ? node : null;
  };
  const partFor = key => {
    const r = KEY_PATTERN.test(key) ? rootNode() : null;
    return r ? r.querySelector('[' + PART_ATTR + '="' + key + '"]') : null;
  };
  const markFor = key => {
    const r = KEY_PATTERN.test(key) ? rootNode() : null;
    return r ? r.querySelector('[' + MARK_ATTR + '="' + key + '"]') : null;
  };
  const closestOf = (node, selector) =>
    node && typeof node.closest === 'function' ? node.closest(selector) : null;
  const contains = (outer, node) =>
    Boolean(outer && node && (outer === node || (outer.contains && outer.contains(node))));
  const inTip = node => contains(el, node);
  const activeNode = () => doc.activeElement || null;
  const partTriggers = part => !part || part.getAttribute(TRIGGER_ATTR) !== 'mark';

  // What an event target belongs to: a part or a `?`, inside the page container.
  function hitOf(target) {
    const r = rootNode();
    if (!r) return null;
    for (const [kind, attr] of [
      ['mark', MARK_ATTR],
      ['part', PART_ATTR]
    ]) {
      const node = closestOf(target, '[' + attr + ']');
      if (!node || !contains(r, node)) continue;
      const key = node.getAttribute(attr) || '';
      if (!KEY_PATTERN.test(key)) continue;
      return { kind, key, node, triggers: kind === 'mark' || partTriggers(node) };
    }
    return null;
  }

  // The nodes whose hover or focus keeps the open tip alive.
  function holds(node) {
    if (!openKey || !node) return false;
    if (inTip(node)) return true;
    if (contains(markFor(openKey), node)) return true;
    const part = partFor(openKey);
    return Boolean(part && partTriggers(part) && contains(part, node));
  }

  function clearTimers() {
    if (hoverTimer !== null) cancelLater(hoverTimer);
    if (closeTimer !== null) cancelLater(closeTimer);
    hoverTimer = null;
    closeTimer = null;
  }

  function clearAria() {
    removeToken(applied.part, TIP_ID);
    if (applied.mark) {
      removeToken(applied.mark, TIP_ID);
      applied.mark.setAttribute('aria-expanded', 'false');
    }
    applied = { part: null, mark: null };
  }

  function applyAria() {
    clearAria();
    const part = partFor(openKey);
    const mark = markFor(openKey);
    if (part) addToken(part, TIP_ID);
    if (mark) {
      addToken(mark, TIP_ID);
      mark.setAttribute('aria-expanded', 'true');
    }
    applied = { part, mark };
  }

  function paint(info) {
    const name = String((info && info.name) || '');
    nameNode.textContent = name;
    face.innerHTML = String((info && info.faceHTML) || '');
    head.hidden = !name && !(info && info.faceHTML);
    textNode.textContent = String((info && info.sentence) || '');
  }

  function position() {
    if (!openKey || !el) return;
    const mark = markFor(openKey);
    const part = partFor(openKey);
    const opener = origin === 'mark' ? mark || part : part || mark;
    if (!opener || typeof opener.getBoundingClientRect !== 'function') return;
    const view = doc.documentElement || {};
    const viewWidth = view.clientWidth || (win && win.innerWidth) || 0;
    const viewHeight = (win && win.innerHeight) || view.clientHeight || 0;
    el.style.left = '0px';
    el.style.top = '0px';
    el.hidden = false;
    const box = el.getBoundingClientRect();
    const width = box.width || 0;
    const height = box.height || 0;
    const rect = opener.getBoundingClientRect();
    if (rect.bottom < 0 || rect.top > viewHeight || rect.right < 0 || rect.left > viewWidth) {
      hide('offscreen');
      return;
    }
    const navbar = typeof doc.querySelector === 'function' ? doc.querySelector('nav.navbar') : null;
    const navBottom = Math.max(
      0,
      navbar && typeof navbar.getBoundingClientRect === 'function'
        ? navbar.getBoundingClientRect().bottom || 0
        : 0
    );
    const below = rect.bottom + GAP;
    const above = rect.top - GAP - height;
    let top;
    let placement = 'below';
    if (below + height <= viewHeight - GAP) top = below;
    else if (above >= navBottom + GAP) {
      top = above;
      placement = 'above';
    } else top = Math.max(navBottom + GAP, Math.min(below, viewHeight - height - GAP));
    const left = Math.max(
      EDGE,
      Math.min(rect.left + rect.width / 2 - width / 2, viewWidth - width - EDGE)
    );
    el.style.left = Math.round(left) + 'px';
    el.style.top = Math.round(top) + 'px';
    el.setAttribute('data-placement', placement);
  }

  function onKey(event) {
    if (!openKey) return;
    if (event.key !== 'Escape' || event.defaultPrevented || event.isComposing) return;
    event.preventDefault();
    event.stopPropagation();
    hide('escape');
  }

  function show(key, source = 'tap', from = 'mark') {
    if (destroyed || !KEY_PATTERN.test(String(key || ''))) return false;
    const currentScope = String(scope() || '');
    if (!currentScope) return false;
    const info = content(key);
    if (!info || !info.sentence || !(partFor(key) || markFor(key))) return false;
    if (source === 'tap') dismissed = '';
    if (openKey === key) {
      if (source === 'tap') pinned = true;
      origin = from;
      paint(info);
      applyAria();
      position();
      return true;
    }
    clearTimers();
    clearAria();
    const wasOpen = Boolean(openKey);
    openKey = key;
    openScope = currentScope;
    origin = from;
    pinned = source === 'tap';
    paint(info);
    applyAria();
    el.hidden = false;
    position();
    if (!openKey) return false; // positioned off screen: hide() ran
    if (!wasOpen && !keydownBound) {
      doc.addEventListener('keydown', onKey, true);
      keydownBound = true;
    }
    try {
      onShown(key);
    } catch (_error) {
      // The caller's bookkeeping must never break the tip.
    }
    return true;
  }

  function hide(reason = '') {
    if (!openKey) return;
    const key = openKey;
    clearTimers();
    clearAria();
    openKey = '';
    openScope = '';
    pinned = false;
    pointerInside = false;
    el.hidden = true;
    if (keydownBound) {
      doc.removeEventListener('keydown', onKey, true);
      keydownBound = false;
    }
    if (DISMISSING.has(reason)) dismissed = key;
    if (reason === 'destroy') return;
    try {
      onHidden(key, reason);
    } catch (_error) {
      // The caller's bookkeeping must never break the tip.
    }
  }

  function refresh() {
    if (destroyed || !openKey) return;
    if (String(scope() || '') !== openScope) return hide('scope');
    const info = content(openKey);
    if (!info || !(partFor(openKey) || markFor(openKey))) return hide('gone');
    paint(info);
    applyAria();
    position();
  }

  // `force` is for a pinned tip whose keyboard focus moved to something
  // unrelated: it does not stay open behind the person's Tab.
  function scheduleClose(force = false) {
    if (!openKey || (pinned && !force)) return;
    if (closeTimer !== null) cancelLater(closeTimer);
    closeTimer = later(() => {
      closeTimer = null;
      if (!openKey || (pinned && !force)) return;
      // Re-read focus and pointer now: a repaint may have replaced the node
      // whose focusout started this, and given its focus to the replacement.
      if (pointerInside || holds(activeNode())) return;
      hide('leave');
    }, CLOSE_GRACE_MS);
  }

  function onPointerOver(event) {
    if (destroyed) return;
    const type = event.pointerType;
    if (type !== 'mouse' && type !== 'pen') return;
    const target = event.target;
    if (openKey && holds(target)) {
      pointerInside = true;
      if (closeTimer !== null) {
        cancelLater(closeTimer);
        closeTimer = null;
      }
    }
    const hit = hitOf(target);
    if (!hit || !hit.triggers) return;
    if (dismissed === hit.key) return;
    if (openKey === hit.key) return;
    // Crossing a part's own children must not restart its wait.
    if (hoverTimer !== null && hoverKey === hit.key) return;
    if (hoverTimer !== null) cancelLater(hoverTimer);
    hoverTimer = null;
    const open = () => {
      hoverTimer = null;
      pointerInside = true;
      show(hit.key, 'hover', hit.kind);
    };
    // Moving between parts swaps the tip at once; a first one waits a moment so
    // sweeping the pointer across the page does not flash tips.
    if (openKey) open();
    else {
      hoverKey = hit.key;
      hoverTimer = later(open, HOVER_OPEN_MS);
    }
  }

  function onPointerOut(event) {
    if (destroyed) return;
    const type = event.pointerType;
    if (type !== 'mouse' && type !== 'pen') return;
    const from = event.target;
    const to = event.relatedTarget;
    const hit = hitOf(from);
    if (dismissed && hit && hit.key === dismissed) {
      const same = hitOf(to);
      if (!same || same.key !== dismissed) dismissed = '';
    }
    if (hoverTimer !== null && hit && !contains(hit.node, to)) {
      cancelLater(hoverTimer);
      hoverTimer = null;
    }
    if (!openKey) return;
    if (!holds(from)) return;
    if (holds(to)) return;
    pointerInside = false;
    scheduleClose();
  }

  function onFocusIn(event) {
    if (destroyed) return;
    const target = event.target;
    const hit = hitOf(target);
    if (dismissed && (!hit || hit.key !== dismissed)) dismissed = '';
    if (openKey && holds(target) && closeTimer !== null) {
      cancelLater(closeTimer);
      closeTimer = null;
    }
    if (!hit || !hit.triggers) return;
    if (dismissed === hit.key) return;
    if (!isKeyboardFocus(target)) return;
    show(hit.key, 'focus', hit.kind);
  }

  function onFocusOut(event) {
    if (destroyed || !openKey) return;
    if (!holds(event.target)) return;
    if (!pinned) return scheduleClose();
    // Pinned: only focus that lands on another element ends it. A null
    // relatedTarget is a window blur or a node a repaint removed.
    if (event.relatedTarget && !holds(event.relatedTarget)) scheduleClose(true);
  }

  function onClick(event) {
    if (destroyed) return;
    const target = event.target;
    const hit = hitOf(target);
    if (hit && hit.kind === 'mark') {
      if (openKey === hit.key && pinned) hide('retap');
      else show(hit.key, 'tap', 'mark');
      return;
    }
    if (!openKey) return;
    if (inTip(target)) return;
    if (hit && hit.key === openKey) {
      hide('activate');
      return;
    }
    hide('outside');
  }

  function onPointerDown(event) {
    if (destroyed || !openKey) return;
    const target = event.target;
    if (inTip(target)) return;
    const hit = hitOf(target);
    if (hit && hit.key === openKey) return;
    hide('outside');
  }

  function queueReposition() {
    if (!openKey || frameQueued) return;
    frameQueued = true;
    raf(() => {
      frameQueued = false;
      if (!destroyed && openKey) position();
    });
  }

  const onPageHide = () => hide('pagehide');

  doc.addEventListener('pointerover', onPointerOver);
  doc.addEventListener('pointerout', onPointerOut);
  doc.addEventListener('focusin', onFocusIn);
  doc.addEventListener('focusout', onFocusOut);
  doc.addEventListener('click', onClick, true);
  doc.addEventListener('pointerdown', onPointerDown, true);
  if (win && typeof win.addEventListener === 'function') {
    win.addEventListener('scroll', queueReposition, true);
    win.addEventListener('resize', queueReposition);
    win.addEventListener('pagehide', onPageHide);
  }

  function destroy() {
    if (destroyed) return;
    hide('destroy');
    destroyed = true;
    clearTimers();
    doc.removeEventListener('pointerover', onPointerOver);
    doc.removeEventListener('pointerout', onPointerOut);
    doc.removeEventListener('focusin', onFocusIn);
    doc.removeEventListener('focusout', onFocusOut);
    doc.removeEventListener('click', onClick, true);
    doc.removeEventListener('pointerdown', onPointerDown, true);
    if (win && typeof win.removeEventListener === 'function') {
      win.removeEventListener('scroll', queueReposition, true);
      win.removeEventListener('resize', queueReposition);
      win.removeEventListener('pagehide', onPageHide);
    }
    if (typeof el.remove === 'function') el.remove();
    else if (el.parentNode && typeof el.parentNode.removeChild === 'function')
      el.parentNode.removeChild(el);
  }

  return {
    show,
    hide,
    refresh,
    destroy,
    isOpen: () => Boolean(openKey),
    currentKey: () => openKey
  };
}
