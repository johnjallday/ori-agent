// A Home's manager suggests opening the single songs the assistant brought to
// it: the one press that makes the song's workspace and hires its assistant.
// The card reads the same offer the assistant's card does and presses the same
// Set up (with open), so the server rechecks the plan it was shown; it never
// opens a song itself and carries no path.
import {
  BRIEF_MAX,
  followSongOpen,
  folderDigestOffer,
  pressSongOpen,
  readSongSuggestions,
  songOpenedView,
  songOpeningLabel,
  songSuggestionView
} from './home-song-offer.js';

// The view and the offer read are shared with the manager's offer in the drawer.
export { folderDigestOffer, songSuggestionView };

function text(value) {
  return String(value ?? '').trim();
}

function element(tag, className, content) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (content !== undefined) node.textContent = content;
  return node;
}

export class HomeSongSuggestions {
  constructor({ workspaceId, anchor, fetchImpl = globalThis.fetch } = {}) {
    this.workspaceId = text(workspaceId);
    this.anchor = anchor || null;
    this.fetchImpl = (...args) => fetchImpl(...args);
    this.root = null;
    this.busy = new Set();
    this.messages = new Map();
    this.briefs = new Map();
  }

  async init() {
    if (!this.workspaceId || !this.anchor?.parentNode) return;
    this.root = element('div', 'assistant-program-suggestions');
    this.root.id = 'homeSongSuggestions';
    this.anchor.parentNode.insertBefore(this.root, this.anchor);
    await this.load();
  }

  async load() {
    // Advisory: without the read the Home shows no suggestion.
    this.render((await readSongSuggestions(this.fetchImpl, this.workspaceId)) || []);
  }

  render(views) {
    if (!this.root) return;
    this.root.replaceChildren(...views.map(view => this.card(view)));
    this.root.hidden = views.length === 0;
  }

  card(view) {
    const card = element('section', 'assistant-program-suggestion');
    card.dataset.offerId = view.offerID;
    const titleID = `homeSongSuggestion-${view.offerID}`;
    card.setAttribute('aria-labelledby', titleID);
    const kicker = element('span', 'assistant-program-kicker', `${view.manager} suggests`);
    const title = element('h2', '', `Open ${view.song}`);
    title.id = titleID;
    const body = element(
      'p',
      '',
      `${view.song} is listed in this Home. Opening it makes its own workspace and hires its assistant to work on it.`
    );
    card.append(kicker, title, body);
    if (view.hire) {
      const hire = element('p', 'assistant-program-suggestion-hire');
      hire.append('Hires ', element('strong', '', view.hire.name));
      if (view.hire.detail) hire.append(` · ${view.hire.detail}`);
      card.append(hire);
    }
    let brief = null;
    if (view.asksBrief) {
      const label = element('label', 'assistant-program-suggestion-brief');
      label.append(element('span', '', 'What should it do with it?'));
      brief = element('textarea', 'form-control');
      brief.rows = 2;
      brief.maxLength = BRIEF_MAX;
      // A redraw while it opens keeps what the user typed.
      brief.value = this.briefs.get(view.offerID) ?? view.brief;
      brief.disabled = view.running || this.busy.has(view.offerID);
      brief.addEventListener('input', () => this.briefs.set(view.offerID, brief.value));
      label.append(
        brief,
        element('small', '', 'This becomes the workspace goal. You can change it later.')
      );
      card.append(label);
    }
    const list = element('ul', 'pa-folder__plan assistant-program-suggestion-lines');
    list.setAttribute('aria-label', `What opening ${view.song} does`);
    globalThis.OriFolderPlanLines.renderSetupLines(list, view.lines, document, {
      sections: view.sections,
      half: 'song'
    });
    card.append(list);
    const actions = element('div', 'assistant-program-suggestion-actions');
    const status = element('p', 'assistant-program-suggestion-status');
    status.setAttribute('role', 'status');
    const message = this.messages.get(view.offerID) || '';
    if (view.waitsElsewhere) {
      status.textContent = view.stopCopy;
      const link = element('a', 'modern-btn modern-btn-primary', 'Go to your assistant');
      link.href = '/';
      actions.append(link);
    } else {
      const button = element('button', 'modern-btn modern-btn-primary', view.label);
      button.type = 'button';
      button.disabled = view.running || this.busy.has(view.offerID);
      if (view.running) button.textContent = songOpeningLabel(view);
      button.addEventListener('click', () => void this.open(view, brief));
      actions.append(button);
      status.textContent = message;
    }
    card.append(actions, status);
    return card;
  }

  async open(view, brief) {
    if (this.busy.has(view.offerID)) return;
    const typed = brief ? brief.value.trim() : '';
    if (typed.length > BRIEF_MAX) return;
    this.busy.add(view.offerID);
    this.messages.set(view.offerID, songOpeningLabel(view));
    await this.load();
    try {
      const pressed = await pressSongOpen(this.fetchImpl, view, typed);
      if (!pressed.ok) {
        this.messages.set(view.offerID, pressed.message);
        return;
      }
      const settled = await followSongOpen(this.fetchImpl, view);
      if (settled.state === 'opened') this.opened(view, settled.outcome);
    } finally {
      this.busy.delete(view.offerID);
      await this.load();
    }
  }

  // opened replaces the card with the song it opened, until the page reloads.
  opened(view, outcome) {
    this.messages.delete(view.offerID);
    const opened = songOpenedView(view, outcome);
    const note = element('section', 'assistant-program-suggestion is-opened');
    note.append(
      element('span', 'assistant-program-kicker', opened.kicker),
      element('h2', '', opened.heading),
      element('p', '', opened.body)
    );
    if (opened.route) {
      const link = element('a', 'modern-btn modern-btn-primary', opened.openLabel);
      link.href = opened.route;
      note.append(link);
    }
    this.root?.after?.(note);
  }
}
