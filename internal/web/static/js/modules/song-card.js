/*
 * song-card.js — the read-only project card a linked workspace shows on its
 * Details page and in the Home map's selected context (song-card PRD,
 * FR8–FR14). One implementation for every place.
 *
 * The server (`GET /api/workspaces/{id}/song-card`) sends stored data only:
 * the Home library entry's facts and save time, the keeper's name, the status
 * line, three routes and the Home's `words` (PRD #792 FR7). This module words
 * it with the shelf's own helpers (`songFactsLabel`, `lastSavedLabel`) so the
 * shelf and the card say the same thing, and builds native anchors. Every
 * word naming the project ("Song", "Project") comes from `words`; without
 * them the card reads the neutral "Project". It has exactly three
 * navigations — the Home, Open in Home, Turn on project details — and no
 * buttons: it never toggles the switch, scans, staffs or starts work.
 */

import { stageLabel, statusLabel } from './field-labels.js';
import { lastSavedLabel, lowerNoun, projectWords, songFactsLabel } from './library-open.js';
import { renderSuggestions, suggestionHandlers, suggestionView } from './song-card-suggestions.js';

export const FACTS_OFF_TEXT = 'Project details are off for this Home.';
export const NOT_LINKED_TEXT = 'Not linked to a Home library entry';
export const NO_KEEPER_TEXT = 'No assistant yet';
export const NO_DETAILS_TEXT = 'No details yet.';
export const EDIT_DETAILS_LABEL = 'Edit details';

const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];

// "Jul 1", or "Jul 1, 2027" outside the current year, for a YYYY-MM-DD due
// date; '' when it is not one.
function dueDate(value, now) {
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(text(value));
  if (!match) return '';
  const [year, month, day] = [Number(match[1]), Number(match[2]), Number(match[3])];
  if (month < 1 || month > 12 || day < 1 || day > 31) return '';
  const label = `${MONTHS[month - 1]} ${day}`;
  return year === now.getFullYear() ? label : `${label}, ${year}`;
}

const lines = value => (Array.isArray(value) ? value : []).map(line => text(line)).filter(Boolean);

/**
 * The details block's rows (PRD #792 FR12), in order, leaving out what is
 * unset: Stage, Status, Next action, Blockers, Deliverables, Milestones, then
 * each plugin-declared field under its declared label. Each row is
 * { id, label, lines: [text], list: bool }; a milestone line is
 * { text, done }.
 */
export function songCardDetails(fields, words, now = new Date()) {
  if (!fields || typeof fields !== 'object') return [];
  const rows = [];
  const add = (id, label, values, list = false) => {
    if (values.length) rows.push({ id, label, lines: values, list });
  };
  add('stage', 'Stage', [stageLabel(fields.stage)].filter(Boolean));
  add('status', 'Status', [statusLabel(fields.status)].filter(Boolean));
  add('next_action', 'Next action', [text(fields.next_action)].filter(Boolean));
  add('blockers', 'Blockers', lines(fields.blockers), true);
  add('deliverables', 'Deliverables', lines(fields.deliverables), true);
  const milestones = (Array.isArray(fields.milestones) ? fields.milestones : [])
    .filter(milestone => text(milestone?.label))
    .map(milestone => {
      const due = dueDate(milestone.due_date, now);
      const done = milestone.complete === true;
      return {
        text: [text(milestone.label), due ? `due ${due}` : '', done ? 'done' : '']
          .filter(Boolean)
          .join(' · '),
        done
      };
    });
  add('milestones', 'Milestones', milestones, true);
  const extra = fields.extra && typeof fields.extra === 'object' ? fields.extra : {};
  for (const field of projectWords(words).fields) {
    const id = text(field.id);
    if (!Object.hasOwn(extra, id)) continue;
    add(`extra:${id}`, text(field.label), lines(extra[id]), field.kind === 'list');
  }
  return rows;
}

/** "Project details not read yet for this song." under the Home's words. */
export function factsAbsentText(words) {
  return `Project details not read yet for this ${lowerNoun(words)}.`;
}

