// Show me a folder — the assistant's first proactive act.
//
// The user points the assistant at a folder; the server looks at its shape
// (names, dates, kinds, project markers — never contents) and returns one
// explained offer. This module owns the chooser sheet and the offer card in
// the Home assistant panel. It never sends a filesystem location — chips are
// identifiers the server resolves, and the native picker runs server-side.

const DIGEST_ENDPOINT = '/api/personal-assistant/folder-digest';

export const FOLDER_CHIP_ICON = '\u{1F4C1}';

// folderActionAvailable reports whether "Show me a folder" may be offered:
// only for a hired assistant with a built HQ, active or paused (FR1, FR52).
export function folderActionAvailable(personalAssistant) {
  const state = String(personalAssistant?.state || '').trim();
  return state === 'active' || state === 'paused';
}

// folderChooserView is the chooser's render decision: which chips to show,
// whether the native picker chip appears, and the note that replaces it.
export function folderChooserView(digest) {
  const chips = Array.isArray(digest?.chips)
    ? digest.chips
        .filter(chip => chip && typeof chip.id === 'string' && chip.id.trim())
        .map(chip => ({ id: chip.id.trim(), label: String(chip.label || chip.id).trim() }))
    : [];
  const pickerVisible = digest?.picker_available === true;
  return {
    chips,
    pickerVisible,
    pickerLabel: 'Pick another folder…',
    note: pickerVisible ? '' : String(digest?.picker_note || 'Pick a folder from the list for now.')
  };
}

function segments(parts) {
  return parts.map(part =>
    typeof part === 'string'
      ? { text: part, strong: false }
      : { text: String(part.text), strong: true }
  );
}

function plural(n, noun) {
  const count = Number(n) || 0;
  return `${count} ${noun}${count === 1 ? '' : 's'}`;
}

// folderOfferView is the whole render decision for one offer (FR22). Every
// string comes from the verdict and the server's counts and names; the
// reason line is always present.
export function folderOfferView(offer) {
  if (!offer || typeof offer !== 'object') return { visible: false };
  const verdict = String(offer.verdict || '').trim();
  const folder = String(offer.folder || '').trim() || 'that folder';
  const subject = String(offer.subject?.name || '').trim() || folder;
  const status = String(offer.status || '').trim();
  const reason = String(offer.reason || '').trim();
  const remember = offer.remember === true;
  const decided = status !== 'pending' && status !== 'closed';
  const base = {
    visible: true,
    verdict,
    status,
    reason,
    decided,
    needsPick: offer.needs_pick === true
  };

  switch (verdict) {
    case 'project':
      return {
        ...base,
        headline: segments(['You have been working in ', { text: subject }, '.']),
        question:
          'Want me to set up a workspace for it?' +
          (remember
            ? ` I will also remember that ${subject} is a project you are working on.`
            : ''),
        actions: [
          {
            id: 'yes',
            label: 'Set up workspace',
            style: 'primary',
            decision: 'yes',
            choice: 'project'
          },
          { id: 'no', label: 'Not this one', style: 'outline', decision: 'no' },
          { id: 'later', label: 'Later', style: 'link', decision: 'later' }
        ]
      };
    case 'dump':
      return {
        ...base,
        headline: segments([
          { text: folder },
          ` has ${plural(offer.loose_files, 'loose file')} of ${plural(offer.loose_kinds, 'kind')}.`
        ]),
        question: 'Want me to tidy it? I will propose moves and you approve each batch.',
        actions: [
          { id: 'yes', label: 'Tidy it', style: 'primary', decision: 'yes', choice: 'tidy' },
          { id: 'no', label: 'Not this one', style: 'outline', decision: 'no' },
          { id: 'later', label: 'Later', style: 'link', decision: 'later' }
        ]
      };
    case 'mixed':
      return {
        ...base,
        headline: segments([
          'I see ',
          { text: plural(offer.projects_count, 'project') },
          ' and ',
          { text: plural(offer.loose_files, 'loose file') },
          ` in ${folder}.`
        ]),
        question: 'Start with a project, or a tidy?',
        actions: [
          {
            id: 'project',
            label: `Start with ${subject}`,
            style: 'primary',
            decision: 'yes',
            choice: 'project'
          },
          {
            id: 'tidy',
            label: 'Tidy the loose files',
            style: 'outline',
            decision: 'yes',
            choice: 'tidy'
          },
          { id: 'later', label: 'Later', style: 'link', decision: 'later' }
        ]
      };
    case 'ambiguous':
      return {
        ...base,
        headline: segments(['I am not sure what ', { text: subject }, ' is.']),
        question: 'Is this a project you work in, or a folder to tidy?',
        actions: [
          {
            id: 'project',
            label: "It's a project",
            style: 'primary',
            decision: 'yes',
            choice: 'project'
          },
          { id: 'tidy', label: 'Tidy it', style: 'outline', decision: 'yes', choice: 'tidy' },
          { id: 'no', label: 'Neither', style: 'link', decision: 'no' }
        ]
      };
    case 'empty':
      return {
        ...base,
        headline: segments(['Nothing in ', { text: folder }, ' needs me yet.']),
        question: 'Try Downloads, or pick another folder.',
        actions: [{ id: 'another', label: 'Show another folder', style: 'outline', open: true }]
      };
    case 'declined':
      return {
        ...base,
        headline: segments(['You asked me not to ask about ', { text: folder }, '.']),
        question: 'Pick another folder whenever you like.',
        actions: [{ id: 'another', label: 'Show another folder', style: 'outline', open: true }]
      };
    default:
      return { visible: false };
  }
}

