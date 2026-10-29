// A Home's manager suggests opening the single songs the assistant brought to
// it: the one press that makes the song's workspace and hires its assistant.
// The card reads the same offer the assistant's card does and presses the same
// Set up (with open), so the server rechecks the plan it was shown; it never
// opens a song itself and carries no path.
import './folder-plan-lines.js';

const DIGEST_ENDPOINT = '/api/personal-assistant/folder-digest';
const POLL_MS = 1500;
const POLL_LIMIT = 200;
const BRIEF_MAX = 2000;

const STOP_COPY = {
  needs_model: 'Its assistant needs a model first. Your assistant kept what finished.',
  needs_choice: 'It has more than one project file. Your assistant asks which one to open.',
  needs_pick: 'Ori no longer has its folder open. Your assistant asks you to pick it again.',
  consent_stale: 'Its shared agent needs your review first.',
  assistant_missing: 'The shared agent this Home used is gone. Choose its agent first.'
};

function text(value) {
  return String(value ?? '').trim();
}

// songSuggestionView is one suggestion as the card shows it, or null for an
// offer that is not a song this Home's manager can open.
export function songSuggestionView(offer) {
  const planLines = globalThis.OriFolderPlanLines;
  if (!offer || typeof offer.id !== 'string' || !offer.id || !planLines) return null;
  const setup = offer.setup || null;
  const source = setup || offer.plan;
  const lines = Array.isArray(source?.lines) ? source.lines : [];
  const parts = planLines.handoffLineParts(lines);
  if (!parts) return null;
  const digest = text(setup?.plan_digest || offer.plan?.digest);
  if (!digest) return null;
  const song = parts.song || text(offer.subject?.name) || text(offer.folder);
  const manager = parts.manager.replace(/^Your /, '');
  const hire = source?.hire || null;
  const agent = hire?.mode === 'pending' ? '' : text(hire?.agent_name);
  const role = text(hire?.role_label);
  const model = text(hire?.model);
  const status = text(setup?.status);
  const stopped = status === 'stopped' ? text(setup?.stop_reason) : '';
  return {
    offerID: offer.id,
    digest,
    song,
    manager,
    sections: planLines.handoffNames(source),
    lines,
    hire: agent
      ? {
          name: agent,
          detail: [role && role !== agent ? `${role} role` : '', model ? `uses ${model}` : '']
            .filter(Boolean)
            .join(' · ')
        }
      : null,
    brief: text(setup?.brief || offer.plan?.brief_default),
    asksBrief: Boolean(offer.plan?.brief_default || setup?.brief),
    running: status === 'running',
    // Stopped anywhere but the hand-off, the assistant's card has the answer.
    waitsElsewhere: Boolean(stopped && stopped !== 'handoff_ready'),
    stopCopy:
      STOP_COPY[stopped] ||
      (stopped && stopped !== 'handoff_ready' ? 'A step did not finish.' : ''),
    label: `Open ${song} & hire`
  };
}

// folderDigestOffer is the one offer a GET ?offer_id= read returns.
export function folderDigestOffer(body) {
  const offer = body?.folder_digest?.offer;
  return offer && typeof offer === 'object' ? offer : null;
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
    let suggestions = [];
    try {
      const response = await this.fetchImpl(
        `${DIGEST_ENDPOINT}/home-suggestions?home_id=${encodeURIComponent(this.workspaceId)}`,
        { headers: { Accept: 'application/json' } }
      );
      if (response.ok) {
        const body = await response.json();
        suggestions = Array.isArray(body?.suggestions) ? body.suggestions : [];
      }
    } catch (_) {
      // Advisory: without it the Home shows no suggestion.
    }
    this.render(suggestions.map(songSuggestionView).filter(Boolean));
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
      if (view.running) button.textContent = `${view.manager} is opening ${view.song}…`;
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
    this.messages.set(view.offerID, `${view.manager} is opening ${view.song}…`);
    await this.load();
    try {
      const body = { plan_digest: view.digest, open: true, request_id: requestId() };
      if (view.asksBrief) body.brief = typed;
      const response = await this.fetchImpl(
        `${DIGEST_ENDPOINT}/offers/${encodeURIComponent(view.offerID)}/setup`,
        {
          method: 'POST',
          headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
          body: JSON.stringify(body)
        }
      );
      const result = await response.json().catch(() => null);
      if (!response.ok) {
        this.messages.set(
          view.offerID,
          result?.plan_changed
            ? 'What opening does has changed. Review it again below.'
            : text(result?.error) || `${view.song} could not be opened.`
        );
        return;
      }
      await this.follow(view);
    } catch (_) {
      this.messages.set(
        view.offerID,
        'Ori could not be reached. Nothing was opened twice; try again.'
      );
    } finally {
      this.busy.delete(view.offerID);
      await this.load();
    }
  }

  // follow reads the offer until its run settles: opened, or stopped with
  // something to answer on the assistant's card.
  async follow(view) {
    for (let i = 0; i < POLL_LIMIT; i += 1) {
      await new Promise(resolve => setTimeout(resolve, POLL_MS));
      let offer = null;
      try {
        const response = await this.fetchImpl(
          `${DIGEST_ENDPOINT}?offer_id=${encodeURIComponent(view.offerID)}`,
          { headers: { Accept: 'application/json' } }
        );
        if (response.ok) offer = folderDigestOffer(await response.json());
      } catch (_) {
        continue;
      }
      if (!offer) continue;
      if (offer.status === 'resolved') {
        this.opened(view, offer.outcome);
        return;
      }
      if (offer.setup?.status === 'stopped') return;
    }
  }

  // opened replaces the card with the song it opened, until the page reloads.
  opened(view, outcome) {
    this.messages.delete(view.offerID);
    const route = text(outcome?.route);
    const note = element('section', 'assistant-program-suggestion is-opened');
    note.append(
      element('span', 'assistant-program-kicker', `${view.manager} opened it`),
      element('h2', '', `${view.song} is open`),
      element('p', '', 'It has its own workspace and assistant, and stays in this Home’s library.')
    );
    if (/^\/workspaces\/[A-Za-z0-9._~%-]+$/.test(route)) {
      const link = element('a', 'modern-btn modern-btn-primary', `Open ${view.song}`);
      link.href = route;
      note.append(link);
    }
    this.root?.after?.(note);
  }
}

function requestId() {
  return (
    globalThis.crypto?.randomUUID?.() || `home-${Date.now()}-${Math.random().toString(16).slice(2)}`
  );
}
