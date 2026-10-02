import { clearLibraryReturn, readLibraryReturn, writeLibraryReturn } from './library-return.js';
import {
  lastSavedLabel,
  libraryOpenAction,
  libraryOpenChoices,
  libraryOpenRoute,
  libraryOpenedMessage,
  libraryRequestError,
  readLibraryPayload as payload
} from './library-open.js';
import { setupQuestURL } from './setup-quest-links.js';

// The open helpers moved to library-open.js so the setup pop-up can share them
// without loading this module on every page; existing importers keep working.
export {
  lastSavedLabel,
  libraryOpenAction,
  libraryOpenChoices,
  libraryOpenRoute,
  libraryOpenedMessage
} from './library-open.js';

function label(value) {
  return String(value == null || value === '' ? 'Unknown' : value).replaceAll('_', ' ');
}

function operationKey(action) {
  return `library-${action}-${globalThis.crypto.randomUUID()}`;
}

function node(tag, className, value) {
  const element = document.createElement(tag);
  if (className) element.className = className;
  if (value != null) element.textContent = String(value);
  return element;
}

// What to tell the user when the folder they chose for this Home cannot be used
// to continue, and whether choosing it again could help. `reason` is the
// library's own answer (picker/Home/package unavailable); `continuation` is the
// server's account of the carried collection (expired, lost after a restart, or
// changed). Every message says the Home is ready and that nothing was connected,
// because no committed result is ever lost by a lost selection, and none of these
// is a reason to distrust a completed grant after an ordinary reload.
export function selectionRecovery({ reason = '', continuation = null } = {}) {
  switch (reason) {
    case 'picker_unavailable':
      return {
        repick: false,
        message:
          'The native folder picker is unavailable on this computer, so a folder cannot be chosen here. Your Home is unchanged. Use Ori desktop to add a folder.'
      };
    case 'provider_unavailable':
      return {
        repick: false,
        message:
          "This Home's package is unavailable, so its library is read-only. Choosing a folder again will not help; re-enable or reinstall the package, then reopen this Home. Nothing was changed."
      };
    case 'home_unavailable':
      return {
        repick: false,
        message: 'That Home could not be found, so nothing was connected. Reopen it from Home.'
      };
    default:
  }
  const kept = 'Your Home is ready and anything already connected is kept.';
  switch (continuation?.reason) {
    case 'expired':
      return {
        repick: true,
        message: `The folder you chose was held for 30 minutes and that time has passed. ${kept} Choose the folder again to continue.`
      };
    case 'lost':
      return {
        repick: true,
        message: `Ori was restarted, which clears a folder choice that has not been connected yet. ${kept} Choose the folder again to continue.`
      };
    case 'changed':
      return {
        repick: true,
        message: `The folder at that location changed after you chose it (it was moved, replaced, or is gone). ${kept} Choose the folder again to continue.`
      };
    default:
      return {
        repick: true,
        message: `Ori no longer has the folder you chose earlier. ${kept} Choose the folder again to continue.`
      };
  }
}

// A short, honest label for where one saved song stands, from the library's own
// eligibility state. It describes the last scan and the integrations installed
// now, never a live check of a project application, and it never promises an
// install: a format no reviewed integration supports is simply a catalog record
// (notes and session planning still work).
const ACTIVATION_STATE_LABELS = {
  review_available: 'Ready to review',
  file_choice_required: 'Needs a file choice',
  connected: 'Connected to a project workspace',
  link_needs_review: 'Saved link needs review',
  revoked_source: 'Discovery consent ended',
  unavailable: 'Not found at the last scan',
  unsupported_format: 'Catalog record only',
  project_provider_unavailable: 'Needs a project integration',
  home_provider_unavailable: 'Home package unavailable',
  provider_ambiguous: 'Integration needs review',
  folder_owned: 'Folder already used by another workspace'
};

export function activationStateLabel(state) {
  // Own properties only: a state like "__proto__" or "toString" must not resolve
  // to something inherited from Object.prototype.
  return typeof state === 'string' && Object.hasOwn(ACTIVATION_STATE_LABELS, state)
    ? ACTIVATION_STATE_LABELS[state]
    : 'Setup status unknown';
}