// folderOutcomeNote is the line shown under a decided offer.
export function folderOutcomeNote(offer) {
  const status = String(offer?.status || '').trim();
  const subject = String(offer?.subject?.name || '').trim() || 'that folder';
  switch (status) {
    case 'awaiting_outcome':
      return offer?.choice === 'tidy'
        ? `Setting up a tidy of ${subject}… (outcome wired in group 5)`
        : `Setting up a workspace for ${subject}…`;
    case 'later':
      return 'I will ask again in a week.';
    case 'declined':
      return `I will not ask about ${subject} again.`;
    case 'resolved':
      return offer?.outcome?.kind === 'tidy'
        ? `${subject} is being tidied.`
        : `The workspace for ${subject} is ready.`;
    default:
      return '';
  }
}

// folderProjectModalOptions is what a project yes hands the Create Workspace
// modal (FR28): the folder's name, its preferred blueprint (empty means the
// blank workspace), the fallback note, and the offer identifier the server
// needs to attach the folder afterwards. The folder's path is not here.
export function folderProjectModalOptions(offer) {
  return {
    entryPoint: 'folder_digest',
    name: String(offer?.subject?.name || '').trim(),
    blueprint: String(offer?.blueprint || '').trim(),
    blueprintNote: String(offer?.blueprint_note || '').trim(),
    folderOfferId: String(offer?.id || '').trim()
  };
}

const state = {
  digest: null,
  offer: null,
  busy: false,
  chooserOpen: false,
  available: false
};

function elements() {
  const root = document.getElementById('personalAssistantFolder');
  if (!root) return null;
  return {
    root,
    show: document.getElementById('personalAssistantFolderShowBtn'),
    chooser: document.getElementById('personalAssistantFolderChooser'),
    chips: document.getElementById('personalAssistantFolderChips'),
    note: document.getElementById('personalAssistantFolderNote'),
    status: document.getElementById('personalAssistantFolderStatus'),
    offer: document.getElementById('personalAssistantFolderOffer'),
    headline: document.getElementById('personalAssistantFolderOfferHeadline'),
    question: document.getElementById('personalAssistantFolderOfferQuestion'),
    reason: document.getElementById('personalAssistantFolderOfferReason'),
    actions: document.getElementById('personalAssistantFolderOfferActions'),
    offerNote: document.getElementById('personalAssistantFolderOfferNote'),
    error: document.getElementById('personalAssistantFolderOfferError')
  };
}

function setText(el, text, hideWhenEmpty = true) {
  if (!el) return;
  el.textContent = text || '';
  if (hideWhenEmpty) el.hidden = !text;
}

function showStatus(message) {
  const els = elements();
  setText(els?.status, message);
}

function showError(message) {
  const els = elements();
  setText(els?.error, message);
}

function requestId() {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function')
    return crypto.randomUUID();
  return `req-${Date.now()}-${Math.random().toString(16).slice(2)}`;
}

async function readJSON(response) {
  try {
    return await response.json();
  } catch (_) {
    return null;
  }
}