/** "Untitled song" under the Home's words. */
export function untitledText(words) {
  return `Untitled ${lowerNoun(words)}`;
}

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
export function songCardView(data, { now = new Date(), words } = {}) {
  if (!data || typeof data !== 'object' || data.linked !== true) return null;
  const resolvedWords = projectWords(data.words || words);
  const entryLinked = Boolean(text(data.entry_id));
  const homeName = text(data.home_name);
  const homeHref = safeWorkspaceRoute(data.home_route);
  const view = {
    words: resolvedWords,
    noun: resolvedWords.noun,
    title: text(data.title) || untitledText(resolvedWords),
    home: homeName ? { label: homeName, href: homeHref } : null,
    entryLinked,
    factsText: '',
    factsNote: '',
    factsSwitch: null,
    savedText: '',
    keeperText: text(data.keeper_agent_name) || NO_KEEPER_TEXT,
    hasKeeper: Boolean(text(data.keeper_agent_name)),
    statusLine: text(data.status_line),
    // The details block: null for a card without an entry (a broken or
    // diverged link shows none); [] reads "No details yet."
    details: null,
    editDetails: null,
    openInHome: null,
    // The Home the card's suggestions are answered on (PRD #792 FR24), and
    // this song's waiting fields suggestions; none for a broken link.
    homeID: text(data.home_id),
    suggestions: []
  };
  if (!entryLinked) {
    view.factsNote = NOT_LINKED_TEXT;
    return view;
  }
  if (data.fields && typeof data.fields === 'object') {
    view.details = songCardDetails(data.fields, resolvedWords, now);
  }
  view.suggestions = (Array.isArray(data.suggestions) ? data.suggestions : [])
    .map(suggestion => suggestionView(suggestion, resolvedWords, now))
    .filter(suggestion => suggestion.id && suggestion.rows.length);
  view.savedText = lastSavedLabel(data.last_saved_at, now);
  if (data.facts_state === 'on') {
    view.factsText = songFactsLabel(data.facts, resolvedWords);
    if (!view.factsText) view.factsNote = factsAbsentText(resolvedWords);
  } else if (data.facts_state === 'off') {
    view.factsNote = FACTS_OFF_TEXT;
    const href = safeWorkspaceRoute(data.facts_switch_route);
    if (href) view.factsSwitch = { label: 'Turn on project details', href };
  } else {
    view.factsNote = factsAbsentText(resolvedWords);
  }
  const entryHref = safeWorkspaceRoute(data.home_entry_route);
  if (entryHref) {
    view.openInHome = { label: 'Open in Home', href: entryHref };
    // The owner edits fields on the shelf; the card never edits one (FR13).
    if (view.details) view.editDetails = { label: EDIT_DETAILS_LABEL, href: entryHref };
  }
  return view;
}

/** The card's navigations in reading order (at most four). */
export function songCardLinks(view) {
  if (!view) return [];
  return [
    view.home?.href ? view.home : null,
    view.factsSwitch,
    view.editDetails,
    view.openInHome
  ].filter(Boolean);
}

function renderDetails(doc, view) {
  const block = element(doc, 'div', 'song-card-details');
  if (!view.details.length) {
    block.append(element(doc, 'p', 'song-card-details-empty', NO_DETAILS_TEXT));
  } else {
    const list = element(doc, 'dl', 'song-card-details-list');
    for (const row of view.details) {
      const item = element(doc, 'div', 'song-card-detail');
      item.setAttribute('data-detail', row.id);
      item.append(element(doc, 'dt', 'song-card-detail-label', row.label));
      const value = element(doc, 'dd', 'song-card-detail-value');
      if (row.list) {
        const lines = element(doc, 'ul', 'song-card-detail-lines');
        for (const line of row.lines) {
          const lineText = typeof line === 'string' ? line : line.text;
          const li = element(doc, 'li', '', lineText);
          if (typeof line === 'object' && line.done) li.className = 'song-card-detail-done';
          lines.append(li);
        }
        value.append(lines);
      } else {
        value.textContent = row.lines.join(' ');
      }
      item.append(value);
      list.append(item);
    }
    block.append(list);
  }
  if (view.editDetails) block.append(anchor(doc, 'song-card-edit', view.editDetails));
  return block;
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
 * `onAccept(suggestion, button)` and `onDismiss(suggestion, button)` give the
 * suggestion block its two native buttons; without them it is read-only.
 */
export function renderSongCard(container, view, { onAccept, onDismiss } = {}) {
  if (!container) return null;
  const doc = container.ownerDocument || globalThis.document;
  if (!view) {
    container.replaceChildren();
    container.hidden = true;
    return null;
  }
  const card = element(doc, 'section', 'song-card');
  const noun = view.noun || projectWords(view.words).noun;
  card.setAttribute('aria-label', `${noun}: ${view.title}`);

  const head = element(doc, 'div', 'song-card-head');
  head.append(element(doc, 'span', 'song-card-eyebrow', noun));
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
  if (Array.isArray(view.details)) card.append(renderDetails(doc, view));
  const suggestions = renderSuggestions(doc, view.suggestions, { onAccept, onDismiss });
  if (suggestions) card.append(suggestions);
  if (view.openInHome) card.append(anchor(doc, 'song-card-open', view.openInHome));

  container.replaceChildren(card);
  container.hidden = false;
  return card;
}

/**
 * Renders a card with working Accept and Dismiss: each answers on the card's
 * Home and then calls `reload` (the mount re-fetches the card). Every mount
 * uses this, so the three surfaces behave the same.
 */
export function renderInteractiveSongCard(
  container,
  view,
  { reload, fetchImpl, confirm, onError } = {}
) {
  const handlers = suggestionHandlers(view, {
    fetchImpl,
    confirm,
    onError,
    onChanged: () => (typeof reload === 'function' ? reload() : undefined)
  });
  return renderSongCard(container, view, handlers);
}

/** Fetches and renders a workspace's card; any failure leaves the container hidden. */
export async function loadSongCard(
  container,
  workspaceID,
  { fetchImpl = globalThis.fetch, now, confirm, onError } = {}
) {
  if (!container || !workspaceID || typeof fetchImpl !== 'function') return null;
  try {
    const response = await fetchImpl(songCardURL(workspaceID), {
      headers: { Accept: 'application/json' }
    });
    if (!response.ok) throw new Error(`song card ${response.status}`);
    const view = songCardView(await response.json(), now ? { now } : {});
    renderInteractiveSongCard(container, view, {
      fetchImpl,
      confirm,
      onError,
      reload: () => loadSongCard(container, workspaceID, { fetchImpl, now, confirm, onError })
    });
    return view;
  } catch {
    renderSongCard(container, null);
    return null;
  }
}
