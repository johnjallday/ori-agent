// A Home's manager offers the songs the assistant brought to the Home, as the
// first thing in its conversation in the drawer: a message in the manager's
// name with the offer card under it. The message is host-written from the
// Home's suggestions read (no model call) and is never saved to the manager's
// chat session; it shows while the suggestion is open and becomes the
// manager's receipt once the song is opened. The card makes the one shared
// press (home-song-offer.js), the same one the Home page's card makes.
import './folder-plan-lines.js';
import {
  BRIEF_MAX,
  followSongOpen,
  pressSongOpen,
  readSongSuggestions,
  songOpenedView,
  songOpeningLabel
} from './home-song-offer.js';
import { RECIPIENT_WORKSPACE_ENTRY } from './personal-assistant-recipient.js';
import { assistantPanelOpenIntent } from './personal-assistant-panel.js';
import { createHomeSongPlacement, homeSongPlacementAvailable } from './home-song-placement.js';

export const OFFER_LIMIT = 5;
export const NOT_NOW_KEY_PREFIX = 'ori.personalAssistant.homeOffer.notNow.';
export const BRIEF_KEY_PREFIX = 'ori.personalAssistant.homeOffer.brief.';
// After a stop: the press made nothing, so a chosen point placed nothing.
export const NOTHING_OPENED = 'Nothing was opened or placed yet.';

/** The manager asks which project file a song with several should open. */
export function chooseFileLine(view) {
  return `Which project file should I open for ${view.song}?`;
}