function renderChooser() {
  const els = elements();
  if (!els?.chooser) return;
  const view = folderChooserView(state.digest);
  els.chooser.hidden = !state.chooserOpen;
  if (els.show) els.show.setAttribute('aria-expanded', String(state.chooserOpen));
  if (els.chips) {
    els.chips.replaceChildren();
    view.chips.forEach(chip => {
      const button = document.createElement('button');
      button.type = 'button';
      button.className = 'pa-folder__chip';
      button.dataset.chip = chip.id;
      button.textContent = chip.label;
      button.disabled = state.busy;
      button.addEventListener('click', () => scan({ chip: chip.id }));
      els.chips.appendChild(button);
    });
    if (view.pickerVisible) {
      const button = document.createElement('button');
      button.type = 'button';
      button.className = 'pa-folder__chip';
      button.dataset.picker = 'true';
      button.textContent = `${FOLDER_CHIP_ICON} ${view.pickerLabel}`;
      button.disabled = state.busy;
      button.addEventListener('click', () => scan({ picker: true }));
      els.chips.appendChild(button);
    }
  }
  setText(els.note, view.note);
}

function renderOffer() {
  const els = elements();
  if (!els?.offer) return;
  const view = folderOfferView(state.offer);
  els.offer.hidden = !view.visible;
  els.root.dataset.state = view.visible
    ? `offer-${view.verdict}`
    : state.chooserOpen
      ? 'chooser'
      : 'idle';
  if (!view.visible) return;
  els.offer.dataset.verdict = view.verdict;
  els.offer.dataset.status = view.status;
  if (els.headline) {
    els.headline.replaceChildren();
    view.headline.forEach(part => {
      if (part.strong) {
        const strong = document.createElement('strong');
        strong.textContent = part.text;
        els.headline.appendChild(strong);
      } else {
        els.headline.append(part.text);
      }
    });
  }
  setText(els.question, view.question, false);
  setText(els.reason, view.reason, false);
  if (els.actions) {
    els.actions.replaceChildren();
    els.actions.hidden = view.decided;
    if (!view.decided) {
      view.actions.forEach(action => {
        const button = document.createElement('button');
        button.type = 'button';
        button.className =
          action.style === 'primary'
            ? 'btn btn-sm btn-primary'
            : action.style === 'outline'
              ? 'btn btn-sm btn-outline-secondary'
              : 'btn btn-sm btn-link';
        button.dataset.folderAction = action.id;
        button.textContent = action.label;
        button.disabled = state.busy;
        button.addEventListener('click', () => {
          if (action.open) {
            openChooser();
            return;
          }
          if (action.decision === 'yes' && action.choice === 'project') {
            startProjectOutcome(action);
            return;
          }
          decide(action);
        });
        els.actions.appendChild(button);
      });
    }
  }
  if (els.offerNote) {
    const note = view.decided ? folderOutcomeNote(state.offer) : '';
    els.offerNote.replaceChildren();
    els.offerNote.hidden = !note;
    if (note) {
      els.offerNote.append(note);
      const route = String(state.offer?.outcome?.route || '').trim();
      if (view.status === 'resolved' && route.startsWith('/')) {
        const link = document.createElement('a');
        link.href = route;
        link.textContent = 'Open it';
        els.offerNote.append(' ', link);
      }
    }
  }
}

function render() {
  const els = elements();
  if (!els) return;
  els.root.hidden = !state.available;
  if (!state.available) return;
  renderChooser();
  renderOffer();
}

function openChooser() {
  state.chooserOpen = true;
  showError('');
  render();
  const els = elements();
  els?.chips?.querySelector('button')?.focus?.();
}

async function load() {
  try {
    const response = await fetch(DIGEST_ENDPOINT, { headers: { Accept: 'application/json' } });
    if (!response.ok) throw new Error(`folder digest ${response.status}`);
    const payload = await readJSON(response);
    state.digest = payload?.folder_digest || null;
    state.offer = state.digest?.offer || null;
    // A pending offer is the assistant's one question; it needs no chooser
    // in front of it.
    if (state.offer) state.chooserOpen = false;
  } catch (_) {
    state.digest = state.digest || { chips: [], picker_available: false };
  }
  render();
  document.dispatchEvent(
    new CustomEvent('personal-assistant:folder-offer', { detail: { offer: state.offer } })
  );
}

