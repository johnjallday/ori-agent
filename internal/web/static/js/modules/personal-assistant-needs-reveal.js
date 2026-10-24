// Which Needs you items this tab produced, so the drawer's strip opens by
// itself for those and for nothing else (assistant-drawer-redesign FR14). A
// hire made from this tab's conversation, a first look this tab started, and
// a folder card that left this tab's conversation are claimed when they are
// made; when the item arrives in Needs you, Home opens the strip once for it.
// Items from other tabs, devices or earlier sessions were never claimed here,
// so they wait behind the strip.
//
// The record is per tab (sessionStorage), so a report that lands after a
// reload or a page change still counts, and bounded: only recent items matter.

export const NEEDS_REVEAL_KEY = 'ori.assistant.needs.reveal';
const NEEDS_REVEAL_LIMIT = 40;

function tabStorage() {
  try {
    return globalThis.sessionStorage || null;
  } catch (_) {
    return null;
  }
}

function keyOf(value) {
  return typeof value === 'string' ? value.trim() : '';
}

export function readNeedsReveal(storage = tabStorage()) {
  const list = value =>
    Array.isArray(value) ? value.map(keyOf).filter(Boolean).slice(-NEEDS_REVEAL_LIMIT) : [];
  try {
    const parsed = JSON.parse(storage?.getItem?.(NEEDS_REVEAL_KEY) || 'null');
    return { own: list(parsed?.own), revealed: list(parsed?.revealed) };
  } catch (_) {
    return { own: [], revealed: [] };
  }
}

export function writeNeedsReveal(record, storage = tabStorage()) {
  try {
    storage.setItem(
      NEEDS_REVEAL_KEY,
      JSON.stringify({
        own: (record?.own || []).slice(-NEEDS_REVEAL_LIMIT),
        revealed: (record?.revealed || []).slice(-NEEDS_REVEAL_LIMIT)
      })
    );
    return true;
  } catch (_) {
    return false;
  }
}

// Whether an arriving item opens the strip: only one this tab claimed, and
// only the first time it arrives.
export function needsRevealDecision({ key, own, revealed } = {}) {
  const id = keyOf(key);
  if (!id || !own?.includes?.(id)) return false;
  return !revealed?.includes?.(id);
}

/** Records that this tab produced the item `key` will name when it arrives. */
export function claimNeedsItem(key, storage = tabStorage()) {
  const id = keyOf(key);
  if (!id) return false;
  const record = readNeedsReveal(storage);
  if (record.own.includes(id)) return true;
  return writeNeedsReveal({ ...record, own: [...record.own, id] }, storage);
}

export function needsItemClaimed(key, storage = tabStorage()) {
  const id = keyOf(key);
  return Boolean(id) && readNeedsReveal(storage).own.includes(id);
}

/**
 * Asks to open the strip for an arriving item. True once for an item this tab
 * claimed (and from then on it counts as revealed); false otherwise.
 */
export function takeNeedsReveal(key, storage = tabStorage()) {
  const record = readNeedsReveal(storage);
  if (!needsRevealDecision({ key, ...record })) return false;
  writeNeedsReveal({ ...record, revealed: [...record.revealed, keyOf(key)] }, storage);
  return true;
}

/** The key a report card in Needs you is known by (first-look-report.js). */
export function reportRevealKey(card) {
  const id = keyOf(card?.dataset?.lookId);
  return id ? `${keyOf(card.dataset.reportKind) || 'folder'}:${id}` : '';
}

/**
 * Opens Needs you at a report card (`lookId` is its data-look-id), for the
 * "Needs you" words in the conversation. Off Home there is no strip; the card
 * is brought into view where it is drawn.
 */
export function revealReportCard(lookId, doc = globalThis.document) {
  const id = keyOf(lookId);
  const card = id
    ? Array.from(doc?.querySelectorAll?.('[data-look-id]') || []).find(
        node => node.dataset.lookId === id
      ) || null
    : null;
  const today = globalThis.PersonalAssistantToday;
  if (typeof today?.reveal === 'function') return today.reveal('conversation', { item: card });
  card?.scrollIntoView?.({ block: 'nearest' });
  return Boolean(card);
}

/**
 * Turns the first "Needs you" in a host-written conversation line into a
 * button that opens the strip and brings the item into view. The line's text
 * is unchanged; only those two words become the control.
 */
export function linkNeedsYouWords(row, onActivate) {
  const doc = row?.ownerDocument;
  if (!doc || typeof onActivate !== 'function') return null;
  const walker = doc.createTreeWalker(row, 4 /* NodeFilter.SHOW_TEXT */);
  for (let node = walker.nextNode(); node; node = walker.nextNode()) {
    const at = node.nodeValue.indexOf('Needs you');
    if (at < 0 || node.parentElement?.closest('button, a')) continue;
    const rest = node.splitText(at);
    rest.splitText('Needs you'.length);
    const button = doc.createElement('button');
    button.type = 'button';
    button.className = 'personal-assistant-needs-link';
    button.textContent = 'Needs you';
    button.addEventListener('click', onActivate);
    rest.replaceWith(button);
    return button;
  }
  return null;
}
