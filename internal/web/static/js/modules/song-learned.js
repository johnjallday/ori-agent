/*
 * song-learned.js — the "Learned from the project file" section of a song
 * (learn-the-songs FR16): the track names, markers and regions Ori read from
 * the project file under a consent that covers names, or why a song was not
 * read. The song card and the shelf's project detail both draw it, so they
 * say the same thing.
 *
 * Everything named here was typed into a project file by a person, so it is
 * data: it is only ever drawn as text (textContent), never as markup or a
 * link, and it is never given to any other part of the page. The section is
 * read-only. Above about a dozen entries a list is collapsed behind its
 * summary; "and K more" says how many a cap left out.
 */

export const LEARNED_HEADING = 'Learned from the project file';

// A list longer than this starts collapsed.
export const LEARNED_COLLAPSE_ABOVE = 12;

// The most entries a list shows (the server keeps at most this many).
const MAX_ENTRIES = 128;

// Why a song was not read, in words. The server sends a short code.
export const NOT_READ_TEXT = {
  too_large: 'This project file is too large to read.',
  unreadable: 'This project file could not be read.',
  no_grammar: "Ori can't read this kind of project file."
};
const NOT_READ_GENERIC = 'This project file was not read.';
export const NOTHING_FOUND_TEXT = 'No track names or markers were found in the project file.';

const count = value => {
  const n = Number(value);
  return Number.isFinite(n) && n > 0 ? Math.floor(n) : 0;
};

/**
 * markerTimeLabel is a time in seconds as m:ss ("1:05"), or h:mm:ss from an
 * hour on; '' for anything that is not a time.
 */
export function markerTimeLabel(seconds) {
  // Only a number or a numeric string is a time: Number(null) and Number('') are
  // 0, and a missing time must not read as "0:00".
  if (typeof seconds === 'string' ? seconds.trim() === '' : typeof seconds !== 'number') return '';
  const value = Number(seconds);
  if (!Number.isFinite(value) || value < 0) return '';
  const whole = Math.floor(value);
  const hours = Math.floor(whole / 3600);
  const minutes = Math.floor((whole % 3600) / 60);
  const rest = String(whole % 60).padStart(2, '0');
  return hours > 0 ? `${hours}:${String(minutes).padStart(2, '0')}:${rest}` : `${minutes}:${rest}`;
}

/**
 * learnedView cleans the server's `learned` object into what is drawn:
 * { notRead: {reason, text} | null, tracks: [name], tracksMore, markers:
 * [{name, time}], markersMore }. A marker is "1:05" and a region "0:30–0:45".
 * Null when the song has no learned section at all.
 */
export function learnedView(raw) {
  if (!raw || typeof raw !== 'object') return null;
  const reason = typeof raw.not_read === 'string' ? raw.not_read : '';
  if (reason) {
    return {
      notRead: { reason, text: NOT_READ_TEXT[reason] || NOT_READ_GENERIC },
      tracks: [],
      tracksMore: 0,
      markers: [],
      markersMore: 0
    };
  }
  const tracks = (Array.isArray(raw.tracks) ? raw.tracks : [])
    .slice(0, MAX_ENTRIES)
    .map(name => String(name ?? '').trim())
    .filter(Boolean);
  const markers = (Array.isArray(raw.markers) ? raw.markers : [])
    .slice(0, MAX_ENTRIES)
    .map(marker => {
      const start = markerTimeLabel(marker?.at);
      const end = Number(marker?.end) > Number(marker?.at) ? markerTimeLabel(marker.end) : '';
      return { name: String(marker?.name ?? '').trim(), time: end ? `${start}–${end}` : start };
    })
    .filter(marker => marker.time);
  return {
    notRead: null,
    tracks,
    tracksMore: count(raw.tracks_left_out),
    markers,
    markersMore: count(raw.markers_left_out)
  };
}

function element(doc, tag, className, content) {
  const node = doc.createElement(tag);
  if (className) node.className = className;
  if (content !== undefined && content !== null && content !== '') node.textContent = content;
  return node;
}

// One list: its summary ("Tracks · 14"), the entries, and "and K more".
function listBlock(doc, kind, title, entries, more, drawEntry) {
  const block = element(doc, 'details', `song-learned-list song-learned-${kind}`);
  // A long list starts closed; a short one is open and needs no click.
  if (entries.length <= LEARNED_COLLAPSE_ABOVE) block.open = true;
  block.append(
    element(doc, 'summary', 'song-learned-summary', `${title} · ${entries.length + more}`)
  );
  const list = element(doc, 'ul', 'song-learned-entries');
  entries.forEach(entry => list.append(drawEntry(entry)));
  block.append(list);
  if (more > 0) block.append(element(doc, 'p', 'song-learned-more', `and ${more} more`));
  return block;
}

/**
 * renderLearned draws the section for a learnedView, or null when there is
 * none. Names are drawn as text only.
 */
export function renderLearned(doc, view) {
  if (!view) return null;
  const section = element(doc, 'section', 'song-learned');
  section.setAttribute('aria-label', LEARNED_HEADING);
  section.append(element(doc, 'h4', 'song-learned-title', LEARNED_HEADING));
  if (view.notRead) {
    section.setAttribute('data-not-read', view.notRead.reason);
    section.append(element(doc, 'p', 'song-learned-note', view.notRead.text));
    return section;
  }
  if (view.tracks.length || view.tracksMore > 0) {
    section.append(
      listBlock(doc, 'tracks', 'Tracks', view.tracks, view.tracksMore, name =>
        element(doc, 'li', 'song-learned-name', name)
      )
    );
  }
  if (view.markers.length || view.markersMore > 0) {
    section.append(
      listBlock(doc, 'markers', 'Markers and regions', view.markers, view.markersMore, marker => {
        const item = element(doc, 'li', 'song-learned-marker');
        item.append(element(doc, 'span', 'song-learned-time', marker.time));
        if (marker.name) item.append(element(doc, 'span', 'song-learned-name', marker.name));
        return item;
      })
    );
  }
  if (section.children.length === 1) {
    section.append(element(doc, 'p', 'song-learned-note', NOTHING_FOUND_TEXT));
  }
  return section;
}