async function scan(body) {
  if (state.busy) return;
  state.busy = true;
  showError('');
  showStatus(body.picker ? 'Choose a folder in the dialog…' : 'Looking at the folder…');
  render();
  try {
    const response = await fetch(`${DIGEST_ENDPOINT}/scan`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
      body: JSON.stringify(body)
    });
    const payload = await readJSON(response);
    if (!response.ok) {
      showStatus(
        String(payload?.error || 'That folder could not be looked at. Choose a different folder.')
      );
      return;
    }
    if (payload?.cancelled) {
      showStatus('');
      return;
    }
    state.offer = payload?.offer || null;
    state.chooserOpen = false;
    showStatus('');
    document.dispatchEvent(
      new CustomEvent('personal-assistant:folder-offer', { detail: { offer: state.offer } })
    );
  } catch (_) {
    showStatus('That folder could not be looked at right now.');
  } finally {
    state.busy = false;
    render();
  }
}

// postOffer sends one offer action (decide or resolve) and returns the
// response, updating the shown offer on success.
async function postOffer(offerId, action, body) {
  const response = await fetch(
    `${DIGEST_ENDPOINT}/offers/${encodeURIComponent(offerId)}/${action}`,
    {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
      body: JSON.stringify({ ...body, request_id: requestId() })
    }
  );
  const payload = await readJSON(response);
  if (response.ok && payload?.offer) {
    state.offer = payload.offer;
    document.dispatchEvent(
      new CustomEvent('personal-assistant:folder-offer', { detail: { offer: state.offer } })
    );
  }
  return { ok: response.ok, payload };
}

function showOfferFailure(payload) {
  if (payload?.needs_pick) {
    showError('');
    state.chooserOpen = true;
    showStatus(String(payload?.error || 'Pick the folder again.'));
    return;
  }
  showError(String(payload?.error || 'That could not be saved. Try again.'));
}

async function decide(action) {
  const offer = state.offer;
  if (!offer?.id || state.busy) return;
  state.busy = true;
  showError('');
  render();
  try {
    const { ok, payload } = await postOffer(offer.id, 'decide', {
      decision: action.decision,
      choice: action.choice || ''
    });
    if (!ok) showOfferFailure(payload);
  } catch (_) {
    showError('That could not be saved. Try again.');
  } finally {
    state.busy = false;
    render();
  }
}

// startProjectOutcome runs a project yes (FR28, FR29): the Create Workspace
// modal opens pre-filled; only once it reports a created workspace is the yes
// recorded and the folder attached. Closing the modal without creating
// leaves the offer exactly where it was.
function startProjectOutcome(action) {
  const offer = state.offer;
  if (!offer?.id || state.busy) return;
  if (offer.needs_pick) {
    showOfferFailure({
      needs_pick: true,
      error: 'Ori no longer has that folder open. Pick it again.'
    });
    render();
    return;
  }
  const manager = typeof window !== 'undefined' ? window.sessionManager : null;
  if (!manager?.showAddWorkspaceModal) {
    showError('The workspace creator is not available on this page.');
    render();
    return;
  }
  showError('');
  manager.showAddWorkspaceModal({
    ...folderProjectModalOptions(offer),
    onCreated: async ({ workspaceId } = {}) => {
      const id = String(workspaceId || '').trim();
      if (!id) return;
      state.busy = true;
      render();
      try {
        const decided = await postOffer(offer.id, 'decide', {
          decision: action.decision,
          choice: action.choice || ''
        });
        if (!decided.ok) {
          showOfferFailure(decided.payload);
          return;
        }
        const resolved = await postOffer(offer.id, 'resolve', { workspace_id: id });
        if (!resolved.ok) showOfferFailure(resolved.payload);
      } catch (_) {
        showError('The workspace was created, but the folder could not be attached. Try again.');
      } finally {
        state.busy = false;
        render();
      }
    }
  });
}

function onStatus(personalAssistant) {
  const available = folderActionAvailable(personalAssistant);
  const changed = available !== state.available;
  state.available = available;
  if (!available) {
    render();
    return;
  }
  if (changed || !state.digest) void load();
  else render();
}

function init() {
  const els = elements();
  if (!els) return;
  els.show?.addEventListener('click', () => {
    state.chooserOpen = !state.chooserOpen;
    if (state.chooserOpen) openChooser();
    else render();
  });
  document.addEventListener('personal-assistant:status', event => {
    onStatus(event.detail?.personalAssistant);
  });
  const panelState = window.PersonalAssistantPanel?._state;
  if (panelState?.personalAssistant) onStatus(panelState.personalAssistant);
  window.PersonalAssistantFolder = {
    open: openChooser,
    reload: load,
    current: () => state.offer
  };
}

if (typeof document !== 'undefined') {
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', init);
  else init();
}
