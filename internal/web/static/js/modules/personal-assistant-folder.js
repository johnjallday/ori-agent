// Show me a folder — the assistant's first proactive act.
//
// The user points the assistant at a folder; the server looks at its shape
// (names, dates, kinds, project markers — never contents) and returns one
// explained offer. This module owns the chooser sheet and the offer card in
// the Home assistant panel. It never sends a filesystem location — chips are
// identifiers the server resolves, and the native picker runs server-side.

const DIGEST_ENDPOINT = '/api/personal-assistant/folder-digest';

export const FOLDER_CHIP_ICON = '\u{1F4C1}';

// FOLDER_QUEST_ACTION_URL is where the "Show your assistant a folder" mission
// card sends the user (show-folder-quest.js opens the chooser from it). The
// Quests widget matches a mission on it to render a pending offer inline;
// it knows no quest IDs.
export const FOLDER_QUEST_ACTION_URL = '/?quest=show-folder';

// folderActionAvailable reports whether "Show me a folder" may be offered:
// only for a hired assistant with a built HQ, active or paused (FR1, FR52).
export function folderActionAvailable(personalAssistant) {
  const state = String(personalAssistant?.state || '').trim();
  return state === 'active' || state === 'paused';
}

// folderChooserView is the chooser's render decision: which chips to show,
// whether the native picker chip appears, and the note that replaces it.
export function firstFolderPromptView(digest, available) {
  return {
    expand: available === true && digest?.prompt_first_folder === true,
    line: "Now let's explore a folder you're working in."
  };
}

export function folderChooserView(digest) {
  const chips = Array.isArray(digest?.chips)
    ? digest.chips
        .filter(chip => chip && typeof chip.id === 'string' && chip.id.trim())
        .map(chip => ({ id: chip.id.trim(), label: String(chip.label || chip.id).trim() }))
    : [];
  const pickerVisible = digest?.picker_available === true;
  // The server explains what can be chosen; the fallbacks only cover a
  // payload without a note. A chooser with nothing to press must say so.
  let note = String(digest?.picker_note || '').trim();
  if (!note && !pickerVisible) {
    note = chips.length ? 'Pick a folder from the list for now.' : 'No folder can be chosen here.';
  }
  return {
    chips,
    pickerVisible,
    pickerLabel: 'Pick another folder…',
    note
  };
}