// Where "Set up the project team" goes for a connected project: its own page,
// asking for the setup form of the first empty role the project's roster offers
// (primary, then required, then any). The roster is the project's own canonical
// read, so a role that is filled, read-only, or unknown is never requested; with
// nothing to fill (or no roster) it is just the project page. The role only
// selects which form opens: the page's own reviewed staffing does the rest.
export function projectTeamURL(route, rosterResponse) {
  const base = String(route || '');
  if (!/^\/workspaces\/[^/?#]+$/.test(base)) return '';
  const rows = Array.isArray(rosterResponse?.roles?.roles) ? rosterResponse.roles.roles : [];
  const empty = rows.filter(
    row =>
      row &&
      row.state === 'empty' &&
      !row.read_only &&
      typeof row.role_id === 'string' &&
      /^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/.test(row.role_id)
  );
  const pick = empty.find(row => row.primary) || empty.find(row => row.required) || empty[0];
  return pick ? `${base}?role=${encodeURIComponent(pick.role_id)}` : base;
}

const folderName = path =>
  String(path || '')
    .split(/[\\/]/)
    .filter(Boolean)
    .pop() || 'this folder';

// The one next step for an unfinished Home library, read from the library's own
// state and never inferred from files. It only names what to review next; every
// action still opens that step's own review and confirmation. An established
// Home (a connected folder with a finished scan) returns stage "ready" and shows
// nothing, so the shelf stays the place to browse and search.
//   collection: display name of the collection carried from intake, if known.
export function setupNextStep({ state = null, collection = '', carried = false } = {}) {
  if (!state) return { stage: 'unknown' };
  if (state.provider_read_only !== false) {
    return {
      stage: 'read_only',
      title: 'This Home’s package is unavailable',
      body: 'The library is read-only until the Home’s package is available again. Nothing was changed, and your saved notes and links are kept.',
      action: null
    };
  }
  const named = collection || '';
  if (state.initialized !== true && carried && named) {
    // The person already chose this collection, so starting the library is part of
    // setting it up. The button says so; the exact folder is then shown for its own
    // review, and a scan is reviewed after that.
    return {
      stage: 'not_initialized',
      title: `Set up the library for ${named}`,
      body: `${named} is waiting. This starts the Home’s library (it copies saved notes and exact project links), then shows you the exact folder to review. Nothing is read or scanned until you confirm the folder, and then the scan.`,
      action: { id: 'initialize', label: `Start library and review ${named}` }
    };
  }
  if (state.initialized !== true) {
    return {
      stage: 'not_initialized',
      title: named ? `Set up the library for ${named}` : 'Start this Home’s project library',
      body: `${named ? `${named} is waiting. ` : ''}Starting the library copies this Home’s saved notes and exact project links into it. It opens no folders and scans nothing; you review each next step separately.`,
      action: { id: 'initialize', label: 'Review library setup' }
    };
  }
  const roots = Array.isArray(state.roots) ? state.roots : [];
  const active = roots.filter(root => !root.revoked_at && !root.needs_review);
  if (!active.length) {
    const canPick = carried || state.picker_available !== false;
    return {
      stage: 'no_root',
      title: named ? `Connect ${named} to this Home` : 'Connect a folder to discover projects',
      body: canPick
        ? 'Connecting lets Ori read folder names and project markers only. Nothing is scanned until you review a scan.'
        : 'The native folder picker is unavailable here. Use Ori desktop to add a folder; your saved projects stay usable meanwhile.',
      action: canPick ? { id: 'add_folder', label: 'Review folder connection' } : null
    };
  }
  const unscanned = active.find(root => !root.last_scan);
  if (unscanned) {
    return {
      stage: 'not_scanned',
      title: `Scan ${folderName(unscanned.path)} once`,
      body: 'The folder is connected but has not been scanned. A scan reads folder names and project markers only, never project files.',
      action: { id: 'scan', label: 'Review scan', rootId: unscanned.id }
    };
  }
  const finished = active.some(
    root => root.last_scan.status === 'complete' || root.last_scan.status === 'partial'
  );
  if (!finished) {
    const failed = active[0];
    return {
      stage: 'scan_incomplete',
      title: 'The last scan did not finish',
      body: 'Anything already found is kept. Review a fresh scan when you are ready; nothing was changed.',
      action: { id: 'scan', label: 'Review scan again', rootId: failed.id }
    };
  }
  return { stage: 'ready' };
}

export function libraryQuery({
  text = '',
  stage = '',
  status = '',
  format = '',
  rootID = '',
  connection = '',
  availability = '',
  priority = '',
  sort = 'name',
  direction = 'asc',
  cursor = ''
} = {}) {
  const query = new URLSearchParams({ page_size: '25', sort, direction });
  for (const [key, value] of Object.entries({
    text: text.trim(),
    stage,
    status,
    format,
    root_id: rootID,
    connection,
    availability,
    priority,
    cursor
  })) {
    if (value) query.set(key, value);
  }
  return query.toString();
}

const DIGEST_SETUP_NOTES = {
  setup_check_unavailable: 'project setup was not checked',
  home_provider_unavailable: 'project setup needs the Home’s installed package',
  project_provider_unavailable: 'project setup needs a compatible installed integration',
  provider_ambiguous: 'more than one installed integration needs review before setup'
};

// libraryDigestText renders the Home's model-free scan digest. The digest
// carries only IDs and counts; the folder name comes from the owner-only roots
// list the shelf already loaded, reduced to its last path segment.
export function libraryDigestText(digest, roots = []) {
  if (!digest || typeof digest !== 'object' || !digest.scan_id) return '';
  const count = value => (Number.isInteger(value) && value >= 0 ? value : 0);
  const root = (Array.isArray(roots) ? roots : []).find(item => item?.id === digest.root_id);
  const name =
    String(root?.path || '')
      .split(/[\\/]/)
      .filter(Boolean)
      .pop() || 'an approved folder';
  const scanned = new Date(digest.scanned_at);
  const when = Number.isNaN(scanned.getTime())
    ? 'an unknown date'
    : scanned.toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric' });
  const projects = count(digest.projects);
  const parts = [`${projects} ${projects === 1 ? 'project' : 'projects'}`];
  if (count(digest.new) > 0) parts.push(`${count(digest.new)} new`);
  // Each song is in one bucket: already connected, can be set up, or an
  // unsupported format. A file choice is part of "can be set up", not another
  // bucket, so the parts never add up to more than the projects found.
  if (count(digest.connected) > 0) parts.push(`${count(digest.connected)} already connected`);
  if (digest.setup_note) {
    parts.push(DIGEST_SETUP_NOTES[digest.setup_note] || 'project setup is unavailable');
  } else {
    const choices = count(digest.needs_file_choice);
    parts.push(
      `${count(digest.activatable)} can be set up${
        choices > 0 ? ` (${choices} ${choices === 1 ? 'needs' : 'need'} a file choice)` : ''
      }`
    );
    if (count(digest.unsupported_format) > 0)
      parts.push(`${count(digest.unsupported_format)} unsupported format`);
  }
  if (count(digest.unavailable) > 0) parts.push(`${count(digest.unavailable)} no longer found`);
  let text = `Scanned ${name} on ${when}: ${parts.join(', ')}.`;
  if (digest.coverage === 'partial')
    text += ' Partial scan: folders it did not reach were left unchanged.';
  return text;
}

const RUN_SKIP_REASONS = {
  no_manager: 'no Manager is bound to this Home',
  no_model: 'the Manager has no tool-capable model configured',
  provider_unavailable: 'the Home provider is unavailable',
  unavailable: 'the Manager’s library tools are unavailable',
  superseded: 'a newer scan replaced this one',
  model_error: 'the model call failed',
  time_limit: 'the review ran out of time',
  interrupted: 'Ori stopped during the review'
};

const RUN_STOP_REASONS = {
  proposal_limit: 'It stopped at the three-suggestion limit.',
  time_limit: 'It stopped at the one-minute limit.',
  token_limit: 'It stopped at the token budget.',
  step_limit: 'It stopped at the step limit.',
  model_error: 'The model call failed after saving these.'
};

// libraryRunText describes the Manager's bounded review of the digest's scan.
// It never implies a suggestion was applied.
export function libraryRunText(run) {
  if (!run || typeof run !== 'object') return '';
  const count = Number.isInteger(run.proposals) && run.proposals > 0 ? run.proposals : 0;
  if (run.status === 'started') return 'Manager review of this scan is in progress…';
  if (run.status === 'skipped')
    return `Manager review skipped: ${RUN_SKIP_REASONS[run.reason] || 'it could not run'}.`;
  if (run.status !== 'finished') return '';
  const model = String(run.model || '').trim();
  const who = model ? `The Manager (${model})` : 'The Manager';
  const found = count
    ? `left ${count} ${count === 1 ? 'suggestion' : 'suggestions'} for your review`
    : 'had no suggestions';
  const stop = RUN_STOP_REASONS[run.reason] ? ` ${RUN_STOP_REASONS[run.reason]}` : '';
  return `${who} reviewed this scan and ${found}.${stop}`;
}

// libraryFeedbackText totals the owner's answers across suggestion kinds.
// It is shown to the owner only; nothing tunes itself from it.
export function libraryFeedbackText(feedback) {
  const count = value => (Number.isInteger(value) && value > 0 ? value : 0);
  let accepted = 0;
  let dismissed = 0;
  for (const row of Array.isArray(feedback) ? feedback : []) {
    accepted += count(row?.accepted);
    dismissed += count(row?.dismissed);
  }
  if (!accepted && !dismissed) return '';
  return `Your answers to Manager suggestions so far: ${accepted} accepted, ${dismissed} dismissed.`;
}

// A suggestion's origin: the bounded scan review, or a Manager chat.
export function proposalSourceLabel(proposal) {
  if (proposal?.source === 'manager_model') {
    const model = String(proposal.model || '').trim();
    return model ? `From the scan review · ${model}` : 'From the scan review';
  }
  return 'From a Manager chat';
}

// Only queue navigation (opaque entry IDs and a user-confirmed retry) lives in
// this browser tab. Every item still requires a fresh server review and a
// distinct user confirmation; storage never grants folder/creator authority.
// librarySharingView words the Home's shared-assistant switch from the
// library read's `sharing`. Hidden when no installed blueprint opens this Home's
// songs, or the Home is read-only.
export function librarySharingView(sharing, readOnly = false) {
  const state = String(sharing?.state || '');
  if (readOnly || !state || state === 'unavailable') return { visible: false };
  const role = String(sharing.role_label || 'assistant').trim();
  const agent = String(sharing.agent_name || '').trim();
  const view = {
    visible: true,
    checked: state === 'on' || state === 'stale',
    switchDisabled: state === 'stale',
    switchLabel: `Add my ${role} to songs I open`,
    note: '',
    action: null
  };
  switch (state) {
    case 'on':
      view.note = agent
        ? `${agent} joins each song you open. File-only: it works with the project files.`
        : `Your ${role} is added to the first song you open, then joins each one.`;
      break;
    case 'off':
      view.note = `Songs you open get no agent. Turn this on to add your ${role} to them.`;
      break;
    case 'stale':
      view.note = `A plugin update changed your ${role}. Review it to keep adding it to the songs you open.`;
      view.action = { id: 'review', label: 'Review the updated assistant' };
      break;
    default:
      view.note = `Turn this on and your ${role} joins each song you open.`;
  }
  if (sharing.assistant_missing && state === 'on') {
    view.note = `${agent || `Your ${role}`} is gone, so songs you open get no agent. The next one can get a new ${role}.`;
    view.action = { id: 'readd', label: 'Add the assistant again' };
  }
  return view;
}

const QUEUE_LIMIT = 100;
export function readActivationQueue(homeID, storage = globalThis.sessionStorage, now = Date.now()) {
  try {
    const saved = JSON.parse(storage?.getItem(`ori:library-queue:${homeID}`) || 'null');
    if (
      !saved ||
      saved.home_id !== homeID ||
      !Array.isArray(saved.ids) ||
      saved.ids.length < 2 ||
      saved.ids.length > QUEUE_LIMIT ||
      !Number.isInteger(saved.index) ||
      saved.index < 0 ||
      saved.index >= saved.ids.length ||
      !Number.isFinite(saved.created_at) ||
      now - saved.created_at > 24 * 60 * 60 * 1000 ||
      saved.created_at > now ||
      new Set(saved.ids).size !== saved.ids.length ||
      saved.ids.some(id => typeof id !== 'string' || !id || id.length > 160) ||
      (saved.id &&
        (typeof saved.id !== 'string' ||
          saved.id.length > 160 ||
          !Number.isInteger(saved.revision) ||
          saved.revision < 1))
    )
      return null;
    const pending = saved.pending;
    if (
      pending &&
      (pending.id !== saved.ids[saved.index] ||
        ![pending.token, pending.key].every(
          value => typeof value === 'string' && value.length > 0 && value.length <= 160
        ))
    )
      return null;
    return {
      home_id: homeID,
      ...(saved.id ? { id: saved.id, revision: saved.revision } : {}),
      ids: [...saved.ids],
      index: saved.index,
      created_at: saved.created_at,
      pending: pending ? { id: pending.id, token: pending.token, key: pending.key } : null
    };
  } catch (_) {
    return null;
  }
}

// This panel belongs to the exact Home. No browser path, child workspace ID,
// source-file content, or model call is accepted as an authority input.
export class ProjectLibraryPanel {
  constructor({ workspaceId, program, fetchImpl = globalThis.fetch } = {}) {
    this.workspaceId = String(workspaceId || '').trim();
    this.program = program || {};
    this.fetchImpl = (...args) => fetchImpl(...args);
    this.state = null;
    this.cursor = '';
    this.rows = [];
    this.searchGeneration = 0;
    this.searchInFlight = false;
    this.busy = false;
    this.selectedProjects = new Set();
    this.queue = readActivationQueue(this.workspaceId);
    this.recentQueues = [];
    const fromOffer =
      new URLSearchParams(globalThis.location?.search || '').get('folder_offer_id') || '';
    this.offerID = fromOffer.length <= 160 ? fromOffer : '';
    // Collections chosen for this Home that the server reported, newest first.
    this.continuations = [];
  }

  // A Home reopened without its query string (new tab, bookmark, an address
  // that was rewritten) still knows which collection it was created from. The
  // server names it only by an opaque offer ID and only while it can still vouch
  // for the folder. Adopting that ID grants nothing: pick-offer, the root review
  // and the commit each re-verify the Home, provider and folder. More than one
  // ready collection is ambiguous, so none is chosen for the user.
  async restoreCollectionContinuation({ adopt = true } = {}) {
    if (!this.workspaceId) return;
    try {
      const response = await this.fetchImpl(
        `/api/personal-assistant/folder-digest/continuations?home_id=${encodeURIComponent(this.workspaceId)}`,
        { headers: { Accept: 'application/json' } }
      );
      if (!response.ok) return;
      const result = await payload(response);
      this.continuations = (Array.isArray(result.continuations) ? result.continuations : []).filter(
        item =>
          item &&
          typeof item.offer_id === 'string' &&
          item.offer_id !== '' &&
          item.offer_id.length <= 160
      );
      const ready = this.continuations.filter(item => item.state === 'ready');
      // An address that already names a collection wins; the read still tells us
      // its folder name for the setup card.
      if (adopt && !this.offerID && ready.length === 1) this.offerID = ready[0].offer_id;
    } catch (_) {
      // Purely advisory: without it the user chooses the folder as before.
    }
  }

  get panel() {
    return document.getElementById('projectLibraryPanel');
  }
  get readOnly() {
    return this.state?.provider_read_only !== false;
  }
  url(path = '') {
    return `/api/workspaces/${encodeURIComponent(this.workspaceId)}/assistant-program/library${path}`;
  }
  async request(path, options = {}) {
    const response = await this.fetchImpl(this.url(path), {
      headers: {
        Accept: 'application/json',
        ...(options.body ? { 'Content-Type': 'application/json' } : {})
      },
      ...options
    });
    const result = await payload(response);
    if (!response.ok) throw libraryRequestError(response, result);
    return result;
  }
  post(path, body = {}) {
    return this.request(path, { method: 'POST', body: JSON.stringify(body) });
  }
  // An OS file-manager request needs a fresh Home provider and exact child
  // link. The browser supplies no file path and never opens a DAW here.
  async showConnectedFolder(entryID, workspaceID, trigger) {
    await this.run(trigger, 'Checking the connected project folder…', async () => {
      const roots = await this.request('/roots');
      if (roots.provider_read_only !== false) {
        throw new Error('This Home is read-only; the folder cannot be shown.');
      }
      const current = await this.request(`/projects/${encodeURIComponent(entryID)}/activation`);
      if (current.state !== 'connected' || current.workspace_id !== workspaceID) {
        throw new Error('The project link changed; review the current connection first.');
      }
      const response = await this.fetchImpl(
        `/api/workspaces/${encodeURIComponent(workspaceID)}/project/show-folder`,
        { method: 'POST', headers: { Accept: 'application/json' } }
      );
      const result = await payload(response);
      if (!response.ok) {
        throw new Error(
          result.error?.message ||
            result.error ||
            'This computer could not show the project folder.'
        );
      }
      this.status('Project folder reveal requested on this computer. No DAW was started.');
    });
  }
  status(message) {
    const element = document.getElementById('projectLibraryStatus');
    if (element) element.textContent = message;
  }
  async run(trigger, message, work) {
    if (this.busy) return;
    this.busy = true;
    this.panel?.setAttribute('aria-busy', 'true');
    if (trigger) trigger.disabled = true;
    this.status(message);
    try {
      await work();
    } catch (error) {
      this.status(error.message || 'The library could not be updated. Nothing was confirmed.');
      if (error.reason === 'stale_review') {
        // The Home moved on while a review was open (even an inert review
        // advances its revision). Re-read it so the next review uses the current
        // one instead of conflicting again; nothing was granted.
        await this.refresh();
        this.status(
          'The library changed while you were reviewing, so nothing was granted. It has been refreshed; review again.'
        );
      }
    } finally {
      this.busy = false;
      this.renderQueueControls();
      this.panel?.setAttribute('aria-busy', 'false');
      if (trigger?.id === 'projectSetupNextAction' && trigger.closest?.('[hidden]')) {
        // The step finished and its card hid itself: keep keyboard focus on the
        // shelf rather than on a control that is no longer there.
        const heading = document.getElementById('projectLibraryTitle');
        heading?.setAttribute('tabindex', '-1');
        heading?.focus();
      } else if (trigger?.isConnected) {
        trigger.disabled =
          (trigger.id === 'projectLibraryAdd' &&
            (!this.state?.initialized ||
              this.readOnly ||
              (this.state.picker_available === false && !this.offerID))) ||
          (trigger.id === 'projectLibraryInitialize' && this.readOnly);
        if (
          !trigger.disabled &&
          !(trigger.id === 'projectLibraryInitialize' && this.state?.initialized)
        ) {
          trigger.focus();
        } else {
          const heading = document.getElementById('projectLibraryTitle');
          heading?.setAttribute('tabindex', '-1');
          heading?.focus();
        }
      }
    }
  }

  async init() {
    if (!this.workspaceId || !this.panel || !this.program?.is_station) return;
    this.panel.hidden = false;
    const jump = document.getElementById('projectLibraryJump');
    if (jump) jump.hidden = false;
    document
      .getElementById('projectLibraryInitialize')
      ?.addEventListener('click', event => void this.initialize(event.currentTarget));
    document
      .getElementById('projectLibraryAdd')
      ?.addEventListener('click', event => void this.addFolder(event.currentTarget));
    document.getElementById('projectLibrarySearchForm')?.addEventListener('submit', event => {
      event.preventDefault();
      void this.search(false);
    });
    // "Last saved" means newest first; the direction stays the person's to change.
    document.getElementById('projectLibrarySort')?.addEventListener('change', event => {
      const direction = document.getElementById('projectLibraryDirection');
      if (event.currentTarget.value === 'last_saved' && direction) direction.value = 'desc';
    });
    document
      .getElementById('projectLibraryMore')
      ?.addEventListener('click', () => void this.search(true));
    document
      .getElementById('projectLibrarySelectAll')
      ?.addEventListener('click', () => this.toggleSelectAll());
    document.getElementById('projectLibraryConnectAll')?.addEventListener('click', event => {
      void this.connectSelected(event.currentTarget);
    });
    document.getElementById('projectLibraryQueueStart')?.addEventListener('click', event => {
      void this.startQueue(event.currentTarget);
    });
    document.getElementById('projectLibraryQueueResume')?.addEventListener('click', event => {
      void this.continueQueue(event.currentTarget);
    });
    document.getElementById('projectLibraryQueueDiscard')?.addEventListener('click', event => {
      void this.discardQueue(event.currentTarget);
    });
    document
      .getElementById('projectLibraryMoreRoots')
      ?.addEventListener('click', event => void this.moreRoots(event.currentTarget));
    document
      .getElementById('projectSetupNextAction')
      ?.addEventListener('click', event => void this.runSetupNext(event.currentTarget));
    document
      .getElementById('projectLibrarySharingSwitch')
      ?.addEventListener('change', event => void this.setSharing(event.currentTarget));
    document
      .getElementById('projectLibrarySharingAction')
      ?.addEventListener('click', event => void this.runSharingAction(event.currentTarget));
    await this.restoreCollectionContinuation();
    await this.refresh();
    await this.resumeFromIntegration();
  }

  // The Home's switch for its shared assistant, and the one action that fixes
  // it when it cannot apply. It rides the library read: no extra fetch.
  renderSharing() {
    const section = document.getElementById('projectLibrarySharing');
    if (!section) return;
    const view = librarySharingView(this.state?.sharing, this.readOnly);
    section.hidden = !view.visible;
    if (!view.visible) return;
    const toggle = document.getElementById('projectLibrarySharingSwitch');
    if (toggle) {
      toggle.checked = view.checked;
      toggle.disabled = this.busy || view.switchDisabled;
    }
    const labelElement = document.getElementById('projectLibrarySharingLabel');
    if (labelElement) labelElement.textContent = view.switchLabel;
    const note = document.getElementById('projectLibrarySharingNote');
    if (note) note.textContent = view.note;
    const action = document.getElementById('projectLibrarySharingAction');
    if (action) {
      action.hidden = !view.action;
      action.textContent = view.action?.label || '';
      action.dataset.sharingAction = view.action?.id || '';
      action.disabled = this.busy;
    }
  }

  async postSharing(path, body) {
    const result = await this.post(path, {
      request_id: `sharing-${globalThis.crypto.randomUUID()}`,
      ...body
    });
    if (this.state && result?.sharing) this.state.sharing = result.sharing;
    return result;
  }

  async setSharing(toggle) {
    const sharing = this.state?.sharing;
    if (!sharing || this.busy) return;
    const enabled = Boolean(toggle?.checked);
    await this.run(
      toggle,
      enabled ? 'Turning the shared assistant on…' : 'Turning the shared assistant off…',
      async () => {
        try {
          await this.postSharing(
            '/sharing',
            enabled ? { enabled, team_digest: sharing.team_digest } : { enabled }
          );
          this.status(
            enabled
              ? `Your ${sharing.role_label || 'assistant'} joins each song you open.`
              : 'Songs you open from now on get no agent. Songs already open keep theirs.'
          );
        } catch (error) {
          if (error.payload?.sharing && this.state) this.state.sharing = error.payload.sharing;
          throw error;
        } finally {
          this.renderSharing();
        }
      }
    );
  }

  async runSharingAction(trigger) {
    const sharing = this.state?.sharing;
    const id = trigger?.dataset?.sharingAction || '';
    if (!sharing || this.busy || !id) return;
    if (id === 'review') {
      const role = sharing.role_label || 'assistant';
      const accepted = await this.confirm(
        'Review the updated assistant',
        [
          `A plugin update changed what your ${role} is for this Home's songs.`,
          `${sharing.agent_name || `Your ${role}`} keeps its own model, instructions and tools.`,
          'It joins the songs you open from now on. Songs already open are unchanged.',
          'File-only: nothing here starts live control of the project app.'
        ],
        'Keep adding it',
        trigger
      );
      if (!accepted) return;
      await this.run(trigger, 'Saving your answer…', async () => {
        try {
          await this.postSharing('/sharing', { enabled: true, team_digest: sharing.team_digest });
          this.status(`Your ${role} joins the songs you open again.`);
        } finally {
          this.renderSharing();
        }
      });
      return;
    }
    if (id === 'readd') {
      await this.run(trigger, 'Getting a new assistant ready…', async () => {
        try {
          await this.postSharing('/sharing/assistant', {});
          this.status(`The next song you open gets a new ${sharing.role_label || 'assistant'}.`);
        } finally {
          this.renderSharing();
        }
      });
    }
  }

  // Shows the one next step of an unfinished library above the roster and
  // progression, so a fresh Home opens on what to do rather than on a level
  // meter. Pure navigation: the button only starts that step's own review.
  renderSetupNext() {
    const section = document.getElementById('projectSetupNext');
    if (!section) return;
    const collection =
      this.continuations.find(item => item.offer_id === this.offerID)?.folder || '';
    const step = setupNextStep({ state: this.state, collection, carried: Boolean(this.offerID) });
    this.setupStep = step;
    const visible = step.stage !== 'ready' && step.stage !== 'unknown';
    section.hidden = !visible;
    document.getElementById('assistantProgramPage')?.classList.toggle('is-setup-first', visible);
    if (!visible) return;
    document.getElementById('projectSetupNextTitle').textContent = step.title;
    document.getElementById('projectSetupNextBody').textContent = step.body;
    const action = document.getElementById('projectSetupNextAction');
    if (action) {
      action.hidden = !step.action;
      action.textContent = step.action?.label || '';
      action.disabled = this.busy;
    }
  }

  async runSetupNext(trigger) {
    const action = this.setupStep?.action;
    if (!action || this.busy) return;
    if (action.id === 'initialize')
      return this.initialize(trigger, { announced: /^Start library and /.test(action.label) });
    if (action.id === 'add_folder') return this.addFolder(trigger);
    if (action.id === 'scan') {
      const root = (this.state?.roots || []).find(item => item.id === action.rootId);
      if (root) return this.scanRoot(root, trigger);
    }
  }

  async refresh() {
    try {
      this.state = await this.request('/roots');
      const initialized = this.state.initialized === true;
      document.getElementById('projectLibrarySetup').hidden = initialized;
      document.getElementById('projectLibraryContent').hidden = !initialized;
      const add = document.getElementById('projectLibraryAdd');
      add.hidden = !initialized || this.readOnly;
      add.disabled = this.state.picker_available === false && !this.offerID;
      document.getElementById('projectLibraryInitialize').disabled = this.readOnly;
      if (!initialized) {
        this.status(
          this.readOnly
            ? 'The Home provider is unavailable. Saved library setup needs its exact installed package.'
            : 'No library yet. Review the saved project links before choosing a folder.'
        );
        // Arriving on an uninitialized Home used to skip both of these.
        this.renderSetupNext();
        this.focusArrival();
        return;
      }
      this.renderFormats();
      this.renderRoots();
      this.renderSharing();
      this.renderSetupNext();
      await this.restoreQueue();
      this.renderQueueControls();
      await this.renderResume();
      await this.renderPendingLinks();
      await this.renderProposals();
      await this.search(false);
      if (!this.readOnly && this.state.picker_available === false && !this.offerID)
        this.status(
          'The native folder picker is unavailable here. You can still review approved folders for another scan; use Ori desktop to add a folder.'
        );
    } catch (error) {
      if (this.state?.initialized === true) {
        // A library that was already on screen stays there. A failed re-read
        // means the current check is unavailable, not that saved projects
        // vanished, and hiding them would say otherwise. Nothing was changed.
        this.status(
          'Ori could not re-check this library just now, so this is what was last saved. Nothing was changed; reopen this Home to check again.'
        );
        this.renderSetupNext();
      } else {
        this.state = null;
        this.status(
          error.message || 'Could not load the Home library. Retry by reopening this Home.'
        );
        document.getElementById('projectLibraryAdd').hidden = true;
        document.getElementById('projectLibrarySetup').hidden = true;
        document.getElementById('projectLibraryContent').hidden = true;
        this.renderSetupNext(); // no state: the card hides rather than guessing
      }
    }
    this.focusArrival();
  }

  // Arriving from a map badge, an Action Center card, or a new Home lands on the
  // relevant heading once the page has rendered, instead of wherever the
  // browser's early hash scroll left it (the library sits below four panels and
  // is un-hidden late). Suggestions arrivals land on the suggestions heading; a
  // #projectLibraryPanel arrival lands on the unfinished-setup card when there is
  // one, else the shelf heading. Only the first render counts, so later
  // refreshes never take the user's focus back.
  focusArrival() {
    if (this.arrivalHandled) return;
    const hash = globalThis.location?.hash;
    let heading = null;
    if (hash === '#projectLibraryProposals') {
      const section = document.getElementById('projectLibraryProposals');
      heading =
        section && !section.hidden
          ? document.getElementById('projectLibraryProposalsTitle')
          : document.getElementById('projectLibraryTitle');
    } else if (hash === '#projectLibraryPanel') {
      const next = document.getElementById('projectSetupNext');
      heading =
        next && !next.hidden
          ? document.getElementById('projectSetupNextTitle')
          : document.getElementById('projectLibraryTitle');
    } else {
      return;
    }
    this.arrivalHandled = true;
    if (!heading) return;
    heading.setAttribute('tabindex', '-1');
    heading.scrollIntoView?.({ block: 'start' });
    heading.focus?.({ preventScroll: true });
  }

  async renderPendingLinks() {
    const section = document.getElementById('projectLibraryPendingLinks');
    const container = document.getElementById('projectLibraryPendingRows');
    if (!section || !container) return;
    container.replaceChildren();
    section.hidden = true;
    let pending;
    try {
      pending = await this.request('/linked-projects/pending');
    } catch (_) {
      return; // A failed verification never suggests an unverified association.
    }
    if (!pending.total || this.readOnly) return;
    section.hidden = false;
    for (const project of pending.rows || []) {
      const card = node('article', 'project-library-resume-card');
      const heading = node('h4', '', project.name);
      const button = node(
        'button',
        'modern-btn modern-btn-secondary',
        `Review ${project.name} for this shelf`
      );
      button.type = 'button';
      button.addEventListener(
        'click',
        () =>
          void this.run(button, 'Checking the exact linked project…', async () => {
            const id = encodeURIComponent(project.workspace_id);
            const review = await this.post(`/linked-projects/${id}/review`, {
              revision: pending.revision
            });
            if (
              !(await this.confirm(
                `Add ${review.project_name} to this shelf?`,
                [
                  review.link_only
                    ? 'No approved scan matched this project. Add link-only metadata without a discovery folder grant.'
                    : 'Reuse the exact saved discovery record; preserve its notes and observations.',
                  'The project workspace is already connected. This does not create a project, scan, read its file, staff an agent, or open a DAW.'
                ],
                'Add linked project',
                button
              ))
            ) {
              await this.refresh(); // Even a canceled review advances Home revision.
              return;
            }
            try {
              await this.post(`/linked-projects/${id}/commit`, {
                review_token: review.token,
                idempotency_key: operationKey('associate-linked-project'),
                confirm: true
              });
            } catch (error) {
              await this.refresh();
              throw error;
            }
            await this.refresh();
            this.status(`${review.project_name} is now represented on this shelf.`);
          })
      );
      card.append(heading, button);
      container.append(card);
    }
    if (pending.total > pending.rows.length)
      container.append(
        node(
          'p',
          '',
          `${pending.total - pending.rows.length} more linked projects remain; review these first and refresh.`
        )
      );
  }

  async renderProposals() {
    const section = document.getElementById('projectLibraryProposals');
    const container = document.getElementById('projectLibraryProposalRows');
    if (!section || !container) return;
    container.replaceChildren();
    section.hidden = true;
    // The digest is a model-free summary of the last completed scan. It is
    // shown even when the Manager has made no suggestions.
    let summary = null;
    try {
      summary = await this.request('/summary');
    } catch (_) {
      summary = null;
    }
    const digestText = libraryDigestText(summary?.digest, this.state?.roots);
    const digestLine = document.getElementById('projectLibraryDigest');
    if (digestLine) {
      digestLine.textContent = digestText;
      digestLine.hidden = !digestText;
    }
    const runText = digestText ? libraryRunText(summary?.proposal_run) : '';
    const runLine = document.getElementById('projectLibraryRun');
    if (runLine) {
      runLine.textContent = runText;
      runLine.hidden = !runText;
    }
    const feedbackText = libraryFeedbackText(summary?.feedback);
    const feedbackLine = document.getElementById('projectLibraryFeedback');
    if (feedbackLine) {
      feedbackLine.textContent = feedbackText;
      feedbackLine.hidden = !feedbackText;
    }
    let page = null;
    try {
      page = await this.request('/proposals');
    } catch (_) {
      page = null; // An unavailable provider/binding never creates a review action.
    }
    if (!page?.total) {
      if (!digestText) return;
      section.hidden = false;
      container.append(node('p', '', 'No Manager suggestions to review for this scan.'));
      return;
    }
    section.hidden = false;
    for (const row of page.rows || []) {
      const proposal = row.proposal;
      const navigation = proposal.kind === 'project_review';
      const sessionGoal = proposal.kind === 'session_goal';
      const sessionRecap = proposal.kind === 'session_recap';
      const rootReview = proposal.kind === 'root_review';
      const card = node('article', 'project-library-resume-card');
      card.append(
        node('h4', '', row.name),
        node(
          'p',
          '',
          navigation
            ? 'Suggested navigation: inspect this song’s current project setup options. No setup was reviewed or authorized.'
            : sessionGoal
              ? `Suggested studio goal: ${proposal.goal.goal}. Desired outcome: ${proposal.goal.desired_outcome || 'Not set'}. Estimated effort: ${proposal.goal.time_minutes ? `${proposal.goal.time_minutes} minutes` : 'Not estimated'}. No session was saved.`
              : sessionRecap
                ? `Suggested studio recap: ${proposal.recap}. This is not evidence that work happened, a task finished or a DAW ran. No recap was saved.`
                : rootReview
                  ? 'Suggested navigation: inspect current discovery folder options. No folder was selected, approved or scanned.'
                  : `Suggested next action: ${proposal.next_action}`
        ),
        node(
          'p',
          '',
          proposal.reason
            ? `Manager's reason (untrusted note): ${proposal.reason}`
            : 'No reason provided.'
        ),
        node(
          'small',
          '',
          `Suggested by ${proposal.agent_name} · ${proposalSourceLabel(proposal)} · ${
            row.status === 'ready'
              ? 'Ready for your separate review'
              : row.status === 'dismissed'
                ? 'Dismissed by you'
                : `Not actionable (${row.status})`
          }`
        )
      );
      if (row.status === 'ready' && !this.readOnly) {
        const button = node(
          'button',
          'modern-btn modern-btn-secondary',
          navigation
            ? `View ${row.name} setup options`
            : sessionGoal
              ? `Edit ${row.name} suggested goal`
              : sessionRecap
                ? `Edit ${row.name} suggested recap`
                : rootReview
                  ? 'View discovery folder options'
                  : `Review ${row.name} suggestion`
        );
        button.type = 'button';
        button.addEventListener('click', () =>
          navigation
            ? void this.details(proposal.entry_id, button)
            : sessionGoal
              ? void this.editGoalProposal(row, button)
              : sessionRecap
                ? void this.editRecapProposal(row, button)
                : rootReview
                  ? void this.viewRootProposal(row, button)
                  : void this.reviewProposal(row, button)
        );
        card.append(button);
      }
      // Dismissing only removes, so it stays available when the Manager or
      // the Home provider has gone away (the row then reads "unavailable").
      if (row.status === 'ready' || row.status === 'unavailable') {
        const dismiss = node(
          'button',
          'modern-btn modern-btn-secondary project-library-dismiss',
          `Dismiss ${row.name} suggestion`
        );
        dismiss.type = 'button';
        dismiss.addEventListener('click', () => void this.dismissProposal(row, dismiss));
        card.append(dismiss);
      }
      container.append(card);
    }
    if (page.total > (page.rows || []).length)
      container.append(
        node(
          'p',
          '',
          `Showing the latest ${(page.rows || []).length} of ${page.total} saved suggestions.`
        )
      );
  }

  // A dismissal is its own owner answer. The idempotency key survives a lost
  // reply, so retrying the same dismissal replays instead of failing.
  async dismissProposal(row, trigger) {
    await this.run(trigger, 'Checking this suggestion…', async () => {
      const proposal = row.proposal;
      const confirmed = await this.confirm(
        `Dismiss the suggestion for ${row.name}?`,
        [
          'Only this suggestion is marked dismissed. No notes, sessions, folders or workspaces change.',
          'The Manager’s scan reviews will not suggest the same kind of step for this project again for seven days.'
        ],
        'Dismiss suggestion',
        trigger
      );
      if (!confirmed) {
        this.status('Nothing was dismissed.');
        return;
      }
      this.pendingDismissals ||= new Map();
      const key = this.pendingDismissals.get(proposal.id) || operationKey('dismiss');
      this.pendingDismissals.set(proposal.id, key);
      await this.post(`/proposals/${encodeURIComponent(proposal.id)}/dismiss`, {
        confirm: true,
        idempotency_key: key
      });
      this.pendingDismissals.delete(proposal.id);
      // Refresh first: it resets the status line, and this message must stay.
      await this.refresh();
      this.status(`Dismissed the suggestion for ${row.name}. Nothing else changed.`);
    });
  }

  async editRecapProposal(row, trigger) {
    await this.run(
      trigger,
      'Checking this suggested studio recap against the current Home…',
      async () => {
        const suggestions = await this.request('/proposals');
        const current = suggestions.rows?.find(
          item =>
            item.proposal.id === row.proposal.id && item.proposal.digest === row.proposal.digest
        );
        if (!current || current.status !== 'ready' || current.proposal.kind !== 'session_recap')
          throw new Error('The suggested recap is no longer current. Review saved sessions first.');
        const proposal = current.proposal;
        const detail = await this.request(`/projects/${encodeURIComponent(proposal.entry_id)}`);
        if (
          detail.entry_revision !== proposal.entry_revision ||
          detail.row.fields_revision !== proposal.fields_revision
        )
          throw new Error('This song changed. Review its current Details before wrapping up.');
        const session = await this.request(
          `/projects/${encodeURIComponent(proposal.entry_id)}/sessions/${encodeURIComponent(proposal.session_id)}`
        );
        if (
          session.entry_id !== proposal.entry_id ||
          session.id !== proposal.session_id ||
          session.revision !== proposal.session_revision ||
          session.state !== 'accepted' ||
          session.recap
        )
          throw new Error(
            'This saved session changed. Reopen its current history before wrapping up.'
          );
        this.sessionForm(detail, session, trigger, null, proposal.recap);
      }
    );
  }

  async viewRootProposal(row, trigger) {
    await this.run(trigger, 'Checking current discovery options…', async () => {
      await this.refresh(); // Render the owner's current root state, not the Manager's snapshot.
      const suggestions = await this.request('/proposals');
      const current = suggestions.rows?.find(
        item => item.proposal.id === row.proposal.id && item.proposal.digest === row.proposal.digest
      );
      if (!current || current.status !== 'ready' || current.proposal.kind !== 'root_review')
        throw new Error('Discovery folders changed. Review the current Home controls instead.');
      const roots = document.getElementById('projectLibraryRoots');
      roots.setAttribute('tabindex', '-1');
      roots.scrollIntoView({ block: 'center' });
      roots.focus();
      this.status(
        'Current discovery folder options are in focus. Separate owner reviews are required.'
      );
    });
  }

  async editGoalProposal(row, trigger) {
    await this.run(
      trigger,
      'Checking this suggested studio goal against the current Home…',
      async () => {
        const suggestions = await this.request('/proposals');
        const current = suggestions.rows?.find(
          item =>
            item.proposal.id === row.proposal.id && item.proposal.digest === row.proposal.digest
        );
        if (!current || current.status !== 'ready' || current.proposal.kind !== 'session_goal')
          throw new Error(
            'The suggested goal is no longer current. Refresh the Home before planning.'
          );
        const detail = await this.request(`/projects/${encodeURIComponent(row.proposal.entry_id)}`);
        if (detail.entry_revision !== row.proposal.entry_revision)
          throw new Error('This song changed. Review its current Details before planning.');
        this.sessionForm(detail, null, trigger, current.proposal.goal);
      }
    );
  }

  async reviewProposal(row, trigger) {
    await this.run(
      trigger,
      'Checking this Manager suggestion against the current Home…',
      async () => {
        const path = `/proposals/${encodeURIComponent(row.proposal.id)}`;
        const review = await this.post(`${path}/review`);
        if (
          !(await this.confirm(
            `Save ${row.name}'s suggested next action?`,
            [
              `Current Home next action: ${review.before.next_action || 'Not set'}`,
              `Proposed Home next action: ${review.after.next_action}`,
              `Manager's reason (untrusted note): ${row.proposal.reason || 'None provided'}`,
              'This saves one user-reviewed Home note only. No scan, project task, agent, DAW or source file is opened or changed.'
            ],
            'Save next action',
            trigger
          ))
        ) {
          await this.refresh(); // The review advanced the Home revision, but saved no field.
          this.status('Suggestion canceled. Saved Home notes are unchanged.');
          return;
        }
        try {
          await this.post(`${path}/commit`, {
            review_token: review.token,
            idempotency_key: operationKey('manager-next-action'),
            confirm: true
          });
        } catch (error) {
          await this.refresh();
          throw error;
        }
        await this.refresh();
        this.status(`Saved the reviewed next action for ${row.name}. No project task was started.`);
      }
    );
  }

  async renderResume() {
    const section = document.getElementById('projectLibraryResume');
    const container = document.getElementById('projectLibraryResumeCards');
    if (!section || !container) return;
    container.replaceChildren();
    let view;
    try {
      view = await this.request('/resume');
    } catch (_) {
      section.hidden = false;
      container.append(
        node('p', '', 'Resume is unavailable. Saved project details may still be readable below.')
      );
      return;
    }
    section.hidden = !view.cards?.length;
    for (const card of (view.cards || []).slice(0, 3)) {
      const item = node('article', 'project-library-resume-card');
      const heading = node('h4', '', card.name);
      const session = card.session;
      item.append(
        heading,
        node('p', '', `Saved goal: ${session.goal}`),
        node('p', '', session.recap ? `Your saved recap: ${session.recap}` : 'No recap saved yet.'),
        node(
          'p',
          '',
          card.project_next_action
            ? `Current saved project next action: ${card.project_next_action}`
            : session.next_action
              ? `Session’s saved next action: ${session.next_action}`
              : 'No next action saved.'
        ),
        node(
          'small',
          '',
          `${session.shared_from_project ? 'User-authored linked-project recap' : 'User-authored note'} · saved ${new Date(session.updated_at).toLocaleDateString()}`
        )
      );
      const open = node('button', 'modern-btn modern-btn-secondary', `View ${card.name} session`);
      open.type = 'button';
      open.addEventListener('click', () => void this.details(card.entry_id, open));
      item.append(open);
      if (card.workspace_id) {
        const workspace = node(
          'button',
          'modern-btn modern-btn-secondary',
          `Open ${card.name} workspace`
        );
        workspace.type = 'button';
        workspace.addEventListener(
          'click',
          () =>
            void this.run(workspace, 'Checking the saved project link…', async () => {
              // The card is a hint. Never navigate from a stale or foreign link.
              const current = await this.request(
                `/projects/${encodeURIComponent(card.entry_id)}/activation`
              );
              if (current.state !== 'connected' || current.workspace_id !== card.workspace_id) {
                this.status('That saved link changed. Reopen project details to review it.');
                await this.renderResume();
                return;
              }
              const route = await this.workspaceRoute(current.workspace_id);
              if (!route) {
                this.status(
                  'That workspace page could not be found right now. Nothing was changed.'
                );
                return;
              }
              globalThis.location.assign(route);
            })
        );
        item.append(workspace);
      }
      container.append(item);
    }
  }

  async moreRoots(trigger) {
    if (!this.state?.next_offset) return;
    await this.run(trigger, 'Loading more saved discovery folders…', async () => {
      const page = await this.request(`/roots?offset=${this.state.next_offset}`);
      if (
        page.revision !== this.state.revision ||
        page.total_roots !== this.state.total_roots ||
        page.provider_read_only !== this.state.provider_read_only
      ) {
        await this.refresh();
        this.status('Discovery folders changed. Start with the refreshed list.');
        return;
      }
      this.state.roots = [...(this.state.roots || []), ...(page.roots || [])];
      this.state.next_offset = page.next_offset || 0;
      this.renderRoots();
    });
  }

  renderFormats() {
    const filter = document.getElementById('projectLibraryFormat');
    if (!filter) return;
    const selected = filter.value;
    const all = node('option', '', 'All formats');
    all.value = '';
    const choices = (this.state?.formats || []).slice(0, 16).map(format => {
      const option = node('option', '', format.label);
      option.value = format.id;
      return option;
    });
    filter.replaceChildren(all, ...choices);
    filter.value = choices.some(choice => choice.value === selected) ? selected : '';
  }

  renderRoots() {
    const container = document.getElementById('projectLibraryRoots');
    container.replaceChildren();
    const roots = Array.isArray(this.state?.roots) ? this.state.roots : [];
    const filter = document.getElementById('projectLibraryRoot');
    if (filter) {
      const selected = filter.value;
      const all = node('option', '', 'All discovery folders');
      all.value = '';
      const choices = roots.map(root => {
        const option = node('option', '', root.path);
        option.value = root.id;
        return option;
      });
      filter.replaceChildren(all, ...choices);
      filter.value = choices.some(choice => choice.value === selected) ? selected : '';
    }
    for (const root of roots) {
      const card = node('article', 'project-library-root');
      const copy = node('div');
      copy.append(
        node('strong', '', root.path),
        node(
          'small',
          '',
          root.revoked_at
            ? 'Disconnected · historical entries retained'
            : root.needs_review
              ? 'Restore requires a new reviewed folder grant'
              : root.last_scan
                ? `${label(root.last_scan.status)} · ${root.last_scan.scope || 'root'} · ${root.last_scan.entries_seen} names checked · ${root.last_scan.skipped_entries} skipped${root.last_scan.partial_reason ? ` · ${label(root.last_scan.partial_reason)}` : ''}`
                : 'Connected · not scanned yet'
        )
      );
      const actions = node('div', 'project-library-root-actions');
      if (!root.revoked_at && !root.needs_review && !this.readOnly) {
        const scan = node('button', 'modern-btn modern-btn-secondary', 'Review scan');
        scan.type = 'button';
        scan.addEventListener('click', () => void this.scanRoot(root, scan));
        actions.append(scan);
        if (root.has_scopes) {
          const narrower = node(
            'button',
            'modern-btn modern-btn-secondary',
            'Review smaller folder'
          );
          narrower.type = 'button';
          narrower.addEventListener('click', () => void this.knownScopes(root, narrower));
          actions.append(narrower);
        }
      }
      if (!root.revoked_at) {
        const revoke = node('button', 'modern-btn modern-btn-secondary', 'Disconnect');
        revoke.type = 'button';
        revoke.addEventListener('click', () => void this.revokeRoot(root, revoke));
        actions.append(revoke);
      }
      card.append(copy, actions);
      container.append(card);
    }
    document.getElementById('projectLibraryRootCount').textContent =
      `${roots.length} of ${this.state?.total_roots || 0} discovery folders`;
    document.getElementById('projectLibraryMoreRoots').hidden = !this.state?.next_offset;
    if (!roots.length)
      container.append(
        node(
          'p',
          'project-library-empty',
          'No discovery folders connected. Add one only when you want to review its metadata.'
        )
      );
  }

  async search(more) {
    if (more && (this.searchInFlight || !this.cursor)) return;
    const tbody = document.getElementById('projectLibraryRows');
    if (!more) {
      this.searchGeneration++;
      this.cursor = '';
      this.rows = [];
      tbody.replaceChildren();
    }
    const generation = this.searchGeneration;
    const moreButton = document.getElementById('projectLibraryMore');
    if (more) {
      this.searchInFlight = true;
      moreButton.disabled = true;
    }
    const params = libraryQuery({
      text: document.getElementById('projectLibrarySearch')?.value || '',
      stage: document.getElementById('projectLibraryStage')?.value || '',
      status: document.getElementById('projectLibraryStatusFilter')?.value || '',
      format: document.getElementById('projectLibraryFormat')?.value || '',
      rootID: document.getElementById('projectLibraryRoot')?.value || '',
      connection: document.getElementById('projectLibraryConnection')?.value || '',
      availability: document.getElementById('projectLibraryAvailability')?.value || '',
      priority: document.getElementById('projectLibraryPriority')?.value || '',
      sort: document.getElementById('projectLibrarySort')?.value || 'name',
      direction: document.getElementById('projectLibraryDirection')?.value || 'asc',
      cursor: more ? this.cursor : ''
    });
    try {
      const page = await this.request(`/projects?${params}`);
      if (generation !== this.searchGeneration) return; // A newer search owns the visible results.
      this.rows.push(...(page.rows || []));
      this.cursor = page.next_cursor || '';
      this.renderRows();
      this.renderQueueControls();
      moreButton.hidden = !this.cursor;
      document.getElementById('projectLibraryCount').textContent =
        `${this.rows.length} of ${page.total} projects`;
      this.status(
        this.readOnly
          ? 'Saved records are readable; the Home provider is unavailable for new changes.'
          : page.total
            ? 'Saved observations only. Open a row to inspect its source and notes.'
            : 'No projects match. Try another filter or review a folder scan.'
      );
    } catch (error) {
      if (generation !== this.searchGeneration) return;
      this.cursor = '';
      moreButton.hidden = true;
      this.status(error.message || 'Could not load this page. Refresh the library and retry.');
    } finally {
      if (more) {
        this.searchInFlight = false;
        moreButton.disabled = false;
      }
    }
  }

  renderRows() {
    const tbody = document.getElementById('projectLibraryRows');
    tbody.replaceChildren();
    for (const row of this.rows) {
      const tr = node('tr');
      const pick = node('td');
      if (!this.readOnly && row.connection === 'catalog_only') {
        const checkbox = node('input');
        checkbox.type = 'checkbox';
        checkbox.checked = this.selectedProjects.has(row.id);
        checkbox.setAttribute('aria-label', `Select ${row.name} for serial project review`);
        checkbox.addEventListener('change', () => {
          if (checkbox.checked && this.selectedProjects.size >= QUEUE_LIMIT) {
            checkbox.checked = false;
            this.status(`Review at most ${QUEUE_LIMIT} projects per queue.`);
            return;
          }
          if (checkbox.checked) this.selectedProjects.add(row.id);
          else this.selectedProjects.delete(row.id);
          this.renderQueueControls();
        });
        pick.append(checkbox);
      }
      const name = node('td');
      name.append(
        node('strong', '', row.name),
        node('small', '', row.next_action || 'No next action saved')
      );
      const saved = lastSavedLabel(row.last_saved_at);
      if (saved) name.append(node('small', 'project-library-saved', saved));
      const stage = node('td');
      stage.append(node('span', '', label(row.stage)), node('small', '', label(row.status)));
      const connection = node('td', '', label(row.connection));
      const observed = node('td');
      observed.append(
        node('span', '', label(row.last_observed_availability)),
        node(
          'small',
          '',
          row.last_scanned_at
            ? `Scanned ${new Date(row.last_scanned_at).toLocaleDateString()}`
            : 'Not scanned'
        )
      );
      const action = node('td', 'project-library-row-actions');
      const open = libraryOpenAction(row, this.readOnly);
      if (open) {
        const openButton = node('button', 'modern-btn modern-btn-primary', open.label);
        openButton.type = 'button';
        openButton.dataset.libraryOpen = row.id;
        openButton.setAttribute('aria-label', open.ariaLabel);
        openButton.addEventListener('click', () => void this.openRow(row, openButton, action));
        action.append(openButton);
      }
      const button = node('button', 'modern-btn modern-btn-secondary', 'Details');
      button.type = 'button';
      button.setAttribute('aria-label', `Review ${row.name}`);
      button.addEventListener('click', () => void this.details(row.id, button));
      action.append(button);
      tr.append(pick, name, stage, connection, observed, action);
      tbody.append(tr);
    }
  }

  // Open one song in one click: its workspace is made (or the one it has is
  // used), the Home's shared assistant joins it, and the page goes there. The
  // server repeats every check; a folder with several project files comes back
  // as chips, and the same request ID makes a retry finish what is missing.
  async openRow(row, trigger, cell, selectedFile = '') {
    if (this.busy || this.readOnly || !row?.id) return;
    this.openRequests = this.openRequests || new Map();
    const requestID = this.openRequests.get(row.id) || `open-${globalThis.crypto.randomUUID()}`;
    this.openRequests.set(row.id, requestID);
    const body = { request_id: requestID };
    if (selectedFile) body.selected_file = selectedFile;
    const label = trigger?.textContent || 'Open';
    this.busy = true;
    cell?.setAttribute('aria-busy', 'true');
    if (trigger) {
      trigger.disabled = true;
      trigger.textContent = 'Opening…';
    }
    this.status(`Opening ${row.name}… making its workspace and adding your assistant.`);
    try {
      const result = await this.post(`/projects/${encodeURIComponent(row.id)}/open`, body);
      this.openRequests.delete(row.id);
      this.status(libraryOpenedMessage(row.name, result));
      const route = libraryOpenRoute(result);
      if (route) globalThis.location.assign(route);
    } catch (error) {
      const choices = libraryOpenChoices(error);
      if (choices.length && cell) {
        this.renderOpenChoices(row, cell, choices);
        this.status(`${row.name} has more than one project file. Choose the one to open.`);
      } else {
        this.status(error.message || `${row.name} could not be opened. Nothing was changed.`);
      }
    } finally {
      this.busy = false;
      cell?.setAttribute('aria-busy', 'false');
      if (trigger?.isConnected) {
        trigger.disabled = false;
        trigger.textContent = label;
      }
    }
  }

  // The file chips for a song folder with several project files.
  renderOpenChoices(row, cell, choices) {
    cell.querySelector('.project-library-open-choice')?.remove();
    const group = node('div', 'project-library-open-choice');
    group.setAttribute('role', 'group');
    group.setAttribute('aria-label', `Project file to open for ${row.name}`);
    for (const name of choices) {
      const chip = node('button', 'modern-btn modern-btn-secondary', name);
      chip.type = 'button';
      chip.dataset.libraryOpenFile = name;
      chip.addEventListener('click', () => void this.openRow(row, chip, cell, name));
      group.append(chip);
    }
    cell.append(group);
    group.querySelector('button')?.focus();
  }

  // Rows a person can pick: catalog-only songs on the pages loaded so far.
  selectableRows() {
    return this.readOnly ? [] : this.rows.filter(row => row.connection === 'catalog_only');
  }

  toggleSelectAll() {
    const pickable = this.selectableRows();
    if (this.queue || !pickable.length) return;
    if (pickable.every(row => this.selectedProjects.has(row.id))) {
      for (const row of pickable) this.selectedProjects.delete(row.id);
    } else {
      let left = 0;
      for (const row of pickable) {
        if (this.selectedProjects.has(row.id)) continue;
        if (this.selectedProjects.size >= QUEUE_LIMIT) left++;
        else this.selectedProjects.add(row.id);
      }
      if (left)
        this.status(`Selected ${QUEUE_LIMIT}, the most one queue takes; ${left} were left out.`);
      else if (this.cursor)
        this.status('Selected every song shown. Load more to include the rest.');
    }
    this.renderRows();
    this.renderQueueControls();
  }

  // Connect every selected song that needs no further choice, after ONE review
  // that lists each song with its exact file. The library binds a server review
  // to the document revision, so the reviews cannot all be taken up front: each
  // song is still reviewed and committed on its own with its own token and key,
  // and only when the server's review names the file the person confirmed. A song
  // that needs a file choice or an integration is left alone and named. The first
  // failure stops the rest; songs already connected stay connected.
  async connectSelected(trigger) {
    if (this.queue || this.readOnly || this.busy || this.selectedProjects.size < 2) return;
    await this.run(trigger, 'Checking the selected songs…', async () => {
      const ready = [];
      const left = [];
      let integration = null;
      for (const id of [...this.selectedProjects]) {
        const path = `/projects/${encodeURIComponent(id)}`;
        const detail = await this.request(path);
        if (detail.row.connection === 'connected') {
          this.selectedProjects.delete(id);
          continue;
        }
        const eligibility = await this.request(`${path}/activation`);
        const files = eligibility.project_files || [];
        if (eligibility.state === 'review_available' && files.length === 1) {
          ready.push({
            id,
            path,
            name: detail.row.name,
            file: files[0],
            roles: eligibility.project_role_labels || []
          });
        } else {
          const offered =
            eligibility.state === 'project_provider_unavailable' && eligibility.integration_offer;
          if (offered && !integration) integration = { detail, offer: offered };
          left.push(
            `${detail.row.name}: ${
              eligibility.state === 'file_choice_required'
                ? 'choose its project file'
                : offered
                  ? `needs the ${offered.display_name || 'project'} integration`
                  : eligibility.reason || 'project setup needs its own review'
            }`
          );
        }
      }
      if (!ready.length) {
        this.renderRows();
        if (integration) {
          // Nothing can be connected yet because the integration is missing: say so
          // where it is seen and offer its review, rather than a line of small text.
          const name = integration.offer.display_name || 'project';
          if (
            await this.confirm(
              `Install the ${name} integration first?`,
              [
                `None of the ${left.length} selected songs can be connected yet:`,
                ...left,
                'Nothing is installed until you confirm it in the integration review. Afterwards you return to a song here and can select all again.'
              ],
              `Review the ${name} integration`,
              trigger
            )
          ) {
            this.startIntegrationReview(integration.detail, integration.offer);
            return;
          }
        }
        this.status(
          left.length
            ? `Nothing to connect together. ${left.join('; ')}.`
            : 'The selected songs are already connected.'
        );
        return;
      }
      const roles = [...new Set(ready.flatMap(item => item.roles))];
      const lines = [
        `Connect ${ready.length} songs, each as its own project workspace:`,
        ...ready.map(item => `${item.name} — ${item.file}`),
        `Installed project roles: ${roles.join(', ') || 'none declared'}`,
        'Each starts File-only. Source files stay where they are and are never changed. Project-role staffing and live access need separate reviews.'
      ];
      if (left.length) lines.push(`Not included (each needs its own review): ${left.join('; ')}`);
      if (!(await this.confirm(`Connect ${ready.length} songs?`, lines, 'Connect all', trigger))) {
        this.status('Nothing was connected.');
        return;
      }
      const done = [];
      let failure = '';
      for (const item of ready) {
        try {
          const detail = await this.request(item.path);
          if (detail.row.connection !== 'connected') {
            const eligibility = await this.request(`${item.path}/activation`);
            const files = eligibility.project_files || [];
            if (
              eligibility.state !== 'review_available' ||
              files.length !== 1 ||
              files[0] !== item.file
            ) {
              throw new Error('it changed after you confirmed, so it was not connected');
            }
            const review = await this.post(`${item.path}/activation/review`, {
              workspace_name: detail.row.name.slice(0, 128),
              project_file: item.file,
              if_revision: detail.revision
            });
            if (review.project_file !== item.file) {
              throw new Error('its reviewed file differs from the one you confirmed');
            }
            await this.post(`${item.path}/activation/commit`, {
              review_token: review.token,
              idempotency_key: operationKey('batch-activation'),
              confirm: true
            });
          }
          done.push(item.name);
          this.selectedProjects.delete(item.id);
        } catch (error) {
          failure = `${item.name}: ${error.message || 'could not be connected'}`;
          break;
        }
      }
      await this.refresh();
      this.status(
        [
          `Connected ${done.length} of ${ready.length}.`,
          failure ? `Stopped at ${failure}. Songs not reached were left alone.` : '',
          left.length ? `${left.length} need their own review.` : ''
        ]
          .filter(Boolean)
          .join(' ')
      );
    });
  }

  renderQueueControls() {
    const message = document.getElementById('projectLibraryQueueStatus');
    const start = document.getElementById('projectLibraryQueueStart');
    const resume = document.getElementById('projectLibraryQueueResume');
    const discard = document.getElementById('projectLibraryQueueDiscard');
    if (!message || !start || !resume || !discard) return;
    const pending = this.queue;
    message.textContent =
      pending?.status === 'expired'
        ? 'Saved queue expired. Discard it before starting a new review; no project was created by expiry.'
        : pending
          ? `${pending.index} of ${pending.ids.length} handled · ${pending.skipped?.length || 0} skipped · order and skips saved on this Home. Each remaining song needs its own review; already connected songs stay connected.`
          : this.selectedProjects.size
            ? `${this.selectedProjects.size} selected · nothing is connected until you review and confirm.`
            : 'Choose at least two catalog-only projects to review one at a time.';
    start.disabled = this.busy || this.readOnly || !!pending || this.selectedProjects.size < 2;
    const selectAll = document.getElementById('projectLibrarySelectAll');
    const connectAll = document.getElementById('projectLibraryConnectAll');
    if (selectAll) {
      const pickable = this.selectableRows();
      const everyPicked =
        pickable.length > 0 && pickable.every(row => this.selectedProjects.has(row.id));
      selectAll.textContent = everyPicked ? 'Clear selection' : `Select all (${pickable.length})`;
      selectAll.disabled = this.busy || this.readOnly || !!pending || pickable.length === 0;
    }
    if (connectAll) {
      connectAll.disabled =
        this.busy || this.readOnly || !!pending || this.selectedProjects.size < 2;
    }
    resume.hidden = discard.hidden = !pending;
    resume.disabled = this.busy || this.readOnly || pending?.status === 'expired';
    discard.disabled = this.busy;
  }

  // Browser storage is only an optional same-tab, previously confirmed creator
  // retry key. Home order, skips and connection receipts come from the server.
  renderQueueHistory() {
    if (typeof document === 'undefined') return;
    const section = document.getElementById('projectLibraryQueueHistory');
    const rows = document.getElementById('projectLibraryQueueHistoryRows');
    if (!section || !rows) return;
    rows.replaceChildren();
    section.hidden = !this.recentQueues.length;
    for (const outcome of this.recentQueues.slice(0, 5)) {
      rows.append(
        node(
          'li',
          '',
          `${outcome.status === 'complete' ? 'Completed' : 'Discarded'} ${new Date(outcome.finished_at).toLocaleDateString()} · ${outcome.connected_count} connected · ${outcome.skipped_count} skipped. ${outcome.status === 'discarded' ? 'Unreviewed songs were not activated.' : 'Every song was handled individually.'}`
        )
      );
    }
  }

  saveQueue() {
    const key = `ori:library-queue:${this.workspaceId}`;
    try {
      if (this.queue) globalThis.sessionStorage.setItem(key, JSON.stringify(this.queue));
      else globalThis.sessionStorage.removeItem(key);
      this.renderQueueControls();
      return true;
    } catch (_) {
      this.renderQueueControls();
      return false;
    }
  }

  async restoreQueue() {
    const local = readActivationQueue(this.workspaceId);
    const { queue, recent } = await this.request('/queue');
    this.recentQueues = Array.isArray(recent) ? recent.slice(0, 5) : [];
    if (queue?.status === 'expired') {
      this.queue = {
        ...queue,
        home_id: this.workspaceId,
        created_at: Date.parse(queue.created_at),
        pending: null
      };
      this.status(
        'Saved review queue expired. Discard it before starting another. No project was created by expiry.'
      );
    } else if (queue?.status === 'active') {
      this.queue = {
        ...queue,
        home_id: this.workspaceId,
        created_at: Date.parse(queue.created_at),
        pending:
          local?.id === queue.id && local.ids[local.index] === queue.ids[queue.index]
            ? local.pending
            : null
      };
    } else {
      // A legacy tab-only queue is never promoted to authority.
      this.queue = local && !local.id ? local : null;
    }
    this.saveQueue();
    this.renderQueueHistory();
  }

  async progressQueue(action, entryID) {
    if (!this.queue?.id) {
      this.queue.index++; // legacy tab-only queue, never a new queue
      this.queue.pending = null;
      return;
    }
    const queue = this.queue;
    const result = await this.post(`/queue/${encodeURIComponent(queue.id)}/progress`, {
      entry_id: entryID,
      action,
      if_revision: queue.revision,
      request_key: operationKey(`queue-${action}`)
    });
    this.queue = {
      ...this.queue,
      ...result.queue,
      created_at: Date.parse(result.queue.created_at),
      pending: null
    };
    this.saveQueue();
  }

  async startQueue(trigger) {
    if (
      this.queue ||
      this.readOnly ||
      this.selectedProjects.size < 2 ||
      this.selectedProjects.size > QUEUE_LIMIT
    )
      return;
    try {
      const { queue } = await this.post('/queue', {
        ids: [...this.selectedProjects],
        request_key: operationKey('queue-start')
      });
      this.queue = {
        ...queue,
        home_id: this.workspaceId,
        created_at: Date.parse(queue.created_at),
        pending: null
      };
      this.saveQueue();
      this.selectedProjects.clear();
      this.renderRows();
      await this.continueQueue(document.getElementById('projectLibraryQueueResume') || trigger);
    } catch (error) {
      await this.restoreQueue().catch(() => {});
      this.status(error.message);
      this.renderQueueControls();
    }
  }

  async discardQueue(trigger) {
    if (
      !this.queue ||
      this.busy ||
      !(await this.confirm(
        'Discard this review queue?',
        [
          'This discards the saved Home queue and its skip/order history. Any project you separately confirmed stays connected.'
        ],
        'Discard queue',
        trigger
      ))
    )
      return;
    try {
      if (this.queue.id) {
        await this.post(`/queue/${encodeURIComponent(this.queue.id)}/discard`, {
          if_revision: this.queue.revision,
          request_key: operationKey('queue-discard'),
          confirm: true
        });
      }
      this.queue = null;
      this.saveQueue();
      await this.restoreQueue();
      this.status('Saved review queue discarded. Connected projects were not changed.');
    } catch (error) {
      await this.restoreQueue().catch(() => {});
      this.status(error.message);
    }
  }

  // `offer` is the host's reviewed-integration offer for a song that cannot be
  // set up yet. Choosing it pauses the queue at this same song (no progress, no
  // creator, no review token); it resumes from here with a fresh review.
  queueChoice(name, position, count, trigger, reason = '', offer = null) {
    return new Promise(resolve => {
      const dialog = node('dialog', 'assistant-program-hire-dialog project-library-dialog');
      const form = node('form');
      form.method = 'dialog';
      const heading = node('h2', '', `Song ${position} of ${count} · ${name}`);
      heading.id = operationKey('queue-heading');
      dialog.setAttribute('aria-labelledby', heading.id);
      const actions = node('div', 'assistant-program-dialog-actions');
      const pause = node('button', 'modern-btn modern-btn-secondary', 'Pause queue');
      const skip = node('button', 'modern-btn modern-btn-secondary', 'Skip this song');
      const proceed = node('button', 'modern-btn modern-btn-primary', 'Review this song');
      pause.type = skip.type = 'button';
      proceed.type = 'submit';
      let choice = 'pause';
      pause.addEventListener('click', () => dialog.close());
      skip.addEventListener('click', () => {
        choice = 'skip';
        dialog.close();
      });
      form.addEventListener('submit', event => {
        if (reason) {
          event.preventDefault(); // an unavailable item can be skipped, never reviewed
          return;
        }
        choice = 'review';
      });
      actions.append(pause, skip);
      if (reason && offer?.quest_id && offer?.display_name) {
        const integration = node(
          'button',
          'modern-btn modern-btn-primary',
          `Review the ${offer.display_name} integration`
        );
        integration.type = 'button';
        integration.addEventListener('click', () => {
          choice = 'integration';
          dialog.close();
        });
        actions.append(integration);
      }
      if (!reason) actions.append(proceed);
      form.append(
        heading,
        node(
          'p',
          '',
          reason
            ? `${reason} Project setup is unavailable. Skip this song or pause the saved Home queue; neither action creates a project.${offer ? ' You can also review the integration; the queue stays paused on this song until you resume it.' : ''}`
            : 'This song needs its own authoritative-file choice and final confirmation. Skipping creates nothing; pausing keeps the saved Home queue.'
        ),
        actions
      );
      dialog.append(form);
      document.body.append(dialog);
      dialog.addEventListener(
        'close',
        () => {
          dialog.remove();
          trigger?.focus?.();
          resolve(choice);
        },
        { once: true }
      );
      dialog.showModal();
      pause.focus();
    });
  }

  async continueQueue(trigger) {
    if (!this.queue || this.readOnly || this.busy) return;
    await this.run(trigger, 'Checking the next saved project before review…', async () => {
      await this.restoreQueue();
      if (this.queue?.status === 'expired') {
        this.status(
          'Queue expired. Discard it and start a new review; no project was created by expiry.'
        );
        return;
      }
      if (!this.queue) return;
      while (this.queue && this.queue.index < this.queue.ids.length) {
        const id = this.queue.ids[this.queue.index];
        const path = `/projects/${encodeURIComponent(id)}`;
        try {
          let detail;
          try {
            detail = await this.request(path);
          } catch (error) {
            if (error.status !== 404) throw error;
            // An explicit Home forget may remove the current catalog record.
            // It cannot silently abandon later queued songs: only a new Skip
            // gesture may advance, and no review/creator path is offered.
            const action = await this.queueChoice(
              'No longer in this Home library',
              this.queue.index + 1,
              this.queue.ids.length,
              trigger,
              'This saved catalog record was removed.'
            );
            if (action === 'pause') {
              this.status('Review queue paused; no other song was connected.');
              return;
            }
            await this.progressQueue('skip', id);
            if (this.queue?.index === this.queue.ids.length) {
              this.queue = null;
              this.saveQueue();
              await this.restoreQueue();
              this.status('Review queue complete. Only separately confirmed songs were connected.');
              return;
            }
            this.saveQueue();
            continue;
          }
          if (detail.row.connection === 'connected') {
            await this.progressQueue('connected', id);
          } else if (this.queue.pending) {
            await this.post(`${path}/activation/commit`, {
              review_token: this.queue.pending.token,
              idempotency_key: this.queue.pending.key,
              confirm: true
            });
            await this.progressQueue('connected', id);
            await this.refresh();
          } else {
            const eligibility = await this.request(`${path}/activation`);
            const canReview = ['review_available', 'file_choice_required'].includes(
              eligibility.state
            );
            const action = await this.queueChoice(
              detail.row.name,
              this.queue.index + 1,
              this.queue.ids.length,
              trigger,
              canReview ? '' : eligibility.reason || 'Project setup needs a fresh review.',
              canReview ? null : eligibility.integration_offer || null
            );
            if (action === 'pause') {
              this.status('Review queue paused; no other song was connected.');
              return;
            }
            if (action === 'integration' && eligibility.integration_offer) {
              // The queue stays exactly here: same song, no progress, no creator.
              this.status(
                'Review queue paused on this song while you review the integration. Resume it afterwards for a fresh check.'
              );
              this.startIntegrationReview(detail, eligibility.integration_offer);
              return;
            }
            if (action === 'skip') {
              await this.progressQueue('skip', id);
            } else if (!canReview) {
              this.status(`${detail.row.name}: project setup is unavailable. Queue paused.`);
              return;
            } else {
              const input = await this.activationInput(detail, eligibility, trigger);
              if (!input) {
                this.status('Review queue paused before project setup.');
                return;
              }
              const review = await this.post(`${path}/activation/review`, input);
              if (
                !(await this.confirm(
                  'Connect this one project?',
                  [
                    `Queue item ${this.queue.index + 1} of ${this.queue.ids.length}: ${review.workspace_name}`,
                    `Authoritative file: ${review.project_file}`,
                    `Installed project roles: ${(review.project_role_labels || []).join(', ')}`,
                    review.statement,
                    'Other queued songs remain untouched until separately confirmed.'
                  ],
                  'Connect project',
                  trigger
                ))
              ) {
                this.status('Review queue paused without connecting this song.');
                return;
              }
              this.queue.pending = {
                id,
                token: review.token,
                key: operationKey('queue-activation')
              };
              if (!this.saveQueue()) {
                throw new Error(
                  'This tab cannot retain a confirmed creator retry key; no project was created. Use one-project review instead.'
                );
              }
              await this.post(`${path}/activation/commit`, {
                review_token: review.token,
                idempotency_key: this.queue.pending.key,
                confirm: true
              });
              await this.progressQueue('connected', id);
              await this.refresh();
            }
          }
          if (!this.queue || this.queue.index === this.queue.ids.length) {
            this.queue = null;
            this.saveQueue();
            await this.restoreQueue();
            this.status('Review queue complete. Only separately confirmed songs were connected.');
            if (typeof document !== 'undefined')
              document.getElementById('projectLibraryStatus')?.focus();
            return;
          }
          this.saveQueue();
        } catch (error) {
          if (this.queue?.pending) {
            // A lost HTTP reply does not prove the creator failed. Refresh
            // the saved link projection before showing the paused queue;
            // resume still reconciles the exact confirmed operation key.
            try {
              await this.refresh();
            } catch (_) {
              // Keep the retry key. The next explicit resume rechecks it.
            }
            this.status(
              `${error.message || 'The result is uncertain'}. Review the saved link above. Queue paused; resume with this tab’s confirmed key. If this tab closes after a child was created but before the Home recorded it, return to this Home’s Review linked projects shelf to separately review its exact reciprocal link. Home queue order and skips survive tab closure; only this tab’s confirmed retry key does not.`
            );
          } else {
            this.status(
              `${error.message || 'Project setup needs a fresh review.'} Queue paused; previously confirmed projects remain connected.`
            );
          }
          return;
        }
      }
    });
  }

  confirm(title, lines, action, trigger, { destructive = false } = {}) {
    return new Promise(resolve => {
      const dialog = node('dialog', 'assistant-program-hire-dialog project-library-dialog');
      const form = node('form');
      form.method = 'dialog';
      const heading = node('h2', '', title);
      heading.id = operationKey('heading');
      dialog.setAttribute('aria-labelledby', heading.id);
      const list = node('ul');
      for (const line of lines) list.append(node('li', '', line));
      const actions = node('div', 'assistant-program-dialog-actions');
      const cancel = node('button', 'modern-btn modern-btn-secondary', 'Cancel');
      cancel.type = 'button';
      const accept = node(
        'button',
        `modern-btn ${destructive ? 'project-library-destructive' : 'modern-btn-primary'}`,
        action
      );
      accept.type = 'submit';
      actions.append(cancel, accept);
      form.append(heading, list, actions);
      dialog.append(form);
      document.body.append(dialog);
      let accepted = false;
      cancel.addEventListener('click', () => dialog.close());
      form.addEventListener('submit', () => {
        accepted = true;
      });
      dialog.addEventListener(
        'close',
        () => {
          dialog.remove();
          trigger?.focus?.();
          resolve(accepted);
        },
        { once: true }
      );
      dialog.showModal();
      cancel.focus();
    });
  }

  // announced: the setup card's button already said it starts the library (for a
  // collection the person chose), and the folder's own review follows, so this
  // step needs no dialog of its own. Any other entry still asks first.
  async initialize(trigger, { announced = false } = {}) {
    await this.run(trigger, 'Preparing your saved project links for review…', async () => {
      const review = await this.post('/initialize/review');
      if (
        !announced &&
        !(await this.confirm(
          'Start a Home project library?',
          [
            `${review.linked_count} exact linked projects will be represented without creating a workspace or scanning a folder.`,
            'Saved user notes are copied once; existing project files stay unchanged.'
          ],
          'Start library',
          trigger
        ))
      )
        return;
      await this.post('/initialize/commit', {
        review_token: review.token,
        idempotency_key: operationKey('initialize'),
        confirm: true
      });
      await this.refresh();
    });
    if (this.offerID && this.state?.initialized === true)
      await this.addFolder(trigger, this.offerID);
  }

  // The carried collection has been used: forget it and drop it from the address
  // so a reload does not offer it again.
  consumeOffer(offerID) {
    this.offerID = '';
    if (offerID && globalThis.history?.replaceState && globalThis.location?.href) {
      const url = new URL(globalThis.location.href);
      url.searchParams.delete('folder_offer_id');
      globalThis.history.replaceState(globalThis.history.state, '', url);
    }
  }

  async addFolder(trigger, offerID = this.offerID) {
    await this.run(trigger, 'Preparing the folder selection…', async () => {
      let picked;
      if (offerID) {
        try {
          picked = await this.post('/roots/pick-offer', { offer_id: offerID });
        } catch (error) {
          // Ask the server why, rather than telling every failure "expired or
          // changed". Package/Home/picker problems are not fixed by a new pick.
          await this.restoreCollectionContinuation({ adopt: false });
          const recovery = selectionRecovery({
            reason: error.reason,
            continuation: this.continuations.find(item => item.offer_id === offerID) || null
          });
          if (!recovery.repick) {
            this.status(recovery.message);
            return;
          }
          if (
            !(await this.confirm(
              'Choose the folder again?',
              [recovery.message],
              'Open folder picker',
              trigger
            ))
          )
            return;
        }
      }
      if (!picked) picked = await this.post('/roots/pick');
      if (picked.cancelled) {
        this.status('No folder was chosen. Nothing was connected or read.');
        return;
      }
      if (picked.existing_root_id) {
        // This Home already approved the folder. A second root review would end
        // in a refused duplicate after the user confirmed it, so continue at that
        // root's own scan review. The offer is spent: nothing else to grant.
        this.consumeOffer(offerID);
        await this.refresh();
        this.status('This folder is already connected to your Home. Review a fresh scan of it.');
        await this.scanRootFlow(picked.existing_root_id, trigger);
        return;
      }
      const review = await this.post('/roots/review', {
        selection_token: picked.selection_token,
        if_revision: this.state.revision
      });
      if (
        !(await this.confirm(
          'Connect this discovery folder?',
          [
            review.root_path,
            review.scope,
            'Connecting permits metadata-only reads; it does not start a scan, open a session, or activate a project.'
          ],
          'Connect folder',
          trigger
        ))
      ) {
        // The inert review still advanced the Home document revision; without a
        // refresh the next attempt would send a stale one and be refused.
        await this.refresh();
        this.status('Folder not connected. Nothing was granted or read.');
        return;
      }
      const receipt = await this.post('/roots/commit', {
        review_token: review.token,
        idempotency_key: operationKey('root'),
        confirm: true
      });
      this.consumeOffer(offerID);
      await this.refresh();
      // The next review follows the refreshed revision of the grant just made.
      // The scan review is itself the disclosure and confirmation (folder,
      // bounds, cancel), so a separate "scan now?" question only added a stop.
      // Cancelling it keeps the connected root; the setup card then offers the
      // scan again without another folder pick.
      await this.scanRootFlow(receipt.root_id, trigger);
    });
  }

  async knownScopes(root, trigger) {
    await this.run(trigger, 'Loading previously observed folders…', async () => {
      const dialog = node('dialog', 'assistant-program-hire-dialog project-library-dialog');
      const heading = node('h2', '', 'Choose a smaller folder');
      heading.id = operationKey('scope-heading');
      dialog.setAttribute('aria-labelledby', heading.id);
      const explanation = node(
        'p',
        '',
        'These names were observed during an earlier scan. Choosing one only opens a separate metadata-scan review; unavailable folders cannot be scanned.'
      );
      const choices = node('ul', 'project-library-scope-list');
      const note = node('p', '', '');
      note.setAttribute('role', 'status');
      const actions = node('div', 'assistant-program-dialog-actions');
      const more = node('button', 'modern-btn modern-btn-secondary', 'More folders');
      more.type = 'button';
      more.hidden = true;
      const close = node('button', 'modern-btn modern-btn-secondary', 'Close');
      close.type = 'button';
      close.addEventListener('click', () => dialog.close());
      actions.append(more, close);
      dialog.append(heading, explanation, choices, note, actions);
      let nextOffset = 0;
      const load = async offset => {
        const page = await this.request(
          `/roots/${encodeURIComponent(root.id)}/scopes?${new URLSearchParams({ if_revision: String(this.state.revision), offset: String(offset) })}`
        );
        for (const choice of page.rows || []) {
          const item = node('li');
          const select = node('button', 'modern-btn modern-btn-secondary', choice.relative_folder);
          select.type = 'button';
          select.addEventListener('click', () => {
            dialog.close();
            void this.scanRoot(root, trigger, choice.id);
          });
          item.append(select);
          choices.append(item);
        }
        nextOffset = page.next_offset || 0;
        more.hidden = !nextOffset;
        note.textContent = `${choices.children.length} of ${page.total} previously observed folders`;
      };
      await load(0);
      more.addEventListener('click', async () => {
        more.disabled = true;
        try {
          await load(nextOffset);
        } catch (error) {
          note.textContent = `${error.message || 'The library changed.'} Close and retry.`;
          more.hidden = true;
        } finally {
          more.disabled = false;
        }
      });
      document.body.append(dialog);
      dialog.addEventListener(
        'close',
        () => {
          dialog.remove();
          trigger?.focus?.();
        },
        { once: true }
      );
      dialog.showModal();
      close.focus();
    });
  }

  async scanRoot(root, trigger, scopeID = '') {
    await this.run(trigger, 'Preparing a one-time metadata scan review…', () =>
      this.scanRootFlow(root.id, trigger, scopeID)
    );
  }

  async scanRootFlow(rootID, trigger, scopeID = '') {
    const recordedScan = () =>
      (this.state?.roots || []).find(item => item.id === rootID)?.last_scan || null;
    const priorScanID = recordedScan()?.id || '';
    const review = await this.post(`/roots/${encodeURIComponent(rootID)}/scans/review`, {
      if_revision: this.state.revision,
      ...(scopeID ? { scope_id: scopeID } : {})
    });
    if (
      !(await this.confirm(
        scopeID ? 'Scan this selected folder once?' : 'Scan this discovery folder once?',
        [
          review.root_path,
          ...(scopeID ? [review.relative_folder] : []),
          review.scope,
          `At most ${review.max_entries} entries. Partial or skipped coverage will remain visible.`,
          ...(review.interrupts_prior_scan
            ? ['This also marks an interrupted prior scan as interrupted.']
            : [])
        ],
        'Scan metadata',
        trigger
      ))
    ) {
      await this.refresh(); // The inert review still advances the Home document revision.
      this.status('Scan canceled. The folder was not read.');
      return;
    }
    const idempotencyKey = operationKey('scan');
    const commit = () =>
      this.post(`/roots/${encodeURIComponent(rootID)}/scans/commit`, {
        review_token: review.token,
        idempotency_key: idempotencyKey,
        confirm: true,
        ...(scopeID ? { scope_id: scopeID } : {})
      });
    let receipt;
    try {
      receipt = await commit();
    } catch (error) {
      // A definite refusal (4xx) is final. No answer at all (a dropped
      // connection) or a server error is uncertain: the scan may have been
      // recorded. The same review and key are a replay on the server, which
      // returns the scan it already recorded and never scans twice.
      if (!(error.status === undefined || error.status >= 500)) throw error;
      try {
        receipt = await commit();
      } catch (again) {
        await this.refresh();
        const seen = recordedScan();
        if (seen && seen.id !== priorScanID) {
          this.status(
            `The reply was lost, but this scan was recorded: ${label(seen.status)} · ${seen.entries_seen} names checked. It was not repeated.`
          );
          return;
        }
        throw again;
      }
    }
    await this.refresh();
    this.status(
      `${label(receipt.status)} scan · ${receipt.entries_seen} entries seen · ${receipt.skipped_links + receipt.skipped_other} skipped${receipt.partial_reason ? ` · ${receipt.partial_reason}` : ''}.`
    );
  }

  async revokeRoot(root, trigger) {
    await this.run(trigger, 'Preparing root disconnect impact…', async () => {
      const review = await this.post(`/roots/${encodeURIComponent(root.id)}/revoke/review`, {
        if_revision: this.state.revision
      });
      if (
        !(await this.confirm(
          'Disconnect this discovery folder?',
          [
            root.path,
            `${review.entry_count} catalog records retain their historical notes.`,
            'Further scans stop. Already connected projects and external source files remain unchanged.'
          ],
          'Disconnect folder',
          trigger
        ))
      ) {
        await this.refresh(); // A canceled review is not a revoke, but it changes the revision.
        this.status('Disconnect canceled. Discovery consent is unchanged.');
        return;
      }
      await this.post(`/roots/${encodeURIComponent(root.id)}/revoke/commit`, {
        review_token: review.token,
        idempotency_key: operationKey('revoke'),
        confirm: true
      });
      await this.refresh();
    });
  }

  // Sends the person to the host's reviewed install quest for this song's
  // integration, remembering only where to come back to. The quest ID comes from
  // the server's offer (validated again by the quest link builder), never from
  // the row; the hint is navigation only and grants nothing.
  startIntegrationReview(detail, offer) {
    let target;
    try {
      target = setupQuestURL({ source: 'host', id: offer.quest_id });
    } catch (_) {
      this.status('That integration review is unavailable right now. Nothing was changed.');
      return;
    }
    const remembered = writeLibraryReturn({
      homeID: this.workspaceId,
      entryID: detail.row.id,
      questID: offer.quest_id,
      path: globalThis.location?.pathname || ''
    });
    if (!remembered) {
      this.status(
        'Ori could not remember where to bring you back to. Open the integration from the Plugins page instead; nothing was changed.'
      );
      return;
    }
    globalThis.location.assign(target);
  }

  // Back from the integration review (or the Plugins page): reopen the same song
  // so it shows its fresh eligibility. The hint names the Home and an opaque
  // song ID and is checked against this exact Home; the song, its source and
  // its eligibility are re-read from the server like any other click. Nothing is
  // created, no folder grant is restored, and no application is launched.
  async resumeFromIntegration() {
    const hint = readLibraryReturn();
    if (!hint || hint.home_id !== this.workspaceId) return;
    clearLibraryReturn(); // one return per hint; a reload never repeats it
    try {
      await this.request(`/projects/${encodeURIComponent(hint.entry_id)}`);
    } catch (error) {
      this.status(
        error.status === 404
          ? 'The song you were checking is no longer in this library. Nothing was changed.'
          : 'Ori could not re-check that song just now. Open it from the shelf to check again.'
      );
      return;
    }
    await this.details(hint.entry_id, null);
    // After details(): its own progress message would otherwise replace this one.
    this.status('Back on your song. Its integration status was checked again.');
  }

  async details(entryID, trigger) {
    await this.run(trigger, 'Loading this saved project…', async () => {
      const detail = await this.request(`/projects/${encodeURIComponent(entryID)}`);
      // Eligibility is an inert, fresh installed-provider/source check. A
      // failed eligibility read must not hide saved notes or offer setup.
      const activation = await this.request(
        `/projects/${encodeURIComponent(entryID)}/activation`
      ).catch(() => ({
        reason: 'Setup options cannot be checked right now. Saved notes remain available.'
      }));
      const sessions = await this.request(
        `/projects/${encodeURIComponent(entryID)}/sessions`
      ).catch(() => null);
      const dialog = node(
        'dialog',
        'assistant-program-hire-dialog project-library-dialog project-library-detail-dialog'
      );
      const heading = node('h2', '', detail.row.name);
      heading.id = operationKey('detail');
      dialog.setAttribute('aria-labelledby', heading.id);
      const summary = node(
        'p',
        '',
        `${label(detail.row.stage)} · ${label(detail.row.status)} · ${label(detail.row.connection)} · ${label(detail.row.availability || detail.row.last_observed_availability)}`
      );
      const sourceList = node('ul');
      for (const source of detail.sources || [])
        sourceList.append(
          node(
            'li',
            '',
            `${source.relative_folder || 'Selected root'} · ${label(source.format)} · ${label(source.last_observed_availability)}${source.total_alternates ? ` · ${source.total_alternates} project-file choices (selection requires a separate review)` : ''}`
          )
        );
      if (!sourceList.children.length)
        sourceList.append(
          node('li', '', 'An exact existing link is recorded; no folder was scanned.')
        );
      const sourceCount =
        (detail.total_sources || 0) > (detail.sources || []).length
          ? node(
              'p',
              '',
              `Showing ${(detail.sources || []).length} of ${detail.total_sources} observed sources. Additional source history is not shown here.`
            )
          : null;
      const next = node('p', '', `Next action: ${detail.fields.next_action || 'Not set'}`);
      const setup = node('section', 'project-library-setup-preview');
      setup.append(
        node('h3', '', 'Project setup'),
        node('p', 'project-library-state', activationStateLabel(activation.state)),
        node('p', '', activation.reason),
        node(
          'p',
          'project-library-note',
          'Based on the last scan and the integrations installed now. No project application was opened or checked.'
        )
      );
      if (activation.state === 'project_provider_unavailable') {
        // The host offers a reviewed integration only when it is the honest
        // remedy for this song's observed format on this computer. Formats no
        // reviewed integration supports get no install offer, only notes and
        // session planning below.
        const offer = activation.integration_offer;
        const remedies = node('div', 'project-library-remedies');
        if (offer?.quest_id && offer?.display_name && !this.readOnly) {
          const review = node(
            'button',
            'modern-btn modern-btn-primary',
            `Review the ${offer.display_name} integration`
          );
          review.type = 'button';
          review.title =
            'Opens the reviewed install. Nothing is installed or connected until you confirm it there; you return to this song afterwards.';
          review.addEventListener('click', () => this.startIntegrationReview(detail, offer));
          remedies.append(review);
        }
        const plugins = node('a', '', 'Review integrations on the Plugins page');
        plugins.href = '/plugins';
        remedies.append(plugins);
        setup.append(remedies);
      }
      if (activation.project_role_labels?.length)
        setup.append(
          node('p', '', `Installed project roles: ${activation.project_role_labels.join(', ')}`)
        );
      if (activation.project_files?.length)
        setup.append(
          node(
            'p',
            '',
            `Observed project files: ${activation.project_files.join(', ')}. File selection and project creation require a separate review.`
          )
        );
      const sessionPanel = node('section', 'project-library-session-history');
      sessionPanel.append(node('h3', '', 'Studio sessions'));
      const sessionList = node('ul');
      if (!sessions)
        sessionPanel.append(node('p', '', 'Saved session history is unavailable right now.'));
      else if (!sessions.total)
        sessionPanel.append(
          node(
            'p',
            '',
            'No saved goals yet. Plan a session without connecting a project or starting a task.'
          )
        );
      else {
        const appendSessions = records => {
          for (const record of records) {
            const item = node('li');
            item.append(
              node('strong', '', record.goal),
              node(
                'small',
                '',
                `Saved by ${record.author} · ${new Date(record.updated_at).toLocaleDateString()}`
              ),
              node(
                'p',
                '',
                record.recap
                  ? `${record.shared_from_project ? 'User-authored linked-project recap' : 'User recap'}: ${record.recap}`
                  : 'No recap saved yet.'
              )
            );
            if (record.next_action)
              item.append(node('p', '', `Saved session next action: ${record.next_action}`));
            const full = node('button', 'modern-btn modern-btn-secondary', 'View full session');
            full.type = 'button';
            full.addEventListener('click', () => {
              dialog.close();
              void this.fullStudioSession(detail, record, trigger);
            });
            item.append(full);
            if (!record.recap && !this.readOnly) {
              const wrap = node('button', 'modern-btn modern-btn-secondary', 'Wrap up session');
              wrap.type = 'button';
              wrap.addEventListener('click', () => {
                dialog.close();
                this.sessionForm(detail, record, trigger);
              });
              item.append(wrap);
            }
            sessionList.append(item);
          }
        };
        appendSessions(sessions.rows);
        sessionPanel.append(sessionList);
        if (sessions.next_offset) {
          const more = node('button', 'modern-btn modern-btn-secondary', 'Load older sessions');
          more.type = 'button';
          let offset = sessions.next_offset;
          more.addEventListener(
            'click',
            () =>
              void this.run(more, 'Loading older studio sessions…', async () => {
                const page = await this.request(
                  `/projects/${encodeURIComponent(entryID)}/sessions?revision=${sessions.revision}&offset=${offset}`
                );
                appendSessions(page.rows);
                offset = page.next_offset;
                more.hidden = !offset;
              })
          );
          sessionPanel.append(more);
        }
      }
      const actions = node('div', 'assistant-program-dialog-actions');
      const close = node('button', 'modern-btn modern-btn-secondary', 'Close');
      close.type = 'button';
      close.addEventListener('click', () => dialog.close());
      actions.append(close);
      if (
        activation.state === 'connected' &&
        detail.row.connection === 'connected' &&
        activation.workspace_id
      ) {
        // Routed by slug; with no resolvable page there is no link to offer.
        const route = await this.workspaceRoute(activation.workspace_id);
        if (route) {
          const open = node('a', 'modern-btn modern-btn-secondary', 'Open connected workspace');
          open.href = route;
          actions.append(open);
        }
        if (!this.readOnly) {
          const showFolder = node(
            'button',
            'modern-btn modern-btn-secondary',
            'Show project folder'
          );
          showFolder.type = 'button';
          showFolder.title =
            'Show the connected project folder on this computer; do not open the project or start a DAW';
          showFolder.addEventListener('click', () => {
            void this.showConnectedFolder(entryID, activation.workspace_id, showFolder);
          });
          actions.append(showFolder);
        }
      }
      if (
        !this.readOnly &&
        ['review_available', 'file_choice_required'].includes(activation.state)
      ) {
        const activate = node('button', 'modern-btn modern-btn-primary', 'Review project setup');
        activate.type = 'button';
        activate.addEventListener('click', () => {
          dialog.close();
          this.activationForm(detail, activation, trigger);
        });
        actions.append(activate);
      }
      if (
        detail.row.connection === 'catalog_only' &&
        (detail.row.last_observed_availability || detail.row.availability) === 'revoked_source'
      ) {
        const forget = node(
          'button',
          'modern-btn modern-btn-secondary',
          'Review forgetting this Home record'
        );
        forget.type = 'button';
        forget.addEventListener('click', () => {
          dialog.close();
          void this.forgetRecord(detail, trigger);
        });
        actions.append(forget);
      }
      if (!this.readOnly) {
        const plan = node('button', 'modern-btn modern-btn-secondary', 'Plan a session');
        plan.type = 'button';
        plan.addEventListener('click', () => {
          dialog.close();
          this.sessionForm(detail, null, trigger);
        });
        actions.append(plan);
        const edit = node('button', 'modern-btn modern-btn-primary', 'Edit project notes');
        edit.type = 'button';
        edit.addEventListener('click', () => {
          dialog.close();
          void this.editFields(detail, trigger);
        });
        actions.append(edit);
      }
      dialog.append(
        heading,
        summary,
        sourceList,
        ...(sourceCount ? [sourceCount] : []),
        next,
        setup,
        sessionPanel,
        actions
      );
      document.body.append(dialog);
      dialog.addEventListener(
        'close',
        () => {
          dialog.remove();
          if (!document.querySelector('dialog[open]')) trigger?.focus?.();
        },
        { once: true }
      );
      dialog.showModal();
      close.focus();
    });
  }

  async forgetRecord(detail, trigger) {
    await this.run(trigger, 'Reviewing the saved Home record and its sessions…', async () => {
      const id = encodeURIComponent(detail.row.id);
      const review = await this.post(`/projects/${id}/forget/review`, {
        revision: detail.revision
      });
      if (
        !(await this.confirm(
          `Forget ${review.project_name} from this Home?`,
          [
            `${review.source_count} historical discovery source(s) will be removed from this Home record.`,
            `${review.session_count} saved studio session(s) and this project's Home notes will be erased.`,
            ...(review.queued_at
              ? [
                  `This is song ${review.queued_at} in the saved review queue. The queue stays in order, but setup for this record becomes unavailable; you must separately skip it or discard the queue.`
                ]
              : []),
            'Project files, other catalog records, discovery root grants and any separate workspace are not deleted. This cannot be undone.'
          ],
          'Forget saved Home record',
          trigger,
          { destructive: true }
        ))
      ) {
        await this.refresh();
        this.status('Forget canceled. Your saved project record is unchanged.');
        return;
      }
      await this.post(`/projects/${id}/forget/commit`, {
        review_token: review.token,
        idempotency_key: operationKey('forget-entry'),
        confirm: true
      });
      await this.refresh();
      this.status(
        `${review.project_name} was forgotten from this Home. Source files and grants were not changed.`
      );
    });
  }

  async fullStudioSession(detail, session, trigger) {
    await this.run(trigger, 'Loading the saved studio session…', async () => {
      const record = await this.request(
        `/projects/${encodeURIComponent(detail.row.id)}/sessions/${encodeURIComponent(session.id)}`
      );
      const dialog = node(
        'dialog',
        'assistant-program-hire-dialog project-library-dialog project-library-detail-dialog'
      );
      const heading = node('h2', '', `Studio session · ${detail.row.name}`);
      heading.id = operationKey('full-session');
      dialog.setAttribute('aria-labelledby', heading.id);
      const facts = node('div', 'project-library-session-history');
      facts.append(
        node('p', '', `User goal: ${record.goal}`),
        node('p', '', `Desired outcome: ${record.outcome || 'Not specified'}`),
        node(
          'p',
          '',
          `Available time: ${record.time_minutes ? `${record.time_minutes} minutes` : 'Not specified'}`
        ),
        node('p', '', `Planned date: ${record.planned_date || 'Not specified'}`),
        node('p', '', `User-entered actual date: ${record.actual_date || 'Not specified'}`),
        node(
          'p',
          '',
          `${record.shared_from_project ? 'User-authored linked-project recap' : 'Saved recap'}: ${record.recap || 'No recap yet'}`
        ),
        node('p', '', `Session next action: ${record.next_action || 'Not set'}`),
        node(
          'small',
          '',
          `User-authored by ${record.author} · saved ${new Date(record.updated_at).toLocaleDateString()}`
        )
      );
      if (record.handoff_citation) {
        facts.append(
          node(
            'p',
            '',
            `Prior Home handoff: Ticket #${record.handoff_citation.ticket_number || '?'} (${record.handoff_citation.ticket_id}), recorded ${new Date(record.handoff_citation.recorded_at).toLocaleDateString()}. Historical receipt only; current Ticket status and completion have not been checked.`
          )
        );
      }
      for (const [title, values] of [
        ['Decisions', record.decisions || []],
        ['Blockers', record.blockers || []]
      ]) {
        if (!values.length) continue;
        const list = node('ul');
        for (const value of values) list.append(node('li', '', value));
        facts.append(node('h3', '', title), list);
      }
      const close = node('button', 'modern-btn modern-btn-secondary', 'Close');
      close.type = 'button';
      close.addEventListener('click', () => dialog.close());
      dialog.append(heading, facts, close);
      document.body.append(dialog);
      dialog.addEventListener(
        'close',
        () => {
          dialog.remove();
          if (!document.querySelector('dialog[open]')) trigger?.focus?.();
        },
        { once: true }
      );
      dialog.showModal();
      close.focus();
    });
  }

  activationForm(detail, eligibility, trigger) {
    void this.activationInput(detail, eligibility, trigger).then(input => {
      if (input) void this.saveActivation(detail, input, trigger);
    });
  }

  activationInput(detail, eligibility, trigger) {
    return new Promise(resolve => {
      const dialog = node('dialog', 'assistant-program-hire-dialog project-library-dialog');
      const form = node('form');
      const heading = node('h2', '', `Set up ${detail.row.name}`);
      heading.id = operationKey('activate-heading');
      dialog.setAttribute('aria-labelledby', heading.id);
      const fields = node('div', 'project-library-fields');
      const name = node('input', 'form-control');
      name.required = true;
      name.maxLength = 128;
      name.value = detail.row.name.slice(0, 128);
      const nameLabel = node('label', '', 'Project workspace name');
      nameLabel.append(name);
      fields.append(nameLabel);
      const choice = node('select', 'form-select');
      choice.required = true;
      if ((eligibility.project_files || []).length !== 1) {
        const placeholder = node('option', '', 'Choose the authoritative file…');
        placeholder.value = '';
        placeholder.disabled = true;
        placeholder.selected = true;
        choice.append(placeholder);
      }
      for (const filename of eligibility.project_files || []) {
        const option = node('option', '', filename);
        option.value = filename;
        choice.append(option);
      }
      const fileLabel = node('label', '', 'Authoritative project file');
      fileLabel.append(choice);
      fields.append(fileLabel);
      const actions = node('div', 'assistant-program-dialog-actions');
      const cancel = node('button', 'modern-btn modern-btn-secondary', 'Cancel');
      cancel.type = 'button';
      cancel.addEventListener('click', () => dialog.close());
      const review = node('button', 'modern-btn modern-btn-primary', 'Review this project');
      review.type = 'submit';
      actions.append(cancel, review);
      form.append(
        heading,
        node(
          'p',
          '',
          'One saved song only. The selected file remains in place. No agent, live connection or file-opening action is granted here.'
        ),
        fields,
        actions
      );
      dialog.append(form);
      document.body.append(dialog);
      let input = null;
      dialog.addEventListener(
        'close',
        () => {
          dialog.remove();
          if (!document.querySelector('dialog[open]')) trigger?.focus?.();
          resolve(input);
        },
        { once: true }
      );
      form.addEventListener('submit', event => {
        event.preventDefault();
        input = {
          workspace_name: name.value.trim(),
          project_file: choice.value,
          if_revision: detail.revision
        };
        dialog.close();
      });
      dialog.showModal();
      choice.focus();
    });
  }

  // A workspace page is routed by its folder slug, never its ID: /workspaces/<id>
  // is a 404. Resolve the slug from the canonical workspace read. Returns '' when
  // it cannot be resolved, so a caller shows no link rather than a dead one.
  async workspaceRoute(workspaceID) {
    return (await this.workspacePage(workspaceID)).route;
  }

  // One read of the canonical workspace gives both facts a link to it needs: its
  // page route and whether it has chosen how it works (its workspace mode). A
  // project that has not chosen a mode opens its own mode wizard first, so its
  // team form must not be requested on top of that.
  async workspacePage(workspaceID) {
    const none = { route: '', modeChosen: false };
    if (!workspaceID) return none;
    try {
      const response = await this.fetchImpl(`/api/workspaces/${encodeURIComponent(workspaceID)}`, {
        headers: { Accept: 'application/json' }
      });
      if (!response.ok) return none;
      const workspace = await payload(response);
      const slug = String(workspace?.folder_slug || '').trim();
      return {
        route: slug ? `/workspaces/${encodeURIComponent(slug)}` : '',
        modeChosen: String(workspace?.runtime_state?.selected_mode_id || '').trim() !== ''
      };
    } catch (_) {
      return none;
    }
  }

  // The connected project's own roster, read from the canonical project route
  // (not the library's), turned into the link that opens its team setup form.
  // Best effort: any failure just means the plain project link, never an error
  // after a project was already connected.
  async projectTeamLink(workspaceID, route) {
    try {
      const response = await this.fetchImpl(
        `/api/workspaces/${encodeURIComponent(workspaceID)}/roles`,
        { headers: { Accept: 'application/json' } }
      );
      return projectTeamURL(route, response.ok ? await payload(response) : null);
    } catch (_) {
      return projectTeamURL(route, null);
    }
  }

  async saveActivation(detail, input, trigger) {
    await this.run(trigger, 'Preparing one project for review…', async () => {
      const path = `/projects/${encodeURIComponent(detail.row.id)}/activation`;
      const review = await this.post(`${path}/review`, input);
      if (
        !(await this.confirm(
          'Connect this one project?',
          [
            `Project: ${review.workspace_name}`,
            `Authoritative file: ${review.project_file}`,
            `Installed project roles: ${(review.project_role_labels || []).join(', ')}`,
            review.statement,
            'No other catalog projects are created. Staffing and live project access are separate choices.'
          ],
          'Connect project',
          trigger
        ))
      )
        return;
      const result = await this.post(`${path}/commit`, {
        review_token: review.token,
        idempotency_key: operationKey('activation'),
        confirm: true
      });
      await this.refresh();
      this.status(
        'One project connected. Open its workspace to review project roles and File-only setup.'
      );
      const dialog = node('dialog', 'assistant-program-hire-dialog project-library-dialog');
      const form = node('form');
      const heading = node('h2', '', `${review.workspace_name} is connected`);
      heading.id = operationKey('connected-project');
      dialog.setAttribute('aria-labelledby', heading.id);
      // The project's team is the next step and its own review. Point straight at
      // its setup form when the project's roster has an empty role to fill.
      const page = await this.workspacePage(result.workspace_id);
      const route = page.route;
      // The team form is requested directly only once the project has chosen how
      // it works. Until then its page opens the mode wizard first (File-only is
      // the starting option), and the team is set up right after.
      const teamURL =
        route && page.modeChosen ? await this.projectTeamLink(result.workspace_id, route) : '';
      const hasTeamStep = teamURL.includes('?role=');
      const close = node('button', 'modern-btn modern-btn-secondary', 'Stay in library');
      close.type = 'button';
      close.addEventListener('click', () => dialog.close());
      const actions = node('div', 'assistant-program-dialog-actions');
      actions.append(close);
      // Routed by slug (an ID path is a 404). With no resolvable page there is no
      // link to offer, and the project stays reachable from the library shelf.
      if (route) {
        const open = node(
          'a',
          `modern-btn ${hasTeamStep ? 'modern-btn-secondary' : 'modern-btn-primary'}`,
          'Open project workspace'
        );
        open.href = route;
        actions.append(open);
      }
      if (hasTeamStep) {
        const team = node('a', 'modern-btn modern-btn-primary', 'Set up the project team');
        team.href = teamURL;
        actions.append(team);
      }
      form.append(
        heading,
        node(
          'p',
          '',
          hasTeamStep
            ? 'The saved folder is referenced, not copied or launched. No project role was staffed and no live access was granted; the project’s team is set up in its own review next.'
            : 'The saved folder is referenced, not copied or launched. No project role was staffed and no live access was granted. Open the project to choose how it works (File-only is the starting option); its team is set up in its own review right after.'
        ),
        actions
      );
      dialog.append(form);
      document.body.append(dialog);
      dialog.addEventListener(
        'close',
        () => {
          dialog.remove();
          if (!document.querySelector('dialog[open]')) trigger?.focus?.();
        },
        { once: true }
      );
      dialog.showModal();
      close.focus();
    });
  }

  sessionForm(detail, session, trigger, suggestedGoal = null, suggestedRecap = null) {
    const wrapping = Boolean(session);
    const dialog = node('dialog', 'assistant-program-hire-dialog project-library-dialog');
    const form = node('form');
    const heading = node('h2', '', wrapping ? 'Wrap up studio session' : 'Plan a studio session');
    heading.id = operationKey('studio-heading');
    dialog.setAttribute('aria-labelledby', heading.id);
    const fields = node('div', 'project-library-fields');
    const notes = node('textarea', 'form-control');
    notes.required = true;
    notes.maxLength = wrapping ? 2000 : 500;
    notes.rows = wrapping ? 5 : 3;
    if (!wrapping && suggestedGoal) notes.value = suggestedGoal.goal;
    if (wrapping && suggestedRecap) notes.value = suggestedRecap;
    const extra = node(wrapping ? 'input' : 'textarea', 'form-control');
    extra.maxLength = wrapping ? 240 : 500;
    if (!wrapping) extra.rows = 2;
    if (!wrapping && suggestedGoal) extra.value = suggestedGoal.desired_outcome || '';
    const date = node('input', 'form-control');
    date.type = 'date';
    const labeled = (text, input) => {
      const labelNode = node('label', '', text);
      labelNode.append(input);
      fields.append(labelNode);
    };
    labeled(wrapping ? 'Your recap' : 'Session goal', notes);
    labeled(wrapping ? 'Proposed next action' : 'Desired outcome', extra);
    labeled(wrapping ? 'Actual date (optional)' : 'Planned date (optional)', date);
    let time;
    let update;
    let share;
    let handoff;
    let decisions;
    let blockers;
    if (wrapping) {
      decisions = node('textarea', 'form-control');
      blockers = node('textarea', 'form-control');
      for (const control of [decisions, blockers]) {
        control.rows = 2;
        control.maxLength = 4096;
      }
      labeled('Decisions (optional, one per line; up to 16)', decisions);
      labeled('Blockers (optional, one per line; up to 16)', blockers);
      for (const control of [decisions, blockers, extra])
        control.addEventListener('input', () => control.setCustomValidity(''));
      update = node('input');
      update.type = 'checkbox';
      labeled(
        'Also update this project’s saved next action (separate from the session note)',
        update
      );
      if (detail.row.connection === 'connected') {
        share = node('input');
        share.type = 'checkbox';
        labeled(
          'Mark this Home recap as a user-authored summary of the exact linked project (no project files, chats or tasks are read)',
          share
        );
        handoff = node('select', 'form-select');
        handoff.disabled = true;
        const none = node('option', '', 'No handoff Ticket cited');
        none.value = '';
        handoff.append(none);
        labeled(
          'Optional prior Home handoff receipt (historical ID, not Ticket status or completion)',
          handoff
        );
        const hint = node(
          'p',
          'project-library-help',
          'Checking this Home’s saved handoff receipts…'
        );
        fields.append(hint);
        void this.request(`/projects/${encodeURIComponent(detail.row.id)}/handoff-receipts`)
          .then(result => {
            if (!dialog.isConnected) return;
            for (const receipt of result.rows || []) {
              const option = node(
                'option',
                '',
                `Ticket #${receipt.ticket_number || '?'} · ${receipt.ticket_id} · ${new Date(receipt.recorded_at).toLocaleDateString()}`
              );
              option.value = receipt.ticket_id;
              handoff.append(option);
            }
            handoff.disabled = !(result.rows || []).length;
            hint.textContent = handoff.disabled
              ? 'No prior confirmed Home handoff receipts for this exact project.'
              : 'A citation records only the prior Home handoff ID; it does not read or verify a child Ticket.';
          })
          .catch(() => {
            if (dialog.isConnected)
              hint.textContent =
                'Handoff receipts are unavailable. You can save this recap without a Ticket citation.';
          });
      }
    } else {
      time = node('input', 'form-control');
      time.type = 'number';
      time.min = '1';
      time.max = '480';
      time.placeholder = 'Optional';
      if (suggestedGoal?.time_minutes) time.value = String(suggestedGoal.time_minutes);
      labeled('Available minutes (optional, 1–480)', time);
    }
    const actions = node('div', 'assistant-program-dialog-actions');
    const cancel = node('button', 'modern-btn modern-btn-secondary', 'Cancel');
    cancel.type = 'button';
    cancel.addEventListener('click', () => dialog.close());
    const review = node('button', 'modern-btn modern-btn-primary', 'Review session');
    review.type = 'submit';
    actions.append(cancel, review);
    form.append(
      heading,
      node(
        'p',
        '',
        suggestedGoal
          ? 'Manager suggestion (untrusted draft). Edit it before your separate review and confirmation. Cancel saves no studio session, task or DAW work.'
          : suggestedRecap
            ? 'Manager suggestion (untrusted draft, not evidence of work). Edit and separately review/confirm it. No actual date, decisions, next action or project note was supplied. Cancel saves nothing.'
            : 'Your notes do not start a task, open a DAW, or prove work happened.'
      ),
      fields,
      actions
    );
    dialog.append(form);
    document.body.append(dialog);
    dialog.addEventListener(
      'close',
      () => {
        dialog.remove();
        if (!document.querySelector('dialog[open]')) trigger?.focus?.();
      },
      { once: true }
    );
    form.addEventListener('submit', event => {
      event.preventDefault();
      const lines = control =>
        control.value
          .split(/\r?\n/)
          .map(value => value.trim())
          .filter(Boolean);
      if (wrapping) {
        for (const control of [decisions, blockers]) {
          const items = lines(control);
          control.setCustomValidity(
            items.length > 16 || items.some(item => item.length > 240)
              ? 'Use at most 16 lines of up to 240 characters each.'
              : ''
          );
          if (!control.reportValidity()) return;
        }
        extra.setCustomValidity(
          update.checked && !extra.value.trim()
            ? 'Enter a next action to update the project note.'
            : ''
        );
        if (!extra.reportValidity()) return;
        if (handoff) {
          handoff.setCustomValidity(
            handoff.value && !share.checked
              ? 'Mark linked-project attribution separately to cite a prior Home handoff.'
              : ''
          );
          if (!handoff.reportValidity()) return;
        }
      }
      const input = wrapping
        ? {
            recap: notes.value.trim(),
            decisions: lines(decisions),
            blockers: lines(blockers),
            next_action: extra.value.trim(),
            actual_date: date.value,
            update_project_next_action: update.checked,
            share_linked_project: Boolean(share?.checked),
            handoff_ticket_id: handoff?.value || ''
          }
        : {
            goal: notes.value.trim(),
            desired_outcome: extra.value.trim(),
            planned_date: date.value,
            time_minutes: time.value ? Number(time.value) : 0
          };
      dialog.close();
      void this.saveStudioSession(detail, session, input, trigger);
    });
    dialog.showModal();
    notes.focus();
  }

  async saveStudioSession(detail, session, input, trigger) {
    await this.run(trigger, 'Preparing your studio session for review…', async () => {
      const base = `/projects/${encodeURIComponent(detail.row.id)}/sessions`;
      const path = session ? `${base}/${encodeURIComponent(session.id)}/recaps` : `${base}/goals`;
      const request = session
        ? {
            if_session_revision: session.revision,
            if_fields_revision: detail.row.fields_revision,
            recap: input
          }
        : { if_entry_revision: detail.entry_revision, goal: input };
      const preview = await this.post(`${path}/review`, request);
      if (
        session &&
        input.share_linked_project &&
        !preview.session?.shared_from_project?.workspace_id
      )
        throw new Error(
          'The exact linked project could not be verified. Reopen the current Home record.'
        );
      if (
        input.handoff_ticket_id &&
        preview.session?.handoff_citation?.ticket_id !== input.handoff_ticket_id
      )
        throw new Error('The saved handoff receipt changed. Reopen the current Home record.');
      const consequences = session
        ? [
            detail.row.name,
            `Recap: ${input.recap}`,
            ...input.decisions.map(value => `Decision: ${value}`),
            ...input.blockers.map(value => `Blocker: ${value}`),
            `Next action: ${input.next_action || 'None saved'}`,
            input.update_project_next_action
              ? 'Also replaces the project’s saved next action.'
              : 'The project’s saved next action is unchanged.',
            input.share_linked_project
              ? `Mark this user-written Home recap as a summary of linked workspace ${preview.session.shared_from_project.workspace_id}. No files, chats, tasks or DAW state were read. Disconnecting the child prevents new linked summaries but keeps this accepted note.`
              : 'Home-only note; it is not marked as a linked-project summary.',
            ...(input.handoff_ticket_id
              ? [
                  `Cite this Home’s earlier reviewed handoff: Ticket #${preview.session.handoff_citation.ticket_number || '?'} (${preview.session.handoff_citation.ticket_id}). This is a historical receipt, not current child status or proof of completion.`
                ]
              : [])
          ]
        : [
            detail.row.name,
            `Goal: ${input.goal}`,
            `Desired outcome: ${input.desired_outcome || 'Not specified'}`,
            'No project connection, task, or DAW operation is created.'
          ];
      if (
        !(await this.confirm(
          session ? 'Save this recap?' : 'Save this goal?',
          consequences,
          session ? 'Save recap' : 'Save goal',
          trigger
        ))
      )
        return;
      await this.post(`${path}/commit`, {
        ...request,
        review_token: preview.token,
        idempotency_key: operationKey(session ? 'recap' : 'goal'),
        confirm: true
      });
      await this.refresh();
      this.status(
        session
          ? input.share_linked_project
            ? 'User-authored Home recap saved with linked-project attribution. Return to Details to resume.'
            : 'Recap saved. Return to Details to resume.'
          : 'Goal saved. Return to Details to wrap up later.'
      );
    });
  }

  async editFields(detail, trigger) {
    const dialog = node('dialog', 'assistant-program-hire-dialog project-library-dialog');
    const heading = node('h2', '', `Edit ${detail.row.name}`);
    heading.id = operationKey('edit');
    dialog.setAttribute('aria-labelledby', heading.id);
    const form = node('form');
    form.method = 'dialog';
    const fields = node('div', 'project-library-fields');
    const stage = document.getElementById('projectLibraryStage').cloneNode(true);
    stage.removeAttribute('id');
    stage.value = detail.fields.stage || 'unknown';
    const status = document.getElementById('projectLibraryStatusFilter').cloneNode(true);
    status.removeAttribute('id');
    status.value = detail.fields.status || 'unknown';
    const action = node('input', 'form-control');
    action.maxLength = 240;
    action.value = detail.fields.next_action || '';
    const purpose = node('input', 'form-control');
    purpose.maxLength = 240;
    purpose.value = detail.fields.purpose || '';
    const sessionDate = node('input', 'form-control');
    sessionDate.type = 'date';
    sessionDate.value = detail.fields.session_date || '';
    const releaseDate = node('input', 'form-control');
    releaseDate.type = 'date';
    releaseDate.value = detail.fields.release_date || '';
    const blockers = node('textarea', 'form-control');
    blockers.rows = 3;
    blockers.maxLength = 4096;
    blockers.value = (detail.fields.blockers || []).join('\n');
    const deliverables = node('textarea', 'form-control');
    deliverables.rows = 3;
    deliverables.maxLength = 4096;
    deliverables.value = (detail.fields.deliverables || []).join('\n');
    for (const list of [blockers, deliverables])
      list.addEventListener('input', () => list.setCustomValidity(''));
    const archive = node('select', 'form-select');
    for (const [value, text] of [
      ['', 'Not recorded'],
      ['not_ready', 'Not ready'],
      ['ready', 'Ready for review'],
      ['reviewed', 'Reviewed']
    ]) {
      const option = node('option', '', text);
      option.value = value;
      archive.append(option);
    }
    archive.value = detail.fields.archive_review_state || '';
    const milestones = node('fieldset', 'project-library-milestones');
    milestones.append(node('legend', '', 'Milestones (user-entered, up to 16)'));
    const milestoneList = node('div', 'project-library-milestone-list');
    const addMilestone = node('button', 'modern-btn modern-btn-secondary', 'Add milestone');
    addMilestone.type = 'button';
    const milestoneControls = [];
    const newMilestone = saved => {
      if (milestoneList.children.length >= 16) return;
      const row = node('div', 'project-library-milestone');
      const title = node('input', 'form-control');
      title.required = true;
      title.maxLength = 240;
      title.value = saved?.label || '';
      const date = node('input', 'form-control');
      date.type = 'date';
      date.value = saved?.due_date || '';
      const complete = node('input');
      complete.type = 'checkbox';
      complete.checked = !!saved?.complete;
      for (const [caption, control] of [
        ['Milestone', title],
        ['Due date', date],
        ['Complete', complete]
      ]) {
        const labelNode = node('label', '', caption);
        labelNode.append(control);
        row.append(labelNode);
      }
      const remove = node('button', 'modern-btn modern-btn-secondary', 'Remove milestone');
      remove.type = 'button';
      remove.addEventListener('click', () => {
        row.remove();
        addMilestone.disabled = false;
      });
      row.append(remove);
      milestoneList.append(row);
      milestoneControls.push({
        id: saved?.id || operationKey('milestone'),
        row,
        title,
        date,
        complete
      });
      addMilestone.disabled = milestoneList.children.length >= 16;
      if (!saved) title.focus();
    };
    for (const saved of detail.fields.milestones || []) newMilestone(saved);
    addMilestone.addEventListener('click', () => newMilestone());
    milestones.append(milestoneList, addMilestone);
    const priority = node('select', 'form-select');
    for (const [value, name] of [
      ['', 'Unset'],
      ...Array.from({ length: 6 }, (_, i) => [String(i), String(i)])
    ]) {
      const option = node('option', '', name);
      option.value = value;
      priority.append(option);
    }
    priority.value = detail.fields.priority == null ? '' : String(detail.fields.priority);
    for (const [name, control] of [
      ['Production stage', stage],
      ['Administrative status', status],
      ['Project purpose (user-entered)', purpose],
      ['Next action', action],
      ['Priority', priority],
      ['Session date (user-entered)', sessionDate],
      ['Release date (user-entered)', releaseDate],
      ['Blockers (one per line, up to 16)', blockers],
      ['Deliverables (one per line, up to 16)', deliverables],
      ['Archive review (metadata only; does not move files)', archive]
    ]) {
      const labelNode = node('label', '', name);
      labelNode.append(control);
      fields.append(labelNode);
    }
    fields.append(milestones);
    const actions = node('div', 'assistant-program-dialog-actions');
    const cancel = node('button', 'modern-btn modern-btn-secondary', 'Cancel');
    cancel.type = 'button';
    const save = node('button', 'modern-btn modern-btn-primary', 'Review changes');
    save.type = 'submit';
    cancel.addEventListener('click', () => dialog.close());
    actions.append(cancel, save);
    form.append(
      heading,
      node(
        'p',
        '',
        'Only the fields you changed will be reviewed. Folder observations never set musical progress.'
      ),
      fields,
      actions
    );
    dialog.append(form);
    document.body.append(dialog);
    dialog.addEventListener(
      'close',
      () => {
        dialog.remove();
        trigger?.focus?.();
      },
      { once: true }
    );
    form.addEventListener('submit', event => {
      event.preventDefault();
      const patch = {};
      const listChanges = [];
      for (const [field, input] of [
        ['blockers', blockers],
        ['deliverables', deliverables]
      ]) {
        const entries = input.value
          .split(/\r?\n/)
          .map(value => value.trim())
          .filter(Boolean);
        if (entries.length > 16 || entries.some(value => value.length > 240)) {
          input.setCustomValidity('Use no more than 16 entries, each at most 240 characters.');
          input.reportValidity();
          return;
        }
        if (JSON.stringify(entries) !== JSON.stringify(detail.fields[field] || []))
          listChanges.push([field, entries]);
      }
      for (const [name, input, previous] of [
        ['stage', stage, detail.fields.stage || ''],
        ['status', status, detail.fields.status || ''],
        ['purpose', purpose, detail.fields.purpose || ''],
        ['next_action', action, detail.fields.next_action || ''],
        ['session_date', sessionDate, detail.fields.session_date || ''],
        ['release_date', releaseDate, detail.fields.release_date || ''],
        ['archive_review_state', archive, detail.fields.archive_review_state || '']
      ]) {
        const value = input.value === 'unknown' ? '' : input.value;
        if (value !== previous) patch[name] = value;
      }
      for (const [field, entries] of listChanges) patch[field] = entries;
      const currentMilestones = milestoneControls
        .filter(control => milestoneList.contains(control.row))
        .map(control => ({
          id: control.id,
          label: control.title.value.trim(),
          due_date: control.date.value,
          complete: control.complete.checked
        }));
      const normalizedMilestones = (detail.fields.milestones || []).map(item => ({
        id: item.id,
        label: item.label,
        due_date: item.due_date || '',
        complete: !!item.complete
      }));
      if (JSON.stringify(currentMilestones) !== JSON.stringify(normalizedMilestones))
        patch.milestones = currentMilestones;
      const beforePriority = detail.fields.priority == null ? '' : String(detail.fields.priority);
      if (priority.value !== beforePriority) {
        if (priority.value === '') patch.clear_priority = true;
        else patch.priority = Number(priority.value);
      }
      dialog.close();
      if (!Object.keys(patch).length) {
        this.status('No project fields changed.');
        return;
      }
      void this.saveFields(detail, patch, trigger);
    });
    dialog.showModal();
    cancel.focus();
  }

  async saveFields(detail, patch, trigger) {
    await this.run(trigger, 'Preparing your project notes for review…', async () => {
      const path = `/projects/${encodeURIComponent(detail.row.id)}/fields`;
      const request = { if_fields_revision: detail.row.fields_revision, patch };
      const review = await this.post(`${path}/review`, request);
      if (
        !(await this.confirm(
          'Save these project notes?',
          [
            detail.row.name,
            ...Object.entries(patch).map(
              ([key, value]) =>
                `${label(key)}: ${key === 'clear_priority' ? 'Unset' : key === 'milestones' ? value.map(item => `${item.label}${item.due_date ? ` · ${item.due_date}` : ''} · ${item.complete ? 'complete' : 'open'}`).join('; ') || 'None' : Array.isArray(value) ? value.join('; ') || 'None' : String(value || 'Not set')}`
            ),
            ...(patch.archive_review_state != null
              ? ['Archive review is a note, not a file move.']
              : [])
          ],
          'Save notes',
          trigger
        ))
      )
        return;
      await this.post(`${path}/commit`, {
        ...request,
        review_token: review.token,
        idempotency_key: operationKey('fields'),
        confirm: true
      });
      await this.refresh();
    });
  }
}
