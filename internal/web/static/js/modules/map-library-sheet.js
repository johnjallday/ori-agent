// map-library-sheet.js — the Home map's "set up projects" sheet.
//
// PRD: tasks/prd-map-library-setup.md FR-1, FR-3, FR-6 to FR-15, FR-19. A
// Home district's library badge ("5 to set up") opens this popover beside the
// district. It lists what the last scan found, lets the person tick the
// projects to set up, choose a file where a project has several, and press one
// button. The reviews and commits are library-setup.js's, one project at a
// time; nothing here creates, changes or deletes anything on its own.
//
// Only the view model is pure (sheetView, confirmLines). openLibrarySheet
// mounts DOM and is covered by the demo.

import {
  SETUP_KINDS,
  createLibraryClient,
  loadSetupRows,
  runSetup,
  setupLabel,
  setupSubtitle,
  setupTotals
} from './library-setup.js';

export const CONSENT_LINE =
  'Each starts File-only. Source files stay where they are and are never changed.';

const SHEET_CLASS = 'ws-map-library-sheet';

/**
 * The sheet's view model for one render. `rows` come from loadSetupRows,
 * `chosen` maps row id → chosen file (file-choice rows), `ticked` is the set of
 * ticked row ids, `progress` maps row id → { status, message } during and
 * after a run.
 */
export function sheetView(rows, { chosen = {}, ticked = new Set(), progress = {} } = {}) {
  // What can be set up as it is comes first, then what needs a file chosen,
  // then what is already on the map, then what needs the dashboard; inside a
  // bucket the library's order is kept.
  const rank = { setup: 0, file_choice: 1, connected: 2, dashboard: 3 };
  const list = (Array.isArray(rows) ? rows : [])
    .filter(Boolean)
    .map((row, index) => ({ row, index }))
    .sort((a, b) => (rank[a.row.kind] ?? 3) - (rank[b.row.kind] ?? 3) || a.index - b.index)
    .map(entry => entry.row);
  const items = list.map(row => {
    const kind = row.kind;
    const file =
      kind === SETUP_KINDS.SETUP
        ? row.files[0]
        : kind === SETUP_KINDS.FILE_CHOICE && row.files.includes(chosen[row.id])
          ? chosen[row.id]
          : '';
    const checkable =
      (kind === SETUP_KINDS.SETUP || kind === SETUP_KINDS.FILE_CHOICE) && !progress[row.id];
    const checked = checkable && ticked.has(row.id) && file !== '';
    return {
      id: row.id,
      name: row.name,
      kind,
      label: setupLabel(kind),
      file,
      files: kind === SETUP_KINDS.FILE_CHOICE ? row.files.slice() : [],
      checkable,
      checked,
      reason: kind === SETUP_KINDS.DASHBOARD ? row.reason || setupLabel(kind) : '',
      progress: progress[row.id] || null
    };
  });
  const selected = items.filter(item => item.checked);
  return {
    items,
    totals: setupTotals(list),
    subtitle: setupSubtitle(setupTotals(list)),
    selected,
    buttonLabel: `Put ${selected.length} on the map`,
    buttonDisabled: selected.length === 0,
    allConnected: list.length > 0 && list.every(row => row.kind === SETUP_KINDS.CONNECTED)
  };
}

/** The confirmation's lines: each ticked project with its exact file (FR-11). */
export function confirmLines(selected) {
  return (Array.isArray(selected) ? selected : []).map(item => `${item.name} — ${item.file}`);
}

/** The rows ticked by default: single-file projects only (FR-7). */
export function defaultTicked(rows) {
  return new Set(
    (Array.isArray(rows) ? rows : [])
      .filter(row => row && row.kind === SETUP_KINDS.SETUP)
      .map(row => row.id)
  );
}

/**
 * Where the sheet goes: to the right of the anchor when there is room, else
 * below it, always inside the viewport with a 16px gutter. Pure.
 */
export function sheetPosition(anchorRect, sheetSize, viewport) {
  const gutter = 16;
  // The app's top bar is fixed and about 60px tall; a sheet clamped under it
  // on a short window would lose its title and Close button.
  const topGutter = 72;
  const gap = 12;
  const width = Math.min(sheetSize.width, viewport.width - gutter * 2);
  const height = Math.min(sheetSize.height, viewport.height - topGutter - gutter);
  let left = anchorRect.right + gap;
  let top = anchorRect.top;
  if (left + width > viewport.width - gutter) {
    left = anchorRect.left;
    top = anchorRect.bottom + 8;
  }
  left = Math.max(gutter, Math.min(left, viewport.width - gutter - width));
  top = Math.max(topGutter, Math.min(top, viewport.height - gutter - height));
  return { left, top, width, maxHeight: height };
}