// A view of what the server has actually observed. A folder graphic moving
// towards the portrait is only a metaphor: no files are moved or uploaded.
// Never derive counts or a blueprint from a proposed workspace plan.
export function folderSceneView({
  chooserOpen = false,
  scanning = false,
  failed = false,
  scanName = '',
  offer = null
} = {}) {
  const offered = folderOfferView(offer).visible;
  if (!chooserOpen && !offered) return { visible: false, phase: 'choosing', label: '', finds: [] };
  if (scanning) {
    return {
      visible: true,
      phase: 'scanning',
      label: `Exploring ${scanName || 'your folder'}…`,
      finds: []
    };
  }
  if (failed)
    return { visible: true, phase: 'error', label: 'I could not explore that folder.', finds: [] };
  if (offered) {
    const finds = [];
    const count = n => (Number.isSafeInteger(n) && n > 0 ? n : 0);
    if (count(offer.projects_count)) finds.push(plural(offer.projects_count, 'project'));
    if (count(offer.loose_files)) finds.push(plural(offer.loose_files, 'loose file'));
    if (!finds.length && offer.verdict === 'project' && offer.subject?.marker) {
      finds.push(String(offer.subject.marker));
    }
    const folder = String(offer.folder || '').trim() || 'your folder';
    return {
      visible: true,
      phase: 'found',
      label:
        offer.status === 'resolved' ? `${folder} is ready.` : `Here's what I noticed in ${folder}.`,
      finds
    };
  }
  return { visible: true, phase: 'choosing', label: 'Pick a folder to explore.', finds: [] };
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

// projectConfirmView is the card for a project subject: what the scan found
// ("Thesis looks like a LaTeX manuscript."), the plan as a question ("Set up
// Thesis as a Writing project workspace?"), and Set up / Adjust…. Set up has
// the assistant create the workspace itself when the server offers that
// (create_available); Adjust… opens the Create Workspace modal pre-filled,
// which is also Set up's whole path when the server does not. A confirm
// reached from a mixed or ambiguous offer gets Back instead of Not this one
// and Later, which belong to the offer it came from.
function projectConfirmView(offer, base, subject, remember, { back = false } = {}) {
  const marker = String(offer.subject?.marker || '').trim();
  // The plan names its blueprint only when that blueprint will be used; a
  // missing one is explained by the note instead.
  const label = String(offer.blueprint || '').trim()
    ? String(offer.blueprint_label || '').trim()
    : '';
  const note = String(offer.blueprint_note || '').trim();
  const headline = marker
    ? segments([{ text: subject }, ` looks like a ${marker}.`])
    : segments([{ text: subject }, ' looks like a project.']);
  let question = label
    ? `Set up ${subject} as a ${label} workspace?`
    : `Set up a workspace for ${subject}?`;
  if (note) question += ` ${note}`;
  if (remember)
    question += ` I will also remember that ${subject} is a project you are working on.`;
  const createAvailable = offer.create_available === true;
  const actions = createAvailable
    ? [
        {
          id: 'setup',
          label: 'Set up',
          style: 'primary',
          decision: 'yes',
          choice: 'project',
          create: true
        },
        {
          id: 'adjust',
          label: 'Adjust…',
          style: 'outline',
          decision: 'yes',
          choice: 'project',
          modal: true
        }
      ]
    : [
        {
          id: 'yes',
          label: 'Set up workspace',
          style: 'primary',
          decision: 'yes',
          choice: 'project',
          modal: true
        }
      ];
  if (back) actions.push({ id: 'back', label: 'Back', style: 'link', back: true });
  else {
    actions.push({ id: 'no', label: 'Not this one', style: 'outline', decision: 'no' });
    actions.push({ id: 'later', label: 'Later', style: 'link', decision: 'later' });
  }
  return { ...base, headline, question, actions, confirming: back };
}

// folderOfferView is the whole render decision for one offer (FR22). Every
// string comes from the verdict and the server's counts and names; the
// reason line is always present. options.confirm ('project' or 'tidy') shows
// the plan card for a mixed or ambiguous offer instead of its question.
export function folderOfferView(offer, options = {}) {
  if (!offer || typeof offer !== 'object') return { visible: false };
  const verdict = String(offer.verdict || '').trim();
  const folder = String(offer.folder || '').trim() || 'that folder';
  const subject = String(offer.subject?.name || '').trim() || folder;
  const status = String(offer.status || '').trim();
  const reason = String(offer.reason || '').trim();
  const remember = offer.remember === true;
  const decided = status !== 'pending' && status !== 'closed';
  // Which plan is being confirmed on the card: 'project' or 'tidy', or ''.
  const requested =
    options?.confirm === true || options?.confirmProject === true
      ? 'project'
      : String(options?.confirm || '');
  const confirm = decided ? '' : requested;
  const base = {
    visible: true,
    verdict,
    status,
    reason,
    decided,
    confirming: false,
    needsPick: offer.needs_pick === true
  };
  const view = verdictView(offer, base, { verdict, folder, subject, remember, confirm });
  if (
    status === 'resolved' &&
    offer?.outcome?.kind === 'project' &&
    offer?.outcome?.receipt?.length
  ) {
    view.question = "Here's what I set up:";
    view.reason = '';
  }
  // A yes needs the folder's path, and a dialog-chosen folder is held in
  // memory only: after a server restart the card asks for the folder again
  // before offering anything a yes would need.
  if (view.visible && view.needsPick && !view.decided) return repickView(view, subject);
  return view;
}

// repickView keeps what the scan said and swaps the yes for "Pick it again".
function repickView(view, subject) {
  return {
    ...view,
    confirming: false,
    question: `Ori no longer has ${subject} open (the server was restarted). Pick the folder again to carry on.`,
    actions: [
      { id: 'repick', label: 'Pick it again', style: 'primary', repick: true },
      { id: 'no', label: 'Not this one', style: 'outline', decision: 'no' },
      { id: 'later', label: 'Later', style: 'link', decision: 'later' }
    ]
  };
}

// tidyConfirmView is the plan for a tidy: what Ori will set up and how it
// keeps its hands off the files. Set up runs it and then shows the setup as
// it happened (the assistant-led setup card's walkthrough); Adjust… opens the
// ordinary File Janitor creator, whose own wizard asks for the folder.
function tidyConfirmView(offer, base, folder) {
  return {
    ...base,
    confirming: true,
    headline: segments(['Set up File Janitor for ', { text: folder }, '?']),
    question:
      'Ori will create a File Janitor workspace with a File Curator, watch the folder while paused, scan it once and propose moves — nothing moves until you approve a batch.',
    actions: [
      {
        id: 'setup',
        label: 'Set up',
        style: 'primary',
        decision: 'yes',
        choice: 'tidy',
        walkthrough: true
      },
      { id: 'adjust', label: 'Adjust…', style: 'outline', manual: 'file-janitor' },
      { id: 'back', label: 'Back', style: 'link', back: true }
    ]
  };
}

function verdictView(offer, base, { verdict, folder, subject, remember, confirm }) {
  if (confirm === 'tidy' && ['dump', 'mixed', 'ambiguous'].includes(verdict)) {
    return tidyConfirmView(offer, base, folder);
  }
  switch (verdict) {
    case 'project':
      return projectConfirmView(offer, base, subject, remember);
    case 'dump':
      return {
        ...base,
        headline: segments([
          { text: folder },
          ` has ${plural(offer.loose_files, 'loose file')} of ${plural(offer.loose_kinds, 'kind')}.`
        ]),
        question: 'Want me to tidy it? I will propose moves and you approve each batch.',
        actions: [
          // The tidy's plan is confirmed on its own card first.
          { id: 'yes', label: 'Tidy it', style: 'primary', confirm: 'tidy' },
          { id: 'no', label: 'Not this one', style: 'outline', decision: 'no' },
          { id: 'later', label: 'Later', style: 'link', decision: 'later' }
        ]
      };
    case 'mixed':
      if (confirm === 'project')
        return projectConfirmView(offer, base, subject, remember, { back: true });
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
          // Each plan is confirmed on its own card first.
          { id: 'project', label: `Start with ${subject}`, style: 'primary', confirm: 'project' },
          { id: 'tidy', label: 'Tidy the loose files', style: 'outline', confirm: 'tidy' },
          { id: 'later', label: 'Later', style: 'link', decision: 'later' }
        ]
      };
    case 'ambiguous':
      if (confirm === 'project')
        return projectConfirmView(offer, base, subject, remember, { back: true });
      return {
        ...base,
        headline: segments(['I am not sure what ', { text: subject }, ' is.']),
        question: 'Is this a project you work in, or a folder to tidy?',
        actions: [
          { id: 'project', label: "It's a project", style: 'primary', confirm: 'project' },
          { id: 'tidy', label: 'Tidy it', style: 'outline', confirm: 'tidy' },
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

// A receipt is read only from the resolved server outcome; the workspace name
// and route are never reconstructed from the folder name or blueprint plan.
export function folderReceiptView(offer) {
  if (offer?.status !== 'resolved' || offer?.outcome?.kind !== 'project') return { visible: false };
  const rows = Array.isArray(offer?.outcome?.receipt) ? offer.outcome.receipt : [];
  if (!rows.length) return { visible: false }; // an older stored offer
  const workspace = rows.find(row => row.kind === 'workspace');
  const route = resolvedRouteFor(offer);
  return {
    visible: true,
    rows: rows.map(row => ({
      kind: String(row.kind || ''),
      name: String(row.name || ''),
      detail: String(row.detail || '')
    })),
    route,
    openLabel: `Open ${String(workspace?.name || offer?.subject?.name || 'workspace').trim()}`
  };
}

// folderOutcomeNote is the line shown under a decided offer.
export function folderOutcomeNote(offer) {
  const status = String(offer?.status || '').trim();
  const subject = String(offer?.subject?.name || '').trim() || 'that folder';
  switch (status) {
    case 'awaiting_outcome':
      return offer?.choice === 'tidy'
        ? `Setting up a tidy of ${subject}…`
        : `Setting up a workspace for ${subject}…`;
    case 'later':
      return 'I will ask again in a week.';
    case 'declined':
      return `I will not ask about ${subject} again.`;
    case 'resolved':
      if (offer?.outcome?.kind === 'tidy') {
        return (
          String(offer?.outcome?.note || '').trim() ||
          `File Janitor is set up for ${subject}; its first proposals are ready to review.`
        );
      }
      if (offer?.outcome?.blueprint && String(offer?.blueprint_label || '').trim()) {
        return `${subject} is set up as a ${String(offer.blueprint_label).trim()} workspace.`;
      }
      return `The workspace for ${subject} is ready.`;
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
  scanning: false,
  scanFailed: false,
  freshScan: false,
  scanName: '',
  chooserOpen: false,
  available: false,
  handOver: false,
  prompting: false,
  // The plan being confirmed on the card before anything is decided:
  // 'project', 'tidy', or ''.
  confirm: '',
  // What the assistant is doing right now, shown under the card while a
  // setup runs.
  progress: ''
};

// announceOffer tells the mission card (progression-widget.js) what the
// chooser shows, so the two render the same offer in the same state.
function announceOffer() {
  if (typeof document === 'undefined') return;
  document.dispatchEvent(
    new CustomEvent('personal-assistant:folder-offer', {
      detail: { offer: state.offer, confirm: state.confirm }
    })
  );
}

function elements() {
  const root = document.getElementById('personalAssistantFolder');
  if (!root) return null;
  return {
    root,
    show: document.getElementById('personalAssistantFolderShowBtn'),
    chooser: document.getElementById('personalAssistantFolderChooser'),
    scene: document.getElementById('personalAssistantFolderScene'),
    sceneAvatar: document.getElementById('personalAssistantFolderSceneAvatar'),
    sceneLabel: document.getElementById('personalAssistantFolderSceneLabel'),
    sceneFinds: document.getElementById('personalAssistantFolderSceneFinds'),
    chips: document.getElementById('personalAssistantFolderChips'),
    title: document.getElementById('personalAssistantFolderTitle'),
    note: document.getElementById('personalAssistantFolderNote'),
    status: document.getElementById('personalAssistantFolderStatus'),
    offer: document.getElementById('personalAssistantFolderOffer'),
    headline: document.getElementById('personalAssistantFolderOfferHeadline'),
    question: document.getElementById('personalAssistantFolderOfferQuestion'),
    reason: document.getElementById('personalAssistantFolderOfferReason'),
    why: document.querySelector('#personalAssistantFolderOffer .pa-folder__why'),
    receipt: document.getElementById('personalAssistantFolderReceipt'),
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
  if (els.title)
    els.title.textContent = state.handOver
      ? firstFolderPromptView(state.digest, true).line
      : 'Which folder should I explore?';
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

function renderScene() {
  const els = elements();
  if (!els?.scene) return;
  const view = folderSceneView({
    chooserOpen: state.chooserOpen,
    scanning: state.scanning,
    failed: state.scanFailed,
    scanName: state.scanName,
    offer: state.offer
  });
  els.scene.hidden = !view.visible;
  if (!view.visible) return;
  els.scene.dataset.phase = view.phase;
  els.scene.dataset.freshScan = String(state.freshScan);
  setText(els.sceneLabel, view.label, false);
  if (els.sceneFinds) {
    els.sceneFinds.replaceChildren();
    view.finds.forEach(find => {
      const badge = document.createElement('span');
      badge.textContent = find;
      els.sceneFinds.appendChild(badge);
    });
  }
  // Clone the already rendered identity, including custom portraits. No new
  // asset lookup, assistant name interpolation, or second appearance pipeline.
  const portrait = document.getElementById('personalAssistantPanelAvatar');
  if (els.sceneAvatar && portrait && els.sceneAvatar.innerHTML !== portrait.innerHTML) {
    els.sceneAvatar.replaceChildren(
      ...Array.from(portrait.childNodes, node => node.cloneNode(true))
    );
  }
}

function renderOffer() {
  const els = elements();
  if (!els?.offer) return;
  const view = folderOfferView(state.offer, { confirm: state.confirm });
  const receipt = folderReceiptView(state.offer);
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
  if (els.why) els.why.hidden = !view.reason;
  if (els.receipt) {
    els.receipt.replaceChildren();
    els.receipt.hidden = !receipt.visible;
    if (receipt.visible)
      receipt.rows.forEach(row => {
        const li = document.createElement('li');
        const labels = {
          workspace: ['Workspace', '◈'],
          folder: ['Folder', '▤'],
          blueprint: ['Blueprint', '✦'],
          agent: ['Agent', '●'],
          task: ['First task', '✓']
        };
        const [label, glyph] = labels[row.kind] || ['Set up', '•'];
        li.dataset.kind = labels[row.kind] ? row.kind : 'other';
        const icon = document.createElement('span');
        icon.className = 'pa-folder__receipt-icon';
        icon.setAttribute('aria-hidden', 'true');
        icon.textContent = glyph;
        li.append(
          icon,
          document.createTextNode(`${label} · ${row.name}${row.detail ? ` (${row.detail})` : ''}`)
        );
        els.receipt.append(li);
      });
  }
  if (els.actions) {
    els.actions.replaceChildren();
    els.actions.hidden = view.decided && !receipt.route;
    if (receipt.route) {
      const open = document.createElement('a');
      open.className = 'btn btn-sm btn-primary';
      open.href = receipt.route;
      open.textContent = receipt.openLabel;
      els.actions.append(open);
    } else if (!view.decided) {
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
        button.addEventListener('click', () => runAction(action));
        els.actions.appendChild(button);
      });
    }
  }
  if (els.offerNote) {
    const note =
      state.progress || (view.decided && !receipt.visible ? folderOutcomeNote(state.offer) : '');
    els.offerNote.replaceChildren();
    els.offerNote.hidden = !note;
    if (note) {
      els.offerNote.append(note);
      const route = String(state.offer?.outcome?.route || '').trim();
      if (view.status === 'resolved' && !receipt.visible && route.startsWith('/')) {
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
  renderScene();
}

// runAction carries out one of the offer's actions, wherever its button was
// pressed: the panel's card or the mission card (act below).
function runAction(action) {
  if (action.open) {
    openChooser();
    return;
  }
  if (action.repick) {
    // Straight to the dialog where there is one; the chips otherwise.
    if (state.digest?.picker_available === true) scan({ picker: true });
    else openChooser();
    return;
  }
  if (action.confirm || action.back) {
    state.confirm = action.back ? '' : String(action.confirm);
    render();
    announceOffer();
    return;
  }
  if (action.modal) {
    startProjectOutcome(action);
    return;
  }
  if (action.manual === 'file-janitor') {
    startManualTidy();
    return;
  }
  decide(action);
}

// startManualTidy is the tidy's Adjust…: the ordinary File Janitor creator,
// named after the folder, whose own setup wizard asks for the folder through
// its picker. The offer stays where it is; nothing is decided by looking.
function startManualTidy() {
  const offer = state.offer;
  const manager = typeof window !== 'undefined' ? window.sessionManager : null;
  if (!manager?.showAddWorkspaceModal) {
    if (typeof window !== 'undefined')
      window.location.assign('/workspaces/new?template=file-janitor');
    return;
  }
  showError('');
  manager.showAddWorkspaceModal({
    entryPoint: 'folder_digest',
    name: `File Janitor — ${String(offer?.folder || '').trim() || 'folder'}`,
    blueprint: 'file-janitor',
    blueprintNote: 'Choose the folder to tidy in the setup wizard after creating.'
  });
}

// act runs the current offer's action with this id, for the mission card that
// renders the same offer inline. Returns false when there is no such action
// to run: no offer, an offer already decided, or an unknown id.
function act(actionId) {
  const view = folderOfferView(state.offer, { confirm: state.confirm });
  if (!view.visible || view.decided) return false;
  const action = (view.actions || []).find(candidate => candidate.id === actionId);
  if (!action) return false;
  runAction(action);
  return true;
}

function openChooser() {
  state.chooserOpen = true;
  state.scanFailed = false;
  showError('');
  render();
  const els = elements();
  els?.chips?.querySelector('button')?.focus?.();
}

async function load() {
  state.scanFailed = false;
  state.freshScan = false;
  let revealFirstPrompt = false;
  try {
    const response = await fetch(DIGEST_ENDPOINT, { headers: { Accept: 'application/json' } });
    if (!response.ok) throw new Error(`folder digest ${response.status}`);
    const payload = await readJSON(response);
    state.digest = payload?.folder_digest || null;
    state.offer = state.digest?.offer || null;
    state.confirm = '';
    // A pending offer is the assistant's one question; it needs no chooser
    // in front of it.
    if (state.offer) state.chooserOpen = false;
    const prompt = firstFolderPromptView(state.digest, state.available);
    if (prompt.expand && !state.prompting) {
      revealFirstPrompt = true;
      state.handOver = true;
      state.chooserOpen = true;
      state.prompting = true;
      // The panel expands immediately; persisting the receipt cannot delay it.
      void fetch(`${DIGEST_ENDPOINT}/prompted`, { method: 'POST' })
        .then(response => {
          if (!response.ok) throw new Error('Could not save the folder prompt');
          state.digest.prompt_first_folder = false;
        })
        .catch(() => showStatus('Could not save the prompt. It may appear again after a reload.'))
        .finally(() => {
          state.prompting = false;
        });
    }
  } catch (_) {
    state.digest = state.digest || { chips: [], picker_available: false };
  }
  render();
  // The brief and HQ receipt precede Needs you and can put the first prompt
  // below the fold. Reveal it once inside an already-open drawer, without
  // scrolling Home when the relationship changes in a closed panel.
  if (revealFirstPrompt && !document.getElementById('personalAssistantPanel')?.hidden) {
    elements()?.scene?.scrollIntoView?.({ block: 'center', behavior: 'instant' });
  }
  announceOffer();
}

async function scan(body) {
  if (state.busy) return;
  state.busy = true;
  state.scanning = !body.picker;
  state.scanFailed = false;
  state.freshScan = false;
  state.scanName = body.picker
    ? ''
    : folderChooserView(state.digest).chips.find(chip => chip.id === body.chip)?.label ||
      'your folder';
  const els = elements();
  if (els?.why) els.why.open = false;
  showError('');
  showStatus(body.picker ? 'Choose a folder in the dialog…' : '');
  render();
  try {
    const response = await fetch(`${DIGEST_ENDPOINT}/scan`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
      body: JSON.stringify(body)
    });
    const payload = await readJSON(response);
    if (!response.ok) {
      state.scanFailed = true;
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
    state.freshScan = Boolean(state.offer);
    state.confirm = '';
    state.chooserOpen = false;
    showStatus('');
    announceOffer();
  } catch (_) {
    state.scanFailed = true;
    showStatus('That folder could not be looked at right now.');
  } finally {
    state.scanning = false;
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
    state.confirm = '';
    announceOffer();
  }
  return { ok: response.ok, payload };
}

function showOfferFailure(payload) {
  if (payload?.needs_pick) {
    // Said on the card that was pressed as well as in the chooser, and the
    // card itself now offers Pick it again.
    const message = String(payload?.error || 'Pick the folder again.');
    showError(message);
    if (state.offer) state.offer = { ...state.offer, needs_pick: true };
    state.chooserOpen = true;
    showStatus(message);
    return;
  }
  showError(String(payload?.error || 'That could not be saved. Try again.'));
}

async function decide(action) {
  const offer = state.offer;
  if (!offer?.id || state.busy) return;
  state.busy = true;
  showError('');
  // A setup takes a few seconds (a workspace, a grant, a scan); the card
  // says so rather than sitting there disabled.
  const folder = String(offer.folder || '').trim() || 'the folder';
  if (action.walkthrough) state.progress = `Setting up File Janitor for ${folder}…`;
  else if (action.create) state.progress = `Setting up the workspace for ${folder}…`;
  render();
  try {
    const body = { decision: action.decision, choice: action.choice || '' };
    // The card's confirmed plan: the assistant sets the workspace up itself.
    if (action.create === true) body.create = true;
    const { ok, payload } = await postOffer(offer.id, 'decide', body);
    if (!ok) {
      showOfferFailure(payload);
      return;
    }
    // A tidy, or a workspace the assistant set up, resolves in the same
    // request. A tidy from the confirmed plan is shown as it happened (the
    // setup card's walkthrough, ending on its review); anything else opens
    // its route: the first review batch, the workspace that already manages
    // the folder, or the new workspace.
    const route = resolvedRouteFor(state.offer);
    // The server-authored receipt is the confirmation of what Set up actually
    // made. Stay on it until the user presses Open <workspace>.
    if (action.create === true && folderReceiptView(state.offer).visible) return;
    if (action.walkthrough && (await revealSetupWalkthrough(state.offer))) return;
    if (route && typeof window !== 'undefined') window.location.assign(route);
  } catch (_) {
    showError('That could not be saved. Try again.');
  } finally {
    state.progress = '';
    state.busy = false;
    render();
  }
}

// revealSetupWalkthrough shows a fresh tidy as the setup it was: the
// assistant-led setup card (in the Today panel) with its receipts — the
// workspace, the File Curator, the folder, the paused watch, the first scan —
// and its walkthrough open, ending on the review. Returns false when there is
// no fresh run to show (the folder was already managed, or the card is not on
// this page), so the caller opens the route instead.
async function revealSetupWalkthrough(offer) {
  if (typeof window === 'undefined') return false;
  if (String(offer?.status || '') !== 'resolved' || offer?.outcome?.kind !== 'tidy') return false;
  if (offer?.outcome?.existing === true) return false;
  const setup = window.AssistantLedSetup;
  if (!setup || typeof setup.load !== 'function') return false;
  let projection = null;
  try {
    projection = await setup.load('');
  } catch (_) {
    return false;
  }
  if (!projection?.run) return false;
  const panel = window.PersonalAssistantPanel;
  if (panel && typeof panel.open === 'function') {
    panel.open(document.getElementById('personalAssistantLauncher'), {
      view: 'today',
      focusTab: false
    });
  }
  const card = document.getElementById('assistantLedSetup');
  card?.scrollIntoView?.({ block: 'start', behavior: 'smooth' });
  const milestones = projection.milestones || [];
  if (milestones.length && setup.presentation && typeof setup.presentation.open === 'function') {
    setup.presentation.open(milestones, { invoker: setup.els?.replay || null });
  }
  return true;
}

// resolvedRouteFor returns the page a resolved outcome should open — a tidy's
// first review batch or a set-up workspace — or '' when the offer is not
// resolved or the route is not a local path.
export function resolvedRouteFor(offer) {
  if (String(offer?.status || '') !== 'resolved') return '';
  const kind = offer?.outcome?.kind;
  if (kind !== 'tidy' && kind !== 'project') return '';
  const route = String(offer?.outcome?.route || '').trim();
  return route.startsWith('/') && !route.startsWith('//') ? route : '';
}

// tidyRouteFor is resolvedRouteFor for a tidy only.
export function tidyRouteFor(offer) {
  return offer?.outcome?.kind === 'tidy' ? resolvedRouteFor(offer) : '';
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
    state.handOver = false;
    state.chooserOpen = false;
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
    openChooser,
    reload: load,
    current: () => state.offer,
    act
  };
}

if (typeof document !== 'undefined') {
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', init);
  else init();
}
