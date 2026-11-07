/*
 * song-card.js — the read-only "Song" card a linked song workspace shows on
 * its Details page and in the Home map's selected context (song-card PRD,
 * FR8–FR14). One implementation for both places.
 *
 * The server (`GET /api/workspaces/{id}/song-card`) sends stored data only:
 * the Home library entry's facts and save time, the keeper's name, the status
 * line and three routes. This module words it with the shelf's own helpers
 * (`songFactsLabel`, `lastSavedLabel`) so the shelf and the card say the same
 * thing, and builds native anchors. It has exactly three navigations — the
 * Home, Open in Home, Turn on project details — and no buttons: it never
 * toggles the switch, scans, staffs or starts work.
 */

import { lastSavedLabel, songFactsLabel } from './library-open.js';

export const FACTS_OFF_TEXT = 'Project details are off for this Home.';
export const FACTS_ABSENT_TEXT = 'Project details not read yet for this song.';
export const NOT_LINKED_TEXT = 'Not linked to a Home library entry';
export const NO_KEEPER_TEXT = 'No assistant yet';

const text = value => (typeof value === 'string' ? value.trim() : '');

// A route the server built for this workspace's Home: a same-origin
// /workspaces/... path, never a protocol-relative or absolute URL.
export function safeWorkspaceRoute(value) {
  const route = text(value);
  return /^\/workspaces\/[^/\s?#][^\s]*$/.test(route) && !route.startsWith('//') ? route : '';
}

export function songCardURL(workspaceID) {
  return `/api/workspaces/${encodeURIComponent(String(workspaceID || ''))}/song-card`;
}

/**
 * The card's view model, or null when the workspace is not a linked song.
 * Every text is plain; every link is { label, href } with a checked route.
 */
export function songCardView(data, { now = new Date() } = {}) {
  if (!data || typeof data !== 'object' || data.linked !== true) return null;
  const entryLinked = Boolean(text(data.entry_id));
  const homeName = text(data.home_name);
  const homeHref = safeWorkspaceRoute(data.home_route);
  const view = {
    title: text(data.title) || 'Untitled song',
    home: homeName ? { label: homeName, href: homeHref } : null,
    entryLinked,
    factsText: '',
    factsNote: '',
    factsSwitch: null,
    savedText: '',
    keeperText: text(data.keeper_agent_name) || NO_KEEPER_TEXT,
    hasKeeper: Boolean(text(data.keeper_agent_name)),
    statusLine: text(data.status_line),
    openInHome: null
  };
  if (!entryLinked) {
    view.factsNote = NOT_LINKED_TEXT;
    return view;
  }
  view.savedText = lastSavedLabel(data.last_saved_at, now);
  if (data.facts_state === 'on') {
    view.factsText = songFactsLabel(data.facts);
    if (!view.factsText) view.factsNote = FACTS_ABSENT_TEXT;
  } else if (data.facts_state === 'off') {
    view.factsNote = FACTS_OFF_TEXT;
    const href = safeWorkspaceRoute(data.facts_switch_route);
    if (href) view.factsSwitch = { label: 'Turn on project details', href };
  } else {
    view.factsNote = FACTS_ABSENT_TEXT;
  }
  const entryHref = safeWorkspaceRoute(data.home_entry_route);
  if (entryHref) view.openInHome = { label: 'Open in Home', href: entryHref };
  return view;
}

/** The card's navigations in reading order (at most three). */
export function songCardLinks(view) {
  if (!view) return [];
  return [view.home?.href ? view.home : null, view.factsSwitch, view.openInHome].filter(Boolean);
}

function element(doc, tag, className, content) {
  const node = doc.createElement(tag);
  if (className) node.className = className;
  if (content !== undefined) node.textContent = content;
  return node;
}

function anchor(doc, className, link) {
  const node = element(doc, 'a', className, link.label);
  node.setAttribute('href', link.href);
  return node;
}

/**
 * Renders the card into `container` (its children are replaced). A null view
 * empties and hides the container, so a non-song page is left as it was.
 */
export function renderSongCard(container, view) {
  if (!container) return null;
  const doc = container.ownerDocument || globalThis.document;
  if (!view) {
    container.replaceChildren();
    container.hidden = true;
    return null;
  }
  const card = element(doc, 'section', 'song-card');
  card.setAttribute('aria-label', `Song: ${view.title}`);

  const head = element(doc, 'div', 'song-card-head');
  head.append(element(doc, 'span', 'song-card-eyebrow', 'Song'));
  head.append(element(doc, 'h2', 'song-card-title', view.title));
  if (view.home) {
    const home = element(doc, 'p', 'song-card-home');
    home.append(doc.createTextNode('In '));
    home.append(
      view.home.href
        ? anchor(doc, 'song-card-home-link', view.home)
        : element(doc, 'span', 'song-card-home-name', view.home.label)
    );
    head.append(home);
  }
  card.append(head);

  const facts = element(
    doc,
    'p',
    `song-card-facts${view.factsText ? '' : ' song-card-facts-note'}`
  );
  facts.append(doc.createTextNode(view.factsText || view.factsNote));
  if (view.factsSwitch) {
    facts.append(doc.createTextNode(' '));
    facts.append(anchor(doc, 'song-card-switch-link', view.factsSwitch));
  }
  card.append(facts);

  if (view.savedText) card.append(element(doc, 'p', 'song-card-saved', view.savedText));
  card.append(
    element(
      doc,
      'p',
      `song-card-keeper${view.hasKeeper ? '' : ' song-card-keeper-empty'}`,
      view.hasKeeper ? `Kept by ${view.keeperText}` : view.keeperText
    )
  );
  if (view.statusLine) card.append(element(doc, 'p', 'song-card-status', view.statusLine));
  if (view.openInHome) card.append(anchor(doc, 'song-card-open', view.openInHome));

  container.replaceChildren(card);
  container.hidden = false;
  return card;
}

/** Fetches and renders a workspace's card; any failure leaves the container hidden. */
export async function loadSongCard(
  container,
  workspaceID,
  { fetchImpl = globalThis.fetch, now } = {}
) {
  if (!container || !workspaceID || typeof fetchImpl !== 'function') return null;
  try {
    const response = await fetchImpl(songCardURL(workspaceID), {
      headers: { Accept: 'application/json' }
    });
    if (!response.ok) throw new Error(`song card ${response.status}`);
    const view = songCardView(await response.json(), now ? { now } : {});
    renderSongCard(container, view);
    return view;
  } catch {
    renderSongCard(container, null);
    return null;
  }
}