let current = null;

const MAP_TOKENS = [
  '--ws-bg',
  '--ws-panel',
  '--ws-panel-2',
  '--ws-line',
  '--ws-line-bright',
  '--ws-ink',
  '--ws-ink-dim',
  '--ws-ink-faint',
  '--ws-font-mono'
];

// The map defines its palette on its own root; a sheet mounted on <body> sits
// outside it, so the values are copied over. Missing values keep the CSS
// fallbacks.
function copyMapTokens(target, source) {
  if (!source || typeof globalThis.getComputedStyle !== 'function') return;
  let computed;
  try {
    computed = globalThis.getComputedStyle(source);
  } catch (_) {
    return;
  }
  for (const token of MAP_TOKENS) {
    const value = computed.getPropertyValue(token).trim();
    if (value) target.style.setProperty(token, value);
  }
}

function el(doc, tag, className, text) {
  const node = doc.createElement(tag);
  if (className) node.className = className;
  if (text != null) node.textContent = String(text);
  return node;
}

/**
 * Open the sheet for one Home. Returns { close } or null when one is already
 * open for that Home (it is focused instead). Options:
 *   homeID, homeName: the Home whose library is read
 *   anchor: the element that opened the sheet; focus returns to it
 *   beside: the element the sheet sits next to (the district); anchor by default
 *   tokensFrom: an element whose --ws-* custom properties the sheet copies, so
 *     a sheet mounted on <body> still wears the map's light or dark palette
 *   shelfRoute: the dashboard suggestions-shelf route for "needs the dashboard"
 *   onDone({ workspaceIds }): called after a run that set up at least one project
 *   client: a library client (createLibraryClient by default)
 *   doc: the document (globalThis.document by default)
 */