const WORKSPACE_PATH = /^\/workspaces\/([^/?#]+)(?:\/|$)/;

function text(value) {
  return String(value ?? '').trim();
}

/** The Home's own dashboard page, where its suggestion card always waits. */
export function homePageRoute(pathname) {
  const match = WORKSPACE_PATH.exec(String(pathname || ''));
  return match ? `/workspaces/${match[1]}/assistant` : '';
}

/**
 * What the drawer shows for the Home's waiting songs. `offers` while the
 * Home's entry agent is the recipient; `unavailable` (one line to the Home's
 * page) when the entry agent cannot be addressed; `none` otherwise, including
 * under the Personal Assistant.
 */
export function homeOfferPresentation({ resolution, views } = {}) {
  const songs = (Array.isArray(views) ? views : []).slice(0, OFFER_LIMIT);
  const entry = (resolution?.options || []).find(
    option => option?.kind === RECIPIENT_WORKSPACE_ENTRY
  );
  if (!songs.length || !entry) return { kind: 'none', views: [] };
  const current = resolution.current;
  if (current?.kind === RECIPIENT_WORKSPACE_ENTRY && current.available) {
    const name = text(current.name);
    return {
      kind: 'offers',
      // The card speaks as the agent the drawer is addressed to, by the name
      // its header shows ("<manager> is opening…", "<manager> opened it").
      views: name ? songs.map(view => ({ ...view, manager: name })) : songs,
      manager: { name, appearance: current.appearance || null },
      homeName: text(current.workspaceName)
    };
  }
  if (entry.disabled) return { kind: 'unavailable', views: songs };
  return { kind: 'none', views: [] };
}

/** The manager's own words for one waiting song. */
export function homeOfferMessage(view, homeName) {
  const assistant = text(view?.hire?.name) || 'its assistant';
  const home = text(homeName) || 'this Home';
  return `I listed ${view.song} in ${home}. Want me to open it and hire the ${assistant}? Tell me what it should do with it.`;
}

/** Not now: the song waits here, and on the Home's page. */
export function waitsHereLine(view) {
  return `${view.song} waits here. Open it from the Home’s page when you’re ready.`;
}

/** The entry agent cannot be addressed: the song is opened from the Home's page. */
export function unavailableLine(view) {
  return `Open ${view.song} from the Home’s page`;
}

/**
 * One card's state from the shared view and this tab's facts:
 * opened (the receipt), notNow (one line), elsewhere (the assistant's card has
 * the answer), running (pressed or the run is on), or offer.
 */
export function homeOfferCardState(view, local = {}) {
  if (local.opened) return 'opened';
  if (view.running || local.pressing) return 'running';
  // A song with more than one project file is answered here, by choosing one.
  if (view.choices?.length) return 'choose';
  if (view.waitsElsewhere) return 'elsewhere';
  if (local.notNow) return 'notNow';
  return 'offer';
}

function safeStorage(storage) {
  if (storage) return storage;
  try {
    return typeof sessionStorage === 'undefined' ? null : sessionStorage;
  } catch (_) {
    return null;
  }
}

/** Not now is per tab and per Home: the server never hears of it. */
export function readNotNow(homeID, storage) {
  try {
    const raw = safeStorage(storage)?.getItem(NOT_NOW_KEY_PREFIX + text(homeID));
    const list = JSON.parse(raw || '[]');
    return new Set(Array.isArray(list) ? list.filter(id => typeof id === 'string') : []);
  } catch (_) {
    return new Set();
  }
}

export function writeNotNow(homeID, ids, storage) {
  try {
    const store = safeStorage(storage);
    const key = NOT_NOW_KEY_PREFIX + text(homeID);
    if (!ids.size) store?.removeItem(key);
    else store?.setItem(key, JSON.stringify([...ids]));
  } catch (_) {
    // A private window may refuse storage; Not now then lasts for the page.
  }
}

/** The brief typed for one song in this tab, or null when none was. */
export function readBrief(offerID, storage) {
  try {
    const value = safeStorage(storage)?.getItem(BRIEF_KEY_PREFIX + text(offerID));
    return typeof value === 'string' ? value : null;
  } catch (_) {
    return null;
  }
}

export function writeBrief(offerID, value, storage) {
  try {
    safeStorage(storage)?.setItem(BRIEF_KEY_PREFIX + text(offerID), String(value ?? ''));
  } catch (_) {
    // As above: the brief then lasts for the page.
  }
}

/**
 * The Home's page shows the opened song: its members reload, its scoped map
 * redraws with the new building, and the building is selected the way a
 * workspace added to a group is (workspace-command.js handleWorkspacesChanged).
 */
export function revealOpenedSong(opened, win = globalThis) {
  const workspaceId = text(opened?.workspaceID);
  if (!workspaceId || typeof win?.dispatchEvent !== 'function') return false;
  win.dispatchEvent(
    new CustomEvent('ori:workspaces-changed', {
      detail: { workspaceId, reason: 'home-song-opened' }
    })
  );
  return true;
}

function element(doc, tag, className, content) {
  const node = doc.createElement(tag);
  if (className) node.className = className;
  if (content !== undefined) node.textContent = content;
  return node;
}

/**
 * The drawer's half: reads the Home's suggestions when the drawer opens or the
 * recipient changes, draws the manager's message and card per song, and runs
 * the shared press. `placement` (optional) may take a press to the Home's map
 * first; without it every press is the automatic path.
 */
export function createHomeOffer({
  doc = document,
  mount,
  fetchImpl = (...args) => globalThis.fetch(...args),
  storage,
  recipients = () => globalThis.PersonalAssistantRecipient || null,
  pathname = () => globalThis.location?.pathname || '',
  avatar = () => '',
  onOpened = () => {},
  arrival = false,
  isOpen = () => true
} = {}) {
  if (!mount) return null;
  const local = new Map();
  let views = [];
  let homeID = '';
  let presentation = { kind: 'none', views: [] };
  let sequence = 0;
  // A boolean, or a getter read when first needed (the drawer knows by then).
  let focusOnArrival = typeof arrival === 'function' ? null : Boolean(arrival);
  // A part that had focus when a redraw had nothing to put it on.
  let droppedFocus = null;
  let lastKey = '';
  let placement = null;
  const listeners = new Set();
  const live = element(doc, 'p', 'visually-hidden');
  live.setAttribute('role', 'status');
  live.setAttribute('aria-live', 'polite');

  function facts(offerID) {
    if (!local.has(offerID)) local.set(offerID, {});
    return local.get(offerID);
  }

  function announce(message) {
    live.textContent = '';
    live.textContent = message;
  }

  function brief(view) {
    return readBrief(view.offerID, storage) ?? view.brief;
  }

  async function refresh() {
    const resolution = recipients()?.resolution?.() || null;
    const workspaceID =
      text(resolution?.current?.workspaceId) ||
      text(
        (resolution?.options || []).find(o => o?.kind === RECIPIENT_WORKSPACE_ENTRY)?.workspaceId
      );
    const ticket = ++sequence;
    if (!workspaceID) {
      views = [];
      homeID = '';
      return render();
    }
    const read = await readSongSuggestions(fetchImpl, workspaceID);
    if (ticket !== sequence) return presentation;
    // A failed read keeps what was shown for this Home.
    if (read !== null || workspaceID !== homeID) views = read || [];
    homeID = workspaceID;
    return render();
  }

  function render() {
    const resolution = recipients()?.resolution?.() || null;
    // While the drawer re-reads who its recipient is, what is drawn stays: an
    // empty redraw would only flicker and drop the reader's focus.
    const current = resolution?.current;
    if (current?.kind === RECIPIENT_WORKSPACE_ENTRY && current.loading) return presentation;
    // A song opened in this tab keeps its receipt after the read stops listing it.
    const shown = [...views];
    for (const [offerID, item] of local) {
      if (item.opened && !shown.some(view => view.offerID === offerID)) shown.unshift(item.view);
    }
    presentation = homeOfferPresentation({ resolution, views: shown });
    const notNow = readNotNow(homeID, storage);
    for (const view of presentation.views) facts(view.offerID).notNow = notNow.has(view.offerID);
    // Nothing the reader can see changed: leave the drawn nodes (and focus,
    // and a control under the pointer) alone.
    const key = renderKey();
    if (key === lastKey) {
      arrive();
      notify();
      return presentation;
    }
    lastKey = key;
    const nodes = [];
    if (presentation.kind === 'offers') {
      for (const view of presentation.views) {
        const item = facts(view.offerID);
        item.view = view;
        item.notNow = notNow.has(view.offerID);
        nodes.push(message(view, item));
      }
    } else if (presentation.kind === 'unavailable') {
      const line = element(doc, 'p', 'pa-home-offer__unavailable');
      const link = element(doc, 'a', '', unavailableLine(presentation.views[0]));
      link.href = homePageRoute(pathname());
      line.append(link);
      nodes.push(line);
    }
    // A redraw (a later read, a state change) must not drop the reader's focus.
    // A redraw that briefly had no such part keeps it for the next one, unless
    // the reader has since moved focus elsewhere.
    const nowHeld = focusedPart();
    const idle = !doc.activeElement || doc.activeElement === doc.body;
    const held = nowHeld || (idle ? droppedFocus : null);
    mount.replaceChildren(...nodes, live);
    mount.hidden = nodes.length === 0;
    droppedFocus = held && !restoreFocus(held) ? held : null;
    if (!held) arrive();
    for (const listener of listeners) {
      try {
        listener(state());
      } catch (_) {
        // One listener's failure never stops the offer from rendering.
      }
    }
    return presentation;
  }

  function message(view, item) {
    const state = homeOfferCardState(view, item);
    const article = element(doc, 'article', 'pa-home-offer');
    article.dataset.offerId = view.offerID;
    article.dataset.state = state;
    article.tabIndex = -1;
    const headingID = `paHomeOffer-${view.offerID}`;
    article.setAttribute('aria-labelledby', headingID);
    const author = element(doc, 'p', 'personal-assistant-message__author');
    const face = element(doc, 'span', 'personal-assistant-message__author-avatar');
    face.setAttribute('aria-hidden', 'true');
    face.innerHTML = avatar(presentation.manager);
    author.append(face, element(doc, 'span', '', presentation.manager.name));
    article.append(author);
    if (state === 'opened') {
      article.append(receipt(item.opened, headingID));
      return article;
    }
    const words = element(
      doc,
      'p',
      'pa-home-offer__text',
      homeOfferMessage(view, presentation.homeName)
    );
    words.id = headingID;
    article.append(words);
    if (state === 'notNow') {
      const line = element(doc, 'p', 'pa-home-offer__waits', `${waitsHereLine(view)} `);
      const link = element(doc, 'a', '', 'Open the Home’s page');
      link.href = homePageRoute(pathname());
      line.append(link);
      article.append(line);
      return article;
    }
    article.append(card(view, item, state));
    return article;
  }

  function card(view, item, state) {
    const section = element(doc, 'section', 'pa-home-offer__card');
    section.setAttribute('aria-label', `Open ${view.song}`);
    if (view.fileCaption)
      section.append(element(doc, 'p', 'folder-entry-choice__caption', view.fileCaption));
    if (view.hire) {
      const hire = element(doc, 'p', 'pa-home-offer__hire');
      hire.append('Hires ', element(doc, 'strong', '', view.hire.name));
      if (view.hire.detail) hire.append(` · ${view.hire.detail}`);
      section.append(hire);
    }
    const list = element(doc, 'ul', 'pa-folder__plan pa-home-offer__lines');
    list.setAttribute('aria-label', `What opening ${view.song} does`);
    globalThis.OriFolderPlanLines?.renderSetupLines?.(list, view.lines, doc, {
      sections: view.sections,
      half: 'song'
    });
    section.append(list);
    let field = null;
    if (view.asksBrief) {
      const label = element(doc, 'label', 'pa-home-offer__brief');
      label.append(element(doc, 'span', '', 'What should it do with it?'));
      field = element(doc, 'textarea', 'form-control');
      field.rows = 2;
      field.maxLength = BRIEF_MAX;
      field.value = brief(view);
      field.disabled = state !== 'offer';
      field.dataset.briefFor = view.offerID;
      field.addEventListener('input', () => {
        writeBrief(view.offerID, field.value, storage);
        notify();
      });
      label.append(
        field,
        element(doc, 'small', '', 'This becomes the workspace goal. You can change it later.')
      );
      section.append(label);
    }
    const actions = element(doc, 'div', 'pa-home-offer__actions');
    const status = element(doc, 'p', 'pa-home-offer__status');
    status.setAttribute('role', 'status');
    if (state === 'choose') {
      // The run stopped before anything was made: ask which file, here.
      section.append(element(doc, 'p', 'pa-home-offer__ask', chooseFileLine(view)));
      actions.setAttribute('role', 'group');
      actions.setAttribute('aria-label', `Project files in ${view.song}`);
      for (const name of view.choices) {
        const choice = element(doc, 'button', 'modern-btn modern-btn-outline', name);
        choice.type = 'button';
        choice.dataset.homeOfferChoice = name;
        choice.setAttribute('aria-label', `Open ${view.song} with ${name}`);
        choice.addEventListener('click', () => void open(view, { entryName: name }));
        actions.append(choice);
      }
      status.textContent = item.message || NOTHING_OPENED;
    } else if (state === 'elsewhere') {
      status.textContent = [view.stopCopy, item.message].filter(Boolean).join(' ');
      const link = element(doc, 'a', 'modern-btn modern-btn-primary', 'Go to your assistant');
      link.href = '/';
      actions.append(link);
    } else {
      const press = element(doc, 'button', 'modern-btn modern-btn-primary', view.label);
      press.type = 'button';
      press.dataset.homeOfferPress = view.offerID;
      if (state === 'running') {
        press.disabled = true;
        press.textContent = songOpeningLabel(view);
      }
      press.addEventListener('click', () => void open(view));
      actions.append(press);
      if (state === 'offer') {
        const later = element(doc, 'button', 'modern-btn modern-btn-secondary', 'Not now');
        later.type = 'button';
        later.addEventListener('click', () => notNow(view));
        actions.append(later);
      }
      status.textContent = item.message || '';
    }
    section.append(actions, status);
    return section;
  }

  function receipt(opened, headingID) {
    const section = element(doc, 'section', 'pa-home-offer__card pa-home-offer__receipt');
    const heading = element(doc, 'h3', '', opened.heading);
    heading.id = headingID;
    section.append(element(doc, 'span', 'pa-home-offer__kicker', opened.kicker), heading);
    if (opened.rows.length) {
      const rows = element(doc, 'ul', 'pa-home-offer__rows');
      for (const row of opened.rows) rows.append(element(doc, 'li', '', row.text));
      section.append(rows);
    }
    if (opened.note) section.append(element(doc, 'p', 'pa-home-offer__note', opened.note));
    if (opened.route) {
      const link = element(doc, 'a', 'modern-btn modern-btn-primary', opened.openLabel);
      link.href = opened.route;
      section.append(link);
    }
    return section;
  }

  function notNow(view) {
    const ids = readNotNow(homeID, storage);
    ids.add(view.offerID);
    writeNotNow(homeID, ids, storage);
    render();
    announce(waitsHereLine(view));
    find('.pa-home-offer', 'offerId', view.offerID)?.focus?.();
  }

  // Everything a redraw would show, so an unchanged one can be skipped. The
  // brief field's text is not part of it: the field is the source of that text.
  function renderKey() {
    return JSON.stringify({
      kind: presentation.kind,
      manager: presentation.manager || null,
      home: presentation.homeName || '',
      path: pathname(),
      cards: presentation.views.map(view => {
        const item = facts(view.offerID);
        return {
          view: { ...view, brief: undefined },
          state: homeOfferCardState(view, item),
          message: item.message || '',
          opened: item.opened || null
        };
      })
    });
  }

  // On arrival (`?panel=assistant`) focus goes to the manager's message once,
  // when both the drawer is open and the offer is drawn, whichever is last:
  // the drawer's own opening focus comes first and is then moved here (FR22).
  function arrive() {
    if (focusOnArrival === null) focusOnArrival = Boolean(arrival());
    if (!focusOnArrival || presentation.kind !== 'offers' || !isOpen()) return false;
    const article = mount.querySelector('.pa-home-offer');
    if (!article) return false;
    focusOnArrival = false;
    article.focus?.();
    return true;
  }

  // Which of the offer's own parts has focus, so a redraw can put it back.
  function focusedPart() {
    const active = doc.activeElement;
    if (!active || typeof mount.contains !== 'function' || !mount.contains(active)) return null;
    const article = active.closest?.('.pa-home-offer');
    const offerID = article?.dataset?.offerId;
    if (!offerID) return null;
    const part =
      active === article
        ? 'article'
        : active.dataset?.briefFor
          ? 'brief'
          : active.dataset?.homeOfferPress
            ? 'press'
            : active.textContent === 'Not now'
              ? 'later'
              : 'article';
    return { offerID, part, caret: part === 'brief' ? active.selectionStart : null };
  }

  function restoreFocus({ offerID, part, caret }) {
    focusOnArrival = false;
    const article = find('.pa-home-offer', 'offerId', offerID);
    if (!article) return false;
    const target =
      part === 'brief'
        ? article.querySelector('[data-brief-for]')
        : part === 'press'
          ? article.querySelector('[data-home-offer-press]')
          : part === 'later'
            ? [...article.querySelectorAll('button')].find(b => b.textContent === 'Not now')
            : null;
    const next = target && !target.disabled ? target : article;
    next.focus?.();
    if (next === target && part === 'brief' && typeof caret === 'number')
      target.setSelectionRange?.(caret, caret);
    return true;
  }

  // Offer ids are the server's; matching the dataset needs no selector escaping.
  function find(selector, key, value) {
    return [...mount.querySelectorAll(selector)].find(node => node.dataset[key] === value) || null;
  }

  /** The press: the map first when placement takes it, else at once. */
  async function open(view, { entryName = '' } = {}) {
    const item = facts(view.offerID);
    if (item.pressing || view.running || item.opened) return;
    const typed = view.asksBrief ? text(brief(view)) : '';
    if (typed.length > BRIEF_MAX) {
      item.message = `The brief is longer than ${BRIEF_MAX} characters. Shorten it first.`;
      render();
      return;
    }
    if (placement?.wants?.(view)) {
      await placement.begin(view, {
        run: (options = {}) => run(view, typed, { ...options, entryName })
      });
      return;
    }
    await run(view, typed, { entryName });
  }

  /**
   * Runs the shared press and follows it. `afterVerified(outcome)` (from a
   * placement) may save the chosen point once and return a note for the
   * receipt; a failed point never re-presses.
   */
  async function run(view, typed, { afterVerified, entryName = '' } = {}) {
    const item = facts(view.offerID);
    if (item.pressing || item.opened) return false;
    item.pressing = true;
    item.message = '';
    render();
    announce(songOpeningLabel(view));
    try {
      const pressed = await pressSongOpen(fetchImpl, view, typed, { entryName });
      if (!pressed.ok) {
        item.message = pressed.message;
        return false;
      }
      const settled = await followSongOpen(fetchImpl, view, { live: () => mount.isConnected });
      if (settled.state === 'stopped') {
        // Say plainly that the click did nothing yet: no workspace, no building.
        item.message = NOTHING_OPENED;
        announce(`${view.song} was not opened. ${NOTHING_OPENED}`);
      }
      if (settled.state !== 'opened') return false;
      const opened = songOpenedView(view, settled.outcome);
      if (typeof afterVerified === 'function') {
        try {
          opened.note = text(await afterVerified(settled.outcome, opened));
        } catch (_) {
          opened.note = 'Its position was not saved; use Move.';
        }
      }
      item.opened = opened;
      announce(`${opened.heading}. ${opened.kicker}.`);
      try {
        await onOpened(opened, view);
      } catch (_) {
        // Showing the building is a courtesy; the receipt is the result.
      }
      return true;
    } finally {
      item.pressing = false;
      await refresh();
    }
  }

  function state() {
    return {
      kind: presentation.kind,
      manager: presentation.manager || null,
      pending:
        presentation.kind === 'offers'
          ? presentation.views.filter(
              view => homeOfferCardState(view, facts(view.offerID)) === 'offer'
            )
          : []
    };
  }

  function notify() {
    const now = state();
    for (const listener of listeners) {
      try {
        listener(now);
      } catch (_) {
        // As in render.
      }
    }
  }

  return {
    refresh,
    render,
    state,
    open,
    run,
    announce,
    brief,
    /** Sets one song's brief (the composer's reply) and shows it in its field. */
    setBrief(offerID, value) {
      writeBrief(offerID, value, storage);
      const field = find('[data-brief-for]', 'briefFor', offerID);
      if (field) field.value = String(value ?? '');
      notify();
    },
    usePlacement(next) {
      placement = next || null;
    },
    /** The drawer opened: an arrival's focus moves to the offer if it is drawn. */
    arrive,
    /** Moves focus to one song's message (after returning from the map). */
    focus(offerID) {
      find('.pa-home-offer', 'offerId', offerID)?.focus?.();
    },
    onChange(listener) {
      if (typeof listener !== 'function') return () => {};
      listeners.add(listener);
      return () => listeners.delete(listener);
    }
  };
}

if (typeof window !== 'undefined' && typeof document !== 'undefined') {
  // The address bar may already have lost `?panel=assistant` to the drawer
  // when this runs (a cached reload), so the drawer's record is asked as well.
  const arrivedNow =
    assistantPanelOpenIntent(window.location.search, false, window.location.pathname).consumes ===
    'assistant';
  const arrival = () => arrivedNow || window.PersonalAssistantPanel?.arrival?.() === 'assistant';
  const start = () => {
    const controller = createHomeOffer({
      mount: document.getElementById('personalAssistantHomeOffer'),
      avatar: who =>
        window.AgentAvatar?.markup
          ? window.AgentAvatar.markup(
              { name: who?.name || '', appearance: who?.appearance || {} },
              { size: 20 }
            )
          : '',
      onOpened: opened => revealOpenedSong(opened, window),
      arrival,
      isOpen: () => window.PersonalAssistantPanel?.isOpen?.() === true
    });
    if (!controller) return;
    window.PersonalAssistantHomeOffer = controller;
    // The composer's brief mode follows the offer (personal-assistant-brief-mode.js).
    controller.onChange(() => window.PersonalAssistantPanel?.syncBriefMode?.());
    // On the Home's own map, a press first takes a click inside the Home.
    const home = () => {
      const current = window.PersonalAssistantRecipient?.current?.() || {};
      return { id: String(current.workspaceId || ''), name: String(current.workspaceName || '') };
    };
    controller.usePlacement(
      createHomeSongPlacement({
        map: () => window.OriWorkspaceMap,
        panel: window.PersonalAssistantPanel,
        home,
        available: () =>
          homeSongPlacementAvailable({
            homeID: home().id,
            map: window.OriWorkspaceMap,
            innerWidth: window.innerWidth,
            viewport: document.querySelector('[data-ws-map-viewport]')
          }),
        focusCard: view => controller.focus(view.offerID)
      })
    );
    const refreshIfOpen = () => {
      if (window.PersonalAssistantPanel?.isOpen?.() !== false) void controller.refresh();
    };
    // `opened` follows the drawer's own opening focus, so an offer already
    // drawn takes the arrival focus here; otherwise the read below does.
    document.addEventListener('personal-assistant:opened', () => {
      controller.arrive();
      void controller.refresh();
    });
    document.addEventListener('personal-assistant:recipient', refreshIfOpen);
  };
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', start);
  else start();
}