export function openLibrarySheet({
  homeID,
  homeName = 'this Home',
  anchor = null,
  beside = null,
  tokensFrom = null,
  shelfRoute = '',
  onDone = () => {},
  client = null,
  doc = globalThis.document,
  fetchImpl = undefined
} = {}) {
  if (!doc || !homeID) return null;
  if (current) {
    if (current.homeID === homeID) {
      current.focus();
      return null;
    }
    current.close({ restoreFocus: false });
  }
  const api = client || createLibraryClient(homeID, fetchImpl);
  const state = {
    phase: 'loading', // loading | list | confirm | running | failed
    rows: [],
    chosen: {},
    ticked: new Set(),
    progress: {},
    message: '',
    created: []
  };

  const root = el(doc, 'div', SHEET_CLASS);
  root.setAttribute('role', 'dialog');
  root.setAttribute('aria-modal', 'false');
  root.setAttribute('data-library-sheet', String(homeID));
  const titleId = `wsMapLibrarySheetTitle-${Math.random().toString(36).slice(2, 8)}`;
  root.setAttribute('aria-labelledby', titleId);
  root.tabIndex = -1;
  copyMapTokens(root, tokensFrom || (anchor && anchor.closest ? anchor.closest('.ws-map') : null));

  const header = el(doc, 'div', `${SHEET_CLASS}-header`);
  const titleWrap = el(doc, 'div');
  const title = el(doc, 'h2', `${SHEET_CLASS}-title`, `Set up projects from ${homeName}`);
  title.id = titleId;
  const subtitle = el(doc, 'p', `${SHEET_CLASS}-subtitle`, 'Reading the last scan…');
  subtitle.setAttribute('aria-live', 'polite');
  titleWrap.append(title, subtitle);
  const closeButton = el(doc, 'button', `${SHEET_CLASS}-close`, '×');
  closeButton.type = 'button';
  closeButton.setAttribute('aria-label', 'Close');
  header.append(titleWrap, closeButton);

  const body = el(doc, 'div', `${SHEET_CLASS}-body`);
  const consent = el(doc, 'p', `${SHEET_CLASS}-consent`, CONSENT_LINE);
  const status = el(doc, 'p', `${SHEET_CLASS}-status`);
  status.setAttribute('role', 'status');
  status.setAttribute('aria-live', 'polite');
  const footer = el(doc, 'div', `${SHEET_CLASS}-footer`);
  root.append(header, body, consent, status, footer);

  function place() {
    const view = { width: globalThis.innerWidth || 1280, height: globalThis.innerHeight || 800 };
    // Beside the district (so it never covers the district's own header), at
    // the height of the control that opened it.
    const near = beside || anchor;
    const rect =
      near && typeof near.getBoundingClientRect === 'function'
        ? near.getBoundingClientRect()
        : { left: view.width / 2 - 220, right: view.width / 2 + 220, top: 80, bottom: 120 };
    const anchorRect =
      anchor && typeof anchor.getBoundingClientRect === 'function'
        ? anchor.getBoundingClientRect()
        : rect;
    const size = { width: 440, height: 640 };
    const pos = sheetPosition(
      { left: rect.left, right: rect.right, top: anchorRect.top, bottom: rect.bottom },
      size,
      view
    );
    root.style.left = `${pos.left}px`;
    root.style.top = `${pos.top}px`;
    root.style.width = `${pos.width}px`;
    root.style.maxHeight = `${pos.maxHeight}px`;
  }

  function focus() {
    const first =
      root.querySelector('input:not([disabled])') ||
      root.querySelector('button:not([disabled])') ||
      root;
    first.focus();
  }

  function close({ restoreFocus = true } = {}) {
    if (current !== handle) return;
    doc.removeEventListener('keydown', onKeydown, true);
    root.remove();
    current = null;
    if (restoreFocus && anchor && typeof anchor.focus === 'function' && anchor.isConnected)
      anchor.focus();
  }

  function onKeydown(event) {
    if (event.key !== 'Escape') return;
    if (!root.contains(doc.activeElement) && doc.activeElement !== doc.body) return;
    event.preventDefault();
    event.stopPropagation();
    close();
  }

  function renderRows(view) {
    const list = el(doc, 'ul', `${SHEET_CLASS}-rows`);
    for (const item of view.items) {
      const row = el(doc, 'li', `${SHEET_CLASS}-row is-${item.kind}`);
      if (item.progress) row.classList.add(`is-${item.progress.status}`);
      if (item.kind === SETUP_KINDS.SETUP || item.kind === SETUP_KINDS.FILE_CHOICE) {
        const label = el(doc, 'label', `${SHEET_CLASS}-pick`);
        const box = el(doc, 'input');
        box.type = 'checkbox';
        box.checked = item.checked;
        box.disabled = !item.checkable;
        box.setAttribute('data-sheet-row', item.id);
        box.addEventListener('change', () => {
          if (box.checked) state.ticked.add(item.id);
          else state.ticked.delete(item.id);
          if (box.checked && item.kind === SETUP_KINDS.FILE_CHOICE && !state.chosen[item.id]) {
            // Ticking a file-choice row without a file: keep the tick, ask for
            // the file; the button label only counts rows with a file.
            status.textContent = `Choose the project file for ${item.name} first.`;
          }
          render();
        });
        const name = el(doc, 'span', `${SHEET_CLASS}-name`, item.name);
        label.append(box, name);
        if (item.kind === SETUP_KINDS.SETUP) {
          label.append(el(doc, 'span', `${SHEET_CLASS}-file`, item.file));
        } else {
          label.append(
            el(doc, 'span', `${SHEET_CLASS}-state is-choice`, setupLabel(SETUP_KINDS.FILE_CHOICE))
          );
        }
        row.append(label);
        if (item.kind === SETUP_KINDS.FILE_CHOICE) {
          const choices = el(doc, 'div', `${SHEET_CLASS}-choices`);
          choices.setAttribute('role', 'radiogroup');
          choices.setAttribute('aria-label', `Project file for ${item.name}`);
          for (const file of item.files) {
            const choice = el(doc, 'label', `${SHEET_CLASS}-choice`);
            const radio = el(doc, 'input');
            radio.type = 'radio';
            radio.name = `sheet-file-${item.id}`;
            radio.value = file;
            radio.checked = item.file === file;
            radio.disabled = !item.checkable;
            radio.addEventListener('change', () => {
              state.chosen[item.id] = file;
              state.ticked.add(item.id);
              status.textContent = '';
              render();
            });
            choice.append(radio, el(doc, 'span', null, file));
            choices.append(choice);
          }
          row.append(choices);
        }
        if (item.progress) {
          row.append(
            el(
              doc,
              'span',
              `${SHEET_CLASS}-progress`,
              item.progress.status === 'working'
                ? 'Setting up…'
                : item.progress.status === 'done'
                  ? 'On the map'
                  : `Failed: ${item.progress.message || 'could not be set up'}`
            )
          );
        }
      } else if (item.kind === SETUP_KINDS.CONNECTED) {
        row.append(
          el(doc, 'span', `${SHEET_CLASS}-name`, item.name),
          el(doc, 'span', `${SHEET_CLASS}-state is-connected`, 'on the map')
        );
      } else {
        row.append(el(doc, 'span', `${SHEET_CLASS}-name`, item.name));
        const why = el(doc, 'span', `${SHEET_CLASS}-state is-dashboard`, item.reason);
        why.title = item.reason;
        row.append(why);
        if (shelfRoute) {
          const link = el(doc, 'a', `${SHEET_CLASS}-link`, 'open the shelf');
          link.href = shelfRoute;
          row.append(link);
        }
      }
      list.append(row);
    }
    return list;
  }

  function render() {
    const view = sheetView(state.rows, state);
    body.replaceChildren();
    footer.replaceChildren();
    consent.hidden = state.phase === 'loading';
    if (state.phase === 'loading') {
      subtitle.textContent = 'Reading the last scan…';
      body.append(el(doc, 'p', `${SHEET_CLASS}-empty`, 'One moment.'));
    } else if (state.phase === 'confirm') {
      subtitle.textContent = view.subtitle;
      const intro = el(
        doc,
        'p',
        `${SHEET_CLASS}-confirm-intro`,
        `Set up ${view.selected.length} ${
          view.selected.length === 1 ? 'project' : 'projects'
        }, each as its own workspace in ${homeName}:`
      );
      const lines = el(doc, 'ul', `${SHEET_CLASS}-confirm`);
      for (const line of confirmLines(view.selected)) lines.append(el(doc, 'li', null, line));
      body.append(intro, lines);
      const go = el(doc, 'button', 'modern-btn modern-btn-primary', 'Put on the map');
      go.type = 'button';
      go.addEventListener('click', () => void run(view.selected));
      const back = el(doc, 'button', 'modern-btn modern-btn-secondary', 'Back');
      back.type = 'button';
      back.addEventListener('click', () => {
        state.phase = 'list';
        render();
        focus();
      });
      footer.append(go, back);
    } else {
      subtitle.textContent = view.subtitle;
      if (!view.items.length) {
        body.append(
          el(doc, 'p', `${SHEET_CLASS}-empty`, 'The last scan found no projects to set up.')
        );
      } else {
        body.append(renderRows(view));
      }
      const go = el(doc, 'button', 'modern-btn modern-btn-primary', view.buttonLabel);
      go.type = 'button';
      go.disabled = view.buttonDisabled || state.phase === 'running';
      go.addEventListener('click', () => {
        state.phase = 'confirm';
        render();
        focus();
      });
      const cancel = el(doc, 'button', 'modern-btn modern-btn-secondary', 'Cancel');
      cancel.type = 'button';
      cancel.disabled = state.phase === 'running';
      cancel.addEventListener('click', () => close());
      footer.append(go, cancel);
      if (shelfRoute) {
        const link = el(doc, 'a', `${SHEET_CLASS}-shelf`, 'Open the project shelf');
        link.href = shelfRoute;
        footer.append(link);
      }
    }
    place();
  }

  async function load() {
    state.phase = 'loading';
    render();
    try {
      const result = await loadSetupRows(api);
      state.rows = result.rows;
      if (result.readOnly) {
        state.message = 'This Home is read-only; projects can only be viewed here.';
      }
      // Keep ticks the person already made (a reload after a run); tick the
      // single-file rows the first time.
      state.ticked = state.ticked.size ? state.ticked : defaultTicked(state.rows);
      for (const id of [...state.ticked]) {
        if (!state.rows.some(row => row.id === id && row.kind !== SETUP_KINDS.CONNECTED))
          state.ticked.delete(id);
      }
      state.phase = 'list';
    } catch (error) {
      state.rows = [];
      state.phase = 'list';
      state.message = error?.message || 'The library could not be read.';
    }
    status.textContent = state.message;
    render();
  }

  async function run(selected) {
    state.phase = 'running';
    state.progress = {};
    status.textContent = '';
    render();
    const result = await runSetup({
      client: api,
      items: selected.map(item => ({ id: item.id, name: item.name, file: item.file })),
      onProgress: (item, phase, detail) => {
        state.progress[item.id] = { status: phase, message: detail?.message || '' };
        render();
      }
    });
    state.created.push(...result.done.map(item => item.workspaceId).filter(Boolean));
    for (const item of result.done) state.ticked.delete(item.id);
    if (result.failed) {
      state.message = `Stopped at ${result.failed.name}: ${result.failed.message}. ${
        result.remaining.length ? `${result.remaining.length} not reached and left ticked.` : ''
      }`.trim();
    } else {
      state.message = `${result.done.length} ${
        result.done.length === 1 ? 'project is' : 'projects are'
      } on the map.`;
    }
    if (result.done.length) onDone({ workspaceIds: state.created.slice() });
    state.progress = {};
    await load();
    if (!result.failed) {
      const view = sheetView(state.rows, state);
      if (view.selected.length === 0 && result.done.length) close();
    }
  }

  const handle = { homeID, close, focus, root };
  current = handle;
  closeButton.addEventListener('click', () => close());
  doc.addEventListener('keydown', onKeydown, true);
  doc.body.append(root);
  render();
  focus();
  void load().then(() => focus());
  return handle;
}

/** For tests and the cockpit: the sheet currently open, if any. */
export function currentLibrarySheet() {
  return current;
}
